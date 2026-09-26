package observer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/store"
)

// Carried defect SP08-D1, the publication half: how many SyncPublication passes one leased capture
// pays. A pass re-reads and verifies every object of the root's recovery closure and fsyncs each of
// them, their directories, both indices and the index directory, so the count is the durability cost
// in a unit host load cannot move. These tests pin it with the store's own counter, never a clock.
//
// A FRESH leased capture needs exactly the two barriers of 00-ARCHITECTURE.md §0.2.2: the root is
// verified and durable before any line names it (the intent, then the record), and the record is
// durable before the capture link is written. The store's observation publication carries both
// (store.DurableObservationPublisher). The w3-e2ereds workstream measured four passes per capture on
// the base: the observer synced before publishRecord, the store synced before and after the index
// write, and finishObservation synced the same root again before the link.
//
// A REDELIVERY keeps its recovery passes: the original publication is re-proven after a restart,
// which these tests pin as unchanged.
//
// The counter name is restated rather than imported, for the reason redelivery_test.go gives: it is
// a contract with an operator, and a test that shares the constant cannot catch a change to it.
const passCounter = "store.publication.sync"

func (r *rdxRig) passes() int64 { return r.counter(passCounter) }

// newPassRig is newRdxRig with the store's metrics wired into the rig's registry, so the store's
// publication counter is readable beside the observer's own.
func newPassRig(t *testing.T) *rdxRig {
	t.Helper()
	return buildPassRig(t, t.TempDir(), func(st store.Store) store.Store { return st })
}

// buildPassRig opens a counting store over root and an observer that writes through wrap(store).
func buildPassRig(t *testing.T, root string, wrap func(store.Store) store.Store) *rdxRig {
	t.Helper()
	clock := newFakeClock()
	metrics := obs.New(clock)
	cfg := config.Defaults()
	st, err := store.Open(root, cfg, store.Deps{Log: logging.Nop(), Clock: clock, Metrics: metrics})
	require.NoError(t, err)
	g, err := dag.Open(root, cfg, logging.Nop())
	require.NoError(t, err)
	built, err := New(Options{
		ProjectRoot: root, Cfg: cfg, Store: wrap(st), Graph: g,
		Log: logging.Nop(), Metrics: metrics, Clock: clock,
	})
	require.NoError(t, err)
	impl, ok := built.(*observer)
	require.True(t, ok, "New must return the concrete observer")
	r := &rdxRig{t: t, root: root, clock: clock, metrics: metrics, o: impl, st: st}
	t.Cleanup(func() { _ = r.st.Close() })
	return r
}

// requirePublished asserts a leased capture's whole publication: the record, the committed
// observation binding, and the capture link naming the record.
func (r *rdxRig) requirePublished(id core.ObservationID, want core.ToolUseID) {
	r.t.Helper()
	ctx := context.Background()
	rec, err := r.st.ToolUse(ctx, want)
	require.NoError(r.t, err)
	lookup, ok := r.st.(store.ObservationReader)
	require.True(r.t, ok)
	bound, err := lookup.ToolUseByObservation(ctx, id)
	require.NoError(r.t, err, "the binding is committed")
	require.Equal(r.t, want, bound.ID)
	sc, err := store.ReadCaptureSidecar(r.root, id)
	require.NoError(r.t, err)
	require.True(r.t, sc.Published, "the capture link is written")
	require.Equal(r.t, want, sc.ToolUseID)
	require.Equal(r.t, rec.Root, sc.Root)
}

