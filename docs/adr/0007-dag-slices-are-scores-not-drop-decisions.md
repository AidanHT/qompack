# 7. DAG slices are scores, not drop decisions

Date: 2026-08-15

## Status

Accepted. Implemented by SP-07 (`internal/dag`).

## Context

`Qompack.md` §6.4 replaces the stock "keep the last 5" recency heuristic with program slicing
over the dependence graph the transcript already is. §8.3 is specific about the output shape:

> Output: a relevance score per node, not a binary keep/drop — the score feeds submodular
> selection.

That distinction is not stylistic. §5.2 states the trap it avoids:

> Most content-selection algorithms produce arbitrary subsets, and an arbitrary subset of a
> prefix-cached sequence is a worst-case edit.

and the closing note turns it into an ordering constraint:

> 3. **The cache correction.** Do not ship slicing or submodular selection before p-selection.
> Selection quality is real, but an arbitrary subset of a cached prefix is a worst-case edit, and
> shipping it first would make the system measurably more expensive while looking smarter.

SP-07 lands in wave 1, long before p-selection exists in SP-12. So the question this ADR settles
is: what may this package ship now without violating that ordering?

`00-ARCHITECTURE.md` §5.12 already answers it:

> Note that `dag` (SP-07, wave 1) ships slicing *scores* long before this: scores are legal input
> to ranking inside a checkpoint or rehydration budget, which is not a prefix edit. What closing
> note 3 forbids is a scattered keep-set driving a drop decision, and that path is the one this
> guard closes.

## Decision

`internal/dag` exposes relevance scores and nothing else. No function in the package returns a
keep-set, a drop list, or a boolean per node, and none may be added.

This is enforced three ways, deliberately redundant because a documentation-only rule decays:

1. `doc.go` carries a `NO SELECTION AUTHORITY` note stating the rule and its reason.
2. `api_guard_test.go` parses the package with `go/parser`, walks every exported declaration, and
   fails the build if any result type renders as `map[NodeID]bool`, `map[string]bool` or `[]bool`,
   or if any exported identifier matches `keepset|dropset|^keep|^drop|evict`. It also asserts the
   `doc.go` note still exists, so deleting the prose breaks the build.
3. The drop path itself is closed elsewhere: `analyzer.NewSelector` refuses to construct while
   `scheduler.PSelectionAvailable()` reports false.

The guard is scoped to selection *shapes*, not to `bool` in general. `ParseNodeID`,
`ParseNodeKind` and `ParseEdgeKind` each return a bare `ok bool` reporting parse validity, which is
not a selection decision; a blanket "no exported function returns bool" rule would have flagged all
three and taught everyone to work around the guard.

## Consequences

### Edge direction is uniform (D-1)

Every edge points from earlier/producer to later/consumer in data-flow order. `BackwardSlice`
traverses `In` edges, `ForwardSlice` traverses `Out`. There are no exceptions, including
`EdgeSupersedes` (superseded → superseding) and `EdgeExplains` (evidence → decision). One rule
means a reader never has to remember which edge kinds run backwards.

### Scores are a maximum over paths, not a sum

    score(c) = 1.0 for every live criterion c
    score(v) = max over e=(u→v) of score(u) · Decay · Multiplier(e.Kind) · e.Weight

Every factor lies in (0,1], so scores are non-increasing along any path, and a max-heap keyed on
the tentative score finalizes each node exactly once with its true maximum — Dijkstra with
multiplication in place of addition.

A sum would let a node reachable by many weak paths outrank one reachable by a single strong path,
inverting §6.4's claim that data dependence beats adjacency. A plain BFS would return whichever
path it happened to reach first, making the answer depend on the order an observer appended its
edges. The published fixture demonstrates the difference: in `slice-backward.json`,
`tooluse:toolu_01ABCdef` scores **0.26622** through the two-hop `produces` chain rather than
**0.18424** through the one-hop `supersedes` edge.

### The multiplier table (D-3)

