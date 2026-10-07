# ci 37562946379 test (macos-latest): TestPreCompactSettle_ReplaysAgainOnceALiveCopyAheadOfItPublishes
Verdict (static, read-only agent, 2026-10-06 23:2x): TEST DEFECT, product correct, high confidence.
- Fixture never waits for the live worker to reach seen.begin (ingest.go:895) after Accept's ring send (ingest.go:306).
  If the settle's replay reaches Seen.begin (drain.go:858) first (S1), the drain owns `first`, publishes both Reads
  in order, nothing deferred -> counterDrainOrderingDeferred == 0 -> fixture sanity :79 fails. Product: both
  published in order, probe.calls==1, drops empty (lines 81-85 would pass).
- No path skips the ordering gate (both lines via leasedPredecessorsReady drain.go:854). Nothing darwin-specific;
  likely amplifier: whole-tree -race on 3-vCPU macos runner.
- Not wave 22: 3a22827d/62b81753 only change cfg.Dispatch (after the race). Latent since dc5b0002 (w16d-sealrow).
- Same product code (e8c62191, ci 37559750996) passed macOS test an hour earlier.
- Post-release test-only fix: settleGate `entered` signal closed when run is invoked for the held nonce; the hook's
  once.Do waits on it (bounded by liveOrderBound) after Accept.
- Confirming repro: overlay starting the worker pool only after dispatchOp returns -> deterministic :79 failure,
  :81-85 pass.
