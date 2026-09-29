package config

import "runtime"

// goosWindows and goosDarwin are runtime.GOOS's spellings for Windows and macOS, named so the
// switches below read as platform decisions rather than string comparisons (internal/ipc/resolve.go
// names them the same way, for the same reason).
const (
	goosWindows = "windows"
	goosDarwin  = "darwin"
)

// ConnectDeadlineMsPortable and ConnectDeadlineMsWindows are runtime.daemon.connectDeadlineMs's
// built-in default on, respectively, every platform whose dial enforces the caller's timeout
// itself, and Windows, whose dial does not.
//
// # Why Windows needs its own number
//
// A named pipe whose every instance is currently claimed answers CreateFile with ERROR_PIPE_BUSY
// — the state a listener is in between accepting one connection and creating the next instance.
// go-winio, the only named-pipe client this repository has (§2.5 pins it), answers that with a
// hard-coded `time.Sleep(10 * time.Millisecond)` and re-checks the caller's deadline only at the
// top of the next loop iteration: go-winio@v0.6.2/pipe.go, tryDialPipe — loop at :208,
// ctx.Done() at :210, ERROR_PIPE_BUSY at :224, the sleep at :229. internal/ipc/dial_windows.go
// names that 10 ms dialBusyRetryQuantum, beside the call it describes.
//
// A budget therefore buys CreateFile attempts in whole quanta, and overshoots by up to one:
//
//	budget   attempts   returns at   why
//	 5 ms    1          ~10 ms       the first sleep already outlives the deadline; no retry
//	20 ms    2          ~20 ms       t=10 is still inside the budget, t=20 is not
//	25 ms    3          ~30 ms       t=10 and t=20 are both inside
//
// 5 ms is not merely tight on Windows, it buys exactly one attempt and costs ~10 ms doing it, so
// a client that met a momentarily-busy listener spooled rather than retried. Two boundaries then
// sit above it, and 25 ms is chosen against both. 20 ms — two quanta — is the floor the anti-drift
// guard below enforces, the least budget that can buy a second attempt at all; it is a minimum, not
// a target, because it buys that attempt with nothing to spare. 25 ms is 2.5 quanta: it buys a
// third attempt outright (t=10 and t=20 are both inside the budget), and the half quantum over the
// guard floor is what lets a sleep that overshoots its 10 ms absorb the delay and still retry. At
// exactly 20 ms it could not: a first sleep that ran long would put the next deadline check at or
// past the budget, collapsing the dial to the single attempt the retry was raised to prevent.
//
// TestConnectDeadlineDefaultClearsTheBusyRetryQuantum (internal/ipc) is the anti-drift guard
// between the two constants: internal/config may not import internal/ipc (§3.2 gives config only
// core), so nothing but that test couples this number to the quantum it is derived from. It fails
// if this value ever drops below 2 x dialBusyRetryQuantum, or if the quantum grows past it.
//
// Raising it does not move the B-A hot-path budget (§8.1; HotPathBudgetMsFor below) and is not
// meant to. A successful connect to a warm daemon is microseconds; this deadline caps only the
// rare ERROR_PIPE_BUSY path, which under 5 ms did not retry at all.
const (
	ConnectDeadlineMsPortable = 5
	ConnectDeadlineMsWindows  = 25
)

// connectDeadlineMsDefault is the value Defaults() ships for runtime.daemon.connectDeadlineMs on
// the platform this binary was built for.
//
// The platform is selected here, from runtime.GOOS, rather than by a pair of build-tagged files
// declaring one constant each. Build tags would hide ConnectDeadlineMsWindows from every
// non-Windows build, and two checked-in artifacts generated from Defaults() have to be able to
// name both values from a single host: docs/config-reference.md, whose Default cell must render
// identically wherever `devtool gen-config-docs` runs (CI regenerates and diffs it on ubuntu),
// and testdata/golden/config/schema.json, whose one-number-per-leaf "default" the config golden
// test reconciles against the running platform. Under build tags each of those would need its own
// copy of 25 — precisely the second, driftable spelling of the number that D11 exists to prevent.
func connectDeadlineMsDefault() int {
	if runtime.GOOS == goosWindows {
		return ConnectDeadlineMsWindows
	}
	return ConnectDeadlineMsPortable
}

