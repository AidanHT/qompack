package checkpoint

// A pure-Go reader for git's own on-disk state (SP-10 §11, G2.5). Package checkpoint may not
// import os/exec (the security CI job restricts it to daemon, cli and tools/), so "checked
// against `git status`" means reading .git/HEAD and .git/index directly. Only what pointer
// validation compares is parsed: per entry, the path, the size and the mtime seconds.
//
// Format reference: git's index-format documentation. Header: 4-byte magic "DIRC", big-endian
// uint32 version, big-endian uint32 entry count. Versions 2 and 3 are supported; version 4
// (path-prefix compression) and anything unrecognized are refused with errIndexUnsupported so
// ValidatePointers can degrade to existence checks VISIBLY rather than guessing.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/paths"
)

var (
	// errNoGitDir reports that <root>/.git does not exist at all: the project simply is not a
	// git checkout. ValidatePointers turns it into one pointer_git_unavailable entry.
	errNoGitDir = errors.New("checkpoint: no .git at the project root")
	// errIndexUnsupported reports git state that exists but cannot be relied on: an index
	// version this reader does not speak, a corrupt or truncated file, or one over the size cap.
	// Same visible-drop treatment as errNoGitDir — never a silent skip.
	errIndexUnsupported = errors.New("checkpoint: git index unsupported")
)

const (
	// maxIndexBytes caps how large a .git/index this package will read: 64 MiB, refused before
	// the read so a pathological index cannot balloon the checkpointer (§11).
	maxIndexBytes = 64 << 20

	// indexMagic opens every git index file.
	indexMagic = "DIRC"
	// indexHeaderLen is magic + version + entry count.
	indexHeaderLen = 12
	// indexEntryFixedLen is the fixed prefix of a version-2 entry: 10 big-endian uint32 stat
	// words (ctime_sec, ctime_nsec, mtime_sec, mtime_nsec, dev, ino, mode, uid, gid, size),
	// a 20-byte SHA-1 object id and the uint16 flags word.
	indexEntryFixedLen = 62
	// indexEntryMTimeOff and indexEntrySizeOff locate the two stat words validation compares,
	// relative to the entry start.
	indexEntryMTimeOff = 8
	indexEntrySizeOff  = 36
	// indexEntryFlagsOff locates the flags word, relative to the entry start.
	indexEntryFlagsOff = 60

	// indexFlagExtended is the 0x4000 bit of the flags word: when set on a version-3 entry, one
	// extra uint16 of extended flags follows before the path.
	indexFlagExtended = uint16(1) << 14
	// indexNameMask is the 0x0FFF path-length field of the flags word; a value of exactly
	// indexNameMask means "longer than fits here: read the path to its NUL terminator instead".
	indexNameMask = uint16(1)<<12 - 1

	// gitDirFilePrefix is what a worktree's .git FILE starts with; the remainder is the real
	// git dir, possibly relative to the root.
	gitDirFilePrefix = "gitdir: "
	// headRefPrefix is what an attached HEAD starts with; the remainder is the branch name.
	headRefPrefix = "ref: refs/heads/"
	// detachedHeadLen is how much of a detached HEAD's object id names the state in a drop
	// detail: the same 12-character short form git itself abbreviates to.
	detachedHeadLen = 12
)

// indexEntry is one parsed .git/index entry, reduced to the three fields pointer validation
// compares: the repository-relative path (always forward-slash, as git stores it), the recorded
// size, and the recorded mtime seconds.
type indexEntry struct {
	Path     string
	Size     uint32
	MTimeSec uint32
}

// resolveGitDir locates the git directory for root: <root>/.git when that is a directory, or —
// when .git is a FILE, as in a linked worktree — the "gitdir: " path it names, cleaned and
// joined against root when relative. An absent .git is errNoGitDir; a .git that exists but
// cannot be used is errIndexUnsupported with the reason.
func resolveGitDir(root string) (string, error) {
	p := filepath.Join(root, ".git")
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", errNoGitDir
	}
	if err != nil {
		return "", fmt.Errorf("%w: stat .git: %v", errIndexUnsupported, err)
	}
	if info.IsDir() {
		return p, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("%w: reading .git file: %v", errIndexUnsupported, err)
	}
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, gitDirFilePrefix) {
		return "", fmt.Errorf("%w: .git file without a %q prefix", errIndexUnsupported, gitDirFilePrefix)
	}
	dir := strings.TrimSpace(strings.TrimPrefix(s, gitDirFilePrefix))
	if dir == "" {
		return "", fmt.Errorf("%w: .git file names an empty gitdir", errIndexUnsupported)
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir), nil
}

// parseHead renders the branch state a HEAD file describes: the branch name when HEAD is an
// attached "ref: refs/heads/..." symref, and "detached@" plus the first 12 characters otherwise.
func parseHead(b []byte) string {
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, headRefPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(s, headRefPrefix))
	}
	if len(s) > detachedHeadLen {
		s = s[:detachedHeadLen]
	}
	return "detached@" + s
}

