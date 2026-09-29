# Wave 13 — w13-ledger (live-lane defects, D45)

Branch `closeout/w13-ledger`. Workflow `wf_3da1508e-fe4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `f8ee78ba9a976b7b679301d86917587226702b95`

### Root cause

F-UAT01-2: resolveTurn used only the open segment, and a session's first segment opens at turn 0 and stays open. D1: the MCP tools read the ledger only after the first compaction had opened it. D2: the daemon's single ledger had no session, and IngestMCP never passed the caller's. D3: staleness was refreshed only at open, startup and idle. D5 had four parts: records stored with session '' were filtered out of the checkpoint; a live draft never re-read the ledger before PreCompact; the elimination's DAG node sat at turn 0; the evidence tool was spelled differently in negknow and mcp. D8: the segment log records a segment's end and tokens only at close, the session-end close omitted the tokens feature, and there was no from/to validation.

### Summary

## w13-ledger report: the elimination ledger and MCP self-record attribution (V6 close-out wave 13, D45)

**Result.** All six items are fixed on closeout/w13-ledger (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w13-ledger, cut from 84d2512). The branch has 11 commits and every commit builds and vets. I wrote seven live-lane reproduction rows in `internal/cli/live_ledger_test.go`. They run the shipped `runDaemon`, the real hook clients and the `mcp` op with an empty session, exactly as `qompack mcp` forwards it. **All seven failed on 84d2512 on the finding they reproduce** (`runs/01-live-red-windows.log`) and pass on the final tree. I also reverted each of ten fix hunks one at a time; the test that pins it went red every time (`runs/02-unit-red-by-reverting-each-fix-windows.log`). Every log is under `plans/sdd/V6-closeout/w13-ledger/runs/`.

### Root causes and fixes

**(1) F-UAT01-2 / install D1: MCP self-records filed at turn 0, fsck exits 1**
- **Cause:** `daemon.resolveTurn` took the turn only from the session's open segment. A session's first segment opens at turn 0 and stays open until a changepoint. So every retrieval's own `qompack-mcp:` record was written at turn 0, behind hook records of later turns. UAT-01's index shows exactly this: `qompack-mcp:93dd…` at turn 0 after `ToolSearch` at turn 3.
- **Design decision:** records written by MCP calls stay inside the turn sequence. §5.16 says the tool-use record is filed like any observed one, and the host's own record for the same call carries turn 3.
- **Fix:**
  - The turn is now the largest of three lower bounds:
    - the observer's live turn, through the new `observer.ProgressReporter` and `Services.SessionProgress`;
    - the tool_use index frontier (`store.PromptRecovery`);
    - the open segment.
  - The daemon attaches `mcp.WithLive`, and `recordTurn` re-resolves the turn at the moment the record is written. Hook records the workers publish while the tool runs therefore cannot end up ahead of it. An explicit turn is still used verbatim.
- **Existing stores:** fsck no longer fails on them for ever. An ephemeral `qompack-mcp:` record at turn 0 that sits behind its session's turn is reported as a note and left out of the order. It does not move the baseline, so a real regression behind it is still a defect. A self-record that carries a turn is ordered like any other record.

**(2) retrieval D1: ledger opened only at the first compaction**
- **Cause:** the MCP tools only read the handle that the first compaction's lazy open had published.
- **Fix:**
  - The tools are now wired with `openingLedger`. It opens the ledger through the daemon's existing one-shot opener (`Options.OpenLedger`) on first use, so there is still one handle, closed on stop.
  - Every other consumer keeps the read-only `liveLedger`. My first attempt changed `liveLedger` itself; the full cli run caught the checkpoint supplier then opening the ledger (`TestProductionCheckpointSourcesStayLazyAndReportUnavailable`), so I split the two accessors.
  - With no ledger, `already_tried` now answers `state:"unavailable"`, `degraded:true`. `record_eliminated` returns a tool error saying nothing was recorded.

**(3) retrieval D2: session scope stored as ""**
- **Cause:** the daemon's single ledger is opened with no session, and `IngestMCP` never passed one.
- **Fix:**
  - A `negknow.Caller` (session, turn) now travels on each call's context. `mcp.invoke` attaches it; so do the rehydrate build and the daemon's rehydration selection.
  - `Record` fills in the caller's session. `Query`, `Active` and `TopActive` judge session scope for the caller.
  - A ledger opened with no session keeps every session's active records in `tried.bloom`. Rebuilt from the "no session" view instead, a session's own records would drop out of the filter at the next reopen; `TestLedger_MultiSessionReopenKeepsEverySessionsKeys` pins this, and the revert made it fail.

**(4) retrieval D3: `already_tried` stayed active after a dependency changed in-session**
- **Fix (refresh on read):** before answering, `Query` compares the `depends_on` files of the active records sharing its match key with the store. It flips changed ones stale and persists the flip. This is one `ChangedSince` over those few records, done outside the ledger lock.
- **Why refresh on read rather than on capture:** refreshing on capture would add a ledger scan to the observer's hot path for every new file version.
- A failed re-check answers `uncertain`. A successful one confirms freshness even when an older ledger-wide coverage watermark is set.

**(5) retrieval D5: checkpoints sealed with `decisions []` and `eliminated []`**
Proven from UAT-08's evidence together with the code. There were four causes:
- The record carried `"session":""`, and the checkpoint keeps only this session's or project-scoped records. Fixed by (3).
- A live draft re-reads the ledger only at `Begin` and during `Advance`, and `Advance` runs only over closed segments. A draft begun at the previous seal therefore never saw the new elimination. `PreCompact` now merges the ledger into the draft and derives each carried elimination's rejected-alternative decision before sealing.
- The ledger emitted the elimination's DAG node at turn 0, so `fromEliminations` skipped it for any segment starting after turn 0. The node is now placed at the caller's turn.
- Decisions were derived from every session's eliminations. They now follow the same this-session-or-project rule (`carriedBy`) as the checkpoint's eliminated list.

The `why` `evidence_withheld` "the capture has no usable path provenance" was a real defect. negknow stores the evidence under tool `record_eliminated` with no path, but `authorizeOrigin` recognised only the fallback spelling `mcp__qompack__record_eliminated`. It now accepts the exported `negknow.EvidenceTool`: this is reason text the agent typed, so there is no file to authorize.

**(6) retrieval D8: timeline showed turns 0-0 and accepted from > to**
- An open segment now reports the session's live turn as its end, and the observer's running token count when the segment is the observer's own.
- `from > to`, when the caller supplied both, is a tool error. A `from` past a defaulted `to` is still an empty answer.
- The session-end segment close never passed the "tokens" pseudo-feature, so every such segment was stored with 0 tokens for good. It now passes it.

**Docs:** the `gen-mcp-docs` prologue (and so `docs/mcp-tools.md`) and the `already_tried`, `record_eliminated` and `timeline` sections of `docs/user-guide.md` describe the new behaviour.

### Criterion changes (rationale also in the commit bodies)
- **`TestBootstrapLedgerSkippedWhenStoreIsNil`, `TestNewToolDepsResolvesTheLedgerLive`, `TestNewToolDepsWithoutAnAccessorStaysNilTolerant`:** these required the old `{"available":false,...}` body, which the live lane recorded as a defect (C44-3). They now require the documented `unavailable` state with `degraded:true`. Each row still forbids an `active` answer or an invented one.
- **`TestOnSessionEnd_SegmentClosedWithFeatures`:** "exactly five keys" became the five §6.6 keys, each still required by name, plus the "tokens" pseudo-feature, whose value is now also asserted. The old count pinned the zero-tokens defect.

### Commands and results
- **Live reproduction rows:** `go test -p 2 -count=1 -run 'TestLive(MCPSelfRecordKeepsSessionTurnOrder|LedgerToolsAnswerBeforeFirstCompaction|SessionScopedEliminationStaysInItsSession|AlreadyTriedSeesInSessionDependencyChange|PreCompactCarriesEliminationAndDecision|TimelineShowsOpenSegmentProgress|TimelineRefusesAnInvertedRange)$' ./internal/cli/` — RED 7 of 7 on 84d2512, then ok.
- **Revert checks:** each of 10 fix hunks reverted alone — RED 10 of 10 (log 02).
- **Focused cli rows on the final tree:** ok, 19.6s (log 03).
- **Touched packages in full on the final tree:** `go test -p 2 -count=1 -timeout=45m ./internal/negknow/ ./internal/mcp/ ./internal/checkpoint/ ./internal/rehydrate/ ./internal/daemon/ ./internal/observer/` — all ok (log 04).
  - `./internal/cli/` in full: ok, 74.5s. After that run I changed only `live_ledger_test.go`, for lint, and re-ran those rows.
  - An earlier combined run failed `TestBudget_RefreshStaleness` (11.4 ms against 10 ms) and `TestBudgetBF` (p95 295 ms against 250 ms) while other seats were loading the machine. Each passed when re-run alone, and both passed in the final full run.
- **Static checks:**
  - `go vet` on the touched packages: ok on Windows and with GOOS=linux.
  - `go run ./tools/devtool fmt-check`: ok.
  - `devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: PASS on every sub-check. The first run caught a `time.Sleep` and an ineffectual assignment in my test; both are fixed.
  - `go test ./test/docs`: ok. `gen-mcp-docs --check`: up to date.
