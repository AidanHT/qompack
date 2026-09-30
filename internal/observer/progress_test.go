package observer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// TestProgress_ReportsTheSessionsLivePosition: Progress is exactly the in-memory state the next
// event will be filed from — the turn, the segment events are enrolled into, and that segment's
// running token count — which is what the daemon's MCP handlers place their own records at.
func TestProgress_ReportsTheSessionsLivePosition(t *testing.T) {
	h, ss := newSessionHarness(t)
	ss.DefaultTokens = 40 // every captured read costs this much, so the segment is not empty
	ctx := context.Background()

	_, err := h.obs.OnSessionStart(ctx, startEvent("startup"))
	require.NoError(t, err)
	h.drive(readOf("toolu_progress_1", "src/a.ts", "export const a = 1;\n"))
	h.drive(readOf("toolu_progress_2", "src/b.ts", "export const b = 2;\n"))

	var pr ProgressReporter = h.obs
	got, ok := pr.Progress(testSession)
	require.True(t, ok)

	st := h.state(testSession)
	st.mu.Lock()
	want := Progress{
		Turn: st.Turn, Segment: st.Segment, SegmentTokens: core.Tokens(st.PrefixTokens - st.SegStartPos),
	}
	st.mu.Unlock()
	require.Equal(t, want, got)
	require.NotZero(t, got.Segment, "SessionStart opened the segment events are enrolled into")
	require.Positive(t, got.SegmentTokens, "two captured reads are not an empty segment")
}

// TestProgress_UnknownSessionIsNotCreated: asking about a session the observer never saw answers
// false and leaves no state behind for it — a status read must not register a session.
func TestProgress_UnknownSessionIsNotCreated(t *testing.T) {
	h, _ := newSessionHarness(t)
	var pr ProgressReporter = h.obs
	_, ok := pr.Progress("sess_never_seen")
	require.False(t, ok)

	h.obs.mu.Lock()
	_, created := h.obs.sess["sess_never_seen"]
	h.obs.mu.Unlock()
	require.False(t, created, "Progress must not create state for a session it was asked about")
}
