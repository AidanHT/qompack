package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// qompackFaultEnvKey is faultinject_test.go's own copy of the fault-injection variable name.
// internal/cli/fault.go is the ONE non-test file allowed to carry this literal; every test file
// that needs it (this one included) is explicitly exempted from that grep.
const qompackFaultEnvKey = "QOMPACK_FAULT"

// The eleven fault sites of task-6-spec.md's table, spelled out here rather than imported: cli's
// own copies (internal/cli/faultsites.go) are unexported, and re-typing eleven short strings once,
// in the file that has to enumerate exactly that table, is cheaper than exporting a seam whose only
// consumer would be this test.
//
// "spool-full:0" — not the bare site name — because the site's own default allowance is 1 (per
// the spec's literal wording, "faultArg (default 1)"), and a single hook process makes at most one
// spool.Append call: with the default, that one call always SUCCEEDS, so the site does nothing
// observable in a one-shot combo (fix round 1, Minor M-5). Passing an explicit 0 makes the very
// first Append fail, which is what this matrix actually needs to exercise the site at all.
var e2eFaultSites = []string{
	"stdin-eof", "stdin-garbage", "oversize", "daemon-down", "spool-readonly",
	"spool-full:0", "disk-full", "state-corrupt", "config-corrupt", "panic:hook", "panic:client",
}

// e2eIdleExitFast keeps every daemon a fault-matrix sub-test's own lazy spawn might start
// short-lived: with QOMPACK_FAULT unset for anything other than daemon-down, a real daemon still
// comes up in the background exactly as production does, and without this it would sit around for
// config.Defaults()'s 1800s idle-exit window as an orphaned process for the rest of the test run.
var e2eIdleExitFastEnv = map[string]string{idleExitSecondsEnvKey: "1"}

// TestHooksExitZeroUnderFaults is task-6-spec.md's 66-combination table: every one of the six hook
// subcommands, under every one of the eleven fault sites, must exit 0 and write valid (possibly
// empty) JSON to stdout. Each combination gets its own fresh, isolated project — several sites
// mutate on-disk state (state-corrupt, config-corrupt, spool-readonly) or a process-wide count
// (spool-full), and sharing a project across combinations would let one fault site's aftermath leak
// into the next.
func TestHooksExitZeroUnderFaults(t *testing.T) {
	bin := Build(t)

	var total int
	for _, hook := range hookSubcommands {
		for _, site := range e2eFaultSites {
			total++
			name := hook.event + "/" + site
			t.Run(name, func(t *testing.T) {
				dir := e2eFaultProject(t)
				payload := payloadFor(t, hook.event, dir)

				env := map[string]string{
					"QOMPACK_PROJECT_ROOT": dir,
					"HOME":                 t.TempDir(),
					qompackFaultEnvKey:     site,
				}
				for k, v := range e2eIdleExitFastEnv {
					env[k] = v
				}

				stdout, stderr, code := Run(t, bin, hook.argv, payload, env)
				require.Equal(t, 0, code,
					"hook %q under fault %q must exit 0 (§2.3)\nstdout:\n%s\nstderr:\n%s",
					hook.event, site, stdout, stderr)

				var out hookio.Output
				require.NoError(t, json.Unmarshal(bytes.TrimSpace(stdout), &out),
					"hook %q under fault %q must write valid JSON, got:\n%s", hook.event, site, stdout)

				// session-start's own preSend seam (internal/cli/sessionstart.go) WAITS for a
				// daemon to become reachable before this call ever returns, and that daemon's one
				// live session never gets a matching flush in this test — so it would never
				// idle-exit on its own and would still hold its own log file open (a Windows
				// delete-blocker) when this subtest's t.TempDir() cleanup runs moments later.
				// Shutting it down explicitly, for every combination (a fast no-op wherever no
				// daemon ever came up), makes that race impossible rather than merely unlikely.
				e2eShutdownIfReachable(t, dir)
			})
		}
	}
	t.Logf("TestHooksExitZeroUnderFaults: %d hooks x %d sites = %d combinations run", len(hookSubcommands), len(e2eFaultSites), total)
}

