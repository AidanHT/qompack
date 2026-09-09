# V5-VERIFY §4.15 disposition

| | |
|---|---|
| Identifier (retained) | `TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent` |
| Current criterion (plans/V5-VERIFY §4, row 4.15) | SP-14/SP-21 degraded/error/privacy behavior, no absence or misleading success on failure. |
| Disposition | **authored** — one production fix was unavoidable and is in its own commit (see "Production change") |
| Level and file | e2e — `test/e2e/v5_x15_test.go` (real binary, real spawned daemon; nothing composed in-process) |
| Base | `verify/v5` @ `87c0c1d`, branch `v5/x15` |

## Why e2e, and why the real spawned daemon

The historical 4.15 seam was the V4 in-process rig (idle `ran` list, in-process MCP handlers,
recording counts). Those seams are already pinned by the still-live rows V4 §4.12
(`test/e2e/v4_x12_test.go`) and V3 §4.8 (`v3_x08_test.go`). The CURRENT criterion names SP-14
(slash commands, observability) and SP-21 (admission) — surfaces a user reads through the binary —
so this row drives `qompack status`, `qompack recall`, `qompack dropped` and `qompack config print`
as processes against the daemon internal/cli's own `runDaemon` composes (started by the binary's
lazy-spawn and session-start paths). That is the only composition in which the SP-14 frontends,
their MCP proxy, the installed tool set and the contract monitor are all the shipped article.

## Test names and what each asserts

All five are subtests of the retained identifier.

| Subtest | Producers | Asserts |
|---|---|---|
| `full_reference` | real daemon via `session-start`; `qompack status` (`--json` and human) | envelope `ok:true`, `primary.source=daemon/available`, `snapshot.mode=full`, no critical failing result; human render carries `mode: full` and never the critical severity text. Every hook/budget row is *qualified*: a figure only with `available` provenance and `n>0`, otherwise no figure, non-`available` status and a reason (`x15v5AssertQualifiedRows`, applied to every report in every arm). |
| `degraded` | `contract.Monitor.Degrade` over `state/contract.json` before any daemon; daemon brought up by the LAZY-SPAWN path (first PostToolUse) so status is read before any `RunAll`; then exactly two SessionStarts | (1) `status --json`: `snapshot.mode=degraded-passive`, the persisted failing result is present by ID with `ok:false`/critical; human render leads with `FAILING`, names `v5.x15.forced`, prints `critical — degrades the session when observed`, `expected:`/`observed:`. (2) ONE compact SessionStart: exit 0, no `hookSpecificOutput`; mode still degraded; session registered live. (3) Recording continues: a prompt, 3 tool turns and all ten §5.22a secret families through real PostToolUse hooks, each with nil `hookSpecificOutput`; `index/tool_use.jsonl` holds exactly one record per event (15). (4) SP-21 privacy: every object under `objects/` decompressed and swept — no secret literal present, and the `«redacted:` placeholder IS present (stored redacted, not dropped). (5) `qompack checkpoint`: exit 0, no `customInstructions`, no artifact, no manifest line. (6) `recall <marker> --json`: exit 0, `ok:true`, `is_error:false`, `found:true`, the marker capture among the hits by `tool_use_id`. (7) `dropped --json`: exit 0, `ok:true`, `drops` array present, `count==len(drops)`, empty (no rehydration ran); an `available:false` answer must carry a reason. (8) LOUD: exactly one `degrading to passive`, zero `restoring full`. (9) Second SessionStart, `source=compact` (the PreCompact armed `AwaitingCompactStart`): mode restored to `full`, exactly one `restoring full` LOUD line. (10) `p.AssertAppendOnly(t)`. |
| `daemon_disabled` | project config `runtime.daemon.enabled=false`; no daemon, no lazy spawn possible | `status --json`: `primary.source=none`, status not `available`, reason names both `daemon` and `disk`, `snapshot` is null, every hook and budget row has no figure; human render prints `source: none` and NO `mode:` line. `recall` and `dropped` with `--json`: exit 1 (`commands.ExitError`), `ok:false`, error kind and message non-empty, any data member marked `is_error`. `ipc.Probe` false afterwards (no daemon spawned). |
| `admission_switch_refused` | project config `runtime.migration.replacement.newResult=true`; `qompack config print --json` | effective value is `false`; `state/config-violations.json` (removed before the command so the COMMAND writes it) names the key with `got=true`, `want=false`, non-empty message; `config.MigrationGates()` still reports the SP-21 gate unpassed (guard: a build that flips it must retire this arm, not weaken it). Control: a default project prints `false` and records no refusal for the key. |
| `config_corrupt_refuses_capture` | `QOMPACK_FAULT=config-corrupt` on a real PostToolUse hook | exit 0, no `hookSpecificOutput`; `logs/hook-quiet-*.jsonl` exists with a line carrying `err`; no spool file, no index record, `ipc.Probe` false (nothing admitted, nothing sent, nothing spawned). |

