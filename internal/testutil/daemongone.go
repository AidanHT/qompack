package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// This file is the one definition of "the daemon is gone" that every daemon-shutdown helper under
// test/ uses: test/e2e's e2eShutdownIfReachable, the shutdownIfReachable helpers of test/fault,
// test/platform, test/release and test/security, and test/guards' v1StopDaemonAndWaitGone. Each of
// them used to carry its own copy of the loop below. The copies drifted: test/e2e's learned, one
// CI failure at a time, to collect every pid the lock names and to never count an unreadable lock
// as settled (w4-e2eflakes f6d9a71, f84e0a2), while the other five kept one read of the lock
// taken as the handshake began, and counted pid 0 — an empty daemon.lock caught mid-create — as a
// holder that had already exited. Each helper still decides for itself whether there is anything to
// shut down and what to do if the daemon never goes; what "gone" means is decided only here.

// ShutdownWait is how ShutdownDaemonUntilGone paces itself. Each caller passes its own numbers,
// derived where it declares them; this file adds none.
type ShutdownWait struct {
	// Tick is the interval between admin.shutdown attempts, and so between lock checks.
	Tick time.Duration
	// Bound is how long to go on asking before giving up. It has to outlast
	// daemon.StopCleanupBound, the daemon's own worst case for going away once asked, or a timeout
	// cannot tell a wedged daemon from one still finishing.
	Bound time.Duration
	// RoundTrip is each attempt's connect and ACK deadline.
	RoundTrip time.Duration
}

// ShutdownOutcome is how one ShutdownDaemonUntilGone call ended.
type ShutdownOutcome struct {
	// Gone reports that the definition held at the last check: no live process holds daemon.lock,
	// every process seen holding it during the call has exited, and no holder went unidentified.
	Gone bool
	// LockPID and LockHeld are the last check's reading of daemon.lock (DaemonHoldingLock).
	LockPID  int
	LockHeld bool
	// Running lists, in ascending order, the pids seen holding the lock during the call that were
	// still running when it ended.
	Running []int
	// Unidentified reports that some process held the lock with no pid any check could read, and
	// released it before one could: nobody can ask whether that process has exited.
	Unidentified bool
}

// Describe says, for a call that ended without Gone, what was still in the way, naming lockPath
// and the bound the caller waited. Callers prefix it with their own name.
func (o ShutdownOutcome) Describe(lockPath string, bound time.Duration) string {
	switch {
	case o.Gone:
		return fmt.Sprintf("every process seen holding %s has exited", lockPath)
	case o.LockHeld && o.LockPID > 0:
		return fmt.Sprintf("a live daemon (pid %d) still held %s after %s of retried admin.shutdown",
			o.LockPID, lockPath, bound)
	case o.LockHeld:
		return fmt.Sprintf("%s was still present with no readable pid after %s of retried admin.shutdown",
			lockPath, bound)
	case len(o.Running) > 0:
		return fmt.Sprintf("daemon pids %v released %s but were still running after %s", o.Running, lockPath, bound)
	default:
		return fmt.Sprintf("a process held %s with no readable pid and released it before any check could "+
			"read one, so whether it has exited cannot be asked; waited out %s instead", lockPath, bound)
	}
}

