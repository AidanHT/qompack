# Closeout runtime — independent non-authoring critical review

- **Reviewer role:** independent critical review, non-authoring. No source edits, commits, delegation, or config/auth/provider/billing/permission changes were made. This report is the only file written.
- **Route probe:** `route-closeout-review-result.json` canonical model verified `claude-opus-4-8`. Replied `ROUTE_PROBE`.
- **Repo:** `qompack-v6`, branch `verify/v6`, base HEAD `36205d1` plus current working tree.
- **Date:** 2026-09-22.
- **Scope (bounded to these paths, dependencies read as needed):**
  `internal/daemon/lock_unix.go`, `internal/daemon/lock_linux.go`, `internal/daemon/lock_linux_v6_test.go`,
  `internal/testutil/procalive_unix.go`, `internal/testutil/procalive_linux.go`, `internal/testutil/procalive_linux_test.go`,
  `test/e2e/mcp_e2e_test.go`, `test/e2e/mcp_stderr_capture_test.go`, `test/e2e/race_instrumentation_test.go`,
  `.github/workflows/ci.yml`, `.github/workflows/nightly.yml`.
- **Consumers/deps inspected:** `internal/daemon/lock.go` (`AcquireLock`, `lockIsStale`), `test/e2e/v3_x10_test.go` (`x10Restart`).

## Source hashes (git blob sha1, working tree)

| file | blob sha1 | lines | state |
|---|---|---|---|
| internal/daemon/lock_unix.go | `2fa40e7016af38445194` | 25 | M |
| internal/daemon/lock_linux.go | `bf11de3b9d253e944a7a` | 37 | new |
| internal/daemon/lock_linux_v6_test.go | `169ac87f13c180d68cae` | 33 | new |
| internal/testutil/procalive_unix.go | `9807bc5145af500fd1d5` | 27 | M |
| internal/testutil/procalive_linux.go | `115da0ea577a5fc6c748` | 36 | new |
| internal/testutil/procalive_linux_test.go | `c1ffb10747294cc2adc1` | 24 | new |
| test/e2e/mcp_e2e_test.go | `881b38ac1cb6012b8372` | 528 | M |
| test/e2e/mcp_stderr_capture_test.go | `ed2d04c5456ccc420c0b` | 80 | new |
| test/e2e/race_instrumentation_test.go | `b7f4710aebce2a21e381` | 37 | new |
| .github/workflows/ci.yml | `62c33c10a92293938e99` | 451 | M |
| .github/workflows/nightly.yml | `f1f382fc4b3deb9c2f1d` | 184 | M |

## Verdict

The four coupled changes in this set — the stderr race fix, the OS build-tag split, the zombie-aware daemon lock probe, and the product-child race gate — are **individually correct** and the in-repo evidence substantiates every claim in the task brief. No must-fix code defect was found in the reviewed source. Two non-code, gate-scoping caveats (F1, F2 below) mean the `race-product-child` gate must **not** be reported green on the strength of what is committed here, exactly as instructed. This review makes **no** claim that V6 as a whole is complete: human UAT, platform, eval, and rollback gates remain out of scope and unaddressed.

## Evidence chain (inspected, not re-run)

The narrative in the brief is fully reproduced by the committed artifacts. Two immutable Linux snapshots off `base_head 36205d1` were executed in the `qompack-v6-linux-verification` container:

1. **snapshotB** — `linux-child-race-20260922-1432.json` (archive `b94f531e…`). Manifest contains `procalive_linux.go` and the OLD `lock_unix.go` (`73cb4cc9…`) but **no `lock_linux.go`**, i.e. the test helper carries the zombie fix, the daemon does not.
   - `linux-product-child-race-corrected-artifacts/test.log`: the syncBuffer fix holds — `TestStdioServerEndToEnd` PASS (4.90s), plus five other e2e green and `TestE2E_RequiredProductChildRaceInstrumentation` PASS — while `TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently` **FAILS** (75.04s): `daemon.lock still present: pid=10072 … (pid alive: false)`, `daemon.hb mtime age: 10m40s`. The tell is precise: the test helper (`e2eProcessAlive` → fixed `testutil.ProcessAlive`) already reports the zombie owner dead, yet the daemon's own unfixed `pidAlive` refuses to reclaim the lock (kill(0) on the unreaped zombie succeeds → judged alive) → no new daemon → crash-recovery fails. This is the exact defect `lock_linux.go` targets.
   - The original race is captured verbatim at `linux-product-child-race-artifacts/race.8310`: `bytes.(*Buffer).String` in `mcpE2EChild.send` (`mcp_e2e_test.go:151`) racing `bytes.(*Buffer).grow` under `io.Copy` from `os/exec`. Matches the `syncBuffer` doc comment and `mcp_stderr_capture_test.go` word for word.

