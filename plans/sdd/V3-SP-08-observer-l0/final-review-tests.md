# SP-08 final whole-branch review — lens: test honesty & integration soundness

Range: 1466b73..cd02d5e (8 commits). Reviewer: test-honesty lens of the three-lens final panel.
All reads were against the pinned SHA via `git show`; no working-tree file was read, nothing mutated.

## Verdict

**Approved — no critical or important findings.** Five minor findings below.

## What was verified (evidence per claim)

**Real stores/graphs where mandated.**
- `supersede_test.go:336 TestSupersede_StatusSurvivesReopen` re-opens a real `store.Open` over the same
  root (line 356) — the Commit 3 "reopen-persistence against the real SP-06 store" mandate is met.
- `stop_test.go:425 TestOnStop_RetrievalPathG10_1` round-trips the capture out of a real store and then
  independently expands *each* referenced tool-result root ("detail the parent never held, still
  reachable by hash") — the strongest possible reading of G10.1, not just a summary equality.
- `prompt_test.go:276 TestOnUserPrompt_NeverRegenerated` uses a real store, asserts object-count
  equality on resubmit, distinct append-only records, same content address.
- The conformance suite's real factory (`observertest/suite_test.go:63-105`) builds over real
  `store.Open` + `dag.Open`; the stub negative control (`TestObserverSuite_ShapePassesAgainstStub`)
  keeps the W-1 skip falsifiable. The behaviour block genuinely lifts.
- `observer_ops_test.go` asserts through a **built daemon** (`require.Same` on Store/Graph/Sketches,
  all five seams non-nil on `dd.svc`), exactly the shape that catches the value-receiver Bind trap.

**E2E through the real binary.** All six rows in `test/e2e/observer_e2e_test.go` run `Build(t)`'s
`./cmd/qompack` through session-start → observe → flush → shutdown. Restart rows
(`TestE2E_SupersessionVisibleAfterRestart`, `TestE2E_VerbatimPromptSurvivesRestart`) re-open a fresh
`store.Open` after the daemon is gone and assert store-level state (StatusSuperseded + SupersededBy;
byte-exact prompt including the volatile substrings a canonicalizer would strip).
`TestE2E_ThinSliceDropsControlOnlyEdges` asserts a strict thin<full slice over the persisted graph —
a real cross-package dag contract, not a smoke test.

**Goldens byte-pinned.** `TestTombstoneGolden` (`tombstone_test.go:286`) compares the full rendered
file with `require.Equal`; `-update` is an explicit opt-in flag; only the *golden file's* bytes are
CRLF-normalized (a CRLF-emitting renderer would still fail). The 13-record fixture covers every
renderer branch (ephemeral, superseded, both, 0B, GB, truncated `…` subject, MCP name).
`test/integration/hookflow_test.go`'s widened regex is a *strengthening*: the ellipsis after the
12-hex short hash is now REQUIRED, matching the golden, with the rationale documented in place.

**Import-set and W-3 discipline.**
- `TestObserverImportSetIsExact` (`sketches_test.go:161`) is a go/parser walk over non-test files
  pinning the realized set with `require.Equal` against the sorted 12-package list. I independently
  extracted the import blocks of every non-test file at the pinned SHA: exactly
  `canon config core dag grammar hookio logging obs paths sketch store tokens` — matches the plan's
  exit criterion; no negknow/chunk/contract/scheduler/checkpoint/symbols/daemon.
- `TestObserverSourceHasNoBloomReference` walks identifiers in the AST — stronger than the plan's
  grep (prose may name Bloom; identifiers may not).
- Whole-branch file map: the diff touches no `internal/store`, `internal/sketch`, `internal/dag`,
  `internal/daemon/{handlers,options,daemon}.go`, and no `internal/cli` file except `daemon.go`.
  `observer_ops.go` contains no `Handle(` call (verified by grep). `daemon.go` carries the two wiring
  blocks plus the two T6-ruling-sanctioned extras (quarantine `os.Remove`, deferred `Store.Close`) and
  nothing else — settled in the ledger, not re-litigated. The gates-commit edits (cover.go,
  cover_test.go, stubs_test.go, nightlyfuzz, planchecks, hookflow regex, V6-VERIFY -run patterns) all
  match their ledger rulings; the stubs guard was strengthened, not weakened
  (`TestObserverNewRequiresItsCollaborators` restates the new constructor contract per-collaborator).

**Phase 1 harness measurement honesty** (`test/e2e/phase1_exit_test.go`).
- Thresholds: `phase1ExitRatio = 4.0` and `canonGapFloor = 1.25`, both commented as unmovable
  design numbers; the assertions use them directly. Unweakened.
- Ratio definition: every asserted ratio is `store.Stats().DedupRatio`; the file's own header states
  "no other definition of the ratio anywhere in this file" and that holds on reading.
- Volatile refresh (the ruled canon-gate fix): scope verified — `refreshVolatile` fires ONLY for the
  `bash`/`testrunner` groups (`corpusPayloadFor`); fileread/grep/glob/webfetch bytes stay
  byte-identical per path. Classes verified against `config/defaults.go`: clocks/ISO time-of-day
  (timestamps), pid= and goroutine ids (pids), 0x/@ hex (addresses), duration spellings (durations) —
  a strict subset of the configured strip set `{timestamps, ansi, pids, addresses, tmpPaths,
  durations}`. Determinism verified: splitmix64 over (seed, seq); seed is FNV of the session ID, so
  the canon-on and canon-off runs of a spec ingest byte-identical streams (RawBytes agree —
  ADR table confirms: identical RawBytes per pair). Replacements are length- and shape-preserving.
  Errors here could only *depress* ratioOn, never inflate the gap.
- Anti-vacuity guards are present and real: `TestPhase1_PathsReachTheObserver` (Files>0, real
  FileHistory hit under paths.Norm+Key, superseded counter>0, HLL>0),
  `TestPhase1_ResponseBytesAreReal` (>1 KB raw/call), `TestPhase1_StoreGrowthSublinear`
  (strict second-half < first-half on Stats.Bytes).
- The sketch dimensions the harness wires match the package's own suite, so the HLL probe measures
  the observer's real feed.
- ADR 0008 records both raw numbers per run pair, the pre-ruling 0.969 measurement, the ruling
  itself, and the honest caveat that byte-identical replays sit at ~1x. Nothing is hidden.

**Benchmark honesty.** `benchFixtures()` keeps Deduped/Delta/AllNovel variants with the optimistic
end explicitly labeled "kept only for comparison"; `runOnToolUseBench` asserts nothing about latency
with the rationale documented, and the B-C breach is recorded in full pessimistic detail in the ADR
table and as SP08-D1 in CARRIED-DEFECTS.tsv (owner V3-VERIFY, status open) — a failed budget
recorded rather than a threshold tuned. That is the honest shape.

**Skips.** Only two `t.Skip` in the new tests: root-inert fault injection (T6 ruling 4) and the
informational corpus sweep when the corpus directory is absent. No skip guards a mandated assertion.

## Findings (all minor)

1. **minor / evidence-completeness** — `test/e2e/phase1_exit_test.go` + `docs/adr/0008-observer-l0.md`:
   the three-platform `bench-gate` exit-criterion item is undischarged on the branch — the ADR marks CI
   figures "pending", and `TestPhase1_HotPathBudgetDocumented` deliberately accepts a pending ADR (it
   pins only that a local B-A p99 figure was written down). The ledger already records "Linux run + CI
   bench figures pending first push"; the enforcing gate is CI's bench-gate on push. Named here so the
   merge decision carries it consciously, not as a new discovery.
2. **minor / label honesty** — `internal/observer/fakes_test.go` (PutBytes): `fakeStore.PutBytes` mints
   roots via `core.HashBytes(core.DomainArgs, body)` — the ARGS hash domain for content bytes. No test
   crosses fake-minted hashes with real-store hashes, so behaviour is unaffected, but a
   content-domain constant (or a comment) would stop the fake teaching the wrong domain.
3. **minor / ephemeral evidence** — `TestPhase1_CanonicalizationGapOnTestOutput` writes
   `phase1-dedup.json` into `t.TempDir()`, which is deleted at test end; the artifact survives only
   in the test log and the ADR. Writing it under a retained path (or only logging) would match intent.
4. **minor / out-of-lens (placeholder scan)** — the Done checklist's placeholder grep over
   `test/e2e` trips on the pre-existing prose TODO at `test/e2e/faultinject_test.go:440` (in the base,
   not this branch's diff). Already flagged by the integrator for the panel; noting for completeness.
5. **minor / observation** — the read-heavy exit ratio (189.35) is dominated by byte-identical
   re-reads and clears 4.0 by two orders of magnitude; the canonicalization gap is the only assertion
   doing real discriminating work, and it rests on the ruled simulated refresh. The ruling explicitly
   assigns V3-VERIFY a re-check on real sessions — that follow-up is load-bearing and should stay
   visible.

## Strengths

The negative-control pattern recurs everywhere it matters (stub suite proving the skip still fires,
"delete the wiring and watch the e2e fail" ordering, anti-vacuity probes inside the Phase 1 harness,
GetRoot miss-as-error in the fake). Restart-and-reopen assertions go through fresh `store.Open` rather
than daemon memory. The AST-based import and Bloom guards are stronger than the plan's greps. The
B-C breach handling — pessimistic fixtures kept, budget failed and recorded as a carried defect
instead of tuned — is exactly what test honesty looks like under a missed budget.

## Out-of-diff checks performed (one per named risk)

- Import-set risk: extracted import blocks of every non-test `internal/observer` file at the pinned SHA.
- Volatile-refresh scope risk: read `internal/config/defaults.go` strip list at the pinned SHA.
- Handle-override risk: grepped `Handle(` in `internal/daemon/observer_ops.go` at the pinned SHA.
