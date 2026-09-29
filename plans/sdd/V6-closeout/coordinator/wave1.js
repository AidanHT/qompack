export const meta = {
  name: 'v6-closeout-wave1',
  description: 'Parallel V6 close-out workstreams: fix blockers, perf, host policy, rollover, eval harness; each reviewed adversarially and fixed',
  phases: [
    { title: 'Implement', detail: 'nine workstreams, one worktree each' },
    { title: 'Review', detail: 'independent adversarial review per workstream' },
    { title: 'Fix', detail: 'resolve confirmed review findings' },
  ],
}

const ROOT = 'C:/Users/Quant/Documents/Programming/Projects'

const COMMON = (w) => `You are one of several parallel workstreams closing out the Qompack V6 verification (Qompack is a Go Claude Code plugin: hooks -> resident daemon -> content-addressed store under .qompack/, an MCP retrieval server, slash commands). Coordinator ledger: ${ROOT}/qompack-v6/plans/V6-CLOSEOUT-CHECKLIST.md — read it first; do NOT edit it (the coordinator owns it).

YOUR WORKTREE: ${ROOT}/qompack-cx-${w.ws} on branch closeout/${w.ws} (cut from verify/v6 @ cf31e01). The Bash tool resets cwd between calls, so prefix every command with \`cd ${ROOT}/qompack-cx-${w.ws} &&\` or use absolute paths/\`git -C\`. Never edit files in qompack-v6, the root qompack checkout, or any other worktree. Never push, tag, merge, rebase other branches, or change git config/hooks.

Hard rules (owner directives, non-negotiable):
- Never weaken a check: no t.Skip, no //nolint, no //nomagic:allow added to silence something, no lowered threshold or budget, no regenerated golden to match changed output, no deleted or loosened assertion, no widened timeout that hides a defect. If a test's criterion is genuinely wrong after an intended change, change it only with an explicit written rationale in your report ("criterion change: old -> new, why").
- Root cause before fixes: reproduce first, write the failing regression test first, then fix.
- Preserve failures as evidence: keep logs you produce under plans/sdd/V6-closeout/${w.ws}/runs/ in your worktree (commit them with your report).
- Commits: conventional \`type(scope): subject\` (lowercase scope, subject <=64 chars, no trailing period), body lines <=100 chars, a \`Refs: V6-VERIFY, ${w.cid}\` footer on feat/fix commits, and NO attribution trailers of any kind (no Co-Authored-By, Signed-off-by, "Generated with", robot emoji) — the commit-msg hook rejects them. Small focused commits; code separate from docs/evidence.
- Formatting/lint: \`go run ./tools/devtool fmt\` / \`fmt-check\` (repo-pinned gofumpt), \`go vet\` on packages you touched, and \`go run ./tools/devtool lint\` once before your final commit.
- Tests: run FOCUSED tests (\`go test ./pkg -run 'Regex' -count=1 -timeout=30m\`) plus each package you touched once in full. Do NOT run the whole tree (./... or devtool test/test-race) — the coordinator runs integrated gates. Up to ten other agents share this 22-CPU Windows machine: a wall-clock/timing assertion that fails may be co-load — re-run it alone once before treating it as real, and say which it was. Never let a pipe mask go test's exit code (use \`; echo exit=$?\` or pipefail). CGO_ENABLED=1 with gcc (installed) for -race on Windows.
- Processes: never kill a process you did not start (other agents' test daemons look identical); only stop PIDs you launched yourself.
- Linux: container \`qompack-v6-linux-verification\` (golang 1.26.6, runs as root, 22 CPUs) via \`docker exec\`. Copy your exact commit in with \`git bundle\` + \`docker cp\` into a fresh /work/cx-${w.ws}-<suffix> dir; never reuse or delete other /work dirs. Root makes permission fixtures vacuous — run as a non-root user (create one if absent) when permissions matter. Use GOMAXPROCS=4 there.
- Read the relevant decision/design documents under plans/sdd/V6-remediation/ before changing a contract they define; if your change alters a recorded decision, say so explicitly in the report.

Deliverable: commits on closeout/${w.ws}, and a committed report at plans/sdd/V6-closeout/${w.ws}/report.md covering: root cause with evidence, what changed and why, exact test commands + results (Windows and Linux where run), criterion changes (if any) with rationale, anything left open, anything needing an owner decision.`

