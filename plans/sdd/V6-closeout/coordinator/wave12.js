export const meta = {
  name: 'v6-closeout-wave12',
  description: 'Wave 12 close-out: X11 paired baseline (D42), budget WARN (D43), spool-recovery docs (D44); reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'one workstream off closeout/integration cf727b7' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = 'cf727b7'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w12'

const COMMON = (w) => `You are one of the parallel workstreams (one seat, the only workstream running; daytime) closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D38 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps; wave 6: w6-borrow, w6-config, w6-ckptsync, w6-gcserial, w6-linuxrows; wave 7: w7-spawnclaim, w7-layout, w7-sp08d3, w7-docs, w7b-selftest, w7b-checkpoint; wave 8: w8-sp08d3fix, w8-stagerace, w8b-polish) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w12-${w.ws} on branch closeout/w12-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w12-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w12-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (four other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w12-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- CPU: the owner uses this laptop interactively. Any load generator you start must be bounded with \`timeout\` to at most 20 minutes per run and at most 8 busy processes (of 22 cores), and you must stop it when the measurement ends; never leave one running. Stop your own background diagnostics before you finish.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const MACHINE = `MACHINE LIMITS FOR THIS SEAT (override the general rules): Phase 3 gates run on this laptop now and memory is tight. The Linux container is STOPPED on purpose: do NOT start it and do not run the Linux gate script; the coordinator runs Linux verification later. On Windows use \`-p 2\`, no load generators. The Phase 3 chain logs to C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/chain-2.log: while that file has a "win-timing start" line with no later "win-timing exit" line, an ISOLATED timing gate is running and you must not run anything load-bearing (no hot-path row, no test/integration package, no -race, no benchmark) until it ends; do code reading and cheap deterministic unit tests meanwhile. Run at most one load-bearing test at a time.`

const WS = []

WS.push({ ws: 'x11pair', cid: 'C3.4/C5.1', effort: 'high', reviewEffort: 'high', task: `TASK — implement coordinator decisions D42, D43 and D44 (read them in the ledger) plus wave 11's comment nits.
MACHINE LIMITS FOR THIS SEAT (override the general rules): DAYTIME, the owner is using this laptop. You are the only workstream. The Linux container is STOPPED: do not start it or run the Linux gate script. On Windows use \`-p 2\`, no load generators; run each wall-clock row alone, at most twice in total per row.
Context: wave 11's report (C:/Users/Quant/Documents/Programming/Projects/qompack-cx-int/plans/sdd/V6-closeout/w11-babudget/report.md, its needs_owner and open_issues) has the evidence.
(D42) test/e2e/v3_x11_test.go TestV3_HotPathUnchangedWithLedgerResident: replace the V2-relative ceiling (x11V2BAp99Ms 3.072 x x11RegressionFactor 1.25) as the gate. In the same test, on the same host, run the hot-path harness twice: once without and once with the observer and the 5 000-entry ledger resident (reuse the existing setup; the no-ledger run mirrors test/integration's resident state or the X11 fixture minus the ledger; say which and why). Gate the ledger run's hook_controlled_observed p50 at <= x11RegressionFactor (1.25, existing) x the paired no-ledger run's p50; read the numbers from the harness's own JSON/notes (the "daemon-observed hook_controlled_observed" note), never from wall-clock samples. Keep every absolute gate (B-A/B-B/B-E against their limits, no spool in isolation). Report, not gate: both runs' B-A p99 and the V2 figure 3.072 ms with a note that V2's B-A excluded the handler and non-durable ingest. Co-load mode keeps its existing reporting behaviour. Put the pure comparison in a function with a fixture test (RED on a ledger p50 above the ceiling, GREEN below, refusing a missing note) the way w9's hotpathDaemonPopulations did. If running the harness twice makes the row exceed its -timeout=30m in ci.yml's e2e job on Windows, say so with the measured time.
(D43) internal/config: config.Load emits a WARN (the package's existing warning mechanism; find it) when the effective runtime.hotPath.budgetMs is below the effective runtime.budgets.l0IngestMs. Never clamp or change the value. Tests: WARN when a user sets budgetMs below l0IngestMs; no WARN for the defaults on every platform.
(D44) docs/troubleshooting.md: document that once the daemon degrades to spool submode it stays there until a new session starts or the daemon restarts (idle exit, or \`qompack daemon stop\` if that command exists; check the real CLI), that capture continues through the spool (nothing is lost), and how /qompack:status shows it. Keep it short and user-facing; check test/docs passes.
Nits (wave 11 review): internal/daemon/doc.go:4, internal/daemon/spawn.go:131, cmd/qompack/main.go:6 state B-A as a fixed 15 ms: make them say the per-platform default (15/50/40, D41). plans/QOMPACK-ERRATA.md:436 wording about which rows were red (the red log shows TestDefaults_RuntimeNamespace and CoversTheIngestBudget; PerPlatform did not compile at RED). plans/00-ARCHITECTURE.md:329 says "Owner decision D41": it is a coordinator decision under D33.
Verify on Windows: new rows -count=3; internal/config in full; test/docs; gen-config-docs --check; fmt-check, vet (windows/linux/darwin), the lint subset; then ALONE: \`go test -p 1 -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e\` without QOMPACK_UNDER_COLOAD (expect PASS; quote both runs' numbers). Report the exact Linux commands.
Scope: test/e2e/v3_x11_test.go (+ a helper file there), internal/config, docs/troubleshooting.md, the three doc comments, the two plan lines.` })

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
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-12 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w12-${w.ws}, branch closeout/w12-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w12-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
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
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-12 workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w12-${r.ws}, branch closeout/w12-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w12-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: r.w.reviewEffort })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
