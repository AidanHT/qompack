# SP-15 frozen contract (contract-first slice)

Frozen by Main before any fan-out, per
[SP-15 "Contract-first slice"](../../V5-SP-15-analyzer-selection-and-grammar.md#subagent-strategy).
Roles A-E code against **this document**, not against each other's files. A change to anything
below is a Main decision recorded here, never a unilateral edit by a role.

Baseline: `develop@7c735ac`, branch `feat/sp15-analyzer-selection-and-grammar`.

## 0. Ownership (exclusive write sets)

| Role | Owns |
|---|---|
| Main | `internal/analyzer/types.go`, `internal/grammar/types.go`, `internal/checkpoint/**`, `internal/daemon/**`, `internal/rehydrate/**`, `internal/config/**`, `tools/devtool/**`, `test/guards/**`, every commit |
| A | `internal/grammar/sequitur.go`, `internal/grammar/rules.go` (new), `internal/grammar/sequitur_test.go` (new) |
| B | `internal/grammar/codec.go` (new), `internal/grammar/statewarn.go` (new), `internal/grammar/formatwarning.go`, their `_test.go` files, `internal/grammar/testdata/**` |
| C | `internal/analyzer/delta.go`, `internal/analyzer/redundancy.go`, `internal/analyzer/block.go` (new), their `_test.go` files |
| D | `internal/analyzer/selector.go`, `internal/analyzer/greedy.go` (new), `internal/analyzer/selector_test.go`, `internal/analyzer/objective_test.go` (new), `internal/analyzer/testdata/**` |
| E | `test/replay/phase5_test.go`, `test/replay/phase6_test.go`, `test/replay/policy_selection.go` (new) |

Two roles never own one file. A role that needs a file it does not own stops and returns a
handoff instead of editing it.

## 1. Layering - the answer to "integrate with SP-11's actual consumer"

`tools/devtool/importrules.go` gives `rehydrate` the allow-set
`{checkpoint, store, negknow, dag, rules, skills, tokens}` and `analyzer` the allow-set
`{store, dag, sketch, scheduler}`. **`rehydrate` may not import `analyzer`, and `analyzer` may not
import `rehydrate` or `negknow`.** Commit 6 therefore does not wire an import; it wires the
composition root:

```
observer / checkpoint  --records-->  analyzer (Candidates)
analyzer.Select        ----------->  Proposal
internal/daemon (a composition root, may import anything)
     |-- runs the selector
     `-- hands rehydrate.Build a rehydrate-local SelectionProvider
```

No architecture amendment to the §3.2 table is required and none is proposed. A nil provider is
the rollback path: it is byte-for-byte today's `rehydrate.Build`.

Because `analyzer` cannot import `negknow`, G6.3 elimination evidence reaches the selector as an
ordinary `Candidate` carrying `Mandatory` and a `Provenance.Qualification` - the neutral shape in
§2. Mapping a `negknow.Record` onto that shape is the daemon's job, not the analyzer's.

## 2. Shared representation types - `internal/analyzer/types.go` (frozen)

`Block`, `DeltaMode`, `Continuation`, `RedundancyReport` and `Selection` are **unchanged**:
`analyzertest`, `test/guards` and `test/integration` already pin them. Everything below is added
alongside them.

```go
type RepresentationKind uint8
const (
    RepExactSpan RepresentationKind = iota // verbatim bytes of the item
    RepCapsule                             // structured summary capsule
    RepPointer                             // durable handle only, contents fetched on demand
    RepArchiveOnly                         // not injected at all; recoverable from the archive
)

type Qualification uint8
const (
    QualCurrent   Qualification = iota // authoritative and applicable now
    QualStale                          // a recorded dependency changed since capture
    QualUncertain                      // observation coverage is incomplete
)
func (q Qualification) Active() bool // true only for QualCurrent

type Provenance struct {
    Origin        dag.NodeID       // the observation this representation derives from
    Root          core.Hash        // ORIGINAL evidence root; a derivative never overwrites it
    Derived       bool             // true when this is a summary of a summary
    Qualification Qualification
    Turn          core.TurnIndex
}

type Representation struct {
    Item          dag.NodeID
    Kind          RepresentationKind
    Coverage      float64      // per-item saturating contribution, [0,1]
    AssembledCost core.Tokens  // INCLUDES wrapper, handle and report overhead
    Requires      []dag.NodeID // transitively closed; sorted ascending
    Prov          Provenance
}

type Candidate struct {
    Item      dag.NodeID
    Pos       int              // §13 invariant 4 still applies: Pos >= p
    Weight    float64          // per-item objective weight, >= 0
    Mandatory bool             // must be carried, or the proposal overflows
    Reps      []Representation // >= 1 compatible representations; AT MOST ONE is chosen
}

type Proposal struct {
    Chosen   []Representation // at most one entry per Candidate.Item
    Tokens   core.Tokens      // sum of Chosen[i].AssembledCost; <= budget always
    Value    float64          // the objective at Chosen; >= 0
    Archive  []dag.NodeID     // archive-only outcome, recoverable, never silently dropped
    Overflow bool             // a Mandatory item could not be carried
    Reason   string           // names the overflowing item when Overflow is set
    Iters    int              // marginal-gain evaluations
}
```

## 3. Selector objective and feasibility (D)

**Objective.** For a chosen set S,

```
F(S) = sum_i  w_i * min(1, sum_{r in S, r.Item = i} Coverage(r))   -   lambda * redundancy(S)
```

reported as `max(0, F(S))` - a **nonnegative saturating coverage** objective. The `min(1, ...)` is
the saturation; `w_i` is `Candidate.Weight`. This is a *declared* objective, not an oracle for
task completion, and no (1-1/e) guarantee is claimed: redundancy subtraction does not preserve
monotonicity, and the dependency and one-representation-per-item constraints change the feasible
family. The greedy step therefore **never accepts a non-positive marginal gain**.

**Feasibility.** A proposal is feasible iff all of:

1. at most one `Representation` per `Candidate.Item`;
2. every `Requires` entry of every chosen representation is itself chosen or already present;
3. `sum AssembledCost <= budget`;
4. no chosen item has `Pos < p`.

**Determinism.** Candidates are sorted by `Item` ascending before the loop; ties in marginal gain
break by `(gain desc, Item asc, Kind asc)`; no result depends on map iteration order. Two `Select`
calls on equal inputs return equal `Proposal`s, `Iters` included.

**Overflow.** If a `Mandatory` candidate has no representation that fits the remaining budget -
including at `RepPointer`, and after every optional item has been dropped - then `Overflow = true`,
`Reason` names it, and the item is appended to `Archive`. Never a partial serialization, never a
dropped constraint reported as success. A zero or tiny budget yields `Chosen == nil`, and
`Overflow` only if a `Mandatory` candidate existed.

**Exactness comparison.** `internal/analyzer/testdata/objective/*.json` holds small instances
(at most 12 candidates) with a brute-forced optimum for the *same declared objective*. The test
asserts the heuristic's value against the exact one and records the ratio; it asserts no bound.

**Guards.** `NewSelector`'s two existing guards are untouched and may not be removed: the
`Pos < p` refusal runs first, then the `scheduler.PSelectionAvailable()` ship-order gate.

## 4. Grammar codec contract (B)

```go
const CodecVersion = 1
// magic: the 16 bytes "qompack-grammar" followed by a NUL
```

- `MarshalBinary` writes `magic || version || payload` and is deterministic for equal grammars.
- `UnmarshalBinary` accepts `version <= CodecVersion`. A **higher** version, a bad magic or a
  truncated payload reports `core.ErrDegraded` and leaves the receiver **unchanged** - that is the
  compatibility reader, not a panic and not a silently empty grammar.
- Round-trip identity: after `UnmarshalBinary(MarshalBinary(g))`, `Rules()`, `Compressed()` and
  `Thrash(n)` equal `g`'s for every `n`.
- The four sentinels `core.ErrNotImplemented | ErrNotFound | ErrBudget | ErrDegraded` remain the
  only legal errors (`grammartest.requireKnownError`).

**The A/B seam.** `MarshalBinary` and `UnmarshalBinary` are methods on the grammar core, which A
owns, but the codec is B's. They meet at a type Main owns in `internal/grammar/types.go`:

```go
type Snapshot struct {
    Rules    []Rule   // every rule, ordered by ID ascending
    Sequence []Symbol // the top-level sequence, terminals and rule references interleaved
    NextID   RuleID   // the ID the next induced rule takes
}

func RuleRef(id RuleID) Symbol              // "\x00R" + decimal id
func ParseRuleRef(s Symbol) (RuleID, bool)  // false for anything that is not a reference
```

- **A** implements `Snapshot() Snapshot` and `Restore(Snapshot) error` on the grammar core, and
  `MarshalBinary`/`UnmarshalBinary` as four-line calls through B's two functions.
- **B** implements `EncodeSnapshot(Snapshot) []byte` and `DecodeSnapshot([]byte) (Snapshot, error)`
  in `codec.go`, against `Snapshot` alone. B never reads `sequitur.go`.

A rule reference inside a `Rule.Body` or a `Compressed()` sequence is `RuleRef(id)`. NUL is the
sigil because no tool name, `"user"`, or `"test:pass"`-style marker can contain one.

## 5. Warning record (B) - `internal/grammar/types.go` (frozen by Main)

`Warning` and `FormatWarning` are **unchanged**: `formatwarning_test.go` and SP-08's injection
assert the wording byte-for-byte. The state-aware layer is new and sits alongside them.

```go
type StateSignature struct{ Goal, Target, Action, Failure string }
func (s StateSignature) Key() string // stable, deterministic, domain-separated

type Progress uint8
const (
    ProgressNone     Progress = iota // nothing observably changed
    ProgressObserved                 // files/env changed, or the failure signature differs
    ProgressUnknown                  // observation coverage is missing for this window
)

type StateWarning struct {
    Signature StateSignature
    Repeats   int
    Turns     []core.TurnIndex
    Progress  Progress
    Uncertain bool          // set for ProgressUnknown and for partial dependency coverage
    DedupKey  string        // Signature.Key() plus the window ordinal
    ExpiresAt core.TurnIndex
    Warning   Warning       // renders through the frozen FormatWarning
}
```

**Bounded delivery, warning-only.** Non-negotiable, and asserted by M6-G15-A:

1. A `StateWarning` never creates an elimination, a prohibition or a binding constraint.
2. `ProgressObserved` suppresses the warning: an ordinary edit-test-edit loop that is changing
   files or failure signatures is not a loop.
3. `ProgressUnknown` may only produce an `Uncertain` warning, never a confident one.
4. Deduplicated by `DedupKey` inside a bounded window; at most `MaxPerSession` delivered.
5. **Self-suppression:** symbols produced by Qompack's own injection, retrieval or warning path
   are excluded from the detector's input, so a warning can never cause the next warning.
6. Usefulness and false alarms are logged separately from repeated-action frequency.

## 6. Diagnostic qualification (C)

- `DeltaScorer.Score` - every block scored, values in [0,1], deterministic. Documented as a
  **behaviour proxy**: token, symbol and path overlap against the *observed* continuation. Not an
  unbiased KL estimate and not a correctness label.
- `DetectRedundancy` - `Superseded` is exact (a later tool use on the same normalized path).
  `NearDups` are **MinHash candidates pending exact verification**; similarity establishes neither
  equivalence nor an exact delta. Read-only: it marks, drops and rewrites nothing.
- Unknown DAG edges are preserved. Nothing here proves content outside a slice irrelevant.
- Disk deduplication does not shrink delivered context and is never counted as though it did.

## 7. Switches - independent disable paths (Main)

| Key | Default | Gates |
|---|---|---|
| `runtime.selection.submodularEnabled` | `false` | the selector; existing, and `TestGuard_SubmodularDefaultsOff` still pins the default |
| `runtime.selection.loopWarningsEnabled` | `false` | state-aware warnings, independently |

Either may be off without affecting the other. Both off is the shipped default, and is exactly
today's behaviour.

## 8. Fixture layout

| Path | Owner | Holds |
|---|---|---|
| `internal/analyzer/testdata/objective/` | D | small instances plus their brute-forced optimum |
| `internal/analyzer/testdata/diagnostics/` | C | counterexample fixtures for the qualified claims |
| `internal/grammar/testdata/codec/` | B | version-tagged compatibility vectors |
| `test/replay/testdata/phase5/`, `phase6/` | E | held-out selection and warning trials |

## 9. Conformance activation

`analyzertest` and `grammartest` skip their `/behaviour` blocks while the implementation probes as
a stub. Landing a real implementation activates them automatically - no suite edit is permitted to
make a block pass. `devtool lint`'s `stubskips` sub-check greps for the literal Rule W-1 message,
so that string is never paraphrased.
