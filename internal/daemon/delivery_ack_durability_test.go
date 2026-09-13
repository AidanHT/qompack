package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// T29 — design §6.2. The hook's ACK byte never precedes the durability of the batch that carries
// its delivery.
//
// The group-commit tests pin the ordering INSIDE the journal: T10 shows no lease is released before
// its batch's seal. This one pins the same ordering at the boundary the promise is actually made
// at. internal/ipc's server writes the one-byte ACK when the registered handler returns
// (server.go's dispatch, then the ACK write), and dispatchOp is that handler, so "dispatchOp has
// not returned" and "no ACK has been written" are one fact — which is what makes §2.4's boundary
// ("the ACK is a promise that the delivery survives a crash") a thing a test can hold.
//
// The seal is held, and it is held at its own durability point: in format 1 before the v1 sidecar's
// paths.WriteAtomic runs at all, and in format 2 inside the slot's SyncData, after its WriteAt.
// Everything earlier on the path — the WAL's Sync, the journal's Write and its Sync — has already
// returned by then, so what the gate holds is precisely the LAST durability point standing between
// the delivery and the promise made to the hook.
func TestDaemon_NoACKBeforeDurableBatch(t *testing.T) {
	for _, format := range []int{1, 2} {
		t.Run(formatName(format), func(t *testing.T) {
			root := t.TempDir()
			dd, _ := newObservingDaemon(t, root)
			lock := lockFor(t, dd, root)
			lock.sealFormat = format
			t.Cleanup(func() { _ = lock.Release() })

			journal, err := dd.deliveryJournal()
			require.NoError(t, err)
			requireNoLeakedInflight(t, journal)

			gate := newWALGate(t)
			holdFirstSeal(t, journal, format, gate)

			token := testDeliveryToken('a')
			req := observeRequest(token, "sess-no-ack", `{"hook_event_name":"PostToolUse"}`)
			resp := make(chan ipc.Response, 1)
			go func() { resp <- dd.dispatchOp(context.Background(), req) }()
			awaitClosed(t, gate.entered, "the batch's seal")

			// The seal is in flight. No response has been produced, so no ACK has been written; the
			// identity is not admitted, so no later caller can see it either.
			requireNoDispatchResponse(t, resp, "while its batch's seal was still in flight")
			require.Zero(t, admittedLeases(journal), "an unsealed lease must not be admitted")
			require.Empty(t, dd.ing.ring, "and no job may be queued for it")

			if format == 1 {
				// Format 1's seal is one paths.WriteAtomic, and the gate holds before it runs, so the
				// sidecar on disk still seals the empty journal.
				//
				// Format 2 has no such assertion to make, and the reason is worth stating: its gate
				// holds INSIDE SyncData, after the slot's WriteAt, so the file already reads back as
				// the new record through the page cache while nothing of it is durable. That gap —
				// bytes visible but not yet flushed — is exactly why a crash image has to be built
				// from the syncs that RETURNED (T28) rather than from what the file happens to show.
				position, perr := journal.loadPosition()
				require.NoError(t, perr)
				require.Zero(t, position.Count, "the seal cannot name a lease it has not made durable")
			}

			// The journal's own line is already written and synced at this point: the seal is the one
			// step left, and it is the one being held.
			info, err := os.Stat(paths.Long(journal.path))
			require.NoError(t, err)
			require.NotZero(t, info.Size(), "the batch's line is durable; only its seal is outstanding")

			gate.release()
			got := awaitDispatchResponse(t, resp)
			require.True(t, got.OK, "once the seal returns, the delivery is durable and the ACK goes out")
			require.Empty(t, got.Err)
			require.Equal(t, 1, admittedLeases(journal), "and exactly one identity is admitted")
			require.True(t, journal.acknowledged(token) == false,
				"the frontier is the worker's own stage and has not run: the ACK is the transport's, not publication's")

			position, err := journal.loadPosition()
			require.NoError(t, err)
			require.Equal(t, 1, position.Count, "the seal names the lease the ACK promised")
		})
	}
}

// holdFirstSeal makes the journal's first seal wait on gate at its own durability point, then run
// for real. Format 1 is held before savePosition's paths.WriteAtomic; format 2 inside the held
// seal's SyncData, which is the slot write's own flush.
func holdFirstSeal(t *testing.T, j *deliveryJournal, format int, gate *walGate) {
	t.Helper()
	if format != 2 {
		sealLease := j.sealLease
		var held bool
		j.sealLease = func(size int64, count int, chain core.Hash) error {
			if !held {
				held = true
				gate.hold()
			}
			return sealLease(size, count, chain)
		}
		return
	}
	require.NotNil(t, j.seal, "a format-2 journal holds its lease seal open")
	syncData := j.seal.syncData
	var held bool
	j.seal.syncData = func(f *os.File) error {
		if !held {
			held = true
			gate.hold()
		}
		return syncData(f)
	}
}

// requireNoDispatchResponse fails when dispatchOp has already answered, which on this path means
// the ACK byte would already have been written.
func requireNoDispatchResponse(t *testing.T, resp <-chan ipc.Response, why string) {
	t.Helper()
	select {
	case got := <-resp:
		t.Fatalf("dispatchOp answered %+v %s: the ACK precedes the delivery's durability", got, why)
	default:
	}
}

// awaitDispatchResponse waits for dispatchOp's answer. The bound only turns a deadlock into a
// failure; it orders nothing.
func awaitDispatchResponse(t *testing.T, resp <-chan ipc.Response) ipc.Response {
	t.Helper()
	select {
	case got := <-resp:
		return got
	case <-time.After(ingestACKWait):
		t.Fatal("dispatchOp never answered once its batch's seal was released")
		return ipc.Response{}
	}
}
