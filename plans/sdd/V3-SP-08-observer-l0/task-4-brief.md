# Task brief — Commit 4: verbatim UserPromptSubmit capture

Source: plans/V3-SP-08-observer-l0.md (verbatim excerpts; the plan's line ranges are noted before each excerpt). Exact values, names, signatures and test rows below are binding.

## Controller rulings that OVERRIDE the plan text below where they conflict

- Commit subject: `feat(observer): verbatim UserPromptSubmit capture and thrash warnings`.
- `features.go`/`features_test.go` ALREADY LANDED in Commit 2 — do not re-create them; `prompt.go` already exists with `collectThrash`/`pendingThrashLines` — add `OnUserPrompt`, `VerbatimPromptID`, `verbatimOptions`, `promptArgs` to it.
- Extend Commit 2's `TestModePassiveStillWrites` and `TestOnToolUse_ConcurrentSessionsRaceFree` so their sessions include prompts (the plan's "only OnUserPrompt's AdditionalContext differs" clause).
- `hookio` has no `UserPromptSubmitOutput` constructor and this branch may not edit `internal/hookio`: build `&hookio.HSO{HookEventName: "UserPromptSubmit", AdditionalContext: …}` by hand with a one-line comment saying the constructor is deferred to V3-VERIFY.
- Post-amendment store semantics: the MinHash opt-out is honoured only when `Canon.Strip` is non-nil, so `verbatimOptions()` must set BOTH `Strip: []canon.Class{}` and `MinHash: sketch.MinHashOptions{Enabled: false}`; `Canon.KeepDeltas` is ignored by the store — `KeepRaw: true` is what preserves deltas.
- Reading content by root in tests: `st.GetRoot(ctx, root)` / `st.Open(ctx, root) (io.ReadCloser, error)`; the package constructor is `store.Open(root, cfg, deps)`.


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
<!-- plan lines 1429-1530 -->

### `internal/observer/prompt.go` (new)

**Responsibility.** §8.1 item 7 and G2.3.

```go
func (o *observer) OnUserPrompt(ctx context.Context, e hookio.Event) (hookio.Output, error)
func VerbatimPromptID(s core.SessionID, t core.TurnIndex) core.ToolUseID
func verbatimOptions() canon.Options                        // the "no optional class" Put options
func promptArgs(prompt string) json.RawMessage              // {"prompt":<text>} for store.ArgsDigest
func (o *observer) collectThrash(st *sessionState)          // called from OnToolUse step 11
func (o *observer) pendingThrashLines(st *sessionState) []string
```

Algorithm:

```
1.  if ctx.Err() != nil { return hookio.Empty(), ctx.Err() }
    if e.Prompt == "" { return hookio.Empty(), nil }
    now := o.now(); st := o.session(e.SessionID); st.mu.Lock(); defer st.mu.Unlock()
2.  body := []byte(e.Prompt)
3.  res, err := Store.PutBytes(ctx, body, store.PutOptions{
        Tool: "UserPromptSubmit", Path: "",
        Canon: verbatimOptions(),
        KeepRaw: true, Ephemeral: false })
    // verbatimOptions() is canon.Options{Strip: []canon.Class{}, MinHash: sketch.MinHashOptions{
    //     Enabled: false}} — an EMPTY, NON-NIL Strip: "no optional class", not "every class".
    // Pre-step (a) is what makes the store honour both fields; see the note below for exactly how
    // far "verbatim and immutably" (§7.3, §8.1 item 7) reaches.
    on err: o.soft("prompt.put", err) and continue to step 6 with a zero root
4.  id := VerbatimPromptID(e.SessionID, st.Turn)
    tok := res.Root.Tokens; if tok == 0 && Tokens != nil { tok = Tokens.EstimateString(e.Prompt, tokens.ClassProse) }
    digest, preview := store.ArgsDigest(promptArgs(e.Prompt))   // §5.8 owns the ≤120-byte cap
    Store.RecordToolUse(ctx, store.ToolUseRecord{ID: id, Session: e.SessionID, Turn: st.Turn,
        TS: now, Tool: "UserPromptSubmit",
        ArgsDigest: digest, ArgsPreview: preview,
        Root: res.Root.Hash, Bytes: int64(len(body)), Tokens: tok,
        Status: store.StatusOK})
5.  dag.BuildUserPrompt(o.opt.Graph, dag.ObservedPrompt{Turn: st.Turn, TS: now,
        Pos: o.advancePos(st, tok), Tokens: tok, Ref: string(id)})
    // emits userprompt:<turn> and userprompt:<turn> --consumes--> assistant:<turn>: the prompt is
    // the producer end (D-1), and §4.4 makes this the ONLY path by which a backward slice from a
    // tool use deep in a session reaches the request that set it off.
    o.enrol(st, dag.UserPromptNode(st.Turn))          // segment membership, graph.go
6.  if Grammar != nil { Grammar.Append(grammar.Symbol("user")) }
7.  st.LastPromptTurn = st.Turn; st.SubagentSince = len(st.ToolUses); st.Turn++
8.  o.recordRecent(st, "user", nil, body, now)
    if fs, ok := o.features(st, now); ok && OnFeatures != nil { OnFeatures(e.SessionID, fs) }
    st.LastTS = now                                  // after features(), per features.go
9.  out := hookio.Empty()
    if o.mode() == ModeFull {
        if lines := o.pendingThrashLines(st); len(lines) > 0 {
            out.HookSpecificOutput = &hookio.HSO{HookEventName: "UserPromptSubmit",
                AdditionalContext: strings.Join(lines, "\n")}
        }
    }
    return out, nil
```

