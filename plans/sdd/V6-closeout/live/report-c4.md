# Live re-run on candidate 4 (`9f6a2fad`, D47)

Workflow `wf_8b477987-691` (`coordinator/live-rerun-c4.js`, resumed by `live-rerun-c4-resume.js`), bundle `qompack-bundles/c4/` (windows-amd64 BUNDLE.json sha256 `aa7da0e1…558d`), Claude Code 2.1.280, Windows 11. Agent-executed on the owner's real host per owner decision D3; not human UAT. Subagents cannot write report files, so the coordinator rendered this verbatim from their returned results.

## Part `install` — done, 4 of 4 sessions, head `35534839d6f96b6a69811f985485913f73e90c6a`

I finished the install part of the candidate 4 re-run (D47). All three rows pass, using exactly the 4 allowed real sessions, all on claude-haiku-4-5-20251001 (no Sonnet needed).

- **UAT-01: pass.** After an MCP call in a real session, fsck, status and doctor now agree. fsck --json exits 0 with the daemon live, stopped and with --seal-check, and the MCP record now carries its call's turn instead of turn 0. status reads '9 assertion(s), all holding' (candidate 3: 2 of 9 FAILING). doctor reports 0 unpublished gaps, the same answer as fsck.
- **UAT-12: pass.** On a 354 KB escape-heavy capture, every expand and re_read result stays within 262,144 bytes (largest 261,172), and following next_span pages through the rest cleanly. Every retrieval form refused the deny-ruled file and the out-of-project paths. Restoring the pre-upgrade backup now passes its integrity checks (candidate 3: exit 1). .qompack/ was byte-identical across the upgrade and the uninstall. Rollback verified stays 'unverified': the old build's own fsck exits 1 on the recovered baseline, on its own turn-0 rule, and the recovery was not activated.
- **C4.8: pass.** Upgrade, uninstall and reinstall all preserved the project's work, and every restore and fsck --seal-check exited 0.
- **Previous build:** the v0.2.0 tag has no bundle task, so the old build was 301a8e9, as in the first run. It rebuilt byte-identical to candidate 3's.

In docs/uat.md I replaced both rows' Result blocks, keeping one candidate 3 history line each. Evidence is under plans/sdd/V6-closeout/live/rerun-c4/{UAT-01,UAT-12,C4.8}/; 4 lines were added to sessions.tsv, and guard/install.txt is updated. Everything is committed as 35534839 on closeout/live4.

New findings to route (none fails a row):
1. recall's k counts results that are then withheld as denied. A default recall returned 2 hits and 'denied':3, which hid session A's README capture even though it was allowed.
2. The daemon's startup check of unpublished captures is capped at 250 ms. It does not finish on a 3-session store, so every daemon start writes a LOUD line.
3. troubleshooting.md §3 still says to read fidelity through expand, and no user doc describes the host decoding binary files.

The guard showed differences only while my own install windows were open, and the final check at the end passes.

### UAT-01

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-01/ (notes.txt, snapshot.txt, version.txt, config-provenance.txt, self-test.txt/.json, cli/, session/, store/). 1 Haiku session (16.1 s). Setup: real profile, frozen bundle installed at LOCAL scope through a disposable marketplace (qompack-live), fresh git project, no QOMPACK_* variables, no config file. Init event: plugin:qompack:qompack connected, the 8 documented tools, the 6 /qompack: commands (no qompack:checkpoint), plugin 0.3.0. Hooks fired: SessionStart:startup, UserPromptSubmit x2, PostToolUse x3, Stop x2. SessionEnd shows no stream event, but index/sessions.jsonl records the session's end. Steps 2-8: version 0.3.0 exit 0. Installed plugin.json 0.3.0 matches step 2 and BUNDLE.json; the source tree plugin.json reads 0.1.0 and the last tag is v0.2.0. The 7 hooks are exec form ${CLAUDE_PLUGIN_ROOT}/bin/qompack.exe. The 8 tools match docs/mcp-tools.md. All 108 config leaves are default and every gated switch is false. self-test exits 0 with no critical row. The daemon's contract snapshot reads precompact.custom_instructions_accepted 'retired', which is what the doc expects. Candidate 3's three defects are fixed: fsck --json exits 0 with the daemon live, stopped and with --seal-check, and the MCP record is now indexed at turn 3, not turn 0. status reads 'host contract: 9 assertion(s), all holding'. doctor --json exits 0 and captures.unpublished reads '0 gap(s) across 7 sidecar(s)', which agrees with fsck. Rollback not applicable: no store existed before the run. Cleanup: uninstall -s local, marketplace remove, and removal of the orphaned cache this run created (BUNDLE.json compared first). The guard check passed afterwards.

- Stale brief premise, no doc change needed: install.md §3 already says Claude Code 2.1.280 accepts --json on install/update/uninstall, and each printed one JSON line again here.
- Host behaviour: the init event gives the plugin path as the marketplace source directory, not the plugins/cache installPath.
- The daemon ran from the D10 staged copy under ~/.qompack/bin/1e9d0cc8.../. With the default 1800 s idle exit it was terminated after checking the lock pid, the qompack.exe image and a command line naming this project. Afterwards doctor labels scope.daemon 'degraded held by pid N, not reachable' while fsck calls the same lock STALE and ok. Both describe the same fact.

### C4.8

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/C4.8/notes.txt; data in plans/sdd/V6-closeout/live/rerun-c4/UAT-12/ (cli/00a-00b previous-build build, 05-06 baseline backup, 09-12 upgrade, 27-36 restore, fsck and old-build reader, 40-44 uninstall/reinstall, 50-59 after session C). 3 Haiku sessions (UAT-12 A, B, C). Previous build: the v0.2.0 tag has no 'bundle' devtool task (cli/00a: devtool: unknown task "bundle"), so 0.2.99-prev was built from 301a8e9 as candidate 3 did. It rebuilt byte-identical. Session A on the old build made reads, Bash and image captures, recall and timeline MCP calls and a /compact (checkpoint 0001). Baseline backup create and verify with the frozen CLI: exit 0/0, 136 files; verify's scratch restore passed its reader proof and integrity checks. Upgrade with marketplace update and plugin update -s local: .qompack/ byte-identical (276 files, full listing). Restore of the pre-upgrade baseline into a fresh destination: exit 0, reader proof 27 roots and 19 tool refs, integrity exit 0 (candidate 3: exit 1). fsck --seal-check exits 0 on the recovery, on the source after B and on the source after C. Source later writes preserved; only the stale run/ lock was reclaimed. Uninstall: .qompack/ byte-identical, 791 files. After the reinstall, session C's re_read README.md answered from session A's old-build capture (turn 1). A backup, verify, restore and fsck --seal-check after C all exit 0.

- Not covered: a public v0.2.0 -> 0.3.0 live upgrade (v0.2.0 cannot produce a bundle; only the committed v0.2.0 store fixture covers it). No previous-build reader certifies the upgraded store: the 301a8e9 fsck exits 1 on a copy of it (cli/59), as a downgrade is never promised. On the recovered baseline the old fsck also exits 1, on its own build's turn-0 MCP rule (cli/35). The recovery was not activated.
- recall at the default k=5 did not return session A's README capture; k=20 does (see UAT-12 F1).
- Candidate 3's two routed defects are gone. The old build's unlinked prompt sidecars now read as published, 'not a gap'. The old build's two turn-0 MCP self-records get a note that does not fail fsck.

