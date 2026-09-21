package store

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// Hardening round (final review F1/F2 + main findings 1/2). These tests exercise the properties the
// review demanded that the earlier suite did not: the lifetime append capacity cap, the fail-closed
// posture on an injected intent-sync failure, the refusal to append fresh marks when a committed
// observation is replayed with a DIFFERENT supersede list, and the refusal to forge completion when a
// recorded target is lost or its identity changed.

// TestObservationPublish_AppendCapacityCapRefused (finding 4): the byte and entry ceilings are a
// LIFETIME append bound, enforced BEFORE the write — not merely a load-time bound. At the cap a
// reservation is refused with ErrBudget and nothing is written or bound.
func TestObservationPublish_AppendCapacityCapRefused(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "capacity content")

	// At the entry ceiling, the next append is refused before it opens the sidecar.
	tp.Store.mu.Lock()
	tp.Store.obsSidecarEntries = maxObservationSidecarEntries
	tp.Store.obsSidecarBytes = 0
	tp.Store.mu.Unlock()
	entryObs := obsID(t, 1)
	err := tp.Store.ReserveObservation(ctx, entryObs, recWithObs("toolu_01OBSCAPENTRYAAAAAAAA", root, entryObs), nil)
	require.ErrorIs(t, err, core.ErrBudget, "at the entry cap the append must be refused")

	// At the byte ceiling, likewise.
	tp.Store.mu.Lock()
	tp.Store.obsSidecarEntries = 0
	tp.Store.obsSidecarBytes = maxObservationSidecarBytes
	tp.Store.mu.Unlock()
	byteObs := obsID(t, 2)
	err = tp.Store.ReserveObservation(ctx, byteObs, recWithObs("toolu_01OBSCAPBYTESAAAAAAAA", root, byteObs), nil)
	require.ErrorIs(t, err, core.ErrBudget, "at the byte cap the append must be refused")

	// A refused reservation binds nothing: the observation is still a genuine miss, not degraded, since
	// the cap refusal does not poison completeness.
	tp.Store.mu.Lock()
	tp.Store.obsSidecarEntries = 0
	tp.Store.obsSidecarBytes = 0
	tp.Store.mu.Unlock()
	_, err = tp.Store.ToolUseByObservation(ctx, entryObs)
	require.ErrorIs(t, err, core.ErrNotFound, "a cap-refused reservation must not have created a binding")
}

// TestObservationPublish_InjectedSyncFailureFailsClosed (F2): a failed intent fsync poisons the
// sidecar for the PROCESS LIFETIME — no runtime clear. After the failure an UNKNOWN observation is
// unavailable (not absent) and every new reservation is refused, even once the transient I/O fault is
// gone; only a full store reopen may re-derive the state.
func TestObservationPublish_InjectedSyncFailureFailsClosed(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "poison content")
	obs := obsID(t, 1)
	const id = core.ToolUseID("toolu_01OBSPOISONAAAAAAAAAA")

	// Inject a sync fault on the intent write.
	tp.Store.obsSyncData = func(*os.File) error { return fmt.Errorf("injected intent sync failure") }

	err := tp.Store.ReserveObservation(ctx, obs, recWithObs(id, root, obs), nil)
	require.Error(t, err, "an intent sync failure must surface as an error")

	// The failed reserve left no legacy record.
	_, err = tp.Store.ToolUse(ctx, id)
	require.ErrorIs(t, err, core.ErrNotFound)

	// Completeness is now unprovable: an unknown observation is unavailable, not absent.
	_, err = tp.Store.ToolUseByObservation(ctx, obsID(t, 42))
	require.ErrorIs(t, err, core.ErrDegraded, "after a poison, an unknown observation is unavailable")

	// Clear the transient fault: the sidecar must STAY uncertain (no runtime clear), so a new
	// reservation is still refused.
	tp.Store.obsSyncData = nil
	err = tp.Store.ReserveObservation(ctx, obsID(t, 7), recWithObs("toolu_01OBSPOSTPOISONAAAAAA", root, obsID(t, 7)), nil)
	require.ErrorIs(t, err, core.ErrDegraded, "a poisoned sidecar fails closed for the process lifetime")
}