## Negative controls and how they were proven

1. **Real switch, SP-14 surfaces (`daemon_disabled`)**: the producer is severed through the
   config key `runtime.daemon.enabled=false` — `ipc.Client.Send` step 2 spools without dialling and
   without lazy-spawning. The same three commands that answer `ok:true` in the degraded arm must
   answer source-none / exit-1 here. This arm passed on every run, including the runs in which the
   positive arm was still red, so the two arms are independently discriminating.
2. **Real switch, contract mode (`full_reference` vs `degraded`)**: the same `status` invocation
   reports `full` with no critical failure in one project and `degraded-passive` with the named
   forced result in the other; the difference is the persisted `state/contract.json` the daemon's
   own monitor loads at `New`.
3. **Real switch, SP-21 gate (`admission_switch_refused` vs its control)**: with the switch not
   asked for, no refusal is recorded; with it asked for, the effective value stays `false` and the
   refusal is on disk.
4. **Real fault site (`config_corrupt_refuses_capture`)**: the `config-corrupt` injection makes the
   privacy policy unloadable through the shipped `QOMPACK_FAULT` seam.
5. **The row found a real SP-14 defect during authoring**, which is the strongest evidence it is
   not vacuous: `qompack recall <query>` without `--k` was refused by the handler with
   `invalid arguments for recall: /k: below minimum` on every invocation through the real binary
   (details below). The row was red on that arm until the frontend was fixed.
6. **The production fix's own control**: `TestRecall_OmitsKWhenNotAsked` (internal/commands) was
   run with `internal/commands/cmd_recall.go` reverted to the base version (`git checkout --` on
   that one file, then restored from a copy) and FAILED (`--- FAIL: TestRecall_OmitsKWhenNotAsked`),
   then passed with the fix in place. `git diff` after the restore showed only the intended change.

## Production change (own commit, `fix(commands): ...`, Refs: V5-VERIFY section 4.15)

`internal/commands/cmd_recall.go`: the frontend marshalled `mcp.RecallArgs{Query: q}`, whose `K`
carries no `omitempty`, so an unset page size was sent as `k:0`. The registered `recall` schema
declares `k` with `minimum:1` and `default:5`; the default applies only when `k` is ABSENT, so
every `qompack recall <query>` was refused with `/k: below minimum` through the real binary. The
frontend's unit tests record the call without applying the schema, which is how it shipped. Fix:
a package-local `recallArgs` wire shape with `k,omitempty`; `mcp.RecallArgs` (the handler's decode
type) is untouched. Regression test `TestRecall_OmitsKWhenNotAsked` added to
`internal/commands/retrieval_test.go`. The whole `internal/commands` package passes.

## Old-to-new assertion map (historical §4.15 text → this row)

