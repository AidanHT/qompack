package storetest

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the storetest suites (subplan table, §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): put/get round-trip, global dedup,
// ChangedSince detecting a hash change, MarkEncoded as the §4.6 DPI guard, and append-only
// growth-only history. All are authored now, gated behind the same Rule W-1 stub probe as the
// rest of the suite, so SP-06 inherits them rather than writing its own grader.

// runPutGetRoundTripCase asserts PutBytes followed by Open reproduces the original bytes exactly,
// and that GetRoot/GetChunk agree with what PutResult itself reported.
func runPutGetRoundTripCase(t *testing.T, factory func(t *testing.T) store.Store) {
	t.Helper()
	s := factory(t)
	ctx := context.Background()

	payload := []byte("storetest: put/get round-trip payload, not one of the near-dup fixtures")
	res, err := s.PutBytes(ctx, payload, store.PutOptions{Tool: "FileRead", Path: "src/round_trip.txt"})
	require.NoError(t, err)
	require.False(t, res.Root.Hash.IsZero(), "fixture sanity: PutBytes must report a non-zero root hash")

	rc, err := s.Open(ctx, res.Root.Hash)
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, payload, got, "Open(PutBytes(b).Root.Hash) must reproduce b exactly")

	root, err := s.GetRoot(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, res.Root, root, "GetRoot must agree with the Root PutBytes itself returned")

	for _, c := range res.Root.Chunks {
		chunkBytes, err := s.GetChunk(ctx, c.Hash)
		require.NoError(t, err)
		require.Len(t, chunkBytes, c.Len, "GetChunk(h) must return exactly the Len bytes ChunkRef reports for h")
	}
}

// runGlobalDedupCase asserts a second PutBytes of identical bytes has Novel == 0 (the "GLOBAL
// dedup" behaviour named in the subplan table): dedup is at the content level, independent of
// which Path or Tool the second call is attributed to.
func runGlobalDedupCase(t *testing.T, factory func(t *testing.T) store.Store) {
	t.Helper()
	s := factory(t)
	ctx := context.Background()

	payload := []byte("storetest: global dedup payload, identical bytes put twice under different paths")

	first, err := s.PutBytes(ctx, payload, store.PutOptions{Tool: "FileRead", Path: "src/one.txt"})
	require.NoError(t, err)
	require.Greater(t, first.Novel, 0, "fixture sanity: the FIRST put of new content must write at least one novel chunk")

	// A second put of the IDENTICAL bytes, under a DIFFERENT tool/path, must still dedup: this is
	// what makes the dedup "global" rather than scoped to one path or tool.
	second, err := s.PutBytes(ctx, payload, store.PutOptions{Tool: "Grep", Path: "src/two.txt"})
	require.NoError(t, err)
	require.Equal(t, first.Root.Hash, second.Root.Hash, "identical bytes must produce the same root hash regardless of Path/Tool")
	require.Zero(t, second.Novel, "a second put of identical bytes must not write any new chunks (global dedup)")
	require.Equal(t, len(second.Root.Chunks), second.Reused, "every one of the second put's chunks must be reported as reused, not novel")
}

// runChangedSinceCase asserts ChangedSince detects a hash change: a Dep pinned to an old file
// version is reported changed once a newer version is appended, and reported unchanged before
// that (00-ARCHITECTURE.md §8.3 staleness).
func runChangedSinceCase(t *testing.T, factory func(t *testing.T) store.Store) {
	t.Helper()
	s := factory(t)
	ctx := context.Background()
	const path = "src/changed_since.txt"

	v1, err := s.PutBytes(ctx, []byte("changed-since: version 1"), store.PutOptions{Path: path})
	require.NoError(t, err)
	require.NoError(t, s.AppendFileVersion(ctx, path, store.FileVersion{
		TS: core.UnixMilli(1), Root: v1.Root.Hash, Turn: core.TurnIndex(0), Bytes: v1.Root.RawBytes,
	}))

	dep := core.Dep{Path: path, Hash: v1.Root.Hash}

	changed, err := s.ChangedSince(ctx, []core.Dep{dep})
	require.NoError(t, err)
	require.Empty(t, changed, "a dep whose hash matches the current file version must not be reported changed")

	v2, err := s.PutBytes(ctx, []byte("changed-since: version 2, different content"), store.PutOptions{Path: path})
	require.NoError(t, err)
	require.NotEqual(t, v1.Root.Hash, v2.Root.Hash, "fixture sanity: the two versions must actually hash differently")
	require.NoError(t, s.AppendFileVersion(ctx, path, store.FileVersion{
		TS: core.UnixMilli(2), Root: v2.Root.Hash, Turn: core.TurnIndex(1), Bytes: v2.Root.RawBytes,
	}))

	changed, err = s.ChangedSince(ctx, []core.Dep{dep})
	require.NoError(t, err)
	require.Equal(t, []core.Dep{dep}, changed, "a dep pinned to the OLD hash must be reported changed once a newer version exists")
}