// e2eFaultProject returns a fresh project root for one fault-matrix combination: a plain temp
// directory (no testutil.Project machinery — a fault-matrix run needs nothing beyond a real,
// existing, isolated root) with a defensive permission-reset registered before t.TempDir()'s own
// cleanup, so the spool-readonly site's deliberately non-writable spool directory can never make
// this test's own cleanup fail.
func e2eFaultProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { resetPermissionsForCleanup(dir) })
	return dir
}

// resetPermissionsForCleanup best-effort undoes anything a fault site's spool-readonly injection
// (internal/cli/fault_lockdir_unix.go / fault_lockdir_windows.go) did to dir, so t.TempDir()'s own
// RemoveAll is never blocked by a permission this test itself provoked.
func resetPermissionsForCleanup(dir string) {
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	if runtime.GOOS == "windows" {
		_ = exec.Command("icacls", dir, "/reset", "/t", "/c").Run() //nolint:gosec // G204: fixed subcommand, dir is this test's own temp directory
	}
}

// e2eLazySpawnSettleBound and e2eLazySpawnSettleTick bound how long e2eShutdownIfReachable waits
// for a fire-and-forget lazy spawn to actually come up before concluding none is coming: a
// non-session-start hook's own client (internal/ipc/client.go's lazySpawn) never waits for the
// daemon it starts, so "not reachable yet" and "never coming up at all" are indistinguishable at
// the instant Run returns — only a short poll tells them apart.
//
// Basis: daemon.SpawnPollBound is the same question asked from the other side — the window
// EnsureRunning polls a spawn it made before declaring it never arrived. Waiting exactly that long
// (V2-MERGE-25 ②; it was a copied 1500ms literal) means this helper concludes "none is coming" at
// precisely the moment the spawning side would have, and moves with it if that window ever changes.
const (
	e2eLazySpawnSettleBound = daemon.SpawnPollBound
	e2eLazySpawnSettleTick  = 25 * time.Millisecond
)

