# V6 close-out w5-winfiles: Windows file semantics (deep backups, shared reads, case-folded guard)

Branch `closeout/w5-winfiles`. Workflow `wf_8f93ec11-36e`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `fe8ed3588ddd8d6bd83872071f741bb893786e26`

### Root cause

(a) TakeBackup walked paths.Long(Dot) and passed each walked \\?\ path to the copy. maintNoFollow then ran filepath.Rel against the unprefixed project root, which fails ("Rel: can't make \\?\... relative to ..."). No other maintenance walk mixes the two spellings. The one leftover was a test matcher that still expected the old prefixed spelling. (b) contract.readMarker and 13 other product readers, plus a test/fault poll, opened files that another process, or a goroutine they are not ordered with, replaces or removes. They used os.ReadFile/os.Open, which on Windows carry no FILE_SHARE_DELETE. That makes the writer's WriteAtomic or os.Remove fail, and the read is itself refused while a replace is finishing. The chunk-token cache flush also did its file I/O outside its lock, so concurrent flushes collided ("Access is denied"). (c) IsProtected and mayBeProtected compared protected names case-sensitively and matched sketches/tried.bloom exactly. That let case variants and NTFS ::$DATA spellings through on Windows. (d) config may import only core (section 3.2, enforced by importgraph), so it cannot call paths.Global.

### Summary

# w5-winfiles report: four Windows file-semantics defects

All four items are fixed and committed. Head is fe8ed35: 15 commits on top of 90e1db3, and the last code commit is 88373aa (fe8ed35 adds evidence logs only).

Every touched package passes in full on Windows (runs/60–69). The exception is one guard test, `TestCarriedDefects_WaveReportRequiresResolution`, which fails identically at base. On Linux (non-root, -race, 88373aa) the only other failure is one known load-sensitive daemon test, which passed 5/5 alone. The lint set passes at 88373aa.

## Resume review
- **0c7a699 (backup long paths):** reviewed and kept.
  - The walk still enumerates `paths.Long(Dot)`, but each source is now rebuilt from `m.l.Dot` plus the walked relative name, so `maintNoFollow` gets the unprefixed spelling.
  - Its deep-path tests were re-run at base (red) and at HEAD (green).
- **`marker_windows_test.go`:** kept.
- **`sharedreaders_test.go` edits:** rewritten and extended (see (b)).
- **Pre-restart logs:** replaced. The two incomplete ones (05, 08) were deleted, and 01–04 were regenerated against 90e1db3 and HEAD.