const WS = [
  { ws: 'ingest', cid: 'C1.1', critical: true, task: `TASK C1.1 — live-ingest regression (release blocker).
Symptom (reproduced in isolation, no co-load, on cf31e01): \`go test ./test/e2e -run 'TestE2E_ObserverThroughDaemon$' -count=1 -v\` -> "index/tool_use.jsonl never reached 44 lines: index holds 1 lines; undrained client spool [client-NNNN.ndjson] ... waited on the 30s idle-tick drain". Log: plans/sdd/V6-remediation/runs/closeout-repro-observer-isolated.log. The same shape fails TestE2E_ThinSliceDropsControlOnlyEdges and TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently (test/e2e) and six test/fault cases ("the observer never indexed toolu_fault_* ... within 30s": TestFault_AuditSeesADeletedObjectUnderALiveIndex, _MCPChildKilledMidRequest, _DaemonKilledMidIngest, _UnavailableHistoricalObject, _CheckpointDropsAnUnresolvablePointer, _CheckpointResolvabilityIsBlindToADeletedObject); see plans/sdd/V6-remediation/runs/rc1-integrated-whole-tree.log. Linux shows the same test/fault failures.
Prime suspect (verify, do not assume): the V6 same-session ordering gate (commit c34acb4; internal/daemon/delivery_order.go predecessorsAcknowledged; ingest.go dispatch around the "Ordering gate" comment). With several workers, event N+1 is dispatched while event N is still processing, the gate returns false and the job is left for the drain ("retried by a later drain, never spun on"); something ALSO prevents the drain from publishing it (the index never grows even after the 30 s idle drain), and some hooks fall back to the client spool. Establish each of these three facts with evidence (counters l0_ordering_deferred / l0_drain_ordering_deferred, daemon log, temporary instrumentation) — why live dispatch defers, why the drain does not recover, why clients spool.
Required outcome: same-session leased events publish in arrival order (keep the ordering contract in plans/sdd/V6-remediation/delivery-order-decision.md and the terminal-disposition contract in delivery-terminal-decision.md, including fail-closed on an unreadable frontier) WITHOUT stranding work behind the idle drain — e.g. per-session FIFO dispatch (serialize same-session jobs, keep cross-session parallelism) and/or waking deferred successors when their predecessor commits. Bounded memory, no busy-spin, no unbounded wait of one worker on another, crash/restart semantics unchanged (WAL retained until durable ACK, identity never re-minted). Fix the drain-side defect too if it is independent.
Verification: a failing-first deterministic regression test in internal/daemon (concurrent same-session arrivals); then the e2e/fault cases above pass; internal/daemon passes fully once; ordering/drain/ingest tests pass under -race -count=5; the same focused set passes on Linux in the container (non-root).
Scope: internal/daemon ingest.go, delivery_order.go, drain.go and their tests (test/e2e or test/fault only if a test has a genuine defect, with rationale). Do NOT edit delivery_lease.go, delivery_generation.go, delivery_segment*.go, delivery_radix.go (the rollover workstream owns them) unless unavoidable — then minimal and flagged.` },

  { ws: 'e2e', cid: 'C1.2/C1.3', task: `TASK C1.2 + C1.3 — two further e2e failures on candidate cf31e01 (log plans/sdd/V6-remediation/runs/rc1-integrated-whole-tree.log):
(1) TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting (test/e2e/install_test.go:589): after installing the bundle, fsck exits 1 with "index.tool_use (severity 2): tool_use toolu_sp17_sess-e2e-sp17-unknown-schema_5_00 reports turn 0 after turn 1 in session ; turns are monotone" and "publication (severity 1): publication audit is incomplete; zero observed gaps cannot certify completeness; unexpected entry in the capture tree". Note the EMPTY session id in the message. The test uses the installed Claude Code CLI (2.1.280) to install into user scope and uninstalls afterwards — that flow is by design.
(2) TestV1_ConfigPrecedenceReachesHookBehaviour/bare_hook_records_an_over_budget_delivery_instead_of_dropping_it (test/e2e/v1_integration_test.go:388): "a bounded prefix survives as evidence" — got empty.
Another workstream (closeout/ingest) is fixing a live-ingest regression: the V6 same-session ordering gate (internal/daemon/delivery_order.go, ingest.go dispatch) defers concurrent same-session events to the idle drain and they are not recovered. First determine, with evidence, whether each of (1) and (2) is (a) caused by that regression, (b) an independent product defect, or (c) a test whose criterion is stale after an intended V6 change (e.g. 00e0c98 "refuse unprovable file payloads before persistence", 80a3e04/7e5a141 publication audit, c34acb4 ordering, 99108a2 observation binding). To test hypothesis (a) you may apply a TEMPORARY experimental patch in your worktree (e.g. force one ingest worker) — never commit it; record the experiment and result.
Then fix every independent product defect (failing regression test first) — e.g. turn assignment violating monotonicity, a publication-audit false positive (identify the exact "unexpected entry"), the empty session id in the fsck message; for (c) change a test only with a written criterion-change rationale. If a failure is purely (a), leave it for the ingest workstream and document the evidence.
Scope: whichever package owns the root cause (likely internal/store publication audit, internal/cli fsck, internal/observer turn assignment, internal/hookio or internal/cli capture). Do NOT edit internal/daemon ingest.go, delivery_order.go, drain.go (ingest workstream) or delivery_lease/generation/segment/radix (rollover workstream).` },

  { ws: 'config', cid: 'C1.8', task: `TASK C1.8 — SP-18's discovered defect (see plans/sdd/V6-SP-18-documentation-and-uat/ and docs/troubleshooting.md §6): config.LoadForCapture (internal/config/capture_load.go) has no per-leaf fallback, so one bad config key makes every hook admit nothing and create no .qompack/ while \`qompack self-test\` reports config.load ok. The documented config contract (README "Configuration", docs/config-reference.md): an invalid value is never fatal — the leaf falls back to its default, the violation is reported and recorded in .qompack/state/config-violations.json — and an unknown key only warns.
Verify the defect with failing tests first: a unit test in internal/config, and a hook-level test (project config with one invalid leaf and one unknown key: capture still happens using the default for that leaf, the violation is recorded/reported). Fix LoadForCapture to follow the same per-leaf fallback contract as the normal loader while KEEPING its security bounds (size/UTF-8/absolute-path refusals stay fail-closed) — explain which failures are structural (must stay fatal to capture) vs per-leaf. Make \`self-test\`/\`doctor\` report capture-config degradation honestly (never "ok" when capture would refuse or degrade). Update docs/troubleshooting.md §6 and any doc that describes the old behaviour; if a generated doc's input changed, regenerate only via its generator and make \`go run ./tools/devtool gen-config-docs --check\` (and the other gen-*-docs --check) pass; run \`go test ./test/docs\`.
Scope: internal/config, internal/cli (self-test/doctor/hook admission as needed), docs.` },

  { ws: 'hostperm', cid: 'C1.9', critical: true, task: `TASK C1.9 — V6-HOST-1: archive retrieval must honor the host's current Read deny rules.
Context: plans/V6-report.md §5 (V6-HOST-1), plans/sdd/V6-VERIFY/trust-review.md, docs/security.md §1/§8, internal/mcp/authorize.go (V6-AUTH fixes c9b5251: every retrieval form — id, root hash, chunk hash, path — re-checks current project/path authority). Today the MCP server enforces project containment only; a file the user has since placed under a Claude Code permissions.deny Read rule (or ask) can still be served from the archive.
Owner default decision: implement honoring of Claude Code permission rules for archived reads. First fetch the CURRENT official docs for exact rule syntax and settings precedence (WebFetch https://code.claude.com/docs/en/settings and https://code.claude.com/docs/en/iam, plus the permissions pages they link): path patterns (gitignore-style; //absolute, ~/home, /relative-to-settings-file, ./ or bare relative to cwd), bare \`Read\` (all reads), settings files — managed (confirm paths per OS), user ~/.claude/settings.json, project .claude/settings.json, local .claude/settings.local.json — and CLAUDE_CONFIG_DIR. Record the doc snapshot (URL, date, quoted rule text relied on) in your report.
Implement: a small read-only package (e.g. internal/hostperm) that loads and evaluates Read deny and ask rules for an absolute file path; MCP retrieval — ALL forms, including previews/snippets returned by recall/timeline/why/dropped, not only expand/re_read — refuses archived content whose original path matches a deny rule, with a distinct error reason (never "not found"); ask rules are treated as deny for archived reads (a plugin cannot prompt) with their own reason; unreadable or malformed settings fail CLOSED for path-bearing content ("host policy unavailable"); records with no path keep their current V6-AUTH behaviour. Reload per request or on mtime change with bounded cost (measure it). Tests: rule matching (every pattern form, Windows and POSIX paths, Windows case-insensitivity, symlink/junction handling consistent with authorize.go), MCP handler tests for every retrieval form, and a test/security case. Document the residual gap honestly in docs/security.md and docs/cannot-do.md: session-only rules, --disallowedTools flags and hook-based policies are invisible to a plugin. Regenerate gen-mcp-docs only via its generator if tool descriptions change.
Scope: new internal/hostperm (obey the repo's package/import rules: check OWNERS.tsv, test/guards import/build-order guards, devtool lint import allow-list), internal/mcp, test/security, docs. Do not touch internal/daemon.` },

  { ws: 'rollover', cid: 'C1.10', critical: true, task: `TASK C1.10 — SP20-D4 delivery-journal capacity. Owner decision D2 (2026-09-22): finish the rollover gates, then ENABLE rollover by default.
Read first: plans/sdd/V6-remediation/delivery-capacity-design.md, delivery-capacity-integration-work.md, delivery-rollover-*.md (esp. coordinator-decision, main-adjudication, critical-review, revised-review), delivery-segment-retention-decision.md, gc-segment-integration-work.md, gc-retention-read-work.md, rollover-compatibility-critical-review.md; plans/CARRIED-DEFECTS.tsv row SP20-D4 and its detail-document section. Today lease/ack journals cap at 65,536 entries / 64 MiB (internal/daemon/delivery_lease.go) and then deliveries are refused (fail-closed, WAL/spool retained); segmented rollover + generation store + radix index exist behind \`enableDeliveryGenerations = false\` (delivery_generation.go) with deliveryRolloverEntries/Bytes thresholds.
Gates before flipping the default (each with tests + evidence):
 1. Old-reader safety: an older binary (a reference build of 301a8e9 exists in the container under /work/compat-old-linux-301a8e9 — verify what it is; or build your own from 301a8e9/v0.2.0 into your own dir) opening a store with rotated segments must refuse or degrade safely — never re-mint identities, reassign arrivals or truncate evidence. Automate the drill (script + recorded run).
 2. Backup/restore: maintenance backup/verify/restore (internal/store/maintenance.go, internal/cli/backup.go) covers segmented stores (segment files, seals, journal log/head, generation manifest, radix objects); restore -> daemon restart -> identities and ACKs intact; fsck certifies.
 3. GC retention across segments (7086bb5, gc_delivery_segments.go): live-rotation test where GC runs during/after rotations and never collects a root referenced only by an archived segment; a negative control proves the test can fail.
 4. Offline tools: seal check/repair and fsck are segment-aware (36205d1); the open item "per-segment Rule R (--accept-torn-slot) not applied to segment seals" — implement or document with rationale.
 5. Resource cost: measure rotation latency (does rotation stall the ACK/hot path?), disk growth per 100k deliveries, memory bound across >=200k deliveries and >=3 rotations; report figures + platform; no budget changes.
Then flip the default ON with documented thresholds consistent with the 65,536/64 MiB caps, invert the SP20-D4 evidence test into a regression proving capture continues past the old cap across restart (record the criterion change), set CARRIED-DEFECTS.tsv row SP20-D4 to fixed with the new evidence test name and update its detail-doc section (test/guards carrieddefects must stay green for that row), and update docs describing the cap (architecture, troubleshooting, cannot-do, config reference if a key is exposed).
Coordination: closeout/ingest is changing internal/daemon ingest.go, delivery_order.go and drain.go (ordering regression). Do NOT edit those three files (if unavoidable, minimal and flagged). End-to-end live-ingest tests may fail on your base for that unrelated reason — distinguish it with evidence.` },

  { ws: 'perfstore', cid: 'C2.6/C2.7', task: `TASK C2.6/C2.7 — carried performance defects SP06-D2 (PutBytes cold/warm budgets 3 ms / 400 us are 5.2x / 14.1x over on Windows) and SP20-D2 (GetChunk verify-on-read adds Lstat+fstat+content hash; ~85-88 us/op). Read plans/CARRIED-DEFECTS.tsv rows + their detail-doc sections, plans/sdd/V6-remediation/performance-analysis.md (prior read-only analysis: write overage is Windows small-file/AV/rename cost — measure on Linux first; read fix W1 = presize the bounded object read buffer, which 19344e3 may already have done), and locate the budgets' definitions and benchmarks.
Do: (1) measure current numbers on Windows and Linux (container) with the existing benchmarks, -benchmem -count 10, and profile (pprof cpu/alloc) — other agents run concurrently, so interleave A/B runs and compare with benchstat like-for-like. (2) Implement real speed-ups that keep every integrity/durability property (verify-on-read hash and path checks stay; fsync-before-publish stays; no caching that could serve stale or unverified bytes; persisted formats and goldens byte-identical). (3) Report before/after distributions per platform. (4) Do NOT change any budget constant or CARRIED-DEFECTS status; instead write an owner proposal in the report per row: the figures you obtained (with load conditions), what remains over and why (e.g. NTFS/Defender per-file create/rename cost), and a recommended disposition (fixed / platform-scoped budget with numbers / wontfix with reason).
Scope: internal/store (and internal/paths if write-path I/O helpers live there) plus their benchmarks. Do not touch internal/daemon.` },

  { ws: 'perfobs', cid: 'C2.3-C2.5', task: `TASK C2.3-C2.5 — carried performance defects SP08-D1 (OnToolUse over a 256 KB tool result breaches budget B-C l0_process p99 < 50 ms: 53-115 ms; evidence BenchmarkOnToolUse_TestOutput256KB), SP10-D1 (checkpoint Finalize 42.8-73 ms/op vs its exit criterion; BenchmarkFinalize), SP09-D1 (negknow Open 258-633 ms CPU/op vs 300 ms; TestBudget_Open). Read the CARRIED-DEFECTS.tsv rows + detail-doc sections and the budgets' definitions.
Do: measure current figures (Windows + Linux container), profile (pprof cpu/alloc), find the dominant costs, and implement real algorithmic/allocation speed-ups that keep behaviour byte-identical (golden outputs unchanged, same persisted formats) — e.g. avoid re-hashing/re-canonicalizing unchanged content, stream instead of whole-buffer copies, bound per-event work. Report before/after with -count 10 + benchstat per platform (other agents run concurrently: interleave A/B). Do NOT change any budget constant or CARRIED-DEFECTS status; write an owner proposal per row (fixed / platform-scoped budget with measured numbers / wontfix + reason).
Scope: internal/observer, internal/checkpoint, internal/negknow (and shared helpers in internal/canon, internal/chunk, internal/sketch only if the hot cost is there — keep format/golden identity). Do not touch internal/daemon or internal/store.` },

  { ws: 'eval', cid: 'C5.4', task: `TASK C5.4 (+ foundations for Phase 4) — real-host evaluation harness. Today eval.LiveRunner is nil (internal/eval/types.go, replay.go) and \`qompack eval --json\` exits 1 ("no evaluation artifacts are readable"). Owner decision D3: evaluation and UAT are agent-run real headless Claude Code sessions on this machine (installed \`claude\` CLI 2.1.280, logged in on the user's subscription), ~40-80 sessions total, recorded as agent-executed, never as human UAT.
Build:
 1. A live driver that runs a real \`claude -p\` session in a disposable project directory with the Qompack plugin loaded — support both \`--plugin-dir <bundle>\` (if the CLI supports it; verify) and the real marketplace flow (\`claude plugin marketplace add <dir>\` + \`claude plugin install qompack@<marketplace>\` at project scope where possible, then uninstall/remove; see test/e2e/install_test.go and install_helpers_test.go for the flow SP-17 exercised; never leave the user's global settings modified — snapshot and verify them). Capture \`--output-format stream-json --verbose\` (or json) and parse per-request usage: input, cache_creation (+ TTL split if present), cache_read, output, model, duration, num_turns, total_cost_usd if reported, errors. Verify the actual current CLI flags/JSON shape with \`claude --help\`, \`claude plugin --help\` and ONE tiny real session (cheap model e.g. claude-haiku-4-5-20251001, trivial prompt, --max-turns 2). Build the bundle with \`go run ./tools/devtool bundle\` (see its flags).
 2. Forced compaction inside a headless session: determine empirically and from the docs (WebFetch https://code.claude.com/docs/en/ pages on headless mode, hooks, compaction, environment variables) how to trigger compaction deterministically — e.g. a \`/compact\` prompt on a \`--resume <session_id>\` turn, or an autocompact-threshold env var if one exists. Prove PreCompact -> Qompack checkpoint -> SessionStart(source=compact) -> rehydration injection in one real session (inspect .qompack/ and the transcript JSONL). This is the core Qompack claim; if it cannot be triggered headless, say exactly why.
 3. eval.LiveRunner implementation + a devtool entry point (e.g. \`go run ./tools/devtool live-eval --tasks <file> --arms stock,qompack --trials N --model M --out <dir>\`) that runs a declared task set: each task = a repo fixture + multi-step instructions + a forced compaction midway + post-compaction questions/actions whose correctness is checked automatically (files/tests/answers), including changing-requirement and held-out variants. Arms: stock (no plugin) vs qompack (plugin). Per trial record: completion and constraint outcomes, per-category usage, a cost estimate from a dated rate table (labelled estimate; subscription is not cash), wall time, hook latency seen by the host, store size. Never double-count resumed/overlapping usage.
 4. A task-set file (8-12 tasks) and a pre-registration document plans/sdd/V6-closeout/eval/preregistration.md: hypotheses, primary outcomes (task success after compaction, constraint/regression violations, recoverability), secondary (tokens/cost/latency), sample size per arm with rationale, decision rule — written BEFORE any trial outcomes exist.
 5. Unit tests for parsing/accounting with recorded fixtures (no network in tests; live runs are opt-in behind an env gate so CI never calls a model).
Spending: at most 6 short real sessions in this workstream for harness validation (cheap model, low max-turns). The coordinator runs the real trials later on the fixed plugin. The plugin currently has a live-ingest regression being fixed elsewhere, so capture counts in smoke sessions may be short — record it, do not debug the daemon.
Scope: internal/eval, tools/devtool (new subcommand), fixtures under testdata/eval or plans/sdd/V6-closeout/eval, harness docs. Do not touch internal/daemon, internal/store, internal/observer, internal/mcp.` },

  { ws: 'linux', cid: 'C3.4', task: `TASK — Linux lane + the six never-reported Windows packages.
A Linux -race run of candidate 3dab390 (non-e2e packages) exists in the container at /work/linux-test-candidate-3dab390-artifacts (test.jsonl, stderr.log, race.15390, per-suite dirs); it ran as ROOT. Failures: internal/ipc TestSpoolWriteFailureDropsAndLoudsOnce; internal/paths TestCreateNew_SetsReadOnly; internal/testutil TestWindowsHostileFiles_AllCreatable (read-only case); test/fault (many — most match the live-ingest regression; others: TestFault_Lifecycle, _LockContention, _PublicationBoundaries, _ReadOnlyObjectsDirectory, _WriteFailures, TestV6Backup_*, TestV6_FsckReportsUnpublishedCapture...); test/guards TestCarriedDefects_WaveReportRequiresResolution (expected until the V6 report — leave it) and TestGuard_TheV1DeliveryPositionHasOneEncoder (NEW — investigate); test/integration TestIntegration_DegradedPassiveStillWritesToTheRealStore, _GCNeverCollectsALiveRootUnderIngest, _HotPathWarmWithRealResidentState; test/security TestSecurity_ArchivedTextIsDataNeverAnInstruction; and a DATA RACE (race.15390) in internal/daemon: handleAdminShutdown's sync.Once (handlers.go:1127) vs daemon.Run (daemon.go:560), from TestRunReturnsOnlyAfterAsyncStopHasFinished.
Do:
 1. A reusable non-root Linux test procedure: a committed script under plans/sdd/V6-closeout/linux/ following the existing dist/v6-remediation/linux-*.sh pattern (git bundle of an exact commit -> fresh /work/<unique> -> identity checks -> run as a non-root user, CGO_ENABLED=1, -race, go test -json per case, race logs retained, exit code recorded). It must take the commit and package list as arguments so the coordinator can reuse it for integrated gates.
 2. Re-run the failing packages non-root at your base (cf31e01) and classify every failure: root-only artifact (vacuous permission fixture) / live-ingest regression (closeout/ingest is fixing the ordering gate in internal/daemon ingest.go, delivery_order.go, drain.go — don't touch those) / real Linux defect / timing under co-load (re-run alone).
 3. Fix the real defects you own: the daemon data race (root cause — test-only or product; fix without weakening), the V1 delivery-position encoder guard, the security ArchivedText case, the integration failures if not ingest-caused, any Linux path/permission defect. Failing-first tests where applicable.
 4. Windows parity: the Windows run of test/guards, test/integration, test/platform, test/release, test/security and tools/devtool never reported (cut off in plans/sdd/V6-remediation/runs/rc1-integrated-whole-tree.log). Run those six packages ONCE on Windows in your worktree (\`go test ./test/guards ./test/integration ./test/platform ./test/release ./test/security ./tools/devtool -count=1 -timeout=40m\`, CGO_ENABLED=0, output + exit code saved under your runs/ dir) and classify failures the same way; fix the ones you own.
Scope: whichever package owns each real defect EXCEPT the three ingest files above and delivery_lease/generation/segment/radix (rollover workstream) — if a root cause lands there, document it with evidence instead of editing.` },
]

