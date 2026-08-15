package hookio_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/hookio"
)

// corpusDir is testdata/corpora/hookio/, the seed corpus this subplan owns: the seven hook
// payload shapes plus five malformed variants (truncated JSON, a top-level array, trailing
// garbage after a valid object, deep nesting, and invalid UTF-8).
const corpusDir = "../../testdata/corpora/hookio"

// FuzzReadEvent asserts the one property ReadEvent promises for arbitrary input: it never panics,
// regardless of how malformed, truncated, or adversarial the payload is.
func FuzzReadEvent(f *testing.F) {
	entries, err := os.ReadDir(corpusDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if b, readErr := os.ReadFile(filepath.Join(corpusDir, e.Name())); readErr == nil {
				f.Add(b)
			}
		}
	}
	// A few inline seeds so the fuzz target still has coverage if the corpus directory is ever
	// unavailable (e.g. a bare `go test` run from a copied single file).
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Add([]byte(`{"tool_input":null,"prompt":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, raw, _ := hookio.ReadEvent(bytes.NewReader(data), defaultTestLimit)
		if len(data) <= defaultTestLimit && len(raw) != len(data) {
			t.Fatalf("raw bytes length = %d, want %d for a payload under the limit", len(raw), len(data))
		}
	})
}
