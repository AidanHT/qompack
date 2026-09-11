package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// diskDrainState reads state/drain.json the way a restarted process does: from the bytes on disk,
// never from a drainer's memory. A missing file reads as empty progress.
func diskDrainState(t *testing.T, root string) drainState {
	t.Helper()
	st, err := newDrainer(DrainConfig{Root: root}).loadState()
	require.NoError(t, err)
	return st
}

// spoolFileSize is path's size on disk.
func spoolFileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(paths.Long(path))
	require.NoError(t, err)
	return fi.Size()
}

// closedTwoDeliverySegment accepts two deliveries for sess and closes its segment, the way
// SessionEnd or a restart leaves it: nothing holds it, so a drain that finds the session ended
// removes it. It returns the segment's path and size.
func closedTwoDeliverySegment(t *testing.T, dd *daemon, clk *fakeClock, sess core.SessionID) (string, int64) {
	t.Helper()
	for k := 0; k < 2; k++ {
		clk.Advance(time.Millisecond)
		liveWALAccept(t, dd, liveWALTool(dd, sess))
	}
	require.NoError(t, dd.ing.CloseSession(sess))
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
	return walFile, spoolFileSize(t, walFile)
}

// recreationCases are the three ways a later segment under a drained name compares with the
// drained one's size S. Each failed its own way against a stale {Done, Offset: S, Size: S} entry.
var recreationCases = []struct {
	name     string
	straggle int    // deliveries in the recreated segment; the drained one held two
	size     int    // cmp.Compare(recreated size, S)
	stale    string // what a stale entry did in this case
}{
	{name: "recreated smaller", straggle: 1, size: -1, stale: "failed every Drain for the whole spool"},
	{name: "recreated at the same size", straggle: 2, size: 0, stale: "removed the segment undrained"},
	{name: "recreated larger", straggle: 3, size: +1, stale: "skipped the segment's first S bytes"},
}

// recreateAndRecover recreates sess's drained segment 0 the way the session's own traffic does —
// the ingest reopens the name after a restart mid-session, or a straggler after SessionEnd — with
// straggle ACKed deliveries, checks the recreated size against drained, crashes, and returns the
// TSs that were ACKed and those a restarted drain replays from the spool.
func recreateAndRecover(t *testing.T, dd *daemon, clk *fakeClock, sess core.SessionID, straggle, size int, drained int64) (acked, got []core.UnixMilli, err error) {
	t.Helper()
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
	require.NoFileExists(t, walFile, "fixture: the drained segment was unlinked")
	for k := 0; k < straggle; k++ {
		clk.Advance(time.Millisecond)
		r := liveWALTool(dd, sess)
		liveWALAccept(t, dd, r) // Accept returned: the delivery is ACKed, its WAL line durable
		acked = append(acked, r.TS)
	}
	require.Equal(t, size, cmp.Compare(spoolFileSize(t, walFile), drained), "fixture: the recreated segment's size")
	require.NoError(t, dd.ing.Close()) // crash: nothing processed, nothing drained
	_, err = recordingDrainer(dd.root, clk, &got).Drain(context.Background())
	return acked, got, err
}

