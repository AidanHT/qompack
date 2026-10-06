# Wave 19h: ASCII-only root folding, raw-segment rootCover, ADR accuracy

Branch `closeout/w19-rehydrate`. Workflow `wf_b50111eb-587`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## fix:rehydrate:h1: status `done`, head `eca33155135abe61c8c13d0270cb1fb655c2f441`

### Summary

All three findings are fixed and every check passes. Each fix was written test-first, and each new test fails on 71e5133d. Branch: closeout/w19-rehydrate. Worktree: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-rehydrate. It is clean, nothing was pushed, and commit subjects are 61 to 63 characters with no attribution trailers.

**(1) MAJOR: Unicode-only case variants of the root (c16b21d5)**
- Cause: the root regex used RE2's (?i) (pathgate.go rootSpellingOf), and containment (inside() and key()) went through filepath.Rel. On Windows both fold by Unicode, so k matched the Kelvin sign U+212A, s matched the long s U+017F, and å matched the Angstrom sign U+212B. NTFS keeps those as separate sibling folders; I re-checked this on disk.
- Root regex: rootSpellingOf now writes each ASCII letter as a two-case class (new foldLiteral), with no (?i). holdRoot, markRoot, recordedPath, exactRooted and reasonWithheld all read the root through it.
- Containment: inside() and key() now use a new exported rehydrate.RootRelative. It compares cleaned paths byte for byte with asciiFoldEqual, the same rule rootPrefix already used.
- Daemon adapter: internal/daemon/rehydrate_service.go now uses RootRelative to find the path below the root before judging the resolved root's spelling. Other paths fall back to filepath.Rel, so the adapter never judges fewer spellings than before.
- rootCover: deliberately keeps hostperm's Unicode lower-casing, because it models what the host refuses and folding more can only add screens.
- At 71e5133d on Windows, all 18 sibling summaries were shown (3 roots × uncut JSON value, one-word Read, two commands, cut past the root alone and as the second of two values); the file pointer and drop reason leaked too. At HEAD all 18 are withheld. The upper-ASCII variants of the real root are shown on Windows and macOS and withheld on Linux.

**(2) MINOR: ADR 0011 overstatements (eca33155)**
- Item 9 (formerly lines 1440-1443) and the wave-19f criterion paragraph (formerly line 1880) said a cut sibling differing by a non-ASCII case was withheld. That was only true when the cut fell inside the root's own spelling. Both now say that, and that since c16b21d5 such a sibling is withheld wherever the cut falls.
- Items 6, 7(d) and 8, the "What changed" narrative, the evidence list and a new criterion-change paragraph are added. docs/security.md has one new sentence.

