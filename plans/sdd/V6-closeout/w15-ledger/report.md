# Wave 15 workstream ledger

Branch `closeout/w15-ledger`. Workflow `wf_0a7ad63f-671`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `2c60aafd`

### Root cause

R4-1: tried.bloom held only active records and reconcile compared counts, so after a restart or idle rebuild a stale record's key was missing and Query's first bloom test answered absent. F-C4-C49-3: the lazily opened ledger left the checkpoint sources unresolved, and both frontier paths treated 'no ledger yet' as an unavailable source. F-C4-UAT06-2: drafts mint decisions only at or after their own frontier, and successor or cold drafts started with none. F-C4-UAT06-1 (ledger half): session-scoped visibility and checkpoint carry compared only the record's session with the caller's and never consulted the fork lineage.

### Summary

W15-LEDGER REPORT (D49). Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15-ledger, branch closeout/w15-ledger, base 08034a3f, head 2c60aafd. Four fix commits, one per finding. For each one a deterministic test was red before the fix. Red and green logs are committed under plans/sdd/V6-closeout/w15-ledger/runs/.

(1) R4-1: a stale elimination answered absent after a restart or an idle rebuild (commit ffe5e18e)
Root cause: tried.bloom held only ACTIVE records.
- Record adds a key only to the in-memory filter.
- reconcileBloom compared counts against the active records, so after a restart it saw nothing missing and did not rebuild.
- Query's first !bloom.Test answered absent before it read the stale record.
- rebuildOnStale / RebuildBloom dropped the key the same way.
Fix: the filter now holds every record in view, active and stale (filterRecords), at every rebuild. reconcileBloom checks each record's MatchKey against the loaded filter (filterMissesARecord) rather than comparing counts, so a filter written by an older build is repaired at Open. ADR 0009 records D49.
New tests, all in internal/negknow/stale_filter_test.go: TestQuery_StaleRecordAnswersStaleAfterRestart, TestQuery_StaleRecordIsUncertainAfterRestartUnderDrop, TestQuery_StaleRecordAnswersStaleAfterColdOpen, TestQuery_StaleRecordAnswersStaleAfterIdleRebuild, TestQuery_StaleRecordAnswersStaleAfterImmediateRebuild, TestReconcile_RebuildsAFilterMissingAStaleRecord. The existing TestThreeWayAnswerFixture_ColdOpen row covers the same rule on the frozen fixture. Logs: runs/r4-1-red.txt, runs/r4-1-green.txt.

(2) F-C4-C49-3: act.advance_frontier failed every 30 s in a session with no compaction and no elimination (commit dfff29cb)
Root cause: the daemon opens its ledger lazily. The checkpoint SourceSet's accessor therefore answered nil, Resolve failed, and both advance paths refused:
- the scheduler's act.advance_frontier, through frontierAdvancer.begin;
- the daemon's advance_frontier, whose advanceFrontierTask returned early.
Fix:
- Resolve now reports this case as checkpoint.ErrNoLedger.
- The frontier port marks its context (allowNoLedger). Under that mark only, Begin admits the partial set, and only while negknow.HasRecords(root) is false, meaning the log is absent or empty.
- The admitted draft's ledger is negknow.Deferred over the accessor. It re-reads the accessor on every call, so the draft picks up a ledger opened later. It answers ErrNotOpen, never an empty list, once there are records on disk that no open ledger serves.
- Every other Begin still refuses by name. PreCompact is the main one, and TestBindCheckpointDegradesWhenTheLedgerCannotBeOpened is unchanged.
- The daemon's advanceFrontierTask sweeps over the partial set. If the sweep still hits ErrNoLedger, it takes the existing unavailable route: counted, one Warn, then Debug.
- The production supplier (internal/cli recordedLedger) opens the ledger through the daemon's shared opener only when the project already holds records. A restarted daemon's frontier therefore reads them instead of refusing on every tick. A project with no records still opens nothing and creates no files.
New tests:
- internal/checkpoint: TestFrontierAdvancer_AdvancesBeforeAnyLedgerExists, TestFrontierAdvancer_ReadsTheLedgerOnceOneExists, TestPreCompact_SealsTheLedgerThatOpenedAfterANoLedgerAdvance, TestFrontierAdvancer_NoLedgerOverRecordsStaysUnavailable.
- internal/daemon: TestAdvanceFrontierTask_AdvancesBeforeAnyLedgerExists (three ticks, no Warn, no Loud, segment encoded), TestAdvanceFrontierTask_NoLedgerOverRecordsStaysUnavailable.
- internal/cli: TestProductionCheckpointSourcesOpenTheLedgerOverRecords, TestProductionCheckpointSourcesOpenNothingWithoutRecords.
- internal/negknow: TestHasRecords, TestHasRecords_UnreadableLogIsNotNothing, TestDeferred_StandsInUntilALedgerIsOpen.
Logs: runs/f-c4-c49-3-red.txt, -daemon-red.txt, -cli-red.txt, -green.txt.

