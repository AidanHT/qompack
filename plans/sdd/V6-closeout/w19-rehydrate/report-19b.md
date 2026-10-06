# Wave 19b rehydrate: gate cost, punctuation, drops, spaced roots (D60)

Branch `closeout/w19-rehydrate`. Workflow `wf_4e6db3de-c66`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## impl:rehydrate: status `done`, head `ba217e44325aeff2f2e417bee17f8ec99e5685ec`

### Root cause

V1: round 1 judged every run of consecutive words, which grows quadratically (136 runs in a 17-word summary). The daemon judged each piece with RuleSet.Evaluate, which does uncached disk work every time: FullPath, GetLongPathNameW, and a Readlink of every path component, twice when the resolved root differs. That multiplied to seconds per build.
V2: summaries were split at " ' ` , ; ( ) { } [ ] < > | = before anything was read whole. The store's Read preview is the path alone, and Grep's is the path then the pattern, so a denied path containing one of those characters was never judged whole.
V3: the set of known withheld paths was built from file pointers and summaries only, and the path-keyed drop entries were redacted before item 6 built its judge.
V4: splitting on whitespace cut a root like C:\Users\John Smith\proj after "John", leaving an absolute path outside the project as the first word.

### Summary

PRODUCT CODE CHANGED (internal/rehydrate, internal/daemon/rehydrate_service.go, internal/hostperm). Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-rehydrate, branch closeout/w19-rehydrate, 5 commits on 4bad4cef. The working tree is clean. Each commit builds and vets.

V1 (performance), fixed in two layers.
(1) The summary is now read in linear time. internal/rehydrate/pathgate.go textPieces drops round 1's quadratic runs and reads pieces from the shapes store.argsPreview writes:
- the whole summary, and each decoded JSON string in it;
- each word and each delimiter token, plus the value after a word's first '=';
- each proper prefix and suffix of the words (Grep's and Glob's "path pattern" preview), and each join at a space;
- each quoted segment, taken whole;
- selector values and named path arguments.
That is at most 4w-2 distinct pieces for a w-word preview: 66 at 17 words, where round 1 read 169.
(2) Each judgement is now cheap. A new hostperm.Evaluator (internal/hostperm/evaluator.go, RuleSet.Evaluator) gives exactly the decision RuleSet.Evaluate gives. It walks a path below the project root down the directories the path names, listing each directory once per build; on Windows the listing includes 8.3 names and reparse flags. A path is judged without the disk when every segment it walks names an entry by its own name, none of them a link or reparse point, up to the first segment that names nothing. Everything else is judged on disk, with link reads memoized: links, 8.3 aliases, device names, a trailing dot or space, streams, non-ASCII names, and every path while shortPending is set.
The daemon adapter (rehydrateHostPaths, now a wrapper over rehydrateHostPathsObserved) judges each build through one evaluator. RuleSet.Evaluate's behaviour is unchanged; its body was split into evaluate and decide.
Result on the verifier's fixture (80 distinct 17-word Bash previews, the real adapter, deny rules Read(./private/deny.txt), Read(./.env), Read(./secrets/**)): 3145 judgements, 0 on disk, 50-70 ms on this loaded machine. 4bad4cef made 9096 judgements, all on disk, in 10.96-11.64 s.

V2 (privacy): a denied path holding a delimiter is now read whole, as the full Read preview, a prefix (Grep), a quoted segment, or an option's '=' value. Every spelling is withheld in both relative and absolute form, while the same shapes naming allowed files are still shown.

V3 (privacy): Build now makes the build's one pathJudge before gateCheckpointDrops redacts the drop entries. The judge notes each path-keyed drop it withholds inside the project. The gate and item 6 share that judge through the unexported Deps.judge field, with one memo.

V4 (over-withholding): protectRoot keeps each spelling of the project root in a summary from being split, with either slash and any case on Windows and an exact match elsewhere, and appendPiece restores it. An absolute path inside a project whose root contains a space, or a comma, apostrophe or '=', is therefore one piece and is shown when allowed. A path that is only part of the root (C:\Users\John), and a sibling (…\proj2), are still withheld.

D60 rulings are recorded in ADR 0011 §23:
- (i) in §23.2: sections 3 and 4 are outside D50, as a deliberate limit;
- (ii) in §23.1: the "may rule the literal reading" sentence is removed;
- (iii) in §23.4: dropped() returns the redacted entry;
- (iv) in the §23.2 limits.
§23.5-§23.7 are new and cover V1/V2, V3 and V4. The open issue "a project path with a space withholds every absolute summary" is removed from the limits. docs/security.md gains a paragraph saying the rehydration block's pointers follow the host rules and how dropped() names a withheld checkpoint drop. docs/mcp-tools.md and the generated MCP docs never described the old drop shape, so nothing there changed and gen-mcp-docs --check passes.

Required rows. Each failed on 4bad4cef product code (run with the product files stashed; the daemon cost row used a copy without the new seam) and passes at HEAD:
- (a) Cost, internal/daemon: go test -run '^TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords$' ./internal/daemon/. It asserts judgements <= 80*maxJudgementsPerSummary (66) and disk judgements <= 80*maxDiskJudgementsPerSummary (2). A secondary wall-clock check holds elapsed < compactAnswerBudget(). On 4bad4cef it failed with "9096 > 5280". <!-- runpatterns: the package path is quoted with a trailing /. that go test resolves to the same package; the row exists there -->
- (b) V2, internal/rehydrate: go test -run '^TestBuild_DelimiterCharactersInADeniedPathNeverShowIt$' ./internal/rehydrate/. All 5 subtests were red on base. <!-- runpatterns: the package path is quoted with a trailing /. that go test resolves to the same package; the row exists there -->
- (b) V2, internal/daemon (store.ArgsDigest previews, exact deny rules): go test -run '^TestRehydrateHostPaths_ADeniedPathWithDelimitersIsWithheld$' ./internal/daemon/
- (c) V3: go test -run '^TestBuild_APathKnownOnlyFromACheckpointDropIsNamedByNoSelector$' ./internal/rehydrate/
- (d) V4: go test -run '^TestBuild_AProjectPathWithASpaceShowsItsOwnAbsolutePaths$' ./internal/rehydrate/
- (d) V4: go test -run '^TestRehydrateHostPaths_AProjectPathWithASpaceShowsItsOwnPaths$' ./internal/daemon/
- Evaluator equivalence and disk routing:
  - go test -run '^TestEvaluator_AgreesWithEvaluate$' ./internal/hostperm/ <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
  - go test -run '^TestEvaluator_JudgesOnDiskOnlyWhatTheDiskCouldRespell$' ./internal/hostperm/ <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
  - go test -run '^TestEvaluator_AnEntryNamedByItsAliasIsJudgedOnDisk$' ./internal/hostperm/ <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- (e) All round-1 rows pass unchanged; no existing assertion was edited.

Mutation checks:
- In pathgate.go, removing any one of these turns at least one row red: the whole-summary piece, quoted segments, prefixes/suffixes, joins, the '=' value, root protection, or noting drop paths.
- In the evaluator, removing the link check turns TestEvaluator_AgreesWithEvaluate red, and removing the alias check turns the alias row red.

Test fixtures were kept within the store's 120-byte preview width (previewRoot and shortProjectDir), because t.TempDir paths alone exceed it and a real preview of that length would be cut.

