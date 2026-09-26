package paths

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/qompack/qompack/internal/core"
)

// IsProtected reports whether p, resolved relative to root's .qompack tree, falls under one of
// the §7.4 append-only locations: sketches/tried.bloom exactly, everything under checkpoints/
// (which is where checkpoints/MANIFEST.jsonl lives too, deliberately, so only AppendOnly can
// write it), and everything under pins/. A p that is not under root's .qompack tree at all is
// never protected.
func IsProtected(root, p string) bool {
	l := Of(root)
	rel, err := filepath.Rel(l.Dot, filepath.Clean(p))
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	rel = filepath.ToSlash(rel)
	switch {
	case rel == protectedSketchesDir+"/"+protectedBloomName:
		return true
	case strings.HasPrefix(rel, protectedCheckpointsDir+"/"):
		return true
	case strings.HasPrefix(rel, protectedPinsDir+"/"):
		return true
	}
	return false
}

// The names IsProtected matches, relative to <root>/.qompack. mayBeProtected reads the same
// constants, so the two cannot drift apart.
const (
	protectedSketchesDir    = "sketches"
	protectedBloomName      = "tried.bloom"
	protectedCheckpointsDir = "checkpoints"
	protectedPinsDir        = "pins"
)

// mayBeProtected reports whether p could be a protected path under ANY project root, from p's text
// alone. False is a proof, not a guess: IsProtected matches only a path whose position relative to
// <root>/.qompack begins with checkpoints/ or pins/ or is sketches/tried.bloom; filepath.Rel returns
// that relative position as a literal suffix of filepath.Clean(p); and Clean only removes elements
// and separators, so every element of its result appears verbatim in p. A p containing none of the
// three names therefore cannot be protected, whatever root rootOf would find, and OpenFile need not
// walk p's ancestors — up to one stat each — to find out. True means only that the walk must run.
func mayBeProtected(p string) bool {
	return strings.Contains(p, protectedCheckpointsDir) || strings.Contains(p, protectedPinsDir) ||
		strings.Contains(p, protectedBloomName)
}

// OpenFile is the only opener this package exposes for a path that may live under .qompack, and
// every write anywhere in the codebase is expected to reach the filesystem through it — directly
// or via WriteAtomic, AppendOnly or CreateNew — so the §7.4 guard cannot be bypassed by a caller
// reaching for a bare os.OpenFile instead. On a protected path it refuses O_TRUNC outright, and
// refuses any write that is neither an append (O_APPEND) nor an exclusive create (O_EXCL) — the
// two operations the invariant actually allows.
func OpenFile(p string, flag int, perm fs.FileMode) (*os.File, error) {
	if !mayBeProtected(p) {
		// No root can make p protected (mayBeProtected), so the guard below could only pass.
		// Skipping the ancestor walk is what keeps the store's per-object staging open at one
		// syscall rather than one per directory level up to the project root.
		return os.OpenFile(Long(p), flag, perm)
	}
	root, abs, ok := rootOf(p)
	if ok && IsProtected(root, abs) {
		if flag&os.O_TRUNC != 0 {
			return nil, fmt.Errorf("%w: O_TRUNC on %s", core.ErrAppendOnly, p)
		}
		if flag&(os.O_WRONLY|os.O_RDWR) != 0 && flag&os.O_APPEND == 0 && flag&os.O_EXCL == 0 {
			return nil, fmt.Errorf("%w: non-append write on %s", core.ErrAppendOnly, p)
		}
	}
	return os.OpenFile(Long(p), flag, perm)
}

// AppendOnly opens p for append, creating it if it does not exist. It is restricted to the three
// extensions the store ever appends to (*.jsonl, *.ndjson, *.log) so a caller cannot launder an
// arbitrary write through the append-only door merely by naming the file something else.
func AppendOnly(p string) (io.WriteCloser, error) {
	if filepath.Ext(p) != ".jsonl" && !strings.HasSuffix(p, ".ndjson") && !strings.HasSuffix(p, ".log") {
		return nil, fmt.Errorf("%w: AppendOnly is for *.jsonl/*.ndjson/*.log only: %s", core.ErrAppendOnly, p)
	}
	return OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
}