2. **snapshotC** — `linux-lock-recovery-20260922-1439.json` (archive sha256 `18c821e3732efc7f27c6d32bd8decc89085dd64e82e556589f9ec507de9ebdf3`). Manifest **includes** `lock_linux.go` (`d970423c…`) and `lock_linux_v6_test.go`.
   - `linux-lock-recovery-race-artifacts/test.log` (executed source pinned by `linux-lock-recovery-race-executed-source.json`, archive sha matches): under the product-child race gate,
     - `internal/daemon`: `TestLock_V6_ExitedUnreapedOwnerCanBeReplaced` PASS **and** all five pre-existing lock invariants (`TestAcquireLockExclusive`, `TestStaleLockReclaimed`, `TestLiveLockNotReclaimed`, `TestAcquireLockRaceWindowIsNotStale`, `TestLockRefusesAfterReclaim`) PASS — no regression to the staleness protocol;
     - `test/e2e`: `TestE2E_RequiredProductChildRaceInstrumentation` PASS with buildinfo proof `sha256=6001ce74… go=go1.26.6`, and `TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently` **PASS (120.74s)**, no race reported.

The chain is internally consistent: race.8310 → `syncBuffer` → StdioServer green; zombie-lock X10 failure (no daemon fix) → `lock_linux.go` → X10 + lock invariants green.

## Correctness review

### Build-tag split — CORRECT, complete, no gap/overlap
`lock_unix.go` and `procalive_unix.go` change `//go:build !windows` → `//go:build !windows && !linux`; the new `lock_linux.go`/`procalive_linux.go` carry `//go:build linux`; `lock_windows.go`/`procalive_windows.go` carry `//go:build windows`. Every GOOS resolves to exactly one file (linux → `*_linux.go`; darwin/bsd → `*_unix.go`; windows → `*_windows.go`). No duplicate symbol, no uncovered platform.

### `pidAlive` (lock_linux.go) — CORRECT
`internal/daemon/lock_linux.go:17-37`. Order is right and matches the brief: kill(pid,0) first; **only on success** is `/proc/<pid>/stat` read. ESRCH → dead; EPERM/other errno → alive (no `/proc` read, correct for foreign-owned daemons). On kill success, state is parsed from the first field after the **last** `')'` (`LastIndexByte`, lock_linux.go:28) — the robust parse for a `comm` that may itself contain spaces/`)`; `Z`/`X` → dead, everything else → alive. Read error, no `)`, or empty fields → conservatively alive (lock_linux.go:25-35). The only window that returns conservative-true for a truly-gone process is the microscopic gap between kill-success and reap; that biases toward *not* stealing a live daemon's lock, which is the correct safety direction. IPC precedence is preserved by the caller: `lockIsStale` (`lock.go:176`) runs `ipc.Probe` (step 2) before `pidAlive` (step 3, `lock.go:181`), so a live listener still wins. No panic paths (`stat[end+1:]` is a valid empty slice when `)` is last).

### `ProcessAlive` (procalive_linux.go) — CORRECT, consistent with daemon
`internal/testutil/procalive_linux.go:17-36` mirrors `pidAlive` in bool form, same `/proc` parse, same conservative-alive defaults. The intentional duplication (testutil cannot import daemon; cf. the import-cycle constraint) is faithful. Minor, non-blocking: daemon uses `errors.Is(err, syscall.ESRCH)` while `procalive_unix.go:18` uses `err == syscall.ESRCH` — both valid; `errors.Is` is the more robust idiom. Not a defect.

### Tests — VALID, assertions are meaningful
- `lock_linux_v6_test.go:16-33`: writes a crafted lock with a *recent* heartbeat pointing at an exited-unreaped child, then asserts `AcquireLock` succeeds. This exercises step-3 (`pidAlive`) overriding step-4 (fresh heartbeat) — a genuinely discriminating assertion; the pre-fix code would fail it. `require.NoError(t, syscall.Kill(pid,0))` at line 24 confirms the child is still a live-to-POSIX zombie, so the test proves the `/proc` refinement, not merely ESRCH.
- `procalive_linux_test.go:15-24`: same discriminating shape for the helper; asserts kill(0) still succeeds for the exited child (line 23), so a POSIX-only implementation would give the wrong answer.
- Zombie persistence is guaranteed: the parent (test process) defers `Wait`, and only the parent can reap, so the child stays `Z` for the 5s `Eventually` window. No flakiness path identified.
- `race_instrumentation_test.go:16-37`: gated on `QOMPACK_REQUIRE_CHILD_RACE=1`; reads `debug/buildinfo` off the `Build(t)` executable and requires the `-race=true` build setting. This is a real, independent proof that `GOFLAGS=-race` reached the child `go build` — it fails if that inheritance is lost, as the nightly comment claims.
- `syncBuffer` (`mcp_e2e_test.go:98-116`): named field `buf bytes.Buffer` (not embedded) → no promoted method can reach the buffer unlocked; only `Write` (io.Writer for `cmd.Stderr`) and `String` are exposed, both mutex-guarded. `finish` reads `String()` only after `cmd.Wait()` (`mcp_e2e_test.go:251`), by which point os/exec's copy goroutine has joined — no residual race. `mcp_stderr_capture_test.go` reproduces the exact writer/reader shape in isolation and asserts no data loss; a focused, Docker-free `-race` witness. `bytes` and `sync` imports remain used.

