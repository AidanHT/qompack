package daemon

import (
	"context"
	"fmt"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// maxAssembledCandidates caps Inputs.Candidates (Qompack.md §5.4 "Twenty candidates, not
// 167,000"). When more boundaries qualify, the highest-Pos ones are kept: a very early boundary
// can only win when the cache is cold, and in that regime rewrite = 0 makes even the 32nd-latest
// boundary a deep cut relative to n, so the newest boundaries are where the decision is close.
const maxAssembledCandidates = 32 // §5.4 "Twenty candidates, not 167,000"

// The candidate-assembly instruments.
const (
	// counterCandidateUnresolved counts changepoint turns with no DAG node to anchor them.
	counterCandidateUnresolved = "sched.candidate.unresolved"
	// counterCandidateCrossingPanic counts candidates dropped because dag.CrossingEdges panicked.
	counterCandidateCrossingPanic = "sched.candidate.crossing_panic"
)

// candidateAssembler builds Inputs.Candidates — changepoint boundaries ∩ API-round boundaries
// (Qompack.md §8.4) — from the dependence DAG and the segment log, memoizing the expensive
// turn→Pos map and recomputing coupling live on every call.
type candidateAssembler struct {
	graph   dag.Graph
	segs    store.SegmentLog
	log     logging.Logger
	metrics obs.Registry

	turnPos map[core.TurnIndex]turnAnchor // Turn → smallest Node.Pos at that turn
	builtAt core.TurnIndex                // maxTurn the map was built at; rebuild when it advances
	built   bool                          // false until the first build (turn 0 is a legal builtAt)
}

// turnAnchor is where a turn begins in the prefix and which segment it belongs to.
type turnAnchor struct {
	Pos     int
	Segment core.SegmentID
}

// newCandidateAssembler returns an assembler over graph and segs. Either may be nil, in which
// case Assemble reports no candidates; a nil log or metrics is replaced by a no-op.
func newCandidateAssembler(graph dag.Graph, segs store.SegmentLog, log logging.Logger, metrics obs.Registry) *candidateAssembler {
	if log == nil {
		log = logging.Nop()
	}
	return &candidateAssembler{graph: graph, segs: segs, log: log, metrics: metrics}
}

// Invalidate drops the memoized turn→Pos map so the next Assemble rebuilds it. The Runtime
// calls it on BindSession.
func (a *candidateAssembler) Invalidate() {
	a.turnPos = nil
	a.built = false
	a.builtAt = 0
}

// Assemble builds Inputs.Candidates: changepoint boundaries ∩ API-round boundaries (§8.4).
// maxTurn is the highest turn the Runtime has observed; it is the turn→Pos cache's version key.
//
// For every changepoint turn in cpTurns that resolves to a DAG position, one Candidate is
// produced with RoundBoundary = t ∈ rounds, Coupling = graph.CrossingEdges(pos) (live, never
// cached — two binary searches at ~0.27 µs, cheaper than any invalidation probe dag offers) and
// ReclaimableTokens = idx.After(pos) (0 when idx is nil). Unresolvable turns are skipped and
// counted on sched.candidate.unresolved; a CrossingEdges panic is recovered per candidate,
// logged Loud once per call, counted on sched.candidate.crossing_panic, and that candidate is
// dropped. The result is ascending by Pos and holds at most maxAssembledCandidates, the
// highest-Pos ones — chosen among the round-boundary candidates first when more than the cap
// qualify (ruling R44; the unfiltered set only when none is a boundary). A nil graph or segment
// log yields (nil, nil).
func (a *candidateAssembler) Assemble(
	ctx context.Context,
	sess core.SessionID,
	cpTurns []core.TurnIndex,
	rounds map[core.TurnIndex]struct{},
	idx *reclaimableIndex,
	maxTurn core.TurnIndex,
) ([]scheduler.Candidate, error) {
	if a.graph == nil || a.segs == nil {
		return nil, nil
	}
	if !a.built || maxTurn > a.builtAt {
		if err := a.rebuild(ctx, maxTurn); err != nil {
			return nil, err
		}
	}

	out := make([]scheduler.Candidate, 0, len(cpTurns))
	loud := false
	for _, t := range cpTurns {
		anchor, ok := a.turnPos[t]
		if !ok {
			a.count(counterCandidateUnresolved)
			continue
		}
		coupling, ok := a.coupling(sess, anchor.Pos, &loud)
		if !ok {
			continue
		}
		_, round := rounds[t]
		var reclaimable core.Tokens
		if idx != nil {
			reclaimable = idx.After(anchor.Pos)
		}
		out = append(out, scheduler.Candidate{
			Pos:               anchor.Pos,
			Turn:              t,
			SegmentID:         anchor.Segment,
			RoundBoundary:     round,
			ReclaimableTokens: reclaimable,
			Coupling:          coupling,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	// Ruling R44's order, the same in all three paths (Evaluate, l3policy.candidates, here): when
	// the cap binds, the round-boundary filter runs BEFORE the highest-Pos cut, falling back to
	// the unfiltered set only when no candidate is a boundary. With the cut first, 32
	// off-boundary changepoints could evict every eligible one and hand Evaluate a set it can
	// only "relax" onto. Under the cap nothing is filtered here: Evaluate applies the same
	// filter itself, and every resolved changepoint stays observable through the snapshot.
	if len(out) > maxAssembledCandidates {
		if elig := onRoundBoundary(out); len(elig) > 0 {
			out = elig
		}
		if len(out) > maxAssembledCandidates {
			out = out[len(out)-maxAssembledCandidates:]
		}
	}
	return out, nil
}

// onRoundBoundary returns the candidates with RoundBoundary set, in their input order; nil when
// none has it.
func onRoundBoundary(cands []scheduler.Candidate) []scheduler.Candidate {
	var elig []scheduler.Candidate
	for _, c := range cands {
		if c.RoundBoundary {
			elig = append(elig, c)
		}
	}
	return elig
}

// rebuild walks every node once (graph.NodesAfter(0) is the one call that enumerates nodes),
// records the smallest Pos per turn, then resolves each turn's segment with one segs.Range(t, t)
// call — the first match, or 0 when none. Turns are resolved in ascending order so the Range
// call sequence is deterministic. On success the map is stamped with maxTurn.
func (a *candidateAssembler) rebuild(ctx context.Context, maxTurn core.TurnIndex) error {
	nodes := a.graph.NodesAfter(0)
	m := make(map[core.TurnIndex]turnAnchor, len(nodes))
	turns := make([]core.TurnIndex, 0, len(nodes))
	for _, n := range nodes {
		cur, ok := m[n.Turn]
		if !ok {
			turns = append(turns, n.Turn)
			m[n.Turn] = turnAnchor{Pos: n.Pos}
			continue
		}
		if n.Pos < cur.Pos {
			cur.Pos = n.Pos
			m[n.Turn] = cur
		}
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i] < turns[j] })
	for _, t := range turns {
		if err := ctx.Err(); err != nil {
			return err
		}
		segs, err := a.segs.Range(ctx, t, t)
		if err != nil {
			return fmt.Errorf("scheduler: resolve segment of turn %d: %w", int(t), err)
		}
		if len(segs) > 0 {
			anchor := m[t]
			anchor.Segment = segs[0].ID
			m[t] = anchor
		}
	}
	a.turnPos = m
	a.builtAt = maxTurn
	a.built = true
	return nil
}

// coupling calls graph.CrossingEdges(pos) live. A panic is recovered here, at the assembler
// boundary: the candidate is reported as not ok, the first panic of a call is logged Loud (the
// *loud flag suppresses repeats within the same Assemble), and every one is counted.
func (a *candidateAssembler) coupling(sess core.SessionID, pos int, loud *bool) (n int, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			n, ok = 0, false
			a.count(counterCandidateCrossingPanic)
			if !*loud {
				*loud = true
				a.log.Loud("scheduler: dag.CrossingEdges panicked; candidate dropped",
					"session", string(sess), "pos", pos, "panic", fmt.Sprint(r))
			}
		}
	}()
	return a.graph.CrossingEdges(pos), true
}

// count bumps a counter when a registry is wired.
func (a *candidateAssembler) count(name string) {
	if a.metrics != nil {
		a.metrics.Counter(name).Add(1)
	}
}
