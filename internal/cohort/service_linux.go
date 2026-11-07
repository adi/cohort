package cohort

import (
	"fmt"
	"os"
	"os/exec"
)

// restartHint is how the user restarts the daemon (and every agent).
const restartHint = "systemctl --user restart cohort"

// killServerOnExit: systemd stops the tmux server with the daemon
// already (it is in the unit's cgroup), so cohort leaves it to systemd.
const killServerOnExit = false

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
