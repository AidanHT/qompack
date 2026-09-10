# V5 — Verification checkpoint: commands, selection, grammar, and Phase-7 refinements

**Status:** executed 2026-09-08 to 2026-09-10 on `verify/v5`; completion report `plans/V5-report.md`. **Planning owner/model:** Astra coordinator (effective metadata not exposed); independent review in [ledger](MIGRATION-EVIDENCE.md).

After authorized Wave 4 integration, retain SP15→SP16→SP14 order and evaluate SP21 separately after its M1–M3 prerequisites. Proposed future `verify/v5` branch/worktree comes from the verified integrated `develop`; none is created now. Wave 5 release work follows this gate. Admission may remain disabled; a disabled feature is not an implemented/pass claim.

## How to run this checkpoint

This is future validation under separate implementation authorization. No test/build/benchmark/replay/probe/installer/generator or Git action runs during the planning pass. Preserve current branch/worktree dirt, completed reports and frozen historical baselines. Any future working copies for comparative task runs must be isolated, with repository/environment snapshot identifiers so branches cannot mutate one another.

Use current source and test definitions, [architecture §0.1](00-ARCHITECTURE.md), [master plan](README.md) and [evidence ledger](MIGRATION-EVIDENCE.md). Run only the relevant future entry points after reviewing their actual arguments. Existing commands include `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go run ./tools/devtool plugin-validate`, `go run ./tools/devtool replay --ci`, and `go run ./tools/devtool bench-hotpath`; these are references, not execution here. Missing packaged-host/closed-loop/failure entry points must be added by their owning implementation task. A skipped integration names why and leaves its capability unverified.

Future fixes use the existing verification branch/worktree convention and small conventional commits, no attribution trailers. Proposed sequence: (1) inventory/contract regressions, (2) compatible corrections with fixtures, (3) migration/recovery integration, (4) controlled evaluation/packaging evidence, (5) report/rollback handoff. The coordinator owns shared integration, commits and report; subagents receive exact exclusive test/source scopes only after implementation is authorized. Planning model requests do not select product models.

Retain future groups A–H: A foundation/evaluation, B sketches/chunk/canon/symbols, C daemon/store, D DAG/observer, E negknow/checkpoint, F rehydrate/rules/skills/scheduler, G MCP/commands, H analyzer/grammar/reuse. Add SP21 adapter/recovery checks to G with H's representation contract handed off; C/E/MCP owners consume SP20 remediation. Main owns shared composition/configuration and reviewed file assignments. SP-12 retains ski-rental file ownership; H does not edit it. An independent correctness/cost reviewer reads the combined artifacts.

### Wave-4 integration, before this checkpoint starts

This checkpoint cannot begin until `develop` carries wave 4. `verify/v5` is cut from the verified
integrated `develop`, so the merges below are a precondition of the checkpoint, not part of it. They
are recorded here because this document owns the integration order; per-subplan merges are described
in their own plans.

**Order is SP-15 → SP-16 → SP-14, and it is not interchangeable.** SP-16 builds on SP-15's analyzer
and config surface, and SP-14's commands consume both. SP-21 is wave-4 work but sits outside this
chain: it merges on its own track, after the SP-20 M1–M3 gate, and does not gate the cut.

| Branch | Tip | Ahead of `develop` `7c735ac` | Worktree |
|---|---|---|---|
| `feat/sp15-analyzer-selection-and-grammar` | `db4ec2e` | 19 | `../qompack-sp15` |
| `feat/sp16-phase7-refinements` | `03ba720` | 10 | `../qompack-sp16` |
| `feat/sp14-slash-commands-and-observability` | `154d2de` | 9 | `../qompack-sp14` |
| `feat/sp21-deterministic-admission-control` | separate track | — | `../qompack-sp21` |

#### Four mechanics, each of which stops a merge dead

**`develop` lives in a sibling worktree.** The main repository is on another branch and
`git checkout develop` there fails with `'develop' is already used by worktree at
.../qompack-develop`. Merging a branch that another worktree has checked out *is* permitted — only
`checkout` and `branch -d` are blocked — so merges run with `git -C` against the develop worktree.

**`--no-ff` is required.** Every branch's merge-base is develop's own tip, so a plain `git merge`
fast-forwards, creates no commit, and silently ignores `-m`. The commit-msg hook never runs, and a
clean fast-forward is not evidence that the subject would have been accepted.

**The commit-msg hook rejects git's default merge subject.** `.git/hooks/commit-msg` shells
`go run ./tools/devtool check-commit-msg`, so Go must be on the merging shell's PATH. The grammar is
`^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\([a-z0-9/_.,-]+\))?: .{1,64}$`, with a
trailing period rejected separately. `Merge branch 'x' into develop` matches no type and is refused.
Three consequences: the scope must be **lowercase**, so `chore(SP-15)` fails where `chore(sp15)`
passes; `feat` and `fix` subjects additionally require a `Refs:` footer, which a merge does not need,
so prefer `chore`; and body lines are capped at 100 runes.

**Attribution trailers are rejected on any line**, subject included — `co-authored-by`,
`signed-off-by` and `generated with` case-insensitively, plus the robot emoji. CI re-greps the whole
pushed range for the same patterns, so such a trailer fails twice.

#### Commands

Run in a POSIX shell; the forms below are parse errors in PowerShell. Confirm first that
`git -C "$D" rev-parse --short HEAD` is `7c735ac` and `git -C "$D" status --porcelain` is empty.

```sh
D=C:/Users/Quant/Documents/Programming/Projects/qompack-develop

git -C "$D" merge --no-ff feat/sp15-analyzer-selection-and-grammar \
  -m "chore(sp15): integrate analyzer selection and grammar"
# validate, then:
git -C "$D" merge --no-ff feat/sp16-phase7-refinements \
  -m "chore(sp16): integrate phase 7 refinements"
# validate, then:
git -C "$D" merge --no-ff feat/sp14-slash-commands-and-observability \
  -m "chore(sp14): integrate slash commands and observability"
```

`D` does not survive a separate shell invocation, and validation between merges is a long run — set
it again or use the literal path. If the hook rejects a subject, the merge leaves `MERGE_HEAD` and a
staged index with no commit: re-commit with `git -C "$D" commit -m "chore(...): ..."`, or back out
with `git -C "$D" merge --abort`. Conflicts stop git on their own; resolve in the develop worktree,
then commit with a conforming subject. `--no-commit` only suppresses the auto-commit on an
already-clean merge. Develop is local-only, so an unwanted merge undoes with
`git -C "$D" reset --hard 7c735ac` before any push, or `git -C "$D" revert -m 1 <merge-sha>` after.

**Expected conflict class.** SP-15 and SP-16 both modify `docs/config-reference.md`,
`internal/config/defaults.go`, `internal/config/runtime.go`, `internal/config/validate_test.go` and
`testdata/golden/config/schema.json`. What to expect at the SP-16 merge is an added-config-key clash
and a stale golden `schema.json`, not a logic conflict. A trial integration of all three in this
order has been run on a scratch copy: the merges combined cleanly, the tree built, `go vet` was
clean, and the config, analyzer, grammar, commands, rehydrate and scheduler packages passed. That is
a strong signal, not a substitute for validating the real `develop` after each merge.

