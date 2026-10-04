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

// sessionStartBound is the pollBound session-start hands ensureRunning through EnsureRunningUntil:
// its pre-send deadline, EnsureRunning's own wait after a late spawn, and the last instant a reply
// could still follow.
func sessionStartBound(until, latest time.Time) pollBound {
	return pollBound{until: until, after: ensureRunningPollBound, latest: latest}
}

// The three rows below run on a stepPollClock (D61(c), wave 22's H4): the wall-clock rows they
// replace gave a loaded machine 2 s, 1.3 s and 1.5 s of margin, and the hosted runner of ci.yml run
// 37229942287 took about 1.4 s to bring one call to its first claim. On the step clock each daemon
// comes up at an exact instant after its spawn and no margin is needed.

// TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound: session-start hands EnsureRunningUntil
// the instant its hook budget allows (D17b), and the poll runs until then — a daemon that comes up
// after EnsureRunning's fixed 1.5 s bound but before that instant is found, not missed and spooled.
// The same daemon polled under EnsureRunning's fixed bound is missed, so the row tells the two apart.
func TestEnsureRunningUntil_PollsToItsDeadlineNotTheFixedBound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		found bool
	}{
		{name: "polled_to_the_given_instant", found: true},
		{name: "polled_for_the_fixed_bound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			clk := newFakeClock(epoch)
			pc := newStepPollClock(epoch)
			d := &stepComingDaemon{t: t, clock: pc, root: root}

			// The first tick at or after the daemon's start comes before until: one poll interval later.
			until := pc.Now().Add(untilDaemonUpAfter + ensureRunningPollInterval)
			bound := sessionStartBound(until, until.Add(time.Second))
			if !tc.found {
				bound = pollBound{after: ensureRunningPollBound}
			}
			spawned, err := ensureRunningWith(root, "self", logging.Nop(), clk, bound,
				d.spawnUpAfter(untilDaemonUpAfter), d.probe, pc)
			require.True(t, spawned)
			require.Len(t, d.spawnAt, 1)
			upAt := d.spawnAt[0].Add(untilDaemonUpAfter)
			if !tc.found {
				require.ErrorIs(t, err, core.ErrNotFound, "EnsureRunning's fixed bound ends before this daemon comes up")
				require.Equal(t, d.spawnAt[0].Add(ensureRunningPollBound), pc.Now(), "the fixed bound's poll ends at its bound")
				return
			}
			require.NoError(t, err, "a daemon up before the deadline must be found")
			require.False(t, pc.Now().Before(upAt), "found once it was up")
			require.Less(t, pc.Now().Sub(upAt), ensureRunningPollInterval, "found at the first tick after it came up")
		})
	}
}

// TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait: the spawn itself ran past the pre-send
// deadline — process creation stalls for seconds on a loaded Windows machine (the V6 close-out's
// cold-start diagnostic saw 4-5 s) — and the daemon it started comes up moments later. It still gets
// the 1.5 s EnsureRunning has always given a daemon it started, taken from the reply wait, instead
// of being given up on at once and the start spooled.
func TestEnsureRunningUntil_ALateSpawnStillGetsTheClassicWait(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	pc := newStepPollClock(epoch)
	d := &stepComingDaemon{t: t, clock: pc, root: root}

	now := pc.Now()
	spawned, err := ensureRunningWith(root, "self", logging.Nop(), clk,
		sessionStartBound(now.Add(-time.Second), now.Add(spawnLockTestBound)), d.spawnUpAfter(fakeDaemonUpAfter), d.probe, pc)
	require.NoError(t, err, "a daemon up within the classic wait after a late spawn must be found")
	require.True(t, spawned)
	require.Len(t, d.spawnAt, 1)
	upAt := d.spawnAt[0].Add(fakeDaemonUpAfter)
	require.False(t, pc.Now().Before(upAt), "found once it was up")
	require.Less(t, pc.Now().Sub(upAt), ensureRunningPollInterval, "found at the first tick after it came up")
}

// TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon: when everything before the pre-send step
// used the whole budget, no reply could follow any more, so session-start gets no wait — but the
// session still gets its daemon started: one spawn, then ErrNotFound at once, and the start is
// spooled for that daemon to replay.
func TestEnsureRunningUntil_APassedDeadlineStillStartsADaemon(t *testing.T) {
	root := t.TempDir()
	clk := newFakeClock(epoch)
	pc := newStepPollClock(epoch)
	d := &stepComingDaemon{t: t, clock: pc, root: root}

	began := pc.Now()
	past := began.Add(-time.Second)
	spawned, err := ensureRunningWith(root, "self", logging.Nop(), clk, sessionStartBound(past, past), d.spawnNever, d.probe, pc)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.True(t, spawned, "a passed deadline cuts the wait, never the spawn")
	require.Len(t, d.spawnAt, 1)
	require.Equal(t, began, pc.Now(), "with no reply possible it does not wait")
	require.Len(t, d.dials, 2, "it dials before its claim and after it, and not again after its spawn")
}

