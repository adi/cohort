package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(`{"manager":"claude --continue","agents":[
		{"name":"api","command":"pi --continue","linked":true},
		{"name":"web_2","command":"codex resume --last"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Manager != "claude --continue" || len(c.Agents) != 2 || !c.Agents[0].Linked || c.Agents[1].Linked {
		t.Fatalf("unexpected config: %+v", c)
	}
	if c.Agent("web_2") == nil || c.Agent("nope") != nil {
		t.Error("Agent lookup")
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"missing manager": `{"agents":[]}`,
		"empty manager":   `{"manager":"","agents":[]}`,
		"unknown field":   `{"manager":"x","agents":[],"extra":1}`,
		"agent unknown":   `{"manager":"x","agents":[{"name":"a","command":"c","harness":"pi"}]}`,
		"wrong-case key":  `{"manager":"x","agents":[{"name":"a","command":"c","Linked":true}]}`,
		"numeric name":    `{"manager":"x","agents":[{"name":"1","command":"c"}]}`,
		"pattern name":    `{"manager":"x","agents":[{"name":"a*","command":"c"}]}`,
		"slash name":      `{"manager":"x","agents":[{"name":"a/b","command":"c"}]}`,
		"leading dash":    `{"manager":"x","agents":[{"name":"-a","command":"c"}]}`,
		"reserved name":   `{"manager":"x","agents":[{"name":"cohort-manager","command":"c"}]}`,
		"empty command":   `{"manager":"x","agents":[{"name":"a","command":""}]}`,
		"duplicate":       `{"manager":"x","agents":[{"name":"a","command":"c"},{"name":"a","command":"c"}]}`,
		"null agent":      `{"manager":"x","agents":[null]}`,
		"comments":        "{\"manager\":\"x\", // hi\n\"agents\":[]}",
		"trailing data":   `{"manager":"x","agents":[]} {}`,
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: accepted %s", name, in)
		}
	}
}

func TestWriteNew(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := WriteNew(p, "claude --continue || claude"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data)
	if err != nil {
		t.Fatalf("written config does not parse: %v\n%s", err, data)
	}
	if c.Manager != "claude --continue || claude" || c.Agents == nil {
		t.Fatalf("unexpected: %+v", c)
	}
	if !strings.Contains(string(data), `"agents": []`) {
		t.Errorf("agents should be written as an empty list:\n%s", data)
	}
	if err := WriteNew(p, ""); err == nil {
		t.Error("empty manager command accepted")
	}
}

func TestFolder(t *testing.T) {
	if got := Folder("/r", "api"); got != "/r/api" {
		t.Errorf("Folder = %q", got)
	}
}
