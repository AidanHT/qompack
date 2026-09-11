package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// RecordToolUseSuperseding is the store half of carried defect SP08-D2. The defect is an
// INTERLEAVING, not a wrong value: a record appended by one call and its supersede marks appended
// by N later calls leave N+1 points at which a cancelled handler can stop, and the states in
// between — a record whose marks never landed, or marks appended by a replay that wrote no record —
// are what the e2e x09 flush arm rejects.
//
// So the assertions below are about WHEN bytes land, not only about what they say: one write for
// the whole batch, nothing at all on a replay, and nothing at all on a cancelled call. A test that
// only read the file back afterwards would pass against the defective 1+N shape.

// countingHandle wraps an appendFile's real handle and counts the Write calls made through it,
// keeping the bytes of each one. It FORWARDS to the handle it wraps rather than replacing it, so
// the index file on disk is still written and a reopened store still replays it — the "one write"
// assertion and the "a restart sees the marks" assertion can then be made about the same run.
//
// It follows bufferHandle (helpers_test.go): an io.WriteCloser that is not an *os.File.
type countingHandle struct {
	inner  interface{ Write([]byte) (int, error) }
	closer interface{ Close() error }
	writes [][]byte
}

func (h *countingHandle) Write(p []byte) (int, error) {
	h.writes = append(h.writes, append([]byte(nil), p...))
	return h.inner.Write(p)
}

func (h *countingHandle) Close() error { return h.closer.Close() }

// countTUWrites interposes a countingHandle on the tool_use append handle, returning it. It takes
// the appendFile's own mutex for the swap, which is the lock every write is made under.
func countTUWrites(t *testing.T, s *FSStore) *countingHandle {
	t.Helper()
	s.tuW.mu.Lock()
	defer s.tuW.mu.Unlock()
	h := &countingHandle{inner: s.tuW.w, closer: s.tuW.w}
	s.tuW.w = h
	return h
}

// supersedingFixture records prior at ts and returns the fixture plus a record that supersedes it.
func supersedingFixture(t *testing.T) (*idxFixture, ToolUseRecord, ToolUseRecord) {
	t.Helper()
	f := newIdxStore(t)
	older := sampleToolUse("toolu_01OLDER", "src/auth.ts", 1734128400123)
	require.NoError(t, f.s.RecordToolUse(context.Background(), older))
	newer := sampleToolUse("toolu_01NEWER", "src/auth.ts", 1734128400456)
	return f, older, newer
}

// TestRecordToolUseSuperseding_RecordAndItsMarksAreOneWrite is the defect's store half, stated
// directly: the record line and every mark it authors reach the file in a SINGLE write, in that
// order, and a restart replays them.
//
// The order is not cosmetic. loadToolUse applies a "supersede" mutation only to an id it has
// already loaded, so a mark that preceded its own record's line would be silently dropped on the
// next open — the record would come back StatusOK and the supersession would be lost.
func TestRecordToolUseSuperseding_RecordAndItsMarksAreOneWrite(t *testing.T) {
	f, older, newer := supersedingFixture(t)
	ctx := context.Background()

	h := countTUWrites(t, f.s)
	marked, recorded, err := f.s.RecordToolUseSuperseding(ctx, newer, []core.ToolUseID{older.ID})
	require.NoError(t, err)
	require.True(t, recorded, "a record the index did not hold must be written")
	require.Equal(t, []core.ToolUseID{older.ID}, marked)

	require.Len(t, h.writes, 1,
		"the record and its marks must land in ONE write: a cancelled handler between two writes is "+
			"what leaves a record with no mark, and a redelivery then appends the mark alone (SP08-D2)")
	lines := splitIndexLines(t, h.writes[0])
	require.Len(t, lines, 2, "one record line plus one mark line")
	require.Contains(t, string(lines[0]), `"id":"`+string(newer.ID)+`"`)
	require.NotContains(t, string(lines[0]), `"op":"supersede"`, "the RECORD line comes first")
	require.Contains(t, string(lines[1]), `"op":"supersede"`)
	require.Contains(t, string(lines[1]), `"id":"`+string(older.ID)+`"`)
	require.Contains(t, string(lines[1]), `"by":"`+string(newer.ID)+`"`)

	// In memory, then across a restart: the mark is the record's status, whichever way it is read.
	got, err := f.s.ToolUse(ctx, older.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSuperseded, got.Status)
	require.Equal(t, newer.ID, got.SupersededBy)

	s2 := f.reopen(t)
	got, err = s2.ToolUse(ctx, older.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSuperseded, got.Status,
		"the mark must survive a restart: index/tool_use.jsonl, not memory, is the truth")
	require.Equal(t, newer.ID, got.SupersededBy)
}

