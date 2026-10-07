# Phase 3 candidate 8 (C3.1)

- Commit: `3ec62ad2e01b985640c0f1fb832df3917f766a5f` on verify/v6 ("freeze close-out candidate 8 (C3.1)"),
  merging closeout/integration `e8c621911fb73e451e97a163b702721b0ef8c7de` (waves 1-23, merged-tree checks).
  This is candidate 8's second freeze. The first, `424f0d08` (integration `77374c3c`), is superseded by
  wave 23 (D71, D72); its records are under `phase3/c8-freeze1/` and `qompack-bundles/c8-freeze1/`.
- `git describe --always --tags`: `v0.2.0-2439-g3ec62ad2`; tree `4f3deaf48a05b7145fdd16d867210ba9e5ebd7b4`;
  `sha256(git archive --format=tar 3ec62ad2)` = `5b9d925b9d9b420cd711dc4484becb3b484ee28521d99826f668aadc7b0f59e9`.
- Changes from candidate 7 (`d20309c0`), by ledger row:
  - wave 18 passprogress and waves 19 to 19i, rehydration and the free-text summary whitelist (D60, D63, D64);
  - candidate 7's live-lane failures, fixed (D59);
  - the pre-freeze audit and wave 20 (D62); wave 21's night seat (D65);
  - audit 2 and wave 22, the last whole-tree audit and its fixes (D66, D67, D68);
  - wave 23, the diff verify's privacy major and the docs (D71, D72), and the golangci-lint SA4023 fix
    (`5c35bedc`, D74).
  The product diff touches 63 non-test Go files in 13 internal packages, most in internal/daemon,
  internal/rehydrate, internal/cli and internal/checkpoint. core.Version stays 0.3.0.
- Run from `../qompack-cx-cand` (detached at it). Bundles `qompack-bundles/c8/` (`bundle -archive -version 0.3.0
  -host-validate`); windows-amd64 BUNDLE.json sha256
  `61ba9c37dda03c14c44acb6824646a8d7410751bba6f1ce3c5c864d6382dcd8b`; checksums.txt sha256
  `bd2184b6b0973c792d93ca5d8b0f9a1b83f4440565703034b737e76c2262953b`; `claude plugin validate --strict --json`
  accepted it (`c8/host-validate.txt`).
- bin/ sha256 (`c8/night.log`):

  | Target | sha256 |
  |---|---|
  | darwin-amd64 | `891ebc25138701511c43aa1aeb098f70d5c5581fba8d7868d5f178eb70b5ff7c` |
  | darwin-arm64 | `49ee0d66572b80beae85a0c2dc2f39035dce4aa1a15726ab5d40cdc3f200a55a` |
  | linux-amd64 | `e5db0447730b0a4f21233cb041b0a1e09bcda3df03c78e8e24f607df8baf9b65` |
  | linux-arm64 | `40963fae06275b81e29d4e1f6086e448820e2060b9bdaf94f203a85bf4953245` |
  | windows-amd64 | `93eb09f348774fbf2783bd607a3af41622976b2edad42798b437091eace381a9` |
  | windows-arm64 | `64061660b9d8371995888db9b6f29ee18947a57f5b7834aa6a0aa3d78e9be24f` |

- Merged-tree check before the freeze (`c8/merged-tree.txt`): tree `4f3deaf4` passes plan lint (runpatterns,
  docmarkers, coveragefloors), test/guards and test/docs.
- Pre-freeze run `c8-20261007T020207Z-3312` on integration `e8c62191` (`c8/prefreeze/`, Windows, sequential, on
  AC, at night): gate, integration, testpkgs, internal and e2efunc all pass, every step VALID. The first launch's
  pre-freeze (`c8/prefreeze.run-1/`, run `c8-20261007T011710Z-1150` on `275165e9`) refused at the gate on
  golangci-lint SA4023 only, before any freeze (D74).
