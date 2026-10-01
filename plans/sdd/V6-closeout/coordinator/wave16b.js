export const meta = {
  name: 'v6-closeout-wave16b',
  description: 'Wave 16b close-out: settle bound and estimator, hostperm 8.3 trigger and self-markers, CI records and timeouts, Linux coverage floors (D55); reviewed, fixed and verified',
  phases: [
    { title: 'Implement', detail: 'four workstreams off closeout/integration 6f118a7b' },
    { title: 'Review', detail: 'independent adversarial review' },
    { title: 'Fix', detail: 'resolve confirmed findings' },
    { title: 'Verify', detail: 'check the fix seat resolved every non-nit finding' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'
const BASE = '6f118a7b'
const SCRATCH = 'C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w16b'

const COMMON = (w) => `You are one of the parallel workstreams (wave 16b: settle, polish, cirec, cover) closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger (read it; do NOT edit it): ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — owner decisions D1-D49 and the coordinator defaults are there. Earlier workstream reports are committed on your base under plans/sdd/V6-closeout/<ws>/report.md (wave 1: ingest, e2e, config, hostperm, rollover, perfstore, perfobs, eval, linux, packaging, rehydrate-cap; wave 2b: w2-*; wave 3/4: w3-startroute, w3-paths, w3-e2ereds, w3-eval3, w4-syncs, w4-e2eflakes; wave 5: w5-coldstart, w5-home, w5-winfiles, w5-helpers, w5-dirsync, w5-deps; wave 6: w6-borrow, w6-config, w6-ckptsync, w6-gcserial, w6-linuxrows; wave 7: w7-spawnclaim, w7-layout, w7-sp08d3, w7-docs, w7b-selftest, w7b-checkpoint; wave 8: w8-sp08d3fix, w8-stagerace, w8b-polish) — read the ones relevant to your task first.

YOUR WORKTREE: ${ROOT}/qompack-cx-w16b-${w.ws} on branch closeout/w16b-${w.ws}, cut from closeout/integration @ ${BASE} (every earlier wave merged). The Bash tool resets cwd between calls: prefix every command with \`cd ${ROOT}/qompack-cx-w16b-${w.ws} &&\` or use absolute paths. Never edit other worktrees. Never push, tag, merge, rebase or reset other branches, change git config/hooks, or SendMessage anyone. Your scratch files go under ${SCRATCH}/${w.ws}/ (create it); never delete anything else in that scratchpad.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold/budget, no golden regenerated to match broken output, no deleted or loosened assertion, no widened timeout that hides a defect. Criterion changes only with a written rationale in your returned report.
- Root cause first; failing regression test first; then fix.
- New budget/bound numbers are the owner's: implement them behind a named constant with a derivation comment, and list each one (value, derivation, what breaks if it is wrong) under needs_owner; the coordinator gets approval before merging.
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix, NO attribution trailers (no Co-Authored-By, Signed-off-by, Claude-Session, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits. Evidence logs may be committed under plans/sdd/V6-closeout/w16b-${w.ws}/runs/.
- \`go run ./tools/devtool fmt\`/\`fmt-check\`, \`go vet\` on touched packages (Windows and GOOS=linux); \`go test ./test/docs\` and gen-*-docs --check when docs/generated inputs change. Use \`go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns\`: never the stubskips sub-check (it runs a whole-tree go test).
- Tests: FOCUSED runs plus each touched package once in full; never ./... or devtool test/test-race (four other workstreams run tests on this machine and in the Linux container at the same time, so the machine is loaded: re-run any wall-clock failure alone before believing it, and say so). Never let a pipe mask go test's exit code.
- Every \`go test -run\` pattern you quote in your report must match a real test name exactly (the runpatterns lint checks committed reports); name temporary diagnostics as such.
- Processes: never kill a process you did not start. If you must stop your own processes, match on YOUR worktree path or your own scratch subdirectory.
- Linux: container \`qompack-v6-linux-verification\`; the committed script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh runs an exact commit non-root with -race (see its header; use --prefix cx-w16b-${w.ws}; pass --repo and --out as Windows-style C:/ paths, a POSIX /c/ path is refused); never reuse or delete other /work dirs.
- CPU: the owner uses this laptop interactively. Any load generator you start must be bounded with \`timeout\` to at most 20 minutes per run and at most 8 busy processes (of 22 cores), and you must stop it when the measurement ends; never leave one running. Stop your own background diagnostics before you finish.
- Never create, read or modify the real ~/.qompack or ~/.claude in tests; use a fake HOME/USERPROFILE. Real Claude Code sessions are NOT allowed.
- The harness refuses report files from subagents: do NOT write report.md. Return the full report (root causes with evidence, what changed, exact commands + results, criterion changes, open items, owner decisions) in your returned summary; the coordinator commits it.`

const MACHINE = `MACHINE LIMITS FOR THIS SEAT (override the general rules): Phase 3 gates run on this laptop now and memory is tight. The Linux container is STOPPED on purpose: do NOT start it and do not run the Linux gate script; the coordinator runs Linux verification later. On Windows use \`-p 2\`, no load generators. The Phase 3 chain logs to C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/chain-2.log: while that file has a "win-timing start" line with no later "win-timing exit" line, an ISOLATED timing gate is running and you must not run anything load-bearing (no hot-path row, no test/integration package, no -race, no benchmark) until it ends; do code reading and cheap deterministic unit tests meanwhile. Run at most one load-bearing test at a time.`

const EV = `EVIDENCE: the Phase 4 live lane ran real Claude Code 2.1.280 sessions on the frozen candidate 3 (d5598eb4) with the packaged bundle. Its report is plans/sdd/V6-closeout/live/report.md in your worktree (evidence dirs beside it under plans/sdd/V6-closeout/live/uat/, c4/, recovery/). Read the rows named below and their evidence files before touching code. Coordinator decision D45 (ledger) says these are fixed before 0.3.0.
MACHINE LIMITS FOR THIS SEAT (override the general rules): DAYTIME, the owner is using this laptop and six seats run at once. Use \`-p 2\`, no load generators, no -race on whole packages, and do NOT run the hot-path rows (TestIntegration_HotPath*, TestV3_HotPath*) or whole test/integration / test/e2e packages; run only the focused rows you add or touch plus the touched packages in full once. The Linux container is STOPPED: do not start it. Real Claude Code sessions are NOT allowed: reproduce each live finding with a deterministic test (a real daemon/store/hook process in a temp project is fine) that is RED before the fix. The coordinator re-runs the live rows on the fixed candidate.
Other seats in this wave (do not fix their items; if your root cause turns out to be theirs, say so and stop): ledger (elimination ledger, MCP self-record turns, timeline), intent (rehydration intent/corrections/fork), restore (backup/restore/fsck segments/cross-version), pinsckpt (pins view, checkpoint pointers), diag (sentinel scan, status/doctor diagnostics), mcpresp (MCP response bounds/selectors/messages, UAT docs).`

const LIM = `MACHINE LIMITS FOR THIS SEAT (override the general rules): three seats run at once. Use \`-p 2\`, no load generators, no -race on whole packages, and do NOT run the hot-path rows (TestIntegration_HotPath*, TestV3_HotPath*) or whole test/integration / test/e2e packages; run the focused rows you add or touch and the touched packages in full once. The Linux container is STOPPED: do not start it. No real Claude Code sessions: reproduce with deterministic tests that are RED before the fix. Read coordinator decision D46 in the ledger. Other seats: observer, decisions, safecut.`

const LIM15_UNUSED = `EVIDENCE: the live re-run on candidate 4 (9f6a2fad, decision D47) ran real Claude Code sessions with the frozen bundle. Its evidence is committed on your base under plans/sdd/V6-closeout/live/rerun-c4/<row>/ and the Result blocks in docs/uat.md; the findings named below come from there. Coordinator decision D49 in the ledger says every one is fixed before 0.3.0 and fixes each disposition: read D45, D46 and D49 first.
MACHINE LIMITS FOR THIS SEAT (override the general rules): DAYTIME, the owner is using this laptop and four seats run at once. Use \`-p 2\`, no load generators, no -race on whole packages, and do NOT run the hot-path rows (TestIntegration_HotPath*, TestV3_HotPath*) or whole test/integration / test/e2e packages; run the focused rows you add or touch and the touched packages in full once. The Linux container is STOPPED: do not start it. No real Claude Code sessions: reproduce each finding with a deterministic test (a real daemon/store/hook process in a temp project is fine) that is RED before the fix.
Other seats: rehydrate (fallback visibility, evolution admission, eviction order), ledger (stale bloom, advance_frontier, decisions across checkpoints, fork inheritance), services (config reload, recall k, publication accounting, fsck index.files), docs. Do not fix their items; if your root cause is theirs, say so and stop.`

const LIM15A_UNUSED = `EVIDENCE: the candidate 4 live re-run's audit (plans/sdd/V6-closeout/live/report-c4.md, section "Independent audit") and its evidence under plans/sdd/V6-closeout/live/rerun-c4/ are on your base. Coordinator decision D50 in the ledger fixes the dispositions: read D45, D46, D49 and D50 first.
MACHINE LIMITS FOR THIS SEAT (override the general rules): DAYTIME, the owner is using this laptop and six seats run at once. Use \`-p 2\`, no load generators, no -race on whole packages, and do NOT run the hot-path rows (TestIntegration_HotPath*, TestV3_HotPath*) or whole test/integration / test/e2e packages; run the focused rows you add or touch and the touched packages in full once. The Linux container is STOPPED: do not start it. No real Claude Code sessions: reproduce with deterministic tests that are RED before the fix.
Other seats running now: wave 15 rehydrate (internal/rehydrate), ledger (internal/negknow, checkpoint decisions), services (daemon config reload, mcp recall in handlers.go, store publication accounting, fsck index.files), docs; and paging / snapshot in this wave. Do not fix their items or edit their files beyond what your fix needs; if your root cause is theirs, say so and stop.`

const LIM15B_UNUSED = `EVIDENCE: the candidate 4 live re-run's audit (plans/sdd/V6-closeout/live/report-c4.md, section "Independent audit") and its evidence under plans/sdd/V6-closeout/live/rerun-c4/ are on your base, with wave 15 (docs, ledger) and wave 15a (paging, snapshot) merged. Coordinator decision D50 in the ledger fixes the dispositions: read D46, D49 and D50 first.
MACHINE LIMITS FOR THIS SEAT (override the general rules): DAYTIME, the owner is using this laptop and other seats run at once. Use \`-p 2\`, no load generators, no hot-path rows, no whole test/integration or test/e2e packages. The Linux container is STOPPED: do not start it. No real Claude Code sessions.
Other seats running now: wave 15 rehydrate (internal/rehydrate) and services (daemon reload, mcp recall, store publication accounting, fsck). Do not edit their files.`

const LIM15C = `Coordinator decisions D49 and D51 in the ledger govern this task: read them. The wave 15 services report is on your base: plans/sdd/V6-closeout/w15-services/report.md (its needs_owner and open issues are the source of this task).
MACHINE LIMITS FOR THIS SEAT (override the general rules): the owner is using this laptop and another seat runs. Use \`-p 2\`, no load generators except the bounded measurement below, no hot-path rows, no whole test/integration or test/e2e. The Linux container is STOPPED: do not start it. No real Claude Code sessions.
Other seat running now: wave 15 rehydrate (internal/rehydrate). Do not edit its files.`

const LIM16 = `Coordinator decision D53 in the ledger governs this wave: read it, with D45, D46, D49 and D50. The independent audit it disposes is plans/sdd/V6-closeout/audit/goal-metrics-audit.md on verify/v6 (read it at ${ROOT}/qompack-v6/plans/sdd/V6-closeout/audit/goal-metrics-audit.md). Candidate 5's overnight evidence is under ${ROOT}/qompack-v6/plans/sdd/V6-closeout/phase3/c5/ (p3-win-e2e-timing.log, linux/cx-p3-p3-linux-e2e-timing-*/, quiet/).
MACHINE LIMITS FOR THIS SEAT (override the general rules): DAYTIME, the owner is using this laptop and other seats run at once. Use \`-p 2\`, no load generators, no -race on whole packages, and do NOT run the hot-path rows (TestIntegration_HotPath*, TestV3_HotPath*) or whole test/integration / test/e2e packages; run the focused rows you add or touch (by exact name) and each touched package in full once. The Linux container is STOPPED: do not start it; the coordinator runs Linux verification and the whole e2e package at night. No real Claude Code sessions.
Other seats in this wave: e2erows (the three e2e rows and the store benchmark HOME leak), spoolseal (PreCompact after client spools, spool-submode wording, slow-disk docs), relhyg (bundle notices, descriptions, marketplace trigger, recall self-record prefix), ci (hosted ci.yml reds: os/exec allow-list, macOS zombie liveness and socket paths, Q1 non-reference disk declaration, cover's TestE2EHookRoundTrip, stubskips timeout, test/fault artifacts), winci (hosted Windows: the 8.3 short-name deny-rule bypass, elevated-runner ACL fixtures, Stop behind a session end's drain, cross-drive paths). Do not fix their items or edit their files beyond what your fix needs; if your root cause is theirs, say so and stop.`

const LIM16B = `Wave 16 is merged into your base, along with every earlier wave. Its reports are on your base under plans/sdd/V6-closeout/w16-<ws>/report.md; read the one your task names. Coordinator decisions D53 and D55 in the ledger (${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md) govern this wave.
MACHINE LIMITS FOR THIS SEAT (override the general rules): DAYTIME, the owner is using this laptop and three other seats run at once. Use \`-p 2\`, no load generators, no -race on whole packages, no hot-path rows (TestIntegration_HotPath*, TestV3_HotPath*), and no whole test/integration or test/e2e package. Run the focused rows you add or touch by exact name, and each touched package in full once. The Linux container is STOPPED and Docker Desktop's engine is stopped; only the cover seat may start them, as its task says. No real Claude Code sessions.
Other seats in this wave: settle (PreCompact settle follow-ups), polish (hostperm 8.3 trigger, grammar self-markers, plugin keywords), cirec (CI records, timeouts, the non-reference-disk docs), cover (Linux coverage floors for internal/paths and internal/store). Do not edit their files beyond what your fix needs.`

const WS = []

WS.push({ ws: 'settle', cid: 'C4.3', effort: 'high', reviewEffort: 'high', task: `TASK — close wave 16 spoolseal's verify findings (plans/sdd/V6-closeout/w16-spoolseal/report.md, section Verify) and one open item.
${LIM16B}
D55 approves these as built: precompactSettleBound (B-E minus MaxPreCompactWindow, 500 ms), a hard-deadline settle, unreplayedNamesBudgetDivisor 20, the kept WARN level and the new wording, and doctor's spool.pending staying degraded when client spools remain while the daemon serves. These four still need fixing:
(1) MINOR, the bound. settleFast now scans every client spool in full (spooledCaptures through pendingClientCaptures: ReadFileShared, json-scan, leaseHeld per capture) BEFORE the bound's context starts. unreplayedCaptures then repeats the whole scan after the bound. Two unbounded scans sit outside the 500 ms, and a healthy session pays them for other sessions' backlogs.
  - Start the settle's deadline before the first scan, and make every scan count against the bound or against a named, derived part of B-E.
  - Reuse the first scan's result; after the bound, rescan only the files it named plus newer ones.
  - Make the healthy-session path (no spool of its own) cost at most a directory listing, as the implementer's settleFast did. Prove it with a row timing the healthy-session case beside a large backlog of other sessions' spools (count files, not wall time, or a deterministic seam).
  - Correct the comment and report claims to match.
(2) MINOR, the estimator. The names allowance is priced with tokens.New(cfg, ""), the identity factor. The seal's Truncate uses the project-calibrated estimator (tokens.NewForProject, factor clamped to [0.6, 1.6]). Price the allowance with the estimator the seal uses (the draft source's Tokens), and add a row with a calibrated factor above 1.
(3) NIT, doctor wording. doctor's sync-submode detail (internal/cli/doctor.go:932-940) calls every spool file a client spool, but the directory also holds daemon WAL spool files (wal-*.ndjson), which the client-spool watcher does not replay. Split the count, or word it generically when WAL files are present.
(4) OPEN ITEM, freshness. In spool submode no hot-path hook connects, so the client-spool watcher is kicked only by a non-hot request or by the idle drain about 120 s after the last served request, and recall can lag. Kick the watcher from the scheduler's idle tick while the hot path is in spool submode, bounded like the existing watcher pass (no new number unless derived and named). Add a row.
Scope: internal/daemon (precompact_settle.go, spool watcher and idle tick), internal/cli/doctor.go, their tests.` })

WS.push({ ws: 'polish', cid: 'C4.6', effort: 'medium', reviewEffort: 'high', task: `TASK — three small follow-ups from wave 16 (reports w16-winci, w16-relhyg on your base).
${LIM16B}
(1) MINOR, hostperm 8.3 trigger (w16-winci verify finding). In internal/hostperm/policy.go:583 (longNameAliases, pending for glob rules; also rules.go:313 and :377), the glob branch sets shortPending for any rule with a tilde after its concrete prefix (strings.Contains(s, "~")). The literal branch uses shortShaped (a tilde, then a digit). So a common non-8.3 rule such as Read(**/*~) refuses every retrieval of a deleted file's history, which contradicts docs/security.md and doc.go ("only while a rule names an 8.3 name").
  - Use one 8.3-shape predicate everywhere: shortShaped, widened if you want to a tilde followed by a digit or a glob metacharacter (CREDEN~?.SEC).
  - Apply it in pending, prefixRow and literalEqual.
  - Add seam rows: under Read(**/*~) a deleted file is allowed and an existing foo.txt~ is still denied; under Read(**/CREDEN~1.SEC) the fail-closed refusal still holds.
  D55 approves the fail-closed behaviour itself.
(2) Grammar self-markers. internal/grammar/statewarn.go recognises Qompack's own slash commands only by the literal "/qompack:" prefix, and its MCP self-marker matches only mcp__qompack__. A release entry named qompack-<os>-<arch> may be namespaced differently by the host (D53(f) observes it in the candidate 6 live lane). Recognise the host's mcp__plugin_<entry>_qompack__<tool> form with the same predicate as internal/mcp/recall_rank.go (reuse it or share it without an import cycle). Make the slash-command check accept the plugin namespace whatever the entry segment (/qompack:..., /qompack-<os>-<arch>:...). Add rows for both spellings, plus negatives for another plugin's tools and commands.
(3) plugin.json keywords: drop "cache"; F8 calls cache-awareness unsupported. Keep "compaction", which describes what Qompack responds to. Regenerate any generated manifest or marketplace file and update the tests that pin the keywords.
Scope: internal/hostperm, internal/grammar (statewarn), internal/mcp only to share the predicate, plugin/.claude-plugin/plugin.json, internal/pluginmanifest, tools/devtool (marketplace), their tests and docs/security.md if wording changes.` })

WS.push({ ws: 'cirec', cid: 'C7.2', effort: 'medium', reviewEffort: 'high', task: `TASK — finish wave 16 ci's records and decisions (plans/sdd/V6-closeout/w16-ci/report.md on your base).
${LIM16B}
D55 decides these items; implement them exactly:
(1) Windows test leg timeout. The windows-latest leg of ci.yml's test job runs -count=2 with -timeout 60m; the other legs keep 30m. Derivation (write it in a comment): -timeout is per test binary, and devtool's wholeTreeTestTimeout (30m) is sized for one pass, so two passes take 2 x 30m. Measured: internal/daemon was killed at 1800 s in run 36816905394's windows test job under -count=2; alone at -count=2 locally it took 527 s with no hang. Record the local outcome in plans/sdd/V6-closeout/w16-ci/runs/fix-daemon-count2-windows.log:
  - 526.6 s;
  - three 0xc0000142 spawn failures in iteration 2 (STATUS_DLL_INIT_FAILED on a loaded daytime host), which pass at -count=3 alone.
  The source JSON is the session scratchpad's w16/ci/daemon-count2.json under C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/; copy the relevant lines. Restate the report's needs_owner item to match.
(2) QOMPACK_NONREFERENCE_DISK scope. B-A stays in the reported set, because it contains B-B's durable ingest (D41). nightly.yml's bench-deep job also declares it on hosted runners: windows-latest B-B p99 was 1310.7 ms against 50 in run 36820740318. Add bench-deep to nightlyNonrefDiskJobs in test/guards/nonrefdisk_test.go.
(3) Docs to match the shipped CI (w16-ci review finding 5):
  - plans/00-ARCHITECTURE.md: the B-B row near line 282, and the CI table's bench-gate, timing, test-e2e, cover and security rows;
  - docs/adr/0010: an addendum, or a new ADR numbered after the last one, for QOMPACK_NONREFERENCE_DISK citing Q1, D53(e) and D55. It says what is reported and what stays gated, and that it is honoured only under GITHUB_ACTIONS=true.
  Any revision rule on 00-ARCHITECTURE applies: add a revision-log line if the document keeps one.
(4) NIT: walWaitDiag's doc (test/e2e/daemon_e2e_test.go:262-268) and the evidence log describe the TestE2EHookRoundTrip cause as inferred from the differential and the mechanism, not observed. The first co-loaded red that prints walWaitDiag settles it; there, 'spool empty, WAL short' still means a product ingest race.
(5) Local devtool test still runs test/e2e inside its co-loaded whole-tree pass, against ADR 0010 decision 4. If the fix is small and contained to tools/devtool (run test/e2e in its own pass, as the hosted test job does), make it. Otherwise record it.
Scope: .github/workflows/ci.yml, .github/workflows/nightly.yml, test/guards/nonrefdisk_test.go, plans/00-ARCHITECTURE.md, docs/adr/, test/e2e/daemon_e2e_test.go (doc only), tools/devtool (test task), plans/sdd/V6-closeout/w16-ci/runs/.` })

WS.push({ ws: 'cover', cid: 'C3.6', effort: 'high', reviewEffort: 'high', task: `TASK — the Linux coverage floors (C3.6): internal/paths is at 86.3 % and internal/store at 86.6 %, against the 90 % floor on ubuntu (ci.yml cover job, run 36816905394, job 110223848071; floors from the coveragefloors source in tools/devtool).
${LIM16B}
- Read the job log: \`gh api repos/AidanHT/qompack/actions/jobs/110223848071/logs\`, read-only.
- Measure the per-function coverage of both packages on Linux, then raise each to at least 90 % with meaningful tests: behaviour rows for real branches, error paths through existing seams, and platform branches that run on Linux.
- No padding: no tests that only call functions without asserting their effects, no coverage-only build tags, no excluded files, and no lowered floor. A function that can only be reached on Windows does not count on Linux. Say which uncovered lines are Linux-unreachable by design.
- Measure on Linux. You may start Docker Desktop's engine (\`docker desktop start\`) and the existing container qompack-v6-linux-verification (\`docker start\`, never create, remove or reset it). Run the committed gate script plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh with --prefix cx-w16b-cover, --no-race and a coverage profile for the two packages (read its header for the flags; pass --repo and --out as Windows-style C:/ paths). Or run the same non-root \`go test -coverprofile\` the cover job runs.
- When you are done, stop the container (\`docker stop qompack-v6-linux-verification\`) and the engine (\`docker desktop stop\`): the owner needs the memory.
- Report before and after per package, with the evidence log.
Scope: tests in internal/paths and internal/store (and their test helpers). Product code changes only for a real defect a new test finds; report each one.` })

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
    const review = await agent(`You are an independent, adversarial reviewer for Qompack V6 close-out wave-16b workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-w16b-${w.ws}, branch closeout/w16b-${w.ws}, base ${BASE}. The task was:\n---\n${w.task}\n---\nImplementer result: ${JSON.stringify(impl)}\nReview \`git -C ${ROOT}/qompack-cx-w16b-${w.ws} log ${BASE}..HEAD\` and the full diff \`git diff ${BASE} HEAD\`. READ-ONLY: do not edit, commit, stash or reset. You may run focused tests (never ./...; never kill processes you did not start; the machine is loaded — re-run a timing failure alone before believing it). No real claude sessions; never touch the real ~/.qompack or ~/.claude.\n${LENS}\nReturn findings with severity, file:line, evidence and a concrete fix; if sound, verdict "sound" and an empty list.`,
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
    const verify = await agent(`You are an independent verifier for Qompack V6 close-out wave-16b workstream "${r.ws}". Worktree ${ROOT}/qompack-cx-w16b-${r.ws}, branch closeout/w16b-${r.ws}. A reviewer raised these findings:\n${JSON.stringify(r.actionable, null, 1)}\nThe fix seat's result: ${JSON.stringify(r.final)}\nFor EACH finding decide whether it is now resolved correctly or rebutted soundly, by reading the code at HEAD and the fix commits (\`git -C ${ROOT}/qompack-cx-w16b-${r.ws} log ${r.impl.head}..HEAD\`), and check the fix commits introduced no new defect or check-weakening. READ-ONLY: do not edit, commit, stash or reset; focused tests allowed (never ./...; never kill processes you did not start; never touch the real ~/.qompack or ~/.claude). Return as findings ONLY what is still wrong (unresolved, badly rebutted, or newly introduced), with severity, file:line, evidence and fix; verdict "sound" with an empty list if everything is resolved.`,
      { label: `verify:${r.ws}`, phase: 'Verify', schema: FINDINGS, effort: r.w.reviewEffort })
    return { ws: r.ws, impl: r.impl, review: r.review, final: r.final, fixRan: true, verify }
  },
)
return results
