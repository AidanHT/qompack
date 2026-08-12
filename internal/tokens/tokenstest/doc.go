// Package tokenstest is the conformance suite for internal/tokens.Estimator
// (00-ARCHITECTURE.md §5.22, D9): one RunEstimatorSuite any factory-produced Estimator can be
// checked against, following Rule W-1 exactly — a /shape block that always runs, and a /behaviour
// block skipped when the factory under test is a stub.
//
// internal/tokens has no stub phase: SP-01 ships it as a real implementation from day one (it is
// listed under "real, full §5.2/§5.3/§5.20 surfaces" in the subplan's Produces section, not under
// "Stubs returning core.ErrNotImplemented"). RunEstimatorSuite still implements the probe/skip
// machinery every other <pkg>test suite uses, both so this suite is a template a future
// alternative Estimator implementation can be tested against unmodified, and so its own
// /shape+/behaviour split matches the other 21 conformance suites' shape. Against the real
// baseline estimator the probe always reports "not a stub", so the behaviour block always runs —
// which is the expected, correct outcome documented at every call site in this package.
package tokenstest