const RESULT = {
  type: 'object',
  properties: {
    status: { type: 'string', enum: ['done', 'partial', 'blocked'] },
    head: { type: 'string' },
    commits: { type: 'array', items: { type: 'string' } },
    root_cause: { type: 'string' },
    summary: { type: 'string' },
    tests: { type: 'array', items: { type: 'object', properties: { command: { type: 'string' }, result: { type: 'string' } }, required: ['command', 'result'] } },
    criterion_changes: { type: 'array', items: { type: 'string' } },
    open_issues: { type: 'array', items: { type: 'string' } },
    needs_owner: { type: 'array', items: { type: 'string' } },
    report_path: { type: 'string' },
  },
  required: ['status', 'head', 'commits', 'summary', 'tests', 'open_issues', 'needs_owner'],
}

const FINDINGS = {
  type: 'object',
  properties: {
    verdict: { type: 'string', enum: ['sound', 'needs-fixes', 'unsound'] },
    findings: { type: 'array', items: { type: 'object', properties: {
      severity: { type: 'string', enum: ['blocker', 'major', 'minor', 'nit'] },
      location: { type: 'string' }, issue: { type: 'string' }, evidence: { type: 'string' }, fix: { type: 'string' },
    }, required: ['severity', 'location', 'issue', 'evidence', 'fix'] } },
  },
  required: ['verdict', 'findings'],
}

