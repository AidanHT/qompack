package canon_test

import "github.com/qompack/qompack/internal/canon"

// fakeCanon is a Canonicalizer whose match set is supplied by the test rather than derived from
// the input. Overlap resolution, the Strip gate, the non-growing guard and Applied ordering are
// all properties of the registry's accept loop, not of any particular rule, so pinning them
// against a real canonicalizer would test two things at once and would break every time a regex
// was tuned.
type fakeCanon struct {
	name       string
	appliesTo  func(tool, path string) bool
	matchSet   []canon.Match
	callsCount *int
}

// Name returns the double's configured name.
func (f fakeCanon) Name() string { return f.name }

// Applies defers to appliesTo, defaulting to "always applies" when the test did not care.
func (f fakeCanon) Applies(tool, path string) bool {
	if f.appliesTo == nil {
		return true
	}
	return f.appliesTo(tool, path)
}

// Matches returns the configured match set verbatim, deliberately ignoring in and o: gating on
// Options.Strip is the registry's job, and a double that filtered would hide a registry that had
// stopped doing it.
func (f fakeCanon) Matches(in []byte, o canon.Options) []canon.Match {
	if f.callsCount != nil {
		*f.callsCount++
	}
	return f.matchSet
}

// Canonicalize echoes its input. The double exists to be composed by Registry.Run, which never
// calls Canonicalize; this satisfies the interface and nothing more.
func (f fakeCanon) Canonicalize(in []byte, o canon.Options) (canon.Result, error) {
	return canon.Result{Canonical: in, Applied: []string{f.name}}, nil
}

// plainCanon implements Canonicalizer but NOT Matcher. It is the shape canontest's own
// registration-order probe uses, and the shape SP-01's frozen conformance suite requires Register
// to accept, so the registry must tolerate it rather than reject it.
type plainCanon struct{ name string }

// Name returns the double's configured name.
func (p plainCanon) Name() string { return p.name }

// Applies always reports true.
func (p plainCanon) Applies(tool, path string) bool { return true }

// Canonicalize echoes its input.
func (p plainCanon) Canonicalize(in []byte, o canon.Options) (canon.Result, error) {
	return canon.Result{Canonical: in}, nil
}

// mk builds one Match. It keeps the tables below readable at a glance.
func mk(off, length int, token string, class canon.Class) canon.Match {
	return canon.Match{Offset: off, Len: length, Token: []byte(token), Class: class}
}
