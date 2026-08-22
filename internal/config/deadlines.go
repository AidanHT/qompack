package config

import "runtime"

// goosWindows is runtime.GOOS's spelling for Windows, named so the switch below reads as a
// platform decision rather than a string comparison (internal/ipc/resolve.go names it the same
// way, for the same reason).
const goosWindows = "windows"

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
