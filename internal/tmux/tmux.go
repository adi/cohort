// Package tmux is a thin wrapper around the tmux binary operating on
// a dedicated, non-default tmux server (tmux -L <socket>).
//
// Windows are addressed by their server-unique window id (@N) or by
// session:index taken from a fresh listing, never by name: tmux
// resolves a name target as an index first and as a pattern last.
package tmux

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// TagOption is the window user option cohort uses to mark the windows
// it owns. Its value is the agent (or manager) name.
const TagOption = "@cohort"

// Window is one winlink: a window as seen from one session. A window
// linked into two sessions appears twice, with the same ID.
type Window struct {
	Session string
	Index   int
	ID      string // @N, unique per server
	Name    string
	Tag     string // value of TagOption, "" if not a cohort window
	Color   string // value of @cohort_color, "" if unset
	Dead    bool   // the active pane's command has exited (remain-on-exit)
}

// Server is a handle to a tmux server identified by its socket name.
// ConfigFile is the server's private tmux configuration, passed with
// -f so cohort's server never reads the user's ~/.tmux.conf. It is
// only read when the server starts; cohort owns server startup.
type Server struct {
	Socket     string
	ConfigFile string
}

// New returns a Server for the given socket name, started with the
// given private config file.
func New(socket, configFile string) *Server {
	return &Server{Socket: socket, ConfigFile: configFile}
}

// SocketArgs selects the server: -L <name>, or -S <path> when Socket
// is a path (used by tests to keep sockets in a temp dir).
func (s *Server) SocketArgs() []string {
	if strings.Contains(s.Socket, "/") {
		return []string{"-S", s.Socket}
	}
	return []string{"-L", s.Socket}
}

// run executes a tmux command against the server and returns stdout.
func (s *Server) run(args ...string) (string, error) {
	full := append(s.SocketArgs(), append([]string{"-f", s.ConfigFile}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("tmux", full...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("tmux %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// HasSession reports whether the named session exists on the server.
// A missing server counts as "no session". The "=" prefix makes tmux
// match the name exactly instead of as a prefix.
func (s *Server) HasSession(session string) bool {
	_, err := s.run("has-session", "-t", "="+session)
	return err == nil
}

const listFormat = "#{session_name}\t#{window_index}\t#{window_id}\t#{" + TagOption + "}\t#{@cohort_color}\t#{pane_dead}\t#{window_name}"

// ListAll returns every winlink on the server. A server that is not
// running has no windows.
func (s *Server) ListAll() ([]Window, error) {
	out, err := s.run("list-windows", "-a", "-F", listFormat)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") ||
			strings.Contains(err.Error(), "error connecting") {
			return nil, nil
		}
		return nil, err
	}
	var wins []Window
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "\t", 7)
		if len(parts) != 7 {
			continue
		}
		idx, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		wins = append(wins, Window{Session: parts[0], Index: idx, ID: parts[2], Tag: parts[3], Color: parts[4], Dead: parts[5] == "1", Name: parts[6]})
	}
	return wins, nil
}

// NewSession creates a detached session whose only window is named
// name, runs argv from dir and carries the window options opts.
// Returns the window id.
func (s *Server) NewSession(session, name, dir string, argv []string, opts map[string]string) (string, error) {
	return s.create(opts, append([]string{"new-session", "-d", "-s", session}, windowArgs(name, dir, argv)...))
}

// NewWindow appends a window to session. Returns the window id.
func (s *Server) NewWindow(session, name, dir string, argv []string, opts map[string]string) (string, error) {
	return s.create(opts, append([]string{"new-window", "-d", "-t", session + ":"}, windowArgs(name, dir, argv)...))
}

// NewWindowBefore inserts a window before target (session:index).
// Returns the window id.
func (s *Server) NewWindowBefore(target, name, dir string, argv []string, opts map[string]string) (string, error) {
	return s.create(opts, append([]string{"new-window", "-d", "-b", "-t", target}, windowArgs(name, dir, argv)...))
}

// RespawnWindow restarts the dead command of window id (@N) with argv,
// run from dir. The window keeps its id, links and options.
func (s *Server) RespawnWindow(id, dir string, argv []string) error {
	_, err := s.run(append([]string{"respawn-window", "-t", id, "-c", dir, "--"}, argv...)...)
	return err
}

func windowArgs(name, dir string, argv []string) []string {
	return append([]string{"-P", "-F", "#{window_id}", "-n", name, "-c", dir, "--"}, argv...)
}

func (s *Server) create(opts map[string]string, args []string) (string, error) {
	out, err := s.run(args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	return id, s.SetWindowOptions(id, opts)
}

// SetWindowOptions sets window options on the window with the given id,
// in one tmux call.
func (s *Server) SetWindowOptions(id string, opts map[string]string) error {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var args []string
	for _, k := range keys {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "set-option", "-w", "-t", id, k, opts[k])
	}
	if len(args) == 0 {
		return nil
	}
	_, err := s.run(args...)
	return err
}

// LinkWindow links the window with the given id into session at
// index.
func (s *Server) LinkWindow(id, session string, index int) error {
	_, err := s.run("link-window", "-d", "-s", id, "-t", session+":"+strconv.Itoa(index))
	return err
}

// The functions below address a window as session:@id, which selects
// that window's winlink in that session. Unlike session:index it stays
// right when tmux renumbers windows (renumber-windows on).

// UnlinkWindow unlinks window id from session. tmux refuses if that is
// the window's only session, so this never destroys a window.
func (s *Server) UnlinkWindow(session, id string) error {
	_, err := s.run("unlink-window", "-t", session+":"+id)
	return err
}

// KillWindow destroys window id (in every session it is linked into).
func (s *Server) KillWindow(session, id string) error {
	_, err := s.run("kill-window", "-t", session+":"+id)
	return err
}

// MoveWindowBefore moves window id's winlink in session in front of
// session:to.
func (s *Server) MoveWindowBefore(session, id string, to int) error {
	_, err := s.run("move-window", "-b", "-s", session+":"+id, "-t", session+":"+strconv.Itoa(to))
	return err
}

// RenumberWindows renumbers the windows of a session without gaps.
func (s *Server) RenumberWindows(session string) error {
	_, err := s.run("move-window", "-r", "-t", session+":")
	return err
}