// L0IngestMsPortable, L0IngestMsWindows and L0IngestMsDarwin are runtime.budgets.l0IngestMs's
// built-in default — the B-B latency budget of 00-ARCHITECTURE.md §2.4 — on, respectively, Linux
// and everything else, Windows, and macOS.
//
// # This number is PURELY A BUDGET
//
// No production path branches on it. Its only consumer is the Limit closure obs.Budgets() attaches
// to B-B (internal/obs/budgets.go), which the hot-path bench harness evaluates through budgetLimit
// (test/bench/hotpath/report.go) to decide whether a run passes. Raising it cannot make a delivery
// slower, a client wait longer, or a hook spool; it changes only what the harness calls a breach.
// Contrast AckDeadlineMsPortable below, which is a real production deadline.
//
// # The measurement (2026-09-13, Windows)
//
// B-B times internal/daemon's ingest.Accept in full, and since f6a8691 made the delivery path
// durable that region contains three flushes per accepted leased delivery — the WAL Sync, the lease
// journal's Sync and the seal (ingest.go's Accept comment enumerates them). The shipped 2 ms
// predates that path and described a region that no longer exists.
//
// SP20-D1 design §7.5's M2 protocol, run on this host on AC power inside an attested quiet window,
// against the shipped deliverySealWriteFormat = 2 build: `devtool bench-hotpath --iterations 2000
// --warm-daemon`, one process at a time.
//
// The protocol asks for three runs. Three runs gave P = 22.528 ms and so L0IngestMs = 30, and an
// acceptance pass at 30 was green three times over. That number did not survive contact with more
// evidence, and the way it failed is the reason this comment is long.
//
// Across the nine runs available at that point the worst B-B p99 was 28.672 ms against a limit of
// 30: a margin of 4.6 %. A gate sitting that close to the worst thing it has ever seen fails on
// host noise, and a gate that fails on host noise gets switched off — which is a worse outcome than
// having no gate, and is the failure mode 00-ARCHITECTURE.md's own bench-compare note describes
// from the other direction. So six further gated runs were taken at the committed 30, under a rule
// fixed BEFORE they ran: all six pass and 30 stands; any one fails and P is re-derived from every
// attested run. One failed, at p99 36.864 ms on a clean window (processor performance 152 %,
// foreign load 5.6 %).
//
// Fifteen runs, twelve of them inside an attested window, give these B-B p99 values in ms.
//
// Three of the fifteen are the acceptance triple taken at the committed 30, and their artifacts no
// longer exist: the acceptance stage was run a second time after the widening, against the new 50,
// and it reuses the names acc-run1..3, so the second stage overwrote the first. The surviving
// scratchpad artifacts are therefore m1-run1..3, m2-run1..3, stress-run4..9 and the acceptance
// triple at 50 — fifteen files, but NOT these fifteen runs. Re-deriving P by globbing whatever
// *.json is on disk silently swaps the acceptance-at-30 values (18.432, 20.480, 24.576) for the
// acceptance-at-50 values (12.288, 20.480, 20.480) and produces a list that disagrees with this
// one. P is the maximum and stays 36.864 ms either way, so the constant does not move; the list
// below is the derivation set as it stood, and this note is here because that substitution was
// actually made once while checking this comment.
//
// The values:
//
//	11.264  11.264  11.264  12.288  13.312  18.432  18.432  20.480
//	20.480  22.528  22.528  24.576  24.576  28.672  36.864
//
// so P — the maximum, per the protocol, now over a sample large enough to contain its own tail — is
// 36.864 ms, and
//
//	L0IngestMs = roundup5(1.25 x P) = roundup5(46.08) = 50
//
// # What this number is, and what it is not
//
// A second, independent instrument agrees on the underlying cost: BenchmarkIngestAcceptLeased's
// median is 7.306 ms, and M0's per-component figures predict 7.2 ms for one uncontended delivery
// (WAL Sync 2.243 + journal Sync 2.197 + seal slot 2.305 + about 0.4 ms of non-flush work). So 50 is
// nearly seven times an uncontended Accept, and none of that gap is slack in the delivery path:
// that path has a ±7 % spread across six runs, while B-B's p50 alone moves between 7.168 and
// 15.360 ms run to run because the harness is starting 2 000 processes beside it — the spawn floor
// is p50 22.3 ms, p99 77.1 ms.
//
// The honest consequence, recorded here so nobody reads more into a green B-B than it carries:
// **B-B is a coarse backstop, not a sensitive regression detector.** It catches a delivery path
// that has become several times slower. It cannot see a 2x regression, because host noise already
// reaches 36 ms. The sensitive instrument is BenchmarkIngestAcceptLeased, whose ±7 % spread would
// show a 2x regression immediately, and the durability invariant is carried by the structural gates
// T9, T10 and T14, which have no clock in them at all. Gating the benchmark is the obvious V6
// follow-up and is not done here.
//
// # Linux and darwin are PROVISIONAL
//
// Neither platform is measurable on the host that produced the Windows number, so both are seeded
// from design §7.5's predicted table (linux 15, darwin 40) and are PROVISIONAL, PENDING CI's
// bench-gate running the same M2 protocol on ubuntu-latest and macos-latest. Until that lands they
// are predictions, not measurements. Design §7.5's own Windows prediction was P = 12-18 ms against
// the 22.528 ms measured here — low — so the linux and darwin rows may well have to rise as well;
// 4043b6a made bench-gate upload its JSON even when the gate fails precisely so a first breach
// still reports the numbers needed to re-price them.
//
// # The first CI figures (run 34797774997 on 69f92a1, 2026-09-14)
//
// That run was the first complete ci.yml run this repository has had, and bench-gate's artifacts
// answered the provisional question in one direction and opened another. Alone on its runner, the
// harness measured B-B p99 4.608 ms on ubuntu-latest (p50 0.896, hook_ack_rtt p99 3.936) and
// 3.072 ms on macos-latest (p50 1.536, hook_ack_rtt p99 4.314). Both provisional limits hold with
// room to spare and are deliberately NOT tightened to the protocol's roundup5(1.25 x P) — 10 and 5
// — on one run: a limit above the measurement is not a weakened gate, and the protocol asks for
// three. The next wave takes those three from CI and re-derives.
//
// Two things the same run showed that the protocol does not price:
//
//   - The hosted Windows runner cannot hold an fsync-bound wall-clock gate. On windows-latest the
//     same row read 73.728 ms in bench-gate (p50 12.288, hook_ack_rtt p99 89.290), 98.304 ms in
//     the timing job (p50 36.864) and 2 359 ms in test-e2e's X11 (p50 40.960, p95 1 442, after
//     thirteen minutes of I/O-heavy tests on the same disk), against 50. A thirtyfold spread on
//     one runner class in one run is a throttled disk, not a delivery path, and it cannot price a
//     constant: the re-derivation rule above is for attested runs, and a number the next job
//     disagrees with by 30x is not one. The constants stay at the fifteen-run figures. Whether
//     hosted Windows runners are a non-reference platform for B-B and B-E's wall row — reported
//     there under ADR 0010's rule, with B-A still gated — is design Q1's third option and the
//     owner's call; it is recorded in plans/V5-report.md §31.10.1 with this recommendation.
//   - AckDeadlineMs is sized from quiet runs and engages the degrade path under ordinary load.
//     Under the whole-tree test job's declared co-load, ubuntu's B-B p99 was 212.992 ms against a
//     17 ms ACK deadline, so the hook client spooled and two tests that wait for every event to
//     reach the store timed out at the 30 s idle-tick drain. That is SP05-D2's complaint arriving
//     on Linux: the Windows figure of 73 covers co-load only because the host it was measured on
//     happened to be noisy. Sizing the deadline for the loaded case is a design change, carried to
//     V6 with these figures.
const (
	L0IngestMsPortable = 15
	L0IngestMsWindows  = 50
	L0IngestMsDarwin   = 40
)

