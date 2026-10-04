# Wave 20 sessionend (candidate 8 pre-freeze audit fixes, D61(a))

Branch `closeout/w20-sessionend`. Workflow `wf_a18b8846-180`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results, in the order the seats ran.

## Assigned audit findings

- **minor** `plans/sdd/V6-closeout/live/report-c7.md:314-315, 703-704; docs/uat.md UAT-09 Result; rerun-c7/UAT-09/notes.txt:44-47,86-91`: UAT-09 O-1 has no disposition anywhere. In a session that ended after a /compact turn, SessionEnd never reached the daemon: the daemon ended the session as abandoned, no client spool existed and no flush was recorded. D59 and D60 do not rule on it, it is not in mkrecheck8.py, and D53(i) cannot see it, because the host's stream carries no SessionEnd pair. Candidate 3's lane reported the related abandoned-session-during-/compact behaviour (live/report.md:63,84), and it was not traced either.

## impl:sessionend: status `done`, head `6a66300ed05ba5bbcd1ae8c2a37138cded925b42`

### Root cause

Not a lost SessionEnd. With idleExitSeconds=30, T9's 34.5 s reply sent no hook. The idle tick's EndAbandoned took that silence for a dead client and logged the WARN at silentMs=30099 while the session was still live. T9's Stop then revived the session through registry.Touch, and SessionEnd's flush ended it normally about 1 s later; the observer's segment-2 close and the 'observer: gc' line prove it. End() does not log, sessions.jsonl end is the newest root ts by design, and a live delivery leaves no client spool. Those three facts were misread as non-delivery.

### Summary

No product code changed. I rebutted UAT-09 O-1 with evidence. The answer is none of (a), (b) or (c): SessionEnd did reach the daemon. The abandoned-session WARN was the documented silence sweep firing during a long final turn that sent no hook, and the session was then revived and ended normally.

Timeline, rebuilt from plans/sdd/V6-closeout/live/rerun-c7/UAT-09/:
- The host started at 1790969044146 (19:24:04.146Z) and ran 106.988 s (session1/meta.json), so it exited at about 19:25:51.13Z.
- T9's UserPromptSubmit came at 19:25:15.523. index_sessions.jsonl's end, 1790969115523, is that timestamp; End is the newest root record's ts by design (internal/store/flush.go:176-186), not the SessionEnd time.
- The model then replied for 34.5 s with no tool call (stream.jsonl: result duration_ms 34546, and no hook between lines 139 and 166).
- With idleExitSeconds=30, the idle tick's sweep (idleExitDue -> EndAbandoned) logged the WARN at 19:25:45.624, silentMs=30099, while the session was still live.
- T9's Stop followed (stream lines 166-167). acceptHotPathEvent calls registry.Touch, which revives a session the sweep ended (registry.go `case !s.Live && s.abandoned`).
- SessionEnd's flush was answered once durable (handleFlush runs the end on its own goroutine, C1.15). That is why the GC line (19:25:51.205) is later than the host's exit.

Proof that OnSessionEnd ran:
- (1) index_segments.jsonl closes segment 2 at ets 1790969151018 (19:25:51.018Z) with feat {gap_seconds 1.021, lexical_cohesion, path_jaccard, todo_transition, tool_shift} and no prob_changepoint. The only Segments().Close call is in OnSessionEnd (internal/observer/session.go:363). The scheduler's close (internal/daemon/scheduler_frontier.go:154-170) always writes prob_changepoint, as segment 1 does.
- (2) The day log's `observer: gc scanned=27` at 19:25:51.205Z is written only by OnSessionEnd (session.go:398).
- (3) There is no client spool because the flush was delivered live, and stderr.txt is empty because no hook failed.
- (4) End() does not log, which explains why the day log has no line for the real end.

The candidate-3 report (live/report.md:63,84: a 32 s /compact at idleExitSeconds=30) is the case docs/troubleshooting.md:1149-1166 already documents and cites. It was traced.

What I added:
- (1) A row in internal/daemon/session_abandon_revive_test.go: TestIdleExit_AnAbandonedSessionRevivedByItsStopStillEndsThroughSessionEnd. It replays the UAT timeline on a fake clock through the real handlers and idleExitDue: prompt; +30.099 s, the sweep ends the session (abandoned, EndedTS-lastPrompt = 30099); +4.4 s, observe.stop revives it and clears the zero-live countdown; +1.021 s, a Reply flush runs the wired observer's SessionEnd exactly once, and the session is ended by its own end (abandoned=false, EndedTS = the flush's time); after one more window idleExitDue is true. Because nothing was broken, it is a green characterization row and not a red-first one. No pass/fail depends on wall time: the clock is faked, the Reply flush waits on the end's own answer, and the settle bound only affects whether the D-C1.1 unsettled counter moves, which I deliberately do not assert.
- (2) One docs/cannot-do.md entry, "A quiet live session is counted as ended until its next hook", placed after the D35(c) entry. It covers the limit (no hook while the model writes a reply with no tool call, or while it compacts), why the sweep exists, and what Qompack does instead (bookkeeping only; nothing captured is lost; the next hook revives the session; its own SessionEnd ends it in full; if the silence lasts another window the daemon exits and the next hook respawns it; a flush that arrives while the daemon is stopping is the D35(c) case). It cites the UAT-09 evidence.

