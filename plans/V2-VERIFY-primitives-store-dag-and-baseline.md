# V2 — Verification checkpoint: primitives, store, DAG, daemon, and the Phase-0 baseline

> **Recommended model: Opus 5 · xhigh effort**
>
> Breadth re-verification plus first wiring of the bench-gate and replay-gate. Opus 5's bug-finding runs at high precision *and* high recall, and the cumulative surface is still only seven subplans — `xhigh` is enough.

**This is a standalone prompt. Execute it exactly as written. Do not skim.**

---

## 0. Map of this document

| § | What it is | Who runs it |
|---|---|---|
| **1** | When this checkpoint runs, what it is for, how to execute it | main session |
| **2** | Cumulative functionality inventory — every row that must pass | §2.0 main session, then **seven parallel subagents** V-A…V-G |
| **3** | Exit-criteria re-verification (the §10 phase gates) | main session |
| **4** | New cross-component integration tests — seven permanent files | **seven parallel subagents**, §4.8 serial |
| **5** | Performance budget validation | main session, one quiet machine |
| **6** | Regression: prior checkpoints re-run, and the 2 % guardrail | main session |
| **7** | Failure protocol — diagnose, fix, re-run, merge | §7.2a fans fixes out across **eight parallel agents** |
| **8** | Completion report template | main session |

**Read these six before dispatching anything.** Five were written by the sessions that shipped wave 1; §2.0b was written by the merge that integrated them. All of them contradict the row tables in places where the rows are wrong:

| § | Written by | Why it changes what you do |
|---|---|---|
| **2.0** + **2.0a** + **2.0b** | this checkpoint | twenty-four `V2-MERGE-*` rows and the ten-file collision map. Most of these gates fail **silently**. **§2.0b is the one to read first**: six defects the wave-1 merge already found and fixed, four of which git reported as clean merges |
| **2.3a** | SP-03 | the MinHash sampler was rewritten; the coverage gate is dead; `benchstat` is never invoked |
| **2.2a** | SP-02 | two §2.2 rows **exit 0 having run nothing**; one deletes working code if followed |
| **2.5a** | SP-05 | its 30 rulings are **git-ignored**; POSIX code has never executed anywhere |
| **2.6a** | SP-06 | ten expected results are wrong; ⑧ must run before anything else |
| **2.7a** | SP-07 | seven §2.7 rows plus V2-ALL-04 do not reconcile; three carry-forward ACTIONs |

Three defect classes recur across independently written sections, so assume every group carries them: a **`-run` pattern matching no test still exits 0**; a **`grep … t.Skip` row can never be satisfied** and deleting the skip violates Rule W-1; and a **coverage row measured nothing** until V2-MERGE-14 landed — it has now, one entry per merge, so every coverage row in this document is live. Confirm that (`devtool cover` prints no `exempt (stub, owned by SP-0[2-7])`) rather than assuming it.

---

## 1. When this runs

This checkpoint runs **after every wave-1 subplan branch has merged into `develop`, and before any wave-2 branch is cut.**

Wave 1 is the six subplans SP-02 … SP-07 (`00-ARCHITECTURE.md` §14). Their branches merged into `develop` with `--no-ff`, in this order:

1. `feat/sp05-daemon-ipc-and-hot-path`
2. `feat/sp03-sketch-library`
3. `feat/sp04-chunking-canonicalization-and-symbols`
4. `feat/sp02-replay-harness-belady-baseline`
5. `feat/sp06-content-addressed-store`
6. `feat/sp07-dependence-dag-and-slicing`

**That is not the order this document originally stated, and not the order `plans/README.md` originally stated either. Both were wrong; see V2-MERGE-21.** The order is fixed by two constraints that are enforced as tests, not by any document: SP-06 must land after SP-03 and SP-04 (its own exit criterion), and SP-02 must land before SP-06 (`TestGuard_Phase0BeforeStore` — `internal/store` real while `internal/eval` is still a stub is a hard failure). If you find yourself "correcting" the list above back to numeric order, read V2-MERGE-21 first: the numeric order is *demonstrably* red, and the row tells you how to reproduce it.

Before you begin, confirm the wave actually landed. Two facts about this repository shape every git command below. **It has no remote** — every command uses local refs, and `main` is the initial commit, so `main..develop` is the whole project history. **It uses linked worktrees** — `git worktree list` shows the primary one holding `verify/v2`, a sibling `../qompack-develop` holding `develop`, and one `../qompack-sp0N` per wave-1 subplan branch that has not been reclaimed. A branch checked out in one worktree cannot be checked out in another, which is why nothing here checks out `develop`: reading its history needs no checkout, and `git checkout develop` would fail with *"already checked out at .../qompack-develop"*. The subplan worktrees are finished work kept for reference; every wave-1 branch is also reachable as a branch and as a `wave1/sp0N-shipped` tag, so removing a worktree loses nothing.

```bash
git worktree list                   # orient yourself: which worktree are you in?
git log --first-parent --oneline main..develop | grep -E 'feat\(sp0[2-7]\)'
# Expect exactly these six, newest first — the relative order is the assertion:
#   1b66239 feat(sp07)   018fd5a feat(sp06)   25f0335 feat(sp02)
#   996f65c feat(sp04)   7f7ca59 feat(sp03)   adff200 feat(sp05)
# Docs commits sit above and between them; they are not part of the check.
git log --format=%B main..develop | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'
# Expect: no output. (CI's verify job runs this; a match fails the build.)
```

**Work happens on `verify/v2`, cut from `develop`.** It has already been cut at the tip of the wave, and it is already checked out in the primary worktree (`.../Projects/qompack`), which is where a fresh session starts. Confirm that rather than creating anything:

```bash
git rev-parse --abbrev-ref HEAD             # expect: verify/v2
git merge-base --is-ancestor develop HEAD && echo "verify/v2 contains all of develop"
# If HEAD is not verify/v2: git checkout verify/v2
# If the branch does not exist at all: git checkout -b verify/v2 develop
go run ./tools/devtool ci-local     # baseline; record the result before changing anything
```

Every fix this checkpoint produces is committed to `verify/v2`. When the whole checkpoint is green, `verify/v2` merges back into `develop` with `--no-ff` and `develop` is tagged `v0.1.0` (`00-ARCHITECTURE.md` §9). **No wave-2 branch (`feat/sp08-observer-l0`, `feat/sp09-negative-knowledge`) may be cut until that merge has happened.**

### 1.1 What this checkpoint is

It is **not** a smoke test. It is an exhaustive re-verification of *every functionality that exists in the codebase at this point*. Wave 1 built six packages in parallel against SP-01's `ErrNotImplemented` stubs and against `testdata/golden/contracts/**` fixtures (Rule W-2). This is the first moment those six real implementations have ever been compiled, run, benchmarked, and measured **together**. Three classes of defect can only surface here:

- **W-2 fixture divergence.** SP-06 tested `store.Put` against SP-01's synthetic `chunk`/`canon`/`symbols`/`sketch` fixtures. SP-04's real canonicalizers and SP-03's real MinHash may not reproduce those bytes. `00-ARCHITECTURE.md` §5.22 Rule W-2 is explicit: *"Any fixture that the real implementation cannot reproduce is a verification failure, not a fixture bug."*
- **Budget composition.** SP-05 measured B-A p99 < 15 ms against a daemon holding *stub* sketches, a *stub* DAG and a *stub* store. The real ones are ~68 KB of sketches plus a 5 000-node graph plus a 2 000-root index. §8.1's budget is only proven when the daemon is warm with the real thing.
- **Exit-criterion composition.** The §10 Phase 1 exit criterion (`≥ 4:1` dedup) needs SP-04's canonicalizers *and* SP-06's store. Neither branch could prove it alone.

### 1.2 Do not test forward

Nothing from SP-08, SP-09, SP-10, SP-11, SP-12, SP-13, SP-14, SP-15, SP-16, SP-17 or SP-18 is in scope. `internal/observer`, `internal/negknow`, `internal/checkpoint`, `internal/pins`, `internal/rehydrate`, `internal/rules`, `internal/skills`, `internal/scheduler`, `internal/mcp`, `internal/commands`, `internal/analyzer`, `internal/grammar` are still `core.ErrNotImplemented` stubs. Their conformance suites must still report skipped behaviour blocks with the exact Rule W-1 message. Asserting anything else about them is a checkpoint failure, not a bonus.

Three specific consequences, so nobody "helpfully" over-tests:

- Budget **B-F** (`mcp_tool_call` p95 < 250 ms) has no producer until SP-13. It is measured as **N/A**, not as a pass.
- Budget **B-E** (`checkpoint_finalize` p99 < 2 s) *is* in force — `qompack checkpoint` exists as a thin client (SP-05) — but it currently exercises the client + daemon + nil-`Services.PreCompact` path only. Record it honestly with that annotation.
- `contract.Monitor` must report `ModeFull` with exactly **four** assertions at `OK:true, SevInfo, Observed:"not-yet-implemented"` (`00-ARCHITECTURE.md` §12.1). Five is a regression; three means someone wired a wave-3 producer early.

### 1.3 How to execute this checkpoint

Fan the §2 inventory out across **seven parallel subagents, one per subplan group**, then run §4 in the main session.

| Subagent | Owns | Returns |
|---|---|---|
| **V-A** | §2.1 SP-01 foundation inventory (V2-SP01-*) | pass/fail per row + the exact command output for every failure |
| **V-B** | §2.2 SP-02 replay/Belady/baseline (V2-SP02-*) | pass/fail per row, the `fraction_of_opt` numbers for `stock`/`null`/`oracle`, the reproducibility diff |
| **V-C** | §2.3 SP-03 sketches (V2-SP03-*) | pass/fail per row, the five Appendix-A sizing numbers, the `L0SketchUpdate` ns/op and alloc count |
| **V-D** | §2.4 SP-04 chunk/canon/symbols (V2-SP04-*) | pass/fail per row, the boundary-stability histogram, the dedup-report `gain` per group |
| **V-E** | §2.5 SP-05 daemon/IPC/contract (V2-SP05-*) | pass/fail per row, the 66-row fault table, the bench-hotpath JSON |
| **V-F** | §2.6 SP-06 store/redact/tokens (V2-SP06-*) | pass/fail per row, `Stats.DedupRatio` on the read-heavy fixture, the GC report |
| **V-G** | §2.7 SP-07 DAG/slicing (V2-SP07-*) | pass/fail per row, the slice latency medians, the thin-vs-full table |

Rules for the fan-out, which are the same rules the subplans used:

- Every subagent runs **read-only verification commands**. A subagent that finds a failure **reports the failing command and its full output**; it does not edit source, does not weaken an assertion, does not add `//nolint`, `t.Skip`, or a `nomagic:allow`.
- All fixes, all `git` operations and all commits happen in the **main session** (§7).
- **§2.0 runs first, in the main session, before a single subagent is dispatched.** It is the only section that inspects the merge itself rather than the code, and several of its rows detect gates that have been silently switched off. Dispatching the fan-out first would have V-A…V-G report green against a tree whose guards no longer guard, and three of its rows (V2-MERGE-03, V2-MERGE-06, V2-MERGE-10) read state that §5 and §7 overwrite — once those have run, those questions can no longer be answered at all.
- §4 (new integration tests), §5 (performance budgets) and §6 (regression) are **main-session only**. §4 authors permanent test files; §5 must be measured on one quiet machine so the numbers are comparable; §6 needs the whole picture.
- Subagents run against a clean `verify/v2` working tree. Give each one this file, `00-ARCHITECTURE.md` §3.2/§5/§6/§7, and its own subplan file.
- **Five of the seven groups carry an inbound section written by the session that shipped them, and a subagent that skips it will report defects that are not defects and pass rows that test nothing.** They are not optional context: §2.3a (SP-03) for V-C, **§2.2a** (SP-02) for V-B, **§2.5a** (SP-05) for V-E, **§2.6a** (SP-06) for V-F, **§2.7a** (SP-07) for V-G, and §2.4's carried-defects banner + §4a of the report for V-D. Two long-form documents back them: `plans/V2-SP02-handoff.md` and `plans/V2-SP07-handoff.md`. Hand each subagent its own section explicitly rather than trusting it to find it.
- **V-E needs one thing the others do not: a copy of SP-05's rulings ledger.** It is the only subplan whose reconciliation is **not in git** — 30 numbered rulings under `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/`, ignored by a `.gitignore` inside its own tree. §2.5a summarizes it, but a verifier that needs a ruling's full reasoning has to reach the worktree `…/qompack-sp05`. Secure it before cutting `verify/v2` — **V2-MERGE-20**.
- **Two defect classes recur across independently written groups. Assume any group can carry them:** a `-run` pattern that matches no test still prints `ok` and exits 0 (found in §2.2 and §2.7 — check patterns with `go test -list`); and a `grep -rn "t.Skip" <pkg> returns nothing` row can never be satisfied and, followed literally, deletes Rule W-1's mandated machinery (found in §2.2, §2.6 and §2.7 — three of the five groups that were reviewed).

---

## 2. Cumulative functionality inventory

Every functionality that exists on `develop` at this point, grouped by the subplan that delivered it. Each row is a **checklist item with an exact command and an exact expected result**. Run every row. "Passes" means the command exits 0 *and* the stated observable holds.

Unless stated otherwise, every command runs from the repository root `C:/Users/Quant/Documents/Programming/Projects/qompack`.

### 2.0 Merge-integrity gates — main session, before the §2 fan-out

These rows check for defects the **merge** produces rather than defects any branch contains. Every one of them is invisible on every individual branch: all six wave-1 branches can be green, each satisfying its own Done checklist and its own out-of-scope allowlist, and still compose a `develop` that fails this table. That is the direct consequence of the parallel-wave model — `00-ARCHITECTURE.md` §9 merges six branches that never saw each other's trees.

Most of the rows below fail **silently**. They do not turn a CI job red; they turn a *gate* into a no-op. A dead gate reports nothing, so §2.1–§2.8, §5 and §6 then measure a tree whose guards have quietly stopped guarding and report green. **A silently disabled gate is a checkpoint failure of the same severity as a failing test** (`00-ARCHITECTURE.md` §13 invariant 10, "Degradation is loud" — a guard that stops guarding without saying so is the loudest possible violation of it).

Run this table on `verify/v2` immediately after cutting it, **before** dispatching V-A…V-G and **before** §5. Three rows (V2-MERGE-03, V2-MERGE-06, V2-MERGE-10) read state that §5 and §7 overwrite; once those sections have run, the evidence is gone and the row can no longer be answered.

Rows **V2-MERGE-14 … V2-MERGE-23** were added after the six branches were read side by side, and each names a state that already holds in the trees as they stand — see **Cross-branch collision inventory** below for the evidence. They are not hypotheses about what a merge might do. **Run V2-MERGE-20 before all of them**: it is the only row whose evidence lives outside git, so it is the only one the ordinary act of cutting a branch can destroy.

| ID | Gate | Command | Expected result |
|---|---|---|---|
| V2-MERGE-01 | **Nightly fuzz matrix reconciles with the tree, in both directions.** `nightly.yml` is edited by SP-05 only; no other wave-1 subplan is permitted to touch `.github/`, so every fuzz target SP-03, SP-04 and SP-06 shipped arrives unregistered by construction | **(a)** what the matrix claims: `grep -oE 'pkg: [^,]+, *fn: [A-Za-z0-9_]+' .github/workflows/nightly.yml \| sort` — **(b)** what the tree ships: `for p in $(ls -d internal/*/ \| sed 's\|internal/\|\|;s\|/\|\|'); do go test -run '^$' -list '^Fuzz' ./internal/$p 2>/dev/null \| grep '^Fuzz' \| sed "s\|^\|pkg: ./internal/$p, fn: \|"; done \| sort` — then diff the two outputs (both are normalised to the same `pkg: X, fn: Y` shape) | The two lists agree as sets of `(pkg, fn)` pairs, with exactly one class of exception: a matrix entry whose package `plans/OWNERS.tsv` still assigns to SP-08…SP-18 may be absent from the tree. **Zero orphans in both directions.** A matrix entry naming a landed package but no existing function, and a shipped `Fuzz*` with no matrix row, are both failures. See **Known defects** below — **three orphan matrix rows and fifteen unregistered targets** were verified against the branch tips at authoring time and must be fixed here, not rediscovered |
| V2-MERGE-02 | The matrix guard is green **and its arity assertion still matches the matrix** | `go test -v -run TestNightlyFuzzMatrix ./test/guards/` | Green, with one subtest per matrix entry. `test/guards/nightlyfuzz_test.go` asserts `require.Len(t, matches, 8)`; adding a row for `internal/symbols` (which has **no** matrix entry today despite SP-04 shipping `FuzzExtract`) or splitting `internal/sketch` into its five real targets changes that count, so the assertion must be updated in the same commit. A green run with a stale `8` means rows were renamed rather than added |
| V2-MERGE-03 | **`testdata/bench-baseline.txt` survived five concurrent appends.** SP-02, SP-03, SP-05, SP-06 and SP-07 each append to this one file; merges 2–5 conflict on it by construction, and a `--ours` resolution drops a package's rows without any test noticing | `git show develop:testdata/bench-baseline.txt \| grep -oE '^pkg: .*' \| sort -u` | One `pkg:` block for **every** package that owns benchmarks at V2: `cli`, `config`, `obs`, `paths` (SP-01) plus `eval`, `sketch`, `chunk`, `canon`, `symbols`, `ipc`, `daemon`, `store`, `redact`, `tokens`, `dag`. A missing block means that branch's appended rows were lost in a merge; `benchstat` then has nothing to compare for that package and §5.3's >10 % warn / >25 % fail gate is **silently a no-op for it**. **Run before §5** — §5's regeneration overwrites the evidence and makes the loss permanently undetectable |
| V2-MERGE-04 | **Both CI gate edits survived.** SP-02 (its §11 commit) and SP-05 (its bench commit) each edit `.github/workflows/ci.yml`, and `bench-gate` / `replay-gate` are adjacent blocks in that file | `git show develop:.github/workflows/ci.yml \| grep -nE '^  (bench-gate\|replay-gate):\|continue-on-error'` | Both jobs present; **neither carries `continue-on-error`**. This is the executable form of V2-ALL-04's prose. A merge that kept one branch's version of the region silently restores `continue-on-error: true` on the other gate, and a gate that cannot fail reports green forever |
| V2-MERGE-05 | **`tools/devtool/importrules.go` covers every package wave 1 landed.** No V2 subplan declares ownership of this file, yet every branch that adds a real package needs a rule in it, and §4.8 of this checkpoint adds another | `go run ./tools/devtool lint` (`importgraph` sub-check) ; `grep -nE 'sketch\|chunk\|canon\|symbols\|store\|redact\|tokens\|dag\|ipc\|daemon\|contract\|eval\|test/replay\|test/dedup\|test/bench/hotpath' tools/devtool/importrules.go` | `importgraph` green, and a rule exists for each of the twelve packages **plus all three new composition roots** — `test/replay` (SP-02), `test/dedup` (SP-04), `test/bench/hotpath` (SP-05). All three branches edit this file and SP-05 re-indents the whole map, so a resolution that loses one root leaves that harness failing `lint --only=importgraph` (§2.0a). Specifically confirm the `internal/sketch` rule still restricts it to `{core, paths, logging}` — that rule is what enforces SP-03's "constructors take scalars, never `config`" invariant tree-wide, and `TestImports_FoundationOnly` only mirrors it in-package. A rule lost to a merge weakens §3.2 layering with no failing test |
| V2-MERGE-06 | **Contract-fixture MANIFESTs match their bytes tree-wide.** Each subplan ships its own `testdata/golden/contracts/<pkg>/MANIFEST.json` with `{file, kind, bytes, sha256}`; nothing verifies them across packages after six merges | For every `testdata/golden/contracts/*/MANIFEST.json`, recompute `sha256` and `bytes` for each listed file and compare | Every entry matches. A merge that took one side of a fixture and the other side of its MANIFEST is internally inconsistent and will surface at wave 2 as an unexplained W-2 divergence rather than as a merge error. Run **before** any `-update` regeneration in §7 |
| V2-MERGE-07 | **The sketch W-2 fixtures exist and are actually consumed.** `testdata/golden/contracts/sketch/` does **not** exist on `develop` before wave 1 — there are no SP-01 placeholders — and SP-03 creates it in its commit 6 of 8. SP-04 and SP-06 are told to test against it under Rule W-2, but could not have during wave 1 | `ls testdata/golden/contracts/sketch/` ; `grep -rn "contracts/sketch\|contracts.*sketch" internal/canon internal/store --include=*_test.go` | Five `.bin` fixtures plus `MANIFEST.json` present, **and** at least one test in `internal/canon` and one in `internal/store` actually reads them. V2-SP01-19 proves no `t.Skip` remains in those packages, but absence of a skip is not presence of a test — a W-2 obligation that was never written is indistinguishable from one that passes, and this is the row that separates them |
| V2-MERGE-08 | **Every `00-ARCHITECTURE.md` §5.x signature exists verbatim after six parallel copies.** Each subplan copied its §5.x block "byte-identical" on its own branch; nothing checks the union | For each of §5.7 (`sketch`), §5.8–§5.10 (`chunk`/`canon`/`symbols`), §5.11 (`store`), §5.12 (`dag`), §5.13 (`ipc`/`daemon`): extract the declared signatures and confirm each appears in the package with the same name, receiver, parameter types and return types | Exact match for every entry. The compiler catches a shape change only where a consumer already exists; a renamed method or a widened parameter on a surface no wave-1 package calls yet stays invisible until SP-08…SP-16 try to call it, which is the most expensive possible time to find it |
| V2-MERGE-09 | **`//nomagic:allow` inventory matches what the subplans authorized.** Each subplan permits a specific, named set; nothing enumerates the union | `grep -rn "nomagic:allow" --include=*.go .` | Every annotation is one a subplan explicitly declared, with its stated reason. SP-03 authorizes exactly one (`FPWarnRate` in `internal/sketch/bloom.go`). Any annotation not traceable to a subplan's Implementation spec was added to silence a lint failure rather than to record a decision, and is a failure of `00-ARCHITECTURE.md` §11.6 regardless of the build being green |
| V2-MERGE-10 | **Each branch respected its own out-of-scope allowlist.** Every subplan's Done checklist asserts this against its own branch; nobody asserts it against the merged result, and a file touched by two branches shows in neither branch's self-check | For each branch, `git diff --name-only develop...feat/spNN-<name>` compared against that subplan's "Out-of-scope discipline" / "Out-of-scope respected" checklist line | Every changed path is on that subplan's allowlist. Files touched by **two** branches are listed explicitly in the completion report even when both were entitled to them — `testdata/bench-baseline.txt` (five branches) and `.github/workflows/ci.yml` (two) are known; `tools/devtool/importrules.go` is on no allowlist at all and must be accounted for by name |
| V2-MERGE-11 | Design and plan documents unmodified by feature work | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` ; `git diff main..develop --name-only -- plans/` | `Qompack.md` empty (this is V2-ALL-05, restated here because it is a merge property). For `plans/`: any change is intentional and named in the completion report. Plan edits made on a feature branch ride into `develop` attributed to that subplan's merge and are invisible to sibling worktrees while the wave is in flight; they belong on `develop` directly |
| V2-MERGE-12 | `plans/OWNERS.tsv` agrees with reality | `go run ./tools/devtool cover` ; `go test -run TestNightlyFuzzMatrix ./test/guards/` | `cover` fails if OWNERS.tsv claims an owner for a package still returning `ErrNotImplemented` (§3.1 item 2), and `TestNightlyFuzzMatrix` reads the same column to decide whether a missing fuzz target is waived. Both consumers must agree: a package that landed in wave 1 must no longer be treated as a waivable stub by either |
| V2-MERGE-13 | **The §6.4 coverage floors actually run for the packages wave 1 landed.** On `develop`, `tools/devtool/cover.go` applies a floor only when a row's owner is `SP-01`; every other row prints `exempt (stub, owned by SP-NN)` and exits 0 at any coverage, including 0 %. Eleven packages with a non-SP-01 owner arrive from wave 1 with a declared floor that nothing checks — a gate that stops guarding without saying so, not a failing test | `go run ./tools/devtool cover 2>&1 \| grep -E '^(OK\|exempt \(stub).*(sketch\|chunk\|canon\|symbols\|store\|redact\|dag\|ipc\|daemon\|contract\|eval)'` ; then `go test -cover -count=1 ./internal/...` and compare each package against its `plans/OWNERS.tsv` floor by hand | `OK <pkg>: NN.N% >= floor NN%` for **all eleven** — `eval` 85, `sketch` 90, `chunk` 90, `canon` 90, `symbols` 75, `ipc` 75, `daemon` 75, `contract` 75, `store` 90, `redact` 75, `dag` 85 — with **no** `exempt (stub, …)` line naming any of them. Two branches already fixed this independently and incompatibly; **V2-MERGE-14 is the row that actually closes it**, and this row is the observable. Raised first by SP-03 (**§2.3a** item 2), again by SP-04 (**SP04-D4**), again by SP-02 (**§2.2 preamble** §4.1) and again by SP-07 (**§2.7 preamble**, ACTION 2) — four independent discoveries of one hole |
| V2-MERGE-14 | **`tools/devtool/cover.go` was rewritten twice, differently, and the two rewrites do not compose by themselves.** SP-02 and SP-04 both replaced the `o.Owner != "SP-01"` skip. SP-02 added `landedSubplans = {SP-01, SP-02}` plus a `floorApplies(o, dir)` helper that prints every exemption reason. SP-04 added `landedSubplans = {SP-01, SP-04}` plus a `probeBlind` set and, inside the loop, a **failure** when an exempt package's probe no longer looks like a stub. Same function, same purpose, incompatible text — git will conflict, and either side taken whole loses the other's half | `grep -n 'landedSubplans\|probeBlind\|floorApplies' tools/devtool/cover.go` ; `go run ./tools/devtool cover` ; `go test ./tools/devtool/ -run TestLandedSubplans -v` | The merged file keeps **both** halves: `floorApplies` (SP-02) *and* `probeBlind` + the exempt-but-not-a-stub failure (SP-04), with `landedSubplans` listing **`SP-01, SP-02, SP-03, SP-04, SP-05, SP-06, SP-07`**. Taking either side alone leaves five to six subplans unlisted, and SP-04's exempt-but-not-a-stub check then fires for `sketch`, `ipc`, `daemon`, `store` and `dag` — which is the alarm working, not a reason to add them to `probeBlind`. **`probeBlind` must keep exactly its four entries** (`scheduler`, `grammar`, `contract`, `redact`); adding a landed wave-1 package to it is the wrong remedy and is called out by name in SP04-D4. Also rewrite `TestLandedSubplansMatchesTheBranch`, whose failure message still reads "SP-03 has not landed" and is backwards once the wave is in (SP-02's handoff §4.1) |
| V2-MERGE-15 | **`internal/testutil/fixtures_test.go`'s frozen-fixture count is changed to `28` by two branches, for different reasons, and `28` is wrong for the merge.** SP-03 raises 23 → 28 for its five QPKS sketch frames. SP-05 raises 23 → 28 for its three `ipc` and two `contract` fixtures. The correct merged total is **33**, and no branch says so | `go test -run TestContractFixture_EveryManifestIsReadable ./internal/testutil/` ; `grep -n 'frozenCount\|pendingCount' internal/testutil/fixtures_test.go` | `require.Equal(t, 33, frozenCount)` — 21 SP-01 + 2 V1 + 5 SP-03 + 3 SP-05 task 1 + 2 SP-05 task 4 — with a comment naming all five groups, and `pendingCount` still 5. **If SP-07's ACTION 1 is taken (recording `backward_slice_scores`), the pair becomes 34 / 4 in the same commit.** This one fails loudly rather than silently, which is the only good news here: the trap is that both branches agree on the literal `28`, so a resolution that "takes the number both sides wanted" is wrong |
| V2-MERGE-16 | **The wave-0 stub guards were edited by four branches each, in overlapping regions.** `test/guards/stubs_test.go` is touched by SP-02, SP-03, SP-06, SP-07 (each flipping its own package to `pureMethods: allMethodsAreReal`, and SP-07 also rewriting the shared `allMethodsAreReal` doc comment). `test/guards/v1_integration_test.go` is touched by SP-02, SP-03, SP-05, SP-06 — and SP-02's single hunk at `TestV1_StubGraphIsInertAndOwned` **overlaps SP-06's**, while SP-03's and SP-05's new imports land in the same import block | `go test ./test/guards/ -v` ; `grep -n 'allMethodsAreReal' test/guards/stubs_test.go` | Green, and `allMethodsAreReal` is set for **every** wave-1 package: `eval`, `sketch`, `chunk`, `canon`, `symbols`, `ipc`, `daemon`, `store`, `redact`, `dag` (plus `contract`, already real in wave 0). A package missing the marker asserts it is still a stub, which is now false; a package that has it but is *not* real is the opposite failure. `v1AssertZeroPayloads` and `TestV1_StubGraphIsInertAndOwned` must be probe-aware (SP-06's form) **and** still carry SP-02's edit |
| V2-MERGE-17 | **`internal/cli/commands.go` is edited by two branches within two lines of each other.** SP-02 appends `evalCmds()` to `All()`; SP-05 adds `daemon` and `self-test` as real `Cmd` literals and deletes their two rows from `notImplemented` | `go run ./cmd/qompack --help` ; `go test -run 'TestAll_\|TestCommands_' ./internal/cli/` ; `grep -n 'notImplemented' -A 20 internal/cli/commands.go` | `eval`, `daemon` and `self-test` are all registered and all runnable; **neither `daemon` nor `self-test` remains in `notImplemented`**, and `eval` is not missing. A resolution that took one side leaves either the replay-gate driver unreachable from the CLI (breaking V2-SP02-15) or the daemon advertised as unimplemented while it is running (breaking §2.5 outright) |
| V2-MERGE-18 | **`testdata/golden/contracts/**` contains files no MANIFEST declares.** V2-MERGE-06 checks MANIFEST → bytes. This is the other direction, and it already fails — for **seven** files, not the two this row first named. SP-02 added `testdata/golden/contracts/store/stats-growth.json` and `testdata/golden/contracts/negknow/health.json` — directories owned by SP-06 and SP-09 — and neither appears in its package's `MANIFEST.json`. Both are real fixtures, read by `internal/eval/growth_test.go` and `test/replay/growth.go`, and neither is on SP-02's own out-of-scope list. **SP-07 adds five more, in its own package's directory**: `crossing.json`, `graph-basic.jsonl`, `nodeid.json`, `slice-backward.json`, `thin-vs-full.json`, with `testdata/golden/contracts/dag/MANIFEST.json` byte-identical between `develop` and `wave1/sp07-shipped` — it still declares only `node_line`, `edge_line` and the `record-by-owner` `backward_slice_scores`. Nothing fails today, and the reason is worth knowing before deciding: `internal/testutil/fixtures_test.go` counts MANIFEST **entries**, not files on disk, so 33/5 is unaffected and this row has no test behind it at all | For every `testdata/golden/contracts/<pkg>/`, list the files on disk and subtract the paths its `MANIFEST.json` declares (`input` + `want`, plus `MANIFEST.json` itself) | Empty difference for every package, **or** each surplus file explicitly declared. Decide one of: declare them in the owning MANIFEST, or move them to a path SP-02 owns (`testdata/golden/eval/growth/…`) and update the two readers. Leaving them is the worst option — SP-09 records `three_way_answer` into `negknow/` in wave 3 and SP-06's `gen-contract-fixtures` regenerates `store/`, and an undeclared neighbour in either directory is the kind of thing an `-update` run deletes without comment |
| V2-MERGE-19 | **`plans/CARRIED-DEFECTS.tsv` records one subplan's carried defects, not the wave's.** Six rows exist and all six are SP-04's. SP-02, SP-03, SP-05, SP-06 and SP-07 each shipped carried items of the same kind, recorded only in prose in this file and in the two handoff documents — so `TestCarriedDefects_WaveReportRequiresResolution`, the gate that refuses to let `plans/V2-report.md` be written over an open row, **cannot see any of them** | `go test ./test/guards/ -run TestCarriedDefects -v` ; `awk -F'\t' '!/^#/ && NF==6 {print $2}' plans/CARRIED-DEFECTS.tsv \| sort \| uniq -c` | Either every wave-1 carried item has a row, or the decision not to add them is recorded in the completion report by name. Note the mechanical obstacle before choosing: `test/guards/carrieddefects_test.go` hardcodes `carriedDefectsDoc = "plans/V2-SP-04-carried-defects.md"` and requires a `## <id>` section **in that one file**, so an `SP06-D1` row needs either a section in a document titled for SP-04 or a per-subplan doc lookup in the guard. Fix the guard, or say plainly that the wave's other carried items are governed by prose alone |
| V2-MERGE-20 | **Untracked decision records are captured before `verify/v2` is cut — and one wave-1 subplan has 30 of them.** SP-05's rulings ledger, seven per-task reviews and seven review diffs live in `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/`, ignored by a one-line `*` in **`.superpowers/sdd/.gitignore` — a file inside the ignored tree**, so `grep -i superpowers .gitignore` at the repo root finds nothing and `git status` is clean. Nothing in the merge carries it, and `git clean -fdx` destroys it | `git check-ignore -v .superpowers/sdd/*/` ; for each worktree in `git worktree list`, `ls .superpowers/sdd/` | Every subplan's decision record is either committed somewhere under `plans/` or `docs/`, or copied out before this checkpoint runs. **Verified at authoring time: only `qompack-sp05` holds one** (`qompack` and `qompack-sp03` have an empty `.superpowers/sdd/`; sp02, sp06 and sp07 have none) — but SP-05's is exactly the one whose subplan wrote no tracked handoff, so the wave's least-documented group is also the only one whose documentation is one `git clean` from gone. **Do this first**; it is the only row here whose evidence lives outside git entirely. See §2.5a A |
| V2-MERGE-21 | **The plan set stated two different wave-1 merge orders and BOTH were wrong.** `plans/README.md` step 3 prescribed **SP-05 → SP-03 → SP-04 → SP-06 → SP-07 → SP-02**; §8's completion-report line in this document asserted the plain numeric **SP-02 → SP-03 → SP-04 → SP-05 → SP-06 → SP-07**. The order is not a matter of documentary authority: `test/guards/buildorder_test.go` **enforces it as a test**, and README's order fails it. `TestGuard_Phase0BeforeStore` — SP-01's, commit `14abe48`, transcribing `Qompack.md` closing note 1 *"without measurement, everything else is opinion"* — fails whenever `internal/store` is real while `internal/eval` is still a stub. `eval` is SP-02's, so putting SP-02 last leaves `develop` red from SP-06's merge until the wave's final commit | `go test -run TestGuard_Phase0BeforeStore ./test/guards/` at **every** wave-1 merge commit, not just the last: `for c in $(git log --first-parent --format=%H <wave-start>..develop); do git checkout -q $c && go test -run TestGuard_ ./test/guards/; done` ; `grep -n 'Wave 1: SP-' plans/README.md` ; `git log --first-parent --oneline develop` | Two binding constraints, and an order satisfying both. **C1: SP-06 lands after SP-03 and SP-04** (`plans/V2-SP-06-content-addressed-store.md` line 1225, its own exit criterion). **C2: SP-02 lands before SP-06** (`TestGuard_Phase0BeforeStore`; §2.6a ① states the same and *assumes* an order that lands SP-02 first, which README did not provide). Executed order: **SP-05 → SP-03 → SP-04 → SP-02 → SP-06 → SP-07**, which satisfies both, keeps README's "heaviest shared infrastructure first" intent, and leaves every intermediate `develop` commit green. `plans/README.md` and §8 were both corrected to it, and README now carries the reason so the next wave's order is not reordered casually. Also confirm the companion rule from the same paragraph: **conflicts were resolved on the incoming branch and re-merged, never hand-edited into the merge commit** — each `feat/sp*` tip therefore carries a `chore(spNN): merge develop …` commit, and every merge into `develop` is conflict-free by construction. **Both halves have been run, so this row is a reproduction rather than an assertion.** The walk is green: all **11** first-parent commits from `7340536` (the V1 checkpoint) through `1b66239` pass all four `TestGuard_` build-order guards, so no intermediate commit is red — re-run it and expect 11 × PASS. The counterfactual is red on demand: `git checkout --detach 4ead708 && git merge --no-ff wave1/sp06-shipped` (SP-06 before SP-02, as README prescribed; resolve its two conflicts on the incoming branch per the rule above) makes `TestGuard_Phase0BeforeStore` fail with `buildorder_test.go:24: store (Phase 1) is implemented while eval (Phase 0) is still a stub`. Do this in a throwaway `git worktree --detach` and discard it; the point is that the published order is *demonstrably* unsatisfiable, not merely unattested. **The general lesson is the row, not the order:** a merge sequence is testable, so test it per commit rather than reading it off a document |
| V2-MERGE-22 | **A `_test.go` file may import a composition root until the import closure happens to close, and nothing checks it.** `tools/devtool/importrules.go` states the rule — a composition root "may import anything; nothing may import them" — but no check enforces its second half for test files. `importgraph` reads `go list`'s `.Imports`, which excludes test-only imports; `testdeps` enforces the opposite direction (production source must not import a test-only *module*). So `internal/dag`'s in-package tests imported `internal/testutil` for one clock helper and it compiled for as long as nothing on `testutil`'s side reached back. SP-05 gave `internal/daemon` a dependency on `internal/dag` in the same wave and it stopped compiling: `dag → testutil → cli → daemon → dag` | `go list -f '{{.ImportPath}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./... | grep -E 'internal/(testutil|cli|commands|daemon)'` ; then check each hit against `compositionRoots` | No `internal/*` package outside `compositionRoots` names one in `.TestImports` or `.XTestImports`. **The fix is the check, not the one import**: extend `importgraph` to read `.TestImports`/`.XTestImports` and apply the composition-root half of §3.2 to them. The `dag` instance is already fixed on `develop` (§2.0b row 5); the point of this row is that it was found by a build failure two branches apart rather than by the lint that owns the rule, and every other package's tests are still unchecked. Owner: §7.2a agent **F-1** |
| V2-MERGE-23 | **`TestCarriedDefects_OpenRowsHaveLivingEvidence` reports "no such test exists" when the module merely fails to build — and tells you to close the row.** Observed during the wave-1 merge: while `internal/dag` did not compile, the guard failed for **SP04-D1, D2 and D3**, whose evidence tests are in `internal/canon`, a package that built fine. It discovers tests by listing them across the module, got nothing usable from a module that would not build, and read "not found" as "does not exist" | Break any package deliberately (add `var _ = undefinedSymbol` to a scratch file in `internal/dag`), run `go test ./test/guards/ -run TestCarriedDefects`, then revert | The guard fails with a message naming the **build failure**, not one naming a defect ID. As written its advice is actively wrong in this state — *"If the defect was fixed, set SP04-D1's status to `fixed` in plans/CARRIED-DEFECTS.tsv"* — so following it closes three open rows on the strength of an unrelated compile error. Fail loudly and separately when the listing command errors, instead of treating an empty result as absence. Owner: §7.2a agent **F-2**, which holds `test/guards/carrieddefects_test.go` |
| V2-MERGE-24 | **`ci-local`'s `fmt-check` is stricter than `gofmt`, and SP-04's branch tip failed it — its own exit criterion — with nothing in the wave noticing.** `tools/devtool/fmt.go` runs the pinned **`gofumpt -l`**; `gofmt -l` is clean on the same tree. `internal/chunk/fastcdc_test.go` carried a blank line before a closing brace from SP-04's first chunk commit `53b839e` through every later commit and the merge. Per-commit gating is `devtool test`, which does not include `fmt-check`, so only the final-commit `ci-local` could have caught it and evidently was not run — or was run before the last commit. Walked all six tips: **SP-04 alone is red**; SP-02, SP-03, SP-05, SP-06 and SP-07 are green | `go run ./tools/devtool fmt-check` — and at each tip: `for t in wave1/sp0{2,3,4,5,6,7}-shipped; do git checkout -q --detach $t && go run ./tools/devtool fmt-check; done` in a throwaway worktree | Already fixed on `develop` — confirm `fmt-check` exits 0 and that the fix was the file conforming, not the check relaxing. **The row is the class, not the blank line.** Two things it exposes: a subplan's final-commit `ci-local` requirement has no enforcement, so "SP-0N's tip was green" is an unverified claim for every branch until walked; and any verification that substitutes `gofmt` for `devtool fmt-check` silently passes a tree CI would reject. Decide whether `devtool test` should include `fmt-check` — it is the cheapest step in the pipeline and it is the one that halts `ci-local` before anything informative runs |

