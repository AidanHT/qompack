package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
)

// warmConnectDeadline and warmAckDeadline bound each individual request warmDaemon issues. See
// newProbeClient's doc comment (transport.go) for why this is a per-request ipc.Client.Send
// rather than one held connection.
const (
	warmConnectDeadline = 2 * time.Second
	warmAckDeadline     = 2 * time.Second
)

// warmDaemon sends n total warm-up requests to addr, so the WAL, registry and ring are warm
// before the measured phases begin. It returns the total ToolResponse bytes sent by the hot-path
// tranche, purely for the human summary — never load-bearing for the gate.
//
// FIX ROUND 2, N-1: only the first hotTranche requests are genuine hot-path traffic —
// task-7-spec.md step 3's own deterministic, seed-1, mixed-shape observe.tool payloads. The
// review's independent re-run showed that sending ALL n as observe.tool (FIX ROUND 1's shape) put
// n in-process, sub-millisecond samples into the daemon's hook_controlled histogram — the exact
// series ruling #29 made the gated "B-A" row — where they could outnumber the real hook-spawn
// population from the B-A/B-D loop and pull the gated percentile toward a warm-up number instead
// of a hook number. The remaining n-hotTranche requests are sent as admin.ping round trips
// instead: real traffic that still exercises the daemon's accept/connection loop exactly as
// before, but admin.ping is not req.Op.HotPath() (internal/ipc/op.go), so it can never touch
// hook_controlled or l0_ingest.
//
// FIX ROUND 1, M-2: by internal/ipc/client.go's own Client.Send contract, Send NEVER returns a
// propagating error — every failure (a connect timeout, a write failure, DaemonEnabled==false, a
// HotSpool breach) is swallowed into a spool append and (Response{OK:false}, nil). The `if _, err
// := c.Send(...); err != nil` branches below are therefore near-dead code, kept only because
// Client is an interface and a future/alternate implementation could legitimately violate that
// contract. Neither loop checks resp.OK either: a per-request refusal is not itself caught here.
// The actual delivery-integrity guard lives one layer up, in runHarness (main.go, FIX ROUND 1
// I-2): after the run, the daemon's own l0_ingest count is asserted to equal exactly
// iterations + hotTranche (FIX ROUND 2 moved this from + n), which catches a degraded run
// (partial or total) that this loop's own return values cannot.
func warmDaemon(ctx context.Context, addr ipc.Addr, spool ipc.SpoolWriter, projectRoot string, n, hotTranche int) (int64, error) {
	c := newProbeClient(addr, spool, warmConnectDeadline)
	defer func() { _ = c.Close() }()

	gen := newPayloadGen(1) // seed 1, task-7-spec.md step 3.
	var hotBytes int64
	for i := 0; i < hotTranche; i++ {
		if err := ctx.Err(); err != nil {
			return hotBytes, err
		}
		ev := gen.next(i, warmSessionID, projectRoot)
		hotBytes += int64(len(ev.ToolResponse))
		req := ipc.Request{
			Op: ipc.OpObserveTool, Session: warmSessionID, TS: core.NowMilli(core.SystemClock()), Event: &ev,
		}
		if _, err := c.Send(ctx, req, warmAckDeadline); err != nil {
			return hotBytes, fmt.Errorf("hotpath: warm-up hot-path request #%d: %w", i, err)
		}
	}

	for i := hotTranche; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return hotBytes, err
		}
		req := ipc.Request{
			Op: ipc.OpAdminPing, Session: warmSessionID, TS: core.NowMilli(core.SystemClock()),
		}
		if _, err := c.Send(ctx, req, warmAckDeadline); err != nil {
			return hotBytes, fmt.Errorf("hotpath: warm-up admin.ping #%d: %w", i, err)
		}
	}
	return hotBytes, nil
}

