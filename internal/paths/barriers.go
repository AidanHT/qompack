package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
)

// Barriers are the two durability calls a sealing write issues: SyncFile makes the bytes written
// through a handle durable, and SyncDir makes a directory's entries durable — the names of the files
// in it, which on POSIX a file's own fsync does not cover (fsyncDir).
//
// The zero value is the real thing: a nil SyncFile is (*os.File).Sync and a nil SyncDir is SyncDir.
// Production code only ever uses the zero value, through the package functions (CreateNew,
// AppendManifest, AppendJSONLDurable, AppendLinesDurable). A caller that must count the barriers a write issues, or cut
// it at one of them the way a power loss would, holds its own Barriers and calls the methods; the
// store's pubSyncDir and the daemon's syncDir fields are the same kind of seam for their own writers.
//
// A barrier that fails stops the write where it is and returns the barrier's error: nothing after it
// runs, so a caller never learns "durable" from a write whose barrier failed.
type Barriers struct {
	// SyncFile makes f's written bytes durable. Nil means (*os.File).Sync.
	SyncFile func(f *os.File) error
	// SyncDir makes dir's entries durable. Nil means SyncDir, which is a no-op on Windows (D24).
	SyncDir func(dir string) error
}

func (x Barriers) syncFile(f *os.File) error {
	if x.SyncFile != nil {
		return x.SyncFile(f)
	}
	return f.Sync()
}

func (x Barriers) syncDir(dir string) error {
	if x.SyncDir != nil {
		return x.SyncDir(dir)
	}
	return fsyncDir(dir)
}

// AppendJSONLDurable is AppendJSONL made durable before it returns. It appends exactly the line
// AppendJSONL would (same encoding, same newline guard, same torn-tail terminator), then:
//
//  1. syncs the file, so the line survives a power cut once the call returns;
//  2. syncs the file's directory when this call created the file, so the file's NAME survives too —
//     on POSIX a new file's fsync does not make its directory entry durable, and a line kept in a
//     file whose name is lost is lost with it.
//
// It is for an append-only log whose line something durable depends on as soon as the call returns:
// a user told "pinned", a checkpoint sealed on the strength of its MANIFEST line. A log whose tail may
// be lost by design (the elimination log, the day logs) keeps AppendJSONL and pays nothing.
//
// "Created by this call" is read from an Lstat taken before the open. A file some other writer
// created an instant earlier, and has not yet synced the directory of, is that writer's to sync; in
// this codebase every writer of a log that reaches this function goes through it, so the one that
// created the file does.
func AppendJSONLDurable(p string, v any) error { return Barriers{}.AppendJSONLDurable(p, v) }

// AppendJSONLDurable is the package function of the same name, issuing its barriers through x.
func (x Barriers) AppendJSONLDurable(p string, v any) error {
	record, err := encodeJSONLRecord(p, v)
	if err != nil {
		return err
	}
	return x.AppendLinesDurable(p, record)
}

// AppendLinesDurable appends lines — one or more complete records, each terminated by a newline —
// to the append-only file p in one write, and makes them durable before it returns exactly as
// AppendJSONLDurable does for one record: the file's sync, then its directory's when the append
// created the file. It is for a producer that declares many records at once and needs them all
// durable (store's retention roots before a backup manifest names them): one sync for the batch,
// not one per line. A lines that is empty or does not end in a newline is refused, since the next
// append would glue its first record onto the unterminated last one.
func AppendLinesDurable(p string, lines []byte) error { return Barriers{}.AppendLinesDurable(p, lines) }

// AppendLinesDurable is the package function of the same name, issuing its barriers through x.
func (x Barriers) AppendLinesDurable(p string, lines []byte) error {
	if len(lines) == 0 || lines[len(lines)-1] != jsonRecordNewline {
		return fmt.Errorf("%w: AppendLinesDurable needs newline-terminated records: %s", core.ErrAppendOnly, p)
	}
	_, statErr := os.Lstat(Long(p))
	created := errors.Is(statErr, fs.ErrNotExist)

	w, err := AppendOnly(p)
	if err != nil {
		return err
	}
	f, ok := w.(*os.File)
	if !ok {
		// AppendOnly returns OpenFile's *os.File. A writer without a handle to sync cannot be made
		// durable, and saying so beats reporting a line durable that is not.
		_ = w.Close()
		return fmt.Errorf("paths: AppendLinesDurable: %s: the append handle cannot be synced", p)
	}
	if err := TerminatePartialTail(f, p); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(lines); err != nil {
		_ = f.Close()
		return err
	}
	if err := x.syncFile(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if created {
		return x.syncDir(filepath.Dir(p))
	}
	return nil
}

// CreateNew is the package function of the same name, issuing its file sync through x.
func (x Barriers) CreateNew(p string, b []byte) error { return createNew(p, b, x) }

// AppendManifest indexes one checkpoint artifact: it appends e to checkpoints/MANIFEST.jsonl, and it
// does so in the order a reader of the manifest depends on, because a manifest line is the whole of
// what makes a checkpoint sealed (Qompack.md §7.4; 00-ARCHITECTURE.md §0.2.2 names it the checkpoint's
// visibility):
//
//  1. SyncDir(checkpoints) — the artifact's NAME. CreateNew made its bytes durable, but on POSIX not
//     its directory entry, and a durable line naming an artifact whose name a power cut then loses is
//     a MANIFEST entry without its artifact: the one shape the reader refuses. The same barrier makes
//     the manifest's own name durable when the manifest already exists, whoever created it.
//  2. The line, then SyncFile(manifest) — the seal itself. Until it returns, the checkpoint is not
//     sealed, and a cut anywhere before leaves at most an orphan artifact, which `qompack fsck`
//     indexes by re-hashing it.
//  3. SyncDir(checkpoints) again, only when step 2 created the manifest (the project's first
//     checkpoint), so the manifest's name survives as well as its line.
//
// Every caller that returns "sealed" does so only after this returns nil: Finalize, before the
// PreCompact answer, the draft's retirement and the successor draft; fsck's orphan repair, before it
// reports the orphan indexed. On Windows SyncDir is a no-op (D24) and the manifest's FlushFileBuffers
// carries steps 1 and 3 under the NTFS-journaling premise that fsyncDir states.
func AppendManifest(l Layout, e ManifestEntry) error { return Barriers{}.AppendManifest(l, e) }

// AppendManifest is the package function of the same name, issuing its barriers through x.
func (x Barriers) AppendManifest(l Layout, e ManifestEntry) error {
	if err := x.syncDir(l.Checkpoints); err != nil {
		return err
	}
	return x.AppendJSONLDurable(ManifestPath(l), e)
}
