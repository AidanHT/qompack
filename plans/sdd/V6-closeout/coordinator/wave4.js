export const meta = {
  name: 'v6-closeout-wave4',
  description: 'Wave 4 close-out: redundant PostToolUse durability passes (SP08-D1) and the load-sensitive Windows e2e rows; each reviewed and fixed',
  phases: [
    { title: 'Implement', detail: 'two workstreams off closeout/integration 31255da' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = '31255da'

const COMMON = (w) => `You are one of several parallel workstreams closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md. Wave-1 workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w4-${w.ws} on branch closeout/w4-${w.ws}, cut from closeout/integration @ ${BASE} (all wave-1 and wave-2b branches and w3-e2ereds merged; their reports are under plans/sdd/V6-closeout/w2-*/report.md). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w4-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase other branches, change git config/hooks, or SendMessage anyone.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w4-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages; \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change.
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (the other wave-4 workstream and three wave-3 workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory, never on the shared session scratchpad root.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w4-${w.ws}); never reuse or delete other /work dirs.
- Real Claude Code sessions are NOT allowed in this wave unless your task says so.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const WS = []

WS.push({ ws: 'syncs', cid: 'C2.3/SP08-D1', task: `TASK — carried defect SP08-D1 (OnToolUse over a 256 KB tool result breaches budget B-C, l0_process p99 < 50 ms; dominated by the store's per-capture durability work). Read plans/CARRIED-DEFECTS.tsv (SP08-D1), plans/sdd/V6-closeout/perfobs/report.md, plans/sdd/V6-closeout/perfstore/report.md and plans/sdd/V6-closeout/w4-e2ereds/report.md first.
w4-e2ereds found that the leased PostToolUse path syncs ONE publication root about FOUR times per capture: onToolUse runs syncObservation before publishRecord; completeIntentLocked runs SyncPublication before and after the index write; and finishObservation (added by 22ff16c, "recover original publication before deriving replay") runs a full SyncPublication again on the same root before the capture link, which re-reads and verifies every chunk, then fsyncs every object, roots/tool_use and the index directory.
(1) Establish, from the code, the docs (00-ARCHITECTURE.md §0.2/§0.2.1, docs/architecture.md durability sections, Qompack.md §8) and the crash tests, exactly which of those passes each durability/crash property depends on (what must be durable before the ACK, before the tool_use line becomes visible, before the capture link, before the file-version record). Write that down in your report as a table.
(2) Remove only passes that are provably redundant on the FRESH-publish path (for example finishObservation's SyncPublication when publishObservation already synced the same root in the same call), keeping them on the replay/recovery path where the original publication must be re-proven. No durability or crash-consistency property may weaken: every existing crash/fault/publication test (internal/store, internal/observer, internal/daemon, test/fault, test/integration append-only and crash-recovery rows) must stay green unchanged, and add a test that pins the new pass count per fresh capture (a counter seam, not timing) plus a crash-injection test between each remaining step if one is missing.
(3) Measure the effect with load-independent evidence (fsync/syscall counts via a counting seam or strace on Linux, allocs/op, B/op) and paired interleaved A/B timing of BenchmarkOnToolUse_TestOutput256KB and its AllNovel/one-changed-line variants (-count >= 10 per side, ABBA, both Windows and Linux via the script). Absolute timings on this loaded host are NOT budget evidence (the quiet C5.2 run is the coordinator's); report them only as paired ratios with a sign test. Do not change any budget constant or CARRIED-DEFECTS status.
Scope: internal/observer (tooluse.go, finishObservation), internal/store (publication/intent completion), their tests and benchmarks, docs/architecture.md durability text if it names the passes. Coordinate: the e2eflakes workstream edits test/e2e and possibly the daemon's rehydrate-state writer.` })

WS.push({ ws: 'e2eflakes', cid: 'C3.2', task: `TASK — the Windows test/e2e rows that fail only under load, so that the final whole-tree Windows gate is trustworthy. Read plans/sdd/V6-closeout/w4-e2ereds/report.md (its full Windows run, runs/, and the four co-load reds) first. For each row: reproduce under controlled load (e.g. run the row with -count=N while a CPU/fsync load generator of your own runs, or run the whole package), root-cause it, and decide product vs test with evidence. Never weaken what a row proves; a wall-clock-only fix (a bigger timeout) is not acceptable unless you prove the bound was wrong and write a criterion-change rationale.
(a) TestE2EShutdownIfReachable_WaitsForASpawnStillInFlight (aec178a, w2-hookout): the helper returned 3.2 ms before the late daemon's cmd.Wait returned; the helper counts the daemon gone when its lock is released, the test asserts process exit. Make the helper and the assertion agree on one definition of "gone" that is correct for every caller of the helper (a helper that returns while a daemon still has files open is what breaks TempDir cleanup on Windows).
(b) TestV5_PreCompactToRehydrateToDroppedRoundTrip/full_budget_round_trip: a Windows sharing violation reading state/rehydrate-sess-e2e-v5-x04.json (v5_x04_test.go:305) under co-load. Find whether the daemon's writer (atomic rename while a reader has the file open) or the test's reader (not using paths.ReadFileShared / not retrying a documented transient) is at fault. If a PRODUCT reader of that state file (status, dropped(), MCP) can hit the same sharing violation on Windows, that is a product defect: fix it at the source with a test.
(c) TestV4_HotPathUnchangedWithTheFullWave3ResidentSet: under load the baseline arm touched spool/<client> (a hook spooled because it missed its ACK) and the wave-3 arm did not, so the touch-set comparison differed. Decide what the row is meant to prove and make it compare the right thing (e.g. exclude hook-spool files that exist only because of an ACK miss, or assert on them separately) without losing its power to catch a real extra write.
(d) TestV3_HotPathUnchangedWithLedgerResident fails even alone on a loaded host (B-A p50 30.7 ms against 15 ms). That is a timing row: do not change its budget; confirm it is purely a timing gate and that its non-timing assertions pass under load, and say so. The quiet C5.1 run judges its numbers.
Finish with the whole test/e2e package alone on Windows (quiet as you can make it) and on Linux via the script, and report every row.
Scope: test/e2e helpers and rows, and the daemon/store writer or reader of state/rehydrate-*.json only if (b) proves a product defect. Coordinate: the syncs workstream edits internal/observer and internal/store publication code.` })


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

const LENS = 'Lenses: (a) root cause proven and fixed at the source; correctness incl. concurrency (races, deadlocks, lock order, goroutine leaks), durability/crash semantics (fsync ordering, WAL retention, idempotency, identity never re-minted), fail-closed paths, Windows vs POSIX, bounded memory/CPU; (b) check-weakening (deleted/loosened assertions, skips, nolint/nomagic, lowered thresholds, regenerated goldens, widened timeouts, tests bent to buggy output) and legitimacy of every criterion change; (c) docs consistent with behaviour, generated docs via generators; (d) commit hygiene (conventional subjects, Refs footer, no attribution trailers); (e) was the whole task done, or parts silently dropped?'

const results = await pipeline(
  WS,
  (w) => agent(`${COMMON(w)}\n\n${w.task}`, { label: `impl:${w.ws}`, phase: 'Implement', schema: RESULT }),
  async (impl, w) => {
    if (!impl) return { w, impl: null, review: null }
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-4 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w4-${w.ws}, branch closeout/w4-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w4-${w.ws} log ${BASE}..HEAD\` and the full diff. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
      { label: `review:${w.ws}`, phase: 'Review', schema: FINDINGS, effort: 'high' })
    return { w, impl, review }
  },
  async (r) => {
    if (!r.impl) return { ws: r.w.ws, impl: null, review: null, final: null }
    const actionable = (r.review ? r.review.findings : []).filter((f) => f.severity !== 'nit')
    if (actionable.length === 0) return { ws: r.w.ws, impl: r.impl, review: r.review, final: r.impl, fixRan: false }
    const final = await agent(`${COMMON(r.w)}\n\nYou are the FIX seat for this workstream (the implementer has finished). Its task was:\n---\n${r.w.task}\n---\nIndependent reviewer findings:\n${JSON.stringify(actionable, null, 1)}\nVerify each independently; fix the correct ones (failing test first where applicable) and re-run focused tests; rebut wrong ones with evidence. Blockers/majors must be resolved or rebutted. Return the final state with a "Review resolution" section (finding -> action/rebuttal) in your summary.`,
      { label: `fix:${r.w.ws}`, phase: 'Fix', schema: RESULT })
    return { ws: r.w.ws, impl: r.impl, review: r.review, final, fixRan: true }
  },
)
return results
