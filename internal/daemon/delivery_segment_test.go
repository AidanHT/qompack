package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// These tests pin the corrected segment-authority contract main required: an immutable chained
// transition log + an atomic head (not a self-hash), an active-0 record committed before any migration
// evidence, head-loss/regression detection, and refusal (never silent legacy) when evidence survives
// without an authority.

func segRoot(i uint64) string { return fmt.Sprintf("%064x", i) }

// TestDeliverySegments_InitActiveZeroAndReopen: a fresh tree has no authority; initActiveZero commits
// the active-0 record, and a reopen recovers it as an existing authority at active 0.
func TestDeliverySegments_InitActiveZeroAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, exists, err := openDeliverySegments(dir)
	require.NoError(t, err)
	require.False(t, exists, "a fresh tree has no authority")
	require.NoError(t, s.initActiveZero())
	require.Equal(t, uint64(0), s.activeSeg())
	require.NoError(t, s.close())

	s2, exists, err := openDeliverySegments(dir)
	require.NoError(t, err)
	require.True(t, exists, "the committed active-0 authority is recovered")
	require.Equal(t, uint64(0), s2.activeSeg())
	require.Equal(t, "", s2.baseRootHex())
	require.NoError(t, s2.close())
}

// TestDeliverySegments_SeventyTransitionsAndReopen commits well past any historical cap and recovers the
// latest active segment and its base root after a restart.
func TestDeliverySegments_SeventyTransitionsAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, _, err := openDeliverySegments(dir)
	require.NoError(t, err)
	require.NoError(t, s.initActiveZero())
	const n = 72
	for i := uint64(1); i <= n; i++ {
		require.NoError(t, s.commitTransition(i, segRoot(i)))
		require.Equal(t, i, s.activeSeg())
	}
	require.NoError(t, s.close())

	s2, exists, err := openDeliverySegments(dir)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, uint64(n), s2.activeSeg(), "the latest active segment survives %d transitions", n)
	require.Equal(t, segRoot(n), s2.baseRootHex())
	require.NoError(t, s2.close())
}

// TestDeliverySegments_HeadLossWithLogIsUnavailable is the MANDATORY correction: an authority log
// (migration evidence) present with a lost head must refuse the open — never reopen legacy and re-mint.
func TestDeliverySegments_HeadLossWithLogIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	s, _, err := openDeliverySegments(dir)
	require.NoError(t, err)
	require.NoError(t, s.initActiveZero())
	require.NoError(t, s.commitTransition(1, segRoot(1)))
	head := s.headPath
	require.NoError(t, s.close())

	require.NoError(t, os.Remove(paths.Long(head)))
	_, exists, err := openDeliverySegments(dir)
	require.ErrorIs(t, err, errSegmentUnavailable, "a surviving log with a lost head is head loss, not legacy")
	require.False(t, exists)
}

// TestDeliverySegments_CompleteTailAdoptedNeverReverts: a crash after a transition's record was appended
// but before its head write lands is adopted forward — the active segment never silently reverts to the
// lower head.
func TestDeliverySegments_CompleteTailAdoptedNeverReverts(t *testing.T) {
	dir := t.TempDir()
	s, _, err := openDeliverySegments(dir)
	require.NoError(t, err)
	require.NoError(t, s.initActiveZero())
	require.NoError(t, s.commitTransition(1, segRoot(1)))
	headAfter1, err := os.ReadFile(paths.Long(s.headPath))
	require.NoError(t, err)
	require.NoError(t, s.commitTransition(2, segRoot(2)))
	require.NoError(t, s.close())

	require.NoError(t, os.WriteFile(paths.Long(s.headPath), headAfter1, 0o600)) // roll head back to seq 1
	s2, exists, err := openDeliverySegments(dir)
	require.NoError(t, err, "a complete chained tail beyond the head is adopted")
	require.True(t, exists)
	require.Equal(t, uint64(2), s2.activeSeg(), "adoption moves forward, never reverts to the lower head")
	require.Equal(t, segRoot(2), s2.baseRootHex())
	require.NoError(t, s2.close())
}

