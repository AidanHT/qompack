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

// TestMode_MayAct pins §12.1: only ModeFull may act (additionalContext, customInstructions,
// scheduler-initiated checkpoints, the drop report). ModeDegradedPassive and ModeOff both refuse,
// for different reasons — one is a contract failure, the other an operator instruction — but the
// caller-facing answer is the same.
func TestMode_MayAct(t *testing.T) {
	require.True(t, contract.ModeFull.MayAct())
	require.False(t, contract.ModeDegradedPassive.MayAct())
	require.False(t, contract.ModeOff.MayAct())
}

// TestMode_MayRecord pins §12.1: ModeDegradedPassive still records — L0/L1 keep running, only the
// acting paths turn off — so only ModeOff refuses recording.
func TestMode_MayRecord(t *testing.T) {
	require.True(t, contract.ModeFull.MayRecord())
	require.True(t, contract.ModeDegradedPassive.MayRecord())
	require.False(t, contract.ModeOff.MayRecord())
}

// TestSeverity_ValuesAreOrdered asserts the three severities keep their declared order. RunAll
// compares Result.Severity against SevCritical by equality, but /qompack:status and future
// filtering read them as a ranking, so a reordering would be a silent behaviour change.
func TestSeverity_ValuesAreOrdered(t *testing.T) {
	require.Less(t, contract.SevInfo, contract.SevWarn)
	require.Less(t, contract.SevWarn, contract.SevCritical)
}
