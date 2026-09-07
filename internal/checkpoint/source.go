package checkpoint

import (
	"fmt"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// SourceSet is the ONLY thing a checkpoint may be built from (00-ARCHITECTURE.md §5.14, §8.5).
// Every field is a seam onto durable, original content: the object store, the segment log, the
// elimination ledger, the pin log, the dependence DAG, the action grammar and the token
// estimator. It deliberately has NO field that can carry live context text.
//
// That absence is the mechanical enforcement of §4.6's "never compress a compression": with no
// way to hand the writer a summary, compilation itself makes "checkpoint from a summary"
// impossible. Adding a string, a []byte or a transcript-shaped field here would silently undo the
// invariant the whole design rests on.
type SourceSet struct {
	// Store is the content-addressed store the original bytes are read back from.
	Store store.Store
	// Segments is the segment log MarkEncoded enforces the DPI guard through.
	Segments store.SegmentLog
	// Ledger supplies the tier-1 eliminated[] records.
	Ledger negknow.Ledger
	// Pins supplies the tier-1 invariants[].
	Pins pins.Store
	// Graph supplies slice scores for ranking and the EdgeExplains chains ExtractDecisions mints
	// decisions from.
	Graph dag.Graph
	// Grammar supplies the compressed action history.
	Grammar grammar.Sequitur
	// Tokens prices every tier against the checkpoint budget.
	Tokens tokens.Estimator
}

// Validate reports the first seam left nil, naming it. Begin calls it before touching anything,
// so a half-wired composition root fails at the top of the call with a message that says which
// dependency is missing, rather than as a nil dereference several frames down inside Advance.
func (s SourceSet) Validate() error {
	switch {
	case s.Store == nil:
		return fmt.Errorf("checkpoint: SourceSet.Store is nil")
	case s.Segments == nil:
		return fmt.Errorf("checkpoint: SourceSet.Segments is nil")
	case s.Ledger == nil:
		return fmt.Errorf("checkpoint: SourceSet.Ledger is nil")
	case s.Pins == nil:
		return fmt.Errorf("checkpoint: SourceSet.Pins is nil")
	case s.Graph == nil:
		return fmt.Errorf("checkpoint: SourceSet.Graph is nil")
	case s.Grammar == nil:
		return fmt.Errorf("checkpoint: SourceSet.Grammar is nil")
	case s.Tokens == nil:
		return fmt.Errorf("checkpoint: SourceSet.Tokens is nil")
	}
	return nil
}

// Draft is an in-progress checkpoint: the incrementally accumulated tiers Advance encodes closed
// segments into during idle time (O5, §7), so that Finalize has nothing heavy left to do and can
// complete inside budget B-E. Begin hands the caller a *Draft and Advance, Finalize and Abort
// each take it back; the pointer is the draft's identity.
//
// All fields are guarded by mu. The writer's own lock (FileWriter.mu) guards only the session →
// draft registry; the two are never held together in this file, which is what keeps the lock
// order trivial.
type Draft struct {
	mu      sync.Mutex
	session core.SessionID
	seq     core.CheckpointSeq
	parent  core.CheckpointSeq
	// cp is the accumulated artifact. Two fields are materialized at snapshot time rather than
	// maintained in place: EncodedSegments (derived from encoded, so the two can never disagree)
	// and OpenQuestions (derived from userOQ + derivedOQ, so explicit questions always sort
	// first, per §8's open-questions bullet).
	cp       Checkpoint
	encoded  map[core.SegmentID]bool
	frontier core.TurnIndex
	src      SourceSet
	started  time.Time
	// dirty records that the in-memory draft has diverged from state/draft-<session>.json since
	// the last write, and it GATES persistLocked. Without the gate, the idle tick's Begin+Advance
	// sweep re-marshals and re-fsyncs an unchanged draft for every live session — and for the
	// finished sessions liveSessions folds in from the store — on every tick, forever.
	dirty bool
	// sealed closes the draft to further content. Finalize sets it inside the same critical
	// section that takes the snapshot, so a concurrent Advance cannot mark segments as encoded
	// into a checkpoint whose bytes have already been decided: those segments would then be
	// unreachable by every later checkpoint (Unencoded filters them out, and Advance's own DPI
	// guard skips them for the successor), which is the §8.2 content loss the guard exists to
	// prevent. Abort sets it too and never clears it, so a discarded draft can never become an
	// artifact. A Finalize that fails before the artifact is committed clears it again, so a
	// transient write failure does not strand the session.
	sealed bool
	// workExplicit records that SetCurrentWork was called; once true, Advance stops deriving
	// CurrentWork and leaves the caller's value alone (§7). It survives restarts through the
	// draft file's work_explicit key, because a daemon restart must not silently resume
	// inventing a goal the user had already stated.
	workExplicit bool

	// path is where this draft persists: state/draft-<session>.json (§7). It is state/, not
	// checkpoints/, precisely so paths.WriteAtomic's replace is legal.
	path string
	// userOQ holds the questions SetOpenQuestions/AddOpenQuestion contributed; derivedOQ holds
	// the ones Advance derived from stale eliminations. Kept apart so the materialized list can
	// keep the §8 promise that explicit entries are preserved and always sort first.
	userOQ    []string
	derivedOQ []string
	// fileTurn and toolTurn record the turn each live pointer was last touched at, so the
	// prepend-dedup of §8's pointer-ordering rule can keep "highest turn wins" even if a caller
	// replays segments out of ascending order. They are in-memory only: the ascending-id
	// processing order Advance enforces makes them redundant on the normal path, so losing them
	// across a resume costs nothing but this safety net.
	fileTurn map[string]core.TurnIndex
	toolTurn map[core.ToolUseID]core.TurnIndex
}

// Ref is the durable reference to one finalized checkpoint artifact (00-ARCHITECTURE.md §5.14):
// enough to locate it, verify it, and know what it cost.
type Ref struct {
	// Seq is the checkpoint's 1-based sequence number.
	Seq core.CheckpointSeq
	// Path is the artifact's path on disk, e.g. "<root>/.qompack/checkpoints/0007.json".
	Path string
	// SHA256 is the artifact's content digest, as recorded in checkpoints/MANIFEST.jsonl and
	// re-verified by Reader.Verify.
	SHA256 core.Hash
	// Bytes is the artifact's size on disk.
	Bytes int64
	// Tokens is the artifact's estimated token cost.
	Tokens core.Tokens
	// Frontier is the turn index this checkpoint advanced the encoding frontier to.
	Frontier core.TurnIndex
	// Created is when the artifact was finalized.
	Created core.UnixMilli
}

// nodesInRange returns every live node of one of kinds whose Turn falls in [from,to].
//
// It exists because dag.Graph has no query language: the shipped interface offers exactly one bulk
// accessor, NodesAfter(pos int), keyed on token POSITION rather than turn, with no kind-keyed
// lookup and no turn-range lookup. So every "for every node of kind K with from ≤ Turn ≤ to"
// phrase in this package routes through here.
//
// NodesAfter(0) is a FULL-GRAPH SCAN — O(V) in both allocation and copy. Call this once and
// partition the result by Kind; never once per kind, and never inside a loop. Two benchmark
// budgets are set against exactly one scan each: BenchmarkAdvanceSegment (< 25 ms) prices one scan
// plus Out(id) over the matched subset per Advance, and BenchmarkExtractDecisions (< 20 ms over
// 5 000 nodes / 12 000 edges) prices one scan plus Out(id) over every matched node. Both run only
// during idle windows or inside the finalize-only PreCompact path.
//
// A nil graph yields no nodes rather than panicking, so the receiver-less callers named in §5b keep
// working with no writer constructed.
func nodesInRange(g dag.Graph, kinds []dag.NodeKind, from, to core.TurnIndex) []dag.Node {
	if g == nil || len(kinds) == 0 || to < from {
		return nil
	}
	want := make(map[dag.NodeKind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	all := g.NodesAfter(0)
	out := make([]dag.Node, 0, len(all))
	for _, n := range all {
		if want[n.Kind] && n.Turn >= from && n.Turn <= to {
			out = append(out, n)
		}
	}
	return out
}
