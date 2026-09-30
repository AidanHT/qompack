package contract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStandingVocabulary_PendingIsNothingSeen: every pending spelling is one the observation
// ledger's own table already reads as "nothing was seen", so a banner's pending count and the
// ledger's not_observed outcome can never disagree about a row.
func TestStandingVocabulary_PendingIsNothingSeen(t *testing.T) {
	t.Parallel()

	for s := range pendingSpellings {
		require.True(t, noObservationSpellings[s], "%q is pending but not in noObservationSpellings", s)
	}
}

// TestStandingVocabulary_EveryNothingSeenSpellingIsNotHolding: no "nothing was seen" spelling, and
// not the producer-absent one, can be counted as a contract that holds.
func TestStandingVocabulary_EveryNothingSeenSpellingIsNotHolding(t *testing.T) {
	t.Parallel()

	for s := range noObservationSpellings {
		require.NotEqual(t, StandingHolding, StandingOf(Result{OK: true, Observed: s}), "%q", s)
	}
	require.Equal(t, StandingIdle, StandingOf(Result{OK: true, Observed: notYetImplementedObserved}))
}
