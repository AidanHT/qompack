# Wave 20 status (candidate 8 pre-freeze audit fixes, D61(a))

Branch `closeout/w20-status`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `internal/contract/assertions.go:111-130 (checkSessionStartFires); docs/troubleshooting.md:153-160`: The same-session-restart branch only applies when h.LastSessionID == the restarting session. When two sessions are open in one project at once (two Claude Code windows on one repo, which the daemon supports because session ends run concurrently since C1.15), every compaction or --resume of a session that is not the most recently started one finds its own marker, fails the `rec.Session != e.Event.SessionID` test, and then counts as an absence because LastSessionID names the other session. One such compaction leaves the row pending (marker-absent-once), which is exactly what F-C48-1 / D58(d) was meant to remove. Two of them, interleaved, push StartsWithoutMarker to 2: a SevCritical failure, and the daemon degrades to degraded-passive on a healthy store where every hook fired. This is older than candidate 8, but w18-ssfires fixed only the single-session case. Its docs say 'a compaction's own start, or a --resume that keeps the session id ... reads same-session-restart' and 'That stays true after a compaction or a --resume of the session', with no conditions attached. Two conditions are left out: it does not hold when another session started in between, and it does not hold when the session's own start already counted an absence. D60(f)'s live re-check runs 'two status reads with two or more sessions'. If those sessions overlap and either one compacts, that check reads pending and fails.
- **major** `internal/cli/fsck.go:1054-1075 (checkFiles), 1782-1791 (comparePinsView), 2110-2112 (checkSpool), 796 (resolveRootLine label map literal), 2345-2361 (checkFidelity)`: `qompack fsck` (text and --json) violates D53(a). Five checks append defect or note lines while ranging over a map: the files log and view, the pin id sets, drain state.json, the three pointer labels, and s.roots. So two fsck reads of an unchanged damaged store list their detail lines in different orders. Past fsckMaxDetail (20), they also name different members, because the cap keeps whichever 20 the map yields first. D59(c) swept only status and doctor; fsck was never swept.
- **minor** `test/e2e/v5_x14_test.go:337-343`: The new F-C48-1 status check only asserts inside `if res.ID == contract.CSessionStartFires`. If e2eStatus's Contract list ever stops carrying session_start.fires (renamed, filtered, or status degraded to a partial answer), the loop asserts nothing and the 'banner stays 0 pending' claim passes vacuously. The later compact[...] assertions at :383-384 read the recorded contract map, not the status read.
- **minor** `docs/troubleshooting.md:152-156`: The ssfires review nit was left unfixed. The doc says without qualification that a compaction's own start, or a same-id --resume, 'reads same-session-restart, which holds'. The code returns it only when StartsWithoutMarker == 0. A session whose own start counted an absence stays pending (marker-absent-once) after it compacts, and TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour pins that.
- **minor** `internal/cli/dispatch_test.go:31 (fakeClock) — pre-existing test leak, observed in both trees`: An internal/cli test writes client spool files into the repository root's .qompack/spool of whichever worktree runs it, instead of into a temp project. Git ignores the files, so the chain's cleanliness checks pass and results are unaffected. It is still a test escaping its sandbox, and it accumulates in INT and CAND during the pre-freeze and release-check.
- nit `internal/eval/livetask.go:435-441 (keysOfBool, used at :408); internal/eval/ledger.go:293,337,382,389,416; internal/eval/provenance.go:152`: Error messages for malformed eval inputs depend on map order. keysOfBool lists the allowed check commands unsorted. The validators range over maps or map literals and return the first bad field, so an input with two or more problems gets a different message on each run. Those messages reach `qompack eval` users.
- nit `internal/checkpoint/current_work_fork_test.go:23-24; internal/daemon/status_order_test.go:21-24`: Two test comments still overstate their evidence; both review nits were left unfixed. The forkwork test header says current work comes from the session's own prompt records 'newest first', but the code takes own[len(own)-1] and does not fall back to an older prompt. The status-order comment says every base run failed 'each by the fourth read', but the statusorder reviewer saw one fail at read 5.

## impl:status: status `done`, head `70dab7334a85f81ec8125b7d65e05fa7dd740418`

### Root cause

(1) checkSessionStartFires recognized a session's own restart only through the project-wide LastSessionID, which names only the most recently started session, so overlapping sessions miscounted. (2) fsck and the eval validators built user-visible lists, and picked the 'first' error, by ranging over Go maps, whose order is randomized. (3) The x14 status check had no presence assertion. (4) In-process hook tests with no QOMPACK_PROJECT_ROOT take their first root from the process cwd (the checkout), and a degraded delivery is never re-rooted.

### Summary

PRODUCT CODE CHANGED (internal/contract/assertions.go, internal/cli/fsck.go, internal/eval/{ledger,livetask,provenance}.go).

All five findings and both nits were re-verified on 738d67c7 and hold. Every fix has a row that fails on the base and passes at HEAD. The commits are on closeout/w20-status (worktree qompack-cx-w20-status), six on top of 738d67c7, unpushed.

1. session_start.fires with overlapping sessions (major, fixed). Cause: checkSessionStartFires (internal/contract/assertions.go) took a start as the session's own restart only when History.LastSessionID named it. With two sessions open, the one that started first compacts while LastSessionID names the other, so its own PreCompact marker counted an absence. One such compaction left the row pending; two interleaved made it SevCritical.
   - Fix: a start whose marker names its own session is now that session's restart if either (a) it is the last-seen session (the old rule) or (b) its Source is compact or resume (new sessionRestartSource, with constants sessionSourceCompact and sessionSourceResume).
   - A restart counts nothing and moves neither StartsWithoutMarker nor LastSessionID. It reads same-session-restart only when the count is 0, marker-absent-once at 1, and failing at 2. A startup or clear start that finds a marker naming itself still counts an absence, as before.
   - No wire-shape change and no new history field. TestGuard_LastSessionIDScannerFindsTheOwnedWrites still finds exactly three writes.
   - New rows: TestSessionStartFires_OverlappingSessionsRestartsHold (red on base: step 1 read marker-absent-once), TestSessionStartFires_OverlappingRestartKeepsACountedAbsence (red on base: the count reached 2 and r.OK was false), and TestSessionStartFires_FreshStartNamingItselfStillCounts (pins the source edge case a skeptic raised). TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour and the other existing restart rows pass unchanged.
   - docs/troubleshooting.md, session_start.fires paragraph only: it now says a session's own compaction or --resume counts nothing even when another session started after it. It reads same-session-restart only while starts_without_marker is 0, stays marker-absent-once at 1 and failing at 2. The closing sentence now says "any of its sessions".

2. fsck detail order (fixed). Five loops ranged over Go maps: checkFiles (log and view), comparePinsView (both id sets), checkSpool (drain state), the pointer-label map literal in resolveRootLine, and checkFidelity over s.roots. Each now walks slices.Sorted(maps.Keys(...)); the labels use a fixed array. I checked every other loop in fsck*.go: they walk slices, sorted ReadDir output or lexical WalkDir. Map-backed tallies are already rendered sorted.
   - New row TestFsck_RepeatedReadsOfADamagedStoreAgree seeds all five checks and requires 20 --json and 20 text reads to be byte-identical, and the capped files sample to be the first 20 paths in sorted order. Seeds: 25 view paths the log never mentions, 6 retired pin ids, 6 refused drain records, one root with three dangling pointers, and 4 roots that restore as corrupt.
   - On the base fsck.go the first comparison differed in all five lists.

3. eval ordering (nit, fixed). keysOfBool now sorts. validateCategories and RateSchedule.Validate walk sorted keys of Reported and PerMillion. validateMoney, the RateSchedule provenance and multiplier checks, BaselineProvenance.Validate and Comparable's earlier/later caveats now iterate fixed ordered arrays instead of map literals.
   - New rows TestEvalValidators_NameTheSameProblemEveryRead (7 subtests, each red on base 20/20 runs) and TestKeysOfBool_ListsKeysInSortedOrder (red on base).

4. v5_x14 vacuous assert (fixed). A found flag plus require.True after the loop. The status read must now carry session_start.fires.

