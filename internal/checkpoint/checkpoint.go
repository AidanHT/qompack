package checkpoint

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// Writer produces immutable checkpoint artifacts (00-ARCHITECTURE.md §5.14). The Begin/Advance/
// Finalize split exists so that the expensive half — reading originals back out of the store and
// encoding them — happens during idle time (O5), leaving Finalize with nothing but a serialize
// and an append. That is what makes the B-E budget (2 s p99 for the whole PreCompact hook)
// achievable.
type Writer interface {
	// Begin opens a draft for session s, chained to parent (0 for the first checkpoint), reading
	// exclusively from src.
	Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src SourceSet) (*Draft, error)
	// Advance encodes CLOSED, UNENCODED segments into the draft and returns the turn index the
	// frontier reached. It is called during idle time (O5). It calls SegmentLog.MarkEncoded and
	// therefore returns core.ErrAlreadyEncoded on a §4.6 DPI violation — a segment's original
	// content may be encoded into a checkpoint exactly once.
	Advance(ctx context.Context, d *Draft, segs []core.SegmentID) (core.TurnIndex, error)
	// Finalize writes the immutable artifact via paths.CreateNew plus a MANIFEST append, honouring
	// budget through Truncate's tier order. It must complete inside budget B-E, which it can
	// because everything heavy is already in the draft.
	Finalize(ctx context.Context, d *Draft, budget core.Tokens) (Ref, error)
	// Abort discards d without writing anything. It never un-marks an encoded segment: the DPI
	// guard is one-way.
	Abort(d *Draft) error
}

// Reader reads finalized checkpoint artifacts back (00-ARCHITECTURE.md §5.14). It backs
// rehydration (L5), the `why` retrieval tool (L6) and `qompack fsck`.
type Reader interface {
	// Latest returns the most recent checkpoint of session s.
	Latest(ctx context.Context, s core.SessionID) (Checkpoint, Ref, error)
	// Get returns the checkpoint with sequence number seq.
	Get(ctx context.Context, seq core.CheckpointSeq) (Checkpoint, Ref, error)
	// List returns a Ref for every checkpoint on disk, in ascending Seq order.
	List(ctx context.Context) ([]Ref, error)
	// Chain returns seq and every ancestor it descends from, following Parent links.
	Chain(ctx context.Context, seq core.CheckpointSeq) ([]Checkpoint, error)
	// Verify re-hashes every artifact against checkpoints/MANIFEST.jsonl and returns the sequence
	// numbers that do not match. It is what `qompack fsck` reports.
	Verify(ctx context.Context) ([]core.CheckpointSeq, error)
}
