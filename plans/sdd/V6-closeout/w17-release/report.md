# Wave 17 release

Branch `closeout/w17-release`. Workflow `wf_bd12d061-33a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `bea787a1bfb38bb6414a2a086c2f0d0c8699b8b1`

### Root cause

Release bin/ bytes depend on core.Version's source default. devtool bundle stamps the version with -X github.com/qompack/qompack/internal/core.Version=0.3.0, but the Go linker keeps the original initializer's string literal ("0.1.0") in the string table, unreferenced. bindiff shows it at, for example, linux-amd64 offset 0x5fff16, between "flushstack" and "WriteMarch". release-check --tag (R7-3) requires that default to equal the tag. So candidate 6 (default 0.1.0) can only be published by a commit whose binaries differ from it in that one byte, and on darwin/arm64 also in the ad-hoc code-signature page hash that covers it. BUNDLE.json's source.commit additionally ties every zip and checksums.txt to the exact commit built. With -buildvcs=false the bin/ bytes do not depend on the commit. Base 9a56b305 reproduces all six frozen bins exactly.

### Summary

PRODUCT BYTES CHANGE, SO THIS NEEDS A NEW CANDIDATE. C7.1 (core.Version, plugin.json and the tag all say 0.3.0) cannot be met with candidate 6's bin/ bytes. Commit 4cfc19d changes `var Version = "0.1.0"` to `"0.3.0"`. Every bundle already stamps 0.3.0 through `-X`, and `qompack version` prints 0.3.0 from the frozen exe, the base exe and the bumped exe alike. But the Go linker keeps the initializer's string literal in the binary even though nothing references it. So each of the six binaries differs from c6 in exactly one byte ("0.1.0" becomes "0.3.0" in the string table). darwin/arm64 also differs in its 32-byte ad-hoc code-signature page hash. Sizes are equal, and nothing else in the binaries differs.

Why the frozen commit cannot be tagged instead: `release-check --tag` (releaseCheckVersion, ruling R7-3) fails unless the tag's version equals core.Version's compiled default and `git describe --tags --exact-match` names that tag. 99d0b18 has 0.1.0, so release.yml's gate refuses v0.3.0 there, and changing that gate would weaken a check. So the release has to come from a commit carrying 0.3.0. Its binaries are therefore candidate 7's, not candidate 6's.

== (1) C7.1: what changed, with byte evidence ==
Files: internal/core/version.go, plugin/.claude-plugin/plugin.json and testdata/golden/plugin/.claude-plugin/plugin.json. Only the `version` line changed in each. The golden's historical homepage, which the test rewrites, is untouched.
- The bumped source plugin.json is byte-identical to the plugin.json inside every frozen c6 bundle (sha256 c9702f1c…, 383 bytes).
- Failing test first:
  - With only version.go bumped, both of these failed on plugin.json drift (runs/pluginmanifest-red-before-json.log):
    - `go test -p 2 -count=1 -run '^TestManifest_GoldenBytes$' ./internal/pluginmanifest/`
    - `go test -p 2 -count=1 -run '^TestBundle_OnDiskMatchesGenerator$' ./internal/pluginmanifest/`
  - With the two JSON files bumped, both PASS.
  - This is not a golden regenerated to hide broken output. D1 chooses the version, and docs/release.md §1 says the constant, the plugin/ tree and its golden move in one commit.

Builds: frozen invocation, GOFLAGS=-p=2, in the -race window after "linux timing exit=" (04:17:32Z).
1. Base 9a56b305 (code tree identical to 99d0b18, which differs from it only under plans/), output `<scratch>/w17/release/bundles-base`:
   - 78 of 91 files are identical, including all six bin/ binaries, every plugin tree file, LICENSE, the notices and each per-bundle checksums.txt.
   - The 13 that differ are the 6 BUNDLE.json, the 6 zips and the top-level checksums.txt. In each BUNDLE.json the only differing field is `source.commit` (99d0b18 vs 9a56b305). In each zip the only differing member is BUNDLE.json.
   - So bin/ bytes do not depend on the commit (-buildvcs=false), and the zip timestamps are fixed at the DOS epoch.
2. Version commit 4cfc19d, output `<scratch>/w17/release/bundles`: 66 identical, 25 differ.
   - The 25: 6 bin (one byte each, as above), 6 BUNDLE.json (source.commit plus the bin sha in `files`), 6 per-bundle checksums.txt (the bin line), 6 zips and checksums.txt. Nothing else differs.
3. Chain bundleA (99d0b18, no --host-validate) equals frozen c6 for all 91 files, so --host-validate does not alter the bytes.

Hashes: full listings are in runs/sha256-{frozen-c6,base-9a56b305,head-4cfc19d}.txt.
- Frozen c6 bins (16-character prefixes):
  - darwin-amd64 90d56455c4b96386
  - darwin-arm64 2886396cf89d7eca
  - linux-amd64 3b39d67dd67015b1
  - linux-arm64 dda35c7fd5c5c3ac
  - windows-amd64 2de82d25f502bcac
  - windows-arm64 dd4dc8fa6f92cbad
- The base bins are identical to these.
- 0.3.0-commit bins:
  - darwin-amd64 165be7b81de89a42
  - darwin-arm64 e55d66ba34d9000d
  - linux-amd64 f72206c3403fd06a
  - linux-arm64 553e005493b2466b
  - windows-amd64 ab8046bac3bad894
  - windows-arm64 c276cc9ec7e9bf6c
- Because bins do not depend on the commit, a candidate 7 that is c6 plus only this commit must reproduce exactly these bin hashes.

Design that gives full byte equality (D53(h)(4)):
- Freeze candidate 7 at the version commit, merged the usual --no-ff way.
- Fast-forward develop and main to that freeze commit.
- Tag that exact commit.
- release.yml then builds from the same commit as the freeze. BUNDLE.json, the zips and checksums.txt would equal the frozen c7 assets too, not only bin/, provided a Linux runner cross-compiles the same bytes. Go 1.21+ toolchains are designed to be host-independent with -trimpath. No V6 hosted run has ever reached the bundle step, so this is unproven until R4.
- Tagging a later docs-only commit is acceptable. bin/ and the 12 non-identity files still match, and BUNDLE.json, the zips and checksums.txt then differ only by source.commit. In that case put every doc that must ship in the tagged tree into c7 before freezing.

Other checks on HEAD, all PASS:
- `go run ./tools/devtool fmt-check`; plugin-validate (OK, 9 files, 6 commands, 7 hook events); gen-config-docs, gen-mcp-docs and gen-command-docs with --check.
- `go vet` on internal/core and internal/pluginmanifest, on Windows and with GOOS=linux.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`, about 4 min.
- Focused tests that read core.Version (each run alone by exact name):
  - `go test -p 2 -count=1 -run '^TestVersion_IsSet$' ./internal/core/`
  - `go test -p 2 -count=1 -run '^TestManifest_VersionFlowsThrough$' ./internal/pluginmanifest/`
  - `go test -p 2 -count=1 -run '^TestManifest_PluginJSONShape$' ./internal/pluginmanifest/`
  - `go test -p 2 -count=1 -run '^TestReleaseCheckVersion$' ./tools/devtool/`
  - `go test -p 2 -count=1 -run '^TestReleaseCheckMarketplace$' ./tools/devtool/`
  - `go test -p 2 -count=1 -run '^TestReleaseCheckStepsAreOrderedAndCoverCILocal$' ./tools/devtool/`
  - `go test -p 2 -count=1 -run '^TestDoctor_NamesTheVersionAndDoesNotProbeTheHost$' ./internal/cli/`
  - `go test -p 2 -count=1 -run '^TestSpecs_PinInstalledSurface$' ./internal/commands/`
  - `go test -p 2 -count=1 -run '^TestWriteInitializedObservable$' ./internal/mcp/`
- `go run ./tools/devtool release-check --tag v0.3.0` at the untagged HEAD stops at version agreement with "HEAD carries no tag". That means the 0.3.0 == core.Version half passes and only the tag is missing, as expected. All logs are in plans/sdd/V6-closeout/w17-release/runs/, together with the comparison scripts cmpbundles.py and bindiff.py.

== (3) What release-check runs ==
It stops at the first FAIL and always writes dist/release-check.json.
1. version agreement: SKIPPED without --tag. With --tag vX it fails unless the tag starts with v, X equals core.Version's compiled default, and `git describe --tags --exact-match` equals vX.
2–9. ci-local, reused:
   - fmt-check
   - lint, with every sub-check including stubskips
   - vet
   - build
   - test: the whole tree in two passes. First the tree minus test/e2e with QOMPACK_UNDER_COLOAD=1 (the non-reference-disk declaration taken back), then test/e2e alone, which inherits the caller's disk declaration. Each pass at -timeout=30m.
   - cover: the same two passes with -coverprofile, plus the coverage floors.
   - plugin-validate
   - gen-config-docs --check
