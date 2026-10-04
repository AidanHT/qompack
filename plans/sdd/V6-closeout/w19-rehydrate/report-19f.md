# Wave 19f rehydrate: D64 root unit

Branch `closeout/w19-rehydrate`. Workflow `wf_cab8691e-9b2`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## fix:rehydrate:f1: status `done`, head `ee280d3a`

### Summary

D64 is implemented on closeout/w19-rehydrate (f2171654..ee280d3a, 4 commits, unpushed). Every failing row was written first and seen red on f2171654.

(1) STRICT root unit. The root is now held together as one unit only when its own spelling, cleaned and slash-separated, contains nothing but Unicode letters, marks and digits, `- _ . @`, `/`, ASCII spaces, and (Windows only) the drive's colon. That check is `rootUnitAdmitted`, applied through `markRoot`. For the six characters you asked me to decide:
- `,` excluded: PowerShell splits a bare argument there, and cmd.exe built-ins split there. I measured `type a,b` typing two files.
- `=` excluded: cmd.exe built-ins split there (measured with `type a=b`).
- `#` excluded: zsh EXTENDED_GLOB reads it as a pattern.
- `:` past the drive excluded: PowerShell reads the name before it as a drive.
- `@` kept: it is only special as a whole `@name` token or before `(`, and the root never starts with it.
- `+` excluded: cmd.exe's `copy` splits there. Measured: `copy a.txt+\...\outside.txt` read the outside file.

Every character the whitelist already rejects is excluded too, including control characters and Unicode spaces. A Unicode space used to fold to an ASCII space inside the root's pattern, so a sibling folder spelled with a real space was shown as the root. A root with no unit makes every summary that spells it free text, so those summaries are withheld.

Two places keep holding the root regardless (`holdRoot`):
- Drop reasons. Without the root held, `<root>/private/deny.txt` named absolutely in a reason would reopen the w19d round-2 leak. `TestBuild_AReasonHoldsTheRootASummaryDoesNot` pins this.
- Learning withheld path names (`recordedPath`). Learning more only withholds more.

A path-named JSON value under such a root is still judged as a structured value.

(2) A store cut right after a drive or provider colon is now withheld. The cut word is judged as if a name followed the colon (`cutAtDriveColon`). This covers `Temp:` `HKCU:` `Env:` `FileSystem:` and `Temp:\`, after `,`, after `=`, glued to a short option, after `|`, inside a quoted run, inside a JSON string, and inside a URL. `C:…` was already withheld. `path:…` and `sha256:…` stay shown.

(3) No code change. The `providerPath` comment, ADR §23 item 7(b) and security.md now say that a bare name such as `Temp:`, `Env:`, `HKCU:`, `fix:` or `feat:` names a drive root, which D64 rules inert. A single-letter bare drive (`C:`) stays withheld. One consequence, recorded under item 2's cd limit: `cd Temp: && cat secret.txt` is now shown.

(4) ADR §23 records D64: what changed, item 8's character set with a reason for each exclusion, items 2, 7, 9, 10 and 11, the evidence list, and a D64 criterion-change paragraph. That paragraph accepts the three corpus flips and the Windows either-slash root.

EXTRA FINDING, fixed in b9505d53, needs ratification: working out `+` exposed a free-text leak in D63's whitelist. `copy a.txt+\Windows\win.ini out.txt` and `copy a.txt+.env out.txt` were both shown, because `+` was read as continuing a name. `+` now joins `pathStartDelims` and leaves `nameByte`. Cost: a `c++/` directory in a path is over-withheld. No earlier row flips, and the corpus does not change.

Fixture change: `shortProjectDir` in internal/daemon now uses long names on Windows (`EvalSymlinks`). Hosted Windows runners spell TEMP with an 8.3 name (`RUNNER~1`), and the `~` would remove the root unit. I emulated an 8.3 TMP locally: without the change, 10 daemon rehydrate rows fail; with it, 79 pass and 1 skips on a platform condition. Hosted CI itself was not run.

Red-first on f2171654 (old pathgate.go overlaid on the new tests):
- TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit fails on all 19 excluded roots on Windows and all 27 in an Alpine container. Each failure is a summary spelling the root that was shown, or for a Unicode-space root its ASCII-space sibling. The 4 admitted roots pass.
- TestBuild_AnApostropheInTheRootHoldsNoRootUnit fails on both roots, and the daemon twin fails on its 5 excluded roots while its control root passes. Same result on both platforms.
- In the cut row, all 12 drive and provider spellings were shown before and are withheld now.
- In the `+` row, all 9 withheld spellings were shown before.
- The reason row and the bare-drive row pass on f2171654 too; they pin what D64 leaves unchanged.

Corpus (w19d, 274 previews, UAT-12 scenario):
- Windows: 193 shown / 81 withheld, 0 leaks, 53 of 246 over-withheld, at both f2171654 and HEAD.
- Linux: 192 / 82, 0 leaks, 54 over-withheld, at both.
- No flips in any of the 7 scenarios.
- Supplementary run under an `o'brien` or `a,b` root: 149 / 125, 0 leaks, 97 over-withheld. That is 44 shown-to-withheld flips, D64's accepted cost in such a root. Host calls drop from 48 to 39.

