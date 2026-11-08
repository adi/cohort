package cohort

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// launchdLabel is the label of cohort's launchd user agent, installed
// as ~/Library/LaunchAgents/<label>.plist (contrib/cohort.plist).
const launchdLabel = "com.github.adi.cohort"

// restartHint restarts the daemon only: launchd stops the job's
// process group, which the tmux server left when it daemonized, so
// every window keeps running and the new daemon adopts them.
const restartHint = "launchctl kickstart -k gui/$(id -u)/" + launchdLabel

// refreshService: the launchd plist is generated from a template at
// install time (install.sh); upgrade leaves it alone.
func refreshService(tag string, sums map[string]string) error { return nil }

// startService starts the cohort launchd agent, loading it first when
// it isn't loaded (e.g. it was booted out, or installed but not yet
// loaded in this login session).
func startService() error {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	target := domain + "/" + launchdLabel
	fmt.Println("cohort is not running; starting it: launchctl kickstart " + target)
	if exec.Command("launchctl", "print", target).Run() != nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		plist := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
		if out, err := exec.Command("launchctl", "bootstrap", domain, plist).CombinedOutput(); err != nil {
			return fmt.Errorf("load the cohort launchd agent %s (is contrib/cohort.plist installed?): %v: %s",
				plist, err, strings.TrimSpace(string(out)))
		}
		// bootstrap starts it already (RunAtLoad).
		return nil
	}
	if out, err := exec.Command("launchctl", "kickstart", target).CombinedOutput(); err != nil {
		return fmt.Errorf("start the cohort launchd agent: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