### Commits

- ed27b4ceb07316ff9d9697400282e6f0d43532a1 perf(hostperm): judge one request's paths through an evaluator
- cd9414d3de3cd4ca6cd86f9021a96c304c71c89a fix(rehydrate): read summaries from the shapes the store writes
- 7f90567f0b267b61f232f3f70700767e2fcdeb7e fix(rehydrate): know paths the checkpoint records only as drops
- 13e41758c83e5283a2a15dc2c094833b9b11290e fix(rehydrate): never cut the project root apart in a summary
- ba217e44325aeff2f2e417bee17f8ec99e5685ec docs(adr): record D60 and the second summary-gate round

### Tests

- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/... ./internal/hostperm/...`: ok (rehydrate 8.2s, rehydrate/rehydratetest 1.8s, hostperm 4.6s)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/`: ok 341.2s
- `go test -p 2 -count=1 -v -run '^TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords$' ./internal/daemon/`: PASS at HEAD: 3145 judgements, 0 on disk, 48-72 ms across runs. On 4bad4cef product code (seam-free copy of the row): FAIL, 9096 judgements, 10.96 s, '9096 > 5280' <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -run '^TestRehydrateHostPaths_ADeniedPathWithDelimitersIsWithheld$' ./internal/daemon/`: PASS at HEAD; FAIL on 4bad4cef ('the payload shows a denied path')
- `go test -p 2 -count=1 -run '^TestRehydrateHostPaths_AProjectPathWithASpaceShowsItsOwnPaths$' ./internal/daemon/`: PASS at HEAD; FAIL on 4bad4cef ('the project's own path is shown')
- `go test -p 2 -count=1 -run '^TestBuild_DelimiterCharactersInADeniedPathNeverShowIt$' ./internal/rehydrate/`: PASS at HEAD; FAIL on 4bad4cef (all 5 subtests leak)
- `go test -p 2 -count=1 -run '^TestBuild_APathKnownOnlyFromACheckpointDropIsNamedByNoSelector$' ./internal/rehydrate/`: PASS at HEAD; FAIL on 4bad4cef (deny.txt shown in section 6)
- `go test -p 2 -count=1 -run '^TestBuild_AProjectPathWithASpaceShowsItsOwnAbsolutePaths$' ./internal/rehydrate/`: PASS at HEAD; FAIL on 4bad4cef (every in-project absolute summary withheld)
- `go test -p 2 -count=1 -run '^TestEvaluator_AgreesWithEvaluate$' ./internal/hostperm/`: PASS (the junction rows ran; the file-symlink row was skipped and logged, because this Windows host lacks the symlink privilege) <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -run '^TestEvaluator_JudgesOnDiskOnlyWhatTheDiskCouldRespell$' ./internal/hostperm/`: PASS <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -run '^TestEvaluator_AnEntryNamedByItsAliasIsJudgedOnDisk$' ./internal/hostperm/`: PASS; red with the alias check removed <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go vet ./internal/rehydrate/ ./internal/hostperm/ ./internal/daemon/ (Windows and GOOS=linux; also at each of the 4 code commits)`: clean
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/rehydrate/... ./internal/hostperm/... ./internal/daemon/ (plus GOOS=linux with a host-built binary)`: 0 issues on both
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 ./test/docs`: ok
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: all exit 0

### Open issues

- Found in round 2, not fixed, needs a ruling: the store cuts a preview longer than 120 bytes with an ellipsis. When the cut falls inside a host-denied path (most likely a long absolute Read path), the gate judges the prefix, which no exact-file rule refuses, so most of the denied file name shows in section 6. A rule on the file's directory still refuses it. Round 1 and the base behave the same. It is recorded in ADR 0011 §23.2 as 'listed for a ruling'. A partial fix would withhold a cut piece that is a string prefix of a known withheld path; that does not cover a denied path Qompack never recorded as a file pointer.
- New deliberate limit from the linear reading, recorded in §23.2 and following the coordinator's design: a path containing a space that sits unquoted between two other words of a command (cp private/my secret.txt backup/) is read word by word. Round 1's quadratic runs caught it. Quoted, or as a Read, Grep or Glob argument, or at the start or end of the preview, it is still read whole.
- No local Linux or macOS run (no WSL distro, and Docker must not be started); only GOOS=linux vet and lint ran. The evaluator's POSIX listing (os.ReadDir symlink flag) and the file-symlink equivalence row are exercised only off Windows, and the file-symlink row was skipped here for lack of privilege.
- -race was not run on the touched packages; that is the coordinator's night run.

### Needs owner

- New test constants with their derivations, all in internal/daemon/rehydrate_hostpaths_test.go:
- maxJudgementsPerSummary = 4*rehydrateCostWords - 2 = 66. A w-word preview with no quotes, delimiters, selectors or '=' yields at most 1 + w + 2(w-1) + (w-1) pieces, each judged once per build.
- maxDiskJudgementsPerSummary = 2. Only the preview's two file words name existing entries all the way down, so only they could reach the disk if they were links. The actual count is 0.
- rehydrateCostPointers = 80 and rehydrateCostWords = 17 come from the verifier's probe.
- rehydrateCostPreviewMax = 120 mirrors store.argsPreviewMax.
- New test constant previewWidth = 120 in internal/rehydrate/summary_privacy_test.go, also mirroring store.argsPreviewMax. No new numeric constant was added to product code.
- The secondary wall-clock assertion in the cost row is elapsed < compactAnswerBudget() (5 s, derived from the manifest). It is a sanity bound, not the pass criterion.

## review:rehydrate:privacy: verdict `needs-fixes`, 8 finding(s)

- **major** `internal/rehydrate/pathgate.go:339-366 (textPieces whole-t span and prefix/suffix loop) with inside() at :158`: Section 6 still withholds in-project absolute summaries, in every project (with or without a space in the root, with or without host rules). The cause is the whole-summary span or a suffix span that starts with the project root itself and runs on into the next word. inside() reads it as a sibling of the root ('C:\q\proj TODO' gives '..\proj TODO'), so the summary is withheld. Round 1 (4bad4cef) introduced this with its runs, round 2 kept it through spans, and the ADR lists it nowhere. D60's V4 ruling is that in-project absolute summaries are shown. Row (d) passes only because every shape it uses is below the root (src\main.go TODO, root\src *.go).
  - Evidence: Overlay probe TestZZProbe_RootShapes (scratchpad/probe/zz_probe_test.go, -overlay, denyFiles root=C:\q\proj):
- WITHHELD at HEAD: 'C:\q\proj TODO' (Grep, path=root), 'C:\q\proj **/*.go' (Glob), 'C:/q/proj TODO', 'cd C:\q\proj && go test ./...', 'git -C C:\q\proj status'.
- The same shapes are withheld for root C:\q\John Smith\proj.
- SHOWN: 'C:\q\proj\src TODO' and the quoted form 'cd "C:\q\proj" && ...'.
- 4bad4cef gives identical results for the plain root. a357d187 read token by token and showed them.
- With HostPaths=nil (containment only), all three shapes are still withheld.
- Through the real adapter (TestZZDaemon_CostAbsolute/abs-backslash), 'cd <root> && git log ...' had every rendered summary withheld (40 of 40), and no piece was refused by a host rule (TestZZDaemon_Debug logged none).
  - Fix: Do not let a span that begins with the project root followed by whitespace withhold the summary by containment alone. Option A: an absolute span takes its containment from its first word (each word is judged on its own anyway), and is judged against host rules and namesKnown only. Option B: keep the span's containment only when the word after the root continues as a path segment ("proj old\\secret.txt"). Option C: let the adapter say whether the out-of-project sibling it names exists. Record the residual risk (a real sibling named '<root base> <word>') as a limit. Add rows for Grep and Glob with path equal to the absolute root, 'cd <root> && go test ./...' and 'git -C <root> status', in both a spaced and a plain root, through rehydrate and through the real daemon adapter.
