package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// The tests below pin WHICH bound a pass records (SP20-D1, F6 review round 2, F1), the half
// drain_durable_progress_test.go and drain_reopened_segment_test.go leave open. Those two pin that a
// pass never records MORE than it read durably, and that the record stays loadable when a reopened
// segment's bound falls to 0. Both are satisfied by a Size floored at the consumed offset, and that
// floor drops a refusal the pre-R9 code made: for a pass that stopped short of the durable bound, the
// bytes between the offset and that bound WERE durable, and a record that stops naming them lets a
// truncation through. The rule is that the record keeps the bound it holds — except a record this
// code did not write, whose Size is a raw stat that may name a held segment's unsynced tail.

// requireDurableSizeMark asserts what state/drain.json carries for base, in the bytes rather than in
// the loaded record. The mark is the negation of drainFileState.SizeIsRawStat, whose zero value is a
// record this code wrote, so the on-disk field is the only place the two provenances differ.
func requireDurableSizeMark(t *testing.T, root, base string, want bool) {
	t.Helper()
	b, err := os.ReadFile(paths.Long(drainStatePath(root)))
	require.NoError(t, err)
	var raw map[string]map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	rec, ok := raw[base]
	require.True(t, ok, "state/drain.json must name %s", base)
	mark, present := rec["durable_size"]
	if !want {
		require.False(t, present, "a record whose size may be a raw stat carries no durable_size mark")
		return
	}
	require.True(t, present, "a record this code wrote carries the durable_size mark")
	require.Equal(t, true, mark, "and the mark is set")
}

// seedPreR9Progress writes the state/drain.json a binary before the mark wrote: the raw stat in size,
// a held segment's unsynced tail included, and no durable_size field, because that binary had none.
func seedPreR9Progress(t *testing.T, root, base string, size, offset int64) {
	t.Helper()
	b, err := json.Marshal(map[string]map[string]any{
		base: {"size": size, "offset": offset, "done": false},
	})
	require.NoError(t, err)
	p := drainStatePath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), b, 0o600))
	requireDurableSizeMark(t, root, base, false) // fixture: the seed really is an unmarked record
}

// TestDrainKeepsTheDurableBoundItRecordedWhenASegmentIsReopenedBelowIt is the round-2 reviewer's
// demonstration, adopted. Every byte of the segment here is durable — the stat size and the synced
// size are equal throughout — so nothing in it turns on the unsynced tail the R9 fix stops recording.
// A handler NAK stops the first pass short of that durable bound, leaving {Size: both lines, Offset:
// the first}. The straggler then reopens the segment, and ingest.holdSynced enters every reopened
// segment into ingest.synced at 0, so the bound the next pass reads to is BELOW the bound the record
// already holds.
//
// Recording that pass's own bound floored at its offset made the recorded size FALL, from the durable
// bound to the consumed offset. The bytes between them were durable when they were recorded, the
// record stopped naming them, and validateProgress therefore stopped refusing when they went missing:
// a truncation the pre-R9 base refuses drew no refusal at all, the entry flipped to Done at the
// shortened size, and a shorter foreign line written over those bytes was delivered as this segment's
// continuation. The record keeps the bound instead, and the refusal stands.
func TestDrainKeepsTheDurableBoundItRecordedWhenASegmentIsReopenedBelowIt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))

	const sess = core.SessionID("sess-reopened-bound")
	segPath := walPath(spool, sess, 0)
	segBase := filepath.Base(segPath)
	a, b := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: 1}).line,
		wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: 2}).line
	require.NoError(t, os.WriteFile(paths.Long(segPath), joinLines(a, b), 0o600))
	bound := int64(len(a) + len(b)) // the ingest's Sync returned for BOTH lines

	synced, nak := bound, true
	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			if nak && r.TS == 2 {
				return ipc.Response{OK: false, Err: "the handler did not acknowledge the delivery"}
			}
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
		IsLive: func(core.SessionID) bool { return true },
		SyncedWAL: func(p string) (int64, bool) {
			if filepath.Base(p) != segBase {
				return 0, false
			}
			return synced, true
		},
	})

	_, err := dr.Drain(ctx)
	require.Error(t, err, "fixture: the second line's handler NAKed, so the pass stopped short of the bound")
	require.Equal(t, &drainFileState{Size: bound, Offset: int64(len(a))}, diskDrainState(t, root)[segBase],
		"fixture: the record names a durable bound past the offset the pass consumed")
	requireDurableSizeMark(t, root, segBase, true)
	require.Equal(t, []core.UnixMilli{1}, got)

	// The straggler reopens the segment: its synced size answers 0, below both the recorded bound and
	// the consumed offset.
	nak, synced = false, 0
	n, err := dr.Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "the reopened segment's bound is 0, so the pass reads nothing of it")
	require.Equal(t, &drainFileState{Size: bound, Offset: int64(len(a))}, diskDrainState(t, root)[segBase],
		"the record keeps the durable bound it held; it does not fall to the offset the pass consumed")
	requireDurableSizeMark(t, root, segBase, true)
	require.Equal(t, bound-int64(len(a)), dr.GapState().PendingBytes,
		"the bytes between the offset and the bound are unread, and pending exactly once")

	// The truncation that takes them. They were durable and the record names them, so the pass must
	// refuse rather than read whatever now stands at the consumed offset.
	require.NoError(t, os.Truncate(paths.Long(segPath), int64(len(a))))
	n, err = dr.Drain(ctx)
	require.Error(t, err, "a file that lost bytes an earlier pass made durable and recorded refuses the pass")
	require.Zero(t, n)
	require.Equal(t, []DrainGap{{Kind: DrainGapProgressUnreadable, Count: 1, Reason: "drain progress no longer matches the spool"}},
		dr.GapState().Gaps)
	require.Equal(t, []core.UnixMilli{1}, got, "and nothing written over them is delivered as the segment's own")
}

