# V6 close-out w3-paths: paths, x/sys and GC-overshoot hygiene

Branch `closeout/w3-paths`. Workflow `wf_85543bfd-f18`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

> Coordinator note: the throwaway phase-probe commits `c02b67b`, `4570089` and `d66db7b` are deliberately unreachable from this branch. Their patches are preserved under `runs/gcprobe/probe-<sha>.patch` so the probe evidence stays readable after the local branches go.

## Implementer — status `done`, head `ef2f98cdab6d40b57ef9f617ffa39fd87114652f`

### Root cause

(1) paths.rootOf treated the nearest ancestor that CONTAINED a .qompack as a path's owner. With ~/.qompack present, store-less writes below the home staged in <home>/.qompack/tmp, and the rename into the unmade target directory failed (the runs/21 signature). The same rule let a stray .qompack inside checkpoints/ or pins/ unprotect the files beside it, and the guard never protected relative paths. Fixed: the owner is the nearest .qompack on the path's own chain (no pass-over, no sideways look), and the guard asks every existing store with the absolute path. (2) GO-2026-5024 was in golang.org/x/sys v0.33.0; now upgraded to v0.44.0, which requires no other modules. (3) The overshoot test priced its first-check end over mark, live-set write and sweep only. A real GC call also runs recordOutcomes before its sweep, and under -race its String()-comparing sorts (222,108 allocations for 3,072 roots) cost 104-292 ms. That additive omission defeats the multiplicative retry, so it is a test defect aggravated by a product inefficiency, not GC overshooting its check interval. Fixed by pricing through GC's own GCReport.Duration, and by byte comparison in the sorts (same order, 18 allocations).

### Summary

# w3-paths: the WriteAtomic walk, the x/sys upgrade, and the GC overshoot test (resumed seat)

Branch `closeout/w3-paths` in `../qompack-cx-w3-paths`. HEAD is `ef2f98c`. Review base is `6aff949`, merged in `80deba0`. The code is final at `c3e3519`. `ef2f98c` only adds evidence under `plans/sdd/V6-closeout/w3-paths/runs/`.

I treated the seven earlier commits as an unreviewed draft. I kept all seven. `d660423` needed two changes, which went in as the new commit `1217e6a`. I added four small commits after that. None of the paused seat's evidence is relied on: everything was re-run at `6aff949` and at `c3e3519`. Its logs sit in `runs/prepause/`. The one exception is its completed phase probe at `c02b67b`, which is cited alongside the new probes.

## 1. `paths.WriteAtomic` staged in an unrelated `~/.qompack`

### Root cause
`rootOf` took as a path's owner the nearest ancestor that merely *contained* a `.qompack`. On a machine with `~/.qompack`, a store-less write below the home was staged in `<home>/.qompack/tmp`. The rename then failed because the target's directory had never been made. That is exactly the w2-lifetime runs/21 signature: `rename C:\Users\Quant\.qompack\tmp\wa-...: The system cannot find the path specified`.

The same rule had two more defects:
- A stray `.qompack` inside `checkpoints/` or `pins/` could unprotect the sealed files beside it.
- A relative path was never protected, because `IsProtected` was asked with it against an absolute root.

### Contract, now in code and in 00-ARCHITECTURE.md
- **Ownership:** the store that owns `p` is the nearest element of `p`'s own absolute path named `.qompack`. The name is case-folded on Windows, as `filepath.Rel` does. If that directory exists, `WriteAtomic` stages in its `tmp/`. If it does not, no store owns `p`, and the write stages beside `p` and makes `p`'s directory.
- **No escape:** the walk never looks beside the path, and never passes over the nearest store to an outer one.
- **User-global layer:** it is reached only by its own path. `paths.Global(home)` is on the path, so its store owns what is under it. `tokens.DefaultCalibPath` now names the file through `paths.Global` explicitly.
- **§7.4 guard:** it is asked about the absolute path, of every existing store on the path, not only the owner.

### What changed from the draft (`d660423` → `1217e6a`)
- The draft skipped a missing nearest store and went on to an enclosing one. A store-less project inside another store's tree then failed its rename exactly as runs/21 did.
- The draft's guard asked only the owner, so a path *through* a stray `.qompack` in `checkpoints/` stayed unprotected.
- The Windows-only case-variant row moved to `atomic_owner_windows_test.go`, so no `t.Skip` was added.

`891f429` drops a struct field nothing read.

### Callers checked
- I read all 49 production call sites. They write into a project `.qompack` or into the user-global layer; eval's corpus writer, eval's importer and backup write to other directories, which no store owns.
- The only project-root resolution is `paths.Resolve`. It walks for `.git`, not `.qompack`, so it cannot escape into `~/.qompack`. It does have a home-as-root edge, listed under "Needs the owner".
- The other ancestor walks look for `go.mod` or `.git`.
- The three daemon seal rows (`a4cee62`) had only failed because of the escape. Their fixture now puts a regular file where the seal's directory would be. The assertions are unchanged.

