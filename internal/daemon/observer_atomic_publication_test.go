package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// atomicFaultStore is publicationFaultStore's counterpart on the write path production actually
// takes: it FORWARDS store.SupersedingRecorder, so the observer keeps the atomic
// record-plus-marks path, and the fault is injected into that one write.
//
// The forwarding is the whole point and is not incidental. A wrapper written the ordinary way —
// embedding store.Store and overriding a method — cannot promote RecordToolUseSuperseding, because
// §5.8's frozen Store interface does not declare it; such a wrapper silently drops the capability
// and puts the observer back on the legacy separate-writes path (see embeddedOnlyStore below).
// This type holds the capability as its own field so a dropped forward is a compile error rather
// than a test that quietly measures a path nothing ships.
type atomicFaultStore struct {
	store.Store
	// idx is the backing store's capability, called through explicitly.
	idx store.SupersedingRecorder

	mu sync.Mutex
	// err is what RecordToolUseSuperseding reports, or nil once repaired.
	err error
	// recordCalls counts legacy RecordToolUse calls. An observe.tool delivery on the atomic path
	// makes none; a wrapper that dropped the capability would make one per delivery.
	recordCalls int
}

var _ store.SupersedingRecorder = (*atomicFaultStore)(nil)

func newAtomicFaultStore(t *testing.T, backing store.Store, err error) *atomicFaultStore {
	t.Helper()
	idx, ok := backing.(store.SupersedingRecorder)
	require.True(t, ok, "fixture: the backing store (%T) must implement store.SupersedingRecorder", backing)
	return &atomicFaultStore{Store: backing, idx: idx, err: err}
}

func (s *atomicFaultStore) RecordToolUseSuperseding(ctx context.Context, rec store.ToolUseRecord,
	older []core.ToolUseID,
) ([]core.ToolUseID, bool, error) {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return nil, false, err
	}
	return s.idx.RecordToolUseSuperseding(ctx, rec, older)
}

func (s *atomicFaultStore) RecordToolUse(ctx context.Context, rec store.ToolUseRecord) error {
	s.mu.Lock()
	s.recordCalls++
	s.mu.Unlock()
	return s.Store.RecordToolUse(ctx, rec)
}

func (s *atomicFaultStore) repair() {
	s.mu.Lock()
	s.err = nil
	s.mu.Unlock()
}

func (s *atomicFaultStore) legacyCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordCalls
}

// embeddedOnlyStore is a store wrapper written the way every other one in this repository is
// written: the interface embedded, one method overridden. It exists to pin what that costs.
type embeddedOnlyStore struct {
	store.Store
}

// TestStoreWrapperDropsTheSupersedingCapability pins the trap the atomic path introduces for
// anyone wrapping a Store, in the package where three such wrappers live.
//
// store.SupersedingRecorder is deliberately NOT part of the frozen store.Store interface, so a type
// that embeds that interface cannot promote RecordToolUseSuperseding — the promotion follows the
// embedded type's method set, and store.Store declares RecordToolUse and MarkSuperseded only. The
// wrapper therefore fails observer.New's capability assertion however real its backing store is,
// and the observer falls back to the 1+N separate-writes path carried defect SP08-D2's second
// mechanism lives in. WireObserver opens a store only when Options.Store is nil, so a wrapper a
// caller presets reaches the observer unchanged.
//
// Nothing is wrong at runtime — production is *FSStore and is pinned by
// TestWireObserverStoreSupportsSupersedingRecorder — but two tests in this package and one in
// test/e2e run the observer over such a wrapper, so this is a live coverage fact rather than a
// hypothetical, and it is worth failing loudly the day the shape changes.
func TestStoreWrapperDropsTheSupersedingCapability(t *testing.T) {
	var w store.Store = &embeddedOnlyStore{}
	_, ok := w.(store.SupersedingRecorder)
	require.False(t, ok,
		"a wrapper embedding the store.Store interface must NOT satisfy store.SupersedingRecorder; "+
			"if it now does, the capability has been folded into the frozen Store interface and every "+
			"wrapper in the tree silently changed which write path it measures")

	var forwarding store.Store = &atomicFaultStore{}
	_, ok = forwarding.(store.SupersedingRecorder)
	require.True(t, ok, "a wrapper that forwards the method explicitly keeps the capability")
}

