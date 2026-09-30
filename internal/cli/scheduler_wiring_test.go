package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// This is an unavailable-route assertion on real repository wiring: handed no source supplier,
// wireScheduler invents none, and the frontier is explicitly unavailable rather than backed by a
// stub. The production root now DOES supply one — see the row below, which asserts the same
// unavailable outcome on the real composition path and for a real reason.
func TestWireSchedulerKeepsFrontierUnavailableUntilSharedSources(t *testing.T) {
	root := t.TempDir()
	opts := daemon.NewOptions(root, config.Defaults())
	t.Cleanup(func() {
		if opts.Store != nil {
			_ = opts.Store.Close()
		}
		if opts.Ledger != nil {
			_ = opts.Ledger.Close()
		}
	})
	_, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	sched, schedOpts := wireScheduler(&opts, nil, nil)
	require.NotNil(t, sched, "local scheduling still constructs")
	t.Cleanup(func() { closeScheduler(sched, schedOpts) })
	require.Nil(t, schedOpts.Frontier)
	require.Nil(t, schedOpts.Sources, "wireScheduler must not fabricate a source set of its own")
	require.NoFileExists(t, filepath.Join(root, ".qompack", "records", "eliminations.jsonl"))
}

// TestProductionCheckpointSourcesStayLazyAndReportUnavailable is the composition root's half of
// the same unavailable route, on the wiring runDaemon actually performs.
//
// Three things are pinned together here, and separating any of them would let the other two pass
// for the wrong reason:
//
//  1. The frontier IS wired. schedOpts.Checkpoints and schedOpts.Sources are both non-nil, which
//     is the exact pair NewSchedulerRuntime turns into a live FrontierAdvancer. Before this
//     wiring both were nil and schedRuntime.advancer was nil in every shipped daemon.
//  2. It is wired LAZILY. Composition opens no negative-knowledge ledger: no eliminations.jsonl
//     and no sketches/ exist afterwards, because §3.3 reserves those for the ledger and an eager
//     open would create them in every daemon that never compacts.
//  3. So the route is UNAVAILABLE until something else opens one — and it says so, naming the
//     missing seam and wrapping core.ErrDegraded, rather than returning a set that faults inside
//     Begin several frames later. (Since D49 the frontier port admits this one gap while the
//     project holds no elimination record and advances without negative knowledge; the
//     supplier's answer, pinned here, is unchanged: checkpoint.ErrNoLedger, by name.)
//
// The last leg then opens a ledger the way the first compaction does — by assigning it onto the
// SAME Options — and re-asks the SAME supplier. A supplier that had captured the value rather than
// the field would still answer "unavailable" there, which is the bug this shape exists to prevent.
func TestProductionCheckpointSourcesStayLazyAndReportUnavailable(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	opts := daemon.NewOptions(root, config.Defaults())
	t.Cleanup(func() {
		if opts.Store != nil {
			_ = opts.Store.Close()
		}
		if opts.Ledger != nil {
			_ = opts.Ledger.Close()
		}
	})
	_, err := daemon.WireObserver(&opts)
	require.NoError(t, err)

	ckpt := wireCheckpointSources(&opts)
	require.NotNil(t, ckpt.w, "the checkpoint writer must construct over a healthy project")
	require.NotNil(t, ckpt.sources)

	sched, schedOpts := wireScheduler(&opts, nil, ckpt.sources)
	require.NotNil(t, sched)
	t.Cleanup(func() { closeScheduler(sched, schedOpts) })
	require.NotNil(t, schedOpts.Checkpoints, "the writer is half of the frontier advancer")
	require.NotNil(t, schedOpts.Sources, "the source supplier is the other half")

	// Lazy: nothing here opened a ledger.
	require.NoFileExists(t, filepath.Join(root, ".qompack", "records", "eliminations.jsonl"))
	require.NoFileExists(t, filepath.Join(root, ".qompack", "sketches", "tried.bloom"))

	// Unavailable, with a reason, while it stays unopened.
	partial, srcErr := schedOpts.Sources()
	require.Error(t, srcErr, "a set with no ledger is not a source set")
	require.ErrorIs(t, srcErr, core.ErrDegraded)
	require.Contains(t, srcErr.Error(), "Ledger", "the reason must name the seam that is missing")
	require.NotNil(t, partial.Pins,
		"the partial set still travels, so materialize_pins is not degraded by a seam it never reads")

	// Now open one exactly as the first compaction does: onto the same Options.
	led, ledErr := negknow.Open(root, opts.Cfg, nil, negknow.Deps{
		Store: opts.Store, Graph: opts.Graph, Log: opts.Log, Metrics: opts.Metrics, Clock: opts.Clock,
	})
	require.NoError(t, ledErr)
	opts.Ledger = led

	full, fullErr := schedOpts.Sources()
	require.NoError(t, fullErr, "the supplier reads the FIELD; a captured value would still be nil")
	require.NoError(t, full.Validate())
	require.Same(t, led, full.Ledger)
}

