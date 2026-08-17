package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/stretchr/testify/require"
)

// TestKnownClasses_RegistrationOrder pins the eight §5.6 classes and their order, and asserts the
// returned slice is a copy a caller cannot use to mutate the package's own table.
func TestKnownClasses_RegistrationOrder(t *testing.T) {
	t.Parallel()

	want := []canon.Class{
		canon.ClassCRLF, canon.ClassANSI, canon.ClassTimestamps, canon.ClassDurations,
		canon.ClassPIDs, canon.ClassAddresses, canon.ClassTmpPaths, canon.ClassPaths,
	}
	require.Equal(t, want, canon.KnownClasses())

	got := canon.KnownClasses()
	got[0] = "clobbered"
	require.Equal(t, want, canon.KnownClasses(), "KnownClasses must hand back a copy")
}

// TestKnownClassesCoverConfigDefaults guards internal/config's own
// "strip values are known canon.Classes" rule across the package boundary: config validates the
// enum, canon owns the enum, and nothing else checks that the two agree.
func TestKnownClassesCoverConfigDefaults(t *testing.T) {
	t.Parallel()

	strip := config.Defaults().Store.Canonicalize.Strip
	require.NotEmpty(t, strip)
	for _, s := range strip {
		_, ok := canon.ParseClass(s)
		require.True(t, ok, "config default strip value %q does not parse as a canon.Class", s)
	}
}

// TestParseClass_Table asserts ParseClass is case-sensitive and matches Appendix C's spellings
// exactly — those strings are a wire format, appearing verbatim in every project's config.
func TestParseClass_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want canon.Class
		ok   bool
	}{
		{"tmpPaths", canon.ClassTmpPaths, true},
		{"tmppaths", "", false},
		{"TmpPaths", "", false},
		{"crlf", canon.ClassCRLF, true},
		{"paths", canon.ClassPaths, true},
		{"timestamps", canon.ClassTimestamps, true},
		{"bogus", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, ok := canon.ParseClass(tc.in)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestOptionsFrom_MapsAppendixC asserts OptionsFrom reads Appendix C's store.canonicalize block
// verbatim rather than duplicating its numbers, which §11.6 forbids: 0.9 is one of the literals
// the nomagic pass rejects outside internal/config/defaults.go.
func TestOptionsFrom_MapsAppendixC(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults().Store.Canonicalize
	o := canon.OptionsFrom(cfg, true)

	require.True(t, o.KeepDeltas)
	require.Len(t, o.Strip, len(cfg.Strip))
	for i, s := range cfg.Strip {
		require.Equal(t, canon.Class(s), o.Strip[i], "strip entry %d", i)
	}
	require.Equal(t, cfg.MinHash.Enabled, o.MinHash.Enabled)
	require.Equal(t, cfg.MinHash.Permutations, o.MinHash.Permutations)
	require.InDelta(t, cfg.MinHash.NearDupThreshold, o.MinHash.NearDupThreshold, 1e-12)
	require.Equal(t, canon.DefaultShingleSize, o.MinHash.ShingleSize)

	require.Equal(t, 128, o.MinHash.Permutations, "Appendix C pins permutations at 128")
	require.InDelta(t, 0.9, o.MinHash.NearDupThreshold, 1e-12, "Appendix C pins nearDupThreshold at 0.9")
	require.Len(t, o.Strip, 6, "Appendix C's strip list has six entries")
}

// TestOptionsFrom_SkipsUnknownStripValues asserts an unknown class in a loaded config is dropped
// here rather than raised: internal/config's Validate already reports it with the config path and
// the offending value, which is a far better diagnostic than anything this function could produce
// from a []Class it was handed.
func TestOptionsFrom_SkipsUnknownStripValues(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults().Store.Canonicalize
	cfg.Strip = []string{"ansi", "not-a-class", "pids"}

	o := canon.OptionsFrom(cfg, false)
	require.Equal(t, []canon.Class{canon.ClassANSI, canon.ClassPIDs}, o.Strip)
	require.False(t, o.KeepDeltas)
}

// TestOptionsFrom_StripIsNonNilWhenEmpty is the subtle half of the nil/empty distinction: a config
// that strips nothing must produce a non-nil, empty Strip, because a nil Strip means "every class"
// and would silently re-enable everything the user turned off.
func TestOptionsFrom_StripIsNonNilWhenEmpty(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults().Store.Canonicalize
	cfg.Strip = nil

	o := canon.OptionsFrom(cfg, false)
	require.NotNil(t, o.Strip)
	require.Empty(t, o.Strip)
}
