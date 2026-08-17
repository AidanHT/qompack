# Task 4 review — contract monitor: producers, history, marker, sentinel, real G9.3 assertions

Reviewed commit: `506baa01` (single commit, range `eeca0651..506baa01`).
Reviewer ran, read-only: `go test ./internal/contract/... -race -count=2`, `go test ./test/guards/...`,
`go test ./internal/testutil/...`, `go test ./internal/contract/contracttest/... -run TestRunHistorySuite -v`,
`go vet ./internal/contract/...`, `go run ./tools/devtool lint`, `go run ./tools/devtool fmt`,
`GOOS=linux go build ./...`, `git show -s --format=%B 506baa01`, `git diff --stat eeca0651 506baa01`.
All green; lint reports `PASS golangci-lint / nomagic / importgraph / testdeps / bindeps / sleepcheck / stubskips`;
`fmt` produced no diff; the Linux cross-build is clean.

---

## Verdict 1 — SPEC COMPLIANCE

### Commit hygiene

| Item | Result |
|---|---|
| Exactly one commit in range | **Met.** `506baa0` only. |
| Subject | **Met.** `feat(contract): G9.3 assertions, fail-loud degradation, two-clean-run restore` — the brief's spelling (the spec's own "Commit 5" block says "G9.3 assertion set"; the brief's wording wins per the BRIEF-over-spec rule and is what shipped). |
| Body | **Met.** Byte-identical to the brief's block, `Refs: SP-05, G9.3, §9, §12` present. |
| Attribution trailers | **Met.** None. `git show -s --format=%B` shows subject, body, Refs, nothing else. |

### Deliverables (spec's `internal/contract/*` section)

| Deliverable | Result |
|---|---|
| `producers.go` — mutex-guarded `map[ID]bool`, `DeclareProducer`/`HasProducer`/`ResetProducers`, idempotent | **Met.** `producers.go:9-44`. Idempotence pinned by `producers_test.go`. |
| `history.go` — `LoadHistory`/`SaveHistory`, zero-value-with-Version-1 on any error, `WriteAtomic`, `Last` capped at 9 | **Adapted with ruling (brief).** Path is `state/history.json` via `HistoryPath` (`history.go:191-193`), not the spec's `state/contract.json` — exactly the brief's binding ruling, and pinned by `TestHistoryPath_IsStateHistoryJSON`. `LoadHistory` swallows every error and returns `Version: 1` (`history.go:199-213`). `SaveHistory` uses `paths.WriteAtomic(p, b, 0o600)` and MkdirAll's the parent (`history.go:217-232`). `Last` capped at 9 (`RecordLast`, `history.go:165-171`). |
| `SessionHistory` struct with the brief's named JSON tags + `SessionCount int json:"sessions"` + `Seen map[ID]core.UnixMilli json:"seen,omitempty"` | **Met.** All eighteen named tags present and correctly spelled (`history.go:58-106`); `SessionCount`/`Sessions()` collision avoided as instructed; `Seen` backs `Saw`/`LastSeen`/`Record`. |
| `History` stays the shipped interface; `*SessionHistory` is its first concrete impl; `contracttest.RunHistorySuite` runs against it with zero Rule W-1 skips | **Met and verified.** `assertion.go`'s `History` interface is untouched in the diff. `contracttest/suite_test.go:160-168` adds `TestRunHistorySuite_AgainstSessionHistory`; verbose run shows `shape` **and** `behaviour` blocks executing (4 sub-tests) with no SKIP. The remaining `TestRunHistorySuite_StubIsSkipped` skip is the suite's own deliberate proof that the Rule W-1 probe still discriminates — correct to keep. |
| `marker.go` — `MarkerPath` = `<root>/.qompack/run/marker.json`, `WriteMarker` writes `{"session","ts"}` via `WriteAtomic` | **Met.** `marker.go:25-50`; shape pinned byte-wise by `TestWriteMarker_RoundTripsThroughReadMarker`. |
| `sentinel.go` — `MintSentinel` (`"qompack-contract-" + HashBytes("qompack.sentinel.v1", sess+ts).Short()`), `RenderSentinel`, `ScanTranscriptTail` seek-to-tail + `bytes.Contains`, missing file → `(false, err)` | **Met.** `sentinel.go:30-77`. `core.Hash.Short()` verified at `internal/core/hash.go:72` to be exactly 12 hex chars, so the brief's fallback clause was not needed. Render string exact. |
| `monitor.go` RunAll step 1 (`"off"` → `(nil, ModeOff)`, zero assertions) | **Met.** `monitor.go:189-191`, before clock/log defaults; proved by a counting Check in `TestRuntimeModeOffShortCircuits`. See Important I4 for a residual gap in `Mode()`. |
| step 2/3 (`"passive"`/`"full"` run-then-force) | **Met.** `monitor.go:246-261` + `Mode()` at 279-286, via a new `forcedMode *Mode` field. Choice documented at `monitor.go:80-86`. |
| step 4 (per-assertion `recover`, `OK:false/SevWarn/"assertion panicked"`, never degrades) | **Met.** `runAssertion`, `monitor.go:270-277`. |
| step 5 (persist History, `obs.Counter("contract.fail."+id)`, `obs.Gauge("contract.mode")` inside RunAll) | **Adapted with ruling (brief).** The brief narrows RunAll to "exactly two capabilities" and assigns persistence to the daemon. Persistence correctly deferred. **But the per-failure `contract.fail.*` counter and the per-run `contract.mode` gauge are now unowned by any task** — see Minor M6; this needs a controller decision, not a code change here. |
| SP-01 mechanics (`Register`/`Degrade`/`Restore`/`Report`/`persist`/`load`/`stateLocked`/`degradeReason`/`failedSummary`/`latestTS`) untouched | **Met.** `git diff` on `monitor.go` touches only the new field, the RunAll extensions and `runAssertion`. Every-critical-run re-loud `Degrade` kept (`monitor.go:231-241` comment + behaviour). |
| `assertions.go` — nine real Checks | **Met with one silent drop** — see the table below and Important I3. |
| `gated` wrapper exactly per spec, short-circuiting before the body, all nine constructed through it | **Met.** `assertions.go:36-54` matches the spec's code block line for line (`r.ID = id`; `if r.Severity == 0 && !r.OK { r.Severity = sev }`). `standard.go:22-32` routes all nine through it. Short-circuit proven with a counting closure in `gated_test.go:22-45` and again for `CAdditionalContext` at `gated_test.go:51-72`. |
| Declared severities unchanged (`CPreCompactTiming` SevWarn, `CMCPRegistered` SevInfo) | **Met.** `standard.go` diff changes only the constructor, not IDs/severities/descriptions/order; `standard_test.go`'s two pins are untouched and pass. |
| `notYetImplementedObserved` literal kept | **Met.** `standard.go:6`, still `"not-yet-implemented"`; `test/guards/contract_test.go` matches it literally and passes. |
| Fixtures `history_degraded.json`, `transcript_with_sentinel.jsonl` under the contract package's golden convention | **Met.** `testdata/golden/contracts/contract/want/` and `input/`, mirroring how `golden_test.go` locates `result_set.json` (direct relative path from the package dir). Both declared `frozen` in `MANIFEST.json`. |
| Import discipline (foundation + hookio + store; no `net`, no `os/exec`; no `time.Sleep` outside test/bench) | **Met.** `assertions.go` adds only `reflect`/`sort`/`strconv`/`strings`/`os`/`encoding/json`/`errors`/`path/filepath` + `core`/`hookio`/`paths`. `devtool lint` `importgraph` and `sleepcheck` both PASS. |
| `contract.go` (spec checklist) | **N/A.** SP-01 already ships the equivalent surface across `assertion.go`/`ids.go`/`mode.go`/`severity.go`; adding an empty `contract.go` would have been churn. Not a gap. |

