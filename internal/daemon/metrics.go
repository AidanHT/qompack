package daemon

import "github.com/qompack/qompack/internal/obs"

// The obs.Counter names this package's own components increment (repo underscore idiom, not
// §2.4's dotted prose spelling). Collected in one place because ingest.go and drain.go both need
// them.
const (
	counterL0RingFull     = "l0_ring_full"
	counterL0WorkerPanic  = "l0_worker_panic"
	counterDrainFileError = "drain_file_error"
)

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
