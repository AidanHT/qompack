package negknow

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// This file is records/eliminations.jsonl: the source of truth the whole negative-knowledge
// feature rests on (00-ARCHITECTURE.md §8.3 item 1, §7.4). tried.bloom is a cache rebuilt from it;
// a checkpoint is a projection of it; neither is ever read back as authority.
//
// Two rules govern everything below.
//
// The file is append-only. Every write goes through paths.AppendOnly, which never asks for
// O_TRUNC and refuses any name outside *.jsonl/*.ndjson/*.log, and a status change is appended as
// an op:"stale" control line rather than rewritten in place. There is no code path here that
// opens the log any other way.
//
// A damaged log must never take the session down (§13 invariant 10). A line that cannot be parsed
// is counted, logged and skipped; a torn final line — the ordinary shape of a crash mid-append —
// is dropped without even that, because nothing is wrong with the log. The replay returns an error
// only when the reader itself failed, since a partial materialization reported as a complete one
// would silently un-eliminate whatever came after the failure.

// logFileName is records/eliminations.jsonl's basename (00-ARCHITECTURE.md §7.4).
const logFileName = "eliminations.jsonl"

// logDirPerm is the mode the records directory is created with on first use, per the subplan's
// implementation spec. paths.EnsureLayout has usually created it already, in which case MkdirAll
// is a no-op and the stricter mode it used stands.
const logDirPerm = 0o755

// scanBufferStart is the replay's initial line buffer and maxLogLineBytes its ceiling. The ceiling
// is what keeps an untrusted file from turning into an unbounded allocation (§12.3); it is far
// above any line normalizeRecord can produce, so only a file written by something else can reach
// it.
const (
	scanBufferStart = 1 << 16
	maxLogLineBytes = 1 << 20
)

// The counters the replay maintains. They are strings, so nothing catches a typo at compile time
// and a second spelling would produce a second, silently empty instrument: these four are
// transcribed from the subplan's instrument list and are the single spelling in this package.
const (
	counterCorruptLines = "negknow.log.corrupt_lines"
	counterDuplicateAdd = "negknow.log.duplicate_add"
	counterUnknownOp    = "negknow.log.unknown_op"
	counterOrphanStale  = "negknow.log.orphan_stale"
)

// logKind is a control line's op.
type logKind string

// opStale is the only control op this version writes: it flips one record to StatusStale without
// rewriting the record line that is already on disk.
const opStale logKind = "stale"

// logControl is the shape of a control line. A record line is a bare Record and does not use it.
type logControl struct {
	Op      logKind  `json:"op"`
	ID      string   `json:"id,omitempty"`
	TS      int64    `json:"ts,omitempty"`
	Because []string `json:"because,omitempty"`
}

// logProbe is the op key on its own. It is the replay's RECOVERY path, not its routing path: the
// combined logLine below routes every well-formed line in a single decode, and this shape is
// unmarshalled only when that decode failed and the line still has to be classified.
//
// The two line kinds are told apart by the PRESENCE of an op key: a Record has no op field, so a
// line carrying one is a control line and a line without one is a record. This is why a record may
// never grow an op field, and why an unrecognized op is skipped rather than rejected — that is the
// forward-compatibility path for a log written by a newer plugin version.
type logProbe struct {
	Op logKind `json:"op"`
}

