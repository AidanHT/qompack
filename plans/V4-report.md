# V4 execution report — CORRECTIVE WORK COMPLETE, GATE NOT SIGNED OFF

**Status: corrective implementation complete, independently reviewed, blockers fixed; the revised V4 gate is NOT signed off.** See section 20 for the independent review and the re-run that followed it. Candidate:
`feat/v4-corrective` @ `20a4a63`, 131+ commits ahead of its convergence base `0b14ea6`.
`verify/v4` has not been created. Date of this record: 2026-09-08. Host: Windows 11 (10.0.26200),
go1.26.6 windows/amd64, gcc 14.2.0 with CGO enabled (so the race detector is available).

**Candidate provenance.** The candidate was cut from `feat/sp20-capture-storage-and-state-remediation`
@ `a123ecc` and converged with `wip/v4-preparation` at `0b14ea6`. That convergence was a
prerequisite nobody had performed: `wip/v4-preparation` was **not an ancestor** of the SP-20 line, so
the checkpoint-owned frontier port (`02f807a`), the MCP unavailable-outcome fix (`f4a7f09`) and the
devtool race/budget alignment (`f189f2e`) were absent from the branch all SP-20 work was building on.
Both source branches are preserved untouched, as are the original Wave 3 SP-10–13 completion records
and the accepted M0-G0 baseline.

**What this report is.** Sections 1–13 retain the per-subplan inventory as written; their rows are
reconciled in [reconciliation-map.md](sdd/V4-VERIFY/reconciliation-map.md), which adjudicates all 195
retained rows without marking any of them PASS. Sections 14–19 record what was actually executed on
this candidate. Twenty-six work units landed, each in its own worktree with an exclusive file-ownership
list, test-first, reporting to a file rather than into the coordinating session.

Scope and authority
V4 retains the original SP-01–SP-13 assertions and cross-component scenarios while applying the current
migration contracts. No old PASS result is copied, and a matching test name is treated as an
implementation pointer, never as evidence.

## 1. SP01 retained assertions

