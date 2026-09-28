# w7-layout: EnsureLayout unmarks a layout before it grows

Branch `closeout/w7-layout`. Workflow `wf_0b499fad-a33`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `f4d3a0f2`

### Root cause

The w6-ckptsync verifier's finding holds. In internal/paths/layout.go, Barriers.EnsureLayout treats .qompack/.gitignore as meaning "layout entries known durable", but it computed `unmarked := gerr != nil` and never removed the marker when a later call had to create a missing layout directory. That call (a newer build adding a directory, or an operator removing one) marks the directory pending, creates it, and then its syncEntries fails. The daemon start then fails, and the retry is a new process. That process finds the directory on disk with ledger state entryUnknown and the layout still marked. So neither switch arm adds it to names: it syncs nothing, returns nil, and a name that was never made durable is reported durable.

Evidence: I ran the new test TestEnsureLayout_RetriesABarrierThatFailedOverAMarkedLayout at 898bb8b plus the test, with the test's marker assertion temporarily removed as a diagnostic. The retry's steps were `[]string(nil)` where `[dir:eval dir:.qompack dir:<root>]` was expected (runs/red/red-diag-retry-syncs-nothing-898bb8b.txt). With the assertion in place, the test fails because the marker survives the failed call (runs/red/red-marker-assert-898bb8b.txt).

### Summary

## w7-layout: EnsureLayout marker durability gap (w6-ckptsync verification) and the AppendRetentionRoot comment nit

The gap is fixed with a small rule change in EnsureLayout. Each package passed in full on Windows. On Linux, internal/paths passed in the full run; internal/store and internal/daemon each had one timing test fail in that run, and both passed when re-run alone.

### (1) The fix: take the marker away before adding a directory
**What the code does now** (internal/paths/layout.go, Barriers.EnsureLayout):
- The entry scan notes whether any layout directory was missing.
- If one was missing and the layout was marked, it removes .qompack/.gitignore before any MkdirAll, then sets `unmarked = true`. An error from that removal (other than not-exist) is returned as `paths: EnsureLayout: unmark ...`.
- The marker is still rewritten only after syncEntries succeeds, as before.
- So a call whose sync fails leaves the layout unmarked. The next process then finds no marker and syncs every entry (root, .qompack, eval).
- A successful call syncs only the new directory's parent and marks the layout again. The steady state still syncs nothing.

