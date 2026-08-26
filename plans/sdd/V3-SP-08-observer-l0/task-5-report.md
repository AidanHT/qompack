# Task 5 report — Commit 5: Stop and SubagentStop capture

**Status:** COMPLETE
**Commit:** `7885650` — `feat(observer): Stop and SubagentStop capture with subagent tool-result hashes`
**Branch:** `feat/sp08-observer-l0`, one commit on `e957564`. Working tree clean.

## Files

```
internal/observer/observer.go   (minimal OnStop removed; the placeholder note now names two
                                 entry points instead of three)
internal/observer/stop.go       (new — OnStop, mainAgentStop, captureSubagent, SubagentCapture,
                                 SubagentToolRef, SubagentCaptureID, subagentName, subagentArgs,
                                 subagentSummary, tailAssistantText, assistantText)
internal/observer/stop_test.go  (new — 12 brief rows + 5 added, plus helpers)
internal/observer/testdata/transcript_tail.jsonl  (new fixture, 7 lines)
4 files changed, 981 insertions(+), 18 deletions(-)
```

## RED

`stop_test.go` and the fixture were written first, with `stop.go` held out of the tree.
`go test -count=1 ./internal/observer/`:

```
# github.com/qompack/qompack/internal/observer [github.com/qompack/qompack/internal/observer.test]
internal\observer\stop_test.go:33:10: undefined: subagentStop
internal\observer\stop_test.go:58:44: undefined: SubagentCapture
internal\observer\stop_test.go:60:14: undefined: SubagentCapture
internal\observer\stop_test.go:66:33: undefined: SubagentCapture
internal\observer\stop_test.go:101:36: undefined: stopSymbol
internal\observer\stop_test.go:120:19: undefined: subagentStop
internal\observer\stop_test.go:193:39: undefined: extraAgentKey
internal\observer\stop_test.go:207:18: undefined: extraAgentKey
internal\observer\stop_test.go:208:18: undefined: extraAgentKey
internal\observer\stop_test.go:209:18: undefined: extraAgentKey
internal\observer\stop_test.go:209:18: too many errors
FAIL	github.com/qompack/qompack/internal/observer [build failed]
```

`stop.go` was then added and the minimal `OnStop` deleted from `observer.go`. The first run after
that produced exactly one real failure — `TestOnStop_SubagentCapturesToolHashes` counted the
capture's own index record among the three tool-use records. The assertion was corrected to slice
`h.Store.Records[:3]` and to assert the fourth record IS the capture.

## GREEN

Every command run separately, exit code checked on the command itself (never through a pipe that
would mask it).

| Command | Result |
|---|---|
| `go run ./tools/devtool fmt` | exit 0, no rewrites |
| `go run ./tools/devtool lint` | golangci-lint / nomagic / importgraph / testdeps / bindeps / sleepcheck / stubskips / docmarkers / coveragefloors **all PASS**; `runpatterns` FAIL with the expected shrunken set (below) |
| `go run ./tools/devtool vet` | exit 0 |
| `go test -count=1 -timeout=30m ./internal/observer/...` | `ok internal/observer 3.801s`, `ok internal/observer/observertest 2.065s` |
| `go test -race -count=1 -timeout=30m ./internal/observer/...` | `ok internal/observer 7.169s`, `ok internal/observer/observertest 2.745s` |
| `go run ./tools/devtool test` | exit 0 across the whole tree, 64 packages (`internal/observer` 3.554s; `test/integration` 335.2s in the first full pass) |
| `go run ./tools/devtool cover` | **`OK observer: 95.8% >= floor 75%`**; every other floor OK |
| `go run ./tools/devtool check-commit-msg <msg>` | exit 0 |

The seventeen focused rows:

```
--- PASS: TestOnStop_MainAgentIncrementsTurnAndFlushes
--- PASS: TestOnStop_SubagentCapturesSummary
--- PASS: TestOnStop_SubagentCapturesToolHashes
--- PASS: TestOnStop_SubagentWindowStartsAtLastPrompt
--- PASS: TestOnStop_SubagentNameFromExtra
--- PASS: TestOnStop_SubagentNameFallback                     (6 sub-cases)
--- PASS: TestOnStop_SummaryFromTranscriptTail
--- PASS: TestOnStop_TranscriptMissingIsSilent
--- PASS: TestOnStop_EmptySummaryStillStoresHashes
--- PASS: TestOnStop_TokensEstimatedWhenTheStoreReportsNone   (added)
--- PASS: TestOnStop_PutFailureStillIncrementsTurn            (added)
--- PASS: TestOnStop_ConsumesEdges
--- PASS: TestOnStop_CaptureIsDeterministic
--- PASS: TestOnStop_RetrievalPathG10_1
--- PASS: TestTailAssistantText_PartialFirstLine              (3 sub-cases)
--- PASS: TestTailAssistantText_Degrades                      (added)
--- PASS: TestSubagentArgs_IsTheToolInputACaptureDoesNotHave  (added)
--- PASS: TestSubagentCaptureID                               (added)
```