// jsonRecordNewline is the byte AppendJSONL both terminates every record with and refuses to see
// inside an encoded payload: a raw newline in the middle of a record would let a single call
// write what every reader would parse as two JSONL lines.
const jsonRecordNewline = '\n'

// AppendJSONL marshals v with HTML-escaping disabled — so characters like "<", ">" and "&" in,
// for instance, a file path or a diff survive unescaped, matching every other JSON writer in
// this codebase — and appends exactly one newline-terminated line to p through AppendOnly.
// encoding/json already guarantees compact, single-line output for any v (it escapes control
// characters inside string values, and it compacts a nested json.RawMessage's own whitespace
// away rather than passing it through verbatim), so in practice there is no legitimate v that
// reaches the check below. AppendJSONL verifies it anyway — a raw newline anywhere before the
// trailing terminator would silently split one record into two lines on disk, which is exactly
// the failure mode the one-record-per-line contract every reader assumes must never happen — and
// rejects the write rather than trust that guarantee blindly.
func AppendJSONL(p string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	line := bytes.TrimSuffix(buf.Bytes(), []byte{jsonRecordNewline})
	if bytes.IndexByte(line, jsonRecordNewline) >= 0 {
		return fmt.Errorf("%w: AppendJSONL payload contains a raw newline: %s", core.ErrAppendOnly, p)
	}

	w, err := AppendOnly(p)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	// A torn tail must not swallow this record too. That is finding F4-2: a crash or a torn write
	// can leave the file ending mid-record with no newline, and appending straight onto it glues
	// this record's first bytes to the partial one — so the reader steps over ONE malformed line and
	// loses TWO records, the damaged one and this perfectly good one.
	//
	// Writer-side only, and deliberately so: nothing about the line format changes, no reader is
	// touched, and the damaged line stays on disk exactly as it was, still reported as malformed by
	// whoever reads it. What stops is the loss spreading forward.
	if err := TerminatePartialTail(w, p); err != nil {
		return err
	}
	record := make([]byte, len(line)+1)
	copy(record, line)
	record[len(line)] = jsonRecordNewline
	_, err = w.Write(record)
	return err
}

// TerminatePartialTail writes a newline to w when p is non-empty and its last byte is not one.
//
// It re-stats the path rather than seeking on the handle because the handle is append-only: on
// Windows an O_APPEND handle's offset is not a reliable read cursor, and a separate bounded read of
// the final byte is the same answer on every platform. A file that cannot be stat'ed or read is
// left alone — the append itself is what the caller cares about, and refusing it over a failed
// diagnostic read would trade a recoverable torn line for a lost record.
func TerminatePartialTail(w io.Writer, p string) error {
	info, err := os.Stat(Long(p))
	if err != nil || info.Size() == 0 {
		return nil
	}
	f, err := os.Open(Long(p))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return nil
	}
	if last[0] == jsonRecordNewline {
		return nil
	}
	_, err = w.Write([]byte{jsonRecordNewline})
	return err
}

// CreateNew creates p exclusively (O_EXCL — a second write to the same filename is os.ErrExist,
// never a silent overwrite) and, once its content is durably written, marks it read-only
// (0o444 / FILE_ATTRIBUTE_READONLY) so that even a write path that somehow bypassed this
// package's own checks would still fail at the filesystem level.
//
// os.ErrExist is reserved for a REGULAR FILE already at p — the collision every caller retries
// past (checkpoint.Finalize bumps its sequence number on it). A directory or any other non-file
// at p is not a collision, and the two platforms disagree about it: Windows refuses to open a
// directory with O_CREATE|O_EXCL as ERROR_ACCESS_DENIED, while POSIX open(2) answers EEXIST for a
// directory exactly as it does for a file. Left to the platform, Finalize on Linux and macOS
// would skip past a directory sitting at its artifact path — the V5 close-out's first CI run on
// those runners found precisely that — so the existing entry is stat'ed and anything that is not
// a regular file is reported as syscall.EISDIR (a directory) or fs.ErrInvalid, never ErrExist.
func CreateNew(p string, b []byte) error {
	f, err := OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if fi, serr := os.Lstat(Long(p)); serr == nil && !fi.Mode().IsRegular() {
				var kind error = fs.ErrInvalid
				if fi.IsDir() {
					kind = syscall.EISDIR
				}
				return &os.PathError{Op: "create", Path: p, Err: kind}
			}
		}
		return err // os.ErrExist for a second write of the same filename
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(Long(p), 0o444)
}

