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

func TestManagerSettingIsCommandOnly(t *testing.T) {
	c := &Config{}
	if got := c.ManagerCommand(); got != DefaultManagerCommand {
		t.Errorf("default manager command: %q", got)
	}
	if got := ManagerFolder("/r"); got != filepath.Join("/r", ManagerName) {
		t.Errorf("manager folder: %q", got)
	}
	// The setting is the startup command; empty means default.
	c.Manager = "claude --model opus"
	if got := c.ManagerCommand(); got != "claude --model opus" {
		t.Errorf("custom manager command: %q", got)
	}
	c.Manager = "  " // blank-ish values are passed through verbatim
	if got := c.ManagerCommand(); got != "  " {
		t.Errorf("manager command not verbatim: %q", got)
	}
}

func TestEnsureManagerMigratesOnly(t *testing.T) {
	c := &Config{Agents: []*Agent{{Name: "pi-api", Harness: "pi"}}}
	if c.EnsureManager() {
		t.Error("no legacy entry: EnsureManager reported change")
	}
	c.Agents = append([]*Agent{{Name: ManagerName, Harness: "claude", Command: "sleep 400"}}, c.Agents...)
	if !c.EnsureManager() {
		t.Error("legacy entry not migrated")
	}
	if c.Has(ManagerName) {
		t.Error("legacy manager agent still in agents list")
	}
	if c.Manager != "sleep 400" {
		t.Errorf("legacy command not hoisted: %q", c.Manager)
	}
	if !c.Has("pi-api") {
		t.Error("other agents lost in migration")
	}
	// An explicit manager setting wins over the legacy entry.
	c2 := &Config{Manager: "claude --flag",
		Agents: []*Agent{{Name: ManagerName, Harness: "claude", Command: "sleep 400"}}}
	if !c2.EnsureManager() {
		t.Error("legacy entry not removed")
	}
	if c2.Manager != "claude --flag" {
		t.Errorf("explicit manager setting clobbered: %q", c2.Manager)
	}
}

func TestParseManagerAndRejectsBad(t *testing.T) {
	// missing manager -> defaults at runtime, file untouched
	c, err := Parse([]byte(`{"agents":[{"name":"a","harness":"pi"}]}`))
	if err != nil {
		t.Fatalf("missing manager rejected: %v", err)
	}
	if c.Manager != "" || c.ManagerCommand() != DefaultManagerCommand {
		t.Errorf("manager defaulting broken: %q -> %q", c.Manager, c.ManagerCommand())
	}
	// any non-empty manager string is a valid shell command
	if c, err = Parse([]byte(`{"manager":"claude --model opus","agents":[]}`)); err != nil {
		t.Fatalf("manager command rejected: %v", err)
	}
	if c.ManagerCommand() != "claude --model opus" {
		t.Errorf("manager command: %q", c.ManagerCommand())
	}
	// duplicate agents -> rejected
	if _, err := Parse([]byte(`{"agents":[{"name":"a","harness":"pi"},{"name":"a","harness":"pi"}]}`)); err == nil {
		t.Error("duplicates accepted")
	}
	// The reserved name never survives Parse: it is always hoisted
	// from agents into the manager setting (migration), so Validate's
	// rejection only fires on direct struct use.
	c, err = Parse([]byte(`{"manager":"claude --flag",` +
		`"agents":[{"name":"cohort-manager","harness":"pi","linked":true}]}`))
	if err != nil {
		t.Fatalf("hoisting rejected: %v", err)
	}
	if c.Has(ManagerName) {
		t.Error("reserved agent name left in agents")
	}
	if err := (&Config{Manager: "claude",
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
	if c.Manager != "sleep 400" {
		t.Errorf("legacy manager command lost: %q", c.Manager)
	}
	if !c.Has("pi-api") {
		t.Error("other agents lost in migration")
	}
	// Legacy entry without a command -> default manager command.
	c, err = Parse([]byte(`{"agents":[{"name":"cohort-manager","harness":"claude"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Manager != DefaultManagerCommand {
		t.Errorf("legacy default command: %q", c.Manager)
	}
}

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if _, err := Load(path); !os.IsNotExist(err) {
		t.Errorf("missing file: got %v", err)
	}
	if err := Save(path, &Config{Manager: "claude --flag", Agents: []*Agent{}}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Manager != "claude --flag" {
		t.Errorf("round trip lost manager: %q", c.Manager)
	}
	// Canonical bytes: Marshal(Save output) == file content (used by
	// the daemon to detect schema normalization needs).
	raw, _ := os.ReadFile(path)
	if string(Marshal(c)) != string(raw) {
		t.Errorf("Marshal is not canonical:\n%s\n%s", Marshal(c), raw)
	}
}
