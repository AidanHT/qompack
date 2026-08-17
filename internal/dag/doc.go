// Package dag is the dependence graph over the transcript: tool_use → tool_result →
// assistant_turn → next_tool_use, plus shared-state edges keyed on file path and symbol name
// (Qompack.md §8.1 item 4). It answers the §6.4 relevance question by backward slicing from a
// criterion set, and it answers the §8.4 cache question — segment_coupling(p) — with
// CrossingEdges(pos).
//
// NO SELECTION AUTHORITY. This package exposes relevance SCORES and nothing else. A score is
// legal input to ranking inside a checkpoint budget (§8.5) or a rehydration budget (§8.6),
// because neither is a prefix edit. It is NOT authority to drop a block from the live context.
// Qompack.md closing note 3 forbids shipping slicing or submodular selection before p-selection,
// and 00-ARCHITECTURE.md §5.12 closes that path in analyzer.NewSelector, which refuses to
// construct unless scheduler.PSelectionAvailable() reports true. No function in this package
// returns a keep-set, a drop list, or a boolean per node, and none may be added — api_guard_test.go
// parses this package with go/parser and fails the build if one appears.
//
// dag must import ONLY the foundation packages (00-ARCHITECTURE.md §3.2: dag's own allow-set is
// "{}", meaning foundation only) — in particular it must NEVER import store, symbols, eval,
// negknow or scheduler, which is what keeps analyzer/negknow free to depend on dag without a
// cycle. Symbol names arrive as a plain []string, resolved by the caller.
//
// # The two frozen wire shapes
//
// Node and Edge carry hand-written MarshalJSON methods so that dag/deps.jsonl's record shapes —
// sharing a single append-only log — match the frozen fixtures at
// testdata/golden/contracts/dag/want/{node_line,edge_line}.jsonl exactly, including the "type"
// discriminator that tells a reader which shape a given line is before it decodes the rest. See
// Node.MarshalJSON's own comment for why that discriminator is not a Node field.
//
// Those fixtures are frozen under Rule W-2, and they are load-bearing in a way that is easy to
// break by accident. node_line.jsonl pins "kind":4 to KindFile and edge_line.jsonl pins "kind":2
// to EdgeConsumes, so the iota blocks in node.go and edge.go may never be reordered and the
// KindInvalid/EdgeInvalid sentinels sit at the END of their blocks rather than at the front.
// For the same reason NodeKind and EdgeKind deliberately do NOT implement encoding.TextMarshaler:
// giving them a MarshalText method silently reroutes Node's and Edge's own JSON through it,
// rewriting "kind":4 as "kind":"file" and making the frozen fixtures fail to decode at all. Text
// names exist — NodeKind.String, ParseNodeKind — and are used for GraphStats map keys and log
// output, never on the wire. kinds_test.go's TestFrozenFixtureKindNumberingUnchanged is the guard.
//
// # Positions
//
// Every node carries Pos, its token position in the prefix. That single field is what lets this
// package answer both of the scheduler's questions without a second index anywhere in the
// repository: CrossingEdges(pos) counts the edges straddling a candidate cut point, and
// NodesAfter(pos) returns the suffix-constrained candidate set that §5.3 makes the only legal
// input to selection.
package dag
