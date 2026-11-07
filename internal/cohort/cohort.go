// Package cohort implements the cohort daemon: it keeps a dedicated
// tmux server in sync with the declarative config at ~/.cohort/config.json.
//
// Layout on the tmux server (socket "cohort"):
//
//	Desktop     - the cohort manager as its first window, plus the agent
//	              windows whose config says "linked": true
//	Background  - the home of every agent window (never the manager)
//
// config.json is the single source of truth: the daemon reads it every
// second and reconciles tmux to match. It never writes it, except to
// create it at first start. Problems are logged once when they appear
// and once when they clear, and mirrored to ~/.cohort/status.json.
package cohort

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"cohort/internal/config"
	"cohort/internal/tmux"
)

const (
	// SocketName is the tmux socket name of cohort's private server.
	SocketName = "cohort"
	// SessionBackground is the session holding all agent windows.
	SessionBackground = "Background"
	// SessionDesktop is the session the human looks at.
	SessionDesktop = "Desktop"
	// gitManagedKey marks a repo folder as created/owned by cohort.
	gitManagedKey = "cohort.managed"
	// tickInterval is how often config is re-read and tmux reconciled.
	tickInterval = time.Second
)

// Daemon is the cohort daemon state.
type Daemon struct {
	root    string // ~/.cohort
	cfgPath string // ~/.cohort/config.json
	tmux    *tmux.Server
	log     *log.Logger
	// shell is the argv prefix every window command is run with; the
	// command string is appended as the last argument.
	shell []string

	cfg    *config.Config // last valid config
	cfgRaw []byte         // bytes of the last config file read
	cfgErr string         // why cfgRaw was rejected, "" if valid

	issues     map[string]string // problems found in the current tick
	lastIssues map[string]string // problems reported after the last tick
	wroteOnce  bool              // status.json written by this process
}

func newDaemon(root, socket string, logw io.Writer) *Daemon {
	return &Daemon{
		root:       root,
		cfgPath:    filepath.Join(root, "config.json"),
		tmux:       tmux.New(socket, filepath.Join(root, "tmux.conf")),
		log:        log.New(logw, "cohort: ", log.LstdFlags),
		shell:      loginShell(),
		lastIssues: map[string]string{},
	}
}

// loginShell runs commands through the user's shell as an interactive
// login shell, so windows get the same environment (PATH, API keys) as
// a terminal, even when cohort itself runs under systemd.
func loginShell() []string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return []string{sh, "-lic"}
}

func (d *Daemon) argv(command string) []string {
	return append(append([]string{}, d.shell...), command)
}

// Run starts the daemon and blocks until SIGINT/SIGTERM.
func Run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".cohort")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", root, err)
	}
	wrote, err := firstStart(filepath.Join(root, "config.json"))
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(root, "cohort.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if wrote {
			fmt.Println("cohort is already running and will pick up the new config")
			return nil
		}
		return errors.New("another cohort daemon is already running")
	}
	if err := seedServerFiles(root); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(root, "cohort.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	return newDaemon(root, SocketName, io.MultiWriter(os.Stderr, logFile)).loop()
}

// firstStart asks for the manager command when there is no config yet
// and cohort was started from a terminal. Without a terminal (systemd)
// the daemon starts anyway and waits for the file to appear.
// It reports whether it wrote the config.
func firstStart(cfgPath string) (bool, error) {
	if _, err := os.Stat(cfgPath); !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if !isTerminal(os.Stdin) {
		return false, nil
	}
	fmt.Printf("cohort first start: no %s yet.\n", cfgPath)
	fmt.Println("The cohort manager is claude code running in ~/.cohort/cohort-manager.")
	fmt.Println("Enter the command that starts it (it is used for every start, fresh or resumed),")
	fmt.Println("e.g.  claude --continue || claude")
	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("manager command: ")
		line, err := in.ReadString('\n')
		cmd := strings.TrimSpace(line)
		if cmd != "" {
			if err := config.WriteNew(cfgPath, cmd); err != nil {
				return false, err
			}
			fmt.Printf("wrote %s\n", cfgPath)
			return true, nil
		}
		if err != nil {
			return false, errors.New("no manager command given")
		}
	}
}

// isTerminal reports whether f is a terminal (/dev/null is a character
// device too, so a mode check is not enough).
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}

func (d *Daemon) loop() error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		d.tick()
		select {
		case <-sig:
			d.log.Printf("shutting down")
			return nil
		case <-ticker.C:
		}
	}
}

