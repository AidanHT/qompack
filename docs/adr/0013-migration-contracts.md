# 13. Migration contracts: identities, publication, the shared ledger, envelopes and ownership

Date: 2026-09-07

## Status

Proposed by SP-19 M0-02 on branch `arch/migration-contracts`, cut from the SP-19 branch at the
commit that lands the v1.5 planning baseline. It amends `plans/00-ARCHITECTURE.md` with §0.2 and
binds the M1–M3 implementation owners (SP-20, SP-13, SP-10, SP-11). It takes effect for
implementation only once those owners have approved it; the independent SP-19 reviewer records
each owner's concern as accepted or open in `plans/MIGRATION-EVIDENCE.md`. Nothing in this ADR
enables a feature: every behaviour it describes stays behind a `runtime.migration.*` gate that is
pending in this build (`internal/config/migration.go`).

## Context

M0-00 merged the four completed wave-3 deliveries into one baseline (66549ce) and found three
things the merge could not settle on its own, all recorded in the M0-00 handoff:

1. **The negative-knowledge ledger has four would-be consumers and one lazy opener.** SP-11 opens
   `negknow.Ledger` on the first compaction (`RehydrateOptions.OpenLedger`) because opening it
   creates `sketches/tried.bloom`, and three inherited e2e cases (`test/e2e/observer_e2e_test.go`,
   `v3_x08_test.go`, `v3_x12_test.go`) assert that file does not exist unless a session recorded
   eliminations. SP-10's `SourceSet.Validate` refuses a nil `Ledger`, so
   its C-1 wiring (`BindCheckpoint`/`WireCheckpoint` in `runDaemon`) was left uncalled; SP-12's
   `Sources` and the MCP tools' `Ledger` were left nil for the same reason. Any composition-root
   open — eager, or on an idle tick through `advance_frontier` (`Ledger.All`) or `rebuild_bloom` —
   breaks those e2e cases. So today the shipped daemon never reaches the checkpointer, and
   `already_tried`/`record_eliminated` answer `available:false`.
2. **Two owners of one draft.** SP-10's cadence path (`finalizeIfDue`) and SP-12's O5
   `advance_frontier` task would both `Begin`/`Advance` the same `FileWriter` draft (`Begin` returns
   the live draft). SP-12 caches `*Draft` across ticks, has no `ErrDraftSealed` recovery, and its
   `Close`/`resetSessionLocked` abort a draft it does not own. Inert only because `Sources` is nil.
3. **Errors read as absence.** `already_tried` turns a ledger query error into `State: absent,
   Degraded: true` (ledger E06), and the contract monitor reports an undeclared producer as
   `OK/SevInfo/not-yet-implemented`. Both conflate "cannot tell" with "no".

The v1.5 design (Qompack.md §§7–8, 00-ARCHITECTURE §0.1, ledger "Shared contracts for drafting"
1–9) requires versioned identities, durable-before-referenced publication, a current-state
authority model, a retrieval envelope that distinguishes absent from unavailable, and unique file
ownership across the composition root, store, checkpoint writer/reader, rehydrator and MCP — and
forbids a checkpoint↔retrieval package cycle.

## Decision

The contracts are specified in `plans/00-ARCHITECTURE.md` §0.2 (the normative text). The decisions
that needed an argument, and the alternatives rejected:

**D13-1 — Shared identity and envelope types live in `internal/core`.** `core` is the one package
every layer already imports (§3.2 allow-table), so `ObservationID`, `Fidelity`, `Coverage`,
`EvidenceOutcome` and `Authority` there create no new edge and no cycle. *Rejected:* a new leaf
package (`internal/evidence`) — it would need an OWNERS row, an importrules row and a stub phase
for a handful of enums; putting them in `checkpoint` or `mcp` — either direction is the cycle §0.1
forbids.

**D13-2 — New evidence is a sidecar, never an edit to a frozen record.** `ToolUseRecord`'s wire
line, `checkpoints/0001.json`, `records/eliminations.jsonl` and every §16 fixture keep their
bytes. Fidelity, transform chain, hash version and ordering live in `index/observations.jsonl`
keyed by `ObservationID`, with optional nonunique `HostID` and legacy `ToolUseID` lookups. The
V4 implementation review corrected the earlier ToolUseID key: repeated or absent host IDs must
not collapse distinct deliveries or force a fabricated host ID. A record with no sidecar has fidelity `unknown`, not `exact`:
the additive contract cannot read an old missing field as complete evidence. *Rejected:* adding
fields to the frozen wire and regenerating goldens — §16 forbids refreshing a fixture to make a
difference disappear, and every old reader would misreport old lines as complete.

