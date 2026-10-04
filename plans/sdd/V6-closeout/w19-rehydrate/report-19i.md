# Wave 19i: the two-fold rule for the root's case

Branch `closeout/w19-rehydrate`. Workflow `wf_2081a1f9-7ba`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## fix:rehydrate:i1: status `done`, head `713cb8f4aa84db2228ab41d4c743d7c72c0e6574`

### Summary

WAVE 19h VERIFY MAJOR: FIXED (red-first, strict). Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-rehydrate, branch closeout/w19-rehydrate, eca33155 -> 713cb8f4. Three commits. Nothing pushed, merged or tagged. No attribution trailers. The worktree is clean.

THE FINDING, REPRODUCED
On eca33155 (Windows), root C:\q\åsa\proj with the rule ./private/deny.txt and a file pointer C:\q\Åsa\proj\private\deny.txt: the summaries private/d* and private/de?y.txt are SHOWN; on 71e5133d they were WITHHELD. A pointer to private/id lets private/i? through the same way. Under a spaced root C:\q\åsa berg\proj, the recorded {"notebook_path":"C:\\q\\Åsa berg\\proj\\private\\deny.txt"} teaches nothing, because recordedPath's ASCII-only holdRoot leaves the root's space in place, so private/d* is SHOWN.

THE TWO-FOLD RULE (recorded in ADR 0011 §23)
Every judgement takes the safe side under both readings of the root.
- Strict reading: an ASCII letter's case folds where paths fold. Used by every judgement that SHOWS: containment/inside(), markRoot and the root unit, exactRooted, rootGlobShape/rootStretch, and rootPrefix for a cut value.
- Broad reading: case also folds by Unicode, as filepath.Rel, RE2's (?i) and paths.Key/hostperm fold it. A judgement that LEARNS or WITHHOLDS uses both and acts on the union.

Code changes, by function:
- RootRelativeBroad (new, exported): compares path elements with strings.EqualFold OR paths.Key equality. On non-fold platforms it is RootRelative.
- insideBy / keyBy / broadKey: inside() and key() now take a reading.
- note(): learns the strict names always. When the strict reading puts the path outside but the broad reading puts it inside AND the host refuses it there, note() also learns the project-relative names. That host gate keeps learning to the exact union: a long-s sibling, which hostperm does not fold, teaches only its own spelling and basename.
- recordedPath: holds the root under either reading. holdRootBroad uses broadRootSpellingOf, which is (?i) over paths.Key-lowered root and text, so İ (U+0130) is folded too.
- globSelectsKnown: uses broadKey.
- reasonWithheld: split into reasonNamesWithheld, run with holdRoot and holdRootBroad. A reason is withheld if either reading withholds it.
- Daemon adapter (rootRelatives): Evaluates the resolved root's spelling for every distinct reading (RootRelative, RootRelativeBroad, filepath.Rel). That is at most 3 Evaluates per path; the docs now say three, not two.

SWEEP: every call site, classified
- inside() callers: withheld() pathgate.go:588, patternWithheld :949, pathNamedWithheld :1256, cutValueWithheld :1306 and :1318, selectorWithheld :2152, the reason token check :2882, operationOutside :2897 and :2904. Each one WITHHOLDS what inside() finds outside, so the narrower strict reading only withholds more. These keep strict.
- withheld() as the learning gate in newPathJudge: under the strict reading the withheld set is a superset of the broad one, so this is already the union.
- key() in note(): learning, so now strict plus broad (BUG 1, fixed).
- key() in globSelectsKnown: withholding, so now broad. No effect is possible, because every caller has already withheld anything that is strict-outside (mutant m5 fails no row).
- RootRelative / asciiFoldEqual: inside(), key() and rootPrefix all decide SHOWN, so strict. rootPrefix was unchanged by 19h.
- holdRoot / rootSpellingOf / foldLiteral:
  - markRoot :839, :877, :1065, :1259, :1262, :1321 and exactRooted :902 decide SHOWN, so strict. Leaving the root unmarked only withholds more.
  - recordedPath decides learning: BUG 2, fixed.
  - reasonWithheld decides withholding: BUG 3, fixed.
- Adapter (rehydrate_service.go): withholding, so all readings now. On Windows eca33155 already equalled 71e5133d. On macOS eca33155 dropped filepath.Rel's reading for paths the strict reading places inside; that reading is now judged again.
- rootCover / hostRootSegments: already Unicode (paths.Key) and only add screens. Untouched.

