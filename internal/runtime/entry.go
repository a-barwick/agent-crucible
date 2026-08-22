package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AllowAnyEntryEnv opts out of the sandbox below. It exists for the case where
// the operator really does keep agent files outside the working tree; set it
// only when nothing untrusted can reach the API.
const AllowAnyEntryEnv = "CRUCIBLE_ALLOW_ANY_ENTRY"

// FindEntry resolves a user agent file relative to cwd, the repo root, or
// examples/. The sidecar imports whatever path this returns, and spec.entry can
// arrive from an HTTP request, so the result is confined to the roots below:
// otherwise POST /api/run with {"spec":{"entry":"/etc/cron.d/x"}} is remote code
// execution. Set CRUCIBLE_ALLOW_ANY_ENTRY to lift the confinement.
func FindEntry(entry string) string {
	p, err := ResolveEntry(entry)
	if err != nil {
		// Return the input so the sidecar reports "no such file" rather than
		// the caller silently running some other agent.
		return entry
	}
	return p
}

// ResolveEntry is FindEntry with the reason it refused.
func ResolveEntry(entry string) (string, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", fmt.Errorf("empty entry")
	}
	if strings.ContainsAny(entry, "\x00\n\r") {
		return "", fmt.Errorf("entry contains control characters")
	}
	roots := entryRoots()
	outside := ""
	for _, c := range candidates(entry) {
		if !fileExists(c) {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		// Resolve symlinks before checking: a link inside examples/ pointing
		// at /etc would otherwise pass.
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			real = abs
		}
		if !underAny(real, roots) {
			if outside == "" {
				outside = abs
			}
			continue
		}
		return abs, nil
	}
	if unconfined() {
		for _, c := range candidates(entry) {
			if fileExists(c) {
				if abs, err := filepath.Abs(c); err == nil {
					return abs, nil
				}
				return c, nil
			}
		}
	}
	// "Not found" and "found somewhere I will not import from" are different
	// problems, and an operator who really does keep agent files elsewhere needs
	// to be told which one they have.
	if outside != "" {
		return "", fmt.Errorf("entry %q resolves to %s, outside the roots the runtime imports from (%s); add its directory to %s or set %s",
			entry, outside, strings.Join(roots, string(filepath.ListSeparator)), "CRUCIBLE_ENTRY_ROOTS", AllowAnyEntryEnv)
	}
	return "", fmt.Errorf("entry %q not found under the working tree, the repo root, or examples/", entry)
}

func candidates(entry string) []string {
	var cands []string
	if filepath.IsAbs(entry) {
		cands = append(cands, entry)
	}
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
	return cands
}

// entryRoots is where an agent file is allowed to live: the working directory
// and the repo checkout. Both are places the operator already controls.
func entryRoots() []string {
	var roots []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			if real, err := filepath.EvalSymlinks(abs); err == nil {
				abs = real
			}
			if seen[abs] {
				return
			}
			seen[abs] = true
			roots = append(roots, abs)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	add(FindRepoRoot())
	for _, extra := range filepath.SplitList(os.Getenv("CRUCIBLE_ENTRY_ROOTS")) {
		add(extra)
	}
	return roots
}

func underAny(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func unconfined() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AllowAnyEntryEnv))) {
	case "1", "true", "yes":
		return true
	}
	return false
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