- Overnight (`overnight-c8.sh`, `c8/chain.log`, outcome `c8/overnight-outcome.txt`):
  `steps=19 passed=16 failed=2 [linux-timing linux-e2e-timing] invalid_power=0 not_reference=0 skipped=0
  reported=1`, every step on AC, from 02:38 to 11:34 UTC.
  - Passed: win-timing, win-e2e-timing, win-x11-alone (Windows isolated timing and X11, on AC); win-race (the
    Windows `-race` whole tree); bundles (two builds byte-identical, `c8/bundle-diff.txt` empty); linux-tree,
    linux-e2e and linux-child (Linux non-root `-race`); c51-win (quiet C5.1: B-A p99 16.4 ms and B-B 11.3 ms
    against 50 ms, B-E 166.8 ms against 2000 ms, B-F p99 73.7 ms against 250 ms; `c8/quiet/`); release-check
    (`release-check --tag v0.3.0` in a scratch clone, all 18 steps PASS and none skipped, `c8/release-check.json`).
  - Failed, the known container class only (D53(b)): linux-timing's TestIntegration_HotPathWarmWithRealResidentState
    and linux-e2e-timing's TestV3_HotPathUnchangedWithLedgerResident fail B-A and B-B alone (p99 180 ms and 106 ms
    against 15 ms, with 592 and 590 deliveries deferred to the client spool and 0 lost); every other row of both
    steps passes (test/e2e 336 pass, 1 fail). The first freeze's re-run failed the same two rows
    (`c8-rerun-1/`). c51-linux is reported only, for the same reason.
  - C5.2 (ABBA against `cf31e01`, ten rounds, `c8/quiet-c52-*/c52-*/paired.txt`): c52-win-observer,
    c52-win-store, c52-win-checkpoint, c52-win-other, c52-linux-observer and c52-linux-store ran complete. No row is
    slower than the base with significance except two in c52-win-other: negknow BenchmarkOpen, 54.78 ms to 59.66 ms
    (1.16x, 1/10 rounds faster, p 0.021; allocations 400,568 to 260,546), inside its 300 ms budget and off the hot
    path, as D54 recorded the same drift on Linux; and symbols BenchmarkEnclosing_100KB, 1.011x. Left for the next
    C5.2 night: c116-rig, c52-linux-checkpoint and c52-linux-other.
- Hosted CI on this candidate: ci.yml run `37562946379` (pushed by `coordinator/c8-night.sh`) and nightly
  `37562945914`. The nightly is green. ci.yml attempt 1 had two reds; attempt 2 (`gh run rerun --failed`, the
  same commit) is green, and the run concluded success (D75):
  - test (macos-latest): TestPreCompactSettle_ReplaysAgainOnceALiveCopyAheadOfItPublishes's fixture-sanity count
    (`precompact_settle_retry_test.go:79`), a test defect: the fixture does not wait for the live worker to own the
    delivery, and when the settle's replay owns it the product publishes both Reads in order with nothing dropped.
    A forced-schedule overlay reproduces the count of 0 with every product assertion passing, 5 of 5. The same
    row failed ci.yml `37559750996`'s Windows job on integration `e8c62191` (same product code).
  - test (windows-latest): internal/daemon's `-count=2` binary ended at 1185 s (its timeout is 60 min) with a
    runtime-class crash dump whose first lines the job's summary does not keep, and no test JSON is uploaded.
    Cause undetermined; it did not recur on attempt 2, and the Windows `-race` whole tree and the Linux
    non-root `-race` tree passed on this tree.
- Owed on this candidate:
  1. the C5.2 night for c116-rig, c52-linux-checkpoint and c52-linux-other, and docs/architecture.md §7's
     C1.16 figure restated from c116-rig;
  2. the live re-check (`coordinator/mkrecheck8.py`, 25 sessions), C5.6 included;
  3. C5.5 under A8 with this bundle (its BUNDLE.json SHA above);
  4. the pre-release, the HTTPS install rehearsal and the bin/ byte comparison (D53(h)).
  `release-check --tag v0.3.0` on the reference host, on AC (C3.12), is done: the overnight release-check step.
