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
every poll (the transport-side twin of the v4 rig's in-process `Drain`) until **(a)** `index/tool_use.jsonl`
holds the 61 tool/stop records, **(b)** no `client-*.ndjson` spool remains, and **(c)** every prompt is
accounted for: `index lines + Counters["observer.err.prompt.put"] ≥ 65`, read from the daemon's own
`OpStatus` on each poll. Then `before := OpStatus` and the exact reconciliation
`len(index lines) == 65 − before.Counters["observer.err.prompt.put"]` (with the counter ≤ 4), then
`rep := qompack status --json`, `after := OpStatus`.

Why the wait is not "65 index lines": the two halves of the mix fail differently, and only one of
them is recoverable.
- **Tool/stop (spool path):** a delivery the daemon cannot process inside the hot-path ACK deadline
  is NAKed, the hook spools it (`client-*.ndjson`) and exits 0, and Drain replays it until its index
  record lands. The wait's (a)+(b) accept exactly the live count the daemon took for these and let
  Drain finish the rest; the record count for them is always 61.
- **Prompt (soft-drop path):** `observe.prompt` always ACKs and runs the observer's `ObservePrompt`
  synchronously under `promptReplyDeadline` = 250 ms (`internal/daemon/handlers.go`). When the
  deadline wins, the cancelled context makes the verbatim `store.PutBytes` return (`internal/store/put.go`),
  `internal/observer/prompt.go` absorbs it via `o.soft(stagePromptPut)` — a Warn to the day log plus
  the counter `observer.err.prompt.put` (`observer.go` `counterErrPrefix` + `prompt.go`
  `stagePromptPut`), never a LOUD line, never a spool — and skips `recordPrompt`, so no index record
  is written. WAL replay of an `observe.prompt` runs only the sentinel scan, so no later Drain can
  recover it. The delivery is still one `hook_controlled` and one `l0_ingest` sample (the WAL append
  and the timing happen before the call), which is why 65 stays the histogram upper bound while the
  index promise is 61 + (prompts that landed). The counter is named in the test as
  `x1v5PromptPutErrCounter`, with the citation.

The wait's timeout message is `x1v5WaitDiag`, a Stringer rendered at format time that says which of
the three mechanisms it waited on: index lines vs. 61/65, any undrained client spool, and — from a
direct `OpStatus` read — `hook_controlled.N`, `l0_ingest.N`, `l0_accept_error`, every non-zero
`observer.err.*` counter, and how many prompts were soft-dropped vs. still unaccounted for; then
the LOUD tail. (The shared `obsWaitDiag` knows only the spool shape and reported a soft-drop as
"processing behind its ACK".)

**`live`** — status from the resident daemon:
- Exit 0; envelope `schema`/`command`/`ok:true`/no error; `StatusReport.schema`; one hook row per
  `pluginmanifest.HookEntryPoints()` and one budget row per `obs.Budgets()`, in order.
- `primary` = `{source: daemon, status: available, age_ms non-null and ≥ 0, reason empty}`;
  `snapshot` forwarded.
- **Agreement with the authoritative payload:** `mode` (`"full"`), `hot`, `contract` equal to the direct
  read; every histogram the daemon served is forwarded, and when its `N` equals the direct read's
  (the normal case: the drained wait leaves nothing in flight and `OpStatus` is not a hot-path op) the
  whole `HistSnapshot` — `N`, p50, p95, p99, p999, max — must be **equal** to the direct read, since a
  histogram is a pure function of its samples; only when `N` moved is the `[before, after]` band
  accepted instead. No histogram is forwarded that the daemon never served; every counter lies in
  `[before, after]`. The displayed B-A and B-B rows are then checked quantile-for-quantile against
  `before.Latency` (the direct read) as well as against the forwarded snapshot, guarded by the same
  `N` equality, so a status that forwards the right count with the wrong quantiles cannot pass.
- **Live-delivery count:** `hook_controlled.N` ≥ 1 and ≤ 65, and equal to `hook_controlled_observed.N`
  and to `l0_ingest.N` (three instruments, one number); the session row for `sess-e2e-v5-x01` is live,
  its `Events` equals the daemon's own row and equals `hook_controlled.N` (registry vs. metrics); the
  status read registered no session of its own.
- **Qualification (`x1v5RequireQualified`, every arm):** a row displays a value iff `status == available`;
  an available row has `N > 0` and no reason; an unavailable row has `latency == null` and a non-empty
  reason; every row's `source` and `age_ms` equal the primary's.
- **B-A:** available, `measure: estimated`, `aggregate: true`, `covers` = the six non-PreCompact hooks in
  hooks.json order, latency equal quantile-for-quantile to the forwarded `hook_controlled` **and** to
  the directly read `hook_controlled` when their `N` agree, and every
  quantile ≥ the forwarded `hook_controlled_observed` (an estimate is never below the lower bound it was
  derived from).
- **B-B:** available, `measure: observed`, `aggregate: true`, empty `covers`, equal to the forwarded
  `l0_ingest` and to the directly read one when their `N` agree.
- **Report figures:** one `t.Logf` (`V5 4.1 live: index_tool_uses= hook_controlled.N= l0_ingest.N=
  B-A p99= B-B p99=`) records the values the V5 report's §8.4 row (`tool_uses=`, `p99=`) cites, so the
  row can be filled from a run artifact instead of a re-run.
- `hook_controlled_observed` is forwarded in the snapshot but is nobody's budget row.
- **Missing telemetry:** B-D unavailable regardless (uninstrumented by construction); any budget whose
  histogram the daemon served with `N > 0` is available; any it did not serve (or served empty) is
  unavailable with a reason naming that histogram. All seven hook rows unavailable with `latency == null`:
  PreCompact's reason names `checkpoint_finalize` (its own instrument, no samples); the six others'
  reasons name `hook_controlled` (folded into the aggregate, not attributable).

**`daemon_down_falls_back_to_persisted_snapshot`** — negative control 1, real switch: `admin.shutdown`
awaited until the process is gone (`e2eShutdownIfReachable`).
- `metrics/latency.json` exists. For `hook_controlled` **and** `l0_ingest`: when the persisted `N`
  equals the last live read's (`after`) `N` — the normal case — the whole `obs.HistSnapshot` (N, p50,
  p95, p99, p999, max) must be **equal** to the served one (`obs.Snapshot.Hists` and
  `daemon.StatusSnapshot.Latency` are both `map[string]obs.HistSnapshot`), so a Persist that wrote the
  right count with the wrong quantiles cannot pass; only when `N` moved is the band
  `after.N < persisted.N ≤ 65` accepted.
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