### Evidence (Windows)
- **Red at base** (runs/03; git archive of `6aff949` plus the new test files): six owner rows fail, as does `TestStateRoundTrip_BelowAUserGlobalStore`, with the runs/21 rename error. The three rows that pin unchanged behaviour pass.
- **Red at the draft** (runs/01): `go test ./internal/paths -run '^TestWriteAtomic_AStrayStoreOnThePathCannotUnprotectWhatIsBelowIt$'` fails. `-run '^TestWriteAtomic_AStorelessProjectInsideAnotherStoresTreeStagesBesideItself$'` fails with the rename error.
- **Green:** runs/02, and the full `./internal/paths` at `c3e3519` in runs/14.
- **Incident reproduced without the real home** (runs/08): a fresh fake home with an empty `.qompack`, and TMP/TEMP below it.
  - Base: 6 ipc and 2 cli rows fail, and base stages in the fake home (`.qompack/tmp` appears).
  - HEAD: ipc passes, the incident rows pass, and the fake home stays untouched.
  - Both base and HEAD also fail `TestBackupCLI_ConsistentRestoreRetainsLaterSourceWrites` and `TestBackupCLI_UncertifiedSnapshotIsRefusedByEveryReader`. That is a separate long-path defect (see open items). The control run (runs/16) reproduces it with no `.qompack` anywhere above, on base and HEAD alike.
- The real `C:\Users\Quant\.qompack` does not exist and was never touched.

## 2. x/sys upgrade for GO-2026-5024

`4355fe3` changes only the two x/sys lines in go.mod and go.sum. x/sys v0.44.0 requires go 1.25.0 and no modules. `go mod tidy -diff` is identical at base and HEAD apart from the x/sys version; the stale sums it lists predate this branch (runs/04). `THIRD_PARTY_NOTICES.md` changes the version only, and two comments no longer name the old version.

Results (runs/05, runs/06):
- `go run ./tools/devtool licenses --check`: current.
- Pinned govulncheck (`go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...`): no vulnerabilities in called code or imported packages. The base listed GO-2026-5024 in `x/sys@v0.33.0`.
- `go run ./tools/devtool build-all`: exit 0.
- `go run ./tools/devtool lint --only=bindeps`: OK, 6 targets checked.
- paths, store and daemon on both OSes: see "Final package runs".

## 3. `TestGC_DeadlineOvershootIsBoundedByTheCheckInterval` fails alone on Linux

### Verdict
This is a test defect, aggravated by a real product inefficiency. It is not a GC overshoot defect.

### Evidence it is the test
- **Base fails 3 of 3 alone** in the container: `--run '^TestGC_DeadlineOvershootIsBoundedByTheCheckInterval$' --count 3` at `6aff949`, message "host could not be measured".
  - Every trail oscillates. Budgets of 118–179 ms stop at the first check after 194–302 ms; the re-priced budgets of 697–970 ms sweep everything.
  - That is an additive phase missing from calibration, which the retry's multiplicative re-price cannot fix.
  - w4-syncs independently sees the same red alone at `6aff949` (their runs `cx-w4-syncs-gc-overshoot-base-6aff949-*`, read-only).
- **Phase probe.** A throwaway test on unreachable local commits: `4570089` is `6aff949` plus the probe, `d66db7b` is `4316e06` plus the probe.
  - The window's first-check end was timed over mark, live-set write and sweep only: 46–81 ms.
  - A whole GC call with an already-spent deadline takes 167–311 ms of its own clock to reach that check.
  - The difference is `recordOutcomes`, 104–134 ms under -race; the paused seat's probe measured 171–292 ms.
- **Allocation red at base** (runs/09): 222,108 allocations for 3,072 dead roots, against 18 at HEAD.

### Evidence it is not GC overshooting
In every failed attempt, the pass either stopped exactly on a check after outliving its budget, or swept everything; those assertions still pass. The time before the first check is pre-sweep work, which the deadline is documented not to bound, like tombstoning.

### Fix
- The first-check end is priced through a real GC call's `GCReport.Duration` (`f679892`, `dd24a58`). The retry re-prices on that same clock.
- `recordOutcomes`, `applyQuota` and the tombstone sorts compare raw hash bytes instead of `String()` forms (`91db668`). The order is identical, pinned by `TestGCHashOrder_IsTheTextOrder`.
- The head probe shows `recordOutcomes` at 10–15 ms under -race.

### After the fix
- **Linux:** 5 of 5 alone at `c3e3519`, together with the two sort-order rows (`--count 5`). Every run landed on attempt 1, with windows of 3.55–5.09x and overshoot within its limit.
- **Windows:** passes in the full `./internal/store` runs (runs/07, runs/14).

