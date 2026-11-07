package cohort

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// harness runs the real reconcile logic against a throwaway tmux
// server and a temporary ~/.cohort.
type harness struct {
	t *testing.T
	d *Daemon
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	root := t.TempDir()
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	d := newDaemon(root, socket, io.Discard)
	d.shell = []string{"/bin/sh", "-c"}
	if err := seedServerFiles(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec.Command("tmux", "-S", socket, "kill-server").Run() })
	return &harness{t: t, d: d}
}

func (h *harness) config(js string) {
	h.t.Helper()
	if err := os.WriteFile(h.d.cfgPath, []byte(js), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) tick() { h.d.tick() }

func (h *harness) tmux(args ...string) string {
	h.t.Helper()
	out, err := exec.Command("tmux", append(h.d.tmux.SocketArgs(), args...)...).CombinedOutput()
	if err != nil {
		h.t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return string(out)
}

// layout renders a session as "tag@id tag@id ..." in index order.
func (h *harness) layout(session string) string {
	h.t.Helper()
	wins, err := h.d.tmux.ListAll()
	if err != nil {
		h.t.Fatal(err)
	}
	var parts []string
	for _, w := range inSession(wins, session) {
		parts = append(parts, w.Tag+w.ID)
	}
	return strings.Join(parts, " ")
}

func (h *harness) tags(session string) string {
	h.t.Helper()
	wins, _ := h.d.tmux.ListAll()
	var parts []string
	for _, w := range inSession(wins, session) {
		parts = append(parts, w.Tag)
	}
	return strings.Join(parts, " ")
}

func (h *harness) expect(session, want string) {
	h.t.Helper()
	if got := h.tags(session); got != want {
		h.t.Fatalf("%s = %q, want %q (issues: %v)", session, got, want, h.d.issues)
	}
}

const twoAgents = `{"manager":"sleep 600","agents":[
 {"name":"api","command":"sleep 600","linked":true},
 {"name":"web","command":"sleep 600","linked":false}]}`

func TestBootstrapAndLinking(t *testing.T) {
	h := newHarness(t)
	h.config(twoAgents)
	h.tick()
	h.expect("Desktop", "cohort-manager api")
	h.expect("Background", "api web")
	if len(h.d.issues) != 0 {
		t.Fatalf("issues: %v", h.d.issues)
	}
	for _, f := range []string{"cohort-manager", "api", "web"} {
		out, err := git(filepath.Join(h.d.root, f), "config", "--local", "--get", gitManagedKey)
		if err != nil || strings.TrimSpace(out) != "true" {
			t.Errorf("folder %s not a cohort-managed repo", f)
		}
	}

	// Flip links: config is the source of truth.
	h.config(`{"manager":"sleep 600","agents":[
	 {"name":"api","command":"sleep 600","linked":false},
	 {"name":"web","command":"sleep 600","linked":true}]}`)
	h.tick()
	h.expect("Desktop", "cohort-manager web")
	h.expect("Background", "api web")

	// A link made by hand is undone.
	wins, _ := h.d.tmux.ListAll()
	for _, w := range inSession(wins, "Background") {
		if w.Tag == "api" {
			h.tmux("link-window", "-d", "-s", w.ID, "-t", "Desktop:9")
		}
	}
	h.tick()
	h.expect("Desktop", "cohort-manager web")
}

func (h *harness) read() string {
	data, err := os.ReadFile(h.d.cfgPath)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(data)
}

func TestDaemonNeverRewritesConfig(t *testing.T) {
	h := newHarness(t)
	compact := `{"manager":"sleep 600","agents":[]}`
	h.config(compact)
	h.tick()
	h.tick()
	if _, err := os.Stat(filepath.Join(h.d.root, "status.json")); err != nil {
		t.Fatalf("status.json not written without issues: %v", err)
	}
	if got := h.read(); got != compact {
		t.Fatalf("config rewritten to %q", got)
	}
}

func TestManagerStaysFirst(t *testing.T) {
	h := newHarness(t)
	h.config(twoAgents)
	h.tick()
	before := h.layout("Desktop")

	// Moved away from the front: put back.
	h.tmux("move-window", "-s", "Desktop:0", "-t", "Desktop:7")
	h.tick()
	if got := h.layout("Desktop"); got != before {
		t.Fatalf("Desktop = %q, want %q", got, before)
	}

	// Killed: recreated at the front, agents untouched.
	h.tmux("kill-window", "-t", "Desktop:0")
	h.tick()
	h.expect("Desktop", "cohort-manager api")
	if strings.Fields(h.layout("Desktop"))[1] != strings.Fields(before)[1] {
		t.Fatal("agent window was replaced")
	}

	// Whole Desktop session killed: recreated, linked agent re-linked.
	h.tmux("kill-session", "-t", "Desktop")
	h.tick()
	h.expect("Desktop", "cohort-manager api")
	h.expect("Background", "api web")
}

func TestBackgroundKilledKeepsLinkedWindows(t *testing.T) {
	h := newHarness(t)
	h.config(twoAgents)
	h.tick()
	apiBefore := strings.Fields(h.layout("Desktop"))[1]

	// api survives in Desktop only; web is gone. cohort must bring api
	// home (same window, no second copy) and recreate web.
	h.tmux("kill-session", "-t", "Background")
	h.tick()
	h.expect("Background", "web api") // order in Background is not significant
	h.expect("Desktop", "cohort-manager api")
	if got := strings.Fields(h.layout("Desktop"))[1]; got != apiBefore {
		t.Fatalf("api window replaced: %s -> %s", apiBefore, got)
	}
}

func TestBadAgentDoesNotBlockOthers(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Join(h.d.root, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.config(`{"manager":"sleep 600","agents":[
	 {"name":"taken","command":"sleep 600","linked":true},
	 {"name":"good","command":"sleep 600","linked":true}]}`)
	h.tick()
	h.expect("Background", "good")
	h.expect("Desktop", "cohort-manager good")
	if msg := h.d.issues["agent taken"]; !strings.Contains(msg, "config cohort.managed true") {
		t.Fatalf("issue for taken lacks the fix command: %q", msg)
	}
	status, err := os.ReadFile(filepath.Join(h.d.root, "status.json"))
	if err != nil || !strings.Contains(string(status), "agent taken") {
		t.Fatalf("status.json missing issue: %s %v", status, err)
	}
	// Fixed: issue clears.
	h.config(`{"manager":"sleep 600","agents":[{"name":"good","command":"sleep 600","linked":true}]}`)
	h.tick()
	if len(h.d.issues) != 0 {
		t.Fatalf("issues: %v", h.d.issues)
	}
}

func TestInvalidConfigKeepsState(t *testing.T) {
	h := newHarness(t)
	h.config(twoAgents)
	h.tick()
	h.config(`{"manager":"sleep 600","agents":[{"name":"1","command":"x"}]}`)
	h.tick()
	if _, ok := h.d.issues["config"]; !ok {
		t.Fatal("invalid config not reported")
	}
	h.expect("Desktop", "cohort-manager api")
	h.expect("Background", "api web")
}

func TestOrphansLeftAlone(t *testing.T) {
	h := newHarness(t)
	h.config(twoAgents)
	h.tick()
	// Remove api (linked) from config: unlinked from Desktop, kept in
	// Background as an orphan.
	h.config(`{"manager":"sleep 600","agents":[{"name":"web","command":"sleep 600","linked":false}]}`)
	h.tick()
	h.expect("Desktop", "cohort-manager")
	h.expect("Background", "api web")
	found := false
	for k := range h.d.issues {
		if strings.HasPrefix(k, "orphan ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphan not reported: %v", h.d.issues)
	}
	// Untagged windows in Desktop are not cohort's business.
	h.tmux("new-window", "-d", "-t", "Desktop:", "sleep", "600")
	h.tick()
	h.expect("Desktop", "cohort-manager ")
}

func TestCommandsRunInShell(t *testing.T) {
	h := newHarness(t)
	h.config(`{"manager":"sleep 600","agents":[{"name":"a","command":"echo hi && sleep 600","linked":false}]}`)
	h.tick()
	out := h.tmux("list-panes", "-a", "-F", "#{pane_start_command}")
	if !strings.Contains(out, "echo hi && sleep 600") {
		t.Fatalf("start commands: %s", out)
	}
}

func TestManagerSeeded(t *testing.T) {
	h := newHarness(t)
	h.config(`{"manager":"sleep 600","agents":[]}`)
	h.tick()
	folder := filepath.Join(h.d.root, "cohort-manager")
	for _, f := range []string{"CLAUDE.md", ".claude/skills/cohort-agents/SKILL.md", ".claude/skills/cohort-talk/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(folder, f)); err != nil {
			t.Errorf("not seeded: %s", f)
		}
	}
	if out, err := git(folder, "status", "--porcelain"); err != nil || out != "" {
		t.Fatalf("seed not committed: %q %v", out, err)
	}

	// The manager owns the files after the first commit: an edit
	// survives the manager window being recreated.
	claude := filepath.Join(folder, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.tmux("kill-window", "-t", "Desktop:0")
	h.tick()
	h.expect("Desktop", "cohort-manager")
	if data, _ := os.ReadFile(claude); string(data) != "mine\n" {
		t.Fatalf("seed overwrote the manager's file: %q", data)
	}
}

func TestSeedKeepsExistingFilesInEmptyRepo(t *testing.T) {
	h := newHarness(t)
	folder := filepath.Join(h.d.root, "cohort-manager")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := gitInit(folder); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "CLAUDE.md"), []byte("own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.config(`{"manager":"sleep 600","agents":[]}`)
	h.tick()
	if data, _ := os.ReadFile(filepath.Join(folder, "CLAUDE.md")); string(data) != "own\n" {
		t.Fatalf("existing file overwritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(folder, ".claude/skills/cohort-talk/SKILL.md")); err != nil {
		t.Fatal("missing files not seeded")
	}
	if len(h.d.issues) != 0 {
		t.Fatalf("issues: %v", h.d.issues)
	}
}

func TestWindowsGetColours(t *testing.T) {
	h := newHarness(t)
	h.config(twoAgents)
	h.tick()
	out := h.tmux("list-windows", "-a", "-F", "#{@cohort} #{@cohort_color}")
	for _, name := range []string{"cohort-manager", "api", "web"} {
		if !strings.Contains(out, name+" "+colorOptions(name)["@cohort_color"]) {
			t.Errorf("%s has no colour:\n%s", name, out)
		}
	}
	// A window from an older cohort (no colour) gets one.
	h.tmux("set-option", "-w", "-u", "-t", "Background:web", "@cohort_color")
	h.tick()
	if got := strings.TrimSpace(h.tmux("display", "-p", "-t", "Background:web", "#{@cohort_color}")); got == "" {
		t.Fatal("colour not backfilled")
	}
}

func TestUnlinkWithRenumberWindows(t *testing.T) {
	h := newHarness(t)
	h.config(`{"manager":"sleep 600","agents":[
	 {"name":"a","command":"sleep 600","linked":true},
	 {"name":"b","command":"sleep 600","linked":true},
	 {"name":"c","command":"sleep 600","linked":true}]}`)
	h.tick()
	h.expect("Desktop", "cohort-manager a b c")
	h.tmux("set-option", "-g", "renumber-windows", "on")
	h.config(`{"manager":"sleep 600","agents":[
	 {"name":"a","command":"sleep 600","linked":false},
	 {"name":"b","command":"sleep 600","linked":false},
	 {"name":"c","command":"sleep 600","linked":true}]}`)
	h.tick()
	h.expect("Desktop", "cohort-manager c")
}
