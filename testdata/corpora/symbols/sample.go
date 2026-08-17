// Package gofixture is the TestExtract_Go input named in
// plans/V2-SP-04-chunking-canonicalization-and-symbols.md: a method, a plain function, a type
// declaration and a three-member const block, in exactly that source order, so the extractor's
// Offset-sorted output is func, func, type, const, const, const.
package gofixture

import "strings"

// Load returns the value stored under key and reports whether it was present. It is the method
// half of the fixture: the receiver group must not be mistaken for the symbol name, so the
// extractor has to skip "(s *Store)" before it reads "Load".
func (s *Store) Load(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	v, ok := s.data[key]
	if !ok {
		return "", false
	}
	if v == "" {
		return defaultName, true
	}
	return v, true
}

// Normalize is the plain-function half of the fixture: no receiver group at all, so the optional
// receiver group in the func rule has to match empty and still capture the right name.
func Normalize(in string) string {
	out := strings.TrimSpace(in)
	if out == "" {
		return defaultName
	}
	if len(out) > maxKeys {
		out = out[:maxKeys]
	}
	if !strings.HasPrefix(out, prefix) {
		out = prefix + out
	}
	return strings.ToLower(out)
}

// Store is the type half of the fixture. Its brace opens on the declaration line, so its span is
// brace-matched from the "type" keyword's line through the closing brace three lines later.
type Store struct {
	data map[string]string
	name string
}

// The const block is deliberately the last declaration in the file: a const-block member's span is
// its own line only, and that is true precisely because no unquoted brace follows it within four
// lines. Moving anything brace-bearing below this block would change three expected spans.
const (
	// defaultName is what Normalize returns for empty input.
	defaultName = "unnamed"

	// maxKeys bounds the length Normalize truncates to.
	maxKeys = 64

	// prefix is prepended to every stored key.
	prefix = "gofixture:"
)
