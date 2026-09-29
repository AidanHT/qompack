package pins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// logFileName is the append-only truth (00-ARCHITECTURE.md §3.3, §7.4) and viewFileName the
// derived convenience view regenerated from it. Only the first is protected by paths.IsProtected;
// only the second is ever replaced in place.
const (
	logFileName  = "invariants.jsonl"
	viewFileName = "invariants.json"
)

// The two record verbs. The deletion verb is "remove", not "del": that spelling is frozen by
// testdata/golden/contracts/pins/want/tombstone.jsonl (Rule W-2) and may not change without an
// amendment.
const (
	opAdd    = "add"
	opRemove = "remove"
)

// The three legal Invariant.Source values. "user" is a human assertion, "decision" one promoted
// from an extracted decision, and "agent" both the agent's own assertion and the fallback for
// anything else — see normalizeSource for why an unknown value is rewritten rather than refused.
const (
	sourceUser     = "user"
	sourceAgent    = "agent"
	sourceDecision = "decision"
)

// maxTextBytes caps one invariant's text. An invariant is a sentence a human or an agent must
// keep in view forever; past this size it is a document, and a checkpoint's tier-1 block — which
// §6.9 never truncates — would be at the mercy of whatever a caller pasted. The cut lands on a
// rune boundary (capText), never mid-rune, so the log stays valid UTF-8.
const maxTextBytes = 2000

// badLineCounter is the obs.Registry counter a skipped malformed replay line increments.
const badLineCounter = "pins.badline"

// dirPerm and filePerm match paths.EnsureLayout: the runtime store is the user's alone.
const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// viewIndent is the two-space indent invariants.json is rendered with. The staging file the
// replacement is written through belongs to paths.ReplacePinsView, which names it itself; this
// package has no say in that name and must not claim to.
const viewIndent = "  "

// maxLineBytes caps one line of the replayed log.
//
// A record this package writes is bounded — maxTextBytes of text plus a small envelope, and even
// a text made entirely of JSON-escaped control characters expands to well under this ceiling — so
// a line past it is already damage. The cap exists because replay is the one path that promises
// to survive damage, and answering unbounded-allocation damage (a log truncated mid-line, two
// logs concatenated by a bad restore, a file that is not a log at all) with an unbounded
// allocation would break that promise at OpenWith time, inside the daemon.
const maxLineBytes = 32 * maxTextBytes

// record is one line of invariants.jsonl.
//
// It is unexported and lives here rather than in pins.go because it is a wire shape, not part of
// the Store seam: nothing outside this package constructs or reads one. Its field ORDER is the
// contract — encoding/json serializes struct fields in declaration order, so op, ts, then either
// the nested invariant or the bare id is what reaches disk, and the nested object inherits
// Invariant's own id, text, source, pinned order. That is exactly the byte sequence the frozen
// fixtures testdata/golden/contracts/pins/want/{add,tombstone}.jsonl carry (Rule W-2), which is
// why a plain marshal of this struct reproduces them with no manual string assembly.
//
// Invariant is a pointer and ID carries omitempty so that ONE type spells both records: an add
// omits "id", a tombstone omits "invariant". A tombstone therefore carries no "pinned" key at
// all, which the fixture pins.
type record struct {
	Op        string         `json:"op"`
	TS        core.UnixMilli `json:"ts"`
	Invariant *Invariant     `json:"invariant,omitempty"`
	ID        string         `json:"id,omitempty"`
}

