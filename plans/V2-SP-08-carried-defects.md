# SP-08 — carried defects

One thing SP-08 knowingly ships. It has a row in `plans/CARRIED-DEFECTS.tsv`, and
`test/guards/carrieddefects_test.go` will not let `V3-VERIFY` write `plans/V3-report.md` while it is
still `open`.

**A note on this file's name.** It is `V2-SP-08-…` rather than `V3-SP-08-…` because
`test/guards/carrieddefects_test.go` derives the one document a row's section may live in from the
row id and the single constant `carriedDefectsWave`, which is `"V2"` — that guard's own comment
says "a later wave keeping its own manifest changes this one constant", and no later wave has kept
one. `plans/CARRIED-DEFECTS.tsv` is still the only manifest, and it already carries SP-02, SP-04,
SP-05 and SP-06 rows alongside this one. Renaming the scheme is a change to a guard SP-08 does not
own; when a wave-3 manifest is cut, this file moves with it.

**This is not a reason to hold the merge.** The cost is inside `store.PutBytes`, it is bounded, and
it is invisible to the user: the hook path acknowledges from the WAL before L0 processing runs, so a
B-C overrun delays index freshness and blocks nothing. Writing it down is the alternative to hoping
someone re-derives it at V3-VERIFY.

Fixing the row means setting its status to `fixed`; deciding not to means setting it to
`deferred:<a later checkpoint>` and adding a paragraph here saying why. Doing neither fails the gate.

---

## SP08-D1 — `OnToolUse` over a large tool result breaches budget B-C

**Symptom.** `OnToolUse` on a 256 KB `go test` result costs far more than budget **B-C**
(`l0_process`, p99 < 50 ms, soft, `Gated: false`) allows, and does so on the realistic case as well
as the pessimal one. On a 64 KB source read it stays inside the budget on every fixture.

The number moves with one variable — how much of the result differs from the previous call — so the
two shipping benchmarks each measure three points on that axis rather than one. `objects` is the
count of chunks the store actually holds after the run, and is what proves a fixture is doing what
its name says.

Measured on a quiet machine (Intel Core Ultra 7 155H, windows/amd64),
`-benchtime 200x -count=5`, against a real `store.Open` and a real `dag.Open` on a temp root. p50 and
p99 are read out of the observer's own `observer.tooluse` histogram, which is the instrument B-C is
evaluated against in production; the ns/op column is Go's mean.

| benchmark | fixture | ns/op | objects | p50 (ms) | p99 (ms) | B-C |
|---|---|---|---|---|---|---|
| `BenchmarkOnToolUse_FileRead64KB` | `Deduped` | 11.5–13.4 ms | 5 | 12.3–14.3 | 14.3–18.4 | inside |
| `BenchmarkOnToolUse_FileRead64KB` | `Delta` | 15.8–16.7 ms | 204 | 15.4–16.4 | 24.6–45.1 | inside |
| `BenchmarkOnToolUse_FileRead64KB` | `AllNovel` | 23.7–24.6 ms | 905 | 24.6 | 36.9–45.1 | inside |
| `BenchmarkOnToolUse_TestOutput256KB` | `Deduped` | 39.9–65.6 ms | 34 | 41.0–61.4 | 53.3–131.1 | **breach** |
| `BenchmarkOnToolUse_TestOutput256KB` | `Delta` | 39.3–55.6 ms | 236 | 41.0–49.2 | 53.3–114.7 | **breach** |
| `BenchmarkOnToolUse_TestOutput256KB` | `AllNovel` | 163.6–224.1 ms | 12209 | 147.5–213.0 | 262.1–655.4 | **breach** |

The three fixtures are:

