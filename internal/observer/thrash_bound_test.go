package observer

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
)

// The UserPromptSubmit thrash warning is additionalContext, and the host caps every additionalContext
// at hookio.HostFieldMaxChars: over it, Claude is handed a file path and a 2,000-character preview it
// is not asked to read (C1.20). The warning had no bound at all — one line per newly looping rule,
// each spelling the rule's whole terminal expansion, whose terminals are host tool names — so these
// tests pin the bound by construction, through the reply path the daemon calls.

// thrashRulesOf builds n rules whose expansion is width symbols named by name.
func thrashRulesOf(n, width int, name func(i, j int) string) []grammar.Rule {
	rules := make([]grammar.Rule, 0, n)
	for i := 0; i < n; i++ {
		exp := make([]grammar.Symbol, 0, width)
		for j := 0; j < width; j++ {
			exp = append(exp, grammar.Symbol(name(i, j)))
		}
		rules = append(rules, thrashRule(grammar.RuleID(i+1), 11, exp...))
	}
	return rules
}

func TestPromptReply_ThrashWarningStaysFarUnderTheHostCap(t *testing.T) {
	require.LessOrEqual(t, thrashWarningMaxChars*5, hookio.HostFieldMaxChars,
		"the warning's own ceiling must sit far under the host's per-field cap, not merely under it")

	longMCP := func(i, j int) string { return fmt.Sprintf("mcp__server_%d__a_rather_long_tool_name_%d", i, j) }
	cases := map[string]struct {
		rules    []grammar.Rule
		omitted  int
		cutLines bool
	}{
		"many long rules":     {rules: thrashRulesOf(500, 40, longMCP), omitted: 495, cutLines: true},
		"one enormous rule":   {rules: thrashRulesOf(1, 50_000, longMCP), cutLines: true},
		"astral tool names":   {rules: thrashRulesOf(7, 3_000, func(int, int) string { return "\U0001F600" }), omitted: 2, cutLines: true},
		"invalid UTF-8 names": {rules: thrashRulesOf(6, 3_000, func(int, int) string { return "\xff\xfe" }), omitted: 1, cutLines: true},
		"exactly the most lines, all short": {
			rules: thrashRulesOf(thrashWarningMaxLines, 3, func(_, j int) string { return []string{"Read", "Edit", "Bash"}[j] }),
		},
		"one more than the most lines": {
			rules:   thrashRulesOf(thrashWarningMaxLines+1, 3, func(_, j int) string { return []string{"Read", "Edit", "Bash"}[j] }),
			omitted: 1,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, func(o *Options) { o.Grammar = &fakeGrammar{Thrashing: tc.rules} })
			h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
			out := h.submitReply("keep going")

			require.NotNil(t, out.HookSpecificOutput)
			ac := out.HookSpecificOutput.AdditionalContext
			require.LessOrEqual(t, hookio.HostChars(ac), thrashWarningMaxChars)
			require.Empty(t, hookio.HostCapOverruns(hookio.ConformOutput(hookio.EventUserPromptSubmit, out)),
				"a thrash warning over the cap reaches Claude as a file path and a preview")
			require.True(t, utf8.ValidString(ac) || strings.Contains(name, "invalid"),
				"a cut must land on a rune boundary")

			lines := strings.Split(ac, thrashLineSep)
			shown := min(len(tc.rules), thrashWarningMaxLines)
			for _, l := range lines[:shown] {
				require.True(t, strings.HasPrefix(l, "[qompack] possible loop: "), "each shown line is still a warning: %.80s", l)
				require.LessOrEqual(t, hookio.HostChars(l), thrashWarningLineMaxChars)
				require.Equal(t, tc.cutLines, strings.HasSuffix(l, thrashCutMarker), "a cut line says so: %.80s", l)
			}
			if tc.omitted > 0 {
				require.Len(t, lines, shown+1)
				require.Equal(t, fmt.Sprintf(thrashOmittedFormat, tc.omitted), lines[shown],
					"the warnings left out are counted, never silently dropped")
			} else {
				require.Len(t, lines, shown)
			}

			require.Equal(t, hookio.Empty(), h.submitReply("still going"),
				"the whole queue drains on one reply, shown or counted: one loop is reported once")
		})
	}
}

// TestBoundThrashWarning_OrdinaryWarningsRenderWhole pins that the bound only ever engages where it
// has to: a handful of ordinary warnings is exactly the lines grammar.FormatWarning wrote, joined.
func TestBoundThrashWarning_OrdinaryWarningsRenderWhole(t *testing.T) {
	var lines []string
	for i := 1; i <= thrashWarningMaxLines; i++ {
		lines = append(lines, grammar.FormatWarning(grammar.Warning{
			Rule:    thrashRule(grammar.RuleID(i), 11, "FileRead", "FileEdit", "Bash"),
			Repeats: 11, Message: thrashAdvice,
		}))
		require.Equal(t, strings.Join(lines, thrashLineSep), boundThrashWarning(lines))
	}
	require.Empty(t, boundThrashWarning(nil))
}

// TestBoundThrashWarning_WorstCaseFitsItsCeiling is the arithmetic behind thrashWarningMaxChars:
// every shown line at its own bound, every separator, and the longest tail any count can produce.
func TestBoundThrashWarning_WorstCaseFitsItsCeiling(t *testing.T) {
	worst := thrashWarningMaxLines*thrashWarningLineMaxChars +
		thrashWarningMaxLines*hookio.HostChars(thrashLineSep) +
		hookio.HostChars(fmt.Sprintf(thrashOmittedFormat, math.MaxInt))
	require.LessOrEqual(t, worst, thrashWarningMaxChars)
}
