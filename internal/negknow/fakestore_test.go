package negknow

import (
	"context"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// fakeStore is the scripted, counting store.Store the staleness and bloom tests drive
// RefreshStaleness with. It implements exactly the three methods this package ever calls —
// ChangedSince, FileHistory and PutBytes — and reports core.ErrNotImplemented from every other
// method of SP-06's interface, so a future call into a part of the store negknow is not supposed
// to touch fails loudly here instead of silently returning a zero value.
//
// The compile-time assertion below is the point of writing it out in full: it is what keeps this
// fake honest against store.Store as SP-06 evolves it, rather than against a hand-copied subset
// that quietly stops matching.
var _ store.Store = (*fakeStore)(nil)

type fakeStore struct {
	mu sync.Mutex

	// history is the scripted FileHistory per paths.Key path. A path absent from the map has no
	// history at all, which is store's "reported unchanged" case (§5.8) — the SP05-D1 shape the
	// subplan's inherited-constraints item 2 is about.
	history map[string][]store.FileVersion

	// changed is what ChangedSince reports back, matched against its argument by path AND hash so
	// a script cannot accidentally flip a record that depends on a different version of the file.
	changed []core.Dep
	// changedErr, when non-nil, is returned instead of a result.
	changedErr error
	// blockChangedSince, when > 0, makes ChangedSince wait that long BEFORE answering — but it
	// waits on ctx.Done() as well, so a caller's deadline always wins. It is never an unguarded
	// time.Sleep: §6.1 bans those outright, _test.go files included (devtool lint's sleepcheck
	// scans them, unlike golangci-lint's forbidigo rule).
	blockChangedSince time.Duration

	// changedCalls records every ChangedSince argument, in call order. Its length IS the
	// "exactly one call" assertion.
	changedCalls [][]core.Dep

	// putBytes records every PutBytes payload, in call order.
	putBytes [][]byte
}

// newFakeStore returns a fake with no scripted history and nothing reported changed.
func newFakeStore() *fakeStore {
	return &fakeStore{history: map[string][]store.FileVersion{}}
}

// withHistory scripts path's version history.
func (f *fakeStore) withHistory(path string, vs ...store.FileVersion) *fakeStore {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history[path] = vs
	return f
}

// reportChanged scripts the deps ChangedSince will report back as changed.
func (f *fakeStore) reportChanged(deps ...core.Dep) *fakeStore {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changed = deps
	return f
}

// calls returns a copy of the recorded ChangedSince call log.
func (f *fakeStore) calls() [][]core.Dep {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]core.Dep, len(f.changedCalls))
	copy(out, f.changedCalls)
	return out
}

// callCount returns how many times ChangedSince has been called.
func (f *fakeStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.changedCalls)
}

