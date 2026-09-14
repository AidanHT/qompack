package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// recoveryLine is one complete, newline-terminated spool record. Keeping its bytes lets these
// recovery cases assert that an unacknowledged line remains exactly retryable, rather than merely
// that a spool file with the same name still exists.
func recoveryLine(t *testing.T) []byte {
	t.Helper()
	line, err := ipc.EncodeRequest(ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-drain-recovery", TS: core.UnixMilli(17),
	})
	require.NoError(t, err)
	return line
}

// requireUnacknowledged accepts an absent state entry, but forbids an entry from claiming progress
// over the current line. Both shapes are restart-retryable; Done or a non-zero offset is not.
func requireUnacknowledged(t *testing.T, dr *drainer, base string) {
	t.Helper()
	st, err := dr.loadState()
	require.NoError(t, err)
	fs, ok := st[base]
	if !ok {
		return
	}
	require.Zero(t, fs.Offset, "an unacknowledged line must not advance the persisted offset")
	require.False(t, fs.Done, "an unacknowledged line must not mark its spool file done")
}

// TestDrainRejectedResponseIsRetryableAfterRestart is SP05-D1's acknowledgement boundary: a
// handler's OK:false is not a consumed line. A fresh drainer models process restart, so this test
// proves retryability from spool bytes and durable state rather than from the first drainer's
// in-memory state.
func TestDrainRejectedResponseIsRetryableAfterRestart(t *testing.T) {
	root := t.TempDir()
	line := recoveryLine(t)
	p := filepath.Join(paths.Of(root).Spool, "client-rejected.ndjson")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, line, 0o600))

	rejectedCalls := 0
	first := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		rejectedCalls++
		return ipc.Response{OK: false, Err: "temporary refusal"}
	}})

	n, err := first.Drain(context.Background())
	require.Error(t, err, "a rejected handler response must be visible to the drain caller")
	require.Zero(t, n, "only handler-acknowledged calls count as drained")
	require.Equal(t, 1, rejectedCalls)

	retained, readErr := os.ReadFile(p)
	require.NoError(t, readErr)
	require.Equal(t, line, retained, "the rejected line must remain byte-for-byte retryable")
	requireUnacknowledged(t, first, filepath.Base(p))

	acceptedCalls := 0
	restarted := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		acceptedCalls++
		return ipc.Response{OK: true}
	}})
	n, err = restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "the restarted drainer must acknowledge the retained line")
	require.Equal(t, 1, acceptedCalls, "restart must actually dispatch the retained request")
	require.NoFileExists(t, p, "an acknowledged client spool file may be removed")
}

// TestDrainCanceledRejectedHandlerLeavesCurrentLinePending covers cancellation while the handler
// is running. The current line remains pending even though the handler returned OK:false, and a
// new drainer can retry it once the caller has a live context again.
func TestDrainCanceledRejectedHandlerLeavesCurrentLinePending(t *testing.T) {
	root := t.TempDir()
	line := recoveryLine(t)
	p := filepath.Join(paths.Of(root).Spool, "client-canceled.ndjson")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, line, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	canceledCalls := 0
	first := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		canceledCalls++
		cancel()
		return ipc.Response{OK: false, Err: "cancelled while handling"}
	}})

	n, err := first.Drain(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, n, "a rejected call during cancellation is not acknowledged")
	require.Equal(t, 1, canceledCalls)
	require.FileExists(t, p, "cancellation must leave the current line pending")
	requireUnacknowledged(t, first, filepath.Base(p))

	retryCalls := 0
	restarted := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		retryCalls++
		return ipc.Response{OK: true}
	}})
	n, err = restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, retryCalls, "the pending line must be dispatched again after cancellation")
	require.NoFileExists(t, p)
}

