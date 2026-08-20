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
			notes := buildNotes(snap, true, c.iterations, deliveryLedger{})
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
	notes := buildNotes(daemon.StatusSnapshot{}, false, 2000, deliveryLedger{})
	for _, n := range notes {
		require.NotContains(t, n, "warm-up hot-path tranche")
	}
}

// TestBuildNotes_CleanRunSaysNothingAboutDelivery pins that a run which delivered every request
// adds no delivery note at all — the artifact of a healthy run is unchanged by this accounting.
func TestBuildNotes_CleanRunSaysNothingAboutDelivery(t *testing.T) {
	clean := deliveryLedger{Sent: 2064, Delivered: 2064}
	notes := buildNotes(daemon.StatusSnapshot{}, false, 2000, clean, "", "")
	for _, n := range notes {
		require.NotContains(t, n, "delivery ledger")
	}
}

// TestBuildNotes_DeferralIsDisclosed pins the opposite: a run that deferred anything says so, in
// the artifact, with the counts spelled out — and carries each gated row's own disclosure through.
func TestBuildNotes_DeferralIsDisclosed(t *testing.T) {
	ledger := deliveryLedger{Sent: 2064, Delivered: 2063, Deferred: 1}
	notes := buildNotes(daemon.StatusSnapshot{}, false, 2000, ledger, "B-A row note", "")

	joined := strings.Join(notes, "\n")
	require.Contains(t, joined, "delivery ledger: 2064 hot-path requests sent, 2063 delivered live")
	require.Contains(t, joined, "1 DEFERRED to the client spool and 0 lost")
	require.Contains(t, joined, "B-A row note")
	require.NotContains(t, joined, "\n\n", "an empty row note must be skipped, not emitted as a blank note")
}
