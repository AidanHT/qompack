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

`fixed` at the V6 close-out (C1.10, 2026-09-22) by segmented rollover, enabled by default; see
**Resolution** at the end of this section. It was `deferred:V6-VERIFY` until then, and the text down
to the resolution is the defect as it was found. Found at the V5 close-out (2026-09-11) while
designing SP20-D1's group commit, which rewrites the same journal code.
`internal/daemon/delivery_lease.go` bounds `state/delivery-leases.jsonl` and `delivery-acks.jsonl`
at `deliveryLeaseMaxEntries` = 65,536 entries and `deliveryLeaseMaxBytes` = 64 MiB each. Its comment calls them admission safety bounds and leaves
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

**Resolution (V6 close-out, C1.10, branch `closeout/rollover`).** Nothing is retired: the journals
are segmented instead. Each segment's two files keep the 65,536-entry and 64 MiB bounds, and reaching
either rotates the journal — the outgoing window (leases, acknowledgements, terminal dispositions) is
archived into the generation store, a Merkle radix keyed by nonce, arrival, acknowledgement,
terminal and per-session frontier, and the switch is one fsynced record in the segment authority's
chained log. Owner decision D2 (2026-09-22) was to finish the rollover gates and then enable it by
default; `enableDeliveryGenerations` is now `true`. Against the acceptance above:

- *Horizon.* Every lease ever admitted stays resolvable, so a late copy of any retired delivery gets
  its original identity back and is skipped; a dormant session's next arrival follows its last one.
- *Crash safety and GC.* The window is archived before the transition commits; an open that finds a
  window already archived finishes that rotation first. Each rotation stages, with the new segment,
  a carried-lease file naming every archived lease that has no acknowledgement, and store GC reads
  the active segment's journals and that file — never an archived segment's journals — so a pass is
  bounded by the active window and the carried leases, not by the project's history. A GC pass run
  at every step of a rotation keeps every root an unsettled lease references, and one a rotation
  moves the authority under halts and deletes nothing
  (`TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot`,
  `TestDeliveryReaders_V6_GCDuringLiveRotationsKeepsEveryArchivedUnsettledRoot`; negative controls:
  harvesting segment 0 only, run 06, and ignoring the carry, run 24, each fail).
- *Evidence test inverted:*
  `TestCarriedDefect_SP20D4_CaptureContinuesPastTheOldEntryCapAcrossRestart` (was
  `..._LeaseJournalRefusesEveryDeliveryPastItsEntryCap`) drives a store at exactly the old cap
  through `dispatchOp`, a rotation and a restart, and a late copy of an archived delivery is not
  published again.
- *Startup cost.* Open loads the active segment's journals (bounded by the entry cap), the authority
  and the generation head; archived segments are not re-read, and segment 0's frozen seals are
  checked by size only.

**Why it could not simply be switched on.** At `cf31e01` the segmented journal existed behind a
default-off seam. Each of these failed a test or probe before its fix (red runs under
`plans/sdd/V6-closeout/rollover/runs/`, real-binary drills under its `drill/runs/`):

1. The generation store wrote one file per radix page (file and directory fsync each) and was
   committed inside every lease and acknowledgement batch, so inside `ingest.Accept`. A 200-delivery
   probe measured 1,853.6 ms per lease and 1,100.1 ms per acknowledgement on Windows (5.8 / 5.4 ms
   with the seam off) and 1,695.8 / 1,652.3 ms on Linux (54.2 / 31.6 ms), 49 files per delivery; one
   400-lease commit took 1,354 s. Fix: pages packed one file per generation plus a root pointer
   (`52c77b6`), the window archived at rotation instead of per batch (`3200254`), and a one-pass
   archive with a branch-page cache (`32602a4`: a full-window rotation 135 s -> 27 s on Windows).
2. After a rotation the legacy files stay as segment 0 with seals a pre-segment build still accepts,
   so that build appended and re-numbered arrivals. Fix (`26bbd3f`): archiving segment 0 rewrites its
   two seals as frozen documents with no `"v"`, which every pre-segment reader refuses.
3. A torn active-segment seal had no repair on a segmented store. Fix (`7577fde`): Rule R
   (`--accept-torn-slot --yes`) reaches the active segment only; an archived segment's seal cannot be
   torn by a crash.
4. A store that had never rotated failed its own offline check (empty generation store). Fix
   (`2e99fdd`).
