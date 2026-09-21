package observer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// The UserPromptSubmit half of L0: §8.1 item 7's verbatim capture (G2.3) and §8.1 item 6's thrash
// warning. The two share a file because they share the hook: UserPromptSubmit is the ONLY hook
// this package emits through, so the warning PostToolUse queued has nowhere else to surface.

// userPromptSubmit is the one string this package spells for the UserPromptSubmit hook. It is the
// PutOptions.Tool and ToolUseRecord.Tool a prompt is filed under AND the HSO.HookEventName a
// thrash warning is stamped with, because the host names the hook and the pseudo-tool identically
// — and additionalContext reaches the transcript only when the event name matches the hook that
// produced it.
const userPromptSubmit = "UserPromptSubmit"

// promptSymbol is both the action-grammar symbol and the feature-window tool name one user turn
// contributes. §8.1 item 6's action stream and §6.6's tool-shift feature both read it and they
// must agree: a prompt spelled one way in the grammar and another in the window would make a
// distribution spike out of every user turn.
const promptSymbol = "user"

// promptIDPrefix begins every VerbatimPromptID.
const promptIDPrefix = "prompt"

// stagePromptPut is the soft-failure stage the verbatim Put reports under. It is deliberately
// distinct from stagePut: a lost user prompt is a G2.3 failure and must be countable on its own,
// not averaged into the tool path's much larger observer.err.put.
const stagePromptPut = "prompt.put"

// thrashLineSep joins several drained warnings into one additionalContext block.
const thrashLineSep = "\n"

// thrashAdvice is the suggestion every thrash warning ends with. It is deliberately one clause: a
// loop warning that argues with the agent costs more tokens than the loop it is reporting.
const thrashAdvice = "consider a different approach"

// OnUserPrompt captures the user's own words verbatim and immutably (§8.1 item 7, G2.3) and is the
// one entry point permitted to say something back — a thrash warning, and only in ModeFull.
func (o *observer) OnUserPrompt(ctx context.Context, e Event) (Output, error) {
	var out Output
	err := o.timed(histPrompt, func() error {
		var err error
		out, err = o.onUserPrompt(ctx, e)
		return err
	})
	return out, err
}