- No hot-path rows, no whole test/e2e or test/integration runs, no Linux container, no real Claude Code session, and no real home: the in-process rigs set HOME, USERPROFILE and CLAUDE_CONFIG_DIR to a temporary directory.

### Decisions made under D33 (no new budget or bound numbers)
- **Pre-fix `"session":""` session-scoped records** are now visible to no session. They belong to a session that cannot be identified. They stay in the append-only log and are still counted.
- **`record_eliminated` with no ledger** is a tool error rather than an `available:false` body, so a failed durable write can never be read as success.
- **Turn resolution cost:** it scans the tool_use index under the store's read lock, twice per MCP call. The existing `PromptFrontier` bound of 2^18 records applies; past it, that source is skipped.

### Commits

- 9d9604fa fix(negknow): scope ledger calls to the calling session and turn
- 46260613 fix(mcp): answer the ledger tools and timeline truthfully
- 37492d56 fix(observer): report session progress, close segments with tokens
- d4be245f fix(daemon): file MCP self-records at the session's current turn
- 502268c2 fix(rehydrate): read eliminations for the session being rehydrated
- 9c010408 fix(checkpoint): seal eliminations and their decisions at PreCompact
- 24e62873 fix(cli): keep pre-fix MCP self-records out of fsck's turn order
- d5b1f57d fix(cli): open the elimination ledger on first MCP use
- ef2aa3e6 docs(mcp): document the live ledger, session scope and timeline
- 9f70f3e7 test(v6): record w13-ledger RED evidence
- f8ee78ba test(v6): record w13-ledger GREEN evidence