// TestShippedDaemonRegistersTheCheckpointIdleTasks is the production-registration proof.
//
// WireCheckpoint had no caller outside test/e2e, so every checkpoint idle task was dead in the
// shipped binary and no test could tell: a row that calls WireCheckpoint itself passes just as
// happily against a daemon that never calls it. This row therefore reaches the tasks the only way
// a user can — it starts the real `qompack daemon` through Dispatch and asks the running process
// to perform one idle pass over admin.idle, which answers with the names that actually ran.
//
// advance_frontier and materialize_pins are asserted rather than act.checkpoint_cadence because
// the act. prefix is §12.1's mode gate: the cadence is skipped whenever the current mode does not
// MayAct, so requiring it would make this row an assertion about contract mode instead of about
// registration. The two unprefixed names are exactly the ones §12.1 requires to keep running
// regardless, which makes them the right evidence here.
func TestShippedDaemonRegistersTheCheckpointIdleTasks(t *testing.T) {
	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	defer stop()

	ran := wiringIdlePass(t, root)
	require.Contains(t, ran, "advance_frontier",
		"the shipped daemon must register the frontier task; only WireCheckpoint does that")
	require.Contains(t, ran, "materialize_pins",
		"the shipped daemon must register the pin view task")
	require.Contains(t, ran, "drain", "sanity: SP-05's own idle tasks still run")

	// The third registration, asserted mode-independently: whenever the pass was allowed to run
	// ANY act.-prefixed task, the cadence must have been one of them. A pass that ran none is a
	// degraded-passive pass, where §12.1 requires the cadence to be skipped.
	acting := false
	for _, name := range ran {
		if strings.HasPrefix(name, "act.") {
			acting = true
			break
		}
	}
	if acting {
		require.Contains(t, ran, "act.checkpoint_cadence",
			"an acting idle pass must include the cadence; only WireCheckpoint registers it")
	}
}

// wiringIdlePass drives one admin.idle round trip against the daemon at root and returns the task
// names that ran. A task that returned an error still ran, which is what makes this usable as a
// registration probe on a daemon whose ledger no compaction has opened yet.
func wiringIdlePass(t *testing.T, root string) []string {
	t.Helper()

	addr, err := ipc.Resolve(root)
	require.NoError(t, err, "ipc.Resolve(%s)", root)
	client := ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), obs.New(testClock()),
		ipc.ClientOptions{
			ProjectRoot:     root,
			ConnectDeadline: bootstrapCallDeadline,
			AckDeadline:     bootstrapCallDeadline,
			Clock:           testClock(),
		})
	defer func() { _ = client.Close() }()

	resp, err := client.Send(context.Background(),
		ipc.Request{Op: ipc.OpAdminIdle, Reply: true}, bootstrapCallDeadline)
	require.NoError(t, err, "the transport must never error a caller")
	require.True(t, resp.OK, "admin.idle must have routed; err=%q", resp.Err)

	var body struct {
		Ran []string `json:"ran"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &body), "decoding admin.idle from %s", resp.Data)
	return body.Ran
}
