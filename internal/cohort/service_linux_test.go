package cohort

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The unit keeps agents across restarts and crashes, and stops them
// only on a stop job.
func TestUnitKeepsAgentsAcrossRestart(t *testing.T) {
	data, err := os.ReadFile("../../contrib/cohort.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	for _, want := range []string{
		"\nKillMode=process\n",
		"ExecStopPost=/bin/sh -c 'systemctl --user list-jobs --no-legend %n | grep -qw stop && tmux -L " + SocketName + " kill-server; exit 0'",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
}

func TestRefreshService(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	newUnit := []byte("[Service]\nKillMode=process\n")
	s := sha256.Sum256(newUnit)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(newUnit) }))
	defer srv.Close()
	old := downloadBase
	downloadBase = srv.URL
	defer func() { downloadBase = old }()
	reloads := 0
	oldReload := daemonReload
	daemonReload = func() error { reloads++; return nil }
	defer func() { daemonReload = oldReload }()
	sums := map[string]string{"cohort.service": hex.EncodeToString(s[:])}

	// Not installed as a service: left alone.
	if err := refreshService("v1", sums); err != nil || reloads != 0 {
		t.Fatalf("no unit: %v, %d reloads", err, reloads)
	}
	unit := filepath.Join(home, ".config/systemd/user/cohort.service")
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refreshService("v1", sums); err != nil || reloads != 1 {
		t.Fatalf("refresh: %v, %d reloads", err, reloads)
	}
	if got, _ := os.ReadFile(unit); string(got) != string(newUnit) {
		t.Fatalf("unit: %q", got)
	}
	// Already current: no reload.
	if err := refreshService("v1", sums); err != nil || reloads != 1 {
		t.Fatalf("unchanged: %v, %d reloads", err, reloads)
	}
	// A bad checksum leaves the unit alone.
	if err := os.WriteFile(unit, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refreshService("v1", map[string]string{"cohort.service": "00"}); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if got, _ := os.ReadFile(unit); string(got) != "mine\n" {
		t.Fatalf("unit replaced: %q", got)
	}
}
