# Host-permission workstream report — C1.9 V6-HOST-1

Branch `closeout/hostperm`. Workflow `wf_16dd5d95-b3a`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `7f80138`

### Root cause

Three defects, each reproduced before it was fixed. (1) V6-HOST-1: internal/mcp/authorize.go only checked recorded provenance and whether a path is inside the project (paths.Norm + paths.ResolvesInside). Nothing read the host's permission settings. So a file captured while readable and later put under a permissions.deny Read rule was still served by expand (by tool_use_id and root hash) and by re_read. Evidence: runs/01-red-before-fix.log. (2) A leftover gap from V6-AUTH-2, found while listing the retrieval forms: re_read with `at: sha256:<hash>` authorized the path argument but never checked where the hash came from. After a junction escape, the whole 200 000-byte archive of src/auth.ts was served under docs/ok.md (same log). (3) A defect in my own first integration: the host check was given the paths.Norm result, and Norm swaps an in-project symlink for its target. A deny rule written on the link's own path never matched. Windows hid this because EvalSymlinks does not follow a junction (runs/03). It reproduced on Linux as uid 10001 at ed5a80c plus one test file (runs/04). Fixed in 6dacb7c.

### Summary

C1.9 / V6-HOST-1 (report content; the harness blocked writing report.md, so this is the report).

I could not use parallel subagents: this session has no spawn tool. Independent work was run concurrently where possible (Linux container runs alongside Windows runs).

DOCUMENTATION SNAPSHOT
Fetched 2026-09-22 with curl -sSL https://code.claude.com/docs/en/{permissions,settings,managed-settings,server-managed-settings,env-vars,iam}.md. The sha256 of each file is in runs/INDEX.txt. The iam page is now the Authentication page and has no rule text.

