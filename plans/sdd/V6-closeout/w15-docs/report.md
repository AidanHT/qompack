# Wave 15 workstream docs

Branch `closeout/w15-docs`. Workflow `wf_0a7ad63f-671`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `9f9d5943`

### Root cause

The docs drifted from the shipped code. D46 removed fidelity and coverage from every retrieval response, but troubleshooting §3, architecture §6 and cannot-do.md still said a response carries them. Capture fidelity lives only on the capture sidecar (internal/store/capture_sidecar.go). UAT-05 assumed a hook's --set reaches the daemon, but spawn.go starts the daemon with only --project. UAT-09 assumed a project with no ledger answers unavailable, but the ledger now opens on first use and answers absent. The binary-file behaviour comes from the host, which decodes before any hook sees the content, and it was undocumented. The stdio probe's denied count is authorize.go refusing pathless self-records; why they fill k is the services seat's defect.

### Summary

All seven D49 documentation items are fixed on closeout/w15-docs (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15-docs, base 08034a3f). No product code changed. The only non-docs edit is the generator for the generated page, tools/devtool/genmcpdocs.go.

**Test first.** The new file test/docs/closeout_claims_test.go holds six rows: TestCloseoutStaleClaimsAreGone, TestFidelityIsReadFromTheCaptureRecord, TestHostBinaryDecodingIsDocumented, TestRecallDeniedCountIsExplained, TestUATNamedTestsExist and TestUATIntroDescribesTheCurrentRecord. All six were RED on the base (runs/04-docs-claims-red.log) and are GREEN after the fixes (runs/05-docs-claims-green.log). The facts behind the fixes come from temporary diagnostics that were run and then removed, not committed as tests (runs/01 and runs/03), plus runs/02, which runs the existing tests UAT-02 now names.

**Findings and fixes:**

(1) F3, fidelity. docs/troubleshooting.md §3 told the reader to read a "record's Fidelity" and to read it "through expand". Neither exists (D46). Capture fidelity is stored only on the capture sidecar, `.qompack/records/captures/<xx>/<observation id>.json` (internal/store/capture_sidecar.go). It has the fields `fidelity`, `redacted`, `truncated`, `source_bytes`, `outcome` and `capture_error`. No command or tool shows it. The `fidelity:` line that fsck prints is store-level restore fidelity, a different set of values. §3 now says how an operator finds the right sidecar: search by the host `tool_use_id`, or for a prompt by `session` plus op `observe.prompt`, with grep and PowerShell examples. It also warns that the file holds the payload bytes. architecture.md §6 and cannot-do.md stopped claiming that a response carries fidelity, and the `full: true` note in troubleshooting was corrected.

(2) Binary decoding by the host. user-guide.md (the fidelity section) and cannot-do.md (a new limit, "No raw bytes of a binary file") now say the same thing. On Claude Code 2.1.280, Read refuses a binary file and no PostToolUse hook fires. Bash output such as `cat` of a binary file arrives already decoded as text, and an image arrives as base64 inside the hook's JSON. So such a capture is recorded as `exact`, meaning exactly what the host delivered, not the file's bytes. Both pages carry the identical sentence "Qompack records what the host delivered and decodes nothing itself.", and a test checks both.

(3) F-C4-UAT05-1. A hook that starts a daemon runs `self daemon --project <root>` only (internal/daemon/spawn.go buildSpawnCommand), so a `--set` on the hook never reaches it. The daemon builds the rehydration under its own configuration. The diagnostic confirmed this: after `session-start --set runtime.rehydrate.maxTokens=150`, the state file still showed a budget of 12000 (runs/03). UAT-05 step 5 now sets both bounds in `<project>/.qompack/config.json` and restarts the daemon. The user guide's `--set` sentence now says the flag applies to its own process only.

(4) UAT-09 step 5. The old "reachable form" was a project with no ledger, which now answers `absent` (TestLiveLedgerToolsAnswerBeforeFirstCompaction). The step now uses a second disposable project whose `.qompack/records/eliminations.jsonl` is created as an empty directory before any session, which is what the re-run did. That puts the ledger in blind mode (TestBlindMode). The step's expectation is unchanged.

(5) UAT-02. A note sits after the Result block, and the verdict is unchanged: "non-exact fidelity is host-limited on Claude Code 2.1.280; covered by `TestCapturePolicyProducesTheCompleteFidelitySet`, `TestHookCapture_OversizeRetainsClassificationWithoutOpaqueBytes`, `TestHookCapture_BinaryPayloadIsClassifiedNotSilentlyRejected`, `TestHookCapture_ShortReadIsRecordedAsPartial` and `TestCaptureSidecar_DegradedCaptureIsRecordedAsSuch` (D49, 2026-09-30)". All five were run green (runs/02). TestUATNamedTestsExist fails if any test named in uat.md stops existing.

