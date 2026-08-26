# Task 5 review — Commit 5: Stop and SubagentStop capture

**Verdict: APPROVED** (no critical or important findings)
**Commit reviewed:** `7885650` on `feat/sp08-observer-l0`, base `e957564`, worktree `qompack-sp08`.
**Diff:** observer.go (placeholder OnStop removed), stop.go (+393), stop_test.go (+577),
testdata/transcript_tail.jsonl (+7). Matches the brief's file list exactly; no unowned file touched.

## Binding items — verified against the diff

- **Main-agent Stop** (`mainAgentStop`): grammar `Symbol("stop")` behind a `Grammar != nil` guard,
  `st.Turn++`, `Graph.Flush` softed as `stop.flush` (`stageStopFlush = "stop.flush"`), `LastTS = now`.
  Zero store writes and zero index records — asserted directly in
  `TestOnStop_MainAgentIncrementsTurnAndFlushes` (`require.Empty(h.Store.Puts)` / `Records`).
- **SubagentStop, nine steps in order** (`captureSubagent`):
  1. `subagentName(e)` reads exactly one key (`extraAgentKey = "agent"`), unmarshals into a string,
     falls back to `"subagent"` on absent/malformed/non-string/empty. Nil-map indexing is safe.
  2. `subagentSummary`: `responseText(e)` when `ToolResponse` non-empty, else
     `tailAssistantText(e.TranscriptPath, transcriptTailBytes)` with `transcriptTailBytes = 512 << 10`,
     else `""` with a Debug log and the capture still written.
  3. `st.ToolUses[min(st.SubagentSince, len(st.ToolUses)):]` verbatim, with a correct comment on why
     the re-clamp matters (state restored from an older, longer ring). Out-of-diff check: the primary
     clamp exists at `tooluse.go:250` (`st.SubagentSince = max(0, st.SubagentSince-k)` in the same
     operation as ring eviction) and `state.go:158-159` clamps on load — the min() is genuinely a
     third belt, not the only protection.
  4. `SubagentCapture` marshalled in declaration order; local is `capture`, not `cap`. Marshal
     failure softed as `stop.marshal` and returns without an index entry (brief's own asymmetry:
     step 4 returns without Turn++, step 5's Put failure increments — both preserved, and the Put
     side is pinned by `TestOnStop_PutFailureStillIncrementsTurn`, which also asserts no orphan
     index record, no DAG node, and the window staying open).
  5. `PutBytes` with `Tool: "SubagentStop"`, empty Path, `Canon: verbatimOptions()`, `KeepRaw: true`.
     Out-of-diff check: `verbatimOptions()` at `prompt.go:203-208` is the empty-non-nil-Strip,
     MinHash-off shape and stop.go declares no `canon.Options` of its own — genuinely reused.
     Token fallback `EstimateRoot(ctx, res.Root.Chunks, tokens.ClassJSON)` guarded on
     `tok == 0 && o.opt.Tokens != nil`; covered by `TestOnStop_TokensEstimatedWhenTheStoreReportsNone`
     including the record/node price agreement and Pos/PrefixTokens advance.
  6. `SubagentCaptureID(s, t)` = `fmt.Sprintf("%s_%s_%d", "subagent", s, t)` minted at the
     PRE-increment turn; record carries `Subagent: agent`, `ArgsDigest`/`ArgsPreview` from
     `subagentArgs` (`{"agent":…,"summary":…}`). RecordToolUse is softed under `stageIndex` — the
     brief's pseudocode omits the wrapper but decision 7 mandates it; consistent with tooluse.go.
  7. Hand-built `dag.Node{ID: dag.ToolUseNode(id), Kind: KindToolUse, Ref: "SubagentStop",
     Pos: o.advancePos(st, tok), …}`; one `EdgeConsumes` per ref, `From: dag.ToolResultNode(r.ToolUseID)`,
     `To: dag.ToolUseNode(id)` — D-1 direction correct, `Weight: edgeWeight` (no bare literal);
     `o.enrol(st, …)` for segment membership. Out-of-diff check: `graph.go:129/141` confirm enrol's
     member-to-segment direction and advancePos's pre-increment start-position semantics
     (decision 5). `TestOnStop_ConsumesEdges` asserts exactly 2 consumes edges and the explicit
     absence of both reversed edges. No hand-assembled NodeID anywhere in stop.go.
  8. `SubagentSince = len(ToolUses)`, `Turn++`, `LastTS`, `o.count(counterSubagentCapture)`
     (out-of-diff: `observer.go:205` defines it as `"observer.subagent_capture"`), `Graph.Flush`
     softed as `stop.flush`.
  9. `hookio.Empty(), nil`; the only returnable error is `ctx.Err()` in the shared preamble, clock
     read once after the ctx check, session lock held for the method (decisions 7/9/11).
- **tailAssistantText**: stat (rejecting directories), seek to `max(0, size-maxBytes)`, ReadAll,
  partial-first-line drop gated on `off > 0`, backward scan via `assistantText` for the last
  assistant line with at least one `type == "text"` block, texts joined `"\n"`, every failure path
  returns `""`.
