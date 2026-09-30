package mcp

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/negknow"
)

// A configuration the daemon reloads reaches the next tool call (V6 close-out D49). UAT-09 on
// candidate 4 changed eliminations.staleResponse mid-session; the daemon logged "config reloaded
// changed=[eliminations.staleResponse]" and already_tried kept answering the flag form, because the
// handlers held the configuration they were registered with. ToolDeps.CfgFn is read on every call.

// liveCfg is a configuration a test changes between calls, the way the daemon's reload does.
type liveCfg struct {
	mu  sync.Mutex
	cfg config.Config
}

func (l *liveCfg) get() config.Config {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cfg
}

func (l *liveCfg) set(fn func(*config.Config)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fn(&l.cfg)
}

// TestToolsReadTheLiveConfigurationOnEveryCall answers the same stale record under flag, then under
// drop after the configuration changed, then under flag again, with no re-registration between.
func TestToolsReadTheLiveConfigurationOnEveryCall(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) { c.Eliminations.StaleResponse = "flag" }))
	id, _ := f.seedElimination(t, negknow.ScopeSession)
	rec, err := f.Ledger.Get(t.Context(), id)
	require.NoError(t, err)
	// A ledger that answers stale whatever its own configuration says, so the answer below is the
	// handler's reading of eliminations.staleResponse and nothing else.
	spy := f.withSpyLedger(t)
	spy.Answer = &negknow.Answer{State: negknow.AnswerStale, Record: &rec, Note: negknow.StaleNote}

	live := &liveCfg{cfg: f.Cfg}
	f.Deps.CfgFn = live.get
	f.rewire(t)
	args := map[string]any{"target": elimTarget, "approach": elimApproach}

	var first AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, args, &first)
	require.Equal(t, stateStale, first.State, "flag: the stale record is shown with its note")

	live.set(func(c *config.Config) { c.Eliminations.StaleResponse = "drop" })
	var second AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, args, &second)
	require.Equal(t, stateUncertain, second.State, "drop, reloaded: the next call drops the stale detail")

	live.set(func(c *config.Config) { c.Eliminations.StaleResponse = "flag" })
	var third AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, args, &third)
	require.Equal(t, stateStale, third.State, "and back again, with no restart")
}