// TestRecordToolUseSuperseding_ReplayWritesNothingAtAll is what makes a redelivered read append no
// line. Under RecordToolUse a replayed record was already a silent no-op — but its CALLER then
// re-ran supersession and appended the marks a second time, which is mechanism 2a of SP08-D2. Here
// the marks are part of the same decision, so recorded=false means the caller marks nothing either.
func TestRecordToolUseSuperseding_ReplayWritesNothingAtAll(t *testing.T) {
	f, older, newer := supersedingFixture(t)
	ctx := context.Background()

	_, recorded, err := f.s.RecordToolUseSuperseding(ctx, newer, []core.ToolUseID{older.ID})
	require.NoError(t, err)
	require.True(t, recorded)
	before := indexLines(t, f.root, toolUseFile)

	h := countTUWrites(t, f.s)
	marked, recorded, err := f.s.RecordToolUseSuperseding(ctx, newer, []core.ToolUseID{older.ID})
	require.NoError(t, err, "a replay of an identical record is not an error")
	require.False(t, recorded, "the index already held this record with this root")
	require.Empty(t, marked, "a replay authors no marks: the marks it would author landed with it")
	require.Empty(t, h.writes, "a replay must not touch the append handle at all")
	require.Equal(t, before, indexLines(t, f.root, toolUseFile),
		"index/tool_use.jsonl must be byte-identical after a replay")
}

// TestRecordToolUseSuperseding_DifferentRootIsAnAppendOnlyViolation keeps RecordToolUse's rule: one
// tool_use id identifies one tool call, and one tool call has one result.
func TestRecordToolUseSuperseding_DifferentRootIsAnAppendOnlyViolation(t *testing.T) {
	f, older, newer := supersedingFixture(t)
	ctx := context.Background()

	_, recorded, err := f.s.RecordToolUseSuperseding(ctx, newer, []core.ToolUseID{older.ID})
	require.NoError(t, err)
	require.True(t, recorded)
	before := indexLines(t, f.root, toolUseFile)

	h := countTUWrites(t, f.s)
	conflicting := newer
	conflicting.Root = core.HashBytes(core.DomainRoot, []byte("a different result for one call"))
	_, recorded, err = f.s.RecordToolUseSuperseding(ctx, conflicting, []core.ToolUseID{older.ID})
	require.ErrorIs(t, err, core.ErrAppendOnly)
	require.False(t, recorded)
	require.Empty(t, h.writes, "a refused record must not append its marks either")
	require.Equal(t, before, indexLines(t, f.root, toolUseFile))
}

// TestRecordToolUseSuperseding_CancelledContextWritesNothing pins the property the SP08-D2
// cancellation sweep rests on: the ONE ctx check precedes every write, so a cancelled call is
// indistinguishable from one that never ran.
func TestRecordToolUseSuperseding_CancelledContextWritesNothing(t *testing.T) {
	f, older, newer := supersedingFixture(t)
	before := indexLines(t, f.root, toolUseFile)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := countTUWrites(t, f.s)
	marked, recorded, err := f.s.RecordToolUseSuperseding(ctx, newer, []core.ToolUseID{older.ID})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, recorded)
	require.Empty(t, marked)
	require.Empty(t, h.writes)
	require.Equal(t, before, indexLines(t, f.root, toolUseFile))

	_, err = f.s.ToolUse(context.Background(), newer.ID)
	require.ErrorIs(t, err, core.ErrNotFound, "a cancelled call must leave no in-memory record either")
}

