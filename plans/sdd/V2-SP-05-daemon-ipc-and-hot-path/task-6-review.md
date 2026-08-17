# Task 6 review — commit 6: thin hook clients, daemon/self-test subcommands, fault injection, e2e suite

Reviewed commit: `8c84cf9` (single commit, base `ef50515f`)
Branch: `feat/sp05-daemon-ipc-and-hot-path`
Worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`
Reviewer: task reviewer (read-only; no edits, no commits)

---

## Verdicts

**1. SPEC COMPLIANCE — Needs fixes.** Every row of the hook wiring table, the `daemon.go`/`selftest.go`
check lists, all eleven fault sites and all nine e2e table rows are present and implemented, most of
them faithfully. Three items are silently weakened rather than adapted-with-ruling (`daemon-down`
does not disable the address; spool-submode's "accept counter unchanged" is asserted against a
counter that cannot move either way; `metrics` is nil where the spec text names SP-01's registry
constructor), and one V1-checkpoint regression test named by five downstream VERIFY plans was
deleted outright rather than adapted.

**2. CODE QUALITY — Needs fixes.** The hook body ordering, deadline constants, `EnsureRunning`
failure path, `ErrAddrTooLong` path, fault-site layering and e2e process hygiene are all correct and
unusually well documented. Two correctness defects stand out: `qompack self-test` permanently
pollutes the project's immutable checkpoint tier, and the hot path performs two `state.bin` reads
where the spec mandates one while every hot-path drop is made completely invisible (nil registry +
`logging.Nop()`).

**Verdict: Needs fixes (2 Critical, 9 Important).** Minor: 12.

---

## Adjudication of the implementer's five judgment calls

### (a) `dispatch_test.go` fault case pinned to a nonexistent `QOMPACK_PROJECT_ROOT`

**Legitimate drive-by, with a stale test name.** The hazard is real and verified: with `noEnv` and a
payload carrying no `cwd`, `resolveProjectRoot` (hookclient.go:206) falls back to `currentDir()`,
which under `go test ./internal/cli/...` is the checkout itself; `paths.Resolve` then walks up to the
checkout's own `.git`, and the spool append lands `.qompack/spool/client-*.ndjson` inside the real
repository. I confirmed the working tree is clean after full `./internal/cli`, `./test/guards` and
`./test/e2e` runs (`git status --porcelain` empty), so the fix holds.

The one thing hidden by it: the sub-test is still named `"unresolvable project root"`, but with the
env override set, `paths.Resolve` succeeds outright (§3.3 step 1) and the case now exercises a
*different* property — `doHook`'s `isDir(root)` refuse-to-conjure-a-store guard (hookclient.go:119).
"Unresolvable root" is no longer reachable at all through the new body. That is a behaviour change in
the product (correct and intended), but the test's premise no longer matches its label. Rename to
something like `payload_root_does_not_exist` (see Minor M-13). Not scope creep; the sweep for other
instances of the same pattern is reported and I found none either.

### (b) `internal/daemon` local `fakeClock` to break `daemon → testutil → cli → daemon`

**Legitimate; verified.** The new file is `internal/daemon/fakeclock_test.go` — a `_test.go` file, so
it cannot leak into the build; `go build ./...` and `go build -tags noinject ./...` are clean and
`go list -deps ./cmd/qompack` does not contain it. The cycle diagnosis is correct:
`internal/testutil/project.go:18` imports `internal/cli` (non-test), and task 6 makes `internal/cli`
import `internal/daemon`, so a `daemon` `_test.go` importing `testutil` closes the loop. The five
edits (`drain/idle/ingest/lock/spawn_test.go`) are a purely mechanical
`testutil.NewFakeClock(testutil.Epoch)` → `newFakeClock(epoch)` substitution — I diffed all five and
no assertion, bound, or fixture changed. The precedent cited (`internal/cli/dispatch_test.go`'s own
local `fakeClock`) is real. Import discipline holds: `go run ./tools/devtool lint
--only=importgraph,testdeps,bindeps,sleepcheck,stubskips` passes, nothing imports `cli` except
`testutil`, which is itself a declared composition root and a pre-existing sanctioned exception
(`tools/devtool/importrules.go:11-19`, `internal/testutil/doc.go:7`).

### (c) `golang.org/x/sys/windows` added to devtool's bindeps allow-list

**Legitimate, and correctly scoped.** Verified three ways: `go.mod` carries
`golang.org/x/sys v0.33.0 // indirect` (never promoted to a direct require);
`GOOS=windows go mod why -m golang.org/x/sys` resolves
`internal/ipc → github.com/Microsoft/go-winio → golang.org/x/sys/windows`; and no qompack source file
imports it (`internal/daemon/spawn_windows.go:8` explicitly *documents avoiding* it for §2.5 reasons).
The allow-list entry is restricted to the `windows` subpackage and the pinned test adds
`golang.org/x/sys/unix → false` as a negative case, so the rule stays scoped. Only quibble: the
`strings.HasPrefix(importPath, "golang.org/x/sys/windows/")` half also admits `.../registry`,
`.../svc`, etc., which go-winio does not need (Minor M-14).

### (d) V1-checkpoint e2e/testutil tests updated — CRITICAL CHECK

Complete list of pre-existing tests modified or deleted, each judged:

| File / test | Change | Verdict |
|---|---|---|
| `internal/cli/dispatch_test.go` — `TestDispatch_HookAlwaysExitsZero` "unresolvable project root" case | env pinned to a nonexistent path | **OK** (see (a)); exit-0 intent preserved, label now stale |
| `internal/testutil/project_test.go` — `TestProject_RunHookInProcess` | dropped the six-hook-log-line count; **inverted** SessionStart/PreCompact `HookSpecificOutput` from `NotNil` to `Nil` | **OK with reservation.** The log half is legitimately dead (brief: "old no-op observation logging … is REPLACED"). The `HookSpecificOutput` half is honest — with no daemon in process, the response *is* `{}` — but it converts a positive assertion into a pin on degraded behaviour. Acceptable because `TestE2EHookRoundTrip` and IT-1 pin the positive case against a real daemon. |
| `internal/testutil/project_test.go` — `TestProject_RunHookAcceptsSubcommandSpelling` | dropped `require.Len(hookLogLines, 2)` | **OK** — dead behaviour |
| `internal/testutil/project_test.go` — `hookLogLines` helper | deleted | **OK** |
| `test/e2e/hooks_test.go` — **`TestE2E_AllSixHooksExitZero`** | **DELETED ENTIRELY** | **NOT OK — see Critical C-1** |
| `test/e2e/hooks_test.go` — `TestE2E_RunHookRealBinaryMode` | dropped log-line count; added daemon shutdown cleanup | **OK** |
| `test/e2e/hooks_test.go` — `hookLogLines` helper | deleted | **OK** |
| `test/e2e/v1_integration_test.go` — IT-1 `TestV1_HookLifecycleThroughRealBinary` | dropped exact-stdout equality, the 8-line log-record assertions, **and the `v1Secrets` payload-leak assertion**; added an `hookio.Output` parse + SessionStart `hookSpecificOutput` check | **PARTLY NOT OK — see Important I-4.** The log-record half is legitimately dead. The secret-leak half is the test's own self-described "load-bearing half" and has no replacement. |
| `test/e2e/v1_integration_test.go` — IT-2 `TestV1_ConfigPrecedenceReachesHookBehaviour` | dropped truncation/warn assertions and the 2 MiB bootstrap-clamp sub-test; replaced with a `NoFileExists(state.bin)` sub-test | **PARTLY NOT OK — see Important I-5.** The precedence→provenance half survives; the replacement sub-test is racy and does not prove its stated claim, and the bootstrap-clamp property is now unpinned. |
| `test/e2e/v1_integration_test.go` — `TestV1_PluginManifestCommandsExecuteAgainstRealBinary`, `v1LimitProject` | added daemon-shutdown cleanups | **OK** — additive |
| `test/guards/v1_integration_test.go` — `TestV1_WriteSetConfinedAcrossFullHookSequence` | added a daemon-shutdown cleanup helper; **no assertion changed** | **OK as a diff, but see Important I-9** (the test now races a live daemon) |
| `internal/daemon/{drain,idle,ingest,lock,spawn}_test.go` | mechanical fakeClock swap | **OK** |

**Frozen fixtures / guard pins:** no frozen fixture was touched. `internal/testutil`'s frozen-fixture
count is unchanged (Task 1 and Task 4 bumped it; task 6 does not). `test/guards/network_test.go` and
`test/guards/writeset_test.go` were **not modified** — neither weakened nor exempted; both pass
(`go test -count=1 ./test/guards/...` → ok, 17.0s). `tools/devtool/importrules.go` is unchanged in
this commit. The only guard-adjacent edit is the additive cleanup helper in
`test/guards/v1_integration_test.go`.

**Net judgement on (d): mostly legitimate, one deletion is unacceptable (C-1) and two updates lose
load-bearing assertions without replacement (I-4, I-5).**

### (e) `selfPath()` gating on `testing.Testing()`

**Sound reasoning, unsound placement.** The hazard is real: `os.Executable()` inside a `go test`
binary returns the test binary, `SpawnDetached` would exec it as `<testbin> daemon --project <root>`,
Go's `flag` package stops at the first non-flag token without erroring, and the testing runtime would
re-run the whole package detached — recursively. Gating is a correct *fix* for a correct *diagnosis*.

But the mechanism is product code branching on "am I under test", and it has three real costs:

1. It links stdlib `testing` into the shipped binary. Verified:
   `go list -deps ./cmd/qompack | grep -E '^(testing|runtime/trace)'` → `testing`, `runtime/trace`.
   §2.5 is precisely about what may reach the shipped binary; `devtool bindeps` only polices
   third-party modules, so nothing caught this.
