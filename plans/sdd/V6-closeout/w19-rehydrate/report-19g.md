# Wave 19g rehydrate: D64 open items

Branch `closeout/w19-rehydrate`. Workflow `wf_a9b6cc1b-a92`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## fix:rehydrate:g1: status `done`, head `71e5133d`

### Summary

I carried out all four of the coordinator's D64 rulings on wave 19f's open items, writing each failing test first, in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-rehydrate on branch closeout/w19-rehydrate. HEAD is 71e5133d. Nothing was pushed. Each of the three fix commits passes the rehydrate package and the daemon rehydrate rows on its own.

(1) Cut value vs root (c2a60db3). `rootPrefix` (internal/rehydrate/pathgate.go) now compares the raw value byte for byte with the cleaned root. On Windows it also accepts the root spelled with `/` throughout. Only ASCII letter case folds, and only on Windows and macOS (new `asciiFoldEqual`). Nothing is deleted and no whitespace is folded; any other value is judged by the directory it spells. `rootKey` is gone; `rootSegs` stays in screen form for `rootCover`. A repeated separator, which names the root, is now withheld (over-withholding).
- New rows: `TestBuild_ACutValueIsTheRootOnlyInItsOwnSpelling` covers siblings differing by `'`, a backtick, `^`, a doubled space, and on Linux `"`, a backslash and a tab; `<q>/obrien` beside a root `o'brien`; and a Kelvin-sign sibling of `kate`. Daemon twin: `TestRehydrateHostPaths_ACutValueIsTheRootOnlyInItsOwnSpelling`, which uses the store's own cut.
- A mutant that folds case by Unicode (as `paths.Key` does) fails the Kelvin row on Windows.

(2) Windows best-fit characters (28637640). I measured `WideCharToMultiByte` with best-fit on, over every Unicode scalar value, in all 14 Windows ANSI code pages (874, 932, 936, 949, 950, 1250-1258). Among the letters, marks and digits the whitelist admitted, 16 best-fit to an ASCII character that is not a letter or digit:
- Inside the Spacing Modifier Letters block: U+02B9, 02BA, 02BC, 02C6, 02C7, 02C8, 02CB, 02CD.
- Outside it: U+01C0 to `|`, U+01C3 to `!`, and the combining marks U+0300, 0302, 0303, 030E, 0331, 0332.
- No other modifier letter maps to punctuation in an ANSI page. Modifier symbols were never on the whitelist, so they needed nothing.
- New `bestFitPunct` holds the whole U+02B0-02FF block plus those 8 outside it. New `wordRune` excludes them and is now used by `safeChars`, `notURLRune` and `rootUnitAdmitted`.
- Rows: `TestBuild_ABestFitCharacterIsOutsideTheWhitelist` and `TestBuild_ABestFitRootHoldsNoRootUnit` (88 code points each). The Windows-only `TestWhitelist_NoANSIBestFitToPunctuationIsSafe` repeats the measurement on each run and pins the list.
- Reverting only the whitelist exclusion fails only the character row; reverting only the root-unit exclusion fails only the root row. The root unit hides the root's characters from the whitelist, so both exclusions are needed.

(3) Dash word in the root (b283dbb3). `rootUnitAdmitted` refuses any root whose spelling contains " -". Row: `TestBuild_ARootWithADashWordHoldsNoRootUnit`. Daemon twin: `TestRehydrateHostPaths_ABestFitOrDashRootHoldsNoRootUnit`, which also covers three best-fit roots.

(4) Ratified behaviours. These are recorded in ADR 0011 §23 items 7(b), 8 and 9, with no code change. I added one pin row, `TestBuild_AWithheldPathIsLearnedUnderEveryRoot`, which shows `recordedPath` still learns under every root; it passes on 895d41f4. The ADR also gets the red-first map, the corpus counts and the size update. docs/security.md is updated to match.

