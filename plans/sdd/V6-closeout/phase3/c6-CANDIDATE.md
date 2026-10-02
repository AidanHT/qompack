# Phase 3 candidate 6 (C3.1)

- Commit: `99d0b18cf7d5991aa733e70ce52a542b80aa7710` on verify/v6 ("freeze close-out candidate 6"), merging closeout/integration `9a56b30549d9762f6c8eb90df7779c962db3a2ac`
  (waves 1-16f, merged-tree checks).
- `git describe --always --tags`: `v0.2.0-1955-g99d0b18c`; tree `2a544bda820d90b89a398c21b1244940111bbbd4`;
  `sha256(git archive --format=tar 99d0b18c)` = `7bb280e91f69dc33a813037a15bfc30b272fc90a1d13052b0501facb51b74e58`.
- Product changes from candidate 5: waves 16, 16b, 16c, 16d, 16e and 16f (D53, D55, D56):
  - candidate 5's e2e rows and the contract freshness stamp;
  - the PreCompact settle of client-spooled captures in spool submode;
  - slow-disk wording and doctor rows;
  - LICENSE and third-party notices in every bundle;
  - the 8.3 deny-rule bypass closed;
  - a thrash warning no reply delivered is re-armed;
  - the server-managed permissions walk covers the whole document;
  - the PreCompact kicks the spool watcher after its seal.
- Run from `../qompack-cx-cand` (detached at it). Bundles `qompack-bundles/c6/` (`bundle -archive -version 0.3.0
  -host-validate`). windows-amd64 BUNDLE.json sha256 `a55f4c6616b2b1c17de3dbe58fe74ceb4256eb7c6dad457000faefc9d801de34`, bin/qompack.exe sha256 `2de82d25f502bcac98a6fdd6c4ee923d9c0f5fbf40512db36b31426c90bde4dc`.
  `claude plugin validate --strict --json` accepted it (`c6/host-validate.txt`).
- Merged-tree check before the freeze (D53(a); `phase3/c6/prefreeze/`, Windows, sequential, at below-normal
  priority on an evening-loaded host).
  - Functional steps pass:
    - build;
    - vet on Windows, Linux and darwin;
    - fmt;
    - gen-config-docs and gen-mcp-docs --check;
    - the lint subset with coveragefloors (runpatterns after the w16f-hpdepth waiver);
    - test/integration without its hot-path rows;
    - the other test/ packages;
    - every internal package and tools/devtool.
  - Not timing evidence: the hot-path rows (TestIntegration_HotPathWarmWithRealResidentState and X11,
    TestV3_HotPathUnchangedWithLedgerResident) breached B-A/B-B under that load and priority, 0 lost, the ledger
    adding up. The daemon ran below normal priority beside the owner's applications and the Docker VM. D28
    judges them on the overnight chain, alone at normal priority.
- Hosted CI on this candidate (verify/v6 pushed by `coordinator/freeze-night.sh`):
  - nightly `36955043924`: green;
  - ci.yml `36955046276`: green on every job except two, classified in wave 17:
    - release-dry-run: X11 on a hosted ubuntu runner at the hosted fsync tail (Q1); the job lacked the
      non-reference-disk declaration;
    - test (windows-latest): TestFault_Lifecycle/out_of_order_sessionend.
- Overnight chain (`overnight-c6.sh`, `c6/chain.log`), first half:
  - Windows isolated timing set: pass (TestIntegration_HotPathWarmWithRealResidentState included);
  - Windows -race whole tree (`devtool test-race`): pass;
  - reproducible bundles: two builds byte-identical, 91 files;
  - X11 (TestV3_HotPathUnchangedWithLedgerResident), alone and in the e2e package: fails on Windows. Its
    no-ledger arm breached B-A/B-B at a spawn floor of 30 ms p50 (14.3 ms quiet). Under investigation in wave 17
    (D53(d));
  - Linux hot-path row: fails at B-B p50 41 ms in the WSL2 container, 0 lost. Not verified in target (D53(b)).
- Owed on this candidate:
  1. the rest of overnight-c6.sh: Linux e2e timing; Linux non-root -race (tree, e2e, child), which also runs
     the 16f rows and X10 under -race; quiet.sh C5.1 on both OSes;
  2. the candidate 6 live lane (live-rerun-c6.js);
  3. C5.5 under A8.