// TestDrainTrailingIncompleteLineWaitsForCompletion prevents an EOF without a record terminator
// from being confused with a fully consumed spool. Once its final byte arrives, a fresh drainer
// must dispatch the now-complete request.
func TestDrainTrailingIncompleteLineWaitsForCompletion(t *testing.T) {
	root := t.TempDir()
	line := recoveryLine(t)
	p := filepath.Join(paths.Of(root).Spool, "client-partial.ndjson")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, line[:len(line)-1], 0o600))

	calls := 0
	first := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		calls++
		return ipc.Response{OK: true}
	}})

	n, err := first.Drain(context.Background())
	require.NoError(t, err, "an incomplete tail is pending input, not a corrupt completed record")
	require.Zero(t, n)
	require.Zero(t, calls)
	retained, readErr := os.ReadFile(p)
	require.NoError(t, readErr)
	require.Equal(t, line[:len(line)-1], retained, "the incomplete bytes must remain in the spool")
	requireUnacknowledged(t, first, filepath.Base(p))

	f, openErr := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, openErr)
	_, writeErr := f.Write(line[len(line)-1:])
	require.NoError(t, writeErr)
	require.NoError(t, f.Close())

	restarted := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		calls++
		return ipc.Response{OK: true}
	}})
	n, err = restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, calls, "the completed bytes must become one dispatched request")
	require.NoFileExists(t, p)
}

// TestDrainStatePersistenceFailurePreservesSpool makes the state DIRECTORY path a test-owned
// regular file. The handler succeeds, but its acknowledgement cannot be made restart-safe, so
// Drain must report the persistence failure and keep the spool. Removing the fixture then
// demonstrates the expected at-least-once retry; it deliberately does not claim exactly-once
// handler execution.
// TestDrainLoadStateTreatsAFileParentAsNoState pins the errno the two platforms disagree on: a
// state path whose parent is a regular file is "not found" on Windows and ENOTDIR on POSIX, and
// loadState must read both as a first drain, or TestDrainStatePersistenceFailurePreservesSpool
// below runs two different sequences on the two — Windows dispatching and then failing to persist,
// POSIX refusing before the handler ran — which the first Linux and macOS CI run found.
func TestDrainLoadStateTreatsAFileParentAsNoState(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(paths.Of(root).State), 0o700))
	require.NoError(t, os.WriteFile(paths.Of(root).State, []byte("state directory is a file"), 0o600))

	dr := newDrainer(DrainConfig{Root: root})
	st, err := dr.loadState()
	require.NoError(t, err, "a state path through a regular file holds no progress record")
	require.Empty(t, st)
}

func TestDrainStatePersistenceFailurePreservesSpool(t *testing.T) {
	root := t.TempDir()
	line := recoveryLine(t)
	p := filepath.Join(paths.Of(root).Spool, "client-state-failure.ndjson")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, line, 0o600))

	stateDir := paths.Of(root).State
	const stateSentinel = "state directory is a file"
	require.NoError(t, os.WriteFile(stateDir, []byte(stateSentinel), 0o600))

	firstCalls := 0
	first := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		firstCalls++
		return ipc.Response{OK: true}
	}})

	n, err := first.Drain(context.Background())
	require.Error(t, err, "failure to persist acknowledged progress must be reported")
	require.Equal(t, 1, n, "the handler did acknowledge one call before persistence failed")
	require.Equal(t, 1, firstCalls)
	require.FileExists(t, p, "the spool must remain until its acknowledgement is persisted")
	state, readErr := os.ReadFile(stateDir)
	require.NoError(t, readErr)
	require.Equal(t, stateSentinel, string(state), "a failed state write must not replace prior state")

	// This is a test-owned fixture. Its removal and replacement with the required directory
	// represents repairing state before a restart; replaying the line is the expected at-least-once
	// consequence of the failed save.
	require.NoError(t, os.Remove(stateDir))
	require.NoError(t, os.MkdirAll(stateDir, 0o700))
	retryCalls := 0
	restarted := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		retryCalls++
		return ipc.Response{OK: true}
	}})
	n, err = restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, retryCalls, "the retained line must be retried after state repair")
	require.NoFileExists(t, p)
}

