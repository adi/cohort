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

const listFormat = "#{session_name}\t#{window_index}\t#{window_id}\t#{" + TagOption + "}\t#{window_name}"

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
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) != 5 {
			continue
		}
		idx, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		wins = append(wins, Window{Session: parts[0], Index: idx, ID: parts[2], Tag: parts[3], Name: parts[4]})
	}
	return wins, nil
}

// NewSession creates a detached session whose only window is named
// name, tagged tag, and runs argv from dir. Returns the window id.
func (s *Server) NewSession(session, name, tag, dir string, argv []string) (string, error) {
	return s.create(tag, append([]string{"new-session", "-d", "-s", session}, s.windowArgs(name, dir, argv)...))
}

// NewWindow appends a window to session. Returns the window id.
func (s *Server) NewWindow(session, name, tag, dir string, argv []string) (string, error) {
	return s.create(tag, append([]string{"new-window", "-d", "-t", session + ":"}, s.windowArgs(name, dir, argv)...))
}

// NewWindowBefore inserts a window before target (session:index).
// Returns the window id.
func (s *Server) NewWindowBefore(target, name, tag, dir string, argv []string) (string, error) {
	return s.create(tag, append([]string{"new-window", "-d", "-b", "-t", target}, s.windowArgs(name, dir, argv)...))
}

func (s *Server) windowArgs(name, dir string, argv []string) []string {
	return append([]string{"-P", "-F", "#{window_id}", "-n", name, "-c", dir, "--"}, argv...)
}

func (s *Server) create(tag string, args []string) (string, error) {
	out, err := s.run(args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if _, err := s.run("set-option", "-w", "-t", id, TagOption, tag); err != nil {
		return id, err
	}
	return id, nil
}

// LinkWindow links the window with the given id into session at
// index.
func (s *Server) LinkWindow(id, session string, index int) error {
	_, err := s.run("link-window", "-d", "-s", id, "-t", session+":"+strconv.Itoa(index))
	return err
}

// UnlinkWindow unlinks the winlink at session:index. tmux refuses if
// that is the window's only session, so this never destroys a window.
func (s *Server) UnlinkWindow(session string, index int) error {
	_, err := s.run("unlink-window", "-t", session+":"+strconv.Itoa(index))
	return err
}

// KillWindow destroys the window at session:index (in every session it
// is linked into).
func (s *Server) KillWindow(session string, index int) error {
	_, err := s.run("kill-window", "-t", session+":"+strconv.Itoa(index))
	return err
}

// MoveWindowBefore moves the winlink at session:from in front of
// session:to.
func (s *Server) MoveWindowBefore(session string, from, to int) error {
	_, err := s.run("move-window", "-b", "-s", session+":"+strconv.Itoa(from), "-t", session+":"+strconv.Itoa(to))
	return err
}

// RenumberWindows renumbers the windows of a session without gaps.
func (s *Server) RenumberWindows(session string) error {
	_, err := s.run("move-window", "-r", "-t", session+":")
	return err
}
