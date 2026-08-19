package canon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
)

// benchTargetBytes is how much input the 100 KB benchmarks feed the registry. Qompack.md §8.1
// sizes the hot path around a tool result of roughly this order, and gives the whole PostToolUse
// hook a 15 ms p99 budget of which canonicalization is one part.
const benchTargetBytes = 100 << 10

// benchInput reads one corpus file and repeats it until it is at least n bytes. n of 0 asks for
// the file exactly as captured.
//
// Repeating rather than padding matters: the registry's cost is dominated by regex scanning, and
// scanning 100 KB of realistic log lines is a completely different workload from scanning 100 KB
// of one repeated character.
func benchInput(b *testing.B, rel string, n int) []byte {
	b.Helper()

	base, err := os.ReadFile(filepath.Join(corpusRoot, filepath.FromSlash(rel))) //nolint:gosec // repo testdata
	if err != nil {
		// The "platform: " prefix is mandatory, not decorative: devtool lint's stubskips sub-check
		// hard-fails any skip whose reason matches none of the three permitted messages, and this
		// one carried none. It fires only when the repo's own testdata is unreadable — a checkout
		// or a sandbox problem rather than a benchmark problem — which is exactly what the
		// platform class is for.
		b.Skipf("platform: corpus file %s is unavailable: %v", rel, err)
	}
	if len(base) == 0 {
		b.Fatalf("corpus file %s is empty", rel)
	}
	if n <= len(base) {
		return base
	}
	out := make([]byte, 0, n+len(base))
	for len(out) < n {
		out = append(out, base...)
	}
	return out
}

// BenchmarkRun_Bash100KB is the noisiest realistic hot-path case: an npm install log, with ANSI,
// progress carriage returns, durations and temp paths in the same bytes. Budget: < 3 ms/op.
func BenchmarkRun_Bash100KB(b *testing.B) {
	in := benchInput(b, "bash/npm-install.txt", benchTargetBytes)
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := canon.OptionsFrom(config.Defaults().Store.Canonicalize, false)

	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Run("Bash", "", in, o); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRun_GoTest is the common case a coding session actually produces. Budget: < 1 ms/op.
func BenchmarkRun_GoTest(b *testing.B) {
	in := benchInput(b, "testrunner/go-test-pass.txt", 0)
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := canon.OptionsFrom(config.Defaults().Store.Canonicalize, false)

	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Run("Bash", "", in, o); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRun_KeepDeltas measures the cost of the byte-exact side record, which SP-06 pays only
// when it needs recoverability. It is here so that cost is visible rather than assumed.
func BenchmarkRun_KeepDeltas(b *testing.B) {
	in := benchInput(b, "bash/npm-install.txt", benchTargetBytes)
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := canon.OptionsFrom(config.Defaults().Store.Canonicalize, true)

	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Run("Bash", "", in, o); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRestore_100KB measures the inverse, which SP-06 pays on every read that needs the
// original bytes back. Budget: < 1 ms/op.
func BenchmarkRestore_100KB(b *testing.B) {
	in := benchInput(b, "bash/npm-install.txt", benchTargetBytes)
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := canon.OptionsFrom(config.Defaults().Store.Canonicalize, true)

	res, err := r.Run("Bash", "", in, o)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(res.Canonical)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := canon.Restore(res.Canonical, res.Deltas); err != nil {
			b.Fatal(err)
		}
	}
}