**D13-3 — One draft owner per session: the checkpointer.** SP-12's `advance_frontier` stops
holding a `*Draft`. It calls a `checkpoint.FrontierAdvancer` port that the checkpointer implements
over its own live draft (`Begin` if none), recovers `ErrDraftSealed` by re-`Begin`-ing against the
new parent and retrying once, and never lets a non-owner seal or abort. *Rejected:* letting SP-12
keep its own draft and teaching it `ErrDraftSealed` — two owners still race on `Finalize`, and the
DPI guard (`ErrAlreadyEncoded`) would be adjudicated in two places with two policies.

**D13-4 — The ledger is opened by one once-guarded handle with the policy "create on first write,
open if it exists for reads".** The one writer, the `record_eliminated` tool, opens — and thereby
creates — the ledger on first use. Every other consumer reads: `already_tried`, SP-10's
`SourceSet.Ledger`, SP-11's rehydration item 3, and SP-12's `rebuild_bloom`, which uses `Peek()`
and is a no-op while nothing is open. Readers open the ledger only if `records/eliminations.jsonl`
already exists; otherwise they receive `ErrLedgerAbsent` and answer *unavailable / no eliminations
recorded* without creating a file. This keeps the inherited e2e invariant intact (`tried.bloom`
appears only after a session recorded eliminations), lets C-1 be wired (the read adapter is
non-nil so `SourceSet.Validate` passes, and it answers an absent ledger with an empty record set
and no error so `Begin`/`Advance` proceed; the "ledger absent" qualifier lives in the sidecar and
SP-11's coverage report, not in `Checkpoint.Eliminated`, whose frozen `[]negknow.Record` shape is
untouched), and closes E06 (an error or an absent ledger is never `absent`). *Rejected:* eager open at wire time — creates the file in every daemon that never
compacts and holds an append handle for the process lifetime (the Windows cleanup failure SP-11
documented); keeping four independent openers — four handles on one append-only file.

**D13-5 — Domain outcomes are data, not protocol errors.** The retrieval envelope carries
`Outcome ∈ {ok, absent, unavailable, denied, corrupt, expired, uncertain}`; MCP `IsError` is
reserved for protocol failures. `absent` may be asserted only with complete, fresh coverage;
anything less is `uncertain` or `unavailable` with a reason and a recovery direction.

**D13-6 — Every behaviour behind an independent gate.** `runtime.migration.{capture.rawEvidence,
publication.durableFrontier, replacement.newResult, compaction.automaticVeto, experiments.enabled}`
are refused by `config.Validate` until the owning subplan flips the gate in its own reviewed
commit with the named test evidence (`internal/config/migration.go`, `MigrationGates`). Until
then the existing paths are unchanged, so rollback of any M1–M3 landing is "the gate stays off":
old readers ignore sidecars, and no old artifact changes shape.

## Consequences

- SP-20 M1 can begin from a fixed identity/envelope contract without touching `checkpoint` or
  `mcp`; SP-13 consumes the envelope through the composition root; SP-10/SP-12 have one owner for
  the draft and a port to implement/consume; SP-11 gets the ledger through the handle instead of
  its private opener.
- Three inherited e2e assertions about `tried.bloom` stay true by construction rather than by
  leaving the checkpointer unwired.
- The composition root grows one file (`internal/cli/ledger_handle.go`) with one editor.
- What this ADR does **not** do: certify native capabilities, enable any gate, migrate any user's
  data, or change a frozen fixture. Those are M1–M3 and V4-VERIFY.

## References

- `plans/00-ARCHITECTURE.md` §0.1, §0.2 (this amendment), §3.2, §5.14–§5.16, §12.1, §13.
- `plans/MIGRATION-EVIDENCE.md`: "Shared contracts for drafting" 1–9; E03, E05, E06, E16; the
  M0-00 handoff (semantic decisions 1–2) and the M0-01 section.
- `plans/V4-SP-20-capture-storage-and-state-remediation.md` M1-01…M2-03;
  `plans/V4-SP-13-mcp-retrieval-layer.md`; `plans/V4-SP-10-checkpointer-l4.md`;
  `plans/V4-SP-11-rehydrator-l5.md`; `plans/V4-SP-12-scheduler-l3.md`.
- Code: `internal/checkpoint/source.go` (`SourceSet.Validate`), `internal/checkpoint/writer.go`
  (`Begin`, `Advance`, `ErrDraftSealed`, `Abort`), `internal/daemon/scheduler_frontier.go`
  (`ensureDraft`, `abandonDraft`), `internal/daemon/rehydrate_service.go` (`OpenLedger`, `deps`),
  `internal/mcp/handlers.go` (`alreadyTried`), `internal/mcp/tools.go` (`ToolDeps`),
  `internal/cli/daemon.go` (`runDaemon`).
