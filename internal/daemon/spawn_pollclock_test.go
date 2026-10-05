package daemon

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// stepPollClock is a pollClock on which time moves only when a row's stand-in dial, or the poll's
// wait for its next tick, moves it. A row on it decides exactly how long every step of
// ensureRunningWith takes, so its verdict never depends on how fast a loaded machine schedules the
// call (D61(c)): the hosted windows-latest runner of ci.yml run 37229942287 took about 1.4 s to get
// TestEnsureRunningUntil_ThePollEndsAtItsDeadline's call to its first claim, past the instant the
// row had given its poll as the deadline, and the row read the product's correct answer to that as
// a failure. What a row schedules on the clock (at) runs, in order, in the goroutine that moves the
// clock past it, at its own instant: the poll's goroutine, in the middle of a dial or a tick wait.
// The D17 cold-start rows in spawn_lock_test.go run on it too, with real listeners and real dials
// (stepProbeBy).
type stepPollClock struct {
	mu     sync.Mutex
	now    time.Time
	events []stepEvent
}

// stepEvent is something a row scheduled on a stepPollClock.
type stepEvent struct {
	at time.Time
	fn func()
}

func newStepPollClock(t0 time.Time) *stepPollClock {
	return &stepPollClock{now: t0}
}

func (c *stepPollClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// at schedules fn for when the clock reaches when.
func (c *stepPollClock) at(when time.Time, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, stepEvent{at: when, fn: fn})
}

// advanceTo moves the clock to when, never backwards, running each event due by then at its own
// instant, earliest first.
func (c *stepPollClock) advanceTo(when time.Time) {
	for {
		c.mu.Lock()
		next := -1
		for i, e := range c.events {
			if !e.at.After(when) && (next < 0 || e.at.Before(c.events[next].at)) {
				next = i
			}
		}
		if next < 0 {
			if when.After(c.now) {
				c.now = when
			}
			c.mu.Unlock()
			return
		}
		e := c.events[next]
		c.events = append(c.events[:next], c.events[next+1:]...)
		if e.at.After(c.now) {
			c.now = e.at
		}
		c.mu.Unlock()
		e.fn()
	}
}

func (c *stepPollClock) NewTicker(d time.Duration) pollTicker {
	return &stepTicker{clock: c, every: d, next: c.Now().Add(d)}
}

// stepTicker is stepPollClock's ticker. Like a time.Ticker it ticks every interval from its start,
// and a tick that came while nobody waited is taken at once, with the ticks missed after it dropped.
// WaitForTick keeps waitForTick's contract (TestWaitForTick_EndsAtTheDeadlineNotTheTick pins the
// production one): it never waits past the deadline, and no dial follows a wait that reached it.
type stepTicker struct {
	clock *stepPollClock
	every time.Duration
	next  time.Time // the next tick not yet taken
}

func (s *stepTicker) WaitForTick(deadline time.Time) bool {
	now := s.clock.Now()
	if !now.Before(deadline) {
		return false
	}
	if s.next.After(now) {
		if !s.next.Before(deadline) {
			s.clock.advanceTo(deadline)
			return false
		}
		s.clock.advanceTo(s.next)
		now = s.clock.Now()
	}
	for !s.next.After(now) {
		s.next = s.next.Add(s.every)
	}
	return now.Before(deadline)
}

func (s *stepTicker) Stop() {}

// stepDial is one dial a stepHungDaemon took: when it was made, the instant it was bounded to,
// when it returned, and the stamp spawn.lock held as it was made ("" when there was none).
type stepDial struct {
	made, by, ended time.Time
	lock            string
}

// stepHungDaemonMaxDials bounds the dials one call may make of a stepHungDaemon. The longest wait
// these rows give is about 2 s of step time, 80 poll intervals, so a call that dials more than this
// is not moving the clock towards its deadline and would otherwise never return.
const stepHungDaemonMaxDials = 10_000

