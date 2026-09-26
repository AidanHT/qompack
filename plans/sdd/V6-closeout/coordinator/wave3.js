export const meta = {
  name: 'v6-closeout-wave3',
  description: 'Wave 3 close-out: SessionStart route follow-ups (D11, replayed-probe defect), pre-existing e2e reds, paths/x-sys hygiene, eval task set v2 (D12); each reviewed and fixed',
  phases: [
    { title: 'Implement', detail: 'four workstreams, one worktree each off closeout/integration 54a4334' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = '54a4334'

const COMMON = (w) => `You are one of several parallel workstreams closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md. Wave-1 workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w3-${w.ws} on branch closeout/w3-${w.ws}, cut from closeout/integration @ ${BASE} (all wave-1 and wave-2b branches merged; their reports are under plans/sdd/V6-closeout/w2-*/report.md). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w3-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase other branches, change git config/hooks, or SendMessage anyone.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w3-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages; \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change.
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (three other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory, never on the shared session scratchpad root.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w3-${w.ws}); never reuse or delete other /work dirs.
- Real Claude Code sessions are NOT allowed in this wave unless your task says so.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const WS = []

WS.push({ ws: 'startroute', cid: 'C1.16/D11', task: `TASK — SessionStart route follow-ups. Read plans/sdd/V6-closeout/w2-lifetime/report.md and w2-hookout/report.md first.
(1) Owner decision D11 (2026-09-25): an unreadable checkpoint store or a failed rehydration build must ALSO answer a compact SessionStart with the explicit "rehydration deferred" note (the note w2-lifetime added for lateness/stopping/no-answer; internal/daemon/session_start_compact.go and the hook client), never silence. TestService_CheckpointErrorEmitsNothing pins today's silence: change it with a criterion-change rationale citing D11, add rows for each failure class (unreadable store, corrupt/fatal checkpoint, build error, panic), and keep the note far under the host cap (HostChars). Degraded-passive mode still injects nothing. Decide and document how dropped() reports these (never "delivered").
(2) Routed product defect (w2-hookout report, "A session.start that was spooled and replayed later mints a §12.1 probe that was never delivered"): drainDispatch routes a stale spooled session.start through dispatchOp -> handleSessionStart, which mints a sentinel token nobody receives, and two prompts later the project is degraded to passive recording ("hook.additional_context_delivered … sentinel not found after two chances"). Deterministic repro: plans/sdd/V6-closeout/w2-hookout/runs/diag-spooled-sessionstart-probe-windows.log with source runs/zz_diag_hookout_test.go.txt. Fix at the source (a replayed session.start must do its durable bookkeeping but never mint a probe or produce host-facing output, and must never count as a missed delivery), regression test first (turn the diagnostic into a real test), and check every other op the drain replays for the same class of "host-facing side effect on replay" bug.
(3) Evaluate the SessionStart cold-start wait (w2-hookout: session-start stops waiting for a daemon it just spawned after about 1.75 s — EnsureRunning's 1.5 s poll plus the client's 250 ms connect floor — against the 10 s reply budget). Measure what a first SessionStart loses when the daemon is not up in time (startup vs resume vs compact sources) and whether the Windows staged-copy first spawn (w2-lifetime: up to ~3 s under co-load) now makes that routine. Change it only if a real loss exists; any new bound goes to the owner (return it under needs_owner).
Scope: internal/daemon (session_start_compact.go, handlers.go SessionStart route, drain.go drainDispatch/dispatchOp only for the replay guard), internal/cli hook client, internal/rehydrate only if needed, tests, docs. Coordinate: the end2 workstream edits flushRoute/session_end.go/drain.go's flush path; keep your drain.go edit to the session.start replay guard.` })

WS.push({ ws: 'e2ereds', cid: 'C3.2', task: `TASK — three test/e2e reds that fail identically on the wave-2 base b070bbe (reported by w2-hookout, w2-lifetime and w2-sessionend; see their reports and runs/, e.g. plans/sdd/V6-closeout/w2-hookout/runs/e2e-v5x02-count4-base-b070bbe-windows.log):
- TestV4_TombstoneToRecallToExpandRoundTrip — a write-time append-only violation "already reserved for a different intent";
- TestV5_TombstoneToExpandRoundTrip — "an unpinned re_read is the newest capture" fails 4/4;
- TestV5_EliminationThroughEveryFourSurfaces/active_through_both_write_surfaces (and any sibling subtest that fails; one earlier report said it "expects a PreCompact hookSpecificOutput", which C1.18 retired — check whether that part is already fixed on this base).
For each: reproduce alone on Windows at this base (and on Linux via the script), then bisect (git bisect or first-parent merges since cf31e01) to the change that broke it, and root-cause it. An append-only violation or a wrong "newest capture" is presumed a PRODUCT defect until proven otherwise; fix product defects at the source with a failing regression test first. If the test's premise is stale because of a deliberate, documented product change (cite the commit and the doc/decision), correct the test with a criterion-change rationale and keep every property it proves that is still true. Finish with the whole test/e2e package alone once on Windows and report every result.
Scope: whatever owns each root cause (likely internal/store, internal/mcp, internal/observer, internal/negknow), test/e2e. Coordinate: startroute and end2 edit internal/daemon; paths edits internal/paths; keep edits there minimal and say so.` })

WS.push({ ws: 'paths', cid: 'C3.5/C3.7', task: `TASK — three hygiene defects that affect the final gates.
(1) internal/paths: paths.WriteAtomic finds its temp directory by walking up to the nearest ancestor that contains a .qompack, so on a machine whose home holds ~/.qompack (the user-global config layer, calibration, fallback log, and — since C1.17 — the Windows staged daemon copies under ~/.qompack/bin) writes into a bare temp dir go to <home>\\.qompack\\tmp and fail ("rename C:\\Users\\...\\.qompack\\tmp\\wa-...: The system cannot find the path specified"); 2 internal/cli and 6 internal/ipc rows failed that way (plans/sdd/V6-closeout/w2-lifetime/runs/21 and its report). This machine WILL have ~/.qompack after the live UAT runs, so the final whole-tree gate would go red for a reason unrelated to the product. Decide the correct contract (the temp dir must be on the same volume and inside the store that owns the target; a walk must never escape a project root into an unrelated user-global .qompack, and the user-global layer must use its own root explicitly) and fix it at the source with regression tests that create a fake home .qompack above a temp project. Check every caller of WriteAtomic (and any other ancestor walk for .qompack, e.g. project-root resolution) for the same escape, and confirm real product paths (project store, user-global config) still write where they must. Never create or touch the real ~/.qompack in tests.
(2) govulncheck lists GO-2026-5024 in golang.org/x/sys/windows v0.33.0 (fixed in v0.44.0); the Windows binary imports the package but does not call the affected symbol. Upgrade golang.org/x/sys to the fixed version (and only what it requires), then run \`go run ./tools/devtool licenses --check\`, the pinned govulncheck (\`go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...\`), build-all, bindeps lint, and the internal/paths, internal/store and internal/daemon packages on Windows and Linux (script). Update THIRD_PARTY_NOTICES.md if the tooling requires it.
(3) internal/store TestGC_DeadlineOvershootIsBoundedByTheCheckInterval fails ALONE in the Linux container (non-root, -race) with its own "host could not be measured" message, on both b070bbe and w2-lint's branch (plans/sdd/V6-closeout/w2-lint/report.md). Find out whether the test's host-measurement precondition is wrong for a -race GOMAXPROCS=4 container (a test defect, fix without weakening what it proves) or whether GC really overshoots (a product defect). Evidence both ways.
Scope: internal/paths and its callers, go.mod/go.sum, THIRD_PARTY_NOTICES.md, internal/store gc tests. Coordinate: others edit internal/daemon, internal/eval, test/e2e.` })

WS.push({ ws: 'eval3', cid: 'C5.5/D12', task: `TASK — make the live evaluation ready for its confirmatory run. Read plans/sdd/V6-closeout/eval/preregistration.md (with amendments A2–A6), plans/sdd/V6-closeout/w2-eval2/report.md and the checklist's owner decisions D12 and defaults.
(1) Owner decision D12 (2026-09-25): tool-output-recall and seed-recall have \`go vet ./...\` as their only task check, which the untouched fixture already passes. Fix BEFORE any confirmatory trial: add real, program-graded task checks that an untouched fixture fails and the reference solution passes; also fix regression-guard's constraint check 'pinned', which compiles the hidden JoinList test so a trial that never implemented JoinList fails a constraint rather than the task (w2-eval2 open issue). This changes the frozen set: publish it under a NEW task-set id (e.g. qompack-live-v2) with new SHA-256s, append a dated amendment to the pre-registration BEFORE any trial (no v2 trial exists), keep v1 recorded as superseded-before-use, and keep TestLiveTaskSet_ReferenceSolutionsPassEveryCheck and the untouched-fixture-fails rule green for every task. The three held-out tasks must still not be run by anyone.
(2) Coordinator default (owner may overrule): \`qompack eval\` reports the pre-registered intention-to-treat decision (failed trials already counted as failures) and lists the failed trials by name, rather than forcing "inconclusive" whenever any trial failed. Implement with tests and docs via the generators.
(3) Verify the w2-eval2 fix seat's work end to end with a DRY RUN only (no model calls): build the windows/amd64 bundle from this tree, run \`go run ./tools/devtool live-eval --tasks testdata/eval/live/<v2 file> --include-held-out --arms stock,qompack --install plugin-dir --bundle <bundle> --known-open-defects none --max-sessions 40 --dry-run\` (or the equivalent flags the tool now has), and confirm the plan's task-set hash, trial order, arms, model pinning and confirmatory preconditions resolve. Report the exact command the coordinator should run for the confirmatory trial.
Scope: testdata/eval/live, internal/eval, internal/commands eval, tools/devtool liveeval*, the pre-registration and docs. No real Claude sessions in this workstream.` })


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
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-3 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w3-${w.ws}, branch closeout/w3-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w3-${w.ws} log ${BASE}..HEAD\` and the full diff. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
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
