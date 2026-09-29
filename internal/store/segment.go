package store

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// Segment is one changepoint-delimited span of a session's turns (00-ARCHITECTURE.md §5.8): the
// unit the scheduler's composite trigger reasons about and the checkpointer encodes.
//
// The json tags are explicit and frozen: this is the shape
// testdata/golden/contracts/store/want/segments_line.jsonl pins byte-for-byte (Rule W-2), field
// for field, in this exact order — one index/segments.jsonl line.
type Segment struct {
	ID      core.SegmentID `json:"id"`
	Session core.SessionID `json:"session"`
	// StartTurn and EndTurn bound this segment's turn range, inclusive.
	StartTurn core.TurnIndex `json:"start_turn"`
	EndTurn   core.TurnIndex `json:"end_turn"`
	StartTS   core.UnixMilli `json:"start_ts"`
	EndTS     core.UnixMilli `json:"end_ts"`
	// Features is the BOCD feature summary captured when this segment closed.
	Features map[string]float64 `json:"features"`
	Tokens   core.Tokens        `json:"tokens"`
	// EncodedOnce is the DPI guard (00-ARCHITECTURE.md §8.2, §4.6): true once this segment has
	// been encoded into a checkpoint from its original content.
	EncodedOnce   bool               `json:"encoded_once"`
	CheckpointSeq core.CheckpointSeq `json:"checkpoint_seq"`
	Closed        bool               `json:"closed"`
	// BloomRef names this segment's own per-segment bloom file, LSM-style (00-ARCHITECTURE.md
	// §6.8); "" until SP-16.
	BloomRef string `json:"bloom_ref"`
}

// SegmentLog is the append-only log of Segments backing the scheduler's composite trigger and
// the checkpointer's DPI guard (00-ARCHITECTURE.md §5.8).
type SegmentLog interface {
	// Open appends a new, unclosed Segment and returns its assigned ID.
	Open(ctx context.Context, s Segment) (core.SegmentID, error)
	// Close marks id closed at endTurn, recording its final BOCD feature summary.
	Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error
	// Get looks up id.
	Get(ctx context.Context, id core.SegmentID) (Segment, error)
	// Range returns every Segment whose turn range intersects [from, to].
	Range(ctx context.Context, from, to core.TurnIndex) ([]Segment, error)
	// Current returns s's currently open Segment, if any.
	Current(ctx context.Context, s core.SessionID) (Segment, error)
	// MarkEncoded is the DPI guard (00-ARCHITECTURE.md §4.6, §8.2): it returns ErrAlreadyEncoded
	// if any id already has EncodedOnce==true with a different CheckpointSeq than seq. It is
	// idempotent for the same seq.
	MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error
	// Frontier returns the checkpoint frontier: the turn up to which s has been encoded.
	Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)
	// Unencoded returns every Segment of s with EncodedOnce==false.
	Unencoded(ctx context.Context, s core.SessionID) ([]Segment, error)
}

// SegmentSync is SegmentLog's optional durability half. Sync makes every record the log has
// appended durable — each MarkEncoded among them — before it returns.
//
// MarkEncoded itself does not sync: nothing depends on an encode record until the checkpoint it
// names is sealed (the idle Advance only reserves — see SegmentReservation). What does depend on them is
// the seal. A checkpoint whose MANIFEST line survives a power cut while the marks naming it do not
// leaves its segments unencoded in the log, and the next draft encodes them again — the §4.6 DPI
// guard broken by a lost tail. checkpoint.Finalize therefore re-marks the draft's segments and calls
// Sync before it appends the MANIFEST line, and skips the call only for a SegmentLog that does not
// offer it (a test double). *segLog, the store's own log, does.
type SegmentSync interface {
	Sync(ctx context.Context) error
}

// SegmentReservation is SegmentLog's optional two-phase encode, so that index/segments.jsonl only
// ever names a checkpoint that has been sealed (F-UAT03-2).
//
// ReserveEncoded is MarkEncoded's DPI guard and in-memory effect with nothing appended: the
// scheduler, Frontier and Unencoded see a reserved segment as encoded, and a reservation into a
// different sequence is core.ErrAlreadyEncoded like any mark. CommitEncoded is the seal's half: it
// appends the encode record for every id at the sequence the artifact was actually written at,
// refusing only an id already DURABLY encoded elsewhere. A reservation is not durable: a log
// reopened after a crash or an idle exit has none, and the draft that made it reserves again from
// its own state file.
//
// checkpoint.Advance reserves and checkpoint.Finalize commits before the MANIFEST line. A
// SegmentLog without this capability (a test double) keeps MarkEncoded's single-phase behaviour.
type SegmentReservation interface {
	ReserveEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error
	CommitEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error
}

// SegmentEncodeFloor is SegmentLog's optional O(1) answer to one question: the highest checkpoint
// sequence a DURABLE encode record names — every one replayed from index/segments.jsonl, and every
// one appended since. A reservation (SegmentReservation) is not a record and does not count.
//
// checkpoint.Begin reads it so that a fresh draft never takes a number an earlier build's encode
// record already claims (F-UAT03-2), without copying the whole log through Range to find out.
type SegmentEncodeFloor interface {
	DurableEncodeFloor() core.CheckpointSeq
}
