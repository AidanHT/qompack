package config_test

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
)

// BenchmarkConfigLoad_ColdNoFiles measures Load with no files present in either layer (defaults
// plus an empty env and empty flags only) — the case every hook process pays on every invocation
// in later waves. Budget: under 2 ms/op.
func BenchmarkConfigLoad_ColdNoFiles(b *testing.B) {
	env := config.Env{
		ProjectRoot: b.TempDir(),
		HomeDir:     b.TempDir(),
		Getenv:      func(string) string { return "" },
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, _, err := config.Load(env); err != nil {
			b.Fatal(err)
		}
	}
}
