package checkpointtest

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the checkpointtest suites (§15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): the schema round-trip, the §6.9 tier
// order (tier 3 first, then tier 2, tier 1 never), the §4.6 DPI guard on Advance, and §13
// invariant 5's "no code blocks in a checkpoint". All are authored now, gated behind the same
// Rule W-1 stub probes as the rest of the suite, so SP-10 inherits them rather than writing its
// own grader.

// Fixture constants. Values are deliberately off the D11/§11.6 forbidden-literal set, since this
// is not a _test.go file and nomagic therefore applies to it.
const (
	// fixtureSession is the session id every fixture checkpoint below belongs to.
	fixtureSession = "sess_checkpointtest"
	// fixtureCreated is an arbitrary but fixed RFC 3339 UTC timestamp.
	fixtureCreated = "2026-01-01T00:12:30.000Z"
	// generousBudget is far larger than any fixture below, so a correct Truncate drops nothing.
	generousBudget = core.Tokens(1 << 30)
	// starvedBudget is one token: nothing but tier 1 can survive it.
	starvedBudget = core.Tokens(1)
)

// budgetLadder is a strictly descending set of budgets the tier-order and monotonicity cases walk.
// None of its values duplicates a config default (D11, §11.6).
var budgetLadder = []core.Tokens{900, 700, 500, 200, 50, 5, starvedBudget}

// runWriterRoundTripCase asserts the Begin → Advance → Finalize path produces a Ref that actually
// describes a written artifact: a 1-based sequence, a path, a content digest, a non-zero size,
// and the frontier Advance reported.
func runWriterRoundTripCase(t *testing.T, factory func(t *testing.T) WriterFixture) {
	t.Helper()
	f := factory(t)
	ctx := context.Background()
	require.NotEmpty(t, f.Segments,
		"fixture sanity: a real WriterFixture must supply at least one closed, unencoded segment")

	d, err := f.Writer.Begin(ctx, f.Session, core.CheckpointSeq(0), f.Source)
	require.NoError(t, err)
	require.NotNil(t, d, "Begin must return a usable draft when it reports no error")

	frontier, err := f.Writer.Advance(ctx, d, f.Segments)
	require.NoError(t, err)

	ref, err := f.Writer.Finalize(ctx, d, f.Budget)
	require.NoError(t, err)
	require.Greater(t, int(ref.Seq), 0, "a finalized checkpoint has a 1-based sequence number")
	require.NotEmpty(t, ref.Path, "Finalize must report where it wrote the artifact")
	require.False(t, ref.SHA256.IsZero(), "Finalize must report the artifact's content digest")
	require.Greater(t, ref.Bytes, int64(0), "a finalized artifact is not empty")
	require.Equal(t, frontier, ref.Frontier,
		"Finalize must report the frontier Advance reached, not re-derive a different one")
}

// runAdvanceDPIGuardCase is the §4.6 "never compress a compression" guard at the checkpoint
// boundary: a segment's ORIGINAL content may be encoded into a checkpoint exactly once. Advancing
// the same draft over the same segments twice is idempotent (same checkpoint seq); advancing a
// LATER draft over them is refused with core.ErrAlreadyEncoded.
func runAdvanceDPIGuardCase(t *testing.T, factory func(t *testing.T) WriterFixture) {
	t.Helper()
	f := factory(t)
	ctx := context.Background()
	require.NotEmpty(t, f.Segments, "fixture sanity: the DPI guard needs a segment to guard")

	first, err := f.Writer.Begin(ctx, f.Session, core.CheckpointSeq(0), f.Source)
	require.NoError(t, err)

	_, err = f.Writer.Advance(ctx, first, f.Segments)
	require.NoError(t, err)

	// Idempotent within one draft: the same segments encoded into the same checkpoint seq.
	_, err = f.Writer.Advance(ctx, first, f.Segments)
	require.NoError(t, err,
		"re-advancing the same draft over the same segments is idempotent, not a DPI violation")

	ref, err := f.Writer.Finalize(ctx, first, f.Budget)
	require.NoError(t, err)

	second, err := f.Writer.Begin(ctx, f.Session, ref.Seq, f.Source)
	require.NoError(t, err)

	_, err = f.Writer.Advance(ctx, second, f.Segments)
	require.ErrorIs(t, err, core.ErrAlreadyEncoded,
		"encoding an already-encoded segment into a SECOND checkpoint is the §4.6 violation")
}

