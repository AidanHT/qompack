package chunk_test

import (
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/stretchr/testify/require"
)

// TestParamsValidate pins chunk.Params.Validate to the 0 < Min < Target < Max rule
// (00-ARCHITECTURE.md §5.5). Unlike every other method in this package, Validate is real (not a
// stub), so this test runs unconditionally — it is never gated behind a Rule W-1 skip.
func TestParamsValidate(t *testing.T) {
	cases := []struct {
		name    string
		p       chunk.Params
		wantErr bool
	}{
		{"defaults_are_valid", chunk.DefaultParams(), false},
		{"ordered_ok", chunk.Params{Min: 1, Target: 2, Max: 3}, false},
		{"min_zero", chunk.Params{Min: 0, Target: 2, Max: 3}, true},
		{"min_negative", chunk.Params{Min: -1, Target: 2, Max: 3}, true},
		{"target_equals_min", chunk.Params{Min: 2, Target: 2, Max: 3}, true},
		{"target_less_than_min", chunk.Params{Min: 5, Target: 2, Max: 10}, true},
		{"max_equals_target", chunk.Params{Min: 1, Target: 2, Max: 2}, true},
		{"max_less_than_target", chunk.Params{Min: 1, Target: 5, Max: 2}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestDefaultParams_MatchesConfig asserts DefaultParams reads config.Defaults().Store.Chunk
// verbatim, rather than duplicating its numbers as local literals (D11, §11.6): the daemon is
// expected to override via New(p) once a project's own config.Config is loaded, so DefaultParams
// itself must stay pinned to the built-in defaults.
func TestDefaultParams_MatchesConfig(t *testing.T) {
	want := config.Defaults().Store.Chunk
	got := chunk.DefaultParams()
	require.Equal(t, want.Min, got.Min)
	require.Equal(t, want.Target, got.Target)
	require.Equal(t, want.Max, got.Max)
	require.NoError(t, got.Validate(), "config.Defaults().Store.Chunk must itself be a valid Params")
}
