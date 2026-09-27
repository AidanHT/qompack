package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The D17 cold-start rows (V6 close-out w5-coldstart): EnsureRunning, session-start's daemon starter,
// follows the same spawn-lock rule as a hook's or the MCP server's lazy spawn. A spawn another
// process announced in run/spawn.lock within the lock's freshness window is waited for, never
// duplicated; a spawn EnsureRunning makes is announced there too; and a lock nobody finishes goes
// stale, after which the next caller spawns.

// spawnLockTestBound is the poll bound these rows give ensureRunning when they expect a daemon: far
// above the fake daemon's start-up delay, so a pass never depends on the machine's speed.
const spawnLockTestBound = 5 * time.Second

// spawnLockMissBound is the bound for the rows that expect NO daemon: they wait it out in full.
const spawnLockMissBound = 200 * time.Millisecond

// fakeDaemonUpAfter is how long the fake daemon takes to start serving once spawned. It is off
// ensureRunning's 25 ms poll grid (ensureRunningPollInterval), so that no row depends on whether a
// poll lands just before or just after the daemon comes up and clears the spawn lock.
const fakeDaemonUpAfter = 163 * time.Millisecond

// fakeDaemons stands in for the process spawnDetached starts: a real listener at the project's
// resolved address, brought up after a delay, the way a spawned daemon comes up once it serves, and
// deleting run/spawn.lock once it listens, as Run does. It counts the spawns it was asked for.
type fakeDaemons struct {
	t *testing.T

	mu      sync.Mutex
	servers []ipc.Server
	up      map[string]bool
	timers  []*time.Timer
	closed  bool

	calls atomic.Int64
}

func newFakeDaemons(t *testing.T) *fakeDaemons {
	f := &fakeDaemons{t: t}
	t.Cleanup(f.close)
	return f
}

// serveAfter brings up a daemon for root after d (serve).
func (f *fakeDaemons) serveAfter(root string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.timers = append(f.timers, time.AfterFunc(d, func() { f.serve(root) }))
}

// serve brings up a daemon for root now: it listens, starts serving and then deletes run/spawn.lock,
// in Run's order (NewServer, Serve, WriteState, removeSpawnLockFile). A second daemon for a root
// already served exits without listening or touching the lock, as a real one that loses the
// singleton lock (daemon.lock) does.
func (f *fakeDaemons) serve(root string) {
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.up[root] {
		return
	}
	srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
	if err != nil {
		return
	}
	if f.up == nil {
		f.up = map[string]bool{}
	}
	f.up[root] = true
	f.servers = append(f.servers, srv)
	go func() {
		_ = srv.Serve(context.Background(), func(context.Context, ipc.Request) ipc.Response {
			return ipc.Response{OK: true}
		})
	}()
	removeSpawnLockFile(root)
}

func (f *fakeDaemons) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for _, tm := range f.timers {
		tm.Stop()
	}
	for _, s := range f.servers {
		_ = s.Close()
	}
}

// spawnUp is a spawner whose daemon comes up after fakeDaemonUpAfter.
func (f *fakeDaemons) spawnUp(root, _ string) error {
	f.calls.Add(1)
	f.serveAfter(root, fakeDaemonUpAfter)
	return nil
}

// spawnNever is a spawner whose daemon never comes up.
func (f *fakeDaemons) spawnNever(string, string) error {
	f.calls.Add(1)
	return nil
}

// writeSpawnLock leaves the spawn.lock another spawner writes before it launches a daemon, stamped
// at, in the format ipc's lazy spawn writes it.
func writeSpawnLock(t *testing.T, root string, at time.Time) string {
	t.Helper()
	run := paths.Of(root).Run
	require.NoError(t, os.MkdirAll(paths.Long(run), 0o700))
	p := filepath.Join(run, runSpawnLockFileName)
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(strconv.FormatInt(at.UnixMilli(), 10)), 0o600))
	return p
}

// TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces is D17a's first row: another hook
// has just spawned this project's daemon (its spawn.lock is fresh) and that daemon is still coming
// up, so session-start must wait for it rather than spawn a second one. The self path would fail
// loudly if EnsureRunning tried to start it.
func TestEnsureRunning_WaitsForTheDaemonAFreshSpawnLockAnnounces(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	writeSpawnLock(t, root, clk.Now())
	newFakeDaemons(t).serveAfter(root, fakeDaemonUpAfter)

	spawned, err := EnsureRunning(root, "/this/path/does/not/exist", logging.Nop(), clk)
	require.NoError(t, err, "a spawn announced in a fresh spawn.lock must be waited for, not duplicated")
	require.False(t, spawned, "EnsureRunning spawned nothing: the daemon it found was another hook's")
}