2. It permanently disables lazy spawn **and** `daemon.EnsureRunning` in every in-process test —
   including `test/guards`' in-process write-set guard and `internal/testutil`'s `RunHook`. As a
   result `internal/cli/sessionstart.go`'s `ensureDaemonRunning` has **no unit test at all**; only
   the early-return branches are reachable in `internal/cli`.
3. It makes the hook body untestable at the seam a reviewer most wants to poke.

Do the e2e tests that need real spawn still exercise it? **Yes.** `TestE2ELazySpawn`,
`TestE2EHookRoundTrip`, `TestE2EIdleExit`, `TestE2ESpoolSubmodeEndToEnd`,
`TestE2ESelfTest*` and IT-1/IT-4 all drive the real compiled binary via `e2e.Run` / `E2EBinaryEnv`,
where `testing.Testing()` is false — I confirmed a real daemon comes up (`run/daemon.lock`,
`run/daemon.hb`, `run/state.bin` all appear) when driving the built binary by hand. So coverage of
the spawn path is not lost, only in-process coverage is.

The cleaner form costs nothing: make `selfPath` a package-level `var selfPath = func() string { … }`
(or plumb `Self` through `cli.Env`), and have the test packages that must not spawn set it to `""`.
That keeps `testing` out of the shipped binary and restores an in-process seam for
`ensureDaemonRunning`. Filed as Important I-7 — the current behaviour is safe, so this is a
maintainability/dependency-hygiene fix, not a correctness one.

---

## 1. Spec compliance walk

### 1.1 Hook wiring table (task-6-spec.md lines 63-72)

| Subcommand | Op | Reply | Deadline | Spool | Status |
|---|---|---|---|---|---|
| `observe tool` | `ipc.OpObserveTool` | no | `deadline: 0` → `st.AckDeadlineMs` | `<root>/.qompack/spool` via `paths.Of(root).Spool` | **met** (hooks.go:12-15, hookclient.go:160-163) |
| `observe prompt` | `ipc.OpObservePrompt` | **yes** | `promptReplyDeadline = 250ms` | same | **met** (hooks.go:16-20; const at hookclient.go:30, tied by comment to `internal/daemon/handlers.go:22`, which I verified is 250 ms) |
| `observe stop` (`--subagent`) | `ipc.OpObserveStop` | no | `AckDeadline` | same | **met**; `--subagent` → `Raw={"subagent":true}` (hookclient.go:255-259), pinned by `TestHooks_ObserveStopSubagentFlagReachesRaw` |
| `session-start` | `ipc.OpSessionStart` | **yes** | `sessionStartReplyDeadline = 10s` | same | **met**, plus `preSend → daemon.EnsureRunning` (sessionstart.go:20-43) |
| `checkpoint` | `ipc.OpCheckpoint` | **yes** | `checkpointReplyDeadline = 15s` | same | **met**; `Raw={"trigger":…}` sourced from `ev.Trigger`, **adapted with rationale** (the manifest passes no flags; report §2, pinned by `TestHooks_CheckpointTriggerReachesRaw`) — correct call |
| `flush` | `ipc.OpFlush` | **yes** | `flushReplyDeadline = 15s` | same | **met** |

All four constants are named with §-comments, per the brief. No magic numbers; `devtool lint`'s
`nomagic` sub-check passes.

### 1.2 Hook skeleton ordering (task-6-spec.md lines 5-48)

| Step | Spec | Shipped | Status |
|---|---|---|---|
| 1 | TS from clock as FIRST statement | hookclient.go:82 `ts := clk.Now().UnixMilli()` — first statement after the nil-clock guard | **met** |
| 2 | `defer recoverToZero()` | delegated to `runGuarded` (recover.go:24-57), which writes `hookio.Empty()` if `cw.n == 0`, per the binding ruling "do not duplicate" | **met (adapted-with-ruling)** |
| 3 | resolve root cheaply pre-stdin | hookclient.go:84 via `paths.Resolve(env.Getenv, cwd)` | **met** |
| 4 | `ipc.ReadState(root, config.Defaults())`, never `config.Load` | hookclient.go:86 | **met at the cli layer** — but see I-1: `ipc.NewClientWithOptions` re-reads it |
| 5 | `ModeOff → Empty()`, done | hookclient.go:87-89, pinned by `TestHooks_ModeOffShortCircuits` (asserts stdin is never read) | **met** |
| 6 | `ReadEvent(stdin, int64(st.MaxPayloadBytes)*4)` | hookclient.go:92 | **met** |
| 7 | re-resolve root from payload, re-read state if moved, re-check ModeOff | hookclient.go:102-109, pinned by `TestHooks_RootReResolvesFromPayload` | **met** |
| 8 | build `ipc.Request` with `Raw` from flags only | hookclient.go:123-126 | **met** |
| 9 | spool at `<root>/.qompack/spool` | hookclient.go:128-136, guarded `Close` type-assertion exactly as the spec's note describes | **met** |
| 10 | client via `NewClientWithOptions` with State/Self/Spawn/Clock | hookclient.go:154-156 | **met**, `State` field ineffective (I-1) |
| 11 | `Send`, write `resp.Output` or `Empty()`, return 0 | hookclient.go:165-170 | **met** |
| — | `ErrAddrTooLong → spool + empty + 0` | hookclient.go:142-147 | **met** (untestable on Windows; POSIX-only path, uncovered) |
| — | `logQuiet` only if log dir exists | hookclient.go:233-244 | **met, untested** (I-8) |
| — | env access via `cli.Env`, never `os.*` in hook bodies | `env.Getenv`, `env.Stdin`, `env.Clock` used throughout; `os.*` confined to `fault.go`, `selfPath`, `osdir.go`, `homeDir` fallback | **met** |
| — | extra: `isDir(root)` refuse-to-conjure guard | hookclient.go:119-121 | **addition, well justified**, pinned by `TestHooks_RefuseToCreateStoreUnderMissingRoot` |

### 1.3 `cli/daemon.go` (task-6-spec.md line 74)

Resolve root (flag > env > cwd) ✓ (daemon.go:38-45) · `EnsureLayout` ✓ · logger against `l.Logs` ✓ ·
`NewOptions`/`New` ✓ · `signal.Notify(os.Interrupt, SIGTERM)` cancelling the run ctx ✓ (daemon.go:93-102)
· `--foreground` ✓ · exit 0 on clean stop ✓ · exit 0 on `ErrLockHeld` ✓ · exit 0 + `Loud` on any
other error ✓ (daemon.go:108-119) · malformed flag → `return nil` ✓ (daemon.go:35) · `LoadConfigAndReport`
with `cfgErr → Defaults + Loud` ✓. **All met**, unit-tested by four `TestDaemon_*` cases. Registered
in `All()` and removed from `notImplemented` ✓ (commands.go).

### 1.4 `cli/selftest.go` check list (task-6-spec.md line 76)

| Check | Spec | Shipped | Status |
|---|---|---|---|
| config load and validation | yes | `selfTestConfigLoad`, SevCritical on failure | **met** |
| `.qompack/` writability | yes | `selfTestWritable`, probe file in `Tmp`, removed | **met** |
| `paths.AppendOnly`/`CreateNew` guard behaviour | yes | `selfTestAppendOnlyGuard` | **met but harmful — Critical C-2** |
| `ipc.Resolve` | yes | `selfTestResolve`, SevCritical on failure | **met** |
| daemon reachable **or spawnable** | yes | `selfTestDaemonReachable`: `ipc.Probe` first, then `daemon.EnsureRunning`; SevWarn on either failure | **met** — it genuinely verifies (it can and does report `not reachable…`), it does not always pass. Severity choice (Warn, not Critical) is the implementer's and is defensible: a diagnostic must not exit 1 because no daemon happens to be up. |
| `admin.ping` round trip | yes | `selfTestAdminPing`, pinned to `resp.OK` per Ruling #22 | **met** |
| `Router.Ops()` coverage vs `ipc.KnownOps()` | yes | `selfTestOpsCoverage` — **informational count only**, SevInfo, no live query | **adapted with rationale** (report §5): there is no `admin.ops` endpoint and `buildRoutes` covers `KnownOps()` unconditionally. Correct call given the shipped surface; the brief explicitly said "use what the daemon actually exposes". |
| `contract.StandardAssertions` against a synthetic Env from persisted History | yes | `selfTestContractAssertions`, throwaway Monitor (empty statePath ⇒ no persistence), `DeclareProducers(&Services{})`, fixed synthetic session id | **met**, well-reasoned |
| fixed-width table / `--json {checks,mode,exit}` | yes | `writeSelfTestTable`, `selfTestReport` | **met** |
| exit 0 iff no SevCritical, 1 otherwise | yes | selftest.go:76-102, via `errAlreadyReported` so Dispatch does not double-print | **met**, and `TestSelfTest_RegisteredInAll` pins `Hook == false` |

### 1.5 The eleven fault sites (task-6-spec.md lines 80-92)

| Site | Named location | Injection shipped | Expected behaviour honoured? |
|---|---|---|---|
| `stdin-eof` | `faultStdin` at hookclient.go:91 | empty reader (fault.go:105-107) | **yes** — logQuiet, empty JSON, 0 |
| `stdin-garbage` | same | `{{{not json` (fault.go:108-110) | **yes** |
| `oversize` | `faultInflateToolResponse` at hookclient.go:112 | 4 MiB `ToolResponse` (fault.go:127-143) | **yes** — `Client.Send` externalizes/spools |
| `daemon-down` | hookclient.go:149-152 + sessionstart.go:35-37 | Spawn → `noopSpawn`, `EnsureRunning` skipped | **partial — Important I-3**: the resolved address is *not* overridden, so the site is inert against a live daemon |
| `spool-readonly` | `faultLockSpoolDirIfNeeded` at hookclient.go:129 | POSIX `chmod 0500`, Windows deny-ACE via `icacls` | **partial** — the drop happens, but `l0.dropped` and the `Loud` line do not (I-2) |
| `spool-full` | `wrapFaultSpool` at hookclient.go:131 | `ErrSpoolFull` after `arg` (default 1) appends | **literal but inert by default** — see Minor M-5; also I-2 |
| `disk-full` | same | ENOSPC-equivalent from the first `Append` | **partial** — spec also names `logQuiet` and `ipc.WriteState`; only `Append` is wrapped (Minor M-4); also I-2 |
| `state-corrupt` | `faultCorruptStateIfNeeded` at hookclient.go:85 and :104 | 32 random bytes over `run/state.bin` before `ReadState` | **yes** — falls back to `config.Defaults()` silently, exit 0; unit-tested |
| `config-corrupt` | `faultCorruptConfigIfNeeded` at hookclient.go:111 | truncated JSON, exactly the spec's byte string | **yes for hooks (inert by design)**; the daemon/self-test half is untested (Minor M-12) |
| `panic:hook` | `maybePanicHook` at hookclient.go:98, immediately after `ReadEvent` | `panic(…)` | **yes** — `runGuarded` recovers and writes empty |
| `panic:client` | `wrapFaultClient` at hookclient.go:157 | `Send` panics | **yes** |

