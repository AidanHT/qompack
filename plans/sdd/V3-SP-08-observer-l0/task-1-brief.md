# Task brief — Commit 1: addressable tombstones, tool classification, task-boundary signals

Source: plans/V3-SP-08-observer-l0.md (verbatim excerpts; the plan's line ranges are noted before each excerpt). Exact values, names, signatures and test rows below are binding.

## Controller rulings that OVERRIDE the plan text below where they conflict

- Commit subject (64-char cap after `type(scope): `): `feat(observer): addressable tombstones, tool tables, task-boundary signals`.
- `tombstone_test.go` ships FIVE tests, not four: the fifth is `TestHumanBytes_NeverEmitsASpaceBeforeTheUnit`; it and `TestHumanBytes_UsesTheBinaryDivisor` stay untouched and must pass.
- `Signals.Paths`: `ExtractSignals` returns RAW host paths as submitted (the observer normalizes to paths.Key form in tooluse.go before `OnSignals`); correct the shipped doc comment on `Signals.Paths` (signals.go) to say exactly that. When no path is present, `Paths` must be nil (not `[]string{}`): `internal/observer/observertest/behaviour.go:289-292` requires `ExtractSignals(hookio.Event{}) == Signals{}`.
- `testdata/golden/observer/` does not exist yet; create it with `tombstones.txt`.
- The `docs/adr/` ADR is Commit 7's, not yours.


---
<!-- plan lines 93-121 -->

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

### §2.2 — the compactable tool set (what a tombstone may replace)

```
FileRead, Bash/PowerShell, Grep, Glob, WebSearch, WebFetch, FileEdit, FileWrite
```

> Only high-volume, reproducible results are targeted. AgentTool and MCP results are preserved.

---
<!-- plan lines 426-527 -->

### Produces (relied on by later subplans)

```go
// package observer — §5.21, normative and unchanged
type Observer interface {
    OnToolUse(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnUserPrompt(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnStop(ctx context.Context, e hookio.Event, subagent bool) (hookio.Output, error)
    OnSessionStart(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnSessionEnd(ctx context.Context, e hookio.Event) (hookio.Output, error)
}
func Tombstone(rec store.ToolUseRecord) string
type Signals struct{ TodoCompleted, TestPassed, GitCommit bool; Paths []string }
func ExtractSignals(e hookio.Event) Signals

// package observer — additions SP-08 makes inside the package it owns
type Mode uint8
const (ModeFull Mode = iota; ModePassive)

type SymbolLister interface{ Names(path string, b []byte) []string }

type Rehydrator interface {                              // implemented by SP-11 in wave 3
    OnCompact(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnClear(ctx context.Context, e hookio.Event) (hookio.Output, error)
}

type FeatureSample struct {                              // daemon maps this to scheduler.Features
    Turn            core.TurnIndex
    TS              core.UnixMilli
    PathJaccard     float64
    ToolShift       float64
    LexicalCohesion float64
    GapSeconds      float64
    TodoTransition  float64
}

type TestOutcome uint8
const (TestUnknown TestOutcome = iota; TestPass; TestFail)
func ExtractTestOutcome(e hookio.Event) TestOutcome
func PathsFromInput(tool string, in json.RawMessage) []string
func NormalizeToolName(hostName string) string           // "Read" → "FileRead"
func IsCompactable(tool string) bool                     // §2.2 set
func TombstoneNote() string                              // the expand affordance line

type SubagentToolRef struct {
    ToolUseID core.ToolUseID `json:"tool_use_id"`
    Root      string         `json:"root"`
    Tool      string         `json:"tool"`
    Path      string         `json:"path"`
    Bytes     int64          `json:"bytes"`
}
type SubagentCapture struct {
    Session     core.SessionID    `json:"session"`
    Agent       string            `json:"agent"`
    Turn        core.TurnIndex    `json:"turn"`
    TS          core.UnixMilli    `json:"ts"`
    Summary     string            `json:"summary"`
    ToolResults []SubagentToolRef `json:"tool_results"`
}
func VerbatimPromptID(s core.SessionID, t core.TurnIndex) core.ToolUseID  // "prompt_<s>_<t>"
func SubagentCaptureID(s core.SessionID, t core.TurnIndex) core.ToolUseID // "subagent_<s>_<t>"

type Options struct {
    ProjectRoot string
    Cfg         config.Config
    Store       store.Store
    Graph       dag.Graph
    Grammar     grammar.Sequitur
    Touch       *sketch.CMS
    Explore     *sketch.HLL
    Hot         *sketch.MisraGries
    Tokens      tokens.Estimator
    Symbols     SymbolLister
    Rehydrate   Rehydrator
    Mode        func() Mode
    OnSignals   func(core.SessionID, Signals)
    OnFeatures  func(core.SessionID, FeatureSample)
    Log         logging.Logger
    Metrics     obs.Registry
    Clock       core.Clock
}
func New(o Options) (Observer, error)

// Persister lets the daemon's idle loop checkpoint observer state. The value returned by New
// ALWAYS satisfies it; observer_ops.go performs the single guarded assertion in the codebase
// (`p, ok := obsv.(Persister)`) and skips the idle registration when ok is false, so a future
// alternate Observer implementation cannot panic the daemon.
type Persister interface{ Persist(ctx context.Context) error }
```

**Import discipline (§3.2).** The §3.2 allow-table *permits* `observer` to import
`hookio store chunk canon sketch dag grammar negknow tokens` plus the foundation, but a permission
is a ceiling rather than an instruction: SP-08 declines two of them — `chunk`, because the observer
never chunks by hand (resolved decision 1), and `negknow`, because §8.1 item 5 forbids L0 from
touching the Bloom filter — and it never had `symbols` (hence `SymbolLister`), `scheduler` or
`checkpoint` (hence `Signals`/`FeatureSample` and the callbacks), or `contract` (hence
`observer.Mode`). The **binding** statement is the exit criterion at the end of this document: the
realized import set is exactly `core paths config logging obs hookio store canon sketch dag grammar
tokens` plus stdlib.

---


---
<!-- plan lines 640-732 -->

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

### `internal/observer/doc.go` (exists — SP-01's package doc; SP-08 rewrites it)

Package documentation stating layer L0, the §8.1 responsibility list, the sole-writer rule from
§5.21 ("No subplan other than SP-08 writes code in `internal/observer`"), and resolved decisions
1–12 above in comment form.

---


---
<!-- plan lines 826-999 -->

### `internal/observer/tombstone.go` (exists — SP-01 shipped `Tombstone` and `humanBytes`)

**Responsibility.** §8.1 item 2 in its exact rendered form, plus tool-name normalization and the
compactable-tool-set predicate of §2.2. The renderer is not written from scratch: SP-08 extends the
one SP-01 already shipped, and leaves `humanBytes` alone.

**Grammar of the marker.** Rendered from `store.ToolUseRecord` with no I/O:

```
[cleared: sha256:<12 hex>… · <size> · <ToolDisplay> <subject> · re-expandable]
```

- separator is `" · "` (space, MIDDLE DOT, space) — matching the design's `·`;
- ellipsis is `…`;
- `<12 hex>` is `rec.Root.Short()` (§4: first 12 hex chars); the design's `a3f2…` is an elision in
  prose, 12 hex is the architecture's canonical short form and is what the golden file records;
- `<size>` is `humanBytes(rec.Bytes)`, which **already ships and is not changed by SP-08**:
  `const bytesPerKB = 1024 //nomagic:allow binary byte-unit divisor for size rendering, not
  store.chunk.min`, `var sizeUnits = [...]string{"KB", "MB", "GB"}`, sizes below the divisor
  rendered as `"%dB"` and everything above it as `"%.1f%s"` scaled through KB → MB → **GB**. The GB
  tier is load-bearing: `TestHumanBytes_UsesTheBinaryDivisor` pins `1.0GB` and `3.0GB`, and SP-08
  may not narrow the renderer to B/KB/MB. 2458 bytes renders `2.4KB`, matching the design example;
- `<ToolDisplay>` is `NormalizeToolName(rec.Tool)`;
- `<subject>` is `rec.Path` when non-empty, else `rec.ArgsPreview` truncated to 48 runes with `…`;
  when both are empty the ` <subject>` group and its leading space are omitted entirely;
- when `rec.Status == store.StatusSuperseded`, ` · superseded` is inserted immediately before
  ` · re-expandable`;
- when `rec.Ephemeral`, ` · ephemeral` is inserted in the same position, before any `superseded`.

**What commit 1 actually changes.** `internal/observer/tombstone.go` already renders
`fmt.Sprintf("[cleared: sha256:%s · %s · %s %s · re-expandable]", rec.Root.Short(),
humanBytes(rec.Bytes), rec.Tool, rec.Path)`. SP-08 adds, to that existing function: the `…` after
the short hash; `NormalizeToolName(rec.Tool)` in place of the raw `rec.Tool`; the `ArgsPreview`
fallback subject and the no-subject elision (which removes today's double space on a pathless
record); and the ` · ephemeral` / ` · superseded` segments. `humanBytes` is untouched.

Two shipped tests are therefore *updated*, not written:

- `TestTombstone_RendersTheSection81Form` — its pinned string gains the ellipsis and becomes
  `[cleared: sha256:a3f2c9e14b70… · 2.4KB · FileRead src/auth.ts · re-expandable]`. `FileRead`
  already survives `NormalizeToolName` unchanged, so the ellipsis is the only delta.
- `TestHumanBytes_UsesTheBinaryDivisor` — **left exactly as it is**, GB rows and negative-size row
  included. If a change to `tombstone.go` makes it fail, the change is wrong.

`TestTombstone_IsAddressable` and `TestTombstone_HandlesAnEmptyRecord` also already ship and keep
passing; `TestTombstone_NoSubject` in the test plan below is the tightened successor to the latter
and replaces it.

```go
func Tombstone(rec store.ToolUseRecord) string
func TombstoneNote() string
```

`TombstoneNote()` returns exactly, on one line:

```
Cleared tool results above are re-expandable: call the qompack MCP tool expand with the sha256 shown in the marker, or with the tool_use_id.
```

**Tool tables.**

```go
var hostToDisplay = map[string]string{
    "Read": "FileRead", "NotebookRead": "FileRead", "Edit": "FileEdit",
    "MultiEdit": "FileEdit", "NotebookEdit": "FileEdit", "Write": "FileWrite",
    "Bash": "Bash", "BashOutput": "Bash", "PowerShell": "PowerShell",
    "Grep": "Grep", "Glob": "Glob", "WebSearch": "WebSearch", "WebFetch": "WebFetch",
    "Task": "AgentTool", "TodoWrite": "TodoWrite",
}
func NormalizeToolName(hostName string) string  // table hit, else hostName unchanged
func IsCompactable(tool string) bool            // display name ∈ §2.2 set
func supersedableClass(tool string) string      // "filecontent" | "search" | "exec" | "web" | ""
```

`IsCompactable` returns true for exactly `FileRead, Bash, PowerShell, Grep, Glob, WebSearch,
WebFetch, FileEdit, FileWrite` — the §2.2 list, nine display names because §2.2 writes
`Bash/PowerShell` as one row — and false for `AgentTool`, `TodoWrite`, and every `mcp__…` name,
matching "AgentTool and MCP results are preserved."

**Where these two exported functions are actually used** (so neither is dead code):

- `IsCompactable` gates the `observer.tombstone` counter in step 14 of `OnToolUse` — the counter
  measures *how many results the host may later clear*, which is the only honest denominator for
  G3.2 — and it is the predicate SP-11 (rehydration) and SP-13 (`expand`) call to decide whether a
  record may legally be represented by a marker at all. It reports **Claude Code's** §2.2 host set
  and nothing else. Qompack's own retrieval results are governed by the §8.7 ephemeral rule
  instead, carried on `store.ToolUseRecord.Ephemeral`, which is why an `mcp__qompack__…` result is
  simultaneously `IsCompactable == false` and first in the eviction order.
- `Tombstone` renders for **any** record, including non-compactable ones, because a caller may want
  an addressable marker for a result it is choosing to elide for its own budget reasons; the
  compactability decision belongs to the caller, not to the renderer. `TombstoneNote()` is emitted
  once per rehydration block by SP-11 and once per `expand` response by SP-13; SP-08 only ships and
  tests it.

`supersedableClass`: `filecontent` for FileRead/FileEdit/FileWrite; `search` for Grep/Glob; `exec`
for Bash/PowerShell; `web` for WebFetch/WebSearch; `""` (never supersedes, never superseded) for
everything else.

---

### `internal/observer/signals.go` (exists as SP-01's stub — `Signals` and a zero-returning `ExtractSignals`)

**Responsibility.** G1.5. Pure functions of `hookio.Event` — no clock, no state, no I/O — so they
are trivially testable and reusable by the daemon.

```go
func ExtractSignals(e hookio.Event) Signals
func ExtractTestOutcome(e hookio.Event) TestOutcome
func PathsFromInput(tool string, in json.RawMessage) []string
func commandOf(e hookio.Event) string        // tool_input.command, "" when absent
func responseText(e hookio.Event) []byte     // see rule below
```

`responseText` unwraps `e.ToolResponse` deterministically:
1. JSON string → the unquoted bytes;
2. object with `"content"` string → those bytes; object with `"content"` array → every element's
   `"text"` field joined with `"\n"`;
3. object with `"stdout"` → `stdout`, plus `"\n"+stderr` when `stderr` is non-empty;
4. anything else → `json.Compact` of the raw message.

Truncated at the package constant `maxScanBytes = 4 << 20 // 4 MiB: bounds the pure decoder's
allocation; the CONFIGURED cap is applied separately in tooluse.go`, with the retained prefix used
as-is. `responseText` deliberately does **not** read `o.maxResultBytes`: everything in this file is
a pure function of `hookio.Event` so that `ExtractSignals` stays reusable by the daemon and
trivially fuzzable, and the configured `runtime.hotPath.maxPayloadBytes` truncation happens at
step 3 of `OnToolUse` instead. `maxScanBytes` is strictly larger than the 1 MiB default so the two
caps never interact in practice; when a user configures a larger `maxPayloadBytes`, `maxScanBytes`
binds first and that is the documented behaviour.

`PathsFromInput` returns, in this order and deduplicated with order preserved: `file_path`,
`path`, `notebook_path`, then every `edits[].file_path`. Values are returned raw; the caller
normalizes with `paths.Norm`/`paths.Key`.

**Regex tables** (compiled once in `var` blocks, all case-insensitive where marked):

```go
var reTestRunner = regexp.MustCompile(`(?i)\b(go\s+test|npm\s+(run\s+)?test|yarn\s+test|pnpm\s+test|jest|vitest|pytest|python\s+-m\s+pytest|cargo\s+test|dotnet\s+test|mvn\s+test|gradle\s+test|rspec|ctest)\b`)
var reTestPass = []*regexp.Regexp{
    regexp.MustCompile(`(?m)^ok\s+\S+`),
    regexp.MustCompile(`(?m)^PASS\b`),
    regexp.MustCompile(`(?i)\btests?:\s+\d+\s+passed\b`),
    regexp.MustCompile(`(?i)=+\s*\d+\s+passed`),
    regexp.MustCompile(`test result: ok\.`),
    regexp.MustCompile(`(?i)\bOK\s+\(\d+\s+tests?\)`),
}
var reTestFail = []*regexp.Regexp{
    regexp.MustCompile(`(?m)^FAIL\b`),
    regexp.MustCompile(`(?m)^---\s+FAIL`),
    regexp.MustCompile(`(?i)\bFAILED\b`),
    regexp.MustCompile(`(?i)\b[1-9]\d*\s+failed\b`),
    regexp.MustCompile(`test result: FAILED\.`),
}
var reGitCommit  = regexp.MustCompile(`(?i)\bgit\s+(-C\s+\S+\s+)?commit\b`)
var reGitNoop    = regexp.MustCompile(`(?i)nothing to commit|no changes added to commit`)
```

- `ExtractTestOutcome`: `TestUnknown` unless `NormalizeToolName(e.ToolName) ∈ {Bash, PowerShell}`
  and `reTestRunner` matches the command. Then `TestFail` if any `reTestFail` matches the response
  text, else `TestPass` if any `reTestPass` matches, else `TestUnknown`. Fail wins over pass —
  a run with one failure is not a passing test run.
- `Signals.TestPassed = ExtractTestOutcome(e) == TestPass`.
- `Signals.GitCommit`: Bash/PowerShell, `reGitCommit` matches the command, `reGitNoop` does not
  match the response.
- `Signals.TodoCompleted`: `e.ToolName == "TodoWrite"` and the input's `todos` array contains at
  least one element with `"status":"completed"`. (Newly-completed detection — the transition, not
  the state — is done in `tooluse.go` against `sessionState.TodoDone`, because `ExtractSignals`
  must stay pure per §5.21.)
- `Signals.Paths`: `PathsFromInput` output, unchanged (raw, caller-normalized).

Malformed JSON in `ToolInput`/`ToolResponse` never panics and never errors: every unmarshal is into
a tolerant struct and failures yield zero values.

---


---
<!-- plan lines 1990-2031 -->

### `tombstone_test.go`

| Test | Setup / input | Expected output |
|---|---|---|
| `TestTombstone_DesignExample` | `ToolUseRecord{Root: hash whose hex starts `a3f2c19d0b74`, Bytes: 2458, Tool: "FileRead", Path: "src/auth.ts"}` | `[cleared: sha256:a3f2c19d0b74… · 2.4KB · FileRead src/auth.ts · re-expandable]` |
| `TestTombstone_NoPathUsesArgsPreview` | `Tool: "Bash"`, `Path: ""`, `ArgsPreview: "go test ./internal/store/..."`, `Bytes: 812` | `[cleared: sha256:…… · 812B · Bash go test ./internal/store/... · re-expandable]` |
| `TestTombstone_LongPreviewTruncatedTo48Runes` | `ArgsPreview` of 200 ASCII chars | subject is exactly 47 runes + `…` |
| `TestTombstone_MegabyteSize` | `Bytes: 3_500_000` | contains ` · 3.3MB · ` |
| `TestTombstone_GigabyteSize` | `Bytes: 3 << 30` | contains ` · 3.0GB · ` — the shipped `humanBytes` GB tier survives the extension |
| `TestTombstone_SupersededMarker` | `Status: StatusSuperseded` | `… · superseded · re-expandable]` |
| `TestTombstone_EphemeralMarker` | `Ephemeral: true` | `… · ephemeral · re-expandable]` |
| `TestTombstone_BothMarkers` | ephemeral + superseded | `… · ephemeral · superseded · re-expandable]` |
| `TestTombstone_NoSubject` | `Path: ""`, `ArgsPreview: ""` | `[cleared: sha256:…… · 0B · Bash · re-expandable]` (no double space) |
| `TestTombstoneGolden` | 13 records covering every branch, size tiers B/KB/MB/GB included | byte-identical to `testdata/golden/observer/tombstones.txt` |
| `TestTombstoneNote_SingleLine` | — | contains `expand`, contains no `\n` |
| `TestNormalizeToolName` | table: Read→FileRead, MultiEdit→FileEdit, Write→FileWrite, Task→AgentTool, `mcp__qompack__recall`→unchanged | as stated |
| `TestIsCompactable` | the nine §2.2 names → true; `AgentTool`, `TodoWrite`, `mcp__qompack__expand` → false | as stated |
| `TestSupersedableClass` | FileRead/FileEdit/FileWrite→`filecontent`; Grep/Glob→`search`; Bash/PowerShell→`exec`; WebFetch/WebSearch→`web`; AgentTool→`""` | as stated |
| **`BenchmarkTombstone`** | one record | **< 2 µs/op, 0 allocations beyond the returned string** (contributes to B-C) |

### `signals_test.go`

| Test | Input | Expected |
|---|---|---|
| `TestExtractSignals_TodoCompleted` | `TodoWrite`, input `{"todos":[{"content":"a","status":"completed"},{"content":"b","status":"pending"}]}` | `TodoCompleted: true` |
| `TestExtractSignals_TodoNoneCompleted` | all `pending`/`in_progress` | `TodoCompleted: false` |
| `TestExtractTestOutcome_GoPass` | Bash, `go test ./...`, response `ok  	github.com/x/y	0.31s\n` | `TestPass` |
| `TestExtractTestOutcome_GoFail` | same command, response `--- FAIL: TestX\nFAIL\n` | `TestFail` |
| `TestExtractTestOutcome_JestPass` | `npm test`, `Tests:       42 passed, 42 total` | `TestPass` |
| `TestExtractTestOutcome_PytestMixed` | `pytest -q`, `2 failed, 40 passed` | `TestFail` (fail wins) |
| `TestExtractTestOutcome_CargoPass` | `cargo test`, `test result: ok. 12 passed; 0 failed` | `TestPass` |
| `TestExtractTestOutcome_NotATestCommand` | `ls -la`, response `PASS` | `TestUnknown` |
| `TestExtractSignals_GitCommit` | Bash, `git commit -m "x"`, response `[main 3f2a1] x\n 2 files changed` | `GitCommit: true` |
| `TestExtractSignals_GitCommitNoop` | same command, response `nothing to commit, working tree clean` | `GitCommit: false` |
| `TestExtractSignals_GitCommitInChain` | `git add -A && git commit -m x` | `GitCommit: true` |
| `TestPathsFromInput_Read` | `{"file_path":"src/a.ts"}` | `["src/a.ts"]` |
| `TestPathsFromInput_MultiEdit` | `{"file_path":"a","edits":[{"file_path":"b"},{"file_path":"a"}]}` | `["a","b"]` (dedup, order kept) |
| `TestPathsFromInput_Glob` | `{"pattern":"**/*.go","path":"internal"}` | `["internal"]` |
| `TestExtractSignals_MalformedJSON` | `ToolInput: []byte("{not json")` | zero `Signals`, no panic |
| `TestResponseText_AllFourShapes` | string / `{"content":"x"}` / `{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}` / `{"stdout":"o","stderr":"e"}` | `x`, `x`, `a\nb`, `o\ne` |
| `FuzzExtractSignals` | seeded from `testdata/corpora/toolout/` | never panics; always returns a `Signals` whose `Paths` are all valid UTF-8 |


---
<!-- plan lines 2328-2340 -->

### Fixtures needed

- `testdata/golden/observer/tombstones.txt` — 13 rendered markers, created in commit 1.
- `testdata/corpora/toolout/` — SP-04's committed raw bash/test/grep/glob/git/ANSI/fileread/webfetch
  output; **reused, not extended**, by both `BenchmarkOnToolUse_TestOutput256KB` and the Phase 1
  harness, which draws every tool-result payload from it.
- `internal/observer/testdata/transcript_tail.jsonl` — 40-line synthetic transcript with sidechain
  assistant messages, for `TestOnStop_SummaryFromTranscriptTail`.
- No new session fixtures: Phase 1 takes its call *sequence* from `eval.Synthesize` and its *bytes*
  from the corpus above. Adding real result bytes to `eval.SynthSpec` would be an `arch/` amendment
  against `internal/eval` and is explicitly not done here.

---

---
<!-- plan lines 2405-2421 -->

### Commit 1 — `feat(observer): addressable tombstones, tool classification, and task-boundary signals`

- [ ] Extend `internal/observer/tombstone_test.go` — it already ships with four passing tests — and
      write `signals_test.go` in full: all rows of both tables above, plus `BenchmarkTombstone` and
      `FuzzExtractSignals`. Update `TestTombstone_RendersTheSection81Form`'s pinned string for the
      new `…`, and leave `TestHumanBytes_UsesTheBinaryDivisor` untouched. Run
      `go test ./internal/observer/ -run 'Tombstone|HumanBytes|Signals|TestOutcome|PathsFromInput|ResponseText'`
      and confirm the **new** rows fail — including the updated §8.1-form assertion, which fails
      until the ellipsis lands — while `TestHumanBytes_UsesTheBinaryDivisor` and
      `TestTombstone_IsAddressable` still pass. A run in which those two also fail means the change
      broke a shipped guarantee.
- [ ] Rewrite `internal/observer/doc.go`, extend `tombstone.go`, implement `signals.go`.
- [ ] Add `testdata/golden/observer/tombstones.txt` (generate once, eyeball every line against
      §8.1 item 2, then commit).
- [ ] `go run ./tools/devtool fmt lint test` — green; the golden test passes byte-for-byte.
- [ ] Footer: `Refs: SP-08, G3.2, G1.5, §8.1 item 2, §2.2`

