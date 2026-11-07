package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFillsCommandAndRejectsBadNames(t *testing.T) {
	ok := &Agent{Name: "pi-api", Harness: "pi"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid agent rejected: %v", err)
	}
	if ok.Command != "pi" {
		t.Errorf("command not defaulted: %q", ok.Command)
	}
	if ok.Folder != "" {
		t.Errorf("folder should stay empty for defaulting, got %q", ok.Folder)
	}

	cases := []struct {
		agent Agent
		want  string
	}{
		{Agent{Name: "", Harness: "pi"}, "empty"},
		{Agent{Name: "has:colon", Harness: "pi"}, "colon"},
		{Agent{Name: "has.dot", Harness: "pi"}, "dot"},
		{Agent{Name: "has space", Harness: "pi"}, "space"},
		{Agent{Name: "unknown-harness", Harness: "vscode"}, "unknown harness"},
	}
	for _, c := range cases {
		err := c.agent.Validate()
		if err == nil {
			t.Errorf("agent %q accepted", c.agent.Name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("agent %q: error %q does not mention %q", c.agent.Name, err, c.want)
		}
	}
}

func TestCommandOverrideSkipsHarnessCheck(t *testing.T) {
	a := &Agent{Name: "custom", Harness: "whatever", Command: "some-wrapper --flag"}
	if err := a.Validate(); err != nil {
		t.Fatalf("command override rejected: %v", err)
	}
	if a.Command != "some-wrapper --flag" {
		t.Errorf("command clobbered: %q", a.Command)
	}
}

func TestResolveFolderDefaultAndExpansion(t *testing.T) {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".cohort")
	a := &Agent{Name: "foo"}
	if got := a.ResolveFolder(root); got != filepath.Join(root, "foo") {
		t.Errorf("default folder: got %q", got)
	}
	b := &Agent{Name: "bar", Folder: "~/elsewhere"}
	if got := b.ResolveFolder(root); got != filepath.Join(home, "elsewhere") {
		t.Errorf("~ expansion: got %q", got)
	}
}

func TestConfigDuplicateAgentsRejected(t *testing.T) {
	c := &Config{Agents: []*Agent{
		{Name: "a", Harness: "pi"},
		{Name: "a", Harness: "pi"},
	}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicates accepted: %v", err)
	}
}

func TestEnsureManagerPinsAndInserts(t *testing.T) {
	c := &Config{}
	if !c.EnsureManager() {
		t.Fatal("manager not inserted into empty config")
	}
	if len(c.Agents) != 1 || c.Agents[0].Name != ManagerName {
		t.Fatalf("manager not first: %+v", c.Agents)
	}
	if !c.Agents[0].Linked {
		t.Error("manager not linked")
	}
	if err := c.Agents[0].Validate(); err != nil {
		t.Errorf("manager agent invalid: %v", err)
	}
	// Unlinked manager gets re-pinned in place, order preserved.
	c.Agents = append(c.Agents, &Agent{Name: "pi-api", Harness: "pi"})
	c.Agents[0].Linked = false
	if !c.EnsureManager() {
		t.Error("manager re-pin not reported as change")
	}
	if !c.Agents[0].Linked || c.Agents[1].Name != "pi-api" {
		t.Errorf("manager re-pin broke config: %+v", c.Agents)
	}
	if c.EnsureManager() {
		t.Error("no-op EnsureManager reported change")
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if _, err := Load(path); !os.IsNotExist(err) {
		t.Errorf("missing file: got %v", err)
	}
	os.WriteFile(path, []byte("{ bad"), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("bad JSON accepted")
	}
	os.WriteFile(path, []byte(`{"agents":[{"name":"x","harness":"nope"}]}`), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("unknown harness accepted")
	}
	os.WriteFile(path, []byte(`{"agents":[{"name":"ok","harness":"pi"}]}`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if !c.Has("ok") {
		t.Error("Has(ok) false")
	}
	if got := c.Agent("ok").Command; got != "pi" {
		t.Errorf("command default: %q", got)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := &Config{Agents: []*Agent{{Name: "ok", Harness: "pi", Linked: true}}}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Has("ok") || !got.Agent("ok").Linked {
		t.Errorf("round trip mismatch: %+v", got.Agents)
	}
}
