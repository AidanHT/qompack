package grammar_test

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
)

// TestRuleRef_RoundTrips pins the rule-reference encoding both grammar core and codec depend on.
// They meet only through these two functions, so an asymmetry here would be a wire bug that
// neither side's own tests could see.
func TestRuleRef_RoundTrips(t *testing.T) {
	for _, id := range []grammar.RuleID{0, 1, 7, 42, 1 << 20} {
		got, ok := grammar.ParseRuleRef(grammar.RuleRef(id))
		require.True(t, ok, "RuleRef(%d) must parse back as a reference", id)
		require.Equal(t, id, got)
	}
}

// TestRuleRef_ThePrefixIsReserved is the check behind the "cannot happen" in ruleRefPrefix's
// comment.
//
// A Snapshot flattens rule references and terminals into one Symbol space, so a terminal that
// spelled RuleRef(7) would decode as a reference to rule 7. The encoding is sound because no
// legitimate Symbol can contain a NUL — §5.11 says a Symbol is a tool name, the literal "user", or
// a test-outcome marker — and that is a property of the alphabet, which is checkable, rather than
// a discipline every call site has to remember, which is not.
func TestRuleRef_ThePrefixIsReserved(t *testing.T) {
	// Every documented inhabitant of Symbol, and the awkward neighbours of the sigil.
	legitimate := []grammar.Symbol{
		"Read", "Edit", "Bash", "Grep", "Glob", "Write", "Task", "WebFetch",
		"user", "test:pass", "test:fail",
		"R", "R7", "$R7", "\\x00R7", "Reader", "0R7", "",
	}
	for _, s := range legitimate {
		require.NotContains(t, string(s), "\x00",
			"a legitimate Symbol cannot contain a NUL; that is what reserves the prefix")
		_, isRef := grammar.ParseRuleRef(s)
		require.False(t, isRef, "%q must not be mistaken for a rule reference", s)
	}
}

// TestParseRuleRef_RefusesAMalformedRemainder pins the decision not to alias rule zero.
//
// A Symbol that carries the reserved prefix but an unparseable remainder is corrupt input, and the
// two available answers are "not a reference" and "rule 0". Returning rule 0 would silently
// redirect a corrupt stream at a real rule; returning not-a-reference degrades it into an
// odd-looking terminal, which is visible and harmless.
func TestParseRuleRef_RefusesAMalformedRemainder(t *testing.T) {
	for _, s := range []grammar.Symbol{
		"\x00R", "\x00Rx", "\x00R-1", "\x00R 7", "\x00R7x", "\x00R\x00R7",
		// Spellings strconv.Atoi accepts but RuleRef never writes. Accepting these would make
		// encoded rule identity many-to-one, so a forged stream could name rule 7 in a spelling no
		// encoder produced and two "different" references would silently be the same edge.
		"\x00R+7", "\x00R07", "\x00R-0", "\x00R007",
	} {
		id, ok := grammar.ParseRuleRef(s)
		require.False(t, ok, "%q carries the prefix but is not a valid reference", s)
		require.Zero(t, id, "a refused parse reports the zero id alongside false, and no caller reads it")
	}
}

// TestRuleRef_IsDistinctPerID guards the one property a careless encoding would break: two
// different rules must not spell the same reference.
func TestRuleRef_IsDistinctPerID(t *testing.T) {
	seen := make(map[grammar.Symbol]grammar.RuleID, 256)
	for id := grammar.RuleID(0); id < 256; id++ {
		s := grammar.RuleRef(id)
		prev, dup := seen[s]
		require.False(t, dup, "RuleRef(%d) and RuleRef(%d) both spell %q", prev, id, s)
		seen[s] = id
		require.True(t, strings.HasPrefix(string(s), "\x00R"))
	}
}