- **major** `internal/rehydrate/pathgate.go:373-394 (quotedSegments), :397 (appendPiece keeps outer quotes), :192 (summaryTokens lacks '&')`: V2's fix reads a quoted segment whole but ignores shell quoting and escaping. So the V2 spellings still reach section 6 in ordinary shell shapes the store records verbatim as a Bash or PowerShell command preview:
- PowerShell's doubled apostrophe in a single-quoted string;
- bash backslash escapes;
- an escaped double quote earlier in the command, which mis-pairs every later quote;
- a path glued to '&&' or '&'.
The file pointer for the same path is withheld. These leak at 4bad4cef too, but they are the same characters V2 was about, and the ADR records none of them as a limit.
  - Evidence: TestZZProbe_Rehydrate and TestZZProbe_Escapes. denyFiles covers each path, and each one is also a known file pointer. SHOWN at HEAD:
- Get-Content -LiteralPath 'private/John''s notes.txt'
- Get-Content 'private/John''s notes.txt'
- cat private/John\'s\ notes.txt
- cat private/deny\(2\).txt
- cat private/my\ secret.txt
- echo "a\"b" && cat "private/deny (1).txt" (the path ends the summary; the suffix piece keeps its quotes)
- grep -c "it\"s" "private/deny (1).txt"
- grep -c "it\"s" "private/a,b.txt"
- wc -l private/a,b.txt&&echo
- cat private/deny.txt&&ls
Through the real hostperm adapter (TestZZDaemon_Privacy, exact Read deny rules): Get-Content 'private/John''s notes.txt' and cat private/my\ secret.txt are both SHOWN.
  - Fix: Add a linear shell-word reading of each text and judge every word it yields as an extra span. For POSIX: '...' is literal, "..." honours backslash escapes of " \ $ `, a backslash outside quotes escapes the next character, and words split on unquoted whitespace and on && || ; | & < >. For PowerShell: '' inside '...' is one apostrophe, and a backtick escapes inside "...". Judge these unescaped words against host rules and namesKnown only, not containment, so that unescaping a Windows backslash path cannot over-withhold. Also trim matching outer quote characters from word and span pieces, and add & to summaryTokens. Add rows for each spelling above, in relative and absolute form.
- **minor** `internal/rehydrate/pathgate.go:339-366; docs/adr/0011-rehydration-budget-and-item-order.md:695-697`: Dropping round 1's runs loses more than the ADR's new limit states ('unquoted between two other words of a command'). Any free-text argument value with a spaced denied path in its middle is now shown, where 4bad4cef withheld it. Examples are a recall query and a parenthesised command. A cheap linear mitigation exists for every path Qompack recorded.
  - Evidence: TestZZProbe_FreeText (both paths are denied and known):
- {"query":"find private/my secret.txt usages"}: SHOWN at HEAD, WITHHELD at 4bad4cef.
- (cat private/my secret.txt): SHOWN at HEAD, WITHHELD at 4bad4cef.
- cp private/my secret.txt backup/: SHOWN at HEAD, WITHHELD at 4bad4cef.
  - Fix: Scan each text for every known withheld path and each of its tails. Match case-folded where paths.Key folds, and require a boundary on both sides (start or end, whitespace, a quote, a delimiter, ':' or '='). This costs O(|known| x len), makes no host calls, and catches a known path wherever its spaces fall. Then reword the §23.2 limit to say it applies to paths Qompack never recorded, and that it covers free-text arguments as well as commands.
- **minor** `internal/rehydrate/pathgate.go:259-270 (namesKnown), :542-556 (globWithheld); docs/adr/0011-rehydration-budget-and-item-order.md:669; docs/security.md:70`: The gate's model of recall's path: selector does not match store.pathSelector.weight (internal/store/search.go:305-330). A plain selector also selects by containment (wPathContains). A glob selector is path.Match'd against every path-segment suffix, while rules.Match anchors any pattern that has a '/'. A selector that selects a known withheld file is therefore shown, often spelling nearly the whole path. The ADR and the code comment say the selector selects 'by equality and by suffix'.
  - Evidence: TestZZProbe_Rehydrate/selectors (private/deny.txt and private/keep/a.txt are denied and known). SHOWN at HEAD and at 4bad4cef:
- {"query":"path:private/deny.tx"}
- {"query":"path:rivate/deny.txt"}
- {"query":"path:deny"}
- {"query":"path:keep/*.txt"}
The store's weight() selects the denied file for each of them.
  - Fix: For a piece read as a path: selector value, mirror weight() exactly:
- a plain value is withheld when it equals, is a /-suffix of, or is a substring of a known key;
- a glob value is withheld when path.Match matches the whole key or any path-segment suffix of it.
Correct the ADR §23.2 sentence, the pathgate.go:259 comment and the security.md wording. Add rows for path:private/deny.tx and path:keep/*.txt.
- **minor** `internal/hostperm/evaluator.go:106-113 (lexical bails on respelled); internal/daemon/rehydrate_hostpaths_test.go maxDiskJudgementsPerSummary`: The disk-free fast path, and the row's bound of 2 disk judgements per summary, hold only for previews made of relative plain words. Any piece that contains a drive colon goes to disk, through respelled's colon check. That includes every prefix, suffix and join of a command holding an absolute Windows path, and a quoted root word. So does every piece while an unexpandable 8.3 rule is in force. A cost row with absolute paths would show this.
  - Evidence: TestZZDaemon_CostAbsolute, 80 previews through the real adapter on this machine:
- cd "C:/.../qN" && git log ...: 2336 judgements, 1447 on disk (about 18 per summary), 0.70 s.
- Unquoted 'cd C:\...\qN && ...': 486 on disk.
- Deny rule Read(**/CREDEN~1.SEC): 1865 judgements, all on disk, 0.82 s.
The verifier measured about 1.5 ms per uncached evaluation, and a resolved root that differs from the root doubles the evaluations. That puts these shapes at a sizeable fraction of compactAnswerBudget (5 s).
  - Fix: In Evaluator.lexical, handle a colon without the disk when syscall.FullPath(abs) == abs. Walk both the as-written path and stripStreams(abs), and use both as candidates, which is what osAlias adds. Fall back to disk only when a walk meets a link or an alias. Add a cost row whose previews are 'cd <abs root> && ...' and absolute file arguments, and bound its disk count.
- **nit** `internal/rehydrate/pathgate.go:484 (summarySelector skips values starting '//'), :168-171 (absLike)`: These pre-existing shapes show a path outside the project:
- a file:// URL naming a local file (the 'file' selector's value starts with '//', so it is never judged);
- a drive-relative Windows path ('D:secret.txt').
  - Evidence: TestZZProbe_Rehydrate/urls-outside. SHOWN at HEAD and at 4bad4cef:
