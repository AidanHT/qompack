package testutil

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// lockHolderChildEnv, when set, turns this test binary into the stand-in lock holder the staged
// ShutdownDaemonUntilGone tests launch: it says lockHolderReady and exits with lockHolderChildExit
// once its stdin closes. It touches nothing on disk, so the only thing it can "hold" is its own pid,
// which the test writes into the project's daemon.lock itself.
const lockHolderChildEnv = "QOMPACK_TESTUTIL_LOCKHOLDER_CHILD"

const (
	// lockHolderReady is the line the stand-in prints once it is running.
	lockHolderReady = "lockholder-child-ready"
	// lockHolderChildExit is the stand-in's exit code. It is not zero because a test function that
	// calls os.Exit(0) is reported as a failure by the testing package itself.
	lockHolderChildExit = 3

	// stagedShutdownTick paces the helper in the staged tests. Every attempt appends one line to the
	// project's client spool, which is how the tests see the helper's progress, so a short tick only
	// makes the staged sequence quicker; the helper's correctness does not depend on it.
	stagedShutdownTick = 25 * time.Millisecond
	// stagedShutdownRoundTrip is each attempt's connect and ACK deadline. Nothing ever listens at a
	// staged project's address, so a connect fails at once and the deadline is never reached.
	stagedShutdownRoundTrip = 5 * time.Second
	// stagedShutdownBound is the helper's bound in the staged holder test, and every wait in the test
	// is bounded by it too. The helper returns as soon as the stand-in exits, so a passing run never
	// spends it; it only has to hold the staged sequence, which is four lock changes a few ticks
	// apart. It is daemon.StopCleanupBound, the bound every real caller's own must outlast.
	stagedShutdownBound = daemon.StopCleanupBound
	// stagedUnidentifiedBound is the helper's bound in the unidentified-holder test, which by design
	// waits all of it: with no pid to ask, the bound is the only way out. It only has to hold one
	// attempt, one lock removal and two more attempts before it runs out; a bound that ran out first
	// would fail the test (the helper returning before the removal was seen), never pass it.
	stagedUnidentifiedBound = 3 * time.Second
	// lockHolderExitBound bounds how long the stand-in may take to exit once its stdin closes.
	lockHolderExitBound = 30 * time.Second
)

// lockHolderChild is the stand-in's whole life: announce, then block until stdin closes.
func lockHolderChild() {
	_, _ = os.Stdout.WriteString(lockHolderReady + "\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(lockHolderChildExit)
}

// startLockHolder launches this test binary as the stand-in, running only the test named by run
// (which must begin by handing control to lockHolderChild), and returns it running together with
// the pipe that makes it exit when closed. The caller's cleanup reaps it.
func startLockHolder(t *testing.T, run string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "-test.run="+run)
	cmd.Env = append(os.Environ(), lockHolderChildEnv+"=1")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start(), "fixture: the stand-in lock holder must start")

	ready := false
	for sc := bufio.NewScanner(stdout); !ready && sc.Scan(); {
		ready = strings.HasPrefix(sc.Text(), lockHolderReady)
	}
	if !ready {
		_ = cmd.Process.Kill() // this test's own child, never another process
		_ = cmd.Wait()
		require.FailNow(t, "fixture: the stand-in lock holder never said it was ready")
	}
	return cmd, stdin
}

// reapLockHolder waits for the stand-in, killing it if it outlives lockHolderExitBound. The
// stand-in is this test's own child, never another process.
func reapLockHolder(cmd *exec.Cmd) {
	reaped := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(reaped)
	}()
	select {
	case <-reaped:
	case <-time.After(lockHolderExitBound):
		_ = cmd.Process.Kill()
		<-reaped
	}
}

// stagedShutdown is one ShutdownDaemonUntilGone call running in the background against a staged
// project root, with what the tests need to watch it: the root's spool directory, where every attempt
// lands in the call's own client spool because nothing listens, and its outcome once it returns.
type stagedShutdown struct {
	t     *testing.T
	spool string
	done  chan struct{}
	out   ShutdownOutcome
}

