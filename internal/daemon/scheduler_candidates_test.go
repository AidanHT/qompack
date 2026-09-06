package daemon

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/scheduler"
)

// The candidate fixture: a session of fixtureTurns turns whose turn t begins at Pos t·posPerTurn,
// with one closed segment per segTurns consecutive turns.
const (
	fixtureTurns = 20
	posPerTurn   = 1_000
	segTurns     = 5
	fixtureSess  = core.SessionID("sess-cand")
)

// assemblerFixture bundles the doubles one Assemble test drives.
type assemblerFixture struct {
	graph *fakeGraph
	segs  *fakeSegmentLog
	log   *recordingLogger
	reg   obs.Registry
	asm   *candidateAssembler
}

func newAssemblerFixture(t *testing.T, turns int) *assemblerFixture {
	t.Helper()
	f := &assemblerFixture{
		graph: newFakeGraph(),
		segs:  newFakeSegmentLog(),
		log:   newRecordingLogger(),
		reg:   obs.New(newFakeClock(epoch)),
	}
	f.graph.addTurnNodes(t, turns, posPerTurn)
	for start := 1; start <= turns; start += segTurns {
		end := min(start+segTurns-1, turns)
		f.segs.addSegment(t, fixtureSess, core.TurnIndex(start), core.TurnIndex(end), core.Tokens(segTurns*posPerTurn))
	}
	f.asm = newCandidateAssembler(f.graph, f.segs, f.log, f.reg)
	return f
}

func turnSet(turns ...core.TurnIndex) map[core.TurnIndex]struct{} {
	m := make(map[core.TurnIndex]struct{}, len(turns))
	for _, t := range turns {
		m[t] = struct{}{}
	}
	return m
}

// expectedSegment is the fixture's segment ID for turn t: segments are opened in turn order.
func expectedSegment(t core.TurnIndex) core.SegmentID {
	return core.SegmentID((int(t)-1)/segTurns + 1)
}

func TestAssemble_IntersectsChangepointsAndRounds(t *testing.T) {
	t.Parallel()

	f := newAssemblerFixture(t, fixtureTurns)
	cands, err := f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{5, 10, 15}, turnSet(5, 15), nil, fixtureTurns)
	require.NoError(t, err)
	require.Len(t, cands, 3)
	for i, want := range []core.TurnIndex{5, 10, 15} {
		require.Equal(t, want, cands[i].Turn)
		require.Equal(t, int(want)*posPerTurn, cands[i].Pos)
		require.Equal(t, expectedSegment(want), cands[i].SegmentID)
	}
	require.True(t, cands[0].RoundBoundary)
	require.False(t, cands[1].RoundBoundary, "turn 10 is a changepoint but not an API-round boundary")
	require.True(t, cands[2].RoundBoundary)
}

func TestAssemble_SkipsUnresolvableTurns(t *testing.T) {
	t.Parallel()

	f := newAssemblerFixture(t, fixtureTurns)
	cands, err := f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{5, 99}, turnSet(5, 99), nil, fixtureTurns)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, core.TurnIndex(5), cands[0].Turn)
	require.Equal(t, int64(1), f.reg.Counter("sched.candidate.unresolved").Value())
}

func TestAssemble_CouplingFromCrossingEdges(t *testing.T) {
	t.Parallel()

	f := newAssemblerFixture(t, fixtureTurns)
	f.graph.crossingFn = func(pos int) int { return pos / 1000 }
	cands, err := f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{3, 7, 12}, nil, nil, fixtureTurns)
	require.NoError(t, err)
	require.Len(t, cands, 3)
	for _, c := range cands {
		require.Equal(t, c.Pos/1000, c.Coupling, "Pos %d", c.Pos)
	}
	require.Equal(t, []int{3 * posPerTurn, 7 * posPerTurn, 12 * posPerTurn}, f.graph.crossingCalls)
}

func TestAssemble_CouplingRecomputedEveryCall(t *testing.T) {
	t.Parallel()

	f := newAssemblerFixture(t, fixtureTurns)
	for range 2 {
		cands, err := f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{4, 8, 12}, nil, nil, fixtureTurns)
		require.NoError(t, err)
		require.Len(t, cands, 3)
	}
	require.Len(t, f.graph.crossingCalls, 6, "coupling is live: two binary searches per candidate per call, never cached")
}

