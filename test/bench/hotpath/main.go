package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The harness-owned session ids stamped into every payload this program sends, real spawn or
// direct ipc.Client call alike.
const (
	baSessionID   = core.SessionID("bench-b-a")
	beSessionID   = core.SessionID("bench-b-e")
	warmSessionID = core.SessionID("bench-warm")
	// ackRTTSessionID stamps the in-process observe.tool requests the reported-only hook_ack_rtt
	// row times (measureAckRTT).
	ackRTTSessionID = core.SessionID("bench-ack-rtt")
)

// The fixed iteration counts task-7-spec.md itself names for the two auxiliary measurements: the
// spawn floor (step 4: "spawn qompack version 200 times") and B-E (step 7: "spawn qompack
// checkpoint 50 times").
const (
	spawnFloorIterations = 200
	checkpointIterations = 50
	warmIterations       = 2000
)

// warmHotTranche is FIX ROUND 2's own fix for N-1 (Important): the daemon's hook_controlled
// histogram — the exact series ruling #29 made the gated "B-A" row — is written by EVERY
// req.Op.HotPath() request the daemon dispatches (internal/daemon/handlers.go's dispatchOp), with
// no way to filter it by session or by "came from a real process" through the status op. Sending
// all warmIterations observe.tool requests as hot-path traffic (FIX ROUND 1's shape) therefore put
// 2000 in-process, sub-millisecond samples into the very population the 15ms gate reads — at
// n=2000 they can outnumber the real hook-spawn population from the B-A/B-D loop outright,
// pulling the gated p99 down to roughly the real population's own p98 (worse at smaller
// --iterations) and its p50 to a warm-up number, not a hook number. Optimistic bias in a hard
// gate, however sound the numbers happen to look.
//
// The fix: only warmHotTranche observe.tool requests (still real, still deterministic,
// task-7-spec.md step 3's own seed-1 mixed payloads) are sent as genuine hot-path traffic — just
// enough to prime the WAL/ingest/registry paths and give the daemon's own JIT/caches something to
// warm on before the REAL measurement begins. The rest of warmIterations is sent as admin.ping
// round trips (measure.go's warmDaemon) — real traffic that exercises the accept/connection loop
// exactly as before, but admin.ping is not req.Op.HotPath() (internal/ipc/op.go), so it never
// touches hook_controlled or l0_ingest at all. With --iterations at its 2000 default, the gated
// B-A population is now iterations + warmHotTranche = 2064, i.e. ~97% real hook spawns — a world
// away from FIX ROUND 1's 50/50 split at n=2000.
//
// 64 is not tuned to any particular budget; it is simply small relative to any --iterations value
// task-7-spec.md's own CI/nightly commands use (2000/5000) while still being enough real hot-path
// traffic to exercise the WAL append path, the session registry and the breach detector's
// rolling-512-sample ring more than a single sample would.
const warmHotTranche = 64

// ackRTTSamples is how many round trips the reported-only hook_ack_rtt row times.
//
// What the row is for: SP20-D1 design §7.5 derives the ACK deadline as
// AckDeadlineMs = L0IngestMs + ceil(slack99), where slack99 = p99(hook_ack_rtt) - p99(B-B). B-B is
// the daemon's own l0_ingest cost and contains none of the transport; this row is the same delivery
// measured from OUTSIDE the daemon, so the difference is exactly what the deadline must cover
// beyond the durable path itself: the pipe read, dispatchOp up to Accept, the ACK write and the
// client's wake-up.
//
// Why it is small, and the cost of that. These are real observe.tool requests: they take real
// leases and land in the daemon's own l0_ingest AND hook_controlled histograms, which are the
// populations B-B and the gated B-A read. That is the same contamination FIX ROUND 2's N-1 fixed
// for the warm-up, so this follows warmHotTranche's own resolution — a small, fixed tranche, with
// the proportion it forms of each population DISCLOSED in the artifact (ackRTTNote) rather than
// asserted away. The price is resolution: a p99 over 64 samples is its own top sample, so slack99
// derived from one run is an estimate, and §7.5's M2 protocol takes the maximum over three runs.
const ackRTTSamples = 64