- **`Deduped`** — one identical payload every call. It dedups completely from the second call on, so
  the store never writes a chunk (`objects` stays at the first call's count). It is the optimistic
  end and is kept only for comparison; an earlier revision of these benchmarks measured only this
  and reported 2–18% headroom, which is what the review caught.
- **`Delta`** — one line differs per call: Qompack.md §8.1's own near-duplicate case, *"same test
  suite, one new failure"*. This is the realistic shape and **the fixture the acceptance criterion
  below is stated against.**
- **`AllNovel`** — a marker every 512 bytes, below the store's 1 KB minimum FastCDC chunk, so every
  chunk of every call is novel. The pessimal end.

**Diagnosis.** Three costs stack, and only the third is new information.

1. Canonicalization, roughly 25–30 ms of the 256 KB fixed cost. This is SP-04's per-rule prefilter
   scan, already recorded as **SP04-D5**: cost is linear in the rule count, and 256 KB is 2.5× the
   100 KB that row's `BenchmarkRun_Bash100KB` is stated against.
2. MinHash, roughly 10–15 ms, at the configured 128 permutations over 256 KB of shingles.
3. **The novel-chunk object-write path inside `store.PutBytes`** — zstd compression plus one file
   write per novel chunk. This is what separates the three fixtures: 34 objects and ~45 ms against
   12 209 objects and ~180 ms for the same payload size. It is amplified on Windows, where per-file
   create/write/close is materially more expensive than on Linux, so the CI reference platform is
   expected to read better than the numbers above.

This row is downstream of **SP06-D2**, which already records that `PutBytes`'s own cold and warm
budgets (3 ms / 400 us) are 9x and 19x over on Windows and have never been measured on the reference
platform. SP08-D1 is what that costs once a hook calls `PutBytes` on a real tool result: the same
mechanics, seen from the caller, at a payload size SP-06's own benchmarks do not cover. V3-VERIFY
should dispose of the two together, and a fix to SP06-D2 may close this row without SP-08 changing
a line.

Nothing in the observer's own bookkeeping is on this scale. Its measurable share is about 7 ms of
repeated `responseText` decoding — the payload is unwrapped four times per event, once at step 3 and
three more times inside the §5.21 pure functions — and reducing that means changing the shape of
`ExtractSignals` and `ExtractTestOutcome`, which are frozen by §5.21.

**Evidence.** `BenchmarkOnToolUse_TestOutput256KB` (and `BenchmarkOnToolUse_FileRead64KB` for the
inside-budget half), in `internal/observer/tooluse_test.go`. Reproduce with:

```
go test -bench BenchmarkOnToolUse -benchtime 200x -count=5 -timeout=30m ./internal/observer/
```

Neither benchmark asserts a latency. B-C is soft and ungated and these numbers move with the host, so
a benchmark that failed the build on one would make every unrelated change look like a regression.
That is precisely why the breach is recorded here instead.

**Why it is deferred rather than fixed.** The dominant cost is `store.PutBytes`'s object-write path,
which is SP-06's mechanics and outside SP-08's scope: the observer never chunks, compresses or writes
an object by hand (resolved decision 1), it hands bytes to the store and the store decides. Reaching
into that from here would be exactly the cross-subplan edit Rule W-1 forbids.

It is also not user-visible. §8.1's own instruction for a budget overrun is to *"degrade to async
queue-and-drain rather than blocking"*, and that is already the architecture: the hook acknowledges
from the write-ahead log before L0 processing runs, so a B-C overrun delays index freshness and
blocks nothing in the session. B-C is `Gated: false` in `internal/obs/budgets.go` for that reason.

No threshold is edited. Moving a §11.3 budget row is a sign-off SP-08 may not give itself.

**Acceptance (V3-VERIFY).** Either of:

1. `BenchmarkOnToolUse_TestOutput256KB/Delta` reports p99 < 50 ms on the CI reference platform on a
   quiet host, at `-benchtime 200x -count=5`; or
2. an explicit §11.3 sign-off that records the measured figures and either moves the B-C row or
   re-defers this one to a later checkpoint with a reason.

Re-measure the whole table above, not just the `Delta` row: `Deduped` breaching at all on the run
recorded here — it did not on an earlier, quieter pass — is the sign that the 256 KB fixture sits
close enough to the budget that host noise crosses it, which is itself information for the decision.

Two candidate fixes belong to that conversation rather than to this row: pushing the object write
off the ingest path (the queue-and-drain §8.1 names, taken one level deeper), and reconsidering
whether a 256 KB tool result is the payload the budget should be stated against — the plugin's own
`runtime.hotPath.maxPayloadBytes` default is 1 MiB, four times larger again.

---

## V3-VERIFY dispositions (2026-08-26)

**SP08-D1 -> deferred:V4-VERIFY, adjudicated with SP06-D2.** V3 isolated re-measurement
(BenchmarkOnToolUse, -count=3, quiet host): 256 KB p99 90-98 ms (Deduped/Delta) and 164-197 ms
(AllNovel) against B-C's soft 50 ms; 64 KB Deduped p50 9.2-10.2 ms. B-C is Reported, never gated
(section 2.4), the daemon's response to overrun is sampling + backpressure rather than blocking,
and the cost is dominated by store.PutBytes's per-novel-chunk object-write path - the same path
SP06-D2 now measures over budget on Linux as well as Windows. The two rows are one defect seen
from two layers and travel together to V4-VERIFY, where the checkpointer's encode path (the other
large PutBytes caller) lands and the budget-vs-implementation decision has its full evidence.