// ShutdownDaemonUntilGone sends admin.shutdown to addr on every w.Tick until the daemon serving root
// is gone, or w.Bound elapses, and reports which.
//
// "Gone" means no process can still write inside the tree, and it has exactly one definition: no
// live process holds root's daemon.lock, AND every process seen holding it during this call has
// exited — on Windows, its process object is signaled, which the kernel does only after it has
// closed the process's handles (ProcessAlive) — AND no holder went unidentified. Callers use this
// from t.Cleanup, immediately before t.TempDir's RemoveAll, so returning any earlier hands the
// directory to RemoveAll with a live writer still in it.
//
// An unreachable address is not "gone": ipc.Probe stops answering at Stop's FIRST act, the listener
// closing, while the process goes on to drain, flush and release, every step of which writes under
// .qompack/. The lock's absence on its own is not "gone" either: Lock.Release is Stop's LAST act,
// and the process still unwinds after it — flushing its sinks, closing the day log, running its
// deferred closers. CI run 32932419445 caught exactly that, twice: test/e2e's
// TestFaultSitesInertWhenUnset failed "TempDir RemoveAll cleanup: unlinkat …/.qompack: directory not
// empty" (an entry created inside .qompack between RemoveAll emptying it and unlinking it, which only
// a live process can do), and test/guards' TestV1_WriteSetConfinedAcrossFullHookSequence found a
// half-finished paths.WriteAtomic staging file in .qompack/tmp/. So process death, not lock
// absence, is the condition.
//
// Which processes to wait for is a SET, observed on every check, not one read of the lock taken as
// the handshake starts. paths.CreateNew creates daemon.lock and only then writes its body, so a
// single read can find the empty file and learn pid 0, for which "has it exited?" is always yes —
// the lock's disappearance alone then ends the wait while the daemon is still unwinding
// (TestShutdownDaemonUntilGone_WaitsForEveryLockHolderToExit stages it). A check runs immediately
// BEFORE each admin.shutdown as well as after it. A daemon that can receive a Send is listening, and
// it wrote its lock body before it listened, so the check just before the Send reads that body; the
// check after it may come too late, because Stop runs on a goroutine of its own and can release the
// lock before the reply is written (internal/daemon's handleAdminShutdown). What neither check can
// catch — a lock that was empty at one and gone at the next — is recorded as unidentified, and the
// call then waits out its whole bound rather than count the tree settled
// (TestShutdownDaemonUntilGone_WaitsOutALockHolderItNeverIdentified stages it).
//
// A lock naming a process that has already exited counts as released. A daemon that dies without
// reaching Lock.Release — an injected fault, a crash, a kill — leaves its lock for nobody to remove,
// and waiting on the FILE would spend the whole bound proving a fact already on disk.
//
// Client.Send never propagates an error: a failed connect, write or ACK spools the request and
// returns silently (00-ARCHITECTURE.md §2.4/§12.3), so one attempt cannot know whether the daemon
// received it. Retrying on a ticker until the daemon is gone is the only way to tell "delivered"
// from "silently spooled" — a ticker, not time.Sleep, per §6.1's wall-clock-sleep ban (devtool
// lint's sleepcheck). The attempts that nobody receives land in root's own client spool.
func ShutdownDaemonUntilGone(root string, addr ipc.Addr, w ShutdownWait) ShutdownOutcome {
	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: w.RoundTrip,
		AckDeadline:     w.RoundTrip,
	})
	defer func() { _ = c.Close() }()

	holders := newLockHolders()
	ticker := time.NewTicker(w.Tick)
	defer ticker.Stop()
	timeout := time.NewTimer(w.Bound)
	defer timeout.Stop()
	for {
		holders.observe(DaemonHoldingLock(root))
		_, _ = c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminShutdown, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, w.RoundTrip)
		pid, held := DaemonHoldingLock(root)
		holders.observe(pid, held)
		if !held && holders.settled() {
			return ShutdownOutcome{Gone: true, LockPID: pid}
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			return ShutdownOutcome{
				LockPID: pid, LockHeld: held, Running: holders.running(), Unidentified: holders.unidentified,
			}
		}
	}
}

