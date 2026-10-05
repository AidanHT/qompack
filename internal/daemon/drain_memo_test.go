package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out C1.13, wave 20: what a drainer remembers between passes about a spool behind a waiting
// head (spoolMemo), and what a pass that skips such lines must still get right. The cost rows are in
// drain_pass_cost_test.go.

// logCount is how many entries at level carry msg.
func logCount(log *recordingLogger, level, msg string) int {
	n := 0
	for _, e := range log.entries(level) {
		if e.Msg == msg {
			n++
		}
	}
	return n
}

// TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames: a corrupt line behind a waiting head
// stays ahead of its file's consumed front, and each pass checks the spool from the fronts on for
// references to the blobs whose cleanup intents wait (cleanupAcknowledged). That check failed on a line
// that does not decode, so while any intent waited, every pass returned an error, counted a file error
// and logged one for each file it finished, and no blob was ever collected: the corrupt line, announced
// once, was reported again on every pass by another route. A line that does not decode is never
// dispatched; it holds back the blobs it names, in case a binary that can decode it replays it, and no
// others. It names a blob as an encoder writes the name, which escapes the & a blob's name may carry.
func TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames(t *testing.T) {
	const blob = "blob-8891-1&a.bin"
	encoded, err := json.Marshal(blob)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), blob, "fixture: the encoder escapes the name's &")
	for _, c := range []struct {
		name, corrupt string
		names         bool
	}{
		{"names no blob", "not a request\n", false},
		{"names the blob", `not a request, though it names "` + blob + "\"\n", true},
		{"names the blob as an encoder escapes it", "not a request, though it names " + string(encoded) + "\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := contentDrainConfig(dd)
			log := newRecordingLogger()
			cfg.Log = log
			dr := newDrainer(cfg)
			dd.drain.Store(dr)
			const done = "client-8891.ndjson"
			head := blockedSpoolHead(t, dd, root, "sess-refs-stuck", 0)
			writeRawSpool(t, root, "client-8890.ndjson", hookSpoolLine(t, head), []byte(c.corrupt))
			published := blobSpoolLine(t, root, liveOrderTool(dd, root, "sess-refs-blob", 7), blob)
			writeHookSpool(t, root, done, published)

			for pass := 1; pass <= 3; pass++ {
				_, err := dr.Drain(ctx)
				require.NoError(t, err, "pass %d", pass)
			}
			require.True(t, spoolWatchPublished(dd, published.Nonce), "fixture: the blob line was published")
			require.Equal(t, int64(1), dd.m.Counter(counterDrainFileError).Value(),
				"the corrupt line is counted once, and nothing else is a file error")
			require.Zero(t, logCount(log, logWarn, "daemon: drain: file error"))
			blobPath := filepath.Join(paths.Of(root).Spool, blob)
			if c.names {
				require.FileExists(t, blobPath, "a blob a line ahead of a front names stays, though the line does not decode")
				return
			}
			require.NoFileExists(t, blobPath, "a corrupt line that names no blob holds none back")
			require.True(t, spoolWatchGone(root, done), "and the spool whose intent it was is released")
		})
	}
}

// TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames: a hook killed in the middle of its
// append leaves a client spool ending in a partial line, with no newline. While any cleanup intent
// waited, each pass's check of the spool for references to the intents' blobs (scanPendingBlobs)
// refused that file, so every pass returned an error and no blob was collected until a hook completed
// the line, which a killed hook never does (D67(e), audit 2 #75). The check skips a trailing partial
// line as it skips one that does not decode: it holds back the pending blobs whose names it carries,
// in case the line is still being written, and no others.
func TestDrain_ATrailingPartialLineHoldsBackOnlyTheBlobsItNames(t *testing.T) {
	const blob = "blob-8893-1&a.bin"
	encoded, err := json.Marshal(blob)
	require.NoError(t, err)
	for _, c := range []struct {
		name, partial string
		names         bool
	}{
		{"names no blob", `{"op":"observe.tool","s":"sess-partial`, false},
		{"names the blob", `{"op":"observe.tool","r":{"blob":"` + blob, true},
		{"names the blob as an encoder escapes it", `{"op":"observe.tool","r":{"blob":` + string(encoded), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := contentDrainConfig(dd)
			log := newRecordingLogger()
			cfg.Log = log
			dr := newDrainer(cfg)
			dd.drain.Store(dr)
			const done = "client-8893.ndjson"
			head := blockedSpoolHead(t, dd, root, "sess-partial-stuck", 0)
			writeRawSpool(t, root, "client-8892.ndjson", hookSpoolLine(t, head), []byte(c.partial))
			published := blobSpoolLine(t, root, liveOrderTool(dd, root, "sess-partial-blob", 7), blob)
			writeHookSpool(t, root, done, published)

			for pass := 1; pass <= 3; pass++ {
				_, err := dr.Drain(ctx)
				require.NoError(t, err, "pass %d", pass)
			}
			require.True(t, spoolWatchPublished(dd, published.Nonce), "fixture: the blob line was published")
			require.Zero(t, dd.m.Counter(counterDrainFileError).Value(), "a partial line is no file error")
			require.Zero(t, logCount(log, logWarn, "daemon: drain: file error"))
			blobPath := filepath.Join(paths.Of(root).Spool, blob)
			if c.names {
				require.FileExists(t, blobPath, "a blob a trailing partial line names stays: the line may still be written")
				return
			}
			require.NoFileExists(t, blobPath, "a trailing partial line that names no blob holds none back")
			require.True(t, spoolWatchGone(root, done), "and the spool whose intent it was is released")
		})
	}
}

// TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent: a line consumed
// without being published leaves its blob's name as a cleanup intent, and the drain names that blob
// from the line's descriptor (pendingBlobOf), which a hostile spool line writes. A name that leaves the
// spool directory must leave no intent: the progress refuses an unsafe intent, so every later pass would
// fail before reading a spool file, and nothing outside the spool may be removed.
//
// pendingBlobOf repeats every check readBlob makes before it reads a body, so a line names an intent
// only for a blob readBlob would have read (audit 2 #2): not for a directory under a blob's name (a
// non-empty one would fail removeBlob, and with it every pass's cleanup), not for a file whose size is
// not the descriptor's, and not for a descriptor with no event. Each case is consumed as denied behind
// a waiting head, and the entry it names survives.
func TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent(t *testing.T) {
	body := []byte("hostile")
	for _, c := range []struct {
		name string
		// blob is the descriptor's name for the blob, and entry what the row puts there, relative to
		// the spool directory.
		blob, entry string
		// dir makes entry a non-empty directory; otherwise it is a file holding body.
		dir bool
		// bytes is the descriptor's size relative to the entry's own: 0 names the entry's size.
		bytes int
		// noEvent strips the line's event, so only its descriptor is left.
		noEvent bool
	}{
		{name: "a name outside the spool directory", blob: "../outside.bin", entry: filepath.Join("..", "outside.bin")},
		{name: "a directory under a blob's name", blob: "blob-8981-1.bin", entry: "blob-8981-1.bin", dir: true},
		{name: "a blob whose size is not the descriptor's", blob: "blob-8981-2.bin", entry: "blob-8981-2.bin", bytes: 1},
		{name: "a descriptor with no event", blob: "blob-8981-3.bin", entry: "blob-8981-3.bin", noEvent: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			const base = "client-8981.ndjson"
			hostile := liveOrderTool(dd, root, "sess-outside", 9)
			cfg := contentDrainConfig(dd)
			cfg.Admit = denyNonce(cfg.Admit, hostile.Nonce)
			dr := newDrainer(cfg)
			dd.drain.Store(dr)
			spool := paths.Of(root).Spool
			entry := filepath.Join(spool, c.entry)
			require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
			if c.dir {
				require.NoError(t, os.MkdirAll(paths.Long(entry), 0o700))
				require.NoError(t, os.WriteFile(paths.Long(filepath.Join(entry, "inside")), body, 0o600))
			} else {
				require.NoError(t, os.WriteFile(paths.Long(entry), body, 0o600))
			}
			fi, err := os.Lstat(paths.Long(entry))
			require.NoError(t, err)
			ref, err := json.Marshal(blobRef{
				Blob: c.blob, Bytes: int(fi.Size()) + c.bytes, Field: drainBlobToolResponse, X: hostile.Raw,
			})
			require.NoError(t, err)
			ev := *hostile.Event
			ev.ToolResponse = nil
			hostile.Event, hostile.Raw = &ev, ref
			if c.noEvent {
				hostile.Event = nil
			}
			head := blockedSpoolHead(t, dd, root, "sess-outside-stuck", 0)
			writeHookSpool(t, root, base, head, hostile)

			for pass := 1; pass <= 2; pass++ {
				_, err = dr.DrainClientSpools(ctx)
				require.NoError(t, err, "pass %d loads its progress and cleans up", pass)
			}
			st, err := dr.loadState()
			require.NoError(t, err)
			require.Contains(t, st, base, "fixture: the spool stays behind its waiting head")
			require.Equal(t, int64(len(hookSpoolLine(t, head))+len(hookSpoolLine(t, hostile))), st[base].Size,
				"fixture: both passes read the whole spool")
			require.Empty(t, st[base].PendingBlobs, "a blob readBlob would not have read is no cleanup intent")
			_, err = os.Lstat(paths.Long(entry))
			require.NoError(t, err, "the entry the line names survives")
		})
	}
}

// TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered: a pass remembers the lines it
// consumed behind a waiting head (spoolMemo), whose cleanup intents its progress write carries. When
// that write fails, the progress on disk is the drainer's own earlier write, and a later pass that found
// it there and kept the memo would skip the blob line whose intent never reached disk: the blob would
// stay for the daemon's lifetime. A failed write forgets every memo, so the later pass consumes the line
// again and records its intent.
func TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	const base, blob = "client-8951.ndjson", "blob-8951-1.bin"
	head := blockedSpoolHead(t, dd, root, "sess-savefail-stuck", 0)
	other := blobSpoolLine(t, root, liveOrderTool(dd, root, "sess-savefail-other", 7), blob)
	tmp := paths.Long(paths.Of(root).Tmp)
	var broke atomic.Bool
	var breakErr error
	cfg := contentDrainConfig(dd)
	dispatch := cfg.Dispatch
	cfg.Dispatch = func(c context.Context, req ipc.Request) ipc.Response {
		resp := dispatch(c, req)
		if req.Nonce == other.Nonce && broke.CompareAndSwap(false, true) {
			// Until the test puts it back, nothing can stage a write under .qompack/tmp, so the pass's
			// progress write fails once the line is published.
			breakErr = errors.Join(os.RemoveAll(tmp), os.WriteFile(tmp, []byte("not a directory"), 0o600))
		}
		return resp
	}
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	writeHookSpool(t, root, base, head)
	_, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err, "fixture: the first pass writes the drainer's own progress")

	w, err := paths.AppendOnly(filepath.Join(paths.Of(root).Spool, base))
	require.NoError(t, err)
	_, err = w.Write(hookSpoolLine(t, other))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	_, err = dr.DrainClientSpools(ctx)
	require.True(t, broke.Load(), "fixture: the blob line was dispatched")
	require.NoError(t, breakErr)
	require.NoError(t, os.Remove(tmp))
	require.Error(t, err, "fixture: the second pass's progress write failed")
	require.True(t, spoolWatchPublished(dd, other.Nonce), "fixture: the second pass published the blob line")

	_, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Equal(t, []string{blob}, st[base].PendingBlobs, "the blob's intent reaches the progress after the failed write")
}

// TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce: a drainer remembers at most
// orderingProcessedCap lines consumed behind a waiting head per file (spoolMemo), the bound drainFile
// keeps on the lines its front may roll over. A line past that bound is consumed again in full by every
// later pass: admitted again, as this row pins, since the drain documents that bound. It is not counted
// or announced again: every pass counted and announced each such line, Loud for one admission could not
// decide, so a waiting head ahead of a large backlog put the same lines into LOUD.log on every pass.
// That holds after a pass whose spent budget stopped it early, too, which read only the start of the
// spool: it does not shorten what the passes before it had read and announced. And it holds behind a
// refused line whose lease the journal holds, which every pass leaves where it is.
func TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce(t *testing.T) {
	const pastCap = 3
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := contentDrainConfig(dd)
	log := newRecordingLogger()
	cfg.Log = log
	const undecided, released, held core.SessionID = "sess-cap-undecided", "sess-cap-released", "sess-cap-held"
	admit := cfg.Admit
	cfg.Admit = func(req ipc.Request) admissionVerdict {
		if req.Session == undecided || req.Session == held {
			return admissionVerdict{Request: req, Failed: true, Reason: "test policy unavailable"}
		}
		return admit(req)
	}
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	head := blockedSpoolHead(t, dd, root, "sess-cap-stuck", 20)
	p0 := spD3Prompt(dd, root, released, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // leased; its job waits on the ring for a worker, and none runs
	next := liveOrderTool(dd, root, released, 1)
	// A refused line whose lease the journal holds stays unconsumed, and nothing announces it, on every
	// pass; no pass can consume it as unadmitted, so it does not hold back what the passes have announced.
	heldRefused := liveOrderTool(dd, root, held, 50)
	_, ok := dd.ing.leaseDelivery(ctx, heldRefused)
	require.True(t, ok)
	lines := [][]byte{hookSpoolLine(t, head), hookSpoolLine(t, next), hookSpoolLine(t, heldRefused)}
	for range orderingProcessedCap {
		lines = append(lines, []byte("not a request\n"))
	}
	for i := range pastCap {
		lines = append(lines, hookSpoolLine(t, liveOrderTool(dd, root, undecided, 60+i)))
	}
	writeRawSpool(t, root, "client-8831.ndjson", lines...)

	announcedOnce := func(when string) {
		t.Helper()
		require.Equal(t, int64(orderingProcessedCap), dd.m.Counter(counterDrainFileError).Value(),
			"%s each corrupt line is counted once", when)
		require.Equal(t, int64(pastCap), dd.m.Counter(counterDrainUnadmitted).Value(),
			"%s each line past the cap is counted once", when)
		require.Equal(t, orderingProcessedCap, logCount(log, logWarn, "daemon: drain: corrupt line"),
			"%s each corrupt line is announced once", when)
		require.Equal(t, pastCap, logCount(log, logLoud, "daemon: drain: capture not admitted; record skipped"),
			"%s each line past the cap is announced once", when)
	}
	for pass := 1; pass <= 3; pass++ {
		_, err := dr.DrainClientSpools(ctx)
		require.NoError(t, err)
		announcedOnce(fmt.Sprintf("after pass %d", pass))
		require.Equal(t, int64(pass), dd.m.Counter(counterDrainLeasedDenyPending).Value(),
			"fixture: pass %d leaves the refused line its lease holds", pass)
		require.Equal(t, pastCap, meter.take().admitted[undecided],
			"pass %d admits each line past the cap: a drainer remembers orderingProcessedCap lines per file", pass)
	}

	// A worker publishes p0, which releases next; a pass whose budget is spent publishes next, which is
	// progress, and stops there, having read nothing past it.
	select {
	case job := <-dd.ing.ring:
		dd.ing.dispatch(context.Background(), dd.runIngested, job)
	default:
		t.Fatal("fixture: p0's job is not queued")
	}
	_, err := dr.DrainClientSpools(withPassBudget(ctx, 0))
	require.ErrorIs(t, err, errPassBudgetSpent, "fixture: the pass made progress, and its spent budget stopped it")
	require.True(t, spoolWatchPublished(dd, next.Nonce), "fixture: the stopped pass published the line p0 released")
	require.Zero(t, meter.take().admitted[undecided], "fixture: the stopped pass read nothing past that line")
	// The memo carries forward what earlier passes remembered past the stop, still within the cap.
	memo := dr.memo["client-8831.ndjson"]
	require.NotNil(t, memo, "fixture: the stopped pass remembers the spool")
	require.LessOrEqual(t, len(memo.consumed), orderingProcessedCap,
		"a memo holds at most orderingProcessedCap lines per file, across a pass that stopped early")
	_, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	announcedOnce("after a pass that stopped early and the pass after it,")
}

// TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt: a line a pass consumes as
// unadmitted is announced once per memo life, and spoolMemo.readTo says how far the passes before it
// read, announcing each such line they consumed. A pass can also read a line behind a waiting head and
// leave it, unconsumed and unannounced, under no lease: its lease lookup fails, its lease cannot be
// taken, or, with no nonce to lease it by, its dispatch fails or it waits on a delivery another handler
// holds. A later pass that refuses it consumes it as unadmitted, and readTo stood past it, so the record
// was skipped with no count and no Loud line. readTo stops short of such a line, so the pass that skips
// it announces it, once.
func TestDrainClientSpools_ALineReadAndLeftIsAnnouncedWhenALaterPassSkipsIt(t *testing.T) {
	for _, c := range []struct {
		name string
		// noNonce strips the line's nonce, so no pass can lease it.
		noNonce bool
		// admittedFirst admits the line's first admission, the read loop's in pass 1, and refuses every
		// later one. Otherwise every admission refuses it.
		admittedFirst bool
		// journalFailsOnce fails the first journal call after the line's first admission.
		journalFailsOnce bool
		// heldElsewhere has another handler hold the line's delivery through pass 1.
		heldElsewhere bool
		// first puts the line at the start of the spool, with no waiting head ahead of it: another
		// session's line follows it instead.
		first bool
		// What pass 1 does with the line: whether it fails, how often it admits the line, and whether it
		// finds the refused line's delivery identity unresolved (drain_leased_deny_pending).
		pass1Err        bool
		pass1Admissions int32
		pass1Unresolved int64
	}{
		{name: "its lease lookup failed", journalFailsOnce: true, pass1Admissions: 1, pass1Unresolved: 1},
		{name: "its lease could not be taken", admittedFirst: true, journalFailsOnce: true, pass1Err: true, pass1Admissions: 1},
		{name: "its dispatch failed with no lease", noNonce: true, admittedFirst: true, pass1Err: true, pass1Admissions: 2},
		{name: "it waited with no lease", noNonce: true, admittedFirst: true, heldElsewhere: true, pass1Admissions: 1},
		// Audit 2 #1: readTo's stop at offset 0 itself. A one-line client spool whose lease lookup
		// meets a rotating journal leaves its line exactly there.
		{
			name: "its lease lookup failed at the spool's first line", journalFailsOnce: true, first: true,
			pass1Admissions: 1, pass1Unresolved: 1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := contentDrainConfig(dd)
			log := newRecordingLogger()
			cfg.Log = log
			const sess core.SessionID = "sess-left"
			x := liveOrderTool(dd, root, sess, 9)
			if c.noNonce {
				x.Nonce = ""
			}
			var admissions atomic.Int32
			var journalFails atomic.Bool
			admit, journal := cfg.Admit, cfg.Journal
			cfg.Admit = func(req ipc.Request) admissionVerdict {
				if req.Session != sess {
					return admit(req)
				}
				first := admissions.Add(1) == 1
				if first && c.journalFailsOnce {
					journalFails.Store(true)
				}
				if first && c.admittedFirst {
					return admit(req)
				}
				return admissionVerdict{Request: req, Failed: true, Reason: "test policy unavailable"}
			}
			cfg.Journal = func() (*deliveryJournal, error) {
				if journalFails.CompareAndSwap(true, false) {
					return nil, errors.New("test journal unavailable")
				}
				return journal()
			}
			dr := newDrainer(cfg)
			dd.drain.Store(dr)
			line := hookSpoolLine(t, x)
			if c.first {
				writeRawSpool(t, root, "client-8811.ndjson", line, hookSpoolLine(t, liveOrderTool(dd, root, "sess-left-other", 3)))
			} else {
				head := blockedSpoolHead(t, dd, root, "sess-left-stuck", 0)
				writeRawSpool(t, root, "client-8811.ndjson", hookSpoolLine(t, head), line)
			}
			key := deliveryIdentityKey(deliveryLease{}, false, bytes.TrimSuffix(line, []byte{'\n'}))
			if c.heldElsewhere {
				_, acquired := cfg.Seen.begin(key)
				require.True(t, acquired, "fixture: another handler holds the delivery")
			}

			_, err := dr.DrainClientSpools(ctx)
			require.Equal(t, c.pass1Err, err != nil, "fixture: pass 1 fails on the line or not: %v", err)
			require.Equal(t, c.pass1Admissions, admissions.Load(), "fixture: pass 1's admissions of the line")
			require.Equal(t, c.pass1Unresolved, dd.m.Counter(counterDrainLeasedDenyPending).Value(),
				"fixture: pass 1 left the line on an unresolved delivery identity or not")
			require.Zero(t, dd.m.Counter(counterDrainUnadmitted).Value(), "fixture: pass 1 consumed nothing as unadmitted")
			if c.heldElsewhere {
				cfg.Seen.finish(key, false)
			}
			for pass := 2; pass <= 3; pass++ {
				_, err = dr.DrainClientSpools(ctx)
				require.NoError(t, err, "pass %d", pass)
			}
			require.Equal(t, int64(1), dd.m.Counter(counterDrainUnadmitted).Value(), "the skipped record is counted once")
			require.Equal(t, 1, logCount(log, logLoud, "daemon: drain: capture not admitted; record skipped"),
				"and announced once")
		})
	}
}