// RestoreLog writes b into an append-only log at p through AppendOnly's flags (create-if-absent,
// O_APPEND, mode 0600) and Syncs. It is the restore door for the two §7.4 logs a backup carries —
// checkpoints/MANIFEST.jsonl and pins/invariants.jsonl — because CreateNew's chmod 0444 would
// leave a restored project unable to seal or pin. A restore never overwrites: an existing path
// returns a wrapped os.ErrExist after os.Lstat.
func RestoreLog(p string, b []byte) error {
	if filepath.Ext(p) != ".jsonl" && !strings.HasSuffix(p, ".ndjson") && !strings.HasSuffix(p, ".log") {
		return fmt.Errorf("%w: RestoreLog is for *.jsonl/*.ndjson/*.log only: %s", core.ErrAppendOnly, p)
	}
	if _, err := os.Lstat(Long(p)); err == nil {
		return fmt.Errorf("%w: %s", os.ErrExist, p)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// bloomBackupPrefix and bloomBackupSuffix bracket the sequence number in a tried.bloom backup's
// filename: tried.bloom.<seq>.bak.
const (
	bloomBackupPrefix = "tried.bloom."
	bloomBackupSuffix = ".bak"
)

// bloomBackupSeq reports the sequence encoded in a tried.bloom.<seq>.bak filename, and whether name
// is one at all. It is the ONE parser for that family: this package writes the name in ReplaceBloom,
// prunes by it in pruneBloomBackups and reports it through HighestBloomBackupSeq, and a second
// spelling anywhere would produce backups one of the three cannot see.
func bloomBackupSeq(name string) (seq int, ok bool) {
	if !strings.HasPrefix(name, bloomBackupPrefix) || !strings.HasSuffix(name, bloomBackupSuffix) {
		return 0, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(name, bloomBackupPrefix), bloomBackupSuffix)
	seq, err := strconv.Atoi(mid)
	if err != nil {
		return 0, false
	}
	return seq, true
}

// HighestBloomBackupSeq reports the highest sequence among the surviving
// sketches/tried.bloom.<n>.bak files in l.Sketches, and whether there is one at all. A missing
// sketches directory is "no backups" rather than an error, because that is the ordinary state of a
// project whose first session has not written a filter yet; anything else that stops the directory
// being read is returned, since a caller must not read an unreadable directory as an empty one.
//
// It is exported for two callers with the same underlying need.
//
// sketch.ReplaceGenerational pre-flights against it. pruneBloomBackups keeps the HIGHEST sequence
// rather than the most recently written file, so a replacement whose seq does not exceed every
// survivor has its own backup deleted the instant it is created — and if the staging write then
// fails there is no backup left to roll back, leaving the store with no tried.bloom at all. That is
// the append-only negative-knowledge file gone (§3.3, §7.4), so the call is refused before anything
// moves rather than reported afterwards.
//
// SP-09 needs it for the other half: its rebuild counter drives seq, and nothing on disk otherwise
// tells a restarted daemon where that counter had got to. This is the answer — the counter resumes
// above the highest surviving backup — which is why the function is exported rather than kept
// unexported beside the pruner.
func HighestBloomBackupSeq(l Layout) (seq int, ok bool, err error) {
	entries, err := os.ReadDir(Long(l.Sketches))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n, isBackup := bloomBackupSeq(e.Name())
		if isBackup && (!ok || n > seq) {
			seq, ok = n, true
		}
	}
	return seq, ok, nil
}

// pruneBloomBackups keeps only the keep newest tried.bloom.<seq>.bak generations in l.Sketches
// and removes the rest. ReplaceBloom calls it with keep=1, so exactly one prior generation
// survives each rebuild (§3.3).
func pruneBloomBackups(l Layout, keep int) {
	entries, err := os.ReadDir(Long(l.Sketches))
	if err != nil {
		return
	}
	type backup struct {
		name string
		seq  int
	}
	var backups []backup
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		seq, ok := bloomBackupSeq(e.Name())
		if !ok {
			continue
		}
		backups = append(backups, backup{name: e.Name(), seq: seq})
	}
	if len(backups) <= keep {
		return
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].seq > backups[j].seq })
	for _, b := range backups[keep:] {
		_ = os.Remove(Long(filepath.Join(l.Sketches, b.name)))
	}
}