// TestDrainForgetsAFileDurablyBeforeUnlinkingIt pins the ordering itself. The drain used to unlink a
// finished spool file first and drop its progress entry afterwards, in memory, for the next
// saveState to persist; a crash in between left state/drain.json naming a file that no longer
// existed, and nothing ever retired that entry. The seam is the unlink: when it is issued, the state
// on disk must already have forgotten the file. Both routes into the removal are covered — the pass
// that reads a file to its end and removes it, and the early path that removes a file an earlier
// pass finished (a live session's segment Stop kept, retired by the next daemon's startup drain).
func TestDrainForgetsAFileDurablyBeforeUnlinkingIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		finishedFirst bool
		dispatched    int
	}{
		{name: "read to its end by the same pass", dispatched: 2},
		{name: "finished by an earlier pass", finishedFirst: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const sess = core.SessionID("sess-forget-first")
			dd, clk := liveWALDaemon(t, nil)
			ctx := context.Background()
			walFile, size := closedTwoDeliverySegment(t, dd, clk, sess)
			base := filepath.Base(walFile)
			if tc.finishedFirst {
				keep := dd.drainConfig()
				keep.IsLive = func(core.SessionID) bool { return true }
				_, err := newDrainer(keep).Drain(ctx)
				require.NoError(t, err)
				require.Equal(t, &drainFileState{Size: size, Offset: size, Done: true}, diskDrainState(t, dd.root)[base],
					"fixture: an earlier pass finished the segment and kept it")
			}

			cfg := dd.drainConfig()
			var namedAtUnlink []bool
			cfg.RemoveWAL = func(path string, drained int64) (bool, error) {
				_, named := diskDrainState(t, dd.root)[base]
				namedAtUnlink = append(namedAtUnlink, named)
				return dd.ing.removeDrainedWAL(path, drained)
			}
			n, err := newDrainer(cfg).Drain(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.dispatched, n)
			require.Equal(t, []bool{false}, namedAtUnlink,
				"when the unlink is issued, state/drain.json must no longer name the file")
			require.NoFileExists(t, walFile)
			require.NotContains(t, diskDrainState(t, dd.root), base)
		})
	}
}

// TestDrainCrashAtTheUnlinkLeavesNoProgressForARecreatedSegment is the crash image the round-2
// reviewer's probe built (drainwal R2-1). The drain unlinks a finished segment, the process dies
// before anything else reaches the disk, and the session's traffic later recreates the name.
// state/drain.json is captured as the unlink is issued and put back after the drain, which is
// exactly what that crash leaves. With the entry still on disk each size relation failed its own
// way (recreationCases). A restarted daemon's drain, the fresh drainer here, must instead fail
// nothing and replay every ACKed line of the recreated segment exactly once.
func TestDrainCrashAtTheUnlinkLeavesNoProgressForARecreatedSegment(t *testing.T) {
	t.Parallel()
	for _, tc := range recreationCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const sess = core.SessionID("sess-crash-at-unlink")
			dd, clk := liveWALDaemon(t, nil)
			walFile, drained := closedTwoDeliverySegment(t, dd, clk, sess)
			statePath := paths.Long(drainStatePath(dd.root))

			var atUnlink []byte
			cfg := dd.drainConfig()
			cfg.RemoveWAL = func(path string, n int64) (bool, error) {
				if b, err := os.ReadFile(statePath); err == nil {
					atUnlink = b
				}
				return dd.ing.removeDrainedWAL(path, n)
			}
			n, err := newDrainer(cfg).Drain(context.Background())
			require.NoError(t, err)
			require.Equal(t, 2, n)
			require.NoFileExists(t, walFile)
			require.NotNil(t, atUnlink, "fixture: the unlink was issued")
			require.NoError(t, os.WriteFile(statePath, atUnlink, 0o600)) // the crash, at the unlink

			acked, got, err := recreateAndRecover(t, dd, clk, sess, tc.straggle, tc.size, drained)
			require.NoError(t, err, "a restarted drain must not fail (a stale entry %s)", tc.stale)
			require.Equal(t, acked, got, "every ACKed line of the recreated segment, exactly once (a stale entry %s)", tc.stale)
			require.NoFileExists(t, walFile, "replayed and held by nobody, the recreated segment is cleaned up")
		})
	}
}

