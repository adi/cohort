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
	"bytes"
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
			// If the tmux server, the Desktop session, or (while any
			// agent is configured) the Background session died, repair
			// immediately.
			nAgents := 0
			if d.cfg != nil {
				nAgents = len(d.cfg.Agents)
			}
			if !d.tmux.HasSession(SessionDesktop) ||
				(nAgents > 0 && !d.tmux.HasSession(SessionBackground)) {
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

// reloadConfig loads config.json when it changed on disk. A missing
// file triggers bootstrap (default config with the manager settings).
// Schema upgrades (e.g. a legacy manager agents entry hoisted to the
// top-level "manager" setting) are persisted back to the file.
func (d *Daemon) reloadConfig() bool {
	st, err := os.Stat(d.cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		d.cfg = &config.Config{Manager: config.DefaultManagerCommand, Agents: []*config.Agent{}}
		if err := config.Save(d.cfgPath, d.cfg); err != nil {
			d.log.Printf("ERROR: write default config: %v", err)
			return false
		}
		if st, err := os.Stat(d.cfgPath); err == nil {
			d.cfgStamp = stamp(st)
		}
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
	raw, err := os.ReadFile(d.cfgPath)
	if err != nil {
		d.log.Printf("ERROR: read config: %v", err)
		return false
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		// Keep last known-good config; a broken file must not tear
		// down running agents.
		d.log.Printf("ERROR: config rejected, keeping previous state: %v", err)
		d.cfgStamp = stamp(st)
		return false
	}
	d.cfg = cfg
	d.cfgStamp = stamp(st)
	// If defaults/migration changed the structure, persist the
	// normalized form so the file on disk matches what we run.
	if norm := config.Marshal(cfg); !bytes.Equal(norm, raw) {
		if err := os.WriteFile(d.cfgPath, norm, 0o644); err != nil {
			d.log.Printf("ERROR: persist normalized config: %v", err)
		} else {
			if st, err := os.Stat(d.cfgPath); err == nil {
				d.cfgStamp = stamp(st)
			}
			d.log.Printf("normalized config (defaults/migration applied)")
		}
	}
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
	mgrName := config.ManagerName
	mgrFolder := config.ManagerFolder(d.root)
	mgrCmd := cfg.ManagerCommand()

	// 1. The manager window is stable in Desktop only; it must never
	// exist in Background. Remove legacy/rogue copies.
	if bgWins, err := d.tmux.ListWindows(SessionBackground); err == nil {
		for _, w := range bgWins {
			if w.Name != mgrName {
				continue
			}
			// Target by index: several same-named windows may exist.
			target := SessionBackground + ":" + itoa(w.Index)
			if w.Linked {
				// Old layout: manager window created in Background and
				// linked into Desktop. Drop the Background link; the
				// window stays (stable) in Desktop.
				if err := d.tmux.UnlinkWindow(target); err != nil {
					d.log.Printf("ERROR: unlink %s from Background: %v", mgrName, err)
				}
			} else {
				// Rogue manager window living only in Background.
				if err := d.tmux.KillWindow(target); err != nil {
					d.log.Printf("ERROR: destroy rogue %s window in Background: %v", mgrName, err)
				}
			}
		}
	}

	// 2. Desktop session: the manager is its stable first window.
	if !d.tmux.HasSession(SessionDesktop) {
		if err := d.ensureFolder(mgrFolder); err != nil {
			d.log.Printf("ERROR: ensure %s folder: %v", mgrName, err)
			return
		}
		if err := d.tmux.NewSession(SessionDesktop, mgrName, mgrFolder, mgrCmd); err != nil {
			d.log.Printf("ERROR: bootstrap Desktop with %s: %v", mgrName, err)
			return
		}
	} else {
		deskWins, err := d.tmux.ListWindows(SessionDesktop)
		if err != nil {
			d.log.Printf("ERROR: list Desktop windows: %v", err)
			return
		}
		if !hasWindow(deskWins, mgrName) {
			// The manager window was killed; recreate it as the first
			// window (harness restart).
			if err := d.ensureFolder(mgrFolder); err != nil {
				d.log.Printf("ERROR: ensure %s folder: %v", mgrName, err)
				return
			}
			front := 0
			if len(deskWins) > 0 {
				front = deskWins[0].Index
			}
			if err := d.tmux.NewWindowBefore(SessionDesktop+":"+itoa(front),
				mgrName, mgrFolder, mgrCmd); err != nil {
				d.log.Printf("ERROR: recreate %s window in Desktop: %v", mgrName, err)
				return
			}
		}
	}

	// 3. Background session: the authoritative home of the agent
	// windows. Created (with the first agent's window) as soon as any
	// agent is configured; with no agents there is nothing to host.
	if _, err := d.tmux.ListWindows(SessionBackground); err != nil && len(cfg.Agents) > 0 {
		if err := d.ensureAgentWindow(cfg.Agents[0], true); err != nil {
			d.log.Printf("ERROR: bootstrap Background: %v", err)
			return
		}
	}

	// 4. Per-agent state: window existence and link state.
	if len(cfg.Agents) == 0 {
		return
	}
	bgWins, err := d.tmux.ListWindows(SessionBackground)
	if err != nil {
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
		inDesktop := hasWindow(deskWins, a.Name)
		switch {
		case a.Linked && !inDesktop:
			target := SessionDesktop + ":" + itoa(nextIdx)
			if err := d.tmux.LinkWindow(SessionBackground+":"+a.Name, target); err != nil {
				d.log.Printf("ERROR: link %q into Desktop: %v", a.Name, err)
				continue
			}
			nextIdx++
		case !a.Linked && inDesktop:
			if err := d.tmux.UnlinkWindow(SessionDesktop + ":" + a.Name); err != nil {
				d.log.Printf("ERROR: unlink %q from Desktop: %v", a.Name, err)
			}
		}
	}
	// Compact index gaps left by unlinks (order preserved).
	if err := d.tmux.RenumberWindows(SessionDesktop); err != nil {
		d.log.Printf("ERROR: renumber Desktop: %v", err)
	}

	// 5. Orphans: windows in Background that no config entry claims.
	// Destroy is postponed by design; report only. The folder is
	// likewise never touched.
	for _, w := range bgWins {
		if !cfg.Has(w.Name) {
			d.log.Printf("WARNING: window %q in Background has no config entry; "+
				"left alone (destroy postponed)", w.Name)
		}
	}
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
		return d.tmux.NewSession(SessionBackground, a.Name, folder, a.StartupCommand())
	}
	return d.tmux.NewWindow(SessionBackground, a.Name, folder, a.StartupCommand())
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

func itoa(i int) string { return strconv.Itoa(i) }