// tick re-reads the config and reconciles tmux, then reports the
// problems that appeared, changed or cleared since the last tick.
func (d *Daemon) tick() {
	d.issues = map[string]string{}
	d.reloadConfig()
	if d.cfgErr != "" {
		d.issue("config", d.cfgErr)
	}
	if d.cfg != nil {
		d.reconcile()
	}
	d.report()
}

// issue records a problem found during the current tick.
func (d *Daemon) issue(key, format string, args ...any) {
	d.issues[key] = fmt.Sprintf(format, args...)
}

// report logs issues that are new or changed, and the ones that went
// away, and rewrites status.json when anything changed.
func (d *Daemon) report() {
	changed := false
	keys := make([]string, 0, len(d.issues))
	for k := range d.issues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if d.lastIssues[k] != d.issues[k] {
			d.log.Printf("ERROR [%s] %s", k, d.issues[k])
			changed = true
		}
	}
	for k := range d.lastIssues {
		if _, ok := d.issues[k]; !ok {
			d.log.Printf("resolved [%s]", k)
			changed = true
		}
	}
	d.lastIssues = d.issues
	if changed || !d.wroteOnce {
		d.writeStatus()
		d.wroteOnce = true
	}
}

// writeStatus mirrors the current issues to status.json, for the
// manager agent to read.
func (d *Daemon) writeStatus() {
	st := struct {
		Updated time.Time         `json:"updated"`
		Issues  map[string]string `json:"issues"`
	}{time.Now().Round(time.Second), d.issues}
	data, _ := json.MarshalIndent(st, "", "  ")
	path := filepath.Join(d.root, "status.json")
	if err := os.WriteFile(path+".tmp", append(data, '\n'), 0o644); err == nil {
		os.Rename(path+".tmp", path)
	}
}

// reloadConfig re-reads config.json when its content changed. An
// invalid file is rejected and the last valid config is kept, so a
// broken edit never tears down running agents.
func (d *Daemon) reloadConfig() {
	raw, err := os.ReadFile(d.cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		d.cfgRaw = nil
		if d.cfg == nil {
			d.cfgErr = fmt.Sprintf("%s does not exist; run cohort once from a terminal to set the manager command", d.cfgPath)
		} else {
			d.cfgErr = fmt.Sprintf("%s was deleted; keeping the last valid config", d.cfgPath)
		}
		return
	}
	if err != nil {
		d.cfgErr = fmt.Sprintf("read config: %v", err)
		return
	}
	if d.cfgRaw != nil && bytes.Equal(raw, d.cfgRaw) {
		return
	}
	d.cfgRaw = raw
	cfg, err := config.Parse(raw)
	if err != nil {
		d.cfgErr = fmt.Sprintf("config rejected, keeping the last valid one: %v", err)
		return
	}
	d.cfg, d.cfgErr = cfg, ""
	d.log.Printf("config loaded: %d agent(s)", len(cfg.Agents))
}

// reconcile brings the tmux server into the state declared by config.
// It is idempotent; failures are recorded as issues and retried on the
// next tick. Windows are matched by their @cohort tag and window id,
// never by name.
func (d *Daemon) reconcile() {
	if !d.reconcileManager() {
		return
	}
	canonical, ok := d.reconcileAgents()
	if !ok {
		return
	}
	d.reconcileDesktop(canonical)
	if wins, ok := d.list(); ok {
		d.backfillColors(wins)
	}
}

// list returns all winlinks, recording an issue on failure.
func (d *Daemon) list() ([]tmux.Window, bool) {
	wins, err := d.tmux.ListAll()
	if err != nil {
		d.issue("tmux", "list windows: %v", err)
		return nil, false
	}
	return wins, true
}

// reconcileManager makes the manager the first window of Desktop,
// creating the session or the window when missing.
func (d *Daemon) reconcileManager() bool {
	wins, ok := d.list()
	if !ok {
		return false
	}
	name := config.ManagerName
	desk := inSession(wins, SessionDesktop)
	if len(desk) > 0 {
		for _, w := range desk {
			if w.Tag != name {
				continue
			}
			if w.Index != desk[0].Index {
				if err := d.tmux.MoveWindowBefore(SessionDesktop, w.ID, desk[0].Index); err != nil {
					d.issue("manager", "move to the front of Desktop: %v", err)
				} else if err := d.tmux.RenumberWindows(SessionDesktop); err != nil {
					d.issue("tmux", "renumber Desktop: %v", err)
				}
			}
			return true
		}
	}
	folder := config.Folder(d.root, name)
	if err := d.ensureFolder(folder); err != nil {
		d.issue("manager", "%v", err)
		return false
	}
	// Before the manager starts, so it reads its instructions.
	if err := d.seedManager(folder); err != nil {
		d.issue("manager seed", "seed instructions in %s: %v", folder, err)
	}
	var err error
	if len(desk) == 0 {
		_, err = d.tmux.NewSession(SessionDesktop, name, folder, d.argv(d.cfg.Manager), windowOptions(name))
	} else {
		_, err = d.tmux.NewWindowBefore(fmt.Sprintf("%s:%d", SessionDesktop, desk[0].Index),
			name, folder, d.argv(d.cfg.Manager), windowOptions(name))
	}
	if err != nil {
		d.issue("manager", "create window: %v", err)
		return false
	}
	return true
}