---

## V5-VERIFY dispositions (2026-09-09)

**SP08-D1 -> deferred:V6-VERIFY.** V4-VERIFY did not dispose of the row. V5 quiet
pass on `verify/v5` @ `0d5c999` (`scratchpad/quiet/E7-I-08.15.txt`, `-benchtime 2s`, serial):

| benchmark | fixture | ns/op | p99 (ms) | objects | B-C |
|---|---|---|---|---|---|
| `BenchmarkOnToolUse_FileRead64KB` | `Deduped` | 10.27 ms | 15.36 | 5 | inside |
| `BenchmarkOnToolUse_FileRead64KB` | `Delta` | 17.28 ms | 40.96 | 145 | inside |
| `BenchmarkOnToolUse_FileRead64KB` | `AllNovel` | 20.29 ms | 28.67 | 451 | inside |
| `BenchmarkOnToolUse_TestOutput256KB` | `Deduped` | 35.24 ms | 81.92 | 34 | **breach** |
| `BenchmarkOnToolUse_TestOutput256KB` | `Delta` | 41.11 ms | 81.92 | 91 | **breach** |
| `BenchmarkOnToolUse_TestOutput256KB` | `AllNovel` | 110.69 ms | 180.2 | 1236 | **breach** |

The 64 KB file-read fixture reads 15.4–41.0 ms at p99 on this window, inside the budget as
when the row was opened; the inventory's co-loaded run had put it over, and ruling Q25's widening of
the summary is therefore not applied — the row keeps its 256 KB wording. The 256 KB fixture is over
at every delta shape, as recorded. The diagnosis stands unchanged: the per-novel-chunk object-write path inside `store.PutBytes`, i.e.
SP06-D2 seen from the caller. Reference platform not measurable here (see SP06-D2's V5 disposition
in `plans/V2-WAVE1-carried-defects.md`); V6-VERIFY takes the four reference-platform rows together.

## SP08-D2 — the observer does not absorb an at-least-once redelivery under a reused lease

**Symptom.** `internal/daemon/ingest.go`'s dispatch contract says "Restart does not retain this
set, so handlers must tolerate at-least-once delivery", and the drain honours it by redelivering a
leased-but-unacknowledged line under its stored lease — same delivery token, same
`ObservationID` — after a crash between publication and `commitDelivery`. Two observer handlers do
not absorb that redelivery:

- `internal/observer/stop.go` `captureSubagent` mints `SubagentCaptureID(e.SessionID, st.Turn)`
  from the fresh handler's per-session turn counter (0 on a fresh process, or the restored turn
  when the session state was reloaded) rather than from the reused observation identity, so the second run writes a second capture blob
  (`store.PutBytes`) and a second `index/tool_use.jsonl` record for one `SubagentStop` event.
- the read-supersede path (`internal/observer/supersede.go` `MarkSuperseded`, reached from
  `tooluse.go`) lets a replayed read whose content is older supersede records appended after it.

**How it was found.** V5-VERIFY fix item F4: after `fix(ipc)` made the capture path live, the
V3 e2e `TestV3_LiveSessionWriteSetAndAppendOnly` went red at its flush arm with a fifth
`SubagentStop` record for four events (`subagent_sess-e2e-x9_0`) in one run and eight inverted
supersede marks in another. The trigger in those runs was the drain replaying copies of ALREADY
acknowledged deliveries (F4-P1), which `1c17f0d` fixes at the drain: an acknowledged copy now
advances the offset without dispatch. What this row records is the narrower window the fix leaves
by design — the legitimate at-least-once redelivery — through which the same two effects reach the
index.