5. Concurrent callers read a second rotation as `ErrBudget` (found by the first drill). Fix
   (`8509447`): a rotation signal names the segment it filled, and the retry rotates past exactly it.

The close-out's adversarial review of the flip found three more, fixed before the row was closed
(`plans/sdd/V6-closeout/rollover/`, runs 20-33):

6. Every store GC pass read every committed segment's journals and capped the acknowledgements it
   folded at 65,536 pairs oldest first, so from the first rotation on the leases of every later
   segment read as open: a pass's work and memory grew with the project's history (review finding 1;
   `TestGCSegments_AckedArchivedLeasesAreReleasedPastTheAckSetBound` fails on the old harvest, run
   22). Fix (`247c87d`, `d7ef6b5`): the carried-lease file above, and a harvest bounded by it.
7. A full-window rotation stalled leases and acknowledgements for 25-125 s. The review read it as
   hashing; a profile showed positioned reads of committed pages (45% of the time, hashing under
   3%), because the window was written key by key in (session, arrival) order, random in the radix's
   key space. Fix (`6a7621c`): the window is planned in memory and merged into the tree in key
   order, each page read and written about once; five successive full windows archive in 1.5-6.0 s
   instead of 12.3-97.6 s on Windows, and in 2.0-5.7 s on Linux
   (`BenchmarkDeliveryRolloverArchiveWindows`, runs 20, 21 and 31).
8. Segment 0 was frozen only after the transition to segment 1 committed, so a crash in between left
   a pre-segment build free to append to it (review finding 3; run 23). Fix (`35fefe8`): segment 0
   is frozen before the commit, and an open that finds it active under frozen seals over an archived
   window finishes the rotation.

The thresholds did not move (`deliveryRolloverEntries = deliveryLeaseMaxEntries`, 64 MiB bytes) and
no configuration key exposes them.

**Criterion changes (old -> new, why).**

- The evidence test, as above: refusal past the cap -> capture continues past it, because that is
  this row's acceptance.
- `TestDeliveryGenerationWiring_*` (two tests): "an admitted lease / acknowledgement is mirrored into
  the store" -> "absent before its segment rotates, exact after it; an acknowledgement of an archived
  lease advances the frontier at once", because per-batch mirroring was the cost removed in fix 1.
- The group-commit twins at the entry and byte caps and the ack check-order case at the entry cap
  now run with rollover off: the refusal they pin still exists only for a journal that cannot rotate.
- `TestBackupWatchedFiles_NamesTheDeliveryStateThisPackageWrites` expects the segment authority's
  head and log too: store already watched them.
- The segmented offline-check negative control loads segment 0 against its frozen position, since
  the legacy seal reader now refuses it by design (fix 2).
- The generation corruption test reopens the store before the corrupted read, since the branch cache
  serves an already-verified page from memory; the refusal it asserts is unchanged.
- Fixtures meaning "a store written before segments" are built with rollover off (fix 4).
- `TestGCSegments_HarvestsOldestLeaseAcrossRotation` and `..._AckInLaterSegmentSettlesOlderLease`:
  "GC reads segment 0's journal and retains its open lease" -> "segment 1's carried-lease file names
  the lease and GC retains it through that file", because GC no longer reads archived journals (fix
  6); the retention asserted is unchanged. Store segment fixtures past 0 carry an empty file by
  default.
- The segment staging tests: a staged segment is five files, and one carrying a different carry is a
  conflict (an added refusal).

**Recorded decisions this changes.** GC's reader no longer harvests every committed segment
(`gc-segment-integration-work.md` Part B, `delivery-segment-retention-decision.md`): it harvests the
active segment and the carry, with the same exact acknowledgement join. A segment past 0 is five
files, not the four the retention decision names. The rotation order gains two steps (the carry is
staged with the segment; segment 0 is frozen before the commit, not after it). The coordinator
adjudication's "operator stop/backup boundary" before writer enablement
(`delivery-rollover-main-adjudication.md`) is superseded by owner decision D2 (enable by default):
the first rotation happens on its own, and `docs/backup.md` tells operators to take a backup before
a project first reaches 65,536 deliveries if an older build may be needed again. The integration
contract's per-batch lease and ack mirror and its "store is a superset of the active window"
(`sdd/V6-remediation/delivery-capacity-integration-work.md`, Wiring 1 and 3) are replaced by
rotation-time archival; the join that superset protected is made against the in-memory window, and
at rotation against the lease just archived. The rotation order in the same document is kept, with
the two steps named above added. The old-reader barrier is the existing parsed position seal, as the
coordinator decision requires. Generation format v1 (only ever written by tests) is refused.