// runAbortCase asserts Abort discards a draft and that the discarded draft cannot then be
// finalized.
//
// Note for SP-10: §5.14 does not say whether Abort rolls back the SegmentLog.MarkEncoded calls a
// prior Advance made. This case deliberately asserts only the direction that is unambiguous — an
// aborted draft produces no artifact. Whichever way SP-10 resolves the rollback question, it
// should add its own case here and state the answer in Abort's doc comment.
func runAbortCase(t *testing.T, factory func(t *testing.T) WriterFixture) {
	t.Helper()
	f := factory(t)
	ctx := context.Background()

	d, err := f.Writer.Begin(ctx, f.Session, core.CheckpointSeq(0), f.Source)
	require.NoError(t, err)
	require.NoError(t, f.Writer.Abort(d))

	_, err = f.Writer.Finalize(ctx, d, f.Budget)
	require.Error(t, err, "an aborted draft must never produce an artifact")
}

// runReaderSchemaRoundTripCase asserts Latest and Get agree, that the Ref describes the
// Checkpoint it came back with, and that a Checkpoint survives a marshal/unmarshal cycle
// unchanged — the "schema round-trip" of §15's behaviour table.
func runReaderSchemaRoundTripCase(t *testing.T, factory func(t *testing.T) ReaderFixture) {
	t.Helper()
	f := factory(t)
	ctx := context.Background()

	c, ref, err := f.Reader.Latest(ctx, f.Session)
	require.NoError(t, err)
	require.Equal(t, checkpoint.SchemaVersion, c.Version, "every artifact carries the schema version")
	require.Equal(t, f.Session, c.Session)
	require.Equal(t, c.Seq, ref.Seq, "the Ref must describe the Checkpoint it was returned with")
	require.NotEmpty(t, ref.Path)

	raw, err := json.Marshal(c)
	require.NoError(t, err)
	var back checkpoint.Checkpoint
	require.NoError(t, json.Unmarshal(raw, &back))
	require.Equal(t, c, back, "a Checkpoint must survive a marshal/unmarshal cycle unchanged")

	got, gotRef, err := f.Reader.Get(ctx, c.Seq)
	require.NoError(t, err)
	require.Equal(t, c, got, "Get(Latest().Seq) must return the same Checkpoint Latest did")
	require.Equal(t, ref.Seq, gotRef.Seq)
	require.Equal(t, ref.SHA256, gotRef.SHA256)

	atFixture, _, err := f.Reader.Get(ctx, f.Seq)
	require.NoError(t, err)
	require.Equal(t, f.Seq, atFixture.Seq, "Get(seq) must return the checkpoint with that seq")
}

// runReaderListAndChainCase asserts List is ascending and complete, and that Chain walks Parent
// links from the oldest ancestor up to the requested seq.
func runReaderListAndChainCase(t *testing.T, factory func(t *testing.T) ReaderFixture) {
	t.Helper()
	f := factory(t)
	ctx := context.Background()

	refs, err := f.Reader.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, refs, "a fixture with a checkpoint on disk must list it")

	var sawFixtureSeq bool
	for i, r := range refs {
		require.NotEmpty(t, r.Path, "every listed Ref names its artifact")
		if r.Seq == f.Seq {
			sawFixtureSeq = true
		}
		if i > 0 {
			require.Greater(t, int(r.Seq), int(refs[i-1].Seq), "List must be ascending by Seq")
		}
	}
	require.True(t, sawFixtureSeq, "List must include every checkpoint on disk")

	chain, err := f.Reader.Chain(ctx, f.Seq)
	require.NoError(t, err)
	require.NotEmpty(t, chain, "Chain of an existing seq contains at least that checkpoint")
	require.Equal(t, f.Seq, chain[len(chain)-1].Seq, "Chain ends at the requested seq")
	for i := 1; i < len(chain); i++ {
		require.Greater(t, int(chain[i].Seq), int(chain[i-1].Seq), "Chain is ordered oldest-first")
		require.NotEmpty(t, chain[i].Parent, "every checkpoint after the first names its parent")
	}
}