(3) F-C4-UAT06-2: the session's second checkpoint had decisions [] (commit f002064e)
Root cause: a draft mints decisions only at or after its own frontier: Advance's from-turn cut and the seal refresh's frontier cut. The successor draft a seal opens, and a cold draft after a restart, started with no decisions. The dec_991dbff588ec elimination sat before the new frontier, so it was never minted again, although the elimination itself is re-read from the ledger and carried.
Fix: in seedTierOne, when the draft descends from the session's own previous checkpoint, carryDecisionsLocked merges that checkpoint's decisions whose source still holds, through the existing mergeDecisionsLocked (same ranking, same cap of 64, id and turn kept). Because the artifact records no provenance, the source is read from the decision's shape:
- a decision with a rejected alternative is an elimination's, and holds while that record is carried;
- no alternative and no evidence means a pinned decision, which holds while a decision pin still mints it;
- anything else came from an explains edge and always holds.
pinDecision was factored out of fromPins so both use the same rule.
New tests in internal/checkpoint: TestSecondCheckpointCarriesTheSessionsEarlierDecision, TestRestartedWriterCarriesTheSessionsEarlierDecision, TestCarriedDecisionEndsWhenItsPinIsRemoved. Logs: runs/f-c4-uat06-2-red.txt, -green.txt.

(4) F-C4-UAT06-1, ledger half: a fork inherited none of the parent's eliminations or decisions (commit 2c60aafd)
Root cause: visibility was r.Session == caller session, with nothing reading the fork lineage.
Fix:
- negknow.Deps.Ancestry(s) returns []negknow.Inherited{Session, Until}. A viewer is resolved once per read, before the lock. visible() treats a session-scoped record as the viewer's own when its session is the viewer's, or when an ancestor made it with TS <= Until. This applies to Query, Active/TopActive, the refresh-on-read path and a session ledger's inView. Records stay attributed to the parent's session; siblings and ScopeProject queries are unchanged.
- checkpoint.Ancestry(layout, s) walks state/lineage-*.json: the parent up to At, then each grandparent up to its own fork point. The walk is bounded by a visited set, and an unreadable record ends it.
- The daemon's ledger (rehydrate_service.go OpenLedger) is opened with checkpoint.LedgerAncestry.
- Drafts store inherit and apply it through carriedBy for eliminated[], Advance merges and source-(b) decisions. The seal refresh also mints inherited records, ranked as foreign, because a fork can compact before any Advance runs.
- why already searches every checkpoint in the project, so it resolves once the fork's checkpoints carry the decision.
- docs/architecture.md records the rule; go test ./test/docs passes.
New tests:
- internal/negknow: TestQuery_ForkSeesItsParentsEliminationsUpToTheForkPoint, TestActive_ForkListsItsParentsEliminationsUpToTheForkPoint, TestQuery_SessionLedgerOfAForkSeesItsParentsEliminations.
- internal/checkpoint: TestForkCarriesItsParentsEliminationsUpToTheForkPoint (includes the sibling and after-the-fork negatives), TestAncestryFollowsTheForkChain.
- internal/cli: TestDaemonLedgerAnswersAForkWithItsParentsEliminations (daemon wiring).
Logs: runs/f-c4-uat06-1-negknow-red.txt, -checkpoint-red.txt, -cli-red.txt, -green.txt.
In checkpoint-red.txt, TestAncestryFollowsTheForkChain already passes: Ancestry was written first so the file would compile, and that red run shows the fork-carry test failing. The negknow red run had the Deps.Ancestry field present but not yet read.