// TestPublicationPasses_FreshLeasedToolCaptureSyncsTwice pins the tool path: one pass before the
// intent, one after the index write, whatever the content — a re-read that dedups onto a stored root
// and supersedes the earlier read, and novel content alike. An unleased capture makes none.
func TestPublicationPasses_FreshLeasedToolCaptureSyncsTwice(t *testing.T) {
	r := newPassRig(t)
	ctx := context.Background()

	_, err := r.o.OnToolUse(ctx, readOf("toolu_A", supersedePath, rdxBody))
	require.NoError(t, err)
	require.Zero(t, r.passes(), "an unleased capture has no delivery to publish durably, and syncs nothing")

	for i, c := range []struct {
		name, id, path, body string
	}{
		{"re-read of a stored root that supersedes A", "toolu_B", supersedePath, rdxBody},
		{"novel content", "toolu_C", "src/novel.ts", "export const novel = 'a body nothing has stored'\n"},
	} {
		r.clock.Advance(time.Second)
		obsID := r.sidecar(uint64(i+1), rdxOpTool)
		before := r.passes()
		_, err := r.o.OnToolUse(WithObservation(ctx, obsID), readOf(c.id, c.path, c.body))
		require.NoError(t, err, c.name)
		require.Equal(t, int64(2), r.passes()-before,
			"%s: a fresh leased capture pays the two §0.2.2 barriers, not a pass per layer", c.name)
		r.requirePublished(obsID, core.ToolUseID(c.id))
	}
	a, err := r.st.ToolUse(ctx, "toolu_A")
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("toolu_B"), a.SupersededBy, "the mark landed with B's record")
}

// TestPublicationPasses_FreshLeasedPromptSyncsTwice pins the prompt path's fresh publication.
func TestPublicationPasses_FreshLeasedPromptSyncsTwice(t *testing.T) {
	r := newPassRig(t)
	id := r.sidecar(1, "observe.prompt")
	_, err := r.o.OnUserPrompt(WithObservation(context.Background(), id), promptOf("a prompt to capture"))
	require.NoError(t, err)
	require.Equal(t, int64(2), r.passes(), "a fresh leased prompt pays the two §0.2.2 barriers")
	r.requirePublished(id, VerbatimPromptID(testSession, 0))
}

// TestPublicationPasses_FreshLeasedSubagentStopSyncsTwice pins the SubagentStop path's fresh
// publication.
func TestPublicationPasses_FreshLeasedSubagentStopSyncsTwice(t *testing.T) {
	r := newPassRig(t)
	id := r.sidecar(1, rdxOpStop)
	_, err := r.o.OnStop(WithObservation(context.Background(), id), stopOf(true), true)
	require.NoError(t, err)
	require.Equal(t, int64(2), r.passes(), "a fresh leased subagent capture pays the two §0.2.2 barriers")
	r.requirePublished(id, SubagentCaptureID(testSession, 0))
}

// TestPublicationPasses_RedeliveryReprovesTheOriginalPublication pins the recovery path as
// unchanged: a redelivery of a published capture re-proves the root before the store completes the
// intent, syncs after it, and re-proves again before the link, so it pays three passes — in the same
// process and after a restart.
func TestPublicationPasses_RedeliveryReprovesTheOriginalPublication(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "same process", true: "after a restart"}[restart], func(t *testing.T) {
			r := newPassRig(t)
			ctx := context.Background()
			id := r.sidecar(1, rdxOpTool)
			ev := readOf("toolu_R", supersedePath, rdxBody)
			_, err := r.o.OnToolUse(WithObservation(ctx, id), ev)
			require.NoError(t, err)
			if restart {
				// The rig's restart reopens the store without metrics, so the redelivery below runs on
				// a fresh counting rig over the same project instead.
				require.NoError(t, r.st.Close())
				r = buildPassRig(t, r.root, func(st store.Store) store.Store { return st })
			}
			before := r.passes()
			index := r.index()
			r.clock.Advance(time.Second)
			r.sidecar(1, rdxOpTool)
			_, err = r.o.OnToolUse(WithObservation(ctx, id), ev)
			require.NoError(t, err)
			require.Equal(t, int64(3), r.passes()-before,
				"a redelivery re-proves the original publication: recovery's two passes and the link's one")
			require.Equal(t, index, r.index(), "the redelivery appends nothing")
			require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed))
			r.requirePublished(id, "toolu_R")
		})
	}
}
