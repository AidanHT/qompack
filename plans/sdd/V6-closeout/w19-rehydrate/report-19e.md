# Wave 19e rehydrate: the whitelist's final verify and extension audit

Branch `closeout/w19-rehydrate`. Workflow `wf_0c9ed8ca-c1d`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## fix:rehydrate:e1: status `done`, head `f2171654`

### Summary

Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-rehydrate, branch closeout/w19-rehydrate, f3196046 -> f2171654. Nothing pushed, merged, rebased or tagged. No attribution trailers. Every leak was fixed by making the whitelist stricter.

FINDINGS. Each was red-first: the new row failed on f3196046 (Windows, and a Linux Alpine container for finding 1) and passes at HEAD.
(1) MAJOR, root spelling. rootSpellingOf now uses '/+' alone on Linux and macOS. On Windows it has two alternatives, one all-'/' (with the MSYS and WSL spellings and //?/) and one all-'\' (with \\?\). A mixed spelling is not the root and is judged as free text. On Windows this goes further than the brief: Git Bash also drops the backslash, so C:/q\proj reads as C:/qproj. Row: TestBuild_ABackslashInsideTheRootsSpellingIsNoRoot (summary_screen_d63_r3_test.go). At f3196046, `cat /q\proj/sibling.txt` (Linux) and `cat C:/q\proj/sibling.txt` (Windows) were shown.
(2) MAJOR, URL. I chose the stricter option by applying both. (a) urlTokenSafe accepts only letters, marks, digits and `- . _ ~ : / ? # @ & = +` after the scheme, so no ',' ';' '!' '$' quotes, parens, '*' '[' braces or backslash. (b) urlOutside judges a path start after each '=', ':' and '@' of the URL proper (the host is not a path start, so a port is not a drive). A '..' in the URL counts, and each part after '&' is judged as a token. Row: TestBuild_AURLIsSafeOnlyFromAStrictCharacterSet. At f3196046, `https://x.example,/etc/passwd`, `?f=/etc/passwd`, `?d=Temp:x`, `u@/etc/passwd` and `a/../../etc/passwd` were shown.
(3) MINOR, provider drives. New startsOutside and providerPath checks run at every path start. A name, then ':', then a non-empty rest is withheld, with these exceptions:
- a drive name holding '/', '\', '.' or '~'. I measured that PowerShell 5.1 and 7.6 refuse '.' and '~' in New-PSDrive names. So `git@github.com:x` and `127.0.0.1:8080` stay shown.
- before '::', a provider name holding '/'. A module qualifier like `Microsoft.PowerShell.Core\Registry::` is stripped to its last segment.
- the inert prefixes: 'path' (recall's selector, judged by selectorWithheld), 'sha256' (core.Hash's text form, used by expand and re_read), and an http(s) scheme followed by '//'.
I derived this list from the corpus. Only `{"query":"path:..."}` and `{"hash":"sha256:..."}` needed it, plus the URL scheme. Anything else is withheld, including `http:x`, `symbol:` and `tool:`. Row: TestBuild_APowerShellProviderDrivePathIsWithheld (Temp:, Env:, HKCU:, HKLM:, Function:, Alias:, Variable:, WSMan:, Cert:, a user-defined drive, Registry::, FileSystem::, and the drive after '=', ',', '-o', inside quotes and inside JSON).
(4) MINOR, '#'. pathStartDelims now includes '#', so a path can start after it and a '..' touching it climbs. Row: TestBuild_APathAfterACommentMarkIsJudged. At f3196046, `#/home/u/.ssh/id_rsa`, `#~/.ssh/id_rsa`, `#C:x` and `git commit -m "fix #/etc/passwd"` were shown.

EXTENSION AUDIT. Each extension's completeness argument is written in ADR 0011 §23 item 7 under 'Each extension of D63(2)'s list'. Each is pinned by an adversarial row in summary_screen_d63_audit_test.go. Three arguments could not be completed as they stood. I narrowed each extension instead of removing it, because the narrower version's argument is complete. Each was red on f3196046:
- Apostrophe between letters. It opens a quoted span that runs to the next apostrophe, so spaces and operators inside it join tokens. The root mark is the only unit judged by what follows it, and could become a sibling's name: `echo it's x --o=<root> zzsib y'z` was shown. New rule (rootInApostropheSpan): no root mark may follow the first apostrophe outside a quoted run. Row: TestBuild_AnApostropheSpanNeverJoinsTheRootToASibling.
- Quoted root before an operator. A program that is not a shell reads the run as one argument: `cat "<root> && make"` opens `/q/proj && make`. dqSibling's operator exception now applies only when the root is a word of its own after a word that does not end in a delimiter. `bash -c "cd <root> && go test ./..."` stays shown. Row: TestBuild_AQuotedRootBeforeAnOperatorIsASiblingAtAPathStart.
- Single '%'. A '%' followed by u and four hex digits (the %uXXXX escape) is now unsafe, as is one followed by a digit, '*' or '~' (batch parameters like %1, %*, %~dp0). A cut within 5 bytes after the '%' is also unsafe. Row: TestBuild_ASinglePercentIsNoEscapeOrParameter.
The other extensions' arguments hold, each with a pin row: root-led Glob preview, brace glob, glued operators, parentheses in runs, null-device tokens, '#'-led tokens and '=' runs, '..' ranges and '...' (I measured Win32 GetFullPath keeping 'C:\a\b\...\c'), the root before '|' or ';', the URL '?' and '#', screenExact, and JSON keys. At f3196046, the glob, brace, operator, parentheses, comment and JSON-key rows failed only on their Temp: and '#' spellings. The null-device, root-before-pipe and '..' rows pass on f3196046.

ROUND-2 NITS.
- JSON keys: the pathgate.go and ADR claim that D63(1) leaves keys unjudged is corrected (keys are decoded strings and are screened). New pin row TestBuild_AJSONKeyIsScreenedAsAString.
- Cost row: the daemon row TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers now compares the judged .claude/ rule and skill files as one exact set. I mutation-tested it with 6b dropped and 6a judged twice, both via a './' spelling and past the memo. The total count passed (30) for both mutants; the new assertion failed both.
- Stale regex comments are rewritten in TestBuild_ACaretEscapedSeparatorIsASeparator, TestBuild_ADriveLessGlobOutsideTheProjectIsWithheld and the daemon row TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules.
- pathgate.go has no line over 120 columns at tab width 8.
- The dead token count in notedValues is removed; recordedPath's no-space rule already implied it.

CRITERION CHANGES (all are shown-to-withheld under the provider-drive rule; no withheld row changed):
- `git log --pretty=format:%h -n 3` in TestBuild_ASinglePercentIsShownAndAPairIsNot and TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules (both rows now show `git log --format=%h -n 3`).
- `qompack recall --query=path:reports` in TestBuild_APathSelectorAfterAQuoteOrEqualsIsJudged (the row now shows `qompack recall path:reports`).

CORPUS (274 previews x 7 scenarios; I reproduced f3196046's TSV byte for byte):

| build | rev | shown | withheld | leaks | over-withheld (UAT-12) |
|---|---|---|---|---|---|
| Windows | f3196046 | 196 | 78 | 0 | 50 of 246 |
| Windows | HEAD | 193 | 81 | 0 | 53 |
| Linux | f3196046 | 195 | 79 | 0 | 51 |
| Linux | HEAD | 192 | 82 | 0 | 54 |

Under no rules the Windows build went 212/62 (49 over-withheld) to 209/65 (52). The same three previews flip in every scenario on both platforms: `git log --pretty=format:%h -n 3`, `sleep 5 && curl localhost:3000` and `{"skill":"superpowers:brainstorming"}`.

DOCS. ADR 0011 §23 gains:
- a what-changed paragraph;
- item 2 limits: user drives under an inert name, the Windows Git Bash reading of a backslash-only root, and a cd that names no directory (`cd && cat .ssh/config` is shown);
- item 7 (a), (b), the completeness section and the deliberate limits;
- item 8 (one slash style), item 10 (the cost-row term) and item 11 (size: 2580 lines, 1737 code lines, 112 functions, gocyclo 765 max 26, gocognit 732 max 38, using the pinned tools);
- evidence and a criterion-changes paragraph.
docs/security.md is updated to match. I added no report file.

Machine. Tests ran at -p 2, one go test at a time, and I killed no process. My Linux containers (qompack-w19e-*) used --rm, and none are left. An earlier mkdir of mine wrongly created C:/Users/Quant/AppData/Local/claude; it held only my files and I removed it.

### Commits

- 39f68237 fix(rehydrate): close the D63 final verify's findings
- fc6d60cc docs(adr): record the D63 final verify and extension audit
- f2171654 docs(adr): state the red-first map and the Linux corpus

### Tests

- `go test -p 2 -count=1 ./internal/rehydrate/ (Windows, HEAD f2171654)`: ok
- `go test -p 2 -count=1 -run <the 80 daemon rehydrate rows, list in scratchpad w19e/daemon_names.txt> -v ./internal/daemon/ (Windows, fc6d60cc; no code change since)`: ok: 79 PASS, 1 SKIP (TestService_StateWriteFailureStillEmits is skipped on Windows by design), 0 FAIL <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=20 -run <the 16 new rows plus TestBuild_ASinglePercentIsShownAndAPairIsNot, TestBuild_APathSelectorAfterAQuoteOrEqualsIsJudged, TestBuild_ADriveLessGlobOutsideTheProjectIsWithheld, TestBuild_ACaretEscapedSeparatorIsASeparator> ./internal/rehydrate/ (Windows)`: ok: 400 PASS, 0 FAIL <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -p 2 -count=20 -run '^(TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules|TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers|TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules)$' ./internal/daemon/ (Windows)`: ok: 60 PASS, 0 FAIL
- `go test -race -p 2 -count=3 -run <the same rehydrate rows> ./internal/rehydrate/ (Windows, CGO with msys64 gcc)`: ok: 60 PASS, no DATA RACE <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -race -p 2 -count=3 -run <the same 3 daemon rows> ./internal/daemon/ (Windows)`: ok: 9 PASS, no DATA RACE <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `Linux, static GOOS=linux test binaries in alpine:latest with the worktree mounted read-only: the whole rehydrate package`: PASS
- `Linux: the same rehydrate rows with -test.count=20`: PASS: 400 runs
- `Linux: the same daemon rows with -test.count=20`: PASS: 60 runs
- `Linux: the 80 daemon rehydrate rows`: 79 PASS, 1 FAIL: TestService_StateWriteFailureStillEmits, because the container runs as root and chmod 0500 does not deny root. Re-run with --user 1000:1000: PASS. Unrelated to this change.
- `Red-first: HEAD tests with f3196046's pathgate.go overlaid (go test -overlay), Windows`: As expected: the 4 finding rows and the apostrophe, quoted-root and single-% rows fail on their findings; the glob, brace, operator, parentheses, comment and JSON-key rows fail only on their Temp: and '#' spellings; the 2 criterion-change rows fail; the null-device, root-before-pipe and '..' rows pass. TestBuild_ABackslashInsideTheRootsSpellingIsNoRoot was also red in Linux.
- `GOFLAGS=-p=2 GOOS={windows,linux,darwin} GOARCH=amd64 go vet ./...`: clean on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: exit 0 (re-run at f2171654)
- `go test -p 2 -count=1 ./test/docs/`: ok (re-run at f2171654)
- `go run ./tools/devtool gen-mcp-docs --check`: docs/mcp-tools.md is up to date
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `Corpus: RV2_OUT=... go test -overlay <zz_rev2_corpus_test.go + adapter> -run '^TestZZRev2Corpus$' ./internal/rehydrate/, at f3196046 (pathgate.go overlaid) and HEAD, Windows and Linux`: 0 leaks in all 4 runs. UAT-12 over-withheld: Windows 50 -> 53, Linux 51 -> 54. The same 3 flips in every scenario.
- `Mutation check of the strengthened cost row: items.go overlaid with two mutants (6b judgement dropped, 6a judged twice under './' or past the memo)`: both mutants meet the count of 30 and both fail the new set assertion

### Criterion changes

- TestBuild_ASinglePercentIsShownAndAPairIsNot: `git log --pretty=format:%h -n 3` moves from shown to withheld, because `--pretty=format` is a valid PowerShell drive name (providerPath). The row now shows `git log --format=%h -n 3`.
- TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules: the same move of `git log --pretty=format:%h -n 3`, with `git log --format=%h -n 3` now shown.
- TestBuild_APathSelectorAfterAQuoteOrEqualsIsJudged: `qompack recall --query=path:reports` moves from shown to withheld, because `--query=path` is a valid drive name. The row now shows `qompack recall path:reports`.
- TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers: strengthened. The judged .claude/ rule and skill files must equal the exact expected set, each once, so a mutant that drops 6b and doubles 6a fails.
- No row that asserted a withheld spelling changed. The three narrowed extensions and the strict URL set flip no earlier row.

### Open issues

- Usefulness lost to the provider-drive rule, recorded in ADR §23 item 7's limits: `--pretty=format:%h`, `localhost:3000`, `HEAD:path`, `npm run test:unit`, `-p 8080:80`, plugin skill IDs `plugin:name`, recall's `symbol:` and `tool:`, `--query=path:x`, URLs holding ',' or a path after '=', ':' or '@' (e.g. `?next=/login`, `?q=is:open`), the root after an apostrophe outside a quoted run, and `printf %5d`. On the corpus this is 3 previews per scenario.
- Before '::', provider names are not exempted for '.' (only '/'), so a backslash pytest node ID such as `tests\test_api.py::test_x` is withheld. It is not in the corpus.
- Residual, documented: a PowerShell drive a user defines named path, sha256 or http(s) is not resolved.
- Residual, kept per the brief and documented: on Windows a root spelled with backslashes only stays the root. Git Bash, reading it unquoted, gets a drive-relative name built from the root's own segments.
- Residual, now explicit in ADR item 2: a cd that names no directory (`cd`, `cd -`, `popd`) followed by a relative name is shown. This belongs to the existing documented cd limit.
- I could not run Linux -race: no cgo cross toolchain for GOOS=linux on this host. Race ran on Windows only.
- In a root container on Linux, TestService_StateWriteFailureStillEmits fails because root ignores chmod. It passes as uid 1000. It predates this change.

### Needs owner

- Accept or reject the 3 corpus flips to withheld (`git log --pretty=format:%h`, `curl localhost:3000`, `{"skill":"plugin:name"}`). Under D63, the only way to restore them is an extension proven complete. Because any such name can be a New-PSDrive name, I found no complete extension; accepting the residual would be the owner's call.
- Confirm the Windows root ruling. The brief kept either slash, so a backslash-only root is still the root, even though Git Bash reads it unquoted as drive-relative. I made one addition: a mixed-slash spelling is no longer the root on Windows.

## review:rehydrate:privacy:e1: verdict `sound`, 0 finding(s)


## verify:rehydrate:e: verdict `needs-fixes`, 3 finding(s)

- **major** `internal/rehydrate/pathgate.go:2113 (rootSpellingOf), :2155 (markRoot); docs/adr/0011-rehydration-budget-and-item-order.md:1237 (item 8: "an apostrophe in the root (`o'brien`) is matched literally, never read as a quote"); internal/rehydrate/summary_screen_edges_r2_test.go:104 (TestBuild_AnApostropheInTheRootIsNotAnOpenQuote)`: Finding (1) was fixed only for the backslash, which is one case of a wider problem. The root unit hides every character of the root's own spelling from the whitelist. So an apostrophe, a comma, ';', '&', '$', braces, brackets, parentheses, a backtick, '!' or '%' in the root reaches the shell unjudged. A POSIX shell or PowerShell removes, splits at or expands that character, and so reads a sibling of an ancestor outside the project. This is the same shape as `/q\proj` -> `/qproj`, which finding (1) rated MAJOR. ADR item 8 lists comma and apostrophe roots as common. The space case is out of scope here: the coordinator ruled on it in D60(c).
  - Evidence: All probes ran at HEAD f2171654 through summaryWithheld, using a scratch -overlay test (scratchpad w19v/zz_probe_test.go). The worktree was not touched.
- Root C:\q\o'brien\proj: `cat C:/q/o'brien/proj/notes.tx't` is SHOWN. I printed the argv each shell builds: bash gives [C:/q/obrien/proj/notes.txt], and pwsh 7 gives arg0=[C:/q/obrien/proj/notes.txt]. On Linux, bash reads `/home/o'brien/proj/notes.tx't` as [/home/obrien/proj/notes.txt]. Also SHOWN: `cat <root>/notes.txt <root>/b.txt` and `cat <root>/notes.txt don't`. rootInApostropheSpan never sees the root's own apostrophe, because the mark hides it.
- Root C:\q\a,b\proj: `cat C:/q/a,b/proj/notes.txt` is SHOWN. pwsh reads it as an Object[] array of [C:/q/a] and [b/proj/notes.txt].
- Root C:\q\x;y\proj: SHOWN. bash runs `printf ... C:/q/x` and then runs `y/proj/notes.txt` as a second command.
- Roots holding `a&b`, `a$HOME`, `a(1)`, `a[1]`, `{a,b}`, a backtick, `a!b` or `50%off`: `cat <root>/notes.txt` and `cd <root> && go test ./...` are SHOWN for every one. bash expands `$HOME` and `{a,b}` before the path reaches any program.
- Context: every required check passes at f2171654. go vet passes for windows, linux and darwin. golangci-lint, fmt-check, test/docs, gen-mcp-docs --check and lint docmarkers,runpatterns pass. internal/rehydrate passes in full. Of the 72 daemon rehydrate rows, 71 pass and 1 skips by design on Windows. The 20 new and changed rows pass at -count=20 (400 runs) and at -race -count=3, as do the 3 daemon rows. With f3196046's pathgate.go overlaid, the new rows fail as the report says, and the null-device row passes as it says.
  - Fix: Fix it the way finding (1) was fixed, by making the rule stricter.
- Form the root unit only from a spelling whose every character is inert where it stands.
- Outside a simple double-quoted run, the root's segments may hold only letters, marks, digits, `- _ . +` and the space that D60(c) rules on.
- Inside a simple double-quoted run, `' , ; & ( ) [ ] { } #` may also stand. A spelling holding `$`, a backtick, `%` or `!` is never the root.
- Judge any other spelling as the free text it is. It is an absolute path, so it is withheld.
- Add red-first rows for apostrophe, comma, semicolon, `$` and brace roots.
- Record the criterion change in TestBuild_AnApostropheInTheRootIsNotAnOpenQuote: unquoted `cd <root> && go test ./...`, `git -C <root> status`, `<root> TODO` and `<root> **/*.go` move to withheld, and the double-quoted forms stay shown. Little usefulness is lost: in bash and PowerShell an unquoted apostrophe root with no second apostrophe is an unterminated-quote syntax error.
- Correct ADR item 8's sentence.
- **minor** `internal/rehydrate/pathgate.go:1636 (providerPath `colon+1 >= len(rest)`), reached from :1202 (cutWordUnsafe) and the cut-JSON path`: A store cut that falls right after a provider drive's colon is shown. The cut hid a non-empty rest, so by finding (3)'s own rule (<name>:<non-empty rest>) the text is a provider-qualified path outside the project. The single-letter drive is withheld in the same position.
  - Evidence: At HEAD these are SHOWN: `Get-Content Temp:…`, `cat x,Temp:…` and `{"command":"Get-Content Temp:…` (a cut JSON preview). `Get-Content C:…` is withheld.
  - Fix: In a cut word (cutWordUnsafe) and in a cut JSON value, treat a trailing `<name>:` at a path start as having a non-empty rest. Withhold it unless the name is inert. Pin this with a red-first row.
- **minor** `internal/rehydrate/pathgate.go:1631-1647 (providerPath); docs/adr/0011-rehydration-budget-and-item-order.md:1010-1011 ("A name with nothing after its `:` (`fix:`) names no path."); internal/rehydrate/summary_screen_d63_r3_test.go:106`: A bare provider drive root (`Temp:`, `Env:`, `HKCU:` or any name New-PSDrive defines) is shown at every path start. In PowerShell it names that drive's root, a directory outside the project, just as `C:` or `/tmp` would. The gate withholds both of those. The ADR's claim that a bare `name:` names no path is false for PowerShell.
  - Evidence: At HEAD these are SHOWN:
- `cd Temp: && cat secret.txt`
- `Set-Location Temp:; Get-Content secret.txt`
- `Get-ChildItem HKCU:` and `cd HKCU: && Get-ChildItem`
- `Copy-Item notes.txt Temp:` and `Copy-Item -Destination Temp: notes.txt`
- `cat --dir=Temp:`, `cat a,Temp:` and `x|Temp:`
- `pwsh -c "cd Temp: && gc secret.txt"`
`cd C: && cat secret.txt` and `cd /tmp && cat secret.txt` are withheld. `Test-Path Temp:` returns True in pwsh 7 (False in 5.1, which has no Temp: drive), and `Env:` and `HKCU:` exist in both.
  - Fix: Withhold `<name>:` with an empty rest at a path start, in tokens, pieces and quoted-run words, unless the name is inert. Record the usefulness lost: `git commit -m "fix: ..."`, and the shown row in TestBuild_APowerShellProviderDrivePathIsWithheld moves to withheld as a recorded criterion change. The only alternative is a narrower extension proven complete, and `pwsh -c "cd Temp: && ..."` has the same shape as a commit message, so no such extension is apparent. Correct the ADR sentence either way. If the owner accepts the residual, record it as an owner decision rather than claiming that no path is named.