| EdgeKind | Multiplier | Why |
|---|---|---|
| `EdgeProduces` | 1.00 | a tool result *is* its tool use's output — nothing is lost across the hop |
| `EdgeConsumes` | 1.00 | the assistant turn read the result verbatim |
| `EdgeExplains` | 1.00 | evidence → decision is the highest-value link in §4.4's non-reconstructible list |
| `EdgeSharedSymbol` | 0.95 | symbol identity is a strong shared-state claim: two turns on one symbol are working on the same thing |
| `EdgeSharedFile` | 0.88 | weaker than symbol identity — one file has many independent regions, and two turns may share nothing but its name |
| `EdgeSequence` | 0.60 | adjacency is recency, and §6.4 says recency is only a proxy |
| `EdgeControlOnly` | 0.50 | control dependence with no data flow; dropped entirely under thin slicing |
| `EdgeSupersedes` | 0.30 | §8.1 item 3 makes superseded reads the *first* eviction candidates, so reaching one must not resurrect it |
| `EdgeInvalid` | 0.00 | never traversed |

None of these, nor `DefaultDecay = 0.85`, duplicates a configuration default, so the D11 / §11.6
`nomagic` pass is satisfied with no allow-annotation. `0.88` must not be rounded to `0.9` and
`0.60` must not be rounded to `0.55`: both rounded values are in the forbidden set, and both were
chosen to avoid it.

### Thin slicing drops control-only edges and nothing else (D-4)

§6.4:

> Thin slicing drops control-dependence-only edges for much smaller slices at the cost of
> soundness — probably the right tradeoff here.

`SliceOptions.Thin` drops `EdgeControlOnly` and nothing else. `EdgeSequence` survives, carrying its
low 0.60 multiplier, because the claim is that adjacency is weak evidence rather than no evidence.
Thin is the default, wired from Appendix C's `"selection": { "slicing": "thin" }` through
`DefaultSliceOptions`.

"Probably the right tradeoff" is a hypothesis, so it is measured rather than asserted.
`TestThinVsFullComparison` runs eight seeded graphs of ~4,780 nodes and ~14,700 edges and publishes
the table at `testdata/golden/contracts/dag/thin-vs-full.json`:

| seed | nodes | edges | thin | full | size ratio | recall | precision | precision (full) |
|---|---|---|---|---|---|---|---|---|
| 1 | 4777 | 14635 | 566 | 1244 | 0.455 | 0.878 | 0.242 | 0.114 |
| 2 | 4780 | 14866 | 426 | 1277 | 0.334 | 0.880 | 0.380 | 0.135 |
| 3 | 4780 | 14772 | 611 | 1288 | 0.474 | 0.880 | 0.227 | 0.116 |
| 4 | 4779 | 14722 | 544 | 1259 | 0.432 | 0.882 | 0.276 | 0.125 |
| 5 | 4780 | 14708 | 545 | 1321 | 0.413 | 0.879 | 0.294 | 0.132 |
| 6 | 4778 | 14806 | 535 | 1224 | 0.437 | 0.879 | 0.258 | 0.121 |
| 7 | 4780 | 14702 | 626 | 1355 | 0.462 | 0.880 | 0.281 | 0.138 |
| 8 | 4780 | 14727 | 533 | 1254 | 0.425 | 0.879 | 0.328 | 0.151 |
| **mean** | | | | | **0.429** | **0.880** | **0.286** | **0.129** |

A thin slice is **43% the size** of the full slice, retains **88% of the ground-truth relevant
set**, and is **2.2× more precise** (0.286 against 0.129). It is also faster on every seed — 149–343
µs against 628–951 µs, each the mean of a timed batch rather than a single-shot reading, because one
slice is not far enough above the clock's granularity to be timed on its own — which follows from it
visiting a strict subset of the edges. §6.4's hedge is therefore resolved in
favour of thin slicing, and the assertions fail the build if the mean size ratio rises above 0.75
or mean recall falls below 0.85 — so a change that makes thin slicing pointless surfaces as a
number somebody has to explain rather than as a quality regression nobody can attribute.

### The graph is not acyclic, and consumers must tolerate that