### Workflows — WELL-FORMED
- `ci.yml`: `test-e2e` runs `go test -count=1 -timeout=30m ./test/e2e` (line 247), no `-race`; the rewritten comment now correctly points at nightly's `race-product-child` job. Consistent.
- `nightly.yml` `race-product-child`: `CGO_ENABLED=1`, `GOFLAGS=-race`, `GORACE="halt_on_error=1 log_path=…/race-artifacts/race"`, then a `find race-artifacts -name 'race.*' -type f -size +0c` gate that forces `rc=1` even if `go test` somehow exits 0 — sound defense-in-depth. `test.log` is not matched by `race.*`, so it cannot self-trip the gate. **All eight `-run` test names were verified to exist exactly once** in `test/e2e` (important given `go test -run` silently passes when a pattern matches nothing): `TestE2E_RequiredProductChildRaceInstrumentation`, `TestE2EHookRoundTrip`, `TestE2E_ObserverThroughDaemon`, `TestE2E_SupersessionVisibleAfterRestart`, `TestE2E_VerbatimPromptSurvivesRestart`, `TestE2E_SessionStartCompactRestoresCheckpointItems`, `TestStdioServerEndToEnd`, `TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently`. `QOMPACK_UNDER_COLOAD=1` on this job is consistent with the established co-load wall-clock exemption policy.

## Findings

### F1 — MEDIUM (gate-scoping, not a code defect): the `race-product-child` gate is proven only in local Docker/WSL, never on the hosted runner it targets
`linux-lock-recovery-race-executed-source.json` states plainly: *"Docker/WSL Linux with no installed Claude host; not an installed-host or release-package certification."* The green snapshotC evidence is a local container run, not an observed `ubuntu-latest` execution of the new `nightly.yml` job. Hosted runners are known to carry an fsync latency tail and co-load timing sensitivity (prior V5/V6 close-out record). X10 at 120.74s against a 15m budget has ample headroom, so the risk is low, but the job's *first hosted execution remains unproven*. **Do not report the gate green until a hosted nightly run is inspected.** Evidence: `linux-lock-recovery-race-executed-source.json` `limit` field; `nightly.yml` `race-product-child`.

### F2 — LOW (evidence completeness): no single run exercised all eight selected tests against the final source together
The six non-X10 e2e tests are green in snapshotB (which lacks `lock_linux.go`); X10 + instrumentation are green in snapshotC (which has it). No committed artifact runs the full eight-test `-run` set against the snapshotC source in one pass. The six non-X10 tests do not touch the daemon stale-lock reclaim path, so the split coverage is sound in principle, but the combined hosted run in F1 is what would close this too. Evidence: `linux-product-child-race-corrected-artifacts/test.log` vs `linux-lock-recovery-race-artifacts/test.log`.

### F3 — INFO (nit, no action required): ESRCH comparison idiom differs across the two probes
`lock_linux.go`/`procalive_linux.go` use `errors.Is(err, syscall.ESRCH)`; `procalive_unix.go:18` and `lock_unix.go:18` use `err == syscall.ESRCH` (with an errorlint suppression). Both are correct for a raw `syscall.Errno`; noting only for consistency.

## Default-off rollover release scope (bounded to reviewed paths)

None of the reviewed source paths contain delivery-rollover logic. Bounded to this change set, the default-off rollover seam is **untouched** and this set introduces **no rollover blocker** and no change to rollover enablement. Rollover default-off status and its enablement prerequisites are owned by other records (e.g. `delivery-capacity-integration-work.md`, `delivery-rollover-*`), which are out of scope here.

## Must-fix blockers introduced by these changes

**None in the reviewed source.** The stderr race, build-tag split, zombie-aware `pidAlive`, its tests, and the race gate are correct and evidenced. The only closure conditions are the non-code caveats F1 (hosted-runner confirmation of the new gate) and F2 (combined-set run), which gate *declaring the race-product-child gate green*, not merging the source.

## Explicitly NOT asserted

V6 is **not** declared complete. Human UAT, platform certification, eval, and rollback gates are outside this review and unverified here. The model/cost material referenced elsewhere is a rate list, not an invoice. This review ran no test commands; all findings derive from source inspection and the committed immutable-snapshot artifacts.