// TestDrainRejectedBlobResponseRetainsBlobForRestart pairs SP05-D1 acknowledgement with the
// externalized-payload path. A NAK must leave both the descriptor line and blob bytes available;
// the fresh drainer proves it can resolve the same bytes before its eventual acknowledgement
// persists and cleanup is permitted.
func TestDrainRejectedBlobResponseRetainsBlobForRestart(t *testing.T) {
	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))

	payload := []byte(`{"large":"tool response"}`)
	blobPath := filepath.Join(dir, "blob-drain-recovery.bin")
	require.NoError(t, os.WriteFile(blobPath, payload, 0o600))
	ref, err := json.Marshal(blobRef{
		Blob: filepath.Base(blobPath), Bytes: len(payload), Field: drainBlobToolResponse,
	})
	require.NoError(t, err)
	line, err := ipc.EncodeRequest(ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-drain-blob", TS: core.UnixMilli(23),
		Event: &hookio.Event{}, Raw: ref,
	})
	require.NoError(t, err)
	p := filepath.Join(dir, "client-blob-rejected.ndjson")
	require.NoError(t, os.WriteFile(p, line, 0o600))

	firstCalls := 0
	first := newDrainer(DrainConfig{Root: root, Dispatch: func(_ context.Context, got ipc.Request) ipc.Response {
		firstCalls++
		require.NotNil(t, got.Event)
		require.Equal(t, payload, []byte(got.Event.ToolResponse), "the handler must see the resolved payload")
		return ipc.Response{OK: false, Err: "try again"}
	}})

	n, err := first.Drain(context.Background())
	require.Error(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, firstCalls)
	retained, readErr := os.ReadFile(p)
	require.NoError(t, readErr)
	require.Equal(t, line, retained, "the blob descriptor line must remain retryable")
	blob, blobErr := os.ReadFile(blobPath)
	require.NoError(t, blobErr)
	require.Equal(t, payload, blob, "a rejected dispatch must not delete its source blob")
	requireUnacknowledged(t, first, filepath.Base(p))

	retryCalls := 0
	restarted := newDrainer(DrainConfig{Root: root, Dispatch: func(_ context.Context, got ipc.Request) ipc.Response {
		retryCalls++
		require.NotNil(t, got.Event)
		require.Equal(t, payload, []byte(got.Event.ToolResponse), "restart must resolve the original blob bytes")
		return ipc.Response{OK: true}
	}})
	n, err = restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, retryCalls)
	require.NoFileExists(t, p)
	require.NoFileExists(t, blobPath, "cleanup follows the successful acknowledged retry")
}

// TestDrainRestartCleansPersistedBlobWithoutRedispatching models a crash after handler
// acknowledgement and state persistence, but before the blob and now-inactive spool file were
// cleaned. A new drainer must consume that persisted cleanup intent without dispatching again.
func TestDrainRestartCleansPersistedBlobWithoutRedispatching(t *testing.T) {
	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))
	line := recoveryLine(t)
	p := filepath.Join(dir, "client-pending-cleanup.ndjson")
	require.NoError(t, os.WriteFile(p, line, 0o600))
	blobName := "blob-pending-cleanup.bin"
	blobPath := filepath.Join(dir, blobName)
	require.NoError(t, os.WriteFile(blobPath, []byte("acknowledged payload"), 0o600))

	base := filepath.Base(p)
	seed := newDrainer(DrainConfig{Root: root})
	require.NoError(t, seed.saveState(drainState{base: &drainFileState{
		Size: int64(len(line)), Offset: int64(len(line)), Done: true, PendingBlobs: []string{blobName},
	}}))

	dispatches := 0
	restarted := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		dispatches++
		return ipc.Response{OK: true}
	}})
	n, err := restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "an already acknowledged line must not be dispatched again during cleanup")
	require.Zero(t, dispatches)
	require.NoFileExists(t, blobPath, "persisted cleanup intent must remove the acknowledged blob")
	require.NoFileExists(t, p, "an inactive spool with no pending cleanup may be removed")
	st, loadErr := restarted.loadState()
	require.NoError(t, loadErr)
	_, found := st[base]
	require.False(t, found, "cleanup completion must retire the persisted spool state")
}

