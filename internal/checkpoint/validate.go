package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
	res := newPointerResolver(root)

	var drops []DropEntry
	for _, ptr := range p.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		rel, info, ok, err := res.resolve(ptr.Path)
		if !ok {
			drops = append(drops, DropEntry{Kind: dropPointerInvalid, ID: ptr.Path, Detail: "path escapes the project root"})
			continue
		}
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

// pointerResolver turns each file pointer's path into the project-relative form the checks key
// on, giving paths.Norm's answer while paying for paths.Norm only where the answer can depend on
// it.
//
// SP10-D1 (plans/V2-SP-10-carried-defects.md). Norm resolves its WHOLE argument through
// filepath.EvalSymlinks — every component of the project root, once for the root and again for
// the pointer — and on Windows each component is an Lstat plus a FindFirstFile to recover its
// on-disk spelling. Over the §13 fixture that was 200 pointers × ~20 components × two syscalls,
// most of Finalize's time against a 50 ms budget, and none of it could change what
// ValidatePointers does with the result: the escape verdict, whether the file is there, and which
// index entry it is compared with.
//
// The fast path takes a pointer that is lexically inside the root and whose every component below
// the root is a plain directory or a plain file — no symlink, no reparse point, nothing irregular —
// established with one Lstat per DISTINCT directory (cached for the call) plus the Lstat the check
// needs anyway. For such a path Norm's answer can differ from the lexical relative path only in the
// per-component spelling toNorm recovers on Windows, and neither consumer of the answer can see
// that: Lstat matches names case-insensitively on exactly the filesystems where toNorm changes
// anything, and the index lookup goes through paths.Key, which folds case on exactly those
// platforms (paths.DefaultFold). Every pointer the writer mints is already a Norm output
// (observer/tooluse.go keys the DAG with it), for which the two are identical byte for byte.
//
// Everything else takes the exact path through paths.Norm and behaves as it always did, syscall
// for syscall: an absolute path or one with a volume, a lexical escape, the root itself, a symlink
// or other non-plain entry anywhere under the root, an Lstat that fails for any reason other than
// absence, and the two shapes toNorm would respell on Windows beyond case — an 8.3 alias (a
// component containing '~') and a component ending in '.' or ' ', which Win32 strips. An absent
// file IS handled here — Norm's EvalSymlinks fails on it and Norm falls back to the lexical form,
// which is what this returns — because a missing file is the common finding this check exists to
// make.
type pointerResolver struct {
	// root is the project root exactly as the caller spelled it, for Norm and for Lstat; clean
	// is filepath.Clean of it, the base every lexical comparison is measured from — the same
	// base Norm uses before it adopts a resolved target.
	root, clean string
	// plainDirs caches, per absolute cleaned directory below root, whether that directory and
	// every ancestor of it below root is present and plain: a directory and nothing else.
	plainDirs map[string]bool
}

// newPointerResolver starts a resolver for one ValidatePointers call. The cache is per call on
// purpose: the tree may change between calls, and Norm consults it afresh every time.
func newPointerResolver(root string) *pointerResolver {
	return &pointerResolver{root: root, clean: filepath.Clean(root), plainDirs: map[string]bool{}}
}

// resolve returns p in the project-relative slash form the checks key on and os.Lstat's answer
// for the file it names. ok is false when p escapes the project root, in which case nothing else
// is meaningful. err is the Lstat's, nil when info is set.
func (r *pointerResolver) resolve(p string) (rel string, info fs.FileInfo, ok bool, err error) {
	if rel, info, err, done := r.fast(p); done {
		return rel, info, true, err
	}
	rel, nerr := paths.Norm(r.root, p)
	if nerr != nil {
		return "", nil, false, nil
	}
	info, err = os.Lstat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return rel, info, true, err
}

// fast is the lexical path described on pointerResolver. done is false whenever the answer might
// depend on resolution it did not do, and the caller then asks Norm.
func (r *pointerResolver) fast(p string) (rel string, info fs.FileInfo, err error, done bool) {
	if r.root == "" || p == "" || os.IsPathSeparator(p[0]) || filepath.IsAbs(p) ||
		filepath.VolumeName(p) != "" || !plainComponents(p) {
		return "", nil, nil, false
	}
	// The same lexical form Norm computes before it looks at the filesystem, so the two agree
	// on what "inside" means.
	abs := filepath.Join(r.clean, p)
	lex, rerr := filepath.Rel(r.clean, abs)
	if rerr != nil || lex == "." || lex == ".." || strings.HasPrefix(lex, ".."+string(filepath.Separator)) {
		return "", nil, nil, false
	}
	if !r.plainDir(filepath.Dir(abs)) {
		return "", nil, nil, false
	}
	info, err = os.Lstat(abs)
	switch {
	case err == nil && (info.Mode().IsRegular() || info.Mode().Type() == fs.ModeDir):
		return filepath.ToSlash(lex), info, nil, true
	case errors.Is(err, fs.ErrNotExist):
		return filepath.ToSlash(lex), nil, err, true
	}
	return "", nil, nil, false
}

// plainComponents reports whether every component of p is one the fast path may take at its
// spelling: nothing containing '~' (a Windows 8.3 alias, which toNorm expands to the long name)
// and nothing ending in '.' or ' ' (which Win32 strips before it looks the name up, so
// FindFirstFile would answer with a different spelling than the one asked). Either separator
// counts, because filepath.Clean accepts both on Windows and p has not been cleaned yet.
func plainComponents(p string) bool {
	if strings.ContainsRune(p, '~') {
		return false
	}
	for _, c := range strings.FieldsFunc(p, func(c rune) bool { return c == '/' || c == os.PathSeparator }) {
		if strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ") {
			return false
		}
	}
	return true
}

// plainDir reports whether dir — an absolute, cleaned directory at or below the root — is the
// root itself or a present, plain directory whose every ancestor below the root is one too.
// Each directory is asked of the filesystem once per resolver.
func (r *pointerResolver) plainDir(dir string) bool {
	if dir == r.clean {
		return true
	}
	if plain, seen := r.plainDirs[dir]; seen {
		return plain
	}
	parent := filepath.Dir(dir)
	plain := parent != dir && r.plainDir(parent)
	if plain {
		fi, err := os.Lstat(dir)
		plain = err == nil && fi.Mode().Type() == fs.ModeDir
	}
	r.plainDirs[dir] = plain
	return plain
}
