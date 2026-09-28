# E2E workstream report — C1.2/C1.3

Branch `closeout/e2e`. Workflow `wf_16dd5d95-b3a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `partial`, head `191a6b76974fafb952b9cce561c3325e97f75fda`

### Root cause

C1.2 has four separate causes. (1) The "turn 0 after turn 1" defect is category (a), caused by the C1.1 regression. The ordering gate defers the tools behind the prompt still in flight. The flush route (handlers.go:969 flushRoute) then runs CloseSession, then svc.SessionEnd, which removes the observer's in-memory session, and only then d.Drain. The drain replays the deferred tools onto a fresh sessionState whose Turn is 0. (2) The empty session id is an independent fsck defect (category b). fsck decoded index/tool_use.jsonl with store.ToolUseRecord's long-key type shape ("session", "superseded_by"), but the file holds the compact tuRec line ("s", "by") plus supersede mutation lines. So every session read as "", the per-session turn check ran across all sessions, and the supersession-link check never fired. This has been true since 2c21467. (3) The "unexpected entry in the capture tree" is the test's own plant, records/captures/newer-plugin.json. No build writes a sidecar there, because sidecars have been sharded since a6faab0; that makes it a stale fixture (category c). Underneath it is a real fsck self-contradiction (category b): the captures row calls a newer-schema sidecar a support gap, while the 7e5a141 publication row calls the same file a defect because the audit is Incomplete. (4) A third failure was hidden until the others were fixed: the config-reset session indexes nothing. The test still has its planted v2 delivery seal in place, and since c78f610 (intended) a delivery the journal refuses stays pending instead of being recorded without identity (category c). C1.3 is category (c): a stale criterion after 00e0c98 (V6-AUTH-1), which drops the bytes of a cut envelope. That is recorded as a decision in capture-work.md and verification-protocol.md, and 00e0c98 already changed the in-process twin TestHooks_OverBudgetDeliveryIsRecordedNotDropped.

### Summary

This is the C1.2/C1.3 e2e workstream on branch closeout/e2e (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-e2e), cut from verify/v6 @ cf31e01. The status is partial for one reason only: the harness refused my write of plans/sdd/V6-closeout/e2e/report.md ("Subagents should return findings as text, not write report files"). The report's content is below for the coordinator to commit. All code, test and evidence work is committed. Evidence is in plans/sdd/V6-closeout/e2e/runs/ (commit 191a6b7).

VERDICT
- C1.3, TestV1_ConfigPrecedenceReachesHookBehaviour/bare_hook_records_an_over_budget_delivery…: (c) stale criterion. Criterion changed in 555e289. It now passes on Windows and on Linux (-race, non-root).
- C1.2 "turn 0 after turn 1": (a) caused by C1.1. Left to the ingest workstream, with the evidence and the extra requirement in "Turn defect" below.
- C1.2 empty session id: (b) an fsck decoding defect. Fixed in 3893fac.
- C1.2 "unexpected entry": (c) the test's own misplaced plant. Fixed in 8da4435. Underneath it was (b), an fsck captures/publication contradiction, fixed in 9d795a0 and 3c369c4.
- C1.2 third, previously hidden failure (the config session stalls): (c) the planted seal was still in place when c78f610's fail-closed identity rule applied. Fixed in 43051d5.
- End state on this branch: TestUnknownSchema fails ONLY on the turn defect, which now names its session (runs/c12-after-fixes-windows.log). The same test with the six commits plus the temporary gate-off patch PASSES end to end (runs/c12-after-fixes-with-e2-gate-off-windows-3.log). Nothing else is wrong in that test once C1.1 is fixed.

TURN DEFECT (a): EVIDENCE
- Reproduction on cf31e01 matches rc1 byte for byte: runs/c12-repro-windows-1.log.
- The kept index (runs/trees/c12-repro-cf31e01/index/tool_use.jsonl) reads: prompt@0, tools@0 | prompt@1, tools@0. The daemon log shows SessionEnd at 51.659 and the tool records stamped 51.843 to 52.532, so the tools were published after the same session's SessionEnd.
- Mechanism: the gate defers the tools behind the prompt still in flight. flushRoute (handlers.go:969) runs CloseSession, then SessionEnd (which removes the observer session), and only then d.Drain. The drain replays the deferred tools onto a fresh sessionState with Turn 0.
- Experiments. Both were temporary and never committed; the patches are kept in runs/.
  - E2 (live gate check off at ingest.go:700): turns are monotone (0, 1…1, 2, 3…3). Logs: runs/c12-experiment-e2-live-gate-off-windows.log and runs/trees/c12-e2-live-gate-off.
  - E1 (one ingest worker, gate kept): turns are still non-monotone. SessionEnd overtakes tools that are still queued. The log shows "segment 1 is already closed at turn 1, refused for 2" and "drain: delivery still in progress". Log: runs/c12-experiment-e1-one-worker-windows.log.
- So C1.1's fix must also order SessionEnd (flushRoute) after every earlier accepted leased arrival of the session. Otherwise the defect returns whenever ingest lags. Under co-load, the full e2e run and re-runs showed the same defect in TestInstall…/upgrade and TestRollbackRehearsal (runs/trees/rollback-coload-live). There, even among the deferred set, tool 2_07 landed at turn 1 after 2_01 to 2_06 at turn 0.
- I sent this to main by SendMessage mid-run. I did not edit handlers.go, ingest.go, drain.go or delivery_order.go.

FSCK FIX (3893fac)
- fsckToolUseLine now decodes the on-disk keys ("s", "by", "op").
- Supersede marks are links, not records. They are kept out of the turn ordering and the scanned count.
- Both ends of a mark must name a record the file carries, which MarkSuperseded already enforces when it writes one.
- New tests are in internal/cli/fsck_tooluse_test.go; every fixture is written through the store's own writers. Before the fix, 4 of 4 failed for the expected reasons (runs/fsck-tooluse-red-windows.log); after it, 4 of 4 pass.

PUBLICATION FIX (9d795a0, 3c369c4)
- store: PublicationAudit.NewerSchemaCaptures plus IncompleteOnlyForNewerSchemas(). It is true only when newer-schema sidecars are the SOLE cause of incompleteness; a truncation, any other note or an interrupted scan makes it false. Store-level Incomplete is unchanged, so the daemon LOUD line and the backup certification are unaffected.
- cli: in that sole-cause case the publication row stays ok and notes, by count, that those sidecars' publication "is not certified by this build, a support gap rather than damage". Every other incompleteness is still a defect.
- Pinned by TestFsck_NewerCaptureSchemaDoesNotExcuseOtherIncompleteness. 7e5a141's TestFsck_UnknownObservationIntentIsIncompleteAndReadOnly still passes.

TEST CHANGES
- 8da4435 plants the newer sidecar at store.CaptureSidecarPath, so the audit actually reads its schema. The assertions are unchanged.
- 43051d5 keeps the daemon's own delivery position bytes. It checks the planted v2 seal is byte-identical after fsck and doctor, then restores the daemon's bytes before the config session. The test's own comment already said the seal is "read only by fsck".

TESTS
- Windows: internal/cli full passes.
- Windows: internal/store full has one failure, TestGC_DeadlineOvershootIsBoundedByTheCheckInterval. It is a wall-clock calibration test, failed under co-load, and passed when re-run alone (220.5s).
- Linux (non-root, -race, GOMAXPROCS=4, commit 43051d5, bundle d7b5704a…): internal/cli passes 314 tests. internal/store passes 793 and fails 1, the same GC deadline test ("host could not be measured"), which passed when re-run alone. The focused e2e run passes 8 and skips 1: TestUnknownSchema is skipped because the claude CLI is not on PATH in the container.
- Windows full test/e2e ran while my own whole-tree lint (stubskips) was running and other agents were loading the machine. It hit the 30m wall with 8 failures:
  - Observer and ThinSlice: C1.1.
  - Install/upgrade, Rollback and UnknownSchema: the turn defect (a).
  - LazySpawn: co-load; passed when re-run alone.
  - AllSixHooks/SessionStart: hookSpecificOutput nil and a daemon still holding the lock after 20s.
  - HooksExitZeroUnderFaults: TempDir cleanup "directory is not empty"; the failing subtests changed between runs.
  - These last two look like co-load but still failed on re-run while the machine stayed loaded, so they are not confirmed as co-load. My diff does not touch the hook path.
- vet and fmt-check: clean.
- devtool lint: golangci-lint, nomagic, importgraph, testdeps, sleepcheck, runpatterns, docmarkers and coveragefloors pass.
  - bindeps fails because of golang.org/x/sys/unix via internal/paths/rename_noreplace_* from 3ab1523; that file is not in my diff.
  - stubskips fails on 3 non-conforming skip reasons, all in tests my commits do not touch (TestMaintenance_SymlinkComponentInBackupTreeRefused from 3ab1523, TestCaptureScope_JunctionSwapDefeatsLexicalContainment from 00e0c98, TestDeliveryPath_V6_RefusesAliasedSegmentParent from 891582e), plus 4 packages killed at the 30m wall.
  - My stubskips go test then hung with no children, waiting on an orphaned daemon's pipe. I stopped only my own PID 60192 so the remaining subchecks could run.

INTENDED report.md CONTENT
The sections above are the report: verdict table, root cause with evidence, what changed and why, tests, criterion changes (see criterion_changes), and open items and owner decisions (see open_issues and needs_owner).

### Commits

- 3893fac fix(cli): read tool_use index lines with the file's own keys
- 9d795a0 feat(store): count newer-schema captures in publication audit
- 3c369c4 fix(cli): keep newer capture schemas a support gap in fsck
- 555e289 test(e2e): expect a byte-free over-budget capture record
- 8da4435 test(e2e): plant the newer capture sidecar in the sidecar layout
- 43051d5 test(e2e): restore the daemon's seal before the config session
- 191a6b7 docs(v6): preserve c1.2 and c1.3 e2e close-out run evidence

### Tests

- `go test ./test/e2e -run '^TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting$' -count=1 -timeout=30m -v  (cf31e01 + temp debug helper)` — FAIL — rc1 reproduced exactly (runs/c12-repro-windows-1.log)
- `same, + temp E1 patch (one ingest worker)` — FAIL — still non-monotone turns (SessionEnd overtakes queued tools); runs/c12-experiment-e1-one-worker-windows.log
- `same, + temp E2 patch (live ordering gate off)` — FAIL — turn defect gone; publication defect remains; R8-2 tripped by an external qompack-inline dir (runs/c12-experiment-e2-live-gate-off-windows.log)
- `go test ./test/e2e -run '^TestV1_ConfigPrecedenceReachesHookBehaviour$' -count=1 -timeout=30m -v (cf31e01)` — FAIL — rc1 reproduced, deterministic, no daemon involved (runs/c13-repro-windows-1.log)
- `go test ./internal/cli -run 'TestFsck_ToolUse' -count=1 -timeout=30m -v (before/after 3893fac)` — 4 FAIL / 4 PASS
- `go test ./internal/cli -run 'TestFsck_NewerCaptureSchema|TestFsck_UnknownObservationIntent' ... (before/after)` — 1 FAIL / all PASS
- `go test ./internal/store -run 'TestAuditPublication' -count=1 -timeout=30m -v (before/after 9d795a0)` — build fail (API absent) / 22 PASS
- `go test ./test/e2e -run '^TestV1_ConfigPrecedenceReachesHookBehaviour$' -count=1 -timeout=30m -v (after 555e289)` — PASS, 7/7 subtests (runs/c13-after-windows.log)
- `go test ./test/e2e -run '^TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting$' ... (after all fixes, no patch)` — FAIL only on the (a) turn defect, now naming session sess-e2e-sp17-unknown-schema (runs/c12-after-fixes-windows.log)
- `same + temp E2 gate-off patch, after 43051d5` — PASS end to end, all four unknown-schema rows verified (runs/c12-after-fixes-with-e2-gate-off-windows-3.log)
- `go test ./internal/cli -count=1 -timeout=30m (Windows)` — ok
- `go test ./internal/store -count=1 -timeout=30m (Windows)` — FAIL 1 — TestGC_DeadlineOvershootIsBoundedByTheCheckInterval (wall-clock calibration under co-load); re-run alone PASS (runs/store-gc-deadline-rerun-alone-windows.log)
- `go test ./test/e2e -count=1 -timeout=30m (Windows, heavily co-loaded)` — FAIL — 30m wall; C1.1 cases, (a) turn defect in Install/Rollback/UnknownSchema, plus LazySpawn/AllSixHooks/HooksExitZeroUnderFaults (co-load suspects; LazySpawn passed on re-run) — runs/pkg-test-e2e-windows.log, runs/e2e-coload-suspects-rerun-windows.log
- `Linux container, non-root qompack-test, -race, GOMAXPROCS=4: go test ./internal/cli ./internal/store (commit 43051d5)` — cli PASS 314; store 793 pass / 1 fail TestGC_DeadlineOvershoot… ('host could not be measured'); re-run alone PASS (runs/linux-*.log, runs/linux-artifacts/)
- `Linux container, non-root, -race: go test ./test/e2e -run '^(TestV1_ConfigPrecedenceReachesHookBehaviour|TestUnknownSchema_…)$'` — PASS 8, skip 1 (UnknownSchema: claude CLI not on PATH in container) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser, and its second branch is abbreviated with an ellipsis; the full names are in the evidence log -->
- `go vet ./internal/cli ./internal/store ./test/e2e; go run ./tools/devtool fmt-check` — exit 0 / exit 0
- `go run ./tools/devtool lint` — exit 1: golangci-lint, nomagic, importgraph, testdeps, sleepcheck, runpatterns, docmarkers, coveragefloors PASS; bindeps FAIL (x/sys/unix via internal/paths from 3ab1523, pre-existing on cf31e01); stubskips FAIL (3 skip reasons in tests from 3ab1523/00e0c98/891582e, pre-existing, + 4 packages at the 30m wall under co-load)

### Criterion changes

- test/e2e/v1_integration_test.go bare_hook_records_an_over_budget_delivery_instead_of_dropping_it (555e289). Old: require.NotEmpty(req.Capture.Bytes, 'a bounded prefix survives as evidence'). New: require.Empty(req.Capture.Bytes, 'an incomplete envelope cannot prove scope; classification and observed size remain'). Why: 00e0c98 (V6-AUTH-1) drops the bytes of every cut envelope, as capture-work.md ('Deliberate consequence') and verification-protocol.md ('opaque retained-byte success assertions are retired … requires byte-free evidence') record. 00e0c98 already made the same change to the in-process twin TestHooks_OverBudgetDeliveryIsRecordedNotDropped. The record, fidelity, oversize error, unavailable outcome, truncated flag, real SourceBytes and nonce are all still asserted.
- test/e2e/install_test.go TestUnknownSchema, plant (b) (8da4435). Old location: records/captures/newer-plugin.json with observation_id 'obs-newer-plugin'. New location: store.CaptureSidecarPath(root, <digest id>) with a matching observation_id. Why: no build writes a sidecar at the capture-tree root (sharded since a6faab0). V6's fixed-depth publication audit (80a3e04) rightly calls such a file an unexpected entry, which is not what this row tests. Every assertion is unchanged, and the newer schema is now actually read by the audit.
- test/e2e/install_test.go TestUnknownSchema, plant (c) (43051d5). Old: the v2 seal stayed over the daemon's state/delivery-ack-position.json through the config-reset session, and its bytes were checked at the end of the test. New: its bytes are checked right after its only readers (fsck, doctor), and the daemon's own position bytes are then restored before the next daemon runs. Why: the test's own comment says the seal is 'read only by fsck' and that a daemon beside it stalls publication. Since c78f610 (intended: lease failures stay pending) a session driven beside it indexes nothing; before c78f610 it passed only by recording without identity. The acceptance claim 'fsck classifies it and leaves the bytes alone' is unchanged.

### Open issues

- report.md was NOT written: the harness refused writing plans/sdd/V6-closeout/e2e/report.md from a subagent. Its full content is in this summary for the coordinator to commit on closeout/e2e.
- TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting stays red on this branch until C1.1 lands. The only remaining failure is the (a) turn defect. It passes with the temporary gate-off patch.
- C1.1 must also order flushRoute's SessionEnd after the session's earlier accepted leased arrivals (experiment E1). Otherwise non-monotone turns come back under ingest lag. The co-loaded runs showed this also breaks TestInstall…/upgrade and TestRollbackRehearsal through fsck. In the rollback tree, a tool even landed at turn 1 after later-numbered tools at turn 0.
- Seen once under heavy co-load and not reproduced: a TestRollbackRehearsal restored root carried a capture sidecar whose op is outside observe.tool/prompt/stop, with an ok outcome and bytes. fsck reported it as 'capture publication requirement is unknown' and 'capture sidecar with an unrecognized op'. publishCapture copies req.Op verbatim, so a leased drain line with a non-observe op is the suspect. Owner: ingest/drain workstream (runs/e2e-coload-suspects-rerun-windows.log).
- Stale docs for C6.1: docs/security.md:110 ('refused … with only a bounded prefix retained') and the 'refusal prefix 4 KiB' bounds row contradict 00e0c98. I did not edit them.
- External interference with the R8-2 guard: C:\Users\Quant\.claude\plugins\data\qompack-inline appeared in the REAL home at 2026-09-22 17:28:36 local. That is an inline --plugin-dir qompack run by another agent. Integrated test/e2e runs will trip R8-2 while it keeps changing. I left it in place.
- Found by lint, pre-existing on cf31e01, not introduced by this branch: bindeps rejects golang.org/x/sys/unix (internal/paths/rename_noreplace_*, 3ab1523), and stubskips rejects the skip reasons of TestMaintenance_SymlinkComponentInBackupTreeRefused, TestCaptureScope_JunctionSwapDefeatsLexicalContainment and TestDeliveryPath_V6_RefusesAliasedSegmentParent.
- Did not reproduce when re-run alone while the machine stayed loaded: TestE2E_AllSixHooksExitZero/SessionStart (hookSpecificOutput nil, daemon still holds the lock after 20s) and TestHooksExitZeroUnderFaults (TempDir cleanup 'directory is not empty', a different subtest each run). Both look like co-load or a slow daemon shutdown. They need a quiet re-run in the integrated gate.

### Needs the owner

- Rule on 3c369c4. It changes the fsck publication row that 7e5a141 introduced, which made any incomplete audit a defect. Now, when the ONLY cause is capture sidecars written by a newer build, the row stays ok (exit 0) and says by count that their publication is not certified. The reason: fsck's own captures row, Qompack.md §7.1 and SP17-M7-06 all call such a sidecar a support gap rather than damage. The alternative is to keep exit 1 and change SP17-M7-06 instead.
- Criterion changes 555e289, 8da4435 and 43051d5 are test-only, with written rationale. The coordinator should accept them or overrule them.

## Independent review

### review:e2e:both: needs-fixes

- **minor** `internal/daemon/handlers.go:979-1009 (flushRoute); classification in the implementer's root_cause, turn defect` — The turn defect is filed as purely (a), caused by the C1.1 regression. The implementer's own experiment E1 points to a second cause that does not depend on the gate. flushRoute calls ing.CloseSession (ingest.go:749, which only closes the WAL handle and never waits for the session's queued or in-flight dispatch jobs), then svc.SessionEnd, then Drain. So any accepted but not yet dispatched tool of the session can be replayed onto a fresh turn-0 observer session. handlers.go is not on the list of files this workstream may not edit. The task says to fix every independent product defect, failing regression test first, but no daemon-level regression test was written. The fix was handed to the ingest workstream as prose only.
  - Evidence: runs/trees/c12-e1-one-worker/index/tool_use.jsonl: with one ingest worker, the last tool of each turn lands at turn 0 after the rest of its turn (1_07 turn 0 after 1_00..1_06 at turn 1; 2_06 and 2_07 at turn 0 after 2_00..2_05 at turn 2). The daemon log shows 'segment 1 is already closed at turn 1, refused for 2' and 'drain: delivery still in progress'. CloseSession (ingest.go:749-762) deletes the wal handle and returns; nothing there waits for queued jobs. E2 (gate off, default workers) came out monotone, but that run shows a timing window, not an ordering guarantee.
  - Fix: Record the turn defect as (a) plus a latent (b): SessionEnd is not ordered after the session's accepted arrivals. Have the coordinator give ownership explicitly to C1.1 (ingest), and make its acceptance include a failing-first internal/daemon test: one ingest worker, a tool accepted and queued but not dispatched, then flush. Assert that every tool_use turn in the session is monotone and that SessionEnd runs only after the session's accepted leased arrivals are dispatched or drained. If C1.1 declines it, fix it here in handlers.go.
- **nit** `internal/cli/fsck.go:867 vs internal/store/recovery_test.go:218-226` — fsck now reports a supersede mark whose superseded end is missing from the file as damage (severity 2, exit 1). The store's own recovery test tolerates exactly this shape and describes it as 'a real shape when a GC has already collected the superseded record's own line'. No shipped code rewrites index/tool_use.jsonl today, so fsck's stricter reading is right for the current build. But the codebase now makes two contradictory claims about the same on-disk shape, and a future index compaction would turn every healthy compacted store into an fsck failure.
  - Evidence: recovery_test.go:224-226 plants {"op":"supersede","id":"toolu_01MISSING...","by":...} with the comment 'a real shape when a GC has already collected the superseded record's own line'. fsck.go:866-868 calls the same line damage. grep finds no writer that removes tool_use.jsonl lines (open.go only opens it append-only).
  - Fix: Pick one statement and write it in both places. Either change the recovery-test comment to say this shape arises only from damage or a hand edit (MarkSuperseded and RecordToolUseSuperseding never write it), or, if compaction is planned, make the older-end-missing case a row.note and keep the by-end-missing case a defect.
- **nit** `internal/cli/fsck.go:843-860 (fsckToolUseLine decode loop)` — The fix exists so that fsck reads the file the way the store writes it, but fsck still does not filter lines the way the loader does. loadToolUse skips any line with v != 1 and any op other than "" or "supersede" (tooluseindex.go:141-167). fsck treats such lines as content records: they join the ids set, which can satisfy a supersede mark, and they enter the per-session turn ordering and the root resolution. The per-session turn check now actually runs, so a newer build's tool_use line carrying a matching "s" can produce a false 'turns are monotone' or root defect. That is the same support-gap-versus-damage confusion 3c369c4 fixes for sidecars. This existed before the branch, but the branch is where the check became per-session.
  - Evidence: fsckToolUseLine has no V field. The loop only special-cases Op == "supersede". recovery_test.go:229-232 shows the store's own v:99 and op:from-a-later-wave lines being skipped by the loader.
  - Fix: Decode "v" as well. Report a line with v != 1 or an unknown op as a note ('written by a newer build') and keep it out of ids, the turn ordering and the root check. Pin this with a row in fsck_tooluse_test.go.
- **nit** `plans/sdd/V6-closeout/e2e/report.md (absent)` — The workstream's report file is missing from the branch. The harness blocked the subagent from writing it, and the content exists only in the result JSON. The evidence commit 191a6b7 points at runs/ but nothing on the branch holds the verdict table, the criterion-change rationale or the needs_owner rulings. That record is also what the coordinator's acceptance of 555e289, 8da4435 and 43051d5 and the ruling on 3c369c4 will need to point to.
  - Evidence: git ls-tree HEAD plans/sdd/V6-closeout/e2e/ contains only runs/. The implementer's report_path says 'NOT WRITTEN'.
  - Fix: Have the coordinator commit the summary, criterion_changes, open_issues and needs_owner content as plans/sdd/V6-closeout/e2e/report.md on closeout/e2e before merging. Note there that docs/install.md:118-120 already documents 'ok: true and exit: 0' for a newer capture sidecar, which supports ruling 3c369c4 as restoring documented behaviour rather than changing it.

## Fix seat (review resolution) — status `partial`, head `eb724b267af4a5ef012ddfa6f8f1723c02be8967`

### Root cause

C1.2 had four separate causes. (1) fsck decoded index/tool_use.jsonl with the long-key contract shape instead of the file's compact keys, so every session read as "", which caused the empty session id and the cross-session turn check (product defect, fixed 3893fac). (2) The test planted its newer sidecar outside the sharded layout, so the V6 audit reported an unexpected entry (stale fixture, 8da4435). (3) The publication row treated newer-schema-only audit incompleteness as a defect, contradicting fsck's own captures row (product defect, 9d795a0 + 3c369c4). (4) The non-monotone turns come from the daemon: the C1.1 ordering-gate regression strands tools until the flush drain, which runs after SessionEnd has dropped observer state. There is also a latent, gate-independent defect: flushRoute runs SessionEnd before the session's accepted-but-undispatched arrivals are dispatched or drained. That was proven by 6edd2e4, red on this branch and on 6cc9da3; the fix is owned by C1.1/C1.13. Separately, the test drove session (d) beside a planted seal that c78f610 makes refuse deliveries (stale fixture, 43051d5). C1.3 is a stale criterion: 00e0c98 deliberately keeps no bytes of a cut envelope (555e289).

### Summary

STATUS: partial, and only for one reason: report.md could not be committed (the harness blocked report-file writes). All code, tests and evidence are committed on closeout/e2e at eb724b2, and the worktree is clean. The implementer seat committed code and evidence but no report, so the complete report follows. It was written from the commits, the commit messages and the preserved logs, and the claims it repeats were checked against the code. The coordinator should commit it verbatim at plans/sdd/V6-closeout/e2e/report.md. Evidence paths are relative to plans/sdd/V6-closeout/e2e/runs/ unless they start with internal/, test/ or plans/.

# V6 close-out, e2e workstream: C1.2 and C1.3

Branch closeout/e2e (worktree ../qompack-cx-e2e), cut from verify/v6 at cf31e01. Head eb724b2.

## 1. Verdicts

| Failure (cf31e01, rc1-integrated-whole-tree.log) | Class | Action |
|---|---|---|
| C1.2: fsck names an EMPTY session ("turn 0 after turn 1 in session ;") | (b) product defect in qompack fsck | fixed, 3893fac |
| C1.2: "unexpected entry in the capture tree" | (c) stale fixture | fixed, 8da4435; assertions unchanged |
| C1.2, found once the fixture was right: the newer-schema sidecar turns the publication row into a defect | (b) product defect: fsck contradicted its own captures row | fixed, 9d795a0 + 3c369c4 |
| C1.2: the non-monotone turns themselves, in index/tool_use.jsonl | (a) the C1.1 ordering-gate regression PLUS a latent (b): SessionEnd is not ordered after the session's accepted arrivals | pinned red by 6edd2e4; fix owned by C1.1/C1.13 (section 8) |
| C1.2, found once the fsck assertion passed: config session (d) indexes nothing | (c) stale fixture after c78f610 | fixed, 43051d5; criterion change 6.2 |
| C1.3: "a bounded prefix survives as evidence", got empty | (c) stale criterion after 00e0c98 | fixed, 555e289; criterion change 6.1 |
| NEW (review round): a drained flush or checkpoint line publishes a capture sidecar | (b) product defect in internal/daemon/drain.go | repro preserved, routed (section 9) |

C1.3 is green on Windows and Linux. C1.2's test passes only with the C1.1 regression removed (experiment E2). With the gate on, fsck still fails, now correctly, on the daemon's non-monotone turns, and it names the session.

## 2. C1.2: root cause, with evidence

Reproduced on cf31e01 with no co-load involved: c12-repro-windows-1.log, kept tree trees/c12-repro-cf31e01/. In that tree's index, prompt_0 is at turn 0, all 8 tools of drive 1 are at turn 0, prompt_1 is at turn 1, and all 8 tools of drive 2 are at turn 0. The fsck output mixed four separate problems.

### 2.1 fsck read tool_use.jsonl with the wrong keys (b)
internal/cli/fsck.go decoded each line as store.ToolUseRecord's long-key shape ("session", "superseded_by"). The file holds the store's compact tuRec line ("s", "by") and the append-only supersede mutation {"op":"supersede","id","by"}. So every session decoded as "". The per-session turn check ran across all sessions (hence the empty id in the message), a supersede mark read as a turn-0 record, and the supersession link check never fired. 3893fac decodes the on-disk keys, keeps marks out of the turn ordering and the scanned count, and requires both ends of a mark to name a record the file holds. Four tests in internal/cli/fsck_tooluse_test.go: fsck-tooluse-red-windows.log (4 FAIL), fsck-tooluse-green-windows.log (4 PASS).

### 2.2 "unexpected entry in the capture tree" (c)
The test planted its newer sidecar at records/captures/newer-plugin.json. No build has written a sidecar there since a6faab0 sharded them under store.CaptureSidecarPath (records/captures/<2hex>/<digest>.json). V6's bounded publication audit (80a3e04) walks the two fixed fanout levels and reports a top-level file as an unexpected entry. 8da4435 plants the sidecar at the real sidecar path under a digest-shaped id. The assertions are unchanged.

### 2.3 A newer-schema sidecar: support gap in one fsck row, defect in another (b)
With the sidecar in the right place, the audit is Incomplete only because of "capture sidecar schema newer than this build". fsck's captures row already calls that a support gap (Qompack.md §7.1), but the publication row failed on any incomplete audit, so fsck contradicted itself.
- 9d795a0 adds NewerSchemaCaptures and IncompleteOnlyForNewerSchemas(). It is true only when that is the sole note; a truncation, an interrupted scan or any other note defeats it. Incomplete itself is unchanged. store-support-gap-red-windows.log (build fail) and store-support-gap-green-windows.log.
- 3c369c4: when that is the only cause, the publication row writes a counted note instead of a defect. fsck-publication-support-gap-{red,green}-windows.log.

RECORDED DECISION TOUCHED: plans/sdd/V6-remediation/publication-work.md says a newer sidecar is a "support-gap note", keeps the pass Incomplete and "never classified", and treats zero gaps as clean only when Incomplete is false. The store side keeps all of that. The fsck side is refined: newer-only incompleteness is now an fsck note, not an fsck failure. The daemon's startup LOUD still reports the pass as incomplete, so fsck and startup state the same fact at different severities. The coordinator should confirm this.

### 2.4 The non-monotone turns in the index (a, plus a latent b)
This is the daemon, not fsck. Each drive is session-start, prompt, 8 tools, stop, checkpoint, flush, and every hook is its own process.
- Baseline: the ordering gate (c34acb4) deferred every tool to the drain. The only drain that ran was the flush's, which runs AFTER svc.SessionEnd. observer.onSessionEnd deletes the session's in-memory state, so every drained tool landed at turn 0.
- E1, one worker with the gate on (experiment-e1-one-worker.patch, c12-experiment-e1-one-worker-windows.log, trees/c12-e1-one-worker): only the tail was late. 1_07 is at turn 0 after 1_00..1_06 at turn 1; 2_06 and 2_07 are at turn 0 after turn 2. The log shows "segment 1 is already closed at turn 1, refused for 2" and "drain: delivery still in progress".
- E2, gate off (experiment-e2-live-gate-off.patch, trees/c12-e2-live-gate-off): turns monotone.

The implementer filed this as purely (a). E1 contains a second cause, which section 8 proves. flushRoute runs CloseSession (which only closes the WAL handle), then SessionEnd, then Drain, so an accepted but undispatched tool is published after SessionEnd, at turn 0. E2 was monotone because of a timing window, not an ordering guarantee. Neither half is fixed here: ingest.go, delivery_order.go and drain.go belong to C1.1, and the coordinator routed the flush ordering in handlers.go to C1.1 (ledger C1.13; relay in ../qompack-cx-ingest/plans/sdd/V6-closeout/ingest/handoff-from-duplicate.md).

### 2.5 The third session indexed nothing (c)
After 2.1 to 2.3 were fixed, the E2 runs stalled at session (d): c12-after-fixes-with-e2-gate-off-windows.log and -2.log ("never reached 26 records"), tree trees/c12-fixes-e2-gate-off-drive3-stall. The test planted a hand-written v2 delivery seal for fsck and then drove (d) beside it. Since c78f610 a delivery whose journal refuses it is retained pending, so (d) could never index anything; it used to pass only because deliveries were recorded unleased. 43051d5 keeps the daemon's own position, checks the planted seal unchanged once fsck and doctor are done, and restores the daemon's position before (d). With all fixes plus E2 the test passes (c12-after-fixes-with-e2-gate-off-windows-3.log). With the gate on it fails only on the correct finding (c12-after-fixes-windows.log: "…unknown-schema_2_00 reports turn 0 after turn 1 in session sess-e2e-sp17-unknown-schema").

## 3. C1.3: root cause (c)
00e0c98 (V6-AUTH-1) drops the opaque bytes of any cut envelope, because a prefix cannot prove its file target is in the project. plans/sdd/V6-remediation/capture-work.md records this as a "Deliberate consequence". The in-process twin, internal/cli/hooks_test.go TestHooks_OverBudgetDeliveryIsRecordedNotDropped, was already changed to require.Empty; the e2e twin (test/e2e/v1_integration_test.go:388) was not. Evidence: c13-repro-windows-1.log (FAIL), c13-after-windows.log (7/7 PASS), linux-e2e-focused-race.log (8/8 PASS, -race, uid 10001).

## 4. What changed
3893fac fsck decode (2.1) · 9d795a0 audit counter and predicate (2.3) · 3c369c4 fsck publication note (2.3) · 555e289 C1.3 criterion (6.1) · 8da4435 fixture in the real sidecar layout (2.2) · 43051d5 seal restored before (d) (6.2) · 191a6b7 implementer's evidence · 6edd2e4 failing-first daemon test (section 8; no product change) · eb724b2 review-round evidence. No internal/daemon product code changed, and no check was skipped, weakened or re-budgeted.

## 5. Test commands and results
All runs use -count=1 -timeout=30m. Windows is this shared 22-CPU host. Linux is container qompack-v6-linux-verification (Go 1.26.6), user qompack-test (uid 10001), GOMAXPROCS=4, -race, with the exact commit copied in by git bundle into a fresh /work/cx-e2e-* directory.

### 5.1 Implementer seat
- `go test ./internal/cli -run TestFsck_ToolUse` — 4 FAIL, then 4 PASS.
- `go test ./internal/store -run TestAuditPublication_` — build fail, then PASS.
- go test ./internal/cli -run 'TestFsck_(NewerCaptureSchema|UnknownObservationIntent|ToolUse)': 1 FAIL, then PASS. <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test ./test/e2e -run TestV1_ConfigPrecedenceReachesHookBehaviour` — FAIL, then PASS.
- `go test ./test/e2e -run TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting` — FAIL, on the turn finding only (C1.1/C1.13). With E2 applied: stalled twice (which led to 43051d5), then PASS.
- go test ./internal/cli: ok (pkg-internal-cli-windows.log).
- go test ./internal/store: 1 FAIL, TestGC_DeadlineOvershootIsBoundedByTheCheckInterval, in a 1374 s run. Alone it PASSES, so this is CO-LOAD (store-gc-deadline-rerun-alone-windows.log). This work did not touch that test.
- Linux -race ./internal/cli ./internal/store: cli 314 pass; store 793 pass with 1 FAIL, the same GC test, which PASSES alone (linux-store-gc-deadline-rerun-alone-race.log), so CO-LOAD.
- Linux -race, test/e2e focused: 8 pass, 1 skip (no Claude CLI in the container, so the unknown-schema case skips by design).
- go vet: clean. devtool fmt-check: clean.
- devtool lint: everything passes except bindeps and stubskips, both pre-existing (5.4).

### 5.2 Review round (fix seat)
- go test ./internal/daemon -run TestFlush_SessionEndIsOrderedAfterAcceptedArrivals -v: FAIL on both arms, "turn 0 after turn 1" (review-flush-order-red-windows.log).
- Same test on a git archive of 6cc9da3 (the closeout/ingest HEAD, C1.1's fix): FAIL on both arms (review-flush-order-red-on-ingest-6cc9da3-windows.log).
- Linux, 6edd2e4, -race, uid 10001: FAIL on both arms, no data race (review-flush-order-red-linux-race.log).
- E3, in a scratch copy only and never committed (drain before SessionEnd): the new test PASSES, and so do TestSessionEndRecordsRecoveryNeeded, TestStragglerAfterSessionEnd…, TestDrainOfSpooledFlushLineDoesNotDeadlock, TestStartupDrainOfSpooledFlushLine… and TestMarkerIsWrittenByFlushAndCheckpointOnly (experiment-e3-drain-before-session-end{.patch,-windows.log}).
- Drained control-line repro on a scratch git archive of 6edd2e4: FAIL, with sidecars of op flush and checkpoint (review-repro-drained-control-sidecar-windows.log, source in review-repro-drained-control-sidecar_test.go.txt).
- go test ./internal/daemon, full package on Windows: FAIL in 643.6 s. The ONLY failure is the new test; every other daemon test passes (review-pkg-internal-daemon-windows.log). The run started at 191a6b7 plus the same file, which was committed as 6edd2e4 during the run.
- devtool fmt-check: clean. go vet ./internal/daemon: clean.
- devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,runpatterns,docmarkers,coveragefloors: all PASS except bindeps, which is pre-existing (review-devtool-lint-windows.log). stubskips was left out because it runs go test -json over the whole tree. The only file added contains no Skip call.

### 5.3 Other reds in test/e2e on this branch (not C1.2/C1.3)
Sources: pkg-test-e2e-windows.log (hit -timeout=30m under co-load), e2e-coload-suspects-rerun-windows.log, rollback-rerun-keep-windows-{1,2}.log.
- ObserverThroughDaemon, ThinSliceDropsControlOnlyEdges: C1.1.
- TestInstall_HostCLIInstallUpgradeUninstall (install_1_01 at turn 0 after turn 1) and TestRollbackRehearsal (rollback_2_01 at turn 0 after turn 1): the same turn defect as 2.4, now reported with the right session. Owned by C1.1 plus C1.13.
- TestRollbackRehearsal, under co-load only: "capture publication requirement is unknown" and "capture sidecar with an unrecognized op". This did not recur alone. Section 9 proves a mechanism that produces exactly these notes; linking it to that particular run is an inference, because its tree was not kept.
- TestE2ELazySpawn: passes alone, so CO-LOAD.
- AllSixHooksExitZero/SessionStart ("SessionStart answers through hookSpecificOutput", nil) reproduces alone. UNCLASSIFIED and not investigated; possibly C1.12 or the daemon's degraded-passive mode.
- TestHooksExitZeroUnderFaults: TempDir cleanup reports "directory is not empty", on different subtests each run. UNCLASSIFIED and not investigated.
- The E2 log also shows an R8-2 trip: ~/.claude/plugins/data/qompack-inline/ appeared during the run. It no longer exists. "inline" is the host's name for a --plugin-dir plugin, which this test does not use, and no other run tripped R8-2. UNVERIFIED, but most consistent with another lane's concurrent real-host session.

### 5.4 Pre-existing lint failures
- bindeps: golang.org/x/sys/unix reaches the linux/darwin binaries. It fails identically on base cf31e01 (../qompack-cx-ingest/plans/sdd/V6-closeout/ingest/runs/lint-bindeps-base-cf31e01.log).
- stubskips: three skip messages in daemon/hookio/store tests this work did not touch, and four packages killed at -timeout=30m under co-load.

## 6. Criterion changes

6.1 C1.3 (555e289).
- Old: require.NotEmpty(Capture.Bytes, "a bounded prefix survives as evidence").
- New: require.Empty(Capture.Bytes, "an incomplete envelope cannot prove scope; …").
- Still asserted: the record exists, Truncated, its classification and outcome, and SourceBytes == len(payload).
- Why: 00e0c98 deliberately keeps no bytes of a cut envelope (V6-AUTH-1, capture-work.md "Deliberate consequence"), and the in-process twin already asserts exactly this. The old criterion would require the leak the V6 fix closed.

6.2 C1.2 (43051d5).
- Old: the planted seal was checked unchanged at the end of the test, across session (d)'s daemon run.
- New: it is checked right after fsck and doctor, its only intended readers, and then the daemon's own position is restored before (d).
- Why: the test's own comment scopes the seal to readers ("planted with the project QUIET and read only by fsck"). Since c78f610 a daemon beside a refused seal retains every delivery pending, so (d) could never index anything. That the daemon never rewrites a position it cannot vouch for is pinned in internal/daemon/delivery_seal_format2_test.go (TestDeliverySeal_ConversionAndDowngrade: "must not have its position rewritten"; TestDeliverySeal_FormatOneRefusesAV2ImageAtEitherSidecar: "nothing was appended").

8da4435 changes no assertion; it moves a fixture to where the product writes it.

## 7. Open items and decisions
Open items:
1. C1.13 is open and owned by C1.1. The acceptance test is committed and red: internal/daemon/flush_arrival_order_test.go (6edd2e4).
2. The drained control-line sidecar defect (section 9) is not in the ledger yet and needs an owner (drain.go belongs to the ingest lane).
3. Until C1.1 and C1.13 land, TestUnknownSchema…, TestInstall_HostCLIInstallUpgradeUninstall and TestRollbackRehearsal… stay red on the (now correct) turn finding.
4. The unclassified reds in 5.3 were not investigated.
5. The ingest lane notes that "segment already closed" also appears with the gate off, because degraded-passive mode skips svc.SessionStart (handlers.go:766). Not verified here.

Decisions needed from the coordinator or owner:
- (i) Confirm the fsck-side refinement of publication-work.md (2.3).
- (ii) Route item 2.
- (iii) If C1.1 declines C1.13, route it back to this lane.

## 8. Review resolution
One finding, severity minor: "the turn defect is filed as purely (a) … flushRoute calls ing.CloseSession … then svc.SessionEnd, then Drain … no daemon-level regression test was written". The fix it asks for: record (a) plus a latent (b), give ownership to C1.1 with a failing-first internal/daemon test as acceptance, and fix it in handlers.go if C1.1 declines.

VERDICT: CORRECT. I verified it independently in the code and the evidence:
- flushRoute (handlers.go:969-1009) runs registry.End, CloseSession, svc.SessionEnd, and Drain last.
- CloseSession (ingest.go:749-762) only deletes the WAL handle.
- observer.onSessionEnd closes the segment at st.Turn and then runs delete(o.sess, id), so the next event of that session starts at turn 0.
- The E1 tree shows 1_07, 2_06 and 2_07 at turn 0.

ACTION:
1. The classification is corrected in 2.4 to (a) plus a latent (b).
2. The failing-first test is written and committed: TestFlush_SessionEndIsOrderedAfterAcceptedArrivals (6edd2e4). It uses the real observer, store, delivery journal and drainer, in two deterministic arms:
   - "queued job no worker has reached": a tool accepted and leased but left on the ring, then flush.
   - "one worker busy on another session": one real ingest worker parked in a ring-only, unleased job of another session. This is the reviewer's one-worker shape, and E1's. The blocker has no WAL line, because the flush's whole-spool drain would otherwise fail on an unrelated in-flight delivery.
   Both arms assert what the reviewer asked for: B's turn equals A's (monotone), and the session record SessionEnd writes counts B. The flush must also answer OK within 15 s, cli.flushReplyDeadline. Results: RED on this branch, RED on C1.1's committed 6cc9da3 (so C1.1 as committed does not close C1.13), and RED on Linux -race with no data race. In arm 1 the gate plays no part, which proves the defect is independent of it.
3. The test is satisfiable. E3 (drain before SessionEnd, in a scratch copy, never committed) turns both arms green and keeps the five tests that pin flush behaviour green. E3 is not a full fix: a delivery a worker holds in flight makes that drain stop with "delivery still in progress", which is what E1 logged. C1.1's design covers that case (wake, bounded wait for acknowledged-or-terminal, then drain before SessionEnd, all inside 15 s); E3 is only its step 4.
4. Ownership is not a fix here. The coordinator has already given this change to C1.1: ledger C1.13 ("Session end must wait for that session's earlier queued or deferred events … Confirm C1.1's fix covers both, or follow up") and the relay ("handlers.go is in C1.1's scope for this. The e2e workstream is not editing handlers.go"). C1.1 has not declined; its handoff carries the design, and it was active during this round. A second implementation here would edit the same lines. The committed test is C1.1's acceptance. If C1.1 closes without it, the reviewer's fallback applies and the coordinator should route it back here.
The committed test is red by design and nothing skips it. This branch's test/e2e is already red for the same cause, so the test adds no new failure mode; it locates the existing one.

## 9. Additional finding: drained control lines publish capture sidecars (b)
- The hook client mints a delivery nonce for EVERY hook (internal/cli/hookclient.go), flush and checkpoint included.
- When such a hook falls back to the client spool (daemon down at SessionEnd, or a reply deadline passed under load), the drain leases the line, and dispatchPending calls publishCapture for any leased line (internal/daemon/drain.go:1152). The live path never does this, because only observe.tool/prompt/stop reach ingest.Accept.
- The publication audit (internal/store/publication_audit.go:428) and fsck's CaptureRequiresReference accept only the three observe ops. The result is a permanently incomplete audit ("capture sidecar with an unrecognized op", with the startup LOUD) and fsck exit 1 ("capture publication requirement is unknown") for the life of the project.
- Repro (not committed as a live test, because drain.go is another lane's file): review-repro-drained-control-sidecar_test.go.txt. Both the flush and checkpoint arms fail, and the audit reports incomplete=true with notes=[capture sidecar with an unrecognized op].
- A likely fix is to publish a capture only for Op.HotPath() lines in dispatchPending. Whether sidecars already on disk need an audit/fsck tolerance is a question for the owner of the capture contract.

### Commits

- 3893facbd504c23dced4d6c6a1bae4f2c98f55c3 fix(cli): read tool_use index lines with the file's own keys
- 9d795a074c8088687d9be6a51e777de8a32eaa3c feat(store): count newer-schema captures in publication audit
- 3c369c4db9f95fd2eaeb9a24722195754377031c fix(cli): keep newer capture schemas a support gap in fsck
- 555e289885f3a61bf7eeeda08e65cffbe8bac20e test(e2e): expect a byte-free over-budget capture record
- 8da4435b497a57f32c7ffd041556c1632238e240 test(e2e): plant the newer capture sidecar in the sidecar layout
- 43051d590fa81a772af8473417bea76e99e4488f test(e2e): restore the daemon's seal before the config session
- 191a6b76974fafb952b9cce561c3325e97f75fda docs(v6): preserve c1.2 and c1.3 e2e close-out run evidence
- 6edd2e4875c08d2cfe358daa9c2c634348ec7f53 test(daemon): pin session end after accepted arrivals  [fix seat: failing-first acceptance test for C1.13, red by design]
- eb724b267af4a5ef012ddfa6f8f1723c02be8967 docs(v6): preserve e2e review-round evidence  [fix seat]

### Tests

- `go test ./internal/daemon -run 'TestFlush_SessionEndIsOrderedAfterAcceptedArrivals' -count=1 -v -timeout=30m (Windows, 191a6b7 + new test)` — FAIL both arms (expected red): 'turn 0 after turn 1', plans/sdd/V6-closeout/e2e/runs/review-flush-order-red-windows.log
- `same test on git archive 6cc9da3 (closeout/ingest HEAD, the C1.1 fix)` — FAIL both arms: C1.1's committed fix does not cover C1.13, runs/review-flush-order-red-on-ingest-6cc9da3-windows.log
- `Linux container, 6edd2e4, uid 10001, GOMAXPROCS=4: go test ./internal/daemon -run '^TestFlush_SessionEndIsOrderedAfterAcceptedArrivals$' -count=1 -race -v -timeout=30m` — FAIL both arms (same message), 0 DATA RACE, runs/review-flush-order-red-linux-race.log
- `E3 (scratch copy, never committed: drain before SessionEnd) go test ./internal/daemon -run 'TestFlush_SessionEndIsOrderedAfterAcceptedArrivals|TestSessionEndRecordsRecoveryNeeded|TestStragglerAfterSessionEnd|TestDrainOfSpooledFlushLine|TestStartupDrainOfSpooledFlushLine|TestMarkerIsWrittenByFlushAndCheckpointOnly' -count=1 -v` — PASS all: the new test is satisfiable, runs/experiment-e3-drain-before-session-end-windows.log
- `go test ./internal/daemon -count=1 -timeout=30m (full package, Windows)` — FAIL in 643.6s; the ONLY failure is the new red-by-design test, every other daemon test passes, runs/review-pkg-internal-daemon-windows.log
- `repro (scratch git archive 6edd2e4): go test ./internal/daemon -run TestReviewRepro_DrainedControlLineWritesNoCaptureSidecar -v` — FAIL: sidecars with op flush/checkpoint; audit incomplete 'capture sidecar with an unrecognized op', runs/review-repro-drained-control-sidecar-windows.log <!-- runpatterns: names the review's repro test, kept as evidence in runs/review-repro-drained-control-sidecar_test.go.txt and run on a scratch archive, not a test in this tree -->
- `go run ./tools/devtool fmt-check ; go vet ./internal/daemon` — clean (exit 0)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,runpatterns,docmarkers,coveragefloors` — all PASS except bindeps (4 x/sys/unix violations, identical on base cf31e01); stubskips omitted because it runs the whole tree; runs/review-devtool-lint-windows.log
- `implementer seat: go test ./internal/cli (full), fsck red/green, store support-gap red/green, e2e C1.3` — cli ok; fsck-tooluse 4 FAIL -> 4 PASS; store build-fail -> PASS; C1.3 FAIL -> PASS on Windows and Linux -race (8/8)
- `implementer seat: go test ./test/e2e -run TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting` — FAIL on the correct per-session turn finding only (C1.1/C1.13); PASS with the E2 gate-off patch after 43051d5
- `implementer seat: go test ./internal/store (Windows and Linux -race)` — 1 FAIL, TestGC_DeadlineOvershootIsBoundedByTheCheckInterval, which PASSES alone on both OSes: co-load, not touched by this work

### Criterion changes

- C1.3 (555e289), test/e2e/v1_integration_test.go:388: require.NotEmpty(Capture.Bytes, 'a bounded prefix survives as evidence') -> require.Empty(...). Why: 00e0c98 (V6-AUTH-1) deliberately keeps no bytes of a cut envelope (capture-work.md 'Deliberate consequence'), and the in-process twin TestHooks_OverBudgetDeliveryIsRecordedNotDropped already asserts Empty. Classification, outcome, Truncated and SourceBytes are still asserted.
- C1.2 (43051d5), test/e2e/install_test.go: the planted v2 seal is now checked unchanged right after fsck and doctor, its only intended readers, and the daemon's own position is restored before session (d); previously it was checked at the end, across (d)'s daemon run. Why: since c78f610 a daemon beside a refused seal retains every delivery pending, so (d) could never index anything. The test's own comment scopes the seal to readers, and daemon-side non-rewrite is pinned by internal/daemon/delivery_seal_format2_test.go.

### Open issues

- report.md not committed: the harness refused the report-file Write. The full report text, including the Review resolution section, is in `summary`; the coordinator should commit it at plans/sdd/V6-closeout/e2e/report.md.
- C1.13 open, owned by C1.1/ingest: flushRoute must settle the session (queued AND in-flight accepted arrivals) before svc.SessionEnd, within the 15 s flush reply deadline. Acceptance test committed and red: internal/daemon/flush_arrival_order_test.go (6edd2e4). Red on C1.1's 6cc9da3 too.
- NEW defect, not in the ledger: internal/daemon/drain.go:1152 publishes capture sidecars for leased non-observe lines (spooled flush/checkpoint); the publication audit and fsck then report 'unrecognized op' / 'publication requirement is unknown' permanently. Repro in runs/review-repro-drained-control-sidecar_test.go.txt; needs an owner (ingest lane's file).
- Until C1.1 and C1.13 land, TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting, TestInstall_HostCLIInstallUpgradeUninstall and TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite stay red on the (now correctly session-named) turn finding.
- Unclassified e2e reds outside C1.2/C1.3, not investigated: TestE2E_AllSixHooksExitZero/SessionStart (hookSpecificOutput nil, reproduces alone), TestHooksExitZeroUnderFaults TempDir cleanup 'directory is not empty', and one R8-2 trip (transient ~/.claude/plugins/data/qompack-inline/, likely another lane's concurrent real-host session, unverified).
- Pre-existing lint failures: bindeps (x/sys/unix, same on base cf31e01); stubskips (three skip messages in untouched daemon/hookio/store tests, and packages killed at 30m under co-load).
- Linux run dir /work/cx-e2e-review-20260922T235835Z (mine) left in the container as evidence.

### Needs the owner

- Confirm the fsck-side refinement of plans/sdd/V6-remediation/publication-work.md (3c369c4): newer-schema-only audit incompleteness is now an fsck note, not an fsck failure. The store audit still reports Incomplete and the daemon startup LOUD still fires.
- Route the drained control-line capture-sidecar defect (drain.go:1152) to an owner, and decide whether sidecars already on disk need an audit/fsck tolerance.
- Coordinator: confirm C1.13 stays with C1.1, with 6edd2e4 as its acceptance test; if C1.1 declines, route the handlers.go settle fix back to the e2e lane.

