package obs

import (
	"time"

	"github.com/qompack/qompack/internal/config"
)

// BudgetID names one of the six 00-ARCHITECTURE.md §2.4 latency budgets.
type BudgetID string

// The six budgets. B-A is the design document's own headline number (Qompack.md §8.1); B-B
// through B-F are 00-ARCHITECTURE.md §2.4's daemon-internal and MCP budgets.
const (
	// BA is hook_controlled: client main() entry to exit (connect + write + ACK).
	BA BudgetID = "B-A"
	// BB is l0_ingest: daemon read to WAL append returned.
	BB BudgetID = "B-B"
	// BC is l0_process: WAL to fully chunked, stored, DAG/sketches updated (async, soft).
	BC BudgetID = "B-C"
	// BD is hook_wall: includes host process creation. Reported only, never gated.
	BD BudgetID = "B-D"
	// BE is checkpoint_finalize: PreCompact entry to exit.
	BE BudgetID = "B-E"
	// BF is mcp_tool_call: request to response.
	BF BudgetID = "B-F"
)

// The histogram names each budget reads from. These match 00-ARCHITECTURE.md §2.4's own "Clock"
// column verbatim, so a later subplan recording an observation and this package checking it agree
// on the name without either hardcoding the other's literal.
const (
	histHookControlled     = "hook_controlled"
	histL0Ingest           = "l0_ingest"
	histL0Process          = "l0_process"
	histHookWall           = "hook_wall"
	histCheckpointFinalize = "checkpoint_finalize"
	histMCPToolCall        = "mcp_tool_call"
)

// The two percentiles a budget may be gated on.
const (
	pctP95 = 95
	pctP99 = 99
)

// Budget describes one latency budget as data: which histogram it reads, which percentile it
// gates on, whether it is gated at all (B-C is soft and B-D is reported-only per §2.4), and how to
// compute its limit from configuration. Limit is a function, never a stored duration, so that
// D11/§11.6 holds even for the budget table itself: nothing here duplicates a config default as a
// literal.
type Budget struct {
	ID    BudgetID
	Hist  string
	Pct   int
	Gated bool
	Limit func(config.Config) time.Duration
}

// Budgets returns the six budgets of 00-ARCHITECTURE.md §2.4, in B-A..B-F order. Every Limit reads
// its bound from cfg.Runtime at call time, so changing configuration changes every gated budget's
// limit without redeploying this package.
func Budgets() []Budget {
	return []Budget{
		{
			ID: BA, Hist: histHookControlled, Pct: pctP99, Gated: true,
			Limit: func(c config.Config) time.Duration {
				return time.Duration(c.Runtime.HotPath.BudgetMs) * time.Millisecond
			},
		},
		{
			ID: BB, Hist: histL0Ingest, Pct: pctP99, Gated: true,
			Limit: func(c config.Config) time.Duration {
				return time.Duration(c.Runtime.Budgets.L0IngestMs) * time.Millisecond
			},
		},
		{
			ID: BC, Hist: histL0Process, Pct: pctP99, Gated: false,
			Limit: func(c config.Config) time.Duration {
				return time.Duration(c.Runtime.Budgets.L0ProcessMs) * time.Millisecond
			},
		},
		{
			ID: BD, Hist: histHookWall, Pct: pctP99, Gated: false,
			// B-D has no config key by design (00-ARCHITECTURE.md §2.4, config/runtime.go's
			// BudgetsCfg doc comment): it reports the host's process-creation cost, which no
			// plugin architecture can budget away.
			Limit: func(config.Config) time.Duration { return 0 },
		},
		{
			ID: BE, Hist: histCheckpointFinalize, Pct: pctP99, Gated: true,
			Limit: func(c config.Config) time.Duration {
				return time.Duration(c.Runtime.Budgets.CheckpointFinalizeMs) * time.Millisecond
			},
		},
		{
			ID: BF, Hist: histMCPToolCall, Pct: pctP95, Gated: true,
			Limit: func(c config.Config) time.Duration {
				return time.Duration(c.Runtime.Budgets.MCPToolCallMs) * time.Millisecond
			},
		},
	}
}

// percentileOfSnapshot picks the HistSnapshot field matching pct (95 or 99); anything else falls
// back to P99, since every current Budget uses one of those two.
func percentileOfSnapshot(snap HistSnapshot, pct int) time.Duration {
	switch pct {
	case pctP95:
		return snap.P95
	case pctP99:
		return snap.P99
	default:
		return snap.P99
	}
}