// pollEndsWait is how far after the call TestEnsureRunningUntil_ThePollEndsAtItsDeadline puts the
// instant it gives the poll as its deadline (until): four post-claim dials.
const pollEndsWait = 4 * spawnClaimDialTimeout

// pollEndsFreedBefore is how long before that instant the row's daemon frees the claim that
// announced it, where it frees it near the end: half a post-claim dial, so the dial after the claim
// this call then takes cannot have its whole bound inside the wait.
const pollEndsFreedBefore = spawnClaimDialTimeout / 2

// pollEndsFreedEarly is how long before that instant the daemon frees the claim where the dial after
// this call's claim does have its whole bound inside the wait: two post-claim dials, so the claim,
// which follows the free by at most a tick wait and a poll dial, leaves its dial's bound before the
// end. It is also how late that dial returns where the row stalls it, which puts its return past the
// end.
const pollEndsFreedEarly = 2 * spawnClaimDialTimeout

// pollEndsHostedStall is how much later than its bound the dial before the poll returns where the
// row models the hosted windows-latest runner of ci.yml run 37229942287. There the call reached its
// first claim about 1.42 s after it began (the dial after that claim was made 417 ms after the row's
// 1 s deadline), so its poll began after the instant the row had given.
const pollEndsHostedStall = 1400 * time.Millisecond

// pollEndsStallStep is the step of the row's sweep over how late the dial before the poll returns.
const pollEndsStallStep = 5 * ensureRunningPollInterval

// pollEndsCase is one of TestEnsureRunningUntil_ThePollEndsAtItsDeadline's runs.
type pollEndsCase struct {
	name string
	// freeBefore is how long before until the daemon frees the claim that announced it. Zero holds
	// the claim to the end.
	freeBefore time.Duration
	// startStall is how much later than its bound the dial before the poll returns.
	startStall time.Duration
	// claimStall is how much later than its bound the dial after this call's own claim returns.
	claimStall time.Duration
	// wantSpawn is whether the call spawns.
	wantSpawn bool
}

// pollEndsCases are the row's runs: six named ones, then the sweep. Every run is judged by the same
// contract (checkPollEndsAtItsDeadline); wantSpawn states each run's expected answer outright.
func pollEndsCases() []pollEndsCase {
	cases := []pollEndsCase{
		{name: "claim_held_to_the_end"},
		{name: "claim_freed_near_the_end", freeBefore: pollEndsFreedBefore},
		{name: "claim_held_past_a_stalled_start", startStall: pollEndsHostedStall},
		{
			name: "claim_freed_during_a_stalled_start", freeBefore: pollEndsFreedBefore,
			startStall: pollEndsHostedStall, wantSpawn: true,
		},
		{
			name: "claim_freed_with_its_dial_stalled_past_the_end", freeBefore: pollEndsFreedEarly,
			claimStall: pollEndsFreedEarly,
		},
		{name: "claim_freed_with_time_for_a_whole_dial", freeBefore: pollEndsFreedEarly, wantSpawn: true},
	}
	// The sweep: however late the call reaches its first claim, a held claim is never a licence, and
	// a claim freed near the end licenses a spawn only when it was already free at that first claim,
	// whose dial comes before the poll begins and so is never cut short.
	freedAt := pollEndsWait - pollEndsFreedBefore
	for stall := time.Duration(0); stall <= pollEndsWait+2*spawnClaimDialTimeout; stall += pollEndsStallStep {
		cases = append(cases,
			pollEndsCase{name: fmt.Sprintf("sweep_claim_held_start_stalled_%s", stall), startStall: stall},
			pollEndsCase{
				name:       fmt.Sprintf("sweep_claim_freed_near_the_end_start_stalled_%s", stall),
				freeBefore: pollEndsFreedBefore, startStall: stall,
				wantSpawn: ensureRunningDialTimeout+stall >= freedAt,
			})
	}
	return cases
}

// pollEndsRun is what one run of the row observed, every instant on its stepPollClock.
type pollEndsRun struct {
	until, latest, freedAt time.Time
	dials                  []stepDial
	spawnAt                []time.Time
	returned               time.Time
	spawned                bool
	err                    error
	lockPath               string
	otherStamp, ownStamp   string
}