**(3) MINOR: rootCover's screen form (28a9ea39)**
- rootCover now reads a rule the way hostperm's compileOne, finishPattern and prefixRow do:
  - raw segments split at `/` only, with `\` kept as path.Match's escape;
  - plus the `\`-as-separator alias, applied on every platform as before;
  - case lower-cased with paths.Key, as hostperm does;
  - root segments in hostperm's posixSegments form, nothing deleted.
- segMatch still also compares in screen form. That comparison can only match more, so it only adds screens, and dropping it would weaken an existing check.
- ruleSegments now shares hostReadingOf and no longer returns fromStart (golangci's unparam flagged it). The test helper screenLiteral changed only to accept two results.

**New tests**
- internal/rehydrate/summary_screen_d64_r4_test.go: TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt, TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideItForPointersAndReasons, TestBuild_ARuleOverTheRootIsMatchedAsTheHostMatchesIt.
- internal/daemon/rehydrate_hostpaths_r4_test.go: TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt (real NTFS sibling folders, checked with os.SameFile) and TestRehydrateHostPaths_ARuleOverTheRootIsMatchedAsTheHostMatchesIt (real hostperm; it checks first that the host refuses certs/server.key).

**Red-first on 71e5133d (overlaid, tree untouched)**
- Windows:
  - both case tests fail on all 3 roots (kate, sam, åsa);
  - the rule test fails 6 of 7 subtests;
  - the daemon twins fail on all 3 roots and all 3 rule shapes.
- Linux container: the case tests pass (paths don't fold there); the rule test fails 5 of 6 and its daemon twin fails 3 of 3.
- The Kelvin-sign rule subtest passes everywhere. It pins the host's Unicode case rule rather than catching a bug.

**Separability (each fix reverted alone, Windows)**
- Restoring (?i) fails only the summary test.
- Restoring filepath.Rel fails the summary and pointer/reason tests.
- Removing rootCover's raw comparison fails only the rule test, on 5 subtests. The escape shape is still caught by the raw split alone.

**Corpus (w19d, 274 previews): 71e5133d and HEAD are identical, with no flips and the same host-call counts**

| Scenario | Windows shown / withheld | Windows leaks / over-withheld | Linux shown / withheld | Linux leaks / over-withheld |
|---|---|---|---|---|
| none-plain | 209 / 65 | 0 / 52 | 208 / 66 | 0 / 53 |
| UAT-12 roots (plain, spaced, nonascii, long) | 193 / 81 | 0 / 53 | 192 / 82 | 0 / 54 |
| common rules (plain, spaced) | 193 / 81 | 0 / 54 | 192 / 82 | 0 / 55 |
| onedrive, dashword, bestfit | 149 / 125 | 0 / 97 | 149 / 125 | 0 / 97 |

**Criterion changes**
No existing assertion and no golden changed. The behaviour changes, all recorded in the ADR:
- The Unicode-case siblings are withheld everywhere.
- On macOS, containment now folds ASCII letters, matching the root regex and rootPrefix there. Those paths were over-withheld before. No macOS host was available to run this; go vet for darwin passes.
- A root spelled with a non-ASCII letter in a case NTFS does fold (U+00C5 for a root with å) is now over-withheld.
- rootCover can add screens.

The Linux runs used the owner's already-running Docker engine with --cpus=2 as uid 1000. No container was left running.

### Commits

- c16b21d5 fix(rehydrate): fold only ascii letters when comparing the root
- 28a9ea39 fix(rehydrate): match a rule to the root as the host matches it
- eca33155 docs(adr): record wave 19g's final verify of root comparisons

### Tests

- `GOOS={windows,linux,darwin} GOARCH=amd64 go vet ./...`: exit 0 on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns (1525 of 1525 checkable resolved), PASS docmarkers
- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/...`: ok (rehydrate and rehydratetest), Windows at eca33155; also PASS in Linux container (uid 1000)
- `go test -p 2 -count=1 -v -timeout=30m -run "^(<84 daemon rehydrate rows: the 82 from w19g's list plus the 2 new twins>)$" ./internal/daemon/`: Windows: 83 PASS + 1 SKIP (TestService_StateWriteFailureStillEmits, platform); Linux container: 84 PASS <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=20 -run '^(TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt|TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideItForPointersAndReasons|TestBuild_ARuleOverTheRootIsMatchedAsTheHostMatchesIt)$' ./internal/rehydrate/`: 60/60 PASS on Windows and in Linux container
- `go test -p 2 -count=20 -run '^(TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt|TestRehydrateHostPaths_ARuleOverTheRootIsMatchedAsTheHostMatchesIt)$' ./internal/daemon/`: 40/40 PASS on Windows and in Linux container
- `go test -p 2 -race -count=3 -run <same new-row patterns> ./internal/rehydrate/ and ./internal/daemon/`: 9/9 and 6/6 PASS, no DATA RACE, on Windows (CGO, gcc) and in Linux container <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -overlay <71e5133d pathgate.go (+rehydrate_service.go)> -run <new-row patterns> ./internal/rehydrate/ ./internal/daemon/`: red-first: Windows both case tests FAIL on 3/3 roots, rule test FAIL 6/7 (Kelvin pin passes), daemon twins FAIL 3/3 roots and 3/3 rule shapes; Linux case tests PASS (no fold), rule test FAIL 5/6, daemon rule twin FAIL 3/3 <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -overlay <single-fix mutants m1/m2/m3> -run <new rehydrate rows> ./internal/rehydrate/`: m1 ((?i) restored): summary test fails; m2 (filepath.Rel restored): summary and pointer/reason tests fail; m3 (raw comparison removed): rule test fails on 5 subtests <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `RV2_OUT=... go test -overlay corpus -run '^TestZZRev2Corpus$' ./internal/rehydrate/ (71e5133d and HEAD, Windows and Linux)`: identical counts, 0 leaks, no flips, same host-call counts <!-- runpatterns: a scratch probe the seat ran from an overlaid test file to measure the corpus or the adapter; it is not committed, and its numbers are recorded on this line -->

### Criterion changes

- No existing assertion changed in either direction (WITHHELD or SHOWN), and no golden changed. internal/rehydrate, rehydratetest and the 82 earlier daemon rehydrate rows pass unchanged on Windows and in a Linux container.
- Test helper screenLiteral (internal/rehydrate/summary_screen_test.go) now takes ruleSegments' two results, because fromStart moved to hostReadings. Its assertions are unchanged.
- Behaviour change: paths in a sibling folder spelled with the Kelvin sign, the long s or the Angstrom sign are outside the project in every judgement on Windows, and withheld in free text on macOS too.
- Behaviour change: on macOS, an absolute path spelling the root in another ASCII case is now inside the project for containment. It was over-withheld before.
- Behaviour change: a root spelled with a non-ASCII letter in a case NTFS does fold is no longer held as the root, and is over-withheld.
- Behaviour change: rootCover can add more screens, because it now also matches hostperm's raw segments; the screen-form comparison is kept.

### Open issues

- Pre-existing gap I found but did not fix (outside scope): a rule anchored outside the project that reaches the root only through an alias (an 8.3 name, a link or a junction) and refuses only part of the project (e.g. //c/users/johnsm~1/proj/**/*.key) is caught by neither rootCover nor rootProbe. rootProbe only detects a rule over the whole project, and the rule's own literal would be `proj`, not `.key`. So `cat certs/server.key` would be shown while the host refuses the file.
- On macOS, containment and the daemon adapter now fold ASCII letters (previously no fold). This makes them consistent with the root regex and rootPrefix there, but no macOS host was available to run it; only go vet for darwin was checked.
- Accepted over-withholding: a root spelled with a non-ASCII letter in a case that NTFS does fold (U+00C5 for a root with U+00E5, or é/É) is no longer held as the root. Summaries that spell the root that way are now withheld.
- internal/rehydrate/items.go checkpointPointer still uses filepath.Rel. I left it because it only shortens Qompack's own checkpoint artifact path (always built from the root itself) for display and judges no recorded path.
- The commit messages and the ADR quote c16b21d5 and 28a9ea39. These SHAs stay valid only if the branch is merged, not cherry-picked or rebased.

## verify:rehydrate:h: verdict `needs-fixes`, 1 finding(s)

- **major** `internal/rehydrate/pathgate.go: key() :2166-2179 (RootRelative), called by note() :488-497; recordedPath() :457-468 (holdRoot through the ASCII-only rootSpellingOf)`: c16b21d5 narrows learning, not just showing, and this new leak is not recorded. The build learns a withheld recorded path's project-relative names so that it can later withhold globs and text that name or select that path. That learning now uses the strict ASCII-only fold. Take a recorded path that spells the root with a non-ASCII letter in a case NTFS does fold, e.g. C:\q\Åsa\proj\private\deny.txt (U+00C5) under the root C:\q\åsa\proj. The host refuses that path as the project's own private/deny.txt: NTFS folds U+00C5 to U+00E5, and hostperm's strings.ToLower folds it too. The build now calls the path 'outside', so it never learns the relative key: nothing goes into j.known, and knownText and knownPaths get no relative entry. For a tool summary under a root with a space, recordedPath no longer holds the root, so the root's space rejects the value and nothing is learned at all. Result: a glob preview that selects the refused file flips from WITHHELD at 71e5133d to SHOWN at HEAD. That breaks D63's rule that a glob selecting a path the build withholds is withheld. It also contradicts the code's own D64 ruling that recordedPath holds every root because learning only withholds more. The ADR's criterion paragraph and the seat's report call this spelling only 'over-withheld'.
  - Evidence: Overlay probes; the tree was not touched. Setup: Windows, root C:\q\åsa\proj, host rules ./private/deny.txt (hostRules helper, which folds as hostperm does), one file pointer C:\q\Åsa\proj\private\deny.txt. At 71e5133d the summaries `private/d*` and `private/de?y.txt` are WITHHELD; at eca33155 both are SHOWN. A pointer C:\q\Åsa\proj\private\id flips `private/i?` the same way. Tool-summary shape: root C:\q\åsa berg\proj, recorded summary {"notebook_path":"C:\\q\\Åsa berg\\proj\\private\\deny.txt"}. At 71e5133d `private/d*` and `private/de?y.txt` are WITHHELD; at HEAD both are SHOWN. With no recorded pointer, both are SHOWN on HEAD, so the whole difference is the lost learning. Everything else checked holds. (1)-(3) are fixed. Red-first was re-verified on Windows by overlaying 71e5133d: both case rows fail 3/3, the rule row fails 6/7, and the daemon twins fail 3/3 and 3/3. All 18 sibling summaries are individually SHOWN at 71e5133d and WITHHELD at HEAD. All checks pass at HEAD on Windows and in a Linux container (uid 1000): go vet for windows, linux and darwin; golangci-lint; fmt-check; test/docs; docmarkers and runpatterns; internal/rehydrate in full; 84 daemon rehydrate rows (83 PASS, 1 platform SKIP); new rows at -count=20 and -race -count=3. The commits and the rest of the ADR are accurate.
  - Fix: Keep the ASCII-only fold for every judgement that decides what is shown. Learning, which only ever withholds more, should also read a recorded path under the broader fold. In note(), when RootRelative fails, also derive the relative key under Unicode folding: filepath.Rel's reading on Windows, or a paths.Key-lowered prefix compare where paths fold. Learn that key into known, knownText and knownPaths. In recordedPath, also hold the root with a Unicode-folding spelling (the old (?i) regex), so a variant root's space does not reject the value. Add red-first rows that fail at eca33155 and pass at 71e5133d, for the file-pointer shape and the tool-summary-under-a-spaced-root shape, both checking that `private/d*` and `private/de?y.txt` are withheld. Then correct ADR 0011's wave-19g criterion paragraph and item 8/9 text, which say the U+00C5 spelling is only over-withheld.