**The one deliberate exception to resolved decision 2, and exactly how far it reaches.** The prompt
is stored with an **empty, non-nil** `Strip`, which `canon.gateSet` reads as "exactly these classes
— i.e. none — plus the always-on structural ones", and with MinHash off. §8.1 item 7 and §7.3 both
say "verbatim and immutably", and a user prompt is not file content read on two platforms, so there
is no dedup space to fork and nothing to gain from stripping timestamps, ANSI, PIDs, addresses,
tmp paths or durations out of the thing the user typed. This is written down here so a later
reviewer does not "fix" the inconsistency with `tooluse.go`.

Three things it does **not** mean, all of them structural and none of them optional:

- `Strip: nil` would be the *opposite* request. `internal/canon/classes.go`: *"A nil Strip means
  'every class' — the documented meaning of the zero Options."* An earlier draft of this plan asked
  for `Strip: nil` and got the strongest canonicalization available.
- `crlf` and `paths` still run. `canon.alwaysOn = {ClassCRLF, ClassPaths}` is *"applied regardless
  of Options.Strip"*, and Appendix C cannot disable either, because §4 makes CRLF→LF normalization a
  precondition of cross-platform dedup. "Without even the unconditional `crlf` class" is impossible
  by construction, and the plan does not claim it.
- Redaction still runs. `store.PutBytes` is REDACT → canonicalize → chunk (§5.22a, §13 invariant 7),
  and no caller may opt out: a credential pasted into a prompt must not reach `objects/` in the
  clear, where content-addressing makes it undeletable.

What "verbatim" therefore guarantees is precise and testable: the stored object is the user's bytes
with nothing removed but secrets and line-ending/separator normalization, no near-duplicate
signature, no delta-against-prior encoding, and no rewrite ever. `KeepRaw: true` means the store
keeps the canonicalizer's deltas, so `canon.Restore` reconstructs the pre-normalization bytes
exactly; between that and the immutable `tool_use.jsonl` entry, nothing the user typed is lost.
`stop.go`'s capture blob takes the same `verbatimOptions()` treatment for the same reason: it is
JSON this package generated, not host content.

**Never regenerated.** Nothing in `internal/observer` ever rewrites a prompt object, a prompt
`tool_use` entry, or a prompt DAG node. `store.PutBytes` is content-addressed and
`RecordToolUse` appends; there is no update path. A test asserts that submitting the same prompt
text twice yields two distinct records with the same root hash and that `objects/` grew by zero
chunks the second time.