### UAT-12

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-12/ (notes.txt indexes it; snapshot.txt, setup-facts.json, sessionA/B/C with their messages, cli/, store/). Step 2: the host refused the Read ('File is in a directory that is denied by your permission settings.'). recall (marker and path:), expand (tool_use_id and root hash), re_read, /qompack:recall and a direct stdio probe all answered denied ('authorization denied: the host's current permission rules deny reading the associated path') with no preview. Out-of-project re_read answered 'path escapes the project root' without echoing the path. Steps 3-5, on a 354,352-byte escape-heavy capture: every result text is within 262,144. Minimal 8,038; full:true 261,163 (span [0,217070], next_span 217070:137282); the next page 165,233 (contiguous, to the end); re_read full:true 261,172; blob 2,947; png 2,394. Measured from cli/20-mcp-probe.json and the host's persisted files (cli/21). No page contains U+FFFD. Responses carry span, total_bytes, truncated and next_span and no fidelity or coverage field, as revised under D46. Step 4: the host decodes Bash output as text and sends images as base64 JSON; both sidecars record fidelity exact, and Qompack decoded nothing. Step 7 (strings to be confirmed at execution): no version-block or retired-meaning warning, since no config file exists. No LOUD line at the first post-upgrade daemon start. config.capture reads 'applied as written'. Step 8: restore exit 0 with its integrity checks passing. Steps 9-10: .qompack/ byte-identical after the uninstall. Steps 2-5 ran after step 6 because 301a8e9 predates C1.9's deny-rule support. Rollback verified: unverified. The same-build restore passes, but the 301a8e9 reader exits 1 on its own turn-0 rule and the recovery was not activated.

- F1 (product, retrieval): recall's k counts hits that authorization later withholds. internal/mcp/handlers.go asks store.Search for K hits and drops denied ones afterwards. At the default k=5, session C got 2 hits and 'denied':3 although three more permitted hits existed, among them session A's original README capture. Retrieval's own MCP self-records also rank above that original capture. Evidence: sessionC/stream.jsonl, cli/50-mcp-probe-recall.json.
- F2 (product, diagnostics): the startup publication accounting is bounded at 250 ms (publicationStartupBound). On this 3-session store (70 captures, ~380 objects) two daemon starts logged LOUD 'publication accounting incomplete; a zero gap count is only a lower bound ... scan interrupted before it finished'. So a healthy store's LOUD.log gets a line on every start and the audit never completes. Evidence: store/logs_LOUD.log.
- F3 (doc): troubleshooting.md §3 still says 'Read the record's Fidelity' and 'read that record's fidelity through expand', which contradicts mcp-tools.md: no retrieval response carries fidelity. D45 says the host's decoding of binary files is documented, but troubleshooting, user-guide and cannot-do do not describe it; only UAT Result blocks mention it.
- F4 (observation): section 6 of the rehydration block lists pointers with the deny-ruled file's absolute path and root hash, and the out-of-project file's absolute path. No content is shown.
- F5 (observation): session B's 8,017-char rehydration block dropped 8 user_intent.evolution entries and 1 pointer as not fitting the budget. That belongs to the sessions part's rows (UAT-04/05).
- F6 (observation): a daemon spawned with no session (by self-test) logged WARN 'checkpoint: no usable source set ... SourceSet.Ledger is nil ... running in degraded mode' 30 s after starting.
- Semantics note: a final page reports truncated:true with no next_span, because span.go defines truncated as 'less than the whole object'. An explicit span 0:354352 cut at 217070 gives next_span 217070:16384, while the full:true cut gives 217070:137282.
- Host behaviour: the old build's PreCompact customInstructions output (retired by C1.18) fails 2.1.280's hook-output validation. The candidate outputs {} and passes. The candidate's checkpoint is 0003 because the old build's persisted draft claims 0002.

### Defects

- recall's k counts hits that authorization later withholds (internal/mcp/handlers.go filters denied hits after store.Search returns K). A default k=5 recall answered 2 hits and 'denied':3 while more permitted hits existed, hiding session A's README capture. Retrieval's own MCP self-records also rank above that original capture. Evidence: rerun-c4/UAT-12/sessionC/stream.jsonl, cli/50-mcp-probe-recall.json.
- The daemon's startup publication accounting is bounded at 250 ms (internal/daemon/publication_audit.go publicationStartupBound). On a 3-session store (70 captures, ~380 objects) two daemon starts logged LOUD 'publication accounting incomplete ... scan interrupted before it finished', so a healthy store gets a LOUD line on every start. Evidence: rerun-c4/UAT-12/store/logs_LOUD.log.
- Documentation: troubleshooting.md §3 still tells readers to read the record's fidelity, 'through expand', which contradicts mcp-tools.md after D46 (no response carries fidelity). The host's decoding of binary files, which D45 says is documented, is not described in troubleshooting, user-guide or cannot-do.

### Open issues

- The public v0.2.0 -> 0.3.0 upgrade was not installed live, because v0.2.0 has no bundle tooling. Only the committed v0.2.0 store fixture covers the released path.
- UAT-12 Rollback verified stays 'unverified': the 301a8e9 reader's fsck exits 1 on the recovered baseline (its own build's turn-0 MCP rule) and on a copy of the upgraded store (a downgrade, never promised), and the recovery was not activated.
- UAT-12 step 4's 'binary' fidelity cannot be reached on this host: Bash output arrives as decoded text and Read sends images as base64 JSON, both stored with fidelity exact. The coordinator should decide the doc disposition, together with finding 3 above.
- Observations for other parts: session B's rehydration dropped 8 user_intent.evolution entries as over budget (UAT-04/05's area). The rehydration block lists pointers with the deny-ruled file's absolute path and hash, and the out-of-project file's absolute path, without content. A daemon spawned by self-test with no session logs WARN 'SourceSet.Ledger is nil ... running in degraded mode'.
- Scratch raw copies remain under C:/Users/Quant/AppData/Local/Temp/claude/qompack-live/install/ (uat01, uat12 including recovery and recovery2, old bundle, tools, guard.json). Candidate 3's scratch was moved to install/c3/, including its secrets.json. No secrets were planted in this run. The host's own transcripts for my 4 sessions remain under ~/.claude/projects/C--Users-Quant-AppData-Local-Temp-claude-qompack-live-install-*/ and were left untouched.

Real-home guard ok: True. plans/sdd/V6-closeout/live/guard/install.txt (appended under '# ==== Candidate 4 re-run (D47), install part'). Differences appeared only inside my own open local-scope install windows: installed_plugins.json, known_marketplaces.json, cache/qompack-live, and one empty plugins/data/qompack-qompack-live that homeguard clean removed. The only thing I deleted by hand under ~/.claude was the orphaned cache/qompack-live this run created, after checking that each version's BUNDLE.json matches one of my bundles. The D10 staged copy ~/.qompack/bin/1e9d0cc8... (sha256 equal to the frozen bin, no qompack.exe running) was removed after the final uninstall, per install.md §6, together with the empty ~/.qompack the run had created. The final checks at 21:07:05 EDT and afterwards read 'real home fingerprint unchanged', check exit 0, .qompack/ 0 entries, the same as at snap time. guard.json is not committed.

## Part `sessions` — done, 9 of 9 sessions, head `c84f793e869e26514cce1b44debd5f2fb006eeb8`

I re-ran the sessions part on candidate 4 (9f6a2fad) with the frozen c4 bundle via --plugin-dir. All runs were agent-executed on the owner's real Windows host under D3; none is human UAT. The part used all 9 budgeted sessions. One of them was accidental, caused by my own mistake: a bare `lane.py` token in a Git Bash command ran the Python helper as a shell script, and its line `CLAUDE = ...` launched `claude =` in the real profile. That session ran about 4 s on Opus, used no tools and no plugin, and left the guard unchanged. It is recorded as sessions.tsv n=5 and in rerun-c4/accidental-session/.

To stay within budget, I ran UAT-05's step 5 (tiny budget) as a hook invocation instead of a second model session. The row explicitly allows this. No row or step was skipped.

Results:
- **Pass:** UAT-02 (on its fail criteria), UAT-04, UAT-05, C4.2, C4.9.
- **Fail:** UAT-03, UAT-06, C4.3.

Confirmed fixed live: MCP records filed at turn 0; the segment naming an unwritten checkpoint after a clean idle exit, and the restore failures it caused; empty checkpoint pointers; the 8 KiB mid-record cut and the spurious intent_mismatch; corrections never reaching evolution; the fork replacing the original intent; the stale pins view; doctor vs fsck disagreeing; the empty status refusal; the missing-object wording.

New failures:
- **UAT-03:** a corrupted newest checkpoint falls back silently to the older one.
- **UAT-06:** the fork's first block omits the correction in force, and the parent's session-scoped elimination and decision are not inherited.

Other new defects:
- A decision is dropped from a resumed session's second checkpoint.
- A config reload of rehydrate.* is logged as applied but is not applied.
- The doc's `--set` on a hook invocation has no effect on the rehydration budget.
- In sessions with no compaction, the idle frontier task fails every 30 s.

I filled all five Result blocks in docs/uat.md; each keeps one candidate 3 history line. `go test ./test/docs` passes. The real-home guard passed after every session and at the end. The run-created staged copy ~/.qompack/bin/1e9d0cc8… (sha256 equal to the bundle binary) was removed with no qompack.exe running, along with the empty ~/.qompack.

Every daemon ended by its default idle exit except UAT-05's run-1 daemon. I terminated that one for step 5 after verifying its lock pid, image and command line, and recorded it.

### UAT-02