// logLine is the union of the two line shapes, and it exists for exactly one reason: the replay
// decodes every line ONCE.
//
// Routing on the op key used to mean unmarshalling each line twice — once into logProbe to read
// the op, then again into a recordWire or a logControl — and encoding/json has to scan the whole
// document either way, so the probe pass roughly doubled the cost of materializing a log. At the
// §11.2 fixture size (20 000 lines averaging ~666 B) that duplicate parsing was most of the margin
// the Open budget has to fit inside. Ruling R26.
//
// The union works because the two shapes barely collide: a control line's "id" and "ts" are the
// same JSON keys, with the same types, that a record line carries. So one struct describes both,
// and ln.Op decides which half of it is meaningful; fields the matched shape does not use stay at
// their zero values, exactly as they would have under the two-step decode.
//
// It is NOT free of consequence, and the honest statement of the cost is this: the union declares
// two keys — "op" and "because" — that a bare record does not have, so a record line spelling
// either of them with a type this struct disagrees with fails a decode that recordWire alone would
// have shrugged off. That is a line the old route MATERIALIZED and this one would drop, which is
// the un-eliminate direction and the one §12 rates High. recoverLine below is what closes it: on
// a failed combined decode it re-runs the exact route the replay used before, so behaviour is
// preserved for every line rather than for most of them. The cost falls only on lines that failed
// once already.
type logLine struct {
	// Op is the discriminator. "" means this line is a bare Record.
	Op logKind `json:"op"`
	// Because is the control line's reason list. A record's own reasons are stale_because, a
	// different key, so the two never overwrite each other.
	Because []string `json:"because,omitempty"`
	// recordWire is the record line's whole shape, embedded so encoding/json promotes its fields
	// into this struct and a record line decodes into it directly. Its ID and TS double as the
	// control line's id and ts.
	recordWire
}

// logPath is <root>/.qompack/records/eliminations.jsonl.
func logPath(root string) string {
	return filepath.Join(paths.Of(root).Records, logFileName)
}

// openLog opens the elimination log for appending, creating the records directory if this is the
// project's first elimination. The returned handle is held for the life of the ledger and every
// append is written through it under the ledger's mutex.
func openLog(root string) (io.WriteCloser, error) {
	p := logPath(root)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), logDirPerm); err != nil {
		return nil, fmt.Errorf("negknow: create %s: %w", filepath.Dir(p), err)
	}
	w, err := paths.AppendOnly(p)
	if err != nil {
		return nil, fmt.Errorf("negknow: open %s: %w", p, err)
	}
	return w, nil
}

// appendLine writes v as one newline-terminated JSONL line in a single Write.
//
// The single write is the durability story: one sub-PIPE_BUF write on POSIX and one WriteFile on
// Windows is atomic against a concurrent appender, so a crash costs at most the whole last line
// and never half of two. There is no fsync per append — the plugin's durability boundary is the
// daemon spool (00-ARCHITECTURE.md §2.4), and a lost tail line costs one elimination, never a
// corrupt index.
//
// HTML escaping is off, matching every other JSON writer in this codebase. encoding/json already
// escapes the control characters that would split a record across two lines, so the interior-
// newline check below can never fire for a value this package produces; it fires only for a
// json.Marshaler that hand-rolls its output, and refusing the write is much cheaper than a file
// every reader silently miscounts.
func appendLine(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("negknow: encode log line: %w", err)
	}
	line := buf.Bytes()
	if bytes.IndexByte(line[:len(line)-1], '\n') >= 0 {
		return fmt.Errorf("%w: negknow: log line contains a raw newline", core.ErrAppendOnly)
	}
	if _, err := w.Write(line); err != nil {
		return fmt.Errorf("negknow: append log line: %w", err)
	}
	return nil
}