(6) C4.9, stdio probe answering denied:N. This is authorization, not a missing host session, and it is not a docs-seat defect. internal/mcp/authorize.go authorizeOrigin refuses any pathless record whose tool is not on its allowlist (Bash, PowerShell, UserPromptSubmit, SubagentStop, record_eliminated, elimination evidence). The refusal reason is "the capture has no usable path provenance", and it covers Qompack's own records of its MCP calls. The host-rule check runs in the daemon against the host's saved settings files; the probe response had no `host_policy`, so those files were readable. The diagnostic showed a pathless recall/expand self-record counted in `denied` while the Read of notes.md is served (runs/01). The C4.9 store holds 5 `mcp__qompack__recall` self-records, and the probe answered denied=5 at the default k=5. That fits the probe's own earlier recall calls filling the top 5 hits, but it is inferred, not proven. It is the services seat's D49 item ("recall's k counts permitted hits and self-records rank after original captures"). I did not fix it. mcp-tools.md now has a paragraph, written in genmcpdocs.go and regenerated, explaining what `denied` counts, why a probe run by hand can answer differently, and that a query can return fewer hits than `k`. I kept that wording true both before and after the services fix.

(7) The uat.md introduction is now three sentences. Candidate 3 (`d5598eb4`) ran all twelve rows: six passed, six failed, routed by D45. Under D47, candidate 4 (`9f6a2fad`) re-ran eleven rows: eight passed, three failed (UAT-03, UAT-06, UAT-09), routed by D49. UAT-10's block is still candidate 3's.

**Checks (all exit 0; the machine was loaded by other seats, but no wall-clock failure was seen):**
- go test ./test/docs in full
- gen-mcp-docs, gen-config-docs and gen-command-docs with --check
- tools/devtool in full once (94.9 s)
- devtool fmt-check (gofumpt reformatted the new test file once)
- go vet on Windows and GOOS=linux
- devtool lint with the eight allowed sub-checks, all PASS

**Criterion changes:** none. Checks were only added, none loosened.

**Note for the merge:** commit 6d3ad421 adds the six rows while the docs are still stale, so test/docs is red at that commit alone and green from b46b439e on. That is intentional, so the red is on record; squash if bisectability matters.

### Commits

- 6d3ad421 test(docs): pin the doc claims the candidate 4 re-run retired
- baf83a91 docs: read capture fidelity from its sidecar, not expand
- dca27c9c docs(uat): set UAT-05's tiny budget where the daemon reads it
- 9dad9b07 docs(uat): record candidate 4's re-run and fix UAT-09 step 5
- b46b439e docs(mcp-tools): explain what recall's denied count withholds
- 9f9d5943 test(docs): record the green docs rows and the check runs

### Tests

- `go test -p 2 -count=1 -run '^(TestCloseoutStaleClaimsAreGone|TestFidelityIsReadFromTheCaptureRecord|TestHostBinaryDecodingIsDocumented|TestRecallDeniedCountIsExplained|TestUATNamedTestsExist|TestUATIntroDescribesTheCurrentRecord)$' -v ./test/docs/ (on base 08034a3f + test file)` — FAIL (RED as intended), runs/04-docs-claims-red.log
- `go test -p 2 -count=1 -run '^(TestCloseoutStaleClaimsAreGone|TestFidelityIsReadFromTheCaptureRecord|TestHostBinaryDecodingIsDocumented|TestRecallDeniedCountIsExplained|TestUATNamedTestsExist|TestUATIntroDescribesTheCurrentRecord)$' -v ./test/docs/` — PASS, runs/05-docs-claims-green.log
- `go test -p 2 -count=1 ./test/docs/` — ok
- `go run ./tools/devtool gen-mcp-docs --check; gen-config-docs --check; gen-command-docs --check` — all up to date, exit 0
- `go test -p 2 -count=1 ./tools/devtool/` — ok (94.9s), runs/06-devtool-and-lint.log
- `go test -p 2 -count=1 -run '^TestCapturePolicyProducesTheCompleteFidelitySet$' -v ./test/integration/ and -run '^(TestHookCapture_OversizeRetainsClassificationWithoutOpaqueBytes|TestHookCapture_BinaryPayloadIsClassifiedNotSilentlyRejected|TestHookCapture_ShortReadIsRecordedAsPartial)$' ./internal/cli/ and -run '^TestCaptureSidecar_DegradedCaptureIsRecordedAsSuch$' ./internal/store/` — PASS, runs/02-host-limited-fidelity-tests.log
- `go run ./tools/devtool fmt-check; go vet ./tools/devtool/ ./test/docs/ (Windows and GOOS=linux)` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, all eight PASS

