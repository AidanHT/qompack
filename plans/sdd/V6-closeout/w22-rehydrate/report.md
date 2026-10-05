# Wave 22 rehydrate seat

Branch `closeout/w22-rehydrate`. Workflow `wf_1246af7f-f54`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **blocker** `internal/rehydrate/pathgate.go:1030 (pathNamedWithheld), containment via inside() at :481; reachable for every non-built-in/MCP tool pointer`: A path-named JSON value (path/file/paths/dir/cwd/… or an element of such an array) that holds MORE THAN ONE path — space- or comma-separated — is judged as ONE path. When the value's first segment is relative (so containment reads the whole string as an in-project relative path) but a later piece is an out-of-project absolute, POSIX-root, home (~), env ($HOME), Windows drive-relative or PowerShell-provider path, neither containment, nor the host (it joins the whole nonsense string and Evaluates it to Allow), nor the name screen (the embedded path spells no rule literal and no recorded withheld name) catches it, and the summary is shown verbatim. This is exactly the out-of-project path disclosure D50/D63 exist to prevent. Path-named values are exempt from the free-text whitelist (D63(1)), so nothing else screens them. D63 §23 item 6's completeness assumes 'a path-named value is one path' ("Read/Write/Edit take an absolute file_path, so a rooted plain path is judged as the one file it names"); a value that is a path LIST or a relative path followed by whitespace+absolute breaks that assumption. Not one of the three known-being-fixed w19h items.
- **blocker** `internal/daemon/rehydrate_hostpaths_test.go:866 (TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules), with shortProjectDir :540-555 and storePreview :557-565`: Hosted `test (macos-latest)` will almost certainly fail tonight. On macOS, shortProjectDir builds the root from os.MkdirTemp("", "q") with no shortening; it canonicalises only on Windows. The macos-latest TMPDIR is 48 characters plus its trailing '/'. The fixture command `docker run -v "<root>:/src" -w /src img go test ./...` with root <TMPDIR>q<digits>/John Smith/proj is 113+d characters, where d is MkdirTemp's random suffix length. For d>=8 (about 99.8% of uniform uint32 suffixes) that exceeds the store's 120-character preview width, so storePreview's require.NotContains("…") fails before Build runs. EXPECTED_FAILING_ROWS and EXPECTED_FAILING_PACKAGES are empty, so the job goes red. No test of candidate 8 has ever run on macOS: the w19 reports say 'macOS is still unrun (GOOS=darwin vet only)'.
- **major** `internal/rehydrate/pathgate.go:283 (newPathJudge drop loop) and pathgate.go:2549 (gateCheckpointDrops); cost doc at internal/daemon/rehydrate_service.go:762-769 and ADR 0011 §23 item 10`: Performance regression since candidate 7 (introduced by 00f324f1/7f90567f). Every path-keyed checkpoint drop entry (file_pointer, pointer_missing, pointer_invalid, pointer_untracked, pointer_dirty) now costs one host judgement through RuleSet.Evaluate, which takes about 1 ms on Windows and is not cached, and two Evaluates when the root resolves elsewhere. The number of drops has no bound. Draft.upsertFilePointerLocked (checkpoint/writer.go:1237) collects every distinct captured file, and Truncate cuts down to the 12K-token checkpoint budget with one file_pointer drop per pointer it cuts, so a large session hands Build hundreds or thousands of such drops. Build time then grows linearly with the drop count and approaches the 5 s compactAnswerBudget. Past that budget the model gets the deferred note instead of the rehydration. At a357d187 the build cost stayed flat at any drop count. No test covers the scaling: the cost rows carry no drops, and the doc's 'at most two Evaluates for each of those' gives no overall bound.
- **major** `internal/rehydrate/pathgate.go:2333 (holdRoot; called through markRoot :2319 from :359, :629, :667, :692, :855, :1049, :1052, :1111 and :2606); internal/rehydrate/build.go:287 and :314 (collectDrops runs twice per build, and each run calls budget.go:514 gateDropReasons, which calls pathgate.go:2589 reasonWithheld once for every drop); pathgate.go:1917/1932 (the selectorValues regex runs on every text)`: rehydrate.Build uses 2.6-2.9x more CPU in the common case, a project with no Read deny or ask rules (3.1x on battery). Allocations are 3x. The cause is c8's D63/D64 summary screen: about 2,630 new lines in pathgate.go. The root-spelling regex (rootSpellingOf, case-insensitive with no literal prefix) runs 2-4 times on the same summary text. reasonWithheld runs on every drop entry, twice per build, with no memo, although almost every truncation drop carries the same detail string. A long session crosses the 13 ms rehydrate.build p99 figure that session_start_compact.go:24 still states. Candidate 7 stayed at about 3-4 ms. No gate fails, because compactAnswerBudget is 5 s.
- **minor** `internal/rehydrate/pathgate.go:282-286 (newPathJudge judges every path-keyed checkpoint drop) and :2537 gateCheckpointDrops; internal/rehydrate/items.go:1306/1339 restorableRules and :1444/1504 indexableSkills; internal/hostperm/policy.go:276 (Evaluate passes a nil linkMemo, so every call readlinks every ancestor)`: c8 adds host judgements that candidate 7 never made: one per distinct path-keyed checkpoint drop (D60(i)), and one per rule file and skill file that items 6a and 6b would restore. Their number has no cap. On Windows each judgement is a full on-disk hostperm Evaluate: about 0.9 ms idle and about 5 ms under co-load. A long session in a large repo with any Read deny or ask rule can therefore push the build toward the 5 s compactAnswerBudget. At that point the session gets the deferred note instead of its rehydration. Overall the rules case is still faster than c7, because free text no longer asks the host.
- **minor** `internal/daemon/session_start_compact.go:24; internal/rehydrate (no Benchmark*); internal/daemon/rehydrate_hostpaths_test.go:295-304 (the cost row logs wall time and never judges it); plans/sdd/V6-remediation/inventory-current.tsv row 1.11.16`: Nothing measures rehydrate.Build's cost, and one code comment states a figure that is no longer true. internal/rehydrate has no benchmark. The only timing is the cost row's log line, 'logged, not judged'. c52derive.py selects C5.2 re-measurements per benchmark, so the C5.2 night cannot see the screen's cost. That is how the 2.6-2.9x regression above reached the freeze unnoticed. session_start_compact.go:24 still states 'rehydrate.build p99 13 ms' as current.
- **minor** `internal/daemon/session_start_compact.go:24`: The comment's claim 'rehydrate.build p99 13 ms' is stale. It was measured before D50/D63 host judgements existed. Each judged path now costs about 1 ms of RuleSet.Evaluate, so a realistic build is 15 to 25 times slower than the number the file quotes when it reasons about the compaction-answer route. The build still fits well within the 5 s budget when there are few drops; see the major finding for the case with many.
- **minor** `internal/daemon/session_start_compact.go:22-31; internal/cli/sessionstart_compact_load_test.go:31-33`: A product comment still states the C1.16 diagnosis as measured fact: observer-lock waits behind the SAME session's tool results, p50 229 ms and p99 918 ms. It cites a test that does not exist (TestSessionStartCompact_LoadRig) and w2-lifetime/runs. 3f2da1b3 established that in those runs the rig's Reads never reached the daemon, so no same-session ingest was measured. D65(a) marked only the docs/architecture.md figure. The w21-night seat flagged these two comments and left them as outside its files.
- **minor** `internal/rehydrate/pathgate.go:2608-2615 (reasonWithheld field loop); source reason at internal/checkpoint/finalize.go:216`: The drop-reason screen reads a leading-slash single-segment token as an absolute POSIX path outside the project, so a slash command embedded in a checkpointer reason is misread as an out-of-project path and the clause is redacted to '(path withheld)'. The finalize pin-reread-failure drop ('invariants'/pins) ends with 'run /qompack:pin --list: <err>'; the token '/qompack:pin' is absLike (leading '/') and inside()=false, so reasonWithheld fires and redactReason replaces the whole clause, hiding the genuine recovery instruction and the underlying error. Summaries already guard this exact shape with driveLessPath() (a single POSIX segment like /review is treated as not-a-path), but reasonWithheld uses plain absLike+inside with no such guard. Over-withholding on a degraded path, not a leak.
- **minor** `internal/rehydrate/pathgate.go:2501 (withheldSummary) and :2500 (withheldPathLabel), rendered by items.go:1147-1150`: Every withheld pointer line repeats a 97-character explanation: 'summary withheld: it names a path the host's permission rules refuse, or one outside the project'. The withheld file label repeats about 84 characters. That text is often longer than the summary it replaces, and it is charged against the fixed 9,400-character payload ceiling (hostcap.go PayloadCeilingChars). Because D63 withholds about one summary in five, and about one in three among JSON/URL previews, the boilerplate pushes real pointers out of section 6.
- **minor** `internal/rehydrate/pathgate.go:1734 (providerPath) and :1761 (inertPrefixes)`: Usefulness gap in a common Claude Code shape. Any `name:value` at a path start reads as a PowerShell drive, so ToolSearch's own `select:` previews are withheld in every project, including projects with no deny rules. Claude Code issues those calls to load deferred MCP tools, Qompack's own among them. The live UAT-05 capture shows `{"max_results":1,"query":"select:mcp__plugin_qompack_qompack__record_eliminated"}`. The same rule withholds docker image tags, npm script names, port maps, git revision paths and pytest node ids. ADR 0011 §23 lists most of these as deliberate limits but does not name ToolSearch's `select:`. inertPrefixes already accepts that a user-defined PowerShell drive named `path` or `sha256` is out of scope.
- **minor** `internal/rehydrate/pathgate.go:2225-2233 (rootSpellingOf folds case via paths.DefaultFold, which is true on darwin) vs pathgate.go:495-497 (inside() uses filepath.Rel, which is case-sensitive on darwin); same split in rootPrefix (1128-1140) and exactRooted`: On macOS the screen gives a case variant of the project root two different verdicts. Free text treats a case-variant spelling of the root as the project, through the (?i) root regex: `cat /Users/u/PROJ/notes.txt` under root /Users/u/proj is SHOWN. The same path as a structured value goes through inside(), whose filepath.Rel on darwin is case-sensitive, so it is WITHHELD. On the default case-insensitive APFS this only over-withholds the structured form. On a case-sensitive APFS volume (an opt-in setting) /Users/u/PROJ is a different directory outside the project, and the free-text verdict then shows an out-of-project path, which breaks D50's rule. No doc states that Qompack assumes a case-insensitive volume on macOS: grep -i 'case-sensitive' docs/*.md docs/adr/0011* finds nothing. Wave 19h's fix for known item (1) is scoped to Windows, but darwin builds the same (?i) regex. On APFS, U+212A and U+212B decompose canonically to K and Å, so there a Unicode fold over-withholds and does not leak. Whatever 19h changes should state its darwin behaviour.
- **minor** `docs/adr/0011-rehydration-budget-and-item-order.md:878-883 and :1286-1291`: (1) A 'Deliberate limits' bullet is stale. It says a best-fit modifier letter (U+02BA to `"`, U+02B9/U+02BC/U+02C8 to `'`) 'is a letter to the whitelist ... D64 does not change it, and it is open for a coordinator ruling'. 28637640 closed that: ADR :1142-1147, security.md:93-95 and pathgate.go's wordRune/bestFitPunct now make those letters unsafe. The bullet was last edited in a3e6435e, before the fix. (2) :1286-1291 leaves a character the ANSI code page cannot hold (it reaches an ANSI-argv program as the glob `?`, e.g. `de统y.txt` as `de?y.txt`) 'open for a coordinator ruling'. D64 and D65 give no ruling, and the user-facing limit lists (security.md:131-138 limits sentence, cannot-do.md section 5) do not mention it.
- **minor** `docs/adr/0011-rehydration-budget-and-item-order.md:986-994 (item 6, 'the owner is asked to ratify it') and :1287-1291 ('open for a coordinator ruling'); w19-rehydrate/report-19d.md Needs owner`: ADR 0011 at HEAD still names open owner rulings that no ledger row gives. (1) The lone-glob relaxation of D63's 'a one-word summary must ALSO pass the free-text whitelist' allows `* ? [ ]` and one level of `{a,b}` in pattern one-words and root-led Glob previews. It is a privacy-relevant loosening, and the ADR says 'the owner is asked to ratify it'. (2) The ANSI default character `?`: a program that globs its own arguments receives `de统y.txt` as `de?y.txt`. The ADR says 'it is open for a coordinator ruling'. (3) The 19d needs-owner list asked for confirmation of the round-2 extensions to D63(2) (glued `;`, `|`, `&&` and `||` splitting; parentheses inside quoted runs; an apostrophe between letters; single-quoted runs; a single `%`) and of the stricter-than-letter `=`, `#`, `@name` and JSON-key screening. D64's eight rulings cover none of the three.
- **minor** `docs/adr/0011-rehydration-budget-and-item-order.md:879-883 ('Deliberate limits' list)`: The ADR's current-limits list says a Unicode modifier letter that best-fit turns into ASCII punctuation (U+02BA, U+02B9, U+02BC, U+02C8) 'is a letter to the whitelist ... D64 does not change it, and it is open for a coordinator ruling'. The code and the ADR's own D64 paragraph say the opposite: since 28637640 those code points are unsafe in the whitelist and the root unit on every platform.
- nit `internal/rehydrate/pathgate.go:390 (note: basename of every withheld path, outside paths included, goes into knownText)`: Usefulness cost that ADR §23 item 7 documents as D61 over-withholding. One Read of a file outside the project teaches its basename to the free-text screen, even with no host rules at all. Typical outside files are a module-cache README.md or a stdlib server.go. Every later Bash, Grep or recall summary that names a project file of the same name is then withheld. Reading dependency sources outside the project is routine in Go, Node, Python and Rust work.
- nit `internal/rehydrate/pathgate.go:2093 (literalOf, litWhole screen in free text)`: Accepted over-withholding (D61, documented). A directory rule's whole-segment literal withholds unrelated free text. In this corpus, `kubectl get secrets -n staging` is withheld by `Read(./secrets/**)` in every deny project.
- nit `internal/daemon/drain.go:985 (sum := spoolLineSum(raw) on every line read) and :1471 (spoolLineSum uses fnv.New64a)`: Wave 20's memo computes a byte-at-a-time FNV-64a sum of every spool line a pass reads. That includes fresh lines that the memo neither holds nor will compare. The sum lives only in memory, so its speed is all that matters.
- nit `test/integration/hotpath_test.go:161 (hotpathHarnessBound = 6 min) and :1117-1131`: Pre-existing; test/integration and test/bench are unchanged since d20309c0. When the harness hits its 6-minute bound, exec.CommandContext kills it. The row then reports 'either a gated budget ... breached ... or the harness itself failed' with an empty stderr, which does not say that a timeout happened. The harness's daemon also outlives the harness and still holds files when TempDir cleanup runs.

