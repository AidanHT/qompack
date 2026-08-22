// Package obs provides counters, gauges, fixed-bucket log histograms, and the six §2.4 latency
// budgets (B-A..B-F) that CheckBudgets gates against configuration — never against a literal.
//
// obs is foundation-layer (§3.2): it imports internal/core, internal/paths and internal/config
// only. It does not import internal/logging (the reverse edge, logging -> obs, is the one that
// would create a cycle; see internal/logging's package comment for the AttachLoudObserver seam
// that connects the two from a composition root instead).
//
// The exported surface splits into five parts:
//
//   - hist.go — Histogram, HistSnapshot, and the fixed-bucket log histogram itself: bucketFor and
//     bucketUpper are transcribed verbatim from 00-ARCHITECTURE.md §9, and Observe is lock-free.
//   - counter.go — Counter and Gauge, the two simpler atomic-backed instruments.
//   - budgets.go — BudgetID, Budget and Budgets(): the six §2.4 budgets as data, with every limit
//     read from config.Config at check time.
//   - registry.go — Registry, the lazily-populated instrument set a daemon or hook process shares,
//     plus Snapshot, CheckBudgets, Persist and the package-level Timed helper.
//   - cpu_windows.go / cpu_other.go — ProcessCPU, this process's own user+system CPU clock, read
//     from GetProcessTimes and from getrusage(RUSAGE_SELF). It is the measurand a budget switches
//     to when it must survive a co-loaded host: a starved process waits longer, it does not
//     execute more.
package obs