#### Validate between merges, serialized

One heavy job per machine, per B09 and R3 — agent count and validation load are separately bounded,
because the session that earned the raised child cap exhausted machine memory running the whole tree
beside its agents. After each merge, in the develop worktree, run these in order and let each finish:

| Step | Command | Why |
|---|---|---|
| 1 | `go build ./...` | Cheapest failure first; a broken build makes later results meaningless |
| 2 | `go vet ./...` | Catches the merge-shaped errors that still compile |
| 3 | `go test -p 1 -timeout=30m ./...` | The standing convention for a serialized whole-tree run |
| 4 | `go run ./tools/devtool lint` | All ten sub-checks, **after** the suite, never beside it |

Two traps that have each produced a wrong conclusion in this repository:

- **Never pipe `go test` through `grep` or `head`.** The pipeline's exit code is the last command's,
  so the shell reports success on a red suite, and the cap truncates the failure list.
- **Co-load fabricates timing failures.** Running `devtool lint` beside the suite produced three
  `internal/negknow` timing breaches that do not exist serialized, and killed `stubskips` at 400s.
  A timing, latency or budget gate needs an otherwise quiet machine.

**Known baseline.** Four tests fail on clean `develop` `7c735ac` and will still fail after these
merges. They are not merge damage, and a fifth failure is:

| Test | Package |
|---|---|
| `TestCarriedDefects_WaveReportRequiresResolution` | `test/guards` |
| `TestV3_HotPathUnchangedWithLedgerResident` | `test/e2e` |
| `TestIntegration_BeladyPMinLandsAtLowCoupling` | `test/integration` |
| `TestIntegration_HotPathWarmWithRealResidentState` | `test/integration` |

The first is this checkpoint's own work: SP05-D1, SP06-D2, SP08-D1 and SP10-D1 are still
`deferred:V4-VERIFY` in `CARRIED-DEFECTS.tsv` while `V4-report.md` exists, and §7 assigns their
dispositions. Re-baseline package counts after each merge rather than predicting them; SP-15 alone
touches 88 files, so totals will move even where the failure set does not.

#### Delegating the integration

**The merge itself is never fanned out.** There is one `develop` worktree, one index and one branch
ref; three children merging concurrently race on all three and the survivor is whichever wrote last.
R3's first safety control is exclusive file ownership, and concurrent merges into one worktree are
its exact inverse. This document already fixes the ownership: the coordinator owns shared
integration, commits and report. A child may be given the merge only as a single serial lane, one
merge at a time, and gains nothing over the coordinator running it.

What parallelizes is the work *around* the merge. Four lanes, in order:

| Lane | Concurrency | Owner | Seat |
|---|---|---|---|
| 1. Pre-merge branch assessment | 3 concurrent | one child per branch | Opus 4.8 / high |
| 2. The merges | **serial, one at a time** | coordinator | — |
| 3. Post-merge validation | **serial, one at a time** | one child per merge | Opus 4.8 / high |
| 4. Checkpoint verification | up to the residual cap | groups A–H | per §1 |

Lane 4 is where the fan-out actually pays: the A–H groups are this checkpoint's real work and are
already scoped for concurrency. Lanes 1–3 exist to get `develop` into a state those groups can run
against.

**Lane 1 — pre-merge assessment, 3 concurrent.** One child per wave-4 branch, each read-only in its
own worktree at that branch's tip, none touching `develop`. Each returns: the branch's changed-file
set, the result of `go build ./...` and its owning packages' tests on that branch alone, and the
collisions it predicts against the other two branches. Three children reading three isolated
worktrees have no shared writer, which is what makes this lane safe to run wide. Their reports let
the coordinator merge knowing what to expect instead of discovering it at the conflict prompt.

**Lane 2 — the merges, serial.** SP-15, then SP-16, then SP-14, by the commands above. Not delegated
and not overlapped.

**Lane 3 — validation, serial.** One child per merge, and only one alive at a time, because heavy
validation is one job per machine under B09 and R3 bounds agent count and validation load separately.
A child that runs the whole tree while another child does the same exhausts memory — that is the
recorded incident behind the raised cap, not a hypothetical. The child runs the four validation steps
in order, reports the failure set, and compares it against the four-test known baseline.

**Every child's brief carries these, or it will report a false BLOCKED:**

- The four known pre-existing failures. A child running a broad suite hits them and, without the
  baseline, concludes the merge broke something.
- Never `git stash`. The stash list is shared across every worktree of one repository, so one child's
  stash silently reaches every other worktree.
- A `go test -run` filter prints `ok` when it matches nothing. Confirm the pattern selects real cases
  before reporting a pass.
- Never pipe `go test` through `grep` or `head`; the exit code is the pipeline's and the list is
  truncated.
- Commit rules: conventional subject, lowercase scope, no attribution trailers, body lines ≤ 100
  runes.
- Its own report path. Two children never write the same file.
- A tool-call budget, and a stop-and-report-BLOCKED rule after three identical failures rather than
  retrying.

**Dispatch.** `ultracode` takes effect only when a user types it in a prompt; this document
containing the word triggers nothing. To run lane 1:

```text
ultracode — run lane 1 of "Delegating the integration" in
plans/V5-VERIFY-commands-selection-grammar-and-refinements.md. One child per wave-4 branch,
read-only, each in its own worktree at that branch's tip. None touches develop.
```

Then merge serially, and dispatch lane 3 one child per merge. The residual of the shared eight-child
pool governs lane 4; confirm no other cooperating wave-4 plan holds children before sizing it.

**Claims this delegation may not make.** B08 forbids claiming observed routing — model identities are
requested and effective routing is not exposed — so no speedup, cost saving or measured-efficiency
claim follows from running lanes 1 and 4 wide.

#### Then cut the branch

Only once the integrated `develop` has been validated does `verify/v5` come from it. Merging
`verify/v5` back is governed separately: see §8's rule that only after independent review may
separately authorized work merge it.

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every command catalog, commit and acceptance row below. Broad commands are available entry points, not a per-edit/per-owner execution list. Use focused real cases first and require a named reason for each long run; preserve coverage, failure artifacts and explicit incomplete states. No execution occurs during planning.

Map existing A–H responsibilities to a few actual run groups rather than launching one suite per owner: command/status/MCP seams; analyzer/grammar/selector cases; and scope/filter/promotion/recovery cases. Dispatch ready independent short groups on isolated resources, preserving SP15→SP16→SP14 integration and SP21's M1–M3 gate. Full phase5/6/7 replay, held-out comparisons and admission quality trials run at their named final policy gate, with their full declared coverage/sample design. A known e2e race timeout is not a required ritual: inherit V4's reviewed race/e2e split or retain a named coverage blocker.

