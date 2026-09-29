package checkpoint_test

// F-UAT03-2 (= F-UAT02-4): index/segments.jsonl may only name a checkpoint that has been sealed.
// The idle frontier advance encodes a session's closed segments into a DRAFT; before the fix it
// appended a durable encode record naming the draft's sequence number at that moment, so a daemon
// that idled out before any compaction sealed the draft left the index claiming a checkpoint that
// was never written — fsck failed index.segments and every backup taken afterwards restored with
// integrity FAILED. test/e2e's TestE2E_IdleExitLeavesNoUnsealedEncodeClaim drives the real idle
// exit; these rows pin the writer's half of the ordering.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// encodeRecords returns the encode records index/segments.jsonl holds on disk, as id → seq.
func encodeRecords(t *testing.T, root string) map[core.SegmentID]core.CheckpointSeq {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, "segments.jsonl")))
	require.NoError(t, err)
	out := map[core.SegmentID]core.CheckpointSeq{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var rec struct {
			Op  string             `json:"op"`
			ID  core.SegmentID     `json:"id"`
			Seq core.CheckpointSeq `json:"seq"`
		}
		require.NoError(t, json.Unmarshal(sc.Bytes(), &rec))
		if rec.Op == "encode" {
			out[rec.ID] = rec.Seq
		}
	}
	require.NoError(t, sc.Err())
	return out
}

func TestAdvanceRecordsNoEncodeUntilTheSeal(t *testing.T) {
	f := newFx(t)
	f.tool("tu_a", 11, "Read", "src/a.ts", "alpha body", false)
	f.closedSeg(12, 10, 19)

	_, reserves := f.store.Segments().(store.SegmentReservation)
	require.True(t, reserves, "these rows need the store's own, reserving segment log")

	d := f.begin()
	f.advance(d, 12)

	seg, err := f.store.Segments().Get(f.ctx(), 12)
	require.NoError(t, err)
	require.True(t, seg.EncodedOnce, "the scheduler still sees the drafted segment as encoded")
	require.Equal(t, d.Seq(), seg.CheckpointSeq)
	require.Empty(t, encodeRecords(t, f.p.Root),
		"no encode record may name draft %04d before a seal publishes it", int(d.Seq()))

	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	require.Equal(t, map[core.SegmentID]core.CheckpointSeq{12: ref.Seq}, encodeRecords(t, f.p.Root),
		"the seal writes the encode record, at the sequence the artifact was written at")
	entries, err := paths.ReadManifest(paths.Of(f.p.Root))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, ref.Seq, entries[0].Seq, "the record names a checkpoint the manifest records")
}

// TestFinalizeSealsTheSegmentsAResumedDraftHolds is the path after a clean idle exit: the draft
// persisted, the reservation died with the log, and the next daemon resumes the draft and seals it.
// The segment must reach the checkpoint AND the index, at the draft's own sequence number.
func TestFinalizeSealsTheSegmentsAResumedDraftHolds(t *testing.T) {
	f := newFx(t)
	f.tool("tu_a", 11, "Read", "src/a.ts", "alpha body", false)
	f.closedSeg(12, 10, 19)
	d := f.begin()
	f.advance(d, 12)
	seq := d.Seq()

	// The daemon exits: its store closes, and the next one opens the same project.
	require.NoError(t, f.store.Close())
	st2 := f.p.Store(t)
	w2, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	src2 := f.src
	src2.Store, src2.Segments = st2, st2.Segments()

	seg, err := st2.Segments().Get(f.ctx(), 12)
	require.NoError(t, err)
	require.False(t, seg.EncodedOnce, "the reservation was not durable")

	d2, err := w2.Begin(f.ctx(), f.sess, 0, src2)
	require.NoError(t, err)
	require.Equal(t, seq, d2.Seq(), "the persisted draft resumes at its own sequence")
	require.Equal(t, 1, d2.EncodedCount())

	_, err = w2.Advance(f.ctx(), d2, []core.SegmentID{12})
	require.NoError(t, err)
	seg, err = st2.Segments().Get(f.ctx(), 12)
	require.NoError(t, err)
	require.True(t, seg.EncodedOnce, "the resumed draft reserves the segment it holds again")

	ref, err := w2.Finalize(f.ctx(), d2, finalizeBudget)
	require.NoError(t, err)
	require.Equal(t, seq, ref.Seq)
	require.Equal(t, map[core.SegmentID]core.CheckpointSeq{12: seq}, encodeRecords(t, f.p.Root))
	r, err := checkpoint.OpenReader(f.p.Root, f.p.Log, nil)
	require.NoError(t, err)
	cp, _, err := r.Get(f.ctx(), seq)
	require.NoError(t, err)
	require.Equal(t, []core.SegmentID{12}, cp.EncodedSegments, "the sealed checkpoint carries the segment")
}

