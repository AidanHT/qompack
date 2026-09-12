package observer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/store"
)

// The cancellation sweep for carried defect SP08-D2's SECOND site: a record whose supersede marks
// never landed, and the redelivery that then appends those marks alone.
//
// The window is the pre-flush shutdown cancelling an observe.* between the observer's append and
// commitDelivery. Cancellation is MONOTONIC — once ctx.Err() answers non-nil it never answers nil
// again — so one handler's durable effect is always a PREFIX of its context-checked writes, and the
// interleavings of a single handler are therefore enumerable rather than merely samplable. That is
// what these tests do: cut the first run after every number of context checks it can survive, then
// redeliver under the same lease and assert the index.
//
// The assertions are the e2e x09 flush arm's own rules, restated over the lines one redelivery
// appended, so a failure here and a failure there describe the same defect:
//
//	(ii)  every supersede mark's superseder is NEWER in the append-only index than the record it
//	      supersedes (x09 v3_x09_test.go:358);
//	(iii) every supersede mark the pass appended is authored by a record that same pass wrote — "a
//	      mark with no new record behind it is a redelivered read re-running supersession"
//	      (x09:362).
//
// Deterministic by construction: a fake clock, a real store, a counted context. No sleeps, no
// wall-clock assertions, no goroutines. The x09 run is the end-to-end confirmation and it is
// statistical; this is the proof.

// rdxReadCutPoints and rdxStopCutPoints bound each sweep. They are comfortably above the number of
// context checks either path makes, so the highest values in each range are COMPLETE first runs —
// the pure-replay case — and the sweep covers "cut everywhere" and "not cut at all" in one loop.
const (
	rdxReadCutPoints = 8
	rdxStopCutPoints = 7
)

// rdxCountdown is a context whose Err answers nil for the first n checks and context.Canceled for
// every check after that.
//
// It models the cancel landing at a chosen point WITHOUT any timing: the production code's own
// ctx.Err() calls are what advance it, so "cut after 3 checks" means exactly that, on any host and
// under any load. Done() is the embedded background context's, which never fires — nothing in
// internal/observer, internal/store or internal/dag selects on it, and a handler that blocked on
// Done would be a different bug from the one under test.
type rdxCountdown struct {
	context.Context

	mu   sync.Mutex
	left int
}

// rdxCutAfter returns a context that survives n checks and is cancelled from the (n+1)-th on.
func rdxCutAfter(n int) *rdxCountdown {
	return &rdxCountdown{Context: context.Background(), left: n}
}

func (c *rdxCountdown) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.left > 0 {
		c.left--
		return nil
	}
	return context.Canceled
}

// rdxProcessStates are the three states the REDELIVERING process can be in, which is what decides
// what it re-derives. The last is the one x09 produced: a flush-time daemon that never reached an
// idle Persist, so every turn counter starts at 0 again.
var rdxProcessStates = []struct {
	name    string
	restart bool
	persist bool
}{
	{name: "same process"},
	{name: "restart with a persisted turn", restart: true, persist: true},
	{name: "restart with no persisted state", restart: true},
}

// rdxOrdinals maps each content record's id to its line ordinal in the append-only index, which is
// exactly what x09's x9IndexOrdinals computes and what its "superseder is newer" rule reads.
func rdxOrdinals(t *testing.T, lines []rdxLine) map[string]int {
	t.Helper()
	ord := make(map[string]int, len(lines))
	for i, l := range lines {
		if l.Op != "" {
			continue
		}
		if _, seen := ord[l.ID]; !seen {
			ord[l.ID] = i
		}
	}
	return ord
}

// rdxAssertMarksAreSound applies x09's two mark rules: (ii) over the whole index, and (iii) over
// just the lines this pass appended.
func rdxAssertMarksAreSound(t *testing.T, all, appended []byte) {
	t.Helper()

	allLines := rdxParse(t, all)
	ord := rdxOrdinals(t, allLines)
	for _, l := range allLines {
		if l.Op != "supersede" {
			continue
		}
		older, hasOlder := ord[l.ID]
		newer, hasNewer := ord[l.By]
		require.True(t, hasOlder && hasNewer,
			"supersede mark %s by %s names a record the index does not hold", l.ID, l.By)
		require.Greater(t, newer, older,
			"x09 rule (ii): supersede mark %s by %s has the OLDER record as superseder (ordinals %d, %d) "+
				"- a replayed read is superseding records newer than its content", l.ID, l.By, newer, older)
	}

	newLines := rdxParse(t, appended)
	wrote := map[string]bool{}
	for _, l := range newLines {
		if l.Op == "" {
			wrote[l.ID] = true
		}
	}
	for _, l := range newLines {
		if l.Op != "supersede" {
			continue
		}
		require.True(t, wrote[l.By],
			"x09 rule (iii): supersede mark %s by %s was appended by a pass that wrote no record for %s "+
				"- a mark with no new record behind it is a redelivered read re-running supersession",
			l.ID, l.By, l.By)
	}
}