// replayLog materializes the elimination log: it returns the records in log order, an index from
// record id to position in that slice, and the number of lines it processed.
//
// Its per-line rules are the file comment's: blank lines are skipped; an unparseable line, or a
// record line with no id, is counted in negknow.log.corrupt_lines and skipped; a second record
// line for an id already seen keeps the FIRST and counts negknow.log.duplicate_add; an op:"stale"
// line REPLACES its record's staleness fields, so a re-flip is idempotent, and counts
// negknow.log.orphan_stale when it names a record the log has not produced; any other op counts
// negknow.log.unknown_op. A torn final line is dropped silently.
//
// log and m are assumed non-nil; a caller holding optional ones passes logging.Nop() and a nop
// registry rather than nil.
func replayLog(r io.Reader, log logging.Logger, m obs.Registry) ([]Record, map[string]int, int, error) {
	var (
		recs []Record
		byID = make(map[string]int)
	)

	corrupt := func(lineNo int, msg string, kv ...any) {
		m.Counter(counterCorruptLines).Add(1)
		log.Warn(msg, append([]any{"line", lineNo}, kv...)...)
	}

	apply := func(line []byte, lineNo int) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return
		}
		// ONE decode per line. This is the whole hot path of materializing a log, and logLine is
		// the union that lets a record line and a control line share it (ruling R26). A line that
		// does not fit the union falls to recoverLine, which re-runs the old two-step route and
		// says which of the three failures it was, so the Warn a reader sees still names the thing
		// that actually went wrong.
		var ln logLine
		if err := json.Unmarshal(line, &ln); err != nil {
			outcome, rerr := recoverLine(line, &ln)
			switch outcome {
			case lineUnparseable:
				corrupt(lineNo, "negknow: skipped an unparseable line in the elimination log", "err", rerr)
				return
			case lineUndecodableRecord:
				corrupt(lineNo, "negknow: skipped an undecodable record in the elimination log", "err", rerr)
				return
			case lineUndecodableControl:
				corrupt(lineNo, "negknow: skipped an undecodable control line", "err", rerr)
				return
			}
		}

		switch ln.Op {
		case "":
			// The record is built from the fields this line already decoded into; nothing is
			// unmarshalled a second time.
			rec, warnings := recordFromWire(ln.recordWire)
			if rec.ID == "" {
				corrupt(lineNo, "negknow: skipped a record with no id in the elimination log")
				return
			}
			for _, w := range warnings {
				log.Warn("negknow: "+w, "line", lineNo, "id", rec.ID)
			}
			if _, dup := byID[rec.ID]; dup {
				m.Counter(counterDuplicateAdd).Add(1)
				log.Warn("negknow: kept the first of two record lines with the same id",
					"line", lineNo, "id", rec.ID)
				return
			}
			byID[rec.ID] = len(recs)
			recs = append(recs, rec)

		case opStale:
			i, ok := byID[ln.ID]
			if !ok {
				m.Counter(counterOrphanStale).Add(1)
				log.Warn("negknow: skipped a stale control line naming an unknown record",
					"line", lineNo, "id", ln.ID)
				return
			}
			recs[i].Status = StatusStale
			recs[i].StaleSince = core.UnixMilli(ln.TS)
			recs[i].StaleBecause = ln.Because

		default:
			m.Counter(counterUnknownOp).Add(1)
			log.Warn("negknow: skipped a control line with an unrecognized op",
				"line", lineNo, "op", string(ln.Op))
		}
	}

	// The scan holds one line back, so that when it ends the final line is still in hand and can be
	// discarded rather than applied if the input turned out to be torn. A Scanner strips the
	// newline either way and cannot tell the two apart, which is what tailTracker is for.
	tail := &tailTracker{r: r}
	sc := bufio.NewScanner(tail)
	sc.Buffer(make([]byte, 0, scanBufferStart), maxLogLineBytes)

	var (
		held     []byte
		heldNo   int
		haveHeld bool
		lineNo   int
		lines    int
	)
	flush := func() {
		if !haveHeld {
			return
		}
		lines++
		apply(held, heldNo)
		haveHeld = false
	}

	for sc.Scan() {
		lineNo++
		flush()
		held = append(held[:0], sc.Bytes()...) // Scanner reuses its buffer; this must own its copy
		heldNo, haveHeld = lineNo, true
	}

	switch err := sc.Err(); {
	case err != nil && errors.Is(err, bufio.ErrTooLong):
		// A line past maxLogLineBytes. The held line is complete, so it is applied; the oversized
		// one is counted and the replay stops, because a Scanner that has hit ErrTooLong cannot
		// resynchronize to the next record boundary and guessing would invent records.
		flush()
		lines++
		corrupt(lineNo+1, "negknow: stopped replaying the elimination log at an oversized line",
			"limit", maxLogLineBytes)
	case err != nil:
		return nil, nil, lines, fmt.Errorf("negknow: read the elimination log: %w", err)
	case haveHeld && tail.torn():
		// The expected artifact of a crash mid-append: the last append reached disk only in part.
		// It is not counted as corruption, because nothing is wrong with the log — the process
		// simply died between two writes.
		haveHeld = false
	default:
		flush()
	}

	return recs, byID, lines, nil
}

