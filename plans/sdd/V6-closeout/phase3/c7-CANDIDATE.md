# Phase 3 candidate 7 (C3.1)

- Commit: `d20309c03ffc364e4cc48663be73cfbb1f2309b2` on verify/v6 ("freeze close-out candidate 7"), merging
  closeout/integration `b31d0753ff44c72b9278c2fd26404b3e4d8be2f7` (waves 1-17, merged-tree checks).
- `git describe --always --tags`: `v0.2.0-2005-gd20309c0`; tree `44d013fb4c87fe368467996e0c9591e2526352ac`;
  `sha256(git archive --format=tar d20309c0)` = `08ab21bb1cd0061fdcf300ee6b8663326b40266300fbe872bb9b44eeefb87ba8`.
- Changes from candidate 6 (D57(c)):
  - product: C7.1 only. core.Version's default and plugin.json (with its golden) read 0.3.0;
  - tests: test/fault's retention audit reads claims before evidence (D57(b)); test/guards' class check
    over every hosted job that runs fsync-bound rows (D57(a));
  - workflows: release-dry-run and release.yml's release job declare the non-reference disk (D57(a));
    release-dry-run builds the six bundles at the release version for the pre-tag byte comparison;
    `.goreleaser.yaml` publishes the draft as a pre-release;
  - docs, the inventory and plans.
- Run from `../qompack-cx-cand` (detached at it). Bundles `qompack-bundles/c7/` (`bundle -archive -version 0.3.0
  -host-validate`); windows-amd64 BUNDLE.json sha256
  `5212ae4eaa2e931266d52069e7d0c72ec2dfd2255d55421c87486ab083e1f395`; `claude plugin validate --strict --json`
  accepted it (`c7/host-validate.txt`).
- bin/ sha256, each equal to w17-release's prediction (`c7/night.log`):

  | Target | sha256 |
  |---|---|
  | darwin-amd64 | `165be7b81de89a42a47265c5e144877d98a8952522a7fbe53f62bb5cdf8f1059` |
  | darwin-arm64 | `e55d66ba34d9000d7cece456d0af14152be61c161d623decbd5c8307f6c2f78e` |
  | linux-amd64 | `f72206c3403fd06a05046950d9e475adf5489abf9d10f1b615dbb57d33f85b62` |
  | linux-arm64 | `553e005493b2466bb3148f8a7dba92cc375717249aec9c520bc60de6419b5c29` |
  | windows-amd64 | `ab8046bac3bad894c9a299c61fb38277f4179f4e33bfb088873e43edcbe33be7` |
  | windows-arm64 | `c276cc9ec7e9bf6c295a0cc9d38525a86777ea956bcc58025e252b9e888cec38` |

  Every bin/ differs from candidate 6's by one unreferenced byte (the version literal), plus darwin/arm64's ad-hoc
  signature hash; builds use -buildvcs=false (`w17-release/report.md`).
- Merged-tree check before the freeze (`c7/prefreeze/`, Windows, sequential, normal priority, at night):
  gate (build; vet on Windows, Linux and darwin; fmt; the generated-docs checks; the lint subset with runpatterns
  and coveragefloors), the other test/ packages, and every internal package with tools/ and cmd/: all pass.
- Carried from candidate 6 by the byte proof and the diff (D57(c)): the Windows isolated timing set; the Windows
  -race whole tree; reproducible bundles; the Linux non-root -race tree, e2e and child; quiet C5.1 (Windows on AC
  passes; Linux fsync-bound rows not verified in target, D53(b)); test/e2e and test/integration (pre-freeze and the
  chain); X11 on AC 3/3 (D57(g), `c6/x11-e1/`).
- Hosted CI on this candidate: ci.yml run `36981590450` (pushed by `coordinator/c7-night.sh`).
- Owed on this candidate:
  1. the live lane (`coordinator/live-rerun-c7.js`, run `wf_64f2980c-af8`), C5.6 included;
  2. C5.5 under A8 with this bundle (its BUNDLE.json SHA above);
  3. hosted ci.yml green or classified, nightly;
  4. `release-check --tag v0.3.0` on the reference host, on AC (C3.12);
  5. the pre-release, the HTTPS install rehearsal and the bin/ byte comparison (D53(h)).
