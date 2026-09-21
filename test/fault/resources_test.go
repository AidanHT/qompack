package fault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/paths"
)

// Disk full, permission errors and lock contention (deliverable 5).
//
// The disk-full rows are SIMULATED and every record says so: they drive the `disk-full` and
// `spool-full:0` QOMPACK_FAULT sites, which make a write fail the way a full volume would, and they
// are not a real ENOSPC. Nothing here fills a disk, and a record that claimed otherwise would be
// claiming coverage this host does not give.

// resourceRow is one write-failure row. `names` carries the same obligation boundaryRow.Names does,
// and for the same reason: these rows declared no tokens at all until now, so any new line counted
// as the product reporting the cut — and the only thing `resource_spool_readonly` gained was the
// universal `store: segment closed without a tokens feature` warn. namingEvidence now refuses an
// empty list, so the obligation is enforced rather than remembered.
type resourceRow struct {
	name, boundary, seed, site, owner, known string
	names                                    []string
}

// spoolRefusalNames are the tokens of the LOUD line `internal/ipc` emits when a spool append is
// refused: `ipc: spool append refused — event dropped`, with the site's own error beside it. They
// are shared by the three sites because that one line IS the surface all three reach.
var spoolRefusalNames = []string{"spool append refused", "event dropped"}

// resourceWriteFailures is the write-failure matrix. It is a function rather than a literal inside
// the test so TestFault_LinesNamingIgnoresTheUniversalWarn can audit every row's tokens.
func resourceWriteFailures() []resourceRow {
	rows := resourceRows()
	for _, row := range rows {
		registerNames("resource_"+row.name, row.names)
	}
	return rows
}

// resourceRows is the table itself; resourceWriteFailures wraps it so no row reaches a verdict
// without its tokens being registered for the audit.
func resourceRows() []resourceRow {
	return []resourceRow{
		{
			"disk_full", "every write fails, from the first one",
			"the `disk-full` QOMPACK_FAULT site (internal/cli/fault.go:172-208). SIMULATED: this is " +
				"a forced write failure, not a real ENOSPC — no volume was filled",
			"disk-full", "internal/cli + internal/ipc", "",
			append([]string{"disk-full", "no space left"}, spoolRefusalNames...),
		},
		{
			"spool_full", "the spool accepts nothing at all",
			"the `spool-full:0` QOMPACK_FAULT site, whose allowance of zero makes every Append fail. " +
				"SIMULATED: a forced Append failure, not a real ENOSPC",
			"spool-full:0", "internal/cli + internal/ipc", "",
			append([]string{"spool file at cap", "spool-full"}, spoolRefusalNames...),
		},
		{
			"spool_readonly", "the spool directory is not writable",
			"the `spool-readonly` QOMPACK_FAULT site, which denies write on the spool directory " +
				"(icacls on Windows, chmod elsewhere) from inside the hook process",
			"spool-readonly", "internal/cli + internal/ipc", "",
			append([]string{"spool-readonly", "access is denied", "permission denied"},
				spoolRefusalNames...),
		},
	}
}

// objectsReadonlyNames are the tokens for the read-only-objects row: the daemon's own failure to
// take an observation, and the two spellings a refused write carries on the two platforms.
var objectsReadonlyNames = registerNames("resource_objects_readonly", []string{
	"observetool failed", "objects", "access is denied", "permission denied", "read-only",
})

