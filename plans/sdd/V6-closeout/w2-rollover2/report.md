# Wave 2b workstream report: w2-rollover2 (C1.10/D6)

Branch `closeout/w2-rollover2`. Workflow `wf_b2b236ea-ef1`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `e5180a2902de2df665b20a0d504dfc805a70286c`

### Root cause

(1) The D6 residuals were silent. The rotation pause, a GC pass halted on the carry bound, and the approaching first rotation had no Loud line, counter or doctor row. Doing this uncovered a real defect: a rotation whose carry passed 64 MiB still committed a carry file that every reader refuses (the next rotation, the offline check, fsck), so the journal failed closed a window late, on a state its own checks reject (red run 04). (2) The docs' 'daemon memory and every lookup bounded by the active window' was not true. The true bounds are: the window plus bounded caches between rotations; a lookup depth that grows with log(history); up to 64 MiB of carried leases plus the archive plan during a rotation; and a GC pass bounded by the window plus 65,536 carried leases. The GC half had already been fixed in wave 1. (3) Finding 5: predecessorsAcknowledged and leaseHeld read the generation store while holding Lock.mu and st (red run 05). An archive-read slot counted in inflight makes it provably safe to read outside the locks. (4) N3: the rollover tests lowered the rotation threshold through package variables (setRollover), so none could run in parallel. They are fsync- and file-creation-bound and ran one after another.

### Summary

w2-rollover2 report: rollover follow-ups under owner decision D6 (C1.10). Branch closeout/w2-rollover2 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w2-rollover2, 14 commits on b070bbe, head e5180a2. Working tree clean. Nothing was pushed, merged or tagged, and no commit carries a trailer.