func TestAssemble_TurnPosCachedUntilTurnAdvances(t *testing.T) {
	t.Parallel()

	f := newAssemblerFixture(t, fixtureTurns)
	for range 2 {
		_, err := f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{4, 8, 12}, nil, nil, fixtureTurns)
		require.NoError(t, err)
	}
	require.Equal(t, []int{0}, f.graph.nodesAfterCalls, "exactly one NodesAfter(0) walk")
	require.Len(t, f.segs.rangeCalls, fixtureTurns, "one segs.Range per turn, not two rounds")
	for _, rc := range f.segs.rangeCalls {
		require.Equal(t, rc[0], rc[1], "Range(t, t) per turn")
	}
}

func TestAssemble_TurnPosRebuiltWhenTurnAdvances(t *testing.T) {
	t.Parallel()

	f := newAssemblerFixture(t, fixtureTurns)
	cands, err := f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{5, fixtureTurns + 1}, nil, nil, fixtureTurns)
	require.NoError(t, err)
	require.Len(t, cands, 1, "turn 21 does not exist yet")

	newTurn := core.TurnIndex(fixtureTurns + 1)
	require.NoError(t, f.graph.AddNode(dag.Node{
		ID: "tooluse:turn-21", Kind: dag.KindToolUse, Turn: newTurn, Pos: int(newTurn) * posPerTurn,
	}))
	f.segs.addSegment(t, fixtureSess, newTurn, newTurn, posPerTurn)

	cands, err = f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{5, newTurn}, nil, nil, newTurn)
	require.NoError(t, err)
	require.Len(t, cands, 2)
	require.Equal(t, newTurn, cands[1].Turn)
	require.Equal(t, int(newTurn)*posPerTurn, cands[1].Pos)
	require.Equal(t, core.SegmentID(fixtureTurns/segTurns+1), cands[1].SegmentID)
	require.Equal(t, []int{0, 0}, f.graph.nodesAfterCalls, "the map is rebuilt once the turn advances")
}

func TestAssemble_CapAt32KeepsHighestPos(t *testing.T) {
	t.Parallel()

	const turns = 100
	f := newAssemblerFixture(t, turns)
	cp := make([]core.TurnIndex, 0, turns)
	for i := turns; i >= 1; i-- { // deliberately descending: the output order must not depend on it
		cp = append(cp, core.TurnIndex(i))
	}
	cands, err := f.asm.Assemble(context.Background(), fixtureSess, cp, nil, nil, turns)
	require.NoError(t, err)
	require.Len(t, cands, maxAssembledCandidates)
	require.True(t, sort.SliceIsSorted(cands, func(i, j int) bool { return cands[i].Pos < cands[j].Pos }))
	require.Equal(t, (turns-maxAssembledCandidates+1)*posPerTurn, cands[0].Pos, "the lowest kept is the 32nd-highest")
	require.Equal(t, turns*posPerTurn, cands[len(cands)-1].Pos)
}

// TestAssemble_CapPrefersRoundBoundaries is ruling R44 on the daemon path: with more changepoint
// turns than the cap, the highest-Pos ones off any round boundary must not evict the eligible
// ones — the filter runs before the cut, and only an empty intersection falls back to the
// unfiltered set.
func TestAssemble_CapPrefersRoundBoundaries(t *testing.T) {
	t.Parallel()

	const turns = 100
	const boundaryTurns = 40 // turns 1..40 are round boundaries; 41..100 — the highest-Pos ones — are not
	f := newAssemblerFixture(t, turns)
	cp := make([]core.TurnIndex, 0, turns)
	rounds := make([]core.TurnIndex, 0, boundaryTurns)
	for i := 1; i <= turns; i++ {
		cp = append(cp, core.TurnIndex(i))
		if i <= boundaryTurns {
			rounds = append(rounds, core.TurnIndex(i))
		}
	}

	cands, err := f.asm.Assemble(context.Background(), fixtureSess, cp, turnSet(rounds...), nil, turns)
	require.NoError(t, err)
	require.Len(t, cands, maxAssembledCandidates)
	for _, c := range cands {
		require.True(t, c.RoundBoundary, "turn %d: an off-boundary candidate took a cap slot", c.Turn)
	}
	require.Equal(t, core.TurnIndex(boundaryTurns-maxAssembledCandidates+1), cands[0].Turn, "the 32 highest-Pos boundaries survive")
	require.Equal(t, core.TurnIndex(boundaryTurns), cands[len(cands)-1].Turn)
	require.True(t, sort.SliceIsSorted(cands, func(i, j int) bool { return cands[i].Pos < cands[j].Pos }))

	// No boundary at all: the unfiltered set is capped, exactly as before.
	cands, err = f.asm.Assemble(context.Background(), fixtureSess, cp, nil, nil, turns)
	require.NoError(t, err)
	require.Len(t, cands, maxAssembledCandidates)
	require.Equal(t, core.TurnIndex(turns-maxAssembledCandidates+1), cands[0].Turn)
	require.Equal(t, core.TurnIndex(turns), cands[len(cands)-1].Turn)

	// Under the cap nothing is filtered: Evaluate applies the boundary filter itself.
	cands, err = f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{5, 10, 15}, turnSet(5), nil, turns)
	require.NoError(t, err)
	require.Len(t, cands, 3)
}

