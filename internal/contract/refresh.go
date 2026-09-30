package contract

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// This file keeps the contract snapshot a status page shows current between SessionStarts.
//
// The monitor evaluates the nine assertions at SessionStart (monitor.go), and two of them observe
// something the host only does after that start: it connects the MCP server beside the session's
// first start, and the probe the start minted reaches the transcript only with the session's first
// prompt. The start therefore reads mcp.server_registered initialize-pending and
// hook.additional_context_delivered not-yet-observed, and until the next start nothing changed that
// reading, although the daemon records both observations in state/history.json the moment they
// happen (the `mcp` op's handshake record, the UserPromptSubmit sentinel scan). The candidate 4 live
// re-run read exactly that contradiction in UAT-01, under a banner calling all nine rows holding
// (D50). RefreshFromHistory and RefreshObservation let a reader — status, doctor — apply what
// history.json already records; StandingOf is how a banner counts the result.

// historyRead names the assertions whose observation state/history.json records as it happens.
// SessionHistory.MCPInitialized and SentinelState.Observed never reset to false, so once either is
// set the check reads the same observation at every later start. SentinelState.Chances counts the
// session's prompts that missed the probe its start minted; once it reaches two the check reads the
// failure the next start will report. Three things reset it: a start's mint, the withdrawal of a
// lost start's probe (the daemon's withdrawLostStartAnswer), and a scan that finds the probe.
var historyRead = []struct {
	id    ID
	check func(context.Context, Env) Result
}{
	{CMCPRegistered, checkMCPServerRegistered},
	{CAdditionalContext, checkAdditionalContextDelivered},
}

// declaredSeverity is the severity StandardAssertions declares for id, which gated gives a failing
// row whose check set none; SevInfo for an id it does not list.
func declaredSeverity(id ID) Severity {
	for _, a := range StandardAssertions() {
		if a.ID == id {
			return a.Severity
		}
	}
	return SevInfo
}

// refreshedFailureDetail is the Detail a refreshed failing row carries: a read is not a
// SessionStart, so it changes no mode, and the row says so beside its severity.
const refreshedFailureDetail = "read from state/history.json after this session's start; " +
	"the next SessionStart evaluates it and applies it to the mode"

// readByHistory returns the row h settles for assertion id — what id's check reports reading h now,
// stamped by clk (nil reads the system clock) — or false when id is not one historyRead names or h
// still leaves it pending. A settled row is an observation (holding) or a failure: two missed
// chances for the probe. The row is the check's own, with the declared severity gated gives a
// failure at a start, so a refresh and the next SessionStart can never spell one reading
// differently; a failure also carries refreshedFailureDetail.
//
// The check runs against a copy holding only the fields the two checks read for their verdict, so h
// is never mutated: the MCP check clears its await record when it reads a handshake, which is
// SessionStart's business, and with no session in its Env it can only read the handshake or pending.
// Producers are not consulted: a caller refreshes only a row whose check already ran (a pending row,
// a not_observed observation), which is itself the proof that its producer was declared where it
// ran.
func readByHistory(id ID, h *SessionHistory, clk core.Clock) (Result, bool) {
	if h == nil {
		return Result{}, false
	}
	for _, p := range historyRead {
		if p.id != id {
			continue
		}
		probe := &SessionHistory{
			MCPInitialized: h.MCPInitialized,
			Sentinel:       SentinelState{Observed: h.Sentinel.Observed, Chances: h.Sentinel.Chances},
		}
		r := p.check(context.Background(), Env{Clock: clk, History: probe})
		r.ID = id
		switch StandingOf(r) {
		case StandingHolding:
			return r, true
		case StandingFailing:
			if r.Severity == SevInfo {
				r.Severity = declaredSeverity(id)
			}
			r.Detail = refreshedFailureDetail
			return r, true
		default:
			return Result{}, false
		}
	}
	return Result{}, false
}

// RefreshFromHistory returns a copy of results in which every row still waiting for its
// observation (StandingPending) that h now settles is replaced by what h records: the observation,
// or the probe's failure once its two chances are spent. Nothing else changes: a failing row stays
// failing until the next SessionStart re-evaluates it, and a row with nothing to judge —
// not-yet-implemented included — or already holding is left as it was. results itself is not
// modified, and no mode changes: only a SessionStart applies a failure to it.
func RefreshFromHistory(results []Result, h *SessionHistory, clk core.Clock) []Result {
	out := append([]Result(nil), results...)
	for i, r := range out {
		if StandingOf(r) != StandingPending {
			continue
		}
		if p, ok := readByHistory(r.ID, h, clk); ok {
			out[i] = p
		}
	}
	return out
}

// RefreshObservation is RefreshFromHistory for one observation-ledger entry, which doctor reads as
// a capability's newest word: a not_observed entry whose Observed is pending and which h now settles
// is returned as what h records — the observation, or a failed outcome once the probe's chances are
// spent; outcome, Observed and coverage re-derived by ObservationsOf's own rules under reg, stamped
// by clk — with its target and scope kept. It reports
// whether it changed anything. The ledger on disk is never rewritten: it keeps one run per
// SessionStart (V5-VERIFY §4.14).
func RefreshObservation(o Observation, h *SessionHistory, reg CapabilityRegister, clk core.Clock) (Observation, bool) {
	if o.Outcome != OutcomeNotObserved || !pendingSpellings[o.Observed] {
		return o, false
	}
	r, ok := readByHistory(o.ID, h, clk)
	if !ok {
		return o, false
	}
	fresh := ObservationsOf([]Result{r}, reg, o.Target, o.Scope)[0]
	fresh.LocalAttempt, fresh.Artifact = o.LocalAttempt, o.Artifact
	return fresh, true
}

// Standing is how a status banner counts one Result. A Result's OK says only that it is not a
// failure; troubleshooting.md §1 says it plainly — an OK row whose Observed means "nothing was
// seen" is "no assertion was made", not "the host contract holds".
type Standing int

// The four standings.
const (
	// StandingHolding: OK, and Observed reports something actually seen.
	StandingHolding Standing = iota
	// StandingPending: OK only because the observation has not arrived yet; the host is expected to
	// supply it later in the session or by the next start (pendingSpellings).
	StandingPending
	// StandingIdle: OK with nothing to judge — no compaction to check, no timeout known, a retired
	// mechanism, a producer absent from this build. Not pending and not holding.
	StandingIdle
	// StandingFailing: not OK.
	StandingFailing
)

// pendingSpellings is the subset of noObservationSpellings that means "the observation is due and
// has not arrived yet", as opposed to "there is nothing here to observe". Each names its check:
//
//	not-yet-observed    — checkAdditionalContextDelivered: the probe has had fewer than two chances
//	initialize-pending  — checkMCPServerRegistered: the MCP handshake has not reached the daemon
//	transcript-pending  — checkTranscriptReadable: the host has not written the transcript yet
//	marker-absent-once  — checkSessionStartFires: one start without a marker; the next start decides
//
// TestStandingVocabulary_PendingIsNothingSeen pins that every entry is also a "nothing was seen"
// spelling, so a pending row can never be classified as an observation.
var pendingSpellings = map[string]bool{
	"not-yet-observed":   true,
	"initialize-pending": true,
	"transcript-pending": true,
	"marker-absent-once": true,
}

// StandingOf reports how r counts in a status banner.
func StandingOf(r Result) Standing {
	switch {
	case !r.OK:
		return StandingFailing
	case pendingSpellings[r.Observed]:
		return StandingPending
	case r.Observed == notYetImplementedObserved, noObservationSpellings[r.Observed]:
		return StandingIdle
	default:
		return StandingHolding
	}
}