Rows: [V4-SP01-01](sdd/V4-VERIFY/inventory.md#v4-sp01-01), [V4-SP01-02](sdd/V4-VERIFY/inventory.md#v4-sp01-02), [V4-SP01-03](sdd/V4-VERIFY/inventory.md#v4-sp01-03), [V4-SP01-04](sdd/V4-VERIFY/inventory.md#v4-sp01-04), [V4-SP01-05](sdd/V4-VERIFY/inventory.md#v4-sp01-05), [V4-SP01-06](sdd/V4-VERIFY/inventory.md#v4-sp01-06), [V4-SP01-07](sdd/V4-VERIFY/inventory.md#v4-sp01-07), [V4-SP01-08](sdd/V4-VERIFY/inventory.md#v4-sp01-08), [V4-SP01-09](sdd/V4-VERIFY/inventory.md#v4-sp01-09), [V4-SP01-10](sdd/V4-VERIFY/inventory.md#v4-sp01-10), [V4-SP01-11](sdd/V4-VERIFY/inventory.md#v4-sp01-11), [V4-SP01-12](sdd/V4-VERIFY/inventory.md#v4-sp01-12), [V4-SP01-13](sdd/V4-VERIFY/inventory.md#v4-sp01-13), [V4-SP01-14](sdd/V4-VERIFY/inventory.md#v4-sp01-14).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 2. SP02 retained assertions

Rows: [V4-SP02-01](sdd/V4-VERIFY/inventory.md#v4-sp02-01), [V4-SP02-02](sdd/V4-VERIFY/inventory.md#v4-sp02-02), [V4-SP02-03](sdd/V4-VERIFY/inventory.md#v4-sp02-03), [V4-SP02-04](sdd/V4-VERIFY/inventory.md#v4-sp02-04), [V4-SP02-05](sdd/V4-VERIFY/inventory.md#v4-sp02-05), [V4-SP02-06](sdd/V4-VERIFY/inventory.md#v4-sp02-06), [V4-SP02-06b](sdd/V4-VERIFY/inventory.md#v4-sp02-06b), [V4-SP02-07](sdd/V4-VERIFY/inventory.md#v4-sp02-07), [V4-SP02-08](sdd/V4-VERIFY/inventory.md#v4-sp02-08), [V4-SP02-09](sdd/V4-VERIFY/inventory.md#v4-sp02-09), [V4-SP02-10](sdd/V4-VERIFY/inventory.md#v4-sp02-10).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 3. SP03 retained assertions

Rows: [V4-SP03-01](sdd/V4-VERIFY/inventory.md#v4-sp03-01), [V4-SP03-02](sdd/V4-VERIFY/inventory.md#v4-sp03-02), [V4-SP03-03](sdd/V4-VERIFY/inventory.md#v4-sp03-03), [V4-SP03-04](sdd/V4-VERIFY/inventory.md#v4-sp03-04), [V4-SP03-05](sdd/V4-VERIFY/inventory.md#v4-sp03-05), [V4-SP03-06](sdd/V4-VERIFY/inventory.md#v4-sp03-06), [V4-SP03-07](sdd/V4-VERIFY/inventory.md#v4-sp03-07).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 4. SP04 retained assertions

Rows: [V4-SP04-01](sdd/V4-VERIFY/inventory.md#v4-sp04-01), [V4-SP04-02](sdd/V4-VERIFY/inventory.md#v4-sp04-02), [V4-SP04-03](sdd/V4-VERIFY/inventory.md#v4-sp04-03), [V4-SP04-04](sdd/V4-VERIFY/inventory.md#v4-sp04-04), [V4-SP04-05](sdd/V4-VERIFY/inventory.md#v4-sp04-05), [V4-SP04-06](sdd/V4-VERIFY/inventory.md#v4-sp04-06), [V4-SP04-07](sdd/V4-VERIFY/inventory.md#v4-sp04-07).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 5. SP05 retained assertions

Rows: [V4-SP05-01](sdd/V4-VERIFY/inventory.md#v4-sp05-01), [V4-SP05-02](sdd/V4-VERIFY/inventory.md#v4-sp05-02), [V4-SP05-03](sdd/V4-VERIFY/inventory.md#v4-sp05-03), [V4-SP05-04](sdd/V4-VERIFY/inventory.md#v4-sp05-04), [V4-SP05-05](sdd/V4-VERIFY/inventory.md#v4-sp05-05), [V4-SP05-06](sdd/V4-VERIFY/inventory.md#v4-sp05-06), [V4-SP05-07](sdd/V4-VERIFY/inventory.md#v4-sp05-07), [V4-SP05-08](sdd/V4-VERIFY/inventory.md#v4-sp05-08), [V4-SP05-09](sdd/V4-VERIFY/inventory.md#v4-sp05-09), [V4-SP05-10](sdd/V4-VERIFY/inventory.md#v4-sp05-10), [V4-SP05-11](sdd/V4-VERIFY/inventory.md#v4-sp05-11), [V4-SP05-12](sdd/V4-VERIFY/inventory.md#v4-sp05-12), [V4-SP05-13](sdd/V4-VERIFY/inventory.md#v4-sp05-13), [V4-SP05-14](sdd/V4-VERIFY/inventory.md#v4-sp05-14), [V4-SP05-15](sdd/V4-VERIFY/inventory.md#v4-sp05-15).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 6. SP06 retained assertions

Rows: [V4-SP06-01](sdd/V4-VERIFY/inventory.md#v4-sp06-01), [V4-SP06-02](sdd/V4-VERIFY/inventory.md#v4-sp06-02), [V4-SP06-03](sdd/V4-VERIFY/inventory.md#v4-sp06-03), [V4-SP06-04](sdd/V4-VERIFY/inventory.md#v4-sp06-04), [V4-SP06-05](sdd/V4-VERIFY/inventory.md#v4-sp06-05), [V4-SP06-06](sdd/V4-VERIFY/inventory.md#v4-sp06-06), [V4-SP06-07](sdd/V4-VERIFY/inventory.md#v4-sp06-07), [V4-SP06-08](sdd/V4-VERIFY/inventory.md#v4-sp06-08), [V4-SP06-09](sdd/V4-VERIFY/inventory.md#v4-sp06-09), [V4-SP06-10](sdd/V4-VERIFY/inventory.md#v4-sp06-10).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

SP-06 provisional correction evidence is recorded in the inventory's object-integrity addendum
and the SP-20 sibling's progress/artifacts. The selected store race cases and corrected
real-store conformance case pass; the initial zero-test conformance selection is preserved and
supplies no coverage. This adds hash/size verification and preserves rejected evidence, with
independent acceptance of that bounded correction. It does not close the retained SP-06 rows,
publication/GC/rollback, installed retrieval or performance gates.

## 7. SP07 retained assertions

Rows: [V4-SP07-01](sdd/V4-VERIFY/inventory.md#v4-sp07-01), [V4-SP07-02](sdd/V4-VERIFY/inventory.md#v4-sp07-02), [V4-SP07-03](sdd/V4-VERIFY/inventory.md#v4-sp07-03), [V4-SP07-04](sdd/V4-VERIFY/inventory.md#v4-sp07-04), [V4-SP07-05](sdd/V4-VERIFY/inventory.md#v4-sp07-05), [V4-SP07-06](sdd/V4-VERIFY/inventory.md#v4-sp07-06), [V4-SP07-07](sdd/V4-VERIFY/inventory.md#v4-sp07-07), [V4-SP07-08](sdd/V4-VERIFY/inventory.md#v4-sp07-08).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 8. SP08 retained assertions

Rows: [V4-SP08-01](sdd/V4-VERIFY/inventory.md#v4-sp08-01), [V4-SP08-02](sdd/V4-VERIFY/inventory.md#v4-sp08-02), [V4-SP08-03](sdd/V4-VERIFY/inventory.md#v4-sp08-03), [V4-SP08-04](sdd/V4-VERIFY/inventory.md#v4-sp08-04), [V4-SP08-05](sdd/V4-VERIFY/inventory.md#v4-sp08-05), [V4-SP08-06](sdd/V4-VERIFY/inventory.md#v4-sp08-06), [V4-SP08-07](sdd/V4-VERIFY/inventory.md#v4-sp08-07), [V4-SP08-08](sdd/V4-VERIFY/inventory.md#v4-sp08-08), [V4-SP08-09](sdd/V4-VERIFY/inventory.md#v4-sp08-09).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 9. SP09 retained assertions

Rows: [V4-SP09-01](sdd/V4-VERIFY/inventory.md#v4-sp09-01), [V4-SP09-02](sdd/V4-VERIFY/inventory.md#v4-sp09-02), [V4-SP09-03](sdd/V4-VERIFY/inventory.md#v4-sp09-03), [V4-SP09-04](sdd/V4-VERIFY/inventory.md#v4-sp09-04), [V4-SP09-05](sdd/V4-VERIFY/inventory.md#v4-sp09-05), [V4-SP09-06](sdd/V4-VERIFY/inventory.md#v4-sp09-06), [V4-SP09-07](sdd/V4-VERIFY/inventory.md#v4-sp09-07), [V4-SP09-08](sdd/V4-VERIFY/inventory.md#v4-sp09-08), [V4-SP09-09](sdd/V4-VERIFY/inventory.md#v4-sp09-09).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 10. SP10 retained assertions

Rows: [V4-SP10-01](sdd/V4-VERIFY/inventory.md#v4-sp10-01), [V4-SP10-02](sdd/V4-VERIFY/inventory.md#v4-sp10-02), [V4-SP10-03](sdd/V4-VERIFY/inventory.md#v4-sp10-03), [V4-SP10-04](sdd/V4-VERIFY/inventory.md#v4-sp10-04), [V4-SP10-05](sdd/V4-VERIFY/inventory.md#v4-sp10-05), [V4-SP10-06](sdd/V4-VERIFY/inventory.md#v4-sp10-06), [V4-SP10-07](sdd/V4-VERIFY/inventory.md#v4-sp10-07), [V4-SP10-08](sdd/V4-VERIFY/inventory.md#v4-sp10-08), [V4-SP10-09](sdd/V4-VERIFY/inventory.md#v4-sp10-09), [V4-SP10-10](sdd/V4-VERIFY/inventory.md#v4-sp10-10), [V4-SP10-11](sdd/V4-VERIFY/inventory.md#v4-sp10-11), [V4-SP10-12](sdd/V4-VERIFY/inventory.md#v4-sp10-12), [V4-SP10-13](sdd/V4-VERIFY/inventory.md#v4-sp10-13), [V4-SP10-14](sdd/V4-VERIFY/inventory.md#v4-sp10-14), [V4-SP10-15](sdd/V4-VERIFY/inventory.md#v4-sp10-15), [V4-SP10-16](sdd/V4-VERIFY/inventory.md#v4-sp10-16), [V4-SP10-17](sdd/V4-VERIFY/inventory.md#v4-sp10-17), [V4-SP10-18](sdd/V4-VERIFY/inventory.md#v4-sp10-18), [V4-SP10-19](sdd/V4-VERIFY/inventory.md#v4-sp10-19), [V4-SP10-20](sdd/V4-VERIFY/inventory.md#v4-sp10-20), [V4-SP10-21](sdd/V4-VERIFY/inventory.md#v4-sp10-21), [V4-SP10-22](sdd/V4-VERIFY/inventory.md#v4-sp10-22), [V4-SP10-23](sdd/V4-VERIFY/inventory.md#v4-sp10-23).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

The checkpoint-owned local frontier adapter and its SP-12 consumer now pass focused race
checks on `65c499d` plus recorded hashes. The adapter resolves the owner's live draft, retries
one sealed-draft handoff, and preserves FileWriter's partial DPI result. This is provisional
lifecycle evidence; the numeric frontier still cannot establish durable publication or gaps.
The inventory addendum maps the individual assertions and independent review.

## 11. SP11 retained assertions

Rows: [V4-SP11-01](sdd/V4-VERIFY/inventory.md#v4-sp11-01), [V4-SP11-02](sdd/V4-VERIFY/inventory.md#v4-sp11-02), [V4-SP11-03](sdd/V4-VERIFY/inventory.md#v4-sp11-03), [V4-SP11-04](sdd/V4-VERIFY/inventory.md#v4-sp11-04), [V4-SP11-05](sdd/V4-VERIFY/inventory.md#v4-sp11-05), [V4-SP11-06](sdd/V4-VERIFY/inventory.md#v4-sp11-06), [V4-SP11-07](sdd/V4-VERIFY/inventory.md#v4-sp11-07), [V4-SP11-08](sdd/V4-VERIFY/inventory.md#v4-sp11-08), [V4-SP11-09](sdd/V4-VERIFY/inventory.md#v4-sp11-09), [V4-SP11-10](sdd/V4-VERIFY/inventory.md#v4-sp11-10), [V4-SP11-11](sdd/V4-VERIFY/inventory.md#v4-sp11-11), [V4-SP11-12](sdd/V4-VERIFY/inventory.md#v4-sp11-12), [V4-SP11-13](sdd/V4-VERIFY/inventory.md#v4-sp11-13), [V4-SP11-14](sdd/V4-VERIFY/inventory.md#v4-sp11-14), [V4-SP11-15](sdd/V4-VERIFY/inventory.md#v4-sp11-15), [V4-SP11-16](sdd/V4-VERIFY/inventory.md#v4-sp11-16), [V4-SP11-17](sdd/V4-VERIFY/inventory.md#v4-sp11-17), [V4-SP11-18](sdd/V4-VERIFY/inventory.md#v4-sp11-18), [V4-SP11-19](sdd/V4-VERIFY/inventory.md#v4-sp11-19), [V4-SP11-20](sdd/V4-VERIFY/inventory.md#v4-sp11-20), [V4-SP11-21](sdd/V4-VERIFY/inventory.md#v4-sp11-21), [V4-SP11-22](sdd/V4-VERIFY/inventory.md#v4-sp11-22), [V4-SP11-23](sdd/V4-VERIFY/inventory.md#v4-sp11-23), [V4-SP11-24](sdd/V4-VERIFY/inventory.md#v4-sp11-24).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 12. SP12 retained assertions

Rows: [V4-SP12-01](sdd/V4-VERIFY/inventory.md#v4-sp12-01), [V4-SP12-02](sdd/V4-VERIFY/inventory.md#v4-sp12-02), [V4-SP12-03](sdd/V4-VERIFY/inventory.md#v4-sp12-03), [V4-SP12-03b](sdd/V4-VERIFY/inventory.md#v4-sp12-03b), [V4-SP12-04](sdd/V4-VERIFY/inventory.md#v4-sp12-04), [V4-SP12-05](sdd/V4-VERIFY/inventory.md#v4-sp12-05), [V4-SP12-06](sdd/V4-VERIFY/inventory.md#v4-sp12-06), [V4-SP12-07](sdd/V4-VERIFY/inventory.md#v4-sp12-07), [V4-SP12-08](sdd/V4-VERIFY/inventory.md#v4-sp12-08), [V4-SP12-09](sdd/V4-VERIFY/inventory.md#v4-sp12-09), [V4-SP12-10](sdd/V4-VERIFY/inventory.md#v4-sp12-10), [V4-SP12-11](sdd/V4-VERIFY/inventory.md#v4-sp12-11), [V4-SP12-12](sdd/V4-VERIFY/inventory.md#v4-sp12-12), [V4-SP12-13](sdd/V4-VERIFY/inventory.md#v4-sp12-13), [V4-SP12-14](sdd/V4-VERIFY/inventory.md#v4-sp12-14), [V4-SP12-15](sdd/V4-VERIFY/inventory.md#v4-sp12-15), [V4-SP12-16](sdd/V4-VERIFY/inventory.md#v4-sp12-16), [V4-SP12-17](sdd/V4-VERIFY/inventory.md#v4-sp12-17), [V4-SP12-18](sdd/V4-VERIFY/inventory.md#v4-sp12-18), [V4-SP12-19](sdd/V4-VERIFY/inventory.md#v4-sp12-19), [V4-SP12-20](sdd/V4-VERIFY/inventory.md#v4-sp12-20), [V4-SP12-21](sdd/V4-VERIFY/inventory.md#v4-sp12-21), [V4-SP12-22](sdd/V4-VERIFY/inventory.md#v4-sp12-22), [V4-SP12-23](sdd/V4-VERIFY/inventory.md#v4-sp12-23), [V4-SP12-24](sdd/V4-VERIFY/inventory.md#v4-sp12-24), [V4-SP12-25](sdd/V4-VERIFY/inventory.md#v4-sp12-25).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

SP-12 no longer caches or aborts checkpoint drafts, or retries DPI batches independently.
Close/session-switch and constructor tests pass; the SP-10 sweep shares the same adapter.
Independent review required a real CLI assertion that Frontier/Sources remain unavailable
until shared ledger/source acceptance. That check passes as an unavailable-route assertion,
along with the existing hot-path guard. Production C-1, M1/M2 and installed recovery remain
open; no capability or migration switch was enabled.

## 13. SP13 retained assertions

Provisional correction on preparation base `7dcb962`: ledger query failures now return
`state: unavailable` with generic recovery text and no backend error disclosure. The old
`TestAlreadyTriedLedgerFailureReturnsAbsent` assertion is retired; its replacement and the
NotFound/cancel/deadline/private-error cases run through the real MCP handler fixture. Legacy
JSON fields decode, but callers that assume a closed three-state enum need a behavior update.
Successful legacy active/stale/absent answers still await M2 freshness/coverage qualification.

Original `initialize.json` and `tools-list.json` goldens remain unchanged. Their `.v2` successors
correct only instructions and seven tool descriptions, including the unsupported native eviction
and complete-capture promises. Tool names, titles, schemas and negotiated protocol versions are
unchanged. Generated MCP documentation reflects these limits.

`mcp-unavailable-red` preserves the failing old absence behavior. `mcp-unavailable-green` passed
the focused MCP and documentation race selection in 24.356 s (MCP 3.041 s, devtool 2.080 s).
Its run manifest identifies dirty input hashes. Later MCP comment clarifications and this report
change no runtime logic, test assertions, fixtures or generator inputs; the independent packet
records that dependency review. These results do not close installed-host, M2 or final V4 gates.

Rows: [V4-SP13-01](sdd/V4-VERIFY/inventory.md#v4-sp13-01), [V4-SP13-02](sdd/V4-VERIFY/inventory.md#v4-sp13-02), [V4-SP13-03](sdd/V4-VERIFY/inventory.md#v4-sp13-03), [V4-SP13-04](sdd/V4-VERIFY/inventory.md#v4-sp13-04), [V4-SP13-05](sdd/V4-VERIFY/inventory.md#v4-sp13-05), [V4-SP13-06](sdd/V4-VERIFY/inventory.md#v4-sp13-06), [V4-SP13-07](sdd/V4-VERIFY/inventory.md#v4-sp13-07), [V4-SP13-08](sdd/V4-VERIFY/inventory.md#v4-sp13-08), [V4-SP13-09](sdd/V4-VERIFY/inventory.md#v4-sp13-09), [V4-SP13-10](sdd/V4-VERIFY/inventory.md#v4-sp13-10), [V4-SP13-11](sdd/V4-VERIFY/inventory.md#v4-sp13-11), [V4-SP13-12](sdd/V4-VERIFY/inventory.md#v4-sp13-12), [V4-SP13-13](sdd/V4-VERIFY/inventory.md#v4-sp13-13), [V4-SP13-14](sdd/V4-VERIFY/inventory.md#v4-sp13-14), [V4-SP13-15](sdd/V4-VERIFY/inventory.md#v4-sp13-15), [V4-SP13-16](sdd/V4-VERIFY/inventory.md#v4-sp13-16), [V4-SP13-17](sdd/V4-VERIFY/inventory.md#v4-sp13-17), [V4-SP13-18](sdd/V4-VERIFY/inventory.md#v4-sp13-18), [V4-SP13-19](sdd/V4-VERIFY/inventory.md#v4-sp13-19), [V4-SP13-20](sdd/V4-VERIFY/inventory.md#v4-sp13-20), [V4-SP13-21](sdd/V4-VERIFY/inventory.md#v4-sp13-21), [V4-SP13-22](sdd/V4-VERIFY/inventory.md#v4-sp13-22), [V4-SP13-23](sdd/V4-VERIFY/inventory.md#v4-sp13-23), [V4-SP13-24](sdd/V4-VERIFY/inventory.md#v4-sp13-24).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.


## 14. Whole-tree and retained cross-wave verification

All commands below ran on the candidate on an otherwise idle machine.

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | PASS |
| Vet | `go vet ./...` | PASS (vet compiles test code, so every package's tests compile together) |
| Lint | `go run ./tools/devtool lint` | **PASS, exit 0 — all ten sub-checks**: golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips, runpatterns, docmarkers, coveragefloors |
| Suite | `go test ./... -count=1 -timeout=40m` | 62 packages ok, 4 packages fail; every failing row accounted for below |
| Replay gate | `TestIntegration_ReplayGateAcceptsRealGrowthFile` | PASS |
| Import graph | `devtool lint --only=importgraph` | PASS, 65 packages |

Whole-tree gate IDs [V4-ALL-01](sdd/V4-VERIFY/inventory.md#v4-all-01) through [V4-ALL-08](sdd/V4-VERIFY/inventory.md#v4-all-08)
are reconciled in the map. **`V4-ALL-08` is retired as written**: "Qompack.md byte-identical to the root
commit" was false before this work began — the file is at v1.3 with an authorized Revision log, and
read-only means read-only *to subplans*, not frozen. Its replacement assertion (changes only through an
authorized Revision-log entry with a matching `QOMPACK-ERRATA.md` record) has **no enforcing guard** in
`test/guards`, `tools/devtool` or the git hooks, and is therefore recorded MISSING, not passed.

Every remaining suite failure, with its disposition:

| Failing row | Disposition |
|---|---|
| `TestCarriedDefects_WaveReportRequiresResolution` — SP05-D1, SP06-D2, SP08-D1, SP10-D1 | **Correct.** Four carried defects remain genuinely open; six of the original ten were resolved with cited evidence. Nothing was resolved to make the guard green. |
| `TestIntegration_BeladyPMinLandsAtLowCoupling` | **Deliberately red.** See section 17. |
| `TestBudget_RebuildBloom`, `TestBudget_Open` | Co-load only; both pass in isolation (3.021 s). |
| `TestV3_HotPathUnchangedWithLedgerResident`, `TestIntegration_HotPathWarmWithRealResidentState` | Budget row B-B; see section 17. Pre-existing breach. |

Not runnable in this environment, and therefore not claimed: the ubuntu/macOS arms of the
supported-platform gate (Windows host); `govulncheck` (needs network); full CI on `verify/v4` (no such
branch, and CI remains behind the waived-open J5 billing blockage).

## 15. Exit criteria

**Not met, and not claimed.** The corrective implementation path M1 to M2 to M3 is complete and the
lint/build/vet gates pass, but three exit conditions are outstanding: reference-platform performance
evidence does not exist (section 17), four carried defects remain open (section 18), and scenario 4.4
was not written (section 16).

## 16. Integration and installed-host scenarios

**Thirteen of the fourteen section-4 cross-component scenarios are written and passing**, against real
producers rather than fakes: `TestV4_` reports 12 passing rows in `test/e2e` plus the growth guardrail
in `test/integration`. There is **no `t.Skip` anywhere** among them — the one scenario that could not be
written is absent, not faked. Six of the fourteen negative controls were verified by actually breaking
the mechanism and confirming the row went red; the remaining eight are two-arm controls that execute
both arms on every run.

**Scenario 4.4, `TestV4_FrontierAdvancementKeepsResidualSpanODelta`, was not written, deliberately.**
Two full replays differing only in `checkpoint.frontier.advanceOnSegmentClose` are byte-identical —
measured, not assumed — because that flag is read only in `internal/daemon` while the evaluation
harness's residual span derives from the last user turn rather than a durable frontier.
`TestV4_FrontierToggleIsNotConsumedByTheReplayPath` records both arms and fires when the scenario
becomes writable. **Consequence: `p4DischargedBy` points at an unwritten row, so the Phase 4 residual
bars are currently enforced by nobody.**

**Installed-host discovery after compaction (T13-HANDLE) remains unverified.** Four section-4 rows drive
the MCP handlers in-process, so the stdio transport and `cli`'s unexported Widener wiring are not
covered.

Seven defects were found by executing these gates rather than by reading diffs, and each is the kind of
thing this checkpoint exists to catch:

1. **Oversized hook payloads were silently dropped.** `doHook` returned on `core.ErrBudget` before
   spooling, and `readHookCapture` discarded the buffered bytes. Any project that tuned
   `maxPayloadBytes` down lost every delivery above it — proved against the real binary: 39,388 bytes
   against a limit of 8192 produced **zero** spool files. The host result survived; the evidence
   vanished with no record it had existed. This violated invariant 1 and invariant 4 simultaneously.
2. **The shipped daemon could not seal a checkpoint on its first PreCompact.** A ledger-less
   `SourceSet` was silently dropped by `SetSources`, and the ledger opened only on the first compaction
   — after the PreCompact that needed it. The only symptom was one Warn line and a null hook output.
3. **A DPI violation could pass silently.** A filter interposed between the segment listing and the
   writer removed already-encoded segments before `Advance` could return `ErrAlreadyEncoded`, making
   both loud-log sites unreachable. A newer safety check had swallowed the case an older, louder one
   existed to report.
4. **A leaked `eliminations.jsonl` handle.** The daemon opened the ledger on the PreCompact path but
   only `runDaemon` closed it. Windows surfaced it as a cleanup failure; on Linux it would have leaked
   silently.
5. **A test that encoded the bug.** `TestV1_HookLifecycleThroughRealBinary` asserted that the first
   PreCompact returns `{}` — which was the defect, not the contract.
6. **Sixteen unsatisfiable `-run` patterns**, six of which exited 0 while verifying nothing, plus 72
   further over-escaped patterns whose first alternative could never match. Rows had been silently
   *partially* verifying. All fixed in place; no waivers were added.
7. **An architecture violation**: `internal/mcp` importing `internal/redact`, introduced by the
   retrieval-side secret re-check. Fixed by inverting the dependency — mcp owns the interface, `cli`
   supplies the implementation — rather than amending the section 3.2 allow-set.

## 17. Performance budgets

Measured like-for-like on an idle machine, candidate versus the untouched pre-work base `a123ecc`:

| Row | Base | Candidate | Limit | Verdict |
|---|---|---|---|---|
| B-A p99 | 3.072 ms | 3.072 ms | 15 ms | PASS both, identical |
| B-B p50 | 2.304 ms | 2.304 ms | 2 ms | unchanged |
| **B-B p99** | **3.072 ms** | **3.840 ms** | **2 ms** | **FAIL on both — pre-existing breach** |
| B-E p99 | 80.634 ms | 136.285 ms | 2000 ms | PASS both (14x margin) |
| B-E_cpu p99 | 31.250 ms | 46.875 ms | 2000 ms | PASS both |

**B-B breaches its 2 ms budget on the untouched base as well, so the breach is not introduced by this
work.** The corrective work adds roughly 0.77 ms of p99 tail with p50 unchanged — the measured cost of
M1's added admission and durability, which is three fsyncs per accepted delivery where there was one.
That cost was examined and kept: the WAL sync *is* the transport-ACK boundary; the lease-journal sync is
a different file, and dropping it turns a torn tail into whole-journal degradation; the position seal is
strictly ordered after it and is what makes a truncated journal detectable, so batching it would make a
lost tail invisible, which is silent identity loss. B-E's growth is the checkpoint doing real work for
the first time and stays inside budget by 14x.

Earlier readings of this row (90.1, 81.9, 30.7 and 6.1 ms) were taken while up to eight agents were
running; they are discarded as co-load noise, not averaged in.

**Reference-platform performance is unmeasured and is not claimed.** SP06-D2, SP08-D1 and SP10-D1 each
state that their budget "has never been measured on the reference platform", and no Linux runtime
exists on this machine — `wsl -l -v` lists only a shell-less `docker-desktop` entry and the Docker
daemon is not running. The only remaining path is a CI run on a pushed branch, which is outward-facing
and awaits the user's decision.

**Belady p_min stays RED with the floor held at 0.70.** Holding the binary fixed and swapping only the
corpus, the pre-correction corpus scores 39/39 (100 %) and the corrected corpus 24/39 (61.5 %) — because
on the old corpus the budget bound on **0 of 39** events and `CrossingEdges(p_min)` was 0 in 39/39, so
every event was won for free by `0 < mean`. The historical 70 % floor was never exercised by a corpus
capable of testing it. There is **no selection regression**; the floor was never real. It stays at 0.70
pending an authorized re-derivation that should exclude the 11/39 still-free wins.

The evaluation corpus itself moved: `stock.fraction_of_opt` 0.695164 to 0.258291, attributed by
measurement entirely to the corpus (a padding-reverted run reproduces 0.258291 exactly). `phase0.json`
is M0-04-protected and unchanged; the re-baseline landed beside it as `phase0-recall.*` with all eight
old failures carried in. The replay gate now carries a corpus SHA in both run and baseline, refuses a
mismatched pairing, and exits **2 (bad input) rather than 1 (gate failure)**. Seven metrics whose
baseline denominator was zero now print `unreportable` with absolute deltas instead of meaningless
percentages; **no sign-off rationale was written for any of them, because the correct fix was to stop
making the comparison.**

## 18. Regression and carried requirements

Six of ten carried defects resolved with cited evidence: SP02-D1 (all four demand kinds now raised at
39/39 events), SP02-D2, SP02-D3 (budget binds 26/39, was 0/39), SP02-D4 (no code change — Qompack.md
v1.3 section 5.2 retired the all-blocks p_min wording), SP02-D5, and SP02-D6 (half fixed, remainder
deferred to V5-VERIFY behind a section-5.18-frozen Session field).

**Four remain genuinely open** and `TestCarriedDefects` fails exactly those four subtests: SP05-D1,
SP06-D2, SP08-D1, SP10-D1. Three of the four are reference-platform performance rows (section 17);
SP06-D2 is a budget-versus-implementation decision that lives in `plans/`.

The V3 waiver is untouched. J5 billing (run 32932419445) and the three-platform p99 backfill remain
waived-open; nothing here reinterprets them as passes or reopens V3.

Known limitations recorded rather than fixed: store quota is not operator-configurable (it lives on
`GCPolicy` because any new `config.Config` leaf breaks the frozen `schema.json`/`appendix-c.jsonc`
golden surface — future names `store.quota.maxBytes`, `store.gc.maxOutcomes`); the two delivery journals
are unbounded and hash-chained, so compacting them is a separate task with its own crash-safety
argument; frontier advancement is inert until a process's first compaction, before which crash recovery
rests on the delivery journal and WAL, which is what the nine crash cuts exercise; retention-roots
compaction retains a narrow append race that aborts safely rather than losing data; and a payload above
4 MiB in a project with **no** `.qompack` directory is still dropped without a trace, deliberately,
because a hook must not conjure state in a project that has not opted in.

## 19. Signoff

**Not signed off.** The corrective implementation is complete and the build, vet and lint gates pass in
full, but the V4 gate requires evidence that does not yet exist:

1. Reference-platform performance measurement for SP06-D2, SP08-D1 and SP10-D1 — unavailable in this
   environment, obtainable only from CI on a pushed branch.
2. Scenario 4.4 authored, or an authorized retirement with replacement evidence for the Phase 4
   residual bars.
3. Installed-host discovery and recovery after compaction (T13-HANDLE), which no in-process test covers.
4. An enforcing guard for `V4-ALL-08`'s replacement assertion, or its explicit retirement.
5. An authorized re-derivation of the Belady p_min floor, or acceptance that the row stays red.

**Wave 4 readiness.** SP-15 and SP-16 name as prerequisites the accepted contracts — SP-19,
SP-20/M1-M2, SP-13/M2, SP-10/11 M3 and SP-12 supported scheduling — all of which are delivered on this
candidate. **SP-14 alone names "verified V4"**, and it is ordered after SP-15 then SP-16 in any case. So
the corrective path unblocks SP-15 and SP-16 on contract grounds, while SP-14 remains blocked on the
five items above.

## 20. Independent review, and the re-run that followed it

An independent adversarial review of the whole candidate ran read-only after sections 14-19 were first
written. It returned **2 BLOCKER, 2 MAJOR, 2 MINOR and 0 vacuous tests**, and it found things every
other check had missed. All four blocking and major findings were fixed before this report was
finalized.

**BLOCKER 1 — the daemon discarded the record, and the shipped default lost large payloads.** The
over-budget capture fix recorded in section 16 worked only when the daemon was *down*, on the spool
path. `internal/daemon/handlers.go` mapped any outcome other than OK or Denied to `Failed` and returned
before any route, persisting nothing while still answering `OK:true`; the tests for that fix happened to
cover only the daemon-down path. Worse, `ipc.WithCapture` downgraded an otherwise-OK capture above
393,216 bytes, so on the shipped default `maxPayloadBytes = 1048576` **any hook payload over roughly
384 KiB — an ordinary 400 KB file read — was dropped entirely, Event and all**, where before this work
it was observed normally. That was a regression introduced by this work, on default configuration, and
the suite pinned the contradiction in both directions.

Resolution: a capture whose outcome is `Unavailable` is now persisted as evidence on the daemon-up path
exactly as on the spool path; `WithCapture` degrades only the capture half and never the Event; a
capture with no derived Event publishes evidence and skips the observation rather than minting a
synthetic one. The contradicting test's *fixture* was the wrong party, not its intent — it built an
oversize record and called it undecidable, when an oversize verdict is a decision made by a policy that
did run. It now uses a genuinely undecidable fixture and keeps its refusal; the two tests asserting
survival were correct and were left untouched.

**BLOCKER 2 — one bad spooled record wedged a spool file permanently.** The same verdict in
`internal/daemon/drain.go` broke the read loop with the offset unadvanced and returned before
`saveState`. Because the admission decision is baked into the spooled record, no later pass could ever
admit it, so a single malformed or over-budget hook spooled while the daemon was down blocked every
record behind it, forever. `DrainGapUnadmitted` and `DrainGapDenied` existed with zero tests.
Resolution: an unadmittable record is recorded as an explicit gap and skipped, the offset advances, and
the drain delivers what follows — proven by `TestDrainSkipsAnUnadmittableRecordAndDeliversWhatFollows`
and `TestDrainReportsADeniedRecordAsAGapAndMovesOn`. Losing one unadmittable record loudly is correct;
losing everything behind it silently is not.

**MAJOR 3 — a data race on the ledger field**, written under a `sync.Once` and read unsynchronized from
per-connection goroutines. New in this branch, and CI runs with `-race`. Fixed through a synchronized
accessor, with the race reproduced negatively first, and without reintroducing eager opening.

**MAJOR 4 — CI was permanently red and masked new failures.** The workflow runs `test/guards` and
`test/integration`, both of which contain deliberately-red rows, so a genuine new failure there was
indistinguishable from the known ones. CI now compares failing rows *and* package verdicts against two
enumerated lists and **fails if either list goes stale in either direction** — if a listed row starts
passing, or if an unlisted row fails. No blanket `continue-on-error`, no skipped packages, and the red
rows stay red locally. This also answers the reviewer's one disagreement with section 17: it accepted
the Belady evidence as decisive but objected to a permanently-red row as the mechanism, which is fair.

**Re-run after the fixes**, on the same candidate:

| Gate | Result |
|---|---|
| `go build ./...`, `go vet ./...` | PASS |
| `go run ./tools/devtool lint` | **PASS, exit 0 — all ten sub-checks** |
| `go test ./internal/... ./cmd/... ./tools/... -p 2` | 57 packages ok; one failure, `TestGC_MarkPhaseHonoursTheDeadline`, which passes 3/3 in isolation |
| `go test ./test/... -p 1` | 5 packages ok; failures are exactly the accounted-for set — the two hot-path budget rows, the four carried defects, and the deliberately-red Belady row |
| `go test -race` over daemon, cli, ipc, store, checkpoint, negknow, state | **No data races.** One failure, `TestServerCloseWithLiveConnection`, which passes 3/3 in isolation under `-race` |

No new failures were introduced by the blocker fixes, and every previously-real failure is gone. The
machine was under memory pressure during these runs, which is why they were chunked with `-p 2` and
`-p 1`; the two isolated failures above are consistent with the wall-clock sensitivity documented
throughout this report and are not accepted as passes on that basis alone.

**Areas the reviewer explicitly did not cover**, and which therefore rest on unit-level evidence only:
invariant 7, the reconstruction side of invariant 6, the rollback drill, the evaluation and replay
metric classification, `internal/rehydrate`, all timing rows, and the roughly 200 new tests
individually (scanned mechanically and sampled, not read one by one).