// reconcileAgents makes sure every configured agent has exactly one
// window that lives in Background. It returns each agent's window id.
func (d *Daemon) reconcileAgents() (map[string]string, bool) {
	wins, ok := d.list()
	if !ok {
		return nil, false
	}
	bg := inSession(wins, SessionBackground)
	bgNext := 0
	if len(bg) > 0 {
		bgNext = bg[len(bg)-1].Index + 1
	}
	canonical := map[string]string{}
	for _, a := range d.cfg.Agents {
		key := "agent " + a.Name
		ids := distinctIDs(wins, a.Name)
		if len(ids) > 1 {
			d.issue(key+" duplicates", "%d windows are tagged %q (%s); using %s, the others are left alone",
				len(ids), a.Name, strings.Join(ids, " "), ids[0])
		}
		if len(ids) == 0 {
			folder := config.Folder(d.root, a.Name)
			if err := d.ensureFolder(folder); err != nil {
				d.issue(key, "%v", err)
				continue
			}
			var id string
			var err error
			if len(bg) == 0 {
				id, err = d.tmux.NewSession(SessionBackground, a.Name, folder, d.argv(a.Command), windowOptions(a.Name))
			} else {
				id, err = d.tmux.NewWindow(SessionBackground, a.Name, folder, d.argv(a.Command), windowOptions(a.Name))
			}
			if err != nil {
				d.issue(key, "create window: %v", err)
				continue
			}
			bg = append(bg, tmux.Window{Session: SessionBackground, ID: id})
			bgNext++
			canonical[a.Name] = id
			continue
		}
		id := ids[0]
		canonical[a.Name] = id
		if hasID(bg, id) {
			continue
		}
		// The window survived only in Desktop (e.g. the Background
		// session was killed): bring it home instead of starting a
		// second copy of the command.
		if err := d.linkIntoBackground(id, &bg, &bgNext); err != nil {
			d.issue(key, "re-link window %s into Background: %v", id, err)
		}
	}
	// Report cohort windows in Background that no agent claims.
	for _, w := range bg {
		if w.Tag != "" && w.Tag != config.ManagerName && d.cfg.Agent(w.Tag) == nil {
			d.issue("orphan "+w.ID, "window %s (%q) in Background has no config entry; left alone (destroy postponed)", w.ID, w.Tag)
		}
	}
	return canonical, true
}

// linkIntoBackground links window id into Background, creating the
// session (with a throwaway placeholder window) when it does not exist.
func (d *Daemon) linkIntoBackground(id string, bg *[]tmux.Window, next *int) error {
	if len(*bg) == 0 {
		ph, err := d.tmux.NewSession(SessionBackground, "placeholder", d.root, []string{"sleep", "60"}, nil)
		if err != nil {
			return err
		}
		if err := d.tmux.LinkWindow(id, SessionBackground, 1000); err != nil {
			return err
		}
		if err := d.tmux.KillWindow(SessionBackground, ph); err != nil {
			return err
		}
		*bg = append(*bg, tmux.Window{Session: SessionBackground, ID: id})
		*next = 1001
		return nil
	}
	if err := d.tmux.LinkWindow(id, SessionBackground, *next); err != nil {
		return err
	}
	*bg = append(*bg, tmux.Window{Session: SessionBackground, ID: id})
	*next++
	return nil
}