// TestEnsureRunning_TwoColdHooksStartOneDaemon: two session-starts reach a project with no daemon
// at the same moment. One spawns; the other waits for that daemon.
func TestEnsureRunning_TwoColdHooksStartOneDaemon(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	f := newFakeDaemons(t)

	type result struct {
		spawned bool
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: spawnLockTestBound}, f.spawnUp)
			results <- result{s, err}
		}()
	}
	wg.Wait()
	close(results)
	spawnedCount := 0
	for r := range results {
		require.NoError(t, r.err)
		if r.spawned {
			spawnedCount++
		}
	}
	require.EqualValues(t, 1, f.calls.Load(), "two cold hooks on one project must start one daemon, not two")
	require.Equal(t, 1, spawnedCount, "exactly one of them reports the spawn")
}

// TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits: on Windows the spawner first
// copies and verifies the binary it runs (the staged copy, D10), which can take seconds on a loaded
// machine. A second hook arriving during that window must find the spawn announced and wait.
func TestEnsureRunning_AHookArrivingWhileAStagedSpawnIsStartingWaits(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	f := newFakeDaemons(t)

	staging := make(chan struct{})
	release := make(chan struct{})
	slowStagedSpawn := func(r, s string) error {
		close(staging)
		<-release // still copying the binary
		return f.spawnUp(r, s)
	}

	first := make(chan error, 1)
	go func() {
		_, err := ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: spawnLockTestBound}, slowStagedSpawn)
		first <- err
	}()
	<-staging
	second := make(chan error, 1)
	var secondSpawned atomic.Bool
	go func() {
		s, err := ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: spawnLockTestBound}, f.spawnUp)
		secondSpawned.Store(s)
		second <- err
	}()
	time.AfterFunc(fakeDaemonUpAfter, func() { close(release) })

	require.NoError(t, <-first)
	require.NoError(t, <-second)
	require.EqualValues(t, 1, f.calls.Load(), "the hook that arrived mid-staging must not spawn a second daemon")
	require.False(t, secondSpawned.Load())
}

// TestEnsureRunning_ItsSpawnHoldsOffALazySpawn: session-start's own spawn is announced in
// spawn.lock, so when its daemon is not up in time and the hook's connect fails, the client's lazy
// spawn — and any other hook's — finds that spawn in flight and does not start a second daemon.
func TestEnsureRunning_ItsSpawnHoldsOffALazySpawn(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	f := newFakeDaemons(t)

	spawned, err := ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: spawnLockMissBound}, f.spawnNever)
	require.True(t, spawned)
	require.ErrorIs(t, err, core.ErrNotFound)

	var lazy atomic.Int64
	requireLazySpawns(t, root, clk, &lazy)
	require.EqualValues(t, 0, lazy.Load(), "a lazy spawn must honour the spawn session-start already announced")
}

// TestEnsureRunning_AnAbandonedSpawnLockBlocksOnlyUntilItIsStale: a spawner that took the lock and
// died never launches a daemon. Its lock holds spawns off only while it is fresh; once it is older
// than the freshness window, the next caller spawns (fail-safe).
func TestEnsureRunning_AnAbandonedSpawnLockBlocksOnlyUntilItIsStale(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	writeSpawnLock(t, root, clk.Now())
	f := newFakeDaemons(t)

	clk.Advance(ensureSpawnLockHalfWindow)
	spawned, err := ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: spawnLockMissBound}, f.spawnUp)
	require.ErrorIs(t, err, core.ErrNotFound, "a fresh lock is waited on, up to the bound")
	require.False(t, spawned)
	require.EqualValues(t, 0, f.calls.Load())

	clk.Advance(ensureSpawnLockHalfWindow + time.Second) // now older than the freshness window
	spawned, err = ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: spawnLockTestBound}, f.spawnUp)
	require.NoError(t, err)
	require.True(t, spawned, "a stale lock must not block a spawn")
	require.EqualValues(t, 1, f.calls.Load())
}

// TestEnsureRunning_ReleasesItsClaimWhenTheSpawnFails: a spawn that could not even start leaves no
// claim behind to hold the next hook off for the whole freshness window.
func TestEnsureRunning_ReleasesItsClaimWhenTheSpawnFails(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	boom := errors.New("fixture: the spawn could not start")

	spawned, err := ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: spawnLockTestBound},
		func(string, string) error { return boom })
	require.ErrorIs(t, err, boom)
	require.False(t, spawned)
	require.NoFileExists(t, filepath.Join(paths.Of(root).Run, runSpawnLockFileName))

	var lazy atomic.Int64
	requireLazySpawns(t, root, clk, &lazy)
	require.EqualValues(t, 1, lazy.Load(), "the next spawner is not held off by a spawn that never started")
}

