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
