package observer

import (
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
)

// The UserPromptSubmit half of L0 (§8.1 items 6 and 7). Only the thrash-warning collector and its
// drain live here so far: PostToolUse is where a high-multiplicity nonterminal becomes visible,
// but PostToolUse has no channel to say anything through, so the warning is queued on the session
// and emitted by the next user prompt — the one hook whose additionalContext reaches the
// transcript. OnUserPrompt itself, and the verbatim capture of §8.1 item 7, land in a later commit
// of this subplan.

// thrashAdvice is the suggestion every thrash warning ends with. It is deliberately one clause: a
// loop warning that argues with the agent costs more tokens than the loop it is reporting.
const thrashAdvice = "consider a different approach"

// collectThrash queues one warning per newly-thrashing grammar rule.
//
// The dedup is per rule id and per session, in st.WarnedRules, because Sequitur reports a rule for
// as long as it stays above the multiplicity threshold: without it, one loop would produce a
// warning on every prompt for the rest of the session. WarnedRules is deliberately not persisted
// (see state.go) — the worst failure of losing it is one repeated line.
func (o *observer) collectThrash(st *sessionState) {
	if o.opt.Grammar == nil {
		return
	}
	for _, rule := range o.opt.Grammar.Thrash(thrashMinUses) {
		if st.WarnedRules[rule.ID] {
			continue
		}
		st.WarnedRules[rule.ID] = true
		st.PendingThrash = append(st.PendingThrash, rule)
	}
}

// pendingThrashLines drains the queued warnings into their rendered form and clears the queue.
//
// The rendering goes through grammar.FormatWarning rather than a local Sprintf: §5.11 leaves the
// wording open, SP-01 closed it there, and SP-15 asserts it byte-for-byte, so a second formatter
// in this package would be a second answer to a question that already has one.
func (o *observer) pendingThrashLines(st *sessionState) []string {
	if len(st.PendingThrash) == 0 {
		return nil
	}

	out := make([]string, 0, len(st.PendingThrash))
	for _, rule := range st.PendingThrash {
		out = append(out, grammar.FormatWarning(grammar.Warning{
			Rule:    rule,
			Repeats: rule.Uses,
			Message: thrashAdvice,
			Turns:   []core.TurnIndex{st.Turn},
		}))
	}
	st.PendingThrash = nil
	return out
}
