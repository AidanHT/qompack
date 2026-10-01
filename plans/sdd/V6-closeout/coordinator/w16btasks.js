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
