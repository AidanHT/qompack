package negknow

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// TestQuery_StaleResponseFollowsTheLiveConfiguration: with Deps.Config set, an open ledger applies
// eliminations.staleResponse as the configuration has it at each query, so the daemon's config
// reload reaches it without a reopen (V6 close-out D49; UAT-09 on candidate 4).
func TestQuery_StaleResponseFollowsTheLiveConfiguration(t *testing.T) {
	root, cfg := newProject(t)
	require.Equal(t, "flag", cfg.Eliminations.StaleResponse)
	var (
		mu   sync.Mutex
		live = cfg
	)
	deps := testDeps("sess", newMetrics())
	deps.Config = func() config.Config {
		mu.Lock()
		defer mu.Unlock()
		return live
	}
	l := openLedger(t, root, cfg, nil, deps)

	const (
		target   = "src/auth.ts:refreshToken"
		approach = "widen pool timeout"
	)
	id := mustRecord(t, l, newRecord("stale-live", target, approach, "pgbouncer ignores it"))
	require.NoError(t, l.MarkStale(context.Background(), []string{id}, []string{"compose changed"}))
	require.Equal(t, AnswerStale, mustQuery(t, l, target, approach, ScopeSession).State, "flag, as opened")

	mu.Lock()
	live.Eliminations.StaleResponse = "drop"
	mu.Unlock()
	require.Equal(t, AnswerUncertain, mustQuery(t, l, target, approach, ScopeSession).State,
		"drop, from the live configuration, without a reopen")

	mu.Lock()
	live.Eliminations.StaleResponse = "flag"
	mu.Unlock()
	require.Equal(t, AnswerStale, mustQuery(t, l, target, approach, ScopeSession).State, "and back")
}
