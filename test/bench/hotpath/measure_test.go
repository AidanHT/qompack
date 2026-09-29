package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
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

// TestAckRTTNote_DisclosesTheDiscardedWarmUps is the review-round-2 disclosure regression. The row
// the ACK deadline is sized from now sends warm-up requests it never times, and the artifact has to
// say so — both that they exist and that the daemon saw them — or the row's own n and the daemon's
// histogram counts stop agreeing for a reader who only has the artifact.
func TestAckRTTNote_DisclosesTheDiscardedWarmUps(t *testing.T) {
	note := ackRTTNote()
	require.Contains(t, note, fmt.Sprintf("%d DISCARDED warm-up requests", ackRTTWarmups),
		"the note must say how many requests are sent and not timed")
	require.Contains(t, note, fmt.Sprintf("all %d requests", ackRTTSamples+ackRTTWarmups),
		"and that the daemon's histograms saw every one of them, not just the timed ones")
	require.Contains(t, note, "BEFORE any of the tranche is sent, warm-ups included",
		"the round-1 ordering claim must cover the warm-ups too")
}

// recordingClient is an ipc.Client that sends nothing and remembers everything it was given. It
// answers the way internal/ipc's own Client contract says a client answers — (Response{OK:true},
// nil), never a propagating error — so the code under test takes exactly the path a real run takes.
type recordingClient struct {
	sent   []ipc.Request
	closed int
}

func (c *recordingClient) Send(_ context.Context, req ipc.Request, _ time.Duration) (ipc.Response, error) {
	c.sent = append(c.sent, req)
	return ipc.Response{OK: true}, nil
}

func (c *recordingClient) Close() error {
	c.closed++
	return nil
}

// TestAckRTTTranche_SendsTheWarmUpsAndTimesOnlyTheSamples is the review-round-1 regression for the
// half of the warm-up fix that is a SEND rather than a number: the warm-up requests must actually go
// out. ackRTTTrancheSends already tells the ledger there are samples+ackRTTWarmups of them, and a
// warm-up loop that built its request and dropped it would leave every test in this package green
// while the run itself over-counted Sent by ackRTTWarmups and failed reconciliation.
//
// Over a recording client it pins, in order: ackRTTWarmups + n requests sent for n timed samples;
// the ledger's own count equal to that; exactly n durations returned, so only the samples are timed;
// every request a fresh-nonce observe.tool on the row's own session; and each request's sequence,
// which is what puts the warm-ups FIRST and keeps them out of the timed set — ackRTTRequest gives a
// warm-up the negative sequence -1-i and sample i the sequence i.
func TestAckRTTTranche_SendsTheWarmUpsAndTimesOnlyTheSamples(t *testing.T) {
	const samples = 3
	require.Positive(t, ackRTTWarmups, "the row's first-sample bias fix must not be silently disabled")

	c := &recordingClient{}
	out, err := ackRTTTranche(context.Background(), c, "/bench/project", samples)
	require.NoError(t, err)
	require.Len(t, out, samples, "only the timed samples come back")
	require.Len(t, c.sent, samples+ackRTTWarmups, "the warm-ups are SENT, not merely counted")
	require.Equal(t, int64(len(c.sent)), ackRTTTrancheSends(samples),
		"and the ledger is told exactly the number that went out")

	nonces := map[string]bool{}
	for i, req := range c.sent {
		require.Equal(t, ipc.OpObserveTool, req.Op, "request %d", i)
		require.Equal(t, ackRTTSessionID, req.Session, "request %d is on the row's own session", i)
		require.NotEmpty(t, req.Nonce, "request %d carries a delivery nonce", i)
		require.False(t, nonces[req.Nonce], "request %d repeats a nonce: it would be a redelivery", i)
		nonces[req.Nonce] = true
		require.NotNil(t, req.Event, "request %d carries the B-B event", i)

		wantSeq := i - ackRTTWarmups // the warm-ups are -ackRTTWarmups..-1, the samples 0..n-1
		if i < ackRTTWarmups {
			wantSeq = -1 - i // ackRTTRequest's own numbering for a warm-up
		}
		require.Equal(t, ackRTTToolUseID(wantSeq), req.Event.ToolUseID,
			"request %d is not the one the tranche sends in that position", i)
	}

	// The delivery ledger lists this tranche by identity, in the order it goes out (sentIdentities).
	ids := sentIdentities(0, false, samples)
	require.Len(t, ids, len(c.sent))
	for i, req := range c.sent {
		require.Equal(t, deliveryIdentity{Session: req.Session, ToolUse: req.Event.ToolUseID}, ids[i],
			"request %d is not the identity the ledger looks for", i)
	}

	// The client belongs to measureAckRTT, which closes it; the tranche must not.
	require.Zero(t, c.closed, "the tranche does not close a client it was handed")
}

// TestAckRTTTranche_ACancelledContextSendsNothing pins the other end of the warm-up loop: its
// context check comes before its send, so a cancelled run issues no request at all rather than
// putting an uncounted delivery into the daemon on its way out.
func TestAckRTTTranche_ACancelledContextSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := &recordingClient{}
	out, err := ackRTTTranche(ctx, c, "/bench/project", 3)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, out)
	require.Empty(t, c.sent, "a cancelled tranche sends neither a warm-up nor a sample")
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
