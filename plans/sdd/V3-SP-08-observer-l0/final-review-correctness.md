# SP-08 final whole-branch review — correctness & concurrency lens

Range: 1466b73..cd02d5e (`feat/sp08-observer-l0`, 8 commits). All reads at the pinned HEAD SHA
(`git show cd02d5e...:path`); no working-tree reads, no mutations, no test runs (concurrent editing
was in progress; the implementer reports carry the run evidence).

## What holds up (verified against the diff, not the reports)

- **Locking discipline (decision 9).** Every entry point is `session() → st.mu.Lock()`; `o.mu` is
  held only for the map lookup/insert and the state-file write; `persistState` copies the map under
  `o.mu`, snapshots each session under its own lock with `o.mu` free, and re-takes `o.mu` only for
  the write — no order inversion anywhere. `sketchMu` is taken only inside a session lock and
  around `saveSketches`, closing the real gap decision 9's premise missed (per-project sketches).
  The OnSessionEnd early release after step 4 is the sanctioned deviation (T6 ruling 1) and is
  genuinely required: the plan's literal step-7 release would self-deadlock in `snapshotSession`.
- **Supersession end-to-end.** `MarkSuperseded(older, by=rec.ID)` matches the store contract
  (store.go:41-42); filters (path, ephemeral both sides, class equality, `p.TS <= rec.TS`,
  zero-root skip, self-skip) are each justified; `marked[0]` → `ObservedTool.Supersedes` and the
  tail's hand-emitted edges both run superseded → superseding, matching `BuildToolUse`'s D-1 edge
  exactly (builders.go:216-224). Newest-first ordering claim of `ToolUsesByPath` was verified in
  the pre-flight ledger and the loop is order-independent anyway.
- **Verbatim guarantees.** `verbatimOptions()` (empty non-nil Strip + MinHash off + KeepRaw)
  matches the amendment's store semantics; the e2e restart test reads the prompt bytes back through
  a fresh `store.Open` with volatile substrings intact. The subagent capture path stores the hash
  list before/independently of the summary, and the `SubagentCaptureID`/record/node/blob all use
  the pre-increment turn consistently.
- **Mode gating incl. amendment (d).** `o.mode()` is consulted at exactly one site (prompt.go step
  9); daemon-side, `handleObservePrompt` calls the seam under `MayRecord()` and attaches its Output
  under `MayAct()` only (handlers.go:416-455) — the two-gate shape the ruling mandated. ModePassive
  never suppresses a write in the observer.
- **Daemon wiring.** `WireObserver(&opts)` open-when-nil assigns Store/Graph/Sketches back onto
  `*Options` so `New` shares the instances; raw sketch pointers deliberately bypass
  `SketchSet.dirty` and the SessionEnd write is the one that happens; `RegisterObserverIdleWork`
  uses the single guarded assertion; RefreshStaleness lives in the wrapped SessionStart seam,
  gated off compact/clear, nil-tolerant, Warn-only — exactly where the plan puts it.
- **Error policy.** Every entry point returns an error only for `ctx.Err()`; every I/O failure goes
  through `soft` with a distinct stage; prompt/stop Put failures still advance the turn (the right
  trade, argued in place).

## Findings

### F1 (Important, plan-mandated conflict) — the prompt→assistant consumes edge dangles forever; §4.4's backward-slice path from tool uses to the prompt does not exist

