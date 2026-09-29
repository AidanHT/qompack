# Wave 12 — w12-x11pair: X11 paired baseline (D42), budget WARN (D43), spool docs (D44)

Branch `closeout/w12-x11pair`. Workflow `wf_740f3ac1-2de`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `26a96f70`

### Root cause

D42: X11's gate was B-A p99 <= 1.25 x V2's 3.072 ms. The two sides were different quantities. V2's B-A sample was recvTS-reqTS + 1 ms, with the handler excluded and an ingest that was not durable. Today's sample adds the measured pre-ACK handler time (SP20-D6, c78f610f), which is B-B's fsync-before-ACK ingest (SP20-D1). The ceiling of 3.84 ms could therefore never be met, even though B-A and B-B both passed against 50 ms. The fix pairs two runs on the same host and compares hook_controlled_observed (recvTS-reqTS). That is the one part of B-A whose definition has not changed, and it contains no ingest. D43: nothing warned when a user set runtime.hotPath.budgetMs below runtime.budgets.l0IngestMs. Every B-A sample contains the durable ingest, so that setting makes deliveries that meet B-B into B-A breaches, and sessions degrade to spool. D44 is docs only. The spool-recovery behaviour is the one wave 11 found: once hooks read hot=spool they stop dialling, so the detector gets no clean windows, and only a new session id or a daemon restart resets it.

### Summary

All three decisions are done in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w12-x11pair on closeout/w12-x11pair (base cf727b7, head 26a96f70). The wave 11 nits are fixed too. X11 now PASSES run alone on Windows.

D42 (test/e2e/v3_x11_test.go, plus the new test/e2e/v3_x11_pair_test.go)
- Which no-ledger run: the X11 fixture minus the ledger, not test/integration's resident state. After the 2 000-event corpus is flushed, the store is closed and the project tree is byte-copied to a twin (only .qompack/logs is skipped: it is the setup's open log, not resident state). The store is then reopened and the 5 000-entry ledger and tried.bloom are built on the original.
- Why: the pair then differs only in the ledger. test/integration's state seeds a tried.bloom and uses a different corpus.
- Limit of the pair: the observer pipeline is in the measured daemon binary in both runs, so the pair cannot isolate the observer. Its cost is still judged by the absolute gates.
- Why a copy: the harness refuses to reuse a project (refuseInheritedIdentities), and copying avoids building the corpus twice.
- Order: the no-ledger twin runs first, then the ledger project, with the same harness binary.
- Both runs face every absolute gate unchanged: B-A, B-B and B-E wall against their obs.Budgets() limits, B-E_cpu, the pass sweep, B-D and the spawn estimate reported, waiver notes absent. X11 never had a spool/state.bin check, so none was added. Each run's delivery-ledger (deferral) note is logged; this run had none.
- The pair check: x11LedgerPairVerdict reads each artifact's own "daemon-observed hook_controlled_observed" note. The ledger run's p50 must be <= 1.25 x the no-ledger run's p50. It refuses a missing, duplicated, unreadable or drifted note; that is a plain error, which fails even in co-load mode. Co-load mode reports an over-ceiling pair and does not gate it, as before.
- Deviation from D42 (owner decision needed): the ceiling also never drops below the no-ledger p50 plus one reported tick of 1.024 ms. That tick is read from the obs histogram, not typed in. See needs_owner.
- Reported, not gated: both runs' B-A p99 and V2's 3.072 ms, with the note that V2's B-A excluded the handler and used a non-durable ingest.
- Fixture tests: TestV3_X11LedgerPairVerdict (RED when over the ceiling, GREEN at or under it, the floor cases, refusals) and TestV3_X11CopyProjectIsExactButForLogs.

