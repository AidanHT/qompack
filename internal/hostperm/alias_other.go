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

// respelled reports false: a POSIX filesystem opens a path as spelled (osAlias).
func respelled(string) bool { return false }

// listNames returns what dir's listing says about each name in it (Evaluator), case-folded when
// fold is set, or nil when dir cannot be listed. A POSIX filesystem records no 8.3 aliases.
func listNames(dir string, fold bool) map[string]entryKind {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make(map[string]entryKind, len(entries))
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
	return names
}
