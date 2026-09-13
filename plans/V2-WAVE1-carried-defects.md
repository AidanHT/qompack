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
idle, `scratchpad/quiet/E5-I-06.18.txt`): PutBytes cold 15.74 ms / warm 5.65 ms
against 3 ms / 400 us. Cold allocations rose from 922 to 1 183 per op since the wave-3 baseline with
`internal/store/bench_test.go` unchanged, and warm is 20 % over V3's 4.7 ms, so the row is no longer a
pure host question (V5 report §22 item 31). No reference-platform figure could be taken on this host: the only WSL
distribution is docker-desktop (read-only filesystem) and the Docker daemon is stopped, both user
actions to change, and a pushed branch is the only other path to the Linux CI leg — outward-facing,
not taken under this checkpoint. The decision the V3 disposition named (budget revision versus
implementation change on the per-novel-chunk write path) is still open and is the same decision as
SP08-D1's; V6-VERIFY, whose SP-17 pushes to CI, takes both together with SP10-D1 and SP20-D2.

## SP05-D2 — the shipped ACK deadline is shorter than one leased `Accept`

`fixed` at the V5 close-out (2026-09-13; opened `deferred:V6-VERIFY`, see the disposition at the end
of this section). Found by V5-VERIFY fix item F4 while pinning F4-P1 (the
drain replay of acknowledged copies, fixed in `1c17f0d`). The hook client waits
`State.AckDeadlineMs` (shipped default 8, `internal/config/defaults.go`) for the daemon's one-byte
transport ACK and otherwise appends the request to `spool/client-<pid>.ndjson`
(`internal/ipc/client.go` `spoolAndReturn`). On this host, under the load of a full e2e session,
25 and 26 of 196 hook deliveries in the two `TestV3_LiveSessionWriteSetAndAppendOnly` runs whose counts
were saved (13 %; `scratchpad/rev2-x09-red.txt`, `rev2-x09-ctrl-nofallback.txt`) took that fallback
AFTER the daemon had leased and acknowledged them; the analysis of the other F4 runs was not saved. The B-B diagnosis (SP20-D1) then showed the general case: since `f6a8691`
the transport ACK is written only after `Accept`'s fsyncs, so whenever one `Accept` takes longer
than 8 ms — every leased `Accept` on this disk, and every `Accept` at all under bench-hotpath's
burst — the hook times out and spools, and bench-hotpath's delivery reconciliation hides the
resulting duplicates. With F4-P1 fixed each such copy is skipped at the next
drain; what remains is wasted spool I/O on the hook's hot path and a startup drain proportional to
it. No evidence test is named: the symptom is a rate under host load, and a deterministic pin needs
a fake clock inside the client's ACK wait, which is SP-17's hardening list. Resolution: measure the
fallback rate on the reference platform and either raise the deadline within budget B-B or make the
client's ACK wait independent of scheduler latency.

**V5 close-out disposition (2026-09-13): fixed.** The row closes on BOTH halves of the ordering it
complained about, and neither alone would have done it: the deadline was raised, and `Accept` was made
cheaper. Only the first is an arm the Resolution above names; the second is what makes the first
honest, and it is why the Resolution's own alternative could be left untaken.

- **The deadline was raised.** `AckDeadlineMs` is no longer the shipped 8 ms. `5d0b904` derives it
  from the same attested `bench-hotpath` runs the B-B limit rests on, as `L0IngestMs +
  ceil(slack99)` where `slack99` is the largest per-run `p99(hook_ack_rtt) - p99(B-B)` — 22.257 ms —
  and `cbfa3d3` carries it up with the widened limit. Windows ships **73 ms**
  (`AckDeadlineMsWindows`, `internal/config/deadlines.go`) against `L0IngestMsWindows` 50, and a new
  T30 anti-drift test pins `AckDeadlineMs > L0IngestMs` on all three platforms, since the derivation
  adds a positive slack. `internal/cli`'s `hookSendDeadlineFloor` (8 ms) deliberately does not move
  with it and now sits strictly below every platform's value, so it can only bind on a state record
  carrying no deadline at all.