**Known defects at authoring time.** These were verified against `develop` and against all six wave-1 branch tips (`git grep 'func Fuzz' <branch>` per branch, reconciled on the `(pkg, fn)` pair). They are listed so this checkpoint **fixes** them rather than spending a subagent rediscovering them. All of them are V2-MERGE-01 / V2-MERGE-02 failures:

`nightly.yml` is edited by **SP-05 only** and SP-05 did **not** change the fuzz matrix — the eight rows on `develop` are byte-identical to the eight on `feat/sp05-…`. So every fuzz target SP-03, SP-04 and SP-06 shipped arrives unregistered by construction, and three matrix rows name functions that do not exist anywhere in the merged tree.

| `nightly.yml` declares | Owner | What wave 1 actually ships | Consequence |
|---|---|---|---|
| `{pkg: ./internal/chunk, fn: FuzzSplit}` | SP-04 | `FuzzSplit` — **the name matches** | **Resolved on SP-04's branch, and must survive the merge.** The guard originally asserted `require.False(exists)` for any non-SP-01 package, so a target that had correctly landed read as a *stale waiver* and hard-failed. SP-04 inverted that clause: an existing target now satisfies the matrix whoever owns its package. Only SP-04 touches `test/guards/nightlyfuzz_test.go`, so nothing should conflict — but confirm the inverted clause is present, because on `develop`'s version this row is a red build |
| `{pkg: ./internal/canon, fn: FuzzCanonicalize}` | SP-04 | `FuzzCanonicalize` — **the name matches** | **Not a defect.** Verified on the branch tip: `internal/canon` ships `FuzzCanonicalize` exactly. (An earlier draft of this section recorded it as `FuzzCanonicalizeRun`; that was wrong, and the four *other* canon targets below are the real gap) |
| `{pkg: ./internal/sketch, fn: FuzzUnmarshalBinary}` | SP-03 | `FuzzBloomUnmarshalBinary`, `FuzzCMSUnmarshalBinary`, `FuzzHLLUnmarshalBinary`, `FuzzMisraGriesUnmarshalBinary`, `FuzzSignatureUnmarshalBinary` — five targets, none with that name | **Silent.** All five committed seed corpora under `testdata/corpora/sketch/**` are never exercised by CI. This is the package whose decoders are the entire fuzz rationale (SP-03: "every rejection path — which is the whole of the fuzz surface") |
| `{pkg: ./internal/ipc, fn: FuzzFraming}` | SP-05 | `FuzzDecodeRequest` | **Silent.** The NDJSON framing decoder is never fuzzed |
| `{pkg: ./internal/redact, fn: FuzzRedact}` | SP-06 | `FuzzRedactIdempotent`, `FuzzRedactPrefilterEquivalence` | **Silent, and actively misleading.** A function named exactly `FuzzRedact` *does* exist — in `internal/eval`, shipped by SP-02 — so a reconciliation done on function names alone concludes this row is satisfied. It is not: the matrix pins `(pkg, fn)` together, `internal/redact` has no `FuzzRedact`, and `internal/eval`'s has no matrix row. **Reconcile on the pair, never on the name.** `FuzzRedactPrefilterEquivalence` is the target that proves SP-06's mandatory-literal prefilter did not change what gets redacted (see §2.6a ②) — the one most worth fuzzing and the one least likely to be noticed missing |

`{pkg: ./internal/checkpoint, fn: FuzzCheckpointJSON}` is the **only** legitimately waived row: `internal/checkpoint` belongs to SP-10, wave 4. `hookio`/`FuzzReadEvent` and `config`/`FuzzConfigLoad` are SP-01's and exist.

**Fifteen targets ship in wave 1 with no matrix row at all**, not the eight an earlier draft counted:

| package | unregistered targets | n |
|---|---|---|
| `internal/canon` | `FuzzRestore`, `FuzzNumericMatchesAgreeWithReference`, `FuzzPrefilterAgreesWithFullScan`, `FuzzTrailingWSAgreesWithReference` | 4 |
| `internal/sketch` | all five (the matrix's sixth name matches none of them) | 5 |
| `internal/redact` | `FuzzRedactIdempotent`, `FuzzRedactPrefilterEquivalence` | 2 |
| `internal/chunk` | `FuzzSplitStream` | 1 |
| `internal/symbols` | `FuzzExtract` — **the package has no matrix entry whatsoever** | 1 |
| `internal/ipc` | `FuzzDecodeRequest` | 1 |
| `internal/eval` | `FuzzRedact` | 1 |

Three orphan matrix rows, fifteen unregistered targets, and two of the three orphans are the decoders the fuzzing exists for.

The fix belongs here and nowhere else. `test/guards/nightlyfuzz_test.go` says so in its own doc comment: *"What this still cannot catch is a subplan landing its package without writing the target the matrix claims: that belongs to the subplan's own definition of done and **to the wave checkpoint, which re-runs this inventory**."* Repair `nightly.yml`, update the `require.Len(t, matches, 8)` arity to whatever the repaired matrix declares, and record the before/after matrix in the completion report.

#### 2.0a Cross-branch collision inventory — the evidence behind V2-MERGE-10 and V2-MERGE-14 … 24

Computed by diffing every wave-1 branch against `develop` and intersecting the path lists. **Ten files are touched by more than one branch.** This is the complete set; a file not on it was touched by at most one branch and cannot collide.

| file | branches | how it merges | what to require |
|---|---|---|---|
| `testdata/bench-baseline.txt` | SP-02, SP-03, SP-05, SP-06, SP-07 | conflicts on every merge after the first | V2-MERGE-03. Union of `pkg:` blocks: SP-01's `cli`/`config`/`obs`/`paths` plus `eval`, `sketch`, `ipc`, `daemon`, `store`, `redact`, `tokens`, `dag`. **Note `chunk`, `canon` and `symbols` are absent from every branch** — SP-04 committed no `bench-baseline` rows at all, so §5.3's gate has never had a baseline for them; that is a gap to close here, not a merge loss to hunt for |
| `test/guards/v1_integration_test.go` | SP-02, SP-03, SP-05, SP-06 | **SP-02's and SP-06's hunks overlap** inside `TestV1_StubGraphIsInertAndOwned`; SP-03's and SP-05's new imports land in the same import block | V2-MERGE-16 |
| `test/guards/stubs_test.go` | SP-02, SP-03, SP-06, SP-07 | four additive edits in different rows, plus SP-07 rewriting the shared `allMethodsAreReal` doc comment | V2-MERGE-16 |
| `tools/devtool/importrules.go` | SP-02, SP-04, SP-05 | SP-02 adds `test/replay`, SP-04 adds `test/dedup`, SP-05 adds `test/bench/hotpath` **and re-indents the whole map**, so the alignment change collides with both other additions | V2-MERGE-05. All three composition roots present after the merge; `go run ./tools/devtool lint` green |
| `tools/devtool/cover.go` | SP-02, SP-04 | two incompatible rewrites of the same function | V2-MERGE-14 |
| `plans/V2-VERIFY-primitives-store-dag-and-baseline.md` | SP-04, SP-06, SP-07 | three additive edits in three different regions — merges **cleanly and silently** | §2.6a ⑧. This file. Its own detection greps are below |
| `.github/workflows/ci.yml` | SP-02, SP-05 | adjacent job blocks | V2-MERGE-04. Verified state: on `feat/sp02-…`, `replay-gate` has lost `continue-on-error` and **`bench-gate` still has it**; on `feat/sp05-…`, exactly the reverse. Neither branch is correct on its own — only the union is |
| `internal/cli/commands.go` | SP-02, SP-05 | SP-02 inserts at line 30, SP-05 at line 29 and deletes two `notImplemented` rows | V2-MERGE-17 |
| `internal/testutil/fixtures_test.go` | SP-03, SP-05 | both rewrite the same `require.Equal` and its comment, both to `28` | V2-MERGE-15 |
| `internal/testutil/project_test.go` | SP-05, SP-06 | different functions (`TestProject_StoreOpens` vs the hook-dispatch comment) — merges cleanly | Confirm both edits survived: SP-06's `PutBytes` no longer expects `ErrNotImplemented`, SP-05's six-hook comment reflects the thin-client behaviour |

**Three files SP-03 flagged as cross-branch conflict candidates are, in fact, single-branch:** `internal/core/hash.go`, `internal/paths/appendonly.go` and `.gitignore` are touched by SP-03 alone. Its warning about `core.HashBytes` regressing to `h.Sum(nil)` if another branch's version wins the merge is therefore moot — no other branch has a version. `TestHashBytes_DoesNotAllocate` still belongs in the §2.1 re-run as the standing guard.

**This file's own merge, verbatim from §2.6a ⑧** — run it before anything else, because it is the only check whose evidence the merge itself can destroy:

```bash
F=plans/V2-VERIFY-primitives-store-dag-and-baseline.md
grep -c  '^#### 2.0a'   "$F"   # the collision inventory (this section)  → must be 1
grep -c  '^#### 2.3a'   "$F"   # SP-03's half                            → must be 1
grep -c  '^#### 2.2a'   "$F"   # SP-02's half                            → must be 1
grep -c  '^#### 2.5a'   "$F"   # SP-05's half                            → must be 1
grep -c  '^#### 2.6a'   "$F"   # SP-06's half                            → must be 1
grep -c  '^#### 2.7a'   "$F"   # SP-07's half                            → must be 1
grep -c  '^## 0. Map'   "$F"   # the document map                        → must be 1
grep -cE 'V2-MERGE-(1[4-9]|2[0-4])' "$F"  # the eleven post-review merge rows → must be > 0
grep -c  '^#### 2.0b'   "$F"   # what the merge itself found      → must be 1
grep -c  '^> \*\*Recommended model' "$F"   # SP-04's header, 2nd witness → must be 1
test "$(grep -c '^````' "$F")" -eq 2 && echo "report fence ok"   # must print
```

The last line is a **format** check rather than a merge check, and it is here because the failure it catches is invisible: §8's completion-report template is wrapped in a **four**-backtick fence precisely because it contains a three-backtick `bash` block. If someone "normalises" the outer fence back to three backticks, the template silently terminates at that inner block and the rest of the report renders as live document structure. It has happened once already.

Every pattern is anchored to a **heading at start of line**, which is what makes it self-exclusive: the `grep` lines above are prose inside a fenced block and start with `grep`, not with `####`, so this check cannot satisfy itself. (Written the obvious way first — `grep -c "V2-SP07-16 is wrong as written"` — and it returned 2 on a file where the only two matches were the section it was testing for and *the grep line itself*, i.e. it would report success on a file that had lost the section entirely and kept only this warning. Do not "simplify" these back to prose matches.)

#### 2.0b What the wave-1 merge actually found — six defects fixed before this checkpoint ran

**Read this before §2.0's table.** The merge did not just apply the gates above; it was itself an
experiment, and the result changes what several rows mean. Six defects surfaced only once two
branches were in one tree. **Four of the six produced no merge conflict at all** — git reported
those files as cleanly merged — which is the finding underneath the individual findings: a merge
integrity gate that inspects only the files git flagged would have passed every one of them.

Each is fixed on `develop` and named here so this checkpoint confirms rather than rediscovers them.
The merge commits carry the full reasoning; `git log --first-parent develop` is the index.

| # | What | Conflict? | Where it was fixed |
|---|---|---|---|
| 1 | **`.ndjson` lost its round-trip arm.** SP-05 froze `ipc/observe_tool.ndjson` and `ipc/response_reply.ndjson` against a `v1RoundTrip` with *no* extension dispatch — everything non-binary fell through to the JSON path, so `.ndjson` was checked without ever being named. SP-03 then made the dispatch exhaustive with a fatal `default`, for its `.bin` QPKS frames. Each branch correct alone; the pair rejects two fixtures that had been passing | **No** — different hunks | `test/guards/v1_integration_test.go`, merge of SP-03. `.ndjson` joins the JSON arm; **not** `v1BinaryFixtureKeys`, which would have downgraded the check to "file is non-empty" |
| 2 | **§2.6a was duplicated.** Merging `develop` into `feat/sp06-…` produced a V2-VERIFY with `#### 2.6a` **twice**. §2.6a's own item ⑧ predicted this mechanism and predicted the wrong direction: it warns a half may go *missing*. The half was duplicated instead — same cause, opposite symptom, and a reader checking only for absence passes it | **No** | Caught by §2.0a's self-check, which asserts the heading count is **1** rather than non-zero. That "must be 1" had never fired before |
| 3 | **Corpus data added to a tree whose validator another branch owns.** SP-06 added `testdata/corpora/toolout/sp06/fileread-auth-v{1..4}.txt` with no `.meta.json` sidecars; SP-04's `internal/canon/golden_test.go` walks that whole tree and requires one per file, as does `test/dedup`'s loader. Broke four canon tests, `FuzzCanonicalize`'s seed loader and two dedup tests | **No** — one branch added data, the other added the rule | Merge of SP-06. Completed to SP-04's convention rather than moved out: they are genuine `Read` outputs and both consumers discover the tree by design. Goldens are byte-identical to source, matching the existing `fileread` group where canon is correctly a no-op. `canon-dedup-report.json` gains one group (`sp06`, ratio 1.485) and **no existing group's numbers change**; overall moves 1.151 → 1.1945 without canon, 1.1823 → 1.2249 with |
| 4 | **A test double with an expiry date nobody wrote down.** `withStubChunker()` was `chunk.New(chunk.DefaultParams())`, *borrowing* SP-01's stub because that stub returned no chunks. SP-04 landed the real FastCDC chunker in the same wave, so it began splitting and `TestPutBytes_ChunkerDegradedGuard` stopped exercising its guard. The tell is exact: `require.Len(res.Root.Chunks, 1)` kept passing for a different reason and only the counter assertion failed | **No** | Merge of SP-06. Replaced with an explicit `silentChunker`. Same assertion strength, no dependency on another package being unimplemented |
| 5 | **An import cycle.** SP-07's in-package `dag` tests imported `internal/testutil` for `NewFakeClock`/`Epoch`; SP-05 gave `internal/daemon` a dependency on `internal/dag`. `dag → testutil → cli → daemon → dag` | **No** | Merge of SP-07. `internal/dag/clock_test.go` — a local two-method `core.Clock` double; the goldens still reproduce, confirming the local epoch matches `testutil.Epoch`. **See V2-MERGE-22** |
| 6 | **`plans/README.md`'s merge order was unsatisfiable.** Its order lands SP-06 before SP-02, and `TestGuard_Phase0BeforeStore` fails while `store` is real and `eval` is a stub | Yes, in a sense — a *test* conflicted | `plans/README.md` and this file, at `fix(plans): correct the wave-1 merge order`. **See V2-MERGE-21** |

**One defect was recorded rather than fixed, deliberately.** `internal/ipc/ipctest`'s transport
suite failed once, under load, on a tree whose `internal/ipc` was byte-identical to `develop`'s, and
passes 3/3 in isolation. It is a flake — but the reason it is *undiagnosable* is the row: the failing
line is `require.True(t, res.OK)` after a `Send` that returned **no error**, because §5.4's
never-error rule turns an unreachable daemon into `Response{OK:false}`. The transport failure
reaches the test as a bare *"Should be true"* with the reason sitting unread in `res.Err`. Carrying
`res.Err` in those assertion messages strengthens them and costs nothing. **Owner: §7.2a agent F-7**,
which holds `internal/ipc` exclusively — which is also why it was not fixed inside a merge commit.

**What this means for §2.0's table.** V2-MERGE-01 … V2-MERGE-13 were written against the branches
as they stood and are unchanged. The rows to read differently: **V2-MERGE-14 is already applied**
(`landedSubplans` is SP-01…SP-07, reached one entry per merge, so no coverage row was ever vacuous
on `develop`); **V2-MERGE-15 confirmed exactly as written** (both branches wrote `28`, the answer is
`33`); **V2-MERGE-16's overlap is resolved to something stronger than either side** (SP-06's probe
with SP-02's full list, plus a non-vacuity guard, because a probe-aware sweep in which everything
has landed asserts nothing); **V2-MERGE-04 and -17 came out right from the auto-merge** and were
verified rather than assumed; and **V2-MERGE-18's file list was wrong** — seven files, not two.

### 2.1 SP-01 — foundation, toolchain, contracts (wave 0, re-verified)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP01-01 | Repo shape: `main`/`develop`, root commit contents | `git log --oneline --all \| tail -3` ; `git show --stat $(git rev-list --max-parents=0 HEAD)` | root commit contains only `Qompack.md`, `plans/*`, `.gitignore`, `LICENSE` |
| V2-SP01-02 | Module builds; vet clean | `go build ./... && go vet ./...` | exit 0, no output |
| V2-SP01-03 | Full lint chain: golangci-lint, `nomagic`, importgraph, testdeps, bindeps, sleepcheck, stubskips | `go run ./tools/devtool lint` | exit 0. `importgraph` passes against the real repo with six new real packages; `bindeps` proves `go list -deps ./cmd/qompack` contains only stdlib, `github.com/qompack/qompack/…`, `klauspost/compress`, `Microsoft/go-winio` |
| V2-SP01-04 | Formatting | `go run ./tools/devtool fmt-check` | prints nothing |
| V2-SP01-05 | `internal/core`: hash, ids, clock, sentinels | `go test ./internal/core/...` | all green, incl. `TestHashBytes_DomainSeparation`, `TestHash_StringShortParse_RoundTrip`, `TestNewDecisionID_Format`, `TestSentinels_AreDistinct` |
| V2-SP01-06 | `internal/paths`: resolve, Norm/Key, long paths, atomic write | `go test ./internal/paths/...` | all green, incl. `TestResolve_WalksToGitDir`, `TestNorm_RejectsEscape`, `TestLongPath_Over260`, `TestWriteAtomic_ReplacesAndSyncs` |
| V2-SP01-07 | **Append-only guard** (§7.4 / §4.6, mechanical) | `go test -run TestAppendOnlyGuard ./internal/paths/` | all five illegal writes against `checkpoints/`, `pins/`, `sketches/tried.bloom` fail; `TestCreateNew_SetsReadOnly` and `TestReplaceBloom_KeepsOneBackup` green |
| V2-SP01-08 | Appendix C defaults reproduced verbatim | `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` | green — `Defaults()` minus `runtime` deep-equals `testdata/golden/config/appendix-c.jsonc` |
| V2-SP01-09 | Config: 5-layer precedence, deep merge, env mapping, null semantics | `go test ./internal/config/...` | all green, incl. `TestLoad_PrecedenceFiveLayers`, `TestLoad_DeepMergePerLeaf`, `TestLoad_EnvKeyMapping`, `TestLoad_NullMeansMeasure` |
| V2-SP01-10 | Config: invalid leaf falls back, never crashes; unknown key warns | `go test -run 'TestLoad_InvalidLeafFallsBackNotCrash\|TestLoad_UnknownKeyWarnsNeverErrors\|TestValidate_' ./internal/config/` | green; `TestValidate_RuleTableIsComplete` proves every leaf has a validation decision |
| V2-SP01-11 | Config schema + generated docs never drift | `go run ./tools/devtool gen-config-docs --check && git diff --exit-code -- docs/config-reference.md` | exit 0, clean |
| V2-SP01-12 | Logging `Loud` channel: three destinations | `go test -run 'TestLoud_ThreeDestinations\|TestLogger_' ./internal/logging/` | green |
| V2-SP01-13 | `obs`: log-bucket histograms, budget IDs B-A…B-F | `go test ./internal/obs/...` | green, incl. `TestBudgets_AllSixPresentAndConfigDriven`, `TestHistogram_PercentileConservative`, `TestCheckBudgets_CountsConsecutiveWindows` |
| V2-SP01-14 | `hookio`: all seven payloads, unknown-field tolerance | `go test ./internal/hookio/...` | green, incl. `TestReadEvent_AllSevenHookPayloads`, `TestReadEvent_UnknownFieldsPreserved`, `TestReadEvent_MissingFieldsNeverPanic` |
| V2-SP01-15 | **Hooks always exit 0** (30 fault combinations) | `go test -run TestDispatch_HookAlwaysExitsZero ./internal/cli/` | green — 30/30 exit 0, stdout always parses as `hookio.Output` |
| V2-SP01-16 | Plugin bundle generated, never hand-edited | `go run ./tools/devtool plugin-validate && git diff --exit-code -- plugin/` | exit 0, clean; 6 hooks + 7 commands present |
| V2-SP01-17 | Six-target cross build | `go run ./tools/devtool build-all` | all six `dist/qompack-<os>-<arch>[.exe]` produced |
| V2-SP01-18 | Conformance suites still enforce Rule W-1 for **unimplemented** packages | `go test ./internal/... 2>&1 \| grep -c 'behaviour: implementation is a stub (Rule W-1)'` | > 0, and **every** remaining skip in the tree carries that exact message or `contract fixture not yet recorded (Rule W-2)`. See V2-SP01-19 for which packages may still skip. |
| V2-SP01-19 | Rule W-1 skips are gone from every wave-0/1 package | `go run ./tools/devtool lint` (`stubskips` sub-check) ; `grep -rn "t.Skip" internal/sketch internal/chunk internal/canon internal/symbols internal/store internal/redact internal/tokens internal/dag internal/ipc internal/contract internal/eval` | **no output** from the grep. Remaining legal skips exist only in `analyzertest schedulertest checkpointtest pinstest rehydratetest rulestest skillstest mcptest negknowtest grammartest observertest` |
| V2-SP01-20 | Build-order guards (closing note 1–4) | `go test ./test/guards/...` | green: `TestGuard_Phase0BeforeStore`, `TestGuard_StoreAndNegknowBeforeCheckpoint`, `TestGuard_SubmodularInertWithoutPSelection`, `TestGuard_SelectorRefusesWithoutPSelection`, `TestGuard_O1FlagDefaults`, `TestGuard_FreshBuildReportsModeFull` |
| V2-SP01-21 | No network, no telemetry, write set confined | `go test -run 'TestGuard_NoNetworkImports\|TestGuard_WriteSetConfinedToQompack' ./test/guards/` | green — `net/http`, `net/url`, `crypto/tls` absent; `net` only in `internal/ipc` |
| V2-SP01-22 | e2e: all six hooks against the real binary | `go test -run TestE2E_AllSixHooksExitZero ./test/e2e/` | green — six exit codes 0, six hook-log lines |
| V2-SP01-23 | `tokens` baseline behaviour preserved after SP-06's edit | `go test -run 'TestClassify_Table\|TestEstimate_ImageFromDimensions\|TestEstimate_ImageCappedAt1600\|TestEstimate_PDFPageCount\|TestEstimateRoot_SumsChunks\|TestCalibrate_ClampsAndPersists' ./internal/tokens/` | all six SP-01 tests still pass **unmodified**. ⚠ **The row's parenthetical is out of date: SP-06 rewrote *two* SP-01 tests, not one.** Besides `TestEstimate_ProseVsCode` (allowed, and now in `exact_test.go`), it replaced `TestEstimate_ImageDecodeFailureFallsBackToByteLength` with `TestEstimate_ImageDecodeFailureFallsBackToUnitScanner` — forced by SP-06's own spec ("Unparseable image → `(0, false)`, and `Estimate` falls back to the unit scanner"), where SP-01 flat-rated an undecodable image at `imageFallbackBytesPerToken = 1000`, making 2 500 bytes of opaque content cost **3 tokens** — the flat-rating `Qompack.md` §2.2 indicts and G10.2 exists to close. **Accept the second rewrite and record it**; the six above are the ones that must be untouched, and they are |
| V2-SP01-24 | Pure functions SP-01 implemented rather than stubbed | `go test -run 'TestRootHash_Formula\|TestYoungDaly_Formula\|TestSkiRental_ComputedNotLiteral\|TestDescriptorKey_Stable\|TestStripInjections\|TestTombstone_MatchesDesignExample' ./internal/...` | green; `grep -R "12\.5" internal/ --include=*.go` outside `_test.go` returns nothing |
| V2-SP01-25 | SP-01 benchmark budgets still met | `go test -bench 'BenchmarkHistogram_Observe\|BenchmarkConfigLoad_ColdNoFiles\|BenchmarkPathsWriteAtomic_4KB' -benchmem ./internal/obs ./internal/config ./internal/paths` | < 100 ns/op, < 2 ms/op, < 2 ms/op respectively |
| V2-SP01-26 | Commit-message policy machinery | `go test -run TestCheckCommitMsg ./tools/devtool/` | green — rejects `Co-Authored-By`, `🤖`, over-length subjects, missing `Refs:` |

### 2.2 SP-02 — replay harness, Belady OPT, Phase-0 baseline (`internal/eval`, `test/replay`)

#### 2.2a Inbound from SP-02 — read before executing the table

> **`plans/V2-SP02-handoff.md` is the long form of this section**, written by the SP-02 session at the tip of `feat/sp02-replay-harness-belady-baseline` against the branch it actually shipped. Every claim in it was checked by running the command; nothing is inferred. This section reproduces it so the checkpoint is self-contained, and the rows in the table below have been corrected in place.

§2.2 was written before `internal/eval` and `test/replay` existed, and **six of its twenty rows do not match what shipped**. Two of them *pass without testing anything* — they exit 0 having run nothing — and one would delete working code if followed literally.

**① V2-SP02-15 passed vacuously: the `--` separator voided every flag.** `devtool replay` forwards its arguments verbatim, so the driver received `["--", "--corpus", …]`. Go's `flag` stops at a bare `--`, and every flag after it was discarded — the driver replayed the **default** corpus, **without** `--ci`, **without** `--phase 0`, and exited 0 with stdout byte-identical to a real run. **Fixed in the driver, not in the row:** unconsumed arguments are now refused with exit 2 (`replay: unexpected argument "--corpus": replay takes flags only, and a bare "--" stops flag parsing…`), covered by `TestReplayDriver_LeftoverArgumentsAreBadInput`. The row below has had the `--` removed.

**② V2-SP02-17 passed vacuously: `-run TestImports` matched no test.** `go test -run TestImports ./internal/eval/` prints `ok … [no tests to run]` and exits 0. The test is `TestImportGraph_EvalIsFoundationOnly`. The row below now runs `-run TestImportGraph_`.

**③ V2-SP02-12 would damage the code if followed literally.** `grep -rn "t.Skip" internal/eval` returns three hits and **all three must stay**: `synth_test.go:333` (the `QOMPACK_EVAL_WRITE_CORPUS=1` guard that stops CI rewriting the corpus it is measuring against), `evaltest/suite.go:102` (Rule W-1's skip machinery itself — dormant because `eval` is real, but it is the mechanism, and deleting it deletes SP-01's conformance-suite contract), and `evaltest/suite.go:97` (a comment describing the line above). **This is the same defect SP-07 reports as V2-SP07-16.** Two independently written sections carry it, so assume §2.3–§2.6 do too, and check every `grep … t.Skip` row in this document against `00-ARCHITECTURE.md` D9/§5.22 before satisfying it. The correct observable is `go run ./tools/devtool lint --only=stubskips`.

**④ Four rows name tests that do not exist under those names.** The behaviours are covered; only the names drifted. `go test ./test/replay/...` and `go test ./internal/eval/...` are green.

| row | plan says | actual |
|---|---|---|
| V2-SP02-13 | "all 19 `TestGate_*` rows" | **17** `TestGate_*`, plus 3 `TestPhase0_*`, plus 13 `TestReplayDriver_*` — 36 tests in `test/replay` |
| V2-SP02-13 | `TestGate_Phase0ExitCriterion` | `TestPhase0_SessionCountFloor`, `TestPhase0_RequiresStock`, `TestPhase0_Reproducibility` |
| V2-SP02-13 | `TestGate_PhaseChecksMayNotBeDisabledInCI` | `TestReplayDriver_PhaseChecksMayNotBeDisabledInCI` |
| V2-SP02-06 | `TestReplay_OracleFewerRepairsThanStock` | `TestReplay_OracleNeedsNoRepairsWhenItFits` |
| V2-SP02-14 | `--to <path>` | **no such flag.** Use `--write-baseline --baseline <path>`; `--out` is the *report*, not the baseline |

