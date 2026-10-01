package observer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
)

// The reply-only path's claim (WithPromptReplyClaim): a thrash warning is drained into a reply only
// when the reply is still going out. A refused claim re-arms the rule for the loop's next
// occurrence instead of consuming it, and never replays the stale warning by itself.

// claimCounter is a claim that answers ok and counts how often it was asked.
type claimCounter struct {
	ok    bool
	calls int
}

func (c *claimCounter) claim() bool {
	c.calls++
	return c.ok
}

// replyWithClaim is the live reply path with c as its claim.
func (h *harness) replyWithClaim(text string, c *claimCounter) Output {
	h.t.Helper()
	ctx := WithPromptReplyClaim(WithPromptReplyOnly(context.Background()), c.claim)
	out, err := h.obs.OnUserPrompt(ctx, promptOf(text))
	require.NoError(h.t, err)
	return out
}

// setThrashing replaces what the fake grammar reports as thrashing.
func (g *fakeGrammar) setThrashing(rules ...grammar.Rule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Thrashing = rules
}

func TestPromptReplyClaim_RefusedClaimReArmsForTheLoopsNextOccurrence(t *testing.T) {
	queued := thrashRule(1, 4, "FileRead", "FileEdit", "Bash")
	g := &fakeGrammar{Thrashing: []grammar.Rule{queued}}
	h := newHarness(t, func(o *Options) { o.Grammar = g })
	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))

	// The loop ran on while the reply waited: Sequitur now counts the rule 11 times.
	g.setThrashing(thrashRule(1, 11, "FileRead", "FileEdit", "Bash"))
	refused := &claimCounter{ok: false}
	require.Equal(t, hookio.Empty(), h.replyWithClaim("keep going", refused))
	require.Equal(t, 1, refused.calls, "the claim is asked exactly once, for the warning it would carry")
	require.Equal(t, int64(1), h.counter(counterThrashUndelivered), "the undelivered warning is counted")

	st := h.state(testSession)
	require.Empty(t, st.PendingThrash, "the stale warning is not kept for a later prompt")
	require.False(t, st.WarnedRules[1], "a warning the host never received does not count as delivered")
	require.Equal(t, 11, st.ThrashFloor[1], "held at the multiplicity the rule has now, not the queued one")

	// Another tool use with the rule where it was: the loop has not occurred again, so nothing queues.
	h.drive(readOf("toolu_2", "src/b.ts", "beta\n"))
	require.Empty(t, h.state(testSession).PendingThrash, "a rule at its floor is not warned again")
	require.Equal(t, hookio.Empty(), h.replyWithClaim("still going", &claimCounter{ok: true}))

	// The loop occurs again: the rule warns afresh, at its new multiplicity, exactly once.
	again := thrashRule(1, 12, "FileRead", "FileEdit", "Bash")
	g.setThrashing(again)
	h.drive(readOf("toolu_3", "src/a.ts", "alpha\n"))
	accepted := &claimCounter{ok: true}
	out := h.replyWithClaim("try again", accepted)
	require.NotNil(t, out.HookSpecificOutput)
	require.Contains(t, out.HookSpecificOutput.AdditionalContext, "repeated 12×")
	require.Equal(t, 1, accepted.calls)
	require.NotContains(t, h.state(testSession).ThrashFloor, grammar.RuleID(1), "the floor is spent once it re-queues")

	h.drive(readOf("toolu_4", "src/a.ts", "alpha\n"))
	require.Equal(t, hookio.Empty(), h.replyWithClaim("and again", &claimCounter{ok: true}),
		"once delivered, the rule is reported once")
}

func TestPromptReplyClaim_AskedOnlyWhenThereIsAWarningToHandOver(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Grammar = &fakeGrammar{} })
	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	c := &claimCounter{ok: false}
	require.Equal(t, hookio.Empty(), h.replyWithClaim("hello", c))
	require.Zero(t, c.calls, "a reply with nothing to hand over never asks")
	require.Zero(t, h.counter(counterThrashUndelivered))
}

func TestPromptReplyClaim_PassiveModeKeepsTheQueue(t *testing.T) {
	rule := thrashRule(1, 11, "FileRead", "FileEdit", "Bash")
	mode := ModePassive
	h := newHarness(t, func(o *Options) {
		o.Grammar = &fakeGrammar{Thrashing: []grammar.Rule{rule}}
		o.Mode = func() Mode { return mode }
	})
	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))

	c := &claimCounter{ok: false}
	require.Equal(t, hookio.Empty(), h.replyWithClaim("keep going", c))
	require.Zero(t, c.calls, "a degraded reply acts on nothing, so it neither asks nor consumes")
	require.Len(t, h.state(testSession).PendingThrash, 1,
		"the warning queued while degraded is still delivered once the mode is restored (§12.1)")

	mode = ModeFull
	out := h.replyWithClaim("and now?", &claimCounter{ok: true})
	require.NotNil(t, out.HookSpecificOutput)
	require.Contains(t, out.HookSpecificOutput.AdditionalContext, "repeated 11×")
}