// TestDrainDoesNotKeepARawStatBoundItDidNotRecord is the other side of the same rule, and the W1
// wedge control for both provenances of a record. Keeping the bound a record holds is safe only for a
// record THIS code wrote: a state/drain.json written before the mark recorded the raw stat, a held
// segment's unsynced tail included, and keeping that would inherit a bound the very machine crash R9
// exists to survive falls below — wedging the whole spool once, for that one record. Such a record is
// lowered to the pass's own durable bound and marked; a marked one is kept as it is. Either way the
// crash that takes only bytes no Sync returned for must leave the spool draining.
func TestDrainDoesNotKeepARawStatBoundItDidNotRecord(t *testing.T) {
	cases := []struct {
		name  string
		preR9 bool
	}{
		{name: "a record written before the mark, whose size names the unsynced tail", preR9: true},
		{name: "a record this code wrote, whose size names only durable bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			spool := paths.Of(root).Spool
			require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))

			const sess = core.SessionID("sess-raw-stat")
			segPath := walPath(spool, sess, 0)
			segBase := filepath.Base(segPath)
			seg := func(ts core.UnixMilli) []byte {
				return wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: ts}).line
			}
			// l1 is covered by a Sync that returned; l2 and l3 are written and not synced.
			l1, l2, l3 := seg(1), seg(2), seg(3)
			require.NoError(t, os.WriteFile(paths.Long(segPath), joinLines(l1, l2, l3), 0o600))
			synced := int64(len(l1))
			stat := spoolFileSize(t, segPath)

			// Both records describe the same history: an earlier pass consumed l1, the only durable
			// line. They differ only in what that pass recorded as the file's size.
			if tc.preR9 {
				seedPreR9Progress(t, root, segBase, stat, synced)
			} else {
				require.NoError(t, newDrainer(DrainConfig{Root: root}).saveState(
					drainState{segBase: &drainFileState{Size: synced, Offset: synced}}))
				requireDurableSizeMark(t, root, segBase, true)
			}

			var got []core.UnixMilli
			dr := newDrainer(DrainConfig{
				Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
				Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
					got = append(got, r.TS)
					return ipc.Response{OK: true}
				},
				IsLive: func(core.SessionID) bool { return true },
				SyncedWAL: func(p string) (int64, bool) {
					if filepath.Base(p) != segBase {
						return 0, false
					}
					return synced, true
				},
			})

			n, err := dr.Drain(ctx)
			require.NoError(t, err)
			require.Zero(t, n, "the segment's one durable line was consumed by the pass whose record this is")
			require.Equal(t, &drainFileState{Size: synced, Offset: synced}, diskDrainState(t, root)[segBase],
				"the pass records its own durable bound: a raw stat naming the unsynced tail is not kept")
			requireDurableSizeMark(t, root, segBase, true) // and the record it writes carries the mark
			require.Equal(t, stat-synced, dr.GapState().PendingBytes,
				"the unsynced tail is pending all the same, though no progress names it")

			// The machine crash: the tail the ingest had written and not synced goes with the page
			// cache. A brand-new healthy file measures the blast radius a wedge would have.
			require.NoError(t, os.Truncate(paths.Long(segPath), synced))
			const healthyName = "client-8181.ndjson"
			healthyPath := filepath.Join(spool, healthyName)
			require.NoError(t, os.WriteFile(paths.Long(healthyPath),
				wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-healthy", TS: 9}).line, 0o600))

			n, err = dr.Drain(ctx)
			require.NoError(t, err, "a crash that took only bytes no Sync returned for must not wedge the spool")
			require.Equal(t, 1, n, "the healthy file's line")
			require.Equal(t, []core.UnixMilli{9}, got)
			require.NoFileExists(t, healthyPath, "fully drained, the healthy file is removed")
			require.Zero(t, dr.GapState().PendingBytes)
		})
	}
}