CRITERION CHANGES (all in ffe5e18e, each with a written rationale in the test source)
Four rows asserted that a rebuild DROPS a stale record's keys and that Query then answers absent. That is exactly the R4-1 defect D49 rules out; each now asserts the stale answer.
- TestRebuildBloom_ActiveOnly became TestRebuildBloom_CoversActiveAndStaleRecords.
- negknowtest runBloomRebuildActiveOnlyCase became runBloomRebuildCoversStaleCase (subtest bloom_rebuilt_from_active_and_stale_records).
- TestE2E_EliminationLifecycle step 4.
- TestV3_ObserverFileVersionsDriveEliminationStaleness steps 7-8, which restores the X2 row's original bullets.
Also: comment wording in test/guards/stubs_test.go, and one comment clarification in the TestProductionCheckpointSourcesStayLazyAndReportUnavailable doc (its assertions are unchanged).
No check was weakened, no skip or nolint was added, and no threshold was changed.

### Commits

- ffe5e18e fix(negknow): keep stale records in the query filter (R4-1)
- dfff29cb fix(checkpoint): advance the frontier before any ledger exists (F-C4-C49-3)
- f002064e fix(checkpoint): carry a session's decisions into later checkpoints (F-C4-UAT06-2)
- 2c60aafd fix(negknow): let a fork inherit its parent's eliminations (F-C4-UAT06-1, ledger half)

### Tests

- `go test -p 2 -count=1 ./internal/negknow/...` — ok (negknow 38.2s, negknowtest 1.5s), after all negknow changes
- `go test -p 2 -count=1 ./internal/checkpoint/...` — ok (checkpoint 95.1s, checkpointtest 1.1s), final code
- `go test -p 2 -count=1 ./internal/daemon/` — ok 289.1s, final code
- `go test -p 2 -count=1 ./internal/cli/` — ok 82.7s, final code
- `go test -p 2 -count=1 -v -run '^TestE2E_EliminationLifecycle$' ./test/e2e/` — PASS
- `go test -p 2 -count=1 -v -run '^TestV3_ObserverFileVersionsDriveEliminationStaleness$' ./test/e2e/` — PASS
- `go test -p 2 -count=1 ./test/docs` — ok (after the docs/architecture.md edit)
- `go run ./tools/devtool fmt-check` — clean
- `go vet ./internal/checkpoint/ ./internal/daemon/ ./internal/cli/ ./internal/negknow/ (Windows and GOOS=linux)` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all eight PASS, exit 0
- `focused red/green rows per finding (exact names in the summary)` — red before each fix, green after; logs in plans/sdd/V6-closeout/w15-ledger/runs/

### Criterion changes

- TestRebuildBloom_ActiveOnly became TestRebuildBloom_CoversActiveAndStaleRecords: stale keys are now kept, not dropped (D49: a record goes stale, never absent).
- negknowtest runBloomRebuildActiveOnlyCase became runBloomRebuildCoversStaleCase (subtest bloom_rebuilt_from_active_and_stale_records): it asserts the stale key is present and Query answers stale.
- TestE2E_EliminationLifecycle step 4: after RebuildBloom the answer is stale, not absent, and the filter keeps two keys.
- TestV3_ObserverFileVersionsDriveEliminationStaleness steps 7-8: the stale record's keys stay and the answer stays stale. This reverses the V3-VERIFY §8.1 case d ruling, which D49 supersedes.
- Comment-only changes: test/guards/stubs_test.go wording; a note in the TestProductionCheckpointSourcesStayLazyAndReportUnavailable doc that the supplier still reports ErrNoLedger while the frontier port admits it (assertions unchanged).

