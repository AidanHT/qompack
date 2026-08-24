package observer

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
)

// thrashRule is one Sequitur rule above the multiplicity threshold.
func thrashRule(id grammar.RuleID, uses int, expansion ...grammar.Symbol) grammar.Rule {
	return grammar.Rule{ID: id, Uses: uses, Expansion: expansion, Span: len(expansion)}
}

func TestCollectThrash_QueuesEachRuleExactlyOnce(t *testing.T) {
	g := &fakeGrammar{Thrashing: []grammar.Rule{
		thrashRule(1, 11, "FileRead", "FileEdit", "Bash"),
	}}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		readOf("toolu_2", "src/a.ts", "alpha\n"),
		readOf("toolu_3", "src/a.ts", "alpha\n"),
	)

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Len(t, st.PendingThrash, 1,
		"Sequitur reports a rule for as long as it stays above the threshold; the warning is per rule")
	require.True(t, st.WarnedRules[1])
}

func TestCollectThrash_QueuesANewlyThrashingRule(t *testing.T) {
	g := &fakeGrammar{}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	g.mu.Lock()
	g.Thrashing = []grammar.Rule{thrashRule(1, 4, "FileRead", "Bash"), thrashRule(2, 9, "Grep", "FileEdit")}
	g.mu.Unlock()
	h.drive(readOf("toolu_2", "src/a.ts", "alpha\n"))

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Len(t, st.PendingThrash, 2)
}

func TestCollectThrash_AppendsTheToolSymbolAndTheTestVerdict(t *testing.T) {
	g := &fakeGrammar{}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		bashOf("toolu_2", "go test ./...", "ok  \tgithub.com/example/pkg\t0.42s\n"),
		bashOf("toolu_3", "go test ./...", "FAIL\tgithub.com/example/pkg\t0.42s\n"),
	)

	require.Equal(t, []grammar.Symbol{
		"FileRead",
		"Bash", grammarTestPass,
		"Bash", grammarTestFail,
	}, g.appended(), "the action stream carries the tool symbol and, when there is one, the verdict")
}

func TestPendingThrashLines_DrainsAndFormatsThroughGrammar(t *testing.T) {
	rule := thrashRule(1, 11, "FileRead", "FileEdit", "Bash")
	h := newHarness(t, func(o *Options) { o.Grammar = &fakeGrammar{Thrashing: []grammar.Rule{rule}} })

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	st := h.state(testSession)
	st.mu.Lock()
	st.Turn = 42
	lines := h.obs.pendingThrashLines(st)
	drained := h.obs.pendingThrashLines(st)
	st.mu.Unlock()

	require.Equal(t, []string{grammar.FormatWarning(grammar.Warning{
		Rule: rule, Repeats: 11, Message: thrashAdvice, Turns: []core.TurnIndex{42},
	})}, lines)
	require.Nil(t, drained, "the queue is drained, so one loop is reported once")
}

func TestCollectThrash_NilGrammarIsTolerated(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Grammar = nil })

	require.NotPanics(t, func() {
		h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
		st := h.state(testSession)
		st.mu.Lock()
		defer st.mu.Unlock()
		h.obs.collectThrash(st)
		require.Empty(t, st.PendingThrash)
	})
}
