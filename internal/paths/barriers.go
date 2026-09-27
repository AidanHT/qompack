package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
)

// ErrLineNotDurable reports a durable append that WROTE its lines but could not complete the barriers
// after the write: the file's sync, or the directory sync that makes its name durable, failed. The
// lines are in the file and every reader sees them now; what is not known is whether they survive a
// power cut. Every other error from AppendJSONLDurable, AppendLinesDurable and AppendManifest means
// nothing was appended.
//
// A caller whose "not done" answer would otherwise be acted on as "nothing happened" must tell the
// two apart. checkpoint.Finalize does: a MANIFEST line that is written is a seal readers already see
// and a sequence the manifest already claims, so the draft behind it must not be unsealed and sealed
// again under the next sequence (w6-ckptsync review finding 2).
var ErrLineNotDurable = errors.New("paths: the line is written but not known durable")

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
// runs, so a caller never learns "durable" from a write whose barrier failed. A barrier that fails
// after an append's write returns the error wrapped in ErrLineNotDurable.
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

// DirBarrier syncs dir's entries through x: SyncDir, unless x.SyncDir replaces it. It is for a
// writer outside this package that builds a tree of its own and must make its names durable before
// something depends on them (store's backup and restore trees).
func (x Barriers) DirBarrier(dir string) error { return x.syncDir(dir) }

