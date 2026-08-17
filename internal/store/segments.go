package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
)

// The four index/segments.jsonl record operations. The log is append-only, so a segment's
// lifecycle is a sequence of records rather than a mutable row: this is what lets MarkEncoded be
// the mechanical DPI guard of §4.6 without ever rewriting a line.
const (
	segOpOpen   = "open"
	segOpClose  = "close"
	segOpEncode = "encode"
	segOpBloom  = "bloom"
)

// segTokensFeature is the key Close reads Segment.Tokens from inside its feats argument.
//
// Segment.Tokens has no setter and Close runs exactly once per segment, so a caller that omits
// this key leaves Tokens at zero PERMANENTLY. SP-12 closes segments and must always pass it;
// Close logs once per segment when it is missing so the omission is visible rather than silent.
const segTokensFeature = "tokens"

// segOpenRec opens a segment.
type segOpenRec struct {
	V   int            `json:"v"`
	Op  string         `json:"op"`
	ID  core.SegmentID `json:"id"`
	S   core.SessionID `json:"s"`
	St  core.TurnIndex `json:"st"`
	STS core.UnixMilli `json:"sts"`
}

// segCloseRec closes a segment, recording its final turn, token cost and BOCD feature summary.
type segCloseRec struct {
	V    int                `json:"v"`
	Op   string             `json:"op"`
	ID   core.SegmentID     `json:"id"`
	ET   core.TurnIndex     `json:"et"`
	ETS  core.UnixMilli     `json:"ets"`
	Tok  core.Tokens        `json:"tok"`
	Feat map[string]float64 `json:"feat,omitempty"`
}

// segEncodeRec is the DPI guard's durable record: this segment was encoded into checkpoint Seq.
type segEncodeRec struct {
	V   int                `json:"v"`
	Op  string             `json:"op"`
	ID  core.SegmentID     `json:"id"`
	Seq core.CheckpointSeq `json:"seq"`
	TS  core.UnixMilli     `json:"ts"`
}

// segBloomRec names a segment's own per-segment bloom file.
//
// SP-06 PARSES this record and surfaces it as Segment.BloomRef, but never writes one: the writer
// is SP-16's, which is exactly the `"" until SP-16` reservation 00-ARCHITECTURE.md §5.8 describes.
type segBloomRec struct {
	V   int            `json:"v"`
	Op  string         `json:"op"`
	ID  core.SegmentID `json:"id"`
	Ref string         `json:"ref"`
}

// segLog is the append-only segment log backing the scheduler's composite trigger and the
// checkpointer's DPI guard (00-ARCHITECTURE.md §5.8, Qompack.md §8.2, §4.6).
//
// It carries its own mutex rather than sharing FSStore's: a MarkEncoded batch inside PreCompact
// (budget B-E) must not contend with the object-index reads a concurrent Put is doing.
type segLog struct {
	mu    sync.Mutex
	f     *appendFile
	byID  map[core.SegmentID]*Segment
	order []core.SegmentID
	maxID core.SegmentID
	clk   core.Clock
	log   logging.Logger

	// degraded goes up when the owning store closes. Every method then reports core.ErrDegraded,
	// which is what lets FSStore.Segments keep returning a non-nil log after Close without
	// changing a §5.8 signature.
	degraded bool
	// warnedNoTokens records which segments have already produced the missing-tokens warning, so
	// a reopened log does not repeat it per call.
	warnedNoTokens map[core.SegmentID]bool
}

// segLog must satisfy the frozen §5.8 seam.
var _ SegmentLog = (*segLog)(nil)

// openSegLog opens index/segments.jsonl and replays it into memory.
func openSegLog(p string, clk core.Clock, log logging.Logger) (*segLog, error) {
	f, err := openAppendFile(p)
	if err != nil {
		return nil, err
	}
	l := &segLog{
		f:              f,
		byID:           make(map[core.SegmentID]*Segment),
		clk:            clk,
		log:            log,
		warnedNoTokens: make(map[core.SegmentID]bool),
	}
	if err := l.load(p); err != nil {
		_ = f.close()
		return nil, err
	}
	return l, nil
}