// rdxAppended returns the bytes final grew by over before, asserting append-only along the way.
func rdxAppended(t *testing.T, before, final []byte) []byte {
	t.Helper()
	require.True(t, strings.HasPrefix(string(final), string(before)),
		"index/tool_use.jsonl is append-only; it was rewritten")
	return final[len(before):]
}

// rdxHasRecord reports whether the index holds a content record with id.
func rdxHasRecord(t *testing.T, index []byte, id string) bool {
	t.Helper()
	for _, got := range rdxIDs(t, index, "") {
		if got == id {
			return true
		}
	}
	return false
}

// rdxMarksAuthoredBy counts the supersede lines whose superseder is id.
func rdxMarksAuthoredBy(t *testing.T, index []byte, id string) int {
	t.Helper()
	n := 0
	for _, l := range rdxParse(t, index) {
		if l.Op == "supersede" && l.By == id {
			n++
		}
	}
	return n
}

// TestRedelivery_ReadCutAtEveryPointKeepsRecordAndMarksTogether is the read sweep, and it is the
// deterministic form of today's x09 failure:
//
//	"supersede mark toolu_x9_120 by toolu_x9_144: the superseder is not a record this flush wrote"
//
// The first run of B passed RecordToolUse and was then cancelled before its mark landed; the
// redelivery found the record already there, re-ran supersession, and appended the (correct) mark
// alone, behind a record line that predated the flush. The fix removes the interleaving rather than
// the mark: a record and the marks it authors land in ONE index write, so "record without its
// marks" is not a state the store can be left in, and a replay appends nothing at all.
func TestRedelivery_ReadCutAtEveryPointKeepsRecordAndMarksTogether(t *testing.T) {
	for _, proc := range rdxProcessStates {
		for cut := range rdxReadCutPoints {
			t.Run(fmt.Sprintf("%s/cut after %d checks", proc.name, cut), func(t *testing.T) {
				r := newRdxRig(t)
				ctx := context.Background()

				// A: an earlier read of the same path, fully recorded, so B has something to supersede.
				_, err := r.o.OnToolUse(ctx, readOf("toolu_A", supersedePath, rdxBody))
				require.NoError(t, err)
				require.True(t, rdxHasRecord(t, r.index(), "toolu_A"), "fixture: A is recorded")
				r.clock.Advance(time.Second)

				// B: the same content again, so it supersedes A. Its first run is cut after `cut`
				// context checks — the pre-flush shutdown, at every point it can land.
				obsB := r.sidecar(2, rdxOpTool)
				b := readOf("toolu_B", supersedePath, rdxBody)
				_, _ = r.o.OnToolUse(WithObservation(rdxCutAfter(cut), obsB), b) //nolint:errcheck // the cut's error is the point

				afterCut := r.index()
				recordedByCut := rdxHasRecord(t, afterCut, "toolu_B")
				if !recordedByCut {
					require.Zero(t, rdxMarksAuthoredBy(t, afterCut, "toolu_B"),
						"a cut that left no B record must have left no mark authored by B either: that "+
							"orphan mark is the state a redelivery then completes behind a stale record")
				}

				if proc.restart {
					r.restart(proc.persist)
				}

				// The redelivery: the daemon rewrites the sidecar (stage 1) and dispatches under the
				// lease it already holds, on a live context.
				r.clock.Advance(time.Second)
				r.sidecar(2, rdxOpTool)
				_, err = r.o.OnToolUse(WithObservation(ctx, obsB), b)
				require.NoError(t, err)

				final := r.index()
				appended := rdxAppended(t, afterCut, final)

				// One record per host tool_use_id, whatever the cut point was.
				ids := rdxIDs(t, final, "")
				n := 0
				for _, id := range ids {
					if id == "toolu_B" {
						n++
					}
				}
				require.Equal(t, 1, n, "exactly one record for one host tool_use_id; index ids: %v", ids)

				if recordedByCut {
					require.Empty(t, appended,
						"the first run published B, so the redelivery must append NOTHING - it appended:\n%s",
						appended)
				}
				rdxAssertMarksAreSound(t, final, appended)
			})
		}
	}
}

// TestRedelivery_StopCutAtEveryPointLeavesOneRecord is the same sweep for site 1, whose x09
// assertion is the SubagentStop count: "index/tool_use.jsonl must hold exactly one SubagentStop
// record per Stop event after the flush".
//
// Every cut point resolves to one record, by one of two routes. A first run cut before its index
// write published nothing the redelivery can recognize, so the redelivery captures normally — at
// the first FREE derived turn, which is what keeps a turn the cut run already consumed from
// collapsing two events onto one id. A first run cut after its index write published a record the
// redelivery recognizes through the capture sidecar, and absorbs.
func TestRedelivery_StopCutAtEveryPointLeavesOneRecord(t *testing.T) {
	for _, proc := range rdxProcessStates {
		for cut := range rdxStopCutPoints {
			t.Run(fmt.Sprintf("%s/cut after %d checks", proc.name, cut), func(t *testing.T) {
				r := newRdxRig(t)
				ctx := context.Background()
				obs := r.sidecar(1, rdxOpStop)

				_, _ = r.o.OnStop(WithObservation(rdxCutAfter(cut), obs), stopOf(true), true) //nolint:errcheck // the cut's error is the point
				afterCut := r.index()

				if proc.restart {
					r.restart(proc.persist)
				}

				r.clock.Advance(time.Second)
				r.sidecar(1, rdxOpStop)
				_, err := r.o.OnStop(WithObservation(ctx, obs), stopOf(true), true)
				require.NoError(t, err)

				final := r.index()
				require.Len(t, rdxIDs(t, final, subagentStop), 1,
					"one SubagentStop record per Stop event, whatever point the first run was cut at")
				rdxAssertMarksAreSound(t, final, rdxAppended(t, afterCut, final))
			})
		}
	}
}

