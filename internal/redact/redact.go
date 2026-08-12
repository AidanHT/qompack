package redact

import "github.com/qompack/qompack/internal/config"

// Redactor finds and replaces secrets in content on its way into the store
// (00-ARCHITECTURE.md §5.22a).
type Redactor interface {
	// Redact replaces every match with a fixed-width placeholder "«redacted:<rule>»". It MUST be
	// deterministic and MUST NOT grow the input beyond a bounded factor, so chunk boundaries stay
	// stable between a redacted and an unredacted read of the same file.
	Redact(in []byte) (out []byte, matches []Match)
	// Rules returns the names of every rule currently active.
	Rules() []string
}

// New returns a Redactor configured by cfg. Constructing always succeeds, so wave-0 composition
// roots can wire a redact.Redactor today, but the returned Redactor is a stub until SP-06 lands
// the real built-in rules and runtime.redact.patterns handling (00-ARCHITECTURE.md §5.22a):
// Redact returns in unchanged with no matches, and Rules returns nil.
//
// Returning the input UNREDACTED is the safe stub direction ONLY because nothing writes to the
// store yet — every producer of store.Put/PutBytes in this repository is itself still a stub
// (00-ARCHITECTURE.md §5.8), so there is no code path today that could persist an unredacted
// secret to objects/. SP-06 MUST implement the real rules before store.Put is wired to anything
// that can reach real content, per §13 invariant 7 ("no writes outside .qompack/" is necessary
// but not sufficient — secrets must never reach objects/ at all). Do not reuse this stub's
// behaviour as a template once store.Put exists: at that point an unredacted pass-through is a
// security defect, not a placeholder.
//
// New has no error return, matching every other "computational" constructor in this codebase
// (chunk.New, symbols.New, grammar.New): building a Redactor performs no I/O by itself, so there
// is nothing for a stub constructor to fail at.
func New(cfg config.Config) Redactor {
	return stubRedactor{}
}

// stubRedactor is the SP-01 placeholder Redactor returned by New. SP-06 owns the real
// implementation. See New's doc comment for why an unredacted pass-through is the correct stub
// behaviour today and why that will stop being true once store.Put is wired.
type stubRedactor struct{}

// Redact returns in unchanged with no matches. Redact has no error return, so this is Rule 1's
// documented zero-effort answer: the stub has found nothing, because it looked for nothing.
func (stubRedactor) Redact(in []byte) (out []byte, matches []Match) { return in, nil }

// Rules always returns nil: the stub has no rules active.
func (stubRedactor) Rules() []string { return nil }

// nopRedactor is Nop's real, permanent implementation: an honest no-op, not a placeholder for a
// future real one.
type nopRedactor struct{}

// Nop returns a Redactor that passes its input through unchanged, reporting no matches and an
// empty rule set. It is fully specified by the architecture, so SP-01 implements it for real
// rather than stubbing it (00-ARCHITECTURE.md §14.1): tests throughout the tree that need "a
// Redactor" but are not testing redaction itself (for example a store fixture, once SP-06 lands)
// need one that is honestly inert, not one that reports core.ErrNotImplemented on every call. Nop
// is for tests; production code always goes through New.
func Nop() Redactor { return nopRedactor{} }

// Redact returns in unchanged with no matches — always, by design, not as a stand-in for
// unimplemented behaviour.
func (nopRedactor) Redact(in []byte) (out []byte, matches []Match) { return in, nil }

// Rules always returns nil: Nop has no rules, by design.
func (nopRedactor) Rules() []string { return nil }
