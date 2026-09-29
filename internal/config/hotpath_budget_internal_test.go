package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHotPathBudgetWarnings_NoneForAnyPlatformDefault is D43's "no WARN for the defaults on every
// platform", checked from one host: each platform's shipped pair (l0IngestMs and the budgetMs D41
// derives from it) is fed to the check Load runs, and none of them may warn. Load itself is only
// ever run with this host's defaults (TestLoad_HotPathBudgetBelowIngestBudgetWarns), so this is
// where the other two platforms' defaults are covered.
func TestHotPathBudgetWarnings_NoneForAnyPlatformDefault(t *testing.T) {
	for _, c := range []struct {
		goos   string
		ingest int
	}{
		{"linux_portable", L0IngestMsPortable},
		{goosWindows, L0IngestMsWindows},
		{goosDarwin, L0IngestMsDarwin},
	} {
		t.Run(c.goos, func(t *testing.T) {
			cfg := Defaults()
			cfg.Runtime.Budgets.L0IngestMs = c.ingest
			cfg.Runtime.HotPath.BudgetMs = HotPathBudgetMsFor(c.ingest)
			require.Empty(t, hotPathBudgetWarnings(cfg, Provenance{}),
				"%s's defaults (budgetMs %d, l0IngestMs %d) must not warn", c.goos,
				cfg.Runtime.HotPath.BudgetMs, c.ingest)

			// The same pair one millisecond tighter does: the check is live on every platform's
			// numbers, not vacuously empty.
			cfg.Runtime.HotPath.BudgetMs = c.ingest - 1
			ws := hotPathBudgetWarnings(cfg, Provenance{})
			require.Len(t, ws, 1, "%s: budgetMs %d below l0IngestMs %d must warn", c.goos,
				cfg.Runtime.HotPath.BudgetMs, c.ingest)
			require.Equal(t, hotPathBudgetKey, ws[0].Key)
		})
	}
}