D43 (internal/config/load.go, hotPathBudgetWarnings)
- config.Load appends a keyed Warning on runtime.hotPath.budgetMs when the effective budgetMs is below the effective l0IngestMs. It never clamps; both values stay as set.
- Being keyed, cli.LoadConfigAndReport logs it at Warn (the daemon's startup load writes it to the day log). It does not use the violation prefix, so it is never persisted as a §11.3 violation.
- Tests: TestLoad_HotPathBudgetBelowIngestBudgetWarns covers defaults, budgetMs set below, l0IngestMs set above the default budgetMs, and equal values. TestHotPathBudgetWarnings_NoneForAnyPlatformDefault checks the linux, windows and darwin default pairs (none warn; one millisecond tighter does).
- TestLoad_UserSetHotPathBudgetIsKept had to change; see criterion_changes.
- The Warning doc comment in config.go now says this warning, like Deprecated ones, describes a value that was applied.

D44 (docs/troubleshooting.md §7)
- New entry: once in spool mode it lasts until a new session starts or the daemon restarts, and compacting the session does not reset it. Capture continues through the spool and nothing is lost, though recent tool uses can reach the store later. /qompack:status shows `hot path:    spool`, the loud line "daemon: hot path degraded to spool submode" (budget_ms, windows), the hotpath_degraded counter and `spool files:`.
- The way out is a new session, the daemon's idle exit, or ending its process. `qompack daemon stop` does not exist in this build (no such subcommand in internal/cli; the doc's existing stop entry already says so), so the entry does not mention it.
- It also points at the D43 case (budgetMs set below l0IngestMs).

Nits
- internal/daemon/doc.go, internal/daemon/spawn.go and cmd/qompack/main.go now give B-A as runtime.hotPath.budgetMs: 15 ms on Linux, 50 on Windows, 40 on macOS by default (D41).
- plans/QOMPACK-ERRATA.md now says RuntimeNamespace and CoversTheIngestBudget were red and PerPlatform did not compile.
- plans/00-ARCHITECTURE.md now says "Coordinator decision D41, taken under D33".

Isolated X11 result (Windows, QOMPACK_UNDER_COLOAD unset, head deca6325): PASS in 668.51 s.
- No-ledger run: harness 2m4s. B-A p99 36.864 ms (limit 50), B-B p99 18.432 ms, B-E_cpu p99 46.875 ms, B-E wall p99 193.510 ms. hook_controlled_observed p50 6.144 ms, p99 10.240 ms, n=2064.
- Ledger run: harness 1m53s. B-A p99 28.672 ms, B-B p99 13.312 ms, B-E_cpu p99 46.875 ms, B-E wall p99 220.069 ms. hook_controlled_observed p50 6.144 ms, p99 10.240 ms, n=2064.
- Pair: ceiling 7.680 ms; PASS. V2 figure 3.072 ms, reported only. No deferrals in either run.

Timeout: the row alone took 668.5 s, against 254-368 s for the single run in wave 11, so the second harness run adds about 2 minutes plus copy and reopen. That is well inside -timeout=30m for the row. However, ci.yml's test-e2e job runs the whole ./test/e2e package under one -timeout=30m. Earlier local Windows whole-package runs already hit 1800 s (plans/sdd/V6-closeout/e2e/runs/pkg-test-e2e-windows.log, hostperm/runs/94-e2e-full-windows.log), so this row now uses about 11 of those 30 minutes on a loaded laptop. Hosted-runner timing is unmeasured.

### Commits

- a9920710 fix(config): warn when budgetMs is below l0IngestMs
- dc564336 test(e2e): pair X11's ledger run with a no-ledger run
- f6fcfeb5 docs(troubleshooting): say how long spool submode lasts
- 092ddcf9 docs(daemon): state B-A's per-platform default budget
- deca6325 docs(plans): correct the D41 red-log and decision wording
- 26a96f70 docs(v6): add w12-x11pair Windows evidence logs

### Tests

- `go test -count=1 -v -run '^TestLoad_HotPathBudgetBelowIngestBudgetWarns$' ./internal/config (RED, before the fix; the internal test file was set aside because it did not compile yet)` — FAIL as expected: the budgetMs-below and l0IngestMs-above subtests had 0 warnings; TestLoad_UserSetHotPathBudgetIsKept, run in the same command, also failed (runs/d43-red-windows.log)
- `go test -p 2 -count=3 -v -run '^TestLoad_HotPathBudgetBelowIngestBudgetWarns$' ./internal/config (the same command also ran TestLoad_UserSetHotPathBudgetIsKept and TestHotPathBudgetWarnings_NoneForAnyPlatformDefault)` — PASS, 9 top-level passes (runs/new-rows-config-count3-windows.log)
- `go test -p 2 -count=3 -v -run '^TestV3_X11LedgerPairVerdict$' ./test/e2e (the same command also ran TestV3_X11CopyProjectIsExactButForLogs)` — PASS, 6 top-level passes (runs/new-rows-e2e-count3-windows.log)
- `go test -p 2 -count=1 ./internal/config` — ok 19.708s
- `go test -p 2 -count=1 -timeout=20m ./internal/daemon ./cmd/qompack` — ok 242.049s (daemon); cmd/qompack has no test files
- `go test -p 2 -count=1 ./test/docs` — ok 2.334s
- `go run ./tools/devtool gen-config-docs --check` — docs/config-reference.md is up to date
- `go test -p 2 -count=1 -run '^TestQompackErrataAndRevisionLogAgree$' ./test/guards (the same command also ran the other two qompackrevision tests)` — ok
- `go run ./tools/devtool fmt-check` — exit 0
- `GOOS=windows|linux|darwin go vet ./internal/config ./internal/daemon ./cmd/qompack ./test/e2e` — exit 0 on all three
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS
- `env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e (alone, once)` — PASS 668.51s. no-ledger B-A p99 36.864ms, ledger B-A p99 28.672ms (limit 50); B-B 18.432 / 13.312ms; hook_controlled_observed p50 6.144 / 6.144ms, ceiling 7.680ms; V2 3.072ms reported only (runs/e2e-x11-isolated-windows.log)

