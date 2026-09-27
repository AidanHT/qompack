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

// hookBudget is one hook invocation's share-out of its manifest timeout (D17b, D21), every instant
// counted from doHook's first statement:
//
//   - doneBy is when the hook's own work has to end: the timeout less hookExitReserve;
//   - preSendBy is the last instant a dial still leaves the full reply deadline before doneBy;
//   - borrowBy is the last instant the find/start step (session-start's EnsureRunningUntil
//     poll) may still be waiting for a daemon on its way. It may borrow the reply wait's idle
//     time past preSendBy, but only so far that the reply keeps minReply plus the dial (D21: D9's
//     compact bound, so a compaction's daemon that takes its whole bound to answer is still heard);
//   - latestPoll is the last instant any pre-send step may still be waiting: past it not even the
//     dial fits before doneBy. A daemon started late gets its classic wait up to there.
//
// The zero hookBudget bounds nothing: a hook with no hostTimeout keeps its fixed deadlines.
type hookBudget struct {
	preSendBy  time.Time
	borrowBy   time.Time
	latestPoll time.Time
	doneBy     time.Time
}

// newHookBudget shares hostTimeout out from began: hookExitReserve at the end, before it the reply
// deadline and the dial, and what is left before those — hostTimeout - hookExitReserve - reply -
// connect — for everything up to and including preSend when the reply is to keep its full deadline.
// minReply is the least reply wait the find/start step's borrowing must leave; borrowBy is then
// doneBy - connect - minReply. A minReply <= 0, or one no shorter than reply, lends nothing:
// borrowBy is preSendBy. A hostTimeout <= 0 bounds nothing.
func newHookBudget(began time.Time, hostTimeout, reply, connect, minReply time.Duration) hookBudget {
	if hostTimeout <= 0 {
		return hookBudget{}
	}
	doneBy := began.Add(hostTimeout - hookExitReserve)
	preSendBy := doneBy.Add(-reply - connect)
	borrowBy := preSendBy
	if minReply > 0 && minReply < reply {
		borrowBy = doneBy.Add(-minReply - connect)
	}
	return hookBudget{preSendBy: preSendBy, borrowBy: borrowBy, latestPoll: doneBy.Add(-connect), doneBy: doneBy}
}

// replyDeadline is how long a hook that dials at now may wait for its answer: reply, or what is
// left before doneBy once the dial's own bound is taken off, whichever is shorter. It is reply
// itself whenever preSend returned by preSendBy, and shorter when the find/start step borrowed idle
// reply time or the steps before the dial ran over — staging a binary and the admission ahead of
// preSend are never cut short. A find/start step that kept to borrowBy leaves minReply less only
// the time from its return to now: its poll ends at borrowBy, and no tick or dial of the poll runs
// past it (daemon.EnsureRunningUntil). A result at or below zero means no answer can be waited for.
func (b hookBudget) replyDeadline(now time.Time, reply, connect time.Duration) time.Duration {
	if b.doneBy.IsZero() {
		return reply
	}
	return min(reply, b.doneBy.Sub(now)-connect)
}
