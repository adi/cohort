package cohort

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fakeReleases(t *testing.T, tag string, bin []byte, sum string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	})
	dl := "/" + Repo + "/releases/download/" + tag + "/"
	mux.HandleFunc(dl+AssetName(), func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux.HandleFunc(dl+"SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  cohort-other-arch\n%s  %s\n", sum, sum, AssetName())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldAPI, oldDL := apiBase, downloadBase
	apiBase, downloadBase = srv.URL, srv.URL
	t.Cleanup(func() { apiBase, downloadBase = oldAPI, oldDL })
}

func TestUpgradeInstall(t *testing.T) {
	bin := []byte("#!/bin/sh\necho new\n")
	s := sha256.Sum256(bin)
	fakeReleases(t, "v9.9.9", bin, hex.EncodeToString(s[:]))
	tag, err := latestTag()
	if err != nil || tag != "v9.9.9" {
		t.Fatalf("latestTag: %q %v", tag, err)
	}
	dst := filepath.Join(t.TempDir(), "cohort")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := install(tag, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	st, _ := os.Stat(dst)
	if string(got) != string(bin) || st.Mode()&0o100 == 0 {
		t.Fatalf("installed %q mode %v", got, st.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestUpgradeRejectsBadChecksum(t *testing.T) {
	fakeReleases(t, "v9.9.9", []byte("tampered"), "00")
	dst := filepath.Join(t.TempDir(), "cohort")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := install("v9.9.9", dst); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if got, _ := os.ReadFile(dst); string(got) != "old" {
		t.Fatalf("binary replaced: %q", got)
	}
}
