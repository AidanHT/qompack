package contract_test

import (
	"testing"

	"github.com/qompack/qompack/internal/contract"
	"github.com/stretchr/testify/require"
)

// TestMode_StringIsFrozen pins the three spellings §12 uses in prose and /qompack:status prints.
// They are also the on-disk spelling in state/contract.json, so this test is a data-format
// assertion, not a cosmetic one.
func TestMode_StringIsFrozen(t *testing.T) {
	require.Equal(t, "full", contract.ModeFull.String())
	require.Equal(t, "degraded-passive", contract.ModeDegradedPassive.String())
	require.Equal(t, "off", contract.ModeOff.String())
}

// TestMode_StringOfUnknownValue asserts Mode.String is total: an out-of-range Mode renders
// "unknown" rather than an empty string, so a corrupt persisted value can never produce a log line
// or status banner with a blank mode in it.
func TestMode_StringOfUnknownValue(t *testing.T) {
	require.Equal(t, "unknown", contract.Mode(99).String())
}

// TestSeverity_ValuesAreOrdered asserts the three severities keep their declared order. RunAll
// compares Result.Severity against SevCritical by equality, but /qompack:status and future
// filtering read them as a ranking, so a reordering would be a silent behaviour change.
func TestSeverity_ValuesAreOrdered(t *testing.T) {
	require.Less(t, contract.SevInfo, contract.SevWarn)
	require.Less(t, contract.SevWarn, contract.SevCritical)
}