// TestRecordToolUseSuperseding_SkipsCandidatesThatAreNotMarkable asserts the three filters, which
// are what let a caller hand over a supersession scan's whole output rather than one vetted id:
// an id the index does not hold, the record's own id, and a pair that is already marked.
//
// MarkSuperseded answers ErrNotFound for the first — correct for a caller naming ONE record, and
// wrong for a caller handing over a list of candidates, where an id that has since been evicted is
// a stale candidate rather than a failure.
func TestRecordToolUseSuperseding_SkipsCandidatesThatAreNotMarkable(t *testing.T) {
	f, older, newer := supersedingFixture(t)
	ctx := context.Background()

	// A second prior read of the same path, already superseded by newer before this call runs.
	alreadyMarked := sampleToolUse("toolu_01MARKED", "src/auth.ts", 1734128400200)
	require.NoError(t, f.s.RecordToolUse(ctx, alreadyMarked))

	marked, recorded, err := f.s.RecordToolUseSuperseding(ctx, newer,
		[]core.ToolUseID{older.ID, alreadyMarked.ID})
	require.NoError(t, err)
	require.True(t, recorded)
	require.ElementsMatch(t, []core.ToolUseID{older.ID, alreadyMarked.ID}, marked)

	// A later record naming: an unknown id, its own id, and a pair already on disk.
	latest := sampleToolUse("toolu_01LATEST", "src/auth.ts", 1734128400789)
	h := countTUWrites(t, f.s)
	marked, recorded, err = f.s.RecordToolUseSuperseding(ctx, latest, []core.ToolUseID{
		"toolu_01ABSENT", latest.ID, older.ID,
	})
	require.NoError(t, err)
	require.True(t, recorded)
	require.Equal(t, []core.ToolUseID{older.ID}, marked,
		"an absent id and the record's own id are skipped; the markable candidate is marked")

	require.Len(t, h.writes, 1)
	require.Len(t, splitIndexLines(t, h.writes[0]), 2, "one record line plus one mark line")

	// older is re-pointed at the newest record, last-wins, exactly as MarkSuperseded would.
	got, err := f.s.ToolUse(ctx, older.ID)
	require.NoError(t, err)
	require.Equal(t, latest.ID, got.SupersededBy)

	// And a repeat of a mark already on disk writes nothing: no unbounded growth under replay.
	h2 := countTUWrites(t, f.s)
	newest := sampleToolUse("toolu_01NEWEST", "src/auth.ts", 1734128400999)
	marked, _, err = f.s.RecordToolUseSuperseding(ctx, newest, []core.ToolUseID{older.ID})
	require.NoError(t, err)
	require.Equal(t, []core.ToolUseID{older.ID}, marked)
	require.Len(t, h2.writes, 1)

	h3 := countTUWrites(t, f.s)
	_, recorded, err = f.s.RecordToolUseSuperseding(ctx, newest, []core.ToolUseID{older.ID})
	require.NoError(t, err)
	require.False(t, recorded)
	require.Empty(t, h3.writes)
}

// TestRecordToolUseSuperseding_ArgsPreviewRedacted keeps RecordToolUse's §13 invariant 7 choke
// point: the preview is built from raw tool arguments and lands in a plaintext index file, so a
// second writer of that file must not be a way around redaction.
func TestRecordToolUseSuperseding_ArgsPreviewRedacted(t *testing.T) {
	f, older, newer := supersedingFixture(t)
	ctx := context.Background()

	newer.ArgsPreview = "aws_key=" + awsExampleKey + " limit=200"
	_, recorded, err := f.s.RecordToolUseSuperseding(ctx, newer, []core.ToolUseID{older.ID})
	require.NoError(t, err)
	require.True(t, recorded)

	got, err := f.s.ToolUse(ctx, newer.ID)
	require.NoError(t, err)
	require.NotContains(t, got.ArgsPreview, "AKIA", "the preview bypassed the redaction choke point")
	require.LessOrEqual(t, len(got.ArgsPreview), argsPreviewMax)

	s2 := f.reopen(t)
	got, err = s2.ToolUse(ctx, newer.ID)
	require.NoError(t, err)
	require.NotContains(t, got.ArgsPreview, "AKIA",
		"index/tool_use.jsonl carries the unredacted secret on disk")
}

// TestRecordToolUseSuperseding_RecordWithNoIDIsRefused mirrors RecordToolUse: a record the index
// cannot name is not a record.
func TestRecordToolUseSuperseding_RecordWithNoIDIsRefused(t *testing.T) {
	f := newIdxStore(t)
	h := countTUWrites(t, f.s)
	_, recorded, err := f.s.RecordToolUseSuperseding(context.Background(), ToolUseRecord{}, nil)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.False(t, recorded)
	require.Empty(t, h.writes)
}

// TestRecordToolUseSuperseding_ClosedStoreDegrades keeps the closed-store guard every method with
// an error return applies (degraded_test.go's table).
func TestRecordToolUseSuperseding_ClosedStoreDegrades(t *testing.T) {
	f := newIdxStore(t)
	require.NoError(t, f.s.Close())
	_, recorded, err := f.s.RecordToolUseSuperseding(context.Background(),
		sampleToolUse("toolu_01CLOSED", "src/auth.ts", 1734128400123), nil)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.False(t, recorded)
}

// splitIndexLines splits one write's bytes into its newline-terminated records.
func splitIndexLines(t *testing.T, b []byte) [][]byte {
	t.Helper()
	require.NotEmpty(t, b)
	require.Equal(t, byte('\n'), b[len(b)-1], "every index record is newline-terminated")
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return out
}