// onFirstNowClock is a fakeClock that runs fn the first time anything reads it. ensureRunning reads
// its clock first inside its first ipc.ClaimSpawn, to stamp the claim before it creates the lock, so
// fn runs after the dial that found no daemon and before the claim.
type onFirstNowClock struct {
	*fakeClock
	once sync.Once
	fn   func()
}

func (c *onFirstNowClock) Now() time.Time {
	c.once.Do(c.fn)
	return c.fakeClock.Now()
}

func (c *onFirstNowClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn: the daemon another spawner announced
// comes up, and deletes run/spawn.lock as Run does, after ensureRunning's dial missed it and before
// its claim. The claim then succeeds, on a lock the daemon itself freed. EnsureRunning must dial again
// before it spawns, find that daemon and give the claim back, not start a second daemon: the lock is
// free both when nobody is spawning and when the spawn it announced has finished.
func TestEnsureRunning_ALockItsDaemonFreedIsNoLicenceToSpawn(t *testing.T) {
	root := t.TempDir()
	f := newFakeDaemons(t)
	clk := &onFirstNowClock{fakeClock: newFakeClock(epoch), fn: func() { f.serve(root) }}
	lockPath := writeSpawnLock(t, root, clk.fakeClock.Now())

	spawned, err := ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: spawnLockTestBound}, f.spawnUp)
	require.NoError(t, err)
	require.EqualValues(t, 0, f.calls.Load(), "the daemon that freed the lock is up: nothing may spawn a second one")
	require.False(t, spawned, "EnsureRunning spawned nothing: the daemon it found was another spawner's")
	require.NoFileExists(t, lockPath, "the claim it made on the freed lock is given back")
}

// staleMidWaitBound is the poll bound TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed gives
// ensureRunning; staleMidWaitAt is when that row ages the lock it found past the freshness window;
// and staleMidWaitSpawn is how long the spawn it then makes takes: longer than the whole bound, as a
// staged spawn on a loaded Windows machine can (the V6 close-out's cold-start diagnostic saw process
// creation stall for 4-5 s), so the daemon it starts comes up after the deadline the wait began with.
const (
	staleMidWaitBound = time.Second
	staleMidWaitAt    = 300 * time.Millisecond
	staleMidWaitSpawn = staleMidWaitBound + staleMidWaitAt
)

// TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed: the spawner whose fresh claim
// ensureRunning found died before it launched anything. Its claim ages past the freshness window
// while ensureRunning is still waiting, so ensureRunning reclaims it and spawns, within the same call
// (fail-safe). The daemon it spawned then gets its own wait, counted from that spawn as for any
// spawn this call makes, not what was left of the wait for the dead spawner's daemon.
func TestEnsureRunning_AClaimThatGoesStaleMidWaitIsReclaimed(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	writeSpawnLock(t, root, clk.Now())
	f := newFakeDaemons(t)

	var aged atomic.Bool
	ager := time.AfterFunc(staleMidWaitAt, func() {
		aged.Store(true)
		clk.Advance(2*ensureSpawnLockHalfWindow + time.Second) // now older than the freshness window
	})
	t.Cleanup(func() { ager.Stop() })
	slowSpawn := func(r, s string) error {
		require.True(t, aged.Load(), "nothing may spawn while the claim it found is fresh")
		stall := time.NewTimer(staleMidWaitSpawn)
		defer stall.Stop()
		<-stall.C
		return f.spawnUp(r, s)
	}

	spawned, err := ensureRunning(root, "self", logging.Nop(), clk, pollBound{after: staleMidWaitBound}, slowSpawn)
	require.NoError(t, err, "a daemon spawned on a claim reclaimed mid-wait must get its own wait to come up")
	require.True(t, spawned, "the claim went stale inside the wait, so this call reclaimed it and spawned")
	require.EqualValues(t, 1, f.calls.Load())
}

// ensureSpawnLockHalfWindow is half the spawn lock's freshness window (ipc's spawnLockStaleAfter,
// 10 s, which this package cannot name).
const ensureSpawnLockHalfWindow = 5 * time.Second

