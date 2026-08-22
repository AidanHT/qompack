package obs

import (
	"time"

	"github.com/qompack/qompack/internal/config"
)

// BudgetID names one of the latency budgets this package gates: 00-ARCHITECTURE.md §2.4's six,
// B-A through B-F, plus B-G, which names the one synchronous cost §2.4 leaves unbudgeted.
type BudgetID string

// The budgets. B-A is the design document's own headline number (Qompack.md §8.1); B-B through
// B-F are 00-ARCHITECTURE.md §2.4's daemon-internal and MCP budgets. B-G has no §2.4 row — see
// its own comment for what it names and why it exists.
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
	// BG is hook_degraded: the synchronous spool append a hook pays inside ipc.Client.Send when
	// the daemon cannot take the event (internal/ipc/client.go's appendToSpool, which calls
	// SpoolWriter.Append). It is the only step of Send no deadline governs — §12.3 bounds what
	// happens when that write FAILS, never how long it may take — and B-A cannot cover it: the
	// gated B-A population is the daemon's own TS-anchored hook_controlled series (recvTS-reqTS),
	// which by construction has no sample for a request that never reached the daemon. So the
	// degraded path is exactly the path B-A stops measuring, and until B-G it had no budget of
	// its own.
	BG BudgetID = "B-G"
)

// The histogram names each budget reads from. The first six match 00-ARCHITECTURE.md §2.4's own
// "Clock" column verbatim, so a later subplan recording an observation and this package checking
// it agree on the name without either hardcoding the other's literal. histHookDegraded has no
// §2.4 row to match — B-G is not one of §2.4's budgets — so it follows the same underscore
// spelling and is written by internal/ipc's own degraded path, which reads the name back off
// Budgets() rather than respelling it.
const (
	histHookControlled     = "hook_controlled"
	histL0Ingest           = "l0_ingest"
	histL0Process          = "l0_process"
	histHookWall           = "hook_wall"
	histCheckpointFinalize = "checkpoint_finalize"
	histMCPToolCall        = "mcp_tool_call"
	histHookDegraded       = "hook_degraded"
)

// degradedSpoolBudgetFactor scales B-A's own hot-path budget into B-G's, so that B-G stays
// config-driven like every other gated budget (D11/§11.6) without inventing a second config key
// for a number no operator should have to tune. An operator who tightens runtime.hotPath.budgetMs
// — their statement of how much latency a hook may add to a tool call — tightens the degraded
// ceiling with it.
//
// Why 64x, and why so far above the measurement. What the degraded path actually costs, measured
// on this repo's own Windows host (NTFS) with nothing else running, is one cold create-and-append:
// 0.65-0.94 ms per append in the fastest of six 32-append batches across eight runs, and up to
// 9.28 ms per append in the slowest batch of a run that was otherwise quiet — a 6x swing with the
// product byte-identical. Under `-race` the fastest batch runs 1.81-2.63 ms per append. Under
// co-load, bc44d2a measured this same append at p99 282 ms and max 541 ms.
//
// 64 x B-A's 15 ms default is 960 ms: roughly 1000x the quiet per-append cost, ~100x the worst
// quiet batch, and still comfortably above the worst figure ever recorded for it on a fully
// co-loaded runner. That is deliberate. B-G is not an SLO on filesystem latency — no plugin
// architecture can budget a stranger's disk, which is the same reasoning §2.4 applies to B-D — it
// is a pathology detector: an fsync per byte, a quadratic re-encode, a lock convoy. The gate that
// actually judges this path against its own host is rate-graded rather than wall-clock, and lives
// with the code it measures (internal/ipc/degraded_test.go); this limit is the outer ceiling
// CheckBudgets applies wherever no baseline can be measured.
const degradedSpoolBudgetFactor = 64

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

// Budgets returns the six budgets of 00-ARCHITECTURE.md §2.4 followed by B-G, in B-A..B-G order.
// Every Limit reads its bound from cfg.Runtime at call time, so changing configuration changes
// every gated budget's limit without redeploying this package.
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
		{
			ID: BG, Hist: histHookDegraded, Pct: pctP99, Gated: true,
			// B-G has no config key of its own: it is a multiple of B-A's, so an operator who
			// moves runtime.hotPath.budgetMs moves this with it. See
			// degradedSpoolBudgetFactor for the derivation of the multiple.
			Limit: func(c config.Config) time.Duration {
				return degradedSpoolBudgetFactor *
					time.Duration(c.Runtime.HotPath.BudgetMs) * time.Millisecond
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
