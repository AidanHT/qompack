package mcp

import (
	"sort"
	"sync"
)

// This file is SP-16 §2's reminder half: the bounded, non-self-amplifying channel through which
// retrieval makes itself visible to a caller.
//
// A reminder is the only part of phase 7 a user actually experiences, which makes it the part
// most able to do harm. §2 asks for three specific protections and this file is all three.
//
// It is CAPPED per session, not per trigger. A bound that reset on every changed reference would
// bound nothing a reader notices: one reminder for each of forty touched files is forty
// reminders. Remaining counts down across the whole session.
//
// It never reminds TWICE about the same thing. Repetition is how a warning stops being read, and
// a second reminder about a key carries no information the first did not.
//
// It never reminds about its OWN output. This is the feedback loop §2 names, and it is not
// hypothetical: reminders and retrieval results become context, changed context is a trigger, and
// a trigger produces reminders. MarkOwnOutput is how the layer that emits something tells the
// budget not to let it come back as a stimulus. The check runs FIRST, before the cap and before
// deduplication, so an own-output reminder never even spends budget.
//
// Suppression is COUNTED, never silent. A session that generated two hundred reminder candidates
// and emitted three is a very different thing from one that generated three, and only the report
// can tell them apart.

// Reminder is one bounded retrieval reminder that a caller may surface.
type Reminder struct {
	// Key identifies what the reminder is about — a content hash, a path, an elimination id.
	Key string
	// Trigger is why it fired.
	Trigger TriggerKind
	// Remaining is how many further reminders this session may emit AFTER this one. It is carried
	// on the reminder itself so a renderer can say "2 of 3" without asking the budget again and
	// racing another handler.
	Remaining int
}

// SuppressReason names why a candidate reminder was not emitted. It is a closed set, and the zero
// value means nothing was suppressed.
type SuppressReason string

const (
	// SuppressNone means the reminder was emitted.
	SuppressNone SuppressReason = ""
	// SuppressOwnOutput means the key names something Qompack itself produced: reminding about it
	// would be the system responding to its own writing.
	SuppressOwnOutput SuppressReason = "own_output"
	// SuppressDuplicate means this session already emitted a reminder for this key.
	SuppressDuplicate SuppressReason = "duplicate"
	// SuppressOverBudget means the session's reminder cap is spent.
	SuppressOverBudget SuppressReason = "over_budget"
)

// ReminderBudget bounds the reminders one session may emit.
//
// Like AttemptBudget it is safe for concurrent use, for the same reason: the handlers that offer
// reminders run inside the daemon on whatever goroutine is serving the call.
type ReminderBudget struct {
	mu sync.Mutex
	// limit is runtime.phase7.retrieval.maxRemindersPerSession. Zero is a legitimate setting and
	// means "emit none".
	limit   int
	emitted map[string]struct{}
	own     map[string]struct{}
	// order is the emission order, for a report that wants to show what a session actually said.
	order      []Reminder
	suppressed map[SuppressReason]int
}

// NewReminderBudget returns a budget allowing at most maxPerSession reminders.
//
// A negative cap becomes zero — emit none. Unlike NewAttemptBudget's floor of 1, zero is NOT
// raised here: config.Validate admits `maxRemindersPerSession >= 0` precisely so a user can turn
// the channel off while leaving the rest of phase 7 on, and silently emitting one anyway would
// override that.
func NewReminderBudget(maxPerSession int) *ReminderBudget {
	return &ReminderBudget{
		limit:      max(maxPerSession, 0),
		emitted:    map[string]struct{}{},
		own:        map[string]struct{}{},
		suppressed: map[SuppressReason]int{},
	}
}

// MarkOwnOutput records that key names something Qompack itself emitted, so it can never become a
// reminder stimulus.
//
// It is deliberately a separate call rather than a flag on Offer: the layer that KNOWS a piece of
// content is Qompack's own — the rehydrator writing an injection, the MCP handler returning a
// span — is not the layer that later notices the content changed. Marking at production time is
// what makes the loop impossible to close by accident at consumption time.
func (r *ReminderBudget) MarkOwnOutput(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.own[key] = struct{}{}
}

// Offer asks for a reminder about key, and reports whether one may be emitted.
//
// The order of the three checks is the specification. Own-output first, so the loop is cut before
// anything else is considered and without spending budget; then duplication, so a repeat costs
// nothing either; then the cap. Only a candidate that passes all three consumes one of the
// session's reminders.
//
// A suppressed offer returns the zero Reminder and the reason, and every reason is counted.
func (r *ReminderBudget) Offer(key string, trigger TriggerKind) (Reminder, SuppressReason) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, own := r.own[key]; own {
		r.suppressed[SuppressOwnOutput]++
		return Reminder{}, SuppressOwnOutput
	}
	if _, dup := r.emitted[key]; dup {
		r.suppressed[SuppressDuplicate]++
		return Reminder{}, SuppressDuplicate
	}
	if len(r.emitted) >= r.limit {
		r.suppressed[SuppressOverBudget]++
		return Reminder{}, SuppressOverBudget
	}

	r.emitted[key] = struct{}{}
	rem := Reminder{Key: key, Trigger: trigger, Remaining: r.limit - len(r.emitted)}
	r.order = append(r.order, rem)
	return rem, SuppressNone
}

// Remaining reports how many further reminders this session may emit.
func (r *ReminderBudget) Remaining() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.limit - len(r.emitted)
}

// ReminderReport is the volume half of M6-G16-B's bounded-retrieval evidence: what a session
// actually said, and everything it decided not to say.
type ReminderReport struct {
	// Limit is the session cap in force.
	Limit int
	// Emitted lists the reminders that were surfaced, in emission order.
	Emitted []Reminder
	// Suppressed counts candidates by why they were withheld. A reason with no candidates is
	// absent rather than zero, so the map reads as a list of what actually happened.
	Suppressed map[SuppressReason]int
	// OwnOutputKeys is how many keys were marked as Qompack's own. It is reported because a
	// suppressed-by-own-output count is only interpretable next to it: three suppressions against
	// three marks is a tight loop, three against three thousand is noise.
	OwnOutputKeys int
}

// TotalSuppressed is how many candidate reminders were withheld for any reason.
func (r ReminderReport) TotalSuppressed() int {
	n := 0
	for _, c := range r.Suppressed {
		n += c
	}
	return n
}

// Report returns the current reminder report. The returned slices and maps are copies, so a
// caller may hold them while the session continues.
func (r *ReminderBudget) Report() ReminderReport {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := ReminderReport{
		Limit:         r.limit,
		Emitted:       append([]Reminder(nil), r.order...),
		Suppressed:    make(map[SuppressReason]int, len(r.suppressed)),
		OwnOutputKeys: len(r.own),
	}
	for k, v := range r.suppressed {
		out.Suppressed[k] = v
	}
	return out
}

// OwnOutput returns every key marked as Qompack's own, sorted. It exists for the loop-audit half
// of M6-G16-B: a reviewer checking that the marks cover what the system actually emitted needs to
// see them, not just their count.
func (r *ReminderBudget) OwnOutput() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.own))
	for k := range r.own {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
