package cli

import "time"

// hookExitReserve is the part of a hook's manifest timeout doHook keeps for what its own clock does
// not see (V6 close-out D17b): before its first statement, the host spawning the process and the
// runtime starting; after its answer, the output write, the logger and spool closes and the process
// exit. The host's timeout runs over all of it.
//
// It is 1.5 s. Measured under full CPU co-load on Windows (plans/sdd/V6-closeout/w5-coldstart/runs,
// the cold-start diagnostic), the session-start process's wall time minus its own first-to-last
// statement span was p50 0.06-0.6 s and at most 1.45 s: the first execution of a freshly written
// binary, which the OS scans before it runs. The reserve covers that maximum. Too small, and a hook
// that waited out its whole reply deadline can still be cancelled by the host, losing the answer;
// too large, and the pre-send step's budget, which is what is left, shrinks for nothing.
const hookExitReserve = 1500 * time.Millisecond

// hookBudget is one hook invocation's share-out of its manifest timeout (D17b). doneBy is the
// instant the hook's own work has to end by — the timeout less hookExitReserve, counted from
// doHook's first statement — and preSendBy the instant preSend has to return by for the dial and the
// full reply deadline to still fit before doneBy. The zero hookBudget bounds nothing: a hook with no
// hostTimeout keeps its fixed deadlines.
type hookBudget struct {
	preSendBy time.Time
	doneBy    time.Time
}

// newHookBudget shares hostTimeout out from began: hookExitReserve at the end, before it the reply
// deadline and the dial, and what is left before those — hostTimeout - hookExitReserve - reply -
// connect — for everything up to and including preSend. A hostTimeout <= 0 bounds nothing.
func newHookBudget(began time.Time, hostTimeout, reply, connect time.Duration) hookBudget {
	if hostTimeout <= 0 {
		return hookBudget{}
	}
	doneBy := began.Add(hostTimeout - hookExitReserve)
	return hookBudget{preSendBy: doneBy.Add(-reply - connect), doneBy: doneBy}
}

// replyDeadline is how long a hook that dials at now may wait for its answer: reply, or what is
// left before doneBy once the dial's own bound is taken off, whichever is shorter. It is reply
// itself whenever preSend kept to preSendBy, and shorter only when the steps before the dial ran
// over — staging a binary and the admission ahead of preSend are never cut short. A result at or
// below zero means no answer can be waited for at all.
func (b hookBudget) replyDeadline(now time.Time, reply, connect time.Duration) time.Duration {
	if b.doneBy.IsZero() {
		return reply
	}
	return min(reply, b.doneBy.Sub(now)-connect)
}
