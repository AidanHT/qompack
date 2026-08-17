package symbols_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/symbols"
)

// fuzzExtensions is the extension set FuzzExtract cycles through, so one mutated input exercises
// every dialect's rule list, comment syntax and block style rather than only the one its seed came
// from. The index is drawn from the fuzzed byte slice itself (see FuzzExtract) because
// testing.F.Fuzz only supports a fixed argument tuple, and adding a string argument would let the
// fuzzer waste its budget mutating the extension instead of the source.
var fuzzExtensions = []string{
	".go", ".ts", ".py", ".rs", ".java", ".c", ".rb", ".sh", ".php", ".md", ".zig",
}

// FuzzExtract asserts the one thing §5.22b promises for arbitrary input: Extract never panics, and
// every span it returns is inside the source it was handed. Both matter because every consumer
// (store.Query.Symbol, dag's shared-symbol edges, the mcp span widener) slices the original bytes
// with Offset and Len without re-checking them.
func FuzzExtract(f *testing.F) {
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
	// Inline seeds so the target still has coverage of the degenerate shapes even if the corpus
	// directory is unavailable (a copied single file, a trimmed checkout).
	f.Add([]byte(""))
	f.Add([]byte("func"))
	f.Add([]byte("func a() {"))
	f.Add([]byte("class A {\n  b() {\n"))
	f.Add([]byte("const (\n\ta = 1\n"))
	f.Add([]byte("s := `unterminated raw string {"))
	f.Add([]byte("'"))
	f.Add([]byte("/*"))

	ex := symbols.New()
	f.Fuzz(func(t *testing.T, data []byte) {
		ext := ""
		if len(data) > 0 {
			ext = fuzzExtensions[int(data[0])%len(fuzzExtensions)]
		}
		path := "fuzz" + ext

		got := ex.Extract(path, data)
		if got == nil {
			t.Fatal("Extract returned a nil slice")
		}
		if len(got) > symbols.MaxSymbols {
			t.Fatalf("Extract returned %d symbols, above the %d cap", len(got), symbols.MaxSymbols)
		}
		for i, s := range got {
			if s.Offset < 0 || s.Len <= 0 || s.Offset+s.Len > len(data) {
				t.Fatalf("symbol %d (%q) span [%d,%d) escapes a %d-byte source", i, s.Name, s.Offset, s.Offset+s.Len, len(data))
			}
			if s.Line < 1 {
				t.Fatalf("symbol %d (%q) has Line %d, want >= 1", i, s.Name, s.Line)
			}
			if i > 0 && got[i-1].Offset > s.Offset {
				t.Fatalf("symbol %d is out of Offset order: %d after %d", i, s.Offset, got[i-1].Offset)
			}
			// Enclosing must agree with Extract: an offset inside a span always resolves.
			if _, ok := ex.Enclosing(path, data, s.Offset); !ok {
				t.Fatalf("Enclosing(%d) found nothing although symbol %d (%q) covers it", s.Offset, i, s.Name)
			}
		}
	})
}
