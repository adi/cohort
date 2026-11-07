// Package cohort implements the cohort daemon: it keeps a dedicated
// tmux server in sync with the declarative config at ~/.cohort/config.json.
//
// Layout on the tmux server (socket "cohort"):
//
//	Background  - authoritative home of every agent window
//	Desktop     - the cohort manager as its stable first window, plus
//	              whichever agent windows are linked per config
//
// The daemon watches config.json for changes and reconciles. It is the
// only writer of windows; the cohort manager agent (the sole driver)
// only edits config.json.
package cohort

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cohort/internal/config"
	"cohort/internal/tmux"
)

const (
	// SocketName is the tmux socket name of cohort's private server.
	SocketName = "cohort"
	// SessionBackground is the authoritative session holding all agent windows.
	SessionBackground = "Background"
	// SessionDesktop is the session the human looks at.
	SessionDesktop = "Desktop"
	// tempWindow is the throwaway window used to bootstrap Desktop.
	tempWindow = "_init"
	// gitManagedKey marks a repo folder as created/owned by cohort.
	gitManagedKey = "cohort.managed"
	// fullReconcileEvery forces a drift-correcting reconcile interval.
	fullReconcileEvery = 10 * time.Second
	// pollInterval is how often config changes are detected.
	pollInterval = time.Second
)

// Daemon is the cohort daemon state.
type Daemon struct {
	root    string // ~/.cohort
	cfgPath string // ~/.cohort/config.json
	tmux    *tmux.Server
	log     *log.Logger

	cfg      *config.Config
	cfgStamp int64 // mtime+size fingerprint of the last read config
	lastFull time.Time
}

// Run starts the daemon and blocks until SIGINT/SIGTERM.
// The tmux server is never killed by the daemon; stopping cohort just
// stops reconciliation.
func Run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".cohort")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", root, err)
	}
	if err := seedTmuxConf(filepath.Join(root, "tmux.conf")); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(root, "cohort.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	d := &Daemon{
		root:    root,
		cfgPath: filepath.Join(root, "config.json"),
		tmux:    tmux.New(SocketName, filepath.Join(root, "tmux.conf")),
		log:     log.New(io.MultiWriter(os.Stderr, logFile), "cohort: ", log.LstdFlags),
	}
	return d.loop()
}

func (d *Daemon) loop() error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	if d.reloadConfig() {
		d.reconcile()
		d.lastFull = time.Now()
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-sig:
			d.log.Printf("shutting down (tmux server left running)")
			return nil
		case <-ticker.C:
			if d.reloadConfig() {
				d.reconcile()
				d.lastFull = time.Now()
				continue
			}
			// If the tmux server or a session died, repair immediately.
			if !d.tmux.HasSession(SessionBackground) || !d.tmux.HasSession(SessionDesktop) {
				d.reconcile()
				d.lastFull = time.Now()
				continue
			}
			// Periodic drift correction.
			if time.Since(d.lastFull) >= fullReconcileEvery {
				d.reconcile()
				d.lastFull = time.Now()
			}
		}
	}
}

// reloadConfig loads config.json when it changed on disk. Missing file
// triggers bootstrap (default config with the manager agent).
func (d *Daemon) reloadConfig() bool {
	st, err := os.Stat(d.cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		d.cfg = &config.Config{}
		d.cfg.EnsureManager()
		if err := config.Save(d.cfgPath, d.cfg); err != nil {
			d.log.Printf("ERROR: write default config: %v", err)
			return false
		}
		d.cfgStamp = stamp(st)
		d.log.Printf("bootstrapped default config at %s", d.cfgPath)
		return true
	}
	if err != nil {
		d.log.Printf("ERROR: stat config: %v", err)
		return false
	}
	if s := stamp(st); s == d.cfgStamp {
		return false
	}
	cfg, err := config.Load(d.cfgPath)
	if err != nil {
		// Keep last known-good config; a broken file must not tear
		// down running agents.
		d.log.Printf("ERROR: config rejected, keeping previous state: %v", err)
		d.cfgStamp = stamp(st)
		return false
	}
	if cfg.EnsureManager() {
		if err := config.Save(d.cfgPath, cfg); err != nil {
			d.log.Printf("ERROR: pinning manager agent in config: %v", err)
		}
	}
	d.cfg = cfg
	d.cfgStamp = stamp(st)
	return true
}