## (a) Backup long paths (C1.7, V6-RECOVERY-2)
**Root cause:** `TakeBackup` handed the walked `\\?\` path to `copyBackupFile`. `maintNoFollow` then called `filepath.Rel` against the unprefixed project root, which fails.
- runs/01 (store) and runs/03 (CLI), at 90e1db3 plus the new tests: the deep-path backup rows fail with that exact Rel error. The GC and fsck deep rows pass.
- runs/02 and runs/04 at HEAD: all green, including the two `TestBackupCLI` rows w3-paths saw fail.

**Audit of every other maintenance walk:**
- fsck's four walks pass the prefixed path only to functions that apply `paths.Long` again, which is idempotent.
- GC's sweep calls `filepath.Rel` against the same prefixed base.
- `loadStoreState` and the staged-binary prune don't use the walked path in any way that matters.
- backup verify and restore build paths from manifest names under the unprefixed backup directory.
- Rollover code uses `os.Root` with relative names.

**Dynamic audit with every `t.TempDir` past MAX_PATH (TMP set to a 224-character directory, archive of 8f57907):**
- runs/40, CLI backup, fsck and doctor tests: ok.
- runs/41, daemon rollover, generation, reader, path, radix, diagnostics and segment tests: ok.
- runs/42, full `internal/store`: three failures.
  - `TestBackup_RefusesFrontierChangedBeforeItsOwnCopy` matched `copyBackupFile`'s source against `paths.Long(head)`. 88373aa changes it to `head`, which is the copy's contract at every depth; it passes under the deep TMP (runs/43).
  - `TestReadBoundedObject_RefusesASocketLeaf` and `TestOpenObjectLeaf_BranchesOnTheHandlesReparseAttribute` fail only because their AF_UNIX socket fixture path is longer than the 108-byte socket path limit. This is a fixture limit in an artificial environment, not a product defect, so I left them alone.

## (b) Shared reads of files replaced atomically
**Root cause:**
- `readMarker` used `os.ReadFile`. On Windows that handle fails `WriteMarker`'s `WriteAtomic`, which is never retried, and the read is itself refused while a replace is finishing.
- `checkSessionStartFires` counted such a refused read as a missing marker, which moves toward the SevCritical §12.1 degradation although the hook fired.
- runs/10 is red: the Windows tests hold the DELETE-access handle a finishing replace holds. runs/11 is green.

**Audit:** I classified every product `os.ReadFile`, `os.Open` and `os.OpenFile` under internal/ and cmd/ (61 functions). These readers were converted to `paths.ReadFileShared` or `paths.OpenShared`:
- **cli:** `readPersistedMetrics` (metrics/latency.json).
- **store:** `loadStoreState` (state/store.json, read by the read-only opens of doctor and fsck).
- **store:** `ReadCaptureSidecar`.
- **store:** `CompactRetentionRoots`, `loadGCState`, `pendingMarkerRoot`. GC passes are not serialized: each concurrent session end runs one, as does the idle scheduler.
- **tokens:** `loadCalibEntry` and `persist` for the user-global calibration.json. A refused read in `persist` wrote back this project's entry alone and dropped every other project's factor.
- **sketch:** `LoadWithLog` (fsck and the negknow ledger it opens).
- **daemon:** `readStateFile` for the scheduler state. `Persist` writes after releasing `r.mu`, while a session bind reads under `r.mu`.
- **daemon:** `LoadSessionRecovery`. It is exported and test/e2e's V5 x03 polls it from its own process.
- **daemon:** `readBlob`. The drain's cleanup removes blobs without being ordered with the live ingest.
- **checkpoint:** `readGitState` for git's HEAD and an index of up to 64 MiB, which git replaces by renaming a lock file over them.
- **test harness:** test/fault's `flushAndAwaitEnd` marker poll, the same defect w4 fixed in test/e2e.

**Chunk-token cache (same audit):** `flush` did its file I/O after releasing `cmu`, so an append's read-write handle overlapped a compaction's `WriteAtomic` of the same file.
- runs/12: 3 of 3 runs red with "Access is denied".
- A new `fmu` now serializes the whole flush. runs/13: 10/10 green.

**Guard made complete:**
- test/guards' `sharedReaders` now carries each converted reader, with the writer it could stall.
- The new `productreads_test.go` parses every non-test file under internal/ and cmd/. Any function that references `os.ReadFile`, `os.Open` or `os.OpenFile` must have an `ordinaryReads` row saying why nothing can replace or remove that file while the handle is open, and stale rows fail too. A scanner self-test pins the shapes it must see.
- runs/14, at 0c7a699 with the new guard files: 15 `sharedReaders` rows fail, and the scan lists the same functions.
- `os.Root` opens were not flagged: they already open with FILE_SHARE_DELETE.

**config.json stays on ordinary reads:** no Qompack process replaces it, and config cannot import paths.

## (c) §7.4 guard case folding (coordinator default)
**Root cause:** the guard compared spellings exactly. Evidence is runs/20 (red) and runs/21 (green).
- `WriteAtomic` over `CHECKPOINTS\0001.json` replaced a sealed checkpoint.
- `OpenFile(PINS\invariants.jsonl, O_TRUNC)` emptied the pins log.
- `OpenFile(tried.bloom::$DATA, O_TRUNC)` emptied the bloom.
- I also measured that a stream suffix on a directory resolves: `checkpoints::$INDEX_ALLOCATION\0001.json` opens the checkpoint.

**Fix (new file `internal/paths/protected_names.go`):**
- Names are folded wherever `paths.DefaultFold` holds (Windows, and darwin conservatively). The fold maps to upper then lower case, which covers both NTFS's case table and APFS's case folding.
- On Windows, every NTFS stream suffix is stripped before a path is judged.
- `ownerOf` now asks a folded `.qompack` for protection on darwin, while staging still picks only an exact `.qompack`.
- `mayBeProtected` stays allocation-free for ASCII paths (new test), and `BenchmarkPathsWriteAtomic_4KB` stays at 29 allocs/op (runs/22).

**Out of scope, documented in docs/architecture.md and 00-ARCHITECTURE.md:** 8.3 short names (these are generated on this host's C:, where `CHECKP~1` resolved), hard links, reparse points, and Unicode normalization differences in the project root on macOS. Stream suffixes were handled rather than just documented, because stripping them was cheap and safe.

## (d) paths.Global in the config loaders
This is not possible. §3.2 gives config the allow-set {core}, and importgraph enforces it.
- The four hand-joins became `config.UserConfigPath` and `config.ProjectConfigPath`, which state that reason.
- A new test in test/guards (which may import both packages) requires them to equal config.json in `paths.Global(home)` and `paths.Of(root).Dot`.

## Criterion changes (each with its rationale)
1. **88373aa (test correction, not a loosening):** `TestBackup_RefusesFrontierChangedBeforeItsOwnCopy` now matches the copy source against `head` instead of `paths.Long(head)`. The two are identical at short depth; past MAX_PATH the old matcher could never match after 0c7a699.
2. **New standing guard:** `TestGuard_EveryProductReadIsClassified`, plus 15 new `sharedReaders` rows.
3. **Wider protection:** `IsProtected` now also refuses case variants (Windows, darwin) and NTFS stream spellings.

## Results at 88373aa, every other workstream running on the same host
- **Windows, touched packages in full (runs/60–69):** paths, contract, tokens, sketch, config, checkpoint, store, cli and daemon all ok. test/guards fails only `TestCarriedDefects_WaveReportRequiresResolution`, which fails identically at base (runs/31).
- **Windows, extra rows:** runs/70 covers `TestFault_CheckpointDropsAnUnresolvablePointer` and `TestFault_Lifecycle`; runs/71 covers `TestV5_HookEventToTombstoneToRetrievalAfterRestart`. All pass.
- **Linux (non-root, -race, same packages):** everything passes except the same carried-defects test and `TestDeliveryOrder_LaneOverflowIsDrainedOnRequest` (4 of 5 acknowledged at its 30 s bound). That test passed 5/5 alone with -race at the same commit; w3-paths and w3-startroute recorded the same load sensitivity. No race logs.
- **Formatting, vet and lint:** fmt-check passes; vet passes on Windows and Linux (darwin for paths and config); the lint set (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, docmarkers, runpatterns) passes at 88373aa (runs/50); the docs test passes.

Evidence is in `C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-winfiles/plans/sdd/V6-closeout/w5-winfiles/runs/`, with Linux artifacts under `runs/linux/`.

### Commits

- 0c7a699 fix(store): back up deep projects through their logical paths (earlier seat; reviewed and kept)
- 3c66d4c fix(contract): read the session-end marker with delete sharing
- 71ee4ec fix(daemon): read scheduler, recovery and blob files shared
- 6836b2f fix(store): read store, sidecar and GC files with delete sharing
- eb8cb98 fix(tokens): share calibration reads and serialize cache flushes
- 038c963 fix(sketch): load sketches with delete sharing
- 73e8fa4 fix(cli): read the persisted metrics snapshot shared
- c560ce7 fix(checkpoint): read git HEAD and index with delete sharing
- cbeeb71 test(fault): poll the session-end marker without blocking it
- c6019e8 test(guards): classify every product file open
- 02ebf20 fix(paths): fold case and NTFS streams in the section 7.4 guard
- 8f57907 refactor(config): name both config files in one place
- 007c3d6 test(guards): correct the dag log row's shared-reader claim
- 88373aa test(store): match the backup copy's logical source spelling
- fe8ed35 docs(v6): add w5-winfiles evidence logs

### Tests

- `go test ./internal/contract -run '^TestReadMarker_ReadsThroughAReplaceStillFinishing$' -count=1 -v (runs/10 before the fix, runs/11 after; run together with TestSessionStartFires_AMarkerMidReplaceIsNotAnAbsence)` — before: both FAIL (sharing violation; session_start.fires counts the marker as absent). After: both PASS
- `go test ./internal/tokens -run '^TestChunkCache_ConcurrentFlushesNeverOverlapOnDisk$' -count=3 -v (runs/12, before) / -count=10 (runs/13, after)` — before: FAIL 3/3 ('rename ... chunktokens.bin: Access is denied'). After: PASS 10/10
- `go test ./test/guards with TestGuard_HotFilesAreReadWithDeleteSharing, TestGuard_SharedReaderScannerSeesAForbiddenCall, TestGuard_EveryProductReadIsClassified and TestGuard_ProductOpenScannerSeesEveryShape as one alternation, on git archive 0c7a699 plus the new guard files (runs/14)` — FAIL as intended: 15 sharedReaders rows fail and the scan lists the same functions. The same four tests PASS at HEAD
- `go test ./internal/paths with the six case/stream guard tests as one alternation (runs/20 before, runs/21 after)` — before: FAIL. The checkpoint was replaced, the pins log truncated, and the bloom emptied through ::$DATA. After: all PASS. runs/22: BenchmarkPathsWriteAtomic_4KB stays at 29 allocs/op
- `go test ./internal/store -run '^TestMaintenance_BackupVerifyRestoreBeyondMaxPath$' -count=1 -v (runs/01 on archive 90e1db3 plus the test, runs/02 at HEAD)` — base: FAIL 'Rel: can't make \\?\... relative to ...'. HEAD: PASS. TestGC_RunsBeyondMaxPath passes on both
- `go test ./internal/cli -run '^TestBackupCLI_CreateVerifyRestoreBeyondMaxPath$' -count=1 -v (runs/03 on base, runs/04 at HEAD)` — base: FAIL (backup create Rel error). HEAD: PASS. TestFsckCLI_PassesBeyondMaxPath, TestBackupCLI_ConsistentRestoreRetainsLaterSourceWrites and TestBackupCLI_UncertifiedSnapshotIsRefusedByEveryReader also PASS at HEAD
- `deep-TMP audit (TMP 224 chars, archive 8f57907): internal/cli backup/fsck/doctor prefixes (runs/40), daemon rollover prefixes (runs/41), go test ./internal/store -count=1 (runs/42)` — cli ok, daemon ok. store: 3 failures. One was a test matcher that expected the old prefixed spelling (fixed in 88373aa; go test ./internal/store -run '^TestBackup_RefusesFrontierChangedBeforeItsOwnCopy$' passes under the same TMP, runs/43). Two are the AF_UNIX socket fixture limit, not the product
- `go test <pkg> -count=1 -timeout=30m for internal/paths, contract, tokens, sketch, config, checkpoint, store, cli, daemon and test/guards on Windows at 88373aa (runs/60-69)` — all ok except test/guards: only TestCarriedDefects_WaveReportRequiresResolution fails, and it also fails at base 90e1db3 (runs/31)
- `go test ./test/fault -run '^TestFault_CheckpointDropsAnUnresolvablePointer$' and -run '^TestFault_Lifecycle$' -count=1 (runs/70); go test ./test/e2e -run '^TestV5_HookEventToTombstoneToRetrievalAfterRestart$' -count=1 (runs/71)` — PASS, PASS, PASS
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-winfiles ... 88373aa touched-race -- ./internal/paths ./internal/contract ./internal/tokens ./internal/sketch ./internal/config ./internal/checkpoint ./internal/store ./internal/cli ./internal/daemon ./test/guards` — non-root, -race, no race logs. All PASS except TestCarriedDefects_WaveReportRequiresResolution (fails at base too) and TestDeliveryOrder_LaneOverflowIsDrainedOnRequest (the co-loaded run timed out at 4 of 5 acknowledged)
- `linux-nonroot-gate.sh ... 88373aa lane-overflow-alone --run '^TestDeliveryOrder_LaneOverflowIsDrainedOnRequest$' --count 5 -- ./internal/daemon` — PASS 5/5 alone with -race. This is a known load-sensitive test (w3-paths, w3-startroute), not a regression
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns (runs/50); go run ./tools/devtool fmt-check; go vet on the touched packages (Windows and GOOS=linux; GOOS=darwin for paths and config); go test ./test/docs` — all pass

### Criterion changes

- 88373aa: TestBackup_RefusesFrontierChangedBeforeItsOwnCopy now matches the copy's source against head instead of paths.Long(head). This is a test correction, not a loosening. Since 0c7a699, TakeBackup passes the path spelled from the layout's Dot. Long(head) equals head while head is short, but past MAX_PATH the old matcher could never fire (runs/42 fails, runs/43 passes).
- c6019e8 (stronger): new TestGuard_EveryProductReadIsClassified. Every product os.ReadFile/os.Open/os.OpenFile under internal/ and cmd/ needs an ordinaryReads row saying why nothing can replace or remove that file while the handle is open, and stale rows fail. sharedReaders gains 15 rows.
- 02ebf20 (stronger): IsProtected and mayBeProtected now also treat case variants as protected where paths.DefaultFold holds (Windows, darwin), and on Windows also NTFS stream spellings (::$DATA, ::$INDEX_ALLOCATION, named streams). Before, both were writable.

### Open issues

- TestCarriedDefects_WaveReportRequiresResolution fails at base 90e1db3 and at HEAD, on Windows and Linux. plans/V6-report.md exists while SP06-D2, SP08-D1, SP09-D1, SP10-D1, SP20-D2 and SP08-D3 are still deferred:V6-VERIFY. This is sign-off state for the coordinator, not this workstream.
- Merge note: TestGuard_EveryProductReadIsClassified will fail after merge if another wave-5 branch adds an os.ReadFile/os.Open/os.OpenFile in internal/ or cmd/, or renames or removes a listed function (for example spawn.go spawnDetached, spawn_stage.go copyStaged/fileSHA256, near w5-coldstart's area). The failure message gives both ways to fix it. test/fault/fault.go flushAndAwaitEnd has a small hunk that may meet w5-helpers' edits.
- GC passes are still not serialized: concurrent session ends and the idle scheduler can each run one. Shared reads remove the Windows sharing failures, but two overlapping passes can still race on gc.json's resume cursor and on retention-roots compaction. Route to the store owner.
- config.json is still read with ordinary handles, because §3.2 keeps config from importing paths. An editor's atomic save that overlaps a hook's read can make LoadForCapture fail closed for that one hook (D8).
- Test harness polls other than the session-end marker, rehydrate state and session recovery were not audited. For example, test/e2e scheduler_idle_test.go polls metrics/latency.json with os.ReadFile (it retries).
- Pre-existing: internal/cli tests (seedFsckProject and others) open stores without HOME isolation, so the calibration loader reads the real ~/.qompack/calibration.json if one exists. It only writes when Calibrate runs. The new rows isolate HOME.
- Outside the textual guard, and documented: 8.3 short names (generated on this host's C:, where CHECKP~1 resolves), hard links, reparse points, and Unicode normalization of the project root on macOS. darwin was not run (no host); platform-neutral rows keyed on DefaultFold cover the logic.
- Deep-TMP only: TestReadBoundedObject_RefusesASocketLeaf and TestOpenObjectLeaf_BranchesOnTheHandlesReparseAttribute fail when their AF_UNIX socket fixture path is longer than 108 bytes. This is a fixture limit; left unchanged.

### Needs the owner

- §3.2 decision: config may import only core, so the config loaders cannot use paths.Global or paths.ReadFileShared. Either amend §3.2 to allow config -> paths (no cycle: paths imports only core), or accept the residual that a hook's ordinary read of config.json can race an editor's atomic save and fail closed for that hook under D8. I made no change beyond naming the paths and the guard test.
- Ratify the new standing guard TestGuard_EveryProductReadIsClassified. Every future product open must be classified in test/guards' ordinaryReads or sharedReaders, which is a recurring cost for every branch.
- Confirm how the coordinator default was applied. Case folding also covers darwin, but only for protection: staging still requires an exact .qompack, so no stray store directory appears on a case-sensitive volume. NTFS stream suffixes were stripped rather than only documented, because stripping was cheap and only adds protection. 8.3 names are documented as out of scope. No new budget or bound numbers were introduced; the new constants are test sizing only.

## Independent review

### review:winfiles: sound

- **minor** `test/guards/productreads_test.go:37-38 (whyBackupTree), cf. internal/store/maintenance.go:210` — The whyBackupTree reason is used by seven ordinaryReads rows (VerifyBackup, RestoreBackup, maintHashFile, maintCopyVerify, maintReadBounded, ...). It says every `qompack backup` action holds the writer lease. It does not: VerifyBackupAt, the read-only CLI verify path, builds a bare Migrator and takes no lease. The conclusion still holds on other grounds, because TakeBackup refuses an existing id before it writes (backup.go:226), so a backup tree file is never replaced. But the guard's stated justification is factually wrong, and the guard exists to record exactly this judgement.
  - Evidence: maintenance.go:210-213: `x := &Maintenance{m: &Migrator{root: root, l: paths.Of(root)}}; return x.VerifyBackupContext(ctx, id)`, which acquires no lease. backup.go:226-230 returns os.ErrExist when the backup dir already exists, before AcquireWriter.
  - Fix: Rewrite whyBackupTree to rest on the true invariant: "a file of a backup tree, created once under a fresh backup id (TakeBackup refuses an existing id) and never replaced or removed afterwards". Drop the writer-lease clause.
- **nit** `internal/store/maintenance_longpath_test.go:116-123 (TestGC_RunsBeyondMaxPath)` — The doc comment says a prefixed/unprefixed mismatch would show up as "an object left behind". The test seeds only a live root and asserts only ScannedObjects > 0, so it never checks that the sweep removes an unreachable object through the prefixed spelling. The claim it states is untested.
  - Evidence: Only seedRoot(live) is called; the assertions are NoError, !Truncated and Positive(ScannedObjects). DeletedObjects is never asserted.
  - Fix: Seed an unreachable object with an mtime older than the pass start (or tombstone a root), then assert rep.DeletedObjects >= 1 and that the object file is gone under paths.Long.
- **nit** `test/guards/productreads_test.go:ordinaryOpensInFile / ordinaryOpeners` — The completeness scanner matches the literal identifier `os`. A file that imports "os" under an alias, or dot-imports it, escapes TestGuard_EveryProductReadIsClassified entirely. Rows are also keyed by FuncDecl name with the receiver ignored, so two same-named methods on different types in one file share a row. Neither case exists in the tree today, but the guard claims the inventory is exact.
  - Evidence: `pkg, ok := sel.X.(*ast.Ident); name := pkg.Name + "." + sel.Sel.Name; if !ordinaryOpeners[name]`. There is no check of f.Imports for the local name of "os", and the key is `rel + ":" + fd.Name.Name`.
  - Fix: Resolve the local name of the "os" import from f.Imports and fail on a dot-import, or require that product files import os unaliased. Include the receiver type in the key, e.g. "(*T).m".
- **nit** `test/guards/sharedreaders_test.go:129` — The comment refers to "ordinaryReaders below". The inventory is `ordinaryReads`, and it lives in productreads_test.go, not below.
  - Evidence: `// that audit. Every other product read is classified in ordinaryReaders below, with the reason`
  - Fix: Change it to "classified in productreads_test.go's ordinaryReads".