// TestDrainRestartCleansPriorBlobBeforeRetryingLaterRejectedLine covers a file that contains an
// acknowledged blob-backed line followed by a rejected line. The first Drain persists the first
// line's offset and cleanup intent before returning the rejection; restart cleans that blob before
// it dispatches only the remaining line.
func TestDrainRestartCleansPriorBlobBeforeRetryingLaterRejectedLine(t *testing.T) {
	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))
	payload := []byte(`{"first":"blob backed"}`)
	blobName := "blob-before-retry.bin"
	blobPath := filepath.Join(dir, blobName)
	require.NoError(t, os.WriteFile(blobPath, payload, 0o600))
	ref, err := json.Marshal(blobRef{Blob: blobName, Bytes: len(payload), Field: drainBlobToolResponse})
	require.NoError(t, err)
	firstLine, err := ipc.EncodeRequest(ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-blob-before-retry", TS: core.UnixMilli(31),
		Event: &hookio.Event{}, Raw: ref,
	})
	require.NoError(t, err)
	secondLine := recoveryLine(t)
	p := filepath.Join(dir, "client-blob-before-retry.ndjson")
	require.NoError(t, os.WriteFile(p, append(firstLine, secondLine...), 0o600))

	firstCalls := 0
	first := newDrainer(DrainConfig{Root: root, Dispatch: func(_ context.Context, got ipc.Request) ipc.Response {
		firstCalls++
		if firstCalls == 1 {
			require.NotNil(t, got.Event)
			require.Equal(t, payload, []byte(got.Event.ToolResponse))
			return ipc.Response{OK: true}
		}
		return ipc.Response{OK: false, Err: "later line rejected"}
	}})
	n, err := first.Drain(context.Background())
	require.Error(t, err)
	require.Equal(t, 1, n, "only the first line was acknowledged")
	require.Equal(t, 2, firstCalls)
	require.FileExists(t, blobPath, "the early exit must retain cleanup work for the first line")
	st, loadErr := first.loadState()
	require.NoError(t, loadErr)
	fs := st[filepath.Base(p)]
	require.NotNil(t, fs)
	require.Equal(t, int64(len(firstLine)), fs.Offset, "restart must resume at the rejected line")
	require.Equal(t, []string{blobName}, fs.PendingBlobs, "cleanup intent must survive the rejection")

	retryCalls := 0
	restarted := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		retryCalls++
		_, statErr := os.Stat(blobPath)
		require.True(t, os.IsNotExist(statErr), "restart must clean the earlier blob before retrying")
		return ipc.Response{OK: true}
	}})
	n, err = restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, retryCalls, "restart must dispatch only the formerly rejected line")
	require.NoFileExists(t, blobPath)
	require.NoFileExists(t, p)
}

// TestDrainWindowsOpenBlobRetainsCleanupIntent is the Windows sharing-violation recovery path.
// A plain os.Open handle prevents blob deletion there, so the intent and inactive spool must stay
// durable until the handle is released and a new drainer can finish cleanup. POSIX permits unlink
// of an open file, so this has no equivalent portable failure injection.
func TestDrainWindowsOpenBlobRetainsCleanupIntent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: POSIX permits deleting an open file")
	}
	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))
	line := recoveryLine(t)
	p := filepath.Join(dir, "client-open-blob.ndjson")
	require.NoError(t, os.WriteFile(p, line, 0o600))
	blobName := "blob-open-cleanup.bin"
	blobPath := filepath.Join(dir, blobName)
	require.NoError(t, os.WriteFile(blobPath, []byte("pending delete"), 0o600))
	base := filepath.Base(p)
	seed := newDrainer(DrainConfig{Root: root})
	require.NoError(t, seed.saveState(drainState{base: &drainFileState{
		Size: int64(len(line)), Offset: int64(len(line)), Done: true, PendingBlobs: []string{blobName},
	}}))

	holder, err := os.Open(blobPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() })
	first := newDrainer(DrainConfig{Root: root})
	n, err := first.Drain(context.Background())
	require.Error(t, err, "a Windows sharing violation must report failed cleanup")
	require.Zero(t, n)
	require.FileExists(t, blobPath)
	require.FileExists(t, p)
	st, loadErr := first.loadState()
	require.NoError(t, loadErr)
	fs := st[base]
	require.NotNil(t, fs)
	require.Equal(t, []string{blobName}, fs.PendingBlobs, "failed deletion must retain durable intent")

	require.NoError(t, holder.Close())
	restarted := newDrainer(DrainConfig{Root: root})
	n, err = restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "cleanup recovery must not redispatch an acknowledged line")
	require.NoFileExists(t, blobPath)
	require.NoFileExists(t, p)
}

