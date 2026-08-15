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

// stubSegmentLog is the SP-01 placeholder SegmentLog returned by stubStore.Segments. SP-06 owns
// the real implementation.
type stubSegmentLog struct{}

// Open always reports core.ErrNotImplemented.
func (stubSegmentLog) Open(ctx context.Context, s Segment) (core.SegmentID, error) {
	return 0, core.ErrNotImplemented
}

// Close always reports core.ErrNotImplemented.
func (stubSegmentLog) Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error {
	return core.ErrNotImplemented
}

// Get always reports core.ErrNotImplemented.
func (stubSegmentLog) Get(ctx context.Context, id core.SegmentID) (Segment, error) {
	return Segment{}, core.ErrNotImplemented
}

// Range always reports core.ErrNotImplemented.
func (stubSegmentLog) Range(ctx context.Context, from, to core.TurnIndex) ([]Segment, error) {
	return nil, core.ErrNotImplemented
}

// Current always reports core.ErrNotImplemented.
func (stubSegmentLog) Current(ctx context.Context, s core.SessionID) (Segment, error) {
	return Segment{}, core.ErrNotImplemented
}

// MarkEncoded always reports core.ErrNotImplemented.
func (stubSegmentLog) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
	return core.ErrNotImplemented
}

// Frontier always reports core.ErrNotImplemented.
func (stubSegmentLog) Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error) {
	return 0, core.ErrNotImplemented
}

// Unencoded always reports core.ErrNotImplemented.
func (stubSegmentLog) Unencoded(ctx context.Context, s core.SessionID) ([]Segment, error) {
	return nil, core.ErrNotImplemented
}