The live arm proved itself non-vacuous during authoring by **finding a real defect**: on the first
run, `primary.age_ms` was `-1` on a live answer (assertion `age_ms ≥ 0` failed). See the production
fix below.

**Third negative control — source sabotage of the live forwarding path (review finding, fixed).** The
adversarial review showed the first version's live agreement was count-only: with
`internal/cli/qompack_commands.go` `fetchDaemonStatus` mutated to add one microsecond to every
forwarded histogram's p99, the whole test stayed green (`scratchpad/x01-review-sabotageD.txt`, PASS
5.44 s), because the B-A/B-B rows were compared to status's *own* forwarded snapshot rather than to
the direct read. After the fix the same one-line edit, applied after the `sessions` loop in
`fetchDaemonStatus`:

```go
for k, hs := range snap.Latency {
	hs.P99 += time.Microsecond
	snap.Latency[k] = hs
}
```

turns the live arm **RED** at the histogram loop (`v5_x01_test.go:308` in round 2; `:452` on the
current tree after the wait rewrite): `histogram "l0_ingest": same sample count, so status
must forward the daemon's own quantiles` — expected `{N:65 … P99:106496000}`, actual `{N:65 …
P99:106497000}` (`scratchpad/x01-fix2-sabotageD.txt`, exit 1; arms 2 and 3 still pass, as they must,
since the disk path never goes through `fetchDaemonStatus`). The edit was reverted with
`git checkout -- internal/cli/qompack_commands.go`; `git diff` on that file is empty and the branch
carries no change to it beyond the age-clamp fix commit.

