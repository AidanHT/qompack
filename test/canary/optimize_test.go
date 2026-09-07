package canary

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/paths"
)

// The three canaries in this file share one shape, and it is the shape M0-03 demands rather than a
// shortcut: they SKIP.
//
// Each probes a capability whose mechanism would change or suppress what the host does — replacing a
// delivered result, vetoing a compaction, reading usage telemetry. Establishing any of them requires
// provoking the behaviour in a live session, and M0-03's sentence is unambiguous: "Never conduct
// destructive/recovery capability probes in the active user session." So without a disposable target
// they record `skipped` with the reason, which leaves the capability unverified — which is exactly
// what keeps it disabled in the register.
//
// Even WITH QOMPACK_CANARY_TARGET set, these read a supplied artifact and record what it says. They
// do not provoke anything themselves. Enabling an optimization needs verified_in_target evidence
// gathered by a future authorized pass, not by this test binary.

// suppliedArtifact returns the path a disposable-target run may leave for a canary to read:
// $QOMPACK_CANARY_TARGET/<name>.json. The second result is false when no target was supplied or the
// artifact is absent.
func suppliedArtifact(name string) (string, bool) {
	dir, ok := disposableTarget()
	if !ok {
		return "", false
	}
	p := filepath.Join(dir, name+".json")
	if fi, err := os.Stat(paths.Long(p)); err != nil || fi.IsDir() {
		return "", false
	}
	return p, true
}

// optimizeCanary is the shared body: assert the register really has this capability off, then either
// skip with a reason or record the supplied artifact without interpreting it as a pass.
func optimizeCanary(t *testing.T, name string, c contract.Capability, skipReason string) {
	t.Helper()

	tgt := hostTarget(t)
	rec := Record{Name: name, Capability: c, Scope: ScopeInstalledSession, Target: tgt}

	// The precondition, checked before anything else: an optimization this build cannot verify must
	// be off. If that ever stops being true, the skip below would be hiding an enabled capability
	// rather than documenting a disabled one.
	reg := contract.DefaultCapabilityRegister()
	r, ok := reg.Get(c)
	require.True(t, ok, "%s is missing from the register", c)
	require.Equal(t, contract.ClassOptimize, r.Class)
	require.False(t, r.Enabled,
		"%s is enabled while its canary cannot run; M0-G2 requires a target canary artifact first", c)
	require.NotEmpty(t, r.Fallback, "%s is disabled, so the register must say what happens instead", c)

	artifact, has := suppliedArtifact(name)
	if !has {
		skipRecorded(t, rec, skipReason+" Fallback in force: "+r.Fallback)
	}

	// A supplied artifact is recorded, not believed. `degraded` is the strongest outcome this test
	// binary may write for an optimization: only a future authorized pass that actually observed the
	// mechanism may write `verified`, and only into the register's Status.
	rec.Outcome = OutcomeDegraded
	rec.Artifact = artifact
	rec.Reason = "a disposable-target artifact was supplied and is recorded as-is. This test did " +
		"not provoke the mechanism — probing it is forbidden in an active session — so the " +
		"artifact's own claim is not upgraded to verified_in_target here. Fallback in force: " + r.Fallback
	writeRecord(t, rec)
}

// TestCanary_NewResultReplacement probes replacing a newly delivered tool result before the model
// sees it (documented as updatedToolOutput, plans/MIGRATION-EVIDENCE.md E09).
//
// Provoking it means letting the plugin substitute a real tool result in a real session. That is the
// most consequential thing in the register and the least reversible, and it additionally requires
// exact supported output shapes and a coexisting-hook decision that SP-21 owns.
func TestCanary_NewResultReplacement(t *testing.T) {
	optimizeCanary(t, "new_result_replacement", contract.CapNewResultReplacement,
		"replacing a delivered tool result cannot be probed in an active session (M0-03), and the "+
			"exact supported output shapes are not established; the capability stays off pending "+
			"the SP-21 capture/retrieval/schema gate.")
}

// TestCanary_CompactionBlocking probes vetoing a compaction the host is about to perform.
//
// Even a canary that SUCCEEDED would not license enabling this. A working veto mechanism does not
// supply the recovery/proactive distinction that would make an automatic veto safe, and the register
// carries the invariant regardless of what any probe finds: automatic optimization veto stays off,
// and a manual compact is never blocked. That last promise is asserted here, not just recorded.
func TestCanary_CompactionBlocking(t *testing.T) {
	rec, ok := contract.DefaultCapabilityRegister().Get(contract.CapCompactionBlocking)
	require.True(t, ok)
	require.Contains(t, rec.Fallback, "manual compact never blocked",
		"whatever a canary finds, the register must go on promising this")

	optimizeCanary(t, "compaction_blocking", contract.CapCompactionBlocking,
		"blocking a compaction cannot be probed in an active session (M0-03), and a working "+
			"mechanism would still not establish a safe automatic veto: the recovery/proactive "+
			"distinction is unverified.")
}

// TestCanary_UsageAttribution probes attributing reported usage and cache categories to a request.
//
// Its skip is the one that most needs its reason kept: an unrunnable telemetry probe means coverage
// is UNKNOWN, and M0-G5's rule is that missing usage stays unknown rather than being zero-filled
// into a cost ledger. A skipped canary here is the reason no cost claim may be made, not a detail.
func TestCanary_UsageAttribution(t *testing.T) {
	tgt := hostTarget(t)
	rec := Record{Name: "usage_attribution", Capability: contract.CapUsageAttribution, Scope: ScopeInstalledSession, Target: tgt}

	reg := contract.DefaultCapabilityRegister()
	r, ok := reg.Get(contract.CapUsageAttribution)
	require.True(t, ok)
	require.Equal(t, contract.ClassRecord, r.Class)
	require.False(t, r.Enabled, "usage attribution is disabled as a source of cost until its coverage is known")

	artifact, has := suppliedArtifact("usage_attribution")
	if !has {
		skipRecorded(t, rec, "no disposable target supplied (QOMPACK_CANARY_TARGET) and the "+
			"host's usage telemetry cannot be read from this process; coverage stays UNKNOWN. "+
			"Missing categories remain missing — they are never zero-filled into a cost ledger, "+
			"and no invoice claim follows from an estimate. Fallback in force: "+r.Fallback)
	}

	rec.Outcome = OutcomeDegraded
	rec.Artifact = artifact
	rec.Reason = "a disposable-target usage artifact was supplied and is recorded as-is. Category " +
		"coverage is whatever that artifact states and no more; unreported categories stay unknown. " +
		"Fallback in force: " + r.Fallback
	writeRecord(t, rec)
}
