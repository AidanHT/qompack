# Wave 13 — w13-restore (live-lane defects, D45)

Branch `closeout/w13-restore`. Workflow `wf_3da1508e-fe4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `9a53a1a50ba2d62be7d0b568150fb005ad6ab392`

### Root cause

(1) F-UAT03-2: checkpoint.FileWriter.Advance, run by the idle frontier advance, called SegmentLog.MarkEncoded(ids, draft.seq). MarkEncoded appends a durable encode record naming the draft's sequence as soon as it runs, but that sequence only exists once PreCompact or the cadence seals the draft. SessionEnd closes the last segment. Idle work then encodes it into draft 0002, and at a clean idle exit only state/draft-<s>.json holds that draft, while index/segments.jsonl names 0002 permanently. A latent consequence: claimSeq considered only the manifest and this process's issuedSeq, so a later daemon's draft for another session could take seq 2, and A's claim would then point at a checkpoint that holds none of its turns. (2) F-C49-2: store.Maintenance.proveReader failed the whole restore when GetRoot missed for any tool_use record. It did not check that roots.jsonl tombstoned the root (the MCP server's ephemeral recall record, collected by GC), which is the case fsck's index.tool_use row treats as an ok note. backup verify only re-hashed files, so it could never see this. (3) D3: 301a8e9 never called LinkCaptureReference for prompts (observer/identity.go links tool and subagent only), so every prompt sidecar it wrote is published:false even though the prompt_<s>_<turn> record exists (argd = ArgsDigest({"prompt": text})). v0.2.0 writes no sidecars at all (no capture_sidecar.go, no checkpoint writer), so the released path was never affected. (4) RestoreProof.Note was a fixed store-level string. The CLI then ran fsck --seal-check on the destination and never updated the note. (5) F-UAT03-4: the lock staleness protocol dialled the address the lock recorded. A copied store's lock records the original path's pipe, so while the original's daemon ran the copy's lock was never stale. runDaemon returned nil on ErrLockHeld before any logger existed. (6) F-C49-3: store.readObjectFile maps "indexed but absent" to ErrDamaged, and the MCP layer words every ErrDamaged as "a damaged object is preserved as evidence", even when tmp/quarantine holds nothing.

### Summary

All six items are fixed on closeout/w13-restore (13 commits, 84d25122..9a53a1a5). Each fix has a regression test that failed on the old code.

(1) Segment encode ordering (F-UAT03-2): the fix is in internal/store and internal/checkpoint.
- The segment log gains an optional two-phase encode, store.SegmentReservation (ReserveEncoded / CommitEncoded).
- Advance now reserves. A reservation has the same DPI guard and the same in-memory effect, so scheduler residual, Frontier and Unencoded behave as before, but nothing is appended to the index.
- Finalize commits the records at the sequence the artifact was actually written at, after the artifact's bytes are durable and before the MANIFEST line (the existing seal order).
- A resumed draft reserves its segments again.
- A fresh draft's sequence now also skips any sequence named by a persisted draft file or by a durable encode record.
- fsck index.segments explains a claim the manifest lacks and names it in a note instead of failing:
  - a persisted draft (live or .stale.json) holding that segment → "unsealed-draft claim";
  - an orphan artifact at that sequence → note (the checkpoints row still reports the orphan).
- A claim nothing on disk explains is still a defect. Restore's integrity step is this same fsck scan, so an existing store with such a claim now restores.
- Regression: test/e2e TestE2E_IdleExitLeavesNoUnsealedEncodeClaim drives the real binary through a real idle exit and then fsck + backup create/verify/restore. It failed on the base with "segment 1 encoded into checkpoint 0002, which the manifest does not record".

(2) GC-retired tool refs (F-C49-2):
- proveReader applies fsck's tombstone rule: a tool ref whose root a gc tombstone retired is counted in the new RestoreProof.ToolRefsTombstoned and is not a failure.
- backup verify now restores into a scratch destination under the system temp directory, makes the same reader proof, runs the same fsck --seal-check integrity scan, reports both, and removes the scratch copy on success. On failure the copy is kept and the error names it.

(3) Cross-version (D3):
- Established by reading v0.2.0 and by running binaries built from v0.2.0, 301a8e9, the base and this branch over stores those builds' own hooks wrote. v0.2.0 stores read clean on both the base and the candidate. The base reproduces D3 exactly on a 301a8e9 store (fsck 1, restore 1, LOUD unpublished_captures=3); this branch reads it clean.
- The publication audit (daemon startup LOUD, fsck publication row) and fsck captures now read an earlier build's unlinked prompt sidecar as published when a prompt record in its session carries its unbound args digest. One record accounts for one sidecar. Nothing is rewritten or re-minted.
- A committed v0.2.0 store fixture guards the released path. There are no local paths in it; a nested .gitattributes keeps its bytes as written.

(4) Restore note: it now states only what the reader proof covers, then either that the integrity report checked the checkpoint chain and delivery seals, or that no integrity check ran. CheckpointSealCovered keeps its meaning (false).

(5) Copied store's lock (F-UAT03-4):
- A lock that records another project's address is judged by a dial of this project's own address plus this store's heartbeat (the existing 90 s rule). The foreign address and pid are not consulted.
- A daemon that loses the lock logs who holds it, and whether the lock was written for another path, to the day log and to --foreground stderr.
- fsck's daemon row (and doctor, through the same helper) probes this project's own address and names a foreign lock.

(6) Missing-object reason (F-C49-3): the new store.ErrObjectMissing narrows ErrDamaged when tmp/quarantine holds no evidence for the object, and MCP words it as "the stored object is missing". The answer stays unavailable.

Docs updated: docs/backup.md, docs/troubleshooting.md, and the user-guide verify row. Evidence is under plans/sdd/V6-closeout/w13-restore/runs/ (RED/GREEN logs, full-package results, lint, crossversion.txt, gen_store.py/check_store.py). The only full-package red, TestGC_DeadlineOvershootIsBoundedByTheCheckInterval, missed by 2 ms (252 ms vs a 250 ms limit) while the machine was loaded, and passed when re-run alone. My changes do not touch GC timing.

### Commits

- d18485c4 fix(checkpoint): record segment encodes only when the seal lands
- 3f8f21b0 fix(fsck): name an unsealed-draft encode claim instead of failing
- a8eeb483 fix(store): let restore accept tool refs a gc tombstone retired
- 35d3d35a fix(backup): make verify prove what restore proves
- 63f1791f fix(mcp): say an object is missing when nothing preserved it
- 8ff08b11 fix(daemon): judge a copied store's lock by its own heartbeat
- 7585cbee fix(fsck): dial this project's address when its lock is foreign
- 6a0feb9c fix(store): read an earlier build's unlinked prompt captures as published
- 266c2f8e test(cli): read a store the v0.2.0 release wrote
- b654ec87 docs(backup): document restore-after-use and earlier-build stores
- faff8831 test(cli): check the publication auditor assertion in cross-version rows
- b99632ba test(evidence): record the w13-restore runs and cross-version check
- 9a53a1a5 docs(checkpoint): say what an aborted draft's reservations become

### Tests

- `go test -p 2 -count=1 -run '^TestE2E_IdleExitLeavesNoUnsealedEncodeClaim$' ./test/e2e/` — RED on base: 'index/segments.jsonl records segment 1 encoded into checkpoint 0002, which the manifest does not record (sealed: map[1:true])' (runs/red1.txt). GREEN after d18485c4 (28.4 s), including fsck exit 0 and backup create/verify/restore exit 0 after the idle exit.
- `go test -p 2 -count=1 -run '^(TestAdvanceRecordsNoEncodeUntilTheSeal|TestFinalizeSealsTheSegmentsAResumedDraftHolds|TestBeginSkipsASequenceAnotherSessionsPersistedDraftHolds|TestBeginSkipsASequenceADurableEncodeRecordNames)$' ./internal/checkpoint/` — RED 4/4 on the unfixed writer/finalize/store files; the one reservation-interface assertion was removed for that run (runs/red_ckpt.txt). GREEN after the fix.
- `go test -p 2 -count=1 -run '^(TestSegment_ReserveEncodedWritesNothingAndDiesWithTheLog|TestSegment_ReserveEncodedKeepsTheDPIGuard|TestSegment_CommitEncodedWritesTheSealedSequence)$' ./internal/store/` — new API (would not compile before); PASS
- `go test -p 2 -count=1 -run '^(TestFsck_AnUnsealedDraftClaimIsNamedNotFailed|TestFsck_AClaimNothingExplainsIsStillADefect|TestFsck_AClaimOnAnOrphanArtifactIsTheOrphansFinding)$' ./internal/cli/` — RED on the old fsck.go: the two note rows fail and the defect-guard row passes both ways (runs/red_fsck.txt). GREEN after 3f8f21b0.
- `go test -p 2 -count=1 -run '^(TestMaintenance_RestoreAcceptsAToolRefAGCTombstoneAccountsFor|TestMaintenance_RestoreStillRefusesAToolRefNothingAccountsFor)$' ./internal/store/` — RED: 'store: restored tool reference root 4c4b0dace993 does not resolve' (the F-C49-2 message). GREEN after a8eeb483.
- `go test -p 2 -count=1 -run '^(TestBackupCLI_VerifyJudgesWhatRestoreJudges|TestBackupCLI_RestoreNoteMatchesItsIntegrityReport)$' ./internal/cli/` — RED: verify carried no restore proof and exited 0 on an unrestorable backup; the note lacked what ran. GREEN after 35d3d35a.
- `go test -p 2 -count=1 -run '^TestExpandReportsAMissingObjectAsMissing$' ./internal/mcp/` — RED: reason '...a damaged object is preserved as evidence...' does not contain 'missing'. GREEN after 63f1791f.
- `go test -p 2 -count=1 -run '^(TestGetChunk_AnIndexedObjectGoneEverywhereIsMissingNotPreserved|TestGetChunk_AQuarantinedObjectIsDamagedNotMissing|TestGetChunk_ADirectoryAtTheObjectPathIsDamagedNotAbsent)$' ./internal/store/` — PASS
- `go test -p 2 -count=1 -run '^(TestForeignLockIsJudgedByThisStoresHeartbeat|TestDescribeLockHolderNamesAForeignLock|TestAcquireLockExclusive|TestStaleLockReclaimed|TestLiveLockNotReclaimed|TestAcquireLockRaceWindowIsNotStale)$' ./internal/daemon/` — RED on the old lock.go: the stale-heartbeat subtest got ErrLockHeld. GREEN after 8ff08b11; the existing lock rows are unchanged.
- `go test -p 2 -count=1 -run '^(TestDaemon_LockHeldExitNamesTheHolder|TestFsck_ALockCopiedFromAnotherPathIsNotARunningDaemon|TestDaemon_ExitsZeroWhenLockHeld|TestBackupCLI_RefusesLiveWriterAndDoesNotOpenStore)$' ./internal/cli/` — RED before 8ff08b11 and 7585cbee: empty day log, daemon_running true. PASS after.
- `go test -p 2 -count=1 -run '^(TestFsck_PromptCapturesAnEarlierBuildNeverLinkedReadAsPublished|TestCrossVersion_AStoreTheV020ReleaseWroteReadsClean)$' ./internal/cli/` — The D3 row is RED with ClaimLegacyPrompt disabled (the exact 'stage 1 only' messages) and GREEN after 6a0feb9c. The v0.2.0 fixture row PASSES; it is a guard, and the base also reads that store clean.
- `binaries built from v0.2.0, 301a8e9, 84d25122 and this branch; python gen_store.py / check_store.py (committed under runs/)` — v0.2.0 store: base and candidate both fsck 0, create/verify/restore 0, no LOUD. 301a8e9 store: base fsck 1, restore 1, LOUD unpublished_captures=3 (D3 reproduced); candidate fsck 0, create/verify/restore 0, no unpublished-captures LOUD (runs/crossversion.txt).
- `go test -p 2 -count=1 -timeout=30m ./internal/store/` — 1 FAIL under co-load: TestGC_DeadlineOvershootIsBoundedByTheCheckInterval (252.08 ms vs a 250 ms limit). Re-run alone with go test -p 1 -count=1 -run '^TestGC_DeadlineOvershootIsBoundedByTheCheckInterval$' ./internal/store/: ok (81.9 s). Load artifact, unrelated to this change.
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok (72.4 s)
- `go test -p 2 -count=1 -timeout=30m ./internal/mcp/ ./internal/checkpoint/` — ok mcp 158.2 s, checkpoint 101.1 s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok (331.0 s)
- `go test -p 2 -count=1 -timeout=30m -run '^(TestV3_ObserverSegmentsRespectDPIGuard|TestE2E_CheckpointHookWritesImmutableArtifact|TestE2E_StoreSurvivesProcessRestart|TestV4_O1SpanInstructionFromARealCheckpointFrontier|TestE2E_IdleExitLeavesNoUnsealedEncodeClaim|TestE2EIdleExit|TestDaemonIdleRunsSchedulerWork)$' ./test/e2e/` — ok (66.4 s); go test -list confirmed all 7 names exist
- `go test -p 2 -count=1 -run '^TestV5_GrammarAndPromotionCoexistInFinalize$' ./test/integration/` — ok
- `go test -p 2 -count=1 -run '^(TestGuard_EveryHomeReachingTestPackageIsolatesHome|TestGuard_HomeIsolationScannersSeeEveryShape|TestV1_WriteSetConfinedAcrossFullHookSequence|TestGuard_WriteSetConfinedToQompack)$' ./test/guards/` — ok
- `go test -p 2 -count=1 ./test/docs ; go run ./tools/devtool gen-command-docs --check ; gen-config-docs --check ; gen-mcp-docs --check` — ok; all three generated docs up to date
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — The first run found 2 errcheck findings in the new tests; fixed in faff8831. Re-run of golangci-lint: PASS. nomagic, importgraph, testdeps, bindeps, sleepcheck, runpatterns and docmarkers: PASS (runpatterns and docmarkers re-run after the evidence commit: PASS).
- `go run ./tools/devtool fmt-check ; go vet and GOOS=linux go vet ./internal/checkpoint ./internal/store ./internal/cli ./internal/daemon ./internal/mcp ./test/e2e` — clean

### Criterion changes

- fsck index.segments: an encode record whose sequence the manifest lacks is now a named note in two cases, where before it was a severity-1 defect: (a) a persisted draft (state/draft-<s>.json, or a set-aside .stale.json) holds that segment for that sequence ('unsealed-draft claim'); (b) a checkpoint artifact at that sequence is on disk with no manifest line (the checkpoints row still reports that orphan as a defect). Rationale: the old ordering bug wrote exactly this state after every idle exit. The draft's state file explains it, the checkpoint chain still verifies, and the turns remain in the capture log. No tool can remove it from the append-only log, so as a failure it made every later backup unrestorable with no action available (D45). A claim nothing on disk explains is still a defect (TestFsck_AClaimNothingExplainsIsStillADefect).
- Restore's reader proof: a tool reference whose root a gc tombstone retired is counted in the new RestoreProof.ToolRefsTombstoned and no longer fails the restore. Rationale: fsck's index.tool_use row already reads this exact reference as 'accounted for by a gc tombstone', and restore must agree with fsck (F-C49-2). A reference nothing accounts for still fails (TestMaintenance_RestoreStillRefusesAToolRefNothingAccountsFor).
- backup verify is stricter. It now also restores into a scratch destination and requires restore's reader proof and fsck --seal-check integrity to pass, so a backup it used to certify can now fail. It costs one extra full copy of the store in the system temp directory, removed on success and kept on failure.
- fsck captures and publication, and the daemon's startup publication accounting: an unpublished observe.prompt sidecar is no longer a gap when a prompt_<session>_<turn> record in its session carries the unbound digest ArgsDigest({"prompt": text}), one record per sidecar. It is counted in the new PublicationAudit.LegacyLinkedPrompts. Rationale: builds before the prompt link (dev 301a8e9) published the prompt but never wrote the link; the reference exists (D3). A sidecar no record accounts for is still a gap.
- Lock staleness: a lock that records a different IPC address from this project's is no longer kept live by a dial of that recorded address, nor on POSIX by its pid. It is judged by a dial of this project's own address plus this store's heartbeat (the existing 90 s rule). Locks recording this project's own address are judged exactly as before; the spawn.lock return rule from D27 is untouched.

### Open issues

- Re-run on the fixed candidate (coordinator; real sessions are not allowed in this seat): UAT-03 (idle exit, then fsck and backup/restore), C4.9 (MCP call, idle, then backup verify/restore), C1.7 on a realistic store (MCP calls, compaction, idle), C4.8/UAT-12 with v0.2.0 as the previous build, and the F-UAT03-4 hand-copy probe. The Linux gate (plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh) was not run because the container is stopped; only GOOS=linux vet ran.
- Merge overlap: d18485c4 edits internal/checkpoint/writer.go (Advance's mark call, claimSeq and Begin's fresh-seq line, persistedClaimFloor) and finalize.go (reconcileEncodedSeq, sealSegmentMarks). The pinsckpt seat (F-UAT03-1, empty encoded_segments and pointers) and the intent seat (seedTierOne, original intent) are likely in the same files. internal/mcp/handlers_span.go (refusedObject) may overlap with mcpresp. fsckDaemonLiveness also changes doctor's liveness answer for foreign locks (diag seat).
- Cross-seat semantic notes: for a segment only reserved in a live draft, the separate `qompack mcp` process now reads encoded_once false in the timeline until the draft is sealed (ledger seat, timeline). Doctor's captures.unpublished (D5, diag seat) will count differently if it uses store.AuditPublication, because legacy prompt sidecars are no longer gaps there.
- 00-ARCHITECTURE.md §5.14 still says Advance 'Calls SegmentLog.MarkEncoded'. It now reserves through store.SegmentReservation where the log offers it; the code docs (checkpoint.go, segment.go, writer.go, finalize.go) are updated but the plan text is not. Coordinator's call whether it needs a revision note.
- Not in this seat's list and not fixed: F-UAT05-4 / F-C49-4. After a daemon is killed on Windows, maintenance and MCP retrieval still refuse for about 90 s. With no pid liveness check on Windows, a same-address lock waits out the heartbeat window.
- A hand copy still waits up to 90 s after its heartbeat's last mtime (cp -p preserves it) before a daemon starts. The day log and fsck now name why. Faster recovery would need a new rule, not a new number.

### Needs the owner

- No new budget or bound numbers were introduced. The foreign-lock rule reuses the existing 90 s staleness window; verify's scratch restore and the legacy-prompt match have no thresholds.
- Coordinator acceptance (D33) of the behaviour changes above: (1) backup verify now performs a full scratch restore plus fsck --seal-check (extra IO and a temporary copy under the system temp directory, kept on failure); (2) foreign-lock staleness ignores the recorded address and pid; (3) earlier dev builds' unlinked prompt sidecars read as published; (4) unsealed-draft and orphan-backed encode claims are fsck notes, not defects.

## Independent review

### review:restore: needs-fixes

- **major** `internal/cli/backup.go:202 (proveBackupByScratchRestore); docs/backup.md:23-27` — `backup verify` now restores a full copy of the store into os.MkdirTemp("", "qompack-verify-"), which is $TMPDIR at large. That breaks 00-ARCHITECTURE.md §13 invariant 7, whose write set is exactly five locations and which says "$TMPDIR at large" "must stay out". A failed verify also leaves a full copy of captured prompts and tool output in the shared temp directory, and each retry leaves another. On Linux, where /tmp is often tmpfs, the copy takes RAM. The implementer did list the extra IO as a needs_owner item, but did not mention the invariant.
  - Evidence: backup.go:202 `scratch, err := os.MkdirTemp("", "qompack-verify-")`, and on failure `the scratch restore is kept at %s`. 00-ARCHITECTURE.md:2968-2985: "No writes outside the product write set... What is NOT in the set, and must stay out: ... `$TMPDIR` at large".
  - Fix: Put the scratch destination inside the write set on the store's own filesystem. The best place is <project>/.qompack/tmp/verify-<id>-<rand>/project: backups already skip tmp/, and Restore stages on the same filesystem. Keep the remove-on-success and keep-on-failure behaviour. Update the docs/backup.md paragraph ("under the system temporary directory", "Verify never writes to the source") and the user-guide row to match. Add an assertion to TestBackupCLI_VerifyJudgesWhatRestoreJudges that the scratch path is under .qompack/tmp.
- **major** `internal/daemon/lock.go:212-222 and :273 (lockIsForeign); internal/cli/fsck.go fsckLockForeign` — The code decides a lock is foreign when the full recorded IPC address differs from the one this process resolves. That address is a stand-in for the project path, and on POSIX it also depends on XDG_RUNTIME_DIR, TMPDIR, the uid, and the QOMPACK_IPC_ADDR override (ipc.resolveFor). So a lock that a LIVE daemon of THIS project wrote under a different environment is classed as foreign. The foreign branch skips step 2 (the dial of the recorded address, which would answer) and step 3 (the POSIX pid check) and relies on the heartbeat alone. If a live daemon's heartbeat is older than 90 s (after suspend/resume, where Go's monotonic ticker does not advance, or under a stall), a second process from the other environment reclaims the lock. That other process can be a daemon start, backup create/restore, or fsck --seal-check. The result is two writers. Before this change the recorded-address dial or the pid check kept the lock held. The brief said not to weaken the lock semantics. The existing test even names the case ('a daemon for this store under another spelling heartbeats it') and relies on the heartbeat alone.
  - Evidence: lock.go:212 `if lockIsForeign(info, a) { if ipc.Probe(a, ...) {return false}; return heartbeatStale(...) }`. lockIsForeign compares `info.Addr != a.Path`. resolve.go:120-130 builds the POSIX path from the XDG_RUNTIME_DIR, else os.TempDir()/qompack-<uid>, else the short fallback, and QOMPACK_IPC_ADDR overrides everything.
  - Fix: Judge foreignness by project identity, not by the whole address. For example, treat a lock as foreign only when its recorded address does not carry this root's ipc.ProjectHash12/ProjectHash8 as its endpoint name (the pipe suffix, or the <hash>.sock basename). Better still, add an additive `root` (or root-hash) field to LockInfo, as Owner was added, and compare that, falling back to the hash comparison for older locks. When the identity matches, keep steps 2 and 3 exactly as before. Add a test where the lock records the same project's hash under a different socket directory, a live pid and a stale heartbeat, and assert ErrLockHeld on POSIX.
- **minor** `internal/checkpoint/writer.go:400, :1083-1112 (persistedClaimFloor)` — Every fresh Begin now reads and JSON-unmarshals every state/draft-*.json, including .stale.json files, and copies every segment in the log through Range(0, MaxInt). None of this has a bound or a budget name. Begin also runs in afterSeal inside Finalize, so the cost falls on the PreCompact hook's B-E path. Draft files for ended sessions accumulate, because this change makes idle-exit drafts for ended sessions stay unsealed by design, so the cost grows with project history.
  - Evidence: writer.go:1083 persistedClaimFloor: `os.ReadDir(state)`, then `os.ReadFile` + `json.Unmarshal` of each draft, then `src.Segments.Range(ctx, 0, core.TurnIndex(math.MaxInt))`. finalize.go afterSeal → `w.Begin(ctx, d.session, seq, src)`. No count or bytes limit, and none is listed for the owner.
  - Fix: Compute the floor once in OpenWriter or on the first Begin, keep it in the writer as a high-water mark, and update it in claimSeq, resumeDraft set-aside and noteSeq. Add a segLog accessor for the maximum durably encoded sequence (O(1), tracked during replay and append) instead of copying Range. If a scan stays, decode only the `seq` field with a streaming json.Decoder and name the bound.
- **minor** `internal/store/publication_audit.go:498; internal/store/legacy_prompt.go (legacyPromptRecords, ClaimLegacyPrompt)` — The legacy claim counts any prompt record whose digest is unbound. The current build also writes unbound digests for UNLEASED prompt records: promptDeliveryDigest with obs=="" returns ArgsDigest(promptArgs(prompt)). So a same-session, same-text unleased record can account for a genuine current-build stage-one prompt sidecar gap and hide it. That weakens both fsck's captures and publication rows and the startup LOUD. The comment says 'only an unbound digest can be matched', but the current build writes unbound digests too.
  - Evidence: internal/observer/prompt_delivery.go:114-118: `if obs == "" { digest, _ := store.ArgsDigest(promptArgs(prompt)); return digest }`. legacy_prompt.go legacyPromptRecords counts every prompt record whose ArgsDigest is non-zero.
  - Fix: Narrow what can claim. Count only records that have no Observation and whose turn/TS precede the first observation-bound prompt record of that session. Or make the claim require the sidecar to carry a 301a8e9-only shape, for example no observation_id binding. Also require the record's Root to match the sidecar's prompt body hash where both are available. Add a test where an unleased current-build record plus a leased stage-one sidecar with the same text still counts as a gap.
- **nit** `internal/checkpoint/finalize.go:263-270; plans/00-ARCHITECTURE.md §5.14` — The doc paragraph above sealSegmentMarks still says 'The marks were appended by Advance, during idle windows and without a sync', which is no longer true for the store's own log, which reserves them. The architecture's §5.14 still says Advance 'Calls SegmentLog.MarkEncoded'. The implementer listed the second as open.
  - Evidence: finalize.go:263 is unchanged, while the reserving branch was added below it.
  - Fix: Qualify the paragraph so it applies only to a non-reserving log. Route the §5.14 revision note to the coordinator.
- **nit** `commits 9a53a1a5, b99632ba, faff8831, b654ec87, 266c2f8e` — Five of the 13 commits have no `Refs: V6-VERIFY, C1.7/C4.8` footer, and 9a53a1a5 and faff8831 have no body at all. There are no attribution trailers, which is correct.
  - Evidence: `git log --format='%h %(trailers:only)' 84d2512..HEAD` shows empty trailers for those five.
  - Fix: Add the Refs footer when the branch is next rewritten or squashed on integration.

## Fix seat (review resolution) — status `done`, head `d1d3f60a16f45bb3b372dca5cb1d7a4d12b99198`

### Root cause

I checked all four reviewer findings against the code and every one was correct. Each got a test that failed first, then a fix. (1) backup verify restored a full copy of the store with os.MkdirTemp("", ...), which is $TMPDIR at large. That breaks 00-ARCHITECTURE.md §13 invariant 7. A failed verify also left the copy there. (2) The lockIsForeign added in 8ff08b11 compared the whole recorded IPC address. On POSIX that address also depends on XDG_RUNTIME_DIR, TMPDIR and the uid, and QOMPACK_IPC_ADDR overrides it everywhere. So a live daemon of the same project that wrote its lock under another environment was judged by its heartbeat alone. Steps 2 and 3 were skipped, and a heartbeat older than 90 s (after a suspend or a stall) let a second writer take the lock. Red: TestALockThisProjectWroteElsewhereKeepsTheFullProtocol and TestFsck_ALockThisProjectWroteAtAnotherAddressIsItsDaemon. (3) persistedClaimFloor, from d18485c4, ran on every fresh Begin. Each time it read and fully decoded every draft file and copied the whole segment log through Range. Begin also runs in Finalize's afterSeal, so this cost landed on the B-E path and grew with project history. Red: TestBeginScansPersistedDraftsOncePerWriter (2 scans, not 1). (4) The legacy prompt claim from 6a0feb9c accepted any record with an unbound digest. The current build writes that same digest for unleased deliveries (promptDeliveryDigest when obs is empty; ingest runs unleased jobs when j.leased is false). So a same-session, same-text unleased record could hide a real stage-1 gap. Red: the subtest "a current build's unleased record does not account for a current build's gap".

### Summary

## Review resolution

All four findings verified as correct. Each is fixed, with a test that failed before its fix. Red logs are in plans/sdd/V6-closeout/w13-restore/runs/fix-red-*.txt.

**Finding 1 (major): verify's scratch restore used $TMPDIR — fixed (48a4f49e).**
- Scratch is now `<root>/.qompack/tmp/verify-<id>-<16 hex>/project`, created by the new verifyScratchDir in internal/cli/backup.go.
- That location is inside the write set, on the store's own filesystem, excluded from every backup (backupSkipDirs), and written under the writer lease verify already holds.
- Removed on success, kept on failure, as before.
- TestBackupCLI_VerifyJudgesWhatRestoreJudges now points TMP/TEMP/TMPDIR at an empty directory. It asserts that verify leaves nothing there, pass or fail, and that the scratch path is under .qompack/tmp.
- docs/backup.md, docs/troubleshooting.md and the docs/user-guide.md row are updated.

**Finding 2 (major): a lock was "foreign" whenever its address differed — fixed (1f9c1110).**
- Foreignness now means "written for another project":
  - AcquireLock records an additive `LockInfo.Root` (like Owner).
  - The new exported `daemon.LockIsForeign(info, root)` compares that root's store with this one: same normalized path, or else os.SameFile on the two .qompack directories.
  - A lock without a root is foreign only if its address is named for another project's hash. The new `ipc.EndpointNamesProject` parses all three endpoint shapes of §2.4.
  - An address that names no project (a QOMPACK_IPC_ADDR override) is not foreign.
- A lock of the same project keeps steps 2 and 3 of the lock protocol (the dial and the POSIX pid check) exactly as before.
- fsck's daemon row and the losing daemon's log line use the same rule. The log line names the recorded root when there is one.
- New tests: TestALockThisProjectWroteElsewhereKeepsTheFullProtocol (includes the reviewer's POSIX case: same hash in another socket dir, live pid, stale heartbeat, ErrLockHeld; on Windows, which has no pid probe, step 4 decides), TestALockNamesItsRootAndIsJudgedByIt, TestEndpointNamesProject_ReadsEveryResolvedShape and TestFsck_ALockThisProjectWroteAtAnotherAddressIsItsDaemon.

**Finding 3 (minor): claim-floor scan on every Begin — fixed (56d73b27).**
- loadClaimFloor folds the floor into issuedSeq on the writer's first fresh Begin only. An incomplete scan is retried.
- Each draft is decoded only up to its "seq" field, streaming through paths.OpenShared.
- The segment log answers from a new O(1) `store.SegmentEncodeFloor`, raised on replay and on every appended encode record. Range is used only for logs without it (test doubles).
- Tests: TestBeginScansPersistedDraftsOncePerWriter and TestSegment_DurableEncodeFloorIsTheHighestRecordedSeq.

**Finding 4 (minor): legacy prompt claim too broad — fixed (d6f1b2ad).**
- `LegacyPromptRecords` now counts only records published before the session's first observation-bound prompt, i.e. one with an intent in index/observations.jsonl. 301a8e9 never wrote that file, and prompt records gained the binding on 2026-09-21.
- If the intents cannot be read completely, nothing claims.
- fsck's captures row now gets its records from a read-only open of the store (`store.LegacyPromptCounter`, exposed on readOnlyStore). It no longer uses its own tool_use scan, so the captures and publication rows apply one rule to one record set.
- The reviewer's root-hash suggestion was not used. Both builds' records carry the same root for the same text, so it cannot tell them apart.
- Two new subtests, including a session resumed across the upgrade, which still claims correctly.

## Commands and results
Six seats were sharing the machine; I used -p 2 throughout. Nothing failed, so no wall-clock re-runs were needed.
- `go test -p 2 -count=1 -timeout=30m` on each touched package, once, one after another: ipc 18 s, checkpoint 68 s, cli 64 s, daemon 286 s, store 190 s. All ok.
- `go test -p 2 -count=1 -timeout=15m -v -run '^TestE2E_IdleExitLeavesNoUnsealedEncodeClaim$' ./test/e2e/` passed in 28 s. It runs the real binary's backup verify.
- `go test ./test/docs` ok. gen-command-docs, gen-config-docs and gen-mcp-docs `--check` report up to date.
- fmt-check passes (after 552dce14). go vet passes on Windows and GOOS=linux for cli, daemon, ipc, store and checkpoint.
- Lint with the permitted sub-checks all passes. golangci-lint passed after 38f6708c fixed an errcheck finding (an unchecked type assertion in my new test).
- The evidence summary is runs/fix-green.txt.

## Criterion changes
None. No assertion was loosened. The test that used to point TMP at a test dir only to hold the kept scratch copy now asserts the stronger property: nothing is written to the system temp directory.

### Commits

- 48a4f49e fix(backup): keep verify's scratch restore inside the store
- 1f9c1110 fix(daemon): judge a foreign lock by project, not by address
- 56d73b27 fix(checkpoint): scan for claimed seqs once per writer
- d6f1b2ad fix(store): claim legacy prompts only before a session's binding
- 552dce14 test(cli): gofumpt the new lock and prompt test fixtures
- 38f6708c test(store): check the reservation assertion in the floor row
- d1d3f60a test(evidence): record the w13-restore review-round runs

### Tests

- `go test -p 2 -count=1 -timeout=30m ./internal/ipc/` — ok 18.2s
- `go test -p 2 -count=1 -timeout=30m ./internal/checkpoint/` — ok 68.5s
- `go test -p 2 -count=1 -timeout=30m ./internal/cli/` — ok 63.5s
- `go test -p 2 -count=1 -timeout=30m ./internal/daemon/` — ok 286.4s
- `go test -p 2 -count=1 -timeout=30m ./internal/store/` — ok 189.8s
- `go test -p 2 -count=1 -timeout=15m -v -run '^TestE2E_IdleExitLeavesNoUnsealedEncodeClaim$' ./test/e2e/` — PASS 28.4s
- `go test -p 2 -count=1 ./test/docs` — ok
- `go run ./tools/devtool fmt-check` — PASS (after 552dce14)
- `go vet (windows and GOOS=linux) ./internal/cli/ ./internal/daemon/ ./internal/ipc/ ./internal/store/ ./internal/checkpoint/` — PASS
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all PASS; golangci-lint needed one errcheck fix (38f6708c) and then passed
- `go run ./tools/devtool gen-command-docs --check / gen-config-docs --check / gen-mcp-docs --check` — up to date
- `red runs before each fix: -run 'TestBackupCLI_VerifyJudgesWhatRestoreJudges', 'TestALockThisProjectWroteElsewhereKeepsTheFullProtocol|TestForeignLockIsJudgedByThisStoresHeartbeat' (with a temporary uncommitted shim), 'TestFsck_ALockThisProjectWroteAtAnotherAddressIsItsDaemon|TestFsck_ALockCopiedFromAnotherPathIsNotARunningDaemon', 'TestBeginScansPersistedDraftsOncePerWriter', 'TestFsck_PromptCapturesAnEarlierBuildNeverLinkedReadAsPublished'` — each FAILED as expected before its fix (runs/fix-red-*.txt); TestSegment_DurableEncodeFloorIsTheHighestRecordedSeq was red only as a compile failure (the method did not exist)

### Open issues

- F-UAT03-4 live re-run: the live-lane hand copy was made with cp, so the copied run/daemon.hb got a fresh mtime. On Windows the copy's lock is therefore held for up to 90 s after the copy, and the daemon now logs the named reason instead of exiting silently. A re-run that starts the daemon within 90 s of the copy will still see no daemon, now with a log line. I considered reclaiming a lock whose recorded root is another store immediately and rejected it: a daemon in another mount namespace (WSL /mnt/c vs C:\) serving the same store records a root this process cannot stat, and immediate reclaim would put two writers on the store.
- Finding 4 residual: the claim cannot distinguish a session where the current build delivered every prompt unleased and the first leased prompt is itself a stage-1 gap whose words match an earlier unleased record. Nothing in the durable record tells these apart. Only the delivery-ack journal could, and the store cannot read it. The residual is documented in LegacyPromptRecords' comment.
- 00-ARCHITECTURE.md §2.4 still describes daemon.lock as 'pid + start time + pipe path'. That line omits Owner (added earlier) and Root (added here). I did not edit the plan doc; docs/troubleshooting.md now names the root field.
- doctor.go reads LockInfo but has no foreign-lock logic of its own. It belongs to the diag seat; I did not touch it.

### Needs the owner

- No new budget or bound numbers. verifyScratchRandBytes = 8 only sets the width of the scratch directory's random suffix, matching store's maintStagingRoot. The foreign-lock rule reuses the existing 90 s staleness window. The draft seq scan and the legacy-prompt rule have no thresholds.
- D33 acceptance of the behaviour changes, as revised in this round: (1) backup verify now does a full scratch restore plus fsck --seal-check, inside <project>/.qompack/tmp/verify-<id>-<random>/project (not the system temp directory). It is removed on success and kept on failure, and the error names it. (2) daemon.lock gains an additive `root` field. A lock written for another project is judged by this store's heartbeat alone (90 s), meaning its recorded root is another store, or, with no root, its address is named for another project's hash. A lock of this project at any address keeps the dial and the POSIX pid check. (3) Prompt sidecars that earlier dev builds wrote and never linked read as published only when a matching record comes before the session's first observation-bound prompt. When the observation intents cannot be read completely, nothing claims. (4) Unchanged from the implementer: unsealed-draft and orphan-backed encode claims are fsck notes, not defects.

## Independent verification of the fix seat: sound

Every non-nit review finding was resolved or soundly rebutted; nothing new was introduced.