// requireLazySpawns sends one fire-and-forget event through a hook client whose daemon is not
// reachable, so its lazy spawn runs, counting the spawns it makes into n.
func requireLazySpawns(t *testing.T, root string, clk core.Clock, n *atomic.Int64) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	sp, err := ipc.NewSpool(filepath.Join(t.TempDir(), "spool"))
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, sp, logging.Nop(), nil, ipc.ClientOptions{
		ProjectRoot: root, Self: "self", Clock: clk,
		ConnectDeadline: spawnLockMissBound, AckDeadline: spawnLockMissBound,
		Spawn: func(string, string) error { n.Add(1); return nil },
	})
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.Send(context.Background(), ipc.Request{Op: ipc.OpObserveTool, Session: "s", TS: 1}, spawnLockMissBound)
	require.NoError(t, err)
}

// untilDaemonUpAfter is when TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound's daemon comes
// up: after EnsureRunning's own fixed bound, so only a poll that runs to its given deadline sees it.
const untilDaemonUpAfter = ensureRunningPollBound + 300*time.Millisecond

// untilDeadlineMargin is how long that row's deadline outlasts the daemon's start, for a loaded
// machine's timer and poll delays.
const untilDeadlineMargin = 2 * time.Second

// sessionStartBound is the pollBound session-start hands ensureRunning through EnsureRunningUntil:
// its pre-send deadline, EnsureRunning's own wait after a late spawn, and the last instant a reply
// could still follow.
func sessionStartBound(until, latest time.Time) pollBound {
	return pollBound{until: until, after: ensureRunningPollBound, latest: latest}
}

// TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound: session-start hands EnsureRunningUntil
// the instant its hook budget allows (D17b), and the poll runs until then — a daemon that comes up
// after EnsureRunning's fixed 1.5 s bound but before that instant is found, not missed and spooled.
func TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	f := newFakeDaemons(t)
	spawnLate := func(r, _ string) error {
		f.calls.Add(1)
		f.serveAfter(r, untilDaemonUpAfter)
		return nil
	}

	until := time.Now().Add(untilDaemonUpAfter + untilDeadlineMargin)
	spawned, err := ensureRunning(root, "self", logging.Nop(), clk, sessionStartBound(until, until.Add(time.Second)), spawnLate)
	require.NoError(t, err, "a daemon up before the deadline must be found")
	require.True(t, spawned)
	require.EqualValues(t, 1, f.calls.Load())
}

// TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait: the spawn itself ran past the pre-send
// deadline — process creation stalls for seconds on a loaded Windows machine (the V6 close-out's
// cold-start diagnostic saw 4-5 s) — and the daemon it started comes up moments later. It still gets
// the 1.5 s EnsureRunning has always given a daemon it started, taken from the reply wait, instead
// of being given up on at once and the start spooled.
func TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	f := newFakeDaemons(t)

	now := time.Now()
	spawned, err := ensureRunning(root, "self", logging.Nop(), clk,
		sessionStartBound(now.Add(-time.Second), now.Add(spawnLockTestBound)), f.spawnUp)
	require.NoError(t, err, "a daemon up within the classic wait after a late spawn must be found")
	require.True(t, spawned)
	require.EqualValues(t, 1, f.calls.Load())
}

// TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon: when everything before the pre-send step
// used the whole budget, no reply could follow any more, so session-start gets no wait — but the
// session still gets its daemon started: one spawn, then ErrNotFound at once, and the start is
// spooled for that daemon to replay.
func TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	f := newFakeDaemons(t)

	past := time.Now().Add(-time.Second)
	began := time.Now()
	spawned, err := ensureRunning(root, "self", logging.Nop(), clk, sessionStartBound(past, past), f.spawnNever)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.True(t, spawned, "a passed deadline cuts the wait, never the spawn")
	require.EqualValues(t, 1, f.calls.Load())
	require.Less(t, time.Since(began), ensureRunningPollBound, "with no reply possible it does not wait")
}

// pollEndsWait is how far ahead of the call TestEnsureRunningUntil_ThePollEndsAtItsDeadline puts
// its deadline: four post-claim dials, long enough that the dial and the claim before the poll
// never outlast it, so the poll's deadline is the instant the row gives it.
const pollEndsWait = 4 * spawnClaimDialTimeout

// pollEndsFreedBefore is how long before that deadline the row's daemon frees the claim that
// announced it: half a post-claim dial, so the dial after the claim this call then takes cannot
// have its full bound inside the wait.
const pollEndsFreedBefore = spawnClaimDialTimeout / 2

// hungDaemonDials stands in for the dials of a daemon that listens and never accepts, a hung one,
// or on Windows a pipe whose every instance is claimed: each dial waits out its whole bound and
// fails. It records the instant each dial was bounded to.
type hungDaemonDials struct {
	mu  sync.Mutex
	bys []time.Time
}