// TestFault_WriteFailures drives the four resource sites and one real read-only directory, and after
// each faulted session runs a CLEAN recovery session over the same project.
//
// The question each row answers is the same: after the failure, is the store consistent and is the
// recording gap explicit? A product that answered a full disk by losing an index line quietly would
// pass a test that only checked the exit code.
func TestFault_WriteFailures(t *testing.T) {
	b := assembledBundle(t)

	for _, row := range resourceWriteFailures() {
		t.Run(row.name, func(t *testing.T) {
			p := newProject(t, "proj")
			t.Cleanup(func() {
				shutdownIfReachable(t, p.Root)
				requireNoOrphan(t, p.Root)
			})

			rec := newRecord(t, "resource_"+row.name)
			rec.Phase = PhaseResource
			rec.Boundary = row.boundary
			rec.SeedMethod = row.seed

			sess := sessionID("resource-" + row.name)
			seedSession(t, b, p, sess)
			baseline := snapshotDegradation(t, b, p)

			// The daemon is STOPPED before the faulted session, and that is R4-3 rather than
			// tidiness. `wrapFaultSpool` decorates `spool.Append` and nothing else
			// (internal/cli/fault.go:190-206), so with a daemon listening the delivery goes
			// straight down the IPC connection and never touches the spool: round 1's disk-full and
			// spool-full rows recorded a faulted turn that was indexed completely normally
			// (tool_uses 3 -> 6) and called it `recovered`. With no daemon up, the client spools —
			// and that is the write these two sites make fail.
			shutdownIfReachable(t, p.Root)
			before := auditProject(t, p.Root)

			faulted := sessionID("resource-" + row.name + "-f")
			faultedID := toolUseID(row.name, 1)
			env := map[string]string{"QOMPACK_FAULT": row.site + ",daemon-down"}
			runHookWithEnv(t, b.Bin, p, []string{"observe", "tool"},
				readToolPayload(t, p.Root, faulted, faultedID, "src/alpha.ts", seedContent("alpha", 48)), env)
			runHookWithEnv(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, faulted), env)
			shutdownIfReachable(t, p.Root)
			// The spool-readonly site denies write on a directory inside the fixture; the deny is
			// lifted here so the clean recovery session below measures the product rather than a
			// permission this test left behind.
			resetPermissionsForCleanup(p.Root)

			// Did the site bite? The faulted delivery must NOT have reached the index, or the row
			// measured a healthy write and would record a pass for the wrong reason.
			if indexHolds(p.Root, faultedID) {
				skipRecorded(t, rec, "the faulted delivery was indexed anyway, so the `"+row.site+
					"` site did not bite on this host; a row that cannot force the failure it names "+
					"is not evidence that the product survived it")
				return
			}
			rec.Detail = "the faulted delivery " + faultedID + " did not reach the index, so the " +
				row.site + " site bit"

			// The clean recovery session, with its daemon left up for the degradation reading.
			recording := recoverSession(t, b, p, sessionID("resource-"+row.name+"-r"))
			ev := namingEvidence(t, degradationSince(baseline, snapshotDegradation(t, b, p)), row.names)
			landedAfterDrain := indexHolds(p.Root, faultedID)
			rec.Detail += fmt.Sprintf("; after the restart and drain it %s",
				map[bool]string{true: "DID land", false: "still had not landed"}[landedAfterDrain])
			shutdownIfReachable(t, p.Root)

			after := auditProject(t, p.Root)
			rec.DanglingBefore = len(before.Dangling)
			rec.DanglingAfter = len(after.Dangling)

			judgeRecovery(t, rec, before, after, recording, ev, row.owner, row.known)
		})
	}
}

// TestFault_ReadOnlyObjectsDirectory makes `.qompack/objects` genuinely non-writable — a real ACL
// deny on Windows, real mode bits elsewhere — and drives a live session into it.
//
// This is the row the fault sites cannot give: `spool-readonly` denies the SPOOL, and a deny on
// objects/ is what a managed install or a mounted read-only volume actually looks like. Task 2 and
// Task 3 established both halves of the mechanism (windowsDenyMask keeps the directory readable and
// traversable; denyBites proves the deny took effect before anything is concluded from it).
func TestFault_ReadOnlyObjectsDirectory(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		resetPermissionsForCleanup(p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "resource_objects_readonly")
	rec.Phase = PhaseResource
	rec.Boundary = "`.qompack/objects` is read-only while a session records into it"
	rec.SeedMethod = "a real session, then a genuine deny on the objects directory (icacls " +
		"(OI)(CI)(WD,AD,WEA,WA,DE,DC) on Windows, 0o500 elsewhere), then a turn, then the deny lifted"

	sess := sessionID("resource-objects-ro")
	seedSession(t, b, p, sess)
	objects := paths.Of(p.Root).Objects

	if err := denyWrites(objects); err != nil {
		skipRecorded(t, rec, "this host would not accept a write deny on "+objects+": "+err.Error())
		return
	}
	if !denyBites(t, objects) {
		resetPermissionsForCleanup(p.Root)
		skipRecorded(t, rec, "a write deny on "+objects+" did not take effect for this process "+
			"(a privileged token ignores a deny ACE, and root ignores POSIX mode bits)")
		return
	}

	baseline := snapshotDegradation(t, b, p)
	shutdownIfReachable(t, p.Root)
	before := auditProject(t, p.Root)

	denied := sessionID("resource-objects-ro-d")
	deniedID := toolUseID("objectsro", 1)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, denied, "startup"))
	writeProjectFile(t, p, "src/denied.ts", seedContent("denied", 48))
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, denied, deniedID, "src/denied.ts", seedContent("denied", 48)))
	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, denied))
	shutdownIfReachable(t, p.Root)

	// Did the deny actually reach the process that WRITES? denyBites proved it bites for this test
	// process, which is not the same question: the store writes happen inside the detached daemon,
	// and a deny that its token ignores would leave this row recording a clean pass over a
	// restriction the product never met. The turn landing in the index is exactly that case, and it
	// is recorded as a skip rather than as a verified row — a skip is never a pass, and "this host
	// could not express the restriction to the product" is not "the product handled it".
	landed := indexHolds(p.Root, deniedID)

	resetPermissionsForCleanup(p.Root)
	if landed {
		skipRecorded(t, rec, "the turn delivered under the deny was indexed anyway, so the write deny "+
			"on "+objects+" did not reach the detached daemon that performs the store writes; this "+
			"host cannot express a read-only objects directory to the writing process")
		return
	}

	recording := recoverSession(t, b, p, sessionID("resource-objects-ro-r"))
	ev := namingEvidence(t, degradationSince(baseline, snapshotDegradation(t, b, p)),
		objectsReadonlyNames)
	shutdownIfReachable(t, p.Root)

	after := auditProject(t, p.Root)
	rec.DanglingBefore = len(before.Dangling)
	rec.DanglingAfter = len(after.Dangling)

	// No pin. This row RECOVERS on today's tree and the mechanism is worth stating: the delivery the
	// denied session made was spooled rather than lost, and lifting the deny let the next daemon
	// drain it, so the turn reaches the index after the restriction goes away.
	//
	// That does not contradict Task 2's F-2, and this row does not re-return it. F-2 is about the
	// EVIDENCE — a read-only `.qompack` leaves nothing durable saying so — and the degradation line
	// in this record is what that looks like from here: recording recovered, and while the deny was
	// in force nothing on any durable surface said it was in force.
	rec.Detail += "\nnote: recording recovered by spool replay once the deny was lifted. Whether " +
		"anything durable said so WHILE the deny was in force is Task 2's F-2 (owner internal/cli + " +
		"internal/ipc); the degradation line above is this row's measurement of it, not a new finding."
	judgeRecovery(t, rec, before, after, recording, ev, "internal/cli + internal/ipc", "")
}

