package observer

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// feed pushes n identical events into the feature window.
func (h *harness) feed(st *sessionState, n int, tool string, paths []string, body string) {
	for range n {
		h.obs.recordRecent(st, tool, paths, []byte(body), h.obs.now())
	}
}

func TestFeatures_NotReadyBeforeTwoWindows(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, 2*featureWindow-1, "FileRead", []string{"a.ts"}, "alpha")

	_, ok := h.obs.features(st, h.obs.now())
	require.False(t, ok, "BOCD needs two full windows before it has anything to compare")
}

func TestFeatures_PathJaccardIdenticalWindows(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, 2*featureWindow, "FileRead", []string{"a.ts"}, "alpha")

	f, ok := h.obs.features(st, h.obs.now())
	require.True(t, ok)
	require.InDelta(t, 1.0, f.PathJaccard, 1e-9)
	require.Equal(t, st.Turn, f.Turn)
	require.Equal(t, h.obs.now(), f.TS)
}

func TestFeatures_PathJaccardDisjointWindows(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, featureWindow, "FileRead", []string{"a.ts"}, "alpha")
	h.feed(st, featureWindow, "FileRead", []string{"b.ts"}, "alpha")

	f, ok := h.obs.features(st, h.obs.now())
	require.True(t, ok)
	require.InDelta(t, 0.0, f.PathJaccard, 1e-9)
}

func TestFeatures_PathJaccardBothEmpty(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, 2*featureWindow, "Bash", nil, "ok")

	f, ok := h.obs.features(st, h.obs.now())
	require.True(t, ok)
	require.InDelta(t, 1.0, f.PathJaccard, 1e-9, "two path-free windows are no locality shift at all")
}

func TestFeatures_ToolShiftIdentical(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, 2*featureWindow, "FileRead", []string{"a.ts"}, "alpha")

	f, ok := h.obs.features(st, h.obs.now())
	require.True(t, ok)
	require.InDelta(t, 0.0, f.ToolShift, 1e-9)
}

func TestFeatures_ToolShiftComplete(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, featureWindow, "FileRead", []string{"a.ts"}, "alpha")
	h.feed(st, featureWindow, "Bash", nil, "alpha")

	f, ok := h.obs.features(st, h.obs.now())
	require.True(t, ok)
	require.InDelta(t, 1.0, f.ToolShift, 1e-9, "total variation between disjoint distributions is 1")
}

func TestFeatures_ToolShiftHalf(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, featureWindow, "FileRead", []string{"a.ts"}, "alpha")
	h.feed(st, featureWindow/2, "FileRead", []string{"a.ts"}, "alpha")
	h.feed(st, featureWindow/2, "Bash", nil, "alpha")

	f, ok := h.obs.features(st, h.obs.now())
	require.True(t, ok)
	require.InDelta(t, 0.5, f.ToolShift, 1e-9)
}

func TestFeatures_LexicalCohesionIdenticalText(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, 2*featureWindow, "FileRead", []string{"a.ts"}, "refresh token parse jwt")

	f, ok := h.obs.features(st, h.obs.now())
	require.True(t, ok)
	require.InDelta(t, 1.0, f.LexicalCohesion, 1e-9)
}

func TestFeatures_LexicalCohesionDisjointVocabulary(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, featureWindow, "FileRead", []string{"a.ts"}, "alpha beta")
	h.feed(st, featureWindow, "FileRead", []string{"a.ts"}, "gamma delta")

	f, ok := h.obs.features(st, h.obs.now())
	require.True(t, ok)
	require.InDelta(t, 0.0, f.LexicalCohesion, 1e-9)
}

func TestFeatures_GapSecondsFromFakeClock(t *testing.T) {
	h := newHarness(t)

	for i := range 2*featureWindow - 1 {
		h.drive(readOf(fmt.Sprintf("toolu_%d", i), "a.ts", "alpha\n"))
	}
	h.Clock.Advance(45 * time.Second)
	h.drive(readOf("toolu_last", "a.ts", "alpha\n"))

	got := h.features()
	require.Len(t, got, 1)
	require.InDelta(t, 45.0, got[0].Sample.GapSeconds, 1e-9)
}

func TestFeatures_TodoTransition(t *testing.T) {
	h := newHarness(t)

	for i := range 2*featureWindow - 1 {
		h.drive(readOf(fmt.Sprintf("toolu_%d", i), "a.ts", "alpha\n"))
	}
	h.drive(toolUse("toolu_todo", "TodoWrite",
		`{"todos":[{"content":"trace the timeout","status":"completed"}]}`,
		`{"content":"ok"}`))

	got := h.features()
	require.Len(t, got, 1)
	require.InDelta(t, 1.0, got[0].Sample.TodoTransition, 1e-9)
}