// ackRTTWarmups is how many requests measureAckRTT sends on ackRTTSessionID, and DISCARDS, before
// it starts timing.
//
// Without them sample #0 was the first request that session had ever made: the daemon's session
// registry had no entry for it and the ingest had no WAL segment, so the sample carried a registry
// insertion and a segment create on top of the round trip it was supposed to time. With n=64 and
// nearest-rank percentiles p99 IS the maximum sample, so a single slow first sample does not
// average out — it becomes the number. Design §7.5 then reads p99(hook_ack_rtt) to derive slack99
// and AckDeadlineMs = L0IngestMs + ceil(slack99), and takes the MAXIMUM across three runs, which
// compounds a per-run first-sample bias rather than diluting it.
//
// warmDaemon does the same thing for the run as a whole, for the same reason; this is its precedent
// applied to a session warmDaemon never touches. Two rather than one: the first creates the
// registry entry and the segment, the second pays whatever one-off the first leaves behind (the
// session's first lease and its arrival counter), so the timed loop starts on a session in steady
// state. They are hot-path traffic like the timed samples and are counted into the delivery ledger
// (ackRTTTrancheSends); raising the tranche instead was the alternative and was not taken, because
// it would change this row's disclosed proportion of the daemon's histograms for a bias that two
// requests remove.
const ackRTTWarmups = 2

// defaultIterations is --iterations's own default. FIX ROUND 1, M-3: this used to alias
// warmIterations, which meant changing the warm-up count would silently change the default
// measurement count too — two unrelated quantities that happened to share one value. They are
// still both 2000 today (task-7-spec.md's own default), but as two independently-named constants
// a future change to one can no longer silently move the other.
const defaultIterations = 2000

// The bounds this harness polls/waits against. daemonUpBound is generous relative to
// test/e2e/daemon_e2e_test.go's own e2eDaemonUpBound (10s) because a bench run's daemon has to
// come up on a host that may already be under load from the very spawns this program is about to
// issue.
//
// daemonDownBound is derived from the daemon's OWN exit bound rather than picked, because past it
// this harness SIGKILLs the process (process.go). A bare 10s was shorter than daemon.Stop's
// documented 15s cleanup window, so the harness could kill a daemon that was still finishing
// legitimately — and a daemon killed mid-WriteAtomic leaves a staging file behind, which is
// precisely the leak TestV1_WriteSetConfinedAcrossFullHookSequence catches. Deriving it means the
// SIGKILL can only ever land on a daemon that has already blown its own bound.
const (
	daemonUpBound   = 20 * time.Second
	daemonUpTick    = 20 * time.Millisecond
	daemonDownBound = daemon.StopCleanupBound + 5*time.Second
)

// flags is bench-hotpath's own command-line surface (task-7-brief.md's binding ruling: forward
// --iterations --hook --warm-daemon --json --project). --under-coload is an addition to that list
// rather than one of its five: see its own comment in parseFlags for what it declares and why the
// brief's five could not express it.
type flags struct {
	iterations  int
	hook        string
	warmDaemon  bool
	jsonPath    string
	project     string
	underCoload bool
}