// TestObservationPublish_CommittedReplayWithDifferentSupersedesRefused (finding 1): once an observation
// is committed, replaying it with a DIFFERENT supersede list is a conflict — it must NOT append fresh,
// unrelated marks. Idempotence and completion always compare the STORED ORIGINAL's canonical bytes.
func TestObservationPublish_CommittedReplayWithDifferentSupersedesRefused(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	rRoot := putRoot(t, tp, "replay record content")
	tRoot := putRoot(t, tp, "replay target content")
	const rID = core.ToolUseID("toolu_01OBSREPLAYRAAAAAAAAA")
	const target = core.ToolUseID("toolu_01OBSREPLAYTAAAAAAAAA")
	obs := obsID(t, 1)

	// A live supersede candidate that the original intent never named.
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: target, Session: "sess-obs", Turn: 1, TS: 1, Tool: "FileRead", Root: tRoot, Path: "src/t.ts",
	}))

	// The original observation commits with NO supersedes.
	require.NoError(t, tp.Store.RecordToolUse(ctx, recWithObs(rID, rRoot, obs)))
	got, err := tp.Store.ToolUseByObservation(ctx, obs)
	require.NoError(t, err)
	require.Equal(t, rID, got.ID)

	// Replaying the SAME committed observation with a different supersede list is refused, and appends
	// no new mark to the target the caller tried to sneak in.
	_, _, err = tp.Store.RecordToolUseSuperseding(ctx, recWithObs(rID, rRoot, obs), []core.ToolUseID{target})
	require.ErrorIs(t, err, core.ErrAppendOnly, "a committed observation may not be re-published with a new supersede list")

	trec, err := tp.Store.ToolUse(ctx, target)
	require.NoError(t, err)
	require.Equal(t, StatusOK, trec.Status, "the caller's foreign target must not have been superseded")

	// The committed binding is unchanged.
	got, err = tp.Store.ToolUseByObservation(ctx, obs)
	require.NoError(t, err)
	require.Equal(t, rID, got.ID)
}

// TestObservationPublish_LostTargetIsUnavailableNotForged (finding 2): a supersede target that was LIVE
// and recorded at reserve, but has since vanished or changed identity, makes recovery report the
// observation unavailable — never a forged success. The recorded target identity is the precondition.
func TestObservationPublish_LostTargetIsUnavailableNotForged(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	aRoot := putRoot(t, tp, "lost-A record content")
	bRoot := putRoot(t, tp, "lost-B record content")
	tARoot := putRoot(t, tp, "target A content")
	tBRoot := putRoot(t, tp, "target B content")

	const rA = core.ToolUseID("toolu_01OBSLOSTRAAAAAAAAAAA")
	const rB = core.ToolUseID("toolu_01OBSLOSTRBAAAAAAAAAA")
	const tA = core.ToolUseID("toolu_01OBSLOSTTAAAAAAAAAAA")
	const tB = core.ToolUseID("toolu_01OBSLOSTTBAAAAAAAAAA")
	obsA := obsID(t, 1)
	obsB := obsID(t, 2)

	// Two live recorded targets.
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{ID: tA, Session: "sess-obs", Turn: 1, TS: 1, Tool: "FileRead", Root: tARoot, Path: "src/ta.ts"}))
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{ID: tB, Session: "sess-obs", Turn: 2, TS: 2, Tool: "FileRead", Root: tBRoot, Path: "src/tb.ts"}))

	// Reserve two intents, each recording its live target's identity, then persist the plain records
	// (the crash cut leaves the marks unwritten).
	recA := recWithObs(rA, aRoot, obsA)
	recB := recWithObs(rB, bRoot, obsB)
	require.NoError(t, tp.Store.ReserveObservation(ctx, obsA, recA, []core.ToolUseID{tA}))
	require.NoError(t, tp.Store.ReserveObservation(ctx, obsB, recB, []core.ToolUseID{tB}))
	plainA := recA
	plainA.Observation = ""
	plainB := recB
	plainB.Observation = ""
	require.NoError(t, tp.Store.RecordToolUse(ctx, plainA))
	require.NoError(t, tp.Store.RecordToolUse(ctx, plainB))

	// Corrupt the world beneath the recorded preconditions: tA vanishes, tB's root changes identity.
	tp.Store.mu.Lock()
	delete(tp.Store.toolUse, tA)
	tp.Store.toolUse[tB].Root = core.HashBytes(core.DomainChunk, []byte("swapped-target-B"))
	tp.Store.mu.Unlock()

	// Recovery of either must refuse to forge completion — the observation is unavailable.
	_, err := tp.Store.RecoverToolUseByObservation(ctx, obsA)
	require.ErrorIs(t, err, core.ErrDegraded, "a vanished recorded target cannot be completed")
	_, err = tp.Store.RecoverToolUseByObservation(ctx, obsB)
	require.ErrorIs(t, err, core.ErrDegraded, "a recorded target whose identity changed cannot be completed")

	// And the committed lookup reports them unavailable, not absent.
	_, err = tp.Store.ToolUseByObservation(ctx, obsA)
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.ToolUseByObservation(ctx, obsB)
	require.ErrorIs(t, err, core.ErrDegraded)
}