### Tests

- `go test -p 2 -count=1 -run 'TestLive(MCPSelfRecordKeepsSessionTurnOrder|LedgerToolsAnswerBeforeFirstCompaction|SessionScopedEliminationStaysInItsSession|AlreadyTriedSeesInSessionDependencyChange|PreCompactCarriesEliminationAndDecision|TimelineShowsOpenSegmentProgress|TimelineRefusesAnInvertedRange)$' ./internal/cli/ (on 84d2512, no fix)` — RED 7/7, each on its live finding (runs/01-live-red-windows.log)
- `scratch mutate.py: revert each of 10 fix hunks alone, run its pinning test` — RED 10/10, files restored (runs/02-unit-red-by-reverting-each-fix-windows.log)
- `go test -p 1 -count=1 -run '<the seven TestLive rows>|TestFsck_ToolUse|TestOpeningLedgerOpensOnFirstUseAndOnlyThere$|TestProductionCheckpointSourcesStayLazyAndReportUnavailable$|TestBootstrapLedgerSkippedWhenStoreIsNil$|TestNewToolDeps' ./internal/cli/` — ok 19.6s on the final tree (runs/03)
- `go test -p 2 -count=1 -timeout=45m ./internal/negknow/ ./internal/mcp/ ./internal/checkpoint/ ./internal/rehydrate/ ./internal/daemon/ ./internal/observer/` — all ok on the final tree, budget rows included (runs/04)
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok 74.5s (before the lint-only edits to live_ledger_test.go; those rows re-ran green in runs/03)
- `earlier combined full run under co-load: TestBudget_RefreshStaleness, TestBudgetBF` — failed on wall clock under other seats' load (11.4ms vs 10ms; p95 295ms vs 250ms); both passed re-run alone (-p 1) and in the final full run
- `go vet (touched packages) on Windows and with GOOS=linux` — ok
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — PASS on every sub-check (first run caught a time.Sleep and an ineffectual assignment in the new test; fixed)
- `go test ./test/docs && go run ./tools/devtool gen-mcp-docs --check` — ok; docs/mcp-tools.md is up to date
- `go build ./... && go vet <touched> at each of the 8 code commits` — all ok (bisectable)

### Criterion changes

- TestBootstrapLedgerSkippedWhenStoreIsNil, TestNewToolDepsResolvesTheLedgerLive, TestNewToolDepsWithoutAnAccessorStaysNilTolerant: now require already_tried's documented unavailable state with degraded:true, not the old {"available":false,"reason":"...not present in this build"} body. The live lane recorded that body as a defect (C44-3). Each row still forbids an active or invented answer.
- TestOnSessionEnd_SegmentClosedWithFeatures: 'exactly 5 keys' became the five §6.6 keys, each still required by name, plus the 'tokens' pseudo-feature, whose value is now asserted. The old count pinned the defect that stored every session-end segment with 0 tokens.

### Open issues