// AckDeadlineMsPortable, AckDeadlineMsWindows and AckDeadlineMsDarwin are
// runtime.daemon.ackDeadlineMs's built-in default on, respectively, Linux and everything else,
// Windows, and macOS.
//
// # This number is a REAL PRODUCTION DEADLINE
//
// Unlike L0IngestMsPortable above, the hot path runs on it: it is how long ipc.Client waits for the
// daemon's one-byte ACK before giving up and spooling the delivery to spool/client-<pid>.ndjson
// (internal/ipc/client.go, reading the AckDeadlineMs field of the 32-byte state record
// internal/ipc/state.go writes from this default). Setting it below the daemon's service time
// loses no data — the daemon has already accepted the delivery — but it makes the hook spool a
// duplicate copy of work that is already done, which the next daemon start has to drain and skip.
// That is CARRIED-DEFECTS.tsv's SP05-D2, measured at 13 % of hooks with the old 8 ms.
//
// # The derivation (2026-09-13, Windows)
//
// Design §7.5: AckDeadlineMs = L0IngestMs + ceil(slack99), where slack99 is the largest per-run
// p99(hook_ack_rtt) - p99(B-B) across the same attested runs L0IngestMsWindows rests on.
// hook_ack_rtt is the reported-only row that times the same delivery from OUTSIDE the daemon
// (test/bench/hotpath/report.go's budgetIDHookAckRTT), so the difference is exactly what this
// deadline must cover beyond the durable path itself: the pipe read, dispatchOp up to Accept, the
// ACK write and the client's wake-up. Measured slack99 = 22.257 ms, so
//
//	AckDeadlineMs = 50 + ceil(22.257) = 73
//
// The largest hook_ack_rtt p99 observed across those runs is 33.666 ms, so 73 clears the worst
// measured round trip by better than a factor of two. That direction is deliberate. A deadline set
// too short does not lose data — the daemon has already accepted the delivery — it makes the hook
// spool a duplicate of finished work, which is SP05-D2 itself; a deadline set too long only delays
// noticing a daemon that is already wedged, and B-A (p99 3-6 ms against the then-universal 15 ms
// budget in these same runs; D41 later derived it per platform, HotPathBudgetMsFor) is what
// actually bounds what a user waits for.
//
// internal/cli's hookSendDeadlineFloor (8 ms) is a different thing and does not move with this: it
// exists so a corrupt or zero-valued state record cannot reach Send as literally 0, and it now sits
// strictly below every platform's value here, so it can only ever bind on a state record that
// carries no deadline at all.
//
// # Linux and darwin are PROVISIONAL
//
// Seeded from design §7.5 (linux 17, darwin 45) and PROVISIONAL, PENDING the same bench-gate
// measurement on ubuntu-latest and macos-latest — see L0IngestMsPortable's note above for why they
// may have to rise.
const (
	AckDeadlineMsPortable = 17
	AckDeadlineMsWindows  = 73
	AckDeadlineMsDarwin   = 45
)

