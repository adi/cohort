// Package config defines cohort's declarative configuration (config.json).
// The cohort manager agent edits this file directly; the cohort daemon
// only reads it (it writes it once, at first start) and reconciles tmux
// state to match.
//
// cohort knows nothing about LLM harnesses: every window just runs a
// command. Starting fresh vs. resuming a session is the command's
// business.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// ManagerName is the reserved name of the cohort manager. Its window
// lives only in the Desktop session, as its first window, and its
// folder is ~/.cohort/cohort-manager.
const ManagerName = "cohort-manager"

// namePattern restricts agent names so they are safe as tmux window
// names and folder names, and can never be mistaken for a tmux window
// index or pattern.
var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// Agent is one command running in its own folder (~/.cohort/<Name>),
// materialized as a tmux window in the Background session.
type Agent struct {
	// Name is the agent name: the tmux window name and the folder name
	// under ~/.cohort/.
	Name string `json:"name"`

	// Command is the shell command run in the agent's window. It is run
	// through an interactive login shell, so the user's environment
	// (PATH, API keys) applies.
	Command string `json:"command"`

	// Linked controls whether the agent's window is linked into the
	// Desktop session.
	Linked bool `json:"linked"`
}

// Validate checks the agent is well formed.
func (a *Agent) Validate() error {
	if !namePattern.MatchString(a.Name) {
		return fmt.Errorf("agent name %q: must start with a letter and contain only letters, digits, '-' and '_'", a.Name)
	}
	if a.Name == ManagerName {
		return fmt.Errorf("agent name %q is reserved for the manager", ManagerName)
	}
	if a.Command == "" {
		return fmt.Errorf("agent %q: command is empty", a.Name)
	}
	return nil
}

// Config is the root of config.json.
type Config struct {
	// Manager is the cohort manager's command (required).
	Manager string   `json:"manager"`
	Agents  []*Agent `json:"agents"`
}

// Validate checks the whole config.
func (c *Config) Validate() error {
	if c.Manager == "" {
		return errors.New(`"manager" command is missing or empty`)
	}
	seen := make(map[string]bool, len(c.Agents))
	for _, a := range c.Agents {
		if a == nil {
			return errors.New("null agent entry")
		}
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

// Folder returns the folder of the agent (or the manager) with the
// given name: always <root>/<name>.
func Folder(root, name string) string {
	return filepath.Join(root, name)
}

// Parse decodes and validates config bytes. Unknown fields are
// rejected (encoding/json would otherwise silently ignore typos).
func Parse(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("parse config: trailing data after the JSON object")
	}
	if err := checkKeys(data); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// checkKeys rejects keys that differ from the schema only by case
// ("Linked" vs "linked"), which encoding/json would accept silently.
func checkKeys(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if err := exactKeys(root, "manager", "agents"); err != nil {
		return err
	}
	var agents []map[string]json.RawMessage
	if raw, ok := root["agents"]; ok {
		if err := json.Unmarshal(raw, &agents); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}
	for _, a := range agents {
		if err := exactKeys(a, "name", "command", "linked"); err != nil {
			return err
		}
	}
	return nil
}

func exactKeys(obj map[string]json.RawMessage, allowed ...string) error {
	for k := range obj {
		ok := false
		for _, want := range allowed {
			if k == want {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("parse config: unknown field %q", k)
		}
	}
	return nil
}

// WriteNew atomically creates a config file with just the manager
// command. Used once, at first start.
func WriteNew(path, manager string) error {
	c := &Config{Manager: manager, Agents: []*Agent{}}
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
