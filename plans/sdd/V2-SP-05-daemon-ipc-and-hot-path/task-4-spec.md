### `internal/contract/*` — the G9.3 monitor

**`producers.go`.** A package-level `map[ID]bool` behind a mutex, with `DeclareProducer`, `HasProducer`, `ResetProducers`. Declaration is idempotent.

**`history.go`.** `LoadHistory(statePath)` reads `.qompack/state/contract.json`, returning a zero `History` with `Version: 1` on any error (a missing file is the first-run case). `SaveHistory` uses `paths.WriteAtomic`. `History.Last` is capped at the 9 most recent `Result`s.

**`marker.go`.** `MarkerPath(projectRoot)` = `<projectRoot>/.qompack/run/marker.json`. `WriteMarker(projectRoot, sess, now)` writes `{"session":"<id>","ts":<unixMilli>}` with `paths.WriteAtomic` (it is a mutable one-record view, not an append-only artifact, so `WriteAtomic` is the correct primitive and `.qompack/run/` is outside the §3.3 append-only set). It is called by the daemon's `flush` and `checkpoint` routes — the SessionEnd and PreCompact terminal hooks — and by nothing else. The `session_start.fires` assertion reads it; it is never cleared, because it is overwritten by the next terminal hook and the assertion only ever compares its recorded session id against the current one.

**`sentinel.go`.**
`MintSentinel(sess, now)` → token `"qompack-contract-" + core.HashBytes("qompack.sentinel.v1", []byte(string(sess)+strconv.FormatInt(int64(now),10))).Short()` (12 hex chars).
`RenderSentinel(s)` → exactly `"<!-- qompack-contract-probe " + s.Token + " -->"` on its own line — an HTML comment so it is inert in any rendering path and unmistakable in a transcript.
`ScanTranscriptTail(path, token, tailBytes)` opens the file, seeks to `max(0, size-tailBytes)`, reads to EOF, and reports `bytes.Contains`. A missing or unreadable file returns `(false, err)`; the caller treats that as "no observation yet", never as a failure.

**`monitor.go`.** `RunAll(ctx, env)`:

1. If `env.Cfg.Runtime.Mode` is `"off"` → return `(nil, ModeOff)` immediately without running anything.
2. If it is `"passive"` → run the assertions for reporting, then force `ModeDegradedPassive`.
3. If it is `"full"` → run the assertions for reporting, then force `ModeFull` (an escape hatch for a user whose host build breaks our detection).
4. `"auto"` (the default) → run every registered assertion, each inside a `recover` (a panicking assertion yields `Result{OK:false, Severity:SevWarn, Observed:"assertion panicked"}` — an assertion bug must not degrade a session), collect results, then:
   - any `Result` with `!OK && Severity == SevCritical` → `Degrade(reason, results)`, mode = `ModeDegradedPassive`, `History.CleanRuns = 0`;
   - otherwise, if the current persisted mode is `ModeDegradedPassive`: `History.CleanRuns++`; when it reaches **2**, `Restore("two consecutive clean assertion runs")` and mode = `ModeFull`; below 2, mode stays `ModeDegradedPassive`;
   - otherwise mode = `ModeFull`.
5. Persist `History` (mode, reason, `Last`), record `obs.Counter("contract.fail."+id)` per failure and `obs.Gauge("contract.mode")`, and return.

`Degrade(reason, results)` performs §12.1's ordered steps exactly: `log.Loud("qompack degraded to passive recording", "reason", reason, "assertion", id, "expected", exp, "observed", obs)` → persist to `state/contract.json` (`Mode`, `DegradedReason`, `DegradedSince`) → set the in-memory mode → `obs.Counter("contract.degrade").Inc()`. `Restore(reason)` is symmetric and equally `Loud`. Both are idempotent: degrading while degraded re-logs only if the reason changed.

**`assertions.go` — `StandardAssertions()`**, the nine assertions of 00-ARCH §12.1 in that order, each with its severity **and the exact observation it makes**. Severity in the middle column is the severity used *when the producer is declared*; a missing producer is handled once, by `gated`, below.

