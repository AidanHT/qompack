package paths

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// Norm returns p as a project-relative, forward-slash, cleaned path: the canonical form every
// store key, DAG node and depends_on entry uses (§4). p may be absolute or relative to
// projectRoot. Symlinks are resolved, but only when doing so keeps the result inside
// projectRoot — a symlink that points outside is left unresolved rather than followed, so Norm
// can never be used to smuggle an outside path back in under a legitimate-looking relative name.
// A path that escapes projectRoot, before or after symlink resolution, is rejected with an error
// wrapping core.ErrNotFound.
func Norm(projectRoot, p string) (string, error) {
	if projectRoot == "" {
		return "", fmt.Errorf("%w: empty project root", core.ErrNotFound)
	}
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(projectRoot, p)
	}
	abs = filepath.Clean(abs)
	if r, err := filepath.EvalSymlinks(abs); err == nil { // resolve only when it stays inside root
		if inside(projectRoot, r) {
			abs = r
		}
	}
	rel, err := filepath.Rel(filepath.Clean(projectRoot), abs)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%w: path escapes project root: %s", core.ErrNotFound, p)
	}
	return rel, nil
}

// inside reports whether r is projectRoot itself, or lies under it, once both are cleaned. Norm
// uses it to decide whether a resolved symlink target is safe to adopt.
func inside(projectRoot, r string) bool {
	root := filepath.Clean(projectRoot)
	r = filepath.Clean(r)
	if r == root {
		return true
	}
	rel, err := filepath.Rel(root, r)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel != ".." && !strings.HasPrefix(rel, "../")
}

// DefaultFold reports whether Key should case-fold on this platform: true on Windows and macOS,
// whose default filesystems are case-insensitive, false everywhere else (§4).
func DefaultFold() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// KeyFold returns p lowercased when fold is true, and p unchanged otherwise. It exists so golden
// fixtures can pin a fold setting and stay byte-identical across platforms; production code
// calls Key. Original casing is never destroyed by either — callers keep the Norm result
// alongside whichever Key they compute from it.
func KeyFold(p string, fold bool) string {
	if fold {
		return strings.ToLower(p)
	}
	return p
}

// Key returns the dedup key for p: KeyFold using this platform's DefaultFold. All store keys,
// DAG file nodes, depends_on entries and glob matching use Key.
func Key(p string) string { return KeyFold(p, DefaultFold()) }
