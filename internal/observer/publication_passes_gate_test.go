package observer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// forwardingNoBarrierStore forwards every capability the leased tool path uses — publication sync,
// the atomic record-plus-marks write and observation recovery — but not
// store.DurableObservationPublisher, because it embeds the store.Store interface. It stands for any
// recorder that does not declare the barriers.
type forwardingNoBarrierStore struct {
	store.Store
	sync     store.PublicationSync
	idx      store.SupersedingRecorder
	recovery store.ObservationRecovery
}

func (s *forwardingNoBarrierStore) SyncPublication(ctx context.Context, h core.Hash) error {
	return s.sync.SyncPublication(ctx, h)
}

func (s *forwardingNoBarrierStore) RecordToolUseSuperseding(ctx context.Context, rec store.ToolUseRecord,
	older []core.ToolUseID,
) ([]core.ToolUseID, bool, error) {
	return s.idx.RecordToolUseSuperseding(ctx, rec, older)
}

func (s *forwardingNoBarrierStore) RecoverToolUseByObservation(ctx context.Context,
	id core.ObservationID,
) (store.ToolUseRecord, error) {
	return s.recovery.RecoverToolUseByObservation(ctx, id)
}

// TestPublicationPasses_RecorderWithoutTheBarriersKeepsTheObserversPasses pins the gate: the
// observer drops its own passes only when the value it records through declares the barriers. A
// recorder that does not keeps the observer's pass before the write and its pass before the link,
// on top of whatever the recorder does inside the call.
func TestPublicationPasses_RecorderWithoutTheBarriersKeepsTheObserversPasses(t *testing.T) {
	r := buildPassRig(t, t.TempDir(), func(st store.Store) store.Store {
		sync, ok := st.(store.PublicationSync)
		require.True(t, ok)
		idx, ok := st.(store.SupersedingRecorder)
		require.True(t, ok)
		recovery, ok := st.(store.ObservationRecovery)
		require.True(t, ok)
		return &forwardingNoBarrierStore{Store: st, sync: sync, idx: idx, recovery: recovery}
	})
	_, declares := r.o.opt.Store.(store.DurableObservationPublisher)
	require.False(t, declares, "fixture: the wrapper must not promote the capability")

	id := r.sidecar(1, rdxOpTool)
	_, err := r.o.OnToolUse(WithObservation(context.Background(), id), readOf("toolu_W", supersedePath, rdxBody))
	require.NoError(t, err)
	require.Equal(t, int64(4), r.passes(),
		"the observer's two passes plus the store's two: nothing is dropped without the declaration")
	r.requirePublished(id, "toolu_W")
}

// crashBeforePublishStore is a real store whose atomic record write dies before it does anything:
// the capture's bytes are stored (PutBytes ran) and nothing has proven or named them yet. It embeds
// *store.FSStore, so it keeps every capability, the barrier declaration included.
type crashBeforePublishStore struct {
	*store.FSStore
}

func (s *crashBeforePublishStore) RecordToolUseSuperseding(context.Context, store.ToolUseRecord,
	[]core.ToolUseID,
) ([]core.ToolUseID, bool, error) {
	return nil, false, errInjectedPublishCrash
}

var errInjectedPublishCrash = errors.New("injected crash before the store's first barrier")

// TestPublicationPasses_CutAfterThePutPublishesOnceOnRedelivery is the fresh path's first cut, the
// one the store's own step tests cannot reach: the observer stored the bytes and the process died
// before the store proved the root or wrote the intent. Nothing names the bytes, so the redelivery
// in a restarted process finds no publication to recover and publishes afresh — once, with the two
// barriers, and linked.
func TestPublicationPasses_CutAfterThePutPublishesOnceOnRedelivery(t *testing.T) {
	r := buildPassRig(t, t.TempDir(), func(st store.Store) store.Store {
		fs, ok := st.(*store.FSStore)
		require.True(t, ok)
		return &crashBeforePublishStore{FSStore: fs}
	})
	ctx := context.Background()
	id := r.sidecar(1, rdxOpTool)
	ev := readOf("toolu_cut", supersedePath, rdxBody)
	_, err := r.o.OnToolUse(WithObservation(ctx, id), ev)
	require.ErrorIs(t, err, ErrUnpublished)
	require.NotEmpty(t, r.roots(), "fixture: the cut came after the put")
	require.Empty(t, r.index(), "fixture: the cut came before any record")
	require.Zero(t, r.passes(), "fixture: the cut came before the store's first barrier")

	require.NoError(t, r.st.Close())
	r = buildPassRig(t, r.root, func(st store.Store) store.Store { return st })
	r.clock.Advance(time.Second)
	r.sidecar(1, rdxOpTool)
	_, err = r.o.OnToolUse(WithObservation(ctx, id), ev)
	require.NoError(t, err)
	require.Equal(t, int64(2), r.passes(), "the redelivery is a fresh publication: two barriers")
	require.Equal(t, []string{"toolu_cut"}, rdxIDs(t, r.index(), ""), "one record for one delivery")
	r.requirePublished(id, "toolu_cut")
}