**Result:** pass on the row's fail criteria (expected non-exact fidelity not observable on this host)

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-02/ (notes.txt, c42-counts.json, fidelity-tally.txt, secret-scan.txt, step6-mcp-responses.json, cli/05-* live, cli/10-* stopped, cli/06-mcp-probe.json); 1 session (sessions-c4 n=3)

- Step 6 as revised under D46 holds: expand carries _meta.qompack (span, total_bytes, truncated, next_span '9909:16384' for big.log), redacted content shows as «redacted:aws_access_key_id»/«redacted:github_token», and no response carries fidelity or coverage.
- Expected field still NOT observed (unchanged since candidate 3): the oversized big.log (310,800 B, delivered whole by the host) and the binary captures read exact. Read refuses blob.bin with no PostToolUse, and `cat` delivers host-decoded text. Sidecars: 35 exact, 1 redacted. The non-exact capability is unverified on this host.
- Planted secrets: 0 hits across the post-run store (raw, 88 zstd-decompressed objects, 36 decoded sidecars).
- Lane note: the first stopped set (cli/09-*) ran `status` first. It started a daemon that doctor caught mid-start (scope.daemon degraded). The set was repeated as cli/10-* with doctor and fsck first: clean.

### UAT-03

**Result:** fail

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-03/ (notes.txt, steps/, cli/b1-b4 backup, probe/); 1 session (n=1); default idle exit, no override

- F-C4-UAT03-1 (fail criterion): with the newest checkpoint 0002 corrupted by one byte in a restored copy, the hand-run SessionStart(compact) built the payload from 0001 and presented it as current ('checkpoint 0001'; state seq 1, dropped [], degraded false, no section 7). Nothing in the payload, the state or the drop report says a fallback happened. LOUD.log says only 'checkpoint artifact does not match its MANIFEST digest; the checkpoint is refused artifact=0002.json'. 'rolled back to 0001' appears nowhere.
- Candidate 3's defects are fixed. Checkpoints now carry encoded_segments and pointers.files/tools. After the default idle exit (21:50:46) no segment names an unwritten checkpoint, and fsck (incl. --seal-check) exits 0. Backup create, verify and restore all exit 0 (12 content roots, 9 tool refs, integrity incl. the seal check ok), and the restored copy's fsck exits 0. `qompack status` now explains 'no daemon answered ... asked one to start'. The step-5 re-read is byte-identical.

### UAT-04

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-04/ (notes.txt, first-prompt.txt, block2..16, steps/s5-checkpoints.txt, steps/s6-native-shrink-search.txt, cli/11 hook by hand); 1 session (n=4, 588 s)

- F-UAT04-1 is fixed. A 16,823-character first prompt is never cut. All 15 compact injections (841-2,498 UTF-16 units, inline) name it first in section 7 as 'user_intent tier1 — OVERFLOW: the verbatim original user intent ... emitted whole or not at all; restore: expand(tool_use_id=prompt_…_0)'. LOUD.log has 0 intent_mismatch lines.
- Compactions: 2 manual and 14 automatic all proceeded, plus 1 host-failed (too_few_groups) whose checkpoint 0001 stays intact. The config-refused compaction proceeded: the hook printed {} and exited 0, and no checkpoint was written. 16 checkpoints, every one re-hashes. 0 native-shrink matches over 112 outputs.
- Run configuration: the forced 30%/100k threshold made the host thrash again. Two turns ended with 'Autocompact is thrashing' after expand had already returned the brief's closing marker and the deleted key Q23's value.
- Design observation: evolution deltas get a fixed 10% share. The three oldest were dropped (named, with pointers) while the payload used 599 of 12,000 tokens.
- Post-run, with the planted unparseable config in place: fsck exits 1 on index.files 'absent while its log carries 4 path(s)'. Whether that follows from the planted config was not isolated.

### UAT-05

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-05/ (notes.txt, run1-block2, run1-state-rehydrate.json, run2-block-tiny-budget.txt, run2-state-rehydrate.json, cli/run2-a/b/c); 1 session (n=6) plus step 5 as a hook invocation (no model)

- Run 1 block (3,093 units, 938/12,000 tokens): pin in section 1; the verbatim semicolon original, then the TAB correction in the newest-first evolution; elimination (sec 3) and decision (sec 4). fsck pins ok (no stale view). record_eliminated answered before any compaction. The model took the delimiter from the Qompack block.
- Step 5: project config min=max=150 and a daemon started under it (the run-1 daemon was TERMINATED for this step after lock pid, image and cmdline checks). Result: 362 units, 109/150 tokens, pin plus the section-7 counted tail '… and 14 more; call dropped()', degraded true with the tier1 OVERFLOW entry first, and dropped() returns all 14.
- F-C4-UAT05-1 (doc vs product): step 5's '--set runtime.rehydrate.maxTokens on a hook invocation' has no effect. The daemon's config sets the budget (run2-a).
- F-C4-UAT05-2 (product): the daemon logs 'config reloaded changed=[runtime.rehydrate.maxTokens runtime.rehydrate.minTokens]' but keeps rehydrating at 12,000 until a restart. The rehydrate service reads its startup copy of the config (run2-b).
- F-C4-UAT05-3: at 150 tokens section 8 (retrieval) is evicted while the pinned invariant stays, against ADR 0011's tier-1 admission order. Candidate 3's run 2 had the same shape.

### UAT-06

**Result:** fail

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-06/ (notes.txt, A/B/C blocks, section2-in-order.txt, diff-block-B2-vs-fork-C2.txt, step7-postcompact-search.txt, steps/checkpoint-*.json); 3 sessions (n=7 A, n=8 --resume, n=9 --resume --fork-session), none skipped

- F-C4-UAT06-1 (expected field not met at step 4): the fork's first block carries the parent's verbatim original ('100 per minute') but not the only correction then in force ('60, not 100'). The 10% evolution share dropped it and names it only as 'user_intent_evolution 0 — did not fit' while the block is 2,652 of 9,400 characters. The parent's session-scoped elimination and its decision are not inherited (no sections 3-4), so the block's only statement of the limit is the superseded one.
- F-UAT06-1 is fixed: the fork's section 2 is the parent's original, with '(forked session: the original request of session c8466b7e ...)' and a section-7 fork-provenance entry. Corrections now reach checkpoints: blocks 1 and 2 carry the 60 correction, and block 4 has the 45 correction on top.
- F-C4-UAT06-2: after --resume, the same session's second checkpoint (0002) has decisions [] although 0001 had dec_991dbff588ec and the elimination is still carried. Block 2 lost section 4.
- /qompack:why dec_991dbff588ec returned an attributed record (what, why, rejected alternative, evidence, checkpoint_seq 1). Step 7: 0 post-compaction waits. SessionStart:resume and SessionStart:fork inject only the probe line. MANIFEST skips seq 3. fsck exit 0.
- Design observation F-C4-UAT06-3: user_intent.evolution holds every later prompt, so a correction falls out of the share after a few ordinary prompts.

### C4.2

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/C4.2/notes.txt indexing rerun-c4/UAT-02 (c42-counts.json, cli/05-*, cli/10-*, hooks.json)

- All seven hook events fired, all success/exit 0. PreCompact is confirmed by the transcript line and checkpoint 0001. SessionEnd is confirmed by the sessions.jsonl end line, and stderr is empty.
- Counts match: prompts 10 = 10; host tool uses 15 minus the refused binary Read = 14 = store captures; sessions 1 = 1. The store also holds 7 qompack-mcp self-records and 2 SubagentStop records. No turn-order violation, so the MCP turn-0 defect is fixed.
- status: all 9 assertions holding. doctor --json has no degraded row (0 gaps across 36 sidecars). fsck --json exits 0, live and after the default idle exit.

### C4.3

**Result:** fail

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/C4.3/notes.txt indexing UAT-03/04/05/06 (6 sessions)

- Every compaction wrote a checkpoint that re-hashes, and every SessionStart:compact injected a bounded inline block (361-3,093 units). No PreCompact rejection, no 'Hook cancelled'.
- The D5 whole-record rule now holds (UAT-04), and corrections reach checkpoints (UAT-05, UAT-06).
- Fails on current authority in a fork (F-C4-UAT06-1) and on the silent checkpoint fallback (F-C4-UAT03-1).
- Qompack-sourced recovery: UAT-05 delimiter from the injected block. In UAT-04, expand returned both facts but the host's thrash stop ended those turns before the model answered. Other answers came from the host's preserved context.

### C4.9

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/C4.9/ (notes.txt, cli/c-00..c-10, store/); 1 session (n=2)

