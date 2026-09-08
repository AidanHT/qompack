package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// filterSeg is the segment every test below builds a filter for.
var filterSeg = store.Segment{ID: 12, Session: "sess-a", StartTurn: 4, EndTurn: 19}

// keySource returns a next func yielding keys, so a test can say what a segment holds in one line.
func keySource(keys ...string) func() ([]byte, bool, error) {
	i := 0
	return func() ([]byte, bool, error) {
		if i >= len(keys) {
			return nil, false, nil
		}
		k := keys[i]
		i++
		return []byte(k), true, nil
	}
}

// buildFilter builds a complete filter over keys at generation 1.
func buildFilter(t *testing.T, keys ...string) *store.SegmentFilter {
	t.Helper()
	f, err := store.BuildSegmentFilter(context.Background(), filterSeg, 1, 1000, keySource(keys...))
	require.NoError(t, err)
	return f
}

// TestSegmentFilter_APositiveIsOnlyEverACandidate pins the first of §3's two rules: there is no
// verdict meaning "present", so a caller cannot mistake a bloom hit for an answer.
func TestSegmentFilter_APositiveIsOnlyEverACandidate(t *testing.T) {
	t.Parallel()

	f := buildFilter(t, "alpha", "beta", "gamma")
	require.Equal(t, store.FilterMaybe, f.Lookup([]byte("alpha"), 1))
	require.False(t, store.FilterMaybe.SkipsSegment(), "a match never lets the caller skip the exact check")
	require.Equal(t, "maybe", store.FilterMaybe.String())
}

// TestSegmentFilter_AMissIsTrustedOnlyWithCompleteCurrentCoverage pins §3's second rule, which is
// the one that can produce a wrong answer if it is got wrong.
func TestSegmentFilter_AMissIsTrustedOnlyWithCompleteCurrentCoverage(t *testing.T) {
	t.Parallel()

	f := buildFilter(t, "alpha", "beta")
	require.True(t, f.Coverage.Complete)

	require.Equal(t, store.FilterMiss, f.Lookup([]byte("absent"), 1))
	require.True(t, store.FilterMiss.SkipsSegment())

	// A caller that believes the segment has moved on gets a bypass, not a miss.
	require.Equal(t, store.FilterBypass, f.Lookup([]byte("absent"), 2))
	require.Equal(t, store.FilterBypass, f.Lookup([]byte("absent"), 0))
}

// TestSegmentFilter_AnIncompleteFilterHasNoUsableNegatives pins that a partial build still helps —
// its positives narrow the search — while every negative over it is bypassed.
func TestSegmentFilter_AnIncompleteFilterHasNoUsableNegatives(t *testing.T) {
	t.Parallel()

	boom := errors.New("disk")
	i := 0
	f, err := store.BuildSegmentFilter(context.Background(), filterSeg, 1, 1000,
		func() ([]byte, bool, error) {
			i++
			if i == 1 {
				return []byte("alpha"), true, nil
			}
			return nil, false, boom
		})
	require.NoError(t, err, "a failing source ends the build, it does not fail the call")

	require.False(t, f.Coverage.Complete)
	require.Equal(t, store.IncompleteSourceError, f.Coverage.Incomplete)
	require.Equal(t, 1, f.Coverage.Keys)

	require.Equal(t, store.FilterMaybe, f.Lookup([]byte("alpha"), 1), "positives still narrow the search")
	require.Equal(t, store.FilterBypass, f.Lookup([]byte("absent"), 1), "negatives mean nothing")
}

// TestSegmentFilter_CancellationIsItsOwnIncompleteReason pins that a maintenance report can tell a
// cancelled build from a failing disk.
func TestSegmentFilter_CancellationIsItsOwnIncompleteReason(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f, err := store.BuildSegmentFilter(ctx, filterSeg, 1, 1000, keySource("alpha", "beta"))
	require.NoError(t, err)
	require.False(t, f.Coverage.Complete)
	require.Equal(t, store.IncompleteCancelled, f.Coverage.Incomplete)
	require.Zero(t, f.Coverage.Keys)
	require.Equal(t, store.FilterBypass, f.Lookup([]byte("alpha"), 1))
}

