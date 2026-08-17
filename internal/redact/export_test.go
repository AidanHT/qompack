package redact

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
)

// NewWithoutPrefilter returns the Redactor New(cfg) would return, with the mandatory-literal
// prefilter disabled so every rule's regex runs against every input unconditionally.
//
// It exists purely so a test can prove the prefilter is behaviour-neutral: the prefiltered and
// unfiltered paths must produce byte-identical output and identical matches for every input. That
// property is the whole safety argument for the optimization, and it is worth far more asserted
// than assumed — a literal that is not actually mandatory would silently stop redacting a secret,
// which is the one failure mode §13 invariant 7 cannot tolerate.
//
// This is a test-only seam (export_test.go is not compiled into the package's non-test build), not
// a production switch: there is deliberately no config key and no exported flag that could turn
// the prefilter off in a real store.
func NewWithoutPrefilter(cfg config.Config) Redactor {
	base, ok := NewWithObs(cfg, logging.Nop(), nil).(*rx)
	if !ok {
		panic("redact: NewWithObs must return *rx")
	}
	clone := &rx{enabled: base.enabled, rules: make([]rule, len(base.rules))}
	copy(clone.rules, base.rules)
	for i := range clone.rules {
		clone.rules[i].lits = nil // an empty literal set means "always run"
	}
	return clone
}