// MkdirAll is os.MkdirAll made durable: it creates dir and every missing parent, then syncs the
// parent of each directory whose entry is not yet durable, deepest first, so that a file written into
// dir afterwards cannot lose its name to a power cut along with a directory whose own entry was never
// synced — a file's WriteAtomic syncs the directory that holds it, not that directory's parent.
//
// "Not yet durable" is the directories this call creates and, from the process's entry ledger
// (entries.go), every directory on dir's path that an earlier call in this process created and has
// not yet synced into its parent: a call whose parent sync failed, or a concurrent call still inside
// its sync. A directory found on disk is therefore never taken as durable merely because it exists —
// the second ingest worker into a shard the first has just created syncs the shard's entry itself
// rather than writing a sidecar a power cut could take with the shard (w6-ckptsync review findings 1
// and 3). A directory that exists and is durable costs one Lstat and syncs nothing, so a writer that
// shards into directories made on demand pays the barrier once per new directory, not per file.
//
// A directory another PROCESS created is taken as durable. That process either synced it (every
// product writer that makes a directory on demand for a durable file goes through here) or failed
// and reported the failure; in the second case the entry sits in the page cache, where the file
// system's own journal commits it within seconds, and a later process has no record to retry from.
// That narrow window is the residual this rule accepts rather than sync every existing ancestor of
// every durable write in every process.
func (x Barriers) MkdirAll(dir string, perm fs.FileMode) error {
	var names []string
	p := filepath.Clean(dir)
	for {
		if _, err := os.Lstat(Long(p)); !errors.Is(err, fs.ErrNotExist) {
			break
		}
		entries.creating(p)
		names = append(names, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	// p is the deepest directory that already existed: dir itself when nothing was missing.
	if st, _ := entries.look(p); st == entryPending {
		names = append(names, p)
	}
	names = append(names, entries.pendingFrom(p)...)
	if err := os.MkdirAll(Long(dir), perm); err != nil {
		return err
	}
	return x.syncEntries("MkdirAll", names)
}

// FileBarrier syncs f's written bytes through x: (*os.File).Sync, unless x.SyncFile replaces it. It
// is for a writer outside this package that holds its own append handle and must make what it wrote
// durable before it acknowledges it (negknow's acknowledged eliminations).
func (x Barriers) FileBarrier(f *os.File) error { return x.syncFile(f) }

// AppendJSONLDurable is AppendJSONL made durable before it returns. It appends exactly the line
// AppendJSONL would (same encoding, same newline guard, same torn-tail terminator), then:
//
//  1. syncs the file, so the line survives a power cut once the call returns;
//  2. syncs the file's directory unless this process has already made the file's NAME durable, so
//     the name survives too — on POSIX a new file's fsync does not make its directory entry durable,
//     and a line kept in a file whose name is lost is lost with it.
//
// It is for an append-only log whose line something durable depends on as soon as the call returns:
// a user told "pinned", a checkpoint sealed on the strength of its MANIFEST line. A log whose tail may
// be lost by design (the elimination log, the day logs) keeps AppendJSONL and pays nothing.
//
// Step 2 is decided from the process's entry ledger (entries.go), never from whether the file already
// exists: a file that exists may be one whose creating append had its directory sync fail, one
// another goroutine created and is still syncing, or one another process created and exited before
// syncing — `qompack pin` is one process per pin (w6-ckptsync review finding 1). So the first durable
// append to a path in each process syncs its directory, whoever created the file, and later appends
// in the same process pay only the file sync. Directories above the file that this process created
// and has not yet made durable are synced too (paths.Barriers.MkdirAll).
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
// AppendJSONLDurable does for one record: the file's sync, then its directory's unless this process
// has already made the file's name durable. It is for a producer that declares many records at once and needs them all
// durable (store's retention roots before a backup manifest names them): one sync for the batch,
// not one per line. A lines that is empty or does not end in a newline is refused, since the next
// append would glue its first record onto the unterminated last one.
func AppendLinesDurable(p string, lines []byte) error { return Barriers{}.AppendLinesDurable(p, lines) }

// AppendLinesDurable is the package function of the same name, issuing its barriers through x.
func (x Barriers) AppendLinesDurable(p string, lines []byte) error {
	if len(lines) == 0 || lines[len(lines)-1] != jsonRecordNewline {
		return fmt.Errorf("%w: AppendLinesDurable needs newline-terminated records: %s", core.ErrAppendOnly, p)
	}
	if _, err := os.Lstat(Long(p)); errors.Is(err, fs.ErrNotExist) {
		entries.creating(p) // before the create, so no concurrent append can find it durable
	}

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
	// From here on the lines are in the file, visible to every reader, so a failure is not "nothing
	// was appended" and must not read as one (ErrLineNotDurable).
	if err := x.syncFile(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("%w: %s: %w", ErrLineNotDurable, p, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrLineNotDurable, p, err)
	}
	names := entries.pendingFrom(p)
	if st, _ := entries.look(p); st != entryDurable {
		names = append([]string{p}, names...)
	}
	if err := x.syncEntries("AppendLinesDurable", names); err != nil {
		return fmt.Errorf("%w: %w", ErrLineNotDurable, err)
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
//     indexes by re-hashing it. A failure from here on is ErrLineNotDurable: the line is written.
//  3. SyncDir(checkpoints) again, only when the manifest's name is not yet durable: step 2 created
//     it (the project's first checkpoint), or an earlier append's step 3 failed and no step 1 has run
//     since. Step 1 covers an existing manifest's name, so a steady-state seal pays one directory
//     sync, and a step 3 that failed is retried by the next append's step 1.
//
// Every caller that returns "sealed" does so only after this returns nil: Finalize, before the
// PreCompact answer, the draft's retirement and the successor draft; fsck's orphan repair, before it
// reports the orphan indexed. On Windows SyncDir is a no-op (D24) and the manifest's FlushFileBuffers
// carries steps 1 and 3 under the NTFS-journaling premise that fsyncDir states.
func AppendManifest(l Layout, e ManifestEntry) error { return Barriers{}.AppendManifest(l, e) }

// AppendManifest is the package function of the same name, issuing its barriers through x.
func (x Barriers) AppendManifest(l Layout, e ManifestEntry) error {
	mp := ManifestPath(l)
	_, gen := entries.look(mp)
	_, statErr := os.Lstat(Long(mp))
	if err := x.syncDir(l.Checkpoints); err != nil {
		return err
	}
	if statErr == nil {
		// Step 1's sync ran after the manifest existed, so it made the manifest's name durable too;
		// the ledger records that, and step 2's append does not sync the directory a second time.
		entries.credit(mp, gen)
	}
	return x.AppendJSONLDurable(mp, e)
}
