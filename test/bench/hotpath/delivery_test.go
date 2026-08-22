package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// spoolFixture builds a real project layout with a real ipc spool underneath it, and returns the
// spool directory plus the SpoolWriter's own path — exactly the two values runHarness hands
// censusClientSpool. Nothing here is a stand-in: ipc.NewSpool is the same writer
// internal/ipc/client.go's spoolAndReturn appends through in production, so the on-disk bytes
// this census reads are the bytes a degraded hook really leaves behind.
func spoolFixture(t *testing.T) (spoolDir, ownPath string, sp ipc.SpoolWriter) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	spoolDir = paths.Of(root).Spool
	sp, err := ipc.NewSpool(spoolDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if cl, ok := sp.(interface{ Close() error }); ok {
			_ = cl.Close()
		}
	})
	return spoolDir, sp.Path(), sp
}

// observeRequest is one hot-path request shaped exactly as internal/cli/hookclient.go builds it:
// Op observe.tool, Session copied off the event, an Event body.
func observeRequest(sess core.SessionID, seq int) ipc.Request {
	ev := hookio.Event{
		HookEventName: "PostToolUse",
		SessionID:     sess,
		ToolName:      "Read",
		ToolUseID:     core.ToolUseID(fmt.Sprintf("toolu_%d", seq)),
	}
	return ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: 1, Event: &ev}
}

// TestClientSpoolNameShape_DerivedFromTheRealWriter pins that the census identifies client spool
// files POSITIVELY, from internal/ipc's own naming, rather than by excluding the wal- prefix.
func TestClientSpoolNameShape_DerivedFromTheRealWriter(t *testing.T) {
	_, ownPath, _ := spoolFixture(t)

	prefix, ext, err := clientSpoolNameShape(ownPath)
	require.NoError(t, err)
	require.Equal(t, "client-", prefix)
	require.Equal(t, ".ndjson", ext)
	require.Equal(t, prefix+strconv.Itoa(os.Getpid())+ext, filepath.Base(ownPath),
		"the derived shape must reconstruct the real writer's own file name")

	_, _, err = clientSpoolNameShape(filepath.Join("spool", "not-a-client-file.txt"))
	require.Error(t, err, "a path that does not carry this process's pid must fail loudly, never silently match nothing")
}

// TestCensusClientSpool_CountsOnlyThisRunsDeferredHotPathRequests is the census's discrimination:
// it counts this harness's own deferred observe.tool lines, and nothing else that shares the
// directory — not the daemon's WAL segments (which hold the requests that WERE delivered, so
// counting them would inflate Deferred and MASK a loss), not admin probes, not another session.
func TestCensusClientSpool_CountsOnlyThisRunsDeferredHotPathRequests(t *testing.T) {
	spoolDir, ownPath, sp := spoolFixture(t)

	require.NoError(t, sp.Append(observeRequest(baSessionID, 1)))
	require.NoError(t, sp.Append(observeRequest(baSessionID, 2)))
	require.NoError(t, sp.Append(observeRequest(warmSessionID, 3)))
	// Not this run's hot-path population:
	require.NoError(t, sp.Append(ipc.Request{Op: ipc.OpAdminPing, Session: adminPingSessionID, TS: 1}))
	require.NoError(t, sp.Append(observeRequest("some-other-session", 4)))
	require.NoError(t, sp.Append(ipc.Request{Op: ipc.OpCheckpoint, Session: beSessionID, TS: 1}))

	// A daemon WAL segment in the same directory, holding a DELIVERED request. ipc.SpoolFiles
	// returns it alongside the client file; the census must not count it.
	walLine, err := ipc.EncodeRequest(observeRequest(baSessionID, 99))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		paths.Long(filepath.Join(spoolDir, "wal-"+string(baSessionID)+".ndjson")), walLine, 0o600))

	census, err := censusClientSpool(spoolDir, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	require.Equal(t, int64(3), census.Deferred,
		"two B-A spawns plus one warm-up request deferred; the admin ping, the checkpoint, the foreign session and the WAL segment are all excluded")
	require.Zero(t, census.Unreadable)
	require.Equal(t, 1, census.Files)
}

// TestCensusClientSpool_EmptyProjectIsZero pins the clean-run path: a project whose spool
// directory holds nothing (or does not exist at all) censuses to zero rather than erroring.
func TestCensusClientSpool_EmptyProjectIsZero(t *testing.T) {
	spoolDir, ownPath, _ := spoolFixture(t)
	census, err := censusClientSpool(spoolDir, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	require.Equal(t, spoolCensus{}, census)

	census, err = censusClientSpool(filepath.Join(t.TempDir(), "never-created"), ownPath, harnessHotPathSessions())
	require.NoError(t, err, "a project that never spooled has nothing to drain and nothing to census")
	require.Equal(t, spoolCensus{}, census)
}

// TestCensusClientSpool_CorruptLineIsCountedNotGuessedAt pins that an undecodable line is
// surfaced as Unreadable — which reconcileDelivery turns into a hard failure — rather than being
// silently skipped (which would understate the census and could turn a deferral into a false
// "lost") or silently counted (which would overstate it and could mask a real loss).
func TestCensusClientSpool_CorruptLineIsCountedNotGuessedAt(t *testing.T) {
	spoolDir, ownPath, sp := spoolFixture(t)
	require.NoError(t, sp.Append(observeRequest(baSessionID, 1)))
	if cl, ok := sp.(interface{ Close() error }); ok {
		require.NoError(t, cl.Close())
	}

	f, err := os.OpenFile(paths.Long(ownPath), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("{not json at all\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	census, err := censusClientSpool(spoolDir, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	require.Equal(t, int64(1), census.Deferred)
	require.Equal(t, int64(1), census.Unreadable)

	_, rerr := reconcileDelivery(2064, 2063, census)
	require.Error(t, rerr, "a spool line the harness cannot classify must stop the run, not be assumed benign")
}

// TestCensusAndReconcile_Run32296920486 is the end-to-end regression for the observed CI failure:
// 2063 of 2064 hot-path requests reached the daemon on macos-latest and the 2064th degraded to
// the spool path. Driven through the REAL spool writer and the REAL census, the run must
// reconcile as one DEFERRAL and zero losses — while the identical shortfall with an empty spool
// must still fail hard.
func TestCensusAndReconcile_Run32296920486(t *testing.T) {
	spoolDir, ownPath, sp := spoolFixture(t)
	require.NoError(t, sp.Append(observeRequest(baSessionID, 1999)))

	census, err := censusClientSpool(spoolDir, ownPath, harnessHotPathSessions())
	require.NoError(t, err)

	sent := expectedHotPathSends(2000, true)
	require.Equal(t, int64(2064), sent)

	ledger, err := reconcileDelivery(sent, 2063, census)
	require.NoError(t, err, "the daemon degrading one request to the spool is documented product behaviour, not an integrity failure")
	require.Equal(t, int64(1), ledger.Deferred)
	require.Zero(t, ledger.Lost)

	// The same arithmetic with nothing on disk to account for it is still a hard failure: the
	// guard's teeth are in the evidence, not in a loosened tolerance.
	_, err = reconcileDelivery(sent, 2063, spoolCensus{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "1 are LOST")
}
