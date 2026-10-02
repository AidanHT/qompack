// Phase 4 live lane. Launch right after the candidate is frozen, replacing ONLY __CANDIDATE__ (the
// frozen commit) and __BUNDLE__ (the frozen windows/amd64 bundle directory, holding BUNDLE.json).
// Before launch: merge closeout/w7-livelane into the candidate (the lane uses homeguard.py and
// live_driver.py beside this file) and create worktree ../qompack-cx-live (branch closeout/live)
// at the candidate. Parts run strictly one after another; a failed real-home guard stops the lane.
// C4.11's Linux half uses the -linux-amd64 sibling of __BUNDLE__ from the same devtool bundle run
// (phase 3's bundles step builds all six); without one it is recorded unknown. Session budgets
// (install 8, sessions 12, retrieval 12, recovery 4 = 36) share owner decision D3's ~40-80 with
// C5.5's 40 pre-registered trials and the 6 smoke/pilot sessions already used: about 82 in all.
export const meta = {
  name: 'v6-closeout-live-rerun-c7',
  description: 'Phase 4: agent-run UAT-01..12, C4.1-C4.11, C1.6/C1.7 and C5.6 on the frozen candidate, sequential real sessions, then an evidence audit',
  phases: [
    { title: 'Install', detail: 'C4.1 isolated-profile + local-scope marketplace install, UAT-01, C4.8 upgrade/uninstall/reinstall, C4.6/UAT-12 privacy and lifecycle' },
    { title: 'Sessions', detail: 'UAT-02..06: C4.2 capture, C4.3 compaction round trip, C4.9 degraded paths' },
    { title: 'Retrieval', detail: 'UAT-07..11: C4.4 every MCP tool, C4.5 slash commands, C4.7 kill switches' },
    { title: 'Recovery', detail: 'C1.6 startup accounting + fsck, C1.7 backup/verify/restore rehearsal, C5.6 resource summary, C4.11 platforms' },
    { title: 'Audit', detail: 'independent read-only audit of every Result block and coverage row against its evidence' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const WT = `${ROOT}/qompack-cx-live`
const CAND = 'd20309c03ffc364e4cc48663be73cfbb1f2309b2'
const BUNDLE = 'C:/Users/Quant/Documents/Programming/Projects/qompack-bundles/c7/qompack-plugin-0.3.0-windows-amd64'
if (CAND.startsWith('__') || BUNDLE.startsWith('__')) throw new Error('live-uat.js: replace __CANDIDATE__ and __BUNDLE__ before launch')

const LIVE = 'plans/sdd/V6-closeout/live'
const TOOLS = 'plans/sdd/V6-closeout/coordinator'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/qompack-live'

const COMMON = (key, part, budget) => `You are the V6 close-out LIVE lane for Qompack (a Go Claude Code plugin: seven hooks -> resident per-project daemon -> content-addressed store under <project>/.qompack/, an MCP retrieval server "qompack mcp" with eight tools, six /qompack: slash commands). You run REAL headless Claude Code sessions on the owner's own Windows machine to prove the plugin works as installed. Owner decision D3: these runs are AGENT-EXECUTED on the real installed host, never human UAT; every record says so.

WORKTREE: ${WT} (branch closeout/live7 at the frozen candidate ${CAND}). The Bash tool resets cwd: prefix commands with \`cd ${WT} &&\`. Frozen bundle (do not rebuild it): ${BUNDLE}; its CLI is ${BUNDLE}/bin/qompack.exe — use that binary for every qompack command, never a build from source. Scratch root for this part: ${SCRATCH}/${key}/ (outside every git repository; one disposable project per scenario beneath it). Coordinator ledger (read, do not edit): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md (owner decisions D1-D57). Read docs/uat.md, docs/install.md, docs/backup.md, docs/commands.md, docs/mcp-tools.md, docs/user-guide.md, docs/release.md §4 and docs/troubleshooting.md first. Tooling (committed; if either file is missing, stop and return blocked): ${TOOLS}/homeguard.py (real-home guard: snap / check / clean, self-test homeguard_test.py) and ${TOOLS}/live_driver.py (stream-json session driver: one user message per turn after the previous result; per-turn "before" argv for degraded steps; writes stream.jsonl, stderr.txt, meta.json with daemon RSS/CPU and .qompack/ byte samples, hooks.json with host-seen hook arrival deltas; "secrets_file" scrubs planted secrets from every output; \`live_driver.py scan-staged <repo> <secrets.json>\` is the pre-commit secret gate; self-test live_driver_test.py). Run both self-tests once before your first session. References: plans/sdd/V6-closeout/eval/runs/marketplace-rehearsal.md (the local-marketplace flow and what it leaves behind) and tools/devtool/liveeval_host.go (marketplaceInstall, stopTrialDaemon, readTranscript).

YOUR PART (${key}): ${part}

HOST FACTS (Claude Code 2.1.280; checked against its own --help and the committed C5.4 smoke/pilot streams):
- Session command: \`claude -p --input-format stream-json --output-format stream-json --verbose --include-hook-events --model claude-haiku-4-5-20251001 --max-turns <n> --setting-sources project,local --permission-mode dontAsk --allowedTools <comma list>\` plus \`--plugin-dir <bundle>\` where the part says so; env ENABLE_CLAUDEAI_MCP_SERVERS=false. --max-turns is not listed by --help but 2.1.280 accepted it in every smoke/pilot run. Never pass --bare (it skips hooks) or --no-session-persistence (the transcript is evidence). With -p, "Settings files that fail validation are silently ignored": prove any project settings you write took effect before relying on them.
- Tool names: plugin MCP tools are mcp__plugin_qompack_qompack__<tool> and the server is "plugin:qompack:qompack"; the slash commands are /qompack:dropped, eval, pin, recall, status, why (six; /qompack:checkpoint was removed for 0.3.0 by D36, so its absence is expected and its presence is a finding) (read both from the stream's system/init event: tools, mcp_servers, slash_commands, plugins). In dontAsk mode a tool not in --allowedTools is denied, so list the MCP tools and the commands' Bash(qompack <cmd>:*) rules explicitly. Each command body runs \`qompack <cmd>\` through Bash: whether the host resolves \`qompack\` on PATH is part of what C4.5 observes — record it, do not pre-fix PATH on the first attempt.
- Hook events arrive as system/hook_started and system/hook_response (hook_name like "SessionStart:compact", outcome, exit_code); they carry NO duration field, so the only host-side latency is the stream arrival delta in live_driver.py's hooks.json — approximate, and it can UNDER-read when the host batches output (the SessionStart:startup pair arrives before the init event, after the hook ran). Pairs marked "measured": false (before init, or within 5 ms) are reported as not measured, never as ~0 ms. SessionEnd trouble shows only on stderr ("SessionEnd hook [...] failed: Hook cancelled").
- Compaction: a user turn whose content is "/compact" compacts headlessly and yields a result; automatic compaction is forced with env CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 and CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=30 (smoke3; --autocompact <tokens> is the documented flag for the window, 100k minimum, unrehearsed). A PreCompact hook rejection is only visible on a "/compact" turn. Resume and fork: --resume <session-id> [--fork-session] (each is a new session for the budget).
- Plugin CLI (no model call): \`claude plugin marketplace add <dir> --scope user|project|local\`, \`claude plugin marketplace update <name>\`, \`claude plugin marketplace remove <name>\` (no --scope removes it from every scope), \`claude plugin marketplace list --json\`, \`claude plugin install <plugin>@<mkt> -s user|project|local [-y] [--json]\`, \`claude plugin update <plugin>@<mkt> -s <scope> [-y] [--json]\`, \`claude plugin uninstall <plugin>@<mkt> -s <scope> [-y] [--keep-data] [--json]\` (without --keep-data it removes ~/.claude/plugins/data/<id>/), \`claude plugin list --json\`, \`claude plugin details <plugin>\` (component inventory), \`claude plugin validate <dir> --strict --json\`. 2.1.280 accepts --json on install/update/uninstall, which docs/install.md §3 says it does not (written on 2.1.263): record that as a documentation finding. \`marketplace add --scope local\` still rewrites the global plugins/known_marketplaces.json until \`marketplace remove\`, and remove leaves plugins/cache/<mkt>/qompack/<version>/ marked .orphaned_at: delete that directory only when your run created it and every version in it is one of your bundles (compare BUNDLE.json), per commit 09a5222.
- Authentication: a disposable CLAUDE_CONFIG_DIR has no login and credentials must never be copied, so plugin CLI steps may use an isolated CLAUDE_CONFIG_DIR=<scratch>/profile, but model sessions run on the operator's real profile with the plugin either --plugin-dir or installed at --scope local inside the disposable project. Real sessions also write the host's own ~/.claude/projects/<slug>/<id>.jsonl transcript and ~/.claude.json; read only your own sessions' transcripts (by session id), commit only extracted facts without message bodies (transcript-facts.json, as liveeval does), never the raw transcript.
- ~/.qompack is resolved from the home directory (USERPROFILE), so real sessions write the real ~/.qompack: logs/ and, per D10, staged daemon copies bin/<sha256>/qompack.exe. QOMPACK_HOME only moves calibration.json. Set QOMPACK_HOME=<scratch>/qhome on every session and CLI step except where a row's preconditions forbid QOMPACK_* variables (UAT-01). homeguard.py lists ~/.qompack by name: record what your run added, never delete anything that existed at snap time, and remove a staged copy only after install.md §6's uninstall step and only if homeguard reports it as gained and its sha256 equals a bundle binary you used.

HARD RULES (owner directives):
- Real-home guard: \`python ${TOOLS}/homeguard.py snap ${SCRATCH}/${key}/guard.json\` before your first session or plugin command; after EVERY session and every plugin CLI step run \`homeguard.py clean\` then \`homeguard.py check\` on that file and append the check output to ${LIVE}/guard/${key}.txt (commit it; never commit guard.json, it lists the operator's plugins). During a local-scope install the marketplace/installed-plugin JSON legitimately differ until uninstall + marketplace remove; outside that window, and at the end of the part, check must pass. Restoring means ONLY \`claude plugin uninstall qompack@qompack-live -s local\` (or, for the install part's release-named entry, \`claude plugin uninstall qompack-windows-amd64@qompack-live -s local\`), \`claude plugin marketplace remove qompack-live\`, and deleting cache or data directories your run created (homeguard names them as gained); NEVER write, edit, copy over or delete any file under ~/.claude by hand, including settings.json, plugins/installed_plugins.json and plugins/known_marketplaces.json. A diff you cannot attribute to your own still-open install window (for example a host marketplace auto-update rewriting known_marketplaces.json) means STOP at once: do not "restore" it, commit the check output, report guard_ok false and let the coordinator decide. NEVER install at user scope in the real profile, never open credential files (.credentials.json or similar).
- Windows Defender (owner decision D32): builds have been flagged as Trojan:Win32/Bearfoos (ML false positive) and the owner's exclusion may not cover the plugin cache, ~/.qompack/bin/<sha256>/ or scratch. If a qompack.exe vanishes, fails with access denied, or a hook reports its binary missing, run \`powershell -NoProfile -Command "Get-MpThreatDetection | Select-Object -Last 5 | Format-List"\` (read-only) and, when it names the binary, record the step as ENVIRONMENTAL under D32, not a product defect. Never change Defender settings or restore quarantined files.
- Session budget for this part: at most ${budget} real sessions (a session = one claude process that talks to the model), counting failed ones, resumes and forks. Append one line per session to ${LIVE}/sessions.tsv (part, n, scenario, evidence dir, model, exit, wall_s). Model claude-haiku-4-5-20251001; use claude-sonnet-5 only when a step genuinely needs it, and say why.
- Daemons: terminate only a daemon your scenario started, identified by its project path in <project>/.qompack/run/daemon.lock AND process image qompack.exe; prefer the idle exit (QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=30 where the row's preconditions allow a non-default). Never kill a process you did not start. No load generators; no \`go test ./...\`.
- No git push/tag/merge/rebase/reset; commit only in ${WT}. Conventional commits (\`docs(uat): ...\`, \`test(live): ...\`), lowercase scope, subject <=64 chars, body lines <=100, NO attribution trailers (no Co-Authored-By, Claude-Session, "Generated with").
- Evidence under ${LIVE}/ (uat/UAT-NN/, c4/C4.N/, recovery/C1.N/, resources/C5.6/): driver outputs, command outputs with exit codes, .qompack/ listings, status/doctor/fsck/self-test outputs, guard results. Keep failures exactly like passes. Scrub only the scratch prefix (live_driver.py's "scrub": "${SCRATCH}") AND planted secrets, mechanically: build every planted credential-shaped string at run time from split parts and write the list ONLY to ${SCRATCH}/${key}/secrets.json (never into a committed file or a command transcript you commit); pass "secrets_file" to live_driver.py on EVERY session of a scenario that plants secrets, so its outputs carry @@SEC_PLANTED_<n>@@ instead; copy any other output (CLI responses, greps, listings, store files) into ${LIVE}/ only through a filter that applies the same replacement; record each secret's sha256 in the evidence. HARD STEP before every commit while a secrets.json exists: \`git add\` the evidence, then \`python ${TOOLS}/live_driver.py scan-staged ${WT} ${SCRATCH}/${key}/secrets.json\`; on exit 1 unstage the named files (\`git restore --staged <path>\`), scrub them and scan again; commit only on exit 0 (the repo is public, push protection blocks credential shapes, and history cannot be rewritten). Raw copies stay in scratch.
- Fill each scenario's Result block in docs/uat.md from what you observed: Result pass|fail|skipped — <reason>; Snapshot: \`qompack version\` output, bundle BUNDLE.json sha256, commit ${CAND}, Windows 11 build, Claude Code 2.1.280; Date 2026-..-.. (America/Toronto); Executed by: "Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host per owner decision D3 — not human UAT"; Evidence path; Rollback verified per the doc's rule. Read each expected-result block field by field. A result that differs from the doc is a FINDING: record it, never edit the expectation to match. If a product defect blocks a step, record the step, stop that scenario, report the defect with evidence (the coordinator routes fixes).
- Do not change product code or tests. Documentation edits are limited to Result blocks and to recording observed strings the doc marks "to be confirmed at execution".
- Subagents cannot write report.md files: return your full report as your result (per scenario and checklist item: result, sessions used, evidence path, findings with evidence; guard results; open issues); the coordinator commits it.`

const RESULT = { type: 'object', properties: {
  status: { type: 'string', enum: ['done', 'partial', 'blocked'] }, head: { type: 'string' },
  commits: { type: 'array', items: { type: 'string' } }, sessions_used: { type: 'number' },
  scenarios: { type: 'array', items: { type: 'object', properties: {
    id: { type: 'string' }, result: { type: 'string' }, evidence: { type: 'string' }, findings: { type: 'array', items: { type: 'string' } },
  }, required: ['id', 'result', 'evidence', 'findings'] } },
  summary: { type: 'string' }, defects: { type: 'array', items: { type: 'string' } },
  guard: { type: 'string' }, guard_ok: { type: 'boolean' }, open_issues: { type: 'array', items: { type: 'string' } },
}, required: ['status', 'head', 'commits', 'sessions_used', 'scenarios', 'summary', 'defects', 'guard', 'guard_ok', 'open_issues'] }

const RERUN = `THIS IS A RE-RUN (decisions D52 and D53). Candidate 4 (9f6a2fad) failed or left open the rows below. Waves 15, 15a, 15b and 15c fixed those defects. Candidate 5 (0d06ab12) was never run live: its overnight chain, the independent audit (plans/sdd/V6-closeout/audit/goal-metrics-audit.md) and hosted CI found the defects wave 16 fixed. Read these first:
- plans/sdd/V6-closeout/live/report-c4.md: candidate 4's results, findings and audit;
- plans/sdd/V6-closeout/w15-*/report.md, w15a-*, w15b-*, w15c-* and w16-*: what changed;
- the ledger's D49, D50, D51, D52 and D53 rows: each disposition.
Re-run ONLY the rows named in your part, on this candidate and bundle, exactly as docs/uat.md and the checklist state them. Read the current text: several steps were revised under D46, D49, D50 and D53.
Write evidence under ${LIVE}/rerun-c7/<row>/ and never overwrite earlier evidence. In docs/uat.md, replace each re-run row's Result block with this candidate's result. Keep the existing history lines and add one line: "Candidate 4 (9f6a2fad): <its verdict> — <one-line reason>, evidence plans/sdd/V6-closeout/live/rerun-c4/<row>/", unless that line is already there.
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
If the budget runs short, run UAT-06's fork session last and record any skip.
RESUMED AGAIN (2026-10-02 15:05 EDT, coordinator): this part was interrupted twice, first by a usage limit and then by a network outage (API ENOTFOUND) at about 15:00 EDT. By then all 8 of its sessions had run (sessions.tsv sessions-c7 1-8; guard/sessions.txt has each check): UAT-03, UAT-04, UAT-06 and C4.5 are judged and committed (056c030f, 29099cc2, 8231c050, dc3f02d6). UAT-05 run 1 and its run 2 re-run (run2r, session 5) completed, and the first run 2 (session 4) is void; their evidence is uncommitted under ${LIVE}/rerun-c7/UAT-05/, and C4.3 has only a notes.txt. The session budget is spent: run NO new session. Judge UAT-05 and C4.3 from the existing evidence, write their Result blocks and notes, and commit. Then finish the part's end steps: a daemon of this lane is still running (qompack.exe daemon --project <scratch>/sessions/c7/uat03/recovery/proj, pid 81524, started 14:04 from ~/.qompack/bin/ab8046ba.../); stop only that process (match its full command line), then do the ~/.qompack cleanup under install.md section 6, run homeguard check and log the part's end. Return the scenario list with UAT-03, UAT-04, UAT-06 and C4.5 taken from their committed Result blocks.` },
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
Scenario ids to return: UAT-10, C4.9, C5.6. Every session uses --plugin-dir ${BUNDLE}.
UAT-10 (1 session): as docs/uat.md states it in its current text (status/dropped/eval envelopes, provenance, usage categories, the uncertainty check). Candidate 3 recorded a FAILING banner and a p95>max finding here: say whether each is gone.
C4.9 (2 sessions):
  - (a) The daemon ends mid-session. Terminate only a daemon you started, after checking its lock pid, image and command line. The host session must never break, and the next hook must recover.
  - (b) A newer settingsVersion in the project config: the documented refusal or degradation, and no broken session. Record the unavailable-object wording in a host session if reachable.
  - (c) was re-run on candidate 4 and passed; do not repeat it.
C5.6 (no session, last): aggregate this candidate's resource cost over EVERY session of this lane (all of ${LIVE}/rerun-c7/*/ that hold a meta.json or hooks.json, from every part), taken exactly as plans/sdd/V6-closeout/live/resources/C5.6/summary.md says under "How each number was taken". Write ${LIVE}/rerun-c7/C5.6/summary.md and data.json. Report:
  - store growth per session;
  - daemon working set and CPU;
  - host-seen latency per hook event (n, median, p95 and max over measured pairs; unmeasured pairs counted apart, never as 0 ms);
  - D53(i): every host-reported hook failure or timeout (a hook_response with a non-zero exit code, an error, blocked or timeout outcome, or a hook the host's stderr names as failed), each with its session and evidence file, and the total. The criterion is 0 except behaviour docs/cannot-do.md or docs/upstream-issues.md documents; say which, if any, are documented.
  Numbers only; no budget is proposed. Bundle sessions and the C4.8/UAT-12 previous-build session are kept apart.` },
]

const results = []
for (const p of PARTS) {
  const r = await agent(COMMON(p.key, p.part, p.budget), { label: `live:${p.key}`, phase: p.phase, schema: RESULT, effort: 'high' })
  results.push({ key: p.key, budget: p.budget, r })
  if (!r || r.guard_ok === false) break
}

const AUDIT = { type: 'object', properties: {
  verdict: { type: 'string', enum: ['sound', 'needs-fixes', 'unsound'] },
  coverage: { type: 'array', items: { type: 'object', properties: {
    item: { type: 'string' }, status: { type: 'string', enum: ['evidenced', 'partial', 'missing', 'failed'] }, evidence: { type: 'string' },
  }, required: ['item', 'status', 'evidence'] } },
  findings: { type: 'array', items: { type: 'object', properties: {
    severity: { type: 'string', enum: ['blocker', 'major', 'minor', 'nit'] }, location: { type: 'string' },
    issue: { type: 'string' }, evidence: { type: 'string' }, fix: { type: 'string' },
  }, required: ['severity', 'location', 'issue', 'evidence', 'fix'] } },
}, required: ['verdict', 'coverage', 'findings'] }

const audit = await agent(`You are an independent, adversarial auditor of the Qompack V6 live UAT lane. Worktree ${WT}, branch closeout/live7, candidate ${CAND}, bundle ${BUNDLE}. READ-ONLY: never edit, commit, run claude sessions, touch ~/.claude or ~/.qompack, or kill processes. The live parts returned: ${JSON.stringify(results.map((x) => ({ key: x.key, budget: x.budget, status: x.r && x.r.status, sessions_used: x.r && x.r.sessions_used, scenarios: x.r && x.r.scenarios, defects: x.r && x.r.defects, guard: x.r && x.r.guard, guard_ok: x.r && x.r.guard_ok })))}.
THIS IS A RE-RUN of the rows candidate 4 failed or left open plus D53(f)'s install, upgrade and restore rows (decisions D52, D53); evidence is under ${LIVE}/rerun-c7/. Coverage: for each of C4.1, C4.3, C4.4, C4.5, C4.6, C4.8, C4.9, C1.7 and D53(i) (host-reported hook failures and timeouts = 0 apart from documented host behaviour) (wording in ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md) name the evidence under ${LIVE}/ that proves it and mark it evidenced, partial, missing or failed; a part that never ran leaves its items missing.
For every re-run UAT Result block (UAT-01, 03, 04, 05, 06, 09, 10, 12), and the release entry's observed slash-command and MCP-tool namespace against docs/install.md and docs/commands.md in docs/uat.md: does the cited evidence exist and show what the block claims, field by field against the scenario's expected-result block? Is every deviation recorded as a finding rather than papered over, and no expectation edited? Is "Executed by" honest (agent-executed per D3, not human UAT)? C1.6: is "automatic recovery" stated honestly? Budgets: count claude processes (meta.json files, sessions.tsv) per part against its budget. Guard: does ${LIVE}/guard/*.txt show a passing check after every session and at each part's end? Secrets: search committed evidence for credential shapes, planted secrets (only @@SEC_PLANTED_n@@ may appear), raw transcripts, guard.json, credential files. Commit hygiene: \`git -C ${WT} log ${CAND}..HEAD\` has conventional subjects and no attribution trailers. Return the coverage table and findings with file:line and evidence.`,
  { label: 'audit:live', phase: 'Audit', schema: AUDIT, effort: 'high' })

return { results, audit }