// startStagedShutdown starts the call. root's lock must already be staged. The call is joined
// before the test ends, since it would otherwise outlive root.
func startStagedShutdown(t *testing.T, root string, w ShutdownWait) *stagedShutdown {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	s := &stagedShutdown{
		t:     t,
		spool: paths.Of(root).Spool,
		done:  make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		s.out = ShutdownDaemonUntilGone(root, addr, w)
	}()
	t.Cleanup(func() {
		select {
		case <-s.done:
		case <-time.After(w.Bound + w.Bound):
		}
	})
	return s
}

// attempts counts the admin.shutdown requests the call has made so far: the lines in the root's
// client spools, of which the call's own writer is the only one. Its file's name carries an id the
// writer drew, so the count reads every client spool rather than reconstruct the name.
func (s *stagedShutdown) attempts() int {
	files, _ := ipc.SpoolFiles(s.spool)
	n := 0
	for _, f := range files {
		if ipc.SpoolFileKindOf(filepath.Base(f)) != ipc.SpoolFileClient {
			continue
		}
		b, _ := paths.ReadFileShared(f)
		n += strings.Count(string(b), `"`+string(ipc.OpAdminShutdown)+`"`)
	}
	return n
}

// awaitAttempts waits until the call has made at least n attempts, and fails the test if the call
// returns first: every attempt is preceded by a lock check, so the call has seen whatever the test
// staged before the last attempt it counted.
func (s *stagedShutdown) awaitAttempts(n int, bound time.Duration, why string) {
	s.t.Helper()
	ticker := time.NewTicker(stagedShutdownTick)
	defer ticker.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for s.attempts() < n {
		select {
		case <-s.done:
			require.FailNowf(s.t, "ShutdownDaemonUntilGone returned too early",
				"it returned %s, after %d attempts, with outcome %+v", why, s.attempts(), s.out)
		case <-deadline.C:
			require.FailNowf(s.t, "ShutdownDaemonUntilGone stalled", "no attempt %d within %s %s", n, bound, why)
		case <-ticker.C:
		}
	}
}

// stageEmptyLock creates root's daemon.lock empty: the state paths.CreateNew leaves between creating
// the file and writing its body.
func stageEmptyLock(t *testing.T, root string) string {
	t.Helper()
	lockPath := daemon.LockPath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(lockPath)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(lockPath), nil, 0o600),
		"fixture: the empty daemon.lock a starting daemon's CreateNew leaves before its body lands")
	return lockPath
}

// stageLockBody writes a lock body naming pid into root's daemon.lock.
func stageLockBody(t *testing.T, lockPath string, pid int) {
	t.Helper()
	body, err := json.Marshal(daemon.LockInfo{PID: pid, Started: time.Now().UnixMilli()})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(lockPath), body, 0o600))
}

// deadPID is a pid no process carries: far above Linux's pid_max ceiling (2^22) and above any
// process id Windows hands out. The tests that use it check that ProcessAlive agrees first.
const deadPID = math.MaxInt32 - 2

// TestLockHolders_AnEmptyLockIsNeverSettled pins lockHolders' bookkeeping over the lock histories a
// shutdown helper can see, one DaemonHoldingLock answer per step: (pid, held).
//
// The row that matters most is the first: a lock seen only as an empty file, pid 0, is a daemon
// that has just created it, and it is not settled — the defect every sibling helper under test/ had
// was a single read of the lock that learned pid 0 and then counted "has pid 0 exited?" as yes.
func TestLockHolders_AnEmptyLockIsNeverSettled(t *testing.T) {
	require.False(t, ProcessAlive(deadPID), "fixture: deadPID must name no process")
	parent := os.Getppid()
	require.True(t, ProcessAlive(parent), "fixture: the process that started this test is alive")

	type step struct {
		pid  int
		held bool
	}
	for _, tc := range []struct {
		name         string
		steps        []step
		settled      bool
		unidentified bool
		running      []int
	}{
		{name: "empty lock, still there", steps: []step{{0, true}}},
		{name: "empty lock, then gone with no pid ever read", steps: []step{{0, true}, {0, false}}, unidentified: true},
		{
			name:  "empty lock, then a live holder's body, then gone",
			steps: []step{{0, true}, {parent, true}, {0, false}}, running: []int{parent},
		},
		{name: "empty lock, then a body naming a dead process", steps: []step{{0, true}, {deadPID, false}}, settled: true},
		{name: "our own pid, then gone", steps: []step{{0, true}, {os.Getpid(), true}, {0, false}}, settled: true},
		{name: "no lock at all", steps: []step{{0, false}}, settled: true},
		{name: "a lock abandoned by a dead process", steps: []step{{deadPID, false}}, settled: true},
		{
			name:  "gone, then a new empty lock",
			steps: []step{{0, true}, {0, false}, {0, true}}, unidentified: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLockHolders()
			for _, s := range tc.steps {
				h.observe(s.pid, s.held)
			}
			require.Equal(t, tc.settled, h.settled(), "settled")
			require.Equal(t, tc.unidentified, h.unidentified, "unidentified")
			require.Equal(t, tc.running, h.running(), "running")
		})
	}
}