Quoted text relied on:
- "Rules are evaluated in order: deny, then ask, then allow" and "If a tool is denied at any level, no other level can allow it". So rules from every file are combined, and allow rules are never read.
- "`Read` | Matches all file reads"; tool-name globs such as "`*` matches every tool".
- Pattern anchors: "//path Absolute", "~/path … home", "/path … relative to the settings source" (project/local = working directory, user = ~/.claude), "path or ./path … current directory".
- "Bare filenames follow gitignore semantics and match at any depth"; "Deny and ask rules: Read(secrets/**) matches a directory named secrets at any depth".
- "isn't usable as a gitignore pattern still guards that exact path".
- Negation: "`!` … carves … out of the path or ./path rules listed before it … A `!` rule listed first carves nothing out"; "The carve-out reaches only rules from the same source"; "can't reopen a file inside a directory that a rule blocks as a whole".
- Symlinks: "Deny rules: apply when either the symlink path or its target matches"; a rule written through a symlinked directory "also applies at the directory's real location".
- Windows: "paths are normalized to POSIX form … C:\Users\alice becomes /c/Users/alice".
- Read parameter rules: `file_path` is ignored, and "A parameter the model omits is never matched".
- Grep: "Claude Code applies Read deny rules to that directory".
- File locations:
  - managed-settings.json and managed-settings.d/*.json under /Library/Application Support/ClaudeCode, /etc/claude-code, and C:\Program Files\ClaudeCode
  - the Settings registry value under HKLM and HKCU \SOFTWARE\Policies\ClaudeCode
  - ~/.claude/remote-settings.json
  - ~/.claude/settings.json, or the same file under $CLAUDE_CONFIG_DIR
  - .claude/settings.json and .claude/settings.local.json (the local file lives at the main checkout's root, except on Windows)
- env-vars: CLAUDE_CODE_SUBPROCESS_ENV_SCRUB "removes … CLAUDE_CONFIG_DIR" from hooks and MCP servers.

WHAT CHANGED
New package internal/hostperm: foundation-only, read-only, no exec, no network.
- It reads every settings source listed above: the managed file and drop-ins, the Windows policy registry (through the standard library's syscall package, so no new dependency), the server-managed cache, user settings, project settings, and local settings, including a linked worktree's main checkout.
- It combines their Read deny and ask lists and returns Allow, Ask or Deny for one absolute path.
- Any source that exists but cannot be read, is malformed, is over 8 MiB, or is a macOS profile it cannot decode returns ErrUnavailable.
- Snapshot() stats every source on each call and re-reads only when something changed. A file changed within the last 2 seconds is always re-read. Parse errors are cached until the file changes; read errors are not cached.
- Registered in:
  - plans/OWNERS.tsv (SP-13, 90% floor, probe Check)
  - importrules.go (foundation-only; mcp may import it)
  - 00-ARCHITECTURE §3.2 and §6.4
  - the test/guards stub registry (count 25→26)
  - the IT-6 floor table

internal/mcp:
- Every record that has a path is now checked against one rule snapshot per call, after the containment check. Forms covered:
  - expand by tool_use_id, root hash and chunk hash (every origin is checked)
  - re_read in every `at` form, including a hash named under a different path
  - recall: the hit and its summary are withheld and counted in `denied`; a new `host_policy` field appears when the settings could not be read
  - why: new `evidence_withheld` field; the evidence size and the expand hint are dropped
  - dropped: entries that point at a stored tool_use_id or a captured path are withheld; new `denied` and `host_policy` fields
- timeline carries no path-bearing data, and a structural test pins that.
- Refusal reasons are distinct, never say "not found", and never echo the path or the rule:
  - deny: denied, "…host's current permission rules deny reading…"
  - ask: denied, "…require approval…"
  - unreadable settings: unavailable, "host policy unavailable…"
- Pathless records behave exactly as V6-AUTH left them.
- ToolDeps.HostPolicy is optional. When it is nil, the real-environment policy is built from ProjectRoot, so the daemon (not touched) and every other caller get it with no wiring change.
- New counters: mcp.host_policy_{denied,ask,unavailable}. A loud log line is written when the policy is unavailable.

Docs: security.md §1 and §8, cannot-do.md §5 (new "It cannot see every host permission rule"), troubleshooting.md §4. No tool description changed, so gen-mcp-docs was not needed.

WHERE THE DOCS LEFT A CHOICE, I CHOSE THE READING THAT REFUSES MORE
- ask is treated as a refusal.
- `./x` is treated like bare `x`.
- `seg/*` matches at any depth, like the documented `seg/**`.
- A `/path` rule in a managed file or the cache is anchored at both the project and the file's own directory.
- Separate managed files cannot carve rules out of each other.
- Tool-name globs also honour `?` and `[...]`.
- On Windows, a rule written with a drive letter or backslashes also gets a POSIX alias.
- A directory-only rule also matches a plain file of that name.
- A malformed Read rule fails closed (the host skips it).
- A rule written through a symlinked directory is applied at the real location on every platform, not just macOS and Linux.
- allowManagedPermissionRulesOnly and the managed-tier "first-wins" rule are ignored (worst case: over-refusal).

RECORDED DECISION THIS CHANGES
authority-review.md §6 explicitly rejected "Option D: parse settings files". C1.9's owner default implements it anyway. I kept the review's framing everywhere (code comments and docs): this is the part of the host's decision a plugin can read, applied fail-closed, never parity with the host and never a PASS by parity.

COST
Measured under co-load, runs/10:
- Snapshot with nothing changed: 0.3–1.8 ms per call.
- Evaluate with 10 rules: 0.45–1.7 ms per path. About 95% of that is the link walk, which V6-AUTH's ResolvesInside already does for the same path.
- Evaluate with no Read rules: about 8 ns.

B-F BUDGET (fails, co-load, not this change)
- Full Windows mcp run: only TestBudgetBF failed, p95 328 ms against a 250 ms limit.
- Re-run alone at head: p95 3.93 s (CPU about 84%, 24 other *.test processes).
- The base cf31e01 alone, same conditions: p95 5.24 s, worse than head.
- Classified as co-load. The per-call cost I add is about 1 ms.

CRITERION CHANGES
None. Behaviour that became stricter as a side effect:
- re_read `at:sha256` now also requires provenance. A store that cannot report origins answers unavailable, the same as expand by hash.
- why omits the hint and sets `evidence_withheld` when the evidence is denied for any reason, including V6-AUTH. An `unavailable` answer keeps the old shape, which TestWhyFindsDecisionInLatestCheckpoint still pins.

RESIDUAL GAP (documented)
These are invisible to a plugin: session-only /permissions rules; --allowedTools, --disallowedTools, --settings and --setting-sources; PreToolUse hooks; an embedding host's managedSettings; policyHelper output; server-managed policy not yet cached on disk; a session working directory that is not the project root; a scrubbed CLAUDE_CONFIG_DIR; WSL inheriting Windows policy.
- Pathless records are never matched: `cat .env` shell output is served under Read(./.env).
- A Grep is judged by the directory it searched, as the host does.
- already_tried and record_eliminated are not gated. They return the model's own notes about a target, not archived file content.

### Commits

- 2f04ae7 feat(hostperm): evaluate the host's Read deny and ask rules
- 7df2c1d fix(mcp): honour the host's Read rules on every retrieval form
- ed5a80c test(security): host Read rules govern packaged archived retrieval
- 6dacb7c fix(mcp): judge a link's own spelling against host Read rules
- 9df73a4 docs(security): state what host Read rules retrieval honours
- ee106e4 docs(cannot-do): cite the committed host-rule evidence directory
- e98f408 test(hostperm,mcp): clear the two lint findings in host-rule tests
- 7f80138 docs(v6-closeout): record the C1.9 host-rule evidence

### Tests

- `go test ./internal/mcp -run 'HostDenyRed|HashFormRed' -count=1 -v (unfixed tree, cf31e01 + new tests)` — FAIL as expected, reproducing V6-HOST-1 and the re_read hash-form gap (runs/01-red-before-fix.log) <!-- runpatterns: historical red-before-fix run on cf31e01 plus the then-new tests, whose names changed before commit; the alternation is also split at the shell-pipeline character by this checker's parser -->
- `same two tests after the fix` — PASS (runs/02-red-after-fix.log)
- `go test ./internal/mcp -run TestHostPolicy_ALinkSpellingIsJudgedToo` (Linux container, uid 10001, at ed5a80c + that test) — FAIL as expected, the link-spelling defect (runs/04); Windows passed before the fix because junctions are not followed (runs/03)
- `go test ./internal/hostperm -count=1 -coverprofile (Windows)` — PASS, 95.1% coverage; 2 tests skip on Windows because they need a platform capability and run on Linux
- `go test ./internal/hostperm -run '^$' -bench . -benchmem -count=5 -benchtime=2000x (Windows, co-load)` — Measured; see summary COST (runs/10-bench-windows.log)
- `go test ./internal/mcp ./internal/hostperm -count=1 -timeout=30m -skip '^TestBudgetBF$' at e98f408 (Windows)` — PASS (runs/33)
- `go test ./internal/mcp -count=1 -timeout=30m (Windows, no co-load declared, 7df2c1d tree)` — Only TestBudgetBF failed: p95 327.68ms > 250ms (runs/30)
- `go test ./internal/mcp -run '^TestBudgetBF$' alone: head (ee106e4) vs base cf31e01 (Windows)` — Both FAIL from co-load: head p95 3.93s, base p95 5.24s under the same load (runs/31, runs/32); classified co-load, not caused by this change
- `QOMPACK_SECURITY_ARTIFACTS=runs/security-windows go test ./test/security -run 'TestV6_' -count=1 -v (Windows)` — PASS, 3 cases; record v6_host_read_rules_govern_archived_retrieval outcome=verified (runs/20)
- `go test ./test/guards -run 'StubRegistry|StubGraph|AllStubs|Import|Owners'; go test ./tools/devtool -run 'Import|Owners|Cover|Floor|PlanCoverage|Landed' (Windows)` — PASS
- `go test ./test/docs -count=1 (Windows)` — PASS (runs/40)
- `Linux inner runner at 9df73a4, uid 10001, GOMAXPROCS=4, -race, --coload: ./internal/hostperm ./internal/mcp` — PASS: hostperm 106 pass, 0 skip; mcp 365 pass, 0 skip; no race report (runs/50, linux-cx-hostperm-9df73a4-*)
- `Linux inner runner at ee106e4, uid 10001, --no-race --coload: ./test/security ./test/guards ./tools/devtool (full)` — devtool PASS (250). security: 22 pass, 2 fail. guards: 158 pass, 10 fail (7 carried defects + TestGuard_TheV1DeliveryPositionHasOneEncoder). The Linux host-rule record is verified (runs/51)
- `Linux, same runner at base cf31e01 with -run on the 3 non-carried failures` — The same 3 fail on base, so they pre-date this branch: ReRead 'no historical version' and ArchivedText 'never indexed' come from the capture/ingest regression (C1.1), plus daemon parseDeliveryPositionV1 (runs/52)
- `go run ./tools/devtool fmt-check; go vet (windows/linux/darwin) on touched packages` — clean (runs/60)
- `go run ./tools/devtool lint (at ee106e4)` — golangci-lint: 2 findings in my test files, fixed in e98f408 and re-verified clean for Windows, Linux and darwin targets (runs/62, runs/63). bindeps FAIL predates this branch: x/sys/unix reaches the binary via internal/paths at cf31e01 too (runs/64). stubskips FAIL predates this branch: 6 problems, none in hostperm or mcp; my 2 skips are accepted platform notices. nomagic, importgraph (72 packages), testdeps, sleepcheck, runpatterns, docmarkers, coveragefloors PASS (runs/61)

### Open issues

- report.md was not committed: the subagent harness refused the file write. The report content is in this output, and runs/INDEX.txt indexes the committed evidence. The coordinator may want to paste the summary into plans/sdd/V6-closeout/hostperm/report.md.
- No live UAT: no agent-run session on the installed host has exercised a forbidden archive read yet (V6-report §5 row stays open for the UAT phase).
- Nothing was run on macOS. The profile fail-closed path and case folding are unit-tested only with a simulated platform.
- B-F (p95 250 ms) could not be judged on this co-loaded machine; head and base both breach under load. An isolated timing lane (ADR-0010) should confirm it.
- hostperm.resolveLinks duplicates paths.resolveLinks, which is unexported, and paths was out of scope. Exporting it from paths would remove the duplication and the second link walk per path.
- The server-managed cache format (remote-settings.json) is undocumented. `permissions` objects are collected at any depth (bounded), but no real cache file was available to test against.
- test/security's shared retrievalBody decodes recall's `denied` count as a bool, so describeEnvelope renders such a recall as non_json. This predates the branch; my new case decodes recall itself.
- Pre-existing failures outside this task, seen on both base and head: TestSecurity_ReReadAnswersFromTheArchiveNotTheLiveDisk and TestSecurity_ArchivedTextIsDataNeverAnInstruction (capture/ingest, C1.1), TestGuard_TheV1DeliveryPositionHasOneEncoder (internal/daemon parseDeliveryPositionV1), the devtool bindeps x/sys/unix violation, and the stubskips problems.

### Needs the owner

- Acknowledge that C1.9 supersedes authority-review.md §6's explicit rejection of settings-file parsing (Option D), on the stated terms: reconstructed from settings files and fail-closed, never parity with the host and never a host PASS.
- Accept or overrule the choices that refuse more than the docs strictly require, especially: (a) a present macOS managed configuration profile, which this build cannot decode, makes ALL path-bearing retrieval unavailable; (b) ask rules refuse; (c) a missing home directory or unreadable settings fail closed; (d) managed first-wins and allowManagedPermissionRulesOnly are ignored.
- Decide whether V6-HOST-1's inventory row can move to implemented with the residual gap stated, or stays open until a live forbidden-archive-read UAT row exists.

## Independent review

### review:hostperm:a: needs-fixes

- **blocker** `internal/mcp/authorize.go:134-157 (authorizeHost) with :171 (authorizePath) and internal/mcp/handlers_span.go:290 (re_read)` — On Windows, the host check can be bypassed by spelling the path differently. Commit 6dacb7c changed authorizeHost to judge only the caller's or record's own spelling, where it used to judge the paths.Norm result. But content is still served under paths.Key(norm). paths.Norm calls filepath.EvalSymlinks, which on Windows rewrites a path to its canonical long name: it expands 8.3 short names, drops trailing dots and spaces, and fixes case. hostperm's resolveLinks does none of this; it only follows reparse points. So a spelling that Norm maps to the denied file gets past the deny rule, and re_read serves the file's archived content. The same gap applies to any record whose captured path is a non-canonical spelling (expand by tool_use_id, recall, dropped), and to a ProjectRoot spelled with 8.3 names when matched against `//c/...` rules.
  - Evidence: I reproduced this read-only with a `go test -overlay` test added to internal/mcp. The fixture captures configuration/credentials.secret, the file exists on disk, and project settings deny `Read(./configuration/credentials.secret)`. Results of re_read:
- `configuration/credentials.secret`: denied.
- The absolute path: denied.
- `configuration/credentials.secret.`: served, found:true with the archived marker.
- `configuration/credentials.secret ` (trailing space): served.
- `configuration./credentials.secret`: served.
- `CONFIG~1/CREDEN~1.SEC`: served. The response was {"found":true,...,"path":"configuration/credentials.secret",...,"content":"REVIEW-SHORTNAME-MARK-91ab\n"}.
This machine creates 8.3 names; `dir /x` shows CREDEN~1.SEC. At 7df2c1d, authorizeHost was handed norm and would have refused all of these. 6dacb7c replaced that spelling instead of adding the link spelling alongside it.
  - Fix: Judge every spelling that can reach the served content. In authorizeHost, refuse when either spelling matches a deny rule (take the stricter of the two answers):
- the raw spelling, as now, so link spellings are still caught;
- filepath.Join(<EvalSymlinks'd root>, norm), which is the canonical path whose history key is served.

This could be a small authorizeHost(ctx, raw, norm) that evaluates both. For defence in depth, also canonicalize in hostperm on Windows: strip trailing dots and spaces from each segment, and expand short names with GetLongPathNameW when the path exists. Add Windows regression tests for trailing dot, trailing space, a dotted directory and an 8.3 short name, across re_read latest/turn/timestamp and expand by tool_use_id for a record captured under a short-name spelling.
- **minor** `internal/hostperm/policy.go:283-330 (match/carvedMatch) and internal/hostperm/rules.go:312 (matchSegments); no cap on rule count` — The cost of evaluating one path grows with the number of rules, and nothing limits the rule count. The only bound is the 8 MiB per-file cap. A committed or generated .claude/settings.json with many Read rules makes every path check slow. That check runs for every recall hit, every dropped entry and every hash origin, on every request.
  - Evidence: Measured with an overlay test: 200 000 `Read(src/**/genN/*.x)` deny rules is a 5.7 MB file, under the cap. The rebuild took 497 ms, and Evaluate took about 637 ms per path at depth 6. A 20-hit recall would pay about 13 s. The implementer's measured cost covered only 10 rules, while the task asks for bounded cost.
  - Fix: Cap the total compiled patterns per snapshot, for example 10 000, and fail closed with a parseError of 'too many Read rules' when it is exceeded, as already done for maxDropIns. Optionally, avoid allocating a DP table per matchSegments call by reusing a buffer per Evaluate. Add a benchmark or test at the cap.
- **minor** `internal/mcp/handlers_common.go:151-154 (default HostPolicy from the real environment); affects test/e2e/v4_*_test.go, test/integration/v4_x09_test.go, internal/daemon/mcpop_test.go` — Every ToolDeps built without HostPolicy now reads the machine's real host policy: ~/.claude/settings.json (or CLAUDE_CONFIG_DIR), C:\Program Files\ClaudeCode or /etc/claude-code, and HKLM/HKCU\SOFTWARE\Policies\ClaudeCode. Only the internal/mcp fixture was made hermetic, so the e2e, integration and daemon tests now depend on the machine they run on. A developer or CI runner with a Read(**) style deny, a malformed Read rule, a malformed HKCU value or a macOS profile will see unrelated retrieval tests fail as denied or unavailable.
  - Evidence: fixture_test.go sets Deps.HostPolicy to a hermetic Home and Managed. The other ToolDeps constructions listed (test/e2e/v4_mcp_test.go:69, v4_x06/x07/x10, test/integration/v4_x09_test.go:102, internal/daemon/mcpop_test.go:138,246) pass ProjectRoot and no HostPolicy, so newHandlers builds hostperm.New(Options{ProjectRoot}) against the real environment.
  - Fix: Give the shared e2e and integration helpers (v4Server and similar) and the daemon mcpop tests a hermetic hostperm.Policy: temp Home, empty Getenv, and an empty ManagedSources{}. Alternatively, have the test harness set a documented override, for example a hostperm option the test builds. Keep the real-environment default only for production composition roots.
- **nit** `plans/sdd/V6-closeout/hostperm/runs/INDEX.txt (doc snapshot section)` — The task required recording the documentation snapshot in the report: URL, date, and the quoted rule text relied on. Only URLs and sha256 hashes are committed. The quoted rule text exists only in the implementer's transient StructuredOutput, because report.md was never written, so the committed record cannot show which sentences the design depends on.
  - Evidence: INDEX.txt lists page hashes only. I re-fetched permissions.md, settings.md, managed-settings.md and server-managed-settings.md today, and their hashes match the recorded ones, so the snapshot itself is accurate.
  - Fix: Commit plans/sdd/V6-closeout/hostperm/report.md from the implementer's summary, including the quoted rule text. Or commit the fetched .md snapshots next to INDEX.txt.

### review:hostperm:b: needs-fixes

- **major** `internal/mcp/authorize.go:136-150 (authorizeHost) with internal/mcp/authorize.go:160 and internal/mcp/handlers_span.go:290 (introduced by 6dacb7c)` — On Windows, `re_read` can get around a host deny rule by spelling the path with a trailing dot or trailing space. Commit 6dacb7c changed authorizeHost to judge only the caller's spelling (`base`). Before, it judged the paths.Norm result. Content is still looked up by paths.Key(norm), and Norm runs filepath.EvalSymlinks, which on Windows turns `secret.env.` and `secret.env ` into `secret.env`. So the host check sees `config/secret.env.`, which matches no rule, while the store serves `config/secret.env`. That breaks the claim in security.md §1 and the commit message that `re_read` checks the rules in every `at` form. The at-sha256 form is not affected, because authorizeHash judges the recorded origin paths. 7df2c1d was not affected either: it passed `norm`.
  - Evidence: Probe on a scratch copy of HEAD 7f80138 (not the worktree): newHPFixture with the file on disk (withFiles{config/secret.env}), project deny rule `Read(./config/secret.env)`, then re_read with several spellings.
- `config/secret.env` → denied
- `CONFIG/SECRET.ENV` → denied
- `config/secret.env.` → {"found":true,"hash":"sha256:8e515bae…","path":"config/secret.env","source":"store",…} and the marker V6-HOST-1-ARCHIVE-7c4e19b2 is served
- `config/secret.env ` → served
- `config/secret.env...` → served
`git show 7df2c1d:internal/mcp/authorize.go` line 160 reads `return h.authorizeHost(ctx, norm)` and handlers_span.go:290 reads `h.authorizePath(ctx, norm)`, so the spelling-only check arrived in 6dacb7c.
  - Fix: Have authorizeHost judge both spellings and refuse when either matches: the caller's or record's own spelling (which keeps the link-spelling fix) and the paths.Norm result joined to h.root (which catches the canonicalized name the store actually serves). For example, authorizePath passes both `path` and `norm` to authorizeHost, which evaluates each and returns the first deny or ask. Do the same in filterDrops. Add a Windows regression case to TestHostPolicy_EveryContentFormRefusesADeniedPath (or a new test) for re_read with `config/secret.env.` and `config/secret.env `, with the file present on disk.
- **nit** `commits ee106e4 and e98f408` — Two of the eight commits have no `Refs:` footer. The other six carry `Refs: V6-VERIFY, C1.9`, and the repository's recent fix and test commits carry Refs trailers.
  - Evidence: `git log --format='%h %s | %(trailers:key=Refs,valueonly)'` shows an empty Refs for ee106e4 (docs(cannot-do): cite the committed host-rule evidence directory) and e98f408 (test(hostperm,mcp): clear the two lint findings in host-rule tests).
  - Fix: When the branch is next rewritten or squashed for integration, add `Refs: V6-VERIFY, C1.9` to both messages. Do not add attribution trailers.

## Fix seat (review resolution) — status `partial`, head `a70e286`

### Root cause

V6-HOST-1: MCP retrieval enforced only project containment and provenance, never the host's Read rules (runs/01). Review findings 1 and 4 have one cause. After 6dacb7c, authorizeHost judged only the caller's or record's spelling, while re_read serves the history of paths.Key(paths.Norm(...)), and on Windows EvalSymlinks maps 'x.', 'x ', 'dir.\x' and 8.3 names to the real file name. hostperm also judged no Win32 alias, including an alias of a link or of an 8.3-spelled root. Finding 2: nothing bounded the rule list, and the matcher rebuilt a DP table per directory prefix per rule, so per-path cost grew without limit (runs/73: 61.7 ms per path at 10,000 rules, 2.73 s at 200,000). Finding 3: ToolDeps without a HostPolicy default to the real machine's settings, and the e2e, integration and daemon test rigs never supplied one.

### Summary

C1.9 fix seat, branch closeout/hostperm (base cf31e01, implementer commits 2f04ae7..7f80138, fix-seat commits 9a1f9c4..a70e286). All four review findings were confirmed by independent reproduction and fixed test-first. The only open piece is finding 3's internal/daemon remainder, which is out of scope.

STATUS IS "partial" FOR TWO REASONS:
(1) This seat's harness refuses to write report .md files ("Subagents should return findings as text, not write report files"). A system rule outranks the computed task, so plans/sdd/V6-closeout/hostperm/report.md was NOT written or committed. The full report, including the "Review resolution" section, is this output. runs/INDEX.txt, a committed evidence manifest, maps every log to its command, tree and result.
(2) internal/daemon/mcpop_test.go still reads the machine's host policy (see open_issues).

REVIEW RESOLUTION (finding -> action)

Findings 1 (blocker) and 4 (major) -> FIXED. They are one defect. 6dacb7c made authorizeHost judge only the caller's or record's spelling, but re_read serves paths.Key(norm), and on Windows Norm's EvalSymlinks maps aliases to the real name.
- Probe: GetFullPathNameW, GetLongPathNameW and EvalSymlinks against a scratch tree. These open the file: 'x.', 'x ', 'x...', 'x. . ', 'dir.\x', 8.3 names, and 'x::$DATA'. These do not: 'dir..\x', 'dir \x'.
- Red, runs/70 (mcp): every re_read form (latest, ':1', 'turn:', timestamp) served config/secret.env under Read(./config/secret.env) for all five alias spellings. CONFIG~1/CREDEN~1.SEC was served both relative and absolute. A record captured under a short spelling was served by expand, recall and dropped. A project root spelled in 8.3 let both '//c/...' and './' rules be walked past.
- Red, runs/71 (hostperm): 4 of 4.
- Red, runs/85: the packaged binary at 7f80138 served the trailing-dot and trailing-space aliases under deny ("served the archived marker"; record outcome failed).
- Fix, mcp layer (e6c3428): authorizeHost(ctx, path, norm) judges the given spelling, which keeps 6dacb7c's link-spelling fix. It also judges filepath.Join(EvalSymlinks(root), norm) and takes the stricter answer (deny > ask). authorizePath and filterDrops pass both. With no rules in force it returns before any syscall.
- Fix, hostperm layer (9a1f9c4): Evaluate and the rule anchors also judge osAlias. That is the OS's own GetFullPathNameW normalization, then stream suffixes cut, then GetLongPathNameW over the longest existing prefix. The hostperm layer is needed on its own: with lnk->real and a rule on ./lnk/**, the spelling 'lnk.\key.pem' canonicalizes to real/key.pem, which matches nothing. It also covers a short-spelled root with './' rules. Every added spelling can only add refusals.
- Tests: internal/mcp/hostperm_spelling_test.go (all platforms; on POSIX it asserts aliases are never served), internal/mcp/hostperm_shortname_windows_test.go, internal/hostperm/alias_windows_test.go (also ::$DATA, :stream, a deleted file under a short directory, and non-alias controls), and Windows alias forms in the packaged security case.
- Green: runs/75 (32 of 32), runs/83 (record verified), runs/86, Linux runs/90 and runs/102.

Finding 2 (minor) -> FIXED, and worse than reported.
- Red: runs/72, TestAnUnboundedRuleCountFailsClosed.
- Measured before the fix (runs/73, Windows): 10,000 rules cost 61.7 ms per path and 200,000 cost 2.73 s (the reviewer measured 637 ms).
- Cap (6e57ca5, d4bd626): at most 5000 compiled patterns and 80,000 path segments, summed over all sources. Past either, the result is a cached parseError, so callers answer "host policy unavailable". The segment cap closes the same gap for a few very long rules. Boundary tests: TestTheRuleCapIsSummedAcrossSources and TestTheSegmentCapBoundsAFewLongRules.
- Why 5000: 4096 was first. devtool lint nomagic (D11) rejected it as a config-default literal, and 1024, 2048 and 10,000 are on the same list. So the cap was restated as 5000 rather than annotated or disguised.
- Matcher: the old DP rebuilt a full table per directory prefix per rule. The new prefixRow answers every prefix in one forward pass and calls path.Match only on reachable cells; carvedMatch takes one row per pattern. Two differential tests pin it to the old matcher, kept verbatim as a reference: 20,000 random pattern/path pairs and 5,000 random carve-out lists. A deliberate mutant is caught by both.
- Cost at 5000 (runs/97, Windows co-load): 0.87-1.65 ms per path for globstar rules, 0.07 ms for anchored rules.
- Rebuild: a per-build Readlink memo, plus resolution that stops at the first missing component, cut a rebuild at the cap from 10.4 s (runs/74) to 0.17-0.63 s. Evaluate still reads links fresh on every request.

Finding 3 (minor) -> FIXED for e2e and integration (3ea6a1f), OPEN for internal/daemon.
- Red: runs/79 and runs/80. A deny-all rule placed via CLAUDE_CONFIG_DIR turned the v4Server, x9v4 and x8v5 expansions into refusals. This machine's real settings hold no Read rules, which is why the suites pass here.
- Fix: v4Server supplies a hermetic policy (empty home, no env, no managed sources) when a row passes none, and both integration rigs pass one. Production composition roots keep the real environment.
- Daemon: runs/95 shows TestDaemonMCPOpDispatchesToolCall fails under the same machine rule. Fixing it needs an edit to internal/daemon, which the brief forbids; see open_issues.

ORIGINAL ROOT CAUSE (V6-HOST-1): retrieval enforced only project containment (Norm + ResolvesInside) and provenance. A file later placed under a permissions.deny or ask Read rule was re-served. Evidence: runs/01 at cf31e01 plus tests; expand by id and by root hash, and re_read, served a host-denied path.

IMPLEMENTER SEAT (recorded from its commits):
- 2f04ae7: internal/hostperm. It reads managed files and drop-ins, the HKLM/HKCU policy registry, the remote-settings cache, user settings (~/.claude or $CLAUDE_CONFIG_DIR), project and local settings, and a worktree's main checkout. It applies the gitignore dialect with //, ~/, /, ./ and bare anchors, carve-outs, symlink and junction targets, and POSIX/case-folding on Windows. It fails closed.
- 7df2c1d: every retrieval form re-checks, with distinct deny, ask and unavailable reasons. The same commit closed a V6-AUTH residual: re_read's at:sha256 form now checks the hash's origins.
- ed5a80c: packaged security case. 6dacb7c: link spelling. 9df73a4 and ee106e4: docs.

DOC SNAPSHOT: https://code.claude.com/docs/en/{permissions,settings,managed-settings,server-managed-settings,env-vars}.md, fetched 2026-09-22 by the implementer. The fix seat re-fetched them the same day and all five sha256 matched: ec2d99b6, 0c5162aa, 34887612, a87aff03, a6ffb000. Rule text relied on:
- "Rules are evaluated in order: deny, then ask, then allow."
- Pattern table: '//path' "Absolute path from filesystem root"; '~/path' "Path from home directory"; '/path' "Path relative to the settings source"; 'path' or './path' "Path relative to current directory".
- "On Windows, paths are normalized to POSIX form before matching. C:\Users\alice becomes /c/Users/alice".
- "Deny rules: apply when either the symlink path or its target matches".
- "The carve-out reaches only rules from the same source".
- "A deny or ask rule whose path isn't usable as a gitignore pattern still guards that exact path".
- Managed file locations: /Library/Application Support/ClaudeCode, /etc/claude-code, and C:\Program Files\ClaudeCode managed-settings.json.
- CLAUDE_CONFIG_DIR "Override the configuration directory (default: ~/.claude)", and CLAUDE_CODE_SUBPROCESS_ENV_SCRUB removes it from subprocess environments.

RECORDED DECISION THIS ALTERS: plans/sdd/V6-remediation/authority-review.md section 6 lists "Explicitly rejected — Option D. Do not attempt to reproduce host policy by parsing settings files". verification-protocol.md says "V6-HOST-1 cannot be cleared by reconstructing static settings." C1.9 implements that parsing on the ledger's owner default. This work therefore departs from those records and does not claim V6-HOST-1 as a pass of native-policy fidelity: it honours the saved rules, fails closed, and documents what a plugin cannot see.

DOCS: security.md section 1 now states every spelling that is judged and the 5000/80,000 bound. troubleshooting.md names the bound as a cause of "host policy unavailable". The hostperm package comment is updated. No MCP tool descriptions changed, so gen-mcp-docs was not re-run. A fix-seat code comment that pointed at report.md was repointed to the committed logs (0071cbb).

### Commits

- 9a1f9c4 fix(hostperm): judge every Windows spelling that opens a file
- 6e57ca5 fix(hostperm): bound what evaluating one path can cost
- 3ea6a1f test(e2e,integration): give the MCP rigs a hermetic host policy
- afa1475 test(security): judge Windows alias spellings of the packaged server
- e6c3428 fix(mcp): judge the served name as well as the caller's spelling
- 692343c docs(security): state every spelling host Read rules are judged on
- 0071cbb docs(hostperm): cite the committed cost evidence for the rule cap
- d4bd626 fix(hostperm): set the rule cap to a count no config default uses
- c6a18b4 test(hostperm,mcp): mark the 8.3 precondition skip as platform-gated
- a70e286 docs(v6-closeout): record the C1.9 review-resolution evidence

### Tests

- `go test ./internal/mcp -run 'TestHostPolicy_AnAliasSpellingOfADeniedPathIsRefused|TestHostPolicy_AShortNameSpellingIsRefused' -count=1 -v (Windows, 7f80138 plus new tests)` — RED as intended: all 30 alias and 8.3 subtests served the denied secret (runs/70)
- `same, fixed tree` — PASS 32/32, no skips (runs/75)
- `go test ./internal/hostperm -run '<4 Windows alias tests>|TestAnUnboundedRuleCountFailsClosed' -count=1 -v` — RED at 7f80138 (runs/71, runs/72); PASS after the fix <!-- runpatterns: the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern -->
- `go test ./internal/hostperm -run 'AgreesWithTheReference' (differential against the verbatim old matcher, plus a mutation check)` — PASS; the mutant is caught by both tests
- `go test ./internal/hostperm ./internal/mcp -count=1 -timeout=30m (Windows)` — hostperm PASS; mcp PASS except TestBudgetBF p95 327.68ms > 250ms (runs/86)
- `go test ./internal/mcp -run '^TestBudgetBF$' alone, head then a base 7f80138 archive (Windows)` — both breach under co-load: head p95 393ms at 42% CPU, base p95 786ms at 85% CPU; pre-existing co-load gate (runs/88)
- `go test ./internal/hostperm -count=1 at the aliases-only intermediate state 9a1f9c4` — PASS (runs/87)
- `QOMPACK_SECURITY_ARTIFACTS=<dir> go test ./test/security -run 'TestV6_' -count=1 -v (Windows)` — host case PASS with alias forms, record verified (runs/81, runs/83); the hash case failed with 'daemon unavailable' under co-load and PASSED alone (runs/82)
- `the new packaged security case on a git archive of 7f80138 (Windows)` — RED as intended: trailing-dot and trailing-space aliases served the archived marker, record failed (runs/85)
- `go test ./test/e2e -run TestV4Server_TheMachinesHostRulesDoNotReachARow; go test ./test/integration -run TestRigs_TheMachinesHostRulesDoNotReachThem` — RED before the rig fix (runs/79, runs/80); PASS after
- `go test ./test/e2e -run '^TestV4' -count=1 -v -timeout=30m (Windows, c6a18b4)` — 17 PASS, 1 FAIL TestV4_TombstoneToRecallToExpandRoundTrip, a write-time append-only violation that is pre-existing and identical on cf31e01 (runs/103, runs/101)
- `go test ./test/integration -count=1 -timeout=30m (Windows)` — 5 failures, none on the MCP path; SpooledEvents and HotPathDegrades PASS alone (co-load); GCNeverCollects, DegradedPassive and HotPathWarm fail again and fail on cf31e01 (runs/93, runs/98, runs/100)
- `go test ./test/e2e -count=1 -timeout=30m (Windows)` — killed at 30m inside a V3 hot-path row under co-load, before the V4 rows; the failures are in hook, install, observer and crash-recovery rows, which run none of this branch's code while no Read rule is in force (runs/94)
- `Linux uid 10001, inner.sh --coload (race) -- ./internal/hostperm ./internal/mcp at 692343c` — PASS hostperm 111/0/0, mcp 387/0/0 including TestBudgetBF, no race (runs/90)
- `Linux inner.sh --coload --no-race -- ./test/security ./test/integration at 692343c` — security 22 pass, 2 fail, both pre-existing on cf31e01 per runs/52; integration 67 pass, 3 fail, all three fail on cf31e01 (runs/91, runs/100)
- `Linux inner.sh --no-race --run '^TestV4' -- ./test/e2e at 692343c` — 45 pass, 2 fail: tombstone is pre-existing (runs/101); x13 hot-path touch set PASSES alone at d4bd626 (co-load, runs/102)
- `Linux inner.sh race -- ./internal/hostperm at final d4bd626` — PASS 111/0/0, no race (runs/102)
- `go test ./internal/daemon -run '^TestDaemonMCPOpDispatchesToolCall$' with and without CLAUDE_CONFIG_DIR pointing at a deny-all rule` — control PASS; with the machine rule FAIL. Shows finding 3's out-of-scope remainder (runs/95)
- `go test ./test/docs -count=1` — PASS (runs/89)
- `go run ./tools/devtool fmt-check; GOOS={windows,linux,darwin} go vet on hostperm, mcp, test/e2e, test/integration, test/security` — clean (runs/99)
- `go run ./tools/devtool lint (c6a18b4, nothing else of this seat running)` — golangci-lint, nomagic, importgraph, testdeps, sleepcheck, runpatterns, docmarkers and coveragefloors PASS; bindeps FAIL pre-existing (x/sys/unix via internal/paths, runs/64); stubskips has 4 problems, the same four daemon/hookio/store skips as the implementer's runs/61, none in a touched file (runs/104)

### Criterion changes

- None. No assertion, threshold, budget, golden, timeout or skip was loosened. Two existing tests changed call shape only: TestMatchSegmentsStaysPolynomialOnHostilePatterns passes a *scratch, and TestHostPolicy_DefaultPolicyIsBuiltFromTheProjectRoot passes the normalized spelling to authorizeHost. Both assert exactly what they did before.
- New tests carry a precondition skip, 'platform: this volume records no 8.3 names', because the spelling under test cannot exist without 8.3 names. It did not fire on this machine. The Windows alias forms in the packaged security case are added only on Windows, since on POSIX each names a different file.
- The cap constant was changed from 4096 to 5000 within this seat. It is new code, not an existing criterion; 4096 collided with a nomagic D11 config-default literal and was restated rather than annotated or disguised.

### Open issues

- plans/sdd/V6-closeout/hostperm/report.md was not written: this seat's harness refuses report .md files. The full report and the Review resolution are this output; runs/INDEX.txt, the committed evidence manifest, says so. The coordinator should commit a report from this output if the ledger requires the file.
- Finding 3, daemon remainder (out of scope: 'Do not touch internal/daemon'): internal/daemon/mcpop_test.go builds mcp.ToolDeps at lines 138 and 246 without a HostPolicy, so the tests read the machine's real host policy. runs/95 shows TestDaemonMCPOpDispatchesToolCall failing under a machine deny-all rule. Fix: pass HostPolicy: hostperm.New(hostperm.Options{ProjectRoot: ..., Home: t.TempDir(), Getenv: func(string) string { return "" }, Managed: &hostperm.ManagedSources{}}). daemon is a composition root, so the import is allowed.
- Binary-level suites (test/security, and the e2e rows that spawn qompack) still read the real managed directory and Windows policy registry by design; they exercise the production composition. HOME is isolated. Redirecting the managed sources would need a product override, which would weaken the check.
- Residual Windows spelling gaps: an 8.3 name is expanded only where the entry exists, so a deleted file's own short name stays short (its parents are expanded). This is unreachable through capture (Key(Norm) stores long names) and through re_read (no history key matches). A \\?\-prefixed spelling skips Win32 normalization, but the served canonical name is still judged.
- macOS APFS normalization-insensitivity (NFC vs NFD) is not modelled by hostperm and is untested; no macOS host was available.
- The Windows per-path cost of a non-empty rule set now includes GetFullPathNameW and GetLongPathNameW per spelling. The paired base-vs-fix Evaluate benchmark could not rank the two under this machine's co-load (runs/78). With no rules in force the added cost is zero.
- Pre-existing failures seen and classified (not caused here): TestBudgetBF (co-load, fails worse on base); Linux and Windows TestIntegration_GCNeverCollectsALiveRootUnderIngest, DegradedPassiveStillWritesToTheRealStore and HotPathWarmWithRealResidentState (all fail on cf31e01); TestV4_TombstoneToRecallToExpandRoundTrip (identical append-only violation on cf31e01); Linux TestSecurity_ReReadAnswersFromTheArchiveNotTheLiveDisk and ArchivedTextIsDataNeverAnInstruction (runs/52); lint bindeps and 4 stubskips skips. The Windows whole-package e2e run timed out at 30m under co-load; its failures were not classified individually.

### Needs the owner

- V6-HOST-1 gate status. plans/sdd/V6-remediation/authority-review.md section 6 explicitly rejects Option D (parsing settings files), and verification-protocol.md says V6-HOST-1 cannot be cleared by reconstructing static settings. The C1.9 owner default implemented exactly that. Rule either that the default supersedes those records for C1.9 (V6-HOST-1 closes as 'saved rules honoured, residual documented'), or that V6-HOST-1 stays an open boundary with this as mitigation.
- Rule-cap values: 5000 compiled Read patterns and 80,000 segments summed over all sources; past either, all path-bearing retrieval fails closed. They were chosen from measured cost (about 1 ms per path at the cap) and to avoid nomagic's config-default literals (1024, 2048, 4096, 10000 are forbidden). Confirm the cap, or raise it if an enterprise managed list could exceed it.
- Route the internal/daemon/mcpop_test.go hermetic-HostPolicy fix (finding 3 remainder) to the daemon owner.