| ID | Severity when producer declared | Check |
|---|---|---|
| `session_start.fires` | `SevCritical` | `run/marker.json` exists and its session id differs from the current one → OK. Absent → `History.StartsWithoutMarker++`; fail only at `>= 2` ("absence across two sessions ⇒ fail"). First-ever session (`History.Sessions == 0`) → OK, `Observed: "first-session"` |
| `session_start.source_compact` | `SevCritical` | if `History.AwaitingCompactStart` (a PreCompact was observed for this session id) then `env.Event.Source == "compact"` → OK, else fail with `Expected:"compact"`, `Observed:env.Event.Source`. Clears the flag either way. Not awaiting → OK, `Observed:"no-precompact-pending"` |
| `hook.additional_context_delivered` | `SevCritical` | `History.Sentinel.Observed` → OK. Not observed and `Chances < 2` → OK, `Observed:"not-yet-observed"`. Not observed and `Chances >= 2` → fail |
| `precompact.has_time_to_write` | `SevCritical` on timeout, `SevWarn` at >60% | p99 of `History.PreCompactWallMs` (the 64 newest samples) against `History.PreCompactTimeoutMs`: `>= 100%` → fail `SevCritical`; `> 60%` → fail `SevWarn`; else OK. `PreCompactTimeoutMs == 0` (never observed) → OK, `Observed:"timeout-unknown"` |
| `precompact.custom_instructions_accepted` | `SevWarn` (advisory by design, §8.5) | `History.PreCompactInstr` empty → OK, `Observed:"no-instructions-emitted"`. Otherwise take its **first line of ≥ 24 characters** as the probe phrase and `ScanTranscriptTail(TranscriptPath, phrase, 256<<10)`: found → OK; absent → warn, `Observed:"instruction phrase not found in transcript tail"` |
| `hook.payload_shape` | `SevCritical` | `env.Event.HookEventName != ""` and `env.Event.SessionID != ""` and (`CWD != ""` or `TranscriptPath != ""`) and no `Extra` key collides with a known field name |
| `mcp.server_registered` | `SevWarn` | `History.MCPInitialized` |
| `transcript.readable` | `SevWarn` | `TranscriptPath` non-empty, `os.Stat` succeeds, and the last non-empty line parses as JSON. Empty path → OK, `Observed:"no-transcript-path"` |
| `plugin.root_resolves` | `SevWarn` | `os.Getenv("CLAUDE_PLUGIN_ROOT")`: empty → OK `Observed:"unset"`; set → the directory exists and contains `bin/qompack` or `bin/qompack.exe` |

**The not-yet-implemented rule, implemented once, in a wrapper — this is the normative mechanism:**

```go
func gated(id ID, sev Severity, desc string, check func(context.Context, Env) Result) Assertion {
    return Assertion{ID: id, Severity: sev, Description: desc,
        Check: func(ctx context.Context, e Env) Result {
            if !HasProducer(id) {
                // §12.1: an assertion whose producer is absent from the build must never degrade
                // the session. A CI test asserts a freshly built develop reports ModeFull.
                return Result{ID: id, OK: true, Severity: SevInfo,
                    Expected: desc, Observed: "not-yet-implemented", TS: now(e)}
            }
            r := check(ctx, e); r.ID = id
            if r.Severity == 0 && !r.OK { r.Severity = sev }
            return r
        }}
}
```