### The nine-assertion table, row by row

| ID | Spec trigger / observation | Shipped | Mutation | Severity |
|---|---|---|---|---|
| `session_start.fires` | marker exists & session differs → OK; absent → `StartsWithoutMarker++`, fail at ≥2; `Sessions()==0` → OK `"first-session"` | **Met** (`assertions.go:78-101`) | `StartsWithoutMarker++` per table, **plus an undocumented reset-to-0 on a hit** (line 89) — see adjudication (a) | Declared SevCritical, filled in by `gated`'s zero-severity fallback. Correct. |
| `session_start.source_compact` | awaiting → `Source=="compact"`? OK : fail `Expected:"compact"`; clears the flag either way; not awaiting → OK `"no-precompact-pending"` | **Met** (`assertions.go:107-121`) | clears `AwaitingCompactStart` on both arms (line 116, before the branch) | Correct. |
| `hook.additional_context_delivered` | `Sentinel.Observed` → OK; `Chances < 2` → OK `"not-yet-observed"`; `Chances >= 2` → fail | **Met** (`assertions.go:127-140`) | **none** — `Chances` tracking is in `SessionHistory.RecordSentinelScan` (`history.go:179-186`), per the brief's ruling. Correct. | SevCritical via fallback; pinned by `TestSentinelNotObservedGivesTwoChances`. |
| `precompact.has_time_to_write` | p99 of 64 newest vs timeout: ≥100% → SevCritical fail; >60% → SevWarn fail; `TimeoutMs==0` → OK `"timeout-unknown"` | **Met** (`assertions.go:151-174`), thresholds as constants; observed SevCritical explicitly set, exceeding the declared SevWarn as the documented design | none | Correct; `TestCriticalFailureDegrades` proves the SevWarn-declared / SevCritical-observed asymmetry degrades. |
| `precompact.custom_instructions_accepted` | empty → OK `"no-instructions-emitted"`; else **first line of ≥ 24 characters** as probe, `ScanTranscriptTail(…, 256<<10)`; found → OK, absent → warn `"instruction phrase not found in transcript tail"` | **Partially met** — the `≥ 24 characters` selection rule is **silently dropped**; `customInstrMinPhraseChars` is declared at `assertions.go:183` and **never read**. See Important I3. | none | SevWarn via fallback; Observed string exact. |
| `hook.payload_shape` | `HookEventName != ""`, `SessionID != ""`, (`CWD != ""` or `TranscriptPath != ""`), no `Extra` key collides with a known field | **Met** (`assertions.go:242-264`); known-field set built by reflection over `hookio.Event` | none | Correct. (See Minor M10 on the collision arm's reachability.) |
| `mcp.server_registered` | `History.MCPInitialized` | **Met** (`assertions.go:270-280`) | none | Declared SevInfo preserved. |
| `transcript.readable` | non-empty path, `os.Stat` succeeds, last non-empty line parses as JSON; empty → OK `"no-transcript-path"` | **Met** (`assertions.go:289-306`), bounded to a 64 KiB tail | none | Correct. |
| `plugin.root_resolves` | env unset → OK `"unset"`; set → dir contains `bin/qompack` or `bin/qompack.exe` | **Met** (`assertions.go:314-327`) | none | Correct. |

**Nil tolerance** (spec's "Nil tolerance in `Env`" paragraph): `Env.Store` is never dereferenced; `Env.Clock == nil` falls back to `core.SystemClock()` both in `RunAll` and per-Check via `now(e)` (`assertions.go:24-29`); a non-`*SessionHistory` or nil `History` yields `noObservationYet` (OK) rather than a panic (`historyOf`, `assertions.go:68-71`, correctly handling the typed-nil case). **One hole:** an empty `Env.ProjectRoot` is *not* treated as "no observation yet" — see Important I2.

### Contract test table, row by row

| Spec row | Status |
|---|---|
| `TestFreshBuildReportsModeFull` | **Met** — `assertions_test.go:70`. Declares only the five wave-1 producers; asserts `ModeFull`, 9 results, and OK/SevInfo/`"not-yet-implemented"` for all four later-wave IDs. |
| `TestDeclaredProducerSetMatchesArchitecture` | **Correctly deferred** to the next task per the brief. Absent from the diff; not claimed in the report. No leak. |
| `TestCriticalFailureDegrades` | **Met** — `assertions_test.go:103`; asserts mode, exactly one Loud line, `reason` and non-zero `since` in `state/contract.json`. |
| `TestTwoCleanRunsRestore` | **Met** — `assertions_test.go:131`. |
| `TestOneCleanRunDoesNotRestore` | **Met** — `assertions_test.go:151`. |
| `TestRuntimeModeOffShortCircuits` | **Met** — `assertions_test.go:166`, counting Check asserts zero executions. |
| `TestRuntimeModePassiveForces` | **Met** — `assertions_test.go:188`; also asserts `Mode()` reflects the force. `TestRuntimeModeFullForces` added as the symmetric case (not required, welcome). |
| `TestPanickingAssertionDoesNotDegrade` | **Met** — `assertions_test.go:219`. |
| `TestSessionStartFiresNeedsTwoMisses` | **Met** — `assertions_test.go:240`. |
| `TestSourceCompactEvaluatedOnFollowingStart` | **Met** — `assertions_test.go:269`; asserts the flag is cleared. |
| `TestSentinelRoundTrip` | **Met** — `sentinel_test.go:21`, against the new frozen transcript fixture. |
| `TestSentinelNotObservedGivesTwoChances` | **Met** — `sentinel_test.go:35`; fails at SevCritical on the second chance. |
| `TestAdditionalContextGatedUntilRehydrator` | **Met** — `gated_test.go:51`, white-box, counting closure proves the body never runs undeclared and runs exactly once when declared. This is the row the brief singled out; it is correctly implemented as a *counting-closure* proof, not an output comparison. |
| `TestPreCompactTimeoutUnknownIsOK` | **Met** — `assertions_test.go:286`. |
| `TestCustomInstructionsProbePhrase` | **Met in name, incomplete in substance** — `assertions_test.go:301` uses a 45-char first line, so it passes identically whether or not the ≥24 rule exists. It does not pin the rule the spec states. See Important I3. |
| `TestMarkerIsWrittenByFlushAndCheckpointOnly` | **Contract half met** — `marker_test.go:67` `TestWriteMarker_IsTheOnlyMarkerWriterInContract` greps the package's non-test sources for `WriteMarker(` call sites and requires none. Daemon half correctly deferred. Honest naming and an explicit doc comment saying which half this is. |
| `TestSentinelRenderIsAnInertComment` | **Met** — `sentinel_test.go:57`, regex-pins 12 lowercase hex + the exact rendered string. |
| `TestHookPayloadShapeRejectsEmptySession` | **Met** — `assertions_test.go:332`, asserts `Observed` names `session_id`. |
| `TestPluginRootUnsetIsOK` | **Met** — `assertions_test.go:346`. |
| `TestHistoryPersistsAcrossMonitors` | **Met** — `assertions_test.go:361`; covers both persisted records (`state/contract.json` mode survival **and** `state/history.json` round-trip). |
| `TestDegradeIsIdempotent` | **Adapted with ruling (brief)** — `monitor_test.go:446`. Asserts two same-reason degrades produce two Loud lines and a third different-reason degrade a third, and that persisted state tracks the most recent call. This inverts the spec's literal wording and is exactly what the brief instructed; the report flags it. Correct. |

**Nothing else silently dropped**, and nothing from the next task leaked in or was wrongly claimed. The one genuine silent drop is the `≥ 24 characters` probe-phrase rule (I3).

---

## Verdict 2 — CODE QUALITY

### Correctness

The reviewer's specific questions, answered first:

**Does a forced passive/full survive restart correctly?** No — and that is a deliberate, documented choice with one residual sharp edge. `forcedMode` is in-memory only (`monitor.go:86`), recomputed from `e.Cfg.Runtime.Mode` on every `RunAll`, and never written by `persist`/`stateLocked`. After a daemon restart, `Mode()` reports the *natural* persisted mode until the first `RunAll` re-applies the force. Since the daemon calls `RunAll` at session start before acting, the window is closed in practice — but any next-task code that reads `Mode()` before the first `RunAll` gets the wrong answer. Recorded as Minor M7.

**Does forcing corrupt the auto state machine?** No. Verified by reading: the whole degrade/restore computation (`monitor.go:207-241`) runs on the *real* results and completes before the force block at 246-261; `forcedMode` is a separate field that `stateLocked`, `persist`, `Degrade` and `Restore` never read; `Degrade`/`Restore` pass their own explicit mode. Switching back to `"auto"`/`""` clears `forcedMode` on the next `RunAll` (the `default:` arm at 254-255) and the natural mode resumes where the observations left it. Clean design. One observable oddity is recorded as Minor M7.

**Panic-recovery result plumbing.** Correct. `runAssertion` (`monitor.go:270-277`) uses a named return, so the recovered `Result` replaces whatever the panicking call would have produced; `ID` comes from `a.ID` (never empty — `Register` rejects empty IDs); `SevWarn` is hard-coded rather than falling through to the declared severity, so an assertion *bug* can never satisfy the `!r.OK && r.Severity == SevCritical` degrade condition. The recover wraps `gated` as well as the inner body.

**Does the gated short-circuit really precede the body?** Yes, proven twice by counting closures (`gated_test.go:22`, `gated_test.go:51`) against the *wrapper itself*, not against an assertion's output — which is the only way to distinguish "never ran" from "ran and was discarded". This is the strongest test in the change.

**SessionHistory JSON round-trip fidelity.** Caps are correct and pinned: `precompact_wall_ms` keeps the 64 newest with the oldest evicted (`TestAddPrecompactWallSample_CapsAtTheNewest64` asserts both ends of the window), `precompact_instr` ≤256, `last` ≤9 newest. `TestHistoryDegradedGolden_Decodes` pins byte-exact `MarshalIndent` round-tripping of the frozen fixture, which is the right Rule W-2 shape. Two gaps: byte-vs-rune truncation (M2) and caps not re-applied on load (M3).

**LoadHistory error swallowing.** Correct and total: read error, unmarshal error and a zero `Version` are all handled (`history.go:199-213`), and the partially-populated `h` from a failed `Unmarshal` is correctly discarded in favour of `zero` rather than returned. Pinned by `TestLoadHistory_MissingFileIsFirstRun` and `TestLoadHistory_CorruptFileFallsBackToZero`.

**Concurrency.** The producer registry is properly mutex-guarded and `HasProducer` is called from inside `gated` with no lock held by the caller — no re-entrancy hazard. `monitor.mu` correctly covers `assertions`/`mode`/`reason`/`since`/`cleanRuns`/`last`/`forcedMode`, and `RunAll` snapshots the assertion slice under lock before running Checks (so a concurrent `Register` cannot race the loop). `hookEventKnownFields` is a package-level var built once at init — safe. The gap is `*SessionHistory` itself (I5).

---

### Findings

#### Important

**I1 — `StartsWithoutMarker` increments per `RunAll`, not per session; a second run in one session can falsely degrade.**
`internal/contract/assertions.go:92`
The counter is bumped every time the Check executes with no matching marker, with nothing keying it to the session. `SessionHistory` carries `LastSessionID` (`history.go:66`) and it is never consulted. §12.1's rule is "absence across two **sessions**", but the implementation encodes "absence across two **runs**". Any second `RunAll` inside one session — a `/qompack:selftest` that synthesizes an `Env` (which the spec's own nil-tolerance paragraph anticipates), a daemon retry, or a future status command — crosses the `>= 2` threshold and degrades the session to passive on a *single* real miss. The test masks this precisely because it hand-bumps `h.SessionCount` between the two `RunAll` calls (`assertions_test.go:258`); nothing in production code does.
*Fix:* gate the mutation on a session change — e.g. `if h.LastSessionID == e.Event.SessionID { /* already counted this session */ }` — or move the counter to the daemon's session-start bookkeeping and have the Check read it. Add a test that calls `RunAll` twice with the same `Event.SessionID` and asserts `StartsWithoutMarker == 1`.

**I2 — An empty `Env.ProjectRoot` is read as "marker absent" and mutates history.**
`internal/contract/assertions.go:87-92`
`readMarker("")` resolves to a relative `.qompack/run/marker.json`, almost always errors, and the Check then increments `StartsWithoutMarker` and (on the second such call) fails at SevCritical. The spec's "Nil tolerance in `Env`" paragraph is explicit that a Check handed a field the caller did not supply "treats empty fields as 'no observation yet' and returns OK rather than failing" — and this one both fails *and* leaves a persistent side effect behind. Every other Check in the file honours that rule; this is the single exception. Combined with I1 it means a half-filled `Env` (exactly the self-test case) can degrade a healthy session.
*Fix:* `if e.ProjectRoot == "" { return Result{OK: true, …, Observed: "no-project-root"} }` before `readMarker`, with no mutation. Pin it with a test.

**I3 — The spec's "first line of ≥ 24 characters" probe-phrase rule was silently dropped; the constant is dead.**
`internal/contract/assertions.go:183` (declared) and `:200` (where it should be used)
`customInstrMinPhraseChars = 24` is declared with a doc comment citing the spec and is never read anywhere in the repo (verified by grep). `phrase := firstLine(h.PrecompactInstr)` takes the first line unconditionally. Two consequences: a short first line (a heading, a bullet marker) becomes a probe phrase specific enough to false-positive against unrelated transcript text — which is the exact failure mode the spec's 24-char floor exists to prevent — and an instruction beginning with a newline yields `phrase == ""`, at which point `bytes.Contains(tail, []byte(""))` is unconditionally true and the assertion can *never* fail. `TestCustomInstructionsProbePhrase` uses a 45-char first line, so it passes identically with or without the rule and pins nothing. The dead constant is not caught by `go vet` (unused *constants* are legal) which is why it survived to commit.
*Fix:* select the first line whose length is `>= customInstrMinPhraseChars`; if no line qualifies, report OK with an `Observed` such as `"no probe phrase long enough"` rather than scanning. Add table cases to `TestCustomInstructionsProbePhrase` for a short first line and an empty phrase.

**I4 — `runtime.mode == "off"` is invisible to `Mode()`, and leaves a stale force behind.**
`internal/contract/monitor.go:189-191`
`RunAll` returns `(nil, ModeOff)` and returns *before* the force block, so `forcedMode` is neither set to `ModeOff` nor cleared. Two concrete problems for the next task, which wires the daemon: (1) `Mode()` keeps reporting the natural mode, and `Mode.MayAct()`/`MayRecord()` (`mode.go:53,60`) both return `true` for `ModeFull` — so any daemon route that consults `Mode()` rather than `RunAll`'s second return value will act and record while the operator has switched Qompack off; (2) after a `"passive"` run followed by an `"off"` run, `Mode()` returns `ModeDegradedPassive`, contradicting the `ModeOff` that `RunAll` just returned. The brief only mandates `Mode()` reflect the *passive/full* force, so this is not a spec violation — but it is a booby trap laid directly in the path of Task 5.
*Fix:* set `forcedMode = &ModeOff` (under `m.mu`) before the early return, so the three runtime modes are handled uniformly and `Mode()` never contradicts `RunAll`.

**I5 — `*SessionHistory` is mutated by assertion Checks with no synchronisation and no documented ownership rule.**
`internal/contract/history.go:58` (type), mutation sites `assertions.go:89,92,116` and `history.go:148,156,165,179`
`monitor.mu` protects the monitor's own fields but the Checks mutate `e.History` *outside* any lock, and `SessionHistory` carries none of its own. The next task wires the daemon, where `RecordSentinelScan` is called from the UserPromptSubmit route while `RunAll` may be running on another goroutine, and where two concurrent `RunAll` calls on the same monitor would both mutate the same record. `Seen` is a bare map, so a concurrent `Record` + `Saw` is a hard `fatal error: concurrent map read and map write`, not a benign data race. Nothing in the type's doc comment states the ownership rule, so the next implementer has nothing to code against. (The race detector is clean here only because no test drives concurrent access.)
*Fix:* at minimum, add a doc line to `SessionHistory` stating it is not safe for concurrent use and must be owned by a single goroutine (the daemon's session-start path), and carry that constraint into the Task 5 brief. If the daemon design cannot honour that, add a `sync.Mutex` to `SessionHistory` and take it in the mutators — noting that the assertion Checks mutate exported fields directly today, which would also have to change.

**I6 — The report tells the next implementer that `SessionHistory`'s sub-shapes are "adjustable"; the newly frozen fixture makes that false.**
`task-4-report.md` "Concerns" bullet 2, vs `testdata/golden/contracts/contract/MANIFEST.json:7` and `internal/contract/history_test.go:124-143`
`history_degraded` is declared `"state": "frozen"`, and `TestHistoryDegradedGolden_Decodes` compares `json.MarshalIndent(&h)` byte-for-byte against the committed file. Any non-`omitempty` field added to, removed from, or renamed on `SessionHistory` or `SentinelState` now breaks a frozen §16 fixture, which under Rule W-2 is a verification failure requiring controller sign-off — not something the next task may quietly "adjust". Freezing the shape is the right call; the report's guidance contradicts it and would send the Task 5 implementer into a Rule W-2 violation.
*Fix:* no code change. Correct the record: state in the Task 5 brief that `SessionHistory`'s wire shape is frozen by `history_degraded`, that new fields must carry `omitempty` (and a zero value in the fixture's scenario) to avoid touching it, and that any other change needs controller sign-off and a fixture re-freeze.

#### Minor

**M1 — Inconsistent Windows long-path handling between `Stat` and `Open`.**
`internal/contract/sentinel.go:59` vs `internal/contract/assertions.go:295,321`, `marker.go:58`, `history.go:201`
Every other filesystem entry point in the change wraps its path in `paths.Long(...)`; `readTail` calls bare `os.Open(path)`. On a >260-char transcript path, `checkTranscriptReadable` would `Stat` successfully and then fail to read, reporting `"transcript_path has no readable content"` for a perfectly readable file. *Fix:* `os.Open(paths.Long(path))`.

**M2 — `SetPrecompactInstr` truncates by bytes, not runes.**
`internal/contract/history.go:156-161`
`instr[:256]` can split a multi-byte rune; `json.Marshal` then substitutes U+FFFD, so `SaveHistory`→`LoadHistory` does not round-trip the stored value and the probe phrase derived from it can no longer match the transcript. The spec says "≤256 **chars**". *Fix:* truncate on a rune boundary (`for !utf8.ValidString(s)` back off, or convert to `[]rune`). `TestSetPrecompactInstr_CapsAt256Chars` uses ASCII only and would not catch it.

**M3 — `LoadHistory` does not re-apply the caps, and accepts any `version`.**
`internal/contract/history.go:199-213`
A hand-edited, corrupted-but-parseable, or future-schema file can carry 10 000 wall samples, 40 `last` entries or a 100 KB `precompact_instr`, and every bound the setters enforce is bypassed for the rest of the process's life — including the p99 window `precompact.has_time_to_write` computes over. A `version: 7` file is accepted as-is. *Fix:* clamp `PrecompactWallMs`/`Last`/`PrecompactInstr` after unmarshal, and treat an unrecognised `Version` like any other error (return `zero`), which is what the "fails toward do nothing" doctrine implies.

**M4 — The frozen golden tests are `t.Parallel()` and depend on the process-global producer registry being empty.**
`internal/contract/golden_test.go:39,74`
`TestResultSet_MatchesFrozenGolden` asserts all nine assertions report `not-yet-implemented`, which is only true while no producer is declared. It is safe *today* only because Go defers parallel tests until every serial test (and its `t.Cleanup(ResetProducers)`) has finished, and no parallel test declares a producer. That is an invisible invariant one future `t.Parallel()` away from a flaky frozen fixture. *Fix:* call `contract.ResetProducers()` with a `t.Cleanup` at the top of both golden tests, or add a package `TestMain` that resets between tests, and say why in a comment.

**M5 — Stale doc comments now pointing at a function that no longer exists.**
`internal/contract/assertion.go:19,35` and `internal/contract/standard_test.go:84` still reference `notYetImplemented`, deleted by this commit. `internal/contract/doc.go:24-30` still says StandardAssertions' "Check functions all report the not-yet-implemented result" and describes SP-05's observations as future work — which is now this commit's shipped behaviour. `internal/contract/contracttest/suite.go:18` still says the History suite "skips until a real History is handed to it", which stopped being true in this same commit. *Fix:* one pass over those four sites; `doc.go`'s "What SP-01 ships for real" section wants a short "what SP-05 replaced" paragraph.

**M6 — The spec's step-5 observability is now unowned.**
`internal/contract/monitor.go:207-264`
The brief legitimately removed step 5 from `RunAll`, but `obs.Counter("contract.fail."+id)` per failing assertion and the per-run `obs.Gauge("contract.mode")` are not in the daemon's Task 5 brief either (the gauge exists only inside `record()`, on Degrade/Restore transitions). A metric that neither task owns is a metric that ships missing. *Fix:* controller decision — assign both to Task 5's daemon wiring, or file them explicitly as deferred.

**M7 — Forced mode is not persisted, and a forced-passive monitor still emits a LOUD "restoring full mode".**
`internal/contract/monitor.go:80-86, 238-239`
Two consequences of the (correct, documented) in-memory choice: after a restart, `Mode()` reports the natural mode until the first `RunAll` re-applies the force; and while `runtime.mode = "passive"` is forcing passivity, a naturally recovering monitor still calls `Restore` and writes `"contract: restoring full mode"` to LOUD.log even though `Mode()` will keep reporting `degraded-passive`. Both are defensible, neither is documented at the point a reader would trip over them. *Fix:* a sentence at the `forcedMode` field noting the pre-first-`RunAll` window, and either suppress or annotate the Restore line while a force is in effect.

**M8 — Nil-receiver guards are inconsistent across `SessionHistory`'s methods.**
`internal/contract/history.go:111-144` (guarded) vs `:148,156,165,179` (unguarded)
`Saw`/`LastSeen`/`Record`/`Sessions` all check `h == nil`; `AddPrecompactWallSample`/`SetPrecompactInstr`/`RecordLast`/`RecordSentinelScan` panic on a nil receiver. Either the guard is load-bearing or it is not. *Fix:* pick one; guarding all of them is cheap and matches `historyOf`'s typed-nil tolerance.

**M9 — The `"no-samples"` branch is an unpinned deviation from the spec table.**
`internal/contract/assertions.go:160-162`
The spec goes straight from the `TimeoutMs == 0` case to the p99 comparison; with zero samples `percentileMs` returns 0 and the ratio is 0, so the added branch is behaviourally equivalent — only the `Observed` string differs, and no test pins it. Harmless, but it is an unrecorded third arm in a table the review has to check row by row. *Fix:* mention it in the report's deviations list, or add a one-line test.

**M10 — `hook.payload_shape`'s collision arm is unreachable for any real payload.**
`internal/contract/assertions.go:253-262`
`hookio.Event.Extra` is `json:"-"` and `hookio.ReadEvent` populates it only with keys the struct tags do *not* claim (`internal/hookio/event.go:33-36,88`), so a claimed key can never appear in `Extra` for an Event that came off the wire. The arm can only fire on a hand-constructed Event. The spec asks for the check, so keeping it is right — but a reader will spend time working out why it never triggers. *Fix:* one comment line saying it is a defence against a hand-built or future-decoder Event, not against `ReadEvent`'s output.

---

### Maintainability

Genuinely good. The doc comments explain *why* rather than restating the code — `gated`'s "not 'runs and is ignored', never runs", `forcedMode`'s rationale, `HistoryPath`'s "two schemas sharing one path would corrupt each other", `SentinelState`'s explanation of why `Chances` is the scan's job and not the assertion's, and `WriteMarker`'s note on why `WriteAtomic` is the right primitive are all the kind of comment that survives a year. `hookEventKnownFields` built by reflection over `hookio.Event` instead of a hand-maintained list is the right call and is explained. Thresholds and caps are named constants (`nomagic` passes). `assertionByID` instead of index-based lookup in tests is a small, correct decision. Test naming follows the spec table where the spec named a test and is honest where it deviates (`TestWriteMarker_IsTheOnlyMarkerWriterInContract` explicitly labels itself the contract-side half). The report's self-flagging of its own weak TDD transcript is the right instinct.

The one systemic maintainability weakness is the dead `customInstrMinPhraseChars` constant (I3): a declared, documented, unreferenced constant is the signature of an intent that was written down and then not wired up, and Go's compiler will never tell you.

---

## Adjudication of the report's three concerns

**(a) `session_start.fires` resets `StartsWithoutMarker` to 0 on a hit — inference beyond the spec table, untested.**
**Accepted as correct, but it must be pinned, and it is the smaller half of the problem in that function.** "Absence across two sessions" reading as *consecutive* absence is the right reading — it is what the field's own doc comment says ("counts consecutive SessionStarts"), what §12.1's degrade-then-recover doctrine implies everywhere else, and the alternative (a lifetime counter) would degrade every long-lived project eventually on two unrelated misses years apart. No ruling change needed. But `TestSessionStartFiresNeedsTwoMisses` exercises only two consecutive misses, so nothing stops a future edit from deleting line 89 — **add a test: one miss, then a run with a marker present, then a miss, and assert `StartsWithoutMarker == 1` and OK**. Note also that the reset is *unreachable* whenever `ProjectRoot` is empty (I2), and that the counter's per-run rather than per-session increment (I1) is the defect that actually threatens a false degrade. Both belong in the same fix as the reset test.

**(b) `SessionHistory` sub-field shapes beyond the brief's named top-level fields were implementer-designed.**
**Accepted.** The brief named the top-level JSON tags and left `SentinelState` to the implementer; `token`/`session`/`minted_at` are exactly what a daemon needs to mint at SessionStart and re-scan at UserPromptSubmit without re-deriving the token, and they are all `omitempty`. `observed`/`chances` are the two fields the assertion reads and are non-`omitempty`, correctly, since `false`/`0` are meaningful. No over-design. **However, the report's accompanying advice — that the next task should "treat these as adjustable" — is wrong and must be corrected before Task 5 starts** (Important I6): the shape is now frozen by `history_degraded` under Rule W-2.

**(c) `internal/testutil/fixtures_test.go` frozen-fixture count bumped 26→28.**
**Verified as exactly the sanctioned mechanism, with no existing fixture touched.** Checked three ways. (1) `git diff eeca0651 506baa01 -- internal/testutil/fixtures_test.go` changes only the pinned integer and its explanatory comment, extending the existing "21 SP-01 + 2 V1 + 3 SP-05 task 1" ledger to "+ 2 SP-05 task 4" and naming the two new fixtures — the identical shape to Task 1's own bump, and to the pre-branch commit `198c299 test(testutil): raise the frozen-fixture count to 23`. (2) `git diff` on `testdata/golden/contracts/contract/MANIFEST.json` is purely additive: two new entries appended, `result_set` and `not_yet_implemented_is_info` byte-identical (the only change to the `not_yet_implemented_is_info` line is the trailing comma required by JSON). (3) No previously-frozen fixture file appears anywhere in the commit's file list — in particular `want/result_set.json` is absent from the diff, and `TestResultSet_MatchesFrozenGolden` / `TestResultSet_GoldenRoundTripsLosslessly` pass unmodified, which they could not do if the producer registry leaked out of any test in the package. `pendingCount` correctly stays at 5. No concern here.

---

## Counts

| Severity | Count |
|---|---|
| Critical | 0 |
| Important | 6 |
| Minor | 10 |

**Needs fixes (0 Critical, 6 Important)**

---

# Re-review (fix round 1)

Reviewed: `506baa01` → **`35a1901b`** (amended, still exactly one commit; subject, body and
`Refs:` line byte-identical; no attribution trailers). Delta: 12 files, +495/−36, confined to
`internal/contract/`.

Re-ran read-only: `go test ./internal/contract/... -race -count=2` (ok, ok), `go test ./test/guards/...`
(ok), `go test ./internal/testutil/...` (ok), `go run ./tools/devtool lint` (PASS golangci-lint /
nomagic / importgraph / testdeps / bindeps / sleepcheck / stubskips), `devtool fmt` (no diff),
`GOOS=linux go build ./...` (clean).

**Frozen fixtures:** `git diff --name-only 506baa01 35a1901b -- testdata/ internal/testutil/` returns
**zero files**. Nothing under `testdata/golden/` and nothing in the frozen-fixture registry was
touched by the fix round; the count stays 28 and `TestHistoryDegradedGolden_Decodes` /
`TestResultSet_MatchesFrozenGolden` still pass unmodified.

Controller rulings #24 (off forces ModeOff in-memory), #25 (no internal locking; ownership
documented) and #26 (contract.fail / contract.mode observability assigned to Task 5) are noted and
applied to the assessment below. M6 and M7's behavioural half are not re-flagged.