pathgate.go size: 2658 lines, 1767 non-blank non-comment, 115 functions, gocyclo total 784 (max 26), gocognit total 741 (max 38).

Scratch evidence is in C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/d64/ (`red-win.txt`, `linux/lx-red.txt`, `win-*.tsv`, `linux/lx-*.tsv`, `extra/`, `daemon_pat.txt`).

### Commits

- 323ffe65 fix(rehydrate): hold the root unit only for a plain root (D64)
- a3e6435e docs(adr): record D64's root unit and drive-colon rulings
- b9505d53 fix(rehydrate): start a path and a name after a glued plus
- ee280d3a docs(adr): record the plus path start found under D64

### Tests

- `go test -p 2 -count=1 -overlay <scratch>/d64/old.json -run '^(TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit|TestBuild_AReasonHoldsTheRootASummaryDoesNot|TestBuild_ACutRightAfterAProviderDriveColonIsWithheld|TestBuild_ABareDriveNameNamesADriveRoot|TestBuild_AnApostropheInTheRootHoldsNoRootUnit|TestBuild_APathOrNameAfterAPlusIsJudged)$' -v ./internal/rehydrate/ (f2171654 pathgate.go overlaid, Windows and Alpine)`: RED as intended: root row fails on 19 (Windows) / 27 (Linux) excluded roots and passes on 4 admitted roots; apostrophe, cut and plus rows fail; reason and bare-drive pins pass
- `go test -p 2 -count=1 -overlay <scratch>/d64/old.json -run '^TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit$' -v ./internal/daemon/ (Windows and Alpine)`: RED as intended: fails on o'brien, a;b, a,b, a$b, a+b; control a@b-c.d passes
- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/... (Windows, HEAD ee280d3a; Linux static binary in alpine:latest)`: PASS on both
- `go test -p 2 -count=1 -timeout=30m -v -run "$(cat <scratch>/d64/daemon_pat.txt)" ./internal/daemon/ (the 80 daemon rehydrate rows)`: Windows: 79 PASS, 1 SKIP (TestService_StateWriteFailureStillEmits, platform). Linux (Alpine, as root): 79 PASS; TestService_StateWriteFailureStillEmits fails only because root ignores mode bits, and passes when re-run as uid 1000
- `TMP=TEMP=<8.3 short path> go test -p 2 -count=1 -run "$(cat <scratch>/d64/daemon_pat.txt)" ./internal/daemon/`: f2171654 gate: only the new red twin fails. D64 gate without the fixture change: 10 rows fail. With the shortProjectDir long-name fixture: 79 PASS + 1 SKIP
- `go test -p 2 -count=20 -run '<the six new/changed rehydrate rows, as above>' ./internal/rehydrate/ ; go test -p 2 -count=20 -run '^TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit$' ./internal/daemon/`: PASS: 120/120 rehydrate and 20/20 daemon on Windows; same counts on Linux
- `go test -p 2 -race -count=3 -run '<the six rows>' ./internal/rehydrate/ ; go test -p 2 -race -count=3 -run '^TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit$' ./internal/daemon/ (Windows)`: PASS: 18/18 and 3/3, no DATA RACE
- `GOOS={windows,linux,darwin} GOARCH=amd64 go vet ./...`: PASS (no output) on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: PASS (exit 0)
- `go test -p 2 -count=1 ./test/docs/`: PASS
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS both (the only notes are pre-existing waived lines in w8b-polish/report.md)
- `RV2_OUT=... go test -overlay <scratch>/d64/overlay.json -run '^TestZZRev2Corpus$' ./internal/rehydrate/ (w19d corpus, f2171654 vs HEAD, Windows and Linux)`: No flips. Windows 193/81 (0 leaks, 53 over-withheld); Linux 192/82 (0 leaks, 54). Supplementary o'brien and a,b roots: 149/125, 97 over-withheld, 44 flips

### Criterion changes

- No test that asserted a summary WITHHELD changed, and no golden changed.
- TestBuild_AnApostropheInTheRootIsNotAnOpenQuote is renamed TestBuild_AnApostropheInTheRootHoldsNoRootUnit. Its 7 SHOWN rows (the root's Grep and Glob previews, cd <root> && ... in both slash styles, git -C <root> unquoted and quoted) flip to WITHHELD under D64(1).
- TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote is renamed TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit. Its 4 SHOWN rows flip to WITHHELD. It is extended to roots holding ; , $ and +, adds a one-word Read under each root (withheld) and a path-named JSON value (shown), and adds the control root a@b-c.d, which stays shown.
- ADR evidence lists and the earlier criterion-change paragraphs now cite both rows by their new names.
- Fixture: shortProjectDir in internal/daemon now spells its temp base by long names on Windows (filepath.EvalSymlinks), because a hosted runner's 8.3 TEMP contains a ~.
- D64(4): the coordinator's acceptance is recorded for the three corpus flips to withheld (git log --pretty=format:%h, curl localhost:3000, {"skill":"plugin:name"}) and for the Windows either-slash root, where a mixed spelling is no root.
- D64(3) ADR wording: 'a name with nothing after its : names no path' becomes 'a bare name names a drive root, which D64 rules inert'. A single-letter bare drive stays withheld.
- b9505d53, pending ratification: + is now a path start and a name start in free text, and is excluded from the root unit. ls include/c++/v1 is newly over-withheld; no earlier row flips.

### Open issues

- The + finding (b9505d53) goes beyond D64's letter. It closes a measured free-text leak (copy a.txt+\Windows\win.ini out.txt and copy a.txt+.env out.txt were shown) and is recorded in ADR §23 for the coordinator to ratify with D64.
- Windows best-fit mapping, measured with code page 1252: modifier letters U+02BA become " and U+02B9, U+02BC and U+02C8 become '. The whitelist admits them as letters, in free text and in the root's own spelling. This is recorded as an open limit in ADR §23 item 2 and was not changed.
- Scope decision: the drop-reason screen and recordedPath keep holding the root even for a root with no unit. Turning the root unit off in reasons would leave a withheld path named absolutely in a reason shown (the w19d round-2 leak). TestBuild_AReasonHoldsTheRootASummaryDoesNot pins this. The coordinator may want to confirm.
- D64(3) consequence: cd Temp: && cat secret.txt is now shown. ADR item 2 records it as the existing cd limit.
- Hosted CI was not run. The 8.3-TEMP behaviour of the daemon rows is verified only by a local emulation.
- Linux -race was not run (no cgo cross toolchain); -race ran on Windows only. golangci-lint ran for the Windows target only, because devtool cannot be go-run with GOOS=linux.
- TestService_StateWriteFailureStillEmits fails in the Linux container only when run as root. This was already so before this work, and it passes as uid 1000.

### Needs owner

- Coordinator: ratify or reverse the + path-start, name-start and root-unit exclusion (b9505d53), found while working out + for D64(1).
- Coordinator: rule on the Windows best-fit modifier-letter limit (U+02BA becomes ", and U+02B9, U+02BC and U+02C8 become '), which affects the free-text whitelist and the root unit.

## verify:rehydrate:f: verdict `needs-fixes`, 2 finding(s)

- **minor** `internal/rehydrate/pathgate.go:2499 (reasonWithheld calls holdRoot), :2148 (rootSpellingOf strings.Fields), :349 (recordedPath); docs/adr/0011-rehydration-budget-and-item-order.md:1293`: D64(1) lists a Unicode space or control character in the root as giving it no root unit. Summaries now follow that, but drop reasons do not. reasonWithheld still holds such a root on purpose (holdRoot), and holdRoot uses rootSpellingOf, which folds every Unicode space and tab to one ASCII space (strings.Fields). sanitize also folds tabs and runs of spaces. So in a section 7 reason, the held 'root' matches a sibling directory spelled with ASCII spaces, and that sibling's absolute path outside the project is shown. This breaks D50 (D60(i) puts drop entries under it). This wave did not cause it: f2171654 behaves the same. But it survives the seat's reason-scope carve-out, and the ADR's item 8 says that holding the root in reasons only 'finds a withheld project path ... and keeps an allowed one'. A root with a run of ASCII spaces, which does keep its unit, leaks the same way in reasons. D60(iv)'s whitespace limit covers store previews, not reasons.
  - Evidence: Overlay probe that calls Build end to end. Root C:\q\a<U+00A0>b\proj, plus a pointer_git_unavailable drop with Detail "checkpoint: git worktree gitdir at C:\q\a b\proj\.git\worktrees\wt is unreadable" (ASCII space, outside the project). res.Dropped and res.Text both show that path verbatim. Same with U+3000. With control root C:\q\ab\proj the detail becomes "checkpoint: (path withheld)". Linux (static binary in alpine, uid 1000): reasonWithheld shows "skills: reading rules at /q/a b/proj/notes/x.md failed" under roots /q/a<TAB>b/proj, /q/a<U+00A0>b/proj and /q/a  b/proj. The same probe gives the same result with f2171654's pathgate.go.
  - Fix: In reasonWithheld (and, for consistency, recordedPath), hold the root only when its spelling survives sanitize and rootSpellingOf unchanged: no tab, CR or LF, no Unicode space, no run of spaces. Otherwise leave it unheld. The reason then splits at the root's own whitespace and fails closed, because the first piece (e.g. C:\q\a) is outside the project. Alternatively, build rootSpellingOf from the root's exact characters, collapsing only what sanitize collapses, and match reasons on their unsanitized whitespace. Add a red-first row under an NBSP root and a tab root with a sibling named in a git drop. Correct ADR item 8's reason sentence.
- **minor** `internal/rehydrate/pathgate.go:2213 (rootUnitChars = "-_.@"), :2194 rootUnitAdmitted; docs/adr/0011-rehydration-budget-and-item-order.md:1276`: D64(1) asked for '@' to be excluded if any shell can reinterpret the root at it. The seat kept '@' on the grounds that it is special only as a whole '@name' and 'cannot start the root's spelling'. But the unit exists for roots that hold spaces, and a space inside the root makes '@' start a shell word. PowerShell splats a word of the root that is a whole '@name' (followed by the root's next space or by the root's end, before a space, ';' or '|'). D63's own whitelist rejects exactly that token in free text (psSplat). So a root such as C:\q\John @Work keeps its unit, and git -C <root> status or cd <root> && ... are shown, though PowerShell reads the root as C:\q\John plus the expansion of $Work.
  - Evidence: Measured with pwsh 7 and Windows PowerShell 5.1 (pwsh -File script): with $Work = @('X:\elsewhere\secret.txt'), `f C:\q\John @Work` passes [C:\q\John] [X:\elsewhere\secret.txt]. By contrast, `f C:\q\John @Work\proj` and `f C:\q\a@Work\proj` pass the text literally. Corpus run with root C:\q\John @Work at HEAD: 193 shown / 81 withheld, identical to the plain root. `git -C C:\q\John @Work status`, `cd C:\q\John @Work && go test ./...`, `cd C:/q/John @Work && …` and `cd /c/q/John @Work && make test` are all shown.
  - Fix: Refuse the root unit when an '@' in the root's spelling follows a space, i.e. starts a space-separated word, or drop '@' from rootUnitChars. Add a row with a root ending in ' @Work' that withholds those summaries, and correct ADR item 8's '@' sentence. A leading '-' after the root's space has the same shape (a PowerShell parameter token, e.g. a root ending in ' -Force'); '-' was not on D64's list to decide, so the coordinator may want to rule on it alongside.

## fix:rehydrate:f2: status `done`, head `895d41f4`

### Summary

Round 2 fixed both verifier findings on closeout/w19-rehydrate. Three new commits (ee280d3a..895d41f4) sit on the four from round 1, so f2171654..895d41f4 is 7 commits, unpushed. Each fix uses a stricter whitelist, not a model of any shell, and each failing row was written first and seen red on ee280d3a, on Windows and in a Linux container.

FINDING 1 (drop reasons held a root that is not spelled exactly), commit 66d243a5. Confirmed. `holdRoot` finds the root after folding every run of whitespace to one ASCII space. So under a root with a Unicode space, a tab, a run of spaces or a trailing space, a reason that named the sibling spelled with one space showed that sibling's path, which is outside the project. A root with a run of spaces or a trailing space also kept its summary unit, so a summary naming that sibling was shown too.
- The fix is a new check, `rootSpelledExactly` (internal/rehydrate/pathgate.go). It passes only when `sanitize` and `rootSpellingOf` leave the cleaned root unchanged: no control character, and no whitespace except single ASCII spaces between other characters.
- `rootUnitAdmitted` now requires this check, so such a root has no summary unit.
- `reasonWithheld` holds the root only when the new `rootExact` field is set. With the root unheld, the root's own whitespace splits a path under it and the first piece is outside the project, so the reason fails closed. Cost: a reason naming a path under such a root (its own `.git\index`) is redacted too.
- I did not apply the verifier's 'for consistency' change to `recordedPath`, and I can show why. `recordedPath` only decides whether a withheld path's names are learned, and learning only withholds more. A probe overlay gated `recordedPath` the same way, under root `C:\q\a  b\proj` with rule `Read(./secrets/**)`. Result: `type token.txt` was shown under the gate and is withheld at HEAD.
- Item 8's reason sentence in the ADR now says this.

FINDING 2 (`@` in the root unit), commit 1a89183c. Confirmed, and I measured it myself with pwsh 7 and Windows PowerShell 5.1. With `$Work` set to an array, `f C:\q\John @Work` passes `[C:\q\John] [X:\elsewhere\secret.txt]`. `f C:\q\a@Work\proj` passes the text unchanged.
- `rootUnitChars` is now `-_.`, so a root with `@` anywhere has no unit. I chose dropping it outright over a word-start rule, per 'stricter whitelist; never shell modelling'.
- Item 8's `@` sentence in the ADR is corrected.
- `-` is left as it is and goes to you (see needs_owner).

DOCS, commit 895d41f4. ADR 0011 §23 is updated in the intro, item 7 (the root's unit and the deliberate limits), item 8, item 9, item 10 (39 host judgements under `John @Work`), item 11, the evidence list, and a new 'Criterion changes (the round-2 verify of D64)' paragraph. security.md is updated the same way. Every SHA quoted in the docs is an ancestor of HEAD.

Size of pathgate.go, measured with the pinned gocyclo v0.6.0 and gocognit v1.2.0 (ee280d3a, then HEAD):
- lines: 2658, 2687
- non-blank non-comment lines: 1767, 1776
- functions: 115, 116
- gocyclo total: 784, 788 (max 26 in both)
- gocognit total: 741, 744 (max 38 in both)

CORPUS (w19d, 274 previews, 7 scenarios):
- At f2171654, ee280d3a and HEAD alike, under UAT-12's rules: Windows shows 193 and withholds 81 (0 leaks, 53 of 246 over-withheld); Linux shows 192 and withholds 82 (0 leaks, 54 over-withheld). Nothing flips in any scenario.
- Extra run under root `John @Work`: Windows goes from 193/81 to 149/125 (44 flips), Linux from 192/82 to 149/125 (43 flips). Host calls drop from 48 to 39.
- Extra run under root `a  b` (two spaces): Windows goes from 166/108 to 149/125 (17 flips), Linux from 165/109 to 149/125 (16 flips).
- Every flip is from shown to withheld, none names anything private, and there are 0 leaks.

RED-FIRST on ee280d3a:
- `TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit` fails on `a@b`, `John @Work` and `a  b`. It passes on the 19 earlier excluded roots on Windows (27 on Linux) and on the 3 admitted roots.
- `TestBuild_ARootNoTextSpellsExactlyIsNeverHeld` fails on every root: 3 on Windows, 5 on Linux. The Unicode-space and tab roots fail because the sibling's gitdir is shown in a reason; the others fail on a root summary shown under the unit.
- With only the reason gate reverted, that row fails on every root on the reason alone, on both platforms.
- `TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit` fails on `a@b-c.d` and `John @Work`.
- `TestBuild_AReasonHoldsTheRootASummaryDoesNot` passes on ee280d3a; it pins behaviour this round leaves unchanged.

Scratch evidence is in C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/d64r2/: `red-win-ee28.txt`, `linux/lx-red-ee28*.txt`, `mut-reason-win.txt`, `linux/lx-mut.txt`, `corpus/win-*.tsv`, `linux/lx-c-*.tsv`, `linux/lx-x-*.tsv`, `splat.ps1`, `zz_recorded_probe_test.go`, `win-dm-short.txt`.

### Commits

- 323ffe65 fix(rehydrate): hold the root unit only for a plain root (D64)
- a3e6435e docs(adr): record D64's root unit and drive-colon rulings
- b9505d53 fix(rehydrate): start a path and a name after a glued plus
- ee280d3a docs(adr): record the plus path start found under D64
- 66d243a5 fix(rehydrate): hold no root that a text cannot spell exactly
- 1a89183c fix(rehydrate): leave @ out of the root unit (D64)
- 895d41f4 docs(adr): record D64's round-2 root unit fixes

### Tests

- `go test -p 2 -count=1 -run '^(TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit|TestBuild_ARootNoTextSpellsExactlyIsNeverHeld|TestBuild_AReasonHoldsTheRootASummaryDoesNot)$' -v ./internal/rehydrate/ with the new tests on ee280d3a's pathgate.go (Windows; Linux static binary in alpine:latest as uid 1000)`: Red, as intended. Root row fails on a@b, John @Work and a  b, passes on 19 (Windows) or 27 (Linux) earlier excluded roots and 3 admitted ones. Exact-spelling row fails on 3/3 roots (Windows) and 5/5 (Linux). The reason row passes.
- `go test -p 2 -count=1 -run '^TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit$' -v ./internal/daemon/ on ee280d3a's pathgate.go (Windows and Alpine)`: Red, as intended: fails on a@b-c.d and John @Work; o'brien, a;b, a,b, a$b, a+b and the control root a-b_c.d pass
- `go test -p 2 -count=1 -overlay <scratch>/d64r2/mut_reason.json -run '^TestBuild_ARootNoTextSpellsExactlyIsNeverHeld$' -v ./internal/rehydrate/ (rootSpelledExactly forced true in reasonWithheld only)`: Fails on every root on the reason leak: 3/3 on Windows, 5/5 on Linux
- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/... (HEAD 895d41f4; Windows, and Linux static binaries in Alpine as uid 1000)`: PASS on both (rehydrate and rehydratetest)
- `go test -p 2 -count=1 -timeout=30m -v -run "$(cat <scratch>/d64/daemon_pat.txt)" ./internal/daemon/ (the 80 daemon rehydrate rows, HEAD)`: Windows: 79 PASS, 1 SKIP (TestService_StateWriteFailureStillEmits, platform). Linux as uid 1000: 80 PASS.
- `TMP=TEMP=C:\Users\Quant\AppData\Local\QTMPDI~1 (8.3 alias, same length as C:\Users\runneradmin\AppData\Local\Temp) go test -p 2 -count=1 -timeout=30m -v -run "$(cat <scratch>/d64/daemon_pat.txt)" ./internal/daemon/`: 79 PASS, 1 SKIP. The emulation directory was removed afterwards.
- `go test -p 2 -count=20 -run '^(TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit|TestBuild_ARootNoTextSpellsExactlyIsNeverHeld|TestBuild_AReasonHoldsTheRootASummaryDoesNot)$' ./internal/rehydrate/ ; go test -p 2 -count=20 -run '^TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit$' ./internal/daemon/`: PASS 60/60 and 20/20 on Windows; same counts on Linux
- `go test -p 2 -race -count=3 -run '<the three rehydrate rows above>' ./internal/rehydrate/ ; go test -p 2 -race -count=3 -run '^TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit$' ./internal/daemon/ (Windows; Linux in golang:1.26.6-bookworm, uid 1000, read-only module cache)`: PASS 9/9 and 3/3 on both platforms, no DATA RACE
- `GOOS={windows,linux,darwin} GOARCH=amd64 go vet ./...`: PASS, no output, on all three
- `go run ./tools/devtool lint --only=golangci-lint ; GOOS={linux,darwin} <pinned golangci-lint built from tools/pinned/go.mod> run ./...`: PASS on Windows, linux and darwin
- `go run ./tools/devtool fmt-check`: PASS (exit 0)
- `go test -p 2 -count=1 ./test/docs/`: PASS
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS on both checks. The only notes are on pre-existing waived lines under plans/sdd.
- `RV2_OUT=... go test -overlay <scratch>/d64r2/corpus/{c,x}-{f217,ee28,new}.json -run '^TestZZRev2Corpus$' ./internal/rehydrate/ (scratch-only corpus harness; Windows and Linux)`: Base corpus: no flips across f2171654, ee280d3a and HEAD. Windows 193/81 (0 leaks, 53 over-withheld), Linux 192/82 (0 leaks, 54). John @Work root: 44 flips on Windows, 43 on Linux, all shown to withheld. a  b root: 17 flips on Windows, 16 on Linux.
- `go test -p 2 -count=1 -overlay <scratch>/d64r2/probe_{head,mut}.json -run '^TestZZRecordedPathProbe$' -v ./internal/rehydrate/ (scratch-only probe for the recordedPath rebuttal)`: HEAD: PASS (`type token.txt` withheld under root a  b). With recordedPath gated on rootSpelledExactly: FAIL, `type token.txt` shown. Gating would weaken the screen.

### Criterion changes

- No row that asserted a summary WITHHELD changed, and no golden changed.
- TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit: the admitted root a@b moves to the excluded roots, so its 6 SHOWN summaries flip to WITHHELD. John @Work and a  b (two spaces) join the excluded roots. The two spellings the admitted loop withheld (cd <root> && cat private/deny.txt and <root>2/x.txt) are now asserted withheld under every excluded root too, so a@b keeps them. The sibling check now folds each run of whitespace to one space (strings.Fields), as sanitize does.
- TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit: the control root a@b-c.d moves to the excluded roots, so its 5 SHOWN rows flip to WITHHELD (Grep and Glob previews of the root, cd <root> && go test ./..., git -C <root> status --short, a one-word Read). John @Work is added. a-b_c.d is the new control root and stays shown.
- New row TestBuild_ARootNoTextSpellsExactlyIsNeverHeld (roots with NBSP, U+3000 or a run of spaces, plus tab and trailing space on POSIX). It withholds every summary spelling the root or its one-space sibling. It redacts a reason naming the sibling's gitdir and a reason naming the sibling's rule file. Failing closed, it also redacts a reason naming the root's own .git\index, which an exactly spelled root keeps.
- Root unit: '@' leaves the set (rootUnitChars is now "-_."). A root that fails rootSpelledExactly (control character, Unicode space, run of spaces, space at either end) has no unit and is held in no drop reason. recordedPath still holds every root.
- ADR 0011 §23 items 7, 8, 9, 10 and 11, the evidence list and a new criterion-change paragraph record all of the above. security.md now lists '- _ .' and single spaces, adds '@', and says a reason holds only a root a preview spells exactly. The D64 criterion-change paragraph now reads 'kept a root of letters, digits, @, - and . shown until the round-2 verify'.

### Open issues

- `-` stays in the root unit (D64 lists it as admitted and did not ask for it to be decided), but a word of the root that starts with `-` after the root's space binds as a PowerShell parameter. Measured: `g C:\q\John -Force` sets -Force on an advanced function, with pwsh 7 and 5.1. A lone `-` (`OneDrive - Contoso`) is passed as text. Recorded in ADR §23 as open for a coordinator ruling.
- Accepted cost of finding 1: under a root no text spells exactly, a drop reason that names a path under the root is redacted (fail closed). In the corpus, roots with `@` or a run of spaces lose 44/43 and 17/16 shown summaries (Windows/Linux), all non-private.
- recordedPath keeps holding the root for every spelling, against the verifier's 'for consistency' suggestion. Probe evidence shows gating it would show `type token.txt` under root `a  b` with Read(./secrets/**). The coordinator may want to confirm.
- Hosted CI was not run. The 8.3-TEMP behaviour of the daemon rows was checked only by local emulation (QTMPDI~1, same path length as the hosted runner's TEMP).
- Still open from round 1: ratify the `+` path-start finding (b9505d53); the Windows best-fit modifier-letter limit (U+02BA becomes ", and U+02B9, U+02BC, U+02C8 become ').

### Needs owner

- Coordinator: rule on `-` at the start of a word of the root after its space (PowerShell parameter binding, e.g. a root ending in ` -Force`). Options: keep `-` (current, per D64's letter); drop it (costs every hyphenated root its unit); or refuse the unit only when `-` follows a space (a position rule; note that OneDrive's lone ` - ` is passed as text).
- Coordinator: confirm that drop reasons keep holding a root with non-whitelist characters when it is spelled exactly (TestBuild_AReasonHoldsTheRootASummaryDoesNot), and that recordedPath keeps holding every root.
- Coordinator: ratify or reverse the `+` path-start, name-start and root-unit exclusion (b9505d53), carried from round 1.
- Coordinator: rule on the Windows best-fit modifier-letter limit, carried from round 1.

## verify:rehydrate:f2: verdict `needs-fixes`, 1 finding(s)

- **minor** `internal/rehydrate/pathgate.go:1114 (rootPrefix), :1086 (cutValueWithheld calls it first), :263 (rootKey = screenText(ToSlash(Clean(root)))), :2082 (screenText); reached from pathNamedWithheld :1033 for a cut path-named JSON value`: A cut path-named JSON value can show a sibling directory outside the project. rootPrefix decides whether a cut value is 'the start of the project root's spelling', and then the value is shown with no other check. It compares in screenText form, which deletes ' " ` \ ^ and folds every run of ASCII whitespace to one space, and rootKey is built the same way. So a cut value that differs from the root only in those characters counts as the project root. Examples: C:\q\obrien\pr... under root C:\q\o'brien\proj, C:\q\ab\pr... under C:\q\a^b\proj, /q/ab/pr... under /q/a\b/proj, a"b or a`b, and C:\q\a b\pr... under C:\q\a  b\proj (two spaces) or a<TAB>b. The value is never judged by its directory or the host, so the absolute path of a directory outside the project is shown. That breaks D50 (D60(i) covers tool-pointer summaries). This is the same class as round-2 finding 1: a root matched through a folded spelling. The seat closed it for holdRoot only. So the ADR's new sentence 'A root that no sanitized text spells exactly is now held in no text, neither a summary nor a reason' (item 8 and the round-2 intro) does not cover this third recognizer. The quote, caret and backslash variants have nothing to do with whitespace, so D60(iv)'s whitespace-collapsed-preview limit does not cover them. The ADR does not document this as a limit (item 9 says only that a cut structured value is judged by the directory it spells). The bug is older than this wave: f2171654 behaves the same. It survives because D64 scoped the root fixes to the summary unit and to reasons.
  - Evidence: Scratch probe (not in the worktree): .../scratchpad/verify-d64r2/zz_vfy_probe_test.go, run with -overlay ov.json (HEAD 895d41f4) and ov_f217.json (f2171654's pathgate.go). TestZZVfyRootPrefixSibling uses cutJSONArg(t, "notebook_path", <sibling>\x.txt, len("<sibling-parent>\\pr")).
- Windows HEAD: cut value shown under roots o'brien, a^b and 'a  b'. The uncut twin {"notebook_path":"C:\\q\\obrien\\proj\\x.txt"} is withheld.
- f2171654: identical.
- Linux (static binaries in alpine:latest, uid 1000), HEAD and f2171654 alike: shown under /q/o'brien/proj, /q/a^b/proj, '/q/a  b/proj', /q/a\b/proj, /q/a`b/proj, /q/a"b/proj and /q/a<TAB>b/proj.
- Realistic previews from store.ArgsDigest of a NotebookEdit {cell_id, edit_mode, new_source:<n y's>, notebook_path:C:\q\obrien\proj\notes.ipynb} under root C:\q\o'brien\proj: every cut from ..."notebook_path":"C:\\q\\obrien… through …C:\\q\\obrien\\proj… is SHOWN. That includes the whole sibling project path C:\q\obrien\proj. Cuts past the root's length are withheld.

Everything else at HEAD checks out:
- D64(1): every listed character is out, including , = @ + # and a ':' past the drive. A root with control characters, Unicode spaces or runs of spaces has no unit.
- Drop reasons now fail closed under a root no text spells exactly.
- recordedPath keeps holding every root, and the seat's rebuttal is sound: learning only withholds more.
- D64(2): cut-at-drive-colon edges are all withheld: "a"Temp:…, ""Temp:…, x+Temp:…, 'Temp:…, -Path:Temp:…, x&&Temp:…, #Temp:…, @Temp:…, (Temp:…, Temp::…, Registry::…, Env:/…, Env:.…, C:.…
- D64(3)/(4): the ADR wording matches the rulings.
- Corpus, re-run with the seat's harness: f2171654 and HEAD are identical. Windows 193 shown / 81 withheld (0 leaks, 53/246 over-withheld), Linux 192/82 (0 leaks, 54/246), 0 flips.
- Checks:
  - go vet windows/linux/darwin PASS
  - devtool lint golangci-lint PASS
  - fmt-check PASS
  - test/docs PASS
  - lint docmarkers,runpatterns PASS
  - internal/rehydrate/... PASS
  - The 80 daemon rehydrate rows: 79 PASS, 1 platform SKIP. Every name in the pattern exists.
  - The 7 D64 rehydrate rows -count=20: 140/140. Daemon row: 20/20.
  - -race -count=3: 21/21 and 3/3, no DATA RACE.
- The 7 commits conform: subjects <=62 characters, Refs: V6-VERIFY, C4.6 footer, no trailers. The tree is clean.
  - Fix: Make rootPrefix compare against the root's exact characters. Normalise separators (on Windows only, '\\' to '/' and runs collapsed) and fold case where the platform folds (paths.Key). Delete no quote, caret, backtick or backslash, and fold no whitespace. Also return false when !j.rootExact, so a cut value under a root no preview spells exactly falls through to the directory judgement and fails closed, as reasons now do. Add a red-first row: a cut NotebookEdit or Read preview spelling C:\q\obrien\pr… under root C:\q\o'brien\proj, and the a^b/ab pair, both withheld. On POSIX add a\b, a"b, a`b and the a<TAB>b/'a  b' sibling. A cut <root>\pr… under a plain root and under John Smith stays shown. Correct the ADR item 8 and item 9 sentences to cover cut structured values.