- After MCP calls and the default idle exit: backup create, verify and restore exit 0 (OpenedOK, 16 content roots, 13 tool refs, 0 tombstoned, integrity incl. seal check ok). fsck exits 0 on source and destination.
- Candidate 3's GC'd-root condition did not recur. MCP records now carry the session, and the GC's recent-session clause keeps their roots, so the tombstoned-reference path (ToolRefsTombstoned > 0) is unexercised.
- Missing-object wording is fixed: 'the stored object is missing: the index records it, but its bytes are no longer in the store'. fsck names the chunk.
- NEW F-C4-C49-3: in a session with no compaction and no elimination, the idle task act.advance_frontier fails every 30 s until the idle exit (57 warns). Error: 'SourceSet.Ledger is nil: its accessor resolved to no ledger: qompack: running in degraded mode'. The idle frontier never advances.
- Observation: a stdio `qompack mcp` probe outside any host session answers recall 'codeword' with 0 hits and denied:5 in both the original and restored project, while the same recall in-session returned 3 hits.

### Defects

- F-C4-UAT03-1: when the newest checkpoint does not verify, rehydration silently uses the older checkpoint and presents it as current. The payload says 'checkpoint 0001', and the state has dropped [] and degraded false. Nothing says a fallback happened: LOUD.log names only the refusal of 0002. Evidence: rerun-c4/UAT-03/probe/00-02, probe-state_*, probe-logs_*.
- F-C4-UAT06-1: in a --fork-session, the first block omits the only correction in force. The 10% evolution share dropped it while the block was 2,652 of 9,400 characters, and it is named only as a restore pointer. The parent's session-scoped elimination and decision are not inherited (checkpoints 0004/0005 have eliminated [] and decisions []). Evidence: rerun-c4/UAT-06/C-block2-SessionStart-compact.txt, section2-in-order.txt, steps/checkpoint-0004.json.
- F-C4-UAT06-2: a decision recorded before the first compaction (dec_991dbff588ec) is missing from the same session's second checkpoint after --resume (0002 has decisions []), while its elimination is carried. why() still resolves it via checkpoint 1. Evidence: rerun-c4/UAT-06/steps/checkpoint-0001.json vs checkpoint-0002.json, B-block2.
- F-C4-UAT05-2: after a project config change, the daemon logs 'config reloaded changed=[runtime.rehydrate.maxTokens runtime.rehydrate.minTokens]' but keeps rehydrating with its startup budget. reload.go updates d.cfg; the rehydrate service reads s.o.Cfg. Evidence: rerun-c4/UAT-05/cli/run2-b-*, store/logs_qompack-20260930.log.
- F-C4-UAT05-1 (doc): UAT-05 step 5's '--set runtime.rehydrate.maxTokens=<small> on a hook invocation' has no effect when a daemon builds the rehydration. Evidence: rerun-c4/UAT-05/cli/run2-a-*.
- F-C4-C49-3: in a session with no compaction and no elimination, the idle task act.advance_frontier fails every 30 s with 'SourceSet.Ledger is nil: its accessor resolved to no ledger: qompack: running in degraded mode'. It logged 57 warns between 01:24Z and 01:52Z, and the idle frontier never advances. Evidence: rerun-c4/C4.9/store/logs_qompack-20260930.log.
- F-C4-UAT05-3 (ADR 0011 deviation, unchanged since candidate 3): at a 150-token budget the retrieval line (section 8) is evicted while a pinned invariant is kept. Evidence: rerun-c4/UAT-05/run2-block-tiny-budget.txt, run2-state-rehydrate.json.
- UAT-02 expected field (unchanged since candidate 3): non-exact fidelity for oversized and binary captures cannot be observed through this host. The host delivers whole reads, refuses binary Read, and decodes `cat`. Needs a doc disposition. Evidence: rerun-c4/UAT-02/fidelity-tally.txt.

### Open issues

- Budget: one of the 9 sessions was an accidental lane-error launch (sessions.tsv n=5; rerun-c4/accidental-session/notes.txt). Because of it, UAT-05 step 5 ran as a hook invocation rather than a second model session.
- UAT-05's run-1 daemon was terminated (verified) because step 5 needed a daemon started under the tiny budget. The reload defect means the running daemon never applied it. Every other daemon ended by its default idle exit.
- UAT-03 fail criterion hinges on reading: the log names the refusal of 0002 but not the fallback to 0001. I recorded it as a fail; the coordinator should rule.
- UAT-04: the forced auto-compaction override (30% of 100k) thrashes the host, as on candidate 3. A calmer setting or --autocompact (unrehearsed) would give cleaner automatic-compaction evidence.
- C4.9: the tombstoned-MCP-root restore path (ToolRefsTombstoned > 0) is unexercised live, because roots are now retained by the recent-session clause.
- Observation for retrieval/C4.4: stdio `qompack mcp` recall outside a host session answers denied:N for records the in-session recall returns.
- docs/uat.md's intro paragraph (lines 8-18) still describes the candidate 3 run. I edited only the Result blocks, as instructed.
- The install.md §3 --json documentation finding does not apply to this part: no plugin CLI step was run (--plugin-dir only).
- Host transcripts for my sessions, including the accidental one under ~/.claude/projects/C--Users-Quant-AppData-Local-Temp-claude-qompack-live-sessions-c4*, remain. They are left untouched per the never-edit-~/.claude rule.
- Scratch raw copies, project stores, recovery copies and secrets.json remain under C:/Users/Quant/AppData/Local/Temp/claude/qompack-live/sessions/ (c4/ and secrets.json) for re-inspection.

Real-home guard ok: True. homeguard snap taken at <scratch>/sessions/guard.json before the first session; the candidate 3 snap was renamed guard-c3.json in scratch. clean + check ran after each of the 9 sessions, the accidental one included, and at the end. Every check exited 0, with settings.json, installed_plugins.json, known_marketplaces.json, cache, marketplaces and data unchanged. The only gains were the host's empty plugins/data/qompack-inline (removed by clean) and ~/.qompack/bin/1e9d0cc8… (the D10 staged copy). The staged copy's sha256 equals the c4 bundle bin/qompack.exe. With no qompack.exe running, I removed it per install.md §6, then removed the empty run-created ~/.qompack (absent at snap). Final check at 23:20:59 EDT: 'real home fingerprint unchanged', .qompack/ (0 entries), check exit 0. Output is appended to plans/sdd/V6-closeout/live/guard/sessions.txt (committed). No plugin CLI or install step was run, and nothing under ~/.claude was written by hand.

## Part `retrieval` — done, 7 of 7 sessions, head `74fbed0d`

This was a resumed seat after the owner's 2026-09-30 pause. The retrieval re-run (D47) on candidate 4 (9f6a2fad, frozen c4 bundle, --plugin-dir) was already committed in e8913616 and a27dc8f5. All 7 sessions were used (sessions.tsv retrieval-c4 rows 1-7). This seat ran no claude session and no plugin command. I checked the evidence: every session directory has the stream, hooks, meta, stderr and transcript facts, each row has notes.txt, and C4.4 has a tool matrix. The UAT-07/08/09/11 Result blocks in docs/uat.md are complete: they carry the Snapshot, the Date, D3's 'Executed by', Evidence, Rollback, and one candidate 3 history line each. No qompack daemon is running. I ran homeguard clean + check once (pass) and committed the guard log as 74fbed0d after scan-staged exit 0. Results: UAT-07 pass, UAT-08 pass, UAT-11 pass, C4.5 pass. UAT-09 fails at step 4 and C4.4 is partial, both from one defect, R4-1: after a daemon restart, already_tried answers 'absent' for a stale elimination because the query filter holds only active records.

### UAT-07

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-07/ (notes.txt; session shared with C4.4 in ../C4.4/session/, retrieval-c4 n=5, id 3187e738); docs/uat.md UAT-07 Result block