- Coordinator: re-run the live rows on the fixed candidate. That means UAT-08 steps 1-5, UAT-09 steps 2 and 5, the C4.4 ledger and timeline rows, and fsck index.tool_use after a session with an MCP call (C4.2). These tests stand in for the real host, which I did not run.
- pinsckpt overlap: decisions and pointers extracted per segment in Advance still run only over closed segments. My PreCompact refresh covers eliminations and their decisions only. Empty encoded_segments and pointers at PreCompact (F-UAT03-1) remain with pinsckpt, whose change may also touch checkpoint/precompact.go.
- The rehydration digest and the checkpoint carry an elimination's status as last recorded. The in-session stale flip happens on already_tried, or at idle or open refresh. If nobody asks already_tried after a dependency changes, a compaction before the next idle refresh can still show the record as [active]. A RefreshStaleness at PreCompact would close this; it is not done here.
- Pre-existing: SessionStart's startup/resume RefreshStaleness reads Services.Ledger, which is nil on the daemon path. The refresh that runs when the ledger opens covers this case now.
- Minor, message wording (mcpresp's area): another session's session-scoped record answers already_tried {state:absent} with the bloom-only note 'no backing record exists'. For this case the note is imprecise.
- Test hygiene outside this seat: hook clients resolve the project root from the process working directory, so hook rigs in internal/cli without QOMPACK_PROJECT_ROOT refuse their Reads as out of project. The rigs affected are compactLoadRig in sessionstart_compact_load_test.go and other cli tests that run hook clients. Their deliveries were spooled into the repository worktree's .qompack/spool, which I found and deleted in my own worktree. C1.16's same-session ingest load therefore never indexed its Reads.

## Independent review

### review:ledger: needs-fixes

- **major** `internal/cli/fsck.go:830 (fsckUnattributedSelfRecord), used at the checkToolUse switch (~line 896)` — The compatibility path for existing stores only exempts pre-fix self-records filed at turn 0. The old resolveTurn returned the open segment's StartTurn, which is non-zero for any session whose segment was rolled: a changepoint, SubagentStop, resume, or a scheduler roll. Those pre-fix self-records are still reported as monotonicity defects, so fsck on such stores exits 1 for ever. That is the outcome item (1) said must not happen.
  - Evidence: The live lane's own evidence has this shape. plans/sdd/V6-closeout/live/uat/UAT-11/diag-rerun/store/index_tool_use.jsonl holds 16 ephemeral qompack-mcp: records (mcp__qompack__expand/re_read) at turn 10, interleaved after hook records at turn 12 (the segment opened at the SubagentStop at turn 10). uat/UAT-11/diag-rerun/cli/12-fsck-json.stdout.txt shows "tool_use qompack-mcp:908f1acbdbd2 reports turn 10 after turn 12 ... turns are monotone". fsckUnattributedSelfRecord requires `tu.Turn == 0`, so every one of these rows is still a defect after the fix. The new tests (TestFsck_ToolUseUnattributedSelfRecordIsNotARegression) cover only turn 0, and TestFsck_ToolUseAttributedSelfRecordIsOrderedLikeAnyOther pins the non-zero case as a defect.
  - Fix: Widen the exemption to match the pre-fix producer's actual signature: an ephemeral record with the mcp.SelfRecordIDPrefix whose turn is behind the session baseline AND equals the StartTurn of one of that session's segments in the segment log (the only value the old resolveTurn could produce). Report it as a note and do not move the baseline. If the segment log is unavailable, fall back to noting any ephemeral self-record behind the baseline. Add an fsck row built from the UAT-11 diag-rerun shape (turn-10 self-records after turn-12 hook records, with a segment starting at 10) that must exit 0, and keep a regressing non-self-record behind it as a defect.
- **minor** `internal/checkpoint/writer.go:926-944 (Draft.refreshNegativeKnowledge)` — At every PreCompact, the refresh turns every carried elimination into a rejected-alternative decision. That includes stale records and project-scoped records from earlier sessions, and it applies no from-turn cut, unlike Advance's extractDecisions(ctx, src, seg.StartTurn, d.session). The node turns of other sessions' records come from those sessions' turn numbering. mergeDecisionsLocked sorts by Turn descending and truncates to maxDraftDecisions (64). In a project with many project-scoped eliminations from long earlier sessions, those decisions can therefore push this session's own lower-turn decisions (explanations, pins) out of the sealed checkpoint at every seal.
  - Evidence: `for _, r := range d.cp.Eliminated { if dec, ok := eliminationDecision(r, eliminationTurn(src.Graph, r, 0)); ok { decs = append(decs, dec) } }` runs with no turn or session restriction beyond carriedBy. writer.go:997-1005 sorts turn-descending and caps at maxDraftDecisions=64 (draft.go:31). The new test (TestPreCompactSealsEliminationsRecordedAfterTheDraftBegan) covers only one same-session record.
  - Fix: Apply the same cut Advance uses. Derive PreCompact decisions only for eliminations the draft has not already considered: records recorded since the last Advance/Begin, or whose node turn is at or after the open segment's StartTurn. Alternatively, limit the PreCompact addition to records whose Session == d.session. Add a row with more than 64 foreign project-scoped eliminations at high turns and assert that the session's own decision survives the seal.
- **minor** `internal/negknow/ledger.go:703-726 (inView/visibleActive) with ledger.go:953-957 and internal/mcp/handlers.go:81` — The multi-session ledger now puts every session's active records into tried.bloom. already_tried from session B for session A's session-scoped record therefore takes the BloomOnly branch. It returns state absent with the note "a filter hit was recorded but no backing record exists (possible false positive)" and _meta bloom_only:true, and increments the bloom_only counter. The note is false (a record does exist), the _meta flag reveals that another session recorded that approach, and false-positive telemetry is inflated. The implementer listed the wording as mcpresp's problem, but this seat's change is what causes it.
  - Evidence: `if len(cands) == 0 { l.count(queryStateBloomOnly); return Answer{State: AnswerAbsent, BloomOnly: true}, nil }` runs whenever byMatch has entries that visible() refuses for the caller's session. The open_issues entry acknowledges the imprecise note.
  - Fix: In Query, tell the two cases apart. If l.byMatch[mh] is non-empty but no record is visible to the caller, return a plain AnswerAbsent (no BloomOnly) and count it under a separate state such as out_of_scope. Keep BloomOnly for the case where no backing record exists at all. Add a negknow row asserting that the cross-session query is absent without BloomOnly.
- **nit** `internal/mcp/ephemeral.go:112 (recordTurn) and internal/mcp/live.go doc comment` — The comments claim that re-resolving at write time means a self-record 'cannot land behind' hook records. The turn is computed just before store.RecordToolUse, not atomically with the append. A SubagentStop (which increments st.Turn) or another turn-advancing publication on a parallel worker can still land in the gap, so the guarantee is 'narrows the window', not 'cannot'.
  - Evidence: `Turn: recordTurn(ctx, r)` is evaluated in the struct literal, then `h.store.RecordToolUse(ctx, rec)` takes the store lock separately. observer/stop.go:268 advances st.Turn on SubagentStop under the session lock only.
  - Fix: Reword the comments to state the residual window. Alternatively, have the store clamp an ephemeral self-record's turn to the session frontier under its write lock at append time.
- **nit** `commits 9f70f3e7, f8ee78ba, ef2aa3e6` — Three commits lack the `Refs: V6-VERIFY, C4.2/C4.4` footer that the other eight carry.
  - Evidence: `git log 84d2512..HEAD --grep='Refs:' --invert-grep` lists ef2aa3e6 docs(mcp), 9f70f3e7 test(v6) RED evidence and f8ee78ba test(v6) GREEN evidence.
  - Fix: Add the Refs footer when the branch is next rewritten or squashed for integration.

## Fix seat (review resolution) — status `done`, head `b96e4a2f`

### Root cause

F1: the pre-fix resolveTurn filed a retrieval's self-record at the open segment's StartTurn, or 0 when no segment was open. After any segment roll that turn is non-zero, but the implementer's fsck exemption matched turn 0 only (see the UAT-11 diag-rerun store). F2: the seal-time ledger refresh minted a decision for every carried elimination with no from-turn or session cut, so foreign project-scoped records at high turns filled the maxDraftDecisions cap. F3: the multi-session ledger's filter holds every session's keys, and Query reported any hit with no visible candidate as BloomOnly, even when a record in the ledger's own view backed it.

### Summary

FIX SEAT for w13-ledger. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w13-ledger, branch closeout/w13-ledger, now at b96e4a2f. The implementer's head was f8ee78ba. I confirmed all three review findings against evidence and fixed each one, with a test that was RED before the fix. The evidence logs are plans/sdd/V6-closeout/w13-ledger/runs/05-review-red-windows.log and 06-review-green-windows.log.

## Review resolution

**Finding 1 (major): fsck exempted only turn-0 self-records. CONFIRMED and FIXED in 7d44c3ca.**
- The old store still fails. In plans/sdd/V6-closeout/live/uat/UAT-11/diag-rerun/store:
  - segments.jsonl opens segment 2 at st=10 (the SubagentStop at turn 10).
  - index_tool_use.jsonl holds 16 ephemeral qompack-mcp: records at turn 10, placed after hook records at turn 12.
  - cli/12-fsck-json.stdout.txt reports 17 defects ("qompack-mcp:908f1acbdbd2 reports turn 10 after turn 12 ...").
- Root cause: the pre-fix resolveTurn (84d25122 internal/daemon/mcpop.go) returned one of two values:
  - the open segment's StartTurn (segLog.Open sets EndTurn = StartTurn, and Current returns only unclosed segments);
  - 0 when the session had no open segment.
  - The stdio process never sends a Turn (cmd_mcp.go:210). Those two values are therefore the old producer's full signature.
  - The implementer's `fsckUnattributedSelfRecord` matched only turn 0.
- Fix, in internal/cli/fsck.go:
  - `readSegmentStarts` reads the `open` records of index/segments.jsonl and collects each session's start turns.
  - `fsckPreFixSelfRecord` exempts a record that sits behind its session's baseline only when all of these hold: it is ephemeral, its id has the mcp.SelfRecordIDPrefix, and its turn is 0 or a start turn of one of its own session's segments.
  - An exempt record is reported as a note and does not move the baseline.
  - If segments.jsonl is missing, it is treated as empty, so only turn 0 is exempt.
  - If segments.jsonl exists but cannot be read, any self-record behind the baseline becomes a note. fsck still fails in that case, because index.segments reports the unreadable log as its own defect.
  - Every other regression is still a defect.
- New rows in internal/cli/fsck_tooluse_test.go:
  - TestFsck_ToolUsePreFixSelfRecordAtItsSegmentStartIsNotARegression: the UAT-11 shape in miniature; must exit 0.
  - TestFsck_ToolUseRegressionBesideSegmentStartSelfRecordsIsStillADefect: a hook record at turn 10 after turn 12, and a self-record at turn 11 (not a segment start), are both defects.
  - TestFsck_ToolUseSegmentStartOfAnotherSessionDoesNotExcuseASelfRecord: a guard; it passed before and after the fix.
  - TestFsck_ToolUseSelfRecordBesideAnUnreadableSegmentLogIsReportedNotCounted.
  - Three of the four were RED on the implementer's fsck.go.

**Finding 2 (minor): the seal-time refresh minted every carried elimination as a decision, with no cut. CONFIRMED and FIXED in fb8ebc93.**
- A temporary diagnostic (deleted, never committed) showed the effect. With 70 foreign project-scoped eliminations at turns 500-569 and the session's own at turn 12, the sealed checkpoint behaves like this:
  - At the default 12000-token budget: 0 decisions, because Truncate drops them all.
  - At a wide budget: 64 decisions, all foreign, and the session's own decision is dropped by the maxDraftDecisions cap.
- Fix, in `Draft.refreshNegativeKnowledge` (internal/checkpoint/writer.go): the refresh now uses Advance's cut on the range no segment has encoded yet.
  - Only records with `r.Session == d.session`.
  - Only those whose node turn is at or after `d.frontier`. A record with no graph node takes the frontier, as Advance's takes its from-turn.
  - eliminated[] still carries every record `carriedBy` admits.
  - Advance's own minting is unchanged.
  - Decisions from earlier ranges stay answerable: `why` walks the checkpoint chain (handlers.go `why`, which checks the latest checkpoint and then the listed ones).
- New row TestPreCompactDecisionsAreTheSessionsOwnNotEveryCarriedElimination in internal/checkpoint/negknow_refresh_test.go:
  - 70 foreign records plus the session's own; the test sets a wide token budget (1<<20) so the cap is what bites.
  - RED before the fix: "0 [own] ... 64 decisions sealed".
- The D5 rows still pass: TestPreCompactSealsEliminationsRecordedAfterTheDraftBegan and the live TestLivePreCompactCarriesEliminationAndDecision.

**Finding 3 (minor): a cross-session match answered BloomOnly. CONFIRMED and FIXED in 1d289874, narrower than the reviewer proposed.**
- The implementer's own row asserted `other.BloomOnly == true` for a cross-session query.
- The fix has to keep SP-09's contract. The plan row TestQuery_ScopeSession_OtherSessionHidden_NextIdle requires BloomOnly when a per-session ledger reads a filter that still holds a foreign session's key.
- The correct distinction is whether the filter's hit is backed by a record in the ledger's own view (`inView`). Visibility to the caller is a separate question.
  - The daemon's multi-session ledger has everything in view. A cross-session match there is a true hit, so the answer is a plain AnswerAbsent, counted under negknow.query.absent.
  - A hit that no record in view backs remains BloomOnly.
- I did not add the reviewer's suggested `out_of_scope` counter. SP-09's metric list is the single authority (see the ledger.go comment), and the pre-existing path for this same case counted `absent`.
- New row TestLedger_OtherSessionsRecordIsAPlainAbsence in internal/negknow/caller_test.go:
  - B querying A's record, and A's project-scope query, are both plain absent.
  - bloom_only counter = 0, absent counter = 2.
  - It was RED before the fix.
- These existing rows still pass unchanged: TestQuery_BloomOnly, TestQuery_ScopeSession_OtherSessionHidden, TestQuery_ScopeSession_OtherSessionHidden_NextIdle, TestQuery_ScopeProject_ExcludesSessionScoped, TestQuery_UnknownStatusIsBloomOnly.

## Criterion changes
- **TestLedger_CallerSessionStampsRecordAndScopesQuery** (the implementer's new row, not yet merged): `require.True(t, other.BloomOnly, ...)` is now `require.False(...)`.
  - Rationale: it pinned the behaviour that finding 3 shows is wrong.
  - The SP-09 rows that define BloomOnly are untouched and still pass.
  - SP-09's Query pseudocode (plans/V3-SP-09-negative-knowledge.md, "if len(cands) == 0 { bloom_only }") now differs from the code for in-view hits that the caller's scope refuses. The plan text is not mine to edit, so the coordinator or plan owner should note it.
- **TestFsck_ToolUseAttributedSelfRecordIsOrderedLikeAnyOther**: I only corrected the doc comment. The assertion is unchanged.
- No threshold, budget, timeout or golden was changed. No t.Skip or nolint was added.

## Commands and results (Windows 11, go1.26.6, -p 2, shared machine)
- **RED** (fix file reverted to the implementer's version):
  - `go test -p 2 -count=1 -run '^(TestFsck_ToolUsePreFixSelfRecordAtItsSegmentStartIsNotARegression|TestFsck_ToolUseRegressionBesideSegmentStartSelfRecordsIsStillADefect|TestFsck_ToolUseSelfRecordBesideAnUnreadableSegmentLogIsReportedNotCounted|TestFsck_ToolUseSegmentStartOfAnotherSessionDoesNotExcuseASelfRecord)$' -v ./internal/cli/` → exit 1 (3 FAIL, 1 guard PASS)
  - `go test -p 2 -count=1 -run '^TestPreCompactDecisionsAreTheSessionsOwnNotEveryCarriedElimination$' -v ./internal/checkpoint/` → exit 1
  - `go test -p 2 -count=1 -run '^(TestLedger_OtherSessionsRecordIsAPlainAbsence|TestLedger_CallerSessionStampsRecordAndScopesQuery)$' -v ./internal/negknow/` → exit 1
- **GREEN, focused rows:**
  - `-run '^TestFsck_ToolUse.*$' ./internal/cli/` → 11 PASS
  - `-run '^TestPreCompact(DecisionsAreTheSessionsOwnNotEveryCarriedElimination|SealsEliminationsRecordedAfterTheDraftBegan)$' ./internal/checkpoint/` → PASS
  - `-run '^TestLedger_(OtherSessionsRecordIsAPlainAbsence|CallerSessionStampsRecordAndScopesQuery)$' ./internal/negknow/` → PASS
  - `-run '^TestQuery_(BloomOnly|ScopeSession_OtherSessionHidden|ScopeSession_OtherSessionHidden_NextIdle|ScopeProject_ExcludesSessionScoped)$' ./internal/negknow/` → PASS
- **Touched packages in full, once each:**
  - `go test -p 2 -count=1 -timeout=30m ./internal/cli/` → ok 76.3s
  - `go test -p 2 -count=1 ./internal/checkpoint/` → ok 89.4s
  - `go test -p 2 -count=1 ./internal/negknow/...` → ok 25.7s / 1.6s
- **Consumers, focused rows:**
  - `go test -p 2 -count=1 -run '^(TestAlreadyTried.*|TestRecordEliminated.*|TestLedgerToolsWithNoLedger|TestWhy.*)$' -v ./internal/mcp/` → 29 PASS
  - `go test -p 2 -count=1 ./internal/rehydrate/...` → ok
  - `go test -p 2 -count=1 -run '^(TestCadenceFinalizesWhenDraftReachesBudget|TestBindCheckpointSealsOnTheFirstPreCompact|TestBindCheckpointDegradesWhenTheLedgerCannotBeOpened|TestWireCheckpointResolvesItsSourcesLive)$' -v ./internal/daemon/` → 4 PASS
  - `go test -p 2 -count=1 -run '^(TestLive(MCPSelfRecordKeepsSessionTurnOrder|LedgerToolsAnswerBeforeFirstCompaction|SessionScopedEliminationStaysInItsSession|AlreadyTriedSeesInSessionDependencyChange|PreCompactCarriesEliminationAndDecision|TimelineShowsOpenSegmentProgress|TimelineRefusesAnInvertedRange)|TestFsck_ToolUse.*)$' -v ./internal/cli/` → 18 PASS
- **Hygiene:**
  - `go run ./tools/devtool fmt-check` → exit 0
  - `go vet` and `GOOS=linux go vet` on ./internal/cli/ ./internal/checkpoint/ ./internal/negknow/ → clean
  - `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` → exit 0, every sub-check PASS
- No wall-clock failures occurred. I started no background processes or load generators.
- I did not start the Linux container, and I ran no real Claude Code sessions.

### Commits

- 7d44c3ca fix(cli): excuse pre-fix self-records at their segment's start
- fb8ebc93 fix(checkpoint): mint only this session's seal-time decisions
- 1d289874 fix(negknow): answer a scoped-out filter hit as a plain absence
- b96e4a2f test(v6): record w13-ledger review-fix RED and GREEN evidence

### Tests

- `go test -p 2 -count=1 -run '^(TestFsck_ToolUsePreFixSelfRecordAtItsSegmentStartIsNotARegression|TestFsck_ToolUseRegressionBesideSegmentStartSelfRecordsIsStillADefect|TestFsck_ToolUseSelfRecordBesideAnUnreadableSegmentLogIsReportedNotCounted|TestFsck_ToolUseSegmentStartOfAnotherSessionDoesNotExcuseASelfRecord)$' -v ./internal/cli/ (implementer's fsck.go)` — RED as required: exit 1, 3 FAIL + 1 guard PASS
- `go test -p 2 -count=1 -run '^TestPreCompactDecisionsAreTheSessionsOwnNotEveryCarriedElimination$' -v ./internal/checkpoint/ (implementer's writer.go)` — RED as required: exit 1 (own decision absent, 64 foreign decisions sealed)
- `go test -p 2 -count=1 -run '^(TestLedger_OtherSessionsRecordIsAPlainAbsence|TestLedger_CallerSessionStampsRecordAndScopesQuery)$' -v ./internal/negknow/ (implementer's ledger.go)` — RED as required: exit 1 (BloomOnly true for a cross-session match)
- `go test -p 2 -count=1 -run '^TestFsck_ToolUse.*$' -v ./internal/cli/` — PASS (11 rows)
- `go test -p 2 -count=1 -run '^TestPreCompact(DecisionsAreTheSessionsOwnNotEveryCarriedElimination|SealsEliminationsRecordedAfterTheDraftBegan)$' -v ./internal/checkpoint/` — PASS
- `go test -p 2 -count=1 -run '^TestLedger_(OtherSessionsRecordIsAPlainAbsence|CallerSessionStampsRecordAndScopesQuery)$' -v ./internal/negknow/` — PASS
- `go test -p 2 -count=1 -run '^TestQuery_(BloomOnly|ScopeSession_OtherSessionHidden|ScopeSession_OtherSessionHidden_NextIdle|ScopeProject_ExcludesSessionScoped)$' -v ./internal/negknow/` — PASS
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok 76.3s
- `go test -p 2 -count=1 ./internal/checkpoint/` — ok 89.4s
- `go test -p 2 -count=1 ./internal/negknow/...` — ok (negknow 25.7s, negknowtest 1.6s)
- `go test -p 2 -count=1 -timeout=30m -run '^(TestAlreadyTried.*|TestRecordEliminated.*|TestLedgerToolsWithNoLedger|TestWhy.*)$' -v ./internal/mcp/` — 29 PASS, ok
- `go test -p 2 -count=1 ./internal/rehydrate/...` — ok
- `go test -p 2 -count=1 -timeout=30m -run '^(TestCadenceFinalizesWhenDraftReachesBudget|TestBindCheckpointSealsOnTheFirstPreCompact|TestBindCheckpointDegradesWhenTheLedgerCannotBeOpened|TestWireCheckpointResolvesItsSourcesLive)$' -v ./internal/daemon/` — 4 PASS, ok
- `go test -p 2 -count=1 -timeout=30m -run '^(TestLive(MCPSelfRecordKeepsSessionTurnOrder|LedgerToolsAnswerBeforeFirstCompaction|SessionScopedEliminationStaysInItsSession|AlreadyTriedSeesInSessionDependencyChange|PreCompactCarriesEliminationAndDecision|TimelineShowsOpenSegmentProgress|TimelineRefusesAnInvertedRange)|TestFsck_ToolUse.*)$' -v ./internal/cli/` — 18 PASS, ok
- `go run ./tools/devtool fmt-check; go vet + GOOS=linux go vet ./internal/cli/ ./internal/checkpoint/ ./internal/negknow/` — exit 0, clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, every sub-check PASS

### Criterion changes

- internal/negknow/caller_test.go TestLedger_CallerSessionStampsRecordAndScopesQuery: `require.True(t, other.BloomOnly, ...)` becomes `require.False(...)`. The implementer's unmerged assertion pinned the behaviour review finding 3 shows is wrong. TestLedger_OtherSessionsRecordIsAPlainAbsence now pins the correct behaviour, and the SP-09 rows that define BloomOnly (TestQuery_BloomOnly, TestQuery_ScopeSession_OtherSessionHidden_NextIdle) are unchanged and pass.
- internal/cli/fsck_tooluse_test.go TestFsck_ToolUseAttributedSelfRecordIsOrderedLikeAnyOther: doc comment corrected to name the widened exemption; the assertion is unchanged.

### Open issues

- Pre-existing, not introduced here: Advance's decision source (b) still mints decisions for other sessions' project-scoped eliminations, compared by node turns that use those sessions' own turn numbering. For a session's first segment (from=0) every project-scoped elimination in the project becomes a candidate, so enough of them can still push this session's own decisions out of the 64-decision cap. The implementer's carriedBy filter narrowed this but did not remove it. It needs a decision on how foreign-session turns should rank.
- Seal-time decisions (refreshNegativeKnowledge) are minted without emitting the dag KindDecision node that ExtractDecisions emits. `why` still finds them through the checkpoint chain, but slice scoring cannot rank them. This is carried over from the implementer's design and was not raised by the reviewer.
- The fsck compatibility exemption would also hide a current-build self-record that regressed to turn 0 or to its own session's segment start. It is reported as a note, not silently dropped, and the current producer never files behind a published record. Accepted trade-off.
- SP-09 plan pseudocode for Query (plans/V3-SP-09-negative-knowledge.md) still says any hit with no visible candidate is bloom_only. The code now answers a plain absence when a record in the ledger's view backs the hit. The plan text needs a note from its owner or the coordinator.
- Not run under the daytime rules: whole test/e2e and test/integration. TestV4_WhyAndDroppedAnswerFromRealProducers and TestE2E_EliminationLifecycle exercise the changed decision and ledger paths through the Advance route, which is structurally unchanged. The coordinator should re-run them and the Phase 4 live rows (F-UAT01-2, UAT-08, UAT-09, UAT-11, retrieval D2/D5) on the fixed candidate.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


