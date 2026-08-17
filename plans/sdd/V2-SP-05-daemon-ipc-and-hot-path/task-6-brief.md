# Task 6 — Commit 6: thin hook clients, daemon/self-test subcommands, fault injection, e2e suite

You are completing `internal/cli`, `cmd/qompack`, and `test/e2e` in `github.com/qompack/qompack` (worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`, branch `feat/sp05-daemon-ipc-and-hot-path`). Already landed: full internal/ipc (client/spool/server/state record), full internal/contract, full internal/daemon (lock/spawn/registry/ingest/drain/handlers/lifecycle). **Read the actual code of those packages first — especially ipc.ClientOptions, ipc.ReadState, daemon.EnsureRunning/SpawnDetached, the daemon's op routes, and the shipped cli framework (dispatch.go, hooks.go, recover.go, commands.go, config.go) — and build against what they ship.**

Read IN ORDER:
1. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/sp01-inventory.md` — SP-01 inventory.
2. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/plan-context.md` — mission, out-of-scope, global rules.
3. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-6-spec.md` — verbatim requirements: the cli implementation spec (hook skeleton, helper functions, per-subcommand wiring table, daemon.go, selftest.go, fault.go with the 11-site table), the e2e test table, and the commit-6 checklist.
4. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/progress.md` — "Pre-flight conflict scan" rulings (esp. #8: SP-05 rewrites hook bodies INSIDE the shipped Cmd/Env framework).
5. Existing `test/e2e/` scaffolding and `internal/testutil` (Project helper, FakeClock), plus `test/guards/network_test.go` + `writeset_test.go` so you don't trip them.

## Binding rulings (controller decisions amending the plan text)

- **Keep the shipped cli framework.** Hooks stay `Cmd{Hook:true}` entries dispatched via `Dispatch`; the always-exit-0 guarantee and panic recovery already live in dispatch.go/recover.go (runGuarded) — do not duplicate them; the plan's `recoverToZero` maps onto that existing mechanism plus a small write-empty-output-if-nothing-written guard inside the hook body. Environment access goes through `cli.Env` (env.Getenv, env.Stdin, env.Clock, env.Set) — never os.Getenv/os.Stdin directly in hook bodies (fault.go and selfPath may use os.* where unavoidable; keep it minimal and injectable).
- **The new hook body (hookclient.go)** follows the plan's skeleton adapted to the framework: (1) stamp TS from env.Clock as the FIRST statement; (2) resolve root cheaply pre-stdin (QOMPACK_PROJECT_ROOT via env.Getenv, else process cwd — reuse `paths.Resolve(env.Getenv, "")` if it fits, else the plan's order); (3) `ipc.ReadState(root, config.Defaults())` — never config.Load on the hot path; (4) ModeOff → write hookio.Empty(), done; (5) ReadEvent with limit `int64(st.MaxPayloadBytes)*4`; (6) re-resolve root from payload (paths.Resolve(env.Getenv, ev.CWD)) and re-read state if it moved; (7) build ipc.Request (Raw from subcommand flags only: {"subagent":true} for `observe stop --subagent`, {"trigger":"<manual|auto>"} for checkpoint — read the trigger from the event/flags as shipped hookio exposes it); (8) spool at `<root>/.qompack/spool` via ipc.NewSpool; (9) client via NewClientWithOptions with State, Self=os.Executable() (helper selfPath, "" on error), Spawn=daemon.SpawnDetached, Clock=env.Clock; (10) Send with the per-subcommand deadline; (11) write resp.Output or hookio.Empty(); return nil. ErrAddrTooLong → spool + empty output + exit 0.
- **Deadline table** (from the plan): observe tool / observe stop → AckDeadline (from state); observe prompt → 250ms prompt-reply deadline (if the daemon exports its promptReplyDeadline const, use it; otherwise declare a cli-local named constant with a comment naming the daemon's constant — keep the two visibly tied); session-start → 10s; checkpoint / flush → 15s. All named constants with §-comments.
- **session-start** additionally calls `daemon.EnsureRunning(root, selfPath(), log, clk)` BEFORE Send; failure to come up → still Send (spools), empty output, exit 0.
- **The old no-op observation logging** (hooks.go runHook/observe writing hooks-YYYYMMDD.jsonl) is REPLACED by the new transport path. Whether you keep a slim observation line is your call ONLY if it costs nothing on the hot path — default: drop it; the WAL is the record now. Update/replace hooks_test.go accordingly (SP-05 replaces the six hook bodies; their old tests test the old bodies).
- **logQuiet**: appends to `.qompack/logs` only if the directory already exists; never creates directories on a failing project's error path.
- **cli/daemon.go**: `qompack daemon [--project <root>] [--foreground]` non-hook Cmd: resolve root (flag > env > cwd), load config via the shipped LoadConfigAndReport, build logger against layout Logs dir, daemon.NewOptions → daemon.New → signal.Notify(os.Interrupt, SIGTERM) cancelling the run context → Run. Exit 0 on clean stop AND on ErrLockHeld AND (with a Loud line) on any other error — a daemon spawned lazily must never surface non-zero. (Dispatch returns ExitError for non-hook errors — so daemon.go's Run wrapper must swallow errors into a logged nil return.)
- **cli/selftest.go**: `qompack self-test [--json]` per the spec's check list (config load; .qompack writability; paths guard behavior; ipc.Resolve; daemon reachable-or-spawnable; admin.ping round trip; registered-ops coverage vs ipc.KnownOps() via the status/admin.ping payload or a daemon Options accessor — use what the daemon actually exposes; contract.StandardAssertions against a synthetic Env built from the persisted SessionHistory). Exit 0 iff no SevCritical failure; exit 1 otherwise (ExitError via returning an error). Fixed-width table output, or JSON under --json with {checks:[...], mode, exit}.
- **cli/fault.go**: QOMPACK_FAULT comma-separated `site[:arg]` parsing; `faultActive(site) (arg string, on bool)`; nil-map fast path when unset. Two build variants: default and `-tags noinject` (noinject compiles faultActive to always-false). The exact 11 sites and their expected behaviors are in the spec's table — implement every one at its named location (some sites live in ipc/daemon call paths reachable from cli: inject at the cli layer by wrapping — e.g. `spool-full`/`disk-full` wrap the SpoolWriter handed to the client; `state-corrupt`/`config-corrupt` corrupt the files before proceeding; `panic:client` wraps the client; `daemon-down` overrides the resolve result + no-op Spawn). Keep the QOMPACK_FAULT string literal in exactly ONE non-test file: internal/cli/fault.go (the security CI job greps for this; daemon's SpawnDetached already strips it from child env — verify, don't duplicate).
- **cmd/qompack/main.go**: add `daemon` and `self-test` dispatch entries following the existing pattern (`version` already ships). Check how commands are assembled (cli.All or similar) and extend there.
- **test/e2e**: implement every row of the spec's e2e table — TestE2EHookRoundTrip, TestE2ELazySpawn, TestE2EIdleExit, TestE2ESelfTest{ExitsZeroOnHealthy,ExitsNonZeroOnCritical}, TestE2ESpoolSubmodeEndToEnd, TestHooksExitZeroUnderFaults (6 hooks × 11 sites = 66 combos, every one exits 0 with valid JSON on stdout), TestFaultSitesInertWhenUnset (incl. byte-identical stdout vs a `-tags noinject` build), TestSelfTestIsTheOnlyNonZeroExit. Follow the existing test/e2e scaffolding conventions (binary build helper, testutil.Project). Use QOMPACK_IPC_ADDR to keep endpoints test-local. Windows host notes: `spool-readonly` uses FILE_ATTRIBUTE_READONLY + a deny-write ACE on Windows (icacls or golang.org/x/sys? — x/sys is indirect-only; use `icacls` via os/exec INSIDE THE TEST only, or attrib; tests may exec) and chmod 0o500 on POSIX (build-tagged helpers); mark POSIX-only rows with build tags so CI covers them.
- e2e tests that spawn real processes may take seconds; keep the daemon idle-exit fast via --set flags (`--set runtime.daemon.idleExitSeconds=1` style, or config file in the temp project — the shipped --set flag layer is the cleanest).
- Import discipline: cli is a composition root (may import anything); nothing imports cli. os/exec allowed in cli/daemon/tools + tests.

## Process

- TDD: e2e table tests and cli unit tests first where feasible (RED), then implement. The 66-combo fault table will genuinely drive fault.go's shape — write it early.
- Before committing: `go test ./internal/cli/... ./test/e2e/... -race` (e2e may be slow; run once), `go test ./test/guards/...`, GOOS=linux+darwin builds, devtool fmt/lint/vet, full `go test ./...` once. Also run each hook subcommand by hand with empty stdin and confirm $LASTEXITCODE is 0.
- Exactly ONE commit, exact message below, no attribution trailers:

```
feat(cli): session-start dispatch, thin hook clients, and self-test

Hook subcommands become thin clients that stamp the B-A origin as their first
statement and exit 0 on every path; session-start is the designated daemon starter
and runs the contract monitor before any other work, per the split-ownership table.
self-test is the only subcommand permitted a non-zero exit.

Refs: SP-05, §7.3, §12, 00-ARCHITECTURE §2.3, §5.21
```

## Report

Full report → `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-6-report.md` (TDD evidence, the 66-row pass table result, files changed, self-review, concerns). Reply with ONLY: Status, commit SHA+subject, one-line test summary, concerns, report path.
