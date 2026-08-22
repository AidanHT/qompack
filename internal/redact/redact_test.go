package redact_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/testutil"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/redact/redacttest"
)

// corpusDir is testdata/corpora/secrets/, relative to this package's own directory.
const corpusDir = "../../testdata/corpora/secrets"

// enabled returns the default configuration, which has runtime.redact.enabled = true, optionally
// carrying user patterns.
func enabled(patterns ...string) config.Config {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Patterns = patterns
	return cfg
}

// newR builds a Redactor from the default (enabled) configuration.
func newR(t *testing.T) redact.Redactor {
	t.Helper()
	return redact.New(enabled())
}

// capture is a logging.Logger that records every Loud line, so a test can assert on the
// rejection diagnostic without reaching for the process-wide LastLoud ring (which every other
// test in the binary also writes to).
type capture struct{ loud *[]string }

func (c capture) With(kv ...any) logging.Logger { return c }
func (c capture) Debug(msg string, kv ...any)   {}
func (c capture) Info(msg string, kv ...any)    {}
func (c capture) Warn(msg string, kv ...any)    {}
func (c capture) Error(msg string, kv ...any)   {}
func (c capture) Loud(msg string, kv ...any)    { *c.loud = append(*c.loud, msg) }

// newCapturing builds a Redactor wired to a capturing logger and a live metrics registry, so a
// test can assert both the Loud diagnostic and the redact.pattern_rejected counter.
func newCapturing(t *testing.T, patterns ...string) (redact.Redactor, *[]string, obs.Registry) {
	t.Helper()
	loud := &[]string{}
	reg := obs.New(core.SystemClock())
	r := redact.NewWithObs(enabled(patterns...), capture{loud: loud}, reg)
	return r, loud, reg
}

// ruleNamesOf projects matches down to their rule names, in match order.
func ruleNamesOf(ms []redact.Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Rule
	}
	return out
}

// readCorpus reads one testdata/corpora/secrets fixture.
func readCorpus(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpusDir, name))
	require.NoError(t, err, "corpus fixture missing: %s", name)
	require.NotEmpty(t, b)
	return testutil.ExpandSecretTokens(b)
}

// corpusNames lists every fixture in testdata/corpora/secrets, sorted.
func corpusNames(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(corpusDir)
	require.NoError(t, err)
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	require.NotEmpty(t, names)
	return names
}

// ── the ten built-in rules ───────────────────────────────────────────────────────────────────

// TestRedact_PEMBlock asserts the private-key rule swallows a whole PEM block — header, base64
// body and footer — as a single match.
func TestRedact_PEMBlock(t *testing.T) {
	in := readCorpus(t, "pem_private_key.txt")

	out, ms := newR(t).Redact(in)

	require.Len(t, ms, 1)
	require.Equal(t, "pem_private_key", ms[0].Rule)
	require.Contains(t, string(out), "«redacted:pem_private_key»")
	require.NotContains(t, string(out), "BEGIN RSA")
	require.NotContains(t, string(out), "END RSA")
}

// TestRedact_AWSKeys asserts both the AKIA and ASIA prefixes match, and that only the keys
// themselves are touched.
func TestRedact_AWSKeys(t *testing.T) {
	in := []byte("id1=AKIA" + "IOSFODNN7EXAMPLE id2=ASIA" + "IOSFODNN7EXAMPLE done")

	out, ms := newR(t).Redact(in)

	require.Len(t, ms, 2)
	require.Equal(t, []string{"aws_access_key_id", "aws_access_key_id"}, ruleNamesOf(ms))
	require.Equal(t,
		"id1=«redacted:aws_access_key_id» id2=«redacted:aws_access_key_id» done",
		string(out))
}

// TestRedact_GitHubTokens asserts all three GitHub credential shapes match. The classic-token
// quantifier is {36,} rather than {36}: redacttest's own frozen positive fixture carries 38 body
// characters, and a fixed {36} would refuse it on the trailing word boundary.
func TestRedact_GitHubTokens(t *testing.T) {
	in := []byte("a ghp_" + "1234567890abcdefghijklmnopqrstuvwxyz " +
		"b gho_" + "1234567890abcdefghijklmnopqrstuvwxyz " +
		"c github_pat_123456789012345678901234567890")

	_, ms := newR(t).Redact(in)

	require.Len(t, ms, 3)
	require.Equal(t, []string{"github_token", "github_token", "github_token"}, ruleNamesOf(ms))
}