const LENSES = {
  a: 'Primary lens: root cause proof, correctness, concurrency (races, deadlocks, lock order, goroutine leaks), durability/crash semantics (fsync ordering, WAL retention, idempotency, identity never re-minted), fail-closed error paths, Windows vs POSIX behaviour, bounded memory/CPU.',
  b: 'Primary lens: check-weakening (deleted/loosened assertions, skips, nolint/nomagic, lowered thresholds, regenerated goldens, widened timeouts, tests bent to buggy output), legitimacy of every criterion change, whether the regression test fails without the fix (reason it through or revert in a scratch COPY outside the worktree), contract fidelity with plans/sdd/V6-remediation decision docs and docs/*, docs updated where behaviour changed, commit hygiene (conventional subjects, Refs footer, no attribution trailers).',
}

const reviewPrompt = (w, impl, lens) => `You are an independent, adversarial reviewer for Qompack V6 close-out workstream "${w.ws}" (${w.cid}). Worktree ${ROOT}/qompack-cx-${w.ws}, branch closeout/${w.ws}, base cf31e01. The workstream's task was:
---
${w.task}
---
Implementer's result: ${JSON.stringify(impl)}
Review \`git -C ${ROOT}/qompack-cx-${w.ws} log cf31e01..HEAD\` and the full diff and report. You are READ-ONLY: do not edit, commit, stash or reset anything in the worktree. You may run FOCUSED tests to check testable claims (never ./... or devtool test; never kill processes you did not start; other agents share the machine, so re-run a timing failure alone before believing it).
${lens === 'both' ? LENSES.a + '\n' + LENSES.b : LENSES[lens]}
Also check: was the whole task done, or were parts silently dropped? Return findings with severity, file:line, evidence and the concrete fix. If the work is sound, return verdict "sound" with an empty list — do not pad.`

