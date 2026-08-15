package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/config"
)

// FuzzConfigLoad feeds arbitrary bytes as the project config file. Load must never panic, and
// whatever Config it returns must already satisfy its own Validate() — a fuzz-discovered input
// that produced a violating config would mean the fallback loop itself is buggy, not merely that
// the input was bad.
func FuzzConfigLoad(f *testing.F) {
	seeds := []string{
		`{"scheduler":{"softFloorPct":0.6}}`,
		`{`,
		`{"store":{"chunk":{"min":99999999}}}`,
		`{"runtime":{"telemetry":{"enabled":true}}}`,
		`null`,
		`[]`,
		`"just a string"`,
		`{"scheduler":{"youngDaly":{"measuredDeltaSeconds":null}}}`,
		`{"a":{"b":{"c":{"d":{"e":1}}}}}`,
		``,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	for _, name := range []string{
		"minimal.jsonc", "comments.jsonc", "trailing-commas.jsonc", "malformed.jsonc", "unknown-keys.jsonc",
	} {
		if b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpora", "config", name)); err == nil {
			f.Add(b)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		root := t.TempDir()
		home := t.TempDir()
		dir := filepath.Join(root, ".qompack")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, prov, warns, err := config.Load(config.Env{
			ProjectRoot: root,
			HomeDir:     home,
			Getenv:      func(string) string { return "" },
			Flags:       nil,
		})
		if err != nil {
			t.Fatalf("Load returned an error for a non-empty ProjectRoot: %v", err)
		}
		if v := cfg.Validate(); len(v) != 0 {
			t.Fatalf("Load must return an already-validated config; got violations: %+v", v)
		}
		if prov == nil {
			t.Fatal("Load must return a non-nil Provenance on success")
		}
		_ = warns

		// JSONSchema and Get must also tolerate whatever Config shape resulted without panicking.
		_ = cfg.JSONSchema()
		_, _ = cfg.Get("scheduler.cache.readMultiplier")
	})
}