Parsing quality is good: `loadFaultMap` (fault.go:77-99) checks the whole token against the known-site
set *before* splitting on `:`, so `panic:hook` / `panic:client` are not misparsed — and that exact
subtlety has its own test. Nil-map fast path present. `noinject` variant (fault_noinject.go) compiles
every seam to a no-op and deliberately never spells the variable name, so the grep contract survives
in that file. `faultsites.go` is untagged so both build variants share one spelling of each site —
a genuinely good decision for the byte-identity test.

### 1.6 e2e table (task-6-spec.md lines 106-114)

| Test | Status |
|---|---|
| `TestE2EHookRoundTrip` | **met** — session-start, 50 × observe tool, WAL ≥ 50 lines, exactly one live session via `admin.status` |
| `TestE2ELazySpawn` | **met** — no daemon asserted first, spool file after call 1, daemon up, call 2, spool drained |
| `TestE2EIdleExit` | **met** — `QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=1` (env rather than `--set`; **better** than the brief's suggestion, since `--set` does not propagate through `SpawnDetached`), asserts both `run/daemon.lock` and `run/state.bin` are gone |
| `TestE2ESelfTestExitsZeroOnHealthy` | **met** — exit 0, JSON parses, `mode == "full"` |
| `TestE2ESelfTestExitsNonZeroOnCritical` | **met**, and degraded at its real source (three marker-less SessionStarts) rather than by hand-writing `state/contract.json` — a strictly better construction than the spec's, with the "why three, not two" reasoning documented and correct |
| `TestE2ESpoolSubmodeEndToEnd` | **partially met — Important I-6**: forces `Hot=HotSpool`, asserts a client spool file exists and exit 0 ✓; the "daemon accept counter unchanged" half is asserted against `Counters["ipc_decode_error"]`, which is unchanged on a *successful* connect too |
| `TestHooksExitZeroUnderFaults` (66 combos) | **met** — 6 × 11, fresh isolated project per combo, exit 0 + `hookio.Output` parse. Re-ran `./test/e2e/...` myself: ok, 69.5 s |
| `TestFaultSitesInertWhenUnset` | **met** — real `-tags noinject` build, byte-identical stdout. The "faultActive false for all eleven" half lives in `internal/cli/fault_test.go` rather than in this test (Minor M-9) |
| `TestSelfTestIsTheOnlyNonZeroExit` | **met, scope-narrowed with rationale** — covers the six hooks + `version` + `config print` + `config schema` + `self-test`; excludes `notImplemented` placeholders (non-zero by design, other subplans) and `qompack daemon` (does not exit under normal invocation; covered by four `TestDaemon_*` unit tests instead). Both exclusions are documented in the test's own doc comment. **Caveat**: because I-3 leaves the daemon live, the "under daemon-down" framing is not actually in force in this test. |

### 1.7 Global constraints

| Constraint | Status |
|---|---|
| Every hook exits 0 on every path | **met** — `Dispatch` (dispatch.go:104-106) is unconditional; `runGuarded` covers panics and no-output |
| stdout always valid JSON | **met** — `hookio.Empty()` fallback in both the body and `runGuarded`'s two guards |
| TS from `env.Clock` as first statement | **met** |
| root resolution cheap pre-stdin | **met** |
| `ipc.ReadState`, not `config.Load`, on the hot path | **met at the cli layer**; the client silently re-reads (I-1). No `config.Load` anywhere on the hook path — confirmed |
| ModeOff → empty fast path | **met** |
| self-test exit 1 iff any SevCritical | **met** |
| all other subcommands exit 0 even on error; daemon wrapper swallows into logged nil | **met** |
| `QOMPACK_FAULT` literal in exactly one non-test file | **NOT met as written** — two non-test files: `internal/cli/fault.go:42` and `internal/daemon/spawn.go:54`. The second is pre-existing (Task 3) and structurally required by `buildSpawnEnv`'s strip. The `security` CI job (`.github/workflows/ci.yml:127-146`) has **no such grep**, so nothing enforces or fails. Needs an exemption or a plan amendment — Minor M-8 |
| `noinject` compiles `faultActive` to always-false | **met**, verified `go build -tags noinject ./...` clean |
| `SpawnDetached` strips it from the child env; cli does not duplicate | **met, verified** — `buildSpawnEnv` (spawn.go:151-163) strips case-insensitively; `internal/cli` never re-implements it, only reads |
| no `time.Sleep` outside test/bench | **met** — `devtool lint --only=sleepcheck` passes; every new poll uses `time.Ticker`/`Timer` |
| no writes outside `.qompack/` at runtime | **met for hooks** (`test/guards` green, checkout clean); **violated in spirit by self-test** — see C-2 (it writes *inside* `.qompack/`, but into the immutable tier) |
| net only in `ipc`; `os/exec` only in daemon/cli/tools | **met** — `os/exec` added to `internal/cli` only in the Windows-only `fault_lockdir_windows.go`, which the security job's allow-list already permits |
| nothing imports cli | **met** (pre-existing `testutil` exception unchanged) |
| exactly ONE commit, exact subject, no attribution trailers | **met** — `git log --oneline ef50515f..8c84cf9` → one commit; `git show -s --format=%B 8c84cf9` matches the brief's subject and body verbatim (the brief's subject, not the spec's longer one — brief wins per instruction); no `Co-Authored-By`, no `Generated with` |

### 1.8 Verification I re-ran independently

- `go build ./...` — clean · `go build -tags noinject ./...` — clean · `go vet ./...` — clean
- `go test -count=1 ./internal/cli/... ./internal/testutil/...` — PASS
- `go test -count=1 ./test/guards/...` — PASS (17.0 s)
- `go test -count=1 ./test/e2e/...` — PASS (69.5 s)
- `go test -count=1 ./tools/devtool/...` — PASS
- `go run ./tools/devtool lint --only=importgraph,testdeps,bindeps,sleepcheck,stubskips` — all PASS
- working tree clean after all of the above (`git status --porcelain` empty)

The report's claims are accurate. No transient observed.

---

## 2. Findings

### Critical

**C-1 — `test/e2e/hooks_test.go`: `TestE2E_AllSixHooksExitZero` deleted; five downstream VERIFY gates now pass vacuously.**

*What.* The commit removes the test outright (diff `test/e2e/hooks_test.go`, −44 lines). Nothing in
the tree carries that name any more.

*Why it matters.* That test is named **by name** as a verification command in six plan documents:

- `plans/V1-SP-01-foundation-toolchain-and-contracts.md:1993, 2183, 2259` (DoD 13)
- `plans/V1-VERIFY-foundation-and-contracts.md:307` (O6), `:441`
- `plans/V2-VERIFY-primitives-store-dag-and-baseline.md:112` (V2-SP01-22)
- `plans/V3-VERIFY-observer-and-negative-knowledge.md:114` (A15)
- `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:200` (V4-SP01-11)
- `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md:125` (I-01.23)
- `plans/V6-VERIFY-production-readiness-and-uat.md:78` (1.1.19)

I ran the exact command those gates specify:

```
$ go test -run TestE2E_AllSixHooksExitZero ./test/e2e/
ok  github.com/qompack/qompack/test/e2e  1.113s [no tests to run]
$ echo $?
0
```

Five future verification checkpoints will now report green while asserting nothing. That is the
worst failure mode a checkpoint can have.

The *hook-log* half of the test's assertions is legitimately obsolete. The other half — "the real
binary, all six hook subcommands, a real temp project, representative payloads, every exit code 0,
every stdout parses as `hookio.Output`" — is exactly the §2.3 property SP-05 is supposed to make
*more* true, and it is still perfectly assertable.

*Fix.* Restore `TestE2E_AllSixHooksExitZero` in `test/e2e/hooks_test.go` under its original name,
keeping the exit-0 + `hookio.Output`-parse loop over `hookSubcommands` and dropping only the
`hookLogLines` count and the `SessionStart`/`PreCompact` exact-bytes switch (or keeping a
`SessionStart → hookSpecificOutput` check as IT-1 now does). Add the daemon-shutdown cleanup the
sibling tests use. If the controller instead decides the name should die, that decision must be
propagated to all six plan documents in the same change — silently orphaning them is not an option.

---

**C-2 — `internal/cli/selftest.go:174-197`: `qompack self-test` permanently writes a fake, read-only checkpoint into the project's immutable tier.**

*What.* `selfTestAppendOnlyGuard` seeds a probe file via
`paths.CreateNew(paths.CheckpointPath(l, core.CheckpointSeq(0)), []byte("{}\n"))` and never removes
it. `paths.CreateNew` writes it read-only.

*Verified empirically* — built the binary, ran `qompack self-test --json` against a fresh temp
project:

```
/tmp/.../.qompack/checkpoints/0000.json      -r--r--r--   3 bytes   contents: {}
```

*Why it matters.*
1. `checkpoints/` is the immutable tier §7.4 exists to protect. A *diagnostic* command now mutates it.
2. The file is `0444` / `FILE_ATTRIBUTE_READONLY`, so a user cannot simply delete it on Windows.
3. It is **not** registered in `checkpoints/MANIFEST.jsonl`, so `qompack fsck` (SP-17) will later see
   an unlisted checkpoint artifact and report store corruption caused by qompack itself.
4. `core.CheckpointSeq(0)` is a *real* sequence number. On a project that already has checkpoint 0,
   `CreateNew` returns `IsExist`, the code proceeds, and the probe then attempts
   `paths.OpenFile(realCheckpoint, O_WRONLY|O_TRUNC)`. Today the guard refuses — which is the whole
   point of the test — but the design puts a truncating open against a *user's real checkpoint* one
   guard-regression away from data loss. A self-test must never aim a destructive syscall at real data.

*Fix.* Point the probe at a clearly-synthetic path the guard also protects but that no real artifact
can occupy — e.g. a `paths.Of(root).Tmp`-based path if `IsProtected` covers it, or a reserved
`checkpoints/.self-test-probe.json` name explicitly excluded from `fsck`/manifest scanning — and
remove it afterwards (clearing the read-only bit first, as `removeSpawnLockFile` already does at
`internal/daemon/spawn.go:36`). If `IsProtected` only covers `checkpoints/*.json`, prefer asserting
the guard against a freshly created, immediately deleted file rather than seq 0. Add a test that
`self-test` leaves the project byte-identical apart from `.qompack/logs` and `run/`.

---

### Important

**I-1 — `internal/cli/hookclient.go:154-156`: two `state.bin` reads on the hot path; `State: st` is dead.**
`ipc.NewClientWithOptions` (`internal/ipc/client.go:147-160`) documents and implements: *"When
`o.ProjectRoot` is set, `ReadState(o.ProjectRoot, config.Defaults())` is used instead and this field
is ignored."* The hook body sets **both** `ProjectRoot: root` and `State: st`, so the 32-byte record
is read twice per hook invocation and the `st` the body carefully re-read after root re-resolution is
discarded. The spec's skeleton comment is explicit: *"one 32-byte read; no config.Load."* This also
makes `TestE2ESpoolSubmodeEndToEnd` pass by accident of the second read rather than by the `State`
it passes. `ProjectRoot` cannot simply be dropped — `lazySpawn` and blob externalization need it.
*Fix:* in `ipc.NewClientWithOptions`, prefer a non-zero `o.State` over the disk read even when
`ProjectRoot` is set (ipc is SP-05-owned, so this is in-subplan), and add a comment at the call site.
Failing that, delete `State: st` and document that the client owns the authoritative read.

**I-2 — `internal/cli/hookclient.go:46-49`: every hot-path drop is invisible — no counter, no durable Loud.**
`cliMetrics` is a nil `obs.Registry`; `cliLog` is `logging.Nop()`. `internal/ipc/client.go:341-358`
nil-guards the registry, so `l0.dropped` / `l0.spooled` are simply never recorded. And
`logging.Nop()` is `logger{s: nil}` — `Loud` (`internal/logging/logger.go:114-122`) writes to the
file sink and `LOUD.log` **only when `l.s != nil`**; with a nil sink it reaches only the in-process
ring, which dies with the hook process microseconds later. Consequences:
- The fault table's expected-behaviour column ("`l0.dropped` +1, one `Loud`") is unachievable for
  `spool-readonly`, `spool-full` and `disk-full`. The 66-combo test only asserts exit 0, so this is
  invisible to the suite.
- §12's "degradations are never silent" is violated on the hot path: a project whose spool has gone
  read-only drops every observation with **zero** on-disk evidence.
- The spec text explicitly says `metrics` is *"SP-01's registry constructor"*, not nil — an
  undeclared deviation.
*Fix:* construct the client's logger lazily — a `logging.Logger` that materializes a real file sink
on first `Loud`/`Warn` only when `.qompack/logs` already exists (the same rule `logQuiet` follows), so
the zero-failure path still allocates nothing. Either wire `obs.New(clk)` per the spec, or record the
deviation with a ruling and drop the fault table's counter expectation.

**I-3 — `internal/cli/hookclient.go:149-152`: `daemon-down` does not make the daemon unreachable.**
The spec's row: *"`ipc.Resolve` returns an address nothing is listening on **and**
`ClientOptions.Spawn` is set to a no-op."* Only the second half is implemented. In
`TestHooksExitZeroUnderFaults` the intended condition holds incidentally (each combo uses a fresh
temp root with no daemon), but in `TestSelfTestIsTheOnlyNonZeroExit` a real daemon is brought up by
the three degrading `session-start` calls and is still live when the ten `daemon-down` cases run
(shutdown happens only in `t.Cleanup`). So that test's central premise — "every subcommand under
`daemon-down` still exits 0" — is not actually exercising `daemon-down`.
*Fix:* have the fault site override the resolved address (e.g. return
`ipc.Addr{Kind: addr.Kind, Path: addr.Path + ".fault-down"}` on POSIX / a unique pipe name on
Windows) in addition to no-op'ing `Spawn`, and assert in the e2e test that the daemon is genuinely
unreachable for those cases.

**I-4 — `test/e2e/v1_integration_test.go`: IT-1's payload-leak assertion deleted with no replacement.**
The removed `v1Secrets` block carried its own justification: *"This is the load-bearing half of
IT-1. §7.4 makes the hook log an observability record, not a content store… would leak prompts and
file contents into a file nobody thinks of as sensitive."* Seven concrete secrets (the verbatim
prompt, a `tool_input` file path, a `tool_response` key, the derived Bash command, `transcript_path`,
the payload `cwd`) were asserted absent. The *file* those assertions targeted is gone, but the
*property* is not obsolete: `logQuiet` now writes `.qompack/logs/hook-quiet-*.jsonl` containing
`err.Error()` strings, and the daemon writes its own day log — neither may carry payload content.
Nothing pins that any more.
*Fix:* re-point the assertion at the whole of `.qompack/logs/**` (day log, `LOUD.log`,
`hook-quiet-*.jsonl`) rather than at `hooks-*.jsonl`, keeping the same seven strings. Note the WAL
under `spool/` is deliberately a content store and must be excluded.