## What the first attempt left behind, and what I did with it
The worktree had uncommitted edits in 13 files plus 3 new files, all from 17:37 to 18:03 today. No process from that attempt was still running (checked before I started). I reviewed every piece. I kept all of it, revised some of it, and split it into focused commits:
- Diagnostics (`delivery_diagnostics.go` and its test; `lock.go` and `daemon.go` attach them): kept and split across 4d3b00b, 3922d17 and f64ec6b. I rewrote the Loud messages where they claimed more than the code does: "nothing is lost" is dropped, the restart refusal is stated, and the GC-halt text now says when the carry actually shrinks. I added two checks to the refusal test: a restart refuses the same rotation again, and the offline check passes on that state (d7c6ece).
- Carry-bound refusal in doRotate: kept as a fix. I reproduced it red on this branch (run 04).
- Store GC-halt diagnostic: kept, message revised (302f974).
- Doctor row: kept, gofumpt-formatted, one unchecked type assertion fixed (dfbfca0, 651a8fb).
- Ordering-gate change (`delivery_order.go`, the slot helpers, the archive test): kept after re-deriving the correctness argument. Reproduced red on this branch (run 05).
- Runs 01 and 02 (the first attempt's red runs), `snapshot-test.sh`, and the Linux before-baseline at b070bbe: kept as evidence (6892ec3).

## Root causes
1. **D6 diagnostics.** The accepted residuals were silent. A rotation's pause showed nowhere. A GC pass halted on the carry bound read like any unreadable source: one Warn per pass, no counter. Nothing warned before the first rotation.
   - Doing (1) exposed a real defect. A rotation whose carry passed deliveryCarryMaxBytes (64 MiB) still staged and committed the carry. Every reader of that file refuses it (the next rotation, the offline check, fsck), so the journal failed closed one window later, on a committed segment its own checks reject.
   - Evidence: run 04 at 4d3b00b plus the test only. `TestDeliveryRollover_CarryPastItsBoundRefusesTheRotation` fails: "Target error should be in err chain: ...carried leases over their byte bound; in chain: (nil)".
   - The rotation now refuses before it stages anything. That is what D6's text accepted ("fails closed at the 64 MiB carry"). The window stays archived and nothing half-written remains: the offline check passes, pinned in d7c6ece.
2. **Memory claim in the docs.** The GC half of the review finding was already fixed in wave 1. A pass now reads only the active segment plus the carry, and halts when the carry is over 65,536 leases. What stayed false was the doc sentence "the daemon's memory and every lookup are bounded by the active window". The true bounds are:
   - between rotations, the active window plus bounded store caches (32,768 branch pages, 64 packs);
   - a lookup reads one path, whose depth grows with the log of history;
   - a rotation also briefly holds the archive plan and up to 64 MiB of carried leases (225 to 235 MiB peak heap measured);
   - a GC pass holds at most the window plus 65,536 carried leases.

   The docs and the TSV row now say this. No code change was needed.
3. **Finding 5.** predecessorsAcknowledged and leaseHeld read the generation store while holding Lock.mu and st.
   - Evidence: run 05 at dfbfca0 plus the test only. `HoldNeitherJournalLock` fails ("Should be zero, but was 6": store reads under Lock.mu). `FailClosed` deadlocks, but only because its test hook takes st inside the read. That shows st is held; it is not a production deadlock.
   - Fix (db1caf5): the window is still scanned under both locks. The store read now runs with neither lock held, under an "archive-read slot" taken under st. The slot counts in inflight, which both rotate() and closeLocked() wait out.
   - Why this is safe: rotation is the only writer that moves a lease from the window into the store, and a gate that sees rotating/closing/closed/fault still answers false at once. Otherwise the store only gains settlements. So a true answer stays true when it is used, and "in neither" is a proven absence as of the window read. A journal that starts closing or faults during the read answers false (leaseHeld returns an error). Fail-closed is preserved.
   - I rejected a per-session frontier cache: it would need invalidating on every archived ack and terminal mirror.
   - Measured (run 06, Windows, three alternating rounds, co-loaded). With four goroutines running the gate, an admission loop got st 29.5M to 43.2M times before (mean cycle 353 to 437 ns) and 63.6M to 66.1M times after (207 to 218 ns). Single-call gate latency is unchanged within noise (warm means 97 to 163 µs before, 85 to 199 µs after).
   - The change is confined to the two lookup functions in delivery_order.go.
4. **N3 (test time).** The rollover tests lowered the rotation threshold through package variables (setRollover), so none of them could run in parallel. They are bound by fsync and file creation (a Windows CPU profile showed 98% of time in syscalls), and they ran one after another.
   - Fix: the thresholds are now fixed per journal from its Lock (7d4272a). This is the same kind of seam as the existing Lock.sealFormat. Production values are unchanged (65,536 entries / 64 MiB).
   - 54 rollover-family tests now run in parallel (4213b4d). They go through `parallelRollover`, per-lock store builders, or are simply marked `t.Parallel` if they write no global state; the radix-merge seeds became parallel subtests.
   - Every threshold, rotation count and assertion is kept. Tests that set the rotate hook, the carry bound, the store GC hook or enableDeliveryGenerations, and those that read the process-wide loud ring, stay sequential.

## What changed, by task item
1. **Diagnostics.**
   - Rotations (4d3b00b): each one, live or finished at open, is a Loud line with the pause, the segments, the archived counts and the carry. Counters `delivery_rotations`, `delivery_rotation_pause_ms` and histogram `delivery_rotation_pause`. A failure is `delivery_rotation_failures` plus a Loud line.
   - Carry bound in the daemon (3922d17): `delivery_rotation_carry_over_bound` plus a Loud line. It fires on the live rotation and again at a restart's open.
   - First rotation (f64ec6b): one Warn when the lease journal reaches 3/4 of the threshold (49,152 leases or 48 MiB), or at an open that finds the store already past that point. It names `qompack backup create` and docs/backup.md, and counts in `delivery_first_rotation_backup_advised`.
   - Store GC (302f974): the carry-bound halt gets its own error, `GCReport.DeliveryCarryOverBound`, and counter `store.gc.delivery_carry_over_bound` on every halted pass. It is Loud once per run of halted passes; a pass that completes ends the run.
   - Visibility: `qompack status` already prints every daemon counter and the recent loud lines, and a test proves the rotation lines appear. `qompack doctor` gets a new row, `delivery.rollover` (dfbfca0). It reports whether the store has rotated, plus the last daemon's persisted counters. It is degraded on a failed rotation or a GC halt, not on the pause.
2. **Docs** (ed446b4): architecture, troubleshooting (the rotation entry plus new entries for the first-rotation Warn, the GC halt and the refused rotation), cannot-do (memory statement corrected, plus a new limit for the pause and the two carry bounds), backup (the Warn as the prompt), and the SP20-D4 row in CARRIED-DEFECTS.tsv.
3. **Finding 5:** fixed, as above.
4. **Test time:** as above.

## Before and after, internal/daemon under -race
| Where | Before (b070bbe) | After |
|---|---|---|
| Linux, non-root, GOMAXPROCS=4, co-loaded | 1015.0 s; 1383 pass, 0 fail, 1 skip | 565.7 s at 4213b4d; 1396 pass, 0 fail, 1 skip (−44%) |
| Windows, GOMAXPROCS=22, co-loaded | 731.3 s; 644 pass, 3 skip (run 03) | 503.0 s at 651a8fb; 657 pass, 3 skip (run 07; −31%) |

Both before and after were co-loaded by other workstreams. The Linux after-run is at 4213b4d; the commits after it change only two test assertions and evidence files.

## Commands and results
- Red runs:
  - `go test ./internal/daemon -run 'TestDeliveryRollover_CarryPastItsBoundRefusesTheRotation|...' -v` at 4d3b00b plus the test only: FAIL as expected (run 04).
  - `go test ./internal/daemon -run TestDeliveryOrder_ArchiveReads -v -timeout 60s` at dfbfca0 plus the test only: FAIL and deadlock as expected (run 05).
- Focused runs (Windows):
  - `go test ./internal/daemon -run 'TestDeliveryDiagnostics_|TestDeliveryRollover_CarryPast|TestDeliveryRollover_ACarry|TestDeliveryOrder_ArchiveReads'`: ok.
  - `go test ./internal/daemon -run 'TestDeliveryOrder_|TestDeliveryRollover_|TestFlush|TestDeliveryJournal_Acknowledged'`: ok, 223.7 s.
  - `go test ./internal/daemon -run 'TestDelivery|TestCarriedDefect'` after the threshold refactor, before the parallel conversion: ok, 297 s.
  - `CGO_ENABLED=1 go test -race` of the ordering, diagnostics and concurrent-callers tests with `-count=2`: ok, 63.5 s.
  - `CGO_ENABLED=1 go test -race` of the whole converted rollover family: ok, 56.1 s.
  - `go test ./internal/store -run TestGCSegments_ -v`: ok.
  - `go test ./internal/cli -run 'TestDoctor|TestFsck'`: ok, 16.8 s.
- Full packages:
  - daemon, Windows, no race: ok, 223 s.
  - daemon, -race: Linux 565.7 s and Windows 503.0 s, as in the table.
  - store 379 s and cli 31 s on Windows (run 08): ok.
  - Linux -race, focused store GC segment tests and cli doctor/fsck tests: 33 and 107 pass.
- Benchmark: `go test ./internal/daemon -run '^$' -bench '^BenchmarkDeliveryRolloverDispatchGate$' -benchtime 1x`, 3 rounds on each of dfbfca0 and db1caf5 (run 06).
- `go test ./test/docs`: ok.
- `go test ./test/guards -run TestCarriedDefects -v`: ManifestIsWellFormed and OpenRowsHaveLivingEvidence pass. WaveReportRequiresResolution still fails for the six pre-existing rows (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3).
- Lint and formatting:
  - `go run ./tools/devtool fmt-check`: pass.
  - `go vet` on daemon, store and cli, and GOOS=linux on daemon: pass.
  - Pinned golangci-lint on daemon, store and cli (`--allow-parallel-runners`, because another workstream held the lint lock): pass after 651a8fb.
  - `devtool lint --only=nomagic,importgraph,testdeps,sleepcheck,docmarkers`: pass.
  - runpatterns fails with 26 unsatisfiable patterns, identical at b070bbe and none in my files.
- Load: the Windows host and the Linux container were co-loaded throughout; w2-lifetime -race runs shared the container during my Linux after-run. No wall-clock assertion was added. No failure occurred, so none needed an alone re-run.

## Evidence
All under `plans/sdd/V6-closeout/w2-rollover2/runs/`: runs 01 to 08, and the Linux artifact directories for before, after, and the store/cli focused run.

### Commits

- 4d3b00b feat(daemon): report every delivery rotation loudly with a counter
- 3922d17 fix(daemon): refuse a rotation whose carry would pass its bound
- f64ec6b feat(daemon): warn before a store's first delivery rotation
- 302f974 feat(store): count and announce gc halts on the delivery carry bound
- dfbfca0 feat(cli): report delivery rollover in doctor
- db1caf5 perf(daemon): read archived leases outside the journal locks
- aa49507 test(daemon): measure admission's wait for st under gate traffic
- d7c6ece test(daemon): pin that a refused rotation leaves the offline check green
- ed446b4 docs: state the rollover pause, carry bounds and memory bound
- 7d4272a refactor(daemon): fix each journal's rollover thresholds at open
- 4213b4d test(daemon): run the rollover tests beside each other
- 6892ec3 docs(v6): record the w2-rollover2 baselines and first-attempt reds
- 651a8fb test: check the type assertions golangci-lint's errcheck flags
- e5180a2 docs(v6): record the rollover test-time and package runs after D6

### Tests

- `go test ./internal/daemon -run 'TestDeliveryRollover_CarryPastItsBoundRefusesTheRotation|TestDeliveryRollover_ACarryAlreadyPastItsBoundIsNamedAsSuch|TestDeliveryDiagnostics_FailedRotation' -count=1 -v (4d3b00b + test, without the doRotate refusal; run 04)` — FAIL as expected: the rotation commits a carry past its bound
- `go test ./internal/daemon -run 'TestDeliveryOrder_ArchiveReads' -count=1 -v -timeout 60s (dfbfca0 + test only; run 05)` — FAIL as expected: 6 store reads under Lock.mu; FailClosed deadlocks because the test hook takes st, which the gate held
- `go test ./internal/daemon -run 'TestDeliveryDiagnostics_|TestDeliveryRollover_CarryPast|TestDeliveryRollover_ACarry|TestDeliveryOrder_ArchiveReads' -count=1 (Windows)` — ok
- `go test ./internal/daemon -run 'TestDeliveryOrder_|TestDeliveryRollover_|TestFlush|TestDeliveryJournal_Acknowledged' -count=1 (Windows, after db1caf5)` — ok 223.7s
- `CGO_ENABLED=1 go test -race ./internal/daemon -run 'TestDeliveryOrder_ArchiveReads|TestDeliveryRollover_ConcurrentCallers|TestDeliveryDiagnostics_' -count=2 (Windows)` — ok 63.5s
- `go test ./internal/daemon -run 'TestDelivery|TestCarriedDefect' -count=1 (Windows, after the per-lock threshold refactor, before the parallel conversion)` — ok 297.3s
- `CGO_ENABLED=1 go test -race ./internal/daemon -run 'TestDeliveryRollover|TestDeliveryReaders_V6|TestDeliveryCarry|TestDeliverySealSegment|TestDeliveryRadix|TestDeliveryGeneration|TestCarriedDefect_SP20D4|TestDeliveryOrder_ArchiveReads|TestDeliveryTerminal_|TestDeliveryJournal_Generation|TestDeliveryPath_V6|TestDeliveryDiagnostics' -count=1 (Windows, after conversion)` — ok 56.1s
- `go test ./internal/daemon -count=1 -timeout=30m (Windows, whole package, converted tree)` — ok 223.0s
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w2-rollover2 4213b4d after-daemon-race --timeout 120m --coload -- ./internal/daemon (Linux non-root, -race, GOMAXPROCS=4)` — PASS 1396/0 fail/1 skip, 565.7s (before at b070bbe, same script and flags: 1383/0/1, 1015.0s); no race log
- `CGO_ENABLED=1 sh snapshot-test.sh 651a8fb 07-after-daemon-race-windows -- -race -json -count=1 -timeout=120m ./internal/daemon (Windows)` — ok 503.0s, 657 pass / 3 skip (before, run 03 at b070bbe: 731.3s, 644 pass / 3 skip)
- `sh snapshot-test.sh 651a8fb 08-store-cli-windows -- -count=1 -timeout=30m ./internal/store ./internal/cli (Windows)` — ok: store 379.3s, cli 30.7s
- `linux-nonroot-gate.sh 651a8fb store-cli-focused-race --run 'TestGCSegments_|TestDoctor_|TestFsck_' --coload -- ./internal/store ./internal/cli (Linux, -race)` — PASS store 33/0, cli 107/0
- `go test ./internal/store -run 'TestGCSegments_' -count=1 -v; go test ./internal/cli -run 'TestDoctor|TestFsck' -count=1 (Windows)` — ok; ok 16.8s
- `go test ./internal/daemon -run '^$' -bench '^BenchmarkDeliveryRolloverDispatchGate$' -benchtime 1x, 3 alternating rounds each on dfbfca0 and db1caf5 (run 06)` — admission st acquisitions under 4-goroutine gate traffic: 29.5M-43.2M at 353-437 ns/cycle before, 63.6M-66.1M at 207-218 ns after; single-call gate latency unchanged within noise
- `go test ./test/docs -count=1` — ok
- `go test ./test/guards -run 'TestCarriedDefects' -count=1 -v` — ManifestIsWellFormed PASS, OpenRowsHaveLivingEvidence PASS; WaveReportRequiresResolution FAIL only for the six pre-existing rows (coordinator's)
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon ./internal/store ./internal/cli; GOOS=linux go vet ./internal/daemon` — pass
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run --allow-parallel-runners ./internal/daemon/... ./internal/store/... ./internal/cli/...` — pass (after 651a8fb fixed 2 errcheck type assertions)
- `go run ./tools/devtool lint --only=nomagic,importgraph,testdeps,sleepcheck,runpatterns,docmarkers` — all pass except runpatterns: 26 unsatisfiable patterns, identical on b070bbe, none in this branch's files

### Criterion changes

- No check was weakened. No t.Skip, //nolint or //nomagic:allow was added, no threshold, budget or timeout was changed (only the evidence runs' own -timeout), no golden was regenerated, and no assertion was removed.
- Carry-bound rotations: a rotation whose carry would pass 64 MiB now refuses before staging anything, instead of committing a carry every reader refuses. Capture therefore stops one window earlier than before, but the committed state stays verifiable. Why: this is exactly D6's accepted behaviour ('fails closed at the 64 MiB carry'). The test pins the refusal, the restart refusal and a green offline check.
- Rollover tests: 54 delivery tests changed from the global setRollover seam to a per-Lock threshold (Lock.rolloverEntries, the same kind of seam as Lock.sealFormat) and now run in parallel. The merge test's 48 seeds became parallel subtests. Every test keeps its threshold, rotation count and assertions. The three store builders set the threshold on their own lock. Test helpers that reopen a store after building it use the same threshold the global used to supply (rolloverAt(1) or rolloverAt(2)).
- delivery_diagnostics_test and doctor_test: two unchecked type assertions became checked ones (errcheck). This is stricter, not looser.
- TestDeliveryRollover_CarryPastItsBoundRefusesTheRotation gained assertions: the offline check passes, and a restart refuses the same rotation with the carry-bound diagnostic.
- Interpretation of 'a Warn (once per store)': the first-rotation Warn is given at most once per journal handle, meaning once per daemon run, including at open when the store is already past 3/4 of the window. A durable once-ever marker would add a new file under .qompack/state that backup, fsck and the migration-evidence detection would all have to learn about. The coordinator can overrule this.
- GC carry-bound halt: the counter counts every halted pass; the Loud line fires once per run of halted passes (every pass still logs its Warn), so idle ticks do not flood LOUD.log.
- Docs/TSV: the SP20-D4 residual text in CARRIED-DEFECTS.tsv was corrected. The daemon's memory is bounded by the window between rotations, not on every lookup; the pause is 2.3-6.8 s; D6 is cited.

### Open issues

- Not every heavy test in internal/daemon is a rollover test, and the ones that are not were left sequential. The largest after the change (Linux -race): TestBoundedQueueDropsRatherThanBlocks 78 s, TestDeliveryOrder_DrainReversedPrefixBeyondBufferBoundDoesNotWedge 69 s (delivery_order_test.go, which the sessionend workstream edits), TestDeliveryPath_CrashCutAtEveryStep 22-43 s, and the group-commit twins. The twins' entry-cap cases write enableDeliveryGenerations through withRolloverDisabled. A per-lock 'rollover off' seam would let them run in parallel too; that is a possible follow-up.
- The Linux after-run is at 4213b4d. The commits after it (651a8fb and e5180a2) change only two test assertions and evidence files. The Windows after-run is at 651a8fb.
- Integrated gates (whole tree, Linux ALL-NON-E2E -race) and hosted CI were not run on this branch. CI's ubuntu whole-tree -race job has not been measured: the local figure is 566 s for the daemon package at GOMAXPROCS=4.
- This build has no in-product repair for either carry bound. The docs say to preserve the state and report it. Leases retired only by a policy denial are carried forever, so they count toward both bounds for the life of a project.
- Pre-existing, not from this branch: devtool lint runpatterns (26 unsatisfiable patterns, the same on b070bbe), and TestCarriedDefects_WaveReportRequiresResolution (six rows awaiting the coordinator's dispositions).
- The harness refuses report files from subagents, so plans/sdd/V6-closeout/w2-rollover2/report.md does not exist. The coordinator should commit it from this output.

### Needs the owner

- Confirm the first-rotation Warn cadence: once per daemon run while the store has never rotated, from 3/4 of the window on. The alternative is a durable once-per-store marker, which would add a new state file.
- Confirm that the carry-bound refusal failing closed one window earlier, with a verifiable committed state, is the behaviour D6 accepted. Also say whether the doc advice 'no repair in this build: preserve and report' is enough, or whether a later release should release retention on a terminal disposition so denials leave the carry. That was the rollover workstream's open GC-semantics item.

## Independent review

### review:rollover2: sound

- **minor** `internal/daemon/delivery_diagnostics.go:146-160 (firstRotationAdviceDueLocked); docs/backup.md:64; docs/troubleshooting.md:525` — The task asked for the first-rotation Warn 'once per store'. The implementation warns once per journal handle, which means once per daemon run. A store sitting between 49,152 and 65,536 leases therefore warns again on every restart. The implementer disclosed this under needs_owner, and the docs describe the per-run behaviour accurately. It is still a spec divergence that nobody has approved yet.
  - Evidence: j.firstRotationAdvised is an in-memory field on deliveryJournal. openDeliveryJournal calls adviseFirstRotationIfDue() on every open. TestDeliveryDiagnostics_FirstRotationAdviceAtOpen pins a Warn at each open that finds the store past 3/4. Implementer: 'Interpretation of a Warn (once per store) ... once per daemon run ... The coordinator can overrule this.'
  - Fix: Get the owner to confirm the once-per-run cadence (and change the D6/task wording to match), or record a durable once-per-store marker. If a marker is added, backup, fsck and migration-evidence detection must all be taught about it.
- **nit** `docs/architecture.md:183-185; docs/cannot-do.md:292-297` — The task asked for the true GC bound to be stated exactly. The docs say a GC pass 'holds at most the active window and 65,536 carried leases'. A pass also reads the whole segment transition log into memory (bounded at 1 MiB by dsegMaxLog). It also lists and validates the four files of every committed segment, so its I/O grows with the number of rotations. And it halts once the log passes 1 MiB or 65,536 segments, a third halt the docs do not mention. All of this is small or far off (about 5,800 rotations, roughly 380M deliveries), and it predates this branch.
  - Evidence: internal/store/gc_delivery_segments.go:51-52 (dsegMaxLog = 1<<20, dsegMaxSegments = 1<<16), :192 (dsegReadFileBounded(logPath, dsegMaxLog)), :246-253 (dsegRequireSegmentFiles for every committed seq).
  - Fix: Add one clause to the architecture/cannot-do memory statement: 'plus the segment transition log (at most 1 MiB), with a per-pass check of every committed segment's seals; past 1 MiB of log or 65,536 segments GC halts likewise'. Alternatively, record it as a pre-existing residual in CARRIED-DEFECTS SP20-D4.
- **nit** `internal/daemon/delivery_order_archive_test.go:95-178` — The archive-read slot (inflight++ in beginArchiveReadLocked) is what stops closeLocked from closing j.gen while an unlocked store read is still running. No test drives a close during an in-flight store read; the fail-closed test injects only a fault. The ordering property itself holds without the slot, because the window is read before the store, archiving comes before the window reset, and the store only grows. So the close safety is the one behaviour the slot adds, and it has no regression test.
  - Evidence: TestDeliveryOrder_ArchiveReadsFailClosed poisons the journal from the hashKey hook but never calls closeLocked/Release. Removing the inflight++/-- pair would leave every test in the file green.
  - Fix: Add a case where the hashKey hook starts j.owner.Release() in a goroutine and asserts it has not returned (e.g. j.gen is still non-nil, or the closing flag is set but closed is not) until the read returns. Then assert that predecessorsAcknowledged returns false and leaseHeld returns an error.

## Fix seat (review resolution) — status `done`, head `bcfbf66c4b60fe70daef959e60255ae44343bd70`

### Root cause

The reviewer's finding is accurate. The first-rotation advice flag (deliveryJournal.firstRotationAdvised) lives only in memory on the journal handle, and each daemon run opens exactly one handle. So the Warn repeats at every restart while the store sits between 3/4 of the threshold and its first rotation. This is a deliberate design choice, not a defect: doctor's visibility depends on per-run counters (internal/cli/doctor.go:958 and :1007-1009).

### Summary

FIX SEAT REPORT: w2-rollover2 (C1.10 / owner decision D6). Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w2-rollover2, branch closeout/w2-rollover2, cut from b070bbe. Final head is bcfbf66 and the tree is clean.

The reviewer's one finding is correct as stated. I kept the behaviour, pinned it with a test, stated the reason in the code and the docs, and left the wording for the owner to confirm (see needs_owner).

## Resume check
- `git status` was clean when I started.
- The implementer (wf_b2b236ea-ef1) had already committed 14 commits, 4d3b00b..e5180a2, on top of b070bbe.
- The first attempt (wf_0d8775ab-04e) left one thing that was kept: its Linux non-root -race BEFORE baseline at b070bbe, committed in 6892ec3 (1383 pass / 0 fail / 1 skip, 1014.96 s from test.jsonl).
- Its red logs 01 and 02 were kept as history. Runs 04 and 05 reproduce both on this branch's exact commits.
- No other leftovers were found, so nothing was discarded.

## What the workstream delivered (implementer, verified present at head)
**(1) D6 diagnostics.**
- Every rotation gets a Loud line plus counters `delivery_rotations`, `delivery_rotation_pause_ms` and the `delivery_rotation_pause` histogram (4d3b00b).
- A failed rotation adds to `delivery_rotation_failures`.
- A rotation whose carry would pass 64 MiB is now refused before anything is staged, and adds to `delivery_rotation_carry_over_bound` (3922d17). This fixed a real defect: before, the rotation that crossed the bound committed a carry that every reader rejects, and the journal failed one window later (run 04).
- A store GC pass that halts on more than 65,536 carried leases is counted in `store.gc.delivery_carry_over_bound`, with a Loud line on the first halted pass of each run of halts (302f974).
- A store that has never rotated gets a first-rotation Warn at 3/4 of the threshold, which names `qompack backup create` and docs/backup.md (f64ec6b).
- Doctor has a new `delivery.rollover` row (dfbfca0). Status already shows the counters and loud lines.

**(2) Docs.** architecture, troubleshooting, cannot-do, backup and the CARRIED-DEFECTS.tsv row for SP20-D4 now describe the pause, both carry bounds, the backup advice and the true memory bound (ed446b4). The old claim that memory is bounded by the active window is gone. The true bound: the active window plus log-depth lookups; a GC pass holds at most the window and 65,536 carried leases; a rotation briefly also holds up to 64 MiB of carry (225-235 MiB peak heap measured).

**(3) Review finding 5 (store reads under the journal locks).** The generation-store read in `predecessorsAcknowledged` and `leaseHeld` now runs with neither `owner.mu` nor `st` held (db1caf5):
- It runs under an archive-read slot that is counted in `inflight`, so a rotation or close waits it out.
- Every answer is a fact that stays true once true.
- A journal that starts closing or faults during the read answers false or an error, so it fails closed.
- Evidence, run 06 on Windows (co-loaded, figures only, nothing asserted):
  - Admission took `st` 29.5M-43.2M times at a 353-437 ns cycle before, and 63.6M-66.1M times at a 207-218 ns cycle after.
  - The single-call gate latency is unchanged within the host's noise.

**(4) Linux lane N3 (the slow rollover tests).**
- Rollover thresholds now live on each Lock, read when the journal opens (7d4272a), so the rollover-family tests can run in parallel (4213b4d).
- Every threshold, rotation count and assertion is unchanged.
- Package times under -race:

| Run | Before (b070bbe) | After |
|---|---|---|
| Linux non-root, GOMAXPROCS=4, co-loaded | 1014.96 s, 1383 pass | 565.74 s at 4213b4d, 1396 pass, 0 fail, 1 skip |
| Windows | 731 s | 503 s at 651a8fb |

## Review resolution
**Finding (minor): the first-rotation Warn is "once per store" in the task but once per daemon run in the code.**
- **Verified correct.**
  - `j.firstRotationAdvised` is an in-memory field on `deliveryJournal`.
  - The Lock opens its journal once (`l.journal` is only ever assigned in delivery_lease.go:391 and delivery_frozen_seal.go:237), and `openDeliveryJournal` calls `adviseFirstRotationIfDue()`.
  - So a store between 49,152 and 65,536 leases is warned again at every restart.
- **Action: kept the per-run cadence on purpose, pinned it, documented why, and routed the wording to the owner.** No durable marker was added. Reasons, with evidence:
  - **Doctor would go quiet.** Doctor's `delivery.rollover` row reads only the last run's counters from metrics/latency.json, which reset at every restart (internal/cli/doctor.go:958 and :1007-1009 print "the last daemon warned that this store's first rotation is near" only when that run's `delivery_first_rotation_backup_advised` > 0). With a once-per-store Warn, every run after the first restart would record 0, and doctor would stop naming the coming rotation for the rest of the window. Task (1) requires status/doctor visibility.
  - **A marker is risky state.** It would be new durable state written into a store that has not yet rotated, which is exactly the window where the store must stay usable by an older build (the downgrade path the Warn protects). backup (refuseIfTheProjectMoved), fsck, `migrationEvidenceExists`/`existingDeliveryMigration` and restore would all have to learn it. It would also need tying to the store's identity: a marker that outlived a recreated state/ would suppress the Warn, which fails in the unsafe direction.
  - **The owner's text allows it.** D6 in V6-CLOSEOUT-CHECKLIST.md says only "a Warn before the first rotation recommends a backup". "(once per store)" was added in the task text. Per-run still gives at most one Warn per run, not one per lease, and gives at least one before the rotation.
- **Changes made:**
  - fa95d49 adds `TestDeliveryDiagnostics_FirstRotationAdviceRepeatsEachRun`: run 1 crosses the point and warns once; a restart before the rotation must warn again at open with `window_leases=6`, once only within that run, with the counter at 1.
  - This is a characterization test of the chosen behaviour, not a failing-first test, because no product code changed.
  - Mutation check: a throwaway variant that remembers advised stores across journals fails this test and no other. The variant was reverted.
  - The comment on `firstRotationAdviceDueLocked` (internal/daemon/delivery_diagnostics.go) states the choice and the reason.
  - aa15636 adds to docs/troubleshooting.md that every restart before the rotation warns again, and why. docs/backup.md already said "warns once per run".

## Commands and results (fix seat, Windows, co-loaded host)
- `go test -count=1 -timeout=10m -run TestDeliveryDiagnostics_ ./internal/daemon` at e5180a2: ok (2.96 s).
- `-run TestDeliveryDiagnostics_FirstRotation -v` with the new test: 4/4 PASS. With the once-per-store mutant: only the new test FAILs ("the next run warns again, at its open").
- `go test -race -count=1 -timeout=15m -run TestDeliveryDiagnostics_ ./internal/daemon`: ok (3.98 s).
- `go test -count=1 -timeout=30m ./internal/daemon` (the touched package, once in full, without -race): ok in 161.1 s. Log committed as plans/sdd/V6-closeout/w2-rollover2/runs/09-fix-daemon-windows.log.
- `go run ./tools/devtool fmt-check`: exit 0. `go vet ./internal/daemon ./internal/store ./internal/cli`: exit 0. `go build ./...`: exit 0.
- `go test -count=1 ./test/docs`: ok. `gen-config-docs`, `gen-command-docs` and `gen-mcp-docs --check`: all up to date.
- No wall-clock failures occurred, so no re-runs were needed. No pipe masked an exit code: the one verbose run used PIPESTATUS.

## Criterion changes (with rationale)
- The first-rotation Warn cadence is "once per daemon run past the 3/4 point, and at least once before the first rotation", instead of the task's "once per store". The rationale is under Review resolution: it keeps doctor showing the advice and avoids new durable state in a store that must still suit an older build.
- No check was weakened, and no thresholds, budgets or assertions were changed.

## Open items
- Integrated gates for C1.10 are still pending on the coordinator's side.
- The Linux -race run of the whole daemon package was not repeated after fa95d49. The change is one sequential test plus a comment, and the focused Windows -race run is green.

### Commits

- 4d3b00b feat(daemon): report every delivery rotation loudly with a counter (implementer)
- 3922d17 fix(daemon): refuse a rotation whose carry would pass its bound (implementer)
- f64ec6b feat(daemon): warn before a store's first delivery rotation (implementer)
- 302f974 feat(store): count and announce gc halts on the delivery carry bound (implementer)
- dfbfca0 feat(cli): report delivery rollover in doctor (implementer)
- db1caf5 perf(daemon): read archived leases outside the journal locks (implementer)
- aa49507 test(daemon): measure admission's wait for st under gate traffic (implementer)
- d7c6ece test(daemon): pin that a refused rotation leaves the offline check green (implementer)
- ed446b4 docs: state the rollover pause, carry bounds and memory bound (implementer)
- 7d4272a refactor(daemon): fix each journal's rollover thresholds at open (implementer)
- 4213b4d test(daemon): run the rollover tests beside each other (implementer)
- 6892ec3 docs(v6): record the w2-rollover2 baselines and first-attempt reds (implementer)
- 651a8fb test: check the type assertions golangci-lint's errcheck flags (implementer)
- e5180a2 docs(v6): record the rollover test-time and package runs after D6 (implementer)
- fa95d49 test(daemon): pin the first-rotation advice to once per run (fix seat)
- aa15636 docs: say the first-rotation warning repeats every run (fix seat)
- bcfbf66 docs(v6): record the review-fix daemon package run (fix seat)

### Tests

- `go test -count=1 -timeout=10m -run TestDeliveryDiagnostics_ ./internal/daemon (e5180a2, before edits)` — ok 2.958s
- `go test -count=1 -timeout=10m -run TestDeliveryDiagnostics_FirstRotation -v ./internal/daemon (with new test)` — 4/4 PASS, ok 1.470s
- `same focused run with a throwaway once-per-store mutant in delivery_diagnostics.go (reverted)` — only TestDeliveryDiagnostics_FirstRotationAdviceRepeatsEachRun FAILs ('the next run warns again, at its open') - test discriminates
- `go test -race -count=1 -timeout=15m -run TestDeliveryDiagnostics_ ./internal/daemon` — ok 3.984s
- `go test -count=1 -timeout=30m ./internal/daemon (full touched package, Windows, co-loaded; runs/09-fix-daemon-windows.log)` — ok 161.122s, exit 0
- `go run ./tools/devtool fmt-check; go vet ./internal/daemon ./internal/store ./internal/cli; go build ./...` — all exit 0
- `go test -count=1 -timeout=20m ./test/docs` — ok 2.115s
- `go run ./tools/devtool gen-config-docs|gen-command-docs|gen-mcp-docs --check` — all up to date, exit 0
- `implementer evidence: linux-nonroot-gate.sh -race ./internal/daemon before b070bbe / after 4213b4d (GOMAXPROCS=4, co-loaded)` — before 1383 pass/0 fail/1 skip, 1014.96 s; after 1396 pass/0 fail/1 skip, 565.74 s
- `implementer evidence: go test -race ./internal/daemon on Windows, b070bbe vs 651a8fb (runs 03/07)` — 731.3 s -> 503.0 s, both ok
- `implementer evidence: go test ./internal/store ./internal/cli Windows (run 08) and focused -race Linux store/cli (651a8fb)` — store ok 379 s, cli ok 31 s; Linux race 33 + 107 pass

### Criterion changes

- First-rotation backup Warn: 'once per store' (task text) becomes 'once per daemon run past the 3/4 advice point, at least once before the first rotation'. The owner's D6 text says only 'a Warn before the first rotation'. Doctor visibility requires it, because doctor's delivery.rollover row reads only the last run's counters, which reset at restart. A once-per-store marker would be new state written into a store that has not rotated and must stay usable by an older build. Pinned by TestDeliveryDiagnostics_FirstRotationAdviceRepeatsEachRun.

### Open issues

- The Linux -race run of the whole daemon package was not repeated after fa95d49 (one sequential test plus a comment). The focused Windows -race run is green.
- Integrated gates for C1.10 are still pending (the coordinator's job).

### Needs the owner

- D6 wording: please confirm the first-rotation Warn cadence. It fires once per daemon run from 49,152 leases (or 48 MiB) until the first rotation, rather than once per store. It is kept per run so each run's doctor delivery.rollover row, which reads only that run's counters, still names the coming rotation, and so no new durable state is written into a store that must still suit an older build. If you want strictly once per store, it needs a durable marker tied to the store's identity, and backup, fsck, migration-evidence detection, restore and doctor would all have to learn it. The checklist wording would then change to match.

