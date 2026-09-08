package admission_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/admission"
	"github.com/stretchr/testify/require"
)

// SP-21 commit 6's contracts: T21-ROLLBACK-01 and T21-QUALITY-01.
//
// T21-QUALITY-01's artifact is "a three-layer comparison report with predeclared margins, or an
// inconclusive result". Qompack ships the inconclusive result, and that is not a placeholder: there
// are no controlled held-out tasks and no observations, so every layer is inconclusive and
// admission is not enabled by any of this. What this commit delivers is the mechanism that reaches
// that verdict honestly and cannot be talked out of it.

// --- T21-ROLLBACK-01 ---------------------------------------------------------------------------

// TestRollbackOrderIsFixed pins the sequence the plan states: disable replacement, restore
// pass-through, verify the compatible reader or restore the backup, then preserve diagnostics and
// original captures for audit.
//
// The order is the safety property. Verifying a reader before replacement is disabled leaves the
// pipeline transforming while its schema is in question, and preserving evidence first would mean
// deciding what to keep before knowing which artifact survived.
func TestRollbackOrderIsFixed(t *testing.T) {
	require.Equal(t, []admission.RollbackStep{
		admission.StepDisableReplacement,
		admission.StepRestorePassThrough,
		admission.StepVerifyReaderOrRestore,
		admission.StepPreserveEvidence,
	}, admission.RollbackPlan(), "the rollback order is the safety property, not a suggestion")
}

// TestASchemaWriteNeedsACompatibleReaderOrAVerifiedBackup is M4-05's pre-write rule.
//
// Writing a new admission schema with neither a reader that can read it nor a backup to go back to
// is the one move with no way out: the old reader cannot parse the new artifact, and there is
// nothing to restore.
func TestASchemaWriteNeedsACompatibleReaderOrAVerifiedBackup(t *testing.T) {
	w := admission.SchemaWrite{From: "1", To: "2"}

	ok, why := admission.PermitSchemaWrite(w, nil, admission.Backup{})

	require.False(t, ok, "a write with no reader and no backup cannot be rolled back")
	require.Equal(t, admission.PermitNoReaderNoBackup, why)
}

// TestACompatibleReaderPermitsTheWrite is the first of the two ways forward.
func TestACompatibleReaderPermitsTheWrite(t *testing.T) {
	w := admission.SchemaWrite{From: "1", To: "2"}

	ok, why := admission.PermitSchemaWrite(w, []string{"2", "2"}, admission.Backup{})

	require.True(t, ok)
	require.Equal(t, admission.PermitReaderCompatible, why)
}

// TestOneLaggingReaderIsEnoughToRequireABackup is the rule that makes "compatible reader support"
// mean every deployed reader, not most of them.
//
// A single reader still on the old version is a process that will fail to read the new artifact,
// and averaging it away is how a rollout breaks one host and calls itself compatible.
func TestOneLaggingReaderIsEnoughToRequireABackup(t *testing.T) {
	w := admission.SchemaWrite{From: "1", To: "2"}

	ok, why := admission.PermitSchemaWrite(w, []string{"2", "2", "1"}, admission.Backup{})

	require.False(t, ok, "one reader that cannot read the new artifact is one too many")
	require.Equal(t, admission.PermitNoReaderNoBackup, why)

	// The same lagging fleet is fine once there is something to go back to.
	ok, why = admission.PermitSchemaWrite(w, []string{"2", "2", "1"},
		admission.VerifyBackup("backup-2026-09-08", "1"))
	require.True(t, ok)
	require.Equal(t, admission.PermitBackupVerified, why)
}

// TestAnAssumedBackupIsNotAVerifiedOne is the same construction rule Baseline uses, for the same
// reason: the dangerous input is a caller that names a backup it never checked.
//
// "There is a nightly backup" is a belief about a cron job. "This backup was verified" is a fact
// about bytes somebody read. Only the second may permit a write, and only VerifyBackup can produce
// it.
func TestAnAssumedBackupIsNotAVerifiedOne(t *testing.T) {
	assumed := admission.Backup{ID: "nightly", Version: "1"}
	require.False(t, assumed.Verified(),
		"a backup named in a composite literal has not been checked")

	ok, why := admission.PermitSchemaWrite(
		admission.SchemaWrite{From: "1", To: "2"}, nil, assumed)

	require.False(t, ok)
	require.Equal(t, admission.PermitNoReaderNoBackup, why)
	require.True(t, admission.VerifyBackup("nightly", "1").Verified())
	require.False(t, admission.VerifyBackup("", "1").Verified(),
		"an unnamed backup is not a backup, however it was constructed")
}