const fixPrompt = (w, findings) => `${COMMON(w)}

You are the FIX seat for this workstream (the implementer has finished). Its task was:
---
${w.task}
---
Independent reviewers returned these findings:
${JSON.stringify(findings, null, 1)}
For each finding: verify it independently. If correct, fix it (failing test first where applicable) and re-run the relevant focused tests. If wrong, record why with evidence. Blockers and majors must be resolved or explicitly rebutted with evidence. Then append a "Review resolution" section to plans/sdd/V6-closeout/${w.ws}/report.md (finding -> action or rebuttal) and commit. Return the final state of the workstream.`

const results = await pipeline(
  WS,
  (w) => agent(`${COMMON(w)}\n\n${w.task}`, { label: `impl:${w.ws}`, phase: 'Implement', schema: RESULT }),
  async (impl, w) => {
    if (!impl) return { w, impl: null, reviews: [] }
    const lenses = w.critical ? ['a', 'b'] : ['both']
    const reviews = await parallel(lenses.map((l) => () =>
      agent(reviewPrompt(w, impl, l), { label: `review:${w.ws}:${l}`, phase: 'Review', schema: FINDINGS, effort: 'high' })))
    return { w, impl, reviews: reviews.filter(Boolean) }
  },
  async (r) => {
    if (!r.impl) return { ws: r.w.ws, impl: null, reviews: [], final: null }
    const actionable = r.reviews.flatMap((x) => x.findings).filter((f) => f.severity !== 'nit')
    if (actionable.length === 0) return { ws: r.w.ws, impl: r.impl, reviews: r.reviews, final: r.impl, fixRan: false }
    const final = await agent(fixPrompt(r.w, actionable), { label: `fix:${r.w.ws}`, phase: 'Fix', schema: RESULT })
    return { ws: r.w.ws, impl: r.impl, reviews: r.reviews, final, fixRan: true }
  },
)
return results