## Per-finding status

**I1 — `StartsWithoutMarker` per-session, not per-run — FIXED.**
`assertions.go:88-110`. The increment is now guarded by `if h.LastSessionID != e.Event.SessionID`,
and `LastSessionID` is written on all three terminating branches (first-session, marker-found,
absent), so the dedupe key advances however the Check resolves. Traced the state machine by hand
across all four arms; no path leaves the key stale, and no path increments twice for one session id.
The pin the coordinator asked for closest inspection of —
`TestSessionStartFires_ConsecutiveAbsenceIsPerSession` (`assertions_test.go:316`) — is genuinely
free of hand-bumped `SessionCount`: it sets `SessionCount: 5` once at construction and then drives
miss → real `WriteMarker` → miss purely through `Event.SessionID`, asserting the streak lands at 1,
not 2. `TestSessionStartFires_SameSessionRunTwiceDoesNotDoubleCount` (`assertions_test.go:292`) is
the direct regression test for the original defect. `TestSessionStartFiresNeedsTwoMisses` was
correctly updated to use two distinct session ids (`sess-a`/`sess-b`) — under the old code it passed
only because the miss double-counted, so leaving it unchanged would have been a false green. One
consequence of the mechanism needs a follow-up: see **NEW-1**.

**I2 — empty `Env.ProjectRoot` — FIXED.**
`assertions.go:88-95`. The guard sits before `Sessions()`, before `readMarker`, and before any
mutation, returning `noObservationYet`. `TestSessionStartFires_EmptyProjectRootIsNoObservation`
asserts OK, the `"no observation yet"` string, **and** that both `StartsWithoutMarker` and
`LastSessionID` are untouched — testing the absence of the side effect, not just the return value,
which is the right shape for this fix.