// onUserPrompt has two paths, split by the SP08-D3 reply-only context marker (Option A):
//
//   - The LIVE REPLY path (promptReplyOnly): drain the pending thrash warning under the session
//     lock and return it. It records NOTHING and advances NO turn — the authoritative verbatim
//     capture is the worker/replay's, so a prompt no daemon captured live is still captured, and a
//     later live prompt can no longer take a turn 0 the replay was supposed to fill.
//   - The WORKER / DRAIN-REPLAY path (normal context, carrying the leased observation identity):
//     resolved decision 3's three durable artifacts followed by decision 4's turn bookkeeping,
//     made idempotent under the leased identity exactly as the tool path is (SP08-D2). A recognized
//     redelivery is absorbed; a fresh capture records, links its observation reference, and closes
//     the turn.
//
// It returns an error for ctx.Err() and — on the worker/replay path for a LEASED delivery — for a
// lost required write (ErrUnpublished), so the frontier is never acknowledged over a capture that
// did not become durable. An unleased in-process caller keeps the base behaviour: every I/O failure
// is absorbed by soft and the session moves on (decision 7).
func (o *observer) onUserPrompt(ctx context.Context, e Event) (Output, error) {
	// 1. Nothing before the ctx check, and the clock is read exactly once (decision 11).
	if err := ctx.Err(); err != nil {
		return hookio.Empty(), err
	}
	// An empty prompt is not a user turn. Capturing it would mint an object, an index entry and a
	// graph node for no content, and — worse — would advance the turn counter past a turn that
	// never happened, which every stored artifact downstream is numbered against. This holds on
	// both paths: an empty prompt is neither a warning to show nor a capture to make.
	if e.Prompt == "" {
		return hookio.Empty(), nil
	}
	st := o.session(e.SessionID)
	st.mu.Lock()
	defer st.mu.Unlock()

	// The live reply path's whole job is the synchronous warning; it records nothing (Option A).
	if promptReplyOnly(ctx) {
		return o.promptReplyOutput(st), nil
	}

	now := o.now()

	// Recognition: a redelivery this observation already published is absorbed — no second object,
	// no second record, no re-run turn bookkeeping — and the session is moved past it. This is the
	// SP08-D2 identity rule, now reached for prompts too.
	obs := ObservationFrom(ctx)
	if rec, ok := o.observationRecord(ctx, obs, e.SessionID, opObservePrompt); ok {
		if err := o.syncPrompt(ctx, rec.Root); err != nil {
			return hookio.Empty(), err
		}
		adoptTurn(st, rec)
		o.count(counterRedelivery)
		return hookio.Empty(), nil
	}

	// 2-3. Artifact (a): the user's bytes, content-addressed. verbatimOptions is the whole of what
	//      makes this capture verbatim, and KeepRaw is what keeps the canonicalizer's deltas so
	//      canon.Restore can reconstruct the pre-normalization bytes exactly.
	body := []byte(e.Prompt)
	res, err := o.opt.Store.PutBytes(ctx, body, store.PutOptions{
		Tool: userPromptSubmit, Path: "", Canon: verbatimOptions(),
		KeepRaw: true, Ephemeral: false,
	})
	if err != nil {
		// A leased delivery must not be frontier-acknowledged over a lost capture: propagate so the
		// daemon keeps it pending and a later drain re-runs it (SP08-D3). An unleased in-process
		// caller has no frontier, so it keeps the base behaviour — the capture is lost, the TURN is
		// not, and renumbering every later artifact around it would be the larger corruption.
		if obs != "" {
			return hookio.Empty(), o.unpublished(stagePromptPut)
		}
		o.soft(stagePromptPut, err)
	} else {
		if recovered, err := o.recoverPrompt(ctx, st, e, obs); err != nil || recovered {
			return hookio.Empty(), err
		}
		if recErr := o.recordPromptDurable(ctx, st, e, res, body, now, obs); recErr != nil {
			return hookio.Empty(), recErr
		}
	}

	// 6. §8.1 item 6, the user half of the action stream.
	if o.opt.Grammar != nil {
		o.opt.Grammar.Append(grammar.Symbol(promptSymbol))
	}

	// 7. Decision 4: the prompt was recorded AT Turn, and this increment closes the user turn. The
	//    subagent cursor moves with it because a SubagentStop reports on what has happened since
	//    the request that provoked it, not since the start of the session.
	st.LastPromptTurn = st.Turn
	st.SubagentSince = len(st.ToolUses)
	st.Turn++

	// 8. The §6.6 features. LastTS is assigned AFTER features() has run: it is the PREVIOUS
	//    event's timestamp, and assigning it earlier would make GapSeconds identically zero.
	o.recordRecent(st, promptSymbol, nil, body, now)
	if fs, ok := o.features(st, now); ok && o.opt.OnFeatures != nil {
		o.opt.OnFeatures(e.SessionID, fs)
	}
	st.LastTS = now

	// Preserve the direct observer API. Only the explicitly marked worker path
	// leaves warnings queued for the live reply.
	if !promptCaptureOnly(ctx) {
		return o.promptReplyOutput(st), nil
	}
	return hookio.Empty(), nil
}

// The durable artifacts of resolved decision 3 (index record, observation-reference link, DAG node)
// now live in recordPromptDurable (prompt_delivery.go), which is idempotent under the leased
// observation identity and error-returning for a leased delivery. onUserPrompt's worker/replay path
// calls it; §5b's turn+1 bridge edge and its rationale moved with it.

// VerbatimPromptID is the tool_use identity of one captured prompt: "prompt_<session>_<turn>".
//
// A UserPromptSubmit payload carries no tool_use_id, so the identity has to be derived, and it is
// derived from the only two values that are already unique together. It is exported because
// retrieval and replay reach a captured prompt by this id and must not re-derive the spelling.
func VerbatimPromptID(s core.SessionID, t core.TurnIndex) core.ToolUseID {
	return core.ToolUseID(fmt.Sprintf("%s_%s_%d", promptIDPrefix, s, t))
}