**Diagnostic fix (review finding, minor).** `loudLines(t, root)` passed as a `require.Eventually`
message argument was evaluated before the wait began, so a timeout showed the pre-wait LOUD tail.
Replaced with `x1v5LoudDiag{root}`, a Stringer rendered only when the failure message is built (the
same deferral `obsWaitDiag` uses). The setup wait now prints `x1v5WaitDiag` (above), which folds the
LOUD tail in after the daemon's own counters.

**Round-4 review findings (fixed).** (1) The setup wait floored the index at 65 lines and could not
complete under co-load, because a prompt that loses the 250 ms reply deadline never produces an index
record and nothing replays it (the reviewer's 7-of-9 timeouts at exactly 61 lines, and the base-tree
sibling `TestE2E_ObserverThroughDaemon` at 41 = 40 + 1). Fixed as described under **Setup**: the
wait is on the 61 spool-path records plus the daemon's own `observer.err.prompt.put` count, then an
exact partition equality. (2) Arm 2 compared the persisted histogram to the live one on `N` only;
now the whole `HistSnapshot` when `N` is unchanged, for both `hook_controlled` and `l0_ingest`.
(3) The wait diagnostic named the soft-drop shape as "processing behind its ACK"; `x1v5WaitDiag`
renders the daemon's counters and sample counts so the mechanism is named.

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
| `data.store.tool_uses == 65`, `dedup_ratio > 1.0`, `objects > 0` | **retired:** no `store` section exists in `StatusReport`; the observer's processing is asserted in setup through `index/tool_use.jsonl == 65 − observer.err.prompt.put` (61 spool-path records always; a prompt's record only when its verbatim capture did not lose the daemon's 250 ms reply deadline — production does not promise 65) |
| `data.sketches.bloom.fill_ratio == 0` | **retired:** no `sketches` section exists in status |
| `data.frontier.turn` / `residual_tokens` present and non-negative | **retired:** no `frontier` section; SP-12's runtime is not surfaced by status on this tree |
| `data.latency.hook_controlled.observe_tool` row with `n ≥ 60` and `p99 < 15ms` | **retired as unsafe, replaced:** SP-14 refuses a per-hook `observe_tool` row because `hook_controlled` mixes every delivering hook (six identical figures would be six claims nobody made) — the test asserts the PostToolUse hook row is *unavailable* with that reason and that B-A is `aggregate`, `estimated`, and equal to the daemon's own histogram. `n ≥ 60` → `1 ≤ N ≤ 65` equal across three instruments and the registry (a delivery that misses the hot-path ACK deadline spools, exits 0, and is never a histogram sample; this was observed on a co-loaded run). `p99 < 15ms` retired: §5 forbids a universal 15 ms as an acceptance fact; the test asserts the displayed quantiles equal the authoritative ones instead |
| `data.mode.mode == "full"`, `data.mode.degraded == false` | **corrected:** `snapshot.mode == contract.ModeFull.String()` and equal to the direct read; no `degraded` boolean exists |
| `data.unavailable` is empty | **corrected/inverted:** per-row provenance replaced the list; the test asserts exactly which rows are unavailable and why (B-D, every hook row, every unsampled budget) and that no unavailable row displays a value |
| (none) | **added:** disk fallback and nothing-observed arms; qualification rule; estimate ≥ observed lower bound; `hook_controlled_observed` served but not a budget row; the status read registers no session; full-quantile equality with the direct read when `N` is unchanged (review round 2) |

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
  the live count the daemon actually took **for the tool/stop (spool-path) deliveries**. That rewrite
  did not cover the prompt soft-drop shape — see round 4 below.
