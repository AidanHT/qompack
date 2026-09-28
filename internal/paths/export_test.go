package paths

import (
	"path/filepath"
	"strings"
)

// ForgetEntriesUnder drops every entry-ledger record at or below root, so a test can model a fresh
// process — one whose ledger knows nothing about a tree an earlier process left on disk — without
// disturbing the records other tests' trees hold.
func ForgetEntriesUnder(root string) {
	entries.mu.Lock()
	defer entries.mu.Unlock()
	r := filepath.Clean(root)
	for k := range entries.m {
		if k == r || strings.HasPrefix(k, r+string(filepath.Separator)) {
			delete(entries.m, k)
		}
	}
}
