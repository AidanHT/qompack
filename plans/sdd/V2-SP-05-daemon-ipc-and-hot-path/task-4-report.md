# Task 4 report — contract monitor: producers, history, marker, sentinel, real G9.3 assertions

## Summary

Delivered the full contract-monitor observation layer in `internal/contract`: `producers.go`
(`DeclareProducer`/`HasProducer`/`ResetProducers`), `marker.go` (`MarkerPath`/`WriteMarker`, plus
unexported `readMarker`), `sentinel.go` (`MintSentinel`/`RenderSentinel`/`ScanTranscriptTail`),
`history.go` (`SessionHistory`, `LoadHistory`/`SaveHistory`/`HistoryPath`), the `gated` wrapper and
nine real `Check` bodies in `assertions.go`, and the two `RunAll` extensions (runtime-mode handling,
per-assertion panic recovery) plus the mode-forcing mechanism in `monitor.go`. `standard.go` now
wires `StandardAssertions()` through `gated` instead of the retired `notYetImplemented` helper,
keeping every declared ID/severity/description byte-identical to what `standard_test.go` pins.

Nothing in `internal/ipc`, `internal/daemon`, `internal/obs`, `internal/cli`, `hookio`, or
`Qompack.md` was touched. The daemon wiring that calls `WriteMarker` from routes and `RunAll` at
session start is explicitly left to the next task, as instructed.

## TDD evidence

Every new file was built test-first: I wrote the test file(s) for a component, confirmed they
failed to compile against the not-yet-existing symbols (RED — e.g. `contract.SessionHistory`,
`contract.MintSentinel`, `contract.DeclareProducer`, `gated` all undefined), then added the
implementation until `go test ./internal/contract/...` went green for that component, iterating
component by component (producers → marker → sentinel → history → assertions/monitor extensions).
Representative RED example captured while developing (producers.go before it existed):

```
$ go test ./internal/contract/...
# github.com/qompack/qompack/internal/contract_test [github.com/qompack/qompack/internal/contract.test]
./assertions_test.go:12:2: undefined: contract.DeclareProducer
./gated_test.go:24:10: undefined: gated
```

(This transcript is reconstructed from the build-first workflow actually used — see "process note"
below for why a byte-for-byte terminal capture of every intermediate RED state isn't reproduced
here.)

Final GREEN, full contract surface, race-checked, run twice:

```
$ go test ./internal/contract/... -race -count=2
ok  	github.com/qompack/qompack/internal/contract	3.342s
ok  	github.com/qompack/qompack/internal/contract/contracttest	2.452s
```

**Process note (self-reported, not hidden):** given the size of this task (9 real assertions, a new
History implementation, sentinel/marker mechanics, and two Monitor extensions, all cross-referencing
each other), I front-loaded research into the brief/spec/inventory before writing any code, then
built each component with its test file simultaneously rather than writing every one of the ~19
spec-table tests fully first and running the whole failing suite once. Each component did go through
compile-fail → implement → green before I moved to the next one, which is the substance of TDD's
"failing test first" discipline, but I did not preserve a single unbroken RED transcript across the
whole task the way a strict single-pass "write all tests, run once, see N failures" approach would
have produced. I'm flagging this explicitly per the verification-before-completion norm rather than
asserting stronger evidence than I actually have.

## Files changed

**New (production):**
- `internal/contract/producers.go`
- `internal/contract/marker.go`
- `internal/contract/sentinel.go`
- `internal/contract/history.go`
- `internal/contract/assertions.go`

**New (tests):**
- `internal/contract/producers_test.go`
- `internal/contract/marker_test.go` (package `contract`, white-box — needs unexported `readMarker`)
- `internal/contract/sentinel_test.go`
- `internal/contract/history_test.go`
- `internal/contract/assertions_test.go`
- `internal/contract/gated_test.go` (package `contract`, white-box — needs unexported `gated`)

**New (fixtures):**
- `testdata/golden/contracts/contract/want/history_degraded.json`
- `testdata/golden/contracts/contract/input/transcript_with_sentinel.jsonl`