// measureAckRTT times the client-side ACK round trip of n in-process observe.tool deliveries: the
// reported-only hook_ack_rtt row (budgetIDHookAckRTT, report.go), from which design §7.5 derives
// slack99 and therefore the ACK deadline itself.
//
// It sends through the SAME ipc.Client every other probe here uses, so a sample is a real connect,
// a real write, the daemon's own durable Accept and the one-byte ACK coming back — a hook's whole
// wait, minus only the process spawn B-D already reports separately. Every request carries a FRESH
// delivery nonce, so each takes a real lease and pays the durable path in full rather than being
// answered as a redelivery of the one before it.
//
// The deadline is this harness's own generous probe deadline rather than the product's
// AckDeadlineMs, and that is the point: a client that gives up early spools and returns fast, which
// would measure the deadline instead of the round trip the deadline is being sized from. Send never
// returns a propagating error (its own contract), so every attempt yields a sample and the returned
// slice has exactly n of them — which is what lets runHarness count them into the delivery ledger.
//
// WARM-UP (review round 2): the first ackRTTWarmups requests are sent and DISCARDED. They are on
// ackRTTSessionID, a session the daemon has never seen, so without them sample #0 paid a session-
// registry insertion and a WAL segment create on top of the round trip — and at n=64 with
// nearest-rank percentiles p99 is the maximum sample, so that one sample WAS the number §7.5 sizes
// the ACK deadline from. See ackRTTWarmups (main.go) for why two, and for why the tranche was not
// simply made larger instead.
//
// ORDERING CONTRACT (review round 1): runHarness must read the status op for the gated B-A and B-B
// rows BEFORE calling this, and read it a second time afterwards for the ledger's delivered count.
// These requests are real hot-path traffic and reach hook_controlled and l0_ingest like any other,
// but with req.TS stamped in-process microseconds before Send rather than inside a spawned child,
// so they are sub-millisecond by construction and are not the quantity either gate judges. A
// percentile that has already been read cannot be pulled by them; a count read afterwards still
// sees them, which is what keeps a deferral of this tranche's own from passing unnoticed. The
// warm-ups are inside this function and therefore inside that window too: they are sent after the
// gated snapshot and before the ledger's, exactly like the timed samples, and ackRTTTrancheSends is
// what tells the ledger there are n + ackRTTWarmups of them.
func measureAckRTT(ctx context.Context, addr ipc.Addr, spool ipc.SpoolWriter, projectRoot string, n int) ([]time.Duration, error) {
	c := newProbeClient(addr, spool, probeConnectDeadline)
	defer func() { _ = c.Close() }()

	return ackRTTTranche(ctx, c, projectRoot, n)
}

// ackRTTTranche is the tranche itself, over a client the caller owns: ackRTTWarmups requests that
// are sent and thrown away, then n that are sent and timed, and only those n come back.
//
// It is a function of its own so that the SENDING can be tested. measureAckRTT builds a real
// ipc.Client against a real daemon, so a test of it is a bench run; a test of this one hands it a
// recording client and counts what actually went out —
// TestAckRTTTranche_SendsTheWarmUpsAndTimesOnlyTheSamples, which is the regression for the warm-up
// requests being ISSUED rather than merely being counted into the ledger by ackRTTTrancheSends.
// Without it the warm-up loop could stop sending and every test in this package would stay green,
// while the run itself failed reconciliation with Sent over-counting by ackRTTWarmups.
func ackRTTTranche(ctx context.Context, c ipc.Client, projectRoot string, n int) ([]time.Duration, error) {
	out := make([]time.Duration, 0, n)
	for _, seq := range ackRTTTrancheSeqs(n) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := ackRTTRequest(projectRoot, seq)
		if err != nil {
			return nil, fmt.Errorf("hotpath: hook_ack_rtt request %d: %w", seq, err)
		}
		if seq < 0 {
			_, _ = c.Send(ctx, req, probeAckDeadline) // a warm-up: discarded, never timed, never reported
			continue
		}
		start := time.Now()
		_, _ = c.Send(ctx, req, probeAckDeadline)
		out = append(out, time.Since(start))
	}
	return out, nil
}

// ackRTTTrancheSeqs is the order ackRTTTranche sends its requests in, by the sequence number each
// one carries: the ackRTTWarmups discarded warm-ups first, numbered -1, -2, ..., then the n timed
// samples, numbered 0..n-1. sentIdentities (delivery.go) reads the same list, so the ledger's
// identities of this tranche are the ones it actually sent, not a second spelling of them.
func ackRTTTrancheSeqs(n int) []int {
	seqs := make([]int, 0, ackRTTWarmups+n)
	for i := 0; i < ackRTTWarmups; i++ {
		seqs = append(seqs, -1-i)
	}
	for i := 0; i < n; i++ {
		seqs = append(seqs, i)
	}
	return seqs
}

