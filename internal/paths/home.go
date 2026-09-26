package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ErrHomeRoot is what every entry point refuses a session with when its project root is the user's
// home directory (owner decision D18, 00-ARCHITECTURE.md §3.3).
//
// Resolve walks up to the nearest .git and otherwise takes the working directory, so a session
// started in the home directory itself, or anywhere below a home that is a git work tree (a
// dotfiles repository), resolves to the home directory. The project store would then be
// <home>/.qompack, which is the user-global layer's own directory: its config.json, calibration
// file, fallback logs and the staged daemon copies under bin/. D18 keeps that directory the
// user-global layer only, so such a session records nothing and says why.
var ErrHomeRoot = errors.New("qompack: the project root is the home directory")

// homeEnvKeys are the environment variables a home directory is read from. os.UserHomeDir reads
// USERPROFILE on Windows and HOME elsewhere; both are read on every platform, because a POSIX
// layer on Windows (Git Bash, MSYS, Cygwin) exports a HOME that the user-global layer's own loader
// prefers, and the refusal must cover every directory that layer can live in.
var homeEnvKeys = []string{"HOME", "USERPROFILE"}

// HomeDirs returns the distinct, non-empty home directories getenv names, HOME first. getenv is
// injected for the reason Resolve's is: tests and the CLI hand it the lookup they already use, so
// this never reads the process environment itself.
func HomeDirs(getenv func(string) string) []string {
	var out []string
	for _, k := range homeEnvKeys {
		if v := getenv(k); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// IsHome reports whether root names the same directory as any of homes (D18).
//
// Both sides are made absolute and cleaned first, so a trailing separator, a `..` element or a
// relative spelling such as `--project .` typed in the home directory are all the home directory.
// The comparison is then made twice:
//
//  1. by spelling, case-insensitively on Windows, where NTFS names are. This is the common case
//     and costs no system call;
//  2. by identity, os.SameFile over os.Stat of each side. os.Stat follows symbolic links and, on
//     Windows, junctions, so a root that reaches the home directory through a link — or a home
//     that is itself a link — is caught however either is spelled. It is also what makes a case
//     variant of home match on a case-insensitive macOS volume. On Windows a creation-time
//     comparison (mayBeSameDir) rules out an ordinary project before os.SameFile opens anything.
//
// A home candidate that is empty or not absolute names no directory and is ignored: resolving a
// HOME of "." against the working directory would refuse whatever project a session started in.
// An error on either Stat answers false. Such a root does not exist as a directory, and every
// entry point already refuses to create a store for a root that does not exist.
func IsHome(root string, homes ...string) bool {
	if root == "" {
		return false
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidates := make([]string, 0, len(homes))
	for _, h := range homes {
		if h == "" || !filepath.IsAbs(h) {
			continue
		}
		h = filepath.Clean(h)
		if sameSpelling(abs, h) {
			return true
		}
		candidates = append(candidates, h)
	}
	if len(candidates) == 0 {
		return false
	}
	rootInfo, err := os.Stat(Long(abs))
	if err != nil || !rootInfo.IsDir() {
		return false
	}
	for _, h := range candidates {
		homeInfo, herr := os.Stat(Long(h))
		if herr == nil && mayBeSameDir(rootInfo, homeInfo) && os.SameFile(rootInfo, homeInfo) {
			return true
		}
	}
	return false
}

// sameSpelling compares two cleaned absolute paths the way this platform's default filesystem
// compares names: case-insensitively on Windows, exactly elsewhere. It is only the fast path;
// IsHome's identity check is what decides every spelling this one cannot.
func sameSpelling(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// RefuseHome returns nil, or an error wrapping ErrHomeRoot that names root and says how to fix it,
// when IsHome(root, homes...) holds. It is the refusal every non-hook entry point reports; a hook
// asks IsHome directly, because a hook always exits 0 and answers the host instead of printing.
func RefuseHome(root string, homes ...string) error {
	if !IsHome(root, homes...) {
		return nil
	}
	return fmt.Errorf("%w (%s): Qompack records nothing there; open a project directory "+
		"(one with its own .git), or set QOMPACK_PROJECT_ROOT to one", ErrHomeRoot, root)
}