10. build-all: six cross-compiles.
11. gen-mcp-docs and gen-command-docs, with --check.
12. `go test -count=1 ./test/guards/`.
13. govulncheck ./... (network; SKIPPED with --skip-vulncheck).
14. licenses --check.
15. real-binary determinism: the host target assembled twice, at the --tag version with --tag, otherwise at the git describe version.
16. rollback rehearsal: confirms the names with `-list`, then runs three internal/store tests and three test/e2e tests. These include TestInstall_HostCLIInstallUpgradeUninstall, which drives the real `claude plugin` CLI in a temporary CLAUDE_CONFIG_DIR and fingerprints the real ~/.claude, reading it only.
17. plugin-validate.
18. marketplace: the generator runs for the tag over placeholder digests and is validated, plus `claude plugin validate --strict --json` when the CLI is on PATH.

With --tag, only steps 1, 15 and 18 change.

I did not run it, because steps 6, 7 and 12 are whole-tree tests. Expected wall time:
- Hosted ubuntu: about 60–75 min. Run 36955046276 spent lint 15.5 min, test 14 min, and cover 14.7 min before it failed.
- This laptop: about 2.5–3 h. The SP-17 record plans/sdd/V6-SP-17-packaging-hardening-and-release/release-check.json totals 8451 s, before the marketplace step existed and on a smaller tree.

