package checkpointtest_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/checkpoint/checkpointtest"
	"github.com/qompack/qompack/internal/core"
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

// TestCheckpointSuite_ShapePassesAgainstStub proves the checkpointtest suites' shape blocks pass
// against a stub Writer and Reader, and that every behaviour block is skipped with the exact
// Rule W-1 message.
//
// SP-10 removed the three drivers that pointed these suites at checkpoint.OpenWriter,
// checkpoint.OpenReader and checkpoint.Truncate. Those three asserted that the SP-01
// implementations WERE stubs — which is precisely what SP-10 falsifies — so after this branch they
// would have run their behaviour blocks for real against an empty t.TempDir() and an empty
// SourceSet, failing on a missing fixture rather than on anything being wrong. The fakes below
// stay stubs forever, so they keep proving exactly what this test's name promises.
//
// The real conformance runs live in the implementation package, where a populated SourceSet and a
// seeded checkpoint directory can actually be built: TestWriterConformanceSuite and
// TestReaderConformanceSuite in internal/checkpoint, and TestTruncateConformanceSuite for
// RunTruncateSuite. That is the same split the subplan uses for pinstest.RunPinsSuite, which it
// drives from a test inside internal/pins.
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

	t.Run("reader-fake-stub", func(t *testing.T) {
		checkpointtest.RunReaderSuite(t, "fake-stub", func(t *testing.T) checkpointtest.ReaderFixture {
			return readerFixture(t, fakeStubReader{})
		})
	})
}