// verbatimOptions is the "no optional class" Put request §8.1 item 7 and §7.3 mean by "verbatim
// and immutably". stop.go's capture blob takes the same treatment for the same reason: it is JSON
// this package generated, not host content.
//
// Strip is EMPTY and NON-NIL, and both halves are load-bearing. canon.gateSet reads a nil Strip as
// "every class" — the documented meaning of the zero canon.Options, and the OPPOSITE request —
// and a non-nil Strip, empty included, as "exactly these, plus the always-on structural ones".
// store.canonOptions reads that same nil-ness as "this entire canon.Options is mine, MinHash
// included", which is the only reason the opt-out below is honoured rather than silently replaced
// by the configured default.
//
// This is the one deliberate exception to resolved decision 2, and it is narrow: a user prompt is
// not file content read on two platforms, so there is no dedup space to fork and nothing to gain
// from stripping timestamps, ANSI, PIDs, addresses, tmp paths or durations out of the thing the
// user typed. Three things it does NOT mean, all structural:
//
//   - crlf and paths still run. They are canon.alwaysOn, Appendix C cannot disable either, and §4
//     makes CRLF→LF normalization a precondition of cross-platform dedup.
//   - redaction still runs. store.PutBytes is redact → canonicalize → chunk and no caller may opt
//     out: a credential pasted into a prompt must not reach objects/ in the clear, where
//     content-addressing makes it undeletable.
//   - no near-duplicate signature is computed, so a prompt is never delta-encoded against a prior
//     one — the "never regenerated" half of G2.3.
func verbatimOptions() canon.Options {
	return canon.Options{
		Strip:   []canon.Class{},
		MinHash: sketch.MinHashOptions{Enabled: false},
	}
}

// promptArgsDoc is the shape store.ArgsDigest digests a prompt as. A prompt has no tool_input, so
// one is synthesized: without it every prompt in every session would collapse onto the single
// empty-args digest, and ArgsPreview — §5.8's ≤120-byte cap — would carry nothing at all.
type promptArgsDoc struct {
	Prompt string `json:"prompt"`
}

// promptArgs renders {"prompt":<text>} for store.ArgsDigest.
func promptArgs(prompt string) json.RawMessage {
	// Marshalling a struct whose only field is a string cannot fail, and ArgsDigest digests bytes
	// it cannot parse rather than rejecting them, so even the impossible branch would still yield
	// a stable digest.
	b, _ := json.Marshal(promptArgsDoc{Prompt: prompt})
	return b
}

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
		if len(st.PendingThrash) == 0 {
			st.WarningTurn = st.Turn + 1
		}
		st.PendingThrash = append(st.PendingThrash, rule)
	}
}

// pendingThrashLines drains the queued warnings into their rendered form and clears the queue.
//
// The rendering goes through grammar.FormatWarning rather than a local Sprintf: §5.11 leaves the
// wording open, SP-01 closed it there, and SP-15 asserts it byte-for-byte, so a second formatter
// in this package would be a second answer to a question that already has one.
//
// Direct formatter callers use their current turn. The split live path uses
// WarningTurn, fixed when the queue first becomes nonempty, so worker/reply
// scheduling cannot change the warning's label.
func (o *observer) pendingThrashLines(st *sessionState) []string {
	return o.pendingThrashAt(st, st.Turn)
}

func (o *observer) pendingThrashAt(st *sessionState, turn core.TurnIndex) []string {
	if len(st.PendingThrash) == 0 {
		return nil
	}

	out := make([]string, 0, len(st.PendingThrash))
	for _, rule := range st.PendingThrash {
		out = append(out, grammar.FormatWarning(grammar.Warning{
			Rule:    rule,
			Repeats: rule.Uses,
			Message: thrashAdvice,
			Turns:   []core.TurnIndex{turn},
		}))
	}
	st.PendingThrash = nil
	return out
}
