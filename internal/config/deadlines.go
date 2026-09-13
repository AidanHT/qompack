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
// Raising it does not move the B-A hot-path budget (§8.1, 15 ms) and is not meant to. A
// successful connect to a warm daemon is microseconds; this deadline caps only the rare
// ERROR_PIPE_BUSY path, which under 5 ms did not retry at all.
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
// --warm-daemon`, three times, one process at a time. B-B p99 came back 22.528, 20.480 and
// 11.264 ms, so P — the maximum of the three, per the protocol — is 22.528 ms, and
//
//	L0IngestMs = roundup5(1.25 x P) = roundup5(28.16) = 30
//
// A second, independent instrument agrees on the underlying cost: BenchmarkIngestAcceptLeased's
// median is 7.306 ms, and M0's per-component figures predict 7.2 ms for one uncontended delivery
// (WAL Sync 2.243 + journal Sync 2.197 + seal slot 2.305 + about 0.4 ms of non-flush work). 30 ms
// is therefore about 4x an uncontended Accept; the gap is the p99 tail, not slack in the budget.
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
const (
	L0IngestMsPortable = 15
	L0IngestMsWindows  = 30
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
// p99(hook_ack_rtt) - p99(B-B) across the same three M2 runs L0IngestMsWindows rests on.
// hook_ack_rtt is the reported-only row that times the same delivery from OUTSIDE the daemon
// (test/bench/hotpath/report.go's budgetIDHookAckRTT), so the difference is exactly what this
// deadline must cover beyond the durable path itself: the pipe read, dispatchOp up to Accept, the
// ACK write and the client's wake-up. Measured slack99 = 22.257 ms, so
//
//	AckDeadlineMs = 30 + ceil(22.257) = 53
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
	AckDeadlineMsWindows  = 53
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
