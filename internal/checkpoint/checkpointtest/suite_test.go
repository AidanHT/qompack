package checkpointtest_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/checkpoint/checkpointtest"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/tokens"
)

// fakeStubWriter mirrors the shape of an SP-01-style stub Writer: every operation reports
// core.ErrNotImplemented, exactly like checkpoint.OpenWriter's own stub does today. It exists so
// the suites are exercised against a second, independent stub as well as against the real one.
type fakeStubWriter struct{}

func (fakeStubWriter) Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src checkpoint.SourceSet) (*checkpoint.Draft, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubWriter) Advance(ctx context.Context, d *checkpoint.Draft, segs []core.SegmentID) (core.TurnIndex, error) {
	return 0, core.ErrNotImplemented
}

func (fakeStubWriter) Finalize(ctx context.Context, d *checkpoint.Draft, budget core.Tokens) (checkpoint.Ref, error) {
	return checkpoint.Ref{}, core.ErrNotImplemented
}

func (fakeStubWriter) Abort(d *checkpoint.Draft) error { return core.ErrNotImplemented }

// fakeStubReader is fakeStubWriter's Reader counterpart.
type fakeStubReader struct{}

func (fakeStubReader) Latest(ctx context.Context, s core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	return checkpoint.Checkpoint{}, checkpoint.Ref{}, core.ErrNotImplemented
}

func (fakeStubReader) Get(ctx context.Context, seq core.CheckpointSeq) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	return checkpoint.Checkpoint{}, checkpoint.Ref{}, core.ErrNotImplemented
}

func (fakeStubReader) List(ctx context.Context) ([]checkpoint.Ref, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubReader) Chain(ctx context.Context, seq core.CheckpointSeq) ([]checkpoint.Checkpoint, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubReader) Verify(ctx context.Context) ([]core.CheckpointSeq, error) {
	return nil, core.ErrNotImplemented
}

// writerFixture builds the WriterFixture the suites are run with. The SourceSet is left zero:
// populating it needs store, negknow, pins, dag, grammar and tokens seams that only have stubs
// today, and Begin never reaches them.
func writerFixture(t *testing.T, w checkpoint.Writer) checkpointtest.WriterFixture {
	t.Helper()
	return checkpointtest.WriterFixture{
		Writer:   w,
		Source:   checkpoint.SourceSet{},
		Session:  core.SessionID("sess_checkpointtest_stub"),
		Segments: []core.SegmentID{1, 2},
		Budget:   core.Tokens(1000),
	}
}

// readerFixture builds the ReaderFixture the suites are run with.
func readerFixture(t *testing.T, r checkpoint.Reader) checkpointtest.ReaderFixture {
	t.Helper()
	return checkpointtest.ReaderFixture{
		Reader:    r,
		Session:   core.SessionID("sess_checkpointtest_stub"),
		Seq:       core.CheckpointSeq(1),
		AbsentSeq: core.CheckpointSeq(9999),
	}
}

// truncateFunc binds checkpoint.Truncate to the default tier assignment and a real token
// estimator — the two arguments checkpointtest may not name itself (00-ARCHITECTURE.md §3.2).
func truncateFunc(t *testing.T) checkpointtest.TruncateFunc {
	t.Helper()
	cfg := config.Defaults()
	est := tokens.New(cfg, filepath.Join(t.TempDir(), "calibration.json"))
	return func(c checkpoint.Checkpoint, budget core.Tokens) (checkpoint.Checkpoint, []checkpoint.DropEntry) {
		return checkpoint.Truncate(c, budget, cfg.Checkpoint.Tiers, est)
	}
}

// TestCheckpointSuite_ShapePassesAgainstStub proves all three checkpointtest suites' shape blocks
// pass against the SP-01 stubs — both the real checkpoint.OpenWriter/OpenReader/Truncate stubs
// and an independent fake — and that every behaviour block is skipped with the exact Rule W-1
// message. SP-10 reuses these suites unchanged, pointed at its real implementation, to flip those
// skips off.
//
// Each suite runs inside its own t.Run wrapper. That is load-bearing, not cosmetic: Rule W-1's
// t.Skip fires on the *T the suite was handed, so calling several suites directly from one test
// function would let the first stub skip abort the rest of them before they ever ran.
func TestCheckpointSuite_ShapePassesAgainstStub(t *testing.T) {
	t.Run("writer-fake-stub", func(t *testing.T) {
		checkpointtest.RunWriterSuite(t, "fake-stub", func(t *testing.T) checkpointtest.WriterFixture {
			return writerFixture(t, fakeStubWriter{})
		})
	})

	t.Run("writer-openwriter-stub", func(t *testing.T) {
		checkpointtest.RunWriterSuite(t, "checkpoint.OpenWriter-stub", func(t *testing.T) checkpointtest.WriterFixture {
			w, err := checkpoint.OpenWriter(t.TempDir(), config.Defaults(), logging.Nop(), obs.New(core.SystemClock()), core.SystemClock())
			if err != nil {
				t.Fatal(err)
			}
			return writerFixture(t, w)
		})
	})

	t.Run("reader-fake-stub", func(t *testing.T) {
		checkpointtest.RunReaderSuite(t, "fake-stub", func(t *testing.T) checkpointtest.ReaderFixture {
			return readerFixture(t, fakeStubReader{})
		})
	})

	t.Run("reader-openreader-stub", func(t *testing.T) {
		checkpointtest.RunReaderSuite(t, "checkpoint.OpenReader-stub", func(t *testing.T) checkpointtest.ReaderFixture {
			r, err := checkpoint.OpenReader(t.TempDir(), logging.Nop(), obs.New(core.SystemClock()))
			if err != nil {
				t.Fatal(err)
			}
			return readerFixture(t, r)
		})
	})

	t.Run("truncate-stub", func(t *testing.T) {
		checkpointtest.RunTruncateSuite(t, "checkpoint.Truncate-stub", truncateFunc)
	})
}
