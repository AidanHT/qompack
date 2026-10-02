# Wave 18 ssfires (live finding F-C48-1)

Branch `closeout/w18-ssfires`. Workflow `wf_3a6c59cd-17f`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `c0a91cec`

### Root cause

The marker is written only by the two terminal hooks, SessionEnd and PreCompact (internal/contract/marker.go WriteMarker). During a compaction, PreCompact rewrites run/marker.json to name the current session, and then SessionStart(source=compact) arrives for that same session. In checkSessionStartFires (internal/contract/assertions.go), a marker counts as found only when it names a different session. A marker naming this session fell through to the absence path. The LastSessionID guard correctly stopped the increment, so starts_without_marker stayed 0, but the function still returned Observed "marker-absent-once". That spelling is in pendingSpellings (refresh.go), so StandingOf counted it as pending and the status banner showed "1 pending" on a healthy store. The first session's own compaction and a --resume that keeps the session id hit the same path.

### Summary

PRODUCT CODE CHANGED: candidate 8 needed

F-C48-1 is fixed in one commit, c0a91cec, on closeout/w18-ssfires (based on 79025ae3). After a compaction or a same-id resume, `qompack status` now reads session_start.fires as holding instead of pending.

Fix (internal/contract/assertions.go, checkSessionStartFires): I added one branch after the existing two-session failure check. When all of these are true, the start returns the new Observed value "same-session-restart" (OK):
- the marker was read and names this session
- LastSessionID is this session
- StartsWithoutMarker is 0
This branch changes nothing in the history: no count, no reset, no LastSessionID write. The first-session, marker-found, empty-ProjectRoot and increment paths are unchanged, and LastSessionID is still written only by this Check.

Two decisions:
- **A counted absence stays pending.** If this session's own start found no marker (count 1), its later compaction still reads "marker-absent-once". That absence really is still waiting for the next session's start to settle it, so calling it holding would hide a real pending state. The brief can be read as asking for holding whenever the marker names this session; I chose this narrower reading on purpose.
- **A failure stays a failure.** A session already failing (count 2) still fails when it compacts.

Classification: "same-session-restart" is added to observedSpellings in internal/contract/observation.go. StandingOf therefore counts it as holding and ClassifyResult as observed, and the drift guard that checks every literal in assertions.go has an entry. The banner renderer (internal/commands/render.go) already uses contract.StandingOf, and doctor reads the same classification through the observation ledger, so neither renderer needed a change.

Docs: in docs/troubleshooting.md section 1 (the `qompack status` part), I added text explaining when session_start.fires reads marker-absent-once and when it reads same-session-restart (holding). The text now also says "0 pending" stays true after a compaction or a `--resume` of the session. I also updated the CSessionStartFires comment in internal/contract/ids.go. docs/commands.md and docs/architecture.md do not list this assertion's observed values, and the gen-*-docs --check commands report the generated docs up to date, so I regenerated nothing.

Tests: the new file is internal/contract/session_start_restart_test.go.
- TestSessionStartFires_CompactOfSameSessionHolds: startup finds the previous session's marker, then two compactions each read same-session-restart and holding with count 0, and the next session reads marker-found.
- TestSessionStartFires_CompactOfFirstSessionHolds: a compaction in the project's very first session.
- TestSessionStartFires_ResumeOfSameSessionHolds: a resume after the session's own SessionEnd.
- TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour: a new session with no marker reads marker-absent-once (pending, count 1); its own compaction stays pending without a recount; two consecutive absent sessions fail at SevCritical; and the failing session's compaction stays failing.

Before the fix, the first three tests failed, each reading "marker-absent-once" where "same-session-restart" was expected. The fourth passed before and after, because it pins behaviour that must not change. I also added the new spelling as a row in the observation_test.go classification table (expects observed) and the refresh_test.go standing table (expects holding).

Machine limits were respected: -p 2, one go test process at a time, no -race, no timing rows, no whole e2e or integration suite.

### Commits

- c0a91cec fix(contract): read a same-session restart as holding

### Tests