| Historical expectation | Disposition |
|---|---|
| Force `contract.ModeDegradedPassive`, drive the same 60-event session as 4.1 | **Corrected.** Forced through the same `state/contract.json` seam; the session is 15 real hook events (marker, prompt, 3 turns, 10 secret families) — the row proves the control-flow split and the surfaces, not encoding volume. |
| Identical `PutBytes`/`RecordToolUse`/`AddNode`/CMS/HLL/Sequitur counts to the full-mode run | **Retired.** Cross-mode count identity is not assertable through the binary and was never a safe guarantee (the full arm and the degraded arm drive different event sets by design). "Recording continues" is kept as an exact per-event index count and the object sweep. |
| Elimination records still written | **Retired here; live in V3 §4.8** (`v3_x08_test.go`, `IngestMCP` while degraded). Not re-asserted. |
| No `additionalContext` on any hook | **Kept.** Every hook in the degraded arm asserts nil `hookSpecificOutput`, the compact SessionStart included. |
| No `customInstructions` on `PreCompact` | **Kept.** `qompack checkpoint` returns no `hookSpecificOutput`, no artifact, no manifest line. |
| No scheduler-initiated checkpoint from the idle tick | **Retired here; live in V4 §4.12** (`act.` prefix enumeration over the idle `ran` list). Not observable through the binary; "no artifact" is asserted. |
| No drop report emitted, no thrash warning | **Corrected.** `dropped` answers an empty, explained report; thrash warnings are SP-15's V5 §4.10 row, not this one. |
| `act.advance_frontier` absent from `RunOnce`'s `ran` list while `drain`, `gc`, `segment_blooms`, `warm_start` still run | **Retired.** `advance_frontier` carries NO `act.` prefix on this tree and runs while degraded (V4 §4.12 asserts exactly that); the idle list is V4 §4.12's seam. |
| MCP retrieval stays available: `recall`, `expand`, `already_tried` all answer | **Corrected.** `recall` and `dropped` are asserted through the SP-14 frontends over the real proxy and daemon (the criterion's surfaces); `expand`/`already_tried` remain V4 §4.5/§4.12's. |
| `/qompack:status` leads with the degraded banner | **Kept and strengthened.** Both `--json` and the human render: mode line, `FAILING` banner, the failing assertion by ID, critical severity text, expected/observed pair. Plus the SP-14 qualified-row rule on every row. |
| Two clean `SessionStart`s restore `ModeFull`, logged as loudly as the degradation | **Kept.** With the correction that the second must be `source=compact` after a PreCompact (the shipped `CSessionStartSourceCompact` assertion is critical). |
| (new, from the current criterion) SP-14 error behaviour: unreachable daemon | **Added** (`daemon_disabled`). |
| (new) SP-21 recorded-as-disabled, never as passed | **Added** (`admission_switch_refused`). |
| (new) SP-21 privacy under degraded mode; capture refusal on unloadable policy | **Added** (secret sweep in `degraded`; `config_corrupt_refuses_capture`). |

## Findings recorded, not chased (outside this row's fix scope)

- **Negative age in status provenance.** Human render read `source: daemon (available, -1000µs old)`.
  `fetchDaemonStatus` stamps `time.Now()` AFTER the round trip while `Invocation.Now` was read
  before it, so `age_ms` is `now.Sub(at)` < 0. Misleading by a millisecond; SP-14 owner.
- **The persisted config refusal loses the WHY.** `Validate` says `must be false: gate ... has
  not passed`, but `ViolationsFromWarnings` rebuilds `Message` from the generic
  `invalid value, using default: true not in false` form, so `state/config-violations.json` names
  key/got/want but not the gate. SP-19 config owner. The row asserts the record and reads the gate
  reason from `config.MigrationGates()` instead.
- **`status` counts an info-severity failure in the `FAILING` banner** (`mcp.server_registered`
  is `initialize-not-received` until an MCP client handshakes). Correct per `renderContract`, and
  the severity text distinguishes it; noted because a reader of the banner line alone cannot.

## Unverified remainder

- **SP-21 `admission.Pipeline` itself is not exercised here.** `internal/admission` has no
  composition-root adapter on this tree (no production package imports it; the memory note
  "admission ships off" and the package doc agree). Its degraded/error/privacy decision table
  (`OutcomeDeny` only from `StagePrivacy`, `ReasonPolicyUnavailable` passes through, disabled gate
  records `disabled`) is unit-tested at 100% in `internal/admission/*_test.go`; composing the ports
  in this row would assert a seam nobody ships. What IS assertable and asserted: the switch is
  refused and recorded (`admission_switch_refused`), and the shipped hook-capture privacy admission
  redacts before persistence while degraded and refuses capture when its policy cannot load.
- **SP-14 `checkpoint` and `eval` frontends** are not routed / not bound on this tree (H3,
  `EvalArtifacts`); their "unavailable" answers are unit-tested in `internal/commands` and not
  re-driven here.
- **`why` and `pin`** are not driven; `why` shares `runTool` with `recall`/`dropped`.
- Cross-mode count identity (historical) is retired, not deferred.

## Commands run and results

```
cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5-x15
go test ./test/e2e -list 'TestV5_DegradedPassive'
  -> TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent (exactly one match)
go test ./test/e2e -run 'TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent' -count=1 -timeout=30m -v
  -> PASS (10.90s), all five subtests; repeated: PASS (13.53s)
go test ./internal/commands -run 'TestRecall_OmitsKWhenNotAsked' -count=1   (fix reverted) -> FAIL
go test ./internal/commands -count=1                                        (fix in place)  -> ok
gofmt -l ./test ./internal   -> clean
go vet ./test/e2e ./internal/commands -> clean
go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns -> PASS
```

Timing: no budget gate is asserted by this row. The `daemon_disabled` arm spends ~3 s in the
MCP proxy's ten 150 ms retries per call (`recall`, `dropped`), which is the shipped behaviour for
an unreachable daemon, not a gate.
