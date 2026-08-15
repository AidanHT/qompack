package eval

import "github.com/qompack/qompack/internal/core"

// SynthSpec parameterizes Synthesize's deterministic synthetic-session generator
// (00-ARCHITECTURE.md §5.18, §6.3). The 24-session synthetic corpus spans read-heavy,
// test-output-heavy, refactor-across-files, long-idle-gap, dependency-change-mid-session,
// subagent-heavy, thrash-loop and multi-compaction specs.
type SynthSpec struct {
	// Turns is the number of turns to generate.
	Turns int
	// ToolMix weights each tool name's probability of being chosen for a given turn.
	ToolMix map[string]float64
	// FileRereadRate is the probability that a file read re-reads a previously read file.
	FileRereadRate float64
	// TestOutputNoise controls how much volatile noise (timestamps, PIDs, …) injected test
	// output carries — the O2 canonicalization case.
	TestOutputNoise float64
	// Changepoints is the number of task-boundary changepoints to inject.
	Changepoints int
	// Eliminations is the number of negative-knowledge elimination events to inject.
	Eliminations int
	// SubagentCalls is the number of subagent invocations to inject.
	SubagentCalls int
	// DependencyChangeAt lists turn indices at which a dependency's content changes, exercising
	// §8.3 staleness.
	DependencyChangeAt []core.TurnIndex
	// CompactionAt lists the turn indices at which the generated session compacts.
	CompactionAt []core.TurnIndex
}

// Synthesize is implemented in synth.go, alongside the generator's own helpers. SynthSpec stays
// here because it is a §5.18 declaration and this is where SP-01 put it.
