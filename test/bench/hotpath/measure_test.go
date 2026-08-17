package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
)

// TestBuildNotes_WarmUpProportionIsComputedNotAsserted is the fix round 3, R2-2 regression: the
// warm-up disclosure note must print the ACTUAL computed warmHotTranche/(iterations+
// warmHotTranche) proportion for whatever iterations value this run used, never the unconditional
// "stays hook-spawn-dominated" claim a small --iterations run could falsify.
func TestBuildNotes_WarmUpProportionIsComputedNotAsserted(t *testing.T) {
	snap := daemon.StatusSnapshot{}

	cases := []struct {
		iterations int
		wantPctStr string // the exact "%.1f%%" this iterations value must produce
	}{
		{2000, fmt.Sprintf("%.1f%%", 100*float64(warmHotTranche)/float64(2000+warmHotTranche))},
		// A small iterations value makes the warm-up tranche a LARGE share of the gated
		// population — the exact case the old unconditional sentence could not honestly cover.
		{10, fmt.Sprintf("%.1f%%", 100*float64(warmHotTranche)/float64(10+warmHotTranche))},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("iterations=%d", c.iterations), func(t *testing.T) {
			notes := buildNotes(snap, true, c.iterations)
			var warmupNote string
			for _, n := range notes {
				if strings.Contains(n, "warm-up hot-path tranche") {
					warmupNote = n
				}
			}
			require.NotEmpty(t, warmupNote, "expected a warm-up composition note when warmDaemonRan is true")
			require.NotContains(t, warmupNote, "stays hook-spawn-dominated",
				"the unconditional dominance claim must be replaced by a computed proportion")
			require.Contains(t, warmupNote, c.wantPctStr,
				"the note must print the proportion actually computed for THIS run's iterations value")
		})
	}
}

// TestBuildNotes_NoWarmUpOmitsTheProportionNote pins the unchanged negative case: no warm-up run,
// no composition note to compute a proportion for.
func TestBuildNotes_NoWarmUpOmitsTheProportionNote(t *testing.T) {
	notes := buildNotes(daemon.StatusSnapshot{}, false, 2000)
	for _, n := range notes {
		require.NotContains(t, n, "warm-up hot-path tranche")
	}
}