// TestDrainCrossFileSharedBlobWaitsForLaterRetry keeps one externalized payload available when a
// WAL record acknowledges it but the same descriptor in a client fallback is rejected. The later
// file is a separate durable recovery boundary, so a fresh drainer must still resolve its bytes;
// successful handling of the WAL record alone cannot authorize blob cleanup.
func TestDrainCrossFileSharedBlobWaitsForLaterRetry(t *testing.T) {
	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))

	payload := []byte(`{"shared":"externalized payload"}`)
	blobName := "blob-cross-file-shared.bin"
	blobPath := filepath.Join(dir, blobName)
	require.NoError(t, os.WriteFile(blobPath, payload, 0o600))
	ref, err := json.Marshal(blobRef{
		Blob: blobName, Bytes: len(payload), Field: drainBlobToolResponse,
	})
	require.NoError(t, err)
	line, err := ipc.EncodeRequest(ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-cross-file-blob", TS: core.UnixMilli(41),
		Event: &hookio.Event{}, Raw: ref,
	})
	require.NoError(t, err)

	walPath := filepath.Join(dir, "wal-sess-cross-file-blob.ndjson")
	clientPath := filepath.Join(dir, "client-cross-file-blob.ndjson")
	require.NoError(t, os.WriteFile(walPath, line, 0o600))
	require.NoError(t, os.WriteFile(clientPath, line, 0o600))

	calls := 0
	first := newDrainer(DrainConfig{Root: root, Dispatch: func(_ context.Context, got ipc.Request) ipc.Response {
		calls++
		require.NotNil(t, got.Event)
		require.Equal(t, payload, []byte(got.Event.ToolResponse))
		if calls == 1 {
			return ipc.Response{OK: true}
		}
		return ipc.Response{OK: false, Err: "client fallback remains pending"}
	}})
	n, err := first.Drain(context.Background())
	require.Error(t, err)
	require.Equal(t, 1, n, "only the WAL record was acknowledged")
	require.Equal(t, 2, calls, "the fallback must be attempted after the WAL")
	walState, loadErr := first.loadState()
	require.NoError(t, loadErr)
	walProgress := walState[filepath.Base(walPath)]
	require.NotNil(t, walProgress)
	require.Equal(t, int64(len(line)), walProgress.Offset)
	require.True(t, walProgress.Done)
	require.Equal(t, []string{blobName}, walProgress.PendingBlobs, "the WAL must retain the cleanup journal")
	require.FileExists(t, walPath, "the cleanup journal keeps the acknowledged WAL until the shared blob is releasable")
	require.FileExists(t, clientPath, "the rejected client fallback remains retryable")
	require.FileExists(t, blobPath, "the later fallback still needs the shared blob")
	requireUnacknowledged(t, first, filepath.Base(clientPath))

	retryCalls := 0
	restarted := newDrainer(DrainConfig{Root: root, Dispatch: func(_ context.Context, got ipc.Request) ipc.Response {
		retryCalls++
		require.NotNil(t, got.Event)
		require.Equal(t, payload, []byte(got.Event.ToolResponse), "restart must resolve the retained blob")
		return ipc.Response{OK: true}
	}})
	n, err = restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "the fresh drainer retries the unacknowledged fallback")
	require.Equal(t, 1, retryCalls)
	require.NoFileExists(t, clientPath)
	require.NoFileExists(t, blobPath, "cleanup follows acknowledgement of every remaining reference")

	cleanupDispatches := 0
	cleanup := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
		cleanupDispatches++
		return ipc.Response{OK: true}
	}})
	n, err = cleanup.Drain(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "cleanup must use the persisted WAL progress rather than redispatch it")
	require.Zero(t, cleanupDispatches)
	require.NoFileExists(t, walPath, "the WAL can retire after its shared cleanup journal is released")
}

