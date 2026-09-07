package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/paths"
)

// ValidatePointers is the ground-truth check of G2.5: every FILE pointer in p is checked against
// the working tree at root and against git's own on-disk state (read in pure Go — see
// gitindex.go — because this package may not import os/exec), and a DropEntry is returned for
// each finding. A checkpoint that points at a deleted file is worse than one that admits it
// dropped the pointer.
//
// Three checks per file pointer, in order:
//
//  1. Existence / type. A path that fails paths.Norm (escapes the project root) is
//     pointer_invalid; an absent file is pointer_missing; a directory is pointer_invalid.
//  2. Working-tree drift vs the parsed .git/index: a size or mtime-seconds mismatch is
//     pointer_dirty, and a file present on disk but absent from the index is pointer_untracked.
//  3. Removal is the CALLER's: Finalize removes only pointer_missing and pointer_invalid.
//     pointer_dirty and pointer_untracked are informational and the pointer is KEPT — a dirty
//     file is exactly the file the agent is working on.
//
// When git state is unavailable (no .git at all, or an index this package cannot parse), check 1
// still runs for every pointer and ONE pointer_git_unavailable entry carries the reason, so the
// condition is visible rather than silent. Index lookups are keyed by paths.Key so
// case-insensitive filesystems match (§4), and the whole index is read once per call.
//
// Tool pointers are not checked here: Finalize validates them against the store, where the store
// is in scope.
//
// The only error ValidatePointers returns is ctx's own cancellation; every other condition is a
// DropEntry.
func ValidatePointers(ctx context.Context, root string, p Pointers) ([]DropEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	branch, index, gitErr := readGitState(root)

	var drops []DropEntry
	for _, ptr := range p.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		rel, err := paths.Norm(root, ptr.Path)
		if err != nil {
			drops = append(drops, DropEntry{Kind: dropPointerInvalid, ID: ptr.Path, Detail: "path escapes the project root"})
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			drops = append(drops, DropEntry{Kind: dropPointerMissing, ID: ptr.Path, Detail: "file no longer exists in the working tree"})
			continue
		case err != nil:
			// Neither provably present nor provably missing (a permission failure, say). The
			// pointer is kept: only proof may remove it, and re_read will surface the same
			// failure to whoever follows the pointer.
			continue
		case info.IsDir():
			drops = append(drops, DropEntry{Kind: dropPointerInvalid, ID: ptr.Path, Detail: "path is a directory"})
			continue
		}

		if gitErr != nil {
			continue // check 1 only; the gap is reported once, below
		}
		entry, tracked := index[paths.Key(rel)]
		if !tracked {
			drops = append(drops, DropEntry{Kind: dropPointerUntracked, ID: ptr.Path, Detail: "not tracked by git"})
			continue
		}
		if int64(entry.Size) != info.Size() || entry.MTimeSec != uint32(info.ModTime().Unix()) {
			drops = append(drops, DropEntry{
				Kind:   dropPointerDirty,
				ID:     ptr.Path,
				Detail: fmt.Sprintf("modified since index on %s", branch),
			})
		}
	}

	if gitErr != nil {
		drops = append(drops, DropEntry{Kind: dropPointerGitUnavailable, ID: "", Detail: gitErr.Error()})
	}
	return drops, nil
}