**I-5 — `test/e2e/v1_integration_test.go` IT-2 replacement sub-test is racy and does not prove its claim.**
`bare_hook_never_reads_project_config_before_a_daemon_has_run` asserts
`require.NoFileExists(run/state.bin)` before and after an `observe tool` run. Two problems:
1. **Race.** A bare `observe tool` with `Self` non-empty (real binary) reaches
   `client.lazySpawn()` on connect failure, which starts a detached daemon; that daemon writes
   `run/state.bin` on startup. The post-run assertion is a stopwatch against process spawn latency.
   `v1LimitProject` even registers a shutdown cleanup *because* a daemon comes up. This will flake.
2. **Non-sequitur.** "state.bin does not exist" does not establish "the read limit came from
   `config.Defaults()`", which is what the comment claims. Nothing observable changed.
Additionally, the deleted `bootstrap_limit_applies_before_any_config_file` sub-test pinned a real,
still-live property — a 2 MiB payload must be clamped, logged, and exit 0, never crash or hang — and
has no replacement anywhere.
*Fix:* replace the tautology with something observable: send a payload just over
`config.Defaults().Runtime.HotPath.MaxPayloadBytes * 4` and assert the hook exits 0 with `{}` and
spools nothing, and a payload just under it and assert one spool line — that distinguishes the
default limit from the project's `projectLimit` behaviourally. Restore the 2 MiB clamp case in the
same shape. Remove the racy `NoFileExists`, or make it `require.Eventually`-free by disabling spawn
(`QOMPACK_PROJECT_ROOT` unchanged but no `Self`) — which is not available from the binary, so prefer
deleting the assertion.

**I-6 — `test/e2e/daemon_e2e_test.go:321-323`: the spool-submode "never connected" assertion cannot fail.**
```go
require.Equal(t, before.Counters["ipc_decode_error"], after.Counters["ipc_decode_error"],
    "a spool-submode client must never have connected at all")
```
`ipc_decode_error` (`internal/ipc/server.go:18`) increments only on a *malformed frame*. A client
that connected and sent a perfectly good request leaves it unchanged too, so the assertion is
satisfied by both the pass and the fail case. There is genuinely no accept/connection counter in
`internal/ipc` or `internal/daemon` (I checked every `counter*` constant), so the spec's literal
"accept counter" is unavailable — but that is an adaptation that needed a ruling, not a silently
substituted counter with a misleading message.
*Fix:* assert something that actually distinguishes the two worlds — e.g. that the session's
`wal-<sess>.ndjson` line count is unchanged for a bounded interval after the call (a connected
request would be WAL-appended by `Accept` before ACK), or add an `ipc_accept` counter to
`internal/ipc/server.go` (SP-05 owns ipc) and use it.

**I-7 — `internal/cli/hookclient.go:189-198`: `testing.Testing()` in product code links `testing` into the shipped binary.**
Verified: `go list -deps ./cmd/qompack` now contains `testing` and `runtime/trace`. §2.5's
dependency discipline is about exactly this; `devtool bindeps` only polices third-party modules, so
nothing caught it. It also disables `ensureDaemonRunning`/lazy-spawn in every in-process test,
leaving `internal/cli/sessionstart.go`'s real branch with no unit coverage at all.
*Fix:* `var selfPath = func() string { p, err := os.Executable(); if err != nil { return "" }; return p }`
and have `internal/cli`'s and `internal/testutil`'s test setup override it to `""` (or plumb `Self`
through `cli.Env`, which is the existing injection convention and would let a test assert
`EnsureRunning` was called with the right arguments). Then drop the `testing` import. See
adjudication (e) for the full reasoning; the current behaviour is *safe*, so this is hygiene, not a
live bug.

