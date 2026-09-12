package observer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The property side of carried defect SP08-D2. The sweeps in redelivery_sweep_test.go enumerate one
// handler's cut points exhaustively; this generates whole SESSIONS — interleaved reads and
// SubagentStops, random cut points, random redeliveries, random restarts with and without an idle
// Persist — and asserts the invariants that have to hold over the resulting index however the
// events fell.
//
// The invariants are the ones the fix is FOR, stated structurally rather than by example:
//
//  1. A record and the supersede marks it authors are ONE index write. Checked over the file's own
//     line order: scanning back from any mark, across its sibling marks, must reach the record that
//     authored it. A mark separated from its author by another record is the interleaving SP08-D2
//     exists for — a redelivered read re-running supersession behind a stale record line.
//  2. No ordinal inversion: a superseder is always newer in the append-only index than what it
//     supersedes (the e2e x09 flush arm's rule, v3_x09_test.go:358).
//  3. One content record per host event, and never more SubagentStop records than Stop events —
//     x09's count assertion, generalized.
//  4. The subagent window stays well formed: SubagentSince indexes inside ToolUses, and no tool use
//     appears in it twice. Rule 3 of the identity rule deliberately leaves SubagentSince alone
//     while restoring window membership by id on a replay, and this is what checks that the pair of
//     decisions is consistent rather than merely asserting that it is.

// rdxPropPaths and rdxPropBodies are the two paths and two contents the generator draws from. Two
// of each is the smallest set that produces every supersession shape that matters: same path and
// same content (identical-root supersession), same path and different content, and different paths
// (no relation at all).
var (
	rdxPropPaths  = []string{"src/auth.ts", "src/retry.ts"}
	rdxPropBodies = []string{rdxBody, rdxBody + "export const retries = 3\n"}
)

// rdxPropMaxSteps bounds one generated session. Each step opens and may reopen a real store, so the
// bound is what keeps a 100-iteration property run to seconds rather than minutes; four steps is
// already enough for a redelivered read to be followed by two later reads of the same path, which
// is the inverted-mark shape.
const rdxPropMaxSteps = 4

// rdxStep is one generated host event and everything the daemon might do to it.
type rdxStep struct {
	// stop selects a SubagentStop rather than a read.
	stop bool
	// path and content index rdxPropPaths and rdxPropBodies; ignored for a Stop.
	path, content int
	// cut is how many context checks the FIRST run survives before the pre-flush shutdown lands.
	cut int
	// redeliver replays the delivery under its reused lease, which is the at-least-once contract.
	redeliver bool
	// restart is 0 for none, 1 for a fresh process with no persisted state (the shape x09
	// produced), 2 for one that reloaded state/observer.json.
	restart int
}

var rdxStepGen = rapid.Custom(func(rt *rapid.T) rdxStep {
	return rdxStep{
		stop:      rapid.Bool().Draw(rt, "stop"),
		path:      rapid.IntRange(0, len(rdxPropPaths)-1).Draw(rt, "path"),
		content:   rapid.IntRange(0, len(rdxPropBodies)-1).Draw(rt, "content"),
		cut:       rapid.IntRange(0, rdxReadCutPoints).Draw(rt, "cut"),
		redeliver: rapid.Bool().Draw(rt, "redeliver"),
		restart:   rapid.IntRange(0, 2).Draw(rt, "restart"),
	}
})

// rdxPropRig is the property test's own rig.
//
// It does not reuse newRdxRig because rapid.T is not a *testing.T: it has no Helper, no TempDir and
// no Cleanup, so the store is opened and closed explicitly here. rapid.T does satisfy
// require.TestingT (Errorf and FailNow), so the assertions are the same ones everywhere else uses.
type rdxPropRig struct {
	rt    *rapid.T
	root  string
	clock *fakeClock
	o     *observer
	st    store.Store
}

func newRdxPropRig(rt *rapid.T, root string, clock *fakeClock) *rdxPropRig {
	r := &rdxPropRig{rt: rt, root: root, clock: clock}
	r.open()
	return r
}

func (r *rdxPropRig) open() {
	cfg := config.Defaults()
	st, err := store.Open(r.root, cfg, store.Deps{Log: logging.Nop(), Clock: r.clock})
	require.NoError(r.rt, err)
	g, err := dag.Open(r.root, cfg, logging.Nop())
	require.NoError(r.rt, err)
	built, err := New(Options{
		ProjectRoot: r.root, Cfg: cfg, Store: st, Graph: g,
		Log: logging.Nop(), Metrics: obs.New(r.clock), Clock: r.clock,
	})
	require.NoError(r.rt, err)
	impl, ok := built.(*observer)
	require.True(r.rt, ok, "New must return the concrete observer")
	r.o, r.st = impl, st
}

// restart models the flush-time daemon taking over the project, with or without the idle Persist
// having written state/observer.json first.
func (r *rdxPropRig) restart(persist bool) {
	if persist {
		require.NoError(r.rt, r.o.Persist(context.Background()))
	}
	require.NoError(r.rt, r.st.Close())
	r.open()
}

func (r *rdxPropRig) close() { _ = r.st.Close() }