5. dispatch_test leak (holds; fixed in the file I own). The auditor's line (:31, testClock) only explains where the timestamp comes from. The real leak in this file was the TestDispatch_HookAlwaysExitsZero "unwritable store" case. It ran with noEnv, so the hook's first root was the checkout. Its degraded FileRead was never re-rooted, so a record was spooled into <checkout>/.qompack/spool and the case never met its unwritable store.
   - Fix: the "unreadable stdin", "unwritable store" and "panicking handler" cases now pin QOMPACK_PROJECT_ROOT to their temp project. A new helper, requireRootOutsideCheckout, runs on every case and fails if resolveProjectRoot would land inside the checkout. With the old setups and the new check, 18 subtests fail (3 faults x 6 hooks).
   - Other files outside my ownership still leak; see open_issues.

Nits (fixed, comments only): internal/checkpoint/current_work_fork_test.go now says the goal comes from the session's newest own prompt record, and an unreadable one leaves the goal unchanged. internal/daemon/status_order_test.go now says "within the first few reads".

One unrelated red in the full internal/daemon run: TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools failed once (details in open_issues).

Report rows (exact names): TestSessionStartFires_OverlappingSessionsRestartsHold, TestSessionStartFires_OverlappingRestartKeepsACountedAbsence, TestSessionStartFires_FreshStartNamingItselfStillCounts, TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour, TestFsck_RepeatedReadsOfADamagedStoreAgree, TestEvalValidators_NameTheSameProblemEveryRead, TestKeysOfBool_ListsKeysInSortedOrder, TestDispatch_HookAlwaysExitsZero, and TestV5_EveryContractAssertionHasARealProducer/full_composition_records_per_capability_evidence.

### Commits

- 2e4ba732 fix(contract): count no absence for another session's restart
- 5c2b019a fix(cli): list fsck detail lines in sorted order
- 48d63d6c fix(eval): name malformed-input problems in a fixed order
- 3fe4a099 test(e2e): require session_start.fires on the x14 status read
- 2564e032 test(cli): pin the dispatch hook faults to temp projects
- 70dab733 test: correct two stale evidence comments

### Findings resolution

- **fixed**: status/major: checkSessionStartFires same-session-restart only for LastSessionID (internal/contract/assertions.go; docs/troubleshooting.md)
  - A marker naming the starting session is now its own restart when it is the last-seen session or the source is compact/resume. A restart counts nothing and moves neither field; it reads same-session-restart at count 0, marker-absent-once at 1, failing at 2. A startup or clear naming itself still counts. Red on base: TestSessionStartFires_OverlappingSessionsRestartsHold (step 1 read marker-absent-once) and TestSessionStartFires_OverlappingRestartKeepsACountedAbsence (r.OK false at count 2). The troubleshooting paragraph now states the exact conditions.
- **fixed**: status/major: fsck lists detail lines in map order in five checks (internal/cli/fsck.go)
  - checkFiles, comparePinsView, checkSpool and checkFidelity now walk sorted keys; resolveRootLine's label map literal became a fixed array. The rest of fsck*.go was swept and has no other map-ordered list. TestFsck_RepeatedReadsOfADamagedStoreAgree seeds all five checks and requires 20 --json and 20 text reads to be byte-identical, with the capped sample sorted. On the base fsck.go all five lists differed on the first comparison.
- **fixed**: regress/minor: test/e2e/v5_x14_test.go:337-343 asserts only inside an if
  - Added a found flag and require.True after the loop, so the row fails when session_start.fires is missing from the status read. The touched row passes 20/20 and 3/3 under -race.
- **fixed**: complete/minor: docs/troubleshooting.md:152-156 states same-session-restart unconditionally
  - The paragraph now says it holds only while starts_without_marker is 0, stays marker-absent-once (pending) at 1 until the next new session decides it, and stays failing at 2. After fix 1 it applies to any session, including when another session started later. test/docs, docmarkers and the generator --check runs pass.
- **partial**: night/minor: internal/cli test writes client spools into the checkout's .qompack/spool (dispatch_test.go:31 area)
  - Fixed in the file I own. TestDispatch_HookAlwaysExitsZero's unwritable-store case leaked six unavailable records through the noEnv first root; its noEnv cases now pin QOMPACK_PROJECT_ROOT to their temp project, and requireRootOutsideCheckout guards every case (18 subtests red on the base setups). Still leaking, outside my file ownership: hooks_test.go TestHooks_AllSixExitZeroWithValidJSON (6 unavailable records, noEnv) and the compactLoadRig (102 denied observe.tool records seen after one full cli run). See open_issues.
- **fixed**: nit: eval keysOfBool and validator error order (livetask.go, ledger.go, provenance.go)
  - Sorted keys for data maps and fixed arrays for map literals; Comparable's caveats are now earlier then later. TestEvalValidators_NameTheSameProblemEveryRead (7 subtests, red on base in 20/20 runs) and TestKeysOfBool_ListsKeysInSortedOrder (red on base).
- **fixed**: nit: stale test comments in current_work_fork_test.go:23-24 and status_order_test.go:21-24
  - Both were verified against the code and the review notes. They now read 'newest own prompt record (an unreadable newest record leaves the goal as it was)' and 'within the first few reads'. Comment-only changes.

### Tests