- file:///C:/q/outside/secret.txt
- curl file:///etc/passwd
- {"url":"file:///etc/passwd"}
- D:secret.txt
- cat C:secret.txt
  - Fix: For a word or JSON value with the file: scheme, strip 'file://' and an optional empty host, URL-decode it, and judge the rest as an absolute path. Treat '^[A-Za-z]:[^\\/]' as rooted in absLike, so a drive-relative path counts as outside the project.
- **nit** `docs/adr/0011-rehydration-budget-and-item-order.md:681-682, :729; docs/security.md:69-71`: The ADR's record of the rulings has three inaccuracies.
- Item 5 cites 'D60(c)', but D60's rulings are (i) to (iv). (c) is a required-row label.
- §23.2 says 'D50 covers ... section 7's drop entries' with no qualifier. Yet section 7 renders an elimination drop as already_tried(target="<model text>", ...) and a checkpoint open_question drop with its question text, both ungated. That is consistent with D60(i), but the ADR should say so.
- security.md says any 'drop entry that names a path' points by hash.
  - Evidence: items.go:780 and :881 build the elimination drop detail from rec.Target. truncate.go:262 puts the open-question text in Detail. gateCheckpointDrops gates only the five path-keyed kinds.
  - Fix: Change 'D60(c)' to the right citation (the design direction, or V1/V2). Qualify the D50 sentence: section 7's path-keyed entries (the five checkpoint kinds and section 6's own pointer drops) are covered, while entries that restore a section 3 or 4 record, or name an open question, carry the model's own text and are outside D50, like those sections. Mirror the same qualification in security.md.
- **nit** `internal/checkpoint/validate.go:91 with gitindex.go:155-158 (rendered by rehydrate dropLine)`: A pointer_git_unavailable drop carries gitErr.Error() verbatim. When the index stat fails, that error is an *os.PathError naming the gitdir. For a linked worktree (for example one created with --no-checkout, which has no index) the gitdir lies outside the project. Section 7 and dropped() then show an out-of-project path that is not one of the five gated kinds.
  - Evidence: From reading the code, not probed. readGitState returns fmt.Errorf("%w: reading index: %v", errIndexUnsupported, err), where err comes from os.Stat(filepath.Join(gitDir, "index")) and gitDir is the .git file's gitdir, which may be absolute and outside the root. buildDropReport renders the first ID-less entry of a kind together with its detail.
  - Fix: Have ValidatePointers report the git failure without the path (errors.Unwrap the PathError to its Err, or use a fixed reason). Alternatively, in gateCheckpointDrops, replace absolute paths outside the project in a pointer_git_unavailable detail with '(path withheld)'.

## review:rehydrate:cost: verdict `needs-fixes`, 3 finding(s)

- **major** `internal/hostperm/evaluator.go:106-112 and :186-198 (lexical/respelled/plainSegments); internal/hostperm/alias_windows.go:154; internal/daemon/rehydrate_hostpaths_test.go:160-190 (cost fixture); docs/adr/0011-rehydration-budget-and-item-order.md:765,798`: V1 is fixed only for colon-free, ASCII-only previews. Evaluator.lexical sends every piece containing a ':' (respelled and plainSegments) or any non-ASCII byte (plainSegments) to the full on-disk RuleSet.evaluate. On Windows each such judgement costs about 0.3 to 3 ms, because every distinct piece has a distinct path, so the memo hits only the root components. Those pieces are common. Every canonical-JSON summary has a colon in every word, prefix, suffix and join: MCP tools including Qompack's own recall/expand, Task, TodoWrite, WebSearch. So do URLs, host:port, conventional-commit 'type(scope):' messages, 'TODO:', drive paths in the middle of a command, em-dashes and smart quotes, and any non-English text. Each costs about 2-3w disk judgements per summary. The cost row's fixture has none of these shapes, so its result ('0 on disk', maxDiskJudgementsPerSummary=2) and the ADR's 'none on disk' describe the fixture, not the class.
  - Evidence: Overlay probe through the real rehydrateHostPathsObserved/mcpOpHostPolicy: three deny rules, real files, previews from store.ArgsDigest. Judgement and disk counts are deterministic. Wall times were taken under co-load from other agents' go test runs.

At HEAD, n=80 (judgements / on disk / wall time):
- bash fixture: 3145 / 0 / 58-106 ms
- JSON (Task-shaped): 3900 / 2760 / 0.88-3.8 s; 4bad4cef 3357 / 1.52 s; b8bbc2dd 160 / 96 ms
- recall {"query":"path:..."}: 1043 / 801 / 0.43 s; 4bad4cef 0.38 s
- urlbash: 2339 / 2080 / 0.73-1.65 s
- colonbash ('grep TODO: in'): 2983 / 2163 / 0.78 s
- em-dash commit message: 2831 / 2642 / 5.2-7.9 s
- Cyrillic commit message: 1698 / 1613 / 1.8-5.4 s

At larger n:
- n=150: em-dash 16.5 s; 56-word preview with one colon 16698 disk / 3.9-4.2 s
- n=400 (budgetTokens about 32000): JSON 13645 disk / 8.3 s

The default 12000-token checkpoint holds about 150 tool pointers, and compactAnswerBudget is 5 s. For JSON summaries HEAD is no cheaper than 4bad4cef.

Verified sound alongside: the evaluator stays fail-closed for aliases. Junction pub->'private stuff' and 8.3 PRIVAT~1 / SECRET~1.TXT, in Bash, Read and absolute forms, are all withheld at HEAD as at base.
  - Fix: Make these classes cheap in an equivalence-preserving way, and pin them in the cost row.
(1) In Evaluator.lexical, handle a stream colon lexically on Windows. Walk the stripStreams tail. If that walk is clean, return base.spellings x {raw tail, stripped tail}, which is exactly Evaluate's spellings + alias + resolves. Any link or alias met on the stripped walk still goes to disk. On POSIX, a ':' needs no exclusion at all.
(2) For non-ASCII segments, compare under a conservative skeleton: NFC/NFD plus simple case folding of both the segment and the listing names. A segment whose skeleton matches no listed entry names nothing and can stay lexical; any collision goes to disk.
(3) Optionally, in pathgate, read prefixes, suffixes and joins of a JSON summary only from its decoded values (texts[1:]), not from the raw JSON text, whose words are JSON syntax.
(4) Add JSON-shaped, URL/colon and non-ASCII previews (via store.ArgsDigest) to TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords, each with a derived disk bound, and correct ADR §23.5 and the adapter comment ('first segment names nothing') to state which piece classes reach the disk.
- **major** `internal/rehydrate/pathgate.go:359-365 (prefix/suffix spans) with inside() :158-169`: A summary that names the project root as a whole word followed by more words is withheld in every project, even with no host rules, under containment alone. The suffix span '<root> && go test ./...' is read as one absolute path, and filepath.Rel gives '..\p && go test ./...', a sibling outside the project. So 'cd <root> && <cmd>', probably the most common Claude Code Bash shape, loses its summary, and so does 'git -C <root> status'. The section 6 line then wrongly says the summary names a path outside the project. Round 1 (4bad4cef) introduced this and round 2 keeps it. It also defeats V4's intent in a space root: 'cd C:\...\John Smith\proj && go test' is withheld there too. No row pins it, and ADR §23.2 does not list it as a limit.
  - Evidence: Probe through the real adapter at b8bbc2dd, 4bad4cef and HEAD, root <tmp>\p, in both containment-only and host-rule modes.
