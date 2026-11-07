package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentValidateDoesNotMutate(t *testing.T) {
	a := &Agent{Name: "pi-api", Harness: "pi"}
	if err := a.Validate(); err != nil {
		t.Fatalf("valid agent rejected: %v", err)
	}
	if a.Command != "" {
		t.Errorf("Validate mutated command: %q", a.Command)
	}
	if a.ResolveFolder("/root") != filepath.Join("/root", "pi-api") {
		t.Errorf("default folder: %q", a.ResolveFolder("/root"))
	}
	if got := a.StartupCommand(); got != "pi" {
		t.Errorf("StartupCommand = %q, want pi", got)
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

func TestManagerSettings(t *testing.T) {
	c := &Config{}
	if !c.EnsureManager() {
		t.Fatal("EnsureManager did not default the manager")
	}
	if c.Manager == nil || c.Manager.Harness != "claude" {
		t.Fatalf("manager not defaulted: %+v", c.Manager)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("default manager invalid: %v", err)
	}
	if got := c.Manager.StartupCommand(); got != "claude" {
		t.Errorf("default manager command: %q", got)
	}
	if got := c.Manager.ResolveFolder("/r"); got != filepath.Join("/r", ManagerName) {
		t.Errorf("default manager folder: %q", got)
	}
	// Custom command overrides harness default.
	c.Manager.Command = "claude --model opus"
	if got := c.Manager.StartupCommand(); got != "claude --model opus" {
		t.Errorf("custom manager command: %q", got)
	}
	// Custom harness.
	c.Manager.Command = ""
	c.Manager.Harness = "codex"
	if got := c.Manager.StartupCommand(); got != "codex" {
		t.Errorf("codex harness command: %q", got)
	}
}

func TestParseDefaultsManagerAndRejectsBad(t *testing.T) {
	// missing manager -> defaulted, valid
	c, err := Parse([]byte(`{"agents":[{"name":"a","harness":"pi"}]}`))
	if err != nil {
		t.Fatalf("missing manager rejected: %v", err)
	}
	if c.Manager == nil || c.Manager.Harness != "claude" {
		t.Errorf("manager not defaulted: %+v", c.Manager)
	}
	// unknown manager harness without command -> rejected
	if _, err := Parse([]byte(`{"manager":{"harness":"vscode"}}`)); err == nil {
		t.Error("unknown manager harness accepted")
	}
	// duplicate agents -> rejected
	if _, err := Parse([]byte(`{"agents":[{"name":"a","harness":"pi"},{"name":"a","harness":"pi"}]}`)); err == nil {
		t.Error("duplicates accepted")
	}
	// The reserved name never survives Parse: it is always hoisted
	// from agents into the manager settings (migration), so Validate's
	// rejection only fires on direct struct use.
	c, err = Parse([]byte(`{"manager":{"harness":"claude"},` +
		`"agents":[{"name":"cohort-manager","harness":"pi","linked":true}]}`))
	if err != nil {
		t.Fatalf("hoisting rejected: %v", err)
	}
	if c.Has(ManagerName) {
		t.Error("reserved agent name left in agents")
	}
	if err := (&Config{Manager: &Manager{Harness: "claude"},
		Agents: []*Agent{{Name: ManagerName, Harness: "pi"}}}).Validate(); err == nil {
		t.Error("reserved agent name accepted by Validate")
	}
}

func TestParseMigratesLegacyManagerAgent(t *testing.T) {
	// Old schema: manager was an entry in agents.
	c, err := Parse([]byte(`{
	  "agents": [
	    {"name": "cohort-manager", "harness": "claude", "command": "sleep 400", "linked": true},
	    {"name": "pi-api", "harness": "pi", "linked": true}
	  ]
	}`))
	if err != nil {
		t.Fatalf("legacy config rejected: %v", err)
	}
	if c.Has(ManagerName) {
		t.Error("legacy manager agent still in agents list")
	}
	if c.Manager == nil || c.Manager.Command != "sleep 400" {
		t.Errorf("legacy manager settings lost: %+v", c.Manager)
	}
	if !c.Has("pi-api") {
		t.Error("other agents lost in migration")
	}
}

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if _, err := Load(path); !os.IsNotExist(err) {
		t.Errorf("missing file: got %v", err)
	}
	if err := Save(path, &Config{Manager: &Manager{Harness: "claude"}}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Manager == nil || c.Manager.Harness != "claude" {
		t.Errorf("round trip lost manager: %+v", c)
	}
	// Canonical bytes: Marshal(Save output) == file content (used by
	// the daemon to detect schema normalization needs).
	raw, _ := os.ReadFile(path)
	if string(Marshal(c)) != string(raw) {
		t.Errorf("Marshal is not canonical:\n%s\n%s", Marshal(c), raw)
	}
}