**Evidence.** Focused tests: `TestDeliveryRollover_*` (rotation-time archival, frozen seals,
concurrent rotation), `TestDeliveryGeneration_*` and `TestDeliveryRadix*` (packs),
`TestDeliverySealSegment_RuleR*`,
`TestDeliveryReaders_V6_GCDuringLiveRotationsKeepsEveryArchivedUnsettledRoot` (negative control
`gc-negative-control.sh`, run 06), and from the review round `TestDeliveryRadix_Merge*`,
`TestDeliveryRollover_Archive*`, `TestDeliveryCarry_*`, `TestDeliveryRollover_Carry*`,
`TestGCSegments_*Carr*` and `..._AckedArchivedLeasesAreReleasedPastTheAckSetBound`,
`TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot` (negative control
`gc-carry-negative-control.sh`, run 24),
`TestDeliveryRollover_SegmentZeroIsFrozenBeforeItsTransitionCommits` and
`..._CrashBetweenFreezeAndTransitionFinishesTheRotation`,
`TestDeliveryReaders_V6_BackupRestoresHistoryAndAcceptsLaterWrites`, and the frozen-seal tests in
`internal/cli` and `internal/store`. Whole packages: daemon on Windows (runs 02, 07), daemon, store
and cli on Linux (runs 08, 12), store and cli on Windows (run 11); the rollover and frozen-seal
tests under `-race` on Linux (runs 13b, 13c). Real binaries (`rollover-drill.py`, drill3, Linux): a
real daemon with its threshold patched to 3 rotated to segment 6 with dense arrivals and every
capture indexed, as a never-rotated control did; the current build's seal check certified the
rotated store's delivery state without changing it; backup, verify and restore preserved the
delivery state byte for byte and the restored copy's delivery state certified and continued densely
after restart; the pre-segment 301a8e9 build changed no delivery-state file and was refused by fsck
and the seal check, and the current build then continued the history at its next arrival. Drill 4
(`drill/runs/20260923-drill4`, at `b30908c`, after the review fixes) repeated the drill with the
carried-lease files and the freeze-before-commit order: every verdict PROVEN, `backup restore`
exited 0, and every current-build `fsck --seal-check` — the rotated store, the restored copy before
and after its restart, and the copy the old build had run on — exited 0 on every row. In drills 2
and 3 "certified" meant the delivery row only: there every `fsck --seal-check`, the never-rotated
control's included, exited 1 on the `captures` and `publication` rows (the base's SessionEnd flush
defect below), and `backup restore` exited 1 on its integrity check. Drill 4 did not reproduce that
defect, and no capture or publication code changed between drill 3 (`2e119fe`) and drill 4, so it is
intermittent rather than fixed: gate 2's end-to-end certification rests on drill 4 and should be
re-confirmed once that defect's own fix lands.

**Resource cost (gate 5).** `BenchmarkDeliveryRolloverResourceCost` at the production thresholds:
210,000 deliveries leased and acknowledged through the real journal by 8 concurrent callers (each
acknowledging its previous delivery after leasing the next), three rotations, and since the review
round a store GC pass at every 50,000-delivery checkpoint beside the running callers. Figures only:
nothing was gated on them and no budget changed. The runs below are at the review round's head
(Windows `856ab94`, run 27; Linux `60d9846`, run 30; the code under test is the same). Both hosts
were co-loaded (the Windows host is shared with other agents, and the container runs on it), so
tails are upper-biased. The implementer's runs before the review fixes are 10b and 16; their
rotation stalls were 27.4 / 124.5 / 86.2 s on Windows and 28.1 / 24.8 / 35.7 s on Linux.