### Open issues

- Not run in this seat under the daytime limits: -race, the Linux container (stopped), the whole test/e2e and test/integration packages, and the hot-path rows. The coordinator's quiet-window run should cover the four fix commits.
- None of the four fixes has been observed in a real host session. UAT-09 (restart then already_tried), C4.9 (idle log shows no act.advance_frontier warn and the frontier advances) and UAT-06 (checkpoint 0002 keeps dec_991dbff588ec; the fork's 0004/0005 carry the parent's elimination and decision) need a live re-run.
- A decision pin removed partway through a draft still appears in that draft's seal and so carries into the successor opened at that seal. It is dropped from the checkpoint after that. The in-draft part predates this seat (mergeDecisions never removes), and the carry does not extend it further.
- The item-3 carry reads only the draft's parent checkpoint. That parent is this session's own when the draft follows its own seal, and the project's newest when a restarted daemon begins cold. If another session sealed after this one's last checkpoint and the successor draft file is also gone, the session's older non-elimination decisions are not carried; decisions from its eliminations come back on the next Advance only if the record is at or after that range's from-turn.
- A fork inherits only the parent's eliminations and the decisions they mint (D49's 'records'). The parent's explains-edge decisions are not inherited: the fork's Advance reads explains edges only from its own segments. Pins are project-wide and are minted again anyway.
- Every already_tried and Active call now reads state/lineage-<session>.json, one stat for sessions that are not forks. The negknow benchmark and open-cost rows pass, but mcp's BudgetBF was already failing on develop before this seat, so a quiet-window timing check of already_tried is advisable.
- Rendering in the fork's rehydration block (whether section 3 or 4 has room) belongs to the rehydrate seat's evolution and eviction items. This seat only makes the records and decisions available.

## Independent review

### review:ledger: needs-fixes

- **major** `internal/cli/scheduler_wiring.go:229 (with internal/cli/mcpwire.go:164 recordedLedger, internal/cli/daemon.go:138)` — In any project that already holds elimination records, the negative-knowledge ledger is now opened synchronously while the daemon is still being wired, before it accepts connections. The fix moved startup cost onto the path that races the hook connect deadline, and no timing evidence was collected.
  - Evidence: WireObserver calls WireRehydrator (observer_ops.go:105), which installs opts.OpenLedger. wireCheckpointSources then runs `src, _ := sources()` in phase 1. That call runs src.Resolve(), which calls LedgerFn = recordedLedger. When negknow.HasRecords(root) is true, recordedLedger calls open() and so negknow.Open. Open loads the whole log, runs reconcile (on the first run after this change it rebuilds and fsyncs tried.bloom for every project with a stale record) and runs refreshAtOpen, bounded by openRefreshDeadline = 250ms. All of this happens before NewWithLease and Run. At base, a startup or resume daemon opened no ledger until the first ledger tool call or compaction. test/e2e/v5_x05_test.go:489-494 documents that the session-start dial budget is hookConnectDeadlineFloor (250 ms), and that a daemon still coming up makes the hook spool and return empty output. The seat ran no hot-path or cold-start rows.
  - Fix: Keep the wiring-time and registration-time sources() calls non-opening. For example, give phase 1 and registerCheckpointIdle a set whose LedgerFn is liveLedger, and let only the advance_frontier idle task use recordedLedger (or a variant marked by context), so the open happens on the first idle tick after Run is accepting. Alternatively, keep the behaviour and measure daemon spawn-to-accept over a project with a populated, stale-bearing ledger (the hot-path/cold-start rows in the quiet window), then record the number for the owner.
