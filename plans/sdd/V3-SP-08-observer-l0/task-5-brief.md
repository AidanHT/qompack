# Task brief — Commit 5: Stop and SubagentStop capture

Source: plans/V3-SP-08-observer-l0.md (verbatim excerpts; the plan's line ranges are noted before each excerpt). Exact values, names, signatures and test rows below are binding.

## Controller rulings that OVERRIDE the plan text below where they conflict

- Commit subject unchanged (62 chars).
- `verbatimOptions()` (prompt.go, Commit 4) exists; reuse it. Reading content by root in tests: `st.GetRoot(ctx, root)` / `st.Open(ctx, root) (io.ReadCloser, error)`; the package constructor is `store.Open(root, cfg, deps)`.
- `e.Extra["agent"]` is restored daemon-side by the amendment's `resolveEvent`; in unit tests set `Extra` directly.


---
<!-- plan lines 93-114 -->

### §8.1 — L0 Observer, verbatim

> **Trigger:** `PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStop`
>
> **Responsibilities:**
>
> 1. **Chunk and store.** Run FastCDC over the tool result. Suggested parameters for source text: `min = 1KB`, `target = 4KB`, `max = 16KB` — smaller than backup workloads because source files are smaller. Store novel chunks zstd-compressed; record the chunk list.
>    **Canonicalize first (O2).** Exact-hash dedup is defeated by volatile substrings: timestamps, ANSI escape codes, PIDs, memory addresses, temp-dir paths, and run durations make every `Bash` and test-runner output unique even when semantically identical. Before chunking, apply per-tool canonicalizers that strip or normalize these (store the canonical form; keep the volatile deltas as a tiny side record if byte-exact recovery matters). For content that still differs after canonicalization, a MinHash signature per result detects near-duplicates — "same test suite, one new failure" — and stores the delta against the prior version instead of the full text. Test and build output is the noisiest content class in a coding session; this is where the dedup ratio is won or lost.
> 2. **Emit the tombstone.** Replace the eventual cleared marker with an addressable one:
>    ```
>    [cleared: sha256:a3f2… · 2.4KB · FileRead src/auth.ts · re-expandable]
>    ```
>    This alone closes G3.2 at near-zero cost.
> 3. **Redundancy detection.** If the chunk set is a superset or near-duplicate of a prior read of the same path, mark the earlier one `SUPERSEDED` in the DAG. Superseded reads are the first candidates for eviction and should never appear in a summary.
> 4. **DAG edges.** Record `tool_use → tool_result → assistant_turn → next_tool_use`, plus shared-state edges keyed on file path and symbol name.
> 5. **Sketch updates.** Feed Count-Min and HyperLogLog. Feed the Bloom filter *only* on explicit negative-knowledge events (§8.3).
> 6. **Sequitur.** Append the tool symbol to the action grammar; check for high-multiplicity nonterminals and emit a thrash warning.
> 7. **Verbatim user capture.** Every `UserPromptSubmit` is written immutably to `index/segments.jsonl`. This is the durable version of section 6 of the summary prompt, and unlike section 6 it is never regenerated.
> 8. **Subagent capture.** On `SubagentStop`, store the subagent's returned summary *and*, where available, its tool-result hashes, so the parent has a retrieval path into detail it never held (G10.1).
>
> **Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.


---
<!-- plan lines 139-156 -->

### §3 gap rows this slice closes

| ID | Gap |
|---|---|
| G1.5 | **No task-boundary signals consulted.** Todo completion, passing test runs, git commits are all natural safe points; none are wired to compaction. |
| G2.3 | **Section 6 ("All user messages") is regenerated, not preserved.** It drifts along with everything else despite existing precisely to prevent drift. |
| G3.2 | **Tombstones carry no pointers.** `[Old tool result content cleared]` says a tool ran and nothing else — even though `FileRead`, `Grep`, and `Glob` results are trivially reproducible and `Bash` output is in the session log. |
| G10.1 | **Subagent output is compressed twice.** A subagent returns a summary; that summary is then summarized. The parent never held the detail, so there is no recovery at either level. |

### §9 traceability rows

| Gap | Closed by | Residual |
|---|---|---|
| G1.5 no boundary signals | L0 todo/git/test signals → L3 | — |
| G2.3 user messages regenerated | L0 verbatim capture, L5 verbatim replay | — |
| G3.2 pointerless tombstones | L0 addressable tombstone | — |
| G10.1 subagent double-compression | L0 `SubagentStop` capture | — |


---
<!-- plan lines 640-724 -->

### Resolved decisions (read these before writing code; no decision below is open)

1. **Pipeline order.** §8.1 item 1 says "canonicalize first"; §5.22a says redaction is applied at
   the single choke point `store.Put`/`PutBytes`, *before* canonicalization and chunking. Both are
   satisfied because the observer never chunks, redacts, or canonicalizes by hand: it calls
   `store.PutBytes` with `PutOptions{Tool, Path, Canon}`, and SP-06's store performs
   redact → canonicalize → chunk internally. The observer's contribution to O2 is **per-tool
   canonicalizer selection**, delivered as `PutOptions.Tool` and `PutOptions.Path` (which is what
   `canon.Registry.For(tool, path)` dispatches on) and `PutOptions.Canon.Strip`. The observer's own
   ordering, after the Put returns, is: **index → file version → sketches → DAG → grammar →
   signals/features → tombstone.**
2. **`crlf` is unconditional.** §4 of the architecture: "Content entering the store is CRLF→LF
   normalized by the `crlf` canonicalizer before chunking." `canonOptions` therefore always
   includes `canon.Class("crlf")`, even when `store.canonicalize.enabled` is `false`. The
   with/without-canonicalization measurement of the Phase 1 exit criterion therefore compares
   *crlf-only* against *crlf + the six configured classes*, which is the honest A/B.
3. **§8.1 item 7's destination.** The design names `index/segments.jsonl`. In this architecture the
   segment log is `store.SegmentLog` (SP-06) and carries no per-turn payload field, and W-3 forbids
   adding one. The verbatim requirement is met with three durable artifacts, none of which is ever
   regenerated from a summary: (a) the prompt bytes stored as a content-addressed object via
   `store.PutBytes` with **no optional canonicalization class and no MinHash** (pre-step (a) above
   is what makes that request reach the store at all — see `prompt.go` for exactly how far
   "verbatim" reaches); (b) an append-only `tool_use.jsonl` entry with `Tool: "UserPromptSubmit"`
   and `ID: VerbatimPromptID(...)`; (c) a `KindUserPrompt` DAG node built by `dag.BuildUserPrompt`
   and enrolled in the currently open segment by an `EdgeSequence` running
   `userprompt:<turn> → segment:<id>` — members point **into** the segment, per SP-07 D-1 and
   `dag.BuildSegment`. This is what closes G2.3: the bytes are content-addressed, immutable, and
   reachable by hash forever.
4. **Turn accounting.** `state.Turn` starts at 0. `OnUserPrompt` records at `Turn` then increments.
   `OnToolUse` records at the current `Turn` without incrementing. `OnStop` increments in **both**
   directions — `subagent=false` because the assistant turn has ended, `subagent=true` because the
   capture itself occupies a turn slot (it is recorded at the pre-increment `Turn`, exactly like a
   prompt). Result: a monotone alternating user/assistant turn sequence in which every stored
   artifact carries the turn it was observed at.
5. **`Node.Pos`.** The observer maintains `state.PrefixTokens`, a monotone per-session token
   counter. Every node is created with `Pos = state.PrefixTokens` (its *start* position), then
   `state.PrefixTokens += node.Tokens`. Prompts, tool results and subagent captures all advance it.
6. **Ephemeral.** A tool whose normalized name starts with `mcp__qompack__` is a retrieval result:
   it is recorded with `Ephemeral: true`, is excluded from supersession (in both directions), and
   is not fed to the CMS/HLL/Misra-Gries (retrieval is not exploration). It still gets DAG nodes
   and a tombstone.
7. **Error policy.** No I/O failure ever escapes an `Observer` method. Every stage is wrapped by
   `o.soft(stage, err)`, which increments `obs.Counter("observer.err."+stage)`, logs at `Warn`, and
   returns. The methods return a non-nil error **only** for `ctx.Err()`. Every method returns
   `hookio.Empty()` unless it has a specific reason to emit output — exactly two exist:
   `OnUserPrompt` may emit a thrash warning, and only in `ModeFull`; and `OnSessionStart` returns
   verbatim whatever the `Rehydrator` seam returns for `source == "compact"` or `"clear"`.
   Counters are incremented through the `obs.Counter` value returned by
   `Registry.Counter(name)` using the increment method SP-01 defined on that interface; wrap every
   counter bump in the single helper `func (o *observer) count(name string)`, which is nil-safe on
   `o.opt.Metrics` and is the only place in the package that names an `obs` method — so adapting to
   SP-01's exact spelling (`Inc()` vs `Add(1)`) is a one-line change.
8. **Nil tolerance.** `Grammar`, `Touch`, `Explore`, `Hot`, `Symbols`, `Rehydrate`, `Mode`,
   `OnSignals`, `OnFeatures` and `Metrics` may each be nil; every call site guards. `New` returns an
   error only when `ProjectRoot == ""`, `Store == nil`, `Graph == nil`, `Log == nil`, or
   `Clock == nil`.
9. **Concurrency (two-level locking).** The daemon's worker pool may deliver two events for
   *different* sessions concurrently, so every entry point is written to be race-free under
   `go test -race`. `o.mu` guards **only** the `sess` map and the `stateFile` write; it is held for
   the map lookup/insert and released immediately. Each `sessionState` carries its own
   `mu sync.Mutex`, and every entry point takes it for the remainder of the method, so all work for
   one session is serialized and all mutation of `Turn`, `PrefixTokens`, `Recent`, `ToolUses`,
   `LastTS`, `TodoDone` and `WarnedRules` happens under it. Store, DAG, grammar and sketch calls are
   made while holding the session lock — SP-06/SP-07/SP-03 own their own internal synchronization,
   and one session is inherently sequential in the host anyway. Never take `o.mu` while holding a
   `sessionState.mu`; the shape is always map-lock → copy pointer → map-unlock → session-lock.
10. **Mode gates output, never writes.** §12 (`ModeDegradedPassive`): "L0 and L1 keep running
    (observe, chunk, store, sketches, DAG, verbatim capture …)". Therefore `o.mode()` is consulted
    in exactly one place — the `AdditionalContext` emission in `prompt.go` — and never guards a
    `PutBytes`, `RecordToolUse`, `AddNode`, `AddEdge` or sketch update. A test
    (`TestModePassiveStillWrites`) drives a full session with `Mode() == ModePassive` and asserts
    the store/DAG/sketch call counts are identical to `ModeFull`.
11. **Time.** Every timestamp in this package comes from
    `now := core.UnixMilli(o.opt.Clock.Now().UnixMilli())`, computed once at the top of each entry
    point (after the ctx check) and reused for the record, the DAG nodes and the feature sample, so
    one hook firing has exactly one timestamp. `time.Now()` never appears in `internal/observer`.
12. **`canon.Options.MinHash` type.** §5.6 declares the field as `MinHash MinHashOptions` inside a
    package that imports `sketch` (§3.2), and §5.7 declares `sketch.MinHashOptions`. Write
    `sketch.MinHashOptions{…}` at the call site. If SP-04 shipped a package-local alias
    (`type MinHashOptions = sketch.MinHashOptions`) the literal still compiles unchanged; if SP-04
    shipped a *distinct* struct, that is a §5 divergence and the response is an `arch/` amendment
    request, not a local workaround (§0).

---


---
<!-- plan lines 1531-1626 -->

### `internal/observer/stop.go` (new)

**Responsibility.** §8.1 item 8 / G10.1.

```go
func (o *observer) OnStop(ctx context.Context, e hookio.Event, subagent bool) (hookio.Output, error)
func SubagentCaptureID(s core.SessionID, t core.TurnIndex) core.ToolUseID
func subagentName(e hookio.Event) string
func subagentArgs(agent, summary string) json.RawMessage   // {"agent":…,"summary":…} for ArgsDigest
func subagentSummary(e hookio.Event) string
func tailAssistantText(path string, maxBytes int64) string
```

Both branches begin with the same preamble as the other entry points:
`if ctx.Err() != nil { return hookio.Empty(), ctx.Err() }`, `now := o.now()`,
`st := o.session(e.SessionID); st.mu.Lock(); defer st.mu.Unlock()`.

**Main-agent Stop (`subagent == false`).** Append `grammar.Symbol("stop")` when `Grammar != nil`;
`st.Turn++`; `Graph.Flush(ctx)` (softed as `"stop.flush"`); `st.LastTS = now`; return
`hookio.Empty(), nil`. No store writes: the assistant's own text is not a tool result and Claude
Code's transcript already holds it.

**SubagentStop (`subagent == true`).**

```
1.  agent := subagentName(e)          // e.Extra["agent"], else "subagent"
    // The three-way host-name resolution (subagent_type → agent_name → agent) happens in the HOOK
    // CLIENT, not here: hookio.Event.Extra is tagged `json:"-"`, so it is populated by
    // hookio.ReadEvent in the client process and dropped by ipc.EncodeRequest. Pre-step (b) is what
    // puts one resolved key back — rawExtras sends {"subagent":true,"agent":"<name>"} and
    // resolveEvent restores it into Extra daemon-side — so this function reads ONE key and falls
    // back to the literal "subagent" when it is absent, malformed, or not a non-empty JSON string.
    // Without pre-step (b) every production capture would be named "subagent"; the unit test alone
    // would never have caught it, which is why an end-to-end row asserts the name through the real
    // daemon (test plan, `stop_test.go` and `observer_e2e_test.go`).
2.  summary := subagentSummary(e)
    // (a) responseText(e) when e.ToolResponse is non-empty;
    // (b) else tailAssistantText(e.TranscriptPath, 512<<10);
    // (c) else "" → log Debug, still capture the hash list (the hashes are the point).
3.  refs := make([]SubagentToolRef, 0)
    for _, t := range st.ToolUses[min(st.SubagentSince, len(st.ToolUses)):] {
        refs = append(refs, SubagentToolRef{ToolUseID: t.ID, Root: t.Root.String(),
                                            Tool: t.Tool, Path: t.Path, Bytes: t.Bytes})
    }
4.  capture := SubagentCapture{Session: e.SessionID, Agent: agent, Turn: st.Turn, TS: now,
                               Summary: summary, ToolResults: refs}   // not `cap` — builtin shadow
    blob, err := json.Marshal(capture)   // struct field order ⇒ deterministic bytes
    if err != nil { o.soft("stop.marshal", err); return hookio.Empty(), nil }
5.  res, err := Store.PutBytes(ctx, blob, store.PutOptions{Tool: "SubagentStop", Path: "",
        Canon: verbatimOptions(), KeepRaw: true})     // prompt.go's empty non-nil Strip, MinHash off
    on err: o.soft("stop.put", err); st.Turn++; st.LastTS = now; return hookio.Empty(), nil
    tok := res.Root.Tokens
    if tok == 0 && o.opt.Tokens != nil {
        tok = o.opt.Tokens.EstimateRoot(ctx, res.Root.Chunks, tokens.ClassJSON)  // the blob is JSON
    }
6.  id := SubagentCaptureID(e.SessionID, st.Turn)
    digest, preview := store.ArgsDigest(subagentArgs(agent, summary))   // §5.8 owns the ≤120-byte cap
    Store.RecordToolUse(ctx, store.ToolUseRecord{ID: id, Session: e.SessionID, Turn: st.Turn,
        TS: now, Tool: "SubagentStop", ArgsDigest: digest, ArgsPreview: preview,
        Root: res.Root.Hash, Bytes: int64(len(blob)), Tokens: tok,
        Status: store.StatusOK, Subagent: agent})
7.  Graph.AddNode(dag.Node{ID: dag.ToolUseNode(id), Kind: dag.KindToolUse, Turn: st.Turn, TS: now,
        Pos: o.advancePos(st, tok), Ref: "SubagentStop", Root: res.Root.Hash, Tokens: tok})
    for _, r := range refs { Graph.AddEdge(dag.Edge{From: dag.ToolResultNode(r.ToolUseID),
        To: dag.ToolUseNode(id), Kind: dag.EdgeConsumes, Weight: 1, Turn: st.Turn}) }
    o.enrol(st, dag.ToolUseNode(id))                  // segment membership, graph.go
    // D-1, no exception: the subagent's RESULTS are the earlier/producer end and the capture is the
    // consumer, so every edge runs toolresult:<ref> → tooluse:<capture>. This is the one node set
    // SP-08 still builds by hand — a subagent capture is not a transcript tool call, so there is no
    // dag.Observed* shape for it — and the ids come from dag's constructors, never from string
    // concatenation. `Ref` is the tool name, matching what BuildToolUse puts on a tool-use node.
8.  st.SubagentSince = len(st.ToolUses); st.Turn++; st.LastTS = now
    o.count("observer.subagent_capture"); Graph.Flush(ctx)
9.  return hookio.Empty(), nil
```

Note that step 6 records the capture at the **pre-increment** `st.Turn`, and `SubagentCaptureID`
is minted from the same value, so the id, the record's `Turn`, the DAG node's `Turn` and the
`SubagentCapture.Turn` field all agree — resolved decision 4.

`tailAssistantText(path, maxBytes)`: `os.Stat`, seek to `max(0, size-maxBytes)`, read to EOF, drop
everything before the first `'\n'` (partial line), then scan lines from the end; unmarshal each into
`struct{ Type string "json:\"type\""; IsSidechain bool "json:\"isSidechain\""; Message struct{
Role string "json:\"role\""; Content []struct{ Type, Text string } "json:\"content\"" }
"json:\"message\"" }`; return the concatenation (joined with `"\n"`) of the `Text` fields of the
last line whose `Message.Role == "assistant"` and whose `Content` has at least one `Type == "text"`.
Any stat/open/read/parse failure returns `""` — never an error, never a panic.

**Why this closes G10.1.** The parent's context holds only the subagent's prose summary. After this
hook, `.qompack/objects/` holds that summary *and* a hash list pointing at every tool result the
subagent produced, all of which the parent never held. `expand(subagent_<session>_<turn>)` returns
the capture; each `root` inside it is independently `expand`-able. That is the retrieval path the
gap says does not exist.

---


---
<!-- plan lines 2136-2154 -->

### `stop_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestOnStop_MainAgentIncrementsTurnAndFlushes` | `subagent=false` | `st.Turn` +1; `fakeGraph.FlushCalls == 1`; zero `PutBytes` |
| `TestOnStop_SubagentCapturesSummary` | `ToolResponse: {"content":"Found the bug in retry.ts"}` | `PutBytes` body unmarshals to a `SubagentCapture` with that `Summary` |
| `TestOnStop_SubagentCapturesToolHashes` | three tool uses recorded since the last prompt | `ToolResults` has 3 refs with the right ids, roots, tools, paths |
| `TestOnStop_SubagentWindowStartsAtLastPrompt` | prompt, 2 tool uses, subagent stop, 1 tool use, subagent stop | first capture 2 refs, second capture 1 ref |
| `TestOnStop_SubagentNameFromExtra` | `Extra["agent"] = "\"code-reviewer\""`, as pre-step (b)'s `resolveEvent` restores it from `Raw` | `Agent == "code-reviewer"`, and the same value on `ToolUseRecord.Subagent` |
| `TestOnStop_SubagentNameFallback` | no `Extra` keys, or `Extra["agent"]` holding a number or an object | `Agent == "subagent"` |
| `TestRawExtras_ResolvesTheSubagentNameClientSide` (`internal/cli`) | an `observe stop --subagent` payload whose `Extra` carries `subagent_type` (and separately: only `agent_name`; only `agent`; a numeric `subagent_type` plus a string `agent_name`; none of the three) | `rawExtras` emits `{"subagent":true,"agent":"<name>"}` for the first four in that preference order, and exactly `{"subagent":true}` for the last |
| `TestOnStop_SummaryFromTranscriptTail` | empty `ToolResponse`; temp JSONL whose last assistant line has two text blocks | `Summary == "block one\nblock two"` |
| `TestOnStop_TranscriptMissingIsSilent` | `TranscriptPath` points at a nonexistent file | `Summary == ""`, capture still written, no error |
| `TestOnStop_EmptySummaryStillStoresHashes` | no response, no transcript, 2 tool uses | capture written with 2 refs |
| `TestOnStop_ConsumesEdges` | 2 refs | two `EdgeConsumes`, each running `dag.ToolResultNode(ref) → dag.ToolUseNode(captureID)` — the refs are the earlier/producer end under D-1 — and none in the reverse direction |
| `TestOnStop_CaptureIsDeterministic` | two *independent* observers over a real store, each with a fresh session state, a `FakeClock` frozen at the same instant, the same session id and the same two preceding tool uses | both `PutBytes` bodies are byte-identical and produce the same root hash; `Stats().Objects` is unchanged after the second capture. (Within one session the capture is intentionally *not* repeatable: `Turn` advances and the id changes, which is what makes each capture addressable.) |
| `TestOnStop_RetrievalPathG10_1` | real store; capture then `store.ToolUse(SubagentCaptureID(...))` then `Open(root)` | JSON round-trips to the same `SubagentCapture` |
| `TestTailAssistantText_PartialFirstLine` | file whose truncation window starts mid-line | the partial line is discarded, the last complete assistant line is returned |


---
<!-- plan lines 2458-2466 -->

### Commit 5 — `feat(observer): Stop and SubagentStop capture with subagent tool-result hashes`

- [ ] Write `stop_test.go` and add `internal/observer/testdata/transcript_tail.jsonl`. Confirm
      failure.
- [ ] Add `stop.go`.
- [ ] Verify `TestOnStop_RetrievalPathG10_1` round-trips a real capture out of a real store.
- [ ] `go run ./tools/devtool test-race` — green.
- [ ] Footer: `Refs: SP-08, G10.1, §8.1 item 8`