// end is the end of the wait the row's bound gives a poll that begins at begun: until, or
// spawnLockMissBound after begun if that is later, and never past latest. It restates pollBound's
// contract, so the row does not judge the poll by the product's own arithmetic.
func (r *pollEndsRun) end(begun time.Time) time.Time {
	end := begun.Add(spawnLockMissBound)
	if r.until.After(end) {
		end = r.until
	}
	if end.After(r.latest) {
		end = r.latest
	}
	return end
}

// earlier is the earlier of a and b.
func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// TestEnsureRunningUntil_ThePollEndsAtItsDeadline (w6-borrow review; wave 22's H4): session-start's
// poll for a daemon on its way ends at the end of its wait: the instant it is given, the borrow limit
// (D21), or, for a poll that began too late to have it, the bound's own wait after it began. That is
// what leaves the reply D9's compact bound. No dial the poll makes may run past that end, and neither
// may the wait for its next tick. The daemon a fresh claim announced listens and never accepts. Held
// to the end, its claim keeps this call from spawning, and only the poll's own dials run. Freed, as
// Run frees it, the claim is taken by this call, which makes the longer dial after a claim. That dial
// is cut short at the end of the wait like any other, and a dial cut short, or one that returns at
// or after the end, is no licence to spawn: a daemon may be up and slow to accept. A dial after a
// claim that had its whole bound, before the poll began or inside the wait, and found nothing is the
// licence (D17a: a hung daemon is replaced, and the daemon started loses daemon.lock to it and
// exits); the poll then begins again from that spawn.
//
// The row runs on a stepPollClock, so its verdict never depends on how fast the machine runs
// (D61(c)), and it judges the instant the call returns exactly. Its stalled runs model the hosted
// runner of ci.yml run 37229942287, which took about 1.4 s to bring the call to its first claim, past
// the deadline the wall-clock row had given: the poll began after that instant and ran its own wait,
// and a claim freed before the call's first claim was taken before the poll began, so its dial was
// never cut short and the call spawned. Both are the rule above; the wall-clock row read both as
// failures. What the step clock does not model is the claim's file I/O and the OS scheduling the
// call, which on a co-loaded machine can still delay a real call's return (ADR 0010).
func TestEnsureRunningUntil_ThePollEndsAtItsDeadline(t *testing.T) {
	for _, tc := range pollEndsCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			clk := newFakeClock(epoch)
			r := &pollEndsRun{lockPath: writeSpawnLock(t, root, clk.Now())}
			r.otherStamp = readSpawnLockStamp(r.lockPath)
			// This call's own claim is stamped apart from the other spawner's, which stays fresh.
			clk.Advance(time.Millisecond)
			r.ownStamp = strconv.FormatInt(clk.Now().UnixMilli(), 10)

			pc := newStepPollClock(epoch)
			r.until = pc.Now().Add(pollEndsWait)
			r.latest = r.until.Add(spawnLockTestBound)
			if tc.freeBefore > 0 {
				r.freedAt = r.until.Add(-tc.freeBefore)
				pc.at(r.freedAt, func() { removeSpawnLockFile(root) })
			}
			daemon := &stepHungDaemon{t: t, clock: pc, lockPath: r.lockPath, stall: func(i int, d stepDial) time.Duration {
				switch {
				case i == 0:
					return tc.startStall
				case d.lock == r.ownStamp:
					return tc.claimStall
				}
				return 0
			}}
			spawn := func(string, string) error {
				r.spawnAt = append(r.spawnAt, pc.Now())
				return nil
			}

			r.spawned, r.err = ensureRunningWith(root, "self", logging.Nop(), clk,
				pollBound{until: r.until, after: spawnLockMissBound, latest: r.latest}, spawn, daemon.probe, pc)
			r.returned = pc.Now()
			r.dials = daemon.dials
			checkPollEndsAtItsDeadline(t, tc, r)
		})
	}
}