// TestRedact_AnthropicBeforeGeneric asserts rule order: sk-ant- is labelled precisely rather than
// being swallowed by the generic sk- rule that follows it.
func TestRedact_AnthropicBeforeGeneric(t *testing.T) {
	in := []byte("key sk-ant-api03-" + "AAAABBBBCCCCDDDD here")

	_, ms := newR(t).Redact(in)

	require.Len(t, ms, 1)
	require.Equal(t, "anthropic_key", ms[0].Rule)
}

// TestRedact_JWT asserts a three-part JWT matches, including when it is the entire input: a tool
// result that IS nothing but a credential must still be redacted (§13 invariant 7).
func TestRedact_JWT(t *testing.T) {
	jwt := "eyJhbGciOiJI" + "UzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0." +
		"dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"

	_, ms := newR(t).Redact([]byte("saw " + jwt + " in the header"))
	require.Len(t, ms, 1)
	require.Equal(t, "jwt", ms[0].Rule)

	out, whole := newR(t).Redact([]byte(jwt))
	require.Len(t, whole, 1, "a bare JWT that is the whole input must still be redacted")
	require.Equal(t, "«redacted:jwt»", string(out))
}

// TestRedact_BearerValueOnly asserts the literal "Bearer " scheme survives and only its value is
// replaced, so a log line stays readable.
func TestRedact_BearerValueOnly(t *testing.T) {
	in := []byte("Authorization: Bearer abcdefghijklmnopqrstuvwx")

	out, ms := newR(t).Redact(in)

	require.Len(t, ms, 1)
	require.Equal(t, "bearer_token", ms[0].Rule)
	require.Equal(t, "Authorization: Bearer «redacted:bearer_token»", string(out))
}

// TestRedact_CredentialedURI asserts only the password component of a connection string is
// replaced — user, host, port and database all survive.
func TestRedact_CredentialedURI(t *testing.T) {
	in := []byte("postgres://app:h4nter2@db.internal:5432/prod")

	out, ms := newR(t).Redact(in)

	require.Len(t, ms, 1)
	require.Equal(t, "credentialed_uri", ms[0].Rule)
	require.Equal(t, "postgres://app:«redacted:credentialed_uri»@db.internal:5432/prod", string(out))
}

// TestRedact_AssignmentValueOnly asserts assignment keys survive verbatim while their values are
// replaced, for both the = and : separator spellings.
func TestRedact_AssignmentValueOnly(t *testing.T) {
	in := []byte("api_key = \"swordfishswordfish\"\nPASSWORD: hunter22\n")

	out, ms := newR(t).Redact(in)

	require.Len(t, ms, 2)
	require.Equal(t, []string{"assignment_secret", "assignment_secret"}, ruleNamesOf(ms))
	require.Equal(t,
		"api_key = «redacted:assignment_secret»\nPASSWORD: «redacted:assignment_secret»\n",
		string(out))
}

// TestRedact_DotenvGatedByKeyName asserts the .env rule is gated on the KEY/TOKEN/SECRET/… key-name
// vocabulary: Redact has no path argument, so an unguarded "NAME=value" rule would scrub every
// ordinary configuration line in a tool result.
func TestRedact_DotenvGatedByKeyName(t *testing.T) {
	in := []byte("PORT=8080\nFOO=1\nDATABASE_URL=postgres://u:s3cretpw@h/db\n" +
		"STRIPE_SECRET_KEY=sk_live_" + "aaaaaaaaaaa\n")

	out, ms := newR(t).Redact(in)

	require.Len(t, ms, 2)
	require.Contains(t, string(out), "PORT=8080", "a non-secret key name must be untouched")
	require.Contains(t, string(out), "FOO=1", "a non-secret key name must be untouched")
	require.NotContains(t, string(out), "s3cretpw")
	require.NotContains(t, string(out), "sk_live_"+"aaaaaaaaaaa")
}

// ── corpus-wide properties ──────────────────────────────────────────────────────────────────

