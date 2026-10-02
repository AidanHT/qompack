# W16F-HPDEPTH: hostperm walks the whole server-managed document

Branch `closeout/w16f-hpdepth`. Workflow `wf_fc5fef3b-1b3`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `256f8f9d`

### Root cause

collectPermissions in internal/hostperm/sources.go walked the server-managed cache (~/.claude/remote-settings.json) recursively and had a hard depth cutoff: `if depth > maxNesting { return }` with maxNesting = 8. When a `permissions` object sat more than 8 objects or arrays deep, the walk returned quietly with no error, so the block's Read deny and ask rules were never compiled or enforced. That is a privacy fail-open, and it contradicts the doc comment "wherever its rules sit, they apply". Red evidence against base 8fe6c1f4 is in runs/red-base.log: a deny on a.txt nested 9 deep, 100 deep, and 100 deep alternating objects and arrays came back Allow (expected 2, actual 0), with no error. The walk also re-unmarshalled each subtree at every level, so its cost grew with the square of the depth. That cost was a second reason to bound it, and decoding once removes it.

### Summary

The fail-open is fixed as D56(d) orders: the walk no longer stops at any depth, and the remaining bound fails closed.

WHAT CHANGED (internal/hostperm/sources.go only, plus a new internal/hostperm/depth_test.go)
- collectPermissions(data) ([]json.RawMessage, error) decodes the cache once, using json.Decoder with UseNumber into `any`, and walks the whole tree in memory.
  - Keys are still visited in sorted order. A `permissions` key whose value is an object is collected and not descended into. Anything else is walked, as before.
  - Each collected block is re-encoded with json.Marshal and passed to the unchanged parseSettings/parseList path, so the checks on rule shape and type are the same as before.
- Bounds:
  - Size is still bounded by maxSettingsBytes (8 MiB) in the source read.
  - Depth is bounded by encoding/json's own nesting limit (10000). A document past it already fails parseSettings' top-level Unmarshal and becomes sourceError "not a JSON object", which fails closed. A new row pins this.
  - Any error in the walk (decode, or the re-encode) is returned as sourceError(s.id, ...), which also fails closed. The walk never truncates silently.
- UseNumber keeps numbers exactly as written. Without it, a valid number that float64 cannot hold (1e400) would make decoding into `any` fail. The fix would then refuse a document that the old code and the host both accept. The rule from D56 "no refusing everything for a harmless document" covers this, and a row pins it.
- maxNesting is removed; nothing else referenced it (checked with grep).
- docs/security.md needs no change. It names "the cached server-managed settings" as a source and says an unparseable settings file makes answers `unavailable`, and both are still accurate. It never described the depth bound.

ROWS (internal/hostperm/depth_test.go), all red first
- TestServerManagedRulesApplyAtAnyDepth, with subtests nine_objects_deep, a_hundred_objects_deep and a_hundred_objects_and_arrays_deep: the Read deny (a.txt) and ask (b.txt) rules are enforced, and c.txt is Allow.
  - Red on base: Deny expected, Allow actual, no error. Evidence: runs/red-base.log.
- TestADeepServerManagedDocumentWithoutPermissionsStaysEnforced: a cache 500 levels deep with no rules plus a user deny rule. The deny is enforced, other paths are allowed, and nothing errors.
  - It passes on base. It was shown red with a temporary uncommitted edit that failed closed on any depth over 6 ("TEMP red check: too deep"), which is the over-correction this row guards against. Evidence: runs/red-tempedit.log.
- TestAServerManagedHooksBlockAtDepthSevenParses: a realistic hooks block whose deepest container is at depth 7, next to a permissions block. The deny is enforced.
  - Shown red with the same temporary edit (runs/red-tempedit.log).
- TestAServerManagedDocumentPastTheJSONNestingLimitFailsClosed: a document nested past encoding/json's 10000 limit gives ErrUnavailable, naming remote-settings.json.
  - Shown red with a temporary edit that tolerated an undecodable nested cache, where it returned no error (runs/red-tempedit.log).
- TestAServerManagedNumberFloat64CannotHoldIsNotRefused: 1e400 inside and outside the permissions block, with the deny still enforced.
  - Shown red by temporarily commenting out dec.UseNumber() ("not a JSON object"). Evidence: runs/red-usenumber-tempedit.log.
- Every temporary edit was reverted before committing. git diff showed only the intended change, and grep finds no "TEMP" left in sources.go.

