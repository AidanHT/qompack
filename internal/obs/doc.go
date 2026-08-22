// Package obs provides counters, gauges, fixed-bucket log histograms, and the latency budgets
// (§2.4's B-A..B-F, plus B-G for the degraded spool append §2.4 leaves unbudgeted). Every limit
// is read from configuration — never from a literal — and CheckBudgets evaluates the gated
// subset; B-C, B-D and B-G are reported only, each for its own stated reason.
//
// obs is foundation-layer (§3.2): it imports internal/core, internal/paths and internal/config
// only. It does not import internal/logging (the reverse edge, logging -> obs, is the one that
// would create a cycle; see internal/logging's package comment for the AttachLoudObserver seam
// that connects the two from a composition root instead).
//
// The exported surface splits into four files:
//
//   - hist.go — Histogram, HistSnapshot, and the fixed-bucket log histogram itself: bucketFor and
//     bucketUpper are transcribed verbatim from 00-ARCHITECTURE.md §9, and Observe is lock-free.
//   - counter.go — Counter and Gauge, the two simpler atomic-backed instruments.
//   - budgets.go — BudgetID, Budget and Budgets(): the six §2.4 budgets plus B-G as data, with
//     every limit read from config.Config at check time.
//   - registry.go — Registry, the lazily-populated instrument set a daemon or hook process shares,
//     plus Snapshot, CheckBudgets, Persist and the package-level Timed helper.
package obs