// stepHungDaemon stands in, on a stepPollClock, for a daemon that listens and never accepts: a hung
// one, or on Windows a pipe whose every instance is claimed. Each dial waits out its whole bound and
// fails. stall makes a dial return later than that, as a dial does on a machine that schedules the
// call late; it is given the dial's index and the dial as it was made.
type stepHungDaemon struct {
	t        *testing.T
	clock    *stepPollClock
	lockPath string
	stall    func(i int, d stepDial) time.Duration
	dials    []stepDial
}

func (h *stepHungDaemon) probe(_ ipc.Addr, by time.Time) bool {
	if len(h.dials) >= stepHungDaemonMaxDials {
		h.t.Fatalf("the call made %d dials without reaching the end of its wait", len(h.dials))
	}
	d := stepDial{made: h.clock.Now(), by: by, lock: readSpawnLockStamp(h.lockPath)}
	end := d.made
	if by.After(end) {
		end = by
	}
	if h.stall != nil {
		end = end.Add(h.stall(len(h.dials), d))
	}
	h.clock.advanceTo(end)
	d.ended = h.clock.Now()
	h.dials = append(h.dials, d)
	return false
}

// stepComingDaemon stands in, on a stepPollClock, for the daemon a spawn starts, which comes up at an
// instant the row chooses. Until then a dial fails at once, as one does when no pipe or socket
// exists yet; from then on a dial answers. Coming up, it deletes run/spawn.lock, as Run does once it
// listens. It records every dial and every spawn.
type stepComingDaemon struct {
	t       *testing.T
	clock   *stepPollClock
	root    string
	up      bool
	dials   []stepDial
	spawnAt []time.Time
}

func (d *stepComingDaemon) probe(_ ipc.Addr, by time.Time) bool {
	if len(d.dials) >= stepHungDaemonMaxDials {
		d.t.Fatalf("the call made %d dials without reaching the end of its wait", len(d.dials))
	}
	now := d.clock.Now()
	d.dials = append(d.dials, stepDial{made: now, by: by, ended: now})
	return d.up
}

// spawnUpAfter is a spawner whose daemon comes up after it on the step clock.
func (d *stepComingDaemon) spawnUpAfter(after time.Duration) func(string, string) error {
	return func(string, string) error {
		now := d.clock.Now()
		d.spawnAt = append(d.spawnAt, now)
		d.clock.at(now.Add(after), func() {
			d.up = true
			removeSpawnLockFile(d.root)
		})
		return nil
	}
}

// spawnNever is a spawner whose daemon never comes up.
func (d *stepComingDaemon) spawnNever(string, string) error {
	d.spawnAt = append(d.spawnAt, d.clock.Now())
	return nil
}

// beyondTestTimeout is longer than the -timeout any run of this suite uses (30 m: devtool's
// wholeTreeTestTimeout, ci.yml's and the Linux gate's), so no verdict can depend on a real wait it
// bounds: a run still waiting then has already failed by its binary's own timeout.
const beyondTestTimeout = time.Hour

// stepProbeBy is probeBy on a stepPollClock. With no time left before by on pc it dials nothing and
// reports false, as probeBy does. Otherwise it makes a real dial of the project's address through
// ipc.Probe, the dial production makes, with a real bound no run reaches (beyondTestTimeout), so the
// dial races no budget: a real listener the row has brought up answers it however late the machine
// runs that listener's accept loop or the dial itself (on Windows a pipe whose next instance is not up
// yet is waited on as busy), and with none there it is refused at once, as a missing pipe or socket
// is. Which of the two a dial meets is decided by where the row's step clock stands when it is made,
// never by scheduling (D61(c)). It does not move pc.
func stepProbeBy(pc *stepPollClock) func(addr ipc.Addr, by time.Time) bool {
	return func(addr ipc.Addr, by time.Time) bool {
		if !by.After(pc.Now()) {
			return false
		}
		return ipc.Probe(addr, beyondTestTimeout)
	}
}

// readSpawnLockStamp returns the stamp the spawn.lock at p holds, or "" when there is none.
func readSpawnLockStamp(p string) string {
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return ""
	}
	return string(b)
}