// runReaderVerifyCase asserts Verify reports no mismatches for an untampered checkpoint store.
// It is the read half of the MANIFEST contract `qompack fsck` reports on.
func runReaderVerifyCase(t *testing.T, factory func(t *testing.T) ReaderFixture) {
	t.Helper()
	f := factory(t)

	bad, err := f.Reader.Verify(context.Background())
	require.NoError(t, err)
	require.Empty(t, bad, "an untampered checkpoint store must have no MANIFEST mismatches")
}

// runReaderAbsentSeqCase pins the ErrNotFound answer: a sequence number that is not on disk is a
// miss, not an empty success.
func runReaderAbsentSeqCase(t *testing.T, factory func(t *testing.T) ReaderFixture) {
	t.Helper()
	f := factory(t)
	require.NotEqual(t, f.Seq, f.AbsentSeq, "fixture sanity: AbsentSeq must not be the present one")

	_, _, err := f.Reader.Get(context.Background(), f.AbsentSeq)
	require.ErrorIs(t, err, core.ErrNotFound, "an absent seq is core.ErrNotFound, not a zero Checkpoint")
}

// runReaderNoCodeBlocksCase is §13 invariant 5 asserted against every artifact on disk: a
// checkpoint carries pointers and reasons, never code (Qompack.md §4.4). This is the assertion
// that catches a summarizer, or a future decision extractor, quietly pasting a hunk into a
// narrative or a pointer's "why".
func runReaderNoCodeBlocksCase(t *testing.T, factory func(t *testing.T) ReaderFixture) {
	t.Helper()
	f := factory(t)
	ctx := context.Background()

	refs, err := f.Reader.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, refs)

	for _, r := range refs {
		c, _, err := f.Reader.Get(ctx, r.Seq)
		require.NoError(t, err)
		raw, err := json.Marshal(c)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(raw), "```"),
			"checkpoint %d contains a fenced code block (§13 invariant 5)", int(r.Seq))
	}
}

// runTruncateNoOpCase asserts a budget larger than the checkpoint drops nothing at all.
func runTruncateNoOpCase(t *testing.T, factory func(t *testing.T) TruncateFunc) {
	t.Helper()
	truncate := factory(t)
	in := oversizedCheckpoint()

	got, drops := truncate(in, generousBudget)
	require.Empty(t, drops, "nothing is dropped when everything fits")
	require.Equal(t, in, got, "a checkpoint that fits its budget is returned unchanged")
}

// runTruncateTier1NeverCase is the half of §6.9 that has no exceptions: invariants, user intent
// and eliminations survive every budget, including one that leaves room for nothing else.
func runTruncateTier1NeverCase(t *testing.T, factory func(t *testing.T) TruncateFunc) {
	t.Helper()
	truncate := factory(t)
	in := oversizedCheckpoint()

	for _, budget := range budgetLadder {
		got, _ := truncate(in, budget)
		require.Equal(t, in.Invariants, got.Invariants, "tier 1 invariants are never truncated (budget %d)", int(budget))
		require.Equal(t, in.UserIntent, got.UserIntent, "tier 1 user intent is never truncated (budget %d)", int(budget))
		require.Equal(t, in.Eliminated, got.Eliminated, "tier 1 eliminations are never truncated (budget %d)", int(budget))

		// Identity metadata is not a tier at all; truncating must never lose it.
		require.Equal(t, in.Version, got.Version)
		require.Equal(t, in.Session, got.Session)
		require.Equal(t, in.Seq, got.Seq)
		require.Equal(t, in.Created, got.Created)
		require.Equal(t, in.Parent, got.Parent)
		require.Equal(t, in.EncodedSegments, got.EncodedSegments)
	}
}