// ackRTTRequest is one hook_ack_rtt delivery: the same fixed event B-A's spawns carry, on the row's
// own session, with a FRESH delivery nonce so it takes a real lease and pays the durable path in
// full rather than being answered as a redelivery of the one before it. The warm-ups and the timed
// samples differ in nothing but whether the round trip is kept.
func ackRTTRequest(projectRoot string, seq int) (ipc.Request, error) {
	nonce, err := ipc.NewDeliveryNonce()
	if err != nil {
		return ipc.Request{}, err
	}
	ev := observeRTTEvent(ackRTTSessionID, projectRoot, ackRTTToolUseID(seq))
	return ipc.Request{
		Op: ipc.OpObserveTool, Session: ackRTTSessionID, TS: core.NowMilli(core.SystemClock()),
		Nonce: nonce, Event: &ev,
	}, nil
}

// ackRTTNote is the artifact's own disclosure for the hook_ack_rtt row: what it is for, that it is
// never gated, and — the review-round-1 correction — where its own traffic sits relative to the
// GATED populations.
//
// It no longer computes a share of them, because it no longer forms one. The tranche is sent after
// the snapshot those rows are read from (runHarness's ordering contract, measureAckRTT above), so
// its share of each gated population is zero and saying so is the honest disclosure. The earlier
// note computed 64/(expectedHotPathSends+64), which named only its own tranche and never the
// warm-up's identical 64 that expectedHotPathSends had already folded in — so with --warm-daemon it
// disclosed half the in-process share. The in-process traffic that IS inside the gated populations
// is the warm-up's, and buildNotes discloses it with its own computed proportion.
func ackRTTNote() string {
	return fmt.Sprintf(
		"%s is REPORTED, never gated: it is the client-side ACK round trip of %d in-process observe.tool deliveries, each with a fresh nonce and a real lease, timed through the same ipc.Client a hook uses. Design §7.5 sizes the ACK deadline from it as AckDeadlineMs = L0IngestMs + ceil(slack99), where slack99 = p99(%s) - p99(%s) over this same run. The timed samples are preceded by %d DISCARDED warm-up requests on the same session: without them sample #0 paid a session-registry insertion and a WAL segment create on top of the round trip, and at %d samples with nearest-rank percentiles p99 IS the maximum sample, so that one sample became the number the deadline was sized from. Its traffic IS real hot-path traffic, so all %d requests do reach the daemon's %s and hook_controlled histograms — but this run reads both of those for the gated %s and %s rows BEFORE any of the tranche is sent, warm-ups included, so none of it is inside either gated population, and the %s this row is differenced against contains none of its own deliveries. They are reconciled rather than ignored: a second status read after the tranche counts every one of them, warm-ups included, into the delivery ledger, so a deferral of this tranche's own cannot pass unnoticed. The in-process traffic that does sit inside the gated populations is the warm-up's, disclosed with its own computed proportion. A p99 over %d samples is still that set's own top sample, which is why §7.5 takes the maximum across three runs rather than trusting one",
		budgetIDHookAckRTT, ackRTTSamples, budgetIDHookAckRTT, obs.BB, ackRTTWarmups, ackRTTSamples,
		ackRTTSamples+ackRTTWarmups, budgetHistName(obs.BB), obs.BA, obs.BB, obs.BB, ackRTTSamples)
}

// fetchStatus round-trips a Reply status request against addr and decodes its
// daemon.StatusSnapshot payload — the harness's only source for B-B, and, per FIX ROUND 1's
// controller ruling #29, for the gated B-A row too (task-7-spec.md step 8).
func fetchStatus(ctx context.Context, addr ipc.Addr, spool ipc.SpoolWriter) (daemon.StatusSnapshot, error) {
	c := newProbeClient(addr, spool, probeConnectDeadline)
	defer func() { _ = c.Close() }()

	req := ipc.Request{
		Op: ipc.OpStatus, Session: statusSessionID, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}
	resp, err := c.Send(ctx, req, replyRoundTripDeadline)
	if err != nil {
		return daemon.StatusSnapshot{}, fmt.Errorf("hotpath: status round trip: %w", err)
	}
	if !resp.OK {
		return daemon.StatusSnapshot{}, fmt.Errorf("hotpath: status refused: %s", resp.Err)
	}
	var snap daemon.StatusSnapshot
	if err := json.Unmarshal(resp.Data, &snap); err != nil {
		return daemon.StatusSnapshot{}, fmt.Errorf("hotpath: decoding status: %w", err)
	}
	return snap, nil
}

// histHookControlledObservedName mirrors internal/daemon/handlers.go's own unexported
// histHookControlledObserved constant ("hook_controlled_observed"): the daemon-owned strict
// lower-bound histogram this harness reports informationally (task-7-spec.md step 8's "observed/
// estimated hook.controlled pair"), never gated.
const histHookControlledObservedName = "hook_controlled_observed"