Red on 895d41f4 (Windows / Linux container):
- Cut row: all 4 roots fail on Windows; 3 fail on Linux, where the Kelvin row passes because Linux paths don't fold case.
- Best-fit rows: 45 of 88 fail each, on both platforms. The 43 Sk code points and the controls pass.
- Dash row: 5 of 5 dash roots fail; its 3 controls pass.
- Windows measurement row fails at U+01C0.
- Daemon twins fail on all 5 roots and on the cut sibling; the control root passes.

Corpus counts (w19d, 274 previews x 7 scenarios):
- Standard scenarios: nothing flips. Under UAT-12's rules both 895d41f4 and HEAD show 193 and withhold 81 on Windows (0 leaks, 53 over-withheld), and show 192 / withhold 82 on Linux (0 leaks, 54 over-withheld). none-plain is 209/65 on Windows and 208/66 on Linux; common-* is 193/81 (54 over) on Windows and 192/82 (55 over) on Linux, unchanged.
- Under roots `OneDrive - Contoso`, `John -Force` or `aʼb` (U+02BC): HEAD shows 149 and withholds 125 (0 leaks, 97 over-withheld), against 193/81 on Windows (44 flips each) and 192/82 on Linux (43 flips each). Every flip is shown to withheld and names nothing private.

Size: pathgate.go is 2766 lines (2687 before), 1809 non-blank non-comment lines, 118 functions; gocyclo total 797 with max 26, gocognit total 756 with max 38.

Criterion changes: no existing row changed in either direction, and no golden changed. The new rows are listed in criterion_changes.

### Commits

- c2a60db3 fix(rehydrate): match a cut value to the root's exact spelling
- 28637640 fix(rehydrate): leave best-fit punctuation out of the whitelist
- b283dbb3 fix(rehydrate): give a root with a dash word no root unit
- 71e5133d docs(adr): record D64's rulings on wave 19f's open items

### Tests

