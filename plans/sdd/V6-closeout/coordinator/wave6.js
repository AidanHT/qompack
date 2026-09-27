export const meta = {
  name: 'v6-closeout-wave6',
  description: 'Wave 6 close-out: session-start borrow (D21), config imports paths (D22), checkpoint barriers + NTFS premise docs (D24), GC pass serialization, Linux load-sensitive rows; each reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'five workstreams off closeout/integration f6095e2' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = 'f6095e2'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w6'

const COMMON = (w) => `You are one of five parallel workstreams closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D24 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w6-${w.ws} on branch closeout/w6-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w6-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w6-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (four other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w6-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const WS = []

WS.push({ ws: 'borrow', cid: 'C1.16/D21', task: `TASK — owner decision D21 and the cold-start follow-ups. Read plans/sdd/V6-closeout/w5-coldstart/report.md (its budget table, runs/35, runs/52 and the review resolution) and plans/sdd/V6-closeout/w5-home/report.md first.
(1) D21: the session-start budget is approved WITH BORROWING. Keep hookExitReserve 1.5 s, the 10 s reply wait, spawnClaimDialTimeout 250 ms, spawnLockStaleAfter 10 s as the spawn-claim freshness bound, and the at-least-1.5 s wait for a daemon spawned late. Change the find/start step so it may borrow idle reply time: it may run until doneBy - dial - compactAnswerBudget (8.25 s from doHook's first statement), so the reply keeps at least D9's 5 s compact bound plus the dial. The reply wait is then min(10 s, doneBy - now - dial). Update TestSessionStartBudget_SplitsTheManifestTimeout and add rows for: a daemon that comes up at 4 s, 6 s and 8 s (answered, not spooled); one that never comes up (spooled at the borrow limit, the whole hook inside 15 s); and a compact start that comes up at 8 s (its reply wait still admits the 5 s compact bound). Re-measure with the cold-start diagnostic under CPU co-load (copied temporarily, never committed): spool rate per source and whole-hook wall time. Show before (f6095e2) and after under the SAME load in interleaved rounds, so the comparison is fair.
(2) Defence in depth from w5-home: ipc.Client.lazySpawn and daemon.EnsureRunning/SpawnDetached do not re-check the D18 home root themselves. Make the spawners refuse a home root too, so a future caller that skips the entry check still creates nothing under ~/.qompack; test with a fake home.
(3) Stale comments in test/fault, test/platform and test/security say session-start's EnsureRunning "has already waited SpawnPollBound"; correct them to the D21 behaviour, and fix the w5-coldstart review nits that remain open.
Scope: internal/cli (hookbudget, session-start client), internal/daemon spawn.go, internal/ipc lazy spawn, those test comments, docs (architecture, troubleshooting). Coordinate: w6-config edits internal/config and test/guards read lists.` })

WS.push({ ws: 'config', cid: 'C1.8/D22', task: `TASK — owner decision D22 and test-home isolation. Read plans/sdd/V6-closeout/w5-winfiles/report.md (its needs_owner on §3.2 and its open items) and plans/sdd/V6-closeout/config/report.md first.
(1) D22: 00-ARCHITECTURE.md §3.2 is amended so internal/config may import internal/paths (paths imports only core, so there is no cycle). Record the amendment in 00-ARCHITECTURE.md (cite D22), update the importgraph lint's allow-list, and docs/architecture.md where it states the rule.
(2) Then make every config read Windows-safe: the loaders name the user-global root through paths.Global, and config.json (project, local and user-global layers, and LoadForCapture) is read with paths.ReadFileShared, so an editor's atomic save that overlaps a hook's read can no longer make that hook fail closed under D8. Regression test first on Windows: a writer replacing config.json atomically in a loop while LoadForCapture reads it in a loop — count failures before and after. Move the reads into test/guards' sharedReaders (TestGuard_EveryProductReadIsClassified, ratified by D23, must stay green and complete).
(3) Tests must never read the real home. w5-winfiles found internal/cli tests (seedFsckProject and others) open stores without HOME isolation, so the calibration loader reads a real ~/.qompack/calibration.json if one exists; after the live UAT this machine WILL have one. Find every test package that can reach the user-global layer (config, calibration, staged copies, fallback log) and isolate HOME/USERPROFILE (e.g. TestMain pointing both at a temp dir), and add a guard row that fails if a test process reads or writes under the real home's .qompack or .claude (a sentinel approach is fine). Prove it with a fake "real home" containing a poisoned calibration.json and config.json.
Scope: plans/00-ARCHITECTURE.md §3.2, tools/devtool importgraph rules, internal/config, internal/tokens calibration read if needed, test TestMain files, test/guards, docs. Coordinate: w6-borrow edits internal/cli session-start/spawn code; keep TestMain additions in cli minimal.` })

WS.push({ ws: 'ckptsync', cid: 'C1.6/D24', task: `TASK — missing durability barriers and the D24 documentation. Read plans/sdd/V6-closeout/w5-dirsync/report.md (its "Checkpoint owner" observation and property table) and plans/sdd/V6-closeout/w4-syncs/report.md first.
(1) checkpoint Finalize: paths.CreateNew fsyncs the artifact but not the checkpoints/ directory, and AppendManifest does not fsync the MANIFEST. On POSIX a power loss can lose a sealed checkpoint's name or its MANIFEST line after the daemon reported it sealed. Establish the required ordering (artifact durable -> directory entry durable -> MANIFEST line durable -> anything that depends on the checkpoint being sealed, e.g. the rehydrate state and the PreCompact answer), add the missing barriers, and pin them with the barrier-counting seam plus crash-injection rows at each step (the checkpoint must be either absent or fully sealed after any cut).
(2) Audit every other create/append/rename site in the product for the same missing-barrier class: pins, negative knowledge (tried.bloom, eliminations), tombstones, the delivery journal and its segments, index appends, spool files, gc.json, rehydrate state, calibration. For each, say whether a directory fsync or file fsync is required by what depends on it, and fix every real gap with a test. Do not add barriers where nothing depends on durability (say why for each).
(3) D24: Windows directory sync stays a no-op on the NTFS-journaling premise. Document the premise and its residual risk precisely in docs/architecture.md's durability section and docs/security.md (what NTFS journals, what could still be lost on a power cut, and that no product guarantee depends on more), and make paths.SyncDir's doc comment cite D24.
Measure the cost of added barriers (counting seam; paired timing on Linux via the script only as ratios). Scope: internal/checkpoint, internal/store and the other writers found in (2), internal/paths docs, docs. Coordinate: w6-gcserial edits the store's GC pass entry and gc.json handling — agree by keeping your gc.json change (if any) to the barrier itself.` })

WS.push({ ws: 'gcserial', cid: 'C1.10/C3.2', task: `TASK — GC passes are not serialized. Read plans/sdd/V6-closeout/w5-winfiles/report.md (open item on GC), plans/sdd/V6-closeout/rollover/report.md and plans/sdd/V6-closeout/w2-sessionend/report.md first.
Each concurrent session end runs a GC pass, and so does the idle scheduler; two overlapping passes can race on gc.json's resume cursor and on retention-roots compaction (CompactRetentionRoots), and possibly on the delivery-segment retention the rollover work added. Establish the actual behaviour with a failing test first (two or more GC passes started together on one store, and a pass racing a session end's own pass), then fix at the source: at most one GC pass per store at a time. Decide and document what a second requester does (join the running pass, or queue exactly one follow-up pass, never lose a request that could free space), and whether a CLI-invoked GC or fsck --repair in another process needs the same exclusion (check what GC entry points exist outside the daemon; if one exists, use a store-level lock that cannot deadlock with the daemon's writer lease). Keep GC's deadline and resume semantics (TestGC_Deadline* rows) unchanged, keep the Windows shared-read fixes, and run internal/store, internal/daemon, test/fault and test/integration in full on Windows and Linux.
Scope: internal/store GC entry/gcrun, internal/daemon session-end and scheduler GC triggers, tests, docs. Coordinate: w6-ckptsync may add a barrier to gc.json writes; keep your change to the serialization.` })

WS.push({ ws: 'linuxrows', cid: 'C3.4', task: `TASK — the Linux rows that fail under load, so the final Linux gates are trustworthy. Read plans/sdd/V6-closeout/w5-helpers/report.md, w5-dirsync/report.md, w5-coldstart/report.md and w4-e2eflakes/report.md first, and the Linux runs they cite.
For each row: reproduce in the container at base f6095e2 ALONE on an idle container and under controlled load (your own load generator, e.g. --count with a parallel CPU/fsync burner in your own /work dir, or -p with other packages), root-cause it, and decide product vs test with evidence. A product defect is fixed at the source with a failing test first; a test defect is fixed without weakening what the row proves. A wider timeout is acceptable only with a derivation showing the old bound was wrong.
(a) test/e2e TestV3_LiveSessionWriteSetAndAppendOnly: "flush never produced the observer: gc log line" within e2eHistoryConvergeBound; passes on an idle container (runs/run3 on integration, 3/3 at 90e1db3), fails 2-3/3 when the container is busy (w5-dirsync, w5-helpers). Is the async SessionEnd (D13) ever dropping its GC under load, or is the bound too tight for the work the row waits for?
(b) test/e2e TestE2E_SpooledSessionStartNeverDegradesTheProject: failed 3/3 on Linux at 90e1db3 ("the replayed start still did the start's bookkeeping", expected 1 got 0). w5-coldstart's aba55d8 changed e2eWaitDaemonUp; check whether that fixed it at f6095e2 and, if not, root-cause it.
(c) test/e2e TestE2E_ThinSliceDropsControlOnlyEdges: under load "real mixed traffic must produce at least one EdgeControlOnly" (observer_e2e_test.go ~563); a different assertion from the one w4 fixed in a893e5d.
(d) internal/daemon TestDeliveryOrder_LaneOverflowIsDrainedOnRequest: failed under co-load in four workstreams (48/50, 1-in-N), passes alone.
(e) internal/daemon TestFeaturesFrom_LexicalCohesionShingleCap: 14.5 ms vs 10 ms under -race co-load (scheduler, timing): confirm it is a pure timing row, what it protects, and whether its bound is a documented budget; do not change a budget, report it.
Finish with the whole test/e2e package and internal/daemon on Linux via the script, once idle and once with --coload, and report every row.
Scope: test/e2e helpers/rows, internal/daemon tests, and product code only where a row proves a product defect (coordinate: w6-gcserial edits session-end GC triggers; w6-borrow edits spawn/session-start).` })

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
  (w) => agent(`${COMMON(w)}\n\n${w.task}`, { label: `impl:${w.ws}`, phase: 'Implement', schema: RESULT }),
  async (impl, w) => {
    if (!impl) return { w, impl: null, review: null }
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-6 workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w6-${w.ws}, branch closeout/w6-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w6-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
      { label: `review:${w.ws}`, phase: 'Review', schema: FINDINGS, effort: 'high' })
    return { w, impl, review }
  },
  async (r) => {
    if (!r.impl) return { w: r.w, ws: r.w.ws, impl: null, review: null, final: null }
    const actionable = (r.review ? r.review.findings : []).filter((f) => f.severity !== 'nit')
    if (actionable.length === 0) return { w: r.w, ws: r.w.ws, impl: r.impl, review: r.review, final: r.impl, fixRan: false, actionable }
    const final = await agent(`${COMMON(r.w)}\n\nYou are the FIX seat for this workstream (the implementer has finished). Its task was:\n---\n${r.w.task}\n---\nIndependent reviewer findings:\n${JSON.stringify(actionable, null, 1)}\nVerify each independently; fix the correct ones (failing test first where applicable) and re-run focused tests; rebut wrong ones with evidence. Blockers/majors must be resolved or rebutted. Return the final state with a "Review resolution" section (finding -> action/rebuttal) in your summary, and carry forward every needs_owner item still open (the implementer's were: ${JSON.stringify(r.impl.needs_owner || [])}).`,
      { label: `fix:${r.w.ws}`, phase: 'Fix', schema: RESULT })
    return { w: r.w, ws: r.w.ws, impl: r.impl, review: r.review, final, fixRan: true, actionable }
  },
  async (r) => {
    if (!r.fixRan || !r.final) return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: !!r.fixRan, verify: null }
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-6 workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w6-${r.ws}, branch closeout/w6-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w6-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: 'high' })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
