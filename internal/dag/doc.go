// Package dag implements the dependence DAG of 00-ARCHITECTURE.md §5.9: the L1/L2 graph over
// tool uses, tool results, assistant turns, user prompts, files, symbols, decisions, eliminations
// and segments, connected by sequence/produces/consumes/shared-file/shared-symbol/supersedes/
// explains/control-only edges, persisted to dag/deps.jsonl and sliced (backward and forward) to
// drive both compaction selection and retrieval.
//
// dag must import ONLY the foundation packages (00-ARCHITECTURE.md §3.2: dag's own allow-set is
// "{}", meaning foundation only) — in particular it must NEVER import store, negknow or scheduler,
// which is what keeps analyzer/negknow free to depend on dag without a cycle.
//
// SP-01 ships the complete §5.9 type set — plus GraphStats, the §14.0 addition §5.9 references
// but does not itself define — as real declarations, and every Graph operation as a stub:
// AddNode, AddEdge, BackwardSlice, ForwardSlice, Flush and Compact report core.ErrNotImplemented;
// Node, Out, In, NodesAfter, CrossingEdges and Stats (which have no error return) report the
// documented zero value — until SP-07 lands the real graph.
//
// Node and Edge additionally carry hand-written MarshalJSON methods so that dag/deps.jsonl's two
// record shapes — one node line, one edge line, sharing a single append-only log — match the
// frozen fixtures at testdata/golden/contracts/dag/want/{node_line,edge_line}.jsonl exactly,
// including the "type" discriminator that tells a reader which shape a given line is before it
// decodes the rest. See Node.MarshalJSON's own comment for why that discriminator is not a Node
// field.
package dag