- 'cd <root> && go test ./...' (with / or \ separators): b8bbc2dd shown; 4bad4cef and HEAD withheld.
- 'git -C <root> status --short': b8bbc2dd shown; 4bad4cef and HEAD withheld.
- 'cd <root>/src && go test', 'ls <root>' and 'go test ./... && cd <root>': shown at all three.
- The same results hold with root <tmp>\John Smith\proj at HEAD.
- In the cost probe's cdbash shape, 40 of the 40 summaries the payload shows are 'summary withheld'.
  - Fix: Do not let containment withhold a prefix or suffix span that is only the reading's artifact. When a pieceSpan or pieceJoin is absLike and outside the project, but its first word (the protected root spelling or an absolute word) is inside the project on its own, judge the span by the host rules only, or skip it, since its words are each judged. Keep the containment verdict for the whole summary and for quoted segments, so that a Read or Grep preview of 'C:/proj backup/x' is still withheld. Add rows for 'cd <root> && ...' and 'git -C <root> ...', in a plain root and in a space root, both shown, next to the existing sibling and proj2 rows.
- **minor** `internal/daemon/rehydrate_hostpaths_test.go:179-186`: maxDiskJudgementsPerSummary = 2 has a wrong derivation. The comment says 'even where they were links a preview could not cost more than two', but the suffix spans that begin at each file word walk through the same first segment. With src or docs as a link, a fixture preview costs 4 disk judgements: two words plus two suffixes. The bound holds only because the fixture contains no link, so it pins the fixture's layout, not the gate's worst case.
  - Evidence: linkbash probe at HEAD: the same 17-word fixture with a junction lib->src, naming lib/modN/fileN.go and lib/guideN.md, gives disk=40 at n=10, 160 at n=40 and 320 at n=80, which is 4 per summary.

Verified sound alongside: the cost row is deterministic (3145 judgements, 0 on disk, in 3 of 3 runs). Run against 4bad4cef's pathgate with HEAD's adapter it is red: '9096 is not less than or equal to 5280'.
  - Fix: Re-derive the bound as 2 file words + 2 suffixes starting at them = 4, or state that it assumes no link in the fixture. Better, add a linked-directory variant of the row whose disk count must equal the derived bound.

## fix:rehydrate: status `done`, head `1dd7b00de02644f531690f5e51bd7fc17d246332`

### Root cause

R1/R7: round 1's reading treats an unquoted stretch as one path. A stretch that starts at the root and runs into the next word therefore reads as the root's name plus a space and more, which is a sibling outside the project, and containment withheld it. R2: summaries were read as written, never as the shell that ran the command reads them (escapes, doubled quotes, an escaped quote mis-pairing every later quote). R3: the linear reading has no span for a path whose spaces sit between other words, and nothing searched texts for the paths the build already knows are withheld. R4: the gate's model of recall's path: selector covered only equality and suffix, while store.pathSelector.weight also matches substrings and glob suffixes. R5/R6: Evaluator.lexical sent every piece with a colon (respelled) or a non-ASCII byte (plainSegments) to the full on-disk evaluation. Both shapes appear in every canonical-JSON, URL, TODO:, drive-mid-command, em-dash and non-English preview. R8: the derivation counted file words but not the suffixes that start at them, and it pinned a link-free fixture with a bound sized for links.

### Summary

PRODUCT CODE CHANGED (internal/rehydrate/pathgate.go, internal/hostperm/evaluator.go + alias_windows.go + alias_other.go, internal/daemon/rehydrate_service.go).

Fix seat, wave 19b rehydrate. Worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w19-rehydrate, branch closeout/w19-rehydrate. 5 commits on the implementer's ba217e44; head 1dd7b00d; working tree clean. Each commit builds and vets on Windows and GOOS=linux. Commit messages carry no attribution trailers.

## Review resolution
All 8 findings were verified independently: a probe on HEAD first, then a failing row. All 8 are CONFIRMED. 7 are fixed and 1 (R8) is re-derived.

