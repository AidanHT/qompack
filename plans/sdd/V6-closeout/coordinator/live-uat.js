export const meta = {
  name: 'v6-closeout-live-uat',
  description: 'Phase 4: agent-run UAT-01..12 and installed-host proof on the frozen candidate, sequential real sessions, then an evidence audit',
  phases: [
    { title: 'Install', detail: 'UAT-01, C4.1 marketplace discovery, C4.8 upgrade/uninstall/reinstall, UAT-12' },
    { title: 'Sessions', detail: 'UAT-02..06 (capture, checkpoint, compaction, rehydration, supersession)' },
    { title: 'Retrieval', detail: 'UAT-07..11 (MCP retrieval, eliminations, staleness, observability, admission off)' },
    { title: 'Audit', detail: 'independent read-only audit of every Result block against its evidence' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const WT = `${ROOT}/qompack-cx-live`
const CAND = '__CANDIDATE__'
const BUNDLE = '__BUNDLE__'

const COMMON = (part) => `You are the V6 close-out LIVE lane for Qompack (a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under <project>/.qompack/, an MCP retrieval server "qompack mcp", slash commands). You run REAL headless Claude Code sessions on the owner's own Windows machine to prove the plugin works as installed. Owner decision D3: these runs are AGENT-EXECUTED on the real installed host, never human UAT; every record says so.

WORKTREE: ${WT} (branch closeout/live at the frozen candidate ${CAND}). The Bash tool resets cwd: prefix commands with \`cd ${WT} &&\`. Frozen bundle (do not rebuild it): ${BUNDLE}. Coordinator ledger (read, do not edit): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md. Read docs/uat.md, docs/install.md, docs/backup.md, docs/user-guide.md, docs/mcp-tools.md and docs/troubleshooting.md first. Reuse the committed tooling: plans/sdd/V6-closeout/eval/runs/smoke_driver.py (stream-json driver), plans/sdd/V6-closeout/eval/runs/homeguard.py (real-home fingerprint), plans/sdd/V6-closeout/eval/runs/marketplace-rehearsal.md (the local-marketplace flow and what it leaves behind), tools/devtool/liveeval*.go (the reference implementation of both).

YOUR PART: ${part}

Host facts (Claude Code 2.1.280, verified by earlier lanes): drive sessions with \`claude -p --input-format stream-json --output-format stream-json --verbose --include-hook-events --setting-sources project,local --permission-mode dontAsk --allowedTools <list>\`, env ENABLE_CLAUDEAI_MCP_SERVERS=false; send one user message per step and wait for its result; \`/compact\` as a user turn compacts headlessly; CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 plus CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=30 forces auto-compaction early; plugin MCP tools are named mcp__plugin_<plugin>_<server>__<tool> (read the exact names from the stream's init event); plugin slash commands are /qompack:<name>. A disposable CLAUDE_CONFIG_DIR cannot authenticate, so sessions use the operator's real ~/.claude.

HARD RULES (owner directives):
- Real-home safety: before your first session snapshot the fingerprint (homeguard.py snap); after EVERY session and install step run the check. ~/.claude/settings.json, plugins/installed_plugins.json and plugins/known_marketplaces.json must end byte-identical, and plugins/{cache,marketplaces,data} may only lose what your run created. NEVER copy, read into logs, or modify credential files (.credentials.json or similar). NEVER install at user scope; marketplace installs use --scope local in a disposable project (see the rehearsal). If a check fails, stop, restore only what your run changed, and report.
- Session budget for this part: at most __BUDGET__ real sessions (a session = one claude process that talks to the model); count every one, including failed ones, in your report. Model: claude-haiku-4-5-20251001 unless a step genuinely needs a stronger model (then claude-sonnet-5 and say why).
- One disposable project per scenario, outside any git repository, under your own scratch dir. Never run anything against a real project. Terminate only daemons your scenario started, identified by project path in .qompack/run/daemon.lock AND process image; prefer the documented shutdown/idle exit.
- Never kill a process you did not start. No git push/tag/merge; commit only in ${WT}. Conventional commits (\`docs(uat): ...\`, \`test(...)\`), subject <=64 chars, body lines <=100, NO attribution trailers (no Co-Authored-By etc.; the hook rejects them).
- Evidence: plans/sdd/V6-closeout/live/uat/UAT-NN/ (stream.jsonl, driver config with <scratch> placeholders instead of real temp paths, command outputs with exit codes, .qompack/ listings, fsck/status/doctor outputs, homeguard before/after). Keep failures exactly like passes. Scrub nothing but the scratch path prefix.
- Fill each scenario's Result block in docs/uat.md from what you observed: Result pass|fail|skipped — <reason>; Snapshot: qompack version, bundle BUNDLE.json sha256 and commit ${CAND}, Windows 11 build, Claude Code 2.1.280; Date 2026-09-.. (America/Toronto); Executed by: "Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host per owner decision D3 — not human UAT"; Evidence path; Rollback verified per the doc's rule. Read each expected-result block field by field. A result that differs from the doc is a FINDING: record it, do not edit the expectation to match. If a product defect blocks a step, record the step, stop that scenario, and report the defect with evidence (the coordinator routes fixes).
- Do not change product code or tests. Documentation edits are limited to the Result blocks and to recording observed strings the doc marks "to be confirmed at execution".
- Subagents cannot write report.md files: return your full report (per scenario: result, sessions used, evidence path, findings with evidence; guard results; open issues) as your result; the coordinator commits it.`

const RESULT = { type: 'object', properties: {
  status: { type: 'string', enum: ['done', 'partial', 'blocked'] }, head: { type: 'string' },
  commits: { type: 'array', items: { type: 'string' } }, sessions_used: { type: 'number' },
  scenarios: { type: 'array', items: { type: 'object', properties: {
    id: { type: 'string' }, result: { type: 'string' }, evidence: { type: 'string' }, findings: { type: 'array', items: { type: 'string' } },
  }, required: ['id', 'result', 'evidence', 'findings'] } },
  summary: { type: 'string' }, defects: { type: 'array', items: { type: 'string' } },
  guard: { type: 'string' }, open_issues: { type: 'array', items: { type: 'string' } },
}, required: ['status', 'head', 'commits', 'sessions_used', 'scenarios', 'summary', 'defects', 'guard', 'open_issues'] }

const PARTS = [
  { key: 'install', phase: 'Install', budget: 8, part: `UAT-01 (install and record what is installed, via the LOCAL marketplace flow at --scope local, per marketplace-rehearsal.md; confirm the host discovered the seven hooks, the qompack MCP server with its eight tools, and the seven /qompack: commands — C4.1), C4.8 (upgrade: install an older bundle first — the previous build is local develop 301a8e9 (the pre-close-out V6 integration; no tag carries the bundle tool): \`git -C ${WT} worktree add --detach <scratch>/prev 301a8e9\`, then \`go run ./tools/devtool bundle --target windows/amd64 --version 0.2.99-prev --out <scratch>/old\` there — do real work in a session, upgrade to the frozen bundle, verify project work is preserved and fsck is clean; uninstall; reinstall; remove the scratch worktree afterwards with \`git worktree remove\`), and UAT-12 (privacy and lifecycle edges, including planted secrets that must never reach any durable surface, a deny-ruled archived read refused by every retrieval form — use a PROJECT-scope .claude/settings.json deny rule inside the disposable project, never the user's settings — and backup -> restore -> fsck certified per docs/backup.md).` },
  { key: 'sessions', phase: 'Sessions', budget: 12, part: `UAT-02, UAT-03, UAT-04, UAT-05, UAT-06 with --plugin-dir pointing at the frozen bundle (C4.2 every hook event captured and index counts match the transcript; C4.3 real compaction round trip — PreCompact checkpoint, SessionStart source=compact rehydration injected under the host's 10,000-character cap, the model recovers pre-compaction facts; the SessionEnd hook must not be reported "Hook cancelled"; C4.9 degraded paths — daemon killed mid-session, unknown schema, unavailable object — never break the host session). Record the hook latencies the host reports (durationMs in the hook events).` },
  { key: 'retrieval', phase: 'Retrieval', budget: 12, part: `UAT-07, UAT-08, UAT-09, UAT-10, UAT-11 with --plugin-dir pointing at the frozen bundle (C4.4: the real model calls each MCP tool — recall, expand, re_read, already_tried, record_eliminated, timeline, why, dropped — and gets correct results AND correct errors; C4.5: the /qompack: slash commands run in a real session and match docs/commands.md; C4.7 kill switches: recording off and reinjection off, each independent, via project config).` },
]

const results = []
for (const p of PARTS) {
  const prompt = COMMON(p.part).replace('__BUDGET__', String(p.budget))
  const r = await agent(prompt, { label: `live:${p.key}`, phase: p.phase, schema: RESULT })
  results.push({ key: p.key, r })
}

const AUDIT = { type: 'object', properties: {
  verdict: { type: 'string', enum: ['sound', 'needs-fixes', 'unsound'] },
  findings: { type: 'array', items: { type: 'object', properties: {
    severity: { type: 'string', enum: ['blocker', 'major', 'minor', 'nit'] }, location: { type: 'string' },
    issue: { type: 'string' }, evidence: { type: 'string' }, fix: { type: 'string' },
  }, required: ['severity', 'location', 'issue', 'evidence', 'fix'] } },
}, required: ['verdict', 'findings'] }

const audit = await agent(`You are an independent, adversarial auditor of the Qompack V6 live UAT lane. Worktree ${WT}, branch closeout/live, candidate ${CAND}. READ-ONLY: never edit, commit, run claude sessions, or kill processes. The three live agents returned: ${JSON.stringify(results.map((x) => ({ key: x.key, status: x.r && x.r.status, scenarios: x.r && x.r.scenarios, defects: x.r && x.r.defects, guard: x.r && x.r.guard })))}.
For every UAT-01..12 Result block in docs/uat.md: does the cited evidence under plans/sdd/V6-closeout/live/uat/ exist and actually show what the block claims, field by field against the scenario's expected-result block? Is every deviation recorded as a finding rather than papered over? Is "Executed by" honest (agent-executed per D3, not human UAT)? Were the session budgets respected (count claude processes in the evidence)? Did the real-home guard pass after every session (look at the recorded fingerprints)? Are credentials, real user paths or secrets present in committed evidence? Check commit hygiene (no attribution trailers). Return findings with file:line and evidence.`,
  { label: 'audit:live', phase: 'Audit', schema: AUDIT, effort: 'high' })

return { results, audit }
