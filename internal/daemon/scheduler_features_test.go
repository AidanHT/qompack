package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// feedPaths appends one observation carrying only paths.
func feedPaths(h *FeatureHistory, ts core.UnixMilli, ps ...string) {
	FeaturesFrom(h, observer.Signals{Paths: ps}, "Read", ts)
}

// feedTools appends one observation per tool name, in order, with no paths.
func feedTools(h *FeatureHistory, ts core.UnixMilli, tools ...string) {
	for i, tool := range tools {
		FeaturesFrom(h, observer.Signals{}, tool, ts+core.UnixMilli(i))
	}
}

// TestFeaturesFrom_PathJaccard: prior window paths {a,b,c}, recent {b,c,d} ⇒ |{b,c}| / |{a,b,c,d}|.
func TestFeaturesFrom_PathJaccard(t *testing.T) {
	t.Parallel()

	h := NewFeatureHistory(3)
	feedPaths(h, 1_000, "a")
	feedPaths(h, 2_000, "b")
	feedPaths(h, 3_000, "c")
	feedPaths(h, 4_000, "b")
	feedPaths(h, 5_000, "c")
	f := FeaturesFrom(h, observer.Signals{Paths: []string{"d"}}, "Read", 6_000)
	require.InDelta(t, 0.5, f.PathJaccard, 1e-12)

	// Raw paths are normalized with paths.Key before membership, so two spellings the platform
	// folds together count once. On a non-folding platform the union stays four and the
	// expectation below follows paths.Key rather than assuming a platform.
	h2 := NewFeatureHistory(1)
	feedPaths(h2, 1_000, "Src/Auth.go", "b")
	f2 := FeaturesFrom(h2, observer.Signals{Paths: []string{"src/auth.go", "b"}}, "Read", 2_000)
	want := 1.0
	if paths.Key("Src/Auth.go") != paths.Key("src/auth.go") {
		want = 1.0 / 3.0 // {b} over {Src/Auth.go, src/auth.go, b}
	}
	require.InDelta(t, want, f2.PathJaccard, 1e-12)
}

// TestFeaturesFrom_PathJaccard_EmptyUnionIsOne: silence is not evidence of a shift.
func TestFeaturesFrom_PathJaccard_EmptyUnionIsOne(t *testing.T) {
	t.Parallel()

	h := NewFeatureHistory(4)
	f := FeaturesFrom(h, observer.Signals{}, "Bash", 1_000)
	require.Equal(t, 1.0, f.PathJaccard, "first observation, no paths anywhere")
	for i := 2; i <= 8; i++ {
		f = FeaturesFrom(h, observer.Signals{Paths: []string{""}}, "Bash", core.UnixMilli(i)*1_000)
	}
	require.Equal(t, 1.0, f.PathJaccard, "both windows full, no paths anywhere")
}

// TestFeaturesFrom_ToolShiftTotalVariation: total-variation distance between the two windows'
// tool distributions — disjoint 1.0, identical 0.0, half-and-half 0.5, either window empty 0.0.
func TestFeaturesFrom_ToolShiftTotalVariation(t *testing.T) {
	t.Parallel()

	disjoint := NewFeatureHistory(4)
	feedTools(disjoint, 1_000, "read", "read", "read", "read")
	feedTools(disjoint, 2_000, "bash", "bash", "bash")
	f := FeaturesFrom(disjoint, observer.Signals{}, "bash", 3_000)
	require.InDelta(t, 1.0, f.ToolShift, 1e-12)

	same := NewFeatureHistory(4)
	feedTools(same, 1_000, "read", "read", "read", "read")
	feedTools(same, 2_000, "read", "read", "read")
	f = FeaturesFrom(same, observer.Signals{}, "read", 3_000)
	require.InDelta(t, 0.0, f.ToolShift, 1e-12)

	half := NewFeatureHistory(4)
	feedTools(half, 1_000, "read", "read", "read", "read")
	feedTools(half, 2_000, "read", "read", "bash")
	f = FeaturesFrom(half, observer.Signals{}, "bash", 3_000)
	require.InDelta(t, 0.5, f.ToolShift, 1e-12)

	first := NewFeatureHistory(4)
	f = FeaturesFrom(first, observer.Signals{}, "read", 1_000)
	require.Equal(t, 0.0, f.ToolShift, "prior window empty")

	require.GreaterOrEqual(t, f.ToolShift, 0.0)
	require.LessOrEqual(t, f.ToolShift, 1.0)
}

// TestFeaturesFrom_GapSeconds: (ts − lastTS)/1000, clamped at zero, zero on the first
// observation.
func TestFeaturesFrom_GapSeconds(t *testing.T) {
	t.Parallel()

	clk := newFakeClock(epoch)
	h := NewFeatureHistory(0)

	first := FeaturesFrom(h, observer.Signals{}, "Bash", core.NowMilli(clk))
	require.Equal(t, 0.0, first.GapSeconds, "first observation")

	clk.Advance(45 * time.Second)
	f := FeaturesFrom(h, observer.Signals{}, "Bash", core.NowMilli(clk))
	require.InDelta(t, 45.0, f.GapSeconds, 1e-9)

	earlier := core.NowMilli(clk) - 30_000
	f = FeaturesFrom(h, observer.Signals{}, "Bash", earlier)
	require.Equal(t, 0.0, f.GapSeconds, "a timestamp before the previous one clamps to zero")
}

