# V6 — Verification checkpoint: production readiness and UAT

**Type:** verification checkpoint (not a subplan). **Wave verified:** 5. **Branch:** `verify/v6`, cut from `develop`.

---

## 0. When this runs, and on what

Run this checkpoint **after both wave-5 branches have merged into `develop`**, in the merge order fixed by `plans/00-ARCHITECTURE.md` §9 and by the two subplan headers:

1. `feat/sp17-packaging-hardening-and-release` → `develop` (`--no-ff`)
2. `feat/sp18-documentation-and-uat` → `develop` (`--no-ff`)

SP-18 merges **after** SP-17 by construction — SP-18's own header states *"documentation of an artifact that does not yet ship is fiction"* — so a `develop` where SP-18 landed first is a merge-order violation and this checkpoint must stop and demand a re-merge.

**Before doing anything else, verify the starting state:**

```bash
git -C C:/Users/Quant/Documents/Programming/Projects/qompack checkout develop
git pull --ff-only 2>/dev/null || true
git log --oneline --merges -12
git log --oneline --merges | grep -n "sp17-packaging-hardening-and-release"
git log --oneline --merges | grep -n "sp18-documentation-and-uat"
```

Expected: both merge commits present; the SP-18 merge is **newer** than the SP-17 merge (lower line number in `git log` output). If either is missing, stop — V6 does not run on an incomplete wave.

**Then cut the verification branch:**

```bash
git checkout -b verify/v6 develop
git rev-parse --abbrev-ref HEAD          # must print verify/v6
```

All work in this checkpoint — every fix, every newly authored integration test, every regenerated artifact — lands on `verify/v6`. Nothing lands directly on `develop`. When the checkpoint is fully green, `verify/v6` merges back into `develop` with `--no-ff` and the tag `v0.5.1` is applied to `develop` (§9 tag scheme `v0.<wave>.<n>`).