// readGitState reads everything ValidatePointers needs from root's git checkout in one pass:
// the branch HEAD points at and the index entries keyed by paths.Key, so lookups match on the
// case-insensitive filesystems where the working tree itself would (§4). The index is read once
// per call and capped at maxIndexBytes.
//
// An unreadable HEAD degrades the branch name to "unknown" rather than failing: the branch only
// flavors a pointer_dirty detail, while the index is the check itself.
//
// HEAD and the index are read shared (paths.ReadFileShared). Git replaces both by renaming a
// .lock file over them: a checkout or switch rewrites HEAD, and add, commit and an
// index-refreshing status (which editors run in the background) rewrite the index, up to 64 MiB
// of which is read here. On Windows an ordinary handle makes that rename fail for as long as the
// read lasts, which git retries and, if the read outlasts its retries, reports as a failed rename
// (test/guards' sharedReaders).
func readGitState(root string) (string, map[string]indexEntry, error) {
	gitDir, err := resolveGitDir(root)
	if err != nil {
		return "", nil, err
	}

	branch := "unknown"
	if hb, err := paths.ReadFileShared(filepath.Join(gitDir, "HEAD")); err == nil {
		branch = parseHead(hb)
	}

	idxPath := filepath.Join(gitDir, "index")
	info, err := os.Stat(idxPath)
	if err != nil {
		return "", nil, fmt.Errorf("%w: reading index: %v", errIndexUnsupported, err)
	}
	if info.Size() > maxIndexBytes {
		return "", nil, fmt.Errorf("%w: index is %d bytes, over the 64 MiB cap", errIndexUnsupported, info.Size())
	}
	raw, err := paths.ReadFileShared(idxPath)
	if err != nil {
		return "", nil, fmt.Errorf("%w: reading index: %v", errIndexUnsupported, err)
	}
	entries, err := parseIndex(raw)
	if err != nil {
		return "", nil, err
	}
	m := make(map[string]indexEntry, len(entries))
	for _, e := range entries {
		m[paths.Key(e.Path)] = e
	}
	return branch, m, nil
}

// parseIndex decodes a version-2 or version-3 git index. Every failure — unknown magic, an
// unsupported version, a short read, an entry that overruns the buffer, corrupt framing — is
// errIndexUnsupported with the reason; it never panics on arbitrary bytes, which
// FuzzParseGitIndex holds it to. Extensions and the trailing checksum after the last entry are
// deliberately ignored: validation needs entries, not cache trees.
func parseIndex(b []byte) ([]indexEntry, error) {
	if len(b) < indexHeaderLen {
		return nil, fmt.Errorf("%w: %d-byte file is shorter than the header", errIndexUnsupported, len(b))
	}
	if string(b[:len(indexMagic)]) != indexMagic {
		return nil, fmt.Errorf("%w: magic %q", errIndexUnsupported, b[:len(indexMagic)])
	}
	version := binary.BigEndian.Uint32(b[4:8])
	if version != 2 && version != 3 {
		return nil, fmt.Errorf("%w: version %d (2 and 3 are supported)", errIndexUnsupported, version)
	}
	count := binary.BigEndian.Uint32(b[8:12])

	var entries []indexEntry
	off := indexHeaderLen
	for i := uint32(0); i < count; i++ {
		if off+indexEntryFixedLen > len(b) {
			return nil, fmt.Errorf("%w: entry %d overruns the buffer", errIndexUnsupported, i)
		}
		mtimeSec := binary.BigEndian.Uint32(b[off+indexEntryMTimeOff:])
		size := binary.BigEndian.Uint32(b[off+indexEntrySizeOff:])
		flags := binary.BigEndian.Uint16(b[off+indexEntryFlagsOff:])

		nameOff := off + indexEntryFixedLen
		if version == 3 && flags&indexFlagExtended != 0 {
			nameOff += 2 // one extra uint16 of extended flags before the path
			if nameOff > len(b) {
				return nil, fmt.Errorf("%w: entry %d overruns the buffer", errIndexUnsupported, i)
			}
		}

		pathLen := int(flags & indexNameMask)
		if pathLen == int(indexNameMask) {
			// The length field saturated: the real path is longer, NUL-terminated.
			nul := bytes.IndexByte(b[nameOff:], 0)
			if nul < 0 {
				return nil, fmt.Errorf("%w: entry %d has an unterminated path", errIndexUnsupported, i)
			}
			pathLen = nul
		}
		if nameOff+pathLen > len(b) {
			return nil, fmt.Errorf("%w: entry %d overruns the buffer", errIndexUnsupported, i)
		}
		path := b[nameOff : nameOff+pathLen]
		if bytes.IndexByte(path, 0) >= 0 {
			return nil, fmt.Errorf("%w: entry %d has a NUL inside its path", errIndexUnsupported, i)
		}

		// 1–8 NUL padding bytes bring the entry, measured from its own start, to a multiple
		// of 8; the byte right after the path must be the first of them.
		entryLen := (nameOff - off) + pathLen
		padded := (entryLen + 8) &^ 7
		if off+padded > len(b) {
			return nil, fmt.Errorf("%w: entry %d overruns the buffer", errIndexUnsupported, i)
		}
		if b[nameOff+pathLen] != 0 {
			return nil, fmt.Errorf("%w: entry %d path is not NUL-terminated", errIndexUnsupported, i)
		}

		entries = append(entries, indexEntry{Path: string(path), Size: size, MTimeSec: mtimeSec})
		off += padded
	}
	return entries, nil
}