// TestRedelivery_ReadReplayNeverInvertsASupersedeMark is mechanism 2b, the INVERTED mark, and it is
// the x09 assertion at line 358 rather than 362.
//
// The first run of B completed but was never acknowledged. A later read C of the same content
// landed and superseded B. Then B is redelivered. On the pre-fix tree the replay re-ran
// supersession with rec.TS taken from the REDELIVERY's clock rather than from the stored record's,
// so supersede.go's "only ever mark EARLIER reads" filter (p.TS > rec.TS) admitted C — and the
// index gained a mark saying the newest read was superseded by an older one.
//
// With the marks landing only alongside a NEW record line, a replay writes nothing and the
// inversion is unreachable rather than merely unlikely.
func TestRedelivery_ReadReplayNeverInvertsASupersedeMark(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()

	_, err := r.o.OnToolUse(ctx, readOf("toolu_A", supersedePath, rdxBody))
	require.NoError(t, err)
	r.clock.Advance(time.Second)

	obsB := r.sidecar(2, rdxOpTool)
	b := readOf("toolu_B", supersedePath, rdxBody)
	_, err = r.o.OnToolUse(WithObservation(ctx, obsB), b)
	require.NoError(t, err)
	r.clock.Advance(time.Second)

	// C, the same content once more: it supersedes B, and B is now the OLDER record.
	_, err = r.o.OnToolUse(ctx, readOf("toolu_C", supersedePath, rdxBody))
	require.NoError(t, err)

	recC, err := r.st.ToolUse(ctx, "toolu_C")
	require.NoError(t, err)
	require.Equal(t, store.StatusOK, recC.Status, "fixture: C is the newest read and is not superseded")
	before := r.index()

	// B's delivery is redelivered under its reused lease, long after C landed.
	r.clock.Advance(time.Second)
	r.sidecar(2, rdxOpTool)
	_, err = r.o.OnToolUse(WithObservation(ctx, obsB), b)
	require.NoError(t, err)

	require.Equal(t, string(before), string(r.index()),
		"a replayed read must append NO line: re-running supersession against the redelivery's own "+
			"clock is what marks records NEWER than the replayed content (x09:358)")

	recC, err = r.st.ToolUse(ctx, "toolu_C")
	require.NoError(t, err)
	require.Equal(t, store.StatusOK, recC.Status,
		"the newest read must not be superseded by a replay of an older one")
	require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed))
}

// TestRedelivery_SameHostIDFromTwoDeliveriesRecordsOnce states the tool path's real predicate.
//
// Recognition on the HOST-identified path is the store's own append-only dedup, and it makes no
// reference to the observation — on this path it cannot, because the host id IS the identity and
// two deliveries carrying it are indistinguishable here. So the rule is "one record per
// (tool_use_id, root)", not "one record per ObservationID", and it absorbs more than a redelivery:
// a duplicated hook registration, a host resend, an in-process replay.
//
// That is a behaviour change for non-redeliveries and it is the intended one — a second run of the
// derived pipeline double-counts the sketches and the tombstone counter, appends a second file
// version, and rebuilds the DAG edge from the wrong predecessor — so it is pinned here rather than
// left implied by the headline.
func TestRedelivery_SameHostIDFromTwoDeliveriesRecordsOnce(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()
	e := readOf("toolu_dup", supersedePath, rdxBody)

	first := r.sidecar(1, rdxOpTool)
	_, err := r.o.OnToolUse(WithObservation(ctx, first), e)
	require.NoError(t, err)
	before := r.index()

	// A different delivery: its own arrival, and therefore its own ObservationID.
	r.clock.Advance(time.Second)
	second := r.sidecar(2, rdxOpTool)
	require.NotEqual(t, first, second, "fixture: two distinct deliveries")
	_, err = r.o.OnToolUse(WithObservation(ctx, second), e)
	require.NoError(t, err)

	require.Equal(t, string(before), string(r.index()),
		"one record per (tool_use_id, root); the second delivery appended:\n%s", r.index()[len(before):])
	require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed))

	st := r.o.session(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Len(t, st.ToolUses, 1,
		"the derived pipeline ran once: window membership is restored by id, never appended twice")
}