// TestShutdownDaemonUntilGone_WaitsForEveryLockHolderToExit is the spawn-in-flight case: the helper
// starts while a daemon has created daemon.lock but not yet written its body, which is what a
// shutdown helper sees when it catches a daemon that is still coming up (test/e2e's
// e2eAwaitSpawnInFlight returns at the first sighting of a held lock, which can be exactly that
// empty file). The sibling helpers in test/fault, test/platform, test/release, test/security and
// test/guards each read the lock ONCE as their handshake began, learned pid 0 there, counted pid 0
// as exited, and so returned the moment the lock disappeared — while the daemon was still unwinding.
//
// The test stages that sequence with a process it controls, so both outcomes are decided by
// causality, not by timing:
//   - an empty daemon.lock is all the helper can read when it starts;
//   - once its loop is running, the lock gains a body naming a live stand-in;
//   - after the helper has read that body, the lock disappears while the stand-in keeps running,
//     as a daemon does between Lock.Release, Stop's last act, and its exit.
//
// A helper that returns while the stand-in runs fails deterministically, because the stand-in exits
// only when this test closes its stdin.
func TestShutdownDaemonUntilGone_WaitsForEveryLockHolderToExit(t *testing.T) {
	if os.Getenv(lockHolderChildEnv) != "" {
		lockHolderChild()
	}
	root := t.TempDir()
	holder, holderIn := startLockHolder(t, "^TestShutdownDaemonUntilGone_WaitsForEveryLockHolderToExit$")
	holderPID := holder.Process.Pid
	// The stand-in is not reaped until the end, so its pid names it and nothing else throughout:
	// Windows keeps a pid unused while cmd holds the process handle, and on Linux an exited child
	// stays a zombie, which ProcessAlive reports as exited, until it is reaped.
	t.Cleanup(func() {
		_ = holderIn.Close()
		reapLockHolder(holder)
	})

	lockPath := stageEmptyLock(t, root)
	s := startStagedShutdown(t, root, ShutdownWait{
		Tick: stagedShutdownTick, Bound: stagedShutdownBound, RoundTrip: stagedShutdownRoundTrip,
	})

	s.awaitAttempts(1, stagedShutdownBound, "before its loop started")

	stageLockBody(t, lockPath, holderPID)
	// An attempt that starts after the body landed is preceded by a check that reads it.
	s.awaitAttempts(s.attempts()+2, stagedShutdownBound, "before it read the lock's holder")

	require.NoError(t, os.Remove(paths.Long(lockPath)))
	s.awaitAttempts(s.attempts()+2, stagedShutdownBound, "once the lock was released, while its holder was still running")
	require.True(t, ProcessAlive(holderPID), "fixture: the stand-in (pid %d) must still be running", holderPID)

	// The holder exits, and only now may the helper return.
	require.NoError(t, holderIn.Close())
	select {
	case <-s.done:
	case <-time.After(stagedShutdownBound):
		t.Fatalf("ShutdownDaemonUntilGone did not return within %s of the last lock holder exiting", stagedShutdownBound)
	}
	require.False(t, ProcessAlive(holderPID),
		"ShutdownDaemonUntilGone returned while the stand-in lock holder (pid %d) was still running", holderPID)
	require.True(t, s.out.Gone, "outcome %+v", s.out)
	require.False(t, s.out.LockHeld)
}