**I3 — the ≥24-character probe-phrase rule — FIXED.**
`probePhrase` (`assertions.go:373-379`) now reads the previously-dead `customInstrMinPhraseChars`
and returns `ok=false` for a short or empty first line; `checkPreCompactCustomInstr:218-226` reports
`OK:true / "no probe phrase long enough"` rather than scanning. The vacuous-match hole is closed at
the root: the empty phrase can no longer reach `bytes.Contains`. Coverage is thorough and covers
exactly the paths flagged — white-box `TestProbePhrase` (`gated_test.go:99`) including the
leading-newline and empty-string cases; `..._ShortFirstLineIsNoObservation`, whose transcript
deliberately *does* contain the short phrase verbatim so the test fails if the code scans anyway
(a real negative control, not a tautology); `..._LeadingNewlineIsNoObservation`; and
`..._GenuineMatchAndMiss` as the positive control. Minor byte-vs-rune nit at **NEW-3**.

**I4 — `runtime.mode == "off"` and `Mode()` — FIXED (ruling #24).**
`monitor.go:195-206`. The early return now sets `forcedMode = &ModeOff` under `m.mu` before
returning, using the same mechanism as passive/full. Verified the interaction with the persisted
state machine by tracing and by the new test: nothing on the off path touches `m.mode`, `m.reason`,
`m.cleanRuns`, `m.last` or `persist`, so the natural machine is frozen exactly where the last real
run left it; the next non-off `RunAll` clears the force through the existing `default:` arm and the
persisted mode resumes unchanged. `TestRuntimeModeOffForcesModeOff` (`assertions_test.go:191`)
pins all four halves — `RunAll` returns `ModeOff`, `Mode()` agrees, `Mode().MayAct()` is false, and
the return to `"auto"` restores `ModeFull` from the state machine rather than a fabricated value.

**I5 — `SessionHistory` concurrency — FIXED as documentation (ruling #25).**
`history.go:58-71`. The doc comment is unusually good for this class of fix: it states the rule
(single owner goroutine for the whole lifetime), names the specific hazard (`Seen` is a bare map, so
this is a hard `fatal error: concurrent map read and map write`, not a race-detector warning),
enumerates who must be inside the ownership boundary (load, every Check, every daemon route calling
`RecordSentinelScan`/setting `MCPInitialized`, and `SaveHistory`), and says who is expected to
enforce it. `LoadHistory` and `SaveHistory` each carry a cross-reference. No locking added, per the
ruling. This is now something Task 5 can code against.

**I6 — the "adjustable sub-shapes" erratum — FIXED.**
The report's "Fix round 1 / I6" section explicitly retracts the original advice, states the Rule W-2
consequence, and writes the correction for Task 5's brief (new fields must be `omitempty` and zero
in the fixture's scenario; anything else needs controller sign-off and a re-freeze). `doc.go:36-38`
now carries the same statement in the source itself. One dangling cross-reference: see **NEW-2**.

## Minors spot-check

| # | Status |
|---|---|
| M1 `readTail` long path | **Fixed** — `sentinel.go:60` now `os.Open(paths.Long(path))`; `paths` import added. Consistent with every other FS call in the package. |
| M2 rune truncation | **Fixed** — `history.go:170-179` truncates via `[]rune`. `TestSetPrecompactInstr_CapsByRunesNotBytes` uses 300 × `€` and asserts rune count, `utf8.ValidString`, absence of U+FFFD, **and** a real `SaveHistory`/`LoadHistory` round trip — it tests the actual corruption mechanism, not just the length. |
| M3 caps + version on load | **Fixed** — `applyCaps` (`history.go:219-236`) re-clamps all three bounded fields; `LoadHistory:256-271` rejects an unrecognised `Version` via a `switch` with an explicit `case historyVersion` and a `default: return zero`. Two new tests cover both halves; the caps test asserts the *newest* samples survive the reclamp, not merely the length. |
| M4 golden `t.Parallel()` | **Fixed** — `t.Cleanup(contract.ResetProducers)` added to `TestResultSet_MatchesFrozenGolden` with a comment explaining the invariant it removes dependence on. Correctly *not* added to `TestResultSet_GoldenRoundTripsLosslessly`, which only unmarshals the file and never touches the registry. |
| M5 stale docs | **Fixed** — all four sites: `assertion.go:19,35` now point at `gated`, `standard_test.go:84-87` likewise, `contracttest/suite.go:16-21` rewritten to past tense with the SessionHistory fact, and `doc.go` gains a "What SP-05 replaced" section. |
| M6 obs counters | **Not applicable** — ruling #26 assigns them to Task 5. Not re-flagged. |
| M7 forced-mode restart window | **Doc half fixed** — `monitor.go:86-92` now spells out that the force is in-memory only and that `Mode()` before the first `RunAll` of a process reports the persisted natural mode. Behavioural half (LOUD restore during a force) carried per instruction; not re-flagged. |
| M8 nil receivers | **Fixed** — all four mutators guard `h == nil` (`history.go:162,173,190,206`), and `TestSessionHistory_NilReceiverMethodsAreNoOps` exercises every method on the type in one `require.NotPanics`. |
| M9 `no-samples` arm | **Fixed** — `TestPreCompactTiming_NoSamplesIsOK` pins the distinct `Observed` string, with a comment explaining why an arm that is behaviourally equivalent is still worth pinning. |
| M10 collision-arm reachability | **Fixed** — `assertions.go:284-290` comment states it cannot fire for a `ReadEvent`-produced Event and explains why it is kept anyway. |

8 of 10 fixed; the other two are ruled, not outstanding.

## New defects introduced by the delta

**NEW-1 (Important) — `LastSessionID` silently acquired a load-bearing invariant and is still an
undocumented, daemon-facing exported field.**
`internal/contract/history.go:79` (the field), `internal/contract/assertions.go:105-108` (the reader)
The I1 fix keys the miss-dedupe off `SessionHistory.LastSessionID`, but that field has **no doc
comment at all** — it is one line in an undocumented run of three, with the JSON tag
`last_session_id` the brief's field list named. Nothing tells a reader it is now owned by
`session_start.fires`. The next task wires the daemon, and `h.LastSessionID = ev.SessionID` at
session start is the single most natural line for a daemon author to write against a field with that
name. If they do, `h.LastSessionID != e.Event.SessionID` is false on every subsequent run,
`StartsWithoutMarker` never advances past 0, and **`session_start.fires` — a SevCritical assertion —
is permanently and silently disabled**, with every existing test still green (they all drive the
Check without a daemon). This is the same class of trap as the original I4, relocated.
*Fix (small, mechanical):* document the field — that `checkSessionStartFires` owns it as its
per-session dedupe key and no other writer may set it — and add a regression test that pre-sets
`h.LastSessionID` to the current `Event.SessionID` and asserts the first genuine miss of a new
session is still counted. A dedicated field (e.g. `marker_checked_session`, `omitempty`) would be
more robust, but changes the wire shape and so needs the Rule W-2 path from I6; the doc + test is
sufficient. Alternatively the controller may fold this verbatim into Task 5's brief as a "must not
write" constraint — but it should not be left implicit in either place.

**NEW-2 (Minor) — dangling cross-reference in `doc.go`.**
`internal/contract/doc.go:37-38` says the wire shape "is frozen by the history_degraded golden
fixture; see SessionHistory's own doc comment for what that means for a future change." But
`SessionHistory`'s doc comment (`history.go:53-71`) covers only the ownership contract — it says
nothing about the freeze (grep for `frozen`/`history_degraded`/`Rule W-2` in `history.go` returns
nothing). The reader is sent to a comment that does not answer the question. *Fix:* add the
two-sentence freeze note to `SessionHistory`'s doc comment, or point `doc.go` at the MANIFEST entry
and `TestHistoryDegradedGolden_Decodes` instead.

**NEW-3 (Minor) — `probePhrase` measures bytes while `SetPrecompactInstr` now measures runes.**
`internal/contract/assertions.go:375` uses `len(line) < customInstrMinPhraseChars`, while the M2 fix
deliberately moved the sibling cap to `[]rune`. Both constants are documented as "characters". The
inconsistency only over-accepts (eight CJK characters are 24 bytes and would qualify as a 24-"char"
phrase), so it cannot reintroduce the empty-phrase hole — but it is a straightforward divergence
between two rules written in the same units, in the same file, in the same commit. *Fix:*
`utf8.RuneCountInString(line) < customInstrMinPhraseChars`.

**NEW-4 (Minor, test-comment only) — inaccurate comment in the new I1 pin.**
`internal/contract/assertions_test.go:346-347` says the third run's stale marker "still names a
different (but now stale) session". It names `sess-a`, which is *the same* id as that run's
`Event.SessionID` — which is precisely why the branch reads it as absent. The comment describes the
opposite of the mechanism under test. The reuse of `sess-a` for the third session also makes the
test quietly order-dependent: had the third run used `sess-b`, `LastSessionID` would already equal
it and the increment would be skipped, so the assertion would fail for a reason unrelated to what
the test claims to pin. *Fix:* correct the comment and use a third distinct id (`sess-c`) with the
marker still naming `sess-a`, which exercises the intended "stale marker from an older session"
scenario the comment describes.

## Re-review counts

| Severity | Count |
|---|---|
| Critical | 0 |
| Important | 1 (NEW-1) |
| Minor | 3 (NEW-2, NEW-3, NEW-4) |

All six original Importants and all eight in-scope Minors are genuinely fixed, with tests that pin
the mechanism rather than the symptom. The delta is well-scoped, touches no frozen fixture, and
introduces no regression. The single blocker is NEW-1, which is a doc comment plus one regression
test — or, at the controller's discretion, an explicit constraint in Task 5's brief.

**Needs fixes (0 Critical, 1 Important)**

---

# Re-review (fix round 2)

Reviewed: `35a1901b` → **`29585ce1`** (amended). Delta: 3 files, +81/−8 — `assertions.go`,
`assertions_test.go`, `history.go`. Nothing else touched.

Re-ran read-only: `go test ./internal/contract/... -race -count=2` (ok, ok), `go test ./test/guards/...`
(ok), `go test ./internal/testutil/...` (ok), **`go test ./...` (entire repo, zero failures)**,
`go run ./tools/devtool lint` (PASS golangci-lint / nomagic / importgraph / testdeps / bindeps /
sleepcheck / stubskips), `devtool fmt` (no diff), `GOOS=linux go build ./...` (clean).

**Commit hygiene:** `git log --oneline eeca0651..29585ce1` → exactly **1** commit; subject, body and
`Refs:` line byte-identical to the brief's block; a case-insensitive grep of the full message for
`co-authored` / `generated with` / `signed-off` returns **0** matches.

**Frozen fixtures:** `git diff --name-only 35a1901b 29585ce1 -- testdata/ internal/testutil/` returns
**zero files**. The fixture bodies, the MANIFEST and the frozen count (28) are untouched across all
three rounds.

## Per-item status

**NEW-1 — `LastSessionID` ownership — FIXED (documentation + regression test), with the guarantee
correctly scoped.**
`history.go:88-96` now carries a ten-line doc comment on the field: it states the owner
(`checkSessionStartFires` and nothing else), what the field means (the session id that last advanced
`StartsWithoutMarker`), the exact prohibition ("The daemon MUST NEVER write this field itself — in
particular, never pre-set it to the incoming session's id at SessionStart before RunAll runs"), and
the consequence of violating it (permanent suppression of a SevCritical assertion). That is the
precise trap I described, named at the site a daemon author would be reading. `doc.go`'s
cross-reference now also resolves (see NEW-2).

On the coordinator's specific question — *does the test genuinely fail if a daemon-style pre-write
suppressed counting?* — the honest answer is **no, and it is not supposed to; it is a
wedge-regression test, not a suppression-prevention test, which is what I asked for and what is
achievable here.** `TestSessionStartFires_DaemonPreWriteOfLastSessionIDDoesNotWedgeFutureCounting`
(`assertions_test.go:382`) performs the forbidden pre-write and then asserts three things: (1) this
session's miss *is* suppressed — the hazard is asserted as real, not defended against, because the
Check provably cannot distinguish "already counted" from "someone wrote my field"; (2) a genuinely
new session id counts again (→1); (3) a third distinct session still reaches the threshold and
returns `OK:false / SevCritical` (→2). Traced by hand: an implementation that stopped counting
altogether after the pre-write fails at assertion (2), and one that could no longer tell two session
ids apart fails at (2) or (3). So the mechanism is pinned as *recoverable*, and the test is honest in
its own comment about which half is hazard-documentation.

What this means for Task 5 is worth stating plainly, because it is the residual risk and it is
accepted rather than eliminated: **the protection against NEW-1 is documentary.** A daemon that
pre-writes `LastSessionID` on *every* session start would suppress the increment on every session,
`StartsWithoutMarker` would never advance, `session_start.fires` would be permanently disabled, and
this test would still pass (it only proves recovery once the daemon stops). That is the accepted
shape of the fix — the alternative, a dedicated field, requires the Rule W-2 re-freeze path from I6
— but the "must not write `LastSessionID`" constraint should still be carried into Task 5's brief
rather than relying on the doc comment alone. Recording it here so the controller has it explicitly;
not a defect in this commit.

**NEW-2 — dangling `doc.go` cross-reference — FIXED.**
`history.go:71-79` adds a "Wire shape is FROZEN" block to `SessionHistory`'s doc comment, separate
from the concurrency contract. It names the fixture path, the MANIFEST `"frozen"` declaration and
`TestHistoryDegradedGolden_Decodes`; states the Rule W-2 consequence ("a verification failure, not a
fixture bug"); requires controller sign-off plus a re-freeze; and gives the escape hatch (a new field
must be `omitempty` and absent/zero in the fixture's scenario). `doc.go:37-38`'s "see SessionHistory's
own doc comment" now resolves to an answer.

**NEW-3 — byte-vs-rune in `probePhrase` — FIXED.**
`assertions.go:377` now uses `utf8.RuneCountInString(line) < customInstrMinPhraseChars`, with
`unicode/utf8` added to the imports; the doc comment says RUNES and explicitly ties the unit to
`SetPrecompactInstr`'s 256-char cap. The two rules in this file are now measured in the same units.
`TestProbePhrase` still passes (its fixtures are ASCII, so it neither regressed nor newly covers the
multi-byte case — acceptable; the unit is now consistent by construction).

**NEW-4 — inaccurate/order-dependent comment in the I1 pin — FIXED, and better than asked.**
`assertions_test.go:348-356`. The third session is now a genuinely distinct `sess-c`, and the
implementer went one step further than my suggestion by `os.Remove`-ing the marker file first —
correctly reasoning that under the Check's own rule ("marker exists AND names a session DIFFERENT
from the current one → OK") a surviving `sess-a` marker would read as *marker-found* for `sess-c`,
which is not the scenario the test claims to pin. My suggested version would have been wrong; theirs
is right. Traced: marker absent → `LastSessionID` (`sess-b`) ≠ `sess-c` → increment to 1, degrade
threshold not reached, `ModeFull`. The comment now describes the mechanism accurately and the test no
longer depends on coincidental id reuse.

## New defects

**NEW-5 (Minor, test-comment only, non-blocking) — one overclaim in the NEW-1 test's comment.**
`internal/contract/assertions_test.go:388-390` says a broken I1 fix "e.g. one that stopped updating
LastSessionID at all once it had ever been set … would fail the second half of this test." Traced
against that exact variant: pre-write `sess-a`; `sess-a` run → no increment (0 ✓); `sess-b` run →
`"sess-a" != "sess-b"` → 1 ✓; `sess-c` run → `"sess-a" != "sess-c"` → 2, `SevCritical` ✓. It passes.
The *second* named variant ("could never again tell two different sessions apart") is genuinely
caught. Mitigating: the unnamed variant is caught elsewhere in the suite —
`TestSessionStartFires_SameSessionRunTwiceDoesNotDoubleCount` repeats one session id from a zero
`LastSessionID` and would fail it — so there is no coverage hole, only a comment that credits the
wrong test. *Fix:* drop the first example or point it at the sibling test. Not worth a fix round on
its own; fold into any later touch of this file.

No other new defects. The delta is minimal, confined to the three files it needed, introduces no
behavioural change beyond the rune-count unit correction, and regresses nothing.

## Re-review counts (round 2)

| Severity | Count |
|---|---|
| Critical | 0 |
| Important | 0 |
| Minor | 1 (NEW-5, non-blocking) |

All four round-1 items (NEW-1 … NEW-4) are fixed, and NEW-4's fix is more correct than the fix I
proposed. All six original Importants and the eight in-scope Minors remain fixed. Single
trailer-free commit, frozen fixtures untouched, whole-repo test suite green.

Two items carried forward for the controller, neither a defect in this commit: (i) the "daemon must
never write `LastSessionID`" constraint should be stated in Task 5's brief, since the protection is
documentary; (ii) ruling #26's `contract.fail.*` / `contract.mode` observability must actually land
in Task 5.

**Approved**
