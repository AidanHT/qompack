package paths

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
)

// gitMarker is the name every git working tree — including a worktree, where it is a file
// instead of a directory — carries at its root.
const gitMarker = ".git"

// Resolve determines the Qompack project root, in the order fixed by §3.3:
//
//  1. QOMPACK_PROJECT_ROOT, read through getenv, if it is non-empty (tests, CI, explicit
//     override).
//  2. payloadCWD walked upward to the nearest ancestor containing .git — a directory in an
//     ordinary checkout, or a file in a git worktree (the file holds "gitdir: …"; Resolve only
//     needs to know something named .git exists, not to follow the indirection itself).
//  3. payloadCWD itself, if no .git is found anywhere above it.
//
// getenv is injected so Resolve never reads the process environment directly: tests, the CLI and
// the daemon's per-project cache each hand it whatever lookup is appropriate.
func Resolve(getenv func(string) string, payloadCWD string) (string, error) {
	if v := getenv("QOMPACK_PROJECT_ROOT"); v != "" {
		return filepath.Abs(v)
	}
	if payloadCWD == "" {
		return "", fmt.Errorf("%w: no project root", core.ErrNotFound)
	}
	abs, err := filepath.Abs(payloadCWD)
	if err != nil {
		return "", err
	}
	for d := abs; ; {
		if fi, statErr := os.Stat(Long(filepath.Join(d, gitMarker))); statErr == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return abs, nil
}

// Global returns the cross-project layer's root, <home>/.qompack (§3.3): the user-global config
// layer, the daemon registry and the token-estimator calibration file all live under it. It is
// never resolved through Resolve — QOMPACK_PROJECT_ROOT and the .git walk apply only to the
// per-project store, never to the cross-project one.
func Global(home string) string {
	return filepath.Join(home, dotDir)
}