// defaultTmuxConf is the initial server configuration. Once seeded,
// the file belongs to the cohort manager agent to customize.
const defaultTmuxConf = `# cohort server configuration.
# This file is passed to tmux with -f so cohort's private server
# (tmux -L cohort) never reads your ~/.tmux.conf.
# Owned by the cohort manager agent: edit freely; restart the server
# (tmux -L cohort kill-server, cohort re-creates it) to apply, or set
# options live with: tmux -L cohort -f ~/.cohort/tmux.conf set-option ...

set -g default-terminal "tmux-256color"
set -g history-limit 100000
set -g mouse on
set -g status-position top
set -g status-interval 5
# Keep agent windows around (as dead panes) when a harness exits, so
# its output can be inspected before the daemon restarts it.
setw -g remain-on-exit on

set -g status-style "bg=colour236,fg=colour145"
set -g status-left "[cohort] #S "
set -g status-right " %H:%M "
set -g window-status-current-style "bg=colour61,fg=colour15"
`

// seedTmuxConf writes the default server config if none exists.
func seedTmuxConf(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte(defaultTmuxConf), 0o644)
}

func stamp(st os.FileInfo) int64 {
	if st == nil {
		return 0
	}
	return st.ModTime().UnixNano() ^ st.Size()
}

// reconcile brings the tmux server into the state declared by config.
// It is idempotent; errors are logged and retried on the next pass.
func (d *Daemon) reconcile() {
	cfg := d.cfg
	if cfg == nil {
		return
	}
	mgr := cfg.Agent(config.ManagerName)
	if mgr == nil {
		// Should not happen (EnsureManager), but be defensive.
		d.log.Printf("ERROR: no %s agent in config", config.ManagerName)
		return
	}

	// 1. Manager window must exist in Background (create the session
	// with it if the session is missing).
	bgWins, err := d.tmux.ListWindows(SessionBackground)
	if err != nil {
		if err := d.ensureAgentWindow(mgr, true); err != nil {
			d.log.Printf("ERROR: bootstrap %s: %v", mgr.Name, err)
			return
		}
		bgWins = []tmux.Window{{Index: 0, Name: mgr.Name}}
	} else if !hasWindow(bgWins, mgr.Name) {
		if err := d.ensureAgentWindow(mgr, false); err != nil {
			d.log.Printf("ERROR: ensure %s window: %v", mgr.Name, err)
			return
		}
	}

	// 2. Desktop session: the manager is its stable first window.
	if !d.tmux.HasSession(SessionDesktop) {
		if err := d.bootstrapDesktop(mgr); err != nil {
			d.log.Printf("ERROR: bootstrap Desktop: %v", err)
			return
		}
	} else {
		d.cleanupTempWindows()
		// Manager must always be linked into Desktop (pinned).
		deskWins, err := d.tmux.ListWindows(SessionDesktop)
		if err != nil {
			d.log.Printf("ERROR: list Desktop windows: %v", err)
			return
		}
		if !hasWindow(deskWins, mgr.Name) {
			idx := firstFreeIndex(deskWins)
			if err := d.tmux.LinkWindow(SessionBackground+":"+mgr.Name,
				SessionDesktop+":"+itoa(idx)); err != nil {
				d.log.Printf("ERROR: link %s into Desktop: %v", mgr.Name, err)
			}
		}
	}

	// 3. Per-agent state: window existence and link state.
	if bgWins, err = d.tmux.ListWindows(SessionBackground); err != nil {
		d.log.Printf("ERROR: list Background windows: %v", err)
		return
	}
	deskWins, err := d.tmux.ListWindows(SessionDesktop)
	if err != nil {
		d.log.Printf("ERROR: list Desktop windows: %v", err)
		return
	}
	nextIdx := lastIndex(deskWins) + 1
	for _, a := range cfg.Agents {
		if !hasWindow(bgWins, a.Name) {
			if err := d.ensureAgentWindow(a, false); err != nil {
				d.log.Printf("ERROR: create agent %q: %v", a.Name, err)
				continue
			}
		}
		linked := a.Linked || a.Name == config.ManagerName
		inDesktop := hasWindow(deskWins, a.Name)
		switch {
		case linked && !inDesktop:
			target := SessionDesktop + ":" + itoa(nextIdx)
			if err := d.tmux.LinkWindow(SessionBackground+":"+a.Name, target); err != nil {
				d.log.Printf("ERROR: link %q into Desktop: %v", a.Name, err)
				continue
			}
			nextIdx++
		case !linked && inDesktop:
			if err := d.tmux.UnlinkWindow(SessionDesktop + ":" + a.Name); err != nil {
				d.log.Printf("ERROR: unlink %q from Desktop: %v", a.Name, err)
			}
		}
	}
	// Compact index gaps left by unlinks (order preserved).
	if err := d.tmux.RenumberWindows(SessionDesktop); err != nil {
		d.log.Printf("ERROR: renumber Desktop: %v", err)
	}

	// 4. Orphans: windows in Background that no config entry claims.
	// Destroy is postponed by design; report only. The folder is
	// likewise never touched.
	for _, w := range bgWins {
		if w.Name == tempWindow {
			continue
		}
		if !cfg.Has(w.Name) {
			d.log.Printf("WARNING: window %q in Background has no config entry; "+
				"left alone (destroy postponed)", w.Name)
		}
	}
}

