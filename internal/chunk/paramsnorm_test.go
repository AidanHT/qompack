package chunk_test

import (
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/stretchr/testify/require"
)

// TestParamsNormalized_Table pins Params.Normalized's clamping rules (00-ARCHITECTURE.md §5.5).
// Normalized is deliberately separate from Validate: Validate is the *config* gate (0 < Min <
// Target < Max, which internal/config's own range tags agree with) and must reject nothing a valid
// qompack.json can express, whereas Normalized is the *algorithm* gate — it turns any Validate-
// passing triple into one FastCDC can actually run with (power-of-two Target for the mask
// derivation, Min >= GearWindow for the priming window, Max in a sane multiple of Target).
func TestParamsNormalized_Table(t *testing.T) {
	t.Parallel()

	const (
		wantTarget = 4096
		wantMin    = 512
		wantMax    = 16384
	)

	cases := []struct {
		name string
		in   chunk.Params
		want chunk.Params
	}{
		{
			// Target 5000 is not a power of two: it rounds DOWN to 4096 so the mask derivation
			// (TrailingZeros64) is exact. Min 0 floors to Target/8. Max 0 defaults to 4*Target.
			name: "zero_min_and_max_with_non_power_of_two_target",
			in:   chunk.Params{Min: 0, Target: 5000, Max: 0},
			want: chunk.Params{Min: wantMin, Target: wantTarget, Max: wantMax},
		},
		{
			// Min 32 is below GearWindow and below Target/8, so it rises to Target/8 = 512.
			// Max 1000 is below 2*Target, so it rises to 2*Target = 8192.
			name: "min_below_gear_window_and_max_below_floor",
			in:   chunk.Params{Min: 32, Target: 4096, Max: 1000},
			want: chunk.Params{Min: wantMin, Target: wantTarget, Max: 2 * wantTarget},
		},
		{
			// Min 8192 exceeds Target, which would leave nextCut with no scan window at all; the
			// documented recovery is Min = Target/4.
			name: "min_at_or_above_target_falls_back_to_quarter_target",
			in:   chunk.Params{Min: 8192, Target: 4096, Max: 16384},
			want: chunk.Params{Min: wantTarget / 4, Target: wantTarget, Max: wantMax},
		},
		{
			// The built-in defaults are already normal: Normalized must be the identity on them,
			// or every existing store would re-chunk the first time this ran.
			name: "defaults_are_a_fixed_point",
			in:   chunk.DefaultParams(),
			want: chunk.DefaultParams(),
		},
		{
			// The wholly zero value: Target falls back to the built-in default and the rest follows.
			name: "zero_value",
			in:   chunk.Params{},
			want: chunk.Params{Min: wantMin, Target: wantTarget, Max: wantMax},
		},
		{
			// Target below the floor clamps up to 256, which is itself a power of two, so the
			// result stays mask-derivable.
			name: "tiny_target_clamps_to_floor",
			in:   chunk.Params{Min: 1, Target: 3, Max: 4},
			want: chunk.Params{Min: 64, Target: 256, Max: 512},
		},
		{
			// Target above the ceiling clamps down to 1<<20, also a power of two.
			name: "huge_target_clamps_to_ceiling",
			in:   chunk.Params{Min: 1 << 30, Target: 1 << 30, Max: 1 << 30},
			want: chunk.Params{Min: 1 << 18, Target: 1 << 20, Max: 1 << 24},
		},
		{
			// Max above 16*Target clamps down, so a config cannot ask for an unbounded
			// force-cut distance (and therefore an unbounded SplitStream buffer).
			name: "max_above_ceiling_clamps_down",
			in:   chunk.Params{Min: 1024, Target: 4096, Max: 1 << 24},
			want: chunk.Params{Min: 1024, Target: wantTarget, Max: 16 * wantTarget},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.in.Normalized()
			require.Equal(t, tc.want, got)

			// Whatever went in, what comes out must satisfy the invariants nextCut relies on.
			require.NoError(t, got.Validate(), "Normalized must always produce a Validate-clean Params")
			require.GreaterOrEqual(t, got.Min, chunk.GearWindow,
				"Min must never fall below the priming window, or nextCut would index before data[0]")
			require.Equal(t, got.Target&(got.Target-1), 0, "Target must be a power of two")
			require.Equal(t, got, got.Normalized(), "Normalized must be idempotent")
		})
	}
}

// TestNewClampsInvalidParams asserts New silently normalizes rather than rejecting: New has no
// error return (00-ARCHITECTURE.md §5.5), so a Params that would make FastCDC undefined has to be
// repaired, not refused — and the repaired values must be readable back through ParamsReporter so
// a caller can tell what it actually got.
func TestNewClampsInvalidParams(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   chunk.Params
		want chunk.Params
	}{
		{"zero_value", chunk.Params{}, chunk.Params{Min: 512, Target: 4096, Max: 16384}},
		{"negative", chunk.Params{Min: -1, Target: -1, Max: -1}, chunk.Params{Min: 512, Target: 4096, Max: 16384}},
		{"inverted", chunk.Params{Min: 99999, Target: 8, Max: 1}, chunk.Params{Min: 64, Target: 256, Max: 512}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := chunk.New(tc.in)
			require.NotNil(t, c)

			reporter, ok := c.(chunk.ParamsReporter)
			require.True(t, ok, "chunk.New must return a Chunker that can report its effective Params")
			require.Equal(t, tc.want, reporter.Params())

			// And the clamped Chunker must actually work, not merely construct.
			data := pseudoRandomBytes(31, 100000)
			chunks := c.Split(data)
			require.NotEmpty(t, chunks)
			requireContiguous(t, chunks, len(data))
		})
	}
}

// TestFromConfig asserts FromConfig reads store.chunk verbatim and does not normalize on the way
// out. The split of responsibility matters: `qompack config` and the config validator must be able
// to show the user the numbers their file actually says, and only New — the thing that has to run
// the algorithm — is allowed to move them.
func TestFromConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	require.Equal(t, chunk.DefaultParams(), chunk.FromConfig(cfg),
		"FromConfig(config.Defaults()) must equal DefaultParams()")

	cfg.Store.Chunk.Min = 2000
	cfg.Store.Chunk.Target = 9000
	cfg.Store.Chunk.Max = 30000
	require.Equal(t, chunk.Params{Min: 2000, Target: 9000, Max: 30000}, chunk.FromConfig(cfg),
		"FromConfig must copy store.chunk verbatim, without normalizing")

	// New, in contrast, does normalize the same config.
	reporter, ok := chunk.New(chunk.FromConfig(cfg)).(chunk.ParamsReporter)
	require.True(t, ok)
	require.Equal(t, chunk.Params{Min: 2000, Target: 8192, Max: 30000}, reporter.Params())
}
