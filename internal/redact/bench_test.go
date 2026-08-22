package redact_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/redact"
)

// benchPayloadBytes is the payload size the subplan budgets Redact against: 100 KB in ≤ 2 ms,
// because store.Put runs the redactor over every byte on the way in.
const benchPayloadBytes = 100 << 10

// buildPayload repeats lines until it reaches at least benchPayloadBytes, then trims to exactly
// that size so every benchmark prices the same number of bytes.
func buildPayload(lines []string) []byte {
	var b strings.Builder
	for i := 0; b.Len() < benchPayloadBytes; i++ {
		b.WriteString(lines[i%len(lines)])
		b.WriteByte('\n')
	}
	return []byte(b.String()[:benchPayloadBytes])
}

// noSecretLines are ordinary build/test output: the overwhelmingly common tool result, carrying no
// secret and none of the rules' mandatory literals.
var noSecretLines = []string{
	"=== RUN   TestChunkerSplitsOnBoundary",
	"    chunker_test.go:118: split 4096 bytes into 3 pieces",
	"--- PASS: TestChunkerSplitsOnBoundary (0.01s)",
	"go: downloading github.com/example/module v1.4.2",
	"compiling package internal/scheduler ... done in 41ms",
	"ok      github.com/example/project/internal/scheduler   0.312s",
	"INFO  worker: processed batch 8821, queue depth 3, latency 12ms",
	"WARN  cache: eviction ratio 0.42 above soft floor 0.35",
	"  at Object.<anonymous> (/home/runner/work/project/src/index.js:44:19)",
	"Compiled successfully in 2.4s. 812 modules transformed.",
}

// keywordNoSecretLines carry a rule's mandatory literal ("key", "token") in ordinary prose while
// containing no actual secret. This is the prefilter's worst realistic case: the literal scan hits,
// so the regex still runs, and it finds nothing.
var keywordNoSecretLines = []string{
	"INFO  cache: key eviction ratio 0.42 above soft floor 0.35",
	"    parser_test.go:91: unexpected token at offset 1284",
	"DEBUG index: primary key rebuilt for 8821 rows in 41ms",
	"--- PASS: TestTokenizerHandlesUnicode (0.01s)",
	"  the lexer emits one token per identifier, keyed by position",
	"WARN  config: unknown key \"retries\" ignored at line 12",
}

// withSecretLines are ordinary output with a realistic scattering of credentials — the shape of a
// deploy log or an accidentally-echoed environment dump.
var withSecretLines = []string{
	"INFO  deploy: starting rollout of revision 4f2a1c",
	"  aws identity resolved as AKIA" + "IOSFODNN7EXAMPLE",
	"compiling package internal/scheduler ... done in 41ms",
	"  connecting to postgres://deploy:s3cretpw@db.internal:5432/app",
	"INFO  worker: processed batch 8821, queue depth 3",
	"  Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345",
	"ok      github.com/example/project/internal/scheduler   0.312s",
	"STRIPE_SECRET_KEY=sk_live_" + "aaaaaaaaaaa",
	"  provider key sk-ant-api03-" + "1234567890abcdefghijklmnopqrstuvwxyz accepted",
	"  presented eyJhbGciOiJI" + "UzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
	"WARN  cache: eviction ratio 0.42 above soft floor 0.35",
	"  token: ghp_" + "1234567890abcdefghijklmnopqrstuvwxyz12",
}

// benchRedact is the shared body: it pins the payload size and reports bytes/op so the numbers are
// comparable across payload shapes.
func benchRedact(b *testing.B, payload []byte) {
	r := redact.New(config.Defaults())
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, _ := r.Redact(payload)
		if len(out) == 0 {
			b.Fatal("empty output")
		}
	}
}

// BenchmarkRedact_100KB_NoSecrets is the common case and the one the ≤ 2 ms budget is really
// about: a tool result with no secret in it at all.
func BenchmarkRedact_100KB_NoSecrets(b *testing.B) {
	benchRedact(b, buildPayload(noSecretLines))
}

// BenchmarkRedact_100KB_NoSecretsButKeyword is the prefilter's worst realistic case: ordinary
// output whose prose happens to contain "key" and "token", so the literal scan cannot skip the
// assignment rule.
func BenchmarkRedact_100KB_NoSecretsButKeyword(b *testing.B) {
	benchRedact(b, buildPayload(keywordNoSecretLines))
}

// BenchmarkRedact_100KB_WithSecrets carries a realistic scattering of real credentials, so most
// rules genuinely have to run and genuinely match.
func BenchmarkRedact_100KB_WithSecrets(b *testing.B) {
	benchRedact(b, buildPayload(withSecretLines))
}

// TestBenchPayloads_HaveTheSecretsTheyClaim keeps the benchmark fixtures honest: the no-secret
// payloads must produce zero matches and the with-secrets payload must produce many, or the
// numbers above would be measuring the wrong thing.
func TestBenchPayloads_HaveTheSecretsTheyClaim(t *testing.T) {
	r := redact.New(config.Defaults())

	for _, tc := range []struct {
		name  string
		lines []string
		want  bool
	}{
		{"no_secrets", noSecretLines, false},
		{"keyword_no_secrets", keywordNoSecretLines, false},
		{"with_secrets", withSecretLines, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ms := r.Redact(buildPayload(tc.lines))
			if tc.want && len(ms) == 0 {
				t.Fatalf("payload %s must carry secrets, got 0 matches", tc.name)
			}
			if !tc.want && len(ms) != 0 {
				t.Fatalf("payload %s must carry no secrets, got %d matches: %v",
					tc.name, len(ms), fmt.Sprint(ruleNamesOf(ms)[:min(5, len(ms))]))
			}
		})
	}
}