// corpusExpect pins which rules fire, in order, on each committed fixture. It is the
// (rule, corpus file, match count) table the subplan asks for, executable rather than prose.
func corpusExpect() map[string][]string {
	return map[string][]string{
		"adversarial-already-redacted.txt": {},
		"anthropic_key.txt":                {"anthropic_key"},
		"assignment_secret.txt":            {"assignment_secret"},
		"aws_access_key_id.txt":            {"aws_access_key_id"},
		"bearer_token.txt":                 {"bearer_token"},
		"credentialed_uri.txt":             {"credentialed_uri"},
		"dotenv_value.txt":                 {"dotenv_value"},
		"generic_sk_key.txt":               {"generic_sk_key"},
		"github_token.txt":                 {"github_token"},
		"jwt.txt":                          {"jwt"},
		"mixed-deploy-log.txt":             {"aws_access_key_id", "credentialed_uri", "bearer_token"},
		"mixed-env-dump.txt":               {"github_token", "dotenv_value"},
		"pem_private_key.txt":              {"pem_private_key"},
	}
}

// TestRedact_CorpusRuleCounts asserts every built-in rule fires exactly once on its own fixture,
// and that the two mixed fixtures fire exactly the set they carry. The adversarial fixture — which
// already holds a placeholder plus two benign assignments — must produce no matches at all.
func TestRedact_CorpusRuleCounts(t *testing.T) {
	want := corpusExpect()
	r := newR(t)

	for _, name := range corpusNames(t) {
		t.Run(name, func(t *testing.T) {
			exp, ok := want[name]
			require.True(t, ok, "corpus fixture %s has no expectation; add one", name)

			_, ms := r.Redact(readCorpus(t, name))
			require.Equal(t, exp, ruleNamesOf(ms))
		})
	}
	require.Len(t, want, len(corpusNames(t)), "every expectation must correspond to a fixture")
}

// TestRedact_Idempotent asserts Redact(Redact(x)) == Redact(x) over every fixture, and that the
// second pass reports no matches at all — the placeholder guard's whole purpose.
func TestRedact_Idempotent(t *testing.T) {
	r := newR(t)
	for _, name := range corpusNames(t) {
		t.Run(name, func(t *testing.T) {
			once, _ := r.Redact(readCorpus(t, name))
			twice, ms2 := r.Redact(once)

			require.Equal(t, once, twice, "Redact(Redact(x)) must equal Redact(x)")
			require.Empty(t, ms2, "a second pass must find nothing: placeholders are immutable")
		})
	}
}

// TestRedact_ValidUTF8Output guards the placeholder writer against the rune-truncation trap: the
// closing » is U+00BB, two bytes in UTF-8, so appending it as a rune constant to a []byte would
// emit a lone 0xBB. That corrupts the output AND silently disables the idempotence guard, because
// the placeholder pattern can then never match what the writer produced.
func TestRedact_ValidUTF8Output(t *testing.T) {
	r := newR(t)
	for _, name := range corpusNames(t) {
		out, _ := r.Redact(readCorpus(t, name))
		require.True(t, utf8.Valid(out), "%s: Redact must emit valid UTF-8", name)
	}
}

// TestRedact_NeverWholeInputExceptPEM asserts no non-PEM rule spans an entire corpus fixture.
// Every fixture carries surrounding context, so a rule that swallowed all of it would be matching
// far more than the secret it is named for.
func TestRedact_NeverWholeInputExceptPEM(t *testing.T) {
	r := newR(t)
	for _, name := range corpusNames(t) {
		in := readCorpus(t, name)
		_, ms := r.Redact(in)
		for _, m := range ms {
			if m.Rule == "pem_private_key" {
				continue
			}
			require.False(t, m.Offset == 0 && m.Len == len(in),
				"%s: rule %s matched across the whole input", name, m.Rule)
		}
	}
}

// TestRedact_BoundedGrowth asserts the practical growth bound on real content: redaction must not
// meaningfully move a tool result's size, or chunk boundaries would shift between a redacted and
// an unredacted read of the same file.
func TestRedact_BoundedGrowth(t *testing.T) {
	r := newR(t)
	for _, name := range corpusNames(t) {
		in := readCorpus(t, name)
		out, _ := r.Redact(in)
		require.LessOrEqual(t, len(out), 2*len(in)+64, "%s: growth bound exceeded", name)
	}
}

