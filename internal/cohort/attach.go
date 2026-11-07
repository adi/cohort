package cohort

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cohort/internal/tmux"
)

// attachWait is how long attach waits for the daemon to bring up the
// Desktop session.
const attachWait = 15 * time.Second

// attach puts the user's terminal on the Desktop session: it starts the
// cohort service (systemd or launchd) when no daemon is running, waits for Desktop,
// then attaches (or switches, when already inside cohort's tmux).
func attach(root string) error {
	running, err := daemonRunning(root)
	if err != nil {
		return err
	}
	if !running {
		if err := startService(); err != nil {
			return err
		}
	}
	srv := tmux.New(SocketName, filepath.Join(root, "tmux.conf"))
	deadline := time.Now().Add(attachWait)
	for !srv.HasSession(SessionDesktop) {
		if time.Now().After(deadline) {
			return fmt.Errorf("no Desktop session after %s; see %s and %s",
				attachWait, filepath.Join(root, "status.json"), filepath.Join(root, "cohort.log"))
		}
		time.Sleep(200 * time.Millisecond)
	}

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	args := append([]string{"tmux"}, srv.SocketArgs()...)
	if insideServer(srv) {
		args = append(args, "switch-client", "-t", SessionDesktop)
		out, err := exec.Command(tmuxPath, args[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	// Replace this process with the tmux client. TMUX is dropped so
	// attaching from inside another tmux works (as a nested client).
	args = append(args, "attach-session", "-t", SessionDesktop)
	return syscall.Exec(tmuxPath, args, envWithout(os.Environ(), "TMUX"))
}

// daemonRunning reports whether a daemon holds the lock.
func daemonRunning(root string) (bool, error) {
	lock, err := os.OpenFile(filepath.Join(root, "cohort.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	// Free: nobody runs. Release it at once for the daemon to take.
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return false, nil
}

// insideServer reports whether this process runs inside a pane of
// cohort's own tmux server ($TMUX names its socket).
func insideServer(srv *tmux.Server) bool {
	env := os.Getenv("TMUX")
	if env == "" {
		return false
	}
	sock := strings.SplitN(env, ",", 2)[0]
	want := srv.Socket
	if !strings.Contains(want, "/") {
		want = filepath.Join(tmuxTmpdir(), want)
	}
	return sock == want || resolved(sock) == resolved(want)
}

// tmuxTmpdir is the directory tmux keeps -L sockets in.
func tmuxTmpdir() string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()))
}

// resolved is path with symlinks resolved, or path when that fails.
// tmux resolves its socket directory, so on macOS, where /tmp is a link
// to /private/tmp, $TMUX names /private/tmp/tmux-<uid>/<socket>.
func resolved(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

func envWithout(env []string, name string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return out
}