| | Windows 11, 22 logical CPUs (run 27) | Linux container, `GOMAXPROCS=4` (run 30) |
|---|---|---|
| rotation stall, windows 1 / 2 / 3 | 3.9 / 5.1 / 4.5 s | 2.3 / 3.5 / 6.8 s |
| lease p50 / p99, whole run | 21.2 / 68.7 ms | 32.3 / 63.9 ms |
| lease p99 before / after the first rotation | 84.4 / 52.3 ms | 71.2 / 49.0 ms |
| acknowledgement p50 / p99 | 21.3 / 63.1 ms | 33.7 / 64.3 ms |
| peak Go heap in use (sys) | 235 MiB (288 MiB) | 224 MiB (273 MiB) |
| live heap after GC at 50k / 100k / 150k / 200k | 24 / 37 / 33 / 28 MiB | 24 / 37 / 32 / 29 MiB |
| store GC pass at 50k / 100k / 150k / 200k / 210k | 0.87, 0.43, 0.31, 0.14, 0.16 s | 0.29, 0.21, 0.12, 0.05, 0.09 s |
| heap rise during a GC pass, at most (callers included) | 52 MiB | 55 MiB |
| GC passes that completed | 5 of 5 | 5 of 5 |
| generation store after rotation 1 / 2 / 3 | 79 / 175 / 281 MiB | 79 / 175 / 281 MiB |
| journals, all segments, at 210,000 | 114 MiB | 114 MiB |
| delivery state per 100,000 deliveries, at 210,000 | 188 MiB | 188 MiB |

The key-ordered archive also publishes fewer intermediate generations (58-59 for three windows where
the per-key archive published 142), so the generation store is about half its earlier size. A GC
pass does not grow with the history: it reads the active segment and the carry, and the passes got
shorter as the run went on. The rotation stall is still the largest operation latency.