// buildNotes assembles the out.json "notes" array: the wave-1 B-C disclaimer task-7-spec.md step
// 3 requires, the daemon's own strict-lower-bound cross-check against the now-gated B-A row (FIX
// ROUND 1, controller ruling #29), the notes explaining exactly what B-A's and B-B's own n each
// include when warm-up ran (M-4, and FIX ROUND 2's N-1 fix for B-A), and — whenever this run
// deferred anything to the spool — the delivery ledger plus each gated row's own tail-adjustment
// disclosure (rowNotes, empty strings skipped).
//
// A run that delivered everything adds nothing: the deferral note is emitted only when the ledger
// actually recorded one, so a clean run's artifact is exactly what it has always been.
func buildNotes(snap daemon.StatusSnapshot, warmDaemonRan bool, iterations int, ledger deliveryLedger, rowNotes ...string) []string {
	notes := []string{"B-C not measured in wave 1: the processing seams are stubs"}
	if ledger.Deferred > 0 {
		notes = append(notes, fmt.Sprintf(
			"delivery ledger: %d hot-path requests sent, %d delivered live to the daemon, %d DEFERRED to the client spool and 0 lost. A deferral is §8.1/§12.2's documented degrade-rather-than-block path (internal/ipc/client.go's Send spools and returns instead of waiting), so the event is durable and the daemon replays it — its client-spool watcher does so while the run is still going — and every one of the %d requests was found by its own identity (session and tool_use_id) in a client spool, the daemon's WAL or the store's tool_use index. A deferral is still a sample missing from the top of every gated daemon-side population, so it is counted back in as an over-budget sample rather than dropped",
			ledger.Sent, ledger.Delivered, ledger.Deferred, ledger.Sent))
	}
	for _, n := range rowNotes {
		if n != "" {
			notes = append(notes, n)
		}
	}
	if hs, ok := snap.Latency[histHookControlledObservedName]; ok && hs.N > 0 {
		notes = append(notes, fmt.Sprintf(
			"daemon-observed hook_controlled_observed (recvTS-reqTS, strict lower bound, no hotPathTailAllowance): p50=%.3fms p99=%.3fms n=%d — informational cross-check against the gated B-A row above, which adds the tail allowance",
			msf(hs.P50), msf(hs.P99), hs.N))
	}
	if warmDaemonRan {
		// FIX ROUND 2, N-1: the gated B-A row's own population composition, stated explicitly —
		// the re-review's minimum ask. warmHotTranche in-process client Sends (stamped at Send
		// time, not a spawned process's main() entry) plus `iterations` real hook spawns from the
		// B-A/B-D loop; the tranche is deliberately small, but it is not zero, so the composition
		// is disclosed rather than asserted away.
		//
		// FIX ROUND 3, R2-2: the disclosure now PRINTS the computed warm-up proportion instead of
		// asserting the unconditional "stays hook-spawn-dominated" — at n=2000 that claim happens
		// to hold (64/2064 ≈ 3.1%), but a caller running this harness with a small --iterations
		// value could see the warm-up tranche dominate instead, and an honesty-of-measurement
		// harness should never assert a proportion it has not actually computed for this run.
		warmPct := 100 * float64(warmHotTranche) / float64(iterations+warmHotTranche)
		notes = append(notes, fmt.Sprintf(
			"B-A's gated n=%d includes %d in-process warm-up observe.tool requests (Send-time TS, not a spawned process's main() entry) alongside %d real hook spawns from the B-A/B-D loop — the warm-up hot-path tranche (warmHotTranche=%d, a small, fixed constant) is %.1f%% of the gated population for this run; see B-A_spawn_estimate for the wall-clock diagnostic derived purely from real spawns",
			iterations+warmHotTranche, warmHotTranche, iterations, warmHotTranche, warmPct))
		notes = append(notes, fmt.Sprintf(
			"B-B's n includes the %d warm-up hot-path (observe.tool) requests, not just the B-A/B-D measurement loop — the daemon's own %s count, reported verbatim (conservative for B-B: warm-up payloads run 4KB-256KB, larger than B-A/B-D's fixed representative payload). The other %d warm-up requests are admin.ping round trips and never reach l0_ingest at all (FIX ROUND 2, N-1)",
			warmHotTranche, budgetHistName(obs.BB), warmIterations-warmHotTranche))
	}
	return notes
}