**Thrash lines.** `collectThrash` calls `o.opt.Grammar.Thrash(thrashMinUses)` and appends to
`st.PendingThrash` each returned rule whose `r.ID` is not already a key of `st.WarnedRules`;
`pendingThrashLines` drains `st.PendingThrash` (setting it to nil), sets `st.WarnedRules[r.ID] = true`
for each drained rule, and renders each with `grammar.FormatWarning(grammar.Warning{Rule: r,
Repeats: r.Uses, Message: "repeated action cycle detected", Turns: nil})`. With SP-15's stub
`Thrash` returning nil, this whole path is inert — which is the wave-2 expectation.

---


---
<!-- plan lines 2118-2135 -->

### `prompt_test.go`

| Test | Setup | Expected |
|---|---|---|
| `TestOnUserPrompt_StoresVerbatim` | prompt `"fix the pgbouncer 1.18 pool bypass"` | `PutBytes` receives exactly those bytes; `Canon.Strip != nil && len(Canon.Strip) == 0` (the empty-non-nil "no optional class" request, **not** `nil`, which `canon` reads as "every class"); `Canon.MinHash.Enabled == false`; `KeepRaw == true` |
| `TestOnUserPrompt_VerbatimAgainstARealStore` | real store on `t.TempDir()` with the six strip classes enabled in config; prompt containing a curly apostrophe, an ISO-8601 timestamp and `PID 4711` | `store.Open(root)` returns the prompt byte for byte, timestamp and PID intact, apostrophe intact — the assertion pre-step (a) exists for; a store without the amendment fails this row |
| `TestOnUserPrompt_RecordsIndexEntry` | same | `RecordToolUse` with `Tool == "UserPromptSubmit"`, `ID == "prompt_<session>_0"` |
| `TestOnUserPrompt_TurnIncrements` | two prompts | ids `prompt_s_0`, `prompt_s_1`; `st.Turn == 2` |
| `TestOnUserPrompt_DAGNodeAndSegmentEdge` | `st.Segment == 3`, first prompt of the session | node `dag.UserPromptNode(0)` (`userprompt:0` — no session component) of `KindUserPrompt`; `EdgeConsumes` `userprompt:0 → dag.AssistantNode(0)` from `dag.BuildUserPrompt`; `EdgeSequence` `userprompt:0 → dag.SegmentNode(3)` for membership |
| `TestOnUserPrompt_NeverRegenerated` | same prompt text submitted twice against a real store | two records, identical `Root`; `Stats().Objects` unchanged after the second |
| `TestOnUserPrompt_EmptyPromptIgnored` | `Prompt: ""` | zero store calls, `hookio.Empty()` |
| `TestOnUserPrompt_GrammarSymbolAppended` | fakeGrammar | `Append("user")` called once |
| `TestOnUserPrompt_ThrashWarningInFullMode` | fakeGrammar returning one rule with `Uses: 11` | output `HSO.AdditionalContext` non-empty, `HookEventName == "UserPromptSubmit"` |
| `TestOnUserPrompt_NoThrashWarningInPassiveMode` | same, `Mode() == ModePassive` | `hookio.Empty()` (§12 forbids injection when degraded) |
| `TestOnUserPrompt_ThrashWarnedOncePerRule` | same rule returned on two prompts | additionalContext on the first only |
| `TestOnUserPrompt_PutFailureStillIncrementsTurn` | `PutBytes` errors | `observer.err.prompt.put == 1`; `st.Turn == 1` |
| `TestVerbatimPromptID` | `("abc", 7)` | `"prompt_abc_7"` |


---
<!-- plan lines 2449-2457 -->

### Commit 4 — `feat(observer): verbatim UserPromptSubmit capture and BOCD feature emission`

- [ ] Write `prompt_test.go` and `features_test.go` in full. Confirm failure.
- [ ] Add `prompt.go`, `features.go`.
- [ ] Assert in review that no code path in the package rewrites a prompt object or record
      (`grep -n 'UserPromptSubmit' internal/observer` reviewed line by line).
- [ ] `go run ./tools/devtool test cover` — `internal/observer` at or above the 75% floor (§6.4).
- [ ] Footer: `Refs: SP-08, G2.3, §8.1 item 7, §6.6`

