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
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// atomicFaultStore is publicationFaultStore's counterpart on the atomic record path: it FORWARDS
// store.SupersedingRecorder, so the observer keeps the atomic record-plus-marks write, and the fault
// is injected into that one write.
//
// On its own it carries only an UNLEASED delivery — one with no nonce, or one the daemon could not
// lease (ingest.leaseDelivery), for which the observer has no identity to publish under. It forwards
// neither store.ObservationRecovery nor store.PublicationSync, and a leased delivery needs both: the
// observer asks the first on every leased delivery before anything else, and NAKs with nothing
// written when it is missing (observer identity.go observationRecord). leasedAtomicFaultStore adds
// those two for the leased path; durableFaultStore adds them and store.DurableObservationPublisher
// for the branch production ships, since the daemon's own store is *store.FSStore.
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
// TestObserverPublicationFailureRemainsDrainRetryable's "tool-use reference" arm on the atomic record
// write, for an UNLEASED delivery: the request carries no nonce, so the daemon assigns it no
// observation identity, and the observer publishes it with no capture link and no publication
// barrier of its own. That is the path a delivery takes when the daemon cannot lease it
// (ingest.leaseDelivery counts it unleased). A leased delivery, which is what a hook sends, is
// TestObserverAtomicLeasedPublicationFailureRemainsDrainRetryable and, on the branch production
// ships, TestObserverDurablePublisherFailureRemainsDrainRetryable.
//
// The legacy row injects its fault into store.RecordToolUse, which the observer stopped calling for a
// tool result when SP08-D2 moved the record and the marks it authors into one index write. It still
// passes, but only because its wrapper drops store.SupersedingRecorder and forces the legacy path —
// so without this row the unleased publication-order property would no longer be measured on the
// atomic write at all.
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
	dr := newDrainer(DrainConfig{Root: root, Seen: dd.ing.seen, Dispatch: withoutLineDeadline(dd.runIngested)})
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

// leasedAtomicFaultStore is atomicFaultStore extended to carry a LEASED delivery, and no further:
// it forwards store.ObservationRecovery and store.PublicationSync from the backing store, each held
// as its own field so that dropping a forward is a compile error, and it deliberately does NOT
// declare store.DurableObservationPublisher.
//
// That makes it the store an observer meets on its non-declaring branch
// (observer identity.go recorderPublishesDurably): the observer proves the record's root durable
// itself before the write and re-proves the publication after it (finishObservation), on top of
// whatever the store's own write does. Production's *store.FSStore declares the barriers, so the
// daemon ships the other branch, which durableFaultStore covers; this one is the path any store that
// does not declare them takes, and the path every leased capture took before SP08-D1.
type leasedAtomicFaultStore struct {
	*atomicFaultStore
	sync     store.PublicationSync
	recovery store.ObservationRecovery
}

var (
	_ store.SupersedingRecorder = (*leasedAtomicFaultStore)(nil)
	_ store.PublicationSync     = (*leasedAtomicFaultStore)(nil)
	_ store.ObservationRecovery = (*leasedAtomicFaultStore)(nil)
)

func newLeasedAtomicFaultStore(t *testing.T, backing store.Store, err error) *leasedAtomicFaultStore {
	t.Helper()
	sync, ok := backing.(store.PublicationSync)
	require.True(t, ok, "fixture: the backing store (%T) must implement store.PublicationSync", backing)
	recovery, ok := backing.(store.ObservationRecovery)
	require.True(t, ok, "fixture: the backing store (%T) must implement store.ObservationRecovery", backing)
	return &leasedAtomicFaultStore{
		atomicFaultStore: newAtomicFaultStore(t, backing, err),
		sync:             sync, recovery: recovery,
	}
}

func (s *leasedAtomicFaultStore) SyncPublication(ctx context.Context, h core.Hash) error {
	return s.sync.SyncPublication(ctx, h)
}

