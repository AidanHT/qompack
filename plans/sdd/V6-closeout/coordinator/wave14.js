export const meta = {
  name: 'v6-closeout-wave14',
  description: 'Wave 14 close-out: observer segment tracking, decision ranking, response-bound cut (D46 follow-ups); reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'three workstreams off closeout/integration a8c7433' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = 'a8c7433'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w14'

const COMMON = (w) => `You are one of the parallel workstreams (three seats: observer, decisions, safecut) closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D38 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps; wave 6: w6-borrow, w6-config, w6-ckptsync, w6-gcserial, w6-linuxrows; wave 7: w7-spawnclaim, w7-layout, w7-sp08d3, w7-docs, w7b-selftest, w7b-checkpoint; wave 8: w8-sp08d3fix, w8-stagerace, w8b-polish) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w14-${w.ws} on branch closeout/w14-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w14-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w14-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (four other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w14-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- CPU: the owner uses this laptop interactively. Any load generator you start must be bounded with \`timeout\` to at most 20 minutes per run and at most 8 busy processes (of 22 cores), and you must stop it when the measurement ends; never leave one running. Stop your own background diagnostics before you finish.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const MACHINE = `MACHINE LIMITS FOR THIS SEAT (override the general rules): Phase 3 gates run on this laptop now and memory is tight. The Linux container is STOPPED on purpose: do NOT start it and do not run the Linux gate script; the coordinator runs Linux verification later. On Windows use \`-p 2\`, no load generators. The Phase 3 chain logs to C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/chain-2.log: while that file has a "win-timing start" line with no later "win-timing exit" line, an ISOLATED timing gate is running and you must not run anything load-bearing (no hot-path row, no test/integration package, no -race, no benchmark) until it ends; do code reading and cheap deterministic unit tests meanwhile. Run at most one load-bearing test at a time.`

const EV = `EVIDENCE: the Phase 4 live lane ran real Claude Code 2.1.280 sessions on the frozen candidate 3 (d5598eb4) with the packaged bundle. Its report is plans/sdd/V6-closeout/live/report.md in your worktree (evidence dirs beside it under plans/sdd/V6-closeout/live/uat/, c4/, recovery/). Read the rows named below and their evidence files before touching code. Coordinator decision D45 (ledger) says these are fixed before 0.3.0.
MACHINE LIMITS FOR THIS SEAT (override the general rules): DAYTIME, the owner is using this laptop and six seats run at once. Use \`-p 2\`, no load generators, no -race on whole packages, and do NOT run the hot-path rows (TestIntegration_HotPath*, TestV3_HotPath*) or whole test/integration / test/e2e packages; run only the focused rows you add or touch plus the touched packages in full once. The Linux container is STOPPED: do not start it. Real Claude Code sessions are NOT allowed: reproduce each live finding with a deterministic test (a real daemon/store/hook process in a temp project is fine) that is RED before the fix. The coordinator re-runs the live rows on the fixed candidate.
Other seats in this wave (do not fix their items; if your root cause turns out to be theirs, say so and stop): ledger (elimination ledger, MCP self-record turns, timeline), intent (rehydration intent/corrections/fork), restore (backup/restore/fsck segments/cross-version), pinsckpt (pins view, checkpoint pointers), diag (sentinel scan, status/doctor diagnostics), mcpresp (MCP response bounds/selectors/messages, UAT docs).`

const LIM = `MACHINE LIMITS FOR THIS SEAT (override the general rules): three seats run at once. Use \`-p 2\`, no load generators, no -race on whole packages, and do NOT run the hot-path rows (TestIntegration_HotPath*, TestV3_HotPath*) or whole test/integration / test/e2e packages; run the focused rows you add or touch and the touched packages in full once. The Linux container is STOPPED: do not start it. No real Claude Code sessions: reproduce with deterministic tests that are RED before the fix. Read coordinator decision D46 in the ledger. Other seats: observer, decisions, safecut.`

const WS = []

WS.push({ ws: 'observer', cid: 'C4.2/C4.3', effort: 'xhigh', reviewEffort: 'high', task: `TASK — the observer's segment tracking and the scheduler after a daemon restart (D46 follow-ups).
${LIM}
(1) Ticket from wave 13 (plans/sdd/V6-closeout/w13-pinsckpt/report.md open issues; reproduction plans/sdd/V6-closeout/w13-pinsckpt/runs/16-ticket-observer-stale-segment.go.txt and runs/17-ticket-observer-stale-segment-red.txt): the SP-08 observer adopts a segment only in OnSessionStart (ensureSegment). After any scheduler roll that no SessionStart follows (changepoint, todo, test, commit, and compact on a host-failed compaction), st.Segment still names the closed segment. SessionEnd then closes that id (a soft ErrAppendOnly), the successor stays open and is never encoded, DAG members keep enrolling against the old id, and the closed segment never gets a DAG segment node. Fix it so the observer learns about every roll (the scheduler/daemon notifies the observer, or the observer re-reads the session's current segment when it enrolls and at SessionEnd, carrying correct StartPos and Tokens for the node — the ticket warns that re-reading only at SessionEnd gives the wrong StartPos/Tokens). Turn the ticket's reproduction into a committed test, RED before the fix.
(2) After a mid-session daemon restart the scheduler runtime stays unbound for the rest of the session (Evaluate returns error_no_window and nothing is persisted) until a PreCompact binds it. Bind it when the restarted daemon first sees the live session (the first hook of that session), with a test.
Scope: internal/observer, internal/scheduler (or wherever the scheduler runtime lives), internal/daemon wiring, their tests.` })

WS.push({ ws: 'decisions', cid: 'C4.3/C4.4', effort: 'high', reviewEffort: 'high', task: `TASK — decision ranking and DAG nodes (D46 follow-ups from wave 13's ledger seat, plans/sdd/V6-closeout/w13-ledger/report.md open issues).
${LIM}
(1) The checkpoint's Advance decision source (b) mints decisions from other sessions' project-scoped eliminations and compares them by node turns that use those sessions' own turn numbering; for a session's first segment (from=0) every project-scoped elimination in the project becomes a candidate, so enough of them can push this session's own decisions out of the 64-decision cap. D46: this session's own decisions always rank before decisions derived from other sessions' project-scoped eliminations; foreign ones fill remaining room only, ordered by their recorded time (not by foreign turn numbers). Test: 64+ foreign project-scoped eliminations never displace the session's own decisions.
(2) Seal-time decisions (Draft.refreshNegativeKnowledge, added in wave 13) are minted without emitting the DAG KindDecision node that ExtractDecisions emits, so slice scoring cannot rank them. Emit the same node (idempotently: no duplicate when Advance later extracts the same decision), with a test.
Also: plans/V3-SP-09-negative-knowledge.md's Query pseudocode still says any hit with no visible candidate is bloom_only, while the code now answers a plain absence when a record in the ledger's view backs the hit: add a dated note under the pseudocode citing wave 13 (do not rewrite the plan).
Scope: internal/checkpoint (decisions, draft), internal/dag if needed, internal/negknow only if required, their tests; the plan note.` })

WS.push({ ws: 'safecut', cid: 'C4.4/C4.10', effort: 'medium', reviewEffort: 'high', task: `TASK — finish wave 13's MCP response bound and two doc items (D46).
${LIM}
(1) internal/mcp/response_bound.go safeCut backs off by doubling and gives up when keep-back <= 0; when a redacted region starts before keep/2 it skips every safe cut before the region, and boundedContent returns the tool error "no page of this content fits runtime.mcp.maxResponseBytes ... without cutting through content the privacy policy redacts" although a safe page exists (wave 13 verify finding in plans/sdd/V6-closeout/w13-mcpresp/report.md, with a probe: window = 630x'a' + BEGIN + 2100x'x' + END + 300x'b', keep=2730 returned ok=false while keep=630 is safe). Fix: when doubling would overshoot, search the interval below the last unsafe cut (binary search for the largest safe cut, or step back) and return false only when no rune boundary >0 is safe. Add rows reproducing the probe (RED first) to the existing safeCut/expand bound tests, keep TestExpandBoundCutNeverSplitsARedactedRegion green, and correct the safeCut comment.
(2) docs/uat.md: UAT-02 step 6 and UAT-12 steps 3-4 expect a Fidelity/coverage on expand/re_read responses; D46 says no retrieval response carries fidelity or coverage (docs/user-guide.md and mcp-tools.md already say so). Reword those expectations to what the shipped responses do carry (read internal/mcp and the docs), and fix the remaining "drop report is Qompack's recorded omissions with coverage attached" phrase (~docs/uat.md:1119). Record in each touched UAT block a one-line note "expectation revised under D46 (2026-09-29)". Do not change verdicts or other expectations.
Scope: internal/mcp/response_bound.go and its tests, docs/uat.md.` })

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
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-14 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w14-${w.ws}, branch closeout/w14-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w14-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
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
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-14 workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w14-${r.ws}, branch closeout/w14-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w14-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: r.w.reviewEffort })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
