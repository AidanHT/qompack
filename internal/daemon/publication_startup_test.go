package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The startup publication accounting on a healthy store of the size the candidate 4 live re-run
// met (V6 close-out D49, F2 of plans/sdd/V6-closeout/live/rerun-c4/UAT-12/): three sessions, 70
// capture sidecars and about 380 objects. Every daemon start there logged LOUD "publication
// accounting incomplete; a zero gap count is only a lower bound ... scan interrupted before it
// finished", because the pass was cut at publicationStartupBound (250 ms) before it had walked the
// tree. A healthy store never gets that line: the scan finishes, and LOUD is written only for a real
// gap or a scan that cannot finish.

// liveRunCaptures and liveRunObjects are the store size UAT-12's LOUD lines report
// (captures_scanned=70, objects_scanned=380).
const (
	liveRunCaptures = 70
	liveRunObjects  = 380
)

// seedLiveRunSizedStore fills the audit daemon's store with liveRunObjects indexed objects and
// liveRunCaptures published capture sidecars: a healthy store, every capture joined to its record.
func seedLiveRunSizedStore(t *testing.T, d *daemon, root string) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < liveRunObjects; i++ {
		_, err := d.svc.Store.PutBytes(ctx, []byte(fmt.Sprintf("captured tool result number %d of the live run", i)),
			store.PutOptions{Tool: "Bash"})
		require.NoError(t, err)
	}
	for i := 0; i < liveRunCaptures; i++ {
		id := core.ObservationID(core.HashBytes("daemon.audit.obs", []byte(fmt.Sprintf("live-%d", i))).String())
		require.NoError(t, store.WriteCaptureSidecar(root, store.CaptureSidecar{
			ObservationID: id, Session: "sess", Op: "observe.tool", Published: true,
			Outcome: core.OutcomeOK, Bytes: []byte("captured tool result"),
		}))
	}
}

// TestStartupPublicationAccounting_HealthyLiveRunSizedStoreIsNeverLoud measures one full pass over a
// store of the live run's size, then runs the startup accounting exactly as Run does and requires it
// to finish clean with no LOUD line, however long the pass takes on this machine.
func TestStartupPublicationAccounting_HealthyLiveRunSizedStoreIsNeverLoud(t *testing.T) {
	d, root, mp := newAuditDaemon(t)
	m := *mp
	seedLiveRunSizedStore(t, d, root)

	auditor, ok := d.svc.Store.(store.PublicationAuditor)
	require.True(t, ok)
	start := time.Now()
	full, err := auditor.AuditPublication(context.Background(), store.DefaultPublicationScanCap())
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.False(t, full.Incomplete, "an unbounded pass over a healthy store is complete: %v", full.Notes)
	require.Equal(t, liveRunCaptures, full.CapturesScanned)
	require.GreaterOrEqual(t, full.ObjectsScanned, liveRunObjects)
	t.Logf("one full accounting pass over %d captures and %d objects took %v (startup bound %v)",
		full.CapturesScanned, full.ObjectsScanned, elapsed, publicationStartupBound)

	var loud loudCapture
	loud.attach(t)

	d.accountPublicationAtStartup(context.Background())
	d.runWG.Wait() // the accounting may finish after Run's startup; Stop joins it the same way

	require.False(t, loud.contains("publication accounting"),
		"a healthy store must never be announced as incompletely accounted")
	require.False(t, loud.contains("unpublished captures"), "a healthy store has no gap to announce")
	require.Zero(t, m.Counter(counterPublicationIncomplete).Value(),
		"the accounting over a healthy store must finish")
}

// TestStartupPublicationAccounting_FinishesPastTheBoundInTheBackground makes the startup bound
// shorter than any pass can be, so the background pass is taken on every machine: it must finish
// clean, be counted as continued, and put nothing on LOUD.log.
func TestStartupPublicationAccounting_FinishesPastTheBoundInTheBackground(t *testing.T) {
	d, root, mp := newAuditDaemon(t)
	m := *mp
	d.publicationBound = time.Nanosecond
	seedLiveRunSizedStore(t, d, root)

	var loud loudCapture
	loud.attach(t)

	d.accountPublicationAtStartup(context.Background())
	d.runWG.Wait()

	require.EqualValues(t, 1, m.Counter(counterPublicationContinued).Value(),
		"a pass cut by the startup bound must finish in the background")
	require.Zero(t, m.Counter(counterPublicationIncomplete).Value(), "the background pass finishes")
	require.False(t, loud.contains("publication accounting"))
}

// TestStartupPublicationAccounting_BackgroundPassAnnouncesARealGap: finishing in the background must
// not cost the announcement its purpose. A stage-one capture left before the start is still LOUD.
func TestStartupPublicationAccounting_BackgroundPassAnnouncesARealGap(t *testing.T) {
	d, root, mp := newAuditDaemon(t)
	m := *mp
	d.publicationBound = time.Nanosecond
	id := core.ObservationID(core.HashBytes("daemon.audit.obs", []byte("stage-one")).String())
	require.NoError(t, store.WriteCaptureSidecar(root, store.CaptureSidecar{
		ObservationID: id, Session: "sess", Op: "observe.tool", Published: false,
		Outcome: core.OutcomeOK, Bytes: []byte("captured tool result"),
	}))
	backdateCaptures(t, root)

	var loud loudCapture
	loud.attach(t)

	d.accountPublicationAtStartup(context.Background())
	d.runWG.Wait()

	require.True(t, loud.contains("unpublished captures or unindexed objects"),
		"a gap the startup store holds is announced however the pass finished")
	require.EqualValues(t, 1, m.Counter(counterPublicationUnpublishedCaptures).Value())
}

// TestStartupPublicationAccounting_StoppedBackgroundPassIsNotLoud: a background pass the daemon's
// own stop cuts short was told to stop; it could have finished, so it is no operator's alarm.
func TestStartupPublicationAccounting_StoppedBackgroundPassIsNotLoud(t *testing.T) {
	d, root, mp := newAuditDaemon(t)
	m := *mp
	d.publicationBound = time.Nanosecond
	seedLiveRunSizedStore(t, d, root)

	var loud loudCapture
	loud.attach(t)

	runCtx, cancel := context.WithCancel(context.Background())
	d.accountPublicationAtStartup(runCtx)
	cancel()
	d.runWG.Wait()

	require.EqualValues(t, 1, m.Counter(counterPublicationContinued).Value())
	require.False(t, loud.contains("publication accounting"),
		"a pass the daemon's stop ended is not a scan that cannot finish")
}

// backdateCaptures sets every capture sidecar's modification time an hour into the past, so a
// sidecar a test seeds is unambiguously older than the snapshot the startup accounting takes after
// it, whatever the file system's timestamp granularity.
func backdateCaptures(t *testing.T, root string) {
	t.Helper()
	past := time.Now().Add(-time.Hour)
	dir := filepath.Join(paths.Of(root).Records, "captures")
	require.NoError(t, filepath.WalkDir(paths.Long(dir), func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		return os.Chtimes(p, past, past)
	}))
}
