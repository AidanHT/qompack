package canontest

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the canontest suite (00-ARCHITECTURE.md §5.22
// table; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md): idempotence,
// Restore∘Canonicalize == identity under KeepDeltas, never growing the input, and registration
// order preservation. All four are authored now, gated behind the same Rule W-1 stub probe as the
// rest of the suite, so SP-04 inherits them rather than writing its own grader.

// canonicalizeFixture is a generic, moderately volatile-looking text blob: it does not depend on
// which of the twelve real canonicalizers SP-04 registers actually fires on it, because the three
// structural properties below (idempotence, restore-identity, non-growth) must hold for the
// Registry as a whole regardless of which specific canonicalizers touch this particular fixture.
const canonicalizeFixture = `$ go test ./...
ok  	example.com/pkg	0.42s
process 41823 exited at 2024-01-15T10:32:07Z after 00:02:17
tmp file: /tmp/build-8f3a21/output.log
`

// runIdempotenceCase asserts Canonicalize(Canonicalize(x)) == Canonicalize(x) at the byte level
// (00-ARCHITECTURE.md §5.6): re-running canonicalization over already-canonical content must be a
// no-op.
func runIdempotenceCase(t *testing.T, factory func(t *testing.T) canon.Registry) {
	t.Helper()
	r := factory(t)

	res1, err := r.Run("bash", "", []byte(canonicalizeFixture), canon.Options{})
	require.NoError(t, err)

	res2, err := r.Run("bash", "", res1.Canonical, canon.Options{})
	require.NoError(t, err)

	require.Equal(t, res1.Canonical, res2.Canonical,
		"Canonicalize(Canonicalize(x)) must equal Canonicalize(x)")
}

// runRestoreRoundTripCase asserts Restore(Canonicalize(x, KeepDeltas).Canonical, .Deltas) == x
// (00-ARCHITECTURE.md §5.6): a byte-exact inverse whenever deltas were kept.
func runRestoreRoundTripCase(t *testing.T, factory func(t *testing.T) canon.Registry) {
	t.Helper()
	r := factory(t)

	in := []byte(canonicalizeFixture)
	res, err := r.Run("bash", "", in, canon.Options{KeepDeltas: true})
	require.NoError(t, err)

	restored, err := canon.Restore(res.Canonical, res.Deltas)
	require.NoError(t, err)
	require.Equal(t, in, restored, "Restore(Canonicalize(x, KeepDeltas).Canonical, .Deltas) must equal x")
}

// runNeverGrowsCase asserts no canonicalizer ever grows its input (00-ARCHITECTURE.md §5.6): the
// Registry's combined output must never exceed the input's length.
func runNeverGrowsCase(t *testing.T, factory func(t *testing.T) canon.Registry) {
	t.Helper()
	r := factory(t)

	in := []byte(canonicalizeFixture)
	res, err := r.Run("bash", "", in, canon.Options{})
	require.NoError(t, err)

	require.LessOrEqual(t, len(res.Canonical), len(in),
		"no canonicalizer may grow its input (§5.6)")
}

// fakeRegistrationOrderCanonicalizer is a minimal Canonicalizer used only to probe Registry
// bookkeeping (registration order), not any real stripping behaviour: it always applies and
// echoes its input unchanged.
type fakeRegistrationOrderCanonicalizer struct{ name string }

func (c fakeRegistrationOrderCanonicalizer) Name() string                   { return c.name }
func (c fakeRegistrationOrderCanonicalizer) Applies(tool, path string) bool { return true }
func (c fakeRegistrationOrderCanonicalizer) Canonicalize(in []byte, o canon.Options) (canon.Result, error) {
	return canon.Result{Canonical: in, Applied: []string{c.name}}, nil
}

// runRegistrationOrderPreservedCase asserts Names and For report registered Canonicalizers in
// registration order (00-ARCHITECTURE.md §5.6: "For(tool, path string) []Canonicalizer //
// deterministic order (registration order)"), not lexical or any other order — registering
// deliberately out-of-lexical-order names ("zzz-first-probe" before "aaa-second-probe") makes an
// order-preserving implementation and a lexically-sorting one produce different, distinguishable
// results.
//
// factory's Registry is not required to start empty — a factory built from canon.Default, for
// example, arrives with SP-04's twelve real canonicalizers already registered — so this asserts
// only that the probes appear as an in-order SUBSEQUENCE of whatever Names()/For() report, not
// that they are the only entries. The probe names are deliberately distinctive so they cannot
// collide with any real canonicalizer's own name.
func runRegistrationOrderPreservedCase(t *testing.T, factory func(t *testing.T) canon.Registry) {
	t.Helper()
	r := factory(t)

	names := []string{"zzz-first-probe", "aaa-second-probe", "mmm-third-probe"}
	for _, n := range names {
		require.NoError(t, r.Register(fakeRegistrationOrderCanonicalizer{name: n}))
	}

	requireInOrderSubsequence(t, names, r.Names(), "Names")

	applicable := r.For("any-tool", "any/path")
	applicableNames := make([]string, len(applicable))
	for i, c := range applicable {
		applicableNames[i] = c.Name()
	}
	requireInOrderSubsequence(t, names, applicableNames, "For")
}

// requireInOrderSubsequence asserts every element of want appears in got, in the same relative
// order, though got may also contain other elements interleaved among them.
func requireInOrderSubsequence(t *testing.T, want, got []string, label string) {
	t.Helper()
	idx := 0
	for _, g := range got {
		if idx < len(want) && g == want[idx] {
			idx++
		}
	}
	require.Equal(t, len(want), idx,
		"%s must contain %v as an in-order subsequence; got %v", label, want, got)
}