// replyWithNonce is the live reply path answering the prompt with this nonce, its claim accepted.
func (h *harness) replyWithNonce(text, nonce string) Output {
	h.t.Helper()
	ctx := WithPromptReplyNonce(WithPromptReplyClaim(WithPromptReplyOnly(context.Background()),
		(&claimCounter{ok: true}).claim), nonce)
	out, err := h.obs.OnUserPrompt(ctx, promptOf(text))
	require.NoError(h.t, err)
	return out
}

// TestPromptReplySpooled_ReArmsTheWarningAClaimedReplyCarried: a claimed reply carried the warning,
// but its hook gave up and spooled the prompt. PromptReplySpooled, matched by that prompt's nonce,
// re-arms exactly the rules that reply carried, as a refused claim would; a warning queued since for
// another rule stays queued, and any other nonce or session changes nothing.
func TestPromptReplySpooled_ReArmsTheWarningAClaimedReplyCarried(t *testing.T) {
	const nonce = "nonce-of-the-spooled-prompt"
	g := &fakeGrammar{Thrashing: []grammar.Rule{thrashRule(1, 4, "FileRead", "FileEdit", "Bash")}}
	h := newHarness(t, func(o *Options) { o.Grammar = g })
	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	out := h.replyWithNonce("keep going", nonce)
	require.NotNil(t, out.HookSpecificOutput, "the claimed reply carries the warning")
	require.Contains(t, out.HookSpecificOutput.AdditionalContext, "repeated 4×")
	require.Zero(t, h.counter(counterThrashUndelivered))

	// The loop ran on, and a second rule was queued, before the drain reached the spooled copy.
	g.setThrashing(thrashRule(1, 6, "FileRead", "FileEdit", "Bash"), thrashRule(2, 5, "Grep", "FileRead"))
	h.drive(readOf("toolu_2", "src/b.ts", "beta\n"))
	require.Len(t, h.state(testSession).PendingThrash, 1, "rule 2 is queued; rule 1 is still counted warned")

	var ra SpooledReplyRearmer = h.obs
	require.False(t, ra.PromptReplySpooled(testSession, "some-other-nonce"), "another prompt's copy changes nothing")
	require.False(t, ra.PromptReplySpooled("sess_unknown", nonce), "nor does a session the observer never saw")
	_, seen := h.obs.Progress("sess_unknown")
	require.False(t, seen, "and no state is created for it")
	require.Zero(t, h.counter(counterThrashUndelivered))

	require.True(t, ra.PromptReplySpooled(testSession, nonce))
	require.Equal(t, int64(1), h.counter(counterThrashUndelivered), "the undelivered warning is counted")
	st := h.state(testSession)
	require.False(t, st.WarnedRules[1], "a warning the host never received does not count as delivered")
	require.Equal(t, 6, st.ThrashFloor[1], "held at the multiplicity the rule has now")
	require.Len(t, st.PendingThrash, 1, "the other rule's queued warning is a later reply's, and stays")
	require.False(t, ra.PromptReplySpooled(testSession, nonce), "a settled copy is forgotten: re-armed once")
	require.Equal(t, int64(1), h.counter(counterThrashUndelivered))

	// The loop has not occurred again: the next reply carries rule 2 only.
	h.drive(readOf("toolu_3", "src/b.ts", "beta\n"))
	out = h.replyWithNonce("what now?", "nonce-2")
	require.NotNil(t, out.HookSpecificOutput)
	require.NotContains(t, out.HookSpecificOutput.AdditionalContext, "FileRead→FileEdit→Bash",
		"the stale warning is not replayed by itself")

	// It occurs again: rule 1 warns afresh, at its new multiplicity.
	g.setThrashing(thrashRule(1, 7, "FileRead", "FileEdit", "Bash"), thrashRule(2, 5, "Grep", "FileRead"))
	h.drive(readOf("toolu_4", "src/a.ts", "alpha\n"))
	out = h.replyWithNonce("try again", "nonce-3")
	require.NotNil(t, out.HookSpecificOutput)
	require.Contains(t, out.HookSpecificOutput.AdditionalContext, "repeated 7×")
}
