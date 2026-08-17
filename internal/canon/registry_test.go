package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// allOn is the Options a test uses when it wants every Class in play: a nil Strip means "every
// class", which is what a caller who has not thought about classes gets.
func allOn() canon.Options { return canon.Options{KeepDeltas: true} }

// TestRegistry_RegisterDuplicateName pins 00-ARCHITECTURE.md §5.6's "error on duplicate Name".
func TestRegistry_RegisterDuplicateName(t *testing.T) {
	t.Parallel()

	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "crlf"}))

	err := r.Register(fakeCanon{name: "crlf"})
	require.ErrorIs(t, err, canon.ErrDuplicateName)
	require.Equal(t, []string{"crlf"}, r.Names(), "a rejected registration must not be recorded")
}

// TestRegistry_RegisterNil pins the documented no-op. test/guards' reflective stub walk calls
// every error-returning method with zero-valued arguments and accepts only nil or
// core.ErrNotImplemented, so Register(nil) must not report anything else — and it must not store
// a nil entry that would panic on the next Run.
func TestRegistry_RegisterNil(t *testing.T) {
	t.Parallel()

	r := canon.NewRegistry()
	require.NoError(t, r.Register(nil))
	require.Empty(t, r.Names())

	res, err := r.Run("Bash", "", []byte("still works\n"), allOn())
	require.NoError(t, err)
	require.Equal(t, []byte("still works\n"), res.Canonical)
}

// TestRegistry_AcceptsNonMatcher is the half of the ErrNotMatcher decision that SP-01's frozen
// canontest suite forces: it registers a plain Canonicalizer and requires Register to succeed,
// then runs the same Registry and tolerates only core's four sentinels. So a non-Matcher is
// accepted, is visible through Names and For, and simply contributes nothing to Run.
func TestRegistry_AcceptsNonMatcher(t *testing.T) {
	t.Parallel()

	r := canon.NewRegistry()
	require.NoError(t, r.Register(plainCanon{name: "shape-probe"}))
	require.Equal(t, []string{"shape-probe"}, r.Names())
	require.Len(t, r.For("Bash", ""), 1)

	res, err := r.Run("Bash", "", []byte("2024-01-15T10:32:07Z\n"), allOn())
	require.NoError(t, err)
	require.Equal(t, []byte("2024-01-15T10:32:07Z\n"), res.Canonical, "a non-Matcher rewrites nothing")
	require.Empty(t, res.Applied, "a non-Matcher never appears in Applied")
}

// TestMatchesOf_ReportsErrNotMatcher pins the sentinel that keeps that skip from being silent.
func TestMatchesOf_ReportsErrNotMatcher(t *testing.T) {
	t.Parallel()

	_, err := canon.MatchesOf(plainCanon{name: "shape-probe"}, []byte("x"), allOn())
	require.ErrorIs(t, err, canon.ErrNotMatcher)
	require.Contains(t, err.Error(), "shape-probe")

	got, err := canon.MatchesOf(fakeCanon{name: "f", matchSet: []canon.Match{mk(0, 1, "", canon.ClassANSI)}}, []byte("x"), allOn())
	require.NoError(t, err)
	require.Len(t, got, 1)
}

// TestRegistry_ForDeterministicOrder asserts For filters by Applies and preserves registration
// order, identically on every call.
func TestRegistry_ForDeterministicOrder(t *testing.T) {
	t.Parallel()

	bashOnly := func(tool, path string) bool { return tool == "Bash" }
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "always-1"}))
	require.NoError(t, r.Register(fakeCanon{name: "bash-only", appliesTo: bashOnly}))
	require.NoError(t, r.Register(fakeCanon{name: "always-2"}))

	want := []string{"always-1", "bash-only", "always-2"}
	for i := 0; i < 100; i++ {
		got := make([]string, 0, len(want))
		for _, c := range r.For("Bash", "") {
			got = append(got, c.Name())
		}
		require.Equal(t, want, got, "iteration %d", i)
	}

	var grep []string
	for _, c := range r.For("Grep", "") {
		grep = append(grep, c.Name())
	}
	require.Equal(t, []string{"always-1", "always-2"}, grep)
}

// TestRun_UnknownStripClass asserts a Strip entry outside KnownClasses is a hard failure, not a
// silent skip: a typo there quietly disables a canonicalizer and the only symptom is a worse
// dedup ratio months later.
func TestRun_UnknownStripClass(t *testing.T) {
	t.Parallel()

	r := canon.NewRegistry()
	res, err := r.Run("Bash", "", []byte("x"), canon.Options{Strip: []canon.Class{"nope"}})
	require.ErrorIs(t, err, canon.ErrUnknownClass)
	require.Contains(t, err.Error(), `"nope"`)
	require.Equal(t, canon.Result{}, res)
}

// TestRun_EmptyInput pins the degenerate case: no error, a nil Canonical, a non-nil empty Applied
// and a zero Reduced.
func TestRun_EmptyInput(t *testing.T) {
	t.Parallel()

	r := canon.NewRegistry()
	for _, in := range [][]byte{nil, {}} {
		res, err := r.Run("Bash", "", in, allOn())
		require.NoError(t, err)
		require.Nil(t, res.Canonical)
		require.NotNil(t, res.Applied)
		require.Empty(t, res.Applied)
		require.Zero(t, res.Reduced)
	}
}