func (s *leasedAtomicFaultStore) RecoverToolUseByObservation(ctx context.Context,
	id core.ObservationID,
) (store.ToolUseRecord, error) {
	return s.recovery.RecoverToolUseByObservation(ctx, id)
}

// The publication sync passes a leased tool capture costs on the observer's non-declaring branch,
// read from the store's own counter (durablePublicationPasses) rather than from a clock.
const (
	// leasedObserverPrePasses is the observer's own pass before the record write: it proves the
	// record's root durable before any index line names it (00-ARCHITECTURE.md §0.2.2), because a
	// non-declaring store is not trusted to. A fault in the write itself therefore lands after it.
	leasedObserverPrePasses = 1
	// leasedPublicationPasses is one whole fresh publication through such a store: the observer's
	// pass before the write, the backing *store.FSStore's two barriers inside it (it carries them
	// whether or not the wrapper declares so), and finishObservation's pass after it.
	leasedPublicationPasses = leasedObserverPrePasses + 2 + 1
)

// TestObserverAtomicLeasedPublicationFailureRemainsDrainRetryable is
// TestObserverAtomicPublicationFailureRemainsDrainRetryable for a LEASED delivery, which is what a
// hook sends: the request carries a nonce, the daemon leases it an observation identity and writes
// its capture sidecar before the observer runs, and the observer publishes under that identity. The
// store forwards what a leased capture needs and does not declare the durable barriers, so the
// observer takes its non-declaring branch and adds its own passes around the write;
// TestObserverDurablePublisherFailureRemainsDrainRetryable is the same property on the declaring
// branch production ships.
//
// The acknowledgement boundary is the same on both branches: a refused record write NAKs, leaves the
// WAL line byte-identical, the capture sidecar unpublished and the frontier uncommitted, and exposes
// no tool-use reference or binding. After repair the drain replays the same lease and publishes
// once, and the pass counts, which are the store's own counter, show the branch that ran.
func TestObserverAtomicLeasedPublicationFailureRemainsDrainRetryable(t *testing.T) {
	root := t.TempDir()
	metrics := obs.New(core.SystemClock())
	backing, err := store.Open(root, testConfig(), store.Deps{Metrics: metrics})
	require.NoError(t, err)
	storeOwned := true
	t.Cleanup(func() {
		if storeOwned {
			_ = backing.Close()
		}
	})
	faulty := newLeasedAtomicFaultStore(t, backing, errors.New("tool-use index refused"))
	passes := func() int64 { return metrics.Counter(durablePublicationPasses).Value() }

	_, dd, o := wireTestDaemon(t, root, func(o *Options) { o.Store = faulty })
	storeOwned = false // wireTestDaemon now owns the supplied store's lifetime
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	// Non-vacuity, before the fault can be reached: the observer writes through this wrapper, the
	// wrapper carries everything a leased capture needs, and it does not claim the barriers, so the
	// branch under test is the non-declaring one.
	require.Same(t, store.Store(faulty), o.Store, "fixture: WireObserver must keep the supplied store")
	for name, capable := range map[string]bool{
		"SupersedingRecorder": isA[store.SupersedingRecorder](o.Store),
		"PublicationSync":     isA[store.PublicationSync](o.Store),
		"ObservationRecovery": isA[store.ObservationRecovery](o.Store),
	} {
		require.True(t, capable, "fixture: the observer's store must implement store.%s", name)
	}
	require.False(t, isA[store.DurableObservationPublisher](o.Store),
		"fixture: the observer's store must not declare the durable barriers, or this row measures the "+
			"declaring branch TestObserverDurablePublisherFailureRemainsDrainRetryable already covers")

	ctx := context.Background()
	const sess core.SessionID = "sess-atomic-leased-publication"
	const toolID core.ToolUseID = "toolu_atomic_leased_publication"
	token := testDeliveryToken('8')
	req := durablePublicationRead(token, sess, toolID)
	require.True(t, dd.dispatchOp(ctx, req).OK, "the transport ACK is the durable acceptance, not the publication")
	job := <-dd.ing.ring
	require.True(t, job.leased, "fixture: the delivery must be leased, or this is the unleased row again")

	var first ipc.Response
	var leasedID core.ObservationID
	dd.ing.dispatch(ctx, func(ctx context.Context, got ipc.Request) ipc.Response {
		leasedID = observer.ObservationFrom(ctx)
		first = dd.runIngested(ctx, got)
		return first
	}, job)
	require.NotEmpty(t, leasedID, "fixture: the handler must run with the leased observation identity")
	require.False(t, first.OK, "an unpublished observation must NAK so its WAL line is retryable")
	require.Equal(t, "observation handling failed", first.Err)
	require.NotContains(t, first.Err, "tool-use index refused")
	require.Equal(t, int64(leasedObserverPrePasses), passes(),
		"a non-declaring store gets the observer's own pass before the write, and the fault is the write")

	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	wal, err := os.ReadFile(walPath(paths.Of(root).Spool, sess, 0))
	require.NoError(t, err)
	require.Equal(t, line, wal, "the failed observation remains byte-for-byte replayable")
	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.False(t, journal.acknowledged(token), "a failed record write must block the committed frontier")
	require.False(t, readOnlySidecar(t, root).Published, "the capture has no reference, so it is not linked")
	_, lookupErr := faulty.ToolUse(ctx, toolID)
	require.ErrorIs(t, lookupErr, core.ErrNotFound, "failed publication must not expose a tool-use reference")
	reader, ok := backing.(store.ObservationReader)
	require.True(t, ok)
	_, lookupErr = reader.ToolUseByObservation(ctx, leasedID)
	require.ErrorIs(t, lookupErr, core.ErrNotFound, "no intent or binding exists for the failed delivery")

	completed, acquired := dd.ing.seen.begin(job.key)
	require.False(t, completed, "only a successful observation may enter the completed seen set")
	require.True(t, acquired, "the failed work remains eligible for retry")
	dd.ing.seen.finish(job.key, false)

	faulty.repair()
	require.NoError(t, dd.ing.CloseSession(sess), "the inactive drainer cannot remove an open WAL on Windows")
	// The drainer Run installs, over this daemon's own journal, so the replay leases the same nonce
	// back; the daemon under test never ran Run.
	dd.drain.Store(newDrainer(contentDrainConfig(dd)))
	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the repaired drainer must actually re-run the retained line")

	require.Equal(t, int64(leasedObserverPrePasses+leasedPublicationPasses), passes(),
		"the retry is a fresh publication on the non-declaring branch: the observer's pass before the "+
			"write, the store's two barriers, and the observer's pass after it")
	rec, err := faulty.ToolUse(ctx, toolID)
	require.NoError(t, err)
	require.NotZero(t, rec.Root, "the repaired publication exposes the stored object through its reference")
	bound, err := reader.ToolUseByObservation(ctx, leasedID)
	require.NoError(t, err, "the replay took the same lease back and committed its binding")
	require.Equal(t, toolID, bound.ID)
	sc := readOnlySidecar(t, root)
	require.True(t, sc.Published, "the capture link is written after the durable record")
	require.Equal(t, toolID, sc.ToolUseID)
	require.Equal(t, rec.Root, sc.Root)
	require.True(t, journal.acknowledged(token), "the replayed publication reaches the frontier")
	require.Equal(t, 1, sp08d2CountTool(t, root, rec.Tool), "one record for one delivery")
	// The session is still live, so its WAL is offset-marked rather than removed; what matters is
	// that the committed offset is past the line, so a later pass has nothing left to publish.
	index := sp08d2Index(t, root)
	n, err = dd.Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "a published, acknowledged line is not replayed again")
	require.Equal(t, int64(leasedObserverPrePasses+leasedPublicationPasses), passes(), "and it syncs nothing more")
	require.Equal(t, string(index), string(sp08d2Index(t, root)), "and it appends nothing")
	require.Zero(t, faulty.legacyCalls(),
		"the observer must publish a tool result through RecordToolUseSuperseding, not RecordToolUse")
}