// ── configuration ────────────────────────────────────────────────────────────────────────────

// TestRedact_Disabled asserts runtime.redact.enabled = false makes Redact the identity and Rules
// empty. store.Open still calls the redactor, so the choke point is never bypassed at the call
// site — only the rule set is empty.
func TestRedact_Disabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Redact.Enabled = false
	r := redact.New(cfg)

	in := readCorpus(t, "mixed-deploy-log.txt")
	out, ms := r.Redact(in)

	require.Equal(t, in, out)
	require.Empty(t, ms)
	require.Empty(t, r.Rules())
}

// TestNew_DisabledConfigPassesThrough pins the zero-value configuration: config.Config{} has
// runtime.redact.enabled false, so New returns a redactor that passes content through untouched.
func TestNew_DisabledConfigPassesThrough(t *testing.T) {
	r := redact.New(config.Config{})
	require.NotNil(t, r)
	require.Empty(t, r.Rules())

	in := []byte("AKIA" + "ABCDEFGHIJKLMNOP")
	out, matches := r.Redact(in)
	require.Equal(t, in, out)
	require.Empty(t, matches)
}

// TestNop pins redact.Nop to its §14.1 contract: input passes through byte-identical, with no
// matches and no rule names — even for input that looks exactly like a secret a real Redactor
// would catch. Nop is real (not a placeholder), so this test runs unconditionally.
func TestNop(t *testing.T) {
	r := redact.Nop()
	require.NotNil(t, r)

	require.Empty(t, r.Rules())

	cases := [][]byte{
		nil,
		{},
		[]byte("ordinary text, nothing sensitive here"),
		[]byte("AKIA" + "ABCDEFGHIJKLMNOP"), // looks exactly like an AWS access key ID
	}
	for _, in := range cases {
		out, matches := r.Redact(in)
		require.Equal(t, in, out, "Nop must pass input through byte-identical")
		require.Empty(t, matches, "Nop must never report a match")
	}
}

// TestRedact_Rules asserts the active rule set is reported, built-ins first, with user patterns
// appended under their custom:<i> names.
func TestRedact_Rules(t *testing.T) {
	require.Len(t, newR(t).Rules(), 10, "the ten built-in rules of §5.22a")

	withUser := redact.New(enabled(`INTERNAL-[0-9]{6}`))
	require.Equal(t, 11, len(withUser.Rules()))
	require.Equal(t, "custom:0", withUser.Rules()[10])
}

// ── user patterns ────────────────────────────────────────────────────────────────────────────

// TestRedact_UserPattern asserts an admitted runtime.redact.patterns entry fires under its
// custom:<i> name.
func TestRedact_UserPattern(t *testing.T) {
	r := redact.New(enabled(`INTERNAL-[0-9]{6}`))

	out, ms := r.Redact([]byte("case INTERNAL-004213 closed"))

	require.Len(t, ms, 1)
	require.Equal(t, "custom:0", ms[0].Rule)
	require.Equal(t, "case «redacted:custom:0» closed", string(out))
}

// TestRedact_InvalidUserPattern asserts an uncompilable pattern is logged once and skipped,
// never fatal, with every built-in rule still active.
func TestRedact_InvalidUserPattern(t *testing.T) {
	r, loud, reg := newCapturing(t, "((")

	require.Len(t, r.Rules(), 10, "the built-ins must survive a bad user pattern")
	require.Len(t, *loud, 1, "exactly one Loud line naming the rejected pattern")
	require.Equal(t, int64(1), reg.Counter("redact.pattern_rejected").Value())

	_, ms := r.Redact(readCorpus(t, "aws_access_key_id.txt"))
	require.Len(t, ms, 1, "built-in rules still fire")
}

