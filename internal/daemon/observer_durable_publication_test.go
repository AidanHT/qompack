package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

// durablePublicationPasses is the store's SyncPublication counter, restated rather than imported
// for the reason the observer's pass tests give: it is a contract with an operator, and a test that
// shares the constant cannot catch a change to it.
const durablePublicationPasses = "store.publication.sync"

// durableFaultStore is atomicFaultStore extended to everything a LEASED tool capture needs from the
// store it is written through, each capability forwarded explicitly from the backing *store.FSStore
// so that dropping one is a compile error:
//
//   - store.ObservationRecovery: the observer asks it first on every leased delivery, to tell a
//     redelivery from a fresh publication (observer identity.go observationRecord). Without it no
//     leased delivery can publish at all; atomicFaultStore does not forward it, which is why its
//     test drives an unleased delivery.
//   - store.DurableObservationPublisher: the declaration that RecordToolUseSuperseding carries the
//     publication's two durability barriers itself. It is what lets the observer drop its own
//     SyncPublication passes around the write (carried defect SP08-D1), and it is the branch the
//     daemon ships, because the store it opens is *store.FSStore.
//   - store.PublicationSync: not needed by a fresh declaring publication, but a redelivery re-proves
//     the original publication through it, and the backing store has it, so the wrapper keeps it
//     rather than turning a regression into a different failure.
//
// The fault is atomicFaultStore's: RecordToolUseSuperseding refuses before it reaches the backing
// store, so nothing is proven, reserved or written until repair.
type durableFaultStore struct {
	*atomicFaultStore
	sync     store.PublicationSync
	recovery store.ObservationRecovery
	durable  store.DurableObservationPublisher
}

var (
	_ store.SupersedingRecorder         = (*durableFaultStore)(nil)
	_ store.PublicationSync             = (*durableFaultStore)(nil)
	_ store.ObservationRecovery         = (*durableFaultStore)(nil)
	_ store.DurableObservationPublisher = (*durableFaultStore)(nil)
)

func newDurableFaultStore(t *testing.T, backing store.Store, err error) *durableFaultStore {
	t.Helper()
	sync, ok := backing.(store.PublicationSync)
	require.True(t, ok, "fixture: the backing store (%T) must implement store.PublicationSync", backing)
	recovery, ok := backing.(store.ObservationRecovery)
	require.True(t, ok, "fixture: the backing store (%T) must implement store.ObservationRecovery", backing)
	durable, ok := backing.(store.DurableObservationPublisher)
	require.True(t, ok, "fixture: the backing store (%T) must declare store.DurableObservationPublisher", backing)
	require.True(t, durable.PublishesObservationsDurably(), "fixture: the backing store must declare the barriers")
	return &durableFaultStore{
		atomicFaultStore: newAtomicFaultStore(t, backing, err),
		sync:             sync, recovery: recovery, durable: durable,
	}
}

func (s *durableFaultStore) SyncPublication(ctx context.Context, h core.Hash) error {
	return s.sync.SyncPublication(ctx, h)
}

func (s *durableFaultStore) RecoverToolUseByObservation(ctx context.Context,
	id core.ObservationID,
) (store.ToolUseRecord, error) {
	return s.recovery.RecoverToolUseByObservation(ctx, id)
}

func (s *durableFaultStore) PublishesObservationsDurably() bool {
	return s.durable.PublishesObservationsDurably()
}

// TestStoreWrapperDropsTheDurablePublisherCapability is TestStoreWrapperDropsTheSupersedingCapability's
// companion for store.DurableObservationPublisher, and pins both directions of its wrapper trap.
//
// A wrapper that embeds the store.Store interface cannot promote PublishesObservationsDurably, which
// the frozen interface does not declare, so an observer over it keeps its own SyncPublication passes
// around every leased write. That direction costs passes, not durability. atomicFaultStore is such a
// wrapper, and it also forwards neither store.PublicationSync nor store.ObservationRecovery, so it
// cannot carry a leased delivery on either branch; its test drives an unleased one.
//
// A type that embeds *store.FSStore inherits the declaration together with every method, including
// any RecordToolUse* it overrides. That direction is the dangerous one: an override that writes
// without calling through still claims the barriers, and the observer would skip passes nothing ran.
// The store documents it at store.DurableObservationPublisher; this test makes the shape loud.
func TestStoreWrapperDropsTheDurablePublisherCapability(t *testing.T) {
	var embedded store.Store = &embeddedOnlyStore{}
	_, ok := embedded.(store.DurableObservationPublisher)
	require.False(t, ok,
		"a wrapper embedding the store.Store interface must NOT satisfy store.DurableObservationPublisher; "+
			"if it now does, the declaration has been folded into the frozen Store interface and every "+
			"wrapper in the tree claims barriers it may not carry")

	var atomic store.Store = &atomicFaultStore{}
	_, ok = atomic.(store.DurableObservationPublisher)
	require.False(t, ok, "atomicFaultStore forwards SupersedingRecorder only and must not declare the barriers")

	var forwarding store.Store = &durableFaultStore{atomicFaultStore: &atomicFaultStore{}}
	_, ok = forwarding.(store.DurableObservationPublisher)
	require.True(t, ok, "a wrapper that forwards the method explicitly keeps the declaration")

	var inherited store.Store = &struct{ *store.FSStore }{}
	_, ok = inherited.(store.DurableObservationPublisher)
	require.True(t, ok,
		"a type embedding *store.FSStore inherits the declaration whatever it overrides; if this "+
			"changes, update the trap documented at store.DurableObservationPublisher")
}

