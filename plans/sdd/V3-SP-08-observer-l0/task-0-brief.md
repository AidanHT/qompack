# Task brief — Commit 0: the arch/sp08-observer-seams amendment

Source: plans/V3-SP-08-observer-l0.md (verbatim excerpts; the plan's line ranges are noted before each excerpt). Exact values, names, signatures and test rows below are binding.


---
<!-- plan lines 280-425 -->

## Interface contract

### Consumes (exact signatures from `00-ARCHITECTURE.md` §5; do not change any of them)

```go
// internal/hookio (§5.3) — SP-01
type Event struct {
    HookEventName  string          `json:"hook_event_name"`
    SessionID      core.SessionID  `json:"session_id"`
    TranscriptPath string          `json:"transcript_path"`
    CWD            string          `json:"cwd"`
    Source         string          `json:"source"`   // SessionStart: startup|resume|compact|clear
    Trigger        string          `json:"trigger"`
    ToolName       string          `json:"tool_name"`
    ToolUseID      core.ToolUseID  `json:"tool_use_id"`
    ToolInput      json.RawMessage `json:"tool_input"`
    ToolResponse   json.RawMessage `json:"tool_response"`
    Prompt         string          `json:"prompt"`
    StopHookActive bool            `json:"stop_hook_active"`
    Extra          map[string]json.RawMessage `json:"-"`
}
type Output struct {
    Continue           *bool  `json:"continue,omitempty"`
    SuppressOutput     *bool  `json:"suppressOutput,omitempty"`
    HookSpecificOutput *HSO   `json:"hookSpecificOutput,omitempty"`
    SystemMessage      string `json:"systemMessage,omitempty"`
}
type HSO struct {
    HookEventName      string `json:"hookEventName"`
    AdditionalContext  string `json:"additionalContext,omitempty"`
    CustomInstructions string `json:"customInstructions,omitempty"`
}
func Empty() Output

// internal/store (§5.8) — SP-06
Put(ctx context.Context, r io.Reader, o PutOptions) (PutResult, error)
PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error)
GetRoot(ctx context.Context, root core.Hash) (Root, error)
RecordToolUse(ctx context.Context, rec ToolUseRecord) error
ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error)
MarkSuperseded(ctx context.Context, older core.ToolUseID, by core.ToolUseID) error
ArgsDigest(raw json.RawMessage) (core.Hash, string)   // §5.8: "SP-08 calls this; nothing else may
                                                      // re-derive it" — canonical-JSON digest under
                                                      // core.DomainArgs + the ≤120-byte preview
AppendFileVersion(ctx context.Context, path string, v FileVersion) error
FileHistory(ctx context.Context, path string) ([]FileVersion, error)
Segments() SegmentLog
Stats(ctx context.Context) (Stats, error)
GC(ctx context.Context, p GCPolicy) (GCReport, error)
Flush(ctx context.Context) error
// plus the value types: PutOptions{Tool, Path, Canon, KeepRaw, Ephemeral},
// PutResult{Root, Novel, Reused, Signature, NearDup}, NearDupInfo{PriorRoot, Jaccard, DeltaBytes},
// Root{Hash, Chunks, CanonBytes, RawBytes, Tokens}, Supersession{StatusOK, StatusSuperseded},
// ToolUseRecord{ID, Session, Turn, TS, Tool, ArgsDigest, ArgsPreview, Root, Path, Bytes, Tokens,
//               Signature, Status, SupersededBy, Ephemeral, Subagent},
// FileVersion{TS, Root, Turn, Bytes}, GCPolicy{RetainDays, RetainSessions, DryRun, Deadline},
// Stats{Objects, Bytes, RawBytes, DedupRatio, ToolUses, Segments, Files, Sketches}

// internal/store SegmentLog (§5.8) — SP-06
Open(ctx context.Context, s Segment) (core.SegmentID, error)
Close(ctx context.Context, id core.SegmentID, endTurn core.TurnIndex, feats map[string]float64) error
Current(ctx context.Context, s core.SessionID) (Segment, error)
Frontier(ctx context.Context, s core.SessionID) (core.TurnIndex, error)

// internal/dag (§5.9) — SP-07
AddNode(n Node) error
AddEdge(e Edge) error
Flush(ctx context.Context) error
// Node{ID, Kind, Turn, TS, Pos, Ref, Root, Tokens, Ephemeral}; Edge{From, To, Kind, Weight, Turn}
// NodeKind: KindToolUse KindToolResult KindAssistant KindUserPrompt KindFile KindSymbol
//           KindDecision KindElimination KindSegment
// EdgeKind: EdgeSequence EdgeProduces EdgeConsumes EdgeSharedFile EdgeSharedSymbol
//           EdgeSupersedes EdgeExplains EdgeControlOnly

// internal/dag builders (§8.1 item 4) — SP-07. builders.go: "the one place in the repository that
// turns an observation of the transcript into nodes and edges", and ObservedTool's own doc names
// "observer (§5.7)" as its only production caller. SP-08 emits the item-4 chain ONLY through these.
func BuildToolUse(g Graph, o ObservedTool) error
func BuildUserPrompt(g Graph, o ObservedPrompt) error
func BuildSegment(g Graph, s SegmentSpec) error
type ObservedTool struct {
    ToolUseID, PrevToolUseID, Supersedes core.ToolUseID
    PrevTurn, Turn core.TurnIndex
    TS core.UnixMilli
    Pos, ResultPos int
    Tool, PathKey  string
    Writes         bool
    Symbols        []string          // resolved by the CALLER; BuildToolUse sorts and dedupes
    Root           core.Hash
    Tokens         core.Tokens
    Ephemeral      bool
}
type ObservedPrompt struct{ Turn core.TurnIndex; TS core.UnixMilli; Pos int; Tokens core.Tokens; Ref string }
type SegmentSpec struct {
    ID, PrevID core.SegmentID
    StartTurn, EndTurn core.TurnIndex
    TS core.UnixMilli
    StartPos int
    Tokens core.Tokens
    Members []NodeID
}

// internal/dag NodeID constructors (D-2) — SP-07. nodeid.go: "Every consumer builds IDs through
// the constructors below rather than concatenating strings … One component spelling a file node
// differently from another does not fail loudly — it silently produces two disconnected halves of
// the same graph." internal/observer NEVER assembles a NodeID from a string.
func ToolUseNode(id core.ToolUseID) NodeID      // "tooluse:<id>"
func ToolResultNode(id core.ToolUseID) NodeID   // "toolresult:<id>"
func AssistantNode(t core.TurnIndex) NodeID     // "assistant:<decimal turn>" — NO session component
func UserPromptNode(t core.TurnIndex) NodeID    // "userprompt:<decimal turn>" — NO session component
func FileNode(pathKey string) NodeID            // "file:<pathKey>"; does NOT re-fold through paths.Key
func SymbolNode(pathKey, name string) NodeID    // "symbol:<pathKey>#<name>"
func SegmentNode(id core.SegmentID) NodeID      // "segment:<decimal id>"

// internal/sketch (§5.7) — SP-03
func (c *CMS) Add(key []byte, n uint32)
func (h *HLL) Add(key []byte)
func (m *MisraGries) Add(key string, n int)
func (s Signature) IsNearDup(o Signature, threshold float64) bool
func Save(p string, s Sketch) error
type MinHashOptions struct{ Enabled bool; Permutations int; ShingleSize int; NearDupThreshold float64 }

// internal/canon (§5.6) — SP-04
type Class string
type Options struct{ Strip []Class; KeepDeltas bool; MinHash MinHashOptions }

// internal/grammar (§5.11) — SP-15 (stub in wave 2; must be tolerated)
Append(s Symbol); Rules() []Rule; Thrash(minUses int) []Rule
func FormatWarning(w Warning) string

// internal/tokens (§5.20) — SP-01/SP-06
EstimateRoot(ctx context.Context, chunks []core.ChunkRef, c Class) core.Tokens
EstimateString(s string, c Class) core.Tokens
func Classify(tool, path string, b []byte) Class

// internal/core (§4), internal/paths, internal/config, internal/logging, internal/obs
func HashBytes(domain string, b []byte) Hash
func (h Hash) String() string; func (h Hash) Short() string
func Norm(projectRoot, p string) (string, error); func Key(p string) string
func WriteAtomic(p string, b []byte) error
func (c Config) Get(dotted string) (any, bool)
Logger.Debug/Info/Warn/Error/Loud(msg string, kv ...any)
Registry.Hist(name string) Histogram; Registry.Counter(name string) Counter
```


---
<!-- plan lines 530-639 -->

### Pre-step: the `arch/sp08-observer-seams` amendment (lands on `develop` before this branch)

Three shipped behaviours block SP-08 as written, and §0's amendment rule is explicit that the
response is a branch against the architecture rather than a local workaround: *"If an interface in §5 is
wrong, you do not work around it. You open a branch `arch/<short-reason>` off `develop`, change §5,
get it merged, and rebase."* Cut `arch/sp08-observer-seams` off `develop`, land it, then cut
`feat/sp08-observer-l0` from the result. It is small, and every part is spelled out here so the
implementer does not have to improvise.

**(a) `store.PutOptions.Canon` must be able to say "no optional classes".** Today
`FSStore.canonOptions` (`internal/store/put.go`) honours the caller's strip list only when it is
non-empty — `if len(o.Canon.Strip) > 0 { opts.Strip = o.Canon.Strip }` — and never reads
`o.Canon.MinHash` or `o.Canon.KeepDeltas` at all, so a caller asking for "nothing optional" silently
inherits the store's configured six classes *and* the store's MinHash setting. `internal/canon`
already draws exactly the distinction the store is dropping: `gateSet` treats a **nil** `Strip` as
"every class" and a **non-nil but empty** `Strip` as "exactly these — i.e. none — plus the always-on
structural ones" (`internal/canon/classes.go`). The amendment makes the store agree:

- change the override test to `if o.Canon.Strip != nil { opts.Strip = o.Canon.Strip }`, so an empty
  non-nil slice means "no optional class" and a nil slice still means "use the store's config";
- honour a caller-supplied opt-out on MinHash, gated by that same `Strip != nil` test: when
  `o.Canon.Strip != nil` **and** `o.Canon.MinHash.Enabled` is false, the returned options carry
  `MinHash.Enabled = false` regardless of `store.canonicalize.minhash.enabled`. The gate is
  load-bearing, not decoration. `sketch.MinHashOptions.Enabled` is a plain `bool` and
  `PutOptions.Canon` is a value field, so an explicit `false` is byte-identical to the zero value:
  an ungated rule would read every `store.PutOptions{}` in the tree as an opt-out, zero every
  `PutResult.Signature`, and silently retire `FSStore.nearDup` — §8.1 item 3's redundancy detector,
  whose output `supersede.go` depends on. `Strip` is the one field that *can* say "unset", so it
  carries the whole decision: a **nil** `Strip` means "I supplied no per-call canon override at
  all", a **non-nil** `Strip` means "this entire `canon.Options` is mine, MinHash included". Both of
  SP-08's call sites pass a non-nil `Strip` (`prompt.go`'s `verbatimOptions()` and `tooluse.go`'s
  `canonOptions()`), so the gate costs this subplan nothing. The reverse direction stays
  config-wins — a caller may turn the signature *off* for one Put, never on, because the permutation
  count and threshold are configuration the caller does not own;
