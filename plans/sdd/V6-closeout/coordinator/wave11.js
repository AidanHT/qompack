export const meta = {
  name: 'v6-closeout-wave11',
  description: 'Wave 11 close-out: platform-derived B-A budget (D41); reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'one workstream off closeout/integration fc5289c' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = 'fc5289c'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w11'

const COMMON = (w) => `You are one of the parallel workstreams (one seat, the only workstream running tonight) closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D38 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps; wave 6: w6-borrow, w6-config, w6-ckptsync, w6-gcserial, w6-linuxrows; wave 7: w7-spawnclaim, w7-layout, w7-sp08d3, w7-docs, w7b-selftest, w7b-checkpoint; wave 8: w8-sp08d3fix, w8-stagerace, w8b-polish) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w11-${w.ws} on branch closeout/w11-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w11-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w11-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (four other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w11-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- CPU: the owner uses this laptop interactively. Any load generator you start must be bounded with \`timeout\` to at most 20 minutes per run and at most 8 busy processes (of 22 cores), and you must stop it when the measurement ends; never leave one running. Stop your own background diagnostics before you finish.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const MACHINE = `MACHINE LIMITS FOR THIS SEAT (override the general rules): Phase 3 gates run on this laptop now and memory is tight. The Linux container is STOPPED on purpose: do NOT start it and do not run the Linux gate script; the coordinator runs Linux verification later. On Windows use \`-p 2\`, no load generators. The Phase 3 chain logs to C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/chain-2.log: while that file has a "win-timing start" line with no later "win-timing exit" line, an ISOLATED timing gate is running and you must not run anything load-bearing (no hot-path row, no test/integration package, no -race, no benchmark) until it ends; do code reading and cheap deterministic unit tests meanwhile. Run at most one load-bearing test at a time.`

const WS = []

WS.push({ ws: 'babudget', cid: 'C1/C5.1', effort: 'high', reviewEffort: 'high', task: `TASK — implement owner decision D41 (read it in the ledger): B-A's default budget becomes platform-derived.
MACHINE LIMITS FOR THIS SEAT (override the general rules): you are the ONLY workstream running (night). The Linux container is STOPPED: do not start it or run the Linux gate script; the coordinator verifies on Linux later. On Windows you may use \`-p 4\`; no load generators; run wall-clock rows alone (nothing else of yours running at the same time).
The defect: the daemon's B-A sample (internal/daemon/handlers.go recordHotPathSample) is observed (recvTS-reqTS) + handler time + hotPathTailAllowance, so it contains B-B's durable l0 ingest by construction. B-B's limit is per platform (internal/config/deadlines.go L0IngestMsPortable 15, L0IngestMsWindows 50, L0IngestMsDarwin 40, from the V5 ruling that kept fsync-before-ACK), but runtime.hotPath.budgetMs (internal/config/defaults.go HotPath.BudgetMs: 15) is 15 on every platform, and it is both B-A's gated limit (internal/obs/budgets.go) and the §8.1 breach detector's limit (internal/daemon/budget.go). Measured on this Windows laptop in isolated runs of the frozen candidate: B-A p50 15.4-16.4 ms, p99 22.5-26.6 ms, B-B p99 12.3 ms, hook_controlled_observed p99 6.1 ms; the breach detector moves every run to spool submode after exactly 3 x 512 samples (evidence: C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/p3-win-timing.log and p3-win-e2e-timing.log). So every real Windows session with more than ~1.5k tool uses goes degraded, with a WARN and a LOUD.log entry.
D41: runtime.hotPath.budgetMs defaults to max(15, the platform's L0IngestMs default): Linux/portable 15, Windows 50, darwin 40. Implement it the way ackDeadlineMsDefault()/the L0IngestMs platform defaults are implemented (a named function, no new literal: compose the existing constants; a derivation comment citing D41 and why B-A must not be tighter than the B-B it contains). A value the user sets in config stays exactly as set. Update everything that states the old universal 15 ms default or "B-A p99 < 15 ms" as a fixed number: generated config docs (gen-config-docs; keep --check green), docs/ (configuration, performance/troubleshooting pages that quote it), 00-ARCHITECTURE.md §2.4/§8.1 if they state it, Qompack.md with a revision-log entry citing D41 (the guard TestQompackChangesOnlyThroughAnAuthorizedRevision defines the format), and test messages in test/integration/hotpath_test.go and test/e2e/v3_x11_test.go that hard-code "B-A p99<15ms" (derive the text from the budget instead). Do not change B-B, the ACK deadline, BreachWindows, the certification rule or any other budget.
Regression tests first (RED before the fix on windows and darwin, checkable on Windows by building/vetting for GOOS=darwin and by unit-testing the pure default function with each platform's inputs): for every platform the default B-A limit is >= the default B-B limit; the Windows default is 50 and darwin 40 and Linux 15; a user-set budgetMs is kept.
Also INVESTIGATE (report only; change nothing unless it is a one-line, obviously safe fix): once the daemon is in spool submode, can it ever return to sync? ToSync needs \`need\` consecutive clean 512-sample windows; do hooks in spool submode still send requests that produce hot-path samples, or do they spool without contacting the daemon (read internal/ipc/client.go, internal/cli hook entry, state.bin handling)? If it can never recover within a daemon's lifetime, say what bounds it (idle exit after runtime.daemon.idleExitSeconds, restart) with evidence; the coordinator decides.
Verify on Windows: the new rows -count=3; internal/config, internal/obs, internal/daemon, internal/commands and internal/ipc in full once (-p 4); gen-config-docs/gen-mcp-docs/gen-command-docs --check; go test ./test/docs; test/guards (the only expected failure is TestCarriedDefects_WaveReportRequiresResolution's five perf rows); then, ALONE and with nothing else running, \`go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration\` and \`go test -p 1 -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e\` without QOMPACK_UNDER_COLOAD: expect both to pass with no spool transition (quote the B-A/B-B rows). If B-A still breaches 50 ms on Windows, stop and report the numbers; do not raise anything further. Report the exact Linux commands the coordinator should run.
Scope: internal/config (defaults/deadlines and their tests), docs and generated docs, Qompack.md, plans/00-ARCHITECTURE.md, test messages named above; internal/obs or internal/daemon only if they duplicate the default.` })

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
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-11 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w11-${w.ws}, branch closeout/w11-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w11-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
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
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-11 workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w11-${r.ws}, branch closeout/w11-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w11-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: r.w.reviewEffort })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
