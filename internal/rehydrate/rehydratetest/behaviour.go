package rehydratetest

import (
	"context"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/stretchr/testify/require"
)

// This file authors the fixtures and behaviour assertions of the rehydratetest suite (§15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): "Items in the normative ItemKind order;
// total tokens <= Budget", plus the drop report, the injection tagging and the degradation
// behaviour §8.6 and §12.3 require. All are authored now, gated behind the same Rule W-1 stub
// probe as the rest of the suite, so SP-11 inherits them rather than writing its own grader.

// The SessionStart source values a rehydration can be asked for (00-ARCHITECTURE.md §5.15).
// Exported because SP-11's own tests branch on the same four.
const (
	// SourceStartup is a fresh session.
	SourceStartup = "startup"
	// SourceResume is a resumed session.
	SourceResume = "resume"
	// SourceCompact is the branch that follows a compaction — the one rehydration exists for.
	SourceCompact = "compact"
	// SourceClear is an explicit context clear.
	SourceClear = "clear"
)

// Fixture constants. Values are deliberately off the D11/§11.6 forbidden-literal set, since this
// is not a _test.go file and nomagic therefore applies to it.
const (
	fixtureSession  = "sess_rehydratetest"
	fixtureSeq      = core.CheckpointSeq(3)
	fixtureRoot     = "/repo"
	generousBudget  = core.Tokens(9000)
	starvedBudget   = core.Tokens(1)
	moderateBudget  = core.Tokens(600)
	fixtureCreated  = "2026-01-01T00:12:30.000Z"
	fixtureFilePath = "src/auth.ts"
)

// injectionMarker is the fixed substring of checkpoint.InjectionOpenTag.
//
// It is spelled out here rather than referenced because rehydratetest may not import checkpoint
// (00-ARCHITECTURE.md §3.2). That duplication is deliberate and bounded: checkpoint's own
// inject_test.go pins the constant byte-for-byte, so the two can only drift if someone changes
// that test in the same commit — at which point this assertion fails and says why.
const injectionMarker = "qompack:injected"

// populatedRequest is the fixture every case starts from: a checkpoint with every tier populated,
// asked for at the given budget.
//
// The Checkpoint is built by field assignment rather than a composite literal because its own
// field types come from packages this one may not import (§3.2) — assigning into
// r.Checkpoint.UserIntent.Original needs no import, whereas naming checkpoint.UserIntent would.
// The same trick is why Invariants and Eliminated are left empty: their element types are
// pins.Invariant and negknow.Record, which cannot be constructed here at all. SP-11, whose own
// tests may import both, should add a case that populates them.
func populatedRequest(budget core.Tokens) rehydrate.Request {
	var r rehydrate.Request
	r.Session = core.SessionID(fixtureSession)
	r.Source = SourceCompact
	r.ProjectRoot = fixtureRoot
	r.Budget = budget

	r.Checkpoint.Version = 1
	r.Checkpoint.Session = core.SessionID(fixtureSession)
	r.Checkpoint.Seq = fixtureSeq
	r.Checkpoint.Created = fixtureCreated
	r.Checkpoint.Parent = "0002.json"
	r.Checkpoint.UserIntent.Original = "Fix the intermittent failures on the refresh endpoint."
	r.Checkpoint.UserIntent.Evolution = []string{"Narrowed to a pool acquisition timeout."}
	r.Checkpoint.OpenQuestions = []string{
		"Is the retried rotation idempotent upstream?",
		"Does staging run the same pooler build as production?",
	}
	r.Checkpoint.CurrentWork.Goal = "Eliminate pool exhaustion under concurrent refreshes."
	r.Checkpoint.CurrentWork.NextStep = "Extract the outbound call from the transaction."
	r.Checkpoint.Narrative = strings.Repeat(
		"Traced the failures to acquisition timeouts rather than token validation. ", 6)
	r.Checkpoint.SketchRefs = map[string]string{"tried": "tried.bloom"}

	r.Ref.Seq = fixtureSeq
	r.Ref.Path = "/repo/.qompack/checkpoints/0003.json"

	// Pointers are tier 3 and the one part of the checkpoint this package can build in full.
	first := appendZero(&r.Checkpoint.Pointers.Files)
	first.Path = fixtureFilePath
	first.Why = "the rotation lives here"

	second := appendZero(&r.Checkpoint.Pointers.Files)
	second.Path = "src/pool.ts"
	second.Why = "acquisition timeout is configured here"

	tool := appendZero(&r.Checkpoint.Pointers.Tools)
	tool.ToolUseID = "toolu_rehydratetest_load"
	tool.Summary = "load test: concurrent refreshes, all failures were acquisition timeouts"

	return r
}

