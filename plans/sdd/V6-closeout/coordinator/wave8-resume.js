export const meta = {
  name: 'v6-closeout-wave8-resume',
  description: 'Wave 8 close-out (resumed after the owner paused the session): SP08-D3 host-order replay and honest substitution notice (D35), Windows concurrent staging race + spawn-claim nits; each reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'two workstreams off closeout/integration 17a42f6' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = '17a42f6'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w8'

const COMMON = (w) => `You are one of the parallel workstreams (two seats in this wave; wave 7b's two seats also run on this machine) closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D35 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps; wave 6: w6-borrow, w6-config, w6-ckptsync, w6-gcserial, w6-linuxrows; wave 7: w7-spawnclaim, w7-layout, w7-sp08d3, w7-docs) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w8-${w.ws} on branch closeout/w8-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w8-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w8-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (four other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w8-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- CPU: the owner uses this laptop interactively. Any load generator you start must be bounded with \`timeout\` to at most 20 minutes per run and at most 8 busy processes (of 22 cores), and you must stop it when the measurement ends; never leave one running. Stop your own background diagnostics before you finish.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`


const DIGEST = 'C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/coordinator/digests'
const NOTES = {
  sp08d3fix: 'At the pause the earlier seat had committed eight commits (da4b05e EarliestPrompt capability, ad298a0 host-time prompt stamps and late-capture notes, 6d013ef rehydrator substitution naming, 76b1d94 host-order client-spool replay, 3881a1a X1 comments, 0873e7e docs, 42f01d6 SP08-D3 moved to fixed, ed4f8d4 guard classification) and was running its verification; nothing was reviewed. Re-check that the row-to-fixed move satisfies the TSV header and guard, and re-run every test you rely on.',
  stagerace: 'At the pause the earlier seat had committed four commits (fa9bb7a staged-copy verification through delete-sharing reads, d1a0d8b keep a staged copy that cannot be read, 324018f guard why string, 01af836 troubleshooting D27 cause and the stopping-daemon limit) and was running a Linux gate of the touched packages at 01af836, killed by the pause (not evidence). Confirm the root cause is proven (product race vs scanner) before relying on the fix.',
}
const RESUME = (w) => `RESUMING — read this first. This workstream started 2026-09-27 evening and was STOPPED because the owner paused the session overnight (not a failure of the work). The earlier seat's work is preserved in your worktree and has NOT been reviewed: commits ${BASE}..HEAD plus any uncommitted or untracked files. A chronological digest of its tool calls and narration is at ${DIGEST}/w8-impl-${w.ws}.txt; read at least its last third. ${NOTES[w.ws]}
Before anything else: \`git log --oneline ${BASE}..HEAD\`, \`git status\`, \`git diff\`. Treat the earlier commits as a colleague's unreviewed draft: keep, revise or revert each deliberately (prefer new commits). Any run cut short by the pause is not evidence: re-run everything you rely on at your final HEAD. The container may hold zombie processes (PID 1 does not reap); never restart it. Then finish the whole task below.`

const WS = []

WS.push({ ws: 'sp08d3fix', cid: 'C2.1/SP08-D3/D35', effort: 'high', reviewEffort: 'high', task: `TASK — resolve carried defect SP08-D3 per coordinator decision D35. Read plans/sdd/V6-closeout/w7-sp08d3/report.md, the SP08-D3 section of plans/V2-SP-08-carried-defects.md (incl. the 2026-09-27 close-out note), plans/CARRIED-DEFECTS.tsv's header and test/guards/carrieddefects_test.go first.
The defect: a session's first prompt is not prompt_<s>_0 when prompts arrive by client-spool replay. HotSpool and runtime.daemon.enabled=false spool every prompt, one file per hook process, and the drain replays client spools in client-<pid> file-name order, which is not host order; and a live prompt can overtake an earlier spooled one within the watcher's window.
D35 (decided, implement it; do not over-engineer):
(1) Replay client spools in host order: order the client spool files by their first record's req.TS (file name only as a tie-break), and within a file keep record order. This covers HotSpool and daemon-disabled mode (acceptance item 3 is ruled: capture-by-drain is accepted, in host order).
(2) The residual live-vs-spool race is ruled out of the host-order guarantee, and the product must say so honestly instead of claiming provenance: when a prompt of a session is captured with a req.TS earlier than the session's already-published prompt_<s>_0 (or any earlier turn), record it the way the design records other substitutions (a Warn and a drop/notice entry naming the substituted turn, so the rehydrator never presents a later prompt as the session's original request without saying so), and correct the §8.5 provenance claim in the docs (docs/architecture.md, docs/cannot-do.md) to the shipped guarantee. Do not re-number published turns.
Failing tests first: invert TestCarriedDefect_SP08D3_SpooledHostFirstPromptLosesTurnZero (its spool_file_order subtest must now show host order; its live_second subtest must show the honest notice), stamping the two prompts with DISTINCT req.TS values (the w7 review found a tie-prone stamp). Then move row SP08-D3 to fixed exactly as the TSV header and the guard require (evidence, detail-section close-out note citing D35), with TestCarriedDefects_ManifestIsWellFormed and TestCarriedDefects_OpenRowsHaveLivingEvidence green for it. Also fix the stale comments in test/e2e/v5_x01_test.go (~lines 71-72: a WAL replay of observe.prompt no longer runs only the sentinel scan).
Run internal/daemon, internal/observer, the rehydrator's package, the test/e2e rows that touch prompt capture/replay, and test/guards on Windows, and the touched packages on Linux via the gate script.
Scope: internal/daemon drain/replay ordering and prompt capture, the rehydrator notice path, tests, CARRIED-DEFECTS.tsv and its detail doc, docs.` })

WS.push({ ws: 'stagerace', cid: 'C1.17', effort: 'high', reviewEffort: 'high', task: `TASK — a possible Windows concurrent-staging hazard, and three review nits from w7-spawnclaim. Read plans/sdd/V6-closeout/w2-lifetime/report.md (D10 staged copies), w5-coldstart/report.md and w7-spawnclaim/report.md first.
(1) internal/daemon TestStageBinary_ConcurrentSpawnersAgree failed once in a loaded Windows run of the whole package (spawn_stage_test.go:179, spawner 4): "rename .stage-94550389 -> qompack.exe: Access is denied (and the existing file: open ... qompack.exe: The process cannot access the file because it is being used by another process)"; it passes alone (-count=20). Users hit this path when several Claude Code sessions start at once on Windows (each session-start stages the verified copy under ~/.qompack/bin/<sha256>/ before spawning). Root-cause it with evidence: is it a real product race (for example one spawner renaming over a staged exe another spawner has just started or is verifying), or an antivirus scanner (Windows Defender, see D32) holding the freshly written exe? Reproduce under your own bounded load (at most 8 busy processes, at most 20 min, stopped after) with -count on the row. If a spawner can fail to start a daemon when the staged copy already exists and is correct, fix it at the source (for example an existing verified copy with the right content is success, not an error; a sharing violation on the final rename is retried within a named, derived bound or resolved by verifying the existing file), failing test first. If it is purely the scanner, make the product tolerate it the same way (users run Defender), and make the test's claim precise. No weakened assertion.
(2) Nits from the w7-spawnclaim review: test/guards/sharedreaders_test.go's why strings for run/spawn.lock still name only two deleters — add Lock.Release's removeSpawnClaim (the lock holder's process: winning daemon, daemon command writer lease, fsck, delivery-seal). docs/troubleshooting.md's entry on a second "qompack daemon" process appearing and exiting at once lists two causes — add the D27 third (a lock holder releasing while another spawn is still starting deletes that spawn's claim, so one extra daemon may start and is turned away by daemon.lock). And document as a known limitation (docs/troubleshooting.md or docs/cannot-do.md, whichever the docs use for such limits) that a SessionEnd flush arriving while the running daemon is stopping (listener closed, lock still held) spawns a daemon that loses, so that session's end work waits in the spool until the next session (replayed, not lost).
Run internal/daemon, internal/ipc, internal/cli and test/guards on Windows in full, and the touched packages on Linux via the gate script.
Scope: internal/daemon staging/spawn, tests, test/guards/sharedreaders_test.go why strings, docs/troubleshooting.md or docs/cannot-do.md.` })

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
  (w) => agent(`${COMMON(w)}\n\n${RESUME(w)}\n\n${w.task}`, { label: `impl:${w.ws}`, phase: 'Implement', schema: RESULT, effort: w.effort }),
  async (impl, w) => {
    if (!impl) return { w, impl: null, review: null }
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-8 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w8-${w.ws}, branch closeout/w8-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w8-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
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
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-8 workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w8-${r.ws}, branch closeout/w8-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w8-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: r.w.reviewEffort })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