`dag.BuildUserPrompt` links `userprompt:T --consumes--> assistant:T` (builders.go:355-366), i.e. it
pairs the prompt with the assistant node **of the same turn index**. But decision 4 has
`OnUserPrompt` record at `Turn` then increment (prompt.go step 7), and `OnToolUse` records at the
current turn — so in the normal alternating flow prompts occupy turns 0,2,4,… while every
`AssistantNode` ever minted (only `BuildToolUse` mints them — verified: builders.go:157 is the sole
production minter) sits at turns 1,3,5,…. The edge `userprompt:0 → assistant:0` is emitted into a
node that no path ever creates. Legal under D-6, but permanently dangling: a backward slice from
any tool use walks `toolresult ← assistant:odd ← …` and can never reach a `userprompt` node,
because no edge enters `assistant:odd` from any prompt. The code's own comment (prompt.go:164-166,
plan line 1468-1471) claims this edge is "the ONLY path by which a backward slice from a tool use
deep in a session reaches the request that set it off" — that stated purpose is structurally
unachieved. In irregular flows (tool events landing on a prompt's turn) the edge instead connects
the prompt to the assistant activity that happened **before** it — backwards causality.

Both halves are plan-mandated (decision 4's increment; step 5's `Turn: st.Turn` call; test row
2126 pins `userprompt:0 → AssistantNode(0)`), so the implementation is faithful — the plan is
internally inconsistent with SP-07's builder pairing, and no test asserts prompt reachability from
a tool-use criterion (prompt_test.go:265 asserts the edge exists, which a dangling edge satisfies;
the e2e slices start from tool results and never check for the prompt). Fix requires a ruling:
either decision 4 stops incrementing on the prompt (prompt and its answering assistant share a
turn, Stop alone advances — matches `ObservedPrompt.Turn`'s own doc "pairs the prompt with the
assistant turn that answered it") or `BuildUserPrompt` targets `AssistantNode(o.Turn+1)` via an
arch/ amendment to SP-07's builder.

### F2 (Important) — an idle `Persist` before the first observer event overwrites `state/observer.json` with an empty session map

`persistState` (state.go) never runs `o.once.Do(o.loadState)`; the once-guard lives only in
`session()`. `Persist` → `persistState` writes `{"version":1,"sessions":{}}` from the unloaded
map, clobbering the crash-resume state the file exists to provide (no `.bad` set-aside — it is a
plain overwrite via WriteAtomic). Failure scenario: daemon killed mid-session (the exact scenario
state.go targets), restarted manually (`qompack daemon`) or restarted by a non-observer request;
no observer seam fires before the first idle tick (~30s); `observer.persist` (priority 50, not
`act.`-prefixed, so it runs even degraded) wipes the file; the host session later resumes,
`OnSessionStart` → `loadState` reads the emptied file → Turn/PrefixTokens/segment bookkeeping
restart at zero, and Pos monotonicity across the resumed session is silently broken. One-line fix:
`o.once.Do(o.loadState)` at the top of `persistState` (or `Persist`).

### F3 (Important, cross-component) — same-session event ordering is not guaranteed: decision 9's premise is unsound over the shipped ingest pool

Decision 9 reasons from "the worker pool may deliver two events for *different* sessions
concurrently" and "one session is inherently sequential in the host anyway". The shipped pipeline
does not provide that: `ingest.Start` launches `max(2, NumCPU/2)` workers pulling from **one**
shared ring with no per-session affinity (ingest.go:236-260), so two queued events for the same
session are routinely dispatched to two workers and complete in either order; and
`handleObservePrompt` runs `OnUserPrompt` synchronously on the reply path while earlier
PostToolUse/Stop events for the same session may still sit in the ring (handlers.go:416-455,
daemon.go runIngested) — the prompt can overtake the tool events that preceded it. `st.mu` makes
this race-FREE but not order-preserving, and the observer's state is order-sensitive: `Turn`
attribution (a Stop draining before the last tool result of its own turn pushes that result into
the next assistant turn, changing its consumes-edge shape), the `LastToolUseID/LastToolUseTurn`
chain, `SubagentSince`, and the `Recent` feature window all assume arrival order. Parallel tool
call bursts immediately followed by Stop are the host's normal traffic shape, and §8.1's own
degrade-to-queue regime is precisely when the queue lags. No test drives same-session events
through the real pool out of order (the race test checks freedom-from-races across two sessions,
which is the easy half). This needs either per-session serialization in the pool (hash the session
to a worker / per-session FIFO), or an explicit, recorded acceptance that turn attribution is
best-effort under lag — either way it is a cross-SP-05/SP-08 decision the branch currently makes
implicitly and undocumented.

### F4 (Minor, plan-mandated) — `ensureSegment` clobbers the adopted segment's true StartPos on resume

On the adopt branch (`cur` open), `st.SegStartPos = st.PrefixTokens` runs unconditionally
(session.go, plan line 1668 "Either way…"), overwriting the persisted `seg_start_pos` that
state.go went to the trouble of round-tripping (state_test pins 104880 surviving the restart —
until the next SessionStart discards it). A host `resume` therefore closes the segment with
StartPos at the resume point and `Tokens = PrefixTokens - resumePos`, undercounting the segment
and misplacing the boundary CrossingEdges is asked about. The persisted value is honoured only in
the no-SessionStart restart path. Plan-internal inconsistency (the persistence rationale
contradicts the ensureSegment spec); flag to V3-VERIFY: the adopt branch should keep the larger of
(persisted SegStartPos, cur-open evidence) rather than resetting to the current prefix.

### F5 (Minor, plan-mandated order) — an ended session persisted at step 5 and deleted at step 7 can be resurrected permanently

`onSessionEnd` writes the state file (including the ending session) before removing it from the
map. If the daemon dies before any later `persistState` (SessionEnd is commonly the last event
before shutdown), the next daemon start rehydrates the ended session into `o.sess`, where nothing
ever prunes it: every future persist rewrites it, forever. Each unlucky shutdown adds a permanent
resident (up to ~256 window entries each) to memory and `observer.json`. Cheap fix: delete from
the map before persisting, or skip the ending session in the step-5 snapshot.

### F6 (Minor) — `snapshotSession`'s window trim does not shift `SubagentSince`

state.go trims the persisted window to the last `subagentWindowCap` entries but persists
`SubagentSince` unshifted. Dead code today (rememberToolUse caps the live ring), but if the cap
ever tightens or the trim ever fires, the restored index points `subagentWindowCap - k` entries too
late/early and `rehydrate` clamps only the upper bound. Either drop the trim (it is unreachable)
or apply the same `max(0, since-k)` the live eviction uses.

### F7 (Minor, observation) — prompt capture is not crash-durable the way tool events are

The WAL/drain path replays `observe.prompt` only into the sentinel scan (`runIngested`'s
OpObservePrompt arm); the G2.3 verbatim capture happens solely in the synchronous reply-path call
(plus its deadline-abandoned goroutine). A daemon crash between the WAL ACK and the capture loses
that prompt permanently despite the WAL holding it — a durability asymmetry with tool results.
This adjoins the settled amendment-(d) ruling (which established the drain does NOT re-dispatch
prompts, in order to avoid double-dispatch) and is recorded here as its residual consequence, not
as a re-litigation: V3-VERIFY should decide whether drain should re-invoke the capture with the
Output discarded (idempotent: the derived VerbatimPromptID and content-addressing make a replayed
capture converge).

Also noted in passing (no action required this branch): `OnSignals`/`OnFeatures` callbacks run
while holding `st.mu` — safe for today's daemon callbacks, but a wave-3 callback that re-enters
the observer for the same session will deadlock; worth a one-line contract comment on Options.

## Out-of-diff checks performed (one per named risk)

- dag builder semantics (turn pairing, PrevTurn guard, supersedes/consumes directions):
  `internal/dag/builders.go` at HEAD — grounds F1; confirms the guard and D-1 directions the
  observer relies on.
- subagent flag/name restoration: `internal/daemon/daemon.go` runIngested + `handlers.go`
  decodeSubagent/resolveEvent at HEAD — the `subagent` bool and `Extra["agent"]` reach `OnStop` as
  the observer assumes; the e2e test covers the client half.
- ingest pool ordering + idle controller firing: `internal/daemon/ingest.go`, `idle.go`,
  `handlers.go` flushRoute at HEAD — grounds F2/F3.
- store supersession contract: `internal/store/store.go` MarkSuperseded/ToolUsesByPath at HEAD.

Test I would run (tree was being edited, so not run): a real-pool integration test enqueueing
tool→tool→stop→prompt for one session with 4 workers and asserting turn attribution — expected to
flake per F3.

## Verdict

Not approved from this lens: F1–F3 are Important. F1 needs a controller ruling on which side of
the plan conflict (decision 4 vs dag.BuildUserPrompt's same-turn pairing) gets amended.
