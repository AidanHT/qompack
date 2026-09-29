package rehydrate

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
)

// TestEliminations_ReadForTheSessionBeingRehydrated: the daemon's ledger serves every session of
// the project, so item 3 must ask it for the session being rehydrated. Without naming the session
// on the call, the ledger answered for "no session", and a session-scoped elimination the session
// itself recorded was missing from its own block (V6 live lane, retrieval D2).
func TestEliminations_ReadForTheSessionBeingRehydrated(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	cfg := config.Defaults()
	cfg.Eliminations.RequireEvidence = false
	led, err := negknow.Open(root, cfg, nil, negknow.Deps{Log: logging.Nop()}) // the daemon's shape
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	const own = core.SessionID("sess_rehydrated")
	_, err = led.Record(negknow.WithCaller(bg(), negknow.Caller{Session: own}), negknow.Record{
		Target: "src/pool.go:DialPool", Approach: "widen pool timeout", Reason: "max_idle caps it",
		Scope: negknow.ScopeSession,
	})
	require.NoError(t, err)

	cp := ckEmpty()
	cp.Session = own
	d := depsWith(&spyLogger{})
	d.Ledger = led

	got := buildEliminations(bg(), requestFor(t, cp, generousTestBudget), d, nil)
	require.Equal(t, 1, got.seen, "the session's own session-scoped elimination belongs in its block")

	other := ckEmpty()
	other.Session = "sess_other"
	got = buildEliminations(bg(), requestFor(t, other, generousTestBudget), d, nil)
	require.Zero(t, got.seen, "another session's session-scoped elimination does not")
}