// bootstrapDesktop creates the Desktop session with the manager window
// as its first (only) window: create a temp window, link the manager
// after it, kill the temp window, renumber.
func (d *Daemon) bootstrapDesktop(mgr *config.Agent) error {
	if err := d.tmux.NewSession(SessionDesktop, tempWindow, d.root, "sleep 1000"); err != nil {
		return err
	}
	if err := d.tmux.LinkWindowAfter(
		SessionBackground+":"+mgr.Name, SessionDesktop+":"+tempWindow); err != nil {
		return err
	}
	if err := d.tmux.KillWindow(SessionDesktop + ":" + tempWindow); err != nil {
		return err
	}
	return d.tmux.RenumberWindows(SessionDesktop)
}

// cleanupTempWindows removes leftover _init windows in Desktop (e.g. if
// a previous bootstrap pass was interrupted).
func (d *Daemon) cleanupTempWindows() {
	d.tmux.KillWindow(SessionDesktop + ":" + tempWindow) // best effort
}

// ensureAgentWindow makes sure the agent's folder exists as a
// cohort-managed local git repo and then creates its tmux window in
// Background. When session is true the Background session is created
// with this window (session bootstrap).
func (d *Daemon) ensureAgentWindow(a *config.Agent, session bool) error {
	folder := a.ResolveFolder(d.root)
	if err := d.ensureFolder(folder); err != nil {
		return err
	}
	if session {
		return d.tmux.NewSession(SessionBackground, a.Name, folder, a.Command)
	}
	return d.tmux.NewWindow(SessionBackground, a.Name, folder, a.Command)
}

// ensureFolder guarantees that folder exists as a local-only git repo.
//
//   - missing  -> created, git init, marked cohort.managed=true
//   - present and marked cohort.managed=true -> kept (agent resume)
//   - anything else -> error: cohort never adopts pre-existing folders
func (d *Daemon) ensureFolder(folder string) error {
	if _, err := os.Stat(filepath.Join(folder, ".git")); errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Stat(folder); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("folder %s already exists and is not a cohort-managed git repo", folder)
		}
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return err
		}
		if err := d.gitInit(folder); err != nil {
			return fmt.Errorf("git init %s: %w", folder, err)
		}
		d.log.Printf("initialized local git repo %s", folder)
		return nil
	} else if err != nil {
		return err
	}
	out, err := git(folder, "config", "--get", gitManagedKey)
	if err != nil || strings.TrimSpace(out) != "true" {
		return fmt.Errorf("folder %s already exists and is not a cohort-managed git repo", folder)
	}
	return nil
}

func (d *Daemon) gitInit(folder string) error {
	if err := gitRun(folder, "init", "-b", "main"); err != nil {
		// Older git without -b.
		if err2 := gitRun(folder, "init"); err2 != nil {
			return err2
		}
	}
	return gitRun(folder, "config", gitManagedKey, "true")
}

func git(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	var out strings.Builder
	cmd := exec.Command("git", full...)
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func gitRun(dir string, args ...string) error {
	_, err := git(dir, args...)
	return err
}

func hasWindow(wins []tmux.Window, name string) bool {
	for _, w := range wins {
		if w.Name == name {
			return true
		}
	}
	return false
}

func lastIndex(wins []tmux.Window) int {
	max := -1
	for _, w := range wins {
		if w.Index > max {
			max = w.Index
		}
	}
	return max
}

func firstFreeIndex(wins []tmux.Window) int {
	used := make(map[int]bool, len(wins))
	for _, w := range wins {
		used[w.Index] = true
	}
	for i := 0; ; i++ {
		if !used[i] {
			return i
		}
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
