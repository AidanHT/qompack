# Wave 19c rehydrate: the D61 screen

Branch `closeout/w19-rehydrate`. Workflow `wf_558a3480-754`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:rehydrate: status `done`, head `69860c1dab114a664626610c09596adfa2e7cb3a`

### Root cause

Rounds 1 and 2 tried to recover every path inside arbitrary text (shell commands, nested shells, escapes, JSON) by enumerating readings and host-judging the pieces. That cannot be complete: each round closed some spellings, opened others, and spent per-word host evaluations, including the Windows fail-closed 8.3 refusal of HEAD~1. Separately, checkpointer drop reasons, such as the verbatim gitErr, were never gated.

### Summary

PRODUCT CODE CHANGED

# Wave 19c, rehydrate: D61 is implemented on closeout/w19-rehydrate

The branch is now at 69860c1d, four commits on top of 1dd7b00d. Nothing was pushed, and the worktree is clean.

## The gate (internal/rehydrate/pathgate.go, rewritten)

The checkpoint's ToolPointer does not carry a tool name, so a summary's shape decides how it is judged.

### Structured summaries (D61(1))
- **What counts as structured:** a summary of one word that is not a URL, counted after the project root's own spelling is held together. A path-named argument of a canonical-JSON preview is also structured: path, file_path, notebook_path, paths, file, dir, cwd and similar names, including array elements.
- **How it is judged:** whole, the way a file pointer is. That means containment plus `Refuses`, once per build and memoized. It is also withheld if it is an absolute, home or variable path outside the project in any form (`file://`, `D:x`), or a glob that selects a known withheld path.
- **Rooted plain words** are judged only that way. Read, Write and Edit take an absolute file_path, so `<root>/README.md` is shown even though `private/README.md` is withheld.
- **Relative words** (Glob or Grep arguments) and words glued from commands (`cat<x`, `a=b`, `path:x`) are also screened as free text.

### Free text (D61(2))
Free text makes no host calls. It is read in three forms: as recorded, as each decoded JSON string, and percent-decoded. Control characters are dropped and whitespace is collapsed the way the store does it. A free-text summary is withheld when:
- **(a)** with `'` `"` `` ` `` `\` `^` removed and case folded on Windows and macOS, it contains a rule's screen literal. The literal is the last segment of the rule's pattern; if that has glob syntax, the nearest all-literal segment before it; failing that, the longest literal run of the last segment. Patterns come from the new `RuleSet.ReadRulePatterns`.
- **(b)** it contains the basename or relative path of a path this build withholds. Only file pointers, path-keyed drops and structured summaries add to that list. Anchors that name no file (`~`, `D:`, `$HOME`) never do.
- **(c)** it contains an absolute path outside the project: drive, drive-relative, UNC, `file://` (unless the URL names the project's own path), POSIX with two or more segments, home or variable rooted, root followed by `..` that leaves it, or a relative `..` that leaves the project.
- recall's `path:` selector names a path outside the project or selects a withheld path. Round 2's lexical selector rule is kept, with no host call.
- **(d)** the host's rules are unavailable. Every free text is also withheld when one rule's literal cannot tell texts apart: a rule with no literal (`Read`, `./**`, `~/**`), or a rule anchored outside the project whose literal is part of the project's own path.

**Truncated summaries:** a summary cut with `…` is withheld if it ends, at a word or segment boundary, in a prefix of a literal, a withheld path or a rule specifier. The prefix must be at least `minCutPrefix` = 3 bytes. Summaries that the rendering would cut are judged too.