- `go test -p 1 -count=1 ./internal/contract/`: ok (full package)
- `go test -p 1 -count=1 ./internal/eval/`: ok (full package)
- `go test -p 1 -count=1 -timeout=30m ./internal/cli/`: ok 221.983s (full package)
- `go test -p 1 -count=1 -timeout=30m ./internal/checkpoint/`: ok 149.327s (full package)
- `go test -p 1 -count=1 -timeout=30m ./internal/daemon/`: FAIL: only TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools (11.73s, 'the first look's own spool was replayed'), a file my commits do not touch; it then passed 20/20 alone. My only internal/daemon change is a comment. See open_issues.
- `go test -p 1 -count=20 -timeout=30m -run '^TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools$' ./internal/daemon/`: ok, 0 failures in 20
- `go test -p 1 -count=20 -run '^TestSessionStartFires_OverlappingSessionsRestartsHold$' ./internal/contract/ (same for -race -count=3; likewise ^TestSessionStartFires_OverlappingRestartKeepsACountedAbsence$, ^TestSessionStartFires_FreshStartNamingItselfStillCounts$, ^TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour$, ^TestSessionStartFires_CompactOfSameSessionHolds$, ^TestSessionStartFires_CompactOfFirstSessionHolds$, ^TestSessionStartFires_ResumeOfSameSessionHolds$)`: all ok at -count=20 and -race -count=3
- `go test -p 1 -count=20 -run '^TestFsck_RepeatedReadsOfADamagedStoreAgree$' ./internal/cli/ ; same with -race -count=3`: ok / ok
- `go test -p 1 -count=20 -run '^TestDispatch_HookAlwaysExitsZero$' ./internal/cli/ ; same with -race -count=3`: ok / ok; no <checkout>/.qompack created
- `go test -p 1 -count=20 -run '^TestEvalValidators_NameTheSameProblemEveryRead$' ./internal/eval/ ; same with -race -count=3`: ok / ok
- `go test -p 1 -count=20 -run '^TestKeysOfBool_ListsKeysInSortedOrder$' ./internal/eval/ ; same with -race -count=3`: ok / ok
- `go test -p 1 -count=20 -timeout=30m -run '^TestV5_EveryContractAssertionHasARealProducer$/^full_composition_records_per_capability_evidence$' ./test/e2e/ ; same with -race -count=3`: ok 68.8s / ok 18.4s
- `red-first on base code: the same exact-name runs with the product file reverted to 738d67c7`: Overlapping contract rows FAIL; fsck row FAIL in all five lists; eval row FAIL 7/7 subtests in 20/20 runs; keysOfBool row FAIL; dispatch check FAIL in 18 subtests
- `GOOS=windows|linux|darwin go vet ./internal/contract/ ./internal/cli/ ./internal/eval/ ./internal/checkpoint/ ./internal/daemon/ ./test/e2e/`: clean on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run (same six packages)`: exit 0, no findings
- `go run ./tools/devtool fmt-check`: clean
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 1 -count=1 ./test/docs ; go test -p 1 -count=1 ./test/guards`: ok 9.8s ; ok 91.7s
- `go run ./tools/devtool gen-config-docs --check ; gen-command-docs --check ; gen-mcp-docs --check`: all report up to date

### Criterion changes

- TestDispatch_HookAlwaysExitsZero: the 'unreadable stdin', 'unwritable store' and 'panicking handler' cases now pin QOMPACK_PROJECT_ROOT to their temp project, and every case first requires the hook's first root to lie outside the checkout. Reason: the unwritable-store case was not exercising an unwritable store at all; it wrote the checkout's real .qompack. This tightens the criterion.
- TestV5_EveryContractAssertionHasARealProducer/full_composition_records_per_capability_evidence: the F-C48-1 status check now also requires the session_start.fires row to be present. Reason: it previously passed without asserting anything when the row was missing. This tightens the criterion.
- session_start.fires semantics (product): another session's own compaction or --resume no longer counts an absence or moves LastSessionID. Genuine absences and the two-session failure keep their behaviour, pinned by TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour and TestSessionStartFires_OverlappingRestartKeepsACountedAbsence.

### Open issues

- Checkout-root spool leak, remaining sources outside my file ownership (verified by running them, then cleaned up): (a) internal/cli/hooks_test.go TestHooks_AllSixExitZeroWithValidJSON dispatches all six hooks with Getenv noEnv, so its first root is the checkout. It wrote 6 'unavailable' records to <checkout>/.qompack/spool and never reached its temp project. Fix: Getenv envWith QOMPACK_PROJECT_ROOT=dir. (b) The compactLoadRig (sessionstart_compact_load_test.go hookE; also used by the precompact_* and segment_roll_live tests) passes noEnv plus a '--project' flag that no hook parses. TestSessionStartCompact_UnderSameSessionIngest alone wrote 'denied' observe.tool records (source_bytes 17354) there. Its Reads are therefore denied and never reach the rig's daemon, so those rows do not exercise what they claim. Fix: Getenv QOMPACK_PROJECT_ROOT=r.root, drop the dead --project, and assert the daemon recorded the Reads. Expect behaviour and timing changes in those rows. (c) BenchmarkHookNoop_InProcess (bench_test.go:48, noEnv), per the skeptic; not re-run by me (daytime limits). After (a) to (c), a package-level guard that <checkout>/.qompack never appears should be added.
- TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools (internal/daemon/precompact_settle_bound_test.go:162) failed once in a full internal/daemon run on a shared machine (11.73s vs about 3s normally; 'the first look's own spool was replayed'). It then passed 20/20 alone at HEAD. My only internal/daemon change is a comment in status_order_test.go, and the contract change runs only at SessionStart, not on the PreCompact settle path. It looks load-sensitive and needs an owner, given the no-flaky-tests directive.

### Needs owner

- New named constant evalOrderReads = 200 (internal/eval/order_test.go). Derivation: small Go maps randomize iteration by starting slot, so a two-entry map shows its rarer order about one read in eight. At 20 reads the two-key rows false-greened on the base in 2 of 10 runs. At 200 an accidental pass is below (7/8)^199, about 3e-12, and each read costs microseconds. Measured: red on base in 20/20 runs.
- New named constants fsckOrderReads = 20 and fsckOrderNeverLogged = fsckMaxDetail+5 (internal/cli/fsck_order_test.go). The 25-entry files list reorders on nearly every read (red at the first comparison on base); the extra 5 make the cap itself observable.
- Behaviour decision recorded: a SessionStart whose marker names itself counts as that session's own restart only for source compact or resume, or when it is the last-seen session. A startup or clear start finding such a marker still counts an absence, because the host mints a fresh id for those. Pinned by TestSessionStartFires_FreshStartNamingItselfStillCounts.
- Routing: hooks_test.go and the compactLoadRig files (sessionstart_compact_load_test.go, precompact_*_test.go, segment_roll_live_test.go, bench_test.go) need an owning seat for the remaining spool-leak fixes in open_issues.

## review:status:0:r1: verdict `needs-fixes`, 2 finding(s)

- **major** `internal/cli/sessionstart_compact_load_test.go:93-95 (compactLoadRig.hookE); also internal/cli/hooks_test.go:47,97,124,147,168,447 and internal/cli/bench_test.go:48`: The assigned night/minor finding (an internal/cli test writes client spools into the checkout's .qompack/spool) is only partly resolved. The seat fixed the one file it owns, dispatch_test.go, and that fix is correct. The package still leaks: compactLoadRig passes Getenv noEnv plus a '--project' flag that no hook parses, so each hook's first root is the checkout. A large Read admitted under that root is refused ('denied'), and a degraded delivery is never re-rooted, so every load Read is spooled into <checkout>/.qompack/spool and never reaches the rig's daemon. TestSessionStartCompact_UnderSameSessionIngest, the precompact_* rows and segment_roll_live therefore never apply the 'same-session ingest' load they claim to test. The audit also asked for a package-level guard that no test creates <repo>/.qompack, and none was added.
  - Evidence: I ran the row in a scratch git-archive copy of HEAD 70dab733 (with git init): `go test -p 1 -count=1 -run '^TestSessionStartCompact_UnderSameSessionIngest$' ./internal/cli/` printed ok 8.1s. It left <scratch>/.qompack/spool/client-9740.ndjson with 159 observe.tool records. All 159 read "outcome":"denied", and their source_bytes were 67195/67201/67241, the rig's 64 KiB load Reads, plus 3 of 17354. In the same copy, the seat's TestDispatch_HookAlwaysExitsZero created no .qompack. The seat itself reports this in open_issues and needs_owner as outside its file ownership.
  - Fix: Route this to a seat that owns these files. In compactLoadRig.hookE, use Getenv envWith(map[string]string{"QOMPACK_PROJECT_ROOT": r.root}), drop the dead "--project" argument, and assert the rig daemon actually recorded the Reads (for example, a non-zero observe.tool count in the rig store). Expect those rows' timing to change. In hooks_test.go (TestHooks_AllSixExitZeroWithValidJSON and the other noEnv sites) and BenchmarkHookNoop_InProcess, pin QOMPACK_PROJECT_ROOT to the test's temp dir. Then add a package TestMain (or a guard test) that fails if <module root>/.qompack exists after the run.
- **nit** `internal/eval/liveaccount.go:331-341 (beside)`: The eval ordering sweep left one user-visible list in map order. beside() ranges over ta.Delta (a map) and calls problem() inside the loop. If two models' running totals decrease in one turn, the 'main-loop ... exceeds the turn's ... running-total change' lines are appended to SessionAccount.Problems (serialized as JSON 'problems') in a different order on each run. This violates D53(a) in the same way the fixed validators did.
  - Evidence: `for model, d := range ta.Delta { ... if n := rest.negative(); n != "" { problem(...) } }` at liveaccount.go:331-341. problem appends to acc.Problems (liveaccount.go:208-211). The model loop just above it already walks sortedModels(...), so only beside() is unordered.
  - Fix: Walk the keys in order: `for _, model := range slices.Sorted(maps.Keys(ta.Delta)) { d := ta.Delta[model]; ... }`. Add a row with two models whose totals both decrease that requires identical Problems across about 200 reads.

## fix:status:r1: status `done`, head `bec822c86ff6551f58adc74053c02f6caa3f48c2`

### Root cause

(1) checkSessionStartFires recognized a session's own restart only through the project-wide LastSessionID, which names only the most recently started session. (2) fsck, the eval validators and the live account's beside() built visible lists by ranging over Go maps, whose order is randomized. (3) The x14 status check had no presence assertion. (4) In-process hook tests whose Env pins no QOMPACK_PROJECT_ROOT take their first project root from the process cwd, which is inside the checkout. The client scope guard refuses an out-of-project or unprovable payload, and a refused record is never re-rooted, so it was spooled into the checkout's .qompack. The compact load rig's `--project` flag was dead, so its load never reached its own daemon.

### Summary

PRODUCT CODE CHANGED (internal/contract/assertions.go, internal/cli/fsck.go, internal/eval/{ledger,livetask,provenance,liveaccount}.go).

All five audit findings and every nit are now fixed on closeout/w20-status (worktree qompack-cx-w20-status). The branch has ten commits on top of 738d67c7, unpushed; HEAD is bec822c8. Each fix has a row that fails on the old code and passes now.

## Round 1 (carried forward)
1. **session_start.fires with two sessions open (major, fixed).** checkSessionStartFires counted a session's own restart only when History.LastSessionID named that session. So when the older of two open sessions compacted, its own marker counted as an absence; two such compactions made the row SevCritical.
   - A marker naming the starting session is now its own restart if that session is the last-seen one or the source is compact or resume.
   - A restart counts nothing and changes neither field. The row reads same-session-restart at count 0, marker-absent-once at 1, failing at 2.
   - A startup or clear start whose marker names itself still counts an absence.
   - Rows: TestSessionStartFires_OverlappingSessionsRestartsHold and TestSessionStartFires_OverlappingRestartKeepsACountedAbsence (both red on base), plus TestSessionStartFires_FreshStartNamingItselfStillCounts.
   - The docs/troubleshooting.md session_start.fires paragraph now states these exact conditions.
2. **fsck detail order (fixed).** Five lists in fsck were built by ranging over Go maps, so their order changed between runs and the 20-line cap kept a different 20 each time. They now walk sorted keys or a fixed array. TestFsck_RepeatedReadsOfADamagedStoreAgree requires 20 --json and 20 text reads to be byte-identical; on the old fsck.go all five lists differed.
3. **eval validator order (nit, fixed).** keysOfBool and the validators now use sorted keys or fixed arrays. Rows: TestEvalValidators_NameTheSameProblemEveryRead and TestKeysOfBool_ListsKeysInSortedOrder.
4. **v5_x14 check that asserted only inside an if (fixed).** The status read must now carry session_start.fires.
5. **dispatch_test spool leak (fixed).** The fault cases now pin QOMPACK_PROJECT_ROOT to their temp project, and requireRootOutsideCheckout guards every case.
6. **Stale comments (nits, fixed).** current_work_fork_test.go and status_order_test.go.

## Review resolution
**Major: the checkout spool leak was only partly fixed. Verdict: holds, fixed.** No other wave-20 seat's findings mention hooks_test.go, sessionstart_compact_load_test.go or bench_test.go; only redeliver.json mentions segment_roll_live_test.go, which I did not edit. So I fixed the remaining sources here.
- **compactLoadRig.hookE (3f2da1b3).**
  - It dispatched hooks with noEnv plus a `--project` flag that no hook parses, so each hook's first root was the test process's cwd, inside the checkout.
  - A Read of a file under r.root is outside that project. The client scope guard (capture_admission.go scopeGuardCapture) records it as denied, and a refused record keeps the first root. Every prime and load Read was therefore spooled into <checkout>/.qompack/spool.
  - It now passes QOMPACK_PROJECT_ROOT=r.root and drops the dead flag. The rig records every Read id it builds.
  - New check requireReadsReachedTheRig runs after stop, which has already drained the spool and the WAL, so nothing in it waits. Every Read must be in the rig's index/tool_use.jsonl or its spool, and at least one must be indexed.
  - Red on the old hookE: "271 of the rig's 271 Reads reached neither its index nor its spool". After the fix: "the rig's daemon indexed 13 of its 82 Reads", and observer.tooluse rose from 0 to 17 in another run.
- **TestHooks_AllSixExitZeroWithValidJSON and BenchmarkHookNoop_InProcess (fbb32c13).** Both now pin QOMPACK_PROJECT_ROOT to their temp dir. Their payload, a FileRead with no tool_input, is recorded as unavailable, so on the checkout root it stayed there. Red before: 2271 bytes spooled into the checkout for the test, 18156 bytes for 50 benchmark iterations.
- **The other noEnv sites in hooks_test.go do not leak.** Lines 97, 124, 147, 168 and 447 send admitted payloads that are re-rooted to dir; most assert onlySpooledRequest(t, dir). TestHooks_RootReResolvesFromPayload needs noEnv by design. The full package passes under the new guard.
- **Package guard (bec822c8).** TestMain now snapshots <root>/.qompack/spool/client-<pid>.ndjson, where root is the hook client's first resolution with no environment, and fails the run if that file appeared or grew. It watches only this process's file, so hooks from a real session in the same checkout cannot trip it. It failed all three leakers before their fixes and passes the full internal/cli run (132s). The guard is the last commit, so every commit before it stays green.

**Nit: beside() listed its problems in map order. Verdict: holds, fixed (a43bc0f1).** It now walks usageModels(ta.Delta). The new row TestAccountHostStream_NamesBesideProblemsInModelOrder has two models whose totals both fall; on the old code it differed at read 10 of 200.

## Risk for tonight
The rig rows now apply real load to the daemon, which changes their behaviour. In one combined -count=20 run, TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook failed once with "its open-segment account is the restored one plus the new Read". That is the redelivery double-count the redeliver seat owns; its audit shows it 1/20 on base 738d67c7. The row then passed 20/20 alone and 3/3 under -race.

Rows to cite (exact names): TestSessionStartFires_OverlappingSessionsRestartsHold, TestSessionStartFires_OverlappingRestartKeepsACountedAbsence, TestSessionStartFires_FreshStartNamingItselfStillCounts, TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour, TestFsck_RepeatedReadsOfADamagedStoreAgree, TestEvalValidators_NameTheSameProblemEveryRead, TestKeysOfBool_ListsKeysInSortedOrder, TestAccountHostStream_NamesBesideProblemsInModelOrder, TestDispatch_HookAlwaysExitsZero, TestHooks_AllSixExitZeroWithValidJSON, TestSessionStartCompact_UnderSameSessionIngest, BenchmarkHookNoop_InProcess.

### Commits

- 2e4ba732 fix(contract): count no absence for another session's restart
- 5c2b019a fix(cli): list fsck detail lines in sorted order
- 48d63d6c fix(eval): name malformed-input problems in a fixed order
- 3fe4a099 test(e2e): require session_start.fires on the x14 status read
- 2564e032 test(cli): pin the dispatch hook faults to temp projects
- 70dab733 test: correct two stale evidence comments
- a43bc0f1 fix(eval): name beside-the-main-loop problems in model order
- 3f2da1b3 test(cli): route the compact load rig's hooks to its project
- fbb32c13 test(cli): pin the six-hook row and hook bench to temp projects
- bec822c8 test(cli): fail the run when a test spools into the checkout

### Findings resolution

- **fixed**: status/major: checkSessionStartFires counted same-session-restart only for LastSessionID (internal/contract/assertions.go; docs/troubleshooting.md)
  - A marker naming the starting session is its own restart when that session is the last-seen one or the source is compact or resume. A restart counts nothing and moves neither field. It reads same-session-restart at count 0, marker-absent-once at 1, failing at 2. A startup or clear start naming itself still counts an absence. TestSessionStartFires_OverlappingSessionsRestartsHold and TestSessionStartFires_OverlappingRestartKeepsACountedAbsence are red on base. The troubleshooting paragraph states the exact conditions.
- **fixed**: status/major: fsck listed detail lines in map order in five checks (internal/cli/fsck.go)
  - checkFiles, comparePinsView, checkSpool and checkFidelity walk sorted keys; resolveRootLine uses a fixed label array. The rest of fsck*.go was swept. TestFsck_RepeatedReadsOfADamagedStoreAgree requires 20 --json and 20 text reads to be byte-identical; on base all five lists differed.
- **fixed**: regress/minor: test/e2e/v5_x14_test.go:337-343 asserted only inside an if
  - A found flag plus require.True after the loop. The row fails when session_start.fires is missing.
- **fixed**: complete/minor: docs/troubleshooting.md:152-156 stated same-session-restart unconditionally
  - The paragraph now says it holds only while starts_without_marker is 0 (marker-absent-once at 1, failing at 2), and applies to any session.
- **fixed**: night/minor: internal/cli tests wrote client spools into the checkout's .qompack/spool (dispatch_test.go:31 area), plus the round-1 reviewer major covering compactLoadRig, hooks_test.go, bench_test.go and the missing package guard
  - Round 0: dispatch_test.go fault cases pinned, with requireRootOutsideCheckout. Round 1: compactLoadRig.hookE now uses QOMPACK_PROJECT_ROOT=r.root, the dead --project flag is dropped, and requireReadsReachedTheRig was added; on the old hookE all 271 Reads were missing, after the fix 13 of 82 were indexed and the rest were in the rig's spool. TestHooks_AllSixExitZeroWithValidJSON and BenchmarkHookNoop_InProcess are pinned to temp projects. The new TestMain guard on <root>/.qompack/spool/client-<pid>.ndjson was red for all three leakers before their fixes and passes the full internal/cli run. The other noEnv sites in hooks_test.go were verified non-leaking: their admitted payloads are re-rooted, and the full package is clean under the guard.
- **fixed**: nit: eval keysOfBool and validator error order (livetask.go, ledger.go, provenance.go)
  - Sorted keys and fixed arrays. TestEvalValidators_NameTheSameProblemEveryRead and TestKeysOfBool_ListsKeysInSortedOrder are red on base.
- **fixed**: nit: stale test comments in current_work_fork_test.go:23-24 and status_order_test.go:21-24
  - Comment-only corrections, verified against the code.
- **fixed**: review nit: internal/eval/liveaccount.go beside() ranged over ta.Delta and appended problems in map order
  - It now walks usageModels(ta.Delta), the sorted keys. TestAccountHostStream_NamesBesideProblemsInModelOrder (two models, both totals falling, evalOrderReads reads) failed at read 10 on the old loop and passes 20/20 and 3/3 under -race.

### Tests

- `go test -p 1 -count=1 -run '^TestHooks_AllSixExitZeroWithValidJSON$' ./internal/cli/ (guard added, rows not yet fixed)`: FAIL: checkout spool guard, client-<pid>.ndjson went from -1 to 2271 bytes (red-first)
- `go test -p 1 -count=1 -run '^TestSessionStartCompact_UnderSameSessionIngest$' ./internal/cli/ (old hookE with the new assertion)`: FAIL: 271 of the rig's 271 Reads reached neither its index nor its spool, and the guard saw 117072 bytes in the checkout (red-first)
- `go test -p 1 -count=1 -run '^$' -bench '^BenchmarkHookNoop_InProcess$' -benchtime=50x ./internal/cli/ (old bench_test.go, then fixed)`: old: FAIL, guard saw 18156 bytes; fixed: ok, 1500140 ns/op, no .qompack created
- `go test -p 1 -count=1 -run '^TestAccountHostStream_NamesBesideProblemsInModelOrder$' ./internal/eval/ (old liveaccount.go)`: FAIL: read 10 differed (red-first)
- `go test -p 1 -count=20 -run '^TestAccountHostStream_NamesBesideProblemsInModelOrder$' ./internal/eval/ ; same with -race -count=3`: ok / ok
- `go test -p 1 -count=1 ./internal/eval/`: ok (full package)
- `go test -p 1 -count=1 -timeout=30m ./internal/cli/`: ok 132.461s (full package, guard active, no checkout .qompack created)
- `go test -p 1 -count=20 -timeout=30m on each of ^TestSessionStartCompact_UnderSameSessionIngest$, ^TestHooks_AllSixExitZeroWithValidJSON$, ^TestLivePinReachesTheNextCheckpointAndBlock$, ^TestLivePinAfterACompactionReachesTheNextOne$, ^TestPreCompactCheckpointCarriesTheCompactedReads$, ^TestSecondPreCompactCarriesItsOwnSpan$, ^TestPreCompactInSpoolSubmodeSealsTheSpooledReads$, ^TestAHostFailedCompactionsSuccessorClosesAtSessionEnd$, ^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$ ./internal/cli/ (run in one process)`: all passed 20/20 except one failure of TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook ('its open-segment account is the restored one plus the new Read'), the pre-existing redelivery double-count owned by the redeliver seat
- `go test -p 1 -count=20 -timeout=30m -run '^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$' ./internal/cli/`: ok 53.0s (20/20 alone)
- `go test -p 1 -race -count=3 -timeout=30m on the same nine cli rows`: ok 74.6s, no race
- `GOOS=windows|linux|darwin go vet ./internal/cli/ ./internal/eval/`: clean on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/ ./internal/eval/`: exit 0, no findings
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 1 -count=1 ./test/guards`: ok 108.5s (accepts the extended TestMain, which still calls pathstest.Main)
- `Round 0 (carried): go test -p 1 -count=1 ./internal/contract/ ; ./internal/checkpoint/ ; ./test/docs ; the gen-config-docs, gen-command-docs and gen-mcp-docs --check runs ; -count=20 and -race -count=3 on the round-0 rows`: ok throughout (see round-0 result)

### Criterion changes

- Carried: TestDispatch_HookAlwaysExitsZero fault cases pin QOMPACK_PROJECT_ROOT and require the first root outside the checkout (tightened; the unwritable-store case was writing the real checkout store).
- Carried: TestV5_EveryContractAssertionHasARealProducer/full_composition_records_per_capability_evidence requires the session_start.fires row to be present (tightened).
- Carried (product): another session's own compaction or --resume no longer counts an absence or moves LastSessionID.
- TestSessionStartCompact_UnderSameSessionIngest now requires every Read the rig built to be in the rig's index or spool, with at least one indexed (tightened). Reason: on the old rig none reached the daemon, so the row passed without applying its load.
- compactLoadRig (shared by the precompact_*, segment_roll_live and C1.16 rows) now delivers to its own project, so those rows exercise the daemon with real Reads. Reason: their Reads were spooled into the checkout instead. Behaviour and timing of those rows change; all passed 20/20 except the redeliver-owned intermittent noted in open_issues.
- TestHooks_AllSixExitZeroWithValidJSON and BenchmarkHookNoop_InProcess deliver to their temp project. Reason: they wrote the checkout's store.
- internal/cli TestMain fails the run if this test process spooled anything into the checkout's store (new guard, tightened).

### Open issues

- TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook (segment_roll_live_test.go:239) failed 1 in 20 in a combined run of the rig rows here, then passed 20/20 alone. It is the redelivery double-count in scheduler_tap.go:201 that the redeliver seat's audit found 1/20 on base 738d67c7. It is not caused by this change and needs that seat's fix before the freeze.
- TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools (internal/daemon/precompact_settle_bound_test.go:162) failed once in a full internal/daemon run in round 0, then passed 20/20 alone. It looks load-sensitive and needs an owner.
- Now that the compact rig's Reads reach its daemon, most of them were still in the rig's client spool when the daemon stopped (one run: 13 of 82 indexed, the rest spooled). This looks like hot-path spool-on-breach under same-session load, not a loss, since the spool is durable and the check accepts it. Nobody has measured whether that backlog drains in time in production.
- The new guard covers in-process hooks of the internal/cli test binary only. test/guards also runs all six hooks in process; checking that package for the same leak is not in this seat's scope.

### Needs owner

- Carried: evalOrderReads = 200 (internal/eval/order_test.go). Derivation: a two-entry map's rarer order comes up about one read in eight, so an accidental pass is below (7/8)^199, about 3e-12. The new beside row uses the same constant.
- Carried: fsckOrderReads = 20 and fsckOrderNeverLogged = fsckMaxDetail+5 (internal/cli/fsck_order_test.go).
- Carried behaviour decision: a SessionStart whose marker names itself is that session's own restart only for source compact or resume, or when it is the last-seen session. Pinned by TestSessionStartFires_FreshStartNamingItselfStillCounts.
- C1.16's evidence needs re-measuring: TestSessionStartCompact_UnderSameSessionIngest's Reads never reached the daemon before 3f2da1b3. If the distributions in plans/sdd/V6-closeout/w2-lifetime/runs came from this rig, they measured compactions with no real same-session ingest and should be re-run.
- BenchmarkHookNoop_InProcess now resolves its project from QOMPACK_PROJECT_ROOT and spools into a temp dir instead of the checkout. A bench-compare of a pre-fbb32c13 base against a newer candidate compares slightly different workloads for this one benchmark.

## review:status:0:r2: verdict `needs-fixes`, 3 finding(s)

- **minor** `internal/eval/livetrial.go:609-616 (DecideLive)`: The eval ordering sweep missed one user-visible output. DecideLive ranges over the s.Arms map and returns a not-applicable decision naming the first arm with PluginMismatch > 0. If both arms (qompack and stock) carry a mismatch, LiveSummary.Decision.Reason names a different arm from one run to the next. This is the same D53(a) class as the validators and beside(), which the seat fixed, and it lands in the live-trial summary that sum.Decision feeds (livetrial.go:522).
  - Evidence: An overlay probe, so the worktree was not edited: internal/eval/zz_decide_test.go called eval.DecideLive 200 times on LiveSummary{Arms: {qompack: {PluginMismatch:1}, stock: {PluginMismatch:2}}}. Run with `go test -p 1 -count=1 -overlay ... -run '^TestZZ_DecideLiveOrder$' ./internal/eval/`, it gave map["1 qompack trial(s) ran with the wrong plugin state":175 "2 stock trial(s) ran with the wrong plugin state":25]. The scratch go/packages scan of non-test range-over-map statements in internal/eval found this as the only remaining loop whose output depends on map order. The other 34 either sort, sum integers, or return only when exactly one key matches. <!-- runpatterns: names a reviewer's scratch probe run through -overlay, not a test in this tree -->
  - Fix: Walk the arms in a fixed order, for example `for _, name := range slices.Sorted(maps.Keys(s.Arms)) { arm := s.Arms[name]; ... }`, or name every mismatched arm in one sorted Reason. Add a row to internal/eval/order_test.go: two mismatched arms, requireSameEveryRead over DecideLive(...).Reason. It should fail on the current code and pass after the fix.
- **nit** `internal/cli/main_test.go:42-85 (checkoutSpoolGuard)`: The new package guard watches a single file, <root>/.qompack/spool/client-<pid>.ndjson. The audit fix asked for a guard that no test creates <repo>/.qompack. A test that escapes its sandbox some other way still passes the guard. Examples: ipc.Client externalize writes blob-<pid>-<n> files under the root (internal/ipc/client.go:437), a hook can spawn a detached daemon at the checkout root, and logQuiet writes to an existing logs dir. The narrow scope does keep a real Qompack session in the same checkout from tripping the guard, which is a valid reason, but the strict check is free whenever the checkout has no store.
  - Evidence: main_test.go newCheckoutSpoolGuard stats only filepath.Join(paths.Of(root).Spool, "client-%d.ndjson"). The checkout had no .qompack before or after my full run (`go test -p 1 -count=1 -timeout=30m ./internal/cli/`: ok 131.2s, `ls .qompack`: no such file). So the absent-before case is the common one, and in that case a stronger check would cost nothing.
  - Fix: Also record whether <root>/.qompack existed before m.Run. If it did not, fail when it exists afterwards (and name what is inside). Keep the per-pid client-spool size check for the case where a live store was already there.
- **nit** `internal/cli/sessionstart_compact_load_test.go:369-408 (requireReadsReachedTheRig)`: The comment says the check runs after stop, 'which drained the spool and the WAL'. The probe contradicts that. The rig's spool directory holds only the session WAL, which keeps every delivered Read, including the ones already indexed. So the 'or still in its spool' branch passes for any Read the client delivered to the rig's root, whether or not the daemon ingested it. The check proves the hooks are routed to the right root plus at least one ingest. It does not prove the same-session load was ingested. The seat lists the backlog as an open issue, but the comment overstates what the check shows.
  - Evidence: An overlay copy of the rig file logged every spooled Read record. One run of `go test -p 1 -count=1 -overlay ... -run '^TestSessionStartCompact_UnderSameSessionIngest$' -v ./internal/cli/` gave a single spool file, wal-sess-c116-compact-under-ingest.ndjson. It held all 91 Reads with outcome=ok, and the log line read 'the rig's daemon indexed 12 of its 91 Reads'.
  - Fix: Rewrite the comment to say what the check proves: every Read reached the rig's project (its WAL or index, never another root), and the daemon indexed at least one. Drop the 'drained the spool and the WAL' claim. Or add the indexed/total ratio to compactLoadReport so the C1.16 re-measurement the seat flagged can cite it.

## fix:status:r2: status `done`, head `6ea2c9c1344f74384b28fc203c7d05bb303bac6b`

### Root cause

(1) checkSessionStartFires recognized a session's own restart only through the project-wide LastSessionID, which names only the most recently started session. (2) fsck, the eval validators, the live account's beside() and DecideLive built visible output by ranging over Go maps, whose iteration order is randomized. (3) The x14 status check had no presence assertion. (4) In-process hook tests whose Env pins no QOMPACK_PROJECT_ROOT take their first project root from the process cwd, which is inside the checkout. A refused or unprovable payload is never re-rooted, so it was spooled into the checkout's .qompack; the compact rig's --project flag was dead. The first guard against this watched only one file. (5) The rig check's comment misread the spool directory, which holds the daemon's session WAL of received Reads.

### Summary

PRODUCT CODE CHANGED (internal/contract/assertions.go, internal/cli/fsck.go, internal/eval/{ledger,livetask,provenance,liveaccount,livetrial}.go).

The original five audit findings, the round-1 review findings and this round's three findings are all fixed. The work is on closeout/w20-status in worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w20-status: 13 commits on top of 738d67c7, unpushed, HEAD 6ea2c9c1. Every fix comes with a row that fails on the old code and passes now.

## Rounds 0-1 (carried forward)
1. **session_start.fires with two sessions open (major, fixed).**
   - Before: checkSessionStartFires counted a session's own restart only when History.LastSessionID named that session. When the older of two open sessions compacted, its own marker counted as an absence. Two such compactions made the row SevCritical.
   - Now: a marker that names the starting session counts as its own restart if that session is the last-seen one or the source is compact or resume. A restart counts nothing and changes neither field.
   - The row reads same-session-restart at count 0, marker-absent-once at 1, and fails at 2. A startup or clear start whose marker names itself still counts an absence.
   - docs/troubleshooting.md states these conditions.
2. **fsck detail order (fixed).** Five lists were built by ranging over Go maps, so their order changed between runs and the 20-line cap kept a different 20 each time. They now walk sorted keys or a fixed array. TestFsck_RepeatedReadsOfADamagedStoreAgree requires 20 identical --json reads and 20 identical text reads.
3. **eval validator order (nit, fixed).** keysOfBool and the validators use sorted keys or fixed arrays. beside() walks the models in sorted order.
4. **x14 status row (fixed).** test/e2e/v5_x14_test.go only asserted inside an if. It now fails when session_start.fires is missing from the status read.
5. **Spool leaks into the checkout (fixed).**
   - The dispatch_test.go fault cases are pinned to temp projects.
   - compactLoadRig.hookE now passes QOMPACK_PROJECT_ROOT=r.root and drops a `--project` flag that no hook parses.
   - TestHooks_AllSixExitZeroWithValidJSON and BenchmarkHookNoop_InProcess are pinned to temp projects.
   - A new TestMain guard catches any further leak.
6. **Stale test comments (nits, fixed).**

## Review resolution (round 2)
**Minor: DecideLive picks the arm it names by map order. Holds, fixed in d3ddeba0.**
- DecideLive ranged over s.Arms and returned the first arm with PluginMismatch > 0. With both arms mismatched, LiveSummary.Decision.Reason named qompack on some runs and stock on others.
- It now walks slices.Sorted(maps.Keys(s.Arms)) and names every mismatched arm in one reason: "1 qompack and 2 stock trial(s) ran with the wrong plugin state". The single-arm text is unchanged, and no caller or test matches on it.
- New row TestDecideLive_NamesEveryMismatchedArmInOrder in internal/eval/order_test.go. On the old loop it failed at read 13 of evalOrderReads: the first read named qompack, read 13 named stock. It now passes 20/20 and 3/3 under -race.

**Nit: the checkout guard only watched one file. Holds, fixed in e069a4cc.**
- The guard now records whether <root>/.qompack existed before m.Run.
- If it did not, any .qompack left after the run fails it, whatever wrote it, and the message lists every file inside with slash paths.
- If a store already existed, a real session may be using it, so only this process's client-<pid>.ndjson size is checked, as before.
- The guard now has a unit row, TestCheckoutSpoolGuard_FailsOnAStoreTheRunCreated. Its no-store case writes .qompack/logs/daemon.log under a temp root; it failed on the narrow guard ("An error is expected but got nil"). Its existing-store case shows that another pid's spool passes and this pid's spool fails.
- The full internal/cli package passes under the stricter guard (148.5s), and the checkout has no .qompack afterwards.

**Nit: requireReadsReachedTheRig's comment overstated what it proves. Holds, fixed in 6ea2c9c1.**
- I confirmed the reviewer's point in the daemon code: the rig's spool directory holds the daemon's own session WAL (internal/daemon/ingest.go walPath, wal-<session>.ndjson), and it keeps every Read the daemon received. Nothing drains it at stop.
- The comment now says the check proves the Reads were delivered to the rig's project and no other root, plus at least one ingest. It does not prove the load was indexed. The "drained the spool and the WAL" claim is gone.
- Pass/fail is unchanged. The log line now splits the Reads three ways for the C1.16 re-measurement: indexed, in the daemon's WAL only, and in a client spool.
- Over 20 runs, roughly 4-20 of each run's 43-95 Reads were indexed, most of the rest were in the daemon's WAL only, and 0-30 were in a client spool. Example: "of the rig's 92 Reads: 17 indexed, 75 in its daemon's WAL only, 0 in a client spool".
- This corrects my round-1 open issue. Most unindexed Reads were received by the daemon and are in its WAL; they were not a client-spool backlog. Only a minority, varying from run to run, sat in a client spool.

## Risk for tonight (carried)
In round 1, one combined -count=20 run of the rig rows failed TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook once with "its open-segment account is the restored one plus the new Read". That is the redelivery double-count the redeliver seat owns; its audit shows it 1/20 on base 738d67c7. The row passed 20/20 alone and 3/3 under -race.

The stricter guard has run on Windows only. There is no local Linux here: WSL is gone and this task forbids Docker. It fails only when a run creates the checkout's .qompack where none existed, and it names what is inside.

Rows to cite (exact names): TestSessionStartFires_OverlappingSessionsRestartsHold, TestSessionStartFires_OverlappingRestartKeepsACountedAbsence, TestSessionStartFires_FreshStartNamingItselfStillCounts, TestSessionStartFires_GenuineAbsenceKeepsItsBehaviour, TestFsck_RepeatedReadsOfADamagedStoreAgree, TestEvalValidators_NameTheSameProblemEveryRead, TestKeysOfBool_ListsKeysInSortedOrder, TestAccountHostStream_NamesBesideProblemsInModelOrder, TestDecideLive_NamesEveryMismatchedArmInOrder, TestCheckoutSpoolGuard_FailsOnAStoreTheRunCreated, TestDispatch_HookAlwaysExitsZero, TestHooks_AllSixExitZeroWithValidJSON, TestSessionStartCompact_UnderSameSessionIngest, BenchmarkHookNoop_InProcess.

### Commits

- 2e4ba732 fix(contract): count no absence for another session's restart
- 5c2b019a fix(cli): list fsck detail lines in sorted order
- 48d63d6c fix(eval): name malformed-input problems in a fixed order
- 3fe4a099 test(e2e): require session_start.fires on the x14 status read
- 2564e032 test(cli): pin the dispatch hook faults to temp projects
- 70dab733 test: correct two stale evidence comments
- a43bc0f1 fix(eval): name beside-the-main-loop problems in model order
- 3f2da1b3 test(cli): route the compact load rig's hooks to its project
- fbb32c13 test(cli): pin the six-hook row and hook bench to temp projects
- bec822c8 test(cli): fail the run when a test spools into the checkout
- d3ddeba0 fix(eval): name every mismatched arm in the live decision
- e069a4cc test(cli): fail the run on any store it creates in the checkout
- 6ea2c9c1 test(cli): say what the compact rig's delivery check proves

### Findings resolution

- **fixed**: status/major: checkSessionStartFires counted same-session-restart only for LastSessionID (internal/contract/assertions.go; docs/troubleshooting.md)
  - A marker naming the starting session is its own restart when that session is the last-seen one or the source is compact or resume. A restart counts nothing and changes neither field. The row reads same-session-restart at 0, marker-absent-once at 1, failing at 2. A startup or clear start naming itself still counts an absence. TestSessionStartFires_OverlappingSessionsRestartsHold and TestSessionStartFires_OverlappingRestartKeepsACountedAbsence are red on base. The troubleshooting paragraph states the exact conditions.
- **fixed**: status/major: fsck listed detail lines in map order in five checks (internal/cli/fsck.go)
  - checkFiles, comparePinsView, checkSpool and checkFidelity walk sorted keys; resolveRootLine uses a fixed label array. TestFsck_RepeatedReadsOfADamagedStoreAgree requires 20 --json reads and 20 text reads to be byte-identical; on base all five lists differed.
- **fixed**: regress/minor: test/e2e/v5_x14_test.go:337-343 asserted only inside an if
  - A found flag plus require.True after the loop: the row fails when session_start.fires is missing.
- **fixed**: complete/minor: docs/troubleshooting.md:152-156 stated same-session-restart unconditionally
  - The paragraph says it holds only while starts_without_marker is 0 (marker-absent-once at 1, failing at 2) and applies to any session.
- **fixed**: night/minor: internal/cli tests wrote client spools into the checkout's .qompack/spool (dispatch_test.go:31 area), plus round-1 reviewer major (compactLoadRig, hooks_test.go, bench_test.go, missing package guard)
  - The dispatch faults are pinned to temp projects (requireRootOutsideCheckout). compactLoadRig.hookE uses QOMPACK_PROJECT_ROOT=r.root; on the old hookE 271/271 Reads were missing. The six-hook row and hook bench are pinned to temp projects. The TestMain guard was red for all three leakers before their fixes. The other noEnv sites in hooks_test.go were checked and do not leak.
- **fixed**: nit: eval keysOfBool and validator error order (livetask.go, ledger.go, provenance.go)
  - Sorted keys and fixed arrays. TestEvalValidators_NameTheSameProblemEveryRead and TestKeysOfBool_ListsKeysInSortedOrder are red on base.
- **fixed**: nit: stale test comments in current_work_fork_test.go:23-24 and status_order_test.go:21-24
  - Comment-only corrections, checked against the code.
- **fixed**: round-1 review nit: internal/eval/liveaccount.go beside() appended problems in map order
  - It walks usageModels(ta.Delta). TestAccountHostStream_NamesBesideProblemsInModelOrder failed at read 10 on the old loop; it passes 20/20 and 3/3 under -race.
- **fixed**: round-2 review minor: internal/eval/livetrial.go:609-616 DecideLive named the first mismatched arm in map order
  - d3ddeba0: DecideLive walks slices.Sorted(maps.Keys(s.Arms)) and names every mismatched arm in one reason ('1 qompack and 2 stock trial(s) ran with the wrong plugin state'); single-arm text is unchanged. New row TestDecideLive_NamesEveryMismatchedArmInOrder (order_test.go) failed at read 13 on the old loop (qompack vs stock) and passes 20/20 and 3/3 under -race. The doc comment says every such arm is named in arm order.
- **fixed**: round-2 review nit: internal/cli/main_test.go checkoutSpoolGuard watched only client-<pid>.ndjson
  - e069a4cc: the guard records whether <root>/.qompack existed before m.Run. If it did not, any store left after the run fails it and the message lists every file inside. If it existed, only this pid's client spool is checked, so a real session's hooks cannot trip it. TestCheckoutSpoolGuard_FailsOnAStoreTheRunCreated failed on the narrow guard ('An error is expected but got nil') and passes 20/20 and 3/3 under -race. The full internal/cli package passes under the strict guard, and the checkout has no .qompack afterwards.
- **fixed**: round-2 review nit: internal/cli/sessionstart_compact_load_test.go requireReadsReachedTheRig comment claimed stop drained the spool and WAL
  - 6ea2c9c1: verified that the spool dir holds the daemon's session WAL (internal/daemon/ingest.go walPath), which keeps every received Read. The comment now says the check proves delivery to the rig's project plus at least one ingest, not that the load was indexed. Pass/fail is unchanged. The log splits the Reads into indexed, in the daemon's WAL only, and in a client spool. In 20 runs the indexed count was about 4-20 of 43-95 Reads, most of the rest were in the WAL only, and 0-30 were in a client spool.

### Tests

- `go test -p 1 -count=1 -run '^TestDecideLive_NamesEveryMismatchedArmInOrder$' ./internal/eval/ (old DecideLive loop)`: FAIL: read 13 differs ('1 qompack ...' vs '2 stock ...') (red-first)
- `go test -p 1 -count=20 -run '^TestDecideLive_NamesEveryMismatchedArmInOrder$' ./internal/eval/ ; same with -race -count=3`: ok / ok
- `go test -p 1 -count=1 -run '^TestCheckoutSpoolGuard_FailsOnAStoreTheRunCreated$' ./internal/cli/ (narrow guard, constructor refactor only)`: FAIL: no-store case 'An error is expected but got nil' (red-first)
- `go test -p 1 -count=20 -run '^TestCheckoutSpoolGuard_FailsOnAStoreTheRunCreated$' ./internal/cli/ ; same with -race -count=3`: ok / ok
- `go test -p 1 -count=20 -timeout=30m -run '^TestSessionStartCompact_UnderSameSessionIngest$' -v ./internal/cli/`: ok 212.2s, 20/20, with per-run indexed/WAL-only/client-spool counts logged
- `go test -p 1 -race -count=3 -timeout=30m -run '^TestSessionStartCompact_UnderSameSessionIngest$' ./internal/cli/`: ok 49.9s
- `go test -p 1 -count=1 ./internal/eval/`: ok 2.7s (full package)
- `go test -p 1 -count=1 -timeout=30m ./internal/cli/`: ok 148.5s (full package, strict guard active; no checkout .qompack afterwards)
- `go test -p 1 -count=1 -timeout=30m ./test/guards`: ok 52.0s
- `GOOS=windows|linux|darwin go vet ./internal/cli/ ./internal/eval/`: clean on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/cli/ ./internal/eval/`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `Round 1 (carried): red-first runs for ^TestHooks_AllSixExitZeroWithValidJSON$, ^TestSessionStartCompact_UnderSameSessionIngest$, BenchmarkHookNoop_InProcess and ^TestAccountHostStream_NamesBesideProblemsInModelOrder$; -count=20 and -race -count=3 on the nine rig rows`: red on old code, green after; 20/20 except one redeliver-owned failure of ^TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook$ (20/20 alone)
- `Round 0 (carried): go test -p 1 -count=1 ./internal/contract/ ; ./internal/checkpoint/ ; ./test/docs ; gen-config-docs/gen-command-docs/gen-mcp-docs --check ; -count=20 and -race -count=3 on the round-0 rows`: ok throughout

### Criterion changes

- Carried: TestDispatch_HookAlwaysExitsZero fault cases pin QOMPACK_PROJECT_ROOT and require the first root to be outside the checkout (tightened).
- Carried: TestV5_EveryContractAssertionHasARealProducer/full_composition_records_per_capability_evidence requires the session_start.fires row (tightened).
- Carried (product): another session's own compaction or --resume no longer counts an absence or moves LastSessionID.
- Carried: TestSessionStartCompact_UnderSameSessionIngest requires every Read to be in the rig's index or spool directory, with at least one indexed (tightened). Round 2 makes no criterion change: the comment now states what this proves, and the log adds an indexed/WAL-only/client-spool split.
- Carried: compactLoadRig delivers to its own project, so the precompact_*, segment_roll_live and C1.16 rows exercise the daemon with real Reads.
- Carried: TestHooks_AllSixExitZeroWithValidJSON and BenchmarkHookNoop_InProcess deliver to their temp project.
- Tightened in round 2: the internal/cli TestMain guard now fails on any checkout .qompack the run created where none existed, not only on this process's client spool. Reason: other escape paths (blobs, logs, a spawned daemon) passed the narrow guard. New row TestCheckoutSpoolGuard_FailsOnAStoreTheRunCreated pins it.
- Product, round 2: with both arms mismatched, DecideLive's reason now names every mismatched arm in sorted order. Pinned by TestDecideLive_NamesEveryMismatchedArmInOrder.

### Open issues

- TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook (segment_roll_live_test.go:239) failed 1 in 20 in a combined run of the rig rows in round 1, then passed 20/20 alone. It is the redelivery double-count in scheduler_tap.go:201 that the redeliver seat found 1/20 on base 738d67c7. It needs that seat's fix before the freeze.
- TestPreCompactSettle_TheLastLookReadsOnlyNamedAndNewSpools (internal/daemon/precompact_settle_bound_test.go:162) failed once in a full internal/daemon run in round 0, then passed 20/20 alone. It looks load-sensitive and needs an owner.
- Corrected from round 1: when the compact rig's daemon stops, most of its Reads are in its session WAL (received) but not in index/tool_use.jsonl. Across 20 runs, about 4-20 of 43-95 Reads were indexed, and 0-30 per run were still in a client spool. Nobody has established whether WAL-only at stop is expected (replayed at the next start) or a gap in ingest under same-session load.
- The stricter checkout guard has run on Windows only. Local Linux is unavailable (WSL distro gone; Docker forbidden by this task), so its first Linux/macOS run will be hosted CI. It fails only when a run creates the checkout's .qompack where none existed, and it names what is inside.
- The guard covers in-process hooks of the internal/cli test binary only. test/guards also runs all six hooks in process, and checking that package for the same leak is outside this seat's scope.

### Needs owner

- Carried: evalOrderReads = 200 (internal/eval/order_test.go). Derivation: a two-entry map's rarer order comes up about one read in eight, so an accidental pass is below (7/8)^199, about 3e-12. The beside and DecideLive rows use the same constant.
- Carried: fsckOrderReads = 20 and fsckOrderNeverLogged = fsckMaxDetail+5 (internal/cli/fsck_order_test.go).
- Carried behaviour decision: a SessionStart whose marker names itself counts as that session's own restart only for source compact or resume, or when it is the last-seen session. Pinned by TestSessionStartFires_FreshStartNamingItselfStillCounts.
- New behaviour (text only): with both arms mismatched, DecideLive's not-applicable reason now names both arms in sorted order ('1 qompack and 2 stock trial(s) ran with the wrong plugin state') instead of one arm chosen at random. The verdict is unchanged.
- C1.16's evidence needs re-measuring. TestSessionStartCompact_UnderSameSessionIngest's Reads never reached the daemon before 3f2da1b3. If the distributions in plans/sdd/V6-closeout/w2-lifetime/runs came from this rig, they should be re-run; the rig now logs indexed, WAL-only and client-spool counts to cite.
- BenchmarkHookNoop_InProcess now spools into a temp dir instead of the checkout. A bench-compare of a pre-fbb32c13 base against a newer candidate compares slightly different workloads for this benchmark.

## verify:status: verdict `sound`, 1 finding(s)

- **nit** `test/e2e (package-level run; not a file this seat changed)`: FYI only, not a defect in this seat's work. The full test/e2e run (-p 1) is not green, but every red is either already present on base or comes from a timing budget. This seat's own e2e row, TestV5_EveryContractAssertionHasARealProducer (x14), passes. All other touched packages pass in full.
  - Evidence: In scratch git-archive copies, the full -p 1 runs of internal/contract, internal/eval, internal/checkpoint, internal/cli (strict checkout guard on, and the copy has no .qompack afterwards) and internal/daemon all exited 0. test/e2e failed on four rows. (1) TestV5_ThrashWarningVisibleInStatusAndCheckpoint/sequitur_warning_is_warning_only_and_once_per_rule failed 3 of 3 runs on HEAD 6ea2c9c1 and also failed on a pure 738d67c7 base copy, so it was already broken on base. (2) TestV3_HotPathUnchangedWithLedgerResident breached its B-A/B-B budgets, a known wall-clock gate. (3) TestE2E_SessionStartLatency measured p99 1.53s against a 1.5s budget. (4) TestV5_ObserveToStatusRoundTrip/live read Source none once; its daemon_down subtest then panicked on a nil deref at v5_x01_test.go:665, which cut that run short before x14 ran. On a second run x01 passed, and run alone x01 and x14 both pass. Red-first was confirmed for the claimed rows: with the base product files restored, the following fail and pass on HEAD: TestSessionStartFires_OverlappingSessionsRestartsHold, TestSessionStartFires_OverlappingRestartKeepsACountedAbsence, TestFsck_RepeatedReadsOfADamagedStoreAgree (read 2 differs), TestEvalValidators_NameTheSameProblemEveryRead, TestKeysOfBool_ListsKeysInSortedOrder, TestAccountHostStream_NamesBesideProblemsInModelOrder, TestDecideLive_NamesEveryMismatchedArmInOrder.
  - Fix: No change is needed on closeout/w20-status. Hand the x10 sequitur-warning failure (already red on base 738d67c7) and the x01 nil deref after a failed live read to their owning seats. Treat the X11 and SessionStartLatency reds as timing gates to re-run in a quiet window.

