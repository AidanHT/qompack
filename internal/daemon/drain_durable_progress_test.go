package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// The two tests below are the halves of one rule (SP20-D1, F6 review R9): the progress a pass persists
// names the durable bound it read to, and never a byte past it. The bound is the file's stat size for
// every file the drain syncs itself, and a held WAL segment's synced size for the one kind of file it
// does not — which is where the two part company, and where a machine crash could wedge the spool.

// appendSpoolFile appends b to the spool file at path the way the process that owns it does: the
// ingest to a segment it reopened, a hook client to its own spool.
func appendSpoolFile(t *testing.T, path string, b []byte) {
	t.Helper()
	w, err := paths.AppendOnly(path)
	require.NoError(t, err)
	_, err = w.Write(b)
	require.NoError(t, err)
	require.NoError(t, w.Close())
}

// TestDrainRecoversFromACrashThatTookAHeldSegmentsUnsyncedTail is the wedge itself. A pass over a WAL
// segment the ingest holds reads only to the segment's synced size, but it used to record the stat
// size, so drain.json named bytes no Sync had returned for. A machine crash then took exactly those
// bytes, and validateProgress found the recorded size past the end of the file and refused every later
// Drain for the WHOLE spool: no delivery from any file, ever again, and no operator recovery — the
// daemon's own progress was the thing that had to be repaired. Recording the bound the pass read to
// leaves nothing for the crash to invalidate: the segment resumes, and the other spool files, which
// never had anything wrong with them, keep draining.
func TestDrainRecoversFromACrashThatTookAHeldSegmentsUnsyncedTail(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))

	const sess = core.SessionID("sess-crash-tail")
	segPath := walPath(spool, sess, 0)
	segBase := filepath.Base(segPath)
	const otherName = "client-3131.ndjson"
	otherPath := filepath.Join(spool, otherName)
	segLine := func(ts core.UnixMilli) []byte {
		return wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: ts}).line
	}
	clientLine := func(ts core.UnixMilli) []byte {
		return wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-other", TS: ts}).line
	}

	// l1 is covered by a Sync that returned. l2 and l3 are written and not synced: bytes the ingest
	// holds, which the drain may not read and a machine crash may still take.
	l1, l2, l3, l4 := segLine(1), segLine(2), segLine(3), segLine(4)
	require.NoError(t, os.WriteFile(paths.Long(segPath), joinLines(l1, l2, l3), 0o600))
	synced := int64(len(l1))
	// A trailing half line keeps the other file on disk, so a wedge of the spool shows as its completed
	// line never draining.
	c1, c2 := clientLine(5), clientLine(6)
	half := len(c2) / 2
	require.NoError(t, os.WriteFile(paths.Long(otherPath), joinLines(c1, c2[:half]), 0o600))

	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
		IsLive: func(core.SessionID) bool { return true }, // the session is live, so its segment is kept
		SyncedWAL: func(path string) (int64, bool) {
			if filepath.Base(path) != segBase {
				return 0, false
			}
			return synced, true
		},
	})

	n, err := dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n, "the segment's synced line, and the other file's whole line")
	require.Equal(t, []core.UnixMilli{1, 5}, got)
	st := diskDrainState(t, root)
	require.Equal(t, &drainFileState{Size: synced, Offset: synced}, st[segBase],
		"a held segment's progress names the durable bound the pass read to, never the stat size")
	require.Equal(t, &drainFileState{Size: int64(len(c1) + half), Offset: int64(len(c1))}, st[otherName],
		"a file the drain syncs itself records the stat size its own sync covered, exactly as before")
	require.Equal(t, int64(len(l2)+len(l3)+half), dr.GapState().PendingBytes,
		"the bytes the bound withheld are pending all the same, though no progress names them")
	require.Contains(t, dr.GapState().Gaps,
		DrainGap{File: segBase, Kind: DrainGapPending, Count: 1, Reason: "spool bytes not yet replayed"},
		"and the file is reported pending once")

	// The machine crash: the tail the ingest had written and not synced goes with the page cache.
	require.NoError(t, os.Truncate(paths.Long(segPath), synced))
	// The daemon restarts. Its ingest reopens the segment, appends a line and syncs it; the hook that
	// owns the other file completes the line it was part-way through.
	appendSpoolFile(t, segPath, l4)
	synced = spoolFileSize(t, segPath)
	appendSpoolFile(t, otherPath, c2[half:])
	require.Greater(t, int64(len(l1)+len(l2)+len(l3)), spoolFileSize(t, segPath),
		"fixture: the stat size the first pass saw is past the end of the file the crash left")

	n, err = dr.Drain(ctx)
	require.NoError(t, err, "progress that names only durable bytes survives the crash that took the rest")
	require.Equal(t, 2, n)
	require.Equal(t, []core.UnixMilli{1, 5, 4, 6}, got,
		"the pass after the crash delivers the rest of the spool, the other file included")
	require.NoFileExists(t, otherPath, "fully drained, the other file is removed")
	require.Equal(t, &drainFileState{Size: synced, Offset: synced, Done: true}, diskDrainState(t, root)[segBase],
		"the segment is drained to its synced size, and kept: its session is live")
	require.Zero(t, dr.GapState().PendingBytes)

	n, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, []core.UnixMilli{1, 5, 4, 6}, got, "and nothing the crash left is delivered twice")
}