// TestDrainRestoresProgressWhenItDoesNotRemoveTheFile pins the other half of forgetting first: a
// removal that does not happen must put the file's progress back on disk before the pass moves on.
// Otherwise a crash later in the pass leaves the file with no entry, and the restarted drain
// replays every line of it again. The removal can not happen in two ways. The ingest can refuse
// because it holds the segment. The drain asks that first (DrainConfig.HoldsWAL) and leaves a
// segment held all along without forgetting it, so the refusal comes when a straggler reopens the
// segment between that question and the removal. Or the unlink can fail, a Windows sharing
// violation say, injected here so both platforms see it. In both cases nothing holds the segment
// when the drain asks.
//
// The crash image is taken while the pass is on the NEXT spool file, a client fallback the listing
// orders after every WAL segment, and put back afterwards: the restarted drain must replay only the
// deliveries ACKed after the pass read the segment.
func TestDrainRestoresProgressWhenItDoesNotRemoveTheFile(t *testing.T) {
	t.Parallel()
	errUnlink := errors.New("injected: the unlink failed")
	cases := []struct {
		name    string
		reopen  bool // a straggler reopens the segment between the held question and the removal
		remove  func(dd *daemon) func(string, int64) (bool, error)
		wantErr error
	}{
		{
			name:   "the ingest refuses a segment reopened after the drain asked",
			reopen: true,
			remove: func(dd *daemon) func(string, int64) (bool, error) { return dd.ing.removeDrainedWAL },
		},
		{
			name: "the unlink fails",
			remove: func(*daemon) func(string, int64) (bool, error) {
				return func(string, int64) (bool, error) { return false, errUnlink }
			},
			wantErr: errUnlink,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const sess = core.SessionID("sess-not-removed")
			dd, clk := liveWALDaemon(t, nil)
			ctx := context.Background()
			clk.Advance(time.Millisecond)
			liveWALAccept(t, dd, liveWALTool(dd, sess)) // not through the registry: the session is not live
			require.NoError(t, dd.ing.CloseSession(sess))
			walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
			base := filepath.Base(walFile)
			size := spoolFileSize(t, walFile)
			fallback, err := ipc.EncodeRequest(liveWALTool(dd, "sess-client-fallback"))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(paths.Long(filepath.Join(paths.Of(dd.root).Spool, "client-4343.ndjson")), fallback, 0o600))
			statePath := paths.Long(drainStatePath(dd.root))

			cfg := dd.drainConfig()
			remove := tc.remove(dd)
			var namedAtUnlink []bool
			var replay []core.UnixMilli // what the restarted drain must replay, in order
			cfg.RemoveWAL = func(path string, drained int64) (bool, error) {
				_, named := diskDrainState(t, dd.root)[base]
				namedAtUnlink = append(namedAtUnlink, named)
				if tc.reopen {
					clk.Advance(time.Millisecond)
					straggler := liveWALTool(dd, sess)
					liveWALAccept(t, dd, straggler) // reopens segment 0: held again, and grown
					replay = append(replay, straggler.TS)
				}
				return remove(path, drained)
			}
			var image []byte
			dispatch := cfg.Dispatch
			cfg.Dispatch = func(ctx context.Context, r ipc.Request) ipc.Response {
				if r.Session != sess && image == nil {
					if b, err := os.ReadFile(statePath); err == nil {
						image = b
					}
				}
				return dispatch(ctx, r)
			}
			n, err := newDrainer(cfg).Drain(ctx)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 2, n, "the segment's delivery and the client fallback's")
			require.Equal(t, []bool{false}, namedAtUnlink, "the file was forgotten before its unlink was tried")
			require.FileExists(t, walFile)
			require.Equal(t, &drainFileState{Size: size, Offset: size, Done: true}, diskDrainState(t, dd.root)[base],
				"the pass ends with the file's progress back on disk")
			require.NotNil(t, image, "fixture: the pass reached the client fallback")

			// The crash, while the pass was on the client fallback; one more delivery was ACKed first.
			require.NoError(t, os.WriteFile(statePath, image, 0o600))
			clk.Advance(time.Millisecond)
			after := liveWALTool(dd, sess)
			liveWALAccept(t, dd, after)
			replay = append(replay, after.TS)
			require.NoError(t, dd.ing.Close())

			var got []core.UnixMilli
			_, err = recordingDrainer(dd.root, clk, &got).Drain(ctx)
			require.NoError(t, err)
			require.Equal(t, replay, got,
				"the restored progress was already on disk: the drained delivery is not replayed")
		})
	}
}