// TestRedact_RejectsZeroWidthUserPattern asserts a pattern matching the empty string is refused.
// Admitting one would splice a placeholder between every byte of every input.
func TestRedact_RejectsZeroWidthUserPattern(t *testing.T) {
	r, loud, reg := newCapturing(t, "x*")

	require.Len(t, r.Rules(), 10)
	require.Len(t, *loud, 1)
	require.Equal(t, int64(1), reg.Counter("redact.pattern_rejected").Value())

	in := []byte(strings.Repeat("ordinary prose with nothing sensitive. ", 108))
	require.Greater(t, len(in), 4096)
	out, ms := r.Redact(in)
	require.Empty(t, ms)
	require.Equal(t, in, out)
}

// TestRedact_RejectsNarrowUserPattern asserts a pattern that can match fewer than three bytes is
// refused: it is what keeps the growth bound true.
func TestRedact_RejectsNarrowUserPattern(t *testing.T) {
	r, loud, reg := newCapturing(t, "[0-9]")

	require.Len(t, r.Rules(), 10)
	require.Len(t, *loud, 1)
	require.Equal(t, int64(1), reg.Counter("redact.pattern_rejected").Value())

	out, ms := r.Redact([]byte("build 7 finished"))
	require.Empty(t, ms, "a single digit must never be replaced")
	require.Equal(t, "build 7 finished", string(out))
}

// TestRedact_GrowthBoundHoldsUnderHostilePattern drives an ADMITTED three-byte-wide pattern across
// a dense input, the worst case the admission rules permit, and asserts the unconditional bound.
func TestRedact_GrowthBoundHoldsUnderHostilePattern(t *testing.T) {
	r := redact.New(enabled(`[0-9]{3}`))
	require.Len(t, r.Rules(), 11, "a three-byte-wide pattern must be ADMITTED")

	in := []byte(strings.Repeat("0123456789", 410))
	require.Greater(t, len(in), 4096)

	out, ms := r.Redact(in)

	require.NotEmpty(t, ms)
	require.LessOrEqual(t, len(out), 13*len(in)+37, "unconditional growth bound")
}

// ── determinism ──────────────────────────────────────────────────────────────────────────────

// secretAlphabet are the fragments PropRedactDeterministic builds inputs from, so generated bytes
// actually exercise the rules rather than almost always missing them.
var secretAlphabet = []string{
	"AKIA" + "IOSFODNN7EXAMPLE", "sk-ant-api03-" + "AAAABBBBCCCCDDDD", "password=", "hunter22",
	"Bearer abcdefghijklmnopqrstuvwx", "postgres://u:s3cretpw@h/db", "ordinary text ",
	"\n", "API_SECRET=abcdef0123456789", "«redacted:jwt»", " = ", "PORT=8080",
}

// TestRedact_Deterministic asserts two Redact calls on the same input produce identical output and
// identical matches. Determinism is load-bearing: chunk boundaries, and therefore dedup, depend on
// the same bytes redacting the same way every time.
func TestRedact_Deterministic(t *testing.T) {
	r := newR(t)
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 24).Draw(rt, "fragments")
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(rapid.SampledFrom(secretAlphabet).Draw(rt, "fragment"))
		}
		in := []byte(b.String())

		out1, ms1 := r.Redact(in)
		out2, ms2 := r.Redact(in)

		if string(out1) != string(out2) {
			rt.Fatalf("non-deterministic output for %q", in)
		}
		require.Equal(rt, ms1, ms2)
	})
}

// ── prefilter equivalence ────────────────────────────────────────────────────────────────────

// body36 and body22 are filler bodies of exactly the lengths the GitHub rules require.
const (
	body36 = "0123456789abcdefghijklmnopqrstuvwxyz"
	body22 = "0123456789abcdefghijkl"
)