Rows **02, 03, 04, 05, 07, 08, 09, 10, 11, 16, 18, 19, 20** are correct as written and pass. `-race` works on the Windows toolchain here, so V2-SP02-01 is fine.

**⑤ Two things this checkpoint must *do*, not just check.**

- **`landedSubplans` must gain SP-03 … SP-07.** `tools/devtool/cover.go` skips a package's §6.4 floor until its owner appears in that set. Before SP-02 the gate was `o.Owner != "SP-01"`, so a landed SP-02 would have shipped with **no floor at all** — `internal/eval` printed `exempt (stub, owned by SP-02)` and its 90.9 % was never compared to 85 %. The same hole is open for SP-03 … SP-07. `TestLandedSubplansMatchesTheBranch` fires when SP-03 lands, which forces someone to look, but **its assertion message still says "SP-03 has not landed", which reads backwards at that moment — rewrite it once the whole wave is in.** Inferring "landed" from the OWNERS.tsv probe was tried and is unsound: `probeStillStub` only recognises the `core.ErrNotImplemented` shape, but `chunk.Split` returns `nil` and `grammar.Append` returns nothing, so it gates code nobody has written and exempts code that shipped. The explicit set is deliberate. **This is V2-MERGE-14** — and note SP-04 rewrote the same function differently on its own branch, so this is a conflict resolution, not a one-line addition.
- **`replay-gate` must become a *required* check.** `ci.yml` no longer carries `continue-on-error` on `replay-gate`, but "required" is a **branch-protection setting on `develop`** that cannot be set from the working tree. Until it is set, a red gate does not block a merge. `bench-gate` is SP-05's half — see V2-MERGE-04.

**⑥ Standing facts about the Phase-0 numbers, so nobody reads a designed property as a defect.**

- **`stock` and `null` report identical `rewrite_tokens` (16 891 126) and `pause_p95` (116 198).** By construction: a Full Compact rewrites the whole message array, so `p_min = 0` for both and §5.2's rewrite cost `w·(n − p_min)` collapses to `w·n`. Documented at `internal/eval/policy.go:259`. Consequence: **the 2 % rule cannot distinguish `stock` from `null` on those two metrics** until a partial-compaction policy exists. The primary metric separates them cleanly — `fraction_of_opt` 0.695164 vs 0.000000.
- **All latency is modelled**, tagged `"latency": "modelled"` on every number. Changing `DefaultLatencyModel()` moves `pause_p95` by far more than 2 % and will read as a regression. It is not one — it is an instrument change, and the correct response is re-baselining per **ADR 0003**, *not* a `Sign-off:` trailer. The sign-off exists for deliberate trade-offs within one instrument.
- **`retrieval_hit_rate` is 0.0 for all three policies, with `retrieval_actions: 0`.** No policy retrieves until SP-13. The driver prints a note beside it saying the zero is an absence of calls rather than a failure. Inert, not broken.
- **Corpus staleness clock.** `CORPUS.json` carries `regeneratedAfterPhase: 0` and the gate fails at `--phase > 2`. Phase 3 requires regeneration — protocol in ADR 0003.
- **The recorded tier is empty.** §6.3 tier 2 (recorded sessions) gates releases. The importer exists and is tested, but no recorded corpus has ever been collected, so release-gating on recorded sessions is **unproven**. Recorded transcripts are never committed, so SP-02 could not fix this. Carry it forward.
- **Two SP-01 stub packages sat at 0.0 % coverage** when SP-02 wired the floor — `internal/canon` and `internal/symbols`. That was SP-04's starting point, not a wave-1 result.

**⑦ Where SP-02 went outside its declared file list** (expect these under V2-MERGE-10, each mechanically necessary): `tools/devtool/replay.go` (SP-01 shipped the registered task here; modified in place rather than duplicated), `tools/devtool/importrules.go` (`test/replay` must be a composition root or `lint --only=importgraph` fails), `tools/devtool/cover.go` (see ⑤), `internal/cli/commands.go` (one line registering `evalCmds()`), `test/guards/stubs_test.go` and `test/guards/v1_integration_test.go` (Rule W-1 flips), `plans/OWNERS.tsv` (header comment only, describing the new `cover` rule; no row changed).