// TestDrainDoesNotForgetASegmentTheIngestHolds pins the question the drainer asks before it forgets
// a finished segment (DrainConfig.HoldsWAL). RemoveWAL refuses, on every pass, a segment the ingest
// holds for a session the registry does not count live: a straggler after SessionEnd, or an
// EndAbandoned session whose handle stays cached. Forgetting such a segment first cost three
// state/drain.json writes per idle pass instead of one: the forget, the restore, and the end of the
// pass. Each pass also reopened the window between the forget and the restore, in which a crash
// leaves the file with no progress. Asked first, the drainer leaves the segment alone: RemoveWAL is
// never reached, and an idle pass writes nothing for it before the next file starts. Before that
// pass, state/drain.json is rewritten indented, a form saveState never produces, so any save in
// between shows.
func TestDrainDoesNotForgetASegmentTheIngestHolds(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-held-not-live")
	dd, clk := liveWALDaemon(t, nil)
	ctx := context.Background()
	clk.Advance(time.Millisecond)
	liveWALAccept(t, dd, liveWALTool(dd, sess)) // held by the ingest; never registered, so not live
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
	base := filepath.Base(walFile)
	size := spoolFileSize(t, walFile)
	statePath := paths.Long(drainStatePath(dd.root))

	cfg := dd.drainConfig()
	asked := 0
	cfg.RemoveWAL = func(path string, drained int64) (bool, error) {
		asked++
		return dd.ing.removeDrainedWAL(path, drained)
	}
	var atNextFile []byte
	dispatch := cfg.Dispatch
	cfg.Dispatch = func(ctx context.Context, r ipc.Request) ipc.Response {
		if r.Session != sess && atNextFile == nil {
			b, err := os.ReadFile(statePath)
			require.NoError(t, err)
			atNextFile = b
		}
		return dispatch(ctx, r)
	}
	dr := newDrainer(cfg)

	n, err := dr.Drain(ctx) // reads the segment to its end
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Zero(t, asked, "a segment the ingest holds is never offered to RemoveWAL")
	want := &drainFileState{Size: size, Offset: size, Done: true}
	require.Equal(t, want, diskDrainState(t, dd.root)[base])

	indented, err := json.MarshalIndent(diskDrainState(t, dd.root), "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(statePath, indented, 0o600))
	fallback, err := ipc.EncodeRequest(liveWALTool(dd, "sess-client-fallback"))
	require.NoError(t, err)
	fallbackFile := filepath.Join(paths.Of(dd.root).Spool, "client-4646.ndjson")
	require.NoError(t, os.WriteFile(paths.Long(fallbackFile), fallback, 0o600))

	n, err = dr.Drain(ctx) // an idle pass over the held, finished segment
	require.NoError(t, err)
	require.Equal(t, 1, n, "only the client fallback's delivery")
	require.Zero(t, asked, "a segment the ingest holds is never offered to RemoveWAL")
	require.Equal(t, string(indented), string(atNextFile),
		"the idle pass must not write state/drain.json for the held segment before the next file starts")
	require.FileExists(t, walFile)
	require.NoFileExists(t, fallbackFile)
	require.Equal(t, want, diskDrainState(t, dd.root)[base], "the held segment's progress is never given up")
}

// TestDrainCrashBetweenForgettingAndUnlinkingReplaysOnlyWhatTheFrontierLacks pins the window the
// ordering opens, so its consequence stays what the design claims. A crash after the file is
// forgotten and before it is unlinked leaves the file with no progress entry, and the restarted
// drain reads it again from offset zero. For a leased line that costs nothing: the committed
// frontier names it, and the drain advances past an acknowledged copy without dispatching it
// (TestDrainDoesNotRedeliverAnAcknowledgedClientCopy). Both deliveries here went through the live
// path — leased, published, acknowledged — so the restarted drain must dispatch neither, and then
// release the segment.
func TestDrainCrashBetweenForgettingAndUnlinkingReplaysOnlyWhatTheFrontierLacks(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	ctx := context.Background()

	const sess = "sess-forgotten-not-unlinked"
	tokens := []string{testDeliveryToken('c'), testDeliveryToken('d')}
	for _, token := range tokens {
		require.True(t, dd.dispatchOp(ctx, observeRequest(token, sess, `{"hook_event_name":"PostToolUse"}`)).OK)
	}
	drainRing(t, dd)
	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	for _, token := range tokens {
		require.True(t, journal.acknowledged(token), "fixture: the live path committed the frontier")
	}
	require.Equal(t, 2, calls(), "fixture: the live path published both")
	require.NoError(t, dd.ing.CloseSession(sess))
	walFile := walPath(paths.Of(root).Spool, sess, 0)
	base := filepath.Base(walFile)
	statePath := paths.Long(drainStatePath(root))

	cfg := dd.drainConfig()
	cfg.IsLive = func(core.SessionID) bool { return false } // the session has ended
	var image []byte
	cfg.RemoveWAL = func(string, int64) (bool, error) {
		if b, err := os.ReadFile(statePath); err == nil {
			image = b
		}
		return false, nil // the process dies here: the unlink is never issued
	}
	_, err = newDrainer(cfg).Drain(ctx)
	require.NoError(t, err)
	require.NotNil(t, image, "fixture: the drain reached the unlink")
	require.NoError(t, os.WriteFile(statePath, image, 0o600))
	require.NotContains(t, diskDrainState(t, root), base, "the crash image: the file is forgotten")
	require.FileExists(t, walFile, "the crash image: the file is still there")

	// The restart: a fresh seen set, the same durable frontier.
	restarted := newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: newSeenSet(seenCapacity), Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal,
	})
	n, err := restarted.Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 2, calls(), "a line the frontier already names is read again but not dispatched again")
	require.NoFileExists(t, walFile)
	require.True(t, restarted.GapState().Complete, "reading an acknowledged line again is not a gap")
}

