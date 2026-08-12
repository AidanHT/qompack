package paths_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/paths"
)

// benchPayloadBytes is one page-ish write, the size the plan names for the baseline entry. It is a
// benchmark input, not a configuration value, so it is deliberately a literal here.
const benchPayloadBytes = 4096

// BenchmarkPathsWriteAtomic_4KB measures the write-temp / fsync / rename cycle that every
// non-append-only file in the store goes through (§4.6).
//
// It matters to budget B-A because the atomic write is the durability cost the hot path cannot
// avoid: state/, config-violations.json and the sketch files all pay it. The fsync dominates and
// is filesystem-bound, so this number is expected to vary far more across machines than the other
// three baseline entries — a regression here is worth checking against the host before the code.
func BenchmarkPathsWriteAtomic_4KB(b *testing.B) {
	dir := b.TempDir()
	p := filepath.Join(dir, "payload.bin")
	payload := bytes.Repeat([]byte("q"), benchPayloadBytes)

	b.SetBytes(benchPayloadBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := paths.WriteAtomic(p, payload, 0o600); err != nil {
			b.Fatal(err)
		}
	}
}