// TestBeginSkipsASequenceAnotherSessionsPersistedDraftHolds: a draft an earlier daemon left for
// ANOTHER session keeps its number, so this session's fresh draft cannot take it and later make
// that draft's claim point at a checkpoint that holds none of its turns.
func TestBeginSkipsASequenceAnotherSessionsPersistedDraftHolds(t *testing.T) {
	f := newFx(t)
	other := core.SessionID("sess_sp10_other")
	raw := fmt.Sprintf(`{"session":%q,"seq":1,"parent":0,"frontier":7,"encoded":[1],`+
		`"started":1767225600000,"checkpoint":{"version":1,"session":%q,"seq":1,"created":"",`+
		`"encoded_segments":[1]}}`, other, other)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(paths.Of(f.p.Root).State,
		"draft-"+string(other)+".json")), []byte(raw), 0o600))

	d := f.begin()
	require.Equal(t, core.CheckpointSeq(2), d.Seq(), "sequence 1 belongs to the other session's unsealed draft")
}

// TestBeginSkipsASequenceADurableEncodeRecordNames covers a store an earlier build wrote: an encode
// record naming a checkpoint that was never sealed, and no draft file left to say so.
func TestBeginSkipsASequenceADurableEncodeRecordNames(t *testing.T) {
	f := newFx(t)
	f.closedSeg(12, 10, 19)
	require.NoError(t, f.store.Segments().MarkEncoded(f.ctx(), []core.SegmentID{12}, 3))

	d := f.begin()
	require.Equal(t, core.CheckpointSeq(4), d.Seq(), "sequence 3 is named by a durable encode record")
}

// TestBeginScansPersistedDraftsOncePerWriter: the claim floor — every draft another daemon lifetime
// left unsealed, and every durable encode record — is fixed when a writer opens, because only this
// writer creates drafts or seals checkpoints afterwards (the daemon lock), and every number it hands
// out is tracked by issuedSeq. So the state/ scan runs on the first fresh Begin, not on every one:
// Finalize's afterSeal Begins a successor inside the PreCompact budget (B-E), and idle-exit drafts of
// ended sessions accumulate with the project's history.
func TestBeginScansPersistedDraftsOncePerWriter(t *testing.T) {
	f := newFx(t)
	other := core.SessionID("sess_sp10_other")
	raw := fmt.Sprintf(`{"session":%q,"seq":3,"parent":0,"frontier":7,"encoded":[1],`+
		`"started":1767225600000,"checkpoint":{"version":1,"session":%q,"seq":3,"created":"",`+
		`"encoded_segments":[1]}}`, other, other)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(paths.Of(f.p.Root).State,
		"draft-"+string(other)+".json")), []byte(raw), 0o600))

	d := f.begin()
	require.Equal(t, core.CheckpointSeq(4), d.Seq(), "sequence 3 belongs to the other session's unsealed draft")
	require.Equal(t, 1, checkpoint.DraftScansForTest(f.w))

	f.tool("tu_a", 11, "Read", "src/a.ts", "alpha body", false)
	f.closedSeg(12, 10, 19)
	f.advance(d, 12)
	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(4), ref.Seq)
	next := f.w.DraftFor(f.sess)
	require.NotNil(t, next, "the seal opened a successor draft")
	require.Equal(t, core.CheckpointSeq(5), next.Seq())
	require.Equal(t, 1, checkpoint.DraftScansForTest(f.w), "the successor's Begin did not scan state/ again")
}
