# 0100. V3 verification — wave 2 (observer L0 + negative knowledge)

Date: 2026-08-26
Status: accepted

## Context

Wave 2 landed SP-08 (the L0 observer, closing Phase 1) and SP-09 (negative knowledge, closing
Phase 2). V3-VERIFY is the exhaustive re-verification of everything on `develop` at that point:
the full §2 inventory (groups A–J), the V1 §1 and V2 §2 source inventories re-run whole, twelve
new permanent cross-component integration tests (X1–X12), every performance budget in force, and
the disposition of all fifteen carried-defect rows. `plans/V3-report.md` is the record; this ADR
is the abridged per-checkpoint entry V6-VERIFY expects in the 0100 block.

## Decision and results

Verdict: GREEN on every locally-runnable row; final sign-off pends only J5 (the CI run was refused wholesale by a GitHub billing failure and reruns once restored). Phases 0, 1 and 2 verified closed on `verify/v3` (cut from `5170dc0`).

Headline numbers (quiet windows-11 lane unless noted):
- Phase 1: read-heavy DedupRatio 189.35 (floor 4.0); canonicalization gap 4.008x (floor 1.25).
- Phase 2: repeated eliminations 138 (stock) vs 39 (negknow), a 71.7% reduction (floor 25%),
  negknow < stock on 18/24 sessions, zero stale-blocks across all six dependency-change sessions.
- Phase 0: `stock.fraction_of_opt` 0.695164 over 24 sessions, baseline byte-reproducible twice.
- Hot path with observer and ledger resident: B-A p99 2.048 ms (V2: 3.072), B-B p99 0.576 ms,
  B-E green; store growth exponent 0.620 committed / 0.0332 on the live observer-fed store.
- benchstat vs the wave-1 baseline: zero time-metric regressions >10%; two byte-count warnings
  (dag RebuildIndex +414 B/op, AddNodeEdgePair +200 B/op) against 30–44% time improvements.

Rulings of record (full trail in plans/V3-report.md and the verify/v3 commit bodies):
1. Merge-order deviation: SP-08 merged before SP-09 (plans/README.md step 3 says the reverse).
   Nothing in the tree constrains the order (§0a item 4's own finding); every intermediate
   first-parent commit was verified green, which is the item's substantive check.
2. X2's row steps 7–8 contradicted I8/I12 and §13 invariant 3 (tried.bloom rebuilds from ACTIVE
   records only); the shipped behaviour is asserted, the row text was the defective party.
3. X10's original DAG assertion was vacuous (deps.jsonl is written only by Graph.Flush); the test
   gained non-vacuity pins and a post-drain flush. A torn tail is surfaced by Warn + TruncatedTail;
   Loud is reserved for damaged records.
4. The W-2 sketch-health fixture was unreproducible at its own declared population (2 keys/record);
   reconciled to measured values 0.434256 / 0.002912202 with the baseline watch-fors and gate pins
   moved in the same commit (§7.3 same-filter-state rule).
5. Qompack.md's diff vs the root commit is exactly the authorized v1.3 revision; J6 is enforced as
   "no unauthorized modification".

Carried defects: SP04-D5, SP04-D6, SP04-D7 fixed; SP04-D2, SP04-D3, SP06-D1 wontfix with the owed
decisions recorded; SP02-D1..D6 (the corpus re-baseline, as one unit), SP05-D1, SP06-D2 (first
Linux figures recorded: PutBytes over budget there too) and SP08-D1 (paired with SP06-D2) deferred
to V4-VERIFY, each with its reason in the row's detail document. B-G/B-D remain observations
nothing in production can judge and are re-carried to V4-VERIFY explicitly: the evaluator seam
belongs to SP-12's idle scheduler (§0a item 6 discharged by this record, not by silence).

New permanent surface: test/e2e/v3_x01..x12_test.go (the §5 suite), the nightly fuzz matrix row
for observer's FuzzExtractSignals, and the reconciled growth/health fixture.

## Consequences

Wave-3 branches (SP-10..SP-13) may be cut from the post-merge `develop` once the §8.5 gate rows
are all answered. V4-VERIFY inherits: the corpus re-baseline unit (SP02-D1..D6), the PutBytes
budget-vs-implementation decision (SP06-D2 + SP08-D1, one defect at two layers), SP05-D1 beside
SP-08's R3 transport-ordering work, the B-G/B-D production wiring, and the recorded-corpus tier
(still empty, restated).

2026-08-26 amendment: the user waived the J5 CI gate (external GitHub billing failure; no job
ever executed). Merge to `develop`, the local `v0.2.0` tag and the wave-3 branch cut proceeded
under that waiver. The CI backfill — nine green jobs plus the three bench-gate p99 figures
folded into ADR 0008 — remains an open obligation on `develop`.
