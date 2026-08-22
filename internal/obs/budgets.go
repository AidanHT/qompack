package obs

import (
	"time"

	"github.com/qompack/qompack/internal/config"
)

// BudgetID names one of the latency budgets this package describes: 00-ARCHITECTURE.md §2.4's
// six, B-A through B-F, plus B-G, which names the one synchronous cost §2.4 leaves unbudgeted.
// Naming a budget is not the same as gating it — see Budget.Gated.
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
	// the daemon cannot take the event. Reported only, never gated — for a structural reason,
	// not a soft one; see its Budgets() entry.
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

// The two percentiles a budget may be gated on.
const (
	pctP95 = 95
	pctP99 = 99
)

// Budget describes one latency budget as data: which histogram it reads, which percentile it
// gates on, whether it is gated at all (B-C is soft and B-D is reported-only per §2.4; B-G is
// reported-only because nothing in the product can currently evaluate it), and how to compute its
// limit from configuration. Limit is a function, never a stored duration, so that D11/§11.6 holds
// even for the budget table itself: nothing here duplicates a config default as a literal.
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
			ID: BG, Hist: histHookDegraded, Pct: pctP99, Gated: false,
			// What B-G names: the synchronous spool append a hook pays inside ipc.Client.Send
			// when the daemon cannot take the event (internal/ipc/client.go's appendToSpool,
			// which calls SpoolWriter.Append). It is the only step of Send no deadline governs —
			// §12.3 bounds what happens when that write FAILS, never how long it may take — and
			// B-A cannot cover it: B-A's population is the daemon's own TS-anchored
			// hook_controlled series (recvTS-reqTS), which by construction has no sample for a
			// request that never reached the daemon. The degraded path is exactly the path B-A
			// stops measuring.
			//
			// Why it is reported only, and why that is structural rather than soft. B-C and B-D
			// are ungated because §2.4 says so. B-G is ungated because no evaluator can currently
			// see it: CheckBudgets' only production caller is the resident daemon's own Registry,
			// hook_degraded is written only by a hook process's per-process Registry (which
			// internal/cli's newHookMetrics never Persists), and a B-G sample exists ONLY when the
			// daemon is unreachable. A sample and an evaluator can therefore never coexist.
			// Declaring Gated:true would be a claim nothing backs. What enforces this budget today
			// is the rate-graded test gate that lives with the code it measures,
			// internal/ipc/degraded_test.go; carrying the observation to something that can judge
			// it in production is observer-wave work (plans/V3-VERIFY-observer-and-negative-
			// knowledge.md), not this package's to invent.
			//
			// The limit is its own config key, runtime.budgets.hookDegradedMs, rather than a
			// multiple of another budget's. Riding runtime.hotPath.budgetMs was the first shape
			// this took and it was wrong twice over: 64x is a quotient chosen to reach an absolute
			// target through someone else's key, not a derivation, and an operator tightening
			// their hot-path tolerance would silently tighten a filesystem bound with it — at
			// budgetMs=1, which test/guards' own end-to-end config test sets, the degraded ceiling
			// would have landed at 64 ms, below figures this very append has already been measured
			// at on a loaded runner.
			//
			// Why the 1000 ms default. What the degraded path costs, measured on this repo's
			// Windows host (NTFS) with nothing else running, is one cold create-and-append:
			// 0.75-1.92 ms per append across 200 batches, 1.07-1.27 ms under `-race`, 16-18 us on
			// Linux tmpfs. bc44d2a measured the same call at p99 282 ms and max 541 ms on a
			// co-loaded runner, and a busy loop on this host drove it to 26 ms per append. One
			// second is roughly 500x the quiet figure and near twice the worst ever recorded for
			// it anywhere, which is the intent: B-G is not an SLO on filesystem latency — no plugin
			// architecture can budget a stranger's disk, the same reasoning §2.4 applies to B-D —
			// it is the number a reader compares a reported p99 against to decide whether what they
			// are looking at is a slow disk or a broken one.
			Limit: func(c config.Config) time.Duration {
				return time.Duration(c.Runtime.Budgets.HookDegradedMs) * time.Millisecond
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
