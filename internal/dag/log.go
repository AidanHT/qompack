package dag

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// This file is dag/deps.jsonl's durability half: loading the log at Open and appending to it at
// Flush. 00-ARCHITECTURE.md §7.4 places the file ("dag/deps.jsonl # dependence edges for slicing")
// and §3.3 makes it append-only in normal operation — every write here goes through
// paths.AppendOnly, never a bare os.OpenFile, so the §7.4 guard cannot be bypassed by reaching
// around this package. compact.go holds the single sanctioned exception.
//
// The loader's governing rule is that a damaged log must never take the session down. §13
// invariant 10 asks for degradation to be LOUD, not fatal: a corrupt record is counted, skipped and
// reported once, and the graph opens with whatever it could read. The alternative — refusing to
// open — would turn one bad byte into a dead session, and the DAG is an optimization over the
// transcript, not the transcript itself.

// dagDirPerm is the permission paths.EnsureLayout gives every .qompack subdirectory; Open matches
// it so a directory it creates is indistinguishable from one EnsureLayout made.
const dagDirPerm = 0o700

// scanBufferStart is the initial size of the loader's line buffer. bufio.Scanner grows it on
// demand up to maxLineBytes, so this only decides how many records are read before the first
// growth: 64 KiB holds several hundred typical records, which makes the growth path rare without
// pre-committing a megabyte per open.
const scanBufferStart = 1 << 16

// Open returns the dependence graph rooted at root, loaded from <root>/.qompack/dag/deps.jsonl.
//
// It creates the dag directory if it is absent, so a caller need not have run paths.EnsureLayout
// first. A missing log is the normal first-run case: the graph opens empty, at generation 0, with
// no error and nothing logged, because a project that has never recorded a graph is healthy, not
// degraded.
//
// Open returns a non-nil error only for a directory it cannot create or an I/O failure that is not
// "the file does not exist". Every per-record problem — a malformed line, an unknown kind, a torn
// final line — is surfaced through GraphStats (LoadErrors, TruncatedTail) and through the log,
// never as a failure to open: see the file comment above for why.
func Open(root string, cfg config.Config, log logging.Logger) (Graph, error) {
	g := newGraph(root, cfg, log)
	if err := os.MkdirAll(paths.Long(filepath.Dir(g.logPath)), dagDirPerm); err != nil {
		return nil, fmt.Errorf("dag: open %s: %w", g.logPath, err)
	}
	if err := g.load(); err != nil {
		return nil, err
	}
	return g, nil
}

