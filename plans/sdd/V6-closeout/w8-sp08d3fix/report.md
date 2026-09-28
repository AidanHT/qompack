# w8-sp08d3fix: SP08-D3 fixed — host-order spool replay and an honest substitution notice (D35)

Branch `closeout/w8-sp08d3fix`. Workflow `wf_50e94c35-219`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `31eabfaa`

### Root cause

SP08-D3 had two causes. (a) Wrong spool order: ipc.SpoolFiles sorts client-<pid>.ndjson by file name, and the drain replayed the client spools in that order. A pid says nothing about when its hook ran, and "client-10" sorts before "client-9". A client-spool line gets its lease when a drain reaches it, and the turn is assigned at publication. So under HotSpool or runtime.daemon.enabled=false, the host's later prompt could become prompt_<s>_0. (b) A race: a prompt that sat only in a hook's spool could be overtaken by a later prompt that arrived live and was published first. Nothing recorded that this happened. readL0Intent checked only the record's session and turn, so the rehydrator presented the later prompt as the verbatim original request without saying so. D35 fixes (a) by host-order replay. It rules (b) out of the guarantee and requires the product to report it honestly instead.

### Summary

SP08-D3 is resolved per D35, and the row is now `fixed`. I resumed from the earlier seat's eight commits (17a42f6..ed4f8d4), which nobody had reviewed. I read every diff, kept all eight unchanged, re-ran every check at ed4f8d4, and added one commit with the evidence logs (31eabfa).

