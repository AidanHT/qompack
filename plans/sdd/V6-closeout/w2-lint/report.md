# Wave 2b workstream report: w2-lint (C1.19)

Branch `closeout/w2-lint`. Workflow `wf_b2b236ea-ef1`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `partial`, head `185228a`

### Root cause

(1) bindeps: 3ab1523 ("feat(store): add certified point-in-time maintenance recovery", 2026-09-21) added internal/paths/rename_noreplace_linux.go and rename_noreplace_darwin.go. They import golang.org/x/sys/unix for renameat2(RENAME_NOREPLACE) and renamex_np(RENAME_EXCL), which RenameDirectoryNoReplace needs to publish a restored .qompack. The commit did not amend 00-ARCHITECTURE.md §2.5 or allowedBinDep. I found it with git log -S. The other hit, 874b343, only mentions x/sys/unix in a comment: syncdata_linux.go already uses syscall.Fdatasync. Nothing in the standard library can replace the import. On go1.26.6, syscall has no renameat2 wrapper and no RENAME_NOREPLACE constant, and linux/amd64 has no SYS_RENAMEAT2 number (linux/arm64 has one, 276). Go's internal renameat2 is limited to loong64/riscv64 and cannot be imported. darwin syscall has neither a renamex_np wrapper nor a SYS_RENAMEATX_NP number, and syscall.Syscall there is a raw SYSCALL trap. (2) stubskips: the four tests called t.Skipf with reasons outside the three permitted forms (none had the "platform: " prefix). Three of them built their directory alias with os.Symlink only, although an unprivileged Windows process can create an NTFS junction, which is an equivalent directory alias.

### Summary

C1.19's two named failures are fixed. bindeps and stubskips now pass without weakening either check. `devtool lint` is still not fully green, because runpatterns fails on 26 rows in the wave-1 reports the coordinator committed on the base. It fails identically on b070bbe, and those files are outside my scope.

**Earlier attempt, found in the worktree.** The brief says nothing was committed, but the branch already held commit 1d6db42 (the bindeps fix), plus uncommitted edits to the four tests, three untracked makeDirLink helpers, and run logs.
- 1d6db42: kept after re-checking it myself. I reproduced the red state (TestAllowedBinDep red, bindeps with 4 violations), re-ran bindeps, licenses --check and govulncheck, and checked every standard-library claim against the go1.26.6 source. Revised in 75f7f20: it overstated that renamex_np is reachable "only through x/sys/unix's trampolines", and it missed two alternatives.
- Test edits: kept. One revision: the helper comment claimed a test helper cannot be shared across packages. The real reason is that internal/testutil depends on daemon, hookio and store, so their in-package tests cannot import it.
- Logs: kept after reproducing them.

**(1) bindeps.** x/sys/unix goes on the allow-list as one exact import path, and the reasons are recorded in 00-ARCHITECTURE.md §2.5 and in the allowedBinDep comment. Alternatives rejected:
- Rewriting only the linux file on syscall: darwin needs the entry anyway, so this would only add a hand-kept amd64 syscall number.
- Copying the darwin cgo_import_dynamic trampoline into the repo: that is x/sys code under the same licence, without its maintenance.
- A mkdir claim followed by a plain rename: it replaces an empty directory a racer puts back, and a crash between the two calls leaves an empty destination that maintRefuseExistingDot then refuses.
- A restore that fails closed on linux and darwin.

Nothing new enters the build: x/sys v0.33.0 is already required and ships in the Windows binary, and THIRD_PARTY_NOTICES already covers it (licenses --check passes). govulncheck reports no reachable vulnerability on linux or darwin. x/sys/unix has no init() on the four unix release targets, and go list -deps shows only golang.org/x/sys/unix itself enters, with no subpackage.

