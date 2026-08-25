package negknow

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// This file is an INTERNAL test (package negknow, not negknow_test) on purpose: the plan's test
// table requires TestLemma and TestApproachClass_SynonymFixpoint, which name `lemma` and the
// `synonyms` table directly, and neither is exported. Everything reachable through the exported
// API is tested from negknow_test in descriptor_test.go, so the internal surface tested here is
// exactly the surface the plan names.

// TestApproachClass_Table pins the eight worked examples of the SP-09 implementation spec. Each
// row's trace is the normative behaviour of the eight-step algorithm, not an aspiration: the
// comment on each row records which step does the work, so a future change that "improves" the
// classifier has to argue with a specific step.
func TestApproachClass_Table(t *testing.T) {
	tests := []struct {
		name     string
		approach string
		want     string
	}{
		// widen · pool · timeout — three tokens, every one a synonym-table fixpoint.
		{"plain", "widen pool timeout", "widen-pool-timeout"},
		// increasing -> increas -> increase -> widen (silent-e restore); connection -> pool;
		// pooling is dropped as a duplicate of pool; timeouts -> timeout (plural strip).
		{"synonyms and lemma", "Increasing the connection-pool timeouts", "widen-pool-timeout"},
		// bump -> widen; "the" is a stopword; deadline -> timeout.
		{"bump", "bump the pool deadline", "widen-pool-timeout"},
		// disable; connection -> pool; pooling -> pool, deduplicated to one token.
		{"disable pooling", "disable connection pooling", "disable-pool"},
		// raise -> widen.
		{"raise", "raise the pool timeout", "widen-pool-timeout"},
		// rewrite; refreshtoken is unmapped and keeps its lemma; async -> parallel;
		// retries -> retry. Exactly maxClassTokens tokens survive.
		{"four tokens", "rewrite refreshToken with async retries", "rewrite-refreshtoken-parallel-retry"},
		// Every character is outside [a-z0-9], so step 2 leaves nothing behind.
		{"blank", "   ", "unclassified"},
		// Both tokens are stopwords, so step 4 empties the list.
		{"all stopwords", "try that", "unclassified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ApproachClass(tc.approach))
		})
	}
}

// TestApproachClass_SynonymFixpoint asserts every value in the synonyms table is itself a key
// mapping to itself. Without that, classifyToken would not be idempotent: a stem produced by one
// lookup could be rewritten by a later one, and two spellings of the same idea would land on
// different classes depending on which spelling arrived first.
func TestApproachClass_SynonymFixpoint(t *testing.T) {
	require.Len(t, stopwords, 48, "the stopword set is closed at 48 entries; growing it re-classes every elimination on disk")
	require.NotEmpty(t, synonyms)
	for k, v := range synonyms {
		require.Equal(t, v, synonyms[v], "synonyms[%q] = %q, but %q is not a fixpoint", k, v, v)
	}
}

// TestApproachClass_SilentERestore pins step 4 of classifyToken — the one lookup that makes the
// synonyms table small. Stripping "ing" or "ed" from an -e verb loses the e (increasing ->
// increas), so a single restore attempt recovers the dictionary form instead of demanding a
// second table entry per verb. Every row here fails without that step.
func TestApproachClass_SilentERestore(t *testing.T) {
	tests := map[string]string{
		"increasing":  "widen",
		"disabling":   "disable",
		"reducing":    "shrink",
		"rewriting":   "rewrite",
		"upgrading":   "upgrade",
		"migrating":   "upgrade",
		"introducing": "enable",
		"removing":    "disable",
		"deleting":    "disable",
		"enlarging":   "widen",
		"downgrading": "downgrade",
		"memoizing":   "cache",
		"cached":      "cache",
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			require.Equal(t, want, ApproachClass(in))
		})
	}
}

// TestApproachClass_DoubledConsonantLimitation pins the documented, accepted limitation: the
// crude lemma does not undouble a consonant, so "dropping" stems to "dropp" rather than to
// "disable" via "drop". That is a recall loss on a query and never a wrong answer, because the
// record lookup is authoritative. It is pinned so that changing it is a deliberate act.
func TestApproachClass_DoubledConsonantLimitation(t *testing.T) {
	require.Equal(t, "dropp", ApproachClass("dropping"))
	require.Equal(t, "skipp", ApproachClass("skipping"))
}

// TestApproachClass_Deterministic classifies 1 000 generated ASCII strings twice and requires the
// two results to agree. Map iteration order is the obvious way this could fail: any algorithm
// that ranged over stopwords or synonyms instead of indexing them would be nondeterministic here.
//
// rapid runs 100 checks by default, so each check draws ten strings: 100 x 10 is the 1 000 the
// plan asks for, without depending on a -rapid.checks flag the CI job does not set.
func TestApproachClass_Deterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		batch := rapid.SliceOfN(rapid.StringMatching(`[ -~]{0,64}`), 10, 10).Draw(rt, "batch")
		for _, s := range batch {
			first := ApproachClass(s)
			second := ApproachClass(s)
			require.Equal(rt, first, second, "input %q classified two different ways", s)
		}
	})
}

// TestApproachClass_Bounded asserts the result is bounded no matter how long the input is: at
// most maxClassTokens tokens, each at most maxTokenBytes bytes, the whole string at most 128
// bytes, and always valid UTF-8. The per-token cap is what bounds an arbitrarily long single
// word; without it a 4 KB input with no separators would produce a 4 KB approach class and a 4 KB
// bloom key preimage.
func TestApproachClass_Bounded(t *testing.T) {
	requireBounded := func(t require.TestingT, in string) {
		got := ApproachClass(in)
		require.True(t, utf8.ValidString(got), "result must be valid UTF-8: %q", got)
		require.LessOrEqual(t, len(got), 128, "result must be at most 128 bytes: %q", got)
		require.NotEmpty(t, got, "result is never empty; the empty case is %q", "unclassified")
		toks := strings.Split(got, "-")
		require.LessOrEqual(t, len(toks), maxClassTokens, "at most %d tokens: %q", maxClassTokens, got)
		for _, tok := range toks {
			require.NotEmpty(t, tok, "no empty token may survive: %q", got)
			require.LessOrEqual(t, len(tok), maxTokenBytes, "token %q exceeds %d bytes", tok, maxTokenBytes)
		}
	}

	// The explicit worst case the plan names: one 4 KB word with no separators at all.
	requireBounded(t, strings.Repeat("a", 4096))
	requireBounded(t, strings.Repeat("widen pool timeout retry cache index lock ", 100))

	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.StringN(-1, -1, 4096).Draw(rt, "s")
		requireBounded(rt, s)
	})
}

// TestApproachClass_Empty pins the four ways an approach can carry no classifiable content: it is
// empty, it is only spaces, it is only punctuation, or every token is a stopword. All four must
// return the same sentinel, because a caller cannot distinguish them and an empty approach class
// would make the descriptor's third field silently optional.
func TestApproachClass_Empty(t *testing.T) {
	for _, in := range []string{"", "   ", "!!! ???", "the a of"} {
		require.Equal(t, "unclassified", ApproachClass(in), "input %q", in)
	}
}

// TestLemma pins the crude lemmatiser directly, including the two cases that exist only to stop
// it from over-stripping: "pass" keeps its double s, and "is" is too short for any rule to fire.
func TestLemma(t *testing.T) {
	tests := map[string]string{
		"increasing": "increas",
		"widened":    "widen",
		"timeouts":   "timeout",
		"retries":    "retry",
		"indices":    "indice",
		"pass":       "pass",
		"is":         "is",
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			require.Equal(t, want, lemma(in))
		})
	}
}