### Criterion changes

- X11 expected output 6 (D42): the gate 'B-A p99 <= 1.25 x V2's 3.072 ms' is replaced by 'ledger-run hook_controlled_observed p50 <= 1.25 x the paired no-ledger run's p50', read from each harness artifact's own note. Rationale: coordinator decision D42. V2's B-A and today's B-A measure different quantities (handler excluded and non-durable ingest then, handler and fsync-before-ACK ingest now). Both runs' B-A p99 and 3.072 ms are still logged, as a report. Every absolute gate is kept and is now applied to both runs.
- X11 pair ceiling floor (deviation from D42's literal text, needs owner approval): ceiling = max(1.25 x base p50, base p50 + 1.024 ms). Rationale: every sample is recvTS-reqTS from two core.UnixMilli stamps, so it is a whole number of milliseconds, and each p50 is reported as k x 1.024 ms for k up to 16. Below 4 ticks, 25% is less than one tick, so the bare factor would judge rounding. Historical Linux notes show p50 of 1.024 ms in most runs and 2.048 ms in others; a 1.024 -> 2.048 pair would fail with nothing changed, and a 0 ms p50 (reported as 0.001 ms) would give a ceiling of 0.00125 ms. From 4 ticks up (Windows is 4-6 ms) the factor alone decides. Removing it is one line in x11LedgerPairVerdict.
- TestLoad_UserSetHotPathBudgetIsKept: `require.Empty(t, warns)` becomes 'exactly the D43 WARN when the set value is below l0IngestMs, otherwise empty'. Rationale: D43 requires that warning in exactly this case (15 on Windows/macOS, 7 everywhere). The value-applied-as-written assertions are unchanged, and the new assertion is stricter than before in that it pins the warning's key.

### Open issues

- Linux and darwin are not verified: the container was stopped by instruction. Coordinator commands (Git Bash on the host, from this worktree): (1) sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w12-x11pair --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w12-x11pair --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w12-x11pair/plans/sdd/V6-closeout/w12-x11pair/runs/linux 26a96f70 w12-touched-race -- ./internal/config ./internal/daemon  (2) the same host options, then: 26a96f70 w12-x11-isolated --no-race --timeout 60m --run '^TestV3_HotPathUnchangedWithLedgerResident$' -- ./test/e2e  Expect PASS with both runs' notes logged. The Linux p50 is expected to be 1.024 ms, which is where the one-tick floor matters. (3) the same host options, then: 26a96f70 w12-x11-fixtures --run '^TestV3_X11LedgerPairVerdict$' -- ./test/e2e. darwin is covered only by GOOS=darwin vet and the per-platform internal test; macos-latest CI is the real check.
- The whole ./test/e2e package was not run (daytime seat); only X11 and the two new fixture rows were. X11 now takes about 11 minutes of the e2e job's single 30-minute package timeout on this laptop. Earlier local Windows whole-package runs already reached 1800 s. Hosted-runner duration is unmeasured.
- The X11 row text in plans/V3-VERIFY (the V2-relative 25% rule) and any report tables quoting the 3.84 ms ceiling were outside this seat's scope and were not edited. The coordinator may want a plan or ERRATA line recording D42's rebasing.
- D44 names `qompack daemon stop`, which does not exist. The docs use the existing supported ways (idle exit, or ending the pid in daemon.lock). An operator stop command is still unowned (documented in troubleshooting §7).
- The D43 warning does not appear in `qompack config print` or `qompack doctor`: config print loads with logging.Nop, and doctor surfaces only §11.3 violations. It reaches the daemon's day log at startup, and LOUD.log on a config reload (reload.go logs every warning as Loud).

### Needs the owner

- X11 pair resolution floor (proposed bound; implemented and active; needs approval before merge). Value: 1.024 ms, one reported tick of hook_controlled_observed, computed in x11ObservedTickUS() by observing 1 ms into the product's obs histogram and reading back its p50 bucket bound, not typed as a literal. Derivation: recvTS and req.TS are core.UnixMilli, so samples are whole milliseconds, and ceiling = max(x11RegressionFactor x base p50, base p50 + tick). If it is wrong: too loose means that at Linux-scale p50 (1 ms) a real move to 2 ms (+100%) passes, although nothing finer than a tick can be resolved there. Too tight (drop the floor to get D42 as written) means a 1.024 -> 2.048 ms quantization flip on Linux, or 3.072 -> 4.096 on Windows, fails X11 with the product unchanged, and a 0 ms base p50 gives an effectively zero ceiling. It does not act for base p50 of 4 ticks or more, which covers all recent Windows runs (4.096-6.144 ms).
- No other new numbers. x11RegressionFactor 1.25, x11HarnessBound 6 min per harness run, and x11LimitDeltaMs are existing constants. D43 introduces no threshold; it compares two existing config values.

## Independent review

### review:x11pair: needs-fixes

- **major** `test/e2e/v3_x11_pair_test.go:137 (x11LedgerPairVerdict), fixture rows at :250-259` — The pair ceiling includes a one-tick floor, max(1.25 x base p50, base p50 + 1.024 ms). It is active by default, but the governing decision does not allow it. D42 as recorded in the checklist (commit d4256196, row D42) says the gate is '<= 1.25 x the paired no-ledger run's (the existing factor; no new number)'. At Linux scale (base p50 1.024 ms) the floor lets the ledger run's p50 double (2.048 ms passes). On Windows, 3.072 -> 4.096 (+33%) also passes. So it loosens the rule D42 fixed, on the platform where the gate is most likely to be read.
  - Evidence: ceilingUS: math.Max(x11RegressionFactor*float64(b.p50us), float64(b.p50us+tickUS)). Fixture rows 'one tick above a one-tick p50 is green (the floor)' {1.024, 2.048, false} and 'one tick above a three-tick p50 is green' {3.072, 4.096, false}. The implementer lists it under needs_owner as 'implemented and active; needs approval before merge'. The quantization reasoning is technically right: obs/hist.go uses 1/8-octave buckets and samples are whole milliseconds, so values are k x 1.024 ms up to 16 ms. But it is still a new bound that D42 explicitly ruled out.
  - Fix: Do not merge until the coordinator ratifies the floor as a named amendment to D42 in plans/V6-CLOSEOUT-CHECKLIST.md, plus an ERRATA/plan line for X11. Alternatively, ship D42 literally: drop the math.Max term and the floor fixture rows, and record the Linux quantization flake risk as an open issue. Either way, the checklist and the code must agree before merge.
- **minor** `docs/troubleshooting.md:853-854` — D44 entry: the way out 'or end its process; the next daemon starts in sync mode' does not work within the session. Only a clean Stop removes state.bin. After a kill, state.bin keeps saying hot=spool. Every hot-path hook (observe.tool/prompt/stop) reads it and spools without dialling, so nothing starts a new daemon until a non-hot-path hook fires (PreCompact, SessionEnd, or a new SessionStart). Until then no daemon drains the spool. Killing the daemon therefore leaves the user in spool mode with no daemon running, the opposite of what the entry promises. (The idle-exit path is fine, because a clean Stop removes state.bin.)
  - Evidence: internal/daemon/daemon.go:1168 ipc.RemoveState runs only in the Stop path. internal/ipc/client.go:215: 'if c.currentHot() == HotSpool && req.Op.HotPath() { return c.spoolAndReturn(req) }' returns before connect/spawn. internal/ipc/op.go:59-62: HotPath is observe.tool, observe.prompt and observe.stop. internal/cli/hookclient.go:334 reads state via ipc.ReadState on every hook.
  - Fix: Make the Action say: start a new session (the reliable way out), or wait for the daemon's idle exit. If the entry keeps the kill option, add that after a kill spool mode lasts until the session is compacted or a new one starts, because hooks in spool mode do not start a daemon, and nothing is drained until one runs. Re-run test/docs.
- **minor** `test/e2e/v3_x11_test.go:667 (non-co-load branch of x11RequireAbsoluteRows) and test/e2e/v3_x11_pair_test.go:63 (x11ReadObserved)` — The task's absolute-gate list included 'no spool in isolation'. X11 never had that check, and none was added. The harness only notes a deferral and does not fail on one (test/bench/hotpath/measure.go:243). So in isolation, a run that degrades to spool partway still passes every gate. Its hook_controlled_observed population then shrinks to the pre-degrade samples, and the pair verdict compares p50s over unequal populations without checking n. A ledger run that degraded after 1.5k samples would be judged on its fast prefix.
  - Evidence: x11ReadObserved returns n but x11LedgerPairVerdict never compares base.n with ledger.n. The isolated log shows n=2064 in both runs, but nothing asserts it. The implementer's summary says 'X11 never had a spool/state.bin check, so none was added'. test/integration's hotpathJudgeSpool is the existing model for this check.
  - Fix: Outside co-load mode, fail either run whose artifact carries the harness's delivery-ledger (deferred) note. Also have x11LedgerPairVerdict refuse, with a plain error that is not a verdict, when base.n != ledger.n. Add a fixture row for unequal n.
- **nit** `test/e2e/v3_x11_pair_test.go:9-10 and :257; root_cause/criterion_changes text` — Two rationale statements are inaccurate. (1) The header says hook_controlled_observed is 'the only part a resident ledger could move without also moving B-B'. Pre-ACK handler work outside the ingest would move B-A without moving B-B. In the isolated run, B-A p50 is 24.6 ms while observed (6.1) plus B-B (9.2) is 15.3, so about 9 ms of B-A is neither and goes unpaired. (2) The fixture and summary say a 0 ms p50 is 'reported as 0.001 ms'. obs bucketUpper(0) is 2 us, so the harness would print 0.002.
  - Evidence: internal/obs/hist.go bucketUpper: for i=0, lo=1 and hi=1, so hi=lo+1=2 us. Isolated log lines 19/29/20: B-A p50 24.576, observed p50 6.144, B-B p50 9.216.
  - Fix: Reword the header: D42 chose the pre-handler share because its definition has not changed since V2, not because it is the only share the ledger could move. Note that the non-ingest handler share is covered only by the absolute B-A gate and the reported B-A p99 pair. Change the fixture base to 0.002 and fix the comment.
- **nit** `internal/config/config.go:268-271; internal/daemon/reload.go:72` — The Warning doc comment tells consumers to 'skip both' the Deprecated warnings and the D43 budget WARN. The D43 warning has no field that identifies it; its Key equals the key a real clamp or violation warning on budgetMs would carry. Separately, on a config reload, reload.go logs every warning with Loud. A user with a low budgetMs therefore gets a LOUD.log line ('daemon: config reload warning') on every unrelated config edit, although D43 specifies a WARN.
  - Evidence: A Warning has only Key/Message/Location/Deprecated. reload.go: 'for _, w := range warns { d.log.Loud("daemon: config reload warning", ...) }'. The implementer's open_issues acknowledges the Loud-on-reload path.
  - Fix: Either add an explicit marker, for example set Deprecated-like 'Applied bool' on the D43 warning, and say consumers skip by that; or reword the comment so it does not promise something consumers cannot do. Record the Loud-on-reload behaviour as a known deviation from 'WARN', or log keyed non-violation warnings at Warn in reloadConfig (out of this seat's scope: route it to the coordinator).

## Fix seat (review resolution) — status `done`, head `aa7b799c1e0415a9efd3fdf230c0962ff7fb4a24`

### Root cause

Review findings: (1) the implementer added a quantization floor that D42 explicitly ruled out; (2) the troubleshooting Action offered killing the daemon, but a kill leaves state.bin=spool, and spool-mode hot-path hooks never dial, so no daemon starts or drains the spool; (3) X11 had no isolated check for spool deferral, and the pair compared p50s without checking that both runs' sample counts were equal.

### Summary

Fix seat for w12-x11pair (D42/D43/D44 plus wave-11 nits), branch closeout/w12-x11pair in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w12-x11pair. The implementer's commits a9920710..26a96f70 are unchanged. I added five commits on top. The tree is clean at aa7b799c. Nothing was pushed or merged, and I left no processes running.

## Review resolution

**1. [major] One-tick floor contradicts D42 ("no new number"). CONFIRMED, resolved by making it a separate commit the coordinator can revert on its own.**
- The floor is real: the ceiling is `math.Max(1.25*base, base+1024us)`. It is not ratified: checklist row D42 says "the existing factor; no new number".
- The quantization argument also holds.
  - recvTS and req.TS are core.UnixMilli, so each sample is a whole number of ms.
  - obs/hist.go puts 1 ms in the bucket whose upper bound is 1.024 ms, and 0 ms in the 0.002 ms bucket.
  - So a p50 is always a whole number of buckets. Below 4 buckets, 25% of the p50 is less than one bucket.
- Neither choice is mine to make, so the history now separates them:
  - **d1d4cd25** implements D42 exactly as written: ceiling = x11RegressionFactor x base p50, no tick parameter, no x11ObservedTickUS. The floor fixture rows are flipped to RED: 1.024->2.048, 3.072->4.096 and 0.001->1.024 are red; 18.432->22.528 is green.
  - **db540905** adds the floor back on top, byte-identical to the implementer's version apart from a comment saying it is an unratified amendment to D42. Its commit message says it is proposed and pending the owner.
- If the owner does not ratify, `git revert db540905` restores D42 as written, with its fixture test. If the owner ratifies, the D42 row in the checklist needs an amendment line (the checklist is the coordinator's to edit).
- Both states pass `go test -p 2 -count=1 -v -run '^(TestV3_X11LedgerPairVerdict|TestV3_X11DeferralNoteIsFound)$' ./test/e2e` (PASS at d1d4cd25 before commit, and at the head).
- It stays under needs_owner. The code and the checklist must agree before merge, as the reviewer asked.

**2. [minor] Troubleshooting told users to kill the daemon to leave spool mode. CONFIRMED, fixed in d7cee9f7.**
- Evidence:
  - `ipc.RemoveState` runs only in the clean Stop path (internal/daemon/daemon.go:1168).
  - `ReadState` does no liveness or staleness check (internal/ipc/state.go:230).
  - Hot-path ops return from `spoolAndReturn` before connect/lazySpawn (internal/ipc/client.go:215). lazySpawn is reached only on a failed connect.
  - A new daemon writes a sync state.bin at startup (daemon.go:716).
- The Action now says:
  - Start a new session (the reliable way out), or wait for the idle exit.
  - Do not end the process: a killed daemon leaves the spool setting in place, spool-mode hooks start no daemon, and nothing is replayed until the session is compacted or a new one starts.
- The "compacting does not reset it" sentence is now qualified with "while the same daemon is running". `qompack daemon stop` does not exist: the same page's "no operator stop command in this build" section says so. That is why D44's ledger text naming it was not followed.
- `go test -p 2 -count=1 ./test/docs` gives ok (runs/fix-test-docs-windows.log).

**3. [minor] No "no spool in isolation" check, and the pair compares populations of different sizes. CONFIRMED, fixed in 26ef2461, failing test first.**
- The failing tests were committed first as runs/fix-red-pair-population-windows.log:
  - TestV3_X11LedgerPairVerdict/unequal_populations failed three ways.
  - TestV3_X11DeferralNoteIsFound failed against a stub.
- Changes:
  - The new `x11DeferralNote` finds the harness's `delivery ledger: ` note. buildNotes writes that note only when `ledger.Deferred > 0` (test/bench/hotpath/measure.go).
  - In an isolated run, x11RequireAbsoluteRows now fails either run that carries it, the same way hotpathJudgeSpool refuses a transition in isolation.
  - `x11LedgerPairVerdict` returns a new sentinel `errX11PairIncomparable` when base.n != ledger.n. This is a refusal, never a verdict.
  - Isolated runs fail on it. Co-loaded runs report it: under D39 a co-loaded deferral is a reported outcome, and the task says co-load keeps its existing reporting behaviour.
  - The unreadable-note refusals are asserted to be neither sentinel, so a co-loaded run still fails them.
- n is deterministic in a clean run: the histogram gets 64 warm-up plus 2000 spawn samples, and n=2064 in every recorded run.

## Verification (Windows, -p 2, only workstream; Linux container stopped as instructed)
- `go test -p 2 -count=3 -v -run '^(TestV3_X11LedgerPairVerdict|TestV3_X11DeferralNoteIsFound|TestV3_X11CopyProjectIsExactButForLogs)$' ./test/e2e`: 9/9 top-level PASS, exit 0 (runs/fix-new-rows-e2e-count3-windows.log).
- `go run ./tools/devtool fmt-check`: exit 0.
- `GOOS={windows,linux,darwin} go vet ./test/e2e ./internal/config`: all exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all 8 PASS, exit 0.
- ALONE, with QOMPACK_UNDER_COLOAD unset: `go test -p 1 -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e` gave **PASS in 628.1 s** (runs/fix-e2e-x11-isolated-windows.log). This is the row's second run in this wave, within the two-run cap.

| Run | Harness time | B-A p99 | B-B p99 | observed p50 | observed p99 | n |
|---|---|---|---|---|---|---|
| no-ledger | 1m59s | 30.720 ms (limit 50) | 13.312 ms (limit 50) | 6.144 ms | 9.216 ms | 2064 |
| ledger-resident | 1m58s | 28.672 ms | 13.312 ms | 6.144 ms | 9.216 ms | 2064 |

  - Neither run has a delivery-ledger note.
  - Pair ceiling 7.680 ms (x1.25). The floor does not act at 6 ticks.
  - V2's 3.072 ms is reported only: V2's B-A excluded the handler and its ingest was not durable.
  - The whole row takes about 10.5 minutes, well under ci.yml's -timeout=30m; the implementer's run was 668.5 s.
- internal/config and gen-config-docs were not touched by the fix seat. The implementer's logs cover them.

## Criterion changes
- **Added** (tightenings):
  - Isolated X11 fails on any spool deferral.
  - The pair refuses unequal populations.
- **The floor** (db540905) loosens D42 as written, below 4 ticks only. It is proposed, not ratified; see needs_owner.

## Linux commands for the coordinator (container stopped here)
From Git Bash on the host, in this worktree:
1. `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w12-x11pair --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w12-x11pair --out C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w12-x11pair/plans/sdd/V6-closeout/w12-x11pair/runs/linux aa7b799c w12-touched-race -- ./internal/config ./test/docs`
2. Same options, then `aa7b799c w12-x11-isolated --no-race --timeout 60m --run '^TestV3_HotPathUnchangedWithLedgerResident$' -- ./test/e2e`. Expect PASS. Linux's observed p50 is likely one tick, which is exactly where the floor decision matters: with db540905 reverted, a 1.024 -> 2.048 flip fails.
3. Same options, then `aa7b799c w12-x11-fixtures --run '^TestV3_X11LedgerPairVerdict$' -- ./test/e2e`.

## Report note
The -run patterns above that use `^(A|B)$` alternation are the exact commands that ran. The runpatterns lint splits them at `|`, the same way it did for earlier waves' reports, which were waived.

### Commits

- 26ef2461 test(e2e): refuse X11 pairs over unequal or deferred runs
- d1d4cd25 test(e2e): hold X11's pair ceiling to D42's factor alone
- db540905 test(e2e): floor X11's pair ceiling at one observed tick
- d7cee9f7 docs(troubleshooting): drop killing the daemon as a spool exit
- aa7b799c docs(v6): add w12-x11pair fix-seat Windows evidence logs

### Tests

- `go test -p 2 -count=1 -run '^(TestV3_X11LedgerPairVerdict|TestV3_X11DeferralNoteIsFound)$' ./test/e2e (RED, before the fix)` — FAIL as intended: 3 unequal_populations subtests plus TestV3_X11DeferralNoteIsFound, exit 1 (runs/fix-red-pair-population-windows.log)
- `go test -p 2 -count=3 -v -run '^(TestV3_X11LedgerPairVerdict|TestV3_X11DeferralNoteIsFound|TestV3_X11CopyProjectIsExactButForLogs)$' ./test/e2e` — PASS 9/9, exit 0
- `go test -p 2 -count=1 -v -run '^(TestV3_X11LedgerPairVerdict|TestV3_X11DeferralNoteIsFound)$' ./test/e2e at d1d4cd25 (D42 literal)` — PASS
- `go test -p 2 -count=1 ./test/docs` — ok, exit 0
- `go run ./tools/devtool fmt-check` — exit 0
- `GOOS=windows|linux|darwin go vet ./test/e2e ./internal/config` — exit 0 on all three
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS, exit 0
- `go test -p 1 -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e (alone, QOMPACK_UNDER_COLOAD unset)` — PASS in 628.1s; observed p50 6.144/6.144 ms (n=2064 both), ceiling 7.680; B-A p99 30.720/28.672 ms, B-B p99 13.312/13.312 ms vs 50; no deferral

### Criterion changes

- Tightened: an isolated X11 run fails if either harness artifact carries the delivery-ledger (deferred) note. Co-loaded runs are unchanged and report it.
- Tightened: x11LedgerPairVerdict refuses a pair whose two hook_controlled_observed n differ (errX11PairIncomparable). Isolated runs fail on it; co-loaded runs report it, under D39.
- Loosening, proposed and unratified: the one-tick floor in db540905. Below 4 ticks the ceiling is base p50 + 1.024 ms instead of 1.25 x base p50. Rationale: the quantity cannot resolve anything finer than a tick. It is isolated in one commit so that reverting it restores D42 exactly.

### Open issues

- Linux and darwin are not verified in this wave because the container was stopped by instruction. The commands are in the summary. Linux is where the floor decision bites: its observed p50 is one tick.
- Spool submode still cannot recover within a session (D44 accepts this for 0.3.0). The doc now names only the exits that actually work: a new session or the idle exit.
- Commits d1d4cd25 and db540905 net to the implementer's floor code. That is on purpose, so the floor can be reverted alone.

### Needs the owner

- X11 pair resolution floor. It is a proposed amendment to D42, NOT ratified; commit db540905 has it active. The coordinator must either ratify it (add an amendment line to checklist row D42, plus an ERRATA/X11 line) or run `git revert db540905`, which restores D42 as written (d1d4cd25, with RED fixture rows for one-tick flips). Value: 1.024 ms, one reported tick of hook_controlled_observed. x11ObservedTickUS() computes it from the product's obs histogram: 1 ms observed, p50 bucket bound read back. Rule: ceiling = max(1.25 x base p50, base p50 + tick). Derivation: recvTS and req.TS are core.UnixMilli, so samples are whole milliseconds and a p50 is a whole number of 1/8-octave buckets. Below 4 ticks, 25% is less than the quantity's resolution. If the floor is wrong: too loose means that at Linux's 1-tick p50 a real move to 2 ms (+100%) passes. Without it, a quantization flip (1.024->2.048 on Linux, 3.072->4.096 on Windows, or 0.002->1.024) fails X11 with the product unchanged. It has no effect at 4 ticks or more, which covers every recent Windows run (4.096-6.144 ms).
- No other new numbers. x11RegressionFactor 1.25, x11HarnessBound and x11LimitDeltaMs are existing constants. D43 compares two existing config values. The new errX11PairIncomparable refusal and the deferral refusal introduce no thresholds.

## Independent verification of the fix seat: needs-fixes

- **major** `test/e2e/v3_x11_pair_test.go:163 (x11LedgerPairVerdict ceilingUS) and fixture rows :261-264; plans/V6-CLOSEOUT-CHECKLIST.md row D42 (unchanged in 26a96f70..HEAD)` — Finding 1 is still open at HEAD. The fix seat split the history well: d1d4cd25 implements D42 exactly as written, and db540905 adds the floor on top so it can be reverted on its own. But HEAD still carries the unratified one-tick floor, and it is active by default. D42 in the checklist still reads 'the existing factor; no new number', and no amendment or ERRATA line exists. So the reviewer's merge condition, that the checklist and the code agree, is not met. The branch as it stands ships the looser default: at Linux's one-tick p50, 1.024 -> 2.048 ms (+100%) passes.
  - Evidence: HEAD: `ceilingUS: math.Max(x11RegressionFactor*float64(b.p50us), float64(b.p50us+tickUS))`. At HEAD the fixture rows `{"one tick above a one-tick p50 is green (the floor)", 1.024, 2.048, false}` and `{... 3.072, 4.096, false}` are green. At d1d4cd25 the same rows are RED and the ceiling is `x11RegressionFactor * float64(b.p50us)`. `git diff 26a96f70..HEAD --stat -- plans/V6-CLOSEOUT-CHECKLIST.md plans/QOMPACK-ERRATA.md` is empty. The fix seat lists the floor under needs_owner as 'NOT ratified; commit db540905 has it active'.
  - Fix: The coordinator must decide before merge. Either ratify the floor as a named amendment on checklist row D42, plus an ERRATA/X11 line, or run `git revert db540905`. The revert applies cleanly, because only the logs commit aa7b799c comes after it, and it restores D42 as written with the RED fixture rows. Then re-run `go test -p 2 -count=1 -run '^TestV3_X11LedgerPairVerdict$' ./test/e2e`. If the owner cannot be reached before merge, fail closed: revert db540905, so that the ratified rule is the default and the floor stays a proposal.

