package analyzer_test

import (
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/stretchr/testify/require"
)

// TestQualification_TheZeroValueDoesNotBind is the regression guard for the one ordering decision
// in Qualification that cannot be recovered from by being careful.
//
// A Qualification is read in three places that can all skip it — a struct literal that omits the
// field, a map index without comma-ok, a decoder that never saw the key — and in every one of them
// Go supplies the zero value. If that zero value were QualCurrent, each of those slips would mint
// an AUTHORITATIVE elimination out of nothing, and a false already_tried that blocks a now-viable
// approach is the §12 High-severity direction: it turns negative knowledge from an asset into a
// liability. The first draft of block.go did exactly this with a plain map index.
//
// So the assertion is not "the constants have these numbers". It is "the value Go hands you when
// nobody said anything is a value that constrains nothing", which is the property every future
// caller depends on without knowing it.
func TestQualification_TheZeroValueDoesNotBind(t *testing.T) {
	var unset analyzer.Qualification
	require.False(t, unset.Active(),
		"an unset Qualification must never act as a binding constraint")
	require.Equal(t, analyzer.QualUncertain, unset,
		"QualUncertain must be the zero value; see the ordering note in types.go")

	var prov analyzer.Provenance
	require.False(t, prov.Qualification.Active(),
		"a Provenance built by literal without naming a qualification does not bind")

	var rep analyzer.Representation
	require.False(t, rep.Prov.Qualification.Active(),
		"a Representation built by literal without naming a qualification does not bind")
}

// TestQualification_OnlyCurrentIsActive pins the other half: exactly one of the three binds, and
// stale and uncertain records stay carried-but-non-binding rather than being dropped or promoted.
func TestQualification_OnlyCurrentIsActive(t *testing.T) {
	require.True(t, analyzer.QualCurrent.Active())
	require.False(t, analyzer.QualStale.Active())
	require.False(t, analyzer.QualUncertain.Active())
}

// TestQualification_StringsAreStable keeps the log and drop-report spellings from drifting with the
// numeric order, which this file has already changed once.
func TestQualification_StringsAreStable(t *testing.T) {
	require.Equal(t, "uncertain", analyzer.QualUncertain.String())
	require.Equal(t, "current", analyzer.QualCurrent.String())
	require.Equal(t, "stale", analyzer.QualStale.String())
}

// TestRepresentationKind_StringsAreStable does the same for the representation kinds, whose order
// is the deterministic tie-break's secondary key (contract §3): a reordering here would silently
// change which representation a tie resolves toward.
func TestRepresentationKind_StringsAreStable(t *testing.T) {
	require.Equal(t, "exact_span", analyzer.RepExactSpan.String())
	require.Equal(t, "capsule", analyzer.RepCapsule.String())
	require.Equal(t, "pointer", analyzer.RepPointer.String())
	require.Equal(t, "archive_only", analyzer.RepArchiveOnly.String())

	require.Less(t, analyzer.RepExactSpan, analyzer.RepCapsule,
		"the kinds run most faithful to least; the tie-break depends on it")
	require.Less(t, analyzer.RepCapsule, analyzer.RepPointer)
	require.Less(t, analyzer.RepPointer, analyzer.RepArchiveOnly)
}
