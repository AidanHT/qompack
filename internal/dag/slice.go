package dag

import (
	"time"

	"github.com/qompack/qompack/internal/config"
)

// SliceOptions configures one BackwardSlice or ForwardSlice call (00-ARCHITECTURE.md §5.9).
type SliceOptions struct {
	// Thin drops EdgeControlOnly edges (00-ARCHITECTURE.md §6.4 thin slicing; default true).
	Thin bool
	// MaxDepth bounds the slice's hop depth; 0 means unbounded.
	MaxDepth int
	// MaxNodes hard-caps the slice's node count, setting Slice.Truncated when hit.
	MaxNodes int
	// Decay is the per-hop relevance score decay applied while walking the slice.
	Decay float32
	// Deadline bounds the slice's wall-clock budget.
	Deadline time.Duration
}

// Slice is one BackwardSlice or ForwardSlice result: every visited node's relevance score, a
// descending order to consume them in, and whether the walk was cut short.
type Slice struct {
	// Scores maps every visited NodeID to its relevance score — NOT a binary keep/drop decision
	// (00-ARCHITECTURE.md §8.3).
	Scores map[NodeID]float32
	// Order lists every visited NodeID in descending Scores order, stable-tiebroken by Turn.
	Order []NodeID
	// Truncated reports whether SliceOptions.MaxNodes, MaxDepth or Deadline cut the walk short.
	Truncated bool
	// Visited is the total number of nodes the walk visited.
	Visited int
	// EdgesVisited is the number of edges the walk FOLLOWED out of the nodes it finalized: one
	// per adjacency entry it read an endpoint and a score from. An entry a thin walk drops for
	// being control-only (§6.4) was looked at but not followed, and is not counted.
	//
	// It exists because §6.4's cost claim — thin slicing is the cheaper walk — has to be testable
	// on a shared CI runner, and a wall-clock reading is not. slice_compare_test.go used to
	// compare two sub-millisecond time.Since readings taken inside `go test -count=2 ./...`, and
	// measured the scheduler instead of the walk: CI run 32397340626 read thin 3.99988ms against
	// full 1.82256ms on seed 3, while seed 2 in the same loop read thin 441.63µs against full
	// 4.36204ms doing comparable work. This counter is exactly reproducible, is unaffected by
	// whatever else the runner is doing, and states the claim more strongly than a stopwatch can:
	// thin follows a SUBSET of the edges full follows, and a subset relation cannot hold by luck.
	//
	// It is one increment in the traversal's inner loop and allocates nothing; no production
	// caller reads it, and the walk behaves identically whether or not anyone does.
	EdgesVisited int
}

// The slicing defaults of SP-07. None of these values duplicates a configuration default, so the
// D11 / §11.6 nomagic pass is satisfied without an allow-annotation.
const (
	// DefaultDecay is the per-hop score decay, and it is the one knob deciding how far relevance
	// travels. At 0.85 a pure data-flow chain is still worth roughly 0.20 eight hops out, while a
	// chain of sequence edges has fallen below the traversal floor well before then — which is
	// §6.4's ordering, "recency is a proxy, slicing is relevance", expressed as a number.
	DefaultDecay float32 = 0.85
	// DefaultMaxNodes caps a slice. §6.4 sizes the graph at "a few thousand nodes", so this is a
	// ceiling a healthy session never reaches and a runaway one cannot cross.
	DefaultMaxNodes = 5000
	// DefaultDeadline bounds a slice's wall-clock cost. §8.4's O3 idle work precomputes slices on
	// user think-time, so a slice that overruns must degrade to a partial answer rather than hold
	// the idle worker; Slice.Truncated is how it says it did.
	DefaultDeadline = 5 * time.Millisecond
)

// thinSlicingName is the Appendix C value of "selection.slicing" that selects thin slicing. The
// other legal value is "full"; anything else is treated as not-thin, because a misconfigured key
// must not silently buy the more expensive walk.
const thinSlicingName = "thin"

// DefaultSliceOptions returns the options a production caller should use, taking the thin/full
// choice from Appendix C's "selection": { "slicing": "thin" }.
//
// This is the ONLY place the configured default reaches SliceOptions.Thin. Go's zero value for a
// bool is false, but §5.9 documents thin slicing as the default, so a caller that fills in
// SliceOptions literally gets a full slice while a caller that asks for the default gets a thin
// one. That asymmetry is deliberate: a test spelling out its options means them literally, and
// production code should not have to remember to opt in to the cheaper walk.
func DefaultSliceOptions(cfg config.Config) SliceOptions {
	return SliceOptions{
		Thin:     cfg.Selection.Slicing == thinSlicingName,
		MaxNodes: DefaultMaxNodes,
		Decay:    DefaultDecay,
		Deadline: DefaultDeadline,
	}
}

// withDefaults normalizes an option set before a walk uses it, so the traversal never has to guard
// against a nonsensical value in its inner loop.
//
// Thin is deliberately NOT defaulted here: false is a meaningful choice — a full slice — and there
// is no way to tell "the caller wants a full slice" from "the caller left the field alone".
// Callers obtain the configured default through DefaultSliceOptions instead.
//
// A zero Deadline means "no deadline" and is legal. It is what the deterministic tests use, since
// a wall-clock bound would make their results depend on how loaded the machine happens to be.
func (o SliceOptions) withDefaults() SliceOptions {
	if o.Decay <= 0 || o.Decay > 1 {
		o.Decay = DefaultDecay
	}
	if o.MaxNodes <= 0 {
		o.MaxNodes = DefaultMaxNodes
	}
	if o.MaxDepth < 0 {
		o.MaxDepth = 0
	}
	if o.Deadline < 0 {
		o.Deadline = 0
	}
	return o
}