### Open issues

- C4.9 probe: denied=5 with 0 hits looks like pathless Qompack self-records (refused for having no usable path provenance) filling recall's top k before filtering. The count matching k=5 is inferred, not proven. The fix belongs to the services seat (recall's k counts permitted hits; self-records rank after original captures). Not fixed here.
- UAT-05 re-run finding: a config reload that reports runtime.rehydrate.* as changed is not applied to the rehydration until a restart. That is the services seat's config-reload item; the new step 5 restarts the daemon, so it holds either way.
- Commit 6d3ad421 leaves test/docs red until b46b439e; squash on merge if every commit must be green.

## Independent review

### review:docs: needs-fixes

- **minor** `docs/cannot-do.md:224-226` — The same entry that item (1) fixed still claims retrieval returns the fidelity label. Its Limit bullet says a record whose fidelity is redacted/truncated/binary/partial/failure/unknown "is returned as it is, with that label". Two bullets later, the rewritten "What Qompack does instead" bullet says "no retrieval response carries it". The page now contradicts itself and D46, which is the exact class of stale claim F3 targets.
  - Evidence: cannot-do.md:224-226 reads: "A record whose fidelity is `redacted`, `truncated`, `binary`, `partial`, `failure` or `unknown` is returned as it is, with that label; no substitute is invented". The same section's edited bullet says: "It records the label on the capture's sidecar record ... no retrieval response carries it". mcp-tools.md:53 says: "No fidelity or coverage field." closeout_claims_test.go only pins the "It returns the label with the content" phrasing, so this sentence was missed.
  - Fix: Reword the Limit bullet to something like: "...is returned as it is (the label stays on its capture sidecar record); no substitute is invented and no gap is filled from elsewhere." Then add "is returned as it is, with that label" to closeoutStaleClaims.
- **minor** `docs/user-guide.md:547-550` — The new `--set` sentence says a daemon-applied setting "comes from the daemon's own configuration files". But a spawned daemon also inherits the spawning process's environment. buildSpawnEnv passes environ through and strips only QOMPACK_FAULT. The daemon then loads config with Getenv (internal/cli/daemon.go:114, 282), so a QOMPACK_* environment override set on the hook's environment does reach a daemon that the hook spawns. "configuration files" understates the daemon's config layers. A reader could conclude that environment overrides do not reach the daemon either.
  - Evidence: internal/daemon/spawn.go:375-380 builds `self daemon --project <root>` with `cmd.Env = buildSpawnEnv(environ, projectRoot)`. buildSpawnEnv (spawn.go:387+) copies every entry except QOMPACK_FAULT. internal/cli/daemon.go:114 calls config.Load with Getenv: env.Getenv.
  - Fix: Say that the setting comes from the daemon's own configuration, meaning its config files and the QOMPACK_* environment it was started with (a daemon a hook spawns inherits the hook's environment, but not its --set flags). Do not say "configuration files" alone. Optionally mention a QOMPACK_* override as an alternative in UAT-05 step 5, while keeping the config-file-plus-restart route as the primary one.
- **nit** `commit 6d3ad421 (test/docs/closeout_claims_test.go)` — This commit adds the six docs rows while the docs are still stale, so ./test/docs is red at 6d3ad421, baf83a91 and dca27c9c and turns green only at b46b439e. The implementer flagged this as deliberate. Even so, it breaks bisectability of a gating package on a branch that will be merged into develop.
  - Evidence: The implementer's note says: "commit 6d3ad421 adds the six rows while the docs are still stale, so test/docs is red at that commit alone and green from b46b439e on". The RED evidence is already preserved in runs/04-docs-claims-red.log.
  - Fix: On merge, squash 6d3ad421 into b46b439e (or reorder so the test commit comes last). The red log keeps the RED-before-fix proof.

## Fix seat (review resolution) — status `done`, head `91cf3d42`

### Root cause

Both review findings were leftover wording. The implementer's F3 fix rewrote one bullet of the cannot-do.md entry but missed the Limit bullet above it, which still said retrieval returns the fidelity label. The new --set sentence in user-guide.md said a daemon's settings come only from its config files, but buildSpawnEnv (internal/daemon/spawn.go) passes the spawning process's QOMPACK_* environment to the daemon, which loads it via Getenv (internal/cli/daemon.go:114).

### Summary