// literalProbes maps every mandatory literal in the rule table to an input that (a) contains that
// literal, (b) contains a REAL secret the corresponding rule must match, and (c) contains no other
// literal that would let a different rule rescue the match.
//
// Property (c) is what gives the table teeth. Because each probe is isolated, deleting any single
// literal from the rule table makes exactly one of these produce zero matches on the prefiltered
// path while the unfiltered path still produces one — which TestPrefilter_EveryLiteralIsMandatory
// then reports. A probe list without isolation silently passes such a mutation, which is exactly
// the trap this table exists to avoid.
var literalProbes = map[string]string{
	"pem/PRIVATE KEY-----": pemProbe,

	"aws/AKIA": "id=AKIA" + "IOSFODNN7EXAMPLE",
	"aws/ASIA": "id=ASIA" + "IOSFODNN7EXAMPLE",

	"github/ghp_":        "t " + "ghp_" + body36,
	"github/gho_":        "t " + "gho_" + body36,
	"github/ghu_":        "t " + "ghu_" + body36,
	"github/ghs_":        "t " + "ghs_" + body36,
	"github/ghr_":        "t " + "ghr_" + body36,
	"github/github_pat_": "t " + "github_pat_" + body22,

	"anthropic/sk-ant-": "provider sk-ant-0123456789abcdef ready",
	"generic/sk-":       "provider sk-0123456789abcdefghij ready",

	"jwt/eyJ": "proof eyJhbGciOiJI" + "UzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N",

	"bearer/bearer": "Authorization: Bearer abcdefghijklmnopqrstuvwx",

	"uri/://": "dialing postgres://u:s3cretpw@h/db now",

	"assign/password": "password=abcdefgh",
	"assign/passwd":   "passwd=abcdefgh",
	"assign/secret":   "client_secret=abcdefgh",
	"assign/key":      "api_key=abcdefgh",
	"assign/token":    "token=abcdefgh",

	"dotenv/KEY":        "MY_KEY=abcdefghij",
	"dotenv/TOKEN":      "MY_TOKEN=abcdefghij",
	"dotenv/SECRET":     "MY_SECRET=abcdefghij",
	"dotenv/PASSWORD":   "MY_PASSWORD=abcdefghij",
	"dotenv/PASSWD":     "MY_PASSWD=abcdefghij",
	"dotenv/CREDENTIAL": "MY_CREDENTIAL=abcdefghij",
	"dotenv/DSN":        "MY_DSN=abcdefghij",
	"dotenv/PRIVATE":    "MY_PRIVATE=abcdefghij",
}

// pemProbe is a minimal but structurally complete private-key block.
const pemProbe = "-----BEGIN RSA PRIV" + "ATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD9\n-----END RSA PRIV" + "ATE KEY-----"

// TestPrefilter_EveryLiteralIsMandatory asserts each probe really does carry a secret the rules
// must catch, on BOTH paths. Combined with the isolation property described on literalProbes, this
// is what proves every literal in the table is genuinely mandatory rather than merely usually
// present: a non-mandatory literal makes the prefiltered path miss a secret the unfiltered path
// finds, and §13 invariant 7 has no tolerance for that.
func TestPrefilter_EveryLiteralIsMandatory(t *testing.T) {
	fast := redact.New(config.Defaults())
	slow := redact.NewWithoutPrefilter(config.Defaults())

	for label, probe := range literalProbes {
		t.Run(label, func(t *testing.T) {
			in := []byte(probe)

			_, slowMs := slow.Redact(in)
			require.NotEmpty(t, slowMs,
				"probe sanity: %q must carry a secret the rule set matches, or it proves nothing", probe)

			fastOut, fastMs := fast.Redact(in)
			slowOut, _ := slow.Redact(in)
			require.Equal(t, slowMs, fastMs,
				"the prefilter SKIPPED a rule that had a real match: literal %s is not mandatory", label)
			require.Equal(t, slowOut, fastOut)
		})
	}
}

// prefilterProbes are inputs chosen to sit exactly on the mandatory-literal boundaries: bare
// literals, wrong-case spellings and near misses. These complement literalProbes by exercising the
// side where NO match is expected.
var prefilterProbes = []string{
	"",
	"nothing sensitive here at all",
	"PRIVATE KEY-----", "private key-----", "-----BEGIN RSA PRIV" + "ATE KEY-----",
	"AKIA", "ASIA", "akiaiosfodnn7example", "AKIA" + "IOSFODNN7EXAMPLE",
	"ghp_", "GHP_1234567890abcdefghijklmnopqrstuvwxyz12", "github_pat_", "ghz_",
	"sk-ant-", "SK-ANT-", "sk-", "sk_", "sk-ant-api03-" + "AAAABBBBCCCCDDDD",
	"eyJ", "EYJ", "eyJhbGciOiJI" + "UzI1NiJ9.eyJzdWIiOiIxIn0.abcdefgh",
	"bearer", "BEARER", "BeArEr abcdefghijklmnopqrstuvwx", "bearerabc",
	"://", "postgres://u:s3cretpw@h/db", "postgres://h/db", "@",
	"password", "PASSWORD", "PaSsWoRd: hunter22", "passwd=abc", "secret", "SECRET",
	"key", "KEY", "token", "TOKEN", "api_key=abcd", "API-KEY: abcd", "client_secret=abcd",
	"refresh_token=abcdefgh", "AUTH_TOKEN=abcdefghij",
	"CREDENTIAL", "CREDENTIALS", "DSN", "PRIVATE", "MY_DSN=postgres://a/b1234",
	"MY_CREDENTIALS=abcdefghij", "export API_SECRET=abcdefghij",
	"«redacted:jwt»", "«redacted:jwt» password=x",
	"\xff\xfe binary \x00 noise", "ünïcödé påsswörd = hünter22",
}

