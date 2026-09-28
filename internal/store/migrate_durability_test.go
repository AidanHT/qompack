package store

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// TestLegacyImportGate_StaysClosedUntilTheImportIsDurable holds a durability gap to the gate that
// already keeps it unreachable (w6-ckptsync review finding 5). The legacy import appends its mapping
// line without a sync and then commits its cursor past it durably, and RecordNewFormatWrite appends
// its line without a sync before the durable handoff that records it; the imported objects get no
// publication pass. None of that is reachable while LegacyImportGate ships closed. When SP-20 M1-04
// opens it, this test fails until the barriers land — and whoever makes them durable replaces it with
// the counting tests that pin them.
func TestLegacyImportGate_StaysClosedUntilTheImportIsDurable(t *testing.T) {
	require.False(t, config.LegacyImportGate().Passed,
		"the legacy import cannot ship open while its mapping line (migrate.go importOne) and new-format "+
			"write line (RecordNewFormatWrite) are appended without a sync before the durable cursor and "+
			"handoff that depend on them, and its imported objects get no publication pass: make them "+
			"durable, pin that with counting tests, then retire this test")
}