// TestObserverAtomicPublicationFailureRemainsDrainRetryable is
// TestObserverPublicationFailureRemainsDrainRetryable's "tool-use reference" arm on the write path
// production takes.
//
// That test injects its fault into store.RecordToolUse, which the observer stopped calling for a
// tool result when SP08-D2 moved the record and the marks it authors into one index write. It still
// passes, but only because its wrapper drops store.SupersedingRecorder and forces the legacy path —
// so without this row the daemon's publication-order property would no longer be measured anywhere
// on the path the daemon actually ships.
//
// The property is the acknowledgement boundary, unchanged: a Store failure is not an observer
// warning that may be ACKed, and the WAL stays the retry source until a tool-use reference is
// published. What is new here is only where the fault lands.
func TestObserverAtomicPublicationFailureRemainsDrainRetryable(t *testing.T) {
	root := t.TempDir()
	backing, err := store.Open(root, testConfig(), store.Deps{})
	require.NoError(t, err)
	storeOwned := true
	t.Cleanup(func() {
		if storeOwned {
			_ = backing.Close()
		}
	})
	faulty := newAtomicFaultStore(t, backing, errors.New("tool-use index refused"))

	_, dd, o := wireTestDaemon(t, root, func(o *Options) { o.Store = faulty })
	storeOwned = false // wireTestDaemon now owns the supplied store's lifetime
	t.Cleanup(func() { _ = dd.ing.Close() })

	// Non-vacuity, asserted before the fault can be reached: the observer this daemon wired is on
	// the atomic path. A wrapper that stopped forwarding the capability would make every assertion
	// below measure the legacy path instead, silently, which is the failure mode this row exists to
	// keep out of the daemon's publication coverage.
	require.Same(t, store.Store(faulty), o.Store, "fixture: WireObserver must keep the supplied store")
	_, capable := o.Store.(store.SupersedingRecorder)
	require.True(t, capable,
		"the Store handed to the observer (%T) must implement store.SupersedingRecorder, or this test "+
			"measures the legacy separate-writes path rather than the one production takes", o.Store)

	req := ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-atomic-publication", TS: core.NowMilli(dd.clk),
		Event: &hookio.Event{
			HookEventName: "PostToolUse", SessionID: "sess-atomic-publication", CWD: root,
			ToolName: "Read", ToolUseID: "toolu_atomic_publication",
			ToolInput:    json.RawMessage(`{"file_path":"src/atomic.go"}`),
			ToolResponse: json.RawMessage(`{"content":"package atomic\n"}`),
		},
	}
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.NoError(t, dd.ing.Accept(req, line))
	job := <-dd.ing.ring

	var first ipc.Response
	dd.ing.dispatch(context.Background(), func(ctx context.Context, got ipc.Request) ipc.Response {
		first = dd.runIngested(ctx, got)
		return first
	}, job)
	require.False(t, first.OK, "an unpublished observation must NAK so its WAL line is retryable")
	require.Equal(t, "observation handling failed", first.Err)
	require.NotContains(t, first.Err, "tool-use index refused")
	walFilePath := walPath(paths.Of(root).Spool, req.Session, 0)
	require.FileExists(t, walFilePath)
	wal, readErr := os.ReadFile(walFilePath)
	require.NoError(t, readErr)
	require.Equal(t, line, wal, "the failed observation remains byte-for-byte replayable")
	_, lookupErr := faulty.ToolUse(context.Background(), req.Event.ToolUseID)
	require.ErrorIs(t, lookupErr, core.ErrNotFound, "failed publication must not expose a tool-use reference")

	completed, acquired := dd.ing.seen.begin(job.key)
	require.False(t, completed, "only a successful observation may enter the completed seen set")
	require.True(t, acquired, "the failed work remains eligible for retry")
	dd.ing.seen.finish(job.key, false)

	faulty.repair()
	require.NoError(t, dd.ing.CloseSession(req.Session), "the inactive drainer cannot remove an open WAL on Windows")
	dr := newDrainer(DrainConfig{Root: root, Seen: dd.ing.seen, Dispatch: dd.runIngested})
	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "the repaired drainer must actually re-run the retained line")
	rec, lookupErr := faulty.ToolUse(context.Background(), req.Event.ToolUseID)
	require.NoError(t, lookupErr)
	require.NotZero(t, rec.Root, "the repaired publication exposes the stored object through its reference")
	require.NoFileExists(t, walFilePath)

	// Neither the failed attempt nor the repaired retry took the legacy path. A tool result is the
	// only thing this session observed, and on the atomic path its record never reaches
	// store.RecordToolUse — so a non-zero count here means the capability was dropped somewhere
	// between this wrapper and observer.New.
	require.Zero(t, faulty.legacyCalls(),
		"the observer must publish a tool result through RecordToolUseSuperseding, not RecordToolUse")
}
