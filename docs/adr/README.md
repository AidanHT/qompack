# Architecture decision records

Every ADR in this directory is indexed below. The set is derived from the directory itself by
`test/docs` (`TestADRIndexListsEveryADR`), so an ADR that is added without a row here fails the
build, and a row whose title no longer matches its ADR fails too.

**Status and date are quoted as the ADR states them**, not re-derived. Where an ADR gives no date,
the cell says so rather than guessing one.

The last column says how to read each one:

- **Historical record** — a decision written at the time it was taken, kept for provenance. Read it
  to learn why something is the way it is; do not read it as the current contract.
- **Current amendment** — a record that changes or extends a contract other documents depend on.
  Read it alongside `plans/00-ARCHITECTURE.md` and the code it names.
- **Stands** — a decision still in force whose contract has not been amended since.

| ADR | Title | Status / date, as stated | Kind |
|---|---|---|---|
| 0002 | [Replay methodology: what the numbers mean and how they are produced](0002-replay-methodology.md) | accepted (SP-02, wave 1); no date line | Stands. "Supersedes: nothing"; the replay gate in CI still enforces it |
| 0003 | [Re-collecting the replay corpus, and the schedule the gate enforces](0003-replay-overfit-recollection.md) | accepted (SP-02, wave 1); no date line | Stands. Related to ADR 0002; the re-collection schedule is the one the gate runs |
| 0007 | [DAG slices are scores, not drop decisions](0007-dag-slices-are-scores-not-drop-decisions.md) | Accepted. Implemented by SP-07 (`internal/dag`); 2026-08-15 | Stands |
| 0008 | [Observer L0: capture, tombstones, supersession, and the Phase 1 exit](0008-observer-l0.md) | Accepted. Implemented by SP-08; 2026-08-25 | Stands |
| 0009 | [Negative knowledge: the bloom is a cache, and nine decisions that follow from it](0009-negative-knowledge-bloom-as-cache.md) | Accepted. Implemented by SP-09 (`internal/negknow`); 2026-08-23 | Stands. The bloom is a cache over the records, never the source of truth |
| 0010 | [A wall-clock budget is judged only where it is judgeable](0010-wall-clock-under-coload.md) | Accepted. Implemented at the close of V3-VERIFY's J5 backfill; 2026-09-06; Addendum 1 2026-09-13 (B-B), Addendum 2 2026-10-01 (hosted non-reference disk) | Current amendment. It states of itself: "Supersedes nothing; generalises two earlier rulings that had each been made once". Addendum 2 adds `QOMPACK_NONREFERENCE_DISK`, under which hosted CI reports B-A, B-B and B-E's wall row |
| 0011 | [Rehydration budget, item order, and whole-rule restoration](0011-rehydration-budget-and-item-order.md) | accepted; 2026-09-06; subplan SP-11; amended 2026-09-22 (§21, D5) | Current amendment. §21 holds the payload under the host's 10,000-character `additionalContext` cap; §1–§20 stand as refined there |
| 0012 | [Scheduler L3: the composite trigger, BOCD, cache regimes and p-selection](0012-scheduler-l3.md) | Accepted; 2026-09-06 | Stands. `plans/V5-report.md` §29 item 8 carries an open pointer correction in this ADR to V6 planning |
| 0013 | [Migration contracts: identities, publication, the shared ledger, envelopes and ownership](0013-migration-contracts.md) | "Proposed by SP-19 M0-02 on branch `arch/migration-contracts`"; 2026-09-07 | Current amendment, proposed. It amends `plans/00-ARCHITECTURE.md` with §0.2 and "takes effect for implementation only once those owners have approved it". Nothing in it enables a feature: every behaviour stays behind a pending `runtime.migration.*` gate |
| 0014 | [The delivery path's group commit and the A/B seal](0014-delivery-group-commit-and-ab-seal.md) | Accepted, on branch `verify/v5-final`; 2026-09-13 | Current amendment. It "records decisions that are already implemented and merged rather than proposing new ones", and settles four of the twelve questions SP20-D1's design put to the owner |
| 0030 | [The QPKS sketch binary format](0030-sketch-binary-format.md) | Accepted; subplan SP-03, `internal/sketch`; no date line | Stands |
| 0100 | [V3 verification — wave 2 (observer L0 + negative knowledge)](0100-v3-verification.md) | accepted; 2026-08-26 | Historical record. It describes itself as "the abridged per-checkpoint entry V6-VERIFY expects in the 0100 block"; `plans/V3-report.md` is the full record |

## Numbering

Numbers are reserved by subplan, so the sequence has gaps. ADR 0030 states the convention it
belongs to: "ADR numbers in this repository are `<SP number as three digits><sequence>`, so SP-03's
ADRs are `0030`, `0031`, … and can never collide with a sibling subplan's." ADR 0100 refers to a
"0100 block" of per-checkpoint verification entries. The low numbers 0002–0014 are a plain
sequence.

No reason is recorded anywhere for any specific missing number, and none is invented here: a gap
means the number was reserved and not used, not that an ADR was withdrawn.

## Writing a new one

Keep the first line a level-1 heading with the ADR's title, state a status and a date in the header
block, and add the row here in the same commit. `go test ./test/docs/...` checks the last part.