// runFileHistoryAppendOnlyCase is the interface-level form of "append-only files never shrink"
// (subplan table): index/files.json is not directly observable through the Store interface, so
// this exercises the property the only way the interface exposes it — FileHistory must only ever
// grow across AppendFileVersion calls, and every previously appended version must remain present,
// unchanged and in order.
func runFileHistoryAppendOnlyCase(t *testing.T, factory func(t *testing.T) store.Store) {
	t.Helper()
	s := factory(t)
	ctx := context.Background()
	const path = "src/history_never_shrinks.txt"
	const versions = 3

	var appended []store.FileVersion
	for i := 0; i < versions; i++ {
		res, err := s.PutBytes(ctx, []byte(fmt.Sprintf("history version %d", i)), store.PutOptions{Path: path})
		require.NoError(t, err)

		v := store.FileVersion{TS: core.UnixMilli(int64(i) + 1), Root: res.Root.Hash, Turn: core.TurnIndex(i), Bytes: res.Root.RawBytes}
		require.NoError(t, s.AppendFileVersion(ctx, path, v))
		appended = append(appended, v)

		hist, err := s.FileHistory(ctx, path)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(hist), len(appended), "FileHistory must never shrink after AppendFileVersion")
		require.Equal(t, appended, hist[:len(appended)], "every previously appended file version must remain present, unchanged, and in order")
	}
}

// runMarkEncodedDPIGuardCase asserts MarkEncoded is the §4.6 "never compress a compression"
// guard: it is idempotent for a repeated call with the SAME CheckpointSeq, and it refuses a
// SECOND, DIFFERENT seq with ErrAlreadyEncoded, leaving the segment's own EncodedOnce/
// CheckpointSeq exactly as the first successful call set them.
func runMarkEncodedDPIGuardCase(t *testing.T, factory func(t *testing.T) store.SegmentLog) {
	t.Helper()
	sl := factory(t)
	ctx := context.Background()

	id, err := sl.Open(ctx, store.Segment{Session: "sess-dpi-guard", StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, id, core.TurnIndex(5), map[string]float64{"path_jaccard": 0}))

	const firstSeq = core.CheckpointSeq(1)
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, firstSeq))

	// Idempotent for the SAME seq.
	require.NoError(t, sl.MarkEncoded(ctx, []core.SegmentID{id}, firstSeq))

	got, err := sl.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, got.EncodedOnce)
	require.Equal(t, firstSeq, got.CheckpointSeq)

	// A DIFFERENT seq must be refused: this is the DPI guard — a segment's ORIGINAL content may
	// be encoded into a checkpoint exactly once.
	const secondSeq = core.CheckpointSeq(2)
	err = sl.MarkEncoded(ctx, []core.SegmentID{id}, secondSeq)
	require.ErrorIs(t, err, core.ErrAlreadyEncoded)

	// The refused call must not have mutated the segment's recorded seq.
	got, err = sl.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, got.EncodedOnce)
	require.Equal(t, firstSeq, got.CheckpointSeq, "a refused MarkEncoded must not change the segment's recorded CheckpointSeq")
}

// runRangeNeverShrinksCase is SegmentLog's own complement to
// runFileHistoryAppendOnlyCase: Range results for an already-covered turn span must not lose a
// previously visible Segment once a later, disjoint Segment is opened and closed.
func runRangeNeverShrinksCase(t *testing.T, factory func(t *testing.T) store.SegmentLog) {
	t.Helper()
	sl := factory(t)
	ctx := context.Background()

	firstID, err := sl.Open(ctx, store.Segment{Session: "sess-range", StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, firstID, core.TurnIndex(3), nil))

	before, err := sl.Range(ctx, core.TurnIndex(0), core.TurnIndex(3))
	require.NoError(t, err)
	require.Contains(t, segmentIDs(before), firstID)

	secondID, err := sl.Open(ctx, store.Segment{Session: "sess-range", StartTurn: 4})
	require.NoError(t, err)
	require.NoError(t, sl.Close(ctx, secondID, core.TurnIndex(7), nil))

	after, err := sl.Range(ctx, core.TurnIndex(0), core.TurnIndex(7))
	require.NoError(t, err)
	require.Contains(t, segmentIDs(after), firstID, "opening a later segment must not make an earlier one disappear from Range")
	require.Contains(t, segmentIDs(after), secondID)
}

// segmentIDs projects a []store.Segment down to its IDs, for require.Contains comparisons.
func segmentIDs(segs []store.Segment) []core.SegmentID {
	ids := make([]core.SegmentID, len(segs))
	for i, s := range segs {
		ids[i] = s.ID
	}
	return ids
}