- **nit** `internal/paths/protected_names.go:125-133 (relUnderDot doc)` — The doc says the returned position keeps target's own separators and case. In the non-ASCII branch it is computed by filepath.Rel(foldKey(dot), foldKey(target)), so it comes back folded. That is harmless, since callers compare through sameName, but the doc is inaccurate.
  - Evidence: `rel, err := filepath.Rel(foldKey(dot), foldKey(target)) ... return rel, true`
  - Fix: Change the doc to say the position is folded in the non-ASCII branch, and that callers must compare its elements with sameName.

## Fix seat (review resolution) — status `done`, head `421951d83e26338399562ba2a02314eb60076d7c`

### Root cause

The review finding is a wrong justification string, not a code defect. whyBackupTree based the safety of ordinary reads of backup-tree files on the writer lease, but fsck's VerifyBackupAt (internal/store/maintenance.go:210) reaches maintReadBounded and maintHashFile with no lease. The reads are safe for a different reason: a tree file is created once under a fresh id and never replaced, and the one removal (the ErrBackupMoved cleanup) comes before the manifest that every reader goes through.

### Summary

FIX-seat report for w5-winfiles. Branch closeout/w5-winfiles in C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-winfiles, cut from closeout/integration @ 90e1db3. HEAD is now 421951d. The implementer's 15 commits (0c7a699..fe8ed35) are unchanged. The FIX seat added two commits: 2f9567a (the guard text correction) and 421951d (evidence logs 80-82 under plans/sdd/V6-closeout/w5-winfiles/runs/).