// TestDeliverySegments_TornTailPreservedAndUnavailable: a torn partial transition beyond the head
// refuses the open and preserves the bytes.
func TestDeliverySegments_TornTailPreservedAndUnavailable(t *testing.T) {
	dir := t.TempDir()
	s, _, err := openDeliverySegments(dir)
	require.NoError(t, err)
	require.NoError(t, s.initActiveZero())
	require.NoError(t, s.commitTransition(1, segRoot(1)))
	logPath := s.logPath
	require.NoError(t, s.close())

	before, err := os.ReadFile(paths.Long(logPath))
	require.NoError(t, err)
	f, err := os.OpenFile(paths.Long(logPath), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write([]byte(`{"v":1,"seq":2,"active":2`)) // torn: no closing brace/newline
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, _, err = openDeliverySegments(dir)
	require.ErrorIs(t, err, errSegmentUnavailable)
	after, err := os.ReadFile(paths.Long(logPath))
	require.NoError(t, err)
	require.Greater(t, len(after), len(before), "the torn bytes are preserved, not truncated")
}

// TestDeliverySegments_ConflictingHeadIsRefused: a head naming bytes past the log, or disagreeing with
// the record it names, is refused — a lower/mismatched head is never trusted over the log.
func TestDeliverySegments_ConflictingHeadIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(h *segHead)
	}{
		{"log_bytes past eof", func(h *segHead) { h.LogBytes += 100 }},
		{"active disagrees with record", func(h *segHead) { h.Active = 999 }},
		{"last_len past log_bytes", func(h *segHead) { h.LastLen = h.LogBytes + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s, _, err := openDeliverySegments(dir)
			require.NoError(t, err)
			require.NoError(t, s.initActiveZero())
			require.NoError(t, s.commitTransition(1, segRoot(1)))
			head := s.headPath
			require.NoError(t, s.close())

			raw, err := os.ReadFile(paths.Long(head))
			require.NoError(t, err)
			var h segHead
			require.NoError(t, json.Unmarshal(raw, &h))
			tc.mutate(&h)
			b, err := json.Marshal(h)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(paths.Long(head), b, 0o600))

			_, _, err = openDeliverySegments(dir)
			require.ErrorIs(t, err, errSegmentUnavailable)
		})
	}
}

// TestDeliverySegments_CommitRequiresActiveZeroAndAdvances: transitions cannot precede active-0, cannot
// regress the active segment, and reject a malformed base root.
func TestDeliverySegments_CommitRequiresActiveZeroAndAdvances(t *testing.T) {
	dir := t.TempDir()
	s, _, err := openDeliverySegments(dir)
	require.NoError(t, err)
	require.ErrorIs(t, s.commitTransition(1, segRoot(1)), errSegmentUnavailable, "no transition before active-0")
	require.NoError(t, s.initActiveZero())
	require.ErrorIs(t, s.commitTransition(1, "not-hex"), errSegmentUnavailable, "a malformed base root is refused")
	require.ErrorIs(t, s.commitTransition(2, segRoot(2)), errSegmentUnavailable, "segments cannot skip an unrecorded predecessor")
	require.NoError(t, s.commitTransition(1, segRoot(1)))
	require.NoError(t, s.commitTransition(2, segRoot(2)))
	require.ErrorIs(t, s.commitTransition(2, segRoot(2)), errSegmentUnavailable, "active only advances")
	require.NoError(t, s.close())
}

// TestDeliverySegments_CreateFreshSegmentIdempotentAndConflict: staging a new segment twice is
// idempotent; a dir with any other content is a preserved conflict, refused.
func TestDeliverySegments_CreateFreshSegmentIdempotentAndConflict(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, createFreshSegment(dir, 1))
	require.NoError(t, createFreshSegment(dir, 1), "re-staging an identical fresh segment is idempotent")

	fresh, err := segmentIsFreshEmpty(dir, 1)
	require.NoError(t, err)
	require.True(t, fresh)

	// A non-empty lease file in a staged dir is a conflict, preserved and refused.
	require.NoError(t, os.MkdirAll(paths.Long(segmentDir(dir, 2)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(segmentDir(dir, 2), deliveryLeaseFile)), []byte("x"), 0o600))
	require.ErrorIs(t, createFreshSegment(dir, 2), errSegmentUnavailable, "a conflicting staged dir is refused, not overwritten")
}