// TestOverlapResolution_LongerAtSameOffsetWins asserts the sort's second key: at a shared offset
// the longer match wins, regardless of which canonicalizer emitted it.
func TestOverlapResolution_LongerAtSameOffsetWins(t *testing.T) {
	t.Parallel()

	in := []byte("0123456789abcdef")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "short", matchSet: []canon.Match{mk(0, 4, "<a>", canon.ClassTimestamps)}}))
	require.NoError(t, r.Register(fakeCanon{name: "long", matchSet: []canon.Match{mk(0, 10, "<b>", canon.ClassTimestamps)}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Equal(t, []byte("<b>abcdef"), res.Canonical)
	require.Equal(t, []string{"long"}, res.Applied)
}

// TestOverlapResolution_EarlierOffsetWins asserts a match that starts inside an already-accepted
// span is dropped rather than truncated.
func TestOverlapResolution_EarlierOffsetWins(t *testing.T) {
	t.Parallel()

	in := []byte("0123456789abcdef")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "first", matchSet: []canon.Match{mk(0, 10, "<b>", canon.ClassTimestamps)}}))
	require.NoError(t, r.Register(fakeCanon{name: "inner", matchSet: []canon.Match{mk(4, 4, "<c>", canon.ClassTimestamps)}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Equal(t, []byte("<b>abcdef"), res.Canonical)
	require.Equal(t, []string{"first"}, res.Applied)
}

// TestOverlapResolution_RankBreaksTies asserts registration order is the final tie-breaker, so the
// result never depends on which canonicalizer happened to be asked first.
func TestOverlapResolution_RankBreaksTies(t *testing.T) {
	t.Parallel()

	in := []byte("0123456789")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "rank0", matchSet: []canon.Match{mk(0, 4, "<a>", canon.ClassTimestamps)}}))
	require.NoError(t, r.Register(fakeCanon{name: "rank1", matchSet: []canon.Match{mk(0, 4, "<b>", canon.ClassTimestamps)}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Equal(t, []byte("<a>456789"), res.Canonical)
	require.Equal(t, []string{"rank0"}, res.Applied)
}

// TestNonGrowingGuard_DropsGrowingMatch asserts 00-ARCHITECTURE.md §5.6's "no canonicalizer ever
// grows its input" is structural: a match whose token is longer than its span is dropped by the
// accept loop rather than trusted to fourteen separate implementations.
func TestNonGrowingGuard_DropsGrowingMatch(t *testing.T) {
	t.Parallel()

	in := []byte("abcdef")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "greedy", matchSet: []canon.Match{mk(1, 1, "<xxx>", canon.ClassPIDs)}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Equal(t, in, res.Canonical)
	require.Empty(t, res.Applied)
}

// TestAcceptLoop_DropsZeroLengthAndZeroClass asserts the two remaining structural rejections: a
// zero-length span is an insertion, which no canonicalizer may perform, and a Match carrying the
// zero Class is dropped by the gate — which is precisely why every built-in rule must be assigned
// a Class, and why TestMatcherClassAssigned exists.
func TestAcceptLoop_DropsZeroLengthAndZeroClass(t *testing.T) {
	t.Parallel()

	in := []byte("abcdef")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "insert", matchSet: []canon.Match{mk(2, 0, "", canon.ClassANSI)}}))
	require.NoError(t, r.Register(fakeCanon{name: "unclassed", matchSet: []canon.Match{{Offset: 4, Len: 2, Token: nil}}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Equal(t, in, res.Canonical)
	require.Empty(t, res.Applied)
}

// TestRun_StripGatesOptionalClasses is the central gating assertion: an empty (but non-nil) Strip
// turns off every optional class while the structural crlf and paths classes stay on, because
// neither can be disabled through Appendix C in the first place.
func TestRun_StripGatesOptionalClasses(t *testing.T) {
	t.Parallel()

	in := []byte("ts=2024-01-15T10:32:07Z\r\nnext\r\n")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "crlf", matchSet: []canon.Match{
		mk(23, 2, "\n", canon.ClassCRLF),
		mk(29, 2, "\n", canon.ClassCRLF),
	}}))
	require.NoError(t, r.Register(fakeCanon{name: "timestamps", matchSet: []canon.Match{
		mk(3, 20, "<ts>", canon.ClassTimestamps),
	}}))

	gated, err := r.Run("Bash", "", in, canon.Options{Strip: []canon.Class{}, KeepDeltas: true})
	require.NoError(t, err)
	require.Equal(t, "ts=2024-01-15T10:32:07Z\nnext\n", string(gated.Canonical),
		"the timestamp must survive while CRLF is still normalized")
	require.Equal(t, []string{"crlf"}, gated.Applied)

	full, err := r.Run("Bash", "", in, canon.Options{Strip: []canon.Class{canon.ClassTimestamps}, KeepDeltas: true})
	require.NoError(t, err)
	require.Equal(t, "ts=<ts>\nnext\n", string(full.Canonical))
	require.Equal(t, []string{"crlf", "timestamps"}, full.Applied)
}

