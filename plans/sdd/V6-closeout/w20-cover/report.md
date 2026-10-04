# Wave 20 cover (candidate 8 pre-freeze audit fixes, D61(a))

Branch `closeout/w20-cover`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **major** `release-check's `ci-local cover` step (tools/devtool/cover.go:180-215) as run tonight on Windows by overnight-c8.sh:77`: The Windows coverage floors have not been evaluated since 2026-09-28 (phase3/p3-cover.json on a94a3fb, exit 1). CI's cover job is ubuntu-only (ci.yml:358), and w16b-cover raised coverage with linux-tagged tests that do not run on Windows. Today's Windows margins are razor-thin, and candidate 8 touches internal/checkpoint. release-check stops at the first FAIL, so a single shortfall about 2 h in leaves determinism, rollback, plugin-validate and marketplace without evidence. A shared pass under co-load that skips or alters a few branches could move a 0.2 pp margin.

## impl:cover: status `done`, head `7403e6ab`

### Root cause

The Windows floors were never measured because CI's cover job is ubuntu-only and no pre-freeze step applies floors on Windows. internal/store's Windows margin was thin (12 statements) because its link-guard rows from w16b-cover create symlinks, which need a privilege on Windows, so they were tagged unix-only. The same guards' Windows branches, where a junction appears as os.ModeIrregular, therefore had no test.

### Summary

Test-only change; no product code changed. The audit finding holds as an evidence gap but not as a predicted failure. On 738d67c7 every Windows coverage floor passes. Only internal/store was within 0.3 pp of its floor, and four new Windows-only tests have widened its margin to 0.415 pp.