// load replays every record in file order.
func (l *segLog) load(p string) error {
	bad, err := scanIndexJSONL(p, func(line []byte) (bool, error) {
		var probe opProbe
		if err := json.Unmarshal(line, &probe); err != nil {
			return false, err
		}
		if probe.V != indexRecordVersion {
			return false, nil
		}
		switch probe.Op {
		case segOpOpen:
			var r segOpenRec
			if err := json.Unmarshal(line, &r); err != nil {
				return false, err
			}
			l.insert(&Segment{
				ID: r.ID, Session: r.S, StartTurn: r.St, EndTurn: r.St, StartTS: r.STS,
			})
			return true, nil
		case segOpClose:
			var r segCloseRec
			if err := json.Unmarshal(line, &r); err != nil {
				return false, err
			}
			if seg, ok := l.byID[r.ID]; ok {
				seg.EndTurn, seg.EndTS, seg.Tokens, seg.Features, seg.Closed = r.ET, r.ETS, r.Tok, r.Feat, true
			}
			return true, nil
		case segOpEncode:
			var r segEncodeRec
			if err := json.Unmarshal(line, &r); err != nil {
				return false, err
			}
			if seg, ok := l.byID[r.ID]; ok {
				seg.EncodedOnce, seg.CheckpointSeq = true, r.Seq
			}
			return true, nil
		case segOpBloom:
			var r segBloomRec
			if err := json.Unmarshal(line, &r); err != nil {
				return false, err
			}
			if seg, ok := l.byID[r.ID]; ok {
				seg.BloomRef = r.Ref
			}
			return true, nil
		default:
			return false, nil
		}
	})
	if err != nil {
		return err
	}
	if bad > 0 {
		l.log.Warn("store: skipped malformed segment index lines", "file", p, "lines", bad)
	}
	return nil
}

// insert adds seg to the in-memory index and keeps maxID monotonic.
func (l *segLog) insert(seg *Segment) {
	if _, dup := l.byID[seg.ID]; !dup {
		l.order = append(l.order, seg.ID)
	}
	l.byID[seg.ID] = seg
	if seg.ID > l.maxID {
		l.maxID = seg.ID
	}
}

// now reads the injected clock.
func (l *segLog) now() core.UnixMilli { return core.UnixMilli(l.clk.Now().UnixMilli()) }

// append writes one record. The caller holds l.mu.
func (l *segLog) append(v any) error {
	line, err := marshalLine(v)
	if err != nil {
		return err
	}
	return l.f.write(line)
}

// sync flushes the log's handle. Flush calls it.
func (l *segLog) sync() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	return l.f.sync()
}

// degrade closes the log's handle and flips every method to core.ErrDegraded. FSStore.Close calls
// it; it does not close the handle separately.
func (l *segLog) degrade() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.degraded = true
	if l.f != nil {
		_ = l.f.close()
	}
}

// Open appends a new, unclosed Segment and returns its assigned ID.
//
// A zero s.ID allocates maxID+1, so IDs are 1-based and monotonic per project. A non-zero ID at or
// below maxID has already been handed out and is refused: reusing one would make two different
// spans of the session indistinguishable in every checkpoint that references them.
func (l *segLog) Open(ctx context.Context, s Segment) (core.SegmentID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return 0, core.ErrDegraded
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	id := s.ID
	switch {
	case id == 0:
		id = l.maxID + 1
	case id <= l.maxID:
		return 0, fmt.Errorf("%w: segment %d", ErrSegmentExists, id)
	}
	if _, dup := l.byID[id]; dup {
		return 0, fmt.Errorf("%w: segment %d", ErrSegmentExists, id)
	}

	startTS := s.StartTS
	if startTS == 0 {
		startTS = l.now()
	}
	if err := l.append(segOpenRec{
		V: indexRecordVersion, Op: segOpOpen, ID: id, S: s.Session, St: s.StartTurn, STS: startTS,
	}); err != nil {
		return 0, err
	}
	l.insert(&Segment{
		ID: id, Session: s.Session, StartTurn: s.StartTurn, EndTurn: s.StartTurn, StartTS: startTS,
	})
	return id, nil
}