// l0IngestMsDefault is the value Defaults() ships for runtime.budgets.l0IngestMs, and
// ackDeadlineMsDefault the value it ships for runtime.daemon.ackDeadlineMs, on the platform this
// binary was built for.
//
// Both select the platform from runtime.GOOS rather than from build-tagged files, for exactly
// connectDeadlineMsDefault's reason above — and more strongly, because each has three values rather
// than two: docs/config-reference.md and testdata/golden/config/schema.json are generated from
// Defaults() on one host and have to name every platform's value, so under build tags each would
// need its own second, driftable spelling of four more numbers.
func l0IngestMsDefault() int {
	switch runtime.GOOS {
	case goosWindows:
		return L0IngestMsWindows
	case goosDarwin:
		return L0IngestMsDarwin
	default:
		return L0IngestMsPortable
	}
}

func ackDeadlineMsDefault() int {
	switch runtime.GOOS {
	case goosWindows:
		return AckDeadlineMsWindows
	case goosDarwin:
		return AckDeadlineMsDarwin
	default:
		return AckDeadlineMsPortable
	}
}

// HotPathBudgetMsFloor is the least runtime.hotPath.budgetMs ever defaults to: 15 ms, the §8.1 /
// §11.3 L0 figure B-A was written against (00-ARCHITECTURE.md §2.4's B-A row), and the value it
// shipped on every platform until D41. It is not a new number; it is that one, named.
const HotPathBudgetMsFloor = 15

// HotPathBudgetMsFor is D41's derivation (plans/V6-CLOSEOUT-CHECKLIST.md, 2026-09-28) of
// runtime.hotPath.budgetMs's built-in default — the B-A limit — from the same platform's
// runtime.budgets.l0IngestMs default — the B-B limit:
//
//	budgetMs = max(HotPathBudgetMsFloor, l0IngestMs)
//
// so 15 on Linux (L0IngestMsPortable), 50 on Windows (L0IngestMsWindows) and 40 on macOS
// (L0IngestMsDarwin). It composes two approved numbers and introduces none.
//
// # Why B-A may not be tighter than B-B
//
// The B-A sample the daemon gates and feeds to the §8.1 breach detector is (recvTS - reqTS) +
// handler time + a 1 ms tail allowance (internal/daemon/handlers.go, recordHotPathSample), and
// since the delivery path became durable the handler time IS B-B's region: the ACK is written only
// after ingest.Accept's WAL, lease-journal and seal flushes. B-A therefore contains B-B by
// construction. When the V5 ruling kept fsync-before-ACK and re-budgeted B-B per platform (the
// L0IngestMs note above), B-A stayed at 15 everywhere, which made a delivery inside its own B-B
// budget a B-A breach. On Windows that was every real session: isolated Phase 3 runs of the frozen
// candidate measured B-A p50 15.4-16.4 ms and p99 22.5-26.6 ms against B-B p99 12.3 ms, and the
// detector moved the daemon to spool submode — degraded WARN and LOUD.log — after exactly
// 3 x 512 samples, so any session past ~1.5k tool uses ran degraded.
//
// # What it does not do
//
// It derives only the DEFAULT, and only from the platform's l0IngestMs DEFAULT. A budgetMs the
// user sets is applied exactly as written (Load never re-derives it), and a user-set l0IngestMs
// does not move budgetMs: the two keys stay independent, as BudgetsCfg's doc comment requires for
// every budget. Where B-B is re-priced (the provisional linux and darwin rows above), B-A follows
// automatically, which is the point of deriving it rather than spelling three more numbers.
func HotPathBudgetMsFor(l0IngestMs int) int {
	return max(HotPathBudgetMsFloor, l0IngestMs)
}

// hotPathBudgetMsDefault is the value Defaults() ships for runtime.hotPath.budgetMs on the
// platform this binary was built for: HotPathBudgetMsFor applied to l0IngestMsDefault, selected
// from runtime.GOOS for connectDeadlineMsDefault's reason above.
func hotPathBudgetMsDefault() int {
	return HotPathBudgetMsFor(l0IngestMsDefault())
}