- **minor** `internal/cli/fsck.go:1840, 2563, 2586; docs/security.md:251` — fsck still follows the superseded active-only rule. An absent tried.bloom with only stale records is reported as an 'ordinary cold start' and --repair skips it. The repair also reports 'rebuilt from N active record(s)', although RebuildBloom now keeps stale records too. docs/security.md still says fsck rebuilds tried.bloom 'from active records'.
  - Evidence: classifyTriedBloom(row, active) raises the defect only when activeRecords > 0. fsckRepairTriedBloom returns early when `errors.Is(loadErr, fs.ErrNotExist) && !fsckHasActiveEliminations(l)`. The After text is `rebuilt from %d active record(s)` with health.Active. Under D49 a stale record's key must be in the filter, and ADR 0009's new addendum says so.
  - Fix: Count active+stale (total records) in classifyTriedBloom and in the repair gate (rename fsckHasActiveEliminations to a has-any-record check, or reuse negknow.HasRecords). Report health.Active+health.Stale in the repair's After text. Update docs/security.md §7 to say 'from the elimination records, active and stale'.
- **minor** `internal/checkpoint/writer.go:597 (seedTierOne) / carryDecisionsLocked` — The D49 decision carry reads only the draft's parent checkpoint. A cold draft carries nothing when its derived parent (maxSeq) was sealed by another session, even though an existing helper finds the session's own newest checkpoint. D49 says a decision stays in every later checkpoint of the session. The implementer disclosed this as an open issue but did not fix it, although the fix is one call.
  - Evidence: `if !derived || pc.Session == d.session { own = &pc }` and then `if own != nil { d.carryDecisionsLocked(own.Decisions, invs) }`. FileWriter.ownLatest (intent.go:444) already returns session s's own newest verifying checkpoint, and seedIntent uses it as a fallback. Reachable when the successor draft file is missing, corrupt, or set aside (resumeDraft: unreadable, other session, or seqClaimed after a crash) and another session sealed in between.
  - Fix: In seedTierOne, when own == nil and the parent was derived, carry from `w.ownLatest(ctx, d.session)` when it exists (decisions only; leave the parent/intent logic as is). Add a row: session A seals, session B seals, A's draft file is removed, A compacts again, and A's elimination decision is still present.