// lineOutcome is what the recovery pass concluded about a line the combined logLine decode
// rejected. Its three failure values map one-to-one onto the three Warn messages the replay wrote
// before the single-pass change, because a reader who has to open a damaged log wants to be told
// whether the bytes were not JSON, or were JSON that is not a record, or were a control line whose
// own fields would not read.
type lineOutcome int

const (
	// lineRecovered means ln now holds a usable line and the replay should carry on with it.
	lineRecovered lineOutcome = iota
	// lineUnparseable means the bytes are not JSON at all.
	lineUnparseable
	// lineUndecodableRecord means valid JSON with no op key whose fields do not fit a Record.
	lineUndecodableRecord
	// lineUndecodableControl means a control line whose own id/ts/because would not decode.
	lineUndecodableControl
)

// recoverLine re-runs the replay's ORIGINAL two-step route for a line the combined logLine decode
// rejected, and reports which outcome that route reached. It runs on no other line, so the cost is
// paid only by lines that have already failed once.
//
// It exists because the union is a slightly larger shape than either line kind, in two directions
// that both need covering:
//
//   - A RECORD line may spell "op" or "because" — keys the union declares and recordWire does not —
//     with a type the union disagrees with. recordWire ignores unknown keys, so that line used to
//     materialize, and dropping it now would silently un-eliminate a real elimination. It is
//     decoded through recordWire alone here, exactly as decodeRecord would.
//   - A CONTROL line from a newer plugin version may carry a key recordWire declares with a type
//     it does not expect — {"op":"prune","source":"other-log"}, where "source" is an integer on
//     the wire. Under the old route that line reached the op switch and was skipped as an
//     unrecognized op, which is the documented rule and the whole reason an unknown op is skipped
//     rather than rejected.
//
// The returned error is the one that explains the outcome, so the caller logs the failure that
// actually happened rather than the union's.
func recoverLine(line []byte, ln *logLine) (lineOutcome, error) {
	var probe logProbe
	if err := json.Unmarshal(line, &probe); err != nil {
		return lineUnparseable, err
	}

	if probe.Op == "" {
		var w recordWire
		if err := json.Unmarshal(line, &w); err != nil {
			return lineUndecodableRecord, err
		}
		*ln = logLine{recordWire: w}
		return lineRecovered, nil
	}

	*ln = logLine{Op: probe.Op}
	if probe.Op != opStale {
		// An unrecognized op needs nothing but its name to be counted and skipped.
		return lineRecovered, nil
	}
	// A stale flip does need its fields, and logControl is the narrow shape that reads them
	// without recordWire's same-named keys in the way.
	var c logControl
	if err := json.Unmarshal(line, &c); err != nil {
		return lineUndecodableControl, err
	}
	ln.ID, ln.TS, ln.Because = c.ID, c.TS, c.Because
	return lineRecovered, nil
}

// tailTracker remembers the last byte to come out of the underlying reader, which is the only way
// a streaming replay can tell a terminated final line from a torn one.
type tailTracker struct {
	r    io.Reader
	last byte
	n    int64
}

func (t *tailTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.last = p[n-1]
		t.n += int64(n)
	}
	return n, err
}

// torn reports whether the input ended mid-line. An empty input is not torn.
func (t *tailTracker) torn() bool { return t.n > 0 && t.last != '\n' }