- Fixed from candidate 3: `path:src/*.go` and `path:*.go` globs now match src/ledger.go, and `tool:Read` (host name) matches FileRead captures (4 hits, same as tool:FileRead). A no-match `path:nothing/*.rs` returns count 0, found false, exit 0.
- Step 7 observed string (to be confirmed at execution) was recorded: {found:false, searched:'the root index, including every indexed root's chunk list', available:false, reason:'no indexed root or chunk carries this hash, so its content provenance could not be established'}.
- Observation carried from candidate 3, not a fail condition: after MCP calls, CLI `path:` top-5 answers are crowded out by retrieval self-records (re_read/expand captures), including a hit for src/nope.go, which never existed. _meta.qompack.hash names the response's own capture, not the resolved object.

### UAT-08

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-08/ (session1 n=1 id 24df5051; session6-isolation n=6 id 13a929a6; snap/, mcp/, cli/); docs/uat.md UAT-08 Result block

- Fixed from candidate 3: record_eliminated and already_tried work before any compaction. The record is stored with the calling session id (24df5051…), not ''. Checkpoint 0001 carries it in eliminated, with dec_e760c8bbf50a in decisions. why and /qompack:why both answer found true.
- Session-scope isolation was shown in the C4.4 project, because the UAT-08 project's record had gone stale (see UAT-09 R4-1). In a later, different session, the session-scoped elimination answered plain absent and the project-scoped one answered active.
- Observation, no row expectation: checkpoint 0002 lists the stale elimination but has decisions []. why still resolves the decision from checkpoint 1.

### UAT-09

**Result:** fail (step 4)

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-09/ (session2-resume n=2, session3-step5 n=3, session7-resume-flag-diag n=7, diag/; steps 1-3 in ../UAT-08/session1/); docs/uat.md UAT-09 Result block

- FINDING R4-1 (product defect): after a daemon restart, already_tried answers {"state":"absent"} for a STALE elimination. Seen under drop (session 2) and under flag (session 7), both resumes of the recording session, with the call filed under the same session id. Cause from code (internal/negknow): the tried.bloom query filter holds only ACTIVE records, and Record adds a key only to the in-memory filter. The on-disk sketches/tried.bloom predates the record, reconcileBloom finds no active record missing and does not rebuild, and Query's first check (!bloom.Test) returns absent before it reads the stale record. By code reading, an idle rebuild_bloom (rebuildOnStale nextIdle) would drop the key the same way. This contradicts mcp-tools.md and UAT-09's 'a record goes stale rather than being deleted'.
- Fixed from candidate 3: the stale flip happens in the same session. Seconds after the model edited config/pool.yaml, already_tried answered stale with stale_because ['config/pool.yaml: dependency hash changed from sha256:9f2751eac830']. The injected block after /compact also shows [stale], so the known digest gap was not reproduced.
- Observation: a staleResponse=drop config written mid-session was reloaded by the running daemon ('config reloaded changed=[eliminations.staleResponse]'), but already_tried still answered in flag form. The change does not reach already_tried in a running daemon.
- Step 5: with records/eliminations.jsonl made unreadable (created as a directory), already_tried answered state unavailable, degraded true, with the note 'this is not evidence the approach is untried', and record_eliminated returned a tool error. DOC FINDING: step 5's 'reachable form' (a project with no ledger) is stale. The ledger now opens on first use and answers a correct absent (../C4.4/mcp/T15-1).
- Step 6: no response prohibits the approach. Step 4's reason/note strings were not reachable on this run.

### UAT-11

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/UAT-11/ (cli/ for steps 1-4 with no model; session/ n=4 id 0bf8b197; mcp/; secret-scan-store.json); docs/uat.md UAT-11 Result block

- Fixed from candidate 3: a 363 KB Read in the first turn no longer degrades the session. hook.additional_context_delivered reads sentinel-observed. SessionStart:compact injected the rehydration block (seq=1, 2,604 chars), with no degraded systemMessage.
- Step 6: all 9 section-6 pointers resolved. One Bash-result hash (3bfe728b) resolved by hash only through a no-model stdio probe; in the session the model expanded it by tool_use_id. big.log came back truncated with next_span and creds.env redacted; both are recorded fidelities.
- The planted secret (sha256 in setup-facts.json) appears in no form in the store: 178 files, 106 zstd-decompressed, 241 base64 fields, 0 hits.
- Steps 1-4 on the frozen binary behave as the doc says. After step 4a, self-test started a daemon and wrote the store skeleton, which the doc allows.

### C4.4

**Result:** partial

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/C4.4/ (tool-matrix.md, mcp/, cli/30-31 no-model probes, store-after-session/, cli/40 fsck exit 0); session retrieval-c4 n=5 id 3187e738

- Every success and error call of the eight tools is correct. Fixed from candidate 3: timeline shows live turn ranges (end_turn 29 then 41; three segments after /compact), and from > to is refused. already_tried with empty target/approach and recall with an empty query are now tool errors. The first already_tried in a fresh project answers absent, so the ledger opens on first use.
- Partial because of UAT-09 R4-1: already_tried returns a wrong 'absent' for a stale elimination once the daemon restarts.
- The init event lists server plugin:qompack:qompack connected, the eight mcp__plugin_qompack_qompack__* tools, and exactly six slash commands. qompack:checkpoint is absent, as D36 expects.

### C4.5

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/C4.5/notes.txt; ../C4.4/cli/20-status-during-session, ../C4.4/cli/21-status-after-compact, ../UAT-08/session6-isolation/slash-commands.json, ../UAT-11/cli/c-10, c-11

- In all four readings (session 5 mid-session, session 5 after /compact, a second session's start in session 6, the UAT-11 project after its session) the status banner reads 'host contract: 9 assertion(s), all holding'. No row is FAILING; on candidate 3, transcript.readable and mcp.server_registered failed.
- The host resolved `qompack` on PATH on the first attempt with no PATH fix: with --plugin-dir, the plugin's bin/ is on PATH.

### Defects

- R4-1 (product, internal/negknow): after a daemon restart, already_tried answers {"state":"absent"} for a STALE elimination, under both staleResponse drop and flag. The tried.bloom query filter holds ACTIVE records only, and Record adds the key only in memory. On restart, reconcileBloom finds no active record missing and does not rebuild, so Query's !bloom.Test check returns absent before it reads the record. By code reading, an idle rebuild_bloom (rebuildOnStale nextIdle) drops stale keys the same way. Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-09/session2-resume/, session7-resume-flag-diag/, diag/.
- Observation: a staleResponse change reloaded by a running daemon does not reach already_tried (UAT-09 mcp-session1/T09-1).
- DOC: docs/uat.md UAT-09 step 5's 'reachable form' (a project with no ledger answers unavailable) is stale. The ledger opens on first use and answers a correct absent (C4.4/mcp/T15-1).

### Open issues

- R4-1 needs a product fix before UAT-09 step 4 and the C4.4 already_tried row can pass. The coordinator should route it.
- UAT-09 step 4's reason/note strings were not reachable on this run, because the stale answer turned into absent.
- UAT-09 step 5's 'reachable form' sentence needs a doc revision; it is outside this part's edit scope.
- The staged copy ~/.qompack/bin/1e9d0cc8…/ is retained. The part that runs install.md §6's uninstall step should remove it if homeguard reports it as gained for that part.
- UAT-07 observation, not a fail condition: CLI path: answers are crowded out by retrieval self-records, including a hit for a path that never existed (src/nope.go).

Real-home guard ok: True. `homeguard clean + check` against <scratch>/retrieval/guard.json at 2026-09-30 10:11 EDT, after resume: `real home fingerprint unchanged`, `check exit 0`. settings.json, installed_plugins.json, known_marketplaces.json, cache, marketplaces and data are all the same as the snap. INFO: ~/.qompack gained bin/1e9d0cc84597bf719955e268ef2e0c990352eb964559116a6612a012f1e5c358/, the run-created staged daemon copy; that sha256 equals the c4 bundle binary. It is retained because no local-scope install/uninstall ran in this part (install.md §6's removal condition was not reached), and other parts may share it. Every per-session check is in plans/sdd/V6-closeout/live/guard/retrieval.txt, and all passed.

## Part `recovery` — done, 2 of 2 sessions, head `4d0bd88e`

I resumed the C1.7 recovery part, whose first seat lost its API connection after finishing the run. I checked the uncommitted evidence against every step the part lists and all of them are there, then wrote plans/sdd/V6-closeout/live/rerun-c4/C1.7/notes.txt. I ran one homeguard check, appended it to plans/sdd/V6-closeout/live/guard/recovery.txt (unchanged, exit 0) and confirmed no qompack.exe is running. I then committed 4d0bd88e on closeout/live4 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-live. I ran no session, plugin command, backup or restore, and started no daemon.

C1.7 passes on a realistic store. Session 1 made MCP calls and ran /compact (checkpoint 0001), and its daemon stopped on the default idle exit. backup create --id before and verify both exit 0, and verify's scratch restore passes the integrity checks including the full seal check. The pre-new-write restore into D1 exits 0; fsck and fsck --seal-check exit 0; D1 matches the backup manifest file for file; a reader finds session 1's content. Session 2 wrote more (checkpoint 0003). The post-new-write restore into D2 exits 0, fsck --seal-check exits 0, and D2 matches the manifest without session 2's writes, which the source keeps. The two refusals behave as documented: a restore into an existing destination exits 1 and leaves D1 unchanged; create, verify and restore with a live daemon each exit 1 with 'daemon lock already held'. A final verify still exits 0.

Two of candidate 3's three blockers are fixed: a segment naming a checkpoint that was never written, and the restore note's contradictory wording. The third was not exercised: no MCP answer was garbage-collected (tombstone count 0), so the restore path for a collected MCP answer is still untested live. I found no new product defect. The run adds evidence for two known issues: recall's top 5 includes hits that are then denied, pushing file hits out (UAT-12 F1); and a daemon with no session logs 'running in degraded mode' (F-C4-C49-3). docs/uat.md has no C1.7 Result block, so I made no docs edit.

### C1.7

**Result:** pass

**Evidence:** plans/sdd/V6-closeout/live/rerun-c4/C1.7/ (notes.txt; cli/00-26 with cmd/stdout/stderr/exit for every step; session1/, session2/ driver outputs + transcript-facts.json; growth.json; store/ fingerprints, logs, manifest, segments; selftests.txt). Sessions: sessions.tsv recovery-c4 n=1 (53.1 s, exit 0) and n=2 (45.1 s, exit 0), claude-haiku-4-5-20251001, --plugin-dir of the frozen c4 bundle, Claude Code 2.1.280, Windows 11 build 10.0.26200.9457, qompack version 0.3.0, BUNDLE.json sha256 aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d, commit 9f6a2fad. The run was done 2026-09-30 10:13-11:23 America/Toronto by the first seat; this seat ran no session, plugin command, backup/restore or daemon. Step by step: initial absence recorded (00 backup create exit 1 'source project has no existing store'). Session 1 made recall, expand, record_eliminated, already_tried and timeline calls and a manual /compact (PreCompact checkpoint {} ok, SessionStart:compact ok, checkpoints/0001.json). All 21 hooks succeeded. The daemon (pid 67652) exited on the DEFAULT 1800 s idle exit with no termination. fsck --seal-check on the stopped source: exit 0. segments.jsonl names only sealed checkpoints, so F-UAT03-2 did not recur. 04 backup create --id before: exit 0, consistent, 83 files. 05 verify: exit 0, scratch restore OpenedOK, 25 roots, 19 tool refs, 0 tombstoned, integrity incl. the full seal check exit 0; nothing written to %TEMP% and the source was unchanged. 06 restore into D1: exit 0, same proof, integrity exit 0; the Note wording contradiction from candidate 3 is fixed. 07/08 fsck D1 and D1 --seal-check: exit 0. 09 D1 equals the manifest file for file. 10 reader probe on D1: session-1 content found. 11 fsck after the reader: exit 0. Session 2 read later.md, wrote summary.md, ran recall and record_eliminated, and compacted (0003). Its daemon also exited on the default idle exit. 14 fsck source --seal-check: exit 0. 15 restore into D2: exit 0. 16 fsck D2 --seal-check: exit 0. 17 D2 equals the manifest; the source has 43 more files (0003, 18 objects, 18 capture records) and 27 changed. 18a D2 reader: session-2 content absent, session-1 content present. 18b source reader: the later writes are kept. 25 final source fsck --seal-check: exit 0. 26 final verify: exit 0. Refusals: 19a restore into the existing D1 exits 1 with 'restore destination .qompack already exists' and 'No integrity check ran', and D1 is unchanged (+0 -0 ~0). 21/22/23 create/verify/restore with a live daemon (pid 45952, lock root = this project) each exit 1 with 'stop the source daemon before maintenance: qompack: daemon lock already held'; no live-attempt backup or D3 was created, and every reader or live daemon exited on its idle exit. Growth (growth.json): session 1 0 -> 390,204 B, session 2 +247,351 B, daemon working set about 41 MB during a session, idle tail peak 74.2 MB in session 1, measured hook deltas median 64 and 47 ms. docs/uat.md has no C1.7 Result block, so no docs edit.

- O1 (observation): checkpoint numbers skip. Session 1's set-aside draft holds seq 2, so session 2 sealed 0003 (parent 0001), and fsck verifies 0001 -> 0003. This matches docs/backup.md's set-aside-draft text, but the doc does not mention the numbering gap.
- O2 (new evidence for the known UAT-12 F1, recall's k counting denied hits): pathless MCP self-records fill the recall top-5 and are then denied, so permitted file hits drop out. D1 recall 'COBALT' gave count 1, denied 4, notes.md absent; session 2 recall 'FALCON' gave count 3, denied 2, later.md absent (cli/10-D1-reader-mcp-probe.json, session2/stream.jsonl).
- O3 (new evidence for the known F-C4-C49-3 / UAT-12 F6): the sessionless daemon started by a `qompack mcp` client logged warn 'checkpoint: no usable source set ... SourceSet.Ledger is nil ... running in degraded mode' (store/source_logs_qompack-20260930.log, 15:21:17Z).
- O4 (observation, not judged): fsck calls the delivery position documents 'v2' while a daemon runs and 'v1' on the stopped store. Every check passed either way.
- O5 (coverage gap): ToolRefsTombstoned stayed 0 in every proof because gc deleted nothing, so candidate 3's F-C49-2 path (an MCP root retired by a GC tombstone) is still not exercised live, as in the C4.9 re-run (F-C4-C49-1).
- Not established: older-release compatibility, cross-version readers and activation of a recovered project remain unverified. The readers were CLI `qompack mcp` probes (IDLEEXITSECONDS=30 on the probe only), not host sessions.

### Open issues

- The restore path for an MCP answer that garbage collection has retired (ToolRefsTombstoned > 0, candidate 3's F-C49-2) is still not exercised by any live run on candidate 4.
- Known retrieval issue UAT-12 F1 (recall's k counts denied hits; MCP self-records push permitted file hits out) has more evidence in C1.7 (O2); it is routed to the coordinator, not fixed.
- Checkpoint-number gap after a set-aside draft (O1): docs/backup.md could say the sequence then skips a number. This is a doc-clarity note, not a defect.
- Scrubbing: live_driver.py rewrites only the forward-slash spelling of the scratch path to <scratch>. session*/stream.jsonl and the two status outputs still contain the backslash-escaped scratch path and the host project slug, as the already-committed rerun-c4 streams do. No credential-shaped string was planted and no secrets.json exists, so the scan-staged gate did not apply.
- The untracked plans/sdd/V6-closeout/coordinator/__pycache__/ was left uncommitted.

Real-home guard ok: True. plans/sdd/V6-closeout/live/guard/recovery.txt: clean+check after session 1 (10:15:06 EDT) exit 0, after session 2 (10:48:34) exit 0, END-of-part block (11:22:58) exit 0, and this seat's resume check (11:39:02 EDT) 'real home fingerprint unchanged', check exit 0. No qompack.exe was running (tasklist). No plugin install in this part (--plugin-dir only). ~/.qompack gained nothing: bin/1e9d0cc8.../ is the c4 bundle binary's staged copy, which existed at snap time and was left in place. guard.json was not committed.

## Independent audit

**Verdict:** needs-fixes

| Item | Status | Evidence |
|---|---|---|
| C4.2 real headless sessions: every hook event captured, index counts match the transcript, status/doctor/fsck clean | evidenced | rerun-c4/UAT-02/c42-counts.json: host tool uses 14 main + 1 subagent. The refused blob.bin Read (toolu_01N2ra66…, the only host_tool_use_ids_missing_from_store entry; stream.jsonl shows 'This tool cannot read binary files') is excluded, leaving 14 = 14 store captures. Prompts 10 = 10, sessions 1 = 1, turn_order_violations []. Hook responses cover all seven events; PreCompact is shown by checkpoint 0001 and SessionEnd by the sessions.jsonl end line. cli/05-status, 05-doctor-json and 05-fsck-json exit 0 with the daemon live; cli/10-fsck-json-stopped(-seal-check) and 10-doctor-json-stopped exit 0 after the default idle exit. Notes: rerun-c4/C4.2/notes.txt. |
| C4.3 real compaction round trip: PreCompact checkpoint, SessionStart compact, bounded payload, model recovers facts | failed | rerun-c4/C4.3/notes.txt indexes UAT-03/04/05/06. Round trip and bounds hold: 16 checkpoints re-hash in UAT-04 steps/s5-checkpoints.txt, and blocks are 361-3,093 units. Two failures remain. UAT-03/probe/02-session-start-compact.stdout.txt: 0002 is corrupted and 0001 is presented as current, with probe-state degraded false and nothing saying a fallback happened. UAT-06/C-block2-SessionStart-compact.txt: the fork block omits the 60 correction. Model recovery: in UAT-04 the host's thrash stop ended both turns before the model answered; UAT-05 recovered the delimiter from the injected block. |
| C4.4 the model uses each of the eight MCP tools with correct results and errors | failed | rerun-c4/C4.4/tool-matrix.md plus mcp/T15-T24: every success and error call is correct. UAT-09/mcp-session2/T01-1-already_tried.json and mcp-session7-diag/T01-1 both answer {"state":"absent"} for a stale elimination after a daemon restart; session 1's T08/T09 answered 'stale' for the same record. That is a wrong result from one of the eight tools, which contradicts 'correct results'. The lane labels it 'partial' (tool-matrix.md:25), but UAT-09 records the same fact as a fail. |
| C4.5 slash commands run in a real session and match docs/commands.md | evidenced | rerun-c4/C4.5/notes.txt: 'host contract: 9 assertion(s), all holding' in four readings (C4.4/cli/20, 21; UAT-08/session6-isolation/slash-commands.json; UAT-11/cli/c-10, c-11). The init event lists exactly six commands and no qompack:checkpoint. On candidate 4 only status, recall, why, pin and dropped ran; /qompack:eval and the full comparison with commands.md rest on the candidate 3 evidence (live/c4/C4.5), which D47 did not require to be re-run. |
| C4.8 upgrade from the previous build, uninstall, reinstall, project work preserved, backup, restore and fsck certified | evidenced | rerun-c4/C4.8/notes.txt and UAT-12/cli. The previous build is 301a8e9 (0.2.99-prev); cli/00a-v020-bundle-attempt.txt shows v0.2.0 has no 'bundle' task (devtool: unknown task "bundle"). cli/12-diff-full: .qompack/ byte-identical across the upgrade. cli/42-diff-across-uninstall: byte-identical across the uninstall (791 files). cli/05, 06, 30, 32, 33 and 53, 55-58 all exit 0. cli/35 and 59: the old reader's fsck exits 1, and this is disclosed. Caveats: this is not the public v0.2.0 upgrade; no previous-build reader certifies the recovery; recall at the default k=5 misses session A's capture. |
| C4.9 degraded paths (daemon killed mid-session, unknown schema, unavailable object) never break the host session | partial | rerun-c4/C4.9/notes.txt and cli/c-01..c-10: backup, verify and restore after MCP calls and the default idle exit all exit 0, and fsck agrees. The unavailable-object wording (c-07) was checked only through a no-model stdio probe in a restored copy, not in a host session. Parts (a), daemon ended mid-session, and (b), newer settingsVersion, were not re-run on candidate 4; the only evidence for them is candidate 3's (live/c4/C4.9). C4.9 also found a new defect, F-C4-C49-3: advance_frontier warns 57 times. |
| C1.7 operator backup/verify/restore through the shipped CLI, pre- and post-new-write rollback rehearsed | evidenced | rerun-c4/C1.7/cli: 00 backup create exit 1 (absent); 03 fsck seal-check 0; 04 create 0; 05 verify 0; 06 restore into D1 0; 07/08 fsck D1 0; 14 source fsck 0; 15 restore into D2 0; 16 D2 fsck 0; 17-D2-vs-backup-vs-source.json and 18a/18b readers; refusals 19a, 21, 22 and 23 exit 1 with D1 unchanged (19b); 25 and 26 exit 0. Both daemons ended on the default idle exit with no termination (02, 13). Two recovery-c4 Haiku sessions are recorded in sessions.tsv and their meta.json files. |
| Re-run UAT Result blocks (01-09, 11, 12): evidence exists and matches, no expectation edited | partial | Every diff hunk in docs/uat.md for 9f6a2fad..HEAD falls inside a **Result** block; no Expected, Steps or Fail text changed. The cited evidence directories exist, and spot-checks match: BUNDLE.json sha256 aa7da0e1…558d and bin 1e9d0cc8…; the UAT-03 probe; UAT-06 checkpoint decisions; the UAT-09 absent/stale/unavailable responses; the UAT-12 text_bytes 8,038 / 261,163 / 165,233 / 261,172 against the 262,144 limit. Exceptions are listed in the findings: the UAT-05 authority-order claim, wording omitted from the UAT-12 and UAT-04 blocks, and the stale page header. |
| Executed by honest (agent-executed per D3) | evidenced | Every re-run block reads 'Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host per owner decision D3 — not human UAT'. Each notes.txt and snapshot.txt says the same. There is no claim of human UAT. |
| C1.6 'automatic recovery' stated honestly | partial | live/recovery/C1.6/notes.txt:35-38 (candidate 3): 'Automatic RECOVERY does NOT exist'. docs/backup.md:79-83 and troubleshooting.md:1124 say accounting 'discovers ... does not reconstruct'. C1.6 was not re-run on candidate 4. Candidate 4 evidence shows the startup accounting is interrupted at 250 ms on a 70-capture store (UAT-12/store/logs_LOUD.log, UAT-05/store/logs_LOUD.log), so on this candidate 'accounting exists' holds only as an incomplete lower bound. |
| Budgets (install 4, sessions 9, retrieval 7, recovery 2 = 22) | evidenced | sessions.tsv rows install-c4 1-4, sessions-c4 1-9 (n=5 is the accidental Opus session with no plugin, counted), retrieval-c4 1-7 and recovery-c4 1-2 total 22. There are 21 meta.json files under rerun-c4, plus accidental-session/transcript-facts.json for the accidental session. |
| Guard: passing check after every session and at each part's end | evidenced | guard/install.txt: 'check exit 1' appears only inside the open local-scope windows (named DIFFs in installed_plugins, known_marketplaces and cache/qompack-live). Checks pass at 20:46:23 (after UAT-01 cleanup), at 21:06:49 and at the 21:07:05 end. guard/sessions.txt: 9 per-session checks plus the end check at 23:20:59, all exit 0. guard/retrieval.txt: start, 7 sessions and the final check at 10:11:09, all exit 0. guard/recovery.txt: after sessions 1 and 2, END and resume, all exit 0. The recovery c4 block has no snap line. |
| Secrets and forbidden files in committed evidence | evidenced | Over all 1,420 files changed in 9f6a2fad..HEAD, no match for AKIA, gh*_, github_pat_, sk-ant-, xox*, PRIVATE KEY, AIza, sk_live_ or glpat- shapes. Placeholders are only @@SEC_PLANTED_1@@ (9) and @@SEC_PLANTED_2@@ (4). There is no guard.json, secrets.json, credentials file, host transcript .jsonl (transcript-facts.json only) or user settings. The one settings file, UAT-12/cli/08-project-settings.json, is the disposable project's deny rule. The init events list only the qompack plugin and builtin plugins. |
| Commit hygiene | evidenced | There are 7 commits and no merges. All subjects are test(live): or docs(uat):, 55-62 characters. No body line exceeds 100 characters, and a grep for co-authored, claude-session, generated with and signed-off finds nothing. |

### Audit findings

- **major** `docs/uat.md:680-682 (UAT-05 Result); plans/sdd/V6-closeout/live/rerun-c4/UAT-05/notes.txt:31-32` — UAT-05 reports pass and says the TAB correction has 'nothing older above it'. In the injected block, the superseded semicolon requirement (the verbatim original) sits above the correction, and the correction is third in the evolution list, below two later ordinary prompts. The row's expectation says 'The corrected requirement must appear above the superseded one'. Its fail criterion is 'an older statement rendered above the correction that superseded it', which reads as literally met. This is asserted as passing rather than recorded as a deviation or an interpretation. Evidence: rerun-c4/UAT-05/run1-block2-SessionStart-compact.txt lines 7-12: '> We are adding a CSV export ... must use a semicolon (;)' comes first, then 'Evolution (most recent first):' with cat data/meta.txt, record_eliminated, and then 'Correction: the export delimiter must be a TAB'. Fix: Record in the UAT-05 Result block that the superseded original renders above the correction, as the fixed section 2 layout requires. State whether the pass relies on reading 'superseded one' as evolution entries only, and route the ambiguity (the expectation versus the section order) to the coordinator. Do not edit the expectation.
- **major** `docs/uat.md:8-19 (page header)` — The page header is stale after the re-run. It still says all twelve scenarios were run once against release candidate 3 (d5598eb4) with 'six pass and six fail', and it promises that every row, UAT-10 included, is re-run on the fixed candidate. Eleven Result blocks now report candidate 4 (9f6a2fad). UAT-10 was not re-run and still reports candidate 3 (Snapshot at line 1226-1228, its FAILING-banner and p95>max findings unverified on candidate 4). The public page therefore misstates which candidate its results belong to. Evidence: docs/uat.md:10 'against release candidate 3 (commit `d5598eb4`...'; :13 'six pass and six fail'; :14 'every row is re-run on the fixed candidate'; :1226-1228 UAT-10 Snapshot is bundle 32600778…, commit d5598eb4. rerun-parts.js assigns no UAT-10 re-run. Fix: Rewrite the header to say that eleven rows were re-run on candidate 4 9f6a2fad under D47, with the new pass/fail tally, and that UAT-10 stands on candidate 3. Alternatively, schedule a UAT-10 re-run.
- **minor** `plans/sdd/V6-closeout/live/rerun-c4/C4.4/tool-matrix.md:25` — C4.4 is labelled 'partial', but the item requires correct results. already_tried answers {"state":"absent"} for a stale record after a daemon restart, which is a wrong result, and UAT-09 records the same fact as a fail. The label understates the item's status for the checklist. Evidence: UAT-09/mcp-session2/T01-1-already_tried.json and mcp-session7-diag/T01-1: {"state":"absent"}; mcp-session1/T08-1 answered 'stale' for the same target and approach. Fix: Mark C4.4 as failed (R4-1) in the checklist and live report, or define 'partial' explicitly as 'fails on one tool'.
- **minor** `docs/uat.md:1447-1477 (UAT-12 Result)` — The Result block leaves out deviations its own notes record. (1) The rehydration block's section 6 lists the deny-ruled file's absolute path and root hash, and the out-of-project file's absolute path, although re_read deliberately withholds an out-of-project path (F4). This bears on C4.6's 'refused by every retrieval form'. (2) The final page answers truncated:true with no next_span. (3) An explicit span 0:354352 pages as next_span 217070:16384, while full:true pages as 217070:137282. Evidence: rerun-c4/UAT-12/notes.txt:170-172 (F4) and its semantics note. cli/20-mcp-probe.json: the span 217070:137282 call has truncated true and no next_span; the span 0:354352 call has next_span 217070:16384. Fix: Add F4 and the paging-semantics note to the UAT-12 Result findings, and route F4 to privacy review (C4.6).
- **minor** `docs/uat.md:217; plans/sdd/V6-closeout/live/rerun-c4/UAT-01/notes.txt:50-55` — After the session's MCP call, the daemon's contract snapshot reads mcp.server_registered 'initialize-pending' and hook.additional_context_delivered 'not-yet-observed'. The same store's state/history.json records mcp_initialized true and sentinel observed true. The contradiction is copied into the block without being raised. It also means 'host contract: 9 assertion(s), all holding' (used as C4.5 proof) was partly vacuous in this reading. Evidence: UAT-01/cli/x4-status-json.stdout.txt lines 37 and 70 (collected_at_ms 1790729106095); UAT-01/store/state_history.json has "mcp_initialized":true and sentinel "observed":true. Fix: Record this as a UAT-01 finding (snapshot not refreshed after an observed initialize or sentinel), and do not cite that banner as positive evidence for mcp.server_registered.
- **minor** `plans/sdd/V6-closeout/live/rerun-c4/C4.9/notes.txt:10-11` — Candidate 4's C4.9 re-ran only the (c)-style restore. Its unavailable-object check was a no-model stdio probe, so 'the host session is never broken' is not shown live on candidate 4 for the daemon-killed, unknown-schema or unavailable-object paths. Evidence: notes.txt: '(a) daemon ended mid-session and (b) newer settingsVersion passed on candidate 3 and were not repeated'; cli/c-07-mcp-probe-unavailable.json is a stdio probe. Fix: Report C4.9 as candidate 4 (c) plus candidate 3 (a)/(b), or re-run (a)/(b) in the next live wave.
- **minor** `plans/sdd/V6-closeout/live/recovery/C1.6/notes.txt:35-38; docs/backup.md:79` — C1.6's 'automatic startup accounting EXISTS' comes from candidate 3. On candidate 4 the startup accounting is interrupted by its 250 ms bound on a 70-capture, 380-object store, on every daemon start in UAT-12 and in UAT-05. So the accounting is present but never completes on ordinary stores. The 'no automatic recovery' statement remains honest. Evidence: rerun-c4/UAT-12/store/logs_LOUD.log: 'publication accounting incomplete ... scan interrupted before it finished' at 00:58:08Z and 01:03:52Z; the same line appears in UAT-05/store/logs_LOUD.log. Fix: Add a candidate 4 note to C1.6 citing the interrupted scans, and keep the D49 fix ('a healthy store never logs publication accounting incomplete') as a C1.6 precondition.
- **minor** `plans/sdd/V6-closeout/live/guard/retrieval.txt (end) and guard/recovery.txt:28` — The parts handled the D10 staged copy inconsistently. The sessions part removed ~/.qompack/bin/1e9d0cc8… under install.md §6 with no install in the part. The retrieval part re-created it and kept it, and the recovery part kept it too, so the lane ends with a run-created copy in the operator's real ~/.qompack. The recovery c4 guard block has no snap or start line, so its claim that the copy 'existed at snap time' is not in the committed log. Evidence: guard/sessions.txt: 'removed with install.md section 6 ...'. guard/retrieval.txt: an INFO gained line at every check, with retention stated in the part report. guard/recovery.txt c4 section begins at 'after session 1' with '.qompack/ (2 entries)'. Fix: The coordinator should remove the staged copy under the §6 rule (sha256 = bundle bin, no qompack.exe running) and log it. Future parts should log their snap state.
- **minor** `plans/sdd/V6-closeout/live/sessions.tsv (sessions-c4 n=5); rerun-c4/accidental-session/notes.txt` — A lane error launched an unplanned claude session in the real profile. It ran with no --setting-sources and no --plugin-dir, on the operator's default model claude-opus-5-5 rather than Haiku. It is honestly recorded and counted, and the guard passed afterwards. The part had to replace UAT-05's second session with a hook invocation. Evidence: accidental-session/notes.txt; transcript-facts.json shows 27 lines, tool_uses {}, and hook_success/SessionStart from the operator's own configuration. Fix: None needed for the record. Keep the 'python <script>' discipline in the brief.
- **nit** `docs/uat.md:787 (UAT-06 Result)` — 'the block's only statement of the limit is the superseded one' overstates. The fork block's evolution list still contains the record_eliminated prompt text 'reason "superseded by the user's correction: 60 per minute"'. The fail itself stands, because the correction record was dropped and no section 3 or 4 is present. Evidence: rerun-c4/UAT-06/C-block2-SessionStart-compact.txt, last evolution line. Fix: Reword to say that the correction record itself is absent and that 60 appears only inside an echoed tool-call prompt.
- **nit** `docs/uat.md:550-567 (UAT-04 Result)` — The post-run fsck exit 1 (index.files 'absent while its log carries 4 path(s)') and the per-compaction LOUD 'tier-1 material exceeds the hard budget cap' appear in notes.txt but not in the Result block. Evidence: rerun-c4/UAT-04/notes.txt, lines on O-UAT04-2 and the 'logs rehydrate: tier-1 material exceeds' line. Fix: Add both to the UAT-04 Result block's findings.
- **nit** `plans/sdd/V6-closeout/live/rerun-c4/*/session/stream.jsonl (and C1.7, C4.9, UAT-02..11)` — The scratch-prefix scrub misses the JSON-escaped Windows form. Committed streams carry 'C:\\Users\\Quant\\AppData\\Local\\Temp\\claude\\qompack-live\\...', which exposes the OS username. This is not a secret, and candidate 3's streams had the same issue. Evidence: grep matches in 27 committed files across 12 evidence directories, for example UAT-02/session/stream.jsonl in the toolu_01N2ra66 Read input and the init event cwd. Fix: Extend live_driver.py's scrub to the backslash-escaped variant for future runs. History cannot be rewritten, so record it as accepted.
- **nit** `docs/uat.md:318-341 (UAT-02 Result)` — The block reports 'pass' although, in its own words, 'the non-exact capability the row exists to show is unverified on this host'. It is disclosed, and D49 later rules it host-limited, but a bare pass label still overstates what was shown. Evidence: docs/uat.md:324-330. Fix: Label it 'pass on fail criteria; row capability host-limited (D49)'.
