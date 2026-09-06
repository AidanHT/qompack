package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/store"
)

// The SP-12 daemon-side benchmarks. Each names its budget from the plan's benchmark table; the
// numbers here are shape/regression checks — the gate is devtool bench-compare against
// testdata/bench-baseline.txt. Later SP-12 seats append their benchmarks to this file.

// benchLCG is a tiny deterministic generator so fixtures are identical across runs.
type benchLCG uint64

func (l *benchLCG) next() uint64 {
	*l = *l*6364136223846793005 + 1442695040888963407
	return uint64(*l >> 33)
}

// BenchmarkFeaturesFrom: two 8-turn windows of 12 paths each, one ~120-byte preview per turn.
// Budget: ≤ 100 µs/op (runs on the async B-C path, 50 ms).
func BenchmarkFeaturesFrom(b *testing.B) {
	const (
		pathsPerTurn = 12
		ring         = 64 // distinct pre-built observations cycled through the timed loop
	)
	type obs struct {
		sig  observer.Signals
		tool string
		text string
	}
	ringObs := make([]obs, ring)
	for i := range ringObs {
		ps := make([]string, pathsPerTurn)
		for k := range ps {
			ps[k] = fmt.Sprintf("src/pkg%d/file%d.go", (i+k)%7, k)
		}
		ringObs[i] = obs{
			sig:  observer.Signals{Paths: ps, TestPassed: i%3 == 0},
			tool: []string{"Bash", "Read", "Edit"}[i%3],
			text: fmt.Sprintf("go test ./internal/pkg%d/... -run TestThing%d -count=1 -race -v -timeout 30s", i%7, i),
		}
	}
	h := NewFeatureHistory(defaultFeatureWindow)
	obsAt := func(i int) {
		o := ringObs[i%ring]
		observeText(h, o.text)
		FeaturesFrom(h, o.sig, o.tool, core.UnixMilli(i)*1_000)
	}
	for i := range 2 * defaultFeatureWindow {
		obsAt(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		obsAt(2*defaultFeatureWindow + i)
	}
}

// BenchmarkReclaimableIndexBuild_5000Blocks: one build of the suffix-sum index over 5 000
// unsorted blocks. Budget: ≤ 3 ms/op.
func BenchmarkReclaimableIndexBuild_5000Blocks(b *testing.B) {
	const n = 5_000
	rng := benchLCG(42)
	blocks := make([]dropBlock, n)
	for i := range blocks {
		blocks[i] = dropBlock{
			Pos:    int(rng.next() % 200_000),
			Tokens: core.Tokens(rng.next() % 2_000),
			Class:  DropClass(rng.next() % 4),
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := newReclaimableIndex(blocks)
		if idx.After(0) != idx.total {
			b.Fatal("index self-check failed")
		}
	}
}

// BenchmarkAssembleCandidates_2000ToolUses: 2 000 tool-use records classified into the
// reclaimable index, 40 changepoint turns, a 5 000-node graph over 500 turns and 100 segments.
// Budget: ≤ 20 ms/op cold (turn→Pos map rebuilt every call), ≤ 200 µs/op warm (map reused).
// CrossingEdges is uncached and paid on both paths.
func BenchmarkAssembleCandidates_2000ToolUses(b *testing.B) {
	const (
		turns        = 500
		nodesPerTurn = 10
		toolUses     = 2_000
		cpCount      = 40
		sess         = core.SessionID("bench-sess")
	)
	rng := benchLCG(7)
	graph := newFakeGraph()
	for turn := 1; turn <= turns; turn++ {
		for k := range nodesPerTurn {
			if err := graph.AddNode(dag.Node{
				ID:   dag.NodeID(fmt.Sprintf("tooluse:t%d-n%d", turn, k)),
				Kind: dag.KindToolUse,
				Turn: core.TurnIndex(turn),
				Pos:  turn*posPerTurn + k*(posPerTurn/nodesPerTurn),
			}); err != nil {
				b.Fatal(err)
			}
		}
	}
	segs := newFakeSegmentLog()
	for start := 1; start <= turns; start += segTurns {
		segs.addSegment(b, sess, core.TurnIndex(start), core.TurnIndex(start+segTurns-1), segTurns*posPerTurn)
	}
	blocks := make([]dropBlock, 0, toolUses)
	tools := []string{"Read", "Bash", "Grep", "Task", "mcp__qompack__recall", "Edit"}
	for i := range toolUses {
		rec := store.ToolUseRecord{
			ID:        core.ToolUseID(fmt.Sprintf("toolu_%d", i)),
			Tool:      tools[int(rng.next())%len(tools)],
			Tokens:    core.Tokens(rng.next() % 1_500),
			Ephemeral: rng.next()%10 == 0,
		}
		if rng.next()%8 == 0 {
			rec.Status = store.StatusSuperseded
		}
		blocks = append(blocks, dropBlock{Pos: int(rng.next() % (turns * posPerTurn)), Tokens: rec.Tokens, Class: ClassifyDrop(rec)})
	}
	idx := newReclaimableIndex(blocks)
	cp := make([]core.TurnIndex, 0, cpCount)
	rounds := make(map[core.TurnIndex]struct{}, cpCount)
	for i := range cpCount {
		t := core.TurnIndex(1 + i*(turns/cpCount))
		cp = append(cp, t)
		if i%2 == 0 {
			rounds[t] = struct{}{}
		}
	}
	ctx := context.Background()
	reg := obs.New(core.SystemClock())

	b.Run("cold", func(b *testing.B) {
		asm := newCandidateAssembler(graph, segs, logging.Nop(), reg)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			asm.Invalidate()
			cands, err := asm.Assemble(ctx, sess, cp, rounds, idx, turns)
			if err != nil || len(cands) != maxAssembledCandidates {
				b.Fatalf("cold: %d candidates, err %v", len(cands), err)
			}
		}
	})
	b.Run("warm", func(b *testing.B) {
		asm := newCandidateAssembler(graph, segs, logging.Nop(), reg)
		if _, err := asm.Assemble(ctx, sess, cp, rounds, idx, turns); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cands, err := asm.Assemble(ctx, sess, cp, rounds, idx, turns)
			if err != nil || len(cands) != maxAssembledCandidates {
				b.Fatalf("warm: %d candidates, err %v", len(cands), err)
			}
		}
	})
}
