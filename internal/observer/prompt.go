package observer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
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

// onUserPrompt is resolved decision 3's three durable artifacts followed by the turn bookkeeping
// of decision 4: store the bytes, index them, put a KindUserPrompt node in the graph and enrol it
// in the open segment, append the user symbol, close the user turn, sample the §6.6 features, and
// drain whatever PostToolUse queued.
//
// It returns an error ONLY for ctx.Err(); every I/O failure is absorbed by soft (decision 7).
func (o *observer) onUserPrompt(ctx context.Context, e Event) (Output, error) {
	// 1. Nothing before the ctx check, and the clock is read exactly once (decision 11).
	if err := ctx.Err(); err != nil {
		return hookio.Empty(), err
	}
	// An empty prompt is not a user turn. Capturing it would mint an object, an index entry and a
	// graph node for no content, and — worse — would advance the turn counter past a turn that
	// never happened, which every stored artifact downstream is numbered against.
	if e.Prompt == "" {
		return hookio.Empty(), nil
	}
	now := o.now()
	st := o.session(e.SessionID)
	st.mu.Lock()
	defer st.mu.Unlock()

	// 2-3. Artifact (a): the user's bytes, content-addressed. verbatimOptions is the whole of what
	//      makes this capture verbatim, and KeepRaw is what keeps the canonicalizer's deltas so
	//      canon.Restore can reconstruct the pre-normalization bytes exactly.
	body := []byte(e.Prompt)
	res, err := o.opt.Store.PutBytes(ctx, body, store.PutOptions{
		Tool: userPromptSubmit, Path: "", Canon: verbatimOptions(),
		KeepRaw: true, Ephemeral: false,
	})
	if err != nil {
		// The capture is lost; the TURN is not. Steps 4 and 5 are skipped, because no index entry
		// and no DAG node may point at an object that was never written (tooluse.go's "never a
		// dangling index record"), but everything from the grammar symbol onward still runs: a
		// prompt whose bytes failed to land is still a prompt the session took, and renumbering
		// every later artifact around it would be the larger corruption.
		o.soft(stagePromptPut, err)
	} else {
		o.recordPrompt(ctx, st, e, res, body, now)
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

	// 9. The ONE place o.mode() is consulted (decision 10). Degraded-passive keeps every write
	//    above and emits nothing, because §12 forbids injection while the contract is degraded.
	out := hookio.Empty()
	if o.mode() == ModeFull {
		if lines := o.pendingThrashLines(st); len(lines) > 0 {
			// hookio carries no UserPromptSubmit constructor yet; adding one is V3-VERIFY's.
			out.HookSpecificOutput = &hookio.HSO{
				HookEventName:     userPromptSubmit,
				AdditionalContext: strings.Join(lines, thrashLineSep),
			}
		}
	}
	return out, nil
}

// recordPrompt writes artifacts (b) and (c) of resolved decision 3: the append-only tool_use entry
// under VerbatimPromptID, and the KindUserPrompt node enrolled in the currently open segment.
//
// It runs only on a successful Put, so every record it writes names an object that exists.
func (o *observer) recordPrompt(ctx context.Context, st *sessionState, e Event,
	res store.PutResult, body []byte, now core.UnixMilli,
) {
	// 4. The index entry. Its id is DERIVED rather than carried: a UserPromptSubmit payload has no
	//    tool_use_id, and a record the graph cannot name is a record nothing can retrieve.
	id := VerbatimPromptID(e.SessionID, st.Turn)
	tok := res.Root.Tokens
	if tok == 0 && o.opt.Tokens != nil {
		tok = o.opt.Tokens.EstimateString(e.Prompt, tokens.ClassProse)
	}
	digest, preview := store.ArgsDigest(promptArgs(e.Prompt))
	o.soft(stageIndex, o.opt.Store.RecordToolUse(ctx, store.ToolUseRecord{
		ID: id, Session: e.SessionID, Turn: st.Turn, TS: now, Tool: userPromptSubmit,
		ArgsDigest: digest, ArgsPreview: preview,
		Root: res.Root.Hash, Bytes: int64(len(body)), Tokens: tok,
		Status: store.StatusOK,
	}))

	// 5. dag.BuildUserPrompt emits userprompt:<turn> and userprompt:<turn> --consumes-->
	//    assistant:<turn>: the prompt is the producer end (SP-07 D-1), and §4.4 makes that edge
	//    the ONLY path by which a backward slice from a tool use deep in a session reaches the
	//    request that set it off.
	o.soft(stageDAG, dag.BuildUserPrompt(o.opt.Graph, dag.ObservedPrompt{
		Turn: st.Turn, TS: now, Pos: o.advancePos(st, tok), Tokens: tok, Ref: string(id),
	}))
	// 5b. The bridging consumes edge, userprompt:<turn> → assistant:<turn+1>. The plan carries an
	//     internal contradiction here: decision 4 records the prompt AT Turn and then increments,
	//     and BuildToolUse — the only AssistantNode minter — mints assistant nodes only at the
	//     post-increment tool turns, yet BuildUserPrompt's own edge targets AssistantNode(Turn),
	//     a node no path ever creates. Under decision 4 the assistant turn that ANSWERS this
	//     prompt always sits at Turn+1, so this hand-emitted edge is the §4.4 backward-slice path
	//     from the work back to the request that set it off. The builder's same-turn edge stays:
	//     it dangles, a dangling edge is legal (D-6) and no slice from a real node traverses it.
	//     V3-VERIFY should fold this bridge into an amended BuildUserPrompt(AssistantNode(Turn+1))
	//     together with SP-07, at which point this AddEdge becomes redundant and is removed.
	o.soft(stageDAG, o.opt.Graph.AddEdge(dag.Edge{
		From: dag.UserPromptNode(st.Turn), To: dag.AssistantNode(st.Turn + 1),
		Kind: dag.EdgeConsumes, Weight: edgeWeight, Turn: st.Turn,
	}))
	o.enrol(st, dag.UserPromptNode(st.Turn))
}

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