// TestADowngradeIsNeverSilent is the plan's "never silently downgrade a format".
//
// A write that moves the schema backwards is sometimes the right move, and it is never the
// accidental one. Requiring the caller to say so turns a mis-ordered version constant from a silent
// data-format regression into a refusal.
func TestADowngradeIsNeverSilent(t *testing.T) {
	down := admission.SchemaWrite{From: "2", To: "1"}
	backup := admission.VerifyBackup("backup-2026-09-08", "2")

	ok, why := admission.PermitSchemaWrite(down, []string{"1"}, backup)
	require.False(t, ok, "a backwards write must be declared, not inferred")
	require.Equal(t, admission.PermitUndeclaredDowngrade, why)

	down.Downgrade = true
	ok, _ = admission.PermitSchemaWrite(down, []string{"1"}, backup)
	require.True(t, ok, "a declared downgrade with a verified backup is permitted")
}

// TestPostWriteRollbackValidatesReadBackOrRestores is M4-05's post-write half: after the write, the
// drill runs again against the artifact that actually exists.
func TestPostWriteRollbackValidatesReadBackOrRestores(t *testing.T) {
	backup := admission.VerifyBackup("backup-2026-09-08", "1")

	t.Run("the new artifact reads back", func(t *testing.T) {
		res := admission.ValidateRollback(true, backup)
		require.True(t, res.OK)
		require.False(t, res.Restored, "nothing needs restoring when the artifact reads")
		require.True(t, res.EvidenceRetained)
	})

	t.Run("it does not, and the backup restores", func(t *testing.T) {
		res := admission.ValidateRollback(false, backup)
		require.True(t, res.OK)
		require.True(t, res.Restored)
		require.True(t, res.EvidenceRetained)
	})

	t.Run("it does not, and there is nothing to restore", func(t *testing.T) {
		res := admission.ValidateRollback(false, admission.Backup{})
		require.False(t, res.OK, "an unreadable artifact with no backup is not a completed rollback")
		require.False(t, res.Restored)
		require.True(t, res.EvidenceRetained,
			"evidence is retained even when the rollback failed — especially then")
	})
}

// TestRollbackNeverDiscardsEvidence is the plan's flat prohibition: never delete captures,
// diagnostics or evidence to make a retry look clean.
//
// It is asserted on the failing path as well as the passing one, because the failing path is where
// the temptation lives.
func TestRollbackNeverDiscardsEvidence(t *testing.T) {
	for _, readable := range []bool{true, false} {
		for _, b := range []admission.Backup{
			admission.VerifyBackup("backup", "1"), {},
		} {
			res := admission.ValidateRollback(readable, b)
			require.True(t, res.EvidenceRetained,
				"evidence is retained unconditionally; readable=%v backup=%q", readable, b.ID)
		}
	}
}

// TestDisablingAdmissionCannotDisableRecording is Qompack.md §7.1's independent kill switches,
// checked structurally rather than described.
//
// The claim in this package's doc comment is that turning admission off never turns capture off.
// The way that claim stays true is that admission's switch has no way to express it: Gate carries
// nothing that names recording, reinjection or experiments, so no rollback of admission can reach
// them. A field added here that did would make the doc comment false silently.
func TestDisablingAdmissionCannotDisableRecording(t *testing.T) {
	typ := reflect.TypeOf(admission.Gate{})

	for i := range typ.NumField() {
		name := strings.ToLower(typ.Field(i).Name)
		for _, forbidden := range []string{"record", "capture", "reinject", "experiment"} {
			require.NotContains(t, name, forbidden,
				"Gate.%s would let disabling admission reach a switch §7.1 keeps independent",
				typ.Field(i).Name)
		}
	}
}

// --- T21-QUALITY-01 ----------------------------------------------------------------------------

// TestTheShippedComparisonIsInconclusive is the honest disposition, as a test.
//
// There are no controlled held-out tasks and no observations, so the shipped comparison is
// inconclusive and enables nothing. Writing it down here means a later change that made the zero
// comparison look like a pass has to fail this first.
func TestTheShippedComparisonIsInconclusive(t *testing.T) {
	var shipped admission.Comparison

	require.Equal(t, admission.VerdictInconclusive, shipped.Verdict())
	require.False(t, shipped.Enables(),
		"an inconclusive comparison is not evidence of retained quality")
}

