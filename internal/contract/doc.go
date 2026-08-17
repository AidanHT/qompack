// Package contract implements the host-contract monitor of 00-ARCHITECTURE.md §5.19 and §12.1: the
// mechanism that closes G9.3 by asserting, on every SessionStart, that the undocumented Claude Code
// hook contracts Qompack depends on still hold — and that degrades loudly to passive recording
// rather than failing silently when one of them does not.
//
// contract may import ONLY hookio and store, plus the foundation packages core, paths, config,
// logging and obs (00-ARCHITECTURE.md §3.2's contract allow-set). Env carries a store.Store so an
// assertion can read what L0 actually recorded, and a hookio.Event so it can inspect the payload
// that triggered the run; neither is dialled, fetched, or otherwise reached over any transport.
//
// # What SP-01 ships for real, and why
//
// This is the one package in wave 0 whose stub *behaviour* is load-bearing. §12.1 requires that an
// assertion whose producer is absent from the build reports OK/SevInfo — never a degradation — and
// that "a CI test asserts a freshly built develop reports ModeFull". Wiring that wrong would put
// every wave-1 and wave-2 verification run into degraded-passive and silently disable the very
// paths those waves are testing. So SP-01 ships the monitor MECHANICS for real:
//
//   - NewMonitor, and the whole Monitor state machine: Register, RunAll, Mode, Degrade, Restore
//     and Report, including §12.1's "SevCritical failure degrades" and "two consecutive clean runs
//     restore" transitions, the state/contract.json persistence, and the logging.Loud emission that
//     makes both transitions impossible to miss.
//   - Mode.String, whose three strings ("full", "degraded-passive", "off") are the ones §12 uses in
//     prose and /qompack:status prints, frozen here.
//   - StandardAssertions, whose nine entries carry their real, DECLARED severities but whose Check
//     functions all report the not-yet-implemented result §12.1 mandates.
//
// # What SP-05 replaced
//
// SP-05 owns the assertion *observations*: every Assertion.Check in StandardAssertions is now a
// real measurement against the host (producers.go, marker.go, sentinel.go, history.go,
// assertions.go) — session_start.fires reads run/marker.json, hook.payload_shape inspects the
// actual Event, precompact.has_time_to_write computes a p99 against the manifest timeout, and so
// on. What SP-05 did NOT replace is §12.1's not-yet-implemented rule itself: every Check is wrapped
// in gated (assertions.go), which still reports OK/SevInfo/"not-yet-implemented" — never running the
// real Check at all — for any assertion whose producer has not called DeclareProducer
// (producers.go). SP-05 also ships the package's first History implementation, SessionHistory
// (history.go): the cross-session, persistent record several of these Checks read and mutate. Its
// wire shape (state/history.json) is frozen by the history_degraded golden fixture; see
// SessionHistory's own doc comment for what that means for a future change.
package contract
