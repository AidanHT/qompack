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
