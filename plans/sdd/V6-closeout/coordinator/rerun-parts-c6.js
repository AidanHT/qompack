const RERUN = `THIS IS A RE-RUN (decisions D52 and D53). Candidate 4 (9f6a2fad) failed or left open the rows below. Waves 15, 15a, 15b and 15c fixed those defects. Candidate 5 (0d06ab12) was never run live: its overnight chain, the independent audit (plans/sdd/V6-closeout/audit/goal-metrics-audit.md) and hosted CI found the defects wave 16 fixed. Read these first:
- plans/sdd/V6-closeout/live/report-c4.md: candidate 4's results, findings and audit;
- plans/sdd/V6-closeout/w15-*/report.md, w15a-*, w15b-*, w15c-* and w16-*: what changed;
- the ledger's D49, D50, D51, D52 and D53 rows: each disposition.
Re-run ONLY the rows named in your part, on this candidate and bundle, exactly as docs/uat.md and the checklist state them. Read the current text: several steps were revised under D46, D49, D50 and D53.
Write evidence under ${LIVE}/rerun-c6/<row>/ and never overwrite earlier evidence. In docs/uat.md, replace each re-run row's Result block with this candidate's result. Keep the existing history lines and add one line: "Candidate 4 (9f6a2fad): <its verdict> — <one-line reason>, evidence plans/sdd/V6-closeout/live/rerun-c4/<row>/", unless that line is already there.
Judge every row against its CURRENT expectation. A defect that is still present, or a new one, is a finding with evidence, never excused. A host-reported hook failure or timeout is a finding under D53(i) unless docs/cannot-do.md or docs/upstream-issues.md documents it as host behaviour.
Commit after each row, so an interruption loses little. Log the guard snap state at the start of your part. At its end, remove any run-created ~/.qompack content under install.md §6 (its sha256 equals the bundle's bin, and no qompack.exe is running) and log that (D50).`

const PREV = 'C:/Users/Quant/Documents/Programming/Projects/qompack-bundles/c5/qompack-plugin-0.3.0-windows-amd64'

