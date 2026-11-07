package cohort

import (
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