// DaemonHoldingLock reports the pid recorded in root's daemon.lock and whether a live process still
// holds it. It tells a daemon that is STILL COMING UP — lock taken, day log open, nothing listening
// yet — apart from a lock abandoned by a process that is already gone; neither ipc.Probe nor os.Stat
// can see that difference.
//
// The lock is read through paths.ReadFileShared, whose handle carries FILE_SHARE_DELETE on Windows.
// An ordinary os.ReadFile handle lacks it, so a caller polling the lock that way made
// Lock.Release's own os.Remove fail with ERROR_SHARING_VIOLATION — it CAUSED the abandoned lock it
// was checking for, about one run in twenty (test/guards' v1StopDaemonAndWaitGone measured it).
//
// A lock file that exists but does not parse counts as held, with pid 0. paths.CreateNew creates the
// file and only then writes the body into it, so an empty or truncated daemon.lock belongs to a
// process that finished creating it microseconds ago — the most alive a daemon ever is. Every caller
// asks again on a tick, so that answer costs a tick, never a bound. Any read error, overwhelmingly
// "no lock file", answers not held.
func DaemonHoldingLock(root string) (pid int, held bool) {
	b, err := paths.ReadFileShared(daemon.LockPath(root))
	if err != nil {
		return 0, false
	}
	var info daemon.LockInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return 0, true
	}
	return info.PID, ProcessAlive(info.PID)
}

// lockHolders is what one ShutdownDaemonUntilGone call has seen of a project's lock: the pids it has
// read holding it, and whether some holder went unread.
type lockHolders struct {
	pids map[int]struct{}
	// unread: the latest check found the lock held with no readable pid (a body not yet written,
	// per DaemonHoldingLock), and no check since has read one.
	unread bool
	// unidentified: a lock last seen unread was gone at a later check. Some process held it and
	// released it, and nothing names that process, so no check can ask whether it has exited.
	unidentified bool
}

func newLockHolders() *lockHolders {
	return &lockHolders{pids: map[int]struct{}{}}
}

// observe records one check's answer from DaemonHoldingLock. A held lock with a readable pid adds
// that pid to the set. A held lock with none adds nothing yet: the caller still counts it as held,
// so the wait goes on, and a later check reads the body once it lands. A body naming a process that
// is no longer alive identifies its holder too, and leaves nothing to wait for. Only a lock that
// was unread and is then gone (no file, or one that can no longer be read) leaves a holder nobody
// can name.
func (h *lockHolders) observe(pid int, held bool) {
	switch {
	case held && pid > 0:
		h.pids[pid] = struct{}{}
		h.unread = false
	case held:
		h.unread = true
	case pid > 0:
		h.unread = false
	case h.unread:
		h.unidentified = true
		h.unread = false
	}
}

// settled reports whether nothing this call saw holding the lock can still write inside the tree:
// every recorded holder has exited, and there was no holder the call could not identify. A lock
// seen only as an empty file (pid 0) is never settled.
func (h *lockHolders) settled() bool {
	return !h.unread && !h.unidentified && len(h.running()) == 0
}

// running lists the recorded holders that can still write inside the tree, in ascending order.
func (h *lockHolders) running() []int {
	var out []int
	for pid := range h.pids {
		if !lockHolderExited(pid) {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out
}

// lockHolderExited reports whether a pid that held the lock can no longer write inside the tree.
//
// For a daemon in its OWN process that is exactly "the process has exited" (ProcessAlive), for the
// reason ShutdownDaemonUntilGone gives: the process unwinds after Lock.Release.
//
// For a daemon running IN THE CALLING TEST BINARY — test/e2e's v4StartRig and the other in-process
// rigs — the lock records the test binary's own pid (internal/daemon/lock.go), and "has it exited?"
// asks whether the process doing the asking has exited: always no, for the whole bound, after which
// the caller would report a straggler writing to a tree that had been quiet all along. There is no
// second process to unwind, and the test binary will not exit until the test does; Stop has
// returned and the lock is gone, which is every guarantee available and the one those rigs' own
// t.Cleanup relies on. So for our own pid the lock's absence is the whole condition. This replaces
// a condition that could never be satisfied; the out-of-process check is unchanged.
func lockHolderExited(pid int) bool {
	if pid == os.Getpid() {
		return true
	}
	return !ProcessAlive(pid)
}