- First form, two consecutive runs: PASS (10.30 s) and PASS (8.02 s), all three subtests passing.
- Review round 2 (quantile agreement against the direct read, lazy LOUD diagnostic, report `t.Logf`):
  - sabotage applied (`fetchDaemonStatus` p99 += 1µs): **FAIL**, `live` at `v5_x01_test.go:308`, arms 2
    and 3 PASS (`x01-fix2-sabotageD.txt`, 6.43 s);
  - sabotage reverted, two consecutive runs: PASS (7.59 s) and PASS (9.55 s), all three subtests. Logged
    figures: run 1 `index_tool_uses=65 hook_controlled.N=65 l0_ingest.N=65 B-A p99=15360us B-B
    p99=163840us`; run 2 `… B-A p99=16384us B-B p99=106496us` (co-loaded machine; the figures are
    the daemon's own and are not asserted against a bound).
- Review round 3 (2026-09-09, independent re-proof of round 2 on the committed tree, HEAD `9335690`,
  no source change): `go test -list` selects the test (`x01-r3-list.txt`); the same one-line
  `fetchDaemonStatus` p99 += 1µs sabotage applied: **FAIL**, `live` at `v5_x01_test.go:308`,
  `histogram "hook_controlled"` expected `P99:14336000` actual `P99:14337000` with `N:65` on both
  sides, arms 2 and 3 PASS (`x01-r3-sabotage.txt`, exit 1, 4.63 s); reverted with `git checkout`,
  `git diff` empty; two consecutive runs PASS (4.79 s) and PASS (6.20 s), all three subtests
  (`x01-r3-run1.txt`, `x01-r3-run2.txt`). Logged figures: run 1 `index_tool_uses=65
  hook_controlled.N=65 l0_ingest.N=65 B-A p99=15360us B-B p99=73728us`; run 2 `… B-A p99=13312us
  B-B p99=81920us`. gofmt, vet and the five devtool sub-checks: clean/PASS (`x01-r3-lint.txt`).
- Review round 4 (2026-09-09, the prompt soft-drop wait, arm-2 full-snapshot equality, the wait
  diagnostic). The reviewer's failure shape: 7 of 9 focused runs timed out at
  `v5_x01_test.go:272` after 64–85 s with `index holds 61 lines; no undrained client spool` (six at
  exactly 61 = 60 tool + 1 stop, one at 64), and the untouched base-tree sibling
  `TestE2E_ObserverThroughDaemon` at 41 = 40 + 1 — the shortfall is the prompt count every time, and
  the reviewer's four sabotage attempts died in that setup before the live arm ran. After the fix:
  - `go test -list` selects the test (`x01-r4-list.txt`).
  - Run 1: **PASS** (13.81 s), all three subtests, **with the soft-drop shape live**: `V5 4.1 setup: 4 of
    4 prompts lost the observe.prompt reply deadline and were soft-dropped (counter
    observer.err.prompt.put); index holds 61 records`; live figures `index_tool_uses=61
    hook_controlled.N=65 l0_ingest.N=65 B-A p99=36864us B-B p99=163840us` — the 65 samples against
    61 records is exactly the mechanism above: a deadline-lost prompt is still a delivery the daemon
    timed and appended (`x01-r4-run1.txt`).
  - Run 2: **PASS** (5.89 s), no soft-drop this time: `index_tool_uses=65 hook_controlled.N=65
    l0_ingest.N=65 B-A p99=16384us B-B p99=122880us` (`x01-r4-run2.txt`). Both shapes green.
  - Sabotage (`fetchDaemonStatus` p99 += 1µs, same edit as above): **FAIL**, `live` at
    `v5_x01_test.go:452` (the histogram loop), `histogram "hook_controlled"` expected
    `{N:65 … P99:15360000 …}` actual `{N:65 … P99:15361000 …}`, arms 2 and 3 PASS
    (`x01-r4-sabotage.txt`, exit 1, 5.48 s). Reverted with `git checkout --
    internal/cli/qompack_commands.go`; `git diff --stat` on the file empty.
  - The lint line below was re-run on this tree: clean/PASS (`x01-r4-lint.txt`).
- `gofmt -l ./test ./internal`: clean. `go vet ./test/e2e ./test/integration`: clean.
- `go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns`: all five PASS.
- `go test -count=1 ./internal/commands` and `./internal/cli`: ok (first round; the CLI file is
  unchanged since).