Every one of the nine is constructed through `gated`. The **five** SP-05 always declares (`session_start.fires`, `session_start.source_compact`, `hook.payload_shape`, `transcript.readable`, `plugin.root_resolves`) are live from wave 1; the **four** §12.1 names as later-wave (`precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `hook.additional_context_delivered`, `mcp.server_registered`) report `not-yet-implemented` until their owning subplan binds its `Services` seam.

**On `hook.additional_context_delivered` specifically.** SP-05 mints, emits and scans for the sentinel from wave 1, so the *mechanism* is complete and unit-tested here (`TestSentinelRoundTrip`, `TestSentinelNotObservedGivesTwoChances`, which call the check directly with the producer declared). But §12.1 assigns its producer to SP-11, so the assertion itself stays `not-yet-implemented` — and therefore cannot degrade a session — until SP-11's rehydrator binds `Services.Rehydrate`. Do **not** invent a SevWarn-then-escalate intermediate state: `gated` short-circuits before the check runs when the producer is absent, so any such state would be unreachable code contradicting its own wrapper. `TestAdditionalContextGatedUntilRehydrator` pins both halves.

**Nil tolerance in `Env`.** `Env.Store` is a wave-1 stub and may be nil; no assertion dereferences it. `Env.Event` may be a zero value when `self-test` synthesizes an `Env` from persisted `History`; every assertion that reads `Event` treats empty fields as "no observation yet" and returns OK rather than failing. `Env.Clock` nil → `core.SystemClock()`.

---

### `internal/contract` (`monitor_test.go`, `assertions_test.go`, `sentinel_test.go`)

| Test | Setup | Expected |
|---|---|---|
| `TestFreshBuildReportsModeFull` | no producers declared beyond SP-05's five; a well-formed `Event`; empty `History` | `mode == ModeFull`; the four later-wave assertions (`precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `hook.additional_context_delivered`, `mcp.server_registered`) report `OK:true, SevInfo, Observed:"not-yet-implemented"` — **this is the CI test §12.1 demands** |
| `TestDeclaredProducerSetMatchesArchitecture` | `daemon.DeclareProducers(&Services{})` on a clean registry | exactly the five always-declared ids are present and the four §12.1 later-wave ids are absent; with `Services{Rehydrate: fn, MCPInitialized: fn, PreCompact: fn}` all nine are present |
| `TestCriticalFailureDegrades` | declare `CPreCompactTiming`; `PreCompactWallMs` p99 at 100% of timeout | `mode == ModeDegradedPassive`; exactly one `Loud` line; `state/contract.json` carries the reason and `DegradedSince` |
| `TestTwoCleanRunsRestore` | start degraded; run clean twice | run 1 → still `ModeDegradedPassive`, `CleanRuns==1`; run 2 → `ModeFull` and a `Loud` restore line |
| `TestOneCleanRunDoesNotRestore` | start degraded; one clean run | still `ModeDegradedPassive` |
| `TestRuntimeModeOffShortCircuits` | `runtime.mode="off"` | `RunAll` returns `(nil, ModeOff)`; zero assertions executed (assert with a counting assertion) |
| `TestRuntimeModePassiveForces` | `runtime.mode="passive"`, all assertions clean | `ModeDegradedPassive`, results still populated |
| `TestPanickingAssertionDoesNotDegrade` | register an assertion that panics | `mode == ModeFull`; its result is `OK:false, SevWarn, Observed:"assertion panicked"` |
| `TestSessionStartFiresNeedsTwoMisses` | no marker, `History.Sessions=1` then `2` | first run OK (`StartsWithoutMarker==1`), second run fails |
| `TestSourceCompactEvaluatedOnFollowingStart` | mark `AwaitingCompactStart`; `Event.Source="startup"` | fail, `Expected:"compact"`, `Observed:"startup"`; the flag is cleared |
| `TestSentinelRoundTrip` | write a transcript whose tail contains the rendered sentinel | `ScanTranscriptTail` true; the assertion reports OK |
| `TestSentinelNotObservedGivesTwoChances` | `DeclareProducer(CAdditionalContext)`; transcript without the token | `Chances` 1 → OK; `Chances` 2 → fail `SevCritical` |
| `TestAdditionalContextGatedUntilRehydrator` | run with and then without `DeclareProducer(CAdditionalContext)` | without → `OK:true, SevInfo, Observed:"not-yet-implemented"` and the check body never executes (assert with a counting closure); with → the real check runs at `SevCritical` |
| `TestPreCompactTimeoutUnknownIsOK` | `History.PreCompactTimeoutMs == 0`, producer declared | OK, `Observed:"timeout-unknown"` |
| `TestCustomInstructionsProbePhrase` | `History.PreCompactInstr` with a 40-char first line; transcript containing / not containing it | OK / `SevWarn` fail |
| `TestMarkerIsWrittenByFlushAndCheckpointOnly` | grep the `contract` and `daemon` sources; then drive `session.start` alone against an empty `run/` | no `marker.json` after `session.start`; one after `flush`; one after `checkpoint` |
| `TestSentinelRenderIsAnInertComment` | — | exactly `<!-- qompack-contract-probe <12hex> -->`; token is 12 lowercase hex chars |
| `TestHookPayloadShapeRejectsEmptySession` | `Event{HookEventName:"PostToolUse"}` | fail, `Observed` names the missing field |
| `TestPluginRootUnsetIsOK` | `CLAUDE_PLUGIN_ROOT` unset | OK, `Observed:"unset"` |
| `TestHistoryPersistsAcrossMonitors` | monitor A degrades; construct monitor B on the same path | B reports `ModeDegradedPassive` before running anything |
| `TestDegradeIsIdempotent` | degrade twice with the same reason | one `Loud` line; two with different reasons → two lines |

### Commit 5

```
feat(contract): G9.3 assertion set, fail-loud degradation, two-clean-run restore

Every assertion is a real observation rather than a version check, and an assertion
whose producer is absent from the build reports SevInfo/not-yet-implemented so a
wave-1 build cannot degrade itself into passivity and silently disable the paths it
is meant to be testing.

Refs: SP-05, G9.3, §9, §12
```

- [ ] Write `internal/contract/monitor_test.go`, `assertions_test.go`, `sentinel_test.go` first — starting with `TestFreshBuildReportsModeFull`, which is the CI test §12.1 explicitly demands; confirm they fail.
- [ ] Add `internal/contract/contract.go`, `monitor.go`, `assertions.go`, `history.go`, `sentinel.go`, `marker.go`, `producers.go`.
- [ ] Add fixtures `testdata/golden/contracts/contract/{history_degraded.json,transcript_with_sentinel.jsonl}`.
- [ ] Flip off the `t.Skip`s in SP-01's `contracttest` conformance suite (if present) and make it pass.
- [ ] Run `go test ./internal/contract/... -race -count=2`; run `go test ./internal/... ` to confirm nothing else regressed.

