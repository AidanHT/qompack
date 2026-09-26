package observer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The SP08-D2 sweeps (redelivery_sweep_test.go) cut a first run after a FIXED number of context
// checks, rdxReadCutPoints (8) and rdxStopCutPoints (7), on the premise that those are "comfortably
// above the number of context checks either path makes". They are not: a complete fresh leased read
// makes 14 checks now and made 22 before SP08-D1's pass removal, and a subagent capture 15 and 23.
// Cut after at most 8 (read) or 7 (stop) checks, the first run never reaches the record write, so
// the record-written cut, the store's post-write pass and the stages after the capture link were
// never cut by them.
//
// These sweeps count the checks of a complete first run first and then cut after every one of them,
// so the range cannot fall behind the path again. On the read path the checks are: the handler
// entry, the recovery lookup, the put, the intent input check, the store's pass before the intent
// (four), the record write, the store's pass after it (four) and the file-version append after the
// link. The cut between the store's commit and the capture link has no context check in it; it is
// TestDerivedPublication_V6_IndexBeforeLinkCutDuplicatesAcrossRestart's. The assertions are the
// SP08-D2 sweeps' own, plus the whole publication (record, committed binding, link) after the
// redelivery.

// ctxCounter is a context whose Err never fires and counts how often it is asked.
type ctxCounter struct {
	context.Context

	mu sync.Mutex
	n  int
}

func (c *ctxCounter) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return nil
}

func (c *ctxCounter) checks() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// sweepReadFixture records A, the earlier read B supersedes, and returns B's event.
func sweepReadFixture(t *testing.T, r *rdxRig) Event {
	t.Helper()
	_, err := r.o.OnToolUse(context.Background(), readOf("toolu_A", supersedePath, rdxBody))
	require.NoError(t, err)
	r.clock.Advance(time.Second)
	return readOf("toolu_B", supersedePath, rdxBody)
}

// TestPublicationPasses_ReadCutAtEveryCheckOfACompleteRun cuts a fresh leased read that supersedes
// an earlier one after every context check it makes, in each redelivering process state, and
// requires the redelivery to publish it exactly once with its mark.
func TestPublicationPasses_ReadCutAtEveryCheckOfACompleteRun(t *testing.T) {
	probe := newRdxRig(t)
	b := sweepReadFixture(t, probe)
	counted := &ctxCounter{Context: context.Background()}
	_, err := probe.o.OnToolUse(WithObservation(counted, probe.sidecar(2, rdxOpTool)), b)
	require.NoError(t, err)
	checks := counted.checks()
	t.Logf("a complete fresh leased read makes %d context checks", checks)

	for _, proc := range rdxProcessStates {
		for cut := 0; cut <= checks; cut++ {
			t.Run(fmt.Sprintf("%s/cut after %d of %d checks", proc.name, cut, checks), func(t *testing.T) {
				r := newRdxRig(t)
				ctx := context.Background()
				b := sweepReadFixture(t, r)
				obsB := r.sidecar(2, rdxOpTool)
				_, firstErr := r.o.OnToolUse(WithObservation(rdxCutAfter(cut), obsB), b)
				if cut == checks {
					require.NoError(t, firstErr, "fixture: the last cut point is a complete first run")
				}
				afterCut := r.index()
				if !rdxHasRecord(t, afterCut, "toolu_B") {
					require.Zero(t, rdxMarksAuthoredBy(t, afterCut, "toolu_B"),
						"a cut that left no B record must have left no mark authored by B either")
				}
				if proc.restart {
					r.restart(proc.persist)
				}
				r.clock.Advance(time.Second)
				r.sidecar(2, rdxOpTool)
				_, err := r.o.OnToolUse(WithObservation(ctx, obsB), b)
				require.NoError(t, err)

				final := r.index()
				n := 0
				for _, id := range rdxIDs(t, final, "") {
					if id == "toolu_B" {
						n++
					}
				}
				require.Equal(t, 1, n, "exactly one record for one host tool_use_id")
				rdxAssertMarksAreSound(t, final, rdxAppended(t, afterCut, final))
				require.Equal(t, 1, rdxMarksAuthoredBy(t, final, "toolu_B"), "B's mark on A lands exactly once")
				r.requirePublished(obsB, "toolu_B")
			})
		}
	}
}

// TestPublicationPasses_StopCutAtEveryCheckOfACompleteRun is the same sweep over a leased
// SubagentStop capture.
func TestPublicationPasses_StopCutAtEveryCheckOfACompleteRun(t *testing.T) {
	probe := newRdxRig(t)
	counted := &ctxCounter{Context: context.Background()}
	_, err := probe.o.OnStop(WithObservation(counted, probe.sidecar(1, rdxOpStop)), stopOf(true), true)
	require.NoError(t, err)
	checks := counted.checks()
	t.Logf("a complete fresh leased subagent capture makes %d context checks", checks)

	for _, proc := range rdxProcessStates {
		for cut := 0; cut <= checks; cut++ {
			t.Run(fmt.Sprintf("%s/cut after %d of %d checks", proc.name, cut, checks), func(t *testing.T) {
				r := newRdxRig(t)
				ctx := context.Background()
				obs := r.sidecar(1, rdxOpStop)
				_, firstErr := r.o.OnStop(WithObservation(rdxCutAfter(cut), obs), stopOf(true), true)
				if cut == checks {
					require.NoError(t, firstErr, "fixture: the last cut point is a complete first run")
				}
				afterCut := r.index()
				if proc.restart {
					r.restart(proc.persist)
				}
				r.clock.Advance(time.Second)
				r.sidecar(1, rdxOpStop)
				_, err := r.o.OnStop(WithObservation(ctx, obs), stopOf(true), true)
				require.NoError(t, err)

				final := r.index()
				ids := rdxIDs(t, final, subagentStop)
				require.Len(t, ids, 1, "one SubagentStop record per Stop event, whatever the cut")
				rdxAssertMarksAreSound(t, final, rdxAppended(t, afterCut, final))
				r.requirePublished(obs, SubagentCaptureID(testSession, 0))
			})
		}
	}
}