// TestALayerWithNoObservationsIsInconclusive keeps an empty sample from reading as a clean run.
//
// Zero held-out tasks with zero regressions is a perfect ratio and no information at all. The
// arithmetic answer and the honest answer differ here, and the honest one wins.
func TestALayerWithNoObservationsIsInconclusive(t *testing.T) {
	declared := admission.Predeclare(minRetention)

	require.Equal(t, admission.VerdictInconclusive, declared.Verdict(),
		"a predeclared margin with nothing measured against it decides nothing")
	require.Zero(t, declared.Observed)
}

// TestAFabricatedLayerCannotPass is the construction rule that makes the margin mean something.
//
// A LayerResult assembled as a composite literal has no predeclared margin — the field is
// unexported — so it is inconclusive whatever its counts say. The order the API permits is declare,
// then observe; there is no way to look at the numbers and choose a threshold afterwards.
func TestAFabricatedLayerCannotPass(t *testing.T) {
	fabricated := admission.LayerResult{Observed: 100, Retained: 100}

	require.Equal(t, admission.VerdictInconclusive, fabricated.Verdict(),
		"a perfect score against no declared margin is not a pass")

	require.Zero(t, admission.Predeclare(minRetention).Observed,
		"Predeclare starts a fresh measurement; a margin cannot be attached to existing counts")
}

// TestALayerRegressesBelowItsPredeclaredMargin is the ordinary case in both directions.
func TestALayerRegressesBelowItsPredeclaredMargin(t *testing.T) {
	require.Equal(t, admission.VerdictRetained,
		admission.Predeclare(minRetention).Observe(95, 100).Verdict())

	require.Equal(t, admission.VerdictRegressed,
		admission.Predeclare(minRetention).Observe(80, 100).Verdict())

	require.Equal(t, admission.VerdictRetained,
		admission.Predeclare(minRetention).Observe(90, 100).Verdict(),
		"exactly at the margin is not a regression; the margin is a minimum")
}

// TestTheVerdictIsTheWorstLayer is how the three layers combine.
//
// Not an average and not a majority: a comparison is only as good as its weakest layer, because
// each layer is a separate promise. Recoverability regressing while task completion improves is
// still a regression in recoverability, and averaging it away is how a real loss gets shipped.
func TestTheVerdictIsTheWorstLayer(t *testing.T) {
	pass := admission.Predeclare(minRetention).Observe(95, 100)
	fail := admission.Predeclare(minRetention).Observe(10, 100)
	empty := admission.Predeclare(minRetention)

	for name, tc := range map[string]struct {
		layers map[admission.Layer]admission.LayerResult
		want   admission.Verdict
	}{
		"all three retained": {
			layers: map[admission.Layer]admission.LayerResult{
				admission.LayerTaskCompletion: pass,
				admission.LayerConstraints:    pass,
				admission.LayerRecoverability: pass,
			},
			want: admission.VerdictRetained,
		},
		"one regressed outranks two retained": {
			layers: map[admission.Layer]admission.LayerResult{
				admission.LayerTaskCompletion: pass,
				admission.LayerConstraints:    pass,
				admission.LayerRecoverability: fail,
			},
			want: admission.VerdictRegressed,
		},
		"one inconclusive blocks two retained": {
			layers: map[admission.Layer]admission.LayerResult{
				admission.LayerTaskCompletion: pass,
				admission.LayerConstraints:    empty,
				admission.LayerRecoverability: pass,
			},
			want: admission.VerdictInconclusive,
		},
		"a regression outranks an inconclusive": {
			layers: map[admission.Layer]admission.LayerResult{
				admission.LayerTaskCompletion: fail,
				admission.LayerConstraints:    empty,
				admission.LayerRecoverability: pass,
			},
			want: admission.VerdictRegressed,
		},
		"a missing layer is inconclusive": {
			layers: map[admission.Layer]admission.LayerResult{
				admission.LayerTaskCompletion: pass,
				admission.LayerConstraints:    pass,
			},
			want: admission.VerdictInconclusive,
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := admission.Comparison{Layers: tc.layers}
			require.Equal(t, tc.want, c.Verdict())
			require.Equal(t, tc.want == admission.VerdictRetained, c.Enables())
		})
	}
}