**I-8 — `internal/cli/hookclient.go:233-244`: `logQuiet` has zero test coverage.**
`grep -rn "logQuiet\|hook-quiet" internal/cli/ test/` finds no hit outside `hookclient.go` itself.
The "append **only if the log directory already exists**; never create directories on a failing
project's error path" rule is an explicit binding ruling in the brief and a §12 obligation, and
nothing pins it. It is trivially testable in-process (feed `errReader{}` as stdin with a root whose
`.qompack/logs` does and does not exist; assert file presence/absence and that no directory was
created).
*Fix:* add two table cases to `internal/cli/hooks_test.go`.

**I-9 — `test/guards/v1_integration_test.go:183-240`: IT-4 now snapshots a tree a live daemon is writing.**
`TestV1_WriteSetConfinedAcrossFullHookSequence` runs the real binary, whose `session-start` now
brings up a resident daemon. The daemon is shut down only in `t.Cleanup` — i.e. **after** every
assertion. So `snapshotTree` (`test/guards/writeset_test.go:179-202`) walks `.qompack/` while the
daemon is actively creating, renaming and deleting files inside it. Two concrete failure modes:
- `filepath.WalkDir` lists a file (WAL rotation target, `tmp/` staging file, `spawn.lock`) that the
  daemon removes before `os.ReadFile` reaches it → the callback returns the error →
  `require.NoError(t, err, "snapshotting %s", root)` fails the guard for an unrelated reason.
- `require.Empty(t, entries, "WriteAtomic left staging files in .qompack/tmp/")` races the daemon's
  own `WriteAtomic` staging.
It is green today on Windows (I ran `./test/guards/...`, 17.0 s), but this is a new flake surface in
a *guard* — the tests that must be trustworthy above all others.
*Fix:* move `v1ShutdownDaemonIfReachable(t, p.Root)` from `t.Cleanup` to an explicit call
immediately after the hook loop and before `snapshotTree(after)`, keeping the `t.Cleanup` as a
belt-and-braces no-op. Consider tolerating `fs.ErrNotExist` in `snapshotTree`'s read.

---

### Minor

**M-1 — `test/guards/v1_integration_test.go:737-790`: orphaned doc comment.** The new
`v1ProbeTimeout…`/`v1ShutdownDaemonIfReachable` block was inserted *between* `v1BuildBinary`'s doc
comment and its declaration with no blank line, so godoc now attaches
`// v1BuildBinary compiles cmd/qompack into a temp directory…` to the `const` block, and
`v1BuildBinary` is undocumented. Move the new block below `v1BuildBinary`, or reinsert the blank
line.

**M-2 — `test/e2e/v1_integration_test.go:47-78`: stale, now-dead test data.** `wantStdout` is no
longer read by any assertion but is still populated for all eight lifecycle entries, and its doc
comment still says *"the exact response the host must receive"*. Two of the values
(`PreCompact`'s `hookSpecificOutput`) are now factually wrong. Likewise `bashCommand`'s comment at
:81-83 still says *"It is asserted absent from…"* after the leak assertion was deleted (see I-4).
Delete the field or restore an assertion that uses it.

**M-3 — `internal/cli/hookclient.go:241`: `logQuiet` uses `time.Now()`, not the injected clock.** Two
lines of behaviour away from a TS stamped from `env.Clock`, and against the repo-wide "every
time-taking component takes a `core.Clock`" rule. Pass `clk` down (it is already in scope at both
call sites).

**M-4 — `internal/cli/fault.go:167-170`: `disk-full` under-implements the spec row.** The spec names
*"`spool.Append`, `logQuiet` **and** `ipc.WriteState` return `syscall.ENOSPC` from their first
write"*; only `Append` is wrapped. `WriteState` is not on the hook path so it is arguably moot, but
`logQuiet` is (ReadEvent failure, `ErrAddrTooLong`). Either wrap it or record the narrowing.

**M-5 — `internal/cli/fault.go:154, 159-166`: `spool-full`'s default allowance of 1 makes the site
inert for a single-hook run.** A hook makes at most one `Append`, and `faultSpool.Append` fails only
when `n > allowed`, so with the default the one append **succeeds** and no drop ever occurs — all six
`spool-full` combos in the 66-matrix pass without the site doing anything. The spec's phrasing
("once the process has written `faultArg` lines (default 1)") is literally honoured, so this is not a
deviation, but the site as configured tests nothing. Either default to 0 or have the e2e matrix use
`spool-full:0`.

**M-6 — `internal/cli/hookclient.go:160-163`: no floor on the derived deadline.** `deadline <= 0` falls
back to `time.Duration(st.AckDeadlineMs) * time.Millisecond`; if that field is itself 0 (a daemon
that wrote a zero, a hand-crafted `state.bin`), the fire-and-forget ops call `Send` with a zero
deadline. `NewClientWithOptions` floors its *own* `AckDeadline` from `StateFromConfig(Defaults())`
only when the whole `State` is zero, not per-field. Add `if deadline <= 0 { deadline = <named
default> }`.

**M-7 — `internal/cli/hookclient.go:85, 104, 111`: fault sites `MkdirAll` under `root` before the
refuse-to-conjure guard.** `faultCorruptStateIfNeeded` and `faultCorruptConfigIfNeeded` create
`.qompack/run` and `.qompack/` respectively; the `isDir(root)` guard that exists precisely to stop a
store being conjured under a nonexistent root runs at :119, *after* them. Fault-injection-only, but
it undermines the guard the very tests that exercise it rely on. Move the guard above the fault
calls, or make the fault helpers themselves no-op when `!isDir(root)`.