// durablePublicationRead is a leased observe.tool delivery carrying a real Read event, so the bound
// observer records an index entry for it.
func durablePublicationRead(nonce string, sess core.SessionID, id core.ToolUseID) ipc.Request {
	return ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: core.UnixMilli(epoch.UnixMilli()),
		Event: &hookio.Event{
			HookEventName: "PostToolUse", SessionID: sess,
			ToolName: "Read", ToolUseID: id,
			ToolInput:    json.RawMessage(`{"file_path":"src/durable.go"}`),
			ToolResponse: json.RawMessage(`{"content":"package durable\n"}`),
		},
		Capture: admittedCapture(`{"hook_event_name":"PostToolUse"}`), Nonce: nonce,
	}
}

// TestObserverDurablePublisherFailureRemainsDrainRetryable is
// TestObserverAtomicPublicationFailureRemainsDrainRetryable on the branch the daemon ships since
// SP08-D1: a LEASED delivery, written through a store that declares store.DurableObservationPublisher,
// so the observer adds no SyncPublication of its own around the record write.
//
// The acknowledgement boundary is unchanged by dropping those passes: a failed record write NAKs,
// leaves the WAL line, the capture sidecar unpublished and the frontier uncommitted, and exposes no
// tool-use reference. After repair the drain replays the same lease and publishes once, with the
// store's two barriers and no more (the store's own counter, not a clock), then commits the frontier.
func TestObserverDurablePublisherFailureRemainsDrainRetryable(t *testing.T) {
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
	faulty := newDurableFaultStore(t, backing, errors.New("tool-use index refused"))
	passes := func() int64 { return metrics.Counter(durablePublicationPasses).Value() }

	_, dd, o := wireTestDaemon(t, root, func(o *Options) { o.Store = faulty })
	storeOwned = false // wireTestDaemon now owns the supplied store's lifetime
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	// Non-vacuity, before the fault can be reached: the observer writes through this wrapper, and the
	// wrapper carries every capability production's store does.
	require.Same(t, store.Store(faulty), o.Store, "fixture: WireObserver must keep the supplied store")
	for name, capable := range map[string]bool{
		"SupersedingRecorder":         isA[store.SupersedingRecorder](o.Store),
		"PublicationSync":             isA[store.PublicationSync](o.Store),
		"ObservationRecovery":         isA[store.ObservationRecovery](o.Store),
		"DurableObservationPublisher": isA[store.DurableObservationPublisher](o.Store),
	} {
		require.True(t, capable, "fixture: the observer's store must implement store.%s", name)
	}

	ctx := context.Background()
	const sess core.SessionID = "sess-durable-publication"
	const toolID core.ToolUseID = "toolu_durable_publication"
	token := testDeliveryToken('7')
	req := durablePublicationRead(token, sess, toolID)
	require.True(t, dd.dispatchOp(ctx, req).OK, "the transport ACK is the durable acceptance, not the publication")
	job := <-dd.ing.ring
	require.True(t, job.leased, "fixture: the delivery must be leased, or no publication barrier is exercised")

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
	require.Zero(t, passes(),
		"the observer adds no pass before a declaring store's write, and the fault precedes the store's first")

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
	dd.drain.Store(newDrainer(dd.drainConfig()))
	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the repaired drainer must actually re-run the retained line")

	require.Equal(t, int64(2), passes(),
		"the retry is a fresh publication: the store's two §0.2.2 barriers, none of the observer's")
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
	require.Equal(t, int64(2), passes(), "and it syncs nothing more")
	require.Equal(t, string(index), string(sp08d2Index(t, root)), "and it appends nothing")
	require.Zero(t, faulty.legacyCalls(),
		"the observer must publish a tool result through RecordToolUseSuperseding, not RecordToolUse")
}

// isA reports whether v implements T.
func isA[T any](v any) bool {
	_, ok := v.(T)
	return ok
}