**Evidence.** `TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent`
(`internal/daemon/drain_reused_lease_test.go`, `d43ecb5`) pins the current outcome: a delivery
whose handler ran but whose acknowledgement never reached the journal is redelivered on the next
drain under the same `ObservationID`, and the handler runs a second time. It asserts the second invocation under the same `ObservationID`, one lease, one sidecar and one frontier record afterwards; its negative control (seen set kept across the restart) fails at the second-invocation assertion, and the acknowledged-copy pin is the distinct case whose delivery must not be dispatched at all.

**Manifestation that remains on the fixed tree.** x09's SubagentStop count assertion
(`test/e2e/v3_x09_test.go`, one record per Stop event) fails when host load lets the pre-flush
shutdown cancel an `observe.stop` between the observer append (stage 2) and `commitDelivery`
(stage 3, which refuses a cancelled context): the flush-time daemon redelivers the
leased-but-unacknowledged WAL copy, correctly, and `captureSubagent` mints a new id from the
restored turn (records `subagent_sess-e2e-x9_16..19` in the F4 author's diagnostic trees,
`scratchpad/f5a/diag/`). The author's runs under an `internal/store` co-run were 1 of 3, 0 of 6, 2 of
12 and 3 of 3 red (`scratchpad/f5-results.txt`; the 2 of 12 in `f5a/x09-diag2.txt`, the 3 of 3 in
`f5a/x09-x3b.txt`), and the reviewer's four runs on a quiet host were all green (`f5a/rev0/x09-x3.txt`,
`f5a/rev0/x09.txt`); the quiet pass's F7 row passed too. The assertion is correct for the architecture and is kept; until the observer is fixed the
test is co-load-sensitive on this host, which the V5 report records in §22 and §29.

**Why it is carried.** The fix belongs to the observer (derive the subagent capture id from the
observation identity; make supersession refuse a superseder older than the record), a change to
the L0 publication path that V5-VERIFY does not own and that needs its own review against
SP-20's capture contract. The crash window is narrow (publication done, acknowledgement lost) and
the acknowledged-copy path that produced every observed instance is closed.

**Acceptance (V6).** Both observer sites idempotent under a reused lease, the evidence test
inverted to assert one record and no inverted mark, and the e2e x09 flush arm unchanged.

**Resolution (V6).** Both sites are closed, in `internal/observer` and `internal/store` only: no
daemon production file changed, no persisted format changed, and no id spelling changed —
`SubagentCaptureID` is still `subagent_<session>_<turn>`, and a redelivery mints nothing at all.

- *Site 1, the re-captured Stop.* `captureSubagent` asks whether this delivery already published a
  capture before it mints anything. The join is the capture sidecar the daemon writes before every
  dispatch: the Stop path now completes publication order's second stage (`LinkCaptureReference`)
  as the tool path always did, so a redelivery finds the reference its first run stamped, adopts
  the turn of the record it found, counts `observer.redelivery_absorbed`, and returns — no second
  blob, no second record, no second turn consumed.
- *Site 2, the replayed read.* `RecordToolUseSuperseding`, reached through the new narrow
  `store.SupersedingRecorder` capability (the shape `RefCounter` already uses, so §5.8's frozen
  `Store` interface is untouched), appends a tool_use record and one supersede mark per surviving
  candidate in ONE `appendFile.write`. "A record whose marks never landed" stops being a state a
  cancelled handler can leave, and a replay — same id, same root — writes nothing at all, marks
  included. That also removes the inverted mark, because a replay never re-runs supersession and so
  can never admit a record newer than the content being replayed.

Three guards were added after adversarial review, each with a test and a recorded negative control:

1. **Derived ids are minted against the index, not the counter.** Two Stops of one session can be
   in flight at once — the ingest pool has at least two workers and the transport ACK is written
   before a worker touches the job — so the turn a process holds may already be taken, and minting
   blind soft-drops the capture: four Stop events, three records. The probe is bounded
   (`derivedTurnProbe`, 64, counted under `observer.derived_turn_exhausted` when exhausted) and
   **gated on a leased identity**, so an in-process caller keeps today's behaviour and
   `TestOnStop_CaptureIsDeterministic` still holds.