## Final package runs
### Windows (co-loaded, one package at a time)
- runs/14 at `c3e3519`: paths, ipc, tokens, cli and store all ok.
- runs/07 at `c3e3519`: contract, observer, obs, rehydrate, sketch, dag, mcp, eval and testutil ok. daemon and checkpoint each had one wall-clock red; both rows pass 10 of 10 alone at HEAD and base:
  - `go test ./internal/daemon -run '^TestObservePromptBlockingVariantTimesOut$' -count=10` (runs/12)
  - `go test ./internal/checkpoint -run '^TestPreCompactDerivesItsBudgetFromTheCallersDeadline$' -count=10` (runs/13)
- runs/07's paths and ipc were built at `4316e06`, and tokens, cli and store at `891f429`. That is why runs/14 re-ran those packages at `c3e3519`.

### Linux (non-root, -race, `pkgs-final` at `c3e3519`)
All 16 packages report; 14 pass. The reds in daemon (8) and mcp (1) all pass alone:
- `TestSpoolWatch_*`: 15 of 15 on each of HEAD and base (`--run '^TestSpoolWatch_' --count 3`).
- `TestSessionStartCompact_*`: 30 of 30 on each of HEAD and base (`--run '^TestSessionStartCompact_' --count 3`).
- `--run '^TestBudgetBF$'`: passes on both (p95 213 ms and 197 ms).
- `--run '^TestDeliveryOrder_LaneOverflowIsDrainedOnRequest$'`: HEAD 48 of 50, base 50 of 50. HEAD's two failures were the first two iterations of one run, whose start overlapped another workstream's container benchmark. The other 40 head runs were interleaved with base and all passed. No mechanism connects this branch to that drain path.
- `--run '^TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget$' --count 5`: 5 of 5, confirming it is fixed after `20af0e6`.

### test/integration hot-path row
`go test ./test/integration -run '^TestIntegration_HotPathWarmWithRealResidentState$'`, alone (runs/17), fails identically on base and HEAD. It is not this branch.

### Static checks (runs/10, runs/11)
- fmt-check: ok.
- vet on windows, linux and darwin for the five touched packages: ok.
- lint: nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers and coveragefloors pass. runpatterns fails only on `w3-e2ereds/report.md` lines 107 and 119, which come from the integration branch.
- Pinned golangci-lint on the touched packages: two findings in untouched `internal/daemon` files, both present at base.
- `go test ./test/docs`: ok.

## Criterion changes
1. **The overshoot test's first-check measurement.**
   - Old: mark, live-set write and sweep, timed on the wall clock. Retry re-priced on the wall clock.
   - New: a whole GC call's `GCReport.Duration`, with the retry on the same clock.
   - Why: the judged attempts are whole GC calls, whose deadline is armed at GC's start. Pricing a subset left out an additive phase. `Duration` leaves out only the cursor write, which comes after the check that stops the pass, so it cannot move where a budget expires. The Full end was already `Duration`.
   - Unchanged: every bound, the attempt count and every assertion. Overshoot is still judged on the wall clock, and the host-sanity inequality is the same, now comparing one clock with itself.
2. **Three daemon seal rows' fixture** (`a4cee62`): a missing directory became a path under a regular file. The old fixture failed only through the escape bug. The downgrade must still fail, and the same residual assertions hold.

## Open items
- **Backup long-path defect (pre-existing, Windows).** `qompack backup create` fails when a project's `.qompack` path is longer than 240 characters. `TakeBackup` walks `paths.Long(m.l.Dot)`, which adds the `\\?\` prefix. `maintNoFollow` then calls `filepath.Rel(m.root, p)` with an unprefixed anchor. Evidence: runs/15 and runs/16 at base and HEAD. This is outside my scope; it needs a store owner.
- **Hot-path row deferrals.** The row defers exactly 593 of 2,130 requests to the spool, on base and HEAD alike. The same count appears in more than 20 earlier logs of other workstreams, so it is systematic, not co-load noise. This belongs to C5.1.
- **golangci-lint in `internal/daemon`**, present at base: `handlers.go:803` (gocritic ifElseChain) and `spawn_stage_test.go:312` (errcheck). C3.5 is red on these regardless of this branch.
- **Other module vulnerabilities, not called:** govulncheck still lists x/mod v0.25.0 (GO-2026-6180 and -6179, fixed in v0.40.0) and klauspost/compress v1.18.0 (GO-2026-5841, fixed in 1.18.7). Both were out of scope.
- **GC pre-sweep phases** still ignore the deadline: the live-set fsync, tombstoning, and now-cheap `recordOutcomes`. This is the documented gap, unchanged.
- **Cleanup:** local throwaway branches `w3-paths-gcprobe` (`c02b67b`), `w3-paths-gcprobe-base` (`4570089`) and `w3-paths-gcprobe-head` (`d66db7b`) are never to be merged. The container `/work/cx-w3-paths-*` directories remain, including the paused seat's killed `final-pkgs-dd24a58`.
- `test/guards` was not run; its known red awaits the Phase 2 dispositions.