// TestDrainClientSpools_ALineReleasedAtTheEndOfItsFileIsRememberedWithItsOwnGaps: the end-of-file
// re-attempt publishes a line that waited on an earlier arrival of its session, released by that
// arrival's live copy while the pass read on. A line published out of order behind a waiting head is
// remembered with the gaps consuming it added (consumedLine.gaps), which every later pass reports again.
// The re-attempt collects each line's gaps from that line's own start (gapRecorder.startLine): collected
// from where the last line the pass read began, the released line's memo took that line's gap too, here
// a refused line's whose lease is held, and every later pass reported it twice.
func TestDrainClientSpools_ALineReleasedAtTheEndOfItsFileIsRememberedWithItsOwnGaps(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := contentDrainConfig(dd)
	const base = "client-8921.ndjson"
	const sess core.SessionID = "sess-end-gaps"
	p0 := spD3Prompt(dd, root, sess, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // leased; its job waits on the ring for a worker, and none runs
	p1 := liveOrderTool(dd, root, sess, 1)
	refused := liveOrderTool(dd, root, "sess-end-gaps-refused", 2)
	_, ok := dd.ing.leaseDelivery(ctx, refused)
	require.True(t, ok, "fixture: the refused line's lease is held")
	admit := publishQueuedFirst(t, dd, cfg.Admit, refused.Nonce, 1)
	cfg.Admit = func(req ipc.Request) admissionVerdict {
		verdict := admit(req)
		if req.Nonce == refused.Nonce {
			return admissionVerdict{Request: req, Failed: true, Reason: "test policy unavailable"}
		}
		return verdict
	}
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	head := blockedSpoolHead(t, dd, root, "sess-end-gaps-stuck", 20)
	writeHookSpool(t, root, base, head, p1, refused)

	_, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.True(t, spoolWatchPublished(dd, p0.Nonce), "fixture: p0's live copy published during the pass")
	require.True(t, spoolWatchPublished(dd, p1.Nonce), "fixture: the end-of-file re-attempt published p1")
	for pass := 2; pass <= 3; pass++ {
		_, err = dr.Drain(ctx)
		require.NoError(t, err, "pass %d", pass)
		held := 0
		for _, g := range dr.GapState().Gaps {
			if g.File == base && g.Kind == DrainGapUnadmitted {
				held += g.Count
			}
		}
		require.Equal(t, 1, held, "pass %d reports the refused line's gap once", pass)
	}
}

// TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop: a pass whose budget is spent
// stops after the line that made its progress (D31), short of the end of a spool. The lines past its
// stop that an earlier pass remembered consuming are no less consumed: a pass that kept only what it had
// read forgot them, and the next pass consumed them again in full. Here the line that makes the progress
// is one whose predecessor a live worker published between the passes.
func TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := contentDrainConfig(dd)
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	const stuck, released, other core.SessionID = "sess-early-stuck", "sess-early-released", "sess-early-other"
	head := blockedSpoolHead(t, dd, root, stuck, 20)
	p0 := spD3Prompt(dd, root, released, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // leased; its job waits on the ring for a worker, and none runs
	next := liveOrderTool(dd, root, released, 1)
	reqs := []ipc.Request{head, next}
	for i := range 3 {
		reqs = append(reqs, liveOrderTool(dd, root, other, 30+i))
	}
	writeHookSpool(t, root, "client-8841.ndjson", reqs...)

	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, n, "fixture: the first pass published the other session's lines and nothing else")
	select {
	case job := <-dd.ing.ring:
		dd.ing.dispatch(context.Background(), dd.runIngested, job)
	default:
		t.Fatal("fixture: p0's job is not queued")
	}
	require.True(t, spoolWatchPublished(dd, p0.Nonce), "fixture: a worker published p0")
	meter.take()

	n, err = dr.DrainClientSpools(withPassBudget(ctx, 0))
	require.ErrorIs(t, err, errPassBudgetSpent, "fixture: the pass made progress, and its spent budget stopped it")
	require.Equal(t, 1, n)
	require.True(t, spoolWatchPublished(dd, next.Nonce), "fixture: the pass published the line p0 released")
	require.Zero(t, meter.take().admitted[other], "fixture: the pass stopped before the lines it remembered")

	n, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	cost := meter.take()
	require.Zero(t, cost.admitted[other], "the lines past the early stop are still remembered as consumed")
	require.Zero(t, cost.admitted[released], "and so is the line the stopped pass published")
	require.Equal(t, 1, cost.admitted[stuck], "only the waiting head is admitted again")
}

// TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands: a line whose
// delivery this daemon has handled but whose acknowledgement is not on the committed frontier yet is
// absorbed, and the pass reports the gap ("in-memory completion has no frontier record"). A drainer that
// remembered the line skipped it on every later pass and reported that gap again for the rest of its
// life, after the acknowledgement had landed. Such a line is not remembered: the next pass consumes it
// again, finds it on the frontier, and reports no gap for it.
func TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	dr := newDrainer(contentDrainConfig(dd))
	dd.drain.Store(dr)
	head := blockedSpoolHead(t, dd, root, "sess-unack-stuck", 0)
	line := liveOrderTool(dd, root, "sess-unack-done", 7)
	lease, ok := dd.ing.leaseDelivery(ctx, line)
	require.True(t, ok)
	key := deliveryIdentityKey(lease, true, nil)
	_, acquired := dd.ing.seen.begin(key)
	require.True(t, acquired)
	dd.ing.seen.finish(key, true) // handled in this daemon's lifetime; its acknowledgement is not written yet
	writeHookSpool(t, root, "client-8811.ndjson", head, line)

	_, err := dr.Drain(ctx)
	require.NoError(t, err)
	gapOfKind(t, dr.GapState(), DrainGapUnacknowledged)

	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.NoError(t, j.acknowledge(ctx, lease.Delivery, lease.ObservationID, core.Hash{}))
	_, err = dr.Drain(ctx)
	require.NoError(t, err)
	for _, g := range dr.GapState().Gaps {
		require.NotEqual(t, DrainGapUnacknowledged, g.Kind, "the acknowledgement has landed: %+v", g)
	}
}

// TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain: a hook keeps appending to
// its client spool while the daemon is slow to answer it, and a head waiting on an earlier arrival of
// its session keeps every pass reading the spool. A drainer forgot what it had consumed there whenever
// the file changed at all, so each append made the next pass consume every line behind the head again
// in full. Lines already consumed do not change when the file grows (each is checked by its own bytes
// as it is read: spoolMemo), so the pass consumes only the appended line. The file's new bytes are not
// durable yet, so it is synced again.
func TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := contentDrainConfig(dd)
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	meter.meterDrainer(dr)
	dd.drain.Store(dr)
	const stuck, other core.SessionID = "sess-grow-stuck", "sess-grow-other"
	const base = "client-8821.ndjson"
	head := blockedSpoolHead(t, dd, root, stuck, 0)
	reqs := []ipc.Request{head}
	for i := range 3 {
		reqs = append(reqs, liveOrderTool(dd, root, other, 40+i))
	}
	writeHookSpool(t, root, base, reqs...)
	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, n, "fixture: the first pass published the lines behind the head")
	meter.take()

	w, err := paths.AppendOnly(filepath.Join(paths.Of(root).Spool, base))
	require.NoError(t, err)
	_, err = w.Write(hookSpoolLine(t, liveOrderTool(dd, root, stuck, 50))) // a later arrival, which waits too
	require.NoError(t, err)
	require.NoError(t, w.Close())
	n, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	cost := meter.take()
	require.Zero(t, cost.admitted[other], "the lines consumed before the spool grew are not consumed again")
	require.Equal(t, 2, cost.admitted[stuck], "the head and the appended line, which both wait, are admitted")
	require.Equal(t, int64(1), cost.syncs, "the grown spool is synced: its appended bytes were not durable")
}

// TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes: a blob line an
// earlier pass consumed behind a waiting head is skipped by the passes after it (spoolMemo), and its
// blob stays while the line is ahead of the front. Once the head publishes, the pass that publishes it
// rolls the front over the remembered line to the end of the file, which releases the line's cleanup
// intent: the same pass removes the blob and the spool, though it consumed nothing new of that line.
func TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	dr := newDrainer(contentDrainConfig(dd))
	dd.drain.Store(dr)
	const base, blob = "client-8801.ndjson", "blob-8801-1.bin"
	const waiting core.SessionID = "sess-release-waiting"
	p0 := spD3Prompt(dd, root, waiting, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // leased; its job waits on the ring for a worker, and none runs
	head := liveOrderTool(dd, root, waiting, 1)
	other := blobSpoolLine(t, root, liveOrderTool(dd, root, "sess-release-other", 7), blob)
	writeHookSpool(t, root, base, head, other)
	blobPath := filepath.Join(paths.Of(root).Spool, blob)
	for pass := 1; pass <= 2; pass++ {
		n, err := dr.DrainClientSpools(ctx)
		require.NoError(t, err)
		require.Equal(t, 2-pass, n, "fixture: the first pass published the blob line, and the second nothing")
		require.FileExists(t, blobPath, "fixture: the blob stays while its line is ahead of the front")
	}

	select {
	case job := <-dd.ing.ring:
		dd.ing.dispatch(context.Background(), dd.runIngested, job)
	default:
		t.Fatal("fixture: p0's job is not queued")
	}
	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the pass publishes the head p0 released")
	require.True(t, spoolWatchPublished(dd, head.Nonce))
	require.NoFileExists(t, blobPath, "the front passed the remembered blob line, so its blob is collected")
	require.True(t, spoolWatchGone(root, base), "and the consumed spool is released in the same pass")
	st, err := dr.loadState()
	require.NoError(t, err)
	require.NotContains(t, st, base, "nothing of the released spool is left in the progress")
}

// TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain: a drainer skips the sync of a
// spool file it synced before and finds unchanged, and skips the lines it consumed there (spoolMemo).
// Both hold only for that very file. Here another file, with the same bytes and the same modification
// time, is renamed over the spool's name: nothing is known of its durability, so the pass syncs it, and
// nothing of its lines is known consumed, so the pass reads them as lines it has not seen.
func TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := contentDrainConfig(dd)
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	meter.meterDrainer(dr)
	dd.drain.Store(dr)
	const other core.SessionID = "sess-replaced-other"
	head := blockedSpoolHead(t, dd, root, "sess-replaced-stuck", 0)
	behind := liveOrderTool(dd, root, other, 7)
	path := filepath.Join(paths.Of(root).Spool, "client-8861.ndjson")
	writeHookSpool(t, root, filepath.Base(path), head, behind)
	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "fixture: the first pass published the line behind the head")
	meter.take()

	// Stat through a handle, so that each identity is its own file's on every platform.
	handleStat := func() os.FileInfo {
		t.Helper()
		f, err := os.Open(paths.Long(path))
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		fi, err := f.Stat()
		require.NoError(t, err)
		return fi
	}
	before := handleStat()
	body, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	// Written beside it and renamed over it, so the two files exist at once and cannot share an identity.
	replacement := filepath.Join(paths.Of(root).Spool, "replacement.tmp")
	require.NoError(t, os.WriteFile(paths.Long(replacement), body, 0o600))
	require.NoError(t, os.Chtimes(paths.Long(replacement), before.ModTime(), before.ModTime()))
	require.NoError(t, os.Rename(paths.Long(replacement), paths.Long(path)))
	after := handleStat()
	require.True(t, !os.SameFile(before, after) && after.Size() == before.Size() && after.ModTime().Equal(before.ModTime()),
		"fixture: another file, of the same size and modification time, has the spool's name")

	n, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "the line behind the head is on the committed frontier already")
	cost := meter.take()
	require.Equal(t, int64(1), cost.syncs, "nothing is known of the replacement's durability, so it is synced")
	require.Equal(t, 1, cost.admitted[other], "nothing of the replacement's lines is known consumed, so they are read")
}

// TestDrain_AHeldSegmentReleasedUnchangedIsSyncedBeforeItsTailIsRead: a drainer skips the sync of a
// file it finds unchanged only when its own sync covered that file (spoolMemo's synced). A WAL segment
// the ingest held during a pass was not synced by the drain: the pass read it only up to the ingest's
// synced size, and its memo says so. Released afterwards with no write (the ingest closed it after a
// failed Sync, say), the segment is unchanged by size and time, but the bytes past that synced size
// were never made durable by anyone. The next pass must sync it before it reads that tail, or it
// leases lines a machine crash can still take: the orphan lease durableEnd exists to prevent. Audit 2
// #0: dropping the memo's synced condition, or calling a held segment synced, left every row green.
func TestDrain_AHeldSegmentReleasedUnchangedIsSyncedBeforeItsTailIsRead(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	const sess core.SessionID = "sess-held-released"
	base := "wal-" + string(sess) + ".ndjson"
	path := filepath.Join(paths.Of(root).Spool, base)
	var held atomic.Bool
	held.Store(true)
	var syncedSize atomic.Int64
	cfg := contentDrainConfig(dd)
	cfg.SyncedWAL = func(p string) (int64, bool) {
		if filepath.Base(p) != base || !held.Load() {
			return 0, false
		}
		return syncedSize.Load(), true
	}
	cfg.HoldsWAL = func(p string) bool { return filepath.Base(p) == base && held.Load() }
	cfg.IsLive = func(core.SessionID) bool { return true }
	dr := newDrainer(cfg)
	var syncs atomic.Int64
	syncFile := dr.syncFile
	dr.syncFile = func(p string) error {
		if filepath.Base(p) == base {
			syncs.Add(1)
		}
		return syncFile(p)
	}
	dd.drain.Store(dr)
	head := blockedSpoolHead(t, dd, root, sess, 0)
	behind := liveOrderTool(dd, root, sess, 7) // the same session: it waits behind the head
	lines := [][]byte{hookSpoolLine(t, head), hookSpoolLine(t, behind)}
	writeRawSpool(t, root, base, lines...)
	before, err := os.Stat(paths.Long(path))
	require.NoError(t, err)
	// The ingest's Sync covered only the head; the second line is its unsynced tail.
	syncedSize.Store(int64(len(lines[0])))

	_, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, syncs.Load(), "fixture: the drain does not sync a segment the ingest holds")

	held.Store(false) // released, with no write since
	after, err := os.Stat(paths.Long(path))
	require.NoError(t, err)
	require.True(t, before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()),
		"fixture: the released segment is unchanged by size and modification time")
	_, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), syncs.Load(),
		"a segment only the ingest's Syncs covered, and only to its synced size, is synced once it is released")
}

// TestDrainer_ReadsABlobBodyOnlyThroughItsSeam: the cost rows count the blob bodies a pass reads through
// drainer.readBlobBody. A call of readBlob anywhere else in drain.go would read bodies they cannot see,
// as naming a consumed line's blob by reading the whole blob once did at four sites, so drain.go calls
// readBlob nowhere: it is only that seam's value.
func TestDrainer_ReadsABlobBodyOnlyThroughItsSeam(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "drain.go", nil, 0)
	require.NoError(t, err)
	var calls []string
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "readBlob" {
				calls = append(calls, fset.Position(call.Pos()).String())
			}
		}
		return true
	})
	require.Empty(t, calls, "drain.go reads a blob's body only through drainer.readBlobBody")
}