// sidecar is publication order's stage 1, which the daemon runs before EVERY dispatch of a leased
// delivery — the first one and each redelivery alike.
func (r *rdxPropRig) sidecar(arrival uint64, op string) core.ObservationID {
	id, err := core.NewObservationID(testSession, arrival)
	require.NoError(r.rt, err)
	require.NoError(r.rt, store.WriteCaptureSidecar(r.root, store.CaptureSidecar{
		ObservationID: id, Session: testSession, Arrival: arrival, Op: op,
		Fidelity: core.FidelityExact, Outcome: core.OutcomeOK, Bytes: []byte(`{"a":1}`),
	}))
	return id
}

func (r *rdxPropRig) index() []byte {
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(r.root).Index, "tool_use.jsonl")))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(r.rt, err)
	return b
}

// lines parses the index the way the x09 flush arm parses it.
func (r *rdxPropRig) lines() []rdxLine {
	var out []rdxLine
	for _, ln := range bytes.Split(r.index(), []byte{'\n'}) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		var l rdxLine
		require.NoError(r.rt, json.Unmarshal(ln, &l))
		out = append(out, l)
	}
	return out
}

// TestRedelivery_IndexInvariantsHoldOverGeneratedSessions is the property test.
func TestRedelivery_IndexInvariantsHoldOverGeneratedSessions(t *testing.T) {
	base := t.TempDir()
	iter := 0

	rapid.Check(t, func(rt *rapid.T) {
		iter++
		root := filepath.Join(base, fmt.Sprintf("p%05d", iter))
		require.NoError(rt, os.MkdirAll(root, 0o700))

		r := newRdxPropRig(rt, root, newFakeClock())
		defer r.close()

		steps := rapid.SliceOfN(rdxStepGen, 1, rdxPropMaxSteps).Draw(rt, "steps")
		stopEvents := 0

		for i, s := range steps {
			op := rdxOpTool
			if s.stop {
				op = rdxOpStop
				stopEvents++
			}
			arrival := uint64(i + 1)
			obsID := r.sidecar(arrival, op)

			// One host event, run under a context that survives s.cut checks and is cancelled from
			// there on. Both handlers absorb every failure they are allowed to absorb, so the error
			// is deliberately not asserted: what the cut leaves behind is the subject, not what it
			// returned.
			run := func(ctx context.Context) {
				if s.stop {
					_, _ = r.o.OnStop(ctx, stopOf(true), true) //nolint:errcheck // the cut's error is the point
					return
				}
				e := readOf(fmt.Sprintf("toolu_%d", i), rdxPropPaths[s.path], rdxPropBodies[s.content])
				_, _ = r.o.OnToolUse(ctx, e) //nolint:errcheck // the cut's error is the point
			}

			run(WithObservation(rdxCutAfter(s.cut), obsID))
			if s.restart > 0 {
				r.restart(s.restart == 2)
			}
			if s.redeliver {
				r.clock.Advance(time.Second)
				r.sidecar(arrival, op) // the daemon rewrites the sidecar before re-dispatching
				run(WithObservation(context.Background(), obsID))
			}
			r.clock.Advance(time.Second)
		}

		lines := r.lines()

		// (1) A record and its marks are one write.
		for i, l := range lines {
			if l.Op != "supersede" {
				continue
			}
			j := i - 1
			for j >= 0 && lines[j].Op == "supersede" {
				j--
			}
			require.GreaterOrEqual(rt, j, 0,
				"supersede mark %s by %s has no record line before it at all", l.ID, l.By)
			require.Equal(rt, l.By, lines[j].ID,
				"supersede mark %s by %s is separated from its author by record %s: the mark was "+
					"appended by a pass that wrote no record, which is a redelivered read re-running "+
					"supersession", l.ID, l.By, lines[j].ID)
		}

		// (2) No ordinal inversion.
		ord := make(map[string]int, len(lines))
		for i, l := range lines {
			if l.Op != "" {
				continue
			}
			if _, seen := ord[l.ID]; !seen {
				ord[l.ID] = i
			}
		}
		for _, l := range lines {
			if l.Op != "supersede" {
				continue
			}
			older, hasOlder := ord[l.ID]
			newer, hasNewer := ord[l.By]
			require.True(rt, hasOlder && hasNewer,
				"supersede mark %s by %s names a record the index does not hold", l.ID, l.By)
			require.Greater(rt, newer, older,
				"supersede mark %s by %s: the superseder is the OLDER record", l.ID, l.By)
		}

		// (3) One record per host event.
		perID := map[string]int{}
		captures := 0
		for _, l := range lines {
			if l.Op != "" {
				continue
			}
			perID[l.ID]++
			if l.Tool == subagentStop {
				captures++
			}
		}
		for id, n := range perID {
			require.Equal(rt, 1, n, "the index holds %d content records for one id %s", n, id)
		}
		require.LessOrEqual(rt, captures, stopEvents,
			"%d SubagentStop records for %d Stop events: a redelivered Stop was re-captured under a "+
				"fresh id", captures, stopEvents)

		// (4) The subagent window stays well formed.
		st := r.o.session(testSession)
		st.mu.Lock()
		defer st.mu.Unlock()
		require.GreaterOrEqual(rt, st.SubagentSince, 0)
		require.LessOrEqual(rt, st.SubagentSince, len(st.ToolUses),
			"SubagentSince indexes past the end of the window")
		seen := make(map[core.ToolUseID]bool, len(st.ToolUses))
		for _, tu := range st.ToolUses {
			require.False(rt, seen[tu.ID],
				"tool use %s appears twice in the subagent window: a replay appended it a second time "+
					"instead of restoring membership by id", tu.ID)
			seen[tu.ID] = true
		}
	})
}
