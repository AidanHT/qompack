package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
)

// wiredCheckpointOptions wires a project the way runDaemon does up to the checkpoint sources: the
// observer (store, DAG) and the rehydrator (which installs the shared lazy ledger opener).
func wiredCheckpointOptions(t *testing.T, root string, cfg config.Config) (*daemon.Options, checkpointWiring) {
	t.Helper()
	opts := daemon.NewOptions(root, cfg)
	opts.Log = logging.Nop()
	opts.Clock = testClock()
	t.Cleanup(func() {
		if l := opts.LedgerHandle(); l != nil {
			_ = l.Close()
		}
		if opts.Store != nil {
			_ = opts.Store.Close()
		}
	})
	_, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	_ = daemon.WireRehydrator(&opts)
	require.NotNil(t, opts.OpenLedger, "fixture sanity: the lazy opener is installed")
	ckpt := wireCheckpointSources(&opts)
	require.NotNil(t, ckpt.sources)
	return &opts, ckpt
}

// TestProductionCheckpointSourcesOpenTheLedgerOverRecords is D49's restart half of F-C4-C49-3: a
// daemon restarted in a project that already holds an elimination has negative knowledge no open
// ledger serves until a compaction or a ledger tool call. The checkpoint sources open it through
// the shared opener, so the frontier reads the records instead of refusing on every idle tick.
func TestProductionCheckpointSourcesOpenTheLedgerOverRecords(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	cfg := config.Defaults()
	cfg.Eliminations.RequireEvidence = false

	// An earlier daemon's elimination, closed with that daemon.
	prev, err := negknow.Open(root, cfg, nil, negknow.Deps{Session: "sess_prev", Clock: testClock(), Log: logging.Nop()})
	require.NoError(t, err)
	_, err = prev.Record(context.Background(), negknow.Record{
		Target: "src/cache.go", Approach: "drop the cache", Reason: "load-bearing", Scope: negknow.ScopeProject,
	})
	require.NoError(t, err)
	require.NoError(t, prev.Close())

	opts, ckpt := wiredCheckpointOptions(t, root, cfg)

	src, err := ckpt.sources()
	require.NoError(t, err, "a project holding records has its ledger opened for the checkpoint sources")
	require.NotNil(t, src.Ledger)
	require.Same(t, opts.LedgerHandle(), src.Ledger, "through the daemon's shared opener: one handle")
	recs, err := src.Ledger.All(context.Background())
	require.NoError(t, err)
	require.Len(t, recs, 1)
}

// TestProductionCheckpointSourcesOpenNothingWithoutRecords: in a project with no elimination the
// sources still open nothing and report the missing ledger by name, which the frontier admits.
func TestProductionCheckpointSourcesOpenNothingWithoutRecords(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	opts, ckpt := wiredCheckpointOptions(t, root, config.Defaults())

	_, err := ckpt.sources()
	require.ErrorIs(t, err, checkpoint.ErrNoLedger)
	require.Nil(t, opts.LedgerHandle(), "no ledger is opened in a project with no record")
	require.NoFileExists(t, filepath.Join(root, ".qompack", "records", "eliminations.jsonl"))
	require.NoFileExists(t, filepath.Join(root, ".qompack", "sketches", "tried.bloom"))
}

// TestCheckpointWiringOpensNoLedgerBeforeRun pins where recordedLedger's open may happen: on the
// first use of the sources after the daemon is serving (an idle tick, a compaction), never while
// runDaemon is still wiring. The wiring phases run before Run accepts connections, and a hook's
// SessionStart dial waits only hookConnectDeadlineFloor for that accept, so an open there (log
// load, reconcile, bloom rebuild and fsync, refreshAtOpen) would spend the hook's connect budget
// in any project that holds records. The project here holds one; the phase-1 snapshot, the
// scheduler wiring, daemon.New and the idle registration must all leave the ledger unopened, and
// the supplier's first call afterwards is the one that opens it.
func TestCheckpointWiringOpensNoLedgerBeforeRun(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	cfg := config.Defaults()
	cfg.Eliminations.RequireEvidence = false

	prev, err := negknow.Open(root, cfg, nil, negknow.Deps{Session: "sess_prev", Clock: testClock(), Log: logging.Nop()})
	require.NoError(t, err)
	_, err = prev.Record(context.Background(), negknow.Record{
		Target: "src/cache.go", Approach: "drop the cache", Reason: "load-bearing", Scope: negknow.ScopeProject,
	})
	require.NoError(t, err)
	require.NoError(t, prev.Close())

	opts, ckpt := wiredCheckpointOptions(t, root, cfg)
	require.Nil(t, opts.LedgerHandle(), "wireCheckpointSources' phase-1 snapshot must not open the ledger")

	sched, schedOpts := wireScheduler(opts, nil, ckpt.sources)
	t.Cleanup(func() { closeScheduler(sched, schedOpts) })
	d, err := daemon.New(*opts)
	require.NoError(t, err)
	registerCheckpointIdle(d, cfg, ckpt, sched)
	registerSchedulerIdle(d, sched, schedOpts)
	require.Nil(t, opts.LedgerHandle(), "the idle registrations must not open the ledger")

	src, err := ckpt.sources()
	require.NoError(t, err, "the first use after wiring opens the ledger over the records")
	require.Same(t, opts.LedgerHandle(), src.Ledger)
}
