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
