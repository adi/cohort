package cohort

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerFilesSeededOnce(t *testing.T) {
	root := t.TempDir()
	if err := seedServerFiles(root); err != nil {
		t.Fatal(err)
	}
	conf, err := os.ReadFile(filepath.Join(root, "tmux.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(conf), "@ROOT@") || !strings.Contains(string(conf), root+"/tab-color.sh") {
		t.Fatal("@ROOT@ not substituted")
	}
	if st, err := os.Stat(filepath.Join(root, "tab-color.sh")); err != nil || st.Mode()&0o100 == 0 {
		t.Fatal("tab-color.sh missing or not executable")
	}
	if err := os.WriteFile(filepath.Join(root, "tmux.conf"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := seedServerFiles(root); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "tmux.conf")); string(data) != "mine\n" {
		t.Fatal("tmux.conf overwritten")
	}
}

// The seeded tmux.conf must load without errors.
func TestTmuxConfLoads(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	root := t.TempDir()
	if err := seedServerFiles(root); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(root, "sock")
	tm := func(args ...string) string {
		out, _ := exec.Command("tmux", append([]string{"-S", sock, "-f", filepath.Join(root, "tmux.conf")}, args...)...).CombinedOutput()
		return string(out)
	}
	defer tm("kill-server")
	tm("new-session", "-d", "-s", "x", "sleep", "60")
	if msgs := tm("show-messages"); strings.Contains(strings.ToLower(msgs), "error") || strings.Contains(msgs, "invalid") || strings.Contains(msgs, "unknown") {
		t.Fatalf("tmux.conf errors:\n%s", msgs)
	}
	if got := tm("show", "-gv", "window-status-current-format"); !strings.Contains(got, "reverse") {
		t.Fatalf("defaults not applied: %q", got)
	}
	// Lock-down: kill and layout keys only show a message, menus are
	// gone, R restarts with confirmation.
	for _, k := range []string{"&", "x", "$", "!", "\"", "%", ".", ":", "<", ">", "w", "s"} {
		if got := tm("list-keys", "-T", "prefix", k); !strings.Contains(got, "disabled in cohort") {
			t.Errorf("prefix %s not disabled: %q", k, got)
		}
	}
	if got := tm("list-keys", "-T", "prefix", "R"); !strings.Contains(got, "confirm-before") || !strings.Contains(got, "kill-window") {
		t.Errorf("prefix R: %q", got)
	}
	for _, k := range []string{"MouseDown3Status", "M-MouseDown3Status", "M-MouseDown3Pane"} {
		if got := tm("list-keys", "-T", "root", k); strings.Contains(got, "display-menu") {
			t.Errorf("%s still opens a menu: %q", k, got)
		}
	}
	if got := tm("list-keys", "-T", "root", "MouseDown3Pane"); strings.Contains(got, "display-menu") {
		t.Errorf("MouseDown3Pane still opens a menu: %q", got)
	}
}

// seedTeamFolder seeds a manager folder under a fake HOME and returns
// (home, folder).
func seedTeamFolder(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, ".cohort")
	folder := filepath.Join(root, "cohort-manager")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := gitInit(folder); err != nil {
		t.Fatal(err)
	}
	if err := newDaemon(root, "unused", io.Discard).seedManager(folder); err != nil {
		t.Fatal(err)
	}
	return home, folder
}

// The seeded team tooling has no user's paths baked in, and the
// default manager command loads the seeded mod.
func TestTeamSeedPortable(t *testing.T) {
	err := fs.WalkDir(managerSeed, seedRoot, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, _ := managerSeed.ReadFile(p)
		if strings.Contains(string(data), "/home/") || strings.Contains(string(data), "/Users/") {
			t.Errorf("%s hardcodes a home directory", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	const mod = "~/.cohort/cohort-manager/mods/cohort-team"
	if strings.Count(DefaultManagerCommand, "--plugin-dir "+mod) != 2 || !strings.Contains(DefaultManagerCommand, "--continue ||") {
		t.Fatalf("default manager command: %q", DefaultManagerCommand)
	}
	if _, err := managerSeed.ReadFile(seedRoot + "/mods/cohort-team/.claude-plugin/plugin.json"); err != nil {
		t.Fatal("mod not in the seed")
	}
}

func TestTeamTool(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	home, folder := seedTeamFolder(t)
	cohortDir := filepath.Join(home, ".cohort")
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(cohortDir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"manager":"m","agents":[{"name":"a","command":"x"},{"name":"b","command":"y","linked":true},{"name":"c","command":"z"}]}`)
	team := func(args ...string) (string, error) {
		cmd := exec.Command(filepath.Join(folder, "bin/team"), args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "TEAM_SETTLE=0")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// The starter teams.json is empty.
	if out, err := team("list"); err != nil || !strings.Contains(out, "no teams yet") {
		t.Fatalf("list on empty teams: %q %v", out, err)
	}

	if err := os.WriteFile(filepath.Join(folder, "teams.json"), []byte(`{
  "agents": {"a": {"purpose": "p", "team": "t"}, "b": {"purpose": "p", "team": "t"}, "gone": {"purpose": "p", "team": "t"}, "c": {"purpose": "p", "team": "c"}},
  "teams": {"t": {"description": "d"}, "c": {"description": "team of one"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := team("show", "t")
	if err != nil || !strings.Contains(out, "shown: a") || !strings.Contains(out, "skipped: gone") ||
		!strings.Contains(out, "status: unknown") {
		t.Fatalf("show: %q %v", out, err)
	}
	cfg, _ := os.ReadFile(filepath.Join(cohortDir, "config.json"))
	var c struct {
		Manager string
		Agents  []struct {
			Name   string
			Linked bool
		}
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	linked := map[string]bool{}
	for _, a := range c.Agents {
		linked[a.Name] = a.Linked
	}
	if c.Manager != "m" || !linked["a"] || !linked["b"] || linked["c"] {
		t.Fatalf("config after show: %s", cfg)
	}

	write("status.json", `{"issues":{}}`)
	if out, err := team("hide", "t"); err != nil || !strings.Contains(out, "hidden: a, b") || !strings.Contains(out, "status: ok") {
		t.Fatalf("hide: %q %v", out, err)
	}
	if out, err := team("list"); err != nil || !strings.Contains(out, "a            hidden") || !strings.Contains(out, "gone         not in config") {
		t.Fatalf("list: %q %v", out, err)
	}
	if out, err := team("show", "nope"); err == nil || !strings.Contains(out, "unknown team") {
		t.Fatalf("unknown team: %q %v", out, err)
	}
}
