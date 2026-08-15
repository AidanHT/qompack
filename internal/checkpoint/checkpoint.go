package checkpoint

import (
	"context"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
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

// OpenWriter returns a Writer rooted at root. Constructing always succeeds, so wave-0 composition
// roots can wire a checkpoint.Writer today, but every operation is a stub reporting
// core.ErrNotImplemented until SP-10 lands the real checkpointer (00-ARCHITECTURE.md §5.14).
//
// The parameter list is the one SP-10 declares, so that landing the real implementation is a body
// change rather than a call-site change across the tree. SP-10 narrows the return type to its own
// concrete *FileWriter; that is a widening of what callers get, not a change to this contract.
func OpenWriter(root string, cfg config.Config, log logging.Logger, m obs.Registry, clk core.Clock) (Writer, error) {
	return stubWriter{}, nil
}

// OpenReader returns a Reader rooted at root. Constructing always succeeds, for the same reason
// OpenWriter does; every operation reports core.ErrNotImplemented until SP-10 lands.
func OpenReader(root string, log logging.Logger, m obs.Registry) (Reader, error) {
	return stubReader{}, nil
}

// stubWriter is the SP-01 placeholder Writer. SP-10 owns the real implementation.
type stubWriter struct{}

// Begin always reports core.ErrNotImplemented. It returns a nil *Draft rather than an empty one:
// a draft that could be passed on to Advance would be exactly the faked behaviour §14.1 rule 2
// of plans/V1-SP-01-foundation-toolchain-and-contracts.md forbids.
func (stubWriter) Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src SourceSet) (*Draft, error) {
	return nil, core.ErrNotImplemented
}

// Advance always reports core.ErrNotImplemented.
func (stubWriter) Advance(ctx context.Context, d *Draft, segs []core.SegmentID) (core.TurnIndex, error) {
	return 0, core.ErrNotImplemented
}

// Finalize always reports core.ErrNotImplemented.
func (stubWriter) Finalize(ctx context.Context, d *Draft, budget core.Tokens) (Ref, error) {
	return Ref{}, core.ErrNotImplemented
}

// Abort always reports core.ErrNotImplemented.
func (stubWriter) Abort(d *Draft) error { return core.ErrNotImplemented }

// stubReader is the SP-01 placeholder Reader. SP-10 owns the real implementation.
type stubReader struct{}

// Latest always reports core.ErrNotImplemented.
func (stubReader) Latest(ctx context.Context, s core.SessionID) (Checkpoint, Ref, error) {
	return Checkpoint{}, Ref{}, core.ErrNotImplemented
}

// Get always reports core.ErrNotImplemented.
func (stubReader) Get(ctx context.Context, seq core.CheckpointSeq) (Checkpoint, Ref, error) {
	return Checkpoint{}, Ref{}, core.ErrNotImplemented
}

// List always reports core.ErrNotImplemented.
func (stubReader) List(ctx context.Context) ([]Ref, error) { return nil, core.ErrNotImplemented }

// Chain always reports core.ErrNotImplemented.
func (stubReader) Chain(ctx context.Context, seq core.CheckpointSeq) ([]Checkpoint, error) {
	return nil, core.ErrNotImplemented
}

// Verify always reports core.ErrNotImplemented.
func (stubReader) Verify(ctx context.Context) ([]core.CheckpointSeq, error) {
	return nil, core.ErrNotImplemented
}