// e2eShutdownIfReachable dials root's resolved address — polling briefly first, since a
// fire-and-forget lazy spawn may still be in flight — and, only if something answers, sends
// admin.shutdown and waits for it to go away. It is a fast no-op whenever no daemon ever comes up
// at all (every daemon-down row, and most panic:hook rows, which fault before any spawn attempt).
//
// "Gone" means no process can still write inside the tree, and the helper has exactly one
// definition of it: no live process holds root's daemon.lock, AND every process this call saw
// holding it has exited — on Windows, its process object is signaled, which the kernel does only
// after it has closed the process's handles (testutil.ProcessAlive). An unreachable address is not
// that: ipc.Probe stops answering at Stop's FIRST act, the listener closing, while the process goes
// on to drain, flush and release, every step of which writes under .qompack/. Nor is the lock's
// absence on its own: Lock.Release is Stop's LAST act, and the process still unwinds after it (the
// loop below quotes the CI failure that proved it). Callers use this from t.Cleanup, immediately
// before t.TempDir's RemoveAll, so a helper that returns any earlier hands the directory to
// RemoveAll with a live writer still in it. A lock seen held whose pid no check could read, and
// that was gone by the next check, names a process nobody can ask about; the helper then waits out
// its whole bound rather than count the tree settled (see e2eLockHolders).
//
// "Anything to shut down" is reachability OR a lock held by a LIVE process, and the second half
// is the third state this helper used to miss entirely. A daemon takes the lock and opens its day
// log well before it listens (internal/daemon/daemon.go: AcquireLock at :377, server.Serve at
// :462), so a spawn that is merely slow is a running process, holding
// <root>/.qompack/logs/qompack-YYYYMMDD.log open, that answers no dial at all —
// e2eDaemonHoldingLock is what sees it. e785891's rule that an abandoned lock must not be waited
// on survives untouched, because an abandoned lock names a dead pid.
//
// Watched with os.Stat rather than daemon.ReadLock, for the reason v1StopDaemonAndWaitGone
// documents at length: ReadLock used to open the file without FILE_SHARE_DELETE, so a poller
// holding it open made the daemon's own os.Remove fail on Windows and CAUSED the abandoned lock it
// was waiting on. readLockFile reads through paths.ReadFileShared now, but os.Stat stays: a
// presence question needs no handle at all. The one read of the lock's CONTENTS goes through
// paths.ReadFileShared, which takes a handle that cannot block a delete (see e2eDaemonHoldingLock).
func e2eShutdownIfReachable(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	lockPath := daemon.LockPath(root)

	// Reachability, and not the lock, still decides whether there is anything to shut down. A
	// fault row that kills a daemon outright can leave the lock behind with nothing listening, and
	// keying the early-out on the lock would make every such row spin out the full bound below
	// waiting for a file no live process will ever remove. The lock's job starts after a daemon
	// has answered: it is what "gone" means, not what "present" means.
	reachable := ipc.Probe(addr, e2eProbeTimeout)
	if !reachable {
		ticker := time.NewTicker(e2eLazySpawnSettleTick)
		deadline := time.NewTimer(e2eLazySpawnSettleBound)
		defer ticker.Stop()
		defer deadline.Stop()
	settle:
		for {
			select {
			case <-ticker.C:
				if ipc.Probe(addr, e2eProbeTimeout) {
					reachable = true
					break settle
				}
			case <-deadline.C:
				break settle
			}
		}
	}
	if !reachable {
		// The third case, and the one that returned too early. "Not reachable after the settle
		// poll" is two different worlds: nothing was ever spawned, and a daemon that IS running
		// but has not listened yet. daemon.Run takes the lock and opens its day log through
		// paths.AppendOnly long before server.Serve (internal/daemon/daemon.go:469 vs :568), so a
		// spawn that merely lost a race with e2eLazySpawnSettleBound — 66 subtests deep into a
		// -count=2 run on a loaded runner — is a live process with an open handle on
		// <root>/.qompack/logs/qompack-YYYYMMDD.log and nothing on the pipe.
		//
		// Returning there handed that process's own directory to the caller's t.TempDir RemoveAll,
		// which is CI run 32380977010's single failure out of 66 combinations:
		//
		//	--- FAIL: TestHooksExitZeroUnderFaults/PostToolUse/disk-full
		//	    TempDir RemoveAll cleanup: unlinkat ...\.qompack\logs\qompack-20260820.log:
		//	    The process cannot access the file because it is being used by another process.
		//
		// paths.AppendOnly opens the day log through OpenFile — no FILE_SHARE_DELETE — so the
		// daemon's own handle is what blocks the unlink, and the hook process cannot be the holder:
		// Run (harness.go) uses cmd.Run, so it has already exited and Windows has closed its
		// handles by the time this cleanup runs, and daemon.buildSpawnEnv strips QOMPACK_FAULT
		// from the child's environment (internal/daemon/spawn.go:155), so the daemon that outlives
		// it is an ordinary one that was simply still coming up.
		//
		// Process liveness decides, not the lock's presence — that distinction is exactly
		// e785891's constraint, kept rather than reversed. A fault row that kills a daemon outright
		// leaves the lock behind with nothing to remove it; keying the early-out on the lock's mere
		// existence would make every such row spin out the full e2eDaemonDownBound below waiting
		// for a file no live process will ever touch. An abandoned lock names a dead pid, so it
		// still returns here as immediately as it did before. Only a lock whose pid is still
		// running falls through, and only that case ever had anything to wait for.
		//
		// The fourth case is a daemon that does not hold the lock YET. A hook's lazySpawn launches
		// a detached daemon and exits without waiting for it, and on a loaded host that process can
		// take seconds just to reach its first statement: TestHooksExitZeroUnderFaults' "TempDir
		// RemoveAll cleanup: … The directory is not empty" was this helper returning here, and the
		// daemon then taking its lock — recreating .qompack/run — inside a tree RemoveAll was
		// deleting (measured under co-load: a lock taken 4.2 s after this return, with a fresh
		// run/spawn.lock present throughout; w2-hookout runs/diag-faultrows-coload-windows.log).
		// run/spawn.lock is the product's own "a spawn is in flight" marker, so while a fresh one
		// exists this waits for the daemon it names to take the lock, and only then proceeds to
		// shut it down. A spawn that never arrives lets the marker go stale, and the wait ends.
		if _, held := e2eDaemonHoldingLock(root); !held && !e2eAwaitSpawnInFlight(root) {
			return
		}
	}

	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: e2eRoundTripDeadline,
		AckDeadline:     e2eRoundTripDeadline,
	})
	defer func() { _ = c.Close() }()

	// Every pid seen holding the lock during this call, starting with the handshake. Lock.Release is
	// Stop's LAST act, but the process still has to unwind after Stop returns — flush its
	// observability sinks, close the day log, run its deferred closers, exit — and every one of
	// those can write inside <root>/.qompack. Returning the moment the lock file disappears therefore
	// hands a still-writing process's directory to the caller's t.TempDir RemoveAll, which is CI run
	// 32932419445's single failure:
	//
	//	--- FAIL: TestFaultSitesInertWhenUnset
	//	    TempDir RemoveAll cleanup: unlinkat /tmp/…/001/.qompack: directory not empty
	//
	// ENOTEMPTY, not EBUSY, and that distinction is the whole diagnosis: on Linux an open handle
	// never blocks an unlink, so nothing was holding the tree open — an entry was CREATED inside
	// .qompack between RemoveAll emptying it and RemoveAll unlinking it, which only a live process
	// can do. (The Windows failure quoted above is the same race seen through a different errno:
	// there the straggler's open handle is what surfaces, here its next write is.) So process
	// death, not lock absence, is the condition that makes the tree safe to remove —
	// e2eProcessAlive's own doc comment already says exactly that.
	//
	// It is a SET, observed on every check, and not one read taken as the handshake starts. That
	// one read is what a daemon's own start defeats: paths.CreateNew creates daemon.lock and only
	// then writes its body, e2eDaemonHoldingLock counts the empty file as held with no pid, and
	// e2eAwaitSpawnInFlight returns at the first sighting of a held lock — which can be exactly that
	// empty file. A pid read then was 0, for which "has it exited?" is always yes, so the lock's
	// disappearance alone ended the wait while the daemon was still unwinding
	// (TestE2EShutdownIfReachable_WaitsForEveryLockHolderToExit stages it). Collecting every pid
	// the lock names, on every check, costs nothing when there is one daemon and still covers a
	// lock that changed hands mid-call.
	//
	// A check runs immediately BEFORE each admin.shutdown as well as after it. A daemon that can
	// receive a Send is listening, and it wrote its lock body before it listened, so the check just
	// before the Send reads that body; the check after it may come too late, because Stop runs on a
	// goroutine of its own and can release the lock before the reply is written
	// (handleAdminShutdown). What neither check can catch — a lock that was empty at one and gone
	// at the next — e2eLockHolders records as unidentified, and the helper never counts that as
	// settled (TestE2EShutdownIfReachable_WaitsOutALockHolderItNeverIdentified stages it).
	holders := newE2ELockHolders()

	// Client.Send never propagates an error — a failed connect/write/ACK round trip just spools
	// the request instead and returns silently (00-ARCHITECTURE.md §2.4/§12.3), so a single
	// admin.shutdown attempt has no way to know whether the daemon actually received it. Retrying
	// on a ticker until the daemon actually goes away (or the overall bound elapses) is the only
	// way this helper can tell "delivered" from "silently spooled" — a ticker, not time.Sleep,
	// per §6.1's wall-clock-sleep ban (devtool lint's sleepcheck sub-check).
	ticker := time.NewTicker(e2eDaemonDownTick)
	defer ticker.Stop()
	timeout := time.NewTimer(e2eDaemonDownBound)
	defer timeout.Stop()
	for {
		holders.observe(e2eDaemonHoldingLock(root))
		_, _ = c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminShutdown, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, e2eRoundTripDeadline)
		// Two things must both be true before this tree is nobody's to write to: no live process
		// holds the lock now, and every process seen holding it has actually exited (with none
		// having held it under a pid this call could not read).
		//
		// The first half also covers a daemon that stops mattering by dying. Lock.Release is
		// Stop's last act, so a process that never reaches it — an injected fault, a crash, a
		// kill — leaves the lock behind for nobody to remove, and waiting on the FILE would cost
		// exactly what e785891 refused to pay at the gate above: the whole of e2eDaemonDownBound
		// spent proving a fact already on disk. e2eDaemonHoldingLock answers "no lock file" and
		// "lock file naming a dead pid" identically, which is why the absent-file check that used
		// to stand here is not merely moved but subsumed.
		lockPID, held := e2eDaemonHoldingLock(root)
		holders.observe(lockPID, held)
		if !held && holders.settled() {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			switch {
			case e2eFileExists(lockPath):
				t.Logf("e2eShutdownIfReachable: a live daemon (pid %d) still held %s after %s of retried "+
					"admin.shutdown; the caller's t.TempDir cleanup is about to remove a tree it may still "+
					"be writing to", lockPID, lockPath, e2eDaemonDownBound)
			case len(holders.running()) > 0:
				t.Logf("e2eShutdownIfReachable: daemon pids %v released %s but were still running after "+
					"%s; the caller's t.TempDir cleanup is about to remove a tree it may still be writing to",
					holders.running(), lockPath, e2eDaemonDownBound)
			case holders.unidentified:
				t.Logf("e2eShutdownIfReachable: a process held %s with no readable pid and released it "+
					"before any check could read one, so whether it has exited cannot be asked; waited "+
					"out %s instead", lockPath, e2eDaemonDownBound)
			}
			return
		}
	}
}