COMMANDS AND RESULTS (Windows, daytime limits: -p 2, no -race, no load generators; Docker not touched)
- Focused on base: `go test -p 2 -count=1 ./internal/hostperm -run '^(TestServerManagedRulesApplyAtAnyDepth|TestADeepServerManagedDocumentWithoutPermissionsStaysEnforced|TestAServerManagedHooksBlockAtDepthSevenParses|TestAServerManagedDocumentPastTheJSONNestingLimitFailsClosed)$' -v` gave exit 1. The three depth subtests failed and the other three passed, as expected. <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- Focused after the fix: the same pattern plus TestServerManagedRulesInsideAnArrayApply and TestServerManagedCacheIsReadWhereverItsRulesSit gave exit 0, all PASS (runs/green-focused.log).
- `go test -p 2 -count=1 -run '^TestAServerManagedNumberFloat64CannotHoldIsNotRefused$' ./internal/hostperm -v` failed under the temporary edit and passed after the fix.
- Full package: `go test -p 2 -count=1 -coverprofile=... ./internal/hostperm` gave exit 0, ok, coverage 94.4% on Windows (runs/full-hostperm.log, runs/final-hostperm-windows.coverprofile).
- `go vet ./internal/hostperm` and `GOOS=linux go vet ./internal/hostperm` were both clean.
- `go run ./tools/devtool fmt-check` exited 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`:
  - nomagic, importgraph, testdeps, bindeps, sleepcheck and docmarkers PASS.
  - golangci-lint first failed with "parallel golangci-lint is running" (another seat's run). Re-run alone, it gave PASS (runs/golangci.log).
  - runpatterns FAIL with 5 findings, all in already-committed reports: w16d-warnlate/report.md lines 164, 169, 192 and 198, and w16e-settlewin/report.md line 92. None of the findings comes from this branch, and a re-run after my commits showed the same 5.
- No wall-clock assertions are involved.

COVERAGE IMPACT (CI floor for hostperm is 90% on Linux; w16e left 593 of 651 statements covered)
- sources.go goes from 190 to 200 statements. Covered statements go from 184 on the true base (185 counting the base run in which the new red rows reached the old cutoff) to 192.
- Applied to the Linux numbers, that is about 601 of 661, or 90.9%, against 595 needed. The margin drops from 7 to about 6 statements.
- Three new statements are unreachable defensive branches, left uncovered: Decode failing after the top-level Unmarshal succeeded, json.Marshal failing on a decoded map, and parseSettings' return of that error. These coverage figures are estimated from the Windows profile delta and were not measured on Linux, because the container is off-limits by day.

CRITERION CHANGES: none. No assertion, threshold or golden was touched. No new budget number was added: 10000 is encoding/json's own limit, and the test only mirrors it as a named constant with a comment.

### Commits

- 1d7805a5 fix(hostperm): walk the whole server-managed cache for rules
- 256f8f9d test(hostperm): record w16f-hpdepth red, green and lint logs

### Tests

- `go test -p 2 -count=1 ./internal/hostperm -run '^(TestServerManagedRulesApplyAtAnyDepth|TestADeepServerManagedDocumentWithoutPermissionsStaysEnforced|TestAServerManagedHooksBlockAtDepthSevenParses|TestAServerManagedDocumentPastTheJSONNestingLimitFailsClosed)$' -v (base 8fe6c1f4 sources.go)` — exit 1 (red as expected): all 3 TestServerManagedRulesApplyAtAnyDepth subtests got Allow (0) for the deny rule; the other 3 rows passed and were shown red by temporary uncommitted edits (runs/red-tempedit.log) <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 2 -count=1 ./internal/hostperm -run '^TestAServerManagedNumberFloat64CannotHoldIsNotRefused$' -v (temporary edit: UseNumber commented out)` — exit 1 (red as expected), 'not a JSON object'; PASS after the edit was reverted
- `go test -p 2 -count=1 ./internal/hostperm -run '^(TestServerManagedRulesApplyAtAnyDepth|TestADeepServerManagedDocumentWithoutPermissionsStaysEnforced|TestAServerManagedHooksBlockAtDepthSevenParses|TestAServerManagedDocumentPastTheJSONNestingLimitFailsClosed|TestServerManagedRulesInsideAnArrayApply|TestServerManagedCacheIsReadWhereverItsRulesSit)$' -v` — exit 0, all PASS <!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the command ran as quoted and its result is recorded on this line -->
- `go test -p 2 -count=1 -coverprofile=final.cover ./internal/hostperm` — exit 0, ok, coverage 94.4% (Windows)
- `go vet ./internal/hostperm && GOOS=linux go vet ./internal/hostperm` — clean
- `go run ./tools/devtool fmt-check` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — nomagic, importgraph, testdeps, bindeps, sleepcheck and docmarkers PASS; golangci-lint first hit 'parallel golangci-lint is running' (another seat), then PASS alone; runpatterns FAIL with 5 findings, all in reports already on the base (w16d-warnlate lines 164/169/192/198, w16e-settlewin line 92), none from this branch

### Open issues

- runpatterns fails on the base: 5 unsatisfiable -run patterns in plans/sdd/V6-closeout/w16d-warnlate/report.md (lines 164, 169, 192, 198) and w16e-settlewin/report.md (line 92). This branch did not cause them, and the ledger mentions a runpatterns waiver (877ed3f7) that apparently does not cover them all. The coordinator should check before RC6.
- The hostperm coverage margin on Linux is estimated, not measured: about 601 of 661 covered (90.9%) against 595 needed, so the margin falls from 7 to about 6 statements. Three new unreachable defensive statements are uncovered: the Decode error, the json.Marshal error, and parseSettings' return of that error. Confirm with the Linux cover gate in the next container or CI run.
- No Linux or -race run was made for these rows, because daytime limits put the container off-limits. The code is platform-neutral pure JSON handling. GOOS=linux vet is clean.

## Independent review

### review:hpdepth: sound


## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.