// TestFeaturesFrom_TodoTransition is the G1.5 wiring: any of the three safe-point signals is a
// transition, none is not.
func TestFeaturesFrom_TodoTransition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		sig  observer.Signals
		want float64
	}{
		{"todo", observer.Signals{TodoCompleted: true}, 1},
		{"test", observer.Signals{TestPassed: true}, 1},
		{"commit", observer.Signals{GitCommit: true}, 1},
		{"none", observer.Signals{}, 0},
	}
	for _, tc := range cases {
		h := NewFeatureHistory(2)
		f := FeaturesFrom(h, tc.sig, "Bash", 1_000)
		require.Equal(t, tc.want, f.TodoTransition, tc.name)
	}
}

// TestFeaturesFrom_WindowRoll: with window 8, after 20 observations recent holds turns 13–20
// and prior holds 5–12; after 10 000 more nothing has grown past the window.
func TestFeaturesFrom_WindowRoll(t *testing.T) {
	t.Parallel()

	h := NewFeatureHistory(8)
	observe := func(i int) {
		observeText(h, fmt.Sprintf("preview %d", i))
		FeaturesFrom(h, observer.Signals{Paths: []string{fmt.Sprintf("p%d", i)}}, fmt.Sprintf("t%d", i), core.UnixMilli(i)*1_000)
	}
	for i := 1; i <= 20; i++ {
		observe(i)
	}
	turnsOf := func(w turnWindow) []string { return append([]string(nil), w.tools...) }
	require.Equal(t, []string{"t13", "t14", "t15", "t16", "t17", "t18", "t19", "t20"}, turnsOf(h.recent))
	require.Equal(t, []string{"t5", "t6", "t7", "t8", "t9", "t10", "t11", "t12"}, turnsOf(h.prior))
	require.Equal(t, "preview 13", h.recent.text[0])
	require.Equal(t, "preview 5", h.prior.text[0])
	_, ok := h.recent.paths[7][paths.Key("p20")]
	require.True(t, ok, "the newest observation's path set sits at the end of recent")

	for i := 21; i <= 10_020; i++ {
		observe(i)
	}
	for _, w := range []turnWindow{h.recent, h.prior} {
		require.LessOrEqual(t, len(w.paths), 8)
		require.LessOrEqual(t, len(w.tools), 8)
		require.LessOrEqual(t, len(w.text), 8)
		require.LessOrEqual(t, cap(w.paths), 8, "pre-allocated to window and reused")
		require.LessOrEqual(t, cap(w.tools), 8)
		require.LessOrEqual(t, cap(w.text), 8)
	}
	require.Equal(t, "t10020", h.recent.tools[7])
}

// TestFeaturesFrom_LexicalCohesionShingleCap: a 200 KB preview retains at most maxShingles
// shingles per window, and the whole observation stays well inside 10 ms.
func TestFeaturesFrom_LexicalCohesionShingleCap(t *testing.T) {
	t.Parallel()

	var sb strings.Builder
	for i := 0; sb.Len() < 200*1024; i++ {
		fmt.Fprintf(&sb, "tok%d ", i) // every bigram distinct, so the cap is the only bound
	}
	preview := sb.String()
	require.GreaterOrEqual(t, len(preview), 200*1024)

	list := shingleList(preview, maxShingles)
	require.LessOrEqual(t, len(list), maxShingles)
	require.Equal(t, maxShingles, len(list), "distinct bigrams fill the cap exactly")
	require.Equal(t, "tok0 tok1", list[0])

	// Two full turns in one window still yield at most maxShingles for the window.
	w := newTurnWindow(2)
	w.push(turnObs{shingles: list})
	w.push(turnObs{shingles: shingleList(strings.ReplaceAll(preview, "tok", "alt"), maxShingles)})
	require.Equal(t, maxShingles, len(windowShingles(w, maxShingles)))

	h := NewFeatureHistory(2)
	observeText(h, preview)
	FeaturesFrom(h, observer.Signals{}, "Bash", 1_000)
	observeText(h, preview)
	start := time.Now()
	f := FeaturesFrom(h, observer.Signals{}, "Bash", 2_000)
	best := time.Since(start)
	// Min-of-5 (ruling R45): one sample under -race and co-load flakes; the minimum is the
	// machine's honest cost and still catches a real regression.
	for range 4 {
		observeText(h, preview)
		start = time.Now()
		FeaturesFrom(h, observer.Signals{}, "Bash", 2_000)
		best = min(best, time.Since(start))
	}
	require.Less(t, best, 10*time.Millisecond)
	require.InDelta(t, 1.0, f.LexicalCohesion, 1e-12, "identical capped texts are fully cohesive")

	// The sides of the Jaccard: either side empty is 1.0, disjoint text is 0.0.
	empty := NewFeatureHistory(1)
	FeaturesFrom(empty, observer.Signals{}, "Bash", 1_000)
	observeText(empty, "alpha beta gamma")
	require.Equal(t, 1.0, FeaturesFrom(empty, observer.Signals{}, "Bash", 2_000).LexicalCohesion)
	disjoint := NewFeatureHistory(1)
	observeText(disjoint, "alpha beta gamma")
	FeaturesFrom(disjoint, observer.Signals{}, "Bash", 1_000)
	observeText(disjoint, "delta epsilon zeta")
	require.Equal(t, 0.0, FeaturesFrom(disjoint, observer.Signals{}, "Bash", 2_000).LexicalCohesion)
	overlap := NewFeatureHistory(1)
	observeText(overlap, "Alpha beta gamma")
	FeaturesFrom(overlap, observer.Signals{}, "Bash", 1_000)
	observeText(overlap, "alpha BETA delta")
	// prior shingles {alpha beta, beta gamma}; recent {alpha beta, beta delta} ⇒ 1/3.
	require.InDelta(t, 1.0/3.0, FeaturesFrom(overlap, observer.Signals{}, "Bash", 2_000).LexicalCohesion, 1e-12)
}