// TestNoCostFieldCanInfluenceTheVerdict is Qompack.md §11.1's separation, checked structurally.
//
// "A cost-only result never enables admission." The way that stays true is that cost has nowhere to
// live: neither Comparison nor LayerResult carries a field naming cost, tokens or price, so no
// arithmetic in Verdict can reach one. A field added here that did would make the rule negotiable.
func TestNoCostFieldCanInfluenceTheVerdict(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(admission.Comparison{}),
		reflect.TypeOf(admission.LayerResult{}),
	} {
		for i := range typ.NumField() {
			name := strings.ToLower(typ.Field(i).Name)
			for _, forbidden := range []string{"cost", "token", "price", "spend", "saving"} {
				require.NotContains(t, name, forbidden,
					"%s.%s: cost is compared separately and never enables admission",
					typ.Name(), typ.Field(i).Name)
			}
		}
	}
}

// TestEnablingStillNeedsEverythingElse states the limit of what a retained verdict buys.
//
// Enables reports that the quality gate did not block enablement. It never reports that admission
// may be turned on: the switch is still off, the host allowlist is still empty, and every other T21
// gate still applies. Reading Enables as sufficient is the mistake this test exists to name.
func TestEnablingStillNeedsEverythingElse(t *testing.T) {
	pass := admission.Predeclare(minRetention).Observe(99, 100)
	c := admission.Comparison{Layers: map[admission.Layer]admission.LayerResult{
		admission.LayerTaskCompletion: pass,
		admission.LayerConstraints:    pass,
		admission.LayerRecoverability: pass,
	}}
	require.True(t, c.Enables())

	var shipped admission.Gate
	require.False(t, shipped.Enabled, "the feature switch is off regardless of any comparison")
	require.True(t, shipped.Allow.Empty(), "and the host allowlist is still empty")

	ok, _ := shipped.Admits(admission.Target{Schema: "qompack.retrieval", Version: "1"})
	require.False(t, ok, "a quality verdict does not admit anything by itself")
}

// TestLifecycleRunsFromOptInBackToPassThrough is the opt-in lifecycle end to end, over the real
// pipeline: off, on, then off again with the same delivery each time.
func TestLifecycleRunsFromOptInBackToPassThrough(t *testing.T) {
	r := newRecorder()

	off, err := admission.NewPipeline(admission.Gate{}, r.ports()).
		Admit(t.Context(), delivery)
	require.NoError(t, err)
	require.Equal(t, admission.ReasonDisabled, off.Reason)

	on, err := admission.NewPipeline(admitting, r.ports()).Admit(t.Context(), delivery)
	require.NoError(t, err)
	require.Equal(t, admission.OutcomeTransform, on.Outcome)

	rolledBack, err := admission.NewPipeline(admission.Gate{}, r.ports()).
		Admit(t.Context(), delivery)
	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rolledBack.Outcome,
		"disabling restores pass-through for the same delivery that was admitted a moment ago")
	require.Equal(t, admission.ReasonDisabled, rolledBack.Reason)
	require.Empty(t, rolledBack.Handle)
	require.Empty(t, rolledBack.Mark)
}

// TestRollbackAndQualityEnumsRenderDistinctNames covers the new String methods through the same
// exhaustive walk the other enums use.
func TestRollbackAndQualityEnumsRenderDistinctNames(t *testing.T) {
	t.Run("step", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.StepDisableReplacement.String(),
			admission.StepRestorePassThrough.String(),
			admission.StepVerifyReaderOrRestore.String(),
			admission.StepPreserveEvidence.String(),
		}, func(i int) string { return admission.RollbackStep(i).String() })
	})

	t.Run("verdict", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.VerdictInconclusive.String(),
			admission.VerdictRetained.String(),
			admission.VerdictRegressed.String(),
		}, func(i int) string { return admission.Verdict(i).String() })
	})

	t.Run("layer", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.LayerTaskCompletion.String(),
			admission.LayerConstraints.String(),
			admission.LayerRecoverability.String(),
		}, func(i int) string { return admission.Layer(i).String() })
	})

	t.Run("permit", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.PermitNoReaderNoBackup.String(),
			admission.PermitReaderCompatible.String(),
			admission.PermitBackupVerified.String(),
			admission.PermitUndeclaredDowngrade.String(),
		}, func(i int) string { return admission.PermitCause(i).String() })
	})
}

// minRetention is this file's predeclared margin: nine in ten held-out tasks must keep the
// unmodified result's answer. It is a test fixture, not a shipped threshold — the package declares
// no default margin, because a default is a threshold nobody predeclared.
const minRetention = 0.9
