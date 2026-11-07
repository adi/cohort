// Package tmux is a thin wrapper around the tmux binary operating on
// a dedicated, non-default tmux server (tmux -L <socket>).
package tmux

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Window describes a window in a session.
type Window struct {
	Index int
	Name  string
	// Linked is 1 if the window is linked to more than one session.
	Linked bool
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

// run executes a tmux command against the server and returns stdout.
func (s *Server) run(args ...string) (string, error) {
	full := append([]string{"-L", s.Socket, "-f", s.ConfigFile}, args...)
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
// A missing server counts as "no session".
func (s *Server) HasSession(session string) bool {
	_, err := s.run("has-session", "-t", session)
	return err == nil
}

// NewSession creates a detached session whose first (and only) window
// is named window and runs cmd from dir.
func (s *Server) NewSession(session, window, dir, cmd string) error {
	_, err := s.run("new-session", "-d", "-s", session, "-n", window, "-c", dir, "--", cmd)
	return err
}

// NewWindow creates a new detached window named window in session,
// running cmd from dir.
func (s *Server) NewWindow(session, window, dir, cmd string) error {
	_, err := s.run("new-window", "-d", "-t", session, "-n", window, "-c", dir, "--", cmd)
	return err
}

// KillWindow destroys the window at target (e.g. "Desktop:foo").
// If the window is linked into multiple sessions, it is destroyed in
// all of them.
func (s *Server) KillWindow(target string) error {
	_, err := s.run("kill-window", "-t", target)
	return err
}

// NewWindowBefore creates a new detached window named window, inserted
// before the given target window (e.g. "Desktop:0"), running cmd from
// dir. Used to keep a window at the front of a session.
func (s *Server) NewWindowBefore(target, window, dir, cmd string) error {
	_, err := s.run("new-window", "-d", "-b", "-t", target, "-n", window, "-c", dir, "--", cmd)
	return err
}

// LinkWindow links src (e.g. "Background:foo") into the session of
// target at the given window index (e.g. "Desktop:3"). The target
// index must not already be in use.
func (s *Server) LinkWindow(src, target string) error {
	_, err := s.run("link-window", "-s", src, "-t", target)
	return err
}

// UnlinkWindow unlinks the window at target (e.g. "Desktop:foo") from
// its session. The window remains linked in its other sessions
// (Background), so it is not destroyed.
func (s *Server) UnlinkWindow(target string) error {
	_, err := s.run("unlink-window", "-t", target)
	return err
}

// RenumberWindows renumbers the windows of a session without gaps.
func (s *Server) RenumberWindows(session string) error {
	_, err := s.run("move-window", "-r", "-t", session)
	return err
}

// ListWindows returns the windows of a session, in index order.
func (s *Server) ListWindows(session string) ([]Window, error) {
	out, err := s.run("list-windows", "-t", session,
		"-F", "#{window_index}\t#{window_name}\t#{window_linked}")
	if err != nil {
		return nil, err
	}
	var wins []Window
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		idx, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		wins = append(wins, Window{Index: idx, Name: parts[1], Linked: parts[2] == "1"})
	}
	return wins, nil
}
