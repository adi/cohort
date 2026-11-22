package cohort

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"cohort/internal/tmux"
)

// serverDefaults holds the tmux server's files, written into ~/.cohort
// when missing and replaced by a newer version's default as long as
// nobody changed them (syncServerFiles). "@ROOT@" in them is replaced
// by the ~/.cohort path.
//
//go:embed defaults
var serverDefaults embed.FS

// serverFiles are the files cohort keeps in root for the tmux server.
var serverFiles = []struct {
	name string
	mode os.FileMode
}{{"tmux.conf", 0o644}, {"tab-color.sh", 0o755}}

// seedServerFiles brings tmux.conf and tab-color.sh in root up to this
// version's defaults (see syncServerFiles), discarding the report.
func seedServerFiles(root string) error {
	_, err := syncServerFiles(root)
	return err
}

// fileSync is what syncServerFiles did with one file.
type fileSync struct {
	Path    string
	Action  string // "created", "updated", "current", "kept" (user changes; default in Path+".new")
	Changed bool   // Path now has different content
}

func (f fileSync) String() string {
	switch f.Action {
	case "created":
		return "created " + f.Path
	case "updated":
		return "updated " + f.Path + " to this version's default"
	case "kept":
		return "kept " + f.Path + " (it has your changes); this version's default is in " + f.Path + ".new"
	}
	return f.Path + " is up to date"
}

// syncServerFiles writes each server file that is missing, and
// replaces one that is an unmodified default of an earlier version
// (listed in defaults/known.sha256). A file with changes of the
// user's is left alone and the new default written next to it as
// <name>.new.
func syncServerFiles(root string) ([]fileSync, error) {
	known, err := knownDefaults()
	if err != nil {
		return nil, err
	}
	var out []fileSync
	for _, f := range serverFiles {
		dst := filepath.Join(root, f.name)
		tmpl, err := serverDefaults.ReadFile("defaults/" + f.name)
		if err != nil {
			return nil, err
		}
		want := []byte(strings.ReplaceAll(string(tmpl), "@ROOT@", root))
		have, err := os.ReadFile(dst)
		res := fileSync{Path: dst}
		switch {
		case errors.Is(err, fs.ErrNotExist):
			res.Action, res.Changed = "created", true
			err = os.WriteFile(dst, want, f.mode)
		case err != nil:
		case bytes.Equal(have, want):
			res.Action = "current"
			os.Remove(dst + ".new") // a leftover from an earlier version
		case known[f.name+" "+sha256hex(bytes.ReplaceAll(have, []byte(root), []byte("@ROOT@")))]:
			res.Action, res.Changed = "updated", true
			err = writeAtomic(dst, want, f.mode)
		default:
			res.Action = "kept"
			if old, _ := os.ReadFile(dst + ".new"); !bytes.Equal(old, want) {
				err = os.WriteFile(dst+".new", want, f.mode)
			}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// knownDefaults returns the set "<name> <sha256>" of every default
// server file cohort has shipped, as templates (root as @ROOT@).
var knownDefaults = func() (map[string]bool, error) {
	data, err := serverDefaults.ReadFile("defaults/known.sha256")
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) == 2 && !strings.HasPrefix(f[0], "#") {
			known[f[1]+" "+f[0]] = true
		}
	}
	return known, nil
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// RefreshFiles brings ~/.cohort/tmux.conf and tab-color.sh up to this
// version's defaults, prints what it did, and reloads tmux.conf into a
// running server. cohort upgrade runs it from the new binary.
func RefreshFiles() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".cohort")
	res, err := syncServerFiles(root)
	if err != nil {
		return err
	}
	for _, r := range res {
		if r.Action != "current" {
			fmt.Println(r)
		}
	}
	if reloadIfChanged(tmux.New(SocketName, filepath.Join(root, "tmux.conf")), res) {
		fmt.Println("reloaded tmux.conf into the running server")
	}
	return nil
}

// reloadIfChanged sources tmux.conf into a running server when the
// sync changed it, reporting whether it did.
func reloadIfChanged(srv *tmux.Server, res []fileSync) bool {
	for _, r := range res {
		if r.Changed && filepath.Base(r.Path) == "tmux.conf" && srv.HasServer() {
			return srv.SourceFile() == nil
		}
	}
	return false
}

// managerSeed holds the cohort manager's starter files: its CLAUDE.md,
// skills, and team tooling (teams.json, bin/team, the cohort-team mod).
// Files under bin/ are written executable. The manager is the one place cohort knows a harness
// (claude code), so its instructions ship with cohort.
//
//go:embed all:managerseed
var managerSeed embed.FS

const seedRoot = "managerseed"

// seedManager writes the starter files into the manager's folder and
// commits them, but only while the folder's repo has no commits yet
// (a new folder). Existing files are never overwritten; after the
// first commit the files belong to the manager.
func (d *Daemon) seedManager(folder string) error {
	if _, err := git(folder, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		return nil
	}
	var written []string
	err := fs.WalkDir(managerSeed, seedRoot, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, seedRoot+"/")
		dst := filepath.Join(folder, filepath.FromSlash(rel))
		if _, err := os.Stat(dst); !errors.Is(err, fs.ErrNotExist) {
			return err // exists (nil) or can't tell
		}
		data, err := managerSeed.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(rel, "bin/") {
			mode = 0o755
		}
		if err := os.WriteFile(dst, data, mode); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	})
	if err != nil || len(written) == 0 {
		return err
	}
	if _, err := git(folder, append([]string{"add", "--"}, written...)...); err != nil {
		return err
	}
	if _, err := git(folder, "-c", "user.name=cohort", "-c", "user.email=cohort@localhost",
		"-c", "commit.gpgsign=false", "commit", "--no-verify", "-q",
		"-m", "cohort: seed manager instructions"); err != nil {
		return err
	}
	d.log.Printf("seeded manager instructions in %s: %s", folder, strings.Join(written, ", "))
	return nil
}