- **Tests**: all 12 brief rows present under their exact names (TestRawExtras_* correctly left to
  the amendment in internal/cli, per the brief's own row note), plus 5 additions.
  `TestOnStop_CaptureIsDeterministic` follows the exact recipe — two independent observers via
  `captureObserver` sharing only one real `store.Open` store, frozen `fakeClock`, same session id,
  same two preceding tool uses, byte-identical blobs, same root, `Stats().Objects` unchanged
  (snapshotted after the second observer's tool-use replay so the delta is a statement about the
  capture alone — a sound refinement). `TestOnStop_RetrievalPathG10_1` round-trips a real store and
  additionally re-opens each ref's root — the G10.1 claim is tested end to end, not mocked.
- **Fixture** is a realistic 7-line transcript exercising three distinct skip reasons before the
  target line.
- **Constraints**: imports are within the allowed set (core, dag, grammar, hookio, store, tokens +
  stdlib); no `time.Now()`; no TODO/FIXME; no flagged nomagic literal in code (the grep hits in
  stop.go are all "G10.1" / "§5.8" comment substrings); commit message has a 62-char subject, max
  body line 99 runes, footer exactly `Refs: SP-08, G10.1, §8.1 item 8`, and no attribution trailer
  (verified from `git log -1 --format=%B`).

## Implementer's flagged concerns — judged

- **(a) Conditional partial-line drop (`off > 0`)**: correct and the right reading of the plan
  sentence — the parenthetical "(partial line)" names what is dropped, and with `off == 0` there is
  no partial line; an unconditional drop would lose a short transcript's only assistant message.
  Both directions are pinned (`the_fragment_is_never_the_answer` fails without the drop;
  `a_whole_file_keeps_its_first_line` and `Degrades` fail with it unconditional), and the
  no-newline-in-window case correctly returns `""`. Not plan-conflicting; no finding.
- **(b) Only `type == "text"` blocks feed the summary**: matches the plan's "the Text fields ...
  at least one Type == text" predicate and avoids empty-line padding from tool_use blocks. Correct.
- **(c) Summary bounded by responseText's 4 MiB scan cap (out-of-diff check: `maxScanBytes = 4 << 20`
  at `signals.go:72`, applied before any decoding) plus the 512 KiB tail, but not by
  `runtime.hotPath.maxPayloadBytes`, and the whole summary feeds `subagentArgs` for ArgsDigest**:
  real but bounded cost concern; the brief mandates no clamp (§5.8 owns only the preview cap).
  Recorded as a minor for V3-VERIFY.
- **(d) `(cached)` e2e/integration in the final devtool pass**: acceptable — the full uncached pass
  ran them (integration 335.2 s), and the only subsequent delta was comments/test edits, which Go's
  content-keyed test cache legitimately treats as unchanged.

## Findings

1. **minor — `subagentSummary` falls to the transcript tail when a non-empty `ToolResponse` decodes
   to empty text** (`stop.go`, `subagentSummary`). The brief's step 2 reads "(a) responseText(e)
   when e.ToolResponse is non-empty; (b) else tailAssistantText" — strictly, a non-empty response
   yielding `""` (e.g. `{"content":""}`) should end at (a) with `""`; the implementation tries the
   tail. The deviation is generous, serves G10.1, and can only add information; noted so V3-VERIFY
   reads the branch deliberately rather than assuming the literal (a)/(b) chain.
2. **minor — no upper bound from `runtime.hotPath.maxPayloadBytes` on the summary, and the full
   summary (up to ~4 MiB via responseText) is re-marshalled into `subagentArgs` and hashed by
   ArgsDigest** (`stop.go`, `captureSubagent` step 6). Cost, not correctness — the blob itself is
   the capture and must carry the summary; the digest input size is the avoidable part. The brief
   does not ask for a clamp. Carried to V3-VERIFY (the implementer flagged it too).
3. **minor — `TestOnStop_SummaryFromTranscriptTail` uses the checked-in fixture instead of the
   brief row's "temp JSONL"** (`stop_test.go`). The assertion (`"block one\nblock two"`, last
   assistant line, two text blocks) is exactly the row's, and the fixture is strictly richer (three
   skip reasons); setup-shape deviation only.

## Strengths

TDD evidence is genuine (RED is a build-failure listing of the very symbols stop.go later defines,
and the one first-run failure is candidly described). The determinism and retrieval-path rows run
against the real store rather than fakes. The added failure-path tests (Put failure, token
estimation, degradation matrix) go beyond the brief in exactly the directions decision 7 cares
about. Comments consistently explain why — the D-1 direction, the conditional drop, the
open-window-on-failure choice — at the level a later maintainer needs. 95.8% package coverage
against a 75% floor, with the residue plausibly unreachable.

## Out-of-diff checks performed (one per named risk, all via `git show 7885650:...`)

1. `verbatimOptions()` shape and single definition — `prompt.go:203`.
2. `SubagentSince` clamp companions — `tooluse.go:250`, `state.go:158`.
3. `responseText` cap and decode order — `signals.go:72,275`.
4. `enrol` / `advancePos` semantics — `graph.go:129,141`.
5. `counterSubagentCapture` spelling — `observer.go:205`.
6. Test helpers exist outside the diff (`newHarness`, `newRealStoreObserver`, `readRoot`,
   `findEdge`, `jsonString`, fakes) — `fakes_test.go`, `prompt_test.go`, `supersede_test.go`,
   `graph_test.go`.

No test re-run: the worktree may be under concurrent edit; the report's evidence table is
internally consistent and every behavioural claim I checked is visible in the committed test code.
Had I run one, it would have been
`go test ./internal/observer/ -run 'TestOnStop|TestTailAssistantText' -count=1`.