// appendZero appends one zero element to *s and returns a pointer to it, so the caller can fill
// its fields in without ever naming the element type.
//
// It exists for the §3.2 reason stated on populatedRequest: checkpoint.FilePointer and
// checkpoint.ToolPointer are types this package may not import, and a composite literal would
// have to name one. Assigning through a pointer needs no import at all.
func appendZero[S ~[]E, E any](s *S) *E {
	var zero E
	*s = append(*s, zero)
	return &(*s)[len(*s)-1]
}

// itemKinds projects a Result's Items down to their kinds, in order.
func itemKinds(items []rehydrate.Item) []rehydrate.ItemKind {
	out := make([]rehydrate.ItemKind, len(items))
	for i, it := range items {
		out[i] = it.Kind
	}
	return out
}

// itemOfKind returns the first Item of kind k, and whether one was present.
func itemOfKind(items []rehydrate.Item, k rehydrate.ItemKind) (rehydrate.Item, bool) {
	for _, it := range items {
		if it.Kind == k {
			return it, true
		}
	}
	return rehydrate.Item{}, false
}

// runItemOrderCase is §8.6's importance order, which is normative: Items must be emitted in
// ascending ItemKind, and Rank must ascend with them. Reordering is not a formatting choice —
// budget truncation drops from the tail, so the order decides what survives.
func runItemOrderCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	got, err := factory(t)(context.Background(), populatedRequest(generousBudget))
	require.NoError(t, err)
	require.NotEmpty(t, got.Items, "a populated checkpoint rehydrates to at least one item")

	kinds := itemKinds(got.Items)
	for i := 1; i < len(kinds); i++ {
		require.Greater(t, kinds[i], kinds[i-1],
			"items must be emitted in ascending ItemKind, without repeats: got %v", kinds)
	}
	for i, it := range got.Items {
		require.Equal(t, i, it.Rank, "Rank must be the item's own position in the emitted order")
	}
	require.LessOrEqual(t, int(kinds[len(kinds)-1]), int(rehydrate.ItemAffordance),
		"no item may carry a kind beyond the eight §8.6 items")
}

// runBudgetCase is the hard constraint of §8.6: the whole injection fits the budget, and the
// reported total is the true sum over the items rather than an independently maintained counter.
func runBudgetCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	build := factory(t)

	for _, budget := range []core.Tokens{generousBudget, moderateBudget, starvedBudget} {
		got, err := build(context.Background(), populatedRequest(budget))
		require.NoError(t, err, "budget %d", int(budget))
		require.LessOrEqual(t, int(got.Tokens), int(budget),
			"the injection must fit its budget (budget %d)", int(budget))

		var sum core.Tokens
		for _, it := range got.Items {
			sum += it.Tokens
		}
		require.Equal(t, sum, got.Tokens,
			"Result.Tokens must be the sum over Items (budget %d)", int(budget))
	}
}

// runDropReportCase is G4.5: what did not fit is named, not silently absent. A model that is told
// something was dropped can ask for it back; a model that is not told assumes it never existed.
func runDropReportCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	build := factory(t)
	ctx := context.Background()

	full, err := build(ctx, populatedRequest(generousBudget))
	require.NoError(t, err)

	starved, err := build(ctx, populatedRequest(starvedBudget))
	require.NoError(t, err)

	require.Less(t, len(starved.Items), len(full.Items),
		"fixture sanity: a one-token budget must fit fewer items than a generous one")
	require.NotEmpty(t, starved.Dropped, "everything that did not fit must be reported (G4.5)")
}