// TestDrainStartupForgetsStaleProgressBeforeTheSessionRecreatesItsSegment covers progress already on
// disk: state the old ordering wrote, whose crash left {Done, Offset: S, Size: S} for a segment
// that is gone. It is the round-2 reviewer's likeliest trigger (drainwal R2-1). Stop keeps a live
// session's segment. The next daemon's startup drain unlinks it and dies before its save. The
// session's own traffic then reaches a later daemon, whose ingest recreates the name. That later
// daemon's startup drain runs before it serves anything, so it must forget the entry, on disk,
// before the name can come back.
func TestDrainStartupForgetsStaleProgressBeforeTheSessionRecreatesItsSegment(t *testing.T) {
	t.Parallel()
	for _, tc := range recreationCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const sess = core.SessionID("sess-stale-on-disk")
			dd, clk := liveWALDaemon(t, nil)
			ctx := context.Background()
			walFile, drained := closedTwoDeliverySegment(t, dd, clk, sess)
			base := filepath.Base(walFile)
			statePath := paths.Long(drainStatePath(dd.root))

			keep := dd.drainConfig()
			keep.IsLive = func(core.SessionID) bool { return true } // Stop keeps a live session's segment
			_, err := newDrainer(keep).Drain(ctx)
			require.NoError(t, err)
			stale, err := os.ReadFile(statePath)
			require.NoError(t, err)
			_, err = newDrainer(dd.drainConfig()).Drain(ctx) // the next daemon retires the segment...
			require.NoError(t, err)
			require.NoFileExists(t, walFile)
			require.NoError(t, os.WriteFile(statePath, stale, 0o600)) // ...and its save never lands
			require.Equal(t, &drainFileState{Size: drained, Offset: drained, Done: true}, diskDrainState(t, dd.root)[base],
				"fixture: the entry outlived its file")

			n, err := newDrainer(dd.drainConfig()).Drain(ctx) // the later daemon's startup drain
			require.NoError(t, err)
			require.Zero(t, n)
			require.NotContains(t, diskDrainState(t, dd.root), base,
				"the startup drain must forget, on disk, the progress of a finished file that is gone")

			acked, got, err := recreateAndRecover(t, dd, clk, sess, tc.straggle, tc.size, drained)
			require.NoError(t, err, "a later drain must not fail (a stale entry %s)", tc.stale)
			require.Equal(t, acked, got, "every ACKed line of the recreated segment, exactly once (a stale entry %s)", tc.stale)
			require.NoFileExists(t, walFile)
		})
	}
}

