# V1 — Verification checkpoint 1: foundation and contracts

**Wave verified:** 0 (SP-01 only) | **Branch:** `verify/v1`, cut from `develop` | **Tag on success:** `v0.0.1` on `develop`

---

## 0. When this runs, and what it is

This is a **standalone prompt**. Execute it exactly as written; do not assume any context from the
session that built SP-01.

**Preconditions — verify these before doing anything else.**

Wave 0 contains exactly one subplan, so the merge order of `00-ARCHITECTURE.md` §9 / §14 for this
wave is a single merge:

1. `feat/sp01-foundation-toolchain-and-contracts` → `develop`, with `--no-ff`.

Confirm it has landed:

```bash
git switch develop
git log --oneline --graph --decorate -n 20
git log --merges --oneline -n 5
```

Expected: a `--no-ff` merge commit whose second parent is the tip of
`feat/sp01-foundation-toolchain-and-contracts`; `git log --oneline develop` shows the root commit
(`chore: initial commit — design document and build plans`) plus the 7 SP-01 branch commits plus
the merge commit. If that merge is absent, **stop** — V1 does not run before the wave-0 branch is
merged.

**Then cut the verification branch.** All work for this checkpoint — including every fix and every
new integration test — happens here:

```bash
git switch develop
git pull --ff-only            # if a remote exists
git switch -c verify/v1
```

**What this checkpoint is.** Not a smoke test. It is an exhaustive re-verification of **every single
functionality that exists in the codebase at this point**, plus a set of new cross-component
integration tests that only became possible now that all of SP-01's packages coexist on one branch.
Those integration tests are authored during this checkpoint and become a permanent part of the
suite.

**What this checkpoint is not.** It does not test anything owned by SP-02 … SP-18. Every
`internal/` package other than the ten SP-01 implements is a `core.ErrNotImplemented` stub, and the
correct verification of a stub is that it is **inert and honest**, never that it produces plausible
data. Do not implement missing behaviour to make a check pass. Do not test the replay harness,
sketches, FastCDC, canonicalizers, the daemon, the store, the DAG, the observer, negative
knowledge, the checkpointer, the rehydrator, the scheduler, MCP tools, slash commands, the
analyzer, Phase-7 refinements, packaging, or documentation pages other than
`docs/config-reference.md`.

**No wave-1 branch (`feat/sp02-*` … `feat/sp07-*`) may be cut until this checkpoint is fully
green.** That rule is in §7 below and is absolute.