// TestSegmentFilter_OverCapacityIsPublishedIncompleteNotGrown pins the honest answer to §3's "a
// fixed filter cannot hold an unbounded insertion stream at a fixed error rate": the build stops
// at its cap and says the coverage is partial, rather than growing without bound or quietly
// degrading the rate it was sized for.
func TestSegmentFilter_OverCapacityIsPublishedIncompleteNotGrown(t *testing.T) {
	t.Parallel()

	const cap = 65536
	i := 0
	f, err := store.BuildSegmentFilter(context.Background(), filterSeg, 1, 1000,
		func() ([]byte, bool, error) {
			i++
			return []byte("k" + strconv.Itoa(i)), true, nil
		})
	require.NoError(t, err)

	require.Equal(t, cap, f.Coverage.Keys)
	require.False(t, f.Coverage.Complete)
	require.Equal(t, store.IncompleteOverCapacity, f.Coverage.Incomplete)
	require.Equal(t, store.FilterBypass, f.Lookup([]byte("nothing-like-this"), 1))
}

// TestSegmentFilter_ANilFilterBypasses pins that the absence of a filter costs a scan rather than
// producing a miss, and that the zero verdict is the safe one.
func TestSegmentFilter_ANilFilterBypasses(t *testing.T) {
	t.Parallel()

	var f *store.SegmentFilter
	require.Equal(t, store.FilterBypass, f.Lookup([]byte("anything"), 1))
	require.Equal(t, store.FilterBypass, store.FilterVerdict(0),
		"a verdict nobody computed costs a scan rather than skipping one")
	require.False(t, store.FilterVerdict(0).SkipsSegment())
	require.Equal(t, "bypass", store.FilterVerdict(99).String())
	require.Equal(t, sketch.BloomStats{}, f.Stats())
}

// TestSegmentFilter_BuildRefusesAMissingKeySource pins that a caller cannot get an empty filter by
// passing nothing, since an empty filter says "definitely not present" about everything.
func TestSegmentFilter_BuildRefusesAMissingKeySource(t *testing.T) {
	t.Parallel()

	_, err := store.BuildSegmentFilter(context.Background(), filterSeg, 1, 1000, nil)
	require.Error(t, err)
}

// TestSegmentFilter_RoundTripsCoverageWithItsBits pins that coverage and generation travel in the
// same file as the bits they describe, which is what "published atomically with its index" means.
func TestSegmentFilter_RoundTripsCoverageWithItsBits(t *testing.T) {
	t.Parallel()

	f := buildFilter(t, "alpha", "beta", "gamma")
	b, err := f.MarshalBinary()
	require.NoError(t, err)

	var got store.SegmentFilter
	require.NoError(t, got.UnmarshalBinary(b))
	require.Equal(t, f.Coverage, got.Coverage)
	require.Equal(t, store.FilterMaybe, got.Lookup([]byte("alpha"), 1))
	require.Equal(t, store.FilterMiss, got.Lookup([]byte("absent"), 1))

	require.Equal(t, core.SegmentID(12), got.Coverage.Segment)
	require.Equal(t, core.TurnIndex(4), got.Coverage.StartTurn)
	require.Equal(t, core.TurnIndex(19), got.Coverage.EndTurn)
	require.Equal(t, core.UnixMilli(1000), got.Coverage.BuiltAt)
}