// runTruncateTierOrderCase is the ordering half of §6.9: tier 3 (pointers, narrative) is
// exhausted before tier 2 (decisions, open questions, current work) is touched at all. Stated as
// an implication over the whole budget ladder, it needs no token estimate of its own — which is
// what makes it checkable from a package that may not import tokens (§3.2).
func runTruncateTierOrderCase(t *testing.T, factory func(t *testing.T) TruncateFunc) {
	t.Helper()
	truncate := factory(t)
	in := oversizedCheckpoint()

	for _, budget := range budgetLadder {
		got, drops := truncate(in, budget)
		if tier3Size(got) > 0 {
			require.Equal(t, in.Decisions, got.Decisions,
				"tier 2 decisions must survive while tier 3 still has content (budget %d)", int(budget))
			require.Equal(t, in.OpenQuestions, got.OpenQuestions,
				"tier 2 open questions must survive while tier 3 still has content (budget %d)", int(budget))
			require.Equal(t, in.CurrentWork, got.CurrentWork,
				"tier 2 current work must survive while tier 3 still has content (budget %d)", int(budget))
		}
		if tier3Size(got) < tier3Size(in) || tier2Size(got) < tier2Size(in) {
			require.NotEmpty(t, drops,
				"anything removed must be reported in the drop report (G4.5) (budget %d)", int(budget))
		}
	}

	// One token leaves room for nothing beyond tier 1.
	starved, drops := truncate(in, starvedBudget)
	require.Zero(t, tier3Size(starved), "tier 3 cannot survive a one-token budget")
	require.NotEmpty(t, drops, "a starved budget must report what it dropped")
}

// runTruncateMonotoneCase asserts shrinking the budget never restores content: over a strictly
// descending budget ladder, neither tier's surviving size ever grows.
func runTruncateMonotoneCase(t *testing.T, factory func(t *testing.T) TruncateFunc) {
	t.Helper()
	truncate := factory(t)
	in := oversizedCheckpoint()

	prev, _ := truncate(in, generousBudget)
	for _, budget := range budgetLadder {
		got, _ := truncate(in, budget)
		require.LessOrEqual(t, tier3Size(got), tier3Size(prev),
			"a smaller budget must not restore tier-3 content (budget %d)", int(budget))
		require.LessOrEqual(t, tier2Size(got), tier2Size(prev),
			"a smaller budget must not restore tier-2 content (budget %d)", int(budget))
		prev = got
	}
}

// runTruncatePurityCase asserts Truncate is a function, not a mutator: the same input twice gives
// the same output, and the caller's own Checkpoint is untouched afterwards. The second half
// matters because Checkpoint is full of slices, and an in-place truncation would silently corrupt
// the caller's copy — the draft the writer is still holding.
func runTruncatePurityCase(t *testing.T, factory func(t *testing.T) TruncateFunc) {
	t.Helper()
	truncate := factory(t)
	in := oversizedCheckpoint()
	pristine := oversizedCheckpoint()

	first, firstDrops := truncate(in, budgetLadder[0])
	require.Equal(t, pristine, in, "Truncate must not mutate the Checkpoint it was given")

	second, secondDrops := truncate(in, budgetLadder[0])
	require.Equal(t, first, second, "Truncate must be deterministic for identical inputs")
	require.Equal(t, firstDrops, secondDrops, "the drop report must be deterministic too")
}

// unchanged reports whether got is value-identical to in; it backs the behavioural stub probe in
// suite.go, which has no error sentinel to look for.
func unchanged(in, got checkpoint.Checkpoint) bool { return reflect.DeepEqual(in, got) }

// tier2Size counts the tier-2 items still present: decisions, open questions and — as one item —
// a non-empty current-work statement.
func tier2Size(c checkpoint.Checkpoint) int {
	n := len(c.Decisions) + len(c.OpenQuestions)
	if c.CurrentWork.Goal != "" || c.CurrentWork.NextStep != "" {
		n++
	}
	return n
}