**Root protection:** the tolerant root regex accepts either slash, doubled separators, any case where paths fold, quotes and escapes inside the spelling, and the MSYS, WSL and `\\?\` forms of the drive on Windows. So `cd <root> && go test ./...` and `git -C <root> status` are shown. A spelling glued to a name character reads as a sibling. A spelling that runs into more words inside one quoted argument, or through an escaped space, also names a sibling and is withheld.

### Section 7 and dropped() (D61(3))
- **Redaction:** each drop reason is screened for out-of-project absolute paths and for withheld paths named as a whole word or segment. It is redacted as an error chain: the parts before the one showing the path are kept, that part becomes `(path withheld)`, and later parts without separators (the system's own message) are kept.
- **Not redacted:** elimination and open_question entries (the model's own text, D60(i)) and section 6's own pointer drops.
- **Path-rule drops** now name a matched pointer only if section 6 may show it.
- **Where the fix lives:** the redaction runs where rehydrate reads the entries, so checkpoints written earlier are covered. internal/checkpoint is unchanged.

### Cost and hostperm (D61(4))
- **Adapter:** the daemon adapter is back to `RuleSet.Evaluate` and also hands the build the rule patterns.
- **hostperm:** byte-identical to a357d187 plus one read-only accessor, `ReadRulePatterns` (patterns.go). The Evaluator and its rows are reverted.
- **Measured cost:** each file pointer, path-keyed drop and structured summary is judged exactly once; free text is never judged. In the real-adapter row, each variant (Bash, canonical-JSON/URL, absolute-root) costs exactly the fixture's 10 file pointers plus 10 structured summaries = 20 judgements. On 1dd7b00d the same 80 free-text previews alone cost 3,145, 1,967 and 1,058 judgements.

## Deviation from "hostperm revert only", with evidence
D61(2)(a) needs each rule's pattern, but a357d187's `RuleSet` only exposes `Empty` and `Evaluate`. The only alternative was re-reading the settings sources outside hostperm, which would duplicate its source discovery. So I added one accessor. It reads no file and evaluates nothing, and has its own row (`TestRuleSet_ReadRulePatternsListsEveryReadRule`).

## Red-first evidence

On 1dd7b00d, each of these failed:
- **(1)** nested shells: `powershell -Command`, `pwsh -c`, `bash -c`, `wsl -e`, plus escapes inside them.
- **(2)** `cp ./private/my secret.txt backup/` was shown.
- **(3)** under `./private/**`, `go vet main.go` and `grep -rn config src/` were withheld.
- **(4)** in the real-adapter usefulness row, `git diff HEAD~1` and `git log --oneline HEAD~3..HEAD` were withheld on Windows.
- **(5)** the cost row: 3,145 / 1,967 / 1,058 judgements against 0 required.
- **(6)** a real `ValidatePointers` git error from a linked worktree leaked the outside gitdir.
- **(7)** `file:///etc/passwd` and `D:secret.txt` were shown.
- **(8)** `…cat private/den…` was shown.

One caveat on (4): `cd <root> && …` and `git -C <root> status` were already shown on 1dd7b00d in both my plain-root and spaced-root fixtures. They are pinned as shown, but I could not reproduce the verifier's "withheld in every project".

Two rows came out of this round's own testing:
- **Reason boundary:** `TestBuild_NeverExceedsTheHostCeiling` found that a withheld structured `/W` redacted every drop reason containing a "w", which broke the `OVERFLOW:` entries. Reasons now match withheld names at word boundaries. `TestBuild_AShortWithheldNameNeverRedactsAnUnrelatedReason` pins it, and is red under a mutation that removes the boundary.
- **Anchor guard:** `TestBuild_AWithheldAnchorPoisonsNoFreeText` pins it, and is red under a mutation that removes the guard.

## Rows
- Every round 1/2 row that asserts a spelling is withheld is unchanged and green.
- **New rehydrate rows:** NestedShells, ADotRelative, ADirectoryRuleNeverPoisons, FileURLsAndDriveRelative, ATruncatedSummary, ADropReason, UsefulSummariesUnderUAT12, ARuleOverTheWholeProject, ScreenLiteral, FreeTextAsksTheHostNothing, AOneWordArgumentIsJudgedByItsShape, AShortWithheldName, AWithheldAnchor.
- **New daemon rows:** HandsTheBuildTheRulePatterns, HostJudgementsAreStructuredSummariesAndFilePointers (Bash, JSON/URL and absolute-path variants), UsefulSummariesAreShownUnderTheUAT12Rules. The last one runs the full usefulness list in a root with a space.

## Docs
- **ADR 0011 §23:** the header and intro are updated. Item 2 has D61(5)'s limits, plus the D60(i) section-7 qualifier for drop entries that carry model text. Items 5 to 10 replace the round-2 text, the D60 citations now point at D60(c), and the evidence list is updated.
- **docs/security.md:** the paragraph is rewritten, replacing "a drop entry that names a path".

## Checks
- `go vet` passes for GOOS windows, linux and darwin.
- golangci-lint is clean on rehydrate, daemon and hostperm.
- fmt-check passes.
- `test/docs` passes, docmarkers and runpatterns pass, and gen-config/command/mcp-docs `--check` are up to date.
- Full packages on Windows: rehydrate, hostperm, and daemon (587 s).
- Every new or changed row (45) passed `-count=20` and `-race -count=3` on Windows, each run by its exact name.
- The host-ceiling property passed with `-rapid.checks=1500`.
- The intermediate commits 4de25450 and b6fc365d were checked from `git archive`: they build and pass.
- Linux container, non-root `qompack-test`, `-p 2`, run cx-w19c-rh-touched-69860c1-20261003T210509Z: rehydrate 281 pass, hostperm 139 pass, daemon 1685 pass and 1 skip.
- Linux race: all 45 rows passed `-race -count=3`, each by exact name.
- I started the container (it was stopped), and stopped it again after confirming no other process was running in it. Artifacts are in scratchpad/w19c-rh-linux/.

### Commits

- 4de25450 feat(hostperm): list the Read rules' path specifiers
- b6fc365d fix(rehydrate): judge summaries by shape and screen free text
- 66ff127d revert(hostperm): drop the round-2 request evaluator
- 69860c1d docs(adr): record D61's structured summaries and screen

### Tests

- `go vet ./internal/rehydrate/ ./internal/daemon/ ./internal/hostperm/ (GOOS=windows, linux and darwin)`: pass
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/rehydrate/... ./internal/daemon/... ./internal/hostperm/...`: pass (no findings)
- `go run ./tools/devtool fmt-check`: pass
- `go test -p 2 -count=1 ./test/docs; go run ./tools/devtool lint --only=docmarkers,runpatterns; go run ./tools/devtool gen-config-docs --check (and gen-command-docs, gen-mcp-docs)`: pass; PASS runpatterns, PASS docmarkers; all three generated docs up to date
- `go test -p 2 -count=1 ./internal/rehydrate/ ./internal/hostperm/ (Windows)`: ok rehydrate, ok hostperm
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows)`: ok 586.9s
- `for t in <32 rehydrate rows in summary_screen_test.go, summary_privacy_test.go, pointer_privacy_test.go>; do go test -p 2 -count=20 -run "^$t\$" ./internal/rehydrate/; done (same loop for the 12 rows in internal/daemon/rehydrate_hostpaths_test.go, and for TestRuleSet_ReadRulePatternsListsEveryReadRule in ./internal/hostperm/)`: all 45 ok
- `the same per-row loops with -race -count=3 (Windows)`: all 45 ok
- `go test -p 2 -count=1 -run '^TestBuild_NeverExceedsTheHostCeiling$' ./internal/rehydrate/ -rapid.checks=1500`: ok
- `go test -p 2 -count=20 -run '^TestBuild_AWithheldAnchorPoisonsNoFreeText$' ./internal/rehydrate/ (also -race -count=3)`: ok
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w19c-rh --out <scratch>/w19c-rh-linux HEAD touched --no-race --gomaxprocs 2 -- ./internal/rehydrate ./internal/hostperm ./internal/daemon`: PASS: rehydrate 281, hostperm 139, daemon 1685 (1 skip), non-root qompack-test, commit 69860c1d
- `Linux, same clone as qompack-test, CGO_ENABLED=1: for each of the 45 rows: go test -race -count=3 -run "^$t\$" <its package>`: 45 ok, race_fail=0
- `Round-2-API copies of the daemon usefulness and cost rows, run on a git-archive extraction of 1dd7b00d`: fail as expected: git diff HEAD~1 and git log HEAD~3..HEAD withheld on Windows; 3145 / 1967 / 1058 judgements where 0 is required
- `go test -run for the 6 new rehydrate rows (one per verifier finding 1, 2, 3, 6, 7, 8), run on 1dd7b00d before the fix`: all fail (red-first)
- `git archive of 4de25450 and of b6fc365d: go build ./...; go vet; go test ./internal/hostperm/ (and ./internal/rehydrate/ plus daemon rows for b6fc365d)`: pass

### Criterion changes

- TestBuild_ArgumentSummariesFailClosedWithoutHostRules: while the host's rules are unavailable, a free-text summary that names no path ({"query":"LUPINE-7731"}) is now required to be withheld (D61(2)(d): without rule literals no free text can be screened).
- TestBuild_AKnownWithheldPathIsFoundAnywhereInAText: {"query":"not my secret.txt.bak at all"} moves from shown to withheld (D61 accepts over-withholding of a text that merely mentions a withheld name or literal).
- Test helpers denyFiles and denyPrivate now return HostRules carrying the rule patterns they stand in for (Read(./<file>), Read(./private/**)); inline HostPaths fixtures are adapted to HostRules. Assertions are unchanged.
- Removed round-2 evaluator cost rows: TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords, TestRehydrateHostPaths_APieceThroughALinkIsJudgedOnDisk, TestRehydrateHostPaths_EveryPreviewShapeIsJudgedWithoutTheDisk, and hostperm TestEvaluator_*. The replacement is TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers, which requires exactly files + structured judgements. Its elapsed < compactAnswerBudget assertion is not carried over (the hard rule bans wall-clock pass/fail); the time is now only logged.
- TestRehydrateHostPaths_RefusesWhatTheHostDenies and TestRehydrateHostPaths_UnreadableRulesFailClosed: mechanical change to read .Refuses from the new HostRules. Assertions unchanged.

### Open issues

- ToolPointer carries no tool name, so a multi-word Grep or Glob preview's path part is screened as free text rather than host-judged. Aliases and links in it are not resolved; this is covered by the recorded limit.
- Recorded limit carried from round 2: an unquoted, unescaped sibling of the root named '<root last segment> <more>' (e.g. 'proj - Copy') in free text or a Read preview reads as the root followed by a word, and is shown.
- Drop reasons are screened for out-of-project absolute paths and for withheld paths. They are not screened for rule literals, so a scan error naming an unrecorded, rule-refused in-project path would show it (recorded in ADR 0011 §23.2).
- Very short rule literals or withheld basenames (one or two characters, e.g. a rule //c/q/** gives the literal 'q' when the project is not under it) withhold most free text. D61 accepts this over-withholding, and it is recorded.
- The verifier's claim that 'cd <root> && cmd' and 'git -C <root> status' are 'withheld in every project' did not reproduce on 1dd7b00d in my plain-root or spaced-root fixtures. Both are now pinned as shown through the real adapter.
- macOS: only go vet was run (GOOS=darwin). No macOS test execution and no hosted CI run for this wave.
- The scratchpad is shared across seats. I built scratchpad/mutate.exe for mutation checks; if a file of that name already existed, it was overwritten.

### Needs owner

- Deviation from 'internal/hostperm (revert only)': one read-only accessor was added, RuleSet.ReadRulePatterns (internal/hostperm/patterns.go). D61(2)(a) needs rule patterns, a357d187's RuleSet exposes none, and re-reading the settings sources outside hostperm would duplicate its source discovery. It evaluates nothing and reads no file. Needs coordinator acceptance.
- minCutPrefix = 3 (internal/rehydrate/pathgate.go): D61's 'length >= 3' for a truncated summary's tail, applied in bytes. That is a superset of the rune count, so it withholds at least as much.
- Cost fixture constants in internal/daemon/rehydrate_hostpaths_test.go: rehydrateCostFiles = 10 and rehydrateCostReads = 10, so a build that judged nothing could not meet the bound by accident. maxCostJudgements = their sum (20) is the exact required count: each file pointer and structured summary is judged once, free text never.
- D61 said the daemon cost row 'stays, re-derived'. It is re-derived under a new name (TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers) because the old name ('linear in their words') no longer describes it. ADR 0011 §23 evidence records the old names.

## review:rehydrate:privacy:r1: verdict `needs-fixes`, 10 finding(s)

- **major** `internal/rehydrate/pathgate.go:974-994 (outsideIn, pathStartAfter); docs/adr/0011-rehydration-budget-and-item-order.md:790`: Free text can show an absolute path outside the project when the path is glued to a short flag (`-X/abs/path`). outsideIn only starts a path at the start of the text or after one of ` |&;<>(){}[],=:@`. When a flag letter comes right before the path, the path is never read. homeOrVarPath already allows a `-letters` prefix for `~`, but the drive and POSIX branches do not. A drive path glued to a flag is caught only by accident: the start after its `:` sees `\a\b`, which has two segments, so a one-segment drive path such as `-oD:\stash` is missed. D61(2)(c) requires these paths to be withheld, and no recorded limit covers the flag-glued form.
  - Evidence: Overlay probe (rehydrate package, UAT-12 rules, root C:\q\proj). All of these are SHOWN: `git -C/home/u/other status`, `gcc -I/opt/homebrew/include -L/opt/homebrew/lib x.c`, `go build -o/usr/local/bin/x .`, `ssh -i/home/u/keys/id_rsa host`, `tar -xf/home/u/backup.tar`, `grep -f/etc/passwd x`, `docker run -v/home/u/data:/data img`, `7z x -oD:\stash a.zip`. The same command through the real daemon adapter, `git -C/home/u/other status`, is also SHOWN. For comparison, `gcc -IC:\Users\x\inc a.c` is withheld, but only through the `:` start.
  - Fix: In outsideIn, also treat i as a path start when the text from the previous pathStartAfter byte (or the start of the text) up to i is `-` followed by letters (`-[A-Za-z]+`), and p[i] is a separator or p[i:i+2] is a drive spelling. This mirrors homeOrVarPath's `-[A-Za-z]*` prefix. Add red-first rows for each spelling above, and correct ADR item 7(c)'s sentence on where a path may start.
- **major** `internal/rehydrate/pathgate.go:295-298 (homeOrVarPath); ADR 0011 line 787`: Free text shows PowerShell's environment-variable paths, the idiomatic form on Windows. homeOrVarPath knows `$VAR`, `${VAR}` and `%VAR%` followed by a separator. It does not know `$env:NAME\…`, which outsideIn also misses because the token after the `:` starts with a letter. It also misses `%A%%B%\…` chains and cmd's `!VAR!`. The structured branch does withhold `$env:APPDATA\x` (homeOrVarRoot matches `$env`), so a Read's preview and a PowerShell command naming the same file disagree. ADR item 7(c) claims variable-rooted paths are withheld, and `$HOME/.aws/x` is, so the 'names assembled at run time' limit does not cover this.
  - Evidence: Overlay probe: SHOWN are `Get-Content $env:USERPROFILE\.aws\credentials`, `Get-Content "$env:USERPROFILE\.ssh\id_rsa"`, `type $env:APPDATA\Claude\settings.json`, `type %HOMEDRIVE%%HOMEPATH%\notes\x.txt` and `type !USERPROFILE!\notes\x.txt`. The real daemon adapter also SHOWS `Get-Content $env:USERPROFILE\.aws\credentials`. Withheld are `cat $XDG_CONFIG_HOME/x/y`, `cat "$HOME"/notes/x.txt`, and the structured `$env:APPDATA\x`.
  - Fix: Extend homeOrVarPath's variable alternative with `\$env:[A-Za-z_][A-Za-z0-9_]*`, `\$\{env:[^}]+\}`, `![A-Za-z_][A-Za-z0-9_]*!` and repeated `(%[A-Za-z_][A-Za-z0-9_()]*%)+`, each followed by `[\\/]`. Add matching cases to homeOrVarRoot so the two branches agree. Add rows for the spellings above.
- **major** `internal/rehydrate/pathgate.go:338-346 (classify, JSON branch) vs 352-357`: A path-named JSON argument whose value is not exactly one path is judged only as one whole path and is never screened, so it can show a denied file name verbatim. Examples are a space- or comma-separated list in a string, or a path with a locator such as `#L10` or `:10`. The non-JSON branch screens a relative one-word value as free text too, and only a rooted plain word skips the screen. The JSON branch skips the screen for every path-named value. So `private/deny.txt#L10` as a lone Glob argument is withheld, but `{"file":"private/deny.txt#L10"}` is shown. D61(1) assumes the value is 'the preview of a path argument', and a value that holds more than one path does not meet that.
  - Evidence: Real daemon adapter, project rules [Read(./private/deny.txt), Read(./.env), Read(./secrets/**)]. SHOWN: `{"paths":"private/deny.txt src/main.go"}` and `{"file":"private/deny.txt#L10"}`. With the fake exact-file rules (the Linux/macOS case, where hostperm cuts no stream suffix), `{"file":"private/deny.txt:10"}`, `{"path":"private/deny.txt,src/a.go"}`, `{"files":"src/a.go;private/deny.txt"}`, `{"path":"cat private/deny.txt"}` and `{"path":".env.local .env"}` are all SHOWN. On Windows the real adapter withholds `:10` only because hostperm strips the stream.
  - Fix: In classify's JSON branch, add every path-named value to texts as well, unless it is a rooted plain word. Use the same test as lines 353-355 (absLike, no notPlainPathRune, at most a drive colon), factored into one helper both branches call. Add red-first rows for the spellings above, including one through the real adapter on Linux for `:10`.
- **minor** `internal/rehydrate/pathgate.go:348-357 (classify); ADR 0011 lines 680-684 and 749-760`: A Read, Write or Edit preview whose path has a space below the project root counts as several words, so it is free text only and the host never judges it. A link or an 8.3 name in such a path is therefore not resolved. ADR item 6 says a Read's file_path is judged whole, and limit 1 says structured summaries are judged by the host's rules, 'which resolve aliases and links'. Neither is true for these previews.
  - Evidence: Real daemon adapter on Windows, rule Read(./private/**), with two junctions `mydocs` and `my docs` pointing at private/. The Read preview `<root>\mydocs\x1.txt` is withheld by hostperm. The Read preview `<root>\my docs\x2.txt` (same target directory) is SHOWN. Each was built in isolation so that no known basename interferes.
  - Fix: Preferred: when a summary is absLike and still holds whitespace after protect, also judge the whole summary as a value (one host judgement, inside D61(4)'s 'structured summaries cost one each'), in addition to the free-text screen. Otherwise, record the limit in §23.2 and narrow item 6's wording to say a Read path with a space below the root is screened as free text, and links and 8.3 names in it are not resolved.
- **minor** `internal/rehydrate/pathgate.go:457-499 (truncatedWithheld, endsWithPrefixOf, cutBoundary); 639-667 (screenLiteral)`: Item (8) still leaks for glob rules. A literal taken from the literal run of a glob segment that a `*` or `?` precedes (`.env` from `**/*.env`, `.pem` from `*.pem`, `ecret` from `[sS]ecret*`) can begin in the middle of a name. endsWithPrefixOf, however, requires a word or segment boundary before the prefix. So a preview the store cut inside `prod.env` or `server.pem` still shows the cut prefix of the denied path.
  - Evidence: Overlay probe with rules [**/*.env, *.pem, ./private/deny.txt]. SHOWN: `<90 a's> cat config/prod.en…` and `<90 a's> openssl x509 -in certs/server.pe…`. Withheld: `… cat config/.en…` and `… cat private/den…`.
  - Fix: Make screenLiteral also report whether the literal is unanchored at its start (it came from a run with `*` or `?` before it). In truncatedWithheld, match an unanchored literal's prefix of minCutPrefix bytes or more at the end of the tail with no boundary requirement. Add rows for the two spellings above.
- **minor** `internal/rehydrate/pathgate.go:1091-1097 (jsonShaped), 338-346`: Any summary that starts with `{"` or `["` and ends in `…` is parsed as cut JSON, and only its quoted strings are screened. A free-text preview of that shape that the store cut (a command, or a pattern value beginning `{"`/`["`) therefore has its unquoted part skipped entirely. D61(2) names the raw text as one of the forms to screen.
  - Evidence: Overlay probe, SHOWN: `{"x":1} && cat private/deny.txt && echo aaaa…(cut)`, `["$x" ] && cat /etc/passwd && echo aaaa…(cut)`, and `{"a":"b"}; cat C:\Users\x\notes.txt aaaa…(cut)`.
  - Fix: In jsonShaped's cut branch, accept the summary as JSON only if every byte outside the scanned strings is JSON grammar (`{}[],:`, whitespace, number characters, true/false/null). Otherwise classify it as free text, or screen the raw sanitized text as well.
- **minor** `internal/rehydrate/pathgate.go:701-740 (screenText, stripQuotes), 1052-1086 (rootRelativeInside, relativeEscapes)`: The ADR says the screen undoes escapes, but some escape forms that split a name or a `..` are not undone. A line continuation inside a word becomes, after the store collapses the newline, an escape character followed by a space (`de\ ny`, `de^ ny`, a PowerShell backtick before a space). Removing the escape and collapsing whitespace leaves `de ny`, which no longer spells the literal. Bash's `.\.` is `..`, but outsideIn keeps the backslash as a separator and reads it as `././`.
  - Evidence: Overlay probe, SHOWN: `cat private/de\ ny.txt`, `type private\de^ ny.txt`, `cat .e\ nv`, ``Get-Content private/de` ny.txt``, `cat .\./outside/x.txt`, and `<root>/.\./x` (same mechanism through rootRelativeInside). Withheld: `type .^.\outside\x.txt` and `cat \.\./outside/x.txt`.
  - Fix: Add one more normalized form in which an escape character (`\` `^` or backtick) followed by a space is removed together with the space, and screen it beside the current forms. For outsideIn, also read a form in which a backslash before a non-separator character is dropped (POSIX escape reading), so `.\.` reads as `..`. Alternatively, record these as a named limit.
- **minor** `internal/rehydrate/pathgate.go:515 (pathSelector)`: recall's `path:` selector is only recognized at the start of the text or after whitespace or `(`. In a command, a quote or `=` usually comes right before it, and then a partial selector that selects a known withheld path is shown. The same selector in the MCP JSON form is withheld. ADR item 7 claims selectors are judged.
  - Evidence: Overlay probe, known withheld file private/deny.txt under Read(./private/deny.txt). Withheld: `{"query":"path:deny"}` and `qompack recall path:deny`. SHOWN: `qompack recall "path:deny"`, `qompack recall 'path:deny'` and `qompack recall --query=path:deny`.
  - Fix: Widen the prefix class to `(?:^|[\s("'=])` so the selector is found after a quote or `=`. Add rows for the three CLI spellings.
- **nit** `docs/adr/0011-rehydration-budget-and-item-order.md:680-688 (§23.2 limits)`: These are leaks inherent to D61's text screen, but the recorded limits do not name them. (1) A name relative to a directory an earlier command changed to: Bash's working directory persists, so after `cd secrets`, a later `cat token.txt` is shown under Read(./secrets/**) unless a file pointer recorded the file. cmd's `cd..` is not read as `..` either. (2) Unicode normalization on macOS: an NFD spelling of an NFC rule literal opens the same file on APFS but does not match, because paths.Key folds case only.
  - Evidence: Overlay probe: under Read(./secrets/**) with no pointer, `cat id_rsa` and `cd.. && type x.txt` are SHOWN. paths.KeyFold is strings.ToLower only (internal/paths/norm.go:195-200), and no NFC step exists in rehydrate or hostperm.
  - Fix: Add both to §23.2's limits: names relative to a directory an earlier command changed to, and Unicode normalization variants (macOS NFD) under 'aliases'.
- **nit** `internal/rehydrate/pathgate.go:639-667 (screenLiteral)`: screenLiteral skips `..` segments but does not resolve them, while hostperm's cleanLiteral does (rules.go:243). For a specifier with `..` after its first segment, the literal can name a segment that the refused paths do not contain.
  - Evidence: `Read(./private/deny.txt/..)` makes hostperm refuse private/** (literal pattern, cleaned). screenLiteral returns `deny.txt`, so `cat private/other.txt` is screened only for `deny.txt` and is shown.
  - Fix: Resolve `..` lexically in the specifier (as cleanLiteral does) before choosing the literal segment. Add a TestScreenLiteral case.

## review:rehydrate:usefulness:r1: verdict `needs-fixes`, 9 finding(s)

- **major** `internal/rehydrate/pathgate.go:119 (screenBy); ADR 0011 §23 item 7(d)`: The check meant to catch 'a rule over the whole project' is a plain substring test, `anchored && strings.Contains(j.rootKey, lit)`. It does not respect segment boundaries and ignores what comes after the literal. So any rule anchored outside the project whose literal happens to appear in the root's path sets screenAll, and every free-text summary in the project is withheld. This happens even when the rule cannot refuse a single project path, including a rule from the Claude Code docs' own examples, used with this owner's own layout (projects under C:\Users\Quant\Documents).
  - Evidence: Usefulness corpus: 94 realistic previews (87 name no denied or outside path), each run through Build with the real hostperm policy (rules in a temp home or project settings) at HEAD, 1dd7b00d and a357d187. Shown/withheld counts:
- No rules: HEAD 84/10, r2 81/13, base 81/13.
- UAT-12 rules, plain root: 78/16, 72/22, 77/17.
- UAT-12 rules, spaced root: 78/16, 72/22, 64/30.
- User rules [Read(~/.ssh/**), Read(~/.aws/**), Read(~/.kube/config), Read(**/*.pem)], plain root: 80/14, 76/18, 80/14.
- Same user rules, root '<tmp>\s4\config-service': HEAD 8/86, r2 76/18, base 80/14. The diagnosis is 'screenAll' for `git status`, `git diff HEAD~1`, `npm test`, `cd "<root>" && go test ./...` and every other free text. Only the 8 rooted Read/Edit/Write previews survive.
- Read(//etc/**) with root '...\fetcher': HEAD 8/86, base 80/14.
- Read(~/Documents/*.pdf) with root '<tmp>\Documents\Programming\Projects\proj': HEAD withholds `git status`, `go test ./...`, `npm test` and `{"k":5,"query":"retry backoff"}` (screenAll). The same rule with root '<tmp>\Code\proj' shows all of them, and a357d187 shows all of them.
- None of these rules refuses a project path: the README.md Read preview is shown in the same builds.
  - Fix: Make the test segment-aware and depth-aware. Set screenAll only when (1) the literal equals a whole '/'-segment of rootKey, and (2) the specifier's segments after that literal are all `**` (or the literal is the root's last segment with nothing after it), meaning the rule refuses a subtree that can contain the root. Otherwise screen by the literal as for any other rule, and also by the last segment's literal run (`.pdf` for `*.pdf`) when that segment is a partial glob. Alternative: probe the host once per build with Refuses(<root>/<fixed probe name>), and record that one extra judgement in the cost bound. Add rows for Read(~/Documents/*.pdf) with a root under Documents, Read(~/.kube/config) with a root containing 'config', and Read(//etc/**) with a root containing 'etc', each showing `git status`. Also correct ADR item 7(d), which presents the substring test as 'a rule … which may refuse every project path'.
- **major** `internal/rehydrate/pathgate.go:1155 (jsonStrings strips the ellipsis), :150-157 (newPathJudge notes structured values), :377-393 (valueWithheld); ADR 0011 §23 item 7(b) line ~781`: When the store cuts a canonical-JSON preview inside a path-named argument, jsonStrings strips the `…`. The fragment is then judged as a complete path. If the cut falls inside the root's spelling, the in-project path is judged outside the project and the summary is withheld. Worse, whenever the fragment is withheld (outside, or refused by the host), newPathJudge note()s it, and its cut basename (1-3 letters such as 's', 'jo' or 'to') becomes a withheld name. Every free-text summary in the same build that contains those letters is then withheld. This breaks D61(2)(b) ('never fragments') and the ADR's claim that 'no free text poisons another'. NotebookEdit (notebook_path sorts after new_source) and MCP tools with file/dir/relative_path arguments after long content are cut this way routinely.
  - Evidence: Probe P1 (no rules): `{"cell_id":"c1","new_source":"xxxxxxxxxxxxxxx","notebook_path":"C:/Users/Quant/AppData/Local/Temp/rvq…/p1/Jo…` is withheld as 'value-outside'. In the same build, `{"k":5,"query":"join the major tables"}` is withheld as 'known:jo'. It is shown without the NotebookEdit pointer, and at a357d187.
Probe P9 (Read(./secrets/**)): a notebook_path cut at `…/proj/secrets/to…` is refused by the host and noted as 'to'. Then `go test ./internal/auto/...`, `npm run tox` and `{"k":5,"query":"photo upload"}` are all withheld ('known:to'). All three are shown at a357d187.
The corpus NotebookEdit entry (an in-project notebook) is withheld in every scenario, and adds 'known:s'.
  - Fix: Have jsonStrings mark the string that runs into the cut (it ends at len(s) and had the ellipsis) as cut. For a cut value: (1) never note() it, since it is a fragment; (2) judge containment by prefix, so a cut absolute value that is a prefix of the root's spelling, or that starts with the root, is inside; (3) withhold it only when the host refuses the fragment's directory part, or when truncatedWithheld matches (prefix of a literal, a withheld path or a specifier). Add rows: a cut notebook_path inside the root is shown and poisons nothing; a cut withheld one adds no name, with `go test ./internal/auto/...` shown in the same build.
- **major** `internal/rehydrate/pathgate.go:432-436 (textWithheld knownText substring match), with :161-182 (note)`: The free-text screen matches withheld basenames as raw substrings, with no boundary. Withheld names include the basenames of out-of-project file pointers and structured Read previews. So a single out-of-project Read in a project with no deny rules at all withholds unrelated in-project summaries for the whole build. 'out.txt' withholds 'layout.txt' and 'stdout.txt'; 'config' withholds 'tsconfig.json'. Round 2 used word boundaries (namesKnownIn) and showed all of these. The implementer added boundaries for drop reasons (TestBuild_AShortWithheldNameNeverRedactsAnUnrelatedReason exists because a structured '/W' broke reasons) but left the free-text screen unbounded. The same '/W' summary still withholds every free text containing 'w'.
  - Evidence: Poison scenario (no rules). One build holds three out-of-project Read previews: `C:\Users\Quant\go\pkg\mod\github.com\spf13\cobra@v1.8.0\README.md`, `<tmp>\scratch\out.txt` and `C:\Users\Quant\.ssh\config`. All 7 in-project free texts are then withheld:
- 'known:readme.md': `git diff README.md`.
- 'known:out.txt': `cat layout.txt`, `go test ./... | tee stdout.txt`.
- 'known:config': `git config user.email`, `cat tsconfig.json`, `go test ./internal/config/...`, `{"k":5,"query":"how is config loaded"}`.
Without the reads, all 7 are shown. At 1dd7b00d and a357d187 all 7 are shown even with the reads.
  - Fix: Match knownText only where the name starts at a boundary: the start of the text, or a byte that is not a name byte. Do this on both forms truncatedWithheld already builds (escapes removed, and backslashes read as '/'), so `private\deny.txt` and `my\ secret.txt` still match. Keep the trailing side unbounded so that the D61 criterion change for `my secret.txt.bak` stands. A name glued to a preceding name character is a different file; names assembled at run time are already a recorded limit. Optionally, let the owner decide whether (b) should note out-of-project paths at all: (c) already withholds every spelling that reaches them within one command. Add a row with outside Reads of README.md and out.txt, where `cat layout.txt` and `cat tsconfig.json` are shown.
- **major** `internal/rehydrate/pathgate.go:295-298 (homeOrVarPath)`: Privacy regression against 1dd7b00d and a357d187. homeOrVarPath recognizes `$VAR/`, `${VAR}/` and `%VAR%\` but not PowerShell's `$env:NAME\`. So a home path in the target shell's own syntax is shown when no rule literal happens to catch it. Separately, a variable followed by a space or an operator (`cd $HOME && cat .ssh/id_rsa`, `cd %USERPROFILE% && type .aws\credentials`) is now shown, while the equivalent `cd ~ && cat .ssh/id_rsa` is withheld. These are out-of-project paths (D50 containment) that both earlier commits withheld. No existing row covers them, so 'every WITHHELD row unchanged' did not catch it.
  - Evidence: Probe P4 (no rules). Shown at HEAD:
- `Get-Content $env:USERPROFILE\.ssh\id_rsa`
- `Get-Content "$env:USERPROFILE\.aws\credentials"`
- `cd $HOME && cat .ssh/id_rsa`
- `cd %USERPROFILE% && type .aws\credentials`
Withheld at HEAD: `cd ~ && cat .ssh/id_rsa`, `cat $HOME/.ssh/id_rsa`, `type %USERPROFILE%\.ssh\id_rsa`.
At 1dd7b00d and a357d187 all 7 are withheld: summaryHomeOrVar matched any `$NAME`.
  - Fix: Add `\$env:[A-Za-z_][A-Za-z0-9_()]*` and `\$\{env:[^}]+\}` followed by a separator to homeOrVarPath. Also treat a home variable (`$HOME`, `${HOME}`, `%USERPROFILE%`, `%HOMEPATH%`, `$env:USERPROFILE`, `$env:HOME`) that ends a word before whitespace or a shell operator the way `~` is treated. That keeps a generic `$F` a recorded limit while closing the cd-to-home spelling. Add these four spellings to TestBuild_PointersNeverShowAHomeOrVariablePath.
- **minor** `internal/rehydrate/pathgate.go:1006-1010 (tokenOutside: any token starting with two separators is UNC)`: Any token that starts with `//` or `\\` is treated as a UNC path, even with no host and share after it. Searches for Go/JS/C comments and directives are therefore withheld everywhere. These are among the most common Grep/Bash previews in a Go codebase like this one. A bare `//` or `//nolint` names no readable location. D61(c) lists UNC shares, which need `\\server\share`. The behaviour is pre-existing at a357d187, but D61's usefulness goal makes it worth fixing.
  - Evidence: Probe P10 (rule Read(./private/deny.txt)). Withheld at HEAD as 'text-outside':
- `grep -rn "// TODO" internal/`
- `rg -n "//nolint" internal/`
- the Grep preview `<root>\internal //go:build`
- `// Deprecated:`
- a Task prompt `List every // TODO comment in internal/ …`
  - Fix: Treat a double-separator token as UNC or network only when it has a host and at least one more segment (`//host/share`, `\\host\share`), mirroring the POSIX two-segment rule; keep the `\\?\` and `\\.\` device prefixes as outside. Add a usefulness row with `grep -rn "// TODO" internal/` and `rg -n "//nolint" internal/`.
- **minor** `internal/rehydrate/pathgate.go:1011-1012 (POSIX two-segment rule)`: `/dev/null` (and `/dev/stderr`, `/dev/stdout`, `/dev/tty`) counts as an out-of-project absolute path. So the very common `2>/dev/null` and `>/dev/null` idioms withhold otherwise ordinary commands. These device paths reveal no file content. Pre-existing at a357d187.
  - Evidence: Corpus, every scenario, withheld as 'text-outside' at HEAD: `ls -la src/ 2>/dev/null || true`, `which go node python3 2>/dev/null`, `go build ./... >/dev/null && echo ok`.
  - Fix: Exempt a fixed list of device paths: /dev/null, /dev/stdin, /dev/stdout, /dev/stderr, /dev/tty, /dev/zero, /dev/random, /dev/urandom, /dev/fd/N. Record the exemption in ADR 0011 §23 item 7(c) and add it to the usefulness row.
- **minor** `internal/rehydrate/pathgate.go:916-922 (rootFollowedBy)`: The root followed by `:` (not sentence punctuation) counts as a name continuation. So a Docker bind mount of the project, `-v "<root>:/src"`, reads as an outside drive path and the command is withheld. Pre-existing at a357d187.
  - Evidence: Corpus and P6, withheld as 'text-outside' in every scenario, plain and spaced roots, both slashes:
- `docker run --rm -v "<root>:/src" -w /src golang:1.23 go test ./...`
- `docker run --rm -v "<rootfwd>:/app" node:20 npm test`
  - Fix: In rootFollowedBy, return followedByPath when `:` is followed by a separator, a quote or the end. A Windows file name cannot hold `:`, and on POSIX `proj:/x` as a sibling name is negligible next to the bind-mount idiom. Add the docker row to the usefulness tests.
- **nit** `internal/rehydrate/pathgate.go:150-157 with :357 (one-word relative values are host-judged and noted)`: A one-word relative summary is host-judged. On Windows, hostperm's fail-closed 8.3 refusal turns an 8.3-shaped word into a 'withheld path', and note() adds it to the withheld names. Every free text in the build that mentions it is then withheld, which reopens round 2's HEAD~1 finding in a narrow form (it needs a one-word summary such as a Grep for HEAD~1).
  - Evidence: Probe P5 (UAT-12 rules, Windows): one-word `HEAD~1` is withheld as 'value-host', and `git diff HEAD~1` in the same build is withheld as 'known:head~1'. Same for `PROGRA~1` and `dir PROGRA~1`. All four are shown at a357d187.
  - Fix: Do not note() a one-word relative value. It is already screened as free text itself, and it is a Glob or Grep argument rather than a recorded path. Alternatively, note it only when it holds a separator or is rooted.
- **nit** `internal/rehydrate/pathgate.go:426-430 (rule-literal substring screen); ADR 0011 §23 item 2 limits`: For the record (D61 accepts this): rule literals withhold common summaries that name no refused path. A literal from a rule anchored outside the project, such as `config` from Read(~/.kube/config), withholds every mention of config. Yet (c) already withholds every spelling that reaches such a rule's paths, except variable-relative ones. The context7 MCP's library IDs are POSIX two-segment strings and are withheld under (c).
  - Evidence: Corpus, withheld at HEAD and naming no refused or outside path:
- UAT-12, 'literal:.env': `node -e "console.log(process.env.NODE_ENV)"`, and the Grep preview `process.env.API_KEY`.
- User rules, 'literal:config': `git config user.email`, `cat tsconfig.json`, `go test ./internal/config/... -run TestLoad`, Task `{"description":"Check config loading",…}`.
- All scenarios, 'text-outside': `{"context7CompatibleLibraryID":"/vercel/next.js","topic":"routing"}` and `/golang/go`.
- `git log @{u}..` ('text-outside', a relative `..`).
With the UAT-12 rules every deny or outside entry is withheld (7/7), where a357d187 missed `{"query":"path:private/deny.txt"}`.
  - Fix: No code change is required by D61. Consider asking the owner whether rules anchored outside the project should contribute literals at all, given that (c) covers their paths; at minimum, list `process.env` and `config`-type cases in ADR §23 item 2 as examples of the accepted over-withholding.

## review:rehydrate:cost:r1: verdict `needs-fixes`, 7 finding(s)

- **major** `internal/rehydrate/pathgate.go:338-346 (classify, JSON branch); also :353-356 (rooted one-word values)`: Regression from round 2. Every value of a path-named JSON argument is classed as a structured value and is never screened as free text, whatever it holds. A value that holds a denied path plus more text (several paths, a :line or #L suffix, a glob) goes only to the host as one whole path, which refuses nothing, so section 6 shows it. The seat's own rule for non-JSON summaries screens a relative word, a multi-word summary, or a word with non-path characters as text as well. JSON values skip that check. Being named `file` instead of `target` therefore makes an argument less protected.
  - Evidence: Real adapter on Windows under UAT-12 rules [Read(./private/deny.txt), Read(./.env), Read(./secrets/**)], scratch probe TestZZRealAdapterLeaks at HEAD 69860c1d. These are shown, and the payload contains 'deny.txt': {"files":"src/main.go private/deny.txt"}, {"paths":["**/deny.txt"]}, {"relative_path":"private/deny.txt#L4"}, and the Read preview <root>\private\deny.txt#L4. {"target":"private/deny.txt:10"} is withheld. {"file":"private/deny.txt:10"} is withheld on Windows only because hostperm reads :10 as an NTFS stream; with the denyFiles fixture, i.e. Linux semantics, it is shown. The one-word Glob summary `**/deny.txt` is withheld, but the same glob as a JSON `paths` element is shown. On 1dd7b00d, {"files":"src/main.go private/deny.txt"} was withheld (scratch TestZZOld), so this is a regression.
  - Fix: In classify's JSON branch, apply the one-word test classify already uses to each path-named value. It stays value-only only when it is a rooted plain path once the root is held together. Otherwise (relative, contains whitespace or a comma, a ':' past the drive, a glob) append it to texts as well, as relative one-word summaries already are. For rooted values, also judge the part before the first '#', '@' or ':' past the drive with withheld(), or drop '#' and '@' from notPlainPathRune's plain set. Add red-first rows for the four spellings above, through the real adapter.
- **minor** `internal/rehydrate/pathgate.go:974-994 (outsideIn, pathStartAfter)`: D61(2)(c) gap. A POSIX absolute path glued to a short flag is never read as a path start, so an out-of-project absolute path is shown. The home-path regex allows the same flag prefix (`-[A-Za-z]*`, so `-i~/.ssh/key` is withheld), but outsideIn starts a path only after whitespace or a delimiter. Drive paths are caught because ':' is a start character; POSIX roots are not.
  - Evidence: Scratch TestZZProbe3 at HEAD, all shown: `ssh -i/home/me/.ssh/id_rsa host`, `gcc -I/usr/include/openssl a.c`, `tar -C/etc/ssl -xf a.tar`, `docker run -v/srv/data:/data img`. `ssh -i ~/.ssh/id_rsa host` and `-i~/.ssh/key` are withheld. These spellings were also shown on 1dd7b00d, so this predates D61, but D61(2)(c) requires POSIX absolute paths of two or more segments to be withheld.
  - Fix: In outsideIn, also start a token at a '/' or '\' that follows `-[A-Za-z]+` at a word start, mirroring homeOrVarPath's flag prefix. Add the four spellings above as withheld rows, plus `cmd /c dir` and `go test ./...` as shown rows.
- **minor** `internal/rehydrate/pathgate.go:1091-1097 (jsonShaped) with :338-346 and :398-412`: D61(2) names three forms to screen: the raw text, its decoded JSON strings, and its percent-decoded form. For a JSON-shaped summary only the decoded strings are screened. The cut heuristic (starts with {" or [" and ends in …) treats a cut command as JSON, so everything outside quotes is never screened. That is a regression from 1dd7b00d.
  - Evidence: `{"a":1} && cat private/deny.txt && echo xxx…` (cut at 120 bytes) is shown at HEAD through the real adapter (TestZZRealAdapterLeaks); classify returns texts=["a"]. `["a"] && cat private/deny.txt …` behaves the same (TestZZProbe2). Both were withheld on 1dd7b00d (TestZZOld). Realism is low: the command must begin with {" or [" and be cut.
  - Fix: For JSON-shaped summaries, also screen the raw text with the path-named values' spans blanked. At minimum, when !json.Valid (the cut heuristic), screen the whole raw text as free text in addition to the decoded strings.
- **minor** `internal/rehydrate/pathgate.go:1279-1340 (reasonWithheld, namesKnownIn, redactReason)`: The reason screen matches withheld paths by bare basename, so an allowed path in a reason that shares a basename with a withheld one is redacted. The redaction protects nothing, because the entry's ID still names the path, but it deletes the restore clause. For path_rule drops it deletes the whole reason, even though buildRestoredInstructions now guarantees the matched pointer is one section 6 shows. D61(3) asks only that no reason show 'a withheld path'.
  - Evidence: Scratch TestZZReasons: ./private/** rule, file pointers private/README.md (withheld) and README.md (allowed), and a withheld out-of-project Read preview C:\Users\me\.claude\CLAUDE.md. knownText=[readme.md private/readme.md claude.md]. The path_rule `.claude/rules/docs.md` reason 'matched README.md; did not fit the rehydration budget; restore: Read .claude/rules/docs.md' becomes '(path withheld)'. The nested_claude_md `src/CLAUDE.md` reason becomes 'did not fit the rehydration budget; restore: (path withheld)' while its ID still shows src/CLAUDE.md. Reading the user's ~/.claude/CLAUDE.md is common, and nested CLAUDE.md files are exactly what item 6a restores.
  - Fix: Screen reasons by each withheld path's relative path and absolute spellings, not by bare basename (no checkpointer or rehydrate reason names a withheld path by basename alone). Or skip a match whose enclosing path segment run equals a path withheld() allows. In redactReason, replace only the clause that names the path instead of everything before the next ': '. Add a row where an allowed README.md and CLAUDE.md keep their restore clauses beside a withheld namesake.
- **minor** `internal/rehydrate/pathgate.go (whole file)`: The code is not simpler than round 2, which the brief asked to check. The hostperm risk is gone, and the cost bound holds. But the rehydrate gate grew, and it still carries shell-reading machinery from the round-2 approach that D61 abandoned: three shell quote readers (posixQuoteOpen, powershellQuoteOpen, cmd), sibling-of-root detection through escaped spaces, the recall selector grammar and a hand-written JSON tokenizer. newPathRules is used only by newPathJudge, despite its 'enough for withheld()' comment.
  - Evidence: Non-comment, non-blank code lines in pathgate.go: a357d187 82, 1dd7b00d 645, HEAD 982. Branch operators (if/case/for/&&/||): 22, 186, 357. Functions: 6, 41, 62. Round-2 non-test additions over a357d187 (pathgate plus hostperm evaluator/alias/policy/resolve) were 1,365 lines; HEAD's are 1,334 (pathgate 1,283 plus patterns.go 51). Lines are flat overall, and branches went from about 280 to 366.
  - Fix: Not a blocker. Candidate cuts: when the root is followed by a space, treat it as a sibling whenever any quote character precedes it in the text (conservative), and drop the three quote readers. Fold newPathRules into newPathJudge. Reuse classify's one-word test for JSON values (finding 1) rather than keeping a separate path.
- **nit** `internal/rehydrate/pathgate.go:997-1015 (tokenOutside) and ADR 0011 §23 item 7(c)`: Two kinds of over-withholding the ADR does not state. (1) The ADR says 'an http(s) or other URL is not a path', but stretches inside a URL after '=' or ':' are scanned, so a query value of the form X:y is read as a drive-relative path. (2) A summary the store cut inside its second spelling of the project root (any command with two in-project absolute paths and a long root) is read as a drive path outside the project and withheld.
  - Evidence: Withheld at HEAD: `curl https://example.com/?q=a:b`, {"url":"https://example.com/docs/v3/guide.html?q=a:b"} (20 of my 80 canonical-JSON/URL previews), `grep -n "s[i:j]" main.go`, `sed -i s:foo:bar: main.go`, {"ratio":"a:b"}. In my 80 absolute-path previews, all 20 `cat <root>\src\main.go <root>\pkg\xN.go` previews are cut inside the second root spelling ('…\pr…') and withheld.
  - Fix: Once a token is known to be an http(s) URL, skip to its end in outsideIn. Treat a cut final token that is a case-folded prefix of the root's cleaned spelling as the root. Or record both cases as accepted over-withholding in §23 item 2.
- **nit** `docs/adr/0011-rehydration-budget-and-item-order.md §23 item 2 (Deliberate limits)`: Shell brace expansion is not listed among the limits, and the screen does not see it.
  - Evidence: `cat private/{deny,other}.txt` is shown under Read(./private/deny.txt) at HEAD (TestZZProbe, 0 host calls).
  - Fix: Add brace expansion (`{a,b}`) next to globs and regular expressions in the 'Aliases and globs typed in free text are not resolved' bullet.

## fix:rehydrate:r1: status `done`, head `9f40a6fc2e6663b946acaf405fbab0060c42c845`

### Root cause

D61's first implementation read only some spellings: paths started only after delimiters; variables only as `$VAR/`, `${VAR}/` and `%VAR%\`; JSON path values were never screened; the escape-joined form was missing; and a cut preview starting like JSON was trusted as JSON. It also withheld summaries that name nothing: anchored rules were tested by substring against the root; withheld names were matched anywhere inside words; cut fragments were judged as whole paths and noted as names; `//word` was read as UNC, `/dev/null` and `<root>:` as outside paths; and reasons were screened by bare basename.

### Summary

PRODUCT CODE CHANGED

# Wave 19c rehydrate, fix seat round 1: every review finding is resolved

The branch closeout/w19-rehydrate is now at 9f40a6fc, three commits on top of 69860c1d. Nothing was pushed and the worktree is clean. The only product file changed is internal/rehydrate/pathgate.go. internal/hostperm, internal/checkpoint and the daemon's product code are unchanged in this round.

## Review resolution
Every finding was checked on its own. 19 rows were written first and each failed on 69860c1d's gate for the reason the review gave (`git stash` of pathgate.go, run by exact name). They pass on the fix. The 3 daemon rows go through the real adapter.

### Fixed
- **F1 and F17, absolute path glued to a short option.**
  - outsideIn now also starts a path at a separator, the root's mark or a drive when `-` and letters stand right before it at the start of a word (flagGlued, flagBefore).
  - protect accepts the root glued to a flag, so `gcc -I<root>/include` is shown.
  - Row: TestBuild_AnAbsolutePathGluedToAFlagIsWithheld, covering every spelling the review listed plus `tar -C/etc/ssl`. `cmd /c dir`, `go test ./...` and `ls -la src/` stay shown.
- **F2 and F12, environment-variable paths.**
  - The variable forms now cover `$env:NAME`, `${env:NAME}`, `!NAME!` and chains such as `%A%%B%`, each followed by a separator.
  - A home-directory variable alone at the end of a word is treated like `~`: HOME, USERPROFILE, HOMEPATH, APPDATA, LOCALAPPDATA, ONEDRIVE and XDG_*_HOME, in any of those spellings. So `cd $HOME && cat .ssh/id_rsa` is withheld.
  - homeOrVarRoot gained `!NAME!` so structured values agree with free text.
  - Row: TestBuild_AnEnvironmentVariablePathIsWithheld. `echo $PATH`, `go test $PKG` and `echo $HOMEPAGE is set` stay shown.
- **F3 and F16, path-named JSON values that hold more than a path.**
  - Such a value is judged by the host and is also screened as free text, unless it is a rooted plain path (plainRooted, shared with the one-word case).
  - `#` and `@` are no longer part of a plain path.
  - Rows: TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened, using the fake exact-file rules (the Linux behaviour), and TestRehydrateHostPaths_APathArgumentHoldingMoreThanAPathIsWithheld through the real adapter, including `:10` and the Read preview `<root>/private/deny.txt#L4`.
- **F4, rooted Read preview with a space below the root.** I took the preferred fix.
  - A summary that starts with the root's mark followed by a separator and still holds whitespace is also judged whole by the host. That is one judgement per such summary.
  - Rows: TestBuild_ARootedPathWithASpaceIsJudgedByTheHost, a fake that refuses through `my docs` and asserts exactly one judgement and none for `<root> TODO`. TestRehydrateHostPaths_ARootedPathWithASpaceIsJudgedByTheHost uses real junctions or symlinks made with makeDirLink.
- **F5, cut prefix for glob literals.**
  - screenLiteral now reports a "mid" literal, one whose run has `*`, `?` or a class before it. Its prefix counts at the end of a cut with no boundary (midScreens).
  - Row: TestBuild_ATruncatedSummaryNeverShowsTheCutPrefixOfAGlobRulesName.
- **F6 and F18, cut text that starts like JSON.**
  - jsonStrings checks the grammar outside strings: `{}[],:`, whitespace, number characters, and the letters of true, false and null.
  - A cut preview that fails the check is one free text, with its strings beside it.
  - Row: TestBuild_ACutCommandThatStartsLikeJSONIsScreened. A cut canonical-JSON prompt and a JSON value cut inside a number stay shown.
- **F7, escapes the screen did not undo.**
  - A screen form now removes an escape character together with the space after it (`de\ ny`, `de^ ny`, a backtick).
  - outsideIn also reads `.\.` as `..`.
  - Row: TestBuild_AnEscapedLineBreakNeverSplitsADeniedName.
- **F8, selector after a quote or `=`.** The prefix class is now `(?:^|[\s("'=])`. Row: TestBuild_APathSelectorAfterAQuoteOrEqualsIsJudged.
- **F9, the screenAll substring test.** I replaced it with rootCover:
  - The rule's segments (ruleSegments, `..` resolved) are matched against the root's segments. path.Match handles glob segments, `**` spans segments, and a drive matches its `c` spelling.
  - The match runs from the filesystem root for `//` and drive rules, and at every alignment for home and settings anchors, which Build does not know.
  - Every free text is withheld only when the rule ends, or ends in a glob with no literal part, at or above the root.
  - When the rule reaches project paths only through what follows the root, that part's literal is screened instead (`.key` for `~/Documents/**/*.key`).
  - Row: TestBuild_ARuleAnchoredOutsideTheProjectWithholdsOnlyWhatItCovers. It shows `git status` and the rest for `~/Documents/*.pdf`, `~/.kube/config` beside `config-service`, and `//etc/**` beside `fetcher`. `~/config/**` with the root under it still withholds everything. The existing ARuleOverTheWholeProject row is unchanged and green.
- **F10, a cut path argument read as a whole path.**
  - jsonStrings marks the string the cut falls inside.
  - A cut value that is a prefix of the root's spelling (rootPrefix) is the project. Otherwise the directory it spells whole is judged, one host judgement.
  - A cut value is never noted as a withheld name.
  - Row: TestBuild_ACutPathArgumentIsAFragmentNotAPath, covering both of the review's probes with the poisoning texts shown.
- **F11, withheld names matched inside other words.**
  - knownText now matches only where a name starts (namedAt), in all three screen forms. What follows the name is still not judged.
  - Row: TestBuild_AWithheldNameIsMatchedOnlyWhereANameStarts. `layout.txt`, `stdout.txt` and `tsconfig.json` are shown. `cat out.txt`, `git diff README.md`, `cat ./config` and `cat out.txt.bak` stay withheld.
- **F13, comment markers read as UNC shares.**
  - A token that starts with two separators is outside only for a host plus a share, or for the `\\?\` and `\\.\` device forms.
  - Row: TestBuild_ACommentMarkerIsNotAUNCShare.
- **F14, device paths.**
  - /dev/null, stdin, stdout, stderr, tty, zero, random, urandom and /dev/fd/N are exempt. Any other /dev path is still judged.
  - Row: TestBuild_ADevicePathIsNotOutsideTheProject.
- **F15, Docker bind mount of the project.**
  - The root followed by `:` and a separator, a quote or the end is the root (rootFollowedBy, rootRelativeInside).
  - Row: TestBuild_ADockerBindMountOfTheProjectIsShown. `<root>2:/src` and `/home/u/data:/data` stay withheld.
- **F19, drop reasons.**
  - Reasons are screened by the whole spelling of each withheld path (knownPaths, matched as a path of its own by pathRunStart), not by its basename.
  - Only the "; " clause that shows the path is redacted (redactClauses).
  - Row: TestBuild_AReasonKeepsItsRestoreBesideAWithheldNamesake.
- **Nits.**
  - N2: screenLiteral resolves `..` lexically, and `~name` without a slash is not a home anchor. Row: TestScreenLiteral_ResolvesDotDotAsTheHostDoes.
  - N3: a one-word relative value with no separator is not noted (recordedPath). Row: TestBuild_AOneWordRevisionPoisonsNoFreeText.
  - N5, part 2: a cut stretch that starts the root's spelling is the root (cutRoot). Row: TestBuild_ACutInsideASecondSpellingOfTheRootIsShown.
  - N1, N4 and N6 are recorded in ADR §23.2's limits.

### Partly fixed, with reasons
- **F20, simplicity.** newPathRules is folded into newPathJudge, and the JSON and one-word branches share plainRooted.
  - I kept the three quote readers. Replacing them with "any quote before a root followed by a space means sibling" would withhold everyday texts such as `echo "done" && cd <root> && make`.
  - The code grew: pathgate.go went from 982 to 1,229 non-comment code lines and from 62 to 80 functions.
- **N5, part 1, URL query stretches (`?q=a:b`).** I did not skip URLs. Skipping them would show a local absolute path carried inside an http URL's query. The over-withholding is recorded as accepted in §23.2.

### Fixture defect found by the Linux run
On Linux, the first version of the APathNamedArgument row spelled a Read path as `/q/proj\src\main.go`. On Linux that is a real path outside the project, so withholding it was correct. 9f40a6fc spells the path per platform; the assertions are unchanged. As a result d5487f74 alone fails that one row on Linux.

## Cost
- Free text still costs no host judgement.
- A structured summary costs one judgement. That now includes a summary that starts at the root and has a space below it, and a cut value, which is judged by its directory.
- The daemon cost row stays exactly at the fixture's 20 judgements, because none of its previews take those shapes.

## Docs
- **ADR 0011 §23:**
  - "What changed" names the review.
  - Item 2's limits add cd-relative names and cmd's `cd..`, Unicode normalization variants, brace expansion, examples of the accepted over-withholding, and the whole-spelling rule for reasons.
  - Items 6 to 10 are rewritten for the fixed screen. The evidence list adds the rows and the criterion change.
- **docs/security.md:** updated to match.

## Checks
All passed:
- Vet for GOOS windows, linux and darwin, on rehydrate and daemon.
- golangci-lint on rehydrate and daemon.
- fmt-check.
- test/docs, docmarkers, runpatterns, and gen-config/command/mcp-docs `--check`.
- Full packages on Windows: rehydrate, and daemon in 352 s.
- Each of the 22 new or changed rows, by exact name, with `-count=20` and with `-race -count=3` on Windows.
- The host-ceiling property with `-rapid.checks=1500`.
- **Linux**, non-root qompack-test, `-p 2`, run cx-w19c-rhf-touched2-9f40a6f-20261003T230300Z at commit 9f40a6fc:
  - rehydrate 306 pass.
  - daemon 1,688 pass, 1 skip.
  - The 22 rows with `-race -count=3`: race_fail=0.
- **Earlier Linux run at a403de16 failed** two tests:
  - The fixture defect above.
  - TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing, an intermittent failure outside my files (see open issues).
- **Container:** it was stopped when I began. I started it, confirmed only its sleep process was running at the end, and stopped it again.

Artifacts are in scratchpad/w19c-rh-r1fix/.

### Commits

- d5487f74 fix(rehydrate): close the w19c review's screen gaps
- a403de16 docs(adr): record the w19c review's summary-screen fixes
- 9f40a6fc test(rehydrate): spell the edge row's Read path per platform

### Tests

- `GOOS=windows|linux|darwin go vet ./internal/rehydrate/ ./internal/daemon/`: pass
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/rehydrate/... ./internal/daemon/...`: pass (no findings)
- `go run ./tools/devtool fmt-check`: pass
- `go test -p 2 -count=1 ./test/docs; go run ./tools/devtool lint --only=docmarkers,runpatterns; go run ./tools/devtool gen-config-docs --check (and gen-command-docs, gen-mcp-docs)`: pass; PASS runpatterns, PASS docmarkers; generated docs up to date
- `go test -p 2 -count=1 ./internal/rehydrate/ (Windows)`: ok
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows)`: ok 351.5s
- `for t in <19 rehydrate rows in summary_screen_edges_test.go + TestBuild_AWithheldAnchorPoisonsNoFreeText>; do go test -p 2 -count=20 -run "^$t\$" ./internal/rehydrate/; done; same for TestRehydrateHostPaths_APathArgumentHoldingMoreThanAPathIsWithheld, TestRehydrateHostPaths_ARootedPathWithASpaceIsJudgedByTheHost, TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules in ./internal/daemon/ (Windows)`: all 22 ok
- `the same 22 per-row loops with -race -count=3 (Windows)`: all 22 ok
- `go test -p 2 -count=1 -run '^TestBuild_NeverExceedsTheHostCeiling$' ./internal/rehydrate/ -rapid.checks=1500`: ok 127s
- `git stash push internal/rehydrate/pathgate.go; per-row go test -run "^$t\$" for the 19 rehydrate rows and 3 daemon rows; git stash pop`: all 21 new rows red on 69860c1d's gate (TestBuild_AWithheldAnchorPoisonsNoFreeText passes there, as its changed shown text is unaffected)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w19c-rhf --out <scratch>/w19c-rh-r1fix/linux HEAD touched2 --no-race --gomaxprocs 2 -- ./internal/rehydrate ./internal/daemon`: PASS at 9f40a6fc, non-root qompack-test: rehydrate 306, daemon 1688 (1 skip)
- `Linux container, run clone cx-w19c-rhf-touched2-9f40a6f, CGO_ENABLED=1, as qompack-test: go test -p 2 -race -count=3 -run "^$t\$" <pkg> for each of the 22 rows`: 22 ok, race_fail=0
- `earlier Linux gate at a403de16 (cx-w19c-rhf-touched-a403de1-20261003T224915Z)`: FAIL: TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened (Windows-only fixture spelling, fixed in 9f40a6fc) and TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing (intermittent, outside my files; 30/30 in isolation)

### Criterion changes

- TestBuild_AWithheldAnchorPoisonsNoFreeText: the shown text `echo $HOME is set` is replaced by `echo $HOMEPAGE is set`. A home directory's variable alone at the end of a word is now that directory, as `~` alone is (D61(2)(c); review F12's `cd $HOME && cat .ssh/id_rsa`), so it is withheld for what it says. `$HOMEPAGE` holds `$home` where a name starts, so the row still checks that the anchor `$HOME` poisons nothing.
- D61(2)(b) matching narrowed per review F11: a withheld basename or relative path withholds a free text only where a name starts (not after a name byte); what follows the name is still not judged. Every earlier withheld row stays green.
- Drop-reason screen (D61(3)) per review F19: reasons are screened by a withheld path's whole spelling (relative in the project, as written outside it) and for out-of-project absolute paths, no longer by bare basename. Redaction replaces only the '; ' clause that shows the path. Every earlier reason row is unchanged and green.
- TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened (a new row this round): the shown Read preview's spelling was corrected in 9f40a6fc to the platform's own spelling of <root>/src/main.go. The Windows-only form is a path outside the project on Linux. Assertions unchanged.

### Open issues

- TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing (internal/daemon/daemon_test.go:1613, outside my files) failed once in the Linux whole-package run at a403de16 (calls=0) and passed 30/30 in isolation and in the rerun at 9f40a6fc. Mechanism: the ObservePrompt seam runs on a goroutine (startPromptRecording), and dispatchOp's reply wait is bounded by promptReplyBudget, measured on the real clock from req.TS. Under load the deadline wins and the test checks calls before the seam ran. This is a wall-clock-dependent pass/fail; it needs an injected clock or a seam-completion channel, from whichever seat owns daemon_test.go.
- d5487f74 alone fails TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened on Linux (Windows-only fixture spelling); 9f40a6fc fixes it. A Linux bisect should treat d5487f74 as known-red for that row.
- Carried: ToolPointer carries no tool name. A multi-word relative Grep or Glob preview is screened as free text only, so aliases and links in it are not resolved; this is a recorded limit. A summary that starts at the root with a space below it is now host-judged.
- Carried, recorded limit: an unquoted, unescaped sibling of the root named '<root last segment> <more>' (`proj - Copy`) in free text or a Read preview reads as the root followed by a word and is shown.
- Carried: drop reasons are not screened for rule literals. This round they are also no longer screened for bare basenames (recorded in ADR 0011 §23.2).
- Carried: very short rule literals or withheld names (one or two characters) withhold most free text. Accepted by D61 and recorded.
- Carried: the round-2 verifier's claim that 'cd <root> && cmd' and 'git -C <root> status' were withheld in every project did not reproduce on 1dd7b00d. Both remain pinned as shown through the real adapter.
- Accepted over-withholding, recorded in §23.2 rather than fixed: a stretch inside a URL query read as drive-relative (`?q=a:b`; skipping URLs would show local paths carried in a URL query); cmd switches glued into a POSIX path (`dir /s/b`); two-segment library ids and container paths (`/vercel/next.js`, `-v <root>:/app/data`); literal mentions such as `process.env`; and, on Windows, a summary below the root whose words hostperm refuses as an unresolvable 8.3 name (a Grep preview `<root>\src HEAD~1`).
- F20: pathgate.go grew to 1,229 non-comment code lines and 80 functions (from 982 and 62). The three quote readers were kept on purpose.
- macOS: only go vet ran (GOOS=darwin). No macOS test execution and no hosted CI run for this round.
- Shared scratchpad: early in this session I wrote files with common names (msg1.txt, msg2.txt, edit1.py, edit2.py, rewrap.py, json_old.txt, json_new.txt, cpu.out, reh.test.exe and similar) at the scratchpad root. If another seat had files of those names, they were overwritten. All my files are now under scratchpad/w19c-rh-r1fix/.

### Needs owner

- Carried from the previous round: the deviation from 'internal/hostperm (revert only)' is still the one read-only accessor RuleSet.ReadRulePatterns (internal/hostperm/patterns.go). It is unchanged this round and needs coordinator acceptance.
- Carried: minCutPrefix = 3 bytes (internal/rehydrate/pathgate.go), D61's 'length >= 3'. It now also applies, with no boundary required, to literals taken from a glob run after `*`, `?` or a class (midScreens).
- Carried: the cost fixture constants rehydrateCostFiles = 10, rehydrateCostReads = 10 and maxCostJudgements = 20, unchanged. Note that the per-build cost now also counts one judgement for a summary that starts at the root with a space below it, and one for a cut structured value (its directory). The fixture has neither.
- New fixed lists in pathgate.go for owner acceptance. (1) homeVarName = HOME, USERPROFILE, HOMEPATH, APPDATA, LOCALAPPDATA, ONEDRIVE, XDG_*_HOME: the variables that, alone at the end of a word, are treated like `~` (review F12). Any other variable alone stays the recorded limit. (2) devicePaths = /dev/null, /dev/stdin, /dev/stdout, /dev/stderr, /dev/tty, /dev/zero, /dev/random, /dev/urandom, plus /dev/fd/N: exempt from 'POSIX absolute path of two segments' (review F14). (3) jsonGrammar: the bytes allowed outside strings for a cut preview to be read as JSON (review F6).
- Owner option raised by review F11: D61(2)(b) says the basename of 'any path this build withholds', which includes out-of-project paths. So one out-of-project Read of a module-cache README.md or ~/.ssh/config still withholds `git diff README.md` and `git config user.email` in the same build, even with the new where-a-name-starts match. I kept D61 as written. Noting outside basenames buys protection only for cross-command spellings such as `cd ~/.ssh` in one command and `cat config` in a later one; (c) already withholds every same-command spelling. The owner may rule that (b) notes only in-project withheld paths.

## review:rehydrate:cost:r2: verdict `needs-fixes`, 4 finding(s)

- **major** `internal/rehydrate/pathgate.go:479-481 (classify, the `below` case added by d5487f74 for F4), with :226-231 and :241-243 (newPathJudge notes a refused value; recordedPath)`: The F4 fix treats any summary that starts at the project root, has a separator after it, and contains a space as a structured value. That includes a Bash or PowerShell command that runs a project script by absolute path, and a Grep preview of path plus pattern. Three things follow. (1) The whole command string goes to hostperm as one path, so this free text costs one host judgement per summary. That breaks D61(4) ('free text costs zero host evaluations'), ADR §23 item 10 ('never for free text') and the adapter comment ('whatever its commands and queries say'). (2) On Windows, under any Read rule, hostperm refuses a word such as HEAD~1 as an unresolvable 8.3 name. Such commands are withheld again, which is the round-2 verifier's finding (4) that D61 required fixed. (3) newPathJudge then notes the refused command as a withheld path (recordedPath accepts it because it is absLike and not cut). Its 'basename' and 'relative path' are command fragments ('src head~1', 'tools/lint.ps1 --since head~2'), and they poison other free text in the same build. That breaks D61(2)(b) ('never fragments, spans or substrings of other summaries'). §23.2 records only the Grep-preview over-withholding. It does not record the poisoning of other summaries. Nothing catches any of this: the daemon cost row's absolute-path variant starts every command with cd, git or diff, and no usefulness row has a root-started command.
  - Evidence: I ran probes through the real adapter (rehydrateHostPaths over mcpOpHostPolicy) with the UAT-12 deny rules on Windows, in scratch git-archive copies. Each set was 80 previews.
- At HEAD 9f40a6fc:
  - `<root>\scripts\runN.sh --fast -n N && echo ok`: refuses=80.
  - `<root>\tools\lint.ps1 --since HEAD~N`: refuses=80, and every shown line is withheld (41/41).
  - Grep preview `<root>\src TODO N`: refuses=80.
  - Grep preview `<root>\src HEAD~N`: refuses=80, withheld 41/41.
  - Plain and 'John Smith' roots behave the same.
- At 69860c1d the same four sets cost refuses=0 and show everything.
- Poisoning probe, one build: Grep `<root>\src HEAD~1`, `git diff src HEAD~1`, `<root>\tools\lint.ps1 --since HEAD~2`, `pwsh tools/lint.ps1 --since HEAD~2 -Fix` and `git diff HEAD~1`. At HEAD the first four are withheld; only `git diff HEAD~1` is shown. At 69860c1d all five are shown, with refuses=0.
- With the same fixture, adding this shape to TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers would count 20+80, so the row would be red at HEAD.
  - Fix: (a) Never note a multi-word value. Make recordedPath also require that the value holds no whitespace once protect holds the root together (reuse plainRooted). A refused root-started summary is then withheld itself but adds no name to knownText or knownPaths. (b) Hand the host only the stretch from the root through the last word that holds a separator. That gives `<root>\my docs\x2.txt` for the F4 link case and `<root>\tools\lint.ps1` for `<root>\tools\lint.ps1 --since HEAD~1`, so command arguments and Grep patterns never reach hostperm. Rule literals in the final name stay covered by the screen. (c) Add to the daemon cost row an absolute-path variant whose commands start at the root. Assert judgements = files + reads + (one per rooted multi-word summary), or + 0 if the coordinator wants D61(4) literally, in which case drop the host judgement for this shape and record the link/8.3 limit instead. Add to the UAT-12 usefulness row, through the real adapter on Windows: `<root>\tools\lint.ps1 --since HEAD~1` shown, and `git diff src HEAD~1` shown beside a Grep preview `<root>\src HEAD~1`. Correct ADR item 10 and the rehydrate_service.go comment to say what this shape costs.
- **minor** `internal/rehydrate/pathgate.go:463-470 (classify, JSON branch: each array element of a path-named argument is its own value); pathgate.go:66-69 (HostPaths doc); internal/daemon/rehydrate_service.go:761-765; docs/adr/0011-rehydration-budget-and-item-order.md item 10 (~line 908)`: D61(4) bounds host evaluations at structured summaries plus file pointers, and the ADR and adapter say 'once for each ... structured summary'. In fact a path-named JSON array costs one host judgement per element, so one summary can cost up to about 27 (the 120-byte cap at 4 bytes per element). A real tool takes this shape: the filesystem MCP server's read_multiple_files `{"paths":[...]}`. The daemon cost row has no array variant, so the stated bound is untested and false for this shape. The bound is still finite, so this is not round 1's 16 s.
  - Evidence: Real adapter at HEAD, UAT-12 rules:
- 80 previews of `{"paths":["s/a1.go",...,"s/f1.go"]}` (6 values each) cost refuses=480, against a bound of 80.
- 80 previews of 20 short values each (`{"paths":["a1",...,"t1"]}`, 111 bytes) cost refuses=1,458, and the build took 1.11 s wall time.
- For comparison, 80 single Read previews cost 80 judgements in 42-48 ms.
  - Fix: Choose one of two fixes and record it. (1) Restate the bound as one judgement per path-named value: at most floor(120/4) per summary, plus file pointers and path-keyed drops. Fix the HostPaths doc, the adapter comment and ADR item 10 to match, and add an array variant to the cost row that asserts the per-value count. (2) To keep D61(4) literal, judge a path-named argument with more than one element as free text only, with zero host judgements, and record it in §23.2 as the same limit as multi-word relative Grep or Glob previews: links and 8.3 names in it are not resolved.
- **minor** `internal/rehydrate/pathgate.go (whole file)`: The brief asked to check that the code is simpler than round 2. It is not. hostperm's added surface did shrink (security-critical +339 lines down to +36, byte-identical to a357d187 apart from the ReadRulePatterns accessor, which is justified). But the rehydrate gate grew past round 2's pathgate plus its hostperm additions together. This fix round alone added 247 code lines and 94 branch operators to answer 20 findings. The screen is again a path finder in arbitrary text (flag gluing, environment-variable grammars, device lists, URL schemes, three quote readers, a JSON grammar check, a backtracking rule-to-root matcher, an error-chain redactor), and each review round grows it by about 25%. That is the trend D61 meant to stop.
  - Evidence: Non-comment, non-blank code lines / funcs / branch operators (if, case, for, &&, ||) / regexps:
- pathgate.go at a357d187: 82 / 6 / 22 / 4.
- pathgate.go at 1dd7b00d: 645 / 41 / 186 / 11.
- pathgate.go at 69860c1d: 982 / 62 / 357 / 7.
- pathgate.go at HEAD: 1,229 / 80 / 451 / 9.
- hostperm non-test code: 1,468 at a357d187, 1,807 at 1dd7b00d, 1,504 at HEAD. Its branches: 337, 425, 346.
- New code over a357d187: round 2 was +902 lines and +252 branches; HEAD is +1,183 lines and +438 branches, which is 31% more lines and 74% more branches than round 2.
  - Fix: The coordinator should rule a size cap for this gate, or accept the size explicitly. Candidate cuts:
- Move screenLiteral into the tests (see the nit).
- Replace redactReason, redactClauses and trailing with one rule: replace the whole reason by withheldDropID when reasonWithheld holds, keeping the entry's kind and its restore call. The only kind whose reason is a machine error is pointer_git_unavailable.
- Fold cutRoot, rootPrefix and cutValueWithheld into one helper that treats the cut stretch as a prefix.
- Make any further finding in the free-text (c) finder a recorded limit rather than new grammar, unless it is a leak through a spelling D61 names.
- **nit** `internal/rehydrate/pathgate.go:898-902 (screenLiteral)`: screenLiteral has no production caller: screenBy and rootCover use ruleSegments and literalOf directly, and only tests call screenLiteral (TestScreenLiteral_*). ADR §23 item 7(a) and the file header name `screenLiteral` as the mechanism. The pinned test therefore exercises a wrapper the gate never runs, and it could drift from screenBy without any test failing.
  - Evidence: Every function in pathgate.go was grepped for callers outside its own definition and outside _test.go files. screenLiteral is the only one with no production reference: prod-refs=0, test-refs=3.
  - Fix: Either point the TestScreenLiteral_* rows at the production entry (call screenBy on a pathJudge and assert j.screens, j.midScreens and j.screenAll), or move screenLiteral into the test file. Update the ADR and header references to name ruleSegments and literalOf, or screenBy.

## review:rehydrate:usefulness:r2: verdict `needs-fixes`, 6 finding(s)

- **major** `internal/rehydrate/pathgate.go:226-231 (newPathJudge notes withheld values), :241-243 (recordedPath), :248-260 (note adds the basename to knownText), with :360-363 (absLike treats a leading single backslash as rooted on every platform)`: A Grep regular expression in the same checkpoint makes unrelated free text disappear. A one-word Grep pattern that starts with a backslash (`\bConfigLoader\b`, `\w+Error\b`, `\s`, `\.Evaluate\(`), or a Grep preview below the root with such a pattern (`<root>\internal \bretryBackoff\b`), is classed as a structured value. absLike reads the leading `\` as a root, inside() finds it outside the project, and recordedPath lets it be noted. Its 'basename', taken from the slash form, is then a regex fragment: `b`, `s`, `(` or `w+error`. That fragment becomes a withheld name. Every free-text summary in the build that holds it where a name starts is then withheld: `go build ./...`, `git branch -a`, `npm run build`, `bash scripts/x.sh`, `git status`, `ls src/`, `npm start`, `echo (done)`. This is the 'fragments, spans or substrings of other summaries' poisoning that D61(2)(b) forbids, and it contradicts ADR item 7(b), 'no free text poisons another'. a357d187 does no cross-summary noting, so this is a regression. `\b…\b` is among the most common Grep patterns in Claude Code sessions. The behaviour is the same on Linux and macOS by code reading: filepath.Rel of an absolute root and `\bfoo\b` fails there too. It was observed on Windows.
  - Evidence: Scratch probe (git archive of HEAD 9f40a6fc, real hostperm policy, UAT-12 rules). In isolation, `go build ./...`, `git branch -a`, `npm run build`, `cat bin/run.sh` and `git status` are all shown. Each pattern below was added to the same build:
- `\bConfigLoader\b` noted knownText=b. The first four texts were then withheld ('known:b').
- `<root>\internal \bretryBackoff\b` noted knownText=b|internal /bretrybackoff/b, with the same effect.
- `\w+Error\b` withheld `bash scripts/x.sh` as well.
- `\s` noted s, which withheld `git status`, `ls src/` and `npm start`.
- `\.Evaluate\(` noted `(`, which withheld `echo (done)` and `go test ./... -run 'Test (A|B)'`.
Usefulness corpus of 175 previews, all in one build: in every one of the 9 scenarios, 7 summaries were withheld only in the combined build, all as known:b from the corpus's Grep `\bConfigLoader\b`. They are `cd "<root>" && go build ./... && go vet ./...`, `go build ./... >/dev/null && echo ok`, `npm run build && npm run lint`, `rg -n "func Build" internal/`, `cmd /c dir /b`, `git commit -m "fix(rehydrate): …"` and `git stash list && git branch -a`. The budget rendered only about 40 of the 175 pointers, so the real number is larger. The existing TestBuild_AShortWithheldNameNeverRedactsAnUnrelatedReason (`/W`) shows the same class was already known for reasons. Only the reason screen got a boundary.
  - Fix: 1. Never note a value that is not a recorded path.
   - In recordedPath, reject a value that starts with a single backslash and is not `\\` UNC. Read, Write and Edit file_path previews are drive-absolute or `/`-absolute, so such a value is a Grep or Glob pattern.
   - Reject any value holding a notPlainPathRune outside the root's spelling.
2. In note(), do not add a basename shorter than minCutPrefix (3 bytes) to knownText. Keep knownPaths as is, so the `/W` reason row stays green.
3. Add a red-first row. Put each of the Grep previews `\bConfigLoader\b`, `\w+Error\b`, `\s`, `\.Evaluate\(` and `<root>\internal \bfoo\b` in one build with `go build ./...`, `git branch -a`, `git status`, `ls src/` and `echo (done)`, and assert that all the free texts are shown.
4. Correct ADR §23 item 7(b) if any shape stays noted.
- **minor** `internal/rehydrate/pathgate.go:1149-1153 (protect: quoteOpen(t[:e])); internal/rehydrate/summary_privacy_test.go:424 (TestBuild_TheProjectRootFollowedByMoreWordsIsShown covers only `proj` and `John Smith/proj`)`: When the project root's path contains an apostrophe, every summary in which the root is followed by a space is withheld as a 'sibling'. protect checks quoteOpen on t[:e], and that prefix includes the root's own apostrophe, which every quote reader takes for an open quote. D61(2)(c) names this case explicitly: the root's spelling, apostrophe included, must never be split, and `cd <root> && go test ./...` and `git -C <root> status` must be shown. The affected shapes are:
- the store-built Grep or Glob preview whose path is the root (`<root> TODO`, `<root> **/*.go`), which has no shell quoting at all;
- cmd.exe commands, where an apostrophe is literal;
- even the correctly POSIX-escaped `cd /…/o\'brien/proj && go test ./...`, which the PowerShell reading sees as an open quote.
This is over-withholding only. Quoted spellings are shown.
  - Evidence: Scratch probe, root `<tmp>\o'brien\proj`, rule Read(./private/deny.txt).
- Withheld as text-sibling: `<root> TODO`, `<root> **/*.go`, `<rootfwd> TODO`, `<root> func main`, `cd <root> && go test ./...`, `cd <rootfwd> && git log --oneline -n 5`, `git -C <root> status --short`, and `cd C:/…/o\'brien/proj && go test ./...`.
- Shown: `git -C "<root>" status`, `cd "<root>" && go test ./...`, the PowerShell backtick-escaped form and `<root>\src TODO`.
- Root `John's projects\proj`: `<root> TODO` and `<root> **/*.go` withheld.
- Corpus scenario uat12-apos: the Grep preview `<root> err != nil \{` was withheld (text-sibling) and is shown with the plain and spaced roots.
  - Fix: Compute the quote state without the root's own literal characters.
- Pass quoteOpen the held text so far (b.String() + t[last:a]) plus only the quote and escape characters the matched span inserted. Walk t[a:e] against the cleaned root rune by rune and drop the characters that are the root's own name, including its apostrophes.
- That keeps `cat "<root> old\x.txt"` and a quote opened inside the spelling read as siblings.
- Add red-first rows with roots `o'brien/proj` and `John's projects/proj` for the D61 usefulness spellings above, including the POSIX-escaped `cd`, keeping the sibling rows withheld.
- **minor** `internal/rehydrate/pathgate.go:1287-1293 (flagGlued accepts only a separator, the root's mark or a drive), :1325; docs/adr/0011-rehydration-budget-and-item-order.md:838-839`: A relative path whose `..` leaves the project is shown when it is glued to a short option. flagGlued starts a path only at a separator, the root mark or a drive letter, never at `.`. Without that start, the whole word (`-C../x`) is judged, and path.Clean keeps `-C..` as an ordinary segment. D61(2)(c) requires `..` to be normalized and an out-of-project path withheld. ADR item 7(c) says a path 'is glued to a short option' without that restriction. Round 1's F1/F17 fixed only the absolute form. This owner's own workflow runs sibling worktrees (`../qompack-develop`), so the leak names real out-of-project directories.
  - Evidence: Scratch probe, plain root, rule Read(./private/deny.txt).
- Shown: `git -C../qompack-develop log -1`, `gcc -I../openssl/include a.c`, `tar -C../backup -xf a.tar` and `go build -o../bin/app .`.
- Withheld as text-outside: `git -C ../qompack-develop log -1`, `cat ../x`, `cd .. && ls`.
- Shown, correctly: `git log main..feature`, `git diff HEAD~3..HEAD`, `git diff origin/main...HEAD`.
  - Fix: 1. In flagGlued, also accept p[i] == '.' when p[i:] begins `..` followed by a separator or the end of the token (and the `.\.` reading).
2. Or, in tokenOutside, strip a leading `-[A-Za-z]+` from a word-start token before calling relativeEscapes.
3. Add the four spellings above to TestBuild_AnAbsolutePathGluedToAFlagIsWithheld (or a sibling row) as withheld. Keep the `main..feature` and `HEAD~3..HEAD` revision ranges as shown.
- **minor** `internal/rehydrate/pathgate.go:1015 (stripQuotes drops every `^`), :1322-1323 (tokenOutside reads any `\`-led token of two segments as a POSIX absolute path)`: Regular expressions are read as absolute paths outside the project. stripQuotes removes `^` (a cmd escape) everywhere, so a Grep pattern `^\s*func\b` becomes `\s*func\b`. tokenOutside then counts that as a 'POSIX absolute path of two segments', because it treats `\` as a separator and `\s*func` and `b` as segments. This regressed from a357d187, which showed these patterns. The same rule withholds `\bConfigLoader\b`, `src \d+\.\d+\.\d+`, `\s+$` and `grep -E "\bfoo\b" -r src/`, which a357d187 also withheld. D61(2)(c) lists POSIX absolute paths, which are `/`-led. A `\`-led word made of regex escapes is no path on any platform.
  - Evidence: Usefulness corpus, all 9 scenarios at HEAD.
- Withheld at HEAD, shown at a357d187: Grep `^\s*//\s*TODO` (value-outsideIn `\s*//\s*TODO`). Probe: `^\s*func\b` is also withheld; `^\s+return`, `^import \(` and `^func \w+\(` are shown.
- Withheld at both commits: Grep `\bConfigLoader\b` (value-outside), `src \d+\.\d+\.\d+` (text-outside), `\s+$` (value-outside) and `grep -E "\bfoo\b" -r src/` (text-outside `\bfoo\b`).
- These 5 entries are 5 of the 7 summaries withheld at HEAD in every scenario that name no path.
  - Fix: 1. Do not let a removed `^` create a token start: strip a caret in stripQuotes only before a character cmd.exe would escape, or keep it for outsideIn.
2. Treat a token that starts with a single backslash as a path only when it reads as names: no backslash followed by a regex class letter (b B d D s S w W) and then a non-letter or the end, and no regex metacharacter (`+ * ? { } ( ) | $`). Alternatively, count only `/`-led tokens as D61's POSIX absolute paths, and count `\`-led ones only when their segments are plain names of two or more characters.
3. Add a usefulness row with the six Grep patterns above, shown.
- **minor** `internal/rehydrate/pathgate.go:636-641 (textWithheld matches j.screens with strings.Contains), :116-129 (screenBy adds the literal of every rule, including one rootCover proves cannot reach the project)`: Every rule literal is matched as a raw substring, so a literal hidden inside an unrelated word withholds the text. `git fetch`, `sketch.md` and `stretchr` hold `etc` under Read(//etc/**), and `tsconfig.json` holds `config` under Read(~/.kube/config). D61(2)(a) says 'contain', and the ADR accepts mentions such as `process.env` and `git config user.email`. But a literal taken from a whole segment (literalOf mid=false) is always spelled at a segment start in any path the rule refuses. Matching it where a name starts (namedAt, already used for (b) after review F11) therefore loses no privacy apart from names built at run time, which are a recorded limit. It would show `git fetch origin`, `cat tsconfig.json`, `npm run fetch-data` and the testify URL. Separately, a home- or absolute-anchored rule that rootCover proves reaches no project path is still screened by its literal. Every absolute or home spelling of what it refuses is already withheld by (c), so the literal only adds the cd-relative case, which is already an accepted limit. In this corpus, the user rules' `config` is the largest single source of over-withholding.
  - Evidence: Usefulness corpus.
- Scenario user rules [~/.ssh/**, ~/.aws/**, ~/.kube/config, **/*.pem], plain root and the config-service root: 13 of 159 no-path summaries withheld at HEAD, 6 more than without the rule. All 6 are literal:config: `git config user.email`, `cat tsconfig.json`, `go test ./internal/config/... -run TestLoad`, a Task prompt 'Check config loading', `git show HEAD~2:internal/config/config.go | head -40` and `{"query":"how is config loaded"}`. a357d187 showed 5 of them.
- Scenario Read(//etc/**), root `fetcher`: the WebFetch preview `https://pkg.go.dev/github.com/stretchr/testify/require` was withheld (literal:etc).
- Probe under Read(//etc/**): `git fetch origin`, `git fetch --all --prune`, `npm run fetch-data`, `cat sketch.md` and `etcd --version` all withheld.
  - Fix: Owner or coordinator decision, since D61 says 'contain'.
1. Recommended: match non-mid screens with namedAt in all three screenForms, and keep substring matching for midScreens (`.env` from `**/*.env`). Add rows showing `git fetch origin` under //etc/** and `cat tsconfig.json` under ~/.kube/config. Keep `cat /etc/passwd`, `cat ./.env` and `cat x/.env` withheld.
2. Optional: when a rule is anchored outside the project and rootCover adds nothing for it, skip its literal, and record that in ADR §23 item 7(d).
- **nit** `internal/rehydrate/summary_privacy_test.go:515-518`: The criterion-change comment on TestBuild_AKnownWithheldPathIsFoundAnywhereInAText still says free text is withheld when it contains 'a withheld path's basename at all'. Since d5487f74 (review F11), a basename counts only where a name starts (namedAt), and ADR item 7(b) says so.
  - Evidence: The comment reads: 'Free text is now withheld when it contains a rule's literal or a withheld path's basename at all'. namedAt at pathgate.go:684-697 skips a match glued to a preceding name byte.
  - Fix: Reword it to 'where a name starts (namedAt); what follows the name is not judged, so `my secret.txt.bak` holds `my secret.txt`'.

## review:rehydrate:privacy:r2: verdict `needs-fixes`, 3 finding(s)

- **major** `internal/rehydrate/items.go:1264-1333 (buildRestoredInstructions); the same code is at a357d187:1287`: Item 6a and section 7 show a host-denied path, and 6a also injects that file's content. buildRestoredInstructions passes every file pointer to d.Rules.PathScoped and d.Rules.NestedClaudeMD, the withheld ones included. NestedClaudeMD then walks the ancestors of a withheld pointer such as private/deny.txt. Under Read(./private/**) it returns private/CLAUDE.md. The 6a heading names it and its body is rendered. When it does not fit, its drop entry (ID private/CLAUDE.md, reason 'restore: Read private/CLAUDE.md') reaches section 7 and dropped(). Nothing judges rule.Path. gateDropReasons screens only Detail, and only for paths the build recorded. The code predates this branch, but it is a section-7/dropped() leak of a host-denied path, which D60(i) covers. The recorded limit for unrecorded paths in reasons does not cover the ID or the 6a content, and docs/security.md says no drop reason shows such a path.
  - Evidence: Overlay probe TestZZProbeNested at 9f40a6fc. Fake host rule ./private/**, file pointer private/deny.txt, fakeScanner nested rule private/CLAUDE.md with body ZZ-NESTED-BODY. At maxBudget: 'payload names private/CLAUDE.md: true; body injected: true'. At budget 200: drop nested_claude_md "private/CLAUDE.md" "did not fit the rehydration budget; restore: Read private/CLAUDE.md". The real scanner (internal/rules/scanner.go:134) reaches this file through the withheld pointer alone.
  - Fix: In buildRestoredInstructions, pass the scanners only the pointers judge.withheld allows (the existing `shown` slice, computed before the scans). Also skip, or record by hash, any returned rule whose Path judge.withheld refuses, so a rule file under a denied directory (for example .claude/rules/** under a deny rule) is neither rendered nor named in its drop entry. Add a red-first row: Read(./private/**), a withheld pointer private/deny.txt and a nested private/CLAUDE.md. Require that neither res.Text nor res.Dropped names it or carries its body. Correct the wording in docs/security.md and ADR §23 item 9 to match.
- **minor** `internal/rehydrate/pathgate.go:1095-1118 (rootFollowedBy, the quote case) and protect at :1050-1080`: An out-of-project sibling of the root, '<root> old', is shown when the quote that makes the space part of the name is glued right after the root's spelling. rootFollowedBy treats any quote after the root as the end of the root's word (followedByPath). A quote that opens a new quoted stretch beginning with a space continues the same shell word, so a shell joins it into '<root> old/…'. ADR §23 item 8 and the item 2 limit both say a quoted sibling is withheld. Only the unmarked-space form is a recorded limit.
  - Evidence: Overlay probe TestZZProbeSibling, root C:\q\proj, rule ./private/deny.txt. SHOWN: `cat C:\q\proj" old"/notes.txt`, `cat C:\q\proj' old'/notes.txt`, `cat "C:\q\proj"" old/notes.txt"`, `cat C:\q\proj" old/notes.txt"`. Withheld, as expected: `cat "C:\q\proj old/notes.txt"`, `cat C:\q\proj\ old/notes.txt`, `cat C:\q\proj"2"/notes.txt`.
  - Fix: In rootFollowedBy (or in protect before the switch), when t[e] is a quote character, skip the run of quote characters to k. If t[k] is a space and quoteOpen(t[:k]) is true, return followedBySibling. A name byte at t[k] already gives followedByName (the `"2"` case). Add the four spellings above as withheld rows. Keep `cat "<root>"/notes.txt` and `cd "<root>" && make` as shown rows.
- **nit** `docs/adr/0011-rehydration-budget-and-item-order.md §23 item 2 limits, and item 7(c)`: D61's two-segment rule means a single-segment absolute directory followed by a relative name in the same command (`cd /q && cat other/x.txt`, `cd / && cat q/other/x.txt`) is shown. The limit for a working directory changed by an earlier `cd` speaks of later calls ('persists between calls'). It does not say plainly that the same command line is covered too.
  - Evidence: pathgate.go:1214 hasSecondSegment requires two segments, and outsideIn reads the relative name after `&&` as project-relative. This follows D61(2)(c) as written, but the ADR's limit wording reads narrower.
  - Fix: Widen the cd-relative limit sentence to include a `cd` earlier in the same command, with one example such as `cd /q && cat other/x.txt`. No code change.

## fix:rehydrate:r2: status `done`, head `108f8cd5e7de69fcc68fb4e9525e578ec332f5f5`

### Root cause

Each finding had its own cause:
- **F1:** Item 6a predates the D50 gate. It handed every file pointer to the rule scanners and never judged a rule file's own path.
- **F2 and F4:** The quote readers were given the raw text up to the end of the root's spelling, including the root's own apostrophe. They also never looked at a quote glued right after the root.
- **F3:** recordedPath accepted any value that looked rooted or held a separator. So regular expressions led by a backslash, and whole multi-word commands, were noted as withheld paths, and their fragments became withheld names.
- **F5:** flagGlued never started a path at `..`.
- **F6:** stripQuotes dropped every `^`, and tokenOutside treated any backslash-led token of two segments as an absolute path.
- **F7:** Rule literals were matched as raw substrings.
- **F8:** The round-1 F4 fix sent the whole root-started summary, arguments included, to the host.
- **F9:** Each value of a path-named JSON argument was judged on its own.

### Summary

PRODUCT CODE CHANGED

# Wave 19c rehydrate, fix seat round 2: all ten review findings and the three nits are resolved

The branch closeout/w19-rehydrate is now at 108f8cd5, two commits on top of 9f40a6fc. Nothing was pushed, and the worktree is clean.

**What changed in product code:**
- internal/rehydrate/pathgate.go: the gate itself.
- internal/rehydrate/items.go: item 6a, which restores instruction files.
- internal/daemon/rehydrate_service.go: one comment only.
- internal/hostperm and internal/checkpoint: untouched.

## Red-first evidence
- 8 new rehydrate rows are in the new file internal/rehydrate/summary_screen_edges_r2_test.go.
- 2 new daemon rows go through the real adapter.
- The daemon cost row gains 2 variants.
- To prove the rows are red on the old code, I copied 9f40a6fc's product code into a scratch directory (`git archive`) together with the new test files. Every new row and both new cost variants failed there, each for the reason the review gave. All pass on the fix.

## Review resolution

### Fixed

**F1 (major): item 6a restored a denied instruction file and named it.**
- buildRestoredInstructions now hands both scanners only the pointers section 6 shows.
- restorableRules then drops every rule file whose own path the judge withholds (outside the project, or refused by the host). Such a file is never rendered and never named in section 7 or dropped().
- It is not counted as a loss either: the session could not have read it.
- Row: TestBuild_AnInstructionFileTheHostRefusesIsNeverRestored. It runs under ./private/** with a withheld private/deny.txt pointer, a nested private/CLAUDE.md, a refused .claude/rules/secret.md and a ../outside.md. It checks at the maximum budget and at a budget where 6a is dropped. It also asserts that the scanner was handed only src/a.go, and that the allowed rules are still restored.
- ADR item 9 and docs/security.md now say this.

**F2: a quote glued after the root hid a sibling.**
- protect now looks at a run of quotes right after the root's spelling. If the next character is a space and the quote state after that run is open, it is a sibling (`<root>" old"/x.txt`).
- Row: TestBuild_AQuoteGluedAfterTheRootStillNamesASibling. The review's four spellings are withheld. `cat "<root>"/notes.txt`, `cd "<root>" && make` and `cat "<root>" "old/notes.txt"` are shown.

**F4: an apostrophe in the root was read as an open quote.**
- rootSpellingOf now captures each run of quotes inserted inside the root's spelling.
- A quote that is the root's own character is matched by ownQuote, one of the literal ways a shell spells it: bare, `\'`, a backtick, doubled, or closed and reopened. It is not captured.
- protect reads the quote state from the text before the root plus the captured runs only.
- Rows:
  - TestBuild_AnApostropheInTheRootIsNotAnOpenQuote, for roots `o'brien/proj` and `John's projects/proj`. The usefulness spellings are shown, including the POSIX-escaped `cd`. The sibling and deny spellings stay withheld.
  - TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote, through the real adapter.
- Side effect: the root spelled without its apostrophe (`obrien`) no longer matches as the root. That is another directory, so it is now withheld. This is recorded.

**F3 (major) and F6: Grep regular expressions were read as paths and as withheld names.**
- New regexLike: a word led by one backslash is a regular expression when it holds `+ * ? { } ( ) | $ ^ [ ]`, or has `\b \d \s \w` (or a capital of one) before a non-letter or the end. Such a word is free text, not a value, and not a POSIX or Windows absolute path.
- stripQuotes now keeps a `^` that stands before a separator.
- Noting is narrower:
  - recordedPath notes only one-word values.
  - note() skips a basename shorter than minCutPrefix (3 bytes).
  - knownPaths is unchanged, so the existing reason row with `/W` stays green.
- I used the reviewer's first option (regex signatures) rather than "segments of two or more plain characters". The first option still treats `\Users\me\a#b.txt` and `\x\y.txt` as paths, so it leaks less.
- Rows:
  - TestBuild_ARegularExpressionIsNeitherAPathNorAWithheldName. In one build it has all the review's patterns plus `go build ./...`, `git branch -a`, `git status`, `ls src/`, `echo (done)` and others, and a one-letter outside file pointer basename `b`. All of these are shown. `type \Users\me\.ssh\id_rsa`, `\Users\me\.aws\credentials` and `cat /etc/passwd` stay withheld.
  - TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules, through the real adapter.

**F5: a relative path glued to a flag could leave the project.**
- flagGlued now starts a path at a `..` segment.
- Row: TestBuild_ARelativePathGluedToAFlagThatLeavesTheProjectIsWithheld. The review's four spellings and `git -C.. status` are withheld. `main..feature`, `HEAD~3..HEAD`, `origin/main...HEAD`, `-I./include` and `-o./bin/app` are shown.

**F8 (major): a command run from the root went to the host whole.**
- rootedStretch hands the host only the stretch from the root through the last word that holds a separator. For a Read of `<root>/my docs/x.txt` that is the whole path. For `<root>\tools\lint.ps1 --since HEAD~1` it is the script path. The whole summary is still screened as free text.
- Multi-word values are never noted.
- Rows:
  - TestBuild_ACommandRunFromTheRootIsJudgedOnlyThroughItsPath uses a fake host that refuses `~1` words, standing in for Windows 8.3 names. It asserts the exact judged map {lint.ps1, src, build.sh, linked/go.sh}, each once. `git diff src HEAD~1` and the lint commands are shown.
  - The daemon usefulness row above.
  - Cost-row variant "commands run from the root": 20 + 80 judgements, none of them holding a space. The row now asserts that every variant's judged paths hold no space.
- The existing F4 link rows stay green.

**F9: a path-named JSON array cost one judgement per element.** I took option 2, which keeps D61(4) literal.
- A JSON preview is judged through its path-named value only when it has exactly one. With several (an array, or a source plus a destination), it is free text alone. So a summary costs at most one host judgement.
- Row: TestBuild_AJSONPreviewCostsAtMostOneHostJudgement. The judged map must be exactly {src/one.go: 1}, and deny and `..` members are withheld.
- Cost-row variant "path-named arrays": 20 judgements, against 500 on 9f40a6fc.

**F7: rule literals matched inside other words.** I took the recommended option 1, which narrows D61's "contain", with evidence.
- A literal taken from a whole segment is spelled at a segment's start in every path the rule refuses. It now counts where a name starts, as withheld names already do (namedAt).
- namedAt also counts a match after any non-ASCII byte (a typographic quote or a no-break space) and one glued to a short option (`tar -Csecrets`).
- Literals taken from inside a glob run (`.env` for `**/*.env`) still count anywhere.
- Row: TestBuild_ARuleLiteralIsMatchedOnlyWhereANameStarts. `git fetch`, `sketch.md`, `stretchr`, `tsconfig.json`, `mysecrets.md`, `process.env` under ./.env and `src.env` are shown. `cat /etc/passwd`, `cat x/.env`, `cat .env.local`, `tar -C.env`, `type .\.env`, `tar -Csecrets`, `kubectl get secrets` and `prod.env` under **/*.env stay withheld.
- One regression I introduced and fixed in the same commit: `type private\de^ ny.txt` stopped matching under the boundary rule. screenForms now reads the escape-joined text in both forms (escapes removed, and escapes read as separators).
- Option 2 (skip the literal of an anchored rule that cannot reach the project) is not done; it is listed for the owner.

**Nits: all three done.**
- ADR: the cd-relative limit now covers a `cd` earlier in the same command line (`cd /q && cat other/x.txt`).
- The criterion comment on TestBuild_AKnownWithheldPathIsFoundAnywhereInAText now says "where a name starts".
- screenLiteral, which production never called, moved into summary_screen_test.go. Its comment says it is ruleSegments followed by literalOf, which is what screenBy calls. The ADR and the file header now name ruleSegments and literalOf.

### Partly fixed, with reasons: F10 (size)
- Done: screenLiteral moved out of product code.
- Rebutted: replacing the redaction trio with "replace the whole reason". D61(3) says "redact the path, keep the error kind", and TestBuild_AReasonKeepsItsRestoreBesideAWithheldNamesake pins clause-level redaction. Replacing the whole reason would drop the error kind.
- Not done: folding cutRoot, rootPrefix and cutValueWithheld. They serve three different call sites, no finding asks for a behaviour change, and folding them is risk with no fix to show for it.
- Size of pathgate.go (non-comment lines / funcs / branch operators / regexps):

| Revision | Lines | Funcs | Branch ops | Regexps |
|---|---|---|---|---|
| a357d187 | 82 | 6 | 22 | 4 |
| 1dd7b00d | 645 | 41 | 186 | 11 |
| 9f40a6fc | 1,229 | 80 | 451 | 9 |
| HEAD | 1,299 | 83 | 493 | 9 |

- This round added 70 lines (+5.7%), where the last round added about 25%. items.go gains one function.
- A size cap is the coordinator's call (see needs_owner).

## Other checks
- A temporary probe file, deleted and never committed, ran 55 deny spellings through the gate: quoted, escaped, nested shells, `&&`, `@`, `<`, `$PWD/`, smart quotes, a no-break space, percent-encoding, a cut prefix, a rooted denied file plus a word, and others. All were withheld, and the usefulness texts were shown.
- 400 summaries under a 116-byte root with an apostrophe were judged in 39 ms, so the capture groups add no measurable cost.

## Docs
- **ADR 0011 §23:**
  - "What changed" names this review.
  - Item 2's limits: the rooted path part, multi-value JSON, a name glued to a variable (`%D%deny.txt`), the same-command cd, the 8.3 example (now about the path part), the root spelled without its apostrophe, the drive-less path read as a regular expression, and the short basename. `process.env.NODE_ENV` moves to the glob-rule example.
  - Items 6 to 10 are updated. The evidence list adds the rows, and the criterion changes are recorded.
- **docs/security.md:** says what the block now restores and judges, and names the limit for drop reasons that mention an unrecorded path.

## Checks
- All passed: vet for GOOS windows, linux and darwin, golangci-lint, fmt-check, test/docs, docmarkers, runpatterns, and the three gen-*-docs `--check`.
- Windows, full packages: rehydrate passed (19 s); daemon passed (297 s).
- Windows: each new or changed row passed with `-count=20` and with `-race -count=3`. The host-ceiling property passed at 1,500 checks (74 s).
- Linux, non-root qompack-test, `-p 2`, at 108f8cd5: rehydrate 323 pass; daemon 1,692 pass, 1 skip. The 14 new or changed rows with `-race -count=3` and CGO on: race_fail=0.
- Container: it was stopped when I began. I started it, checked that only its sleep process was running at the end, and stopped it again.

Artifacts are in scratchpad/w19c-rh-r2fix/.

### Commits

- c5d9e82a fix(rehydrate): close the w19c round-2 review's gaps
- 108f8cd5 docs(adr): record the w19c round-2 review's screen fixes

### Tests

- `GOOS=windows|linux|darwin go vet ./internal/rehydrate/ ./internal/daemon/`: pass (re-run at HEAD 108f8cd5)
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/rehydrate/... ./internal/daemon/...`: pass, exit 0, no findings
- `go run ./tools/devtool fmt-check`: pass (after gofumpt on the new test file and rewording a doc comment whose '' gofmt would turn into a typographic quote)
- `go test -p 2 -count=1 ./test/docs; go run ./tools/devtool lint --only=docmarkers,runpatterns; go run ./tools/devtool gen-config-docs --check (and gen-command-docs, gen-mcp-docs)`: ok; PASS runpatterns, PASS docmarkers; generated docs up to date
- `go test -p 2 -count=1 ./internal/rehydrate/ (Windows)`: ok 19.3s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows)`: ok 297.0s
- `for t in TestBuild_AnInstructionFileTheHostRefusesIsNeverRestored TestBuild_AQuoteGluedAfterTheRootStillNamesASibling TestBuild_AnApostropheInTheRootIsNotAnOpenQuote TestBuild_ARegularExpressionIsNeitherAPathNorAWithheldName TestBuild_ARelativePathGluedToAFlagThatLeavesTheProjectIsWithheld TestBuild_ARuleLiteralIsMatchedOnlyWhereANameStarts TestBuild_ACommandRunFromTheRootIsJudgedOnlyThroughItsPath TestBuild_AJSONPreviewCostsAtMostOneHostJudgement TestScreenLiteral_IsTheRulePatternsLiteralPart TestScreenLiteral_ResolvesDotDotAsTheHostDoes TestBuild_AKnownWithheldPathIsFoundAnywhereInAText; do go test -p 2 -count=20 -run "^$t\$" ./internal/rehydrate/; done (Windows)`: all 11 ok
- `the same 11 rehydrate loops with go test -p 2 -race -count=3 -run "^$t\$" ./internal/rehydrate/ (Windows)`: all 11 ok
- `for t in TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote; do go test -p 2 -count=20 -run "^$t\$" ./internal/daemon/; go test -p 2 -race -count=3 -run "^$t\$" ./internal/daemon/; done (Windows)`: all 6 ok
- `go test -p 2 -count=1 -run '^TestBuild_NeverExceedsTheHostCeiling$' ./internal/rehydrate/ -rapid.checks=1500`: ok 73.8s
- `red-first: git archive 9f40a6fc into scratch + the new test files; ran each of the 8 new rehydrate rows and TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers, TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules, TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote by exact name (-test.run "^$t\$")`: all 11 rows red on 9f40a6fc for the reasons the review gave; in the cost row only the two new variants failed (path-named arrays 500 judgements; the root-started command judged whole)
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w19c-rhr2 --out <scratch>/w19c-rh-r2fix/linux HEAD touched --no-race --gomaxprocs 2 -- ./internal/rehydrate ./internal/daemon`: PASS at 108f8cd5 as non-root qompack-test: rehydrate 323 pass, daemon 1692 pass 1 skip
- `Linux run clone cx-w19c-rhr2-touched-108f8cd-20261004T013353Z, CGO_ENABLED=1, as qompack-test: go test -p 2 -race -count=3 -run "^$name\$" ./<pkg>/ for each of the 14 new or changed rows listed above`: 14 ok, race_fail=0

### Criterion changes

- TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers:
- New variants 'path-named arrays' (expects 20) and 'commands run from the root' (expects 20 + 80, one judgement per root-started summary, which is a structured summary judged through its path part).
- costBuild now returns the judged paths, and the row requires that none holds a space, so no command argument reaches the host.
- The three earlier variants keep their exact 20.
- D61(2)(a) narrowed (review F7): a rule's whole-segment literal counts only where a name starts (namedAt), after a non-ASCII byte, or glued to a short option. A literal from inside a glob run still counts anywhere. The evidence is in ADR 0011 §23 item 7(a). No earlier withheld row changed.
- D61(2)(b) narrowed (review F3): only one-word structured values are noted, and no basename shorter than minCutPrefix (3 bytes) is screened for. knownPaths is unchanged.
- D61(4) applied (review F9): a JSON preview's path-named value is judged by the host only when it is the preview's only one.
- Doc-only: TestBuild_AKnownWithheldPathIsFoundAnywhereInAText's criterion comment now says 'where a name starts' (nit). Its assertions are unchanged.
- screenLiteral moved from product code into summary_screen_test.go as a test helper (nit). TestScreenLiteral_IsTheRulePatternsLiteralPart and TestScreenLiteral_ResolvesDotDotAsTheHostDoes are unchanged and green.
- No earlier row's assertion changed. Every round 1, round 2 and round-1-fix row passes unchanged on Windows and Linux.

### Open issues

- Carried: TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing (internal/daemon/daemon_test.go:1613, not in my files). Its pass or fail depends on the wall clock: the seam runs on a goroutine and the reply wait is bounded by promptReplyBudget on the real clock. It needs an injected clock or a seam-completion channel from whichever seat owns daemon_test.go. It passed in this round's Windows and Linux whole-package runs.
- Carried: d5487f74 alone fails TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened on Linux, because its fixture spelling was Windows-only (fixed in 9f40a6fc). A Linux bisect should treat d5487f74 as known red for that row.
- Carried: ToolPointer carries no tool name. These shapes are screened as free text only, so links and 8.3 names in them are not resolved (recorded limit): a relative Glob or Grep preview of several words; now also a JSON preview with several path-named values. A root-started summary is judged through its path part only, so a link at a final name that holds a space is not resolved either (recorded).
- Carried, recorded limit: an unquoted, unescaped sibling named '<root last segment> <more>' (`proj - Copy`) is shown.
- Carried: drop reasons are not screened for rule literals or bare basenames (recorded in ADR 0011 §23.2; docs/security.md now names this limit).
- Carried: a rule literal of one or two characters withholds most free text (accepted by D61). Withheld basenames shorter than 3 bytes are no longer looked for (this round, F3).
- Accepted over-withholding, recorded in §23.2:
- a URL query stretch read as drive-relative (`?q=a:b`);
- cmd switches glued into a POSIX path (`dir /s/b`);
- two-segment library ids and container paths;
- mentions of a rule's literal where a name starts (`git config user.email` under ~/.kube/config) and of a glob-run literal anywhere (`process.env` under **/*.env);
- on Windows, a root-started summary whose path part, which runs to the last word holding a separator, holds an 8.3-shaped word (`<root>\run.ps1 --since HEAD~1 -o out\x`);
- the root spelled without its own apostrophe.
- New, recorded limits:
- A drive-less backslash path that matches regexLike's signatures (`\Users\me\c++\x`, `\data\s_1\x`) is not read as an outside path.
- A name glued to a cmd variable (`type %D%deny.txt`) is a name assembled at run time and is not matched.
- F10: pathgate.go is 1,299 non-comment lines, 83 funcs and 493 branch operators (9f40a6fc: 1,229 / 80 / 451). No size cap has been ruled.
- macOS: only go vet ran (GOOS=darwin). No macOS test run and no hosted CI run for this round.

### Needs owner

- Carried: the one deviation from 'internal/hostperm (revert only)' is the read-only accessor RuleSet.ReadRulePatterns (internal/hostperm/patterns.go). It is unchanged and still needs coordinator acceptance.
- Carried: minCutPrefix = 3 bytes (pathgate.go), D61's 'length >= 3'. This round also uses it as the shortest withheld basename that free text is screened for (review F3 fix 2). Shorter basenames match where a name starts in most texts. The relative path is still screened.
- Carried: the cost fixture constants rehydrateCostFiles = 10, rehydrateCostReads = 10 and maxCostJudgements = 20 are unchanged. The new variant 'commands run from the root' expects 20 + rehydrateCostPointers (80), one judgement per root-started summary, and the row now asserts that no judged path holds a space.
- Carried fixed lists: homeVarName, devicePaths and jsonGrammar. New fixed lists in regexLike: the metacharacter set `+ * ? { } ( ) | $ ^ [ ]` and the class letters `b B d D s S w W` before a non-letter or the end. These follow the reviewer's first option; `^ [ ]` were added because a Grep pattern often holds them and a drive-less path seldom does.
- D61 deviation 1, D61(2)(a) 'contain' (review F7 option 1): a rule's whole-segment literal now counts only where a name starts (namedAt, also after a non-ASCII byte or glued to a short option). A literal from inside a glob run still counts anywhere. Evidence: every path the rule refuses spells a whole-segment literal at a segment's start. The probe and the rows show every deny spelling still withheld.
- Owner option, not taken (review F7 option 2): when an anchored rule (home, //, drive) is proven by rootCover to refuse nothing in the project, skip its literal. That would show `git config user.email`, `go test ./internal/config/...` and `{"query":"how is config loaded"}` under ~/.kube/config. It would lose only the cd-relative spelling, which is already a recorded limit.
- D61 deviation 2, D61(4) (review F9): I chose option 2. A JSON preview with several path-named values is free text only and costs zero host judgements, so every summary costs at most one. The alternative is to restate the bound per value.
- D61 deviation 3, D61(1) and D61(4) (review F8): a summary that starts at the root and goes on below it with a space is treated as a structured summary (it may be a Read of a path with a space). It costs one judgement, of its path part only. The literal-zero alternative (no host judgement for this shape, with the link and 8.3 limit recorded) was rejected because D61(1) requires a Read's file_path to be judged whole.
- Item 6a: a rule file the judge withholds is neither restored nor counted as a drop, since the session could not have read it. The alternative is a `(path withheld)` drop entry that keeps a count.
- Carried owner option (review F11): D61(2)(b) also screens for the basenames of withheld out-of-project paths. So a withheld module-cache README.md still withholds `git diff README.md`. It is kept as D61 is written.
- F10: the coordinator should rule a size cap for pathgate.go or accept its size.

## review:rehydrate:privacy:r3: verdict `needs-fixes`, 10 finding(s)

- **major** `internal/rehydrate/pathgate.go:397-398 (varRef), :404-405 (homeVarRef), :415-418 (homeOrVarPath)`: Free text shows PowerShell home-variable paths whenever `env:` is not written in lower case. varRef matches the drive name only as `\$env:` and `\$\{env:`, but PowerShell variable names and the Env: drive ignore case. `$Env:` is the spelling Microsoft's own docs use. The following command shows an out-of-project path in section 6 when no user rule's literal happens to catch it: `Get-Content $Env:USERPROFILE\.ssh\id_rsa`. ADR item 7(c) says PowerShell's `$env:USERPROFILE\x` is withheld, and round 1 rated this leak class major. Its fix covered only lower case. a357d187 and 1dd7b00d withheld these spellings (their summaryHomeOrVar matched any `$NAME`), so this is a regression against the D61 base. It has been shown since 69860c1d. No recorded limit covers it.
  - Evidence: Overlay probe on the rehydrate package at HEAD 108f8cd5, no host rules, root C:\q\proj. Each of these is SHOWN by both summaryWithheld and a full Build:
- `Get-Content $Env:USERPROFILE\.ssh\id_rsa`
- `Get-Content $ENV:APPDATA\Claude\settings.json`
- `$p = "$Env:USERPROFILE\.ssh\id_rsa"; Get-Content $p`
- `Get-Content -Path $Env:USERPROFILE/.ssh/id_rsa`
- `type $Env:HOMEDRIVE$Env:HOMEPATH\notes\x.txt`
- `{"script":"Get-Content $Env:USERPROFILE\\.ssh\\id_rsa"}`
Through the real adapter (internal/daemon overlay, mcpOpHostPolicy, UAT-12 rules), the store preview of `Get-Content $Env:USERPROFILE\.ssh\id_rsa` is shown=true.
Controls that are withheld: `$env:USERPROFILE\…` in lower case, `$Home\.ssh\id_rsa`, and `$Env:USERPROFILE` ending a word (homeVarRef is already `(?i:`).
The same leaks are shown at 9f40a6fc.
  - Fix: 1. Make the drive name case-insensitive in varRef: `\$(?i:env):[A-Za-z_][A-Za-z0-9_]*` and `\$\{(?i:env):[^}]+\}`.
2. homeOrVarRoot's `\$\{?…` already covers structured values.
3. Add red-first rows to TestBuild_AnEnvironmentVariablePathIsWithheld and to the daemon CommonIdioms row for the six spellings above.
- **major** `internal/rehydrate/pathgate.go:761-774 (namedAt), :1275-1278 (nameByte includes `$`), :739-749 (screenForms); docs/adr/0011 §23 item 7(a)`: The F7 narrowing (D61 deviation 1: rule literals count only where a name starts) is a privacy regression against 9f40a6fc. It also falsifies the evidence the deviation rests on. ADR 7(a) says every spelling of a refused path, "however it is quoted, escaped …, holds it once those characters are gone", at a segment's start. That fails for bash ANSI-C and locale quoting: once the quotes are removed, `$'deny.txt'` becomes `$deny.txt`, and `$` is a nameByte. It also fails for an escape the screen does not decode, which leaves letters or digits glued in front of the name (`private/deny.txt`, `private%252Fdeny.txt`). The `$'…'` spellings name the denied file in plain text with no escape at all. The recorded limit cites only the escaped form `$'\x70rivate'`, so it does not cover them.
  - Evidence: Overlay probe under the UAT-12 rules [./private/deny.txt, ./.env, ./secrets/**]. These are SHOWN at HEAD and WITHHELD at 9f40a6fc (same probe run on a git-archive copy):
- `cat private/$'deny.txt'`
- `cat "private/"$'deny.txt'`
- `cat $'.env'`
- `cat $".env"`
- `tar czf x.tgz $'secrets'`
- `curl -d '{"name":"private/deny.txt"}' http://localhost:8080/read`
- `curl -d '{"name":"x/.env"}' http://localhost:8080/read`
- `curl http://h/x?f=private%252Fdeny.txt`
Through the real adapter on Windows, `cat private/$'deny.txt'` and `cat $'.env'` are shown=true.
  - Fix: Choose one:
1. Add a screen form in which a `$` that immediately precedes a quote character is removed with the quote (bash `$'…'` and `$"…"`), plus a form in which `\uXXXX`, `\xXX` and repeated `%XX` escapes are decoded to a fixed point (bounded) before namedAt.
2. For whole-segment rule literals only (not knownText), also accept a match preceded by `$`.
In either case:
- Add the eight spellings above as withheld rows.
- Correct ADR 7(a)'s invariant, and name in needs_owner that deviation 1 lost these spellings until the fix lands.
- **minor** `internal/rehydrate/pathgate.go:1374-1394 (outsideIn; pathStartAfter is ASCII only), :416 (homeOrVarPath prefix class), :1098-1116 (sanitize collapses only ASCII whitespace)`: D61(2)(c) misses an absolute or home path that follows a typographic quote, a guillemet or a Unicode space. PowerShell treats “ ” ‘ ’ as quotes and NBSP, em space, ideographic space and U+2028 as whitespace, so these commands read the outside file. Whatever the shell does, section 6 shows the out-of-project path. Round 2 widened namedAt to treat any non-ASCII byte as a name start for (a) and (b), but not the (c) finder or homeOrVarPath. A two-segment drive path is caught only by accident, through the `:` start. Pre-existing: these are shown at 9f40a6fc too.
  - Evidence: Overlay probe, no rules. SHOWN at HEAD, confirmed by a full Build:
- `Get-Content “D:\secret.txt”`
- `Get-Content ‘D:\secret.txt’`
- `Get-Content “/etc/passwd”`
- `Get-Content ‘~/.ssh/id_rsa’`
- `Get-Content<U+00A0>/etc/passwd`
- `type<U+3000>D:\secret.txt`
- `cat<U+2003>/etc/passwd`
- `cat<U+2028>/etc/passwd`
- `cat «/etc/passwd»`
Control, withheld: `Get-Content “C:\Users\me\.ssh\id_rsa”`.
Nits of the same family (single-segment drive path after `!` or `*`, and a non-file URL scheme that opens local files): `cmd /v /c "type !D:\secret.txt"`, `type *D:\secret.txt`, `code --open-url vscode://file/D:/secret.txt` and `vscode://file/home/u/x.txt` are shown. The ADR's "an http(s) or other URL is not a path" covers the vscode ones.
  - Fix: 1. In outsideIn, treat any byte >= 0x80 before i as a path start, matching namedAt.
2. Add `[^\x00-\x7f]` to homeOrVarPath's leading class.
3. In sanitize, fold unicode.IsSpace runes to ' ' (or in screenText, so the store's collapse is not contradicted).
4. Add the nine spellings above as withheld rows.
- **minor** `internal/rehydrate/pathgate.go:786-807 (truncatedWithheld), :833 (cutBoundary)`: Item (8) still leaks a cut prefix whenever the cut falls in a stretch that only a normalized form spells: percent-encoded, a decoded JSON string with an escape just before the name, after a typographic quote, glued to a short option, or after `$'`. truncatedWithheld reads only the raw summary's screenForms, and its bounded prefix needs an ASCII cutBoundary. D61(2) defines the normalized forms as the raw text, its decoded JSON strings and its percent-decoded form, and applies the truncation rule to them. namedAt's boundaries (non-ASCII, flagBefore) are not used either. Pre-existing at 9f40a6fc.
  - Evidence: Overlay probe; every summary is 117 bytes plus `…`, as the store cuts. UAT-12 rules unless stated. All SHOWN at HEAD (full Build):
- `curl a… https://x.example/?f=private%2Fden…`
- `x a… file:///C:/q/proj/private/my%20sec…` under rule ./private/my secret.txt. The uncut URL is withheld.
- `{"script":"a… x\nden…`
- `{"script":"a… x\tsecr…`
- `{"list":"a… README.md\n.en…` under ./.env
- `cat a… cat “den…`
- `tar a… tar -C.en…`
- `cat a… cat $'den…`
Control, withheld: `{"query":"a… private\/den…`.
  - Fix: 1. Apply the prefix check per text part, the way freeTextWithheld screens: to each cut part's decoded JSON string (classify already marks it cut) and to percentDecode of each form.
2. In endsWithPrefixOf's bounded case, accept the same starts namedAt accepts: a non-ASCII byte, flagBefore, and a `$` before a quote once fixed as in the finding above.
3. Add the eight spellings above as rows.
- **minor** `internal/rehydrate/pathgate.go:1245-1252 (protect, the F2 quote-run check)`: A sibling of the root is shown when a quote that closes the root's spelling is followed by an escaped space: `"<root>"\ old/x.txt` in POSIX, a backtick-space in PowerShell, caret-space in cmd. A shell joins all of these into the one word `<root> old/x.txt`. F2 looks only at a run of quotes followed directly by a space. rootFollowedBy then sees the quote and returns followedByPath. The ADR §23.2 limit says a sibling whose space is escaped (`proj\ old`) is withheld, so no limit covers this. Pre-existing at 9f40a6fc.
  - Evidence: Overlay probe, root C:\q\proj, UAT-12 rules. SHOWN at HEAD (full Build):
- `cat "C:/q/proj"\ old/x.txt`
- `cat 'C:/q/proj'\ old/x.txt`
- ``Get-Content "C:\q\proj"` old\x.txt``
- `type "C:\q\proj"^ old\x.txt`
  - Fix: After skipping the quote run to k, classify what follows the run instead of the first quote. If t[k] is `\`, a backtick or `^` and t[k+1] is a space, set sibling. Equivalently, call rootFollowedBy(t, k) when k > e. Add the four spellings above as withheld rows beside TestBuild_AQuoteGluedAfterTheRootStillNamesASibling.
- **minor** `internal/rehydrate/items.go:1410-1460 (buildSkillIndex); docs/adr/0011 §23 item 9; docs/security.md`: Item 6b never consults the build's judge. A SKILL.md the host refuses still has its name and frontmatter description injected into the payload. When it is outside the compact index, or does not fit, its drop entry shows `restore: Read <its path>` in section 7 and dropped(). Under unavailable rules (D61(2)(d), fail closed) the path is named too. This is the same shape as round 2's F1, which was fixed for 6a by judging each rule file's own path. The section-7 half arguably falls under §23.2's last bullet (an unrecorded path a rule refuses, in a drop reason). Injecting the content and ignoring unavailable rules are not covered.
  - Evidence: Overlay probe. fakeIndexer entry {Name: zz-secret-skill, Description: ZZ-SKILL-DESCRIPTION, Source: .claude/skills/secret/SKILL.md}, host rule ./.claude/skills/secret/**.
- Kept in the compact index: the payload shows ZZ-SKILL-DESCRIPTION.
- Not kept: res.Text and res.Dropped carry `{Kind:skill ID:zz-secret-skill Detail:not in the compact skill index (budget 450 tokens); restore: Read .claude/skills/secret/SKILL.md}`.
- With HostRules{} (rules unavailable), section 7 still names `.claude/skills/zz/SKILL.md`.
  - Fix: 1. In buildSkillIndex, drop every entry whose Source judge.withheld refuses from both the kept and all lists, before rendering and before minting drops. This mirrors restorableRules: one host judgement per skill file, and no loss is counted because the session could not read the file.
2. Add a row under Read(./.claude/skills/secret/**) and one under unavailable rules.
3. Extend ADR item 9 and docs/security.md to 6b.
- **minor** `internal/rehydrate/pathgate.go:121-134 (screenBy), :156-192 (rootCover); internal/daemon/rehydrate_service.go (rehydrateHostPaths)`: A rule that covers the whole project but is anchored at the resolved spelling of a linked root (a junction, symlink or subst drive) does not set screenAll. rootCover compares rules only with Request.ProjectRoot as spelled. The host does resolve the root's link, so it refuses every project path, yet free text that names project files by their relative paths is shown. The §23.2 alias limit covers a rule written through an 8.3 name and aliases typed in free text. It does not cover a root opened through a link.
  - Evidence: Daemon overlay probe through the real adapter (mcpOpHostPolicy). The project is opened at the junction <tmp>\proj, which points to <tmp>\real\work, with the project rule Read(//c/…/real/work/**).
- refuses("src/main.go") = true.
- Store previews `cat src/main.go` and `go vet ./src/main.go` are shown=true.
- The structured Glob preview `src/main.go` is withheld.
  - Fix: Either way, add a linked-root row.
- Option 1: hand the build the root's resolved spellings in HostRules (the adapter already judges "the resolved spelling" when the root resolves elsewhere) and run rootCover against each.
- Option 2: once per build, probe Refuses(<root>/<fixed probe name>) and set screenAll when the host refuses a fresh name directly below the root (one extra judgement, recorded in item 10's bound). This option also covers 8.3-anchored rules.
- **minor** `internal/rehydrate/pathgate.go:251-257 (recordedPath), :262-287 (note), :502 (classify regex exemption)`: This is usefulness, not privacy. A one-word summary that looks rooted but is no path is judged as a path outside the project, withheld, and noted. Examples: a SlashCommand preview (`/review`, `/compact`), a Grep route pattern (`/api/v1/users`), and a backslash-led regular expression without regexLike's signatures (`\.test\.ts`). Its last segment then withholds unrelated free text in the same build. D61(2)(c) says a single-segment POSIX word is no path in free text, but recordedPath notes it as a structured value. On Windows, no Read, Write or Edit file_path is drive-less. This is the poisoning class D61(2)(b) set out to end.
  - Evidence: Overlay builds, rule ./private/deny.txt. Each listed poison is withheld itself and withholds its victim:
- `/review` withholds `git log --grep=review`.
- `/compact` withholds `{"query":"compact summary"}`.
- `\.test\.ts` withholds `find src -name "*.ts"` (noted `.ts`).
- `/api/v1/users` withholds `go test ./internal/users/...` and `{"query":"how are users created"}`.
Without the poison, each victim is shown.
  - Fix: 1. Do not note a value with only one segment below its anchor.
2. On Windows, note no drive-less `/`- or `\`-led value.
3. Optionally, on Windows read a drive-less single word as free text rather than a value.
4. Add a row with the four poisons and their victims shown.
- **nit** `internal/rehydrate/pathgate.go:1742-1754 (reasonWithheld) and :1810-1819 (redactReason)`: Two pointer_git_unavailable reasons keep an out-of-project gitdir:
- one whose path is the root's spelling followed by a space and a word (a main checkout named `<root> main`), which is the 'proj - Copy' heuristic applied to a drop reason, where §23.2 does not record it;
- one whose gitdir is a single-segment POSIX path. D61(3) bars any out-of-project absolute path in a reason, and the two-segment exemption is (2)(c)'s, for free text.
In an error chain, the path runs to the next `: `, so no word-splitting is needed.
  - Evidence: Overlay builds with a checkpoint pointer_git_unavailable drop. Both are kept verbatim in res.Dropped:
- `checkpoint: git index unsupported: reading index: CreateFile C:\q\proj main\.git\worktrees\wt\index: Access is denied.`
- `… reading index: open /repo.git: permission denied`
Nine other spellings are redacted correctly to `(path withheld)` with the error kind kept: drive, POSIX, UNC, a spaced home, `proj.git`, `proj-main` and `<root>\..\main`.
  - Fix: In redactReason, for a chain part of the form `<verb> <path>` (open, stat, lstat, CreateFile, GetFileAttributesEx, readlink), judge the rest of the part whole with inside(). Otherwise, in reasonWithheld, treat a root spelling followed by a space as a sibling when the stretch runs to the `: ` separator. Count single-segment absolute paths as outside in reasons.
- **nit** `internal/daemon/rehydrate_service.go:760-766; internal/rehydrate/pathgate.go:515-520 (rootedStretch doc)`: Both comments say a summary that starts at the root hands the host "only its path part, never a command's arguments". rootedStretch actually runs to the last word that holds a separator, so `<root>\run.ps1 --since HEAD~1 -o out\x` hands hostperm the whole command. ADR §23.2 records the resulting over-withholding. The cost row's "no judged path holds a space" assertion holds only for its fixture shape.
  - Evidence: rootedStretch (pathgate.go:521-530) sets end at every word containing `/` or `\`. The ADR §23.2 limit text gives the same example: '(`<root>\run.ps1 --since HEAD~1 -o out\x`, whose path part runs to `out\x`)'.
  - Fix: Reword both comments to say the stretch runs through the last word that holds a separator, and that a later argument that holds one reaches the host. Or stop the stretch at the first word that starts with `-` and record that.

## review:rehydrate:usefulness:r3: verdict `needs-fixes`, 8 finding(s)

- **major** `internal/rehydrate/pathgate.go:761 (namedAt), :739-750 (screenForms), :1075-1090 (stripQuotes); docs/adr/0011-rehydration-budget-and-item-order.md:840 (item 7(a))`: Privacy regression from deviation 1 (F7). This round matches a rule's whole-segment literal only where a name starts. The ADR justifies that by saying 'every spelling of such a path in a command, however it is quoted, escaped, nested in another shell or glued to an operator, holds it once those characters are gone', at a segment's start. Two quoting classes break that claim.
(1) Bash ANSI-C and locale quoting (`$'…'`, `$"…"`). Once the quotes are removed, a `$` is left glued before the literal, and `$` is a nameByte.
(2) cmd.exe and PowerShell, where a backslash before a quote is not an escape. In `"private\"deny.txt` the shell reads the word as private\deny.txt. The separator form, however, is built from stripQuotes, which drops `\"` as a POSIX escape. So both forms read `privatedeny.txt`.
The old substring match caught both classes. A recorded file pointer does not help either, because knownText uses the same namedAt.
  - Evidence: Method: real adapter (rehydrateHostPaths over a hermetic hostperm policy) with the UAT-12 rules, Windows, scratch git-archive copies. Probe file: scratchpad/r3/zz_probe_test.go and zz_probe2_test.go.
SHOWN at HEAD 108f8cd5 and withheld at 9f40a6fc:
- `cat private/$'deny.txt'`
- `cat private/$"deny.txt"`
- `cat private/$''deny.txt`
- `cat $'.env'`
- `type "private\"deny.txt`
- `type "private\"deny.txt" & echo ok`
- `Get-Content 'private\'deny.txt`
- `Get-Content "private\"deny.txt`
- Under Read(./private/John's notes.txt): `cat private/$'John\'s notes.txt'`. This is bash's usual way to spell an apostrophe, and the only escape in it (`\'`) is one the screen claims to undo.
- With a file pointer private/deny.txt recorded, `cat private/$'deny.txt'` and `type "private\"deny.txt` are still shown.
No existing row covers these spellings, which is why the 'withheld rows stay green' check did not catch them.
The §23.2 limit that names ANSI-C quoting gives the `$'\x70rivate'` encoding as its example, and none covers the cmd/PowerShell `\"` case.
  - Fix: Keep namedAt, which is where this round's usefulness gain comes from, and fix the screen forms instead:
(a) In screenForms, before building the forms, drop a `$` that stands before a quote: `regexp.MustCompile(`\$(['"])`).ReplaceAllString(held, "$1")`.
(b) Build the separator-reading form as `screenText(strings.ReplaceAll(t, `\`, "/"), false)` rather than from stripQuotes(t). The escapes-removed form already gives the POSIX reading.
I validated (a) and (b) through `go test -overlay`. All 11 spellings above are withheld. The rehydrate package passes, and so do the daemon TestRehydrate* rows. The 117-preview usefulness corpus shows identical counts in all 7 scenarios.
Add red-first rows for these spellings, through the real adapter. Correct the item 7(a) sentence, and the reasoning behind deviation 1 in needs_owner.
- **minor** `internal/rehydrate/pathgate.go:1239 (protect: state := b.String() + t[last:a])`: Regression from the F4 fix. For the second and later root spellings in a text, the quote state is read from b.String(), which holds each earlier spelling as a single rootMark. The quote runs those spellings captured (rootSpellingOf's groups) are therefore lost. A quote opened inside an earlier spelling and closed after it then reads as open, and the reverse also happens. The round-2 code read t[:e] and did not lose them.
  - Evidence: Real adapter, UAT-12 rules, root <P>\proj. All three of these are SHOWN at HEAD and withheld at 9f40a6fc:
- `cat <P>\"proj\a.txt" <P>\"proj old\x.txt"`
- `type <P>\"proj\a.txt" & type <P>\"proj old\x.txt"`
- with root <P>\John Smith\proj: `cat …\John Smith\"proj\a.txt" …\John Smith\"proj old\x.txt"`
In each, the sibling `proj old` is shown.
The reverse direction: `type <P>\"proj\a.txt" & cd <root> && dir` is withheld as text-sibling. That one was also withheld at 9f40a6fc.
  - Fix: Keep a separate quote-state builder qs and an offset lastQ. For each candidate spelling that passes the nameByte check:
- write t[lastQ:a] and its captured runs to qs;
- set lastQ = e;
- use state := qs.String().
The root's own characters never enter the state, and the earlier spellings' quotes are kept.
Validated by overlay: all four spellings are judged correctly, and every row stays green. Add the three spellings as withheld rows, and the cmd one as a shown row.
- **minor** `internal/rehydrate/pathgate.go:1082 (stripQuotes keeps `^` before a separator)`: Regression from the F6 fix. Keeping `^` before any separator makes cmd.exe's caret-escaped separators unreadable to outsideIn and homeOrVarPath, so absolute, home and `..` paths spelled that way are shown. The keep was meant to protect a regular expression's anchor, but the new regexLike guard in tokenOutside already exempts `^\s*func\b`-shaped words once the caret is dropped (`\s*func\b` holds `*`). Only `^/…` route anchors still need the caret kept. ADR item 7(c) records the keep but not its consequence.
  - Evidence: No rules, real adapter. All of these are SHOWN at HEAD and withheld at 9f40a6fc:
- `type ^\Users^\Quant^\.ssh^\id_rsa`
- `type ..^\..^\outside.txt`
- `type %USERPROFILE%^\.ssh^\id_rsa`
- `cd ^\Users^\Quant && type .ssh\id_rsa`
The same commands without carets are withheld at HEAD.
  - Fix: Keep `^` only before `/` (`case c == '^' && i+1 < len(t) && t[i+1] == '/'`), and drop it before `\`.
Validated by overlay: the four spellings are withheld. TestBuild_ARegularExpressionIsNeitherAPathNorAWithheldName and the rest of the suite stay green, and the corpus counts are unchanged.
Alternatively, add the caret-escaped spelling to the §23.2 limits.
- **minor** `internal/rehydrate/pathgate.go:490 (classify, JSON branch: named == 1)`: Regression from F9 option 2. A preview with several path-named values loses every structured check, including the ones that ask the host nothing. A glob that selects a recorded withheld path is the case that matters. globSelectsKnown and containment cost no host judgement, so the one-judgement-per-summary bound does not require dropping them. The §23.2 limit for multi-valued previews names only links and 8.3 names.
  - Evidence: UAT-12 rules, file pointer private/deny.txt, real adapter. These are SHOWN at HEAD and withheld at 9f40a6fc:
- `{"paths":["private/*.txt","src/a.go"]}`
- `{"paths":["private/d*","src/*.go"]}`
- `{"file":"private/de?y.txt","path":"src"}`
`{"paths":["private/*.txt"]}` is withheld at HEAD.
  - Fix: When named > 1, keep each path-named value's zero-cost checks: append a value part for any whole value with `isGlob(v) && j.globSelectsKnown(v)`, or run valueWithheld with hostRefuses skipped.
Validated by overlay (the glob arm): all three are withheld. The JSON cost row's judged map stays {src/one.go: 1}.
Add the three as withheld rows, and fix the §23.2 wording.
- **minor** `internal/rehydrate/pathgate.go:115-129 (screenBy) and :262-292 (note); ADR 0011 §23.2`: Usefulness. Two owner options carried from round 2 cause most of the over-withholding left at HEAD.
(i) The literal of an anchored home rule that rootCover proves reaches nothing in the project still screens free text (review F7 option 2).
(ii) The basenames of withheld paths outside the project are screened in free text (review F11). That poisons whole builds that read dependency sources.
Both follow D61 as written. The numbers below are evidence for the owner's ruling, not a code defect.
  - Evidence: Corpus: 117 store previews built with store.ArgsDigest, through the real adapter on Windows, each built alone and then in chunked combined builds. 109 are clean: git, go, npm, pytest, cargo, dotnet, cd <root> && …, HEAD~N, /c, URLs, PowerShell, Read/Glob/Grep, MCP JSON, Task and TodoWrite. 8 name a denied or outside path. Shown/withheld per revision:

| Scenario | a357d187 | 9f40a6fc isolated | 9f40a6fc combined | HEAD |
|---|---|---|---|---|
| no rules | 100/17 | 109/8 | 105/12 | 112/5 |
| UAT-12, plain root | 96/21 | 102/15 | 98/19 | 106/11 |
| UAT-12, `John Smith` root | 77/40 | 102/15 | 98/19 | 106/11 |
| UAT-12, `o'brien` root | 77/40 | 100/17 | 96/21 | 106/11 |
| user rules (~/.ssh/**, ~/.aws/**, ~/.kube/config, **/*.pem) | 99/18 | 98/19 | 95/22 | 105/12 |
| same rules, root `config-service` | 98/19 | 98/19 | 95/22 | 105/12 |
| **/*.env, ./config/credentials.json | 98/19 | 106/11 | 102/15 | 110/7 |

HEAD's combined builds match its isolated ones, so this round's poisoning fixes hold.
Every clean summary HEAD withholds, with the screen rule that withheld it:
- In every rule scenario: the one-word Grep preview `HEAD~1`, judged as a structured value. hostperm refuses `~1` as an unresolvable 8.3 name.
- Everywhere: `{"context7CompatibleLibraryID":"/vercel/next.js","topic":"routing"}` (text-outside; recorded).
- UAT-12: `{"k":10,"query":"rehydrate budget order secrets handling"}` (literal:secrets; recorded).
- User rules, six summaries, all literal:config from ~/.kube/config. a357d187 showed five of them:
  - `git show HEAD~2:internal/config/config.go | head -40`
  - `git config user.email`
  - `go test ./internal/config/... -run TestLoad`
  - WebFetch `https://github.com/owner/repo/blob/main/internal/config/config.go`
  - `{"k":5,"query":"how is config loaded"}`
  - the Task prompt 'Check how config loading…'
Separately, F11: one build that also holds Read previews of the cobra module cache's README.md, go.mod and command.go, and a scratch main.go, withholds 9 of 10 ordinary texts, each as known:<basename>. The same happens at 9f40a6fc:
- `git diff README.md`
- `head -50 README.md`
- `cat go.mod`
- `find . -name "*.go" -newer go.mod`
- `git diff HEAD~1 -- go.mod go.sum`
- `go run main.go`
- `go vet ./cmd/qompack/main.go`
- `{"k":5,"query":"README.md install steps"}`
- `rg -n "func Execute" command.go`
  - Fix: Owner rulings:
(i) Take F7 option 2: skip the literal of a home- or root-anchored rule that rootCover proves refuses nothing in the project. Every absolute or home spelling it refuses is already withheld by (c), and the cd-relative spelling is already a limit.
(ii) Screen D61(2)(b) basenames and relative paths only for withheld paths inside the project. An outside path's absolute spellings are (c)'s, and its basename names it only relative to a cd, which is a recorded limit.
Optionally, do not send a one-word relative value with no separator to the host. recordedPath already treats such a value as no path. Judge it by containment, globSelectsKnown and the screen instead.
- **minor** `internal/rehydrate/pathgate.go:1259-1263 (protect, followedBySpace with a quote open)`: Usefulness, present since round 2 (not this round). Inside a quoted argument, the root followed by a space always reads as a sibling. So a nested shell that changes to the project root is withheld in every project. D61 requires `cd <root> && go test ./...` to be shown, and these are the same command inside `bash -c` or `cmd /c`.
  - Evidence: Real adapter, UAT-12 rules, plain and `John Smith` roots, all text-sibling at HEAD and at 9f40a6fc:
- `bash -c "cd <rootfwd> && go test ./..."`
- `cmd /c "cd /d <root> && go test ./..."`
- `git commit -m "move tests under <rootfwd> root"`
a357d187 showed the first and third with the plain root.
`powershell -Command "Set-Location <root>; go test ./..."` is shown, because `;` follows the root.
  - Fix: In protect, when the root is followed by a space and the next word is a shell operator (`&&`, `||`, `|`, `&`, `;`, `>`), treat the root's word as ended even with a quote open: no sibling name is `proj && …`. Add the first two as shown rows, and keep `bash -c "cat <root> old/x.txt"` withheld.
- **nit** `internal/rehydrate/pathgate.go:542-553 (regexLike), :502 (classify)`: regexLike counts the glob metacharacters `*`, `?`, `[` and `]`, so every drive-less backslash glob reads as a regular expression. A one-word Glob value is then neither judged as a value nor read as an outside path. The §23.2 limit gives only regex examples (`\Users\me\c++\x`, `\data\s_1\x`).
  - Evidence: No rules. These are SHOWN at HEAD and withheld at 9f40a6fc as value-outside:
- the Glob preview `\Users\Quant\.ssh\*`
- `\Users\Quant\.aws\cred*`
The same path without the glob, `\Users\Quant\.ssh\id_rsa`, is withheld.
  - Fix: Decide regex-likeness without `*?[]` unless a class escape (`\b \d \s \w`) or `+ ( ) { } | $ ^` is also present. `\s*func`, `\w+Error` and `\.Evaluate\(` stay regexes. Otherwise, add drive-less globs to the §23.2 limit.
- **nit** `internal/rehydrate/pathgate.go:518 (rootedStretch doc); internal/daemon/rehydrate_service.go:764; docs/security.md:73`: Three sentences say a root-started summary hands the host its path part, 'never a command's arguments'. The criterion change says the space check means 'no command argument reaches the host'. In fact rootedStretch runs to the last word that holds a separator, so arguments that hold one are judged too. §23.2 itself records this (`<root>\run.ps1 --since HEAD~1 -o out\x`).
  - Evidence: By code reading of rootedStretch: `<root>\scripts\test.ps1 -Path src\a.go` is judged whole as `<root>\scripts\test.ps1 -Path src\a.go`. The cost row's no-space assertion holds only because its fixture's commands have no separator-holding argument.
  - Fix: Reword the three sentences to: 'its path part, the stretch from the root to the last word that holds a separator, which may include an argument that holds one'. Narrow the criterion-change claim to the fixture.

## review:rehydrate:cost:r3: verdict `needs-fixes`, 6 finding(s)

- **major** `internal/rehydrate/pathgate.go:543 (regexLike metacharacter set), used at :502 (classify) and :1436 (tokenOutside)`: This round introduced a privacy regression: free text now shows drive-less Windows absolute paths outside the project when they hold a glob. regexLike counts `*` and `?` (and `+`, `$`) as regular-expression signatures. So a PowerShell or cmd listing such as `dir \Users\x\.ssh\*` is read as a regex, not as a path outside the project, and is shown. The one-word Glob preview `\Users\x\.ssh\*` is shown for the same reason. D61(2)(c) requires absolute paths outside the project to be withheld. The 'globs are not resolved' limit is about resolving the glob, not about the absolute prefix in front of it. ADR §23.2 records only `\Users\me\c++\x` and `\data\s_1\x`, which makes the limit look rare, but the glob form is the common spelling in PowerShell and cmd.
  - Evidence: Probes, rule Read(./private/deny.txt), through the rehydrate package and through the real daemon adapter (mcpOpHostPolicy, UAT-12 rules).
- SHOWN at HEAD 108f8cd5: `dir \Users\someone\.ssh\*`, `Get-ChildItem \Users\someone\.aws\*`, `Get-ChildItem \Users\someone\.ssh\id_*`, `type \Users\someone\.aws\credential?`, `type \Users\someone\Documents\*.txt`, `del \Users\someone\AppData\Local\Temp\*.log`, `type \Users\someone\notes+old.txt`, and the one-word `\Users\someone\.ssh\*`.
- WITHHELD at a357d187, 1dd7b00d and 9f40a6fc: every one of these. `type \Users\someone\.ssh\id_rsa` is still withheld at HEAD.
- Fix candidate validated with go test -overlay: change line 543 to `strings.ContainsAny(tok, "+{}()|$^[]")`, i.e. drop `*` and `?`. All of the spellings above except `notes+old.txt` are then withheld. TestBuild_ARegularExpressionIsNeitherAPathNorAWithheldName stays green, and the whole internal/rehydrate package passes. This works because the class-letter rule already catches `\s*`, `\w+` and `\d?`, and a single-segment backslash word is never outside anyway.
  - Fix: Remove `*` and `?` from regexLike's metacharacter set. The class-letter rule keeps `\s*func\b`, `\w+Error\b` and `\s+$` classed as regexes. Optionally, count `+` and `$` only in a word's first segment. Add red-first rows, in the rehydrate package and through the real adapter, for `dir \Users\x\.ssh\*`, `Get-ChildItem \Users\x\.aws\*`, `type \Users\x\.aws\credential?` and the one-word `\Users\x\.ssh\*`, all withheld. Narrow ADR §23.2's regexLike limit to what remains.
- **minor** `internal/rehydrate/pathgate.go:1239 (protect: state := b.String() + t[last:a])`: This round's F4 fix reads the quote state from b, the held text built so far. But b has every earlier held root spelling replaced by rootMark, and that drops the quote runs captured inside it. If an earlier spelling of the root opened a quote inside itself and closed it right after, a later spelling's quote state is flipped. A quoted sibling of the root then reads as the root followed by a word, and is shown. This is a regression from 9f40a6fc, which read the raw t[:e].
  - Evidence: Root C:\q\proj, rule ./private/deny.txt.
- SHOWN at HEAD, WITHHELD at 9f40a6fc: `cd C:\q\"proj" && cat "C:\q\proj old\x.txt"`, the sibling `C:\q\proj old`.
- WITHHELD at both: `cat "C:\q\proj old\x.txt"`.
- Cause: state for the second spelling is `cd \x01" && cat "`, which has 2 quotes and reads as closed. The raw text has 3 quotes and is open.
- Fix candidate validated with go test -overlay: keep a second builder q, and write t[last:a] plus the match's captured runs into it wherever b gets t[last:a] plus rootMark. Use `state := q.String() + t[last:a] + runs`. The spelling is then withheld, and the whole internal/rehydrate package passes.
  - Fix: Track the quote state in its own builder: the text between spellings plus only the captured quote runs, never rootMark. Add the spelling above to TestBuild_AQuoteGluedAfterTheRootStillNamesASibling as withheld, and keep `cd C:\q\"proj" && make` as shown.
- **minor** `internal/rehydrate/pathgate.go (whole file); docs/adr/0011-rehydration-budget-and-item-order.md §23 item 10`: The brief asked for code simpler than round 2. It is not, and this round grew it again. The cost lens itself passes. Free text costs zero host judgements. hostperm is a357d187 plus the read-only ReadRulePatterns accessor only (git diff a357d187 HEAD -- internal/hostperm adds just patterns.go and patterns_test.go). The cost row is deterministic, and the free-text bound is red on both older revisions. No size cap has been ruled (round-2 F10 is carried).
  - Evidence: Code size, non-comment lines / funcs / branch operators:
- pathgate.go: a357d187 82/6/22; 1dd7b00d 645/41/186; 9f40a6fc 1,229/80/451; HEAD 1,299/83/493.
- hostperm non-test code: 1,468 at a357d187, 1,807 at 1dd7b00d, 1,504 at HEAD.
- New non-test code over a357d187: round 2 was about 902 lines (pathgate +563, hostperm +339); HEAD is about 1,253 (+1,217, +36), 39% more lines and about 90% more branches.

Cost through the real adapter (mcpOpHostPolicy, UAT-12 rules, 10 file pointers + 10 Read previews + 80 previews per set). Each cell is Refuses calls, then median wall time of 5 builds:

| Revision | Bash (17 words) | Canonical JSON, ':' and non-ASCII | Absolute-path free text | Path-named JSON | Root-started commands |
|---|---|---|---|---|---|
| HEAD | 20 / 13 ms | 20 / 11 ms | 20 / 12 ms | 100 / 41 ms | 100 / 42 ms |
| 1dd7b00d | 3,175 / 112 ms | 2,282 / 75 ms | 727 / 36 ms | 592 / 60 ms | 836 / 32 ms (all 27 rendered withheld) |
| a357d187 | 180 / 107 ms | 116 / 75 ms | 100 / 61 ms | 100 / 66 ms | 100 / 64 ms |

- 9f40a6fc root-started commands: 100, with 80 judged paths holding spaces and 27 withheld; this round fixed that.
- TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers at -count=10: 40×20 and 10×100 judgements, every run.
- Suites run with -p 2: internal/rehydrate ok; internal/hostperm ok; internal/mcp ok (123 s); internal/daemon -run 'Rehydrat|WireRehydrator' ok.
  - Fix: Coordinator decision: either rule a size cap for pathgate.go or accept its size explicitly. Further free-text grammar should be a recorded limit unless it closes a leak in a spelling D61 names. Candidate cuts carried from round 2: fold cutRoot, rootPrefix and cutValueWithheld into one helper; fold the three quote readers.
- **nit** `internal/daemon/rehydrate_service.go:763-766; internal/rehydrate/pathgate.go:521 (rootedStretch doc); docs/adr/0011 §23 item 10 (line 973)`: The adapter comment says a root-started summary hands the host 'only its path part, never a command's arguments', and rootedStretch's doc says 'never their arguments'. The stretch runs to the last word that holds a separator, so arguments and Grep patterns do reach hostperm whenever a later word holds `/` or `\`. The ADR records only the `-o out\x` case. Also, the cost row's 'no judged path holds a space' assertion checks only its own fixture, whose commands have no later word with a separator.
  - Evidence: Real adapter, UAT-12 rules, at HEAD.
- Grep preview `<root>\internal //nolint:errcheck`: judged as `<root>\internal //nolint:errcheck`.
- `<root>\internal https://example.com/x`: judged whole.
- `<root>\tools\run.ps1 -Since HEAD~1 -Out out\x`: judged whole, and withheld (the 8.3 refusal).
- Grep preview `<root>\src HEAD~1/x`: judged whole and withheld. This case is not covered by the recorded `-o out\x` example.
  - Fix: Reword the adapter comment, the rootedStretch doc and ADR item 10: the host gets the stretch from the root through its last word that holds a separator, which may include arguments. Add `<root>\src HEAD~1/x` (a Grep pattern holding a separator) to the §23.2 over-withholding example.
- **nit** `internal/rehydrate/items.go:1281-1307 (restorableRules); internal/daemon/rehydrate_hostpaths_test.go:263 (costBuild has no Rules)`: Item 6a now asks the host once for each rule file the scanner returns. This adds a term to D61(4)'s bound ('structured summaries + file pointers'). The HostPaths doc and ADR item 10 state the new term, but no row measures it: costBuild sets no Deps.Rules, so item 6a returns before pathJudgeFor. needs_owner lists item 6a's no-drop choice, but not the added cost term as a D61(4) deviation.
  - Evidence: costBuild builds rehydrate.Deps{HostPaths: …} with Rules nil. TestBuild_AnInstructionFileTheHostRefusesIsNeverRestored checks only what is rendered, never how many judgements were made.
  - Fix: Add a cost-row variant with a scanner that returns K rule files, through fakeScanner or the real rules scanner over K .claude/rules files, and assert 20 + K judgements. Record the added term as a D61(4) deviation in needs_owner and the criterion changes.
- **nit** `internal/rehydrate/pathgate.go:1249 (glued-quote sibling check); :1082 (stripQuotes keeps a caret before a separator)`: Two side effects of this round's F2 and F6 fixes, both low likelihood.
(a) Over-withholding: the glued-quote check reads a prose apostrophe earlier in the text as an open quote. A prompt such as `Don't edit "<root>" files` is now withheld as a sibling; it was shown at 9f40a6fc.
(b) Leak: a caret kept before a separator is not a path start. cmd's `type ^\Users\x\.ssh\id_rsa` (`^\` is `\`) is now shown; it was withheld at 9f40a6fc.
  - Evidence: Rehydrate probe, root C:\q\proj.
- `Don't edit "C:\q\proj" files`: HEAD withheld, 9f40a6fc shown.
- `edit "C:\q\proj" files`: shown at both.
- `type ^\Users\someone\.ssh\id_rsa`: HEAD shown, 9f40a6fc withheld.
- `type C:^\Users\…` and `type private^\deny.txt`: withheld at both.
  - Fix: (a) In the quote readers used for the sibling check, ignore an apostrophe that stands between two ASCII letters (a contraction), as ownQuote already does for the root's own apostrophe. Or record this as accepted over-withholding.
(b) In outsideIn, let a separator that follows a word-start `^` start a token, as flagGlued does for `-X`; regexLike still exempts `^\s*func\b`. Or record this as a limit.

## fix:rehydrate:r3: status `done`, head `e65ada8f7ae3beea383f5309673d2e1a5482a09c`

### Root cause

Each finding had its own cause:
- **F1:** varRef spelled PowerShell's drive name only in lower case (`$env:`).
- **F2 and F9:** round 2's narrowing (deviation 1) matched literals only where a name starts. The screen forms still left a name character glued before the name in three cases: bash's `$` before a quote; `\"`, which stripQuotes read as a POSIX escape in every form; and character codes it never decoded.
- **F3:** outsideIn and homeOrVarPath started a path only after ASCII characters.
- **F4:** truncatedWithheld read only the raw text and required an ASCII boundary before the prefix.
- **F5:** after the glued quote run, protect checked only for a plain space.
- **F6:** buildSkillIndex never consulted the build's judge.
- **F7:** rootCover compares a rule only with the root as the request spells it, and never sees the aliases the host resolves.
- **F8:** recordedPath noted any rooted one-word value.
- **F10 and F16:** protect read the quote state from the held text, where each earlier root spelling had become a single mark and lost its quotes.
- **F11:** stripQuotes kept `^` before `\`.
- **F12:** option 2 of round 2's F9 dropped every structured check for multi-valued JSON, the zero-cost glob check included.
- **F14:** an open quote always made 'root followed by a space' a sibling.
- **F15:** regexLike counted the glob characters `*` and `?`, and a name's `+` and `$`, as regex signatures.
- **N1:** a reason's path was read word by word, so a gitdir named like the root plus a space, or a single-segment one, passed.
- **Performance:** my first F3 cut started a scan after every non-ASCII byte, which is quadratic on a long word. Fixed with pathLead before commit.

### Summary

PRODUCT CODE CHANGED

# Wave 19c rehydrate, fix seat round 3: all 16 findings and all 7 nits handled; nothing pushed

Branch closeout/w19-rehydrate is at e65ada8f, two commits on top of 108f8cd5. The worktree is clean.

**Product code changed:**
- internal/rehydrate/pathgate.go: the screen.
- internal/rehydrate/items.go: item 6b, the skill index.
- internal/daemon/rehydrate_service.go: one comment.
- internal/hostperm and internal/checkpoint: not touched.

## Red-first evidence
- New rows:
  - 14 rehydrate rows in the new file internal/rehydrate/summary_screen_edges_r3_test.go.
  - 2 daemon rows in the new file internal/daemon/rehydrate_hostpaths_r3_test.go. Both go through the real adapter; one uses a real junction or symlink.
  - A new variant of the daemon cost row: "instruction and skill files".
- How I showed they fail on the old code: I extracted 108f8cd5 into a scratch directory (`git archive`) and added the new test files.
  - Every new row failed there, each for the reason the review gave.
  - In the cost row only the new variant failed: 25 judgements instead of 30, because item 6b judged no skill file.
- All of them pass on the fix.

## Review resolution

### Fixed
**F1 (major): `$Env:` and `$ENV:` were not recognised.**
- PowerShell ignores case in the drive name, but varRef matched only lower-case `env`. It now matches any case.
- Row: TestBuild_AnEnvDriveSpelledInAnyCaseIsWithheld. The review's six spellings and `${Env:…}` are withheld.

**F2 and F9 (major, the same defect): round 2's "where a name starts" rule lost spellings.**
- Spellings that leave a character glued before the denied name were shown: `$'deny.txt'`, `$"…"` and `$''`, cmd/PowerShell `"private\"deny.txt`, `/`, `\x2f`, `\057`, `%252F`.
- The screen now reads these extra forms:
  - bash's `$` before a quote is removed;
  - a form where every backslash is a separator (the old form is kept as well);
  - a form with `\uXXXX`, `\xHH` and `\NNN` decoded, repeated until nothing changes;
  - percent-decoding repeated until nothing changes.
- Row: TestBuild_AnANSICQuoteOrAnUndecodedEscapeNeverHidesARuleLiteral. It covers both reviews' spellings, the `John\'s notes.txt` case, and the recorded-file-pointer case. The allowed spellings stay shown.
- ADR item 7(a) now states the invariant correctly.

**F3: an outside path after a typographic quote or a Unicode space was shown.**
- outsideIn now starts a path after any non-ASCII byte.
- homeOrVarPath's leading class now includes non-ASCII characters.
- A drive also starts after `!` or `*` (nit).
- I did not fold Unicode spaces in sanitize, as the review suggested. It is not needed for any of these spellings, and it would split a Read path that holds a no-break space (NBSP). The host would then judge only the part before the space.

**F4: a cut inside an encoded name showed its prefix.**
- truncatedWithheld now also reads the cut part's decoded JSON string and every percent-decoded form.
- A name prefix now counts after the same starts namedAt accepts: a non-ASCII byte, a short option, or `$'`.

**F5: a sibling hid behind a closing quote plus an escaped space.**
- Spellings like `"<root>"\ old`, a backtick-space or a caret-space are now siblings.

**F6: item 6b ignored the build's judge.**
- buildSkillIndex now drops every skill whose SKILL.md the build withholds, in the same way restorableRules does for item 6a.

**F7: a rule over the whole project, written against the resolved path of a linked root, did not withhold free text.** I took the reviewer's option 2.
- While any rule anchored outside the project is in force, the build asks the host once about `qompack-rehydrate-root-probe` below the root. If the host refuses it, every free text is withheld.
- I chose this over option 1 because hostperm also resolves an 8.3 root and a rule written through a link, which an EvalSymlinks list of roots would miss.
- Project-relative rules never trigger the probe, so no existing exact-count row changed.
- Rows: the in-package alias row, which asserts 1, 1 and 0 probes for its three rule sets, and the daemon junction row.

**F8: a rooted word that is not a path poisoned unrelated free text.**
- A drive-less value is no longer noted as a withheld name when it names no path on the platform:
  - on Windows, any such value;
  - on POSIX, one led by a backslash, or with a single segment.
- The value itself is still withheld.

**F10 and F16: the quote state at a later root spelling lost an earlier spelling's quotes.**
- The quote state now has its own builder.
- `type <P>"proj\a.txt" & cd <root> && dir` is now shown.

**F11: cmd.exe's caret-escaped backslash hid outside paths.**
- A caret is now kept only before `/`.

**F12: a glob among several path-named JSON values was never checked against withheld paths.**
- Such a value is now marked `selects`, and a glob is judged with globSelectsKnown. This asks the host nothing; the row asserts the asked map is still `{private/deny.txt: 1}`.

**F14: a nested shell that cd's into the root was withheld.**
- Inside quotes, the root followed by `&&`, `|`, `||`, `;`, `<` or `>` now ends the root's word.
- A lone `&` does not, because Windows folders are often named `A & B`.

**F15 (major): drive-less glob paths were read as regular expressions.**
- `*` and `?` are no longer regex signatures.
- I also took the review's optional step: a name's `+` and `$` are no longer signatures. Only `.+` and a closing `$` count. So `notes+old.txt`, `c++` and `$Recycle.Bin` read as paths and are withheld.

**Nits:**
- N1: a drop reason's operation path (`open <path>`, `CreateFile <path>`) is judged whole.
- N2, N4, N5: comments and docs now say a root-started summary's path part may include an argument that holds a separator. The ADR §23.2 example now includes `<root>\src HEAD~1/x`. The cost row's comment and its assertion message are narrowed to the fixture.
- N6: the new cost variant measures 20 + 2×5 judgements.

### Rebutted or recorded rather than fixed
- **N3, the `[]` part:** I kept `[` and `]` as regex signatures. Removing them would withhold `\[DEBUG\]`, a common Grep pattern. A drive-less glob class is recorded as a limit.
- **N7(a), a contraction's apostrophe before a quoted root:** recorded as accepted over-withholding. Changing the quote readers would flip other sibling decisions. N7(b) is fixed by F11.
- **F13:** these are owner rulings and are listed under needs_owner. I reran the reviewer's 117-preview corpus on the fix. Counts are identical to 108f8cd5 in all seven scenarios, and the combined builds match the isolated ones.
- **F17:** pathgate.go has grown again. A size cap is the coordinator's call.

| Revision | Non-comment lines | Funcs | Branch ops | Regexps |
|---|---|---|---|---|
| 108f8cd5 | 1,299 | 83 | 493 | 9 |
| Now | 1,401 | 91 | 556 | 11 |

### A performance defect I introduced and fixed in the same round
My first version of F3 started a path scan after every non-ASCII byte. That is quadratic on a long non-ASCII word, and the host-ceiling property test went from about 4 s to 253 s. A scan now starts after a non-ASCII byte only where a path can begin (pathLead), and the test is back to about 4 s.

### Scratchpad collision
The reviewers share this session's scratchpad. My Write call overwrote their `scratchpad/r3/zz_probe_test.go` and `scratchpad/r3/overlay.json`. Their findings carry the evidence. My files are in `scratchpad/w19c-r3fix/`.

## Docs
- **ADR 0011 §23:**
  - "What changed" now covers round 3.
  - Item 2's limits are updated.
  - Items 6 to 10 are updated, including the corrected invariant in 7(a), the root probe in 7(d), the cost terms in item 10, and the evidence list.
- **docs/security.md:** updated to match.
- **A harness gotcha:** the Edit tool decodes `/` to `/`. I restored every affected spelling with a script written by the Write tool, and checked them with grep.

## Checks
- All passed: vet for GOOS windows, linux and darwin; golangci-lint; fmt-check (after gofumpt on the new test file); test/docs; docmarkers; runpatterns; and the three gen-*-docs `--check`.
- **Windows, full packages:** rehydrate ok (7.6 s); daemon ok (280.7 s).
- **Windows, focused:** each of the 17 new or changed rows passed with `-count=20` and with `-race -count=3`.
- **Linux, non-root qompack-test, `-p 2`, at e65ada8f:** rehydrate 356 pass; daemon 1695 pass, 1 skip.
- **Linux race:** the 17 rows with `-race -count=3` and CGO on: 17 ok, 0 race logs.
- **Container:** it was stopped when I began. I started it, checked that only `sleep` was running at the end, and stopped it again.

### Commits

- 5011c604 fix(rehydrate): close the w19c round-3 review's screen gaps
- e65ada8f docs(adr): record the w19c round-3 review's screen fixes

### Tests

- `GOOS=windows|linux|darwin go vet ./internal/rehydrate/ ./internal/daemon/`: pass (re-run after the last edit)
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/rehydrate/... ./internal/daemon/...`: exit 0, no findings
- `go run ./tools/devtool fmt-check`: exit 0 (after gofumpt -w on summary_screen_edges_r3_test.go)
- `go test -p 2 -count=1 ./test/docs; go run ./tools/devtool lint --only=docmarkers,runpatterns; go run ./tools/devtool gen-config-docs --check (and gen-command-docs, gen-mcp-docs)`: ok; PASS runpatterns, PASS docmarkers; all three generated docs up to date
- `go test -p 2 -count=1 ./internal/rehydrate/ (Windows, at HEAD)`: ok 7.6s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/ (Windows)`: ok 280.7s
- `go test -p 2 -count=1 -run '^TestBuild_NeverExceedsTheHostCeiling$' ./internal/rehydrate/`: ok 3.95s after the pathLead fix (253s with the first F3 cut, fixed before commit)
- `for t in TestBuild_AnEnvDriveSpelledInAnyCaseIsWithheld TestBuild_AnANSICQuoteOrAnUndecodedEscapeNeverHidesARuleLiteral TestBuild_AnOutsidePathAfterATypographicQuoteOrAUnicodeSpaceIsWithheld TestBuild_ACutInsideAnEncodedOrQuotedNameNeverShowsItsPrefix TestBuild_AQuoteThenAnEscapedSpaceAfterTheRootNamesASibling TestBuild_ASkillTheHostRefusesIsNeverIndexed TestBuild_ARuleOverTheProjectThroughAnAliasWithholdsEveryFreeText TestBuild_ARootedWordThatIsNoPathPoisonsNoFreeText TestBuild_AnEarlierSpellingOfTheRootKeepsItsQuotes TestBuild_ACaretEscapedSeparatorIsASeparator TestBuild_AGlobInAMultiValuedJSONPreviewIsJudgedByWhatItSelects TestBuild_ANestedShellThatChangesToTheRootIsShown TestBuild_ADriveLessGlobOutsideTheProjectIsWithheld TestBuild_AReasonNamingAPathAfterAnOperationIsRedacted; do go test -p 2 -count=20 -run "^$t\$" ./internal/rehydrate/; go test -p 2 -race -count=3 -run "^$t\$" ./internal/rehydrate/; done (Windows)`: all 28 runs ok
- `for t in TestRehydrateHostPaths_EscapedAndCaseFoldedSpellingsAreWithheldUnderTheUAT12Rules TestRehydrateHostPaths_ARuleOverTheProjectThroughALinkWithholdsEveryFreeText TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers; do go test -p 2 -count=20 -run "^$t\$" ./internal/daemon/; go test -p 2 -race -count=3 -run "^$t\$" ./internal/daemon/; done (Windows)`: all 6 runs ok
- `red-first: git archive 108f8cd5 into scratch plus the two new test files and the changed rehydrate_hostpaths_test.go; each of the 17 rows above run by exact name (-run "^$t\$")`: all 17 red on 108f8cd5, each for the reported reason. In the cost row only the new variant failed (expected 30, actual 25).
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w19c-rhr3 --out <scratch>/w19c-r3fix/linux e65ada8f touched --no-race --gomaxprocs 2 -- ./internal/rehydrate ./internal/daemon`: PASS as non-root qompack-test (uid 10001): rehydrate 356 pass; daemon 1695 pass, 1 skip
- `Linux run clone cx-w19c-rhr3-touched-e65ada8-20261004T031327Z, CGO_ENABLED=1, as qompack-test: go test -p 2 -race -count=3 -run "^$name\$" ./<pkg>/ for each of the 17 rows above`: 17 ok, race_logs=0
- `usefulness: the reviewer's scratch corpus (117 store previews, 7 rule scenarios) through the real adapter via go test -overlay on the daemon package`: isolated shown/withheld identical to 108f8cd5 in every scenario (112/5, 106/11 three times, 105/12 twice, 110/7); combined builds match the isolated ones

### Criterion changes

- TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers:
- New variant 'instruction and skill files': 5 `paths:` rule files and 5 skills through the real rules.New() and skills.New(); it expects 20 + 2×5 judgements.
- costBuild gains an `instructions` parameter.
- The earlier variants' expected counts are unchanged.
- One assertion message is narrowed to the fixture (nit N4); its check is unchanged.
- D61(4)'s bound gains one judgement, the root probe, while a rule anchored outside the project is in force. No exact-count row changed, because every one of them uses only project-relative rules.
- D61(2)(b) narrowed (F8): a drive-less value that names no path on the platform is no longer noted.
- regexLike narrowed (F15), in the direction of withholding more: `*`, `?`, a name's `+` and a non-closing `$` are no longer regular-expression signatures.
- New rows only: 14 in internal/rehydrate/summary_screen_edges_r3_test.go and 2 in internal/daemon/rehydrate_hostpaths_r3_test.go. TestBuild_AnANSICQuoteOrAnUndecodedEscapeNeverHidesARuleLiteral splits its spellings over two builds so that section 6's budget share holds every pointer.
- No earlier row's assertion changed. Every round 1, round 2 and earlier round-3 row passes unchanged on Windows and Linux.

### Open issues

- Carried: TestObservePrompt_PassiveModeInvokesSeamButEmitsNothing (internal/daemon/daemon_test.go, not in my files) depends on the wall clock. Its seam runs on a goroutine, and the reply wait is bounded by promptReplyBudget on the real clock. It needs an injected clock or a seam-completion channel from whoever owns daemon_test.go. It passed in this round's Windows and Linux whole-package runs.
- Carried: on Linux, d5487f74 alone fails TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened, because its fixture spelling was Windows-only (fixed in 9f40a6fc). A Linux bisect should treat d5487f74 as known red for that row.
- Carried, recorded limit: ToolPointer carries no tool name. Two shapes are free text only, so a link or an 8.3 name in them is not resolved:
- a relative Glob or Grep preview of several words;
- a JSON preview with several path-named values (a glob among those values is now judged by what it selects).
A root-started summary is judged through its path part only.
- Carried, recorded limit: an unquoted, unescaped sibling named '<root last segment> <more>' (`proj - Copy`) is shown in free text.
- Carried: drop reasons are not screened for rule literals or bare basenames (ADR 0011 §23.2; docs/security.md names this).
- Carried: a rule literal of one or two characters withholds most free text (accepted under D61).
- Carried: the one-word Grep preview `HEAD~1` is withheld on Windows under any Read rule. It is judged as a structured value, and hostperm refuses `~1` as an 8.3 name it cannot resolve. See the F13 owner option.
- Accepted over-withholding, recorded in ADR §23.2. Carried:
- a URL query read as drive-relative (`?q=a:b`);
- `dir /s/b`;
- two-segment library ids and container paths;
- mentions of a rule literal where a name starts;
- a root-started path part holding an 8.3-shaped word;
- the root spelled without its own apostrophe.
New this round:
- a quoted root followed by a word that is no shell operator, a lone `&` included (`git commit -m "move tests under <root> root"`, `cmd /c "cd /d <root> & go test"`);
- a contraction before a quoted root (`Don't edit "<root>" files`);
- a backtick that closes a command substitution right after the root and before a space;
- on Windows, every free text while any rule names an 8.3 name after a glob and an anchored rule is in force (hostperm refuses the root probe, a path that does not exist);
- operationOutside may redact a reason part of the form `<word> /<x>` that is not a path.
- Recorded limits, narrowed this round:
- regexLike still reads a drive-less path as a regular expression when it holds `( ) { } | ^ [ ]` or `.+`, ends in `$`, or has a segment that starts with a class letter before a non-letter (`\data\s_1\x`, `\$Recycle.Bin\S-1-5-21\x`).
- `\U` codes, HTML entities and base64 are not decoded.
- A name glued to a cmd variable (`%D%deny.txt`) is not matched.
- F17 size: pathgate.go is 1,401 non-comment lines, 91 funcs, 556 branch operators and 11 regexps, against 1,299 / 83 / 493 / 9 at 108f8cd5. No size cap has been ruled.
- Scratchpad collision: the reviewers share this session's scratchpad, and I overwrote their scratchpad/r3/zz_probe_test.go and scratchpad/r3/overlay.json. Their findings carry the evidence. My probes and logs are in scratchpad/w19c-r3fix/.
- macOS: only go vet ran (GOOS=darwin). There was no macOS test run and no hosted CI run this round.

### Needs owner

- Carried: the only deviation from 'internal/hostperm (revert only)' is the read-only accessor RuleSet.ReadRulePatterns (internal/hostperm/patterns.go). It is unchanged.
- Carried: minCutPrefix = 3 bytes (D61's 'length >= 3'). It is also the shortest withheld basename that free text is screened for.
- Carried and new cost-fixture constants:
- carried: rehydrateCostFiles = 10, rehydrateCostReads = 10, maxCostJudgements = 20;
- new: rehydrateCostInstructions = 5. Derivation: any K >= 1 catches a build that judges none of them; 5 also tells 'rule files only' (25) and 'each twice' (40) apart from the correct 20 + 2×5 = 30.
- New fixed names and lists:
- rootProbe = "qompack-rehydrate-root-probe". It has no extension, no leading dot and no 8.3 shape, so no rule written for files is likely to name it.
- escapeCode decodes `\uXXXX`, `\xHH` and `\NNN`, the JSON, JavaScript, Python, C and bash ANSI-C spellings.
- operatorAt accepts `&&`, `|` (and so `||`), `;`, `<` and `>`, and deliberately not a lone `&`.
- regexLike's signatures are now `{ } ( ) | ^ [ ]`, `.+`, a closing `$`, and the class letters `bBdDsSwW` before a non-letter or the end.
Carried: homeVarName, devicePaths, jsonGrammar.
- D61 deviation 1, restated (D61(2)(a) 'contain' narrowed to 'where a name starts'): round 2's evidence did not hold. That narrowing lost bash `$'…'`, cmd/PowerShell `\"` and undecoded character codes until this round's extra screen forms restored them. The owner should accept the narrowed rule with those forms, or rule a return to plain containment for whole-segment literals.
- D61(4) deviation 4, new (review F7): while a rule anchored outside the project is in force, a build makes one extra host judgement, the root probe. The bound becomes structured summaries + file pointers + instruction and skill files + 1. I chose option 2 over option 1 (handing the build EvalSymlinks-resolved roots) because hostperm also resolves an 8.3 root and a rule written through a link, which option 1 would not see.
- D61(4) cost terms (nit N6): rule files (item 6a, since round 2) and skill files (item 6b, this round) cost one judgement each. The new 'instruction and skill files' cost variant measures this.
- D61(2)(b) narrowed (review F8): a drive-less value that names no path on the platform is not noted as a withheld name. On Windows that is any such value; on POSIX, one led by a backslash or of a single segment.
- Items 6a and 6b: a rule file or skill file the judge withholds is neither restored nor counted as a drop. With rules that cannot be established, both items restore and name nothing. The alternative is one '(path withheld)' count entry per item.
- Owner options from review F13, not taken:
- (i) skip the literal of an anchored home or root rule that rootCover proves refuses nothing in the project;
- (ii) screen D61(2)(b) basenames only for withheld paths inside the project;
- optionally, do not send a one-word relative value with no separator (such as `HEAD~1`) to the host.
The corpus shows these as the remaining over-withholding: the literal `config` in six summaries under ~/.kube/config, and module-cache basenames.
- F17: the coordinator should rule a size cap for pathgate.go or accept its size. Candidate cuts carried from round 2: fold cutRoot, rootPrefix and cutValueWithheld into one helper; fold the three quote readers.

## verify:rehydrate: verdict `needs-fixes`, 3 finding(s)

- **major** `internal/rehydrate/pathgate.go:1521-1526 (outsideIn: `p[i-1] >= utf8.RuneSelf && pathLead(p, i)`), :1560-1563 (pathLead), :450 (homeOrVarPath leading class `[^\x00-\x7f]`); docs/adr/0011 §23 item 7(c) lines 929-931`: This round's fix for F3 introduced a regression. A path now starts after any non-ASCII byte, not only after a typographic quote or a Unicode space. So an in-project path with a segment that ends in a non-ASCII letter and more segments after it is read as a POSIX absolute path outside the project. Examples are `café/sub/x.txt` and CJK names such as `文档/设计/说明.md`. D61(2)(c) requires an in-project absolute path to be shown. Here an in-project absolute Read preview is withheld, and newPathJudge then notes it as a withheld path. Its basename then withholds unrelated free text in the same build, which is the poisoning D61(2)(b) forbids. Meanwhile the file pointer for the same file is shown. For any project with CJK directory names, every relative or absolute command naming a file two levels below such a directory is withheld. No recorded limit covers this.
  - Evidence: Read-only overlay probes at HEAD e65ada8f, compared with a git-archive copy of 108f8cd5. Root C:\q\proj.
- Probe judge (summaryWithheld), no rules and also UAT-12 rules. Withheld at HEAD, shown at 108f8cd5:
  - `cat docs/café/x/menu.md`
  - `cat 日本/設定/x.txt`
  - Read preview `C:\q\proj\café\sub\x.txt`
  - Read preview `C:\q\proj\src\日本\設定\x.txt`
  - `cat C:\q\proj\café\sub\x.txt`
  - `cd C:\q\proj && cat café\sub\x.txt`
  - `{"file_path":"C:/q/proj/café/sub/x.txt"}`
  - `{"query":"café/sub/x"}`
  Controls shown at both commits: `données/sub` (ends in an ASCII letter), `über/a/b.go` (non-ASCII at the start).
- Full Build, file pointer café/sub/notes.txt plus the Read preview `C:\q\proj\café\sub\notes.txt`:
  - At HEAD the preview is withheld, and so are `cat notes.txt` and `grep -n TODO notes.txt`. The file pointer itself is shown.
  - Without the Read preview, both commands are shown.
  - At 108f8cd5 all of them are shown.
- Full Build under the UAT-12 rules: `C:\q\proj\文档\设计\说明.md`, `cat 文档/设计/说明.md` and `git add 文档/设计/说明.md` are withheld at HEAD and shown at 108f8cd5.
  - Fix: 1. Start a path after a non-ASCII character only when that character is a Unicode space or quotation punctuation. Decode the preceding rune with utf8.DecodeLastRuneInString(p[:i]) and accept it when unicode.IsSpace or unicode.In(r, unicode.Pi, unicode.Pf, unicode.Ps, unicode.Pe) holds; never accept a letter.
2. Make the same change in homeOrVarPath's leading class, using `[\p{Z}\p{Pi}\p{Pf}\p{Ps}\p{Pe}]` in place of `[^\x00-\x7f]`.
3. Add rows that are shown, and that note nothing: the in-project Read previews `<root>\café\sub\x.txt` and `<root>\文档\设计\说明.md`, `cat docs/café/x/menu.md`, `cat 文档/设计/说明.md`, and `cat notes.txt` beside a Read of `<root>\café\sub\notes.txt`. Keep TestBuild_AnOutsidePathAfterATypographicQuoteOrAUnicodeSpaceIsWithheld green.
4. Correct ADR item 7(c)'s 'or a non-ASCII character' to match.
- **major** `internal/rehydrate/pathgate.go:780-786 (textWithheld reads D61(2)(c) from stripQuotes(held), with no dollarQuote), :805 (dollarQuote applied only inside screenForms, i.e. for (a)/(b)), :1540 (pathStartAfter has no `$`), :450 (homeOrVarPath prefix class has no `$`); docs/adr/0011 §23.2 lines 719-722; docs/security.md`: Round 3 fixed bash ANSI-C (`$'…'`) and locale (`$"…"`) quoting only for rule literals and withheld names (item 7(a)/(b)). The absolute, home, drive-relative and `..` reading (item 7(c)) still sees `$/etc/passwd` once quotes are removed. `$` is not a path start, and it is not in homeOrVarPath's prefix class. So a command that spells an out-of-project path in bash's own quoting shows that path in section 6. One such command is the idiomatic way to quote an apostrophe: `cat $'/home/u/John\'s notes.txt'`. D61(2)(c) requires every one of these to be withheld. ADR §23.2 says the screen 'undoes quotes, bash's `$'…'` and `$"…"`'. docs/security.md now says the same next to the absolute-path clause. No recorded limit covers these spellings. They are pre-existing (shown at 108f8cd5 too), but this round touched exactly this quoting and closed only half of it.
  - Evidence: Full Build (overlay probe) under the UAT-12 rules, root C:\q\proj. All of these are SHOWN at HEAD e65ada8f and at 108f8cd5:
- `cat $'/etc/passwd'`
- `cat $'/home/u/John\'s notes.txt'`
- `cat $'~/.ssh/id_rsa'`
- `cat $"$HOME/.aws/credentials"`
- `type $'D:secret.txt'`
- `cat $'../other/x.txt'`
- `cat $"/etc/passwd"`
Controls, withheld: `cat /etc/passwd`, `cat '/etc/passwd'`, and `cat $'C:\Users\x\secret.txt'`, which is caught only through the `:` start.
  - Fix: 1. Apply dollarQuote before stripQuotes wherever item 7(c) is read: in textWithheld (`p := stripQuotes(dollarQuote.ReplaceAllString(held, "$1"))`), and likewise in valueWithheld and reasonWithheld.
2. Alternatively, add `$` to the characters a path and homeOrVarPath may follow when a quote comes right after it.
3. Add the seven spellings above as withheld rows, in the rehydrate package and through the real adapter. Keep `echo $'done'` and `git log --format=$'%h %s'` shown.
- **minor** `internal/rehydrate/pathgate.go:590-600 (regexLike, narrowed for F15); docs/adr/0011 §23.2 (over-withholding list, lines 729-750)`: Dropping `*`, `?` and `+` as regular-expression signatures was justified only for drive-less glob paths. But it also turns common escaped-operator searches into 'POSIX absolute paths of two segments', so they are withheld. This is an over-withholding regression from 108f8cd5. Neither the ADR's accepted over-withholding list nor its regexLike limit records it, and the round's corpus had no such pattern. Affected searches include nullish coalescing (`\?\?`), optional chaining (`\?\.`), Markdown bold (`\*\*…\*\*`) and `\+\+`. They are withheld as a one-word Grep preview and inside a command.
  - Evidence: Overlay probe (summaryWithheld) under the UAT-12 rules. Withheld at HEAD, shown at 108f8cd5:
- `\?\?`
- `\?\.`
- `\*\*bold\*\*`
- `\+\+`
- `rg -n "\?\?" src/`
- `rg -n "\*\*Note\*\*" docs/`
- `grep -rn "\+\+i" src/`
The one-word forms are not noted (F8 holds), so they do not poison other summaries.
  - Fix: Either:
- count as a regex a backslash-led word whose first segment is a lone metacharacter (`\?`, `\*`, `\+`). A drive-less path never starts `\*\` or `\?\`, and `\Users\x\.ssh\*` still reads as a path. Add the spellings above as shown rows.
- or record them in ADR §23.2's accepted over-withholding list.