// daemonSettleBound is how long the contention row waits for a second `qompack daemon` to make up
// its mind about the lock. It is generous because it is a bound on a process start, and nothing
// here asserts anything about how long that took: §6.1 forbids the timing claim, not the bound.
const daemonSettleBound = 60 * time.Second

// TestFault_LockContention starts two `qompack daemon` processes for one project and asks the three
// questions the brief names: exactly one holds the lock, the other exits 0 without corrupting
// anything, and hooks delivered during the contention exit 0. Then it takes a stale lock — a dead
// pid with an old heartbeat — and asks whether the next daemon reclaims it.
//
// daemon_test.go's TestRunReturnsNilWhenLockHeld owns the in-process form of the first half. What
// only the installed binary can answer is whether the SECOND PROCESS exits zero, which is what a
// host's hook does when it lazily spawns a daemon into a project that already has one.
func TestFault_LockContention(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "resource_lock_contention")
	rec.Phase = PhaseResource
	rec.Boundary = "two daemons for one project, and a stale lock left by a dead one"
	rec.SeedMethod = "a real session brings the first daemon up; a second `qompack daemon` is run " +
		"against the same project in the foreground; then the lock is rewritten to name a dead pid"

	sess := sessionID("resource-lock")
	seedSession(t, b, p, sess)

	// The first daemon, brought up the way a host brings it up.
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "resume"))
	if !waitDaemonUp(t, p.Root) {
		t.Fatalf("fault: session-start did not bring the first daemon up")
	}
	firstPID, held := daemonHoldingLock(p.Root)
	if !held {
		t.Fatalf("fault: nothing held %s after session-start", daemon.LockPath(p.Root))
	}

	before := auditProject(t, p.Root)

	// The second daemon. It is run in the FOREGROUND and must return on its own: `qompack daemon`
	// finding the lock held is not an error, it is the singleton working.
	stdout, stderr, code := run(t, b.Bin, p.Root, []string{"daemon"}, nil,
		p.EnvWith(map[string]string{"QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS": "1"}))
	secondPID, stillHeld := daemonHoldingLock(p.Root)

	// Hooks delivered during the contention.
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, sess, toolUseID("contention", 1), "src/alpha.ts", seedContent("alpha", 48)))

	contention := fmt.Sprintf("second `qompack daemon` exited %d (stdout %q, stderr %q); the lock "+
		"holder was pid %d before and pid %d after (held=%v)",
		code, snippet(string(stdout)), snippet(string(stderr)), firstPID, secondPID, stillHeld)

	var problems []string
	if code != 0 {
		problems = append(problems, fmt.Sprintf("the second daemon exited %d rather than 0", code))
	}
	if !stillHeld || secondPID != firstPID {
		problems = append(problems, fmt.Sprintf("the lock holder changed from pid %d to pid %d (held=%v)",
			firstPID, secondPID, stillHeld))
	}

	shutdownIfReachable(t, p.Root)

	// The stale lock: a lock file naming a pid that is not alive, with a heartbeat old enough that
	// daemon.AcquireLock's staleness rule (staleAfter = 90 s) applies.
	reclaimed, staleDetail := plantStaleLockAndReclaim(t, b, p)
	if !reclaimed {
		problems = append(problems, "a stale lock was not reclaimed: "+staleDetail)
	}

	shutdownIfReachable(t, p.Root)
	after := auditProject(t, p.Root)
	rec.DanglingBefore = len(before.Dangling)
	rec.DanglingAfter = len(after.Dangling)
	rec.Detail = contention + "\nstale lock: " + staleDetail +
		fmt.Sprintf("\naudit before %s; after %s", before, after)

	newRefs := newlyDangling(before, after)
	if len(newRefs) > 0 {
		problems = append(problems, "the contention left "+describeRefs(newRefs))
	}

	if len(problems) == 0 {
		recordOutcome(t, rec, OutcomeRecovered, "exactly one daemon held the lock, the second exited 0 "+
			"without touching the project, hooks delivered during the contention exited 0, and a "+
			"stale lock was reclaimed by the next daemon")
		return
	}
	recordOutcome(t, rec, OutcomeFailed, fmt.Sprintf("lock contention did not hold its contract: %s. "+
		"Owner: internal/daemon.", strings.Join(problems, "; ")))
	t.Errorf("fault %s: %s\n%s", rec.Name, strings.Join(problems, "; "), rec.Detail)
}

