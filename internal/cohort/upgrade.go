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
// The daemon keeps running the old binary until the service restarts,
// which the user does (agents survive it).
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
	sums, err := releaseSums(tag)
	if err != nil {
		return err
	}
	if err := install(tag, exe, sums); err != nil {
		return err
	}
	fmt.Printf("upgraded %s: %s -> %s\n", exe, current, tag)
	if err := refreshService(tag, sums); err != nil {
		fmt.Printf("warning: service file not updated: %v\n", err)
	}
	fmt.Println("The running daemon keeps the old version until the service restarts")
	fmt.Println("(agents keep running):")
	fmt.Println("  " + restartHint)
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

func releaseURL(tag, name string) string {
	return downloadBase + "/" + Repo + "/releases/download/" + tag + "/" + name
}

// releaseSums fetches tag's SHA256SUMS as file name -> hex digest.
func releaseSums(tag string) (map[string]string, error) {
	data, err := fetch(releaseURL(tag, "SHA256SUMS"))
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			sums[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	return sums, nil
}

// fetchVerified downloads one release file and checks it against sums.
func fetchVerified(tag, name string, sums map[string]string) ([]byte, error) {
	want, ok := sums[name]
	if !ok {
		return nil, fmt.Errorf("%s not in the release's SHA256SUMS", name)
	}
	data, err := fetch(releaseURL(tag, name))
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s: checksum mismatch", name)
	}
	return data, nil
}

// writeAtomic replaces dst with data through a temp file in its folder.
func writeAtomic(dst string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cohort-upgrade-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// install downloads tag's binary, checks it against the release's
// SHA256SUMS and atomically replaces dst with it.
func install(tag, dst string, sums map[string]string) error {
	bin, err := fetchVerified(tag, AssetName(), sums)
	if err != nil {
		return err
	}
	return writeAtomic(dst, bin, 0o755)
}