**(2) stubskips.**
- daemon RefusesAliasedSegmentParent, daemon DrainAfterPhysicalScopeChanges and hookio JunctionSwapDefeatsLexicalContainment now use makeDirLink (a symlink, or an NTFS junction where symlinks are not allowed), which paths, mcp and hostperm already use. All three now run and pass on this unprivileged Windows host. They only skip, with a "platform: " reason, where the host allows neither link.
- The junction tests still catch the defect they guard: with ResolvesInside mutated to lexical-only, hookio and DrainAfterPhysicalScopeChanges both fail.
- store SymlinkComponentInBackupTree needs a file symlink, which has no junction equivalent. It keeps its skip, now with the permitted "platform: " reason.
- New store test DirectoryLinkComponentInBackupTreeRefused covers a linked backup directory on every host. It fails when maintNoFollow stops checking os.ModeIrregular.
- No assertion was changed or removed.

**(3) Lint results.** The one full `devtool lint` ran on Windows at 8116d69; on Windows this is the same command as CI's lint-windows job.

| Sub-check | Result |
|---|---|
| golangci-lint | PASS |
| nomagic | PASS |
| importgraph | PASS |
| testdeps | PASS |
| bindeps | PASS |
| sleepcheck | PASS |
| stubskips | the four skip problems are gone; the only remaining problem was test/e2e killed at the 30m wall under co-load. Re-run alone with --only=stubskips: PASS, 24 permitted platform notices |
| runpatterns | FAIL, 26 pre-existing rows |
| docmarkers | PASS |
| coveragefloors | PASS |

fmt-check PASS; go vet PASS on the touched packages for windows, linux and darwin.

### Commits

- 1d6db42 fix(devtool): admit golang.org/x/sys/unix to bindeps with a record (committed by the interrupted earlier attempt; independently re-verified and adopted)
- 75f7f20 docs(devtool): record the rejected x/sys/unix alternatives
- 36883f6 test(daemon): run the two directory-alias tests on a junction
- dbc6b17 test(hookio): run the junction-swap test on a real junction
- 8116d69 test(store): cover a linked backup directory on every host
- 185228a chore(v6): add the w2-lint evidence logs

### Tests

- `go test ./tools/devtool -run TestAllowedBinDep (with bindeps.go reverted to b070bbe)` — FAIL as expected: allowedBinDep("golang.org/x/sys/unix") = false, want true (runs/bindeps-red-reverify-windows.log)
- `go run ./tools/devtool lint --only=bindeps (bindeps.go at b070bbe / at HEAD)` — 4 violations (linux and darwin, amd64 and arm64) / PASS, 6 targets
- `go test ./tools/devtool -run 'TestAllowedBinDep|TestIsStdlib|TestShippedModulesEqualTheAllowlistIntersection' -count=1 -v` — PASS (shipped modules: go-winio, klauspost/compress, x/sys)
- `go run ./tools/devtool licenses --check` — PASS: THIRD_PARTY_NOTICES.md is current
- `CGO_ENABLED=0 GOOS={linux,darwin} GOARCH=amd64 govulncheck -show verbose ./cmd/qompack (pinned build)` — 0 symbol-level vulnerabilities; module-level only: GO-2026-5841 (klauspost s2, not imported), GO-2026-5024 (x/sys/windows, windows only)
- `go test -json -count=1 -run '<the 4 tests>' ./internal/daemon ./internal/hookio ./internal/store, test edits stashed, fed to a scratch replica of classifySkips` — 4 skip problems (baseline reproduced) <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `same, with the edits plus TestMaintenance_DirectoryLinkComponentInBackupTreeRefused` — 4 PASS, 1 permitted platform skip, 0 problems
- `mutation: paths.ResolvesInside returns lexical containment only; run the hookio JunctionSwap and daemon DrainAfterPhysicalScopeChanges tests` — both FAIL on the junction, so the tests still discriminate (reverted)
- `mutation: maintNoFollow drops os.ModeIrregular; go test ./internal/store -run TestMaintenance_DirectoryLinkComponentInBackupTreeRefused` — FAIL as expected (reverted)
- `go test -count=1 -timeout=30m ./internal/hookio | ./internal/store | ./tools/devtool | ./internal/daemon (Windows, co-loaded)` — all ok (store 424s, devtool 43s, daemon 433s); both daemon tests PASS on a junction
- `linux-nonroot-gate.sh --prefix cx-w2-lint 8116d69 pkgs-full -- ./internal/hookio ./internal/store ./internal/paths (uid 10001, -race)` — hookio PASS 113, paths PASS 134 / 18 skipped, store 829 pass / 1 FAIL TestGC_DeadlineOvershootIsBoundedByTheCheckInterval; all 22 skips permitted; 0 race logs
- `linux-nonroot-gate.sh 8116d69 daemon-focused --run '^(TestDeliveryPath_V6_RefusesAliasedSegmentParent|TestDeliveryTerminal_DrainAfterPhysicalScopeChanges)$' -- ./internal/daemon` — PASS 2
- `linux-nonroot-gate.sh {8116d69, b070bbe} --run '^TestGC_DeadlineOvershootIsBoundedByTheCheckInterval$' -- ./internal/store (alone)` — FAIL on both, same message ('no attempt placed the deadline inside the sweep'): not caused by this branch
- `golangci-lint (pinned) with GOOS=linux and GOOS=darwin over paths, daemon, hookio, store, tools/devtool` — PASS both
- `go run ./tools/devtool lint (full, once, Windows, 8116d69)` — exit 1: 8 of 10 sub-checks PASS; stubskips failed only on the test/e2e 30m timeout; runpatterns FAIL on 26 rows
- `go run ./tools/devtool lint --only=stubskips (re-run alone)` — PASS, stubskips: OK, 24 permitted platform notices
- `go run ./tools/devtool lint --only=runpatterns in a git-archive snapshot of b070bbe` — FAIL, the same 26 rows as at HEAD (diff empty): pre-existing
- `go run ./tools/devtool fmt-check; go vet on touched packages (windows, linux, darwin)` — PASS