**Subagent strategy for this checkpoint.** Fan the §1 inventory out across parallel subagents, one
per inventory group (A … R below — SP-01 is the only completed subplan, so its inventory is
partitioned by package group). Each subagent runs only the commands in its group, captures verbatim
command output, and returns a filled-in slice of the §8 report table plus any failure with full
output — subagents diagnose, they do **not** commit. The **main session** runs §4 (the new
integration tests — they are cross-cutting, they build the real binary, and they must not race
against each other for `t.TempDir()`/`HOME`/port-free-but-shared resources), §5 (performance
budgets — parallel benchmark runs contaminate each other's timings; run these serially and alone),
and §7 (all commits and the final merge).

**Environment note.** The dev machine is Windows 11 with PowerShell as the primary shell; a Bash
tool is available. `go run ./tools/devtool <task>` is the canonical cross-shell entry point
(§2.6 D "No Makefile-only workflow"), so prefer it over raw shell pipelines. Where a check below
needs a text search, PowerShell's `Select-String` and Git Bash's `grep` are both acceptable; the
expected result is stated as a match count, not as a specific tool's output.

---

## 1. Cumulative functionality inventory

Every functionality delivered by every completed subplan, enumerated, with the exact command(s) to
run and the expected result. Nothing here is "run all tests": every suite, test, benchmark, CLI
call and lint gate is named.

Run all commands from the repository root
(`C:/Users/Quant/Documents/Programming/Projects/qompack`) on `verify/v1`.

### SP-01 — Foundation: repo init, toolchain, CI, config, `.qompack` layout, plugin manifest, no-op hooks, test scaffolding, and every interface stub

Source of items: SP-01 "What exists when you finish", the Interface-contract section, the
Implementation spec (§1–§20), the Test plan, and the 18-item Definition of Done.

#### Group A — Repository, git hygiene, commit conventions

| # | Functionality | Command | Expected result |
|---|---|---|---|
| A1 | Repository initialized with `main` and `develop`; root commit contains exactly `Qompack.md`, `plans/`, `.gitignore`, `LICENSE` | `git log --oneline --all` ; `git show --stat $(git rev-list --max-parents=0 HEAD)` | Root commit subject is `chore: initial commit — design document and build plans`; its stat lists only `Qompack.md`, `plans/*` (22 files), `.gitignore`, `LICENSE` — 30 files total, no Go files, no CI files |
| A2 | Branch topology: `main`, `develop`, merged `feat/sp01-…`, current `verify/v1` | `git branch -a` ; `git merge-base --is-ancestor main develop && echo ok` | All four branches present; `ok` printed; `verify/v1` tip is a descendant of the SP-01 merge commit |
| A3 | Exactly 8 commits total (1 root + 7 on the SP-01 branch), each a valid Conventional Commit | `git log --oneline $(git rev-list --max-parents=0 HEAD)..develop --no-merges` ; `git log --format=%s $(git rev-list --max-parents=0 HEAD)..develop --no-merges` | 7 non-merge commits on `develop` beyond the root; every subject matches `^(feat\|fix\|docs\|test\|refactor\|perf\|build\|ci\|chore\|revert)(\([a-z0-9/_.,-]+\))?: .{1,64}$` with no trailing period. **The scope class must include a comma**: SP-01 lands multi-package commits (`feat(logging,obs,tokens,hookio)`, `test(e2e,guards)`) and both `tools/devtool/checkcommitmsg.go` and CI spell it that way; a comma-less class rejects real commits on this branch |
| A4 | `Refs:` footer present on every `feat`/`fix` commit | `git log --format='%s%n%b%n---' develop --no-merges` | Every `feat:`/`fix:` commit body contains a `Refs:` line naming SP-01 and at least one `§`/`G` reference |
| A5 | **No attribution trailers anywhere** (§10, verbatim rule) | `git log --format=%B --all \| grep -Ei 'Co-Authored-By\|Signed-off-by\|Generated with\|🤖'` (PowerShell: `git log --format=%B --all \| Select-String -Pattern 'Co-Authored-By\|Signed-off-by\|Generated with\|🤖'`) | **Zero matches.** Any match is an immediate failure of this checkpoint |
| A6 | `.gitignore` is the §9 normative minimum, verbatim | `git show HEAD:.gitignore` | Byte-identical to the §9 block: `/.qompack/`, `**/.qompack/`, `/bin/`, `/dist/`, `/plugin/bin/`, `*.exe`, `*.dll`, `*.so`, `*.dylib`, `*.test`, `*.out`, `coverage.*`, `/testdata/bench-*.json`, the recorded-sessions pair, and the tooling block |
| A7 | `.qompack/` is genuinely ignored, and self-ignores | `mkdir -p .qompack && printf 'x' > .qompack/probe.txt && git status --porcelain` ; then delete the probe | `git status --porcelain` shows **no** `.qompack` entry |
| A8 | `.gitattributes` protects goldens and corpora from CRLF mangling | `git check-attr -a -- "testdata/corpora/hookio/seed_posttooluse.json"` ; `git check-attr -a -- internal/config/config.go` | `testdata/corpora/**` reports `text: unset` (i.e. `-text`, binary) and `*.go`/`*.md` report `text eol=lf`. **Do not use `testdata/golden/config/schema.json` as the probe**: it is not a `*.golden` file, so it correctly reports `text: auto, eol: lf`, and the original row expected `-text` from a path its own rule never covered |
| A9 | `Qompack.md` unmodified since the root commit (DoD 18) | `git diff $(git rev-list --max-parents=0 HEAD) HEAD -- Qompack.md` | Empty output |
| A10 | `commit-msg` hook installable and enforcing | `go run ./tools/devtool install-hooks` ; then `go test -run TestCheckCommitMsg ./tools/...` | Hook written to `.git/hooks/commit-msg` (mode 0755 on POSIX); `TestCheckCommitMsg` PASS — accepts valid subjects; rejects over-length subject, trailing period, missing type, `Co-Authored-By` in body, `🤖` in body, missing `Refs:` on a `feat` |
| A11 | Placeholder scan clean | `git grep -nwE 'TODO\|TBD\|FIXME\|XXX' -- ':!Qompack.md' ':!plans/' ':!testdata/'` ; `git grep -niE 'not sure\|implement appropriately\|handle edge cases' -- ':!Qompack.md' ':!plans/' ':!testdata/'` | **Zero matches** from both. `core.ErrNotImplemented` bodies are a declared contract and are not placeholders. Three corrections to the original command, each of which made it useless: `-R` is not a `git grep` switch, so the command **errored out** and its zero-match result was an error exit, not a clean scan; the tag scan must be **case-sensitive**, because `-i` turns `TODO` into a match for the domain word *todo* (`TodoWrite`, `Signals.TodoCompleted`, `todo_transition`, `changepoint.features: ["…","todos"]`) and reports 23 false hits; and it needs `-w`, or `TBD` matches inside longer identifiers. The prose patterns keep `-i` since no domain term collides with them. **V2 correction:** `':!testdata/'` was added to both — SP-04's corpus recorded a `git diff` of this very document (`testdata/corpora/toolout/git/git-diff.txt` and its canon golden), so an unexcluded scan matches its own command text forever |

#### Group B — Toolchain: module, pinned tools, `devtool`, `nomagic`, graph checks

| # | Functionality | Command | Expected result |
|---|---|---|---|
| B1 | Go module identity and pins | `go list -m` ; `head -6 go.mod` | `github.com/qompack/qompack`; `go 1.26`; `toolchain go1.26.4` |
| B2 | Closed runtime dependency list (§2.5) | `go list -m all \| head -40` ; inspect `go.mod` `require` block | Direct requires are exactly: `Microsoft/go-winio`, `google/go-cmp`, `klauspost/compress`, `stretchr/testify`, `golang.org/x/tools`, `pgregory.net/rapid` |
| B3 | Pinned external tools live in a separate nested module | `go list -m -modfile=tools/pinned/go.mod all \| head` ; `go list ./... \| grep -c 'tools/pinned'` | Four pinned modules (golangci-lint, x/perf, x/vuln, gofumpt) with resolved versions and a committed `tools/pinned/go.sum`; **0** packages from `tools/pinned` in the root module's package list |
| B4 | Formatting is clean | `go run ./tools/devtool fmt-check` | Prints nothing; exit 0 |
| B5 | Full lint pass — all seven sub-checks (DoD 3) | `go run ./tools/devtool lint` | Exit 0. `golangci-lint` (govet staticcheck errcheck revive gocritic ineffassign unconvert unparam misspell bodyclose gosec forbidigo copyloopvar), then `nomagic`, `importgraph`, `testdeps`, `bindeps`, `sleepcheck`, `stubskips` all clean |
| B6 | `nomagic` literal gate (D11 / §11.6) works and is not vacuous | `go test -run TestNoMagic_Analyzer ./tools/lint/nomagic/...` ; then a **temporary** negative probe: add `var _ = 0.55` to a scratch file under `internal/obs/`, run `go run ./tools/lint/nomagic ./internal/obs/...`, then delete the scratch file | `TestNoMagic_Analyzer` PASS (one violation per literal class plus the allowed line, matching `// want`); the negative probe **reports** `literal 0.55 duplicates a config default`; after deletion `go run ./tools/devtool lint` is clean again |
| B7 | Ski-rental threshold is computed, never literal | `go test -run TestSkiRental_ComputedNotLiteral ./internal/scheduler/...` ; `git grep -n '12\.5' -- 'internal/**/*.go' ':!*_test.go'` | Test PASS (`SkiRentalShouldWrite(13,0.1,1.25)==true`, `(12,…)==false`, `(1,0,1.25)==false`); grep returns **zero** matches |
| B8 | Import-graph dependency DAG (§3.2) enforced against the real repo | `go test -run 'TestImportGraph_(RejectsViolation\|AcceptsRealRepo)' ./tools/...` | Both PASS. `RejectsViolation` rejects a synthetic `store → negknow` list with a message naming §3.2; `AcceptsRealRepo` passes against the actual package graph |
| B9 | Composition-root rule: nothing imports `daemon`, `cli`, `commands`, `testutil`, `cmd/qompack`, `test/e2e`, `test/guards` | `go run ./tools/devtool lint --only=importgraph` | Exit 0; no violation lines |
| B10 | Test-only dependencies never reach production packages | `go run ./tools/devtool lint --only=testdeps` ; `go test -run TestTestDeps_RejectsProductionTestify ./tools/...` | Exit 0 / PASS. `testify`, `go-cmp`, `rapid` appear only in `internal/testutil`, the `<pkg>test` subpackages, and `test/**` |
| B11 | Shipped binary's dependency closure (§2.5) — for all six targets | `go run ./tools/devtool lint --only=bindeps` ; spot-check `go list -deps ./cmd/qompack \| grep -Ev '^(internal/\|github.com/qompack/\|github.com/klauspost/compress\|github.com/Microsoft/go-winio)' \| grep -E '^[a-z0-9.-]+\.[a-z]{2,}/'` | Exit 0. The only non-stdlib modules in the closure are `github.com/qompack/qompack/…`, `klauspost/compress/…`, `Microsoft/go-winio/…`. `golang.org/x/tools`, testify, go-cmp, rapid are **absent** on every one of linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/{amd64,arm64} |
| B12 | No wall-clock sleeps (§6.1) | `go run ./tools/devtool lint --only=sleepcheck` | Exit 0; no `time.Sleep` selector call outside `test/bench` |
| B13 | Stub-skip accounting | `go run ./tools/devtool lint --only=stubskips` | Exit 0; the count of `behaviour: implementation is a stub (Rule W-1)` skips is reported and matches the number of stubbed packages listed with a non-`SP-01` owner in `plans/OWNERS.tsv` |
| B14 | Every `devtool` task exists and behaves | `go run ./tools/devtool` (usage) ; then `vet`, `build`, `test`, `cover`, `bench`, `bench-hotpath`, `replay`, `plugin-validate`, `fsck`, `gen-config-docs --check`, `gen-contract-fixtures`, `ci-local` | Usage lists the §2.6 task names. `bench-hotpath` prints `bench-hotpath: harness not present (owned by SP-05)` and **exits 0**. `replay` prints `replay: driver not present (owned by SP-02)` and **exits 0**. `fsck` exits non-zero with `qompack fsck: not implemented in this build` (fsck is a non-hook subcommand). Every other task exits 0 |
| B15 | Six-target cross-build (DoD 10) | `go run ./tools/devtool build-all` ; `ls dist/` | Six artifacts: `qompack-linux-amd64`, `qompack-linux-arm64`, `qompack-darwin-amd64`, `qompack-darwin-arm64`, `qompack-windows-amd64.exe`, `qompack-windows-arm64.exe`. All built with `CGO_ENABLED=0`, `-trimpath` |
| B16 | `.golangci.yml` forbid rules are real | inspect `.golangci.yml` ; `go run ./tools/devtool lint` | `forbidigo` forbids `fmt.Print*`, `time.Sleep`, `net.Dial`; `internal/ipc` carries the documented `net.Dial` exclusion; `errcheck.check-type-assertions: true`; `revive` has `exported` and `package-comments` |
| B17 | Full local CI (DoD 3, 4, 5, 8, 9) | `go run ./tools/devtool ci-local` | Exit 0 end to end: `fmt-check` → `lint` → `vet` → `build` → `test` → `cover` → `plugin-validate` → `gen-config-docs --check` |

#### Group C — `internal/core`

| # | Functionality | Command | Expected result |
|---|---|---|---|
| C1 | Domain-separated hashing | `go test -run 'TestHashBytes_(DomainSeparation\|KnownVector)' ./internal/core/...` | Both PASS. `HashBytes("a","bc") != HashBytes("ab","c")`; `HashBytes("qompack.root.v1", nil) == sha256("qompack.root.v1\x00")` |
| C2 | `Hash` text form and parsing | `go test -run 'TestHash_StringShortParse_RoundTrip\|TestParseHash_Rejects\|TestHash_JSONRoundTrip' ./internal/core/...` | All PASS. `String()` = `"sha256:"` + 64 lowercase hex; `Short()` = 12 hex chars; `ParseHash` round-trips 100 random hashes; rejects `""`, `"sha256:"`, 63 hex, 65 hex, non-hex — each wrapping `ErrNotFound`; JSON marshals to `"sha256:…"` |
| C3 | Decision IDs | `go test -run TestNewDecisionID_Format ./internal/core/...` | PASS; matches `^dec_[0-9a-f]{12}$` |
| C4 | Seven sentinels distinct, `IsNotImplemented` unwraps | `go test -run 'TestSentinels_AreDistinct\|TestIsNotImplemented_UnwrapsWrapping' ./internal/core/...` (the unwrap half is a separate test; `TestSentinels_AreDistinct` alone never mentions `IsNotImplemented`) | PASS. `ErrNotImplemented`, `ErrNotFound`, `ErrAppendOnly`, `ErrAlreadyEncoded`, `ErrBudget`, `ErrDegraded`, `ErrContract` pairwise non-`errors.Is`; `IsNotImplemented(fmt.Errorf("w: %w", ErrNotImplemented))` is true |
| C5 | ID/scalar types, clock, version | `go test ./internal/core/...` ; `go doc ./internal/core` | Whole-package PASS. `SessionID ToolUseID TurnIndex SegmentID CheckpointSeq DecisionID Tokens UnixMilli ChunkRef Dep Clock SystemClock NowMilli UnixMilli.Time Version` all declared exactly as §4 / SP-01 Produces spells them; `Version == "0.1.0"` |
| C6 | Domain registry documented and the IPC non-member noted | inspect `internal/core/hash.go` comment block | Lists `qompack.chunk.v1`, `qompack.root.v1`, `qompack.neg.v1`, `qompack.decision`, `qompack.args.v1`, and explicitly states that the IPC endpoint hash is **undomained** `sha256.Sum256` and must not be "fixed" into `HashBytes` |

#### Group D — `internal/paths` (the §7.4 append-only invariant, mechanically)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| D1 | Project-root resolution, all four branches (§3.3) | `go test -run 'TestResolve_' ./internal/paths/...` | `TestResolve_EnvWins`, `TestResolve_WalksToGitDir`, `TestResolve_GitFileWorktree`, `TestResolve_FallsBackToCWD` all PASS |
| D2 | Path normalization and dedup key | `go test -run 'TestNorm_RejectsEscape\|TestNorm_ForwardSlashRelative\|TestKeyFold\|TestNorm_Property' ./internal/paths/...` | All PASS. Escapes rejected with "escapes project root"; Windows separators normalized to `/`; `KeyFold("Src/Foo.TS", true) == "src/foo.ts"` and `(…, false)` preserves casing; the rapid property holds |
| D3 | Layout creation and self-ignore | `go test -run TestEnsureLayout_CreatesAllDirsAndSelfIgnore ./internal/paths/...` | PASS. All 17 directories created (`objects index sketches dag grammar checkpoints pins eval eval/replay eval/opt records state run spool logs metrics tmp`); `.qompack/.gitignore` content is exactly `*\n` |
| D4 | Atomic writes | `go test -run 'TestWriteAtomic_' ./internal/paths/...` | `TestWriteAtomic_ReplacesAndSyncs` PASS with no `wa-*` leftovers in `tmp/`; `TestWriteAtomic_RefusesProtected` returns `core.ErrAppendOnly` |
| D5 | **`TestAppendOnlyGuard`** — the §7.4 / §4.6 mechanical enforcement (DoD 12) | `go test -v -run TestAppendOnlyGuard ./internal/paths/...` | PASS with **all five** illegal attempts failing: (a) `O_TRUNC` on `checkpoints/0001.json` → `ErrAppendOnly`; (b) non-append `O_WRONLY` on `pins/invariants.jsonl` → `ErrAppendOnly`; (c) `WriteAtomic` onto `sketches/tried.bloom` → `ErrAppendOnly`; (d) second `CreateNew` of the same checkpoint seq → `os.ErrExist`; (e) `AppendOnly("x.json")` → `ErrAppendOnly` (wrong extension) |
| D6 | Checkpoint immutability on disk | `go test -run TestCreateNew_SetsReadOnly ./internal/paths/...` | PASS; file mode has no write bit (`FILE_ATTRIBUTE_READONLY` on Windows) and a subsequent `os.WriteFile` fails |
| D7 | JSONL append discipline | `go test -run TestAppendJSONL_OneLinePerRecord ./internal/paths/...` | PASS; exactly 3 lines for 3 records; an embedded `\n` is JSON-escaped, never raw |
| D8 | Bloom replacement is the only legal `tried.bloom` write path | `go test -run TestReplaceBloom_KeepsOneBackup ./internal/paths/...` | PASS; newest `tried.bloom` in place, exactly one `.bak` generation retained |
| D9 | Windows long paths | `go test -run 'TestLongPath_Over260\|TestLong_PrefixesOverThreshold' ./internal/paths/...` | PASS on every platform. `TestLongPath_Over260` asserts only that the fixture path exceeds `MAX_PATH` (260); it does not reach 300. The 300-character case is `TestLong_PrefixesOverThreshold`, which is Windows-gated and skips elsewhere — so on Linux and macOS this row proves the >260 half only |
| D10 | Checkpoint manifest | `go test -run TestManifest_AppendAndRead ./internal/paths/...` | PASS; three `ManifestEntry` records read back in order with exact `seq`/`sha256`/`bytes`/`created` |
| D11 | Whole-package + coverage floor (90%, §6.4 row 1) | `go test -cover ./internal/paths/...` | PASS; statement coverage **≥ 90%** |

#### Group E — `internal/config` (Appendix C, five layers, validation, provenance, schema)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| E1 | **Appendix C reproduced verbatim** (DoD 11, §11.1) | `go test -v -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/...` | PASS. `json.Marshal(Defaults())` minus the `runtime` key deep-equals `testdata/golden/config/appendix-c.jsonc` — every key spelling (`softFloorPct`, `hardCeilingMargin`, `nearDupThreshold`, `promoteAfterExpansions`, …) and every value (`0.55`, `20000`, `0.004`, `0.1`, `1.25`, `300`, `120`, `12000`, `20000`, `10000`, `0.01`, `0.001`, `2048`, `128`, `0.9`, `0.4`, `20`) exact |
| E2 | §11.5 `runtime` namespace plus the three SP-01 additions | `go test -run TestDefaults_RuntimeNamespace ./internal/config/...` | PASS field by field: `mode:auto`; daemon `{true,1800,8,8,5}`; hotPath `{15,3,true,1048576}`; logging `{info,10,5}`; redact `{true,[]}`; telemetry `{false}`; rehydrate `{8000,12000,450,8}`; mcp `{40,262144}`; **budgets** `{l0IngestMs:2, l0ProcessMs:50, checkpointFinalizeMs:2000, mcpToolCallMs:250}`; **selection** `{submodularEnabled:false}`; **tokens** `{4.0,3.6,3.2,3.4,3.0,750,1600,1800,0.6,1.6,0.2}` |
| E3 | JSONC tolerance | `go test -run TestStripJSONC ./internal/config/...` | PASS; line and block comments blanked with **byte offsets preserved**; `//` inside a string untouched; trailing commas removed in objects and arrays |
| E4 | Five-layer precedence (§11.2) | `go test -run 'TestLoad_PrecedenceFiveLayers\|TestLoad_DeepMergePerLeaf\|TestLoad_EnvKeyMapping' ./internal/config/...` | All PASS. Flag beats env beats project beats user beats defaults (`0.8` wins, `Origin == OriginFlag`); a project file setting only `scheduler.softFloorPct` leaves `cache.readMultiplier == 0.1` and `idle.detectAfterSeconds == 120` intact; `QOMPACK_SCHEDULER__CACHE__READMULTIPLIER=0.08` → `0.08` with `OriginEnv` |
| E5 | `null` means "measure at runtime", not zero | `go test -run TestLoad_NullMeansMeasure ./internal/config/...` | PASS; `MeasuredDeltaSeconds == nil`, not `*float64(0)` |
| E6 | Forward compatibility: unknown keys warn, never error | `go test -run 'TestLoad_UnknownKeyWarnsNeverErrors\|TestLoad_UnparseableFileWarns' ./internal/config/...` | Both PASS; warnings emitted, `error == nil`, defaults intact |
| E7 | **Invalid config falls back per leaf, never crashes** (§11.3) | `go test -run 'TestLoad_InvalidLeafFallsBackNotCrash\|TestCLI_ConfigViolationsAreLoudAndPersisted' ./internal/config/... ./internal/cli/...` | Both PASS. `config` half: `softFloorPct` back to `0.55`, `chunk.min` back to `1024`, two `Warning`s, `ViolationsFromWarnings` decodes both, `error == nil`, **no file written and no logger touched**. `cli` half: two `Loud` messages and `.qompack/state/config-violations.json` containing both `Violation`s |
| E8 | Provenance carries file and line | `go test -run TestLoad_ProvenanceLocationHasLine ./internal/config/...` | PASS; `Location` ends `config.json:4` |
| E9 | Complete validation rule table (§11.3) | `go test -v -run 'TestValidate_EveryRule\|TestValidate_RuleTableIsComplete\|TestValidate_TiersPartition\|TestValidate_TelemetryMustBeFalse' ./internal/config/...` | All PASS. One sub-case per rule line (a two-bound rule contributes two cases), each producing exactly one `Violation` with the expected `Key`; every leaf of `Config` is either constrained or in the explicit `unconstrained` allowlist; tier lists must be disjoint **and** cover all eight truncatable fields; `runtime.telemetry.enabled = true` is a `Violation` |
| E10 | Ship-order flag derivation (§5.12) | `go test -run TestSubmodularEnabled_DerivedFromRuntime ./internal/config/...` | PASS; `Selection.Submodular.Enabled` follows `runtime.selection.submodularEnabled`, and `json.Marshal(cfg.Selection)` contains **no** `enabled` key |
| E11 | JSON Schema emission | `go test -run TestJSONSchema_Golden ./internal/config/...` | PASS; byte-equals `testdata/golden/config/schema.json`; keys sorted; every leaf carries `default`, `description`, `x-qompack-section` |
| E12 | Dotted lookup | `go test -run TestGet_DottedLookup ./internal/config/...` | PASS; `Get("scheduler.cache.writeMultiplier")` → `1.25, true`; `Get("nope")` → `nil, false` |
| E13 | Load never panics on arbitrary input | `go test -run FuzzConfigLoad -fuzz FuzzConfigLoad -fuzztime 60s ./internal/config/` | No crashers; corpus under `testdata/corpora/config/` exercised; always returns a validated config |
| E14 | Generated config documentation is not stale (DoD 9) | `go run ./tools/devtool gen-config-docs --check` ; `git diff --exit-code -- docs/config-reference.md` | Exit 0 both. `docs/config-reference.md` lists every key with type, default, valid range and motivating `Qompack.md` section |
| E15 | Whole-package + coverage floor (90%) | `go test -cover ./internal/config/...` | PASS; statement coverage **≥ 90%** |

#### Group F — `internal/logging` (the Loud channel)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| F1 | Level filtering | `go test -run TestLogger_LevelFiltering ./internal/logging/...` | PASS; a `Warn`-level logger writes only Warn and Error lines |
| F2 | **Loud goes to three destinations** (§12 invariant 10) | `go test -v -run TestLoud_ThreeDestinations ./internal/logging/...` | PASS; the day log, `LOUD.log`, and the in-memory ring (`LastLoud()`) all carry the message, and the attached observer closure fired exactly once |
| F3 | Loud counter wiring without an import cycle | `go test -run TestLoud_CounterWiring ./internal/cli/...` | PASS; the real `obs` registry reports `loud.total == 1` after a `Loud` through the dispatcher — while `internal/logging` still does not import `internal/obs` (re-checked by B8) |
| F4 | Rotation | `go test -run TestLogger_Rotation ./internal/logging/...` | PASS; with `MaxFileMB=1, MaxFiles=2`, 3 MB of lines leaves at most 2 rotated files plus the current one |
| F5 | `Nop()` still records loudness | `go test ./internal/logging/...` | Whole-package PASS; `Nop()` discards everything except `Loud`, which still lands in the ring |
| F6 | Coverage floor (75%) | `go test -cover ./internal/logging/...` | ≥ 75% |

#### Group G — `internal/obs` (histograms, budget IDs B-A…B-F)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| G1 | Log-bucket histogram correctness | `go test -run 'TestHistogram_BucketMonotone\|TestHistogram_PercentileConservative\|TestHistogram_MaxExact' ./internal/obs/...` | All PASS. `bucketFor` non-decreasing and `bucketUpper(bucketFor(u)) >= u`; 10 000 observations of exactly 10 ms give `10ms <= P99 <= 10ms × 1.0905` (**conservative**, so a gate can never pass by rounding); `Max` exact at 1 234 567 µs |
| G2 | **All six budgets present and config-driven** (§2.4, §11.3) | `go test -v -run TestBudgets_AllSixPresentAndConfigDriven ./internal/obs/...` | PASS. `Budgets()` returns B-A…B-F. B-A limit `15ms` from `runtime.hotPath.budgetMs` (p99, gated); B-B from `runtime.budgets.l0IngestMs` (p99, gated); B-C from `l0ProcessMs` (**not gated** — soft); B-D `Gated == false`, reported only; B-E from `checkpointFinalizeMs` (p99, gated); B-F from `mcpToolCallMs` (p95, gated). Changing config changes every limit |
| G3 | Breach-window accounting | `go test -run TestCheckBudgets_CountsConsecutiveWindows ./internal/obs/...` | PASS; one `BudgetBreach` with `Windows == 3` |
| G4 | Counters, gauges, snapshot, persistence | `go test ./internal/obs/...` | Whole-package PASS; `Snapshot()` returns a deep copy; `Persist(layout)` writes `metrics/latency.json` through `paths.WriteAtomic` |
| G5 | Coverage floor (75%) | `go test -cover ./internal/obs/...` | ≥ 75% |

#### Group H — `internal/tokens` (baseline estimator, G10.2 groundwork)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| H1 | Content classification | `go test -v -run TestClassify_Table ./internal/tokens/...` | PASS on all 14 cases: `.png`→Image, `.pdf`→PDF, `%PDF` magic→PDF, `.json`→JSON, extension-less valid JSON→JSON, `diff --git` body→Diff, `.md`→Prose, Go source→Code, NUL bytes→Binary, `.ts`→Code, Grep output→Code, empty→Prose |
| H2 | Exact estimator arithmetic | `go test -run 'TestEstimate_ProseVsCode\|TestEstimate_ImageFromDimensions\|TestEstimate_ImageCappedAt1600\|TestEstimate_PDFPageCount' ./internal/tokens/...` | All PASS: 4000 bytes prose → `1000`; 4000 bytes code → `1112`; 100×100 PNG → `14`; 4000×4000 PNG → `1600` (capped); 3-page synthetic PDF → `5400` |
| H3 | Uncalibrated estimator is the identity | `go test ./internal/tokens/...` | `Factor()` starts at exactly `1.0` |
| H4 | Calibration clamps and persists | `go test -run TestCalibrate_ClampsAndPersists ./internal/tokens/...` | PASS; `Factor()` ≤ `1.6` after 20 calls; `<home>/.qompack/calibration.json` written and re-read by a fresh `New` |
| H5 | `EstimateRoot` over `[]core.ChunkRef` (no `store` import) | `go test -run TestEstimateRoot_SumsChunks ./internal/tokens/...` ; `go list -deps ./internal/tokens \| grep -c 'qompack/internal/store'` | Test PASS (3×1000-byte prose chunks → `750`); grep count **0** |
| H6 | Monotonicity property | `go test -run TestEstimate_MonotoneInLength ./internal/tokens/...` | rapid property PASS |
| H7 | `tokenstest` conformance suite shape | `go test -v -run 'TestEstimatorSuite_RealImplementation' ./internal/tokens/...` — **`TestTokensSuite` does not exist**, and `-run` on a name that matches nothing exits 0, so the original command reported success while running no test at all | Shape block PASS; behaviour block skipped with exactly `behaviour: implementation is a stub (Rule W-1)` **only** for the parts SP-06 owns (`EstimateRoot` exact chunk accounting); baseline assertions run |
| H8 | Coverage floor (75%) | `go test -cover ./internal/tokens/...` | ≥ 75% |

#### Group I — `internal/hookio` (host-drift absorption)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| I1 | All seven hook payload shapes parse | `go test -v -run TestReadEvent_AllSevenHookPayloads ./internal/hookio/...` | PASS for `PostToolUse`, `UserPromptSubmit`, `SessionStart` (×4 sources: startup/resume/compact/clear), `PreCompact`, `Stop`, `SubagentStop`, `SessionEnd`; every field matches the golden `want/` file |
| I2 | Unknown fields preserved in `Extra` | `go test -run TestReadEvent_UnknownFieldsPreserved ./internal/hookio/...` | PASS; `Extra["future_field"]` holds raw JSON; no error |
| I3 | Missing and null fields never panic | `go test -run 'TestReadEvent_MissingFieldsNeverPanic\|TestReadEvent_NullFields' ./internal/hookio/...` | Both PASS; zero-valued `Event`, nil error |
| I4 | Payload limit | `go test -run TestReadEvent_LimitExceeded ./internal/hookio/...` | PASS; `core.ErrBudget` with the raw bytes still returned |
| I5 | Output codec | `go test -run 'TestWriteOutput_EmptyIsMinimal\|TestWriteOutput_NoHTMLEscaping\|TestSessionStartOutput_Shape' ./internal/hookio/...` | All PASS. `Empty()` serializes to exactly `{}\n`; `a<b&c` is not HTML-escaped; `SessionStartOutput("ctx")` → `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"ctx"}}` |
| I6 | Parser never panics on arbitrary bytes | `go test -run FuzzReadEvent -fuzz FuzzReadEvent -fuzztime 60s ./internal/hookio/` | No crashers; seeds from `testdata/corpora/hookio/` (7 valid + 5 malformed) exercised |
| I7 | Coverage floor (75%) | `go test -cover ./internal/hookio/...` | ≥ 75% |

#### Group J — `internal/cli`, `cmd/qompack`, and the six no-op hooks

| # | Functionality | Command | Expected result |
|---|---|---|---|
| J1 | **Hooks always exit 0** (§2.3, DoD 13) | `go test -v -run TestDispatch_HookAlwaysExitsZero ./internal/cli/...` | PASS on **all 30 combinations** (6 hook subcommands × 5 fault injections: unreadable stdin, malformed JSON, unresolvable project root, read-only `.qompack`, panicking inner function). Exit code 0 every time; stdout always parses as `hookio.Output` |
| J2 | Exit-code policy for non-hooks | `go test -run 'TestDispatch_NonHookErrorExitsOne\|TestDispatch_UnknownCommandExitsTwo' ./internal/cli/...` | Both PASS; stub subcommand → exit 1 with the name on stderr; unknown subcommand → exit 2 with usage |
| J3 | Panic recovery is loud | `go test -run TestDispatch_PanicRecovered ./internal/cli/...` | PASS; exit 0 for a hook command, a `Loud` record with a stack, and valid JSON still written to stdout |
| J4 | Hook observability log, without payload content | `go test -run TestHooks_WriteHookLog ./internal/cli/...` | PASS; `.qompack/logs/hooks-YYYYMMDD.jsonl` gains 6 lines with the right `hook` values and fields `{ts, hook, session_id, tool_name, bytes, truncated}` — **no payload text** |
| J5 | `config print` with provenance | `go test -run TestConfigPrint_Provenance ./internal/cli/...` ; `go run ./cmd/qompack config print --provenance` | Test PASS; the live command annotates each leaf with origin and location |
| J6 | `config schema` | `go test -run TestConfigSchema_Emits ./internal/cli/...` ; `go run ./cmd/qompack config schema \| head -5` | Test PASS; live output parses as JSON and equals `Defaults().JSONSchema()` |
| J7 | `--set` flag plumbing | `go test -run TestSetFlagParsing ./internal/cli/...` | PASS; `--set scheduler.cache.readMultiplier=0.08 --set eval.minSessions=5` both land in **`Env.Set`** and take effect. (The field is `Set`, not `Flags`; `Flags` is the name it is passed under into the config loader's own struct) |
| J8 | The dispatch table is complete from day one | `go run ./tools/devtool build` ; then for each of `mcp status recall pin why dropped eval fsck doctor bench` (**V2 correction: `daemon` and `self-test` are real since SP-05** — `daemon` starts a resident process and `self-test` may exit non-zero by design; both have their own rows): `./bin/qompack <name>` (`.\bin\qompack.exe` on Windows) | Each prints `qompack <name>: not implemented in this build` to stderr and exits **1** (`self-test` may exit 1 by its own policy). None panics; none exits 0 falsely |
| J9 | `version` and `help` | `./bin/qompack version` ; `./bin/qompack --help` | usage text exits 0; `version` prints `core.Version` — `0.1.0` in-source, but `devtool build` injects a git-describe string via `-ldflags` since SP-05, so expect that form from a devtool-built binary (SP-17 owns reconciling binary and plugin-manifest versions) |
| J10 | Six hook subcommands respond correctly through the real binary | `./bin/qompack observe tool < payload.json`, `observe prompt`, `observe stop`, `observe stop --subagent`, `session-start`, `checkpoint`, `flush` | `observe *` and `flush` emit `{}`; `session-start` emits `{"hookSpecificOutput":{"hookEventName":"SessionStart"}}`; `checkpoint` emits `{"hookSpecificOutput":{"hookEventName":"PreCompact"}}`; every exit code 0 |
| J11 | `cmd/qompack/main.go` stays a dispatcher | `wc -l cmd/qompack/main.go` (PowerShell: `(Get-Content cmd/qompack/main.go \| Measure-Object -Line).Lines`) | **< 150** lines; no package-level init beyond `var` declarations |
| J12 | Coverage floor (75%) for `cli` | `go test -cover ./internal/cli/...` | ≥ 75% |

#### Group K — `internal/pluginmanifest` and the `plugin/` bundle (§3.4, §7.5)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| K1 | Generated bundle is byte-exact | `go test -v -run TestManifest_GoldenBytes ./internal/pluginmanifest/...` | PASS; all 10 generated files byte-equal their goldens in `testdata/golden/plugin/` (2-space indent, trailing newline) |
| K2 | All six §7.3 hooks covered, with the seven manifest entries | `go test -run TestManifest_CoversAllSixHooks ./internal/pluginmanifest/...` ; `cat plugin/hooks/hooks.json` | PASS; keys exactly `PostToolUse UserPromptSubmit SessionStart PreCompact Stop SubagentStop SessionEnd`; timeouts `5,5,15,20,5,10,20`; `PostToolUse` carries `"matcher": "*"`; every command uses `${CLAUDE_PLUGIN_ROOT}/bin/qompack …`; `SubagentStop` carries `--subagent` |
| K3 | Seven slash commands, shelling out correctly | `go test -run 'TestManifest_SevenCommands\|TestManifest_CommandsShellOutToBinary' ./internal/pluginmanifest/...` | Both PASS; names exactly `status recall pin checkpoint why dropped eval`; each `.md` has `description`, `argument-hint`, `allowed-tools` frontmatter and a body containing ``!`qompack <name> $ARGUMENTS` `` |
| K4 | `.mcp.json` shape | `go test -run TestMCPJSON_UsesPluginRoot ./internal/pluginmanifest/...` ; `cat plugin/.mcp.json` | PASS; `{"mcpServers":{"qompack":{"command":"${CLAUDE_PLUGIN_ROOT}/bin/qompack","args":["mcp"]}}}` |
| K5 | Drift detection has teeth | `go test -run TestValidate_DetectsDrift ./internal/pluginmanifest/...` | PASS; mutating one byte of `hooks.json` yields exactly one `Diff` naming that path |
| K6 | Committed bundle matches the generator (DoD 8) | `go run ./tools/devtool plugin-validate` ; `git diff --exit-code -- plugin/` | Both exit 0. `plugin-validate` additionally asserts 7 commands; the 8-MCP-tool assertion is correctly **deferred** while `mcp.RegisterAll` returns `ErrNotImplemented` |
| K7 | `plugin.json` metadata | `cat plugin/.claude-plugin/plugin.json` | `name: qompack`, `version: 0.1.0` (from `core.Version`), the §3.4 description, author, homepage, keywords |
| K8 | Coverage floor (75%) | `go test -cover ./internal/pluginmanifest/...` | ≥ 75% |

#### Group L — The 23 interface stubs and the implemented pure functions (§5, D9)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| L1 | Every stubbed package compiles with the exact §5 signature | `go build ./...` ; `go vet ./...` | Exit 0 both. The 23 packages present: `chunk canon symbols redact sketch store dag grammar negknow analyzer scheduler checkpoint pins rehydrate rules skills mcp commands eval ipc daemon observer contract` |
| L2 | **Stubs are inert, not faked** | `go test -v -run TestAllStubsReturnNotImplemented ./test/guards/...` | PASS; the reflective walk covers all 23 packages (the `probes.go` table asserts none is missing); every method returns `core.ErrNotImplemented` or a documented zero value (`Bloom.Test`→`false`, `Graph.CrossingEdges`→`0`, `Sequitur.Rules`→`nil`); constructors return a usable value with a **nil** error |
| L3 | `chunk.RootHash` (real) | `go test -run TestRootHash_Formula ./internal/chunk/...` | PASS; equals `HashBytes("qompack.root.v1", h1‖h2)` |
| L4 | `chunk.DefaultParams` / `Params.Validate` (real) | `go test ./internal/chunk/...` | PASS; defaults `1024/4096/16384` read from `config.StoreCfg.Chunk`; `Validate` enforces `Min < Target < Max` |
| L5 | `scheduler.YoungDaly` (real) | `go test -run TestYoungDaly_Formula ./internal/scheduler/...` | PASS; `YoungDaly(30,600) == math.Sqrt(2*30*600)`; `YoungDaly(0,600) == 0`; `YoungDaly(-1,5) == 0` |
| L6 | `scheduler.SkiRentalShouldWrite` (real, computed) | see B7 | PASS |
| L7 | `scheduler.PSelectionAvailable` gate | `go test ./internal/scheduler/...` | Returns **false** on this build (SP-12 flips it) |
| L8 | `negknow.Descriptor.Key` (real, frozen) | `go test -run TestDescriptorKey_Stable ./internal/negknow/...` | PASS; a fixed `Descriptor` yields the frozen 32-byte golden key (`qompack.neg.v1` domain, `0x1f` field separators) |
| L9 | `checkpoint.StripInjections` + injection tags (real) | `go test -run TestStripInjections ./internal/checkpoint/...` | PASS on all three cases: full span removed; unclosed span removes to end of string; **a bare close tag is PRESERVED, not removed**. A close tag with no open tag before it delimits nothing, so it is ordinary transcript text — stripping it would silently edit content Qompack never wrote, which is the one thing this function must not do. Constants are exactly `<!-- qompack:injected seq=%d ver=%d -->` and `<!-- /qompack:injected -->` |
| L10 | `observer.Tombstone` (real, matches §8.1 verbatim) | `go test -v -run 'TestTombstone_' ./internal/observer/...` — the §8.1 case is `TestTombstone_RendersTheSection81Form`; **`TestTombstone_MatchesDesignExample` does not exist** and selected zero tests while exiting 0 | PASS; renders `[cleared: sha256:<12hex> · 2.4KB · FileRead src/auth.ts · re-expandable]` — `humanBytes` divides by **1024** so `2457 → 2.4KB` |
| L11 | `grammar.FormatWarning` (real, frozen wording) | `go test ./internal/grammar/...` | PASS; one line, no trailing newline, shape `[qompack] possible loop: A→B→C repeated N× (turns X–Y) — <message>` |
| L12 | `rehydrate.StandingInstruction` (real) | `go test ./internal/rehydrate/...` | Returns exactly `Before committing to an approach, call already_tried.` |
| L13 | `contract.Mode.String` (real, frozen) | `go test ./internal/contract/...` | `full`, `degraded-passive`, `off`, `unknown` |
| L14 | **`analyzer.NewSelector` constructor guards are real** (§13 invariant 4 + closing note 3) | `go test -v -run 'TestGuard_SubmodularInertWithoutPSelection\|TestGuard_SelectorRefusesWithoutPSelection' ./test/guards/...` | Both PASS. A block with `Pos < p` → `core.ErrBudget` **first** (so the §13-invariant-4 reason is never masked); with all `Pos >= p` but `PSelectionAvailable() == false` → `core.ErrNotImplemented` |
| L15 | `store/compress.go` zstd round-trip (real) | `go test -run TestStoreCompress_RoundTrip ./internal/store/...` | rapid PASS up to 1 MiB; a decompression bomb over the 64 MiB `MaxDecodedSize` returns an **error**, not an allocation |
| L16 | `ipc.Resolve` endpoint derivation (real) | `go test -v -run 'TestResolve' ./internal/ipc/...` — the derivation is covered by nine `TestResolveFor_*`/`TestResolve_*` tests; **`TestIPCResolve_SunPathFallback` does not exist** and selected zero tests while exiting 0 | PASS. Windows: `\\.\pipe\qompack.<hash12>`; POSIX: `$XDG_RUNTIME_DIR/qompack/<hash12>.sock` → `<tmp>/qompack-<uid>/<hash12>.sock` → `<tmp>/qp-<hash8>.sock` when the path exceeds 100 bytes. Hash is **undomained** `sha256.Sum256` of the normalized absolute project root |
| L17 | `contract.StandardAssertions` returns the nine §5.19 IDs, all not-yet-implemented | `go test -v -run 'TestStandardAssertions\|TestResultSet' ./internal/contract/...` — `TestContractSuite` matched only by prefix (the real name is `TestContractSuite_ShapePassesAgainstStub`, which asserts suite shape, not the nine IDs). The four `TestStandardAssertions_*` tests are what check the table, and `TestResultSet_MatchesFrozenGolden` pins the §16 fixture | PASS; nine assertions; each `Check` returns `OK:true, Severity:SevInfo, Observed:"not-yet-implemented"` |
| L18 | SP-01-declared types §5 names but never defines | `go doc ./internal/obs Counter` ; `go doc ./internal/ipc AddrKind` ; `go doc ./internal/daemon SketchSet` ; `go doc ./internal/dag GraphStats` ; `go doc ./internal/negknow Deps` ; `go doc ./internal/contract History` ; `go doc ./internal/paths Global` | All declared exactly as SP-01 §14.0 spells them: `obs.Counter/Gauge/Snapshot`; `ipc.AddrKind{NamedPipe,UnixSocket}`, `HotPathMode{HotSync,HotSpool}`, `ACK=0x06`, `NAK=0x15`, `MaxLineBytes=1<<20`; `daemon.SketchSet{Tried,Touch,Explore,Top}`; `dag.GraphStats`; `negknow.Deps`; `contract.History`; `paths.Global` |
| L19 | Stub packages are **exempt** from their coverage floors, visibly | `go run ./tools/devtool cover` | Exit 0; the log prints `exempt (stub, owned by <SP-NN>)` for `store sketch chunk canon negknow checkpoint scheduler dag analyzer rehydrate eval mcp` and every other non-SP-01-owned package; no floor is silently skipped |

#### Group M — 22 conformance suites and `plans/OWNERS.tsv` (§5.22, Rule W-1)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| M1 | All 22 suites exist and their shape blocks pass against the stubs (DoD 5) | `go test -v ./internal/... \| grep -E '^(=== RUN\|--- (PASS\|SKIP))' ` , or simply `go test ./internal/...` | Suites present: `chunktest canontest sketchtest symbolstest redacttest tokenstest storetest dagtest negknowtest grammartest analyzertest schedulertest checkpointtest pinstest rehydratetest rulestest skillstest mcptest evaltest ipctest contracttest observertest` = **22**, with `storetest` exporting **two** (`RunStoreSuite`, `RunSegmentLogSuite`). Every shape block PASS |
| M2 | Every behaviour block skipped with the **exact** Rule W-1 message | `go test -v ./internal/... \| grep -c 'behaviour: implementation is a stub (Rule W-1)'` ; then `go test -v ./... \| grep -E '^\s*--- SKIP' -A1 \| grep -v -E 'Rule W-1\|Rule W-2'` | The first count is ≥ 22 (one per suite with a behaviour block). The second search returns **nothing** — but the permitted set is **three**, not two: `behaviour: implementation is a stub (Rule W-1)`, `contract fixture not yet recorded (Rule W-2)`, and a **`platform: ` prefix followed by a mandatory reason**, which `tools/devtool/stubskips.go` accepts and which the OS-gated `internal/paths` symlink tests actually use. Filter on all three (`grep -v -E 'Rule W-1\|Rule W-2\|platform: '`) or this row reports false violations on every host that skips a platform-gated test |
| M3 | Suite shape tests named per package | `go test -run 'Suite_ShapePassesAgainstStub' ./internal/...` | Every `Test<Pkg>Suite_ShapePassesAgainstStub` PASS. There are **seven**, not one per package: `analyzer`, `checkpoint`, `contract`, `ipc`, `mcp`, `observer`, `rehydrate`. The other suites assert shape through their own named tests instead, so "one per package" is the wrong completeness bar here — M1 and M2 are what cover the rest |
| M4 | `plans/OWNERS.tsv` is complete and authoritative | `cat plans/OWNERS.tsv` ; `go test -run TestV1_StubGraphIsInertAndOwned ./test/guards/` | Tab-separated `package owner floor probe`; contains all 23 stubbed packages **plus** the 10 SP-01 implements. **The enforcing check is IT-9, not `devtool lint`** — verified by deleting the `paths` row and re-running: `lint --only=stubskips` still exits 0, while IT-9 fails with `package "paths" exists on disk but has no plans/OWNERS.tsv row`. IT-9 checks the correspondence in both directions and also that every floor matches §6.4 |
| M5 | The behaviour assertions SP-01 authored are present (not empty shells) | inspect each `<pkg>test` file for the assertions listed in SP-01's suite table | `chunktest` has boundary-stability/determinism/size assertions; `canontest` idempotence + inverse; `sketchtest` round-trip/CRC/FP properties; `symbolstest` enclosing-span; `redacttest` idempotence + per-rule fixtures; `tokenstest` monotone/clamp/sum; `storetest` dedup + `ChangedSince` + `MarkEncoded` DPI guard; `dagtest` slice/thin/crossing; `negknowtest` three-way answer + bloom-as-cache; `grammartest` Sequitur's two invariants; `analyzertest` selector guards; `schedulertest` purity + trigger table + Young–Daly; `checkpointtest` schema round-trip + tier truncation + no-code-blocks; `pinstest` append-only + tombstone; `rehydratetest` item order + budget; `rulestest` glob + nested CLAUDE.md; `skillstest` index-only + budget; `mcptest` JSON-RPC conformance; `evaltest` determinism + Belady optimality; `ipctest` framing + ACK/NAK; `contracttest` degrade/restore; `observertest` tombstone + signals |

#### Group N — Golden contract fixtures (`testdata/golden/contracts/`, Rule W-2)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| N1 | Per-package `MANIFEST.json` present and well-formed | `ls testdata/golden/contracts/` ; `cat testdata/golden/contracts/store/MANIFEST.json` | One directory per package with a `MANIFEST.json` carrying `package`, `owner`, and a `fixtures[]` list with `name`, `kind` ∈ {`format`,`behaviour`}, `state` ∈ {`frozen`,`record-by-owner`}, `input`, `want` |
| N2 | Every `format` fixture is `frozen` and hand-authored | `grep -r '"kind": "format"' -A2 testdata/golden/contracts/` | Every `format` fixture has `state: "frozen"` and **either** a non-empty `want` **or** a non-empty `input`. The eleven `hookio` fixtures are deliberately input-only: the frozen artifact there *is* the wire form, and `testutil` documents that a parsed-form `want` would freeze a Go struct's marshalling rather than the host's payload shape — the opposite of what the fixture is for. A fixture with neither `input` nor `want` is empty and is the real failure. The frozen set covers: one `index/tool_use.jsonl` line, one `index/roots.jsonl` line, one `index/segments.jsonl` line, one `dag/deps.jsonl` node line and one edge line, one `records/eliminations.jsonl` record, one `pins/invariants.jsonl` add and one tombstone, one `checkpoints/MANIFEST.jsonl` entry, one complete `checkpoints/0001.json`, the Appendix C document, one `hookio.Event` per hook type with its parsed form, and the `contract` result set |
| N3 | Every `behaviour` fixture is correctly deferred | `grep -r '"kind": "behaviour"' -A2 testdata/golden/contracts/` | Every one has `state: "record-by-owner"` and an empty `want` |
| N4 | Recording refuses while an implementation is a stub | `go run ./tools/devtool gen-contract-fixtures --record store` | **Retired at V2 — do not run `--record store`:** `store` is real now, so the command would WRITE fixtures rather than refuse. The refusal contract is still enforced for stub-owned packages; exercise it with `--record negknow` (SP-09) instead |
| N5 | The frozen `checkpoints/0001.json` fixture obeys §13 invariant 5 (no code snippets) | `go test -run 'TestCheckpointSuite\|TestGuard' ./internal/checkpoint/... ./test/guards/...` ; manual: `grep -c '```' testdata/golden/contracts/checkpoint/want/0001.json` | The golden contains **zero** fenced code blocks; every tier is populated (`invariants`, `user_intent`, `eliminated`, `decisions`, `open_questions`, `current_work`, `pointers`, `narrative`, `sketch_refs`, `dropped`, `cache`) |
| N6 | The accessor skips cleanly on unrecorded fixtures | `go test -v -run TestContractFixture ./internal/testutil/...` ; `grep -n 'NotRecordedSkip' internal/testutil/fixtures.go` | The constant `testutil.NotRecordedSkip` is exactly `contract fixture not yet recorded (Rule W-2)` and is pinned by its own test. **Expect zero occurrences in test output at V1** — the original command greps a non-`-v` run, which never prints skip reasons at all, and even with `-v` the count is 0 because no consumer references a `record-by-owner` fixture yet. The message becomes observable when the first wave-1 owner records against one; asserting it appears *today* would require writing a skip with nothing to skip |

#### Group O — `internal/testutil` and `test/e2e`

| # | Functionality | Command | Expected result |
|---|---|---|---|
| O1 | Temp-project fixture | `go test -run 'TestProject_LayoutAndCleanup' ./internal/testutil/...` | PASS; full `.qompack` layout; `QOMPACK_PROJECT_ROOT` set; cleanup removes nothing outside `t.TempDir()` |
| O2 | `AssertAppendOnly` has teeth | `go test -v -run TestProject_AssertAppendOnly ./internal/testutil/...` | PASS against a correct layout **and** fails against the deliberately-weakened `paths.OpenFile` shim — proving the assertion is not vacuous |
| O3 | `FakeClock` determinism | `go test -run TestFakeClock_Deterministic ./internal/testutil/...` | PASS; `Advance` moves `Now`; `Since` exact; no wall-clock read |
| O4 | Golden helper | `go test -run TestGolden_UpdateFlag ./internal/testutil/...` | PASS; `-update` rewrites, absence compares; CRLF in the file causes no false failure |
| O5 | Windows-hostile fixtures (§6.2) | `go test -run TestWindowsHostileFiles_AllCreatable ./internal/testutil/...` | PASS; spaces-in-path, >260-char path, CRLF file, `0444` file all created; the case-colliding pair collapses to one file on a case-insensitive FS and the test asserts **that outcome explicitly** |
| O6 | Real-binary e2e hook run (DoD 13) | `go test -v -run TestE2E_AllSixHooksExitZero ./test/e2e/...` | PASS; the real binary is built once per test binary; all six hooks exit 0; every stdout parses as `hookio.Output`; the hook log gains six lines |
| O7 | Real-binary config surface | `go test -run TestE2E_ConfigPrintFromRealBinary ./test/e2e/...` | PASS; `config print --json` from the real binary deep-equals `Defaults()` for a project with no config file |
| O8 | `os/exec` allowlist honoured | `go run ./tools/devtool lint --only=bindeps,importgraph` ; inspect `internal/testutil/spawn.go` | Exit 0. In **non-test production code** `os/exec` appears only in `internal/testutil/spawn.go` and `tools/devtool/util.go`, and `testutil`'s use is isolated in `spawn.go` with an inline comment naming §6.2. Note the row previously named `internal/daemon` and `internal/cli`: neither imports `os/exec` at V1 (the daemon that would spawn does not exist yet, and the CLI is in-process). The complete V1 set, tests included, is `internal/paths` (two `_test.go` files), `internal/testutil/spawn.go`, `test/e2e/harness.go`, `test/guards` (four `_test.go` files) and `tools/devtool/util.go` — the composition roots and the harnesses, which is the shape §6.2 intends |

#### Group P — `test/guards` (closing-note, contract, write-set, network guards)

| # | Functionality | Command | Expected result |
|---|---|---|---|
| P1 | Closing note 1 — Phase 0 before Phase 1 | `go test -v -run TestGuard_Phase0BeforeStore ./test/guards/...` | PASS (both `eval` and `store` are stubs today, so the guard is satisfied and stays armed) |
| P2 | Closing note 2 — store + negknow before checkpoint | `go test -v -run TestGuard_StoreAndNegknowBeforeCheckpoint ./test/guards/...` | PASS |
| P3 | Closing note 3 — submodular inert without p-selection | `go test -v -run TestGuard_SubmodularInertWithoutPSelection ./test/guards/...` | PASS; `runtime.selection.submodularEnabled` and the derived `Selection.Submodular.Enabled` both **false**; `NewSelector` refuses `Pos < p` with `core.ErrBudget` |
| P4 | Closing note 3 — selector refuses while p-selection is absent | `go test -v -run TestGuard_SelectorRefusesWithoutPSelection ./test/guards/...` | PASS with `core.ErrNotImplemented` |
| P5 | Closing note 4 — O1 defaults on | `go test -v -run TestGuard_O1FlagDefaults ./test/guards/...` | PASS; `IncrementalSpanInstruction == true`, `Frontier.AdvanceOnSegmentClose == true`, `Frontier.MaxResidualTokens == 20000` |
| P6 | **A fresh build reports `ModeFull`** (§12.1, DoD 14) | `go test -v -run TestGuard_FreshBuildReportsModeFull ./test/guards/...` | PASS; `RunAll` returns `ModeFull`; every result with `Observed == "not-yet-implemented"` has `OK == true` and `Severity == SevInfo`. A `degraded-passive` result here would silently disable the very paths waves 1–2 will test |
| P7 | Write set confined to `.qompack` (§13 invariant 7) | `go test -v -run TestGuard_WriteSetConfinedToQompack ./test/guards/...` | PASS; every created/modified path across all six hooks is under `<root>/.qompack/` or `<home>/.qompack/` |
| P8 | No network imports (D10) | `go test -v -run TestGuard_NoNetworkImports ./test/guards/...` | PASS; `net/http`, `net/url`, `crypto/tls` absent from every non-test package; `net` present **only** in `internal/ipc` |

#### Group Q — CI pipeline

| # | Functionality | Command | Expected result |
|---|---|---|---|
| Q1 | `ci.yml` job set matches §8 | `cat .github/workflows/ci.yml` | Jobs: `verify`, `test` (ubuntu/macos/windows × go 1.26.x), `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`. Triggers: push to `**`, PR into `develop`/`main`. `GOTOOLCHAIN: local`, `CGO_ENABLED: '0'` |
| Q2 | Attribution-trailer and Conventional-Commit gates exist in CI | inspect the `verify` job | Two run-steps: one greps the commit range for `Co-Authored-By\|Signed-off-by\|Generated with\|🤖` and fails on a match; one validates every subject against the §10 regex **and, as a separate `case` test, rejects a trailing period**. The two must be separate: `.` in `.{1,64}` matches a literal period like any other character, so the pattern alone can never reject one — `tools/devtool/checkcommitmsg.go` has always checked it separately for exactly this reason, and CI did not until this checkpoint |
| Q3 | Gates correctly not-yet-required | inspect `bench-gate` and `replay-gate` | Both carry `continue-on-error: true` with an inline comment naming the subplan that removes it (SP-05 for bench-gate, SP-02 for replay-gate) |
| Q4 | Security job asserts the §8 import allowlists | inspect the `security` job | Runs `govulncheck`, `devtool lint --only=importgraph,testdeps,bindeps`, and the two `grep`-based allowlists for `net/http\|net/url\|crypto/tls` and `os/exec` |
| Q5 | Nightly and release workflows present | `cat .github/workflows/nightly.yml .github/workflows/release.yml` | Nightly: cron `0 3 * * *`, fuzz 10 min/target over the **eight** declared targets (six of which are waived while their package is a stub — see N5/N6 and `TestNightlyFuzzMatrix`), Windows `-race`, `bench-hotpath --iterations 5000`, live replay gated on `QOMPACK_SESSIONS_DIR`. Release: tag-triggered `v*`, `goreleaser release --clean` |
| Q6 | `.goreleaser.yaml` six targets | `cat .goreleaser.yaml` | Six §2.6 targets, `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X …/internal/core.Version={{.Version}}"`, `checksum.name_template: checksums.txt` |
| Q7 | Issue templates and CODEOWNERS | `ls .github/ISSUE_TEMPLATE/` ; `cat .github/CODEOWNERS` | `bug.yml` and `upstream-tracker.yml` present; `upstream-tracker.yml` enumerates the five §12 upstream issues; CODEOWNERS covers `*` and `/plans/` |
| Q8 | CI is green on `verify/v1` (DoD 17) | push the branch and inspect the run | `verify`, `test` ×3 OS, `cover`, `crossbuild`, `plugin-validate`, `security`, `docs` **green**; `bench-gate` and `replay-gate` run and report their not-present message |

#### Group R — Whole-tree test execution

| # | Functionality | Command | Expected result |
|---|---|---|---|
| R1 | Full suite (DoD 5) | `go test ./...` | Exit 0. Zero failures. Zero skips other than the **three** permitted messages — Rule W-1, Rule W-2, and a `platform: ` prefix with a mandatory reason (see M2). On a non-Windows host the OS-gated `internal/paths` symlink tests take the third form, so a two-message expectation fails this row on Linux and macOS |
| R2 | Race detector (DoD 6) | `go test -race ./...` (Linux/macOS; Windows nightly) | Exit 0; no data races |
| R3 | Windows repeat run (DoD 6) | `go test -count=2 ./...` on Windows | Exit 0; no order-dependent or state-leaking test |
| R4 | Build and vet on all three platforms (DoD 2) | `go build ./...` and `go vet ./...` on Windows locally + Linux/macOS in CI | Exit 0 everywhere |
| R5 | Coverage floors for SP-01-implemented packages (DoD 7) | `go run ./tools/devtool cover` | Exit 0. Binding this wave: `config` ≥ 90%, `paths` ≥ 90%; `core`, `logging`, `obs`, `tokens`, `hookio`, `cli`, `pluginmanifest`, `testutil` ≥ 75%. `cover` fails if `OWNERS.tsv` claims `SP-01` for a package whose probe still reports `ErrNotImplemented` |

---

## 2. Exit-criteria re-verification

Each completed subplan's exit criteria, **quoted**, with the concrete measurement procedure.

### SP-01 — quoted from `Qompack.md` (criteria SP-01 must *make measurable* for later waves)

> **Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions. *(Phase 0 — SP-02 achieves it; SP-01 ships `eval.minSessions: 20` as the config default and the `replay-gate` job that will enforce it.)*

**Measurement now.** Do **not** attempt to produce the number — SP-02 owns it.
Verify only that SP-01 made it measurable:
1. `go test -run TestDefaults_MatchesAppendixCVerbatim ./internal/config/...` → `eval.minSessions == 20` in the golden.
2. `go run ./cmd/qompack config print --json | grep -A2 '"eval"'` → `"minSessions": 20`, `"replayOnPhaseGate": true`.
3. `grep -n 'replay-gate' -A6 .github/workflows/ci.yml` → the job exists, invokes
   `devtool replay --corpus testdata/sessions/synthetic --baseline develop`, and is
   `continue-on-error: true` with SP-02 named inline.
4. `go run ./tools/devtool replay` → prints `replay: driver not present (owned by SP-02)`, **exit 0**.

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms. *(Phase 1 — SP-06 achieves the dedup ratio and SP-08 the hook p99, both measured by SP-02's replay corpus; SP-01 ships `store.Stats.DedupRatio`, budget B-A and the bench-harness contract that will measure them.)*

**Measurement now.** The ratio is **not measurable at V1** — `store` is a stub. Record it as
`N/A — SP-06` in the report table; do not fabricate a number. Verify the affordances exist:
1. `go doc ./internal/store Stats` → the struct declares `DedupRatio float64` with the "RawBytes / Bytes — the Phase 1 exit criterion (≥ 4:1)" comment.
2. `go test -run TestBudgets_AllSixPresentAndConfigDriven ./internal/obs/...` → B-A exists, is gated at p99, limit read from `runtime.hotPath.budgetMs` = 15 ms.
3. `go run ./tools/devtool bench-hotpath --iterations 2000 --json bench.json` → `bench-hotpath: harness not present (owned by SP-05)`, exit 0.
4. The wave-0 headroom proxy for B-A is `BenchmarkHookNoop_InProcess` — see §5 B-A-proxy.

### SP-01 — guardrails it must express as configuration and CI, quoted from §11.3

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

**Measurement procedure.**
1. **15 ms / 2 s as config keys, not literals:** `go test -run TestBudgets_AllSixPresentAndConfigDriven ./internal/obs/...` (B-A ← `runtime.hotPath.budgetMs = 15`; B-E ← `runtime.budgets.checkpointFinalizeMs = 2000`), plus `go run ./tools/devtool lint --only=nomagic` proving neither number appears as a literal outside `internal/config/defaults.go`.
2. **Sublinear store growth:** not measurable at V1 (`store` is a stub). Record `N/A — SP-06`.
3. **2% rule:** encoded in the `replay-gate` job; verify the job text mentions the sign-off trailer requirement (`grep -n 'sign-off' .github/workflows/ci.yml` or the `devtool replay` help text). See §6 for the V1 application of this rule.
4. **Every phase gate runs the full replay suite:** `eval.replayOnPhaseGate == true` in `Defaults()`; `replay-gate` job wired.

### SP-01 — "Local, measurable Definition of Done", all 18 items

Each is quoted and mapped to the inventory item that measures it. **All 18 must pass.**

> 1. `git log --oneline develop..feat/sp01-foundation-toolchain-and-contracts` shows exactly 7 commits (commit 1 is the shared root of `main` and `develop`), each a valid Conventional Commit, none containing an attribution trailer.

Procedure: the branch is merged now, so run the post-merge equivalent —
`git log --oneline --no-merges $(git rev-list --max-parents=0 HEAD)..develop` → **7** commits; then A3, A4, A5. Expected: 7 commits, all conforming, zero trailers.

> 2. `go build ./...` and `go vet ./...` exit 0 on Windows, Linux and macOS.

Procedure: R4 locally on Windows plus the CI `verify` and `test` jobs for Linux/macOS.

> 3. `go run ./tools/devtool lint` exits 0: `golangci-lint`, `nomagic`, `importgraph`, `testdeps`, `bindeps`, `sleepcheck`, `stubskips` all clean. In particular `importgraph` passes against the real repository, and `bindeps` proves `go list -deps ./cmd/qompack` contains only stdlib, `github.com/qompack/qompack/…`, zstd and go-winio — so `golang.org/x/tools` in the root `go.mod` never reaches the shipped binary.

Procedure: B5 + B8 + B10 + B11 + B12 + B13.

> 4. `go run ./tools/devtool fmt-check` prints nothing.

Procedure: B4.

> 5. `go test ./...` exits 0; all 22 conformance suites report a skipped behaviour block, and every skip emitted anywhere in the tree carries the message `behaviour: implementation is a stub (Rule W-1)` or `contract fixture not yet recorded (Rule W-2)` — no other skip reason is permitted.

Procedure: R1 + M1 + M2. The M2 negative search (no other skip reason) is the load-bearing half.

> 6. `go test -race ./...` exits 0 on Linux and macOS; `go test -count=2 ./...` exits 0 on Windows.

Procedure: R2 + R3.

> 7. `go run ./tools/devtool cover` meets every §6.4 floor for the packages SP-01 **implements**, and exempts every package it only **stubs** … printing `exempt (stub, owned by <SP-NN>)` for each …

Procedure: R5 + L19. Confirm the exemption lines are printed (visible, not hidden in a constant).

> 8. `go run ./tools/devtool plugin-validate` exits 0 and `git diff --exit-code -- plugin/` is clean.

Procedure: K6.

> 9. `go run ./tools/devtool gen-config-docs --check` exits 0.

Procedure: E14.

> 10. `go run ./tools/devtool build-all` produces all six §2.6 targets.

Procedure: B15.

> 11. `TestDefaults_MatchesAppendixCVerbatim` passes — `config.Defaults()` minus `runtime` is byte-equivalent to Appendix C.

Procedure: E1.

> 12. `TestAppendOnlyGuard` passes: all five illegal write attempts against `checkpoints/`, `pins/` and `sketches/tried.bloom` fail.

Procedure: D5. Run with `-v` and confirm **five** distinct sub-assertions, each with the expected error type.

> 13. `TestDispatch_HookAlwaysExitsZero` passes all 30 fault-injection combinations, and `TestE2E_AllSixHooksExitZero` passes against the real built binary.

Procedure: J1 + O6. Run J1 with `-v` and count 30 sub-tests.

> 14. `TestGuard_FreshBuildReportsModeFull` passes — a freshly built `develop` reports `ModeFull` with every not-yet-implemented assertion at `OK:true, SevInfo` (§12.1).

Procedure: P6.

> 15. All four closing-note guards pass, and `TestGuard_NoNetworkImports` and `TestGuard_WriteSetConfinedToQompack` pass.

Procedure: P1–P5, P7, P8.

> 16. Benchmarks within budget: `BenchmarkHistogram_Observe` < 100 ns/op, `BenchmarkConfigLoad_ColdNoFiles` < 2 ms/op, `BenchmarkHookNoop_InProcess` < 3 ms/op, `BenchmarkPathsWriteAtomic_4KB` < 2 ms/op; results committed to `testdata/bench-baseline.txt`.

Procedure: §5 items P-1 … P-4, plus `git show HEAD:testdata/bench-baseline.txt` containing the four entries.

> 17. CI is green on the branch for `verify`, `test` (3 OS), `cover`, `crossbuild`, `plugin-validate`, `security`, `docs`; `bench-gate` and `replay-gate` run and report their not-present message.

Procedure: Q8.

> 18. `Qompack.md` is byte-identical to its state at commit 1 (`git diff <root-commit> HEAD -- Qompack.md` is empty).

Procedure: A9.

### SP-01 — "Done checklist" items not already covered above

| Quoted item | Procedure |
|---|---|
| > Every §5 interface has a compiling stub with the exact normative signature — checked by diffing the declared signatures against `00-ARCHITECTURE.md` §5 package by package | For each of the 23 stubbed packages run `go doc -all ./internal/<pkg>` and diff the exported signature set against §5 of `00-ARCHITECTURE.md`. **Any deviation is an architecture amendment, not a fix** — see §7. Fan this across subagents; it is the single highest-value manual check in V1, because every wave-1 subplan is about to build against these signatures |
| > Every §5 interface has a `<pkg>test` conformance suite (22 suites, `storetest` exporting two) … `plans/OWNERS.tsv` lists every package on disk with its owner, §6.4 floor and stub probe | M1, M4 |
| > `testdata/golden/contracts/` contains the frozen `format` fixtures and a `MANIFEST.json` per package correctly marking `behaviour` fixtures `record-by-owner` | N1, N2, N3 |
| > Type consistency with the Interface contract section: `core.Dep`, `core.ChunkRef`, `core.Hash`, `core.Clock`, the seven sentinels, `config.Config`/`Provenance`/`Violation`/`Warning`, `paths.Layout`, `obs.Histogram`/`Registry`/`BudgetBreach`, `hookio.Event`/`Output`/`HSO`, `logging.Logger` | C5, E-group, D-group, G-group, I-group; plus `go doc` diff as above |
| > Placeholder scan clean | A11 |
| > `Qompack.md` unmodified | A9 |
| > `go run ./tools/devtool ci-local` green | B17 |

---

## 3. Architecture-invariant re-verification (§13, all ten)

These are cross-cutting and must be re-checked at every checkpoint, because a later wave can break
one without breaking a package test.

| Invariant (§13) | Procedure at V1 | Expected |
|---|---|---|
| 1. Never compress a compression | `go doc ./internal/checkpoint SourceSet` ; `go doc ./internal/store SegmentLog` | `SourceSet` has **no** field able to carry live context text (only `Store`, `Segments`, `Ledger`, `Pins`, `Graph`, `Grammar`, `Tokens`); `SegmentLog.MarkEncoded` is declared with the `ErrAlreadyEncoded` contract; the injection tags exist (L9) |
| 2. Append-only means append-only | D5, D6, D8, O2 | All pass |
| 3. Bloom is a cache, never source of truth | `go doc ./internal/negknow Answer Ledger` | `Answer.BloomOnly` exists; `RebuildBloom` is documented as taking active records only; `paths.ReplaceBloom` is the only replacement path (D8) |
| 4. Nothing scattered before `p` | L14 / P3 | `NewSelector` refuses `Pos < p` with `core.ErrBudget`, checked **before** the ship-order error |
| 5. No code snippets in checkpoints | N5 | Zero fenced blocks in the checkpoint golden |
| 6. Hooks exit 0, always | J1 (30 combinations), J10, O6 | All exit 0 |
| 7. No network, no telemetry, no writes outside `.qompack/` | P7, P8, Q4, and `TestValidate_TelemetryMustBeFalse` (E9) | All pass |
| 8. Every §12-volatile constant is a config key | B6, B7, E2, G2 | `nomagic` armed and non-vacuous; no `12.5`; budgets config-driven |
| 9. Every latency budget is measured, not assumed | §5 below | Four benchmarks run and recorded |
| 10. Degradation is loud | F2, F3, P6, E7 | Loud reaches three destinations, is counted, and `Degrade` persists state |

---

## 4. New cross-component integration tests

These tests are **authored during this checkpoint** and become a permanent part of the suite. They
exist because SP-01's packages now coexist on one branch: every one of them crosses at least three
package boundaries, and none of them could have been written inside a single package's tests.

**Honest scoping note.** Wave 0 delivers no store, no observer, no checkpointer and no retrieval
layer, so the "hook event → store → tombstone → retrieval round-trip" seam **does not exist yet**
and must not be simulated. The seams that *do* exist at wave 0 are: the hook wire format → CLI
dispatch → config load → layout → hook log; the plugin manifest → the real binary; the append-only
guard → every write path; the contract monitor → degradation state; and the stub graph itself,
whose correct behaviour is to be inert. Test those, exhaustively, and write the round-trip test in
V2 when the store lands.

Place these in `test/e2e/v1_integration_test.go` and `test/guards/v1_integration_test.go` (both are
composition roots, so importing across the tree is legal — §3.2). Every test uses
`testutil.NewProject`, `testutil.FakeClock`, and a per-test `HOME` override; **no wall-clock
sleeps** (`devtool lint --only=sleepcheck` must stay clean).

### IT-1 — `TestV1_HookLifecycleThroughRealBinary` (`test/e2e`)

- **Crosses:** `pluginmanifest` → `cmd/qompack` → `cli` → `hookio` → `config` → `paths` → `logging`.
- **Setup:** `e2e.Build(t)` for the real binary; `testutil.NewProject(t, testutil.WithGit(), testutil.WithClock(2026-01-01T00:00:00Z))`; `HOME`/`USERPROFILE` pointed at a second `t.TempDir()`.
- **Inputs:** the seven golden hook payloads from `testdata/golden/contracts/hookio/input/`, fed on stdin in the realistic order: `SessionStart(source=startup)` → `UserPromptSubmit` → `PostToolUse`(FileRead) → `PostToolUse`(Bash) → `Stop` → `PreCompact(trigger=auto)` → `SessionEnd`. `SubagentStop` is run as an eighth call with `--subagent`.
- **Expected outputs:**
  - exit code `0` for all eight invocations;
  - every stdout unmarshals into `hookio.Output` with no trailing garbage;
  - `session-start` stdout equals `{"hookSpecificOutput":{"hookEventName":"SessionStart"}}`;
  - `checkpoint` stdout equals `{"hookSpecificOutput":{"hookEventName":"PreCompact"}}`;
  - the other six produce exactly `{}`;
  - `.qompack/logs/hooks-<date>.jsonl` has **exactly 8** lines, in call order, each with `hook` equal to the invoked subcommand name, `session_id` echoing the payload, `bytes > 0`, `truncated == false`;
  - the log file contains **none** of: the prompt text, the tool_response body, or any file path from the payload (assert by substring search) — proving the wave-0 hooks are observable without storing content;
  - `.qompack/.gitignore` exists with content exactly `*\n`.

### IT-2 — `TestV1_ConfigPrecedenceReachesHookBehaviour` (`test/e2e`)

- **Crosses:** `config` (five layers) → `cli` bootstrap ordering → `hookio` limit → `logging`.
- **Setup:** fake `HOME` with `~/.qompack/config.json` setting `runtime.hotPath.maxPayloadBytes = 4096`; project `.qompack/config.json` setting it to `8192`.
- **Inputs:** three sub-cases, each running the real binary `observe tool` with a synthesized `PostToolUse` payload of **12 KiB**:
  1. no env override;
  2. `QOMPACK_RUNTIME__HOTPATH__MAXPAYLOADBYTES=16384`;
  3. no env, but `--set runtime.hotPath.maxPayloadBytes=32768`.
- **Expected outputs:**
  - all three exit `0` and print `{}`;
  - case 1 → hook-log line has `truncated: true` (effective limit 8192 < 12288) and the day log carries one `level=warn` line naming the payload limit;
  - cases 2 and 3 → `truncated: false`, no warn line;
  - `qompack config print --provenance` in case 2 annotates `runtime.hotPath.maxPayloadBytes` with `OriginEnv`, and in case 3 with `OriginFlag`;
  - in every case the hook read stdin under the **default** 1 MiB bootstrap limit first — assert by feeding a 2 MiB payload in a fourth sub-case and expecting exit `0` with `{}` and a hook-log line recording `bytes` clamped at the bootstrap limit, never a crash.

### IT-3 — `TestV1_AppendOnlyInvariantSurvivesRealHookRun` (`test/guards`)

- **Crosses:** `cli` hooks → `paths` layout/append-only → `testutil.AssertAppendOnly`.
- **Setup:** temp project; run the eight-hook sequence of IT-1 **in-process** (`(*Project).RunHook`) so the layout is created by real code, then hand-place a `checkpoints/0001.json` via `paths.CreateNew`, a `pins/invariants.jsonl` line via `paths.AppendJSONL`, and a `sketches/tried.bloom` via `paths.ReplaceBloom`.
- **Inputs:** the five illegal writes of `TestAppendOnlyGuard`, executed against this **live, hook-created** layout rather than a hand-built one.
- **Expected outputs:** all five fail with `core.ErrAppendOnly` or `os.ErrExist`; `checkpoints/0001.json` is read-only on disk; `p.AssertAppendOnly(t)` passes; re-running the whole hook sequence a second time neither truncates nor rewrites any protected file (assert by comparing SHA-256 of all three before and after).

### IT-4 — `TestV1_WriteSetConfinedAcrossFullHookSequence` (`test/guards`)

- **Crosses:** every SP-01 package that touches the filesystem, through the **real binary**.
- **Setup:** temp project root and temp `HOME`; snapshot the complete file tree of both, plus the OS temp dir, before the run.
- **Inputs:** the eight-hook sequence from IT-1, executed as separate processes.
- **Expected outputs:** every created or modified path is under `<root>/.qompack/` or `<home>/.qompack/`; the OS temp dir gains nothing that outlives the run; no file is written to the repository working tree; `.qompack/tmp/` is empty at the end (no `wa-*` leftovers).

### IT-5 — `TestV1_PluginManifestCommandsExecuteAgainstRealBinary` (`test/e2e`)

- **Crosses:** `pluginmanifest` → `plugin/hooks/hooks.json` → `cli` dispatch table → `hookio`.
- **Setup:** build the real binary into `<tmp>/bin/qompack[.exe]`; parse the committed `plugin/hooks/hooks.json`; substitute `${CLAUDE_PLUGIN_ROOT}` with `<tmp>`.
- **Inputs:** for each of the seven manifest entries, execute the declared `command` string verbatim (argv-split as a shell would), feeding the matching golden payload on stdin.
- **Expected outputs:**
  - all seven exit `0`;
  - all seven stdouts parse as `hookio.Output`;
  - each invocation's measured wall time is **below its declared timeout** (`PostToolUse` 5 s, `UserPromptSubmit` 5 s, `SessionStart` 15 s, `PreCompact` 20 s, `Stop` 5 s, `SubagentStop` 10 s, `SessionEnd` 20 s) — the measurement is recorded into the report as headroom, not gated tightly;
  - the `matcher` on `PostToolUse` is `"*"`;
  - the set of subcommands the manifest references is a **subset** of the names `cli.All()` registers (assert programmatically — this is the seam that silently breaks when a subcommand is renamed).

### IT-6 — `TestV1_StubGraphIsInertAndOwned` (`test/guards`)

- **Crosses:** all 23 stub packages → `plans/OWNERS.tsv` → the on-disk package set.
- **Setup:** read `plans/OWNERS.tsv`; enumerate `internal/*` directories from disk.
- **Inputs:** for every row whose `probe` is not `-`, construct the package's value via its documented constructor and call the named probe method with zero-valued arguments.
- **Expected outputs:**
  - every constructor returns a non-nil value and a **nil** error;
  - every probe returns an error satisfying `core.IsNotImplemented`;
  - no probe returns a non-zero data payload alongside that error (assert with `reflect.DeepEqual` against the zero value) — this is invariant "no behaviour is faked";
  - the `OWNERS.tsv` package set **equals** the on-disk `internal/` package set plus `cmd/qompack` (no missing rows, no phantom rows);
  - every row's `floor` matches the §6.4 group table for that package.

### IT-7 — `TestV1_ContractMonitorDegradesAndRestoresLoudly` (`test/guards`)

- **Crosses:** `contract` → `logging` (Loud) → `obs` → `paths` (state file) → `config`.
- **Setup:** temp project; `obs.New(fakeClock)`; a real file logger; `contract.NewMonitor(log, reg, <state>/contract.json)`; register `contract.StandardAssertions()`.
- **Inputs:** (a) `RunAll` on a clean env; (b) `RunAll` after registering one synthetic assertion that returns `OK:false, Severity:SevCritical`; (c) two further clean `RunAll`s with that assertion removed.
- **Expected outputs:**
  - (a) → `ModeFull`, every standard result `OK:true / SevInfo / Observed:"not-yet-implemented"`, **zero** `Loud` lines;
  - (b) → `ModeDegradedPassive`; `<state>/contract.json` exists and names the failed assertion with expected vs. observed; `LOUD.log` gains exactly one line; `logging.LastLoud()` returns it; `reg.Counter("loud.total").Value() == 1`;
  - (c) → after two consecutive clean runs, `Monitor.Restore` fires, mode returns to `ModeFull`, and the restoration is logged **just as loudly** (a second `LOUD.log` line, `loud.total == 2`);
  - at no point is a state transition silent (assert `LOUD.log` line count equals the number of transitions).

### IT-8 — `TestV1_ObsBudgetsAreConfigDrivenEndToEnd` (`test/guards`)

- **Crosses:** `config` → `obs` budget table → `obs.Registry.CheckBudgets`.
- **Setup:** two configs: `Defaults()`, and `Defaults()` with `--set runtime.hotPath.budgetMs=1` applied through `config.Load` (not by mutating the struct — the point is to test the load path).
- **Inputs:** feed a histogram named for B-A with 512 observations of exactly 10 ms; call `CheckBudgets(cfg)` under each config, three times in a row.
- **Expected outputs:** under defaults, **no** breach (10 ms < 15 ms); under the 1 ms config, one `BudgetBreach{Budget:"B-A", Observed:≈10ms, Limit:1ms}` per call, with `Windows` incrementing `1 → 2 → 3`; B-C and B-D never appear in the breach list regardless of observations (`Gated == false`); B-F is evaluated at **p95**, not p99.

### IT-9 — `TestV1_FrozenContractFixturesRoundTripIntoDeclaredTypes` (`test/guards`)

- **Crosses:** `testdata/golden/contracts/**` → `hookio`, `checkpoint`, `pins`, `negknow`, `store`, `dag`, `paths`, `config`, `contract` type declarations.
- **Setup:** walk every `MANIFEST.json`; select fixtures with `kind == "format"` and `state == "frozen"`.
- **Inputs:** each frozen `want/` file, unmarshalled into the Go type its manifest entry names.
- **Expected outputs:**
  - every fixture unmarshals with **no unknown-field loss** (re-marshal and compare byte-for-byte after canonical key ordering);
  - the `checkpoints/0001.json` fixture populates every tier-1, tier-2, tier-3 and metadata field of `checkpoint.Checkpoint`, and contains **zero** fenced code blocks (§13 invariant 5);
  - the `hookio` fixtures cover all seven hook event names and all four `SessionStart` sources;
  - the `contract` fixture's result set contains the nine §5.19 assertion IDs;
  - every `kind == "behaviour"` fixture is `record-by-owner` with an empty `want`, and the accessor `testutil.ContractFixture` reports `frozen == false` for it.

### IT-10 — `TestV1_ToolchainGatesRejectRealViolations` (`test/guards`, build-tag `guardprobe` or `t.TempDir`-based fixtures)

- **Crosses:** `tools/lint/nomagic`, `tools/devtool/importgraph`, `tools/devtool/testdeps`, `tools/devtool/bindeps` → the real rule tables.
- **Setup:** synthesize package listings and Go source in `t.TempDir()`; never mutate the repository.
- **Inputs:** (a) a file containing `0.55`, `1.25`, `12.5`, `20000` and one `//nomagic:allow tuned by hand` line, plus one **bare** `//nomagic:allow`; (b) a package list containing `store → negknow`; (c) a package list where `internal/scheduler` imports `testify`; (d) a dependency list for `cmd/qompack` containing `golang.org/x/tools`.
- **Expected outputs:** (a) four reports for the literals, none for the annotated line, **one** report for the bare `//nomagic:allow` (missing reason); (b) rejected with a message naming §3.2; (c) rejected naming the package; (d) rejected naming `golang.org/x/tools`. This proves the gates that protect every later wave are not vacuous — the single most common silent-failure mode in a checkpoint like this.

### Authoring rules for these tests

- They must pass on Windows, Linux and macOS. Use `paths.Long` for any deep path, `filepath.Join` everywhere, and never assume `/tmp`.
- They must not depend on wall-clock time — use `testutil.FakeClock` and, for the IT-5 timeout headroom measurement, `time.Since` around a subprocess (permitted: that is a measurement, not a sleep).
- They must clean up: `t.TempDir()` only; `t.Setenv` for `HOME`/`USERPROFILE`/`QOMPACK_PROJECT_ROOT`.
- After authoring, re-run `go run ./tools/devtool lint` (importgraph now sees new files in the two composition roots) and `go test -race ./test/...`.

---

## 5. Performance budget validation

Every latency/size budget **in force at this point**. A budget whose component does not exist yet is
listed with an explicit `N/A — <owner>` so nobody mistakes silence for success.

Run these **serially, in the main session, on an otherwise idle machine.** Record every number into
the report table and into `testdata/bench-baseline.txt`.

| ID | Budget (source) | Status at V1 | Measurement command | Threshold / expected |
|---|---|---|---|---|
| **P-1** | `BenchmarkHistogram_Observe` — feeds B-B (`00-ARCH` §7, SP-01 DoD 16) | **In force** | `go test -run '^$' -bench BenchmarkHistogram_Observe -benchmem -count 5 ./internal/obs/` | **< 100 ns/op**; allocations 0 B/op |
| **P-2** | `BenchmarkConfigLoad_ColdNoFiles` — config load must not eat B-A's 15 ms (SP-01 DoD 16) | **In force** | `go test -run '^$' -bench BenchmarkConfigLoad_ColdNoFiles -benchmem -count 5 ./internal/config/` | **< 2 ms/op** |
| **P-3** | `BenchmarkHookNoop_InProcess` — the wave-0 headroom proxy for **B-A p99 < 15 ms** (§11.3, §8.1) | **In force (proxy)** | `go test -run '^$' -bench BenchmarkHookNoop_InProcess -benchmem -count 5 ./internal/cli/` | **< 3 ms/op** for `observe tool` end to end in-process. Record the number: it is the headroom against 15 ms that SP-05's daemon and SP-08's observer will spend |
| **P-4** | `BenchmarkPathsWriteAtomic_4KB` (SP-01 DoD 16) | **In force** | `go test -run '^$' -bench BenchmarkPathsWriteAtomic_4KB -benchmem -count 5 ./internal/paths/` | **< 2 ms/op** on CI-class hardware. **Read the allocation columns, not just sec/op.** This benchmark is fsync-bound and the threshold is unreachable on a host whose fsync alone costs more than 2 ms — which includes the Windows/NTFS dev host `testdata/bench-baseline.txt` was recorded on, where it is already annotated `MISSED`. Before treating an overrun as a code defect, isolate the host: measure create+write+close with and without `f.Sync()`. If the delta accounts for the overrun, the finding is the machine. `B/op` and `allocs/op` are the platform-independent halves and a change there **is** a code change |
| **P-5** | **B-A** `hook_controlled` p99 < 15 ms (§11.3 L0, §2.4, §8.1) — the real spawn-inclusive measurement | **Harness N/A — SP-05** | `go run ./tools/devtool bench-hotpath --iterations 2000 --json bench.json` | Prints `bench-hotpath: harness not present (owned by SP-05)`, **exit 0**. Verify instead that the budget is *expressed*: `go test -run TestBudgets_AllSixPresentAndConfigDriven ./internal/obs/` shows B-A gated at p99 with limit from `runtime.hotPath.budgetMs = 15`. Report `N/A — SP-05` with P-3 as the standing proxy |
| **P-6** | **B-B** `l0_ingest` p99 < 2 ms | **N/A — SP-05** | as P-5 | Budget key present (`runtime.budgets.l0IngestMs = 2`), `Gated == true`; no daemon exists to measure |
| **P-7** | **B-C** `l0_process` p99 < 50 ms (soft) | **N/A — SP-05/SP-06** | as P-5 | Budget key present (`l0ProcessMs = 50`), `Gated == false` — the soft treatment is itself the thing to verify |
| **P-8** | **B-D** `hook_wall` (reported, never gated) | **Partially measurable** | IT-5 records per-hook subprocess wall time; also `go run ./tools/devtool build && <time the 8 real-binary invocations of IT-1>` | Recorded, **never gated** (§2.4: process creation is the host's cost). Report the p50/max of the eight spawns as the wave-0 B-D reference point. On Windows expect 6–20 ms of pure spawn floor per §2.1 — that is expected and is not a failure |
| **P-9** | **B-E** `checkpoint_finalize` p99 < 2 s (§11.3 L4) | **N/A — SP-10** | `go test -run TestBudgets_AllSixPresentAndConfigDriven ./internal/obs/` | Budget expressed: `runtime.budgets.checkpointFinalizeMs = 2000`, p99, `Gated == true`. The `checkpoint` subcommand is a no-op today; measure its wall time in IT-5 anyway and record it (expected: dominated by process spawn, far under 2 s) |
| **P-10** | **B-F** `mcp_tool_call` p95 < 250 ms | **N/A — SP-13** | as P-9 | Budget expressed: `mcpToolCallMs = 250`, evaluated at **p95** |
| **P-11** | **Store dedup ratio ≥ 4:1** (Phase 1 exit criterion, §11.3) | **N/A — SP-06** | `go doc ./internal/store Stats` | `Stats.DedupRatio` declared with the ≥ 4:1 comment. **Record `N/A — SP-06`. Do not fabricate a ratio** |
| **P-12** | **Store growth sublinear in session length after dedup** (§11.3) | **N/A — SP-06** | — | Record `N/A — SP-06` |
| **P-13** | Micro-benchmark regression gate (§7): >10% warns, >25% fails vs. `testdata/bench-baseline.txt` | **Baseline-setting run** | `go test -run '^$' -bench . -benchmem -count 10 -p 1 ./... > new.txt` ; `go run -modfile=tools/pinned/go.mod golang.org/x/perf/cmd/benchstat testdata/bench-baseline.txt new.txt` (**`-count 10`, not 5**: benchstat needs at least 6 samples per side to report a confidence interval, and the committed baseline was recorded at 10 — a 5-sample run can only be compared point-to-point, which is what the header of `testdata/bench-baseline.txt` says) | V1 is the first checkpoint, so the committed baseline is SP-01's. Expect **no** benchmark more than 10% worse than the committed baseline; if the machine differs materially from the one that produced the baseline, note it in the report and re-record the baseline as part of this checkpoint's commits |
| **P-14** | Binary size sanity (§2.2 "a single 12–20 MB static binary") | **In force** | `go run ./tools/devtool build` then `ls -l bin/qompack*` (`.exe` on Windows) | 8–25 MB **for the finished binary**. At V1 expect **far less** — 23 of the 33 packages are `ErrNotImplemented` stubs, so there is almost nothing to link; ~2–3 MB is the correct answer here and an 8 MB floor would fail this row for the wrong reason. The upper bound is the live half of this check today: exceeding 25 MB at V1 would mean an accidental dependency. Neither bound is a hard gate |

**Recording rule.** Every number produced above goes into the §8 report table *and* into
`testdata/bench-baseline.txt` if it is one of P-1 … P-4. A checkpoint that reports "budget met"
without a number is a §1.3 RC-3 failure — the exact sin this project exists to fix.

---

## 6. Regression

### 6.1 Prior verification checkpoints

**V1 has no predecessors.** V1 is the first verification checkpoint in the build; the wave map
(`00-ARCHITECTURE.md` §14) lists V1 … V6, and this is V1. There is therefore no earlier
checkpoint inventory to re-run.

What replaces it: **V1's own inventory (§1) is the regression baseline for V2 … V6.** Two
consequences you must act on now:

1. Every command in §1 must be reproducible by a later checkpoint. If any command in §1 turned out
   to be wrong, ambiguous, or non-reproducible while you ran it, **fix the command text in this
   file** as part of this checkpoint's commits, so V2 inherits a correct list.
2. The §8 completion report, once filled in, is committed to `verify/v1` and becomes the
   comparison target for V2's regression section.

### 6.2 The 2% no-regression guardrail (§11.3)

Quoted verbatim:

> - No metric may regress by more than 2% to improve another without explicit sign-off

**Application at V1.** There is no prior metric set, so nothing can regress against a predecessor
checkpoint. The rule still binds in two places:

1. **Against SP-01's committed benchmark baseline.** `testdata/bench-baseline.txt` was recorded on
   the SP-01 branch. Run P-13. Any benchmark more than 2% worse **that was made worse in order to
   improve something else** requires an explicit `sign-off:` note in the commit body naming what
   was traded for what. A regression with no compensating improvement is simply a bug — fix it,
   do not sign it off. Pure environmental noise (different machine, different CPU governor) is not
   a regression: re-record the baseline in a `perf:` commit and say so in the body.
2. **Against the replay gate.** `replay-gate` enforces the 2% rule mechanically from the end of
   wave 1 (§8: "required checks on `develop` and `main` from the end of wave 1 onward"). At V1 it
   correctly prints `replay: driver not present (owned by SP-02)` and exits 0. Verify that it is
   **wired** (Q3) so that SP-02 only has to remove `continue-on-error`.

**Report line required.** The §8 table must contain a `2% guardrail` row reading either
`no regressions vs. testdata/bench-baseline.txt` or an explicit list of regressed metrics with
their sign-off.

### 6.3 Cross-wave fixture rule (Rule W-2) at V1

> **W-2.** … The wave's verification checkpoint re-runs those same tests against the real
> implementation. Any fixture that the real implementation cannot reproduce is a verification
> failure, not a fixture bug.

At V1 there is no real implementation for any `behaviour` fixture, so W-2's re-run half is vacuous.
What V1 must verify is the **precondition**: every `format` fixture is frozen and consumable today
(IT-9), and every `behaviour` fixture is correctly marked `record-by-owner` (N3). Getting this
wrong here is what makes W-2 unenforceable in V2.

---

## 7. Failure protocol

Any failure — a failing test, a lint violation, a missing file, an unmet benchmark budget, a
signature that does not match `00-ARCHITECTURE.md` §5, a skip with a non-permitted message, a
fabricated stub, a CI job red — triggers this protocol. There are no "minor" failures at a
verification checkpoint; the entire point of the checkpoint is that wave 1 branches from a known
state.

### 7.1 Systematic diagnosis (do this before writing any fix)

1. **Reproduce deterministically.** Re-run the exact failing command with `-v` and, for tests,
   `-count=1` to defeat caching. Capture the complete output verbatim. If it does not reproduce,
   run it 10 times (`-count=10`) before calling it flaky; a genuinely flaky test at V1 is itself a
   defect to fix, not to retry around.
2. **Localize to a package and a seam.** Determine whether the failure is inside one package
   (a package test) or across a seam (an integration test, a lint graph check, a golden). Record
   which of the two it is; the fix location differs completely.
3. **Read the contract before the code.** Open the relevant section of `00-ARCHITECTURE.md` §5 (or
   `Qompack.md` for behaviour) and state, in one sentence, what the contract requires. Most V1
   failures are a mismatch between an implementation and a normative signature, not a bug.
4. **Classify the failure into exactly one of four buckets, and act accordingly:**
   - **(a) Implementation defect.** The code does not do what SP-01 says. → Fix the code on
     `verify/v1`.
   - **(b) Test/fixture defect.** The check itself is wrong (bad expectation, wrong path, platform
     assumption). → Fix the test or the golden, and state in the commit body *why* the old
     expectation was wrong. A golden may only be re-recorded with an explicit justification —
     never with a bare `-update`.
   - **(c) Checkpoint-document defect.** A command in §1–§6 of *this file* is wrong. → Fix this
     file, then re-run.
   - **(d) Architecture defect.** The normative interface in §5 is itself wrong or unsatisfiable.
     → **Do not work around it.** Stop, open `arch/<short-reason>` off `develop`, amend
     `00-ARCHITECTURE.md` §5, merge it, rebase `verify/v1`, then fix the code. This is the §0
     amendment rule and it is the one failure class that must not be handled on the verify branch.
5. **Write the fix as the smallest change that satisfies the contract.** Do not refactor
   opportunistically during a verification checkpoint. Do not implement functionality owned by a
   later subplan to make a check pass — a stub that returns `core.ErrNotImplemented` is the
   *correct* answer at V1, and "fixing" it by implementing SP-06's store is a worse failure than
   the one you started with.
6. **Prove the fix.** Re-run the specific failing command, then the whole group it belongs to, then
   `go run ./tools/devtool ci-local`.

### 7.2 Committing fixes

All fixes are committed **to `verify/v1`**, as **small Conventional Commits — as many as needed**.
Do not batch unrelated fixes into one commit. Each commit must compile and pass `devtool test` for
the packages it touches, and must carry a `Refs:` footer naming SP-01, the V1 inventory item ID
(e.g. `V1-D5`), and the `Qompack.md` / `00-ARCHITECTURE.md` sections involved.

Shape:

```
fix(paths): reject non-append O_WRONLY on pins/invariants.jsonl

The append-only guard checked O_TRUNC but not a plain O_WRONLY reopen, so an
in-place rewrite of the pins log was possible through paths.OpenFile. §7.4
requires the file to be additive-only; the guard is the mechanical enforcement
of §4.6, so a gap in it is a silent DPI violation rather than a cosmetic bug.

Refs: SP-01, V1-D5, §7.4, §4.6, §3.3
```

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

That rule is verbatim and absolute. It applies to every commit, merge commit, tag message, and PR
body produced by this checkpoint. CI's `verify` job greps the commit range for `Co-Authored-By`,
`Signed-off-by`, `Generated with`, and `🤖` and fails the build if any appear.

The new integration tests of §4 are committed the same way — one commit per coherent group is
fine, e.g.:

```
test(e2e): V1 cross-component integration tests for the wave-0 seams

Refs: SP-01, V1-IT-1..IT-5, §3.4, §7.3, §11.3
```

### 7.3 Re-run from the top

After **any** fix — even a one-line one, even a comment — **re-run this entire checkpoint from
§1 item A1**, in order. Not just the failing item. A fix in `paths` can break `config`'s
provenance line numbers; a fix in `config` can break the Appendix C golden; a fix in
`pluginmanifest` can break `plugin-validate`. Partial re-runs are how a broken foundation reaches
wave 1.

Practical loop:

```bash
go run ./tools/devtool ci-local          # fmt, lint, vet, build, test, cover, plugin-validate, docs
go test -race ./...                      # POSIX; on Windows: go test -count=2 ./...
# then re-walk §1 A → R, §2, §3, §4, §5, §6
```

### 7.4 The gate

**No wave-1 branch is cut until this checkpoint is fully green.** Concretely: `feat/sp02-*`,
`feat/sp03-*`, `feat/sp04-*`, `feat/sp05-*`, `feat/sp06-*` and `feat/sp07-*` do not exist until
every row of the §8 report table reads PASS (or a justified `N/A — SP-NN` for a component that does
not exist yet). "Green except for one thing" is not green. If a failure cannot be fixed — because it
requires an architecture amendment, or because it exposes a design gap — the amendment lands on
`develop` first (§7.1 bucket d) and the checkpoint re-runs; the wave still does not start.

### 7.5 Closing the checkpoint

When every item is green:

```bash
# on verify/v1
go run ./tools/devtool ci-local                # final green run
git switch develop
git merge --no-ff verify/v1 -m "chore(verify): V1 — foundation and contracts verified"
git tag v0.0.1
```

Then, and only then, cut the wave-1 branches from the post-verification `develop`
(§9: "The next wave's branches are cut from the post-verification `develop`").

Merge-commit message rules are the same: Conventional Commit subject, no attribution trailers.

---

## 8. Completion report template

Fill this in as you go. Every row needs a **result** and, where the item produces a number, a
**metric**. `N/A` is only permitted with the owning subplan named. Commit the filled-in report to
`verify/v1` as `plans/V1-report.md` before merging.

```markdown
# V1 completion report — foundation and contracts

Branch: verify/v1 (from develop @ <sha>)   Date: <date>   Machine: <os/arch/cpu>
Go: <go version>   Merged wave-0 branch: feat/sp01-foundation-toolchain-and-contracts @ <sha>

## SP-01 — Group A: repository, git hygiene, commit conventions

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| A1 | Repo initialized; root commit contents | PASS/FAIL | files in root commit: |
| A2 | Branch topology main/develop/feat/verify | PASS/FAIL | |
| A3 | 8 commits, Conventional Commits | PASS/FAIL | non-merge commits: |
| A4 | `Refs:` footer on feat/fix | PASS/FAIL | |
| A5 | No attribution trailers | PASS/FAIL | matches: 0 |
| A6 | `.gitignore` = §9 normative minimum | PASS/FAIL | |
| A7 | `.qompack/` genuinely ignored | PASS/FAIL | |
| A8 | `.gitattributes` protects goldens/corpora | PASS/FAIL | |
| A9 | `Qompack.md` unmodified | PASS/FAIL | diff bytes: 0 |
| A10 | commit-msg hook installs and enforces | PASS/FAIL | |
| A11 | Placeholder scan clean | PASS/FAIL | matches: 0 |

## SP-01 — Group B: toolchain

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| B1 | Module identity and Go pins | PASS/FAIL | |
| B2 | Closed runtime dependency list | PASS/FAIL | |
| B3 | Pinned tools in nested module | PASS/FAIL | |
| B4 | `fmt-check` clean | PASS/FAIL | |
| B5 | `devtool lint` (7 sub-checks) | PASS/FAIL | |
| B6 | `nomagic` armed and non-vacuous | PASS/FAIL | negative probe reported: yes/no |
| B7 | Ski-rental computed, no `12.5` literal | PASS/FAIL | grep matches: 0 |
| B8 | importgraph rejects + accepts real repo | PASS/FAIL | |
| B9 | Composition-root rule | PASS/FAIL | |
| B10 | testdeps | PASS/FAIL | |
| B11 | bindeps across 6 targets | PASS/FAIL | |
| B12 | sleepcheck | PASS/FAIL | |
| B13 | stubskips accounting | PASS/FAIL | skips counted: |
| B14 | All devtool tasks behave | PASS/FAIL | bench-hotpath/replay exit 0 with not-present msg: |
| B15 | build-all → 6 targets | PASS/FAIL | |
| B16 | `.golangci.yml` forbid rules | PASS/FAIL | |
| B17 | `ci-local` green | PASS/FAIL | wall time: |

## SP-01 — Group C: internal/core

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| C1 | Domain-separated hashing | PASS/FAIL | |
| C2 | Hash text form / parse / JSON | PASS/FAIL | |
| C3 | Decision IDs | PASS/FAIL | |
| C4 | Seven sentinels distinct | PASS/FAIL | |
| C5 | Types, clock, version | PASS/FAIL | coverage: |
| C6 | Domain registry + IPC non-member documented | PASS/FAIL | |

## SP-01 — Group D: internal/paths

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| D1 | Resolution, 4 branches | PASS/FAIL | |
| D2 | Norm / Key / property | PASS/FAIL | |
| D3 | Layout + self-ignore | PASS/FAIL | dirs created: 17 |
| D4 | WriteAtomic (+ protected refusal) | PASS/FAIL | |
| D5 | **TestAppendOnlyGuard** — 5/5 fail correctly | PASS/FAIL | |
| D6 | CreateNew read-only | PASS/FAIL | |
| D7 | AppendJSONL one line per record | PASS/FAIL | |
| D8 | ReplaceBloom keeps one backup | PASS/FAIL | |
| D9 | Long path > 260 | PASS/FAIL | |
| D10 | Manifest append/read | PASS/FAIL | |
| D11 | Coverage ≥ 90% | PASS/FAIL | coverage: |

## SP-01 — Group E: internal/config

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| E1 | **Appendix C verbatim** | PASS/FAIL | |
| E2 | runtime namespace + 3 additions | PASS/FAIL | |
| E3 | JSONC stripping | PASS/FAIL | |
| E4 | Five-layer precedence / deep merge / env | PASS/FAIL | |
| E5 | null = measure at runtime | PASS/FAIL | |
| E6 | Unknown/unparseable warn, never error | PASS/FAIL | |
| E7 | Invalid leaf falls back; cli reports loudly | PASS/FAIL | |
| E8 | Provenance file:line | PASS/FAIL | |
| E9 | Full validation rule table | PASS/FAIL | rules covered: |
| E10 | Submodular flag derived, not serialized | PASS/FAIL | |
| E11 | JSON Schema golden | PASS/FAIL | |
| E12 | Dotted Get | PASS/FAIL | |
| E13 | FuzzConfigLoad 60s | PASS/FAIL | crashers: 0 |
| E14 | config-reference.md not stale | PASS/FAIL | |
| E15 | Coverage ≥ 90% | PASS/FAIL | coverage: |

## SP-01 — Group F/G/H/I: logging, obs, tokens, hookio

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| F1 | Level filtering | PASS/FAIL | |
| F2 | Loud → 3 destinations | PASS/FAIL | |
| F3 | Loud counter wiring, no import cycle | PASS/FAIL | |
| F4 | Rotation | PASS/FAIL | |
| F5 | Nop still records Loud | PASS/FAIL | |
| F6 | Coverage ≥ 75% | PASS/FAIL | coverage: |
| G1 | Histogram monotone/conservative/max-exact | PASS/FAIL | P99 error bound: |
| G2 | Six budgets, config-driven | PASS/FAIL | |
| G3 | Breach windows | PASS/FAIL | |
| G4 | Counters/gauges/snapshot/persist | PASS/FAIL | |
| G5 | Coverage ≥ 75% | PASS/FAIL | coverage: |
| H1 | Classify table (14 cases) | PASS/FAIL | |
| H2 | Estimator arithmetic | PASS/FAIL | |
| H3 | Uncalibrated Factor == 1.0 | PASS/FAIL | |
| H4 | Calibration clamps + persists | PASS/FAIL | |
| H5 | EstimateRoot; no store import | PASS/FAIL | |
| H6 | Monotone property | PASS/FAIL | |
| H7 | tokenstest shape | PASS/FAIL | |
| H8 | Coverage ≥ 75% | PASS/FAIL | coverage: |
| I1 | Seven hook payloads parse | PASS/FAIL | |
| I2 | Unknown fields → Extra | PASS/FAIL | |
| I3 | Missing/null never panic | PASS/FAIL | |
| I4 | Limit → ErrBudget | PASS/FAIL | |
| I5 | Output codec shapes | PASS/FAIL | |
| I6 | FuzzReadEvent 60s | PASS/FAIL | crashers: 0 |
| I7 | Coverage ≥ 75% | PASS/FAIL | coverage: |

## SP-01 — Group J/K: cli, binary, hooks, plugin bundle

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| J1 | Hooks exit 0 — 30/30 fault injections | PASS/FAIL | subtests: 30 |
| J2 | Non-hook exit codes 1 / 2 | PASS/FAIL | |
| J3 | Panic recovered loudly, exit 0 | PASS/FAIL | |
| J4 | Hook log, no payload content | PASS/FAIL | |
| J5 | config print --provenance | PASS/FAIL | |
| J6 | config schema | PASS/FAIL | |
| J7 | --set plumbing | PASS/FAIL | |
| J8 | Dispatch table complete (12 stub subcommands) | PASS/FAIL | |
| J9 | version / help | PASS/FAIL | |
| J10 | Six hooks via real binary | PASS/FAIL | |
| J11 | main.go < 150 LOC | PASS/FAIL | LOC: |
| J12 | Coverage ≥ 75% | PASS/FAIL | coverage: |
| K1 | Bundle golden bytes (10 files) | PASS/FAIL | |
| K2 | Six hooks / seven entries / timeouts | PASS/FAIL | |
| K3 | Seven commands shell out | PASS/FAIL | |
| K4 | .mcp.json shape | PASS/FAIL | |
| K5 | Drift detection | PASS/FAIL | |
| K6 | plugin-validate + git diff clean | PASS/FAIL | |
| K7 | plugin.json metadata | PASS/FAIL | |
| K8 | Coverage ≥ 75% | PASS/FAIL | coverage: |

## SP-01 — Group L/M/N: stubs, conformance suites, fixtures

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| L1 | 23 stub packages build + vet | PASS/FAIL | |
| L2 | Stubs inert, not faked | PASS/FAIL | packages probed: 23 |
| L3–L13 | Pure functions implemented (RootHash, DefaultParams/Validate, YoungDaly, SkiRental, PSelectionAvailable, Descriptor.Key, StripInjections, Tombstone, FormatWarning, StandingInstruction, Mode.String) | PASS/FAIL | |
| L14 | NewSelector guards (ErrBudget first, then ErrNotImplemented) | PASS/FAIL | |
| L15 | zstd round-trip + bomb bounded | PASS/FAIL | |
| L16 | ipc.Resolve endpoint derivation | PASS/FAIL | |
| L17 | Nine contract assertions, all not-yet-implemented | PASS/FAIL | |
| L18 | SP-01-declared missing types | PASS/FAIL | |
| L19 | Stub coverage exemptions printed | PASS/FAIL | exemptions listed: |
| M1 | 22 suites present, shape blocks pass | PASS/FAIL | suites: 22 |
| M2 | Only permitted skip messages tree-wide | PASS/FAIL | non-permitted skips: 0 |
| M3 | Per-suite shape tests | PASS/FAIL | |
| M4 | OWNERS.tsv complete and authoritative | PASS/FAIL | rows: |
| M5 | Authored behaviour assertions present | PASS/FAIL | |
| N1 | Per-package MANIFEST.json | PASS/FAIL | |
| N2 | format fixtures frozen | PASS/FAIL | count: |
| N3 | behaviour fixtures record-by-owner | PASS/FAIL | count: |
| N4 | Recording refuses against a stub | PASS/FAIL | |
| N5 | Checkpoint golden has no code snippets | PASS/FAIL | fences: 0 |
| N6 | Rule W-2 skip message exact | PASS/FAIL | |

## SP-01 — Group O/P/Q/R: testutil, e2e, guards, CI, whole tree

| ID | Item | Result | Metric / notes |
|---|---|---|---|
| O1–O5 | testutil fixture, AssertAppendOnly teeth, FakeClock, golden, Windows-hostile files | PASS/FAIL | |
| O6 | E2E all six hooks exit 0 | PASS/FAIL | |
| O7 | E2E config print from real binary | PASS/FAIL | |
| O8 | os/exec allowlist | PASS/FAIL | |
| P1 | Guard: Phase 0 before store | PASS/FAIL | |
| P2 | Guard: store+negknow before checkpoint | PASS/FAIL | |
| P3 | Guard: submodular inert | PASS/FAIL | |
| P4 | Guard: selector refuses without p-selection | PASS/FAIL | |
| P5 | Guard: O1 defaults on | PASS/FAIL | |
| P6 | Guard: fresh build reports ModeFull | PASS/FAIL | |
| P7 | Guard: write set confined | PASS/FAIL | |
| P8 | Guard: no network imports | PASS/FAIL | |
| Q1–Q7 | CI workflows, gates, templates | PASS/FAIL | |
| Q8 | CI green on verify/v1 | PASS/FAIL | run URL / job results: |
| R1 | `go test ./...` | PASS/FAIL | |
| R2 | `go test -race ./...` | PASS/FAIL | |
| R3 | `go test -count=2 ./...` (Windows) | PASS/FAIL | |
| R4 | build + vet on 3 platforms | PASS/FAIL | |
| R5 | Coverage floors (config/paths ≥90; others ≥75) | PASS/FAIL | per-package: |

## Exit-criteria re-verification (§2)

| Criterion | Result | Evidence |
|---|---|---|
| Phase 0 measurability shipped (eval.minSessions=20, replay-gate wired) | PASS/FAIL/N-A | |
| Phase 1 measurability shipped (Stats.DedupRatio, B-A budget, bench contract) | PASS/FAIL/N-A | |
| §11.3 guardrail 1 — 15 ms / 2 s expressed as config keys | PASS/FAIL | |
| §11.3 guardrail 2 — sublinear store growth | N/A — SP-06 | |
| §11.3 guardrail 3 — 2% rule wired | PASS/FAIL | |
| §11.3 guardrail 4 — replay suite on every phase gate | PASS/FAIL | |
| DoD 1 … DoD 18 (SP-01) | 18 rows PASS/FAIL | one line each |

## Architecture invariants (§3)

| Invariant | Result | Evidence |
|---|---|---|
| 1 never compress a compression | PASS/FAIL | |
| 2 append-only | PASS/FAIL | |
| 3 bloom is a cache | PASS/FAIL | |
| 4 nothing scattered before p | PASS/FAIL | |
| 5 no code snippets in checkpoints | PASS/FAIL | |
| 6 hooks exit 0 | PASS/FAIL | |
| 7 no network / telemetry / outside writes | PASS/FAIL | |
| 8 volatile constants are config keys | PASS/FAIL | |
| 9 budgets measured | PASS/FAIL | |
| 10 degradation is loud | PASS/FAIL | |

## New integration tests (§4) — authored this checkpoint

| ID | Test | Result | Notes |
|---|---|---|---|
| IT-1 | TestV1_HookLifecycleThroughRealBinary | PASS/FAIL | hook-log lines: 8 |
| IT-2 | TestV1_ConfigPrecedenceReachesHookBehaviour | PASS/FAIL | |
| IT-3 | TestV1_AppendOnlyInvariantSurvivesRealHookRun | PASS/FAIL | |
| IT-4 | TestV1_WriteSetConfinedAcrossFullHookSequence | PASS/FAIL | paths outside .qompack: 0 |
| IT-5 | TestV1_PluginManifestCommandsExecuteAgainstRealBinary | PASS/FAIL | max wall vs timeout: |
| IT-6 | TestV1_StubGraphIsInertAndOwned | PASS/FAIL | packages: 23 |
| IT-7 | TestV1_ContractMonitorDegradesAndRestoresLoudly | PASS/FAIL | LOUD lines == transitions: |
| IT-8 | TestV1_ObsBudgetsAreConfigDrivenEndToEnd | PASS/FAIL | |
| IT-9 | TestV1_FrozenContractFixturesRoundTripIntoDeclaredTypes | PASS/FAIL | fixtures: |
| IT-10 | TestV1_ToolchainGatesRejectRealViolations | PASS/FAIL | |

## Performance budgets (§5)

| ID | Budget | Threshold | Observed | Result |
|---|---|---|---|---|
| P-1 | BenchmarkHistogram_Observe | < 100 ns/op | | PASS/FAIL |
| P-2 | BenchmarkConfigLoad_ColdNoFiles | < 2 ms/op | | PASS/FAIL |
| P-3 | BenchmarkHookNoop_InProcess (B-A proxy) | < 3 ms/op | | PASS/FAIL |
| P-4 | BenchmarkPathsWriteAtomic_4KB | < 2 ms/op | | PASS/FAIL |
| P-5 | B-A hook_controlled p99 | < 15 ms | N/A — SP-05 (budget expressed) | N/A |
| P-6 | B-B l0_ingest p99 | < 2 ms | N/A — SP-05 | N/A |
| P-7 | B-C l0_process p99 (soft) | < 50 ms | N/A — SP-05/06 | N/A |
| P-8 | B-D hook_wall (reported only) | — | p50 / max spawn: | RECORDED |
| P-9 | B-E checkpoint_finalize p99 | < 2 s | N/A — SP-10 (budget expressed) | N/A |
| P-10 | B-F mcp_tool_call p95 | < 250 ms | N/A — SP-13 | N/A |
| P-11 | Store dedup ratio | ≥ 4:1 | N/A — SP-06 | N/A |
| P-12 | Store growth sublinear | — | N/A — SP-06 | N/A |
| P-13 | benchstat vs bench-baseline.txt | ≤ 10% warn / 25% fail | | PASS/FAIL |
| P-14 | Binary size | 8–25 MB | | PASS/FAIL |

## Regression (§6)

| Item | Result | Notes |
|---|---|---|
| Prior checkpoint inventories re-run | N/A — V1 is the first checkpoint | |
| 2% guardrail vs testdata/bench-baseline.txt | PASS/FAIL | regressions + sign-offs: |
| replay-gate wired for SP-02 | PASS/FAIL | |
| Rule W-2 preconditions (format frozen / behaviour deferred) | PASS/FAIL | |

## Fixes committed on verify/v1

| Commit | Subject | Inventory item | Bucket (a/b/c/d) |
|---|---|---|---|
| | | | |

## Verdict

- [ ] Every row above is PASS or a justified N/A with an owning subplan named
- [ ] Full checkpoint re-run from the top after the last fix: green
- [ ] No `Co-Authored-By` or attribution trailer in any commit on verify/v1
- [ ] verify/v1 merged into develop with --no-ff; tag v0.0.1 applied
- [ ] **Wave 1 branches (SP-02 … SP-07) may now be cut from develop**
```