// parseFlags parses args into a flags value. flag.ErrHelp is returned verbatim so main can treat
// -h/--help as a clean exit rather than a usage error.
func parseFlags(args []string, errw io.Writer) (flags, error) {
	fs := flag.NewFlagSet("bench-hotpath", flag.ContinueOnError)
	fs.SetOutput(errw)
	f := flags{}
	fs.IntVar(&f.iterations, "iterations", defaultIterations, "number of B-A/B-D spawn samples")
	fs.StringVar(&f.hook, "hook", "observe-tool", "hook subcommand to spawn for B-A/B-D (only observe-tool is supported today)")
	fs.BoolVar(&f.warmDaemon, "warm-daemon", false, "pre-populate the daemon before measuring: a small hot-path observe.tool tranche plus admin.ping traffic for the rest (FIX ROUND 2, N-1)")
	fs.StringVar(&f.jsonPath, "json", "", "write the out.json artifact to this path (omit to skip)")
	fs.StringVar(&f.project, "project", "", "use this directory as the temp project instead of creating one")
	// --under-coload is a statement about the RUN'S ENVIRONMENT, not a switch on a gate, and it is
	// spelled that way on purpose: the caller declares a fact only the caller knows (this harness
	// is sharing its host with unrelated concurrent work), and the harness derives the one
	// consequence that fact has — the two rows whose samples span a process boundary, the
	// wall-clock B-E row and B-A, become measurements rather than judgements, each disclosed in
	// the artifact by its own note (beWallWaivedNote, baWallWaivedNote). Nothing else changes:
	// the CPU-time B-E gate (budgetIDBECPU) and B-B are still hard, and every invocation that
	// does not pass it — bench-gate's and nightly bench-deep's `devtool bench-hotpath` lines,
	// ci.yml's test-e2e job, and a bare local run — keeps every gate it has always had, byte for
	// byte. Default false so that forgetting it can only ever make a run STRICTER.
	//
	// The evidence for B-A is CI's own, on windows-latest, one commit: the B-A row measured p99
	// 3.072 ms in bench-gate (harness alone on its runner) and 11.264 ms then 18.432 ms in two
	// whole-tree `test` job runs minutes apart (limit 15 ms), while the spawn floor's p50 went
	// 12.954 → 24.431 / 23.143 ms and B-B — which contains no process spawn — moved only
	// 0.576 → 0.768 / 0.704 ms. B-A is the daemon-observed hook_controlled estimate (recvTS −
	// reqTS + tail allowance): reqTS is stamped inside the spawned hook process, so the interval
	// contains the child's scheduling wait under co-load, and there is no CPU-time analogue of a
	// cross-process latency.
	//
	// B-B stays gated under this flag, and the reason recorded here for it is now STALE. The
	// premise was that B-B "contains no process spawn" and so barely moves under co-load, measured
	// as 0.576 → 0.768 ms above. That measurement predates f6a8691: B-B times ingest.Accept in
	// full, and since the delivery path became durable that region contains three flushes (the WAL
	// Sync, the lease journal's Sync and the seal), each of which co-load moves by roughly the
	// factor the figures above show for everything else. SP20-D1 design §7.6 prices both ways out —
	// one co-load-sized limit, or ADR 0010's report-and-judge-in-isolation with the co-load-immune
	// structural gates carrying the invariant — and the choice is the owner's open question Q3.
	// Until that ruling this flag's behaviour is UNCHANGED: B-B is gated in both shapes, exactly as
	// it has been. Only the reason above is corrected, so the next reader does not inherit a
	// premise the durable path retired.
	fs.BoolVar(&f.underCoload, "under-coload", false,
		"declare that this run shares its host with unrelated concurrent work (e.g. the whole-tree `go test ./...`), "+
			"so the wall-clock B-E row and the B-A row are reported instead of gated; B-B and the CPU-time B-E gate are unaffected")
	if err := fs.Parse(args); err != nil {
		return flags{}, err
	}
	if f.iterations <= 0 {
		return flags{}, fmt.Errorf("--iterations must be > 0, got %d", f.iterations)
	}
	if _, err := hookArgs(f.hook); err != nil {
		return flags{}, err
	}
	return f, nil
}

// hookArgs maps --hook's value onto the qompack subcommand args measureSpawns spawns for B-A/B-D.
// "observe-tool" is the only value task-7-spec.md's own CI/nightly commands ever pass.
func hookArgs(hook string) ([]string, error) {
	switch hook {
	case "", "observe-tool":
		return []string{"observe", "tool"}, nil
	default:
		return nil, fmt.Errorf("--hook %q not supported (only observe-tool)", hook)
	}
}

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr))
}

// runMain is main's testable body: parse flags, run the harness, write the artifact, print the
// summary, and return the process exit code — non-zero when a gated budget fails
// (task-7-spec.md step 10) or when the harness itself could not complete.
func runMain(args []string, stdout, stderr io.Writer) int {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintln(stderr, "hotpath:", err)
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	report, err := runHarness(ctx, f, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "hotpath:", err)
		return 1
	}

	if f.jsonPath != "" {
		if err := writeJSON(f.jsonPath, report); err != nil {
			fmt.Fprintln(stderr, "hotpath: writing json artifact:", err)
			return 1
		}
	}
	printSummary(stdout, report)

	if report.GateFailed() {
		fmt.Fprintln(stderr, "hotpath: a gated budget breached its limit — see the summary above")
		return 1
	}
	return 0
}