I made no upstream-issues entry because this is not host behaviour, and recording it as such would state a false cause. The finding's proposed fix ("no spool means the hook never ran", "likely host behaviour") is wrong, and I did not adopt it.

Candidate 8 live re-check, which I am handing to the coordinator: no new diagnostic is needed. If you want one, the check is: in any session whose day log shows `ending abandoned session`, confirm SessionEnd's arrival by finding the session's final segment close (its feat has no prob_changepoint) and an `observer: gc` line after the host's last Stop. Do not use client-spool absence or sessions.jsonl `end` as evidence.

### Commits

- af6c95dd test(daemon): replay uat-09's abandoned then revived session end
- 6a66300e docs(cannot-do): quiet live sessions count as ended until a hook

### Findings resolution

- **rebutted**: complete: UAT-09 O-1 has no disposition; SessionEnd never reached the daemon after a /compact turn (report-c7.md:314-315, 703-704; notes.txt:44-47, 86-91; live/report.md:63,84)
  - The premise is false. SessionEnd did reach the daemon and OnSessionEnd ran: segment 2 closed at 19:25:51.018Z with the observer's feature set (session.go:363 is the only Close caller; the scheduler's close always carries prob_changepoint), and 'observer: gc' at 19:25:51.205Z comes only from OnSessionEnd (session.go:398). The WARN (silentMs=30099 at 19:25:45.624) is the documented silence sweep firing during T9's 34.5 s reply with no tool call at idleExitSeconds=30. Stop revived the session through Touch, and SessionEnd ended it about 1 s later. No spool exists because delivery was live. sessions.jsonl end equals the last prompt because End is the newest root ts by design (store/flush.go:176-186). Candidate 3's /compact case is already documented in troubleshooting.md:1149-1166. Disposition: by design, documented. I added a characterization row pinning the abandon -> revive -> SessionEnd path and a cannot-do entry. The ledger still needs a disposition line, and the observation wording should be corrected; see needs_owner.

### Tests

- `go test -p 1 ./internal/daemon -run '^TestIdleExit_AnAbandonedSessionRevivedByItsStopStillEndsThroughSessionEnd$' -count=20`: ok (15.4s); green on 738d67c7 by design (characterization row, no defect to make red)
- `go test -p 1 -race ./internal/daemon -run '^TestIdleExit_AnAbandonedSessionRevivedByItsStopStillEndsThroughSessionEnd$' -count=3`: ok (3.8s)
- `go test -p 1 -timeout=30m ./internal/daemon -count=1`: ok (718.7s)
- `go test -p 1 ./test/docs ./test/guards -count=1`: ok (docs 3.6s, guards 51.4s)
- `GOOS=windows|linux|darwin go vet ./internal/daemon`: clean on all three
- `go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/daemon`: exit 0, no findings
- `go run ./tools/devtool fmt-check`: exit 0
- `go run ./tools/devtool lint --only=docmarkers,runpatterns`: PASS docmarkers, PASS runpatterns

### Open issues

- The observation is misworded wherever it is recorded, in files I do not own: docs/uat.md:1184 ('the recording session's SessionEnd never reached the daemon'), plans/sdd/V6-closeout/live/report-c7.md:217 and :314-315, and rerun-c7/UAT-09/notes.txt:44-47 and :86-91. Suggested replacement: 'the abandoned-session WARN fired during T9's 34.5 s reply with no tool call (idleExitSeconds=30); the turn's Stop revived the session and its SessionEnd ended it at 19:25:51 (segment 2 close, observer: gc)'.
- docs/troubleshooting.md section 7's abandoned-session entry names only a long /compact as the hookless step. A long reply with no tool call is a second observed shape. That file is shared and my task named no section of it, so I left it unchanged. The cannot-do entry states both shapes.
- Design note, not a defect: the sweep cannot see that a turn is open (a UserPromptSubmit with no Stop yet). Exempting open turns would bring back the orphaned-daemon problem for a client killed mid-turn. At the default of 1800 s the WARN needs half an hour of silence.

### Needs owner

- Record a ledger disposition for UAT-09 O-1: by design and documented (abandoned-session sweep at idleExitSeconds=30 during a 34.5 s reply with no tool call; SessionEnd was delivered and ended the session), evidenced by commit af6c95dd's row and the store files above. It does not count against D53(i): no hook failed.
- Candidate 8 live re-check: no new row is required. If one is added, the check is to confirm SessionEnd by finding the session's final segment close (feat without prob_changepoint) and an 'observer: gc' line after the host's last Stop, not by client-spool absence or sessions.jsonl end.
- No new constants were introduced (the test's idleExitSeconds=30 is the UAT-09 lane's QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS, named in the test).

## review:sessionend:0:r1: verdict `sound`, 0 finding(s)


## verify:sessionend: verdict `sound`, 0 finding(s)