func (h *hungDaemonDials) probe(_ ipc.Addr, by time.Time) bool {
	h.mu.Lock()
	h.bys = append(h.bys, by)
	h.mu.Unlock()
	wait := time.NewTimer(time.Until(by))
	defer wait.Stop()
	<-wait.C
	return false
}

// dials returns the instants every dial so far was bounded to, in order.
func (h *hungDaemonDials) dials() []time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Time(nil), h.bys...)
}

// TestEnsureRunningUntil_ThePollEndsAtItsDeadline (w6-borrow review): session-start's poll for a
// daemon on its way ends at the instant it is given, the borrow limit (D21), because that is what
// leaves the reply D9's compact bound. No dial the poll makes may run past it, and neither may the
// wait for its next tick. The daemon a fresh claim announced listens and never accepts. In the
// first row its claim is held to the end, so only the poll's own dials run; in the second the
// daemon frees the claim as Run does just before the deadline, so this call claims it and makes
// the longer dial after a claim, which must be cut short there too, and a dial cut short is no
// licence to spawn: a daemon may be up and slow to accept.
func TestEnsureRunningUntil_ThePollEndsAtItsDeadline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		freed bool
	}{
		{name: "claim_held_to_the_end"},
		{name: "claim_freed_near_the_end", freed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			clk := newFakeClock(epoch)
			lockPath := writeSpawnLock(t, root, clk.Now())
			f := newFakeDaemons(t)
			dials := &hungDaemonDials{}

			until := time.Now().Add(pollEndsWait)
			freedDone := make(chan struct{})
			if tc.freed {
				freed := time.AfterFunc(time.Until(until.Add(-pollEndsFreedBefore)), func() {
					removeSpawnLockFile(root)
					close(freedDone)
				})
				t.Cleanup(func() { freed.Stop() })
			} else {
				close(freedDone)
			}

			spawned, err := ensureRunningWith(root, "self", logging.Nop(), clk,
				pollBound{until: until, after: spawnLockMissBound, latest: until.Add(spawnLockTestBound)},
				f.spawnNever, dials.probe)
			over := time.Since(until)
			require.ErrorIs(t, err, core.ErrNotFound, "no daemon answered within the wait")
			got := dials.dials()
			require.Greater(t, len(got), 2, "the poll dialled")
			// Each contract is checked on its own, so one run shows every way the poll overran.
			assert.False(t, spawned, "a dial cut short at the end of the wait is no licence to spawn")
			assert.Zero(t, f.calls.Load(), "nothing is spawned while a daemon may be up and slow to accept")
			var past []string
			for i, by := range got[1:] { // got[0] is the dial before the poll, which is never cut short
				if by.After(until) {
					past = append(past, fmt.Sprintf("dial %d of %d: %s", i+1, len(got)-1, by.Sub(until)))
				}
			}
			assert.Empty(t, past, "no dial of the poll may be bounded past its deadline")
			// How long after its deadline the call returned is a measurement, not a judgement (ADR
			// 0010): the dials and the spawn it may not make are pinned above, the wait for a tick
			// by TestWaitForTick_EndsAtTheDeadlineNotTheTick, and what is left is the claim's file
			// I/O and the OS scheduling this process, which a co-loaded machine stretched to 0.5 s.
			t.Logf("the poll returned %s after its deadline", over)
			<-freedDone
			if tc.freed {
				assert.NoFileExists(t, lockPath, "the claim taken on the freed lock is given back")
			} else {
				assert.FileExists(t, lockPath, "another spawner's claim is left alone")
			}
		})
	}
}

// TestWaitForTick_EndsAtTheDeadlineNotTheTick (w6-borrow review): the poll's wait for its next tick
// ends at the poll's deadline when that comes first, and then no dial may follow; a tick that comes
// first lets the poll dial. The slow ticker here would tick only at spawnLockTestBound, so a wait
// that ran to the tick instead of the deadline takes 5 s against a deadline 200 ms away, a margin
// no scheduling delay on a loaded machine comes near.
func TestWaitForTick_EndsAtTheDeadlineNotTheTick(t *testing.T) {
	slow := time.NewTicker(spawnLockTestBound)
	defer slow.Stop()
	began := time.Now()
	require.False(t, waitForTick(slow, began.Add(spawnLockMissBound)), "no dial may follow a wait that reached the deadline")
	require.Less(t, time.Since(began), spawnLockTestBound/2, "the wait ended at the deadline, not at the tick")
	require.False(t, waitForTick(slow, time.Now()), "a deadline that has come gets no wait and no dial")

	fast := time.NewTicker(ensureRunningPollInterval)
	defer fast.Stop()
	require.True(t, waitForTick(fast, time.Now().Add(spawnLockTestBound)), "a tick before the deadline lets the poll dial")
}