- add the matching note to `plans/00-ARCHITECTURE.md` §5.8 under `PutOptions`: a nil `Canon.Strip`
  means "no per-call override at all — the store's configured classes *and* the store's MinHash
  setting", an empty non-nil `Canon.Strip` means "no optional class",
  `Canon.MinHash.Enabled == false` disables the signature for that one Put **only when
  `Canon.Strip` is non-nil**, and `Canon.KeepDeltas` remains derived from `PutOptions.KeepRaw`
  rather than read from `Canon`;
- add `TestCanonOptions_EmptyStripMeansNoOptionalClasses` in `internal/store`: a body carrying a
  timestamp and an ANSI escape, Put once with
  `Canon: canon.Options{Strip: []canon.Class{}, MinHash: sketch.MinHashOptions{Enabled: false}}` and
  once with `Canon: canon.Options{}`, against a store whose config has the six classes enabled. The
  first must come back with a zero `PutResult.Signature` and a `Root.CanonBytes` equal to the input
  length after CRLF/path normalization only; the second must not. The second Put is also the row
  that pins the gate: its `MinHash.Enabled` is the zero `false` too, and its `Signature` must still
  be non-zero, because its `Strip` is nil and a nil `Strip` is not an opt-out.

**(b) the subagent's name must survive the IPC boundary.** `hookio.Event.Extra` is
`map[string]json.RawMessage` tagged `json:"-"`: `hookio.ReadEvent` fills it in the *hook client*
process and `ipc.EncodeRequest` then drops it, so a daemon-side reader of `e.Extra` sees an empty
map in production and every subagent capture would be named `"subagent"`. Two edits fix it:

- `internal/cli/hookclient.go`'s `rawExtras` already builds `{"subagent":true}` for
  `observe stop --subagent`. Extend it to resolve the agent's name **client-side**, where `Extra` is
  real — the first of `Extra["subagent_type"]`, `Extra["agent_name"]`, `Extra["agent"]` that
  `json.Unmarshal`s into a non-empty string — and emit `{"subagent":true,"agent":"<name>"}`. A
  numeric or object value falls through to the next key; all three missing emits the existing
  `{"subagent":true}` byte-for-byte, so the wire format stays backward-compatible and
  `decodeSubagent` is untouched.
- `internal/daemon/handlers.go`'s `resolveEvent` re-populates `Event.Extra` from `req.Raw` when
  `req.Raw` decodes as a JSON object, so what the client parsed reaches every bound `Services` seam.
  This is a restoration rather than a new channel: `Extra` is already §5.3's documented home for a
  hook payload's unclaimed keys, and it is the only field of `hookio.Event` the transport silently
  empties.
- add `TestResolveEvent_RestoresRawExtras` in `internal/daemon`, and extend `internal/ipc`'s
  `TestDecodeRequestRoundTrip` with a request whose `Raw` carries the agent name.

**(c) the contract mode must be reachable from a bound function.** §12.1 gives the observer two
behaviours — `ModeFull` acts, `ModeDegradedPassive` records but does not — and §8.1 item 7's
thrash warning is gated on the first. The mode lives on the `contract.Monitor`, and nothing SP-08
can hold reaches one. `contract.NewMonitor` is called *inside* `daemon.New`
(`internal/daemon/daemon.go`, after the bind loop) into the unexported `d.monitor` field; it is on
neither `Options`, nor `Services`, nor the `Daemon` interface. `WireObserver` must return before
`New` is called at all, so there is no ordering that lets it dereference a monitor. Three edits fix
it:

- add `Mode func() contract.Mode` to `Services` in `internal/daemon/options.go`. It is a **provided**
  seam, the inverse direction to the nine consumed ones beside it: SP-05 fills it in, a bound
  function reads it. Rule W-3 is satisfied — the struct is widened, nothing is renamed or
  re-typed — but the struct belongs to SP-05, which is why the edit belongs on this branch and not
  on `feat/sp08-observer-l0`;
- in `internal/daemon/daemon.go`, hoist the two `statePath` / `contract.NewMonitor` lines above the
  `for _, bind := range o.binds` loop and assign `svc.Mode = monitor.Mode` before it runs. The hoist
  is safe because `NewMonitor` reads only `o.ProjectRoot`, `o.Log` and `o.Metrics`, none of which a
  bind produces; it is *necessary* because a bind body that captures `s.Mode` before the assignment
  captures nil and reports `ModePassive` for the process's whole life — a fully silent failure, since
  a passive observer still records and still exits 0;
- add the matching `Mode` line and its two-direction note to `plans/00-ARCHITECTURE.md` §5.4, and
  `TestServicesModeIsAssignedBeforeBinds` in `internal/daemon`: a bind body that captures `s.Mode`,
  a `daemon.New` over a temp project, and an assertion that the captured func is non-nil and returns
  `contract.ModeFull` on a fresh state directory. Asserting inside the bind body instead would pass
  vacuously — the monitor has not read `state/contract.json` yet at that point and answers `ModeFull`
  for every project, including a degraded one.

**Why an amendment rather than a workaround.** (a) cannot be worked around at all — nothing outside
`internal/store` can reach `FSStore.canonOptions` — and working (b) around by registering a `Handle`
route would replace SP-05's WAL-before-ACK path outright (see `internal/daemon/observer_ops.go`
below). (c) has two workarounds and both are worse than the amendment: constructing a second
`contract.NewMonitor` over the same `state/contract.json` gives the observer a monitor whose mode
diverges from the daemon's the moment either degrades, and reading the state file directly duplicates
§12.1's parsing in a package that §3.2 forbids from importing `contract` for anything else. All three
are exactly the "an interface in §5 is wrong" case §0 names.