// tier3Size counts the tier-3 items still present: file pointers, tool pointers and — as one
// item — a non-empty narrative.
func tier3Size(c checkpoint.Checkpoint) int {
	n := len(c.Pointers.Files) + len(c.Pointers.Tools)
	if c.Narrative != "" {
		n++
	}
	return n
}

// oversizedCheckpoint is the fixture every Truncate case starts from: every tier populated, and
// far more tier-2 and tier-3 content than a small budget can hold.
//
// Eliminated is deliberately left nil. It is []negknow.Record, and checkpointtest may not import
// negknow (§3.2) — so the tier-1 assertions above exercise Invariants and UserIntent, and assert
// only that Eliminated is returned exactly as it was given. SP-10, whose own tests may import
// negknow, should add a populated-Eliminated case alongside these.
func oversizedCheckpoint() checkpoint.Checkpoint {
	return checkpoint.Checkpoint{
		Version:         checkpoint.SchemaVersion,
		Session:         core.SessionID(fixtureSession),
		Seq:             core.CheckpointSeq(2),
		Created:         fixtureCreated,
		Parent:          "0001.json",
		EncodedSegments: []core.SegmentID{7, 8, 9},

		// ── Tier 1: never truncated ──
		Invariants: []checkpoint.Invariant{
			{ID: "inv_never_a", Text: "Refresh-token rotation stays single-use.", Source: "user", Pinned: core.UnixMilli(1)},
			{ID: "inv_never_b", Text: "Never change the pool mode without re-running the reproduction.", Source: "agent", Pinned: core.UnixMilli(2)},
		},
		UserIntent: checkpoint.UserIntent{
			Original:  "Fix the intermittent failures on the refresh endpoint.",
			Evolution: []string{"Narrowed to a pool acquisition timeout.", "Widened to the pooler's transaction mode."},
		},

		// ── Tier 2: truncate late ──
		Decisions: []checkpoint.Decision{
			{ID: core.DecisionID("dec_one"), What: "Move rotation out of the request transaction.", Why: "It holds a lock across an outbound call.", Turn: core.TurnIndex(11)},
			{ID: core.DecisionID("dec_two"), What: "Keep the pool size unchanged.", Why: "Resizing moves the cliff without removing the pattern.", Turn: core.TurnIndex(19)},
			{ID: core.DecisionID("dec_three"), What: "Record the widened-timeout attempt as eliminated.", Why: "The pooler ignores it in transaction mode.", Turn: core.TurnIndex(23)},
		},
		OpenQuestions: []string{
			"Is the retried rotation idempotent upstream?",
			"Does staging run the same pooler build as production?",
			"Do we need our own dedupe key?",
		},
		CurrentWork: checkpoint.CurrentWork{
			Goal:     "Eliminate pool exhaustion under concurrent refreshes.",
			NextStep: "Extract the outbound call from the transaction and re-run the reproduction.",
		},

		// ── Tier 3: truncate first ──
		Pointers: checkpoint.Pointers{
			Files: []checkpoint.FilePointer{
				{Path: "src/auth.ts", Why: "the rotation lives here"},
				{Path: "src/pool.ts", Why: "acquisition timeout is configured here"},
				{Path: "docker-compose.yml", Why: "pins the pooler version and mode"},
				{Path: "package-lock.json", Why: "pins the client library the elimination depends on"},
			},
			Tools: []checkpoint.ToolPointer{
				{ToolUseID: core.ToolUseID("toolu_load_test"), Summary: "load test: concurrent refreshes, all failures were acquisition timeouts"},
				{ToolUseID: core.ToolUseID("toolu_pool_stats"), Summary: "pool statistics during the failing window"},
				{ToolUseID: core.ToolUseID("toolu_pooler_log"), Summary: "pooler log for the failing window"},
			},
		},
		Narrative: strings.Repeat(
			"Reproduced the failures under concurrent load and traced them to acquisition timeouts rather than token validation. ", 8),

		// ── Metadata ──
		SketchRefs: map[string]string{"tried": "tried.bloom", "touch": "touch.cms"},
		Cache:      checkpoint.CacheInfo{PChosen: 3, RewriteTokens: 5, TTLState: "warm"},
	}
}