// pinStore is the real Store: an append-only log on disk plus the replayed set of live
// invariants in memory (00-ARCHITECTURE.md §5.14).
//
// The log is replayed in full once, by OpenWith, and every later mutation updates both the file and
// the in-memory set. This store is NOT the log's only writer, though, and that is the correction
// F-UAT05-2 made: `qompack pin` runs in its own process and appends to the same log while the
// resident daemon holds a store it opened long before. A store that answered from its open-time
// replay alone sealed the next checkpoint without that pin and — through the checkpoint's and the
// idle task's Materialize — rewrote invariants.json without it too. So every read and every
// mutation first folds in whatever the log gained since this store last read it (catchUpLocked):
// one stat when nothing changed, and only the appended tail when something did. The log is
// append-only, so a tail is all another writer can add; a log that got SHORTER was replaced (a
// restore) and is replayed from the start.
//
// order is the insertion-order half of the ordered map §2 specifies. All sorts its output by
// (Pinned, ID), so order does not decide the answer; it exists so the pre-sort sequence is
// deterministic rather than Go's randomized map iteration, which keeps invariants.json's bytes
// stable across runs for any two invariants that somehow compare equal.
type pinStore struct {
	l   paths.Layout
	log logging.Logger
	m   obs.Registry
	clk core.Clock

	mu    sync.Mutex
	order []string
	live  map[string]Invariant
	seen  map[string]bool

	// cur is how much of the log memory already reflects, and warnedGone whether a log that was
	// read and has since gone has been reported. See catchUpLocked.
	cur        logCursor
	warnedGone bool

	// viewDirty records that the last attempt to regenerate invariants.json failed, so the view on
	// disk is behind both the log and the live set. See materializeLocked for why it is needed and
	// Add and Remove for what acts on it.
	viewDirty bool

	// barriers are the syncs appendLocked makes a record durable through. The zero value is the real
	// thing and is all production uses; a test counts or cuts them (export_test.go).
	barriers paths.Barriers
}

// pinStore is the implementation behind the seam every caller holds.
var _ Store = (*pinStore)(nil)

// logCursor records how far the in-memory set has folded the log.
//
// offset stops at the end of the last NEWLINE-TERMINATED line, never inside a line: a record another
// process is appending at this instant may be visible only in part, and the next fold must start at
// the beginning of that record, not in its middle. size is the file size the last fold saw, and is
// what a later read compares against — an unterminated final line keeps size ahead of offset without
// meaning the log grew again. exists says whether the log was there at all when it was last read.
type logCursor struct {
	offset int64
	size   int64
	exists bool
}

// logPath is <root>/.qompack/pins/invariants.jsonl.
func (s *pinStore) logPath() string { return filepath.Join(s.l.Pins, logFileName) }

// viewPath is <root>/.qompack/pins/invariants.json.
func (s *pinStore) viewPath() string { return filepath.Join(s.l.Pins, viewFileName) }

