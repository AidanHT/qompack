package daemon

import (
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The obs.Counter names this package's own components increment (repo underscore idiom, not
// §2.4's dotted prose spelling). Collected in one place because ingest.go and drain.go both need
// them. handlers.go and idle.go declare their own additions alongside their own call sites, but
// this file remains the one every counter name is at least reachable from via histName/grep.
const (
	counterL0RingFull     = "l0_ring_full"
	counterL0WorkerPanic  = "l0_worker_panic"
	counterDrainFileError = "drain_file_error"
)

// writeMetricsSnapshot persists the metrics registry's current Snapshot to
// .qompack/metrics/latency.json via the shipped obs.Registry.Persist — the ONE writer of that
// file (ruling #20). A prior revision of this function wrote a second, differently-shaped
// "writeLatencyJSON" to the same path; two writers racing on one file with two different JSON
// schemas made metrics/latency.json's shape nondeterministic for SP-14/`/qompack:status` to parse
// (fix round 1, I-1) — deleted rather than kept alongside Persist.
func writeMetricsSnapshot(root string, m obs.Registry) error {
	if m == nil {
		return nil
	}
	return m.Persist(paths.Of(root))
}

// loudTail returns the last n non-empty Loud messages, via the shipped logging.LastLoud() ring
// (§5.17 "the last five Loud messages") — never by tailing LOUD.log itself. root is accepted to
// match task-5-spec.md's signature (a future caller may want a project-scoped ring); the shared
// ring is process-wide today, so it is unused.
func loudTail(root string, n int) []string {
	_ = root
	all := logging.LastLoud()
	if n <= 0 || len(all) <= n {
		return all
	}
	return append([]string(nil), all[len(all)-n:]...)
}

// histName looks up the histogram name obs.Budgets() associates with id, so callers never
// hardcode "l0_ingest"/"l0_process" as string literals — the budget table is the single source of
// truth for which histogram a given budget reads. An id Budgets() does not recognize (should never
// happen for the two IDs this package uses) reports "", which obs.Registry.Hist treats as its own
// distinct, harmless series rather than panicking.
func histName(id obs.BudgetID) string {
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b.Hist
		}
	}
	return ""
}
