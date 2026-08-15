package redact

import (
	"fmt"
	"regexp"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// rule is one compiled detection rule: the name that appears in its placeholder and in Match.Rule,
// the pattern, which submatch group Redact actually replaces, and the mandatory-literal prefilter
// that decides whether the pattern is worth running at all.
type rule struct {
	name string
	re   *regexp.Regexp
	// group is the submatch index whose span is replaced. 0 means the whole match; a positive
	// value replaces only that capture, which is what keeps "Bearer ", "password=" and a
	// connection string's user/host/port readable while their secret component is scrubbed.
	group int
	// lits are byte strings this rule CANNOT match without, with OR semantics: if none of them is
	// present in the input, no match is possible and the regex is skipped. An empty slice means
	// "always run".
	//
	// This is a pure performance gate and never a correctness one. Go's regexp is a non-JIT RE2
	// engine at roughly 3 MB/s here, while bytes.Contains is SIMD-optimized at roughly 1 GB/s, so
	// on the overwhelmingly common secret-free tool result the ten scans collapse to ten cheap
	// substring searches. Every literal below is mandatory by construction — see each rule's
	// comment — and TestPrefilter_IsBehaviourNeutral plus FuzzRedactPrefilterEquivalence assert
	// bit-identical output against a forced no-prefilter path.
	lits [][]byte
	// fold tests lits against an ASCII-lowercased copy of the input rather than the input itself.
	// It is set for exactly the rules whose pattern carries (?i).
	fold bool
}

// lits builds a rule's mandatory-literal set.
func lits(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

// wholeMatch is rule.group's value for a rule that replaces its entire match.
const wholeMatch = 0

// builtinRuleCount is the number of rules 00-ARCHITECTURE.md §5.22a enumerates. §5.22a names nine
// secret families; sk-ant- is split out ahead of the generic sk- rule so an Anthropic key is
// labelled precisely rather than by the catch-all, which makes ten.
const builtinRuleCount = 10

// builtinRules returns the §5.22a rule table in its normative application order. Order is
// load-bearing in exactly one place — anthropic_key precedes generic_sk_key, so an sk-ant- key is
// labelled as itself — but it is fixed for every rule so that Match.Rule, and therefore the
// placeholder text and every golden that embeds it, is deterministic.
//
// Two patterns deviate from the subplan's literal table, both forced by the frozen redacttest
// conformance fixtures (internal/redact/redacttest/behaviour.go), which are the specification:
//
//   - github_token quantifies the classic-token body {36,} rather than {36}. redacttest's positive
//     fixture carries 38 body characters; a fixed {36} matches the first 36 and then fails the
//     trailing \b, so the rule would not fire on its own conformance fixture at all.
//
//   - assignment_secret accepts a bare "token" key only with "=", never ":". redacttest's
//     github_token NEGATIVE fixture is "token: ghz_1234567890abcdefghijklmnopqrstuvwxyz12" and the
//     suite requires it to produce no match whatsoever. §5.22a writes the family as "password=,
//     secret=, token= assignments", so honouring the ":" spelling only for unambiguous key names
//     is faithful to the architecture and satisfies the fixture.
func builtinRules() []rule {
	mk := func(name, pattern string, group int, prefilter [][]byte, fold bool) rule {
		return rule{
			name: name, re: regexp.MustCompile(pattern), group: group,
			lits: prefilter, fold: fold,
		}
	}
	rules := make([]rule, 0, builtinRuleCount)
	rules = append(rules,
		// The pattern requires "-----BEGIN [A-Z ]*PRIVATE KEY-----", so the closing half of that
		// header is mandatory. Matching on the header rather than on "BEGIN" alone also keeps
		// ordinary prose about private keys off the regex.
		mk("pem_private_key",
			`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`,
			wholeMatch, lits("PRIVATE KEY-----"), false),

		// The alternation is anchored on exactly these two prefixes.
		mk("aws_access_key_id",
			`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`,
			wholeMatch, lits("AKIA", "ASIA"), false),

		// Every alternative begins with one of these six literal prefixes.
		mk("github_token",
			`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b|\bgithub_pat_[A-Za-z0-9_]{22,}\b`,
			wholeMatch, lits("ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"), false),

		mk("anthropic_key",
			`\bsk-ant-[A-Za-z0-9_\-]{16,}`,
			wholeMatch, lits("sk-ant-"), false),

		mk("generic_sk_key",
			`\bsk-[A-Za-z0-9]{20,}`,
			wholeMatch, lits("sk-"), false),

		mk("jwt",
			`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`,
			wholeMatch, lits("eyJ"), false),

		// (?i) on a literal scheme name: fold, so any casing of "Bearer" is found.
		mk("bearer_token",
			`(?i)\bbearer\s+([A-Za-z0-9\-._~+/]{20,}={0,2})`,
			1, lits("bearer"), true),

		// The pattern mandates BOTH "://" and a later "@". OR semantics cannot express the
		// conjunction, so this prefilters on "://" alone — correct, merely weaker than it could be.
		mk("credentialed_uri",
			`\b[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s/@:]+:([^\s/@]{3,})@`,
			1, lits("://"), false),

		// Every key alternative contains one of these five: api[_-]?key and access[_-]?key contain
		// "key"; client[_-]?secret contains "secret"; auth/access/refresh/api[_-]?token and the bare
		// "token" alternative all contain "token". (?i), so fold.
		mk("assignment_secret",
			`(?i)\b(?:(?:password|passwd|secret|api[_-]?key|access[_-]?key|client[_-]?secret`+
				`|auth[_-]?token|access[_-]?token|refresh[_-]?token|api[_-]?token)\b[ \t]*[:=]`+
				`|token\b[ \t]*=)[ \t]*("[^"\n]{4,}"|'[^'\n]{4,}'|[^\s,;"'\n]{1,})`,
			1, lits("password", "passwd", "secret", "key", "token"), true),

		// The key-name gate is a literal alternation and the pattern has no (?i), so these are
		// mandatory as written. CREDENTIALS is omitted because CREDENTIAL is a prefix of it.
		mk("dotenv_value",
			`(?m)^[ \t]*(?:export[ \t]+)?[A-Z][A-Z0-9_]*`+
				`(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|CREDENTIALS|DSN|PRIVATE)`+
				`[A-Z0-9_]*[ \t]*=[ \t]*([^\s#][^\n]{7,})$`,
			1, lits("KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "DSN", "PRIVATE"), false),
	)
	return rules
}

// customRuleFormat is the name a runtime.redact.patterns entry is reported under, by index.
const customRuleFormat = "custom:%d"

// asciiProbeLimit is the number of single-byte strings admissible tests a candidate pattern
// against: every 7-bit ASCII byte. A pattern that matches any one of them can produce a
// one-byte match, which is what the minimum-width admission rule exists to refuse.
const asciiProbeLimit = 128

// compileUserPatterns compiles cfg's runtime.redact.patterns into rules named custom:<i>.
//
// Rejection is per-pattern and never fatal: an operator's typo must not disable the built-in rule
// set that keeps secrets out of objects/. Each rejected pattern produces exactly one Loud line
// naming the pattern and the reason, and increments redact.pattern_rejected.
func compileUserPatterns(patterns []string, log logging.Logger, m obs.Registry) []rule {
	out := make([]rule, 0, len(patterns))
	for i, pat := range patterns {
		name := fmt.Sprintf(customRuleFormat, i)
		re, err := regexp.Compile(pat)
		if err != nil {
			reject(log, m, name, pat, fmt.Sprintf("does not compile: %v", err))
			continue
		}
		if reason, ok := admissible(re); !ok {
			reject(log, m, name, pat, reason)
			continue
		}
		out = append(out, rule{name: name, re: re, group: wholeMatch})
	}
	return out
}

// admissible reports whether re is safe to run over arbitrary content, and why not when it is not.
//
// The two refusals are the same refusal seen from different sides. A pattern that matches the
// empty string would splice a placeholder between every byte of every input; a pattern that can
// match a single byte would do the same thing one byte at a time. Both destroy the growth bound
// §5.22a requires, and with it the chunk-boundary stability that makes dedup work. Width is proven
// rather than assumed: the pattern is run against the empty string and against every single-byte
// ASCII string, and admitted only if it matches none of them.
func admissible(re *regexp.Regexp) (string, bool) {
	if re.MatchString("") {
		return "matches the empty string, so it would match everywhere", false
	}
	for b := 0; b < asciiProbeLimit; b++ {
		if re.FindStringIndex(string(rune(b))) != nil {
			return fmt.Sprintf("can match a single byte (%q), but a rule must be at least %d bytes wide",
				string(rune(b)), minMatchBytes), false
		}
	}
	return "", true
}

// patternRejectedCounter is the metric name /qompack:status surfaces so a silently-skipped user
// pattern is still visible to an operator.
const patternRejectedCounter = "redact.pattern_rejected"

// reject logs and counts one refused user pattern.
func reject(log logging.Logger, m obs.Registry, name, pattern, reason string) {
	if log != nil {
		log.Loud("redact: refusing runtime.redact.patterns entry",
			"rule", name, "pattern", pattern, "reason", reason)
	}
	if m != nil {
		m.Counter(patternRejectedCounter).Add(1)
	}
}