// TestSegmentFilter_AnUnreadableFileIsNeverAnEmptyFilter is the decoding half of the same rule:
// every way a file can be unreadable is an error, because decoding one into an empty filter would
// turn corruption into a silent, total false negative.
func TestSegmentFilter_AnUnreadableFileIsNeverAnEmptyFilter(t *testing.T) {
	t.Parallel()

	good, err := buildFilter(t, "alpha").MarshalBinary()
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"no header newline", []byte(`{"v":1}`)},
		{"unparseable header", append([]byte("not json\n"), good...)},
		{"wrong version", append([]byte(`{"v":99,"segment":12}`+"\n"), good...)},
		{"truncated bits", good[:len(good)-4]},
		{"no bits at all", []byte(`{"v":1,"segment":12}` + "\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var f store.SegmentFilter
			require.Error(t, f.UnmarshalBinary(tc.data))
		})
	}
}

// TestSegmentFilter_MarshalRefusesAnEmptyFilter pins that the zero SegmentFilter cannot be written
// out at all, so there is no path to a file that answers "not present" to everything.
func TestSegmentFilter_MarshalRefusesAnEmptyFilter(t *testing.T) {
	t.Parallel()

	var f *store.SegmentFilter
	_, err := f.MarshalBinary()
	require.Error(t, err)

	_, err = (&store.SegmentFilter{}).MarshalBinary()
	require.Error(t, err)

	_, err = store.WriteSegmentFilter(t.TempDir(), nil)
	require.Error(t, err)
}

// TestSegmentFilterRef_IsStoreRelative pins that the reference recorded in an append-only log is
// portable: a store copied to another machine must still resolve its own filters.
func TestSegmentFilterRef_IsStoreRelative(t *testing.T) {
	t.Parallel()

	require.Equal(t, "sketches/seg-0012.bloom", store.SegmentFilterRef(12))
	require.Equal(t, "sketches/seg-0001.bloom", store.SegmentFilterRef(1))
	require.Equal(t, "sketches/seg-9999.bloom", store.SegmentFilterRef(9999))
	require.NotContains(t, store.SegmentFilterRef(12), ":", "no volume letter can appear in a ref")

	root := t.TempDir()
	require.Equal(t,
		filepath.Join(root, ".qompack", "sketches", "seg-0012.bloom"),
		store.SegmentFilterPath(root, 12))
}

// TestWriteAndReadSegmentFilter_RoundTripsThroughDisk pins the file half of publication, and that
// a missing filter and a corrupt one are distinguishable — "never built" is a different
// maintenance problem from "built and broken".
func TestWriteAndReadSegmentFilter_RoundTripsThroughDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))

	_, err := store.ReadSegmentFilter(root, filterSeg.ID)
	require.ErrorIs(t, err, core.ErrNotFound, "never built")

	ref, err := store.WriteSegmentFilter(root, buildFilter(t, "alpha", "beta"))
	require.NoError(t, err)
	require.Equal(t, "sketches/seg-0012.bloom", ref)

	got, err := store.ReadSegmentFilter(root, filterSeg.ID)
	require.NoError(t, err)
	require.Equal(t, store.FilterMaybe, got.Lookup([]byte("alpha"), 1))
	require.Equal(t, store.FilterMiss, got.Lookup([]byte("absent"), 1))
	require.Positive(t, got.Stats().Capacity)

	require.NoError(t, os.WriteFile(store.SegmentFilterPath(root, filterSeg.ID), []byte("garbage"), 0o600))
	_, err = store.ReadSegmentFilter(root, filterSeg.ID)
	require.ErrorIs(t, err, sketch.ErrMalformed, "built and broken")
}

// TestReadSegmentFilter_RefusesAFilterForAnotherSegment pins that a misfiled or hand-copied filter
// cannot be read as this segment's, since its negatives would then describe the wrong span.
func TestReadSegmentFilter_RefusesAFilterForAnotherSegment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))

	b, err := buildFilter(t, "alpha").MarshalBinary()
	require.NoError(t, err)
	// Filed under segment 13 while its coverage says 12.
	require.NoError(t, os.WriteFile(store.SegmentFilterPath(root, 13), b, 0o600))

	_, err = store.ReadSegmentFilter(root, 13)
	require.ErrorIs(t, err, sketch.ErrMalformed)
}
