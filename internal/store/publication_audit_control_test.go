package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// Builds before the V6 close-out published a capture sidecar for every leased CONTROL line a drain
// replayed — a session start, checkpoint or SessionEnd flush whose hook had fallen back to its client
// spool — although a control line is not an observation and nothing ever references its sidecar.
// Those files are evidence and stay on disk. The accounting must classify them as the known legacy
// artifact they are: not a publication gap, not damage, and not a reason to call every later pass
// incomplete, which is what "capture sidecar with an unrecognized op" did for the project's lifetime.

// TestAuditPublication_LegacyControlCaptureIsKnownNotIncomplete: a control-op sidecar written the way
// the old drain wrote it (outcome ok, durable bytes, never published) is counted as a legacy control
// capture, and the pass stays complete. It excuses nothing else: a real tool gap beside it is still a
// gap, and a genuinely unknown op beside it still makes the pass incomplete.
func TestAuditPublication_LegacyControlCaptureIsKnownNotIncomplete(t *testing.T) {
	body := []byte(`{"hook_event_name":"SessionEnd"}`)

	t.Run("alone", func(t *testing.T) {
		tp := newTestStore(t)
		seedCapture(t, tp.Root, "flush", "flush", false, core.OutcomeOK, body)
		seedCapture(t, tp.Root, "checkpoint", "checkpoint", false, core.OutcomeOK, body)
		seedCapture(t, tp.Root, "start", "session.start", false, core.OutcomeOK, body)

		a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
		require.NoError(t, err)
		require.Equal(t, 3, a.CapturesScanned)
		require.Equal(t, 3, a.LegacyControlCaptures, "each control-op sidecar is counted as the legacy artifact it is")
		require.Zero(t, a.UnpublishedCaptures, "a control line needs no reference, so it is never a gap")
		require.False(t, a.HasGaps())
		require.False(t, a.Incomplete, "a known legacy artifact does not make the accounting incomplete: %v", a.Notes)
		require.Empty(t, a.Notes)
	})

	t.Run("beside a real gap and an unknown op", func(t *testing.T) {
		tp := newTestStore(t)
		seedCapture(t, tp.Root, "flush", "flush", false, core.OutcomeOK, body)
		seedCapture(t, tp.Root, "tool-gap", auditOpObserveTool, false, core.OutcomeOK, []byte("captured tool result"))
		seedCapture(t, tp.Root, "martian", "observe.martian", false, core.OutcomeOK, body)

		a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
		require.NoError(t, err)
		require.Equal(t, 1, a.LegacyControlCaptures)
		require.Equal(t, 1, a.UnpublishedCaptures, "the tool gap beside it is still a gap")
		require.True(t, a.Incomplete, "an unknown op beside it still makes the pass incomplete")
		require.Contains(t, a.Notes, "capture sidecar with an unrecognized op")
	})
}

// TestCaptureRequiresReference_ControlOpsAreKnownToNeedNone: the publication requirement of a control
// line is known — none — which is what lets fsck's captures row stop calling it unknown.
func TestCaptureRequiresReference_ControlOpsAreKnownToNeedNone(t *testing.T) {
	for _, op := range []string{"flush", "checkpoint", "session.start"} {
		require.True(t, IsControlCaptureOp(op), op)
		required, known := CaptureRequiresReference(op, nil)
		require.True(t, known, "%s: a control line's requirement is known", op)
		require.False(t, required, "%s: a control line needs no reference", op)
	}
	for _, op := range []string{auditOpObserveTool, auditOpObservePrompt, auditOpObserveStop, "observe.martian", "", "status"} {
		require.False(t, IsControlCaptureOp(op), op)
	}
}
