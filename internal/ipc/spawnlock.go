package ipc

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// spawnLockName is the file a spawner takes inside <root>/.qompack/run before it launches a detached
// daemon, so that concurrently-running spawners — every hook's lazy spawn, the MCP server's, and
// session-start's EnsureRunning — start one daemon between them rather than one each.
const spawnLockName = "spawn.lock"

// spawnLockStaleAfter is how old an unclaimed spawn.lock has to be before a later spawner assumes
// the spawn attempt it recorded never finished and spawns again. It is the bound that keeps a
// spawner that died after taking the lock from holding every later spawn off for good: within it a
// spawn is "in flight", past it the lock is reclaimed.
const spawnLockStaleAfter = 10 * time.Second

// SpawnClaim is what ClaimSpawn found.
type SpawnClaim int

const (
	// SpawnClaimed: the caller now holds spawn.lock and should spawn the daemon. If the spawn cannot
	// start, it Releases the lock so the next spawner is not held off by a spawn that never happened.
	SpawnClaimed SpawnClaim = iota + 1
	// SpawnInFlight: another spawner claimed the lock within spawnLockStaleAfter. Its daemon is on its
	// way; the caller waits for it, or leaves it to arrive, and spawns nothing.
	SpawnInFlight
	// SpawnUnclaimable: the lock could not be taken for a reason other than contention — the run
	// directory could not be created, say, or the project root is the home directory (D18). What
	// that means is the caller's rule: a hook's lazy spawn gives up quietly, session-start's
	// EnsureRunning, the designated starter, spawns regardless — after refusing the home directory
	// itself.
	SpawnUnclaimable
)

// SpawnLock is a held claim on a project's spawn.lock. The daemon a claimant launches removes the
// file once it listens (daemon.removeSpawnLockFile); a claimant whose spawn failed calls Release;
// and whoever holds daemon.lock removes it as it lets that lock go (daemon's Lock.Release, V6
// close-out D27), so a claim whose daemon lost the singleton lock does not outlive the winner.
type SpawnLock struct {
	path  string
	stamp []byte
}

// ClaimSpawn is the spawn-lock rule every spawner of a project's daemon follows (V6 close-out D17):
// the first spawner to create run/spawn.lock spawns; a later one that finds a lock younger than
// spawnLockStaleAfter treats the spawn as in flight and spawns nothing; a lock older than that is
// presumed abandoned and reclaimed. The lock records clk's UnixMilli, and its age is measured
// against clk.
//
// A lock that exists but does not parse is judged by the file's own modification time instead of
// being reclaimed at once. paths.CreateNew creates the file empty and only then writes and syncs the
// stamp, so a spawner reading it in between sees an empty file: that is another claim still being
// written, and reclaiming it would start a second daemon beside the first. An unparseable lock older
// than spawnLockStaleAfter is still reclaimed, so garbage never blocks spawning for longer than a
// stale stamp would. A stamp dated after clk's now is judged the same way: a clock stepped back, or
// a stamp that only looks like one, must not read as fresh until its date comes round.
//
// No claim is ever taken for the home directory (owner decision D18, refuseSpawnRoot): its
// .qompack is the user-global layer, so not even run/ is created there, and the claim is
// SpawnUnclaimable.
func ClaimSpawn(projectRoot string, clk core.Clock) (*SpawnLock, SpawnClaim) {
	if clk == nil {
		clk = core.SystemClock()
	}
	if refuseSpawnRoot(projectRoot) != nil {
		return nil, SpawnUnclaimable
	}
	runDir := paths.Of(projectRoot).Run
	// A spawner may be the very first process to touch this project's .qompack tree (a cold daemon
	// means nothing has run yet), so run/ cannot be assumed to exist.
	if err := os.MkdirAll(paths.Long(runDir), dirPerm); err != nil {
		return nil, SpawnUnclaimable
	}
	lockPath := filepath.Join(runDir, spawnLockName)
	stamp := []byte(strconv.FormatInt(clk.Now().UnixMilli(), 10))

	// Two attempts: the lock can vanish, or go stale and be removed, between a failed create and the
	// retry. A second failure means another spawner claimed it in that window.
	for range 2 {
		err := paths.CreateNew(lockPath, stamp)
		if err == nil {
			return &SpawnLock{path: lockPath, stamp: stamp}, SpawnClaimed
		}
		if !os.IsExist(err) {
			return nil, SpawnUnclaimable
		}
		switch st, held := readSpawnLock(lockPath, clk); st {
		case spawnLockFresh:
			return nil, SpawnInFlight
		case spawnLockStale:
			removeSpawnLockIf(lockPath, held)
		case spawnLockStaleUnreadable:
			removeSpawnLockFile(lockPath)
		case spawnLockGone:
			// Removed since the create failed (its daemon came up, or its claimant released it).
		}
	}
	return nil, SpawnInFlight
}

