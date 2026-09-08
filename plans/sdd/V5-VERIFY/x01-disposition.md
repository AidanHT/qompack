# V5-VERIFY §4.1 — `TestV5_ObserveToStatusRoundTrip` disposition

- **Retained identifier:** `TestV5_ObserveToStatusRoundTrip`
- **Current criterion** (plans/V5-VERIFY §4, row 4.1): "SP-14 status agrees with authoritative
  qualified observations and missing telemetry."
- **Disposition:** `authored` — with one unavoidable production fix (below) and one unverified remainder.
- **Base:** `verify/v5` @ `87c0c1d`. **Branch:** `v5/x01`.
- **Level and file:** e2e, `test/e2e/v5_x01_test.go`. The seam crosses three processes — the hook
  subcommands, the lazily spawned resident daemon, and `qompack status --json` asking it over the
  transport — so the in-process v4 rig would assert a composition nobody ships. The daemon is the one
  the first `session-start` spawns, exactly as a user's is.

## Producers on this tree

| Role | Producer | Where |
|---|---|---|
| Events in | `qompack session-start`, `observe tool`, `observe prompt`, `observe stop --subagent` (real binary) | `internal/cli` hook bodies |
| Authoritative observation | `ipc.OpStatus` → `daemon.handleStatus` → `daemon.StatusSnapshot` (registry, metrics histograms, counters, contract mode) | `internal/daemon/handlers.go` |
| Persisted observation | `obs.Registry.Persist` → `.qompack/metrics/latency.json`, written by `daemon.Stop` | `internal/daemon/daemon.go`, `metrics.go` |
| Qualified view | `commands.CollectStatus` → `StatusReport` (primary provenance, per-hook rows, per-budget rows) | `internal/commands/statuscollect.go` |
| Frontend | `qompack status --json` → command envelope | `internal/cli/qompack_commands.go`, `internal/commands/cmd_status.go` |

## Test structure and what each part asserts

`TestV5_ObserveToStatusRoundTrip` (one top-level test, three subtests):

