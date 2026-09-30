package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
)

// TestDaemonLedgerAnswersAForkWithItsParentsEliminations is the wiring half of D49's fork finding
// (F-C4-UAT06-1): the daemon's one ledger — the one already_tried and record_eliminated are served
// from — reads the lineage records, so a fork's already_tried sees its parent's session-scoped
// elimination from before the fork, attributed to the parent, and a sibling session does not.
func TestDaemonLedgerAnswersAForkWithItsParentsEliminations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	cfg := config.Defaults()
	cfg.Eliminations.RequireEvidence = false
	opts := daemon.NewOptions(root, cfg)
	opts.Log = logging.Nop()
	opts.Clock = testClock()
	_ = daemon.WireRehydrator(&opts)
	led := openingLedger(&opts)()
	require.NotNil(t, led)
	t.Cleanup(func() { _ = led.Close() })

	const (
		parent  core.SessionID = "sess-parent"
		fork    core.SessionID = "sess-fork"
		sibling core.SessionID = "sess-sibling"
		forkAt  core.UnixMilli = 2_000_000_000_000
	)
	ctx := negknow.WithCaller(context.Background(), negknow.Caller{Session: parent, Turn: 2})
	id, err := led.Record(ctx, negknow.Record{
		Target: "per-client rate limit", Approach: "100 requests per minute", Reason: "superseded",
		TS: forkAt - 1,
	})
	require.NoError(t, err)

	// The lineage record NoteFork writes when the fork starts (checkpoint.Lineage, v1).
	lin, err := json.Marshal(map[string]any{
		"v": 1, "session": fork, "source": "fork", "parent_session": parent, "origin_session": parent, "at": forkAt,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".qompack", "state", "lineage-"+string(fork)+".json"), lin, 0o600))

	ask := func(s core.SessionID) negknow.Answer {
		a, qerr := led.Query(negknow.WithCaller(context.Background(), negknow.Caller{Session: s, Turn: 9}),
			"per-client rate limit", "100 requests per minute", negknow.ScopeSession)
		require.NoError(t, qerr)
		return a
	}
	got := ask(fork)
	require.Equal(t, negknow.AnswerActive, got.State, "the fork's already_tried sees its parent's elimination")
	require.Equal(t, id, got.Record.ID)
	require.Equal(t, parent, got.Record.Session, "attributed to the parent session")
	require.Equal(t, negknow.AnswerAbsent, ask(sibling).State, "a sibling session does not")
}