Keep one integrated V5 source candidate and share actual artifacts across all retained inventory rows. Recheck changed producers and consumers before scheduling broader regression. The coordinator assigns each distinct mode/platform/corpus job once; package-level parallelism is counted before adding concurrent processes. Quiet performance checks and shared-state scenarios remain isolated. Final V5 still requires the complete inventory, actual consumer/recovery evidence and all enabled admission gates; shorter runs cannot waive them.

Reuse the existing logical owners and R1 model/effort/fallback policy: Opus 4.8 high for substantive validation, low/medium only constrained inventory/collation, Fable 5.1 high for a necessary independent critical review, and Opus 5 high for multi-file judgment owners. All cooperating SP14–21 and V4–V6 work shares at most eight active children, at most two Fable, one level deep and no nesting, [as revised by R3](MIGRATION-EVIDENCE.md#routing-r1-follow-up-sp-14-through-sp-21) under explicit user authorization for wave 4; this supersedes R1's earlier three-child/one-Fable statement for agent count only. R3 is explicit that the raised cap holds only while its five controls hold — exclusive file ownership, a contract-first slice landed before fan-out, report-to-file with short structured returns, per-unit anti-loop budgets with a stop-and-report-BLOCKED rule, and heavy validation serialized at one command per machine — and that agent count and validation load stay separately bounded, because the session that earned the higher cap exhausted machine memory running the whole tree alongside its agents. The serialization rules in this document are unaffected by that revision. Narrower plan limits remain. The coordinator owns run allocation, final report and acceptance. Do not buy extra capacity or create configuration to force parallelism.

## 2. Cumulative functionality inventory

The original row identifiers remain below as reconciliation references. For each ID, the future inventory owner records the actual current test definition, revised assertion, result/artifact and any retirement/replacement reason. Old test names or historical checked reports are not proof of a current requirement. No row is silently discarded; obsolete success assertions use the current criterion in its owner plan. Existing package tests are inspected before adding missing entry points.

### 2.1 SP-01 — foundation/config/contracts

Retained IDs: `I-01.1`, `I-01.2`, `I-01.3`, `I-01.4`, `I-01.5`, `I-01.6`, `I-01.7`, `I-01.8`, `I-01.9`, `I-01.10`, `I-01.11`, `I-01.12`, `I-01.13`, `I-01.14`, `I-01.15`, `I-01.16`, `I-01.17`, `I-01.18`, `I-01.19`, `I-01.20`, `I-01.21`, `I-01.22`, `I-01.23`, `I-01.24`.

- [x] Future owner reconciles every retained row against SP-19 M0-G1/G2/G4; retained package/CLI/import guards, versioned settings and supported capability states; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled 2026-09-09: `plans/sdd/V5-VERIFY/inventory-SP-01.md`, V5-report.md §1 (§3b).

### 2.2 SP-02 — replay/accounting/baseline

Retained IDs: `I-02.1`, `I-02.2`, `I-02.3`, `I-02.4`, `I-02.5`, `I-02.6`, `I-02.7`, `I-02.8`, `I-02.9`, `I-02.10`, `I-02.11`, `I-02.12`, `I-02.13`, `I-02.14`.

- [x] Future owner reconciles every retained row against SP-19 M0-G5/G6; preserve old synthetic outputs, fix labels/corpus together, request categories and missing telemetry; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-02.md`, V5-report.md §2, additive row I-14.17 for M0-G5 (§3b).

### 2.3 SP-03 — sketches

Retained IDs: `I-03.1`, `I-03.2`, `I-03.3`, `I-03.4`, `I-03.5`, `I-03.6`, `I-03.7`, `I-03.8`, `I-03.9`, `I-03.10`, `I-03.11`, `I-03.12`, `I-03.13`.

- [x] Future owner reconciles every retained row against SP-20 T20-M2-02; exact positives, covered negatives, bounded capacity; Misra–Gries candidates verified before exactness claims; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-03.md`, V5-report.md §3; the negknow half is scored under SP-09 (ruling Q10).

### 2.4 SP-04 — chunk/canon/symbols

Retained IDs: `I-04.1`, `I-04.2`, `I-04.3`, `I-04.4`, `I-04.5`, `I-04.6`, `I-04.7`, `I-04.8`, `I-04.9`, `I-04.10`, `I-04.11`, `I-04.12`, `I-04.13`, `I-04.14`, `I-04.15`, `I-04.16`, `I-04.17`.

- [x] Future owner reconciles every retained row against SP-20 T20-M1-01/02/06; permitted payload fidelity, semantic/physical spans and parser fallback; canonicalization is derived; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-04.md`, V5-report.md §4 (rulings Q11–Q14).

### 2.5 SP-05 — daemon/IPC/contracts

Retained IDs: `I-05.1`, `I-05.2`, `I-05.3`, `I-05.4`, `I-05.5`, `I-05.6`, `I-05.7`, `I-05.8`, `I-05.9`, `I-05.10`, `I-05.11`, `I-05.12`, `I-05.13`, `I-05.14`, `I-05.15`, `I-05.16`, `I-05.17`, `I-05.18`, `I-05.19`, `I-05.20`.

- [x] Future owner reconciles every retained row against SP-19 M0-G2/G3 plus SP-20 T20-M1-03/04/05; durable acknowledgements, bounded workers and qualified event coverage; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-05.md`, V5-report.md §5; fix items F4 (drain frontier) recorded in §22.

### 2.6 SP-06 — store/redaction/tokens

Retained IDs: `I-06.1`, `I-06.2`, `I-06.3`, `I-06.4`, `I-06.5`, `I-06.6`, `I-06.7`, `I-06.8`, `I-06.9`, `I-06.10`, `I-06.11`, `I-06.12`, `I-06.13`, `I-06.14`, `I-06.15`, `I-06.16`, `I-06.17`, `I-06.18`.

- [x] Future owner reconciles every retained row against SP-20 M1 gates; object/index publication, backup/import/GC and privacy before persistence; assembled estimates calibrated, no exact chunk-sum claim; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-06.md`, V5-report.md §6, additive row I-06.19 (backup/import/GC/rollback).

### 2.7 SP-07 — DAG/dependencies

Retained IDs: `I-07.1`, `I-07.2`, `I-07.3`, `I-07.4`, `I-07.5`, `I-07.6`, `I-07.7`, `I-07.8`, `I-07.9`, `I-07.10`, `I-07.11`.

- [x] Future owner reconciles every retained row against SP-20 T20-M2-01/02 and SP-15 M5-G15-B; approximate observed relation graph, unknown dependency coverage retained; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-07.md`, V5-report.md §7 (ruling Q23).

### 2.8 SP-08 — observer

Retained IDs: `I-08.1`, `I-08.2`, `I-08.3`, `I-08.4`, `I-08.5`, `I-08.6`, `I-08.7`, `I-08.8`, `I-08.9`, `I-08.10`, `I-08.11`, `I-08.12`, `I-08.13`, `I-08.14`, `I-08.15`.

- [x] Future owner reconciles every retained row against SP-20 T20-M1-01–05; captured host payload versus full process/file output, distinct events with equal content, child/gap provenance; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-08.md`, V5-report.md §8; `fix(ipc)` nonce width found here (§22 item 4).

### 2.9 SP-09 — negative knowledge

Retained IDs: `I-09.1`, `I-09.2`, `I-09.3`, `I-09.4`, `I-09.5`, `I-09.6`, `I-09.7`, `I-09.8`, `I-09.9`, `I-09.10`, `I-09.11`, `I-09.12`, `I-09.13`, `I-09.14`.

- [x] Future owner reconciles every retained row against SP-20 T20-M2-01/02 and SP-13 T13-STATE; reason-independent identity/exact confirmation retained, errors not absence; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-09.md`, V5-report.md §9; SP09-D1 opened for `TestBudget_Open` (§21).

### 2.10 SP-10 — checkpoint/pins

Retained IDs: `I-10.1`, `I-10.2`, `I-10.3`, `I-10.4`, `I-10.5`, `I-10.6`, `I-10.7`, `I-10.8`, `I-10.9`, `I-10.10`, `I-10.11`, `I-10.12`, `I-10.13`, `I-10.14`, `I-10.15`, `I-10.16`, `I-10.17`, `I-10.18`.

- [x] Future owner reconciles every retained row against SP-10 Test plan gates; committed frontier, compatible readers, lifecycle gaps, complete records and explicit overflow; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-10.md`, V5-report.md §10; T10-* gates recorded as planning artefacts (ruling Q31).

### 2.11 SP-11 — rehydration/rules/skills

Retained IDs: `I-11.1`, `I-11.2`, `I-11.3`, `I-11.4`, `I-11.5`, `I-11.6`, `I-11.7`, `I-11.8`, `I-11.9`, `I-11.10`, `I-11.11`, `I-11.12`, `I-11.13`, `I-11.14`, `I-11.15`, `I-11.16`, `I-11.17`, `I-11.18`, `I-11.19`.

- [x] Future owner reconciles every retained row against SP-11 T11-AUTH/BUDGET/LIFE/LOAD/SCOPE/POINTER/CORRECT/ROLLBACK; current intent and bounded extra context; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-11.md`, V5-report.md §11; T11 gates `implemented_unverified` pending the V6 plan reconciliation (ruling Q35).

### 2.12 SP-12 — scheduler

Retained IDs: `I-12.1`, `I-12.2`, `I-12.3`, `I-12.4`, `I-12.5`, `I-12.6`, `I-12.7`, `I-12.8`, `I-12.9`, `I-12.10`, `I-12.11`, `I-12.12`, `I-12.13`, `I-12.14`, `I-12.15`, `I-12.16`, `I-12.17`, `I-12.18`, `I-12.19`, `I-12.20`, `I-12.21`.

- [x] Future owner reconciles every retained row against SP-12 M5-G12-A–E; useful local cadence, unknown observations safe, no native cut/veto or O(delta) claim; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-12.md`, V5-report.md §12; native cut retired, seven passing gates kept (ruling Q41).

### 2.13 SP-13 — MCP retrieval

Retained IDs: `I-13.1`, `I-13.2`, `I-13.3`, `I-13.4`, `I-13.5`, `I-13.6`, `I-13.7`, `I-13.8`, `I-13.9`, `I-13.10`, `I-13.11`, `I-13.12`, `I-13.13`, `I-13.14`, `I-13.15`, `I-13.16`, `I-13.17`, `I-13.18`, `I-13.19`.

- [x] Future owner reconciles every retained row against SP-13 T13-PROTOCOL through T13-ROLLBACK; installed authorized archive search/expansion, history/current and errors distinct; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-13.md`, V5-report.md §13 (ruling Q46).

### 2.14 SP-14 — commands/observability

Retained IDs: `I-14.1`, `I-14.2`, `I-14.3`, `I-14.4`, `I-14.5`, `I-14.6`, `I-14.7`, `I-14.8`, `I-14.9`, `I-14.10`, `I-14.11`, `I-14.12`, `I-14.13`, `I-14.14`, `I-14.15`, `I-14.16`.

- [x] Future owner reconciles every retained row against SP-14 gates; stable command/JSON/exit semantics and honest status/accounting/coverage; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-14.md`, V5-report.md §14; I-14.14 conformance suite MISSING with owner SP-14.

### 2.15 SP-15 — selection/grammar

Retained IDs: `I-15.1`, `I-15.2`, `I-15.3`, `I-15.4`, `I-15.5`, `I-15.6`, `I-15.7`, `I-15.8`, `I-15.9`, `I-15.10`, `I-15.11`, `I-15.12`, `I-15.13`, `I-15.14`, `I-15.15`, `I-15.16`, `I-15.17`, `I-15.18`.

- [x] Owner reconciled every retained row against SP-15 M5-G15-A/B/C and M6-G15-A/B: feasible actual consumer, qualified diagnostic signals, bounded progress-aware warnings. Delivered 2026-09-08; evidence and dispositions in [§3a](#3a-sp-15-delivery-record-executed-2026-09-08) and `plans/sdd/V5-SP-15/report-main.md`. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions.

### 2.16 SP-16 — reuse/refinement

Retained IDs: `I-16.1`, `I-16.2`, `I-16.3`, `I-16.4`, `I-16.5`, `I-16.6`, `I-16.7`, `I-16.8`, `I-16.9`, `I-16.10`, `I-16.11`, `I-16.12`, `I-16.13`, `I-16.14`, `I-16.15`, `I-16.16`, `I-16.17`.

- [x] Future owner reconciles every retained row against SP-16 M6-G16-A–E; scoped applicability/expiry, bounded retrieval, future-only promotion and optional ablations; retains compatible historical regressions and an explicit old-to-new assertion map. Historical baseline source: this file at HEAD `7f92af5`, corresponding completed V1–V3 reports, and current source/test definitions. — reconciled: `inventory-SP-16.md`, V5-report.md §16; M6-G16-C delivered-partial, `implemented_unverified` (ruling Q64).

### Migration additions without renumbering prior rows

- [x] SP-19/M0: mapping, packaged capability/lifecycle canaries, request ledger, settings and baseline provenance. `verified_in_target` at the repository-validator level; installed host `implemented_unverified` (V5-report.md §24).
- [ ] SP-20/M1–M2: capture/privacy, crash/publication, resumable migration/backup/rollback, state authority, uncertainty, retention and scope gates. V5: `verified_in_target` for every gate except uncertainty, which is partial on the digest surface (4.5; V5-report.md §22 item 26); SP20-D1, SP20-D2 and SP20-D3 are carried to V6-VERIFY (§21, §24); two SP-20 commit checkboxes delivered-unchecked (ruling Q29).
- [ ] SP-21/M4: independently opt-in admission gate, every pointer resolves, processed-envelope recursion, fidelity, privacy and unmodified-output comparison. Disabled admission is recorded as disabled, never passed. V5: the foundation is `verified_in_target`, but the unmodified-output comparison is not (T21-QUALITY-01 inconclusive by construction, T21-HOST-01 accepted unverified), so the box stays open; admission recorded as disabled, never passed (V5-report.md §18, §24).

## 3. Exit-criteria re-verification

- [x] R2 run map accounts for focused/parallel groups, each justified long gate, actual instrumented coverage, current candidate/artifact identity and every incomplete result; no duplicate per-row whole-tree runs or unreviewed coverage substitutions. — `verified_in_target` (V5-report.md §18, §0, §17): every run names its commit in §17, and the whole tree re-ran on the report commit `b1a0183` (B3), failing only on the three carried tests (SP20-D1's two B-B tests and the Belady floor), and the race detector found no data race in the three packages changed since `b64b3f6` (C1b).
- [x] Carry V4's actual mandatory results and unresolved disabled capabilities without rewriting history. `verified_in_target` (V5-report.md §18, §29).
- [x] SP14 command/JSON/exit/status/request-ledger behavior matches the supported plugin. `verified_in_target` with four gaps: the H3 `/qompack:checkpoint` route, `Deps.EvalArtifacts` (unsupported, Q53), the I-14.14 conformance suite and the I-14.16 benchmarks (V5-report.md §18).
- [ ] SP15 selection reaches the real Qompack consumer with one compatible representation, closure, deterministic ties and complete overhead/overflow — `implemented_unverified` at host level, evidenced in-repo (§3a). Assembled cost remains an ESTIMATE: M5-U15-representation-overhead is open, so no token guarantee is claimed. V5: `implemented_unverified` end-to-end (V5-report.md §18); ticked at SP-15's delivery (`db4ec2e`) before verification, unticked by V5.
- [ ] SP15 warnings remain progress-aware, bounded and warning-only; grammar complexity is NOT justified and is recorded optional-disabled — the M6-G15-B ablation went against Sequitur (§3a). V5: warning-only and bounded are `verified_in_target` (4.10's degraded arm), but the progress-aware detector is `unsupported`: no production root composes `grammar.Detector` and `runtime.selection.loopWarningsEnabled` defaults false (V5-report.md §18); ticked at SP-15's delivery (`db4ec2e`) before verification, unticked by V5.
- [x] SP16 scope/applicability/expiry/privacy and bounded retrieval/usefulness tests pass; promotion has no native residency claim. `verified_in_target` at the package seams, `unsupported` end to end: every phase-7 switch ships off (V5-report.md §18).
- [x] SP21 enabled surfaces, if any, satisfy all T21 gates, including comparison to unmodified output; unknown parser, changed failure/delta baseline, storage failure, own-envelope recursion and competing hooks have safe outcomes. — no enabled surface; the foundation is `verified_in_target` through 4.17 (V5-report.md §18, §24).

## 3a. SP-15 delivery record (executed 2026-09-08)

Additive, per §8's rule that new rows do not renumber prior inventory. **This section discharges
SP-15's rows only.** SP-14, SP-16, SP-19/20/21 and every cross-component row in §4–§7 are untouched
by it, and the V5 checkpoint as a whole remains open.

**Branch:** `feat/sp15-analyzer-selection-and-grammar`, 18 commits over `develop@7c735ac`,
**unpushed**. Windows 11, go1.26.6. Full record: `plans/sdd/V5-SP-15/` — `contract.md` (the frozen
contract plus amendments A1–A7), `report-A/B/C/D/E.md`, `report-main.md`.

`internal/analyzer` and `internal/grammar` were SP-01 stubs at the baseline and are now real. Both
new capabilities ship **disabled** behind independent switches
(`runtime.selection.submodularEnabled`, `runtime.selection.loopWarningsEnabled`, both default
false). No default was flipped by a synthetic score.

### 3a.1 Gate dispositions

| Gate | Status | Evidence |
|---|---|---|
| M5-G15-A | `implemented_unverified` | 7 committed instances with brute-forced optima; 12 held-out + 117 corpus rows at replay phase 5; zero/tiny/overflow all exercised |
| M5-G15-B | `implemented_unverified` | 3 counterexample fixtures with provenance: similarity-is-not-equivalence, an exact supersession chain, a shared-file edge implying nothing about relevance |
| M5-G15-C | `implemented_unverified` | 13 daemon cases + 11 rehydrate cases against the real composition root, the real item-3 builder and the real drop report |
| M6-G15-A | `implemented_unverified` | 13 held-out streams; 0/7 false alarms; 0 amplification over 210 fed-back observations; dedup/cap/expiry gated |
| M6-G15-B | **optional-disabled, explicitly accepted** | the ablation in §3a.3 |

`implemented_unverified` is used deliberately rather than `verified_in_target`: every gate above has
real executed evidence in-repo, and none of it was exercised through an installed host. No host
canary covers selection or warnings.

**Conformance activation.** Four inherited `/behaviour` blocks were Rule W-1 skips at the baseline
and now RUN and PASS: `analyzertest`'s `NewCheapScorer` (4 cases), `DetectRedundancy` (3),
`NewSelector` (6), and `grammartest`'s `invariants_hold_after_every_append` (23 appends). No suite
was edited to make a block pass; the single suite change is the correctness fix in §3a.4.

### 3a.2 Consumer integration, and the §3.2 question it settled

`tools/devtool/importrules.go` denies `rehydrate` any `analyzer` import and denies `analyzer` any
`negknow` import, so selection is wired at the **daemon composition root**, not through an import
edge. **No §3.2 amendment was required or proposed.**

Selection reaches `rehydrate.Build` as request DATA rather than a provider on `Deps`, because
`Build` is a pure function of `(Request, Deps)` and `PropBuild_Deterministic` requires
byte-identical output for identical input — a live provider would have put a store read, a DAG walk
and a token estimate inside a function whose whole contract is that it has none.

G6.3 elimination evidence reaches the selector as a neutral `Candidate` carrying `Mandatory` plus a
`Provenance.Qualification`; only a ledger-active record binds, and stale or uncertain records are
carried with their qualification and never promoted. Under pressure an authoritative elimination
degrades to a pointer BEFORE overflow is considered; overflow then names the record and archives it
with a recovery path.

A nil selection returns its input untouched, so the disabled path is byte-for-byte the path that
shipped — every pre-existing rehydrate test passes unchanged.

### 3a.3 M6-G15-B — the ablation went against Sequitur

| Arm | detected | false alarms | alloc | rules |
|---|---|---|---|---|
| state-signature | 5/6 | **0/7** | 65,984 B | 0 |
| sequitur | 6/6 | **6/7** | 159,200 B | 32 |
| sequitur + progress | 6/6 | **2/7** | 122,176 B | 32 |

Sequitur buys exactly one detection — a cycle whose period exceeds `minRepeats` — and pays six
false alarms for it, still two after being handed the same progress signal the winning arm uses, at
2.4× the allocations. A third arm was added specifically to remove the "Sequitur was handicapped"
objection; it still loses, and the fixtures were **not** tuned afterwards.

**Disposition: not justified by this evidence; stays optional and disabled** per
M6-U15-sequitur-value. This CONFIRMS the shipped design rather than changing it — the detector is
signature-only and never referenced Sequitur. The induction is still worth having: it is real
rather than stubbed, and `internal/observer` consumes `Thrash` on a pre-existing path this evidence
does not touch. Per §8, optional policy non-delivery is explicit and no default was flipped.

### 3a.4 The one conformance-suite change

`grammartest.checkNoDigramTwice` counted **overlapping** digram occurrences, while Sequitur's
invariant is over non-overlapping ones. It was therefore **unsatisfiable, not strict**, and the
fixture reaches that state twice.

Verified by tracing the implementation before any edit, rather than on the reporting role's word:
`Read Edit Bash` ×3 induces `S → R R R` with `R → Read Edit Bash` used three times, and the two
`(R,R)` pairs overlap at index 1. Acting on that leaves `S → Q R` with `Q → R R`, and `Q` used once
is a rule-utility violation that inlines `Q` straight back. There is no other grammar for that
input, so the check rejected the only correct answer.

The amendment forgives only same-sequence adjacency, and a forgiven occurrence does not advance the
recorded site. `behaviour_internal_test.go` pins both halves: `x x x` passes as one countable
occurrence, while `x x x x` holds two DISJOINT pairs and still fails, as do runs up to nine, the
same digram across two sequences, and adjacent indices in different sequences. The loosening cannot
decay into a check that accepts everything.

### 3a.5 §6 regression note — selector guards were NOT retired

§6 anticipates retiring "selector guards that require unsupported native p-selection". SP-15 did
**not** do so. Both `NewSelector` guards are byte-identical to the pre-SP-15 source and in the
original order: the `Pos < p` refusal (§13 invariant 4) still runs first, then the closing-note-3
ship-order gate. `Propose` routes through the same constructor, so the new surface inherits both.

What changed is only the stub-marker rows in `test/guards/stubs_test.go`, now
`pureMethods: allMethodsAreReal` for `analyzer` and `grammar` alongside `dag`, `negknow` and
`checkpoint`. A refusal to CONSTRUCT is not a stub, and `buildorder_test.go` still asserts it
separately and is untouched.

### 3a.6 Defects found

| # | Defect | Found by | Fix |
|---|---|---|---|
| 1 | `Qualification`'s zero value was `QualCurrent`, so any struct literal omitting the field silently minted an authoritative elimination — the §12-High false `already_tried`, reachable by forgetting a field | a role coding against the coordinator's own frozen contract | `66c60f7`; `QualUncertain` is now the zero value, pinned by test |
| 2 | `checkNoDigramTwice` unsatisfiable (§3a.4) | role A | `810d196` |
| 3 | `ParseRuleRef` delegated to `strconv.Atoi`, which accepts `"+7"` and `"07"`, making encoded rule identity many-to-one — a forged stream could name rule 7 in a spelling no encoder emits | a coordinator test written for role A's question | `1b0fd5d` |
| 4 | The codec clause admitted version zero, so a zero-filled truncated write would read as a valid empty grammar | role B | `767dd7e` (A6) |
| 5 | An uncertain warning rendered identically to a confident one, so the qualification lived in the type and nowhere the reader could see it | role B | `767dd7e` (A7) |
| 6 | `Proposal.Reason` is prose but `rehydrate` consumed it as a `DropEntry.ID` — a whole sentence in the report's id column | coordinator, at integration | `f84cdf4`; `Item` and `Reason` split |

Defects 1 and 6 are the ones no single role could have found: 1 was in the frozen contract itself,
and 6 exists only where two roles' outputs meet.

### 3a.7 Failures observed, and their §7 disposition

None is caused by SP-15. Each was reproduced on the base tree or in isolation rather than matched to
a known category by name.

| Failure | Verification | Verdict |
|---|---|---|
| `test/e2e` `TestV3_HotPathUnchangedWithLedgerResident` | run on base `develop@7c735ac`: same failure, same number — B-B p50 **2.816 ms** against a 2.000 ms limit; B-A/B-E/B-E_cpu all PASS | pre-existing B-B breach; a V4 sign-off item |
| `test/integration` `TestIntegration_HotPathWarmWithRealResidentState` | run alone: the same B-B breach at the same figures | same root cause |
| `test/integration` `TestIntegration_BeladyPMinLandsAtLowCoupling` | run on base: identical failure. 61.5% against a 70% floor, and the assertion itself says "report the measured rate, do not lower the floor" | the deliberately-red Belady row; an authorized re-derivation of the floor is a V4 sign-off item |
| `test/guards` `TestCarriedDefects_WaveReportRequiresResolution/{SP08-D1,SP10-D1}` | wave-2 defects still `open` in `plans/CARRIED-DEFECTS.tsv` while `plans/V4-report.md` exists | pre-existing bookkeeping; no SP-15 file involved |
| `go run ./test/replay` — phase-3 A2 divergence, corpus stale | reproduce identically on base at `--phase 3` | pre-existing. Registering phases 5 and 6 makes the staleness message name a later phase; it does not create the failure |
| `internal/mcp TestBudgetBF`, `internal/negknow TestBudget_DetectorScan` | pass when re-run serially with `-p 1` | co-load artifacts |
| `internal/negknow TestBudget_Open` | passes alone at 259 ms CPU/op against a 300 ms budget | load-sensitive, marginal |

Per §6, none of these is erased: each is recorded as a failure with its evidence, and the two V4
sign-off items remain the V4 owner's.

**A methodology correction against the coordinator's own run.** The first whole-tree pass was
INVALID and its failures must not be cited. It omitted the required `-timeout=30m` — whose absence
panicked `test/e2e` mid-suite and aborted every package after it — and used `-p 2`, manufacturing
exactly the co-load that makes timing rows meaningless. The re-runs above are the evidence.

### 3a.8 What SP-15 does NOT discharge

| Item | State |
|---|---|
| **Independent adversarial review (R1, mandatory)** | **OPEN.** R1 is explicit that an author's own recheck cannot discharge it. The coordinator's verification came from a thread that did not author the code under review — stronger than self-review, but not the separate reviewer the policy names, and no such route is available in this client. §8's final gate, "only after independent review may separately authorized work merge `verify/v5`", is therefore NOT met |
| Requested routing (R1 / B08) | Opus 4.8 and Fable 5.1 are not selectable in this client; every role ran Opus 5 under the documented fallback. Effective per-child model/effort metadata is not exposed, so **no routing or cost-saving claim is made** |
| M5-U15-representation-overhead | **OPEN.** Assembled cost is an estimate, not calibrated against provider-reported usage. Sound for ranking; not a token guarantee, and §5's rule against unsupported acceptance facts applies |
| `ToolUsesBySession` | **OPEN, reported.** Redundancy coverage is exact per covered path only; `DetectRedundancy` returns `core.ErrDegraded` alongside a valid result rather than presenting partial coverage as complete |
| Host-level verification | No host canary exercises selection or warnings; every gate above rests on in-repo evidence |
| SP-14, SP-16, SP-19/20/21 rows | Untouched by this section |

## 3b. V5 execution record (executed 2026-09-08/09)

This checkpoint ran under the user's 2026-09-08 instruction to integrate wave 4 and finish V5; the
record of what was done, every command, artifact, waiver and open item is
[V5-report.md](V5-report.md). This section only maps the plan's rows to that record so the checkboxes
above can be read against evidence rather than intent.

| Plan section | Where the evidence is | State |
|---|---|---|
| Wave-4 integration (SP-15 → SP-16 → SP-14, then SP-21) | V5-report.md §0; `develop` `504f38f`, `0884ea8`, `33bb890`, `0dd982b`, `6856cb2`, `87c0c1d` | done, validated between merges, lint green on `87c0c1d` |
| §2.1–2.16 inventory | `plans/sdd/V5-VERIFY/inventory-SP-NN.md` (16 files, 274 rows), V5-report.md §1–16 | done; 239 PASS (I-11.19 partial on its L5-BUILD clause), 1 FAIL (I-01.18 retired grep), 1 FAIL-BASELINE (I-09.14, clock-bound: SP09-D1), 5 FAIL-COLOAD-SUSPECT (each re-run alone in the quiet pass), 28 NOT-RUN deferred to the quiet pass, 13 MISSING with owners (four retired by ruling) |
| Migration additions (SP-19/M0, SP-20/M1–M2, SP-21/M4) | V5-report.md §24 | recorded per capability; admission disabled and recorded as disabled |
| §3 exit criteria | V5-report.md §18 | each criterion carries a plan-§8 status word |
| §4.1–4.16 and §4.17 | `test/e2e/v5_xNN_test.go`, `test/integration/v5_xNN_test.go`, `plans/sdd/V5-VERIFY/xNN-disposition.md`, V5-report.md §19 | all seventeen authored and adversarially reviewed; 4.13 retires the native-cut assertion as the plan requires; unverified remainders recorded per row |
| §5 performance | V5-report.md §17, §20 (serial quiet pass) | Windows figures only; reference platform not measurable on this host |
| §6 regression | V5-report.md §21; `plans/CARRIED-DEFECTS.tsv` | SP05-D1 fixed, SP02-D6 wontfix, SP10-D1 improved and re-owned, SP06-D2/SP08-D1 re-owned, SP08-D2, SP05-D2, SP09-D1, SP20-D1, SP20-D2 and SP20-D3 opened |
| §7 failure protocol | V5-report.md §22 | fifteen entries plus the quiet-pass findings |
| §8 report | V5-report.md | written; independent review in its §28 |

The checkbox lines above are ticked where V5-report.md records `verified_in_target`; a ticked box on a
row that also names an unverified remainder means the row's current criterion was asserted with real
producers and the remainder is recorded, not that the historical text passed.


## 4. New cross-component integration tests

Historical proposed test names are retained as identifiers, not assertions that these tests exist or pass. Correct their assertions/names compatibly during future implementation and record the mapping; unsafe guarantees below are retired.

| Retained section / test identifier | Current future criterion and owner |
|---|---|
| 4.1 `TestV5_ObserveToStatusRoundTrip` | [x] SP-14 status agrees with authoritative qualified observations and missing telemetry. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x01-disposition.md`. |
| 4.2 `TestV5_TombstoneToExpandRoundTrip` | [x] SP-20/SP-13 exact authorized handle recovery with fidelity and no historical/current substitution. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x02-disposition.md`. |
| 4.3 `TestV5_HookEventToTombstoneToRetrievalAfterRestart` | [x] SP-20 acknowledged capture survives interrupted drain/restart, or records explicit gap. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x03-disposition.md`. |
| 4.4 `TestV5_PreCompactToRehydrateToDroppedRoundTrip` | [x] SP-10/SP-11/SP-13 bounded recovery/coverage without optional-PostCompact dependency. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x04-disposition.md`. |
| 4.5 `TestV5_EliminationThroughEveryFourSurfaces` | [ ] SP-20/SP-13/SP-14 stale/uncertain/error state survives every surface and old-caller adapter. — authored and green; disposition `partial`: the error state does not survive the digest surface under a blind ledger, and pin records are stamped `mcp`; both are routed as defect candidates (V5-report.md §19, §22 items 26–27, `plans/sdd/V5-VERIFY/x05-disposition.md`). |
| 4.6 `TestV5_SelectorGatedByRealScheduler` | [x] SP-12/SP-15 local capability guards replace native-p-selection prerequisites. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x06-disposition.md`. |
| 4.7 `TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint` | [x] SP-15 selected complete records reach actual SP-11 consumer without native cuts. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x07-disposition.md`. |
| 4.8 `TestV5_GrammarAndPromotionCoexistInFinalize` | [ ] SP-15/SP-16 codec/selection/promotion coexist under scope, overhead and provenance. — authored and green; disposition `partial`: no grammar fold is wired into `Finalize`, so codec coexistence is recorded unverified (V5-report.md §19, `plans/sdd/V5-VERIFY/x08-disposition.md`). |
| 4.9 `TestV5_TruncationOrdersActionHistoryAndPromotionCorrectly` | [x] SP-11/SP-15/SP-16 complete-record overflow replaces arbitrary byte truncation. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x09-disposition.md`. |
| 4.10 `TestV5_ThrashWarningVisibleInStatusAndCheckpoint` | [x] SP-15 warning-only progress-aware dedup; self-generated warnings cannot feed back. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x10-disposition.md`. |
| 4.11 `TestV5_SegmentBloomNarrowsRecall` | [x] SP-20/SP-16 filter generation/coverage: exact positives, stale/incomplete negatives bypass. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x11-disposition.md`. |
| 4.12 `TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession` | [x] SP-16 scope/branch/version/expiry/unfinished-intent matrix; no guaranteed warm improvement. — authored and green; `partial` by design only in that warm improvement is not asserted, which the criterion does not guarantee (V5-report.md §19, `plans/sdd/V5-VERIFY/x12-disposition.md`). |
| 4.13 `TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne` | [x] Retire ski-rental native cut assertion; SP-12 owns deprecated compatibility and local cadence. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x13-disposition.md`. |
| 4.14 `TestV5_EveryContractAssertionHasARealProducer` | [x] SP-19 per-capability observed/unknown evidence, no fixed producer count or setter claim. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x14-disposition.md`. |
| 4.15 `TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent` | [x] SP-14/SP-21 degraded/error/privacy behavior, no absence or misleading success on failure. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x15-disposition.md`. |
| 4.16 `TestV5_NoPackageWritesOutsideDotQompack` | [x] SP-17 trust/retention/write-set boundaries with explicit allowed data writes and no project mutation. — authored and green on this tree: V5-report.md §19, `plans/sdd/V5-VERIFY/x16-disposition.md`. |

### 4.17 Admission extension

- [ ] Future SP21 tests distinguish fresh owned-result first transformation from already-processed envelope bypass, then a tiny target-tested host allowlist. (V5: the distinction is verified by T21-RECURSE-01 and 4.17 (a); the target-tested host allowlist does not exist yet — T21-HOST-01 accepted unverified, V5-report.md §24.)
- [x] Durable capture/authorization and resolution precede replacement; unavailable baseline/parser change/failure signature resets relative-delta eligibility. — T21-PIPE-01, T21-BASELINE-01 passing; 4.17 (b) (V5-report.md §24).
- [x] Unsupported binary/multimodal/schema content follows pass-through or privacy-denial policy, with structured/displayed status and errors preserved. — T21-PASS-01, T21-FIDELITY-01 passing; 4.17 (c) (V5-report.md §24).
- [ ] Record supported-task quality/recoverability against unmodified output plus cost/latency separately; replacement stays off until target and regression gates pass. (V5: T21-QUALITY-01 inconclusive by construction, no observations; replacement stays off — V5-report.md §24.)

## 5. Performance budget validation

Old B-A–B-F, L0/L5 and individual benchmark labels remain historical comparison targets where applicable. Reconcile each with its actual workload, binary/provider/model/OS, payload size, process startup, I/O, locking, sample distribution and date. No universal 15 ms, 4:1, sublinear growth, near-zero distortion, O(delta) native latency or first-turn savings is an acceptance fact.

Measure Qompack-added tokens and total observed context separately. Count the assembled representation using the supported estimator, label estimation and model changes, and calibrate against reported usage. Preserve old fraction-of-OPT outputs as diagnostics for their synthetic harness/objective; classic paging OPT is not a task-quality ceiling. Re-reading a changed file can be useful.

The request ledger separates uncached input, cache reads, cache writes/TTL, output, retries, failed/aborted trials, compaction, subagents and Qompack model calls if any. Record provider/model/rate-table date/pricing mode/completeness; missing telemetry is unknown, not zero. Estimated price is category usage times applicable rates plus relevant non-token charges; reported usage, estimates and invoiced cost remain separate. Subscription allowance is not automatically cash per token.

Declare regression margins and sample-size rationale before outcomes. Use repeated stochastic baselines, held-out tasks and changing requirements. Primary outcomes are task completion, constraints/regressions and recoverability; cost, latency, context, retrieval burden, repeated work, CPU/storage and tail delays accompany them. Report uncertainty, exclusions, failed trials and inconclusive outcomes; twenty runs cannot establish an unsupported two-percent guarantee. Cost and correctness gates remain independent. Record timings on quiet runners after future authoring completes.

## 6. Regression

Preserve Waves 0–2 and the V3 report/addendum unchanged. The 2026-08-26 user waiver closed V3 despite J5 billing blockage; J5 run 32932419445 and three-platform p99 backfill remain waived-open until actual evidence. Do not reinterpret early held-gate cells as a project restart.

Carry SP05-D1 to SP-20 drain/ack recovery, SP02-D1–D6 as one V4 corpus/rebaseline unit, and SP06-D2/SP08-D1 as a paired performance decision. Preserve SP04-D2/D3 and SP06-D1 wontfix rationale while testing new fidelity/retention contracts; SP04-D7's delta consumer is assigned to checkpoints. Reconcile SP11-C28 frontierOf/Ref.Frontier against the active sibling. Existing SP07 NodeID/generation/fixture rulings in V2-SP07-handoff.md remain regression context.

No baseline regeneration, fixture relabeling or machine-read carry update can erase a failure. Future updates to CARRIED-DEFECTS.tsv require actual evidence under its existing guard. Documentation v1.5 is the authorized planning baseline; the former rule demanding byte identity of Qompack.md to the initial commit is retired for this explicit revision.

Retire selector guards that require unsupported native p-selection; replacement guards test actual local capability, compatible budget and authorized recovery. Preserve the historical CLI `bench` survivor until SP17 explicitly implements or deprecates it compatibly; absence of a current implementation is not a false pass. Activate conformance only for real supported behavior and report unsupported/skip cases honestly.

## 7. Failure protocol

Record severity, exact command/artifact, snapshot, version, reproduction conditions and affected owner. A unit pass cannot override a failed or skipped host gate. Missing coverage and retrieval failures remain unknown/unavailable, never absence or successful restoration.

The future integration owner makes the smallest compatible fix, rechecks affected gates and dependencies, and preserves original failures. Disable recording, reinjection, replacement and experimental policies independently according to the fault/privacy policy. Optimization failure normally passes through; privacy denial follows denial policy.

Before data cutover use an engine-supported consistent backup and stable writer frontier. Rollback before and after new-format writes must be rehearsed: use compatible readers or the verified backup, with explicit treatment of later writes an older binary cannot read. Stop incompatible writers, retain object IDs/evidence/rollback roots and do not promise automatic downgrade. Failed mandatory gates block affected enablement/release; optional-disabled policies are identified explicitly.

## 8. Completion report template

Migration rows are additive and do not renumber prior inventory. The future report must retain every original row ID with its explicit current assertion/result or documented retirement/replacement mapping.

Future report retains the existing V-report convention and records: branch/HEAD and dirty baseline; date, supported OS/provider/model/host/plugin/schema versions; inventory row-by-row result and old-to-new assertion map; completed work preserved; commands actually run and artifact paths; skips/failures/waivers; migration/capability gate statuses; request-usage completeness and estimated-price provenance; statistical outcomes; privacy/retention and rollback drill; independent findings and resolution; remaining blockers and next authorized action.

Use `documented`, `verified_in_target`, `implemented_unverified`, `unsupported`, `experimental` and `unknown` appropriately. A new report must not copy old PASS cells as new runs. Existing completion reports are immutable historical records. Proposed future report paths follow V4-report.md, V5-report.md and V6-report.md naming; these reports are not created in the planning pass.

Retain per-SP01–16 inventory and original exit/integration/budget/regression/report roles. Add SP19/20 prerequisite status and SP21 enabled-surface matrix. Record three evaluation layers, controlled/held-out snapshots and separate stock/current-Qompack/corrected-recovery/admission comparisons; observation masking is a baseline only if the actual harness supports it.

- [ ] Future V5 gate requires no unresolved mandatory regression or recovery/privacy blocker for enabled scope. NOT met: SP20-D1 (budget B-B red on the shipped leased delivery path) and SP20-D2 (verify-on-read over the store read budgets) are known regressions in enabled scope, and SP20-D3 is a latent recovery-fidelity defect; all three are `deferred:V6-VERIFY` and carried under the user's 2026-09-08 waiver (V5-report.md Authority paragraph, §21, §29). F4-P1 is fixed; the reference-platform rows SP06-D2, SP08-D1, SP09-D1 and SP10-D1 and the F4 residue SP08-D2/SP05-D2 are deferred with them.
- [x] Optional policy non-delivery is explicit; no synthetic-score-only default flip. Grammar optional-disabled, phase-7 switches off, admission off (V5-report.md §18 for grammar and phase 7, §24 for admission).
- [x] Only after independent review may separately authorized work merge `verify/v5`, consider the historical `v0.4.0` marker and proceed to Wave 5. No branch/tag/commit action occurs during planning. — independent review in V5-report.md §28; merge into local `develop` authorized by the user's 2026-09-08 instruction (waiver in the report's Authority paragraph); no tag, no push.