**Modified (production):**
- `internal/contract/monitor.go` — added `forcedMode *Mode` field; `RunAll` gained the
  `runtime.mode == "off"` early return, the `"passive"`/`"full"` force-override at the end (via the
  new `forcedMode` field, read by both `RunAll`'s return and `Mode()`), and per-assertion panic
  recovery via the new `runAssertion` helper. `Register`, `Degrade`, `Restore`, `Report`, `persist`,
  `load`, `stateLocked`, `degradeReason`, `failedSummary`, `latestTS` are byte-identical to what
  shipped.
- `internal/contract/standard.go` — `StandardAssertions()` now builds each entry via
  `gated(id, severity, desc, checkFn)` instead of `notYetImplemented(id, severity, desc)`; the
  `notYetImplementedObserved` constant is kept (still the frozen string test/guards and the golden
  fixture match literally). IDs, declared severities, descriptions and order are unchanged — pinned
  and reverified by the untouched `TestStandardAssertions_MatchTheNormativeTable` and
  `TestStandardAssertions_DeclaredSeveritiesAreNotFlattened`.

**Modified (tests, additive only — no existing test body changed):**
- `internal/contract/monitor_test.go` — appended `TestDegradeIsIdempotent`.
- `internal/contract/contracttest/suite_test.go` — appended
  `TestRunHistorySuite_AgainstSessionHistory`, wiring `RunHistorySuite` to `&contract.SessionHistory{}`
  so its Rule W-1 behaviour block now runs (previously only skipped against the stub and the suite's
  own throwaway `memHistory`).
- `testdata/golden/contracts/contract/MANIFEST.json` — appended two new frozen entries
  (`history_degraded`, `transcript_with_sentinel`); the two pre-existing entries are untouched.
- `internal/testutil/fixtures_test.go` — bumped `TestContractFixture_EveryManifestIsReadable`'s
  pinned frozen-fixture count from 26 to 28 (the count this file's own history shows gets bumped
  whenever a subplan adds new frozen fixtures — see the "raise the frozen-fixture count to 23" commit
  in the log before this branch). This is not a contract-package file and is outside the
  `paths/config/core/logging/obs+hookio+store` import allow-set the brief holds `internal/contract`
  itself to, but it is a mechanical, load-bearing consequence of adding two frozen fixtures under
  `testdata/golden/contracts/contract/`, and the brief's own reading list ("internal/testutil's
  frozen-fixture registry") anticipates it.

## Frozen fixtures — none touched

`testdata/golden/contracts/contract/want/result_set.json` (the pre-existing frozen fixture) is
byte-identical; `TestResultSet_MatchesFrozenGolden` and `TestResultSet_GoldenRoundTripsLosslessly`
still pass unmodified. This works because `gated`'s short-circuit only lets a real `Check` run once
`HasProducer(id)` is true, and no test in this task leaves any producer declared across a test
boundary (every `DeclareProducer` call is paired with `t.Cleanup(contract.ResetProducers)`), so the
golden test's fresh, producer-empty registry still sees all nine assertions as
not-yet-implemented/SevInfo/ModeFull, exactly as frozen.

## Adapted-shipped-tests — none

No existing test body was modified. The two "modified" test files above only gained new test
functions; every previously-shipped assertion in `monitor_test.go`, `standard_test.go`,
`internal_test.go`, `mode_test.go`, `golden_test.go`, `contracttest/suite.go`,
`contracttest/behaviour.go`, and `test/guards/contract_test.go` is untouched and still passes.

One deliberate **deviation from `task-4-spec.md`'s literal text**, per the brief's binding ruling
(not a "shipped test" — a new test I authored under the spec's own name):
`TestDegradeIsIdempotent`. The spec's table says "degrade twice with the same reason → one Loud
line; two with different reasons → two lines" (i.e., Degrade should be idempotent when the reason is
unchanged). The brief explicitly overrides this: "KEEP SP-01's deliberate every-critical-run re-loud
Degrade behavior (do NOT make it reason-change-only; ... assert two degrades produce Loud lines both
times — same or different reason." I implemented the test exactly as the brief instructs (2 calls,
same reason → 2 Loud lines; a 3rd call, different reason → a 3rd Loud line) and made no change to
`Degrade`'s shipped, unconditional-Loud implementation.

## Design decisions worth flagging

1. **`Sentinel.Chances` tracking lives outside the `CAdditionalContext` Check**, per the brief's
   ruling ("Sentinel.Chances tracking is done by the daemon scan — read the table carefully"). I
   added `SessionHistory.RecordSentinelScan(found bool)` as the mutator the daemon's future
   UserPromptSubmit route (next task) and this task's own tests call; the assertion's `Check` is a
   pure reader of `History.Sentinel.Observed`/`.Chances`.

2. **`forcedMode *Mode` on `monitor`** is the mechanism behind `runtime.mode` "passive"/"full". It
   overrides what `Mode()` and `RunAll`'s return value report without touching the natural
   degrade/restore state machine (`mode`/`reason`/`cleanRuns`) or its persistence to
   `state/contract.json` — so switching an operator back to `"auto"` resumes exactly where the real
   observations left off, rather than resurrecting a fabricated "forced" history. Documented inline
   at the field and at the two call sites in `RunAll`.

3. **RunAll's extension is deliberately narrow.** `task-4-spec.md`'s monitor.go section also
   describes persisting `History` and recording per-assertion `obs.Counter("contract.fail."+id)`
   inside `RunAll` step 5. The brief's binding ruling instead says to extend `RunAll` with "exactly
   two capabilities" (runtime-mode handling, panic recovery) and states plainly "the daemon persists
   after RunAll (next task's job)". I followed the brief: `RunAll` never calls `SaveHistory`, and
   does not add the `contract.fail.*` counters. `SessionHistory` mutation still happens (via the
   `Env.History` pointer, inside each `Check`), matching "Checks MUTATE the SessionHistory ... the
   daemon persists after RunAll."

4. **`session_start.fires` resets `StartsWithoutMarker` to 0 on a hit.** The spec's table only states
   the increment-on-absence rule; a reset-on-hit is my inference from "absence ACROSS TWO SESSIONS"
   reading as *consecutive* absence, and from not wanting one stale miss years ago to combine with a
   single miss today into a false degrade. Covered by `TestSessionStartFiresNeedsTwoMisses` (which
   only exercises two consecutive misses, so it doesn't independently prove the reset) — the reset
   itself isn't separately unit-tested; flagging this as the one piece of behavior in `assertions.go`
   that isn't pinned by an explicit spec-table row.

5. **`hookEventKnownFields`** is built once via `reflect` directly against `hookio.Event{}` (mirroring
   `hookio`'s own unexported `claimedKeys` construction) rather than a hand-maintained literal list,
   so `hook.payload_shape`'s collision check can never silently drift from `hookio.ReadEvent`'s own
   field set.

6. **`transcript.readable` and the `custom_instructions` scan bound their reads** (64 KiB and 256 KiB
   tails respectively, per `readTail`/`ScanTranscriptTail`) rather than loading an arbitrarily large
   transcript file, since these assertions run on every `SessionStart`.

## Self-review / verification run

```
go build ./internal/contract/...                     # clean
go test ./internal/contract/... -race -count=2        # ok, ok
go test ./internal/contract/contracttest/... -run TestRunHistorySuite -v
                                                        # SessionHistory behaviour block now RUNS (not skipped)
go test ./test/guards/...                              # ok (TestGuard_FreshBuildReportsModeFull unaffected)
go test ./internal/ipc/... ./internal/daemon/...       # ok (untouched packages, sanity only)
go run ./tools/devtool fmt                             # no diff
go run ./tools/devtool vet                             # clean
go run ./tools/devtool lint                            # PASS golangci-lint / nomagic / importgraph /
                                                        #   testdeps / bindeps / sleepcheck / stubskips
go test ./...                                          # all ok, after the testutil fixture-count bump
GOOS=linux go build ./...                              # clean
```

Transient failure encountered and fixed during verification: `go test ./...` initially reported

```
--- FAIL: TestContractFixture_EveryManifestIsReadable (0.01s)
    fixtures_test.go:84: Not equal: expected: 26, actual: 28
FAIL	github.com/qompack/qompack/internal/testutil	1.390s
```

Root cause: `internal/testutil/fixtures_test.go` pins the total count of frozen contract fixtures
repo-wide, and this task added two (`history_degraded`, `transcript_with_sentinel`). Fixed by bumping
the pinned count to 28 with an updated comment (see "Files changed" above); rerun confirmed
`internal/testutil` green and did not require touching any other package.

## Concerns

- Item 4 above (`StartsWithoutMarker` reset-on-hit) is an inference beyond the letter of the spec
  table; I believe it's the intended reading of "§12.1: absence across two sessions" but it is not
  independently pinned by a test, and the controller may want an explicit test or a ruling on
  whether the reset should happen at all.
- `SessionHistory`'s sub-shapes (`SentinelState`'s field set beyond `Observed`/`Chances`, and exactly
  which fields the daemon will set on `LastMarkerTS`/`LastPrecompactTS`/etc.) were not specified
  field-by-field by the brief beyond the top-level JSON tag list; I designed them for what the nine
  `Check` bodies actually read/write plus what a daemon route would plausibly need to set, and the
  next task's implementer (daemon wiring) should treat these as adjustable if the daemon-side
  bookkeeping wants something these fields don't cleanly support.
- I did not implement `Services`/`DeclareProducers` (daemon-side production-declaration wiring) —
  confirmed out of scope per the brief, this task only ships `DeclareProducer`/`HasProducer`/
  `ResetProducers` as the primitives the daemon will call.

## Verbatim commit

```
feat(contract): G9.3 assertions, fail-loud degradation, two-clean-run restore

Every assertion is a real observation rather than a version check, and an assertion
whose producer is absent from the build reports SevInfo/not-yet-implemented so a
wave-1 build cannot degrade itself into passivity and silently disable the paths it
is meant to be testing.

Refs: SP-05, G9.3, §9, §12
```

---

## Fix round 1

Reviewer verdict: Needs fixes (0 Critical, 6 Important, 10 Minor). Full review at
`task-4-review.md`. All six Important findings fixed; minors triaged below. Amended into the same
single commit, exact subject/body unchanged, no trailers.

### I1 — StartsWithoutMarker now increments per SESSION, not per RunAll

`checkSessionStartFires` (`assertions.go`) now keys the increment off `History.LastSessionID`: the
counter only advances when `e.Event.SessionID != h.LastSessionID`, and `LastSessionID` is updated on
every branch (first-session, marker-found, absent). A second `RunAll` inside the same session no
longer double-counts. New tests:
`TestSessionStartFires_SameSessionRunTwiceDoesNotDoubleCount` (two `Check` calls, same
`SessionID` → `StartsWithoutMarker` stays 1) and `TestSessionStartFires_ConsecutiveAbsenceIsPerSession`
(miss / marker-present / miss → 1, driven entirely by `Event.SessionID` and a real `marker.json`,
**not** by hand-bumping `SessionCount`, per the reviewer's explicit ask). The pre-existing
`TestSessionStartFiresNeedsTwoMisses` was updated to use two distinct session IDs across its two
`RunAll` calls (it previously reused one session ID across both, which the fix now correctly treats
as one absence, not two) — this is my own test from this task, not a shipped/frozen one, so no
adaptation entry is needed for it beyond noting it here.

### I2 — empty Env.ProjectRoot is now a no-observation OK

`checkSessionStartFires` returns `noObservationYet` immediately when `e.ProjectRoot == ""`, before
touching `h` at all — no mutation. New test:
`TestSessionStartFires_EmptyProjectRootIsNoObservation` asserts `OK`, `Observed == "no observation
yet"`, and `h.StartsWithoutMarker`/`h.LastSessionID` both left at their zero values.

### I3 — the ≥24-character probe-phrase rule is wired up

Added `probePhrase(instr string) (phrase string, ok bool)`, which returns `ok == false` for a first
line shorter than `customInstrMinPhraseChars` (now actually read) — including the empty-first-line
case (`instr` starting with `\n`), which previously made `bytes.Contains(tail, []byte(""))`
vacuously true and the assertion unable to ever fail. `checkPreCompactCustomInstr` now reports
`OK: true, Observed: "no probe phrase long enough"` instead of scanning when `probePhrase` refuses.
New tests: `TestCustomInstructionsProbePhrase_ShortFirstLineIsNoObservation`,
`TestCustomInstructionsProbePhrase_LeadingNewlineIsNoObservation`,
`TestCustomInstructionsProbePhrase_GenuineMatchAndMiss` (the positive control), plus a white-box
`TestProbePhrase` against the helper directly (`gated_test.go`).

### I4 — runtime.mode == "off" now forces ModeOff through forcedMode

The early return for `e.Cfg.Runtime.Mode == "off"` now sets `m.forcedMode = &ModeOff` under `m.mu`
before returning, using the exact mechanism `"passive"`/`"full"` already used. `Mode()` and
`Mode().MayAct()`/`MayRecord()` now agree with what `RunAll` just returned; a subsequent `RunAll`
whose `runtime.mode` is not `"off"` clears the force via the existing `default:` arm, restoring the
persisted state machine's mode. New test: `TestRuntimeModeOffForcesModeOff` — off then `Mode() ==
ModeOff` and `MayAct() == false`; switching to auto restores `ModeFull`.

### I5 — SessionHistory ownership contract documented, no locking added (per ruling)

Per the explicit instruction not to add locking: `SessionHistory`'s doc comment now states plainly
that it is **not safe for concurrent use**, that `Seen` is a bare map whose concurrent
read/write is a fatal, unrecoverable crash (not a benign data race), and that a single value must be
owned by exactly one goroutine for its whole lifetime — load through every Check that touches it
through the eventual `SaveHistory`. `LoadHistory`/`SaveHistory` each got a one-line cross-reference
to that contract. This constraint is now also called out explicitly below for whoever writes Task
5's brief.

### I6 — erratum: SessionHistory's wire shape is FROZEN, not adjustable

My original "Concerns" bullet 2 said the next task should "treat these [SentinelState's sub-fields]
as adjustable." **That was wrong and is retracted.** `history_degraded` is declared `"state":
"frozen"` in `MANIFEST.json`, and `TestHistoryDegradedGolden_Decodes` compares
`json.MarshalIndent(&h)` byte-for-byte against the committed fixture. Any non-`omitempty` field
added to, removed from, or renamed on `SessionHistory` or `SentinelState` breaks that frozen §16
fixture, which is a Rule W-2 verification failure requiring controller sign-off and a fixture
re-freeze — not something a future task may quietly adjust. **Correction for Task 5's brief:**
`SessionHistory`'s wire shape is frozen; a new field must carry `omitempty` (and be absent/zero in
the fixture's own scenario) to avoid touching the fixture, and any other change needs controller
sign-off. (This pairs with I5: Task 5's brief should also state the no-internal-locking / single-
owner-goroutine contract above.)

### Minors — fixed vs left

**Fixed:**
- **M1** — `readTail` (`sentinel.go`) now opens `paths.Long(path)`, matching every other filesystem
  entry point in the package; a >260-char transcript path on Windows now reads correctly instead of
  failing after a successful `Stat`.
- **M2** — `SetPrecompactInstr` now truncates by RUNE count (`[]rune`), not by byte slicing, so a
  multi-byte-UTF-8 instruction cannot be split mid-rune (which would round-trip through JSON as
  U+FFFD). New test `TestSetPrecompactInstr_CapsByRunesNotBytes` uses a 300-rune multi-byte string
  and asserts a lossless `SaveHistory`/`LoadHistory` round trip.
- **M3** — `LoadHistory` now (a) rejects a `Version` present and not equal to `historyVersion`,
  falling back to zero exactly like corrupt JSON, and (b) calls the new `(*SessionHistory).applyCaps`
  after unmarshal, re-clamping `PrecompactWallMs`/`PrecompactInstr`/`Last` to their normal bounds so
  a hand-edited or future-schema file cannot smuggle an unbounded field past load. New tests:
  `TestLoadHistory_UnrecognizedVersionFallsBackToZero`, `TestLoadHistory_ReappliesCapsOnLoad`.
- **M4** — `TestResultSet_MatchesFrozenGolden` now calls `t.Cleanup(contract.ResetProducers)`
  itself, with a comment explaining why, so it no longer depends on the invisible invariant that no
  other parallel test in the binary declares a producer. (`TestResultSet_GoldenRoundTripsLosslessly`
  does not touch the producer registry at all — verified by inspection — so it was left unchanged
  rather than given a misleading no-op cleanup.)
- **M5** — four stale doc comments fixed: `assertion.go` (two references to the deleted
  `notYetImplemented`, now pointing at `gated`), `standard_test.go` (same), `doc.go` (added a "What
  SP-05 replaced" section naming every file and explaining the gated/producer mechanism, replacing
  the "future work" framing), `contracttest/suite.go` (History suite doc no longer says it "skips
  until a real History is handed to it" — it now says SP-05 shipped that History and the suite runs
  its behaviour block against it).
- **M8** — `AddPrecompactWallSample`, `SetPrecompactInstr`, `RecordLast`, `RecordSentinelScan` all
  gained `if h == nil { return }` guards, matching `Saw`/`LastSeen`/`Record`/`Sessions`. New test
  `TestSessionHistory_NilReceiverMethodsAreNoOps` exercises all eight methods against a nil receiver.
- **M9** — added `TestPreCompactTiming_NoSamplesIsOK`, pinning the `"no-samples"` `Observed` string
  and its OK result explicitly (previously unpinned, though behaviourally equivalent to the
  `TimeoutMs==0` path).
- **M10** — added a comment on `checkHookPayloadShape`'s collision arm explaining it defends against
  a hand-built or future-decoder `Event`, not against `hookio.ReadEvent`'s own output (which can
  never populate `Extra` with a claimed key).

**Left (with reasoning):**
- **M6** — the spec's step-5 observability (`obs.Counter("contract.fail."+id)` per failing assertion,
  a per-run `obs.Gauge("contract.mode")` beyond the one `Degrade`/`Restore` already record) is
  correctly unowned by any task per the brief's narrowing of `RunAll`. This needs a controller
  decision (assign to Task 5, or explicitly defer/drop), not a code change in this package — flagging
  it here again for the controller rather than guessing.
- **M7** — the `forcedMode` field now documents the pre-first-`RunAll`-after-restart window
  explicitly (the trivial doc half of this finding is fixed). The behavioural half — suppressing or
  annotating the `"contract: restoring full mode"` LOUD line while `runtime.mode == "passive"` is
  forcing passivity — was left unchanged: it is a real but minor UX wrinkle (the LOUD line and
  `Mode()` briefly disagree about what "restored" means while a force is in effect), fixing it
  properly would touch `Restore`'s shipped logging behavior and needs its own test coverage, and it
  is not one of the six Important findings this round targets. Recorded here for a future round or a
  controller call.

### Verification run (fix round 1)

```
go build ./internal/contract/...                     # clean
go test ./internal/contract/... -race -count=2        # ok, ok
go test ./test/guards/...                              # ok
go test ./internal/ipc/... ./internal/daemon/...       # ok (untouched packages, sanity only)
go run ./tools/devtool fmt                             # no diff
go run ./tools/devtool vet                             # clean
go run ./tools/devtool lint                             # PASS golangci-lint / nomagic / importgraph /
                                                        #   testdeps / bindeps / sleepcheck / stubskips
go test ./...                                          # all ok (no testutil fixture-count churn this
                                                        #   round — no new frozen fixtures added)
GOOS=linux go build ./...                              # clean
```

No frozen fixture was touched this round (`result_set.json` and `history_degraded.json` are both
byte-identical to what shipped in the reviewed commit; `git diff` confirms neither file appears in
this round's changes).

---

## Fix round 2

Re-review confirmed I1–I6 fixed. One NEW Important plus three trivial minors from the "Re-review
(fix round 1)" section of `task-4-review.md`. All four addressed; amended into the same single
commit, exact subject/body unchanged, no trailers.

### NEW-1 (Important) — LastSessionID ownership documented and regression-tested

`LastSessionID` (`history.go`) is load-bearing for the I1 fix (round 1) but had shipped with no doc
comment of its own. It now carries an explicit ownership statement: it is owned exclusively by
`checkSessionStartFires` (`assertions.go`), and the daemon **must never write it** — in particular,
never pre-set it to the incoming session's own id before `RunAll` runs, because that would make the
Check see "already counted this session" on the very first `RunAll` of every session, permanently
suppressing the increment and disabling the SevCritical assertion for good. This constraint will
also be carried into Task 5's brief per the coordinator's note.

New regression test:
`TestSessionStartFires_DaemonPreWriteOfLastSessionIDDoesNotWedgeFutureCounting`. It simulates the
exact forbidden daemon-style pre-write (`h.LastSessionID = "sess-a"` before calling `Check` with
`Event.SessionID == "sess-a"`), confirms that one session's miss IS suppressed by the violation (the
Check has no way to distinguish "already counted" from "a caller wrote this behind my back" — which
is precisely why the field is now documented as off-limits), and then proves the violation does
**not** permanently wedge the mechanism: a genuinely new session id afterward counts correctly (1),
and a second consecutive real miss in a third distinct session still degrades at `SevCritical` (2).
A broken I1 fix — one that stopped updating `LastSessionID` at all, or that could never again tell
two sessions apart after any pre-write — would fail the second half of this test.

### NEW-2 (Minor) — freeze sentence added to SessionHistory's own doc comment

`doc.go` already pointed readers at `SessionHistory`'s doc comment for "what \[the frozen fixture\]
means for a future change," but that comment covered only the concurrency/ownership contract (I5),
not the fixture-freeze consequence (I6) — so the pointer led nowhere. `SessionHistory`'s doc comment
(`history.go`) now has its own explicit paragraph: `history_degraded` is declared `"frozen"` in
`MANIFEST.json`, `TestHistoryDegradedGolden_Decodes` compares `json.MarshalIndent` byte-for-byte
against it, any non-`omitempty` field change breaks it under Rule W-2, and a new field needs
`omitempty` plus a zero/absent value in the fixture's own scenario to avoid touching it. `doc.go`
itself needed no change — its pointer is now accurate.

### NEW-3 (Minor) — probePhrase now measures runes, matching M2's fix

`probePhrase` (`assertions.go`) compared `len(line)` — a byte count — against
`customInstrMinPhraseChars`, while round 1's M2 fix moved `SetPrecompactInstr`'s cap to a rune count
specifically because "chars" means runes, not bytes. Both were documented as "characters" but
measured differently. `probePhrase` now uses `utf8.RuneCountInString(line)`, and its doc comment
says explicitly that the count is runes, "matching SetPrecompactInstr's own 256-char cap." No new
test was added specifically for this (the existing `TestProbePhrase`, `TestCustomInstructionsProbePhrase_*`
tests all use ASCII phrases, so byte and rune counts coincide and none of them would have caught this
class of bug in either direction) — flagging that as a residual gap rather than claiming coverage
that doesn't exist.

### NEW-4 (Minor) — TestSessionStartFires_ConsecutiveAbsenceIsPerSession comment and id reuse fixed

The test's third call reused `"sess-a"` as the session id and its comment claimed "the stale marker
from 'a' still names a different (but now stale) session" — backwards: the marker on disk names
`"sess-a"`, and the current run's `Event.SessionID` is also `"sess-a"`, which is EXACTLY why
`checkSessionStartFires`'s "marker exists and names a DIFFERENT session" rule does not fire and the
run reads absent. Relying on that id-reuse coincidence also made the assertion's correctness depend
on which literal id happened to be reused, rather than on the documented rule. Fixed: the third call
now uses a genuinely distinct `"sess-c"`, with the marker file explicitly removed
(`os.Remove(contract.MarkerPath(root))`) beforehand so the "absent" reading comes from "no marker
exists at all," not from any id coincidence — and the comment now states this correctly, including
why a marker naming any id other than the current one would itself read as marker-found (not the
scenario being pinned).

### Verification run (fix round 2)

```
go build ./internal/contract/...                     # clean
go test ./internal/contract/... -race -count=2        # ok, ok
go test ./test/guards/...                              # ok
go run ./tools/devtool fmt                             # no diff
go run ./tools/devtool vet                             # clean
go run ./tools/devtool lint                             # PASS golangci-lint / nomagic / importgraph /
                                                        #   testdeps / bindeps / sleepcheck / stubskips
go test ./...                                          # all ok
```

No frozen fixture touched this round either.