// Add appends inv to the log and regenerates the view.
//
// It is idempotent by id: re-adding an invariant that is currently live appends NOTHING, because
// an L0 hook that re-asserts the same pin on every turn is the normal case and an unbounded log
// of identical records would be the cost of it. That no-op still regenerates the view first when
// an earlier attempt left it behind, so a retry after a failed Add repairs rather than merely
// agrees. An id that was tombstoned is no longer live, so re-adding it appends again and brings
// it back.
//
// The order of the checks is deliberate. Text is validated first (empty or whitespace-only is
// not an invariant), then capped, and only then is the id minted — so the id always identifies
// the text that was actually STORED, never an oversize input that was thrown away. Pinned
// defaults from the clock, which makes it equal to the record's own ts for a freshly minted
// invariant and different whenever the caller supplied one.
func (s *pinStore) Add(ctx context.Context, inv Invariant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(inv.Text) == "" {
		return fmt.Errorf("pins: empty invariant text")
	}
	inv.Text = s.capText(inv.Text)
	inv.Source = s.normalizeSource(inv.Source)
	if inv.ID == "" {
		inv.ID = MintID(inv.Text)
	}
	if inv.Pinned == 0 {
		inv.Pinned = core.NowMilli(s.clk)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Another process may have pinned or removed this very id since this store last read the log;
	// deciding "already live" from a stale set would skip an append the log needs, or duplicate one.
	if err := s.catchUpLocked(); err != nil {
		return err
	}
	if _, live := s.live[inv.ID]; live {
		// Idempotent by id, but not blindly. If an earlier view write failed, the log and memory
		// already carry this invariant while invariants.json does not, and a caller that read that
		// error as "not recorded" and retried would otherwise get a success that regenerated
		// nothing. This retry is that caller's one chance to close the gap.
		if s.viewDirty {
			return s.materializeLocked()
		}
		return nil
	}
	if err := s.appendLocked(record{Op: opAdd, TS: core.NowMilli(s.clk), Invariant: &inv}); err != nil {
		return err
	}
	s.applyAdd(inv)
	return s.materializeLocked()
}

// Remove appends a tombstone for id. It never rewrites or truncates the log (§7.4): the add
// record stays exactly where it was and the removal is a later line that shadows it.
//
// "Not live" splits in two, and the split matters. An id this project has NEVER recorded is
// core.ErrNotFound — a caller removing a pin that does not exist has a bug, and silently
// succeeding would hide it. An id that IS recorded but already tombstoned is a silent no-op:
// pinstest.RunPinsSuite requires a second Remove to succeed without resurrecting the invariant,
// on the ground that a tombstone is an append-only record rather than a toggle, and appending a
// duplicate tombstone would add a line that changes nothing. That no-op still regenerates the
// view when an earlier attempt left it behind — see the branch itself for why this is the
// direction that does real damage.
func (s *pinStore) Remove(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.catchUpLocked(); err != nil {
		return err
	}
	if _, live := s.live[id]; !live {
		if s.seen[id] {
			// Already tombstoned. The same repair as Add's, in the direction that does real damage:
			// a failed view write leaves the removed invariant still LISTED in invariants.json, so
			// every reader of the view — a rehydrating session, the MCP server — keeps presenting a
			// pin the log says is gone.
			if s.viewDirty {
				return s.materializeLocked()
			}
			return nil
		}
		return fmt.Errorf("%w: pins: no invariant %q", core.ErrNotFound, id)
	}
	if err := s.appendLocked(record{Op: opRemove, TS: core.NowMilli(s.clk), ID: id}); err != nil {
		return err
	}
	s.applyRemove(id)
	return s.materializeLocked()
}

// All returns every live invariant, sorted by Pinned ascending with ties broken by ID ascending.
//
// The tiebreak is not decoration: several invariants pinned inside the same millisecond is the
// ordinary case for a hook that pins a batch, and without it the order of a checkpoint's tier-1
// block would vary between runs and no checkpoint golden could ever be byte-stable.
func (s *pinStore) All(ctx context.Context) ([]Invariant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.catchUpLocked(); err != nil {
		return nil, err
	}
	return s.snapshotLocked(), nil
}

// Materialize regenerates pins/invariants.json from the live set. Add and Remove both call it, so
// a caller only needs it directly to repair a view that was deleted or corrupted out from under
// the store — and checkpoint.FileWriter.Finalize calls it to guarantee the view a rehydrating
// session reads matches the checkpoint that session is restoring.
//
// The live set is brought up to the log first. This is the call that made F-UAT05-2 visible: the
// daemon's idle materialize_pins task and every seal regenerated the view from the set the daemon
// replayed at open, overwriting the view `qompack pin` had just written with one that omitted its
// pin. A log that cannot be read now is an error and the view is left as it is, rather than being
// rewritten from a set nothing vouches for.
func (s *pinStore) Materialize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.catchUpLocked(); err != nil {
		return err
	}
	return s.materializeLocked()
}

// capText enforces maxTextBytes, cutting at the last rune boundary at or below the cap so the
// result is always valid UTF-8, and logs a Warn naming both sizes: silently shortening a pinned
// fact is exactly the kind of loss the pins seam exists to prevent, so it must at least be
// visible in the day log.
func (s *pinStore) capText(text string) string {
	if len(text) <= maxTextBytes {
		return text
	}
	cut := maxTextBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	s.log.Warn("pins: invariant text truncated to the size cap",
		"was_bytes", len(text), "kept_bytes", cut, "cap_bytes", maxTextBytes)
	return text[:cut]
}

// normalizeSource maps src onto the three legal values.
//
// The empty string is the common "caller did not care" case — most hook payloads carry no source
// at all — so it becomes "agent" silently; warning about it would bury the day log in noise. Any
// OTHER unrecognized value is a caller bug: it is still rewritten to "agent" rather than
// rejected, because losing the invariant would be worse than mislabelling it, but the rejected
// spelling is named in a Warn so the bug is findable.
func (s *pinStore) normalizeSource(src string) string {
	switch src {
	case sourceUser, sourceAgent, sourceDecision:
		return src
	case "":
		return sourceAgent
	default:
		s.log.Warn("pins: unknown invariant source rewritten to agent", "source", src)
		return sourceAgent
	}
}