**Setup.** `session-start`, then 60 `observe tool` (distinct ids/paths/content), 4 `observe prompt`,
1 `observe stop --subagent` — the historical mix. The wait drives `admin.drain` over the transport on
every poll (the transport-side twin of the v4 rig's in-process `Drain`) until `index/tool_use.jsonl`
holds 65 records **and** no `client-*.ndjson` spool remains, so nothing is in flight when the
authoritative reads are taken. Then: `before := OpStatus`, `rep := qompack status --json`,
`after := OpStatus`.

**`live`** — status from the resident daemon:
- Exit 0; envelope `schema`/`command`/`ok:true`/no error; `StatusReport.schema`; one hook row per
  `pluginmanifest.HookEntryPoints()` and one budget row per `obs.Budgets()`, in order.
- `primary` = `{source: daemon, status: available, age_ms non-null and ≥ 0, reason empty}`;
  `snapshot` forwarded.
- **Agreement with the authoritative payload:** `mode` (`"full"`), `hot`, `contract` equal to the direct
  read; every histogram the daemon served is forwarded and its `N` lies in `[before, after]`; no
  histogram is forwarded that the daemon never served; every counter lies in `[before, after]`.
- **Live-delivery count:** `hook_controlled.N` ≥ 1 and ≤ 65, and equal to `hook_controlled_observed.N`
  and to `l0_ingest.N` (three instruments, one number); the session row for `sess-e2e-v5-x01` is live,
  its `Events` equals the daemon's own row and equals `hook_controlled.N` (registry vs. metrics); the
  status read registered no session of its own.
- **Qualification (`x1v5RequireQualified`, every arm):** a row displays a value iff `status == available`;
  an available row has `N > 0` and no reason; an unavailable row has `latency == null` and a non-empty
  reason; every row's `source` and `age_ms` equal the primary's.
- **B-A:** available, `measure: estimated`, `aggregate: true`, `covers` = the six non-PreCompact hooks in
  hooks.json order, latency equal quantile-for-quantile to the forwarded `hook_controlled`, and every
  quantile ≥ the forwarded `hook_controlled_observed` (an estimate is never below the lower bound it was
  derived from).
- **B-B:** available, `measure: observed`, `aggregate: true`, empty `covers`, equal to `l0_ingest`.
- `hook_controlled_observed` is forwarded in the snapshot but is nobody's budget row.
- **Missing telemetry:** B-D unavailable regardless (uninstrumented by construction); any budget whose
  histogram the daemon served with `N > 0` is available; any it did not serve (or served empty) is
  unavailable with a reason naming that histogram. All seven hook rows unavailable with `latency == null`:
  PreCompact's reason names `checkpoint_finalize` (its own instrument, no samples); the six others'
  reasons name `hook_controlled` (folded into the aggregate, not attributable).

**`daemon_down_falls_back_to_persisted_snapshot`** — negative control 1, real switch: `admin.shutdown`
awaited until the process is gone (`e2eShutdownIfReachable`).
- `metrics/latency.json` exists; its `hook_controlled.N` ≥ the last live read and ≤ 65.
- `primary` = `{source: disk, status: available, age_ms non-null ≥ 0, reason contains "daemon:"}`;
  `snapshot == null`.
- B-A from disk equals the persisted histogram quantile-for-quantile, `measure: estimated`, `N` ≥ the
  live arm's `N`; B-B equals the persisted `l0_ingest`; B-A and B-B agree on `N`.
- Every budget row's availability matches the live arm's (B-D still unavailable); every hook row
  unavailable.

**`nothing_observed_is_reported_as_unknown`** — negative control 2, real switch: the spawned
fallback daemon is awaited up and shut down again, then `latency.json` is removed.
- Exit 0 and `ok:true` still; `primary` = `{source: none, status: error, age_ms null, reason contains
  "daemon:" and "disk:"}`; `snapshot == null`.
- Every hook and budget row unavailable with `latency == null`; every non-B-D row's reason is the
  report's own reason (nothing answered, so no histogram was "empty").

Ends with `p.AssertAppendOnly(t)`.

## Negative control — how it was proven

Two runtime switches, both production paths: the daemon shut down (fallback to the persisted
snapshot) and the persisted snapshot removed (nothing observed). Neither arm can pass on a report that
invents a value: the qualification rule fails on any row that displays a latency without
`status: available`, and arm 3 fails if any row is available at all.

The live arm additionally proved itself non-vacuous during authoring by **finding a real defect**: on
the first run, `primary.age_ms` was `-1` on a live answer (assertion `age_ms ≥ 0` failed). See the
production fix below. No source sabotage was needed beyond that.

Note on the status command's lazy spawn: a `qompack status` that finds no daemon spawns one, the same
seam `qompack mcp` uses. Arm 2 therefore leaves a fresh daemon coming up; the test waits for it and
shuts it down before arm 3 removes the file, so it can neither answer arm 3 live nor re-persist the
file underneath it. The cleanup handles arm 3's spawn.

## Production fix (own commit, first on the branch)

`fix(commands): clamp a live status answer's age at zero` — `internal/commands/statuscollect.go`
`ageMS`: `Invocation.Now` is read in `parse()` before the body runs, and `cli.fetchDaemonStatus`
stamps the answer with `time.Now()` after the round trip, so every live answer is a few milliseconds
younger than the report's clock and rendered as a **negative** `age_ms` (observed: `-1`). A negative
age is not a qualified observation. The fix clamps at 0 ("as fresh as the report"), keeps `null` for an
unknown age, and adds `TestStatus_LiveAnswerStampedAfterNowIsNotNegativelyAged` in
`internal/commands/statuscollect_test.go`. `go test -count=1 ./internal/commands` and
`./internal/cli` pass after the change.

## Old-to-new assertion map (historical §4.1 → this test)

| Historical expectation | Disposition |
|---|---|
| Start a daemon; `session-start`, 60 `observe tool` over 8 paths (3 re-read), 4 `observe prompt`, 1 `observe stop --subagent` | **kept (corrected):** same mix and counts; 60 distinct paths instead of 8 with re-reads — supersession is SP-06/SP-08's row, not status's, and distinct paths make the index count exact |
| `qompack status --json`, exit 0 | **kept** |
| `from_daemon == true` | **corrected:** `primary.source == "daemon"` with `status == "available"` and a non-null `age_ms` (SP-14 replaced the boolean with provenance) |
| `data.store.tool_uses == 65`, `dedup_ratio > 1.0`, `objects > 0` | **retired:** no `store` section exists in `StatusReport`; the observer's processing is asserted through `index/tool_use.jsonl == 65` in setup instead |
| `data.sketches.bloom.fill_ratio == 0` | **retired:** no `sketches` section exists in status |
| `data.frontier.turn` / `residual_tokens` present and non-negative | **retired:** no `frontier` section; SP-12's runtime is not surfaced by status on this tree |
| `data.latency.hook_controlled.observe_tool` row with `n ≥ 60` and `p99 < 15ms` | **retired as unsafe, replaced:** SP-14 refuses a per-hook `observe_tool` row because `hook_controlled` mixes every delivering hook (six identical figures would be six claims nobody made) — the test asserts the PostToolUse hook row is *unavailable* with that reason and that B-A is `aggregate`, `estimated`, and equal to the daemon's own histogram. `n ≥ 60` → `1 ≤ N ≤ 65` equal across three instruments and the registry (a delivery that misses the hot-path ACK deadline spools, exits 0, and is never a histogram sample; this was observed on a co-loaded run). `p99 < 15ms` retired: §5 forbids a universal 15 ms as an acceptance fact; the test asserts the displayed quantiles equal the authoritative ones instead |
| `data.mode.mode == "full"`, `data.mode.degraded == false` | **corrected:** `snapshot.mode == contract.ModeFull.String()` and equal to the direct read; no `degraded` boolean exists |
| `data.unavailable` is empty | **corrected/inverted:** per-row provenance replaced the list; the test asserts exactly which rows are unavailable and why (B-D, every hook row, every unsampled budget) and that no unavailable row displays a value |
| (none) | **added:** disk fallback and nothing-observed arms; qualification rule; estimate ≥ observed lower bound; `hook_controlled_observed` served but not a budget row; the status read registers no session |

## Unverified remainder

- **SP-14 status against SP-12 scheduler and SP-16 warm-start surfaces** (historical `frontier`,
  `scheduler.breakdown`): `daemon.Options.StatusExtra` exists but nothing on this tree binds it in the
  shipped composition (`grep StatusExtra internal --include=*.go` finds only its declaration and the
  scheduler tap's note that it is not decorated), so `snapshot.extra` is absent and there is no
  producer to assert against. Recorded as unverified, not as passed.
- **PreCompact's own instrument (B-E) displayed as available**: this row never runs a PreCompact, so
  B-E is asserted only on its honest unavailable path. The available path is covered by SP-14's unit
  tests and by V4 §4 rows that seal checkpoints.
- **Latency budgets as acceptance facts** (`p99 < 15ms`): deliberately not asserted (§5).

## Run

```
cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5-x01
go test -list 'TestV5_ObserveToStatusRoundTrip' ./test/e2e      # prints the name: pattern selects it
go test -count=1 -run '^TestV5_ObserveToStatusRoundTrip$' -v ./test/e2e
go test -count=1 -run '^TestStatus_LiveAnswerStampedAfterNowIsNotNegativelyAged$' ./internal/commands
```

Results on 2026-09-08 (this machine, other agents co-loaded):
- Run before the fix: FAIL — `live`: `age_ms` `-1` (the defect above). Two subsequent runs then FAILED
  in setup on a co-loaded machine: 11–12 of 65 deliveries spooled and the daemon's own idle tick did
  not drain them within 60 s; the wait was rewritten to drive `admin.drain` per poll and to accept
  the live count the daemon actually took.
- Final form, two consecutive runs: PASS (10.30 s) and PASS (8.02 s), all three subtests passing.
- `gofmt -l ./test ./internal`: clean. `go vet ./test/e2e ./test/integration`: clean.
- `go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns`: all five PASS.
- `go test -count=1 ./internal/commands` and `./internal/cli`: ok.