// runInjectionTagCase is §8.5's regeneration rule: the payload is tagged so a later read of the
// transcript strips it out instead of re-encoding it into the next checkpoint — the §4.6
// never-compress-a-compression invariant, enforced at the injection boundary.
func runInjectionTagCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	got, err := factory(t)(context.Background(), populatedRequest(generousBudget))
	require.NoError(t, err)
	require.NotEmpty(t, got.Text, "a rehydration with items has a payload")

	require.Contains(t, got.Text, injectionMarker,
		"the payload must be wrapped in the §8.5 injection tags")
	require.True(t, strings.Contains(got.Text, "/"+injectionMarker),
		"the payload must be CLOSED as well as opened, or stripping it would swallow the rest of the transcript")
	require.Less(t, strings.Index(got.Text, injectionMarker), strings.Index(got.Text, "/"+injectionMarker),
		"the open tag must precede the close tag")
}

// runStandingInstructionCase is §5.15's "item 3's companion, emitted verbatim". Negative
// knowledge that is present but not actionable gets re-tried; the standing instruction is what
// makes it actionable.
func runStandingInstructionCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	got, err := factory(t)(context.Background(), populatedRequest(generousBudget))
	require.NoError(t, err)

	item, ok := itemOfKind(got.Items, rehydrate.ItemEliminations)
	if !ok {
		// A checkpoint with no eliminations legitimately emits no item 3. The instruction must
		// then not appear either: it is item 3's companion, not a free-floating banner.
		require.NotContains(t, got.Text, rehydrate.StandingInstruction(),
			"the standing instruction belongs to item 3; with no item 3 it must not be emitted")
		return
	}
	require.Contains(t, item.Text, rehydrate.StandingInstruction(),
		"item 3 must carry the standing instruction verbatim")
	require.Contains(t, got.Text, rehydrate.StandingInstruction(),
		"and it must survive into the payload")
}

// runDegradeCase is §12.3: everything fails toward "do nothing". A budget that cannot hold even
// the first item is a degraded rehydration, reported as such — not an error that stops the
// session from starting.
func runDegradeCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	got, err := factory(t)(context.Background(), populatedRequest(starvedBudget))
	require.NoError(t, err, "a starved budget must degrade, never fail")
	require.LessOrEqual(t, int(got.Tokens), int(starvedBudget))
	if len(got.Items) == 0 {
		require.True(t, got.Degraded, "a rehydration that could inject nothing must say it was degraded")
		require.Empty(t, got.Text, "no items means no payload, not an empty tagged wrapper")
	}
}

// runSeqCase asserts the Result names the checkpoint it came from, which is what lets the
// contract monitor confirm the injected context actually reached the transcript (§12.1).
func runSeqCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	got, err := factory(t)(context.Background(), populatedRequest(generousBudget))
	require.NoError(t, err)
	require.Equal(t, fixtureSeq, got.Seq, "Result.Seq must be the sequence of the Checkpoint that was rehydrated")
}

// runDeterminismCase asserts two identical requests produce identical results. Rehydration output
// is compared across replays to measure divergence, so a nondeterministic ordering or wording
// would make that measurement meaningless.
func runDeterminismCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	build := factory(t)
	ctx := context.Background()

	first, err := build(ctx, populatedRequest(generousBudget))
	require.NoError(t, err)
	second, err := build(ctx, populatedRequest(generousBudget))
	require.NoError(t, err)
	require.Equal(t, first, second, "Build must be deterministic for an identical Request")
}

// runSourceCase asserts every SessionStart source is answered. `compact` is the one rehydration
// exists for, but `startup`, `resume` and `clear` all reach Build too, and an unrecognized source
// is a host change rather than a crash (§12.1, §12.3).
func runSourceCase(t *testing.T, factory func(t *testing.T) BuildFunc) {
	t.Helper()
	build := factory(t)
	ctx := context.Background()

	for _, source := range []string{SourceStartup, SourceResume, SourceCompact, SourceClear, "a-source-that-does-not-exist-yet"} {
		r := populatedRequest(generousBudget)
		r.Source = source
		got, err := build(ctx, r)
		require.NoError(t, err, "source=%s must be answered", source)
		require.LessOrEqual(t, int(got.Tokens), int(r.Budget), "source=%s must respect the budget", source)
	}
}
