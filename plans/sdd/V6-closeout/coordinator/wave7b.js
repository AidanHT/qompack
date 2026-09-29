export const meta = {
  name: 'v6-closeout-wave7b',
  description: 'Wave 7b close-out: self-test honours a disabled daemon (+ install/troubleshooting doc fixes), and /qompack:checkpoint does what it says or stops shipping; each reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'two workstreams off closeout/integration 898bb8b' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = '898bb8b'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w7b'

const COMMON = (w) => `You are one of two parallel workstreams (wave 7's three seats also run on this machine) closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D32 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps; wave 6: w6-borrow, w6-config, w6-ckptsync, w6-gcserial, w6-linuxrows) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w7b-${w.ws} on branch closeout/w7b-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w7b-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w7b-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (four other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w7b-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- CPU: the owner uses this laptop interactively. Any load generator you start must be bounded with \`timeout\` to at most 20 minutes per run and at most 8 busy processes (of 22 cores), and you must stop it when the measurement ends; never leave one running. Stop your own background diagnostics before you finish.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const WS = []

WS.push({ ws: 'selftest', cid: 'C6.1', effort: 'medium', reviewEffort: 'medium', task: `TASK — a product defect and two doc corrections found by the prep seats.
(1) \`qompack self-test\` calls daemon.EnsureRunning without checking runtime.daemon.enabled (internal/cli/selftest.go, around the EnsureRunning call), so it starts a resident daemon for a project configured with the daemon disabled, and that daemon can then drain the spool. docs/release.md §4 promises "runtime.daemon.enabled: false — no resident process and no lock file". Failing test first (a project whose config.json disables the daemon: self-test must start no daemon, create no lock file, and report the daemon check as skipped-by-configuration rather than failed), then fix. Check every other user command that can start a daemon (grep EnsureRunning / SpawnDetached / lazySpawn callers outside the hook path, e.g. mcp lazy spawn, status, doctor) for the same missing check and fix each real gap with a test; say why each remaining caller is correct.
(2) docs/install.md §3 is stale for Claude Code 2.1.280: \`claude plugin install\`, \`update\` and \`uninstall\` also take --json, and \`uninstall\` without --keep-data removes the plugin's data directory (~/.claude/plugins/data/<id>/). Correct it from the CLI's own help (\`claude plugin install --help\` etc.; never run a real session or change the real ~/.claude).
(3) docs/troubleshooting.md (around the spooled-start passages, ~lines 736 and 769): the client-spool watcher only runs after a served request kicks it, so a start spooled because no daemon was listening is picked up by the daemon's startup drain, not by the watcher; make the wording say exactly that (read internal/daemon's watcher and startup drain to be precise). Wave 7 edits the Defender section of that file; do not touch it.
Scope: internal/cli (self-test and any other daemon-starting command), tests, docs/install.md, docs/troubleshooting.md.` })

WS.push({ ws: 'checkpoint', cid: 'C4.5/SP-14 H3', effort: 'high', reviewEffort: 'high', task: `TASK — the /qompack:checkpoint slash command ships but does not do what it says. plugin/commands/checkpoint.md says "Write an immutable checkpoint now" and runs \`qompack checkpoint $ARGUMENTS\`, but \`qompack checkpoint\` is the PreCompact hook entry point (it reads a hook event from stdin and always exits 0), so the command writes nothing; docs/commands.md marks it "not yet routed" (SP-14 handoff edge H3, arch/checkpoint-now-subcommand). A public release must not ship a command that silently does nothing.
Decide by what serves users, and do not over-engineer: (A) route it for real, if the existing checkpoint machinery can seal a checkpoint for the project's current session on demand with a small, well-tested change (how does the command learn the session — e.g. the most recent active session the daemon knows for this project, or a session id the host exposes to slash commands; verify against the Claude Code 2.1.280 docs/CLI help, never run a real session). The hook binding must keep working: if the PreCompact hook and the user command need different entry points, change the shipped hooks manifest and every test/doc that names it consistently, and keep old installed hook commands working if an upgrade could see them. Or (B) if (A) needs more than a small change, stop shipping the command: remove plugin/commands/checkpoint.md, regenerate the command docs with the generator, update every count/list of commands (docs, plugin-validate expectations, tests, the live-lane coverage in plans/sdd/V6-closeout/coordinator/live-uat.js is NOT in your tree — just report that the count changed), and say in docs/cannot-do.md that checkpoints are written automatically before each compaction and a manual checkpoint is not offered yet.
Write down which option you chose and why (cost, risk, user value). Failing test first for whichever behaviour ships. Run internal/cli, internal/commands, internal/checkpoint, internal/daemon, test/docs and the plugin validation (\`go run ./tools/devtool plugin-validate\`, \`claude plugin validate <bundle dir>\` on a bundle you build into your scratch dir) at your final HEAD, and the Linux gate for the touched packages.
Scope: plugin/commands, plugin hooks manifest only if (A) requires it, internal/cli, internal/commands, internal/checkpoint/daemon only for (A), docs (commands via generator, cannot-do, user-guide), tests.` })

