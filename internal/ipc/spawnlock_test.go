package ipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// sendOnceForLazySpawn sends one fire-and-forget event to root's unreachable daemon, so the
// client's lazy spawn runs, and returns how many spawns it made.
func sendOnceForLazySpawn(t *testing.T, root string, spawn func(string, string) error) int64 {
	t.Helper()
	addr, err := Resolve(root)
	require.NoError(t, err)
	var calls atomic.Int64
	c, _ := newTestClient(t, addr, ClientOptions{
		ProjectRoot: root, Self: "qompack-fake",
		Spawn: func(r, s string) error { calls.Add(1); return spawn(r, s) },
	})
	_, err = c.Send(context.Background(), Request{Op: OpObserveTool, Session: "s", TS: 1}, time.Second)
	require.NoError(t, err)
	return calls.Load()
}

// TestLazySpawn_ALockStillBeingWrittenIsInFlight: paths.CreateNew creates spawn.lock empty and only
// then writes its timestamp, so a second spawner can read it in between. An empty lock that was
// created just now is another spawner mid-write, not garbage: reclaiming it would start a second
// daemon beside the first.
func TestLazySpawn_ALockStillBeingWrittenIsInFlight(t *testing.T) {
	root := t.TempDir()
	run := paths.Of(root).Run
	require.NoError(t, os.MkdirAll(run, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(run, spawnLockName), nil, 0o600))

	n := sendOnceForLazySpawn(t, root, func(string, string) error { return nil })
	require.EqualValues(t, 0, n, "an empty spawn.lock created just now is a claim still being written")
}

// TestLazySpawn_AnOldUnreadableLockIsReclaimed: an unparseable lock is still never allowed to block
// forever — once its file is older than the freshness window it is reclaimed like any stale lock.
func TestLazySpawn_AnOldUnreadableLockIsReclaimed(t *testing.T) {
	root := t.TempDir()
	run := paths.Of(root).Run
	require.NoError(t, os.MkdirAll(run, 0o700))
	p := filepath.Join(run, spawnLockName)
	require.NoError(t, os.WriteFile(p, []byte("not a timestamp"), 0o600))
	old := time.Now().Add(-spawnLockStaleAfter - time.Second)
	require.NoError(t, os.Chtimes(p, old, old))

	n := sendOnceForLazySpawn(t, root, func(string, string) error { return nil })
	require.EqualValues(t, 1, n, "an unreadable lock older than the freshness window must be reclaimed")
}

// TestLazySpawn_AFutureStampDoesNotHoldSpawnsOff: a stamp dated after now (a clock stepped back, or
// bytes that only parse as one) is judged by the file's age, like an unparseable lock. Read as a
// stamp it would be "fresh" until its date came round and hold every spawn off until then.
func TestLazySpawn_AFutureStampDoesNotHoldSpawnsOff(t *testing.T) {
	root := t.TempDir()
	run := paths.Of(root).Run
	require.NoError(t, os.MkdirAll(run, 0o700))
	p := filepath.Join(run, spawnLockName)
	future := time.Now().Add(spawnLockFutureStampOffset).UnixMilli()
	require.NoError(t, os.WriteFile(p, []byte(strconv.FormatInt(future, 10)), 0o600))
	old := time.Now().Add(-spawnLockStaleAfter - time.Second)
	require.NoError(t, os.Chtimes(p, old, old))

	n := sendOnceForLazySpawn(t, root, func(string, string) error { return nil })
	require.EqualValues(t, 1, n, "a future stamp on a lock older than the freshness window must be reclaimed")
}

// spawnLockFutureStampOffset dates TestLazySpawn_AFutureStampDoesNotHoldSpawnsOff's stamp a day
// ahead: far past any freshness window, so only the file's own age can decide.
const spawnLockFutureStampOffset = 24 * time.Hour

// TestLazySpawn_ReleasesTheClaimWhenSpawnFails: a spawn that could not start leaves no claim to hold
// every other spawner off for the freshness window.
func TestLazySpawn_ReleasesTheClaimWhenSpawnFails(t *testing.T) {
	root := t.TempDir()
	n := sendOnceForLazySpawn(t, root, func(string, string) error { return errors.New("fixture: exec failed") })
	require.EqualValues(t, 1, n)
	require.NoFileExists(t, filepath.Join(paths.Of(root).Run, spawnLockName),
		"a failed spawn must release its spawn.lock")
}
