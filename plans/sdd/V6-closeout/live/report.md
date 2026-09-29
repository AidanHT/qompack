# Phase 4 live lane on candidate 3 (`d5598eb4`)

Workflow `wf_3f8fa5f3-e45` (`coordinator/live-uat-c3.js`), bundle `qompack-bundles/c3/` (windows-amd64 BUNDLE.json sha256 `32600778…4505`), Claude Code 2.1.280, Windows 11. Agent-executed on the owner's real host per owner decision D3; not human UAT. Subagents cannot write report files, so the coordinator rendered this verbatim from their returned results.

## Part `install` — done, 4 of 8 sessions, head `e7241208`

Install part done with 4 of the 8 allowed real sessions (haiku), plus one failed launch that never started a process: the driver's bin 'claude' is a .cmd shim, so I used the npm package's claude.exe. The parent session's CLAUDE_CODE_* linkage variables were removed for every session. C4.1 passes: the frozen 0.3.0 bundle installs through a disposable marketplace into an isolated profile, and the host lists 7 hooks, the qompack MCP server and 6 commands. UAT-01 passes: installed at local scope in the real profile, the init event showed the 8 tools, 6 commands, 4 exercised hook events and all gated switches false. But `qompack fsck` fails after any MCP call: MCP records are indexed at turn 0. C4.6 passes: 0 secret hits across all Qompack surfaces, and every retrieval form refused deny-ruled and out-of-project reads. UAT-12 and C4.8 fail. With full:true, expand and re_read responses reach 263,559 bytes, over maxResponseBytes (262,144). The pre-upgrade backup (written by 301a8e9) restores with a proven reader but failing integrity checks, because the candidate treats the old build's prompt capture sidecars as unpublished. Upgrade, uninstall and reinstall all preserved .qompack/ byte-identically, and recall found session A's work after the reinstall. Result blocks for UAT-01 and UAT-12 are filled in docs/uat.md. The evidence is under plans/sdd/V6-closeout/live/{c4/C4.1,c4/C4.6,c4/C4.8,uat/UAT-01,uat/UAT-12}, with sessions.tsv and guard/install.txt, all committed on closeout/live. Cleanup: uninstall and marketplace remove after each window; run-created orphaned caches removed (BUNDLE.json compared first); the D10 staged copy removed after the final uninstall (sha256 equal to the frozen bin), along with the empty run-created ~/.qompack; the prev worktree removed. Final guard check passes. Raw copies and secrets.json stay in scratch.

### C4.1

**Result:** pass — 0 sessions (plugin CLI only). Frozen bundle 0.3.0 installed through a disposable local marketplace (qompack-live) into a fresh isolated CLAUDE_CONFIG_DIR at user scope.

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.1/ (notes.txt indexes it). Every step exit 0: marketplace add, install --json, plugin list --json, marketplace list --json, plugin details, and validate --strict --json on the installed cache copy (success true, no errors or warnings). Every checksums.txt line verifies in the cache; bin/qompack.exe and BUNDLE.json are byte-identical to the frozen bundle. The host lists 7 hooks, the qompack MCP server and 6 commands (shown as 'Skills (6)'); /qompack:checkpoint is absent, as D36 intends. The isolated profile was deleted afterwards and the real-home guard was unchanged after every step.

- Checklist C4.1 still says 'the seven commands'; D36 ships six and the host lists six.
- The brief said install.md §3 claims install, update and uninstall have no --json. The committed install.md §3 already records that 2.1.280 accepts --json. Observed: install/update/uninstall --json each print one JSON line. No doc change needed.
- `claude plugin details` files the six slash commands under 'Skills (6)'.

### UAT-01

**Result:** pass, with findings — 1 session (haiku). Real profile, LOCAL-scope marketplace install. Preconditions held: no <project>/.qompack, `test -e ~/.qompack/config.json` exit 1, zero QOMPACK_* variables.

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-01/ (notes.txt, snapshot.txt, version.txt, config-provenance.txt, self-test.txt/.json, session/, cli/, store/). Init event: plugin:qompack:qompack connected; the 8 tools match docs/mcp-tools.md; slash commands are qompack:{dropped,eval,pin,recall,status,why}. Hook events fired: SessionStart:startup, UserPromptSubmit, PostToolUse and Stop, all success. SessionEnd ran (per the store) but has no event in the stream and nothing on stderr. version prints 0.3.0; every one of the 108 config leaves is default and all gated switches are false. self-test exit 0: the 4 host rows read not-yet-implemented and session_start.fires reads marker-found. Result block filled in docs/uat.md. Rollback not applicable (initial state absent). Cleanup: uninstall -s local, marketplace remove, and removal of the run-created orphaned cache. Guard unchanged.

- DEFECT: `qompack fsck` exits 1 after an ordinary session with one MCP call. index.tool_use (severity 2) reports 'tool_use qompack-mcp:... reports turn 0 after turn 3; turns are monotone': the MCP server's own record (tool mcp__qompack__recall) is indexed at turn 0. This blocks C4.2's 'fsck clean'.
- `qompack status` with the daemon live reports 'host contract: 2 of 9 FAILING'. mcp.server_registered reads 'initialize-not-received' although the host connected the server and it answered a call. transcript.readable reads 'transcript_path does not exist', but the host creates the transcript after SessionStart:startup.
- doctor --json reports captures.unpublished as degraded ('5 gap(s) across 7 sidecar(s)'), while fsck's captures and publication rows pass. The two diagnostics disagree.
- Doc: step 4 expects version 0.1.0 at plugin/.claude-plugin/plugin.json; the bundle has 0.3.0 at .claude-plugin/plugin.json. Step 8 says precompact.custom_instructions_accepted reads 'retired'; the installed CLI self-test reads not-yet-implemented. Expectations not edited.
- Host: the init event gives the plugin path as the marketplace SOURCE directory, not the plugins/cache installPath (confirmed in UAT-12: the old build's daemon ran from the source directory's bin).
- The frozen daemon ran from the D10 staged copy ~/.qompack/bin/c7b2b120.../qompack.exe. With default config (idle exit 1800 s) it was terminated only after checking the lock pid, that the image is qompack.exe, and that the command line names this project.

### C4.8

**Result:** fail — 3 sessions (UAT-12 A/B/C). What passed: the upgrade from 301a8e9 (0.2.99-prev) to frozen 0.3.0, uninstall and reinstall. .qompack/ was byte-identical across the upgrade and across the uninstall (368 files). After the reinstall, session C's recall returned session A's old-build capture. What failed: 'backup -> restore -> fsck certified'. The pre-upgrade backup restores with a proven reader but failing integrity checks (exit 1), and fsck of the source store also exits 1.

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.8/notes.txt; data in plans/sdd/V6-closeout/live/uat/UAT-12/cli/ (09-12 upgrade, 05-06 backup, 30-33 restore and fsck of the recovery, 29/31 source unchanged, 40/42 uninstall, 43-44 reinstall, 51-52 source fsck) and sessionA/B/C.

- DEFECT (cross-version): the candidate counts the previous build's four UserPromptSubmit capture sidecars as 'stage 1 only, no reference joined'. The captures and publication rows fail (fsck exit 1), `backup restore` exits 1 ('integrity checks failed', destination retained), and the first post-upgrade daemon start logs LOUD unpublished_captures=4. A pre-upgrade backup is therefore not certifiable.
- The index.tool_use MCP turn-0 defect (UAT-01) recurs: 14 records.
- The restore itself worked: OpenedOK, 16 content roots and 10 tool refs proven (same build), and the delivery seal check (--seal-check) passed. The source's later writes were untouched.
- Not covered: a 301a8e9 reader against the upgraded store (downgrade); the recovery was not activated.

### C4.6

**Result:** pass (the `why` tool was not exercised). No planted secret reached any durable Qompack surface, and deny-ruled and out-of-project archived reads were refused by every retrieval form tried.

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.6/notes.txt and c46-scan.json. Two credential-shaped secrets (aws_access_key_id sha256 3b87f8aa...5a5f04, github_token sha256 f9ccc6ab...d9f130) were planted; the literals exist only in the scratch secrets.json. The scan checked each secret's literal, its JSON-escaped form, every 10-character fragment and its sha256 hex over: project .qompack (368 files, 221 .zst decompressed, including backup/, checkpoints/0001.json and the rehydrate state), the restored recovery (94 files), ~/.qompack and QOMPACK_HOME. Result: 0 hits, with «redacted:» placeholders present. The 16 MCP results and 1 rehydration payload in the streams, and the CLI and probe outputs, also had 0 hits. Refusals: host Read was refused; recall (MCP, slash command, bundle CLI) shows no deny.txt content; expand (by tool_use_id and root hash) and re_read answer denied; out-of-project re_read gives 'path escapes the project root' without echoing the path; expand of the outside capture is denied for lost path provenance.

- The host's own transcripts of sessions A and B contain both secrets: the host's Read results and the host's compaction summary. This is a host surface, not a Qompack one, and is recorded as asked.
- `qompack expand` does not exist as a CLI command (exit 2), so the CLI form of expand is n/a.
- `why` was not exercised because no decision id existed.

### UAT-12

**Result:** fail — 3 sessions. (a) With full:true, expand and re_read return 263,559 and 263,567 bytes of result text (266,291 / 266,318-byte JSON-RPC lines) against runtime.mcp.maxResponseBytes 262,144: the bound caps the content span, not the response. (b) The restore's integrity checks fail (see C4.8). Deny, out-of-project refusal, the minimal span, and uninstall survival all pass. Order deviation, recorded in the Result block and notes: steps 2-5 ran after step 6, on the candidate, because 301a8e9 predates C1.9's deny-rule support. The optional old-build characterization of step 2 was not run.

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-12/ (notes.txt, setup-facts.json, sessionA/B/C, cli/ including 20-mcp-probe.json with exact sizes from a direct stdio probe of frozen `qompack mcp`, store/ with day log, LOUD.log, checkpoint, rehydrate state and backup manifest). Result block filled in docs/uat.md; Rollback verified = unverified, with the reasons given.

