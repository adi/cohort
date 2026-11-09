package cohort

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cohort/internal/config"
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
	// Not t.TempDir(): it embeds the test name, and macOS caps unix
	// socket paths at 104 bytes.
	sockDir, err := os.MkdirTemp("", "cohort")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "tmux.sock")
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
	for _, f := range []string{"CLAUDE.md", ".claude/skills/cohort-agents/SKILL.md", ".claude/skills/cohort-talk/SKILL.md",
		".claude/skills/cohort-team/SKILL.md", "teams.json", "bin/team",
		"mods/cohort-team/.claude-plugin/plugin.json", "mods/cohort-team/hooks/hooks.json", "mods/cohort-team/hooks/register.ts"} {
		if _, err := os.Stat(filepath.Join(folder, f)); err != nil {
			t.Errorf("not seeded: %s", f)
		}
	}
	if st, err := os.Stat(filepath.Join(folder, "bin/team")); err != nil || st.Mode()&0o100 == 0 {
		t.Error("bin/team not executable")
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

// runCount counts the lines a test command appended to <root>/runs: one per start.
func (h *harness) runCount() int {
	data, _ := os.ReadFile(filepath.Join(h.d.root, "runs"))
	return strings.Count(string(data), "\n")
}

func (h *harness) waitManagerDead() {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		if strings.TrimSpace(h.tmux("display-message", "-p", "-t", "Desktop:0", "#{pane_dead}")) == "1" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatal("manager never exited")
}

func TestManagerRestartedWhenItExits(t *testing.T) {
	h := newHarness(t)
	runs := filepath.Join(h.d.root, "runs")
	h.config(`{"manager":"echo run >> ` + runs + `; exit 0","agents":[{"name":"a","command":"exit 0","linked":true}]}`)
	h.tick()
	h.waitManagerDead()
	id := strings.TrimSpace(h.tmux("display-message", "-p", "-t", "Desktop:0", "#{window_id}"))

	// Pretend the first run lasted long enough: restarted right away,
	// in the same window (same id, still first in Desktop).
	h.d.run(config.ManagerName).started = time.Now().Add(-time.Hour)
	h.tick()
	h.waitManagerDead()
	if n := h.runCount(); n != 2 {
		t.Fatalf("manager ran %d times, want 2", n)
	}
	if got := strings.TrimSpace(h.tmux("display-message", "-p", "-t", "Desktop:0", "#{window_id}")); got != id {
		t.Fatalf("manager window replaced: %s -> %s", id, got)
	}
	if _, ok := h.d.issues["manager restarts"]; ok {
		t.Fatalf("issue after one restart: %v", h.d.issues)
	}

	// It died right after the restart: wait out the backoff, report it.
	h.tick()
	if n := h.runCount(); n != 2 {
		t.Fatalf("restarted without backoff: %d runs", n)
	}
	if !strings.Contains(h.d.issues["manager restarts"], "keeps exiting") {
		t.Fatalf("issues: %v", h.d.issues)
	}
	h.d.run(config.ManagerName).deadSince = time.Now().Add(-restartBackoffMin)
	h.tick()
	h.waitManagerDead()
	if n := h.runCount(); n != 3 {
		t.Fatalf("not restarted after backoff: %d runs", n)
	}

}

func TestAgentRestartedWhenItExits(t *testing.T) {
	h := newHarness(t)
	runs := filepath.Join(h.d.root, "runs")
	h.config(`{"manager":"sleep 600","agents":[{"name":"a","command":"echo run >> ` + runs + `; exit 0","linked":true},
		{"name":"b","command":"sleep 600"}]}`)
	h.tick()
	dead := func() {
		t.Helper()
		for i := 0; i < 50; i++ {
			if strings.TrimSpace(h.tmux("display-message", "-p", "-t", "Background:a", "#{pane_dead}")) == "1" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("agent a never exited")
	}
	dead()
	id := strings.TrimSpace(h.tmux("display-message", "-p", "-t", "Background:a", "#{window_id}"))
	h.d.run("a").started = time.Now().Add(-time.Hour) // its run lasted
	h.tick()
	dead()
	if n := h.runCount(); n != 2 {
		t.Fatalf("agent ran %d times, want 2", n)
	}
	// Restarted in place: same window, still in Background and Desktop.
	if got := strings.TrimSpace(h.tmux("display-message", "-p", "-t", "Background:a", "#{window_id}")); got != id {
		t.Fatalf("window replaced: %s -> %s", id, got)
	}
	if got := h.tags("Desktop"); got != "cohort-manager a" {
		t.Fatalf("Desktop: %q", got)
	}
	// A quick exit waits out the backoff and is reported per agent.
	h.tick()
	if n := h.runCount(); n != 2 || !strings.Contains(h.d.issues["agent a restarts"], "keeps exiting") {
		t.Fatalf("runs %d, issues %v", n, h.d.issues)
	}
	if len(h.d.issues) != 1 {
		t.Fatalf("other agents affected: %v", h.d.issues)
	}
	// Removing the agent forgets its restart state.
	h.config(`{"manager":"sleep 600","agents":[{"name":"b","command":"sleep 600"}]}`)
	h.tick()
	if _, ok := h.d.runs["a"]; ok {
		t.Fatal("restart state kept for a removed agent")
	}
}

func TestManagerBackoff(t *testing.T) {
	var m windowRun
	now := time.Now()
	m.started = now
	// Quick deaths double the delay up to the cap.
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}
	for i, w := range want {
		m.died(now.Add(time.Second))
		if m.backoff != w {
			t.Fatalf("death %d: backoff %s, want %s", i+1, m.backoff, w)
		}
	}
	// A run that lasts clears it.
	m.alive(now.Add(restartQuickExit))
	if m.backoff != 0 {
		t.Fatalf("backoff not cleared: %s", m.backoff)
	}
	// So does a death after a long run; an unknown start (manager left
	// over from a previous daemon) counts as long.
	m.backoff = time.Minute
	m.died(now.Add(time.Hour))
	if m.backoff != 0 {
		t.Fatalf("long run: backoff %s", m.backoff)
	}
	m = windowRun{backoff: time.Minute}
	m.died(now)
	if m.backoff != 0 {
		t.Fatalf("unknown start: backoff %s", m.backoff)
	}
}