- `go test -p 2 -count=1 -run 'TestSessionStartFires_CompactOfSameSessionHolds|TestSessionStartFires_CompactOfFirstSessionHolds|TestSessionStartFires_ResumeOfSameSessionHolds|TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour' ./internal/contract/ (before fix)` — FAIL: three tests failed with marker-absent-once where same-session-restart was expected; GenuineAbsenceKeepsItsBehaviour passed (it pins unchanged behaviour)
- `go test -p 2 -count=1 -run 'TestSessionStartFires_CompactOfSameSessionHolds|TestSessionStartFires_CompactOfFirstSessionHolds|TestSessionStartFires_ResumeOfSameSessionHolds|TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour' -v ./internal/contract/ (after fix)` — PASS, all 4
- `go test -p 2 -count=1 ./internal/contract/... ./internal/commands/` — ok contract, contract/contracttest, commands
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok (91.3s)
- `go test -p 2 -count=1 -run '^(TestMarkerIsWrittenByFlushAndCheckpointOnly|TestSessionStartReplay_MintsNoProbeAndAnswersNothing|TestSessionStartReplay_NeverDegradesTheProjectTwoPromptsLater|TestSessionStartReplay_WithdrawsTheProbeItsLostAnswerCarried|TestSessionStartReplay_LeavesTheDegradeBannerToTheNextLiveStart|TestSessionStartReplay_OwesTheBannerItsLostAnswerCarried|TestSessionStartReplay_AnEarlierSessionsProbeIsNotMissedByTheReplayedSession)$' -v ./internal/daemon/` — PASS, all 7
- `go test -p 2 -count=1 -timeout=30m -run '^(TestE2ESelfTestExitsNonZeroOnCritical|TestE2ESelfTestExitsZeroOnHealthy|TestSelfTestIsTheOnlyNonZeroExit|TestV5_EveryContractAssertionHasARealProducer)$' -v ./test/e2e/` — PASS, all 4 (the e2e rows that read session_start.fires)
- `go vet ./internal/contract/... ./internal/commands/ ./internal/cli/ ; GOOS=linux go vet (same packages)` — clean on Windows and linux
- `go run ./tools/devtool fmt-check` — exit 0
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/contract/... ./internal/commands/...` — exit 0, no issues
- `go test -p 2 -count=1 ./test/docs/` — ok
- `go run ./tools/devtool gen-config-docs --check; gen-mcp-docs --check; gen-command-docs --check` — all three up to date, exit 0

### Criterion changes

- docs/troubleshooting.md section 1 (qompack status) now names the observed value same-session-restart (holding) for session_start.fires and says 'none failing ... 0 pending' holds after a compaction or a --resume of the session.

### Open issues

- Not pushed and not merged into closeout/integration; the coordinator batches it into candidate 8.
- Not re-verified live. A candidate 8 live row with a mid-session /compact should confirm the banner reads '0 pending' and that session_start.fires reads same-session-restart.
- Whether Claude Code 2.1.280 keeps the session id on --resume was not checked live. The doc text is conditional ('a --resume that keeps the session id'). If resume mints a new id, that start reads marker-found as before.
- A same-session start that finds NO marker while the count is 0 (the marker deleted by hand mid-session) still reads marker-absent-once without counting. I left it alone because the brief limits the fix to a marker that names this session.
- golangci-lint ran on Windows only (the pinned module); linux coverage is from GOOS=linux go vet.

## Independent review

### review:ssfires: sound

- **minor** `test/e2e/v5_x14_test.go:360-371` — Only unit tests in internal/contract cover the fix. In the real daemon, PreCompact writes run/marker.json (handlers.go:1364/1524) and then RunAll runs for SessionStart(source=compact) (handlers.go:1030). The new same-session-restart reading depends on that order, and no daemon or e2e row pins it. TestV5_EveryContractAssertionHasARealProducer already drives exactly this sequence (session A first-session, PreCompact, then a compact restart). It checks every other assertion on the compact run but never checks CSessionStartFires.
  - Evidence: x14 asserts first[CSessionStartFires]=first-session (line 358) and next[CSessionStartFires]=marker-found (line 387), but has no compact[CSessionStartFires] assertion. The implementer ran the test and it passed, but it pins nothing about F-C48-1. test/e2e is outside the implementer's owned files.
  - Fix: Have the coordinator, or a follow-up owner of test/e2e, add these assertions in the 'Run 2 (the compact restart)' block: require.Equal(t, "same-session-restart", compact[contract.CSessionStartFires].Observed) and require.Equal(t, contract.OutcomeObserved, compact[contract.CSessionStartFires].Outcome). The candidate 8 live /compact row should also confirm the banner reads '0 pending'.
- **nit** `docs/troubleshooting.md:154-156` — The new doc sentence says without qualification that a compaction's own start, or a same-id --resume, reads same-session-restart. The code (assertions.go:121) returns it only when StartsWithoutMarker == 0. A session whose own startup counted an absence keeps reading marker-absent-once (pending) after it compacts. The implementer chose that behaviour on purpose, and TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour pins it.
  - Evidence: Doc text: 'A compaction's own start, or a `--resume` that keeps the session id, finds the marker that session's PreCompact or SessionEnd just wrote and reads `same-session-restart`, which holds'. Code condition: h.StartsWithoutMarker == 0 && err == nil && rec.Session == e.Event.SessionID && h.LastSessionID == e.Event.SessionID.
  - Fix: Qualify the sentence, e.g. '... reads `same-session-restart`, which holds, unless that session's own start already read `marker-absent-once`; that row stays pending until the next session's start decides it.'

## Fix seat (review resolution) — status `done`, head `86de2648`

### Root cause

Only two terminal hooks write run/marker.json: SessionEnd and PreCompact (internal/contract/marker.go WriteMarker). During a compaction, PreCompact rewrites the marker so it names the current session. SessionStart(source=compact) for that same session then arrives. checkSessionStartFires (internal/contract/assertions.go) counted a marker as found only when it named a different session, so this case fell through to the absence path. The LastSessionID guard correctly skipped the increment, and starts_without_marker stayed 0. Even so, the function returned Observed "marker-absent-once". That value is in pendingSpellings (refresh.go), so StandingOf counted the row as pending and the banner read "1 pending" on a healthy store. This happens in practice because the daemon bumps SessionCount on every start (handlers.go:1127). The compact start of the project's first session therefore takes the same path rather than the first-session branch.

### Summary

PRODUCT CODE CHANGED: candidate 8 needed

F-C48-1 is fixed on closeout/w18-ssfires, which now holds two commits on top of 79025ae3. Head is 86de2648. Nothing is pushed or merged; the coordinator batches it into candidate 8.

**The fix (c0a91cec, implementer, unchanged by me):** checkSessionStartFires now returns a new Observed value, "same-session-restart" (OK), when all of these hold:
- the marker was read and names this session;
- LastSessionID is this session;
- StartsWithoutMarker is 0.

That path changes nothing in the history: no count, no reset, no write. The value is listed in observedSpellings, so StandingOf reads it as holding and ClassifyResult as observed. The renderers (internal/commands/render.go, and doctor through the observation ledger) already use that classification, so neither changed. docs/troubleshooting.md section 1 now names the new value and says "0 pending" stays true after a compaction or after a --resume that keeps the session id. Unit tests are in internal/contract/session_start_restart_test.go. The genuine absence cases behave as before:
- a new session with no marker reads marker-absent-once (pending) and counts once;
- two consecutive absent sessions fail;
- a counted absence stays pending when that session compacts;
- a failing session stays failing when it compacts.

## Review resolution

**Finding 1 (minor; no daemon or e2e row pins the fix at test/e2e/v5_x14_test.go): CORRECT, fixed in 86de2648.**

I checked the claim myself:
- PreCompact writes the marker before RunAll runs for the compact SessionStart.
- The daemon bumps SessionCount on every start (internal/daemon/handlers.go:1127), so session A's compact start has Sessions()=1 and takes the new branch, not first-session.
- x14 drove exactly this order but never asserted CSessionStartFires on the compact run.

What I added to TestV5_EveryContractAssertionHasARealProducer:
1. **The live symptom:** a status read (e2eStatus) between the compaction and session B. Its session_start.fires row must read same-session-restart, and StandingOf must be StandingHolding. This is the point where the live lane read status. contractSnapshot is read-only (LoadHistory plus RefreshFromHistory, no save), so the extra read does not change the rest of the run.
2. **The ledger record:** the compact run's ledger row must have Outcome == OutcomeObserved and Observed == "same-session-restart".

Red first: with internal/contract/assertions.go temporarily set back to its 79025ae3 version, the row failed at v5_x14_test.go:339, expected "same-session-restart", actual "marker-absent-once". After restoring the file it passed.

Ownership: test/e2e was not in my owned list. I took x14 to fall under "their tests", because it is the end-to-end test of every contract assertion's producer. The change adds assertions only; nothing was loosened. The coordinator can drop 86de2648 if that reading is wrong.

Machine limits were respected: -p 2, one go test process at a time, no -race, no timing rows, and only exact-name e2e rows.

### Commits

- c0a91cec fix(contract): read a same-session restart as holding
- 86de2648 test(e2e): pin the compact restart's session_start.fires

### Tests

- `go test -p 2 -count=1 -timeout=30m -run '^TestV5_EveryContractAssertionHasARealProducer$' ./test/e2e/ (with internal/contract/assertions.go temporarily set to its 79025ae3 version)` — FAIL (red as intended): v5_x14_test.go:339 expected "same-session-restart", actual "marker-absent-once"; file restored afterwards
- `go test -p 2 -count=1 -timeout=30m -run '^(TestV5_EveryContractAssertionHasARealProducer|TestE2ESelfTestExitsNonZeroOnCritical|TestE2ESelfTestExitsZeroOnHealthy|TestSelfTestIsTheOnlyNonZeroExit)$' -v ./test/e2e/` — PASS, all 4
- `go test -p 2 -count=1 ./internal/contract/... ./internal/commands/ ./test/docs/` — ok contract, contract/contracttest, commands, test/docs
- `go vet ./internal/contract/... ./internal/commands/ ./test/e2e/ ; GOOS=linux go vet (same packages)` — clean on Windows and linux
- `go run ./tools/devtool fmt-check` — exit 0
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/contract/... ./test/e2e/...` — exit 0, no issues
- `go run ./tools/devtool gen-config-docs --check; gen-mcp-docs --check; gen-command-docs --check` — all three exit 0 (up to date)
- `(implementer, carried) go test -p 2 -count=1 -run 'TestSessionStartFires_CompactOfSameSessionHolds|TestSessionStartFires_CompactOfFirstSessionHolds|TestSessionStartFires_ResumeOfSameSessionHolds|TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour' ./internal/contract/` — red before c0a91cec for the first three (marker-absent-once), all 4 PASS after
- `(implementer, carried) go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok (91.3s); not re-run by me, since 86de2648 touches only test/e2e

### Criterion changes

- docs/troubleshooting.md section 1 (qompack status) now names the observed value same-session-restart (holding) for session_start.fires and says 'none failing ... 0 pending' holds after a compaction or a --resume of the session.
- TestV5_EveryContractAssertionHasARealProducer (V5 §4.14 row) additionally requires the compact restart's session_start.fires to be observed as same-session-restart, and a status read after the compaction to count it as holding.

### Open issues

- Not pushed and not merged into closeout/integration; the coordinator batches c0a91cec and 86de2648 into candidate 8.
- Not re-verified live: a candidate 8 live row with a mid-session /compact should confirm the banner reads '0 pending' and that session_start.fires reads same-session-restart.
- Whether Claude Code 2.1.280 keeps the session id on --resume is unverified live; the doc text is conditional. If resume mints a new id, that start reads marker-found as before.
- A same-session start that finds no marker with the count at 0 (the marker deleted by hand mid-session) still reads marker-absent-once without counting; it was left alone because the fix is scoped to a marker that names this session.
- golangci-lint ran on Windows only (pinned module); linux coverage is from GOOS=linux go vet.
- 86de2648 edits test/e2e/v5_x14_test.go, outside the listed owned files, read as 'their tests'; the coordinator may drop it if that reading is wrong.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