- DEFECT: maxResponseBytes bounds the content, not the response. The overshoot is JSON escaping plus the envelope, and it grows with escape-heavy content: the blob capture's 2,134 content bytes became 2,948 bytes of text (+38%).
- expand and re_read responses carry no fidelity or coverage field, contrary to user-guide.md and the step 3-4 expectation.
- The binary expectation was not observed: the host delivers `cat blob.bin` as decoded text (3,000-byte file stored as 2,134 bytes with U+FFFD and control characters), and Qompack stores and returns it as text.
- Observed strings (marked 'to be confirmed at execution'): the host's denial reads 'File is in a directory that is denied by your permission settings.'; Qompack's reads "authorization denied: the host's current permission rules deny reading the associated path". The day log across the upgrade has no version-block or retired-meaning warning (no config file exists), plus the LOUD unpublished_captures=4 line. config.capture reads 'applied as written'.
- With idleExitSeconds=30 the daemon logged 'ending abandoned session ... silentMs=31607' during a live session's 32 s /compact. Capture continued. The abandonment threshold is the idle-exit window.
- In a freshly git-init'ed repo with no index, the checkpoint drops 'pointer_git_unavailable', and the rehydration payload echoes the absolute .git\index path.
- Host: /qompack:recall's `!qompack recall` resolved through the plugin's bin/ on PATH (C4.5 observation). The host saved the 259.7KB MCP result to a file and showed the model a 2 KB preview despite MAX_MCP_OUTPUT_TOKENS=150000. `plugin update` marks the old cache version .orphaned_at immediately. Host transcripts carry hook durationMs (e.g. Stop 115 ms), which the stream lacks.

### Defects

- D1 fsck index.tool_use fails (severity 2, exit 1) on every store that served an MCP call: the MCP server's own record (tool mcp__qompack__<tool>, tool_use_id qompack-mcp:...) is indexed at turn 0 after hook records of later turns. Evidence: uat/UAT-01/cli/x-fsck-json and y-fsck-json-stopped, store/index_tool_use.jsonl; uat/UAT-12/cli/51-52.
- D2 runtime.mcp.maxResponseBytes caps the content span, not the response. expand and re_read with full:true on a 324,902-byte capture return 263,559 / 263,567 bytes of text (266,291 / 266,318-byte JSON-RPC lines) against 262,144. The overshoot grows with escape-heavy content. Evidence: uat/UAT-12/cli/20-mcp-probe.json and sessionB/stream.jsonl.
- D3 (cross-version) the candidate treats the previous build's (301a8e9) UserPromptSubmit capture sidecars as unpublished ('stage 1 only, no reference joined'). fsck captures/publication fail, `backup restore` of a pre-upgrade backup exits 1 on its integrity checks, and the first post-upgrade daemon start logs LOUD unpublished_captures=4. Evidence: uat/UAT-12/cli/30,32,33,27,51; store/logs_LOUD.log.
- D4 the status contract monitor reports mcp.server_registered failing ('initialize-not-received') while the host shows the server connected and it served calls. It also reports transcript.readable failing because the host transcript does not exist yet at SessionStart:startup in -p mode. Evidence: uat/UAT-01/cli/x-status.stdout.txt.
- D5 doctor captures.unpublished reads degraded (5 gaps across 7 sidecars on a candidate-only store; 40/55 in UAT-12) while fsck captures/publication pass on the same candidate-only store. Evidence: uat/UAT-01/cli/x-doctor-json, y-doctor-json-stopped.
- D6 expand and re_read responses carry no fidelity or coverage field, although user-guide.md and UAT-12's expectations say fidelity and coverage qualify them. Evidence: uat/UAT-12/cli/20-mcp-probe.json.

### Open issues

- docs/uat.md's intro still says no human scenario has been executed and every Result block reads 'not executed'. Now stale for UAT-01 and UAT-12; I edited only the Result blocks, as instructed.
- UAT-01 step 4's stale expectation (0.1.0 at plugin/.claude-plugin/plugin.json) and step 8's 'retired' claim need the coordinator's decision; the expectations were not edited.
- Binary fidelity cannot be reached through this host: Read refuses binary files (not tried here) and Bash delivers decoded text. UAT-12 step 4's expectation needs a doc disposition.
- C4.6: the `why` tool's evidence preview was not exercised, because no decision id existed.
- C4.8: a 301a8e9 reader against the upgraded store (downgrade) was not run, and the recovered project was not activated.
- The optional old-build characterization of UAT-12 step 2 was not run.
- The abandoned-session detector ends a live session silent past idleExitSeconds (seen at 30 s during a /compact). Decide whether that is acceptable for short idle settings.
- Host transcripts under ~/.claude/projects/C--Users-Quant-AppData-Local-Temp-claude-qompack-live-install-*/ remain. They belong to the host; I left them untouched per the never-edit-~/.claude rule.
- Scratch raw copies, including the project stores and secrets.json, remain under C:/Users/Quant/AppData/Local/Temp/claude/qompack-live/install/ for re-inspection. Delete them when no longer needed.

Real-home guard ok: True. plans/sdd/V6-closeout/live/guard/install.txt, committed. Diffs appeared only inside my own open local-scope install windows: installed_plugins.json and known_marketplaces.json, cache/qompack-live, and a host-created empty plugins/data/qompack-qompack-live removed by homeguard clean. Nothing else was touched by hand under ~/.claude except deleting the run-created orphaned cache/qompack-live directories, as the rules allow. The last two entries (13:34:51 and 13:35:07 EDT) read 'real home fingerprint unchanged', check exit 0. The last one also shows .qompack (0 entries), the same as at snap time.

## Part `sessions` — done, 10 of 12 sessions, head `fccf8436cc8c5456bc5b3a0223dd6a3af1fb0c52`

Sessions part of the V6 live lane is complete: 10 of the 12 allowed real Haiku sessions used, all through --plugin-dir with the frozen 0.3.0 bundle. All runs were agent-executed on the owner's real host under D3; none is human UAT.

- **Scenarios covered:** UAT-02 through UAT-06, C4.2, C4.3 and C4.9. No budgeted step was skipped.
- **Evidence and doc changes:** evidence is under plans/sdd/V6-closeout/live/uat/UAT-02..06 and live/c4/C4.9. Index notes are in live/c4/C4.2 and live/c4/C4.3. The five Result blocks in docs/uat.md are filled, and sessions.tsv and guard/sessions.txt are appended.
- **Real-home guard:** check passed (exit 0) after every session and at the end. The host's empty plugins/data/qompack-inline was removed by homeguard clean each time. The run-created staged daemon copy (~/.qompack/bin/c7b2b120..., sha256 equal to the bundle binary) was removed at the end with no qompack.exe running, together with the empty ~/.qompack, which was absent at snap.
- **Daemons:** only this part's own daemons were stopped, each after a lock-pid, image and command-line check.

**Scenario results:**
- **UAT-02:** pass on its fail criteria, but expand carries no Fidelity, and the oversized and binary captures read exact.
- **UAT-03:** pass. The checkpoint's pointers are empty, and a probe settled the incomplete-outcome form.
- **UAT-04:** pass. Manual, automatic, host-failed and hook-failed compactions all proceeded, the manifest stayed intact and no native-shrink claim appeared.
- **UAT-05:** fail.
- **UAT-06:** fail.
- **C4.2:** fail on the clean doctor/fsck clause; pass on events and counts.
- **C4.3:** fail on D5's whole-record rule and on current authority. The round trip works, and a subagent-only fact was recovered through Qompack expand after compaction.
- **C4.9:** pass on all three degraded paths.

Main new defects:
- A first prompt over 8 KiB is injected cut to 8,192 bytes under the verbatim heading, with no overflow entry.
- User corrections never reach intent evolution.
- A pin made mid-session is missed by the next checkpoint (stale view).
- After --fork-session, the fork's first prompt replaces the original intent.
- An idle frontier advance records a segment encoded into a checkpoint that is never written, so fsck and restore integrity fail.
- A garbage-collected MCP ephemeral record makes backup restore fail while fsck passes.

### UAT-02

