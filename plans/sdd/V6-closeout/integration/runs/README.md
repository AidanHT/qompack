# Integrated gates on `closeout/integration` `b070bbe` (run 1)

Two whole-tree gates started together on 2026-09-23 at about 12:00 local, with co-load declared.
They were also co-loaded with the wave-2 workflow (`wf_0d8775ab-04e`), whose agents ran focused
tests and a Linux `-race` pass of `internal/daemon` before an account usage limit stopped them.

## Linux: `linux/` (non-root, `-race`, every non-e2e package)

Run `cx-int-int1-linux-race-b070bbe-20260923T160033Z` (`linux-nonroot-gate.sh … --coload
--timeout 60m -- ALL-NON-E2E`). Every package reported. Two packages were red:

- `test/guards` `TestCarriedDefects_WaveReportRequiresResolution`: six subtests (SP06-D2,
  SP08-D1, SP08-D3, SP09-D1, SP10-D1, SP20-D2). This is expected. Those carried defects still
  await their V6 dispositions (checklist Phase 2); the guard is doing its job.
- `test/integration` `TestIntegration_HotPathWarmWithRealResidentState`: the known co-load
  timing row. It must be re-run on a quiet host (C5.1).

`test.jsonl` is stored gzipped.

## Windows: `windows/int1-windows-whole-tree.*` (`go run ./tools/devtool test`, CGO off)

Exit 1. **The run is invalid as a gate.** Test alarms set for 30 minutes fired after 58–59
minutes, and packages that normally take seconds report ~4,100 s. The host appears to have
slept or been starved for a long stretch, so every wall-clock failure in it is unusable. The
reds were re-run one at a time on a quiet machine on 2026-09-25 (`int1-win-triage-*`, same
commit, clean tree):

| Whole-tree red | Alone |
|---|---|
| `internal/daemon` `TestDeliveryOrder_FlushWaitsForABacklogThatKeepsPublishing` | PASS 7.02 s |
| `internal/daemon` `TestDeliveryOrder_FlushSettlesQueuedSessionEventsBeforeSessionEnd` | PASS 1.50 s |
| `internal/daemon` timeout inside `TestDeliveryRadix_ManyGenerationsOldRootsUsable` | PASS 2.81 s |
| `test/e2e` `TestE2EHookRoundTrip` | PASS 5.68 s |
| `test/fault` timeout inside `TestFault_PublicationBoundaries` | PASS 129.7 s (`config_json_corrupt` 61.8 s, `delivery_seal_torn_slot` 32.1 s) |
| `test/integration` `TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites` | PASS 9.85 s |
| `test/security` `TestSecurity_ReReadAnswersFromTheArchiveNotTheLiveDisk` | PASS 11.44 s |
| `test/security` `TestV6_ArchivedReadRetainsItsAuthorizationBoundary` | PASS 0.42 s |
| `test/security` `TestV6_HashAddressesDoNotBypassPathAuthorization` | PASS 1.08 s |
| `test/security` `TestV6_HostReadRulesGovernArchivedRetrieval` | PASS 1.37 s |
| `test/security` `TestSecurity_ArchivedTextIsDataNeverAnInstruction` | **FAIL 30.83 s: reproduces alone** |
| `test/guards` carried defects | same as Linux (expected) |
| `internal/mcp`, `internal/store` killed at 33 min | not re-run separately; both passed on Linux, and the whole-tree re-run on the final candidate covers them |

The one real Windows red is `TestSecurity_ArchivedTextIsDataNeverAnInstruction`. The Read
capture is indexed, but its file version never reaches `index/files.jsonl`. It passes on Linux.
It is assigned to wave-2b workstream `w2-wintriage`, together with the two slow fault subcases.

The whole-tree Windows gate must be re-run on the final candidate, on a quiet and awake host
(C3.2).
