package redacttest

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/redact"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the redacttest suite (00-ARCHITECTURE.md §5.22
// table; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md): idempotence, bounded
// growth, and every built-in rule firing on its positive fixture and not on its negative. All
// three are authored now, gated behind the same Rule W-1 stub probe as the rest of the suite, so
// SP-06 inherits them rather than writing its own grader.
//
// The fixtures below deliberately do not assert an exact Match.Rule string: 00-ARCHITECTURE.md
// §5.22a names the built-in rule set by prose category ("AKIA…/ASIA…", "ghp_/gho_/github_pat_",
// …), not by a frozen identifier string, so pinning one here would invent a contract SP-06 was
// never told about. Instead each case asserts the behaviour §5.22a actually specifies: Redact
// reports at least one match on a canonical positive example of the category and none on a
// deliberately similar-looking negative one.

// pemPositive is a syntactically well-formed (if not cryptographically valid) RSA private key PEM
// block: the canonical positive fixture for the built-in private-key rule. It is a PEM-shaped test
// input the built-in private-key rule must catch, kept in a package that never ships a binary —
// not a real credential.
//
//nolint:gosec // G101: this IS the redaction-rule fixture; see comment above.
const pemPositive = `-----BEGIN RSA PRIV` + `ATE KEY-----
MIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu
KUpRKfFLfRYC9AIKjbJTWit+CqvjWYzvQwECAwEAAQ==
-----END RSA PRIV` + `ATE KEY-----`

// ruleFixture is one built-in rule's positive (must produce a match) and negative (must not
// produce a match) example text.
type ruleFixture struct {
	name     string
	positive string
	negative string
}

// ruleFixtures covers every built-in rule category 00-ARCHITECTURE.md §5.22a names in prose.
func ruleFixtures() []ruleFixture {
	return []ruleFixture{
		{
			name:     "private_key_pem",
			positive: pemPositive,
			negative: "This function generates a private key for testing purposes.",
		},
		{
			name:     "aws_access_key",
			positive: probeSecret, // AWS's own documentation example access key ID
			negative: "The word AKIA alone is not a key.",
		},
		{
			name:     "github_token",
			positive: "token: ghp_" + "1234567890abcdefghijklmnopqrstuvwxyz12",
			negative: "token: ghz_1234567890abcdefghijklmnopqrstuvwxyz12",
		},
		{
			name:     "sk_style_api_key",
			positive: "ANTHROPIC_API_KEY=sk-ant-api03-" + "1234567890abcdefghijklmnopqrstuvwxyz",
			negative: `color_scheme = "skyblue"`,
		},
		{
			name:     "bearer_token",
			positive: "Authorization: Bearer eyJhbGciOiJI" + "UzI1NiIsInR5cCI6IkpXVCJ9.abc123def456",
			negative: "Authorization: Basic dXNlcjpwYXNz",
		},
		{
			name:     "password_assignment",
			positive: "password=Sup3rSecretPass!23",
			negative: "# password requirements: at least 8 characters",
		},
		{
			name:     "dotenv_value",
			positive: "API_SECRET=abcdef0123456789",
			negative: "# comment explaining what API_SECRET configures",
		},
		{
			name:     "jwt",
			positive: "eyJhbGciOiJI" + "UzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			negative: "eyJhbGciOiJIUzI1NiJ9",
		},
		{
			name:     "connection_string_credentials",
			positive: "postgres://admin:hunter2@db.internal:5432/prod",
			negative: "postgres://db.internal:5432/prod",
		},
	}
}

// runBuiltInRulesFireOnPositiveNotNegativeCase asserts, for every 00-ARCHITECTURE.md §5.22a
// built-in rule category, that Redact reports at least one match on the category's positive
// fixture and none on its negative fixture.
func runBuiltInRulesFireOnPositiveNotNegativeCase(t *testing.T, factory func(t *testing.T) redact.Redactor) {
	t.Helper()
	for _, f := range ruleFixtures() {
		t.Run(f.name, func(t *testing.T) {
			r := factory(t)

			_, posMatches := r.Redact([]byte(f.positive))
			require.NotEmpty(t, posMatches, "rule %q must fire on its positive fixture", f.name)

			_, negMatches := r.Redact([]byte(f.negative))
			require.Empty(t, negMatches, "rule %q must not fire on its negative fixture", f.name)
		})
	}
}

// runIdempotenceCase asserts Redact(Redact(x)) == Redact(x) (00-ARCHITECTURE.md §5.22a): running
// an already-redacted result back through Redact must be a no-op, so a placeholder never gets
// mistaken for a fresh secret.
func runIdempotenceCase(t *testing.T, factory func(t *testing.T) redact.Redactor) {
	t.Helper()
	r := factory(t)

	var all strings.Builder
	for _, f := range ruleFixtures() {
		all.WriteString(f.positive)
		all.WriteByte('\n')
	}

	out1, matches1 := r.Redact([]byte(all.String()))
	require.NotEmpty(t, matches1, "fixture sanity: the concatenated positive fixtures must produce matches")

	out2, _ := r.Redact(out1)
	require.Equal(t, out1, out2, "Redact(Redact(x)) must equal Redact(x)")
}

// manyOrdinaryLines is how many benign lines runBoundedGrowthCase pads its fixture with, so a
// single fixed-width placeholder cannot meaningfully move the input:output size ratio.
const manyOrdinaryLines = 200

// boundedGrowthSlack is the additive slack runBoundedGrowthCase allows beyond the input length:
// generous enough for any single fixed-width "«redacted:<rule>»" placeholder to replace a short
// match, without pinning an exact, currently-unspecified placeholder width.
const boundedGrowthSlack = 256

// runBoundedGrowthCase asserts Redact does not grow the input beyond a bounded factor
// (00-ARCHITECTURE.md §5.22a): a large body of ordinary text carrying one short embedded secret
// must not come back meaningfully larger, which is what keeps chunk boundaries stable between a
// redacted and an unredacted read of the same file.
func runBoundedGrowthCase(t *testing.T, factory func(t *testing.T) redact.Redactor) {
	t.Helper()
	r := factory(t)

	var b strings.Builder
	for i := 0; i < manyOrdinaryLines; i++ {
		b.WriteString("ordinary log line, nothing sensitive in it at all.\n")
	}
	b.WriteString("password=x\n")
	in := []byte(b.String())

	out, matches := r.Redact(in)
	require.NotEmpty(t, matches, "fixture sanity: the embedded password= assignment must match")
	require.LessOrEqual(t, len(out), len(in)+boundedGrowthSlack,
		"Redact must not grow the input beyond a bounded factor (§5.22a)")
}