// TestDrainWritesALoadableRecordWhenAFileShrinksUnderThePass pins what keeping the bound costs, and
// that the cost is paid. Once Size can stand ABOVE the stat a pass saw — for a file that shrank
// between validateProgress and that stat — consuming to the stat no longer means Offset == Size.
// Setting Done from the offset alone then wrote {Done, Offset < Size}, which loadState refuses
// outright, and handed the file to removeCompletedFile as finished at a size below the durable bound
// its own record names: unlinked, with the bytes between the two gone unread. The pass leaves the
// file unfinished instead. Its record stays loadable, the next pass refuses over the missing durable
// bytes, and that refusal clears the moment the file holds them again — a refusal, not a wedge.
//
// The seam is the listing's own order: ipc.SpoolFiles offers wal-* before client-*, so the segment's
// dispatch runs after the pass validated its progress and before it stats the client file. Nothing
// here waits on a clock or on another goroutine.
func TestDrainWritesALoadableRecordWhenAFileShrinksUnderThePass(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))

	line := func(s core.SessionID, ts core.UnixMilli) []byte {
		return wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: s, TS: ts}).line
	}
	segPath := filepath.Join(spool, "wal-sess-shrink.ndjson")
	require.NoError(t, os.WriteFile(paths.Long(segPath), line("sess-shrink", 1), 0o600))
	const clientName = "client-5959.ndjson"
	clientPath := filepath.Join(spool, clientName)
	c1, c2 := line("sess-client", 2), line("sess-client", 3)
	require.NoError(t, os.WriteFile(paths.Long(clientPath), joinLines(c1, c2), 0o600))
	bound := int64(len(c1) + len(c2))
	require.NoError(t, newDrainer(DrainConfig{Root: root}).saveState(
		drainState{clientName: &drainFileState{Size: bound}}))

	var got []core.UnixMilli
	shrunk := false
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			if !shrunk { // the segment's line: the pass has not reached the client file yet
				require.NoError(t, os.Truncate(paths.Long(clientPath), int64(len(c1))))
				shrunk = true
			}
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
	})

	n, err := dr.Drain(ctx)
	require.NoError(t, err)
	require.True(t, shrunk, "fixture: the file shrank after validateProgress and before the pass stated it")
	require.Equal(t, 2, n, "the segment's line, and the one line the shortened file still holds")
	require.Equal(t, []core.UnixMilli{1, 2}, got)
	// diskDrainState loads the record the way a restarted process does, so {Done, Offset != Size}
	// fails right here; and a file called finished at the shortened size would already be gone.
	require.Equal(t, &drainFileState{Size: bound, Offset: int64(len(c1))}, diskDrainState(t, root)[clientName],
		"the record keeps its durable bound, and the file is not finished at a size below it")
	require.FileExists(t, clientPath, "so nothing unlinks it")

	before, err := os.ReadFile(paths.Long(drainStatePath(root)))
	require.NoError(t, err)
	n, err = dr.Drain(ctx)
	require.Error(t, err, "the file is shorter than the durable bytes its record names: the pass refuses")
	require.Zero(t, n)
	after, err := os.ReadFile(paths.Long(drainStatePath(root)))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after),
		"a pass that refuses its progress leaves state/drain.json byte for byte as it found it")

	appendSpoolFile(t, clientPath, c2) // the bytes come back
	n, err = dr.Drain(ctx)
	require.NoError(t, err, "the refusal clears once the file holds the durable bytes again")
	require.Equal(t, 1, n)
	require.Equal(t, []core.UnixMilli{1, 2, 3}, got, "and the line the shrink hid is delivered exactly once")
}
