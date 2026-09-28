# run4: post-wave-6 Linux checks on integration `898bb8b`

- `int4-linux-tree`: non-root, -race, `--coload`, every package but test/e2e. All packages pass except:
  - test/guards `TestCarriedDefects_WaveReportRequiresResolution`: the six carried-defect rows, pending
    their Phase 2 dispositions (expected).
  - test/guards `TestGuard_AReleasedLockReportsItsSealResidual`: w6-borrow's `1a3fa76` renamed
    `ensureRunning` to `ensureRunningWith`, and the guard's site list still names the old one. Fixed at
    the wave-7 merge.
  - test/integration `TestIntegration_HotPathWarmWithRealResidentState`: 591 of 2130 hot-path requests
    were deferred to the client spool (0 lost), so the structural `ba.N > bd.N` check failed.
- `int4-hotpath-alone`: the same row alone, no -race, `--count 2`: it fails the same way (586 and 592
  deferred), with B-A/B-B p50 of about 25-30 ms against the 15 ms p99 budget. The deferral count barely moves
  between -race under co-load and a lone non-race run, which points at this container's fsync cost (three
  syncs per accept), not at load. The quiet C5.1 run decides the budget (C2.8, D33).

`test.jsonl` files over 2 MB are gzipped.
