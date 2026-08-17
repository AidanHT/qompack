package chunk

import "encoding/binary"

// This file is the bridge between package chunk's unexported state and the external chunk_test
// package. It exists for one specific reason: internal/testutil (which owns the -update golden
// machinery every golden test in this repository shares) transitively imports internal/store, and
// store imports chunk — so an INTERNAL test file (package chunk) that imported testutil would
// create an import cycle and fail to build. An EXTERNAL test file (package chunk_test) has no such
// problem, but it cannot see the gear table.
//
// The classic export_test.go pattern resolves that: identifiers declared here are compiled into
// package chunk only for the test binary, are visible to chunk_test, and do not exist in the
// shipped package at all.

// GearTableBytes returns the gear table encoded as 256 little-endian uint64s in index order. It is
// the exact preimage TestGearTableGolden digests, so the golden pins both the table's values and
// their order.
func GearTableBytes() []byte {
	buf := make([]byte, 0, len(gear)*8)
	var enc [8]byte
	for _, g := range gear {
		binary.LittleEndian.PutUint64(enc[:], g)
		buf = append(buf, enc[:]...)
	}
	return buf
}