**Purpose.** This is not a smoke test. Wave 5 is the last wave in the §14 wave map; `develop` at this point contains *every* subplan SP-01 … SP-18, and this checkpoint re-verifies **every functionality that exists in the codebase**, subplan by subplan, plus the cross-component seams that only became testable once packaging (SP-17) and the documentation-conformance test suite (SP-18) exist. Per 00-ARCHITECTURE §9, **no wave-6 branch is cut until this checkpoint is fully green.** (Wave 5 is the final wave in the current map, so in practice the gate is on the release merge `develop → main` and the `v1.0.0` tag that SP-17's release pipeline consumes — the rule is identical: nothing is cut from, or promoted out of, `develop` until V6 is green.)

**Environment assumptions.** Windows 11 dev machine; Git Bash and PowerShell both available; commands below are written for Git Bash unless a PowerShell variant is given. CI runs the three-OS matrix (ubuntu-latest, macos-latest, windows-latest). Where a command must run on all three, the checkpoint says so explicitly and the evidence is the CI job artifact, not a local run.

**Ground rules for the whole checkpoint:**

- Every claim in the completion report must be backed by pasted command output. "Looks fine" is not a result.
- Do not modify `Qompack.md`. `git diff develop -- Qompack.md` must be empty at the end.
- Do not modify `plans/00-ARCHITECTURE.md` §5 signatures. If one is genuinely wrong, that is an `arch/` amendment, not a V6 fix.
- Do not weaken a test, a threshold, or an assertion to make this checkpoint pass. A failing budget is a bug in the code or an honest re-measurement with sign-off, never a lowered number.

---

## 1. Cumulative functionality inventory

Every item below is a functionality that exists on `develop` at this point. Each carries the **exact command** and the **expected result**. Items are grouped by owning subplan. The implementer fans these groups out across parallel subagents (see §7 "Subagent strategy") and runs the §3 integration tests in the main session.

Unless stated otherwise, all commands run from the repository root `C:/Users/Quant/Documents/Programming/Projects/qompack` on branch `verify/v6`.

### 1.1 SP-01 — Foundation, toolchain, contracts

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.1.1 | Repository shape: `main` + `develop`, root commit contents | `git branch --list main develop` ; `git log --oneline --reverse \| head -1` ; `git show --stat $(git rev-list --max-parents=0 HEAD)` | Both branches exist; the root commit contains exactly `Qompack.md`, `plans/`, `.gitignore`, `LICENSE`; subject is `chore: initial commit — design document and build plans` |
| 1.1.2 | Module builds and vets | `go build ./...` ; `go vet ./...` | Both exit 0, no output |
| 1.1.3 | Full lint suite (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips) | `go run ./tools/devtool lint` | Exit 0, every sub-check reported clean |
| 1.1.4 | Formatting | `go run ./tools/devtool fmt-check` ; `gofumpt -l .` | No output from either |
| 1.1.5 | Binary dependency closure is closed (D-list of §2.5) | `go list -deps ./cmd/qompack \| grep -vE '^(internal/|[a-z/]+$)' ` and inspect | Only stdlib, `github.com/qompack/qompack/…`, `github.com/klauspost/compress/zstd`, `github.com/Microsoft/go-winio`. No `golang.org/x/tools`, no testify, no go-cmp, no rapid |
| 1.1.6 | Test-only dependency isolation | `go run ./tools/devtool lint` (the `testdeps` sub-check) | `testify`, `go-cmp`, `rapid` imported only from `_test.go` files |
| 1.1.7 | Import-graph layer DAG (§3.2 table) | `go run ./tools/devtool lint` (the `importgraph` sub-check) | Exit 0. Spot-verify by hand: `store` does not import `negknow`; `tokens` does not import `store`; `scheduler` imports foundation only; nothing imports `daemon`, `cli`, `commands`, `testutil` |
| 1.1.8 | Appendix C reproduced verbatim by `config.Defaults()` | `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` | PASS |
| 1.1.9 | Five-layer config precedence, deep per-leaf merge, env mapping, `--set` flags | `go test ./internal/config/...` | All PASS, incl. `QOMPACK_SCHEDULER__CACHE__READMULTIPLIER` mapping and project-file partial-override inheritance |
| 1.1.10 | Validation is fallback-not-crash, per leaf, with `Loud` + `state/config-violations.json` | `go test -run 'TestValidate\|TestLoad_InvalidLeafFallsBack' ./internal/config/` | PASS; no panic on any invalid leaf; unknown keys produce `Warning`, never `error` |
| 1.1.11 | Provenance and JSON Schema emission | `go run ./cmd/qompack config print --provenance` ; `go run ./cmd/qompack config schema \| head -20` | Every leaf annotated with `Origin` + `Location`; schema is valid JSON Schema |
| 1.1.12 | Append-only guard on `checkpoints/`, `pins/`, `sketches/tried.bloom` | `go test -run TestAppendOnlyGuard ./internal/paths/` | PASS — all five illegal write attempts (truncate, in-place rewrite, out-of-order seq, `O_TRUNC`, overwrite) fail with `core.ErrAppendOnly` |
| 1.1.13 | `paths.WriteAtomic`, `Norm`, `Key`, Windows long-path handling | `go test ./internal/paths/...` | PASS on Windows, including the >260-char path, the space-bearing path, and the `Foo.ts`/`foo.ts` case-collision fixture |
| 1.1.14 | `core` primitives: `Hash`, `HashBytes` domain separation, `ParseHash`, `Clock`, seven sentinels | `go test ./internal/core/...` | PASS; `HashBytes` is domain-separated (`sha256(domain \|\| 0x00 \|\| b)`) |
| 1.1.15 | `logging` with the `Loud` channel and rotation | `go test ./internal/logging/...` | PASS; a `Loud` call writes to the log **and** `.qompack/logs/LOUD.log` |
| 1.1.16 | `obs` log-bucket histograms, budget IDs B-A…B-F, `CheckBudgets` | `go test ./internal/obs/...` ; `go test -bench BenchmarkHistogram_Observe -benchtime 200000x ./internal/obs/` | PASS; `BenchmarkHistogram_Observe` < 100 ns/op |
| 1.1.17 | `hookio` optional-tolerant codecs, `Extra` preservation | `go test ./internal/hookio/...` ; `go test -fuzz FuzzReadEvent -fuzztime 60s ./internal/hookio/` | PASS; fuzz finds no crasher; missing fields never panic |
| 1.1.18 | Hand-rolled CLI dispatch, exit-code policy | `go test -run TestDispatch_HookAlwaysExitsZero ./internal/cli/` | PASS on all 30 fault-injection combinations |
| 1.1.19 | Six no-op-safe hook entry points always exit 0, against the **real** binary | `go test -run TestE2E_AllSixHooksExitZero ./test/e2e/` | PASS |
| 1.1.20 | Plugin manifest generated from `internal/pluginmanifest`, no drift | `go run ./tools/devtool plugin-validate` ; `git diff --exit-code -- plugin/` | Exit 0; clean diff; 7 commands and 8 MCP tools asserted present |
| 1.1.21 | Config docs generated, never stale | `go run ./tools/devtool gen-config-docs --check` | Exit 0 |
| 1.1.22 | Six-target cross-build | `go run ./tools/devtool build-all` | All six §2.6 targets produced (linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/{amd64,arm64}) |
| 1.1.23 | Contract-monitor default posture on a fresh build | `go test -run TestGuard_FreshBuildReportsModeFull ./internal/contract/` | PASS — `ModeFull`; every assertion with a shipped producer reports a real observation |
| 1.1.24 | The four closing-note build-order guards | `go test -run 'TestGuard_' ./...` | All PASS, incl. `TestGuard_NoNetworkImports` and `TestGuard_WriteSetConfinedToQompack` |
| 1.1.25 | Conformance-suite infrastructure: 22 `<pkg>test` suites, zero remaining W-1 skips anywhere | `grep -rn "t.Skip" --include='*_test.go' internal/ \| grep -v '_test.go:.*short'` ; `go test ./...` | **Zero** `t.Skip` carrying `behaviour: implementation is a stub (Rule W-1)` or `contract fixture not yet recorded (Rule W-2)` anywhere in the tree. Every suite runs against a real implementation |
| 1.1.26 | `internal/testutil` fixture: temp project, `FakeClock`, golden helpers, Windows specifics | `go test ./internal/testutil/...` | PASS |
| 1.1.27 | Baseline benchmarks within their SP-01 budgets | `go test -bench 'BenchmarkConfigLoad_ColdNoFiles\|BenchmarkHookNoop_InProcess\|BenchmarkPathsWriteAtomic_4KB' ./...` | < 2 ms/op, < 3 ms/op, < 2 ms/op respectively |
| 1.1.28 | `Qompack.md` unmodified since the root commit | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | Empty |

### 1.2 SP-02 — Replay harness, Belady OPT, Phase-0 baseline (L7)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.2.1 | `internal/eval` full suite | `go test -race ./internal/eval/...` | PASS |
| 1.2.2 | Conformance suite live | `grep -rn "t.Skip" internal/eval/evaltest/` ; `go test -run RunEvalSuite ./internal/eval/evaltest/` | No skips; suite passes against `eval.New` |
| 1.2.3 | 24-session deterministic synthetic corpus present and hash-stable | `ls testdata/sessions/synthetic/*.json \| wc -l` ; `go test -run TestCorpusIntegrity ./internal/eval/` | 24 files; `CORPUS.json` hashes match; regeneration byte-identical |
| 1.2.4 | Phase-0 baseline number committed, reproducible, honestly tiered | `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline /tmp/p0a.json` ; repeat to `/tmp/p0b.json` ; `cmp /tmp/p0a.json /tmp/p0b.json` ; `grep corpusTier testdata/baseline/phase0.json` | Byte-identical between runs; `"corpusTier": "synthetic"`; `policies.stock.fraction_of_opt` present over 24 sessions (≥ `eval.minSessions` = 20) |
| 1.2.5 | Belady OPT ceiling and floor bracket every policy | `go test -run 'TestOracleScoresOne\|TestNullScoresZero\|TestStockAboveNull' ./internal/eval/` | `oracle` = 1.0 on every session; `null` = 0.0 on every session; `stock` strictly above `null` on all 24 |
| 1.2.6 | Five §4.2 divergence metrics | `go test -run TestDivergence ./internal/eval/` | PASS: file-set Jaccard, tool edit distance, first-divergence turn, same-decision, redundant work |
| 1.2.7 | All ten §11.2 secondary metrics incl. the v1.2 latency trio | `go test -run TestMetricsOf ./internal/eval/` | All ten rows produced; latency values tagged `"latency": "modelled"` |
| 1.2.8 | Breakpoint OPT reported and labelled not-plugin-actionable (§5.6) | `go run ./test/replay --corpus testdata/sessions/synthetic --json /tmp/rep.json` ; `grep -i NotPluginActionable /tmp/rep.json` | Note present in every breakpoint result |
| 1.2.9 | `replay-gate`: 2% rule, sign-off trailer, phase assertions, growth guardrail, max-wall | `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop` | Exit 0; report lists every merged phase's exit assertion as passing |
| 1.2.10 | Every gate failure mode has a test | `go test -run 'TestGate_' ./test/replay/...` | PASS: 2% boundary both sides, trailer accept/reject, session-count, reproducibility, bloom FP ceiling, corpus staleness, growth-inconclusive, max-wall |
| 1.2.11 | Redacting importer for recorded transcripts | `go test -run TestRedact ./internal/eval/` ; `go test -fuzz FuzzRedact -fuzztime 60s ./internal/eval/` | Idempotent; no fixture secret survives; fuzz clean |
| 1.2.12 | Eval benchmarks within budget | `go test -bench 'E-' ./internal/eval/` (or the named benchmarks) | E-1 < 120 s, E-2 ≤ 250 ms/op, E-3 ≤ 50 ms/op, E-4 ≤ 20 ms/op, E-5 ≤ 15 ms/op |
| 1.2.13 | `internal/eval` imports foundation only | `go run ./tools/devtool lint` (importgraph) | `eval` imports no `internal/` package outside `{core, paths, config, logging, obs}` |
| 1.2.14 | Coverage floor | `go run ./tools/devtool cover` | `internal/eval` ≥ 85% |

### 1.3 SP-03 — Sketch library (L1)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.3.1 | All five sketches implement `Sketch`; §5.7 signatures exact | `go build ./internal/sketch/` ; `go test ./internal/sketch/...` | PASS |
| 1.3.2 | Appendix A Bloom sizing | `go test -run TestBloom_AppendixASizing ./internal/sketch/` | `mRaw = 95_851`, `mBits = 95_872`, `k = 7`, body 11 984 bytes |
| 1.3.3 | Appendix A Count-Min sizing | `go test -run TestCMS_AppendixASizing ./internal/sketch/` | `width = 2719`, `depth = 5`, body 54 380 bytes |
| 1.3.4 | HyperLogLog sizing and error | `go test -run TestHLL_AppendixSizing ./internal/sketch/` | 2 048 registers, 2 102-byte frame, 2.3% standard error |
| 1.3.5 | Measured Bloom FP rate matches the 1% claim | `go test -run TestBloom_EstimatedFPRateMatchesEmpirical ./internal/sketch/` | Empirical FP ∈ [0.008, 0.013] at capacity |
| 1.3.6 | Resize policy and saturation alarm (§11.4) | `go test -run 'TestBloom_ResizeTarget\|TestBloom_Saturated' ./internal/sketch/` | `(2×capacity, fpRate, true)` above 0.5 fill; `(capacity, fpRate, false)` below; `Saturated()` fires at `EstimatedFPRate ≥ 0.10` |
| 1.3.7 | `RebuildBloom` from an arbitrary `iter.Seq[[]byte]` at a different capacity | `go test -run TestRebuildBloom ./internal/sketch/` | Equivalent filter rebuilt; ≤ 15 ms for 5 000 keys |
| 1.3.8 | `MergeFrom` / `Scale` on CMS, HLL, Misra-Gries (O4 primitives) | `go test -run 'TestCMS_Merge\|TestHLL_Merge\|TestMG_Merge\|TestCMS_Scale' ./internal/sketch/` | Correct; `ErrShapeMismatch` on shape mismatch and on `nil` |
| 1.3.9 | Misra-Gries `Top(n)` has no false positives by construction | `go test -run TestMG_ ./internal/sketch/` | PASS |
| 1.3.10 | MinHash signature, Jaccard, near-dup threshold | `go test -run TestMinHash ./internal/sketch/` | PASS incl. the "same test suite, one new failure" fixture |
| 1.3.11 | `tried.bloom` generational replacement stays inside the append-only invariant | `go test -run 'TestSave_ErrGenerational\|TestReplaceGenerational' ./internal/sketch/` | `Save` returns `ErrGenerational` for any `tried.bloom` path; `ReplaceGenerational` keeps exactly one `.bak`; rolls back on write failure |
| 1.3.12 | Corruption detection is loud and typed | `go test -run TestLoadWithLog_Corrupt ./internal/sketch/` | Exactly one `Loud`; error satisfies both `errors.Is(err, core.ErrNotFound)` and `errors.Is(err, ErrCorrupt)` |
| 1.3.13 | Frozen v1 wire format still decodes; param names lowercase and ascending | `go test -run 'TestGolden_V1StillDecodes\|TestParamNames' ./internal/sketch/` | PASS; Bloom writes `fprate` (not `fpRate`); names strictly ascending in every frame |
| 1.3.14 | Frame-size guards | `go test -run 'TestMaxBloomBits\|TestMaxCMSCells\|TestEncodeHeader_ErrTooLarge' ./internal/sketch/` | Largest legal body ≤ 32 MiB; `ErrTooLarge` rather than an oversized frame |
| 1.3.15 | Five fuzz targets clean | `go test -fuzz FuzzSketchUnmarshal -fuzztime 60s ./internal/sketch/` (repeat per target) | No panic, no non-sentinel error |
| 1.3.16 | Hot-path sketch update budget with zero allocations | `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` ; `go test -run TestL0SketchUpdate_ZeroAlloc ./internal/sketch/` | ≤ 5 µs/op, 0 allocs/op |
| 1.3.17 | Foundation-only imports; coverage floor | `go test -run TestImports_FoundationOnly ./internal/sketch/` ; `go run ./tools/devtool cover` | Imports only `core`, `paths`, `logging`; coverage ≥ 90% |

### 1.4 SP-04 — FastCDC chunking, canonicalizers, symbols (L1)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.4.1 | FastCDC `Split` / `SplitStream` at 1 KB / 4 KB / 16 KB from config | `go test ./internal/chunk/...` | PASS; `Min ≤ len ≤ Max` for every chunk except the last |
| 1.4.2 | Boundary stability under insertion | `go test -run 'TestSplit_BoundaryStability' -rapid.checks=1000 ./internal/chunk/` | Inserting bytes at offset *k* perturbs at most 2 chunks after the insertion point |
| 1.4.3 | Cross-platform determinism (gear table + boundary goldens) | `go test -run 'TestGearTableGolden\|TestSplit_GoldenBoundaries' ./internal/chunk/` **and** the same tests in CI's `test` matrix on ubuntu/macos/windows | Identical goldens on all three OSes. A per-platform golden is a failure, not a fix |
| 1.4.4 | Domain-separated Merkle `RootHash` | `go test -run TestRootHash ./internal/chunk/` | `HashBytes("qompack.root.v1", concat(chunk hashes))` |
| 1.4.5 | Canonicalizer registry: 14 canonicalizers, §5.6 registration order | `go test -run 'TestRegistry_Order\|TestNames' ./internal/canon/` | `Default` registers crlf, ansi, timestamps, durations, pids, addresses, tmpPaths, then bash, testrunner, grep, glob, fileread, webfetch, git — in that order |
| 1.4.6 | Idempotence: `Canonicalize(Canonicalize(x)) == Canonicalize(x)` | `go test -run 'Idempot' ./internal/canon/` | PASS for every canonicalizer, each with its own named test |
| 1.4.7 | Byte-exact inverse: `Restore(Canonicalize(x).Canonical, deltas) == x` | `go test -run 'TestRestore' ./internal/canon/` ; `go test -fuzz FuzzRestore -fuzztime 120s ./internal/canon/` | PASS; fuzz clean |
| 1.4.8 | No canonicalizer grows its input | `go test -run 'NonGrowth' ./internal/canon/` | PASS for every canonicalizer |
| 1.4.9 | Every rule emits a `Match` with its assigned `Class`; nothing silently dropped by the `Strip` gate | `go test -run TestMatcherClassAssigned ./internal/canon/` | PASS over the whole `testdata/corpora/toolout/` corpus |
| 1.4.10 | MinHash signature attached to every `Registry.Run` result; delta-vs-full decision surfaced | `go test -run 'TestRun_Signature\|TestRun_NearDup' ./internal/canon/` | PASS |
| 1.4.11 | With-vs-without-canonicalization dedup measurement (the O2 justification) | `go test ./test/dedup/...` ; `cat testdata/canon-dedup-report.json` | Report reproduced byte-for-byte; `testrunner` group `gain ≥ 1.25`; overall `gain ≥ 1.0` |
| 1.4.12 | Symbol extraction: `Extract`, `Enclosing`, `References` | `go test ./internal/symbols/...` | PASS; `Kind ∈ {func,type,class,const,var}` in every dialect |
| 1.4.13 | `Enclosing` is the minimal-sufficient-span resolver (§8.7) | `go test -run TestEnclosing ./internal/symbols/` | Returns the smallest symbol span containing the offset |
| 1.4.14 | Chunk/canon/symbol benchmarks within budget | `go test -bench . ./internal/chunk/ ./internal/canon/ ./internal/symbols/` | `Split_100KB` < 800 µs (the §8.1 "well under 1 ms" claim); `GearScan_1MiB` ≥ 400 MB/s; `Split_1MiB` ≥ 120 MB/s, ≤ 2 allocs; `SplitStream_4MiB` ≤ 40 ms; `RootHash_1000Chunks` < 40 µs; `Run_Bash100KB` < 3 ms; `Run_GoTest` < 1 ms; `Restore_100KB` < 1 ms; `Extract_100KB` < 2 ms; `Enclosing_100KB` < 2 ms; `References_100KB_50Names` < 1 ms |
| 1.4.15 | Fuzz targets clean | `go test -fuzz FuzzSplit -fuzztime 120s ./internal/chunk/` ; `FuzzCanonicalizeRun`, `FuzzRestore`, `FuzzExtract` likewise | Zero crashers |
| 1.4.16 | Import discipline and coverage | `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | `chunk`/`symbols` foundation-only, `canon` foundation + `sketch`; coverage `chunk` ≥ 90%, `canon` ≥ 90%, `symbols` ≥ 75% |
| 1.4.17 | Corpus hygiene | `grep -rniE 'ghp_|AKIA|BEGIN [A-Z ]*PRIVATE KEY|Bearer [A-Za-z0-9]' testdata/corpora/toolout/` | No matches; `.gitattributes` carries `testdata/corpora/** -text` |

### 1.5 SP-05 — Daemon, IPC, hot path, contract monitor (L0 substrate)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.5.1 | IPC address resolution per platform | `go test ./internal/ipc/...` | Windows named pipe `\\.\pipe\qompack.<hash12>` ACL'd to the current user SID; POSIX Unix socket with XDG→TempDir order, dir `0700`, socket `0600`, `sun_path` ≤ 100-byte fallback |
| 1.5.2 | NDJSON framing, 1 MiB line limit, `\x06`/`\x15` ACK/NAK | `go test -run 'TestFraming' ./internal/ipc/` ; `go test -fuzz FuzzFraming -fuzztime 60s ./internal/ipc/` | PASS; fuzz clean |
| 1.5.3 | `ipc.Client.Send` never propagates an error; spools on any failure | `go test -run 'TestClient_SpoolsOnFailure\|TestClient_NeverErrors' ./internal/ipc/` | Returns `Response{OK:false}, nil` on refused/timeout; event lands in `.qompack/spool/client-<pid>.ndjson` |
| 1.5.4 | `ipctest` conformance suite live | `grep -rn "t.Skip" internal/ipc/ipctest/` ; `go test ./internal/ipc/ipctest/` | Zero skips; PASS |
| 1.5.5 | Daemon singleton lock, stale-lock reclamation, 90 s staleness window / 30 s heartbeat | `go test -run 'TestLock' ./internal/daemon/` | PASS; a lock whose pid is dead is reclaimed |
| 1.5.6 | Lazy detached spawn (hidden window on Windows, `Setsid` on POSIX) | `go test -run TestLazySpawn ./internal/daemon/` ; `go test -run TestE2E_DaemonAutostart ./test/e2e/` | Daemon starts; spawning client does not wait, spools and exits 0 |
| 1.5.7 | WAL-backed ingest queue; ACK after WAL append, before indexing | `go test -run 'TestIngest\|TestWAL' ./internal/daemon/` | PASS; crash mid-index loses no event |
| 1.5.8 | `Drain` is idempotent over spool + WAL | `go test -run TestDrain ./internal/daemon/` | Replaying twice produces the same store state |
| 1.5.9 | Idle controller, registration, `RunOnce` budget, idle exit at 1800 s | `go test -run 'TestIdle' ./internal/daemon/` | PASS; idle-exits with zero live sessions |
| 1.5.10 | Config hot reload on mtime change; `store.chunk.*` deferred to next `SessionStart` and logged loudly | `go test -run TestConfigReload ./internal/daemon/` | PASS |
| 1.5.11 | Extension seams unused-but-present tolerance | `go test -run TestServicesAllNil ./internal/daemon/` | Every op answered without panic with a fully nil `Services` |
| 1.5.12 | Hot-path budget B-A on the real binary, three platforms | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json /tmp/ba.json` locally; CI `bench-gate` artifact for macos/ubuntu | **B-A p99 < 15 ms**, **B-B p99 < 2 ms**; `spawn_floor_ms` and `b_a_method` present; B-D reported, never gated |
| 1.5.13 | Hot-path overrun degradation is an observable state transition | `go test -run 'TestBreachDetectorTransitionsAfterThreeWindows\|TestBreachDetectorRevertsAfterThreeCleanWindows\|TestHotModeTransitionWritesStateAndNAKs' ./internal/daemon/` | `sync`→`spool` after 3 consecutive breach windows; reverts after 3 clean windows; visible in `state.bin`, the NAK frame, a WARN log, and the `status` payload |
| 1.5.14 | Nine contract assertions run at every `SessionStart` | `go test ./internal/contract/...` | All nine IDs of §5.19 registered and evaluated |
| 1.5.15 | Producer-presence rule and its declared split | `go test -run 'TestFreshBuildReportsModeFull\|TestDeclaredProducerSetMatchesArchitecture' ./internal/contract/` | On a **post-wave-5** `develop` **every** assertion has a shipped producer — `mcp.server_registered` (SP-13), `precompact.*` (SP-10), `hook.additional_context_delivered` (SP-11) must now report real observations, **not** `not-yet-implemented`. If any still reports `not-yet-implemented`, that is a V6 failure |
| 1.5.16 | Degradation semantics | `go test -run 'TestDegradedPassiveSuppressesActingPaths\|TestDegradedPassiveStillRecords\|TestModeOffSkipsIngest' ./internal/daemon/ ./internal/contract/` | In `degraded-passive`: L0/L1 keep recording; no `additionalContext`, no `customInstructions`, no scheduler-initiated checkpoints, no drop report; MCP retrieval still answers |
| 1.5.17 | Restoration after two clean runs | `go test -run TestMonitorRestore ./internal/contract/` | `Monitor.Restore` called, logged as loudly as the degradation |
| 1.5.18 | Session-start marker produced only by terminal hooks | `go test -run TestMarkerIsWrittenByFlushAndCheckpointOnly ./internal/daemon/` | PASS |
| 1.5.19 | Every hook subcommand exits 0 under every injected fault | `go test -run 'TestHooksExitZeroUnderFaults\|TestFaultSitesInertWhenUnset\|TestSelfTestIsTheOnlyNonZeroExit' ./internal/cli/ ./test/e2e/` | All 66 combinations exit 0; `qompack self-test` is the only subcommand permitted a non-zero exit |
| 1.5.20 | Security posture of the transport | CI `security` job; locally `go run ./tools/devtool security-audit` | Zero non-test imports of `net/http`, `net/url`, `crypto/tls`; `net` only in `internal/ipc` and only `unix`; `os/exec` only in `internal/daemon`, `internal/cli`, `tools/` |
| 1.5.21 | Coverage floors | `go run ./tools/devtool cover` | `ipc`, `daemon`, `contract` and SP-05's `cli` files ≥ 75% |

### 1.6 SP-06 — Content-addressed store, redaction, tokens (L1)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.6.1 | Object layout: `objects/ab/cd/<sha256>.zst`, two-level fanout, zstd | `go test -run TestPutBytes_FanoutLayout ./internal/store/` | PASS |
| 1.6.2 | Global dedup across the project | `go test -run TestPutBytes_GlobalDedup ./internal/store/` | Four reads of one file cost one chunk set; `PutResult.Reused` > 0 |
| 1.6.3 | Canonicalize-before-chunk ordering | `go test -run TestPutBytes_CanonicalizeBeforeChunk ./internal/store/` | PASS |
| 1.6.4 | Redaction at the single choke point, **before** canonicalization and chunking | `go test -run TestPutBytes_RedactionBeforeChunking ./internal/store/` ; `go test -run TestE2E_SecretNeverLandsInObjects ./test/e2e/` | No built-in secret literal appears in any object under `objects/` |
| 1.6.5 | Ten ordered redaction rules, idempotent, bounded growth | `go test ./internal/redact/...` ; `go test -fuzz FuzzRedactIdempotent -fuzztime 60s ./internal/redact/` | `Redact(Redact(x)) == Redact(x)`; fuzz clean; `sk-ant-` split ahead of generic `sk-` |
| 1.6.6 | `tool_use` index, args digest and ≤120-char preview | `go test -run 'TestRecordToolUse\|TestToolUse' ./internal/store/` | PASS; append-only JSONL |
| 1.6.7 | Supersession marking | `go test -run TestMarkSuperseded ./internal/store/` | Argument order is older-first; `Status`/`SupersededBy` set |
| 1.6.8 | File version history and point-in-time lookup | `go test -run 'TestAppendFileVersion_History\|TestFileAt' ./internal/store/` | PASS |
| 1.6.9 | `ChangedSince([]core.Dep)` — the staleness primitive | `go test -run TestChangedSince ./internal/store/` | All five `ChangedSince` cases PASS; signature takes `[]core.Dep`, never a `negknow` type |
| 1.6.10 | `Search` backing `recall` | `go test -run TestSearch ./internal/store/` | Text, path, symbol and tool queries return scored `Hit`s |
| 1.6.11 | `OpenSpan` minimal-span reads on chunk boundaries | `go test -run TestOpenSpan_Boundaries ./internal/store/` | PASS |
| 1.6.12 | Segment log with the encoded-once DPI guard | `go test -run 'TestSegment_MarkEncodedRefusesDifferentSeq\|TestSegment_MarkEncodedBatchIsAllOrNothing' ./internal/store/` | Re-encoding a segment into a different `CheckpointSeq` returns `core.ErrAlreadyEncoded`; batch is all-or-nothing; same-seq is idempotent |
| 1.6.13 | `Frontier` / `Unencoded` queries | `go test -run 'TestSegment_Frontier\|TestSegment_Unencoded' ./internal/store/` | PASS |
| 1.6.14 | Mark-and-sweep GC, deadline-bounded, resumable, `max(30 days, 10 sessions)` retention | `go test -run 'TestGC' ./internal/store/` | `TestGC_RetentionIsWhicheverIsLonger` PASS; `GCReport.Truncated` set when the deadline is hit |
| 1.6.15 | `Stats.DedupRatio` responds to the canonicalization toggle | `go test -run TestStats_DedupRatio ./internal/store/` | Toggling `store.canonicalize.enabled` changes the number |
| 1.6.16 | Phase-1 dedup ratio on read-heavy sessions | `go test -run TestPhase1ExitCriterion_ReadHeavy ./internal/store/` | `Stats.DedupRatio ≥ 4.0` |
| 1.6.17 | Sublinear store growth | `go test -run TestStats_SublinearGrowth ./internal/store/` | PASS |
| 1.6.18 | Exact chunk-level token accounting; real image/PDF sizing; per-project calibration | `go test ./internal/tokens/...` | All 22 `tokens` tests PASS; `EstimateRoot` takes `[]core.ChunkRef`; factor clamped to `[0.6, 1.6]` and persisted in `~/.qompack/calibration.json` |
| 1.6.19 | Store benchmarks within budget | `go test -bench . ./internal/store/` | `BenchmarkPutBytes_100KB_Cold` ≤ 3 ms (against B-C `l0_process` p99 < 50 ms); every row of the SP-06 budget table met |
| 1.6.20 | Conformance suites live | `grep -rn "t.Skip" internal/store/storetest/ internal/redact/redacttest/ internal/tokens/tokenstest/` | Zero |
| 1.6.21 | Import discipline and coverage | `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | `store` imports only `chunk canon sketch symbols redact tokens` + foundation; `tokens`, `redact` foundation-only; coverage `store` ≥ 90%, `redact` ≥ 90%, `tokens` ≥ 90% |

### 1.7 SP-07 — Dependence DAG and slicing (L1/L2)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.7.1 | Nine node kinds, eight edge kinds, stable `NodeID` scheme | `go test ./internal/dag/...` | Every kind constructible, serializable, and exercised by at least one test |
| 1.7.2 | Append-only `dag/deps.jsonl` round-trips byte-exactly | `go test -run 'TestLog_RoundTrip' ./internal/dag/` | PASS; written only via `paths.AppendOnly` |
| 1.7.3 | Torn tail and corrupt line tolerated and surfaced | `go test -run 'TestLoad_TornTail\|TestLoad_CorruptLine' ./internal/dag/` | Both load; `TruncatedTail` and `LoadErrors` set; exactly one `Loud` |
| 1.7.4 | Idle-only `Compact` drops tombstoned nodes | `go test -run TestCompact ./internal/dag/` | Generation bumped; slice answers preserved on a tombstone-free graph; no-op below 25% waste |
| 1.7.5 | `BackwardSlice` / `ForwardSlice` return **scores**, never keep/drop | `go test -run 'TestBackwardSlice\|TestForwardSlice\|TestNoBooleanKeepAPI' ./internal/dag/` | `Slice.Scores` is `map[NodeID]float32`; `TestNoBooleanKeepAPI` finds no exported function returning a keep-set, a drop list, or `map[NodeID]bool`; the `NO SELECTION AUTHORITY` note is still in `doc.go` |
| 1.7.6 | Thin slicing is the default, tied to Appendix C | `go test -run TestDefaultSliceOptions ./internal/dag/` | `DefaultSliceOptions(config.Defaults()).Thin == true` |
| 1.7.7 | Measured thin-vs-full tradeoff | `go test -run TestThinVsFullComparison ./internal/dag/` ; `cat testdata/golden/contracts/dag/thin-vs-full.json` | Mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85` across eight seeds |
| 1.7.8 | `CrossingEdges(pos)` = `segment_coupling(p)` | `go test -run TestCrossingEdges -rapid.checks=1000 ./internal/dag/` ; `go test -bench BenchmarkCrossingEdges ./internal/dag/` | Matches brute force on every rapid case and all twelve golden positions; < 5 µs on 15 000 edges |
| 1.7.9 | `NodesAfter(pos)` totally ordered, live-only, freshly allocated | `go test -run TestNodesAfter ./internal/dag/` | Property-tested against a linear filter |
| 1.7.10 | Slice latency budget (§6.4 "sub-millisecond") | `go test -bench 'BenchmarkBackwardSlice5000\|BenchmarkForwardSlice5000' ./internal/dag/` ; `go test -run TestSliceLatencyBudget ./internal/dag/` | Both **< 1 ms/op** on 5 000 nodes / ~15 000 edges; the test fails the build if not |
| 1.7.11 | Builder output is acyclic | `go test -run TestBuilderOutputIsAcyclic ./internal/dag/` | No cycle over 200 built tool uses incl. parallel siblings |
| 1.7.12 | Concurrency safety, no lock upgrades | `go test -race -run TestConcurrent ./internal/dag/` | PASS |
| 1.7.13 | Golden fixtures consumable by SP-08/09/12 | `ls testdata/golden/contracts/dag/` ; `go test -run TestGolden ./internal/dag/` | `graph-basic.jsonl`, `nodeid.json`, `slice-backward.json`, `crossing.json`, `thin-vs-full.json` present and reproduced |
| 1.7.14 | Conformance suite live; imports; coverage | `grep -rn "t.Skip" internal/dag/dagtest/` ; `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | Zero skips; `dag` imports only `core paths config logging`; coverage ≥ 85% |

### 1.8 SP-08 — Observer L0

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.8.1 | `OnToolUse` full 14-step pipeline | `go test ./internal/observer/...` | PASS |
| 1.8.2 | Addressable tombstone (G3.2) | `go test -run 'TestTombstone' ./internal/observer/` | Golden-locked format `[cleared: sha256:… · <size> · <Tool> <subject> · re-expandable]`; `BenchmarkTombstone` < 2 µs/op |
| 1.8.3 | Supersession detection marks the earlier read `SUPERSEDED` | `go test -run TestSupersede ./internal/observer/` | PASS; superseded reads become first eviction candidates |
| 1.8.4 | DAG edges recorded per §8.1 item 4 | `go test -run TestGraphEdges ./internal/observer/` | `tool_use → tool_result → assistant_turn → next_tool_use` plus shared file/symbol edges |
| 1.8.5 | Count-Min and HLL fed; **Bloom never fed** | `go test -run 'TestSketches\|TestObserverSourceHasNoBloomReference\|TestOnSessionEnd_NeverWritesTriedBloom' ./internal/observer/` ; `grep -rn "Bloom" internal/observer/ --include='*.go' \| grep -v _test.go` | CMS/HLL updated; **zero** occurrences of `Bloom` in non-test observer source; `.qompack/sketches/tried.bloom` never created by any observer path |
| 1.8.6 | Verbatim, immutable user-prompt capture (G2.3) | `go test -run 'TestOnUserPrompt_NeverRegenerated' ./internal/observer/` ; `go test -run TestE2E_VerbatimPromptSurvivesRestart ./test/e2e/` | Content-addressed, indexed, never rewritten; survives a daemon restart byte-identically |
| 1.8.7 | Subagent capture with tool-result hashes (G10.1) | `go test -run TestOnStop_RetrievalPathG10_1 ./internal/observer/` | Summary **and** hashes round-trip out of a real store |
| 1.8.8 | Task-boundary signals (G1.5) | `go test -run 'TestExtractSignals\|TestWireObserver' ./internal/observer/` | Todo completion, passing test run, git commit detected and delivered to the scheduler seam |
| 1.8.9 | `SessionStart` startup/resume branch; `SessionEnd` flush + GC + session index | `go test -run 'TestOnSessionStart\|TestOnSessionEnd' ./internal/observer/` | Ordering matches §7.3 and §8.2 |
| 1.8.10 | Phase-1 exit criterion, dedup half | `go test -run TestPhase1_DedupRatioReadHeavy ./test/e2e/` | `store.Stats().DedupRatio ≥ 4.0` on `eval.Synthesize(0x51080001, readHeavy)` |
| 1.8.11 | Canonicalization gap on test-output-heavy sessions | `go test -run TestPhase1_CanonicalizationGapOnTestOutput ./test/e2e/` | `ratioOn ≥ ratioOff × 1.25` on `eval.Synthesize(0x51080002, testOutputHeavy)` |
| 1.8.12 | Sublinear growth with the observer wired | `go test -run TestPhase1_StoreGrowthSublinear ./test/e2e/` | PASS |
| 1.8.13 | Observer-side latency | `go test -bench 'BenchmarkOnToolUse_FileRead64KB\|BenchmarkOnToolUse_TestOutput256KB' ./internal/observer/` | Both within **B-C p99 < 50 ms** |
| 1.8.14 | Observer conformance suite | `go test ./internal/observer/observertest/` ; `grep -rn "t.Skip" internal/observer/observertest/` | `RunObserverSuite` passes; zero skips |
| 1.8.15 | Import discipline and coverage | `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | Imports exactly `core paths config logging obs hookio store canon sketch dag grammar tokens`; no `scheduler`, `checkpoint`, `symbols`, `contract`, `negknow`, `analyzer`, `daemon`; coverage ≥ 75% |

### 1.9 SP-09 — Negative knowledge (L2)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.9.1 | Four-field canonical descriptor | `go test -run TestCanonicalize ./internal/negknow/` | `(NormalizedPath, Symbol, ApproachClass, ReasonHash)`; `Key()` = `HashBytes("qompack.neg.v1", …)` |
| 1.9.2 | Append-only `records/eliminations.jsonl` as source of truth | `go test -run TestRecord ./internal/negknow/` | Written via `paths.AppendOnly`; bloom updated as a **cache** |
| 1.9.3 | Three-way `already_tried` answer | `go test -run 'TestQuery_Absent\|TestQuery_Active\|TestQuery_Stale' ./internal/negknow/` | `AnswerAbsent` / `AnswerActive` / `AnswerStale` with the §8.3 note verbatim |
| 1.9.4 | Stale note is byte-identical to §8.3 item 4 | `go test -run TestStaleNoteVerbatim ./internal/negknow/` | Compared against a literal, em dash included |
| 1.9.5 | Bloom-only hits never masquerade as records | `go test -run 'TestQuery_BloomOnly\|TestBloomLoadFailure_NoRecords_NeverFalsePositive' ./internal/negknow/` | No path returns `AnswerActive`/`AnswerStale` without a materialized record; bloom load failure ⇒ `absent` for everything |
| 1.9.6 | `RefreshStaleness` via `store.ChangedSince` | `go test -run TestRefreshStaleness ./internal/negknow/` | Dependency hash change flips `status` to `stale` with `stale_since` and `stale_because` |
| 1.9.7 | `RebuildBloom` from **active records only** | `go test -run 'TestRebuildBloom_Resizes\|TestRebuildBloom_NeverFromCheckpoint' ./internal/negknow/` | `sketch.RebuildBloom` has exactly one call site in `internal/`, fed only by `visibleActive()`; at 8 000 active records `FillRatio ≤ 0.5` and `EstFPRate < 0.02` |
| 1.9.8 | `scope: session \| project` semantics | `go test -run TestScope ./internal/negknow/` | Session-scoped die with the session; project-scoped persist |
| 1.9.9 | Heuristic `Detector` over the DAG (source #3) | `go test -run TestDetector ./internal/negknow/` | test-fail → revert → different-approach pattern produces records |
| 1.9.10 | `Health()` surfaces fill ratio and estimated FP rate (§11.4) | `go test -run TestHealth ./internal/negknow/` | `Records`, `Active`, `Stale`, `FillRatio`, `EstFPRate`, `NeedsResize` populated |
| 1.9.11 | Phase-2 exit criterion in replay | `go test -run TestPhase2ExitCriterion ./test/replay/` | `stockRepeats > 0`; `negknowRepeats ≤ 0.75 × stockRepeats`; strictly better on ≥ 8 individual sessions; `staleBlocks == 0` on every session with a non-empty `DependencyChangeAt` |
| 1.9.12 | Performance budgets | `go test -run 'TestBudget_' ./internal/negknow/` | Every row asserted |
| 1.9.13 | Conformance suite, imports, coverage, write set | `grep -rn "t.Skip" internal/negknow/negknowtest/` ; `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | Zero skips; imports only `sketch store dag` + foundation; coverage ≥ 90%; writes confined to `.qompack/{records,sketches,state,tmp}/` |

### 1.10 SP-10 — Checkpointer L4 and pins

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.10.1 | §8.5 schema implemented verbatim, `"version": 1`, importance-ordered field order | `go test -run 'TestSchemaFieldOrderIsImportanceOrder\|TestSchemaVersion' ./internal/checkpoint/` | PASS |
| 1.10.2 | `Writer.Begin/Advance/Finalize/Abort` | `go test ./internal/checkpoint/...` | PASS |
| 1.10.3 | `SourceSet` structurally cannot carry live context text (§8.5 regeneration rule) | `go test -run TestSourceSetCarriesNoText ./internal/checkpoint/` | PASS — no field can carry a summary; "checkpoint from a summary" is uncompilable |
| 1.10.4 | `Advance` is DPI-guarded | `go test -run TestAdvanceIsDPIGuarded ./internal/checkpoint/` | Calls `SegmentLog.MarkEncoded`; returns `core.ErrAlreadyEncoded` on a violation |
| 1.10.5 | `Finalize` writes an immutable read-only artifact via `paths.CreateNew` + MANIFEST append | `go test -run 'TestFinalize' ./internal/checkpoint/` ; `go test -run TestAppendOnlyGuard ./internal/paths/` | Writing `0007.json` twice is an error; file mode `0444` / `FILE_ATTRIBUTE_READONLY` |
| 1.10.6 | `Reader.Latest/Get/List/Chain/Verify` with MANIFEST re-hash and parent fallback | `go test -run 'TestReader\|TestVerify' ./internal/checkpoint/` | Mismatch is a loud failure; the affected checkpoint is refused; parent used |
| 1.10.7 | `Truncate` — tier 1 never, tier 2 late, tier 3 first (§6.9) | `go test -run 'TestTruncate' ./internal/checkpoint/` | All six truncation tests PASS; monotone in budget |
| 1.10.8 | **No code snippets in checkpoints** (§13 invariant 5 / G3.4) | `go test -run TestGoldenCheckpointsContainNoCodeBlocks ./internal/checkpoint/` | PASS — goldens contain no multi-line fenced block |
| 1.10.9 | `ExtractDecisions` is the only producer of `core.DecisionID` | `go test -run TestExtractDecisions ./internal/checkpoint/` ; `grep -rn "DecisionID(" internal/ \| grep -v checkpoint/decisions.go` | PASS; only consumers outside `decisions.go`; `KindDecision` nodes and `EdgeExplains` chains emitted into the DAG |
| 1.10.10 | `ValidatePointers` against the working tree / git index (G2.5) | `go test -run TestValidatePointers ./internal/checkpoint/` | Missing pointer paths become `DropEntry`s |
| 1.10.11 | `FocusInstructions`: §4.5 standing template verbatim + O1 span paragraph | `go test -run 'TestFocusStandingTemplateVerbatim\|TestFocusIncrementalSpanNamesPathAndTurn' ./internal/checkpoint/` | Template byte-exact; O1 paragraph names the checkpoint path and turn N; `ForbidSnippets` honoured |
| 1.10.12 | Injection tagging and `StripInjections` | `go test -run TestInjectionTags ./internal/checkpoint/` | `<!-- qompack:injected seq=%d ver=%d -->` open/close; strip is exact |
| 1.10.13 | `pins` append-only log with tombstone deletion and materialized view | `go test ./internal/pins/...` | `Add`/`Remove`/`All`/`Materialize`; deletion is a tombstone record, never a rewrite |
| 1.10.14 | `PreCompact` hook writes a real checkpoint and emits `customInstructions` | `go test -run TestE2E ./test/e2e/checkpoint_test.go` (or `go test -run TestE2E_Checkpoint ./test/e2e/`) | `qompack checkpoint` exits 0; emits `customInstructions` in `full` mode and **none** in `degraded-passive`; checkpoint re-hashes to its MANIFEST line |
| 1.10.15 | Independent cadence: checkpoints exist even when compaction never fires | `go test -run 'TestCadenceFinalizesWhenDraftReachesBudget\|TestCadenceIsOffInDegradedPassive' ./internal/checkpoint/ ./internal/daemon/` | PASS |
| 1.10.16 | Budget B-E on the `PreCompact` hook | `go run ./tools/devtool bench-hotpath --hook checkpoint -n 200 --json /tmp/be.json` (and CI on three platforms) | **B-E p99 < 2 s** |
| 1.10.17 | Checkpoint micro-benchmarks | `go test -bench 'BenchmarkFinalize\|BenchmarkAdvanceSegment\|BenchmarkExtractDecisions' ./internal/checkpoint/` | < 50 ms, < 25 ms, < 20 ms; no >10% regression vs `testdata/bench-baseline.txt` |
| 1.10.18 | Residual-span reduction from frontier advancement | the two `devtool replay --set checkpoint.frontier.advanceOnSegmentClose={false,true}` runs of SP-10's exit criteria | `on.json` `Score.ResidualSpan.P50` ≥ **30% below** `off.json`; `Score.Divergence` not regressed beyond 2% |
| 1.10.19 | Conformance suites, imports, coverage | `grep -rn "t.Skip" internal/checkpoint/checkpointtest/` ; `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | Zero skips in `RunWriterSuite`/`RunReaderSuite`/`RunPinsSuite`; `checkpoint` imports only `store dag negknow pins grammar tokens` + foundation; `pins` foundation-only; coverage both ≥ 90% |

### 1.11 SP-11 — Rehydrator L5, rules, skills

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.11.1 | Eight-item injection in the normative §8.6 order | `go test -run TestBuild_ItemOrderInPayload ./internal/rehydrate/` | `ItemInvariants, ItemUserIntent, ItemEliminations, ItemDecisions, ItemCurrentWork, ItemPointers, ItemDropReport, ItemAffordance` — exactly that order |
| 1.11.2 | Item 2 is the L0 verbatim capture, never a summary (G2.3, G7.3) | `go test -run 'TestUserIntent_EarliestTurnOfThisSessionWins\|TestUserIntent_IgnoresOtherSessions' ./internal/rehydrate/` | Byte-identical to the session's first user prompt |
| 1.11.3 | Eliminations digest: top-N by slice score + standing instruction | `go test -run 'TestEliminations_StandingInstructionAlwaysPresent\|TestEliminations_StaleRendersTheVerbatimNote' ./internal/rehydrate/` | `StandingInstruction()` emitted verbatim; stale note verbatim |
| 1.11.4 | Pointers, not contents | `go test -run TestPointers ./internal/rehydrate/` | Paths + hashes + one-line reasons; no file bodies |
| 1.11.5 | Drop report (G4.5) and its persistence | `go test -run 'TestDropReport' ./internal/rehydrate/` ; `ls .qompack/state/rehydrate-*.json` in the fixture | Explicit list of dropped path rules, nested `CLAUDE.md`, truncated skills; persisted for `dropped` |
| 1.11.6 | `rules.Scanner.PathScoped` restores `paths:`-globbed rules (G4.1) | `go test -run TestRestored_PathScoped ./internal/rules/ ./internal/rehydrate/` | Every rule whose glob matches a pointer path is re-read from disk |
| 1.11.7 | `rules.Scanner.NestedClaudeMD` (G4.2) | `go test -run TestRestored_NestedClaudeMD ./internal/rules/` | Every nested `CLAUDE.md` in a pointer-set directory restored |
| 1.11.8 | `skills.Indexer` compact index (G4.4), budget from config | `go test -run 'TestSkillIndex_' ./internal/skills/` ; `grep -rn "450" internal/skills/ --include='*.go' \| grep -v _test.go` | Index ≤ `runtime.rehydrate.skillIndexTokens` (450); **no literal 450** in `internal/skills` |
| 1.11.9 | Injection tagging carries the checkpoint seq | `go test -run TestBuild_InjectionTagging ./internal/rehydrate/` | Payload wrapped in the §8.5 tags with the right `seq` |
| 1.11.10 | Budget discipline 8–12K, far below stock 50K+25K | `go test -run 'TestBuild_NeverExceedsMaxTokens\|TestBuild_SmallerThanStock\|TestBuild_MinFillReadmitsUnits\|PropBuild_MonotoneInBudget' ./internal/rehydrate/` | Never exceeds `runtime.rehydrate.maxTokens` = 12000; monotone in budget |
| 1.11.11 | Checkpoint-as-fallback when the summarizer fails (G7.5) | `go test -run 'TestBuild_NoTranscriptRead_ClosesG75' ./internal/rehydrate/` ; `go test -run TestE2E_SessionStartCompactAfterFailedSummary ./test/e2e/` | Rehydration never depends on the summary having complied |
| 1.11.12 | `SessionStart(source=compact)` and `clear` branches wired | `go test -run TestE2E_SessionStartCompactUnderBudget ./test/e2e/` | `additionalContext` delivered under budget; `clear` resets |
| 1.11.13 | Degraded-passive emits nothing | `go test -run TestService_DegradedPassiveEmitsNothing ./internal/daemon/` | No `additionalContext` in `degraded-passive` |
| 1.11.14 | `rehydrate.Reporter` satisfies `mcp.DropReporter` | `go build ./test/e2e/` (the `var _ mcp.DropReporter = rehydrate.NewReporter(...)` assertion) | Compiles |
| 1.11.15 | Phase-3 exit criterion | `go test -run TestPhase3 ./test/replay/` | Assertions A1/A2/A3 pass over the 24-session corpus against the Phase-0 baseline |
| 1.11.16 | L5 latency budgets | `go test -bench . ./internal/rehydrate/ ./internal/rules/ ./internal/skills/` | `L5-BUILD` < 250 ms p99, `L5-RULES` < 50 ms p99, `L5-SKILLS` < 20 ms p99, `L5-SESSIONSTART` < 1.5 s p99 |
| 1.11.17 | Conformance suite, imports, coverage | `grep -rn "t.Skip" internal/rehydrate/rehydratetest/` ; `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | Zero skips; `rules`/`skills` foundation-only, `rehydrate` imports only `checkpoint store negknow dag rules skills tokens` + foundation; coverage `rehydrate` ≥ 85%, `rules` ≥ 75%, `skills` ≥ 75% |

### 1.12 SP-12 — Scheduler L3

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.12.1 | `Evaluate` is a pure function of `Inputs` | `go test ./internal/scheduler/...` | No I/O, no clock, no globals; deterministic for fixed input |
| 1.12.2 | Composite trigger, all four clauses | `go test -run 'TestEvaluate_SoftFloor\|TestEvaluate_Changepoint\|TestEvaluate_YoungDaly\|TestEvaluate_HardCeiling\|TestEvaluate_IdleColdCache' ./internal/scheduler/` | `TriggerReason` values exactly `soft_floor`, `changepoint`, `young_daly`, `hard_ceiling`, `idle_cold_cache` |
| 1.12.3 | Soft floor at 55% of the effective window; hard ceiling one turn below the host threshold | `go test -run 'TestThresholds\|PropSoftFloorBelowHardCeiling' ./internal/scheduler/` | Read from `scheduler.softFloorPct` / `hardCeilingMargin`, never literals |
| 1.12.4 | p-selection: `reclaimable(p)·r − rewrite(p) − λ·coupling(p)`, argmax | `go test -run 'TestChooseP\|TestPScore' ./internal/scheduler/` | Candidates are changepoint boundaries ∩ API-round boundaries; `Breakdown` carries `reclaimable`, `rewrite`, `distortion` |
| 1.12.5 | Sliding-TTL idle model keyed on **last API call** (E1) | `go test -run 'TestTTL\|PropCacheFactor_MonotoneDecreasing' ./internal/scheduler/` | `TTLState ∈ {warm, expiring, cold, unknown}`; `rewrite(p) → 0` when cold |
| 1.12.6 | Young–Daly interval with measured δ | `go test -run 'TestYoungDaly\|TestRecordCompactionCost' ./internal/scheduler/` | `YoungDaly(δ, M) = √(2·δ·M)`; `null` config means measure-at-runtime, not zero |
| 1.12.7 | BOCD over paths/tools/time/todos with pruning and normalization | `go test -run 'TestBOCD' -rapid.checks=1000 ./internal/scheduler/` | Posterior normalized; bounded length; `MarshalBinary` round-trips (CRC-checked) |
| 1.12.8 | Droppable-block ranking (§8.7 eviction order) | `go test -run 'TestDropClassOf\|TestClassifyDrop' ./internal/scheduler/ ./internal/daemon/` | ephemeral → superseded → ordinary compactable tool result → everything else |
| 1.12.9 | O3 idle background work: six registered tasks | `go test -run 'TestIdleTasks' ./internal/daemon/` ; `go test -run TestE2E_SchedulerIdle ./test/e2e/` | `advance_frontier`, `gc`, `precompute_slice`, `refresh_delta`, `rebuild_bloom`, `compact_dag`; each inert until placed in `Decision.Background` |
| 1.12.10 | The single acting idle task is suppressed in degraded-passive | `go test -run TestIdleActingTaskSkippedInDegradedPassive ./internal/daemon/` | `act.advance_frontier` suppressed; the other five keep running |
| 1.12.11 | O5 frontier advancement drives `checkpoint.Writer.Advance` | `go test -run 'TestAdvanceFrontier' ./internal/daemon/` | Segments close on changepoint / todo completion / passing test / commit; `ErrAlreadyEncoded` path handled |
| 1.12.12 | Persistence and self-healing | `go test -run 'TestPersist\|TestStateCorrupt' ./internal/daemon/ ./internal/scheduler/` | `state/bocd.json` and `state/scheduler.json` round-trip; corruption self-heals loudly without losing store data |
| 1.12.13 | Event path proven end to end | `go test -run 'TestWrapServices_' ./internal/daemon/` ; `go test -run TestE2E_SchedulerIdle ./test/e2e/` | A real hook payload reaches `Observe`; `Stop` records an API-round boundary; an idle tick runs the six tasks |
| 1.12.14 | Scheduler is off the hot path | `go test -run TestSchedulerNotOnHotPath ./internal/...` ; `go run ./tools/devtool bench-hotpath --iterations 2000` | No hook path reaches `Evaluate`; B-A p99 < 15 ms unchanged within noise |
| 1.12.15 | `PSelectionAvailable()` gate, asserted from both sides | `go test -run TestPSelectionAvailable ./internal/scheduler/ ./internal/daemon/` | `true` after `NewSchedulerRuntime` succeeds; `false` in a build where no runtime was constructed |
| 1.12.16 | Phase-4 exit criterion | `go test -run TestPhase4 ./test/replay/` | See §2.12 for the four numeric assertions |
| 1.12.17 | Scheduler benchmarks | `go test -bench . ./internal/scheduler/ ./internal/daemon/` | `Evaluate` ≤ 50 µs/op, ≤ 8 allocs; `BOCD.Observe` ≤ 150 µs full / ≤ 20 µs steady; `BOCD.MarshalBinary` ≤ 2 ms; `FeaturesFrom` ≤ 100 µs; `AssembleCandidates` ≤ 20 ms cold / ≤ 200 µs warm; `Runtime.Evaluate` ≤ 25 ms; `reclaimableIndex` ≤ 3 ms; `SchedulerTap.ObserveTool` ≤ 1.5 ms |
| 1.12.18 | Conformance suite, imports, coverage | `grep -rn "t.Skip" internal/scheduler/schedulertest/` ; `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | Zero skips; `scheduler` imports only `core paths config logging obs`; coverage ≥ 85%; the SP-12 files in `daemon` do not lower the 75% floor |

### 1.13 SP-13 — MCP retrieval layer L6

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.13.1 | JSON-RPC 2.0 stdio server: `initialize`, `notifications/initialized`, `tools/list`, `tools/call`, `ping` | `go test ./internal/mcp/...` | PASS |
| 1.13.2 | All eight §8.7 tools registered with published input schemas | `go test -run TestToolsList ./internal/mcp/` ; `printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \| go run ./cmd/qompack mcp` | `recall`, `expand`, `re_read`, `already_tried`, `record_eliminated`, `timeline`, `why`, `dropped` — `tools/list` byte-equals its golden |
| 1.13.3 | `recall(query, k=5)` against the real store | `go test -run TestRecall ./internal/mcp/` | Hits carry hash, path, tool, summary, score |
| 1.13.4 | `expand(hash \| tool_use_id)` re-materializes a cleared result | `go test -run TestExpand ./internal/mcp/` | Chunk-boundary-aligned minimal span by default |
| 1.13.5 | `re_read(path, at)` current and historical | `go test -run TestReRead ./internal/mcp/` | Historical version served from `FileAt` |
| 1.13.6 | `already_tried` three-way, honouring `staleResponse` | `go test -run TestAlreadyTried ./internal/mcp/` | `absent` / `active` / `stale` with the note verbatim; `absent` (never `active`) on ledger failure or `BloomOnly` |
| 1.13.7 | `record_eliminated` writes through the ledger | `go test -run TestRecordEliminated ./internal/mcp/` | Structured record appended; **not** ephemeral |
| 1.13.8 | `timeline(from, to)` returns segments | `go test -run TestTimeline ./internal/mcp/` | PASS |
| 1.13.9 | `why(decision_id)` answers against real `ExtractDecisions` output | `go test -run TestWhy ./internal/mcp/` ; post-merge re-run against `checkpoint.OpenReader` | Rationale + evidence returned |
| 1.13.10 | `dropped()` answers against the real `rehydrate.DropReporter` | `go test -run TestDropped ./internal/mcp/` | Current drops listed |
| 1.13.11 | Ephemeral-at-birth on the seven retrieval tools | `go test -run TestEphemeral ./internal/mcp/` | `_meta.qompack.ephemeral == true`; a `store.ToolUseRecord` with `Ephemeral: true` is written; `record_eliminated` carries neither |
| 1.13.12 | Minimal-sufficient-span default and `full=true` escape hatch | `go test -run 'TestSpan\|PropPagingReconstructs' ./internal/mcp/` | Default ≤ `store.chunk.max` (16384) before widening; `full=true` up to `runtime.mcp.maxResponseBytes` (262144); paging reconstructs objects exactly |
| 1.13.13 | Promoter counts expansions | `go test -run 'TestNoteExpansion\|TestPromoted' ./internal/mcp/` | `promoted == true` at exactly `retrieval.promoteAfterExpansions`; `Promoted()` deduplicated, promotion-ordered, survives a process restart |
| 1.13.14 | Contract observable `mcp.server_registered` is real | `go test -run TestMCPRegistered ./internal/contract/ ./test/e2e/` ; `go run ./cmd/qompack self-test` after an MCP session | `contract.History.MCPInitialized` set in `state/contract.json`; `.qompack/state/mcp.json` written; `self-test` reports `mcp.server_registered` OK |
| 1.13.15 | Panic isolation and hostile input | `go test -run 'TestPanicIsolated\|TestMalformed' ./internal/mcp/` ; `go test -fuzz FuzzMCPLine -fuzztime 30s ./internal/mcp/` | Handler panics, malformed lines, oversized lines and unknown methods never terminate `Serve` |
| 1.13.16 | Budget B-F | `go test -run TestBudgetBF ./internal/mcp/` (and CI on three platforms) | p95 < 250 ms over 200 calls against the 2 000-tool-use / 40 MB fixture |
| 1.13.17 | Docs generated from the tool table | `go run ./tools/devtool gen-mcp-docs && git diff --exit-code -- docs/mcp-tools.md` | No diff; `plugin-validate` sees exactly 8 tools |
| 1.13.18 | Conformance suite, imports, coverage | `grep -rn "t.Skip" internal/mcp/mcptest/` ; `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | Zero skips; `mcp` imports only `store negknow checkpoint` + foundation (no `symbols`, `canon`, `sketch`, `ipc`, `daemon`, `rehydrate`); coverage ≥ 85% |

### 1.14 SP-14 — Slash commands and observability

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.14.1 | Seven commands in §7.5 order | `go run ./cmd/qompack status --json` etc.; `go test -run TestNames ./internal/commands/` | `commands.Names()` == `["status","recall","pin","checkpoint","why","dropped","eval"]` |
| 1.14.2 | `/qompack:status` renders all eleven sections | `go run ./cmd/qompack status` ; `go run ./cmd/qompack status --json` | Mode (loud banner when degraded), contract table with expected vs observed, store size + dedup ratio, sketch fill ratio + est. FP rate, per-hook latency percentiles vs B-A/B-D, frontier turn + residual tokens, last checkpoint seq/size, last scheduler `Decision.Breakdown`, GC stats, last five `Loud` messages, daemon reachability |
| 1.14.3 | No second implementation of retrieval | `go test -run TestRecall_NoSecondImplementation ./internal/commands/` | `recall`, `why`, `dropped` reach data only through `mcp.Tool.Handler` |
| 1.14.4 | `pin` and `pin --eliminated` | `go test -run TestPin ./internal/commands/` | `pin` → `pins.Store`; `--eliminated` → `negknow.Ledger` with a canonical descriptor, evidence hash, resolved `depends_on` |
| 1.14.5 | `checkpoint-now` drives the real writer and honours the DPI guard | `go test -run TestCheckpointNow ./internal/commands/` | `Begin → Advance → Finalize`; `ErrAlreadyEncoded` honoured |
| 1.14.6 | `eval` reports fraction-of-OPT, all §11.2 secondaries incl. the latency trio, and the §11.3 regression table | `go run ./cmd/qompack eval --json` ; `go test -run TestEvalCommand ./internal/commands/` | Belady computed **once per session**, not once per session per policy |
| 1.14.7 | Uniform `--json` envelope and `--help` | `go test -run 'TestEnvelope\|TestHelp' ./internal/commands/` | `Envelope{command,schema,ok,error,data}`; failing runs still emit a valid envelope with a non-zero exit; no panic on nil `Deps` members |
| 1.14.8 | Deterministic, ANSI-free rendering | `go test -run TestRenderStable ./internal/commands/` | Byte-stable across 100 renders under `FakeClock`; no direct `os.Stdout`/`os.Stderr` writes |
| 1.14.9 | Manifest ≡ binary ≡ docs | `go run ./tools/devtool plugin-validate` ; `git diff --exit-code -- plugin/commands/ docs/commands.md` | All seven resolve to real subcommands of the built binary; generated files byte-identical |
| 1.14.10 | Conformance suite; benchmarks; coverage | `go test ./internal/commands/commandstest/` ; `go test -bench . ./internal/commands/` ; `go run ./tools/devtool cover` | Zero skips; `BenchmarkCheckpointNow` < 2 s (B-E), `BenchmarkMCPFrontend` p95 < 250 ms (B-F), `BenchmarkCollect` p95 < 250 ms, `BenchmarkRenderStatus` < 5 ms; coverage ≥ 75% |

### 1.15 SP-15 — Analyzer selection and grammar (L2)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.15.1 | Cheap Δ-scorer: token overlap + symbol-reference counting | `go test -run 'TestCheapScorer' ./internal/analyzer/` | Scores in [0,1] against the observed continuation |
| 1.15.2 | Δ-scoring ranks negative knowledge highest (G6.3) | `go test -run TestCheapScorerRanksNegativeKnowledgeHighest ./internal/analyzer/` | An elimination rationale outranks a re-readable file body |
| 1.15.3 | `DetectRedundancy`: superseded + MinHash near-dups | `go test -run TestDetectRedundancy ./internal/analyzer/` | `RedundancyReport` populated; `ExcludeFromSummary` keeps superseded reads out of summaries (§8.1 item 3) |
| 1.15.4 | Suffix constraint is **structural** | `go test -run 'TestNoSelectorBypass\|PropNothingBeforeP' ./internal/analyzer/` | `NewSelector` returns `ErrBlockBeforeP` for any block with `Pos < p`; no bypass exists |
| 1.15.5 | Ship-order guard proven inert without p-selection | `go test -run 'TestNewSelectorInertWithoutPSelection\|TestPSelectionProbeIsTestOnly' ./internal/analyzer/` | `ErrPSelectionUnavailable` when `scheduler.PSelectionAvailable()` is false; zero non-test references to `SetPSelectionProbe` |
| 1.15.6 | Submodular lazy greedy meets the `(1 − 1/e)` bound | `go test -run Prop -rapid.checks=1000 ./internal/analyzer/` | `PropGuaranteeUnitCost` and `PropGuaranteeKnapsack` hold on 1 000 brute-forced instances each |
| 1.15.7 | Ephemeral results rank first for eviction | `go test -run TestEphemeralFirst ./internal/analyzer/` | `ρ(b) = 1` for `Ephemeral` blocks |
| 1.15.8 | Sequitur maintains both invariants, online and linear-time | `go test -run Prop -rapid.checks=1000 ./internal/grammar/` | `PropDigramUniqueness`, `PropRuleUtility`, `PropExpansionRoundTrip` all hold on 1 000 sequences |
| 1.15.9 | Thrash detection and warning delivery | `go test -run 'TestThrash\|TestWarningsFor' ./internal/grammar/` | High-multiplicity nonterminals surfaced; one-line warning through `UserPromptSubmit` `additionalContext` |
| 1.15.10 | Versioned CRC-checked `grammar/actions.seq` codec | `go test -run 'TestSave\|TestLoad' ./internal/grammar/` | Round-trips; corruption detected |
| 1.15.11 | Grammar folded into the checkpoint at compaction time, appended never rewritten | `go test -run 'TestFoldActionHistory\|TestFoldDoesNotBumpVersion\|TestRenderActionHistoryNoCodeFences' ./internal/checkpoint/` | `"version": 1` unchanged; no code fences (invariant 5) |
| 1.15.12 | Phase-5 exit criterion | `go test -run TestPhase5 ./test/replay/` | `FractionOfOPT(analyzer-suffix-submodular) > FractionOfOPT(baseline)` on all 24 sessions at an identical 12 000-token budget; delta ≥ 0.02 on read-heavy and refactor subsets; numbers match `testdata/replay-baseline/phase5.json` to 4 dp |
| 1.15.13 | Phase-6 exit criterion | `go test -run TestPhase6 ./test/replay/` | On every `thrash-loop` session the first warning fires at or before the third repetition **and** ≥ 5 turns before the loop ends; on non-thrash sessions the warning set is empty |
| 1.15.14 | Analyzer/grammar benchmarks | `go test -bench . ./internal/analyzer/ ./internal/grammar/` | `SequiturAppend` < 20 µs; `GrammarMarshal50k` < 20 ms and < 512 KiB; `WarningsFor` < 5 ms cold, `WarningsForCached` < 50 µs; `CheapScorer500` < 250 ms; `DetectRedundancy2000` < 300 ms; `LazyGreedy2000` < 50 ms; `LazyGreedyEvaluations` ≤ naive/5; `FoldActionHistory` < 20 ms |
| 1.15.15 | Conformance suites, imports, coverage | `grep -rn "t.Skip" internal/analyzer/analyzertest/ internal/grammar/grammartest/` ; `go run ./tools/devtool lint` ; `go run ./tools/devtool cover` | Zero skips; `analyzer` imports only `store dag sketch scheduler` + foundation; `grammar` foundation-only; coverage `analyzer` ≥ 85%, `grammar` ≥ 75% |

### 1.16 SP-16 — Phase 7 refinements

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.16.1 | O4 cross-session warm start: CMS decay + merge | `go test -run 'TestWarmStart' ./internal/daemon/` | Project CMS `Scale`d then `MergeFrom`ed into the live sketch at daemon start |
| 1.16.2 | Warm start honours `sketches.cms.warmStartFromProject` in both directions | `go test -run TestWarmStartDisabledWhenConfigFalse ./internal/daemon/` | PASS |
| 1.16.3 | Project-scope eliminations carried forward with staleness re-evaluated | `go test -run 'TestCarryForward' ./internal/daemon/ ./internal/negknow/` | Re-verified against the working tree at session start |
| 1.16.4 | BOCD feature priors seeded from past sessions | `go test -run 'TestWarmPrior' ./internal/scheduler/ ./internal/daemon/` | `scheduler.NewDetectorFromState` used; priors non-default on a project with history |
| 1.16.5 | O4 measured benefit | `go test -run TestPhase7WarmStartDelta ./test/replay/` ; `cat testdata/phase7/warmstart-delta.json` | Warm mean `FractionOfOPT` ≥ cold; warm mean `FirstDivergenceTurn` ≥ cold; both signed deltas committed |
| 1.16.6 | Demand-driven promotion into the pointer tier | `go test -run TestPromotedPointersReachRehydration ./test/e2e/` | With `retrieval.promoteAfterExpansions = 2`, a hash expanded 3× appears in the next checkpoint's `pointers.tools` at elevated weight and reaches rehydration within the 12 000-token cap |
| 1.16.7 | Per-segment Bloom filters (LSM-style, §6.8) | `go test -run 'TestSegmentBloom\|TestSegmentsMayContain' ./internal/store/` | `Segment.BloomRef` non-empty for every segment closed after SP-16; `SegmentsMayContain` answers with **zero** calls to `Open`/`OpenSpan`/`GetChunk`/`GetRoot`; measured FP ≤ 1.5% at design fill; `m ∈ [19600,19700]`, `k == 7` |
| 1.16.8 | Ski-rental write policy computed, never literal | `go test -run 'TestSkiRental\|TestNoLiteral12Point5InSource' ./internal/scheduler/` | `SkiRentalThreshold(0.1, 1.25) == 12.5` exactly; the token `12.5` appears nowhere in non-test source; the policy defers only on soft reasons and never overrides `hard_ceiling`, `changepoint` or `idle_cold_cache` |
| 1.16.9 | Progressive truncation reserves set to a measured argmax | `go test -run TestTruncationCurve ./internal/checkpoint/` ; `cat testdata/phase7/truncation-curve.json` | `ReserveLatePct` and `ReserveFirstPct` equal the recorded argmax; truncation monotone; tier 1 never truncated at any budget |
| 1.16.10 | Declared non-delivery of prefix reordering (§5.6/§12) | `go test -run TestPrefixReorderingNotAttempted ./internal/...` ; `grep -n "" docs/adr/0016-phase7-refinements.md \| grep -i "prefix reorder"` | Test green; the verbatim non-delivery sentence is in the ADR |
| 1.16.11 | Phase-7 benchmarks | `go test -bench 'BenchmarkBuildSegmentBloom\|BenchmarkPromote500\|BenchmarkWarmStart10Sessions\|BenchmarkEvaluateWithSkiRental' ./...` | < 50 ms, < 50 ms, < 3 s, within 10% of baseline |
| 1.16.12 | Artifacts reproducible without the golden-update flag | `go test -run 'TestTruncationCurve\|TestPhase7WarmStartDelta' ./...` with `QOMPACK_UPDATE_GOLDEN` unset | Green |
| 1.16.13 | Coverage floors preserved | `go run ./tools/devtool cover` | `checkpoint`, `store`, `config`, `sketch` ≥ 90%; `scheduler` ≥ 85%; `daemon` ≥ 75% |

### 1.17 SP-17 — Packaging, hardening, release

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.17.1 | Deterministic bundle assembly | `go run ./tools/devtool package` ; run twice and compare | Six per-platform bundle trees, one universal tree, ten archives with matching `.sha256`; **byte-identical archives across two consecutive runs** |
| 1.17.2 | Bundle layout per §3.4 | `find dist/plugin-windows-amd64 -type f \| sort` | `.claude-plugin/plugin.json`, `hooks/hooks.json`, `.mcp.json`, seven `commands/*.md`, `bin/qompack.exe` |
| 1.17.3 | Binary size budget | `ls -l dist/**/bin/qompack*` | linux/amd64 ≤ 20 MiB; all others ≤ 24 MiB |
| 1.17.4 | Universal launcher shims | `sh dist/plugin-universal/bin/qompack version` ; `dist\plugin-universal\bin\qompack.cmd version` (PowerShell) | Correct per-platform binary `exec`'d; launcher-internal failure exits 0; child exit code propagated otherwise |
| 1.17.5 | Launcher overhead accounted inside B-D | `go run ./tools/devtool bench-hotpath --iterations 500 --bundle universal --json /tmp/launcher.json` | p99 < 4 ms POSIX, < 15 ms Windows, reported inside B-D |
| 1.17.6 | Per-platform bundle pays no launcher cost | `go run ./tools/devtool bench-hotpath --iterations 2000 --bundle native --json /tmp/native.json` | **B-A p99 < 15 ms** and **B-E p99 < 2 s** on all three platforms, including the un-excluded Defender arm on Windows |
| 1.17.7 | `install-gate`: the shipped bundle actually works | `go run ./tools/devtool install-check` (and the CI `install-gate` job on three OSes) | All seven `hooks.json` command strings exit 0 through the platform shell within their manifest timeouts; `.mcp.json` handshakes and lists exactly 8 tools; all seven `commands/*.md` invocations exit 0 with non-empty stdout (parseable JSON where the frontmatter passes `--json`) |
| 1.17.8 | Clean uninstall | `go run ./tools/devtool uninstall-check` ; `go test -run TestUninstallLeavesNothing ./test/install/` | Project tree byte-identical to its pre-install snapshot once the plugin root, `<project>/.qompack/` and `~/.qompack/` are removed |
| 1.17.9 | Upgrade safety | `go test -run 'TestUpgradeAcrossVersions\|TestUpgradeCheckpointSchemaBump' ./test/install/` | No data loss across a version bump; a newer checkpoint schema is quarantined with the parent still usable |
| 1.17.10 | Cross-platform matrix | `go run ./tools/devtool platform-matrix` (and the CI `platform-matrix` job) | >260-char paths, spaces, non-ASCII, `Foo.ts`/`foo.ts` collision, CRLF, read-only files, eight processes racing for one daemon, socket/pipe permissions, Defender arm — all green |
| 1.17.11 | Hooks exit 0 under the full fault matrix | `go run ./tools/devtool fault-inject` ; `go test -run TestHooksExitZero_Matrix ./test/fault/` | All **54** cases exit 0, empty-or-valid stdout, no stack trace on stderr |
| 1.17.12 | Security audit, three independent proofs | `go run ./tools/devtool security-audit` ; `go test ./test/security/...` | Import-graph allowlist holds under all three `GOOS`; the network canary accepts zero connections; the Linux socket inventory shows only Unix sockets; the write set is confined to the four documented prefixes; `telemetry` is `false` from every config layer; none of the ten secret markers appears in any byte under `.qompack/`; every MCP handler survives a panic and six malformed argument shapes; all six allocation bounds hold |
| 1.17.13 | Vulnerability and static security scans | `govulncheck ./...` ; `gosec ./...` | Both exit 0 with ≤ 6 suppressions, each carrying a written reason |
| 1.17.14 | Every §12.3 degradation row has a driven failure and a documented recovery | `go test ./test/fault/...` ; read `docs/security.md` | Each of the ten rows: one driven-failure test and one recovery paragraph |
| 1.17.15 | `qompack fsck` | `go run ./cmd/qompack fsck --strict` on a clean store; then on each of the nine seeded corruptions; then `--repair` | Exits 0 clean; exits 1 on each seeded corruption; repairs all nine; a full pass over a 200 000-object store completes in < 60 s and is resumable under a short deadline (`FsckReport.Truncated`) |
| 1.17.16 | `qompack doctor` | `go run ./cmd/qompack doctor --skip-mcp` ; `go run ./cmd/qompack doctor --json` | All 16 checks run in < 3 s with `--skip-mcp`, < 8 s with the MCP probe, in both human and `--json` modes; each non-pass carries a remedy |
| 1.17.17 | `qompack version` and version-drift guard | `go run ./cmd/qompack version --json` ; `go run ./tools/devtool verify-version` ; `go run ./tools/devtool changelog --check` | Binary version, `pluginmanifest.DefaultVersion`, the tag and the CHANGELOG heading are one number; both devtool checks clean |
| 1.17.18 | Release pipeline dry run | `actionlint` ; `goreleaser release --snapshot --clean` | `actionlint` clean; snapshot release succeeds; the artifact list matches `docs/release.md` |
| 1.17.19 | The four new CI jobs are required checks | inspect branch protection / `.github/workflows/ci.yml` | `package-gate`, `platform-matrix`, `fault-gate`, `install-gate` present and required on `develop` and `main` |
| 1.17.20 | Checksum file format | `head -1 dist/plugin-universal/bin/SHA256SUMS` | `<64 lowercase hex><two spaces><filename>\n`, LF endings, bytewise-sorted by filename, no header |

### 1.18 SP-18 — Documentation and UAT

| # | Functionality | Command | Expected result |
|---|---|---|---|
| 1.18.1 | Complete owned doc set, 22 documents | `go test -run 'TestOwnedDocsExist\|TestOwnedDocsFinalInventoryIsTwentyTwo\|TestOwnedDocsHasNoDuplicates\|TestOwnedDocsStartWithH1' ./test/docs/` | `README.md` + eight `docs/*.md` + `docs/adr/README.md` + twelve ADRs; each ≥ 400 bytes and starting with an H1 |
| 1.18.2 | Config reference generated, never stale, complete | `go run ./tools/devtool gen-config-docs && git diff --exit-code` ; `go test -run 'TestConfigReferenceNotStale\|TestConfigReferenceCoversEveryKey\|TestConfigReferenceHasNoOrphanRows\|TestJSONSchemaCoversEveryDocumentedKey' ./test/docs/` | No diff; 72 leaves, 72 `Meta` entries, zero orphans, zero missing |
| 1.18.3 | Config-reference ranges agree with `Validate()` | `go test -run TestMetaRangesMatchValidate ./test/docs/` | Every documented range matches the validator |
| 1.18.4 | `runtime` namespace marked additive | `go test -run TestConfigReferenceRuntimeSectionIsMarkedAdditive ./test/docs/` | PASS |
| 1.18.5 | Cannot-do list reproduced verbatim | `go test -run 'TestCannotDoListVerbatim\|TestCannotDoCoversResiduals\|TestCannotDoCoversG72\|TestCannotDoQuotesScopeNote' ./test/docs/` | All eight §12 bullets byte-for-byte; all fifteen §9 residual strings; the "Not closeable from a plugin" sentence; the §5.6 scope note |
| 1.18.6 | Upstream issues and tracker template | `go test -run 'TestUpstream' ./test/docs/` ; inspect `.github/ISSUE_TEMPLATE/upstream-tracker.yml` | All five upstream items with their five required subsections and a filable issue block; the template has all seven fields |
| 1.18.7 | Loud-message glossary covers every contract ID and failure mode | `go test -run 'TestLoudGlossaryCoversContractIDs\|TestLoudGlossaryCoversFailureModes' ./test/docs/` | All nine `contract.ID` constants with the severity `contract.StandardAssertions()` actually reports; all ten §12.3 rows |
| 1.18.8 | ADRs D1–D12 present and structurally correct | `go test -run 'TestAdrShape\|TestAdrIndexEntriesExist\|TestAdrNamesItsDecisionID\|TestArchitectureLinksEveryADR' ./test/docs/` | Twelve ADRs `0001`–`0012`, each naming its decision ID and linked from `docs/architecture.md` |
| 1.18.9 | Architecture digest quotes the ten invariants | `go test -run 'TestArchitectureDigestSections\|TestArchitectureQuotesTenInvariants' ./test/docs/` | PASS |
| 1.18.10 | Hygiene: no placeholders, no unresolved markers, no trailing whitespace, all links resolve | `go test -run 'TestNoPlaceholders\|TestNoUnresolvedTemplateMarkers\|TestNoTrailingWhitespaceOrTabs\|TestNoLinksToUnownedGeneratedDocs' ./test/docs/` ; `grep -RnE '\b(TODO\|TBD\|FIXME\|XXX\|WIP)\b' README.md docs/ .github/ISSUE_TEMPLATE/upstream-tracker.yml` | All PASS; grep returns nothing; every relative link and anchor resolves |
| 1.18.11 | Twelve UAT scenarios, structurally conformant, quoting the phase criteria in the right scenario | `go test -run 'TestUAT\|TestUATQuotesPhaseExitCriteria' ./test/docs/` | UAT-01 … UAT-12 contiguous, five required blocks each, a result-table row each; Phase 1 quoted in UAT-02, Phase 2 in UAT-09, Phase 3 in UAT-05, Phase 4 in UAT-10 |
| 1.18.12 | **UAT executed end to end by a human against a real project** | follow `docs/uat.md` UAT-01 … UAT-12 | Twelve verdicts recorded in the completion report §8. This is a manual, non-CI item and it is mandatory for V6 |
| 1.18.13 | Config-docs generator determinism and budget | `go test -run 'TestGenerateIsDeterministic\|TestGenerateSectionOrder\|TestGenerateEscapesPipes\|TestGenerateEndsWithSingleNewline\|TestGenerateRejectsBadMeta\|TestGenerateRendersDefaultsAsJSON\|TestGenerateStableUnderLeafOverrides\|TestGenerateKeyCountLineMatches' ./tools/devtool/configdocs/` ; `go test -bench BenchmarkGenerate ./tools/devtool/configdocs/` | All PASS; `BenchmarkGenerate` < 25 ms/op (**B-DOC**) |
| 1.18.14 | Doc-package import discipline | `go list -deps ./tools/devtool/configdocs ./test/docs \| grep qompack/internal` | Exactly two `internal/` packages: `internal/config` (from `configdocs`) and `internal/contract` (from `test/docs`) |

---

## 2. Exit-criteria re-verification

Every completed subplan's exit criteria, quoted, with the concrete measurement procedure to run **now**, on `verify/v6`, against the real merged implementation. A criterion measured on a branch three waves ago is not evidence about today's `develop`.

### 2.1 SP-01 — Foundation

> SP-01 precedes Phase 0, so no phase exit criterion applies to it directly. The criteria it must **make measurable for later waves** … And the guardrails SP-01 must express as configuration and CI, verbatim from §11.3:
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Procedure.** Prove the *mechanisms* still exist and still bind (the numbers themselves are §5):

1. `go run ./cmd/qompack config print --provenance` → `eval.minSessions` = 20, `scheduler.cache.readMultiplier` = 0.1, `writeMultiplier` = 1.25, each with an `Origin` and `Location`.
2. `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/` → PASS.
3. `go run ./tools/devtool lint` → the `nomagic` sub-check clean, proving no forbidden literal escaped `internal/config/defaults.go` in eighteen subplans.
4. Inspect `.github/workflows/ci.yml` and branch protection: `bench-gate` and `replay-gate` are **required** on `develop` and `main` and neither is `continue-on-error`.
5. `go test -run 'TestAppendOnlyGuard|TestGuard_' ./...` → PASS.
6. `git log --format=%B $(git merge-base main develop)..HEAD | grep -Ei 'co-authored-by|signed-off-by|generated with'` → nothing.

### 2.2 SP-02 — Phase 0

> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.

**Procedure.**

1. `go run ./test/replay --corpus testdata/sessions/synthetic --write-baseline /tmp/p0-v6.json`; run again to `/tmp/p0-v6b.json`; `cmp` them → byte-identical.
2. Compare `policies.stock.fraction_of_opt`, `sessions` (= 24) and `corpusTier` (= `synthetic`) against the committed `testdata/baseline/phase0.json` → equal.
3. Confirm the honesty discharge: `docs/adr/0002-replay-methodology.md` states the committed number is synthetic-corpus and names the recorded-corpus command and its owner.
4. V6 is the last checkpoint before release, so the tier-2 number is due here: if `$QOMPACK_SESSIONS_DIR` holds ≥ 20 recorded sessions, produce `testdata/baseline/phase0-recorded.json` with `"corpusTier": "recorded"`. If it does not, record **"recorded tier not available on this machine"** explicitly in the completion report — never silently skip it.
5. SP-02's three owned §11.3 guardrails: `--growth` (sublinear), the 2% rule with its sign-off-trailer scan, and `--phase` running every merged phase's exit assertion.

### 2.3 SP-03 — Sketches

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions … hook p99 < 15ms.
> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents …
> - Hook p99 latency < 15ms (L0), < 2s (L4)

SP-03 is a dependency of both criteria rather than the owner of either. Re-measure its three named contributions:

1. `go test -bench BenchmarkL0SketchUpdate -benchmem ./internal/sketch/` → ≤ 5 µs/op and **0 allocs/op** (contribution to hook p99).
2. `go test -run TestMinHash ./internal/sketch/` on the "same test suite, one new failure" fixture → near-dup detected (contribution to 4:1).
3. `go test -run 'TestSave_ErrGenerational|TestReplaceGenerational|TestRebuildBloom' ./internal/sketch/` → frozen `tried.bloom` format plus rebuild-from-an-arbitrary-iterator (contribution to zero stale blocks).

### 2.4 SP-04 — Chunking, canonicalization, symbols

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.
> FastCDC over 100KB is well under 1ms
> boundary stability under insertion (inserting bytes at offset k perturbs at most 2 chunks after the insertion point); determinism across platforms and Go versions; `Min ≤ len ≤ Max` for every chunk except the last.
> `Canonicalize(Canonicalize(x)) == Canonicalize(x)`; `Restore(Canonicalize(x).Canonical, deltas) == x` whenever `KeepDeltas`; no canonicalizer ever *grows* its input.

**Procedure.**

1. `go test ./test/dedup/...`; diff `testdata/canon-dedup-report.json` against the committed copy → byte-for-byte, `testrunner` `gain ≥ 1.25`, overall `gain ≥ 1.0`. SP-04 owns *"measure with and without"*, not the 4:1 number.
2. `go test -bench BenchmarkSplit_100KB ./internal/chunk/` → < 800 µs/op.
3. `go test -run TestSplit_BoundaryStability -rapid.checks=1000 ./internal/chunk/` → PASS.
4. Cross-platform determinism: read the CI `test` job logs for ubuntu, macos **and** windows and confirm `TestGearTableGolden` and `TestSplit_GoldenBoundaries` passed on all three against the *same* goldens. A per-platform golden is a failure.
5. `go test -run 'Idempot|NonGrowth|TestRestore' ./internal/canon/` → PASS for every canonicalizer individually.

### 2.5 SP-05 — Daemon, IPC, hot path, contract monitor

> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.
> **Exit criterion:** … hook p99 < 15ms.
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently.
> Everything is designed to degrade gracefully. If a hook stops firing, Qompack becomes a passive recorder and the session behaves exactly as it does today.

**Procedure.**

1. `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json /tmp/ba-v6.json` locally on Windows; read the CI `bench-gate` artifacts for ubuntu and macos. **B-A p99 < 15 ms**, **B-B p99 < 2 ms**, **B-E p99 < 2 s** on all three; B-D recorded, never gated.
   This is the first honest measurement of the budget with the **finished** system attached — observer, scheduler tap, negknow, checkpointer, MCP handlers, warm start. Wave 1 measured it with almost nothing wired. Treat a pass here as the real result and a wave-1 pass as historical.
2. `go test -run 'TestBreachDetectorTransitionsAfterThreeWindows|TestBreachDetectorRevertsAfterThreeCleanWindows|TestHotModeTransitionWritesStateAndNAKs' ./internal/daemon/` → the "degrade to async queue-and-drain" clause is an observable transition.
3. `go test -run 'TestFreshBuildReportsModeFull|TestDeclaredProducerSetMatchesArchitecture' ./internal/contract/` → `ModeFull`, and at V6 **no assertion may report `not-yet-implemented`**: every producer has shipped. `mcp.server_registered`, `precompact.has_time_to_write`, `precompact.custom_instructions_accepted` and `hook.additional_context_delivered` must all be real observations.
4. `go test -run 'TestDegradedPassiveSuppressesActingPaths|TestDegradedPassiveStillRecords|TestModeOffSkipsIngest' ./...` → §7.1 holds.
5. `go test -run 'TestHooksExitZeroUnderFaults|TestSelfTestIsTheOnlyNonZeroExit' ./...` → 66 combinations exit 0.

### 2.6 SP-06 — Store

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions …
> - Store growth sublinear in session length after dedup
> **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint.

**Procedure.**

1. `go test -run TestPhase1ExitCriterion_ReadHeavy ./internal/store/` → `Stats.DedupRatio ≥ 4.0`. **Record the number**, do not just record PASS.
2. `go test -run TestStats_SublinearGrowth ./internal/store/` plus the replay driver's `--growth` → both sublinear and agreeing.
3. `go test -run 'TestSegment_MarkEncodedRefusesDifferentSeq|TestSegment_MarkEncodedBatchIsAllOrNothing' ./internal/store/` → `core.ErrAlreadyEncoded`; batch all-or-nothing; same-seq idempotent.
4. `go test -run TestStats_DedupRatio ./internal/store/` with the canonicalization toggle → the number moves.
5. `go test -bench BenchmarkPutBytes_100KB_Cold ./internal/store/` → ≤ 3 ms, against B-C.

### 2.7 SP-07 — DAG and slicing

> This is graph reachability: BFS over a few thousand nodes, sub-millisecond.
> Thin slicing drops control-dependence-only edges for much smaller slices at the cost of soundness — probably the right tradeoff here.
> Output: a relevance score per node, not a binary keep/drop — the score feeds submodular selection.
> `segment_coupling(p)` is the count of DAG edges crossing `p` — a direct, cheap measure of how much the post-`p` region depends on pre-`p` detail.
> Do not ship slicing or submodular selection before p-selection.

**Procedure.**

1. `go test -bench 'BenchmarkBackwardSlice5000|BenchmarkForwardSlice5000' ./internal/dag/` and `go test -run TestSliceLatencyBudget ./internal/dag/` → both < 1 ms/op on 5 000 nodes / ~15 000 edges.
2. `go test -run TestThinVsFullComparison ./internal/dag/` → mean `size_ratio ≤ 0.75`, mean `recall ≥ 0.85` across eight seeds.
3. `go test -run TestNoBooleanKeepAPI ./internal/dag/` → no exported keep-set/drop-list/`map[NodeID]bool` return; the `NO SELECTION AUTHORITY` note still in `doc.go`.
4. `go test -run TestCrossingEdges -rapid.checks=1000 ./internal/dag/`; `go test -bench BenchmarkCrossingEdges ./internal/dag/` → matches brute force; < 5 µs at 15 000 edges.
5. Closing-note-3 ordering verified repo-wide now that both halves exist: `go test -run 'TestNewSelectorInertWithoutPSelection|TestPSelectionAvailable' ./internal/analyzer/ ./internal/scheduler/`.

### 2.8 SP-08 — Observer L0 (closes Phase 1)

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup

**Procedure.**

1. `go test -run 'TestPhase1_DedupRatioReadHeavy|TestPhase1_CanonicalizationGapOnTestOutput|TestPhase1_StoreGrowthSublinear' ./test/e2e/` → `DedupRatio ≥ 4.0`; `ratioOn ≥ ratioOff × 1.25`; sublinear. Record all three numbers and compare them to the figures recorded in `docs/adr/0008-observer-l0.md`; a drift beyond noise is a regression to investigate, not a doc update.
2. `bench-gate` on three platforms with the observer wired → B-A p99 < 15 ms, B-B p99 < 2 ms.
3. `go test -bench 'BenchmarkOnToolUse_FileRead64KB|BenchmarkOnToolUse_TestOutput256KB|BenchmarkTombstone' ./internal/observer/` → within B-C; tombstone < 2 µs/op.
4. Gap closure re-asserted end to end: `go test -run 'TestTombstone|TestOnUserPrompt_NeverRegenerated|TestE2E_VerbatimPromptSurvivesRestart|TestOnStop_RetrievalPathG10_1|TestExtractSignals' ./...` → G3.2, G2.3, G10.1, G1.5.

### 2.9 SP-09 — Negative knowledge (Phase 2)

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.
> **The bloom filter is a cache, never the source of truth (§8.3).** Every membership answer is backed by a record lookup or explicitly flagged `BloomOnly`.

**Procedure.**

1. `go test -run TestPhase2ExitCriterion ./test/replay/` → `stockRepeats > 0`; `negknowRepeats ≤ 0.75 × stockRepeats`; strictly better on ≥ 8 individual sessions; `staleBlocks == 0` across every session with a non-empty `DependencyChangeAt`, with at least one such session present. Record `stockRepeats`, `negknowRepeats`, `staleBlocks`.
2. `go test -run TestRebuildBloom_Resizes ./internal/negknow/` → at 8 000 active records, `FillRatio ≤ 0.5` and `EstFPRate < 0.02`.
3. `go test -run 'TestQuery_BloomOnly|TestBloomLoadFailure_NoRecords_NeverFalsePositive|TestRebuildBloom_NeverFromCheckpoint' ./internal/negknow/` → invariant 3 holds; `sketch.RebuildBloom` has exactly one call site in `internal/`, fed only by `visibleActive()`.
4. Cross-check the whole staleness path against SP-13's surface — see integration test §3.4.

### 2.10 SP-10 — Checkpointer (producing half of Phase 3)

> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> Note what is **not** here: no code snippets. Files are pointers with a one-line reason.

**Procedure.**

1. Re-run SP-10's own gate at V6:
   ```
   go run ./tools/devtool replay --corpus testdata/sessions/synthetic \
       --filter multi-compaction --set checkpoint.frontier.advanceOnSegmentClose=false --json /tmp/off.json
   go run ./tools/devtool replay --corpus testdata/sessions/synthetic \
       --filter multi-compaction --set checkpoint.frontier.advanceOnSegmentClose=true  --json /tmp/on.json
   ```
   `on.json` `Score.ResidualSpan.P50` must be **≥ 30% below** `off.json`'s, and `Score.Divergence` must not regress beyond the 2% of §11.3.
2. `go run ./tools/devtool bench-hotpath --hook checkpoint -n 200` on three platforms → **B-E p99 < 2 s**.
3. `go test -run 'TestGoldenCheckpointsContainNoCodeBlocks|TestSourceSetCarriesNoText|TestAdvanceIsDPIGuarded' ./internal/checkpoint/` → no snippets; no summary-sourced checkpoint; DPI guard live.
4. `go test -run 'TestCadenceFinalizesWhenDraftReachesBudget|TestCadenceIsOffInDegradedPassive' ./...` → checkpoints exist even when compaction never fires, and the cadence is off in `degraded-passive`.

### 2.11 SP-11 — Rehydrator (closes Phase 3)

> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.
> **Budget discipline.** Default rehydration budget is deliberately far below Claude Code's 50K + 25K: target 8–12K.
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Procedure.**

1. `go test -run TestPhase3 ./test/replay/` → assertions A1 (first-divergence turn index improves against the Phase-0 baseline), A2 (at a smaller rehydration budget than stock), A3 (measured reduction in summarization-call input tokens when the span instruction is honoured). Record all three deltas.
2. `go test -run 'TestBuild_NeverExceedsMaxTokens|TestBuild_SmallerThanStock|TestBuild_MinFillReadmitsUnits|PropBuild_MonotoneInBudget|TestE2E_SessionStartCompactUnderBudget' ./...` → ≤ `runtime.rehydrate.maxTokens` = 12000, far below 50K + 25K.
3. `bench-gate` and `replay-gate` green with no >2% regression on any metric.

### 2.12 SP-12 — Scheduler (Phase 4)

> **Exit criterion:** measured reduction in total rewrite tokens per session, with no regression in divergence metrics; median compaction pause and residual span flat as session length grows (the amortization claim, tested directly).
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Procedure.** `go test -run TestPhase4 ./test/replay/`, recording every number:

1. `Σ RewriteTokens[qompack-l3] ≤ 0.80 × Σ RewriteTokens[stock]` over the 24-session corpus.
2. No divergence metric (`FirstDivergenceTurn`, `FileSetJaccard`, `DecisionPreservation`, `RedundantReads`, `ReAttempts`) regressed by more than 2%, with **no** `sign-off:` trailer required.
3. Least-squares slope of median residual span against session turn count ≤ 0.02 tokens/turn; longest-quartile median ≤ 1.25× shortest-quartile median. This is the amortization claim, tested directly.
4. `ResidualSpan.P95 ≤ 20 000` (`checkpoint.frontier.maxResidualTokens`).
5. Compaction pause derived linearly from residual span (`3000 ms + 0.12 ms/token`), written to `.qompack/eval/phase4-pause.json` with `"pause_modelled": true`, and **never** presented as a measured wall-clock in deterministic replay. Verify the flag is present in the artifact.
6. `go test -run TestSchedulerNotOnHotPath ./...` plus `bench-hotpath -n 2000` on three platforms → B-A p99 unchanged within noise.

### 2.13 SP-13 — MCP retrieval

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. …
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite
> | Retrieval layer re-inflates the context window | Medium | Ephemeral-at-birth policy; minimum-sufficient-span defaults; retrieval results are first eviction candidates (§8.7) |
> | **B-F** | `mcp_tool_call` — request → response | p95 < 250 ms (`minimal` span) | CI |

**Procedure.**

1. `go test -run TestAlreadyTried ./internal/mcp/` against the `dependency-change-mid-session` synthetic fixture → `"stale"` (never `"active"`) for every record whose `depends_on` hash changed, and `"absent"` under `staleResponse: "drop"`. **Zero stale-block incidents attributable to the MCP layer.**
2. `go test -run TestBudgetBF ./internal/mcp/` on ubuntu, macos and windows → p95 < 250 ms over 200 calls against the 2 000-tool-use / 40 MB fixture.
3. Re-inflation defence: `go test -run 'TestEphemeral|TestSpan|TestNoteExpansion' ./internal/mcp/` → `_meta.qompack.ephemeral = true`, a `ToolUseRecord{Ephemeral:true}` written, minimal span by default. The eviction-ordering half is verified in §3.5.
4. `plugin-validate` sees exactly 8 tools; `tools/list` byte-equals its golden.

### 2.14 SP-14 — Slash commands and observability

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite
> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions … hook p99 < 15ms.
> **Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

SP-14 does not *achieve* these numbers — SP-06, SP-05 and SP-02 do. Its exit condition is that `/qompack:status` and `/qompack:eval` **report** them correctly and deterministically, and that a wrong number is visible rather than silent.

**Procedure.**

1. Drive a real session against the fixture project, then `go run ./cmd/qompack status --json > /tmp/status.json` and cross-check field by field:
   - `dedup_ratio` == the `Stats.DedupRatio` recorded in §2.6.
   - per-hook p99 values agree with the `bench-hotpath` artifact of §2.5 within 20%.
   - `frontier` / `residual_tokens` agree with the Phase-4 artifact of §2.12.
   - sketch fill ratio and estimated FP rate agree with `negknow.Health()` from §2.9.
   A disagreement between the status surface and the underlying measurement is a **V6 failure**, not a rounding note.
2. `go run ./cmd/qompack eval --json` → fraction-of-OPT, every aggregatable §11.2 secondary metric including the v1.2 latency trio, and the §11.3 regression table; Belady computed once per session, not once per session per policy.
3. `go test -run TestRecall_NoSecondImplementation ./internal/commands/` → no shadow implementation of any retrieval path.

### 2.15 SP-15 — Analyzer and grammar (Phases 5 and 6)

> **Exit criterion:** improved fraction-of-OPT at equal budget.
> **Exit criterion:** thrash detected before the user notices it, on replay.
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite
> ```
> f(S_greedy) ≥ (1 − 1/e)·f(S_opt) ≈ 0.63
> ```

**Procedure.**

1. `go test -run TestPhase5 ./test/replay/` → `FractionOfOPT(analyzer-suffix-submodular) > FractionOfOPT(baseline)` across all 24 sessions at an identical **12 000-token** budget, delta ≥ 0.02 on the read-heavy and refactor subsets, and the numbers matching `testdata/replay-baseline/phase5.json` to 4 decimal places.
2. `go test -run TestPhase6 ./test/replay/` → on every `thrash-loop` session the first warning fires at or before the third repetition **and** at least 5 turns before the loop ends; on every non-`thrash-loop` session the warning set is empty at end of session.
3. `go test -run Prop -rapid.checks=1000 ./internal/analyzer/` → `PropGuaranteeUnitCost` and `PropGuaranteeKnapsack` against brute force. `-rapid.checks=1000` is **not** optional; rapid's default of 100 does not discharge this criterion.
4. `go test -run Prop -rapid.checks=1000 ./internal/grammar/` → `PropDigramUniqueness`, `PropRuleUtility`, `PropExpansionRoundTrip`.
5. `go test -run TestCheapScorerRanksNegativeKnowledgeHighest ./internal/analyzer/` → G6.3 closed by a specific ranking, not by assertion.

### 2.16 SP-16 — Phase 7

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite
> **Cross-session warm start (O4).** The store outlives the session; use it. Warm-start Count-Min with the project's historical hot-file distribution, carry `scope: "project"` eliminations forward, and seed the changepoint model's feature priors from past sessions. First-compaction quality in a fresh session should benefit from every session before it.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

**Procedure.**

1. `go test -run TestPhase7WarmStartDelta ./test/replay/` → warm mean `FractionOfOPT` ≥ cold; warm mean `FirstDivergenceTurn` ≥ cold; both signed deltas matching `testdata/phase7/warmstart-delta.json`.
2. `go test -run TestWarmStartDisabledWhenConfigFalse ./internal/daemon/` → `sketches.cms.warmStartFromProject` honoured in both directions.
3. `go test -run TestPromotedPointersReachRehydration ./test/e2e/` → with `retrieval.promoteAfterExpansions = 2`, a 3×-expanded hash lands in the next checkpoint's `pointers.tools` at elevated weight and reaches rehydration inside the 12 000-token cap.
4. `go test -run 'TestSegmentBloom|TestSegmentsMayContain' ./internal/store/` → `Segment.BloomRef` non-empty for every segment closed after SP-16; zero `Open`/`OpenSpan`/`GetChunk`/`GetRoot` calls; measured FP ≤ 1.5% at design fill; `m ∈ [19600,19700]`, `k == 7`.
5. `go test -run 'TestSkiRental|TestNoLiteral12Point5InSource' ./internal/scheduler/` → `SkiRentalThreshold(0.1, 1.25) == 12.5` computed as `w/r`; the literal absent from non-test source; the policy never overrides `hard_ceiling`, `changepoint` or `idle_cold_cache`.
6. `go test -run TestTruncationCurve ./internal/checkpoint/` with `QOMPACK_UPDATE_GOLDEN` unset → reserves equal the recorded argmax; truncation monotone; tier 1 never truncated at any budget.
7. `go test -run TestPrefixReorderingNotAttempted ./...` and the verbatim non-delivery sentence in `docs/adr/0016-phase7-refinements.md`.

### 2.17 SP-17 — Packaging, hardening, release

> | G9.2 hand-rebuilt layer | This plugin *is* the layer, packaged | — |
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite
> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. … If the budget is exceeded, degrade to async queue-and-drain rather than blocking.
> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.
> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions … hook p99 < 15ms.

**Procedure.** Walk SP-17's fifteen numbered Definition-of-Done items and treat each as a V6 line item:

1. `go run ./tools/devtool package` twice; `sha256sum` every archive from both runs and diff → identical. Six per-platform bundles, one universal bundle, ten archives with matching `.sha256`.
2. `ls -l` every release binary → linux/amd64 ≤ 20 MiB; all others ≤ 24 MiB.
3. `bench-hotpath` against the **universal** bundle (launcher p99 < 4 ms POSIX, < 15 ms Windows, reported inside B-D) and against the **per-platform** bundle (B-A p99 < 15 ms, B-E p99 < 2 s on all three platforms, Defender arm not excluded on Windows).
4. CI `install-gate` on all three OSes → seven `hooks.json` command strings exit 0 inside their manifest timeouts; `.mcp.json` handshakes and lists exactly 8 tools; all seven `commands/*.md` invocations exit 0 with non-empty stdout.
5. `go test -run TestUninstallLeavesNothing ./test/install/`.
6. `go test -run 'TestUpgradeAcrossVersions|TestUpgradeCheckpointSchemaBump' ./test/install/`.
7. `go test -run TestHooksExitZero_Matrix ./test/fault/` → all 54 cases exit 0, empty-or-valid stdout, no stack trace on stderr.
8. `go test ./test/security/...` → import-graph allowlist under all three `GOOS`; network canary accepts zero connections; Linux socket inventory shows only Unix sockets; write set confined to the four documented prefixes; telemetry `false` from every layer; none of the ten secret markers under `.qompack/`; MCP handlers survive a panic and six malformed argument shapes; all six allocation bounds hold.
9. `govulncheck ./...` and `gosec ./...` → exit 0 with ≤ 6 suppressions, each with a written reason.
10. Every row of the §12.3 table has a driven-failure test **and** a recovery paragraph in `docs/security.md`.
11. `qompack fsck --strict`: exit 0 clean; exit 1 on each of the nine seeded corruptions; `--repair` repairs all nine; the 200 000-object pass < 60 s and resumable under a short deadline.
12. `qompack doctor`: 16 checks, < 3 s with `--skip-mcp`, < 8 s with the MCP probe, human and `--json`.
13. `devtool verify-version`, `devtool changelog --check`, `actionlint`, `goreleaser release --snapshot --clean`.
14. All thirteen CI jobs green.
15. `gofumpt -l` empty; `golangci-lint run` clean; `nomagic` clean; coverage floors unchanged; `internal/cli` ≥ 75%.

Additionally: `doctor`'s reported Phase-1 ratio and §11.4 bloom thresholds must agree with the numbers recorded in §2.6 and §2.9.

### 2.18 SP-18 — Documentation and UAT

> **Phase 1 — Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.
> **Phase 2 — Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change.
> **Phase 3 — Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.
> **Phase 4 — Exit criterion:** measured reduction in total rewrite tokens per session, with no regression in divergence metrics; median compaction pause and residual span flat as session length grows (the amortization claim, tested directly).
> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

SP-18 ships no phase; it ships the guide by which a human confirms every shipped phase on their own machine. Its criterion is that the guide is **executable exactly as written**.

**Procedure.**

1. `go test ./test/docs/... ./tools/devtool/configdocs/...` → zero failures, zero skips.
2. `go run ./tools/devtool gen-config-docs && git diff --exit-code` → no diff; 72 leaves, 72 `Meta` entries, zero orphans, zero missing.
3. `go test -run TestUATQuotesPhaseExitCriteria ./test/docs/` → each phase criterion quoted verbatim **inside** the scenario that operationalizes it: Phase 1 → UAT-02, Phase 2 → UAT-09, Phase 3 → UAT-05, Phase 4 → UAT-10.
4. **Execute `docs/uat.md` UAT-01 … UAT-12 by hand against a real project, using the packaged bundle from §2.17.** Record twelve verdicts in the completion report. A scenario that cannot be executed exactly as written is a documentation defect and therefore a V6 failure: fix `docs/uat.md` on `verify/v6`, never record a workaround.
5. `go test -run 'TestCannotDoListVerbatim|TestCannotDoCoversResiduals|TestCannotDoCoversG72|TestCannotDoQuotesScopeNote|TestLoudGlossaryCoversContractIDs|TestLoudGlossaryCoversFailureModes' ./test/docs/` → the honesty surface is complete: eight §12 bullets byte-for-byte, fifteen §9 residual strings, the "Not closeable from a plugin" sentence, the §5.6 scope note, nine `contract.ID`s and ten §12.3 failure rows.
6. **The documentation describes the binary that actually ships.** `go test -run 'TestUATCommandsAreReal|TestUserGuideCoversSevenCommands|TestUserGuideJSONFormMatchesCommandsDoc|TestUserGuideCoversEightMCPTools|TestUserGuideHookTableMatchesManifest|TestUserGuideStatusSectionCoversEveryStatusField|TestUserGuideStorageTreeMatchesLayout|TestReadmeStructure|TestReadmeInstallMatchesManifest|TestReadmeFirstFiveMinutesHasFiveSteps' ./test/docs/` → every `qompack <word>` occurrence across `docs/uat.md`, `docs/user-guide.md` and `docs/troubleshooting.md` is in the 00-ARCHITECTURE §2.3 subcommand set; every `/qompack:<word>` is one of the seven names read from `plugin/commands/*.md`; the seven hook event names match `plugin/hooks/hooks.json`; the eight MCP tool sections carry their §8.7 purpose text verbatim; the twelve §5.17 status field labels are all present. Then re-run the subcommand half against the **packaged** binary of §2.17 — that is §3.3. A document that describes a subcommand the shipped binary does not have is a release blocker, not a doc nit.
7. **The twelve ADRs and the architecture digest.** `go test -run 'TestAdrShape|TestAdrNamesItsDecisionID|TestAdrIndexEntriesExist|TestArchitectureDigestSections|TestArchitectureQuotesTenInvariants|TestArchitectureLinksEveryADR' ./test/docs/` → `0001`–`0012` present, all twelve carrying the same `**Date:**`, each with `## Context`, `## Decision`, `## Consequences`, `### Positive`, `### Negative`, `## References` in that byte order, each naming its own `D<N>` and each with at least one Negative item; the eight digest headings in order; ARCH §13 invariant 7 quoted verbatim. Note the boundary: `OwnedDocs()` and `docs/adr/README.md` own exactly twelve ADRs. ADR files written by other subplans — `docs/adr/0016-phase7-refinements.md` of §2.16 is the one this checkpoint reads — exist on disk, are not in `OwnedDocs()`, and are not indexed. If `TestAdrIndexEntriesExist` fails because a sibling ADR was added to the index, the index is what is wrong.
8. **Upstream issues and the tracker template.** `go test -run 'TestUpstreamIssuesCoversFive|TestUpstreamIssuesQuotesEvidence|TestUpstreamTrackerTemplateShape' ./test/docs/` → the five upstream items as `##` headings in design-document order, each with its five `###` subsections and a fenced block whose first line starts with `Title:`; the §12 upstream sentence, G1.2, G4.5, G8.1, the §2.6 PTL paragraph and the two §2.7 "**Lost**" rows plus the "**Not re-injected at all**" row all verbatim; `.github/ISSUE_TEMPLATE/upstream-tracker.yml` carrying `name:`, `description:`, `title:`, `labels:`, `body:`, the seven field `id:` values, all five item titles as dropdown options, and `required: true` at least six times.
9. **Hygiene and completeness of the owned set.** `go test -run 'TestOwnedDocsExist|TestOwnedDocsFinalInventoryIsTwentyTwo|TestOwnedDocsHasNoDuplicates|TestOwnedDocsStartWithH1|TestReadmeLength|TestNoPlaceholders|TestNoUnresolvedTemplateMarkers|TestNoTrailingWhitespaceOrTabs|TestRelativeLinksResolve|TestNoLinksToUnownedGeneratedDocs|TestReadmeDocumentationIndexIsComplete|TestTroubleshootingSymptomIndexResolves' ./test/docs/` → exactly 22 owned documents, no duplicates, each ≥ 400 bytes and starting with an H1, `README.md` ≤ 220 lines, every relative link and anchor resolving, no link to `docs/mcp-tools.md` or `CHANGELOG.md`, and a symptom index of ≥ 15 rows all resolving to real headings. Then the manual scan SP-18's Done checklist requires, run by hand and pasted into the report: `grep -RnE '\b(TODO|TBD|FIXME|XXX|WIP)\b' README.md docs/ .github/ISSUE_TEMPLATE/upstream-tracker.yml` → nothing.
10. **Generator determinism, budget and import discipline.** `go test ./tools/devtool/configdocs/` → `TestLeavesWalksInDeclarationOrder`, `TestLeavesReportsMissingMeta`, `TestLeavesReportsOrphanMeta`, `TestLeavesDottedKeysResolveViaGet`, `TestGenerateIsDeterministic`, `TestGenerateSectionOrder`, `TestGenerateKeyCountLineMatches`, `TestGenerateRejectsBadMeta`, `TestGenerateEscapesPipes`, `TestGenerateRendersDefaultsAsJSON`, `TestGenerateEndsWithSingleNewline`, `TestGenerateStableUnderLeafOverrides`, `TestJSONSchemaCoversEveryDocumentedKey` and `TestMetaRangesMatchValidate` all PASS; `go test -bench BenchmarkGenerate ./tools/devtool/configdocs/` → **B-DOC < 25 ms/op**; `go list -deps ./tools/devtool/configdocs ./test/docs | grep qompack/internal` → exactly two lines, `internal/config` and `internal/contract`. A third `internal/` package under `test/docs` is a V6 failure even if every doc test still passes.
11. **Execution rules for the hand-run of item 4.** These are what make the twelve verdicts evidence rather than opinion, and they are quoted from `docs/uat.md`'s own preamble:
    - A scenario fails if **any** `**Pass**` bullet is unmet. A partially-met scenario is a fail, not a partial pass. Record the failing bullet verbatim.
    - Run the full guide on at least one platform and record which. The exhaustive cross-platform matrix — long paths, unicode, antivirus interference, case-insensitive filename collisions — is §1.17.10's automated CI job and is never a hand-substitute for it, nor the reverse.
    - Preconditions are not negotiable: a real project of ≥ 200 files with a test suite and at least one `CLAUDE.md`, and the plugin installed per `README.md` `## Install` **from the bundle built in §2.17**, not from `go run ./cmd/qompack`.
    - UAT-03 on a machine without a repository clone: run the `/qompack:status` half only, judge the scenario on that bullet alone, and write the omission on the scenario's line in §8.9. Silence about the omission is a false pass.
    - UAT-11 requires both halves — (a) daemon loss with spool growth and a clean drain, and (b) the Loud degradation drill including the restore step in which two clean session starts call `Monitor.Restore` and `/qompack:status` reports `mode: full` again. Half a drill is a fail.
    - UAT-12 samples `netstat -ano` / `ss -tanp` filtered to the daemon's pid **at least three times** across the session; one sample is not a measurement.
    - Verdicts are recorded in the completion report §8.9 and in the merge commit body. Leave the `## Result table` in `docs/uat.md` empty in the repository — the shipped table is the operator's blank form, and committing filled cells makes `git diff develop -- docs/uat.md` non-empty for a reason that is not a fix.
    - A scenario that cannot be executed exactly as written is fixed in `docs/uat.md` on `verify/v6` (item 4), and the scenario is then re-executed from its first step, not resumed.

Additionally: every threshold `TestUATNamesConcreteThresholds` asserts present in the guide — `15 ms`, `2 s`, `250 ms`, `4:1`, `8000`, `12000`, `450`, `20000` — must equal the corresponding number in §5 and in Appendix C. A guide threshold that no longer matches the measured budget is a documentation defect fixed on `verify/v6`; it is never grounds for re-stating the budget.

---

## 3. New cross-component integration tests

Everything in §1 and §2 verifies a component or a criterion. This section verifies the **seams that only became testable in wave 5** — the ones where the packaged artifact, the shipped documentation and the finished runtime meet. These are new, permanent tests: they are authored on `verify/v6`, committed, and stay in the suite forever. A seam covered only by a manual observation during this checkpoint is a seam that regresses silently in the next release.

**Placement rules.**

- Tests that drive a real project and a real daemon go in `test/e2e/`.
- Tests that install, upgrade or remove a built bundle go in `test/install/` and run in the CI `install-gate` job.
- Tests that assert a posture (no network, no secrets, confined write set) go in `test/security/` and run in the CI `security` job.
- **Nothing goes in `test/docs/`.** §1.18.14 pins that package to exactly two `internal/` imports; a test that needs a store, a daemon or a bundle would break it. A doc-vs-binary assertion belongs in `test/install/`, reading the documents as plain files.
- Every test uses `internal/testutil`'s temp-project fixture and `FakeClock` where time matters, opens no network socket, and writes nothing outside its temp project and `~/.qompack/` redirected into the fixture.
- No test may be marked `t.Skip` on any platform. A test that cannot run on Windows is a test written wrong; use the platform-conditional assertion, not the skip.

### 3.1 — `TestV6_PackagedBundleObservesARealSessionEndToEnd`

**Seam:** bundle → launcher → `internal/cli` → `internal/ipc` → `internal/daemon` → `internal/store` + `internal/dag` + `internal/sketch`.
**Setup:** install `dist/plugin-<goos>-<goarch>` into a temp project exactly as `README.md` `## Install` describes; fire all seven `hooks.json` command strings through the platform shell with recorded payloads (a `SessionStart`, twenty `PostToolUse` events including four reads of one 200 KB file, a `UserPromptSubmit`, a `Stop`, a `SubagentStop`, a `SessionEnd`).
**Asserts:** every invocation exits 0 inside its manifest timeout; `index/tool_use.jsonl` has exactly one line per tool call; `objects/` is populated with the two-level fanout; `Stats().DedupRatio ≥ 4.0` on the read-heavy loop; the DAG log round-trips; no file was written outside `.qompack/` and the redirected `~/.qompack/`.

### 3.2 — `TestV6_UniversalLauncherPreservesHookSemantics`

**Seam:** `packaging/launcher/{qompack.sh,qompack.cmd,qompack.ps1}` → the per-platform binary.
**Setup:** the universal bundle from `devtool package`, plus a fault arm in which the launcher's own resolution fails (missing `bin/<goos>-<goarch>/` directory).
**Asserts:** the correct binary is `exec`'d on each platform; a launcher-**internal** failure still exits 0 (invariant 6 survives the shim); a non-zero child exit is propagated unchanged; the launcher's own overhead is reported inside **B-D** and never folded into B-A.

### 3.3 — `TestV6_DocumentedCommandsRunAgainstTheShippedBundle`

**Seam:** `README.md` + `docs/user-guide.md` + `docs/uat.md` + `docs/troubleshooting.md` → `plugin/commands/*.md` → the packaged binary.
**Setup:** parse every `qompack <word>` and `/qompack:<word>` occurrence out of the four documents; resolve each against the installed bundle rather than against a `go run` build.
**Asserts:** each subcommand exists in the shipped binary and exits 0 (or exits non-zero only where the document says it should — `self-test` is the single permitted non-zero exit); each of the seven `commands/*.md` frontmatter invocations exits 0 with non-empty stdout, parseable JSON wherever the frontmatter passes `--json`; the `.mcp.json` handshake lists exactly the eight §8.7 tools. This is the automated half of §2.18 item 6 and the standing regression guard for the whole documentation set.

### 3.4 — `TestV6_EliminationStalenessSurvivesPackagingAndAnswersThroughMCP`

**Seam:** `internal/negknow` → `internal/store` → `internal/sketch` → `internal/mcp` → the packaged MCP server. **This is the test §2.9 item 4 refers to.**
**Setup:** through the bundle's `.mcp.json` server, call `record_eliminated` with a real evidence hash and a non-empty `depends_on`; then modify the file named in `depends_on` in the working tree; then let the idle window run the `refresh_delta` and `rebuild_bloom` tasks.
**Asserts:** before the change, `already_tried` answers `active` and the record is in `records/eliminations.jsonl` with `status: active`; after the change and the idle window, it answers `stale` with the §8.3 note byte-identical, and the record carries `stale_since` and `stale_because`; the bloom was rebuilt from `visibleActive()` only, never from a checkpoint; with `eliminations.staleResponse: "drop"` the same query answers `absent`; and at no point does any query answer `active` for a record that no longer exists — **zero stale-block incidents through the shipped surface**, which is the Phase-2 criterion measured where a user actually meets it.

### 3.5 — `TestV6_EphemeralRetrievalResultsAreEvictedFirst`

**Seam:** `internal/mcp` → `internal/store` → `internal/analyzer` → `internal/scheduler` → `internal/checkpoint`. **This is the test §2.13 item 3 refers to.**
**Setup:** a session with a mixture of ordinary tool results, superseded reads and retrieval results produced by `recall`/`expand`, then a compaction at a budget tight enough to force drops.
**Asserts:** every retrieval result carries `_meta.qompack.ephemeral == true` and a `store.ToolUseRecord{Ephemeral: true}`; the real selector assigns `ρ(b) = 1` to those blocks so they rank first for eviction, ahead of superseded reads and ordinary compactable tool results (§8.7 order); the context after compaction is no larger than the context before retrieval by more than the promoted-pointer allowance; and a hash expanded `retrieval.promoteAfterExpansions` times is the one exception, arriving in `pointers.tools` rather than as a body. The re-inflation risk row of §12 is closed by this test, not by the ephemeral flag alone.

### 3.6 — `TestV6_FsckRepairsSeededCorruptionWithoutLosingLiveData`

**Seam:** `internal/cli` (`fsck`) → `internal/store` + `internal/checkpoint` + `internal/negknow` + `internal/sketch`.
**Setup:** a populated store; then each of the nine seeded corruptions in turn, applied to a fresh copy.
**Asserts:** `fsck --strict` exits 0 on the clean store and 1 on each corruption, naming the finding; `--repair` repairs all nine; after every repair the live roots, the checkpoint chain and the active elimination records are still readable and byte-identical to their pre-corruption values; the bloom is rebuilt from records rather than trusted (invariant 3); nothing outside `.qompack/` is touched.

### 3.7 — `TestV6_DoctorAgreesWithStatusAndWithTheUnderlyingSubsystems`

**Seam:** `internal/cli` (`doctor`) → `internal/commands` (`status`) → `store`, `negknow`, `obs`, `contract`, `scheduler`.
**Setup:** one populated project; run `qompack doctor --json` and `qompack status --json` back to back with no session activity in between.
**Asserts:** all 16 `doctor` checks run; the dedup ratio, bloom fill ratio, estimated FP rate, per-hook p99s, contract mode and frontier/residual figures agree field for field between the two surfaces and with the packages that produce them; every non-pass check carries a remedy string. A disagreement between two of our own observability surfaces is a V6 failure — §2.14 says so for `status`, and this test says it for `doctor`.

### 3.8 — `TestV6_ConfigReferenceDescribesTheBinaryThatShips`

**Seam:** `tools/devtool/configdocs` → `internal/config` → the packaged binary.
**Setup:** parse every dotted key out of `docs/config-reference.md`; drive the installed binary, not the source tree.
**Asserts:** `qompack config schema` from the bundle contains every documented key; `qompack config print --provenance` reports an `Origin` and a `Location` for each; every documented key is settable with `--set <key>=<value>` and the change is visible in the next `config print`; every documented range is the range `config.Validate()` enforces, except in the `runtime.` namespace where a documentary range is permitted. 72 keys, 72 rows, no orphans.

### 3.9 — `TestV6_InstallUpgradeUninstallLeavesTheProjectByteIdentical`

**Seam:** `test/install` → the bundle → the project tree and `~/.qompack/`.
**Setup:** snapshot the project tree; install; run a session; upgrade to a bumped version; run another session; uninstall.
**Asserts:** no data lost across the version bump; a newer checkpoint schema is quarantined with its parent still usable; after removing the plugin root, `<project>/.qompack/` and `~/.qompack/`, the project tree hashes byte-identically to the pre-install snapshot; `git status` in the project reports no new tracked files at any point.

### 3.10 — `TestV6_DegradedPassiveIsCorrectFromThePackagedBundle`

**Seam:** `internal/contract` → `internal/daemon` → every acting subsystem, driven through the bundle.
**Setup:** the UAT-11(b) drill automated — corrupt one byte of a finalized checkpoint after clearing its read-only attribute, then start a session.
**Asserts:** the MANIFEST sha256 mismatch is Loud, the affected checkpoint is refused and its parent used, the session flips to `degraded-passive`; L0 and L1 keep recording; no `additionalContext`, no `customInstructions`, no scheduler-initiated checkpoint, no drop report; `act.advance_frontier` suppressed while the other five idle tasks keep running; MCP retrieval still answers because it is pull-based; `logs/LOUD.log` has the entry and `status` leads with the banner naming expected vs observed; after restoring the file, two clean session starts call `Monitor.Restore` and the mode returns to `full`.

### 3.11 — `TestV6_NoSecretAndNoNetworkAcrossAFullPackagedSession`

**Seam:** `internal/redact` → `internal/store` → the daemon process, observed from outside.
**Setup:** a full session through the bundle that reads a file containing one fake token per built-in rule descriptor, while a network canary listens and the process's socket table is sampled.
**Asserts:** the canary accepts zero connections; the daemon holds no listening TCP socket and opens no outbound connection at any sample; every object under `objects/` contains the `«redacted:` placeholder and none of the ten secret markers; the write set is confined to the four documented prefixes; `runtime.telemetry.enabled` reads `false` from every configuration layer. This is UAT-12 made non-manual, so the manual scenario confirms rather than discovers.

### 3.12 — `TestV6_CheckpointToRehydrationRoundTripThroughTheBundle`

**Seam:** `internal/checkpoint` → `internal/rehydrate` → `internal/rules` + `internal/skills` → `internal/mcp` (`dropped`).
**Setup:** run a session to a real `PreCompact`, then a `SessionStart(source=compact)`, through the installed bundle.
**Asserts:** the checkpoint is read-only, listed in `MANIFEST.jsonl` with a matching hash, `"version": 1`, contains no fenced code block; the injection carries the `<!-- qompack:injected seq=… -->` tag; the eight §8.6 items arrive in the normative order with item 2 byte-identical to the session's first user prompt; total injected tokens ≤ 12000; the `paths:`-scoped rule and the nested `CLAUDE.md` in the pointer set are restored; the skill index is ≤ 450 tokens; `dropped()` lists exactly what `rehydrate.Reporter` persisted. This is UAT-04, UAT-05 and UAT-06 in one automated pass.

### 3.13 — `TestV6_ReleaseArtifactsAreReproducibleAndSelfConsistent`

**Seam:** `devtool package` → `.goreleaser.yaml` → `internal/pluginmanifest` → `CHANGELOG.md`.
**Setup:** two consecutive `devtool package` runs into separate output directories.
**Asserts:** the ten archives are byte-identical between runs and each matches its `.sha256`; `SHA256SUMS` is `<64 lowercase hex><two spaces><filename>\n`, LF endings, bytewise-sorted, no header; the binary version, `pluginmanifest.DefaultVersion`, the tag under test and the CHANGELOG heading are one number; the artifact list matches `docs/release.md`; `release-guard --tag <tag> --require-branch main` reports every failing condition rather than the first.

### 3.14 — `TestV6_HotPathHoldsWithEverySubsystemResidentInTheBundle`

**Seam:** everything, measured where the user runs it.
**Setup:** the per-platform bundle installed, a warm daemon pre-populated with 2 000 tool uses and 40 MB of raw output, warm start enabled, the elimination ledger and the grammar resident.
**Asserts:** **B-A p99 < 15 ms**, **B-B p99 < 2 ms**, **B-E p99 < 2 s**, **B-F p95 < 250 ms**; B-D reported; no hook path reaches `scheduler.Evaluate`; the breach detector does not transition during the run. This is the same measurement as §5 but pinned as a permanent test so a future change cannot quietly spend the budget.

---

## 4. Whole-tree and release-candidate gates

Run these in the main session, after §1–§3, on `verify/v6`. They are whole-tree operations: delegating them produces two half-answers.

### 4.1 The local pass

```bash
go run ./tools/devtool ci-local        # fmt, lint, vet, nomagic, importgraph,
                                       #   testdeps, bindeps, sleepcheck, stubskips, build
go test ./... -race                    # ubuntu + macos
go test ./... -count=2                 # windows
go run ./tools/devtool cover           # every §6.4 floor
go run ./tools/devtool build-all       # six targets
go run ./tools/devtool package         # ten archives, twice, compared
go run ./tools/devtool install-check
go run ./tools/devtool uninstall-check
go run ./tools/devtool platform-matrix
go run ./tools/devtool fault-inject
go run ./tools/devtool security-audit
go run ./tools/devtool bench-hotpath --iterations 2000
go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop --ci
go run ./tools/devtool plugin-validate
go run ./tools/devtool gen-config-docs --check
go run ./tools/devtool gen-mcp-docs     ; git diff --exit-code -- docs/mcp-tools.md
go run ./tools/devtool gen-command-docs ; git diff --exit-code -- docs/commands.md plugin/commands/
go run ./tools/devtool verify-version
go run ./tools/devtool changelog --check
govulncheck ./...
gosec ./...
actionlint
goreleaser release --snapshot --clean
```

### 4.2 The thirteen CI jobs on `verify/v6`

| Job | Runs on | Must show |
|---|---|---|
| `verify` | ubuntu | `gofumpt -l` empty, `golangci-lint run` clean, `go vet` clean, `nomagic` clean, import-graph clean, test-only-dep check clean, build clean, and the attribution-trailer grep over the commit range empty |
| `test` | ubuntu, macos, windows | `go test ./...` green; `-race` on ubuntu and macos, `-count=2` on windows |
| `cover` | ubuntu | every §6.4 floor held (table in §4.3) |
| `crossbuild` | ubuntu | all six §2.6 targets built |
| `bench-gate` | ubuntu, macos, windows | hard pass on **B-A** and **B-E**; B-D recorded |
| `replay-gate` | ubuntu | the §11.3 2% rule with **zero** allowed regressions (§6.2) and every phase exit assertion, Phases 0–7 |
| `plugin-validate` | ubuntu | `plugin/**` regenerated with a clean diff; 7 commands, 8 MCP tools |
| `security` | ubuntu | `govulncheck`, `gosec`, secret scan, the import assertions of `00-ARCHITECTURE` §8 |
| `docs` | ubuntu | `gen-config-docs` with a clean diff; `./test/docs/...` green |
| `package-gate` | ubuntu, macos, windows | reproducible archives, checksums, binary size budgets |
| `platform-matrix` | ubuntu, macos, windows | long paths, spaces, non-ASCII, `Foo.ts`/`foo.ts`, CRLF, read-only files, eight racing processes, socket/pipe permissions, the Defender arm |
| `fault-gate` | ubuntu, macos, windows | all 54 cases exit 0 |
| `install-gate` | ubuntu, macos, windows | hooks, MCP handshake and all seven commands exercised from the installed bundle |

All thirteen must be green on the **final** `verify/v6` commit — not on an earlier one with fixes layered after it. `package-gate`, `platform-matrix`, `fault-gate` and `install-gate` are required checks on `develop` and `main` (§1.17.19); confirm that in branch protection, not only in the workflow file.

### 4.3 Coverage floors (00-ARCHITECTURE §6.4), measured on the merged Linux profile

| Floor | Packages |
|---|---|
| **90%** | `config` `store` `sketch` `chunk` `canon` `negknow` `checkpoint` `paths` `redact` `tokens` `pins` |
| **85%** | `scheduler` `dag` `analyzer` `rehydrate` `eval` `mcp` |
| **75%** | everything else, including `commands` `grammar` `observer` `ipc` `daemon` `contract` `rules` `skills` `cli` |

A drop below any floor fails `verify` and is a V6 failure, never a waiver. `internal/cli` grew substantially in SP-17 (`fsck`, `doctor`, `version`); confirm it is still above 75% rather than assuming SP-17 left it there.

### 4.4 The ten §13 invariants, re-asserted on the release candidate

> 7. **No network. No telemetry. No writes outside `.qompack/`** (plus `~/.qompack/` for the global layer). CI asserts the import graph and a runtime test asserts the write set.

| # | Invariant | Proof on `verify/v6` |
|---|---|---|
| 1 | Never compress a compression | `go test -run 'TestSourceSetCarriesNoText\|TestAdvanceIsDPIGuarded\|TestSegment_MarkEncodedRefusesDifferentSeq\|TestInjectionTags' ./internal/checkpoint/ ./internal/store/` |
| 2 | Append-only means append-only | `go test -run TestAppendOnlyGuard ./internal/paths/` ; `qompack fsck --strict` checks 1, 4, 5, 6, 7, 8 |
| 3 | The bloom filter is a cache, never the source of truth | `go test -run 'TestQuery_BloomOnly\|TestBloomLoadFailure_NoRecords_NeverFalsePositive\|TestRebuildBloom_NeverFromCheckpoint' ./internal/negknow/` ; §3.4 |
| 4 | Nothing scattered before `p` | `go test -run 'TestNoSelectorBypass\|PropNothingBeforeP\|TestNewSelectorInertWithoutPSelection' ./internal/analyzer/` |
| 5 | No code snippets in checkpoints | `go test -run 'TestGoldenCheckpointsContainNoCodeBlocks\|TestRenderActionHistoryNoCodeFences' ./internal/checkpoint/` |
| 6 | Hooks exit 0. Always. | `go test -run TestHooksExitZero_Matrix ./test/fault/` (54) ; `go test -run 'TestHooksExitZeroUnderFaults\|TestSelfTestIsTheOnlyNonZeroExit' ./internal/cli/ ./test/e2e/` (66) ; §3.2 for the launcher |
| 7 | No network, no telemetry, no writes outside `.qompack/` | `go test ./test/security/...` ; `go run ./tools/devtool security-audit` ; §3.11 |
| 8 | Every constant §12 says might change is a config key | `go run ./tools/devtool lint` (`nomagic`) ; `go test -run 'TestNoLiteral12Point5InSource' ./internal/scheduler/` ; `grep -rn "450" internal/skills/ --include='*.go' \| grep -v _test.go` empty |
| 9 | Every latency budget is measured, not assumed | §5, every cell filled |
| 10 | Degradation is loud | `go test -run 'TestMonitorRestore\|TestLoadWithLog_Corrupt\|TestHotModeTransitionWritesStateAndNAKs' ./...` ; §3.10 |

### 4.5 Residue checks

| Check | Command | Required result |
|---|---|---|
| No stub skips anywhere | `grep -rn "t.Skip" --include='*_test.go' internal/ test/ tools/` | No occurrence carrying `behaviour: implementation is a stub (Rule W-1)` or `contract fixture not yet recorded (Rule W-2)` |
| No unimplemented sentinels outside `core` | `grep -rn "ErrNotImplemented" --include='*.go' internal/ \| grep -v '^internal/core/'` | Empty |
| No `not-yet-implemented` contract assertion | `go test -run TestDeclaredProducerSetMatchesArchitecture ./internal/contract/` ; `go run ./cmd/qompack self-test` | All nine assertions report a real observation |
| `Qompack.md` untouched | `git diff develop -- Qompack.md` ; `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | Both empty |
| `plans/00-ARCHITECTURE.md` §5 untouched | `git diff develop -- plans/00-ARCHITECTURE.md` | Empty, or one reviewed `arch/` amendment merged into `develop` first and named in the report |
| No attribution trailers | `git log develop..verify/v6 --format=%B \| grep -iE 'co-authored-by\|signed-off-by\|generated with\|🤖'` | Empty |
---

## 5. Performance budget validation

Every budget in the project, measured **on the release candidate**, on all three platforms. Run the sweep serially on an idle machine after §1–§4, from the **packaged per-platform bundle** wherever the budget is one a user experiences; a number produced by `go run ./cmd/qompack` is a development number and does not discharge a shipped budget.

> 9. **Every latency budget is measured, not assumed** (§7). Adding work to L0 without a bench result is a review rejection.

A cell left blank is a budget that was not measured, and §13 invariant 9 says an unmeasured budget is a failure. Where a job genuinely does not run on a platform, write `n/a — <job> is ubuntu-only` in the cell. Never write "ok".

### 5.1 How each number is produced

| ID | Budget | Source | Command |
|---|---|---|---|
| **B-A** | `hook_controlled` p99, per-platform bundle | §8.1, §11.3 L0, ARCH §2.4 | `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --bundle native --json /tmp/v6-ba.json` on all three; CI `bench-gate` artifact is the record of ubuntu and macos |
| **B-B** | `l0_ingest` (daemon read → WAL append) p99 | ARCH §2.4 | the same bench artifact; `go test -bench BenchmarkIngestAccept ./internal/daemon/` |
| **B-C** | `l0_process` p99 (soft) | ARCH §2.4 | `go test -bench 'BenchmarkOnToolUse_' -benchtime 5s ./internal/observer/` |
| **B-D** | `hook_wall` incl. host process creation and the launcher | ARCH §2.4 | the same bench artifact — **reported, never gated** |
| **B-E** | `checkpoint_finalize` p99 | §11.3 L4, ARCH §2.4 | `go run ./tools/devtool bench-hotpath --hook checkpoint -n 200 --bundle native --json /tmp/v6-be.json` on all three |
| **B-F** | `mcp_tool_call` p95 (`minimal` span) | ARCH §2.4 | `go test -run TestBudgetBF ./internal/mcp/` ; `go test -bench BenchmarkMCPFrontend ./internal/commands/` |
| **LAUNCH-P** | Universal-launcher overhead p99, POSIX | SP-17 DoD 3 | `go run ./tools/devtool bench-hotpath --iterations 500 --bundle universal --json /tmp/v6-launcher.json` on ubuntu and macos |
| **LAUNCH-W** | Universal-launcher overhead p99, Windows | SP-17 DoD 3 | the same command on windows (`qompack.cmd` / `qompack.ps1`) |
| **B-DOC** | Config-reference generation | SP-18 DoD | `go test -bench BenchmarkGenerate ./tools/devtool/configdocs/` |
| **L5** | `L5-BUILD`, `L5-RULES`, `L5-SKILLS`, `L5-SESSIONSTART` | SP-11 | `go test -bench . ./internal/rehydrate/ ./internal/rules/ ./internal/skills/` |
| **SIZE** | Release binary size | SP-17 DoD 2 | `ls -l dist/*/bin/qompack*` after `devtool package` |
| **FSCK** / **DOCTOR** | Whole-store pass and diagnostic latency | SP-17 DoD 11, 12 | `qompack fsck --strict` on the 200 000-object fixture ; `qompack doctor --skip-mcp` and `qompack doctor` |
| **P1** | Dedup ratio and canonicalization gap | §10 Phase 1 | `go test -run 'TestPhase1_DedupRatioReadHeavy\|TestPhase1_CanonicalizationGapOnTestOutput' ./test/e2e/` ; `go test ./test/dedup/...` |
| **Growth** | Store growth exponent | §11.3 | `go test -run TestStats_SublinearGrowth ./internal/store/` ; `devtool replay --growth` |
| **Rehydration / residual / rewrite** | Injection cap, residual span, rewrite tokens | §8.5, §8.6, §10 Phases 3–4 | `go test -run 'TestBuild_NeverExceedsMaxTokens' ./internal/rehydrate/` ; `go test -run 'TestPhase3\|TestPhase4' ./test/replay/` |
| **Bloom** | Elimination and segment bloom health | §11.4 | `go test -run TestHealth ./internal/negknow/` ; `go test -run 'TestSegmentBloom' ./internal/store/` |
| **Micro** | Every named micro-benchmark of §1 | ARCH §7 | `go run ./tools/devtool bench` then `benchstat testdata/bench-baseline.txt /tmp/v6-bench.txt` |

### 5.2 The validation table

| ID | Budget | Threshold | linux | macos | windows | Verdict |
|---|---|---|---|---|---|---|
| B-A | `hook_controlled` p99, packaged bundle | **< 15 ms** (hard) | | | | |
| B-B | `l0_ingest` p99 | < 2 ms | | | | |
| B-C | `l0_process` p99 | < 50 ms (soft) | | | | |
| B-D | `hook_wall` p99 | reported, never gated | | | | reported |
| B-E | `checkpoint_finalize` p99, packaged bundle | **< 2 s** (hard) | | | | |
| B-F | `mcp_tool_call` p95 | < 250 ms | | | | |
| LAUNCH-P | Universal launcher overhead p99 | **< 4 ms** (POSIX) | | | n/a — POSIX only | |
| LAUNCH-W | Universal launcher overhead p99 | **< 15 ms** (Windows) | n/a — Windows only | n/a — Windows only | | |
| B-DOC | `BenchmarkGenerate` | < 25 ms/op | | | | |
| L5-BUILD | `rehydrate.Build` p99 | < 250 ms | | | | |
| L5-RULES | `rules.Scanner` p99 | < 50 ms | | | | |
| L5-SKILLS | `skills.Indexer` p99 | < 20 ms | | | | |
| L5-SESSIONSTART | whole `SessionStart` p99 | < 1.5 s | | | | |
| SIZE-1 | `linux/amd64` binary | ≤ 20 MiB | | n/a | n/a | |
| SIZE-2 | every other release binary | ≤ 24 MiB | | | | |
| FSCK | full pass, 200 000 objects | < 60 s, resumable | | | | |
| DOCTOR | 16 checks | < 3 s `--skip-mcp`, < 8 s with MCP | | | | |
| P1-ratio | `Stats().DedupRatio`, read-heavy | ≥ 4.0 | | | | |
| P1-gap | canonicalization gap, test-output-heavy | `ratioOn ≥ ratioOff × 1.25` | | | | |
| P1-canon | `testrunner` / overall dedup gain | ≥ 1.25 / ≥ 1.0 | | | | |
| Growth | store growth exponent after dedup | < 1.0 | | | | |
| Rehydration | `Result.Tokens` maximum | ≤ 12 000 | | | | |
| Residual-P95 | residual span at compaction | ≤ 20 000 | | | | |
| Residual-slope | median residual vs turn count | ≤ 0.02 tok/turn | | | | |
| Rewrite | `Σ RewriteTokens` vs stock | ≤ 0.80 × stock | | | | |
| Bloom-FP | elimination bloom `EstFPRate` at 8 000 active | < 0.02 | | | | |
| Bloom-fill | elimination bloom `FillRatio` at 8 000 active | ≤ 0.5 | | | | |
| SegBloom | segment bloom measured FP at design fill | ≤ 1.5% | | | | |
| M-sketch | `BenchmarkL0SketchUpdate` | ≤ 5 µs/op, 0 allocs | | | | |
| M-chunk | `Split_100KB` / `GearScan_1MiB` / `SplitStream_4MiB` | < 800 µs / ≥ 400 MB/s / ≤ 40 ms | | | | |
| M-canon | `Run_Bash100KB` / `Run_GoTest` / `Restore_100KB` | < 3 ms / < 1 ms / < 1 ms | | | | |
| M-symbols | `Extract_100KB` / `Enclosing_100KB` / `References_100KB_50Names` | < 2 ms / < 2 ms / < 1 ms | | | | |
| M-store | `PutBytes_100KB_Cold` / `Search` 1 000 roots / `GC` 50 k objects | ≤ 3 ms / ≤ 25 ms / ≤ 2 s | | | | |
| M-dag | `BackwardSlice5000` / `ForwardSlice5000` / `CrossingEdges` | < 1 ms / < 1 ms / < 5 µs | | | | |
| M-observer | `BenchmarkTombstone` | < 2 µs/op | | | | |
| M-scheduler | `Evaluate` / `BOCD.Observe` steady / `Runtime.Evaluate` | ≤ 50 µs, ≤ 8 allocs / ≤ 20 µs / ≤ 25 ms | | | | |
| M-checkpoint | `Finalize` / `AdvanceSegment` / `ExtractDecisions` / `FoldActionHistory` | < 50 ms / < 25 ms / < 20 ms / < 20 ms | | | | |
| M-analyzer | `CheapScorer500` / `DetectRedundancy2000` / `LazyGreedy2000` | < 250 ms / < 300 ms / < 50 ms | | | | |
| M-grammar | `SequiturAppend` / `GrammarMarshal50k` / `WarningsFor` cold | < 20 µs / < 20 ms / < 5 ms | | | | |
| M-commands | `Collect` p95 / `RenderStatus` | < 250 ms / < 5 ms | | | | |
| M-obs | `Histogram_Observe` | < 100 ns/op | | | | |
| M-phase7 | `BuildSegmentBloom` / `Promote500` / `WarmStart10Sessions` | < 50 ms / < 50 ms / < 3 s | | | | |
| E-1…E-5 | eval replay suite | 120 s / 250 ms / 50 ms / 20 ms / 15 ms | | | | |
| benchstat | worst regression vs `testdata/bench-baseline.txt` | fail > 25%, explain > 10% | | | | |

### 5.3 Notes that must not be shortcut

- **B-A and B-E are measured from the installed per-platform bundle**, whose `bin/qompack[.exe]` is the native binary, so no launcher sits inside the measurement. The universal bundle's launcher cost is LAUNCH-P/LAUNCH-W and is reported inside **B-D**, never inside B-A. Folding it into B-A is exactly the dishonest measurement this project has refused since wave 1.
- **The Windows Defender arm is not excluded.** SP-17's platform matrix runs one arm with real-time protection active; B-A must hold there too, and the report says which arm produced the recorded number.
- **B-D is reported and never gated.** Host process creation is not ours.
- **This is the first and only measurement of every budget with the finished system attached** — observer, scheduler tap, negknow ledger, checkpointer, MCP handlers, grammar, warm start, segment blooms, packaging. Earlier waves measured subsets. Where a V6 number is worse than the wave-1 or wave-4 number for the same budget but still inside the threshold, record both and say so; where it crosses the threshold, that is a failure under §9, not a re-baselining opportunity.
- `testdata/bench-baseline.txt` is updated on `verify/v6` **only** for legitimate wave-5 additions, in its own commit, with the reason in the body.

---

## 6. Regression

V6 re-runs **every prior inventory in full**. "Wave 5 only touched packaging and documentation" is a claim; SP-17's own scope rule permits `Hardening:`-tagged fixes anywhere in `internal/`, and SP-18 edits two files owned by other subplans. Neither is a reason to trust an old measurement, and this is the last checkpoint before a public artifact.

### 6.1 Predecessor checkpoints

| Checkpoint | Covered subplans | Re-run in V6 as |
|---|---|---|
| **V1** (wave 0) | SP-01 | §1.1 in full, plus §2.1 and the §4.1 toolchain pass |
| **V2** (wave 1) | SP-02, SP-03, SP-04, SP-05, SP-06, SP-07 | §1.2–§1.7 in full, plus §2.2–§2.7 and budgets B-A, B-B, B-C, B-D |
| **V3** (wave 2) | SP-08, SP-09 | §1.8–§1.9 in full, plus §2.8–§2.9 and the Phase-1 and Phase-2 criteria |
| **V4** (wave 3) | SP-10, SP-11, SP-12, SP-13 | §1.10–§1.13 in full, plus §2.10–§2.13 and budgets B-E and B-F |
| **V5** (wave 4) | SP-14, SP-15, SP-16 | §1.14–§1.16 in full, plus §2.14–§2.16 and the Phase-5, Phase-6 and Phase-7 criteria |
| **V6** (wave 5, this one) | SP-17, SP-18 | §1.17–§1.18, §2.17–§2.18, all of §3, and every gate of §4 |

There is nothing to skip and nothing to sample. Every row of §1 is executed against the release candidate, on this branch, at this commit.

### 6.2 The §11.3 2% no-regression guardrail — with zero sign-offs

> - No metric may regress by more than 2% to improve another without explicit sign-off

```bash
go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop --ci --json /tmp/v6-replay.json
```

The gate compares every metric in `eval.MetricsOf` for every policy against the recorded baseline, honouring `MetricDirection` and the zero-baseline absolute tolerance. At V6 the rule is stricter than at any previous checkpoint:

- **Zero `Regression` entries with `Allowed == false`.** As at every checkpoint.
- **Zero `Regression` entries with `Allowed == true`.** This is the difference. `sign-off:` exists so that a mid-project trade can be made deliberately and recorded; it is not available at the release gate. The report's "sign-offs used" cell must read `none`. A metric that regresses more than 2% at V6 is fixed under §9, or the release does not ship. A trailer added to `verify/v6` to carry a regression past this gate fails the gate by its presence.
- The 2% boundary is exclusive: −1.98% passes, −2.02% fails. Do not round.
- `TestGate_PhaseChecksMayNotBeDisabledInCI` must pass — `eval.replayOnPhaseGate` cannot be turned off to make this green.
- Every phase exit assertion, **Phases 0 through 7**, runs inside the gate. `--phase 7` must pass.

### 6.3 Metric deltas against the Phase-0 baseline

The §11.2 table, measured on the release candidate and compared to the committed `testdata/baseline/phase0.json`. This is the number the project has been aiming at since SP-02; V6 is where it is stated in one place.

| Metric (§11.2) | Direction | `phase0.json` (stock) | Release candidate | Δ | Verdict |
|---|---|---|---|---|---|
| Fraction of Belady OPT (§11.1) | higher better | | | | |
| First-divergence turn | higher better | | | | |
| File-set Jaccard | higher better | | | | |
| Redundant work rate | lower better | | | | |
| Decision preservation | higher better | | | | |
| Rewrite tokens / session | lower better | | | | |
| Rehydration budget | lower better | | | | |
| Retrieval hit rate | reported | | | | |
| Compaction pause (modelled) | lower better | | | | |
| Residual span at compaction | lower better | | | | |
| First-turn-after latency (modelled) | lower better | | | | |

Rules for this table: the two modelled latency rows carry `"latency": "modelled"` and `"pause_modelled": true` in their artifacts and are never presented as measured wall-clock; the retrieval hit rate is reported, not gated, because there is no baseline for a capability Phase 0 did not have; every other row must be at least as good as Phase 0 or inside the 2% band of §6.2, with no sign-off available.

If `$QOMPACK_SESSIONS_DIR` holds ≥ 20 recorded sessions, produce the same table a second time against `testdata/baseline/phase0-recorded.json` (§2.2 item 4) and label both tiers. If it does not, the report says **"recorded tier not available on this machine"** in this section as well as in §2.2 — the honesty discharge belongs where the numbers are read.

### 6.4 `benchstat` against the recorded baseline

```bash
go run ./tools/devtool bench > /tmp/v6-bench.txt
benchstat testdata/bench-baseline.txt /tmp/v6-bench.txt
```

- A micro-benchmark that regresses **> 25%** fails the build.
- One that regresses **> 10%** does not fail CI but requires a written explanation in the completion report naming the benchmark, the percentage and the cause. "Noise" is an acceptable cause only with a second run pasted beside the first.
- Additions are expected — SP-17 and SP-18 introduce `BenchmarkGenerate`, the packaging and fsck/doctor timings — and are appended to `testdata/bench-baseline.txt` in their own commit on `verify/v6`, never mixed with a fix.

### 6.5 Regression run order

1. §4.1's `ci-local` first — a lint or build failure invalidates everything after it.
2. §1's eighteen inventory groups, fanned out (§7).
3. §2's exit criteria, in the main session, in subplan order.
4. §3's integration tests, authored and committed.
5. §4.2's thirteen CI jobs on the final commit.
6. §5's budget sweep, serially, on an idle machine.
7. §6.2's replay gate, §6.3's metric table, §6.4's `benchstat`.
8. The UAT hand-run of §2.18 item 4, last, against the bundle that every gate above has now passed.
---

## 7. Subagent strategy

This checkpoint is the widest in the set — eighteen inventory groups, eighteen exit-criteria sections, fourteen new integration tests, forty-odd budgets and a manual acceptance run. It is also mostly parallel, because §1's groups share no files and produce independent evidence.

**Fan §1 out. Keep every seam, every number and every human judgement in the main session.**

**Main session only, never delegated:**

- The preflight of §0, the branch cut, and every commit. Subagents return command output, diffs and measured numbers — never commits.
- **Every integration test in §3.** They span four to six packages each, they join the permanent suite, and they are precisely where a subagent's local view writes a confident wrong assertion.
- The whole-tree gates of §4 — `-race`, coverage, the thirteen CI jobs, the invariant sweep, the residue checks.
- The budget sweep of §5. One machine, one configuration, one idle system, one set of numbers. Benchmarks delegated across agents are not comparable to each other and not comparable to `testdata/bench-baseline.txt`.
- The replay gate, the 2% rule and the metric-delta table of §6.
- **The UAT hand-execution of §2.18 item 4.** This never leaves the main session and is never delegated to a subagent under any circumstance. UAT is a human executing a written guide against a real project on a real machine; an agent reporting that it "ran the scenarios" is not a user-acceptance test, it is a claim about a claim. The operator runs the twelve scenarios, records the twelve verdicts in §8.9, and signs §8.10.
- The completion report, the merge, and the tags.

**Parallel subagents, one row per agent (split a row into two agents if the machine allows):**

| Subagent | Owns | Given | Returns |
|---|---|---|---|
| **V6-A** | §1.1 (SP-01, 28 rows) | this file, §1.1, §2.1 | pass/fail per row, the four SP-01 benchmark numbers, the exact text of any lint, guard or drift failure |
| **V6-B** | §1.2 (SP-02, 14 rows) + §1.3 (SP-03, 17 rows) | this file, §1.2–§1.3, §2.2–§2.3 | pass/fail per row, `policies.stock.fraction_of_opt`, E-1…E-5, the byte-comparison of the two baseline runs, `BenchmarkL0SketchUpdate` ns/op and allocs, empirical Bloom FP |
| **V6-C** | §1.4 (SP-04, 17 rows) | this file, §1.4, §2.4 | pass/fail per row, `testrunner` and overall gain, all eleven chunk/canon/symbol benchmark numbers, the three-OS golden comparison |
| **V6-D** | §1.5 (SP-05, 21 rows) | this file, §1.5, §2.5 | pass/fail per row, the 66-combination fault result, the contract producer table (must be 9/0, not 5/4), breach-detector transition and revert results |
| **V6-E** | §1.6 (SP-06, 21 rows) + §1.7 (SP-07, 14 rows) | this file, §1.6–§1.7, §2.6–§2.7 | pass/fail per row, `DedupRatio`, growth exponent, every store benchmark, both slice benchmarks, `CrossingEdges` µs, `thin-vs-full.json` size_ratio and recall |
| **V6-F** | §1.8 (SP-08, 15 rows) + §1.9 (SP-09, 13 rows) | this file, §1.8–§1.9, §2.8–§2.9 | pass/fail per row, both Phase-1 ratios, the canonicalization gap factor, the Phase-2 JSON (`stock_repeats`, `negknow_repeats`, `stale_blocks`), `Health()` at 8 000 records |
| **V6-G** | §1.10 (SP-10, 19 rows) + §1.11 (SP-11, 17 rows) | this file, §1.10–§1.11, §2.10–§2.11 | pass/fail per row, `Finalize` timings, residual P50 on/off, the three Phase-3 deltas, maximum `Result.Tokens` |
| **V6-H** | §1.12 (SP-12, 18 rows) + §1.13 (SP-13, 18 rows) | this file, §1.12–§1.13, §2.12–§2.13 | pass/fail per row, the four Phase-4 numbers, residual slope, B-F p95, the `tools/list` golden comparison |
| **V6-I** | §1.14 (SP-14, 10 rows) + §1.15 (SP-15, 15 rows) + §1.16 (SP-16, 13 rows) | this file, §1.14–§1.16, §2.14–§2.16 | pass/fail per row, the field-by-field `status --json` cross-check, ΔfractionOfOPT, first-warning turn, warm−cold deltas, segment-bloom FP |
| **V6-J** | §1.17 (SP-17, 20 rows) | this file, §1.17, §2.17 | pass/fail per row, the two-run archive comparison, every binary size, `fsck` and `doctor` timings, the `gosec`/`govulncheck` suppression list with reasons |
| **V6-K** | §1.18 (SP-18, 14 rows) | this file, §1.18, §2.18 items 1–3 and 5–10 | pass/fail per row, the owned-doc count, the 72/72 leaf and `Meta` counts, `BenchmarkGenerate` ns/op, the `go list -deps` output. **Item 4 is not given to this agent.** |

**Rules for subagents.**

1. A subagent runs commands and reports results. It **fixes nothing** unless the main session hands it a diagnosed defect, and even then it returns a diff, not a commit.
2. No subagent may run any command with `-update`, `QOMPACK_UPDATE_GOLDEN=1`, `--write-baseline`, `--write-report` or `--regen-corpus`. Regenerating a contract is the main session's decision under §9 class (d).
3. No subagent may edit `Qompack.md` or `plans/00-ARCHITECTURE.md`, for any reason.
4. No subagent runs a benchmark that feeds §5, and no subagent runs the UAT.
5. A subagent that finds a failure returns: the exact command, the full verbatim output, the packages on either side of the seam, and its classification under §9. It does not speculate about other groups' failures and does not "fix while it is in there".
6. Groups are dispatched together and collected together. The main session does not start §3 until every group has reported, because an integration test written over a broken component measures the break.

---

## 8. Completion report template

Fill this in as the checkpoint runs and paste it into the merge commit body (abridged) and into `docs/adr/0018-v6-release-verification.md` (in full). Every metric cell carries a **measured number**. A row with `PASS` and an empty metric cell is not a result.

### 8.1 Header

```
V6 verification — production readiness and user acceptance (release candidate)
develop commit:        <sha>     (merges: sp17 <sha>, sp18 <sha>, in that order)
verify/v6 commit:      <sha>
Run started:           <ISO-8601>      Run completed: <ISO-8601>
Platforms:             ubuntu-<ver> / macos-<ver> / windows-<ver>
Go toolchain:          go1.26.<x>
Bundle under test:     dist/plugin-<goos>-<goarch>, sha256 <hex>
Release version:       v<semver>
Fixes landed on verify/v6:                       <n commits>
Attribution-trailer grep over develop..verify/v6: <empty / FINDINGS>
Recorded-corpus tier:  <available: N sessions / "recorded tier not available on this machine">
```

### 8.2 Inventory results (§1)

One row per inventory ID. No collapsed ranges: a block written as "1.6.1 … 1.6.21 — all pass" is not a report.

**SP-01 — foundation, toolchain, contracts (28)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.1.1 | repository shape and root commit | | |
| 1.1.2 | module builds and vets | | |
| 1.1.3 | full lint suite | | |
| 1.1.4 | formatting | | |
| 1.1.5 | binary dependency closure | | |
| 1.1.6 | test-only dependency isolation | | |
| 1.1.7 | import-graph layer DAG | | |
| 1.1.8 | Appendix C verbatim | | |
| 1.1.9 | five-layer config precedence | | |
| 1.1.10 | validation falls back, never crashes | | |
| 1.1.11 | provenance and JSON Schema | | |
| 1.1.12 | append-only guard (5/5 refusals) | | |
| 1.1.13 | paths: atomic write, norm, long paths | | |
| 1.1.14 | core primitives and seven sentinels | | |
| 1.1.15 | logging and the Loud channel | | |
| 1.1.16 | obs histograms and budget IDs | | ns/op: |
| 1.1.17 | hookio codecs and `Extra` | | crashers: |
| 1.1.18 | CLI dispatch and exit-code policy | | 30 combos: |
| 1.1.19 | six hook entry points exit 0 | | |
| 1.1.20 | plugin manifest no-drift | | |
| 1.1.21 | config docs no-drift | | |
| 1.1.22 | six-target cross-build | | |
| 1.1.23 | contract monitor `ModeFull` on a fresh build | | |
| 1.1.24 | the four closing-note guards | | |
| 1.1.25 | conformance infrastructure, zero W-1 skips | | skip count: |
| 1.1.26 | testutil fixture | | |
| 1.1.27 | SP-01 baseline benchmarks | | ms/op ×3: |
| 1.1.28 | `Qompack.md` unmodified | | |

**SP-02 — replay harness, Belady OPT, Phase-0 baseline (14)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.2.1 | `internal/eval` suite under `-race` | | |
| 1.2.2 | `evaltest` conformance suite live | | |
| 1.2.3 | 24-session corpus hash-stable | | |
| 1.2.4 | Phase-0 baseline reproducible and tiered | | fraction_of_opt: |
| 1.2.5 | Belady ceiling and floor bracket every policy | | |
| 1.2.6 | five §4.2 divergence metrics | | |
| 1.2.7 | ten §11.2 secondary metrics | | |
| 1.2.8 | breakpoint OPT labelled not-plugin-actionable | | |
| 1.2.9 | `replay-gate` rules | | |
| 1.2.10 | every gate failure mode tested | | |
| 1.2.11 | redacting importer | | |
| 1.2.12 | eval benchmarks E-1…E-5 | | s / ms/op: |
| 1.2.13 | `eval` imports foundation only | | |
| 1.2.14 | coverage ≥ 85% | | actual: |

**SP-03 — sketch library (17)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.3.1 | five sketches implement `Sketch` | | |
| 1.3.2 | Bloom Appendix A sizing | | m/k: |
| 1.3.3 | Count-Min Appendix A sizing | | w/d: |
| 1.3.4 | HyperLogLog sizing and error | | |
| 1.3.5 | measured Bloom FP rate | | empirical: |
| 1.3.6 | resize policy and saturation alarm | | |
| 1.3.7 | `RebuildBloom` from an arbitrary iterator | | ms @ 5 000 keys: |
| 1.3.8 | `MergeFrom` / `Scale` | | |
| 1.3.9 | Misra-Gries `Top(n)` | | |
| 1.3.10 | MinHash and the near-dup threshold | | |
| 1.3.11 | `tried.bloom` generational replacement | | |
| 1.3.12 | corruption detection is loud and typed | | |
| 1.3.13 | frozen v1 wire format | | |
| 1.3.14 | frame-size guards | | |
| 1.3.15 | five fuzz targets clean | | crashers: |
| 1.3.16 | hot-path sketch update, zero alloc | | µs/op, allocs: |
| 1.3.17 | imports and coverage | | actual: |

**SP-04 — FastCDC, canonicalizers, symbols (17)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.4.1 | `Split` / `SplitStream` at three sizes | | |
| 1.4.2 | boundary stability under insertion | | |
| 1.4.3 | cross-platform determinism (3 OSes, same goldens) | | |
| 1.4.4 | domain-separated Merkle `RootHash` | | |
| 1.4.5 | canonicalizer registry order (14) | | |
| 1.4.6 | idempotence, per canonicalizer | | |
| 1.4.7 | byte-exact inverse | | |
| 1.4.8 | no canonicalizer grows its input | | |
| 1.4.9 | every rule emits a classed `Match` | | |
| 1.4.10 | MinHash signature on every `Run` | | |
| 1.4.11 | with-vs-without canonicalization measurement | | testrunner / overall gain: |
| 1.4.12 | symbol extraction | | |
| 1.4.13 | `Enclosing` minimal-sufficient span | | |
| 1.4.14 | chunk/canon/symbol benchmarks | | eleven numbers: |
| 1.4.15 | fuzz targets clean | | crashers: |
| 1.4.16 | import discipline and coverage | | actual: |
| 1.4.17 | corpus hygiene | | |

**SP-05 — daemon, IPC, hot path, contract monitor (21)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.5.1 | IPC address resolution per platform | | |
| 1.5.2 | NDJSON framing, 1 MiB limit, ACK/NAK | | |
| 1.5.3 | `Client.Send` never errors; spools | | |
| 1.5.4 | `ipctest` conformance suite live | | |
| 1.5.5 | singleton lock and stale reclamation | | |
| 1.5.6 | lazy detached spawn | | |
| 1.5.7 | WAL-backed ingest queue | | |
| 1.5.8 | `Drain` idempotent over spool + WAL | | |
| 1.5.9 | idle controller and idle exit | | |
| 1.5.10 | config hot reload | | |
| 1.5.11 | extension seams tolerate a nil `Services` | | |
| 1.5.12 | B-A / B-B on the real binary, three platforms | | p99 ×3: |
| 1.5.13 | overrun degradation is an observable transition | | |
| 1.5.14 | nine contract assertions at `SessionStart` | | |
| 1.5.15 | producer-presence rule (zero not-yet-implemented) | | |
| 1.5.16 | degradation semantics | | |
| 1.5.17 | restoration after two clean runs | | |
| 1.5.18 | session-start marker from terminal hooks only | | |
| 1.5.19 | hooks exit 0 under every injected fault | | 66 combos: |
| 1.5.20 | transport security posture | | |
| 1.5.21 | coverage floors | | actual: |

**SP-06 — store, redaction, tokens (21)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.6.1 | object layout and two-level fanout | | |
| 1.6.2 | global dedup across the project | | |
| 1.6.3 | canonicalize-before-chunk ordering | | |
| 1.6.4 | redaction at the single choke point | | |
| 1.6.5 | ten ordered redaction rules | | |
| 1.6.6 | `tool_use` index and preview | | |
| 1.6.7 | supersession marking | | |
| 1.6.8 | file version history and point-in-time lookup | | |
| 1.6.9 | `ChangedSince([]core.Dep)` | | |
| 1.6.10 | `Search` backing `recall` | | |
| 1.6.11 | `OpenSpan` on chunk boundaries | | |
| 1.6.12 | segment log with the encoded-once DPI guard | | |
| 1.6.13 | `Frontier` / `Unencoded` | | |
| 1.6.14 | mark-and-sweep GC, deadline-bounded | | |
| 1.6.15 | `DedupRatio` responds to the toggle | | on/off: |
| 1.6.16 | Phase-1 dedup ratio, read-heavy | | ratio: |
| 1.6.17 | sublinear store growth | | exponent: |
| 1.6.18 | exact chunk-level token accounting | | |
| 1.6.19 | store benchmarks | | cold/warm: |
| 1.6.20 | conformance suites live | | |
| 1.6.21 | import discipline and coverage | | actual: |

**SP-07 — dependence DAG and slicing (14)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.7.1 | nine node kinds, eight edge kinds | | |
| 1.7.2 | `dag/deps.jsonl` byte-exact round-trip | | |
| 1.7.3 | torn tail and corrupt line tolerated, surfaced | | |
| 1.7.4 | idle-only `Compact` | | |
| 1.7.5 | slices return scores, never keep/drop | | |
| 1.7.6 | thin slicing is the default | | |
| 1.7.7 | measured thin-vs-full tradeoff | | size_ratio / recall: |
| 1.7.8 | `CrossingEdges` = `segment_coupling(p)` | | µs @ 15 000 edges: |
| 1.7.9 | `NodesAfter` total order | | |
| 1.7.10 | slice latency budget | | ms back / fwd: |
| 1.7.11 | builder output acyclic | | |
| 1.7.12 | concurrency safety, no lock upgrades | | |
| 1.7.13 | golden fixtures reproduced | | |
| 1.7.14 | conformance, imports, coverage | | actual: |

**SP-08 — observer L0 (15)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.8.1 | `OnToolUse` 14-step pipeline | | |
| 1.8.2 | addressable tombstone | | ns/op: |
| 1.8.3 | supersession marks the earlier read | | |
| 1.8.4 | DAG edges recorded | | |
| 1.8.5 | CMS and HLL fed; Bloom never fed | | grep count: |
| 1.8.6 | verbatim, immutable user-prompt capture | | |
| 1.8.7 | subagent capture with tool-result hashes | | |
| 1.8.8 | task-boundary signals | | |
| 1.8.9 | `SessionStart` / `SessionEnd` ordering | | |
| 1.8.10 | Phase-1 dedup half | | ratio: |
| 1.8.11 | canonicalization gap on test output | | on/off: |
| 1.8.12 | sublinear growth with the observer wired | | exponent: |
| 1.8.13 | observer-side latency | | ms: |
| 1.8.14 | `observertest` suite, zero skips | | |
| 1.8.15 | import discipline and coverage | | actual: |

**SP-09 — negative knowledge (13)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.9.1 | four-field canonical descriptor | | |
| 1.9.2 | append-only `eliminations.jsonl` is the truth | | |
| 1.9.3 | three-way `already_tried` answer | | |
| 1.9.4 | stale note byte-identical to §8.3 | | |
| 1.9.5 | bloom-only hits never masquerade | | |
| 1.9.6 | `RefreshStaleness` via `ChangedSince` | | |
| 1.9.7 | `RebuildBloom` from active records only | | fill / estFP @ 8 000: |
| 1.9.8 | `scope: session \| project` semantics | | |
| 1.9.9 | heuristic `Detector` over the DAG | | |
| 1.9.10 | `Health()` surfaces fill and FP rate | | |
| 1.9.11 | Phase-2 exit criterion | | stock / negknow / stale_blocks: |
| 1.9.12 | performance budgets | | |
| 1.9.13 | conformance, imports, coverage, write set | | actual: |

**SP-10 — checkpointer L4 and pins (19)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.10.1 | §8.5 schema verbatim, importance-ordered | | |
| 1.10.2 | `Begin` / `Advance` / `Finalize` / `Abort` | | |
| 1.10.3 | `SourceSet` cannot carry live context text | | |
| 1.10.4 | `Advance` is DPI-guarded | | |
| 1.10.5 | `Finalize` writes an immutable artifact | | |
| 1.10.6 | `Reader` and `Verify` with MANIFEST re-hash | | |
| 1.10.7 | `Truncate` tier order | | |
| 1.10.8 | no code snippets in checkpoints | | |
| 1.10.9 | `ExtractDecisions` sole producer of `DecisionID` | | |
| 1.10.10 | `ValidatePointers` against the working tree | | |
| 1.10.11 | `FocusInstructions` verbatim + O1 paragraph | | |
| 1.10.12 | injection tagging and `StripInjections` | | |
| 1.10.13 | `pins` append-only log with tombstones | | |
| 1.10.14 | `PreCompact` writes a real checkpoint | | |
| 1.10.15 | independent cadence | | |
| 1.10.16 | budget B-E on the `PreCompact` hook | | p99 ×3: |
| 1.10.17 | checkpoint micro-benchmarks | | ms ×3: |
| 1.10.18 | residual-span reduction from frontier advance | | P50 on/off: |
| 1.10.19 | conformance, imports, coverage | | actual: |

**SP-11 — rehydrator L5, rules, skills (17)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.11.1 | eight-item injection in §8.6 order | | |
| 1.11.2 | item 2 is the L0 verbatim capture | | |
| 1.11.3 | eliminations digest + standing instruction | | |
| 1.11.4 | pointers, not contents | | |
| 1.11.5 | drop report and its persistence | | |
| 1.11.6 | path-scoped rules restored | | |
| 1.11.7 | nested `CLAUDE.md` restored | | |
| 1.11.8 | skill index within budget, no literal 450 | | tokens: |
| 1.11.9 | injection tagging carries the seq | | |
| 1.11.10 | budget discipline 8–12K | | max tokens: |
| 1.11.11 | checkpoint-as-fallback when the summary fails | | |
| 1.11.12 | `SessionStart` compact and clear branches | | |
| 1.11.13 | degraded-passive emits nothing | | |
| 1.11.14 | `Reporter` satisfies `mcp.DropReporter` | | |
| 1.11.15 | Phase-3 exit criterion | | A1 / A2 / A3: |
| 1.11.16 | L5 latency budgets | | four p99s: |
| 1.11.17 | conformance, imports, coverage | | actual: |

**SP-12 — scheduler L3 (18)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.12.1 | `Evaluate` is a pure function of `Inputs` | | |
| 1.12.2 | composite trigger, all four clauses | | |
| 1.12.3 | soft floor and hard ceiling from config | | |
| 1.12.4 | p-selection argmax and `Breakdown` | | |
| 1.12.5 | sliding-TTL idle model on last API call | | |
| 1.12.6 | Young–Daly interval with measured δ | | |
| 1.12.7 | BOCD with pruning and normalization | | |
| 1.12.8 | droppable-block ranking | | |
| 1.12.9 | six registered idle tasks | | |
| 1.12.10 | the acting idle task suppressed when degraded | | |
| 1.12.11 | frontier advancement drives `Advance` | | |
| 1.12.12 | persistence and self-healing | | |
| 1.12.13 | event path proven end to end | | |
| 1.12.14 | scheduler is off the hot path | | B-A delta: |
| 1.12.15 | `PSelectionAvailable()` gate, both sides | | |
| 1.12.16 | Phase-4 exit criterion | | four numbers: |
| 1.12.17 | scheduler benchmarks | | µs/ms: |
| 1.12.18 | conformance, imports, coverage | | actual: |

**SP-13 — MCP retrieval layer L6 (18)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.13.1 | JSON-RPC 2.0 stdio server | | |
| 1.13.2 | eight tools registered with schemas | | |
| 1.13.3 | `recall` against the real store | | |
| 1.13.4 | `expand` re-materializes a cleared result | | |
| 1.13.5 | `re_read` current and historical | | |
| 1.13.6 | `already_tried` three-way | | |
| 1.13.7 | `record_eliminated` writes through the ledger | | |
| 1.13.8 | `timeline` | | |
| 1.13.9 | `why` against real `ExtractDecisions` output | | |
| 1.13.10 | `dropped` against the real reporter | | |
| 1.13.11 | ephemeral-at-birth on the seven retrieval tools | | |
| 1.13.12 | minimal span default, `full=true` escape | | |
| 1.13.13 | promoter counts expansions | | |
| 1.13.14 | `mcp.server_registered` is a real observation | | |
| 1.13.15 | panic isolation and hostile input | | crashers: |
| 1.13.16 | budget B-F | | p95 ×3: |
| 1.13.17 | docs generated from the tool table | | |
| 1.13.18 | conformance, imports, coverage | | actual: |

**SP-14 — slash commands and observability (10)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.14.1 | seven commands in §7.5 order | | |
| 1.14.2 | `/qompack:status` renders all eleven sections | | |
| 1.14.3 | no second implementation of retrieval | | |
| 1.14.4 | `pin` and `pin --eliminated` | | |
| 1.14.5 | `checkpoint-now` honours the DPI guard | | |
| 1.14.6 | `eval` reports OPT, secondaries, regressions | | |
| 1.14.7 | uniform `--json` envelope and `--help` | | |
| 1.14.8 | deterministic, ANSI-free rendering | | |
| 1.14.9 | manifest ≡ binary ≡ docs | | |
| 1.14.10 | conformance, benchmarks, coverage | | ms: |

**SP-15 — analyzer selection and grammar (15)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.15.1 | cheap Δ-scorer | | |
| 1.15.2 | Δ-scoring ranks negative knowledge highest | | |
| 1.15.3 | `DetectRedundancy` | | |
| 1.15.4 | suffix constraint is structural | | |
| 1.15.5 | ship-order guard inert without p-selection | | |
| 1.15.6 | submodular lazy greedy meets `(1 − 1/e)` | | |
| 1.15.7 | ephemeral results rank first for eviction | | |
| 1.15.8 | Sequitur invariants, online and linear | | |
| 1.15.9 | thrash detection and warning delivery | | |
| 1.15.10 | versioned CRC-checked `actions.seq` | | |
| 1.15.11 | grammar folded into the checkpoint | | |
| 1.15.12 | Phase-5 exit criterion | | ΔfractionOfOPT: |
| 1.15.13 | Phase-6 exit criterion | | first-warning turn: |
| 1.15.14 | analyzer and grammar benchmarks | | |
| 1.15.15 | conformance, imports, coverage | | actual: |

**SP-16 — Phase-7 refinements (13)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.16.1 | CMS decay-and-merge warm start | | |
| 1.16.2 | warm start honours config both ways | | |
| 1.16.3 | project eliminations carried forward | | |
| 1.16.4 | BOCD feature priors seeded | | |
| 1.16.5 | O4 measured benefit | | warm−cold ΔOPT / Δdiv: |
| 1.16.6 | demand-driven promotion into the pointer tier | | |
| 1.16.7 | per-segment Bloom filters | | m/k, measured FP: |
| 1.16.8 | ski-rental threshold computed, never literal | | |
| 1.16.9 | truncation reserves at the measured argmax | | |
| 1.16.10 | declared non-delivery of prefix reordering | | |
| 1.16.11 | Phase-7 benchmarks | | |
| 1.16.12 | artifacts reproducible without the golden flag | | |
| 1.16.13 | coverage floors preserved | | actual: |

**SP-17 — packaging, hardening, release (20)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.17.1 | deterministic bundle assembly, two runs | | sha256 match: |
| 1.17.2 | bundle layout per §3.4 | | |
| 1.17.3 | binary size budget | | MiB per target: |
| 1.17.4 | universal launcher shims | | |
| 1.17.5 | launcher overhead accounted inside B-D | | p99 POSIX / Windows: |
| 1.17.6 | per-platform bundle pays no launcher cost | | B-A / B-E ×3: |
| 1.17.7 | `install-gate` on three OSes | | |
| 1.17.8 | clean uninstall | | |
| 1.17.9 | upgrade safety | | |
| 1.17.10 | cross-platform matrix | | Defender arm: |
| 1.17.11 | hooks exit 0 under the 54-case fault matrix | | |
| 1.17.12 | security audit, three independent proofs | | |
| 1.17.13 | `govulncheck` and `gosec` | | suppressions: |
| 1.17.14 | every §12.3 row driven and documented | | 10/10: |
| 1.17.15 | `qompack fsck` | | 200 k pass: |
| 1.17.16 | `qompack doctor` | | s with / without MCP: |
| 1.17.17 | version and drift guard | | version: |
| 1.17.18 | release pipeline dry run | | |
| 1.17.19 | the four new CI jobs are required checks | | |
| 1.17.20 | checksum file format | | |

**SP-18 — documentation and UAT (14)**

| ID | Functionality | Result | Measured value / evidence |
|---|---|---|---|
| 1.18.1 | twenty-two owned documents | | count: |
| 1.18.2 | config reference generated, never stale | | 72/72: |
| 1.18.3 | ranges agree with `Validate()` | | |
| 1.18.4 | `runtime` namespace marked additive | | |
| 1.18.5 | cannot-do list reproduced verbatim | | 8 bullets / 15 residuals: |
| 1.18.6 | upstream issues and tracker template | | |
| 1.18.7 | Loud glossary covers IDs and failure modes | | 9 / 10: |
| 1.18.8 | ADRs D1–D12 present and structural | | |
| 1.18.9 | architecture digest quotes the ten invariants | | |
| 1.18.10 | hygiene: placeholders, links, whitespace | | grep result: |
| 1.18.11 | twelve UAT scenarios structurally conformant | | |
| 1.18.12 | **UAT executed end to end by a human** | | see §8.9 |
| 1.18.13 | generator determinism and B-DOC | | ms/op: |
| 1.18.14 | doc-package import discipline | | packages: |

### 8.3 Exit criteria (§2)

| § | Subplan | Criterion (short) | Threshold | Measured | Verdict |
|---|---|---|---|---|---|
| 2.1 | SP-01 | guardrail mechanisms exist and bind | required checks, no literals | | |
| 2.2 | SP-02 | one reproducible stock number | ≥ 20 sessions, byte-identical reruns | | |
| 2.3 | SP-03 | contributions to 4:1 and 15 ms | ≤ 5 µs, 0 allocs | | |
| 2.4 | SP-04 | measure with and without canonicalization | gain ≥ 1.25 / ≥ 1.0; Split < 800 µs | | |
| 2.5 | SP-05 | hook p99 with the finished system attached | B-A < 15 ms, B-B < 2 ms, B-E < 2 s ×3 | | |
| 2.6 | SP-06 | dedup ratio and sublinear growth | ≥ 4.0; exponent < 1.0 | | |
| 2.7 | SP-07 | sub-millisecond slicing, scores not keep/drop | < 1 ms; size_ratio ≤ 0.75, recall ≥ 0.85 | | |
| 2.8 | SP-08 | Phase 1 closed | ratio ≥ 4.0, gap ≥ 1.25×, sublinear | | |
| 2.9 | SP-09 | Phase 2 closed | repeats ≤ 0.75×, stale blocks == 0 | | |
| 2.10 | SP-10 | residual-span reduction, B-E | P50 ≥ 30% below, < 2 s | | |
| 2.11 | SP-11 | Phase 3 closed | A1, A2, A3; ≤ 12 000 tokens | | |
| 2.12 | SP-12 | Phase 4 closed | rewrite ≤ 0.80×, slope ≤ 0.02, P95 ≤ 20 000 | | |
| 2.13 | SP-13 | Phase 2 surfaced, B-F, no re-inflation | stale never active; p95 < 250 ms | | |
| 2.14 | SP-14 | the surfaces report the real numbers | field-by-field agreement | | |
| 2.15 | SP-15 | Phases 5 and 6 closed | Δ ≥ 0.02 subsets; warn ≤ 3rd repetition | | |
| 2.16 | SP-16 | Phase 7 measured | warm ≥ cold on both metrics | | |
| 2.17 | SP-17 | fifteen Definition-of-Done items | all fifteen | | |
| 2.18 | SP-18 | the guide is executable exactly as written | zero doc defects; twelve verdicts | | |

### 8.4 New integration tests (§3)

| Test | Result | Measured value / note |
|---|---|---|
| `TestV6_PackagedBundleObservesARealSessionEndToEnd` | | tool_uses=, DedupRatio= |
| `TestV6_UniversalLauncherPreservesHookSemantics` | | launcher p99= |
| `TestV6_DocumentedCommandsRunAgainstTheShippedBundle` | | subcommands checked= |
| `TestV6_EliminationStalenessSurvivesPackagingAndAnswersThroughMCP` | | stale_blocks= |
| `TestV6_EphemeralRetrievalResultsAreEvictedFirst` | | evicted-first fraction= |
| `TestV6_FsckRepairsSeededCorruptionWithoutLosingLiveData` | | 9/9 repaired= |
| `TestV6_DoctorAgreesWithStatusAndWithTheUnderlyingSubsystems` | | fields compared= |
| `TestV6_ConfigReferenceDescribesTheBinaryThatShips` | | keys= |
| `TestV6_InstallUpgradeUninstallLeavesTheProjectByteIdentical` | | |
| `TestV6_DegradedPassiveIsCorrectFromThePackagedBundle` | | |
| `TestV6_NoSecretAndNoNetworkAcrossAFullPackagedSession` | | canary connections= |
| `TestV6_CheckpointToRehydrationRoundTripThroughTheBundle` | | injected tokens= |
| `TestV6_ReleaseArtifactsAreReproducibleAndSelfConsistent` | | archives compared= |
| `TestV6_HotPathHoldsWithEverySubsystemResidentInTheBundle` | | B-A p99= |

### 8.5 Whole-tree gates and invariants (§4)

| Gate | Result | Note |
|---|---|---|
| `ci-local` (fmt, lint, vet, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips, build) | | |
| `go test ./... -race` (ubuntu, macos) | | |
| `go test ./... -count=2` (windows) | | |
| `cover` — every §6.4 floor | | lowest package and %: |
| The thirteen CI jobs on the final `verify/v6` commit | | 13/13: |
| The ten §13 invariants (§4.4) | | 10/10: |
| Residue checks (§4.5) | | skips / sentinels / not-yet-implemented: |
| `Qompack.md` and `00-ARCHITECTURE.md` untouched | | |
| Attribution-trailer scan | | |

### 8.6 Performance budgets (§5)

Paste the completed §5.2 table here, every cell filled, plus the two `benchstat` notes required by §6.4.

### 8.7 Regression (§6)

| Item | Result | Note |
|---|---|---|
| V1 inventory re-run in full (§1.1) | | |
| V2 inventory re-run in full (§1.2–§1.7) | | |
| V3 inventory re-run in full (§1.8–§1.9) | | |
| V4 inventory re-run in full (§1.10–§1.13) | | |
| V5 inventory re-run in full (§1.14–§1.16) | | |
| §11.3 2% rule — disallowed regressions | | must be `none` |
| §11.3 2% rule — sign-offs used | | **must be `none` at this gate** |
| §11.2 metric deltas vs `phase0.json` (§6.3) | | table attached |
| Recorded-tier baseline | | number, or "recorded tier not available on this machine" |
| `benchstat` regressions > 10% | | each explained: |
| `benchstat` regressions > 25% | | must be `none` |

### 8.8 Findings and fixes

| # | Finding | Class (a–e per §9) | Root cause | Fix commit | Re-run result |
|---|---|---|---|---|---|
| 1 | | | | | |

### 8.9 UAT verdicts (§2.18 item 4)

Twelve verdicts, hand-executed, one row each. Platform and date are per row because a scenario re-run after a documentation fix is a new execution.

| Scenario | Date | Platform | Verdict | Evidence attached | Notes |
|---|---|---|---|---|---|
| UAT-01 — install and first session | | | | | |
| UAT-02 — observation and deduplication | | | | | |
| UAT-03 — hot-path latency | | | | | |
| UAT-04 — a real compaction writes a checkpoint | | | | | |
| UAT-05 — rehydration after compaction | | | | | |
| UAT-06 — instruction restoration and drop report | | | | | |
| UAT-07 — retrieval is ephemeral and minimal | | | | | |
| UAT-08 — recording and querying an elimination | | | | | |
| UAT-09 — staleness flip | | | | | |
| UAT-10 — scheduler behaviour | | | | | |
| UAT-11 — degradation drill (both halves) | | | | | |
| UAT-12 — privacy and uninstall | | | | | |

Every verdict is `pass` or `fail`. There is no `partial`, no `n/a` and no `not run`: §2.18 item 11 makes an unmet Pass bullet a fail, and a scenario that was not executed leaves the release gate unmet rather than the cell empty.

### 8.10 Sign-off

```
[ ] Every inventory row in §1 executed and recorded — no row skipped, no row summarized.
[ ] Every exit criterion in §2 re-measured against the merged develop, including SP-17's fifteen
    Definition-of-Done items and SP-18's docs suites.
[ ] All fourteen §3 integration tests authored, passing, and committed to verify/v6.
[ ] Every §4 gate green, including the thirteen CI jobs on the final commit and all ten invariants.
[ ] Every §5 budget measured on all three platforms and written down — no blank cell.
[ ] §6 regression green: V1–V5 inventories re-run, zero disallowed regressions, zero sign-offs used.
[ ] Twelve UAT verdicts recorded in §8.9, all pass, all hand-executed by a person.
[ ] Zero t.Skip in any conformance suite; zero not-yet-implemented contract assertions.
[ ] Zero attribution trailers in develop..verify/v6.

Verified by: ____________________   Date: ____________
```
---

## 9. Failure protocol

Any failure — a red test, a missed budget, an unmet exit criterion, a coverage floor, a lint finding, a golden mismatch, a surviving `t.Skip`, a `not-yet-implemented` contract assertion, a documentation defect discovered during the UAT run, a scenario verdict of `fail` — stops the checkpoint. **Nothing is released.** V6 is terminal: there is no next wave to defer the fix into, so "we will fix it in wave 6" is not an available sentence.

### 9.1 Diagnose systematically. Do not guess.

- Reproduce with the narrowest possible command (`go test ./internal/<pkg>/ -run <ExactTestName> -v`, or the single `hooks.json` command string, or the one UAT step) and capture the output **verbatim**. A paraphrased failure is a lost failure.
- Establish **when** it broke: `git bisect` between the pre-wave-5 tag `v0.4.0` and HEAD, or, faster for two branches, check out each wave-5 merge commit's first parent in turn.
- Classify before touching code. The classes are the ones the completion report §8.8 asks for:
  - **(a) Merge artifact.** A three-way merge dropped or reordered one of two blocks at a shared site. SP-17 and SP-18 are disjoint by construction — SP-17's Done checklist asserts it — so a collision here is itself the finding. Restore both blocks in the documented order and say which merge produced it.
  - **(b) Genuine integration defect.** Two subplans individually correct and jointly wrong; at wave 5 the common shape is "the code is right and the *package* is wrong" — a bundle that omits a file, a launcher that swallows an exit code, a document that describes last month's flag. Fix at the seam and add the integration test that would have caught it to §3. A wave-5 defect with no new §3 test is a fix that will be re-made later.
  - **(c) Latent defect in an earlier wave, exposed by real load.** V6 is the first time every layer runs at once, from an installed artifact, on a user's machine. Fix it in the owning package, not in the packaging that revealed it.
  - **(d) A stale fixture or baseline.** Regenerating a golden is legitimate **only** when the implementation change is intended and the golden is the thing that is wrong. Rule W-2: *"Any fixture that the real implementation cannot reproduce is a verification failure, not a fixture bug."* Never run `-update` or `QOMPACK_UPDATE_GOLDEN=1` to turn red green. Regenerate only after writing down why the new bytes are correct, in its own commit, with that reason in the body.
  - **(e) An architectural interface is wrong.** Do not work around it. Open `arch/<short-reason>` off `develop`, amend `00-ARCHITECTURE.md` §5, merge it, and rebase `verify/v6`. At the release gate this is expensive and rare; that it is expensive is not a reason to paper over it.
- A documentation defect found during the UAT run is class (b) or (c) and is fixed in the document, on `verify/v6`, followed by re-execution of that scenario from its first step (§2.18 item 11). It is never recorded as a workaround, an "operator note", or a footnote in the guide.
- Write the diagnosis down before the fix. A fix whose commit body cannot say *why* the code was wrong is a guess wearing a commit message.

### 9.2 Fix on the verification branch

All fixes land on `verify/v6` — cut from `develop` in §0, never from a subplan branch and never on `develop` directly — as **small conventional commits, as many as needed**, never one large one. Each commit compiles and passes `go run ./tools/devtool test` for the packages it touches. Format per 00-ARCHITECTURE §10:

```
<type>(<scope>): <subject>

<body — why the code was wrong, not what the diff does>

Refs: V6, SP-NN, <gap ids>, <Qompack.md sections>
```

`type` ∈ `feat fix docs test refactor perf build ci chore revert`; `scope` is the Go package or the subplan slug; the subject is imperative, ≤ 72 characters, no trailing period.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

That rule is verbatim and absolute. It applies to every commit, merge commit, tag message and PR body in this repository, including the release tag. CI's `verify` job greps the commit range for `Co-Authored-By`, `Signed-off-by`, `Generated with` and `🤖` and fails the build if any appear, and §10 checks the range again by hand before the release merge.

Do not weaken a test, a threshold or an assertion to make a row pass. A failing budget is a bug in the code or an honest re-measurement recorded as such — never a lowered number, and at this checkpoint never a sign-off (§6.2).

### 9.3 Re-run the whole checkpoint from the top

After **any** fix, re-run §1 through §6 in full — not just the failing test, not just its package. A fix in `store` invalidates every measurement above it; a fix in the bundle assembler invalidates every number produced from the bundle; a fix in a document invalidates the doc suite and the scenario that reads it. The point of an exhaustive final checkpoint is that its result is a statement about the whole tree at **one commit**, and a partial re-run cannot make that statement.

The UAT of §2.18 item 4 is re-run last, after the re-run of §1–§6 is green, against the bundle rebuilt from the fixed tree. A UAT verdict recorded against a bundle that no longer exists is not evidence.

### 9.4 What "stop" means here

At V1 through V5, a failure delayed the next wave. At V6 there is no next wave: a failure delays **the release**. Concretely, until every row is green:

- `develop` is not merged into `main`.
- No release tag is created, and `release-guard` must continue to refuse one.
- No bundle is published, attached to a release, or handed to a user for testing.
- The tag `v0.5.1` is not applied to `develop` either — the verification tag says the checkpoint passed, and it has not.

---

## 10. Gate — the release

This is the last gate in the plan set. Every box below is ticked with evidence in hand, in order.

- [ ] Every row of §1 (all 304 inventory items, eighteen groups) is PASS, recorded in §8.2 with its measured value.
- [ ] Every exit criterion in §2 is re-measured on `verify/v6` and PASS, including SP-17's **fifteen** Definition-of-Done items (§2.17) and SP-18's documentation suites (§2.18).
- [ ] All fourteen §3 integration tests are authored, green, and committed to `verify/v6`.
- [ ] Every gate of §4 is green: `ci-local`, `-race`, `-count=2`, every §6.4 coverage floor, all ten §13 invariants, and every residue check (zero `t.Skip`, zero `ErrNotImplemented` outside `internal/core`, **zero contract assertions reporting `not-yet-implemented`**).
- [ ] Every cell of the §5.2 budget table is filled with a measured number on all three platforms, and every hard budget passes: **B-A p99 < 15 ms**, **B-B p99 < 2 ms**, **B-E p99 < 2 s**, **B-F p95 < 250 ms**, launcher p99 **< 4 ms POSIX / < 15 ms Windows**, B-D reported and not gated.
- [ ] §6 regression is green: the V1, V2, V3, V4 and V5 inventories re-run **in full**, the §11.3 2% rule with **zero disallowed regressions and zero sign-offs used**, the §11.2 metric-delta table against `phase0.json` complete, and `benchstat` showing no regression above 25% with every regression above 10% explained.
- [ ] All thirteen CI jobs are green on the **final** `verify/v6` commit: `verify`, `test` ×3, `cover`, `crossbuild`, `bench-gate` ×3, `replay-gate`, `plugin-validate`, `security`, `docs`, `package-gate`, `platform-matrix`, `fault-gate`, `install-gate` — with the last four confirmed as required checks on `develop` and `main`.
- [ ] **UAT-01 through UAT-12 are recorded in §8.9 as `pass`, every one hand-executed by a person against a real project using the packaged bundle**, with the platform named, the evidence artifacts attached, and no scenario marked partial, skipped or delegated.
- [ ] `git log develop..verify/v6 --format=%B | grep -iE 'co-authored-by|signed-off-by|generated with|🤖'` is empty — no `Co-Authored-By`, `Signed-off-by`, `Generated with` or 🤖 anywhere in the range, in any commit, merge commit, tag message or PR body.
- [ ] `git diff develop -- Qompack.md` is empty, and `plans/00-ARCHITECTURE.md` is unmodified (or carries exactly one reviewed `arch/` amendment, named in §8.5).
- [ ] The completion report §8 is filled in, pasted into the merge commit body (abridged) and committed in full as `docs/adr/0018-v6-release-verification.md`.

**Then, and only then, merge and tag:**

```bash
git checkout develop
git merge --no-ff verify/v6 -m "chore: verification checkpoint V6

<one line per fix landed, or 'no fixes required'>
UAT: 12/12 pass, <platform>, <date>

Refs: V6, SP-17, SP-18"
git tag v0.5.1
```

Re-run `go run ./tools/devtool ci-local` and the replay gate **on `develop` after the merge**, because a `--no-ff` merge can still surface a semantic conflict neither parent had. Then release, per 00-ARCHITECTURE §9 (*"`main` only ever receives merges from `develop` at release tags"*) and its tag scheme (*"`v0.<wave>.<n>` on `develop` after each verification; `v<semver>` on `main` at release (SP-17)"*):

```bash
git checkout main
git merge --no-ff develop -m "release: v<semver>

Refs: V6, SP-17"
go run ./tools/devtool verify-version --tag v<semver>
go run ./tools/devtool changelog --check
go run ./tools/devtool release-guard --tag v<semver> --require-branch main
git tag v<semver>          # the release semver SP-17's tag-triggered pipeline consumes
git push origin main develop --tags
```

- [ ] `verify/v6` merged into `develop` with `--no-ff`; `ci-local` and `replay-gate` re-run green on `develop` after the merge; `develop` tagged `v0.5.1`.
- [ ] `develop` merged into `main` with `--no-ff` and `main` tagged with the release semver; `release-guard --tag <tag> --require-branch main` clean; the release workflow's artifacts match `docs/release.md` and every archive matches its checksum.
- [ ] The release tag message carries no attribution trailer either.

**Wave 5 is the last wave in the §14 map, so this gate does not cut anything.** Every previous checkpoint ended by authorizing the next wave's branches; this one ends by authorizing a release. There is no `feat/sp19-*`, no V7, and nothing held back for a later wave — the fourteen §3 tests, the §5 numbers and the twelve UAT verdicts are the whole statement.

When every box above is ticked, the statement this checkpoint makes is exact and may be written down without hedging: **every functionality in the codebase has been verified against the merged `develop`, every budget in the project has been measured on all three platforms and holds, every phase exit criterion from Phase 0 through Phase 7 has been re-measured on the release candidate, and a person has installed the shipped bundle and executed all twelve acceptance scenarios successfully. The project is production-ready for user testing.**