D-7 forbids one specific cycle: an assistant node must never consume the result of the tool use it
emitted, because `tool_use → tool_result → assistant → tool_use` would make a backward slice from
any tool use swallow that tool use's own forward chain at full score.

The *whole* graph, however, is not a DAG and cannot be. Reading a file at one turn and editing it
at a later one closes a legitimate loop, because §8.1 item 4 directs a shared-file edge INTO a tool
use that consumed the file and OUT of one that produced it:

    tooluse:t1 → toolresult:t1 → assistant:2 → tooluse:t2 → file:a → tooluse:t1

Every one of those edges is individually correct; it is the ordinary Read-then-Edit pattern,
faithfully recorded. `TestReadThenWriteClosesALegitimateCycle` pins this so it cannot be assumed
away. Slicing tolerates it by construction rather than by a visited-set bolted on afterwards:
scores strictly decrease along any path, each node is finalized exactly once, and the `minScore`
floor bounds the walk independently of either. **SP-09's negative-knowledge detector, which scans
this graph for the test-fail → revert → different-approach pattern, inherits the same obligation.**

### Measured cost

Recorded in `testdata/bench-baseline.txt` on a 4,780-node / 14,702-edge graph:

| Operation | Budget | Measured |
|---|---|---|
| `BackwardSlice`, thin | < 1 ms (§6.4 "sub-millisecond") | **0.40 ms** |
| `BackwardSlice`, full | not gated | 1.15 ms |
| `ForwardSlice`, thin | < 1 ms | 0.046 ms |
| `CrossingEdges` (index warm) | < 5 µs (§8.4) | **0.27 µs** |
| `AddNode` + `AddEdge` pair, no flush | < 3 µs | **1.21 µs** |
| Index rebuild after mutation | < 10 ms | 5.3 ms |
| `Open` of a ~19,500-record log | < 250 ms | 88 ms |
| `Compact` of a ~19,500-record log | < 400 ms | 41 ms |

Every figure above is from an uninstrumented build, which is the only build the budgets are a claim
about. `TestSliceLatencyBudget` and `TestCrossingLatencyBudget` assert them on every `go test`,
which includes `ci-local`'s `test` step — so the real budget is enforced in CI, not merely
benchmarked. Two of the other ways this module is tested do not produce a shipped binary, and both
inflate the same walk substantially:

| Build | Backward slice | Inflation |
|---|---|---|
| uninstrumented | 0.40 ms | — |
| `devtool cover` (`-covermode=atomic`) | 0.81–1.18 ms | ~3× |
| `devtool test-race` (`-race`) | 2.02 ms | ~5× |

Those runs measure the instrumentation, so the gates scale their ceiling by a documented factor per
instrumentation rather than asserting 1 ms against a build that can never meet it. They scale
rather than skip: a gate that silently evaporates under `-race` is one nobody notices has stopped
running, and scaled they still fail on the regression class they exist to catch — the `orderByScore`
defect below cost 5.4×, which breaches every ceiling.

Two of these were only reached by fixing what the benchmark exposed, which is the argument for
having written them:

- `orderByScore` originally read `scores[a]`, `scores[b]`, `nodes[a].Turn` and `nodes[b].Turn`
  inside its comparator — four string-map lookups per comparison, so roughly twenty thousand
  lookups to order a 566-node slice. Materializing each node's key once before sorting cut
  `BackwardSlice` from 2.09 ms to 0.39 ms, a **5.4× improvement**, and moved it from missing §6.4's
  budget to clearing it comfortably.
- `withIndex` originally released the read lock, rebuilt under the write lock, then re-acquired and
  re-checked in a loop. Under concurrent writers a reader re-dirties on every pass and pays repeated
  O(N log N) rebuilds; the concurrency test ran over six minutes without completing. Running the
  read closure under the write lock on the dirty path caps the work at one rebuild per call.

## What this ADR does not decide

Δ-scoring, submodular selection, redundancy reports and any keep/drop decision derived from these
scores belong to SP-15. `segment_coupling`'s *use* — the p-selection score itself — belongs to
SP-12. This package supplies `CrossingEdges(pos)` and `NodesAfter(pos)` and stops there.
