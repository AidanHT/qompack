package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// staleAfter is §2.4's heartbeat staleness window (task-3-spec.md lock.go, staleness step 4):
// once daemon.hb's mtime is older than this, and neither the dial probe nor the POSIX kill(0)
// probe found a live process, the lock is reclaimed rather than blocking every future daemon start
// forever.
const staleAfter = 90 * time.Second

// dialProbeTimeout bounds the liveness dial the staleness protocol tries first — "costs nothing"
// per task-3-spec.md, so it is short.
const dialProbeTimeout = 20 * time.Millisecond

// The two files under .qompack/run/ the singleton protocol reads and writes.
const (
	lockFileName      = "daemon.lock"
	heartbeatFileName = "daemon.hb"
)

// ErrLockHeld is returned by AcquireLock when a live daemon already owns projectRoot's singleton
// lock: the liveness dial succeeded, or the POSIX kill(0)/heartbeat-staleness checks found the
// recorded process still alive. A caller that goes on to bind the transport itself may also see
// ipc.ErrAddrInUse well after AcquireLock returns successfully — a race where a second daemon won
// the bind between this daemon reclaiming the lock and its own Serve call — and should wrap that
// as ErrLockHeld too (ipc may not depend on daemon, so the wrapping happens on this side).
var ErrLockHeld = errors.New("qompack: daemon lock already held")

// LockInfo is daemon.lock's on-disk JSON shape (00-ARCHITECTURE.md §2.4).
type LockInfo struct {
	PID     int    `json:"pid"`
	Started int64  `json:"started"`
	Addr    string `json:"addr"`
	Version string `json:"version"`
}

// Lock is a held per-project singleton lock (.qompack/run/daemon.lock).
type Lock struct {
	path string
	hb   string
	clk  core.Clock
}

// AcquireLock takes .qompack/run/daemon.lock for the current process at addr, resolving
// contention with the 5-step staleness protocol of task-3-spec.md: a dead process's lock is
// reclaimed; a live one's is not.
func AcquireLock(projectRoot string, a ipc.Addr, clk core.Clock) (*Lock, error) {
	if clk == nil {
		clk = core.SystemClock()
	}

	runDir := paths.Of(projectRoot).Run
	if err := os.MkdirAll(paths.Long(runDir), 0o700); err != nil {
		return nil, fmt.Errorf("daemon: lock: mkdir: %w", err)
	}
	lockPath := filepath.Join(runDir, lockFileName)
	hbPath := filepath.Join(runDir, heartbeatFileName)

	body, err := json.Marshal(LockInfo{
		PID:     os.Getpid(),
		Started: clk.Now().UnixMilli(),
		Addr:    a.Path,
		Version: core.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("daemon: lock: encode: %w", err)
	}

	if err := paths.CreateNew(lockPath, body); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("daemon: lock: create: %w", err)
		}
		if !lockIsStale(lockPath, hbPath, a, clk) {
			return nil, ErrLockHeld
		}
		removeLockFiles(lockPath, hbPath)
		if err := paths.CreateNew(lockPath, body); err != nil {
			return nil, ErrLockHeld // lost the retry race: another daemon won it (step 5)
		}
	}

	// A freshly (re)claimed lock counts as "recently alive" from the moment it exists, not only
	// once the daemon's own 30s heartbeat ticker first fires — otherwise a second AcquireLock
	// arriving inside that first 30s window would see no heartbeat file at all and misjudge a
	// perfectly live daemon as stale.
	if err := touchFile(hbPath, clk.Now()); err != nil {
		return nil, fmt.Errorf("daemon: lock: initial heartbeat: %w", err)
	}

	return &Lock{path: lockPath, hb: hbPath, clk: clk}, nil
}

// LockPath returns the path ReadLock reads: <projectRoot>/.qompack/run/daemon.lock.
//
// It exists for the caller that wants to observe whether the lock is PRESENT rather than what it
// says, and that distinction is a real one on Windows. Go's os.Open/os.ReadFile — and so ReadLock —
// open with FILE_SHARE_READ|FILE_SHARE_WRITE and no FILE_SHARE_DELETE (syscall/syscall_windows.go),
// so for as long as a reader holds the file open, another process's os.Remove of it fails with
// ERROR_SHARING_VIOLATION. Lock.Release does exactly that os.Remove, so a caller polling ReadLock
// to watch for a shutdown can itself be what stops the shutdown from completing. os.Stat on this
// path answers the same question via GetFileAttributesEx, which takes no handle at all.
func LockPath(projectRoot string) string {
	return filepath.Join(paths.Of(projectRoot).Run, lockFileName)
}

// ReadLock reads projectRoot's current daemon.lock without taking it — for /qompack:status and
// diagnostics. ok is false when the file is missing or unparseable.
func ReadLock(projectRoot string) (LockInfo, bool) {
	return readLockFile(LockPath(projectRoot))
}