2. **A derived TOOL id is adopted only when the prior record's root matches this delivery's.**
   Without the check, a legitimately different root (a changed canonicalization config, a different
   payload cap, a moved truncation boundary) adopts an id it cannot record: `ErrAppendOnly` →
   `ErrUnpublished` → the drain breaks its read loop without advancing the offset, and that line is
   retried forever with the rest of the spool file behind it. On a mismatch the handler falls
   through to a freshly probed id, keeping the base's self-healing duplicate instead of converting
   it into a permanent stall.
3. **The sidecar must describe THIS delivery**, its `Session` and `Op` checked against the event
   being handled. This rule promotes the ObservationID from evidence to a precondition for whether
   a capture is written at all, so it now depends on an invariant nothing enforces mechanically:
   **an ObservationID is never reused for a different delivery, and a sidecar outlives its
   delivery's redelivery window.** `core.NewObservationID` is `H(session‖arrival)` over a dense
   per-session counter that nothing retires today, and SP20-D4 is the row whose fix would restart
   those counters — a retention or compaction pass must preserve this invariant or retire the
   sidecars along with the leases. The benign direction is worth stating too: if a future GC prunes
   sidecars, recognition degrades to a miss, which is today's duplicate rather than a swallowed
   capture.

**Two behaviour differences that are not parity**, both soft and both in the safer direction: the
supersession scan now runs BEFORE the record is in the index, so it sees `supersessionLookback`
real priors where the post-write scan saw one fewer plus the record itself; and a record another
session appends between the scan and the write is not marked, where the post-write
`MarkSuperseded` would have seen it.

**The legacy path is defect-bearing by design, and is reachable only by fakes.**
`detectSupersession` keeps its §5.21-pinned signature and remains the path for a Store without the
capability — which is exactly the 1+N write shape this row exists to remove. Production always has
the capability (`store.Open` returns `*FSStore` and no production type wraps `store.Store`), so the
absence is made loud rather than silent: `observer.New` asserts once, logs `Loud` and counts
`observer.legacy_supersede_path`, and `TestWireObserverStoreSupportsSupersedingRecorder`
(`internal/daemon`) pins the real composition. Every observer unit test takes the legacy path, so
without that pin a wrapper introduced by SP20-D1's group-commit rewrite would leave every unit test
green and only x09 red, intermittently, under load.

