// Package redacttest is the conformance suite for redact.Redactor (00-ARCHITECTURE.md §5.22):
// every implementation SP-06 ships must pass RunRedactorSuite. SP-01 ships the suite itself,
// including the behaviour assertions SP-06 inherits (Rule W-1) — only the guarded /behaviour block
// is skipped until a real Redactor lands.
package redacttest

import (
	"testing"

	"github.com/qompack/qompack/internal/redact"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// probeSecret is AWS's own long-standing documentation example access key ID: a canonical,
// unambiguous positive fixture for the built-in AKIA…/ASIA… rule, used only to probe for
// stub-ness (see isStub) — the full built-in-rule coverage lives in behaviour.go.
const probeSecret = "aws_access_key_id = @@SEC_AWS_AKID@@"

// RunRedactorSuite is the conformance suite for redact.Redactor. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Redactor on
// every call.
func RunRedactorSuite(t *testing.T, name string, factory func(t *testing.T) redact.Redactor) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		r := factory(t)
		require.NotNil(t, r)

		// Neither Redact nor Rules has an error return (§5.22a); any value they produce is
		// shape-valid, so this only asserts that calling each does not panic.
		_, _ = r.Redact([]byte("shape probe"))
		_ = r.Rules()
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("idempotent", func(t *testing.T) { runIdempotenceCase(t, factory) })
		t.Run("bounded_growth", func(t *testing.T) { runBoundedGrowthCase(t, factory) })
		t.Run("built_in_rules_fire_on_positive_not_negative", func(t *testing.T) {
			runBuiltInRulesFireOnPositiveNotNegativeCase(t, factory)
		})
		t.Run("rules_lists_active_rule_names", func(t *testing.T) {
			r := factory(t)
			require.NotEmpty(t, r.Rules(), "a real Redactor must expose at least its built-in rule names")
		})
	})
}

// isStub reports whether factory currently produces a stub Redactor, using Redact as the probe
// (plans/OWNERS.tsv: redact's probe method is Redact). Redact has no error return (§5.22a), so
// unlike most suites in this tree, isStub cannot check core.IsNotImplemented: instead it relies
// directly on Rule 1's documented stub contract (the SP-01 stub echoes its input with no matches).
// probeSecret is a canonical, unambiguous positive fixture for the built-in AWS-access-key rule,
// so any real Redactor must report at least one match for it.
func isStub(t *testing.T, factory func(t *testing.T) redact.Redactor) bool {
	t.Helper()
	_, matches := factory(t).Redact([]byte(probeSecret))
	return len(matches) == 0
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Redactor, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) redact.Redactor) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