// checkPollEndsAtItsDeadline judges one run of TestEnsureRunningUntil_ThePollEndsAtItsDeadline. Each
// contract is checked on its own, so one run shows every way the poll went wrong.
func checkPollEndsAtItsDeadline(t *testing.T, tc pollEndsCase, r *pollEndsRun) {
	t.Helper()
	require.ErrorIs(t, r.err, core.ErrNotFound, "no daemon answered within the wait")
	require.NotEmpty(t, r.dials, "the call dialled")

	first := r.dials[0]
	assert.Equal(t, first.made.Add(ensureRunningDialTimeout), first.by, "the dial before the poll is never cut short")
	if tc.startStall >= pollEndsWait {
		require.True(t, first.ended.After(r.until), "the stalled start reaches the first claim after until, as on the hosted runner")
	}
	// The call's first claim follows that dial at once. Unless the daemon had freed the claim by
	// then, the call found it in flight there, and the poll for that daemon began.
	freeAtFirstClaim := tc.freeBefore > 0 && !r.freedAt.After(first.ended)
	var inFlightEnd time.Time
	if !freeAtFirstClaim {
		inFlightEnd = r.end(first.ended)
	}

	// The dial after this call's own claim is the first made while spawn.lock held its stamp.
	claimDial := -1
	for i, d := range r.dials {
		if d.lock == r.ownStamp {
			claimDial = i
			break
		}
	}
	if tc.freeBefore == 0 {
		assert.Equal(t, -1, claimDial, "another spawner's claim held to the end is never taken")
	}
	if claimDial >= 0 {
		d := r.dials[claimDial]
		whole := d.made.Add(spawnClaimDialTimeout)
		if inFlightEnd.IsZero() {
			assert.Equal(t, whole, d.by, "a dial after a claim taken before the poll began is never cut short")
		} else {
			assert.Equal(t, earlier(whole, inFlightEnd), d.by, "the dial after a claim ends where the wait does")
			if d.by.Before(whole) {
				assert.False(t, r.spawned, "a dial cut short at the end of the wait is no licence to spawn")
			}
			if !d.ended.Before(inFlightEnd) {
				assert.False(t, r.spawned, "a dial that returned at or after the end of the wait is no licence to spawn")
			}
		}
	}

	assert.Equal(t, tc.wantSpawn, r.spawned, "whether the call spawned")
	if !tc.wantSpawn {
		assert.Empty(t, r.spawnAt, "nothing is spawned while a daemon may be up and slow to accept")
	} else if assert.Len(t, r.spawnAt, 1, "the call spawns once") &&
		assert.GreaterOrEqual(t, claimDial, 0, "a spawn follows a claim") {
		assert.Equal(t, r.dials[claimDial].ended, r.spawnAt[0], "the spawn follows the dial after the claim that found nothing")
	}
	var spawnEnd time.Time
	if r.spawned && len(r.spawnAt) == 1 {
		spawnEnd = r.end(r.spawnAt[0])
	}

	// Every dial of the poll is bounded by its own timeout or the end of the wait it was made in,
	// whichever is earlier: the wait for another spawner's daemon, or the wait this call's own spawn
	// began.
	var past, wrong []string
	pollDials := 0
	for i := 1; i < len(r.dials); i++ {
		if i == claimDial {
			continue
		}
		d := r.dials[i]
		var end time.Time
		switch {
		case !spawnEnd.IsZero() && !d.made.Before(r.spawnAt[0]):
			end = spawnEnd
		case !inFlightEnd.IsZero():
			end = inFlightEnd
		default:
			t.Errorf("dial %d of %d was made before the poll began and is not the dial after a claim", i, len(r.dials)-1)
			continue
		}
		pollDials++
		if d.by.After(end) {
			past = append(past, fmt.Sprintf("dial %d of %d: %s past", i, len(r.dials)-1, d.by.Sub(end)))
		}
		if want := earlier(d.made.Add(ensureRunningDialTimeout), end); !d.by.Equal(want) {
			wrong = append(wrong, fmt.Sprintf("dial %d of %d: bounded %s after it was made, want %s",
				i, len(r.dials)-1, d.by.Sub(d.made), want.Sub(d.made)))
		}
	}
	assert.Greater(t, pollDials, 2, "the poll dialled")
	assert.Empty(t, past, "no dial of the poll may be bounded past the end of its wait")
	assert.Empty(t, wrong, "each dial of the poll is bounded by its own timeout or the end of its wait, whichever is earlier")

	// The call returns at the end of its wait, or, when a dial returned after it, as that dial
	// returns: nothing it waits for, neither a dial nor the wait for a tick, runs past the end.
	finalEnd := inFlightEnd
	if !spawnEnd.IsZero() {
		finalEnd = spawnEnd
	}
	last := r.dials[len(r.dials)-1]
	want := finalEnd
	if last.ended.After(want) {
		want = last.ended
	}
	assert.Equal(t, want, r.returned, "the call returned %s after the end of its wait", r.returned.Sub(finalEnd))

	switch {
	case tc.freeBefore == 0:
		assert.Equal(t, r.otherStamp, readSpawnLockStamp(r.lockPath), "another spawner's claim is left alone")
	case r.spawned:
		assert.Equal(t, r.ownStamp, readSpawnLockStamp(r.lockPath),
			"the claim stays for the daemon this call started, which removes it once it listens")
	default:
		assert.NoFileExists(t, r.lockPath, "the claim taken on the freed lock is given back")
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
