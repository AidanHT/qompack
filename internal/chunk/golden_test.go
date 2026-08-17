package chunk_test

import (
	"crypto/sha256"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/testutil"
)

// TestGearTableGolden freezes the gear table. The digest is taken over its 256 entries in index
// order, each encoded little-endian (see chunk.GearTableBytes in export_test.go), so the golden
// pins both the values and their order.
//
// Treat a diff here as a wire-format break, never as a golden to refresh. The table is generated at
// init from a single frozen seed rather than committed as a 4 KB literal, which makes it cheap to
// verify and impossible to typo — but it also means a one-character change to gearSeed silently
// moves every boundary the chunker will ever find. That re-chunks every object in every existing
// store: every root in index/roots.jsonl becomes unreachable and every chunk in the CAS becomes
// garbage, with no migration short of re-ingesting every session. This golden is the tripwire.
func TestGearTableGolden(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256(chunk.GearTableBytes())
	var digest core.Hash
	copy(digest[:], sum[:])

	testutil.Golden(t, "gear-table.sha256", []byte(digest.String()+"\n"))
}