Per-function coverage of the new file:

```
SubagentCaptureID 100.0%   OnStop 100.0%          onStop 100.0%          mainAgentStop 100.0%
captureSubagent    94.1%   subagentName 100.0%    subagentArgs 100.0%    subagentSummary 100.0%
tailAssistantText  88.5%   assistantText 100.0%
```

The residue is unreachable without fault injection: `json.Marshal` failing on a struct of strings,
ints and a slice of those; and `Seek` / `ReadAll` failing on a file just stat'd and opened.

`TestRawExtras_ResolvesTheSubagentNameClientSide` was NOT duplicated — it landed with the amendment
in `internal/cli` and still passes there.

## Lint plan-gate: the remaining set

Was **6** accepted patterns before this commit; now **3**, and every one is a `TestOnSessionStart*`
/ `TestOnSessionEnd*` row — exactly the expected residue, **no new entry**:

```
plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:358  -run "TestOnSessionStart_CompactDelegates|TestOnSessionStart_CompactWithoutRehydratorIsEmpty|TestOnSessionStart_ClearDelegates"
plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:360  -run "TestOnSessionStart_StartupOpensSegment|TestOnSessionEnd_Order"
plans/V6-VERIFY-production-readiness-and-uat.md:241             -run "TestOnSessionStart|TestOnSessionEnd"
```

Cleared by this commit, exactly the three the brief predicted:

```
plans/V3-VERIFY-observer-and-negative-knowledge.md:392   -run "TestOnStop|TestTailAssistantText"
plans/V5-VERIFY-commands-selection-grammar-and-refinements.md:271  -run "TestOnStop_|TestTailAssistantText_"
plans/V6-VERIFY-production-readiness-and-uat.md:239      -run "TestOnStop_RetrievalPathG10_1"
```

Commit 6 (`session.go`) clears the remaining three. The two standing waivers
(`V2-SP02-handoff.md:52`, `V2-VERIFY:318`, both `TestImports`) are unchanged.

## What the implementation does

**Main-agent Stop** (`subagent == false`) is the brief's four statements and nothing else: append
`grammar.Symbol("stop")` when `Grammar != nil`, `st.Turn++`, `Graph.Flush(ctx)` softed as
`"stop.flush"`, `st.LastTS = now`. Zero store writes and zero index records, asserted directly.

**SubagentStop** is the nine steps verbatim: name from `Extra["agent"]` with the `"subagent"`
fallback; summary from `responseText` then the transcript tail then `""`; refs sliced from
`st.ToolUses[min(st.SubagentSince, len(st.ToolUses)):]`; `SubagentCapture` marshalled in
declaration order; `PutBytes` with `Tool: "SubagentStop"`, `verbatimOptions()` — reused from
`prompt.go`, not re-rolled — and `KeepRaw`; the `EstimateRoot(..., tokens.ClassJSON)` token
fallback; `RecordToolUse` at the pre-increment turn under `SubagentCaptureID` with
`Subagent: agent`; a hand-built `dag.ToolUseNode(id)` of `KindToolUse` with `Ref: "SubagentStop"`,
plus one `EdgeConsumes` per ref running `toolresult:<ref>` to `tooluse:<capture>`; `enrol`;
`SubagentSince = len(ToolUses)`, `Turn++`, `LastTS = now`; the `observer.subagent_capture` counter;
`Graph.Flush`.

The local variable is `capture`, never `cap`. Edge weight uses the package's existing `edgeWeight`
constant (`float32 = 1`) rather than a bare literal.

## Three readings worth naming

1. **The partial-line drop is conditional on `off > 0`.** The plan sentence reads "seek to
   `max(0, size-maxBytes)`, read to EOF, drop everything before the first newline (partial line)".
   Taken unconditionally, every transcript small enough to be read whole would lose its first
   line — including a one-line transcript whose only assistant message IS that line. The
   parenthetical names what is being dropped, and when `off == 0` there is no partial line, so the
   drop is gated on having actually truncated. Both directions are pinned by test and both were
   mutation-checked: forcing the drop off makes
   `TestTailAssistantText_PartialFirstLine/the_fragment_is_never_the_answer` fail (it returns
   `FRAGMENT-THAT-MUST-NOT-BE-RETURNED`), and forcing it unconditional makes
   `/a_whole_file_keeps_its_first_line` and `TestTailAssistantText_Degrades` fail.

