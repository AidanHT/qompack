package rehydrate_test

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/stretchr/testify/require"
)

// TestStandingInstruction_IsTheVerbatimSentence pins the exact bytes 00-ARCHITECTURE.md §5.15
// quotes. SP-11 emits it beside the eliminations item and SP-13's `already_tried` tool
// description is written to agree with it; both land in wave 3, so a silent rewording here would
// surface as a cross-subplan mismatch rather than as a test failure.
func TestStandingInstruction_IsTheVerbatimSentence(t *testing.T) {
	require.Equal(t, "Before committing to an approach, call already_tried.", rehydrate.StandingInstruction())
}

// TestStandingInstruction_IsOneBareLine pins the formatting the injection depends on: one
// sentence, no trailing newline, no markup. It is concatenated into an already-tagged payload, so
// stray whitespace or a stray fence would land verbatim in the model's context.
func TestStandingInstruction_IsOneBareLine(t *testing.T) {
	got := rehydrate.StandingInstruction()

	require.Equal(t, strings.TrimSpace(got), got, "no leading or trailing whitespace")
	require.NotContains(t, got, "\n", "one line, so it can be placed anywhere in the payload")
	require.NotContains(t, got, "`", "no markup: the sentence is injected verbatim")
	require.Contains(t, got, "already_tried", "the sentence must name the tool it is telling the agent to call")
}

// TestStandingInstruction_IsStable asserts repeated calls return identical bytes: it is a
// constant, not a rendering, and callers compare it.
func TestStandingInstruction_IsStable(t *testing.T) {
	require.Equal(t, rehydrate.StandingInstruction(), rehydrate.StandingInstruction())
}