### Criterion changes

- bindeps allow-list: golang.org/x/sys/unix is admitted as one exact import path, and TestAllowedBinDep's {"golang.org/x/sys/unix", false} becomes true. Rationale: RenameDirectoryNoReplace needs renameat2(RENAME_NOREPLACE) and renamex_np(RENAME_EXCL); the standard library has no equivalent on linux/amd64 or darwin; no new module, version or licence enters; govulncheck finds no reachable vulnerability. The test gains negative cases so that x/sys/unix/linux, x/sys/cpu, x/sys/plan9 and the bare module path stay disallowed.
- Skip reasons: four tests now use the permitted 'platform: ' prefix. This widens nothing. Three of them now RUN on unprivileged Windows through an NTFS junction, where before they always skipped. The fourth (a file-symlink leaf, which has no junction equivalent) keeps its skip, and the new TestMaintenance_DirectoryLinkComponentInBackupTreeRefused covers the directory-link case on every host. No assertion was changed or removed, and no threshold, timeout, golden or //nolint was touched.

### Open issues

- runpatterns fails on 26 -run quotes in the wave-1 reports committed on base b070bbe, identical at HEAD. By report: config 203, 204; e2e 94, 203-207, 312; hostperm 122, 124, 277; ingest 104, 106, 356, 360, 362, 366; linux 93, 94, 106, 107, 417; packaging 123, 317; rehydrate-cap 94. Three kinds: (a) a '^(A|B)$' alternation in a bullet (not a table row), which the checker splits at the pipe; (b) placeholders such as '<the six TestFault_* cases>', '<322 remainder>' or '...same three...'; (c) prose punctuation or temporary tests: 'TestFsck_ToolUse:', 'TestHostPolicy_ALinkSpellingIsJudgedToo,', 'HostDenyRed', and TestReviewRepro_* from a scratch git archive. The existing fix is a `<!-- runpatterns: reason -->` waiver per row, or a corrected quote. I did not edit the reports: they are outside my scope and the coordinator owns them.
- TestGC_DeadlineOvershootIsBoundedByTheCheckInterval fails alone in the Linux container (non-root, -race) on both 8116d69 and b070bbe, with the test's own 'host could not be measured' message. Earlier wave-1 reports saw it pass alone, so it depends on the host's state. It is not related to this branch; route it to the store/perf owner.
- The Linux side of C3.5 (ubuntu `devtool lint`, the CI verify job) was not run as a full lint. What covers it: bindeps checks all six targets from any host, golangci-lint passed with GOOS=linux and darwin on the touched packages, and every skip in the Linux gate runs had a permitted reason.
- Informational, out of scope: govulncheck lists GO-2026-5024 in golang.org/x/sys/windows v0.33.0 (fixed in v0.44.0). The Windows binary imports that package but, per govulncheck, does not call the affected symbol. GO-2026-5841 is in klauspost/compress/s2, which is not imported.
- Informational: TestDeliveryPath_V6_RefusesAliasedSegmentParent does not isolate pinDeliveryChild's type or SameFile checks; os.Root refuses the alias too (runs/mutation-daemon-pindeliverychild-windows.log). That was already true of the symlink fixture and is unchanged. The test asserts the combined behaviour.
- Both full-tree stubskips passes ran test/e2e, which on this host invokes the real claude CLI for `claude plugin validate/marketplace/install/uninstall` in an isolated HOME. These are CLI plugin commands, not model sessions. Its install cases no longer skip, because claude is on PATH.