The amendment is a separate branch and a separate merge; it does **not** count against SP-08's own
5–8 commit band, and `git rev-list --count develop..feat/sp08-observer-l0` is still 7 afterwards.

---


---
<!-- plan lines 2344-2404 -->

### Commit 0 — the `arch/sp08-observer-seams` amendment (a **separate branch**, merged first)

The Implementation spec's pre-step, landed on `develop` before `feat/sp08-observer-l0` is cut. It is
one commit on its own branch and does not count toward SP-08's 5–8 band.

```
git fetch && git checkout develop && git pull
git checkout -b arch/sp08-observer-seams
```

- [ ] `fix(store): honour an explicit empty Canon.Strip and a caller MinHash opt-out` —
      `internal/store/put.go`'s `canonOptions` per pre-step (a), plus
      `TestCanonOptions_EmptyStripMeansNoOptionalClasses`, plus the §5.8 note in
      `plans/00-ARCHITECTURE.md`.
- [ ] Same commit: `internal/cli/hookclient.go`'s `rawExtras` forwarding the resolved agent name and
      `internal/daemon/handlers.go`'s `resolveEvent` restoring `Event.Extra` from `req.Raw`, per
      pre-step (b), with `TestRawExtras_ResolvesTheSubagentNameClientSide`,
      `TestResolveEvent_RestoresRawExtras` and the extended `TestDecodeRequestRoundTrip`.
- [ ] Same commit: `Services.Mode func() contract.Mode` in `internal/daemon/options.go`, the hoist of
      `statePath` / `contract.NewMonitor` above the bind loop in `internal/daemon/daemon.go` with
      `svc.Mode = monitor.Mode` assigned before it, the §5.4 declaration in
      `plans/00-ARCHITECTURE.md`, and `TestServicesModeIsAssignedBeforeBinds`, per pre-step (c).
- [ ] Confirm the copy of this plan you are working from carries the branch-purity carve-out: the
      Definition-of-Done criterion reads "no edit to `internal/store`, to
      `internal/daemon/handlers.go`, or to any file under `internal/cli` **except**
      `internal/cli/daemon.go`", and the Done checklist's file-map bullet says the same. If it does
      not, narrow both before cutting the feature branch — this is a plan edit, not a code edit, and
      it belongs on this branch. `internal/cli/daemon.go` holds the repository's only
      non-test `daemon.New` call site
      (`git grep -n 'daemon\.New(' -- internal cmd test tools ':!*_test.go'` → one hit), so with the
      criterion unnarrowed `WireObserver` has no caller it is permitted to have, all five `Services`
      seams stay nil, every hook still exits 0, and this subplan's own e2e rows
      (`TestE2E_ObserverThroughDaemon`, `TestE2E_SubagentNameReachesTheDaemon`) — which build and run
      the real `./cmd/qompack` binary — cannot pass. The call itself cannot be added here: it
      belongs to Commit 6, because `daemon.WireObserver` does not exist yet on this branch and a
      reference to it would not compile.
- [ ] Confirm, likewise, that the `observer_ops.go` declaration in the Implementation spec is
      `func WireObserver(o *Options) (observer.Observer, error)` plus
      `func RegisterObserverIdleWork(d Daemon, obsv observer.Observer)`, and the idle `Persist`
      registration has moved into the second function. The `s *SessionRegistry` parameter and the
      `o.Idle()` call the earlier draft carried are both unsatisfiable: `Bind` must run before
      `daemon.New`, while the registry and the idle controller are built inside it and reachable
      only afterwards through `d.Registry()` and `d.Idle()`.
- [ ] `go run ./tools/devtool ci-local` green on the amendment branch alone; merge to `develop`.
- [ ] Footer: `Refs: SP-08 pre-step, §0 amendment rule, §5.8 PutOptions, §5.3 Event.Extra`

---

The remaining work happens on **`feat/sp08-observer-l0`**, cut from `develop` with SP-01, SP-03,
SP-04, SP-05, SP-06, SP-07 **and the amendment above** already merged:

```
git fetch && git checkout develop && git pull
git checkout -b feat/sp08-observer-l0
```

Conventional Commits per §10: `<type>(<scope>): <subject>`, body explains the decision, footer
`Refs:` names the subplan, gaps and design sections.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