const RESULT = { type: 'object', properties: {
  status: { type: 'string', enum: ['done', 'partial', 'blocked'] }, head: { type: 'string' },
  commits: { type: 'array', items: { type: 'string' } }, root_cause: { type: 'string' }, summary: { type: 'string' },
  tests: { type: 'array', items: { type: 'object', properties: { command: { type: 'string' }, result: { type: 'string' } }, required: ['command', 'result'] } },
  criterion_changes: { type: 'array', items: { type: 'string' } }, open_issues: { type: 'array', items: { type: 'string' } },
  needs_owner: { type: 'array', items: { type: 'string' } },
}, required: ['status', 'head', 'commits', 'summary', 'tests', 'open_issues', 'needs_owner'] }
const FINDINGS = { type: 'object', properties: {
  verdict: { type: 'string', enum: ['sound', 'needs-fixes', 'unsound'] },
  findings: { type: 'array', items: { type: 'object', properties: {
    severity: { type: 'string', enum: ['blocker', 'major', 'minor', 'nit'] }, location: { type: 'string' },
    issue: { type: 'string' }, evidence: { type: 'string' }, fix: { type: 'string' },
  }, required: ['severity', 'location', 'issue', 'evidence', 'fix'] } },
}, required: ['verdict', 'findings'] }

const LENS = 'Lenses: (a) root cause proven and fixed at the source; correctness incl. concurrency (races, deadlocks, lock order, goroutine leaks), durability/crash semantics (fsync ordering, WAL retention, idempotency, identity never re-minted), fail-closed paths, Windows vs POSIX, bounded memory/CPU; (b) check-weakening (deleted/loosened assertions, skips, nolint/nomagic, lowered thresholds, regenerated goldens, widened timeouts, tests bent to buggy output) and legitimacy of every criterion change; (c) every new bound/budget number is named, derived, and listed for the owner; (d) docs consistent with behaviour, generated docs via generators; (e) commit hygiene (conventional subjects, Refs footer, no attribution trailers); (f) was the whole task done, or parts silently dropped?'

const results = await pipeline(
  WS,
  (w) => agent(`${COMMON(w)}\n\n${w.task}`, { label: `impl:${w.ws}`, phase: 'Implement', schema: RESULT, effort: w.effort }),
  async (impl, w) => {
    if (!impl) return { w, impl: null, review: null }
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-7b workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w7b-${w.ws}, branch closeout/w7b-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w7b-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
      { label: `review:${w.ws}`, phase: 'Review', schema: FINDINGS, effort: w.reviewEffort })
    return { w, impl, review }
  },
  async (r) => {
    if (!r.impl) return { w: r.w, ws: r.w.ws, impl: null, review: null, final: null }
    const actionable = (r.review ? r.review.findings : []).filter((f) => f.severity !== 'nit')
    if (actionable.length === 0) return { w: r.w, ws: r.w.ws, impl: r.impl, review: r.review, final: r.impl, fixRan: false, actionable }
    const final = await agent(`${COMMON(r.w)}\n\nYou are the FIX seat for this workstream (the implementer has finished). Its task was:\n---\n${r.w.task}\n---\nIndependent reviewer findings:\n${JSON.stringify(actionable, null, 1)}\nVerify each independently; fix the correct ones (failing test first where applicable) and re-run focused tests; rebut wrong ones with evidence. Blockers/majors must be resolved or rebutted. Return the final state with a "Review resolution" section (finding -> action/rebuttal) in your summary, and carry forward every needs_owner item still open (the implementer's were: ${JSON.stringify(r.impl.needs_owner || [])}).`,
      { label: `fix:${r.w.ws}`, phase: 'Fix', schema: RESULT, effort: r.w.effort })
    return { w: r.w, ws: r.w.ws, impl: r.impl, review: r.review, final, fixRan: true, actionable }
  },
  async (r) => {
    if (!r.fixRan || !r.final) return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: !!r.fixRan, verify: null }
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-7b workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w7b-${r.ws}, branch closeout/w7b-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w7b-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: r.w.reviewEffort })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
