package negknow

import (
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/paths"
)

// This file mints the two NodeIDs this package contributes to the dependence DAG, and emits the
// KindElimination node SP-11 ranks by slice score (§8.6 item 3) and SP-15 counts as high-Δ
// content (G6.3).
//
// Both ids go through SP-07's exported constructors. Concatenating "elimination:"/"file:" onto a
// key compiles and parses, but it skips dag's sanitizeKey — control bytes, invalid UTF-8 and keys
// over 384 bytes are all rewritten there — so a hand-built id can differ from the constructor's
// for the same input and silently fork the graph into two disconnected halves with no error
// anywhere (internal/dag/nodeid.go). testdata/golden/contracts/negknow/node-ids.json pins both
// spellings so a divergence from SP-07/SP-08's naming is caught at the wave-2 verification
// checkpoint rather than at runtime.

// NodeIDFor returns r's elimination node id.
func NodeIDFor(r Record) dag.NodeID { return dag.EliminationNode(r.ID) }

// FileNodeID returns the file node id for p. Unlike dag.FileNode, which requires a key that is
// already normalized, this folds p through paths.Key first — the form every descriptor's
// NormalizedPath is already in.
func FileNodeID(p string) dag.NodeID { return dag.FileNode(paths.Key(p)) }

// emitDAG records r as one KindElimination node with its file and symbol anchors.
//
// It is best effort in the strict sense: every error is logged at Debug and swallowed, and a nil
// graph is simply nothing to write to. An elimination that reached the append-only log is real
// whether or not the graph accepted a node for it, and failing Record over a graph write would
// trade the durable half of the feature for the derived one.
//
// The edge semantics belong to SP-07's builder, not here. dag.BuildElimination points the file
// and symbol anchors INTO the elimination, because the elimination is the CONCLUSION: a backward
// slice from it then returns the work that produced it, which is what §8.3's "is this still
// true?" question needs to be answerable at all. Evidence is deliberately not passed: this
// package's evidence is a content-store root, not a graph node, so there is nothing to name.
func (l *ledger) emitDAG(r Record) {
	if l.deps.Graph == nil {
		return
	}
	err := dag.BuildElimination(l.deps.Graph, dag.EliminationSpec{
		RecordID: r.ID,
		TS:       r.TS,
		PathKey:  r.Desc.NormalizedPath,
		Symbol:   r.Desc.Symbol,
	})
	if err != nil {
		l.log.Debug("negknow: could not emit the elimination's DAG nodes", "id", r.ID, "err", err)
	}
}