// Close marks id closed at endTurn, recording its final BOCD feature summary.
//
// Closing twice at the SAME endTurn is a no-op that writes nothing, so a replayed hook cannot grow
// the log. Closing at a DIFFERENT endTurn is an append-only violation: a segment's span is fixed
// once a checkpoint may already have encoded it.
func (l *segLog) Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return core.ErrDegraded
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	seg, ok := l.byID[id]
	if !ok {
		return fmt.Errorf("%w: segment %d", core.ErrNotFound, id)
	}
	if seg.Closed {
		if seg.EndTurn == endTurn {
			return nil
		}
		return fmt.Errorf("%w: segment %d is already closed at turn %d, refused for %d",
			core.ErrAppendOnly, id, seg.EndTurn, endTurn)
	}
	if endTurn < seg.StartTurn {
		return fmt.Errorf("store: segment %d end turn %d precedes start turn %d",
			id, endTurn, seg.StartTurn)
	}

	clean, tokens, haveTokens := splitSegFeatures(feats)
	if !haveTokens && !l.warnedNoTokens[id] {
		l.warnedNoTokens[id] = true
		l.log.Warn("store: segment closed without a tokens feature; Segment.Tokens stays zero permanently",
			"segment", int(id), "session", string(seg.Session))
	}

	endTS := l.now()
	if err := l.append(segCloseRec{
		V: indexRecordVersion, Op: segOpClose, ID: id, ET: endTurn, ETS: endTS,
		Tok: tokens, Feat: clean,
	}); err != nil {
		return err
	}
	seg.EndTurn, seg.EndTS, seg.Tokens, seg.Features, seg.Closed = endTurn, endTS, tokens, clean, true
	return nil
}

// splitSegFeatures separates the "tokens" pseudo-feature from the BOCD feature summary. The
// returned map is nil when nothing else remains, so an absent "feat" key and an empty one decode
// to the same Segment.
func splitSegFeatures(feats map[string]float64) (map[string]float64, core.Tokens, bool) {
	var (
		clean      map[string]float64
		tokens     core.Tokens
		haveTokens bool
	)
	for k, v := range feats {
		if k == segTokensFeature {
			tokens, haveTokens = core.Tokens(math.Round(v)), true
			continue
		}
		if clean == nil {
			clean = make(map[string]float64, len(feats))
		}
		clean[k] = v
	}
	return clean, tokens, haveTokens
}

// Get looks up id.
func (l *segLog) Get(ctx context.Context, id core.SegmentID) (Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return Segment{}, core.ErrDegraded
	}
	seg, ok := l.byID[id]
	if !ok {
		return Segment{}, fmt.Errorf("%w: segment %d", core.ErrNotFound, id)
	}
	return *seg, nil
}

// Range returns every Segment whose turn range intersects [from, to], ascending by StartTurn.
//
// An OPEN segment has no final EndTurn yet, so it matches whenever to reaches its StartTurn: its
// span is still growing and cannot be excluded on an upper bound it has not reached.
func (l *segLog) Range(ctx context.Context, from, to core.TurnIndex) ([]Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return nil, core.ErrDegraded
	}

	var out []Segment
	for _, id := range l.order {
		seg := l.byID[id]
		var hit bool
		if seg.Closed {
			hit = seg.StartTurn <= to && seg.EndTurn >= from
		} else {
			hit = to >= seg.StartTurn
		}
		if hit {
			out = append(out, *seg)
		}
	}
	sortSegments(out)
	return out, nil
}

// Current returns s's currently open Segment, if any.
func (l *segLog) Current(ctx context.Context, s core.SessionID) (Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return Segment{}, core.ErrDegraded
	}

	var found *Segment
	open := 0
	for _, id := range l.order {
		seg := l.byID[id]
		if seg.Session != s || seg.Closed {
			continue
		}
		open++
		if found == nil || seg.ID > found.ID {
			found = seg
		}
	}
	if found == nil {
		return Segment{}, fmt.Errorf("%w: no open segment for session %s", core.ErrNotFound, s)
	}
	if open > 1 {
		l.log.Loud("store: more than one open segment for a session; using the highest id",
			"session", string(s), "open", open, "using", int(found.ID))
	}
	return *found, nil
}