// TestDrainForgetsReleasedProgressForMissingFilesBeforeReadingAny pins the start-of-pass rule on its
// own terms. An entry is forgotten when three things hold: its file is finished (Done), it carries
// no cleanup intent, and it is absent from the pass's spool listing. The forgetting is persisted
// before a single line is dispatched. The other entries are handled this way:
//   - An entry with cleanup intents stays until they are consumed: an intent is the only record of a
//     blob the drain must still remove. Here the blob is still referenced by the one file left in
//     the spool, the lost-ACK shape of two copies of one delivery.
//   - An entry whose last intent the start-of-pass cleanup consumes is forgotten before the first
//     line too. That is the old ordering's crash image for a blob-backed file, whose blob went before
//     its unlink.
//   - An unfinished entry stays. The drain never removes an unfinished file, so the file's absence
//     is not a removal the drain made.
func TestDrainForgetsReleasedProgressForMissingFilesBeforeReadingAny(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))

	payload := []byte(`{"shared":"externalized payload"}`)
	const blobName = "blob-still-referenced.bin"
	blobPath := filepath.Join(spool, blobName)
	require.NoError(t, os.WriteFile(paths.Long(blobPath), payload, 0o600))
	ref, err := json.Marshal(blobRef{Blob: blobName, Bytes: len(payload), Field: drainBlobToolResponse})
	require.NoError(t, err)
	line, err := ipc.EncodeRequest(ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-present", TS: core.UnixMilli(51), Event: &hookio.Event{}, Raw: ref,
	})
	require.NoError(t, err)
	present := filepath.Join(spool, "client-present.ndjson")
	require.NoError(t, os.WriteFile(paths.Long(present), line, 0o600))

	const (
		goneWAL         = "wal-sess-gone.ndjson"
		goneClient      = "client-4040.ndjson"
		goneWithIntent  = "wal-sess-gone-with-intent.ndjson"
		goneBlobRemoved = "wal-sess-gone-blob-removed.ndjson"
		goneUnfinished  = "client-4141.ndjson"
	)
	unfinished := drainFileState{Size: 300, Offset: 120}
	require.NoError(t, newDrainer(DrainConfig{Root: root}).saveState(drainState{
		goneWAL:         {Size: 752, Offset: 752, Done: true},
		goneClient:      {Size: 376, Offset: 376, Done: true},
		goneWithIntent:  {Size: 400, Offset: 400, Done: true, PendingBlobs: []string{blobName}},
		goneBlobRemoved: {Size: 500, Offset: 500, Done: true, PendingBlobs: []string{"blob-already-removed.bin"}},
		goneUnfinished:  &unfinished,
	}))

	var atFirstDispatch drainState
	dispatches := 0
	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: func(context.Context, ipc.Request) ipc.Response {
		dispatches++
		if atFirstDispatch == nil {
			atFirstDispatch = diskDrainState(t, root)
		}
		return ipc.Response{OK: true}
	}})
	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, dispatches)

	require.NotContains(t, atFirstDispatch, goneWAL, "finished, no intent, gone: forgotten before the first line")
	require.NotContains(t, atFirstDispatch, goneClient, "finished, no intent, gone: forgotten before the first line")
	require.NotContains(t, atFirstDispatch, goneBlobRemoved,
		"the start-of-pass cleanup consumed its last intent: forgotten before the first line")
	require.Contains(t, atFirstDispatch, goneWithIntent, "an entry with a cleanup intent stays")
	require.Equal(t, []string{blobName}, atFirstDispatch[goneWithIntent].PendingBlobs)
	require.Equal(t, &unfinished, atFirstDispatch[goneUnfinished], "an unfinished entry stays")

	require.NoFileExists(t, blobPath, "the kept intent is honoured once its last reference is consumed")
	require.NoFileExists(t, present)
	require.Equal(t, &drainFileState{Size: 400, Offset: 400, Done: true}, diskDrainState(t, root)[goneWithIntent],
		"its intent consumed, the entry is released")

	n, err = dr.Drain(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	final := diskDrainState(t, root)
	require.NotContains(t, final, goneWithIntent, "released and gone, the next pass forgets it")
	require.Equal(t, &unfinished, final[goneUnfinished])
}

