package cohort

import (
	"strings"
	"time"

	"cohort/internal/tmux"
)

// Whether a harness is working is read off its screen: claude and codex
// show "esc to interrupt" near the bottom while a turn runs
// and drop it when they stop. Pane titles are no use (claude keeps "✳"
// while working; codex keeps spinning its title when idle).
const (
	workingMarker = "esc to interrupt"
	workingLines  = 10 // bottom non-blank lines searched
)

// workingRecent is how long after a window's last output it is still
// re-read. tmux records activity in whole seconds, so output in the
// same second as the previous read would otherwise go unseen.
var workingRecent = 2 * time.Second

// screenWorking reports whether a captured screen shows a running turn.
func screenWorking(screen string) bool {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < workingLines; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		seen++
		if strings.Contains(strings.ToLower(lines[i]), workingMarker) {
			return true
		}
	}
	return false
}

// markWorking sets WorkingOption on every cohort window to whether its
// harness is working. A screen only changes with output, so a window
// is read only when it had output since it was last read (or within
// workingRecent); quiet windows cost nothing.
func (d *Daemon) markWorking(wins []tmux.Window) {
	now := time.Now()
	seen := map[string]bool{}
	for _, w := range wins {
		if w.Tag == "" || seen[w.ID] {
			continue
		}
		seen[w.ID] = true
		last, read := d.activity[w.ID]
		recent := now.Sub(time.Unix(w.Activity, 0)) <= workingRecent
		if read && last == w.Activity && !recent && w.Working != "" {
			continue
		}
		d.activity[w.ID] = w.Activity
		working := false
		if !w.Dead {
			screen, err := d.tmux.CapturePane(w.ID)
			if err != nil {
				continue // the window may have just gone; next tick
			}
			working = screenWorking(screen)
		}
		want := "0"
		if working {
			want = "1"
		}
		if want != w.Working {
			if err := d.tmux.SetWindowOptions(w.ID, map[string]string{tmux.WorkingOption: want}); err != nil {
				d.issue("tmux", "mark %s working: %v", w.ID, err)
			}
		}
	}
	for id := range d.activity {
		if !seen[id] {
			delete(d.activity, id)
		}
	}
}