**Why this over syncing the parents on every call:** EnsureLayout does not run only once per daemon start. store.Open (fsck, backup, the daemon's observer ops) and negknow's ledger open also call it. Syncing the root, .qompack and eval every time would add three directory fsyncs to each of those opens. The unmark rule keeps that cost at zero and costs a few syncs only in the rare call that creates a directory.

**Residuals, stated in the doc comment:**
- Between the unmark and the re-mark, git would see .qompack if it looked.
- Two processes can race. If process B stats the marker just before process A removes it, B takes A's new directory as durable. This is the same cross-process stance Barriers.MkdirAll already takes and documents. It is narrow, and A's failure still leaves the layout unmarked for the next process.

**Docs:** The doc comment at layout.go now states the rule, and the "idempotent" sentence now reads "unless that file already exists and the call created nothing". The EnsureLayout sentence under "Backup and restore" in docs/architecture.md says the layout is unmarked before a directory is added, and that this covers the next call in this process or the next one.

**Tests** (internal/paths/barriers_retry_test.go):
- `TestEnsureLayout_RetriesABarrierThatFailedOverAMarkedLayout`: marks a layout, removes backup/, and fails the .qompack sync. It then models a fresh process and requires the retry to sync eval, .qompack and root and to re-mark. A third call must sync nothing.
- `TestEnsureLayout_ReMarksALayoutOnceItsNewDirectoryIsDurable`: a successful re-create syncs only .qompack and re-marks, and a fresh process over the complete layout syncs nothing.
- Test-only helper `paths.ForgetEntriesUnder(root)` in the new file internal/paths/export_test.go. It drops the ledger records at or below one root to model a fresh process, without touching other tests' trees.

### (2) The comment nit
internal/store/lifecycle.go, AppendRetentionRoot's comment now says the state directory is synced "unless this process has already made the file's name durable", and points to paths.AppendLinesDurable as the owner of the rule.

### Commands and results
**Red and green:**
- RED at 898bb8b plus the new tests: `go test ./internal/paths -run '^TestEnsureLayout_RetriesABarrierThatFailedOverAMarkedLayout$' -count=1 -v` failed. The marker was still present after the failed call; the diagnostic run without that assertion showed the retry syncing nothing. The sibling test `TestEnsureLayout_ReMarksALayoutOnceItsNewDirectoryIsDurable` passed on the base, which is expected: it pins the success path.
- GREEN at 418c678: that test plus `TestEnsureLayout_ReMarksALayoutOnceItsNewDirectoryIsDurable`, `TestEnsureLayout_RetriesABarrierThatFailed`, `TestEnsureLayout_SyncsALayoutAnotherWriterStarted` and `TestEnsureLayout_IdempotentAndNeverRewritesGitignore` all passed. Log: runs/red/green-418c678.txt.

**Checks:**
- `go vet ./internal/paths ./internal/store` on Windows and with GOOS=linux: ok.
- `go run ./tools/devtool fmt-check`: ok.
- `go test ./test/docs -count=1`: ok.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all 8 PASS. runpatterns and docmarkers were re-run after the evidence commit and passed.
- No generated-doc inputs changed, so no gen-*-docs check was needed.

**Windows, full packages at f6379c0, one at a time:**

| Package | Result | Time |
|---|---|---|
| internal/paths | ok | 11.0s |
| internal/store | ok | 498.6s |
| internal/daemon | ok | 443.0s |

Logs are runs/windows-full-*-f6379c0.txt.

**Linux, non-root with -race, at f6379c0:** `sh linux-nonroot-gate.sh --prefix cx-w7-layout f6379c00 w7-layout-final --timeout 30m -- ./internal/paths ./internal/store ./internal/daemon`

| Package | Result |
|---|---|
| internal/paths | PASS: 217 pass, 18 skip |
| internal/store | 871 pass, 1 fail: TestGC_DeadlineOvershootIsBoundedByTheCheckInterval |
| internal/daemon | 1535 pass, 1 fail: TestStop_IsNotHeldBehindASessionEndsDrain |

- Both failures are wall-clock tests, and the machine was loaded: my own Windows store and daemon runs overlapped this run, and the other workstreams were running too.
- Load is visible in the logs: the GC calibration ranged from 626ms to 2.93s against 217–259ms when run alone, and Stop took 2.73s against a 2.45s limit.
- Neither test goes through the EnsureLayout marker.
- Re-run alone at the same commit, both passed: `--run '^TestGC_DeadlineOvershootIsBoundedByTheCheckInterval$' -- ./internal/store` (1/1) and `--run '^TestStop_IsNotHeldBehindASessionEndsDrain$' --count 5 -- ./internal/daemon` (5/5). The artifacts are committed under runs/.

**Procedure note on the Linux run:** I had wrapped the host half of the gate in my own `timeout 1200`, and it killed the host script at 20 minutes. The container run kept going and finished; I copied its artifacts home by hand with docker cp. The host log is saved as host-partial.txt inside the final run's artifacts folder. None of my processes are still running in the container or on the host.

### Criterion changes
None. No assertion, threshold or golden was touched, and every existing test passes unmodified.

### Files
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-layout/internal/paths/layout.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-layout/internal/paths/barriers_retry_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-layout/internal/paths/export_test.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-layout/internal/store/lifecycle.go
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-layout/docs/architecture.md
- C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-layout/plans/sdd/V6-closeout/w7-layout/runs/

### Commits

- 418c6780 fix(paths): unmark a layout before adding a directory to it
- 001cac9f docs(architecture): state when EnsureLayout unmarks a layout
- f6379c00 docs(store): give AppendRetentionRoot the current dir-sync rule
- f4d3a0f2 chore(sdd): record w7-layout red, green and gate evidence

### Tests

- `go test ./internal/paths -run '^TestEnsureLayout_RetriesABarrierThatFailedOverAMarkedLayout$' -count=1 -v (898bb8b + new tests)` — RED: the marker survives the failed call; the diagnostic run without the marker assertion shows the retry syncing nothing (runs/red/)
- `go test ./internal/paths -run '^TestEnsureLayout_RetriesABarrierThatFailedOverAMarkedLayout$' -count=1 -v (418c678)` — PASS (with 4 sibling EnsureLayout tests, runs/red/green-418c678.txt)
- `go test ./internal/paths -count=1 -timeout=30m (Windows, f6379c0)` — ok 11.0s
- `go test ./internal/store -count=1 -timeout=30m (Windows, f6379c0)` — ok 498.6s
- `go test ./internal/daemon -count=1 -timeout=30m (Windows, f6379c0)` — ok 443.0s
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w7-layout f6379c00 w7-layout-final --timeout 30m -- ./internal/paths ./internal/store ./internal/daemon (non-root, -race)` — paths PASS; store 1 fail (TestGC_DeadlineOvershootIsBoundedByTheCheckInterval), daemon 1 fail (TestStop_IsNotHeldBehindASessionEndsDrain): wall-clock tests on a loaded machine
- `linux-nonroot-gate.sh f6379c00 rerun-gc-overshoot --run '^TestGC_DeadlineOvershootIsBoundedByTheCheckInterval$' -- ./internal/store (alone)` — PASS 1/1 (calibration 217-259ms)
- `linux-nonroot-gate.sh f6379c00 rerun-stop-drain --run '^TestStop_IsNotHeldBehindASessionEndsDrain$' --count 5 -- ./internal/daemon (alone)` — PASS 5/5
- `go vet ./internal/paths ./internal/store (Windows and GOOS=linux)` — ok
- `go run ./tools/devtool fmt-check` — ok
- `go test ./test/docs -count=1` — ok
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS (runpatterns and docmarkers re-run PASS after the evidence commit)

### Open issues

- Cross-process race (existing class, documented at layout.go): if process B stats the marker just before process A removes it, B takes A's newly created directory as durable. This is the same stance as Barriers.MkdirAll; A's failure still leaves the layout unmarked for the next process.
- Linux wall-clock tests TestGC_DeadlineOvershootIsBoundedByTheCheckInterval and TestStop_IsNotHeldBehindASessionEndsDrain failed under co-load in the full run and passed alone; they are unrelated to this change.

## Independent review

### review:layout: sound


## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.

