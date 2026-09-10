# Wave 1 — carried defects without a per-subplan document

Rows in `plans/CARRIED-DEFECTS.tsv` whose subplan has no
`plans/V2-SP-NN-carried-defects.md` of its own are explained here, per the derivation rule in
`test/guards/carrieddefects_test.go`: the guard prefers the per-subplan document whenever one is on
disk, and falls back to this file only when it is not. Creating
`plans/V2-SP-06-carried-defects.md` later MOVES every SP06-* section out of this file.

---

## SP06-D1 — `GCPolicy.Deadline` bounds only the sweep, not the tombstone phase

**Symptom.** `store.GC` runs `tombstoneDeadRoots` before the deadline-aware sweep, and that phase
answers only to `ctx`. `GCReport.Duration` can therefore exceed `GCPolicy.Deadline` by however
long tombstoning takes — measured 100–260 ms on a Windows host at 650 dead roots. The sweep's own
overshoot is tight and tested (`TestGC_DeadlineOvershootIsBoundedByTheCheckInterval`: 11–22 ms
against limits of 50–63 ms over ten runs), which is exactly why the tombstone phase is the whole
of the remaining gap.

**One of the three phases named here has since been fixed.** The mark phase was unbounded too — it
took neither `ctx` nor the deadline, and `harvestHashes` streamed every checkpoint, pins and
elimination file with nothing able to stop it — and the post-audit fix round bounded it
(`TestGC_MarkPhaseHonoursTheDeadline`). A truncated mark ends the pass with nothing collected,
because an incomplete live set cannot be swept against. This row is now about the tombstone phase
ALONE, which is where the remaining overshoot lives.

**Why it is not fixed at V2.** Bounding the tombstone phase needs a phase-aware resume cursor —
today's cursor resumes the sweep, and a deadline hit mid-tombstone would either re-tombstone from
the top (quadratic on repeated short deadlines) or skip dead roots silently. That is a design
change to GC's resume contract, larger than the checkpoint's fix authorization, and it interacts
with SP-17's `fsck` (which is what repairs a root/object mismatch today). The shape is recorded in
the GC deadline tests' comments in `internal/store`.

**Note on numbering.** This id lives in `CARRIED-DEFECTS.tsv`'s namespace. It is unrelated to the
D-numbered adjudication table inside `plans/V2-SP-06-content-addressed-store.md` (D1–D18 there are
ship-time notes, resolved in place).

**Acceptance (V3).** Either a phase-aware cursor makes `Duration ≤ Deadline + one check interval`
hold across both phases with a test at a few hundred dead roots, or the field's doc comment is
changed to say the deadline scopes the sweep alone and this row moves to `wontfix` with that
wording pinned.

---

## SP05-D1 — a budget-starved drain consumes the line whose dispatch it aborted

**Symptom.** `drainFile` advances the offset and commits the dedup key (`SeenOrAdd`) BEFORE
dispatching a line, and the per-line context is derived from the drain's own. When the idle tick's
budget expires mid-binding, the dispatch is cancelled half-done, but the line is already consumed
on both ledgers: the offset points past it and the seen-set says it ran. The next drain skips it.
The event is gone — at-most-once on a path whose contract is "costs freshness, never data".
Observed by V2-VERIFY's §4.7 authoring under `-race` with a near-expired `admin.idle` budget while
the ring had lines in flight; the quiet-session idle loop (generous budget, empty ring) does not
reach the interleaving, which is why nothing had seen it.

**Why it is not fixed at V2.** The unconditional consume is a recorded, deliberate decision
(task-3-spec.md drain.go step 3, restated in the code comment): a permanently-failing line must
not wedge the file, so "dispatched" counts as consumed even when the handler refuses or times
out. Distinguishing a poison line from a healthy line aborted by the drain's own death requires
either rolling back the seen-set entry (the set is shared live with the ring workers and has no
removal semantics) or committing offset/seen only after dispatch returns (which re-opens the
wedge the rule exists to close unless paired with a retry budget). Both are design changes to an
adjudicated rule, owned by a checkpoint that can re-adjudicate it, not a surgical fix.