// TestDrainForgetsAFileAlreadyGoneAtItsRemoval pins removeCompletedFile's not-exist answer. A file
// that vanishes between the pass's stat and its removal is gone, and its entry, forgotten on disk
// before the removal was tried, must stay forgotten. Putting it back, as the drain once did, wrote
// {Done, Offset: S, Size: S} to disk for a name with no file, and the name's next file inherited it:
// the same wedge as a crash at the unlink. Only something other than the drainer deletes a spool
// file, an operator say; the seam at the removal stands in for it.
func TestDrainForgetsAFileAlreadyGoneAtItsRemoval(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-gone-at-removal")
	dd, clk := liveWALDaemon(t, nil)
	walFile, drained := closedTwoDeliverySegment(t, dd, clk, sess)
	base := filepath.Base(walFile)

	cfg := dd.drainConfig()
	cfg.RemoveWAL = func(path string, n int64) (bool, error) {
		require.NoError(t, os.Remove(paths.Long(path))) // gone before the drain's own removal
		return dd.ing.removeDrainedWAL(path, n)
	}
	n, err := newDrainer(cfg).Drain(context.Background())
	require.NoError(t, err, "a file already gone is not a file error")
	require.Equal(t, 2, n)
	require.NoFileExists(t, walFile)
	require.NotContains(t, diskDrainState(t, dd.root), base,
		"a file already gone at its removal must leave no progress on disk for the name's next file")

	smaller := recreationCases[0]
	acked, got, err := recreateAndRecover(t, dd, clk, sess, smaller.straggle, smaller.size, drained)
	require.NoError(t, err, "a restarted drain must not fail (a stale entry %s)", smaller.stale)
	require.Equal(t, acked, got, "every ACKed line of the recreated segment, exactly once")
}

// TestDrainKeepsProgressWhenItCannotPersistTheForgetting pins removeCompletedFile's other early
// exit: the forgetting itself not reaching the disk. Nothing may then be unlinked, because the
// unlink is safe only once the forgetting is durable. The entry must also go back into memory, so
// the next save puts it back on disk; dropped there, it would make the next pass read the whole file
// again. The save is made to fail by turning .qompack/tmp, where paths.WriteAtomic stages, into a
// regular file. IsLive does that, because shouldDelete asks it just before the forgetting is saved,
// and the next file's first dispatch undoes it.
func TestDrainKeepsProgressWhenItCannotPersistTheForgetting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	walFile := writeSpoolFile(t, root, "wal-sess-save-fails.ndjson", 2)
	writeSpoolFile(t, root, "client-8080.ndjson", 1)
	base := filepath.Base(walFile)
	size := spoolFileSize(t, walFile)
	tmp := paths.Long(paths.Of(root).Tmp)

	sabotaged, unlinks := false, 0
	dr := newDrainer(DrainConfig{
		Root: root, Clock: newFakeClock(epoch),
		IsLive: func(core.SessionID) bool {
			require.NoError(t, os.RemoveAll(tmp))
			require.NoError(t, os.WriteFile(tmp, nil, 0o600)) // where WriteAtomic stages, now a file
			sabotaged = true
			return false
		},
		RemoveWAL: func(string, int64) (bool, error) { unlinks++; return false, nil },
		Dispatch: func(context.Context, ipc.Request) ipc.Response {
			if sabotaged {
				require.NoError(t, os.Remove(tmp))
				require.NoError(t, os.MkdirAll(tmp, 0o700))
				sabotaged = false
			}
			return ipc.Response{OK: true}
		},
	})
	n, err := dr.Drain(context.Background())
	require.Error(t, err, "fixture: the forgetting could not be saved")
	require.Equal(t, 3, n)
	require.Zero(t, unlinks, "nothing is unlinked until its forgetting is on disk")
	require.FileExists(t, walFile)
	require.Equal(t, &drainFileState{Size: size, Offset: size, Done: true}, diskDrainState(t, root)[base],
		"the entry went back into memory, and the next save put it back on disk")
}