- `go test -p 2 -count=1 -run '^(TestBuild_ACutValueIsTheRootOnlyInItsOwnSpelling|TestBuild_ABestFitCharacterIsOutsideTheWhitelist|TestBuild_ABestFitRootHoldsNoRootUnit|TestBuild_ARootWithADashWordHoldsNoRootUnit|TestBuild_AWithheldPathIsLearnedUnderEveryRoot|TestWhitelist_NoANSIBestFitToPunctuationIsSafe)$' ./internal/rehydrate/ -v (on 895d41f4: new tests with the old pathgate.go)`: Fails as expected. Windows: cut 4/4 subtests, best-fit 45/88 each, dash 5/8, measurement row fails, learning pin passes. Linux container: cut 3/4, best-fit 45/88 each, dash 5/8.
- `go test -p 2 -count=1 -run '^(TestRehydrateHostPaths_ABestFitOrDashRootHoldsNoRootUnit|TestRehydrateHostPaths_ACutValueIsTheRootOnlyInItsOwnSpelling)$' ./internal/daemon/ -v (on 895d41f4)`: Fails as expected on Windows and Linux: 5 roots fail and the control passes; the cut twin fails.
- `go test -p 2 -count=20 -run '<the six new rehydrate rows>' ./internal/rehydrate/ -v`: PASS: Windows 120/120. Linux 100/100 (the five rows that run there).
- `go test -p 2 -count=20 -run '^(TestRehydrateHostPaths_ABestFitOrDashRootHoldsNoRootUnit|TestRehydrateHostPaths_ACutValueIsTheRootOnlyInItsOwnSpelling)$' ./internal/daemon/ -v`: PASS: Windows 40/40, Linux 40/40.
- `go test -p 2 -race -count=3 -run '<new rehydrate rows>' ./internal/rehydrate/ and -run '<the 2 daemon rows>' ./internal/daemon/`: PASS, no data races. Windows: 18 rehydrate + 6 daemon. Linux: 15 + 6.
- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/...`: PASS for rehydrate and rehydratetest, on Windows at 71e5133d and on Linux.
- `go test -p 2 -count=1 -timeout=30m -run '<the 82 daemon rehydrate rows, scratchpad d64r3/dp.txt>' ./internal/daemon/ -v`: PASS. Windows: 81 pass, 1 skips by platform (TestService_StateWriteFailureStillEmits). Linux as uid 1000: all 82 pass. Linux as root: TestService_StateWriteFailureStillEmits fails, on 895d41f4 too (container artifact: root can write a 0500 directory).
- `GOOS={windows,linux,darwin} go vet ./...`: PASS on all three, no output.
- `GOOS={windows,linux,darwin} golangci-lint v1.64.8 run ./...`: PASS on all three, no output.
- `go run ./tools/devtool fmt-check`: PASS (exit 0).
- `go test -p 2 -count=1 ./test/docs/`: PASS.
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers.
- `RV2_OUT=... go test -count=1 -overlay <corpus overlay> -run '^TestZZRev2Corpus$' ./internal/rehydrate/ (895d41f4 and HEAD, Windows and Linux; scratch harness, not committed)`: Standard scenarios: no flips (uat12-plain 193/81 Windows, 192/82 Linux, 0 leaks). Dash and best-fit roots: 149/125 at HEAD, 44 flips on Windows and 43 on Linux, all shown to withheld, 0 leaks.
- `go test -overlay <single-fix mutant> -run '<new rows>' ./internal/rehydrate/ (Windows)`: Reverting only the whitelist exclusion fails only the character row (45). Reverting only the root-unit exclusion fails only the root row (45). Reverting only the dash check fails only the dash row (5). A Unicode-fold rootPrefix fails the Kelvin subtest.

### Criterion changes

- No existing row changed, in the WITHHELD or SHOWN direction, and no golden changed. internal/rehydrate, rehydratetest and the 80 earlier internal/daemon rehydrate rows pass unchanged.
- New behaviour, pinned by new rows: a cut path-named value is the project only when it starts the root's own spelling byte for byte (ASCII case folds only on Windows and macOS). A cut sibling differing by a quote, caret, backslash, whitespace run or non-ASCII case is withheld; a repeated separator is over-withheld (TestBuild_ACutValueIsTheRootOnlyInItsOwnSpelling and its daemon twin).
- New behaviour: letters, marks and digits whose Windows ANSI best fit is ASCII punctuation (the whole U+02B0-02FF block, plus U+01C0, 01C3, 0300, 0302, 0303, 030E, 0331, 0332) are unsafe in free text, quoted runs, URLs and JSON string values, and keep a root from getting the root unit, on every platform. Words such as Hawaiian ʻokina names or a decomposed 'è' (U+0300) are over-withheld (TestBuild_ABestFitCharacterIsOutsideTheWhitelist, TestBuild_ABestFitRootHoldsNoRootUnit, TestWhitelist_NoANSIBestFitToPunctuationIsSafe on Windows).
- New behaviour: a root with a word that starts with '-' after a space has no root unit, 'OneDrive - Contoso' included (TestBuild_ARootWithADashWordHoldsNoRootUnit, TestRehydrateHostPaths_ABestFitOrDashRootHoldsNoRootUnit).
- New pin, passing on 895d41f4: recordedPath still learns a withheld path under roots with an apostrophe, a run of spaces, a dash word or a best-fit letter (TestBuild_AWithheldPathIsLearnedUnderEveryRoot).

### Open issues

- Found while measuring, outside D64's scope: a character the ANSI code page cannot hold at all reaches an ANSI-argv program as the default character '?'. That is a glob for a program that expands its own wildcards (MSVC setargv), e.g. 'de统y.txt' arrives as 'de?y.txt'. It is not a best fit, so the ruling does not cover it. Recorded in ADR 0011 §23 item 7 as open for a coordinator ruling.
- OEM code-page best fits were measured but left alone, since no command line is converted into an OEM page: U+0301 and U+0308 to an apostrophe and '"' in code page 437, U+0327 to ',', U+20DD to a tab, U+00DE/U+00FE to '_', U+02BB/U+02CA/U+30FC. Excluding U+0301 and U+0308 would withhold every NFD-accented name.
- Ruling (3) applied literally: a lone '-' after a space ('OneDrive - Contoso', the folder OneDrive for Business creates) also loses the root unit, although PowerShell passes a lone '-' as text. This costs 44 corpus summaries on Windows and 43 on Linux in such a root, all shown to withheld. The coordinator may want to confirm.
- The Linux checks ran in the local golang:1.26.6-bookworm Docker container, not on hosted CI. As root, TestService_StateWriteFailureStillEmits fails there because root writes through the 0500 directory; it fails the same way on 895d41f4 and passes as uid 1000. macOS was covered by vet and golangci-lint only; no tests ran there.

## verify:rehydrate:g: verdict `needs-fixes`, 3 finding(s)

- **major** `internal/rehydrate/pathgate.go:2228 (rootSpellingOf adds "(?i)") and :481-497 (inside() uses filepath.Rel, which compares with strings.EqualFold on Windows)`: On Windows, a root spelled with a Unicode-only case variant is still read as the project. Examples are the Kelvin sign U+212A for k, the long s U+017F for s and the Angstrom sign U+212B for å. NTFS keeps each of these as a separate sibling directory. holdRoot's case-insensitive regex marks the variant as the root inside free text, and containment treats it as inside the project. So the following are all shown: an uncut path-named value, free text, and a cut value whose cut falls past the end of the root. This was already true at 895d41f4, so the wave did not introduce it. But it is exactly the sibling that ruling (1)'s ASCII-only fold rules out, and the wave fixed it only where the store's cut falls inside the root's spelling (rootPrefix).
  - Evidence: Probe at HEAD on Windows, root C:\q\kate\proj with UAT-12 rules. Shown: {"notebook_path":"C:\\q\\Kate\\proj\\nb\\a.ipynb"} (U+212A). Shown: `cat C:\q\Kate\proj\nb\a.ipynb`. Shown: the store cut `..."notebook_path":"C:\\q\\Kate\\proj\\nb\\a.…`. Under root C:\q\src\proj, `cat C:\q\ſrc\proj\notes.txt` and its notebook_path value are both shown. The U+212B sibling of a root `åse` is shown too. 895d41f4 gives the same results. In the Linux container every one of these is withheld. Measured on NTFS here: `kate` and `Kate` (U+212A) are separate directories in one parent, and so are `src` and `ſrc`. Probes are in C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w19g/probe_zz_test.go and probe_zz2_test.go (scratch copy only).
  - Fix: Apply ruling (1)'s fold rule to the other places that compare against the root. In rootSpellingOf, fold only ASCII letters: replace (?i) with explicit [kK]-style classes. In inside(), compare root segments with asciiFoldEqual on Windows instead of relying on filepath.Rel's EqualFold. Add red-first rows for the uncut value, free text, a cut past the root and a file pointer. If the coordinator holds this outside D64, record it in ADR 0011 §23 as open for a ruling instead.
