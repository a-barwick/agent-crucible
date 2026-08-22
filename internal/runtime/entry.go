package runtime

import (
	"os"
	"path/filepath"
)

// FindEntry resolves a user agent file relative to cwd, the repo root,
// or examples/. The sidecar imports whatever path this returns.
func FindEntry(entry string) string {
	if entry == "" {
		return ""
	}
	if filepath.IsAbs(entry) && fileExists(entry) {
		return entry
	}
	var cands []string
	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for i := 0; i < 8; i++ {
			cands = append(cands,
				filepath.Join(dir, entry),
				filepath.Join(dir, "examples", filepath.Base(entry)),
			)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if root := FindDir(); root != "" {
		repo := filepath.Dir(root)
		cands = append(cands,
			filepath.Join(repo, entry),
			filepath.Join(repo, "examples", filepath.Base(entry)),
			filepath.Join(root, entry),
		)
	}
	for _, c := range cands {
		if fileExists(c) {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs
			}
			return c
		}
	}
	return entry
}

func FindRepoRoot() string {
	if d := FindDir(); d != "" {
		return filepath.Dir(d)
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