// runHarness is the ten-step body task-7-spec.md lays out end to end: build the real binary,
// start a real daemon child process, optionally warm it, measure the spawn floor, B-A/B-D, B-E,
// read B-B (and, per FIX ROUND 1's controller ruling #29, the gated B-A) off the daemon's own
// status op, and assemble the Report. stderr is threaded through only for stopDaemon's own
// teardown diagnostics (M-5, I-3) — nothing on the measured path writes to it.
func runHarness(ctx context.Context, f flags, stdout, stderr io.Writer) (Report, error) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return Report{}, err
	}

	projectRoot, cleanupProject, err := resolveProjectDir(f.project)
	if err != nil {
		return Report{}, err
	}
	defer cleanupProject()

	homeDir, err := os.MkdirTemp("", "qompack-bench-home-")
	if err != nil {
		return Report{}, fmt.Errorf("hotpath: creating temp home: %w", err)
	}
	defer func() { _ = os.RemoveAll(homeDir) }()

	if err := paths.EnsureLayout(paths.Of(projectRoot)); err != nil {
		return Report{}, fmt.Errorf("hotpath: preparing project layout: %w", err)
	}

	buildDir, err := os.MkdirTemp("", "qompack-bench-build-")
	if err != nil {
		return Report{}, fmt.Errorf("hotpath: creating temp build dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(buildDir) }()

	binPath := filepath.Join(buildDir, "qompack"+exeSuffix())
	fmt.Fprintf(stdout, "hotpath: building %s...\n", binPath)
	if err := buildBinary(moduleRoot, binPath); err != nil {
		return Report{}, err
	}

	// QOMPACK_IPC_ADDR + QOMPACK_PROJECT_ROOT keep the daemon's endpoint and every file it
	// touches anchored inside this run's own temp project (task-7-brief.md's binding ruling).
	addrOverride, err := ipcAddrEnvOverride(shortPIDTag())
	if err != nil {
		return Report{}, err
	}
	envOverrides := map[string]string{
		"QOMPACK_PROJECT_ROOT": projectRoot,
		"QOMPACK_IPC_ADDR":     addrOverride,
		"HOME":                 homeDir,
		"USERPROFILE":          homeDir,
	}
	// This process's own direct ipc.Resolve/dial calls (admin.ping, warm-up, status) must see the
	// identical override every spawned child sees.
	if err := os.Setenv("QOMPACK_IPC_ADDR", addrOverride); err != nil {
		return Report{}, fmt.Errorf("hotpath: %w", err)
	}
	if err := os.Setenv("QOMPACK_PROJECT_ROOT", projectRoot); err != nil {
		return Report{}, fmt.Errorf("hotpath: %w", err)
	}

	addr, err := ipc.Resolve(projectRoot)
	if err != nil {
		return Report{}, fmt.Errorf("hotpath: resolving daemon address: %w", err)
	}
	childEnv := buildChildEnv(envOverrides)

	// One real SpoolWriter, rooted at the project's own spool directory, backs every admin/
	// status/warm-up client this program constructs directly (transport.go's newProbeClient) —
	// the same spool a real hook client would use, so a connect failure during warm-up degrades
	// exactly the way production does rather than silently dropping.
	spool, err := ipc.NewSpool(paths.Of(projectRoot).Spool)
	if err != nil {
		return Report{}, fmt.Errorf("hotpath: creating spool: %w", err)
	}

	fmt.Fprintf(stdout, "hotpath: starting daemon (%s)...\n", addr.Path)
	dp, err := startDaemon(binPath, projectRoot, childEnv)
	if err != nil {
		return Report{}, err
	}
	defer stopDaemon(dp, addr, spool, stderr)

	if err := waitForDaemon(ctx, addr, spool, daemonUpBound, daemonUpTick); err != nil {
		return Report{}, fmt.Errorf("%w (daemon stderr:\n%s)", err, dp.stderrString())
	}
	fmt.Fprintln(stdout, "hotpath: daemon is reachable")

	if f.warmDaemon {
		fmt.Fprintf(stdout, "hotpath: warming daemon (%d hot-path observe.tool + %d admin.ping)...\n",
			warmHotTranche, warmIterations-warmHotTranche)
		bytesSent, werr := warmDaemon(ctx, addr, spool, projectRoot, warmIterations, warmHotTranche)
		if werr != nil {
			return Report{}, werr
		}
		fmt.Fprintf(stdout, "hotpath: warm-up sent ~%.1f MB across %d hot-path requests (plus %d admin.ping round trips)\n",
			float64(bytesSent)/(1<<20), warmHotTranche, warmIterations-warmHotTranche)
	}

	fmt.Fprintf(stdout, "hotpath: measuring spawn floor (%d x qompack version)...\n", spawnFloorIterations)
	floorSamples, err := measureSpawnFloor(ctx, binPath, spawnFloorIterations, childEnv)
	if err != nil {
		return Report{}, err
	}
	floorP50, _, floorP99, _, _ := percentiles(append([]time.Duration(nil), floorSamples.Wall...))

	hArgs, err := hookArgs(f.hook)
	if err != nil {
		return Report{}, err
	}
	fmt.Fprintf(stdout, "hotpath: measuring B-A/B-D (%d x qompack %v)...\n", f.iterations, hArgs)
	bdSamples, err := measureSpawns(ctx, binPath, hArgs, f.iterations, childEnv, func(seq int) []byte {
		return representativeObservePayload(baSessionID, projectRoot, seq)
	})
	if err != nil {
		return Report{}, err
	}
	baSamples := subtractFloor(bdSamples.Wall, floorP50)

	fmt.Fprintf(stdout, "hotpath: measuring B-E (%d x qompack checkpoint)...\n", checkpointIterations)
	beSamples, err := measureSpawns(ctx, binPath, []string{"checkpoint"}, checkpointIterations, childEnv, func(seq int) []byte {
		return checkpointPayload(beSessionID, projectRoot, seq)
	})
	if err != nil {
		return Report{}, err
	}

	// The gated rows' own snapshot, read BEFORE the hook_ack_rtt tranche below.
	//
	// That tranche sends real hot-path traffic — it has to, to time an ACK — so every one of its
	// requests reaches recordHotPathSample and ing.Accept, and would otherwise sit inside the very
	// populations the gates read: hook_controlled (the gated B-A row, controller ruling #29) and
	// l0_ingest (B-B). They are not the same quantity as the rest of that population, either:
	// measureAckRTT stamps req.TS in-process microseconds before Send, while a spawned hook stamps
	// it inside the child, so the tranche's samples are sub-millisecond by construction and carry
	// none of the child's scheduling wait. That is the contamination FIX ROUND 2's N-1 fixed for the
	// warm-up by moving its bulk to admin.ping, a remedy unavailable to a row that must send
	// hot-path traffic; reading the histograms first is the remedy that is available, since a sample
	// cannot enter a percentile that has already been read. slack99 = p99(hook_ack_rtt) - p99(B-B)
	// is therefore not self-referential either: B-B's population here contains none of this row's
	// own deliveries.
	//
	// The tranche is still accounted for, not merely excluded: it is counted into the delivery
	// ledger against a second status read below, so a deferral of its own cannot pass unnoticed.
	gatedSnap, err := fetchStatus(ctx, addr, spool)
	if err != nil {
		return Report{}, fmt.Errorf("hotpath: reading B-A/B-B off the daemon's status op: %w", err)
	}

	fmt.Fprintf(stdout, "hotpath: measuring hook ACK round trip (%d x in-process observe.tool)...\n", ackRTTSamples)
	ackRTT, err := measureAckRTT(ctx, addr, spool, projectRoot, ackRTTSamples)
	if err != nil {
		return Report{}, err
	}

	// FIX ROUND 1, I-2: reconcile delivery before trusting anything the status op reports.
	// internal/ipc/client.go's Send degrades silently to the spool on a connect timeout, a write
	// failure, DaemonEnabled==false, or a HotSpool breach — every one of those paths makes the
	// SPAWNED hook exit FASTER than a delivered one, which would bias a wall-clock B-A estimate
	// downward and could pass a gate that is actually measuring the degraded path. l0_ingest's own
	// count is the daemon's ground truth for "how many observe.tool requests actually arrived":
	// every one of them (the warm-up's own HOT TRANCHE and the B-A/B-D loop alike) reaches
	// acceptHotPathEvent -> ing.Accept, and nothing else — B-E's checkpoint spawns, the spawn
	// floor, and (FIX ROUND 2) the warm-up's own admin.ping bulk — touches it.
	//
	// The shortfall that count can show is not one condition but two, and only one of them is a
	// defect: a request DEFERRED to the spool is durable and replayable (§8.1/§12.2's documented
	// degrade-rather-than-block behaviour, the same path
	// TestIntegration_HotPathDegradesRatherThanBlocks pins), while a request LOST is neither. The
	// census below is what tells them apart — it reads the same spool tier the client wrote to —
	// and the deferrals it finds are carried into the budget rows as over-budget samples, never
	// dropped from the population (report.go's tailAdjustedP99).
	//
	// The census runs BEFORE the status read, and that order is deliberate. l0_ingest is frozen
	// by this point: the last measured spawn has returned, nothing this program sends afterwards
	// is a hot-path op, and internal/daemon/daemon.go's drainDispatch routes a REPLAYED hot-path
	// line straight to runIngested — never back through ing.Accept — so a drain can never raise
	// the delivered count. The spool census, by contrast, can only ever SHRINK (a drain deletes a
	// client file it has consumed). Reading the shrinking side first is what keeps a drain that
	// fires mid-teardown from turning a deferral into a false "lost". No drain is expected here at
	// all — nothing on this path sends flush or admin.drain, and the idle-tick drain is gated on
	// cfg.Scheduler.Idle.DetectAfterSeconds (120s) of registry silence that a continuous spawn
	// loop never reaches — but the ordering costs nothing and removes the question.
	// The hook_ack_rtt tranche is hot-path traffic like any other, so it is part of the population
	// the ledger reconciles — and it has returned by the time the census runs, which is what keeps
	// "nothing this program sends afterwards is a hot-path op" true above. It is added HERE rather
	// than inside expectedHotPathSends because that function's contract — iterations, plus the
	// warm-up's own tranche — is what TestExpectedHotPathSends pins, and this row is not part of it.
	// ackRTTTrancheSends counts the row's DISCARDED warm-ups alongside its timed samples: they are
	// deliveries too, and a Sent short of them would look to reconcileDelivery like another client
	// feeding this daemon.
	//
	// The ledger and the gated rows therefore cover two populations that differ by exactly this
	// tranche: the ledger's is the whole run, and the gated rows' is what the earlier snapshot saw.
	// gatedLedger (delivery.go) scopes the missing-sample accounting to the second.
	sent := expectedHotPathSends(f.iterations, f.warmDaemon) + ackRTTTrancheSends(len(ackRTT))
	census, err := censusClientSpool(paths.Of(projectRoot).Spool, spool.Path(), harnessHotPathSessions())
	if err != nil {
		return Report{}, err
	}

	// The ledger's own status read, taken AFTER the tranche, because the gated snapshot above was
	// taken before it and so cannot account for it. Only the delivered COUNT is read from here;
	// every percentile this report publishes comes from the earlier snapshot.
	finalSnap, err := fetchStatus(ctx, addr, spool)
	if err != nil {
		return Report{}, fmt.Errorf("hotpath: reading the delivered count off the daemon's status op: %w", err)
	}

	ledger, err := reconcileDelivery(sent, finalSnap.Latency[budgetHistName(obs.BB)].N, census)
	if err != nil {
		return Report{}, err
	}

	// The gated rows are built from the earlier snapshot, so their own shortfall accounting is
	// scoped to the population it covers: the sends this harness had made by then — which is
	// exactly expectedHotPathSends' pinned contract — against that snapshot's own counts.
	bbSnap := gatedSnap.Latency[budgetHistName(obs.BB)]
	gated, err := gatedLedger(ledger, expectedHotPathSends(f.iterations, f.warmDaemon), bbSnap.N)
	if err != nil {
		return Report{}, err
	}

	cfg := config.Defaults()
	// FIX ROUND 1, I-1 / controller ruling #29 (spec step 6 amended): B-A is now gated on the
	// daemon's own hook_controlled histogram (TS-anchored: recvTS - reqTS + hotPathTailAllowance),
	// fetched via status — the same series internal/daemon's breach detector consumes — rather than
	// the wall-clock, floor-subtracted spawn estimate. See bAMethod's doc comment (report.go) for
	// why: a constant subtracted per sample removes the floor's location but none of its
	// dispersion, contaminating exactly the percentile the gate reads.
	baSnap := gatedSnap.Latency[budgetHistName(obs.BA)]

	// B-A's own population can be shorter than B-B's even with every request delivered: a
	// received request whose wire timestamp validHotPathTS rejects reaches ing.Accept but never
	// hook_controlled. hookControlledShortfall refuses to return a shortfall it cannot account
	// for out of the ledger's deferrals plus the daemon's own hotpath_sample_invalid count.
	baMissing, err := hookControlledShortfall(gated, baSnap.N, gatedSnap.Counters)
	if err != nil {
		return Report{}, err
	}

	baRow, bbRow, daemonNotes := buildDaemonRows(cfg, baSnap, bbSnap, baMissing, gated.Undelivered(), f.underCoload)

	// One limit, read once from obs.Budgets() + config.Defaults() (task-7-brief.md's binding
	// ruling), and applied to BOTH B-E rows: the wall-clock one and the CPU-time one are two
	// measurements of the same §2.4 budget, so they must never be able to drift to two numbers.
	beLimit := budgetLimit(cfg, obs.BE)
	beWallNote := ""
	if f.underCoload {
		beWallNote = beWallWaivedNote(beLimit)
	}

	report := Report{
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		N:        f.iterations,
		BAMethod: bAMethod,
		SpawnFloorMs: SpawnFloor{
			N: spawnFloorIterations, P50: msf(floorP50), P99: msf(floorP99),
		},
		Notes: buildNotes(gatedSnap, f.warmDaemon, f.iterations, ledger,
			append(daemonNotes, beWallNote, ackRTTNote())...),
		Budgets: buildBudgetRows(baRow, bbRow,
			bdSamples.Wall, beSamples.Wall, beSamples.CPU, baSamples, ackRTT, beLimit, !f.underCoload),
	}
	return report, nil
}

// buildBudgetRows assembles the report's budget rows, in the fixed order the artifact publishes
// them. baRow and bbRow arrive already built, because they come from the daemon's own histograms
// through the status op rather than from samples this harness timed itself (buildDaemonRows).
//
// It is a function rather than a literal inside runHarness so that the row set is testable without
// a daemon (review round 1, F6). The out.json golden cannot cover it: that fixture pins the JSON
// shape of a hand-built Report, so a row added to the harness alone never reaches it — which is
// exactly what happened when budgetIDHookAckRTT was added and the six-row golden went on passing.
// TestBuildBudgetRows_PublishesExactlyTheDocumentedRowSet pins the ids, their order, and which of
// them carry a limit, so the next row cannot arrive gated by accident either.
func buildBudgetRows(baRow, bbRow BudgetRow, bdWall, beWall, beCPU, baSpawnEstimate, ackRTT []time.Duration,
	beLimit time.Duration, gateBEWall bool,
) []BudgetRow {
	return []BudgetRow{
		baRow,
		bbRow,
		buildBudgetRow(string(obs.BD), bdWall, 0, false),
		buildBudgetRow(string(obs.BE), beWall, beLimit, gateBEWall),
		// The co-load-immune half of B-E, gated on the same limit and gated ALWAYS: the same
		// children's own user+system CPU time, which a shared runner does not move. See
		// budgetIDBECPU's doc comment (report.go) for the measurements behind that claim, for
		// why the wall-clock row above cannot be priced from the spawn floor instead, and for
		// the one thing a CPU clock cannot see.
		buildBudgetRow(budgetIDBECPU, beCPU, beLimit, true),
		// The wall-clock, floor-subtracted diagnostic — informational only, never gated. See
		// budgetIDBASpawnEstimate's own doc comment (report.go).
		buildBudgetRow(budgetIDBASpawnEstimate, baSpawnEstimate, 0, false),
		// The client-side ACK round trip, reported only: design §7.5's slack99 is this row's
		// p99 minus B-B's. See budgetIDHookAckRTT (report.go).
		buildBudgetRow(budgetIDHookAckRTT, ackRTT, 0, false),
	}
}

// buildDaemonRows builds the two rows sourced from the daemon's own histograms via the status op —
// B-A (ruling #29's hook_controlled estimate) and B-B (l0_ingest) — with their limits read from
// obs.Budgets() + cfg, and returns them with every disclosure note they owe the artifact, in row
// order. underCoload is the one thing that changes their shape, and it changes B-A's only: B-A is
// built REPORTED (LimitMs/Pass nil, exactly B-D's shape) and baWallWaivedNote is appended, because
// reqTS is stamped inside the spawned hook process and a child's scheduling wait on a shared host
// sits inside the interval with no CPU clock to move the judgement to — see the flag's comment in
// parseFlags for the measurements. B-B is gated in both shapes — still true, though no longer for
// the reason it was written for: it has no process boundary inside it, but it does now contain the
// durable path's three flushes, which co-load does move (see parseFlags, and design §7.6's Q3).
// The shortfall accounting is identical in both
// shapes too: tailAdjustedP99 still counts the missing samples back in and the P99 field carries
// the same number the gate would have read, so a reported row can be re-judged from the artifact
// alone.
func buildDaemonRows(cfg config.Config, baSnap, bbSnap obs.HistSnapshot, baMissing, bbMissing int64, underCoload bool) (baRow, bbRow BudgetRow, notes []string) {
	baLimit := budgetLimit(cfg, obs.BA)
	baRow, baNote := buildBudgetRowFromSnapshot(string(obs.BA), baSnap, baLimit, !underCoload, baMissing)
	notes = append(notes, baNote)
	if underCoload {
		notes = append(notes, baWallWaivedNote(baLimit))
	}
	bbRow, bbNote := buildBudgetRowFromSnapshot(string(obs.BB), bbSnap, budgetLimit(cfg, obs.BB), true, bbMissing)
	notes = append(notes, bbNote)
	return baRow, bbRow, notes
}

// shortPIDTag is a short, human-legible discriminator (this process's own pid) folded into the
// bench run's QOMPACK_IPC_ADDR override — not for uniqueness (randomHex already guarantees that)
// but so a stray pipe/socket left behind by a killed run is identifiable in an `ls`/pipe list.
func shortPIDTag() string {
	return fmt.Sprintf("%d", os.Getpid())
}

// writeJSON marshals r as indented JSON (trailing newline) to path.
func writeJSON(path string, r Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("hotpath: marshalling report: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil { //nolint:gosec // an intentional, caller-named artifact path, not a secret
		return err
	}
	return nil
}

// printSummary writes the human-readable table `devtool bench-hotpath` (and a direct `go run`)
// prints, per task-7-brief.md's binding ruling.
func printSummary(w io.Writer, r Report) {
	fmt.Fprintf(w, "\nhotpath bench summary (%s, n=%d)\n", r.Platform, r.N)
	fmt.Fprintf(w, "  spawn floor: p50=%.3fms p99=%.3fms (n=%d)\n", r.SpawnFloorMs.P50, r.SpawnFloorMs.P99, r.SpawnFloorMs.N)
	fmt.Fprintf(w, "  b_a_method: %s\n\n", r.BAMethod)
	for _, b := range r.Budgets {
		status := "reported"
		if b.Pass != nil {
			status = "FAIL"
			if *b.Pass {
				status = "PASS"
			}
		}
		limit := "n/a"
		if b.LimitMs != nil {
			limit = fmt.Sprintf("%.3fms", *b.LimitMs)
		}
		fmt.Fprintf(w, "  %-19s n=%-5d p50=%8.3fms p95=%8.3fms p99=%8.3fms p999=%8.3fms max=%9.3fms limit=%-10s [%s]\n",
			b.BudgetID, b.N, b.P50, b.P95, b.P99, b.P999, b.Max, limit, status)
	}
	if len(r.Notes) > 0 {
		fmt.Fprintln(w)
		for _, n := range r.Notes {
			fmt.Fprintf(w, "  note: %s\n", n)
		}
	}
	fmt.Fprintln(w)
}