// MarkEncoded is the DPI guard of §4.6 ("never compress a compression") and §8.2 ("a segment
// already encoded into a checkpoint is never re-encoded from that checkpoint").
//
// The ENTIRE batch is validated before a single record is appended, so a partial DPI violation can
// never half-mark: either every id in ids is legal for seq, or nothing is written at all. Marking
// the same ids into the same seq again is idempotent; marking them into a DIFFERENT seq reports
// core.ErrAlreadyEncoded and leaves every segment's recorded seq untouched.
func (l *segLog) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return core.ErrDegraded
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	pending := make([]core.SegmentID, 0, len(ids))
	for _, id := range ids {
		seg, ok := l.byID[id]
		if !ok {
			return fmt.Errorf("%w: segment %d", core.ErrNotFound, id)
		}
		if !seg.Closed {
			return fmt.Errorf("%w: segment %d", ErrSegmentOpen, id)
		}
		if seg.EncodedOnce {
			if seg.CheckpointSeq == seq {
				continue // idempotent for the same seq
			}
			return fmt.Errorf("%w: segment %d already encoded into checkpoint %d, refused for %d",
				core.ErrAlreadyEncoded, id, seg.CheckpointSeq, seq)
		}
		pending = append(pending, id)
	}

	for _, id := range pending {
		if err := l.append(segEncodeRec{
			V: indexRecordVersion, Op: segOpEncode, ID: id, Seq: seq, TS: l.now(),
		}); err != nil {
			return err
		}
		l.byID[id].EncodedOnce, l.byID[id].CheckpointSeq = true, seq
	}
	return nil
}

// Frontier returns the checkpoint frontier: the turn up to which s has been encoded.
//
// It is the EndTurn of the last CONSECUTIVELY encoded segment, walking the session's segments in
// StartTurn order and stopping at the first unencoded one. Contiguity is the whole point: a
// frontier of N asserts that a checkpoint fully covers the session THROUGH turn N (§8.5 O1/O5),
// which a non-contiguous maximum would misstate — segments 1, 2 and 4 encoded means turn coverage
// ends where segment 2 ends, because segment 3 is still only in the raw log.
//
// Zero is the "no coverage" value, and it is deliberately indistinguishable from "turn 0 is
// covered". Three already-merged consumers — SP-10's TestFocusOmitsSpanWhenFrontierZero, SP-08's
// TestOnSessionStart_ResumeAdoptsFrontierTurn and SP-13's timeline upper bound — all treat it that
// way. Do not "fix" this with a -1 sentinel: it would break all three.
func (l *segLog) Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return 0, core.ErrDegraded
	}

	segs := l.sessionSegmentsLocked(s)
	var frontier core.TurnIndex
	for _, seg := range segs {
		if !seg.EncodedOnce {
			break
		}
		frontier = seg.EndTurn
	}
	return frontier, nil
}

// Unencoded returns every closed Segment of s that no checkpoint has encoded yet, ascending by
// StartTurn. An OPEN segment is not returned: it cannot be encoded until it closes.
func (l *segLog) Unencoded(ctx context.Context, s core.SessionID) ([]Segment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return nil, core.ErrDegraded
	}

	var out []Segment
	for _, seg := range l.sessionSegmentsLocked(s) {
		if seg.Closed && !seg.EncodedOnce {
			out = append(out, seg)
		}
	}
	return out, nil
}

// sessionSegmentsLocked returns copies of s's segments, ascending by StartTurn. The caller holds
// l.mu.
func (l *segLog) sessionSegmentsLocked(s core.SessionID) []Segment {
	var out []Segment
	for _, id := range l.order {
		if seg := l.byID[id]; seg.Session == s {
			out = append(out, *seg)
		}
	}
	sortSegments(out)
	return out
}

// sortSegments orders segments by StartTurn, breaking ties on ID so the result is total and
// deterministic (two segments may legitimately share a StartTurn across sessions).
func sortSegments(segs []Segment) {
	sort.SliceStable(segs, func(i, j int) bool {
		if segs[i].StartTurn != segs[j].StartTurn {
			return segs[i].StartTurn < segs[j].StartTurn
		}
		return segs[i].ID < segs[j].ID
	})
}
