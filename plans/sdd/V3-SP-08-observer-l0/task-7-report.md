# Task 7 report — Commit 7: Phase 1 exit harness, gate runs, ADR 0008

Date: 2026-08-25. Worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp08`,
branch `feat/sp08-observer-l0`, base head f1c7753.

## State found on arrival, and the verification stance taken

A previous interrupted integration run had already (a) copied `test/e2e/phase1_exit_test.go`
from the detached prep worktree, (b) removed `qompack-sp08-t7` (`git worktree list` no longer
shows it; the directory is gone; the prep artifacts survive as
`.superpowers/sdd/V3-SP-08-observer-l0/task-7-prep-report.md` and `task-7-phase1-ratios.json`),
(c) written a full draft of `docs/adr/0008-observer-l0.md`, and (d) left a
`bench-observer.json` from a bench run — all uncommitted, with no task-7 report. Nothing from
that run was trusted: every gate was re-run on this branch, every ADR figure was re-verified
against its primary source, and the bench was re-measured; two ADR corrections resulted (below).

## Step 2 — Phase 1 harness on the FULL branch (head f1c7753)

`go test ./test/e2e/ -run Phase1 -count=1 -timeout=30m -v` → **exit 0, 8/8 PASS in 95.2 s**
(the prep run predates `session.go`; this run is the one that counts). Assertions stand at the
design numbers: `phase1ExitRatio = 4.0`, `canonGapFloor = 1.25` (verified in the file; never
weakened).

| Gate | Measured | Result |
|---|---|---|
| `TestPhase1_DedupRatioReadHeavy` (§10 exit criterion) | raw=14,003,773 stored=73,959 objects=128 **ratio=189.3451** ≥ 4.0 | PASS |
| `TestPhase1_CanonicalizationGapOnTestOutput` | on: 108.7402 (71,870 B) vs off: 27.1304 (288,059 B) → **gap 4.0081×** ≥ 1.25× | PASS |
| `TestPhase1_PathsReachTheObserver` | Files/AppendFileVersion/MarkSuperseded/HLL all non-zero | PASS |
| `TestPhase1_ResponseBytesAreReal` | 41,678 raw B/call ≥ 1,024 | PASS |
| `TestPhase1_ReportArtifact` | eight keys present | PASS |
| `TestPhase1_StoreGrowthSublinear` | first-half 72,455 B vs second-half 1,504 B | PASS |
| `TestPhase1_CorpusSweep` | 24 sessions replayed, ratios 47.26–118.01, no panic | PASS |
| `TestPhase1_HotPathBudgetDocumented` | ADR exists with a B-A p99 figure (green now that the ADR is in the tree) | PASS |

Ratios are byte-identical to the prep-worktree run at 7885650 — the harness is deterministic in
(seed, spec) as the prep report claimed.

## Step 3 — gates (each its own invocation, exit codes checked, machine quiet)

- `go run ./tools/devtool ci-local` → **exit 0, green end to end** (fmt-check, lint incl. the
  now-cleared plan-gate rows, vet, build, test tree-wide, cover — observer floor row reads
  `OK observer: 96.2% >= floor 75%` — plugin-validate, gen-config-docs --check).
- `go test -race -count=1 -timeout=30m ./internal/observer/...` → **exit 0** (observer 8.9 s,
  observertest 3.1 s), run again post-merge of the harness as ordered.
- `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon
  --json bench-observer.json` → **exit 0**; flag spellings verified against
  `test/bench/hotpath/main.go` (`--iterations/--hook/--warm-daemon/--json`, exactly as given).
  **B-A p50 2.048 ms / p99 2.048 ms** (p999 3.072, max 14.0, n=2064, limit 15 → PASS);
  **B-B p99 0.704 ms** (p50 0.002, p999 5.120, limit 2 → PASS); spawn floor p50 11.594 / p99
  17.964; B-D p50 12.647 / p99 24.667 (informational). This re-run replaced the interrupted
  run's json; the ADR carries THESE figures.
- `go run ./tools/devtool replay` → **exit 0**.
- `go run ./tools/devtool build-all` → **exit 0**.
- `go test -cover ./internal/observer/` → **96.2% of statements** (ADR figure; ≥ 75%).
- Linux check: `wsl -e bash -lc "go version"` fails — the only WSL distro on this machine is
  docker-desktop (stopped, no bash). Recorded **pending-CI**, in the ADR too.

## Step 4 — ADR 0008

`docs/adr/0008-observer-l0.md` (drafted by the interrupted run) was verified claim-by-claim
against primary sources: twelve resolved decisions and amendment (a)–(d) against the plan and
progress.md; every "Ruling:" line in progress.md is represented (process rulings summarized as
such, shape rulings itemized: WireObserver opens the seams, landedSubplans at merge,
features.go/prompt.go moves, import-set pin, reTestFail case rules, BenchmarkTombstone alloc
clause struck, suite lift at Commit 2, neardup at the Put, 7th zero-Root filter + near-dup-arm
intent, the parked ArgsPreview redaction question, the six sanctioned Task 6 deviations, and
the T7 volatile-refresh ruling); tombstone grammar against `tombstone.go` and the 13-line
golden; supersession rules against the shipped `supersede.go` rulings; dedup table against
this run's log; B-C table against `plans/V2-SP-08-carried-defects.md` (SP08-D1 pointer present,
figures match row-for-row); BenchmarkTombstone 434.1 ns/op / 272 B/op / 6 allocs against
task-1-report.md; coverage against the cover gate. Two corrections made: the "five tests in
tombstone_test.go" claim (the file now has 18 test funcs — reworded to "the tests in"), and
the B-A/B-B/spawn/B-D figures updated to this session's verified bench run.

## Step 5 — done-checklist greps

- Placeholder scan (`git grep -nE 'TODO|TBD|FIXME|XXX|not implemented|handle (this|edge cases)'
  -- internal/observer internal/daemon/observer_ops.go test/e2e`): **one hit**,
  `test/e2e/faultinject_test.go:440` — the word "TODO" inside a prose comment ("would just be
  testing a different subplan's TODO"), Task 6's file, reviewed and landed there. Not a
  placeholder; not edited by this task (not mine to touch). Flagged for V3-VERIFY to reword if
  the literal-grep gate must read empty. No `core.ErrNotImplemented` in `internal/observer`
  non-test code (observertest's stub factory and suite probe are the only mentions, by design).
- NodeID/argsPreviewMax grep (the brief's one-line pattern, with `:!*_test.go`): **empty**.
- Trailer grep `git log develop..HEAD --format=%B | grep -Ei
  'co-authored-by|signed-off-by|generated with'` (local develop ref): **empty** — checked
  before and after this commit.
- `git diff develop..HEAD -- Qompack.md`: **empty**.
- `git diff develop..HEAD -- internal/cli/daemon.go`: the two wiring blocks
  (`daemon.WireObserver(&opts)` before `daemon.New`, `daemon.RegisterObserverIdleWork(d, obsv)`
  after) **plus the two Task-6-ruling-sanctioned additions** (deferred `opts.Store.Close()`
  after Run; startup `os.Remove` of the store's empty quarantine scaffolding) and the
  `path/filepath` import they need — exactly the T6 ruling (2) state, nothing else.

## Steps 6–7 — commit and final state

One commit, subject exactly `test(observer): Phase 1 exit-criterion harness and hot-path
benchmarks` (54 chars after the prefix), footer exactly
`Refs: SP-08, §10 Phase 1, §11.3, §8.1 performance budget`, body carrying the headline ratios
and B-A figures; validated with `devtool check-commit-msg` and `git log -1 --format=%B`.
Staged: `test/e2e/phase1_exit_test.go`, `docs/adr/0008-observer-l0.md`.
**`bench-observer.json` is left untracked**: no bench json is tracked anywhere in the repo
(`git ls-files` grep empty; `.gitignore` ignores `/testdata/bench-*.json`), so the convention
is that bench artifacts stay out of the tree.

`git rev-list --count develop..HEAD` after the commit: **8**, not the dispatch note's expected
9 — the branch holds 6 feat + 1 chore(gates) + this test commit; the plan's "commit 0"
(`fix(store): honour an empty Canon.Strip...`) is on the amendment branch, already merged to
develop, and was never one of this branch's commits. The Done checklist's own "exactly 7"
plus the ruled 8th gates commit = 8, consistent with the SDD ledger.

`go run ./tools/devtool lint`: green. Tree clean except the deliberately untracked
`bench-observer.json`.

## For V3-VERIFY

- CI platform bench figures (ubuntu/macos/windows) pending the first push; `bench-gate` is the
  enforcing job. Linux test run pending CI (no usable WSL distro).
- `faultinject_test.go:440` prose "TODO" is the placeholder scan's one residue.
- The canonicalization gap is measured under the T7 volatile-refresh ruling; re-check on real
  sessions.

---

# Post-review fix wave (controller rulings R1-R5)

One sanctioned 9th commit on top of cd02d5e: **ef6a42c3178b48279fda1e3dbffa98ca0d7f337d**,
subject exactly `fix(observer): bridge prompt consumes edges and load state before persist`,
footer `Refs: SP-08, §4.4, decision 4, SP05-D1`. No existing commit re-minted; 8 files, +100/-13.
`git rev-list --count develop..HEAD` = 9, per ruling R5. `check-commit-msg` OK; trailer grep empty.

## What landed, per ruling

- **R1 (F1, dangling prompt edge)**: `prompt.go` recordPrompt step 5b hand-emits
  `userprompt:T --consumes--> assistant:T+1` (Weight 1 via `edgeWeight`, Turn T) immediately after
  `dag.BuildUserPrompt`, with a call-site comment naming the decision-4 × BuildUserPrompt
  contradiction and the V3-VERIFY fold-in (amended `BuildUserPrompt(AssistantNode(Turn+1))` with
  SP-07). The builder's same-turn edge stays and dangles inertly (D-6). `internal/dag` untouched;
  decision 4 untouched. `TestOnUserPrompt_DAGNodeAndSegmentEdge` now asserts BOTH edges;
  new `TestOnUserPrompt_BackwardSliceReachesThePrompt` drives prompt-then-tool-use against a REAL
  `dag.Open` graph and asserts `userprompt:0` is in the BackwardSlice from the tool-use node,
  thin AND full.
- **R2 (F2, state wipe)**: `state.go` persistState now opens with `o.once.Do(o.loadState)` (same
  sync.Once as session()), with a comment on the idle-Persist-before-first-event window. New
  regression `TestPersist_BeforeAnyEntryPointDoesNotWipeTheFile`: persist real state, build a
  fresh observer over the same root, Persist BEFORE any entry point, assert the prior session
  (Turn 17, PrefixTokens 4242) survives in the file.
- **R3 (F3, same-session ordering)**: no behaviour change. `doc.go` decision 9 strengthened: the
  locking serializes one session's STATE not its ORDER; arrival order over the daemon transport is
  best-effort (shared worker ring without session affinity; synchronous prompt path bypasses the
  queue); Turn/PrefixTokens are monotone counters of events as OBSERVED (SP05-D1); transport-level
  session affinity / sequence numbers are V3-VERIFY's, beside SP05-D1.
- **R4 (bench-gate)**: no code change. ADR "Hook latency" section gains one bullet naming the
  ubuntu/macos/windows `bench-gate` green at first push as a NAMED condition of Phase 1 closure,
  discharged post-push with the three p99 figures folded in, owned by V3-VERIFY.
- **R5 (mechanical minors)**: (a) `faultinject_test.go:440` prose reworded ("another subplan's
  unfinished surface") — the plan's placeholder grep over
  `internal/observer internal/daemon/observer_ops.go test/e2e` now returns empty (verified,
  exit 1). (b) Plan checklist: commit-count bullet records the ruled 8-plus-1; the Bloom bullet
  points at the AST-level `TestObserverSourceHasNoBloomReference` (doc comments legitimately name
  Bloom, a bare grep is not the check); the negknow bullet points at the import-graph lint and the
  realized-import test with the same comments distinction; "eight resolved decisions" corrected to
  twelve in both bullets (ADR's own section header already says twelve).

## Verification

- `devtool fmt`: clean. `go test ./internal/observer/ -count=1`: ok (8.4s).
- Focused `-v` run: all three covering tests RAN and PASS (`_DAGNodeAndSegmentEdge`,
  `_BackwardSliceReachesThePrompt`, `TestPersist_BeforeAnyEntryPointDoesNotWipeTheFile`).
- `go test -race -count=1 -timeout=30m ./internal/observer/...`: ok, exit 0.
- `go run ./tools/devtool ci-local`: **exit 0** (fmt-check → lint → vet → build → test → cover →
  plugin-validate → gen-config-docs --check all green), run once, alone, after the edits.
- Placeholder grep: empty. Trailer grep on the new commit: empty. `git log -1 --format=%B`
  verified; only `bench-observer.json` remains untracked (convention).

## Residuals for V3-VERIFY

- Fold the R1 bridge into an amended `BuildUserPrompt(AssistantNode(Turn+1))` with SP-07, then
  drop the observer's hand-emitted edge and the builder's dangling same-turn edge.
- Transport-level same-session ordering (session affinity or sequence numbers), beside SP05-D1.
- Discharge the named bench-gate condition on first push; fold the three CI p99 figures into the
  ADR.
