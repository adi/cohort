package cohort

import (
	"strings"
	"time"

	"cohort/internal/tmux"
)

// What a harness is doing is read off the bottom of its screen. Pane
// titles are no use: claude keeps "✳" while working, codex keeps
// spinning its title when idle.
//
//   - working: claude and codex show "esc to interrupt" while a turn runs.
//   - input: claude's permission and question prompts end with "Esc to
//     cancel"; codex asks "Would you like to run the following
//     command?" (or make edits) above "Press enter to confirm or ...".
//   - idle: anything else, including a finished turn.
const (
	stateWorking = "working"
	stateInput   = "input"
	stateIdle    = "idle"
	stateLines   = 10 // bottom non-blank lines searched
)

var (
	workingMarkers = []string{"esc to interrupt"}
	inputMarkers   = []string{"esc to cancel", "to confirm or", "would you like to"}
)

// stateRecent is how long after a window's last output it is still
// re-read. tmux records activity in whole seconds, so output in the
// same second as the previous read would otherwise go unseen.
var stateRecent = 2 * time.Second

// screenState says what a captured screen shows the harness doing.
// A question outranks a running turn.
func screenState(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	state, seen := stateIdle, 0
	for i := len(lines) - 1; i >= 0 && seen < stateLines; i-- {
		line := strings.ToLower(lines[i])
		if strings.TrimSpace(line) == "" {
			continue
		}
		seen++
		for _, m := range inputMarkers {
			if strings.Contains(line, m) {
				return stateInput
			}
		}
		for _, m := range workingMarkers {
			if strings.Contains(line, m) {
				state = stateWorking
			}
		}
	}
	return state
}

// markStates records in StateOption what every cohort window's harness
// is doing. A screen only changes with output, so a window is read
// only when it had output since it was last read (or within
// stateRecent); quiet windows cost nothing.
func (d *Daemon) markStates(wins []tmux.Window) {
	now := time.Now()
	seen := map[string]bool{}
	for _, w := range wins {
		if w.Tag == "" || seen[w.ID] {
			continue
		}
		seen[w.ID] = true
		last, read := d.activity[w.ID]
		recent := now.Sub(time.Unix(w.Activity, 0)) <= stateRecent
		if read && last == w.Activity && !recent && w.State != "" {
			continue
		}
		d.activity[w.ID] = w.Activity
		state := stateIdle
		if !w.Dead {
			screen, err := d.tmux.CapturePane(w.ID)
			if err != nil {
				continue // the window may have just gone; next tick
			}
			state = screenState(screen)
		}
		if state != w.State {
			if err := d.tmux.SetWindowOptions(w.ID, map[string]string{tmux.StateOption: state}); err != nil {
				d.issue("tmux", "mark state of %s: %v", w.ID, err)
			}
		}
	}
	for id := range d.activity {
		if !seen[id] {
			delete(d.activity, id)
		}
	}
}
