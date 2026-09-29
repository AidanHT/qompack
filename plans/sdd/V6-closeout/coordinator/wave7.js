export const meta = {
  name: 'v6-closeout-wave7',
  description: 'Wave 7 close-out: stray spawn claim (D27) + Defender docs (D32), EnsureLayout marker durability gap, SP08-D3 and C1.4/C1.5 confirmations; each reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'three workstreams off closeout/integration 898bb8b' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = '898bb8b'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w7'

const COMMON = (w) => `You are one of three parallel workstreams closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D32 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps; wave 6: w6-borrow, w6-config, w6-ckptsync, w6-gcserial, w6-linuxrows) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w7-${w.ws} on branch closeout/w7-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w7-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w7-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (two other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w7-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- CPU: the owner uses this laptop interactively. Any load generator you start must be bounded with \`timeout\` to at most 20 minutes per run and at most 8 busy processes (of 22 cores), and you must stop it when the measurement ends; never leave one running. Stop your own background diagnostics before you finish.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const WS = []

WS.push({ ws: 'spawnclaim', cid: 'C1.16/D27', effort: 'high', reviewEffort: 'high', task: `TASK — owner decision D27 (fix the stray spawn claim), the w6-borrow verify nit, and the D32 Defender documentation. Read plans/sdd/V6-closeout/w6-linuxrows/report.md (its needs_owner on the stray spawn claim and runs/review/f5-diag-spawnclaim_test.go.txt, the failing diagnostic) and plans/sdd/V6-closeout/w6-borrow/report.md first.
(1) D27: a daemon that loses daemon.lock leaves run/spawn.lock fresh for spawnLockStaleAfter (10 s), so a SessionEnd flush that arrives after the running daemon exits takes no claim and starts no daemon (ClaimSpawn answers SpawnInFlight where SpawnClaimed is expected). Land the diagnostic as a real failing test first, then fix at the source so the claim is given back: the losing daemon removes the claim when a live daemon holds the lock, and a daemon's Stop removes a claim it (or its spawner) owns — choose the smallest correct rule, keep D17's single-daemon guarantee (never let two daemons run for one project), and prove both with rows (a lost race frees the claim; a later flush after the running daemon exits starts exactly one daemon). Then check whether x09's wait for a stray claim to lapse (test/e2e/v3_x09_test.go, c551fcd) is still needed; remove it only if the fix makes it dead code, and keep the row's assertions.
(2) The w6-borrow verify nit: internal/cli/sessionstart.go (ensureDaemonRunning doc comment), internal/daemon/spawn.go (EnsureRunningUntil: "leaves the reply that bound in full") and internal/cli/hookbudget.go (borrowBy comment) still claim the reply keeps "at least"/"in full" the compact bound. Reword them the way docs/architecture.md now reads (about the compact bound, less the poll's return and the dial's transit; D29 accepted that edge).
(3) D32: Windows Defender's ML detection flags Qompack builds (Trojan:Win32/Bearfoos.A!ml / B!ml) and can block or quarantine qompack.exe. Document it in docs/troubleshooting.md (the symptom a user sees, how to verify the binary against the release checksums, how to report a false positive to Microsoft, and that an exclusion is the user's own decision), and record code signing as an open release item in docs/release.md. Do not change any Defender setting and do not submit anything to Microsoft.
Scope: internal/daemon (lock/spawn/stop), internal/ipc (spawn claim), test/e2e x09 helper only if dead, those three comments, docs/troubleshooting.md, docs/release.md.` })

WS.push({ ws: 'layout', cid: 'C1.6/D26', effort: 'high', reviewEffort: 'high', task: `TASK — the durability gap w6-ckptsync's verifier found, and its nit. Read plans/sdd/V6-closeout/w6-ckptsync/report.md (the Verify section) first.
(1) internal/paths/layout.go Barriers.EnsureLayout: the .qompack/.gitignore marker says "layout entries known durable", but it is never cleared when a later call creates a missing layout directory (a newer build adds one, or an operator removed one). If that call's parent sync fails, the daemon start fails, and the retry is a NEW process: it finds the directory present, its ledger state unknown and the layout marked, so it syncs nothing and returns nil — a name never made durable is reported durable. Write the failing test first (mark a layout, remove one layout directory, fail its parent sync, model a fresh process with a test-only ledger reset, and require the next EnsureLayout to sync that directory's parent), then fix with the smallest correct rule: e.g. remove the marker before creating any missing layout directory and rewrite it only after syncEntries succeeds (or drop the marker and sync the few layout parents on every EnsureLayout, which runs once per process start — pick by cost and simplicity and say why). Keep the doc comment at layout.go and the docs/architecture.md "Backup and restore" EnsureLayout sentence in line with the rule that ships.
(2) Nit: internal/store/lifecycle.go AppendRetentionRoot's doc comment still says the state directory is synced "when the append created the file"; under dd9444f the rule is "unless this process has already made the file's name durable" — reword or point to paths.AppendLinesDurable.
Run internal/paths, internal/store and internal/daemon in full on Windows and on Linux (the gate script) at your final HEAD.
Scope: internal/paths layout, internal/store lifecycle comment, docs/architecture.md.` })

WS.push({ ws: 'sp08d3', cid: 'C2.1/C1.4/C1.5', effort: 'medium', reviewEffort: 'medium', task: `TASK — three confirmations on the integrated candidate; change code only if a confirmation fails.
(1) C2.1 SP08-D3: read plans/CARRIED-DEFECTS.tsv, its detail document's SP08-D3 section, and the evidence test TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero. Establish whether the V6 prompt-replay recovery really fixes the defect (a replayed prompt is captured at the right turn) — run the evidence test and the related e2e/integration rows on Windows and Linux. If it is fixed, re-disposition the row to fixed exactly as the file's header and test/guards/carrieddefects_test.go require (evidence, detail-section update, and whatever the guard enforces for a fixed row; convert the evidence test so it asserts the fixed behaviour if it currently pins the defect), with the guard green for this row. If it is NOT fixed, say precisely why, with evidence, and leave the row deferred.
(2) C1.4: confirm 3dab390's two daemon fixture corrections (see plans/V6-CLOSEOUT-CHECKLIST.md in C:/Users/Quant/Documents/Programming/Projects/qompack-v6 and git show 3dab390) still hold on this candidate: the corrected rows pass on Windows and Linux, and the corrections did not weaken what the rows prove.
(3) C1.5 V6-AUTH-1/2 (inventory rows 1.13.4 and 1.17.12, historical FAIL): find the real-capture security regression tests those rows name (plans/sdd/V6-VERIFY*/inventory*, the V6 plan), run them on Windows and Linux, and report pass/fail without weakening.
For every confirmation, name the exact tests, commands, results, and the Phase 3 step (win-tree, win-race, linux-tree, linux-e2e, linux-timing, linux-e2e-timing) that will re-cover it on the frozen candidate.
Scope: plans/CARRIED-DEFECTS.tsv and its detail doc, the SP08-D3 evidence test, and code only if a confirmation fails.` })

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
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-7 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w7-${w.ws}, branch closeout/w7-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w7-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
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
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-7 workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w7-${r.ws}, branch closeout/w7-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w7-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: r.w.reviewEffort })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