W15-docs fix seat, branch closeout/w15-docs in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w15-docs, now at 91cf3d42 (on top of the implementer's 6d3ad421..9f9d5943, base 08034a3f). Both reviewer findings were correct. I pinned each one first (RED), then fixed the docs (GREEN). Only docs/ and test/docs changed; no product code.

## Review resolution
1. **Minor, docs/cannot-do.md:224-226** ("is returned as it is, with that label"): CONFIRMED and FIXED. The Limit bullet contradicted the same entry's bullet ("no retrieval response carries it") and D46. It now says the record "is returned as it is (the label stays on its capture sidecar record); no substitute is invented and no gap is filled from elsewhere." Pinned in `closeoutStaleClaims` (test/docs/closeout_claims_test.go).
2. **Minor, docs/user-guide.md:547-550** ("comes from the daemon's own configuration files"): CONFIRMED and FIXED.
   - Evidence: internal/daemon/spawn.go `buildSpawnCommand`/`buildSpawnEnv` passes the parent environment through and strips only QOMPACK_FAULT. internal/cli/daemon.go:114 loads config with `Getenv: env.Getenv`. So a QOMPACK_* override in the hook's environment reaches a daemon the hook spawns.
   - The sentence now says the setting comes from the daemon's own configuration: its config files and the `QOMPACK_*` environment it was started with. A spawned daemon inherits the hook's environment but not its `--set` flags. A daemon that is already running keeps the environment it started with.
   - I first wrote "keeps the configuration it started with" and corrected it before committing. internal/daemon/reload.go reloads `config.json` on the idle tick and on `session.start`, so that claim was only true for the environment. I did not go further into reload behaviour, which belongs to the services seat.
   - As the reviewer suggested, UAT-05 step 5 now names `QOMPACK_RUNTIME__REHYDRATE__MINTOKENS` / `QOMPACK_RUNTIME__REHYDRATE__MAXTOKENS` as a second route. The names follow `envVarName` in internal/config/load.go. The config file plus a daemon restart stays the primary route.
   - Pinned in `closeoutStaleClaims`.

## Commands and results (this round)
- `go test -p 2 ./test/docs -run "^TestCloseoutStaleClaimsAreGone$" -count=1` with the new pins and the old docs: FAIL, exit 1 (both claims reported). Log: runs/07-review-pins-red.log.
- `go test -p 2 ./test/docs -count=1`: ok, exit 0.
- `go run ./tools/devtool gen-command-docs --check`, `gen-config-docs --check`, `gen-mcp-docs --check`: all up to date, exit 0.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./test/docs` on Windows and with GOOS=linux: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all 8 PASS.
- Log for the green runs: runs/08-review-fix-green.log.
- Logs are under plans/sdd/V6-closeout/w15-docs/runs/.

No wall-clock tests were involved. No load generators, Linux container or real sessions were used, and no background processes were left running.

## Criterion changes
None. Two stale-claim pins were added, which tightens the checks. Nothing was loosened.

### Commits

- 6d3ad421 test(docs): pin the doc claims the candidate 4 re-run retired
- baf83a91 docs: read capture fidelity from its sidecar, not expand
- dca27c9c docs(uat): set UAT-05's tiny budget where the daemon reads it
- 9dad9b07 docs(uat): record candidate 4's re-run and fix UAT-09 step 5
- b46b439e docs(mcp-tools): explain what recall's denied count withholds
- 9f9d5943 test(docs): record the green docs rows and the check runs
- 5f163dca test(docs): pin the two stale claims the review found
- 91cf3d42 docs: keep fidelity off retrieval and name the daemon's env layer

### Tests

- `go test -p 2 ./test/docs -run "^TestCloseoutStaleClaimsAreGone$" -count=1 (new pins, docs unfixed)` — FAIL exit 1 (expected RED: both stale claims reported)
- `go test -p 2 ./test/docs -count=1` — ok, exit 0
- `go run ./tools/devtool gen-command-docs --check; gen-config-docs --check; gen-mcp-docs --check` — all up to date, exit 0
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./test/docs (windows and GOOS=linux)` — exit 0 both
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS

### Criterion changes

- Added two entries to closeoutStaleClaims in test/docs/closeout_claims_test.go: cannot-do.md must not say "is returned as it is, with that label", and user-guide.md must not say "comes from the daemon's own configuration files". This tightens the checks; nothing was weakened.

### Open issues

- For the services seat, not fixed here: the daemon reloads project config.json on the idle tick and on session.start (internal/daemon/reload.go). The UAT-05 finding still reads "changed=[runtime.rehydrate.maxTokens runtime.rehydrate.minTokens] is not applied to its..." (docs/uat.md about line 705), which points at a config-reload defect in their scope. The UAT-05 step keeps its daemon-restart instruction, which works either way.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


