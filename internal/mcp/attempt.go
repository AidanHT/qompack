package mcp

import (
	"fmt"
	"sort"
	"sync"

	"github.com/qompack/qompack/internal/core"
)

// This file is SP-16 §2's attempt half: a bounded record of what retrieval was actually tried,
// and what came of it.
//
// It exists because of one sentence in the subplan's interface contract — "a retrieval error is
// never 'not tried'" — and that sentence describes a real failure this codebase has already been
// bitten by. Ledger row E06 records the MCP sibling turning a ledger query error into an absent
// answer with a degraded marker; a caller that reads absence acts as though the thing does not
// exist. The same shape recurs one layer up: a retrieval that was attempted and failed must not
// be indistinguishable from one nobody made, or a bounded-attempt policy silently becomes a
// bounded-truth policy.
//
// Three properties are load-bearing.
//
// NOT TRIED and TRIED AND FAILED are different answers. Status returns AttemptNone only when
// nothing was ever recorded for a key. Any recorded attempt — including one that was refused
// before it ran — produces a status that says so, with its omission attached.
//
// A budget that is EXHAUSTED says so. Running out of attempts is a fact about the search, not
// about the thing searched for, so Record reports the refusal and Status carries
// AttemptExhausted rather than falling back to "absent".
//
// A budget that cannot COUNT admits it. A key whose attempts overflowed the tracking cap reports
// AttemptUnknown, because "we stopped keeping track" is exactly the missing telemetry M6-G16-B
// requires to stay visible rather than being rounded down to zero.

// TriggerKind names why a retrieval was attempted (SP-16 §2's trigger list). It is a closed set:
// a trigger this build cannot name is TriggerUnknown, never a blank that reads as "no trigger".
type TriggerKind string

const (
	// TriggerUnknown is the zero value: the caller did not say why. It is recorded as-is rather
	// than guessed at, and counts against the budget like any other.
	TriggerUnknown TriggerKind = ""
	// TriggerChangedContext is a reference whose underlying content changed since it was read.
	TriggerChangedContext TriggerKind = "changed_context"
	// TriggerUnresolvedReference is a reference the model used that resolves to nothing held.
	TriggerUnresolvedReference TriggerKind = "unresolved_reference"
	// TriggerApplicableElimination is an elimination whose scope and applicability gate passed.
	TriggerApplicableElimination TriggerKind = "applicable_elimination"
	// TriggerRepeatedError is the same failure recurring across turns.
	TriggerRepeatedError TriggerKind = "repeated_error"
)

// Valid reports whether k is one of the closed trigger kinds. TriggerUnknown is valid: not saying
// why is a legitimate, recordable state, unlike a misspelled reason.
func (k TriggerKind) Valid() bool {
	switch k {
	case TriggerUnknown, TriggerChangedContext, TriggerUnresolvedReference,
		TriggerApplicableElimination, TriggerRepeatedError:
		return true
	default:
		return false
	}
}

// AttemptStatus is what an AttemptBudget knows about one key.
type AttemptStatus uint8

const (
	// AttemptNone means nothing was ever recorded for this key. It is the zero value, and it is
	// the ONLY status that means "not tried".
	AttemptNone AttemptStatus = iota
	// AttemptOK means at least one attempt succeeded.
	AttemptOK
	// AttemptFailed means every recorded attempt failed, and the budget is not yet spent.
	AttemptFailed
	// AttemptExhausted means the per-trigger attempt cap was reached without success. Further
	// attempts are refused; this is a statement about the search, not about the thing sought.
	AttemptExhausted
	// AttemptUnknown means attempts were made but the budget stopped tracking them — the
	// distinct-key cap overflowed. Coverage is missing and must stay visible.
	AttemptUnknown
)

// String returns the human-facing spelling of s, for a burden report and /qompack:status.
func (s AttemptStatus) String() string {
	switch s {
	case AttemptNone:
		return "not-tried"
	case AttemptOK:
		return "ok"
	case AttemptFailed:
		return "failed"
	case AttemptExhausted:
		return "exhausted"
	case AttemptUnknown:
		return "unknown"
	}
	return "unknown"
}

// Tried reports whether s means a retrieval was actually attempted. Every status except
// AttemptNone does — which is the whole distinction this file exists to keep.
func (s AttemptStatus) Tried() bool { return s != AttemptNone }

// maxTrackedKeys bounds how many distinct keys one AttemptBudget remembers.
//
// It is a memory bound, not a policy one, and it is deliberately generous: the budget lives for a
// session and holds a short string plus two counters per key. What matters is that overflowing it
// degrades to AttemptUnknown rather than to silent eviction, so a caller can tell the difference
// between "no attempt" and "we stopped counting".
//
// It is not a config key, and it is not one of §11.6's numbers: nothing a user configures should
// be able to lower the point at which this budget stops being able to tell those two apart.
const maxTrackedKeys = 8192

// keyState is one tracked key's counters.
type keyState struct {
	attempts int
	failures int
	ok       bool
	last     core.Omission
	trigger  TriggerKind
}

// AttemptBudget bounds retrieval attempts and records their outcomes for one session.
//
// It is safe for concurrent use: MCP handlers run inside the daemon, which serves several
// sessions at once, and a budget shared by the handlers of one session is written from whatever
// goroutine happens to be serving a call.
type AttemptBudget struct {
	mu sync.Mutex
	// maxPerKey is runtime.phase7.retrieval.maxAttemptsPerTrigger.
	maxPerKey int
	keys      map[string]*keyState
	// overflowed records that maxTrackedKeys was hit, so every untracked key reports
	// AttemptUnknown instead of AttemptNone.
	overflowed bool
	// refused counts attempts turned away by the cap, for the burden report.
	refused int
}

