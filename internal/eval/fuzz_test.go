package eval_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
)

// redactCorpusDir is testdata/corpora/evalredact/, the seed corpus this subplan owns: one file per
// redaction rule plus the shapes that broke a naive implementation — an already-redacted span, a
// credentialed URL whose password half also parses as an e-mail address, and text where two rules
// overlap.
const redactCorpusDir = "../../testdata/corpora/evalredact"

// FuzzRedact asserts the property the whole recorded tier depends on: redaction is idempotent.
//
// It has to be. An imported corpus gets re-imported, a session gets re-redacted before a release
// run, and a rule whose own replacement matches it again would keep chewing through already-clean
// text — turning "<REDACTED>" into "<<REDACTED>>" and eventually into something that no longer
// resembles the session it came from.
func FuzzRedact(f *testing.F) {
	entries, err := os.ReadDir(redactCorpusDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if b, readErr := os.ReadFile(filepath.Join(redactCorpusDir, e.Name())); readErr == nil {
				f.Add(string(b))
			}
		}
	}
	// Inline seeds so the target still has coverage if the corpus directory is unavailable.
	f.Add("")
	f.Add("nothing to see here")
	f.Add("alice@example.com")
	f.Add("<REDACTED>@host.internal")
	f.Add("postgres://admin:pw@db.internal:5432/app")
	f.Add("password: hunter2 and token: ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789")

	f.Fuzz(func(t *testing.T, text string) {
		s := eval.Session{
			ID: "fuzz",
			Turns: []eval.Turn{{
				Index: 0, Role: "user", Text: text, Tokens: core.Tokens(len(text)),
			}},
			Meta: map[string]string{"note": text},
		}

		once, _ := eval.Redact(s)
		twice, second := eval.Redact(once)

		if once.Turns[0].Text != twice.Turns[0].Text {
			t.Fatalf("Redact is not idempotent on Text:\n once: %q\ntwice: %q",
				once.Turns[0].Text, twice.Turns[0].Text)
		}
		if once.Meta["note"] != twice.Meta["note"] {
			t.Fatalf("Redact is not idempotent on Meta:\n once: %q\ntwice: %q",
				once.Meta["note"], twice.Meta["note"])
		}
		if second != 0 {
			t.Fatalf("a second Redact replaced %d further spans; the first pass was incomplete",
				second)
		}
		if len(once.Turns) != len(s.Turns) || once.Turns[0].Tokens != s.Turns[0].Tokens {
			t.Fatal("Redact changed a session's shape, which the replay numbers are computed from")
		}
	})
}