**Result:** pass (none of the row's three fail criteria occurred), with two expected-result fields not observed

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-02/ (notes.txt); session n=1; docs/uat.md Result block filled

- F-UAT02-1: expand/re_read responses carry result._meta.qompack but no Fidelity anywhere. Fidelity exists only in the capture sidecars and index (34 exact, 1 redacted). Same as F-UAT12-2.
- F-UAT02-2: the oversized big.log (310,800 B) was delivered whole by the host and stored whole (312,302 B root), so its sidecar reads exact. The row expects a non-exact value. The 262,144 B bound applies only to expand paging (truncated true, next_span).
- F-UAT02-3 (host): no binary fidelity is reachable. Read refuses a .bin file and fires no PostToolUse, so there is no capture. A PNG arrives as an image block and `cat` arrives as host-decoded text (3,064 B -> 2,068 B); both are stored exact.
- F-UAT02-4 / F-UAT03-2: after the daemon's idle frontier work, index/segments.jsonl records an encode into checkpoint 0002, and 0002 is never written. fsck fails index.segments.
- F-UAT02-6 (host): init lists the subagent tool as Task, but the model's tool_use is named Agent.
- F-UAT02-7: plugin.root_resolves reads unset in the daemon snapshot although the hooks ran from ${CLAUDE_PLUGIN_ROOT}.
- Planted-secret scan of the whole post-run store found 0 hits: literals, base64-decoded sidecar bytes and decompressed .zst objects. creds.txt was stored with «redacted:...» placeholders.

### C4.2

**Result:** FAIL on the 'status, doctor, fsck clean' clause; PASS on hook capture and counts

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-02/ (c42-counts.json, cli/07-transcript-facts.json, cli/05-*, cli/09-*); index plans/sdd/V6-closeout/live/c4/C4.2/notes.txt

- All seven events fired and all 37 hook responses were success/exit 0: SessionStart (startup+compact), UserPromptSubmit 9, PostToolUse 15, Stop 9 and SubagentStop 2. PreCompact wrote checkpoint 0001 and the transcript shows 'Compacted PreCompact [...] completed successfully: {}'. SessionEnd wrote the sessions.jsonl end line. stderr was empty, with no Hook cancelled.
- Counts match. Prompts: host 9 = store 9. Captures: host tool uses 16 minus the refused binary Read (no PostToolUse) = 15 = store 15. Sessions: 1 = 1. The store also holds 8 qompack-mcp:* self-records and 2 SubagentStop records.
- status was clean: all 9 assertions holding, and latency rows read unavailable with reasons, never 0.
- doctor reported captures.unpublished degraded (26 gaps / 35 sidecars) while fsck and status counted 0 (F-UAT01-3).
- fsck exited 1 on index.tool_use: 8 MCP self-records at turn 0 (F-UAT01-2). After the idle work it also failed index.segments.

### UAT-03

**Result:** pass (checkpoint written, manifest hash matches, byte-identical after idle exit + restart), with findings

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-03/ (notes.txt, steps/, probe/); session n=2

- F-UAT03-1: the PreCompact checkpoint has empty encoded_segments, pointers.files and pointers.tools after two captured Reads.
- F-UAT03-2 (product defect): after a CLEAN idle exit, segments.jsonl claims an encode into checkpoint 0002, which is never published. fsck index.segments then fails, and a backup taken after it restores with integrity FAILED.
- F-UAT03-3: `qompack status` starts a daemon but prints 'daemon: status refused: ' with an empty reason.
- F-UAT03-4: a hand copy of a store carrying a stale run/daemon.lock from another path never starts a daemon. The daemon exits 0 silently with no log line, and hooks answer the deferred note after 8.3 s.
- F-UAT03-5 (the doc's 'to be confirmed' incomplete outcome): with 0001 corrupted in a restored copy, the rehydration read 'checkpoint 0000' and was built from verbatim L0. Only the LOUD log names the refusal.
- F-UAT03-6 (host): the model recovered K17 from Claude Code's own post-compaction file re-attachment, not from Qompack.

### UAT-04

**Result:** pass (manual, automatic, host-failed and hook-failed compactions all proceeded; manifest intact and re-hashing; no native-shrink claim)

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-04/ (notes.txt, steps/s5-manifest-check.txt, steps/s6-native-shrink-search.txt); session n=5

- F-UAT04-1 (product defect; a D5/UAT-05 'record cut mid-record' failure): a 17,774-char first prompt is injected as its first 8,192 bytes, cut mid-word, under the 'verbatim, never summarized' heading. There is no overflow entry or pointer, the state file says truncated:false, and a spurious intent_mismatch is logged LOUD 14 times. The checkpoint and the L0 capture both hold all of it.
- F-UAT04-2 (retrieval): recall 'Q23 data/keys.txt' returns 0 hits, with 'denied':2 in a project that has no deny rules, although the Bash capture holds the value. recall by the value itself finds it.
- F-UAT04-3 (run configuration): the 30%/100k override made the host compact 13 times and end one turn with 'Autocompact is thrashing'. Every compaction still proceeded.
- F-UAT04-5: a host-failed compaction (too_few_groups) happened after PreCompact wrote 0001. 0001 stays intact and verifying.
- Injections were 9,009-9,010 UTF-16 units, under 9,500, delivered inline. The config-refused compaction answered {} and wrote no checkpoint.

### UAT-05

**Result:** fail (current authority not kept; a pin missing with no drop entry; and UAT-04's 8 KiB mid-record cut)

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-05/ (notes.txt, run1-*/run2-* blocks, state files, dropped envelopes); sessions n=3, n=4

- F-UAT05-1 (product defect): an explicit user correction never reached user_intent.evolution in any checkpoint or block. Run 1 re-injected only the superseded semicolon requirement.
- F-UAT05-2 (product defect): a /qompack:pin made while the daemon runs is written to invariants.jsonl but not to the invariants.json view (fsck reports 'the view is stale'). The next checkpoint and block omit it, with dropped [] and degraded false. It appeared after a daemon restart.
- F-UAT05-3: MCP record_eliminated answers available:false, 'elimination ledger not present in this build'.
- F-UAT05-4: after a daemon is terminated, backup create/verify refuse 'daemon lock already held' for about 90 s although no daemon runs.
- Passing fields: size within budget (221/12000 and 109/150 tokens); delimiters and section order; inline (725 and 361 units). Run 2's tiny budget named the tier1 OVERFLOW with an expand pointer, degraded true, and a counted tail.

### UAT-06

**Result:** fail (after --fork-session the 'verbatim original intent' is the fork's own first prompt; no correction ever carried)

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-06/ (notes.txt, block1-4, section2-in-order.txt, diff-block2-vs-fork-block3.txt, step7-postcompact-search.txt); sessions n=6, n=7 (--resume), n=8 (--resume --fork-session); no step skipped

- F-UAT06-1 (product defect): in a forked session the rehydrator replaces the checkpoint's true original with the fork's first prompt and labels it 'verbatim from L0'. It adds an intent_mismatch drop.
- F-UAT06-2: two corrections were never recorded, so every block carries only the superseded 100-per-minute requirement (same defect as F-UAT05-1).
- F-UAT06-3: SessionStart:resume and SessionStart:fork inject only the contract probe line.
- F-UAT06-4 (host): Claude Code 2.1.280 re-attaches files read by Read or `cat` after compaction, from its cache, even when the file was deleted.
- Step 7: no log line waits on or reports a post-compaction event (pass). /qompack:why was not run because no block lists a decision id. MANIFEST skips checkpoint seq 3.

### C4.3

**Result:** FAIL on D5's whole-record rule and on current authority; the round trip itself works end to end

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.3/notes.txt indexing UAT-03/04/05/06 (7 sessions)

- PreCompact wrote a checkpoint at every compaction and every manifest line re-hashes. Every /compact turn showed 'completed successfully: {}', so there was no PreCompact output rejection (C1.12). No session's stderr had 'Hook cancelled' (C1.15/D13).
- SessionStart:compact injected the block both for /compact and for 11 forced automatic compactions. Sizes were 361-9,010 UTF-16 units, under ~9,500 and under the host's 10,000 cap, all inline.
- The model recovered a pre-compaction fact from Qompack: in UAT-06 session B, M17, which only a subagent had read and whose file was deleted, came back via recall + expand after the second compaction ('Source: Qompack archive (expand)'). The other probes were answered from the host's own context.
- Overflow was named with a pointer at a tiny budget (UAT-05 run 2). It was NOT named for a first prompt over 8 KiB (F-UAT04-1).
- UAT-03's daemon-gone-and-back re-read was byte-identical. UAT-04's failed compaction left the last checkpoint intact.

### C4.9

**Result:** PASS (the host session never broke in (a), (b) or (c)), with findings

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.9/ (notes.txt, session1/, session2-copy/, cli/, store-*); sessions n=9, n=10

- (a) A before step stopped the project's own daemon after checking the lock pid, image qompack.exe and cmdline. The next turns succeeded and hooks exited 0. MCP recall answered 'daemon unavailable; retrieval temporarily offline' until the ~90 s stale window passed. A new daemon then answered, and every delivery from the outage had been replayed from the spool. Nothing was lost.
- F-C49-1: in-session /qompack:status showed 'source: none (error)' and 'daemon: status refused: ' with an empty reason. The day log says nothing about the ended daemon or the replay.
- (b) A settingsVersion 99 project config was written mid-session. Turns and capture continued. config-violations.json holds the exact documented message, and self-test, doctor, config print and the log all name the runtime.migration reset. Session 1's attempt at (b) failed in my harness (a quoting error, before-step exit 1, nothing written); it was redone in session 2.
- (c) One chunk object of a capture was moved out of a disposable copy. expand and re_read answered found:false, available:false, and the session continued. fsck names the missing chunk, the unmaterializable tool_use and the unresolvable sidecar.
- F-C49-2 (product defect): backup verify passes on a store whose MCP ephemeral record was garbage-collected, but backup restore fails ('restored tool reference root ... does not resolve'). fsck treats the same reference as an ok note. Because of this the copy was made by hand with cp -rp after a clean idle exit.
- F-C49-3: the missing-object reason says 'a damaged object is preserved as evidence', which is not what happened.

### Defects

- F-UAT04-1: the rehydrator injects only the first 8,192 bytes of a longer first prompt, cut mid-word, under 'Original user intent (verbatim from L0 capture — never summarized)'. There is no OVERFLOW/tier1 entry or pointer, the state file says truncated:false, and a spurious intent_mismatch drop is logged LOUD every compaction. The checkpoint and the exact L0 capture both hold all 17,774 chars. This violates D5 whole records and UAT-05's 'record cut mid-record' criterion. Evidence: uat/UAT-04/injected-block-first.txt, store/, notes.txt.
- F-UAT05-1 / F-UAT06-2: explicit user corrections are never recorded as user_intent.evolution in any checkpoint or rehydration (UAT-05 two runs, UAT-06 two corrections), so blocks present only the superseded original requirement. Evidence: uat/UAT-05/run1-checkpoint-0001.json, uat/UAT-06/section2-in-order.txt.
- F-UAT06-1: after `claude --resume <id> --fork-session`, section 2 'verbatim original intent' is the fork's own first prompt, and the checkpoint's true original is overridden as an intent_mismatch. Evidence: uat/UAT-06/block3-C-fork-compact.txt, diff-block2-vs-fork-block3.txt.
- F-UAT05-2: a /qompack:pin made while the daemon runs is written to pins/invariants.jsonl but not to the pins/invariants.json view (fsck 'the view is stale'). The next PreCompact checkpoint and block omit it with no drop entry. Evidence: uat/UAT-05/cli/run1-fsck-json, run1-injected-block.txt.
- F-UAT03-2 (= F-UAT02-4): after a clean idle exit, index/segments.jsonl records segment N encoded into a checkpoint seq that is never written. fsck index.segments fails, and backup restore then reports integrity FAILED. Evidence: uat/UAT-03/cli/s5-fsck-json, probe/12-backup-restore.
- F-C49-2: backup create/verify succeed but restore fails with 'restored tool reference root ... does not resolve' when an MCP ephemeral record's root was GC'd. This follows any session that calls an MCP tool and then idles. fsck reports the same reference only as an ok-row note. Evidence: c4/C4.9/cli/c-00-backup-restore.*, c-01-copy-note.txt.
- F-UAT03-1: PreCompact checkpoints carry empty encoded_segments, pointers.files and pointers.tools after captured reads.
- Known, reproduced: fsck index.tool_use flags MCP self-records at turn 0 (F-UAT01-2). doctor captures.unpublished over-counts gaps (F-UAT01-3). status reports mcp.server_registered initialize-not-received and transcript.readable 'does not exist' while the server is connected and the transcript exists (F-UAT01-1).
- F-UAT02-1: expand/re_read responses carry no Fidelity (only result._meta.qompack). docs/user-guide and UAT-02 say they do (= F-UAT12-2).
- F-UAT04-2 (retrieval, route to C4.4): recall 'Q23 data/keys.txt' returns 0 hits with 'denied':2 in a project with no deny rules, although the Bash capture holds the value. F-UAT05-3: MCP record_eliminated answers 'elimination ledger not present in this build'.
- Diagnostics: `qompack status` prints 'daemon: status refused: ' with an empty reason when no daemon is up (F-UAT03-3, F-C49-1). The day log records nothing about a terminated daemon or the spool replay. Maintenance and MCP retrieval refuse for about 90 s after a daemon is terminated (F-UAT05-4, F-C49-4). The missing-object reason wrongly says 'damaged object preserved' (F-C49-3).
- F-UAT03-4: a store copied with a stale run/daemon.lock from another path never starts a daemon. The spawned daemon exits 0 with no log line.

### Open issues

- C4.3 recovery of a fact that only Qompack holds was shown once (UAT-06 session B, a subagent-read value) and needed a guided recall + expand. Unguided recall of a Bash-delivered fact failed (F-UAT04-2).
- In UAT-02, UAT-03, UAT-05 and UAT-06 every other post-compaction fact probe was answered from Claude Code's own context. Claude Code 2.1.280 re-attaches files read with Read or `cat` from its cache after compaction, even when deleted, and keeps a preserved message segment. Scenario designs that assume the host forgets file reads need revising.
- /qompack:why was not exercised, because no checkpoint ever recorded a decision (decisions [] everywhere). The capability is unverified by UAT-06.
- UAT-04's forced auto-compaction override (30% of 100k) produced host thrash. A calmer override, or --autocompact (unrehearsed), would give a cleaner automatic-compaction record if one is needed.
- Rollback: all five rows started from initial absence (recorded). The only restores run were probes: UAT-03 restore integrity failed on index.segments, and C4.9 restore failed on a GC'd MCP root. Same-build restore of a post-session store is therefore currently blocked by F-UAT03-2 and F-C49-2. Cross-version and activation checks are unverified.
- The install.md §3 --json documentation finding is not applicable to this part: no plugin CLI step was run; sessions used --plugin-dir. As read in this worktree, install.md §3 already describes 2.1.280's --json.
- Harness note: in UAT-06 session B an async background subagent produced an extra result line, which shifted live_driver.py's per-turn accounting by one (meta.json). Every message was still delivered and answered (stream.jsonl).
- The Result blocks in docs/uat.md for UAT-02..06 are filled in the worktree commit fccf8436. The coordinator should route the product defects listed in defects.

Real-home guard ok: True. homeguard.py snap taken at <scratch>/sessions/guard.json before the first step (guard.json is not committed). clean + check ran after each of the 10 sessions and after the post-run steps; every check passed (14 times check exit 0, 0 failures). Output is appended to plans/sdd/V6-closeout/live/guard/sessions.txt. The only gains were the host's empty plugins/data/qompack-inline/ (removed by homeguard clean each session) and ~/.qompack/bin/c7b2b120.../ (the D10 staged copy, INFO). At the end, with no qompack.exe running and the staged copy's sha256 equal to the frozen bundle's bin/qompack.exe, I removed it with install.md §6's Remove-Item -Recurse -Force $HOME\.qompack\bin, then removed the empty run-created ~/.qompack (it was absent at snap). The final check shows the real home fingerprint unchanged and .qompack/ at 0 entries. Nothing under ~/.claude was written by hand, and no plugin CLI or install was used (--plugin-dir only).

## Part `retrieval` — done, 12 of 12 sessions, head `1b00e0da8bddb7ee152bcd1d541eaaa88f741961`

Retrieval part of the V6 close-out live lane. All work was agent-executed on the owner's real Windows host under D3, not human UAT. I used 12 of 12 real Haiku sessions with the frozen 0.3.0 bundle loaded via --plugin-dir, and every qompack CLI step used the bundle binary.

Results:
- **UAT-07:** pass with findings.
- **UAT-08:** fail.
- **UAT-09:** fail.
- **UAT-10:** pass with findings.
- **UAT-11:** fail.
- **C4.4:** partial.
- **C4.5:** pass.
- **C4.7:** pass.

The four serious product defects:
1. The elimination ledger opens only at the daemon's first compaction. Until then, and after any daemon restart, record_eliminated and already_tried answer 'not present', with no state or degraded field.
2. Session-scoped eliminations are stored with an empty session, so other sessions can see them.
3. Staleness is not refreshed inside the live session after a dependency changes.
4. The contract sentinel is scanned in only the last 256 KiB of the transcript. One large first-turn result therefore degrades the session to passive and silently disables rehydration.

Evidence, notes, tool-matrix.md and commands-vs-docs.md are committed under plans/sdd/V6-closeout/live/. The UAT-07 to UAT-11 Result blocks are filled in docs/uat.md, and `go test ./test/docs` passes. Every real-home guard check passed. The staged copy ~/.qompack/bin/c7b2b120… was created by this run and hashes to the bundle binary; I removed it, and the empty ~/.qompack, after confirming no qompack.exe was running. The final guard check shows the home unchanged.

### UAT-07

**Result:** pass (with findings)

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-07/ (notes.txt; run2/ is the run of record, run1/ kept as a harness failure: Edit missing from --allowedTools). Sessions 2 (retrieval n=1, n=2).

- None of the row's three fail conditions occurred. After an out-of-band disk edit (driftLimit 7), re_read with no `at` still returned the captured turn-7 version (40, source store). at turn:1 returned the turn-1 version (25). expand by hash and by tool_use_id gave the same hash, span [0,838] and content, with no `source`.
- F1: `path:<glob>` is not a glob. path:src/*.go and path:*.go return 0 hits; path:src/ledger.go and path:ledger.go match. internal/store/search.go matches by equality, suffix or substring only. Docs (mcp-tools.md, user-guide, uat.md) say glob.
- F2: `tool:Read` returns 0 hits and `tool:FileRead` matches. The selector takes Qompack's display names, not host tool names.
- F3: step 7 OBSERVED for a never-stored hash: {"found":false,"available":false,"reason":"complete content provenance could not be established"}. It is not a tool error and not absent, but it does not name what was searched.
- Observation: _meta.qompack.hash is the hash of the response's own capture, not the resolved object's hash. Each MCP retrieval response is re-captured twice and pushes files out of the top-k `path:` answers.

### UAT-08

**Result:** fail

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-08/ (notes.txt). Session 1 (n=3).

- Step 2 as written: before the daemon's first compaction, record_eliminated answered {"found":false,"available":false,"reason":"elimination ledger not present in this build"}. There is no id and no evidence. already_tried returned the same body with no state and no degraded. Cause: the daemon wires the MCP tools to a ledger it opens lazily on its first compaction (internal/cli/daemon.go installMCPTools; internal/daemon/rehydrate_service.go, wire_checkpoint.go).
- Diagnostic continuation after /compact matched the row: the ack had id, descriptor, scope, evidence, depends_on, depends_on_unresolved [] and status active. already_tried answered active with reason, evidence, scope, recorded_at and depends_on. A different approach answered {"state":"absent"}. The record is on disk.
- Second defect: the session-scoped record is stored with "session":"" because the ledger is opened with no session. A later, different session of the project answered for it (UAT-09 s5), so session scope does not isolate.

### UAT-09

**Result:** fail

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-09/ (notes.txt). Steps 1-2 are in ../UAT-08/session and step 5 is in c4/C4.4/session. Sessions: n=4 (--resume), n=5, plus parts of n=3 and n=6.

- Step 2 fail: after the model edited config/pool.yaml and a new file version was appended, already_tried stayed `active` for the rest of the recording session (seconds later, and after /compact). Staleness is refreshed only when a daemon opens the ledger, at a startup or resume, or after 120 s idle. The flip came from a later daemon.
- Step 3 pass: stale, with note, reason, evidence, scope, recorded_at, depends_on and stale_because ["config/pool.yaml: dependency hash changed from sha256:9f2751eac830"].
- Step 4 pass in a new session with drop: {"state":"uncertain"}. The observed reason and note strings equal the handlers.go constants. The first attempt was void: the lane's 30 s idle exit ended the silent session during a 150 s wait.
- Step 5 fail: in a project with no ledger the answer was the not-present body (no state, no degraded) before compaction and {"state":"absent"} after it. It was never state unavailable with degraded true.
- Step 6: no response text prohibits the approach. The Haiku model nevertheless read the stale answer's attached reason as a prohibition.
- Code note, not exercised to a conclusion: MCP handlers copy the config when the daemon starts. A staleResponse change reaches already_tried only in a new daemon. Known digest gap not reproduced.

### UAT-10

**Result:** pass (with findings)

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-10/ (notes.txt, status.json, status text in cli/10-status-text-same-moment, dropped.json, eval.json, cli/11-eval-json exit 1, cli/13-eval-corpus-*). Session 1 (n=8).

- No instrument-less cell printed 0. Per-hook rows read unavailable with a reason. The B-D reason is quoted. age_ms is 0 only for a live daemon source. /qompack:eval --json exits 1 with `no evaluation artifacts`, naming dist/live-eval and testdata/bench-replay.json. Telemetry is off.
- Usage categories are not reachable from a user project. Via `qompack eval --corpus` on the committed pilot run, they appear only as arm totals {known, known_records, unknown_records} in alphabetical order. Cost reads 'unavailable: no request ledger was recorded for this run'. The documented canonical order and the {"known":false}+missing form were not observable.
- The contract banner says 1 of 9 FAILING (mcp.server_registered initialize-not-received) while MCP was answering. p95/p99 are printed above the max (106.50 vs 99.00 ms). After a daemon stop the refusal reason is empty. The digest [active] elimination agrees with already_tried.

### UAT-11

**Result:** fail

**Evidence:** plans/sdd/V6-closeout/live/uat/UAT-11/ (notes.txt, cli/ for steps 1-4, session/, sessionstart-hook-responses.json, sentinel-offsets.json, secret-scan-store.json, diag-rerun/). Sessions 2 (n=9 is the row's run; n=12 is a diagnostic rerun).

- Steps 1-4 pass exactly as written. newResult false (default). A refused true gives `invalid value, using default: true not in false`. settingsVersion 2 resets the whole runtime.migration block (logged in config-violations.json, the day log and LOUD.log) and capture continues. An unparseable config: hooks print {} exit 0, .qompack holds only config.json, and self-test exits 1 with config.capture critical 'the project config file is not a single strict JSONC object'. An unknown key: capture continues and config.capture warns 'runtime.notAKey: unknown key'.
- Step 5 blocked by a DEFECT: an ordinary 363 KB Read in the first turn pushed the SessionStart probe sentinel out of the 256 KiB transcript tail that the daemon scans (internal/daemon/handlers.go sentinelScanTailBytes). The sentinel is at byte ~800 and prompt 2 starts at byte 954,253. Both chances missed, the monitor degraded the session to passive, and SessionStart:compact injected nothing (systemMessage only). Step 6 was unreachable.
- The planted secret is absent from both stores in every form, and the creds.env capture is redacted.
- Diagnostic rerun with two small prompts first: the block was injected, all 11 section-6 pointers resolved by expand, and the 5 paths also resolved by re_read. big.log was truncated with next_span and creds.env redacted; both are recorded fidelities.

### C4.4

**Result:** partial — 6 of 8 tools pass their success and error calls; record_eliminated and already_tried fail before the daemon's first compaction

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.4/tool-matrix.md, session/ (n=6), cli/13-mcp-probe-meta.json. The why success call is in c4/C4.5/session (n=7).

- Correct errors were returned. Bad handle sha256:zz: is_error 'hash must be "sha256:" followed by 64 hex characters'. Unknown path: found:false 'no historical version has been captured for this path yet'. Path escaping the root: is_error. Malformed at: is_error. Malformed query k:0: is_error '/k: below minimum'. Bad scope enum: is_error. Unknown argument to dropped: is_error. Unknown tool_use_id: {found:false, searched:'tool_use index'}.
- C44-3: record_eliminated and already_tried are unavailable before the first compaction (same defect as UAT-08).
- C44-4/7: checkpoints in these daemons carry decisions [] and eliminated [] even after an active elimination. The likely cause, not proven, is that MCP records carry turn 0 and fromEliminations skips turns before the cut. why found dec_e760c8bbf50a only in a later daemon's checkpoint, with evidence_withheld 'authorization denied: the capture has no usable path provenance'.
- C44-6: timeline in a live session returns one open segment (turns 0-0, 0 tokens) after 12 turns and 2 compactions, and accepts from > to.
- C44-1/5: an empty recall query is answered over MCP but refused by the CLI with exit 2. An empty already_tried target or approach answers absent.
- The model cannot see result-level _meta. A stdio probe shows ephemeral true on the 7 retrieval tools and false on record_eliminated.

### C4.5

**Result:** pass (with findings)

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.5/commands-vs-docs.md, slash-commands.json (host expansions extracted from this session's transcript), cli/10-help-* and cli/11-*. Session 1 (n=7) in the UAT-08 project.

- Host fact: `qompack` resolved on the command's PATH on the first attempt. `where qompack` returned only the bundle's bin\qompack.exe; with --plugin-dir the host puts the plugin bin/ on PATH.
- The init event lists exactly the six commands, and qompack:checkpoint is absent (D36). The command_permissions match each command's Allowed tools. All six `--help` outputs are byte-identical to docs/commands.md.
- status, status --json, recall --json, recall --k, pin, pin --list, why (found and miss), dropped, dropped --json, eval and eval --json (exit 1 unavailable naming both paths), and recall with no query (exit 2) all match the docs.
- C45-1: in a healthy session the status banner reads 2 of 9 FAILING: mcp.server_registered initialize-not-received and transcript.readable 'transcript_path does not exist'.
- C45-2: when the `!` command exits non-zero, the host shows only its own 'Shell command failed ... [stderr]' line and the model is never invoked, so /qompack:eval in a user project appears as a host error line. commands.md does not mention this.

### C4.7

**Result:** pass (with findings)

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.7/notes.txt, off/ (n=10), reinj/ (n=11). `config print --provenance` before each session proved each key came from the project layer.

- mode off alone: every hook succeeded with exit 0 and output {}. No capture, index or checkpoint was written. meta shows 'no lock' after every turn and no qompack.exe ran. SessionStart:compact injected nothing. Reinjection was left at its default true.
- F1: an MCP recall in mode off returns the tool error 'the qompack daemon returned an empty result', which does not say Qompack is switched off. F2: `qompack mcp` wrote .gitignore, an empty day log and state/mcp.json into the mode-off project.
- reinjection off alone (mode auto): PreCompact wrote checkpoints/0001.json and tool events were recorded in index/tool_use.jsonl. SessionStart:compact returned only the contract probe marker, with no qompack:injected tag. The day log reads 'rehydrate: injection disabled by runtime.migration.reinjection.sessionStartCompact'.

### Defects

- D1 (high): the elimination ledger is opened lazily on the daemon's first compaction (internal/cli/daemon.go installMCPTools -> liveLedger; internal/daemon/rehydrate_service.go, wire_checkpoint.go). Before a compaction, and after any daemon restart, record_eliminated and already_tried return {"found":false,"available":false,"reason":"elimination ledger not present in this build"}. That body has no state and no degraded field, which is outside already_tried's documented states. Evidence: UAT-08 T2/T3, UAT-09 s4 T5-T7 and s5 T1, C4.4 T1, and earlier UAT-05.
- D2 (high): session-scoped eliminations are recorded with "session":"" because the daemon's ledger is opened without a session. negknow visible() then shows them to every later session of the project. Evidence: UAT-08 files/eliminations*.jsonl; UAT-09 s5, a new session that answered uncertain for session 7b2ccf44's record.
- D3 (high): within the live session that changed a dependency, already_tried keeps answering active. RefreshStaleness runs only at ledger Open, at startup or resume, and in idle work after 120 s. The flip was written by a later daemon at 18:49:03Z. Evidence: UAT-08 stream L157->L160 and L192->L195; UAT-09 notes.
- D4 (high): the §12.1 sentinel scan reads only the last 256 KiB of the transcript (internal/daemon/handlers.go sentinelScanTailBytes). One large first-turn tool result (a 363 KB Read) makes both chances miss. The monitor then degrades the session to passive and SessionStart:compact injects no rehydration, only a systemMessage. Evidence: UAT-11 sessionstart-hook-responses.json, sentinel-offsets.json, LOUD.log. The diagnostic rerun with small first prompts injects normally.
- D5 (medium): checkpoints sealed in the daemon that opened the ledger lazily carry decisions [] and eliminated [], even after an active elimination, so `why` has nothing to answer for that session. A plausible cause, not proven, is that MCP-origin records carry turn 0 (fsck 'reports turn 0 after turn N' in every project) and checkpoint/decisions.go fromEliminations skips turns before the cut. When found, why returns evidence_withheld 'the capture has no usable path provenance'.
- D6 (medium, docs or code): the recall `path:<glob>` selector is not a glob; it matches by equality, suffix or substring (internal/store/search.go). `tool:<ToolName>` matches Qompack display names (FileRead), not host names (Read). Evidence: UAT-07 run2 cli/11-recall-*.
- D7 (medium): status reports contract assertions FAILING in healthy sessions: mcp.server_registered initialize-not-received while MCP answers, and transcript.readable 'transcript_path does not exist'. Evidence: C4.5, UAT-10; cf. F-UAT01-1.
- D8 (low): timeline inside a live session returns one open segment (turns 0-0, 0 tokens) and accepts from > to (C4.4).
- D9 (low): a set of small response and message issues:
- An empty MCP recall query is answered (the CLI refuses it, exit 2).
- already_tried with an empty target or approach answers absent.
- The never-stored-hash expand miss does not say what was searched.
- In mode off, the MCP recall error is 'the qompack daemon returned an empty result', and `qompack mcp` writes .gitignore, logs/ and state/mcp.json into the mode-off project.
- Status prints p95/p99 above the max.
- The status refusal after a daemon stop has an empty reason.
- D10 (low, docs): with the committed eval runs, the usage categories' canonical order and the {"known":false}+missing form cannot be observed on any reachable surface. The eval arm categories are keyed alphabetically, and every committed run lacks a request ledger (UAT-10).

### Open issues

- UAT-08, UAT-09 and UAT-11 are recorded as fail pending fixes for D1-D4, and D2 needs a ruling on session scope. After fixes, re-run UAT-08 steps 1-5, UAT-09 steps 2 and 5, and UAT-11 steps 5-6 in fresh projects.
- The UAT-09 step 4 reload-reach question is unresolved. MCP handlers copy config at daemon start (mcp.newHandlers normalizeCfg), so a staleResponse change reaches already_tried only in a new daemon. Found by reading code; the in-session test was voided by a daemon restart.
- Decide D6: make `path:` a glob, or change mcp-tools.md, user-guide and uat.md to say suffix/substring. Also document which names `tool:` accepts.
- UAT-10: the canonical usage-category serialization is still unverified. It needs a live-eval run that records a request ledger.
- The fsck exit 1 on turn-0 MCP index records ('reports turn 0 after turn N') appears in every project of this part, as in UAT-02. It is likely linked to D5.
- My run's non-default QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 made the daemon abandon a silent live session after 31 s. The abandonment window equals idleExitSeconds. With the default of 1800 s this means 30 minutes of silence ends the session in the daemon; that may deserve documentation.
- Not in this part's scope, so not recorded here: the install.md §3 --json documentation finding (install lane) and the 'human scenarios have not been executed' intro paragraph in docs/uat.md, which I was not permitted to edit.

Real-home guard ok: True. plans/sdd/V6-closeout/live/guard/retrieval.txt: 15 check runs (start, after each of the 12 sessions, the pre-cleanup end check and the final check), all 'check exit 0'. settings.json, installed_plugins.json and known_marketplaces.json were unchanged throughout, and the plugins cache, marketplaces and data listings were unchanged. The only gain was ~/.qompack/bin/c7b2b120…/qompack.exe, the D10 staged copy created 14:38:33 by session 1 (~/.qompack was absent at snap). Its sha256 equals the bundle binary and no qompack.exe was running, so it was removed with install.md §6 Remove-Item -Recurse -Force $HOME\.qompack\bin, followed by the empty ~/.qompack. Final check: '.qompack/ (0 entries)', 'real home fingerprint unchanged', exit 0. Homeguard clean removed only a run-created empty plugins/data/qompack-inline after each session. There was no plugin install, no marketplace step and nothing under ~/.claude written by hand.

## Part `recovery` — done, 3 of 4 sessions, head `78bba1620aa009250647f1f26a82ba07b5c56f5a`

Recovery part complete; results are committed as 78bba162 on closeout/live in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-live.

- **Budget:** 3 of the 4 real sessions were used, all on claude-haiku-4-5-20251001 with --plugin-dir pointing at the frozen c3 windows-amd64 bundle. Every qompack command used that bundle's qompack.exe.
- **Self-tests:** both passed first (homeguard_test 10 OK, live_driver_test 3 OK), recorded in plans/sdd/V6-closeout/live/recovery/selftests.txt.

**C1.6 pass.**
- I injected both fault-suite gaps on a stopped real project: dropped the last index/roots.jsonl line (its chunk object exists on disk) and returned an observe.tool sidecar to stage one.
- After a bare session-start restart, the daemon's startup accounting reported both gaps with no operator command: status --json counters unindexed_object_candidates=1 and unpublished_captures=1, the loud tail, and LOUD.log.
- fsck --json exited 1 and named both gaps.
- Automatic recovery does not exist: objects/ and every capture sidecar are byte-identical after the restart, and the roots line and sidecar were not repaired.

**C1.7 pass.**
- backup create --id before and verify both exit 0.
- Pre-new-write: restoring into a fresh D1 gives exit 0, fsck and fsck --seal-check exit 0, and recall finds the pre-backup marker.
- Post-new-write: after session 2, restoring into a fresh D2 gives exactly the backup manifest. The later marker is absent from D2 and present in the source, and fsck of the source exits 0.
- Restoring into an existing destination is refused (exit 1, D1 unchanged). Backup create and verify against a live daemon (started by a hook entry point) are refused (exit 1).

**C5.6 done.** plans/sdd/V6-closeout/live/resources/C5.6/summary.md and data.json aggregate numbers only over all 29 sessions; no budget is proposed.

**C4.11.** windows/amd64 is this host. The Linux container is not running, so the Linux half is unknown; the linux/amd64 bundle passes static acceptance but nothing from it ran. macOS and windows/arm64 are unknown (no runner).

**docs/uat.md.** It has no Result blocks for C1.6, C1.7, C4.11 or C5.6, so I made no uat.md edits.

**Real-home guard and cleanup.**
- homeguard check passed after every session and CLI phase and at the end.
- The staged daemon copy ~/.qompack/bin/c7b2b120... was mine (created 15:28:22 by session 1) and matches the bundle binary. With no qompack.exe running, I removed it, then removed the empty ~/.qompack, which was absent at snap.
- No qompack.exe is left running.
- Disposable projects stay under the recovery scratch folder for inspection.

### C1.6

**Result:** pass: automatic startup accounting and fsck detection both exist. Automatic recovery does not exist, and nothing was reconstructed.

**Evidence:** plans/sdd/V6-closeout/live/recovery/C1.6/ (notes.txt, session/, cli/, store/cuts.json, store/fp*.json, store/diff-*.txt, store/LOUD.log, store/post-state.json). 1 real session (Haiku), sessions.tsv recovery n=1. Seeding: 4 Read turns with every hook successful, then the daemon exited on its own after the 30 s idle timeout. Before the cuts, fsck exited 0 with every check ok. Cut A (object_written_index_line_absent): removed the last of 12 index/roots.jsonl lines (root f2e3973e), whose chunk object c6a707a0 exists on disk. Cut B (v6UnpublishToolCapture): returned the observe.tool sidecar sha256:550a87e2 to stage one (published false, root zero, tool_use_id removed). Restart: a bare `qompack session-start` through the bundle CLI. `status --json` with the daemon up (exit 0) showed counters daemon.publication.unindexed_object_candidates=1 and daemon.publication.unpublished_captures=1. Its loud tail and the newly created LOUD.log both carry: 'daemon: unpublished captures or unindexed objects found at startup unpublished_captures=1 unindexed_object_candidates=1 ... incomplete=false truncated=false'. `fsck --project <root> --json` exited 1 both with the daemon up and after it exited, and stayed read_only. It failed index.roots (root f2e3973e does not resolve), index.tool_use, captures (sidecar 550a87e2 'at stage 1 only') and publication ('1 unlinked captures and 1 unindexed object candidates'). Re-fingerprint: objects/ and all 12 capture sidecars are byte-identical to the post-cut state, roots.jsonl still has 11 lines, and the sidecar is still published:false. Nothing was recovered automatically, which matches docs/backup.md ('discovers ... does not reconstruct').

- Observation: the startup LOUD line and the status counters give counts only. They do not name the stage-one observation id or the unindexed object hash; fsck is where the identities appear.
- Expected, not a defect: after the bare session-start restart, status reports contract assertion mcp.server_registered FAILING (info), because no MCP host initialised.

### C1.7

**Result:** pass: the operator backup, verify and restore path works through the shipped CLI on the packaged bundle, for both pre-new-write and post-new-write rollback. Both refusals behave as documented.

**Evidence:** plans/sdd/V6-closeout/live/recovery/C1.7/ (notes.txt, session1/, session2/, cli/01..17 with cmd/stdout/stderr/exit, 10-D2-vs-backup-vs-source.json, store/). 2 real sessions (Haiku), recovery n=2 and n=3. `backup create --id before --json` exit 0 (consistent:true, 48 files); `backup verify` exit 0. Pre-new-write: restore into a fresh D1 exit 0 (OpenedOK true, ContentRootsProven 9, ToolRefsProven 6, SameBuildOnly true, integrity exit 0 including 'the full dual-reader seal check passed'). fsck D1 exit 0 and fsck --seal-check D1 exit 0; D1 equals the manifest file for file. Reader proof: recall of the pre-backup marker in D1 found notes.md. Post-new-write: session 2 read later.md, then restore into a fresh D2 exit 0 and fsck D2 exit 0. D2 equals the manifest exactly, against the source: objects 9 vs 15, sidecars 9 vs 15, roots 9 vs 15 lines, delivery leases/acks 10 vs 17. recall of the later marker: D2 found false, source found later.md. fsck of the source exit 0. Restore into the existing D1: exit 1 ('restore destination .qompack already exists'), D1 unchanged. With the source daemon started by a bare session-start hook entry point, `backup create --id live-attempt` exit 1 ('stop the source daemon before maintenance: qompack: daemon lock already held') and `verify` exit 1; no live-attempt directory was left; that daemon then exited on idle. Final: fsck source exit 0, verify exit 0.

- Message wording: every restore response, including the refused one, carries the note 'checkpoint-chain and delivery-seal integrity are NOT covered — run packaged `fsck --seal-check` on the destination'. The same response's integrity report says 'the full dual-reader seal check passed', and docs/backup.md says restore runs the delivery-seal check. The note contradicts the report.
- Observation: the manifest's frontier (0) and snapshot_id ('') are the legacy-import cursor fields (internal/store/backup.go), not a delivery frontier. The backup's frontier identity was therefore proven from the manifest file set and hashes plus the delivery position seals. docs/uat.md's 'verified backup/frontier identity' has no dedicated field.
- Observation: the reader proof itself writes to the destination. `qompack recall` in D1 started a daemon there and stored its own result (D1 objects 9 -> 10); fsck afterwards was still exit 0. This fits troubleshooting's 'some diagnostics write', but checking a recovered project with a reader starts that project's daemon.
- Not established (documented as separate gates): older-release compatibility, activation of a recovered project, cross-version readers.

### C5.6

**Result:** done (numbers only, no budget proposed)

**Evidence:** plans/sdd/V6-closeout/live/resources/C5.6/summary.md and data.json, aggregated from all 29 meta.json and hooks.json files and sessions.tsv. Store growth per session: median 268,003 B, p95 1,757,861 B, max 1,759,477 B, min 209 B (C4.7 off), total 14,320,650 B. Daemon working set, max per session: median 38.9 MiB, max 107.0 MiB (UAT-12 sessionA, the old 0.2.99-prev build). Peak working set is the same. CPU seconds at the last sample were 0.20 to 2.27 per session. Host-seen hook arrival delta (approximate, measured pairs only, p95 nearest-rank), n / median / p95 / max ms: PostToolUse 228 / 52 / 146 / 296; UserPromptSubmit 152 / 49 / 114 / 220 (28 not measured, before init); Stop 179 / 56 / 127 / 237; SubagentStop 37 / 79 / 124 / 153 (1 not measured); SessionStart:compact 35 / 86 / 144 / 8289 (the max is from UAT-08; 1 not measured). SessionStart:startup, resume and fork were never measured: 26, 2 and 1 pairs, all before init. All events: 631 measured, 59 of 690 not measured. sessions.tsv: install 4, sessions 10, retrieval 12, recovery 3; 29 sessions, 2466.0 s wall, 0 non-zero exits, all Haiku.

- No PreCompact or SessionEnd hook_started/hook_response pair appears in any session's stream. For example, UAT-04 has 14 compact boundaries and no PreCompact event. So there is no host-seen latency for those two hooks.
- Outlier: one SessionStart:compact pair of 8289 ms in uat/UAT-08/session.

### C4.11

**Result:** windows/amd64: this lane's host (evidence under the install, sessions, retrieval and recovery parts). linux/amd64 no-model half: unknown. linux/amd64 real sessions: unknown. macOS (darwin/amd64, darwin/arm64) and windows/arm64: unknown (no runner).

**Evidence:** plans/sdd/V6-closeout/live/c4/C4.11/linux-probe.txt and notes.txt. `docker exec qompack-v6-linux-verification true` exited 1 ('container ... is not running'). The brief forbids starting Docker, so no Linux step ran and I did not check whether a Linux claude exists in the container. The frozen linux/amd64 sibling bundle passes static acceptance: target linux/amd64, source.commit d5598eb4, bin/qompack sha256 96d770e3... matches its BUNDLE.json entry. Nothing from it was executed. Real Linux sessions: unknown, because the container has no Claude login and credentials are never copied (D34(c)).

- The Linux verification container exists but is stopped. Getting a Linux no-model result needs the coordinator or owner to start it, then a re-run of this item.

### Defects

- Low (message wording): every `qompack backup restore --json` response, including the refused one, carries the note 'checkpoint-chain and delivery-seal integrity are NOT covered — run packaged `fsck --seal-check` on the destination'. The same response's integrity report says 'the full dual-reader seal check passed', and docs/backup.md says restore runs the delivery-seal check. Evidence: recovery/C1.7/cli/03-restore-before-into-D1.stdout.txt, 08-..., 13-...

### Open issues

- C4.11 Linux: the qompack-v6-linux-verification container is stopped, and this lane may not start Docker. The Linux no-model half (validate --strict, isolated-profile marketplace add/install, plugin list --json) stays unknown until the container is started and the item is re-run. The linux/amd64 bundle has already passed static acceptance.
- C1.7 does not establish rollback compatibility: the restore proofs are same-build only. Older-release compatibility, cross-version readers and activation of a recovered project remain unverified, as docs/backup.md states.
- C5.6: PreCompact and SessionEnd emit no hook events on the host stream, so the host-seen latency for those hooks cannot be measured this way. SessionStart:startup, resume and fork arrive before init and were never measured.
- Observation for the coordinator: the startup accounting LOUD line gives counts only. It does not name the observation id or the object hash (fsck does). This is not a defect under docs/backup.md, but a status user must run fsck to locate the gap.
- The first commit attempt went in with a malformed message (subject and body merged into one line). I amended my own unpushed local commit to fix it, and HEAD is now 78bba162.

Real-home guard ok: True. plans/sdd/V6-closeout/live/guard/recovery.txt. Snap taken at 15:27 before any session or plugin command. homeguard clean then check ran after each of the 3 sessions and after each CLI phase; every check exited 0 with 'real home fingerprint unchanged'. The only INFO was the gained ~/.qompack/bin/c7b2b120.../ staged copy. That copy is the frozen bundle binary (sha256 c7b2b120a6ef...) and was created by session 1. With no qompack.exe running, I removed it using install.md section 6's Remove-Item -Recurse -Force $HOME\.qompack\bin, then removed the empty run-created ~/.qompack. There was no install in this part (--plugin-dir only), so the section 6 host uninstall step does not apply. The final check at 15:39:41 shows settings.json, installed_plugins.json and known_marketplaces.json unchanged, cache 10, marketplaces 2, data 2, .qompack 0 entries: 'real home fingerprint unchanged', check exit 0. guard.json is not committed.

## Independent audit

**Verdict:** needs-fixes

| Item | Status | Evidence |
|---|---|---|
| C4.1 | evidenced | c4/C4.1/ (01-06 cmd/stdout/exit all 0, 07-cache-integrity.txt, 13-profile-deleted.txt, notes.txt). The isolated profile lists 7 hooks, the qompack MCP server and 6 commands (D36 retires /qompack:checkpoint, so the checklist's 'seven commands' is stale). guard/install.txt:1-60 shows every check passing (exit 0). |
| C4.2 | failed | c4/C4.2/notes.txt, uat/UAT-02/c42-counts.json, cli/05-*, cli/09-*. Every hook event fired and the counts match: 9 prompts = 9, 15 captures = 15, 1 session = 1. But fsck exits 1: index.tool_use flags the MCP self-records at turn 0 (turn_order_violations in c42-counts.json), and after idle work index.segments names an unwritten 0002. doctor reports captures.unpublished as degraded. The 'status, doctor, fsck clean' clause fails. |
| C4.3 | failed | c4/C4.3/notes.txt indexes UAT-03/04/05/06. The mechanics work: 14 manifest lines all re-hash (uat/UAT-04/steps/s5-manifest-check.txt), injections stay under 9,500 units, and a Qompack-sourced recovery happened in UAT-06 B (M17). The payload fails D5, though. uat/UAT-04/injected-block-first.txt cuts section 2 mid-word ('reconciliat') with no overflow entry. Corrections never reach the checkpoint or the block (uat/UAT-06/section2-in-order.txt, UAT-05 run1). |
| C4.4 | failed | c4/C4.4/tool-matrix.md; stream lines verified, e.g. session/stream.jsonl:20 and :30. Before the daemon's first compaction, record_eliminated and already_tried return an undocumented 'elimination ledger not present in this build' body. timeline returns turns 0-0 after 12 turns and accepts from > to. The other tools' success and error calls match docs/mcp-tools.md. |
| C4.5 | evidenced | c4/C4.5/commands-vs-docs.md, slash-commands.json, cli/10-help-* and cli/11-*. All six commands ran and match docs/commands.md. qompack resolved on PATH through the plugin's bin/. Findings C45-1 (status banner reads 2 of 9 FAILING) and C45-2 (host error line on a non-zero exit) are recorded. |
| C4.6 | partial | c4/C4.6/notes.txt and c46-scan.json: 0 hits across the source (368 files), the recovery (94), ~/.qompack, QOMPACK_HOME and the streams. Deny and out-of-project refusals hold for Read, recall (MCP, slash, CLI), expand and re_read. Not tested: `why` (notes.txt:48, 'not exercised'), so 'every retrieval form' is not fully evidenced. My own scan of committed evidence, including base64-decoded blobs, found no credential shapes. |
| C4.7 | evidenced | c4/C4.7/notes.txt, off/ and reinj/ sessions. Mode off: hooks exit 0 with {}, no capture, no daemon, and 209 B of store growth (resources/C5.6/summary.md). Reinjection off: checkpoint written, no injected span, and the day log names the switch. Finding F2 is recorded: `qompack mcp` writes .gitignore, logs/ and state/mcp.json into a mode-off project. |
| C4.8 | failed | c4/C4.8/notes.txt and uat/UAT-12/cli. The store is unchanged across the upgrade (07 vs 12 sha256 lists, 95 files, identical) and across the uninstall (40 vs 42, 368 files, identical). Reinstall works. But backup restore exits 1 (cli/30-backup-restore.exit.txt), and fsck of the recovery exits 1 on captures and publication (cli/32, cli/33). |
| C4.9 | evidenced | c4/C4.9/notes.txt, session1/, session2-copy/ and cli/. The session continued through (a) a daemon ended mid-session, (b) settingsVersion 99 and (c) a missing chunk. fsck and status explain each case. F-C49-2 is recorded: a restore fails when an MCP ephemeral root was GC'd (cli/c-00-backup-restore.stdout.txt, 'does not resolve'). |
| C4.10 | evidenced | docs/uat.md Result blocks at lines 195, 296, 402, 512, 631, 727, 827, 921, 1018, 1132, 1245 and 1366 are all filled. The diff d5598eb4..HEAD touches only the 12 Result blocks: 72 removed lines, exactly the 12 x 6 placeholder lines, and no expectation edited. Every block has the D3 'Executed by' text and an evidence directory that exists under uat/UAT-NN/. The recorded results are 6 pass and 6 fail. |
| C4.11 | partial | c4/C4.11/linux-probe.txt: `docker exec` reports the container is not running (exit 1). The linux/amd64 sibling bundle was accepted statically only. windows/amd64 is covered by this lane. Linux, macOS and windows/arm64 are recorded unknown, which the rules allow, but the Linux no-model half is still not done. |
| C1.6 | evidenced | recovery/C1.6/notes.txt:36-39, store/diff-cut-to-after.txt ('COMPARED SET (objects/ + capture sidecars) UNCHANGED'), cli/03 and cli/04 fsck exit 1, cli/00 fsck exit 0, LOUD.log. Automatic startup accounting and fsck detection exist. The notes state plainly that automatic RECOVERY does not exist. |
| C1.7 | partial | recovery/C1.7/cli exit codes: 01-04 = 0, 08/09 = 0, 13/15/15b = 1 (the refusals), 16/17 = 0. D2 matches the manifest exactly (10-D2-vs-backup-vs-source.json). The rehearsal used a store with only Read tool uses, no MCP call and no checkpoint (session1/transcript-facts.json; the restore integrity reads 'no checkpoint has been written yet', CheckpointSealCovered false). On the realistic stores in this same lane, restore fails integrity: UAT-03 probe, C4.8/UAT-12 cli/30 and C4.9 cli/c-00. |
| C5.6 | partial | resources/C5.6/summary.md and data.json cover 29 meta.json and 29 hooks.json files, matching the 29 sessions.tsv rows. Store growth, daemon WS/CPU and hook arrival deltas are measured on the UAT sessions only. The checklist asks for these 'during the live trials', and C5.5 has not run. PreCompact, SessionEnd and SessionStart:startup/resume/fork have no measured latency, and daemon samples are taken only after turns. |

### Audit findings

- **major** `plans/sdd/V6-closeout/live/recovery/C1.7/notes.txt:25-45 (and the recovery part's C1.7 result 'pass: the operator backup, verify and restore path works')` — C1.7 claims the operator backup/verify/restore path works in general, but it was only rehearsed on a store that no ordinary session produces: Reads only, no MCP call, no checkpoint. In this same lane, restore fails integrity on every more realistic store, and C1.7 never cross-references those failures. Evidence: C1.7 session1 and session2 transcript-facts.json show tool_uses {Read:3} and {Read:2}. cli/03 integrity reads checkpoints 'no checkpoint has been written yet' and restore.CheckpointSealCovered false. Restore failures elsewhere in the lane: UAT-03 probe (integrity FAILED on index.segments after a clean idle exit), c4/C4.9/cli/c-00-backup-restore exit 1 ('restored tool reference root f77b00a45902 does not resolve' after an MCP ephemeral GC), and uat/UAT-12/cli/30-backup-restore exit 1. Fix: Restate C1.7 as 'rehearsed and passing on a Read-only, checkpoint-free store'. Cite F-UAT03-2, F-C49-2 and D3 (cross-version) as open blockers to a certified restore after ordinary use (MCP calls, compaction, idle frontier work). Keep the C1.7 checklist item open until a rehearsal on such a store passes.
- **minor** `plans/sdd/V6-closeout/live/guard/install.txt:143-153` — The install part has no guard check after UAT-12 session B, a real session that ran 13:26:10-13:28:05 EDT (uat/UAT-12/sessionB/meta.json started_ms 1790702770345, wall 115.5 s). The brief requires clean + check after EVERY session. The entries jump from 'after marketplace update + plugin update' (13:25:38) to 'after uninstall -s local' (13:32:04). Several plugin CLI steps are also batched under one check (09 marketplace update + 10 plugin update; 11 and 44 plugin list have no check of their own). Evidence: `grep '^==' guard/install.txt` gives lines 143 (13:25:38) and 153 (13:32:04), with nothing between. Session times were converted from the meta.json epochs. Fix: Record the omission in the install part's report. The 13:32:04 check shows only diffs attributable to the open window, so the later record covers the gap, but the lane should say a per-session check was skipped rather than implying one after every session.
- **minor** `docs/uat.md:402-427 (UAT-03 Result) and docs/uat.md:727-745 (UAT-06 Result)` — UAT-03 and UAT-06 list 'Configuration: defaults' as a precondition, but their sessions ran with QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120 (default 1800). Neither Result block mentions the override. UAT-03's step 4 idle exit, and the F-UAT03-2 segment/0002 defect seen after it, both happened under that non-default value. Evidence: uat/UAT-03/session/driver-config.json and uat/UAT-06/session*/driver-config.json env contain QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS '120'. uat/UAT-03/notes.txt:12 and uat/UAT-06/notes.txt:13 record it; the docs/uat.md blocks do not. Fix: Add one line to each Result block naming the non-default idle-exit override and why it was used (the precondition deviation). UAT-02 reproduced F-UAT02-4 under the default 1800 s, so say that too, which confirms the defect does not depend on the override.
- **minor** `docs/uat.md:296-303 (UAT-02 Result)` — UAT-02 is marked 'pass', but the expected-result block calls the non-exact fidelity of the oversized and binary captures 'the point of the row', and neither was observed: big.log was stored exact and no binary fidelity is reachable. The deviation is recorded as a finding, but the headline result does not say that the row's capability is unverified. Evidence: docs/uat.md UAT-02 expected result: 'The oversized and binary captures are the point of the row: each must come back with a non-exact fidelity'. Result line 298: 'Two expected-result fields were NOT observed'. Fix: Headline the result as 'pass on the fail criteria; the row's non-exact-fidelity capability unverified on this host' (or equivalent). The expectation stays unedited.
- **minor** `docs/uat.md:1366-1383 (UAT-12 Result)` — UAT-12 lists 'a binary object decoded as text' as a fail criterion. The block records that the binary file came back as host-decoded text, but only under 'Not as expected', not among the fail reasons (a) and (b). The row fails anyway, so this is a classification gap, not a changed verdict. Evidence: docs/uat.md:1377 'the binary file reached Qompack only as host-decoded text (Bash cat), returned as text'. The UAT-12 fail list includes 'a binary object decoded as text'. Fix: Add (c) to the fail reasons, with the note that the decoding happened in the host before Qompack saw the bytes, so the owner can decide whether it counts against the product.
- **minor** `install part defect D1 ('fsck index.tool_use fails ... on every store that served an MCP call')` — D1 is stated too broadly. C4.9 session 1 served MCP recall calls and fsck still exited 0, because its single MCP record carries session "" and was later GC-tombstoned. The C4.2 notes themselves cite 'C4.9 session1 (fsck clean)'. Evidence: c4/C4.9/cli/s1-post-fsck-json.exit.txt = 0; index.tool_use detail: 'tool_use qompack-mcp:d2300736b8c7 points at root f77b00a45902, which a gc tombstone accounts for'. Fix: Reword D1 to 'on stores where an MCP self-record follows hook records of later turns in the same session', and cite C4.9 s1 as the counter-example.
- **nit** `plans/sdd/V6-closeout/live/guard/sessions.txt:143` — The staged-copy removal command in the sessions guard log contains a literal backspace byte: '$HOME\.qompack<0x08>in' (heredoc '\b'). Commit 1b00e0da fixed the same corruption in retrieval.txt only. Evidence: `od -c` on line 143 shows 'q o m p a c k \b i n'. Fix: Apply the same one-line fix as 1b00e0da to sessions.txt.
- **nit** `plans/sdd/V6-closeout/live/guard/sessions.txt:1` — Unlike the install, retrieval and recovery logs, the sessions guard log has no baseline check right after the snap. Its first entry is after session 1, so its start state is shown only indirectly, by the install part's final check at 13:35:07. Evidence: guard/sessions.txt line 1 is '== session 1 UAT-02/C4.2 (plugin-dir) — 13:45:48'. guard/retrieval.txt:1 and guard/recovery.txt:1 are baseline checks. Fix: Note in the sessions report that the snap was taken without a logged baseline check; run one first in future parts.
- **nit** `install part C4.8 summary ('.qompack/ was byte-identical across the upgrade and across the uninstall (368 files)')` — The file count is off: the upgrade comparison covers 95 files, and 368 is the uninstall comparison. Evidence: uat/UAT-12/cli/07-store-sha256-after-backup.txt and 12-store-sha256-after-upgrade.txt: 95 lines each, identical. 40 and 42: 368 lines each, identical. Fix: Say '95 files across the upgrade, 368 across the uninstall'.
- **nit** `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-live (working tree)` — The worktree has untracked Python bytecode (plans/sdd/V6-closeout/coordinator/__pycache__/) from running homeguard.py and live_driver.py. Evidence: `git status --short` shows '?? plans/sdd/V6-closeout/coordinator/__pycache__/'. Fix: Delete it, or gitignore __pycache__, before the branch is merged, so nobody stages it by accident.