**Acceptance (V3).** Either the drain distinguishes ctx-death from handler failure (e.g. check
the drain ctx after dispatch; a line whose dispatch was cut short by the drain's own cancellation
is neither offset-advanced nor seen-committed, bounded by a per-line retry cap so poison lines
still cannot wedge), with a test driving exactly the starved-budget interleaving — or the ruling
is re-affirmed with the loss documented in the drain's contract and D4's "never data" wording
amended, and this row moves to `wontfix` with that wording pinned.

**Disposition (V5-VERIFY, 2026-09-08): fixed by SP-20; evidence added.** V5-VERIFY §6 carried
this row to SP-20's drain/ack recovery, and SP-20 re-adjudicated the rule rather than patching
around it. `f6a8691` (`fix(daemon): retain unacknowledged drain recovery data`, Refs SP05-D1 /
T20-M1-05) replaced `SeenOrAdd`-then-dispatch with `seenSet.begin` / `finish(key, acknowledged)`,
moved `offset, fs.Offset = nextOffset` to AFTER `dispatchPending` returns nil, and made
`dispatchPending` return the per-line context's error when the handler answers `OK:false` with a
dead context — so a dying drain surfaces as `context.DeadlineExceeded` / `context.Canceled`,
stops the pass with the offset still pointing AT the interrupted line, and never commits the
seen-set. `a6faab0` then put the lease/acknowledge journal behind the same boundary. The
poison-line consume rule the V2 note said had to be re-adjudicated was, and SP-20 chose the
other side of the trade: a handler that refuses a line with a LIVE context is ALSO no longer
consumed (it is `DrainGapUnacknowledged`, redelivered on the next pass;
`TestDrainRejectedResponseIsRetryableAfterRestart` pins it), and "consumed regardless" survives
only for the terminal admission verdicts (`DrainGapDenied`, `DrainGapUnadmitted`), which retrying
cannot change. A dying drain and a refusing handler are therefore distinguishable by error class
— which is what this row asked for — but the V2 acceptance's "bounded by a per-line retry cap so
poison lines still cannot wedge" was NOT delivered, and the residual should be stated plainly: a
handler that persistently answers `OK:false` with a live context (`runIngested`'s "stop handling
failed" shape) or panics (`dispatchPending`'s "handler panicked") makes `drainFile` break its
read loop at that line with the offset unadvanced, on every pass, with no retry cap, so every
record behind it is blocked until the handler stops refusing. That trades loss for a wedge. It is
SP-20's merged, pinned choice, not an oversight: T20-M1-05 ("interrupted drain SP05-D1, bounded
queue, retry") and `00-ARCHITECTURE.md` step 2 specify lease → handle → ack with redelivery of
the same observation, and `plans/V4-report.md` BLOCKER 2 re-adjudicated the wedge only for the
ADMISSION class (an unadmittable record is skipped as a loud gap because its verdict is baked into
the spooled bytes and can never change) while leaving a refusing handler retryable, on the
reasoning that its answer CAN change. No retry cap for the handler class exists in code or plan;
if one is wanted it is new work, not part of this row's closure.

The evidence test, `internal/daemon/drain_idle_budget_test.go`
`TestCarriedDefect_SP05D1_IdleBudgetExpiryLeavesInterruptedLinePending`, drives the exact
scenario: `idleController.RunOnce` with the drain registered under `idleTaskDrain`, a budget that
expires while the handler is still binding the second line (the handler blocks on its context and
answers `observation handling interrupted`, `runIngested`'s shape), then recovery in both forms —
a same-process retry on the same drainer and seen-set, and a fresh drainer after restart. It
asserts both ledgers (persisted offset equals the first line's length and `Done` is false; the
seen-set does not hold the interrupted key) and that exactly the interrupted line is redelivered.

The budget's expiry is a program order, not a wall clock. `RunOnce` arms a REAL timer for its
budget (`context.WithTimeout(ctx, remain)`; the fake clock only fixes `remain`), and the first
cut of this test handed it 20 ms, which also had to cover the lease-journal open, the capture
fsync and the first line's dispatch on a fresh `TempDir` before the second line was reached —
review measured 16 of 40 co-loaded runs failing at "both lines were dispatched before the budget
expired" (`scratchpad/rev-flake-probe.log`) against 40 of 40 passing alone. The test now runs
`RunOnce` under a test-only parent context (`sp05d1BudgetContext`) whose `Done` the second line's
handler closes from inside its binding with `Err() == context.DeadlineExceeded` — the error
`RunOnce`'s own timer would have produced, inherited by the derived timeout context down to the
per-line handler context — and hands `RunOnce` a one-hour budget so its real timer never fires.
Re-proof: `go test ./internal/daemon -run TestCarriedDefect_SP05D1 -count=40 -race` passed 0/40
failing (`scratchpad/sp05d1-coload-40.log`, 8.3 s) with the whole of it overlapped by a
concurrent `go test ./internal/daemon -race -count=1` (50.9 s, started a second later,
`scratchpad/sp05d1-coload-load-daemon.log`) on the same host. The reworked test is still RED on
the pre-SP-20 tree at the same offset-ledger assertion, `expected: 46, actual: 92` in both
subtests (`scratchpad/sp05d1-v3-before.log`), so the program-order expiry reproduces the defect
exactly as the wall-clock one did.

RED/GREEN proof, both on the same test file. Against the pre-SP-20 tree (`git archive f6a8691^`
extracted to the session scratchpad): `go test ./internal/daemon -run TestCarriedDefect_SP05D1
-count=1 -v` fails both subtests at the offset ledger, `expected: 46, actual: 92` — both lines
consumed (`scratchpad/sp05d1-before.log`). Against `verify/v5 @ 87c0c1d` plus the test: the same
command passes (`scratchpad/sp05d1-after.log`), and `-race -count=5` together with
`TestDrainRejectedResponseIsRetryableAfterRestart` and
`TestDrainCanceledRejectedHandlerLeavesCurrentLinePending` passes (`scratchpad/sp05d1-race.log`).
No code change was needed; the row moves to `fixed` with that test as its evidence.

---

## SP06-D2 — the `PutBytes` cold and warm budgets are unreachable and unverified

**Symptom.** `plans/V2-SP-06-content-addressed-store.md` sets `BenchmarkPutBytes_100KB_Cold` at
≤ 3 ms and `BenchmarkPutBytes_100KB_Warm` at ≤ 400 µs. Measured on the Windows development host,
2026-08-23, medians of five runs at 50 iterations: **cold 27.2 ms** (no-redact 25.6 ms) and **warm
7.68 ms** (no-redact 6.79 ms). The warm row is 19× its budget and the cold row 9×.

**Why the numbers are not the whole story.** SP-06's D16 established that these benchmarks are
platform-bound: profiling put 93 % of the residual in `runtime.cgocall`, i.e. ~25 file create+rename
pairs per put, which cost 10–40× less on Linux. The budgets were set for the CI Linux runner, and
**no Linux measurement of them exists** — `plans/V2-report.md` §9.3 records the pair as "documented
over … Linux re-measure deferred", and CI has never completed a green run (the Actions quota has
blocked every job since 2026-08-22).

**Why this row exists at all.** `plans/V2-report.md` §2.6a ② recorded this pair as "fixed here as a
budget revision, not code". Only the Redact half of that pair was actually revised; the `PutBytes`
half was not, and §16.3 item 10 re-opens it. So the plan carries a budget nothing meets, the report
says it was revised, and neither statement is checked by anything.

**Acceptance (V3).** A measured number on the reference platform, with the payload shape and the
platform named in the row itself, replacing both figures — or a statement that the budget is a Linux
figure and a Windows exemption with its factor recorded, in the plan and in §9.3 together. What is
not acceptable is a third round of "documented over".


---

## V3-VERIFY dispositions (2026-08-26)

**SP06-D1 -> wontfix (option b taken).** GCPolicy.Deadline's doc comment now says what it actually
scopes: the mark's hash harvest and the sweep; the tombstone phase and the mark's in-memory index
walks answer only to ctx. The 100-260 ms tombstone overshoot at 650 dead roots stands as documented
behaviour. V3 lane figures: BenchmarkGC_50kObjects 1.217 s (budget 2 s), and the V2-SP06-20
overshoot gate is 3/3 green in isolation (its one J1 red was the whole-tree run's own package
parallelism).

**SP05-D1 -> deferred:V4-VERIFY.** The dying-drain vs refusing-handler distinction is
transport-shape work: it needs the same per-session sequencing surface as SP-08's parked R3
(same-session ordering), and wave 3's SP-11/SP-12 rework the drain path both would land in.
Re-adjudicating it now would be redesigning it twice.

**SP06-D2 -> deferred:V4-VERIFY, now measured on both platforms.** V3 lane (quiet Windows host):
PutBytes cold 16.1 ms / warm 4.7 ms (V2 recorded 27.2 / 7.68). First Linux figures (WSL2
docker-desktop distro on the same 22-core host, native-tmpfs store, 6 runs): cold 5.11-5.87 ms,
warm 4.33-4.60 ms. The 3 ms / 400 us budgets are unmet on Linux too - cold ~1.8x over, warm ~11x
over - so this is no longer a measurement gap but a budget-vs-implementation decision, and it
travels to V4-VERIFY beside SP08-D1, whose B-C breach is dominated by this same per-novel-chunk
write path. Windows/Linux exemption factor from these runs: ~3x cold, ~1.05x warm.

---

## V5-VERIFY dispositions (2026-09-09)

**SP06-D2 -> deferred:V6-VERIFY.** V4-VERIFY did not dispose of the row (its report carries it as
sign-off item 5). V5 quiet pass on `verify/v5` @ `0d5c999` (Windows, serial, machine otherwise
idle, `scratchpad/quiet/E5-I-06.18-*.txt`): PutBytes cold 15.74 ms / warm 5.65 ms
against 3 ms / 400 us. No reference-platform figure could be taken on this host: the only WSL
distribution is docker-desktop (read-only filesystem) and the Docker daemon is stopped, both user
actions to change, and a pushed branch is the only other path to the Linux CI leg — outward-facing,
not taken under this checkpoint. The decision the V3 disposition named (budget revision versus
implementation change on the per-novel-chunk write path) is still open and is the same decision as
SP08-D1's; V6-VERIFY, whose SP-17 pushes to CI, takes both together with SP10-D1 and SP20-D2.

## SP05-D2 — the shipped ACK deadline is shorter than one leased `Accept`

`deferred:V6-VERIFY`. Found by V5-VERIFY fix item F4 while pinning F4-P1 (the
drain replay of acknowledged copies, fixed in `1c17f0d`). The hook client waits
`State.AckDeadlineMs` (shipped default 8, `internal/config/defaults.go`) for the daemon's one-byte
transport ACK and otherwise appends the request to `spool/client-<pid>.ndjson`
(`internal/ipc/client.go` `spoolAndReturn`). On this host, under the load of a full e2e session,
24–44 of 196 hook deliveries per `TestV3_LiveSessionWriteSetAndAppendOnly` run (12–22 %) took that
fallback AFTER the daemon had leased and acknowledged them (`scratchpad/rev2-analyze.py` output,
`scratchpad/r2e/`, `scratchpad/f4r3/`). The B-B diagnosis (SP20-D1) then showed the general case: since `f6a8691`
the transport ACK is written only after `Accept`'s fsyncs, so whenever one `Accept` takes longer
than 8 ms — every leased `Accept` on this disk, and every `Accept` at all under bench-hotpath's
burst — the hook times out and spools, and bench-hotpath's delivery reconciliation hides the
resulting duplicates. With F4-P1 fixed each such copy is skipped at the next
drain; what remains is wasted spool I/O on the hook's hot path and a startup drain proportional to
it. No evidence test is named: the symptom is a rate under host load, and a deterministic pin needs
a fake clock inside the client's ACK wait, which is SP-17's hardening list. Resolution: measure the
fallback rate on the reference platform and either raise the deadline within budget B-B or make the
client's ACK wait independent of scheduler latency.

## SP20-D1 — budget B-B cannot hold the leased delivery path's durability points

`deferred:V6-VERIFY`. Budget B-B (`l0_ingest`, gated, p99 < 2 ms, "daemon read
to WAL append returned") is incompatible with the durability design SP-20 M1 shipped for the
leased delivery path. `internal/daemon/ingest.go` `Accept` documents it: three durability points,
four fsync syscalls per accepted leased delivery — the WAL append sync, the delivery journal's
lease write and sync, and the lease-position sidecar written through `paths.WriteAtomic` (temp-file
fsync plus directory fsync) — and states that the reductions considered (sealing the position per
batch, deferring the lease off Accept) are not durability-neutral. Until V5's `fix(ipc)` the hook
client's nonce was refused by the journal, every delivery was unleased and Accept paid only the WAL
sync, which is why V4 measured B-B at 3.8 ms p99 (already over) and never saw the rest. With
leases live, `devtool bench-hotpath --iterations 2000 --warm-daemon` on `verify/v5` @ `0d5c999`
reports B-B p50 917.5 ms / p99 983.0 ms (n = 2064; `scratchpad/quiet/H1-bench-hotpath.txt`, power
state AC, processor performance 161 %, foreign load 4.8 %), while B-A (hook wall-clock) stays inside its 15 ms gate at p99 3.07 ms.
The diagnosis (workflow `v5-bb-diagnosis`, adversarially reviewed): the timed region is the whole
`Accept` closure — WAL append and fsync under `ingest.mu`, then the lease under the lock's mutex
(a position-sidecar re-read, the journal write and fsync, and `WriteAtomic`'s temp-file fsync) —
and the one-byte transport ACK is written only after `Accept` returns (`ipc/server.go`, dispatch
then `writeByte`). On this host a leased `Accept` costs about 37–40 ms of service time against
8–9 ms for the unleased control (a 4.7× ratio; three `FlushFileBuffers` per call, 65 % of the
profile, the position re-read another 28 %), so the row fails on service time alone; the 1 s p50 is
the queue behind the serialized `Accept` when 2 000 hooks arrive every ~39 ms into a server that
takes ~40 ms each. The reviewer's own harness runs on AC show the queue growing linearly with the burst
(300 hooks: p50 1.4 s; 1 000 hooks: p50 2.6 s, max 4.3 s) while the per-hook wall-clock stays at
~25 ms, and on a live daemon the same timed region also waits for the lock that `acknowledge`
holds during its own two syncs. The hook client never sees that ACK: `awaitACK` gives up at the shipped 8 ms
deadline and spools, and bench-hotpath's delivery reconciliation clamps the spooled duplicates
away, so B-A reads PASS on the daemon's receive timestamp alone. The budget row's own text ("daemon
read to WAL append returned") stopped describing `Accept` at `f6a8691` (WAL fsync, 2026-09-07) and
again at `9c023ac` (leases live); the committed bench baseline's `BenchmarkIngestAccept` rows
(2 µs, unsynced) predate both.

**Evidence.** `BenchmarkIngestAcceptLeased` (`internal/daemon/bench_test.go`, `0d5c999`): the
existing nonce-less `BenchmarkIngestAccept` priced only the WAL sync; the leased variant takes a
fresh lease per iteration through the real file-backed journal. On this host (AC, co-loaded, CPU performance 127–154 %; ratios meaningful, absolutes not): unleased 8.2–9.0 ms/op, leased 39.9–41.9 ms/op, single `WriteAtomic` 7.0–8.9 ms/op; `develop` @ `87c0c1d` unleased 6.4–7.4 ms/op (same single fsync). The benchmark fails itself unless the journal holds b.N + 1 leases afterwards, so a silent unleased fallback cannot pass it.

**Why it is carried.** The fix is a design decision, not a patch: either B-B is re-budgeted for a
path that now includes durable assignment (and the hot-path spec's 2 ms figure is corrected where
it is stated), or the lease's durability points move off Accept with a group-commit or a
synced-bytes admission rule redesigned to tolerate it — the trade the code comment refuses to make
silently. Both belong to the owner of the hot-path budgets with SP-20's author in the room, which
is V6-VERIFY's SP-17 hardening pass. V5 records the measurement and the gate red, as V4 recorded the
smaller breach, and does not lower the budget.

**Acceptance (V6).** A reference-platform and a Windows figure for `BenchmarkIngestAcceptLeased`,
a written decision between re-budget and redesign, and bench-hotpath's B-B row green or explicitly
re-gated against the decided figure.

## SP20-D2 — verify-on-read puts the store's read path over its budget

`deferred:V6-VERIFY`. `16ecc77` `fix(store): verify bounded object reads and
retain corruption evidence` (SP-20 M1, 2026-09-07) replaced `os.ReadFile` on the object read path
with `readBoundedObject` — `Lstat`, `Open`, `fstat`, `SameFile`, a `LimitReader` read — and made
`getObject` hash the plaintext (`core.HashBytes(core.DomainChunk, plain)`) against the requested
address on every read (`internal/store/objects.go`). Every read now pays two extra metadata
syscalls and one content hash of the chunk. On the AC quiet window of `verify/v5` @ `0d5c999`
(`scratchpad/quiet/E5-I-06.18.txt`, `-benchtime 2s`; the `devtool bench` sweep in
`scratchpad/quiet/v5-bench.txt` agrees): `BenchmarkGetChunk` 88.2 µs/op, 26 allocs/op against the
60 µs budget (I-06.18) and the wave-3 baseline's 44.5–45.7 µs, 18 allocs; `BenchmarkOpenSpan_4KB_of_4MB`
88.6 µs (150 µs budget, inside; baseline 46 µs); `BenchmarkSearch_1000Roots` 81.8 ms, 42.7 k allocs
against 25 ms (already 2× over at the baseline's 45–52 ms, 30.3 k allocs) because `Search` reads
every candidate chunk through the same `getObject` (`internal/store/search.go`). The base-clock confirmation (`scratchpad/quiet/E5b-store-count3.txt`, battery 46 %, processor performance 95 %, `-count=3`) reads 224–321 µs / 316–333 µs / 242–271 ms with the same 26 / 27 allocs/op: the added cost is syscall-bound and scales worse than the clock on battery. The
allocation columns do not depend on the clock, so this is the code, not the host;
`git diff 1e767c3..HEAD -- internal/store/read.go internal/store/objects.go` is the whole change on
that path. `devtool bench-compare` did not flag it: the sweep carries one sample per row and
benchstat refuses to call a single sample significant ("need >= 4 samples"), the blindness V2-VERIFY
recorded as its gates item 24 and the V5 report's §22 item 24 records again.

**Why it is carried.** Verify-on-read is a deliberate integrity decision of SP-20's remediation
(bounded reads, content-addressed verification, quarantine with evidence), reviewed and shipped
with its conformance log; making `GetChunk` meet 60 µs again means either trusting zstd's frame
checksum and the index length as SP-06 did (undoing the decision), caching verified reads, or
re-budgeting the row against the verified path. That is the same budget-versus-guarantee decision
SP20-D1 records for durability, owned by the same pass. V5 records the measurement, opens the row,
and lowers no budget.

**Acceptance (V6).** A written decision (re-budget, cache, or verify at publication only) with a
reference-platform and a Windows `BenchmarkGetChunk` figure, `BenchmarkSearch_1000Roots` judged
against whichever budget the decision states, and the bench baseline regenerated on a quiet AC
window with enough samples for the gate to compare.