WHAT CHANGED
1. Host-order replay (internal/daemon/drain_host_order.go, drain.go). Each drain pass (Drain and the spool watcher's DrainClientSpools) now calls orderClientSpoolsByHostTS. It orders the client-<pid>.ndjson files by the req.TS of each file's first record. The file name only breaks a tie.
   - The timestamp is read from the start of the first record, capped at ipc.MaxLineBytes.
   - A file whose first record has no readable or positive stamp (half-written or corrupt) sorts after every stamped file, by name.
   - WAL segments keep their place ahead of the client files, and each file is still read front to back.
   - The ipc.SpoolFiles comment is corrected. The new read is registered in the productreads guard (TestGuard_EveryProductReadIsClassified had caught it).
2. Host stamps on prompt records. runIngested passes req.TS into the capture (observer.WithHostTS), and recordPromptDurable stores it as the prompt record's TS. The live worker and drain replay both go through runIngested, so every daemon-captured prompt carries its host stamp. An in-process caller with no stamp keeps the observer clock. The DAG node and the session features still use the observer clock.
3. Capture-time notice (observer notePromptHostOrder). When a prompt lands at turn k behind an earlier-published turn with a later host stamp, a new counter observer.prompt_out_of_host_order is incremented. A Warn names the capture (id) and the lowest overtaken turn (substituted_id). Turns are never renumbered.
4. Rehydrator notice (rehydrate hostOrderNotice). This uses a new optional store capability, store.PromptOrder / FSStore.EarliestPrompt. It is one scan of the index, bounded by the existing PromptFrontier limit (1<<18, now the shared constant promptScanLimit, not a new number).
   - When a later turn carries an earlier host stamp than prompt_<s>_0, item 2 still injects prompt_<s>_0 verbatim.
   - It also adds a Warn and a drop entry of kind user_intent_source, ID host_order. The entry names both records and gives expand(tool_use_id=<host-first>) as the way to restore.
   - A store that cannot answer is logged and no substitution is claimed. The production daemon's store is a plain *FSStore, so the capability is present.
5. Docs.
   - docs/architecture.md §7: a new paragraph on what "the verbatim original intent" guarantees.
   - docs/cannot-do.md: a new entry, "The first captured prompt is not always the first prompt the host sent".
   - docs/user-guide.md: the §8.5 provenance wording is corrected to the shipped guarantee.
   - test/e2e/v5_x01_test.go: the stale X1 comments now describe the V6 capture path; a WAL or spool replay of observe.prompt now runs the full capture, not only the sentinel scan.
6. Carried-defects bookkeeping. Row SP08-D3 in plans/CARRIED-DEFECTS.tsv is now `fixed`. Evidence is TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero, and the summary rewrite follows the SP08-D2 pattern (what is fixed, then the residual). plans/V2-SP-08-carried-defects.md has a close-out note citing D35, listing what shipped and the residuals.

EVIDENCE TEST (inverted). The two prompts now carry distinct host stamps (second.TS = first.TS + 1, require.Less).
- spool_file_order: client-9, stamped first, becomes turn 0 ahead of client-10. Both records carry their host stamps, the counter stays at 0, and there is no host_order entry.
- live_second: turn 0 stays the live prompt and nothing is renumbered. The counter reads 1, and a real rehydrate.Build over the daemon's store emits exactly one host_order entry naming prompt_<s>_0 and prompt_<s>_1.
- The name is historical. It was kept because committed reports quote it in -run patterns.

FAILING FIRST (negative controls, each a temporary edit reverted with git checkout; runs/win-negative-controls.log). Each one turns the evidence test red:
- NC1, drain without orderClientSpoolsByHostTS: spool_file_order fails (expected "first", got "second").
- NC2, capture without WithHostTS: both subtests fail on the host-stamp assertion.
- NC3, rehydrator without the capability: live_second fails ("[]" should have 1 item).

CHECKS at ed4f8d4, all passing:
- `go run ./tools/devtool fmt-check`.
- `go vet` on daemon, observer, rehydrate, store, ipc, test/e2e and test/guards, on Windows and GOOS=linux.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`.
- No generated-docs inputs changed (config, commands and MCP are untouched), so no gen-*-docs --check was needed.

Evidence logs are committed under plans/sdd/V6-closeout/w8-sp08d3fix/runs/. The Linux artifacts are in runs/cx-w8-sp08d3fix-sp08d3fix-final-ed4f8d4-20260928T132004Z-artifacts, with test.jsonl gzipped.

### Commits

- da4b05e1 feat(store): add the EarliestPrompt prompt-order capability
- ad298a07 fix(observer): stamp prompts with host time and note late captures
- 6d013ef3 fix(rehydrate): name a host-earlier later turn as a substitution
- 76b1d94b fix(daemon): replay client spools in host order
- 3881a1ab test(e2e): correct the stale X1 prompt-capture comments
- 0873e7e6 docs: state the shipped first-prompt guarantee
- 42f01d63 docs(plans): move SP08-D3 to fixed under owner decision D35
- ed4f8d45 test(guards): classify the drain's first-record timestamp read
- 31eabfaa test(sp08d3): record the SP08-D3 close-out verification runs

### Tests

- `go test -count=1 -v -run '^TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero$' ./internal/daemon/ (Windows, at ed4f8d4)` — PASS (both subtests); runs/win-focus-sp08d3-evidence.log
- `go test -count=1 -v -run '^TestDrainOrder_ClientSpoolsReplayByFirstRecordHostTS$' ./internal/daemon/` — PASS; runs/win-focus-drainorder.log
- `go test -count=1 -v -run '^TestPromptHostOrder_' ./internal/observer/` — PASS (TestPromptHostOrder_RecordCarriesTheHostTimestamp, TestPromptHostOrder_HostEarlierCaptureIsCountedAndNamed, TestPromptHostOrder_NamesTheEarliestOvertakenTurn); runs/win-focus-observer.log
- `go test -count=1 -v -run '^TestUserIntent_' ./internal/rehydrate/` — PASS (incl. TestUserIntent_HostEarlierLaterTurnIsNamed, TestUserIntent_HostOrderAgreementReportsNothing, TestUserIntent_HostOrderUnverifiableIsWarnedNotGuessed); runs/win-focus-rehydrate.log
- `go test -count=1 -v -run '^TestEarliestPrompt_PicksTheEarliestHostStampedPromptOfTheSession$' ./internal/store/` — PASS; runs/win-focus-store.log
- `Negative controls NC1/NC2/NC3 (temporary diagnostic edits, reverted), each followed by go test -count=1 -run '^TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero$' ./internal/daemon/` — FAIL as intended in all three; worktree clean after revert; runs/win-negative-controls.log
- `go test -count=1 -timeout=30m ./internal/daemon/ (Windows)` — ok 406.9s
- `go test -count=1 -timeout=30m ./internal/observer/ (Windows)` — ok 132.6s
- `go test -count=1 -timeout=30m ./internal/rehydrate/ (Windows)` — ok 2.4s
- `go test -count=1 -timeout=30m ./internal/ipc/ (Windows)` — ok 20.4s
- `go test -count=1 -timeout=30m ./internal/store/ (Windows, co-loaded)` — FAIL only TestGC_DeadlineOvershootIsBoundedByTheCheckInterval, a known wall-clock row (calibration 623ms-2.27s under load). Re-run alone: go test -count=1 -v -run '^TestGC_DeadlineOvershootIsBoundedByTheCheckInterval$' ./internal/store/ -> PASS. It also passes on Linux. Unrelated to this change, which adds only EarliestPrompt to store
- `go test -count=1 -timeout=30m ./test/docs/ (Windows)` — ok 3.1s
- `go test -count=1 -timeout=30m ./test/guards/ (Windows)` — FAIL only TestCarriedDefects_WaveReportRequiresResolution, for SP06-D2, SP08-D1, SP09-D1, SP10-D1 and SP20-D2. This is the known O4 red caused by plans/V6-report.md existing. SP08-D3 is no longer in that list
- `go test -count=1 -v -run '^TestCarriedDefects_ManifestIsWellFormed$' ./test/guards/` — PASS incl. /SP08-D3
- `go test -count=1 -v -run '^TestCarriedDefects_OpenRowsHaveLivingEvidence$' ./test/guards/` — PASS (the fixed SP08-D3 row is no longer iterated)
- `go test -count=1 -timeout=40m ./test/e2e/ (Windows, full package; covers every prompt capture/replay row incl. TestV5_ObserveToStatusRoundTrip, TestE2E_VerbatimPromptSurvivesRestart, TestE2E_ObserverThroughDaemon, TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently)` — FAIL only TestV3_HotPathUnchangedWithLedgerResident. Re-run alone: go test -count=1 -v -timeout=30m -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e/ -> FAIL the same way: 593 of 2130 deliveries deferred to the client spool on the client side before any drain. packaging/report.md recorded the identical 593/2130 on base cf31e01, so this red predates the change. Every other e2e row passed
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w8-sp08d3fix --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w8-sp08d3fix --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w8-sp08d3fix/plans/sdd/V6-closeout/w8-sp08d3fix/runs ed4f8d45 sp08d3fix-final --timeout 30m -- ./internal/daemon ./internal/observer ./internal/rehydrate ./internal/store ./internal/ipc ./test/guards` — Linux, non-root (uid 10001), -race: daemon 1543 pass, observer 564, rehydrate 201, store 873, ipc 152, no data races. test/guards: only the same O4 WaveReportRequiresResolution red (5 rows, not SP08-D3). SP08-D3 evidence subtests and ManifestIsWellFormed/SP08-D3 pass
- `go run ./tools/devtool fmt-check; go vet (Windows and GOOS=linux) on the touched packages; go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all exit 0

### Criterion changes

- Evidence test inverted, following the TSV header: TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero used to assert the wrong outcome (the host's second prompt at turn 0 with no notice). It now asserts D35's guarantee. spool_file_order expects host order with no flag. live_second expects turn 0 unchanged, host stamps on both records, the out-of-host-order counter at 1, and a host_order drop entry naming both ids. The two prompts carry distinct req.TS values (Less, not LessOrEqual). This is stricter than before; nothing was loosened, skipped or deleted.
- Row SP08-D3 status changed from deferred:V6-VERIFY to fixed, with the detail-doc close-out note citing D35. The evidence column keeps the same, now inverted, test.
- No threshold, budget, timeout or golden was changed. promptScanLimit is the existing 1<<18 bound of PromptFrontier, only given a name and shared.

### Open issues

- Residual that D35 accepted: the live-vs-spool race. A prompt spooled because its hook could not reach the daemon can be overtaken by a later live prompt if that prompt arrives before the next client-spool pass, about two spoolCheckIntervals (2 s each). The overtaken prompt lands at a later turn; the product now counts it, warns, and names it in the rehydration rather than renumbering.
- The rehydrator's host-order check runs only when item 2 injects the L0 record. When it falls back to the checkpoint copy, the existing user_intent_source/l0 entry already says so. Checkpoint seeding does not record which turn the original came from; D35 did not require it.
- Within one client spool file, records keep file order, as D35 specifies. This matters only for a hook process that spooled more than one record.
- Behaviour change for reviewers: a daemon-captured prompt record's TS is now the host send time (req.TS), not the capture time. Readers of ToolUseRecord.TS see host time for prompts: session Start/End in recomputeSessions, store search since-filter and ordering, and MCP RecordedAt for prompt hits. For a replayed spool this is earlier, and more accurate, than before. No reader was found that relies on the capture time; tool records are unchanged.
- The drain now opens each client spool once per pass to read its first record's timestamp, capped at MaxLineBytes, before draining. The cost is proportional to the number of files, like the pass itself. It is off the hot path.
- Known reds, not caused by this change: TestCarriedDefects_WaveReportRequiresResolution for SP06-D2, SP08-D1, SP09-D1, SP10-D1 and SP20-D2 (O4, because V6-report.md exists), and TestV3_HotPathUnchangedWithLedgerResident, which also fails alone with the same 593/2130 deferral recorded on base cf31e01.

## Independent review

### review:sp08d3fix: needs-fixes

- **minor** `plans/V2-SP-08-carried-defects.md:616-617 (also docs/architecture.md:593-595, docs/cannot-do.md:279-281)` — The residual says a multi-record client spool 'matters only for a hook that spooled more than one record'. That is wrong. The spool file is named only by pid (internal/ipc/spool.go:98, client-<pid>.ndjson, opened with paths.AppendOnly), and a file stays on disk until a drain reaches Done. So when a later hook process reuses the pid, it appends to the older hook's file. Windows recycles pids aggressively, and in runtime.daemon.enabled=false mode spools pile up with no drain. There, multi-record files that mix far-apart host times are the normal case, not a corner case. Turn 0 is still correct: each file's first record is its earliest, so the global minimum is drained first. Later turns can land out of host order, though, and each one fires observer.prompt_out_of_host_order plus the Warn. The docs present that counter as the signal of the live-vs-spool race. An operator seeing it in daemon-disabled mode would be told the wrong cause. The same applies across passes: a file partly consumed in an earlier pass is ordered by its already-consumed first record.
  - Evidence: internal/ipc/spool.go:98 `fmt.Sprintf("%s%d%s", spoolFilePrefix, os.Getpid(), spoolFileExt)`, and writeLocked opens it with paths.AppendOnly. drain_host_order.go:96 reads the first record of the file at byte 0, whatever the file's consumed Offset. Detail doc line 616-617: 'One hook process writes one file, so this matters only for a hook that spooled more than one record.' Example: client-9=[A t=100, C t=300 (pid reused)] and client-10=[B t=200] replays as A, C, B. B is then captured at turn 2 behind turn 1 and counted and warned as out of host order, although no live prompt was involved.
  - Fix: Correct the residual bullet in the detail doc. It should say that pid reuse makes a client-<pid> file hold records from several hook processes, so later turns (never turn 0) can be replayed out of host order. It should also say that the counter and Warn then fire without the live race. Add one sentence to the counter's description in cannot-do.md and architecture.md: it counts any capture behind a later-stamped turn, with the live-vs-spool race and pid-reused spool files as the two known sources. Optional code hardening that stays within D35: after loadState, order each client file by the req.TS of its first UNCONSUMED record, at st[base].Offset, rather than byte 0. This fixes the cross-pass case at no extra cost.
- **nit** `internal/observer/prompt_delivery.go:79-82; plans/V2-SP-08-carried-defects.md:580` — The Warn is documented as naming 'the lowest such turn', meaning the lowest overtaken turn. The walk stops at the first earlier prompt stamped no later than the capture. If the session already holds an inversion below that point, a lower overtaken turn exists and is not named. The detail doc states the stronger claim without the qualifier.
  - Evidence: Turns stamped [3000, 1000, 4000] and a capture at 2000: the walk sets substituted=2, then breaks at turn 1 (1000 <= 2000). Turn 0 (3000 > 2000) was also overtaken and is the lowest, but the Warn names turn 2. The code comment says 'the lowest turn the walk found' but the detail doc says 'the lowest such turn'.
  - Fix: Reword detail doc line 580, and the matching docs wording, to 'naming the nearest run of overtaken turns'. Alternatively, walk all prior turns (bounded by turn) without breaking, if the lowest overtaken turn is really wanted.
- **nit** `docs/uat.md:596-597` — The user-guide's provenance wording was corrected to 'the verbatim first captured prompt', but the identical claim in the UAT guide was not. It still says the original is 'resolved by derived id from the verbatim first prompt'. D35 asks for the §8.5 provenance claim to match the shipped guarantee wherever the docs make it.
  - Evidence: docs/uat.md:597: 'the original is resolved by derived id from the verbatim first prompt rather'. The corrected docs/user-guide.md:407 now reads 'from the verbatim first captured prompt'.
  - Fix: Make the same one-word change ('first captured prompt') in docs/uat.md:597, optionally with the same cannot-do.md anchor link, then re-run go test ./test/docs/.
- **nit** `test/e2e/v5_x01_test.go:59-62` — The comment directly above the corrected X1 block still carries the pre-V6 premise. It says that, unlike a tool or stop, a prompt is not replayed by Drain until its record lands. Since the V6 SP08-D3 fix, a leased prompt whose capture fails keeps its WAL line and is replayed. The rewritten x1v5PromptPutErrCounter comment now says so a few lines below, so the two comments contradict each other.
  - Evidence: Line 60-62: 'a tool or stop delivery is NAKed ... and is replayed by Drain until its record lands. A prompt is not — see x1v5PromptPutErrCounter.' The new comment at lines 71-76 says 'a WAL or client-spool replay of an observe.prompt runs that same capture, so a later Drain does recover a prompt the worker did not capture ... a leased delivery is then left unacknowledged for a later replay, and an unleased one is soft-dropped for good'.
  - Fix: Reword lines 60-62 to say that a prompt is always ACKed rather than NAKed, and that only an unleased prompt whose capture fails is lost for good. Comment change only.
- **nit** `plans/V2-SP-08-carried-defects.md:559-561 vs docs/architecture.md:584, docs/cannot-do.md:275, commit subjects/bodies` — D35's authority is labelled inconsistently. The detail doc calls it 'a coordinator decision under D33'. Its own heading, the public docs and every commit body call it an 'owner decision D35'. The task brief also calls it a coordinator decision. User-facing docs should not overstate who ruled the guarantee.
  - Evidence: V2-SP-08-carried-defects.md:559 '(... owner decision D35)' and :560-561 'D35, a coordinator decision under D33'. cannot-do.md: 'The V6 close-out's owner decision D35 ruled this race out'.
  - Fix: Pick one label and use it everywhere: 'coordinator decision D35 (under D33)' if that is its standing, or cite the owner ruling that makes it an owner decision. Update the detail-doc heading, docs/architecture.md, docs/cannot-do.md and the code comments to match.

## Fix seat (review resolution) — status `done`, head `9814530d`

### Root cause

The reviewer's finding holds, and it understates the problem. ipc names a hook's client spool `client-<pid>.ndjson` (`internal/ipc/spool.go:98`) and opens it with `paths.AppendOnly`. The file stays until a drain reaches Done (`shouldDelete` always allows a client file, but only once it is Done). So a later hook that reuses the pid appends to an earlier hook's file, and one file then holds records from several hook processes. D35's premise, "one file per hook process", does not hold under pid reuse.

Two separate consequences:

(a) Across passes. `orderClientSpoolsByHostTS` ran before `loadState` and read each file's record at byte 0. A partly consumed file was therefore placed by a record an earlier pass had already consumed. That was a real ordering bug, and it was fixable within D35.

(b) Within one pass. File order keeps a pid-reused file's records in file order, so a host-later prompt appended to it can be replayed ahead of another file's host-earlier prompt. The reviewer says turn 0 is still correct there. That is wrong whenever the file's first record belongs to another session or is not a prompt. Example: `client-9 = [session S1 prompt t=100, session S2 prompt "second" t=300]`, `client-10 = [S2 "first" t=200]`. S2's `prompt_<s>_0` becomes "second". So the docs' claim that turn 0 is the host's first prompt under HotSpool and daemon-disabled mode was false under pid reuse.

The honesty mechanism from D35 part 2 does fire in case (b): the counter, the Warn and the rehydrator's `host_order` entry. A test now pins this.

### Summary

FIX-SEAT REPORT, SP08-D3 (D35), branch closeout/w8-sp08d3fix. The reviewer's one finding is confirmed. I fixed the part D35 allows in code, pinned the rest with a test, and corrected the docs.

## Review resolution
- Finding (minor): "a multi-record client spool 'matters only for a hook that spooled more than one record' is wrong under pid reuse; the counter is presented as the live-race signal only". **CONFIRMED, fixed.**
  - Evidence: `spool.go:98` names the file by pid and `writeLocked` opens it with `paths.AppendOnly`. In daemon-disabled mode files stay on disk because nothing drains them.
  - Correction to the reviewer's analysis: "turn 0 is still correct" does not hold. When the reused file's first record is another session's, or is not a prompt, a later host prompt can take turn 0 (see root_cause (b)).
  - Code, the optional hardening, done as a failing test first: `orderClientSpoolsByHostTS(files, st)` now runs after `validateProgress`. It places each client file by the req.TS of the record at its validated consumed offset (`nextRecordHostTS`, which seeks to `st[base].Offset`; byte 0 when the file has no entry).
    - Red run against a signature stub: `TestDrainOrder_PartlyConsumedClientSpoolOrdersByNextRecord` got [11, 9, 10] instead of [11, 10, 9], and `TestSP08D3_PartlyConsumedReusedSpoolReplaysInHostOrder` put "second" at turn 0. Log: `runs/review-red-next-record-order.log`.
    - Both pass after the fix.
  - Residual pin: new `TestSP08D3_ReusedSpoolWithinOnePassIsNamed` (a characterization, passing before and after). Within one pass, a pid-reused file puts the host-second prompt at turn 0. `observer.prompt_out_of_host_order` then reads 1, and `rehydrate.Build` emits exactly one `user_intent_source`/`host_order` entry naming both ids, with no live prompt involved.
  - Docs, all rewritten to name two exceptions (the live-vs-spool race and pid-reused spool files) and to say the counter and notice count any capture behind a later-stamped turn:
    - the residual bullet in the `plans/V2-SP-08-carried-defects.md` close-out, plus "what shipped" item 1 and the evidence list
    - `docs/architecture.md` §7, the guarantee paragraph
    - `docs/cannot-do.md`: the Limit, Why and What-instead bullets. The watcher-window sentence now scopes the 2 s window to the live race, and says a file appended to after a drain began it is placed by its next record.
    - `docs/user-guide.md`: one sentence
    - the `plans/CARRIED-DEFECTS.tsv` SP08-D3 summary; it stays `fixed` with the same evidence test and 6 columns
  - Stale comments I also corrected: the `drain.go` Drain doc comment, the `ipc.SpoolFiles` doc comment, the `drain_host_order.go` "one file per hook process" wording, and the `test/guards/productreads_test.go` entry, renamed from `firstRecordHostTS` to `nextRecordHostTS` (same reason text; the pass still orders under dr.mu before reading or removing any spool file).
  - Not done, and why: a per-record merge across files, or a spool name unique per hook process. D35 specifies file order with record order kept within a file. Changing the name would also change what `spoolMaxBytes` bounds, since the per-file cap "resumes the running byte count across process restarts", and the name format is asserted in hotpath bench, e2e and testutil tests. The detail doc records this as the closing option.

## Files changed
- `internal/daemon/drain.go`, `drain_host_order.go`
- `drain_host_order_test.go` (new test)
- `drain_pid_reuse_test.go` (new: two daemon-level tests and helpers)
- `internal/ipc/spool.go` (comment only)
- `test/guards/productreads_test.go`
- `docs/architecture.md`, `docs/cannot-do.md`, `docs/user-guide.md`
- `plans/CARRIED-DEFECTS.tsv`, `plans/V2-SP-08-carried-defects.md`
- evidence under `plans/sdd/V6-closeout/w8-sp08d3fix/runs/review-*` and `cx-w8-sp08d3fix-sp08d3fix-review-7509f1f-20260928T143209Z-artifacts/`

## Tests (Windows unless noted)
- Focused `go test ./internal/daemon -count=1 -run` on:
  - `TestDrainOrder_ClientSpoolsReplayByFirstRecordHostTS`
  - `TestDrainOrder_PartlyConsumedClientSpoolOrdersByNextRecord`
  - `TestSP08D3_PartlyConsumedReusedSpoolReplaysInHostOrder`
  - `TestSP08D3_ReusedSpoolWithinOnePassIsNamed`
  - `TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero`
  - `TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero`

  All PASS.
- Full `./internal/daemon` ok (240 s), `./internal/ipc` ok, `./test/docs` ok.
- `./test/guards` full: FAIL only in `TestCarriedDefects_WaveReportRequiresResolution`, on subtests SP06-D2, SP08-D1, SP10-D1, SP09-D1 and SP20-D2. This is pre-existing and identical in the implementer's run; SP08-D3 is not among them.
- Focused guards: `TestCarriedDefects_ManifestIsWellFormed`, `TestCarriedDefects_OpenRowsHaveLivingEvidence` and `TestGuard_EveryProductReadIsClassified` all PASS.
- e2e prompt/spool rows, 13 tests including `TestE2ESpoolSubmodeEndToEnd`, `TestE2E_VerbatimPromptSurvivesRestart`, `TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently` and `TestV5_ObserveToStatusRoundTrip`: all PASS. I did not re-run full `./test/e2e`; the implementer's full run failed only the load-sensitive `TestV3_HotPathUnchangedWithLedgerResident` timing gate.
- `go run ./tools/devtool fmt-check`: exit 0. The lint subset: exit 0. `go vet` on daemon, ipc and guards (Windows and GOOS=linux): exit 0.
- Linux non-root -race gate on 7509f1f:
  - `internal/daemon` PASS (1546 pass, 0 fail, 1 skip; the new tests ran and passed)
  - `internal/ipc` PASS (152)
  - `test/guards` shows the same five pre-existing wave-report failures and nothing else

## Criterion changes
None. No assertion was loosened. One test was added as a characterization of a documented residual (`TestSP08D3_ReusedSpoolWithinOnePassIsNamed`).

## Background processes
None of mine are left running; the Linux gate and its monitor both ended.

### Commits

- 5fbad781 fix(daemon): place client spools by their next unconsumed record
- 7509f1fb docs: name hook pid reuse as a second host-order exception
- 9814530d test(sp08d3): record the fix-round verification runs

### Tests

- `go test ./internal/daemon -count=1 -run 'TestDrainOrder_|TestSP08D3_PartlyConsumedReusedSpoolReplaysInHostOrder|TestSP08D3_ReusedSpoolWithinOnePassIsNamed' (against signature stub, before fix)` — FAIL as intended: TestDrainOrder_PartlyConsumedClientSpoolOrdersByNextRecord and TestSP08D3_PartlyConsumedReusedSpoolReplaysInHostOrder red; runs/review-red-next-record-order.log
- `go test ./internal/daemon -count=1 -v -run 'TestDrainOrder_|TestSP08D3_PartlyConsumedReusedSpoolReplaysInHostOrder|TestSP08D3_ReusedSpoolWithinOnePassIsNamed|TestCarriedDefect_SP08D3_'` — PASS (6 tests)
- `go test ./internal/daemon -count=1 -timeout=30m` — ok 239.9s
- `go test ./internal/ipc -count=1 -timeout=30m` — ok
- `go test ./test/docs -count=1 -timeout=30m` — ok
- `go test ./test/guards -count=1 -timeout=30m` — FAIL only TestCarriedDefects_WaveReportRequiresResolution (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2), pre-existing and identical to the implementer's run
- `go test ./test/guards -count=1 -v -run 'TestCarriedDefects_ManifestIsWellFormed|TestCarriedDefects_OpenRowsHaveLivingEvidence|TestGuard_EveryProductReadIsClassified'` — PASS
- `go test ./test/e2e -count=1 -timeout=30m -v -run (13 prompt/spool rows incl. TestE2ESpoolSubmodeEndToEnd, TestE2E_VerbatimPromptSurvivesRestart, TestV5_ObserveToStatusRoundTrip)` — PASS, ok 83.9s <!-- runpatterns: the -run argument describes a set of thirteen rows the evidence log names, not a runnable pattern -->
- `go run ./tools/devtool fmt-check; go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns; go vet (windows + GOOS=linux) daemon/ipc/guards` — all exit 0
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w8-sp08d3fix 7509f1fb sp08d3fix-review --timeout 30m -- ./internal/daemon ./internal/ipc ./test/guards` — daemon PASS 1546/0/1, ipc PASS 152, guards only the same 5 pre-existing WaveReportRequiresResolution failures (exit 1)

### Open issues

- Within-one-pass pid-reuse residual: a client-<pid> file that a pid-reusing hook appended to before any drain keeps its record order, so a host-later prompt can take a later turn, and possibly turn 0, ahead of another file's earlier prompt. The product names it (counter, Warn, rehydrator host_order entry). Only a per-record merge across files or a per-process-unique spool name would close it, and D35 specified neither.
- test/guards TestCarriedDefects_WaveReportRequiresResolution fails on SP06-D2, SP08-D1, SP10-D1, SP09-D1 and SP20-D2 (pre-existing, coordinator-owned).
- Full ./test/e2e was not re-run this round; the implementer's full run failed only the load-sensitive TestV3_HotPathUnchangedWithLedgerResident timing gate.

### Needs the owner

- Optional: decide whether the within-one-pass pid-reuse residual should be closed beyond D35, with a spool file name unique per hook process or a per-record merge. Either one changes the spool format or the drain design, including what spoolMaxBytes bounds. As shipped it is documented and flagged honestly.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