// load streams deps.jsonl into the in-memory maps.
//
// Two properties are worth stating explicitly, because they are what make a load different from a
// replay of AddNode/AddEdge calls:
//
//   - It bypasses the pending queue entirely. A record read from the log is already on disk;
//     routing it through AddNode would queue it for a write-back, and every restart would append a
//     second copy of the entire graph.
//   - Validation still runs. A record that fails it is skipped with LoadErrors++, never returned as
//     an error, so one bad line costs exactly one relationship instead of the whole session.
func (g *graph) load() error {
	f, err := os.Open(paths.Long(g.logPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // never recorded a graph: healthy, not degraded
		}
		return fmt.Errorf("dag: open %s: %w", g.logPath, err)
	}
	defer func() { _ = f.Close() }()

	// Whether the final line is torn has to be known BEFORE it is applied, and a Scanner cannot
	// tell a terminated line from an unterminated one — it strips the newline either way. Reading
	// the last byte up front is what makes the hold-back below meaningful. ReadAt leaves the file
	// offset alone, so the scan still starts at zero.
	torn, err := lastByteIsNotNewline(f)
	if err != nil {
		return fmt.Errorf("dag: read %s: %w", g.logPath, err)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	var (
		records      int
		offset       int64 // start of the line about to be scanned
		firstBad     int64 = -1
		held         []byte
		heldAt       int64
		haveHeld     bool
		stoppedEarly bool // the scan gave up before reaching the end of the file
	)
	apply := func(line []byte, at int64) {
		records++
		rec, recognised, decodeErr := decodeRecord(line)
		if decodeErr == nil && recognised {
			decodeErr = g.applyRecordLocked(rec)
		}
		if decodeErr != nil {
			g.stats.LoadErrors++
			if firstBad < 0 {
				firstBad = at
			}
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, scanBufferStart), maxLineBytes)
	for sc.Scan() {
		// One line is always held back, so that when the loop ends the last line read is still in
		// hand and can be discarded rather than applied if the file turned out to be torn.
		line := sc.Bytes()
		if haveHeld {
			apply(held, heldAt)
		}
		held = append(held[:0], line...) // Scanner reuses its buffer; this must own its copy
		heldAt = offset
		haveHeld = true
		// +1 for the newline the Scanner stripped. This assumes the LF terminators this package
		// writes; a hand-edited CRLF file would under-report LogBytes by a byte a line, which costs
		// nothing — LogBytes is a human-facing size, while LogRecords, the counter Compact's
		// threshold actually reads, is counted per line and is unaffected.
		offset += int64(len(line)) + 1
	}

	switch scanErr := sc.Err(); {
	case scanErr != nil:
		// The held line is complete and terminated — the failure lies in what came after it — so it
		// is applied before the failure is reported.
		if haveHeld {
			apply(held, heldAt)
			haveHeld = false
		}
		if !errors.Is(scanErr, bufio.ErrTooLong) {
			return fmt.Errorf("dag: read %s: %w", g.logPath, scanErr)
		}
		// A line past maxLineBytes. It is counted as one load error, and the load stops there: a
		// Scanner that has hit ErrTooLong cannot resynchronize to the next record boundary, and
		// guessing where one starts would invent records rather than skip them. Nothing this
		// package writes can produce such a line — encodeRecord refuses it — so reaching here means
		// the file was written by something else.
		g.stats.LoadErrors++
		stoppedEarly = true
		if firstBad < 0 {
			firstBad = offset
		}
	case haveHeld && torn:
		// The expected artifact of a crash mid-append: the last append reached disk only in part.
		// Discard it and say so. It is not counted as a load error, because nothing is wrong with
		// the log — the process simply died between two writes.
		g.stats.TruncatedTail = true
		offset -= int64(len(held)) + 1 // the torn bytes were never a record
	case haveHeld:
		apply(held, heldAt)
	}

	g.stats.LogRecords = records
	g.stats.LogBytes = offset

	if g.stats.TruncatedTail {
		g.log.Warn("dag: discarded an unterminated final record from the dependence log",
			"path", g.logPath, "bytes", len(held))
	}
	if g.stats.LoadErrors > 0 {
		// One Loud per Open, never one per line: a log with ten thousand damaged records must
		// produce a report, not ten thousand of them (§13 invariant 10).
		// stopped_early distinguishes "n records were skipped and the rest loaded" from "the load
		// gave up here": both are degradation, but only the second means the graph is missing
		// everything after that offset, and an operator must be able to tell them apart.
		g.log.Loud("dag: skipped unreadable records while loading the dependence log",
			"path", g.logPath, "errors", g.stats.LoadErrors, "first_offset", firstBad,
			"records", records, "stopped_early", stoppedEarly)
	}
	return nil
}

// lastByteIsNotNewline reports whether f's final byte is anything other than a record terminator,
// which is exactly the condition "the last line was never finished". An empty file is not torn.
func lastByteIsNotNewline(f *os.File) (bool, error) {
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	size := fi.Size()
	if size == 0 {
		return false, nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], size-1); err != nil {
		return false, err
	}
	return last[0] != recordNewline, nil
}

// applyRecordLocked folds one loaded record into the in-memory maps, reusing AddNode's and
// AddEdge's own validation and merge rules so that a graph loaded from disk is byte-for-byte the
// graph that was written — including the anchor earliest-Pos rule and the edge dedup fold, both of
// which would produce a DIFFERENT graph if the loader applied records naively.
//
// It deliberately does not touch g.pending: see load's comment.
//
// The caller must already hold g.mu for writing.
func (g *graph) applyRecordLocked(rec record) error {
	switch rec.kind {
	case recNode:
		if err := validateNode(rec.node); err != nil {
			return err
		}
		merged := rec.node
		if prev, ok := g.nodes[rec.node.ID]; ok {
			merged = mergeNode(prev, rec.node)
		}
		g.nodes[rec.node.ID] = merged
		// A node record after a tombstone revives the id, exactly as AddNode does: the store's GC
		// may collect a root that a later turn legitimately re-reads.
		delete(g.dead, rec.node.ID)

	case recEdge:
		normalized, err := validateEdge(rec.edge)
		if err != nil {
			return err
		}
		key := edgeKey{from: normalized.From, to: normalized.To, kind: normalized.Kind}
		if i, ok := g.edgeIdx[key]; ok {
			if normalized.Weight > g.edges[i].Weight {
				g.edges[i].Weight = normalized.Weight
			}
			if normalized.Turn < g.edges[i].Turn {
				g.edges[i].Turn = normalized.Turn
			}
			break
		}
		idx := len(g.edges)
		g.edges = append(g.edges, normalized)
		g.edgeIdx[key] = idx
		g.out[normalized.From] = append(g.out[normalized.From], idx)
		g.in[normalized.To] = append(g.in[normalized.To], idx)

	case recTomb:
		if rec.id == "" {
			return fmt.Errorf("%w: tombstone record names no id", ErrInvalidNode)
		}
		g.dead[rec.id] = true

	case recGen:
		g.gen = rec.gen

	default:
		return fmt.Errorf("dag: cannot apply a record of unknown kind %q", string(rec.kind))
	}

	g.idxDirty = true
	return nil
}