// ChangedSince returns the subset of deps this fake was scripted to report changed, recording its
// argument. It matches on path AND hash, exactly as a real store's "current hash differs from the
// recorded one" comparison does.
func (f *fakeStore) ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error) {
	f.mu.Lock()
	recorded := make([]core.Dep, len(deps))
	copy(recorded, deps)
	f.changedCalls = append(f.changedCalls, recorded)
	block, scripted, err := f.blockChangedSince, f.changed, f.changedErr
	f.mu.Unlock()

	if block > 0 {
		t := time.NewTimer(block)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
		}
	}
	if err != nil {
		return nil, err
	}

	want := make(map[string]struct{}, len(scripted))
	for _, d := range scripted {
		want[d.Path+"\x1f"+d.Hash.String()] = struct{}{}
	}
	var out []core.Dep
	for _, d := range deps {
		if _, ok := want[d.Path+"\x1f"+d.Hash.String()]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// FileHistory serves the scripted history for path. A path with no script has no history, which
// is not an error: store reports it as "unchanged" rather than as missing.
func (f *fakeStore) FileHistory(ctx context.Context, path string) ([]store.FileVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vs := f.history[path]
	out := make([]store.FileVersion, len(vs))
	copy(out, vs)
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out, nil
}

// PutBytes mints a deterministic root over b, so a test can predict the evidence hash a future
// ingest helper would record without running a real content-addressed store.
func (f *fakeStore) PutBytes(ctx context.Context, b []byte, o store.PutOptions) (store.PutResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	f.putBytes = append(f.putBytes, cp)
	return store.PutResult{Root: store.Root{Hash: fakeStoreRoot(b)}}, nil
}

// fakeStoreRoot is the deterministic root PutBytes returns for b.
func fakeStoreRoot(b []byte) core.Hash { return core.HashBytes("negknow.fakestore", b) }

// Everything below is store.Store's remaining surface. negknow never calls any of it, and each
// reports core.ErrNotImplemented so that a call added by mistake fails here rather than reading a
// zero value as an answer.

func (f *fakeStore) Put(ctx context.Context, r io.Reader, o store.PutOptions) (store.PutResult, error) {
	return store.PutResult{}, core.ErrNotImplemented
}

func (f *fakeStore) GetChunk(ctx context.Context, h core.Hash) ([]byte, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) GetRoot(ctx context.Context, root core.Hash) (store.Root, error) {
	return store.Root{}, core.ErrNotImplemented
}

func (f *fakeStore) Open(ctx context.Context, root core.Hash) (io.ReadCloser, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) OpenSpan(ctx context.Context, root core.Hash, off, n int64) (io.ReadCloser, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) Has(h core.Hash) bool { return false }

func (f *fakeStore) RecordToolUse(ctx context.Context, rec store.ToolUseRecord) error {
	return core.ErrNotImplemented
}

func (f *fakeStore) ToolUse(ctx context.Context, id core.ToolUseID) (store.ToolUseRecord, error) {
	return store.ToolUseRecord{}, core.ErrNotImplemented
}

func (f *fakeStore) ToolUsesByPath(ctx context.Context, path string, limit int) ([]store.ToolUseRecord, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) MarkSuperseded(ctx context.Context, older, by core.ToolUseID) error {
	return core.ErrNotImplemented
}

func (f *fakeStore) AppendFileVersion(ctx context.Context, path string, v store.FileVersion) error {
	return core.ErrNotImplemented
}

func (f *fakeStore) FileAt(ctx context.Context, path string, at time.Time) (store.FileVersion, error) {
	return store.FileVersion{}, core.ErrNotImplemented
}

func (f *fakeStore) Search(ctx context.Context, q store.Query) ([]store.Hit, error) {
	return nil, core.ErrNotImplemented
}

func (f *fakeStore) Segments() store.SegmentLog { return fakeSegmentLog{} }

func (f *fakeStore) Stats(ctx context.Context) (store.Stats, error) {
	return store.Stats{}, core.ErrNotImplemented
}

func (f *fakeStore) GC(ctx context.Context, p store.GCPolicy) (store.GCReport, error) {
	return store.GCReport{}, core.ErrNotImplemented
}

func (f *fakeStore) Flush(ctx context.Context) error { return core.ErrNotImplemented }

func (f *fakeStore) Close() error { return nil }

// fakeSegmentLog is fakeStore's SegmentLog counterpart. negknow never reaches it.
type fakeSegmentLog struct{}

func (fakeSegmentLog) Open(ctx context.Context, s store.Segment) (core.SegmentID, error) {
	return 0, core.ErrNotImplemented
}

func (fakeSegmentLog) Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error {
	return core.ErrNotImplemented
}

func (fakeSegmentLog) Get(ctx context.Context, id core.SegmentID) (store.Segment, error) {
	return store.Segment{}, core.ErrNotImplemented
}

func (fakeSegmentLog) Range(ctx context.Context, from, to core.TurnIndex) ([]store.Segment, error) {
	return nil, core.ErrNotImplemented
}

func (fakeSegmentLog) Current(ctx context.Context, s core.SessionID) (store.Segment, error) {
	return store.Segment{}, core.ErrNotImplemented
}

func (fakeSegmentLog) MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error {
	return core.ErrNotImplemented
}

func (fakeSegmentLog) Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error) {
	return 0, core.ErrNotImplemented
}

func (fakeSegmentLog) Unencoded(ctx context.Context, s core.SessionID) ([]store.Segment, error) {
	return nil, core.ErrNotImplemented
}

// TestFakeStoreScripting is the fake's own smoke test: without it a mis-scripted fake would show
// up as a confusing failure inside a staleness test rather than as a failure of the fake.
func TestFakeStoreScripting(t *testing.T) {
	f := newFakeStore()
	d1 := core.Dep{Path: "a.yml", Hash: core.HashBytes("t", []byte("1"))}
	d2 := core.Dep{Path: "b.yml", Hash: core.HashBytes("t", []byte("2"))}
	f.reportChanged(d1)

	got, err := f.ChangedSince(context.Background(), []core.Dep{d1, d2})
	require.NoError(t, err)
	require.Equal(t, []core.Dep{d1}, got, "ChangedSince reports only what it was scripted to")
	require.Equal(t, 1, f.callCount())
}
