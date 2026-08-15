package observer

import (
	"fmt"
	"strconv"

	"github.com/qompack/qompack/internal/store"
)

// bytesPerKB is the binary divisor humanBytes renders sizes with: 1 KB is 1024 bytes, not 1000.
// The distinction is load-bearing rather than pedantic — 2457 bytes renders as "2.4KB" at 1024
// and "2.5KB" at 1000, and Qompack.md §8.1's example tombstone says 2.4KB, so a decimal divisor
// would silently break the golden.
const bytesPerKB = 1024 //nomagic:allow binary byte-unit divisor for size rendering, not store.chunk.min

// sizeUnits are the suffixes humanBytes steps through above bytesPerKB. A tool result larger than
// the last one is rendered in that unit rather than overflowing into an unnamed one.
var sizeUnits = [...]string{"KB", "MB", "GB"}

// Tombstone renders the addressable marker of Qompack.md §8.1 item 2: the one-line trace a
// cleared tool result leaves behind, carrying enough identity to fetch the content back.
//
//	[cleared: sha256:a3f2c9e14b70 · 2.4KB · FileRead src/auth.ts · re-expandable]
//
// It is fully specified by the architecture, so SP-01 implements it for real rather than stubbing
// it (§14.1 rule 3 of plans/V1-SP-01-foundation-toolchain-and-contracts.md). The point of the
// marker is that clearing a result is reversible: the short hash is a real store address, so
// `expand` can retrieve exactly what was cleared. A tombstone that merely said "[cleared]" would
// turn a cache eviction into data loss.
//
// The separator is U+00B7 MIDDLE DOT with a space on each side, the hash carries its "sha256:"
// prefix, and the short form is the first 12 hex characters — the same 12 core.Hash.Short
// produces, so a tombstone can be pasted straight back into a retrieval call.
func Tombstone(rec store.ToolUseRecord) string {
	return fmt.Sprintf("[cleared: sha256:%s · %s · %s %s · re-expandable]",
		rec.Root.Short(), humanBytes(rec.Bytes), rec.Tool, rec.Path)
}

// humanBytes renders n as a compact size with one decimal place and no space before the unit:
// under one KB as a whole number of bytes ("973B"), and above it in KB, MB or GB scaled by
// bytesPerKB ("2.4KB"). Sizes at or beyond the largest unit stay in that unit.
func humanBytes(n int64) string {
	if n < bytesPerKB {
		return strconv.FormatInt(n, 10) + "B"
	}
	v := float64(n) / bytesPerKB
	i := 0
	for i < len(sizeUnits)-1 && v >= bytesPerKB {
		v /= bytesPerKB
		i++
	}
	return fmt.Sprintf("%.1f%s", v, sizeUnits[i])
}