**M-8 — `QOMPACK_FAULT` appears in two non-test files.** `internal/cli/fault.go:42` and
`internal/daemon/spawn.go:54`. The second is pre-existing and structurally necessary
(`buildSpawnEnv` must name what it strips), and the spec itself acknowledges it ("and that
`SpawnDetached` strips it from the child environment (it does, see `spawn.go`)"). But the stated CI
contract is "exactly one non-test file", and `.github/workflows/ci.yml`'s `security` job contains no
such grep at all — so the constraint is neither satisfied nor enforced. Needs a plan amendment
("exactly one file *that reads it*", with `spawn.go` exempted) plus the grep actually being written,
or the constraint dropped. Not this task's fault, but this task is where it should have been surfaced.

**M-9 — `test/e2e/faultinject_test.go:250-267`: `TestFaultSitesInertWhenUnset` splits its two halves.**
The spec puts both "faultActive false for all eleven sites" and "byte-identical vs `-tags noinject`"
in this one test; the first half lives in `internal/cli/fault_test.go`'s
`TestFaultActive_UnsetIsInertForAllElevenSites`. Reasonable (the e2e package cannot see unexported
`faultActive`) — worth a cross-reference comment. Separately, `e2e.Run` inherits `os.Environ()`
(harness.go), and this test only *omits* `QOMPACK_FAULT` from its overlay rather than clearing it, so
a developer with it set in their shell would get a silently meaningless pass. Add an explicit
`qompackFaultEnvKey: ""` to the overlay.

**M-10 — `internal/cli/hookclient.go:281-297`: `homeDir` is misfiled.** It is used only by
`daemon.go` and `selftest.go`, never by a hook body, and it lives in the file whose whole doc-comment
premise is "the hot path". Move it to `config.go` or a small `env.go`.

**M-11 — `internal/cli/fault_lockdir_windows.go`: `icacls` in the shipped binary; unused under `noinject`.**
The file carries only `//go:build windows`, so under `-tags noinject` `denyWriteDir` compiles but is
unreachable (`faultLockSpoolDirIfNeeded` is a no-op) — `golangci-lint`'s `unused` would flag it if
lint were ever run with that tag. And the production Windows binary now contains a code path that
shells out to `icacls /deny`, gated only by an env var. The brief left the placement ambiguous and
the implementer's reasoning (report concern 6) is sound, but adding `//go:build windows && !noinject`
would at least keep it out of the hardened build.

**M-12 — `config-corrupt`'s real behaviour is untested.** The fault table says the site is inert for
hooks and *"exercised for real by `qompack daemon`/`self-test`: per-leaf fallback + `Loud` (§11.3);
exit 0"*. No test runs either subcommand under `config-corrupt`. `runDaemon` implements the fallback
(daemon.go:70-73); `self-test` reports `config.load` as SevCritical on error, which would make it
exit 1 — arguably correct for a diagnostic, but nothing pins either. Add one case to
`TestSelfTestIsTheOnlyNonZeroExit`'s table or a dedicated e2e case.

**M-13 — `internal/cli/dispatch_test.go:110`: sub-test name is now wrong.** `"unresolvable project
root"` no longer describes what the case exercises (see adjudication (a)). Rename to
`"project root that does not exist"`.

**M-14 — `tools/devtool/bindeps.go:44`: allow-list prefix is broader than the justification.**
`strings.HasPrefix(importPath, "golang.org/x/sys/windows/")` admits `registry`, `svc`, `svc/mgr`, etc.
go-winio needs only `golang.org/x/sys/windows`. The pinned test even asserts
`{"golang.org/x/sys/windows/registry", true}`, deliberately widening the surface. Consider dropping
the prefix clause and keeping only the exact match, unless a subpackage is actually reached.

---

## 3. Notes for the controller (not defects)

- **Report accuracy.** Every verification claim in `task-6-report.md` that I re-ran reproduced. The
  66-row table, the build matrix, the lint sub-checks and the clean-checkout claim all hold. The
  report is unusually candid — five of the nine Important findings above are *adjacent* to concerns
  the implementer raised themselves.
- **Documentation quality** in the new files is genuinely high: `loadFaultMap`'s colon-ordering
  comment, `faultsites.go`'s "one spelling for both build variants" rationale, `faultSpool`'s
  explanation of why `Close` must be forwarded explicitly through an embedded interface, and
  `TestE2ESelfTestExitsNonZeroOnCritical`'s "why three SessionStarts, not two" are all the kind of
  comment that survives contact with a future maintainer.
- **`test/e2e` process hygiene** is solid: `Build`/`buildNoInject` are `sync.Once`-guarded and cleaned
  up in `TestMain`; every combo gets its own `t.TempDir()`; `resetPermissionsForCleanup` is registered
  *after* `t.TempDir()` so LIFO ordering runs it *before* `RemoveAll` (correct); the Windows deny-ACE
  is reset with `icacls /reset /t /c`; and every daemon-bringing test explicitly shuts down.
  `QOMPACK_IPC_ADDR` is not used, but endpoint uniqueness is guaranteed anyway because
  `ipc.Resolve` hashes the (normalized) project root and every test root is a distinct `t.TempDir()`
  — an acceptable adaptation of the brief's suggestion.
- **Deferred/carried items unchanged by this task:** the go-winio `Close`-vs-`Accept` Windows wrinkle
  (bounds left deliberately generous, per instruction) and every previously-deferred minor from
  Tasks 1-5.

---

## Summary

| Severity | Count |
|---|---|
| Critical | 2 |
| Important | 9 |
| Minor | 12 |

**Verdict: Needs fixes (2 Critical, 9 Important).**

---

# Re-review (fix round 1)

Reviewed: `8c84cf9` → **`bdc9585c`** (amended into the same single commit; subject/body byte-identical
to the brief's, no attribution trailers — re-verified with `git show -s --format=%B`).
Delta: 22 files, +689/−164.
Scope: verify C-1/C-2 and I-1..I-9, adjudicate the one out-of-findings fix (connect-deadline floor).

## Per-finding status

| # | Status | Evidence |
|---|---|---|
| **C-1** | **FIXED** | `TestE2E_AllSixHooksExitZero` restored under its exact original name in `test/e2e/hooks_test.go`. I ran `go test -count=1 -v -run TestE2E_AllSixHooksExitZero ./test/e2e/`: six named sub-tests actually execute (PostToolUse … SessionEnd), 3.05 s, PASS — no more `[no tests to run]`. The body asserts `require.Equal(t, 0, code, …)` against the **real built binary** plus a `hookio.Output` unmarshal, so a non-zero exit is caught: `harness.go`'s `Run` maps `exec.ExitError` to `ExitCode()`, and sibling tests in the same package demonstrably observe non-zero codes (`TestE2E_UnknownCommandExitsTwo` → 2, `TestSelfTestIsTheOnlyNonZeroExit` → 1, both passing). All five downstream VERIFY gates are live again. |
| **C-2** | **FIXED** | Two halves, both verified empirically, not just by reading. **(a) Probe artifact.** `selfTestAppendOnlyGuard` now uses `selfTestCheckpointProbeSeq = 999999999` (out of any real sequence range) and always cleans up: `cleanup()` clears the read-only bit (`os.Chmod 0600`) then `os.Remove`, called explicitly on the `CreateNew`-failed path and via `defer` on every success path — including a panic unwind. I built the binary and ran `self-test --json` then `self-test` against the same temp project: `.qompack/checkpoints/` is **empty** after both (previously it held a permanent `-r--r--r-- 0000.json`). No unlisted artifact, and no destructive syscall is ever aimed at a real sequence number. **(b) Observability.** `cliLog`/`cliMetrics` replaced by `newHookLogger(root)` (lazy file sink, materialized on first `Warn`/`Error`/`Loud`, only if `.qompack/logs` already exists) and `newHookMetrics(clk) = obs.New(clk)`. Verified live: `QOMPACK_FAULT=disk-full` with an existing logs dir → exit 0, `{}` on stdout, **and** a durable `LOUD.log` line: `msg="ipc: spool append refused — event dropped" op=observe.tool err="qompack: fault disk-full: no space left on device"`. Verified the negative half too: `QOMPACK_FAULT=disk-full,daemon-down` against a project with no `.qompack/logs` leaves only `.qompack/run/spawn.lock` behind — **no logs directory is conjured**. The fault table's "one Loud" expectation is now genuinely satisfiable. |
| **I-1** | **FIXED** | `internal/ipc/client.go`'s precedence switch inverted: `case st != (State{})` first ("trust the caller's own already-read State; never re-read from disk"), then `ProjectRoot`, then `StateFromConfig(defaults)`. The hot path is back to the spec's one 32-byte read, with a call-site comment in `hookclient.go:259-263` explaining why `ProjectRoot` is still set (lazySpawn lock + `externalize()` blob dir). Blast radius checked: every other `NewClientWithOptions` caller (`selftest.go`, `internal/daemon/ingest_test.go`, `internal/ipc/client_test.go`, the three e2e/guards admin clients) passes either a zero `State` or no `ProjectRoot`, so none changes behaviour. `go test ./internal/ipc/...` PASS. **One residual — see N-1.** |
| **I-2** | **FIXED** | Folded into C-2(b); verified above. |
| **I-3** | **FIXED** | New `faultDaemonDownAddr` (`fault.go:294-305`) appends `.qompack-fault-daemon-down` to the resolved path, applied at `hookclient.go:243` *after* `ipc.Resolve` succeeds (so the `ErrAddrTooLong` path is untouched) and mirrored as identity in `fault_noinject.go`. The site now satisfies both halves of the spec row, so `TestSelfTestIsTheOnlyNonZeroExit`'s "under daemon-down" premise is real even with a live daemon in the project. **See N-4** (no unit test). |
| **I-4** | **FIXED, and improved** | `v1Secrets` restored verbatim (all seven strings) with a new `v1LogsCorpus` helper that walks the whole of `.qompack/logs/**` — day log, `LOUD.log`, and `hook-quiet-*.jsonl` — rather than the single `hooks-*.jsonl` the original targeted. The doc comment correctly and explicitly **excludes** the WAL under `spool/` as the deliberate content store. Broader coverage than the original. |
| **I-5** | **FIXED, and materially better** | The tautological/racy `NoFileExists(state.bin)` sub-test is gone. Three sub-tests keyed to `config.Defaults().Runtime.HotPath.MaxPayloadBytes * 4` (the limit `hookio.ReadEvent` actually enforces): just-under → parses, spools exactly one line; just-over → never reaches the spool at all; `boundary*2` → still exit 0 with `{}` (the restored bootstrap-clamp property, recalibrated). The discrimination argument holds: with `projectLimit = 8192`, the project layer's `*4` would be 32 768, so a ~4 MiB just-under payload passing **proves** `config.Defaults()` governed. `v1PayloadOfSize` is byte-exact (`require.Len(t, out, n)`), so the ±256 margins are deterministic, not approximate. |
| **I-6** | **FIXED** | `TestE2ESpoolSubmodeEndToEnd` now compares the session's `wal-<sess>.ndjson` line count before/after the spool-submode call — a counter that genuinely moves when a hot-path op is accepted (`ingest.Accept` WAL-appends before ACK), unlike `ipc_decode_error`. The positive control exists in the same package (`TestE2EHookRoundTrip` asserts 50 observe-tool calls → at least 50 WAL lines), so the negative assertion is not vacuous. |
| **I-7** | **FIXED (Ruling #28)** | `selfPath()` deleted; `cli.Env` gains `Self string`, populated only by `cmd/qompack/main.go` via `os.Executable()`. `hookclient.go` no longer imports `testing` **or** `os`. Threaded explicitly through `hookSpec.preSend(root, self, clk)` and `selfTestDaemonReachable(root, addr, self, clk)` — an injected dependency, not a global seam. Verified myself: `go list -deps ./cmd/qompack` piped to `grep -c '^testing$'` → **0**, and `runtime/trace` → **0**. `ensureDaemonRunning` is now in principle unit-testable; no test exercises its `EnsureRunning` branch yet, but that is pre-existing and no longer structurally blocked. |
| **I-8** | **FIXED** | Two new tests. `TestHooks_LogQuietWritesWhenLogsDirExists` pins the positive case down to the record shape (`quietLogLine.TS` non-empty, `.Err` naming the stdin failure). `TestHooks_LogQuietNeverCreatesLogsDir` pins the negative case and goes further than I asked — it asserts `.qompack/` **itself** is never conjured, not just `logs/`. |
| **I-9** | **FIXED** | `v1ShutdownDaemonIfReachable(t, p.Root)` is now called explicitly between the hook loop and `snapshotTree(after)` in `TestV1_WriteSetConfinedAcrossFullHookSequence`; the `t.Cleanup` registration is retained as an idempotent no-op. The daemon is gone before any assertion walks the tree. `go test ./test/guards/...` PASS (19.5 s). |

### Minors

M-1 through M-14 are all addressed except **M-8** — see N-5. Spot-verified: M-1 (`v1BuildBinary`
moved directly under its own doc comment), M-3 (`logQuiet(root, err, clk)` takes a `core.Clock`),
M-4 (narrowed with a genuinely good argument: `ipc.WriteState` is never called by a hook, and
`logQuiet` already discards every write error so an injected ENOSPC there would be
observationally identical — accepted), M-5 (`"spool-full:0"` in the e2e site list, documented),
M-6 (`hookSendDeadlineFloor`), M-7 (both `faultCorrupt*IfNeeded` now bail on `!isDir(root)`, with a
new test), M-9 (`QOMPACK_FAULT: ""` explicitly cleared in the overlay plus a cross-reference
comment), M-10 (`homeDir` moved to `config.go`), M-11 (`fault_lockdir_*.go` gated `&& !noinject`),
M-12 (site wired into both `daemon.go` and `selftest.go`, with
`TestSelfTest_ConfigCorruptFallsBackAndExitsZero`), M-13 (renamed), M-14 (exact match only; the
pinned test flipped `.../registry` to `false`).

## Adjudication — the connect-deadline floor (out-of-findings fix)

### (1) Is the diagnosis sound, or does the floor mask a product bug elsewhere?

**Sound, but incompletely attributed — accept the fix, ledger the underlying weakness.**

The evidence is good: bisection isolates I-1's diff alone as the trigger, and the causal story checks
out mechanically. `config.Defaults()` really does ship `ConnectDeadlineMs: 5`
(`internal/config/defaults.go:114`), documented as *"deadline for a hot-path client to connect"* — a
budget sized for a warm daemon, not a cold one. Removing a synchronous `ReadState` really did remove
a few ms of incidental slack between `EnsureRunning`'s return and the first real dial. So the
regression is real and the attribution to I-1 is correct.

Where the framing understates things: this is not purely a too-tight-budget problem, it is also a
**readiness-contract weakness in `daemon.EnsureRunning`**. `EnsureRunning` declares the daemon up on
the first successful `ipc.Probe`, and `Probe` (`internal/ipc/probe.go`) *dials and immediately
closes* — it consumes the very accept slot it uses as proof. On Windows the listener must re-post a
`ConnectNamedPipe` before the next dial can land; on POSIX the accept loop must come back around.
"Probe succeeded" therefore does not imply "the next dial will be accepted within 5 ms", on either
platform. That gap is what the 250 ms floor is papering over.

That is not a reason to reject the fix — the gap lives in `internal/ipc`/`internal/daemon`
(pre-posting multiple pipe instances, or a non-consuming final probe), squarely outside task 6's
file scope, and Ruling #22 deliberately chose dial-only liveness. But it should be **ledgered for
the final review** as a real follow-up, alongside the drain-cadence note, rather than recorded as
"the 5 ms budget was simply too small". The floor is a correct mitigation at the layer that can
apply one; it is not the root fix.

### (2) Right layer? Consistent with the plan's deadline table?

**Layer: yes.** Per-op deadline policy already lives in `hookSpec`, so a per-op connect budget
belongs there. It is deliberately *not* a `config.Defaults()` change (which would loosen the budget
for the daemon and every op) and *not* an `ipc` change (which would affect every caller). Correct
call.

**Budget fit: yes for the three ops the fix names.** 250 ms of connect sits at 2.5 % of
session-start's 10 s, and 1.7 % of checkpoint's and flush's 15 s — comfortably inside both those
reply deadlines and their 15 s / 20 s / 20 s manifest timeouts. The "a genuinely absent daemon fails
the dial almost instantly, it does not wait out the timeout" claim is correct: a nonexistent named
pipe or socket is a fast `ERROR_FILE_NOT_FOUND` / `ENOENT`, not a timeout.

**Consistency: NO — the predicate does not match the stated intent.** See **N-2**: the guard is
`spec.deadline > 0`, and `observe prompt` carries `deadline: promptReplyDeadline` (250 ms), so it is
included. Both the constant's own doc comment (*"as opposed to a hot-path fire-and-forget op
(observe.\*), which keeps `State.ConnectDeadlineMs`'s tight … budget untouched"*) and the report
(*"observe.tool/observe.prompt/observe.stop are untouched"*) say otherwise.

### (3) Is hot-path B-A p99 under 15 ms untouched?

**For `observe.tool` and `observe.stop`: yes, verifiably.** Both have `spec.deadline == 0`, so
`connectDeadline` stays `time.Duration(st.ConnectDeadlineMs) * time.Millisecond` = 5 ms.
`AckDeadline` is untouched for every op (still `State.AckDeadlineMs`, 8 ms), so the write+ACK half of
B-A is unchanged everywhere.

**For `observe.prompt`: no.** `ipc.Op.HotPath()` (`internal/ipc/op.go:59-65`) returns true for
`OpObserveTool`, `OpObservePrompt` and `OpObserveStop` alike, so `observe.prompt` **is** a B-A
hot-path op, and its connect ceiling went 5 ms → 250 ms — a 50x widening, undeclared.

Practical exposure is narrow: a deadline is a ceiling, not a cost, so with a warm daemon the p99 is
unchanged and a bench run against a healthy daemon will show no regression. The exposure is the
stalled-daemon tail — a daemon that exists but has not re-posted its accept — where
`UserPromptSubmit` now blocks up to 250 ms (100 % of its own reply deadline) before falling back to
the spool, against 5 ms previously. It cannot fail or block a turn (5 s manifest timeout), but 5 ms
was a deliberate §2.4 choice for exactly this op family, and changing it needs to be a decision, not
a side effect of a predicate that happens to catch a fourth op.

### (4) Concern 2 (drain cadence) — deferrable?

**Agreed, deferrable, no action.** A missed dial spools; the spool is durable and is drained by the
daemon on start and on its idle tick, so the cost is latency-to-WAL, not loss. With the connect
floor in place the missed-dial case is rarer still for the ops that matter. Correctly ledgered for
the final review.

## New defects

**N-1 (Important) — `internal/ipc/client.go:89-93`: `ClientOptions.State`'s field doc still states the contract this fix inverted.**

```go
// State supplies mode, hot, deadlines and limits when ProjectRoot is empty …
// When ProjectRoot is set, ReadState(ProjectRoot, config.Defaults()) is used instead and
// this field is ignored.
State State
```

That is now false, and it directly contradicts `NewClientWithOptions`'s freshly-updated doc ~50 lines
below. The type-level comment above it (*"ProjectRoot empty disables both ReadState and lazy
spawn"*) is likewise no longer accurate. This is exactly the field whose semantics C-2/I-1 inverted,
so leaving the old contract in place is a live footgun: a future caller that reads it will either
skip passing a `State` it already has (paying a redundant disk read on the hot path — reintroducing
C-2's root cause), or pass a cached/stale `State` believing it will be ignored and silently get it
used as authoritative. In a codebase where doc comments are consistently treated as normative, a
contradiction between a field doc and its constructor doc is the kind of thing a later maintainer
"resolves" by restoring the old behaviour.

*Fix (3 lines):* rewrite the field doc to match — a non-zero `State` always wins; `ProjectRoot` is
consulted for `State` only when `State` is the zero value, and independently governs lazySpawn's
lock path and `externalize()`'s blob directory.

**N-2 (Important) — `internal/cli/hookclient.go:250-256`: the connect-deadline floor also widens `observe.prompt`, a hot-path op, contrary to its own comment and to the report.**

```go
connectDeadline := time.Duration(st.ConnectDeadlineMs) * time.Millisecond
if spec.deadline > 0 && connectDeadline < hookConnectDeadlineFloor {
    connectDeadline = hookConnectDeadlineFloor
}
```

`hooks.go:19` registers `observe prompt` as `doHook(hookSpec{op: ipc.OpObservePrompt, reply: true,
deadline: promptReplyDeadline})` — `spec.deadline == 250ms > 0` — so the floor applies to it.
`ipc.Op.HotPath()` classes `OpObservePrompt` as hot-path, and `config.Defaults()`'s
`ConnectDeadlineMs: 5` is documented as the hot-path connect budget. So a hot-path op's dial ceiling
silently went 5 ms → 250 ms, while `hookConnectDeadlineFloor`'s own doc comment claims observe.\* is
untouched and `task-6-report.md` states it explicitly. Whatever the right answer is, the code, the
comment and the report must agree — right now none of the three does.

*Fix (one line):* make the predicate say what the comment says —
`if spec.deadline > 0 && !spec.op.HotPath() && connectDeadline < hookConnectDeadlineFloor` (or add
an explicit `connectFloor bool` to `hookSpec`, set only for session-start/checkpoint/flush). If the
widening for `observe.prompt` is actually **wanted**, that needs to be a stated decision with the
B-A implication spelled out, and both the comment and the report corrected — not left implicit.

**N-3 (Minor) — `internal/cli/selftest.go:273-277`: self-test's `admin.ping` client keeps the 5 ms connect budget immediately after its own `EnsureRunning`.**
`selfTestDaemonReachable` may have just spawned a daemon; `selfTestAdminPing` then constructs a
client with no `State`, no `ConnectDeadline`, and `ProjectRoot` set — so it reads `state.bin` and
dials with 5 ms, hitting exactly the race the floor was introduced to fix. Consequence is bounded (a
spurious `admin.ping` **SevWarn**, never a non-zero exit, so no test flakes), but it is the same
condition left un-mitigated in the sibling path. Pass `ConnectDeadline: hookConnectDeadlineFloor`
here too — self-test is not on any hot-path budget.

**N-4 (Minor) — `internal/cli/fault.go:294-305`: `faultDaemonDownAddr` is the only fault seam in `fault.go` with no unit test.**
Every other injector (`faultStdin`, `maybePanicHook`, `faultInflateToolResponse`, `wrapFaultSpool`,
`wrapFaultClient`, `faultCorruptStateIfNeeded`, `faultCorruptConfigIfNeeded`) has one in
`fault_test.go`; this one, added to close a Critical-adjacent Important, does not. Two asserts:
suffix appended when the site is active, address returned unchanged when it is not.

**N-5 (Minor) — M-8 reported as addressed but is not.**
`task-6-report.md`'s "Minors left" says *"M-1 … and M-8 were addressed opportunistically"*. M-1 was.
M-8 was not: `QOMPACK_FAULT` still appears in two non-test files (`internal/cli/fault.go:42` and
`internal/daemon/spawn.go:54`), `.github/workflows/ci.yml`'s `security` job still contains no such
grep, and `fault.go`'s package doc still asserts *"the ONE non-test file this literal may appear
in"* — a claim that is factually false in the shipped tree. Either add the exemption note naming
`spawn.go` (which structurally must spell it, and which the spec itself acknowledges), or amend the
constraint. The finding is unchanged in severity; only the report's claim about it is wrong.

## Verification I re-ran independently (fix round 1)

- `go build ./...` — clean; `go build -tags noinject ./...` — clean; `go vet ./...` — clean
- `go list -deps ./cmd/qompack` → `testing` **0**, `runtime/trace` **0** (Ruling #28 confirmed)
- `go test -count=1 ./internal/cli/... ./internal/ipc/... ./internal/daemon/... ./internal/testutil/... ./tools/devtool/...` — all PASS
- `go test -count=1 ./test/guards/...` — PASS (19.5 s)
- `go test -count=1 -timeout 600s ./test/e2e/...` — PASS (79.8 s)
- `go test -count=3 -run 'TestE2ESelfTest|TestSelfTestIsTheOnlyNonZeroExit|TestE2E_AllSixHooksExitZero' ./test/e2e/` — PASS (3 consecutive runs; the previously-flaky pair is stable)
- `go test -count=1 -v -run TestE2E_AllSixHooksExitZero ./test/e2e/` — six sub-tests actually execute, PASS
- `go run ./tools/devtool lint --only=importgraph,testdeps,bindeps,sleepcheck` — all PASS
- Manual: `self-test` twice against a temp project → `checkpoints/` empty, no stray artifact
- Manual: `QOMPACK_FAULT=disk-full` → exit 0, `{}`, durable `LOUD.log` drop line
- Manual: `QOMPACK_FAULT=disk-full,daemon-down` with no logs dir → exit 0, no `logs/` created
- `git status --porcelain` — clean throughout

## Re-review verdict

Both Criticals and all nine Importants are genuinely fixed, several of them better than the review
asked for (I-4's broadened corpus, I-5's discriminating sub-tests, I-8's extra `.qompack/`
assertion). The fix round is high quality and the report is candid — the one out-of-findings issue
was self-flagged with bisection evidence.

What remains is small and mechanical: two places where the code and its own stated contract disagree
(N-1, N-2), plus three minors. Neither Important is a live failure today; both are one-to-three-line
changes, and both sit on exactly the contracts this round changed, which is why they should not ship
as-is.

| Severity | Count |
|---|---|
| Critical | 0 |
| Important | 2 (N-1, N-2) |
| Minor | 3 (N-3, N-4, N-5) |

**Verdict: Needs fixes (0 Critical, 2 Important).**

---

# Re-review (fix round 2)

Reviewed: `bdc9585c` → **`6fc27a3b`** (still one commit; subject/body byte-identical to the brief's;
`git show -s --format=%B` grepped for `co-authored` / `generated with` / `signed-off` → **none**).
Delta: 6 files, +140/−24.
Scope: N-1..N-5, no new defect, single trailer-free commit.

## Per-item status

| # | Status | Evidence |
|---|---|---|
| **N-1** | **FIXED** | `internal/ipc/client.go:90-97`'s `ClientOptions.State` doc now states the real precedence: *"A non-zero State always wins — trusted outright, never re-read from disk — even when ProjectRoot is also set … Only when State is the zero value does ProjectRoot matter for this field: ReadState(ProjectRoot, config.Defaults()), falling back further to StateFromConfig(config.Defaults()) when ProjectRoot is also empty."* That is an exact description of the shipped switch, and it cross-references `NewClientWithOptions`'s own doc, so the two no longer contradict. The footgun I named — a caller passing a cached `State` believing it will be ignored — is closed. The type-level comment above it (*"ProjectRoot empty disables both ReadState and lazy spawn"*) is untouched but remains **true** as written (it constrains the empty case only), so no further edit is needed there. |
| **N-2** | **FIXED, well** | The predicate is now `spec.deadline > 0 && !spec.op.HotPath() && connectDeadline < hookConnectDeadlineFloor`, extracted into a standalone `hookConnectDeadline(spec, st)` (`hookclient.go:147-160`) precisely so it is unit-testable without a client or a connection — the right refactor, not just a one-line patch. Both stale comments are corrected: `hookConnectDeadlineFloor`'s doc now says *"as opposed to any `ipc.Op.HotPath()` op (observe.tool, observe.prompt, observe.stop) … 'carries a fixed deadline' alone is not the test"*, and the function's own doc names the regression explicitly. Code, comment and behaviour now agree. **Pinned by a real test**: `TestHookConnectDeadline` (`hooks_test.go:249-297`) is a seven-case table covering all six hooks against `ipc.State{ConnectDeadlineMs: 5}` — I ran it verbosely and all seven sub-cases execute, including the exact N-2 case (`observe.prompt (hot path, DOES carry a fixed deadline)` → **5 ms, not widened**), plus a "State already exceeds the floor is kept" case (`flush` with 9000 ms → 9000 ms) that pins the floor as a *minimum*, not an override. A regression on the predicate would fail this test loudly. |
| **N-3** | **FIXED** | `selftest.go:277-283` passes `ConnectDeadline: hookConnectDeadlineFloor` to the `admin.ping` client, with a comment tying it to the same `EnsureRunning`-readiness race. Correct mechanically: `NewClientWithOptions` only falls back to `State.ConnectDeadlineMs` when `o.ConnectDeadline <= 0`, so the explicit 250 ms wins. self-test is on no hot-path budget, so this costs nothing. |
| **N-4** | **FIXED** | Three new tests in `fault_test.go:213-244`, matching the coverage shape every other injector in that file already had: inactive → address returned byte-identical; active → `Kind` preserved and exactly `faultDaemonDownAddrSuffix` appended (asserting append, not replacement, and that the result differs from the real path); active among other comma-separated sites → still applied. All three run and PASS. |
| **N-5** | **FIXED (with a small residual — see below)** | `fault.go`'s package doc no longer asserts the false *"the ONE non-test file this literal may appear in"*. It now says the literal is *"deliberately confined to exactly two non-test files"* and names both — `internal/cli/fault.go` (`const qompackFaultEnv`) and `internal/daemon/spawn.go` (`const qompackFaultEnv`, `buildSpawnEnv`) — with the justification for the second, and correctly notes that `internal/daemon/spawn_test.go` and `test/e2e/faultinject_test.go` are test code the constraint does not count. I re-grepped the tree: exactly those two non-test files, matching the note. The route chosen (record the exemption rather than amend the constraint) is one of the two I offered, and the doc is now factually accurate about the shipped tree. |

## New defect

**N-6 (Minor) — the `QOMPACK_FAULT` file-count contract is now stated two different ways inside one package, and the new note contains one inaccurate clause.**

Fix round 2 corrected `fault.go`'s package doc to "exactly two non-test files", but left its
build-tagged twin `internal/cli/fault_noinject.go:10-12` carrying the **old** claim verbatim:

> *"the security CI job's grep for its literal string must find it in exactly one non-test file,
> fault.go, and this file's whole point is to not need it at all"*

So `internal/cli` now contains two files stating contradictory versions of the same contract, 40
lines apart — structurally the same defect class as N-1, reintroduced by N-5's own fix.

Separately, one clause of the new note in `fault.go:12-14` describes something that does not exist:

> *"Fix round 1's fault_noinject.go … intentionally spells the same constant name without the
> literal string"*

`fault_noinject.go` declares **no** `qompackFaultEnv` constant at all — only the ten no-op fault
functions. The true and simpler statement is that it never spells the literal anywhere, in code or
in comments.

*Impact:* documentation only, no behavioural effect, and the authoritative file (`fault.go`, the one
that actually holds the constant) is now correct. Unlike N-1 there is no correctness footgun — no
caller writes code against how many files contain a string. Recorded for opportunistic cleanup, not
a blocker.

*Fix (2 lines):* in `fault_noinject.go`, change "exactly one non-test file, fault.go" to match
`fault.go`'s corrected wording; in `fault.go`, drop or reword the "spells the same constant name"
clause.

## Verification I re-ran independently (fix round 2)

- `go build ./...` and `go build -tags noinject ./...` — clean
- `go vet ./...` and `go vet -tags noinject ./...` — clean (the `noinject` twin's new
  `faultDaemonDownAddr` compiles)
- `go list -deps ./cmd/qompack` → `testing` **0** (Ruling #28 still holds)
- `go test -count=1 -v -run 'TestHookConnectDeadline|TestFaultDaemonDownAddr' ./internal/cli/` —
  all 3 + 7 sub-cases execute, PASS
- `go test -count=1 ./internal/cli/... ./internal/ipc/... ./internal/daemon/... ./internal/testutil/... ./tools/devtool/...` — all PASS
- `go test -count=1 ./test/guards/...` — PASS (19.7 s)
- `go test -count=1 -timeout 600s ./test/e2e/...` — PASS (80.7 s)
- `go test -count=2 -run 'TestE2ESelfTest|TestSelfTestIsTheOnlyNonZeroExit|TestE2E_AllSixHooksExitZero|TestE2EHookRoundTrip' ./test/e2e/` — PASS (2 consecutive runs; the N-3-touched self-test path is stable)
- `go run ./tools/devtool lint --only=importgraph,testdeps,bindeps,sleepcheck` — all PASS
- `git show -s --format=%B 6fc27a3b` — exact brief subject/body, no attribution trailers
- `git log --oneline ef50515f..6fc27a3b` — exactly one commit
- `git status --porcelain` — clean throughout

I concur with the coordinator's disposition of the one observed flake
(`TestServerCloseWithLiveConnection`, Windows): it is the already-ledgered go-winio
`Close`-racing-`Accept` wrinkle from Ruling #27's own deferred list, lives in `internal/ipc`'s
Task-2 code, and is untouched by this task. No action here.

## Re-review verdict

Both round-1 Importants are properly closed, and N-2 in particular was fixed the right way — the
predicate was extracted into a pure function so the regression could be **pinned by a test** rather
than only corrected, and that test names `observe.prompt` as the case that must not be widened. N-3
and N-4 are clean, N-5 is honest. What remains is a two-line comment inconsistency in a build-tagged
twin file, with no behavioural reach.

Across three rounds this commit has cleared 2 Criticals, 11 Importants and 15 Minors, with every
Critical fix independently reproduced by me against the real binary rather than accepted on the
report's word.

| Severity | Count |
|---|---|
| Critical | 0 |
| Important | 0 |
| Minor | 1 (N-6) |

**Verdict: Approved.** N-6 is a documentation nit — fold it into any later touch of
`internal/cli/fault*.go`, or carry it to the final review; it does not warrant another round.