// TestDrainRejectsMismatchedProgressBeforePruningIt pins where the start-of-pass prune runs: after
// validateProgress. A pass that refuses the progress it loaded must leave state/drain.json exactly as
// it found it, the evidence an operator repairs from, as a refusal by loadState does
// (TestDrainRejectsInvalidPersistedStateBeforeTouchingRecoveryResources). The state holds one entry
// of each kind: a released one whose file is gone, which the prune would forget, and one recording
// more bytes than its file holds, which validateProgress refuses. It is written indented, a form
// saveState never produces, so a save of any content shows.
func TestDrainRejectsMismatchedProgressBeforePruningIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	present := writeSpoolFile(t, root, "client-9090.ndjson", 1)
	size := spoolFileSize(t, present)
	st := drainState{"wal-sess-gone.ndjson": {Size: 752, Offset: 752, Done: true}} // released, its file gone
	st[filepath.Base(present)] = &drainFileState{Size: size + 1, Offset: size + 1, Done: true}
	state, err := json.MarshalIndent(st, "", "  ")
	require.NoError(t, err)
	statePath := drainStatePath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(statePath)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(statePath), state, 0o600))

	dispatches := 0
	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: func(context.Context, ipc.Request) ipc.Response {
		dispatches++
		return ipc.Response{OK: true}
	}})
	n, err := dr.Drain(context.Background())
	require.Error(t, err)
	require.Zero(t, n)
	require.Zero(t, dispatches)
	require.Equal(t, []DrainGap{{Kind: DrainGapProgressUnreadable, Count: 1, Reason: "drain progress no longer matches the spool"}},
		dr.GapState().Gaps, "fixture: validateProgress refused the pass")
	after, err := os.ReadFile(paths.Long(statePath))
	require.NoError(t, err)
	require.Equal(t, string(state), string(after),
		"a pass that refuses its progress must leave state/drain.json byte for byte as it found it")
	require.FileExists(t, present)
}

// TestDrainKeepsCleanupIntentsForAFileDeletedAfterTheListing pins drainFile's answer to a listed
// file that is gone when the pass stats it: something other than the drainer deleted it while the
// pass was on an earlier file, an operator say. Its entry is dropped only if it carries no cleanup
// intent, the start-of-pass prune's rule for intents (forgetReleased). An intent that outlives a pass
// is the only record of a blob the drain must still remove, and dropped with the entry, that blob
// stayed on disk for good. The blob here cannot be removed yet: it is a non-empty directory, a
// removal both platforms refuse. Once it can be, the next pass removes it and forgets the entry.
func TestDrainKeepsCleanupIntentsForAFileDeletedAfterTheListing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	earlier := writeSpoolFile(t, root, "client-6060.ndjson", 1)
	gone := writeSpoolFile(t, root, "client-7070.ndjson", 1)
	base := filepath.Base(gone)
	size := spoolFileSize(t, gone)
	const blobName = "blob-7070-0.bin"
	blobPath := filepath.Join(paths.Of(root).Spool, blobName)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(blobPath, "keep")), 0o700)) // not removable yet
	intent := drainFileState{Size: size, Offset: size, Done: true, PendingBlobs: []string{blobName}}
	require.NoError(t, newDrainer(DrainConfig{Root: root}).saveState(drainState{base: &intent}))

	deleted := false
	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: func(context.Context, ipc.Request) ipc.Response {
		if !deleted {
			require.NoError(t, os.Remove(paths.Long(gone))) // listed, then deleted before its stat
			deleted = true
		}
		return ipc.Response{OK: true}
	}})
	n, err := dr.Drain(context.Background())
	require.Error(t, err, "fixture: the blob could not be removed")
	require.Equal(t, 1, n, "the earlier file's line")
	require.True(t, deleted, "fixture: the file was deleted while the pass was on the earlier file")
	require.Equal(t, &intent, diskDrainState(t, root)[base],
		"a file gone before its stat must not take its cleanup intent with it")

	require.NoError(t, os.RemoveAll(paths.Long(blobPath)))
	require.NoError(t, os.WriteFile(paths.Long(blobPath), []byte(`{"removable":true}`), 0o600))
	n, err = dr.Drain(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoFileExists(t, blobPath, "the kept intent removes its blob")
	require.NoFileExists(t, earlier)
	require.NotContains(t, diskDrainState(t, root), base, "its intent consumed and its file gone, the entry is forgotten")
}
