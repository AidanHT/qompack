//go:build !windows

package hostperm

import (
	"os"
	"strings"
)

// osAlias reports no alias: a POSIX filesystem opens a path under the spelling it is given, apart
// from the case folding and the symlinks Evaluate already handles. No name there is an alias that
// fails to resolve, so unresolved is always false.
func osAlias(string) (alias string, unresolved bool) { return "", false }

// osShortName reports p itself: a POSIX filesystem records no 8.3 names, so every name is known.
func osShortName(p string) (short string, complete bool) { return p, true }

// osFullPath reports p itself: a POSIX system opens a clean absolute path as spelled.
func osFullPath(p string) (string, bool) { return p, true }

// entryName reports seg itself: a POSIX file name may hold any byte but `/` and NUL.
func entryName(seg string) string { return seg }

// trimmedName reports name itself: a POSIX system trims nothing from a name it opens.
func trimmedName(name string) string { return name }

// unexpandable reports false: osAlias reports nothing unresolved on a POSIX system, which
// records no 8.3 names (`HEAD~1` is a name like any other).
func unexpandable(string) bool { return false }

// listNames returns what dir's listing says about each name in it (Evaluator), case-folded when
// fold is set, or nil when dir cannot be listed. A POSIX filesystem records no 8.3 aliases, and
// its names compare as bytes (or, on macOS, are not compared when non-ASCII: plainSegment), so wide
// is always nil.
func listNames(dir string, fold bool) (names map[string]entryKind, wide []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	names = make(map[string]entryKind, len(entries))
	for _, en := range entries {
		n := en.Name()
		if fold {
			n = strings.ToLower(n)
		}
		kind := entryOwn
		if en.Type()&os.ModeSymlink != 0 {
			kind |= entryLink
		}
		names[n] |= kind
	}
	return names, nil
}