## Review resolution

Finding 1 (minor): test/guards/productreads_test.go whyBackupTree claims a writer lease. It is CONFIRMED in substance and FIXED in 2f9567a. The reviewer got one detail wrong. `qompack backup verify` does take the writer lease: internal/cli/backup.go:99 calls acquireWriterLease before every action (create, verify and restore). So the literal sentence "every `qompack backup` action holds the writer lease" is true. VerifyBackupAt (internal/store/maintenance.go:210-213) is called by `qompack fsck` (internal/cli/fsck.go:1964, verifyOneBackup), not by `qompack backup`. It builds a bare Migrator and holds no lease, and through it fsck reaches two rows that use whyBackupTree: readValidatedManifest -> maintReadBounded, and verifyTree -> maintHashFile. The reason's conclusion ("none overlaps the TakeBackup that wrote it") rested on a lease, and that lease does not cover the fsck path. So the stated justification was wrong for part of what it classifies, even though the conclusion holds.

The true invariant, checked against the code:
- TakeBackup refuses an existing id before it writes (backup.go:226-230; Maintenance.TakeBackup maintenance.go:159 does an Lstat as well).
- No product code replaces a backup-tree file. A grep for RemoveAll and for users of the backup dir finds only backup.go:344.
- That one removal, the ErrBackupMoved cleanup, runs before the manifest is written (the manifest's WriteAtomic comes later, at backup.go:~365).
- Every reader in these rows reaches tree files only through the manifest: VerifyBackup and RestoreBackup read the manifest first; verifyTree and stageRestore iterate man.Files.
- maintHashFile also re-hashes the restore's private staged copy (verifyStaged). The old reason did not cover that, and the new one does.

The new text:
"a file of a backup tree, or of a restore's private staged copy of one: created once under a fresh backup id (TakeBackup refuses an existing id before it writes) and never replaced afterwards. The one removal, TakeBackup's ErrBackupMoved cleanup, runs before the manifest exists, and these readers reach tree files only through that manifest. No lease is assumed: fsck's VerifyBackupAt reads backups without one"

The same VerifyBackupAt code also contradicted the neighbouring whyMigrateFiles reason, which said "the Migrator runs only inside `qompack backup` under the writer lease". NewMigrator has no product callers; Migrators are built only in NewMaintenance (cli/backup.go, under the lease) and in VerifyBackupAt. So whyMigrateFiles now says the Migrator writes migrate files only inside `qompack backup` under the lease, and that fsck's bare Migrator reads no migrate file (VerifyBackupContext reads only the pending-marker Lstat, the manifest and the tree). fsck reads the migrate files through paths.ReadFileShared (fsck.go:1980). No classification changed: only the written reasons did. No failing-test-first step applies, because the change is to the guard's recorded justification strings and not to behaviour.

## Commands and results (FIX seat; the machine was loaded by other workstreams)
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./test/guards`: exit 0 on Windows and with GOOS=linux.
- `go test -count=1 -run '^TestGuard_EveryProductReadIsClassified$' -v ./test/guards`: PASS (runs/80-fix-guards-classified.log).
- `go test -count=1 -timeout=30m ./test/guards` (full package, once): exit 1 (runs/81-fix-guards-full.log). The only failure is TestCarriedDefects_WaveReportRequiresResolution with the same six subtests (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3). They fail identically at the base (runs/31-guards-carrieddefects-at-base.log) and in the implementer's head run (runs/69). This is checkpoint bookkeeping for the coordinator, not a defect of this branch. Every other guard test passes, including TestGuard_EveryProductReadIsClassified.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all eight sub-checks PASS, exit 0 (runs/82-fix-lint.log).

## Criterion changes
None. No check, threshold, assertion or golden was touched. The FIX seat changed only two justification strings.

## Implementer's delivery (unchanged; see the commits and runs/01-71)
- (a) Backup long paths: fixed at the source, with deep-path store and CLI regression tests (0c7a699, 88373aa).
- (b) Readers of files the daemon replaces now use paths.ReadFileShared or OpenShared: the contract marker, daemon, store, tokens, sketch, cli metrics and checkpoint git reads (3c66d4c..c560ce7). sharedReaders is complete, and a new standing guard classifies every product open (c6019e8, 007c3d6).
- (c) The §7.4 guard folds case and strips NTFS stream suffixes (02ebf20).
- (d) The config paths are named in one place (8f57907). §3.2 blocks paths.Global and ReadFileShared from config; see needs_owner.

### Commits

- 0c7a699 fix(store): back up deep projects through their logical paths
- 3c66d4c fix(contract): read the session-end marker with delete sharing
- 71ee4ec fix(daemon): read scheduler, recovery and blob files shared
- 6836b2f fix(store): read store, sidecar and GC files with delete sharing
- eb8cb98 fix(tokens): share calibration reads and serialize cache flushes
- 038c963 fix(sketch): load sketches with delete sharing
- 73e8fa4 fix(cli): read the persisted metrics snapshot shared
- c560ce7 fix(checkpoint): read git HEAD and index with delete sharing
- cbeeb71 test(fault): poll the session-end marker without blocking it
- c6019e8 test(guards): classify every product file open
- 02ebf20 fix(paths): fold case and NTFS streams in the section 7.4 guard
- 8f57907 refactor(config): name both config files in one place
- 007c3d6 test(guards): correct the dag log row's shared-reader claim
- 88373aa test(store): match the backup copy's logical source spelling
- fe8ed35 docs(v6): add w5-winfiles evidence logs
- 2f9567a test(guards): rest the backup-tree reason on its true invariant (FIX seat)
- 421951d docs(v6): add w5-winfiles review-fix evidence logs (FIX seat)

### Tests

- `go test -count=1 -run '^TestGuard_EveryProductReadIsClassified$' -v ./test/guards` — PASS (runs/80-fix-guards-classified.log)
- `go test -count=1 -timeout=30m ./test/guards` — exit 1: only TestCarriedDefects_WaveReportRequiresResolution fails, with 6 subtests. The same 6 fail at the base (runs/31) and at the implementer head (runs/69), so this is not caused by this branch. All other guard tests pass (runs/81-fix-guards-full.log)
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 sub-checks PASS, exit 0 (runs/82-fix-lint.log)
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./test/guards (Windows and GOOS=linux)` — exit 0 on both

### Open issues

- TestCarriedDefects_WaveReportRequiresResolution fails at the base on six carried defects (SP06-D2, SP08-D1, SP10-D1, SP09-D1, SP20-D2, SP08-D3). Each needs a V6 wave-report resolution, fixed or deferred with a reason, which is the coordinator's checkpoint bookkeeping and outside this workstream's scope.
- Residual (pending the §3.2 decision in needs_owner): a hook's ordinary os.ReadFile of config.json can race an editor's atomic save and fail closed for that hook under D8.

### Needs the owner

- §3.2 decision: config may import only core, so the config loaders cannot use paths.Global or paths.ReadFileShared. Either amend §3.2 to allow config -> paths (there is no cycle, because paths imports only core), or accept the residual that a hook's ordinary read of config.json can race an editor's atomic save and fail closed for that hook under D8. The implementer changed nothing beyond naming the paths and adding the guard row.
- Ratify the new standing guard TestGuard_EveryProductReadIsClassified. Every future product file open must be classified in test/guards' ordinaryReads or sharedReaders, which is a recurring cost on every branch. The FIX seat corrected two of its recorded reasons (whyBackupTree, whyMigrateFiles) after review.
- Confirm how the coordinator default was applied to the §7.4 guard. Case folding also covers darwin, but only for protection: staging still requires an exact .qompack, so no stray store directory appears on a case-sensitive volume. NTFS ::$DATA stream suffixes are stripped rather than only documented, because stripping was cheap and only adds protection. 8.3 short names are documented as out of scope. No new budget or bound numbers were introduced; the new constants are test sizing only.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