== (2) THE 0.3.0 RELEASE RUNBOOK (D53(h) order; every outward step needs the coordinator's D33/D4 decision) ==
Placeholders:
- C7 = candidate 7's freeze commit.
- B7 = C:/Users/Quant/Documents/Programming/Projects/qompack-bundles/c7.
- RUNS = plans/sdd/V6-closeout/w17-release/runs.
- Repository AidanHT/qompack.
- Origin today: develop=9c84e31 (an ancestor of verify/v6), NO main branch, NO tags. Local tags v0.0.1, v0.1.0, v0.2.0 and wave1/* exist only locally.

R0. Candidate 7 (local).
- Command: merge closeout/w17-release into closeout/integration, plus any docs that must ship in the tagged tree (README status, CHANGELOG [0.3.0], the docs/release.md status, install.md §9). Freeze as usual: --no-ff into verify/v6, giving C7. From a clean checkout of C7 (`git status --porcelain` empty), run `go run ./tools/devtool bundle -archive -version 0.3.0 -host-validate -out B7`.
- Verify:
  - `python RUNS/cmpbundles.py qompack-bundles/c6 B7` shows exactly the 25 differences above.
  - `python RUNS/bindiff.py <c6 bin> <c7 bin>` shows one byte per target, plus the signature hash on darwin-arm64.
  - B7's bin lines equal RUNS/sha256-head-4cfc19d.txt.
  - Run a second build and compare it with B7: 91 of 91 identical.
- Carry c6's evidence forward with a note naming the three changed files.
- Outward: none. Rollback: discard C7.

R0b. Hosted CI on C7 (C7.2, D53(h)(1)).
- Command: `git -C ../qompack-v6 push origin verify/v6`, then `gh run list --branch verify/v6 --limit 2`.
- Outward: a branch push; ci.yml and nightly.yml run.
- Verify: release-dry-run green once the ci seat's F1 fix has landed; every other red classified.
- Rollback: none needed.

R1. Merge path (D4, C7.3), fast-forward only so the tag commit is the freeze commit.
- Commands:
  - `git merge-base --is-ancestor origin/develop C7`
  - `git -C ../qompack-develop status --porcelain` must print nothing. develop is checked out in that sibling worktree, so updating develop from the main repo fails.
  - `git -C ../qompack-develop merge --ff-only C7`
  - `git -C ../qompack-develop push origin develop`
  - `git push origin C7:refs/heads/main` creates main on origin. Local main e7dd4ad is an ancestor, and no worktree has it checked out.
- Outward: develop is the default branch, so the public README moves; main appears.
- Verify: `git ls-remote origin refs/heads/develop refs/heads/main` shows C7 for both.
- Rollback: `git push origin :refs/heads/main` (main did not exist before). For develop: `git push --force-with-lease=refs/heads/develop:C7 origin 9c84e31d596ff2357cceeed42af240c8b6642dd5:refs/heads/develop`. That is destructive, so use it only before anything builds on develop.
- Branch protection (C7.3): turn it on only after deciding how the marketplace PR (opened with GITHUB_TOKEN, which runs no CI) can satisfy required checks; see F10.

R2. release-check --tag on the reference host, in a quiet window when the owner says go.
- Commands:
  - `git worktree add --detach ../qompack-rel-v030 C7`
  - `git tag -a v0.3.0 -m "Qompack 0.3.0" C7` (local only, unsigned)
  - `cd ../qompack-rel-v030 && go run ./tools/devtool release-check --tag v0.3.0`
- It must exit 0 with every step PASS. Copy dist/release-check.json into the evidence.
- GITHUB_ACTIONS is unset here, so the fsync rows ARE gated (Q1).
- Outward: none. Rollback: `git tag -d v0.3.0`.

R3. Tag push (C7.4).
- Command: `git push origin refs/tags/v0.3.0`. NEVER use `git push --tags` or `--follow-tags`: v0.0.1, v0.1.0 and v0.2.0 would each fire release.yml.
- Outward: a public tag. release.yml runs release-check --tag, `bundle --version 0.3.0 --archive --host-validate`, marketplace, attestation and goreleaser, and produces a DRAFT release (not public).
- Verify:
  - `gh run watch <release.yml run id>`
  - `gh release view v0.3.0 --json isDraft,isPrerelease,assets` shows isDraft=true and 10 assets: 6 zips, checksums.txt, marketplace.json, LICENSE and THIRD_PARTY_NOTICES.md.
- Rollback: `gh release delete v0.3.0 --yes; git push origin :refs/tags/v0.3.0; git tag -d v0.3.0`. The draft was never public.

R4. bin/ byte check on the DRAFT, before anything is public. This is earlier than D53(h)(4) asks, and it is repeated in R6.
- Commands:
  - `gh release download v0.3.0 -D dist/published -p 'qompack-plugin-*.zip' -p checksums.txt -p marketplace.json`
  - `(cd dist/published && sha256sum --strict -c checksums.txt)`
  - `diff <(sort dist/published/checksums.txt) <(sort B7/checksums.txt)`
  - `cd dist/published && for z in qompack-plugin-0.3.0-*.zip; do python -m zipfile -e "$z" "${z%.zip}"; done`
  - `python RUNS/cmpbundles.py B7 dist/published`
  - `gh attestation verify dist/published/qompack-plugin-0.3.0-windows-amd64.zip --repo AidanHT/qompack`
- Pass: if the tag is C7, all 91 files are identical, and the diff is empty. If the tag is a docs-only descendant, the only differences are the 6 BUNDLE.json (source.commit), the 6 zips and checksums.txt, and all six bin/ files are identical.
- Any bin/ difference means STOP: delete the draft and the tag (R3 rollback) and diagnose cross-host reproducibility.
- Then replace the stale generated notes (F3): `gh release edit v0.3.0 --notes-file <v0.3.0-notes.md>`. Include the D53(h) statement that Windows timings were taken under a D32-excluded path, D53(i)'s host-seen hook p50/p95, and the untested targets.

R5. Publish as a PRE-RELEASE (D53(h)(2)).
- Command: `gh release edit v0.3.0 --draft=false --prerelease --latest=false`, in ONE call. Two edits, or the UI's "Publish release" with the pre-release box unticked, would publish a FULL release: `released` fires, marketplace.yml opens its PR, and the release becomes Latest.
- Outward: a public pre-release, with assets on HTTPS. The `published` and `prereleased` events fire; `released` does not, so marketplace.yml does not run.
- Verify:
  - `gh release view v0.3.0 --json isDraft,isPrerelease` shows false/true.
  - `gh run list --workflow marketplace.yml --limit 3` shows no new run.
  - `curl -sSLI https://github.com/AidanHT/qompack/releases/download/v0.3.0/marketplace.json` ends in 200.
- Rollback: `gh release edit v0.3.0 --draft=true`, or delete it.

R6. Repeat the R4 comparison on the anonymously served bytes.
- Command: `curl -sSLO https://github.com/AidanHT/qompack/releases/download/v0.3.0/<each zip and checksums.txt>`, then the same checks as R4.
- Outward: none.

R7. HTTPS install rehearsal (D53(h)(3), C7.5).
(a) Windows, from the qompack-windows-amd64 entry:
- Commands:
  - `export CLAUDE_CONFIG_DIR=<scratch>/cc-https`
  - `claude --version` (must be 2.1.224 or later)
  - `claude plugin marketplace add https://github.com/AidanHT/qompack/releases/download/v0.3.0/marketplace.json`
  - `claude plugin install qompack-windows-amd64@qompack -s user`
  - `claude plugin list --json`
  - `find "$CLAUDE_CONFIG_DIR/plugins" -path '*bin/qompack.exe' -exec sha256sum {} +`
- The hash must equal B7's windows-amd64 bin. Then run the live UAT-01/C4.1 session through the coordinator's live-lane harness, with the D32 exclusion considered for the cache path.
- Rollback: `claude plugin uninstall qompack-windows-amd64@qompack -s user; claude plugin marketplace remove qompack`, then delete the config directory.
(b) Linux, no model: a fresh disposable container, not the owner's supabase stack, and only with the owner's go.
- Commands:
  - `npm i -g @anthropic-ai/claude-code@2.1.280`
  - as a NON-root user: `export CLAUDE_CONFIG_DIR=/tmp/cc`, then the same marketplace add, and `claude plugin install qompack-linux-amd64@qompack -s user`
  - `B=$(find /tmp/cc/plugins -path '*bin/qompack' -type f); stat -c '%a' "$B"; sha256sum "$B"; "$B" version`
- Pass: mode has u+x (the zip stores 0100755 with a Unix creator), the sha equals B7's linux-amd64 bin, and the command prints 0.3.0.
- Mode 644 answers C7.5's open question negatively: the exec-form hooks would fail with EACCES on linux/darwin. That decision blocks R8 for those entries.
- Rollback: discard the container.

R8. Promote (D53(h)(5)).
- Command: `gh release edit v0.3.0 --prerelease=false --latest`
- Outward: becomes Latest. `released` fires, and marketplace.yml re-downloads, re-verifies and regenerates the marketplace, then opens a PR from marketplace/v0.3.0 onto develop. This needs Settings > Actions > "Allow GitHub Actions to create and approve pull requests".
- Verify: `gh run list --workflow marketplace.yml`; `gh pr list --head marketplace/v0.3.0`.
- Rollback: `gh release edit v0.3.0 --prerelease`, plus `gh pr close <n> --delete-branch`.

R9. Marketplace step (C7.5).
- Review the PR: its diff must be only .claude-plugin/marketplace.json, with pins equal to checksums.txt. GITHUB_TOKEN PRs start no CI, so push to the branch or re-run CI. Then `gh pr merge <n>`.
- Verify, in a clean CLAUDE_CONFIG_DIR:
  - `claude plugin marketplace add https://github.com/AidanHT/qompack.git`
  - `claude plugin install qompack-windows-amd64@qompack -s user`
  - the sha matches B7.
- Rollback: a revert PR on develop.

R10. Defender (owner only, D32; the coordinator sends nothing to Microsoft).
- The owner runs `& "$env:ProgramFiles\Windows Defender\MpCmdRun.exe" -Scan -ScanType 3 -File <downloaded qompack.exe> -DisableRemediation` and, if flagged, submits it at https://www.microsoft.com/wdsi/filesubmission. Record the outcome in the notes and in troubleshooting.

== Workflow flags (ci seat owns .github/ and .goreleaser.yaml; I edited neither) ==
- F1, BLOCKER: release.yml gates the release on hosted fsync timing.
  - Its release-check step runs ci-local test and cover, whose test/e2e pass judges TestV3_HotPathUnchangedWithLedgerResident's B-A and B-B on ubuntu with no QOMPACK_NONREFERENCE_DISK.
  - Candidate 6's release-dry-run (job 110676058084) failed exactly there: cover, B-A p99 49.152 ms and B-B p99 49.152 ms against 15. The test step's e2e pass got lucky in the same job.
  - Fix: set `QOMPACK_NONREFERENCE_DISK: '1'` on release.yml's job and on ci.yml's release-dry-run (Q1 third option / D53(e); it is honoured only where GITHUB_ACTIONS=true). Add both to test/guards/nonrefdisk_test.go's job lists, with a release.yml assertion. Correct release-dry-run's comment that it "runs no wall-clock budget".
  - R2's local run still gates the rows.
- F2: goreleaser's `draft: true` without `prerelease` means the UI's default publish is a FULL release. Suggest adding `release.prerelease: true`; TestGoreleaserBuildsNothingAndDraftsTheRelease would need to follow. Otherwise follow R5 exactly.
- F3: the release notes come from `release-scope --markdown` over plans/sdd/V6-SP-17-packaging-hardening-and-release, SP-17 records from 2026-09-16. They are stale for V6 and must be replaced (R4).
- F4: no V6 hosted release-dry-run has ever reached the bundle or marketplace steps (36816905394 failed in test, 36905843834 on cover floors, 36955046276 on the cover e2e hot path). Hosted cross-host reproducibility is therefore unproven. Suggest an extra dry-run step `bundle --version 0.3.0 --archive`, whose bin/ (independent of the commit) can be compared with B7 before tagging.
- F5: the Go version and flags match the freeze: go1.26.6 pinned, GOTOOLCHAIN=local, CGO_ENABLED=0, and one goBuildArgs giving `-trimpath -buildvcs=false -ldflags "-s -w -buildid= -X …/internal/core.Version=0.3.0"`. The differences:
  - the host OS (ubuntu vs windows): expected not to matter, unproven
  - GOFLAGS=-p=4 in the freeze environment: no effect on output
  - --evidence: written outside the bundle
  - GOAMD64 v1 on both.
- F6: checksums are not skipped. goreleaser's are disabled by design, devtool's checksums.txt is uploaded, and marketplace.yml re-verifies.
- F7: release.yml has no environment approval or concurrency, so any v* tag push fires it.
- F8: goreleaser-action floats on '~> v2'.
- F9: tag-day govulncheck can turn red on a new advisory.
- F10: the marketplace PR gets no CI under GITHUB_TOKEN, and required checks would block it.

== Stale text for the docs seat (not edited; outside my files) ==
- README.md lines 22–30 ("plugin.json declares version 0.1.0", "Until that commit lands, qompack version prints 0.1.0").
- docs/release.md line 7 ("V6 release status: blocked").
- docs/install.md §9 ("nothing is published yet").
- tools/devtool/releasechecksteps.go:170's comment "core.Version is `0.1.0`". Devtool is mine only for a proven bug, so I left it.

Machine limits were respected:
- All builds and tests ran after "linux timing exit=" and before "linux race exit=", one at a time at -p 2.
- No hot-path rows, benchmarks, Docker, other worktrees or real ~/.claude.
- My two stashes were popped. The other stashes in the list belong to other branches and were not touched.

### Commits

- 4cfc19dbd8f535484ed9c8ae4208a4496292a8b0 chore(release): set the version to 0.3.0
- bea787a1bfb38bb6414a2a086c2f0d0c8699b8b1 docs(v6): record the w17-release bundle byte comparison

### Tests

- `go run ./tools/devtool bundle -archive -version 0.3.0 -out <scratch>/w17/release/bundles-base (base 9a56b305, clean)` — exit 0, 40 s; vs frozen c6: 78/91 identical, 13 differ (6 BUNDLE.json source.commit only, 6 zips, checksums.txt); all six bin/ identical
- `go run ./tools/devtool bundle -archive -version 0.3.0 -out <scratch>/w17/release/bundles (HEAD 4cfc19d, clean)` — exit 0; vs frozen c6: 66/91 identical, 25 differ; each binary differs by 1 byte (unreferenced "0.1.0" literal), darwin-arm64 also its 32-byte ad-hoc signature hash
- `python cmpbundles.py qompack-bundles/c6 phase3/c6/bundleA` — 91/91 identical: --host-validate does not alter bundle bytes
- `qompack.exe version (frozen c6, base, 0.3.0 builds; fake HOME/USERPROFILE)` — all print 0.3.0, exit 0
- `go test -p 2 -count=1 -run '^TestManifest_GoldenBytes$' ./internal/pluginmanifest/ (version.go bumped, JSON not yet)` — FAIL (red first): plugin.json drifted from golden
- `go test -p 2 -count=1 -run '^TestBundle_OnDiskMatchesGenerator$' ./internal/pluginmanifest/ (version.go bumped, JSON not yet)` — FAIL (red first): plugin/.claude-plugin/plugin.json content differs
- `go test -p 2 -count=1 -run '^TestManifest_GoldenBytes$' ./internal/pluginmanifest/` — PASS after the JSON bump
- `go test -p 2 -count=1 -run '^TestBundle_OnDiskMatchesGenerator$' ./internal/pluginmanifest/` — PASS after the JSON bump
- `go test -p 2 -count=1 -run '^TestVersion_IsSet$' ./internal/core/` — ok
- `go test -p 2 -count=1 -run '^TestManifest_VersionFlowsThrough$' ./internal/pluginmanifest/` — ok
- `go test -p 2 -count=1 -run '^TestReleaseCheckVersion$' ./tools/devtool/` — ok
- `go test -p 2 -count=1 -run '^TestReleaseCheckMarketplace$' ./tools/devtool/` — ok
- `go test -p 2 -count=1 -run '^TestDoctor_NamesTheVersionAndDoesNotProbeTheHost$' ./internal/cli/` — ok
- `go test -p 2 -count=1 -run '^TestSpecs_PinInstalledSurface$' ./internal/commands/` — ok
- `go test -p 2 -count=1 -run '^TestWriteInitializedObservable$' ./internal/mcp/` — ok
- `go run ./tools/devtool fmt-check; plugin-validate; gen-config-docs --check; gen-mcp-docs --check; gen-command-docs --check; go vet ./internal/core/ ./internal/pluginmanifest/ (Windows and GOOS=linux)` — all exit 0; plugin-validate OK (9 files, 6 commands, 7 hook events); docs up to date
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0, all eight PASS
- `go run ./tools/devtool release-check --tag v0.3.0 (untagged HEAD)` — stops at version agreement: HEAD carries no tag (the 0.3.0 == core.Version half passes; expected)
- `go run ./tools/devtool release-check (no --tag)` — NOT RUN: it runs whole-tree tests (ci-local test, cover, test/guards); hosted ~60-75 min, this laptop ~2.5-3 h

### Criterion changes

- testdata/golden/plugin/.claude-plugin/plugin.json version 0.1.0 -> 0.3.0 (one line). Rationale: owner decision D1 sets the release version, and docs/release.md section 1 requires core.Version and the generated plugin.json to move in the release's own version commit. The golden pins the generator's bytes at core.Version, so it moves with the constant. The test's historical-homepage rewrite and every other byte are unchanged. This is not output regenerated to hide a defect: the bumped source plugin.json equals the plugin.json inside every frozen c6 bundle byte for byte.

### Open issues

- Candidate 7 is required: C7.1 changes one unreferenced byte in every bundled binary (plus the darwin/arm64 signature hash), so the c6 bin/ bytes cannot be published from any commit that passes release-check --tag. Freeze c7 at the version commit (or c6 plus this commit and the docs that must ship), build it into qompack-bundles/c7, and verify with RUNS/cmpbundles.py and bindiff.py.
- F1 (ci seat, blocker for release.yml): release-check's ci-local test and cover judge test/e2e's fsync-bound hot-path rows on hosted ubuntu with no QOMPACK_NONREFERENCE_DISK. Candidate 6's release-dry-run failed there (B-A/B-B p99 49.152 ms vs 15). Add the declaration to release.yml and to ci.yml's release-dry-run, plus the matching nonrefdisk guard entries.
- F4: no V6 hosted run has built bundles, so whether a Linux runner cross-compiles the same bin/ bytes is unproven until R4. A dry-run step at --version 0.3.0 would prove it before tagging.
- F2: a goreleaser draft publishes as a FULL release by default. Use R5's single gh call, or set release.prerelease: true.
- F3: the release notes are generated from SP-17-era evidence (2026-09-16) and must be replaced before publishing.
- C7.5 exec-bit question is still open: it needs the published pre-release and a Linux non-root install (R7b).
- Docs seat: README.md lines 22-30, docs/release.md line 7, docs/install.md section 9 status, and the releasechecksteps.go:170 comment are stale after the version commit.
- The local release-check --tag (R2) has not been run: about 2.5-3 h of whole-tree work on the reference host in a quiet window.

### Needs the owner

- Accept candidate 7 = candidate 6 + chore(release) 4cfc19d (bundled bytes change by one unreferenced string byte per binary; behaviour and qompack version output identical) and decide whether c6 live-lane/C5.5 evidence carries forward by this byte proof or is re-run on the c7 bundle (recommend: carry forward, and run the R7 HTTPS rehearsal on the c7/release bytes).
- Merge path: fast-forward develop and main to the c7 freeze commit and tag that exact commit (full byte equality of zips/checksums/BUNDLE.json with the freeze), versus tagging a later docs-only commit (bin/ equality only).
- Apply the existing Q1/D53(e) report-only ruling to release.yml and ci.yml release-dry-run (QOMPACK_NONREFERENCE_DISK), so a hosted fsync tail cannot block the tag's gate; the reference-host release-check still gates those rows.
- Whether to set .goreleaser.yaml release.prerelease: true so the default publish is a pre-release.
- Linux exec-bit outcome (R7b): if the host drops the 0755 mode on zip extraction, whether linux/darwin entries ship in 0.3.0.
- Owner-only: the D32 Defender scan and false-positive submission of the released qompack.exe; the repository setting allowing Actions to open PRs; the branch-protection settings versus the GITHUB_TOKEN marketplace PR.

## Independent review

### review:release: needs-fixes

- **major** `Implementer summary, runbook step R2 (release-check --tag on the reference host); evidence C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/c6/p3-win-e2e-timing.log:1,18-19 and p3-win-x11-alone.log:18-19,144` — R2 says the local `release-check --tag v0.3.0` 'must exit 0 with every step PASS' and that the fsync rows are gated locally. Candidate 6's own evidence from the reference host says that run fails today, and the runbook does not mention it. release-check's `ci-local test` step runs test/e2e on its own, the same command as phase3.sh's win-e2e-timing (`go test -count=1 -timeout=30m ./test/e2e`, with co-load taken back). On c6 that command failed on exactly one row: TestV3_HotPathUnchangedWithLedgerResident on Windows. Its `cover` step judges the same row again with coverage instrumentation on. The gate stops at the first FAIL, so determinism, the rollback rehearsal and the marketplace steps would never run. The binaries for c7 differ by one unreferenced byte, so c7 inherits this result. The checklist item for this gate, C3.12 (`release-check` with no tag green), is also still unchecked.
  - Evidence: chain.log: `run p3-win-e2e-timing exit=1`, `run p3-win-x11-alone exit=1`. p3-win-e2e-timing.log: `--- FAIL: TestV3_HotPathUnchangedWithLedgerResident`, B-A p99=81.920ms and B-B p99=57.344ms against limit=50.000ms. Running it by itself: B-A p99 98.304ms, B-B 81.920ms. tools/devtool/test.go wholeTreePasses: the e2e pass sets env {QOMPACK_UNDER_COLOAD: ""} and inherits the caller's disk declaration. Outside GitHub Actions, QOMPACK_NONREFERENCE_DISK is ignored (internal/obs/nonrefdisk.go:60-63). In V6-CLOSEOUT-CHECKLIST.md, C3.12 is `[ ]` and D53(d) (re-run X11 alone; if it reproduces, find the cause) has no recorded outcome for c6.
  - Fix: Give R2 an explicit precondition and an expected outcome. On current evidence it FAILS at `ci-local test` on the X11 Windows row, so R2/R3 are blocked until D53(d) has a recorded disposition for c6/c7, or a quiet-window run passes (D53(b)'s quiet C5.1 passed against 50 ms). Note that `cover` re-judges the row with coverage on. Name C3.12 as the open prerequisite. Do not present R2 as a formality that takes 2.5-3 h, and do not suggest any local waiver: NONREFERENCE_DISK is ignored locally, by design.
- **minor** `.github/workflows/release.yml:55 (attest-build-provenance); implementer summary, runbook R3 Outward/Rollback` — R3 says the draft 'was never public' and that the rollback is a full undo (delete the release, the remote tag and the local tag). The repository is PUBLIC. When the tag is pushed, actions/attest-build-provenance signs provenance for the six zips and marketplace.json with Sigstore's public-good instance and records it in the public Rekor transparency log and in the repository's attestations. That cannot be undone. So R3 publishes the release's digests, commit and workflow before R4 has compared any bytes, and deleting the draft does not withdraw them.
  - Evidence: `gh repo view AidanHT/qompack` returns `PUBLIC develop`. release.yml:55-59 attests `dist/bundle/*.zip` and `dist/bundle/marketplace.json` before goreleaser creates the draft. Runbook R3 rollback: `gh release delete v0.3.0 --yes; git push origin :refs/tags/v0.3.0; git tag -d v0.3.0. The draft was never public.`
  - Fix: List the public, irrevocable provenance record under R3's Outward. State that the rollback cannot remove it. Combine this with F4: before R3, run a hosted dry run at `--version 0.3.0` and compare its bin/ with B7. Cross-host reproducibility is then proven before anything is attested publicly.
- **minor** `docs/uat.md:81, docs/uat.md:141-142, docs/uat.md:146-149 (not in the implementer's stale-docs handoff list)` — The handoff to the docs seat lists README.md:22-30, docs/release.md:7, docs/install.md section 9 and releasechecksteps.go:170. It leaves out docs/uat.md. After 4cfc19d, uat.md's present-tense statements are false. A source build now prints 0.3.0, and the source tree's plugin.json now reads 0.3.0.
  - Evidence: docs/uat.md:81: 'on this tree, built from source, it printed `0.1.0`'. docs/uat.md:141: '`0.1.0` on this tree'. docs/uat.md:147: '`plugin/.claude-plugin/plugin.json` reads `0.1.0` until the release's version commit'. Line 212 is a dated observation of c6 and can stay as history.
  - Fix: Add docs/uat.md:81, 141-142 and 146-149 to the docs-seat list, so that lines 81 and 141-142 are reworded to say the build prints its stamped version (0.3.0 from this commit on), and the source-tree sentence at 146-149 is updated.
- **minor** `plans/sdd/V6-closeout/w17-release/runs/release-check-tag-v0.3.0-untagged-head.log:4` — The summary says release-check --tag ran at the untagged HEAD 4cfc19d. The committed log names c07cbe52, the commit before the amend, which is unreachable from the branch (reflog only). The trees are identical, because the amend only rewrote the message: the original wrongly said 'Bundled bytes are unchanged'. Still, the evidence quotes a SHA no reader can resolve, and nothing in the log says so.
  - Evidence: Log line: `fatal: no tag exactly matches 'c07cbe5280fca2e85349a9c1df00312094c5d4a7'`. Reflog: c07cbe52 at 00:20:01 is amended to 4cfc19db at 00:28:56. `git diff --stat c07cbe52 4cfc19d` is empty.
  - Fix: Re-run `go run ./tools/devtool release-check --tag v0.3.0` at 4cfc19d. It stops at step 1 within seconds. Alternatively, add a header line to the log: 'run at c07cbe52, amended to 4cfc19d with an identical tree (message-only)'.
- **nit** `Implementer summary, runbook R4/R6 pass criterion` — The criterion says that if the tag is C7, all 91 files are identical. R4 also downloads marketplace.json into dist/published, and a `devtool bundle` output (B7) has no marketplace.json. So `cmpbundles.py B7 dist/published` will always print one ONLY-IN-CANDIDATE line, with 92 files in total, and a literal reading of the criterion fails.
  - Evidence: R4: `gh release download v0.3.0 -D dist/published -p 'qompack-plugin-*.zip' -p checksums.txt -p marketplace.json`, then `python RUNS/cmpbundles.py B7 dist/published`. cmpbundles.py walk() counts every file in both trees.
  - Fix: Download marketplace.json into a separate directory, or state the expected result: '91 identical, plus marketplace.json only in published'. Also compare marketplace.json with a local `devtool marketplace --tag v0.3.0 --checksums B7/checksums.txt` output.
- **nit** `Implementer summary, runbook R0 verify` — 'B7's bin lines equal RUNS/sha256-head-4cfc19d.txt' is stated without condition. It holds only if c7 changes no Go build input beyond 4cfc19d. The summary states that condition elsewhere ('c6 plus only this commit'). R0 does not, and other wave-17 seats are landing in the same candidate.
  - Evidence: Summary: 'a candidate 7 that is c6 plus only this commit must reproduce exactly these bin hashes'. In R0 the condition is missing.
  - Fix: Add to R0: first run `git diff --stat 99d0b18 C7 -- cmd internal go.mod go.sum` and confirm it shows only internal/core/version.go. If not, expect new bin hashes and use bindiff.py to explain them.
- **nit** `commit bea787a1 (docs(v6): record the w17-release bundle byte comparison)` — There is no `Refs:` footer, while 4cfc19d carries `Refs: V6-VERIFY, C7.1`. Other docs(v6) commits on this repo often omit the footer, so this is hygiene only. Neither commit has an attribution trailer, which is correct.
  - Evidence: `git log --format=%B 9a56b305..HEAD` shows no Refs line for bea787a.
  - Fix: If the branch is re-cut, add `Refs: V6-VERIFY, C7.1` to the docs commit. Otherwise leave it.
- **nit** `plans/sdd/V6-closeout/w17-release/runs/focused-version-tests.log:1-3; implementer criterion_changes rationale` — The summary says each focused test ran alone by exact name. The log has a single non-verbose `ok` line each for core, pluginmanifest and devtool, yet three devtool tests and three pluginmanifest tests are claimed. So the evidence cannot show that each named test ran (only that the pattern matched at least one test, since no '[no tests to run]' marker appears). Separately, the criterion-change rationale says docs/release.md section 1 has the golden move with the constant. Section 1 names the constant and the regenerated plugin/ tree only. The golden follows because TestManifest_GoldenBytes pins Default(core.Version). The change is legitimate; only the citation is loose.
  - Evidence: t-devtool.log has the single line `ok github.com/qompack/qompack/tools/devtool 1.620s`. docs/release.md:15-20. My own rerun at -p 2 in the -race window: TestManifest_GoldenBytes, TestBundle_OnDiskMatchesGenerator and TestVersion_IsSet all PASS at HEAD.
  - Fix: Record focused runs with -v, or one invocation per name, so every claimed test name appears as `--- PASS`. Reword the rationale: 'the golden pins Default(core.Version), so it moves with the constant that docs/release.md section 1 bumps'.

## Fix seat (review resolution) — status `done`, head `7210e9a1`

### Root cause

Release bin/ bytes depend on core.Version's source default. devtool bundle stamps 0.3.0 through -X, but the Go linker keeps the original unreferenced "0.1.0" initializer literal, and release-check --tag (R7-3) requires that default to equal the tag. So the published binaries can only be built from a commit whose bytes differ from c6 by that one byte, plus the darwin/arm64 ad-hoc signature hash. With -buildvcs=false, bin/ does not depend on the commit; base 9a56b305 reproduces all six frozen bins. For review finding 1: release-check's isolated test/e2e pass and its cover pass judge TestV3_HotPathUnchangedWithLedgerResident's fsync-bound B-A/B-B rows on the local host with no waiver (the non-reference-disk declaration is honoured only under GITHUB_ACTIONS). On c6 that row failed on Windows, so R2 is blocked pending D53(d).

### Summary

PRODUCT BYTES CHANGE, SO THIS NEEDS A NEW CANDIDATE (unchanged from the first seat). Commit 4cfc19d sets core.Version, plugin/.claude-plugin/plugin.json and its golden to 0.3.0. As a result, every bundled bin/ differs from candidate 6 by one byte, and darwin/arm64 also differs in its 32-byte ad-hoc signature page hash. The cause is the unreferenced "0.1.0" initializer literal, which the linker keeps even though -X stamps 0.3.0. release-check --tag (R7-3) refuses to tag a commit whose core.Version default is 0.1.0, so the release must come from a candidate 7 that carries this commit. All of the first seat's byte evidence stands; see runs/cmp-*.txt, runs/sha256-*.txt and runs/bindiff-frozen-vs-head.txt.

This fix seat made one commit, 7210e9a1, which touches evidence only. No product, bundle, .github or docs file changed.

== Review resolution ==
1. MAJOR: R2 is presented as a formality, but c6's reference-host evidence shows it fails today. CONFIRMED, runbook corrected (R2 below).
   - Evidence checked:
     - chain.log has `run p3-win-e2e-timing exit=1` and `run p3-win-x11-alone exit=1`.
     - p3-win-e2e-timing.log:18-19: TestV3_HotPathUnchangedWithLedgerResident B-A p99 81.920 ms [FAIL] and B-B 57.344 ms [FAIL], limit 50 ms. 528 of 2064 samples were undelivered, so the gate fails on the certification ground too.
     - p3-win-x11-alone.log:18-19: B-A 98.304 ms and B-B 81.920 ms, with the spawn floor at p50 29.96 ms. D53(d) records 14.3 ms for a quiet host.
     - tools/devtool/test.go wholeTreePasses: the isolated test/e2e pass gets only {QOMPACK_UNDER_COLOAD: ""} and inherits the caller's disk declaration.
     - internal/obs/nonrefdisk.go NonReferenceDisk() honours the declaration only when GITHUB_ACTIONS=true.
     - release-check's `ci-local test` step judges this row, and `cover` re-judges it with instrumentation. It stops at the first FAIL, so determinism, the rollback rehearsal and the marketplace steps would never run.
     - In the checklist, C3.12 is `[ ]`, and D53(d) has no recorded c6 outcome.
   - R2 now has an explicit precondition: C3.12 open, plus a D53(d) disposition for c6/c7, or a quiet-window pass. Its expected outcome on current evidence is FAIL at ci-local test. It offers no local waiver.
2. MINOR: the attestation is public and cannot be withdrawn, so "the draft was never public" is wrong. CONFIRMED, runbook corrected (R3, plus a new pre-tag R2b).
   - `gh repo view` says the repository is PUBLIC.
   - release.yml:55-59 runs actions/attest-build-provenance@v2 over dist/bundle/*.zip and marketplace.json before goreleaser drafts the release. For a public repository it signs with Sigstore public-good and writes to the Rekor transparency log, which is append-only.
   - R3 now lists this under Outward and says the rollback cannot remove it.
   - R3 is gated on the new R2b, a hosted bundle dry run compared with B7 (F4), so hosted cross-host reproducibility is proven before anything is attested.
3. MINOR: docs/uat.md is missing from the stale-docs handoff. CONFIRMED, added to the docs-seat handoff list.
   - Lines 81 and 141-142 say a source build "printed `0.1.0`" / "`0.1.0` on this tree". Lines 146-149 say the source plugin.json "reads `0.1.0` until the release's version commit".
   - Line 212 is a dated c6 observation and stays as history.
   - I did not edit docs/: it is outside this seat's owned files, and the docs seat runs in parallel.
4. MINOR: the release-check log quotes the unreachable pre-amend SHA c07cbe52. CONFIRMED and FIXED in commit 7210e9a1.
   - I re-ran `go run ./tools/devtool release-check --tag v0.3.0` with HEAD detached at 4cfc19d, inside the machine limits' -race window (chain.log at "linux timing exit=" with no "linux race exit="), then returned to the branch.
   - The outcome is unchanged: version agreement passes the `0.3.0 == core.Version` comparison (releasechecksteps.go:182) and stops at "fatal: no tag exactly matches '4cfc19db…'".
   - The log now carries a header naming 4cfc19d and explaining the c07cbe52 history.
   - SHA scan over runs/: 4cfc19d and 9a56b305 are ancestors of HEAD. 99d0b18 is not an ancestor; it is candidate 6's freeze commit on verify/v6, cited on purpose, and it resolves.

== Stale text for the docs seat (updated list; none edited here) ==
- README.md:22-30
- docs/release.md:7 ("V6 release status: blocked")
- docs/install.md §9 ("nothing is published yet")
- docs/uat.md:81, 141-142 and 146-149 (reword them to say the build prints its stamped version, 0.3.0 from the version commit on; keep line 212 as history)
- tools/devtool/releasechecksteps.go:170, a comment reading "core.Version is `0.1.0`". It is outside this seat's scope unless there is a proven bug.

== (3) What release-check runs (unchanged) ==
It stops at the first FAIL and always writes dist/release-check.json. Steps:
1. Version agreement: SKIPPED without --tag. With --tag vX: the tag must start with v, X must equal core.Version's compiled default, and `git describe --tags --exact-match` must equal vX.
2-9. ci-local: fmt-check; lint, all sub-checks including stubskips; vet; build; test, in two whole-tree passes at -timeout=30m (the tree minus test/e2e with QOMPACK_UNDER_COLOAD=1, then test/e2e alone with co-load taken back); cover, the same two passes plus the coverage floors; plugin-validate; gen-config-docs --check.
10. build-all.
11. gen-mcp-docs and gen-command-docs, with --check.
12. `go test -count=1 ./test/guards/`.
13. govulncheck ./... (SKIPPED with --skip-vulncheck).
14. licenses --check.
15. Real-binary determinism.
16. Rollback rehearsal: three internal/store tests and three test/e2e tests, including the real `claude plugin` CLI in a temporary CLAUDE_CONFIG_DIR.
17. plugin-validate.
18. Marketplace.

Only steps 1, 15 and 18 change with --tag. I did not run it without --tag, because steps 6, 7 and 12 are whole-tree tests. Expected wall time: about 60-75 min on hosted ubuntu, about 2.5-3 h on this laptop, if it passes.

== (1) C7.1 byte evidence (carried forward from seat 1, unchanged) ==
- Base 9a56b305 against frozen c6: 78 of 91 files identical, including all six bin/. Only the 6 BUNDLE.json (source.commit), the 6 zips and checksums.txt differ.
- Version commit 4cfc19d against c6: 66 identical, 25 differ:
  - 6 bins, one byte each, plus the darwin-arm64 signature hash
  - 6 BUNDLE.json
  - 6 per-bundle checksums.txt
  - 6 zips
  - checksums.txt
- Chain bundleA (99d0b18, without --host-validate) equals c6 in all 91 files.
- 0.3.0 bin sha256 prefixes:

| Target | sha256 prefix |
|---|---|
| darwin-amd64 | 165be7b81de89a42 |
| darwin-arm64 | e55d66ba34d9000d |
| linux-amd64 | f72206c3403fd06a |
| linux-arm64 | 553e005493b2466b |
| windows-amd64 | ab8046bac3bad894 |
| windows-arm64 | c276cc9ec7e9bf6c |

- bin/ does not depend on the commit (-buildvcs=false), so a c7 made of c6 plus this commit must reproduce exactly these.
- Full-equality design: freeze c7 at the version commit, fast-forward develop and main to the freeze commit, and tag that exact commit.

== (2) THE 0.3.0 RELEASE RUNBOOK, REVISED (D53(h) order; every outward step needs the coordinator's D33/D4 decision) ==
Placeholders:
- C7 = candidate 7's freeze commit.
- B7 = C:/Users/Quant/Documents/Programming/Projects/qompack-bundles/c7.
- RUNS = plans/sdd/V6-closeout/w17-release/runs.
- Repository: AidanHT/qompack (PUBLIC).
- Origin today: develop=9c84e31, no main, no tags. Local-only tags: v0.0.1, v0.1.0, v0.2.0, wave1/*.

R0. Freeze candidate 7 (local).
- Command: merge closeout/w17-release into closeout/integration, together with every doc that must ship in the tagged tree (README status, CHANGELOG [0.3.0], docs/release.md status, install.md §9, docs/uat.md). Freeze with --no-ff into verify/v6, which gives C7. From a clean checkout of C7, run `go run ./tools/devtool bundle -archive -version 0.3.0 -host-validate -out B7`.
- Verify:
  - `python RUNS/cmpbundles.py qompack-bundles/c6 B7` shows exactly the 25 differences above.
  - `python RUNS/bindiff.py` shows one byte per target, plus the darwin-arm64 signature hash.
  - B7's bin lines equal RUNS/sha256-head-4cfc19d.txt.
  - A second build matches 91 of 91.
- Carry c6's evidence forward with a note naming the three changed files.
- Outward: none. Rollback: discard C7.

R0b. Hosted CI on C7 (C7.2, D53(h)(1)).
- Command: `git -C ../qompack-v6 push origin verify/v6`, then `gh run list --branch verify/v6 --limit 2`.
- Outward: a branch push.
- Verify: release-dry-run is green once the ci seat's F1 fix lands, and every other red is classified.
- Rollback: none needed.

R1. Merge path (D4, C7.3), fast-forward only.
- Commands:
  - `git merge-base --is-ancestor origin/develop C7`
  - `git -C ../qompack-develop status --porcelain` must print nothing. develop is checked out in that sibling worktree, so updating it from the main repo fails.
  - `git -C ../qompack-develop merge --ff-only C7`
  - `git -C ../qompack-develop push origin develop`
  - `git push origin C7:refs/heads/main`
- Outward: develop is the default branch, so the public README moves, and main appears.
- Verify: `git ls-remote origin refs/heads/develop refs/heads/main` shows C7 for both.
- Rollback:
  - `git push origin :refs/heads/main`
  - develop, only before anything builds on it: `git push --force-with-lease=refs/heads/develop:C7 origin 9c84e31d596ff2357cceeed42af240c8b6642dd5:refs/heads/develop`
- Branch protection only after F10 is decided.

R2. release-check --tag on the reference host.
- PRECONDITIONS. On current evidence this step FAILS. Do not start it until:
  - (a) C3.12 (release-check, no tag, green) is checked; and
  - (b) TestV3_HotPathUnchangedWithLedgerResident has a recorded D53(d) disposition for c6/c7, or passes in a quiet window on this host.
- Why it fails today: candidate 6's p3-win-e2e-timing ran exactly release-check's isolated e2e pass (`go test -count=1 -timeout=30m ./test/e2e`, co-load taken back) and FAILED on that one row, B-A p99 81.920 ms and B-B 57.344 ms against 50 ms. Run alone (p3-win-x11-alone) it failed again, at 98.304 and 81.920 ms, with the spawn floor at about 2x the quiet 14.3 ms. c7's binaries differ by one unreferenced byte, so c7 inherits this.
- release-check would stop at `ci-local test`. Even if the test pass got lucky, `cover` re-judges the row with coverage on.
- The only evidence that could pass is D53(b)'s quiet c5 C5.1 run (B-A 30.7 ms, B-B 22.5 ms against 50). c6's quiet.sh C5.1 is still pending in the chain.
- No local waiver exists or should be sought. QOMPACK_NONREFERENCE_DISK is ignored outside GitHub Actions by design (nonrefdisk.go:60-63), and QOMPACK_UNDER_COLOAD is taken back for the e2e pass by design.
- Commands, run in a quiet window when the owner says go:
  - `git worktree add --detach ../qompack-rel-v030 C7`
  - `git tag -a v0.3.0 -m "Qompack 0.3.0" C7` (local only)
  - `cd ../qompack-rel-v030 && go run ./tools/devtool release-check --tag v0.3.0`
- Expected: exit 0 with every step PASS. Copy dist/release-check.json into the evidence. If it stops at ci-local test or cover on the X11 row, R3 is blocked and the row goes back to D53(d).
- Wall time: about 2.5-3 h if it passes.
- Outward: none. Rollback: `git tag -d v0.3.0`.

R2b. Pre-tag hosted bundle dry run (F4; required before R3).
- This needs the ci seat to add a release-dry-run step: `go run ./tools/devtool bundle --version 0.3.0 --archive`, with dist/bundle uploaded as a workflow artifact. Do not attest it.
- Commands:
  - `gh run download <run id> -n <artifact> -D dist/dryrun`
  - `python RUNS/cmpbundles.py B7 dist/dryrun`
- Pass: all six bin/ files are identical. BUNDLE.json, the zips and checksums.txt may differ only by source.commit if the run was on a commit other than C7.
- Any bin/ difference is a STOP: diagnose cross-host reproducibility before anything is tagged.
- Outward: a workflow artifact on a public repository, which is downloadable by people with access, not a release.

R3. Tag push (C7.4).
- Command: `git push origin refs/tags/v0.3.0`. NEVER use `--tags` or `--follow-tags`, which would fire release.yml for v0.0.1, v0.1.0 and v0.2.0.
- Outward:
  - A public tag.
  - release.yml runs release-check --tag, bundle, marketplace, attestation and goreleaser, ending in a DRAFT release that is not listed publicly.
  - IRREVOCABLE: actions/attest-build-provenance (release.yml:55-59) signs provenance for the six zips and marketplace.json through Sigstore public-good. That writes the digests, commit and workflow identity to the public Rekor transparency log and to the repository's attestations. This happens BEFORE R4 compares any bytes.
- Verify:
  - `gh run watch <id>`
  - `gh release view v0.3.0 --json isDraft,isPrerelease,assets` shows isDraft=true and 10 assets.
- Rollback: `gh release delete v0.3.0 --yes; git push origin :refs/tags/v0.3.0; git tag -d v0.3.0`. This removes the draft and the tag only. The Rekor entries and the published digests cannot be withdrawn, and `gh attestation delete` does not touch the transparency log. That is why R2b must pass first.
- Note: release.yml re-runs the whole-tree gate on hosted ubuntu, where F1 applies.

R4. bin/ byte check on the DRAFT.
- Commands:
  - `gh release download v0.3.0 -D dist/published -p 'qompack-plugin-*.zip' -p checksums.txt -p marketplace.json`
  - `(cd dist/published && sha256sum --strict -c checksums.txt)`
  - `diff <(sort dist/published/checksums.txt) <(sort B7/checksums.txt)`
  - extract each zip
  - `python RUNS/cmpbundles.py B7 dist/published`
  - `gh attestation verify dist/published/qompack-plugin-0.3.0-windows-amd64.zip --repo AidanHT/qompack`
- Pass if the tag is C7: all 91 files identical and an empty diff.
- Pass if the tag is a docs-only descendant: only the 6 BUNDLE.json (source.commit), the 6 zips and checksums.txt differ, and every bin/ is identical.
- Any bin/ difference: STOP and apply R3's rollback, remembering that the attestation stays.
- Then replace the stale notes (F3) with `gh release edit v0.3.0 --notes-file <v0.3.0-notes.md>`. The notes must state:
  - the D53(h) D32-excluded-path disclosure for the Windows timings
  - D53(i)'s host-seen hook p50/p95
  - the untested targets

R5. Publish as a PRE-RELEASE (D53(h)(2)).
- Command: `gh release edit v0.3.0 --draft=false --prerelease --latest=false`, as ONE call.
- Outward:
  - A public pre-release.
  - `published` and `prereleased` fire. `released` does not, so marketplace.yml does not run.
- Verify:
  - isDraft=false and isPrerelease=true
  - no new marketplace.yml run
  - `curl -sSLI https://github.com/AidanHT/qompack/releases/download/v0.3.0/marketplace.json` ends in 200
- Rollback: `gh release edit v0.3.0 --draft=true`, or delete it.

R6. Repeat R4 on the anonymously served bytes.
- Command: `curl -sSLO` each zip and checksums.txt, then run the same checks as R4.
- Outward: none.

R7. HTTPS install rehearsal (D53(h)(3), C7.5).
(a) Windows.
- Commands:
  - `export CLAUDE_CONFIG_DIR=<scratch>/cc-https`
  - `claude --version` (must be 2.1.224 or later)
  - `claude plugin marketplace add https://github.com/AidanHT/qompack/releases/download/v0.3.0/marketplace.json`
  - `claude plugin install qompack-windows-amd64@qompack -s user`
  - `claude plugin list --json`
  - sha256 the installed bin/qompack.exe
- Pass: the hash equals B7's windows-amd64 bin. Then run the live UAT-01/C4.1 session through the coordinator's harness.
- Rollback: uninstall the plugin, remove the marketplace and delete the config directory.
(b) Linux, no model: a disposable container, never the owner's supabase stack, and only on the owner's go.
- Commands:
  - `npm i -g @anthropic-ai/claude-code@2.1.280`
  - as a non-root user: `CLAUDE_CONFIG_DIR=/tmp/cc`, the same marketplace add, then `claude plugin install qompack-linux-amd64@qompack -s user`
  - `stat -c '%a'`, `sha256sum` and `version` on the installed binary
- Pass: the mode has u+x, the sha equals B7's linux-amd64 bin, and the binary prints 0.3.0.
- Mode 644 answers C7.5's open question negatively and blocks R8 for the linux and darwin entries.
- Rollback: discard the container.

R8. Promote (D53(h)(5)).
- Command: `gh release edit v0.3.0 --prerelease=false --latest`
- Outward: becomes Latest. `released` fires, and marketplace.yml opens a PR from marketplace/v0.3.0 onto develop. This needs "Allow GitHub Actions to create and approve pull requests" enabled.
- Verify: `gh run list --workflow marketplace.yml` and `gh pr list --head marketplace/v0.3.0`.
- Rollback: `gh release edit v0.3.0 --prerelease`, then `gh pr close <n> --delete-branch`.

R9. Marketplace step (C7.5).
- The PR diff must contain only .claude-plugin/marketplace.json, with pins equal to checksums.txt.
- GITHUB_TOKEN PRs start no CI: push to the branch or re-run CI, then `gh pr merge <n>`.
- Verify in a clean CLAUDE_CONFIG_DIR:
  - `claude plugin marketplace add https://github.com/AidanHT/qompack.git`
  - install qompack-windows-amd64
  - the installed bin's sha equals B7's
- Rollback: a revert PR.

R10. Defender (owner only, D32).
- `& "$env:ProgramFiles\Windows Defender\MpCmdRun.exe" -Scan -ScanType 3 -File <downloaded qompack.exe> -DisableRemediation`
- If flagged, the owner submits at https://www.microsoft.com/wdsi/filesubmission.
- Record the outcome in the notes and in troubleshooting.

== Workflow flags (the ci seat owns .github/ and .goreleaser.yaml; neither was edited) ==
- F1, BLOCKER for release.yml: release-check's test and cover judge the fsync-bound hot-path rows on hosted ubuntu with no QOMPACK_NONREFERENCE_DISK. c6's release-dry-run failed in cover: B-A and B-B p99 49.152 ms against 15.
  - Set QOMPACK_NONREFERENCE_DISK '1' on release.yml's job and on ci.yml's release-dry-run.
  - Add both to test/guards/nonrefdisk_test.go.
  - Correct release-dry-run's comment that it runs no wall-clock budget.
  - This does NOT affect R2: locally the declaration is ignored.
- F2: goreleaser `draft: true` without `prerelease` means the UI's default publish is a full release. Use R5's single call, or set release.prerelease: true.
- F3: the release notes come from SP-17 evidence (2026-09-16) and must be replaced.
- F4: no V6 hosted run has reached the bundle step. Add R2b's dry-run bundle step, unattested, so hosted reproducibility is proven before R3's irrevocable attestation.
- F5: the Go version and flags match the freeze: go1.26.6, GOTOOLCHAIN=local, CGO_ENABLED=0, `-trimpath -buildvcs=false -ldflags "-s -w -buildid= -X …Version=0.3.0"`. Only the host OS differs (ubuntu vs windows), which is unproven until R2b.
- F6: checksums are not skipped.
- F7: release.yml has no environment approval and no concurrency setting, so any v* tag push fires it.
- F8: goreleaser-action floats on '~> v2'.
- F9: tag-day govulncheck can turn red.
- F10: the marketplace PR gets no CI under GITHUB_TOKEN, against required checks.
- F11 (new): attest-build-provenance runs on every tag push, before any human byte check, and its record on a public repository is permanent. If the owner wants attestation only after review, it would need to move to a post-publish workflow. That would be a design change for the ci seat and the owner, not this seat.

Machine limits: the only new command was the release-check --tag re-run, which stops at step 1 within seconds. It ran in the -race window (after "linux timing exit=" at 04:17:32Z, before "linux race exit="), alone. No other build or test ran, and I touched no Docker, no other worktree and no real ~/.claude.

### Commits

- 4cfc19dbd8f535484ed9c8ae4208a4496292a8b0 chore(release): set the version to 0.3.0
- bea787a1bfb38bb6414a2a086c2f0d0c8699b8b1 docs(v6): record the w17-release bundle byte comparison
- 7210e9a1 docs(v6): re-run release-check --tag at the version commit

### Tests

- `go run ./tools/devtool release-check --tag v0.3.0 (HEAD detached at 4cfc19dbd8f535484ed9c8ae4208a4496292a8b0, untagged; fix seat re-run)` — exit 1 at version agreement: 'no tag exactly matches 4cfc19db…'. The 0.3.0 == core.Version comparison (releasechecksteps.go:182) passed. Logged in runs/release-check-tag-v0.3.0-untagged-head.log
- `SHA reachability scan over plans/sdd/V6-closeout/w17-release/runs/ (git merge-base --is-ancestor)` — 4cfc19d and 9a56b305 reachable; 99d0b18 (c6 freeze on verify/v6, cited intentionally) resolves; c07cbe52 now only named in the explanatory header
- `go run ./tools/devtool bundle -archive -version 0.3.0 (base 9a56b305; seat 1)` — 78/91 identical with c6; all six bin/ identical; 13 differ only via source.commit
- `go run ./tools/devtool bundle -archive -version 0.3.0 (4cfc19d; seat 1)` — 66/91 identical; each bin differs by 1 byte (unreferenced 0.1.0 literal), darwin-arm64 also its signature hash
- `go test -p 2 -count=1 -run '^TestManifest_GoldenBytes$' ./internal/pluginmanifest/ (seat 1)` — red before the JSON bump, PASS after
- `go test -p 2 -count=1 -run '^TestBundle_OnDiskMatchesGenerator$' ./internal/pluginmanifest/ (seat 1)` — red before the JSON bump, PASS after
- `go test -p 2 -count=1 -run '^TestVersion_IsSet$' ./internal/core/ (seat 1)` — ok
- `go test -p 2 -count=1 -run '^TestReleaseCheckVersion$' ./tools/devtool/ (seat 1)` — ok
- `go run ./tools/devtool fmt-check; plugin-validate; gen-config-docs/gen-mcp-docs/gen-command-docs --check; go vet internal/core internal/pluginmanifest (Windows and GOOS=linux) (seat 1)` — all exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (seat 1)` — exit 0, all eight PASS
- `go run ./tools/devtool release-check (no --tag)` — NOT RUN: whole-tree tests. On c6 evidence it would FAIL at ci-local test on TestV3_HotPathUnchangedWithLedgerResident (Windows B-A p99 81.920 ms, B-B 57.344 ms against 50)

### Criterion changes

- testdata/golden/plugin/.claude-plugin/plugin.json version 0.1.0 -> 0.3.0, a one-line change carried from seat 1. Rationale: D1 sets the release version, and docs/release.md section 1 moves core.Version, the plugin tree and its golden in one commit. The bumped source plugin.json equals the plugin.json inside every frozen c6 bundle byte for byte. This fix seat changed no criteria.

### Open issues

- Candidate 7 is required. C7.1 changes one unreferenced byte in every bundled binary, plus the darwin/arm64 signature hash. Freeze c7 as c6 plus 4cfc19d plus the docs that must ship, build qompack-bundles/c7, and verify with RUNS/cmpbundles.py and bindiff.py.
- R2 (release-check --tag) and C3.12 are BLOCKED on current evidence. TestV3_HotPathUnchangedWithLedgerResident failed on the Windows reference host in c6's p3-win-e2e-timing (B-A 81.920 ms, B-B 57.344 ms against 50) and again alone in p3-win-x11-alone (98.304 / 81.920 ms, spawn floor about 2x quiet). D53(d) has no recorded c6 disposition. c6's quiet C5.1 (quiet.sh) is still pending.
- F1 (ci seat, blocker for release.yml): add QOMPACK_NONREFERENCE_DISK to release.yml and to ci.yml's release-dry-run, with nonrefdisk guard entries.
- F4/R2b (ci seat): add an unattested `bundle --version 0.3.0 --archive` step to release-dry-run and compare its bin/ with B7 before any tag push. Hosted cross-host reproducibility is unproven, and R3's attestation is irrevocable.
- F2: a goreleaser draft publishes as a FULL release by default. Use R5's single gh call, or set release.prerelease: true.
- F3: the release notes are generated from SP-17-era evidence and must be replaced before publishing.
- C7.5 exec-bit question: it needs the published pre-release and a Linux non-root install (R7b).
- Docs seat: README.md:22-30, docs/release.md:7, docs/install.md section 9 and docs/uat.md:81, 141-142, 146-149 are stale after the version commit. The comment at tools/devtool/releasechecksteps.go:170 is stale too.

### Needs the owner

- Accept candidate 7 = candidate 6 + chore(release) 4cfc19d. It changes one unreferenced string byte per binary; behaviour and `qompack version` output are identical. Also decide whether c6's live-lane and C5.5 evidence carries forward by this byte proof (recommended, with R7 run on the c7/release bytes).
- Merge path: fast-forward develop and main to the c7 freeze commit and tag that exact commit (full byte equality), or tag a later docs-only commit (bin/ equality only).
- D53(d) disposition for TestV3_HotPathUnchangedWithLedgerResident on Windows c6/c7: re-run it alone in a quiet window, or find the cause. Either is required before R2/C3.12 can pass. No local waiver exists by design.
- Apply the Q1/D53(e) report-only ruling to release.yml and ci.yml's release-dry-run (QOMPACK_NONREFERENCE_DISK). The reference-host release-check still gates those rows.
- Accept that a v0.3.0 tag push publishes an irrevocable public Sigstore/Rekor provenance record before any human byte check. Either require R2b's hosted dry-run byte proof first (recommended), or move attestation to a post-publish workflow.
- Whether to set .goreleaser.yaml release.prerelease: true.
- Linux exec-bit outcome (R7b): if the 0755 mode is dropped on extraction, decide whether the linux and darwin entries ship in 0.3.0.
- Owner only: the D32 Defender scan and false-positive submission; the repository setting that lets Actions open PRs; branch protection versus the GITHUB_TOKEN marketplace PR.

## Independent verification of the fix seat: needs-fixes

- **minor** `Fix-seat summary, runbook R2 PRECONDITIONS (b) and the sentence 'The only evidence that could pass is D53(b)'s quiet c5 C5.1 run ... c6's quiet.sh C5.1 is still pending in the chain'` — The fix for finding 1 is mostly right. C3.12 is named as the open prerequisite, the expected outcome is FAIL at ci-local test, cover re-judging the row is noted, and no local waiver is offered. The remaining problem is the quiet-window route in precondition (b). It points the reader at C5.1, which is a different run from the X11 row. C5.1 is `bench-hotpath --iterations 5000 --warm-daemon`. It has no X11 resident corpus and no paired no-ledger/ledger run. quiet.sh contains no TestV3_HotPathUnchangedWithLedgerResident run at all. So neither c5's quiet C5.1 pass nor c6's pending quiet.sh C5.1 can satisfy (b)'s 'passes in a quiet window on this host'. No scheduled run judges X11 quietly. The text also treats D53(d) as having no c6 outcome. In fact phase3.sh's win-x11-alone is D53(d)'s re-run alone for c6, and it reproduced. What is missing is the disposition. A coordinator who waits on quiet.sh's C5.1 to unblock R2 will get no X11 evidence.
  - Evidence: V6-CLOSEOUT-CHECKLIST.md C5.1: 'Quiet hot-path run: `bench-hotpath --iterations 5000 --warm-daemon`'. coordinator/quiet.sh:405 runs only `devtool bench-hotpath --iterations "$ITER" --hook observe-tool --warm-daemon`. `grep HotPathUnchanged\|x11 coordinator/quiet.sh` finds nothing. coordinator/phase3.sh:9 reads 'win-x11-alone  X11 (TestV3_HotPathUnchangedWithLedgerResident) by itself, -v (D53(d): its spawn floor)'. p3-win-x11-alone.log:18-19 shows B-A 98.304 ms and B-B 81.920 ms [FAIL], i.e. it reproduced. X11 also failed in c5 win-e2e-timing, c6 prefreeze and c6 win-e2e-timing.
  - Fix: Reword R2: c5's quiet C5.1 pass is only indirect evidence. It uses the same harness rows and the same 50 ms limit, but not the X11 corpus or the ledger pairing. State that c6's quiet.sh C5.1 does not judge X11. If (b)'s quiet branch is to be used, a quiet, normal-priority, X11-alone run must be scheduled explicitly: `go test -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e` on an idle host, added to quiet.sh or run on its own. Record that D53(d)'s c6 re-run reproduced (with the floor at about 2x quiet), so the open item is its disposition, not a missing outcome.
- **nit** `Fix-seat summary, runbook R2b (and F4 handoff to the ci seat); .github/workflows/ci.yml:590-603` — R2b asks the ci seat to 'add a release-dry-run step: `go run ./tools/devtool bundle --version 0.3.0 --archive`, with dist/bundle uploaded as a workflow artifact'. That step already exists. ci.yml:593 runs `bundle --archive --version 0.0.0-dryrun`, and :595-603 uploads it as `release-dry-run-bundles` with `if: always()`. What is actually needed is (i) the version changed to 0.3.0, because a 0.0.0-dryrun stamp cannot be byte-compared with B7, and (ii) a hosted release-check at :590 that passes, which depends on F1. The bundle step has no `if: always()`, which is why no V6 run has reached it. Read literally, 'add' invites a duplicate bundle step writing into the same dist/bundle. Two smaller points in the same text. The runbook leaves `<artifact>` as a placeholder although the name is known. It also says the artifact is 'downloadable by people with access'. On this PUBLIC repository that means any signed-in GitHub user, and dry-run bundles are already published that way on every push.
  - Evidence: ci.yml:590 `- run: go run ./tools/devtool release-check --skip-vulncheck`; :593 `- run: go run ./tools/devtool bundle --archive --version 0.0.0-dryrun`; :595-598 `uses: actions/upload-artifact@v4 / if: always() / name: release-dry-run-bundles`.
  - Fix: Reword R2b/F4: 'change ci.yml:593's --version 0.0.0-dryrun to 0.3.0 for the C7 push (it runs only once release-check at :590 passes, i.e. after F1), then `gh run download <id> -n release-dry-run-bundles -D dist/dryrun`'. State the artifact's exposure accurately.
- **nit** `Fix-seat summary, runbook R3 Rollback: '`gh attestation delete` does not touch the transparency log'` — The new rollback text names a gh subcommand that does not exist. That implies the operator could remove the repository's attestation record with it during a rollback. The substance is correct: the Rekor entries and the published digests cannot be withdrawn. The command reference is wrong.
  - Evidence: `gh attestation --help` (gh 2.89.0) lists only download, trusted-root and verify. There is no delete.
  - Fix: Drop the command name. Say instead: the Rekor transparency-log entry is permanent; the repository-side attestation record can at most be removed through GitHub's attestation REST endpoints, if the owner chooses; neither removal unpublishes the digests.