// TestShutdownDaemonUntilGone_WaitsOutALockHolderItNeverIdentified pins the one lock history the
// helper cannot answer by asking a process: a lock it saw held, with no pid it could read, that was
// gone by its next check. That is what it sees when a daemon's whole life after CreateNew falls
// between two checks: the body lands, the daemon listens, takes the helper's own admin.shutdown, and
// releases the lock (Stop runs on a goroutine of its own and can release before the reply is
// written). No check read a pid, so none can ask whether that daemon has exited, and it may still be
// unwinding.
//
// The test stages that history with no process at all: an empty daemon.lock, removed after the
// helper's loop has seen it, without a body ever landing. The helper must not return at the next
// check, and the most it can do is wait out its own bound, which the return is asserted against from
// below — no host load can make that fail.
func TestShutdownDaemonUntilGone_WaitsOutALockHolderItNeverIdentified(t *testing.T) {
	root := t.TempDir()
	lockPath := stageEmptyLock(t, root)

	called := time.Now()
	s := startStagedShutdown(t, root, ShutdownWait{
		Tick: stagedShutdownTick, Bound: stagedUnidentifiedBound, RoundTrip: stagedShutdownRoundTrip,
	})
	s.awaitAttempts(1, stagedUnidentifiedBound, "before its loop started")

	require.NoError(t, os.Remove(paths.Long(lockPath)))
	s.awaitAttempts(s.attempts()+2, stagedUnidentifiedBound, "once a lock whose holder it never identified was released")

	select {
	case <-s.done:
	case <-time.After(stagedUnidentifiedBound + stagedUnidentifiedBound):
		t.Fatalf("ShutdownDaemonUntilGone did not return within twice its own bound (%s)", stagedUnidentifiedBound)
	}
	require.GreaterOrEqual(t, time.Since(called), stagedUnidentifiedBound,
		"with no pid to ask, the helper may only stop waiting at its own bound")
	require.False(t, s.out.Gone, "outcome %+v", s.out)
	require.True(t, s.out.Unidentified, "outcome %+v", s.out)
	require.False(t, s.out.LockHeld, "outcome %+v", s.out)
	require.Contains(t, s.out.Describe(lockPath, stagedUnidentifiedBound), "no readable pid")
}