// TestApplied_DeduplicatedRegistrationOrder asserts Applied names each contributing canonicalizer
// once, in registration order — the only ordering that is stable across inputs, given that
// composition is a single pass rather than a chain.
func TestApplied_DeduplicatedRegistrationOrder(t *testing.T) {
	t.Parallel()

	in := []byte("AAAABBBBCCCCDDDD")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "ansi", matchSet: []canon.Match{mk(4, 4, "", canon.ClassANSI)}}))
	require.NoError(t, r.Register(fakeCanon{name: "timestamps", matchSet: []canon.Match{
		mk(0, 4, "<ts>", canon.ClassTimestamps),
		mk(12, 4, "<ts>", canon.ClassTimestamps),
	}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Equal(t, "<ts>CCCC<ts>", string(res.Canonical))
	require.Equal(t, []string{"ansi", "timestamps"}, res.Applied)
}

// TestReduced_Value pins Result.Reduced to 1 - len(canonical)/len(input).
func TestReduced_Value(t *testing.T) {
	t.Parallel()

	in := make([]byte, 1000)
	for i := range in {
		in[i] = 'x'
	}
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "cut", matchSet: []canon.Match{mk(0, 100, "", canon.ClassANSI)}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Len(t, res.Canonical, 900)
	require.InDelta(t, 0.1, res.Reduced, 1e-9)
}

// TestSignature_OnlyWhenEnabled asserts Result.Signature is computed only when MinHash is enabled,
// and that when it is, it is exactly sketch.MinHash over the CANONICAL bytes — not the input.
//
// Both halves hold against SP-03's stub (which returns the zero Signature) and against its real
// implementation, because the test recomputes rather than hard-coding a value. That is what keeps
// this a Rule W-2-clean assertion against a same-wave sibling.
func TestSignature_OnlyWhenEnabled(t *testing.T) {
	t.Parallel()

	in := []byte("ok example.com/pkg 0.42s\n")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "durations", matchSet: []canon.Match{mk(19, 5, "<d>", canon.ClassDurations)}}))

	off, err := r.Run("Bash", "", in, canon.Options{})
	require.NoError(t, err)
	require.Zero(t, off.Signature.Perms)
	require.Empty(t, off.Signature.Mins)

	opts := canon.Options{MinHash: canon.MinHashOptions{
		Enabled:      true,
		Permutations: config.Defaults().Store.Canonicalize.MinHash.Permutations,
		ShingleSize:  canon.DefaultShingleSize,
	}}
	on, err := r.Run("Bash", "", in, opts)
	require.NoError(t, err)
	require.Equal(t, sketch.MinHash(on.Canonical, opts.MinHash), on.Signature)
}

// TestRun_LargeInputTailIsVerbatim asserts the MaxInputBytes policy: beyond the cap the remainder
// is appended untouched, and Delta offsets stay valid because the tail is concatenated after
// composition has already produced its coordinates.
func TestRun_LargeInputTailIsVerbatim(t *testing.T) {
	t.Parallel()

	tail := []byte("TAILTAILTAIL")
	in := make([]byte, 0, canon.MaxInputBytes+len(tail))
	for len(in) < canon.MaxInputBytes {
		in = append(in, 'x')
	}
	in = append(in, tail...)

	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "head", matchSet: []canon.Match{mk(0, 4, "", canon.ClassANSI)}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Len(t, res.Canonical, len(in)-4)
	require.Equal(t, tail, res.Canonical[len(res.Canonical)-len(tail):])

	restored, err := canon.Restore(res.Canonical, res.Deltas)
	require.NoError(t, err)
	require.Equal(t, in, restored)
}

// TestRun_DeltasAreAscendingAndDisjoint pins the invariant Restore's single forward pass depends
// on: deltas come back in ascending Offset order with no overlap.
func TestRun_DeltasAreAscendingAndDisjoint(t *testing.T) {
	t.Parallel()

	in := []byte("AAAABBBBCCCCDDDDEEEE")
	r := canon.NewRegistry()
	require.NoError(t, r.Register(fakeCanon{name: "many", matchSet: []canon.Match{
		mk(12, 4, "<c>", canon.ClassTimestamps),
		mk(0, 4, "<a>", canon.ClassTimestamps),
		mk(4, 4, "<b>", canon.ClassTimestamps),
	}}))

	res, err := r.Run("Bash", "", in, allOn())
	require.NoError(t, err)
	require.Len(t, res.Deltas, 3)
	for i := 1; i < len(res.Deltas); i++ {
		prev, cur := res.Deltas[i-1], res.Deltas[i]
		require.LessOrEqual(t, prev.Offset+prev.Len, cur.Offset,
			"delta %d must start at or after the end of delta %d", i, i-1)
	}

	restored, err := canon.Restore(res.Canonical, res.Deltas)
	require.NoError(t, err)
	require.Equal(t, in, restored)
}