- **And `Accept` was made cheaper.** SP20-D1's group commit and format-2 seal take a leased `Accept`
  from the 18.3–23.3 ms this section measured to a median **7.306 ms**
  (`BenchmarkIngestAcceptLeased`, `-count=6`, 6.751–7.765) — about 2.5 to 3 times cheaper in
  isolation. Raising the deadline over an unchanged service time would have bought only the quiet
  window; 73 ms is about ten times an uncontended `Accept`, and above even the 37–42 ms this section
  measured co-loaded on the pre-group-commit path.

B-B's own row is green at the new limit: three runs in attested quiet windows read p99 20.480 /
12.288 / 20.480 ms against 50 ms (SP20-D1's disposition below).

**Evidence.** The row now names `BenchmarkIngestAcceptLeasedBurst`
(`internal/daemon/delivery_bench_test.go`), which releases a whole burst of deliveries at once and
fails unless every one of them holds a lease and an acknowledgement. It is the instrument for the
regime this section called "effectively every hook in bench-hotpath's burst": what a burst contends
for is now the device rather than one mutex held across all of it, because `383f97b` took the
acknowledgement out from under `Lock.mu` into its own group-commit pipeline. Keeping a live
benchmark in the evidence column means a re-deferral is a one-field edit.

**Residual, carried rather than asserted away.**

- **The fallback rate itself was not re-measured.** What is measured is the two quantities whose
  ordering produced it — the deadline and the service time — not the 13 % under a full e2e session.
  The rate is a symptom of that ordering and the ordering is now inverted with room to spare, but no
  run on file re-counts it.
- **No reference-platform figure.** The Resolution's first clause — measure the fallback rate on the
  reference platform — is still unanswerable on this host, for the reasons SP06-D2's disposition
  gives. `AckDeadlineMsPortable` 17 and `AckDeadlineMsDarwin` 45 are seeded from SP20-D1 design
  §7.5's predicted table and are marked PROVISIONAL in `deadlines.go`, pending CI's bench-gate
  running the same protocol on `ubuntu-latest` and `macos-latest`. If those limits rise, the
  deadlines derived from them rise with them.
- **The ACK wait is still a wall clock.** The Resolution's other alternative — make the client's ACK
  wait independent of scheduler latency — was NOT taken: `ipc.Client` still waits a duration. The
  deterministic pin this section asked for therefore still needs a fake clock inside that wait and
  stays on SP-17's hardening list. The row closes because the deadline is now above the measured
  service time, not because the wait stopped depending on the scheduler.

## SP20-D1 — budget B-B cannot hold the leased delivery path's durability points

`fixed` at the V5 close-out (2026-09-13; opened `deferred:V6-VERIFY`, see the disposition at the end
of this section). Budget B-B (`l0_ingest`, gated, p99 < 2 ms, "daemon read
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
then `writeByte`). A leased `Accept` costs 18.3–23.3 ms of service time against 1.9–2.2 ms unleased on the quiet AC
windows (9.5–11.4×; E4, E11, E4b), and 37–42 ms against 8–9 ms co-loaded (4.7×; `scratchpad/f7c/`,
where three `FlushFileBuffers` per call take 65 % of the profile and the position re-read another
28 %), so the row fails on service time alone; the p50 near 0.9 s is the queue behind the serialized
`Accept` under the 2 000-hook burst. The reviewer's own harness runs on AC show the queue growing with
the burst (300 hooks: p50 1.4 s; 1 000 hooks: p50 2.6 s, max 4.3 s) while the per-hook wall-clock stays
at ~25 ms, and on a live daemon the same timed region also waits for the lock that `acknowledge`
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

**V5 close-out disposition (2026-09-13): fixed.** The decision this row demanded is taken and written
down, and it went BOTH ways rather than either: the durability points moved off the serialized
`Accept`, and what remained was re-budgeted from measurement rather than assumed. The design the work
was built against is now committed as
`plans/sdd/V4-SP-20-capture-storage-and-state-remediation/sp20d1-design-final.md` (`2a11461`), which
until then existed only in a session scratch directory although thirty files cite it. The one
acceptance item NOT met is the reference-platform figure; it is carried below with the provisional
limits it travels with.

- **Redesign, not only a re-budget.** `ebd86f6` adds a leader/follower group-commit queue, and each
  of the three durability points takes one: the ingest WAL (`2d3499a`), the delivery lease journal
  (`25c5b7e`) and the acknowledgement journal (`383f97b`, which also takes the acknowledgement out
  from under `Lock.mu`). The lease-position sidecar's `paths.WriteAtomic` — two of the original four
  fsyncs — is replaced by the A/B delivery seal: added unwired (`a02e89a`), read in either format
  (`98ff691`), wired (`effa28d`) and written in format 2 (`5d32859`), with an offline repair tool and
  its admin subcommand for a seal a downgrade left behind (`d1e2477`, `c2109d0`). Under
  `BenchmarkIngestAcceptLeasedParallel/64/sessions-4` a delivery now costs 0.0315 lease-batches and
  0.2 syncs — about 32 deliveries per batch and one sync per five accepted deliveries.
- **A Windows figure, and the components behind it.** `BenchmarkIngestAcceptLeased` measures a median
  **7.306 ms/op** (`-count=6`, 6.751–7.765) against the 18.3–23.3 ms this row recorded on the same
  host. Its components (`BenchmarkDeliveryLeaseComponents`, `BenchmarkDeliverySealComponents`): WAL
  `Sync` 2.243 ms, lease-journal `Sync` 2.197 ms, format-2 seal slot 2.305 ms, `checkFileV2`
  0.170 ms, `postSealIdentity` 0.089 ms — against the v1 path they replace, `sealWriteAtomic`
  6.371 ms plus `checkFileV1AfterSeal` 1.801 ms. Format 2 alone therefore saves about **5.6 ms per
  delivery** (8.172 against 2.564), and the three remaining flushes plus about 0.4 ms of non-flush
  work predict 7.2 ms for one uncontended delivery, which the 7.306 ms median confirms: two
  instruments, one cost.
- **A written decision on the budget.** `5d0b904` re-budgets `l0IngestMs` by design §7.5's M2
  protocol — three `devtool bench-hotpath --iterations 2000 --warm-daemon` runs, one process at a
  time, `L0IngestMs = roundup5(1.25 x P)` for `P` the worst per-run p99 — and `cbfa3d3` widens it to
  the measured tail: six more gated runs were taken at the committed 30 under a rule fixed before
  they ran, one failed at p99 36.864 ms inside a clean window, so `P` is re-derived over all fifteen
  runs (twelve attested) and the Windows limit is **50 ms**. The same commit corrects the
  `l0IngestMs` doc tag, which still read "daemon read to WAL append returned" — the region as it was
  before `f6a8691` — and regenerates `docs/config-reference.md` and the schema golden through their
  generators rather than by hand.
- **The B-B row is green, and was not re-gated away.** At the committed defaults, three runs inside
  attested quiet windows read B-B p99 **20.480 / 12.288 / 20.480 ms** against the 50 ms limit — PASS,
  PASS, PASS — with B-A and B-E_cpu green and no deferral or shortfall in any run. `9638b30` then
  applies the owner's Q3 ruling (design §7.6): under `--under-coload` the B-B row becomes
  reported-only, which is ADR 0010's second branch for a property that is intrinsically wall-clock and
  has no CPU clock to move to (B-B has no child process, `obs.ProcessCPU`'s 15.625 ms tick would read
  exact zero against a limit in the low tens of ms, and what inflates B-B is fsync — blocked time
  costing no CPU). `Gated: true` in `internal/obs/budgets.go` is untouched, the isolated lanes
  (`bench-gate`, nightly `bench-deep`, `timing`, `test-e2e`) still judge it, and
  `TestColoadYieldersAreJudgedInIsolation` stops that coverage disappearing silently.
- **No budget was lowered.** `l0IngestMs` went from 2 ms to 50 ms on Windows: a raise, against a
  measured region the 2 ms figure never described.

**Linux and darwin are PROVISIONAL.** Neither is measurable on this host, so `L0IngestMsPortable` 15 /
`AckDeadlineMsPortable` 17 and `L0IngestMsDarwin` 40 / `AckDeadlineMsDarwin` 45 are seeded from design
§7.5's predicted table and marked provisional in `internal/config/deadlines.go`, pending CI's
bench-gate running the same protocol on `ubuntu-latest` and `macos-latest`. §7.5's own Windows
prediction was `P` = 12–18 ms against the 36.864 ms measured here, so both rows may well have to rise;
`4043b6a` makes bench-gate upload its hot-path JSON even when the gate fails, precisely so a first
breach still reports the numbers needed to re-price them. That is also the acceptance item this
disposition does not close: there is still no reference-platform `BenchmarkIngestAcceptLeased` figure.

**What a green B-B is now worth, stated rather than assumed.** 50 ms is nearly seven times an
uncontended `Accept`, and none of that gap is slack in the delivery path, which has a ±7 % spread. It
is contention from the harness's own 2 000 process spawns, whose floor alone is p99 77 ms. B-B is a
coarse backstop that catches a several-fold regression and cannot see a 2× one. The sensitive
instrument is `BenchmarkIngestAcceptLeased`; the durability invariant is carried by the design's
co-load-immune structural gates T9, T10 and T14 — `TestDeliveryJournal_BatchCommitsOneWriteOneSyncOneSeal`,
`TestDeliveryJournal_NoLeaseReleasedBeforeItsBatchSeal` and
`TestDeliveryJournal_CheckRunsAfterEvaluationAndImmediatelyBeforeAppend` in `internal/daemon`, which
run in every lane and have no clock in them at all. Gating the benchmark is the V6 follow-up
`cbfa3d3` names.

**Downgrade.** A binary from before `98ff691` reads only v1, so it cannot read a seal this build
writes. The rollback is the constant, never a `git revert` (`3fa6bd6`): setting
`deliverySealWriteFormat` back to 1 is a one-line change that makes this code write v1 again while it
still reads both, where reverting the flip commit would retire the format-2 corruption coverage in
the same move. `qompack admin delivery-seal` (`d1e2477`, `c2109d0`) converts a seal offline for a
binary that reads only v1; a failed conversion signals rather than falling silent (`cb8f984`), and a
conversion whose consent record was lost is refused (`e7f9ec0`).

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
88.6 µs (150 µs budget, inside at turbo; baseline 46 µs, allocs 19 → 27); `BenchmarkSearch_1000Roots` 81.8 ms, 42.7 k allocs
against 25 ms (already 2× over at the baseline's 45–52 ms, 30.3 k allocs) because `Search` reads
every candidate chunk through the same `getObject` (`internal/store/search.go`). The base-clock confirmation (`scratchpad/quiet/E5b-store-count3.txt`, battery 46 %, processor performance 95 %, `-count=3`) reads 224–321 µs / 316–333 µs / 242–271 ms, all three over their budgets, with the same 26 / 27
allocs/op where recorded: the added cost is syscall-bound and scales worse than the clock on battery. The
allocation columns do not depend on the clock, so this is the code, not the host;
`git diff 1e767c3..HEAD -- internal/store/read.go internal/store/objects.go` is the whole change on
that path. `devtool bench-compare` did not flag it: with one sample per row benchstat's test has
almost no power (it called 6 of 311 rows significant), the blindness V2-VERIFY recorded as its gates
item 24 and the V5 report's §22 item 24 records again.

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

## SP20-D3 — a delta side record shared across roots breaks exact recovery for all but the first

`fixed` at the V5 close-out (2026-09-11; opened `deferred:V6-VERIFY`, see the disposition at the end
of this section). Found by V5-VERIFY section-4 row 4.17 (`TestV5_AdmissionExtension`) while its
author wired the resolver to `RestoreOriginal`; recorded in `plans/sdd/V5-VERIFY/x17-disposition.md`
("Findings in `internal/store`") and carried here at the independent review's request.

**Symptom.** `putSideRecord` (`internal/store/put.go`) content-addresses a delta side record over
`marshalDeltas(deltas)` alone and returns the existing record when that address is already known. Two
content roots whose canonicalization removed the same volatile token at the same offset — two tool
outputs with the same timestamp prefix and different bodies — therefore share one side record, and its
declared base is the FIRST root. The second root's `PutBytes` reports `FidelityExact`; its
`RestoreOriginal` then fails with `ErrDeltaCorrupt` ("delta X declares base A, not B") and reports
`FidelityCorrupt`. GC retains the shared record through one base only. This is an SP-20 invariant 6
exactness claim that cannot be honoured on read; the 4.17 author reproduced it (`x17-run2.txt`).

**Reach.** Latent on the shipped tree: `RestoreOriginal` (`internal/store/lifecycle.go`) has no
production caller, so no enabled surface reads a side record back. The records themselves are written
on the ordinary put path (`admitRecovery`, then `putSideRecord`) and persist, so a consumer added later
inherits the defect for every root written before the fix.

**Related, recorded but not a row.** When canonicalization changes nothing, `admitRecovery` reports
`FidelityExact` with no side record, and `RestoreOriginal` labels the same bytes `FidelityCanonical`;
`storedFidelity` has the same shape for a dedup hit. The bytes are right and the read label
under-claims, the safe direction. The V5 report's §22 records it with this row.

**Evidence.** None named. The 4.17 test gives every payload its own timestamp seconds value precisely
so it does not trip this, and no pin test was written: the fix changes side-record identity and GC
coupling, which is the store owner's decision. The row carries evidence `-` like SP05-D2, and V6's
first step is a failing characterization test with two roots that share a removed token.

**Acceptance (V6).** Side-record identity includes the declared base (or records are keyed per root),
GC retains a record for every base that declares it, the characterization test restores both roots
exactly, and put and read report the same fidelity for the same bytes.

**V5 close-out disposition (2026-09-11): fixed.** Every acceptance item is met on `verify/v5-final`
(branch `v5/sp20-d3`, merged in `a3b4687`); the evidence is `internal/store/recoveryidentity_test.go`.

- **Identity includes the base** (`48770c3`). A delta record's payload is `{"base":…,"deltas":[…]}`,
  so each base gets its own record; a known address is reused only when it holds a delta record that
  declares the same base, and anything else there makes the put keep the full original instead
  (counted in `store.delta.addressTaken`). `ReadDelta` reads both payload shapes and refuses a payload
  whose base disagrees with the index line's.
- **Both roots restore exactly:** `TestPutBytes_SharedVolatileTokenRestoresBothRootsExactly`, the
  characterization the row asked for (red on `5708f38`).
- **GC per base:** `TestGC_EachBaseRetainsItsOwnDeltaRecord`, in both directions; with one record per
  base the existing base/record coupling needed no code change.
- **Put and read agree:** a KeepRaw put that canonicalization did not change writes `"verbatim":true`
  on its roots line and reads back exact (`TestPutBytes_PutAndReadFidelityAgree`,
  `TestPutBytes_VerbatimClaimPersistsOnAVersionOneLine`; the line stays `v=1`). `storedFidelity` is
  replaced by `dedupFidelity`, which claims exact or full for a dedup hit only when the stored record
  is provably this put's own (`TestPutBytes_DedupHitNeverClaimsAnotherPutsOriginal`); the fix found
  that a second input differing only in its timestamp used to be labelled exact while restoring the
  first input.
- **Concurrent puts of one root** are serialized so each claims only the original it restores
  (`6ab54f6`, `TestPutBytes_ConcurrentPutsOfOneRootClaimOnlyTheOriginalItRestores`), and a put whose
  context expired while it waited for that lock refuses instead of writing (`50ee228`,
  `TestPutBytes_RefusesAContextThatExpiredWhileItWaitedForItsPutLock`).

**Compatibility.** No golden changed. Records written before the fix (bare delta array) still read. A
root written before the fix that points at a shared record cannot be repaired (the index is
append-only): its read returns no bytes with `FidelityCorrupt`, never exact, and GC keeps it with the
record and the record's owner (`TestRestoreOriginal_PreFixSharedRecordIsNeverExact`). A put without
KeepRaw that deduplicates onto a root stored with a delta record now reports canonical where it used
to report exact; the only such caller, the migration import, never reads the label. **Rollback:** a
binary from before `48770c3` expects a bare-array payload, so it reads every delta record written since
as `FidelityCorrupt` with no bytes. Canonical bytes and every content root still read; only the
exact-original recovery of roots put since the fix is lost, in the safe direction, and rolling forward
restores it because nothing is rewritten. The `"verbatim"` key is forward-compatible (an older reader
drops it and under-claims canonical).

## SP09-D1 — negknow's `Open` budget holds on this host only at turbo clocks

`deferred:V6-VERIFY`. Opened by V5-VERIFY at its independent review's request (ruling Q28 in the V5
report). `internal/negknow` `TestBudget_Open` asserts §11.2's budget for `Open`: 300 ms CPU/op, best of
three attempts. On this host it passes at turbo clocks and fails without them:

| run | probe | `Open` CPU/op | file |
|---|---|---|---|
| F1 on `0d5c999`, alone | AC, processor performance 225 % | PASS (no figure logged) | `scratchpad/quiet/F1-ci-timing.txt` |
| F1b on `1a1bbaf`, alone, `-count=3` | AC, 120 % | 257.8, 268.8, 273.4 ms | `scratchpad/quiet/F1b-negknow-count3.txt` |
| B1 on `b64b3f6`, serial whole tree | battery, throttled: the resume2 pass's probes read 55–60 %, none taken during B1 itself | 585.9, 609.4, 632.8 ms (FAIL) | `scratchpad/quiet/B1-wholetree.txt` |

It failed on the pre-wave-4 tree too (`scratchpad/m2-negknow-premerge.txt`), so it is not a wave-4
regression. CPU/op scales with the clock (273 ms × 120/55 ≈ 596 ms), so the budget is met here only at
turbo, the pattern for which SP10-D1 stays unresolved; `TestBudget_DetectorScan` has the same shape
(2.6–3.6 ms at turbo, 6.1–6.7 ms throttled, against 5 ms). The first draft of the V5 ruling closed this
as co-load; the serial B1 run shows it was the clock.

**Resolution (V6).** A reference-platform figure from CI's `timing` job, then either a budget stated
against the platform and clock it was written for, or a real speed-up of `Open`.

**V5 close-out note (2026-09-13): the second arm has largely happened; the row stays open on the
first.** The negknow speed-up merged at `eaef177` — each record's bloom keys derived once at `Open`,
the detector scanned through an index view — moves `BenchmarkOpen` to **90.4 ms CPU/op** measured on
this host at 161 % processor performance, against the 258-273 ms this section records at 120 %.
Normalised to one clock that is about 2.2x faster. `BenchmarkDetectorScan`, whose shape this section
calls the same, is now 789 us CPU/op against its 5 ms budget.

That is **not** enough to close the row, for two reasons, and both are the row's own.

First, the resolution asks for a *reference-platform* figure, and none exists: every number here and
above is from the same Windows developer host. CI's `timing` job on this branch's push is what
supplies it.

Second, the scaled arithmetic is too thin to stand in for a throttled measurement. Using this
section's own scaling law (CPU/op varies with the inverse of the clock), 90.4 ms at 161 % implies
about 265 ms at the 55 % this section measured 586-633 ms at — inside the 300 ms budget, but by 13 %,
and on an estimate rather than a measurement. A 13 % margin derived by scaling is exactly the kind of
number the B-B re-budget in §31.3 was caught out by: three runs there gave a limit whose margin over
the worst observed sample was 4.6 %, and the fourth run exceeded it. `DetectorScan` scales to about
2.3 ms against 5 ms, which is comfortable; `Open` does not, and it is `Open` this row is about.

## SP20-D4 — the delivery journals never retire a lease, so leasing stops for good at 65,536

`deferred:V6-VERIFY`. Found at the V5 close-out (2026-09-11) while designing SP20-D1's group commit,
which rewrites the same journal code. `internal/daemon/delivery_lease.go` bounds
`state/delivery-leases.jsonl` and `delivery-acks.jsonl` at `deliveryLeaseMaxEntries` = 65,536 entries
and `deliveryLeaseMaxBytes` = 64 MiB each. Its comment calls them admission safety bounds and leaves
"measured retention/compaction" to "a separate migration task" that no plan owns. Only the daemon
writes either file, and it only appends; store GC (`internal/store/gcrun.go`) only reads them. No
lease is ever retired, even for a delivery acknowledged long ago, so once a project has leased 65,536
deliveries, `lease` refuses every new one with `ErrBudget` for the life of the project. The byte
bound cannot bind first: a lease line is about 309 bytes, so 65,536 of them are about 20 MB.

**Symptom.** A delivery past the cap is not dropped; it loses its durable identity.

- **Still ACKed.** It is WAL-appended and synced, `dispatchOp` answers OK, and the hook client sees
  success.
- **Counted.** `ingest.leaseDelivery` counts it `l0_delivery_unleased` and logs a warning.
- **No identity.** The observer runs with an empty `ObservationID`, so no capture sidecar is written
  and no frontier record is committed. Dedup falls back to a hash of the wire line held in process
  memory.
- **Drain.** The drain is refused a lease for the WAL or spool copy too and records
  `DrainGapUnleased`, so `DrainGaps().Complete` is false. After a restart it dispatches the copy a
  second time, where below the cap the frontier would have skipped it (F4-P1).

A retry of a delivery already in the journal still gets its original lease back, because the lookup
runs before the cap check.

**Reach.** Leases are taken live by `observe.tool` (every PostToolUse), `observe.prompt` (every
UserPromptSubmit) and `observe.stop` (every Stop and SubagentStop), and at drain time by any spooled
line that carries a nonce, which the hook client mints for all six hooks. That is roughly one lease
per tool call. At the hot-path benchmark's model of a realistic session (2,000 tool uses,
`plans/00-ARCHITECTURE.md`), the cap is about 31 sessions: weeks for one active developer, days under
multi-agent use. A project at the cap also pays about 0.7 s on this host at every daemon start to load
and re-validate the two journals (measured on the evidence test's fixture).

**Evidence.** `TestCarriedDefect_SP20D4_LeaseJournalRefusesEveryDeliveryPastItsEntryCap`
(`internal/daemon/delivery_lease_cap_test.go`, `68176e6`). It writes 65,536 acknowledged leases with
the journal's own encoding, chain and position seals, opens them through the real
`openDeliveryJournal`, and drives a fresh delivery through `dispatchOp`, the ingest worker and a
post-restart drain. The cap is spelled as the shipped value, so the negative control (the constant
doubled in a scratch copy) fails at the refusal assertion.

**Why it is carried.** Retention changes what the journals promise. A retired lease must still answer
a late copy of its delivery, such as a hook's fallback spool line drained days later; otherwise the
frontier's redelivery dedup is lost and the drain publishes that copy a second time. The fix must also
be crash-safe across two files and coupled to store GC's retention roots, and it rewrites the code
SP20-D1 is rewriting at the close-out, so it cannot land safely beside that change.

**Acceptance (V6).**

- Both journals retained or compacted together (`loadAcks` refuses an acknowledgement that names no
  surviving lease), with a horizon that keeps redelivery dedup for every copy that can still arrive,
  for example a per-session arrival watermark that answers "already delivered" for a retired delivery.
- A crash-safe compaction that leaves both journals consistent at every cut, with GC's lease and ack
  roots updated to match.
- The evidence test inverted: a project past 65,536 leases leases its next delivery, and a late copy
  of a retired delivery is still skipped.
- The startup load cost bounded independently of the project's age.

## SP20-D5 — a drain pass lowers the durable bound its own record already holds

`fixed` at the V5 close-out (2026-09-13). Opened and closed inside the same close-out: the defect was
introduced by this close-out's own drain work — `cdf7829` made a pass record the durable bound it read
to, `4e7218f` floored that at the offset the pass consumed — and fixed five commits later, so it was
never on `develop` and was never carried open. It gets a row anyway. A truncation-refusal defect that
reached this branch's delivery path is exactly what a reader months from now needs to be able to find
by id, and the fix leaves a residual that ships with the code.

**Symptom.** A pass recorded `max(the durable bound it read to, the offset it consumed)`. On the pass
after a held segment is reopened — `ingest.holdSynced` enters a reopened segment into `ingest.synced`
at 0, so `durableEnd` answers less than the record already held — the recorded size FELL, from the
durable bound to the consumed offset. Every byte between them was durable when it was recorded and the
record stopped naming them, so `validateProgress` stopped refusing when they went missing: a
truncation the base before this work refuses drew no refusal at all, the entry flipped to `Done` at
the shortened size, and a shorter foreign line written over those bytes was delivered as the segment's
own continuation.

**Why it was invisible.** The two tests that already covered this code moved a held segment's synced
size only upward, so neither could see a bound that FALLS. That is the axis the wedge lived on, and it
is why the whole package, the race counts and the carried-defect pins were all green over it
(`510bfc3`).

**Fix.**

- **The floor is the bound the record already holds** (`9d1ef2d`). It is lowered only for a record no
  pass of this code wrote, whose `Size` is an older raw stat that may name a held segment's unsynced
  tail — keeping THAT would inherit a bound a legitimate machine crash falls below, which is the wedge
  this work exists to fix. Such a record is floored at the consumed offset, as before, and is upgraded
  to a marked record the first time this code writes it.
- **An additive `durable_size` mark** carries the provenance. It is written positively, so its ABSENCE
  means "this size may be a raw stat" — which is what every record older than the mark is. In Go it is
  carried as its negation, `drainFileState.SizeIsRawStat`, so the zero value is a record this code
  wrote. An older binary ignores the field (its `loadState` is a plain `json.Unmarshal`) and drops it
  again when it rewrites the record, returning the record to that binary's own meaning.
- **`Done` now also requires `Size == Offset`** (`9d1ef2d`). The floor can hold `Size` above the stat a
  pass saw, for a file that shrank between `validateProgress` and that stat, and `Done` set from the
  consumed offset alone wrote `{Done, Offset < Size}` — a record `loadState` refuses outright — and
  handed the file to `removeCompletedFile`, which unlinks it at a size below the durable bound its own
  record names.
- **The mark is set only where the pass's own durable bound carries the recorded `Size`** (`a12bae9`).
  The unconditional clear wrote the mark over a `Size` resting on the offset floor, which froze a
  non-durable claim: the next pass floors at `fs.Size` for a marked record, so that bound could never
  be lowered again. Such a record now keeps its provenance, stays lowerable, and is marked by the first
  pass whose own bound reaches the offset it names; a marked record was already durable and stays
  marked.

`validateProgress` is unchanged in code, and no assertion anywhere was relaxed to land this.

**Evidence.** `TestDrainKeepsTheDurableBoundItRecordedWhenASegmentIsReopenedBelowIt`
(`internal/daemon/drain_recorded_bound_test.go`, `ea6d140`) is the row's named pin: a NAKed dispatch
leaves `{Size: bound, Offset: below it}` on a held segment whose every byte is durable, the segment is
then reopened so its synced size answers 0, and the size the next pass persists must still be the
bound — with a truncation past that bound still drawing the refusal. Four pins travel with it:
`TestDrainDoesNotKeepARawStatBoundItDidNotRecord`,
`TestDrainWritesALoadableRecordWhenAFileShrinksUnderThePass` (`ea6d140`),
`TestDrainDoesNotMarkASizeThatRestsOnALegacyOffset` and
`TestDrainKeepsTheProvenanceOfARecordItDidNotVisit` (`cfb885e`), plus
`TestDrainKeepsItsProgressLoadableWhenAStragglerReopensADrainedSegment`
(`drain_reopened_segment_test.go`, `510bfc3`), which drives the bound downward through the production
seams — a real ingest, its own `syncedWAL`, `holdsWAL` and `removeDrainedWAL` — rather than a seeded
record. Every negative control ran in a `git archive` scratch copy, never the worktree, and each had to
produce a real failure signature: the offset floor is caught by the first pin, the unconditional
`max(end, fs.Size)` floor by the second pin's pre-mark case ALONE, dropping `fs.Size > fi.Size()` from
`validateProgress` by both plus the existing twin, and `Done` from the consumed offset alone by the
third pin alone. None of the six uses a clock, a sleep or a goroutine ordering.

**Residual, stated rather than asserted away** (`762c8cc`, comment only).

- **A downgrade surrenders the protection for one pass.** The record an older binary rewrites is
  byte-identical to a genuine pre-mark record, so the next pass of this code reads it as a raw stat,
  floors at the consumed offset, and lets the recorded bound fall to that offset the first time a
  reopened segment answers a lower `durableEnd`. The truncation window the mark closes is open again
  for that record until a bound of this code's own reaches its offset and re-marks it. The alternative
  — trusting an unmarked size — is the wedge the mark exists to prevent, so the cycle is accepted at
  that price rather than cured.
- **An inherited offset is not itself a durable bound.** A binary with no `durableEnd` (`develop`'s
  `drain.go` has none) read a held segment to EOF and recorded what it consumed, so its offset can name
  bytes no `Sync` ever returned for. `loadState` forbids a `Size` below `Offset`, so no floor this code
  can choose cures that residual: the crash that takes those bytes wedges the spool for that one
  record, as it did for the binary that wrote it.
  `TestDrainDoesNotMarkASizeThatRestsOnALegacyOffset` pins that residual as what it is, and pins that
  the pass must not mark such a size durable.
- **The mark does not always rest on a `Sync` of this code's own.** `durableEnd` returns the stat
  without syncing for a file with nothing unread (`size <= offset`), so an inherited record whose
  `Offset` already equals the stat is marked durable with no `Sync` of this code behind it. That case
  is reachable only where `Size == Offset`, and `validateProgress` compares both against the stat, so
  the refusal is identical either way — which is why it costs nothing, and why the comments now say
  what the code does instead of claiming the pass synced to that bound.
