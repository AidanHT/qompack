# V4 execution report — IN PROGRESS

**Status: IN PROGRESS — revised gate not accepted.** This is the current preparation execution report, not final signoff. Source tree: `wip/v4-preparation`, branched from `919ca3abfba938c53ddbd9a9f55224f0054375a6`; each run records its own source HEAD and dirty-input hashes. `verify/v4` has not been created.

Scope and authority
V4 retains the original SP-01–SP-13 assertions and cross-component scenarios while applying the current migration contracts. The individual reconciliations are in the [retained inventory](sdd/V4-VERIFY/inventory.md); every linked row remains UNVERIFIED unless explicitly labelled retired assertion. No old PASS result is copied.

Source and preparation evidence
Preparation artifacts are in `.v4-artifacts/` and remain provisional. `paths-baseline` reports 88.8% coverage and `paths-corrected` 89.4%, both below the unchanged 90% floor. After adding Windows UNC/NUL boundary regressions, `paths-boundaries` passes at 90.9%. Prior failures remain intact. `runner-focused` and `prep-runner-guards` pass the package selector and co-load guard. The pinned formatting check passes. No whole-tree or installed-host gate follows from these focused results.

SP-20's isolated sibling contains core contract commit `029d065` and independently reviewed drain correction `f6a8691`. Its focused and corrected race artifacts are provisional; the [independent packet](sdd/V4-VERIFY/runner-coverage-review.md) records findings and remaining limitations. Those commits have not been integrated into this preparation source. Full SP-20 M1/M2 remains incomplete.

Execution and model record
Codex native child requests: Luna/medium for inventory and Terra/high for fixture/reviewer work. Effective route, effort, and model costs were not exposed. Maximum three active children, no nested children. This report authorizes no new action; it records work already authorized by the user. No additional source, test, build, Git, runtime, or configuration mutation is authorized by this report.

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

## 11. SP11 retained assertions

