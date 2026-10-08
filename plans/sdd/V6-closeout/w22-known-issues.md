# Candidate 8 known issues (draft for release step 2)

Under D66(d) and D67(o), these are the minors wave 22 did not close. They come from the wave 22 seat
reports (`plans/sdd/V6-closeout/w22-*/report.md` on closeout/integration). Release step 2 copies the
user-facing items into the Known issues sections of CHANGELOG.md and docs/release-notes/v0.3.0.md, in
the docs-only descendant of the frozen candidate (D58(e)). It deletes the interim sentence and its
`releaseInterimMarkers` entries in the same commit. Anything the candidate 8 nights or the live
re-check add is appended here first.

## User-facing

1. **Idle windows across two daemon idle exits.** Windows can be left open but quiet past
   `runtime.daemon.idleExitSeconds` (30 minutes by default) through two daemon idle exits, each
   followed by a new window before the old ones send a hook. The second such start then fails
   session_start.fires and degrades the project to passive recording, until a later new session finds
   a session-end marker. It cannot happen once any session of the project has ended or compacted.
   (contract #8 residual; docs/troubleshooting.md §1.)
2. **Fork decisions on a score tie.** A forked session's checkpoint carries its parent's decisions. On
   a score tie, when the rehydration budget runs short, a parent decision can be kept ahead of the
   fork's own. The cut decision is still named in `dropped()`, with its `why()` route. (w15carry.)
3. **Path-keyed drops in projects with deny or ask rules.** In a project with a Read deny or ask rule,
   a rehydration build makes at most 64 host judgements of path-keyed checkpoint drops, and the drops
   a summary or a reason names are judged first. Drops past that bound show as "(path withheld)" in
   section 7 and in `dropped()`, and a section 6 summary that names one of them may be withheld too.
   This over-withholds; it never shows a refused path. (rehydrate #28, D71(d).)
4. **Rehydration build cost.** A rehydration build with no checkpoint drops costs about 1.7× candidate
   7's, about 2 ms more, because of the D63 summary whitelist. That is far inside the 5 s compaction
   budget. (rehydrate #33.)
5. **Hook warning lines have no location.** A hook's "invalid configuration value" line in the day log
   does not say which file, variable or flag set the value. The daemon's start line and any command's
   warn line do, under `location=`. (config.)
6. **One log line under runtime.mode off.** With `runtime.mode` off in config.json, a hook whose read of
   its delivery fails still appends one hook-quiet line naming the read error, where `.qompack/logs`
   exists. It stops once state.bin also says off. (config.)
7. **First compaction after a restart reads a large paste whole.** After a daemon restart, the first
   compaction of a session whose newest prompt is a very large paste reads that prompt once in full to
   derive current work. (cliwork #25.)
8. **A slow PreCompact can be sealed twice.** If a PreCompact's reply misses the hook's 15 s deadline
   and the spooled copy is replayed before the slow live seal finishes, that compaction is sealed
   twice. The daemon never consumes the copy before a seal succeeds, so the compaction always keeps a
   checkpoint. (redeliver.)
9. **A replayed SessionStart marks its session live.** A SessionStart replayed from a hook's spool, for
   a session that had ended or that this daemon never saw, marks that session live. A later replayed
   delivery of that session can then bind an unbound scheduler to it until the live session's next
   SessionStart. The daemon's idle exit also waits for that session's silence timeout. (redeliver
   route residual.)
10. **A lost segment close.** A segment close owed by a delivery whose first run was cut is held only in
    memory. If that run, its Stop-drain replay and a replay after a restart's bind are all cut, the
    close is never made, and the span stays in the following segment, encoded at a coarser boundary.
    (D67(b).)
11. **One scheduler account per project.** The scheduler keeps a single token account per project,
    bound to one session. Another concurrent session's tool use that calls for a segment close closes
    the bound session's segment and counts its tokens there. This belongs in docs/cannot-do.md, D67(b).
12. **Duplicate daemon start on a busy listener.** Already documented (docs/troubleshooting.md, D61(c)):
    a hook, command or MCP client whose connect is cut short against a live but busy listener may spawn
    a duplicate daemon. The duplicate loses daemon.lock and exits. On Linux, a full listen queue is a
    fast connect failure, documented in 9e4c3e0c.
13. **Recorded corpus not exercised.** The recorded-corpus replay tier (C3.8) is not exercised in 0.3.0.
    (D67(g).)
14. **macOS assumes a case-insensitive volume.** On macOS, Qompack assumes the default case-insensitive
    volume. (D67(m).)
15. **A rooted path glued inside one path value.** Inside a single path-named tool argument, a rooted
    path glued after `+ # ) ] } ! ^` is not judged separately, so the rehydration block can show it.
    docs/security.md documents this residual. (w23-docs finding 5.)
16. **Unusual spellings of a path inside a tool argument.** The rehydration block judges each path in a
    tool argument, but a few rare spellings can still show a path outside the project or one a Read rule
    refuses:
    - a truncated `~[name`;
    - a home directory named by a login holding `@`, `$` or a non-ASCII letter;
    - a Windows `%VAR%` whose name is not an identifier;
    - look-alike Unicode slashes or dots (`／`, `∖`, `．`);
    - a non-canonical spelling (`a/./b`) of a refused path whose file name is under 3 bytes.
    (D72(a).)

## Test-only residuals (ledger only, not release notes)

- Hook-path connect budget: eleven internal/cli rows need a real named-pipe connect inside the 250 ms
  hook connect floor. TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound is intermittent
  under co-load. (cliwork FR1-2.)
- Drain line deadline: one-shot drain rows apply drainLineDeadline (5 s) to a real dispatch, and a few
  rows that drain through a running daemon do the same. (rows.)
- Wall-clock margins of about 1.3 s to 4.8 s remain in spawn_lock_test.go, spawn_home_test.go:91 and
  spawn_claim_release_test.go real-listener rows. (spawn.)
- internal/daemon's drain pass-cost row costs 35-60 s per package run, and is kept at full scale.
  (rows #38/#83.)
- On a Windows machine whose TEMP is longer than about 40 characters, four daemon rehydrate rows build
  previews wider than the store's width. (rehydrate.)
- TestCmdMCPRetryIsCancellable and TestServerCloseWithLiveConnection rely on product timers of 150 ms
  and 2 s. (clock.)
- On a slow windows-latest runner, ci.yml's Windows test leg (-count=2, 60m per binary) can kill
  internal/daemon at its timeout while it is still progressing, and drain rows such as
  TestDeliveryOrder_ARequestedDrainCutShortByItsBudgetIsRequestedAgain can miss their bound in the
  same run. Not a hang (D70(b)). After the release: a larger budget or one pass on that leg. The drain
  row's own red is a deterministic strand, not the budget (D73(2)): after the release its stalled
  attempt calls the real dispatch through withoutLineDeadline, as must
  TestDeliveryOrder_ARequestedPassFinishesALineSlowerThanItsBudget's.
- TestPromptWarning_SlowDurableAcceptIsLateForTheClient does not join the late reply call before its
  next prompt, so it fails when that prompt's reply call takes the session lock first (D70(b)). After
  the release: promptWG.Wait() before the next prompt.
- Each UserPromptSubmit delivery now rewrites state/history.json with an fsync (contract r2's WentOn),
  off the hook reply path but inside the session's ordering gate and under historyMu (D71(d)).
- capWentOn trims an oversized went_on map quadratically; only a hand-edited history.json can reach it
  (Qompack writes at most 16 entries) (D71(d)).
- TestFault_DaemonKilledMidIngest waits for a daemon with dials only, so a recovery session-start that
  meets an orphan spawn claim (a burst hook's lazy duplicate, D35(a), D61(c)) and spawns nothing reads as
  'recording stopped silently' (D73(1)). After the release: wait out the claim and send the next hook.
- Unverified lead (D73(b)), since found real and moved out of this list: see "Released-product
  residual awaiting a ruling" below.
- TestPreCompactSettle_ReplaysAgainOnceALiveCopyAheadOfItPublishes does not wait for the live worker to own
  the delivery, so on a loaded runner its fixture-sanity count reads 0 while the product is correct (D75(c)).
  After the release: settleGate signals when the held run starts, and the hook waits for it.
- ci.yml's reconciliation prints only the tail of a crashed test binary's output and uploads no test JSON,
  so a crash's cause line is lost (D75(c)). After the release: keep the head too, and upload test.json on
  failure.
- After the release, ci.yml 37746311073 on develop fff45a45 (code identical to the tag's) had two test-defect
  reds (D81(a)). Job cover: TestPromptWarning_SlowDurableAcceptIsLateForTheClient, the missing join above
  (D70(b)). Job timing (windows-latest): TestGC_DeadlineTruncatesAndResumes at
  internal/store/gc_test.go:475 priced its budget at 4x one control mark, the binary's first and coldest,
  so the budget outlasted the judged pass's last check at object 512; the collector is correct. Neither
  blocks 0.3.0. Fixed for 0.3.1 on fix/v031-flakes (c7f1dd38, 352aec9b: the budget is priced from the
  fastest of four marks, the assertion is unchanged).

## Added by candidate 8's live re-check (D76)

17. **/qompack:status names a 'last decision' it does not show.** The command's description (and the
    generated docs/commands.md) list 'last decision', but neither the status page nor `status --json`
    carries one. `/qompack:why <decision-id>` explains a recorded decision. (F-C8-C45-1, D76(b).)
18. **A session end sweeps an unindexed object the startup accounting reported.** After a crash that left
    an object written but not indexed, the next session end's garbage collection deletes that object
    before an operator can audit it. No capture content is lost (the capture keeps its bytes inline), and
    `qompack fsck` still names the gap. (F-C8-C16-1, D76(c).)
- Diagnostic only: checkpoint.decision_read_error counts a by-design read of an elimination node's empty
  root, so it reads above 0 on healthy stores (D76(d)). After the release: skip those nodes.
19. **A capture can be dropped when hook processes share an id while the daemon is behind.** On Windows a
    new hook process can reuse an earlier one's process id and append to the same fallback spool file. Once
    that file reaches its 64 MiB cap while it waits to be consumed, a capture whose hook could not reach the
    daemon is dropped, and LOUD.log says so. (D78(c).)
- The C1.16 rig runs every hook in one process, so they share one spool file and its cap (D78(b)); after the
  release it gives each hook its own spool identity. An unprovable Read target is persisted nowhere, not as
  unavailable (D78(d)).

## Released-product residual awaiting a ruling

Not a test-only residual and not in the known issues above. D81(c)(4) found D73(b)'s lead real in
0.3.0: a PreCompact settle (`internal/daemon/precompact_settle.go`) could leave ring-held, WAL-only and
predecessor-leased captures out of its drop report. 0.3.1 names every capture it leaves, and the
summary names no cause it cannot know. No ledger row yet rules whether 0.3.0 discloses it as known
issue 20 in CHANGELOG.md and the release notes or keeps it ledger-only with a reason. That ruling is
the coordinator's (C6.4 review round 3, finding 3.2).
