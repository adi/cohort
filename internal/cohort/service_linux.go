package cohort

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// restartHint restarts the daemon only; agents keep running.
const restartHint = "systemctl --user restart cohort"

// startService starts the cohort systemd user service.
func startService() error {
	fmt.Println("cohort is not running; starting it: systemctl --user start cohort")
	cmd := exec.Command("systemctl", "--user", "start", "cohort")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start the cohort service (is contrib/cohort.service installed?): %w", err)
	}
	return nil
}

// refreshService installs the release's systemd unit when it differs
// from the installed one, and reloads systemd so the next stop or
// restart uses it.
func refreshService(tag string, sums map[string]string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unit := filepath.Join(home, ".config", "systemd", "user", "cohort.service")
	old, err := os.ReadFile(unit)
	if err != nil {
		return nil // not installed as a user service; nothing to refresh
	}
	data, err := fetchVerified(tag, "cohort.service", sums)
	if err != nil {
		return err
	}
	if bytes.Equal(old, data) {
		return nil
	}
	if err := writeAtomic(unit, data, 0o644); err != nil {
		return err
	}
	if err := daemonReload(); err != nil {
		return err
	}
	fmt.Printf("updated %s\n", unit)
	return nil
}

// daemonReload makes systemd re-read unit files; a variable for tests.
var daemonReload = func() error {
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