// stalePID is the pid a planted stale lock names. It is deliberately one no process can hold:
// process id 0 is the system idle/scheduler pseudo-process on both platforms this runs on, never a
// pid a daemon could have been assigned, so testutil.ProcessAlive answers false without this test
// ever having to pick a number and hope.
const stalePID = 0

// plantStaleLockAndReclaim writes a lock file naming a dead pid with an old heartbeat, then brings a
// daemon up and reports whether it took the lock over.
//
// Nothing is signalled here: the lock is a FILE, the pid in it never existed as a daemon, and the
// only process this function starts is the one it then shuts down.
func plantStaleLockAndReclaim(t *testing.T, b bundle, p project) (bool, string) {
	t.Helper()
	lockPath := daemon.LockPath(p.Root)
	if pid, held := daemonHoldingLock(p.Root); held {
		return false, fmt.Sprintf("a live daemon (pid %d) still held the lock; the stale-lock half "+
			"of this row needs the project quiet", pid)
	}
	// daemon.LockInfo's own field names, because AcquireLock's staleness protocol reads the pid out
	// of this document: a record with the wrong key would present as an unparseable lock rather
	// than as a lock naming a dead process, and the row would be measuring the wrong refusal.
	info := daemon.LockInfo{PID: stalePID, Started: 1, Addr: "stale", Version: "0"}
	body, err := json.Marshal(info)
	if err != nil {
		return false, "could not encode the stale lock: " + err.Error()
	}
	if err := os.MkdirAll(paths.Long(filepath.Dir(lockPath)), 0o700); err != nil {
		return false, "could not create the run directory: " + err.Error()
	}
	writeOver(t, lockPath, body)
	// The heartbeat beside it is what AcquireLock reads for staleness, and it reads its MTIME
	// rather than its contents (lock.go: "once daemon.hb's mtime is older than this"). Writing an
	// old-looking body would change nothing at all; the mtime has to move.
	hb := strings.TrimSuffix(lockPath, ".lock") + ".hb"
	writeOver(t, hb, []byte("stale"))
	ageHeartbeat(t, p.Root)

	runHook(t, b.Bin, p, []string{"session-start"},
		sessionStartPayload(t, p.Root, sessionID("resource-lock-stale"), "startup"))
	up := waitDaemonUpFor(t, p.Root, daemonSettleBound)
	pid, held := daemonHoldingLock(p.Root)
	detail := fmt.Sprintf("planted a lock naming pid %d with an ancient heartbeat; a daemon %s and "+
		"the lock is now held=%v by pid %d", stalePID,
		map[bool]string{true: "answered", false: "did not answer"}[up], held, pid)
	return up && held && pid != stalePID, detail
}