- **minor** `docs/adr/0011-rehydration-budget-and-item-order.md:1880 (criterion changes) and :1440-1443 (item 9); the seat's criterion_changes repeats the claim`: Two claims are overstated. The ADR says "a cut sibling that differs from the root by a quote, a caret, a backslash, a whitespace run or a non-ASCII case is withheld". Item 9 says "Any other value is judged by the directory it spells, outside the project". The non-ASCII-case part holds only when the cut falls inside the root's spelling. A Kelvin-sign sibling cut past the end of the root is still shown, because containment folds it (see finding 1).
  - Evidence: At HEAD on Windows, the store-cut preview `{"cell_id":"c1","new_source":"x…x","notebook_path":"C:\\q\\Kate\\proj\\nb\\a.…` (U+212A) is SHOWN under root C:\q\kate\proj. The same sibling cut inside the root (`C:\\q\\Kat…`) is withheld, as the new row expects.
  - Fix: Either narrow both sentences (and docs/security.md if it is read the same way) to a cut that falls inside the root's spelling, or fix finding 1 and keep the sentences as they are.
- **minor** `internal/rehydrate/pathgate.go:263-267 (rootSegs kept in screen form) with :2066 (ruleSegments in screen form) and :196-247 (rootCover/segMatch)`: rootCover deletes ' " ` \ ^ from both the rule segment and the root segment before calling path.Match. hostperm matches the raw segments with path.Match. Two kinds of rule anchored outside the project are affected. One uses `?` for a character the screen form deletes from the root. The other uses a negated class whose `^` the screen form deletes, which inverts the class. Both refuse project files at the host but miss rootCover, so their literal is never screened and free text naming a refused project file is shown. :263 is one of the lines ruling (1) cites, and the wave left rootSegs in screen form. This was already true at 895d41f4 and is outside D64's three rulings.
  - Evidence: Probe at HEAD with a host that, like hostperm, refuses <root>/**/*.key. Rule //c/q/o?brien/proj/**/*.key under root C:\q\o'brien\proj: `cat secret.key` and `cat docs/secret.key` are SHOWN. Rule //c/q/[^z]*/**/*.key under the plain root C:\q\proj: `cat secret.key` is SHOWN. Controls that spell the rule exactly (o'brien, p*) withhold both. Results are the same on 895d41f4 and in the Linux container (//q/…). Probes are in scratchpad/w19g probe_zz3_test.go and probe_zz4_test.go.
  - Fix: In rootCover, count a segment as matched when the raw rule segment matches the raw root segment, or when their screen forms match. Either way only widens cover, so it can only withhold more. Add a red-first row for each spelling, or route it to the coordinator as a new item.

