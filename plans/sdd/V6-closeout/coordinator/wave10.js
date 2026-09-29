export const meta = {
  name: 'v6-closeout-wave10',
  description: 'Wave 10 close-out: lost hot-path events under load (lostev) and D39 co-load spool reporting (coloadspool); reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'two workstreams off closeout/integration 8245d4e' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = '8245d4e'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w10'

const COMMON = (w) => `You are one of the parallel workstreams (two seats: lostev and coloadspool; Phase 3 gates run on this machine at the same time) closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D38 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps; wave 6: w6-borrow, w6-config, w6-ckptsync, w6-gcserial, w6-linuxrows; wave 7: w7-spawnclaim, w7-layout, w7-sp08d3, w7-docs, w7b-selftest, w7b-checkpoint; wave 8: w8-sp08d3fix, w8-stagerace, w8b-polish) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w10-${w.ws} on branch closeout/w10-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w10-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w10-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (four other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w10-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- CPU: the owner uses this laptop interactively. Any load generator you start must be bounded with \`timeout\` to at most 20 minutes per run and at most 8 busy processes (of 22 cores), and you must stop it when the measurement ends; never leave one running. Stop your own background diagnostics before you finish.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const MACHINE = `MACHINE LIMITS FOR THIS SEAT (override the general rules): Phase 3 gates run on this laptop now and memory is tight. The Linux container is STOPPED on purpose: do NOT start it and do not run the Linux gate script; the coordinator runs Linux verification later. On Windows use \`-p 2\`, no load generators. The Phase 3 chain logs to C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/chain-2.log: while that file has a "win-timing start" line with no later "win-timing exit" line, an ISOLATED timing gate is running and you must not run anything load-bearing (no hot-path row, no test/integration package, no -race, no benchmark) until it ends; do code reading and cheap deterministic unit tests meanwhile. Run at most one load-bearing test at a time.`

const WS = []

WS.push({ ws: 'lostev', cid: 'C3.4/C5.1', effort: 'xhigh', reviewEffort: 'high', task: `TASK — root-cause and fix a possible LOST-event defect.
${MACHINE}
Finding (wave 9, evidence in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w9-testfix/plans/sdd/V6-closeout/w9-testfix/runs/09-* and report.md there): a full \`QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=180m -json ./test/integration\` on Windows, under heavy Phase 3 load, failed TestIntegration_HotPathWarmWithRealResidentState on the harness's delivery-integrity guard (test/bench/hotpath/delivery.go ~281): "of the 2130 hot-path requests this harness sent, the daemon's own l0_ingest histogram observed 1537 and only 575 are accounted for by a deferred request line in the client spool — 18 are LOST". Runs of the row alone deferred 593 with 0 lost. In these runs the daemon's §8.1/§12.2 breach detector moved to spool submode after 3 x 512 over-budget windows (w9 root cause), so the hooks after that point spool.
Decide from code and evidence, not guesses, which of these it is (or something else):
(a) a REAL drop in the product: internal/ipc/client.go appendToSpool / spool.go Append+writeLocked (AppendOnly open failing, e.g. a Windows sharing violation against the daemon's concurrent client-spool drain or rename, ErrSpoolFull, the sticky s.err, a size refusal), or the daemon's client-spool watcher/drain losing, truncating or double-consuming lines while a hook still appends (internal/daemon client-spool watcher, drain, orderClientSpoolsByHostTS placement), or a hook process exiting before its append is flushed;
(b) a HARNESS accounting race: e.g. the daemon's client-spool watcher drains and removes spool lines during the run, so a request replayed from the spool is neither in the l0_ingest hot-path histogram nor still in a spool file when the harness counts, making a delivered event look LOST.
Useful discriminators: the l0_dropped counter and any "spool write failed"/"spool append refused" LOUD line in the run's daemon/hook logs; whether the harness counts spool lines before or after the drain; whether those 18 requests' ids are in the store (captured) at the end.
If (a): fix the product so an event is never silently lost (durable append that retries/reopens on a transient Windows sharing violation, or a drain that never removes a line it did not consume, whichever is the actual cause), with a deterministic regression test (fault injection or a forced interleaving) that is RED before the fix. If (b): fix the harness so it accounts replayed-from-spool events correctly (e.g. counts spool lines the drain consumed, or snapshots before the drain can run), still refusing to report when an event is truly lost, with a unit test for the accounting, and state plainly that the product never lost an event and how you proved it.
Do not touch any budget or gating rule; do not edit test/integration/hotpath_test.go (another seat owns it). Scope: internal/ipc, internal/daemon (client-spool drain/watcher), test/bench/hotpath, their tests.
Verify: the touched packages in full once (-p 2), the new rows -count=5, and ONE reproduction attempt of the full condition if the timing gate is not running: \`QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=60m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration\` (it may still fail on the no-spool assertion, which seat coloadspool changes under D39; judge the LOST guard only). Report the exact Linux commands the coordinator should run.` })

WS.push({ ws: 'coloadspool', cid: 'C3.4', effort: 'high', reviewEffort: 'high', task: `TASK — implement owner decision D39 (read it in the ledger) in test/integration/hotpath_test.go, plus wave 9's two comment nits.
${MACHINE}
Background (wave 9 report: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w9-testfix/plans/sdd/V6-closeout/w9-testfix/report.md): TestIntegration_HotPathWarmWithRealResidentState asserts, in every mode, that the daemon never moved to the §12.2 spool submode (sawSpool false, no degraded WARN, nothing in LOUD.log; ~hotpath_test.go:1091-1112, "state.bin must report hot=0 (sync) for the entire measured run"). On a loaded host the breach detector (internal/daemon/handlers.go applyHotPathTransition, BreachWindows 3 x sampleWindow 512) correctly moves to spool, so the co-load run fails on a budget consequence that co-load mode waives (ADR 0010).
D39: in co-load mode (QOMPACK_UNDER_COLOAD=1) a spool transition is REPORTED (t.Log the transition, the window count and the B-A/B-B delivered numbers), not failed; but when it happens the test still requires (1) that it is loud and named: the degraded WARN and the LOUD.log entry the product emits for a breach transition are present and name the breach (assert their presence, not their absence), (2) the delivery ledger adds up with 0 lost (the harness already refuses to report otherwise; keep that), and (3) every other assertion of the row unchanged. In isolated mode nothing changes: the transition, the WARN and the LOUD.log entry stay forbidden. Read how the test distinguishes the modes today and reuse that; do not add a new mode or env var. If the product emits no WARN/LOUD line naming the breach, say so and assert what it does emit (state.bin hot=1 plus the breach counter), do not change the product.
Nits (wave 9 verify): the in-test comment ~hotpath_test.go:1008-1009 still says the deferrals are "the ACK deadline expiring"; name the breach detector's move to spool submode (or an ACK deadline expiring). Re-wrap the ~190-column comment line ~1012 to the block's width.
Add a fixture-level unit test for the mode split if the assertion logic can be factored into a pure function (as w9 did with hotpathDaemonPopulations); otherwise explain why not. Criterion change: say exactly what co-load now proves and that isolated is unchanged.
Verify on Windows: the new/changed focused rows -count=3, \`go vet\`, fmt-check, the lint subset (golangci-lint,nomagic,runpatterns,docmarkers,sleepcheck), and ONE co-load run of the row when the timing gate is not running: \`QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=60m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration\`. Scope: test/integration/hotpath_test.go only (plus a helper file in test/integration if needed).` })

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
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-10 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w10-${w.ws}, branch closeout/w10-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w10-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
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
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-10 workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w10-${r.ws}, branch closeout/w10-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w10-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: r.w.reviewEffort })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