- **minor** `internal/cli/daemon.go:136; internal/cli/mcpwire.go:128-133; plans/00-ARCHITECTURE.md:1743` — Comments and specs contradict the new behaviour. The comment at daemon.go:136 says wireCheckpointSources 'opens NO ledger'. openingLedger's doc says 'every OTHER accessor (the checkpoint SourceSet supplier, the scheduler's LedgerFn) still only reads'. 00-ARCHITECTURE.md's Ledger interface still says 'rebuilds tried.bloom from ACTIVE RECORDS ONLY', and the V3-SP-09, V1-VERIFY I8 and V3-report rows state the same superseded rule with no D49 erratum.
  - Evidence: recordedLedger (mcpwire.go:164) opens through opts.OpenLedger when HasRecords, and it is the supplier's LedgerFn (scheduler_wiring.go:196). ADR 0009 and doc.go were updated, but the architecture spec and the in-code wiring notes were not.
  - Fix: Update the daemon.go:136 and openingLedger comments to name recordedLedger's open-over-records behaviour. Add a D49 erratum or pointer in plans/QOMPACK-ERRATA.md (or hand it to the docs seat) for 00-ARCHITECTURE.md §3.3/§5.10 and the I8 row, so later waves do not re-derive the active-only rule.
- **nit** `internal/checkpoint/lineage.go:216 (Ancestry)` — An unreadable or contract-violating lineage record silently ends the ancestry walk: no log line and no counter. The fork then quietly loses its parent's eliminations. The sibling reader rehydrateService.lineage warns in the same situation.
  - Evidence: `if err != nil || rec == nil || rec.ParentSession == "" || visited[...] { return out }`. The error is dropped. Ancestry runs on every Begin and every already_tried/Active call, so an unconditional log would be noisy.
  - Fix: Count the failure (for example checkpoint.lineage_unreadable) and Warn once per session, keeping the fail-to-inherit-less behaviour.
- **nit** `internal/checkpoint/noledger.go:22-23 / finalize.go:240` — The claim 'Every other Begin ... still refuses a set whose ledger could not be resolved' is not strictly true. Finalize's successor Begin (afterSeal) and the cadence seal (finalizeIfDue) reuse d.src, which on an admitted draft carries the negknow.Deferred stand-in. Resolve sees a non-nil Ledger and admits it without the allowNoLedger mark. The behaviour is still fail-closed, because Deferred answers ErrNotOpen once records exist, so only the comment is wrong.
  - Evidence: admitNoLedger sets src.Ledger = negknow.Deferred(...), and Begin stores it as live.src or d.src. afterSeal calls w.Begin(ctx, d.session, seq, src) with that src.
  - Fix: Reword the comment to say that a draft admitted under the allowance keeps the Deferred stand-in through its seal and successor, which fails closed once records exist, or strip the stand-in in afterSeal before the successor Begin.

## Fix seat (review resolution) — status `done`, head `5409ae98`

### Root cause

Major: wireCheckpointSources' phase-1 snapshot and registerCheckpointIdle both called the resolving supplier, so recordedLedger ran negknow.Open before Run accepted connections in any project holding records. Minor 1: fsck's classifyTriedBloom and its repair gate counted active records only, the rule D49 superseded. Minor 2: seedTierOne carried decisions only from the draft's parent checkpoint, which for a cold draft is the project's newest seal and can belong to another session. Minor 4: the comments and the 00-ARCHITECTURE.md Ledger spec were not updated with the behaviour.

### Summary

FIX seat for w15-ledger, working in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15-ledger on closeout/w15-ledger. My four commits sit on top of the implementer's four (ffe5e18e, dfff29cb, f002064e, 2c60aafd). All four review findings were correct and are fixed. Each fix started with a test that failed first; the failing runs are logged under plans/sdd/V6-closeout/w15-ledger/runs/.

## Review resolution

**1. MAJOR: the ledger opens during daemon wiring. Correct, fixed in f1ad272e.**
- Root cause, confirmed: `wireCheckpointSources` ran `src, _ := sources()` as phase 1, and `registerCheckpointIdle` ran `cw.sources()`. `sources()` calls `SourceSet.Resolve`, which calls `LedgerFn = recordedLedger`. In a project where `negknow.HasRecords` is true, that runs `opts.OpenLedger()`, and so `negknow.Open`, before `NewWithLease`/`Run` accepts connections.
- The failing test `TestCheckpointWiringOpensNoLedgerBeforeRun` (internal/cli/recorded_ledger_test.go) sets up a project holding one record and runs the whole wiring sequence: wiredCheckpointOptions, wireScheduler, `daemon.New`, `registerCheckpointIdle`, `registerSchedulerIdle`. It then asserts `opts.LedgerHandle()` is still nil, and that the supplier's first call afterwards opens the ledger through the shared opener. Before the fix it failed at phase 1 (runs/review-major-wiring-red.txt).
- Fix (internal/cli/scheduler_wiring.go): the supplier is split in two.
  - `set()` builds the SourceSet without resolving it. It reads `LedgerHandle` and keeps the `recordedLedger` accessor.
  - `sources()` is `set()` plus `Resolve`. On success it now returns the resolved set, so a ledger it just opened comes back with it. On error it returns the partial set, as before.
  - `BindCheckpoint` and `WireCheckpoint` take the `set()` snapshot. `SetSources` only calls Validate, and WireCheckpoint uses the snapshot only as its static fallback, which never applies because a supplier is always passed.
  - The open now happens on the first idle tick (advance_frontier or materialize_pins), the first scheduler advance or the first compaction after Run is serving. Startup is back to the base behaviour.
- The `daemon.go` wiring comment and the `openingLedger`/`recordedLedger` docs were updated in the same commit (this is also part of finding 4).
- I did not collect timing numbers. The structural fix removes the open from the startup path, so there is no new number to hand the owner.

**2. minor: fsck still uses the active-only rule. Correct, fixed in 0c62f5eb.**
- The failing test `TestFsck_AbsentFilterWithOnlyStaleRecordsIsADefect` (internal/cli/fsck_test.go) has one stale record and no tried.bloom. Before the fix, the read-only pass said "sketches/tried.bloom is absent, which is an ordinary cold start" and the row was ok=true (runs/review-minor1-fsck-red.txt).
- Fix in internal/cli/fsck.go:
  - `classifyTriedBloom` now receives active+stale.
  - The repair gate `fsckHasActiveEliminations` is now `fsckHasFilteredEliminations` (active or stale).
  - The repair's After text now reads "rebuilt from N record(s) (A active, S stale)".
  - The corrupt/truncated messages and the repair-list comment were reworded.
- docs/security.md §7 now says tried.bloom is rebuilt "from the elimination records, active and stale".

**3. minor: a cold draft carries no decisions when another session sealed last. Correct, fixed in bce3342c.**
- The failing test `TestColdDraftCarriesDecisionsPastAnotherSessionsSeal` (internal/checkpoint/decisions_carry_test.go): session A seals, then another session seals, then A's draft file is removed, the writer is reopened and A compacts again. Before the fix, A's elimination decision was missing (runs/review-minor2-carry-red.txt).
- Fix in `seedTierOne` (internal/checkpoint/writer.go): when `own == nil` and the parent was derived, decisions carry from `w.ownLatest(ctx, d.session)`. The parent and intent logic are unchanged.

**4. minor: comments and the architecture spec contradict the new behaviour. Correct, fixed in f1ad272e and 5409ae98.**
- The code comments were fixed with finding 1.
- plans/00-ARCHITECTURE.md: the Ledger interface's RebuildBloom comment now states D49 (active and stale), points to ADR 0009's D49 addendum, and names the V3-SP-09 plan, V1-VERIFY row 3 and V3-report I8/X2 as superseded.
- I did not rewrite those historical plans or reports. I also did not add a QOMPACK-ERRATA.md entry: that file records revisions of Qompack.md, and Qompack.md itself says "Bloom rebuilt from records", which is not active-only.

## Commands and results
Windows, `-p 2`, on a machine shared with other seats. Nothing failed, so no failure needed a solo re-run.
- `go test ./internal/cli -run '^TestCheckpointWiringOpensNoLedgerBeforeRun$' -count=1 -p 2`: FAIL before the fix, PASS after.
- `go test ./internal/cli -run '^TestFsck_AbsentFilterWithOnlyStaleRecordsIsADefect$' -count=1 -p 2`: FAIL before the fix (re-verified with the final fixture), PASS after.
- `go test ./internal/checkpoint -run '^TestColdDraftCarriesDecisionsPastAnotherSessionsSeal$' -count=1 -p 2`: FAIL before the fix, PASS after.
- Neighbouring rows re-run and passing: `TestProductionCheckpointSourcesOpenTheLedgerOverRecords`, `TestProductionCheckpointSourcesOpenNothingWithoutRecords`, `TestProductionCheckpointSourcesStayLazyAndReportUnavailable`, `TestFsck_RepairQuarantinesAFailingObjectAndRebuildsTheFilter`, `TestSecondCheckpointCarriesTheSessionsEarlierDecision`, `TestRestartedWriterCarriesTheSessionsEarlierDecision`, `TestCarriedDecisionEndsWhenItsPinIsRemoved`.
- Full packages, once each: `go test ./internal/checkpoint -count=1 -p 2 -timeout=30m` ok (66.3s); `go test ./internal/cli -count=1 -p 2 -timeout=30m` ok (75.0s); `go test ./test/docs -count=1 -p 2` ok.
- `go run ./tools/devtool fmt-check`: exit 0. `go vet ./internal/cli ./internal/checkpoint` on Windows and with GOOS=linux: exit 0. `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: exit 0.
- Evidence logs are in plans/sdd/V6-closeout/w15-ledger/runs/: review-major-wiring-red.txt, review-minor1-fsck-red.txt, review-minor2-carry-red.txt, review-fixes-green.txt.

## Criterion changes
None. No assertion was loosened, no skip or threshold was added, and no golden was touched. The fsck messages changed wording (active-only to active and stale), as D49 requires; no test pinned the old strings.

## Owner decisions
No new budgets or constants. There were no needs_owner items to carry forward (the implementer had none).

I started no background processes that are still running, and the working tree is clean.

### Commits

- f1ad272e fix(cli): open no ledger while the daemon is still wiring
- 0c62f5eb fix(cli): count stale records in fsck's tried.bloom check
- bce3342c fix(checkpoint): carry decisions past another session's seal
- 5409ae98 docs(arch): record D49's active-and-stale filter rule

### Tests

- `go test ./internal/cli -run '^TestCheckpointWiringOpensNoLedgerBeforeRun$' -count=1 -p 2` — RED before fix (ledger opened by phase-1 snapshot), PASS after
- `go test ./internal/cli -run '^TestFsck_AbsentFilterWithOnlyStaleRecordsIsADefect$' -count=1 -p 2` — RED before fix (ordinary cold start, ok=true), PASS after
- `go test ./internal/checkpoint -run '^TestColdDraftCarriesDecisionsPastAnotherSessionsSeal$' -count=1 -p 2` — RED before fix (elimination decision missing), PASS after
- `go test ./internal/cli -run '^(TestProductionCheckpointSourcesOpenTheLedgerOverRecords|TestProductionCheckpointSourcesOpenNothingWithoutRecords|TestProductionCheckpointSourcesStayLazyAndReportUnavailable)$' -count=1 -p 2` — PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/checkpoint -run '^(TestSecondCheckpointCarriesTheSessionsEarlierDecision|TestRestartedWriterCarriesTheSessionsEarlierDecision|TestCarriedDecisionEndsWhenItsPinIsRemoved)$' -count=1 -p 2` — PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./internal/checkpoint -count=1 -p 2 -timeout=30m` — ok (66.3s)
- `go test ./internal/cli -count=1 -p 2 -timeout=30m` — ok (75.0s)
- `go test ./test/docs -count=1 -p 2` — ok
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/cli ./internal/checkpoint (Windows and GOOS=linux)` — exit 0 both
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0

### Criterion changes

- None: no assertion loosened, no skip, threshold or golden change. fsck's tried.bloom messages were reworded from active-only to 'active and stale' as D49 requires; no test pinned the old strings.

### Open issues

- Not run under this seat's daytime limits: the hot-path and cold-start rows, the whole test/integration and test/e2e packages, and Linux (the container is stopped). The quiet-window/integration run should include them, plus the negknow, daemon and test/e2e rows the implementer touched.
- On a restarted daemon in a project that holds records, the ledger now opens on the first idle tick, scheduler advance or compaction after Run accepts. That is off the hook path, but the first advance_frontier tick pays for negknow.Open. No timing was measured because none was needed for this fix.
- Pre-existing weak fixture, not changed: TestFsck_RepairQuarantinesAFailingObjectAndRebuildsTheFilter writes "source":"mcp". Source is an integer on the wire, so negknow's replay drops that record and the repair rebuilds from 0 records. The test asserts only that the repair ran. Recommend a follow-up to use "source":1 and assert the record count.
- Historical plan and report rows that state the active-only rule (V3-SP-09, V1-VERIFY row 3, V3-report I8/X2, V2-SP-03/V2-SP-06 quotes) were left as historical records. The pointer lives in 00-ARCHITECTURE.md and ADR 0009's D49 addendum.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