// ReplaceBloom is the only legal way to replace sketches/tried.bloom (§3.3): every other write
// path into .qompack refuses it, through IsProtected. The current file, if any, is renamed to
// tried.bloom.<seq>.bak before the new content is staged and swapped into place, and exactly one
// backup generation is kept afterward — older backups are pruned.
func ReplaceBloom(l Layout, b []byte, seq int) error {
	cur := filepath.Join(l.Sketches, "tried.bloom")
	if _, err := os.Stat(Long(cur)); err == nil {
		bak := filepath.Join(l.Sketches, fmt.Sprintf("%s%d%s", bloomBackupPrefix, seq, bloomBackupSuffix))
		if err := os.Rename(Long(cur), Long(bak)); err != nil {
			return err
		}
		pruneBloomBackups(l, 1)
	}

	tmp := filepath.Join(l.Tmp, fmt.Sprintf("tried.bloom.%d", seq))
	if err := os.MkdirAll(Long(l.Tmp), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(Long(tmp), b, 0o600); err != nil {
		return err
	}
	return os.Rename(Long(tmp), Long(cur))
}

// ReplacePinsView writes b over pins/invariants.json, the materialized view of the pins log.
//
// It exists because IsProtected guards the WHOLE pins/ subtree, so WriteAtomic refuses
// pins/invariants.json outright — and it must, because pins/invariants.jsonl is the append-only
// file that carries the truth. But invariants.json is not that file: it is a projection that can
// be rebuilt from the log at any time, and it is replaced in full every time the log changes.
// A protected-but-replaceable derived file needs a door rather than a hole in IsProtected, which
// is exactly the shape ReplaceBloom already has for sketches/tried.bloom.
//
// The staging file lives in l.Tmp so the finishing rename is same-volume by construction, and the
// write barrier is WriteAtomic's, step for step: Sync the staging file, renameWithRetry it into
// place, then fsyncDir the destination directory. Nothing weaker is enough for this file, on
// either platform.
//
// The file's own fsync is what makes a half-written view impossible rather than merely unlikely:
// invariants.json is read at session start to decide which invariants are live, so a torn write
// would present a truncated invariant set as a complete one. The DIRECTORY fsync is the other
// half, and it is separate: on POSIX a crash between the rename and the next unrelated metadata
// flush can lose the rename itself even though the file's data was already durable, which leaves
// the previous generation of the view in place — silently a checkpoint behind.
//
// renameWithRetry rather than a bare os.Rename because of Windows. os.Rename is MoveFileEx, whose
// replace step fails with ERROR_ACCESS_DENIED whenever the destination has ANY open handle — even
// one opened with FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE (replace_windows.go carries
// the table, measured on this host). This destination is continuously re-opened by design:
// invariants.json is read at session start, and the daemon's materialize_pins idle task rewrites
// it on every idle tick. A bare rename therefore fails for as long as any reader holds the view
// open, which is exactly when a rewrite is most likely to be attempted.
func ReplacePinsView(l Layout, b []byte) error {
	if err := os.MkdirAll(Long(l.Tmp), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(Long(l.Tmp), "invariants.json.")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if err := renameWithRetry(tmp, Long(filepath.Join(l.Pins, "invariants.json"))); err != nil {
		return err
	}
	return fsyncDir(l.Pins)
}
