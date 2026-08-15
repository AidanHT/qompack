package storetest_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/store/storetest"
)

// fakeStubStore mirrors the shape of an SP-01-style stub Store: every operation reports the
// documented zero value or core.ErrNotImplemented, exactly like store.Open's own stub does today.
// It exists only to exercise RunStoreSuite before SP-06 ships a real Store.
type fakeStubStore struct{}

func (fakeStubStore) Put(ctx context.Context, r io.Reader, o store.PutOptions) (store.PutResult, error) {
	return store.PutResult{}, core.ErrNotImplemented
}

func (fakeStubStore) PutBytes(ctx context.Context, b []byte, o store.PutOptions) (store.PutResult, error) {
	return store.PutResult{}, core.ErrNotImplemented
}

func (fakeStubStore) GetChunk(ctx context.Context, h core.Hash) ([]byte, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubStore) GetRoot(ctx context.Context, root core.Hash) (store.Root, error) {
	return store.Root{}, core.ErrNotImplemented
}

func (fakeStubStore) Open(ctx context.Context, root core.Hash) (io.ReadCloser, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubStore) OpenSpan(ctx context.Context, root core.Hash, off, n int64) (io.ReadCloser, error) {
	return nil, core.ErrNotImplemented
}
func (fakeStubStore) Has(h core.Hash) bool { return false }
func (fakeStubStore) RecordToolUse(ctx context.Context, rec store.ToolUseRecord) error {
	return core.ErrNotImplemented
}

func (fakeStubStore) ToolUse(ctx context.Context, id core.ToolUseID) (store.ToolUseRecord, error) {
	return store.ToolUseRecord{}, core.ErrNotImplemented
}

func (fakeStubStore) ToolUsesByPath(ctx context.Context, path string, limit int) ([]store.ToolUseRecord, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubStore) MarkSuperseded(ctx context.Context, older core.ToolUseID, by core.ToolUseID) error {
	return core.ErrNotImplemented
}

func (fakeStubStore) AppendFileVersion(ctx context.Context, path string, v store.FileVersion) error {
	return core.ErrNotImplemented
}

func (fakeStubStore) FileHistory(ctx context.Context, path string) ([]store.FileVersion, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubStore) FileAt(ctx context.Context, path string, at time.Time) (store.FileVersion, error) {
	return store.FileVersion{}, core.ErrNotImplemented
}

func (fakeStubStore) ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubStore) Search(ctx context.Context, q store.Query) ([]store.Hit, error) {
	return nil, core.ErrNotImplemented
}
func (fakeStubStore) Segments() store.SegmentLog { return fakeStubSegmentLog{} }
func (fakeStubStore) Stats(ctx context.Context) (store.Stats, error) {
	return store.Stats{}, core.ErrNotImplemented
}

func (fakeStubStore) GC(ctx context.Context, p store.GCPolicy) (store.GCReport, error) {
	return store.GCReport{}, core.ErrNotImplemented
}
func (fakeStubStore) Flush(ctx context.Context) error { return core.ErrNotImplemented }
func (fakeStubStore) Close() error                    { return core.ErrNotImplemented }

// fakeStubSegmentLog is fakeStubStore's SegmentLog counterpart.
type fakeStubSegmentLog struct{}

func (fakeStubSegmentLog) Open(ctx context.Context, s store.Segment) (core.SegmentID, error) {
	return 0, core.ErrNotImplemented
}

func (fakeStubSegmentLog) Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error {
	return core.ErrNotImplemented
}

func (fakeStubSegmentLog) Get(ctx context.Context, id core.SegmentID) (store.Segment, error) {
	return store.Segment{}, core.ErrNotImplemented
}

func (fakeStubSegmentLog) Range(ctx context.Context, from, to core.TurnIndex) ([]store.Segment, error) {
	return nil, core.ErrNotImplemented
}

func (fakeStubSegmentLog) Current(ctx context.Context, s core.SessionID) (store.Segment, error) {
	return store.Segment{}, core.ErrNotImplemented
}

func (fakeStubSegmentLog) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
	return core.ErrNotImplemented
}

func (fakeStubSegmentLog) Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error) {
	return 0, core.ErrNotImplemented
}

func (fakeStubSegmentLog) Unencoded(ctx context.Context, s core.SessionID) ([]store.Segment, error) {
	return nil, core.ErrNotImplemented
}

// TestRunStoreSuite_StubIsSkipped proves the suite's shape block passes against a stub Store and
// that its behaviour block is skipped with the exact Rule W-1 message. SP-06 reuses RunStoreSuite
// unchanged, pointed at its real implementation, to flip that skip off.
func TestRunStoreSuite_StubIsSkipped(t *testing.T) {
	storetest.RunStoreSuite(t, "fake-stub", func(t *testing.T) store.Store {
		return fakeStubStore{}
	})
}

// TestRunStoreSuite_AgainstRealStore exercises RunStoreSuite against the store store.Open
// actually returns. As of SP-06 that is the real FSStore, so the suite's behaviour block runs
// rather than skipping — which is what Rule W-1's runtime probe exists to switch on.
func TestRunStoreSuite_AgainstRealStore(t *testing.T) {
	storetest.RunStoreSuite(t, "store.Open", func(t *testing.T) store.Store {
		s, err := store.Open(t.TempDir(), config.Defaults(), store.Deps{})
		if err != nil {
			t.Fatal(err)
		}
		// A real store holds open append-only handles, so it MUST be closed before the test's
		// TempDir is removed: on Windows an open handle makes RemoveAll fail and the test error
		// out during cleanup. The SP-01 stub held no handles, which is why this was not needed
		// until SP-06 landed.
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

// TestRunSegmentLogSuite_StubIsSkipped is TestRunStoreSuite_StubIsSkipped's SegmentLog sibling.
func TestRunSegmentLogSuite_StubIsSkipped(t *testing.T) {
	storetest.RunSegmentLogSuite(t, func(t *testing.T) store.SegmentLog {
		return fakeStubSegmentLog{}
	})
}

// TestRunSegmentLogSuite_AgainstRealStore is TestRunStoreSuite_AgainstRealStore's SegmentLog
// sibling.
func TestRunSegmentLogSuite_AgainstRealStore(t *testing.T) {
	storetest.RunSegmentLogSuite(t, func(t *testing.T) store.SegmentLog {
		s, err := store.Open(t.TempDir(), config.Defaults(), store.Deps{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s.Segments()
	})
}