Everything else verified at 71e5133d:
- Rulings (1)-(3) are implemented as ruled, with red-first rows. On a scratch copy of 895d41f4 with the new tests, Windows fails the cut row 4/4, the best-fit rows 45/88 each and the dash row 5/8; the measurement row is red and the learning pin passes. The Linux container (uid 1000) fails cut 3/4, best-fit 45 each and dash 5. The daemon twins fail on 5 roots plus the cut twin on both platforms.
- The Windows best-fit row re-measures the same 16 code points on this host.
- Each fix commit passes rehydrate and the 82 daemon rows on its own.
- No existing row or golden changed. Commits follow the format: Refs footer, no trailers, subjects of 64 characters or fewer.
- Ruling (4) is recorded accurately: b9505d53 is an ancestor and makes the '+' change, drop reasons hold an exactly spelled root, and the recordedPath pin passes.

Gates:
- go vet on windows, linux and darwin: clean.
- golangci-lint v1.64.8 on all three: clean.
- fmt-check, test/docs, lint docmarkers and runpatterns: PASS.
- ./internal/rehydrate/...: PASS on Windows and Linux.
- 82 daemon rows: Windows 81 pass and 1 platform skip; Linux uid 1000 all 82 pass.
- New rows at -count=20: Windows 120/120 and 40/40; Linux 100/100 and 40/40.
- New rows at -race -count=3: Windows 18 and 6, Linux 15 and 6, no races.

Corpus, reproduced:
- Standard scenarios have 0 flips: uat12-plain is 193 shown / 81 withheld on Windows (0 leaks, 53 over-withheld) and 192/82 on Linux (54 over-withheld).
- Under the OneDrive, dash-word and U+02BC roots: 149/125 at HEAD (0 leaks, 97 over-withheld), with 44 flips on Windows and 43 on Linux, every one from shown to withheld.

Size matches: 2766 lines, 1809 non-blank non-comment, 118 functions, gocyclo 797 (max 26), gocognit 756 (max 38).

