package checkpoint_test

// A decision stays in every later checkpoint of its session while it holds, the way eliminations
// carry (coordinator decision D49 of the V6 close-out, finding F-C4-UAT06-2 of the candidate 4
// live re-run, plans/sdd/V6-closeout/live/rerun-c4/UAT-06/steps/checkpoint-0001.json and
// checkpoint-0002.json).
//
// A draft mints decisions only from what it encodes or refreshes at or after its own frontier, and
// the successor a seal opens (or the draft a restarted daemon begins) starts with none: the
// session's second checkpoint sealed decisions [] beside the elimination the first one had minted
// dec_991dbff588ec from, still carried.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
)

const (
	carryApproach = "100 requests per minute"
	carryPin      = "keep the limiter in-process because a shared store adds a network hop"
)

// sealFirstWithDecision seals the session's first checkpoint over an elimination recorded at turn
// 12 and a decision pin, and returns it. The segment holding turn 12 closes before the compaction,
// so the first checkpoint encodes it and every later segment of the session starts after it.
func sealFirstWithDecision(t *testing.T, f *fx) checkpoint.Checkpoint {
	t.Helper()
	f.pins.invs = append(f.pins.invs, pins.Invariant{ID: "inv_carry", Text: carryPin, Source: "decision"})
	seedForPreCompact(t, f)
	ctx := negknow.WithCaller(f.ctx(), negknow.Caller{Session: f.sess, Turn: 12})
	_, err := f.ledger.Record(ctx, negknow.Record{
		Target: "per-client rate limit", Approach: carryApproach, Reason: "superseded by the user's correction",
		Evidence: core.HashBytes(core.DomainChunk, []byte("limits evidence")),
	})
	require.NoError(t, err)
	f.closedSeg(3, 10, 12)

	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	cp := f.sealed(t, res.Ref.Seq)
	require.Len(t, rejected(cp, carryApproach), 1, "fixture sanity: the first checkpoint has the decision")
	require.NotEmpty(t, pinned(cp), "fixture sanity: and the pinned one")
	return cp
}

// pinned returns the decisions minted from the carryPin decision pin.
func pinned(cp checkpoint.Checkpoint) []checkpoint.Decision {
	var out []checkpoint.Decision
	for _, d := range cp.Decisions {
		if d.What == "keep the limiter in-process" {
			out = append(out, d)
		}
	}
	return out
}

// continueSession closes one more segment after the first seal and encodes it into the session's
// live draft (the successor the seal opened, or one a restarted writer begins).
func continueSession(t *testing.T, f *fx) {
	t.Helper()
	f.tool("toolu_carry_0003", 14, "Read", "docs/notes.md", "notes", false)
	f.closedSeg(4, 13, 15)
	f.advance(f.begin(), 4)
}

// TestSecondCheckpointCarriesTheSessionsEarlierDecision is F-C4-UAT06-2: the same session's next
// checkpoint still carries the decision its first one minted, with the same id.
func TestSecondCheckpointCarriesTheSessionsEarlierDecision(t *testing.T) {
	f := newFx(t)
	first := sealFirstWithDecision(t, f)
	want := rejected(first, carryApproach)[0]

	continueSession(t, f)
	res, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, err)
	second := f.sealed(t, res.Ref.Seq)
	require.Equal(t, core.CheckpointSeq(2), second.Seq)

	got := rejected(second, carryApproach)
	require.Len(t, got, 1, "the decision holds while its elimination is carried: %+v", second.Decisions)
	require.Equal(t, want.ID, got[0].ID, "and it is the same decision, for why(decision_id)")
	require.Equal(t, want.Turn, got[0].Turn, "at the turn it was made")
	require.Len(t, pinned(second), 1)
}

// TestRestartedWriterCarriesTheSessionsEarlierDecision: the daemon restarts between the two
// compactions (the --resume of UAT-06 may land on a new daemon) and the successor draft file is
// gone, so the next PreCompact begins its draft cold from the session's newest checkpoint.
func TestRestartedWriterCarriesTheSessionsEarlierDecision(t *testing.T) {
	f := newFx(t)
	first := sealFirstWithDecision(t, f)
	want := rejected(first, carryApproach)[0]
	require.NoError(t, os.Remove(paths.Long(f.draftPath())))

	w, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	f.w = w
	f.tool("toolu_carry_0004", 14, "Read", "docs/notes.md", "notes", false)
	f.closedSeg(4, 13, 15)
	res := f.precompactAs(f.sess)
	second := f.sealed(t, res.Ref.Seq)

	got := rejected(second, carryApproach)
	require.Len(t, got, 1, "the decision survives a daemon restart: %+v", second.Decisions)
	require.Equal(t, want.ID, got[0].ID)
}

// TestCarriedDecisionEndsWhenItsPinIsRemoved: "while it holds" — a decision pinned as one is not
// carried into a draft begun after the pin is gone, while the elimination's decision is. The draft
// is begun cold (a restarted daemon), which is where the carry reads the pin set.
func TestCarriedDecisionEndsWhenItsPinIsRemoved(t *testing.T) {
	f := newFx(t)
	sealFirstWithDecision(t, f)
	require.NoError(t, os.Remove(paths.Long(f.draftPath())))
	f.pins.invs = nil // unpinned

	w, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	f.w = w
	f.tool("toolu_carry_0005", 14, "Read", "docs/notes.md", "notes", false)
	f.closedSeg(4, 13, 15)
	second := f.sealed(t, f.precompactAs(f.sess).Ref.Seq)
	require.Empty(t, pinned(second), "a removed decision pin no longer holds: %+v", second.Decisions)
	require.Len(t, rejected(second, carryApproach), 1)
}