### Commits

- d660423 fix(paths): own a path only by the .qompack on its own path (earlier seat; kept, revised by 1217e6a)
- 63c3d07 test(ipc): round-trip state below a user-global store (earlier seat; kept)
- 4355fe3 fix(deps): upgrade golang.org/x/sys to v0.44.0 (earlier seat; kept, re-verified)
- f679892 fix(store): price the gc overshoot window through GC itself (earlier seat; kept)
- 91db668 perf(store): sort gc outcomes and tombstones without text forms (earlier seat; kept)
- a4cee62 test(daemon): fail seal downgrades on a path that cannot be made (earlier seat; kept)
- dd24a58 fix(store): price the gc overshoot window on GC's own clock (earlier seat; kept)
- 80deba0 chore(v6): merge closeout/integration 6aff949
- 1217e6a fix(paths): stop at the nearest store, guard with every store
- 8d65ca2 refactor(tokens): name the calibration file through paths.Global
- 4316e06 docs(store): say which clock the gc attempt model prices on
- 891f429 refactor(paths): drop pathOwner's unread absolute path
- c3e3519 docs(store): cite both gc phase probes and the base re-run
- ef2f98c docs(closeout): record the w3-paths runs (evidence only)

### Tests

- `go test ./internal/paths (owner rows + TestTmpDirFor_IsTheOwningStoresTmpOrBesideTheTarget + TestOwnerOf_AsksEveryStoreOnThePathForProtection), git archive 6aff949 + new test files (runs/03)` — RED as intended: 6 owner rows + TestStateRoundTrip_BelowAUserGlobalStore fail with the runs/21 rename error; 3 keep-behaviour rows pass
- `go test ./internal/paths -run '^TestWriteAtomic_AStorelessProjectInsideAnotherStoresTreeStagesBesideItself$' against the draft walk (runs/01)` — RED as intended (rename into the unmade directory)
- `go test ./internal/paths -run '^TestWriteAtomic_AStrayStoreOnThePathCannotUnprotectWhatIsBelowIt$' against the draft walk (runs/01)` — RED as intended (not refused)
- `go test ./internal/paths ./internal/ipc ./internal/tokens ./internal/cli ./internal/store -count=1 -timeout=30m, one at a time, Windows, c3e3519 (runs/14)` — all ok
- `go test <daemon contract checkpoint observer obs rehydrate sketch dag mcp eval testutil> -count=1, Windows, c3e3519 (runs/07)` — ok except one wall-clock row each in daemon and checkpoint; both pass 10/10 alone at HEAD and base (runs/12, runs/13)
- `fake-home incident repro: TMP=TEMP below <fakehome> with an empty .qompack, go test ./internal/ipc ./internal/cli, base 6aff949 vs head c3e3519 (runs/08)` — base: 6 ipc + 2 cli incident rows FAIL and base stages into the fake store; head: ipc ok, incident rows pass, fake store untouched; both fail 2 TestBackupCLI rows from path length (control runs/16)
- `go run ./tools/devtool licenses --check; go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./... (runs/05)` — licenses current; GO-2026-5024 gone (present at base); 0 vulnerabilities in called code
- `go run ./tools/devtool build-all; go run ./tools/devtool lint --only=bindeps (runs/06)` — exit 0; bindeps OK (6 targets)
- `linux-nonroot-gate.sh 6aff949 --run '^TestGC_DeadlineOvershootIsBoundedByTheCheckInterval$' --count 3 -- ./internal/store` — FAIL 3/3 at base ('host could not be measured', oscillating trails)
- `linux-nonroot-gate.sh c3e3519 (overshoot row + two sort-order rows) --count 5 -- ./internal/store` — PASS 15/15; every overshoot run lands on attempt 1; 18 allocations
- `go test ./internal/store -run '^TestRecordOutcomes_SortingAllocatesNothingPerComparison$' on git archive 6aff949 (runs/09)` — RED as intended: 222108 allocations
- `linux-nonroot-gate.sh c3e3519 pkgs-final -- 16 packages (paths store daemon ipc cli tokens + WriteAtomic callers)` — 14 PASS; daemon 8 and mcp 1 red, all pass alone at HEAD and base (spoolwatch 15/15, sessionstart-compact 30/30, BudgetBF pass, LaneOverflow 48/50 head vs 50/50 base, first two head iterations overlapped another workstream's container benchmark)
- `linux-nonroot-gate.sh c3e3519 --run '^TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget$' --count 5 -- ./internal/daemon` — PASS 5/5 (fixed after 20af0e6)
- `go test ./test/integration -run '^TestIntegration_HotPathWarmWithRealResidentState$' alone, base then HEAD (runs/17)` — FAIL on both, identical 593/2130 deferral (pre-existing, not this branch)
- `devtool fmt-check; GOOS={windows,linux,darwin} go vet five touched packages; devtool lint (all but stubskips and golangci-lint); go test ./test/docs (runs/10, runs/11)` — pass, except runpatterns on w3-e2ereds/report.md:107,119 (integration's)
- `pinned golangci-lint on paths/store/daemon/ipc/tokens, GOOS=windows/linux/darwin (runs/10)` — 2 findings in untouched internal/daemon files, identical at base 6aff949

### Criterion changes

- TestGC_DeadlineOvershootIsBoundedByTheCheckInterval: the FirstCheck calibration is now one whole GC call with an already-spent deadline, read from GCReport.Duration (was mark+writeLiveSet+sweep on the wall clock), and the retry re-prices on GCReport.Duration (was the wall clock). Rationale: judged attempts are whole GC calls whose deadline is armed at GC's start; the subset omitted recordOutcomes, an additive cost the multiplicative re-price cannot absorb. Duration excludes only the cursor write, which follows the stopping check and cannot move where a budget expires; the Full end was already Duration. Unchanged: every bound, the attempt count, all assertions, wall-clock overshoot judgement, and the host-sanity inequality (now one clock on both sides).
- Daemon rows TestBorrowedLease_ReportsResidualOnlyAfterOwnerRelease, TestDeliverySeal_AFailedDowngradeIsRecordedAndStillReleases and TestDeliverySeal_AResidualReachesTheOperatorFromStop (a4cee62): the forced-failure seal path is now under a regular file instead of a missing directory. Rationale: the old fixture failed only through the WriteAtomic escape bug; the downgrade must still fail and the same residual assertions hold.

### Open issues

- Pre-existing Windows defect: qompack backup create fails when the project's .qompack path exceeds 240 chars (TakeBackup walks paths.Long(Dot) so paths get \\?\; maintNoFollow does filepath.Rel against the unprefixed m.root). Evidence runs/15, runs/16 at base and head. Out of scope; route to a store owner.
- Hot-path row TestIntegration_HotPathWarmWithRealResidentState fails identically at base and head with exactly 593/2130 deferred (same count in 20+ earlier logs): systematic, not co-load noise (C5.1).
- golangci-lint pre-existing at base in internal/daemon: handlers.go:803 gocritic ifElseChain, spawn_stage_test.go:312 errcheck; C3.5 red regardless of this branch.
- runpatterns lint red on plans/sdd/V6-closeout/w3-e2ereds/report.md:107,119 (integration-owned).
- govulncheck module-level, not called: golang.org/x/mod v0.25.0 (GO-2026-6180, GO-2026-6179, fixed v0.40.0) and klauspost/compress v1.18.0 (GO-2026-5841, fixed 1.18.7).
- GC pre-sweep phases (live-set fsync, tombstoning, recordOutcomes now 10-15 ms under -race) still do not consult the deadline: documented gap, unchanged.
- TestDeliveryOrder_LaneOverflowIsDrainedOnRequest: head 48/50 vs base 50/50 alone on Linux; the 2 fails were the first iterations of one run overlapping another workstream's container benchmark; no mechanism links this branch; verify seat may want one more sample.
- Throwaway local branches w3-paths-gcprobe (c02b67b), w3-paths-gcprobe-base (4570089), w3-paths-gcprobe-head (d66db7b) are unreachable from HEAD and never to be merged; container /work/cx-w3-paths-* dirs remain.
- test/guards not run; its known red awaits the Phase 2 dispositions.

### Needs the owner

- paths.Resolve walks for .git, so a session in the home directory itself, or below a home that is a git work tree (dotfiles repo), resolves the project root to the home and the project store becomes the user-global ~/.qompack. Not changed (§3.3 defines the order); decide whether Resolve should refuse or relocate that case.
- Coordinator: route the backup long-path defect (store maintenance) and the pre-existing internal/daemon golangci-lint findings to their owners (startroute owns internal/daemon).

## Independent review

### review:paths: needs-fixes

- **minor** `internal/paths/appendonly.go:52-76 (mayBeProtected + OpenFile fast path)` — The branch says relative spellings of protected paths are now guarded (d660423 message; ownerOf doc at internal/paths/atomic.go). That holds for WriteAtomic and OpenSharedRW, but not for OpenFile or AppendOnly. OpenFile's mayBeProtected fast path still reads the path text as given, while IsProtected is now asked about filepath.Abs(p). A relative path that doesn't spell checkpoints/pins/tried.bloom, because the protected directory is in the cwd, skips the guard. The mayBeProtected doc comment ("a literal suffix of filepath.Clean(p)") is also now false, since the suffix is of Clean(abs). This is not a regression, because base never protected relative paths, but the fix the commit claims is incomplete.
  - Evidence: Probe on a git-archive copy of HEAD (scratch, since deleted): EnsureLayout, AppendJSONL(pins/invariants.jsonl), t.Chdir(l.Pins), then paths.OpenFile("invariants.jsonl", O_WRONLY|O_TRUNC). Result: err=<nil>, and pins/invariants.jsonl is truncated to "". The same probe with WriteAtomic("0001.json") after t.Chdir(l.Checkpoints) is correctly refused. TestWriteAtomic_RefusesAProtectedPathNamedRelatively passes only because its relative spelling ".qompack/pins/invariants.jsonl" still contains "pins".
  - Fix: In OpenFile, check mayBeProtected against the absolute path: if !filepath.IsAbs(p), resolve abs with filepath.Abs first, or skip the fast path for relative p. Then fix mayBeProtected's comment to say it reasons over the absolute path. Add a row to TestWriteAtomic_RefusesAProtectedPathNamedRelatively that chdirs into l.Pins and opens "invariants.jsonl" with O_TRUNC through OpenFile and AppendOnly/OpenSharedRW.
- **nit** `commits 63c3d07, a4cee62, 8d65ca2, 891f429, 4316e06, c3e3519` — Six non-merge commits lack the Refs footer that the branch's other code commits carry (Refs: V6-VERIFY, C3.5/C3.7). Three of them change code or tests: 63c3d07 test(ipc), a4cee62 test(daemon) and 8d65ca2 refactor(tokens). No attribution trailers were found, and all subjects are conventional.
  - Evidence: git log --format=%B 6aff949..HEAD: only d660423, 4355fe3, f679892, 91db668, dd24a58 and 1217e6a have a 'Refs:' line.
  - Fix: If the coordinator folds or rebases this branch, add 'Refs: V6-VERIFY, C3.5/C3.7' to those commits. Otherwise record the gap in the workstream report.
- **nit** `internal/config/load.go:51, internal/config/capture_load.go:150` — Task (1) said the user-global layer must use its own root explicitly. tokens.DefaultCalibPath now names it through paths.Global (8d65ca2), but the user-config loaders still hand-join HomeDir with ".qompack". These are reads only, and ownerOf would own a write there correctly because the store is on the path, so this is a consistency gap, not a behaviour bug.
  - Evidence: grep '".qompack"' on non-test .go files lists these two loaders (plus liveeval.go and grammar's self-marker), alongside the paths.Global definition in internal/paths/resolve.go:54.
  - Fix: Replace filepath.Join(env.HomeDir, ".qompack", "config.json") with filepath.Join(paths.Global(env.HomeDir), "config.json") in both loaders, if config may import paths without a cycle. Otherwise note why it cannot.

## Fix seat (review resolution) — status `done`, head `a9737bb49a20e5ec51f5b3ee3e84f96d6d4799a6`

### Root cause

OpenFile's fast path called mayBeProtected(p) on the path text exactly as the caller gave it, while ownerOf (since d660423) asks every store about filepath.Abs(p). A relative spelling whose protected element comes from the working directory ("invariants.jsonl" from inside pins/, "..\\invariants.jsonl" from pins/sub, "0001.json" from inside checkpoints/) contains none of the three names, so the guard was skipped and O_TRUNC truncated the pins log. The pre-branch base 54a4334 never guarded any relative spelling, so this is not a regression; it was an incomplete fix. Fixed by making a relative p absolute before the fast-path test.

### Summary

## w3-paths fix seat: final state

Branch closeout/w3-paths, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w3-paths. HEAD is a9737bb and the tree is clean. Nothing was pushed or tagged.

The reviewer's one finding was real. It is now fixed with a regression test that failed first. The implementer's delivery for tasks (1)-(3) is unchanged. It sits on this branch before and after the 80deba0 merge of integration 6aff949:
- d660423 and 1217e6a: a write stages only in the store that owns its path, and the §7.4 guard asks every store on the path.
- 63c3d07: test for ipc state written below a user-global store.
- 4355fe3: golang.org/x/sys upgraded to v0.44.0.
- f679892 and dd24a58: GC overshoot window priced on GC's own clock.
- 91db668: GC sort performance.
- 8d65ca2: calibration file named through paths.Global.
- 891f429, 4316e06, c3e3519: refactor and docs follow-ups.

## Review resolution

**Finding (minor): OpenFile/AppendOnly fast path reads a relative path's text, so the guard is skipped. CONFIRMED and FIXED in 34ddb55.**

Root cause:
- OpenFile's fast path called `mayBeProtected(p)` on the path text exactly as the caller gave it.
- ownerOf, since d660423, asks every store about `filepath.Abs(p)`.
- A relative spelling can leave out the protected element because the working directory supplies it. Three examples: `invariants.jsonl` from inside pins/, `..\invariants.jsonl` from pins/sub, and `0001.json` from inside checkpoints/. None of these contains "pins", "checkpoints" or "tried.bloom", so OpenFile skipped ownerOf and let O_TRUNC through.
- mayBeProtected's doc comment ("a literal suffix of filepath.Clean(p)") was also wrong once the guard began asking about the absolute path.

Evidence:
- **Red, before the fix** (runs/fix/01-relative-guard-red.log). The new rows of `go test -count=1 -run 'TestWriteAtomic_RefusesAProtectedPathNamedRelatively' -v ./internal/paths/`:
  - FAILED for the rows "from inside checkpoints/", "from inside pins/" and "from below pins/ through ..".
  - `OpenFile("invariants.jsonl", O_WRONLY|O_TRUNC)` returned err=nil.
  - `OpenFile("0001.json", O_TRUNC)` was stopped only by the file's read-only attribute ("Access is denied"), not by the guard.
  - The existing "from the project root" row passed, which is why the old test missed this: its spelling `.qompack\pins\...` still contains "pins", as the reviewer said.
- **Not a regression, as the reviewer said** (runs/fix/03-base-probe-relative-and-case.log; probe source in runs/fix/probes/). A probe on a git-archive of the pre-branch base 54a4334 (d660423^) shows the base truncated the pins log through a relative spelling both from inside pins/ and from the project root. The base never guarded any relative path.

Fix (internal/paths/appendonly.go):
- OpenFile now makes a relative p absolute before asking mayBeProtected.
- An absolute p keeps the one-syscall fast path, with only an added `filepath.IsAbs` check.
- If Abs fails, OpenFile falls back to p's own text. That is sound because ownerOf guards nothing for such a p either.
- mayBeProtected's comment now states its proof over an absolute path. It also cites Windows full-path normalization (Long's list) and says why a relative p proves nothing.

Test:
- TestWriteAtomic_RefusesAProtectedPathNamedRelatively keeps its name and is now table-driven over four working directories: project root, inside checkpoints/, inside pins/, and pins/sub.
- Each row asserts that these are refused with ErrAppendOnly:
  - WriteAtomic of the checkpoint
  - OpenFile O_TRUNC of the checkpoint
  - OpenFile O_TRUNC and non-append O_WRONLY of the pins log
  - OpenSharedRW of the pins log
- Each row also asserts that AppendJSONL through the relative spelling still appends. That call goes AppendJSONL → AppendOnly → OpenFile with O_APPEND. Finally it checks that the checkpoint bytes are unchanged and the pins log equals its old contents plus the one new record.
- The helper `requireOpenRefused` closes any handle a failing row opened.

On AppendOnly: it only ever opens with O_WRONLY|O_APPEND|O_CREATE, which the guard allows on a protected path. Its result is therefore the same whether or not the guard runs. The same holds for CreateNew (O_EXCL) and RestoreLog (O_APPEND). The only real exposure was direct OpenFile callers passing O_TRUNC or a non-append write flag.

## Commands and results (this seat)
Windows host (logs in plans/sdd/V6-closeout/w3-paths/runs/fix/):
- `go test -count=1 -run 'TestWriteAtomic_RefusesAProtectedPathNamedRelatively' -v ./internal/paths/`: FAIL before the fix (3 of 4 rows, exit 1), PASS after (exit 0).
- `go test -count=1 -timeout=30m ./internal/paths/` at 34ddb55: ok (exit 0).
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./internal/paths` with GOOS=windows, linux and darwin: exit 0 each.
- Pinned golangci-lint v1.64.8 on `./internal/paths/...` for windows, linux and darwin: exit 0 each. For linux and darwin it was a host-built binary run with GOOS set, because `go run` under a foreign GOOS builds a binary the host cannot run.
- `go run ./tools/lint/nomagic ./internal/paths/...`: exit 0.
- `go run ./tools/devtool lint --only=sleepcheck`: PASS.

Linux container, non-root uid 10001, -race, GOMAXPROCS=4, via `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-paths --out <Windows path> 34ddb55 ...` (artifacts in runs/fix/linux/):
- `--run '^(TestWriteAtomic_RefusesAProtectedPathNamedRelatively|TestAppendOnlyGuard|TestMayBeProtected_NeverHidesAProtectedPath|TestMayBeProtected_EveryProtectedLocationTakesTheWalk)$' --count 3 -- ./internal/paths`: PASS (pass=39 fail=0 skip=0), gate exit 0.
- Full `./internal/paths`: PASS (pass=162 fail=0 skip=18), gate exit 0. The 18 skips match the implementer's earlier c3e3519 Linux run (pass=158 skip=18). The four extra passes are the new subtests.

No wall-clock failure happened in any of these runs, so nothing needed re-running alone.

## Criterion changes
None. No check was weakened. The one edited test was only made stronger: the same name, with more rows and more assertions.

## Open items
1. **The §7.4 guard is text-based and does not handle case variants on a case-insensitive filesystem.** This was found while verifying the finding. It already existed at the base 54a4334, is Windows only as measured, and was not fixed here.
   - On Windows, `WriteAtomic(<root>\.qompack\CHECKPOINTS\0001.json)` replaced a sealed checkpoint. renameWithRetry clears its read-only attribute to do so.
   - `OpenFile(<root>\.qompack\PINS\invariants.jsonl, O_TRUNC)` emptied the pins log.
   - Both reproduce at HEAD and at the base (runs/fix/04-case-variant-probe-head.log and 03-base-probe-relative-and-case.log).
   - Cause: IsProtected compares `rel` against "checkpoints/", "pins/" and "sketches/tried.bloom" case-sensitively, and mayBeProtected uses a case-sensitive Contains. The store name itself (.QOMPACK) is case-folded on Windows by isStoreDirName.
   - Default macOS APFS is also case-insensitive while filepath.Rel is exact there, so .QOMPACK is probably exposed on macOS too (not tested).
   - Windows 8.3 short names and `::$DATA` stream suffixes are the same kind of textual hole.
   - No product caller builds such a spelling: every protected path comes from lowercase Layout fields. So this is defense-in-depth, not a live product defect.
   - Left out of this fix round because it is outside the reviewer's finding and needs a design choice about per-platform case folding.
2. From the implementer's static log: golangci-lint on ./internal/daemon reports an errcheck finding at internal/daemon/spawn_stage_test.go:312 and a gocritic finding at internal/daemon/handlers.go:803. Both reproduce at base 6aff949 and belong to the startroute scope, not to this workstream.

## Owner decisions
- Whether the §7.4 guard should fold case for protected directory names on case-insensitive filesystems (Windows, macOS). If yes, decide which workstream owns it and whether 8.3 short names and ADS suffixes are in scope or explicitly accepted as out of scope for a textual guard.

### Commits

- 34ddb550fc0b75df2c0294c2383be12c4ef3afc0 fix(paths): guard a relative path by its absolute spelling
- a9737bb49a20e5ec51f5b3ee3e84f96d6d4799a6 docs(closeout): record the w3-paths fix seat's runs

### Tests

- `go test -count=1 -run 'TestWriteAtomic_RefusesAProtectedPathNamedRelatively' -v ./internal/paths/ (Windows, before fix)` — FAIL as expected: 3 of 4 rows (inside checkpoints/, inside pins/, below pins/ through ..); OpenFile("invariants.jsonl", O_TRUNC) returned err=nil
- `go test -count=1 -run 'TestWriteAtomic_RefusesAProtectedPathNamedRelatively' -v ./internal/paths/ (Windows, after fix 34ddb55)` — PASS, all 4 rows
- `go test -count=1 -timeout=30m ./internal/paths/ (Windows, 34ddb55)` — ok, exit 0
- `go run ./tools/devtool fmt-check; go vet ./internal/paths with GOOS=windows/linux/darwin` — all exit 0
- `pinned golangci-lint v1.64.8 run ./internal/paths/... with GOOS=windows/linux/darwin; go run ./tools/lint/nomagic ./internal/paths/...; go run ./tools/devtool lint --only=sleepcheck` — all exit 0 / PASS
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-paths --out <win path> 34ddb55 fix-relative-guard-focused --run '^(TestWriteAtomic_RefusesAProtectedPathNamedRelatively|TestAppendOnlyGuard|TestMayBeProtected_NeverHidesAProtectedPath|TestMayBeProtected_EveryProtectedLocationTakesTheWalk)$' --count 3 -- ./internal/paths` — PASS pass=39 fail=0 skip=0 (non-root uid 10001, -race, GOMAXPROCS=4), gate exit 0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w3-paths --out <win path> 34ddb55 fix-paths-full -- ./internal/paths` — PASS pass=162 fail=0 skip=18 (same 18 skips as the implementer's c3e3519 run), gate exit 0

### Open issues

- The §7.4 guard misses case variants on Windows, and this is pre-existing (already at base 54a4334). WriteAtomic(<root>\.qompack\CHECKPOINTS\0001.json) replaces a sealed checkpoint, and OpenFile(<root>\.qompack\PINS\invariants.jsonl, O_TRUNC) empties the pins log. The cause is that IsProtected and mayBeProtected compare the protected directory names case-sensitively. macOS is probably exposed too (not tested), and Windows 8.3 short names and ::$DATA stream suffixes are the same kind of hole. No product caller builds such a spelling. Evidence: runs/fix/03-base-probe-relative-and-case.log and runs/fix/04-case-variant-probe-head.log.
- golangci-lint on ./internal/daemon reports an errcheck finding (spawn_stage_test.go:312) and a gocritic finding (handlers.go:803). Both reproduce at base 6aff949 and belong to the startroute scope, not to w3-paths.

### Needs the owner

- Should the §7.4 guard fold case for checkpoints/, pins/ and sketches/tried.bloom on case-insensitive filesystems (Windows, macOS)? If yes, which workstream owns it, and are 8.3 short names and ADS suffixes in scope or explicitly accepted as out of scope for a textual guard?

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