// requireSameRedaction asserts the prefiltered and unfiltered redactors agree exactly.
func requireSameRedaction(t *testing.T, fast, slow redact.Redactor, in []byte, label string) {
	t.Helper()
	fastOut, fastMs := fast.Redact(in)
	slowOut, slowMs := slow.Redact(in)
	require.Equal(t, slowOut, fastOut, "%s: prefilter changed the output", label)
	require.Equal(t, slowMs, fastMs, "%s: prefilter changed the matches", label)
}

// TestPrefilter_IsBehaviourNeutral is the safety argument for the mandatory-literal prefilter:
// skipping a rule's regex when none of its literals is present must be indistinguishable from
// running it. A literal that is not truly mandatory would silently stop redacting a real secret,
// so this compares the two paths across the whole corpus, the benchmark payloads, and inputs
// sitting directly on each literal's boundary.
func TestPrefilter_IsBehaviourNeutral(t *testing.T) {
	fast := redact.New(config.Defaults())
	slow := redact.NewWithoutPrefilter(config.Defaults())

	t.Run("corpus", func(t *testing.T) {
		for _, name := range corpusNames(t) {
			requireSameRedaction(t, fast, slow, readCorpus(t, name), name)
		}
	})

	t.Run("bench_payloads", func(t *testing.T) {
		for label, lines := range map[string][]string{
			"no_secrets":         noSecretLines,
			"keyword_no_secrets": keywordNoSecretLines,
			"with_secrets":       withSecretLines,
		} {
			requireSameRedaction(t, fast, slow, buildPayload(lines), label)
		}
	})

	t.Run("literal_boundaries", func(t *testing.T) {
		for _, probe := range prefilterProbes {
			requireSameRedaction(t, fast, slow, []byte(probe), fmt.Sprintf("%q", probe))
		}
	})

	t.Run("user_patterns_are_never_prefiltered", func(t *testing.T) {
		cfg := enabled(`INTERNAL-[0-9]{6}`)
		requireSameRedaction(t, redact.New(cfg), redact.NewWithoutPrefilter(cfg),
			[]byte("case INTERNAL-004213 closed"), "custom:0")
	})
}

// ── conformance ──────────────────────────────────────────────────────────────────────────────

// TestRunRedactorSuite_AgainstRealRedactor runs the frozen §5.22 conformance suite against the
// real implementation. Its behaviour block is gated behind a stub probe, so this passing with the
// behaviour subtests actually RUNNING is the Rule W-1 flip SP-06 owes.
func TestRunRedactorSuite_AgainstRealRedactor(t *testing.T) {
	redacttest.RunRedactorSuite(t, "redact.New", func(t *testing.T) redact.Redactor {
		return redact.New(config.Defaults())
	})
}

// TestRedactorSuite_BehaviourIsNotSkipped is the mechanized Rule W-1 merge blocker: it proves the
// conformance suite's stub probe reports the real Redactor as real, so the behaviour block above
// is genuinely executed rather than silently skipped.
func TestRedactorSuite_BehaviourIsNotSkipped(t *testing.T) {
	_, ms := redact.New(config.Defaults()).Redact([]byte("aws_access_key_id = AKIA" + "IOSFODNN7EXAMPLE"))
	require.NotEmpty(t, ms, "redacttest.isStub must see a real Redactor, or every behaviour case is skipped")
}
