package daemon_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
)

// The rehydration service's half of C1.16's hand-over (session_start_compact.go): the answer is
// offered to the waiting session.start route BEFORE the drop report is written, and a rehydration
// the route has already answered without records itself as undelivered.

// TestService_OffersTheAnswerThenRecordsIt: with a waiting route, the rehydration is offered through
// the ticket and the drop report is the ordinary one — exactly what TestService_RecordsState pins
// for a direct call.
func TestService_OffersTheAnswerThenRecordsIt(t *testing.T) {
	f := rsNewFixture(t)
	ctx, offered := daemon.PendingCompactContext(context.Background())

	out, err := f.svc.OnCompact(ctx, rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	got, ok := offered()
	require.True(t, ok, "a rehydration built for a waiting route must be offered to it")
	require.Equal(t, out, got, "the route receives exactly what was built")
	require.NotEmpty(t, got.HookSpecificOutput.AdditionalContext)

	st := rsReadState(t, f.proj.Root)
	require.False(t, st.Degraded)
	require.NotEmpty(t, st.Items, "a delivered rehydration records the items it emitted")
	for _, d := range st.Dropped {
		require.NotEqual(t, daemon.UndeliveredDropKind, d.Kind, "a delivered rehydration is not undelivered")
	}
}

// TestService_AbandonedRehydrationRecordsItselfUndelivered: the route answered with the deferred
// note, so the model never saw this payload. dropped() must say so first — with where the
// checkpoint can still be read — instead of describing items as emitted that never were.
func TestService_AbandonedRehydrationRecordsItselfUndelivered(t *testing.T) {
	f := rsNewFixture(t)

	_, err := f.svc.OnCompact(daemon.AbandonedCompactContext(context.Background()), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)

	st := rsReadState(t, f.proj.Root)
	require.True(t, st.Degraded, "an undelivered rehydration is a degraded one")
	require.Empty(t, st.Items, "nothing was emitted, so no item may be recorded as emitted")
	require.Zero(t, st.Tokens)
	require.Equal(t, f.reader.ref.Seq, st.Seq)
	require.NotEmpty(t, st.Dropped)
	first := st.Dropped[0]
	require.Equal(t, daemon.UndeliveredDropKind, first.Kind, "the whole rehydration leads the drop report")
	require.Equal(t, "checkpoint-0001", first.ID)
	require.Contains(t, first.Detail, "not delivered")
	require.Contains(t, first.Detail, "restore: Read .qompack/checkpoints/0001.json",
		"the entry points, project-relative, at the checkpoint the model can still read")

	drops, err := rehydrate.NewReporter(f.proj.Root, nil).CurrentDrops(context.Background(), rsSession)
	require.NoError(t, err)
	require.Equal(t, daemon.UndeliveredDropKind, drops[0].Kind, "dropped() reports it first")
}

// TestService_RouteRehydratesMarkBuildsNothing: the observer's own SessionStart call, made beside a
// rehydration the route is building, is bookkeeping: it reads no checkpoint, builds nothing and
// records nothing.
func TestService_RouteRehydratesMarkBuildsNothing(t *testing.T) {
	f := rsNewFixture(t)

	out, err := f.svc.OnCompact(daemon.RouteRehydratesContext(context.Background()), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)
	require.True(t, rsIsEmptyOutput(out))
	require.Zero(t, f.reader.latests, "no checkpoint read for a rehydration nobody will use")
	require.Zero(t, f.tok.count(), "no build")
	require.NoFileExists(t, paths.Long(rsStatePath(f.proj.Root)))
}

// TestService_ReplayedRehydrationRecordsWhyItWasNotDelivered: a compact SessionStart replayed from a
// hook's spool builds a rehydration the model never received — the hook had already answered
// without the daemon — so its drop report leads with the undelivered entry and names the replay,
// which also replaces a report a late live answer left describing it as delivered.
func TestService_ReplayedRehydrationRecordsWhyItWasNotDelivered(t *testing.T) {
	f := rsNewFixture(t)

	_, err := f.svc.OnCompact(daemon.ReplayedCompactContext(context.Background()), rsCompactEvent(f.proj.Root))
	require.NoError(t, err)

	st := rsReadState(t, f.proj.Root)
	require.True(t, st.Degraded)
	require.Empty(t, st.Items, "nothing was emitted")
	require.NotEmpty(t, st.Dropped)
	first := st.Dropped[0]
	require.Equal(t, daemon.UndeliveredDropKind, first.Kind)
	require.Contains(t, first.Detail, daemon.UndeliveredReplayed, "the report says the hook answered without the daemon")
	require.Contains(t, first.Detail, "restore: Read .qompack/checkpoints/0001.json")
}