### Needs the owner

- Ratify the 00-ARCHITECTURE.md §2.5 amendment (1d6db42, refined in 75f7f20) that adds golang.org/x/sys/unix, exact path only, to the closed runtime dependency list. §2.5 says any addition requires an amendment; the rationale and every rejected alternative are recorded there and in the allowedBinDep comment.
- Coordinator: decide how the 26 runpatterns rows in the wave-1 reports are fixed (per-row waiver or corrected quote). Until then `devtool lint`, and so C3.5, stays red.

## Independent review

### review:lint: sound

- **nit** `plans/V1-VERIFY-foundation-and-contracts.md:122 (B11), also plans/V3-VERIFY-observer-and-negative-knowledge.md:255 (A3) and plans/V1-SP-01-foundation-toolchain-and-contracts.md:2250` — The §2.5 amendment admits golang.org/x/sys/unix. These verification rows still describe the allowed closure as stdlib + qompack + zstd + go-winio + x/sys/windows. B11's manual spot-check pipeline has an exclusion regex without x/sys/unix, so re-running it on a linux or darwin host now prints `golang.org/x/sys/unix`, which the row itself says to read as a FAIL, while `lint --only=bindeps` passes. The same false alarm happened once before for x/sys/windows, and the row's Post-V2 correction records it.
  - Evidence: git grep shows only 00-ARCHITECTURE.md updated for x/sys/unix. B11's spot-check is `grep -Ev '^(internal/|github.com/qompack/|github.com/klauspost/compress|github.com/Microsoft/go-winio|golang.org/x/sys/windows)'` and its expected output is 'prints nothing'.
  - Fix: Add a short post-V6 correction note to B11, adding `\|golang.org/x/sys/unix` to its exclusion regex and naming C1.19 / the §2.5 amendment. Optionally add a one-line pointer in V3-VERIFY A3. These are historical plan documents, so annotate them; don't rewrite them.
- **nit** `internal/daemon/delivery_path_v6_test.go:78-80` — The new comment says the test works because 'pinDeliveryChild refuses both (os.ModeSymlink, os.ModeIrregular)'. The implementer's own mutation log shows os.Root also refuses the alias, so the test does not isolate pinDeliveryChild's type check. The comment implies more precision than the test has.
  - Evidence: Implementer open_issues: 'TestDeliveryPath_V6_RefusesAliasedSegmentParent does not isolate pinDeliveryChild's type or SameFile checks; os.Root refuses the alias too (runs/mutation-daemon-pindeliverychild-windows.log).'
  - Fix: Change the comment to say the alias is refused by the combined os.Root / pinDeliveryChild path, e.g. '...and the segment-parent open refuses both'. Or leave it, and record the non-isolation as the known limit it already is.

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.