**Residual, handed to SP08-D3 (its acceptance item 4).** A process kill between a derived-id record
and its sidecar link leaves the record durable and the sidecar unlinked, so the redelivery cannot
recognize it and captures again — now at the next free turn, a duplicate rather than a soft-dropped
capture. No context check sits between the two, so the pre-flush shutdown cannot produce it; only a
hard kill or `stopCleanupBound` expiry reaches it. Closing it needs the observation-to-id join
durable before or with the record, which is the same SP-20 contract decision SP08-D3 needs for
prompts. Two smaller residuals: probe exhaustion (64 consecutive occupied derived ids, reachable
only by a state file lagging the index by more than a session's worth of captures) and the
ObservationID precondition above.

**Cost, alongside SP08-D1.** `captureSubagent` gains a `ReadCaptureSidecar` at the top and a
`LinkCaptureReference` at the end (itself a second read plus a `WriteAtomic`): three file
operations per SubagentStop, inside budget B-C, whose row SP08-D1 is still `deferred:V6-VERIFY`.
The recognition read is skipped entirely for a delivery with no identity, so the in-process path
pays nothing, and the read path trades 1+N index writes for one.

**Evidence.** The evidence test keeps its name and was inverted: it now asserts one SubagentStop
record, no line appended by either redelivery, and no inverted mark, while still asserting the
daemon's half — each cut delivery dispatched a second time under the identity it was first
assigned. It FAILS on the pre-fix base `79a5171`, where the redelivery appends exactly the two
defect lines: `{"op":"supersede","id":"toolu_sp08d2_later","by":"toolu_sp08d2_first"}` (an older
read superseding a newer record) and a second `subagent_<session>_1` record for one Stop event.
Deterministic coverage: a cancellation sweep over every context-check point of a read and of a Stop
× {same process, restart with a persisted turn, restart with none}, asserting the flush arm's own
mark rules over the lines each redelivery appended; a `pgregory.net/rapid` property test over
generated sessions; and eleven negative controls, one per guard, each scored CAUGHT only on a real
failure signature.

The x09 co-load protocol ran each e2e iteration beside a detached `go test ./internal/store/`,
which is the load that lets the pre-flush shutdown cancel an `observe.*` between the observer's
append and `commitDelivery`. On the pre-fix base `79a5171` the defect **reproduced**: 6 green and 1
red in 7 runs, the red at `v3_x09_test.go:369` — "index/tool_use.jsonl must hold exactly one
SubagentStop record per Stop event after the flush", expected 4, actual 5, which is site 1's
phantom capture. On this branch the same protocol ran **12 times, 12 green, 0 red**. The x09 flush
arm itself is byte-identical to the base. `-race -count=20` over the observer, store and daemon
tests reports no data race and no failure, the rapid property test is clean under `-race`, and the
seven-package sweep (`internal/observer`, `internal/daemon`, `internal/store`,
`internal/rehydrate`, `internal/checkpoint`, `internal/mcp`, `test/guards`) is green.

## SP08-D3 — a prompt the daemon did not capture live is never verbatim-captured

**Symptom.** G2.3's verbatim prompt capture (`observer.OnUserPrompt`) runs only on the live reply
path (`internal/daemon/handlers.go` `handleObservePrompt`). Every replay of an `observe.prompt`
delivery — the startup Drain that runs before Serve, `redrainOnceServing`, the idle drain, Stop's
drain, a flush — reaches `runIngested` through `drainDispatch`, and `runIngested`'s prompt arm runs
only the sentinel scan. The delivery is then acknowledged and its spool or WAL copy, the last
durable copy of the words, is deleted. Until the V5 close-out nothing counted the loss; the counter
`l0_prompt_replayed_uncaptured` now does (an upper bound: a line the full ring refused, or one whose
live capture landed before a crash, replays the same way).

A lost turn 0 is worse than a missing record. `readL0Intent` (`internal/rehydrate/items.go`) needs
`prompt_<s>_0`. Without it the rehydrator falls back to the checkpoint's `UserIntent.Original`,
which `seedTierOne` (`internal/checkpoint/writer.go`) took from the earliest prompt that WAS
captured, so rehydration item 2 injects the second or a later prompt under the "verbatim original"
label, with a drop entry that claims §8.5 provenance. When no Stop advances the turn between the
lost prompt and the next one, the next prompt is recorded as `prompt_<s>_0` itself and is injected
with no drop entry at all.

**How it was found.** SP-08's final correctness review, finding F7
(`plans/sdd/V3-SP-08-observer-l0/final-review-correctness.md`), handed V3-VERIFY the question
"whether drain should re-invoke the capture with the Output discarded". No checkpoint ruled on it,
and `plans/sdd/V5-VERIFY/x01-disposition.md` reshaped X1 around the loss as behaviour. The V5
close-out (2026-09-10) found it unruled while fixing the live half. It is not the V3-VERIFY plan's
row F7 (segment log / DPI guard), which shares the name.

F7's premise that a replayed capture converges is false. The record id is
`VerbatimPromptID(session, st.Turn)`, read when the capture takes the session lock, and every call
runs `Turn++`; `RecordToolUse` refuses a second root under one id and the DAG merges a node by id. A
drain that re-invoked the capture would therefore record the same words again one turn later (the
class of SP08-D2), and it would still miss the window in which the live worker acknowledges the
delivery before a detached capture lands.

**Exposure.** The paths on which a prompt reaches the daemon only by replay, or never finishes its
capture:

| path | where | turn 0 | later prompts |
|---|---|---|---|
| idle exit, then a prompt after about 60 minutes of silence | `EndAbandoned` plus the zero-live countdown (`IdleExitSeconds` 1800); the client spools before `lazySpawn` | low | the first prompt after every long break |
| the HotSpool submode after a B-A breach | the client spools every hot-path op without dialling (`internal/ipc/client.go`); only a new session clears the mode | rare | every later prompt of that session |
| a connect failure against a busy daemon | the hot-path dial budget, 5 ms (25 ms on Windows) | low | low to moderate under load |
| a cold start with no serving daemon | the `EnsureRunning` bound, a spawn failure, or a long startup Drain | rare (less rare for headless `claude -p`) | rare |
| a crash between the WAL append and the capture | the WAL line replays through the sentinel scan | very rare | very rare |
| Stop with a capture in flight or refused | `stopPromptRecordings`; `l0_prompt_capture_refused` counts refusals | very rare | very rare |
| configured spool-only (`runtime.daemon.enabled=false`, an over-long address) | `internal/cli/hookclient.go` | every session | every prompt |

The one loss V5 measured — the 250 ms reply deadline cancelling the capture (x01 saw 4 of 4 prompts
soft-dropped under co-load) — is fixed by `0e2e45a` and `a73db51`. A late capture can still land
after a Stop-driven turn advance or after a compaction read (that fix's review, finding F2); the
identity rule in the acceptance below closes that window too.

**Evidence.** `TestCarriedDefect_SP08D3_DrainedPromptIsNeverCaptured`
(`internal/daemon/drain_prompt_capture_test.go`, `777522c`) pins today's outcome with the real
observer and store: a spooled `observe.prompt` is drained, acknowledged and deleted with no
`prompt_<s>_0` record and `observer.err.prompt.put` at 0 (the capture was never attempted, so this
is not a soft-drop); `l0_prompt_replayed_uncaptured` reads 1; and the next LIVE prompt in the session
is then recorded as `prompt_<s>_0`, the silent substitution. That live capture landing is also the
test's positive control. Negative control (not committed): with the replay arm also calling
`svc.ObservePrompt`, the test fails at its not-found assertion, because the drained prompt is then
captured as `prompt_<s>_0`.

**Why it is carried.** A correct fix changes the L0 capture contract across SP-05, SP-08 and SP-20:
the capture moves into the acknowledgement-gated ingest job keyed by the delivery's ObservationID,
`Accept` returns the lease, `onUserPrompt` splits, and prompts start linking capture sidecars the
way tool results do. It shares its identity helper with SP08-D2, collides with the SP20-D1
group-commit rewrite of `Accept`, and still needs owner rulings (replay order, HotSpool, rehydrator
honesty) before turn 0 is reliable. The V5 close-out was authorized to fix failing tests and gates;
this row fails neither, so it is counted and pinned now and resolved with SP08-D2 in V6.

**Acceptance (V6).**
1. One prompt record per ObservationID, whether the delivery arrived live or by replay; the prompt's
   frontier acknowledgement follows its capture (the capture runs inside `runIngested` ahead of
   `commitDelivery`, not as a drain-only re-invoke).
2. A session's first admitted prompt is `prompt_<s>_0` even when it arrives by replay — replay order
   by the first record's TS per client file at minimum, a per-session merge on `req.TS` in full — or,
   if the owner rules ordering out, the rehydrator stops presenting a later turn's capture as the
   original (a Warn, a drop entry naming the substituted turn, no §8.5 provenance claim) and
   checkpoint seeding records which turn it took the original from.
3. A HotSpool ruling: exempt `observe.prompt` from the client-side short-circuit, or accept
   capture-by-drain once item 1 lands.
4. SP08-D2 is closed (V6) and ships the identity helper this item needs, in
   `internal/observer/identity.go`: `observationRecord` (the record a delivery already published,
   read from its capture sidecar and confirmed against the index, with the session and op checks),
   `linkObservation` (publication order's second stage for any record), `freeDerivedTurn` (a
   bounded probe for a derived id the index does not already hold) and `adoptTurn`. The prompt
   capture links its sidecar after its record and consults `observationRecord` before minting
   `VerbatimPromptID`, so a replayed or redelivered prompt records nothing — that is item 1's "one
   prompt record per ObservationID", for the capture half. Whether a prompt may SKIP an occupied
   turn is this row's ruling and not SP08-D2's: `freeDerivedTurn` is deliberately applied to
   SubagentStop and the derived tool id only, because probing past an occupied `prompt_<s>_0` is
   precisely this row's own silent substitution and `internal/rehydrate` needs turn 0 specifically.
   SP08-D2's residual also lands here: a process kill between a derived-id record and its sidecar
   link leaves a record the redelivery cannot recognize, and closing it — for prompts and
   SubagentStop alike — means making the observation-to-id join durable before or with the record
   (a sidecar reservation, or an additive `obs` field on the index file's compact record), which is
   an SP-20 contract decision.
5. The evidence test inverted; the replay pin in `internal/daemon/prompt_record_test.go` ("a
   replayed observe.prompt must not record the prompt twice") rewritten to one record per
   ObservationID; X1's index promise restored to 65 or reconciled against the counter.