## impl:rehydrate: status `done`, head `ec6e3ccd765c343891c302084e67b49516bf1b0d`

### Summary

Wave 22 rehydrate seat. Branch closeout/w22-rehydrate (worktree qompack-cx-w22-rehydrate), base eca33155, HEAD ec6e3ccd: 10 commits, all unpushed and unmerged. Every blocker, major and assigned minor is fixed. Nits were not taken. Each fix has a row that fails on eca33155 first; each row's commit is in findings_resolution.

WHAT CHANGED
- #26 (blocker), pathgate.go. A path-named JSON value is now checked piece by piece, with no host call:
  - It is split at whitespace, `,`, `;` and `|` (valueNamesOutside, pieceOutside).
  - A piece is withheld if it is outside the project, is a PowerShell drive, or has a rooted path or a drive after an inner `:`, `=` or `@`.
  - The project root is matched at each piece start the way containment matches it (rootSpanAt), so a single project path with a space stays shown under any root, including roots no text spells exactly.
  - If the store's cut falls inside the root's own spelling, the value still counts as the project.
  - Class extension: a lone value like `{"path":"Temp:secret.txt"}` is now withheld.
- #36 (blocker), daemon tests only.
  - shortProjectDir now uses /tmp off Windows.
  - The Docker fixture is shortened; `:/src` still makes it withheld.
  - storePreview and storePreviewOf now check every preview against the longest temp directory a hosted runner uses (macOS 48 characters plus a 10-digit suffix; windows-latest's TEMP in long form).
  - New regression row: TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory.
  - Red evidence: in the Linux container (TMPDIR set to the macos-latest path) both this row and the original CommonIdioms row failed with the old fixture. On Windows the row passes at base, because windows-latest's TEMP is only 39 characters.
- #28 (major).
  - While a Read rule's pattern is in force, a build asks the host about at most 64 fresh drop paths, in checkpoint order. Every later drop is answered as refused without a call and memoized, so section 7 and dropped() withhold it (fail closed).
  - A drop past the cap is learned as a withheld path only if its spelling contains a rule's literal. The first version learned every one; BenchmarkBuild showed that made the screen quadratic (about 0.3 s) and over-withheld even in projects with no rules. I amended my own unpushed commit before handing back.
  - With no Read rule, the host's rules are empty and cost nothing, so every drop is judged as before.
  - The cap sits in hostRefuses while drops are being read, so any judgement made for a drop counts, including the ones 19i's note() adds.
  - Result: 1000 drops cost 84 host judgements in about 0.16 s through the real adapter, down from 1020 in 2.55 s.
  - The cost comment in rehydrate_service.go and ADR item 10 are updated.
- #33 / #34 / #35. A per-build memo shared by every copy of the judge:
  - Held-root text, keyed by (root expression, text). Each reading of the root has its own expression, so the key carries the fold mode, as 19i needs.
  - Each distinct summary's verdict.
  - Each distinct drop reason's screen result. The summary and reason memos are created only after newPathJudge finishes learning.
  - Two prefilters skip a regex when it cannot match:
    - the root's last segment, folded ASCII-only where paths fold (19h's rule);
    - `path:` in any ASCII case (no non-ASCII letter folds onto these under RE2's `(?i)`; checked with SimpleFold).
  - holdRoot's body moved into holdWith(spelling, t), spelled exactly as 19i spells it.
  - The drop list is still gated twice (steps 9a and 10), because min-fill re-admits item 2 units in between; the second pass now hits the memo.
  - New BenchmarkBuild reports p50 and p99 per build.
  - New row TestBuild_TheScreensMemoNeverChangesAnAnswer. A mutant prefilter that does not fold ASCII case fails it.
- #27: in a drop reason, Qompack's own slash commands (`/qompack:<name>`) are read without their leading `/`. Every other word starting with `/` is still judged as a path. The edit sits outside 19i's hunk.
- #30: section 6 shows one legend line under its heading when any pointer it was built from is withheld. The legend is priced exactly in both tokens and characters. Labels are now `(summary withheld)` and `file (path withheld)`. Drop entries keep the full note. No golden changed.
- #31 (D67(l)): `select` is added to inertPrefixes.
- #29 / #71: session_start_compact.go and the rig header in internal/cli are comments only. They now name TestSessionStartCompact_UnderSameSessionIngest, say the figures predate 3f2da1b3 (D65(a)), and restate the build cost from BenchmarkBuild.
- Docs (ADR 0011 §23 and security.md):
  - New item 12.
  - Item 6 gains the several-path rule.
  - Item 10 gains the drop cap.
  - D67(l) and (m) replace the open questions (#68): the lone-glob relaxation and 19d's extensions are ratified, and the exact-path over-withholding is kept.
  - The stale modifier-letter bullet now records D64's closing of it (#42, #69).
  - The ANSI default character and a case-sensitive APFS volume are listed limits (#42, #68, #85).

CORPUS (w19d, 274 previews × 10 scenarios; base eca33155 and HEAD give identical counts; format: shown/withheld, leaks, over-withheld)
- Windows:
  - none-plain 209/65, 0, 52
  - uat12-plain, uat12-spaced, uat12-nonascii, uat12-long: 193/81, 0, 53
  - common-plain, common-spaced: 193/81, 0, 54
  - uat12-onedrive, uat12-dashword, uat12-bestfit: 149/125, 0, 97
- Linux (non-root, container):
  - none-plain 208/66, 0, 53
  - uat12-*: 192/82, 0, 54
  - common-*: 192/82, 0, 55
  - onedrive, dashword, bestfit: 149/125, 0, 97
- Flips: none, in any scenario, on either platform. The corpus has no multi-path path-named value and no ToolSearch `select:` preview, so neither of the two allowed flips occurs.

BENCHMARK (BenchmarkBuild on a co-loaded Windows host, n=6 interleaved, eca33155 → HEAD; the host is an in-memory stand-in)

| Fixture | p50 (ms) | p99 (ms) |
|---|---|---|
| norules, 0 drops | 6.20 → 4.94 (no significant change, p=0.13) | 10.56 → 10.64 (no significant change) |
| norules, 600 drops | 15.46 → 6.29 (−59%) | 23.0 → 19.5 (no significant change) |
| norules, 1000 drops | 27.14 → 9.43 (−65%) | 41.8 → 18.9 (−55%) |
| uat12, 0 drops | 7.68 → 5.03 (−34%, p=0.041) | 12.8 → 12.2 (no significant change) |
| uat12, 600 drops | 20.94 → 6.99 (−67%) | 26.4 → 14.8 (−44%) |
| uat12, 1000 drops | 33.23 → 8.31 (−75%) | 48.0 → 16.2 (−66%) |

Allocations fall 6–26%. I could not check the "within 1.5x of candidate 7" target: there is no candidate 7 baseline on this fixture.

CHECKS AT HEAD ec6e3ccd
- go vet passes for windows, linux and darwin.
- golangci-lint passes (after waiting for another seat's lock).
- fmt-check, test/docs, docmarkers and runpatterns pass.
- Windows full run passes: rehydrate/..., daemon (563 s), cli.
- Linux non-root gate (-race, GOMAXPROCS 2):
  - rehydrate 683 pass, rehydratetest 14 pass, cli 515 pass.
  - daemon hit the 30-minute timeout with no failures, as other seats' runs did. Re-run with a 60-minute timeout: 1726 pass, 0 races, 1 fail. The failure is TestSpoolWatch_AClientSpoolSlowerThanAPassBudgetIsPublished, a load-sensitive spool row in a file I did not touch. It passed at the same SHA in the first run (5.2 s) and passed 5/5 under -race in isolation.
- New and changed rows: Windows -count=20 gives 220/220, -race -count=3 gives 33/33. Linux -count=20 and -race -count=3 all pass.
- Container scratch I created is removed: /var/folders, /work/w22rh-red, /tmp/w22rh-home.

MERGE NOTES FOR 19i
- pathgate.go holdRoot: keep my one line (`return j.held(j.rootSpelling, t)`). holdWith is byte-identical to 19i's. holdRootBroad can call j.held(j.broadSpelling, paths.Key(t)) to share the memo; the prefilter never skips the broad reading.
- reasonWithheld: my only change is its first line.
- rehydrate_service.go: both branches edited the same cost comment. Merge 19i's "three Evaluates" with my drop-term text.
- ADR: my edits avoid 19i's hunks; item 12 sits after item 11.

### Commits

- 90ff0fd9 fix(rehydrate): judge each piece of a path-named value
- 7bd34725 test(daemon): fit root previews under hosted temp dirs
- 9de6387a fix(rehydrate): bound host judgements of checkpoint drops
- bd72c381 perf(rehydrate): memoize the screen per build
- d8f52bab fix(rehydrate): keep qompack commands in drop reasons
- 3e0f8e4c fix(rehydrate): read ToolSearch's select: as inert
- 91687fae fix(rehydrate): explain withheld pointers once in section 6
- 408e45ce docs(daemon): restate the compact route's stale figures
- c5fd576c docs(adr): record wave 22's rehydrate fixes and rulings
- ec6e3ccd docs(rehydrate): say the legend follows section 6's candidates

### Findings resolution

- **fixed**: #26 (blocker) path-named value holding several paths judged as one
  - 90ff0fd9. Red-first rows: TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece and TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece, both red on eca33155 ('the payload shows a withheld path'). Covers the audit's 8 shapes plus the rest of the class: `;` and `|` separators, a path after an inner `:`, `=` or `@`, %VAR%, a climb, a sibling of the root, a cut later piece, a lone provider drive, the multi-valued branch. Single project paths with spaces stay shown under plain and spaced roots; the existing D64 rows (non-exact roots, a cut inside the root) still pass. Corpus: 0 flips on Windows and Linux.
- **fixed**: #36 (blocker) macOS fixture exceeds the preview width under a 48-char TMPDIR
  - 7bd34725. shortProjectDir uses os.MkdirTemp("/tmp",...) off Windows; Docker fixture shortened; storePreview and storePreviewOf now require every preview uncut under the longest hosted temp base. Regression row TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory and the CommonIdioms row were red in the Linux container (TMPDIR=/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T) with the old fixture; after the fix all 26 host-path rows pass there. A hosted macOS run is still owed (D67(n)).
- **fixed**: #28 (major) one uncached host Evaluate per path-keyed drop makes Build cost unbounded
  - 9de6387a, amended once on my own unpushed branch before handing back. The cap was fail-closed and is unchanged. Learning first applied to every unjudged drop: that was quadratic (about 0.3 s) and over-withheld in no-rules projects, so it now applies only to drops whose spelling contains a rule's literal. Cap is 64 fresh drop judgements while a Read rule is in force; later drops are withheld unjudged, and learned only if their spelling names a rule literal; no-rules projects are unchanged. Red rows: TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements (1003 calls on eca33155 vs bound 65) and the new 1000-drop variant of TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers (1020 judgements / 2.55 s, now 84 / 0.16 s). The cost comment in rehydrate_service.go and ADR §23 item 10 are updated.
- **fixed**: #33 (major) Build CPU 2.6-2.9x
  - bd72c381. Per-build memo: held root keyed by (expression, text), so the fold mode is in the key; summaries; reasons (made only once learning ends). Root-tail prefilter folds ASCII-only (19h); `path:` prefilter. The drop list is gated twice by necessity (min-fill changes it between steps); the second pass hits the memo. Pure performance, so there is no verdict to show red: evidence is BenchmarkBuild (600 and 1000 drops 59-75% faster at p50, p<=0.002, n=6) and TestBuild_TheScreensMemoNeverChangesAnAnswer, which a narrow-fold mutant fails.
- **fixed**: #34 (minor) uncapped host judgements per drop, rule file and skill
  - The term that grows with the session (drops) is capped by 9de6387a; rule and skill files are bounded by project configuration, file pointers and summaries by the checkpoint budget. The path-drop variant was added to the cost row, with its wall time logged. The per-build hostperm linkMemo option was not taken: hostperm is outside this seat's files and D61(b)(4) keeps it at a357d187.
- **fixed**: #35 (minor) no benchmark for rehydrate.Build
  - bd72c381 adds BenchmarkBuild (norules/uat12 × 0/600/1000 drops, reporting p50-ns and p99-ns); before/after figures are in the summary. Adding it to the C5.2 set (c52derive.py, inventory row 1.11.16) is outside this seat's files; listed in needs_owner.
- **fixed**: #29 (minor) stale 'rehydrate.build p99 13 ms'
  - 408e45ce. The comment now says the figure predates the D50/D63 screen and restates the cost from BenchmarkBuild and the adapter cost rows. Comment only.
- **fixed**: #71 (minor) C1.16 figures stated as fact, nonexistent test cited
  - 408e45ce. session_start_compact.go and the internal/cli rig header now name TestSessionStartCompact_UnderSameSessionIngest and say the figures predate 3f2da1b3 (D65(a)). Comments only.
- **fixed**: #27 (minor) slash command in a drop reason read as a POSIX path
  - d8f52bab. unslashCommands removes the leading `/` from `/qompack:<name>` words before screening only; the output keeps the original text. Red row TestBuild_AQompackCommandInADropReasonIsNoPath (eca33155 produced '(path withheld): pins: store closed'). The row also checks that /repo.git, /secrets.txt, /etc/... and `/qompack:pin/../../etc/passwd` stay redacted.
- **fixed**: #30 (minor) 97-char explanation repeated on every withheld line
  - 91687fae (plus ec6e3ccd for wording). One priced legend line under section 6's heading; labels `(summary withheld)` and `file (path withheld)`. Daemon rows now match the exact new label plus line end, and the `— [^s]` check became `— [^s(]`. Red row TestBuild_AWithheldPointerIsExplainedOnceInItsSection. No golden changed.
- **fixed**: #31 (minor) ToolSearch select: withheld everywhere
  - 3e0f8e4c, per D67(l): `select` added to inertPrefixes. Red row TestBuild_ToolSearchsSelectorIsShown (with and without rules); select:/etc/passwd, select:../x, select:C:\x and selector: stay withheld. The corpus has no select: preview, so 0 flips.
- **fixed**: #85 (minor) macOS case model; case-sensitive APFS undocumented
  - c5fd576c, per D67(m): the case-insensitive volume assumption is documented in docs/security.md and ADR §23 item 2. No code change was needed: at eca33155 RootRelative already folds ASCII case on darwin (19g/19h), so structured values and free text agree. A darwin row awaits a hosted macOS run.
- **fixed**: #42 (minor) stale best-fit bullet; ANSI default character unruled
  - c5fd576c. The ADR bullet now records D64's closing (bestFitPunct). The ANSI default character is recorded as a D67(l) limit in the ADR (item 2 list and item 7) and in security.md's limits sentence. The matching line in docs/cannot-do.md §5 belongs to the docs seat; listed in needs_owner.
- **fixed**: #68 (minor) ADR names open owner rulings
  - c5fd576c. 'The owner is asked to ratify it', 'the owner is asked whether...' and 'open for a coordinator ruling' are replaced by D67(l): lone-glob relaxation and 19d's extensions ratified, exact-path over-withholding kept, ANSI default character accepted as a limit.
- **fixed**: #69 (minor) ADR limits list contradicts the code on modifier letters
  - c5fd576c. The bullet now says the code points are excluded from the whitelist and the root unit (bestFitPunct) and keeps only the fullwidth/compatibility residual.

### Tests

- `go test -p 2 -count=1 -overlay <rows on base> -run '^(TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece|TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements)$' ./internal/rehydrate/ (in base eca33155 tree)`: FAIL as intended (red first): payload shows a withheld path; 1003 host calls vs 65
- `go test -p 2 -count=1 -run '^(TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece|TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements|TestBuild_AQompackCommandInADropReasonIsNoPath|TestBuild_ToolSearchsSelectorIsShown|TestBuild_AWithheldPointerIsExplainedOnceInItsSection)$' -v ./internal/rehydrate/ (rows before fixes)`: FAIL on base code for all five rows, as intended
- `daemon.test -test.run '^(TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory|TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules)$' in the Linux container as qtest with TMPDIR=/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T (red state, then fixed)`: red state: both FAIL (store cut the preview); fixed: all 26 TestRehydrateHostPaths_ rows PASS
- `GOOS={windows,linux,darwin} GOARCH=amd64 go vet ./internal/rehydrate/... ./internal/daemon/ ./internal/cli/`: PASS on all three
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: PASS
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/... ./internal/daemon/ ./internal/cli/ (Windows, HEAD ec6e3ccd)`: ok rehydrate 7.3s, rehydratetest 3.0s, daemon 563.0s, cli 137.4s
- `sh linux-nonroot-gate.sh --out .../wave22/linux-rehydrate ec6e3ccd w22-rehydrate --gomaxprocs 2 -- ./internal/rehydrate/... ./internal/daemon ./internal/cli`: rehydrate PASS (683), rehydratetest PASS (14, 1 skip), cli PASS (515); daemon hit the 30m timeout with 1538 pass and 0 fail
- `sh linux-nonroot-gate.sh ... ec6e3ccd w22-rehydrate-daemon60 --gomaxprocs 2 --timeout 60m -- ./internal/daemon`: 1726 pass, 0 races, 1 fail: TestSpoolWatch_AClientSpoolSlowerThanAPassBudgetIsPublished (load-sensitive; spool_watch untouched by this seat)
- `sh linux-nonroot-gate.sh ... w22-rehydrate-spoolrow --run '^TestSpoolWatch_AClientSpoolSlowerThanAPassBudgetIsPublished$' --count 5 -- ./internal/daemon (race)`: PASS 5/5
- `go test -p 2 -count=20 -run "$(cat rows.pattern)" ./internal/rehydrate/ ./internal/daemon/ (Windows; 11 new or changed rows)`: 220/220 PASS
- `go test -race -p 2 -count=3 -run "$(cat rows.pattern)" ./internal/rehydrate/ ./internal/daemon/ (Windows)`: 33/33 PASS, no data race
- `linux-nonroot-gate.sh ... w22-rehydrate-rows-x20 --no-race --run <rows> --count 20 -- ./internal/rehydrate ./internal/daemon`: PASS (rehydrate 360, daemon 300 incl. subtests)
- `linux-nonroot-gate.sh ... w22-rehydrate-rows-race3 --run <rows> --count 3 -- ./internal/rehydrate ./internal/daemon (race)`: PASS (rehydrate 54, daemon 45)
- `RV2_OUT=... go test -overlay <corpus> -run '^TestZZRev2Corpus$' ./internal/rehydrate/ at eca33155 and HEAD, Windows and Linux (scratch harness, not committed)`: identical counts at base and HEAD on both platforms, 0 leaks, 0 flips <!-- runpatterns: a scratch probe the seat ran from an overlaid test file to measure the corpus or a fix; it is not committed, and its numbers are recorded on this line -->
- `BenchmarkBuild interleaved base vs HEAD, -test.benchtime=30x, 6 rounds, benchstat`: 600/1000 drops -59% to -75% p50 (p=0.002); allocs -6% to -26%; no-drop builds -20% to -34% p50 (uat12 p=0.041)

### Criterion changes

- #26 class extension: every path-named JSON value is now also checked piece by piece. A lone value that is a PowerShell drive path ({"path":"Temp:secret.txt"}) or has a rooted path after an inner `:`, `=` or `@` is now withheld; it was shown at eca33155. Rationale: same out-of-project disclosure class (D50/D63). Corpus flips: 0.
- #28: while a Read rule is in force, path-keyed checkpoint drops beyond the first 64 fresh paths are withheld unjudged (fail closed) and learned only when their spelling names a rule literal. Rationale: bounded cost (2.5 s → 0.16 s for 1000 drops); over-withholding accepted per D66(e). Projects with no Read rule behave as before.
- #31 (D67(l)): `select:` previews are now shown.
- #30: withheld labels are now `(summary withheld)` and `file (path withheld)`, with one legend line in section 6. Daemon rows that matched the old 'summary withheld' text now match the exact new label including the line end (stricter). The `— [^s]` shown check became `— [^s(]` so it still excludes the new label.
- #36: the CommonIdioms Docker fixture is shortened to `docker run -v "<root>:/src" img`; it is still asserted withheld. storePreview and storePreviewOf now also require each preview uncut under the longest hosted temp base (stricter). shortProjectDir uses /tmp off Windows. The UsefulSummaries and CommonIdioms previews moved verbatim into shared builders.
- TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers: the expected count gains a min(drops, 64) term for the new 1000-drop variant. Existing variants are unchanged.

### Open issues

- Merging with wave 19i (closeout/w19-rehydrate) will conflict in a few places; resolution notes are in the summary. Correctness does not depend on which side's holdRootBroad routing is kept.
- No macOS run has exercised #36 or the darwin limit of #85. D67(n)'s pre-freeze hosted ci.yml run of closeout/integration with macOS should confirm test (macos-latest).
- On the shared container at GOMAXPROCS 2, the Linux daemon package under -race needs more than 30m (2529 s here). Other seats needed --timeout 60m too.
- TestSpoolWatch_AClientSpoolSlowerThanAPassBudgetIsPublished failed once under -race at container load average ~6, and passed in every other run including 5/5 in isolation. It is pre-existing and load-sensitive, in a file this seat did not touch.
- New over-withholding (accepted under D66(e)): in a project with a Read deny or ask rule, path-keyed drops beyond the first 64 fresh paths show in section 7 and dropped() as '(path withheld)' without a host judgement. Worth a line in the release notes.
- Narrow residual in #28: a drop past the bound that a rule refuses only through a link or 8.3 name, and whose spelling holds no rule literal, is not learned. A free-text name of it is then the documented alias limit (ADR §23 item 2).
- Could not check finding 33's 'within 1.5x of candidate 7' target: there is no candidate 7 baseline on this fixture. No-drop builds improved but are still spread across the whitelist (about 5 ms p50 on this co-loaded host).

### Needs owner

- Add BenchmarkBuild (internal/rehydrate) to the C5.2 benchmark set (c52derive.py) and update inventory row 1.11.16. Both are outside this seat's files (finding 35).
- docs/cannot-do.md §5 should gain the ANSI default-character limit that D67(l) accepted (finding 42). That file belongs to the docs seat; ADR 0011 and security.md already carry it.
- Run the hosted ci.yml check with macOS required by D67(n) before freezing, to confirm the #36 fix on macos-latest.

## review:rehydrate:r1: verdict `needs-fixes`, 3 finding(s)

- **major** `internal/rehydrate/pathgate.go:1400-1417 (pieceOutside), :1322-1359 (valueNamesOutside), reached from pathNamedWithheld :1265; docs/security.md:78-81 and ADR 0011 §23 item 6 claim the class is closed`: Finding #26 (blocker) is fixed for its eight sample shapes but not for its whole class. A path-named value is still shown when it holds several paths and a later piece names an outside path in either of two ways. (a) The piece starts with a quote, parenthesis or angle bracket and holds a POSIX-rooted, home or variable path, for example `"/etc/passwd"`, `'~/.ssh/id_rsa'` or `"$HOME/.aws/credentials"`: absLike and providerPath only look at the first byte, so inside() reads the piece as relative. (b) A climb follows an inner `=`, `@`, or the `:` after a name with a dot or slash, or after an inert name (`--out=../../x`, `@../x`, `a.ts:../x`, `select:../x`, `sha256:../x`): the delimiter loop checks only absLike(rest), and inside() of the whole piece cleans the climb away (`--out=..` is one segment that the next `..` removes). Free text's tokenOutside/hasDotDot withholds every one of these shapes. The seat defined its own class to include PATH-style lists, option values and climbs, and docs/security.md says 'a path after a `:`, `=` or `@` inside a piece' is judged. All of these were already shown at eca33155, so this is an incomplete class fix, not a regression. It still shows an outside absolute path, which is the D50/D63 breach #26 exists to close.
  - Evidence: Probes through rehydrate.Build in clean scratch copies of ec6e3ccd. With denyFiles(private/deny.txt), with no host, and under roots C:\q\proj and C:\q\John Smith\proj, these are SHOWN: {"paths":"\"src/a.ts\" \"/etc/passwd\""}, {"paths":"src/a.ts '/etc/passwd'"}, {"paths":"src/a.ts,\"/etc/passwd\""}, {"paths":["src/a.ts","\"/etc/passwd\""]}, {"paths":"src/a.ts '~/.ssh/id_rsa'"}, {"paths":"src/a.ts \"$HOME/.aws/credentials\""}, {"paths":"src/a.ts (/etc/passwd)"}, {"path":"\"/etc/passwd\""}, {"paths":"src/a.ts --out=../../outside/x.txt"}, {"paths":"src/a.ts @../outside/x.txt"}, {"paths":"src/a.ts:../outside/x.txt"}, {"paths":"src/a.ts select:../outside/x.txt"}. The same probe confirms the eight audit shapes are now withheld. Through the real adapter (daemon overlay test: uat12Project, rehydrateHostPaths over mcpOpHostPolicy, the store's own previews), these are SHOWN: {"paths":"src/main.go \"/etc/passwd\""}, {"paths":"src/main.go '~/.ssh/id_rsa'"}, {"paths":["src/main.go","\"/etc/passwd\""]}, {"paths":"src/main.go --out=../../outside/x.txt"}, {"paths":"src/main.go @../outside/x.txt"}. The control {"paths":"src/main.go /etc/passwd"} is withheld.
  - Fix: Close it fail-closed, as D66(e) directs. In pieceOutside, also run free text's per-token outside reading on each piece: readingsOutside(pc), which covers pathStartDelims and hasDotDot, together with tokenOutside for a quoted run. Or treat `" ' ( ) [ ] < > { }` as piece separators, or withhold any piece that holds one of them before a path start. After each inner `:`, `=` or `@`, withhold when !j.inside(rest) (a climb included), not only when absLike(rest). Add these shapes to TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece and to its daemon twin. Show them red at ec6e3ccd, re-run the w19d corpus, and list any shown-to-withheld flips.
- **major** `internal/rehydrate/pathgate.go:457-467 (newPathJudge drop loop: skipped drops are noted only when textNamesWithheld(..., screenExact) holds) and :653-664 (hostRefuses cap); commit 9de6387a`: This is a regression against eca33155 that shows a host-refused path. Past the 64-judgement cap, a path-keyed drop is withheld in sections 6 and 7 but is learned as a withheld name only when its spelling holds a rule's literal. At eca33155 every drop was judged by the host, and a refused one was learned whatever its spelling. Take a drop that the host refuses only through a link, junction or 8.3 alias, and whose spelling holds no rule literal (lnk/token.txt with lnk -> secrets, under Read(./secrets/**)). If it comes after 64 fresh drop paths, it is no longer learned, so a free-text summary or a recall selector that names it is shown. D66(e) accepts over-withholding but not showing a host-refused path. The seat records this as accepted in ADR §23 item 2 and in its open issues, but that does not make it compatible with D66(e). The trigger is a long session, which is exactly what finding #28 targets.
  - Evidence: Overlay probe TestZZVerifyProbe3 in clean scratch trees. The host stand-in refuses secrets/** and lnk/** (as hostperm does when it resolves a link), Patterns ["./secrets/**"]. The drop is file_pointer lnk/token.txt, preceded by 0 or 70 filler file_pointer drops. The summaries are `cat lnk/token.txt` and `{"query":"path:token.txt"}`. At eca33155 both are withheld with 0 and with 70 fillers. At ec6e3ccd both are withheld with 0 fillers and both are SHOWN with 70 fillers.
  - Fix: Keep the cap and still learn every skipped drop that a summary could name. Collect the checkpoint's tool summaries' name tokens once; their number is bounded by the checkpoint budget. Learn (note) a skipped drop whenever its basename or relative path occurs at a name boundary in that set, or spend a host judgement only on those skipped drops; that count is bounded by the summaries, not the drops. Either way the screen's cost no longer grows with the drops and nothing eca33155 withheld is shown. Add the alias case, with more than 64 preceding drops, as a red-first row.
- **minor** `internal/rehydrate/pathgate.go (buildMemo, held prefilter, selectorValues prefilter); finding #33's target`: Finding #33 asked for no-rules builds to be back within about 1.5x of candidate 7. The seat says it could not check this because no candidate 7 baseline exists. A baseline can be made by overlaying the same fixture on a357d187. Measured that way, the common no-drop, no-rules build is still about 2x candidate 7. The 600-drop and 1000-drop builds are about 1.5-1.7x. The large regression against eca33155 is fixed (3-4x faster with drops). No gate fails, and the absolute cost of about 5 ms is far inside the 5 s budget.
  - Evidence: BenchmarkBuild's fixture, overlaid identically on a357d187 (candidate 7, old HostPaths signature), eca33155 and ec6e3ccd, norules, -benchtime=30x/60x, 5 interleaved rounds on a co-loaded Windows host. p50 for drops0: c7 1.9-2.7 ms against HEAD 3.2-5.4 ms. drops600: c7 3.2-4.5 ms against HEAD 5.0-7.0 ms. drops1000: c7 4.6-5.4 ms against HEAD 7.0-10.3 ms. eca33155: drops0 6.0-7.0 ms, drops600 17.4-19.4 ms, drops1000 23.5-27.5 ms.
  - Fix: Either profile the zero-drop path further (for example memoize jsonStrings or splitTokens per summary, or prefilter the remaining whole-text regexes) until it is within about 1.5x, or record #33's target as a known minor with these figures, and add BenchmarkBuild to the C5.2 set as the seat's needs_owner item proposes.

## fix:rehydrate:r1: status `partial`, head `290b04be93a203a094d3db6dc1e61115564f294f`

### Summary

Wave 22 rehydrate seat, fix round 1. Branch closeout/w22-rehydrate (worktree qompack-cx-w22-rehydrate), now at 290b04be: 3 new commits on top of ec6e3ccd, all unpushed and unmerged. Both majors are fixed as classes, and each has a row that fails on ec6e3ccd first. The minor is narrowed and recorded as a known minor. One required check did not run: the Linux non-root gate. Docker Desktop is stopped (no engine pipe; `wsl -l -v` shows docker-desktop Stopped), and the brief forbids starting it. Everything else passed on Windows.

**MAJOR 1: finding 26's class (271b2660)**
- Cause: a later piece of a path-named value was checked only at its start, and after an inner `:`, `=` or `@` only for a rooted path. So `"/etc/passwd"`, `'~/.ssh/id_rsa'` and `(…)` read as relative, and a climb after `=`, `@` or `:` was cleaned away.
- Fix: `valuePathStart` now marks every place a reader starts a path in a piece:
  - after an opener anywhere (`" ' `` ` `` ( [ { <`), after `=` or `@`, and after a `:` that is not a drive's colon;
  - after a non-ASCII character that is not a letter, mark or digit (a zero-width space, for example);
  - after a run of punctuation that leads the piece (`>/x`, `!/x`, `+\x`, `#/x`, `)/x`);
  - after a leading short option (`-I/x`).
- Inside a piece, `+`, `#`, `!` and closing brackets are treated as name characters, so `c++`, `C#`, `+page`, `(auth)` and `[id]` stay shown.
- `restOutside` judges what runs from each start to the end of the piece, using:
  - containment, which resolves climbs: `--out=../../x` and `"../x"` are withheld, `src/../a.ts` is shown;
  - the same text with its closing quotes and brackets trimmed (`".."` is the parent);
  - both other readings of a backslash, for a path that is not rooted;
  - the PowerShell drive rule, unless another start lies inside the name before the colon (`namesDrive`).
- The root's own spelling is read whole at every start, and none of its characters starts a path. This also fixes over-withholding that ec6e3ccd introduced against eca33155: a quoted absolute project path, `--out=<root>/x` and `(<root>/x)` are shown again.
- Rows: 44 new withheld shapes and 17 shown guards in `TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece`.
  - It now runs under three roots (plain, `John Smith`, `O'Brien (x)`), with host rules and with no host.
  - Its daemon twin gains 10 withheld and 4 shown previews.
- All 12 shapes the verifier listed fail on ec6e3ccd.

**MAJOR 2: finding 28's regression (145a20f0)**
- A drop past the 64-judgement bound is now learned as withheld like any refused path, whatever its spelling (fail closed).
- `dropOrder` judges first the drops that a summary or a drop reason names (`textNames`/`dropNamed`). So a named drop the host allows is shown again in sections 6 and 7, as at eca33155. The bound and its count are unchanged: 1000 drops cost 84 real-adapter judgements in about 0.11 s.
- To keep the screen's cost from growing with the drops, `learnedIndex` reads the learned paths once learning ends:
  - a byte trie for names and for the paths in reasons;
  - NUL-joined keys for selectors;
  - each glob first checked against its literal.
- `appendDistinct` stops scanning past 64 entries. Every answer equals the per-path loop's: `TestLearnedIndex_AnswersAsTheLoopsDo` passed 40,000 random rounds as a stress run; the committed row runs 400. `TestLearnedIndex_AJudgeAnswersAsItsListsDo` checks it through a real judge.
- Red row `TestBuild_ADropPastTheJudgementBoundIsWithheldWhereverItIsNamed` covers the alias drop `lnk/token.txt` named six ways: free text, basename selector, part-of-path selector, glob, cut prefix and drop reason. It runs with 0, 70 and 1000 fillers. It fails on ec6e3ccd, and passes on eca33155 except for its bound assertion.

**MINOR: finding 33 against candidate 7 (290b04be): narrowed, then recorded as a known minor**
- Six allocation hot spots in the screen are removed, each pinned by a row comparing it with the code it replaced:
  - a glob replacer built per call;
  - a JSON decode of every string;
  - tokens copied byte by byte;
  - a slice of path starts per word;
  - an operator split on tokens with no operator;
  - unguarded anchored regexes.
- Zero-drop no-rules build: 13.1k allocations, against 21.1k at ec6e3ccd and 7.6k at candidate 7.
- p50 at GOMAXPROCS 2, 12 interleaved rounds, fixture overlaid on a357d187 (ms):

| Fixture | candidate 7 | ec6e3ccd | HEAD | eca33155 |
|---|---|---|---|---|
| norules, 0 drops | 2.43 | 4.73 | 4.11 | 5.90 |
| norules, 600 drops | 3.41 | 5.31 | 4.73 | 15.85 |
| norules, 1000 drops | 5.40 | 8.41 | 7.93 | 24.83 |
| uat12, 0 drops | n/a | 3.77 | 3.73 | 6.73 |
| uat12, 600 drops | n/a | 6.37 | 5.47 | 15.85 |
| uat12, 1000 drops | n/a | 8.44 | 9.75 | 25.65 |

- Ratios against candidate 7: 1.69x with no drops (ec6e3ccd 1.95x), 1.39x with 600, 1.47x with 1000. With every CPU, no drops measured 1.2x to 1.5x.
- uat12 at 1000 drops does not differ significantly from ec6e3ccd (focused A/B: 10.00 vs 10.53 ms, p=0.31).
- The remaining gap is the D63 whitelist's own work. ADR 0011 §23 item 12 records it as a known minor.

**Corpus (w19d, 274 previews, 10 scenarios)**
- Windows: identical counts at eca33155, ec6e3ccd and HEAD; 0 leaks and 0 flips in every scenario:
  - none-plain 209/65, over-withheld 52;
  - uat12 plain, spaced, nonascii, long: 193/81, 53;
  - common plain, spaced: 193/81, 54;
  - onedrive, dashword, bestfit: 149/125, 97.
- Linux corpus not run (Docker is down).

**Docs**
- ADR 0011 §23: item 6 (the path-start rule), item 2's limits (the past-the-bound residual is now accepted over-withholding), item 12 (finding 28's new design; finding 33's figures).
- docs/security.md: two paragraphs.
- Cost comment in rehydrate_service.go and the pathgate.go header.

**Merge notes for 19i (closeout/w19-rehydrate)**
- `note()`/`learn()` are untouched. The index is built from the final lists, so 19i's broad-fold entries are indexed automatically.
- `globSelectsKnown`: keep 19i's `broadKey` first line, then my `j.learned` branch.
- `appendDistinct` keeps its signature.
- My other edits are in `textNamesWithheld`, `cutPrefixNamed`, `selectsKnown`, `namesKnownIn` (index branches) and the drop loop in `newPathJudge`.

### Commits

- 271b2660 fix(rehydrate): judge a path wherever a value's piece starts one
- 145a20f0 fix(rehydrate): learn every drop past the judgement bound
- 290b04be perf(rehydrate): cut the screen's allocations on every summary

### Findings resolution

- **fixed**: major: finding 26's class still open (quoted, bracketed or led piece; climb after an inner =, @ or :)
  - 271b2660. valuePathStart, pieceOutside, restOutside and namesDrive judge a path wherever a reader starts one, climbs included. Red first at ec6e3ccd: TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece and TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece both fail there; every shape the verifier listed is in them. Class sweep: valueNamesOutside is the only path into a path-named value's pieces, and cutValueWithheld runs after it. A one-word or free-text summary already goes through tokenOutside. Also fixed: ec6e3ccd's over-withholding of a quoted absolute project path and of `--out=<root>/x`. Corpus: 0 flips on Windows against eca33155 and ec6e3ccd.
- **fixed**: major: regression against eca33155, a host-refused drop past the 64-judgement cap went unlearned
  - 145a20f0, with perf in 290b04be. Every drop past the bound is now learned as withheld. dropOrder judges the drops a text names first, so section 7 and section 6 show a named allowed drop as eca33155 did. learnedIndex keeps the screen's cost flat; its equivalence rows are TestLearnedIndex_AnswersAsTheLoopsDo and TestLearnedIndex_AJudgeAnswersAsItsListsDo. Red first: TestBuild_ADropPastTheJudgementBoundIsWithheldWhereverItIsNamed fails at ec6e3ccd and passes at eca33155 except for its bound assertion. The bound holds: 84 judgements and about 0.11 s for 1000 drops through the real adapter.
- **deferred-known-issue**: minor: no-rules builds about 2x candidate 7 (finding 33's 1.5x target)
  - Narrowed by 290b04be: allocations 21.1k to 13.1k (candidate 7: 7.6k). p50 against candidate 7 at GOMAXPROCS 2 is 1.69x with no drops, 1.39x with 600 and 1.47x with 1000; ec6e3ccd was 1.95x, 1.56x and 1.56x. Recorded in ADR 0011 §23 item 12. Release-notes sentence: "A rehydration build with no checkpoint drops costs about 1.7x candidate 7's (about 2 ms more) because of the D63 summary whitelist; this is far inside the 5 s compaction budget."

### Tests

- `go test -p 2 -count=1 -overlay <wt rows over ec6e tree> -run '^TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece$' ./internal/rehydrate/ (in an archive of ec6e3ccd)`: FAIL (red first): the payload shows passwd
- `go test -p 2 -count=1 -overlay <same> -run '^TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece$' ./internal/daemon/ (ec6e3ccd tree)`: FAIL (red first): quoted root preview withheld; quoted, paren and climb shapes shown
- `go test -p 2 -count=1 -overlay <same> -run '^(TestBuild_ADropPastTheJudgementBoundIsWithheldWhereverItIsNamed|TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements)$' -v ./internal/rehydrate/ (ec6e3ccd tree)`: FAIL (red first): with 70 fillers, "cat lnk/token.txt" is shown; the ordering assertion fails
- `go test -p 2 -count=1 -overlay <probe copy of the new row> -run '^TestZZF2Probe$' ./internal/rehydrate/ (eca33155 tree)`: every withheld and shown assertion passes; only the bound assertion fails, as expected <!-- runpatterns: a scratch probe the seat ran from an overlaid test file to measure the corpus or a fix; it is not committed, and its numbers are recorded on this line -->
- `GOOS={windows,linux,darwin} GOARCH=amd64 go vet ./internal/rehydrate/... ./internal/daemon/ ./internal/cli/`: PASS on all three
- `GOOS={linux,darwin} GOARCH=amd64 go test -c ./internal/rehydrate/ ./internal/daemon/`: builds
- `go run ./tools/devtool lint --only=golangci-lint`: PASS
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS runpatterns, PASS docmarkers
- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/... ./internal/daemon/ (Windows, HEAD 290b04be)`: ok rehydrate 3.3s, rehydratetest 1.7s, daemon 343.0s
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/ (Windows)`: ok 109.5s
- `go test -p 2 -count=20 -run '^(TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece|TestBuild_ADropPastTheJudgementBoundIsWithheldWhereverItIsNamed|TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements|TestLearnedIndex_AnswersAsTheLoopsDo|TestLearnedIndex_AJudgeAnswersAsItsListsDo|TestHotPathGuards_ChangeNoAnswer|TestOperatorPieces_TheShortcutChangesNoAnswer|TestSplitTokens_SlicingChangesNoAnswer|TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece)$' ./internal/rehydrate/ ./internal/daemon/ (Windows)`: 180/180 PASS
- `go test -race -p 2 -count=3 -run '<same pattern>' ./internal/rehydrate/ ./internal/daemon/ (Windows)`: 27/27 PASS, no data race <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `RV2_OUT=... go test -overlay <w19d corpus> -run '^TestZZRev2Corpus$' ./internal/rehydrate/ at eca33155, ec6e3ccd and HEAD (Windows; scratch harness, not committed)`: identical counts, 0 leaks, 0 flips in all 10 scenarios <!-- runpatterns: a scratch probe the seat ran from an overlaid test file to measure the corpus or a fix; it is not committed, and its numbers are recorded on this line -->
- `BenchmarkBuild fixture overlaid on a357d187, eca33155, ec6e3ccd and HEAD; 12 interleaved rounds, -test.cpu 2, -benchtime 40x; benchstat`: p50 ratios against candidate 7 in the summary; HEAD is 31-70% faster than eca33155 everywhere; uat12 at 1000 drops shows no significant difference from ec6e3ccd (p=0.31)
- `sh linux-nonroot-gate.sh ... -- ./internal/rehydrate/... ./internal/daemon (and the Linux corpus)`: NOT RUN: Docker Desktop is stopped (dockerDesktopLinuxEngine pipe missing, WSL docker-desktop Stopped), and the brief forbids starting it

### Criterion changes

- Path-named values: these are now withheld everywhere a reader starts a path (they were shown at both eca33155 and ec6e3ccd): quoted, bracketed, braced, angle or backtick paths; climbs after `=`, `@` or `:`; punctuation-led pieces; leading short options; a zero-width space; a climb read with backslashes as separators. A quoted absolute project path and `--out=` or `(` before an absolute project path are shown again; ec6e3ccd withheld them and eca33155 showed them. Rationale: D50/D63 and D66(e).
- Drops past the judgement bound are learned as withheld whatever their spelling, so every text naming them is withheld. Drops that a summary or reason names are judged first. Rationale: the verifier's regression against eca33155; fail closed.
- TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements, UAT-12 subtest:
- the drops a summary names (key999.txt, file0.go) are judged first, so the last judged drop is pkg/sub11/file61.go;
- pkg/sub12/file62.go is now asserted withheld;
- the withheld count is 2+(n-62), was 1+(n-63)+1;
- a new summary `{"query":"path:file99"}` is asserted withheld under the rules and shown with none;
- the call count (1+64) is unchanged.
Rationale: named-first ordering. The new assertions are stricter.
- TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece now runs with no host as well as with host rules, under a third root `O'Brien (x)`, with 44 more withheld and 17 more shown shapes. The daemon twin gains 10 withheld and 4 shown previews. All stricter.
- Pure-speed changes, each pinned by a row that compares it with the code it replaced: appendDistinct no longer dedups lists past 64 entries (readers go through learnedIndex); absLike, psSplat, operatorPieces, splitTokens, pathStarts (now a visitor), jsonUnquote (fast path), plainTokenSafeGlob (replacer built once). No verdict changes.

### Open issues

- The Linux non-root gate and the Linux w19d corpus were not run this round. Docker Desktop was stopped when I checked, and the brief forbids starting it. Linux-specific behaviour touched here: the backslash-as-separator reading of a climb (`..\\x` is now withheld on Linux), the quoted /tmp roots in the daemon twin, and the macOS-length preview width of the new daemon previews (computed at 112 of 120 characters).
- New accepted over-withholding (D66(e)) in path-named values: a project path whose segment ends in an opener or a non-ASCII symbol directly before a separator (`a(/x`, `notes/★/x`, a folder named with an emoji), and a piece that starts with punctuation or a short option (`+/x`, `-I/x`). The corpus has none of these.
- New accepted over-withholding (D66(e)) for drops: in a session with a Read rule, a text is withheld if it names a drop past the bound in a way the ordering does not read as a name (a cut prefix, part of a path, a glob), or if more than 64 named drops need a fresh judgement.
- Merging with 19i will touch `globSelectsKnown` (19i's `broadKey` line next to my index branch) and the cost comment in rehydrate_service.go. Resolution notes are in the summary.
- Hosted macOS is still owed for #36 and the darwin limit of #85 (D67(n)).

### Needs owner

- Run, once the Docker engine is up: sh C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --out C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-rehydrate-fr1 290b04be w22-rehydrate-fr1 --gomaxprocs 2 --timeout 60m -- ./internal/rehydrate/... ./internal/daemon. Run it from the worktree, plus the new rows with --count 20 and with race --count 3.
- Add BenchmarkBuild (internal/rehydrate) to the C5.2 set (c52derive.py, inventory row 1.11.16). Both are outside this seat's files.
- Put finding 33's residual in the release notes' known issues (the sentence is in findings_resolution).
- docs/cannot-do.md §5 should gain the ANSI default-character limit (D67(l)); that file belongs to the docs seat. Carried over from the previous round.

## review:rehydrate:r2: verdict `needs-fixes`, 4 finding(s)

- **major** `internal/rehydrate/pathgate.go:767-782 (hostRefuses, past-bound branch `j.judged[p] = true`), read later by internal/rehydrate/items.go:1348 (restorableRules, item 6a) and :1513 (indexableSkills, item 6b); introduced by 145a20f0`: This is a regression against eca33155. When a drop is past the 64-judgement bound, hostRefuses records it as refused without asking the host. It writes that answer into the `judged` memo, which every later host judgement in the same build reads. Item 6a's restorableRules and item 6b's indexableSkills ask withheld() about project-relative paths.Key paths. Those are the same keys the checkpointer gives file pointers (writer.go:884 tc.pathKey), so the two collide. Take a session that once read or edited a nested CLAUDE.md, a `.claude/rules/*.md` file or a SKILL.md, whose pointer was later cut, beyond 64 drops, while any Read deny or ask rule is in force. That instruction file is then not restored and the skill is not indexed. No drop entry names either one, so the loss is silent. eca33155 judged every drop through the host and restored both. The seat's sweep for finding 28 covered the texts (dropOrder, learnedIndex) but missed the other readers of the shared memo. The ADR and security.md describe only withheld texts as the cost of the bound.
  - Evidence: Probe C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w22rh-v2/probe/zz_v2poison_test.go (TestZZV2Poison), run in git-archive copies of 290b04be and eca33155. Setup: ckUAT05 plus a file pointer pkg/live.go. Drops: N file_pointer drops pkg/fN.go, then drops `.claude/rules/zzrule.md` and `.claude/skills/zzskill/skill.md`. d.Rules = fakeScanner returning rule `.claude/rules/zzrule.md` (globs `**`, body ZZRULEBODY). d.Skills returns a skill whose Source is `.claude/skills/zzskill/skill.md`. HostPaths = denyFiles(root, ".env"). HEAD: fillers=0 and 10 give rule restored=true, skill indexed=true; fillers=70 and 200 give rule restored=false, skill indexed=false. eca33155: restored=true and indexed=true at 0, 10, 70 and 200.
  - Fix: Keep the unjudged past-bound answer out of `judged`. Hold it in a separate set that only the drop-learning loop, gateCheckpointDrops and dropped() read, so items 6a and 6b and any structured summary of the same key still get a real host judgement. Those are bounded by the project's configuration and the checkpoint budget, not by the drops. Alternatively, judge items 6a and 6b's files before the drops. Add a red-first row: a rule file and a skill file whose own file_pointer drops lie past the bound under a Read rule are still restored and indexed. Sweep every other caller of withheld() and hostRefuses that runs after newPathJudge.
- **major** `internal/rehydrate/pathgate.go:1489 (valueListSep), :1516-1546 (valuePathStart: inside a piece, only valueOpeners start a path); class of finding 26 (271b2660)`: The finding-26 class is still open. Some characters that no project path holds before a separator still never start a judged path inside a piece of a path-named value, so an outside path after them is shown:
- ASCII control characters and DEL. valueListSep omits them, but a NUL-separated list is exactly what `find -print0` and `git ls-files -z` produce, and the store's canonical preview keeps the NUL as `\u0000`, which jsonStrings decodes.
- A glued `>`/`>>` redirect (illegal in Windows names) and a glued `&`/`&&`.
- The best-fit punctuation letters. D64 made these unsafe on every platform for free text: U+02BA reaches an ANSI program as `"`, and U+01C0 as `|`.
Free text withholds every one of these shapes, but path-named values are exempt from the whitelist. ADR 0011 §23 item 6 claims a path is judged "wherever a reader starts a path" and documents only `+ # ( ) [ ]` as name characters inside a piece. None of these residuals is recorded as a limit. They were shown at eca33155 too. Realistic exposure is low, but each one is an outside path shown in section 6.
  - Evidence: Probes zz_v2probe_test.go and zz_v2store_test.go in the same scratchpad w22rh-v2/probe directory. They ran through Build at 290b04be, on the store's own previews (store.ArgsDigest), with hostRules(root, uat12Rules...) and with no host. Every one of these is SHOWN at HEAD in both modes:
- `{"paths":"src/a.ts\u0000/etc/passwd"}`
- `{"paths":"src/a.ts\u0000~/.ssh/id_rsa"}`
- `{"paths":"src/a.ts\u001f../../outside/x"}`
- `{"paths":"src/a.ts>/etc/passwd"}`
- `{"paths":"src/a.ts>>/etc/passwd"}`
- `{"paths":"src/a.ts&&/etc/passwd"}`
- `{"paths":"src/a.ts>~/.ssh/id_rsa"}`
- `{"paths":"src/a.ts>../../outside/x"}`
- `{"paths":"src/a.tsʺ/etc/passwd"}` (U+02BA)
- `{"paths":"src/a.tsǀ/etc/passwd"}` (U+01C0)
- `{"path":"C:\\q\\proj\\src\\a.ts>/etc/passwd"}`
Controls that HEAD now withholds: the same value split by `\n`, `\t`, NBSP or U+2028, a quote, `(`, `;` or `|`.
  - Fix: Close it fail-closed, per D66(e). Make valueListSep (or valuePathStart) treat every C0 control character and DEL as a separator. Make valuePathStart start a path anywhere in a piece after `>`, `&` and any bestFitPunct code point. None of these is a name character before a separator in a real project path, so the corpus should not flip. Add these shapes to TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece and its daemon twin, red at 290b04be. Name the remaining deliberate in-piece residual (`+ # ) ] } ! ^`) in ADR 0011 §23 item 6.
- **minor** `Linux non-root gate and Linux w19d corpus for 271b2660/145a20f0/290b04be (seat open_issues)`: The brief requires Linux checks, and none ran at HEAD:
- the touched packages under linux-nonroot-gate.sh;
- the new rows at -count=20 and -race -count=3 on Linux;
- the Linux w19d corpus counts.
The seat could not run them, and neither could I: the Docker Desktop engine is stopped (no dockerDesktopLinuxEngine pipe; `wsl -l -v` lists only docker-desktop, Stopped), and starting it is forbidden. 271b2660 adds Linux-only behaviour that only cross-compiles and vet have checked: backslash readings of a climb (`..\\x` withheld on Linux) and the quoted /tmp roots in the daemon twin. The seat's Linux corpus files (w22rh/corpus/lx-*.tsv) predate fr1.
  - Evidence: `docker ps` reports that npipe:////./pipe/dockerDesktopLinuxEngine cannot be found. The newest Linux artifacts under C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-rehydrate*/ are labelled ec6e3cc; none is for 290b04be. Windows passed everything I re-ran at 290b04be:
- rehydrate/... (5.2 s), daemon (396.8 s) and cli (98.3 s);
- the new rows at -count=20 and -race -count=3;
- vet on windows, linux and darwin; golangci-lint; fmt-check; test/docs; docmarkers and runpatterns.
- The Windows w19d corpus is identical at eca33155 and HEAD in all 10 scenarios: 0 leaks, 0 flips.
  - Fix: Once the owner's engine is up, run linux-nonroot-gate.sh on 290b04be (or the merged tree, as D67(n) requires) for ./internal/rehydrate/... and ./internal/daemon, plus the new rows at --count 20 and race --count 3. Also run the Linux w19d corpus and list any flips against eca33155.
- **minor** `Seat report, finding 35 deliverable (Build p50 and p99 before and after)`: The brief asked for Build's p50 AND p99 at eca33155 and at HEAD, for a no-rules project and a UAT-12 project, including 1000 drops. The seat's table gives p50 only, against candidate 7, ec6e3ccd, HEAD and eca33155. No p99 figure is reported, yet the session_start_compact.go comment restates the build's cost on that basis.
  - Evidence: I measured BenchmarkBuild myself: 6 interleaved rounds, -test.cpu 2, -benchtime 30x, the same fixture overlaid on eca33155, co-loaded Windows host. Medians in ms, eca33155 -> HEAD:

| Project | Drops | p50 | p99 |
|---|---|---|---|
| norules | 0 | 6.18 -> 3.18 | 8.90 -> 4.98 |
| norules | 600 | 15.02 -> 3.88 | 21.45 -> 11.78 |
| norules | 1000 | 22.76 -> 6.46 | 33.80 -> 11.80 |
| uat12 | 0 | 5.42 -> 3.72 | 10.55 -> 5.90 |
| uat12 | 600 | 15.68 -> 4.83 | 27.30 -> 7.92 |
| uat12 | 1000 | 27.17 -> 7.69 | 42.55 -> 12.94 |

Allocations fall from 22.5k to 13.1k with no drops. HEAD is faster than eca33155 everywhere, so nothing regressed.
  - Fix: Carry the p99 figures above, or the seat's own, into the wave report and ADR 0011 §23 item 12 beside the p50 table. No code change.


---

## Wave 22b rehydrate seat (rounds after the restart)

Branch `closeout/w22-rehydrate`. Workflow `wf_77be3fb4-369`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## fix:rehydrate:r2: status `done`, head `c00a27b8af156f6194ac4f0c2be40b1c009c90cb`

### Summary

Wave 22 rehydrate seat, fix round 2 plus the merge and the hosted CI findings. Branch closeout/w22-rehydrate (worktree qompack-cx-w22-rehydrate) is now at c00a27b8. Nothing is pushed or merged.

All four review findings and all three hosted CI findings are fixed. The merge is verified, and both corpora are unchanged. One gate is red on this machine for an environmental reason: the full `devtool lint` failed its stubskips sub-check because test/e2e's binary was killed at -timeout=30m under co-load. No skip reason failed.

**Where the round stood**
- Earlier attempts left 25db1dba (review major 1), the merge ffa174d5 and b20a40fc (review major 2). This attempt verified those and added four commits.
- I amended my own first commit and replayed the two after it, to bring its subject to 64 characters or fewer. That rewrote only commits made in this attempt; no other branch was touched.
- Every commit from eca33155 to HEAD has a subject of 64 characters or fewer, no attribution trailers, and the `Refs: V6-VERIFY, C4.6` footer.

**(A) Merge ffa174d5: complete and correct**
- Parents are 25db1dba and 54d4889a; 54d4889a and eca33155 are ancestors of HEAD.
- Re-merging in memory gives conflicts only in `pathgate.go` and `rehydrate_service.go`. Both resolutions keep both sides:
  - `newPathJudge` holds my `unjudged`, `drops` and `memo` beside 19i's `broadSpelling`.
  - `holdRoot` and `holdRootBroad` both go through the memoized `held`. Its key is `heldKey{spelling *regexp.Regexp, text}`, so the strict and broad readings never share an answer. The `rootTail` prefilter runs only for the strict expression.
  - The service's cost comment merges both texts.
- The other memos also key on the fold reading. `memo.summaries` serves only `summaryWithheld`, a show judgement that uses the strict reading. `memo.reasons` serves only `gatedReasonOf` → `reasonWithheld`, which reads both.
- All 168 lines 19h/19i added to ADR 0011 and all 5 added to security.md are present. In the code, the only 19i lines not found verbatim are the two now routed through `held`, plus the reflowed comment.
- 19i's row files are identical, except `(summary withheld)` (finding 30) and r5's table moved to package scope.
- Every 19h, 19i and wave 22 row passes on Windows and Linux.

**(B) Review round 2**
- Major 1 (25db1dba): `hostRefuses` answers "unjudged" only while the drop loop reads (`drops.reading`), so items 6a and 6b ask the host.
  - Red-first row: `TestBuild_ARuleAndASkillWhoseDropsLiePastTheBoundAreStillRestored`.
  - Daemon twin case: 94 judgements against 84 at 290b04be.
- Major 2 (b20a40fc): control characters, `>`, `&` and best-fit letters now split or start a path inside a piece.
  - The deliberate residual inside a piece (`+ # ) ] } ! ^`) is named in ADR §23 item 6.
- Minor 3, Linux: all Linux checks now ran (details below).
- Minor 4: c00a27b8 adds p99 beside p50 in ADR §23 item 12 and in the `session_start_compact.go` comment.

**(C) Hosted CI**
- **H1 (f0061b41):** the 19i row's skip reason now starts with `platform: `.
  - Red at b20a40fc on Linux: the skip reason matches no permitted form.
  - Green at HEAD: zero problems in every Linux run.
  - Sweep: no other skip in my files lacks a permitted reason.
- **H2 (f49250b7):** fixture roots now stay short on every runner.
  - `privacyRoot` uses the new `shortRoot`: /tmp off Windows, the long-name temp dir on Windows. It was `t.TempDir()`.
  - Both packages record each base's spelling as made and as resolved.
  - The guard respells every spelling as the hosted base: macOS's 48-character TMPDIR plus a ten-digit suffix. It replaces the longest first, which removes the macOS `/private` double prefix that put r5's previews 1-2 bytes over the width. It also covers JSON-escaped, forward-slash and ASCII-case-folded spellings.
  - It refuses any preview that spells a `t.TempDir()` of the row.
  - `requestFor` (rehydrate) and `storePreview`/`storePreviewOf` (daemon) apply it to every preview.
  - New guard rows: `TestOnAHostedRunner_RespellsEveryBaseWhole`, `TestShortRoot_IgnoresTheTemporaryDirectory` and `TestRehydrateHostPaths_AHostedRunnersSpellingRespellsEveryBaseWhole`. `TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory` now also covers r5's previews.
  - Red first at b20a40fc: `TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames` fails as hosted CI did, under a 48-character TMP/TEMP/TMPDIR on Windows and a 49-character TMPDIR on Linux. Seven rehydrate rows fail the new guard at the old `privacyRoot`. The daemon row is red under the old respelling.
  - r5 rows under the simulated long TMPDIR: on Linux (49 characters) both rehydrate packages and the 90 daemon rehydrate rows pass, r5 among them. On Windows the rehydrate packages pass with TMP, TEMP and TMPDIR at 48 characters. The daemon rows pass with TMPDIR at 48 and TMP/TEMP at hosted Windows's 39.
- **H3 (ecad3f26):** the r4 premise is asserted per platform: the same folder on macOS (APFS folds by Unicode; D67(m)), another folder on NTFS and Linux. The product assertion (withheld) is unchanged and nothing is skipped. Recorded in ADR §23's D67(m) bullet.

**w19d corpus (274 previews, 10 scenarios): shown/withheld, leaks, over-withheld**

| Scenario | Windows | Linux |
|---|---|---|
| none-plain | 209/65, 0, 52 | 208/66, 0, 53 |
| uat12 plain, spaced, nonascii, long | 193/81, 0, 53 | 192/82, 0, 54 |
| common plain, spaced | 193/81, 0, 54 | 192/82, 0, 55 |
| onedrive, dashword, bestfit | 149/125, 0, 97 | 149/125, 0, 97 |

- Counts are identical at eca33155, 54d4889a and HEAD on both platforms: 0 leaks, 0 flips.
- Neither allowed flip (#26's fix, and `select:`) occurs, because the corpus holds neither shape.

**BenchmarkBuild, eca33155 → HEAD** (b20a40fc's code; six interleaved rounds of 30 builds at -cpu 2, medians, in ms, on a heavily co-loaded host; p99 is each round's slowest build)

| Project | Drops | p50 | p99 |
|---|---|---|---|
| norules | 0 | 11.71 → 6.35 | 22.57 → 15.32 |
| norules | 600 | 26.24 → 8.81 | 59.01 → 20.10 |
| norules | 1000 | 40.70 → 11.50 | 74.61 → 22.22 |
| uat12 | 0 | 14.22 → 7.81 | 24.33 → 19.57 |
| uat12 | 600 | 36.86 → 13.09 | 70.15 → 23.50 |
| uat12 | 1000 | 54.33 → 14.34 | 110.87 → 25.10 |

- Allocations with no drops and no rules: 22.5k → 13.5k.
- The verify's quieter figures (p50 3.2 to 7.7 ms, p99 5.0 to 12.9 ms at 290b04be) are recorded in the ADR.
- HEAD is faster than eca33155 at every size.

**Linux** (container qompack-v6-linux-verification, non-root)
- Full race gate at c00a27b8 for ./internal/rehydrate/... and ./internal/daemon: PASS (712 + 14 + 1745 tests, 0 failures, no race log, 19 minutes).
- New rows at -count=20: PASS. Race at -count=3: PASS.

### Commits

- 25db1dba fix(rehydrate): keep the unjudged drop answer out of the memo (earlier attempt of this round, verified)
- ffa174d5 chore(rehydrate): merge closeout/w19-rehydrate (19h, 19i) (earlier attempt of this round, verified complete)
- b20a40fc fix(rehydrate): judge value paths after controls, > and & (earlier attempt of this round, verified)
- f0061b41 test(rehydrate): use the permitted platform skip on a 19i row (H1)
- f49250b7 test(rehydrate): build preview roots short on every runner (H2)
- ecad3f26 test(daemon): assert the r4 row's folder premise per platform (H3)
- c00a27b8 docs(adr): record Build's p99 beside its p50 (review r2 minor 4)

### Findings resolution

- **fixed**: (A) merge closeout/w19-rehydrate (tip 54d4889a, waves 19h and 19i)
  - ffa174d5, from an earlier attempt of this round, verified complete and correct.
- Re-merging in memory (git merge-tree 25db1dba 54d4889a) conflicts only in pathgate.go (newPathJudge's fields, holdRoot and holdRootBroad) and in rehydrate_service.go's cost comment. Both resolutions keep both sides.
- Memos key on the fold reading: heldKey{spelling regexp, text}, a separate regexp for each reading, with the rootTail prefilter applied only to the strict one. memo.summaries serves only the show judgement; memo.reasons serves only the withholding judgement (reasonWithheld).
- Every line 19h/19i added to ADR 0011 (168) and security.md (5) is present. In the code, the only lines not found verbatim are the two holdWith calls now routed through held, plus the reflowed comment.
- 19i's rows pass on Windows and Linux: internal/rehydrate in full; the daemon rehydrate rows; r4 and r5 at -count=20 and under race at -count=3.
- **fixed**: review r2 major: the unjudged past-bound drop answer entered the judged memo, so item 6a's rule files and item 6b's skills went unrestored
  - 25db1dba, from an earlier attempt of this round, re-verified.
- hostRefuses records the unjudged answer in pathJudge.unjudged, and only while drops.reading is set. Only the drop loop and dropWithheld read it; items 6a and 6b and structured summaries ask the host.
- Red-first row: TestBuild_ARuleAndASkillWhoseDropsLiePastTheBoundAreStillRestored. It fails at 290b04be with 70 fillers.
- Daemon twin: a case in TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers, 94 judgements against 84 at 290b04be.
- Passes on Windows and Linux at -count=20 and under race at -count=3.
- **fixed**: review r2 major: finding 26's class still open for control characters, > and &, and best-fit letters inside a path-named value's piece
  - b20a40fc, from an earlier attempt of this round, re-verified.
- valueListSep splits at every control character (unicode.IsControl).
- valueOpeners gains > and &, and best-fit letters start a path (wordRune).
- TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece gains 22 withheld shapes and 5 shown guards, including an R&D root. Its daemon twin gains 8 withheld and 2 shown store previews. Both are red on ffa174d5.
- The deliberate residual inside a piece (+ # ) ] } ! ^) is named in ADR 0011 §23 item 6 and in security.md.
- Both corpora have no flips.
- **fixed**: review r2 minor: Linux gate, Linux new rows and Linux corpus never ran
  - Run this round in container qompack-v6-linux-verification.
- linux-nonroot-gate.sh at c00a27b8, --gomaxprocs 2, race, for ./internal/rehydrate/... and ./internal/daemon: PASS (712, 14 and 1745 tests, 0 failures, no race log).
- New and touched rows at --count 20 without race: PASS (820 + 700). Under race at --count 3: PASS (123 + 105).
- w19d corpus at eca33155, 54d4889a and HEAD: identical, 0 leaks, 0 flips.
- Artifacts are under C:/Users/Quant/AppData/Local/Temp/claude/qompack-audit/wave22/linux-rehydrate-fr2/.
- **fixed**: review r2 minor: Build's p99 never reported
  - c00a27b8.
- ADR 0011 §23 item 12 carries p50 and p99 against eca33155: the verify's figures on 290b04be, plus this round's co-loaded figures on b20a40fc's code.
- HEAD is faster at every size; p99 at 1000 drops is about 25 ms under co-load.
- The session_start_compact.go comment now states p50 3 to 8 ms and p99 5 to 13 ms, and two to three times that under co-load.
- **fixed**: hosted CI H1: stubskips rejected a 19i row's skip reason
  - f0061b41.
- TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted now skips with the `platform: ` form.
- Red first: the Linux gate at b20a40fc, classified with the stubskips rules, gives 1 problem.
- Green: every Linux run at c00a27b8 gives 0 problems.
- Sweep: no other skip in internal/rehydrate or the daemon rehydrate rows lacks a permitted reason.
- Full devtool lint on Windows: every sub-check passes except stubskips. Its single problem is that test/e2e's binary was killed at -timeout=30m under co-load; no skip-reason problems.
- **fixed**: hosted CI H2: hosted temp-dir roots overflow the 120-byte store preview
  - f49250b7.
- internal/rehydrate's privacyRoot moves from t.TempDir to shortRoot.
- Both packages record each base as made and as resolved. The guard respells every spelling as longestTempBase, longest first, JSON-escaped, with forward slashes and ASCII-case-folded, and refuses previews that spell a t.TempDir of the row.
- Rehydrate's requestFor and the daemon's storePreview/storePreviewOf apply it.
- Rows: TestOnAHostedRunner_RespellsEveryBaseWhole, TestShortRoot_IgnoresTheTemporaryDirectory and TestRehydrateHostPaths_AHostedRunnersSpellingRespellsEveryBaseWhole. TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory now also covers r5's previews.
- Red first:
  - TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames fails at b20a40fc under a 48-character TMP/TEMP/TMPDIR on Windows and a 49-character TMPDIR on Linux.
  - Seven rehydrate rows fail the guard at the old privacyRoot.
  - The daemon row fails under the old respelling, reading /private/var/....
- r5 rows under a simulated long TMPDIR pass: Linux 49 characters for both rehydrate packages and the 90 daemon rehydrate rows; Windows TMPDIR 48 with TMP/TEMP 39.
- **fixed**: hosted CI H3: the APFS SameFile premise in r4
  - ecad3f26.
- TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt asserts os.SameFile against variantIsTheRootsFolder(): true on darwin, false on Windows and Linux.
- The withheld product assertion is unchanged on every platform, and nothing is skipped.
- The red evidence is the hosted macos-latest log (ci 37229942287): all three subtests failed require.False.
- Recorded in ADR 0011 §23's D67(m) bullet.
- Sweep: r5 checks its folder premise only on Windows, where it writes the variant folders; no other rehydrate row asserts a file-system fold.

### Tests

- `git merge-tree --write-tree 25db1dba 54d4889a; diff of the conflict resolution against ffa174d5; presence check of every line 19h/19i added`: conflicts only in pathgate.go and rehydrate_service.go, both resolved keeping both sides; 0 missing doc lines; only 2 code lines rerouted through the memo
- `linux-nonroot-gate.sh b20a40fc --no-race --env TMPDIR=/proc/self/root/proc/self/root/proc/self/root/tmp --run '^(TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames|TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted|...)$' -- ./internal/rehydrate ./internal/daemon`: RED as expected: AnAbsolutePath fails; the skip reason is classified as a stubskips PROBLEM
- `TMP=TEMP=TMPDIR=<48-char dir> go test -run '^TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames$' ./internal/rehydrate/ (b20a40fc archive, Windows)`: RED as expected: toolu_guide's summary is cut
- `go test -p 2 -count=1 ./internal/rehydrate/ with the new guard and the old privacyRoot (Windows)`: RED as expected: 7 rows spell t.TempDir()
- `go test -run '^TestRehydrateHostPaths_AHostedRunnersSpellingRespellsEveryBaseWhole$' ./internal/daemon/ with the old registration-order respelling`: RED as expected: /private/var/folders/... instead of /var/folders/...
- `linux-nonroot-gate.sh c00a27b8 --no-race --env TMPDIR=<49-char /proc/self/root chain> -- ./internal/rehydrate/...`: PASS 712 + 14 tests; stubskips problems=0
- `linux-nonroot-gate.sh c00a27b8 --no-race --env TMPDIR=<49-char> --run 'Rehydrat' -- ./internal/daemon`: PASS 90 tests, r5 and r4 among them
- `sh linux-nonroot-gate.sh --out .../linux-rehydrate-fr2 c00a27b8 w22rh-fr2-gate --gomaxprocs 2 --timeout 60m -- ./internal/rehydrate/... ./internal/daemon (race, non-root)`: PASS: rehydrate 712, rehydratetest 14, daemon 1745; 0 failures; no race log; stubskips problems=0
- `linux-nonroot-gate.sh c00a27b8 --no-race --count 20 --run <23 new and touched rows> -- ./internal/rehydrate ./internal/daemon`: PASS 820 + 700
- `linux-nonroot-gate.sh c00a27b8 --count 3 (race) --run <same rows> -- ./internal/rehydrate ./internal/daemon`: PASS 123 + 105
- `linux-nonroot-gate.sh <throwaway harness commits over eca33155, 54d4889a and c00a27b8> --no-race --env RV2_OUT=- --run '^TestZZRev2Corpus$' -- ./internal/rehydrate`: 2740 rows each; identical counts; 0 leaks; 0 flips
- `RV2_OUT=... go test -run '^TestZZRev2Corpus$' ./internal/rehydrate/ at eca33155 and 54d4889a (archives) and HEAD (-overlay), Windows`: identical counts; 0 leaks; 0 flips <!-- runpatterns: a scratch probe the seat ran from an overlaid test file to measure the corpus or a fix; it is not committed, and its numbers are recorded on this line -->
- `go test -p 2 -count=1 -timeout=30m ./internal/rehydrate/... ./internal/daemon/ (Windows, HEAD)`: ok: rehydrate 7.5s, rehydratetest 2.7s, daemon 539.7s
- `TMP=TEMP=TMPDIR=<48-char> go test -p 2 -count=1 ./internal/rehydrate/... (Windows)`: ok
- `TMP=TEMP=<39-char> TMPDIR=<48-char> go test -p 2 -count=1 -run 'Rehydrat' ./internal/daemon/ (Windows)`: ok
- `go test -p 2 -count=20 -run <23 rows> ./internal/rehydrate/ ./internal/daemon/ (Windows)`: 460/460 pass <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test -race -p 2 -count=3 -run <23 rows> ./internal/rehydrate/ ./internal/daemon/ (Windows)`: ok, no data race <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `GOOS={windows,linux,darwin} GOARCH=amd64 go vet ./internal/rehydrate/... ./internal/daemon/ ./internal/cli/`: PASS on all three
- `go run ./tools/devtool fmt-check`: exit 0
- `go test -p 2 -count=1 ./test/docs/...`: ok
- `GOFLAGS=-p=2 go run ./tools/devtool lint (FULL, Windows)`: PASS: golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, runpatterns, docmarkers, coveragefloors. FAIL stubskips with 1 problem: test/e2e's binary was killed at -timeout=30m (pass 2 took 30m8s under co-load). 0 skip-reason problems; pass 1 inspected internal/rehydrate and internal/daemon
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/ (Windows)`: 1 failure: TestDoctor_SpoolSubmodeIsInformational/a_daemon_is_serving. Alone it passes; 1 of 10 fails at HEAD and 20 of 20 pass at b20a40fc, with the code under test identical (see open issues)
- `BenchmarkBuild: eca33155 with the fixture overlaid, against HEAD; 6 interleaved rounds, -test.cpu 2, -test.benchtime 30x`: HEAD faster at every size; p50 and p99 table in the summary

### Criterion changes

- H3: TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt's fixture premise changes from require.False(os.SameFile) on every platform to require.Equal(variantIsTheRootsFolder(), os.SameFile): true on darwin, false elsewhere. Rationale: on macOS's default APFS volume, which D67(m) assumes, each variant is the root's own folder (hosted macos-latest, ci 37229942287). The product assertion (withheld on every platform) is unchanged, and nothing is skipped.
- H2: internal/rehydrate's privacyRoot moves from t.TempDir()/proj to shortRoot()/proj, which is /tmp off Windows and the long-name temp dir on Windows. requestFor now holds every tool summary to the hosted-runner width and refuses any summary that spells a t.TempDir() of the row. Both are stricter.
- H2: the daemon guard now records each base as made and as resolved and respells the longest spelling first. This removes the accidental macOS /private prefix (/tmp/q... inside /private/tmp/q... was respelled as /private + the hosted base). On macOS the check is now against the 60-character hosted base, as 7bd34725 designed it, instead of an accidental 68. It is also stricter elsewhere: JSON-escaped, forward-slash and ASCII-case-folded spellings are respelled, and a value spelling a t.TempDir() of the row is refused.
- r5's case table (19i) moves to the package variable unicodeRootVariants, with each code point written as a Go escape; its values are unchanged.
- H1: the 19i row's skip reason gains the `platform: ` prefix; the skip condition is unchanged.

### Open issues

- The full devtool lint on this machine (GOFLAGS=-p=2, heavy co-load) failed stubskips only because test/e2e's binary was killed at -timeout=30m: pass 2 took 30m8s, and pass 1 took 55m43s. No skip reason failed anywhere it inspected, but e2e's own skip reasons were not inspected in this run. The e2e timeout headroom is the cliwork seat's #84. Skips outside my files that lack a permitted reason and fire only conditionally: test/canary/injection_test.go:82, test/e2e/phase1_exit_test.go:667 and test/e2e/sessionstart_compact_test.go:660 (under -short).
- internal/cli TestDoctor_SpoolSubmodeIsInformational/a_daemon_is_serving failed once in my full cli run, then 1 of 10 at HEAD (status 'degraded', expected 'ok'), under heavy co-load. It passed 20 of 20 at b20a40fc. My only non-test change since b20a40fc is a comment, so the cause is load, not this round. It is the same class as audit 2 #80 (cliwork): a real daemon probe that must answer within a deadline. Not my files.
- Test-only residual: on a Windows machine whose TEMP is longer than about 40 characters, four daemon rehydrate rows build previews over the store's width and fail as fixtures. They are r4, r5, ABestFitOrDashRootHoldsNoRootUnit and APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece; I measured this with TEMP at 48 characters. Hosted Windows (39 characters) and this machine (33) are inside that limit, and the guard holds Windows previews to the hosted spelling. Closing it would mean making fixture roots outside the temp dir, for example in C:\, on the owner's machine.
- H2's macOS respelling and H3's APFS premise are verified by simulation and by the hosted macOS log, not on a macOS host. The hosted ci.yml run with macOS that D67(n) requires is the confirmation.
- Accepted over-withholding added by b20a40fc (D66(e)): a project path segment that ends in & or > or in a best-fit letter right before a separator. The corpus has none.
- Carried from fix round 1: finding 33's residual against candidate 7 stays a deferred known minor. Release-note sentence: "A rehydration build with no checkpoint drops costs about 1.7x candidate 7's (about 2 ms more) because of the D63 summary whitelist; this is far inside the 5 s compaction budget."

### Needs owner

- Run a hosted ci.yml on merged closeout/integration that includes macOS (D67(n)), to confirm H2 and H3 on macos-latest.
- Decide whether stubskips' whole-tree -timeout (tools/devtool wholeTreeTestTimeout, not my file) needs the same headroom cliwork is giving test-e2e (#84): on a co-loaded Windows host, pass 2 (test/e2e alone) took 30m8s against a 30m limit.
- Carried: add BenchmarkBuild to the C5.2 set (c52derive.py, inventory row 1.11.16); outside this seat's files.
- Carried: the docs seat adds the ANSI default-character limit (D67(l)) to docs/cannot-do.md §5 if not already done, and finding 33's sentence to the release notes' known issues.

## review:rehydrate:r3: verdict `needs-fixes`, 2 finding(s)

- **minor** `internal/rehydrate/hosted_root_w22_test.go:166 (spellsTestTempDir) and its twin at internal/daemon/rehydrate_hostpaths_w22_test.go:233, used by requestFor (fake_test.go:566) and requireUncutOnAHostedRunner (rehydrate_hostpaths_test.go:663); introduced by f49250b7 (H2)`: H2's guard is meant to refuse any preview that spells a t.TempDir() of the row. The commit message, the seat's report and the helper's comment all say it does, but it cannot see this for a row whose top-level test name is longer than 64 bytes. Go names the t.TempDir directory after t.Name() cut to its first 64 bytes, with symbols removed (go1.26.6 testing.go:1449-1460: `pattern = pattern[:min(len(pattern), 64)]`). spellsTestTempDir instead looks for `<tmp>/<whole top-level name>`, which never matches once the name passes 64 bytes. Such a row does not reach the length check either, because that check only respells the shortRoot/shortProjectDir bases. A row like that could go back to a t.TempDir() root and pass locally, then cut its previews on a hosted runner again: H2's regression, with no guard catching it. 11 of the 21 daemon rows that build store previews have names over 64 bytes, including the r4 and r5 rows behind H2 and H3 (for example TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt, 71 bytes). One privacyRoot row in internal/rehydrate is also over (TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements, 68 bytes). Today's fixtures are correct: the sweep is complete, and every row uses shortRoot or shortProjectDir. So this is a test-guard gap, with no product or gate impact.
  - Evidence: I ran an overlay probe (scratchpad w22rh-v3/zz_v3guard_test.go) at c00a27b8 on Windows, through requestFor with a summary {"file_path":"<t.TempDir()>/proj/a.go"}:
- TestZZV3GuardShortName (22 bytes) FAILS as intended: 'spells t.TempDir(), whose length is the runner's'.
- TestZZV3GuardLongName_AnythingPastSixtyFourBytesOfTheTestsNameIsCutByTempDir (76 bytes) PASSES. The guard let C:\Users\Quant\AppData\Local\Temp\TestZZV3GuardLongName_AnythingPastSixtyFourBytesOfTheTestsNameIs2077182171\001\proj\a.go through.

Everything else in the seat's result checked out:
- Merge: ffa174d5 is complete. Re-merging conflicts only in pathgate.go and rehydrate_service.go. Every line 19h/19i added is present except the two now routed through held and a reflowed comment. The broad/strict call sites match 54d4889a. The memos key on the expression and the text.
- Review r2 majors: both fixed. The r2 verifier's own poison, value and store probes now restore and withhold correctly.
- Tests at HEAD:
  - Windows: rehydrate/..., daemon (587 s) and cli pass; the new rows pass at -count=20.
  - Linux non-root race gate: 712, 14 and 1745 pass; my stubskips classification of that run finds 0 problems.
  - Under a 49-character TMPDIR on Linux and an 8.3-named TEMP on Windows, both packages and the daemon rehydrate rows pass. b20a40fc fails the 8.3-TEMP case, so H2 was red there.
- w19d corpus at eca33155 and HEAD: identical on Windows and Linux, 0 leaks, 0 flips.
- Full `devtool lint` on Windows: exit 0. stubskips OK; e2e pass 2 took 25m12s.
  - Fix: Make spellsTestTempDir build the prefix the way testing does: take t.Name() cut to 64 bytes, drop the characters removeSymbolsExcept drops (the subtest '/' among them), and match <tmp or its resolved spelling>/<that prefix>. Alternatively, refuse any spelling under os.TempDir() (as given and as resolved) that is not under a recorded shortRoot/shortProjectDir base. Add a guard-row case with a top-level name over 64 bytes in both packages.
- **nit** `internal/rehydrate/hosted_root_w22_test.go:223-241 (TestShortRoot_IgnoresTheTemporaryDirectory)`: The row's comment says it holds 'on any platform', but it only sets TMPDIR. Windows reads TMP/TEMP, so there the assertion that shortRoot's base is not under the 48-character directory is trivially true and checks nothing. It is meaningful only off Windows.
  - Evidence: t.Setenv("TMPDIR", long) at :228. On Windows, os.TempDir() uses TMP/TEMP (os/file_windows.go), so shortRoot's os.MkdirTemp("", "q") never looks at TMPDIR.
  - Fix: Also t.Setenv TMP and TEMP to the long directory on Windows, and assert the base is the long-name temp dir's, not under `long`. Or reword the comment to say the row binds off Windows only.


## Hosted CI findings H1-H4 (ci.yml run 37229942287)

```json
{
  "source": "hosted ci.yml run 37229942287 on closeout/integration 456a0d24 (integration 2bf29705 + 19h + 19i), 2026-10-04; logs at C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/ci37229942287/failed.log",
  "findings": [
    {"id": "H1", "severity": "major", "owner": "rehydrate", "location": "internal/rehydrate (TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted)",
     "issue": "devtool lint's stubskips sub-check fails on verify and release-dry-run: the row is skipped on non-fold platforms with a reason matching none of the three permitted messages (Rule W-1, Rule W-2, or \"platform: \" followed by a reason). Seats ran only --only=golangci-lint and docmarkers,runpatterns, never the full devtool lint.",
     "fix": "Use the permitted 'platform: <reason>' form, then run the FULL go run ./tools/devtool lint."},
    {"id": "H2", "severity": "major", "owner": "rehydrate", "location": "internal/daemon/rehydrate_hostpaths_r5_test.go:100 via rehydrate_hostpaths_test.go:563 storePreview; internal/daemon TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules; internal/rehydrate summary_privacy_test.go:235 TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames",
     "issue": "On hosted macOS (TMPDIR /var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/, resolved to /private/var/...) and hosted Windows (C:\\Users\\RUNNER~1\\AppData\\Local\\Temp, D: workspace), fixture roots built from os.MkdirTemp or t.TempDir are long enough that the store's 120-character preview is cut ('…'). The r5 tool-summary rows fail their 'the store cut the preview' fixture check for U+0130, U+212A and U+212B. TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames expects the uncut toolu_guide summary but gets it cut, on macOS and on Windows. CommonIdioms is audit 2 #36. All are test-fixture defects, not product leaks.",
     "fix": "Build every rehydrate and daemon-rehydrate fixture root under a short root: os.MkdirTemp(\"/tmp\", \"q\") off Windows, and on Windows a short canonical dir. Never t.TempDir() for a root that appears in a preview. Add a guard row that builds every root-based preview against a 48-character TMPDIR with a 10-digit suffix and requires each to be uncut. Sweep both packages for every t.TempDir/MkdirTemp root that reaches a preview."},
    {"id": "H3", "severity": "major", "owner": "rehydrate", "location": "internal/daemon/rehydrate_hostpaths_r4_test.go:65 (TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt, subtests åsa, kate, sam)",
     "issue": "On hosted macOS (APFS, case-insensitive and normalization-insensitive, Unicode case folding) the Kelvin-sign, long-s and Angstrom-sign variants ARE the root's own directory, so the fixture's require.False(os.SameFile) fails. The product is safe there: the strict reading treats them as outside and withholds, which is over-withholding.",
     "fix": "Make the fixture premise platform-true: assert os.SameFile per platform (true where the filesystem folds that letter, false on NTFS and Linux), and keep the product assertion (withheld) on every platform. Never skip. Record per D67(m)."},
    {"id": "H4", "severity": "major", "owner": "rows", "location": "internal/daemon/spawn_lock_test.go:495-545 (TestEnsureRunningUntil_ThePollEndsAtItsDeadline/claim_freed_near_the_end and claim_held_to_the_end), hosted windows-latest -count=2",
     "issue": "spawn_lock_test.go:531 'a dial cut short at the end of the wait is no licence to spawn' failed (should be false), :532 spawned 1 (should be zero), and the dial log was not empty: 'dial 1 of 10: 667ms, dial 2 of 10: 687ms, ...'. claim_held_to_the_end failed the same way. The first -count run passed with 'the poll returned 1.426ms after its deadline'. This may be a PRODUCT race: EnsureRunningUntil spawning after a dial cut short at its deadline would double-spawn a daemon. It may also be a wall-clock-dependent fixture.",
     "fix": "Investigate first. Reproduce under a slowed dial (a fake dialer that takes as long as the remaining time) and decide product or test. If the product spawns on a cut-short dial, fix it red-first: a dial cut short by the deadline must never license a spawn. If the test's verdict depends on scheduling, make it deterministic without weakening it."}
  ]
}
```