2. **Only blocks whose `type` is `"text"` contribute to the joined summary.** The plan says "the
   Text fields of the last line whose ... Content has at least one Type == text". An assistant turn
   that also made a tool call carries `tool_use` blocks with no text at all, so joining every
   block's Text would pad the summary with empty lines. Filtering to text blocks is what makes the
   fixture's two-block line yield exactly the two texts joined by one newline.

3. **The marshal-failure branch returns WITHOUT incrementing the turn**, while the Put-failure
   branch increments. That asymmetry is the brief's own (step 4 vs step 5) and is preserved rather
   than smoothed; the marshal branch is unreachable for this struct in practice.

## The fixture

`internal/observer/testdata/transcript_tail.jsonl` is a 7-line transcript in the host's real shape:
a `summary` meta line, a `user` line, an earlier single-block assistant line, an assistant line
carrying only a `tool_use` block, the two-text-block assistant line the scan must land on, another
`tool_use`-only assistant line AFTER it, and a `user` tool_result line last. The scan therefore
walks past three different skip reasons — non-assistant role, no text block, and a line shape that
does not unmarshal into the subset struct — before reaching the answer, so
`TestOnStop_SummaryFromTranscriptTail` is a statement about the scan rather than about a one-line
file. The `TestTailAssistantText_*` rows build their own temp files because the truncation window
has to be positioned byte-exactly.

## Self-review

- **`verbatimOptions()` reused, not re-rolled.** `grep -n 'canon.Options' internal/observer/*.go`
  returns two sites: `prompt.go`'s `verbatimOptions` and `tooluse.go`'s `canonOptions`. `stop.go`
  declares none and constructs none.
- **No hand-assembled NodeIDs.** Every id in `stop.go` comes from `dag.ToolUseNode` /
  `dag.ToolResultNode`; grepping the file for the literal node prefixes returns nothing.
- **No `time.Now()`.** The clock is read once at the top of `onStop`, before the branch, and the
  same value reaches the record, the node and the capture's own TS field.
- **Locking** follows decision 9 exactly: `o.session()` (which takes and releases `o.mu`), then
  `st.mu.Lock(); defer st.mu.Unlock()` for the remainder. `o.mu` is never taken under `st.mu`. The
  race suite is green.
- **No I/O failure escapes.** Every store, graph and flush call in the file is wrapped by `o.soft`;
  the only non-nil error either branch can return is `ctx.Err()`, which
  `TestObserver_EveryEntryPointReturnsCtxErr` already covers for `OnStop`.
- **Nil tolerance.** `Grammar` and `Tokens` are guarded and `Metrics` goes through the nil-safe
  `o.count`. `newHarness` leaves both `Grammar` and `Tokens` nil by default, so every row that does
  not opt in exercises the nil path.
- **observertest** — `stop_and_subagent_stop_are_both_accepted` and the `OnStop` row of
  `every_entry_point_tolerates_a_malformed_event` both still pass against the real implementation.
  The suite's `fixtureTranscript` is a nonexistent path, which the silent-degrade branch absorbs.
- **`transcriptLine.Type` / `IsSidechain`** are parsed but not filtered on. That is deliberate and
  documented in the struct's own comment: a subagent's lines are tagged `isSidechain` inside the
  parent's transcript, so narrowing the scan to them would look tighter and would return `""` on
  any host build that spells or omits the tag differently. The predicate stays the one the plan
  states.
- **Commit message** verified with `check-commit-msg` (exit 0) and re-read with
  `git log -1 --format=%B`: subject is 62 characters after the type/scope prefix, longest body line
  is 99 runes, the footer is exactly `Refs: SP-08, G10.1, §8.1 item 8`, and a grep for
  `co-authored-by|signed-off-by|generated with|claude-session|robot emoji` returns nothing.

## Concerns for V3-VERIFY

- `TestTailAssistantText_PartialFirstLine/a_later_complete_line_wins` documents rather than
  discriminates: because the scan runs backwards, a later complete assistant line wins with or
  without the drop. The discriminating sibling is `the_fragment_is_never_the_answer`, which is the
  one that fails when the drop is removed.
- The summary is bounded only by `responseText`'s own scan cap and by `transcriptTailBytes`
  (512 KiB); it is NOT additionally clamped to `runtime.hotPath.maxPayloadBytes` the way a tool
  result body is, and the whole of it also goes into `subagentArgs` for the args digest. The brief
  does not ask for that clamp, but it is worth a look if a host ever returns a very large subagent
  reply.
- `test/e2e` and `test/integration` show `(cached)` in the final `devtool test` pass. That is
  honest: the only change between the full uncached pass (which ran them, 335.2 s for integration)
  and the final one was a comment in `stop.go` plus test-file edits, and a comment-only change
  produces a byte-identical test binary, which is what Go's content-addressed test cache keys on.
