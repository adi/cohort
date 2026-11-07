package cohort

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are fetched from.
const Repo = "adi/cohort"

// Release endpoints; variables so tests can point them at a local server.
var (
	apiBase      = "https://api.github.com"
	downloadBase = "https://github.com"
	httpClient   = &http.Client{Timeout: 2 * time.Minute}
)

// AssetName is the release asset holding the binary for this platform.
func AssetName() string {
	return "cohort-" + runtime.GOOS + "-" + runtime.GOARCH
}

// Upgrade replaces the running binary with the latest GitHub release.
// The daemon keeps running the old binary until the service restarts;
// cohort doesn't restart it, since that restarts every agent.
func Upgrade(current string) error {
	tag, err := latestTag()
	if err != nil {
		return fmt.Errorf("find latest release: %w", err)
	}
	if tag == current {
		fmt.Printf("cohort %s is the latest release\n", current)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if err := install(tag, exe); err != nil {
		return err
	}
	fmt.Printf("upgraded %s: %s -> %s\n", exe, current, tag)
	fmt.Println("The running daemon keeps the old version until the service restarts:")
	fmt.Println("  " + restartHint + "   # restarts every agent too")
	return nil
}

func latestTag() (string, error) {
	req, err := http.NewRequest("GET", apiBase+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API: %s", resp.Status)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", errors.New("GitHub API: release has no tag")
	}
	return r.TagName, nil
}

func fetch(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// install downloads tag's binary, checks it against the release's
// SHA256SUMS and atomically replaces dst with it.
func install(tag, dst string) error {
	base := downloadBase + "/" + Repo + "/releases/download/" + tag + "/"
	bin, err := fetch(base + AssetName())
	if err != nil {
		return err
	}
	sums, err := fetch(base + "SHA256SUMS")
	if err != nil {
		return err
	}
	want := ""
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == AssetName() {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("%s not in the release's SHA256SUMS", AssetName())
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s: checksum mismatch", AssetName())
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cohort-upgrade-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