func TestAssemble_ReclaimableFromIndex(t *testing.T) {
	t.Parallel()

	f := newAssemblerFixture(t, fixtureTurns)
	idx := newReclaimableIndex([]dropBlock{
		{Pos: 2_500, Tokens: 300, Class: DropOrdinary},
		{Pos: 7_000, Tokens: 200, Class: DropEphemeral},
		{Pos: 7_500, Tokens: 50, Class: DropNone},
		{Pos: 12_200, Tokens: 120, Class: DropSuperseded},
	})
	cands, err := f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{2, 7, 12, 13}, nil, idx, fixtureTurns)
	require.NoError(t, err)
	require.Len(t, cands, 4)
	for _, c := range cands {
		require.Equal(t, idx.After(c.Pos), c.ReclaimableTokens, "Pos %d", c.Pos)
	}
	require.Equal(t, core.Tokens(620), cands[0].ReclaimableTokens) // Pos 2000: everything droppable after it
	require.Equal(t, core.Tokens(320), cands[1].ReclaimableTokens) // Pos 7000: the ephemeral block sits at 7000
	require.Equal(t, core.Tokens(120), cands[2].ReclaimableTokens) // Pos 12000
	require.Equal(t, core.Tokens(0), cands[3].ReclaimableTokens)   // Pos 13000: nothing after
}

func TestAssemble_NilGraphReturnsNoCandidates(t *testing.T) {
	t.Parallel()

	segs := newFakeSegmentLog()
	noGraph := newCandidateAssembler(nil, segs, newRecordingLogger(), obs.New(newFakeClock(epoch)))
	require.NotPanics(t, func() {
		cands, err := noGraph.Assemble(context.Background(), fixtureSess, []core.TurnIndex{1, 2}, turnSet(1), nil, 2)
		require.NoError(t, err)
		require.Nil(t, cands)
	})

	noSegs := newCandidateAssembler(newFakeGraph(), nil, newRecordingLogger(), obs.New(newFakeClock(epoch)))
	require.NotPanics(t, func() {
		cands, err := noSegs.Assemble(context.Background(), fixtureSess, []core.TurnIndex{1, 2}, turnSet(1), nil, 2)
		require.NoError(t, err)
		require.Nil(t, cands)
	})
}

func TestAssemble_RecoversCrossingEdgesPanic(t *testing.T) {
	t.Parallel()

	f := newAssemblerFixture(t, fixtureTurns)
	f.graph.panicOn(10*posPerTurn, "index out of range in a broken graph")
	var cands []scheduler.Candidate
	var err error
	require.NotPanics(t, func() {
		cands, err = f.asm.Assemble(context.Background(), fixtureSess, []core.TurnIndex{5, 10, 15}, turnSet(5, 10, 15), nil, fixtureTurns)
	})
	require.NoError(t, err)
	require.Len(t, cands, 2)
	require.Equal(t, core.TurnIndex(5), cands[0].Turn)
	require.Equal(t, core.TurnIndex(15), cands[1].Turn)
	require.Equal(t, 1, f.log.count(logLoud), "one Loud line: %v", f.log.msgs(logLoud))
	require.Equal(t, int64(1), f.reg.Counter("sched.candidate.crossing_panic").Value())
	require.Equal(t, int64(0), f.reg.Counter("sched.candidate.unresolved").Value())
	require.Contains(t, fmt.Sprint(f.log.entries(logLoud)[0].KV), fmt.Sprint(10*posPerTurn))
}