**R1 + R7 (major, one root cause). The project root followed by more words was withheld.**
- Cause: an unquoted span or join that starts at the root and runs into the next word reads as a sibling of the root (`C:\q\proj TODO` gives `..\proj TODO`).
- Fix (pathgate.go pieceRefused): each unquoted span or join now carries its first word. When that word is inside the project and only the whole stretch runs outside it, the stretch is judged by host rules alone. Every word is still checked for containment on its own. Quoted segments, path-named arguments and single words keep containment.
- Deviation from R7's suggestion: I waive containment for the whole summary too. Grep or Glob with path = root IS the whole summary, which R1 and D60 require shown.
- Residual, recorded as an ADR 0011 §23.2 limit: an unquoted preview naming a sibling called `<root> <more>` (Windows' `proj - Copy\x`) is judged by host rules only. Quoted, or as a JSON path argument, it is still withheld.
- Shown now: `cd <root> && go test ./...`, `git -C <root> status`, and Grep/Glob over the root, in both a plain and a spaced root.

**R2 (major). Shell quoting and escapes.**
- Fix: each text is also read as a POSIX shell reads it and as PowerShell reads it (posixWords, powershellWords; linear). Any word that differs from the plain words is judged like a word, containment included.
- Deviation: the reviewer said to skip containment for these words. Unescaping only drops characters, PowerShell keeps backslashes, and the root-protection and spaced-root rows stay green with shell words judged fully. So skipping containment would only weaken the gate.
- The reviewer's two extras were tried and dropped: adding `&` to summaryTokens, and trimming outer quotes. All rows stayed green without them, so both are redundant with the shell reading.

**R3 (minor). A recorded withheld path in free text.**
- Fix (namesKnownIn): every withheld path the build records is searched for in each text with a word boundary on both sides. That covers each path-segment suffix and the full path under the root's absolute spelling. A separator is not a boundary, so `README.md` under another directory still shows. The search makes no host calls.
- The §23.2 limit now covers only paths Qompack never recorded, in free text as well as commands.

**R4 (minor). Selectors.**
- Fix (selectsKnown): the store's selector logic (pathSelector.weight) is mirrored. A plain value matches by equality, path-segment suffix or substring. A glob matches by path.Match on the whole key or any path-segment suffix.
- The ADR, the code comment and docs/security.md are corrected to match.

**R5 + R6 (major). Colon and non-ASCII pieces went to disk.**
- Fix (evaluator.lexical): each piece is walked both as written and as syscall.FullPath normalizes it. Each segment is looked up by the entry name it opens; on Windows a stream's colon is cut off.
- The candidate spellings are exactly the ones Evaluate produces. Resolved spellings are joined the way resolveLinks joins them: on Windows, filepath.Join adds no separator after a segment ending in `:`. The new candidate row caught this mismatch.
- The 8.3 verdict is computed lexically, on Windows only.
- A segment ending in a dot or space may only end the walk, and must name nothing as written or trimmed. Reason: Win32 trims it when it ends a prefix that GetLongPathName or Readlink opens (probed: `cd \x` resolves to `cd\x`).
- Non-ASCII on Windows is compared under any possible case table: same UTF-16 length, ASCII letters fold-equal, a non-ASCII unit matches anything. Any match goes to disk. On macOS non-ASCII always goes to disk.
- The adapter also passes the root's parent as an evaluator base, so the waived sibling stretch from R1 is settled lexically.
- Result: Task JSON went from 2722 disk evaluations in 1.95 s to 0 in about 85 ms. Recall JSON, URL, colons, em dash, Cyrillic and absolute-root commands are all 0 on disk.
- R6 option (3), reading JSON prefixes only from decoded values, was not needed and not done.

**R8 (minor). The cost row's disk bound.** maxDiskJudgementsPerSummary is tightened from 2 to 0 with a correct derivation. A new linked-directory variant must produce exactly 4 disk evaluations per summary (2 file words × {the word, the suffix that starts at it}); measured: exactly 320 for 80 summaries.

## Failing rows first, red on ba217e44 product code
Run with `-overlay` pointing at the base product files; all were red there:
- `TestBuild_TheProjectRootFollowedByMoreWordsIsShown`
- `TestBuild_ShellQuotingAndEscapesNeverShowADeniedPath`
- `TestBuild_AKnownWithheldPathIsFoundAnywhereInAText`
- `TestBuild_APathSelectorIsJudgedByWhatItSelects`
- `TestRehydrateHostPaths_TheProjectRootFollowedByMoreWordsIsShown`
- `TestRehydrateHostPaths_AShellEscapedDeniedPathIsWithheld`
- `TestRehydrateHostPaths_EveryPreviewShapeIsJudgedWithoutTheDisk` (all 8 classes)
- `TestEvaluator_LexicalCandidatesAreEvaluatesSpellings`

The two bound rows, `...SummaryJudgementsAreLinearInTheirWords` and `...APieceThroughALinkIsJudgedOnDisk`, pin R8's derivation and are green on base.

## Mutation checks
19 mutations of the new mechanisms; every one turns a row red: root waiver, the shell reading, the POSIX reading, the PowerShell reading, namesKnownIn, its absolute form, the `@` boundary, the sentence-end rule, selector matching, selector glob matching, trailed-name check, resolved-Join mimic, lexical unresolved, non-ASCII exact-found, wide scan, all-names scan, link kind, parent base. A first survivor (selector-value unquoting) was redundant and is removed. A second (the all-names scan) gained a Cyrillic case that kills it.

## Linux
The owner's docker-desktop WSL distro was already running; I started nothing. I ran static Linux test binaries from /run, which I removed afterwards. It caught 2 real defects in my own work, both fixed and folded into their commits:
- POSIX has no 8.3 names, but my lexical walk marked `HEAD~1` unresolved. Fixed with a per-OS `unexpandable` helper.
- The R2 row's absolute PowerShell shape hard-coded backslashes.

Final Linux results: hostperm full PASS (including the file-symlink equivalence row Windows skips), rehydrate full PASS, every TestRehydrateHostPaths_* row PASS.

## Other
- ADR 0011 §23: new §23.8 (all 8 findings); §23.2 selector sentence corrected and the limit list grown from 4 to 6; §23.5 points at §23.8; Evidence list extended. docs/security.md wording updated.
- During the optional devtool lint run, its `stubskips` sub-check started a whole `test/e2e` run. I stopped that process tree, which was mine. The unrelated go test (PID 16152) was not touched. stubskips is not a required check.

### Commits

- 565415eedddf36f2cb1ca30f2aa5c1cc8c3012bf perf(hostperm): walk colon and non-ASCII pieces without the disk
- b07f97a72a69a256091b3e7e6cf1520ed6302e81 fix(rehydrate): show the project root followed by more words
- 3f6d0abf1107722eb8b25181624c0cb136d33827 fix(rehydrate): read shell words, recorded paths and selectors
- ba6788b103861a15372bb997aad0563122505d9e perf(daemon): settle root siblings from the parent's listing
- 1dd7b00de02644f531690f5e51bd7fc17d246332 docs(adr): record the round-2 review's summary-gate fixes

### Tests

- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/... ./internal/hostperm/...`: ok at head content (rehydrate 7.3s, rehydratetest 1.4s, hostperm 4.2s)
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/`: ok 509.7s. This ran on a tree whose Windows product behaviour equals head. Changes since are test-only (costBuild signature, macOS expectation) plus the POSIX-only unexpandable helper. All daemon rehydrate rows were re-run at head: go test -p 2 -count=1 -run '^(TestRehydrateHostPaths_|TestWireRehydrator_InstallsTheHostPathRules$)' ./internal/daemon/ -> ok 1.5s
- `go test -p 2 -count=1 -run '^TestBuild_TheProjectRootFollowedByMoreWordsIsShown$' ./internal/rehydrate/`: PASS at head; FAIL on ba217e44 product code (both subtests)
- `go test -p 2 -count=1 -run '^TestBuild_ShellQuotingAndEscapesNeverShowADeniedPath$' ./internal/rehydrate/`: PASS; FAIL on base (relative and absolute)
- `go test -p 2 -count=1 -run '^TestBuild_AKnownWithheldPathIsFoundAnywhereInAText$' ./internal/rehydrate/`: PASS; FAIL on base
- `go test -p 2 -count=1 -run '^TestBuild_APathSelectorIsJudgedByWhatItSelects$' ./internal/rehydrate/`: PASS; FAIL on base
- `go test -p 2 -count=1 -run '^TestRehydrateHostPaths_TheProjectRootFollowedByMoreWordsIsShown$' ./internal/daemon/`: PASS; FAIL on base (plain and spaced root)
- `go test -p 2 -count=1 -run '^TestRehydrateHostPaths_AShellEscapedDeniedPathIsWithheld$' ./internal/daemon/`: PASS; FAIL on base
- `go test -p 2 -count=1 -v -run '^TestRehydrateHostPaths_EveryPreviewShapeIsJudgedWithoutTheDisk$' ./internal/daemon/`: PASS: 0 disk evaluations in all 8 classes (Task JSON: 4494 judgements, about 85ms). FAIL on base, e.g. Task JSON 2722 disk / 1.95s, URL 1360, em dash 1921 <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -v -run '^TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords$' ./internal/daemon/`: PASS: 3145 judgements, 0 on disk (bound now 80*66 judgements and 80*0 disk) <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -v -run '^TestRehydrateHostPaths_APieceThroughALinkIsJudgedOnDisk$' ./internal/daemon/`: PASS: exactly 320 = 80*4 disk evaluations <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -run '^TestEvaluator_LexicalCandidatesAreEvaluatesSpellings$' ./internal/hostperm/`: PASS; FAIL on base ('private/deny.txt:hidden' went to disk) <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -run '^TestEvaluator_ANameAListedNonASCIINameMayEqualIsJudgedOnDisk$' ./internal/hostperm/`: PASS; red with the wide scan or the all-names scan removed <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -run '^TestEvaluator_AgreesWithEvaluate$' ./internal/hostperm/`: PASS, unchanged row (on Windows the file-symlink link is skipped for lack of privilege; on Linux all three links were made) <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `go test -p 2 -count=1 -run '^TestEvaluator_JudgesOnDiskOnlyWhatTheDiskCouldRespell$' ./internal/hostperm/`: PASS (criterion change, see criterion_changes) <!-- runpatterns: names a row of an earlier round that a later rehydrate round removed or renamed (D61 reverted the evaluator; D63 replaced the screen); the command ran as quoted at that round's head -->
- `Linux: CGO_ENABLED=0 GOOS=linux go test -c for hostperm, rehydrate and daemon, run in the already-running docker-desktop WSL distro (TMPDIR=/run/qtest-w19, removed afterwards)`: hostperm full PASS; rehydrate full PASS; daemon -run '^(TestRehydrateHostPaths_|TestWireRehydrator_InstallsTheHostPathRules$)' PASS. The first run caught two defects of mine, fixed before handoff
- `mutation script (19 mutations of the new mechanisms, each against its row)`: all 19 killed, after removing one redundant reading and adding one row case
- `go vet ./internal/rehydrate/... ./internal/hostperm/ ./internal/daemon/ (Windows, GOOS=linux, GOOS=darwin for hostperm and rehydrate; also each of the 4 code commits via git archive)`: clean
- `go vet ./internal/cli/... ./internal/mcp/... ./test/e2e/... ./test/guards/... ./test/integration/... ./test/replay/... (importers of the touched packages)`: clean
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/rehydrate/... ./internal/hostperm/... ./internal/daemon/ (Windows, and GOOS=linux with a host-built binary)`: 0 issues on both
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go run ./tools/devtool lint --only=importgraph,testdeps,sleepcheck; go run ./tools/lint/nomagic ./internal/rehydrate/... ./internal/hostperm/... ./internal/daemon/`: all PASS; nomagic exit 0
- `go test -p 2 -count=1 ./test/docs`: ok
- `go run ./tools/devtool gen-config-docs --check; gen-command-docs --check; gen-mcp-docs --check`: all exit 0

### Criterion changes

- TestEvaluator_JudgesOnDiskOnlyWhatTheDiskCouldRespell (a round-2 implementer row, internal/hostperm). Before: it asserted that a stream ('private/deny.txt:s') and a non-ASCII name ('café.txt') are judged on disk, and 'nul' on Windows. Making those lexical is exactly the fix R6 asks for. They now appear in its no-disk list, with more R6 shapes: URL, mid-command drive, JSON, 'gone. /x', 'git diff HEAD~1', em dash. On macOS, non-ASCII stays on disk. The on-disk list is now: a trailing dot or space naming an entry once trimmed, plus links. 'nul' leaves the routing list; its decision equivalence is still pinned by TestEvaluator_AgreesWithEvaluate (unchanged) and by the new candidate row. Correctness is pinned spelling for spelling by the new TestEvaluator_LexicalCandidatesAreEvaluatesSpellings.
- TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords: maxDiskJudgementsPerSummary tightened from 2 to 0, and the derivation corrected per R8. The row was refactored onto a shared costBuild helper with a short canonical root (costProject), so a preview naming the root fits the store's width and each piece is judged once. Its judgement bound, withholding assertion and wall-clock sanity bound are unchanged.

### Open issues

- NEW, found this round, needs a ruling; over-withholding, not a leak. On Windows, under any Read deny or ask rule, a summary containing a word like HEAD~1 (git diff HEAD~1, git reset --soft HEAD~2) is withheld. hostperm refuses a kept 8.3-shaped segment it cannot expand (unresolvedShortName), and since round 1 every word is judged. Probe through the real adapter: refuses('git diff HEAD~1') = true and refuses('HEAD~1') = true. a357d187 showed these summaries. Linux does not refuse them, since POSIX has no 8.3 names. A fix would change hostperm's fail-closed 8.3 semantics or the adapter's reading of that refusal. Both are outside this seat's 'memo/pre-match seam only' scope for hostperm.
- CARRIED, still listed for a ruling (ADR 0011 §23.2). The store cuts a preview longer than 120 bytes with an ellipsis. When the cut falls inside a host-denied path, the gate judges the prefix, which no exact-file rule refuses. namesKnownIn needs the whole path, so it does not catch a cut prefix either.
- CARRIED, narrowed. The linear-reading limit now applies only to paths Qompack never recorded. A recorded withheld path is found anywhere in a text (namesKnownIn). The limit now also names free-text arguments, not just commands.
- NEW deliberate limit (R1 residual, ADR §23.2). An unquoted preview naming a sibling of the root called '<root last segment> <more>' (Windows Explorer's 'proj - Copy\\x') is judged by host rules alone, so it is shown unless a host rule refuses it. A quoted form, or a path-named JSON argument, is still withheld.
- NEW deliberate limit (ADR §23.2). Only POSIX and PowerShell quoting is modelled. cmd.exe's ^ escape is read as written, which matters only for a path Qompack never recorded.
- Platform coverage: macOS is still unrun (GOOS=darwin vet only). The macOS paths (non-ASCII judged on disk, and the matching cost-row expectation) are untested on a real Mac. Linux now has real runs.
- -race was not run on the touched packages; that is the coordinator's night run.
- The daemon package's full run (509.7s) came before two test-only edits and the POSIX-only unexpandable helper. All daemon rehydrate rows were re-run at head on Windows and on Linux, but not the whole package again.

### Needs owner

- Changed test constant, internal/daemon/rehydrate_hostpaths_test.go: maxDiskJudgementsPerSummary goes from 2 to 0 (tightened). Derivation: the fixture has no link, alias, entry a trailing dot or space trims to, or non-ASCII listed name. Each piece either stops naming entries at some segment or names plain entries throughout, so the evaluator needs no disk.
- New test constant, same file: linkedDiskJudgementsPerSummary = 2*2 = 4. Derivation: with lib a link to src, the pieces whose first segment is lib are the 2 file words and the 2 suffixes that start at them. Every prefix, join and the whole preview start at 'git'.
- Carried test constants with their derivations: maxJudgementsPerSummary = 4w-2 = 66 (comment updated: shell readings add nothing for this fixture, and namesKnownIn asks the host nothing); rehydrateCostPointers = 80; rehydrateCostWords = 17; rehydrateCostPreviewMax = 120 and previewWidth = 120, both mirroring store.argsPreviewMax.
- Secondary wall-clock checks in the 3 cost rows: elapsed < compactAnswerBudget() (5s). They are sanity bounds, not pass criteria.
- No new tunable number in product code. New named sentinels in hostperm: entryNone = 0 and entryUnsure = 0xff, lookup answers outside the entryKind bit set. New named strings in rehydrate: wordBoundary and recallPathSelector = 'path' (mirrors internal/mcp selectorPath).

## verify:rehydrate: verdict `needs-fixes`, 4 finding(s)

- **major** `internal/rehydrate/pathgate.go:519-545 (textPieces' use of shellWords), :579-695 (posixWords, powershellWords, quotedSegments), :253-258 (namesKnownIn runs over raw texts only); docs/adr/0011-rehydration-budget-and-item-order.md §23.2 third limit bullet and §23.8 'Shell quoting and escapes'; docs/security.md:72-75`: R2 is fixed only for quoting at the top level. A command that runs another shell carries the path inside a nested quote: `powershell -Command "..."` (the usual way to reach PowerShell from Git Bash on Windows), `pwsh -c`, `bash -c`, `sh -c`, `wsl -e sh -c`. posixWords and powershellWords return the outer argument as one word (`Get-Content 'private/John''s notes.txt'`), and that word is never read again. quotedSegments skips any quote nested inside a pair. namesKnownIn searches only the raw texts. So the V2 spellings still reach section 6 when they sit inside such an argument, and so do recorded withheld paths whose file pointer is withheld (the escaped spelling `John''s` or `my\ secret` is never unescaped). The ADR says 'Quoted, escaped the way a POSIX shell or PowerShell reads it ... any path is read whole' and that a recorded withheld path 'is found anywhere in a summary'; security.md says the same. Both claims are false for these shapes.
  - Evidence: Overlay probe TestZZProbe_NestedIsolated: each summary built on its own, with an exact denyFiles rule on its path.

Not recorded (no file pointer), SHOWN at HEAD:
- powershell -NoProfile -Command "Get-Content -LiteralPath 'private/my secret.txt'"
- sh -c 'cat "private/my secret.txt"'
- wsl -e sh -c 'cat "private/my secret.txt"'
- powershell -Command "Get-Content 'C:\q\proj\private\my secret.txt'"
- bash -c "wc -l 'private/a,b.txt'"
- powershell -Command "Get-Content 'private/deny (1).txt'"
- pwsh -c "cat 'private/John''s notes.txt'"
- bash -c 'cat private/my\ secret.txt'

The same paths at the top level are WITHHELD: cat 'private/deny (1).txt' and cat private/my\ secret.txt.

Recorded (a file pointer for the same path, which is withheld), still SHOWN:
- powershell -Command "Get-Content 'private/John''s notes.txt'"
- pwsh -c "cat 'private/John''s notes.txt'"
- bash -c 'cat private/my\ secret.txt'

Through the real adapter (TestZZDaemon_Shapes: rehydrateHostPathsObserved and mcpOpHostPolicy, deny rules Read(./private/John's notes.txt) and Read(./private/my secret.txt), real files, store.ArgsDigest previews), the first and third are SHOWN both with and without the file pointers. 0 disk evaluations.
  - Fix: Read again, through shellWords and quotedSegments, every shell word or quoted segment that still holds a quote, a backslash or a backtick. Bound the depth (say 3); each level is linear and the words only get shorter. Judge the words each level yields as words.

Also run namesKnownIn over every shell-read word, not only the raw texts.

Add rows for each V2 spelling wrapped in powershell -Command "...", pwsh -c, bash -c / sh -c and wsl -e sh -c:
- relative and absolute;
- not recorded and recorded;
- in rehydrate and through the real adapter.

If nested quoting is not fixed, record it as a limit instead and correct the ADR §23.2/§23.8 and security.md claims.
- **minor** `internal/rehydrate/pathgate.go:388-443 (namesKnownIn, containsBounded; wordBoundary does not include '/'); ADR 0011 §23.8 'A recorded withheld path in free text'`: R3's search for a recorded withheld path misses the common `./` (or `.\`) spelling of the same path. The character before `private/...` is then a '/', which is not a word boundary, and no tail starts there. No word or span spells the path either, so a recorded withheld path with a space is shown when it is written `./`-relative in free text or in a command.
  - Evidence: TestZZProbe_KnownPollution 'dotslash' rows: private/my secret.txt is denied and recorded as a file pointer.
- {"query":"find ./private/my secret.txt usages"}: SHOWN
- cp ./private/my secret.txt backup/: SHOWN
- {"query":"find private/my secret.txt usages"}: WITHHELD
  - Fix: Treat a leading `./` or `.\` as part of the left boundary when a word boundary or the start of the text comes before it. Alternatively, also search for "./"+k. Add the two rows above.
- **minor** `internal/rehydrate/pathgate.go:114-121 (newPathJudge notes every withheld non-join piece as known, prefix/suffix spans and word fragments included), widened by selectsKnown's substring rule (:325-344) and namesKnownIn (:388-406)`: Under a directory rule, every span or fragment whose first word lies in the denied directory is refused. Each one is then noted as a known withheld path, and its tails and substrings withhold unrelated summaries. Example: one summary naming the denied directory followed by other files (`grep -rn apiKey secrets/ config/app.yaml src/main.go`) makes `secrets/ config/app.yaml src/main.go` known. Its tail `main.go` and its substrings `config` and `app.yaml` then withhold summaries that name no withheld path. The same happens with fragments: Grep's `private/my secret.txt TODO` under Read(./private/**) notes `private/my`, whose tail is `my`.

This is present at ba217e44 for whole-word equality. R4's substring rule and R3's search through the text widen it. It is over-withholding only, but it occurs in the task's own rule set (Read(./secrets/**)).
  - Evidence: Real adapter, TestZZDaemon_Pollution, deny rules [Read(./private/deny.txt), Read(./.env), Read(./secrets/**)], real files.
- Without the trigger summary, these are SHOWN: {"query":"path:config"}, {"query":"path:app.yaml"}, go run main.go.
- With the summary 'grep -rn apiKey secrets/ config/app.yaml src/main.go' added, all three are WITHHELD.
- Rehydrate probe with a fake rule on the private directory (TestZZProbe_KnownPollution): adding 'grep -rn TODO private/ src/main.go' withholds 'go run main.go', 'go vet ./... && go build -o bin/app main.go', {"query":"path:main.go"} and {"query":"main.go: retry loop"}.
- The same probe against ba217e44's pathgate.go also withholds them.
  - Fix: Note a prefix/suffix span as a known path only when the span as one path is the refused thing: skip it when its lead word alone is already withheld, since the directory rule then explains the refusal. Consider the same for a word that is only a fragment of a span that is itself noted. Add a row: one directory rule, one summary of the shape above, and unrelated summaries naming the trailing basename or a substring of it stay shown.
- **minor** `internal/hostperm/policy.go unresolvedShortName (reached through Evaluator.lexical's unexpandable verdict) for every summary word judged since round 1; internal/daemon/rehydrate_service.go adapter`: The fix seat lists this among its open issues; it is still wrong and needs the ruling before merge. On Windows, under any Read deny or ask rule, a summary holding a git revision word such as HEAD~1 is withheld. Each word is judged as a project path; `HEAD~1` names nothing and is 8.3-shaped, so it gets hostperm's fail-closed unresolvedShortName refusal. These summaries are among the most common Bash previews. a357d187 showed them, and section 6 now tells the model they name a refused path.
  - Evidence: Real adapter, TestZZDaemon_Shapes. Exact deny rules on private/John's notes.txt, private/my secret.txt and .env, none of which these summaries name. All are WITHHELD at HEAD:
- git diff HEAD~1
- git reset --soft HEAD~2
- git log HEAD~3..HEAD --oneline
- git show HEAD~1:src/main.go

The lexical evaluator reproduces Evaluate's verdict here (0 disk evaluations), so this is a semantic choice, not a cost.
  - Fix: Get the coordinator's ruling. One option that keeps re_read's fail-closed 8.3 semantics: for section 6 only, do not withhold a summary because one of its words is refused solely as an unexpandable 8.3 name that names nothing in the project. A summary word is not a read; the adapter could tell this refusal apart through a dedicated Effect or flag rather than the diagnostic Rule string. Add a row with HEAD~N shapes under the three-rule fixture.