WHAT RELEASE-CHECK'S COVER STEP DOES ON WINDOWS. releaseCheckSteps (tools/devtool/releasechecksteps.go) reuses ciLocalSteps, so `ci-local cover` runs taskCover (tools/devtool/cover.go). That step makes two passes, both at -covermode=atomic and -timeout=30m:
- Shared pass: every package except test/e2e, with QOMPACK_UNDER_COLOAD=1 set and QOMPACK_NONREFERENCE_DISK removed.
- Isolated pass: test/e2e alone, with the co-load declaration removed.
It then applies plans/OWNERS.tsv floors to every package whose subplan is listed in landedSubplans. The floors are identical on every OS; cover.go has no OS switch. Each package's coverage comes only from its own test binary (there is no -coverpkg), so the floors depend on the shared pass's internal/* and cmd/* binaries alone. overnight-c8.sh calls `release-check --tag v0.3.0` with no GOFLAGS, so tonight's run uses the default -p (22 on this host).

MEASUREMENT. One heavy run on 738d67c7:
`QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -covermode=atomic -coverprofile=... ./internal/... ./cmd/...`
- QOMPACK_NONREFERENCE_DISK unset; ran 15:34:47 to 16:05:26; exit 0.
- test/e2e, test/integration and tools/* were left out because of the machine limits. They contribute nothing to any floor.
- The floors were applied with a script that copies parseCoverProfile and the floor loop.

Per package (covered/total, margin over floor):
- store 90.206% (5609/6218), +0.206 pp, 12 statements of slack
- paths 90.582% (654/722), +0.582 pp, 4
- checkpoint 90.603% (2208/2437), +0.603 pp, 14
- negknow 91.472% (1491/1630), +1.472
- config 91.802% (907/988), +1.802
- redact 92.369% (230/249), +2.369
- mcp 88.081% (floor 85), +3.081
- analyzer 88.165% (85), +3.165
- eval 88.337% (85), +3.337
- tokens 93.360%, +3.360
- hostperm 94.430%, +4.430
- pins 95.385%, +5.385
- dag 90.754% (85), +5.754
- sketch 96.475%, +6.475
- testutil 82.051% (75), +7.051
- skills 82.645% (75), +7.645
- canon 98.151%, +8.151
- cli 83.975% (75), +8.975
- core 84.884% (75), +9.884
- chunk 100%, admission 100%
- ipc 85.384%, rehydrate 95.585% (85), rules 86.607%, daemon 86.683% (75), scheduler 97.077% (85), state 89.520%, hookio 90.000%, pluginmanifest 90.426%, commands 91.318%, contract 91.486% (75), grammar 91.557%, observer 91.619%, logging 91.946%, symbols 93.727%, obs 95.804%. Each of these is at least +10 pp over its floor.
- cmd/qompack 0/3 is exempt as a composition root (main.go declares only func main).

These match the auditor's run at -p 1 and both skeptics' runs to within 0.1 pp.

WITHIN 0.3 PP: STORE. Is the floor meant to apply on Windows?
- 00-ARCHITECTURE.md §6.4 says coverage "is measured on the merged profile from the Linux job". Ledger item C3.6 says "store/paths >= 90 % on Linux". ci.yml's cover job runs on ubuntu.
- Windows enforcement comes only from release-check reusing ciLocalSteps, and docs/release.md §1 step 3 (D57(a)) requires that run on the Windows reference host. So the floors are applied to a profile they were not defined on.
- Shared files carry branches that only one OS can ever cover. Examples: paths/protected_names.go's !DefaultFold and non-Windows streamless branches, leaf_unix.go against leaf_windows.go, and store's object_open_unix.go against object_open_windows.go.
- Even so, an OS-specific floor is not justified: every floor passes on Windows, and lowering one would weaken the gate.

The remedy is real Windows tests. The store gap on Windows is concentrated in the w16b-cover rows symlink_edges_unix_test.go and observation_guards_unix_test.go, which run only on unix because creating a symlink needs a privilege. An NTFS junction needs none, and the guards see it as os.ModeIrregular, not ModeSymlink. Commit 7403e6ab adds internal/store/junction_edges_windows_test.go with four rows. Each plants a junction:
- where the GC reads a retention source (checkpoints/, the generation store, delivery-segments/): the pass halts and nothing is swept;
- in the pending registry, at an object leaf and at a capture sidecar: the publication audit notes each and counts nothing behind it;
- at index/: the observation reader marks the sidecar uncertain;
- at a restore's destination .qompack: ErrRestoreTargetExists, worded "is a special file or reparse alias", and nothing is published through it.

A mutation check confirms the tests have teeth: weakening isSymlinkish to ModeSymlink alone turns the audit row red. With maintNoFollow weakened, the GC rows stay green, because the later Lstat/SameFile checks still halt the pass (defence in depth).

After the change, a full store run (`QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -covermode=atomic ./internal/store/`, 777 s) gives 90.415% (5622/6218), +0.415 pp, 25.8 statements of slack. The 13 new statements are exactly the blocks the new rows reach. Comparing this run with the -p 2 run block by block, no other store block changed in either direction, so store coverage at -p 1 and -p 2 is deterministic.

paths (+0.58 pp) and checkpoint (+0.60 pp) are above the 0.3 pp threshold, so neither needed a change. checkpoint carries a live risk, listed under open issues.

### Commits

- 7403e6ab test(store): pin the link guards against junctions on windows

### Findings resolution

- **fixed**: night/major: Windows coverage floors unevaluated since 2026-09-28; release-check's cover step could fail tonight on razor-thin margins (cover.go:180-215, overnight-c8.sh:77)
  - Evidence gap closed: release-check's shared cover pass (co-load declared, NONREFERENCE_DISK removed, atomic) was run on Windows over ./internal/... ./cmd/... on 738d67c7 at -p 2 and exited 0. All 37 floor-bound packages pass. store was the only package within 0.3 pp (90.206%, 12 statements of slack); paths +0.582 pp and checkpoint +0.603 pp. The floors as defined in §6.4 and C3.6 are measured on Linux, so applying them on Windows comes only from release-check reusing ciLocalSteps; an OS-specific floor is not justified because everything passes. Remedy per the brief: real Windows tests. Commit 7403e6ab adds four junction rows that pin the store's link guards on Windows; store's Windows coverage rises to 90.415% (5622/6218). A block-by-block comparison of the two store runs shows no other block changing, so the result is stable at -p 1 and -p 2. Not proven: behaviour under tonight's default -p 22, and the test/integration and test/e2e passes, which this run left out.

### Tests

- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -covermode=atomic -coverprofile=<scratch>/coverage.out ./internal/... ./cmd/...   (738d67c7, NONREFERENCE_DISK unset)`: exit 0, 30m39s; all floors pass; store 90.206% (5609/6218), paths 90.582% (654/722), checkpoint 90.603% (2208/2437), negknow 91.472%, config 91.802%; cmd/qompack exempt as a composition root
- `QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -timeout=30m -covermode=atomic -coverprofile=<scratch>/store-after.out ./internal/store/   (on 7403e6ab content)`: ok, 777.463s, 90.415% (5622/6218); only the 13 new statements differ from the baseline profile
- `go test -p 1 -count=20 -run '^(TestGC_HaltsOnAJunctionWhereARetentionSourceBelongs|TestAuditPublication_NotesJunctionsItWillNotTraverse|TestObservationGuards_JunctionedIndexRefused|TestMaintenanceRestore_RefusesADestinationDotThatIsAJunction)$' ./internal/store/`: ok 54.441s
- `go test -p 1 -race -count=3 -run '^(TestGC_HaltsOnAJunctionWhereARetentionSourceBelongs|TestAuditPublication_NotesJunctionsItWillNotTraverse|TestObservationGuards_JunctionedIndexRefused|TestMaintenanceRestore_RefusesADestinationDotThatIsAJunction)$' ./internal/store/`: ok 47.930s
- `mutation check: isSymlinkish limited to os.ModeSymlink and maintNoFollow limited to os.ModeSymlink, then the four rows (reverted afterwards with git checkout)`: TestAuditPublication_NotesJunctionsItWillNotTraverse FAIL as expected; the GC and restore rows stay green through later guards (defence in depth)
- `GOOS={windows,linux,darwin} go vet ./internal/store/`: ok on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/store/...`: exit 0, no issues
- `go run ./tools/devtool fmt-check`: pass
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: exit 0, PASS

### Open issues

- internal/checkpoint has 14.7 statements of slack on Windows (90.603%), and the w20-forkwork seat has uncommitted changes to intent.go, source.go and writer.go (+193/-36 including tests). Its final check should include a Windows `go test -covermode=atomic -cover ./internal/checkpoint/` at >= 90.0% before the merge. I could not measure it without touching that worktree.
- internal/paths has 4.2 statements of slack on Windows (90.582%). Its uncovered Windows statements are mostly OS-exclusive branches (protected_names !DefaultFold and the non-Windows streamless branch, which only Linux covers) and I/O error branches. No seat touches paths, and the package is deterministic, so no change was made.
- Not covered by this measurement: tonight's release-check runs the shared pass at the default -p (22) and includes test/integration, test/*, tools/* and the isolated test/e2e pass. A failure in any of those also fails the cover step regardless of floors; p3-cover on a94a3fb failed that way, on TestIntegration_HotPathWarmWithRealResidentState. The machine limits kept those packages out of my run.
- The prefreeze `internal` step still applies no floors. Swapping it for `QOMPACK_UNDER_COLOAD=1 go run ./tools/devtool cover` on Windows, as the auditor suggested, would make every candidate's Windows floors visible before the night. That is the coordinator's script to change.

### Needs owner

- Definitional mismatch, no action needed tonight: §6.4 and C3.6 define the floors on the Linux merged profile, but release-check on the Windows reference host (docs/release.md §1 step 3, D57(a)) enforces the same OWNERS.tsv floors on the Windows profile, because releaseCheckSteps reuses ciLocalSteps. Decide whether §6.4 should state that the reference-host release-check also binds the floors on Windows, which is the current behaviour and the stricter one, or whether release-check's cover should follow the Linux definition. I recommend recording the current, stricter behaviour in §6.4 and not adding an OS-specific floor: every floor passes on Windows, and the store gap was closed with real tests rather than a smaller number. No new constants were introduced.

## review:cover:0:r1: verdict `needs-fixes`, 2 finding(s)

- **minor** `internal/store/junction_edges_windows_test.go:128 (TestObservationGuards_JunctionedIndexRefused)`: The row can't tell refusal from traversal, so it passes even when the junction guard is removed. The sentinel written behind the junction is the unterminated, non-JSON line "external sentinel". If loadObservationsLocked did follow the junction, its scanner would take the atEOF error path and set obsSidecarUncertain=true anyway (observation_publication.go:270-276 and 296-298). The assertion `require.True(t, uncertain)` therefore holds whether openObservationIndex refuses the junction or reads straight through it. The unix row it mirrors has the same weakness.
  - Evidence: Probe via -overlay. I mutated observation_guards.go:29-40 so that it skips the !IsDir/ModeSymlink check, opens the index with os.OpenRoot(dot+"\\index"), which follows the junction, and drops the SameFile check. With the original test the row still PASSES (`--- PASS: TestObservationGuards_JunctionedIndexRefused (1.18s)`). I then changed only the sentinel to `[]byte(nil)`: the real guard PASSES (ok 0.384s) and the mutated guard FAILS at junction_edges_windows_test.go:135 ("Should be true").
  - Fix: Write an empty observations file behind the junction (`os.WriteFile(..., nil, 0o600)`), or a valid canonical intent line, so that reading through the junction leaves uncertain=false. Optionally make the same one-line fix in observation_guards_unix_test.go's aliased-index case.
- **minor** `internal/store/junction_edges_windows_test.go:59-61 (TestGC_HaltsOnAJunctionWhereARetentionSourceBelongs, "generation store" case) and its doc comment at :53-55`: The generation-store case passes for a reason that has nothing to do with the junction. GC never reads delivery-generations/: dsegGensDir appears only in dsegMigrationEvidence (gc_delivery_segments.go:443-466), where the directory merely being present, with no authority, counts as head loss and halts the pass. A junction to an empty directory halts the pass whether or not maintNoFollow refuses it. So the case does not pin the guard, and the comment's claim that the generation store is "read only as itself" overstates what the code does. The checkpoints and delivery-segments cases do discriminate.
  - Evidence: Overlay probe 1: junctionInPlaceOf was replaced with a plain os.MkdirAll, which is what traversal would see. The checkpoints and delivery-segments cases FAIL as they should, but the generation-store case still PASSES. Overlay probe 2: installSegAuthority was added to the generation-store case. The junction then does NOT halt the pass (that case FAILS), which confirms the halt comes only from the no-authority migration-evidence rule.
  - Fix: Either drop the generation-store case, or make it discriminate. To discriminate, call tp.Store.dsegMigrationEvidence() directly and require an error whose text contains "reparse point" (from maintNoFollow) for the junction, plus a control showing that a plain empty directory returns (true, nil). Reword the comment to say the generation store's presence is migration evidence, not something that is read.

## fix:cover:r1: status `done`, head `7adc502f`

### Root cause

No pre-freeze step applies the coverage floors on Windows, and CI's cover job runs only on ubuntu, so the Windows floors had not been measured since 2026-09-28. internal/store's Windows margin was thin (12 statements) because its w16b-cover link-guard rows create symlinks. Creating a symlink needs a privilege on Windows, so those rows run only on unix, and the guards' Windows branches, where a junction appears as os.ModeIrregular, had no test. In round 1, two of the new rows passed whether or not the guard ran. One used a sentinel that a traversing reader also flags as uncertain. The other targeted a directory the GC only checks for presence.

### Summary

Test-only change; no product code changed. On 738d67c7 every Windows coverage floor passes, so the audit finding is an evidence gap, not a predicted failure. internal/store was the only package within 0.3 pp of its floor. New Windows-only tests have widened its margin to 0.415 pp (5622/6218, 25.8 statements of slack).

WHAT RELEASE-CHECK'S COVER STEP DOES ON WINDOWS. releaseCheckSteps (tools/devtool/releasechecksteps.go) reuses ciLocalSteps, so `ci-local cover` runs taskCover (tools/devtool/cover.go). It makes two passes, both -covermode=atomic -timeout=30m:
- Shared pass: every package except test/e2e, with QOMPACK_UNDER_COLOAD=1 set and QOMPACK_NONREFERENCE_DISK removed.
- Isolated pass: test/e2e alone, without the co-load declaration.
It then applies the plans/OWNERS.tsv floors to every package whose subplan is listed in landedSubplans. The floors are the same on every OS; cover.go has no OS switch. There is no -coverpkg, so each package's number comes only from its own test binary, which means the floors depend only on the shared pass's internal/* and cmd/* binaries. overnight-c8.sh calls `release-check --tag v0.3.0` with no GOFLAGS, so tonight's run uses the default -p (22 on this host).

MEASUREMENT. One heavy run on 738d67c7:
`QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -covermode=atomic ./internal/... ./cmd/...`
- QOMPACK_NONREFERENCE_DISK unset; exit 0, 30m39s.
- test/e2e, test/integration and tools/* were left out because of the machine limits; none of them contributes to a floor.
- Floors were applied with a copy of parseCoverProfile and the floor loop.

Per package (coverage, margin over floor):
- Within 1 pp: store 90.206% (5609/6218, +0.206 pp, 12 statements of slack); paths 90.582% (654/722, +0.582, 4); checkpoint 90.603% (2208/2437, +0.603, 14).
- Within 3.5 pp: negknow 91.472% (+1.472); config 91.802% (+1.802); redact 92.369% (+2.369); mcp 88.081% (floor 85, +3.081); analyzer 88.165% (85, +3.165); eval 88.337% (85, +3.337); tokens 93.360% (+3.360).
- Within 10 pp: hostperm 94.430% (+4.430); pins 95.385% (+5.385); dag 90.754% (85, +5.754); sketch 96.475% (+6.475); testutil 82.051% (75, +7.051); skills 82.645% (75, +7.645); canon 98.151% (+8.151); cli 83.975% (75, +8.975); core 84.884% (75, +9.884).
- Every other package is at least +10 pp over its floor; chunk and admission are at 100%.
- cmd/qompack (0/3) is exempt as a composition root.
These numbers match the auditor's -p 1 run and both skeptics' runs to within 0.1 pp.

IS THE STORE FLOOR MEANT TO APPLY ON WINDOWS?
- 00-ARCHITECTURE.md §6.4 and ledger item C3.6 define the floors on the Linux profile, and ci.yml's cover job runs on ubuntu. Windows enforcement comes only from release-check reusing ciLocalSteps, which docs/release.md §1 step 3 (D57(a)) requires on the Windows reference host.
- An OS-specific floor is still not justified: every floor passes on Windows, and lowering one would weaken the gate.
- Remedy: real Windows tests. On Windows the store gap sits in link-guard rows that create symlinks, which need a privilege there, so they run only on unix. An NTFS junction needs no privilege, and the guards see it as os.ModeIrregular. Commit 7403e6ab adds internal/store/junction_edges_windows_test.go.

REVIEW RESOLUTION (round 1). Both reviewer findings held. I reproduced each with an overlay mutation and fixed it in commit 7adc502f.

1. TestObservationGuards_JunctionedIndexRefused could not tell refusal from traversal.
- Reproduced: with openObservationIndex mutated to open dot\index straight through the junction (no Lstat or SameFile check), the original row still passed (`--- PASS (0.09s)`).
- Cause: the file behind the junction was the unterminated, non-JSON line "external sentinel", which makes a reader that follows the junction mark the sidecar uncertain anyway.
- Fix: the file behind the junction is now empty. The same mutation turns the row red (junction_edges_windows_test.go:154, "Should be true") and the real guard passes.
- I made the same one-line change to the unix aliased-index case in observation_guards_unix_test.go. That change is verified only by GOOS=linux vet and test compile, because no local Linux runtime exists.

2. The GC row's generation-store case did not test the guard.
- GC never reads delivery-generations/. dsegMigrationEvidence treats its mere presence as head loss and halts the pass, junction or not.
- Fix: that case is removed from the GC table and its doc comment is reworded. It is replaced by TestDsegMigrationEvidence_NamesAJunctionedGenStore:
  - control: a plain directory returns (true, nil);
  - junction: returns an error wrapping errRetentionRootsUnavailable whose text contains "is a symlink or reparse point" (from maintNoFollow).
- With maintNoFollow mutated to ModeSymlink only, the new row fails: the error becomes "...delivery-generations is not a directory".
- The checkpoints and delivery-segments GC cases stay; the reviewer confirmed they discriminate.

After the fix, a full store run (co-load declared, atomic, -p 1) finished ok in 401.8 s at 90.415% (5622/6218). That is identical to round 0: the statements the generation-store GC case reached are now reached by the direct row.

Checkpoint and paths margins (+0.60 and +0.58 pp) are above the 0.3 pp threshold, so neither changed. Their risks are carried forward under open issues.

### Commits

- 7403e6ab test(store): pin the link guards against junctions on windows
- 7adc502f test(store): make the junction rows tell refusal from traversal

### Findings resolution

- **fixed**: night/major: Windows coverage floors unevaluated since 2026-09-28; release-check's cover step could fail tonight on razor-thin margins (cover.go:180-215, overnight-c8.sh:77)
  - Evidence gap closed. Release-check's shared cover pass (co-load declared, NONREFERENCE_DISK removed, atomic) was run on Windows over ./internal/... ./cmd/... on 738d67c7 at -p 2 and exited 0; all 37 floor-bound packages pass. store was the only package within 0.3 pp (90.206%, 12 statements of slack). Commits 7403e6ab and 7adc502f add Windows junction rows that pin the store's link guards, raising store's Windows coverage to 90.415% (5622/6218, +0.415 pp). An OS-specific floor was rejected because every floor passes. Not proven: behaviour at tonight's default -p 22, and the test/integration and test/e2e passes, which this run left out.
- **fixed**: review minor: TestObservationGuards_JunctionedIndexRefused (junction_edges_windows_test.go:128) passes even with the junction guard removed, because the 'external sentinel' line makes a traversing reader uncertain anyway
  - Reproduced: an overlay mutation that opens index through the junction with no Lstat or SameFile check left the original row PASSING. The file behind the junction is now empty, so a traversing reader stays certain. The same mutation now FAILS at junction_edges_windows_test.go:154 and the real guard passes. I made the same one-line change in observation_guards_unix_test.go's aliased-index case; it is verified only by GOOS=linux vet and test compile, since no local Linux runtime exists. Commit 7adc502f.
- **fixed**: review minor: the GC row's generation-store case (junction_edges_windows_test.go:59-61) halts through the no-authority migration-evidence rule, not the junction guard; its comment overstated that the store is read
  - Confirmed in code: dsegGensDir is only presence-tested in dsegMigrationEvidence (gc_delivery_segments.go:442-475). The case is removed from the GC table and the comment reworded. It is replaced by TestDsegMigrationEvidence_NamesAJunctionedGenStore: a plain directory returns (true, nil) as the control; a junction returns errRetentionRootsUnavailable containing 'is a symlink or reparse point'. With maintNoFollow mutated to ModeSymlink only, the new row FAILS ('...delivery-generations is not a directory'). Store coverage is unchanged at 5622/6218. Commit 7adc502f.

### Tests

- `QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout=30m -covermode=atomic -coverprofile=<scratch>/coverage.out ./internal/... ./cmd/...   (738d67c7, NONREFERENCE_DISK unset; round 0)`: exit 0, 30m39s; all floors pass; store 90.206% (5609/6218), paths 90.582% (654/722), checkpoint 90.603% (2208/2437)
- `go test -p 1 -count=1 -overlay=<scratch>/ovA.json -run '^TestObservationGuards_JunctionedIndexRefused$' ./internal/store/   (guard removed; before and after the fix)`: before the fix: PASS (the defect the reviewer reported); after the fix: FAIL at junction_edges_windows_test.go:154 as intended
- `go test -p 1 -count=1 -overlay=<scratch>/ovB.json -run '^TestDsegMigrationEvidence_NamesAJunctionedGenStore$' ./internal/store/   (maintNoFollow limited to ModeSymlink)`: FAIL as intended: error reads '...delivery-generations is not a directory' and lacks 'is a symlink or reparse point'
- `go test -p 1 -count=20 -run '^(TestGC_HaltsOnAJunctionWhereARetentionSourceBelongs|TestDsegMigrationEvidence_NamesAJunctionedGenStore|TestAuditPublication_NotesJunctionsItWillNotTraverse|TestObservationGuards_JunctionedIndexRefused|TestMaintenanceRestore_RefusesADestinationDotThatIsAJunction)$' ./internal/store/`: ok 18.231s (-v confirms all five rows and both GC subtests ran)
- `go test -p 1 -race -count=3 -run '^(TestGC_HaltsOnAJunctionWhereARetentionSourceBelongs|TestDsegMigrationEvidence_NamesAJunctionedGenStore|TestAuditPublication_NotesJunctionsItWillNotTraverse|TestObservationGuards_JunctionedIndexRefused|TestMaintenanceRestore_RefusesADestinationDotThatIsAJunction)$' ./internal/store/`: ok 4.570s
- `QOMPACK_UNDER_COLOAD=1 go test -p 1 -count=1 -timeout=30m -covermode=atomic -coverprofile=<scratch>/store-r1.out ./internal/store/   (NONREFERENCE_DISK unset; on 7adc502f content)`: ok 401.784s; 90.415% (5622/6218), +0.415 pp, 25.8 statements of slack
- `GOOS={windows,linux,darwin} go vet ./internal/store/ ; GOOS=linux go test -c -o /dev/null ./internal/store/`: vet ok on all three; linux test binary compiles
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/store/...`: exit 0
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: exit 0, PASS

### Criterion changes

- TestGC_HaltsOnAJunctionWhereARetentionSourceBelongs no longer has a 'generation store' case. Reason: GC only presence-tests delivery-generations/, so a plain directory halts the pass exactly as a junction does and the case could not detect a missing guard. The criterion moved to TestDsegMigrationEvidence_NamesAJunctionedGenStore, which is stricter: it requires the no-follow refusal's own error text and includes a plain-directory control.
- TestObservationGuards_JunctionedIndexRefused and TestObservationGuards_FIFOAndAliasedIndexRefused/aliased_index now plant an empty observations file behind the alias, replacing the 'external sentinel' line. Reason: the old content made a traversing reader uncertain too, so the assertion could not distinguish refusal from traversal. This is a strengthening; the assertion is unchanged.

### Open issues

- internal/checkpoint has 14.7 statements of slack on Windows (90.603%). The w20-forkwork seat has uncommitted changes to intent.go, source.go and writer.go (+193/-36 including tests). Its final check should include a Windows `go test -covermode=atomic -cover ./internal/checkpoint/` at >= 90.0% before merge. I could not measure it without touching that worktree.
- internal/paths has 4.2 statements of slack on Windows (90.582%). The uncovered Windows statements are mostly OS-exclusive branches and I/O error branches. No seat touches paths and the package is deterministic, so no change was made.
- Not covered by this measurement: tonight's release-check runs the shared pass at the default -p (22) and includes test/integration, test/*, tools/* and the isolated test/e2e pass. A test failure in any of those fails the cover step regardless of floors; p3-cover on a94a3fb failed that way, on TestIntegration_HotPathWarmWithRealResidentState.
- The prefreeze `internal` step still applies no floors. Replacing it with `QOMPACK_UNDER_COLOAD=1 go run ./tools/devtool cover` on Windows would make every candidate's Windows floors visible before the night. That is the coordinator's script to change.
- The unix aliased-index change (observation_guards_unix_test.go) has been checked only by GOOS=linux vet and test compile. No local Linux runtime exists (no WSL distro; Docker is not permitted), so CI's ubuntu job is its first real run.

### Needs owner

- Definitional mismatch, no action needed tonight: §6.4 and C3.6 define the floors on the Linux merged profile, but release-check on the Windows reference host (docs/release.md §1 step 3, D57(a)) enforces the same OWNERS.tsv floors on the Windows profile, because releaseCheckSteps reuses ciLocalSteps. Decide whether §6.4 should say so (recommended: it is the current and stricter behaviour) or whether release-check's cover should follow the Linux definition. I recommend against an OS-specific floor. No new constants were introduced.

## review:cover:0:r2: verdict `sound`, 0 finding(s)


## verify:cover: verdict `sound`, 0 finding(s)