**Not on SP-02's own list, and found by this checkpoint:** SP-02 also added `testdata/golden/contracts/store/stats-growth.json` and `testdata/golden/contracts/negknow/health.json` — two fixture files inside directories owned by **SP-06** and **SP-09**, declared in neither package's `MANIFEST.json`, and read by `internal/eval/growth_test.go` and `test/replay/growth.go`. **This is V2-MERGE-18.**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP02-01 | Whole package green | `go test -race ./internal/eval/...` | exit 0 |
| V2-SP02-02 | Block/demand derivation (`Blocks`, `Demands`, `approachClass`) | `go test -run 'TestBlocks_\|TestDemands_\|TestApproachClass_' ./internal/eval/` | green, incl. `TestBlocks_PositionsAreCumulative`, `TestDemands_OnlyPreCompactionBlocks`, `TestDemands_EliminationMatchByApproachClass` |
| V2-SP02-03 | **Belady OPT** (§6.10) — knapsack DP, determinism, budget respect, p_min | `go test -run TestBelady_ ./internal/eval/` | green, incl. `TestBelady_UnitWeightsMatchesClassicBelady`, `TestBelady_KnapsackBeatsGreedyDensity`, `TestBelady_BudgetNeverExceeded`, `TestBelady_PMinIsEarliestDropped`, `TestBelady_FallbackWhenDPTooLarge` |
| V2-SP02-04 | Breakpoint OPT (§5.6), permanently disclaimed | `go test -run TestBreakpointOPT_ ./internal/eval/` | green; `Plan.Note == NotPluginActionable` on every path |
| V2-SP02-05 | Policy registry + the three built-ins | `go test -run 'TestStockPolicy_\|TestNullPolicy_\|TestRegisterPolicy_\|TestPolicyNames_' ./internal/eval/` | green; `PolicyNames() == ["null","oracle","stock"]` |
| V2-SP02-06 | Counterfactual replay, deterministic mode | `go test -run TestReplay_ ./internal/eval/` | green, incl. `TestReplay_DeterministicAcrossRuns`, **`TestReplay_OracleNeedsNoRepairsWhenItFits`** (the plan's `TestReplay_OracleFewerRepairsThanStock` does not exist — §2.2a ④), `TestReplay_LiveModeRefusedWithoutEnv`, `TestReplay_HorizonRespected` |
| V2-SP02-07 | Divergence metrics — all five §4.2 bullets | `go test -run TestCompare_ ./internal/eval/` | green, incl. `TestCompare_IdenticalRuns`, `TestCompare_FirstDivergenceIsRelativeToCompaction`, `TestCompare_JaccardHalf`, `TestCompare_EditDistanceKnown` |
| V2-SP02-08 | Fraction-of-OPT scoring + every §11.2 secondary metric | `go test -run 'TestScoreRun_\|TestMetricsOf_\|TestReport_' ./internal/eval/` | green, incl. `TestScoreRun_FractionIsMicroAveraged`, `TestScoreRun_RewriteTokensSection52TableA/B`, `TestScoreRun_NoHardcodedMultiplier`, `TestMetricsOf_CoversEveryDirection` |
| V2-SP02-09 | Deterministic synthesizer + the committed 24-session corpus | `go test -run 'TestSynthesize_\|TestCorpus_' ./internal/eval/` | green, incl. `TestSynthesize_MatchesCommittedCorpus` (byte-identical), `TestSynthesize_EveryCompactionHasDemands`, `TestCorpus_ManifestHashesMatch` |
| V2-SP02-10 | Redacting importer for recorded corpora | `go test -run 'TestImport_\|TestRedact_\|TestImportCommand_' ./internal/eval/` | green; `TestImport_RefusesDestinationInsideRepo` and `TestImportCommand_NoRedactRequiresEnv` pass |
| V2-SP02-11 | Sublinear-growth checker (§11.3) | `go test -run TestCheckSublinearGrowth_ ./internal/eval/` | green — `Sublinear==true` at α≈0.62, false at α≈1.0, `Reason` set when inconclusive |
| V2-SP02-12 | `evaltest` conformance suite, zero **behaviour** skips | `go test -run 'Suite' ./internal/eval/... -v` ; `go run ./tools/devtool lint --only=stubskips` ; `go test -run TestRunHarnessSuite_AgainstEvalNew ./internal/eval/...` | Suite green with every `/behaviour` subtest reporting RUN and PASS rather than SKIP; `stubskips` lists **no** skips for `internal/eval/evaltest` and reports `internal/eval`'s single skip as `platform-gated`. **The plan's `grep -rn "t.Skip" internal/eval` returns nothing is wrong and must not be satisfied** — it returns three hits, all of which must stay, and deleting them deletes Rule W-1's machinery and the corpus write-guard. See §2.2a ③; the identical defect is V2-SP07-16 |
| V2-SP02-13 | **Replay gate driver** — 2 % rule, sign-off trailer, phase assertions | `go test ./test/replay/... -v` | green: **36 tests — 17 `TestGate_*`, 3 `TestPhase0_*`, 13 `TestReplayDriver_*`** (the plan's "19 `TestGate_*`" is wrong; §2.2a ④). Incl. `TestGate_TwoPercentBoundaryExclusive`, `TestGate_SignOffAllowsNamedMetricOnly`, `TestGate_BloomFPCeiling`, `TestGate_GrowthInconclusiveFails`, **`TestPhase0_SessionCountFloor` / `TestPhase0_RequiresStock` / `TestPhase0_Reproducibility`** (for the plan's `TestGate_Phase0ExitCriterion`) and **`TestReplayDriver_PhaseChecksMayNotBeDisabledInCI`** (for the plan's `TestGate_…`) |
| V2-SP02-14 | **Phase-0 baseline is reproducible** | `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline --baseline <scratch>/out-a.json` then again with `<scratch>/out-b.json`; compare | byte-identical; both equal the committed `testdata/baseline/phase0.json`. **There is no `--to` flag** — the plan's spelling is wrong (§2.2a ④). `--out` is the *report*, not the baseline. Write both outputs into the scratch dir, never over the committed baseline |
| V2-SP02-15 | Replay gate runs end to end against the corpus | `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline testdata/baseline/phase0.json --phase 0 --ci` | exit 0; report JSON parses; `policies.oracle.fraction_of_opt == 1.0`, `policies.null.fraction_of_opt == 0.0`, `policies.stock.fraction_of_opt` strictly between them on all 24 sessions. **The `--` in the plan's command voided every flag after it** and made this row exit 0 having replayed the default corpus with no phase check and no `--ci` (§2.2a ①). The driver now refuses leftover arguments with exit 2; re-running the *old* command and seeing exit 2 is the positive control that the fix is present |
| V2-SP02-16 | Honesty tags in the report | inspect the report JSON from V2-SP02-15 | every latency value tagged `"latency":"modelled"`; breakpoint plan carries `NotPluginActionable`; `retrieval_hit_rate: 0.0` accompanied by `retrieval_actions: 0`; `forfeited_discount_tokens` reported separately from `rewrite_tokens`; `corpusTier: "synthetic"` |
| V2-SP02-17 | `internal/eval` import purity | `go test -run TestImportGraph_ ./internal/eval/` (`importgraph_test.go`) | `internal/eval` imports no `internal/` package outside `{core, paths, config, logging, obs}`. **The plan's `-run TestImports` matches nothing and prints `ok … [no tests to run]`, exit 0** — the test is `TestImportGraph_EvalIsFoundationOnly` (§2.2a ②). Generalise the lesson: `go test -run` prints `ok` when its pattern matches nothing, so **any** row in this document whose pattern has drifted from the test names is a silent pass. SP-07 checked all of its own patterns against `go test -list` for this reason; the other groups' have not been |
| V2-SP02-18 | Fuzz target | `go test -run=XXX -fuzz FuzzRedact -fuzztime 60s ./internal/eval/` | zero crashers |
| V2-SP02-19 | SP-02 benchmark budgets E-1…E-5 | `go test -bench 'Belady\|Breakpoint\|Synthesize\|Compare' -benchmem ./internal/eval/` | E-2 ≤ 250 ms/op, E-3 ≤ 50 ms/op, E-4 ≤ 20 ms/op, E-5 ≤ 15 ms/op; E-1 (full corpus replay) < 120 s |
| V2-SP02-20 | Coverage floor | `go run ./tools/devtool cover` **and** `go test -cover -count=1 ./internal/eval/...` | `internal/eval` ≥ **85 %** (it measured 90.9 % at SP-02's tip). Read the second command's number as well: this row is only non-vacuous once V2-MERGE-14 has landed and `SP-02` is in `landedSubplans` in the *merged* `cover.go` |

### 2.3 SP-03 — sketch library (`internal/sketch`)

> **Read §2.3a below before executing this table.** Four rows (V2-SP03-11, -12, -13, -20) have been corrected in place against what the branch actually shipped, and §3.3's measurement procedure changed with them. The one that matters most: **`TestMinHash_OneNewFailure` passed throughout a defect that broke near-duplicate detection across the whole 8–66 KB range**, so a green result from it alone is not evidence about MinHash.

#### 2.3a Inbound from SP-03 — items its branch could not close, and divergences you must not revert

Written by the SP-03 session at the tip of `feat/sp03-sketch-library`, against the branch it actually shipped rather than against its plan. Items 1–3 are **work this checkpoint owns**; items 4–7 are things a verifier will otherwise "correct" back into defects. Every claim below was reproduced, not inferred; where a number appears, it was measured on the branch tip.

**1. `internal/sketch`'s MinHash sampler was rewritten, and SP-03's plan text is now wrong about it.** A whole-branch review found that the plan's sampling rule (`plans/V2-SP-03-sketch-library.md` line 918: rate `= ⌈nsh / MinHashSampleTarget⌉`, keep `hash < MaxUint64/rate`) derives the keep-threshold from **document length**, so two near-identical documents whose shingle counts straddle a multiple of 8 192 are sampled at densities differing by a whole integer factor and their permutation minima then agree with probability ≈ 1/rate however similar they are. Reproduced on documents differing by 150 bytes with a true Jaccard of 0.98: **estimated 0.4062, `IsNearDup(0.9)` false**, recovering to 0.99 once both sat on the same side. It recurs at every boundary — ~8, 16, 25, 33, 41, 49, 57, 66 KB — i.e. the whole size range of an ordinary tool result, and it lands on *growing* documents, which is the one input `Qompack.md` §8.1 item 1 exists to detect.

The sampler now keeps the `MinHashSampleTarget` **smallest distinct shingle hashes** (bottom-k), so the effective threshold moves continuously with the document. `Signature`, its compact wire form and `Jaccard`'s body are unchanged — 00-ARCHITECTURE §5.7's shape is untouched — and every frozen fixture is byte-identical because the 4 096-byte golden document sits below the target and runs no selection at all. ADR 0030 §9 is the record. **Do not reconcile the code back to the plan's line 918.** Correct the plan instead; the ruling is that `Qompack.md` specifies MinHash, 128 permutations and `nearDupThreshold` 0.9 but never specifies a sampler, so the sampler was the subplan's invention and the spec's behavioural requirement is what governs.

**2. The §6.4 coverage floors are enforced for SP-01's packages only, and are a silently dead gate for every package wave 1 landed.** `tools/devtool/cover.go:39` skips any `plans/OWNERS.tsv` row whose owner is not `SP-01`, printing `exempt (stub, owned by SP-NN): <pkg>` and exiting 0 — so `sketch`, `chunk`, `canon`, `symbols`, `store`, `redact`, `tokens`, `dag`, `ipc`, `daemon`, `contract` and `eval` all have declared floors that nothing checks, at any coverage down to zero. The same `Owner != "SP-01"` skip also disables `probeStillStub` for them, which is the check V2-MERGE-12 relies on. OWNERS.tsv has no way to express "this owner has landed", so closing this needs a column or an equivalent in `cover.go` — a devtool change, which is why SP-03 could not make it. **This is a §2.0-class defect: a gate that stopped guarding without saying so.** Fix it here and re-run every wave-1 package's floor.

**3. `benchstat` is the gate for `testdata/bench-baseline.txt` (00-ARCHITECTURE §7) and nothing ever runs it.** SP-03 reported this as "it is in no `go.mod`, no `tools/`, no CI step" and installed it by hand (`go install golang.org/x/perf/cmd/benchstat@latest`) to produce its rows. **Checked against `develop`, the first half is wrong and the conclusion is right, in a worse way:** `benchstat` *is* pinned — `tools/pinned/go.mod` requires it and `tools/pinned/tools.go` blank-imports it — and `tools/devtool/util.go:26` declares `benchstatPkg = "golang.org/x/perf/cmd/benchstat"`. **That constant has no callers.** No `devtool` task invokes benchstat, and the `bench-gate` CI job runs `bench-hotpath` only. So §7's ">10 % warn / >25 % fail" rule has **no executable form anywhere in the repository** — it exists as a pinned dependency, a dead constant, and a manual command in §5.3 of this document. That is worse than a missing tool, because the pin and the constant both read as though the gate is wired. Add a `devtool bench-compare` task (or a CI step) that actually calls it, and make `bench-gate` fail on the >25 % condition.

**4. SP-03 shipped nine commits, not the eight its Done checklist names.** The ninth is the final-review fix round (`fix(sketch): length-invariant MinHash sampling and decode guards`). It was deliberately **not** back-dated into the commits that own the code, because doing so would have made commit 5 read as though it always had the correct sampler and erased the fact that a review caught it. Squash if the letter of the checklist matters; the reasoning is recorded here so the count is not reported as an unexplained anomaly under V2-MERGE-10.

**5. SP-03 touched seven files outside its allowlist, each under a recorded ruling.** Expect these under V2-MERGE-10 and treat them as accounted for: `test/guards/stubs_test.go` and `test/guards/v1_integration_test.go` (guards whose purpose is to go red when a stub becomes real — leaving them red takes CI down); `internal/testutil/fixtures_test.go` (frozen-fixture count 23 → 28); `.gitignore` (`**/testdata/rapid/`, deliberately unrooted because six packages now use rapid and it writes beside whichever failed); `internal/core/hash.go` + `hash_test.go`; `internal/paths/appendonly.go` + `appendonly_test.go`. **The last two pairs are cross-branch conflict candidates** — `internal/core` is consumed by every wave-1 branch — so resolve them by inspection, not by `--ours`/`--theirs`:

- `core.HashBytes` ended `copy(out[:], h.Sum(nil))`, where `h.Sum(nil)` heap-allocates the 32-byte digest: one allocation per hash, three per `PostToolUse`, **5 000 per Bloom rebuild**. It is now `h.Sum(out[:0])` — byte-identical digest, verified against nil, empty and 300 input lengths, guarded by `TestHashBytes_DoesNotAllocate`. Every `core.HashBytes` caller in the tree benefits; `BenchmarkRebuildBloom5000` went 5 004 → 4 allocs/op. If another branch's version of this function wins the merge, the L0 hot path silently regresses to 3 allocs and only that test will say so.
- `internal/paths/appendonly.go` gains an exported `HighestBloomBackupSeq`, sharing the existing `bloomBackupSeq` parser with `pruneBloomBackups` rather than adding a second one. It is exported because **SP-09 otherwise has no way to re-derive its rebuild counter after a restart** from what is on disk.

**6. Divergences from SP-03's plan text that are deliberate — reconcile the plan, not the code.** Beyond item 1: `ReplaceGenerational` delegates to `paths.ReplaceBloom` because the plan's own body calls `paths.WriteAtomic` on a path `paths.IsProtected` refuses outright, so the plan's code cannot run; the backup name is consequently `tried.bloom.<seq>.bak` with `%d`, not the plan's `%04d`, because `pruneBloomBackups` parses that family. `MisraGries.Add` saturates rather than using the plan's plain arithmetic. `Load`'s silence is narrower than the plan claims — see V2-SP03-12.

**7. Two defects in SP-03's plan document itself, found while verifying the code against it.** Neither is an implementation fault and both will otherwise be rediscovered: (a) the plan's Produces block lists five `sketchtest` suite signatures that do not match the tree and **never did** — every one ships with a `name string` second parameter and `RunMinHashSuite` takes a plain `func([]byte, MinHashOptions) Signature`; `git show 7340536:internal/sketch/sketchtest/*.go` confirms SP-01 shipped them that way and SP-03 did not touch them, so V2-MERGE-08 should compare against 00-ARCHITECTURE §5.7 and the tree, not against that block. (b) The plan's §11.6 forbidden-literal list (line 138) omits `8000`, which `tools/lint/nomagic/literals.go:14` does forbid; `internal/sketch/doc.go` lists it correctly.

**Carried forward, not blocking.** `sketchtest.RunCMSSuite`'s one-sided estimate case cannot distinguish a min-estimator from a max-estimator (a max-estimator satisfies the stated `Estimate ≥ true` guarantee), and the accuracy backstop `TestCMS_ErrorBoundHolds` lives in `internal/sketch` rather than in the reusable suite — so SP-16's warm-started CMS, held only to the suite, would inherit the safety property and not the ε·N accuracy one; a real discriminator has to force collisions via `Dims()`-derived load. `BenchmarkMinHash100KiB` is the tightest row on the branch and its 2.5 ms budget is a Windows-laptop number worth re-deriving on CI hardware under §5. `MinHash`'s selection uses a median-of-three quickselect over a 16 384-value buffer keyed by the unkeyed, invertible `fnv1a64`, so crafted input could in principle force quadratic behaviour — not exploitable in this threat model (the user's own tool output, on B-C, soft) and `fnv1a64` cannot be keyed because signatures persist in an append-only index, but worth recording once. `sketch.HeaderMagic` is an exported mutable `[4]byte`; a `const HeaderMagicString = "QPKS"` with the var derived from it would give the load-bearing value an immutable definition.

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP03-01 | Whole package green under race, twice | `go test -race -count=2 ./internal/sketch/...` | exit 0 |
| V2-SP03-02 | QPKS header: framing, CRC32C, version, param ordering | `go test -run 'TestHeader_\|TestHash128_' ./internal/sketch/` | green, incl. `TestHeader_FrameLayout`, `TestHeader_DetectsSingleBitFlip`, `TestHeader_RejectUnsortedParams`, `TestHeader_EncodeRejectsBadParamName` |
| V2-SP03-03 | **Bloom Appendix-A sizing** | `go test -run TestBloom_AppendixASizing ./internal/sketch/ -v` | `mRaw = 95_851`, `mBits = 95_872`, `k = 7`, body 11 984 bytes |
| V2-SP03-04 | Bloom: no false negatives, measured FP rate at capacity | `go test -run 'TestBloom_NoFalseNegatives\|TestBloom_EstimatedFPRateMatchesEmpirical' ./internal/sketch/ -v` | 10 000/10 000 members test true; empirical FP ∈ [0.008, 0.013]; `EstimatedFPRate()` ∈ [0.009, 0.012] |
| V2-SP03-05 | Bloom saturation + resize (§11.4, §12) | `go test -run 'TestBloom_Resize\|TestBloom_Saturated\|TestBloom_Stats' ./internal/sketch/` | `ResizeTarget` returns `(2×cap, fp, true)` above 0.5 fill and `(cap, fp, false)` below; `Saturated()` fires at `EstimatedFPRate ≥ 0.10`; cap respected at `MaxBloomCapacity` |
| V2-SP03-06 | `RebuildBloom` from an arbitrary `iter.Seq` at a different capacity | `go test -run 'TestBloom_Rebuild' ./internal/sketch/` ; `go test -bench BenchmarkRebuildBloom5000 ./internal/sketch/` | equivalent filter reconstructed; ≤ 15 ms for 5 000 keys |
| V2-SP03-07 | **CMS Appendix-A sizing** + overestimate-only guarantee | `go test -run 'TestCMS_AppendixASizing\|TestCMS_EstimateNeverUnderestimates\|TestCMS_ErrorBoundHolds' ./internal/sketch/ -v` | `Dims() == (2719, 5)`, body 54 380 bytes; never underestimates; ≥ 99 % of keys within ε·N = 100 |
| V2-SP03-08 | CMS merge / scale / heavy hitters (O4 groundwork) | `go test -run 'TestCMS_MergeFrom\|TestCMS_Scale\|TestCMS_HeavyHitters' ./internal/sketch/` | merge additive and exact; `Scale` decays; `HeavyHitters(mg,3)` returns `[{a,500},{b,300},{c,100}]`; `ErrShapeMismatch` on mismatch and on nil |
| V2-SP03-09 | HLL sizing, error bounds, exact-union merge | `go test -run TestHLL_ ./internal/sketch/ -v` | 2 048 registers, 2 102-byte frame, ~2.3 % standard error; relative error ≤ 0.07 at n ∈ {1e3,1e4,1e5,1e6}; merged registers byte-identical to the union |
| V2-SP03-10 | Misra-Gries: no false positives, frequent-item guarantee, mergeable | `go test -run TestMG_ ./internal/sketch/` | green, incl. `TestMG_NoFalsePositives`, `TestMG_FrequentItemGuarantee`, `TestMG_DeterministicUnderMapOrder`, `TestMG_MergeFrom` |
| V2-SP03-11 | **MinHash** — the O2 near-dup signal | `go test -run TestMinHash_ ./internal/sketch/ -v` | green, incl. `TestMinHash_OneNewFailure` (Jaccard ≥ 0.9 for "same suite, one new failure"), `TestMinHash_ShiftInvariance` (≥ 0.9), `TestMinHash_DisjointInputs` (≤ 0.05), `TestMinHash_StableAcrossRuns` (frozen `Mins` constants). **Also required, and they are the rows that matter most:** `TestMinHash_StraddlingTheSampleTargetIsContinuous` and `TestMinHash_BottomKMatchesTheSlowDefinition`. See **Inbound from SP-03**, item 1 — `TestMinHash_OneNewFailure` alone passed throughout a defect that made near-dup detection fail across the whole 8–66 KB range, because its fixture happens to sit below the sampler's first boundary. A green `TestMinHash_OneNewFailure` is not evidence about MinHash; these two are |
| V2-SP03-12 | Persistence: atomic `Save`/`Load`, `LoadWithLog` is the loud path | `go test -run 'TestSave_\|TestLoad_\|TestLoadWithLog_\|TestQuarantine' ./internal/sketch/` | green; `LoadWithLog` emits exactly one `Loud` on corruption; corrupt ⇒ `errors.Is(err, core.ErrNotFound) && errors.Is(err, ErrCorrupt)`. **This row's "`Load` emits zero log records" was too strong and has been corrected:** `Load` hands `LoadWithLog` a `logging.Nop`, which writes no log *line* anywhere — but `logging.Nop().Loud` still appends to the process-wide `logging.LastLoud` ring and still fires observers installed via `logging.AttachLoudObserver`. Assert "no log line", not "no record". ADR 0030 §11 states the narrow truth; do not "fix" the code to match the old wording |
| V2-SP03-13 | **`tried.bloom` generational replacement** (§7.4 append-only) | `go test -run 'TestSave_RefusesTriedBloom\|TestReplaceGenerational_\|TestAppendOnly_TriedBloomNeverTruncated' ./internal/sketch/` | `Save` on `tried.bloom` ⇒ `ErrGenerational`, no file created; exactly one `.bak` generation kept; rollback restores the prior generation on write failure; `AssertAppendOnly` passes. **Changed by SP-03's final review:** a `seq` that does not strictly exceed the highest surviving `.bak` is now **refused before anything on disk moves** (`ErrMalformed`, naming both sequences), where it was previously allowed to proceed and reported afterwards. That ordering was a data-loss path — `paths.ReplaceBloom` renames the live file before it stages, so a low `seq` plus a staging failure left the store with **no `tried.bloom` at all**. Expect `TestReplaceGenerational_NonMonotonicSeqIsRefused` and `TestReplaceGenerational_RefusalProtectsAgainstAStagingFailure`; the old `..._IsReported` / `..._RollbackFailureIsReported` names are gone on purpose |
| V2-SP03-14 | Property suite (12 properties) | `go test -run TestProp_ ./internal/sketch/` | green, incl. `TestProp_BloomRebuildEquivalence`, `TestProp_CMSMergeAdditive`, `TestProp_HLLMergeIsRegisterMax`, `TestProp_MarshalIdempotent`, `TestProp_UnmarshalNeverPanics`, `TestProp_MinHashJaccardAccuracy` |
| V2-SP03-15 | Five fuzz targets | for each of `FuzzBloomUnmarshalBinary FuzzCMSUnmarshalBinary FuzzHLLUnmarshalBinary FuzzMisraGriesUnmarshalBinary FuzzSignatureUnmarshalBinary`: `go test -run=XXX -fuzz <T> -fuzztime 60s ./internal/sketch/` | zero crashers; every failure path returns a package sentinel |
| V2-SP03-16 | **Frozen on-disk format** (W-2 contract for SP-04/SP-06) | `go test -run TestGolden ./internal/sketch/` (**without** `-update`) | the five `testdata/golden/contracts/sketch/*.v1.bin` reproduce byte-for-byte; `MANIFEST.json` hashes match; `TestGolden_V1StillDecodes` passes |
| V2-SP03-17 | Import purity | `go test -run TestImports_FoundationOnly ./internal/sketch/` | imports exactly `{core, paths, logging}` |
| V2-SP03-18 | **L0 sketch-update budget** (the §8.1 item-5 contribution to B-A) | `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` and `go test -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` | **≤ 5 µs/op and 0 allocs/op** |
| V2-SP03-19 | Remaining sketch micro-budgets | `go test -bench . -benchmem ./internal/sketch/` | `Bloom.Add/Test`, `CMS.Add/Estimate`, `HLL.Add` ≤ 1.0 µs/op 0 allocs; `HLL.Cardinality` ≤ 25 µs; `MisraGries.Add` ≤ 5 µs; `MinHash` 4 KiB ≤ 1.5 ms, 100 KiB ≤ 2.5 ms; Bloom marshal/unmarshal ≤ 60 µs; CMS ≤ 250 µs |
| V2-SP03-20 | Coverage floor | `go run ./tools/devtool cover` **and** `go test -cover -count=1 ./internal/sketch/...` | `internal/sketch` ≥ **90 %** (it measured 96.5 % at SP-03's tip; `sketchtest` 99.5 %). **`devtool cover` does NOT check this today and will not fail if it is missed** — `tools/devtool/cover.go` enforces the §6.4 floors only for packages `plans/OWNERS.tsv` assigns to **SP-01**, so this row's command prints `exempt (stub, owned by SP-03): sketch` and exits 0 at any coverage whatsoever, including 0 %. That is a silently dead gate of exactly the class §2.0 exists to catch. Read the second command's number, and fix the gate — see **Inbound from SP-03**, item 2 |

### 2.4 SP-04 — chunking, canonicalization, symbols (`internal/chunk`, `internal/canon`, `internal/symbols`)

> **SP-04 shipped six defects it knowingly did not fix, and this checkpoint owns every one of them.** They are recorded as data in `plans/CARRIED-DEFECTS.tsv`, with a diagnosis and acceptance criteria per row in `plans/V2-SP-04-carried-defects.md`, and `test/guards/carrieddefects_test.go` refuses to let `plans/V2-report.md` exist while any row owned by `V2-VERIFY` is still `open`. **The full table and the resolution rules are at §4a of the completion report (§8) — read it before running this section, not while filling the report in**, because two of the six change what this section should expect and one (SP04-D4, the `landedSubplans` gap that is now V2-MERGE-14) will interrupt the merge itself.
>
> Two further notes specific to this group. **`testdata/bench-baseline.txt` carries no `chunk`, `canon` or `symbols` rows** — SP-04 committed none, so §5.3's >10 %/>25 % gate has never had a baseline for the three packages this section covers (see §2.0a). And **SP04-D6** records `BenchmarkRun_Bash100KB` at ±33 % on the reference host, which a 25 % gate cannot distinguish from a regression; establish that baseline from a quiet machine at `-count 10` before treating any canon benchmark as a miss.

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP04-01 | Three packages green under race | `go test -race ./internal/chunk ./internal/canon ./internal/symbols ./test/dedup` | exit 0 |
| V2-SP04-02 | FastCDC params: validation and deterministic clamping | `go test -run 'TestParams\|TestNewClampsInvalidParams' ./internal/chunk/` | green; the 8-case validate table and the 3-case normalize table match |
| V2-SP04-03 | **Chunk size bounds and contiguity** (§5.5 normative) | `go test -run 'TestSplit_SizeBounds\|TestSplit_Contiguity\|TestPropSizeBounds\|TestPropAllBytesCovered' ./internal/chunk/` | every non-last chunk in `[1024, 16384]`; `Σ Len == len(data)`; concatenation reproduces the input |
| V2-SP04-04 | **Cross-platform determinism** | `go test -run 'TestGearTableGolden\|TestSplit_GoldenBoundaries\|TestSplit_Determinism' ./internal/chunk/` (**no** `-update`) | goldens reproduce byte-for-byte on ubuntu, macos **and** windows in CI's `test` matrix. A per-platform golden is a checkpoint failure |
| V2-SP04-05 | **Boundary stability under insertion/deletion** (§5.5, §6.1) | `go test -run 'TestPropBoundaryStability_Insertion\|TestPropBoundaryStability_Deletion' -v ./internal/chunk/` | per trial: pre-`k` chunks byte-identical, a realignment index exists, ≤ 12 novel chunks; aggregate: ≤ 2 novel in ≥ 85 % of trials, ≤ 3 in ≥ 95 %. **Record the logged histogram** — it is a wave-1 baseline |
| V2-SP04-06 | Mean chunk size and Merkle root | `go test -run 'TestSplit_MeanChunkSize\|TestRootHash_\|TestRefs_RoundTrip\|TestChunkHashMatchesCoreHashBytes' ./internal/chunk/` | mean ∈ [3200, 5200]; `RootHash` domain-separated and order-sensitive; `Refs` produce `core.ChunkRef` |
| V2-SP04-07 | Streaming chunker equals in-memory chunker | `go test -run TestSplitStream_ ./internal/chunk/` | identical chunk sequence for readers of 1, 7, 4096 and full size across seven input sizes; callback and read errors propagate unwrapped |
| V2-SP04-08 | `FuzzSplit` | `go test -run=XXX -fuzz FuzzSplit -fuzztime 120s ./internal/chunk/` | zero crashers |
| V2-SP04-09 | FastCDC performance (§8.1 "well under 1 ms over 100 KB") | `go test -bench . -benchmem ./internal/chunk/` | `BenchmarkSplit_100KB` < 800 µs/op; `BenchmarkGearScan_1MiB` ≥ 400 MB/s; `BenchmarkSplit_1MiB` ≥ 120 MB/s and ≤ 2 allocs/op; `BenchmarkSplitStream_4MiB` ≤ 40 ms/op; `BenchmarkRootHash_1000Chunks` < 40 µs/op |
| V2-SP04-10 | Canon registry: order, duplicate rejection, strip gating | `go test -run 'TestRegistry_\|TestRun_\|TestKnownClassesCoverConfigDefaults\|TestParseClass_' ./internal/canon/` | `For("Bash","")` returns `[crlf ansi timestamps durations pids addresses tmpPaths bash testrunner git]` in that exact order, 100 repeats identical; `Names()` has the 14 registered names |
| V2-SP04-11 | Match composition: overlap resolution and the non-growing guard | `go test -run 'TestOverlapResolution_\|TestNonGrowingGuard_\|TestApplied_\|TestReduced_' ./internal/canon/` | longer-at-same-offset wins; earlier offset wins; rank breaks ties; growing matches silently dropped |
| V2-SP04-12 | **Normative canon properties** (§5.6) | `go test -run 'TestPropIdempotence_EveryCanonicalizer\|TestPropNonGrowing_EveryCanonicalizer\|TestPropRestoreIsExactInverse' ./internal/canon/` | `Run(Run(x)) == Run(x)`; `len(canonical) ≤ len(input)` always; `Restore(canonical, deltas) == input` whenever `KeepDeltas` |
| V2-SP04-13 | The seven generic canonicalizers | `go test -run 'TestCRLF_\|TestANSI_\|TestTimestamps_\|TestDurations_\|TestPIDs_\|TestAddresses_\|TestTmpPaths_' ./internal/canon/` | every table row matches; `pid=7` unchanged (short-match guard); `127.0.0.1:54123` → `127.0.0.1:<p>` |
| V2-SP04-14 | The seven per-tool canonicalizers | `go test -run 'TestBash_\|TestTestRunner_\|TestGrep_\|TestGlob_\|TestFileRead_\|TestWebFetch_\|TestGit_' ./internal/canon/` | every table row matches; `TestBash_ProgressGatedByStrip` and `TestBash_TrailingWhitespaceIsAlwaysOn` pin the class-gating contract |
| V2-SP04-15 | Every emitted `Match` carries a class (no silent discard) | `go test -run TestMatcherClassAssigned ./internal/canon/` | green over **every** corpus file; no zero `Class` |
| V2-SP04-16 | `Restore` error handling | `go test -run 'TestRestore_\|FuzzRestore' ./internal/canon/` ; `go test -run=XXX -fuzz FuzzRestore -fuzztime 120s ./internal/canon/` | `ErrDeltaRange` / `ErrDeltaOrder` as specified; zero fuzz crashers |
| V2-SP04-17 | Near-dup decision surface consumed by SP-06 | `go test -run 'TestDecide_\|TestSignature_OnlyWhenEnabled\|TestOptionsFrom_MapsAppendixC' ./internal/canon/` | all 6 `Decide` rows match; the golden `testdata/golden/contracts/canon/dedup-decisions.json` reproduces; `OptionsFrom` maps Appendix C (`permutations 128`, `nearDupThreshold 0.9`, the six strip classes) |
| V2-SP04-18 | Canon golden corpus | `go test -run TestGoldenCorpus_AllFiles ./internal/canon/` (**no** `-update`) | every one of the 24 `testdata/corpora/toolout/**` files reproduces its `testdata/golden/canon/**.canon.txt` byte-for-byte |
| V2-SP04-19 | **Dedup measurement harness — "with and without canonicalization"** (§10 Phase 1) | `go test -v ./test/dedup/` | `TestDedupRatio_WithVsWithout`: `testrunner` group `gain ≥ 1.25`; overall `gain ≥ 1.0`; `fileread` group `ratioWith > 1.0`. `TestDedupReport_Written` reproduces `testdata/canon-dedup-report.json` byte-for-byte |
| V2-SP04-20 | Canon performance | `go test -bench . ./internal/canon/` | `BenchmarkRun_Bash100KB` < 3 ms/op; `BenchmarkRun_GoTest` < 1 ms/op; `BenchmarkRestore_100KB` < 1 ms/op |
| V2-SP04-21 | Symbols: ten dialects, spans, caps | `go test -run TestExtract_ ./internal/symbols/` | green across Go, TS, Python, Rust, JVM, C-family, Ruby, Shell, PHP and the generic brace fallback; braces in strings/comments ignored; cap at 20 000 symbols; truncation at 4 MiB |
| V2-SP04-22 | Symbols: `Enclosing` = the §8.7 minimal sufficient span | `go test -run 'TestEnclosing_\|TestReferences_\|TestPropSpansWellFormed' ./internal/symbols/` | smallest containing span wins; out-of-range ⇒ `(Symbol{}, false)`; `References` respects word boundaries and zero-fills |
| V2-SP04-23 | `FuzzExtract` | `go test -run=XXX -fuzz FuzzExtract -fuzztime 120s ./internal/symbols/` | zero crashers; every span in bounds |
| V2-SP04-24 | Symbols performance | `go test -bench . ./internal/symbols/` | `BenchmarkExtract_100KB` < 2 ms/op; `BenchmarkEnclosing_100KB` < 2 ms/op; `BenchmarkReferences_100KB_50Names` < 1 ms/op |
| V2-SP04-25 | Conformance suites, zero skips | `go test -run 'TestCanonConformance\|TestSymbolsConformance' ./internal/canon ./internal/symbols` ; `grep -rn "t.Skip" internal/canon/canontest internal/symbols/symbolstest internal/chunk/chunktest` | suites green; grep returns nothing |
| V2-SP04-26 | Corpus hygiene | `grep -rniE 'AKIA\|ghp_\|sk-ant\|BEGIN [A-Z ]*PRIVATE KEY\|@gmail\.com' testdata/corpora/toolout/` | no output — no credential, key, token or real email in the committed corpus |
| V2-SP04-27 | Coverage floors | `go run ./tools/devtool cover` **and** `go test -cover -count=1 ./internal/chunk/ ./internal/canon/ ./internal/symbols/` | `internal/chunk` ≥ **90 %**, `internal/canon` ≥ **90 %**, `internal/symbols` ≥ **75 %**. Read the second command's numbers: `cover` is vacuous for all three until **V2-MERGE-14** lands (§2.7a A ②). SP-04 is the one subplan that added itself to `landedSubplans`, so its three packages are the *only* ones a naive merge might measure — which makes a green `cover` here especially misleading about the other eight |

### 2.5 SP-05 — daemon, IPC, hot path, contract monitor (`internal/ipc`, `internal/daemon`, `internal/contract`, `internal/cli`, `test/bench/hotpath`)

#### 2.5a Inbound from SP-05 — the rulings ledger is **git-ignored**; read §A before dispatching V-E

**A. The ledger exists, and a verifier will not see it.**

SP-02, SP-03, SP-04, SP-06 and SP-07 each left their reconciliation somewhere git tracks. **SP-05's is untracked and invisible:** `git diff develop..feat/sp05-daemon-ipc-and-hot-path -- plans/ docs/` is empty, but the real record — **30 numbered rulings**, seven per-task review files, seven review diffs and a final review — lives in

```
.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/progress.md   (+ review-task*.diff, final-review.md)
```

which is ignored by **`.superpowers/sdd/.gitignore`, a one-line `*` that sits inside the ignored tree itself**. That is the part that makes this dangerous rather than merely inconvenient: `grep -i superpowers .gitignore` at the repo root finds **nothing**, `git status` is clean, and the only way to discover the rule is `git check-ignore -v` on a path you already suspect. A fresh clone, a different worktree, or one `git clean -fdx` and thirty sanctioned decisions are gone — and a verifier will then report a dozen of them as defects.

> **Before dispatching V-E, do one of these.** Point it explicitly at the worktree `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05` (confirmed present, working tree clean, HEAD `b57df25`); or inline the ledger into V-E's prompt; or copy the ledger somewhere committed. **If this checkpoint runs anywhere other than that worktree, the third option is the only safe one** — and it should happen before `verify/v2` is cut, not after.

**B. Divergences most likely to be misread as bugs.** Every one is a recorded ruling, not a slip:

- **Commits 4 and 5 are swapped** — `contract` lands before daemon composition (`98c4fef feat(contract): …` precedes `30a04e2 feat(daemon): …`). The plan's stated order references symbols that do not exist yet; **it literally cannot compile.** Verified against the branch.
- **Commit subjects are not byte-identical to the plan's text** — they match the task briefs instead, trimmed to satisfy `tools/devtool/checkcommitmsg.go`'s `subjectRE`. Note what that limit actually is before calling a subject over-length: **64 characters of free text *after* `type(scope): `,** not 64 for the whole line. The seven subjects run 67–77 characters overall and 61–63 after the prefix — legal, and tighter than it looks.
- **The B-A bench gate does not use the plan's method (ruling #29).** "Wall-clock hook spawn minus a constant floor" was replaced by the daemon's TS-anchored `hook_controlled` estimate, because B-A is defined in `obs/budgets.go` as `main()` entry → exit, which **excludes process creation** — and subtracting a constant removes the floor's *location* but none of its *dispersion*, contaminating exactly the p99 the gate reads. Wall-clock survives as `B-A_spawn_estimate` with `limit_ms`/`pass` null. **Under the plan's original method this branch fails B-A on every platform, including bare metal.** Do not "restore" the plan's method to make the row match the document.
- **The ring-full spill was deleted (ruling #23)** even though the plan describes it: WAL-first ordering already makes every spilled line durable, and the spill rested on a byte-identity invariant that `hookio.Event.Extra` violates. The `l0_ring_full` counter is retained.
- **Five smaller ones:** there is **no `ipc.Router`** (confirmed: no such type on the branch); **`internal/obs` is untouched**, so the plan's `obs/budgets.go` section is historical; `SessionHistory` persists to **`state/history.json`**, which is a *new* file beside the Monitor's pre-existing `state/contract.json` — both exist and they are not the same artifact; counters use the **underscore** idiom, not the plan's dotted names; and `contract.History` stays an **interface**, with `SessionHistory` as its first concrete implementation.

**C. Code that has never executed anywhere.**

`internal/ipc/listen_unix.go` and `internal/ipc/server_unix_test.go` have **never run on any machine** — no WSL and no Docker on this host, so they were desk-verified against `unixsock_posix.go` semantics and compile-checked via `GOOS=linux`/`GOOS=darwin` only. **The ubuntu and macos CI legs will be their first-ever execution.** Specifically unexercised: `TestStaleUnixSocketReclaimed` (which was self-defeating — unlink-on-close contradicted its own premise; fixed with `SetUnlinkOnClose(false)`) and `TestListenReturnsErrAddrInUse`'s live-listener probe. **Treat a red POSIX leg here as expected-possible, not as a regression** — diagnose it as a first run, not as something the merge broke.

The bench gate has likewise **never run on a CI runner at all**, and its current warm-up shape (64-request hot tranche + `admin.ping` bulk, ruling #30) is newer still. `TestWindowsPipeACLRejectsOtherUser` **always skips** — it needs `QOMPACK_TEST_ACL=1` and a second Windows account; only the SDDL string-shape assertion runs today.

**D. The most dangerous latent trap — worth an explicit check.**

`contract.SessionHistory.LastSessionID` is **owned exclusively by `checkSessionStartFires`** (`internal/contract/assertions.go`; the only writes are at its lines 99/105/110). If any future daemon code writes it at session start, the `session_start.fires` assertion — **`SevCritical`** — is **permanently and silently disabled, and every test still passes.** Protection today is a doc comment plus one wedge test (`TestSessionStartFires_DaemonPreWriteOfLastSessionIDDoesNotWedgeFutureCounting`), nothing structural. Add a check that no package outside `internal/contract` writes that field, and carry the obligation into SP-08's checkpoint — L0 is exactly the code that will want to.

**E. Known-deferred. Do not re-report these as new findings.**

`go-winio v0.6.2`'s pipe-listener `Close` can hang 20 s+ racing a fresh `Accept` (observed 0–84 times per 100 iterations); bounded at our layer, but `TestServerCloseWithLiveConnection` **still flakes on Windows under load** — the real fix is a newer go-winio or a cancellable-`Accept` redesign. `paths.IsProtected` does not cover `spool/` (pre-existing; spool's append-only guarantee is structural, not access-controlled). `EnsureRunning`'s readiness contract is weak — `ipc.Probe` consumes the accept slot it uses as proof, which is why reply ops carry a **250 ms connect floor**. Drain cadence means a spooled line can wait until restart or an idle tick. Also: `hotPathSampleMaxAge` **discards** rather than clamps extreme samples; `connIdleTimeout` is unconfigurable; the FR-6 first-run window lets a config-disabled project spawn **exactly once** before `state.bin` exists; and async `Stop` can still be truncated by process exit one level up in `cli` (deferred to SP-17).

**F. Things that look like violations and are not.**

`contracttest` shows **4 SKIPs** — deliberate `*_StubIsSkipped` meta-tests, not Rule W-1 breaches (the same shape as §2.2a ③ and V2-SP07-16). `replay-gate` keeps its `continue-on-error` on this branch because **SP-02 owns removing it** — and `plans/V4-VERIFY-*.md:1152` will go on flagging it; leave it alone there. `bench-gate` is not a required check because that is a **GitHub branch-protection setting, not a repo file** — flip it only after the first three-platform green (same constraint as §2.2a ⑤). The lock file is `0o444`, forced by `paths.CreateNew`. **`QOMPACK_FAULT` appears in two non-test files** — `internal/cli/fault.go` and `internal/daemon/spawn.go`, which strips it from the child env — so a grep expecting exactly one will false-positive (both confirmed on the branch). `precompact.has_time_to_write` returns a `SevCritical` result under a `SevWarn` declared severity: documented design.

**G. What "green" currently means on this branch.**

All exit criteria pass, **but on Windows only for behaviour**. Coverage: `ipc` 84.2 %, `daemon` 82.0 %, `contract` 83.9 %, `cli` 81.5 %. Full suite and `-race` clean. Bench: **B-A 4.1 ms, B-B 0.7 ms, B-E 195 ms**. **POSIX is compile-verified only** (see §C). One item has **no dedicated test** by accepted adjudication: FR-4's `Serve`-failure arm, because a deterministic transport failure needs ipc-layer injection — a verifier can drive it directly with `server.Close()`, and that is the cheapest way to close the gap here.

**H. Four merge facts established by reading the branch against its siblings.**

**①** SP-05 removed `continue-on-error` from **`bench-gate` and not `replay-gate`**; SP-02's branch is the exact mirror. Neither is correct alone — **V2-MERGE-04**, and see §F for why that is right on each branch taken separately.

**②** SP-05 **did not touch the nightly fuzz matrix**, even though it owns `.github/`. `nightly.yml`'s eight rows are byte-identical on `develop` and on `feat/sp05-…`. `internal/ipc` ships `FuzzDecodeRequest`; the matrix asks for `FuzzFraming`. **The NDJSON framing decoder — the one component that parses bytes arriving from outside the process — has never been fuzzed by CI**, and because `.github/` is SP-05-exclusive, no other branch could have registered SP-03's, SP-04's or SP-06's targets either. That is why V2-MERGE-01 is a *merge* row: the constraint that produced it is structural.

**③** SP-05 raised `internal/testutil/fixtures_test.go`'s frozen count to **28** for five fixtures of its own (three `ipc`: `observe_tool`, `response_reply`, `state_degraded`; two `contract`: `history_degraded`, `transcript_with_sentinel`). SP-03 raised the same literal to 28 for five *different* fixtures. **The merged answer is 33** — V2-MERGE-15.

**④** SP-05 **re-indented the whole composition-root map** in `tools/devtool/importrules.go` when adding `test/bench/hotpath`, so its diff collides with SP-02's `test/replay` and SP-04's `test/dedup` on lines none of the three meant to change — **V2-MERGE-05**.

**Still open, and named here because nobody else will name it:** SP-05's B-A budget was measured against a daemon holding *stub* sketches, a *stub* DAG and a *stub* store. §4.6 and §5.1 are where that measurement is redone against the real thing, and they are the only place the 15 ms figure has ever been tested for what it actually claims. Branch state at handoff: **HEAD `b57df25`, 7 commits, not pushed, not merged.**

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP05-01 | Three packages green, race + repeat | `go test -race -count=2 ./internal/ipc ./internal/daemon ./internal/contract` | exit 0 |
| V2-SP05-02 | Address resolution: pipe, socket, XDG, `sun_path` guard | `go test -run 'TestProjectHash12\|TestResolve' ./internal/ipc/` | 12-hex project hash, case-folded on windows/darwin; XDG preferred; `<TempDir>/qp-<h8>.sock` fallback ≤ 100 bytes; `ErrAddrTooLong` when impossible; `\\.\pipe\qompack.<h12>` on windows |
| V2-SP05-03 | NDJSON framing, byte-exact | `go test -run 'TestEncodeRequestByteExact\|TestDecodeRequestRoundTrip\|TestLineReaderRejectsOversize' ./internal/ipc/` | encoded bytes equal `testdata/golden/contracts/ipc/observe_tool.ndjson`, one trailing `\n`, no HTML escaping; 1 MiB line cap enforced with resynchronization |
| V2-SP05-04 | 32-byte hot-path state record | `go test -run TestState ./internal/ipc/` ; `go test -bench BenchmarkReadState ./internal/ipc/` | round-trip exact, file exactly 32 bytes; missing/short/bad-CRC all fall back to config defaults; atomic under 500 concurrent writers; **< 100 µs/op** |
| V2-SP05-05 | **`Client.Send` never returns a propagating error** | `go test -run 'TestSend' ./internal/ipc/` | all rows green, incl. `TestSendNeverReturnsError` (200 rapid cases × 4 hostile listeners, `err == nil` every time, wall time < 3 × ack deadline), `TestSendDaemonDownSpoolsAndReturnsNilError`, `TestSendNAKSwitchesToSpool`, `TestSendHotSpoolSkipsConnect`, `TestSendModeOffDoesNothing`, `TestSendOversizeExternalizes` |
| V2-SP05-06 | Spool is append-only and drop-safe | `go test -run TestSpool ./internal/ipc/` | pre-existing content intact after `Append`; `O_TRUNC` rejected by `paths.AppendOnly`; write failure ⇒ nil return, `l0.dropped` incremented per event, exactly one `Loud` per session |
| V2-SP05-07 | Server: routing, ACK/NAK, panic containment, concurrency | `go test -run TestServer ./internal/ipc/` | 3 200 ACKs across 64 goroutines under `-race`; unknown op ⇒ one `NAK` and the connection survives; handler panic ⇒ `NAK` + `ipc.route.panic == 1` |
| V2-SP05-08 | Transport permissions | `go test -run 'TestUnixSocketPermissions\|TestStaleUnixSocketReclaimed' ./internal/ipc/` ; on windows `QOMPACK_TEST_ACL=1 go test -run TestWindowsPipeACLRejectsOtherUser ./internal/ipc/` | socket `0600`, directory `0700`; stale socket reclaimed; SDDL contains the current SID and begins `D:P` |
| V2-SP05-09 | `ipctest` conformance suite, zero skips | `go test -run 'Suite' ./internal/ipc/...` ; `grep -rn "t.Skip" internal/ipc` | green; no output |
| V2-SP05-10 | Daemon singleton lock and lazy detached spawn | `go test -run 'TestAcquireLock\|TestStaleLockReclaimed\|TestLiveLockNotReclaimed\|TestHeartbeat\|TestRunReturnsNilWhenLockHeld' ./internal/daemon/` | second acquisition ⇒ `ErrLockHeld`; dead pid reclaimed; a live listener defeats an old heartbeat |
| V2-SP05-11 | Session registry | `go test -run TestRegistry ./internal/daemon/` | new session resets `HotSpool`→`HotSync`; ended sessions evicted first over `maxSessions`; all-live over cap keeps all and emits one `Loud` |
| V2-SP05-12 | **WAL ingest — the durability boundary** | `go test -run TestIngest ./internal/daemon/` ; `go test -bench BenchmarkIngestAccept ./internal/daemon/` | WAL holds exact bytes; ring full spills to spool without blocking (`l0.ring_full` counted); **ACK precedes processing**; `BenchmarkIngestAccept` meets **B-B p99 < 2 ms** |
| V2-SP05-13 | Drain: idempotent, resumable, blob-aware, corruption-tolerant | `go test -run TestDrain ./internal/daemon/` | 10 lines ⇒ 10 dispatches (not 20), file deleted; cancel/resume totals exactly 1 000; blob resolved and deleted; corrupt line counted, file still consumed |
| V2-SP05-14 | Idle controller (O3 seam) | `go test -run 'TestIdle\|TestIsIdle' ./internal/daemon/` | priority order respected; budget respected; task panic isolated; `IsIdle` flips exactly at `idle.detectAfterSeconds` |
| V2-SP05-15 | **§8.1 fallback: observable `sync` → `spool` transition** | `go test -run 'TestBreachDetector\|TestHotModeTransition\|TestSpoolOnBreachFalse' ./internal/daemon/` | transition after exactly 3 consecutive 512-sample breach windows; a clean window resets; reverts after 3 clean windows; the transition is visible in `state.bin`, in the NAK frame, in the WARN log and in `status` |
| V2-SP05-16 | **Wave-appropriate nil-service tolerance** | `go test -run 'TestServicesAllNil\|TestHandleOverridesDefaultRoute\|TestBindRunsInOrder' ./internal/daemon/` | all 13 ops answered with a fully nil `Services`, zero panics, WAL holds every hot-path event; `mcp` alone returns `OK:false, Err:"mcp not built"` |
| V2-SP05-17 | Degradation semantics (§12.1) | `go test -run 'TestDegradedPassive\|TestModeOffSkipsIngest' ./internal/daemon/` | in `ModeDegradedPassive`: no `additionalContext`, no `customInstructions`, no acting idle tasks — but 20/20 `observe.tool` still recorded; in `ModeOff` nothing is ingested |
| V2-SP05-18 | Config hot reload defers chunk changes | `go test -run TestConfigReloadDefersChunkChange ./internal/daemon/` | in-memory chunk block unchanged; `state/config-pending.json` written; one `Loud` |
| V2-SP05-19 | `SketchSet` never rewrites `tried.bloom` | `go test -run TestSketchSetNeverWritesTriedBloom ./internal/daemon/` | bytes and mtime unchanged |
| V2-SP05-20 | Idle exit | `go test -run TestIdleExitWithZeroSessions ./internal/daemon/` | `Run` returns; `run/daemon.lock` and `run/state.bin` removed |
| V2-SP05-21 | **Contract monitor reports `ModeFull` on a wave-1 build** | `go test -run 'TestFreshBuildReportsModeFull\|TestDeclaredProducerSetMatchesArchitecture' ./internal/contract/ -v` | `ModeFull`; exactly **four** assertions at `OK:true, SevInfo, Observed:"not-yet-implemented"` (`precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `hook.additional_context_delivered`, `mcp.server_registered`); exactly five always-declared producers |
| V2-SP05-22 | Contract monitor: degrade loud, restore on two clean runs | `go test -run 'TestCriticalFailureDegrades\|TestTwoCleanRunsRestore\|TestOneCleanRunDoesNotRestore\|TestDegradeIsIdempotent\|TestHistoryPersistsAcrossMonitors' ./internal/contract/` | degrade writes exactly one `Loud` + `state/contract.json` with `DegradedSince`; restore needs two clean runs and is equally loud |
| V2-SP05-23 | The nine assertions, individually | `go test -run 'TestSessionStartFires\|TestSourceCompact\|TestSentinel\|TestAdditionalContextGated\|TestPreCompactTimeoutUnknown\|TestCustomInstructionsProbePhrase\|TestHookPayloadShape\|TestPluginRootUnset\|TestMarkerIsWrittenByFlushAndCheckpointOnly' ./internal/contract/` | each behaves as its row in §12.1 specifies; `TestPanickingAssertionDoesNotDegrade` proves an assertion crash never degrades the session |
| V2-SP05-24 | Budget table matches the architecture | `go test -run TestBudgets ./internal/obs/` | six budgets B-A…B-F in order with the §2.4 `Clock` strings; `Gated` true for B-A, B-B, B-E, B-F; B-D never gated; B-A limit follows `runtime.hotPath.budgetMs` |
| V2-SP05-25 | **Hooks exit 0 under every injected fault** | `go test -run 'TestHooksExitZeroUnderFaults\|TestFaultSitesInertWhenUnset\|TestSelfTestIsTheOnlyNonZeroExit' ./test/e2e/ -v` | **66/66** combinations exit 0 with valid JSON on stdout; fault sites inert when `QOMPACK_FAULT` is unset; only `self-test` may exit non-zero |
| V2-SP05-26 | Daemon e2e against the real binary | `go test -run 'TestE2EHookRoundTrip\|TestE2ELazySpawn\|TestE2EIdleExit\|TestE2ESpoolSubmodeEndToEnd\|TestE2ESelfTest' ./test/e2e/` | 50 hooks ⇒ 50 WAL lines; lazy spawn listening within 1.5 s and the spool drained; idle exit removes lock and state; `self-test --json` exits 0 with `mode == "full"` |
| V2-SP05-27 | **B-A / B-B / B-D / B-E hot-path harness** | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-v2-baseline.json` | `B-A.pass == true` (p99 < 15 ms), `B-B.pass == true` (p99 < 2 ms), `B-E.pass == true` (p99 < 2 s); `b_a_method` and `spawn_floor_ms` present; B-D reported, never gated. **See §5 — this must be re-run warm with the real store/DAG/sketches** |
| V2-SP05-28 | Security posture | `go run ./tools/devtool lint` + the CI `security` job on the branch | zero non-test imports of `net/http`, `net/url`, `crypto/tls`; `net` only in `internal/ipc` and only `unix`; `os/exec` only in `internal/daemon`, `internal/cli`, `tools/`; `govulncheck` clean |
| V2-SP05-29 | Coverage floors | `go run ./tools/devtool cover` **and** `go test -cover -count=1 ./internal/ipc/ ./internal/daemon/ ./internal/contract/ ./internal/cli/` | `internal/ipc`, `internal/daemon`, `internal/contract` and SP-05's `internal/cli` files each ≥ **75 %**. Measured at SP-05's tip: **ipc 84.2 %, daemon 82.0 %, contract 83.9 %, cli 81.5 % — on Windows only** (§2.5a G). Read the second command's numbers: `cover` is vacuous for `ipc`/`daemon`/`contract` until **V2-MERGE-14** lands, and `contract` additionally sits in `probeBlind`, so it is exempt from the stub cross-check by design and needs `landedSubplans` to bind at all |

### 2.6 SP-06 — content-addressed store, redaction, exact token accounting (`internal/store`, `internal/redact`, `internal/tokens`)

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP06-01 | Three packages green, race + repeat | `go test -race -count=2 ./internal/store ./internal/redact ./internal/tokens` | exit 0 |
| V2-SP06-02 | **Redaction: all ten built-in rule families** | `go test -run TestRedact_ ./internal/redact/ -v` | PEM, AWS (`AKIA`/`ASIA`), GitHub (`ghp_`/`gho_`/`github_pat_`), Anthropic `sk-ant-` before generic `sk-`, JWT, bearer value-only, credentialed URI, assignment value-only, dotenv gated by key name — each fires exactly once on its fixture and leaves surrounding text byte-identical |
| V2-SP06-03 | Redaction: idempotent, bounded growth, deterministic | `go test -run 'TestRedact_Idempotent\|TestRedact_BoundedGrowth\|TestRedact_Deterministic\|TestRedact_NeverWholeInputExceptPEM' ./internal/redact/` ; `go test -run=XXX -fuzz FuzzRedactIdempotent -fuzztime 60s ./internal/redact/` | `Redact(Redact(x)) == Redact(x)`; `len(out) ≤ 2*len(in)+64`; zero fuzz crashers |
| V2-SP06-04 | Redaction: user patterns admitted or rejected safely | `go test -run 'TestRedact_UserPattern\|TestRedact_Rejects\|TestRedact_InvalidUserPattern\|TestRedact_GrowthBoundHoldsUnderHostilePattern\|TestRedact_Disabled' ./internal/redact/` | zero-width and single-char patterns rejected at `New` with one `Loud` and `redact.pattern_rejected == 1`; built-ins stay active |
| V2-SP06-05 | Token classification (G10.2) | `go test -run 'TestClassify_All\|TestClassify_SP01TableStillPasses' ./internal/tokens/` | seven classes correct; SP-01's 14-case table unchanged by the inserted image-magic rule |
| V2-SP06-06 | **Media sizing replaces the flat 2 000** (§2.2 / G10.2) | `go test -run 'TestEstimateImage_\|TestEstimatePDF_' ./internal/tokens/ -v` | 1024×768 PNG ⇒ 1049 tokens; 4000×3000 PNG downscaled then clamped to **1600**; JPEG/GIF/WebP dimensions parsed; text PDF within ±15 % of `units(text)*0.92` and **not** 2 000; scanned PDF ⇒ `PDFTokensPerPage` from config |
| V2-SP06-07 | **Exact chunk-level accounting** | `go test -run 'TestEstimateRoot_\|TestChunkCache_\|TestUnits_Deterministic' ./internal/tokens/` ; `go test -bench BenchmarkEstimateRoot_64Cached ./internal/tokens/` | cache hit ⇒ `tokens.chunk_miss == 0`; miss path reproduces SP-01's baseline formula exactly; memo key is `core.Hash` alone; cache persists across `Close`/reopen and survives a truncated `chunktokens.bin` with one `Loud`; **≤ 5 µs/op** |
| V2-SP06-08 | Per-project calibration, clamped and persisted | `go test -run TestCalibrate_ ./internal/tokens/` | factor clamped to `[CalibrationMin, CalibrationMax]` from config (never literals `0.6`/`1.6`); persists; ignores zero; reads SP-01's flat calibration file |
| V2-SP06-09 | **Object layer: 2-level fanout, zstd, dedup** | `go test -run 'TestPutBytes_FanoutLayout\|TestPutBytes_CompressionNone\|TestPutBytes_GlobalDedup\|TestPutBytes_IdenticalRootIsFree' ./internal/store/` | `objects/<h[0:2]>/<h[2:4]>/<h>.zst`; four file versions ⇒ 4 roots with strictly decreasing `Novel` and total objects < 1.6× v1 alone; identical payload ⇒ `Novel==0`, no new `roots.jsonl` line |
| V2-SP06-10 | **Ingest order: redact → canon → chunk** (§8.1 item 1, §13 invariant 7) | `go test -run 'TestPutBytes_RedactionBeforeChunking\|TestPutBytes_RedactionRunsWhenDepsRedactIsNil\|TestPutBytes_CanonicalizeBeforeChunk\|TestPutBytes_CanonFailureFallsBack' ./internal/store/` | no object anywhere under `objects/` contains the literal secret, including when `Deps.Redact` is nil; canon failure degrades to storing redacted bytes with a `Warn`, never an error |
| V2-SP06-11 | Raw-byte accounting is per-put, not per-root | `go test -run 'TestPutBytes_DedupHitReportsThisPutsRawBytes\|TestPutBytes_KeepRaw\|TestPut_ReaderTruncation\|TestPutBytes_EphemeralFlagPersists' ./internal/store/` | `Stats.RawBytes` is the sum of the inputs (the regression that would silently understate `DedupRatio`); delta roots restore byte-exactly; `Truncated` at `MaxPutBytes` without error; `Ephemeral` survives reopen |
| V2-SP06-12 | Near-duplicate detection | `go test -run 'TestPutBytes_NearDup\|TestPutBytes_NoNearDupForDistinctPaths' ./internal/store/` | v1→v2 ⇒ `NearDup != nil`, `Jaccard ≥ 0.9`, `PriorRoot` = v1 |
| V2-SP06-13 | Read paths: `GetChunk`, `Open`, **`OpenSpan` (§8.7 minimal span)**, `Has` | `go test -run 'TestGetChunk_\|TestOpen_StreamsFullRoot\|TestOpenSpan_Boundaries\|TestHas_NoIO' ./internal/store/` | round-trip exact; all six span boundary cases exact; corruption quarantined to `tmp/quarantine/` with one `Loud` and `core.ErrNotFound` |
| V2-SP06-14 | Index durability and tolerance | `go test -run 'TestOpenStore_\|TestClosedStoreErrors\|TestConcurrentPut' ./internal/store/` | 50 roots survive `Close`/reopen; truncated final line counted as `store.index.badline == 1` without data loss; closed store returns `core.ErrDegraded` everywhere; 8×50 concurrent puts race-clean |
| V2-SP06-15 | `tool_use` index + supersession | `go test -run 'TestRecordToolUse_\|TestToolUsesByPath_\|TestMarkSuperseded_\|TestArgsDigest_' ./internal/store/` | idempotent replay ⇒ one line; conflicting replay ⇒ `core.ErrAppendOnly`; supersession appends a mutation record and never rewrites; args digest key-order invariant; preview ≤ 120 bytes on a rune boundary |
| V2-SP06-16 | **File version history + `ChangedSince`** (§8.2, §8.3) | `go test -run 'TestAppendFileVersion_\|TestFileAt\|TestChangedSince_\|TestFilesJSON_MaterializedByFlush' ./internal/store/` | history ascending; `FileAt` picks the version at-or-before; `ChangedSince` is exactly hash inequality, key-normalized, input-ordered, unknown paths counted not errored |
| V2-SP06-17 | **Segment log and the DPI guard** (§4.6, §8.2) | `go test -run TestSegment_ ./internal/store/ -v` | monotonic ids; `MarkEncoded` idempotent for the same seq; **`TestSegment_MarkEncodedRefusesDifferentSeq` ⇒ `core.ErrAlreadyEncoded`**; batch all-or-nothing; open segments refused; `Frontier` is the contiguous encoded prefix, not the maximum |
| V2-SP06-18 | Search (backs `recall`) | `go test -run TestSearch_ ./internal/store/` ; `go test -bench BenchmarkSearch_1000Roots ./internal/store/` | exact-path beats suffix; text ranked by occurrence; symbol span widened to the enclosing symbol; default `K==5`; deterministic over 20 runs; truncation counted not errored; **≤ 25 ms/op** |
| V2-SP06-19 | **`Stats.DedupRatio` and the Phase-1 exit criterion** | `go test -run 'TestStats_DedupRatio\|TestPhase1ExitCriterion_ReadHeavy\|TestStats_SublinearGrowth' ./internal/store/ -v` | **`DedupRatio ≥ 4.0`** on the read-heavy corpus (40 reads across 10 files with edits between); `Bytes` after 200 puts < 25 × `Bytes` after 8 puts |
| V2-SP06-20 | **GC: deadline-bounded, resumable, mark-and-sweep** | `go test -run TestGC_ ./internal/store/ -v` ; `go test -bench BenchmarkGC_50kObjects ./internal/store/` | roots harvested from checkpoints/pins/eliminations **without importing those packages**, including bare 64-hex; retention is `max(30 days, 10 sessions)`; zero policy deletes nothing; dry run deletes nothing; deadline truncates with a resumable cursor and the union equals one unbounded run; tombstones are appends; **≤ 2 s/op, deadline honoured ±50 ms** |
| V2-SP06-21 | Flush + session index + append-only guard | `go test -run 'TestFlush_\|TestAppendOnlyGuard_StoreFiles' ./internal/store/` | `sessions.jsonl` last-wins per session; second flush appends nothing; every `*.jsonl` this package writes refuses truncation |
| V2-SP06-22 | Store property suite | `go test -run Prop ./internal/store/` | `PropPutGetRoundtrip`, `PropOpenSpanMatchesSlice`, `PropDedupMonotone`, `PropRedactIdempotent`, `PropChangedSinceIsExactlyHashInequality`, `PropMarkEncodedNeverDowngrades` all green |
| V2-SP06-23 | **Index formats are frozen** | `go test -run TestGolden_IndexFormats ./internal/store/` (**no** `-update`) | `roots.jsonl`, `tool_use.jsonl`, `segments.jsonl`, `files.json`, `sessions.jsonl` byte-identical to `testdata/golden/store/*` |
| V2-SP06-24 | Store e2e | `go test -run 'TestE2E_StoreSurvivesProcessRestart\|TestE2E_SecretNeverLandsInObjects' ./test/e2e/` | 200 payloads survive restart with matching `Stats`; **no built-in secret literal appears in any decompressed object** |
| V2-SP06-25 | Store/redact/tokens performance | `go test -bench . -benchmem ./internal/store ./internal/redact ./internal/tokens` | `PutBytes_100KB_Cold` ≤ 3 ms; `_Warm` ≤ 400 µs; `GetChunk` ≤ 60 µs; `OpenSpan_4KB_of_4MB` ≤ 150 µs; `OpenStore_50kRoots` ≤ 400 ms; `MarkEncoded_100` ≤ 1 ms; `Redact` 100 KB ≤ 2 ms |
| V2-SP06-26 | Conformance suites, zero skips | `go test -run 'Suite' ./internal/store/... ./internal/redact/... ./internal/tokens/...` ; `grep -rn "t.Skip" internal/store/storetest internal/redact/redacttest internal/tokens/tokenstest` | `RunStoreSuite` and `RunSegmentLogSuite` green; grep returns nothing |
| V2-SP06-27 | Coverage floors | `go run ./tools/devtool cover` **and** `go test -cover -count=1 ./internal/store/... ./internal/redact/... ./internal/tokens/...` | `internal/store` ≥ **90 %**; `internal/redact` ≥ **90 %** and `internal/tokens` ≥ **90 %** (SP-06's self-imposed override over the §6.4 75 % floor). **`devtool cover` cannot check two of these three.** `plans/OWNERS.tsv` records `redact` at floor **75** and `tokens` at floor **75** (owner `SP-01`), so `cover` passes them anywhere in 75–89 % while this row claims 90. Read the second command's numbers; then either raise both OWNERS.tsv floors to 90 so the override is machine-checked, or restate the row at 75 and record the actual figures. A self-imposed floor that no tool enforces is a §2.0-class dead gate in miniature |

#### 2.6a Carried forward from SP-06 — read before running §2.6

Ten items below change what §2.6 should expect. Each was found during SP-06's implementation, is recorded in that subplan's own spec-resolutions table, and is reproduced here because **this checkpoint is where they surface**. None is a defect to re-open: ① is a real ordering constraint this checkpoint is the right place to settle, ⑦ is a regeneration the wave-1 merges legitimately force, ⑧ is a merge-hygiene check that must run **before** anything else in this document, and the rest are corrections to the expected result.

> **⑧ first.** It is the only item here that can be destroyed by the merge it describes, and the only one whose evidence expires. If you read nothing else in this section, run its greps.

**① `TestGuard_Phase0BeforeStore` fails on `feat/sp06-content-addressed-store` in isolation, and that is correct behaviour.**

The guard fires when `internal/store` is real while `internal/eval` is still an SP-01 stub, citing closing note 1 — *"Phase 0. Without measurement, everything else is opinion."* `internal/eval` is **SP-02's**, so a branch containing only SP-06 structurally cannot satisfy it: store is real, eval is not.

The guard was deliberately **left untouched**. It encodes a ship-order constraint about what reaches an integration branch, and neutralizing it to make one topic branch green would discard exactly the signal it exists to raise.

- **Resolution at this checkpoint:** it clears by itself once SP-02 is merged into `develop`, because `evalProbe` then reports not-a-stub. §1's merge sequence already lands SP-02 before SP-06.
- **What to verify here:** after the wave-1 merges, `go test -run TestGuard_Phase0BeforeStore ./test/guards/` must pass **without any change to `buildorder_test.go`**. If it still fails, the wave order was violated, not the guard.
- **Do not** "fix" this by merging SP-06 ahead of SP-02, or by gating the guard on a branch name or environment variable.

**② V2-SP06-25 — the `Redact` 100 KB ≤ 2 ms budget does not hold for two of three payload shapes.** After a mandatory-literal prefilter, the dominant no-secret case is **0.64 ms** (from 32.5 ms, 51×) and a warm `PutBytes_100KB` is **227 µs**. But a payload merely *containing* a keyword such as `key` or `token` costs **8.2 ms**, and one carrying real secrets **27.2 ms**. Measured per rule, every rule except `pem_private_key` costs ≥ 2 ms for a single 100 KB RE2 pass on its own, so the budget is unreachable whenever even one full scan is required — `pem_private_key` is 250× cheaper only because its literal prefix `-----BEGIN ` triggers Go's literal-prefix scanner, which a leading `\b` defeats. Collapsing the ten rules into one alternation was measured and is **0.53–0.67×, i.e. slower**. Expect the two over-budget figures; treat the 2 ms number as needing revision, and see SP-17 for the window-around-literal-hits option that was deliberately not taken (it changes `\b` and `(?m)^$` semantics at every window edge).

**③ V2-SP06-26 — `grep -rn "t.Skip"` over the three conformance packages WILL return hits, and must.** Rule W-1 is implemented as a *runtime probe* (`skipIfStubStore`, `skipIfStub`), not as static skips, so the `t.Skip(ruleW1SkipMsg)` literal is permanently present in each suite and simply stops being reached once a real implementation lands. **Replace the grep with the assertion that actually matters:** run `go test -run 'Suite' ./internal/store/... ./internal/redact/... ./internal/tokens/... -v` and confirm every `/behaviour` subtest reports RUN and PASS rather than SKIP. As of SP-06 all six frozen store/segment behaviour cases execute (`put_get_round_trip`, `global_dedup_second_put_is_not_novel`, `changed_since_detects_a_hash_change`, `file_history_is_append_only_and_never_shrinks`, `mark_encoded_is_the_dpi_guard`, `range_never_loses_a_previously_returned_segment`). *(This is the third independent report of the same class of defect — see §2.2a ③ for `internal/eval` and V2-SP07-16 for `internal/dag`.)*

**④ V2-SP06-09 — `Novel` is not strictly decreasing across all four fixture versions.** v4 rewrites 240 lines *and* grows the file 19 %, so it legitimately writes more chunks than v1. The ≤ 1.6× bound is scoped to v1–v3, the small-edit case §6.1 actually describes; v4 is covered by `TestPutBytes_LargeRewriteStillSharesChunks`.

**⑤ V2-SP06-19 — `DedupRatio ≥ 4.0` holds, but the numbers are pre-canonicalization.** Measured **12.40** on four versions read four times each and **6.34** on the read-heavy corpus, both with `internal/canon` still an SP-01 stub. SP-04's O2 canonicalizers can only raise them, so re-measure here and expect an increase; a *decrease* after the wave-1 merges is a regression worth investigating.

**⑥ V2-SP06-18 / -20 / -25 — several benchmarks were taken on Windows and miss on syscall cost, not algorithmically.** Text search 69.5 ms vs 25 ms, `GC_50kObjects` 2.42 s vs 2 s, `GetChunk` 154 µs vs 60 µs, `OpenStore_50kRoots` 404 ms vs 400 ms. A CPU profile is **90.9 % `runtime.cgocall`** with zstd decode at 1.4 %. These budgets are specified against the CI Linux runner, where `open()` is roughly 10× cheaper; **re-measure there before treating any of them as a real miss.** Note also that `BenchmarkGC_50kObjects` previously seeded only 4 000 objects while claiming 50 000 — it now genuinely builds ~50 k, so its number is not comparable to any figure recorded before SP-06.

**⑦ The `testdata/golden/store/*` index goldens WILL need `-update` after the wave-1 merges, and that is expected rather than a regression.** They pin the on-disk shape of the five index files, recorded while `sketch`, `canon` and `chunk` were all still SP-01 stubs (Rule W-2). Three things change underneath them at this checkpoint: no `"sig"` key appears in any line today, because `sketch.Signature.MarshalBinary` reports `ErrNotImplemented` and both writers omit the key rather than fail the write — **once SP-03 lands, real signatures start being emitted**; `"canon"` currently always equals `"raw"`, because canonicalization is a no-op, and **SP-04 will make them diverge**; and the chunk arrays come from an injected content-defined chunker rather than the real one, so **SP-04's chunker changes the hashes and the boundaries**. The correct action here is: run `go test ./internal/store/ -run TestGolden_IndexFormats` first, and if it fails, confirm the diff is confined to those three axes before regenerating with `-update`. A diff touching key order, key names, or the `{"v":1,…}` record shape is **not** covered by this note and is a real regression. These goldens are SP-06's own artifacts under `testdata/golden/store/`; the frozen contract fixtures under `testdata/golden/contracts/store/` pin the Go types instead, are asserted by `fixture_test.go`, and must reproduce **unchanged** — if those move, something is genuinely wrong.

**⑧ THIS FILE WAS EDITED ON THREE WAVE-1 BRANCHES AT ONCE. Verify every half survived the merge before running anything below.** SP-06 added §2.6a (this section). SP-04 independently added a recommended-model header and a **new §2.0 merge-inspection section with `V2-MERGE-*` rows**, which did not exist on `develop`. SP-07 independently added the §2.7 preamble. The three touch different regions, so git merges them **cleanly and silently** — which is the danger: nothing will conflict, and nothing will announce that a half went missing if a resolution took one side wholesale. Run the greps in **§2.0a** and require every one of them.

Those patterns are deliberately **self-exclusive**: they match section headings and row IDs, not the prose of this warning, which names every half and would otherwise satisfy its own check. (Written the obvious way first — `grep -c "2.6a Carried forward from SP-06"` and `grep -c "V2-MERGE-"` — both returned a match on a file missing SP-04's half entirely, reporting success for exactly the state they exist to catch. Do not "simplify" them back.)

If any is zero the merge lost content, and the missing half is **gates this checkpoint is supposed to run** — §2.0's own note says three of its rows read state that §5 and §7 later overwrite, so once the fan-out has started those questions can no longer be answered at all. Recover with `git show <branch>:plans/V2-VERIFY-primitives-store-dag-and-baseline.md` and re-apply. The correct resolution is a **union of every edit**; never `-X ours` / `-X theirs` on a plan file.

**One caveat that made this urgent rather than routine, now closed.** When SP-06 landed, SP-04's half was **uncommitted in its worktree** (`C:\Users\Quant\Documents\Programming\Projects\qompack`, branch `feat/sp04-…`), so no branch carried it and `git show` could not have recovered it — a `checkout`, `reset` or `stash drop` there would have destroyed it with no conflict and no trace. That content is now in this file. The general lesson stands for wave 2: **a plan-file edit that lives only in a worktree is one `git checkout` from gone**, and `git worktree list` plus a per-worktree `git status` is the five-second check that finds it.

**⑨ `TestConformance_BehaviourBlocksActuallyRun` shells out to `go test -json`.** It is the mechanized W-1 merge blocker (see ③) and spawns one subprocess per suite, so it requires the **`go` toolchain on `PATH`** in whatever environment runs it. On a sandboxed or toolchain-less runner it fails with "go test -json produced no test events", which is an environment fault and not a conformance failure — do not read it as the suites having stopped running. It also builds those three packages without `-race` even under `devtool test-race`; that is a cost, not a correctness issue.

**⑩ Two couplings that will surface as loud, self-explaining failures rather than silent drift.** `test/e2e/store_test.go` hardcodes one literal per secret family and asserts each is present in its corpus fixture first, so regenerating `testdata/corpora/secrets/**` fails on a `fixture sanity` message naming the file — update the literal, do not weaken the assertion. Separately, **SP-06's commits 1–7 do not individually pass `golangci-lint`**: three `errcheck`/`unconvert` findings in commit 3 and 5's test files were fixed in commit 8, so only the branch tip is lint-clean. That is within the subplan's rule (per-commit gating is `devtool test`; `ci-local` is required at the final commit only), but a per-commit or bisecting lint run will report them.

**⑪ Defects SP-06 found in its own code during integration, fixed on the branch, and worth re-asserting here.** Each was reachable only through the public API or only on a failure path, which is how they survived to commit 8; each now has a test, and each is a regression class wave 2 can reintroduce. `openFS` leaked every append-only handle it had already opened whenever a later step failed — and a failed `openFS` returns no store, so nothing in the process could ever close them (`FSStore.releaseWriters`; on Windows they also locked the index files against the next attempt). `parseRootLine` accepted records it could not interpret, so any future `v:2` or unknown `op` line in `roots.jsonl` would have been indexed as a **phantom root** that `Stats` counts and `Search` ranks. **Seven entry points took a `context.Context` and never read it** — including `Put`, `PutBytes`, `GetChunk`, `Open` and `GC`, i.e. the whole ingest hot path — which makes SP-05's B-C budget advisory rather than enforced; the exemption for in-memory readers is now explicit and swept by `TestContract_BlockingEntryPointsHonourCancellation` / `TestContract_InMemoryLookupsIgnoreCancellation`. `clampSpan` and `truncateRunes` each promised a bound they did not enforce. And `chunkSet`'s `int32` narrowing was unguarded on the read path, so one bad digit in one index line could turn every read of that chunk into a spurious integrity failure — or, landing on `-1`, into a *silently disabled* integrity check.

**⑫ Two spec-level corrections SP-06 made that later waves inherit.** The `Redact` algorithm as written in `plans/V2-SP-06-content-addressed-store.md` appended `'»'` as a **rune constant**, which Go truncates to the single byte `0xBB` instead of the two-byte UTF-8 sequence — producing invalid UTF-8 and, worse, output that `placeholderRe` can never match, so the idempotence guard was **silently dead** and `Redact(Redact(x)) != Redact(x)`. And the guard `if rule.name != "pem_private_key" && s == 0 && e == len(in) { continue }` meant `Redact("@@SEC_AWS_AKID@@")` returned the key **unredacted**; the PEM carve-out reasoning applies verbatim to all ten families, so it is generalized rather than special-cased. Both are fixed in the code. **The buggy lines are still in the subplan document** — correct them there before any later subplan copies them.

**⑬ Three deliberate departures from SP-06's own plan text that change observable behaviour.** Each was forced, each is recorded, and each will otherwise be read as a defect here. **(a) `putObject`'s per-object `Sync()` before the rename is gone.** Measured: one 100 KB result is tens to hundreds of chunks and serialized fsyncs cost **1.9 s** against a 3 ms budget. The rename still provides *atomicity* — no torn object is ever visible — and *durability* is batched into `Flush`, exactly how borg, restic and git commit a repository transaction. **A crash can now lose an object but can never corrupt one.** The reachable crash states are: neither object nor root line (clean); an object with no root line (an orphan, which GC collects); or **a root line without its object**, which `GetChunk`/`Open` report as `core.ErrNotFound` and `Has` catches by falling back to a stat. That last state is degraded-but-detected, and repairing it is `qompack fsck`'s job — **a residual SP-17 inherits**, and one of the few places where "loud degradation" currently means "an error at read time" rather than "a repair". **(b) The rename backoff is not the specified 1/2/4 ms sleep**, because §6.1 bans wall-clock sleeps outright, `devtool lint`'s `sleepcheck` enforces that with no annotation escape hatch, and 7 ms of backoff inside a 3 ms budget is precisely what the ban exists to prevent. Retries are separated by `runtime.Gosched()`; a lock that outlives them fails the Put, the caller degrades (§12.3), and the identical object is rewritten next attempt because the content address has not changed. **(c) SP-06 rewrote a second SP-01 `tokens` test** — see V2-SP01-23, where the row's "only `TestEstimate_ProseVsCode`" parenthetical is now wrong.

### 2.7 SP-07 — dependence DAG and slicing (`internal/dag`)

#### 2.7a Inbound from SP-07 — read before executing the table

> **`plans/V2-SP07-handoff.md` is the long form of this section**, written at the tip of `feat/sp07-dependence-dag-and-slicing`. Seven rows below, plus V2-ALL-04, do not reconcile against a literal reading of their *Expected result* column. Two of them repeat defects SP-02 already reported in §2.2a — **if the same two flaws appear in two independently written sections, assume §2.3–§2.6 carry them too.**

**A. Rows whose expectation is wrong**

**① V2-SP07-16 is wrong as written, and satisfying it literally deletes a mandated mechanism.** `grep -rn "t.Skip" internal/dag` can never return nothing: `internal/dag/dagtest/suite.go:124` calls `t.Skip(ruleW1SkipMsg)`, which is Rule W-1's mandatory mechanism (`00-ARCHITECTURE.md` D9, §5.22), and `dagtest/suite_test.go:42` (`TestRunGraphSuite_StubIsSkipped`) is an **SP-01 test that asserts the skip fires**. The bare string `t.Skip` also appears in four *comments* (`bench_test.go:63`, `dagtest/behaviour.go:237` and `:239`, `dagtest/suite.go:119`), each explaining why a skip is *not* used at that site — so the grep returns hits even with no skip present. **This is the same defect as V2-SP02-12.** The row's intent already holds: `go test -run Conformance ./internal/dag/ -v | grep -c -- "--- SKIP"` ⇒ `0`. Use that, or the scoped `grep -rnE "t\.Skip\(" internal/dag --include='*.go' | grep -v 'dagtest/suite\.go'` ⇒ empty.

**② V2-SP07-20 passes vacuously** — the same hole SP-02 closed for `internal/eval`. `tools/devtool/cover.go` `continue`s on every package not owned by SP-01, printing `exempt (stub, owned by SP-07): dag` **without ever testing whether the package is still a stub**, so this row would report success at 0 % coverage just as readily. `plans/OWNERS.tsv:36` declares the floor (`dag SP-07 85 AddNode`), so the floor is registered and unenforced. Measured directly instead:

```sh
go test -coverprofile=<scratch>/dag.cover ./internal/dag/     # coverage: 90.9% of statements
```

**SP-07 could not fix this, and the reason matters for the merge:** SP-02's `landedSubplans` list *does not exist on `feat/sp07-…`*, because SP-07 was cut from `develop` **before** SP-02 merged — so `cover.go` there is still the unconditional version, and SP-07 had nothing to add itself to. The fix is **V2-MERGE-14**.

> **The scope of this row is the whole document, not `internal/dag`.** The same hole currently exempts `chunk`, `canon`, `symbols`, `store`, `sketch`, `redact`, `ipc`, `daemon`, `contract` and `eval` as well — so **every coverage assertion anywhere in this file is vacuous until V2-MERGE-14 lands**, in §2.2 (V2-SP02-20), §2.3 (V2-SP03-20), §2.4 (V2-SP04-27), §2.5 (V2-SP05-29), §2.6 (V2-SP06-27), §2.7 (this row) **and in §3.1 item 2**, which asserts the same property against `develop` and is the one place a reader would reasonably expect the gate to be checked as a gate. Do the fix once, for every wave-1 package as it lands, not per-row as each subagent trips over it.

**B. Deviations forced by SP-01's frozen fixtures — accept these, do not repair them**

Rule W-2 froze `testdata/golden/contracts/dag/want/{node_line,edge_line}.jsonl` before SP-07 was written, and four of SP-07's own specifications conflict with those bytes. The frozen fixtures won; `docs/adr/0007-dag-slices-are-scores-not-drop-decisions.md` carries the full reasoning.

- **V2-SP07-03 — the NodeID is 378 bytes, not 376.** The frozen node line pins `"id":"file:src/auth.ts"`, and `file:` is 5 bytes, so `5 + 360 (head) + 1 ("~") + 12 (Hash.Short) = 378`. The row's 376 assumes a 3-byte prefix. The long forms (`file:`, `tooluse:`, `toolresult:`, `assistant:`, `userprompt:`, `symbol:`, `decision:`, `elimination:`, `segment:`) are contractual. `TestNodeIDLongKeyHashSuffix` asserts 378 as a literal. **Read the row as 378.**
- **V2-SP07-02 — kinds round-trip through `String()`/`Parse*`, not `encoding.TextMarshaler`.** `TestNodeKindTextRoundTrip` and `TestEdgeKindTextRoundTrip` cover every kind via `String()` + `ParseNodeKind`/`ParseEdgeKind`. **Adding the `TextMarshaler` pair was verified empirically to break both marshalling and unmarshalling of the frozen fixtures**, because the kinds are pinned as *integers* (`"kind":4` ⇒ `KindFile`) and `encoding/json` prefers `TextMarshaler` over the integer form. `doc.go` carries a standing warning against adding it. **The multiplier table, the kind count and the numbering this row also checks are all unaffected** — only the round-trip mechanism differs, so verify those three literally and read "text round-trips" as `String()`/`Parse*`. The same constraint is why `KindInvalid`/`EdgeInvalid` are **last** in their iota blocks: prepending a sentinel renumbers every kind and breaks `node_line.jsonl`.
- **V2-SP07-12 / -13 — the generation record is `"type":"generation"`, not a `g` record.** SP-01's frozen line shape discriminates every line on a `"type"` field, so the four line types are `node`, `edge`, `tombstone`, `generation`. `gen:1` is literally present: `{"type":"generation","v":1,"gen":1,"ts":…,"nodes":12,"edges":10}`. Only the discriminator token differs. **Read `g` as `"type":"generation"`.**
- **V2-SP07-11 — `thin-vs-full.json` carries no `ns_thin`/`ns_full` fields.** A golden containing nanosecond measurements differs on every run, so it would either be rewritten constantly or compared so loosely it asserts nothing. The `ns_thin ≤ ns_full` claim is asserted per seed in `TestThinVsFullComparison` and logged (`thin 253.31µs, full 720.53µs` — thin is 2.5–4× cheaper on every seed). The size/recall/precision fields *are* in the golden and reproduce within 2 %. **One caveat that bit this branch:** a single slice costs a few hundred microseconds, which is **not** safely above Go's monotonic-clock granularity on Windows (it falls back to roughly millisecond resolution when no process holds the system timer finer, and that can change mid-run). The original assertion compared two single-shot readings 8 µs apart and failed under `go test ./...` load. It now times a batch of 20 and divides — **if you touch this measurement, keep the batch.**
- **V2-SP07-05 — the concurrency test runs 250 iterations, not 2 000.** `TestConcurrentMutationAndRead` uses 8 writers × 8 readers × **250**. Every reader iteration calls `CrossingEdges` and `NodesAfter` against an index a concurrent writer has almost certainly dirtied, so each pays a full O(N log N) rebuild — by design, that is the contended path the test exists to exercise. Cost grows as `iterations × N log N` *and* N grows with iterations: at 2 000 the graph reaches ~32 000 nodes and the run takes minutes. **Read the row as 250**, or move throughput-under-contention to a benchmark.

**C. Carry-forward work items this checkpoint owns**

- **ACTION 1 — `backward_slice_scores` is still unrecorded.** `testdata/golden/contracts/dag/MANIFEST.json` lists it as `record-by-owner` with SP-07 as owner, but recording it flips `internal/testutil/fixtures_test.go`'s guard from 23 recorded / 5 record-by-owner to 24 / 4 — **a different package's contract test**. SP-07 shipped the five fixtures its own plan names — `nodeid.json`, `graph-basic.jsonl`, `crossing.json`, `slice-backward.json`, `thin-vs-full.json` — and left this one visible rather than quietly editing another package's guard mid-wave. Either record it and update the guard in the same commit, or drop the manifest entry if `slice-backward.json` covers it. **Do not leave it as-is:** a permanently unrecorded `record-by-owner` entry trains people to ignore the manifest. Note the interaction with **V2-MERGE-15**: the merged counts are 33/5 before this action and **34/4** after it, so both edits belong in one commit.
- **ACTION 2 — add SP-07 to `landedSubplans`.** Converges with §2.2a ⑤ and SP04-D4; the authoritative form is **V2-MERGE-14**, which must list all seven subplans, not just this one.
- **ACTION 3 — V2-ALL-04 cannot run in this environment.** The row requires pushing `verify/v2` and confirming the CI matrix. **This repository has no git remote.** `ci-local` is the closest available equivalent and is green end to end. Either add a remote before the checkpoint, or record the row as **environment-blocked** — but **do not mark it passed on the strength of `ci-local`**, which does not run the cross-OS matrix, `crossbuild`, `security` or `docs`.
- **INHERIT (SP-09) — the DAG is not acyclic, and the negative-knowledge detector must tolerate it.** D-7 forbids one specific cycle (an assistant consuming the result of the tool use it emitted). The **whole graph is not a DAG and cannot be**: the ordinary Read-then-Edit pattern closes a legitimate loop, because §8.1 item 4 directs a shared-file edge *into* a tool use that consumed a file and *out of* one that produced it — `tooluse:t1 → toolresult:t1 → assistant:2 → tooluse:t2 → file:a → tooluse:t1`. Every edge there is individually correct, and `TestReadThenWriteClosesALegitimateCycle` pins it so it cannot be assumed away. Slicing tolerates it by construction (scores strictly decrease along any path, each node finalizes once, the `minScore` floor bounds the walk). **SP-09 scans this same graph for the test-fail → revert → different-approach pattern and inherits the obligation: a naïve recursive descent will not terminate.** Stated in ADR 0007, repeated here because SP-09 is in a later wave and will not otherwise see it. **Carry this into `V3-VERIFY-observer-and-negative-knowledge.md` when this checkpoint closes.**
- **NOTE — wall-clock gates are scaled, not skipped, under instrumentation.** `TestSliceLatencyBudget` and `TestCrossingLatencyBudget` assert §6.4's and §8.4's budgets on every `go test`, including `ci-local`'s uninstrumented `test` step — so the **real** budget is enforced in CI. They scale their ceiling when the binary carries instrumentation, because the same walk measures:

  | build | backward slice | inflation |
  |---|---|---|
  | uninstrumented | 0.40 ms | — |
  | `-covermode=atomic` (`devtool cover`) | 0.81–1.18 ms | ~3× |
  | `-race` (`devtool test-race`) | 2.02 ms | ~5× |
  | both | 9.23 ms | ~23× |

  Factors are **4× for coverage and 8× for race, multiplied when both apply**, each above its measured inflation. They scale rather than skip because Rule W-1 bans `t.Skip` and a gate that evaporates under `-race` is one nobody notices has stopped running. All four modes still fail on the regression class the gates exist to catch (the `orderByScore` defect cost 5.4×). **If a slower CI box misses the uninstrumented 1 ms budget, that is a real signal about that host, not a reason to raise `sliceBudget`.**

**D. What was already green on SP-07's branch, and one dead `-run` pattern.** Verified at the branch tip: `go test -race ./internal/dag/...` ok; `go test -race ./...` ok across the whole module; `go run ./tools/devtool ci-local` exit 0, all 8 steps; `Qompack.md` untouched; `devtool lint`'s `importgraph` confirms `internal/dag` imports only `core`, `paths`, `config`, `logging`; coverage 90.9 % ≥ 85 % measured directly. **Every §2.7 `-run` pattern was checked against `go test -list`**, which is how the next item was found — and is a check the other six groups have not had.

**One dead `-run` pattern, already fixed, and the generalisation it earned.** V2-SP07-10 runs `-run 'TestSliceGolden|TestCrossingEdgesGolden'`, and the backward-slice golden test was named `TestBackwardSliceGolden` — which that pattern does **not** match, so the row verified `crossing.json` only and reported `ok` while never touching `slice-backward.json`. Renamed to `TestSliceGoldenBackward`. **`go test -run` prints `ok` when its pattern matches nothing**, so any row in this document whose pattern has drifted is a silent pass. All SP-07 patterns were checked against `go test -list`; the other groups' were not. Same lesson as §2.2a ②.

| ID | Functionality | Command | Expected result |
|---|---|---|---|
| V2-SP07-01 | Package green under race | `go test -race ./internal/dag/...` | exit 0 |
| V2-SP07-02 | Nine node kinds, eight edge kinds, multiplier table | `go test -run 'TestNodeKind\|TestEdgeKind' ./internal/dag/` | text round-trips for all kinds **via `String()` + `ParseNodeKind`/`ParseEdgeKind`, NOT `encoding.TextMarshaler`** — adding the `TextMarshaler` pair breaks the frozen fixtures, which pin kinds as integers (§2.7a B); multipliers exactly `1.00, 1.00, 1.00, 0.95, 0.88, 0.60, 0.50, 0.30`; `EdgeInvalid` ⇒ 0; `KindInvalid`/`EdgeInvalid` are **last** in their iota blocks and must stay there |
| V2-SP07-03 | Stable `NodeID` scheme | `go test -run 'TestNodeID\|TestParseNodeID' ./internal/dag/` | `testdata/golden/contracts/dag/nodeid.json` reproduces byte-for-byte; 500-byte key ⇒ exactly **378** bytes with hash suffix (`5 + 360 + 1 + 12`; the row's 376 assumed a 3-byte prefix the frozen fixture forbids — §2.7a B); control chars and invalid UTF-8 sanitized and round-trip through `Flush`/`Open` |
| V2-SP07-04 | Graph mutation: validation, upsert, dedup, tombstones | `go test -run 'TestAddNode\|TestAddEdge\|TestTombstone\|TestOutInCopies\|TestAnchorNodePosIsEarliest\|TestClosedGraphRejects' ./internal/dag/` | invalid nodes/edges wrap `ErrInvalidNode`/`ErrInvalidEdge`; upsert merges; ephemeral is sticky; edge dedup keeps max weight and min turn; dangling endpoints tolerated and counted; anchor nodes keep the earliest `Pos` |
| V2-SP07-05 | Concurrency | `go test -race -run TestConcurrentMutationAndRead ./internal/dag/` | 8 writers × 8 readers × **250** iterations: no race, no deadlock, no panic; final `Stats().Nodes` exact. **Read the row as 250, not 2 000** — cost is `iterations × N log N` with N growing in iterations, so 2 000 takes minutes and buys no coverage (§2.7a B) |
| V2-SP07-06 | **`CrossingEdges` = `segment_coupling(p)`** (§8.4) | `go test -run 'TestCrossingEdges\|PropCrossingEdgesMatchesBruteForce' ./internal/dag/` ; `go test -bench BenchmarkCrossingEdges ./internal/dag/` and `go test -run TestCrossingLatencyBudget ./internal/dag/` | matches brute force on every rapid case and all twelve golden positions; the `lo < pos <= hi` rule asserted at both ends; dangling excluded; equal positions cross nothing; **< 5 µs median on 15 000 edges** |
| V2-SP07-07 | `NodesAfter` total order | `go test -run 'TestNodesAfterOrdering\|PropNodesAfterMatchesFilter' ./internal/dag/` | live-only, freshly allocated, ordered by `(Pos, Turn, ID)` |
| V2-SP07-08 | **Scored backward/forward slicing** (§6.4, §8.3) | `go test -run 'TestBackwardSlice\|TestForwardSlice\|TestSlice' ./internal/dag/` | exact decayed scores (`1, 0.85, 0.7225`); max-path not sum; `MaxNodes` truncation exact and the exact-fit case not marked truncated; `MaxDepth` respected; the `1e-4` floor is not a truncation; unknown criteria ⇒ empty with nil error; deterministic tie-break |
| V2-SP07-09 | Thin slicing is the default and is a subset of full | `go test -run 'TestThinDropsControlOnly\|TestDefaultSliceOptionsFromConfig\|PropThinSliceIsSubsetOfFull\|PropScoresBoundedAndMonotone' ./internal/dag/` | `DefaultSliceOptions(config.Defaults()).Thin == true`; `Decay == 0.85`; `MaxNodes == 5000`; `Deadline == 5ms`; thin ⊆ full with `thin[id] ≤ full[id]` |
| V2-SP07-10 | Slice goldens | `go test -run 'TestSliceGolden\|TestCrossingEdgesGolden' ./internal/dag/` (**no** `-update`) | `slice-backward.json` **and** `crossing.json` reproduce to 5 decimals / exactly. Confirm **both** ran: the backward-slice test was named `TestBackwardSliceGolden`, which this pattern does not match, so the row silently verified `crossing.json` alone and printed `ok`. It is renamed `TestSliceGoldenBackward` — verify with `go test -list 'TestSliceGolden\|TestCrossingEdgesGolden' ./internal/dag/` before trusting the result (§2.7a D) |
| V2-SP07-11 | **Thin-vs-full measurement** (the §6.4 tradeoff, quantified) | `go test -run TestThinVsFullComparison ./internal/dag/ -v` | across 8 seeds: mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85`, mean `precision ≥ precision_full`; `thin-vs-full.json` matches within 2 %. **`ns_thin`/`ns_full` are asserted in the test and logged, not carried in the golden** — a golden holding nanoseconds differs on every run (§2.7a B). Expect `thin 253.31µs, full 720.53µs`-shaped log lines, 2.5–4× on every seed. The measurement times a **batch of 20** because a single slice is not safely above Go's clock granularity on Windows; keep the batch |
| V2-SP07-12 | Persistence: append-only `deps.jsonl`, torn tails, corrupt lines | `go test -run 'TestFlush\|TestOpen\|TestAutoFlushAt2000\|PropLogRoundTrip' ./internal/dag/` | `Flush` bytes equal `graph-basic.jsonl` (minus the generation header); torn tail ⇒ `TruncatedTail == true`, `LoadErrors == 0`; corrupt line ⇒ `LoadErrors == 1` and exactly one `Loud`; auto-flush at 2 000 records; flush failure retains pending. **Read "the `g` header" as `{"type":"generation",…}`** — SP-01's frozen line shape discriminates on `"type"`, so the four line types are `node`, `edge`, `tombstone`, `generation` (§2.7a B) |
| V2-SP07-13 | Idle-only `Compact` | `go test -run TestCompact ./internal/dag/` | drops tombstoned nodes, writes **`{"type":"generation","v":1,"gen":1,…}`** (the row's "a `g` record with `gen:1`" — `gen:1` is literally present, only the discriminator token differs), no-op below the 25 % waste threshold, flushes pending first, preserves every slice answer, restores cleanly on cancel |
| V2-SP07-14 | **§8.1 item 4 edge builders, acyclic by construction** | `go test -run 'TestBuild\|TestBuilderOutputIsAcyclic' ./internal/dag/ -v` | the exact five-node/five-edge set for a read; consumes edge starts at the **previous** result; suppressed for parallel siblings (`PrevTurn == Turn`); future `PrevTurn` rejected; write direction reverses file/symbol edges; supersession edge scored `0.255`; **DFS over 200 built tool uses finds no cycle** |
| V2-SP07-15 | **No selection authority** (closing note 3, mechanized) | `go test -run TestNoBooleanKeepAPI ./internal/dag/ -v` | no exported function returns `map[NodeID]bool` / `[]bool`; no exported identifier matches `keepset\|dropset\|^keep\|^drop\|evict`; `doc.go` still contains the literal `NO SELECTION AUTHORITY` |
| V2-SP07-16 | `dagtest` conformance suite, zero **behaviour** skips | `go test -run Conformance ./internal/dag/ -v \| grep -c -- "--- SKIP"` ; `grep -rnE "t\.Skip\(" internal/dag --include='*.go' \| grep -v 'dagtest/suite\.go'` | Skip count **0**; scoped grep empty; `RunGraphSuite` green against the real `dag.Open`. **The plan's `grep -rn "t.Skip" internal/dag` returns nothing is wrong and must not be satisfied** — it matches Rule W-1's mandatory skip at `dagtest/suite.go:124` (which `TestRunGraphSuite_StubIsSkipped`, an SP-01 test, asserts must fire) plus four explanatory *comments*. Deleting them violates `00-ARCHITECTURE.md` D9/§5.22. Identical to V2-SP02-12 (§2.7a A) |
| V2-SP07-17 | Synthetic generator determinism | `go test ./internal/dag/dagtest/` | seed 7 produces an identical node/edge dump on two runs; the generated graph is acyclic |
| V2-SP07-18 | **Slice latency** (§6.4 "sub-millisecond") | `go test -bench 'Slice' ./internal/dag/` and `go test -run TestSliceLatencyBudget ./internal/dag/` | `BenchmarkBackwardSlice5000` and `BenchmarkForwardSlice5000` **< 1 ms/op** median of 20 on a 5 000-node / ~15 000-edge graph; `BuildToolUse` < 3 µs |
| V2-SP07-19 | Import purity | `go run ./tools/devtool lint` (`importgraph`) | `internal/dag` imports only `core`, `paths`, `config`, `logging` — in particular **not** `store`, **not** `symbols`, **not** `eval` |
| V2-SP07-20 | Coverage floor | `go run ./tools/devtool cover` **and** `go test -cover -count=1 ./internal/dag/` | `internal/dag` ≥ **85 %** (measured 90.9 % at SP-07's tip). **`devtool cover` does not check this until V2-MERGE-14 lands** — it prints `exempt (stub, owned by SP-07): dag` and exits 0 at any coverage, including 0 %, without ever testing whether the package is a stub. Read the second command's number, and treat a green `cover` alone as evidence of nothing (§2.7a A ②) |

### 2.8 Whole-tree gates that must be green before §4 begins

| ID | Gate | Command | Expected |
|---|---|---|---|
| V2-ALL-01 | Full suite, race | `go test -race ./...` | exit 0 (ubuntu, macos) |
| V2-ALL-02 | Full suite, Windows repeat | `go test -count=2 ./...` | exit 0 (windows dev machine and CI) |
| V2-ALL-03 | Local CI | `go run ./tools/devtool ci-local` | exit 0 end to end |
| V2-ALL-04 | CI on `verify/v2` | push the branch | `verify`, `test` (×3 OS), `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs` all green. `bench-gate` and `replay-gate` are **required checks from the end of wave 1 onward** (§8) — confirm the `continue-on-error` flag SP-01 left on them has been removed by SP-02/SP-05. Verify that removal mechanically via **V2-MERGE-04**, not from this job's colour: a job still carrying `continue-on-error: true` reports green unconditionally, so "all green" is evidence of nothing for exactly the two gates that matter most here. Note also that `nightly.yml` is **not** exercised by this push — its correctness is V2-MERGE-01/02 and nothing in the `verify/v2` CI run will reveal a broken fuzz matrix. **⚠ This row cannot run as written: the repository has no git remote** (SP-07 ACTION 3). Either add one before the checkpoint, or record the row as **environment-blocked** and say so in the completion report. Do **not** mark it passed on the strength of `ci-local` (V2-ALL-03), which runs neither the cross-OS matrix nor `crossbuild`, `security` or `docs`. Separately, "required check" is a **branch-protection setting on `develop`** that no working-tree edit can set — removing `continue-on-error` is necessary and not sufficient (§2.2a ⑤) |
| V2-ALL-05 | `Qompack.md` untouched by the whole wave | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | empty |
| V2-ALL-06 | **Plan documents reconciled with the code that shipped** | `git diff main..develop --name-only -- plans/` ; then read each named file | Every wave-1 plan edit is intentional and listed by name in the completion report (V2-MERGE-11). At minimum these are known and owed a decision: `plans/V2-SP-03-sketch-library.md` line 918's sampler rule is now wrong (§2.3a item 1) and its §11.6 forbidden-literal list omits `8000`; `plans/V2-SP-02-replay-harness-belady-baseline.md` carries a handoff banner; `plans/V2-SP-06-content-addressed-store.md` still contains the `'»'` rune-constant bug and the whole-input `continue` guard (§2.6a ⑫); `plans/V2-SP-07-dependence-dag-and-slicing.md`'s D-2 short NodeID prefixes contradict the frozen fixture. **Correct the plans, not the code** — in every one of these cases the ruling already went the other way |

---

## 3. Exit-criteria re-verification

Each completed subplan's exit criteria, quoted, with the concrete measurement procedure to run **now, on the integrated `develop`**. A criterion that a subplan proved on its own branch against stubs is not proven here until it is re-measured against the real siblings.

### 3.1 SP-01 (wave 0)

> SP-01 precedes Phase 0, so no phase exit criterion applies to it directly. The criteria it must **make measurable for later waves** … are:
> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.
> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.
> And the guardrails SP-01 must express as configuration and CI, verbatim from §11.3:
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Measurement.** SP-01's obligation was to make these measurable; wave 1 is where they become *measured*. Verify the machinery still exists and is now wired to real producers:

1. `go test -run TestBudgets_AllSixPresentAndConfigDriven ./internal/obs/` — B-A..B-F exist with config-driven limits (V2-SP01-13).
2. `go run ./tools/devtool cover` prints `exempt (stub, owned by SP-NN)` for **exactly** the still-stubbed packages and no longer exempts **any of the eleven wave-1 packages** — `eval`, `sketch`, `chunk`, `canon`, `symbols`, `ipc`, `daemon`, `contract`, `store`, `redact`, `dag` — all of which now have landed owners on `develop`, so their §6.4 floors bind. `cover` must fail if `plans/OWNERS.tsv` claims an owner for a package whose probe still returns `ErrNotImplemented`. **⚠ This item is the document's own statement of the gate and it is vacuous until V2-MERGE-14 lands** — on `develop` today `cover` exempts all eleven and exits 0, so item 2 reads as a check while testing nothing. It also named only six of the eleven; `symbols`, `redact`, `ipc`, `daemon` and `contract` were missing. See §2.7a A ②, which is where the whole-document scope of this hole is set out.
3. `go run ./tools/devtool replay --phase 0 --ci` enforces the 2 % rule and the Phase-0 exit criterion (§3.2 below). **Note the missing `--`:** the separator this line used to carry made Go's `flag` discard every argument after it, so the command replayed the default corpus with no phase check and no `--ci` and exited 0 (§2.2a ①). That defect appeared **twice** in this document — here and at V2-SP02-15 — and SP-02's handoff only corrected the other one. Any other `devtool replay --` in this file or in `.github/` is the same bug.
4. `go run ./tools/devtool bench-hotpath …` enforces B-A and B-E (§3.5, §5).
5. `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` — Appendix C still byte-exact after six branches touched config consumers.

### 3.2 SP-02 — Phase 0

> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.

> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

**Measurement procedure.**

```bash
# a) the single number, and that it is one number over >= 20 sessions
go run ./test/replay --corpus testdata/sessions/synthetic --baseline testdata/baseline/phase0.json --phase 0 --ci --json v2-replay.json
jq '.sessions, .corpusTier, .policies.stock.fraction_of_opt' v2-replay.json
# Expect: 24, "synthetic", a single float equal to the committed baseline.

# b) reproducibility across processes
go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline --to a.json
go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline --to b.json
diff a.json b.json      # Expect: identical, and both equal to testdata/baseline/phase0.json

# c) the ceiling and floor are real
go test -run 'TestOraclePolicy_ScoresExactlyOne\|TestSynthesize_EveryCompactionHasDemands' ./internal/eval/
# Expect: oracle == 1.0 on all 24; null == 0.0 on all 24; stock strictly between.

# d) the 2% rule is live in both directions
go test -run 'TestGate_TwoPercentBoundaryExclusive\|TestGate_LowerBetterMetricDirection\|TestGate_SignOff' ./test/replay/

# e) sublinear growth — now measurable from a REAL store (see §4.4)
go test -run TestIntegration_RealStoreGrowthIsSublinear ./test/integration/
```

**Honesty requirement.** §10 Phase 0 says "real sessions". The committed number is synthetic-corpus. Confirm `docs/adr/0002-replay-methodology.md` still states this explicitly, names the recorded-corpus command, and names who runs it and when. If that paragraph has been softened or removed, that is a checkpoint failure.

### 3.3 SP-03 — sketch library

SP-03 owns no phase exit criterion. The two it must not obstruct, quoted:

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization …); hook p99 < 15ms.

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents … The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

> - Hook p99 latency < 15ms (L0), < 2s (L4)

**Measurement procedure.**

1. **Contribution to the 15 ms budget**: `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` ⇒ ≤ 5 µs/op, 0 allocs; `go test -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` green. Record the ratio to 15 ms (target ≈ 0.03 %).
2. **Contribution to the 4:1 ratio**: `go test -run 'TestMinHash_OneNewFailure|TestMinHash_StraddlingTheSampleTargetIsContinuous|TestMinHash_BottomKMatchesTheSlowDefinition' -v ./internal/sketch/` ⇒ Jaccard ≥ 0.9 on the "same test suite, one new failure" fixture, **and** the estimate tracks the true Jaccard across the sample-target boundaries; then §4.2's `TestIntegration_StoreNearDupUsesRealMinHash` proves the store actually consumes it. **`TestMinHash_OneNewFailure` on its own is not evidence and must not be quoted as if it were.** It passed for the whole life of a defect that made near-duplicate detection fail across the entire 8–66 KB range, because its fixture is 200 lines and happens to sit below the sampler's first boundary; at 210 lines the same test measured 0.414. Whatever §4.2 builds, size its documents so at least one pair straddles a multiple of `MinHashSampleTarget` — a near-dup integration test whose inputs are all on one side of that boundary re-creates exactly the blind spot that hid this for eight reviews. See **Inbound from SP-03**, item 1.
3. **Contribution to zero stale-block incidents** (Phase 2, not achieved here — only *not obstructed*): `go test -run 'TestBloom_Rebuild\|TestSave_RefusesTriedBloom\|TestReplaceGenerational_' ./internal/sketch/` ⇒ rebuild from an arbitrary iterator at a different capacity works and `tried.bloom` can only be replaced generationally. Do **not** assert anything about `negknow` — it is SP-09.
4. **Format freeze**: `go test -run TestGolden ./internal/sketch/` without `-update`. Regenerating a fixture here to make it pass is the exact failure Rule W-2 forbids.

### 3.4 SP-04 — chunking, canonicalization, symbols

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.
>
> SP-04 owns **"measure with and without canonicalization"** … The `≥ 4:1` ratio and the `< 15ms` hook p99 are **SP-08**'s and **SP-05**'s to achieve and assert; SP-04 must not claim them.

> FastCDC over 100KB is well under 1ms

> boundary stability under insertion (inserting bytes at offset k perturbs at most 2 chunks after the insertion point); determinism across platforms and Go versions; `Min ≤ len ≤ Max` for every chunk except the last.

> `Canonicalize(Canonicalize(x)) == Canonicalize(x)`; `Restore(Canonicalize(x).Canonical, deltas) == x` whenever `KeepDeltas`; no canonicalizer ever *grows* its input.

**Measurement procedure.**

```bash
# with/without canonicalization, per group and overall
go test -v ./test/dedup/
jq '.groups[] | {group, ratio_without, ratio_with, gain}, .overall' testdata/canon-dedup-report.json
# Expect: testrunner gain >= 1.25; overall gain >= 1.0; fileread ratio_with > 1.0.
# Expect: TestDedupReport_Written reproduces the committed file byte-for-byte.

# FastCDC over 100KB
go test -bench BenchmarkSplit_100KB -benchmem ./internal/chunk/     # < 800 us/op

# boundary stability, with the distribution logged
go test -v -run 'TestPropBoundaryStability_(Insertion|Deletion)' ./internal/chunk/
# Record the novelty histogram. Thresholds: <=12 per trial (hard), <=2 in >=85%, <=3 in >=95%.

# cross-platform determinism (must be checked on all three CI OSes, no -update anywhere)
go test -run 'TestGearTableGolden|TestSplit_GoldenBoundaries' ./internal/chunk/

# the three normative canon properties
go test -run 'TestPropIdempotence_EveryCanonicalizer|TestPropNonGrowing_EveryCanonicalizer|TestPropRestoreIsExactInverse' ./internal/canon/
```

**Scope discipline.** SP-04 must not be marked as having achieved `≥ 4:1` or `< 15 ms`. Those are re-verified under §3.5 and §3.6 respectively; §4.1 is where SP-04's canonicalizers and SP-06's store are composed to produce the ratio for the first time.

### 3.5 SP-05 — daemon, IPC, hot path

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. … If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 …; hook p99 < 15ms. *(SP-05 owns and satisfies the second clause)*

> - Hook p99 latency < 15ms (L0), < 2s (L4)

> Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently.

> Everything is designed to degrade gracefully. If a hook stops firing, Qompack becomes a passive recorder and the session behaves exactly as it does today.

**Measurement procedure.**

```bash
# B-A / B-B / B-D / B-E, real process spawns, warm daemon.
# CRITICAL: this must now run with the REAL store, DAG and sketches resident (see §4.5 / §5).
go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v2-bench-hotpath.json
jq '.[] | {budget_id, n, p50, p95, p99, max, pass}' v2-bench-hotpath.json
# Expect: B-A p99 < 15ms pass:true ; B-B p99 < 2ms pass:true ; B-E p99 < 2s pass:true ;
#         B-D reported with pass omitted/false-gated; b_a_method and spawn_floor_ms present.

# the degradation doctrine, as an observable state machine
go test -run 'TestBreachDetector|TestHotModeTransitionWritesStateAndNAKs|TestSpoolOnBreachFalseDoesNotTransition' ./internal/daemon/
go test -run 'TestDegradedPassiveSuppressesActingPaths|TestDegradedPassiveStillRecords|TestModeOffSkipsIngest' ./internal/daemon/

# never fail silently
go test -run 'TestCriticalFailureDegrades|TestDegradeIsIdempotent|TestTwoCleanRunsRestore' ./internal/contract/

# a wave-1 build is FULL, not degraded
go test -v -run 'TestFreshBuildReportsModeFull|TestDeclaredProducerSetMatchesArchitecture' ./internal/contract/

# hooks never take the session down
go test -v -run TestHooksExitZeroUnderFaults ./test/e2e/     # 66/66
```

**B-E annotation.** Record B-E as *measured against the current `checkpoint` path, whose `Services.PreCompact` is nil at wave 1*. It is a real number for the code that exists; it is not yet a measurement of checkpoint finalization. Write that sentence into the completion report rather than implying otherwise.

### 3.6 SP-06 — content-addressed store

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization …); hook p99 < 15ms.

> - Store growth sublinear in session length after dedup

> **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint.

**Measurement procedure.**

```bash
# the ratio half, in-package (real chunker + real canon now that wave 1 is integrated)
go test -v -run 'TestPhase1ExitCriterion_ReadHeavy|TestStats_DedupRatio' ./internal/store/
# Expect: Stats.DedupRatio >= 4.0 on the read-heavy corpus.

# the ratio half, composed end to end across SP-03/SP-04/SP-06 (authored in §4.1)
go test -v -run TestIntegration_Phase1DedupRatioWithRealPipeline ./test/integration/

# sublinear growth, in-package and through SP-02's checker (§4.4)
go test -run TestStats_SublinearGrowth ./internal/store/
go test -v -run TestIntegration_RealStoreGrowthIsSublinear ./test/integration/

# the DPI guard
go test -v -run 'TestSegment_MarkEncodedRefusesDifferentSeq|TestSegment_MarkEncodedBatchIsAllOrNothing|PropMarkEncodedNeverDowngrades' ./internal/store/

# the hook-p99 half is NOT SP-06's; SP-06's bounded contribution is B-C
go test -bench 'BenchmarkPutBytes_100KB' -benchmem ./internal/store/    # cold <= 3ms, warm <= 400us
```

**W-2 re-verification (mandatory, this is the checkpoint's core job).** SP-06 developed against SP-01's synthetic `testdata/golden/contracts/{chunk,canon,symbols,sketch}/` fixtures. SP-04 regenerated the `canon` and `symbols` fixtures from the real implementations in its commit 7; SP-03 froze the `sketch` fixtures in its commit 6. Re-run SP-06's fixture-consuming tests against the **real** implementations:

```bash
go test -count=1 ./internal/store/...
```

Any SP-06 test that only passed against a placeholder fixture fails here. Per Rule W-2, **fix the test or the implementation — never the fixture** unless the fixture is provably the synthetic placeholder SP-01 shipped and the real owner (SP-03/SP-04/SP-07) has already replaced it on `develop`.

**Three §2.6a items land directly on this procedure and will otherwise be mis-read:**

- **The 4:1 numbers you are re-measuring were already 12.40 and 6.34 — pre-canonicalization** (§2.6a ⑤). SP-04's canonicalizers can only raise them. Expect an increase; a **decrease** after the wave-1 merges is a regression worth investigating, and "≥ 4.0, therefore pass" hides it.
- **`testdata/golden/store/*` will need `-update`, and that is expected** (§2.6a ⑦) — real signatures start being emitted, `"canon"` stops always equalling `"raw"`, and the real chunker changes the hashes. Confirm the diff is confined to those three axes first. A diff touching key order, key names or the `{"v":1,…}` record shape is **not** covered and is a real regression. The frozen contract fixtures under `testdata/golden/contracts/store/` must reproduce **unchanged**.
- **`BenchmarkPutBytes_100KB` warm is 227 µs against the 400 µs budget, but the redaction budget behind it is unreachable** (§2.6a ②). `Redact` over 100 KB is 0.64 ms with no secret, **8.2 ms** with a keyword that matches nothing, and **27.2 ms** with real secrets, against a stated 2 ms. Record all three; the 2 ms figure needs revising, not the code.

### 3.7 SP-07 — dependence DAG and slicing

> This is graph reachability: BFS over a few thousand nodes, sub-millisecond.

> Thin slicing drops control-dependence-only edges for much smaller slices at the cost of soundness — probably the right tradeoff here.

> Output: a relevance score per node, not a binary keep/drop — the score feeds submodular selection.

> `segment_coupling(p)` is the count of DAG edges crossing `p` — a direct, cheap measure of how much the post-`p` region depends on pre-`p` detail.

> Do not ship slicing or submodular selection before p-selection.

**Measurement procedure.**

```bash
# sub-millisecond, proven by a gate not an assertion
go test -bench 'BenchmarkBackwardSlice5000|BenchmarkForwardSlice5000|BenchmarkCrossingEdges' ./internal/dag/
go test -v -run 'TestSliceLatencyBudget|TestCrossingLatencyBudget' ./internal/dag/
# Expect: slices < 1ms median of 20; CrossingEdges < 5us median.

# the thin tradeoff, as numbers
go test -v -run TestThinVsFullComparison ./internal/dag/
# Expect: mean size_ratio <= 0.75, mean recall >= 0.85, precision >= full, ns_thin <= ns_full.

# scores, never keep/drop
go test -v -run TestNoBooleanKeepAPI ./internal/dag/

# segment_coupling correctness
go test -run 'TestCrossingEdgesGolden|TestCrossingEdgesBoundaries|PropCrossingEdgesMatchesBruteForce' ./internal/dag/

# the ship-order guard still holds with dag real and scheduler stubbed
go test -v -run 'TestGuard_SubmodularInertWithoutPSelection|TestGuard_SelectorRefusesWithoutPSelection' ./test/guards/
# Expect: analyzer.NewSelector still errors because scheduler.PSelectionAvailable() is false.
# dag shipping slice SCORES is legal (00-ARCH §5.12); a scattered keep-set driving a drop is not.
```

---

## 4. New cross-component integration tests

These tests **only make sense now**. Each exercises a seam that did not exist on any single wave-1 branch. They are authored during this checkpoint and **become part of the permanent suite** — they are committed to `verify/v2` and run in CI from here on.

**Location.** A new package `test/integration/` (a composition root, additive to the §3.1 `test/` grouping alongside `test/e2e`, `test/dedup`, `test/replay`, `test/bench/hotpath`). It lives outside `internal/` because it imports across the §3.2 allow-sets deliberately — e.g. `chunk` + `canon` + `store` + `dag` + `eval` in one file, which no `internal/` package is permitted to do. Add `test/integration` to `compositionRoots` in `tools/devtool/importrules.go` in the same commit, so `importgraph` keeps enforcing "nothing may import it".

**Conventions.** `testify/require`; `testutil.NewProject(t)` for every project root; `testutil.FakeClock` for anything time-dependent; no `time.Sleep`; every test deterministic and CI-safe.

### 4.1 Store ingest with the real chunker, canonicalizers and MinHash

The seam: `store.Deps{Chunker, Canon, Symbols, Redact, Tokens}` were stubs or fakes on SP-06's branch. This is the first execution of the real §8.1 item-1 pipeline.

**`TestIntegration_StorePutUsesRealChunkerAndCanon`**
*Setup.* `p := testutil.NewProject(t)`; `s, err := store.Open(p.Root, p.Cfg, store.Deps{Chunker: chunk.New(chunk.FromConfig(p.Cfg)), Canon: canon.Default(p.Cfg.Store.Canonicalize), Symbols: symbols.New(), Tokens: tokens.NewExact(p.Cfg, calib, cache), Redact: redact.New(p.Cfg), Clock: p.Clock})` — every dep real, none nil, none faked.
*Input.* `testdata/corpora/toolout/testrunner/go-test-pass.txt` with `PutOptions{Tool:"Bash", Canon: canon.OptionsFrom(p.Cfg.Store.Canonicalize, true)}`.
*Expected.* `PutResult.Root.CanonBytes < PutResult.Root.RawBytes` (canonicalization actually ran); every chunk length in `[1024, 16384]` except the last; `chunk.RootHash(chunks) == PutResult.Root.Hash`; `io.ReadAll(s.Open(ctx, root))` equals `canon.Default(...).Run("Bash","",raw,opts).Canonical` byte-for-byte. This last equality is the whole point: it pins the store's internal pipeline order against the canonicalizer's public output.

**`TestIntegration_CanonKeepRawRestoresThroughStore`**
*Setup.* Same store, `PutOptions{KeepRaw: true}`.
*Input.* `testdata/corpora/toolout/bash/npm-install.txt` (timestamps + ANSI + durations).
*Expected.* Read back the canonical bytes and the stored delta root; `canon.Restore(canonical, deltas)` reproduces the **redacted** input byte-for-byte. Asserts SP-04's `Restore` inverse and SP-06's delta-root encoding agree on the wire format.

**`TestIntegration_StoreNearDupUsesRealMinHash`**
*Setup.* Real deps, `store.canonicalize.minhash.enabled = true`, `nearDupThreshold = 0.9` from config.
*Input.* `go-test-pass.txt` then `go-test-rerun.txt` (same suite, one new failure) on the same `Path`.
*Expected.* Second `PutResult.NearDup != nil`, `Jaccard >= 0.9`, `PriorRoot` = the first root, and `NearDup.DeltaBytes < len(second canonical)/2`. This is the §8.1 "same test suite, one new failure" claim, proven across SP-03's MinHash, SP-04's canonicalizers and SP-06's store for the first time.

**`TestIntegration_Phase1DedupRatioWithRealPipeline`** — *the §10 Phase 1 exit criterion, composed.*
*Setup.* Real deps. Build a read-heavy session in-test from `testdata/corpora/toolout/sp06/fileread-auth-v1..v4.txt`: 10 synthetic file paths × 4 reads each, with a 2-line edit between reads 1→2, an 18-line edit between 2→3 and a 240-line edit between 3→4 (40 puts total).
*Expected.* `s.Stats(ctx).DedupRatio >= 4.0`. Run the same body twice — once with `canonicalize.enabled = true`, once `false` — and assert the enabled ratio is **strictly greater**, then write both numbers into the completion report. This is the composed form of "measure with and without canonicalization"; `test/dedup` measures it at the chunk level, this measures it at the store level.

**`TestIntegration_SecretsNeverSurviveTheRealPipeline`**
*Setup.* Real deps including the real canonicalizers (which rewrite bytes *after* redaction).
*Input.* A payload containing one instance of each of the ten built-in secret families, embedded in ANSI-coloured `npm install` output with timestamps.
*Expected.* Walk every file under `objects/`, zstd-decompress, and assert none of the ten literals appears. Then assert `canon` did not reconstruct a secret by joining across a placeholder boundary: no object contains a substring of any secret longer than 8 characters. §13 invariant 7 under canonicalization, which no single branch could test.

### 4.2 Hook event → daemon → store → tombstone → retrieval round-trip

The seam the parent checkpoint calls out. `internal/observer` is SP-08 (wave 2), so the wiring uses the extension seam SP-05 shipped for exactly this purpose: `daemon.Options.Bind(func(*Services))`. That is legitimate — it is the seam's first real exercise — and the test-local binding is replaced by `observer.OnToolUse` in V3.

**`TestIntegration_HookEventThroughDaemonToStore`**
*Setup.*
1. `p := testutil.NewProject(t)`; open the real `store`, real `dag.Graph`, real `daemon.SketchSet` (bloom/cms/hll).
2. `opts := daemon.NewOptions(p.Root, p.Cfg)`; set `Store`, `Graph`, `Sketches`; `opts.Bind(func(s *daemon.Services){ s.ObserveTool = testObserveTool })` where `testObserveTool` performs exactly the §8.1 items 1, 4, 5 that SP-08 will later own: `store.PutBytes` → `store.RecordToolUse` → `store.AppendFileVersion` → `dag.BuildToolUse` → `cms.Add(pathKey,1)` + `hll.Add(pathKey)`, then returns `hookio.Empty()`.
3. Start the daemon; send 200 `observe.tool` requests through the real `ipc.Client` (in-process server is not enough here — use `test/e2e`'s real-binary harness for at least 20 of them so the wire format is exercised).
*Expected.*
- 200 ACKs, zero spool lines, `wal-<sess>.ndjson` has 200 lines.
- After `Drain`, `store.Stats().ToolUses == 200`; `dag.Stats().Nodes` equals the builder's expected count; `hll.Cardinality()` within 7 % of the distinct path count; `cms.Estimate(hotPath)` ≥ the true count.
- `dag` remains acyclic (run the same DFS `TestBuilderOutputIsAcyclic` uses).

**`TestIntegration_TombstoneRoundTripThroughStore`** — *the retrieval round-trip.*
*Setup.* Continue from the store above. Pick a recorded `store.ToolUseRecord` for a `FileRead` of `src/auth.ts`.
*Input.* `marker := observer.Tombstone(rec)` (the pure function SP-01 implemented).
*Expected.*
1. `marker` matches `^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$` — the §8.1 item 2 form, with the real short hash.
2. Parse the hash out of the marker with `core.ParseHash` after re-expanding the short form via `store.ToolUse(rec.ID).Root` — assert `rec.Root.Short()` is exactly the 12 hex chars the marker carries.
3. **Full re-expansion:** `io.ReadAll(store.Open(ctx, rec.Root))` equals the canonicalized bytes originally ingested. The tombstone is therefore addressable, not decorative — G3.2, end to end.
4. **Minimal span (§8.7):** `symbols.New().Enclosing(path, full, offsetOfRefreshToken)` gives `(sym, true)`; `store.OpenSpan(ctx, rec.Root, int64(sym.Offset), int64(sym.Len))` returns exactly `full[sym.Offset : sym.Offset+sym.Len]`. This proves SP-04's symbol extractor and SP-06's span reader resolve the same boundaries — the seam `mcp.expand` will sit on in wave 3.
5. `store.Search(ctx, store.Query{Symbol:"refreshToken", K:5})` returns a `Hit` whose `Span` equals that symbol span and whose `Root` is `rec.Root`.

**`TestIntegration_SupersessionMarksEarlierReadThroughDAG`**
*Setup.* Ingest `fileread-auth-v1.txt` then `-v2.txt` on the same path through the same binding, passing `Supersedes` to `dag.BuildToolUse` and calling `store.MarkSuperseded`.
*Expected.* `store.ToolUse(older).Status == StatusSuperseded` and `SupersededBy` set; `dag.Out(dag.ToolUseNode(older))` contains an `EdgeSupersedes` to the newer node; a `BackwardSlice` from the newer tool use scores the older at `0.85 * 0.30 = 0.255`. §8.1 item 3 across `store` + `dag`, which neither package could assert alone (`store` must not import `dag`; `dag` imports neither).

**`TestIntegration_SpooledEventsSurviveToTheStore`**
*Setup.* Force `state.bin` to `hot=1` (spool submode). Send 50 `observe.tool` events through the real binary. Then flip back to `sync`, start the daemon, and let one idle tick drain.
*Expected.* Zero connects during the spool phase (daemon accept counter unchanged); 50 spool lines; after drain, `store.Stats().ToolUses == 50` and no duplicates (`TestNAKDuplicateIsDedupedOnDrain`'s dedup rule holds against a real store). Proves D4's degradation path loses freshness, never data — with a real L1 behind it.

### 4.3 Contract monitor against a real store

`internal/contract` may import `store` (§3.2). On SP-05's branch that was the stub.

**`TestIntegration_ContractMonitorRunsAgainstRealStore`**
*Setup.* `contract.NewMonitor(log, metrics, statePath)` with `contract.Env{ProjectRoot, Event, Cfg, Store: realStore, Log, Clock: fakeClock, History}`; register `contract.StandardAssertions()`.
*Expected.* `RunAll` returns `ModeFull`; exactly four results at `SevInfo`/`not-yet-implemented`; `transcript.readable` and `hook.payload_shape` evaluate against real data; no assertion touches a `store` method that returns `ErrNotImplemented` (assert by failing the test if any result's `Detail` contains `not implemented`).

**`TestIntegration_DegradedPassiveStillWritesToTheRealStore`**
*Setup.* Force the monitor to `ModeDegradedPassive`; drive 20 `observe.tool` events through the bound `ObserveTool`.
*Expected.* All 20 land in the real store with roots, tool-use records and file versions; **no** `Output.HookSpecificOutput` is emitted on any hook. This is §12.1's "L0 and L1 keep running … the store stays correct and the session's data is not lost", verified against the actual L1 rather than a stub.

### 4.4 Replay harness fed by real store statistics

SP-02 committed `testdata/golden/contracts/store/stats-growth.json` as a W-2 placeholder because no real store existed. It does now.

**`TestIntegration_RealStoreGrowthIsSublinear`**
*Setup.* Real store with real deps. Put a 100 KB payload mutated 1 % per iteration, 256 times, sampling `store.Stats()` at turns 8, 16, 32, 64, 128, 192, 224, 256 (8 samples, 32× span — satisfying `CheckSublinearGrowth`'s ≥ 6 samples and ≥ 8× span rules).
*Expected.* `eval.CheckSublinearGrowth(samples).Sublinear == true` with `Exponent < 1.0`; `Reason` empty. Then assert the committed placeholder is still *shape-compatible* with the real samples (same JSON field set, `dedupRatio` present and > 1) so the gate's provider seam is proven swappable without editing `internal/eval`.

**`TestIntegration_ReplayGateAcceptsRealGrowthFile`**
*Setup.* Write the real samples from the previous test to a temp file; run the gate driver with `--growth <file>`.
*Expected.* Exit 0. Then truncate to 3 samples and re-run: exit non-zero with a message naming "at least 6". Proves SP-02's §11.3 guardrail is wired to a real producer, not only to a fixture.

**`TestIntegration_RealBloomHealthFeedsTheFPCeiling`**
*Setup.* A real `sketch.Bloom(10_000, 0.01)` filled to capacity; map `bloom.Stats()` into `eval.SketchHealth{FillRatio, EstFPRate}`.
*Expected.* The gate's §11.4 bloom check passes at the real `EstFPRate` (≈ 0.01) and fails when fed a synthetic `0.11`, with the §11.4 sentence in the message. `negknow` is SP-09 — assert nothing about records, active/stale counts, or `already_tried`.

### 4.5 DAG, positions and the p-selection substrate

**`TestIntegration_BeladyPMinLandsAtLowCoupling`**
*Setup.* For each of the 24 synthetic corpus sessions: derive `eval.Blocks(s, at)` at each compaction turn; build a `dag.Graph` whose node `Pos` values are the block positions and whose edges follow `dag.BuildToolUse` from the session's tool calls; compute `eval.BeladyDetail(...)` to get `KeepSet.P`.
*Expected.* Across all compaction events, `dag.CrossingEdges(P)` is **strictly lower than the mean of `CrossingEdges` over 32 uniformly sampled positions** in the same session, in at least 70 % of events. This is the first empirical evidence that "cut where coupling is low" and "cut where OPT drops the earliest block" agree — the hypothesis SP-12's p-selection is built on. It is deliberately a *measurement with a floor*, not a proof; record the actual percentage in the completion report as the wave-1 baseline.
*Guard.* The test must not import `analyzer` or `scheduler`, and must not construct a keep-set from slice scores — that would violate closing note 3 and `TestNoBooleanKeepAPI`'s intent.

**`TestIntegration_DAGSliceRanksStoreRootsWithoutDroppingAnything`**
*Setup.* The store + DAG from §4.2. Take a `BackwardSlice` from the most recent tool-use node.
*Expected.* Every scored `NodeID` with a `tu:` prefix resolves through `store.ToolUse` to a real record (no dangling references between the two packages' key schemes); scores are in `(0,1]`; the returned value is `map[NodeID]float32`, never a keep-set. Pins the `dag.NodeID` ↔ `core.ToolUseID` ↔ `paths.Key` conventions across three packages that cannot import each other.

**`TestIntegration_SymbolNodesUseTheRealExtractor`**
*Setup.* Ingest a real TypeScript fixture; run `symbols.New().Extract` and feed the names into `dag.BuildToolUse{Symbols: names}`.
*Expected.* One `sy:<pathKey>#<name>` node per extracted symbol, deduplicated, edges appended in ascending name order; `store.Search(Query{Symbol: name})` finds the same root. `dag` never imports `symbols` (the caller resolves) — assert that the import-graph check still passes after this test exists.

### 4.6 The hot path with real resident state

**`TestIntegration_HotPathWarmWithRealResidentState`** (drives `test/bench/hotpath`, asserted rather than only reported)
*Setup.* Start a real daemon over a project pre-populated with **real** state: 2 000 tool-use records and 40 MB of raw tool output in the real store, a real DAG of ~5 000 nodes / ~15 000 edges, and the real sketch set (12 KB bloom, 54 KB CMS, 2 KB HLL) resident. Bind the §4.2 `ObserveTool`.
*Input.* 2 000 real process spawns of `qompack observe tool` with a representative payload.
*Expected.* **B-A p99 < 15 ms** and **B-B p99 < 2 ms**, and `hotPathMode` never transitions to `spool` during the run (assert `state.bin` still reports `hot=0` and no WARN transition line). Record B-D and `spawn_floor_ms`.
*Why it is new.* SP-05 measured this against stubs; §2.1 of `00-ARCHITECTURE.md` argues the *real* cold-state cost is what threatens the budget. This is the first honest measurement of the claim that the daemon removes it.

**`TestIntegration_HotPathDegradesRatherThanBlocks`**
*Setup.* Same warm daemon; inject an artificial 25 ms stall into the bound `ObserveTool` for three consecutive 512-sample windows.
*Expected.* The daemon flips to `spool`, clients stop connecting, every hook still exits 0, no event is lost after the next drain, and one WARN line plus the `status` payload record the transition. §8.1's "degrade to async queue-and-drain rather than blocking", proven with a real L1 under load.

### 4.7 Store, daemon and the append-only invariant under concurrency

**`TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites`**
*Setup.* Real daemon + real store + real DAG + real sketch set over one `.qompack/`; 8 concurrent sessions each driving 200 events, plus an idle tick running `store.GC` with a deadline and `dag.Compact`.
*Expected.* Under `-race`: no data race; `testutil.Project.AssertAppendOnly(t)` passes afterwards; `checkpoints/`, `pins/`, `sketches/tried.bloom` untouched; `dag/deps.jsonl` monotonically grows except for the single documented `Compact` rewrite, which bumps `Generation`; every `*.jsonl` still parses; `store.Open` on reopen reports the same `Stats` as before shutdown.

**`TestIntegration_GCNeverCollectsALiveRootUnderIngest`**
*Setup.* Run `store.GC` with a short deadline concurrently with ingest.
*Expected.* `GCReport.Truncated == true`; the mark phase restarts on live-digest mismatch; no root written during the run is ever collected; a subsequent unbounded GC completes and the union of reports equals a single unbounded run.

### 4.8 Test registration and CI wiring

In the same commit that adds `test/integration/`:

- add `"test/integration": true` to `compositionRoots` in `tools/devtool/importrules.go`;
- confirm `go run ./tools/devtool lint` still passes (`importgraph`, `testdeps`, `bindeps`, `sleepcheck`);
- confirm the `test` CI job picks the package up on all three OSes (`go test ./...` already covers it);
- record every new test name in the §8 completion report.

---

## 5. Performance budget validation

Every latency/size budget **in force at this point**. Budgets whose component does not exist yet are marked N/A and must be reported as N/A, not as a pass. Measure on one quiet machine; append results to `testdata/bench-baseline.txt` and diff with `benchstat`.

### 5.1 Normative latency budgets (`00-ARCHITECTURE.md` §2.4, `Qompack.md` §8.1 / §11.3)

| ID | Clock | Threshold | Command | Status at V2 |
|---|---|---|---|---|
| **B-A** | `hook_controlled`: client `main()` → `exit` | **p99 < 15 ms** | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json v2-bench.json` **with the real store/DAG/sketches resident** (§4.6) | **In force. Hard gate.** Must pass on ubuntu, macos, windows |
| **B-B** | `l0_ingest`: daemon read → WAL append returned | p99 < 2 ms | same run; plus `go test -bench BenchmarkIngestAccept ./internal/daemon/` | **In force. Hard gate** |
| **B-C** | `l0_process`: WAL → chunked, stored, DAG/sketches updated | p99 < 50 ms (**soft**) | measure through §4.2's binding: instrument the bound `ObserveTool` with an `obs.Histogram` and read its p99 after 2 000 events | **In force, soft.** Overrun ⇒ sampling/backpressure, never blocking. Record the number; do not gate |
| **B-D** | `hook_wall`: includes host process creation | reported, never gated | same bench run; `spawn_floor_ms` and `b_a_method` present | **Reported.** Gating it would be dishonest — it is the host's cost |
| **B-E** | `checkpoint_finalize`: `PreCompact` entry → exit | **p99 < 2 s** | same bench run, `--hook checkpoint` | **In force**, but currently exercises the client + daemon + nil-`Services.PreCompact` path. Annotate accordingly |
| **B-F** | `mcp_tool_call`: request → response | p95 < 250 ms | — | **N/A at V2.** `internal/mcp` is a stub (SP-13, wave 3). Report N/A |

> **B-A is not measured the way SP-05's plan document describes it, and restoring the plan's method fails this gate on every platform including bare metal.** Ruling #29 replaced "wall-clock hook spawn minus a constant floor" with the daemon's **TS-anchored `hook_controlled` estimate**, because B-A is defined in `obs/budgets.go` as `main()` entry → exit and therefore **excludes process creation** — and subtracting a constant removes the floor's *location* but none of its *dispersion*, contaminating exactly the p99 this row reads. Wall-clock survives as **`B-A_spawn_estimate`** with `limit_ms`/`pass` null, which is what B-D above reports. Confirm `b_a_method` in the JSON names the TS-anchored estimate; **a run whose `b_a_method` is the subtraction method is measuring the host's scheduler, not the hook** (§2.5a B).
>
> **This bench gate has never run on a CI runner.** SP-05 measured B-A 4.1 ms / B-B 0.7 ms / B-E 195 ms **on Windows only**, and the current warm-up shape (64-request hot tranche + `admin.ping` bulk, ruling #30) is newer than even those numbers. The three-platform requirement in the Status column is satisfied here for the first time — treat a first-run failure on ubuntu or macos as a first run, not as a regression the merge introduced, and see §2.5a C for the POSIX code paths that have **never executed on any machine**.

### 5.2 Size and ratio budgets (`Qompack.md` §10 Phase 1, §11.3)

| Budget | Threshold | Command | Status |
|---|---|---|---|
| **Store dedup ratio, read-heavy** | **≥ 4:1** | `go test -v -run 'TestPhase1ExitCriterion_ReadHeavy' ./internal/store/` and `go test -v -run TestIntegration_Phase1DedupRatioWithRealPipeline ./test/integration/` | **In force.** Both must report `DedupRatio ≥ 4.0` |
| **Canonicalization gain** | `testrunner` group `gain ≥ 1.25`; overall `gain ≥ 1.0` | `go test -v ./test/dedup/` ; `jq '.groups[], .overall' testdata/canon-dedup-report.json` | **In force.** "measure with and without canonicalization" |
| **Store growth sublinear** | `CheckSublinearGrowth(...).Sublinear == true`, `Exponent < 1.0` | `go test -run TestStats_SublinearGrowth ./internal/store/` ; `go test -v -run TestIntegration_RealStoreGrowthIsSublinear ./test/integration/` | **In force** |
| Sketch sizes match Appendix A | bloom 11 984 B body (m=95 872, k=7); CMS 54 380 B (2719×5); HLL 2 102 B frame | `go test -v -run 'AppendixASizing\|AppendixSizing' ./internal/sketch/` | **In force** |
| Bloom FP ceiling (§11.4) | measured FP ∈ [0.008, 0.013] at capacity; `Saturated()` at ≥ 0.10 | `go test -v -run 'TestBloom_EstimatedFPRateMatchesEmpirical\|TestBloom_SaturatedThreshold' ./internal/sketch/` | **In force** |
| Rehydration budget 8–12 K | — | — | **N/A.** `internal/rehydrate` is SP-11 |
| Checkpoint budget 12 K | — | — | **N/A.** `internal/checkpoint` is SP-10 |

### 5.3 Component micro-budgets (all in force; `benchstat` gated at >10 % warn / >25 % fail)

| Component | Budget | Command |
|---|---|---|
| `chunk.Split` 100 KB | < 800 µs/op | `go test -bench BenchmarkSplit_100KB -benchmem ./internal/chunk/` |
| gear scan 1 MiB | ≥ 400 MB/s | `go test -bench BenchmarkGearScan_1MiB ./internal/chunk/` |
| `chunk.Split` 1 MiB | ≥ 120 MB/s, ≤ 2 allocs/op | `go test -bench BenchmarkSplit_1MiB -benchmem ./internal/chunk/` |
| `SplitStream` 4 MiB | ≤ 40 ms/op | `go test -bench BenchmarkSplitStream_4MiB ./internal/chunk/` |
| `RootHash` 1 000 chunks | < 40 µs/op | `go test -bench BenchmarkRootHash_1000Chunks ./internal/chunk/` |
| `canon.Run` bash 100 KB | < 3 ms/op | `go test -bench BenchmarkRun_Bash100KB ./internal/canon/` |
| `canon.Run` go test output | < 1 ms/op | `go test -bench BenchmarkRun_GoTest ./internal/canon/` |
| `canon.Restore` 100 KB | < 1 ms/op | `go test -bench BenchmarkRestore_100KB ./internal/canon/` |
| `symbols.Extract` 100 KB | < 2 ms/op | `go test -bench BenchmarkExtract_100KB ./internal/symbols/` |
| `symbols.Enclosing` 100 KB | < 2 ms/op | `go test -bench BenchmarkEnclosing_100KB ./internal/symbols/` |
| `symbols.References` 100 KB / 50 names | < 1 ms/op | `go test -bench BenchmarkReferences_100KB_50Names ./internal/symbols/` |
| **`L0SketchUpdate`** (CMS+HLL+Bloom, the §8.1 item-5 share of B-A) | **≤ 5 µs/op, 0 allocs/op** | `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` + `go test -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` |
| Bloom/CMS/HLL `Add`/`Test`/`Estimate` | ≤ 1.0 µs/op, 0 allocs | `go test -bench 'BloomAdd\|BloomTest\|CMSAdd\|CMSEstimate\|HLLAdd' -benchmem ./internal/sketch/` |
| `HLL.Cardinality` | ≤ 25 µs/op | `go test -bench BenchmarkHLLCardinality ./internal/sketch/` |
| `MisraGries.Add` | ≤ 5 µs/op amortized | `go test -bench BenchmarkMisraGriesAdd ./internal/sketch/` |
| `MinHash` 4 KiB / 100 KiB | ≤ 1.5 ms / ≤ 2.5 ms (B-C, **not** B-A) | `go test -bench 'MinHash4KiB\|MinHash100KiB' ./internal/sketch/` |
| sketch marshal/unmarshal | bloom ≤ 60 µs, CMS ≤ 250 µs | `go test -bench 'Marshal\|Unmarshal' ./internal/sketch/` |
| `RebuildBloom` 5 000 keys | ≤ 15 ms/op | `go test -bench BenchmarkRebuildBloom5000 ./internal/sketch/` |
| `store.PutBytes` 100 KB cold / warm | ≤ 3 ms / ≤ 400 µs | `go test -bench 'PutBytes_100KB' -benchmem ./internal/store/` |
| `store.GetChunk` warm | ≤ 60 µs/op | `go test -bench BenchmarkGetChunk ./internal/store/` |
| `store.OpenSpan` 4 KB of 4 MB | ≤ 150 µs/op | `go test -bench BenchmarkOpenSpan_4KB_of_4MB ./internal/store/` |
| `store.Search` 1 000 roots / 8 MB | ≤ 25 ms/op | `go test -bench BenchmarkSearch_1000Roots ./internal/store/` |
| `store.Open` 50 000 roots | ≤ 400 ms/op | `go test -bench BenchmarkOpenStore_50kRoots ./internal/store/` |
| `store.MarkEncoded` 100 segments | ≤ 1 ms/op (inside B-E) | `go test -bench BenchmarkMarkEncoded_100 ./internal/store/` |
| `store.GC` 50 000 objects | ≤ 2 s/op, deadline ±50 ms | `go test -bench BenchmarkGC_50kObjects ./internal/store/` |
| `redact.Redact` 100 KB | ≤ 2 ms/op | `go test -bench . ./internal/redact/` |
| `tokens.EstimateRoot` 64 cached | ≤ 5 µs/op | `go test -bench BenchmarkEstimateRoot_64Cached ./internal/tokens/` |
| `dag.BackwardSlice` / `ForwardSlice` 5 000 nodes | **< 1 ms/op** median of 20 | `go test -bench 'Slice5000' ./internal/dag/` + `go test -run TestSliceLatencyBudget ./internal/dag/` |
| `dag.CrossingEdges` 15 000 edges | **< 5 µs** median | `go test -bench BenchmarkCrossingEdges ./internal/dag/` + `go test -run TestCrossingLatencyBudget ./internal/dag/` |
| `dag.BuildToolUse` | < 3 µs/op | `go test -bench BenchmarkAddToolUse ./internal/dag/` |
| `ipc.ReadState` | < 100 µs/op | `go test -bench BenchmarkReadState ./internal/ipc/` |
| `ipc` encode 4 KB request | < 5 µs/op | `go test -bench BenchmarkEncodeRequest ./internal/ipc/` |
| `ipc` server round trip | p99 < 2 ms | `go test -bench BenchmarkServerRoundTrip ./internal/ipc/` |
| `eval` E-2/E-3/E-4/E-5 | 250 ms / 50 ms / 20 ms / 15 ms per op | `go test -bench . ./internal/eval/` |
| `obs.Histogram.Observe` | < 100 ns/op | `go test -bench BenchmarkHistogram_Observe ./internal/obs/` |
| `config.Load` cold | < 2 ms/op | `go test -bench BenchmarkConfigLoad_ColdNoFiles ./internal/config/` |
| `paths.WriteAtomic` 4 KB | < 2 ms/op | `go test -bench BenchmarkPathsWriteAtomic_4KB ./internal/paths/` |

**Baseline discipline.** After every budget above has been measured on `verify/v2`:

```bash
go test -bench=. -benchmem -run '^$' ./... > v2-bench.txt
go run -modfile=tools/pinned/go.mod golang.org/x/perf/cmd/benchstat testdata/bench-baseline.txt v2-bench.txt
```

Any micro-benchmark >10 % worse than the committed baseline is a warning that must be explained in the completion report; >25 % worse fails the build (`00-ARCHITECTURE.md` §7). Update `testdata/bench-baseline.txt` in the final `verify/v2` commit so wave 2 has an integrated baseline rather than six per-branch ones.

> **V2-MERGE-03 must already have passed before you run the two commands above.** Five wave-1 branches append to `testdata/bench-baseline.txt` and the merges conflict on it by construction; a resolution that dropped one branch's rows leaves `benchstat` with no baseline for that package, which it reports as *nothing at all* rather than as a regression. Regenerating the file here then bakes the loss in permanently: the >10 %/>25 % gate for that package becomes a silent no-op and stays one through every later checkpoint. Confirm the `pkg:` inventory first, then regenerate.

> **Two facts about this table that the numbers above will not tell you.**
>
> **① `chunk`, `canon` and `symbols` have never had a baseline at all.** SP-04 committed no `testdata/bench-baseline.txt` rows, so eleven of the rows in this table — every `chunk`, `canon` and `symbols` budget — have nothing to `benchstat` against, and their >10 %/>25 % gate has been vacuous since the day it was written. This is not a merge loss to hunt for; there is nothing to lose. Measure them here and commit the rows.
>
> **② The comparison itself is not automated.** `benchstat` is pinned in `tools/pinned/` and named by a constant in `tools/devtool/util.go:26` that **nothing calls**; no `devtool` task and no CI job runs it, and `bench-gate` runs `bench-hotpath` only. `00-ARCHITECTURE.md` §7's rule therefore lives entirely in the two commands above, executed by hand, by whoever remembers. See **§2.3a item 3** — wiring it is work this checkpoint owns, not a note for later.
>
> **Known-noisy row:** `BenchmarkRun_Bash100KB` measures **±33 %** on the reference host (vs ±4 % for `RootHash_1000Chunks` and ±7 % for `References_100KB_50Names` in the same run), because it is one `bytes.Index` pass per rule over 100 KB and is memory-bandwidth bound. A ±33 % benchmark cannot be distinguished from a regression by a 25 % gate. Record its baseline from a quiet machine at `-count 10` or more, or exempt this one benchmark with the measured distribution as the justification — **do not tighten the code to chase the number** (SP04-D6).
>
> **Platform-sensitive rows:** several `store` budgets were measured on Windows and miss on syscall cost rather than algorithmically (`store.Search` 69.5 ms vs 25 ms, `GC_50kObjects` 2.42 s vs 2 s, `GetChunk` 154 µs vs 60 µs, `OpenStore_50kRoots` 404 ms vs 400 ms; the CPU profile is **90.9 % `runtime.cgocall`**). They are specified against the CI Linux runner — re-measure there before recording a miss (§2.6a ⑥). `MinHash` 100 KiB ≤ 2.5 ms is the tightest row on SP-03's branch and is likewise a Windows-laptop number (§2.3a, carried forward). `redact.Redact` 100 KB ≤ 2 ms is **unreachable by design** for two of three payload shapes (§2.6a ②).

---

## 6. Regression

### 6.1 Re-run all prior verification checkpoints' inventories

**V1 (wave 0, SP-01).** V1's inventory is re-run in full.

- If `plans/V1-VERIFY-foundation-and-contracts.md` exists, execute **its** "Cumulative functionality inventory" section verbatim, every row, before doing anything else in this checkpoint.
- If it does not exist in the tree, §2.1 above (`V2-SP01-01` … `V2-SP01-26`) **is** the V1 inventory, restated for this checkpoint, and executing §2.1 discharges the obligation.

Either way, the following V1-era invariants must hold unchanged after six branches landed on top of them, and each is a regression if it does not:

| V1 invariant | Command | Expected |
|---|---|---|
| Appendix C reproduced verbatim | `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` | green |
| Append-only guard has teeth | `go test -run TestAppendOnlyGuard ./internal/paths/` | all five illegal writes fail |
| Hooks always exit 0 | `go test -run TestDispatch_HookAlwaysExitsZero ./internal/cli/` + `go test -run TestHooksExitZeroUnderFaults ./test/e2e/` | 30/30 and 66/66 |
| Fresh build reports `ModeFull` | `go test -run TestGuard_FreshBuildReportsModeFull ./test/guards/` | green, four `not-yet-implemented` |
| Four closing-note guards | `go test ./test/guards/` | all green; submodular still inert |
| No network / confined write set | `go test -run 'TestGuard_NoNetworkImports\|TestGuard_WriteSetConfinedToQompack' ./test/guards/` | green |
| Plugin bundle never drifts | `go run ./tools/devtool plugin-validate && git diff --exit-code -- plugin/` | clean |
| Config docs never drift | `go run ./tools/devtool gen-config-docs --check && git diff --exit-code -- docs/config-reference.md` | clean |
| Import graph still a DAG | `go run ./tools/devtool lint` | `importgraph` clean with six real packages + `test/integration` |
| Shipped binary dependency closure | `go run ./tools/devtool lint` (`bindeps`, all six GOOS/GOARCH) | only stdlib, `qompack/…`, `klauspost/compress`, `Microsoft/go-winio` |
| `Qompack.md` unmodified | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | empty |

**There is no V2 predecessor other than V1.** V2 is the second checkpoint; wave 1 is the first parallel wave.

### 6.2 The 2 % no-regression guardrail (§11.3)

> No metric may regress by more than 2% to improve another without explicit sign-off

Enforcement at this checkpoint:

```bash
# 1) the replay gate applies the rule to every metric in MetricsOf, per policy
#    NOTE: no "--" separator. It made Go's flag package discard every flag after
#    it, so this command replayed the DEFAULT corpus with no phase check, no --ci
#    and no baseline, and exited 0 with plausible-looking output. See 2.2a (1).
#    The driver now refuses leftover arguments with exit 2.
go run ./tools/devtool replay \
  --corpus testdata/sessions/synthetic \
  --baseline testdata/baseline/phase0.json \
  --phase 0 --ci --json v2-replay.json
# Expect exit 0 and "regressions": [] .

# 2) prove the rule is live rather than vacuous, both sides of the boundary
go test -v -run 'TestGate_TwoPercentBoundaryExclusive|TestGate_ImprovementNeverRegresses|TestGate_LowerBetterMetricDirection|TestGate_ZeroBaselineUsesAbsoluteTolerance' ./test/replay/

# 3) the sign-off escape hatch is narrow and named
go test -v -run 'TestGate_SignOffAllowsNamedMetricOnly|TestGate_SignOffRejectsShortReason' ./test/replay/
```

Rules for this checkpoint specifically:

- **A regression discovered here is fixed, not signed off.** The sign-off trailer exists for a deliberate trade in a feature PR. A verification checkpoint that signs off its own regression has defeated its purpose. If a metric genuinely must move, the sign-off goes on the wave-2 PR that trades it, with a reason naming that exact metric.
- **The baseline is not rewritten to make the gate pass.** `testdata/baseline/phase0.json` is regenerated only if the *corpus* changed, and a corpus change at V2 is out of scope. `TestGate_CorpusStaleness` and `TestSynthesize_MatchesCommittedCorpus` both catch this.
- **Benchmarks obey the same spirit** through `benchstat`: >10 % warns (explain in the report), >25 % fails (fix on `verify/v2`).
- Record, in the completion report, the delta of every §11.2 metric against `testdata/baseline/phase0.json` even when it is zero. "No change" measured is worth more than "no change" assumed — that is `Qompack.md` §1.3 RC-3 applied to our own process.

---

## 7. Failure protocol

Any failing row in §2, any unmet criterion in §3, any failing test in §4, any breached budget in §5, any regression in §6 triggers this protocol. There is no "note it and move on".

### 7.1 Systematic diagnosis (do this before writing a fix)

1. **Reproduce deterministically.** Re-run the exact failing command with `-count=1`. If it is flaky, that is itself the bug — flakiness at an integration seam is a real defect, not noise. Fix the nondeterminism (usually: a wall-clock read that should be `testutil.FakeClock`, a map-iteration order, or an unsynchronized shared sketch).
2. **Localize to a seam or a package.** Ask: does the failure reproduce with a *stub* on the other side of the seam? If yes, the defect is inside one package and belongs to that subplan's code. If no, it is a seam defect — the two implementations disagree about a contract in `00-ARCHITECTURE.md` §5.
3. **Classify.** Exactly one of:
   - **(a) Implementation bug** in a wave-1 package → fix the implementation.
   - **(b) Test/assertion bug** — the test asserts something the design never promised → fix the test, and quote the design sentence that justifies the change in the commit body.
   - **(c) W-2 fixture divergence** → per Rule W-2, *"any fixture that the real implementation cannot reproduce is a verification failure, not a fixture bug."* The default is to fix the implementation. Regenerating a fixture is permitted **only** when the fixture is provably SP-01's synthetic placeholder and the owning subplan has already replaced it on `develop`; say so explicitly in the commit body and name the owning subplan.
   - **(d) Interface defect** — §5 of `00-ARCHITECTURE.md` is genuinely wrong. Do **not** work around it. Open `arch/<short-reason>` off `develop`, amend §5, merge it, rebase `verify/v2`, then fix. This is §0's amendment rule; silent divergence is the one failure mode that makes parallel waves worthless.
   - **(e) Budget breach** → profile before optimizing. Attribute the cost (`go test -bench -cpuprofile`, the daemon's per-stage histograms, `b_a_method`/`spawn_floor_ms` from the bench artifact). A budget met by removing work that the design requires is a failure disguised as a pass.
4. **Never weaken the check.** Do not add `//nolint`, do not add `t.Skip`, do not add a `//nomagic:allow`, do not lower a threshold, do not regenerate a golden to match broken output, do not delete an assertion. If a threshold is genuinely wrong, that is class (d) and needs an amendment commit with the measurement that justifies it.
5. **Write the failing test first if one does not exist.** If a defect slipped through wave 1, the reason is a missing test. Add it to the permanent suite (in the owning package, or in `test/integration/` if it is a seam), observe it fail, then fix.

### 7.2 Fixing

- All fixes are committed to **`verify/v2`**. Never to `develop` directly, never to a merged `feat/sp0N-*` branch, never as an amended wave-1 commit.
- **Small conventional commits, as many as needed.** There is no commit-count budget on a verification branch — the 5–8 rule is a subplan rule. One coherent fix per commit. Format per `00-ARCHITECTURE.md` §10: `<type>(<scope>): <subject>`, body explaining the decision rather than the diff, footer `Refs: V2, SP-NN, §<sections>`. Example:

  ```
  fix(store): use canon output length for RawBytes on a dedup hit

  A put whose canonical form matches an existing root reported the prior
  put's RawBytes, which understated Stats.DedupRatio by the difference in
  volatile bytes between the two reads. Discovered at V2 by
  TestIntegration_Phase1DedupRatioWithRealPipeline, which is the first test
  to run the real canonicalizers against the real store.

  Refs: V2, SP-06, SP-04, §10 Phase 1, §8.2
  ```

- **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

  That rule is verbatim and absolute. It applies to every commit, merge commit, tag message and PR body on this branch. CI's `verify` job greps for `Co-Authored-By`, `Signed-off-by`, `Generated with` and `🤖` in the commit range and fails the build if any appear.

- Each fix commit compiles and passes `go run ./tools/devtool test` for the packages it touches before it is made.

### 7.2a Fan the fix work out — one agent per disjoint file set

This checkpoint arrives with a **known inventory of work**, not just whatever §2 turns up: twenty-four `V2-MERGE-*` rows, six `SP04-D*` carried defects, SP-07's three ACTIONs, and the inbound items in §2.3a / §2.2a / §2.5a / §2.6a / §2.7a. Doing that serially wastes most of the wall clock, because the packages are independent — which is exactly why wave 1 could be built in parallel in the first place.

**The constraint that makes this safe is file ownership, not topic.** §2.0a already computed which files more than one branch touched, and the same map says which fix agents may run at the same time: **two agents must never hold the same file.** Assign by path, not by subject; an agent that needs a file it does not own reports it and stops rather than editing it.

**Serial prologue — main session, before any fan-out.** These cannot be parallelized and several destroy their own evidence if run late:

1. **V2-MERGE-20** — secure SP-05's untracked ledger. Do this before `verify/v2` is even cut.
2. **V2-MERGE-03, -06, -10** — record the bench-baseline inventory, the MANIFEST reconciliation and the per-branch allowlist audit. §5 and §7 overwrite all three.
3. **V2-MERGE-14** — merge the two `tools/devtool/cover.go` rewrites and set `landedSubplans` to SP-01…SP-07. **Every coverage row in the document is vacuous until this lands** (§2.7a A ②), so doing it first turns eleven dead rows into live ones before any agent measures against them.
   *The wave-1 merge performed this incrementally — each subplan added itself to `landedSubplans` in its own merge, so the set should already read SP-01…SP-07 when you arrive. Confirm it rather than assume it: run `go run ./tools/devtool cover` and require that **no** `exempt (stub, owned by SP-0[2-7])` line appears. If one does, the merge dropped a half and this step is still live work.*
4. **V2-MERGE-12 and the `redact` / `tokens` floor decision** — `plans/OWNERS.tsv` is settled here, in the main session, and **no fan-out agent owns it**. It has to be serial for the same reason step 3 does: a floor edited while agents are measuring against it makes every number they report unattributable. Reconcile the owner/probe columns against reality, and settle the one open contradiction — the declared floors for `redact` and `tokens` are 75 while §2.6 claims 90 — by either raising OWNERS.tsv or restating V2-SP06-27 at 75. Record which, and why, in the completion report. **F-8 handles plan prose only and stays out of this file.**

**Then fan out.** Each package below owns its files exclusively for the duration:

| Agent | Owns (exclusive) | Closes |
|---|---|---|
| **F-1 toolchain & CI** | `tools/devtool/importrules.go`, `tools/devtool/importgraph.go`, `tools/devtool/importgraph_test.go`, `tools/devtool/test.go`, `tools/devtool/fmt.go`, `.github/workflows/ci.yml`, `.github/workflows/nightly.yml`, `test/guards/nightlyfuzz_test.go` | V2-MERGE-01, -02, -04, -05, **-22** (extend `importgraph` to `.TestImports`/`.XTestImports`), **-24** (decide whether `fmt-check` joins `devtool test`); §2.3a item 3 (wire `benchstat` into a `devtool` task and `bench-gate`) |
| **F-2 guards & contract fixtures** | `test/guards/stubs_test.go`, `test/guards/v1_integration_test.go`, `test/guards/carrieddefects_test.go`, `internal/testutil/fixtures_test.go`, `testdata/golden/contracts/**` | V2-MERGE-07, -15, -16, -18, -19, **-23** (make the carried-defects guard distinguish a failed listing from an absent test); SP-07 **ACTION 1** — which spans `contracts/dag/MANIFEST.json` *and* `fixtures_test.go` and **must be one commit**, which is why both files sit with one agent |
| **F-3 chunk / canon / symbols** | `internal/chunk`, `internal/canon`, `internal/symbols`, `testdata/golden/canon/**`, `testdata/corpora/toolout/**` | SP04-D1, D2 (a decision, not a patch), D3, D5, D6 |
| **F-4 store / redact / tokens** | `internal/store`, `internal/redact`, `internal/tokens`, `testdata/golden/store/**` | §2.6a ⑤ ⑦ (the `-update` confined to three axes), the `Redact` budget revision from ② |
| **F-5 dag** | `internal/dag`, `docs/adr/0007-*.md` | §2.7a B row corrections; **not** `testdata/golden/contracts/dag/` — that is F-2's, per ACTION 1 |
| **F-6 eval / replay** | `internal/eval`, `test/replay`, `testdata/sessions/**`, `testdata/baseline/**` | §2.2a corrections; the recorded-tier gap |
| **F-7 daemon / ipc / contract** | `internal/ipc`, `internal/daemon`, `internal/contract`, `internal/cli` | §2.5a D (a structural guard so nothing outside `internal/contract` writes `LastSessionID`), §2.5a G's untested FR-4 `Serve`-failure arm |
| **F-8 plan reconciliation** | `plans/V2-SP-03-*.md`, `plans/V2-SP-06-*.md`, `plans/V2-SP-07-*.md`, `plans/V2-SP-05-*.md` | **V2-ALL-06.** Documents only, no code — so it runs alongside everything else with zero contention |

**Three conflicts are structural and are resolved above rather than left to discover:** F-1 and F-2 both work inside `test/guards/`, so ownership is stated **per file** — F-1 has `nightlyfuzz_test.go`, F-2 has the rest. F-2 and F-5 both have a claim on the dag fixtures; ACTION 1 requires its two edits in one commit, so F-2 takes both and F-5 stays out. And F-1 holds five files in `tools/devtool/` but **not** `cover.go`, which the serial prologue settles before any agent starts — F-1 arrives after that and must not revisit it.

**§4's seven integration tests fan out the same way** — §4.1 … §4.7 author seven separate files under `test/integration/`, with only §4.8 (registration and CI wiring) serial at the end. Run them as seven agents, then do §4.8 in the main session.

**Rules for fix agents**, which differ from the §2 verification rules because these agents *do* write:

- An agent that needs to edit a file it does not own **stops and reports**. It does not edit it, and it does not "just add one line".
- §7.1's classification still binds: no `//nolint`, no `t.Skip`, no `//nomagic:allow`, no lowered threshold, no golden regenerated to match broken output. An agent that believes a threshold is wrong reports it as class (d) — an architecture amendment — and stops.
- Each agent commits its own work to `verify/v2` with its own conventional commits, and runs `go run ./tools/devtool test` for the packages it touched before each commit.
- **Rebase, do not merge, between agents.** `verify/v2` stays linear; a fix branch per agent that merges back with `--no-ff` would make the §6 regression comparison harder to read for no benefit.
- After the last agent lands, §7.3 applies: **re-run the whole checkpoint from §2 row 1**, not just the rows each agent touched.

### 7.3 Re-run the whole checkpoint from the top

After **any** fix — even a one-line one — **re-run this checkpoint from §2 row 1.** Not just the failing row, not just the affected package. Wave 1's whole risk profile is composition: a fix in `canon` moves chunk boundaries, which moves dedup ratios, which moves store size, which moves B-C, which can move B-A. A partial re-run cannot see that.

Practically: re-dispatch all seven subagents against the fixed tree, then re-run §4, §5 and §6 in the main session. Record how many full passes it took in the completion report.

### 7.4 The gate

> **No wave-2 branch is cut until this checkpoint is fully green.**

`feat/sp08-observer-l0` and `feat/sp09-negative-knowledge` do not exist until every row of §2, every criterion of §3, every test of §4, every budget of §5 and every item of §6 passes on `verify/v2` **and** CI is green on the branch across `verify`, `test` (×3 OS), `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`.

A partially-green checkpoint is not a checkpoint. If something cannot be fixed, it is class (d) and needs an architecture amendment, not a waiver.

### 7.5 Merge and tag

Once fully green:

```bash
go run ./tools/devtool ci-local                 # final full local gate
git log --format=%B develop..verify/v2 | grep -Ei 'co-authored-by|signed-off-by|generated with|🤖'   # no output
git checkout develop
git merge --no-ff verify/v2 -m "chore(verify): V2 — wave-1 verification checkpoint

Re-verified every functionality delivered by SP-01..SP-07 on the integrated
develop, added the cross-component integration suite under test/integration,
and re-measured every budget in force with the real store, DAG and sketches
resident rather than against stubs.

Refs: V2, SP-01, SP-02, SP-03, SP-04, SP-05, SP-06, SP-07"
git tag v0.1.0
git push origin develop --tags
```

Then, and only then, cut wave 2 from the post-verification `develop`:

```bash
git checkout -b feat/sp08-observer-l0        # and, separately, feat/sp09-negative-knowledge
```

---

## 8. Completion report template

Fill this in and paste it as the checkpoint's output. Every row gets a verdict. `N/A` is a legitimate verdict only where this document already marks it so; anything else must be `PASS` or `FAIL`.

````markdown
# V2 completion report

- Branch: verify/v2 (cut from develop @ <sha>)
- develop head at start: <sha>   | verify/v2 head at end: <sha>
- Wave-1 merge order confirmed (`plans/README.md` step 3): SP-05 → SP-03 → SP-04 → SP-02 → SP-06 → SP-07  [ ]
- `TestGuard_Phase0BeforeStore` green at **every** wave-1 merge commit, not only the last: <n>/11 first-parent commits PASS (green at the merge; re-confirm, and say so if the number moved)  [ ]
- Conflicts resolved on the incoming branch, never in the merge commit (00-ARCHITECTURE "Branching")  [ ]
- Full checkpoint passes required: <n>
- Fix commits on verify/v2: <n>   (list below)
- Platforms measured: ubuntu-latest / macos-latest / windows-11-dev
- Date: <ISO 8601>

## 0. Merge integrity (§2.0 — recorded before the fan-out ran)
| ID | Gate | Verdict | Evidence / note |
|---|---|---|---|
| V2-MERGE-01 | Nightly fuzz matrix reconciles both directions | | orphan matrix entries: <n>; unregistered `Fuzz*` in tree: <n>; both must be 0 |
| V2-MERGE-02 | `TestNightlyFuzzMatrix` green with matching arity | | matrix entry count before → after: <n> → <n>; `require.Len` updated: [ ] |
| V2-MERGE-03 | bench-baseline `pkg:` blocks intact **(recorded before §5 regenerated the file)** | | packages present: <list>; missing: <list, must be empty> |
| V2-MERGE-04 | `bench-gate` + `replay-gate` present, neither `continue-on-error` | | |
| V2-MERGE-05 | `importrules.go` covers every landed package + all three new composition roots (`test/replay`, `test/dedup`, `test/bench/hotpath`); `internal/sketch` still `{core,paths,logging}` | | |
| V2-MERGE-06 | Contract MANIFESTs match bytes tree-wide | | manifests checked: <n>; mismatches: <n> |
| V2-MERGE-07 | Sketch W-2 fixtures exist **and** are consumed by canon + store | | consuming tests: <names> |
| V2-MERGE-08 | Every §5.x signature verbatim after six parallel copies | | drifted signatures: <list, must be empty> |
| V2-MERGE-09 | `//nomagic:allow` inventory traceable to a subplan | | annotations found: <n>; unauthorized: <n> |
| V2-MERGE-10 | Per-branch out-of-scope allowlists respected | | files touched by >1 branch: <list> |
| V2-MERGE-11 | `Qompack.md` untouched; `plans/` changes accounted for | | |
| V2-MERGE-12 | `OWNERS.tsv` agrees with `cover` and the fuzz guard | | |
| V2-MERGE-13 | §6.4 coverage floors run for all eleven non-SP-01 wave-1 packages | | per-package actual vs floor: |
| V2-MERGE-14 | `cover.go` keeps **both** rewrites; `landedSubplans` = SP-01…SP-07; `probeBlind` still exactly 4 | | `TestLandedSubplansMatchesTheBranch` rewritten: [ ] |
| V2-MERGE-15 | `fixtures_test.go` frozen count = **33** (34 if SP-07 ACTION 1 taken), pending 5 (or 4) | | value found before fix: <n> |
| V2-MERGE-16 | `stubs_test.go` + `v1_integration_test.go`: every wave-1 package marked `allMethodsAreReal`; probe-aware assertions intact | | packages missing the marker: <list, must be empty> |
| V2-MERGE-17 | `eval`, `daemon`, `self-test` all registered; neither `daemon` nor `self-test` left in `notImplemented` | | |
| V2-MERGE-18 | No undeclared files under `testdata/golden/contracts/**` | | surplus files: <list>; disposition chosen: declare / relocate |
| V2-MERGE-19 | `CARRIED-DEFECTS.tsv` covers the wave, or the decision not to is recorded | | rows by `opened_by`: <counts> |
| V2-MERGE-20 | **(run first)** SP-05's untracked rulings ledger secured before `verify/v2` was cut | | where it was copied to: <path>; other untracked records found: <list> |
| V2-MERGE-21 | `plans/README.md` and this file agree on the wave-1 merge order, `develop` matches it, and the per-commit walk is green at **every** first-parent commit — not only the tip | | first-parent order observed: <list>; incoming-branch resolution commits present: <n>/6; walk result: <n>/11 PASS; counterfactual re-run and confirmed red: [ ] |
| V2-MERGE-22 | No non-root package's `_test.go` imports a composition root; `importgraph` now checks it | | packages still importing one: <list>; importgraph extended: [ ] |
| V2-MERGE-23 | The carried-defects guard distinguishes "listing failed" from "test absent" | | verified by breaking a package deliberately: [ ] |
| V2-MERGE-24 | `devtool fmt-check` (gofumpt, not gofmt) exits 0; all six wave-1 tips walked | | tips red at their final commit: <list, SP-04 known>; `fmt-check` added to `devtool test`: yes / no + why |

**Coverage floors — before / after.** V2-MERGE-13 starts red by construction. Record each of the eleven wave-1 packages' measured coverage against its `plans/OWNERS.tsv` floor — `eval` 85, `sketch` 90, `chunk` 90, `canon` 90, `symbols` 75, `ipc` 75, `daemon` 75, `contract` 75, `store` 90, `redact` 75, `dag` 85 — and what `cover.go` was changed to. A green `devtool cover` that still prints `exempt (stub, …)` for any of them is the defect, not the fix. Record separately whether `redact` and `tokens` were raised to 90 in OWNERS.tsv or V2-SP06-27 was restated at 75 (their declared floors are 75 while §2.6 claims 90).

**Nightly fuzz matrix — before / after.** Record the full `(pkg, fn)` table as it stood on `develop` and as it stands on `verify/v2`, which of the three orphan rows each fix closed, and which of the fifteen unregistered targets gained a row. A repair that renamed rows without adding one for `internal/symbols` is incomplete.

**Cross-branch collisions — resolution log.** One line per file in §2.0a's ten-row table: which branches' halves are present in the merged result, and how you confirmed it. `--ours`/`--theirs` anywhere in this list is a finding, not a resolution.

**Inbound items — disposition.** One line per numbered item in §2.3a (SP-03, 7 items + carried-forward), §2.2a (SP-02, 7 items), §2.5a (SP-05, 4 items), §2.6a (SP-06, 12 items) and §2.7a (SP-07, A/B/C/D). State for each: **fixed here**, **accepted as-is** (a divergence the code is right about), or **carried to <checkpoint>**. An item with no line is an item nobody decided about.

**Silently-disabled gates found.** List every gate that was reporting green while not actually gating (dead fuzz targets, `continue-on-error` restored by a merge, a `benchstat` baseline with no rows for a package, an `importgraph` rule lost in a conflict, a coverage floor exempted for a landed package, a `-run` pattern matching no test, a `grep … t.Skip` row that can never be satisfied). This list is the single most valuable output of this checkpoint for wave 2: each entry is a guard that six branches trusted and none of them owned. **Four independent sessions found the coverage-floor hole and three found the `t.Skip` grep hole** — the pattern to carry forward is that a check nobody owns is a check nobody fixes.

**Carried to wave 2 and beyond.** Explicitly restate, in `V3-VERIFY-observer-and-negative-knowledge.md` and wherever else they land: SP-07's **INHERIT** (the graph contains legitimate cycles; SP-09's detector must terminate on them), SP-02's **empty recorded corpus tier** (§6.3 tier 2 gates releases and has never been exercised), SP-03's **`RunCMSSuite` cannot discriminate a max-estimator** (SP-16 inherits a suite that would pass a wrong warm-start), and SP-06's **`fsck` residual** (a crash can leave a root line without its object; repair is SP-17's).

## 1. Inventory — SP-01 foundation
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP01-01 | Repo shape and root commit | | |
| V2-SP01-02 | build + vet | | |
| V2-SP01-03 | devtool lint (7 sub-checks) | | |
| V2-SP01-04 | fmt-check | | |
| V2-SP01-05 | internal/core | | |
| V2-SP01-06 | internal/paths | | |
| V2-SP01-07 | Append-only guard | | 5/5 illegal writes rejected: |
| V2-SP01-08 | Appendix C verbatim | | |
| V2-SP01-09 | Config precedence / merge / env / null | | |
| V2-SP01-10 | Config fallback-not-crash + validation table | | |
| V2-SP01-11 | Config schema + docs no-drift | | |
| V2-SP01-12 | logging Loud channel | | |
| V2-SP01-13 | obs histograms + B-A..B-F | | |
| V2-SP01-14 | hookio seven payloads | | |
| V2-SP01-15 | Hooks exit 0 (30 faults) | | 30/30: |
| V2-SP01-16 | plugin-validate no-drift | | |
| V2-SP01-17 | build-all six targets | | |
| V2-SP01-18 | W-1 skip message discipline | | |
| V2-SP01-19 | Zero skips in wave-0/1 packages | | |
| V2-SP01-20 | Closing-note build-order guards | | |
| V2-SP01-21 | No network / write set | | |
| V2-SP01-22 | e2e six hooks | | |
| V2-SP01-23 | tokens baseline preserved | | 6/6 SP-01 tests unmodified: |
| V2-SP01-24 | Pure functions (RootHash/YoungDaly/SkiRental/…) | | |
| V2-SP01-25 | SP-01 benchmark budgets | | ns/op: |
| V2-SP01-26 | commit-msg policy machinery | | |

## 2. Inventory — SP-02 replay / Belady / baseline
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP02-01 | package -race | | |
| V2-SP02-02 | Blocks / Demands | | |
| V2-SP02-03 | Belady OPT | | |
| V2-SP02-04 | Breakpoint OPT + disclaimer | | |
| V2-SP02-05 | Policy registry | | PolicyNames(): |
| V2-SP02-06 | Replay determinism | | |
| V2-SP02-07 | Divergence metrics | | |
| V2-SP02-08 | Fraction-of-OPT + §11.2 metrics | | |
| V2-SP02-09 | Synthesizer + 24-session corpus | | |
| V2-SP02-10 | Redacting importer | | |
| V2-SP02-11 | Sublinear-growth checker | | |
| V2-SP02-12 | evaltest suite, zero skips | | |
| V2-SP02-13 | Replay gate (19 rows) | | |
| V2-SP02-14 | Baseline reproducible | | byte-identical across runs: |
| V2-SP02-15 | Gate end to end | | stock=<x> null=<0.0> oracle=<1.0> |
| V2-SP02-16 | Honesty tags in the report | | |
| V2-SP02-17 | eval import purity | | |
| V2-SP02-18 | FuzzRedact 60 s | | |
| V2-SP02-19 | E-1..E-5 budgets | | |
| V2-SP02-20 | coverage ≥ 85 % | | actual: |

## 3. Inventory — SP-03 sketches
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP03-01 | package -race -count=2 | | |
| V2-SP03-02 | QPKS header / CRC / hashing | | |
| V2-SP03-03 | Bloom Appendix-A sizing | | mRaw/mBits/k/body: |
| V2-SP03-04 | Bloom FP measured | | empirical / estimated: |
| V2-SP03-05 | Saturation + resize | | |
| V2-SP03-06 | RebuildBloom | | ms for 5 000 keys: |
| V2-SP03-07 | CMS sizing + overestimate-only | | width/depth/body: |
| V2-SP03-08 | CMS merge / scale / heavy hitters | | |
| V2-SP03-09 | HLL sizing + error bounds + merge | | rel. err at 1e3/1e4/1e5/1e6: |
| V2-SP03-10 | Misra-Gries guarantees | | |
| V2-SP03-11 | MinHash behaviour | | one-new-failure Jaccard: |
| V2-SP03-12 | Save/Load/LoadWithLog | | |
| V2-SP03-13 | tried.bloom generational replacement | | |
| V2-SP03-14 | 12 properties | | |
| V2-SP03-15 | 5 fuzz targets × 60 s | | |
| V2-SP03-16 | Frozen goldens (no -update) | | |
| V2-SP03-17 | import purity | | |
| V2-SP03-18 | L0SketchUpdate budget | | ns/op, allocs: |
| V2-SP03-19 | remaining micro-budgets | | |
| V2-SP03-20 | coverage ≥ 90 % | | actual: |

## 4. Inventory — SP-04 chunk / canon / symbols
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP04-01 | three packages -race | | |
| V2-SP04-02 | params validate + clamp | | |
| V2-SP04-03 | size bounds + contiguity | | |
| V2-SP04-04 | cross-platform determinism | | goldens identical on 3 OSes: |
| V2-SP04-05 | boundary stability | | ≤2 novel in __% ; ≤3 in __% ; max __ |
| V2-SP04-06 | mean size + RootHash | | mean bytes: |
| V2-SP04-07 | SplitStream ≡ Split | | |
| V2-SP04-08 | FuzzSplit 120 s | | |
| V2-SP04-09 | chunk benchmarks | | 100 KB µs, MB/s: |
| V2-SP04-10 | registry order + gating | | |
| V2-SP04-11 | overlap + non-growing guard | | |
| V2-SP04-12 | idempotence / non-growth / inverse | | |
| V2-SP04-13 | seven generic canonicalizers | | |
| V2-SP04-14 | seven per-tool canonicalizers | | |
| V2-SP04-15 | every Match carries a Class | | |
| V2-SP04-16 | Restore errors + FuzzRestore | | |
| V2-SP04-17 | Decide + Signature + OptionsFrom | | |
| V2-SP04-18 | canon golden corpus | | |
| V2-SP04-19 | dedup report (with/without) | | testrunner gain __ ; overall gain __ |
| V2-SP04-20 | canon benchmarks | | |
| V2-SP04-21 | symbols ten dialects | | |
| V2-SP04-22 | Enclosing / References | | |
| V2-SP04-23 | FuzzExtract 120 s | | |
| V2-SP04-24 | symbols benchmarks | | |
| V2-SP04-25 | conformance suites, zero skips | | |
| V2-SP04-26 | corpus hygiene | | |
| V2-SP04-27 | coverage 90/90/75 | | actual: |

### 4a. SP-04 carried defects — resolve or re-defer every row

SP-04 shipped six defects it knowingly did not fix, recorded in `plans/CARRIED-DEFECTS.tsv` with the
diagnosis and acceptance criteria for each in `plans/V2-SP-04-carried-defects.md`. They are listed
here because two of them change what this checkpoint must do, and one of them will interrupt it:

| id | what it costs | what this checkpoint owes it |
|---|---|---|
| SP04-D1 | JSON-escaped Windows temp paths are not canonicalized, so they fork the dedup space and carry user names into stored content | fix, or re-defer with a reason |
| SP04-D2 | canonicalization is not idempotent when a deletion joins two fragments; §5.6 says it must be | a DECISION, not a patch — the complete fix changes the `canon.Delta` contract SP-06 stores against |
| SP04-D3 | three timestamp/duration edges are wrong in the patterns and faithfully reproduced by the scanner | fix, or re-defer with a reason |
| SP04-D4 | `devtool cover`'s `landedSubplans` does not list SP-02 or SP-03, so **their coverage floors are off on the merged `develop`** | **Wider than the row says, and it is now V2-MERGE-14.** SP-02 rewrote the same function on its own branch with a different `landedSubplans` and a `floorApplies` helper, so this is a conflict resolution rather than a one-line addition. The merged set must list **SP-01 … SP-07** — SP-05, SP-06 and SP-07 are missing too. `cover` fails until you do, which is SP-04's exempt-but-not-a-stub check working; **do not silence it by adding a landed package to `probeBlind`** |
| SP04-D5 | canonicalization cost is now dominated by per-rule prefilter scans, ~1 ms of headroom left | re-measure and record; open a successor row if the headroom has gone |
| SP04-D6 | `BenchmarkRun_Bash100KB` measured ±33% on the reference host, which the 25% bench-gate cannot tell from a regression | record the baseline from a quiet machine at `-count 10`, or exempt this one benchmark with the distribution as justification |

`test/guards/carrieddefects_test.go` enforces this: it fails once `plans/V2-report.md` exists while
any row owned by `V2-VERIFY` is still `open`. Resolving a row means setting its status to `fixed`, or
to `deferred:<checkpoint>` with the reason written into the detail document. Both are fine. Leaving a
row open is what the gate stops.

```bash
go test ./test/guards/ -run TestCarriedDefects -v
# Expect: three PASS. After V2-report.md is written, expect a failure naming every unresolved row.
```

**The manifest covers SP-04 and nobody else — V2-MERGE-19.** All six rows are `opened_by SP-04`. SP-02,
SP-03, SP-05, SP-06 and SP-07 each shipped carried items of exactly this kind (an empty recorded corpus
tier, a `benchstat` that nothing installs, a `RunCMSSuite` that cannot discriminate a max-estimator, an
unreachable `Redact` budget, an unrecorded `record-by-owner` fixture, a `fsck` residual), and **none of
them is visible to this gate.** Note the obstacle before deciding: `carrieddefects_test.go` hardcodes
`carriedDefectsDoc = "plans/V2-SP-04-carried-defects.md"` and requires a `## <id>` section in that one
file, so an `SP06-D1` row needs either a section in a document titled for SP-04 or a per-subplan lookup
in the guard. Fix the guard and add the rows, or record in the completion report that the wave's other
carried items are governed by prose alone. Both are decisions; neither is reachable by doing nothing.

## 5. Inventory — SP-05 daemon / IPC / contract
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP05-01 | three packages -race -count=2 | | |
| V2-SP05-02 | address resolution | | |
| V2-SP05-03 | NDJSON framing byte-exact | | |
| V2-SP05-04 | 32-byte state record | | ReadState µs/op: |
| V2-SP05-05 | Send never errors | | |
| V2-SP05-06 | spool append-only + drop-safe | | |
| V2-SP05-07 | server routing / panic / concurrency | | |
| V2-SP05-08 | transport permissions | | |
| V2-SP05-09 | ipctest zero skips | | |
| V2-SP05-10 | singleton lock + lazy spawn | | |
| V2-SP05-11 | session registry | | |
| V2-SP05-12 | WAL ingest (B-B) | | p99: |
| V2-SP05-13 | drain idempotent/resumable | | |
| V2-SP05-14 | idle controller | | |
| V2-SP05-15 | sync→spool transition | | |
| V2-SP05-16 | all-nil Services tolerance | | 13/13 ops: |
| V2-SP05-17 | degraded-passive / off semantics | | |
| V2-SP05-18 | config reload defers chunk change | | |
| V2-SP05-19 | SketchSet never writes tried.bloom | | |
| V2-SP05-20 | idle exit | | |
| V2-SP05-21 | ModeFull + 4 not-yet-implemented | | count: |
| V2-SP05-22 | degrade loud / restore on 2 clean | | |
| V2-SP05-23 | nine assertions individually | | |
| V2-SP05-24 | budget table matches §2.4 | | |
| V2-SP05-25 | 66-combination fault table | | 66/66: |
| V2-SP05-26 | daemon e2e | | |
| V2-SP05-27 | bench-hotpath B-A/B-B/B-D/B-E | | see §9 |
| V2-SP05-28 | security posture | | |
| V2-SP05-29 | coverage ≥ 75 % ×4 | | actual: |

## 6. Inventory — SP-06 store / redact / tokens
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP06-01 | three packages -race -count=2 | | |
| V2-SP06-02 | ten redaction rule families | | |
| V2-SP06-03 | idempotent / bounded / deterministic | | |
| V2-SP06-04 | user-pattern admission | | |
| V2-SP06-05 | token classification | | |
| V2-SP06-06 | media sizing (G10.2) | | PNG/PDF values: |
| V2-SP06-07 | exact chunk accounting | | EstimateRoot µs/op: |
| V2-SP06-08 | calibration clamp + persist | | |
| V2-SP06-09 | fanout / zstd / dedup | | |
| V2-SP06-10 | redact→canon→chunk order | | |
| V2-SP06-11 | per-put raw accounting | | |
| V2-SP06-12 | near-dup detection | | |
| V2-SP06-13 | GetChunk / Open / OpenSpan / Has | | |
| V2-SP06-14 | index durability + tolerance | | |
| V2-SP06-15 | tool_use index + supersession | | |
| V2-SP06-16 | file history + ChangedSince | | |
| V2-SP06-17 | segment log + DPI guard | | ErrAlreadyEncoded: |
| V2-SP06-18 | search | | ms/op: |
| V2-SP06-19 | DedupRatio + sublinear growth | | ratio: |
| V2-SP06-20 | GC mark-and-sweep | | s/op, deadline error: |
| V2-SP06-21 | flush + session index + append-only | | |
| V2-SP06-22 | property suite | | |
| V2-SP06-23 | frozen index formats | | |
| V2-SP06-24 | store e2e incl. secret walk | | |
| V2-SP06-25 | store/redact/tokens benchmarks | | |
| V2-SP06-26 | conformance suites, zero skips | | |
| V2-SP06-27 | coverage 90/90/90 (OWNERS.tsv declares 90/75/75 — say which was changed) | | store: __ redact: __ tokens: __ |

## 7. Inventory — SP-07 DAG / slicing
| ID | Functionality | Verdict | Metric / note |
|---|---|---|---|
| V2-SP07-01 | package -race | | |
| V2-SP07-02 | 9 node kinds / 8 edge kinds / multipliers | | |
| V2-SP07-03 | NodeID scheme + goldens | | |
| V2-SP07-04 | mutation semantics | | |
| V2-SP07-05 | concurrency | | |
| V2-SP07-06 | CrossingEdges = segment_coupling | | µs median: |
| V2-SP07-07 | NodesAfter total order | | |
| V2-SP07-08 | scored slicing | | |
| V2-SP07-09 | thin default + subset property | | |
| V2-SP07-10 | slice goldens | | |
| V2-SP07-11 | thin-vs-full measurement | | size_ratio __ recall __ precision __ |
| V2-SP07-12 | append-only persistence + tolerance | | |
| V2-SP07-13 | idle-only Compact | | |
| V2-SP07-14 | builders + acyclicity | | |
| V2-SP07-15 | no selection authority | | |
| V2-SP07-16 | dagtest zero skips | | |
| V2-SP07-17 | synth determinism | | |
| V2-SP07-18 | slice latency | | ms/op backward / forward: |
| V2-SP07-19 | import purity | | |
| V2-SP07-20 | coverage ≥ 85 % | | actual: |

## 8. Whole-tree gates
| ID | Gate | Verdict | Note |
|---|---|---|---|
| V2-ALL-01 | `go test -race ./...` | | |
| V2-ALL-02 | `go test -count=2 ./...` (windows) | | |
| V2-ALL-03 | `devtool ci-local` | | |
| V2-ALL-04 | CI green on verify/v2 (9 jobs) | | bench-gate & replay-gate required: |
| V2-ALL-05 | Qompack.md unmodified | | |

## 9. Exit-criteria re-verification
| Subplan | Criterion (quoted) | Measured value | Verdict |
|---|---|---|---|
| SP-01 | machinery for §11.3 guardrails exists and is wired to real producers | | |
| SP-02 | "a single number for stock behaviour, reproducible across at least 20 real sessions" | stock fraction_of_opt = __ over 24 synthetic sessions; reproducible: __ | |
| SP-02 | "Store growth sublinear in session length after dedup" | exponent = __ | |
| SP-02 | "No metric may regress by more than 2%…" | regressions: __ | |
| SP-02 | "Every phase gate runs the full replay suite" | phase 0 asserted: __ | |
| SP-03 | must not obstruct 4:1 / 15 ms | L0SketchUpdate = __ µs, __ allocs (= __ % of B-A) | |
| SP-03 | must not obstruct zero stale-block incidents | RebuildBloom + generational replacement: __ | |
| SP-04 | "measure with and without canonicalization" | testrunner gain __, overall gain __ | |
| SP-04 | "FastCDC over 100KB is well under 1ms" | __ µs/op | |
| SP-04 | boundary stability / determinism / size bounds | __ | |
| SP-04 | idempotence / inverse / non-growth | __ | |
| SP-05 | "hook p99 < 15ms" | B-A p99 = __ ms (linux) / __ (macos) / __ (windows) | |
| SP-05 | "< 2s (L4)" | B-E p99 = __ s **(nil PreCompact service at wave 1)** | |
| SP-05 | "degrade to async queue-and-drain rather than blocking" | transition observed at __ | |
| SP-05 | "Never fail silently" / graceful degradation | __ | |
| SP-06 | "ratio ≥ 4:1 on read-heavy sessions" | DedupRatio = __ (in-package) / __ (composed) | |
| SP-06 | "Store growth sublinear" | __ | |
| SP-06 | "encoded-once flag is the DPI guard" | __ | |
| SP-07 | "BFS over a few thousand nodes, sub-millisecond" | __ ms backward / __ ms forward | |
| SP-07 | "thin slicing … probably the right tradeoff" | size_ratio __, recall __ | |
| SP-07 | "a relevance score per node, not a binary keep/drop" | __ | |
| SP-07 | "segment_coupling(p) is the count of DAG edges crossing p" | __ µs | |
| SP-07 | "Do not ship slicing … before p-selection" | selector still inert: __ | |

## 10. New cross-component integration tests (permanent)
| Test | Seam | Verdict | Metric / note |
|---|---|---|---|
| TestIntegration_StorePutUsesRealChunkerAndCanon | chunk+canon+store | | |
| TestIntegration_CanonKeepRawRestoresThroughStore | canon+store | | |
| TestIntegration_StoreNearDupUsesRealMinHash | sketch+canon+store | | Jaccard: |
| TestIntegration_Phase1DedupRatioWithRealPipeline | chunk+canon+store | | ratio on/off canon: |
| TestIntegration_SecretsNeverSurviveTheRealPipeline | redact+canon+store | | |
| TestIntegration_HookEventThroughDaemonToStore | ipc+daemon+store+dag+sketch | | events/WAL/roots: |
| TestIntegration_TombstoneRoundTripThroughStore | observer.Tombstone+store+symbols | | |
| TestIntegration_SupersessionMarksEarlierReadThroughDAG | store+dag | | |
| TestIntegration_SpooledEventsSurviveToTheStore | ipc spool+daemon+store | | |
| TestIntegration_ContractMonitorRunsAgainstRealStore | contract+store | | |
| TestIntegration_DegradedPassiveStillWritesToTheRealStore | contract+daemon+store | | |
| TestIntegration_RealStoreGrowthIsSublinear | store+eval | | exponent: |
| TestIntegration_ReplayGateAcceptsRealGrowthFile | store+eval+test/replay | | |
| TestIntegration_RealBloomHealthFeedsTheFPCeiling | sketch+eval | | estFPRate: |
| TestIntegration_BeladyPMinLandsAtLowCoupling | eval+dag | | low-coupling in __% of events |
| TestIntegration_DAGSliceRanksStoreRootsWithoutDroppingAnything | dag+store | | |
| TestIntegration_SymbolNodesUseTheRealExtractor | symbols+dag+store | | |
| TestIntegration_HotPathWarmWithRealResidentState | everything | | B-A p99: |
| TestIntegration_HotPathDegradesRatherThanBlocks | daemon+store | | |
| TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites | daemon+store+dag+paths | | |
| TestIntegration_GCNeverCollectsALiveRootUnderIngest | store GC + ingest | | |

## 11. Performance budgets
| Budget | Threshold | linux | macos | windows | Verdict |
|---|---|---|---|---|---|
| B-A hook_controlled p99 | < 15 ms | | | | |
| B-B l0_ingest p99 | < 2 ms | | | | |
| B-C l0_process p99 (soft) | < 50 ms | | | | reported |
| B-D hook_wall p99 | reported | | | | reported |
| B-E checkpoint_finalize p99 | < 2 s | | | | (nil PreCompact) |
| B-F mcp_tool_call p95 | < 250 ms | N/A | N/A | N/A | N/A (SP-13) |
| Store dedup ratio (read-heavy) | ≥ 4:1 | | | | |
| Canon gain (testrunner / overall) | ≥ 1.25 / ≥ 1.0 | | | | |
| Store growth exponent | < 1.0 | | | | |
| L0SketchUpdate | ≤ 5 µs, 0 allocs | | | | |
| dag BackwardSlice 5 000 | < 1 ms | | | | |
| dag CrossingEdges | < 5 µs | | | | |
| store PutBytes 100 KB cold / warm | ≤ 3 ms / ≤ 400 µs | | | | |
| store Search 1 000 roots | ≤ 25 ms | | | | |
| store GC 50 k objects | ≤ 2 s | | | | |
| chunk Split 100 KB | < 800 µs | | | | |
| canon Run bash 100 KB | < 3 ms | | | | |
| symbols Extract 100 KB | < 2 ms | | | | |
| ipc ReadState | < 100 µs | | | | |
| eval E-2 / E-3 / E-4 / E-5 | 250/50/20/15 ms | | | | |
| benchstat vs baseline | no >25 % regression | | | | warnings: |

## 12. Regression
| Item | Verdict | Note |
|---|---|---|
| V1 inventory re-run in full | | source: plans/V1-VERIFY-foundation-and-contracts.md or §2.1 |
| Appendix C verbatim | | |
| Append-only guard | | |
| Hooks exit 0 (30 + 66) | | |
| ModeFull with four not-yet-implemented | | |
| Four closing-note guards | | |
| No network / confined write set | | |
| plugin & config docs no-drift | | |
| import graph + bindeps | | |
| §11.3 2 % rule: regressions found | | list, or "none" |
| §11.3 2 % rule: sign-offs used | | must be "none" at a checkpoint |
| §11.2 metric deltas vs phase0.json | | table or "all zero" |
| benchstat >10 % warnings | | explained: |

## 13. Failures, diagnoses and fixes
| # | Failing item | Class (a–e) | Diagnosis | Fix commit | Re-run pass # |
|---|---|---|---|---|---|
| 1 | | | | | |

## 14. Gate
- [ ] Every row above is PASS (or a documented N/A this file authorises)
- [ ] **V2-MERGE-20 was run *before* `verify/v2` was cut**: SP-05's untracked rulings ledger (`.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/`) is copied somewhere that survives `git clean -fdx`, and the destination is named in the report
- [ ] **§0 is filled in, all twenty-four `V2-MERGE-*` rows** — including the collision-resolution log and the inbound-items disposition
- [ ] **No `--ours` / `--theirs` / `-X ours` / `-X theirs` was used to resolve any of the ten files in §2.0a**, and each of the ten is confirmed to carry every branch's half
- [ ] `go run ./tools/devtool cover` prints `OK <pkg>: NN.N% >= floor NN%` for all eleven wave-1 packages and `exempt (stub, …)` for none of them
- [ ] `nightly.yml`'s fuzz matrix has zero orphans in both directions, and `require.Len` matches the repaired arity
- [ ] Every row in `plans/CARRIED-DEFECTS.tsv` owned by `V2-VERIFY` is `fixed` or `deferred:<checkpoint>`; `go test ./test/guards/ -run TestCarriedDefects` passes with `plans/V2-report.md` in place
- [ ] The wave's other carried items are either rows in `CARRIED-DEFECTS.tsv` or a recorded decision (V2-MERGE-19)
- [ ] The plan documents named in **V2-ALL-06** are reconciled with the code that shipped — plans corrected, code left alone
- [ ] SP-07's **INHERIT** is written into `plans/V3-VERIFY-observer-and-negative-knowledge.md` before wave 2 is cut
- [ ] CI green on verify/v2: verify, test ×3, cover, crossbuild, bench-gate, replay-gate, plugin-validate, security, docs — **or V2-ALL-04 recorded as environment-blocked with the reason** (no git remote)
- [ ] `testdata/bench-baseline.txt` updated with integrated wave-1 numbers, **including `chunk`, `canon` and `symbols`, which have never had a baseline**
- [ ] No `Co-Authored-By` / `Signed-off-by` / `Generated with` / 🤖 anywhere in `develop..verify/v2`
- [ ] `verify/v2` merged into `develop` with `--no-ff`; `develop` tagged `v0.1.0`
- [ ] **Only now**: `feat/sp08-observer-l0` and `feat/sp09-negative-knowledge` cut from the post-verification `develop`
````