// TestDrainRefusesProgressWhoseFileLostItsDurableBytes is the safety twin. Recording the durable bound
// instead of the stat must widen only the window a machine crash can legitimately leave — the unsynced
// tail, which was never on disk. Every refusal validateProgress carries stands: a file shorter than the
// bytes its progress names is either one that lost durable bytes or a different file under the same
// name, and neither is progress to act on. The pass refuses the whole spool over it, dispatches
// nothing from the healthy file beside it, removes nothing, and leaves state/drain.json byte for byte
// as it found it — the evidence an operator repairs from.
//
// The third case is the one the fix redefines: a held segment, whose recorded size is its synced size
// and not its stat, must still refuse when the file comes back short of that size. Its consumed offset
// is untouched there, so only the recorded-size half of the rule can catch it.
func TestDrainRefusesProgressWhoseFileLostItsDurableBytes(t *testing.T) {
	cases := []struct {
		name string
		wal  bool // a WAL segment the ingest holds: its bound is the synced size, short of the stat
		// image replaces the drained file with what the crash, or whatever else touched it, left.
		image func(t *testing.T, path string, fs *drainFileState, foreign []byte)
	}{
		{
			name: "a file truncated below the bytes the drain consumed",
			image: func(t *testing.T, path string, fs *drainFileState, _ []byte) {
				t.Helper()
				require.NoError(t, os.Truncate(paths.Long(path), fs.Offset-1))
			},
		},
		{
			name: "a file replaced by different bytes, short of the bound its progress names",
			image: func(t *testing.T, path string, fs *drainFileState, foreign []byte) {
				t.Helper()
				require.Greater(t, fs.Size-fs.Offset, int64(1), "fixture: a replacement fits inside the bound")
				before, err := os.ReadFile(paths.Long(path))
				require.NoError(t, err)
				replacement := foreign[:fs.Offset+1] // past the consumed offset, so only the size can catch it
				require.NotEqual(t, before[:len(replacement)], replacement, "fixture: a different file, not the same bytes")
				require.NoError(t, os.WriteFile(paths.Long(path), replacement, 0o600))
			},
		},
		{
			name: "a held segment that comes back short of the synced size it recorded",
			wal:  true,
			image: func(t *testing.T, path string, fs *drainFileState, _ []byte) {
				t.Helper()
				require.Less(t, fs.Offset, fs.Size, "fixture: the bound is past what the pass consumed")
				require.NoError(t, os.Truncate(paths.Long(path), fs.Offset))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			spool := paths.Of(root).Spool
			require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))

			const sess = core.SessionID("sess-twin")
			name := "client-2121.ndjson"
			if tc.wal {
				name = segName(sess, 0)
			}
			path := filepath.Join(spool, name)
			line := func(s core.SessionID, ts core.UnixMilli) []byte {
				return wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: s, TS: ts}).line
			}
			a, b, c := line(sess, 1), line(sess, 2), line(sess, 3)
			half := len(c) / 2
			require.NoError(t, os.WriteFile(paths.Long(path), joinLines(a, b, c[:half]), 0o600))
			// A healthy file, with a trailing half line of its own so it outlives the first pass. The
			// refusal must stop it too: that is the blast radius the wedge had.
			healthyPath := filepath.Join(spool, "client-2222.ndjson")
			h1, h2 := line("sess-healthy", 8), line("sess-healthy", 9)
			healthyHalf := len(h2) / 2
			require.NoError(t, os.WriteFile(paths.Long(healthyPath), joinLines(h1, h2[:healthyHalf]), 0o600))

			// The ingest's Sync covered a and half of b, so a pass over a held segment consumes a alone
			// and records a bound short of the stat size.
			synced := int64(len(a) + len(b)/2)
			var got []core.UnixMilli
			dr := newDrainer(DrainConfig{
				Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
				Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
					got = append(got, r.TS)
					return ipc.Response{OK: true}
				},
				IsLive: func(core.SessionID) bool { return true },
				SyncedWAL: func(p string) (int64, bool) {
					if !tc.wal || filepath.Base(p) != name {
						return 0, false
					}
					return synced, true
				},
			})

			drained := []core.UnixMilli{1, 2, 8}
			wantBound := int64(len(a) + len(b) + half)
			if tc.wal {
				drained, wantBound = []core.UnixMilli{1, 8}, synced
			}
			n, err := dr.Drain(ctx)
			require.NoError(t, err)
			require.Equal(t, len(drained), n)
			require.Equal(t, drained, got)
			fs := diskDrainState(t, root)[name]
			require.NotNil(t, fs, "fixture: the pass recorded the file's progress")
			require.Equal(t, wantBound, fs.Size, "the progress names the durable bound the pass read to")

			before, err := os.ReadFile(paths.Long(drainStatePath(root)))
			require.NoError(t, err)
			var foreign []byte
			for int64(len(foreign)) <= fs.Offset {
				foreign = append(foreign, line("sess-foreign", 7)...)
			}
			appendSpoolFile(t, healthyPath, h2[healthyHalf:]) // the healthy file's line is completed
			tc.image(t, path, fs, foreign)

			n, err = dr.Drain(ctx)
			require.Error(t, err, "a file that no longer holds the durable bytes its progress names refuses the pass")
			require.Zero(t, n)
			require.Equal(t, drained, got, "nothing is dispatched, not even the healthy file's completed line")
			require.Equal(t, []DrainGap{{Kind: DrainGapProgressUnreadable, Count: 1, Reason: "drain progress no longer matches the spool"}},
				dr.GapState().Gaps)
			after, err := os.ReadFile(paths.Long(drainStatePath(root)))
			require.NoError(t, err)
			require.Equal(t, string(before), string(after),
				"a pass that refuses its progress leaves state/drain.json byte for byte as it found it")
			require.FileExists(t, healthyPath, "and removes nothing")
		})
	}
}