// appendLocked writes one record to the log through paths.AppendJSONLDurable — which opens the file
// with paths.AppendOnly, marshals compactly with HTML escaping DISABLED so a "<" or "&" inside a
// pinned fact survives as itself, terminates the line with exactly one newline, and syncs it (and,
// for the project's first pin, the pins directory that now names the log) before it returns.
//
// The sync is what makes Add's and Remove's success true. Both append here and then regenerate
// invariants.json through paths.ReplacePinsView, which syncs the view and its directory; with an
// unsynced log line, a power cut could keep the derived view and lose its source, and the next
// replay and Materialize would silently drop a pin the user was told was kept (or bring back one
// they removed). The log is the truth (§3.3), so its line is durable first. Pins are written a
// handful of times per session, so one sync per record is not on any hot path.
//
// The handle is opened and closed per record rather than held for the store's lifetime because
// the frozen Store seam has no Close: a long-lived handle would have nothing to release it.
func (s *pinStore) appendLocked(rec record) error {
	if err := s.barriers.AppendJSONLDurable(s.logPath(), rec); err != nil {
		return fmt.Errorf("pins: appending to %s: %w", s.logPath(), err)
	}
	return nil
}

// applyAdd inserts or refreshes inv in the in-memory ordered map. A refresh keeps the
// invariant's existing position, so a log that records the same id twice reports it once with
// the later value.
func (s *pinStore) applyAdd(inv Invariant) {
	if _, ok := s.live[inv.ID]; !ok {
		s.order = append(s.order, inv.ID)
	}
	s.live[inv.ID] = inv
	s.seen[inv.ID] = true
}