// TestShutdownDaemonUntilGone_ReadsTheHolderBeforeEachShutdown pins the check the helper makes
// immediately BEFORE each admin.shutdown. It stages the ordinary case every caller meets: a daemon
// that is fully up, its lock body written and its address listening, whose shutdown releases the
// lock before the reply reaches the helper. internal/daemon's handleAdminShutdown starts Stop on a
// goroutine of its own and returns its reply, so Lock.Release, Stop's last act, can land before
// the helper reads the lock again after its Send. The check after the Send then finds no lock and
// has read no pid, and only the check before the Send has named the process that may still be
// unwinding.
//
// The stand-in "daemon" is split in two: this test serves the project's address itself, with a
// handler that removes daemon.lock and only then answers, and the lock names a live stand-in
// process that exits only when this test closes its stdin. A helper that learned holders only after
// its Send would find nothing to wait for at its first check and return while the stand-in runs.
func TestShutdownDaemonUntilGone_ReadsTheHolderBeforeEachShutdown(t *testing.T) {
	if os.Getenv(lockHolderChildEnv) != "" {
		lockHolderChild()
	}
	root := t.TempDir()
	holder, holderIn := startLockHolder(t, "^TestShutdownDaemonUntilGone_ReadsTheHolderBeforeEachShutdown$")
	holderPID := holder.Process.Pid
	t.Cleanup(func() {
		_ = holderIn.Close()
		reapLockHolder(holder)
	})
	lockPath := stageEmptyLock(t, root)
	stageLockBody(t, lockPath, holderPID)

	// The address listens only once the body is on disk, as a daemon's does (daemon.Run takes the
	// lock and writes its body before server.Serve).
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), ipc.MaxLineBytes)
	require.NoError(t, err)
	var shutdowns atomic.Int64
	serveCtx, stopServing := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(serveCtx, func(_ context.Context, req ipc.Request) ipc.Response {
			if req.Op == ipc.OpAdminShutdown {
				// Released before the reply, as Stop on its own goroutine can release it. A second
				// shutdown finds the lock already gone, which is not an error for a stopping daemon.
				if rmErr := os.Remove(paths.Long(lockPath)); rmErr != nil && !os.IsNotExist(rmErr) {
					return ipc.Response{OK: false, Err: rmErr.Error()}
				}
				shutdowns.Add(1)
			}
			return ipc.Response{OK: true}
		})
	}()
	// Registered before startStagedShutdown's join, so it runs after it (cleanups run last-registered
	// first): the address goes on answering until the helper has returned.
	t.Cleanup(func() {
		stopServing()
		_ = srv.Close()
		<-served
	})

	s := startStagedShutdown(t, root, ShutdownWait{
		Tick: stagedShutdownTick, Bound: stagedShutdownBound, RoundTrip: stagedShutdownRoundTrip,
	})

	// Two shutdowns received: the lock was released by the first, the helper checked after it,
	// found no lock, and asked again instead of returning.
	ticker := time.NewTicker(stagedShutdownTick)
	defer ticker.Stop()
	deadline := time.NewTimer(stagedShutdownBound)
	defer deadline.Stop()
	for shutdowns.Load() < 2 {
		select {
		case <-s.done:
			require.FailNowf(t, "ShutdownDaemonUntilGone returned too early",
				"it returned after %d received shutdowns, while the lock's holder (pid %d, alive=%v) was "+
					"still running, with outcome %+v", shutdowns.Load(), holderPID, ProcessAlive(holderPID), s.out)
		case <-deadline.C:
			require.FailNowf(t, "ShutdownDaemonUntilGone stalled",
				"only %d shutdowns received within %s", shutdowns.Load(), stagedShutdownBound)
		case <-ticker.C:
		}
	}
	require.True(t, ProcessAlive(holderPID), "fixture: the stand-in (pid %d) must still be running", holderPID)
	_, held := DaemonHoldingLock(root)
	require.False(t, held, "fixture: the first shutdown released the lock")

	require.NoError(t, holderIn.Close())
	select {
	case <-s.done:
	case <-time.After(stagedShutdownBound):
		t.Fatalf("ShutdownDaemonUntilGone did not return within %s of the lock's holder exiting", stagedShutdownBound)
	}
	require.False(t, ProcessAlive(holderPID),
		"ShutdownDaemonUntilGone returned while the lock's holder (pid %d) was still running", holderPID)
	require.True(t, s.out.Gone, "outcome %+v", s.out)
}

// TestShutdownDaemonUntilGone_AnAbandonedLockIsGoneAtOnce pins the other side of the definition: a
// lock naming a process that has already exited counts as released, so the helper does not spend
// its bound on a file nobody will ever remove, which is what a daemon killed before Lock.Release
// leaves behind.
func TestShutdownDaemonUntilGone_AnAbandonedLockIsGoneAtOnce(t *testing.T) {
	require.False(t, ProcessAlive(deadPID), "fixture: deadPID must name no process")
	root := t.TempDir()
	stageLockBody(t, stageEmptyLock(t, root), deadPID)
	s := startStagedShutdown(t, root, ShutdownWait{
		Tick: stagedShutdownTick, Bound: stagedShutdownBound, RoundTrip: stagedShutdownRoundTrip,
	})
	select {
	case <-s.done:
	case <-time.After(stagedShutdownBound):
		t.Fatalf("ShutdownDaemonUntilGone spent its whole bound (%s) on an abandoned lock", stagedShutdownBound)
	}
	require.True(t, s.out.Gone, "outcome %+v", s.out)
	require.Equal(t, 1, s.attempts(), "an abandoned lock is settled at the check after the first attempt")
}
