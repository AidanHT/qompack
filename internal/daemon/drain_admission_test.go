package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// The drain's two admission-refusal branches — DrainGapUnadmitted and DrainGapDenied — had no test
// of any kind before this file, on the newest and least-exercised code of the crash-recovery path.

// writeSpoolLines writes several requests into ONE spool file, in order, which is what a session's
// worth of hooks leaves behind while the daemon is down. delivery_publication_test.go's
// single-line helper cannot express "the record BEHIND the poison one", which is the whole subject
// of these tests.
func writeSpoolLines(t *testing.T, root, name string, reqs ...ipc.Request) {
	t.Helper()
	var buf []byte
	for _, req := range reqs {
		line, err := ipc.EncodeRequest(req)
		require.NoError(t, err)
		buf = append(append(buf, line...), '\n')
	}
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, name)), buf, 0o600))
}

// undecidableRequest is a spooled record no policy can ever decide: its capture error names this
// process's inability to classify anything rather than any fact about the delivery.
func undecidableRequest(nonce, session string) ipc.Request {
	req := observeRequest(nonce, session, "")
	req.Event = nil
	req.Capture.Outcome, req.Capture.Fidelity = core.OutcomeUnavailable, core.FidelityUnknown
	req.Capture.CaptureError, req.Capture.Bytes = core.CaptureErrorPolicy, nil
	return req
}

func gapOfKind(t *testing.T, st DrainGapState, kind DrainGapKind) DrainGap {
	t.Helper()
	for _, g := range st.Gaps {
		if g.Kind == kind {
			return g
		}
	}
	t.Fatalf("no %s gap in %+v", kind, st.Gaps)
	return DrainGap{}
}

// TestDrainSkipsAnUnadmittableRecordAndDeliversWhatFollows is the wedge-then-recover case.
//
// One hook fires while the daemon is down with a payload no policy can decide, and it is spooled
// between two ordinary ones. The drain used to record a DrainGapUnadmitted, set readErr and break
// out of the read loop with the offset UNADVANCED, then return before saveState. The admission
// decision is baked into the spooled record, so no later pass could ever decide it differently:
// every startup and every idle tick re-read the same offset, hit the same verdict and abandoned
// the file again. The third record was never delivered for the life of the project, and the first
// was re-dispatched on every pass.
//
// The requirement is that losing one unadmittable record is LOUD and bounded: the gap is reported,
// the offset advances past it, the progress is persisted, and everything behind it is delivered.
func TestDrainSkipsAnUnadmittableRecordAndDeliversWhatFollows(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	require.NoError(t, dd.ing.Close())

	before := observeRequest(testDeliveryToken('9'), "sess-wedge", `{"hook_event_name":"PostToolUse"}`)
	poison := undecidableRequest(testDeliveryToken('b'), "sess-wedge")
	after := observeRequest(testDeliveryToken('c'), "sess-wedge", `{"hook_event_name":"PostToolUse"}`)
	writeSpoolLines(t, root, "client-00007.ndjson", before, poison, after)

	n, err := dd.Drain(context.Background())
	require.NoError(t, err, "one unadmittable record is a gap, not a failed replay")
	require.Equal(t, 2, n, "both admissible records are delivered")
	require.Equal(t, 2, calls())

	gaps := dd.DrainGaps()
	require.True(t, gaps.Observed)
	require.False(t, gaps.Complete, "a skipped record is a hole in the record and must be reported")
	g := gapOfKind(t, gaps, DrainGapUnadmitted)
	require.Equal(t, 1, g.Count)
	require.Equal(t, string(core.CaptureErrorPolicy), g.Reason, "the closed label, never payload text")

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.True(t, journal.acknowledged(before.Nonce))
	require.True(t, journal.acknowledged(after.Nonce), "the record BEHIND the poison one is published")
	require.False(t, journal.acknowledged(poison.Nonce), "and the skipped one is not")

	// Recover: the offset advanced past the poison record and the progress was persisted, so a
	// second pass finds the file consumed rather than replaying it from the same wedge.
	again, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, again, "a consumed spool file is not re-dispatched")
	require.Equal(t, 2, calls(), "and nothing is delivered twice")
	entries, _ := os.ReadDir(paths.Long(paths.Of(root).Spool))
	require.Empty(t, entries, "a fully consumed client spool file is removed")
}

// TestDrainReportsADeniedRecordAsAGapAndMovesOn is DrainGapDenied's first test. A denial is a
// decision — retrying produces the same answer — so the record is released and the replay
// continues, but the hole is still reported so a caller can never read "nothing was recorded" as
// coverage.
func TestDrainReportsADeniedRecordAsAGapAndMovesOn(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	require.NoError(t, dd.ing.Close())

	denied := observeRequest(testDeliveryToken('d'), "sess-denied-drain", "")
	denied.Event = nil
	denied.Capture.Outcome, denied.Capture.Fidelity = core.OutcomeDenied, core.FidelityUnknown
	denied.Capture.Bytes = nil
	after := observeRequest(testDeliveryToken('f'), "sess-denied-drain", `{"hook_event_name":"PostToolUse"}`)
	writeSpoolLines(t, root, "client-00008.ndjson", denied, after)

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, calls(), "the denied record reaches no handler")
	require.Len(t, sidecarFiles(t, root), 1, "and leaves no capture of its own beside the admitted one")

	gaps := dd.DrainGaps()
	require.False(t, gaps.Complete)
	require.Equal(t, 1, gapOfKind(t, gaps, DrainGapDenied).Count)

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.False(t, journal.acknowledged(denied.Nonce), "a denial publishes nothing")
	require.True(t, journal.acknowledged(after.Nonce), "and does not hold up what follows it")
}

// TestDrainPersistsADegradedCaptureAsEvidence is the spool half of the same requirement
// TestDispatchOpPersistsADegradedCaptureAsEvidence pins on the live path: an over-budget record
// spooled while the daemon was down is not a gap, it is evidence, and the drain publishes its
// sidecar rather than treating the decision as an absence.
func TestDrainPersistsADegradedCaptureAsEvidence(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	require.NoError(t, dd.ing.Close())

	req := observeRequest(testDeliveryToken('6'), "sess-spool-oversize", `{"hook_ev`)
	req.Event = nil
	req.Capture.Outcome, req.Capture.Fidelity = core.OutcomeUnavailable, core.FidelityTruncated
	req.Capture.CaptureError, req.Capture.Truncated = core.CaptureErrorOversize, true
	req.Capture.SourceBytes = 4 << 20
	req.Capture.Bytes = nil // opaque prefixes cannot prove file scope; retain classification only
	writeSpoolLines(t, root, "client-00009.ndjson", req)

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 0, calls(), "no Event was derived, so none is invented")

	sc := readOnlySidecar(t, root)
	require.Equal(t, core.OutcomeUnavailable, sc.Outcome)
	require.Equal(t, core.CaptureErrorOversize, sc.CaptureError)
	require.Equal(t, 4<<20, sc.SourceBytes)
	require.Empty(t, sc.Bytes)

	gaps := dd.DrainGaps()
	require.True(t, gaps.Complete, "an admitted degraded record is not a hole: %+v", gaps.Gaps)
}
