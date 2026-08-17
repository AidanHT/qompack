package canon

// Canonicalizer strips one class of volatile content from one tool's output
// (00-ARCHITECTURE.md §5.6). Canonicalize MUST be idempotent:
// Canonicalize(Canonicalize(x).Canonical, o) == Canonicalize(x, o).
//
// Idempotence is achieved structurally rather than by convention: every built-in replaces a span
// with a token built from '<' and '>', characters that appear in no pattern, so no token can ever
// match a rule on a second pass. A canonicalizer added later must uphold the same property.
//
// Every built-in ALSO implements Matcher, which is what Registry.Run composes over; Canonicalize
// is the standalone path, and each built-in implements it by delegating to the same sort/accept/
// apply pass Run uses, so a canonicalizer behaves identically alone and inside the registry.
// Result.Signature is left zero by a standalone Canonicalize: only Run sees the fully
// canonicalized bytes, and a near-duplicate score for a partially canonicalized document would be
// a score for something that never gets stored.
type Canonicalizer interface {
	// Name identifies this canonicalizer; Registry.Register errors on a duplicate Name.
	Name() string
	// Applies reports whether this canonicalizer should run for tool's output at path.
	Applies(tool, path string) bool
	// Canonicalize strips this canonicalizer's class of content from in.
	Canonicalize(in []byte, o Options) (Result, error)
}