// lockIsStale runs the staleness protocol's steps 1-4 against an existing lock file, stopping at
// the first decisive answer. hbPath is the heartbeat file recorded alongside the lock.
func lockIsStale(lockPath, hbPath string, a ipc.Addr, clk core.Clock) bool {
	info, ok := readLockFile(lockPath)
	if !ok {
		return true // step 1: unparseable lock file
	}

	// Step 2: the authoritative liveness dial (Ruling #22 — a successful DIAL, not a round trip
	// through a request/response). ipc.Probe connects and closes without writing anything, against
	// the address the lock file recorded (falling back to the caller's own resolved address if the
	// recorded one is empty). A positive answer is decisive; anything else — no listener, a
	// timeout — is not proof of death on its own, so the protocol falls through to steps 3 and 4
	// rather than trusting a negative network result outright.
	addr := a
	if info.Addr != "" {
		addr = ipc.Addr{Kind: a.Kind, Path: info.Addr}
	}
	if ipc.Probe(addr, dialProbeTimeout) {
		return false
	}

	// Step 3: POSIX kill(0, pid). known is false on Windows, which has no cheap equivalent.
	if alive, known := pidAlive(info.PID); known {
		return !alive
	}

	// Step 4: heartbeat mtime, falling back to the lock file's own recorded Started time when no
	// heartbeat file exists yet. This closes the acquisition race in AcquireLock between
	// CreateNew(lockPath) becoming visible and touchFile(hbPath) actually running: without this
	// fallback, a competing AcquireLock landing in that window would see "no heartbeat at all" and
	// misjudge a lock created microseconds ago as stale — reclaiming it out from under its rightful
	// owner. Anchoring on Started instead means "just created" is never indistinguishable from
	// "never had a chance to report in".
	lastSeen := time.UnixMilli(info.Started)
	if fi, err := os.Stat(paths.Long(hbPath)); err == nil {
		lastSeen = fi.ModTime()
	}
	return clk.Now().Sub(lastSeen) > staleAfter
}

// readLockFile reads and parses p, reporting ok=false for anything that is missing or does not
// decode as LockInfo — step 1 of the staleness protocol treats both the same way.
func readLockFile(p string) (LockInfo, bool) {
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return LockInfo{}, false
	}
	var info LockInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return LockInfo{}, false
	}
	return info, true
}

// removeLockFiles deletes a stale lock and its heartbeat (staleness protocol step 5).
// paths.CreateNew leaves the lock file read-only (0o444/FILE_ATTRIBUTE_READONLY), which blocks
// deletion on Windows, so its mode is cleared first — harmless on POSIX, where permissions never
// gate an unlink.
func removeLockFiles(lockPath, hbPath string) {
	_ = os.Chmod(paths.Long(lockPath), 0o600)
	_ = os.Remove(paths.Long(lockPath))
	_ = os.Remove(paths.Long(hbPath))
}

// touchFile creates p if it does not exist and sets both its atime and mtime to t.
func touchFile(p string, t time.Time) error {
	if _, err := os.Stat(paths.Long(p)); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		f, cerr := os.OpenFile(paths.Long(p), os.O_CREATE|os.O_WRONLY, 0o600)
		if cerr != nil {
			return cerr
		}
		if cerr := f.Close(); cerr != nil {
			return cerr
		}
	}
	return os.Chtimes(paths.Long(p), t, t)
}

// owned reports whether the lock file on disk still names this process — false once it has been
// reclaimed by a later AcquireLock (a legitimate 90s-stale takeover, or the race I-2 fixed), or
// once it has already been removed. Heartbeat and Release both consult this before touching
// anything on disk, so a daemon that no longer owns the lock never refreshes or deletes the file
// that belongs to whoever holds it now.
func (l *Lock) owned() bool {
	info, ok := readLockFile(l.path)
	return ok && info.PID == os.Getpid()
}

// Heartbeat updates daemon.hb's mtime to now, creating the file if it is somehow absent. The
// daemon calls this on a 30s ticker (Task 4); AcquireLock also calls it once, immediately, so a
// lock is never seen as stale before the first tick fires. It refuses to write if this process no
// longer owns the lock (see owned).
func (l *Lock) Heartbeat() error {
	if !l.owned() {
		return fmt.Errorf("daemon: lock: heartbeat: no longer own %s", l.path)
	}
	if err := touchFile(l.hb, l.clk.Now()); err != nil {
		return fmt.Errorf("daemon: lock: heartbeat: %w", err)
	}
	return nil
}

// Release removes the lock and its heartbeat. It is safe to call twice: a second call finds the
// lock already gone (owned reports false) and returns nil without touching anything. It is also
// safe to call after the lock has been reclaimed by a different process (owned reports false for
// the same reason): Release never deletes a file it does not currently own.
func (l *Lock) Release() error {
	if !l.owned() {
		return nil
	}
	_ = os.Chmod(paths.Long(l.path), 0o600) // paths.CreateNew leaves the lock file read-only
	err1 := os.Remove(paths.Long(l.path))
	if err1 != nil && os.IsNotExist(err1) {
		err1 = nil
	}
	err2 := os.Remove(paths.Long(l.hb))
	if err2 != nil && os.IsNotExist(err2) {
		err2 = nil
	}
	if err1 != nil {
		return fmt.Errorf("daemon: lock: release: %w", err1)
	}
	if err2 != nil {
		return fmt.Errorf("daemon: lock: release: %w", err2)
	}
	return nil
}