Rows: [V4-SP11-01](sdd/V4-VERIFY/inventory.md#v4-sp11-01), [V4-SP11-02](sdd/V4-VERIFY/inventory.md#v4-sp11-02), [V4-SP11-03](sdd/V4-VERIFY/inventory.md#v4-sp11-03), [V4-SP11-04](sdd/V4-VERIFY/inventory.md#v4-sp11-04), [V4-SP11-05](sdd/V4-VERIFY/inventory.md#v4-sp11-05), [V4-SP11-06](sdd/V4-VERIFY/inventory.md#v4-sp11-06), [V4-SP11-07](sdd/V4-VERIFY/inventory.md#v4-sp11-07), [V4-SP11-08](sdd/V4-VERIFY/inventory.md#v4-sp11-08), [V4-SP11-09](sdd/V4-VERIFY/inventory.md#v4-sp11-09), [V4-SP11-10](sdd/V4-VERIFY/inventory.md#v4-sp11-10), [V4-SP11-11](sdd/V4-VERIFY/inventory.md#v4-sp11-11), [V4-SP11-12](sdd/V4-VERIFY/inventory.md#v4-sp11-12), [V4-SP11-13](sdd/V4-VERIFY/inventory.md#v4-sp11-13), [V4-SP11-14](sdd/V4-VERIFY/inventory.md#v4-sp11-14), [V4-SP11-15](sdd/V4-VERIFY/inventory.md#v4-sp11-15), [V4-SP11-16](sdd/V4-VERIFY/inventory.md#v4-sp11-16), [V4-SP11-17](sdd/V4-VERIFY/inventory.md#v4-sp11-17), [V4-SP11-18](sdd/V4-VERIFY/inventory.md#v4-sp11-18), [V4-SP11-19](sdd/V4-VERIFY/inventory.md#v4-sp11-19), [V4-SP11-20](sdd/V4-VERIFY/inventory.md#v4-sp11-20), [V4-SP11-21](sdd/V4-VERIFY/inventory.md#v4-sp11-21), [V4-SP11-22](sdd/V4-VERIFY/inventory.md#v4-sp11-22), [V4-SP11-23](sdd/V4-VERIFY/inventory.md#v4-sp11-23), [V4-SP11-24](sdd/V4-VERIFY/inventory.md#v4-sp11-24).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

## 12. SP12 retained assertions

Rows: [V4-SP12-01](sdd/V4-VERIFY/inventory.md#v4-sp12-01), [V4-SP12-02](sdd/V4-VERIFY/inventory.md#v4-sp12-02), [V4-SP12-03](sdd/V4-VERIFY/inventory.md#v4-sp12-03), [V4-SP12-03b](sdd/V4-VERIFY/inventory.md#v4-sp12-03b), [V4-SP12-04](sdd/V4-VERIFY/inventory.md#v4-sp12-04), [V4-SP12-05](sdd/V4-VERIFY/inventory.md#v4-sp12-05), [V4-SP12-06](sdd/V4-VERIFY/inventory.md#v4-sp12-06), [V4-SP12-07](sdd/V4-VERIFY/inventory.md#v4-sp12-07), [V4-SP12-08](sdd/V4-VERIFY/inventory.md#v4-sp12-08), [V4-SP12-09](sdd/V4-VERIFY/inventory.md#v4-sp12-09), [V4-SP12-10](sdd/V4-VERIFY/inventory.md#v4-sp12-10), [V4-SP12-11](sdd/V4-VERIFY/inventory.md#v4-sp12-11), [V4-SP12-12](sdd/V4-VERIFY/inventory.md#v4-sp12-12), [V4-SP12-13](sdd/V4-VERIFY/inventory.md#v4-sp12-13), [V4-SP12-14](sdd/V4-VERIFY/inventory.md#v4-sp12-14), [V4-SP12-15](sdd/V4-VERIFY/inventory.md#v4-sp12-15), [V4-SP12-16](sdd/V4-VERIFY/inventory.md#v4-sp12-16), [V4-SP12-17](sdd/V4-VERIFY/inventory.md#v4-sp12-17), [V4-SP12-18](sdd/V4-VERIFY/inventory.md#v4-sp12-18), [V4-SP12-19](sdd/V4-VERIFY/inventory.md#v4-sp12-19), [V4-SP12-20](sdd/V4-VERIFY/inventory.md#v4-sp12-20), [V4-SP12-21](sdd/V4-VERIFY/inventory.md#v4-sp12-21), [V4-SP12-22](sdd/V4-VERIFY/inventory.md#v4-sp12-22), [V4-SP12-23](sdd/V4-VERIFY/inventory.md#v4-sp12-23), [V4-SP12-24](sdd/V4-VERIFY/inventory.md#v4-sp12-24), [V4-SP12-25](sdd/V4-VERIFY/inventory.md#v4-sp12-25).
Disposition: all rows are **UNVERIFIED** or explicitly retired under the revised migration contracts; implementation pointers do not constitute execution evidence. Current owner must reconcile each exact assertion with SP-19 migration authority before any acceptance.

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

[V4-ALL-01](sdd/V4-VERIFY/inventory.md#v4-all-01), [V4-ALL-02](sdd/V4-VERIFY/inventory.md#v4-all-02), [V4-ALL-03](sdd/V4-VERIFY/inventory.md#v4-all-03), [V4-ALL-04](sdd/V4-VERIFY/inventory.md#v4-all-04), [V4-ALL-05](sdd/V4-VERIFY/inventory.md#v4-all-05), [V4-ALL-06](sdd/V4-VERIFY/inventory.md#v4-all-06), [V4-ALL-07](sdd/V4-VERIFY/inventory.md#v4-all-07), [V4-ALL-08](sdd/V4-VERIFY/inventory.md#v4-all-08).
No whole-tree gate, race gate, coverage gate, or replay gate is accepted by preparation artifacts.

## 15. Exit criteria

Not met. Required exit evidence includes accepted combined-tree verification, all retained rows dispositioned with executable evidence, revised capability/host-boundary checks, coverage floors, and no unexplained failures.

## 16. Integration and installed-host scenarios

Original §4.1–§4.14 scenarios remain unverified in the inventory. Installed host behavior, native compaction controls, native eviction, output setters, and native history rewriting are unsupported or unobserved; no scenario is accepted from names, comments, or local seams. M0-G0 is accepted as an existing ledger result; remaining M0 qualifications are preserved. Core prerequisite `029d065` and drain correction `f6a8691` are accepted only as bounded corrective slices. Late-client publication, durable identity/lease, privacy capture, object/reference/frontier publication, GC roots, import/backup and M2 producers remain open.

## 17. Performance budgets

No final budget is accepted. V3 waiver J5/p99 remains waived-open. The provisional `.v4-artifacts/mcp-bf-quiet.run.json` records 46.728 seconds elapsed over 200 calls, with the unchanged 250 ms gate; no other owned command ran, machine background load is unknown, and this is not final candidate evidence. This warm in-process check cannot replace the full process-spawn hot-path gate or measure SP-20's new WAL Sync cost. Other budget rows remain linked and unverified.

## 18. Regression and carried requirements

SP10–SP13 original completed history remains preserved, but combined-tree behavior and revised migration gates are unverified. Do not mark SP-20 M1/M2 passed. Preserve capability unknowns, unsupported native controls, source dirt, and the requirement for recoverable evidence.

## 19. Signoff

**Not signed off.** The revised V4 gate is open and incomplete. Final acceptance requires coordinator review of every linked inventory row, replacement evidence for retired assertions, accepted SP-20/M0 qualifications, and the complete §4/whole-tree/installed-host evidence set.



