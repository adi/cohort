// Package config defines cohort's declarative configuration (config.json).
// The cohort manager agent edits this file directly; the cohort daemon
// watches it and reconciles tmux state to match.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ManagerName is the name of the cohort manager agent itself. It is
// pinned as the stable first window of the Desktop session and is
// always present in the config.
const ManagerName = "cohort-manager"

// Agent is one LLM harness running in one folder, materialized as a
// tmux window named <Name> in the Background session.
type Agent struct {
	// Name is the agent name; also the tmux window name and, by
	// default, the folder name under ~/.cohort/.
	// Restricted to letters, digits, '-' and '_'.
	Name string `json:"name"`

	// Harness identifies the LLM harness (claude, codex, pi, opencode...).
	// Determines the default startup command unless Command is set.
	Harness string `json:"harness"`

	// Folder defaults to ~/.cohort/<Name> when empty. May use ~/ prefix.
	Folder string `json:"folder,omitempty"`

	// Command overrides the harness default startup command.
	// Passed to tmux as a shell command.
	Command string `json:"command,omitempty"`

	// Linked controls whether the agent's window is linked into the
	// Desktop session. Ignored (forced true) for the cohort manager.
	Linked bool `json:"linked"`
}

// Config is the root of config.json.
type Config struct {
	Agents []*Agent `json:"agents"`
}

// HarnessCommands maps harness names to their default startup commands.
var HarnessCommands = map[string]string{
	"claude":   "claude",
	"codex":    "codex",
	"pi":       "pi",
	"opencode": "opencode",
}

// Validate fills in defaults and checks the agent is well formed.
func (a *Agent) Validate() error {
	if a.Name == "" {
		return errors.New("agent name is empty")
	}
	if strings.ContainsAny(a.Name, ":.") || strings.ContainsRune(a.Name, ' ') {
		return fmt.Errorf("agent name %q: only letters, digits, '-' and '_' allowed", a.Name)
	}
	if a.Command == "" {
		cmd, ok := HarnessCommands[a.Harness]
		if !ok {
			return fmt.Errorf("agent %q: unknown harness %q and no command given", a.Name, a.Harness)
		}
		a.Command = cmd
	}
	return nil
}

// ResolveFolder returns the agent's absolute folder path.
func (a *Agent) ResolveFolder(root string) string {
	if a.Folder == "" {
		return filepath.Join(root, a.Name)
	}
	return Expand(a.Folder)
}

// Expand resolves a leading ~/ or ~ in a path.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Load reads and validates the config file at path.
// Returns a wrapped error if the file does not exist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate validates all agents and rejects duplicates.
func (c *Config) Validate() error {
	seen := make(map[string]bool, len(c.Agents))
	for _, a := range c.Agents {
		if err := a.Validate(); err != nil {
			return err
		}
		if seen[a.Name] {
			return fmt.Errorf("duplicate agent %q", a.Name)
		}
		seen[a.Name] = true
	}
	return nil
}

// Agent returns the agent with the given name, or nil.
func (c *Config) Agent(name string) *Agent {
	for _, a := range c.Agents {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// Has reports whether an agent with the given name is configured.
func (c *Config) Has(name string) bool { return c.Agent(name) != nil }

// EnsureManager guarantees the cohort manager agent is present and
// pinned (linked, first in the list). Returns true if the config was modified.
func (c *Config) EnsureManager() bool {
	if m := c.Agent(ManagerName); m != nil {
		if m.Harness == "" {
			m.Harness = "claude"
		}
		if !m.Linked {
			m.Linked = true // pinned: manager is Desktop's stable first window
			return true
		}
		return false
	}
	c.Agents = append([]*Agent{{
		Name:    ManagerName,
		Harness: "claude",
		Linked:  true,
	}}, c.Agents...)
	return true
}

// Save writes the config to path, pretty-printed.
func Save(path string, c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