**Residuals, carried as open items.** Delivery state grows with history (about 0.18 GiB per 100,000
deliveries, above) and nothing prunes it; compressing packs or storing a lease once rather than
under several keys would cut it and is a format change. A rotation of a full window still blocks
leases and acknowledgements while it archives (3.9 / 5.1 / 4.5 s on Windows and 2.3 / 3.5 / 6.8 s on
Linux above), so hooks may spool and the drain re-leases under the same nonce; archiving sealed
leases incrementally off the barrier (the review's option b) would change the recorded rotation
order and needs a rotation-intent record, and is an owner decision. Store GC halts, collecting
nothing, while the active segment carries more than 65,536 archived leases that were never
acknowledged, and a rotation refuses (the journal fails closed as at its cap) once the carry passes
64 MiB, about 200,000 such leases; a lease denied by a terminal disposition but never acknowledged
stays carried, because GC never released on one. The same-session ordering gate reads two
generation-store keys with the owner mutex and the journal's state mutex held after the first
rotation (review finding 5: `BenchmarkDeliveryRolloverDispatchGate`, about +35-45 us per call on
Windows and +8-10 us on Linux with five archived windows, runs 26 and 32); `delivery_order.go`
belongs to the ingest workstream, which has it. A crash while a new segment's five files are being
created leaves a partial staged directory that the next rotation refuses as a conflict (the recorded
rule: preserve, never overwrite); the carry added a fifth file to that window. The segment
authority's 1 MiB open bound allows about 5,600 transitions (about 3.6 × 10^8 deliveries), after
which the transition is refused and the journal fails closed; that path is not exercised by a test.
A pre-segment build refuses the journal and changes no delivery state, but one from before V6's
fail-closed journal change (301a8e9 is one) still indexes what it receives there without identities;
a store-wide barrier would stop that and its read-only commands too, which is an owner decision.
Nothing prompts an operator to take a backup before the first rotation, the only rollback path to an
older build (`docs/backup.md` says to).

**Found outside this row.** Both belong to the base and reproduce on a never-rotated project: a
SessionEnd `flush` delivery writes a capture sidecar whose op `classifyCaptureView` does not know,
so fsck fails `captures` and `publication` and `backup restore` exits 1 on its integrity check; and
live captures are held until SessionEnd (close-out C1.1). Evidence:
`drill/runs/20260922-drill2/posthoc-*`. Because of the first, `backup restore` exited 1 in drills 2
and 3; drill 4 did not reproduce it (above).

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

## SP20-D6 — budget B-A gates on a sample that omits the handler the hook waits on

`deferred:V6-VERIFY`, opened at the V5 close-out (2026-09-13). The row is open, not fixed: every
repair on the table changes a frozen contract, so choosing between them is the owner's call and not
this close-out's. The defect is recorded and pinned instead.

**What the contract says.** `plans/00-ARCHITECTURE.md:281` defines B-A as `hook_controlled — client
main() entry → exit (connect + write + ACK)`, p99 < 15 ms, gated in CI on all three platforms at
5 000 iterations. The ACK wait is INSIDE the budgeted region by that definition, not beside it.

**What is measured.** `internal/daemon/handlers.go` calls `recordHotPathSample` after `callHandler`
has returned (line 271), but the value it records is computed from the two timestamps that bracket
the daemon's READ alone: `observed := time.Duration(int64(recvTS)-int64(req.TS)) * time.Millisecond`
(line 312), plus `hotPathTailAllowance`. The handler's own duration is excluded by construction — it
is never read from any clock, so no amount of work inside the route can move the sample.
`hotPathTailAllowance` (`internal/daemon/budget.go:14-26`) is 1 ms, and its doc comment says it
estimates "the ACK read plus process exit, after the daemon has stopped timing", and that
over-counting is "the safe direction".

**Why that is wrong rather than merely approximate.** `internal/ipc/server.go`'s `handleConn` runs
`resp := s.dispatch(ctx, h, req)` (line 232) BEFORE it writes the ACK byte or the response line, so
the hook client is blocked on the full handler, not on the daemon's read. Since SP20-D1 — this wave
— the hot-path route for `observe.tool` runs the durable `ingest.Accept` (WAL append + fsync, lease
journal, seal) inside that pre-ACK region, and `internal/config/deadlines.go`'s derivation comment
records a measured B-B p99 of **36.864 ms** on this Windows host. The hook's real main()→exit time
is therefore (B-A observed) + up to ~37 ms, while the gated sample is (B-A observed) + 1 ms. The
allowance UNDER-counts the tail by the whole B-B region — the opposite of what `budget.go`'s comment
claims — so the §8.1 fallback ("B-A p99 exceeds budget for 3 consecutive 512-sample windows",
`00-ARCHITECTURE.md:307-309`) cannot fire on a breach the hook actually pays. Nothing absorbs it
elsewhere either: `internal/config/defaults.go` sets `HotPath.BudgetMs = 15` as a single value with
no per-platform switch, where B-B has one (50 on Windows, 40 on macOS).

**The consequence, stated plainly.** B-A is green on a host where the hook it describes is over
budget by a factor of several, and the degrade-to-spool clause the design leans on for exactly that
situation is unreachable through this series. SP05-D2's fallback rate — 13 % of hooks in the two x09
runs on file — is what the same region looks like measured from the client's own ACK deadline, which
is the side that does see it.

**Why it was not fixed here.** Each of the three repairs changes a contract this checkpoint may not
move, and they are mutually exclusive:

- **Re-define B-A to stop at daemon receipt** and let `AckDeadlineMs` bound the hook's total. Honest
  about what the daemon can measure, and it makes `hotPathTailAllowance` unnecessary rather than
  wrong — but it retires §2.4's own "connect + write + ACK" wording and leaves the region the hook
  pays gated by a deadline rather than a budget.
- **Re-budget B-A per platform to include the durable `Accept`**, as B-B already is. Keeps the
  contract's region and admits the measured cost — but the 15 ms figure is Qompack.md §8.1's own
  headline number, not only §2.4's, and CI gates it on three platforms.
- **Feed the handler duration into the sample** (time the route, add it to `observed` in place of
  the flat allowance). Smallest code change and it makes the series mean what its name says — but it
  makes B-A breach on this host immediately, which trips §8.1 and puts the daemon into spool submode
  for the rest of every session, i.e. it changes shipped runtime behaviour rather than a number.

The third also interacts with SP20-D1's own deferral: whichever way B-B's durability cost is settled
is the cost B-A would start carrying. Deferral target **V6-VERIFY**, alongside SP20-D1's, SP20-D2's
and SP09-D1's platform-measurement rows, which are the same decision from the other end.

**Evidence.** `TestCarriedDefect_SP20D6_GatedBASampleExcludesThePreACKHandler`
(`internal/daemon/hotpath_ack_tail_test.go`). It drives one real `observe.tool` request through
`dispatchOp` with the shipped route wrapped so that it advances a fake clock by 100 ms before
returning, and asserts the recorded `hook_controlled` sample is exactly `recvTS - req.TS` + 1 ms
— 5 ms against a handler that cost 100 ms — that the same value is what reaches the breach
detector's
channel, and that it sits inside the 15 ms budget while the region `00-ARCHITECTURE.md:281` defines
is far outside it. Those equalities are what flip when the row is fixed. A second subtest pins,
through a real `ipc` server and client, that the ACK byte is observed only after the handler
returned; it is deterministic in the direction that holds today (the channel close happens-before
the handler's return, which happens-before the ACK write, which happens-before the client's read),
but its negative control is NOT deterministic, so it states the current ordering rather than
carrying the flip. The test uses no `time.Sleep`, no wall-clock threshold and no
`require.Eventually`: the handler cost is a fake-clock advance, the orderings are channels, and the
exact value is read from the histogram's `Max`, which `internal/obs/hist.go` tracks in whole
microseconds independent of bucketing. Both negative controls were run in a `git archive` scratch
copy, never the worktree, and each produced a real failure signature: timing the sample to after the
handler fails the `hook_controlled_observed` assertion at 104 ms against 4 ms, and feeding the
handler into the gated series alone fails the `hook_controlled` assertion at 105 ms against 5 ms.

## V6 remediation update — 2026-09-21

The earlier measurements and failure descriptions above remain historical evidence.

**SP20-D6 — fixed accounting, no universal timing claim.** The gated estimate now adds
measured handler duration (including durable ingest before ACK) to the receive-time
lower bound and the existing estimated client-exit tail. The retained test maps
`TestCarriedDefect_SP20D6_GatedBASampleExcludesThePreACKHandler` to
`TestCarriedDefect_SP20D6_GatedBASampleIncludesThePreACKHandler`. Its controlled clock
checks both histogram and fallback-channel samples and a resulting budget breach.
The focused run `sdd/V6-remediation/runs/delivery-cap-and-preack-corrected.json` passed.
The tail remains an estimate; actual platform latency and supported budgets remain
separate V6 performance obligations.

**SP20-D4 — partial mitigation, still unresolved.** Exhaustion or another failure of
a configured delivery journal now refuses ACK and observer dispatch for identified
requests. WAL/spool input remains pending; old identities and journal bytes survive.
The same focused run exercises the retained 65,536-entry fixture and real admission
and drain refusal. Automatic rollover has not been implemented, so this row remains
`deferred:V6-VERIFY` and still blocks V6 sign-off. Never delete journals or reset
arrival counters to conceal exhaustion.


V6 prompt follow-up (2026-09-21): SP08-D3 maps the retained
`TestCarriedDefect_SP08D3_DrainedPromptIsNeverCaptured` to
`TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero`. Focused replay,
restart, interrupted-link and missing-object checks passed in the new V6 remediation
run series. The row stays open for concurrent session ordering and the inherited
derived tool/stop publication cut; these are not certified by the prompt-only fix.

V6 integration follow-up (2026-09-22): `99108a2`, `22ff16c`, and `c34acb4`
add bound original publication intents and leased same-session ordering. Focused
observer/publication/order runs and the Linux instrumented X10 crash-replay run
pass on their identified snapshots; final candidate packaged recovery and
acceptance are still outstanding. SP08-D3 remains deferred pending those gates.
The segmented journal implementation and GC/offline readers pass focused identity,
70-rotation retention, same-build backup/restore, and Linux race cases. Its
production switch remains default-off because older readers/rollback and resource
costs are not certified. SP20-D4 therefore remains deferred; this supersedes the
earlier statement that no rollover implementation exists without claiming the
enabled production capacity limitation is resolved. Evidence is recorded under
`sdd/V6-remediation/reader-integration-resolution.md` and
`sdd/V6-remediation/linux-runtime-resolution.md`.

## V6 close-out update — 2026-09-22

**SP20-D4 -> fixed.** This supersedes the "partial mitigation, still unresolved" paragraph and the
2026-09-22 integration follow-up above: segmented rollover is enabled by default (C1.10), and the
resolution, its evidence test and its residuals are at the end of the SP20-D4 section.

---

## V6-VERIFY disposition of SP06-D2 (2026-10-01, coordinator under owner decision D33; ledger D54)

**SP06-D2 -> wontfix for 0.3.0.** Measured by quiet C5.2 on candidate 5 `0d06ab12`, ten balanced ABBA rounds against the pre-Phase-2 base `cf31e01`, medians per call (`plans/sdd/V6-closeout/phase3/c5/quiet/c52-win/paired.txt` and `c52-linux/paired.txt` on verify/v6 `593003e9`; Windows 11, Intel Core Ultra 7 155H; Linux is the Docker Desktop container, valid for CPU- and read-bound rows, not for fsync-bound ones (D53(b))), against PutBytes's 3 ms (cold) and 400 us (warm)
budgets:

| benchmark | Windows | Linux |
|---|---|---|
| `BenchmarkPutBytes_100KB_Cold` | 17.27 ms (0.71x base) | 19.38 ms (0.82x base) |
| `BenchmarkPutBytes_100KB_Warm` | 3.27 ms (0.47x base) | 2.34 ms (0.38x base) |

Both OSes are 10/10 rounds faster than the base, and cold allocations fell 2-3.4x. Both budgets are
still missed by 6-8x. The cost is one durable, verified object file per novel chunk (fsync before ACK,
SP20-D1; D26), and on the container a slow fsync on top. The budgets were set before the store became
durable and verify-on-read; no measurement on any platform has met them since. PutBytes runs after the
hook's ACK, on the B-C path (SP08-D1), so a user's hook never waits on it. Meeting 3 ms needs a
batched-write store format (packs or group commit), a crash-model change that is not taken at the
release freeze. Revisit with SP08-D1 in post-0.3.0 performance work.

---

## V6-VERIFY disposition of SP09-D1 (2026-10-01, coordinator under owner decision D33; ledger D54)

**SP09-D1 -> fixed.** Measured by quiet C5.2 on candidate 5 `0d06ab12`, ten balanced ABBA rounds against the pre-Phase-2 base `cf31e01`, medians per call (`plans/sdd/V6-closeout/phase3/c5/quiet/c52-win/paired.txt` and `c52-linux/paired.txt` on verify/v6 `593003e9`; Windows 11, Intel Core Ultra 7 155H; Linux is the Docker Desktop container, valid for CPU- and read-bound rows, not for fsync-bound ones (D53(b))): negknow `BenchmarkOpen` reads **61.6 ms/op** on Windows
(78 ms CPU/op in the candidate's own run) and **77.0 ms/op** on Linux, against the 300 ms budget, with
allocations down 35 % (the V6 close-out allocation work, `internal/negknow/open_cost_test.go`). Even at
the row's worst observed clock factor (2.3x at 55 % processor performance), Windows stays under 300 ms.
`TestBudget_Open` passes in candidate 5's isolated timing on Windows and Linux
(`phase3/c5/p3-win-timing.log`, `phase3/c5/linux/cx-p3-p3-linux-timing-*`).

Recorded, not acted on: Linux reads 1.33x the base (58.2 ms) while allocations fell. That is unattributed;
an fsync-bound step on the container is the likeliest reading. Open runs once per daemon start, off the
hot path.

---

## V6-VERIFY disposition of SP20-D2 (2026-10-01, coordinator under owner decision D33; ledger D54)

**SP20-D2 -> fixed (budgets met on the reference platform; Windows residual recorded).** Measured by
quiet C5.2 on candidate 5 `0d06ab12`, ten balanced ABBA rounds against the pre-Phase-2 base `cf31e01`, medians per call (`plans/sdd/V6-closeout/phase3/c5/quiet/c52-win/paired.txt` and `c52-linux/paired.txt` on verify/v6 `593003e9`; Windows 11, Intel Core Ultra 7 155H; Linux is the Docker Desktop container, valid for CPU- and read-bound rows, not for fsync-bound ones (D53(b))):

| benchmark | budget | Linux | Windows |
|---|---|---|---|
| `GetChunk` | 60 us | **20.4 us** | 73.7 us, 0.55x base |
| `Search_1000Roots` | 25 ms | **7.0 ms** | **11.9 ms**, 0.10x base |
| `OpenSpan_4KB_of_4MB` | 150 us | **21.2 us** | **71.1 us** |

The row's other two concerns are closed on both OSes. On Windows, GetChunk stays 23 % over. That
residual is the per-object file open, Lstat and fstat, which is NTFS cost, plus the content hash
verify-on-read keeps on purpose (integrity, 16ecc77). The read buffer is presized (allocations 22 -> 9).
Retrieval's user-facing envelope, B-F, is gated separately (quiet C5.1 B-F gate runs pass).

---

## V6-VERIFY candidate 6 confirmation (2026-10-02, C6.3)

Candidate 6 is `verify/v6` `99d0b18`. Every `Test*` evidence test below ran green on it in the Windows whole tree (pre-freeze tree `61b0cd66`, product-identical, and the `-race` pass `phase3/c6/p3-win-race.log`) and in hosted ci.yml `36955046276` `test (ubuntu-latest)` (`-race`) and `test (macos-latest)`; codes and paths are in `sdd/V6-closeout/inventory-c6-map.md`. A `Benchmark*` evidence symbol exists on the candidate; `go test` does not execute benchmarks, and the measurement a row rests on is named in its row. Status is unchanged; `CARRIED-DEFECTS.tsv` remains the source of record.

| row | status | ruling | on candidate 6 |
|---|---|---|---|
| SP06-D1 | `wontfix` | V3-VERIFY (option b, documented deadline scope) | no runtime symptom; the GC deadline rows pass in both isolated timing passes on candidate 6 |
| SP05-D1 | `fixed` | V4-VERIFY (`66690d52`) | TestCarriedDefect_SP05D1_IdleBudgetExpiryLeavesInterruptedLinePending green |
| SP06-D2 | `wontfix` | D54 (as SP08-D1) | PutBytes cold/warm: the candidate 5 measurement carries by D57(e): a coverage trace (`sdd/V6-closeout/w17-inventory/runs/c52-executed-files.txt`) shows the one executed file changed since candidate 5 is the test helper `internal/paths/pathstest/home.go`, by comment lines only (the comment-only reading is the inventory's, for the owner to ratify) (no internal/store product file changed) |
| SP05-D2 | `fixed` | V5 close-out (`e026cba6`) | BenchmarkIngestAcceptLeasedBurst exists; the quiet C5.1 run on candidate 6 (`phase3/c6/quiet/c51-win.log`) passes on Windows, B-A p99 30.72 ms against 50 with no deferral; the Linux container defers 3591 of 5130 hot-path requests to the client spool with 0 lost and is not verified in target (D53(b)); X11's two candidate 6 Windows failures ran on battery and are invalid as reference measurements (D57(d)); on AC, X11 passes alone three rounds out of three with no deferral (`phase3/c6/x11-e1/summary.txt`, D57(g)) |
| SP20-D1 | `fixed` | V5 close-out (`e026cba6`; B-B re-budget, fsync before ACK) | BenchmarkIngestAcceptLeased exists; quiet C5.1 on candidate 6 measures Windows B-B p99 24.58 ms against 50 (`phase3/c6/quiet/c51-win.log`); Linux container B-B (61.44 ms against 15) is not verified in target (D53(b)) |
| SP20-D2 | `fixed` | D54 (budgets met on Linux; Windows GetChunk residual recorded) | BenchmarkGetChunk: the candidate 5 measurement carries by D57(e): a coverage trace (`sdd/V6-closeout/w17-inventory/runs/c52-executed-files.txt`) shows the one executed file changed since candidate 5 is the test helper `internal/paths/pathstest/home.go`, by comment lines only (the comment-only reading is the inventory's, for the owner to ratify) (no internal/store product file changed) |
| SP20-D3 | `fixed` | V5 close-out (`782652d1`) | TestPutBytes_SharedVolatileTokenRestoresBothRootsExactly green |
| SP09-D1 | `fixed` | D54 | TestBudget_Open green in both isolated timing passes on candidate 6 (`phase3/c6/p3-win-timing.log`, the Linux timing artifacts); BenchmarkOpen: the candidate 5 measurement carries by D57(e): a coverage trace (`sdd/V6-closeout/w17-inventory/runs/c52-executed-files.txt`) shows the one executed file changed since candidate 5 is the test helper `internal/paths/pathstest/home.go`, by comment lines only (the comment-only reading is the inventory's, for the owner to ratify) |
| SP20-D4 | `fixed` | C1.10, D2, D6, D16 | TestCarriedDefect_SP20D4_CaptureContinuesPastTheOldEntryCapAcrossRestart green; rollover ships enabled |
| SP20-D5 | `fixed` | V5 close-out (`e026cba6`) | TestDrainKeepsTheDurableBoundItRecordedWhenASegmentIsReopenedBelowIt green |
| SP20-D6 | `fixed` | V6 remediation (`65bc8d77`; accounting, no universal timing claim) | TestCarriedDefect_SP20D6_GatedBASampleIncludesThePreACKHandler green |

## V6-VERIFY candidate 7 note (2026-10-02, C6.3)

Candidate 7 is `verify/v6` `d20309c0`, the freeze of `closeout/integration` `b31d0753`. Against candidate 6 its only product change is core.Version's default literal and `plugin.json`'s version; test/fault, test/guards' `nonrefdisk_test.go`, the golden `plugin.json`, two workflows, `.goreleaser.yaml` and docs also changed (`sdd/V6-closeout/w17-inventory/runs/c7-carry-proof.txt`). None of the evidence tests or benchmarks above is in a changed file, so the candidate 6 confirmation above carries to candidate 7 (D57(c)); candidate 7's pre-freeze check (`phase3/c7/prefreeze/summary.log`) ran the Windows tree, except test/e2e and test/integration, green. Status is unchanged.