// Flush appends every buffered record to dag/deps.jsonl.
//
// SP-05's daemon calls it from its idle loop and at shutdown; a hot-path hook never has to,
// because AddNode and AddEdge auto-flush at autoFlushRecords on their own.
func (g *graph) Flush(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.flushLocked(ctx)
}

// flushLocked is Flush's body, and is also what maybeAutoFlushLocked and Compact call.
//
// It must never call the exported Flush: the caller already holds the write lock and sync.RWMutex
// is not re-entrant, so reaching for the exported method here would deadlock against itself on the
// very first auto-flush.
//
// Durability shape. Every pending record is encoded into ONE buffer and written with ONE Write,
// then synced, then closed. A single append of a whole batch is the durability boundary this file
// promises — a torn tail from a crash mid-write costs at most the last partial line, which the
// loader is built to discard (see load). It is deliberately not a write-ahead log: SP-05's daemon
// WAL is the real one, and duplicating it here would buy nothing the loader cannot already recover
// from.
//
// Failure shape. On any write error the pending records are KEPT: the daemon's idle loop retries on
// its next tick, and nothing is lost from memory in the meantime. The failure is loud (§13
// invariant 10) because a graph that has silently stopped persisting looks identical to a healthy
// one until the next restart, when the session's whole dependence history turns out to be missing.
//
// The caller must already hold g.mu for writing.
func (g *graph) flushLocked(ctx context.Context) error {
	if g.closed {
		return ErrClosed
	}
	if len(g.pending) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// The directory is recreated if it has gone missing. Open makes it, but a long-lived daemon
	// outlives whatever removed it, and losing a whole batch of records because someone cleaned out
	// .qompack between two flushes would be a strictly worse answer than making the directory again.
	if err := os.MkdirAll(paths.Long(filepath.Dir(g.logPath)), dagDirPerm); err != nil {
		g.log.Loud("dag: cannot create the dependence log's directory; records retained in memory",
			"path", g.logPath, "err", err, "pending", len(g.pending))
		return fmt.Errorf("dag: flush %s: %w", g.logPath, err)
	}

	// The destination is opened BEFORE the batch is encoded, and the order matters under failure:
	// maybeAutoFlushLocked retries on every subsequent mutation once the queue is over the
	// threshold, so encoding first would make each retry re-marshal the entire backlog and turn a
	// persistently unwritable log into quadratic work on the mutation path.
	//
	// paths.AppendOnly is the only sanctioned write path for a *.jsonl file under .qompack (§3.3);
	// a bare os.OpenFile here would sidestep the guard the whole package exists to keep honest.
	w, err := paths.AppendOnly(g.logPath)
	if err != nil {
		g.log.Loud("dag: cannot open the dependence log for append; records retained in memory",
			"path", g.logPath, "err", err, "pending", len(g.pending))
		return fmt.Errorf("dag: flush %s: %w", g.logPath, err)
	}

	var buf bytes.Buffer
	for _, rec := range g.pending {
		line, encodeErr := encodeRecord(rec)
		if encodeErr != nil {
			_ = w.Close()
			g.log.Loud("dag: refusing to append an unencodable record to the dependence log",
				"path", g.logPath, "err", encodeErr, "pending", len(g.pending))
			return encodeErr
		}
		buf.Write(line)
		buf.WriteByte(recordNewline)
	}

	n, writeErr := w.Write(buf.Bytes())
	if writeErr == nil {
		// AppendOnly's declared type is io.WriteCloser, so Sync is reached by assertion rather than
		// by name. Its dynamic type is *os.File today; if that ever stops being true the flush
		// degrades to "written but not fsynced" rather than failing, which is the right trade for a
		// file the loader can already recover a torn tail from.
		if syncer, ok := w.(interface{ Sync() error }); ok {
			writeErr = syncer.Sync()
		}
	}
	if closeErr := w.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		g.log.Loud("dag: failed to append to the dependence log; records retained in memory",
			"path", g.logPath, "err", writeErr, "pending", len(g.pending))
		return fmt.Errorf("dag: flush %s: %w", g.logPath, writeErr)
	}

	g.stats.LogBytes += int64(n)
	g.stats.LogRecords += len(g.pending)
	g.pending = g.pending[:0]
	return nil
}
