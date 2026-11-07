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

// ManagerName is the name of the cohort manager agent itself. Its
// window lives natively (and only) in the Desktop session as the
// stable first window; it is not part of the agents list.
const ManagerName = "cohort-manager"

// DefaultManagerCommand is the manager's startup command when the
// config does not override it. The manager is reserved: it is always
// claude code running in ~/.cohort/cohort-manager — only the exact
// startup command is customizable.
const DefaultManagerCommand = "claude"

// ManagerFolder returns the manager's absolute folder path (fixed).
func ManagerFolder(root string) string {
	return filepath.Join(root, ManagerName)
}

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
	// Desktop session.
	Linked bool `json:"linked"`
}

// HarnessCommands maps harness names to their default startup commands.
var HarnessCommands = map[string]string{
	"claude":   "claude",
	"codex":    "codex",
	"pi":       "pi",
	"opencode": "opencode",
}

// Validate checks the agent is well formed without mutating it.
func (a *Agent) Validate() error {
	if a.Name == "" {
		return errors.New("agent name is empty")
	}
	if strings.ContainsAny(a.Name, ":.") || strings.ContainsRune(a.Name, ' ') {
		return fmt.Errorf("agent name %q: only letters, digits, '-' and '_' allowed", a.Name)
	}
	if a.Command == "" {
		if _, ok := HarnessCommands[a.Harness]; !ok {
			return fmt.Errorf("agent %q: unknown harness %q and no command given", a.Name, a.Harness)
		}
	}
	return nil
}

// StartupCommand resolves the command tmux should run in the window.
func (a *Agent) StartupCommand() string {
	if a.Command != "" {
		return a.Command
	}
	return HarnessCommands[a.Harness]
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

// Config is the root of config.json.
type Config struct {
	// Manager is the cohort manager's startup command (tmux shell
	// command). Empty means DefaultManagerCommand. Everything else
	// about the manager is reserved and hardcoded.
	Manager string   `json:"manager,omitempty"`
	Agents  []*Agent `json:"agents"`
}

// ManagerCommand resolves the manager's startup command.
func (c *Config) ManagerCommand() string {
	if c.Manager != "" {
		return c.Manager
	}
	return DefaultManagerCommand
}

// Validate validates all agents and rejects duplicates and the
// reserved manager name in agents.
func (c *Config) Validate() error {
	seen := make(map[string]bool, len(c.Agents))
	for _, a := range c.Agents {
		if err := a.Validate(); err != nil {
			return err
		}
		if a.Name == ManagerName {
			return fmt.Errorf("%q is reserved for the manager; use the top-level \"manager\" setting", ManagerName)
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

// EnsureManager migrates a legacy manager entry (the manager used to
// be an agents entry) into the top-level manager setting. Returns
// true if the config was modified.
func (c *Config) EnsureManager() bool {
	m := c.Agent(ManagerName)
	if m == nil {
		return false
	}
	if c.Manager == "" {
		c.Manager = m.StartupCommand()
		if c.Manager == "" {
			c.Manager = DefaultManagerCommand
		}
	}
	c.removeAgent(ManagerName)
	return true
}

// removeAgent deletes the agent with the given name, if present.
func (c *Config) removeAgent(name string) {
	for i, a := range c.Agents {
		if a.Name == name {
			c.Agents = append(c.Agents[:i], c.Agents[i+1:]...)
			return
		}
	}
}

// Parse decodes config bytes, applies defaults/migration
// (EnsureManager) and validates. It does not mutate the input.
func Parse(data []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.EnsureManager()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Load reads, parses and validates the config file at path.
// Returns a wrapped error if the file does not exist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
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

// Marshal returns the canonical pretty-printed config bytes (the
// format Save writes).
func Marshal(c *Config) []byte {
	data, _ := json.MarshalIndent(c, "", "  ")
	return append(data, '\n')
}