// TestDrainFreshSeenMayRedeliverWithoutPersistedOffset documents the recovery scope of Seen.
// The first process has recorded a successful live delivery only in its in-memory set; because it
// has not persisted a drain offset, a fresh set after restart has no durable identity to suppress
// redelivery. This is the expected at-least-once boundary, not an exactly-once assertion.
func TestDrainFreshSeenMayRedeliverWithoutPersistedOffset(t *testing.T) {
	root := t.TempDir()
	line := recoveryLine(t)
	p := filepath.Join(paths.Of(root).Spool, "client-fresh-seen-redelivery.ndjson")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, line, 0o600))

	liveSeen := newSeenSet(8)
	key := core.HashBytes(walHashDomain, line[:len(line)-1])
	completed, acquired := liveSeen.begin(key)
	require.False(t, completed)
	require.True(t, acquired)
	liveSeen.finish(key, true) // models a live handler acknowledgement before drain progress saved
	_, stateErr := os.Stat(drainStatePath(root))
	require.True(t, os.IsNotExist(stateErr), "the live acknowledgement has no durable drain offset")

	redeliveries := 0
	restarted := newDrainer(DrainConfig{
		Root: root,
		Seen: newSeenSet(8),
		Dispatch: func(context.Context, ipc.Request) ipc.Response {
			redeliveries++
			return ipc.Response{OK: true}
		},
	})
	n, err := restarted.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, redeliveries, "a new process may redeliver without a persisted offset")
	require.NoFileExists(t, p)
}

// TestDrainRejectsInvalidPersistedStateBeforeTouchingRecoveryResources ensures drain state is a
// recovery gate, not advisory input. A malformed or impossible record must not be reset to an
// empty state that dispatches a retained line or cleans an existing blob before an operator can
// repair the state file.
func TestDrainRejectsInvalidPersistedStateBeforeTouchingRecoveryResources(t *testing.T) {
	line := recoveryLine(t)
	const base = "client-corrupt-state.ndjson"
	const blobName = "blob-corrupt-state.bin"
	cases := []struct {
		name  string
		state string
	}{
		{name: "malformed", state: "{"},
		{name: "null state", state: "null"},
		{name: "null file entry", state: `{"client-corrupt-state.ndjson":null}`},
		{name: "negative offset", state: fmt.Sprintf(`{"client-corrupt-state.ndjson":{"size":%d,"offset":-1,"done":false,"pending_blobs":["blob-corrupt-state.bin"]}}`, len(line))},
		{name: "offset beyond spool size", state: fmt.Sprintf(`{"client-corrupt-state.ndjson":{"size":%d,"offset":%d,"done":false,"pending_blobs":["blob-corrupt-state.bin"]}}`, len(line), len(line)+1)},
		{name: "done without complete offset", state: fmt.Sprintf(`{"client-corrupt-state.ndjson":{"size":%d,"offset":0,"done":true,"pending_blobs":["blob-corrupt-state.bin"]}}`, len(line))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			layout := paths.Of(root)
			require.NoError(t, os.MkdirAll(layout.Spool, 0o700))
			spoolPath := filepath.Join(layout.Spool, base)
			require.NoError(t, os.WriteFile(spoolPath, line, 0o600))
			blobPath := filepath.Join(layout.Spool, blobName)
			blobBytes := []byte("must survive invalid state")
			require.NoError(t, os.WriteFile(blobPath, blobBytes, 0o600))

			statePath := drainStatePath(root)
			require.NoError(t, os.MkdirAll(filepath.Dir(statePath), 0o700))
			require.NoError(t, os.WriteFile(statePath, []byte(tc.state), 0o600))

			dispatches := 0
			dr := newDrainer(DrainConfig{Root: root, Dispatch: func(context.Context, ipc.Request) ipc.Response {
				dispatches++
				return ipc.Response{OK: true}
			}})
			n, err := dr.Drain(context.Background())
			require.Error(t, err)
			require.Zero(t, n)
			require.Zero(t, dispatches, "invalid state must stop before dispatch")
			spool, spoolErr := os.ReadFile(spoolPath)
			require.NoError(t, spoolErr)
			require.Equal(t, line, spool)
			blob, blobErr := os.ReadFile(blobPath)
			require.NoError(t, blobErr)
			require.Equal(t, blobBytes, blob, "invalid state must not trigger cleanup")
			persisted, readErr := os.ReadFile(statePath)
			require.NoError(t, readErr)
			require.Equal(t, tc.state, string(persisted), "invalid state must remain available for repair")
		})
	}
}