// applyRemove drops id from the live set and from the insertion order. seen is deliberately NOT
// cleared: it is the memory that distinguishes "already tombstoned" (a no-op) from "never
// recorded" (core.ErrNotFound) in Remove, and a tombstone for an id whose add record is missing
// still counts as having been recorded.
func (s *pinStore) applyRemove(id string) {
	s.seen[id] = true
	if _, ok := s.live[id]; !ok {
		return
	}
	delete(s.live, id)
	for i, existing := range s.order {
		if existing == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// snapshotLocked builds All's answer: the live invariants in (Pinned, ID) order. The returned
// slice is always non-nil, so an empty set marshals as [] rather than null.
func (s *pinStore) snapshotLocked() []Invariant {
	out := make([]Invariant, 0, len(s.live))
	for _, id := range s.order {
		if inv, ok := s.live[id]; ok {
			out = append(out, inv)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned < out[j].Pinned
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// materializeLocked regenerates the view and records whether the view is now behind the log.
//
// The viewDirty bookkeeping is the whole reason this is a wrapper. Add and Remove append to the
// log and update memory BEFORE they regenerate the view — an ordering that is deliberate, since
// memory must never claim a pin the file does not carry — so a view write that fails leaves the
// invariant durably recorded and live (or durably tombstoned and gone) while invariants.json
// still shows the previous set. The error is returned, because the caller must learn the view is
// behind; but the caller's natural response, retrying the identical Add or Remove, would hit the
// idempotence short-circuit and receive a success that regenerated nothing. Reopening the store
// does not repair it either: replay rebuilds memory from the log and never re-materializes. The
// flag is what turns those short-circuits into the repair.
func (s *pinStore) materializeLocked() error {
	err := s.renderViewLocked()
	s.viewDirty = err != nil
	return err
}

// renderViewLocked renders the live set as a 2-space-indented JSON array with a single trailing
// newline and replaces the view with it.
func (s *pinStore) renderViewLocked() error {
	b, err := json.MarshalIndent(s.snapshotLocked(), "", viewIndent)
	if err != nil {
		return fmt.Errorf("pins: encoding %s: %w", viewFileName, err)
	}
	return s.replaceView(append(b, '\n'))
}

// replaceView writes b over pins/invariants.json.
//
// It goes through paths.ReplacePinsView rather than paths.WriteAtomic, which §2 of the subplan
// names, because that call cannot succeed: IsProtected guards the WHOLE pins/ subtree — as it must,
// since pins/invariants.jsonl is the append-only file carrying the truth — and WriteAtomic refuses
// every protected path outright, so the plan-literal call returns core.ErrAppendOnly and takes
// every Add and Remove down with it.
//
// invariants.json is not that file. It is a projection rebuilt from the log in full whenever the
// log changes, and a protected-but-replaceable derived file gets a door rather than a hole in
// IsProtected — which is exactly what paths.ReplaceBloom already is for sketches/tried.bloom.
// SP-10 added ReplacePinsView beside it, so every write into .qompack still belongs to
// internal/paths and none of them routes around the guard.
func (s *pinStore) replaceView(b []byte) error {
	if err := paths.ReplacePinsView(s.l, b); err != nil {
		return fmt.Errorf("pins: replacing %s: %w", s.viewPath(), err)
	}
	return nil
}

// replay reads the whole log start to end and folds it into the in-memory ordered map. It runs
// once, from OpenWith; every later read folds only what the log gained (catchUpLocked).
//
// A line that cannot be used is SKIPPED, counted on the registry, and reported once per open at
// Warn — never fatal. That asymmetry is the point: a single corrupt record must not cost a
// project every invariant it ever pinned, whereas a log that cannot be READ at all is fatal,
// because answering "no pins" for a file we failed to open would let the next checkpoint silently
// drop tier-1 content. An absent log is neither: it is simply a project with no pins yet.
//
// Records are read with a bounded reader of this package's own rather than a bufio.Scanner
// because a Scanner gives up permanently on a line longer than its buffer, which would turn one
// oversize corrupt line into an aborted replay — the exact failure this function promises never
// to have. The ceiling a Scanner would have supplied is kept all the same: see readCappedLine and
// maxLineBytes. A line past the cap is simply one more shape of corruption, skipped and counted
// like every other one.
func (s *pinStore) replay() error {
	return s.foldFrom(0, true)
}

// catchUpLocked folds into memory whatever the log gained since this store last read it.
//
// Nothing changed is the common case and costs one stat. A log that GREW is read from the cursor:
// the log is append-only (§3.3, §7.4), so the bytes before the cursor are the bytes already folded
// and only the tail can be new. A log SHORTER than the cursor cannot have been appended to — it was
// replaced, which only a restore does — so the set is rebuilt from a full replay rather than read
// past the end of a file that no longer holds what memory says it held. A replacement of equal or
// greater length is indistinguishable from growth by size alone; restore stops the daemon first,
// and a store opened afterwards replays from the start.
//
// A log that is ABSENT, or is no longer a regular file, holds nothing to fold, and the set already
// held is kept rather than emptied: "no pins" would let the next checkpoint drop tier-1 content
// silently, which is the one outcome this seam exists to prevent, and nothing another writer could
// have appended is lost by keeping it — there is no tail to read. A log that WAS read and is now
// gone is abnormal, so it is named once at Warn. A regular log that cannot be read is an error.
func (s *pinStore) catchUpLocked() error {
	fi, err := os.Stat(paths.Long(s.logPath()))
	switch {
	case errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.Mode().IsRegular()):
		if s.cur.exists && !s.warnedGone {
			s.warnedGone = true
			s.log.Warn("pins: the invariant log is gone or not a regular file; keeping the invariants already read",
				"path", s.logPath(), "held", len(s.live))
		}
		return nil
	case err != nil:
		return fmt.Errorf("pins: checking %s: %w", s.logPath(), err)
	}
	s.warnedGone = false
	size := fi.Size()
	if s.cur.exists && size == s.cur.size {
		return nil
	}
	if size < s.cur.offset {
		s.log.Warn("pins: the invariant log is shorter than what was already read; replaying it from the start",
			"path", s.logPath(), "size", size, "read", s.cur.offset)
		s.order, s.live, s.seen = nil, make(map[string]Invariant), make(map[string]bool)
		return s.foldFrom(0, true)
	}
	return s.foldFrom(s.cur.offset, false)
}

// foldFrom reads the log from byte start to its end, folds every usable record into memory, and
// moves the cursor to the end of the last newline-terminated line.
//
// countTail says whether an unusable UNTERMINATED final line counts as damage. The open-time replay
// counts it, as it always has: at open nothing else is writing, so an unterminated tail is what a
// crashed writer left. A catch-up does not, because there it is as likely to be a record another
// process is appending right now; it is read again, from its first byte, by the next fold that
// finds the log changed. A usable unterminated line is applied either way — the replay has always
// kept a record a crashed writer left without its newline — and applying it again later is a no-op.
func (s *pinStore) foldFrom(start int64, countTail bool) error {
	f, err := paths.OpenShared(s.logPath())
	if errors.Is(err, fs.ErrNotExist) && !s.cur.exists {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pins: opening %s: %w", s.logPath(), err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("pins: reading %s: %w", s.logPath(), err)
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return fmt.Errorf("pins: reading %s: %w", s.logPath(), err)
		}
	}

	r := bufio.NewReader(f)
	pos := start
	skipped := 0
	for {
		line, n, oversize, readErr := readCappedLine(r)
		// ReadSlice reports no error exactly when it found the newline, so a nil error is a
		// terminated line and anything else is the file's unterminated tail (or a read failure).
		terminated := readErr == nil
		usable := !oversize && (len(bytes.TrimSpace(line)) == 0 || s.applyLine(line))
		if !usable && (terminated || countTail) {
			skipped++
			s.m.Counter(badLineCounter).Add(1)
		}
		if terminated {
			pos += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return fmt.Errorf("pins: reading %s: %w", s.logPath(), readErr)
		}
	}
	if skipped > 0 {
		s.log.Warn("pins: skipped malformed records while replaying the invariant log",
			"path", s.logPath(), "skipped", skipped)
	}
	s.cur = logCursor{offset: pos, size: max(fi.Size(), pos), exists: true}
	return nil
}

// applyLine folds one raw log line into the in-memory set and reports whether it was usable. An
// add with no invariant, an invariant with no id, a tombstone with no id and an unknown verb are
// all "not usable": they are structurally valid JSON that names nothing, which is indistinguishable
// from corruption for replay purposes.
func (s *pinStore) applyLine(line []byte) bool {
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		return false
	}
	switch rec.Op {
	case opAdd:
		if rec.Invariant == nil || rec.Invariant.ID == "" {
			return false
		}
		s.applyAdd(*rec.Invariant)
		return true
	case opRemove:
		if rec.ID == "" {
			return false
		}
		s.applyRemove(rec.ID)
		return true
	default:
		return false
	}
}

// readCappedLine reads one line, up to and including its terminating '\n', and reports how many
// bytes it consumed, the discarded ones included, and whether that line ran past maxLineBytes.
//
// An oversize line is DISCARDED as it is read rather than returned: the bytes past the cap are
// never accumulated, the bytes before it are released, and the reader is left positioned at the
// start of the next line. One unbounded line therefore costs one skipped record — not an
// unbounded allocation, and not an aborted replay.
//
// It is built on ReadSlice rather than ReadBytes because ReadSlice is the only one of the two
// that lets the caller see how long a line is getting before committing memory to it: it returns
// whatever fits in the reader's fixed buffer and reports bufio.ErrBufferFull when the delimiter
// was not among it. ReadBytes decides instead of asking, allocating however many bytes the file
// claims to want.
func readCappedLine(r *bufio.Reader) (line []byte, n int, oversize bool, err error) {
	for {
		chunk, readErr := r.ReadSlice('\n')
		n += len(chunk)
		if !oversize && len(line)+len(chunk) > maxLineBytes {
			// Past the ceiling. Release what was accumulated so a damaged log cannot pin it in
			// memory, and keep reading only to find the newline this line is missing.
			oversize, line = true, nil
		}
		if !oversize {
			// ReadSlice's result aliases the reader's own buffer and is valid only until the next
			// read, so this append — which copies — is what lets the line outlive the loop.
			line = append(line, chunk...)
		}
		if !errors.Is(readErr, bufio.ErrBufferFull) {
			return line, n, oversize, readErr
		}
	}
}