// reconcileDesktop makes Desktop show exactly the manager plus the
// windows of the agents configured as linked, in config order for new
// links. Windows without a cohort tag are not cohort's and are left
// alone.
func (d *Daemon) reconcileDesktop(canonical map[string]string) {
	wins, ok := d.list()
	if !ok {
		return
	}
	desk := inSession(wins, SessionDesktop)
	if len(desk) == 0 {
		return
	}
	want := map[string]bool{}
	for _, a := range d.cfg.Agents {
		if id, ok := canonical[a.Name]; ok && a.Linked {
			want[id] = true
		}
	}
	changed := false
	for _, w := range desk {
		if w.Tag == "" || w.Tag == config.ManagerName || want[w.ID] {
			continue
		}
		if !linkedElsewhere(wins, w) {
			// Unlinking would destroy it (tmux refuses anyway).
			why := "has no config entry"
			if d.cfg.Agent(w.Tag) != nil {
				why = "is a duplicate of the agent's window"
			}
			d.issue("orphan "+w.ID, "window %s (%q) exists only in Desktop and %s; left alone (destroy postponed)", w.ID, w.Tag, why)
			continue
		}
		if err := d.tmux.UnlinkWindow(SessionDesktop, w.ID); err != nil {
			d.issue("agent "+w.Tag, "unlink from Desktop: %v", err)
			continue
		}
		changed = true
	}
	next := desk[len(desk)-1].Index + 1
	for _, a := range d.cfg.Agents {
		id, ok := canonical[a.Name]
		if !ok || !a.Linked || hasID(desk, id) {
			continue
		}
		if err := d.tmux.LinkWindow(id, SessionDesktop, next); err != nil {
			d.issue("agent "+a.Name, "link into Desktop: %v", err)
			continue
		}
		next++
		changed = true
	}
	if changed {
		if err := d.tmux.RenumberWindows(SessionDesktop); err != nil {
			d.issue("tmux", "renumber Desktop: %v", err)
		}
	}
}

// windowOptions are the options every cohort window carries: its tag
// and its colour.
func windowOptions(name string) map[string]string {
	opts := colorOptions(name)
	opts[tmux.TagOption] = name
	return opts
}

// backfillColors gives cohort windows created before colours existed
// (or by an older cohort) their colour.
func (d *Daemon) backfillColors(wins []tmux.Window) {
	done := map[string]bool{}
	for _, w := range wins {
		if w.Tag == "" || w.Color != "" || done[w.ID] {
			continue
		}
		done[w.ID] = true
		if err := d.tmux.SetWindowOptions(w.ID, colorOptions(w.Tag)); err != nil {
			d.issue("tmux", "set colour of %s: %v", w.ID, err)
		}
	}
}

// inSession returns the winlinks of one session in index order.
func inSession(wins []tmux.Window, session string) []tmux.Window {
	var out []tmux.Window
	for _, w := range wins {
		if w.Session == session {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// distinctIDs returns the ids of the windows tagged tag, those in
// Background first, each once.
func distinctIDs(wins []tmux.Window, tag string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, pass := range []bool{true, false} {
		for _, w := range wins {
			if w.Tag == tag && (w.Session == SessionBackground) == pass && !seen[w.ID] {
				seen[w.ID] = true
				ids = append(ids, w.ID)
			}
		}
	}
	return ids
}

func hasID(wins []tmux.Window, id string) bool {
	for _, w := range wins {
		if w.ID == id {
			return true
		}
	}
	return false
}

// linkedElsewhere reports whether w's window is also linked into a
// session other than w's own.
func linkedElsewhere(wins []tmux.Window, w tmux.Window) bool {
	for _, o := range wins {
		if o.ID == w.ID && o.Session != w.Session {
			return true
		}
	}
	return false
}

// ensureFolder guarantees that folder exists as a local-only git repo.
//
//   - missing  -> created, git init, marked cohort.managed=true
//   - present and marked cohort.managed=true -> kept (agent resume)
//   - anything else -> error: cohort never adopts pre-existing folders
func (d *Daemon) ensureFolder(folder string) error {
	if _, err := os.Stat(folder); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return err
		}
		if err := gitInit(folder); err != nil {
			return fmt.Errorf("git init %s: %w", folder, err)
		}
		d.log.Printf("initialized local git repo %s", folder)
		return nil
	} else if err != nil {
		return err
	}
	out, err := git(folder, "config", "--local", "--get", gitManagedKey)
	if err != nil || strings.TrimSpace(out) != "true" {
		return fmt.Errorf("folder %s already exists and is not a cohort-managed git repo "+
			"(if it is a restored copy of a cohort folder, adopt it with: git -C %s config %s true)",
			folder, folder, gitManagedKey)
	}
	return nil
}

func gitInit(folder string) error {
	if _, err := git(folder, "init", "-b", "main"); err != nil {
		// Older git without -b.
		if _, err := git(folder, "init"); err != nil {
			return err
		}
	}
	_, err := git(folder, "config", "--local", gitManagedKey, "true")
	return err
}

func git(dir string, args ...string) (string, error) {
	var out, stderr strings.Builder
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return out.String(), nil
}