// e2eLockHolders is what e2eShutdownIfReachable has seen of a project's lock during one call: the
// pids it has read holding it, and whether some holder went unread.
type e2eLockHolders struct {
	pids map[int]struct{}
	// unread: the latest check found the lock held with no readable pid (a body not yet written,
	// per e2eDaemonHoldingLock), and no check since has read one.
	unread bool
	// unidentified: a lock last seen unread was gone at a later check. Some process held it and
	// released it, and nothing names that process, so no check can ask whether it has exited.
	unidentified bool
}

func newE2ELockHolders() *e2eLockHolders {
	return &e2eLockHolders{pids: map[int]struct{}{}}
}

// observe records one check's answer from e2eDaemonHoldingLock. A held lock with a readable pid adds
// that pid to the set. A held lock with none adds nothing yet: the caller still counts it as held,
// so the wait goes on, and the next check reads the body once it lands. A body naming a process that
// is no longer alive identifies its holder too, and leaves nothing to wait for. Only a lock that was
// unread and is then gone (no file, or one that can no longer be read) leaves a holder nobody can
// name.
func (h *e2eLockHolders) observe(pid int, held bool) {
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
// every recorded holder has exited, and there was no holder the call could not identify.
func (h *e2eLockHolders) settled() bool {
	return !h.unread && !h.unidentified && len(h.running()) == 0
}

// running lists the recorded holders that can still write inside the tree, in ascending order.
func (h *e2eLockHolders) running() []int {
	var out []int
	for pid := range h.pids {
		if !e2eShutdownProcessSettled(pid) {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out
}

// e2eShutdownProcessSettled reports whether the pid that held the lock can no longer write inside
// the tree.
//
// For a daemon in its OWN process that is exactly "the process has exited", and the long comment
// above says why nothing weaker will do: Lock.Release is Stop's last act, but the process still has
// to unwind afterwards, and CI run 32932419445 caught it creating an entry inside .qompack between
// RemoveAll emptying the directory and RemoveAll unlinking it. "Exited" is e2eProcessAlive's, which
// on Windows waits for the process object to be signaled: an exit code alone is set while the
// kernel is still closing the process's handles (testutil.ProcessAlive).
//
// For a daemon running IN THIS TEST BINARY — v4StartRig and the other in-process rigs — that
// question is not merely unanswerable but meaningless. internal/daemon/lock.go:103 records the
// holder's pid, which for an in-process rig is the test binary's own, so "has it exited?" asks
// whether the process doing the asking has exited: always false, for the whole of
// e2eDaemonDownBound, after which the caller logged a diagnostic claiming a straggler was still
// writing to a tree that had in fact been quiescent for twenty seconds. A false diagnostic is worse
// than a slow one, because it is the first thing the next person to debug these rows will chase.
//
// The unwind the out-of-process case waits for has no counterpart here: there is no second process
// to unwind, and the test binary will not exit until the test does. Stop has returned and the lock
// is gone, which is every guarantee available and the same one v4StartRig's own t.Cleanup relies
// on. So for our own pid the lock's absence is the whole condition.
//
// This is not a relaxation of the out-of-process check, which is unchanged: it replaces a condition
// that could never be satisfied with the one that actually establishes the fact.
func e2eShutdownProcessSettled(shutdownPID int) bool {
	if shutdownPID == os.Getpid() {
		return true
	}
	return !e2eProcessAlive(shutdownPID)
}

// e2eDaemonHoldingLock reports the pid recorded in root's daemon.lock and whether a live process
// still holds it. It is the signal that tells a daemon which is STILL COMING UP — lock taken, day
// log open, nothing listening — apart from a lock abandoned by a process that is already gone;
// neither ipc.Probe nor os.Stat can see the difference, and e2eShutdownIfReachable has to.
//
// The lock is read with paths.ReadFileShared directly rather than with daemon.ReadLock. When this
// helper was written the two were not interchangeable: ReadLock was os.ReadFile
// (internal/daemon/lock.go's readLockFile), which on Windows takes a handle with
// FILE_SHARE_READ|FILE_SHARE_WRITE and no FILE_SHARE_DELETE, so a caller polling it made
// Lock.Release's own os.Remove fail with ERROR_SHARING_VIOLATION — it would CAUSE the abandoned
// lock it was checking for, the failure v1StopDaemonAndWaitGone measured at roughly one run in
// twenty. readLockFile reads through paths.ReadFileShared now, so ReadLock is safe to poll and the
// difference is down to what this helper needs from the bytes rather than to the share mask.
// paths.OpenShared adds FILE_SHARE_DELETE to that mask (bbd8905, "stop readers blocking the writer
// they watch"), which is what makes reading this file at all safe — here and in ReadLock alike.
//
// A lock file that exists but does not parse counts as held. paths.CreateNew creates the file and
// only then writes the body into it (internal/paths/appendonly.go), so an empty or truncated
// daemon.lock is one that a process finished creating microseconds ago — the most alive a daemon
// ever is, not a dead one. Both callers re-ask on a tick, so that conservative answer costs a
// tick and never a bound.
func e2eDaemonHoldingLock(root string) (pid int, held bool) {
	b, err := paths.ReadFileShared(daemon.LockPath(root))
	if err != nil {
		// Overwhelmingly this is "no lock file", i.e. no daemon ever took this project — every
		// daemon-down row, and most panic:hook rows. Any other read error lands here too, and
		// answering "not held" for it is deliberate: it leaves this helper's pre-fix behaviour
		// exactly as it was for a state it cannot see into, rather than spending the whole of
		// e2eDaemonDownBound on a guess.
		return 0, false
	}
	var info daemon.LockInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return 0, true // mid-CreateNew, per the paragraph above.
	}
	return info.PID, e2eProcessAlive(info.PID)
}

// e2eSpawnLockName is internal/ipc's unexported spawnLockName, respelled here as internal/daemon
// respells it (runSpawnLockFileName): the file a hook's lazySpawn writes in <root>/.qompack/run,
// carrying its own UnixMilli timestamp, before it launches a detached daemon. The daemon removes it
// once it listens.
const e2eSpawnLockName = "spawn.lock"

// e2eSpawnLockStaleAfter is internal/ipc's unexported spawnLockStaleAfter: how old a spawn.lock has
// to be before a client stops treating the spawn it names as in flight and spawns again. It is the
// product's own answer to "is a daemon still coming?", so this helper uses exactly it.
const e2eSpawnLockStaleAfter = 10 * time.Second

// e2eSpawnInFlight reports whether root holds a spawn.lock younger than e2eSpawnLockStaleAfter — a
// detached daemon launch the product itself still counts as underway. It reads through
// paths.ReadFileShared for the reason e2eDaemonHoldingLock does: the daemon deletes this file
// itself, and a reader without FILE_SHARE_DELETE would make that delete fail on Windows. A file that
// does not parse is stale, as ipc's own spawnLockIsStale reads it.
func e2eSpawnInFlight(root string) bool {
	b, err := paths.ReadFileShared(filepath.Join(paths.Of(root).Run, e2eSpawnLockName))
	if err != nil {
		return false
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return false
	}
	return time.Since(time.UnixMilli(ms)) < e2eSpawnLockStaleAfter
}

// e2eAwaitSpawnInFlight waits, while a spawn is in flight (e2eSpawnInFlight), for the daemon it
// launched to take root's lock, and reports whether one did. It gives up when the marker is gone or
// stale — the product's own point for "that spawn is not coming" — or after e2eDaemonUpBound, this
// file's bound for a lazily spawned daemon to come up on a loaded host.
func e2eAwaitSpawnInFlight(root string) bool {
	ticker := time.NewTicker(e2eLazySpawnSettleTick)
	defer ticker.Stop()
	deadline := time.NewTimer(e2eDaemonUpBound)
	defer deadline.Stop()
	for {
		if _, held := e2eDaemonHoldingLock(root); held {
			return true
		}
		if !e2eSpawnInFlight(root) {
			// One last look: the daemon removes spawn.lock only once it listens, by which time it
			// holds the lock, so a marker that vanished may mean the daemon just arrived.
			_, held := e2eDaemonHoldingLock(root)
			return held
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			_, held := e2eDaemonHoldingLock(root)
			return held
		}
	}
}

// e2eFileExists reports whether p is present, without opening it — see e2eShutdownIfReachable on
// why a handle would be self-defeating here.
func e2eFileExists(p string) bool {
	_, err := os.Stat(paths.Long(p))
	return err == nil
}

// buildNoInjectOnce guards the single -tags noinject build TestFaultSitesInertWhenUnset performs.
var (
	buildNoInjectOnce sync.Once
	builtNoInjectBin  string
	buildNoInjectDir  string
	buildNoInjectErr  error
)

// buildNoInject compiles ./cmd/qompack with -tags noinject into its own temp directory, so
// TestFaultSitesInertWhenUnset can compare its output against the default build's byte for byte.
// The directory is created with os.MkdirTemp rather than t.TempDir and is removed by
// removeNoInjectBuild from TestMain, not by a t.Cleanup, for the same reason as doBuild's.
func buildNoInject(t *testing.T) string {
	t.Helper()
	buildNoInjectOnce.Do(func() {
		root, err := moduleRoot()
		if err != nil {
			buildNoInjectErr = err
			return
		}
		buildNoInjectDir, buildNoInjectErr = os.MkdirTemp("", "qompack-e2e-noinject-")
		if buildNoInjectErr != nil {
			return
		}
		out := filepath.Join(buildNoInjectDir, "qompack")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-tags", "noinject", "-o", out, "./cmd/qompack")
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			buildNoInjectErr = err
			t.Logf("go build -tags noinject: %v\n%s", err, stderr.String())
			return
		}
		builtNoInjectBin = out
	})
	if buildNoInjectErr != nil {
		t.Fatalf("e2e: building the -tags noinject ./cmd/qompack: %v", buildNoInjectErr)
	}
	return builtNoInjectBin
}

// removeNoInjectBuild deletes the directory buildNoInject created. TestMain calls it after the
// last test, mirroring harness.go's Build/removeBuild pair: the once-cached binary has to outlive
// the test that triggered the build — under `go test -count=2` one process runs every test twice
// against a single fired sync.Once, and the per-test t.Cleanup that used to live here handed the
// second execution a cached path whose directory was already gone (found at V2-VERIFY, gate
// V2-ALL-02).
func removeNoInjectBuild() {
	if buildNoInjectDir != "" {
		_ = os.RemoveAll(buildNoInjectDir)
	}
}

// TestFaultSitesInertWhenUnset is task-6-spec.md's e2e table row: with QOMPACK_FAULT unset, a hook
// run against the default build must be byte-identical to the same run against a -tags noinject
// build — the compile-time guarantee that fault injection adds no observable behaviour when it is
// not asked for.
//
// This is the second of the row's own two halves (fix round 1, Minor M-9): "faultActive false for
// all eleven sites with the variable unset" is pinned at the unit level, in
// internal/cli/fault_test.go's TestFaultActive_UnsetIsInertForAllElevenSites — the e2e package
// cannot see that unexported function to assert it directly. This test covers the other half, the
// one only a real build comparison can prove.
func TestFaultSitesInertWhenUnset(t *testing.T) {
	bin := Build(t)
	noinjectBin := buildNoInject(t)

	dir := t.TempDir()
	t.Cleanup(func() { e2eShutdownIfReachable(t, dir) })
	env := map[string]string{
		"QOMPACK_PROJECT_ROOT": dir, "HOME": t.TempDir(),
		// Explicitly cleared, not merely omitted (fix round 1, Minor M-9): Run's own harness
		// inherits os.Environ(), so a developer running this locally with QOMPACK_FAULT already
		// set in their shell would otherwise get a silently meaningless pass.
		qompackFaultEnvKey: "",
	}
	payload := payloadFor(t, "PostToolUse", dir)

	stdoutDefault, _, codeDefault := Run(t, bin, []string{"observe", "tool"}, payload, env)
	require.Equal(t, 0, codeDefault)

	stdoutNoinject, _, codeNoinject := Run(t, noinjectBin, []string{"observe", "tool"}, payload, env)
	require.Equal(t, 0, codeNoinject)

	require.Equal(t, string(stdoutDefault), string(stdoutNoinject),
		"a hook's stdout with QOMPACK_FAULT unset must be byte-identical between the default build and a -tags noinject build")
}

// TestSelfTestIsTheOnlyNonZeroExit is task-6-spec.md's e2e table row: under the daemon-down fault,
// every subcommand SP-05 ships except self-test still exits 0.
//
// The project is pre-degraded first (three consecutive marker-less SessionStarts — see
// TestE2ESelfTestExitsNonZeroOnCritical's own doc comment for why three, not two), BEFORE
// daemon-down is applied to the cases below: a merely daemon-unreachable but otherwise HEALTHY
// project would make self-test itself report only a SevWarn ("daemon reachable or spawnable"
// failed) and still exit 0 — proving nothing about the exit-code policy. Degrading the project for
// real first is what makes "self-test is the only one that may exit non-zero" a meaningful claim
// rather than a vacuous one, since the other ten cases below still have to stay at 0 despite a
// genuine, non-recoverable-by-this-fault critical failure sitting in the project's own history.
//
// Scoped to the subcommands this subplan actually implements — the six hooks (already exit-0 by
// construction), daemon, self-test, version, config print and config schema — rather than the
// whole dispatch table: the not-yet-implemented placeholders (`status`, `recall`, `mcp`, ...)
// report core.ErrNotImplemented and a non-zero exit by design, independent of any fault, and
// asserting otherwise would just be testing another subplan's unfinished surface.
func TestSelfTestIsTheOnlyNonZeroExit(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	baseEnv := e2eEnv(p)

	for i, sess := range []string{"sess-e2e-only-nonzero-1", "sess-e2e-only-nonzero-2", "sess-e2e-only-nonzero-3"} {
		payload, err := json.Marshal(struct {
			HookEventName string `json:"hook_event_name"`
			SessionID     string `json:"session_id"`
			CWD           string `json:"cwd"`
			Source        string `json:"source"`
		}{"SessionStart", sess, p.Root, "startup"})
		require.NoError(t, err)

		_, stderr, code := Run(t, bin, []string{"session-start"}, payload, baseEnv)
		require.Equal(t, 0, code, "degrading session-start #%d: stderr:\n%s", i, stderr)
		if i == 0 {
			e2eWaitDaemonUp(t, p.Root)
		}
	}
	require.Eventually(t, func() bool {
		h := contract.LoadHistory(contract.HistoryPath(p.Root))
		return h.StartsWithoutMarker >= 2
	}, e2eHistoryConvergeBound, e2eDaemonUpTick, "the project never actually degraded")

	env := e2eEnv(p)
	env[qompackFaultEnvKey] = "daemon-down"
	for k, v := range e2eIdleExitFastEnv {
		env[k] = v
	}

	cases := []struct {
		argv []string
		want int
	}{
		{[]string{"observe", "tool"}, 0},
		{[]string{"observe", "prompt"}, 0},
		{[]string{"observe", "stop"}, 0},
		{[]string{"session-start"}, 0},
		{[]string{"checkpoint"}, 0},
		{[]string{"flush"}, 0},
		{[]string{"version"}, 0},
		{[]string{"config", "print", "--json"}, 0},
		{[]string{"config", "schema"}, 0},
		{[]string{"self-test", "--json"}, 1}, // the one command §2.3 permits a non-zero exit — and, with the project genuinely degraded above, it actually does.
	}

	for _, tc := range cases {
		t.Run(argvName(tc.argv), func(t *testing.T) {
			payload := payloadFor(t, "PostToolUse", p.Root)
			_, stderr, code := Run(t, bin, tc.argv, payload, env)
			require.Equal(t, tc.want, code, "argv=%v stderr:\n%s", tc.argv, stderr)
		})
	}
}

// argvName renders argv as a t.Run-safe subtest name.
func argvName(argv []string) string {
	name := ""
	for i, a := range argv {
		if i > 0 {
			name += "_"
		}
		name += a
	}
	return name
}