func TestFeatures_AllFinite(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		h := newHarness(t)
		st := h.state(testSession)

		tools := []string{"FileRead", "FileEdit", "Bash", "Grep", "TodoWrite"}
		words := []string{"", "alpha", "alpha beta", "gamma", "delta epsilon zeta", "α β"}
		files := [][]string{nil, {"a.ts"}, {"b.ts"}, {"a.ts", "b.ts"}, {}}

		n := rapid.IntRange(2*featureWindow, 4*featureWindow).Draw(rt, "events")
		for range n {
			h.obs.recordRecent(st,
				rapid.SampledFrom(tools).Draw(rt, "tool"),
				rapid.SampledFrom(files).Draw(rt, "paths"),
				[]byte(rapid.SampledFrom(words).Draw(rt, "text")),
				h.obs.now())
			h.Clock.Advance(time.Duration(rapid.IntRange(0, 5000).Draw(rt, "ms")) * time.Millisecond)
		}
		st.LastTS = h.obs.now()
		h.Clock.Advance(time.Second)

		f, ok := h.obs.features(st, h.obs.now())
		require.True(rt, ok)
		for name, v := range map[string]float64{
			"PathJaccard":     f.PathJaccard,
			"ToolShift":       f.ToolShift,
			"LexicalCohesion": f.LexicalCohesion,
			"TodoTransition":  f.TodoTransition,
		} {
			require.False(rt, math.IsNaN(v) || math.IsInf(v, 0), "%s is not finite: %v", name, v)
			require.GreaterOrEqual(rt, v, 0.0, name)
			require.LessOrEqual(rt, v, 1.0, name)
		}
		require.False(rt, math.IsNaN(f.GapSeconds) || math.IsInf(f.GapSeconds, 0))
		require.GreaterOrEqual(rt, f.GapSeconds, 0.0)
	})
}

func TestFeatures_RecentRingBounded(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.feed(st, 1000, "FileRead", []string{"a.ts"}, "alpha")

	require.Len(t, st.Recent, 2*featureWindow, "the window is a ring, not a transcript")
}

func TestFeatures_RecentTextIsBounded(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	h.obs.recordRecent(st, "FileRead", []string{"a.ts"},
		make([]byte, cohesionTokenCap*bytesPerCohesionToken*4), h.obs.now())

	require.Len(t, st.Recent, 1)
	require.LessOrEqual(t, len(st.Recent[0].Text), cohesionTokenCap*bytesPerCohesionToken,
		"a feature window may not pin an unbounded amount of a session's bytes")
}

func TestNewlyCompletedTodos_RecordsEveryCompletedItem(t *testing.T) {
	h := newHarness(t)
	st := h.state(testSession)

	first := toolUse("toolu_1", "TodoWrite",
		`{"todos":[{"content":"one","status":"completed"},{"content":"two","status":"in_progress"}]}`,
		`{"content":"ok"}`)
	require.True(t, h.obs.newlyCompletedTodos(st, first))
	require.False(t, h.obs.newlyCompletedTodos(st, first), "the same list closes nothing new")

	second := toolUse("toolu_2", "TodoWrite",
		`{"todos":[{"content":"one","status":"completed"},{"content":"two","status":"completed"}]}`,
		`{"content":"ok"}`)
	require.True(t, h.obs.newlyCompletedTodos(st, second), "a second item completing IS a transition")
	require.Equal(t, map[string]bool{"one": true, "two": true}, st.TodoDone)
}

func TestJaccard_EdgeCases(t *testing.T) {
	set := func(keys ...string) map[string]struct{} {
		m := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			m[k] = struct{}{}
		}
		return m
	}

	require.InDelta(t, 1.0, jaccard(set(), set()), 1e-9, "both empty is no shift")
	require.InDelta(t, 0.0, jaccard(set("a"), set()), 1e-9, "one empty is a complete shift")
	require.InDelta(t, 0.0, jaccard(set(), set("a")), 1e-9)
	require.InDelta(t, 1.0/3.0, jaccard(set("a", "b"), set("b", "c")), 1e-9)
}

func TestTotalVariation_EdgeCases(t *testing.T) {
	require.InDelta(t, 0.0, totalVariation(nil, nil), 1e-9)
	require.InDelta(t, 1.0, totalVariation(map[string]float64{"a": 1}, map[string]float64{"b": 1}), 1e-9)
	require.InDelta(t, 0.0, totalVariation(map[string]float64{"a": 1}, map[string]float64{"a": 1}), 1e-9)
}

func TestCosineTokens_EdgeCases(t *testing.T) {
	require.InDelta(t, 0.0, cosineTokens(nil, []byte("alpha")), 1e-9, "an empty side scores zero")
	require.InDelta(t, 0.0, cosineTokens([]byte("12345 !!"), []byte("alpha")), 1e-9, "no tokens at all")
	require.InDelta(t, 1.0, cosineTokens([]byte("Alpha beta"), []byte("alpha BETA")), 1e-9, "tokens are lowercased")
}