ROWS (red-first)
Windows, with rows overlaid on eca33155, 71e5133d and HEAD:
- TestBuild_AWithheldFileUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames (private/d*, private/de?y.txt), TestBuild_AWithheldToolSummaryUnderAUnicodeCaseSpellingOfASpacedRootTeachesItsNames and TestBuild_AShortWithheldNameUnderAUnicodeCaseSpellingOfTheRootIsLearnedByItsPath (private/i?). Each has 5 subtests: U+00C5, U+212A, U+017F, U+212B, U+0130.
  - eca33155: FAIL on the 4 spellings the host folds. The U+017F subtest passes, as a pin of hostperm's own fold.
  - 71e5133d: PASS, except U+0130 (71e5133d never folded İ) and, in the file-pointer and short-name rows, U+017F (71e5133d showed that sibling pointer by its path, which is 19g's intended fix).
  - HEAD: PASS.
- TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted: FAIL 5/5 on eca33155 and on commit cd1bdbd1; PASS on 71e5133d except U+0130; PASS at HEAD.
- Daemon twin TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames uses real hostperm and real NTFS folders. Its fixture asserts SameFile for U+00C5 and not for the other four, and that the host refuses the recorded spelling exactly where it folds. It has 15 subtests.
  - eca33155: 12 FAIL (3 shapes x 4 host-folded spellings).
  - 71e5133d: PASS except U+0130 (3) and U+017F's two file-pointer shapes.
  - HEAD: PASS.
- Linux container: all learning rows and the twin PASS on all three revisions, because paths do not fold there. The reason row SKIPs on non-fold platforms (reason below and in open issues).
- 19h's rows (with their 18 sibling summaries) and every existing row still pass.

Single-fix mutants (Windows):
- m1, no broad learning in note: the 3 learning rows fail on the 4 host-folded spellings.
- m2, no broad hold in recordedPath: only the tool-summary row fails, on the same 4.
- m3, no broad pass in reasonWithheld: only the reason row fails, 5/5.
- m4, no host gate on broad learning: the 3 learning rows fail on U+017F only (over-learning).
- m5, strict key in globSelectsKnown: nothing fails, by construction.

CORPUS COUNTS
Format: shown/withheld/leaks/over-withheld, at 71e5133d | eca33155 | HEAD.

w19d corpus, 274 previews x 10 scenarios. Identical at all three revisions, 0 flips, same host-call counts.
- Windows: none-plain 209/65/0/52; uat12-plain/spaced/nonascii/long 193/81/0/53; common-plain/spaced 193/81/0/54; onedrive/dashword/bestfit 149/125/0/97.
- Linux: none-plain 208/66/0/53; uat12-* 192/82/0/54; common-* 192/82/0/55; onedrive/dashword/bestfit 149/125/0/97.

Variant corpus: 15 extra scenarios of 274 previews.
- sum-*: summaries spelled under the variant root.
- ptr-*: the denied files' pointers recorded under the variant.
- ptrall-*: all five pointers recorded under the variant.

Windows:
| Scenario | 71e5133d | eca33155 | HEAD |
|---|---|---|---|
| sum-c5 | 193/81/0/53 | 149/125/0/97 | 149/125/0/97 |
| sum-kelvin | 193/81/44/46 | 149/125/0/46 | 149/125/0/46 |
| sum-longs | 193/81/44/46 | 149/125/0/46 | 149/125/0/46 |
| sum-angstrom | 193/81/44/46 | 149/125/0/46 | 149/125/0/46 |
| sum-idot | 149/125/0/46 | 149/125/0/46 | 149/125/0/46 |
| ptr-* (all five) | 193/81/0/53 | 193/81/0/53 | 193/81/0/53 |
| ptrall-* (except idot) | 193/81/0/53 | 186/88/0/60 | 186/88/0/60 |
| ptrall-idot | 186/88/0/60 | 186/88/0/60 | 186/88/0/60 |

Host calls in the ptr-* scenarios: 1418 at 71e5133d, 596 at eca33155, 1418 again at HEAD.

Linux: identical on all three revisions. sum-* 149/125/0/46, ptr-* 192/82/0/54, ptrall-* 185/89/0/61.

Drop-reason probe: 15 templates x 24 scenarios = 360 judgements. Format: shown/withheld/leaks/over.
- Windows: 71e5133d 142/218/94/0, eca33155 134/226/102/16, HEAD 64/296/32/16.
- Linux: 134/226/110/0 on all three revisions.

Adapter probe (Windows): 144 Refuses judgements with the root handed in its long and its 8.3 spelling, so the resolved-root branch runs. The answers are identical at 71e5133d, eca33155 and HEAD.

FLIP LIST
SHOWN at HEAD but WITHHELD at 71e5133d:
- w19d corpus: none.
- variant corpus: none (Windows and Linux).
- reason probe: none (Windows and Linux).
- adapter probe: none.

Flips that eca33155 introduced:
- Variant corpus, to-withheld (all intended by 19h): 204 on Windows. That is 44 x 4 sum scenarios (19h's sibling and U+00C5 over-withholding) and 7 x 4 ptrall scenarios (main.go and server.pem basenames learned from now-outside variant pointers).
- Reason probe, to-SHOWN: 56. These are the regression. Seven glued shapes, all naming the withheld file under the U+00C5, Kelvin, long-s and Angstrom spellings, each plain and spaced:
  - path=<V>\private\deny.txt
  - (<V>\private\deny.txt)
  - file=<V>/private/deny.txt;
  - '<V>\private\deny.txt'
  - a,<V>\private\deny.txt
  - [<V>\private\deny.txt]
  - key=<V>\.env
- At HEAD all 56 are withheld again, plus 14 İ shapes that 71e5133d never withheld (eca33155 -> HEAD: 70 to withheld, 0 to shown).

The learning bug does not show up in the corpus: each build there carries one tool pointer, and its private names are already screened by rule literals. The rows are where it shows.

CHECKS (final HEAD 713cb8f4)
- go vet: windows, linux and darwin all exit 0.
- golangci-lint, fmt-check, test/docs, docmarkers and runpatterns all PASS.
- internal/rehydrate/... full: ok on Windows and Linux.
- 85 daemon rehydrate rows (84 earlier plus the twin): Windows 84 PASS and 1 platform SKIP; Linux 85 PASS.
- New rows at -count=20: Windows 80/80 and 20/20; Linux 360/360 (20 skips) and 320/320.
- New rows at -race -count=3: Windows 12 and 3 PASS; Linux 54 and 48 PASS; no DATA RACE anywhere.

ENVIRONMENT
Linux ran as transient `docker run --rm --cpus=2 -u 1000` containers of golang:1.26.6-bookworm, the image of the named container (19h's method). qompack-v6-linux-verification was not touched and is still Exited. No container is left running. Nothing touched ~/.qompack or ~/.claude.

Scratch evidence is in C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w19i:
- probe tests: zz_w19i_probe_test.go, zz_w19i_adapter_test.go
- out/*.tsv, out/win-analysis.txt, out/lx-analysis.txt
- lx/ (Linux runs)

ADR 0011 CHANGES
- New narrative paragraph stating the two-fold rule.
- Item 6: containment decides only what is shown; learning and glob selection read the root broadly.
- Items 8 and 9 corrected: U+00C5 is over-withheld only for showing; it still teaches names where the host refuses, and reasons naming it are redacted. The reason-screen paragraph now also records the glued-outside-path behaviour.
- Item 10: at most three Evaluates per judged path.
- The wave-19g criterion paragraph is corrected, and its "nothing flips" claim is qualified.
- The evidence list names the new rows.
- A new wave-19h criterion paragraph carries the red-first map, mutants, adapter probe and corpus counts.
- docs/security.md gains one paragraph.

### Commits

- cd1bdbd1 fix(rehydrate): learn a withheld path under both root readings
- 98a2aa95 fix(rehydrate): screen drop reasons under both root readings
- 713cb8f4 docs(adr): record the two-fold rule for the root's case

### Tests

- `go test -p 2 -count=1 -overlay <eca33155 pathgate.go+rehydrate_service.go> -run '^(TestBuild_AWithheldFileUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames|TestBuild_AWithheldToolSummaryUnderAUnicodeCaseSpellingOfASpacedRootTeachesItsNames|TestBuild_AShortWithheldNameUnderAUnicodeCaseSpellingOfTheRootIsLearnedByItsPath|TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted)$' ./internal/rehydrate/ (Windows)`: red-first: FAIL. 4/5 subtests in each learning row (U+017F passes as a pin); 5/5 in the reason row.
- `same pattern with -overlay <71e5133d pathgate.go+summary_screen_test.go, r4 test removed> (Windows)`: PASS, except U+0130 in all four rows and U+017F in the file-pointer and short-name rows (that sibling pointer was shown by its path on 71e5133d)
- `go test -p 2 -count=1 -run '^TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames$' ./internal/daemon/ at eca33155 / 71e5133d / HEAD (Windows, real NTFS folders)`: eca33155 FAIL 12/15; 71e5133d FAIL 5/15 (U+0130 x3, U+017F's two file-pointer shapes); HEAD PASS 15/15
- `same rows in a Linux container (uid 1000, --cpus=2) on 71e5133d, eca33155 and HEAD binaries`: PASS on all three revisions (paths do not fold); the reason row SKIPs by platform
- `go test -p 2 -count=1 -overlay <single-fix mutants m1..m5> -run <the 4 new rehydrate rows> ./internal/rehydrate/ (Windows)`: m1 fails the 3 learning rows (4 spellings each); m2 fails the tool-summary row only; m3 fails the reason row only (5/5); m4 fails U+017F in the 3 learning rows only; m5 fails nothing (by construction)
- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/...`: ok (rehydrate and rehydratetest) on Windows at 713cb8f4; PASS in Linux container
- `go test -p 2 -count=1 -v -timeout=30m -run "^(<85 daemon rehydrate rows: the 84 from w19h plus TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames>)$" ./internal/daemon/`: Windows 84 PASS + 1 SKIP (TestService_StateWriteFailureStillEmits, platform); Linux container 85 PASS
- `go test -p 2 -count=20 -run <4 new rehydrate rows> ./internal/rehydrate/ ; go test -p 2 -count=20 -run '^TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames$' ./internal/daemon/`: Windows 80/80 and 20/20 PASS; Linux 360 PASS + 20 SKIP (subtests) and 320/320 PASS
- `go test -p 2 -race -count=3 -run <same new-row patterns> ./internal/rehydrate/ and ./internal/daemon/`: Windows 12/12 and 3/3 PASS; Linux 54 PASS (3 skip) and 48 PASS; no DATA RACE
- `GOOS={windows,linux,darwin} GOARCH=amd64 go vet ./...`: exit 0 on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `W19I_OUT/W19I_REASONS/RV2_OUT=... go test -overlay <corpus + probe overlays> -run '^(TestZZW19iCorpus|TestZZW19iReasons|TestZZRev2Corpus)$' ./internal/rehydrate/ at 71e5133d, eca33155 and HEAD, Windows and Linux`: w19d corpus identical at all three revisions (0 leaks, 0 flips). Variant corpus: nothing SHOWN at HEAD that 71e5133d or eca33155 withheld. Reason probe: eca33155 showed 56 that 71e5133d redacted; HEAD shows 0 of them.
- `W19I_ADAPTER=... go test -overlay <adapter probe> -run '^TestZZW19iAdapter$' ./internal/daemon/ at 71e5133d, eca33155 and HEAD (Windows, long and 8.3 root)`: 144/144 Refuses verdicts identical at all three revisions

### Criterion changes

- No existing assertion changed in either direction (WITHHELD or SHOWN), and no golden changed. internal/rehydrate, rehydratetest, 19h's rows (with their 18 sibling summaries) and the 84 earlier daemon rehydrate rows pass unchanged on Windows and in a Linux container.
- Behaviour change (more withholding only): a withheld path recorded under the root spelled with another case of a non-ASCII letter now teaches its project-relative names (known, knownText, knownPaths) wherever the host refuses it. That covers U+00C5 Åsa, the Kelvin sign and the Angstrom sign, as 71e5133d did, plus U+0130 İ for i, which 71e5133d never learned. Under the long s (U+017F), which hostperm does not fold, only the path's own spelling and basename are learned.
- Behaviour change (more withholding only): recordedPath holds the root under the broad reading too, so a recorded value under a spaced root's Unicode-case variant is learned.
- Behaviour change (more withholding only): a drop reason is redacted when either reading of the root withholds it. Glued shapes that name a withheld path under a variant spelling are now redacted again.
- Behaviour change: the daemon host adapter judges the resolved root's spelling for every distinct reading of a path's place below the root, so a build costs at most three Evaluates per judged path instead of two. On Windows and Linux its answers equal 71e5133d's (probe: 144/144 identical). On macOS it judges filepath.Rel's reading again.
- New test helper hostFoldRules (internal/rehydrate/summary_screen_d64_r5_test.go) models hostperm's lower-casing of both rule and path. The existing hostRules helper, which models filepath.Rel's fold, is unchanged. The two differ only for U+0130 and U+017F.
- ADR 0011: corrected items 8 and 9 and the wave-19g criterion paragraph, which said the U+00C5 spelling is only over-withheld (true only for showing). Corrected item 10's Evaluate bound from two to three. Added the two-fold rule, item 6's note, the reason-screen note on glued outside paths, the evidence-list entries and a wave-19h criterion paragraph. docs/security.md gained one paragraph.

### Open issues

- Pre-existing gap, not introduced by 19h and not fixed here (out of scope): a drop reason that glues a path OUTSIDE the project to the text around it (path=C:\q\other\x.txt, (…), '…', --dir=…) and names no recorded withheld path is SHOWN. This holds under any root and on every platform, with identical results at 71e5133d and HEAD (probe TestZZGlued). The screen reads an outside path only as a ': ' chain part, an operation's path, or a whitespace token that starts with it. In the reason probe these shapes account for every HEAD leak: 32 on Windows (the 'path=' and '--dir=' templates naming a Kelvin, long-s, Angstrom or İ sibling) and 110 on Linux, where every variant is a sibling. Qompack's own reason producers that I checked (checkpoint validate.go/gitindex.go Go PathErrors, the rule and skill scan err.Error()) emit 'open <path>: …' shapes, which operationOutside catches. That is why the reason row skips on non-fold platforms.
- Accepted over-withholding, safe direction: a recorded structured value that only the broad reading can hold as the root (a spaced root's Unicode-case variant) and that the host allows is learned by its strict names (basename and whole spelling). Neither 71e5133d nor eca33155 learned it. This follows D64 ruling (7), 'recordedPath holds every root, since learning only withholds more'. The corpus cannot show it, because each corpus build carries one tool pointer.
- macOS was not run; only go vet for darwin passes. On macOS the broad reading folds by Unicode, as 71e5133d's (?i) and paths.Key already did there.
- No row distinguishes globSelectsKnown's broad key or the adapter's extra readings on Windows or Linux. Both are fail-safe by construction (mutant m5 is green; the adapter probe gives 144/144 identical answers). They exist to satisfy the two-fold rule and to restore macOS's filepath.Rel judgement.

### Needs owner

- Whether the pre-existing glued-outside-path gap in the drop-reason screen should get its own fix, by reading an absolute path after pathStartDelims, quotes and brackets inside a reason token. The D63 product-string rule leaves it open today. No current producer was found to emit such a shape. The behaviour is unchanged since 71e5133d.

## verify:rehydrate:i1: verdict `needs-fixes`, 2 finding(s)

- **major** `internal/daemon/rehydrate_hostpaths_r5_test.go:83 (TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames, U+017F subtests); internal/daemon/rehydrate_service.go:816 (rootRelatives); internal/daemon/rehydrate_hostpaths_test.go:540 (shortProjectDir)`: On macOS the new daemon twin fails its three U+017F subtests at HEAD. It would pass on 71e5133d and eca33155. This wave's adapter change makes it fail, so it turns the macos-latest whole-tree CI job red. This is a test defect, not a leak.
  - Evidence: How it fails:
- shortProjectDir resolves the temp base with EvalSymlinks only on Windows. On macOS, TMPDIR is under /var, which is a symlink to /private/var. So in the adapter `resolved` != root, and its resolved-root branch runs.
- HEAD's rootRelatives adds RootRelativeBroad. On darwin, paths.DefaultFold() is true, so it compares path elements with strings.EqualFold, which folds the long s onto s. I verified EqualFold("sam","ſam") and EqualFold("sam berg","ſam berg") are both true.
- So the adapter evaluates <resolved>/private/deny.txt. hostperm anchors ./private/deny.txt at the root's resolved spelling too (newRuleSet -> anchorVariants -> spellings -> resolveLinks), so that path is denied.
- refuses(recorded) is therefore true, but line 83 requires refused == (fold && rc.hostFolds) == false, and the row then requires private/d* etc. to be shown.
- On 71e5133d and eca33155 the darwin adapter had no Unicode-folding reading: filepath.Rel folds nothing on darwin, and RootRelative folds ASCII only. The row would pass there.

Reproduced on Windows, since no macOS was available. A scratch overlay probe builds the twin's long-s file-pointer shape with the root handed through an 8.3 ancestor (`<tmp>\LONGAN~1\sam\proj`, which resolves to `...\longancestorname\...`). Result: refuses(long-s recorded) is true at 71e5133d, eca33155 and HEAD. On Windows filepath.Rel folds the long s too, which is why shortProjectDir's Windows-only resolution hides it there. At HEAD, private/d* and private/de?y.txt are withheld.

The repo already knows this trap: costProject (rehydrate_hostpaths_test.go:221-236) canonicalizes its root, "so that the adapter judges each path once rather than once more under a second spelling of the root".

Product behaviour is fail-safe. It over-withholds, or is correct on APFS, which folds the long s under Unicode case folding. But the same premise sits under these texts, which hold only when the root resolves to itself:
- ADR 0011's wave-19h criterion paragraph (line 2077): "under the long s ... teaches only its own spelling and basename".
- The twin's comment (r5_test.go:32).
- cd1bdbd1's note().

Everything else checked out:
- Red-first:
  - The 4 rehydrate rows on eca33155 failed 4/4/4/5 subtests.
  - On 71e5133d they passed except U+0130 and, in the file-pointer and short-name rows, U+017F.
  - At HEAD all passed.
  - The twin failed 12 of 15 subtests on eca33155, failed 5 of 15 on 71e5133d, and passed 15 of 15 at HEAD.
- My own single-fix mutants:
  - m1 (no broad learning): the 3 learning rows fail on 4 spellings.
  - m2 (strict recordedPath): only the tool-summary row fails, on 4 spellings.
  - m3 (no broad reason pass): only the reason row fails, 5/5.
  - m4 (no host gate): only the long-s subtests fail.
  - m6 (RootRelativeBroad == RootRelative): same as m1.
- My own call-site sweep. Every inside() caller withholds on outside: withheld, patternWithheld, pathNamedWithheld, cutValueWithheld x2, selectorWithheld, reasonNamesWithheld, operationOutside x2, and items.go's four withheld() gates. markRoot, exactRooted and rootPrefix only decide SHOWN. Learning happens only in note(), and its relative set is now a superset of 71e5133d's filepath.Rel reading under the same host gate.
- My own 5544-row variant probe (7 variants x plain/spaced x 11 learning sources x 26 summaries, plus 9 reasons): 0 flips to SHOWN at HEAD versus 71e5133d or eca33155, on Windows and Linux.
- The seat's w19d, variant and reason corpora reproduce exactly on Windows and Linux (transient uid-1000 --cpus=2 containers, removed).
- go vet (windows, linux, darwin), golangci-lint, fmt-check, test/docs, docmarkers and runpatterns all pass.
- internal/rehydrate/... full: ok on Windows and Linux.
- The 85 daemon rehydrate rows: Windows 84 pass and 1 platform skip; Linux 85 pass.
- New rows at -count=20: Windows 80/80 and 20/20; Linux 360 pass with 20 skips, and 320/320.
- New rows at -race -count=3: Windows 12 and 3 pass; Linux 54 (3 skips) and 48 pass; no data race.
- No existing assertion was changed. The worktree is clean at 713cb8f4.
- I found the named container qompack-v6-linux-verification running and left it alone, because I did not start it.
  - Fix: In the twin, canonicalize the root on every platform (shortProjectDir, then filepath.EvalSymlinks, as costProject does) so the fixture's resolved == root premise holds, and state that premise in the comment. Alternatively, expect the long-s spelling to be refused, and its globs withheld, whenever the root resolves elsewhere. Either way, qualify the long-s sentence in ADR 0011 (line 2077) and the r5 comments: where the root resolves elsewhere, the adapter's broad reading (and filepath.Rel's on Windows) refuses the long-s spelling, and it teaches project-relative names. That is the safe direction. Run hosted macOS CI before merging.
- **nit** `docs/adr/0011-rehydration-budget-and-item-order.md:1534 vs :1550 (item 9, drop-reason screen)`: Item 9 opens with an unqualified guarantee that no drop reason shows an absolute path outside the project. The sentence this wave added (correctly) says a reason that glues an outside path to its text is shown on every platform. The ADR now contradicts itself within one paragraph.
  - Evidence: Line 1534: "No drop entry's reason, in section 7 or in `dropped()`, shows an absolute path outside the project or names a path the build withholds." Lines 1550-1553: "A reason that glues a path outside the project to the text around it (`path=/q/other/x`, `(…)`) and names no recorded withheld path is shown, under any root and on every platform". The behaviour is pre-existing and the same on 71e5133d. The seat routed the code gap to the owner. Qompack's checkpoint producers I checked (gitindex.go `stat .git: %v` and `reading index: %v` PathErrors) emit `open <path>:` shapes, which operationOutside catches.
  - Fix: Qualify the opening guarantee. Limit it to the shapes the screen reads: a chain part, an operation's path, a whitespace token, or a withheld path's whole spelling. Say it holds for the reasons Qompack's producers emit, and point to the open owner question on glued shapes.

## fix:rehydrate:i2: status `done`, head `54d4889a`

### Summary

Wave 19i round 2 is done. The round had one major finding, it is fixed, and I reproduced it red before fixing it. Worktree: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-rehydrate, branch closeout/w19-rehydrate, 54d4889a on top of 713cb8f4. Nothing was pushed, and the worktree is clean.

FINDING: the macOS daemon twin is a test defect, not a leak. The verifier's diagnosis is confirmed.
- The defect: TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames expects the long-s (U+017F) spelling of the root not to be refused. That holds only while the root resolves to itself.
- shortProjectDir resolves the temporary directory only on Windows. macOS's temporary directory is under /var, which is a link to /private/var. So on macOS the adapter's rootRelatives also checks the path through RootRelativeBroad (which folds the long s to s) under the resolved root, and the host refuses private/deny.txt there.

RED FIRST (no Mac was available, so I used two probes)
- (a) Windows, with an overlay that hands the twin's base through its own 8.3 spelling (via GetShortPathName), so the root resolves elsewhere:
  - 713cb8f4's twin fails the three U+017F subtests at r5_test.go:83 ("fixture: the host refuses the recorded spelling as it folds case"). The other 12 pass.
  - The fixed twin passes 15/15.
- (b) Emulated macOS: a Linux test binary with paths.DefaultFold, hostperm's fold and the twin's `fold` forced on (so case folds and filepath.Rel folds nothing, as on darwin). It ran in a transient uid-1000 --cpus=2 container with TMPDIR reached through a symlink.
  - 713cb8f4's twin fails the same three subtests at line 84 (the same check; the file has one more line).
  - With 71e5133d's or eca33155's pathgate.go and rehydrate_service.go, those three pass. This confirms the macOS red came from round 1's adapter change. The other 12 fail on both revisions: eca33155 as on Windows, and 71e5133d because macOS containment used filepath.Rel, which folds nothing.
  - The fixed twin passes 15/15 with TMPDIR both canonical and linked.

FIX (a4cbf519)
- The twin now makes its root canonical on every platform: MkdirAll, then filepath.EvalSymlinks, as costProject already does.
- Its comment now states the premise: what happens when the root resolves elsewhere, and that refusing there is the safe direction.
- No assertion changed. The red-first map is unchanged: on Windows the fixed twin fails 12/15 on eca33155 and 5/15 on 71e5133d (U+0130 x3, plus U+017F's file pointer and short name). In a real Linux container it passes on all three revisions. Under emulated macOS it fails 12/15 on each old revision, and the long-s subtests pass.

DOCS (54d4889a)
- ADR 0011 §23, wave-19h criterion paragraph: the long-s sentence now holds only where the root resolves to itself. Where the root resolves elsewhere (a link, an 8.3 name, macOS's /var), the long-s spelling is refused and teaches its project-relative names. That over-withholds on NTFS, which keeps that folder separate, and is the safe direction where the volume folds the two together.
- The twin's description now says it runs under a root that resolves to itself.
- The sweep sentence records that on macOS the adapter's broad reading is new and only refuses more.
- A new paragraph, "Criterion changes (wave 19i's verify of the two-fold rule)", records the defect, both probes, the fix and the maps.
- Comments updated: unicodeCaseSpellings in internal/rehydrate/summary_screen_d64_r5_test.go, and rootRelatives in internal/daemon/rehydrate_service.go (comment only). The rootRelatives comment had said RootRelativeBroad folds case "as the host's rules fold a path"; it also pairs the long s with s, which the host's lower-casing does not.

WIDER CHECK UNDER EMULATED macOS (85 daemon rehydrate rows, TMPDIR canonical and linked)
- HEAD: 84 pass, 1 fail. The failure is the r4 row TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt, and it comes from the emulation: that row's own `fold` check still reads Linux, so it expects the upper-case calls to be withheld.
- eca33155: that row plus the twin fail.
- 71e5133d: those two plus TestRehydrateHostPaths_ARuleOverTheRootIsMatchedAsTheHostMatchesIt fail.
- So under the macOS reading, no row that passed at 71e5133d or eca33155 fails at HEAD.

CHECKS
- go vet for windows, linux and darwin: clean.
- golangci-lint, fmt-check, test/docs, docmarkers and runpatterns: all pass.
- internal/rehydrate/... in full: ok on Windows and Linux.
- The 85 daemon rehydrate rows: Windows 84 pass and 1 platform skip (TestService_StateWriteFailureStillEmits); Linux (uid 1000) 85 pass.
- The round's rows at -count=20: twin 20/20 (300/300 subtests) on Windows and Linux. The four rehydrate r5 rows: 80/80 on Windows; Linux 60 pass and 20 skips (the reason row skips where paths do not fold).
- At -race -count=3: twin 3 (45 subtests) on Windows and Linux; rehydrate r5 rows 12 on Windows, and 9 plus 3 skips on Linux. No data race.
- Linux runs used transient `docker run --rm -u 1000:1000 --cpus=2 golang:1.26.6-bookworm` containers, all removed. The named container qompack-v6-linux-verification (running another audit) was only inspected read-only; nothing was run in it and nothing was left running. Scratch evidence is under the session scratchpad, folder w19i-r2.

### Commits

- a4cbf519 test(daemon): resolve the long-s twin's root on every platform
- 54d4889a docs(adr): say where the long s's answer holds

### Tests

- `go test -p 2 -count=1 -overlay <probe83/overlay-oldtwin.json: base via its own 8.3 spelling + 713cb8f4 twin> -run '^TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames$' ./internal/daemon -v (Windows)`: FAIL as expected: U+017F file pointer, tool summary and short name fail at r5_test.go:83; other 12 pass
- `same with <probe83/overlay.json> (fixed twin)`: PASS 15/15
- `emulated darwin (paths.DefaultFold, hostperm fold, twin fold forced on; Linux binary; transient uid-1000 --cpus=2 container) -test.run '^TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames$', TMPDIR canonical and via symlink`: 713cb8f4 twin: canonical 15/15, linked fails the 3 U+017F subtests; on 71e5133d/eca33155 product code (linked) U+017F x3 pass, 12 others fail; fixed twin: 15/15 both; fixed twin on 71e5133d/eca33155: 12/15 fail each
- `go test -p 2 -count=1 -overlay <ov-win-eca33155.json / ov-win-71e5133d.json> -run '^TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames$' ./internal/daemon -v (Windows, fixed twin)`: red-first preserved: eca33155 fails 12/15; 71e5133d fails 5/15 (U+0130 x3, U+017F file pointer and short name)
- `Linux real (container): dm-{head,71e5133d,eca33155}.test -test.run '^TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames$'`: PASS on all three revisions
- `GOOS={windows,linux,darwin} go vet ./...`: PASS (clean)
- `go run ./tools/devtool fmt-check`: PASS
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS (381 plan documents)
- `go test -p 2 -count=1 ./test/docs/`: ok
- `go test -p 2 -count=1 ./internal/rehydrate/...`: ok on Windows; Linux binaries rehydrate and rehydratetest PASS
- `go test -p 2 -count=1 -timeout=30m -v -run '<the 85 daemon rehydrate rows, scratchpad w19i-r2/dp.txt>' ./internal/daemon/`: Windows 84 pass + 1 platform skip (TestService_StateWriteFailureStillEmits); Linux uid 1000 85 pass
- `go test -p 2 -count=20 -v -run '^TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames$' ./internal/daemon/`: Windows 20/20 (300/300 subtests); Linux 20/20 (300/300)
- `go test -p 2 -count=20 -v -run '^(TestBuild_AWithheldFileUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames|TestBuild_AWithheldToolSummaryUnderAUnicodeCaseSpellingOfASpacedRootTeachesItsNames|TestBuild_AShortWithheldNameUnderAUnicodeCaseSpellingOfTheRootIsLearnedByItsPath|TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted)$' ./internal/rehydrate/`: Windows 80/80; Linux 60 pass + 20 skip
- `go test -p 2 -race -count=3 -v -run '^TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames$' ./internal/daemon/`: Windows 3 pass (45 subtests), Linux 3 pass (45), no data race
- `go test -p 2 -race -count=3 -v -run '<the four rehydrate r5 rows above>' ./internal/rehydrate/`: Windows 12 pass; Linux 9 pass + 3 skip; no data race
- `emulated darwin 85-row sweep (TMPDIR canonical and linked) at HEAD, eca33155, 71e5133d`: HEAD 84/85: only the r4 row fails, because its own GOOS fold flag is not emulated; eca33155 adds the twin; 71e5133d adds the twin and the rule row; nothing passes on an old revision and fails at HEAD

### Criterion changes

- TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames: the fixture's root is now canonical on every platform (shortProjectDir, then MkdirAll, then filepath.EvalSymlinks, as costProject does), so the long s's expectation rests on a root that resolves to itself. No assertion changed in either the WITHHELD or SHOWN direction. Recorded in ADR 0011 §23 under 'Criterion changes (wave 19i's verify of the two-fold rule)'.
- ADR 0011 §23, wave-19h criterion paragraph: the long-s sentence is now qualified to 'where the root resolves to itself'. Where the root resolves elsewhere, the long-s spelling is refused and teaches its project-relative names, which over-withholds on NTFS. The sweep sentence records that the macOS adapter's broad reading is new and refuses only more.

### Open issues

- Hosted macOS CI has never run any wave-19 commit. The latest CI run is on verify/v6 d20309c0, 2026-10-02. The emulated-darwin container probe is the closest substitute available here, and it does not emulate APFS's own case folding.
- Pre-existing since wave 19g, not changed this round: the r4 daemon row TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt requires on every platform that the Kelvin-sign, long-s and Angstrom-sign sibling folders are other directories (os.SameFile false). If a case-insensitive APFS volume folds those names onto the root's folder by Unicode case folding (the verifier believes it folds the long s), that fixture check fails on macos-latest. Unverified.
- Observation for the coordinator, not acted on: if APFS folds the long s, then in a macOS session whose root resolves to itself, the long-s spelling names the project's own denied file. hostperm's strings.ToLower does not refuse it, so a glob that selects it is shown (globs are judged as written, D60(iv)). That is the same answer at 71e5133d, eca33155 and HEAD. Showing it follows the host's own judgement, but it is a platform-folding gap that D66(e)'s fail-closed fallback could cover if the coordinator rules so.

### Needs owner

- Run hosted CI (macos-latest whole-tree job) on closeout/w19-rehydrate at 54d4889a before merging, as the verifier asked. A push is required, and this seat may not push.

## verify:rehydrate:i2: verdict `needs-fixes`, 1 finding(s)

- **major** `internal/daemon/rehydrate_hostpaths_r4_test.go:65 (TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt, subtest seg "åsa" / sib "Åsa", from line 54). Added in c16b21d5 (wave 19h). Round 2 did not touch it, but it merges together with this branch.`: The r4 daemon row requires, on every platform, that the Angstrom-sign sibling folder is a different directory from the root's folder (require.False(os.SameFile)). On macOS's default volume (APFS, case-insensitive and normalization-insensitive) it is the same directory, so this subtest most likely fails on the macos-latest whole-tree job. That is the same kind of problem round 2 was asked to fix. This is a test defect, not a leak: the product withholds the sibling's calls under the strict reading either way. The seat listed this row as open issue 2 and marked it 'unverified', resting on whether APFS folds the Kelvin sign and the long s by Unicode case folding. The Angstrom subtest does not depend on that.
  - Evidence: WHY IT FAILS ON macOS
- Apple documents APFS as normalization-insensitive (by hashing normalized names) in both its case-sensitive and case-insensitive variants since High Sierra.
- U+212B (Angstrom sign) has a singleton canonical decomposition to U+00C5, so NFD gives A+U+030A. The root's U+00E5 decomposes to a+U+030A.
- The default macOS volume is case-insensitive, which pairs A with a.
- So looking up `.../Åsa/proj` finds the root's own `åsa` folder. writeProjectFile(sib, ...) writes into the project, and os.SameFile(root, sib) is true.
- Line 65 then fails with "fixture: the sibling is another directory".
- A published paper (arXiv 2211.16735) also says APFS treats the Kelvin sign as `k`. That would fail the `kate` subtest too, but I put less weight on it: the same paper says NTFS does this, and this machine's NTFS keeps the folders apart, which is why the row passes on Windows.
- No macOS host was available. My emulated-darwin probe (below) cannot model APFS folding, so this rests on documented APFS behaviour.

SUGGESTED FIX (red-first, record it as a criterion change)
- Make the fixture's precondition depend on the platform; do not change the WITHHELD assertion.
- Where os.SameFile reports that the sibling spelling opens the root's folder (APFS), still require every sibling call to be withheld. The strict reading places that spelling outside the project, so this is safe over-withholding.
- Alternatively, skip creating the sibling where it is not a separate directory.
- Qualify ADR 0011 §23's NTFS-only statement to say what happens on APFS.
- Run hosted macOS CI before merging. It is already a named merge condition.

EVERYTHING ELSE IN ROUND 2 CHECKED OUT (HEAD 54d4889a; worktree clean; I committed nothing)
- Product code:
  - 713cb8f4..HEAD changes no non-comment line of product code. The only product file touched is rehydrate_service.go, and only its comments.
  - The twin's assertions are unchanged. The fixture now canonicalizes its root with MkdirAll + filepath.EvalSymlinks, as costProject does. That changes behaviour only where TMPDIR is a link, so nothing is weakened on Windows or Linux.
- Red-first, Windows probe:
  - I wrote my own overlay that hands shortProjectDir's base back as its 8.3 name (GetShortPathName), e.g. Temp\Q20500~1.
  - With 713cb8f4's twin, the 3 U+017F subtests fail at r5_test.go:83; the other 12 pass.
  - With the fixed twin, 15/15 pass.
  - With the old twin and the long s expected refused, 15/15 pass. This confirms the ADR's new claim: when the root resolves elsewhere, the long-s spelling is refused and its globs are withheld.
- Fixed twin against older revisions (Windows overlays): eca33155 fails 12/15; 71e5133d fails 5/15 (U+0130 x3, plus U+017F's file-pointer and short-name subtests). This matches the seat's map.
- Emulated darwin (Linux; paths.DefaultFold, hostperm's fold and the twin/r4 `fold` forced on; transient uid-1000, --cpus=2 container, removed):
  - Fixed twin: 15/15 with TMPDIR canonical and with TMPDIR through a symlink.
  - 713cb8f4 twin: 15/15 canonical; through the symlink, the 3 U+017F subtests fail.
  - 71e5133d and eca33155, with either twin: 12 fail and the long-s 3 pass, in both TMPDIR modes. This confirms the macOS red came from round 1's adapter change.
  - 105 daemon rows with TMPDIR through the symlink: HEAD 105/105; eca33155 fails only the twin; 71e5133d fails the twin, the r4 row and ARuleOverTheRootIsMatchedAsTheHostMatchesIt.
- My own call-site sweep. Each caller below is classified as show-deciding or withhold/learn-deciding:
  - inside(): every caller withholds when inside() says outside. That covers withheld (and through it items.go's 4 gates and gateCheckpointDrops), patternWithheld, pathNamedWithheld, cutValueWithheld x2, selectorWithheld, reasonNamesWithheld and operationOutside x2.
  - insideBy and its classReading recursion: run under the reading they are given.
  - key: used only by note (learn). broadKey: used by note, behind the host-refusal gate, and by globSelectsKnown (withhold); a strict-inside path gets the same key under both readings.
  - recordedPath (learn): accepts a path under either root reading.
  - holdRoot: used strictly by markRoot and exactRooted (show-deciding). reasonWithheld (withhold) holds the root under both readings.
  - rootPrefix and asciiFoldEqual: show-deciding, strict.
  - foldLiteral: builds the strict rootSpelling.
  - hostRootSegments, rootCover and segMatch: use paths.Key (Unicode fold) for screens, the withholding side.
  - The adapter refuses if any of its readings is refused (strict, broad, filepath.Rel).
  - items.go:311 filepath.Rel: only the checkpoint artifact's own pointer, unchanged.
  - Result: no narrowed withhold remains.
- My own flip probe, through the real adapter and real hostperm on real folders:
  - Scope: 4 roots x plain/spaced x root resolving to itself or elsewhere (8.3 on Windows, a symlink on Linux) x 4 spellings (as written, ASCII upper, Unicode variants) x 12 learning sources x 24 summaries, plus 7 reasons. 24064 verdicts per revision.
  - Windows: 0 SHOWN at HEAD that 71e5133d or eca33155 withheld or redacted; 2274 and 1116 more withheld at HEAD. The probe is sensitive: private/d*, private/i?, private/* and {"pattern":"private/d*"} are withheld at HEAD and shown at eca33155.
  - Linux: identical across all three revisions.
  - Emulated darwin: 0 flips vs eca33155. The 1424 flips vs 71e5133d all come from the pure-ASCII case spellings (KATE, SAM, IRIS, åSA), which 19h intended to read as the project on macOS. In the same scenarios the relative glob and the real-root reason are shown on both revisions.
- ADR 0011 §23's new and edited text, the twin comment, the unicodeCaseSpellings comment and the rootRelatives comment are accurate. Each count I re-measured matches.
- Commits: conventional, subjects of 62 and 46 characters, a Refs footer, and no attribution trailers.
- Checks:
  - go vet (windows, linux, darwin), golangci-lint, fmt-check, test/docs, docmarkers and runpatterns: all pass. golangci-lint first hit another process's 'parallel golangci-lint is running' lock; the retry passed.
  - internal/rehydrate/...: ok on Windows and Linux.
  - 105 daemon rehydrate rows (the 77 rehydrate rows plus every test in the files that call rehydrate.*): Windows 104 pass plus the platform skip TestService_StateWriteFailureStillEmits; Linux uid 1000, 105 pass.
  - New rows at -count=20: twin 20/20 (300 subtests) on Windows and Linux; the four r5 rows 80/80 on Windows, and on Linux 60 pass plus 20 skips (the reason row).
  - New rows at -race -count=3: twin 3 (45 subtests) on Windows and Linux; r5 rows 12 on Windows, 9 plus 3 skips on Linux; no data race.
  - All Linux runs used transient `docker run --rm -u 1000:1000 --cpus=2 golang:1.26.6-bookworm` containers, all removed. The named container qompack-v6-linux-verification was neither used nor touched.
  - Scratch evidence is in the session scratchpad, folder w19i-r2v.
  - Fix: In the r4 row, make the separate-directory precondition depend on the platform, red-first. Where os.SameFile shows that the Angstrom (or Kelvin or long-s) spelling opens the root's own folder, as on APFS, keep requiring the sibling's calls to be withheld (safe over-withholding under the strict reading) instead of failing the fixture; or skip creating the sibling there. Record it as a criterion change in ADR 0011 §23, qualify the NTFS-only sentence for APFS, and run the hosted macos-latest whole-tree job before merging.

