package cohort

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// managerSeed holds the cohort manager's starter files: its CLAUDE.md
// and skills. The manager is the one place cohort knows a harness
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
		if err := os.WriteFile(dst, data, 0o644); err != nil {
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
