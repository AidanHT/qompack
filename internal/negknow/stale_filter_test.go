package negknow

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// A record goes stale, never absent (coordinator decision D49 of the V6 close-out, finding R4-1 of
// the candidate 4 live re-run, plans/sdd/V6-closeout/live/rerun-c4/UAT-09/notes.txt).
//
// The query filter used to hold ACTIVE records only. Record adds its keys to the in-memory filter
// and nothing else, so a record that went stale before the next persisted rebuild was never in the
// on-disk tried.bloom; a restarted daemon loaded that filter, found no active record missing from
// it, did not rebuild, and Query's first !bloom.Test answered absent before it read the stale
// record. An idle rebuild dropped a stale key in a long-running daemon the same way. The filter now
// covers every record that exists, active and stale, at Open, at every rebuild and at reconcile.

const (
	staleFilterTarget   = "config/pool.yaml:max_idle"
	staleFilterApproach = "raise max_idle to 16"
	staleFilterReason   = "the pool still starves under load"
)

// recordThenFlip records one session-scoped elimination for sess through a daemon-shaped ledger
// (no session of its own) and flips it stale, without any rebuild in between.
func recordThenFlip(t *testing.T, l *ledger, sess string) string {
	t.Helper()
	ctx := asCaller(core.SessionID(sess), 3)
	id, err := l.Record(ctx, newRecord("stale-filter", staleFilterTarget, staleFilterApproach, staleFilterReason))
	require.NoError(t, err)
	require.NoError(t, l.MarkStale(ctx, []string{id}, []string{"config/pool.yaml: dependency hash changed"}))
	return id
}

// TestQuery_StaleRecordAnswersStaleAfterRestart is the observed failure: a daemon restart between
// the flip and the next question. The on-disk filter was persisted at the first Open, before the
// record existed, and never again.
func TestQuery_StaleRecordAnswersStaleAfterRestart(t *testing.T) {
	root, cfg := newProject(t)
	first := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	id := recordThenFlip(t, first, "sess-a")
	require.NoError(t, first.Close())

	second := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	got, err := second.Query(asCaller("sess-a", 9), staleFilterTarget, staleFilterApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerStale, got.State, "a stale record must answer stale after a restart, never absent")
	require.NotNil(t, got.Record)
	require.Equal(t, id, got.Record.ID)
	require.Equal(t, StaleNote, got.Note)
}

// TestQuery_StaleRecordIsUncertainAfterRestartUnderDrop: the same restart under
// eliminations.staleResponse "drop" hides the staleness detail, not the record (§11.3 invariant 8).
func TestQuery_StaleRecordIsUncertainAfterRestartUnderDrop(t *testing.T) {
	root, cfg := newProject(t)
	first := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	recordThenFlip(t, first, "sess-a")
	require.NoError(t, first.Close())

	cfg.Eliminations.StaleResponse = staleResponseDrop
	second := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	got, err := second.Query(asCaller("sess-a", 9), staleFilterTarget, staleFilterApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerUncertain, got.State, "drop withholds the detail; it never turns a record into an absence")
}

// TestQuery_StaleRecordAnswersStaleAfterColdOpen: with no tried.bloom on disk at all, Open rebuilds
// from the records, and that rebuild must carry the stale record's keys too.
func TestQuery_StaleRecordAnswersStaleAfterColdOpen(t *testing.T) {
	root, cfg := newProject(t)
	first := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	recordThenFlip(t, first, "sess-a")
	require.NoError(t, first.Close())
	require.NoError(t, os.Remove(paths.Long(bloomPath(root))))

	second := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	got, err := second.Query(asCaller("sess-a", 9), staleFilterTarget, staleFilterApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerStale, got.State)
}

// TestQuery_StaleRecordAnswersStaleAfterIdleRebuild is the long-running daemon's path: the flip
// owes a nextIdle rebuild, and the idle maintenance task that pays it must not drop the stale key.
func TestQuery_StaleRecordAnswersStaleAfterIdleRebuild(t *testing.T) {
	root, cfg := newProject(t)
	require.Equal(t, rebuildNextIdle, cfg.Eliminations.RebuildOnStale)
	l := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	recordThenFlip(t, l, "sess-a")
	require.True(t, l.NeedsRebuild(), "the flip owes a rebuild under nextIdle")

	_, _, run := l.MaintenanceTask(nil)
	require.NoError(t, run(context.Background()))
	require.False(t, l.NeedsRebuild(), "the idle task paid the rebuild it owed")

	got, err := l.Query(asCaller("sess-a", 9), staleFilterTarget, staleFilterApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerStale, got.State, "an idle rebuild must not drop a stale record's key")

	// And the filter the idle rebuild persisted is the one a restart loads.
	require.NoError(t, l.Close())
	again := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	got, err = again.Query(asCaller("sess-a", 10), staleFilterTarget, staleFilterApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerStale, got.State)
}

// TestQuery_StaleRecordAnswersStaleAfterImmediateRebuild: under rebuildOnStale "immediate" the flip
// itself rebuilds, and that rebuild must keep the key too.
func TestQuery_StaleRecordAnswersStaleAfterImmediateRebuild(t *testing.T) {
	root, cfg := newProject(t)
	cfg.Eliminations.RebuildOnStale = rebuildImmediate
	l := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	recordThenFlip(t, l, "sess-a")

	got, err := l.Query(asCaller("sess-a", 9), staleFilterTarget, staleFilterApproach, ScopeSession)
	require.NoError(t, err)
	require.Equal(t, AnswerStale, got.State)
}

// TestReconcile_RebuildsAFilterMissingAStaleRecord: Open's reconcile is what repairs an on-disk
// filter written by an earlier build, which held active records only. The filter here holds no key
// at all, and the log holds one stale record: reconcile must see the missing key and rebuild.
func TestReconcile_RebuildsAFilterMissingAStaleRecord(t *testing.T) {
	root, cfg := newProject(t)
	first := openLedger(t, root, cfg, nil, testDeps("", newMetrics()))
	recordThenFlip(t, first, "sess-a")
	require.NoError(t, first.Close())

	// Adopt an EMPTY filter, which is exactly what an active-only rebuild of this log produced.
	second := openLedger(t, root, cfg, first.newConfiguredBloom(), testDeps("", newMetrics()))
	require.True(t, second.bloom.Test(second.recs[0].Desc.MatchKey()), "reconcile added the stale record's key")
}
