# W16-WINCI: hosted Windows reds and the 8.3 deny-rule bypass

Branch `closeout/w16-winci`. Workflow `wf_90777431-3b4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `74939cbe`

### Root cause

(1) PRIVACY, a product defect in internal/hostperm. On the hosted runner TEMP is C:\Users\RUNNER~1\..., so the 'long' absolute rule in the short-project-root subtest named a path with a short parent (RUNNER~1) and a long leaf. RuleSet.Evaluate judged two spellings of the served file: the path as given (short root) and its full long name from osAlias/GetLongPathNameW. A rule's own literal was never canonicalized, except through links in throughLinks. So a rule spelled with any 8.3 segment, or mixing short and long segments, matched neither spelling, and re_read served the archived secret. paths.Norm is not where the spellings diverge: its EvalSymlinks already expands 8.3 names. (2) The hosted account's token has SeBackupPrivilege and SeRestorePrivilege enabled. Every product path in the four rows opens with backup intent: Go's os.Open adds FILE_FLAG_BACKUP_SEMANTICS to every read-only open, paths.OpenShared sets it explicitly, and the I/O manager opens a rename's target directory with backup intent. So deny ACEs were bypassed. The evidence is internal: in the EnsureLayout row a plain O_CREATE (no backup semantics) into the denied directory was refused, while the product's rename into that same directory succeeded. The share-mode row fits the same hypothesis but this machine cannot confirm it. (3) This is the row, not the product: Stop already starts the session ends' grace before the runWG join (stopSessionEnds, then startupMu, then runWG.Wait). The row timed all of Stop against grace + abandon window + 2 s. After the join, Stop legitimately runs its own bounded drain (stopDrainBound) and its cleanup. The whole row ran about 22x slower on the runner (18.75 s against 0.85 s locally), and Stop's 7.3 s is consistent with that slowdown. (4) The fixture itself: the bundle was made under t.TempDir (C:) and named relative to the checkout (D:). No relative path joins two volumes.

### Summary

This is the w16-winci workstream report. The coordinator commits it, since the harness refuses report files. Worktree: C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-winci, branch closeout/w16-winci, base bb54c6ba. Five commits; the head is 74939cbe. Evidence logs are in plans/sdd/V6-closeout/w16-winci/runs/, including 00-nightly-36820740318-race-windows.log.

Correction to the brief: this machine's C: volume DOES record 8.3 names. TestHostPolicy_AShortNameSpellingIsRefused ran here and passed; it did not skip. It passed only because the local TEMP (C:\Users\Quant\AppData\Local\Temp) has no short segment. No volume or system setting was changed.

ITEM 1 — PRIVACY (D53, C4.6). Fixed in c7165782.
- Reproduced 3 ways, all red before the fix:
  - (a) A deterministic seam in internal/hostperm/shortname_seam_windows_test.go. getLongPathName becomes a package variable, and fakeShortNames stands in for a volume that records 8.3 names. 5 of 7 subtests of TestShortNames_EverySpellingOfTheServedFileIsJudged were red, including the exact CI shape (a mixed absolute rule with a short root). TestShortNames_AnUnresolvableShortNameIsRefused was also red. Logs: runs/01 (red), runs/02 (green).
  - (b) The real CI failure, reproduced locally with no system change: TMP/TEMP set to the 8.3 spelling of a scratch directory. 'short project root/absolute rule, relative path' served SHORTNAME-ARCHIVE-5d20e7a4 exactly as on CI. Logs: runs/03 (red), runs/04 (green).
  - (c) A new mcp subtest, 'mixed absolute rule, relative path' (the directory spelled short, the file long). It is red on any 8.3 volume with a normal TEMP. Logs: runs/05 (red), runs/06 (green).
- The fix:
  - Policy.longNameAliases applies every non-negated rule whose concrete prefix holds a ~, a trailing dot or space, or a stream colon (spelledAsAlias) at its real name too, through osAlias/GetLongPathNameW. For //, ~/ and / rules it also follows links from there. The alias is never carvable and only adds refusals. A rule set with no such segment pays no syscall.
  - Fail closed: osAlias/longName now report a segment of 8.3 shape (~ followed by a digit) that names nothing on disk. With any rule in force, Evaluate denies such a path (Rule unresolvedShortName), consistent with C1.9 finding 1. An existing long name containing ~1 is judged as itself. With no rules, nothing changes.
- Other retrieval forms:
  - re_read, expand, recall, dropped and why all go through authorizeHost, which calls RuleSet.Evaluate.
  - The rehydration section 6 pointer judge (daemon rehydrateHostPaths) calls RuleSet.Evaluate too.
  - All of them inherit the fix, because it sits in rule compilation and Evaluate. internal/rehydrate pathgate needed no change.
- The forms subtests on an 8.3 volume:
  - The rule there is ./configuration/credentials.secret.
  - The cwd anchors include the root's long name, and every candidate (CONFIG~1/CREDEN~1.SEC, the absolute short path, the short record) exists, so it expands and is never 'unresolved'.
  - The run with TMP set short showed all forms passing.

ITEM 2 — ELEVATED-RUNNER FIXTURES (e6297cbf).
- New helper pathstest.WithoutBackupPrivileges(t, fn). It locks the OS thread, calls ImpersonateSelf, removes SeBackupPrivilege and SeRestorePrivilege from the thread token (SE_PRIVILEGE_REMOVED), runs fn, then reverts. It changes nothing outside that thread. There is a non-Windows stub, and pathstest.EnabledBypassPrivileges for diagnostics.
- Each of the four rows runs its product call inside the helper, and first checks inside it that its own denial holds for this token. That check is a loud require naming the enabled privileges, never a skip:
  - TestStageBinary_NeverRemovesACopyHeldOpen: OpenShared must get ERROR_SHARING_VIOLATION.
  - TestStageBinary_RestagesACopyItCanNeverRead: OpenShared must get fs.ErrPermission.
  - TestTerminatePartialTail_UnreadablePathIsLeftAlone: os.Open must get a permission error.
  - TestEnsureLayout_WriteAtomicFailureIsPropagated: a new rename probe from l.Tmp into l.Dot must get a permission error.
- Every assertion on product behaviour is unchanged. Self-test: TestWithoutBackupPrivileges_FnRunsWithNeitherBypassPrivilege. It logs the process token's enabled privileges, so the next hosted run records the account type.
- Limit: this machine is not elevated, so the privilege removal is a no-op here and is unproven on an elevated token until hosted CI runs. If a row still fails there, the precondition message now names the cause.

ITEM 3 — STOP BEHIND A DRAIN (8fd9547b).
- Reproduced alone under -race, -count=20, before any change: 20/20 pass, row 0.79–1.08 s (runs/10). After the change: 20/20 pass, Stop 387–481 ms (runs/12).
- The row now records how the session end's stuck drain line ended, and requires context.Canceled (the grace) rather than DeadlineExceeded (the line's own drainLineDeadline, which is what ended it when the grace started after the join).
- Mutation check: moving stopSessionEnds after runWG.Wait fails the new assertion (runs/11, row 10.9 s).
- Stop's total is now bounded by sessionEndGrace + sessionEndAbandonAfter + stopCleanupBound (0.2 + 0.25 + 15 s), all existing named constants. There is a precondition that this stays inside liveOrderBound, and a t.Logf of Stop's time.

ITEM 4 — LIVEEVAL (00c1c04a).
- The fixture now makes the bundle in os.MkdirTemp(<package dir>, ".tmp-liveeval-bundle-"), removed by t.Cleanup. The dot prefix keeps the go tool from treating it as a package.
- Cross-volume reproduction: the pre-fix test binary, run with TMP on a \\?\C: spelling, failed with the same 'Rel: can't make' error (runs/13).
- The fixed test passes normally (runs/14). Under the \\?\ simulation it fails later for an unrelated reason: CreateProcess rejects a \\?\ working directory (an artifact of the simulation; CI's TEMP is a normal C:\ path).
- The product needs no new message: readLiveBundle resolves --bundle with filepath.Abs against the working directory, which resolves every relative form, a drive-relative one included.

COMMANDS (Windows, -p 2):
- go test ./internal/hostperm -run 'TestShortNames_EverySpellingOfTheServedFileIsJudged' — red before the fix, green after.
- go test ./internal/hostperm -run 'TestShortNames_AnUnresolvableShortNameIsRefused' — red before the fix, green after.
- go test ./internal/mcp -run 'TestHostPolicy_AShortNameSpellingIsRefused' -v — red before, green after, with TMP short and with a normal TMP.
- go test ./internal/mcp -run 'TestHostPolicy' — 20/20 pass.
- go test ./internal/daemon -run 'TestStageBinary_NeverRemovesACopyHeldOpen' — pass.
- go test ./internal/daemon -run 'TestStageBinary_RestagesACopyItCanNeverRead' — pass.
- go test ./internal/paths -run 'TestTerminatePartialTail_UnreadablePathIsLeftAlone' — pass.
- go test ./internal/paths -run 'TestEnsureLayout_WriteAtomicFailureIsPropagated' — pass.
- go test ./internal/paths/pathstest -run 'TestWithoutBackupPrivileges_FnRunsWithNeitherBypassPrivilege' — pass.
- go test ./internal/daemon -race -run '^TestStop_IsNotHeldBehindASessionEndsDrain$' -count=20 — 20/20 before and after.
- go test ./tools/devtool -run '^TestRunLiveEval_RelativeBundleReachesTheHostWhole$' — pass.
- Full packages, -count=1 -p 2: hostperm 3.3 s, paths 8.0 s, paths/pathstest 1.6 s, tools/devtool 91 s, mcp 167 s, daemon 356 s — all ok.
- go vet on the touched packages: Windows and GOOS=linux, ok.
- devtool fmt-check: ok.
- devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns: all 8 PASS.
- Not run, per this seat's limits: hot-path rows, whole e2e/integration packages, the Linux container. The coordinator runs those at night.

CRITERION CHANGES:
- (a) TestStop_IsNotHeldBehindASessionEndsDrain:
  - What tells the outcomes apart moved from Stop's total wall-clock (< 2.45 s, made of a 2 s magic slack and nothing for Stop's own drain and cleanup) to the cause of the release (Canceled by the grace versus the line's own deadline). The defect is that exact ordering, and the mutation fails it.
  - The total bound is now composed of named product constants (15.45 s), not a widened magic number.
- (b) The four elevated rows gained loud preconditions, and their product calls now run without backup/restore privileges. No assertion was loosened.
- (c) The liveeval row only moved where its bundle is made.
- (d) Two new fail-closed refusals in hostperm, both conservative: an unexpandable 8.3-shaped segment under any rule; and the real-name alias of a rule spelled short.

### Commits

- c7165782 fix(hostperm): judge a rule's 8.3 spelling at its real name
- e6297cbf test(paths): enforce deny-ACE fixtures for an elevated token
- 8fd9547b test(daemon): assert how Stop's wait behind a drain ended
- 00c1c04a test(devtool): make the relative-bundle fixture volume-proof
- 74939cbe test(v6): record the w16-winci package runs and lint

### Tests

- `go test ./internal/hostperm -run 'TestShortNames_EverySpellingOfTheServedFileIsJudged' -count=1 -p 2 -v (seam; before fix)` — FAIL 5/7 subtests incl. 'mixed absolute rule, short root, long file' (runs/01)
- `go test ./internal/hostperm -run 'TestShortNames_AnUnresolvableShortNameIsRefused' -count=1 -p 2 -v (before fix)` — FAIL (runs/01); both rows PASS after fix (runs/02)
- `TMP=<8.3 spelling> go test ./internal/mcp -run 'TestHostPolicy_AShortNameSpellingIsRefused' -count=1 -p 2 -v (before fix)` — FAIL short_project_root/absolute_rule,_relative_path served SHORTNAME-ARCHIVE-5d20e7a4, same as CI (runs/03)
- `TMP=<8.3 spelling> go test ./internal/mcp -run 'TestHostPolicy' -count=1 -p 2 -v (after fix)` — PASS 20/20 (runs/04)
- `go test ./internal/mcp -run 'TestHostPolicy_AShortNameSpellingIsRefused' -count=1 -p 2 -v (new mixed subtest, before / after fix)` — FAIL mixed_absolute_rule,_relative_path before (runs/05); PASS after (runs/06)
- `go test ./internal/daemon -run 'TestStageBinary_NeverRemovesACopyHeldOpen' -count=1 -p 2 -v` — PASS (runs/09)
- `go test ./internal/daemon -run 'TestStageBinary_RestagesACopyItCanNeverRead' -count=1 -p 2 -v` — PASS (runs/09)
- `go test ./internal/paths -run 'TestTerminatePartialTail_UnreadablePathIsLeftAlone' -count=1 -p 2 -v` — PASS (runs/09)
- `go test ./internal/paths -run 'TestEnsureLayout_WriteAtomicFailureIsPropagated' -count=1 -p 2 -v` — PASS (runs/09)
- `go test ./internal/paths/pathstest -run 'TestWithoutBackupPrivileges_FnRunsWithNeitherBypassPrivilege' -count=1 -v` — PASS; process token enables [] on this non-elevated machine
- `go test ./internal/daemon -race -run '^TestStop_IsNotHeldBehindASessionEndsDrain$' -count=20 -p 2 -v (before change)` — PASS 20/20, row 0.79-1.08 s, alone (runs/10)
- `go test ./internal/daemon -run '^TestStop_IsNotHeldBehindASessionEndsDrain$' -count=1 -v with a temporary mutation (stopSessionEnds after runWG.Wait)` — FAIL on the new Canceled assertion, row 10.9 s; mutation reverted (runs/11)
- `go test ./internal/daemon -race -run '^TestStop_IsNotHeldBehindASessionEndsDrain$' -count=20 -v (after change)` — PASS 20/20, Stop 387-481 ms (runs/12)
- `pre-fix devtool test binary with TMP=\\?\C:... -test.run '^TestRunLiveEval_RelativeBundleReachesTheHostWhole$'` — FAIL 'Rel: can't make ... relative to ...', same as CI (runs/13)
- `go test ./tools/devtool -run '^TestRunLiveEval_RelativeBundleReachesTheHostWhole$' -count=1 -v` — PASS (runs/14)
- `go test ./internal/hostperm -count=1 -p 2` — ok 3.3 s
- `go test ./internal/paths -count=1 -p 2` — ok 8.0 s
- `go test ./internal/paths/pathstest -count=1 -p 2` — ok 1.6 s
- `go test ./tools/devtool -count=1 -p 2` — ok 91.2 s
- `go test ./internal/mcp -count=1 -p 2` — ok 166.6 s
- `go test ./internal/daemon -count=1 -p 2` — ok 356.5 s
- `go vet (Windows and GOOS=linux) ./internal/hostperm ./internal/mcp ./internal/paths ./internal/paths/pathstest ./internal/daemon ./tools/devtool` — ok
- `go run ./tools/devtool fmt-check` — ok
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — PASS all 8 (runs/20-lint-summary.txt)

### Criterion changes

- TestStop_IsNotHeldBehindASessionEndsDrain: the discriminating check is now how the session end's stuck drain line ended (context.Canceled from the grace, not the line's own drainLineDeadline), replacing the whole-Stop wall-clock bound of grace + abandon + 2 s. Rationale: after the join Stop legitimately runs its own bounded drain and cleanup; the hosted -race run took 7.3 s with the grace on time, and the whole row was about 22x slower there. A mutation restoring the pre-fix order fails the new check. The total is now bounded by grace + sessionEndAbandonAfter + stopCleanupBound, all named constants.
- TestStageBinary_NeverRemovesACopyHeldOpen, TestStageBinary_RestagesACopyItCanNeverRead, TestTerminatePartialTail_UnreadablePathIsLeftAlone and TestEnsureLayout_WriteAtomicFailureIsPropagated: the product call runs on a thread without SeBackupPrivilege and SeRestorePrivilege, and a loud in-thread precondition (never a skip) checks that the fixture's denial holds for this token. TestEnsureLayout_WriteAtomicFailureIsPropagated gains a rename probe, because the rename is the operation the deny ACE has to stop. Product assertions are unchanged.
- TestRunLiveEval_RelativeBundleReachesTheHostWhole: the bundle is made under the package directory instead of t.TempDir, so a relative path to it exists on every machine. Assertions are unchanged.
- TestHostPolicy_AShortNameSpellingIsRefused gains the subtest 'short project root/mixed absolute rule, relative path'. New hostperm rows: TestShortNames_EverySpellingOfTheServedFileIsJudged and TestShortNames_AnUnresolvableShortNameIsRefused (seam-driven, run on any Windows volume). New pathstest row: TestWithoutBackupPrivileges_FnRunsWithNeitherBypassPrivilege.

### Open issues

- Item 2's fixtures cannot be confirmed here: this machine is not elevated (whoami /priv shows no SeBackupPrivilege), so WithoutBackupPrivileges is a no-op locally. The next hosted Windows run must show the four rows green. If any still fails, its new precondition names the privileges in force and the denial that did not hold. TestStageBinary_NeverRemovesACopyHeldOpen is the least certain: it is a share-mode denial, and backup semantics are not documented to bypass sharing. If it stays red there, the precondition will say whether the open was refused at all.
- Not mine, seen in ci.yml run 36816905394's windows test job: TestOpenObjectLeaf_BranchesOnTheHandlesReparseAttribute (internal/store) fails with 'listen unix C:\Users\RUNNER~1\...\s: bind: invalid argument'. This is the AF_UNIX socket fixture on the hosted runner, likely the socket-path item in the ci seat's scope. Not touched.
- Residual hostperm gap, documented and not fixed: a rule whose own 8.3-spelled segment names a file or directory that no longer exists cannot be expanded (GetLongPathNameW needs the entry). Such a rule still matches its exact spelling, but not the long name of a deleted file. A path in the same situation is now refused outright.
- The whole test/e2e, integration and Linux verification of these commits are left to the coordinator's night run, per this seat's limits. The new hostperm code is Windows-only behind runtime.GOOS; Linux was covered by GOOS=linux vet only.
- Scratch logs remain under scratchpad/w16/winci/. All my test binaries and the \\?\ simulation directory were removed, and no background process of mine is running.

### Needs the owner

- No new budget or bound numbers. TestStop_IsNotHeldBehindASessionEndsDrain's total bound is composed only of existing named constants: sessionEndGrace (200 ms in the row), sessionEndAbandonAfter (250 ms) and stopCleanupBound (15 s), 15.45 s in all. If it is wrong, a Stop that spends more than its own cleanup ceiling fails the row. The coordinator may still want to approve this as a criterion change, because the total moved from 2.45 s to 15.45 s; what tells the two outcomes apart is now the Canceled-versus-DeadlineExceeded assertion.
- Product behaviour change for approval (C4.6, fail closed). With any Read deny or ask rule in force, a retrieval path holding an 8.3-shaped segment (~ followed by a digit) that names nothing on disk is refused, because its long name cannot be established. A deleted file whose real long name happens to contain ~1 will also be refused under any rule.

## Independent review

### review:winci: needs-fixes

- **minor** `internal/daemon/spawn_stage_windows_test.go:123-135 (TestStageBinary_NeverRemovesACopyHeldOpen)` — The root cause for this row is a guess, and the fix probably does not address it. The fixture's denial is a share mode: holdStaged opens with GENERIC_READ and FILE_SHARE_DELETE only. NT enforces share access even for backup-intent opens; SeBackupPrivilege bypasses the security check, not the sharing check. So removing the backup and restore privileges probably does not change what CI saw (stageBinary returned nil). The implementer flags this in open_issues, but the commit body and the root_cause text present all four rows as one explained mechanism.
  - Evidence: CI log runs/00 line 307: 'Target error should be in err chain: expected: The process cannot access the file ... in chain:' (empty, so err was nil). The commit e6297cbf body attributes all four rows to backup semantics. The implementer's own open_issues says 'backup semantics are not documented to bypass sharing ... cannot confirm'. Nothing in the diff tests the share-mode hypothesis.
  - Fix: Treat this row as open, not done. The brief allows an injected failure seam where the row only needs the error path, which holds for any token. Make the error that verifyStaged's OpenShared returns injectable (or the heldOpenElsewhere input). Drive ERROR_SHARING_VIOLATION through stageBinary, then assert the error is returned and the copy is kept (requireSameStagedFile). Keep the real-handle variant as a diagnostic that logs perr. Correct the e6297cbf body or the report so this row is not claimed as explained.
- **minor** `internal/paths/pathstest/privileges_windows.go:30-55; privileges_windows_test.go:18-27` — WithoutBackupPrivileges never does anything on any machine available before hosted CI. Its only self-test passes trivially on a non-elevated token: both privilege lists are empty, and the test log prints 'process token enables: []'. So item 2's fix rests on code (ImpersonateSelf, then AdjustTokenPrivileges with SE_PRIVILEGE_REMOVED on the thread token, then RevertToSelf) that has never removed a privilege in a run. Also, x/sys returns no error for ERROR_NOT_ALL_ASSIGNED, so the check at :73 never runs.
  - Evidence: I ran `go test ./internal/paths/pathstest -run TestWithoutBackup -v` and it logged 'process token enables: []'. The report says 'the privilege removal is a no-op here and is unproven on an elevated token'.
  - Fix: Factor the removal over a privilege list. Add a self-test that removes a privilege every user token holds enabled, SeChangeNotifyPrivilege. It should assert the privilege is absent inside fn (thread token) and still present after the helper returns (process token, and the thread token reverted). That proves the impersonate, remove and revert steps on any Windows machine, including a non-elevated one. Drop the unreachable ERROR_NOT_ALL_ASSIGNED branch, or read GetLastError explicitly.
- **minor** `internal/hostperm/policy.go:487-499 (longNameAliases), :286-305 (spellings)` — The fix applies a rule at its real name only when the rule's concrete prefix (the segments before the first glob) holds an 8.3 segment. A path is judged only as given and by its long name, never by its short spelling. So a rule that puts an 8.3 name after a glob still misses when the caller names the long file. Example: Read(CREDEN~1.SEC), which compiles to **/CREDEN~1.SEC, or Read(**/CONFIG~1/**). concretePrefix is 0, so no alias is made, and neither candidate spelling of <root>/configuration/credentials.secret contains creden~1.sec. Literal patterns (pt.literal) are skipped entirely as well. The brief asked that every spelling of the served file be judged.
  - Evidence: policy.go:492-494: `prefix := concretePrefix(pt); if prefix == 0 || !spelledAsAlias(pt.segs[:prefix]) { return nil }`. spellings() adds only osAlias(p), the GetLongPathNameW form, and link resolutions. No GetShortPathNameW spelling is added.
  - Fix: When the RuleSet holds any non-negated rule with a tilde anywhere in its segments (flag it at compile time, so a rule set without one still pays no syscall), add the path's GetShortPathNameW spelling to Evaluate's candidates. Add seam rows to shortname_seam_windows_test.go for 'Read(CREDEN~1.SEC)' and 'Read(**/CONFIG~1/**)' with the long path. Alternatively, document the gap as a known residual in doc.go and security.md.
- **minor** `docs/security.md:39-45; internal/hostperm/doc.go:37-40` — This commit adds two behaviours that users can see, and neither the package doc nor the user security doc describes them. First, a rule spelled through 8.3 names now also applies at the file's real name. Second, with any Read deny or ask rule in force, a retrieval path holding an 8.3-shaped segment (~ then a digit) that names nothing on disk is refused. That refusal is answered as a host deny ('denied'), even when only ask rules are in force. security.md still says a deny rule answers denied and an ask rule is refused with a different reason, so it misdescribes this new refusal.
  - Evidence: policy.go:264-267 returns `Decision{Effect: Deny, Rule: unresolvedShortName}` whatever the effects of the rules in force. git diff bb54c6ba HEAD touches neither docs/security.md nor internal/hostperm/doc.go.
  - Fix: In both places, add one sentence each: a rule written with 8.3 names also holds at the real name, and a path whose 8.3 name cannot be expanded (a deleted file's short name) is refused as denied under any deny or ask rule, because its long name cannot be judged. Note the reason code it answers with.
- **minor** `internal/hostperm/shortname_seam_windows_test.go:21-24` — The comment explaining why the seam exists is false, and the implementer's own report corrects it: this machine's volume does record 8.3 names, and the internal/mcp row runs here rather than skipping.
  - Evidence: The comment reads 'this machine's volume records none, so without the fake the 8.3 rows here and in internal/mcp skip locally'. I ran `go test ./internal/mcp -run TestHostPolicy_AShortNameSpellingIsRefused -v` here and all 10 subtests ran and passed, with no SKIP. The report says 'Correction to the brief: this machine's C: volume DOES record 8.3 names'.
  - Fix: Reword it: the local volume records 8.3 names, but the local TEMP has no short segment, so the CI shape (a short TEMP parent under a mixed rule) never arose here. The fake makes that shape, and an unexpandable short name, deterministic on any volume.
- **nit** `internal/hostperm/policy.go:483-486 (longNameAliases doc), :515-517` — An alias is never carvable, so a working-directory rule spelled short ('./CONFIG~1/**') followed by a carve-out ('!./CONFIG~1/public.txt') now refuses public.txt through the long-name alias. The carve-out cannot reopen it. This refuses more than the host would, which fails closed, but it is undocumented.
  - Evidence: The new pattern{...} built in longNameAliases leaves carvable false, and ruleList.match checks non-carvable patterns before carvedMatch.
  - Fix: Document this in the longNameAliases comment and the doc.go 'refuses more' paragraph. Alternatively, give the alias of a carvable rule the long-name spelling of its carve-outs too.
- **nit** `internal/paths/pathstest/privileges_other.go:6-9` — The comment says the Linux gate never runs as root. The project's WSL Linux verification container runs as root. Every caller of the helper is Windows-only (a _windows_test file or a GOOS skip), so nothing behaves wrongly, but the stated reason is wrong.
  - Evidence: The comment reads 'except running as root, which the Linux gate never does (it runs non-root)'. The project memory on WSL2 Linux verification says it runs as root.
  - Fix: Reword it: off Windows the helper's only callers are Windows-only rows, so there is nothing to remove. Root is out of this helper's scope.
- **nit** `commit 74939cbe` — This commit's message has no 'Refs:' footer; the other four commits in the range carry 'Refs: V6-VERIFY, C…'.
  - Evidence: `git log -1 --format=%B 74939cbe` ends after the body with no Refs line.
  - Fix: When the coordinator integrates, add 'Refs: V6-VERIFY, C4.6, C7.2' (reword on cherry-pick or squash into the report commit).
- **nit** `internal/paths/layout_test.go:98-101` — A pre-existing check still skips the row when the plain-create probe gets past the deny entry: t.Skip("platform: current token bypasses the test-owned deny ACE..."). The brief says a skip on CI is never the fallback for these rows. The new rename precondition is loud, but this create check runs before it, outside WithoutBackupPrivileges.
  - Evidence: `if probeErr == nil { ...; t.Skip("platform: current token bypasses the test-owned deny ACE; file-create permission scenario unverified") }` remains unchanged above the new block.
  - Fix: Move the create probe inside WithoutBackupPrivileges and turn the skip into a loud require that names EnabledBypassPrivileges, as the rename probe does.

## Fix seat (review resolution) — status `done`, head `4c221481`

### Root cause

(1) The c7165782 privacy fix expanded a rule's 8.3 names only in its concrete prefix. An 8.3 name after a glob or in a literal rule was never compared with the path, whose spellings were only its own and its long name. Fixed by comparing such rule segments with each path segment's GetShortPathNameW name, and refusing paths that do not exist while such a rule is in force. (2) The elevated-runner held-open red has no confirmed cause: a share-mode denial is not documented to give way to backup privileges. It is now proven through an injected refusal, and the real-handle row reports what the read got. The three ACL rows remain explained as the documented effect of SeBackupPrivilege and SeRestorePrivilege with backup-intent opens; the helper that removes those privileges is now proven on a privilege every token holds.

### Summary

The fix seat checked all five reviewer findings on closeout/w16-winci, agreed with each, and fixed each one. The branch is at 4c221481 in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16-winci. Items (1)-(4) are as the implementer left them, apart from the changes below. All four of the fix seat's commits are behind a new failing test or a mutation that turns the test red.

## Review resolution
1. **Held-open row's cause is a guess (agreed, now fixed).**
   - A share-mode refusal is not documented to give way to backup privileges. Backup tools such as robocopy /B still fail on files in use for exactly that reason. So e6297cbf's backup-semantics story does not explain TestStageBinary_NeverRemovesACopyHeldOpen.
   - The nightly run recorded only that stageBinary returned nil. It cannot tell whether OpenShared succeeded or failed some other way, which would have led to a remove-and-restage.
   - Fix (a9da859d): stageBinary now hands its first check of an existing copy to a new stageBinaryVerifying, where that check can be injected.
   - New row TestStageBinary_KeepsACopyWhoseCheckIsRefusedAsHeldOpen injects ERROR_SHARING_VIOLATION and ERROR_LOCK_VIOLATION. It asserts each error is returned and the very same sealed copy is kept. As a control, ERROR_ACCESS_DENIED gets the copy replaced.
   - Mutation: with the heldOpenElsewhere branch disabled the row goes red (runs/48).
   - The real-handle row is kept as an asserting row, not demoted to a diagnostic. Its precondition now prints what this token's read got (`got %v`).
   - The cause stays unexplained in the code comment and here. e6297cbf's commit body is not rewritten; this report and a9da859d correct it.
2. **The privilege helper never removed anything in a run (agreed, now fixed).**
   - Fix (33029245): the removal is factored into withoutPrivileges over a list of privileges.
   - New in-package row TestWithoutPrivileges_RemovesAHeldPrivilegeOnlyForFn removes SeChangeNotifyPrivilege, which every user token holds. It asserts the privilege is gone inside fn under a thread token, and that once the helper returns the thread is back on the process token, which still holds it enabled.
   - Mutations: dropping the removal goes red ("still held after its removal"); dropping the revert goes red ("the impersonation ends when the helper returns"). See runs/46.
   - I confirmed in x/sys's zsyscall that AdjustTokenPrivileges returns nil on ERROR_NOT_ALL_ASSIGNED. The dead branch is gone, and the helper now reads each privilege back and fails if it is still held.
3. **An 8.3 name after a glob is not judged (agreed, real privacy gap, now fixed).**
   - Reproduced red (runs/40) with Read(CREDEN~1.SEC), Read(**/CONFIG~1/**), Read(**/CONFIG~1/credentials.secret), a short root with a long file, and a literal rule Read(./x/../CONFIG~1/CREDEN~1.SEC). Every one let the long path of the denied file through.
   - Fix (c1f1abd4): longNameAliases also reports a "pending" 8.3 name, meaning one it could not expand: after a glob, in a literal rule, or naming a missing entry. That sets RuleSet.shortPending, so a rule set without one pays nothing.
   - Evaluate then takes each spelling's GetShortPathNameW form, aligned segment by segment (new osShortName, with a getShortPathName seam). A rule segment containing ~ matches a path segment by either its own name or its 8.3 name, in prefixRow and in literal rules.
   - Carve-outs (! rules) are compared with the path's own names only, so the 8.3 form never reopens a file. A mutation that removes this guard goes red.
   - A path that does not exist has no 8.3 names to compare. While such a rule is in force it is refused as Deny (unknownShortName), so it fails closed. With no such rule, a deleted file is judged as before.
   - The seam fake now covers both directions, long-to-short and short-to-long.
4. **Docs (agreed).** docs/security.md and internal/hostperm/doc.go now say:
   - a rule written with 8.3 names also holds at the real name;
   - the two 8.3 refusals answer `denied` with the deny reason under any deny or ask rule, never the ask reason.
5. **Seam comment was false (agreed).** The comment in shortname_seam_windows_test.go is reworded. This machine's volume does record 8.3 names; what never arose locally was CI's short TEMP parent under a rule mixing short and long names.

## Tests run
I ran tests one row or package at a time with -p 2, no load generators, and no -race on whole packages. Nothing failed on wall-clock timing.

## Criterion changes
None. No check was weakened. The real-handle held-open row keeps its assertion, and a new seam row is added beside it.

### Commits

- c7165782 fix(hostperm): judge a rule's 8.3 spelling at its real name (implementer)
- e6297cbf test(paths): enforce deny-ACE fixtures for an elevated token (implementer)
- 8fd9547b test(daemon): assert how Stop's wait behind a drain ended (implementer)
- 00c1c04a test(devtool): make the relative-bundle fixture volume-proof (implementer)
- 74939cbe test(v6): record the w16-winci package runs and lint (implementer)
- c1f1abd4 fix(hostperm): judge an 8.3 name a rule holds after a glob (fix seat: review findings 3, 4, 5)
- 33029245 test(paths): prove the privilege removal on a token that holds one (fix seat: review finding 2)
- a9da859d test(daemon): prove a held-open copy is kept for any token (fix seat: review finding 1)
- 4c221481 test(v6): record the w16-winci fix-seat runs

### Tests

- `go test ./internal/hostperm -run 'TestShortNames_' -count=1 -p 2 -v (before fix)` — FAIL as intended: 5 new subtests of TestShortNames_EverySpellingOfTheServedFileIsJudged plus TestShortNames_AShortRuleAfterAGlobRefusesWhatItCannotJudge (runs/40)
- `go test ./internal/hostperm -run 'TestShortNames_' -count=1 -p 2 -v (after fix)` — PASS, 12 subtests plus 2 rows (runs/41); a mutation removing the carve-out guard turned the carve-out row red
- `go test ./internal/hostperm -count=1 -p 2` — ok (runs/42)
- `go test ./internal/mcp -run '^TestHostPolicy_AShortNameSpellingIsRefused$' -count=1 -v` — PASS, 10 subtests, no SKIP (runs/43)
- `go test ./internal/mcp -count=1 -p 2 -timeout=30m` — ok 144.3s (runs/52)
- `go test ./internal/paths/pathstest -run '^TestWithoutPrivileges_RemovesAHeldPrivilegeOnlyForFn$' -count=1 -v (before helper)` — FAIL as intended: build failed, undefined privilegeState (runs/44)
- `go test ./internal/paths/pathstest -count=1 -v` — ok, all 6 rows PASS (runs/45); removal and revert mutations each go red (runs/46)
- `go test ./internal/daemon -run '^TestStageBinary_KeepsACopyWhoseCheckIsRefusedAsHeldOpen$' -count=1 -v (before seam)` — FAIL as intended: undefined stageBinaryVerifying (runs/47); with the heldOpenElsewhere branch disabled, FAIL 'a copy that cannot be verified is never returned to be run' (runs/48)
- `go test ./internal/daemon -run '^TestStageBinary_KeepsACopyWhoseCheckIsRefusedAsHeldOpen$' -count=1 -v` — PASS; TestStageBinary_NeverRemovesACopyHeldOpen and TestStageBinary_RestagesACopyItCanNeverRead also PASS (runs/49)
- `go test ./internal/daemon -count=1 -p 2 -timeout=30m` — ok 388.1s (runs/50)
- `go test ./internal/paths -run '^TestTerminatePartialTail_UnreadablePathIsLeftAlone$' -count=1 -v` — PASS (runs/53)
- `go test ./internal/paths -run '^TestEnsureLayout_WriteAtomicFailureIsPropagated$' -count=1 -v` — PASS (runs/53)
- `go test ./test/guards -run '^TestGuard_EveryHomeReachingTestPackageIsolatesHome$' -count=1 -v` — PASS (runs/51)
- `go test ./test/guards -run '^TestGuard_HotFilesAreReadWithDeleteSharing$' -count=1 -v` — PASS (runs/51)
- `go test ./test/docs -count=1` — ok
- `go vet on hostperm, mcp, daemon and paths/pathstest, on Windows and with GOOS=linux; go build ./...` — clean
- `go run ./tools/devtool fmt-check` — clean
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (at head)` — all 8 checks PASS

