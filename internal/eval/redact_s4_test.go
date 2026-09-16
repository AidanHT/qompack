package eval_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/eval"
)

// The credential-shaped fixtures are written split across a `+` for the reason
// internal/testutil/secrettokens.go gives: the runtime value is exact while no contiguous
// credential-shaped run appears in this source for a scanner to match.
//
//nolint:gosec // G101: redaction-rule fixtures, not credentials.
var (
	ghpBody38   = "1234567890abcdefghijklmnopqrstuvwxyz" + "12"
	ghuToken    = "ghu_" + ghpBody38
	ghsToken    = "ghs_" + ghpBody38
	ghrToken    = "ghr_" + ghpBody38
	ghpToken    = "ghp_" + ghpBody38
	npmrcSecret = "Kq4Rm8Tv2Wy6" + "Ze0AbCdEfGhIj"
)

// redactText runs one string through the exporter by way of a one-turn session, which is the only
// entry point this package exports.
func redactS4Text(t *testing.T, text string, extra ...*regexp.Regexp) string {
	t.Helper()
	out, _ := eval.Redact(eval.Session{
		ID:    "sess-s4",
		Turns: []eval.Turn{{Role: "user", Text: text}},
	}, extra...)
	require.Len(t, out.Turns, 1)
	return out.Turns[0].Text
}

// TestRedact_ReachesEveryGitHubTokenShape is finding S-4's first half.
//
// The classic-token rule quantified its body at exactly {36}, so a 38-character token matched its
// first 36 characters and then failed the trailing \b — the rule did not fire at all and the whole
// credential survived the export. The alternation also carried only ghp_ and gho_, leaving the
// user-to-server, server-to-server and refresh prefixes outside it entirely.
func TestRedact_ReachesEveryGitHubTokenShape(t *testing.T) {
	for _, tok := range []string{ghpToken, ghuToken, ghsToken, ghrToken} {
		out := redactS4Text(t, "the token is "+tok+" from the keychain")
		require.NotContains(t, out, tok, "the %s-prefixed token survived the export", tok[:4])
		require.Contains(t, out, "<TOKEN>")
	}
}

// TestRedact_ReachesTheUnderscoredAndDotenvKeyShapes is the other half of the rule-set gap: the
// exporter's assignment family had four key names against internal/redact's eleven, no leading
// underscore, and no .env line rule at all.
func TestRedact_ReachesTheUnderscoredAndDotenvKeyShapes(t *testing.T) {
	for _, line := range []string{
		"//registry.npmjs.org/:_authToken=" + npmrcSecret,
		"_password=" + npmrcSecret,
		"auth=" + npmrcSecret,
		"client_secret: " + npmrcSecret,
		"passwd=" + npmrcSecret,
		"API_SECRET=" + npmrcSecret,
		"export DATABASE_PASSWORD=" + npmrcSecret,
	} {
		out := redactS4Text(t, line)
		require.NotContains(t, out, npmrcSecret, "the credential survived %q as %q", line, out)
		require.Contains(t, out, "<REDACTED>")
	}
}

// TestRedact_AppliesOperatorPatterns is S-4's second half: an operator's own
// runtime.redact.patterns rule existed only in configuration, so the export applied the built-in
// table alone and was strictly weaker than the capture path over exactly the secrets the operator
// had already declared.
func TestRedact_AppliesOperatorPatterns(t *testing.T) {
	const marker = "INTERNAL-PROJECT-CODENAME-8813"
	require.Equal(t, marker, redactS4Text(t, marker), "no built-in rule matches this shape")

	out := redactS4Text(t, "the plan is "+marker+" for now", regexp.MustCompile(`INTERNAL-[A-Z-]+-\d{4}`))
	require.NotContains(t, out, marker, "a configured pattern must reach the export")
	require.Contains(t, out, "<REDACTED>")
}

// TestRedact_StaysIdempotentWithOperatorPatterns: a configured rule must not break the fixed-point
// property the whole rule set rests on.
func TestRedact_StaysIdempotentWithOperatorPatterns(t *testing.T) {
	extra := regexp.MustCompile(`INTERNAL-[A-Z-]+-\d{4}`)
	text := strings.Join([]string{
		"INTERNAL-PROJECT-CODENAME-8813",
		"ghu_" + ghpBody38,
		"_authToken=" + npmrcSecret,
		"API_SECRET=" + npmrcSecret,
		"alice@example.com",
	}, "\n")

	once := redactS4Text(t, text, extra)
	twice := redactS4Text(t, once, extra)
	require.Equal(t, once, twice, "Redact(Redact(x)) must equal Redact(x)")
}

// TestRedact_OperatorPatternsAreIgnoredWhenNoneAreGiven keeps the widening honest in the other
// direction: an ordinary export with no configured rules must behave exactly as before.
func TestRedact_OperatorPatternsAreIgnoredWhenNoneAreGiven(t *testing.T) {
	const ordinary = "a perfectly ordinary sentence with no credential in it"
	require.Equal(t, ordinary, redactS4Text(t, ordinary))
}
