package daemon

import (
	"math"
	"sort"
	"sync"
	"time"
)

// sampleWindow is §2.4's "rolling 512-sample HDR histogram per hook": the fixed ring size a
// breachDetector closes a window over.
const sampleWindow = 512 //nomagic:allow §2.4 "rolling 512-sample HDR histogram per hook"

// hotPathTailAllowance is the documented, deliberately-over-counting estimate of the client tail
// B-A's own clock cannot observe from the daemon side (ACK read + process exit): measured by the
// bench harness on all three platforms to be under 0.4 ms, and fixed here at 1 ms so the fallback
// fires early rather than late (§2.4). It is added to hook.controlled.observed (recvTS - req.TS)
// to estimate hook.controlled, the value the breach detector actually consumes.
const hotPathTailAllowance = 1 * time.Millisecond //nomagic:allow §2.4 estimated client tail, see doc comment

// Transition is what a closed window decided, if anything.
type Transition uint8

const (
	// NoTransition means the window closed without crossing either threshold.
	NoTransition Transition = iota
	// ToSpool means the hot path has breached its budget for `need` consecutive windows: the
	// daemon should degrade to spool submode.
	ToSpool
	// ToSync means a spooling daemon has run `need` consecutive clean windows: it should revert
	// to sync submode.
	ToSync
)

// breachDetector is the §8.1/§2.4 hot-path breach detector: a fixed 512-sample ring: closing a
// window computes its p99 by copy+sort (§2.4 does not ask for a true HDR/streaming percentile,
// and a plain sort over 512 float64-sized samples is cheap enough to run off the ACK path
// unconditionally), compares it to limit, and tracks consecutive breach/clean windows against
// need. It has no notion of "which op" or "which session" — one detector covers every hot-path
// request, matching the daemon-wide hot-path submode in SessionRegistry.
type breachDetector struct {
	mu sync.Mutex

	window [sampleWindow]time.Duration
	n      int

	limit time.Duration // cfg.Runtime.HotPath.BudgetMs
	need  int           // cfg.Runtime.HotPath.BreachWindows (default 3)

	breaches int
	clean    int
}

// newBreachDetector returns a breachDetector gating on limit with need consecutive windows
// required for either transition. need <= 0 falls back to 1, so a misconfigured 0 cannot make
// every single window transition (which dividing-by/comparing-against a need of 0 would).
func newBreachDetector(limit time.Duration, need int) *breachDetector {
	if need <= 0 {
		need = 1
	}
	return &breachDetector{limit: limit, need: need}
}

// Config reports the limit/need this detector actually gates on. limit and need are set once at
// construction and never mutated afterward (a config reload does not currently re-apply them —
// fix round 1, M-5), so reading them needs no lock; the accessor exists so a caller that logs a
// transition (applyHotPathTransition) reports what the detector actually holds rather than
// whatever the live config currently says, which can have drifted since construction.
func (b *breachDetector) Config() (limit time.Duration, need int) {
	return b.limit, b.need
}

// Observe pushes d into the ring. When the ring fills (every sampleWindow-th sample) it closes a
// window: p99 >= limit counts as a breach (resetting the clean streak), otherwise it counts as
// clean (resetting the breach streak). need consecutive breach windows transitions ToSpool; need
// consecutive clean windows transitions ToSync. Both streaks are then reset, so a transition is
// reported exactly once per need-window run, not on every window after the threshold is crossed.
//
// closed reports whether THIS call was the one that closed a window (every sampleWindow-th call)
// — the caller's signal for anything that must run once per window rather than once per sample,
// such as refreshing a cached obs.Registry.CheckBudgets result (fix round 1, I-3: CheckBudgets
// itself is stateful per-call, so calling it on every sample rather than every closed window
// inflated its Windows count by up to sampleWindow times).
func (b *breachDetector) Observe(d time.Duration) (t Transition, closed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.window[b.n] = d
	b.n++
	if b.n < sampleWindow {
		return NoTransition, false
	}
	p99 := percentileDuration(b.window[:], 0.99)
	b.n = 0

	if p99 >= b.limit {
		b.breaches++
		b.clean = 0
	} else {
		b.clean++
		b.breaches = 0
	}

	if b.breaches >= b.need {
		b.breaches = 0
		return ToSpool, true
	}
	if b.clean >= b.need {
		b.clean = 0
		return ToSync, true
	}
	return NoTransition, true
}

// Reset clears the ring and both streaks without changing limit/need — used when a new session
// resets the daemon-wide hot-path submode (registry.Ensure's own reset, task-3) so stale samples
// from a previous, unrelated session can never contribute to this session's first window.
func (b *breachDetector) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.n = 0
	b.breaches = 0
	b.clean = 0
}

// percentileDuration returns the p-th (0..1) percentile of window by copying and sorting it —
// window itself is never mutated, so the caller's ring stays valid for the next fill. idx uses
// the same ceiling nearest-rank rule task-5-spec.md's own pseudocode does:
// int(math.Ceil(p*len))-1, clamped into range.
func percentileDuration(window []time.Duration, p float64) time.Duration {
	cp := append([]time.Duration(nil), window...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(math.Ceil(p*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}