// refuseSpawnRoot returns the D18 refusal, which wraps paths.ErrHomeRoot, when projectRoot is this
// process's home directory — HOME or USERPROFILE, the homes daemon.AcquireLock refuses — and nil
// otherwise. Every entry point in internal/cli refuses that root before it spawns anything, and a
// spawned daemon refuses it at its lock; the spawners ask as well (V6 close-out w6-borrow), so a
// future caller that skips the entry check still creates no spawn.lock under ~/.qompack and starts
// no daemon for it.
func refuseSpawnRoot(projectRoot string) error {
	return paths.RefuseHome(projectRoot, paths.HomeDirs(os.Getenv)...)
}

// Release removes the claim, if spawn.lock still holds it. A claim that went stale and was reclaimed
// by another spawner is that spawner's now, and is left alone. Release on a nil *SpawnLock is a
// no-op, so a caller can release whatever ClaimSpawn returned.
func (l *SpawnLock) Release() {
	if l == nil {
		return
	}
	removeSpawnLockIf(l.path, l.stamp)
}

// spawnLockState is what readSpawnLock found at the lock path.
type spawnLockState int

const (
	spawnLockGone            spawnLockState = iota + 1 // no file: nothing to reclaim, claim again
	spawnLockFresh                                     // a claim made within spawnLockStaleAfter
	spawnLockStale                                     // an older claim, whose contents were read
	spawnLockStaleUnreadable                           // an older file that could not be read at all
)

// readSpawnLock classifies the lock at lockPath, returning the contents it judged when it read them.
// The read goes through paths.ReadFileShared, not os.ReadFile, because spawn.lock has a deleter in
// ANOTHER process: the daemon a spawner launched removes it as soon as it is listening, and a
// competing spawner removes a stale one. An os.ReadFile handle carries no FILE_SHARE_DELETE, so on
// Windows a spawner sitting in this check would make that delete fail with ERROR_SHARING_VIOLATION
// and leave behind a lock that suppresses every later spawn until it ages out. ReadFileShared grants
// delete sharing, so the cleanup lands.
func readSpawnLock(lockPath string, clk core.Clock) (spawnLockState, []byte) {
	b, err := paths.ReadFileShared(lockPath)
	switch {
	case os.IsNotExist(err):
		return spawnLockGone, nil
	case err != nil:
		// Present but unreadable: judge it by its modification time, like an unparseable one.
		if statFresh(lockPath) {
			return spawnLockFresh, nil
		}
		return spawnLockStaleUnreadable, nil
	}
	b = bytes.TrimSpace(b)
	var fresh bool
	ms, perr := strconv.ParseInt(string(b), 10, 64)
	if age := clk.Now().Sub(time.UnixMilli(ms)); perr == nil && age >= 0 {
		fresh = age <= spawnLockStaleAfter
	} else {
		fresh = statFresh(lockPath)
	}
	if fresh {
		return spawnLockFresh, b
	}
	return spawnLockStale, b
}

// statFresh reports whether the file at p was modified within spawnLockStaleAfter of the real
// current time — the clock the filesystem stamped it with. A file that cannot be stat'ed is not
// fresh: nothing about it can hold a spawn off.
func statFresh(p string) bool {
	fi, err := os.Stat(paths.Long(p))
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) <= spawnLockStaleAfter
}

// removeSpawnLockIf deletes the spawn.lock at lockPath if it still holds want, so that a spawner
// removing a stale claim, or releasing its own, never deletes a claim made since. It narrows the
// window rather than closing it (files have no compare-and-delete): two spawners reclaiming the same
// stale lock at the same instant can still both spawn, and the second daemon then loses the
// singleton lease and exits.
func removeSpawnLockIf(lockPath string, want []byte) {
	b, err := paths.ReadFileShared(lockPath)
	if err != nil || !bytes.Equal(bytes.TrimSpace(b), want) {
		return
	}
	removeSpawnLockFile(lockPath)
}

// removeSpawnLockFile deletes the spawn.lock at lockPath. paths.CreateNew marks the file read-only,
// which on Windows blocks DeleteFile outright, so the mode is cleared first — harmless on POSIX,
// where file permissions never gate an unlink.
func removeSpawnLockFile(lockPath string) {
	_ = os.Chmod(paths.Long(lockPath), 0o600)
	_ = os.Remove(paths.Long(lockPath))
}