### Criterion changes

- None in the fix seat. No check weakened. New rows: TestShortNames_AShortRuleAfterAGlobRefusesWhatItCannotJudge, 5 subtests of TestShortNames_EverySpellingOfTheServedFileIsJudged, TestWithoutPrivileges_RemovesAHeldPrivilegeOnlyForFn and TestStageBinary_KeepsACopyWhoseCheckIsRefusedAsHeldOpen. TestStageBinary_NeverRemovesACopyHeldOpen keeps its assertions, and its precondition message now includes the read's actual error.
- The implementer's change stands as reported: TestStop_IsNotHeldBehindASessionEndsDrain's total bound moved from 2.45 s to 15.45 s, built only from named constants, and the Canceled-versus-DeadlineExceeded assertion now carries the discrimination (see needs_owner).

### Open issues

- Why TestStageBinary_NeverRemovesACopyHeldOpen failed on the elevated hosted runner is still unknown. The real-handle row keeps its assertion, now with a precondition that prints what the read got (the brief's fallback), so it may fail on the next hosted run. If it does, the precondition message names the mechanism. The product behaviour it names is proven for any token by TestStageBinary_KeepsACopyWhoseCheckIsRefusedAsHeldOpen.
- e6297cbf's commit body attributes all four elevated reds to backup semantics. That holds as the documented reading for the three ACL rows (RestagesACopyItCanNeverRead, TerminatePartialTail_UnreadablePathIsLeftAlone, EnsureLayout_WriteAtomicFailureIsPropagated), but not for the held-open row. The body was not rewritten; this report and a9da859d correct it.
- The backup-privilege removal has still never run on a token that holds those privileges. Its steps are now proven on SeChangeNotifyPrivilege; the first real exercise is the next hosted Windows run.
- No local test could exercise the internal/mcp 'short project root' subtests in CI's exact shape: a short TEMP parent with real 8.3 names on disk. The seam rows cover that shape and more. The next hosted race-windows run should be checked.
- Coordinator night runs are still owed: Linux container, test/e2e, test/integration and the whole-tree -race. The test/e2e, test/guards and test/integration packages import internal/hostperm.

### Needs the owner

- No new budget or bound numbers. TestStop_IsNotHeldBehindASessionEndsDrain's total bound is composed only of existing named constants: sessionEndGrace (200 ms in the row), sessionEndAbandonAfter (250 ms) and stopCleanupBound (15 s), 15.45 s in all. If it is wrong, a Stop that spends more than its own cleanup ceiling fails the row. The coordinator may still want to approve this as a criterion change, because the total moved from 2.45 s to 15.45 s; what tells the two outcomes apart is now the Canceled-versus-DeadlineExceeded assertion.
- Product behaviour change for approval (C4.6, fail closed). With any Read deny or ask rule in force, a retrieval path holding an 8.3-shaped segment (~ followed by a digit) that names nothing on disk is refused, because its long name cannot be established. A deleted file whose real long name happens to contain ~1 will also be refused under any rule.
- Product behaviour change for approval (C4.6, fail closed, added by the fix seat). While a Read deny or ask rule names an 8.3 name that cannot be expanded at compile time (after a glob, as in Read(**/CREDEN~1.SEC); in a literal rule; or naming a missing entry), every retrieval of a path that no longer exists is refused as denied (unknownShortName), because a deleted file's own 8.3 names cannot be known. Rule sets without such a rule are unaffected. If this is too strict, the alternative is to judge deleted files on their long names alone and document the gap as a residual.

## Independent verification of the fix seat: needs-fixes

- **minor** `internal/hostperm/policy.go:583 (longNameAliases, pending for glob rules); also rules.go:313 and :377` — Fix seat commit c1f1abd4 turns on the 8.3 comparison and the fail-closed refusal of missing paths for any rule with a tilde after its concrete prefix, not only for a rule that names an 8.3 name. Common rules with a tilde that are not 8.3 names, such as the editor-backup glob Read(**/*~), set RuleSet.shortPending. Every retrieval of a deleted file's history is then refused as Deny (unknownShortName). That breaks the documented contract: docs/security.md and doc.go say this refusal happens only 'while a rule names an 8.3 name after a glob'. The trigger is also inconsistent within the same function. The literal branch at :580 uses shortShaped (a tilde then a digit), but the glob branch uses strings.Contains(s, "~").
  - Evidence: I ran a scratch copy of HEAD with an added in-package test, using newShortEnv and a deleted path <long>/src/deleted-main.go. With deny Read(**/*~) the result was `deny "(a rule names an 8.3 name, and a path that does not exist has none to compare)"`. With Read(./elsewhere/**) the same path was allowed. Code: `pending = slices.ContainsFunc(pt.segs[prefix:], func(s string) bool { return strings.Contains(s, "~") })` against `return nil, slices.ContainsFunc(pt.segs, shortShaped)` for literals. The reviewer suggested 'a tilde anywhere', but the docs the fix seat wrote describe the narrower, 8.3-name trigger.
  - Fix: Compute pending in the glob branch with an 8.3-shape test. Use shortShaped, widened if wanted to a tilde followed by a digit or a glob metacharacter, as in CREDEN~?.SEC. Use the same predicate where prefixRow and literalEqual decide whether to compare against the 8.3 alt. Add a seam row: under Read(**/*~), a deleted file is allowed, and an existing foo.txt~ is still denied.