const PARTS = [
  { key: 'install', phase: 'Install', budget: 4, part: `${RERUN}
Scenario ids to return: UAT-01, C4.1, C4.8, UAT-12 (upgrade leg), C1.7.
(1) UAT-01 and C4.1 (1 session), installed the way a release user installs (D53(f)).
  - Build a local directory marketplace named qompack-live whose ONE plugin entry is named qompack-windows-amd64. That is the release's per-target entry name; plugin.json inside still says qompack. Its source is a copy of the frozen bundle under your scratch.
  - Install it at --scope local in a fresh disposable project, with no QOMPACK_* variables. Run one short session that runs a Qompack slash command and makes at least one Qompack MCP call.
  - Record the namespace the host gives the release entry, exactly as observed: claude plugin list --json and claude plugin details; the session init line's slash_commands and tools; the command name you typed; the MCP tool names the host used. Then check that recall ranks the host-captured records of your own MCP calls after the original captures (D49, wave 16 relhyg).
  - Compare with docs/install.md and docs/commands.md. A namespace the docs do not match is a finding; record the observed strings where the docs mark them "to be confirmed at execution".
  - Then run UAT-01 steps 2-8 with the bundle CLI.
  - Restore: \`claude plugin uninstall qompack-windows-amd64@qompack-live -s local\`, \`claude plugin marketplace remove qompack-live\`, orphan cleanup, guard check.
(2) C4.8, the UAT-12 upgrade leg and C1.7 (at most 3 sessions). The previous build is candidate 5's frozen bundle, ${PREV} (0.3.0 has no earlier public release; 0.3.0 is the first installable one).
  - Previous build: in a disposable project with --plugin-dir ${PREV}, run 1-2 sessions with MCP calls and a /compact. Let its daemon idle-exit, or end it as documented and say so.
  - Back up: backup create, then backup verify, with the FROZEN candidate's CLI.
  - Upgrade: one session on the same project with --plugin-dir ${BUNDLE}. Record status, doctor --json and fsck --json.
  - Restore (C1.7 smoke): backup restore into a fresh destination, then fsck --seal-check on the recovery and on the source.
  - Survival: uninstall and reinstall (local scope through the qompack-live marketplace as in (1), or --plugin-dir as before; say which). The project's .qompack/ must survive both.
  - Every step must pass on the CURRENT expectations; record each command's exit code and output.
Guard check after every session and every plugin CLI step. If the budget runs short, drop the reinstall session of (2) and record the skip.` },
  { key: 'sessions', phase: 'Sessions', budget: 8, part: `${RERUN}
Scenario ids to return: UAT-03, UAT-04, UAT-05, UAT-06, C4.3, C4.5. Every session uses --plugin-dir ${BUNDLE}, one disposable project per scenario, and the DEFAULT idle exit unless a step needs the daemon gone (then say so in the Result block).
UAT-03 (1 session): the checkpoint round trip across an idle exit and restart. Then the corrupted-newest-checkpoint probe as candidate 4 ran it (rerun-c4/UAT-03/probe/): the payload header, section 7, the drop report, the state's degraded/degraded_reason and LOUD.log must all name the rollback (D49). Backup create/verify/restore and fsck as before.
UAT-04 (1 session): the ~17,000-character first prompt, then two manual /compact and at least one automatic compaction. Do NOT force the 30%/100k auto-compaction override that thrashed the host on candidates 3 and 4: use a setting that compacts without thrashing, and state it. The long original is carried whole or named as an overflow. LOUD 'tier-1 material exceeds the hard budget cap' appears at most once per session (D50). Run fsck with the daemon up and after idle exit.
UAT-05 (2 sessions): the correction must render ABOVE the superseded original in section 2 (D50, read literally), with the pin. Step 5 as the current text states it, including a config change to runtime.rehydrate.* reaching the RUNNING daemon without a restart (D49): record the reload line and the next block's budget.
UAT-06 (3 sessions): compact, then --resume, then --resume --fork-session with a correction. The fork's first block carries the parent's original AND the correction in force. The parent's session-scoped elimination and decision are visible in the fork (checkpoints, already_tried, why). After --resume, the session's second checkpoint still carries its earlier decision.
C4.3: judge from these rows (bounded payloads, correct current authority, Qompack-sourced recovery).
C4.5: in one of these sessions, after an MCP call, record \`qompack status\` and \`doctor --json\`. The banner must not count a pending row as holding, and mcp.server_registered and hook.additional_context_delivered must read what history.json records (D50). Two reads of unchanged state must agree exactly (D53(a)).
If the budget runs short, run UAT-06's fork session last and record any skip.` },
  { key: 'retrieval', phase: 'Retrieval', budget: 5, part: `${RERUN}
Scenario ids to return: UAT-09, UAT-12, C4.4, C4.6. Every session uses --plugin-dir ${BUNDLE}, in fresh disposable projects.
UAT-09 (2 sessions):
  - Step 2: in-session staleness.
  - Then restart the daemon (idle exit or a documented stop). already_tried on the stale record must answer stale, never absent (R4-1).
  - A mid-session eliminations.staleResponse change must reach already_tried in the running daemon (D49).
  - Step 5 in its current reachable form.
UAT-12 (2 sessions; reuse the candidate 4 recipe without the previous-build upgrade, which the install part covers):
  - The deny-rule and out-of-project checks.
  - The rehydration block after /compact: section 6 shows no host-denied path and no absolute out-of-project path (hash-only pointers, D50).
  - Paging over a >300 KB capture: every truncated page carries next_span, the last page is not truncated, and an explicit span 0:<total> pages exactly like full:true (D50).
  - Recall at the default k returns permitted hits up to k, with denied hits counted apart. Qompack's own retrieval self-records rank after the original captures (D49).
  - The daemon's LOUD.log carries no 'publication accounting incomplete' line on this multi-session store (D49/D51).
C4.4 (1 session; may share with UAT-09's second session): the eight tools' success and error calls, already_tried included after a restart.
C4.6: judge from UAT-12 (every retrieval form and the rehydration block refuse the denied path).` },
  { key: 'resilience', phase: 'Recovery', budget: 3, part: `${RERUN}
Scenario ids to return: UAT-10, C4.9. Every session uses --plugin-dir ${BUNDLE}.
UAT-10 (1 session): as docs/uat.md states it in its current text (status/dropped/eval envelopes, provenance, usage categories, the uncertainty check). Candidate 3 recorded a FAILING banner and a p95>max finding here: say whether each is gone.
C4.9 (2 sessions):
  - (a) The daemon ends mid-session. Terminate only a daemon you started, after checking its lock pid, image and command line. The host session must never break, and the next hook must recover.
  - (b) A newer settingsVersion in the project config: the documented refusal or degradation, and no broken session. Record the unavailable-object wording in a host session if reachable.
  - (c) was re-run on candidate 4 and passed; do not repeat it.` },
]
