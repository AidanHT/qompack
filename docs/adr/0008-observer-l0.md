# 8. Observer L0: capture, tombstones, supersession, and the Phase 1 exit

Date: 2026-08-25

## Status

Accepted. Implemented by SP-08 (`internal/observer`, `internal/daemon/observer_ops.go`, the
Commit 6 wiring in `internal/cli/daemon.go`, and the `test/e2e/phase1_exit_test.go` harness).

## Context

Qompack.md §8.1 gives layer L0 the observation responsibilities: content-address every tool
result, canonicalize per tool, near-dedup (O2), addressable tombstones (G3.2), redundancy /
supersession detection, verbatim prompt capture (G2.3), subagent capture (G10.1), and the
task-boundary signals (G1.5). §10 Phase 1 grades the slice on one exit criterion:

> store size vs. raw transcript ratio >= 4:1 on read-heavy sessions (measure with and without
> canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

This ADR records the twelve resolved design decisions, the `arch/sp08-observer-seams`
amendment, every controller ruling that changed the plan's shape while the branch was executed,
the tombstone grammar and supersession rules as shipped, and the measured numbers.

## The twelve resolved decisions

1. **Pipeline order.** The observer never chunks, redacts, or canonicalizes by hand: it calls
   `store.PutBytes` with `PutOptions{Tool, Path, Canon}` and SP-06's store performs
   redact -> canonicalize -> chunk internally. The observer's contribution to O2 is per-tool
   canonicalizer selection. Its own ordering after the Put: index -> file version -> sketches ->
   DAG -> grammar -> signals/features -> tombstone.
2. **`crlf` is unconditional.** `canonOptions` always includes `canon.Class("crlf")` even with
   `store.canonicalize.enabled=false`, so the Phase 1 with/without measurement compares
   crlf-only against crlf + the six configured classes — the honest A/B.
3. **§8.1 item 7's destination.** The design names `index/segments.jsonl`; the shipped segment
   log is `store.SegmentLog` and carries no per-turn payload field (W-3 forbids adding one).
   Verbatim prompts are met with three durable artifacts, none regenerated from a summary:
   (a) prompt bytes content-addressed via `store.PutBytes` with no optional canonicalization
   class and no MinHash (amendment (a) makes that request reach the store); (b) an append-only
   `tool_use.jsonl` entry with `Tool: "UserPromptSubmit"` and `ID: VerbatimPromptID(...)`;
   (c) a `KindUserPrompt` DAG node enrolled into the open segment (`userprompt:<turn> ->
   segment:<id>`, members point into the segment, SP-07 D-1). This closes G2.3.
4. **Turn accounting.** `state.Turn` starts at 0; `OnUserPrompt` records then increments;
   `OnToolUse` records without incrementing; `OnStop` increments for both subagent directions —
   a monotone alternating turn sequence.
5. **`Node.Pos`.** `state.PrefixTokens` is a monotone per-session token counter; every node gets
   `Pos = state.PrefixTokens` (start position), then the counter advances by `node.Tokens`.
6. **Ephemeral.** A tool named `mcp__qompack__*` is a retrieval result: `Ephemeral: true`,
   excluded from supersession in both directions, not fed to CMS/HLL/Misra-Gries; still gets DAG
   nodes and a tombstone.
7. **Error policy.** No I/O failure escapes an Observer method; every stage is wrapped by
   `o.soft(stage, err)` (counter `observer.err.<stage>`, Warn log, return). Methods return a
   non-nil error only for `ctx.Err()`; output is `hookio.Empty()` except the thrash warning
   (`OnUserPrompt`, ModeFull only) and the Rehydrator passthrough (`OnSessionStart`,
   compact/clear).
8. **Nil tolerance.** `Grammar`, `Touch`, `Explore`, `Hot`, `Symbols`, `Rehydrate`, `Mode`,
   `OnSignals`, `OnFeatures`, `Metrics` may each be nil. `New` errors only on empty
   `ProjectRoot` or nil `Store`/`Graph`/`Log`/`Clock`.
9. **Concurrency.** Two-level locking: `o.mu` guards only the session map and the state-file
   write; each `sessionState` has its own mutex held for the rest of the entry point. Never
   `o.mu` while holding a session lock. (One premise correction shipped in Commit 2:
   `internal/sketch` documents its types as NOT internally synchronized, so the observer guards
   the three shared sketches with its own `sketchMu` — see the Task 2 report.)
10. **Mode gates output, never writes.** §12: degraded-passive keeps L0 recording; `o.mode()` is
    consulted exactly once, at the `AdditionalContext` emission in `prompt.go`.
    `TestModePassiveStillWrites` pins identical store/DAG/sketch call counts across modes.
11. **Time.** One `core.UnixMilli(o.opt.Clock.Now().UnixMilli())` per entry point; `time.Now()`
    never appears in the package.
12. **`canon.Options.MinHash` type.** Spelled `sketch.MinHashOptions{...}` at call sites, per
    §5.6/§5.7.

## The `arch/sp08-observer-seams` amendment

Landed on `develop` before the branch was cut; its commit is not one of this branch's seven.

- **(a)** `store.PutOptions.Canon`: a nil `Canon.Strip` means "no per-call override"; a non-nil
  (possibly empty) `Strip` means "this canon.Options is mine, MinHash included" — an empty
  non-nil Strip requests no optional class, and `MinHash.Enabled=false` disables the signature
  for that Put only when `Strip != nil`. This is what lets the verbatim prompt path request
  "nothing optional" without zeroing `PutResult.Signature` for every plain `PutOptions{}` in
  the tree.
- **(b)** The subagent's name survives the IPC boundary: `internal/cli/hookclient.go` resolves
  the agent name client-side into `{"subagent":true,"agent":"<name>"}`, and
  `internal/daemon/handlers.go`'s `resolveEvent` re-populates `Event.Extra` from `req.Raw`.
- **(c)** `Services.Mode func() contract.Mode` (SP-05's struct widened, W-3); `contract.NewMonitor`
  hoisted above the bind loop in `daemon.New` so no bind captures a nil mode source.
- **(d)** `handleObservePrompt` invokes the `ObservePrompt` seam under `mode.MayRecord()` (the
  same gate as observe.tool/observe.stop) and attaches the returned Output to the reply only
  under `mode.MayAct()` — degraded-passive records verbatim prompts, ModeOff does not; the WAL
  drain never re-dispatches prompt events, verified before implementing.

## Controller rulings that changed the plan's shape

Process rulings (devtool runs one task per invocation; the local gate set is `ci-local` +
`test-race` + `bench-hotpath` + `replay` + `build-all`; commits executed sequentially; four
over-long commit subjects shortened) are recorded in the SDD ledger. The rulings that changed
the shipped shape:

- **`WireObserver` opens the seams.** No production code opened a store or DAG;
  `daemon.WireObserver(o *Options)` now opens store/DAG/SketchSet when the corresponding
  `Options` field is nil and assigns them back, keeping `runDaemon`'s diff to the two wiring
  blocks (plus two sanctioned additions: a deferred `opts.Store.Close()` after `Run` and a
  startup `os.Remove` of the store's empty quarantine scaffolding — V3-VERIFY to decide whether
  `store.Open` should stop pre-creating it).
- **`landedSubplans` at merge.** `tools/devtool/cover.go`'s floor list gains SP-08 in the merge
  commit, not on the branch (SP-06/07 precedent; a tripwire test enforces it). Commit 7 records
  the measured coverage below instead.
- **`features.go` (and `prompt.go`'s thrash helpers) moved from Commit 4 into Commit 2**, whose
  `OnToolUse` steps 12-13 need them; Commit 4 added `OnUserPrompt`/`VerbatimPromptID` to
  `prompt.go`; Commit 3 wired `OnToolUse` step 8 (supersession), which Commit 2 left nil.
- **Import-set pin.** devtool's import-graph check permits `negknow`/`chunk` for the observer;
  the plan's stricter set is pinned by a source-level test (`TestObserverImportSetIsExact`).
- **`ExtractSignals` regexes.** `reTestFail[2]` is case-sensitive `\bFAILED\b` (the plan's
  `(?i)` made cargo's "0 failed" a failure); lowercase counted failures are caught by
  `\b[1-9]\d*\s+failed\b`.
- **`BenchmarkTombstone` alloc clause struck.** The "0 allocations beyond the returned string"
  clause was unimplementable with `humanBytes` frozen; the < 2 us/op clause stays; the measured
  allocations are recorded below.
- **Behaviour suite lifts at Commit 2** with minimal bodies for the entry points owned by
  Commits 4-6; the Rule W-1 skip message stays on the `fake-stub` factory only.
- **`observer.neardup` counts at the Put (tooluse.go step 5a), not inside the supersession
  scan** — the store's near-dup signal exists for pathless content too, and the plan's placement
  read ~0 on exactly the §8.1 item 1 class it exists for.
- **Supersession filters.** A 7th filter skips priors with a zero Root (empty results are never
  superset material and would otherwise bump `err.supersede.root` forever); the near-dup arm
  superseding a strictly smaller later read is intended (§8.1 item 3's own wording) and pinned
  by a (subset AND Jaccard >= threshold) row.
- **Parked (V3-VERIFY).** Unredacted prompt text reaches `index/tool_use.jsonl`'s `ArgsPreview`
  (<= 120 bytes) — the preview writer is SP-06's and the observer may not import `redact`;
  V3-VERIFY must rule whether SP-06's index writer applies `deps.Redact` to previews.
- **Sanctioned Task 6 deviations** (verified, not flagged, by review): `OnSessionEnd` releases
  the session lock after step 4 (the literal scope self-deadlocks against `persistState`); two
  e2e rows assert via the store API instead of decoding raw `tool_use.jsonl` (SP-06's wire
  schema uses short keys); the fault-injection row skips as root on POSIX; `TestState_AtomicWrite`
  uses an in-package reader; the latent `SketchSet.Write`/`Save` race is documented, with no
  production `Write` callers today.
- **The Phase 1 canonicalization measurement (this commit).** Re-serving committed corpus
  captures byte-identical made the canon gap structurally unmeasurable (measured 0.969: exact
  chunk dedup wins on both sides and canon pays only delta/bookkeeping overhead). Ruled: the
  harness applies a deterministic, seeded volatile-substring refresh — only substring classes
  the configured strip set covers (clocks, pid/goroutine ids, run durations, hex addresses) —
  to the bash and testrunner groups per serve, per §8.1's own premise that those outputs are
  unique in reality; fileread/grep/glob/webfetch bytes stay byte-identical per path (an
  unchanged file re-read IS identical). No committed corpus file edited, `internal/eval` and
  `internal/observer` untouched, thresholds unmoved. V3-VERIFY re-checks on real sessions.

## Tombstone grammar (G3.2, §8.1 item 2)

```
[cleared: sha256:<12 hex>… · <size> · <tool>[ <subject>] · [ephemeral · ][superseded · ]re-expandable]
```

- Separator: U+00B7 MIDDLE DOT with one space each side; opener `[cleared: sha256:`; closer
  `re-expandable]`.
- `<12 hex>` is the first 12 hex digits of the full sha256, followed by U+2026 ellipsis marking
  it an elision of the full digest — the hash is the address, and re-expansion resolves it in
  the content-addressed store.
- `<size>` uses binary units (1024; `bytesPerKB` is the package's one nomagic annotation):
  `973B`, `2.4KB`, `3.3MB`, `3.0GB`, `0B` for empty.
- `<subject>` is the tool's subject line (path, command, pattern, URL); truncated at a rune
  boundary with U+2026 when over the cap; absent for a subjectless call (`Bash` alone).
- `ephemeral` marks Qompack's own retrieval results (§8.7); `superseded` marks a result a later
  tool use replaced (§8.1 item 3); both may appear together, in that order.
- 13 rendered markers are frozen in `testdata/golden/observer/tombstones.txt`; the tests in
  `internal/observer/tombstone_test.go` pin renderer and grammar (the golden lives outside
  `observertest`, which may not import `store`).

## Supersession rules (§8.1 item 3)

- Scope: only path-carrying, non-ephemeral tool uses of a supersedable class (file-content
  reads/writes; retrieval and pathless calls never participate). Seven filters, including: skip
  priors with zero Root; skip ephemeral in both directions.
- Trigger: the new read's chunk set is a superset of a prior read of the same path, or a
  near-duplicate (subset AND Jaccard >= threshold via the store's MinHash signature). The
  near-dup arm superseding a strictly smaller later read is intended.
- Effect: the EARLIER read is marked — `store.MarkSuperseded(older, by)` older-first, status
  `StatusSuperseded` on the record (survives reopen) — and returned most-recent-first;
  `graph.go` hands `marked[0]` to `dag.BuildToolUse` as `ObservedTool.Supersedes` and emits the
  tail itself, so the edge direction is decided by SP-07's builder, not this package.
- "Never appears in a summary" is enforced structurally: the fact lives on the record status and
  the `EdgeSupersedes` edge, both of which SP-10/SP-15 read; `internal/checkpoint` does not
  import `internal/observer`.
- Best-effort by design: SP05-D1 means a read the observer never saw cannot supersede anything.
- Counters: `observer.superseded` per MarkSuperseded; `observer.neardup` at the Put.

## Measured numbers

### Dedup ratios — the §10 Phase 1 exit criterion

Harness: `test/e2e/phase1_exit_test.go` — call sequence from `eval.Synthesize`, bytes from
SP-04's committed `testdata/corpora/toolout/` corpus, driven through `observer.OnUserPrompt` /
`OnToolUse` against a real `store.Open` on a temp root, FakeClock advancing 1 s per event.
Every ratio is `store.Stats().DedupRatio` = `RawBytes / Bytes` (§5.8). Measured on this branch
(Windows, go1.26; identical to the isolated prep-worktree run at Commit 5 — the numbers are
deterministic and reproduced byte-identically across runs):

| Run | Canonicalization | RawBytes | Stored | Ratio | Gate |
|---|---|---:|---:|---:|---|
| read-heavy (seed 0x51080001) | on | 14,003,773 | 73,959 | **189.35** | >= 4.0 -> **PASS** (exit criterion) |
| read-heavy | off | 14,003,773 | 77,236 | 181.31 | (reference) |
| test-output-heavy (seed 0x51080002) | on | 7,815,160 | 71,870 | **108.74** | — |
| test-output-heavy | off | 7,815,160 | 288,059 | 27.13 | on >= off x 1.25 -> gap **4.008x**, **PASS** |

Supporting probes (read-heavy, canon on): Files=106, superseded (MarkSuperseded calls)=133, HLL
cardinality=101, objects=128, 336 driven tool calls, 41,678 raw bytes/call (floor 1,024);
store growth sublinear: first-half stored 72,455 bytes vs second-half 1,504 (§11.3 guardrail).
The corpus sweep replayed all 24 committed synthetic sessions without a panic, ratios
47.3-118.0. The canonicalization gap is measured under the ruled volatile refresh above; canon
on vs off on byte-identical replays is ~1x by construction (189.35 vs 181.31 read-heavy).

### Hook latency — B-A and B-B (§8.1 performance budget, §11.3)

`go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon
--json bench-observer.json`, local Windows (Intel Core Ultra 7 155H), observer wired:

- **B-A** (hook client spawn -> ack, gate p99 < 15 ms): p50 2.048 ms, p99 **2.048 ms**
  (p999 3.072 ms, max 14.0 ms, n=2064) — **PASS**.
- **B-B** (in-daemon hot path, gate p99 < 2 ms): p99 **0.704 ms** (p50 0.002 ms, p999
  5.120 ms, n=2064) — **PASS**.
- Diagnostics from the same run: spawn floor p50 11.594 ms / p99 17.964 ms (n=200); B-D
  (end-to-end spawn wall clock, informational) p50 12.647 ms / p99 24.667 ms; the gated B-A row
  is the daemon-observed hook-controlled statistic plus the tail allowance, per controller
  ruling #29 on SP-05's harness.
- CI platform figures — **DISCHARGED 2026-09-06** by run
  [32932419445](https://github.com/AidanHT/qompack/actions/runs/32932419445), `bench-gate` green on all
  three runners (`bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon`, n=2064 for
  B-A/B-B, n=50 for B-E):

  | budget | gate | ubuntu-latest (linux/amd64) | macos-latest (darwin/arm64) | windows-latest (windows/amd64) |
  |---|---|---:|---:|---:|
  | **B-A** hook spawn -> ack | p99 < 15 ms | **2.048 ms** | **3.072 ms** | **3.072 ms** |
  | **B-B** in-daemon hot path | p99 < 2 ms | **0.060 ms** | **0.320 ms** | **0.576 ms** |
  | **B-E** PreCompact finalize | p99 < 2000 ms | **9.295 ms** | **73.498 ms** | **188.110 ms** |

  Every gated row reports `pass: true` on every platform. Diagnostics from the same runs, for the
  reader who needs the spawn cost these figures deliberately exclude: spawn floor p50/p99 is
  4.826/6.520 ms on ubuntu, 8.676/13.760 ms on macos and 12.954/18.490 ms on windows, and the
  informational B-D end-to-end wall clock is p99 5.956 / 24.342 / 19.761 ms respectively. The
  three-runner spread is a spread of process-spawn cost, not of hot-path work: B-B, the figure
  that contains no spawn at all, stays under a third of a millisecond everywhere except Windows,
  where it is still under a fifth of its gate.
- By controller ruling (wave-1 precedent): the local Windows B-A/B-B figures above are recorded,
  and a green `bench-gate` on ubuntu-latest, macos-latest and windows-latest on the branch's
  first push was a NAMED condition of Phase 1 closure. V3-VERIFY owned the discharge and it is
  now complete — the condition is satisfied, not waived. The delay was external: GitHub Actions
  refused every job on the branch's first push for an account-billing failure, so the run above
  is that push's rerun once billing was restored (see `plans/V3-report.md`, J5).

### L0 processing — B-C, and SP08-D1

Micro-benchmarks (Task 2/Task 3, quiet machine, `-benchtime 200x -count=5`, real store + DAG,
p50/p99 from the `observer.tooluse` histogram — the instrument B-C is evaluated against):

| benchmark | fixture | p50 (ms) | p99 (ms) | B-C p99 < 50 ms |
|---|---|---:|---:|---|
| `BenchmarkOnToolUse_FileRead64KB` | Deduped | 12.3-14.3 | 14.3-18.4 | inside |
| `BenchmarkOnToolUse_FileRead64KB` | Delta | 15.4-16.4 | 24.6-45.1 | inside |
| `BenchmarkOnToolUse_FileRead64KB` | AllNovel | 24.6 | 36.9-45.1 | inside |
| `BenchmarkOnToolUse_TestOutput256KB` | Deduped | 41.0-61.4 | 53.3-131.1 | **breach** |
| `BenchmarkOnToolUse_TestOutput256KB` | Delta | 41.0-49.2 | 53.3-114.7 | **breach** |
| `BenchmarkOnToolUse_TestOutput256KB` | AllNovel | 147.5-213.0 | 262.1-655.4 | **breach** |

The 256 KB breach is **SP08-D1** in `plans/V2-SP-08-carried-defects.md` (row in
`plans/CARRIED-DEFECTS.tsv`): the cost is inside `store.PutBytes`, bounded, and invisible to the
user — the hook path acknowledges from the WAL before L0 processing runs, so a B-C overrun
delays index freshness and blocks nothing. B-C is soft (`Gated: false`); V3-VERIFY owns the
fix-or-defer decision.

`BenchmarkTombstone`: 434.1 ns/op, 272 B/op, **6 allocs/op** against the < 2 us/op budget
(4.6x margin; the alloc clause was struck by ruling — `core.Hash.Short` contributes 2 of the 6,
`humanBytes` 3, the builder 1).

### Coverage

`go test -cover ./internal/observer/`: **96.2% of statements** (floor 75%; the floor becomes
machine-enforced when the merge commit adds SP-08 to `tools/devtool/cover.go`'s
`landedSubplans`).

### Gates run locally for this commit

`ci-local` (fmt-check -> lint -> vet -> build -> test -> cover -> plugin-validate ->
gen-config-docs --check): green, exit 0, with the harness in the tree. `go test -race
-count=1 -timeout=30m ./internal/observer/...`: ok, exit 0. `replay`: exit 0. `build-all`:
exit 0. `bench-hotpath` as above: exit 0, every gated budget PASS.
`bench-gate`/`replay-gate`/`security`/`docs`/`crossbuild` are CI jobs and run on push; a Linux
run was not possible locally (no WSL distro with a Go toolchain on this machine) and is
pending CI.