// NewAttemptBudget returns a budget allowing at most maxAttemptsPerTrigger attempts per key.
//
// A cap below 1 is raised to 1 rather than refused. config.Validate already enforces
// `maxAttemptsPerTrigger >= 1`, so a smaller value reaching here means a caller bypassed
// configuration; making it zero would turn every retrieval into an immediate AttemptExhausted,
// which reports failure for work that was never allowed to happen.
func NewAttemptBudget(maxAttemptsPerTrigger int) *AttemptBudget {
	return &AttemptBudget{
		maxPerKey: max(maxAttemptsPerTrigger, 1),
		keys:      map[string]*keyState{},
	}
}

// Allow reports whether another attempt on key is within budget, without recording one.
//
// A caller that is about to do expensive work asks first; a caller that has already done it calls
// Record. Both consult the same counter, so the two orders agree.
func (b *AttemptBudget) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.keys[key]
	if !ok {
		// An untracked key is allowed only while there is room to track it. Allowing one the
		// budget could not then record would spend real work on an attempt whose outcome nothing
		// would remember.
		return len(b.keys) < maxTrackedKeys
	}
	return !st.ok && st.attempts < b.maxPerKey
}

// Record notes one attempt on key and returns the key's status afterwards.
//
// outcome is SP-20's closed vocabulary: core.OutcomeOK is the only success. Anything else — a
// denial, a fault, an expiry — is a FAILED attempt, and why is kept in the omission so a burden
// report can say what went wrong rather than only how often.
//
// An attempt made past the cap is still recorded as refused and counted in the burden, because a
// caller that keeps asking is itself a signal; what it does not do is spend more budget.
func (b *AttemptBudget) Record(key string, trigger TriggerKind, outcome core.EvidenceOutcome, why core.Omission) AttemptStatus {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, ok := b.keys[key]
	if !ok {
		if len(b.keys) >= maxTrackedKeys {
			b.overflowed = true
			b.refused++
			return AttemptUnknown
		}
		st = &keyState{trigger: trigger}
		b.keys[key] = st
	}
	if st.ok {
		return AttemptOK
	}
	if st.attempts >= b.maxPerKey {
		b.refused++
		return AttemptExhausted
	}

	st.attempts++
	if outcome == core.OutcomeOK {
		st.ok = true
		st.last = core.Omission{}
		return AttemptOK
	}
	st.failures++
	st.last = why
	if st.last.Reason == "" {
		st.last = core.Omission{
			Reason:   fmt.Sprintf("retrieval reported %q", string(outcome)),
			Recovery: "resolve the outcome; a failed attempt is not an absent result",
		}
	}
	if st.attempts >= b.maxPerKey {
		return AttemptExhausted
	}
	return AttemptFailed
}

// Status reports what is known about key without recording anything.
//
// A key that was never seen reports AttemptNone only when the budget never overflowed. Once it
// has, an unseen key is AttemptUnknown: the budget can no longer tell "nobody asked" from "we
// stopped writing it down", and reporting the stronger of the two would be a claim it cannot back.
func (b *AttemptBudget) Status(key string) (AttemptStatus, core.Omission) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st, ok := b.keys[key]
	if !ok {
		if b.overflowed {
			return AttemptUnknown, core.Omission{
				Reason:   "attempt tracking overflowed; this key's history is unknown",
				Recovery: "treat as unverified rather than untried; narrow the trigger set",
			}
		}
		return AttemptNone, core.Omission{}
	}
	switch {
	case st.ok:
		return AttemptOK, core.Omission{}
	case st.attempts >= b.maxPerKey:
		return AttemptExhausted, st.last
	default:
		return AttemptFailed, st.last
	}
}

// AttemptReport is the burden report M6-G16-B asks for: how much retrieval was attempted, how much
// of it failed, and what could not be tracked at all.
type AttemptReport struct {
	// Keys is how many distinct keys were tracked.
	Keys int
	// Attempts is the total number of attempts recorded.
	Attempts int
	// Failures is how many of those attempts did not succeed.
	Failures int
	// Refused is how many attempts were turned away by the cap or by tracking overflow. It is
	// counted separately from Attempts: a refusal spends no budget and retrieves nothing, but a
	// caller that keeps generating them is a signal in its own right.
	Refused int
	// Exhausted lists, sorted, the keys whose budget ran out without success.
	Exhausted []string
	// TrackingOverflowed reports that maxTrackedKeys was reached, so Keys, Attempts and Failures
	// UNDERCOUNT and some keys report AttemptUnknown. This is the missing-telemetry flag: it is
	// reported rather than smoothed over.
	TrackingOverflowed bool
	// ByTrigger counts attempts against the trigger that first opened each key, so a report can
	// say WHICH trigger is generating the burden rather than only how large it is. A trigger set
	// that is mostly repeated_error is a different problem from one that is mostly
	// changed_context, and M6-G16-B's "usefulness distinct from frequency" needs the split.
	ByTrigger map[TriggerKind]int
}

// Report returns the current burden report.
func (b *AttemptBudget) Report() AttemptReport {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := AttemptReport{
		Keys:               len(b.keys),
		Refused:            b.refused,
		TrackingOverflowed: b.overflowed,
		ByTrigger:          map[TriggerKind]int{},
	}
	for k, st := range b.keys {
		out.Attempts += st.attempts
		out.Failures += st.failures
		out.ByTrigger[st.trigger] += st.attempts
		if !st.ok && st.attempts >= b.maxPerKey {
			out.Exhausted = append(out.Exhausted, k)
		}
	}
	sort.Strings(out.Exhausted)
	return out
}
