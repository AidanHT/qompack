package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The heads of the hook client spools, read once per file version (V6 close-out D55, wave 16b).
//
// The PreCompact settle (precompact_settle.go) must know which client spools hold the compacting
// session's captures, and a client-<pid> name says nothing about its session: only the lines do.
// Reading every client spool on every PreCompact made a healthy session pay for every other
// session's backlog, before its bound and again after it. So the daemon remembers what each client
// spool holds, as the settle reads it: every complete hot-path line's head (spoolLineHead) and where
// the line starts, for the file's size and modification time when it was read. A hook client spool
// only grows (its hook appends; a reused pid's hook appends to the same name) until a drain releases
// it, and a drain records its progress in state/drain.json, never in the file. A file listed at the
// size and time it was read at has therefore not changed, and its heads are taken from memory. A file
// that has changed, or is new, is read again whole: one read per file version, not per PreCompact.
//
// The settle indexes the files it reads, and so does the client-spool watcher, for every spool its
// pass leaves behind (lookAtClientSpools): the backlog another session's spools form is then already
// indexed, off the hook path, when a healthy session compacts, and its settle pays the listing alone.
//
// The size and time are not the file's identity, though. A drain releases client-<pid>.ndjson, and a
// hook whose pid was reused can write the same name again, at the same size and, on the slow
// filesystems spool submode is for (network shares, drvfs), at the same coarse modification time,
// before any listing has shown the file gone. So the daemon's own removal of a client spool drops
// its entry (removed, which the drain calls through DrainConfig.ClientSpoolRemoved), and a read under
// way when an entry is dropped is not remembered: a recreated file is always read again. No stat is
// added for it: the drain knows when it removes a file.

// spoolHeadIndex is the daemon's memory of the client spools' line heads. The zero value is ready.
type spoolHeadIndex struct {
	mu    sync.Mutex
	files map[string]spoolHeadFile
	// removals counts the client spools the daemon has removed (removed). A read that began before
	// one is not remembered, since the file it read may be the one removed.
	removals uint64
	// reads counts the client spool files read to index them, by every look: the settle's and the
	// watcher's. A settle counts its own looks' reads (spoolScan.reads).
	reads atomic.Int64
	// read reads one client spool whole; nil reads it with paths.ReadFileShared. It is a test seam: a
	// row makes the reads of a cold backlog slow, under the look's own context, without a clock.
	read func(ctx context.Context, path string) ([]byte, error)
}

// spoolHeadFile is one indexed client spool: the size and time it was listed at, and its lines.
type spoolHeadFile struct {
	size  int64
	mod   time.Time
	lines []spoolHeadLine
}

// spoolHeadLine is one complete hot-path line of a client spool: where it starts, its session as
// resolveEvent would give it, and what the settle keeps of it.
type spoolHeadLine struct {
	start int64
	sess  core.SessionID
	c     pendingCapture
}

// clientSpoolListing is one listed hook client spool: its base name, and its size and modification
// time as the listing gave them.
type clientSpoolListing struct {
	base string
	size int64
	mod  time.Time
}

// listClientSpools lists root's hook client spools. It is one directory listing and reads no file:
// the size and time come from the listing's own entries (on Windows the directory record itself,
// elsewhere one lstat per entry).
func listClientSpools(root string) []clientSpoolListing {
	des, err := os.ReadDir(paths.Long(paths.Of(root).Spool))
	if err != nil {
		return nil // no spool directory: nothing was spooled
	}
	var out []clientSpoolListing
	for _, de := range des {
		if !de.Type().IsRegular() || !isClientSpoolName(de.Name()) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue // gone between the listing and its entry's stat
		}
		out = append(out, clientSpoolListing{base: de.Name(), size: info.Size(), mod: info.ModTime()})
	}
	return out
}

// heads returns l's line heads: from memory when the index holds l at the size and time listed,
// and otherwise read from root's spool now and remembered. read reports whether this call read the
// file. When the file is not in memory and ctx has already ended, nothing is read and ok is false:
// the caller counts the file as unread. A file gone since the listing has nothing to give and is not
// remembered, and neither is one read while the daemon removed a client spool (removed).
func (x *spoolHeadIndex) heads(ctx context.Context, root string, l clientSpoolListing) (lines []spoolHeadLine, ok, read bool) {
	x.mu.Lock()
	f, hit := x.files[l.base]
	removals := x.removals
	x.mu.Unlock()
	if hit && f.size == l.size && f.mod.Equal(l.mod) {
		return f.lines, true, false
	}
	if ctx.Err() != nil {
		return nil, false, false
	}
	x.reads.Add(1)
	readFile := x.read
	if readFile == nil {
		readFile = func(_ context.Context, path string) ([]byte, error) { return paths.ReadFileShared(path) }
	}
	b, err := readFile(ctx, filepath.Join(paths.Of(root).Spool, l.base))
	if err != nil {
		return nil, true, true // consumed and removed since the listing, or unreadable: nothing to name from it
	}
	lines = parseSpoolHeads(b, l.base)
	x.mu.Lock()
	if x.removals == removals {
		if x.files == nil {
			x.files = map[string]spoolHeadFile{}
		}
		x.files[l.base] = spoolHeadFile{size: l.size, mod: l.mod, lines: lines}
	}
	x.mu.Unlock()
	return lines, true, true
}

// removed drops the client spool base from the index: the daemon has just removed it (the drain's
// release of a fully replayed file, DrainConfig.ClientSpoolRemoved). A file of the same name listed
// later is a new one, whatever its size and time, and is read again. A read under way now is not
// remembered (heads), so it cannot put the removed file's heads back.
func (x *spoolHeadIndex) removed(base string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.removals++
	delete(x.files, base)
}

// forget drops every remembered file a complete listing no longer shows: released by a drain.
func (x *spoolHeadIndex) forget(listed map[string]bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for base := range x.files {
		if !listed[base] {
			delete(x.files, base)
		}
	}
}

// parseSpoolHeads decodes the head of every complete hot-path line of b, the client spool base. A
// trailing partial line is its hook still writing it: the file's size changes when it finishes, and
// the next look reads the file again.
func parseSpoolHeads(b []byte, base string) []spoolHeadLine {
	var out []spoolHeadLine
	var at int64
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}
		line, start := b[:i], at
		b, at = b[i+1:], at+int64(i)+1
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		h, err := decodeSpoolLineHead(line)
		if err != nil || !h.Op.HotPath() {
			continue
		}
		c := h.capture()
		c.file = base
		out = append(out, spoolHeadLine{start: start, sess: h.session(), c: c})
	}
	return out
}

// indexClientSpools remembers the heads of the client spools in bases that are still listed. The
// watcher calls it for the spools its pass left, so the settle of a session that holds none of
// them reads none of them. ctx bounds it like the pass it follows: a file not reached is read by
// whichever look needs it next.
func (d *daemon) indexClientSpools(ctx context.Context, bases map[string]bool) {
	for _, l := range listClientSpools(d.root) {
		if bases[l.base] {
			d.spoolHeads.heads(ctx, d.root, l)
		}
	}
}
