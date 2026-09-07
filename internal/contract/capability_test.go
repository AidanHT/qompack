package contract_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
)

// The capability register is SP-19 M0-03's answer to the question 00-ARCHITECTURE.md §12.1 now
// asks: which host capabilities does this build claim, on what evidence, and which of them is
// allowed to be ON. The nine §5.19 assertions answer a different question — did a mechanism fire
// this session — and conflating the two is exactly how "an unavailable producer reported OK" grew
// into "the capability is fine".
//
// Everything below is pinned against plans/MIGRATION-EVIDENCE.md's "Capability decisions" table and
// the SP-19 "Capability register" contract row. A change to either is a change here.

// TestDefaultCapabilityRegister_Validates is the load-bearing one: the register the build ships
// must satisfy its own rules. A default that cannot validate is a register nobody can trust to
// gate anything.
func TestDefaultCapabilityRegister_Validates(t *testing.T) {
	t.Parallel()
	require.NoError(t, contract.DefaultCapabilityRegister().Validate())
}

// TestDefaultCapabilityRegister_ListsTheEightCapabilitiesExactlyOnce pins the SET. SP-19's
// interface contract enumerates eight separate capabilities precisely so that evidence for one
// cannot be spent on another; a register that dropped or merged a row would silently re-create the
// conflation the plan exists to remove.
func TestDefaultCapabilityRegister_ListsTheEightCapabilitiesExactlyOnce(t *testing.T) {
	t.Parallel()

	want := []contract.Capability{
		contract.CapObservation,
		contract.CapInjection,
		contract.CapNewResultReplacement,
		contract.CapUsageAttribution,
		contract.CapTokenEstimation,
		contract.CapCompactionRequest,
		contract.CapCompactionBlocking,
		contract.CapHistoryRewriting,
	}
	require.Equal(t, want, contract.Capabilities(),
		"Capabilities() is the normative order the ledger's table states")

	reg := contract.DefaultCapabilityRegister()
	require.Len(t, reg.Records, len(want))

	seen := map[contract.Capability]int{}
	for _, rec := range reg.Records {
		seen[rec.Capability]++
	}
	for _, c := range want {
		require.Equal(t, 1, seen[c], "%s must appear exactly once in the default register", c)
	}
}

// TestDefaultCapabilityRegister_MatchesTheLedgerTable transcribes plans/MIGRATION-EVIDENCE.md's
// "Capability decisions" table as a literal, so the register cannot drift from the decision record
// without this test saying which row moved.
func TestDefaultCapabilityRegister_MatchesTheLedgerTable(t *testing.T) {
	t.Parallel()

	type row struct {
		class   contract.CapabilityClass
		status  contract.EvidenceStatus
		enabled bool
	}
	want := map[contract.Capability]row{
		// "Preserve recording; expose payload/relationship gaps": the observer exists and runs, and
		// its integration is not certified — implemented_unverified, on.
		contract.CapObservation: {contract.ClassRecord, contract.StatusImplementedUnverified, true},
		// "Tested SessionStart compact adapter only; no PostCompact prerequisite."
		contract.CapInjection: {contract.ClassAct, contract.StatusImplementedUnverified, true},
		// "Off pending SP-21 capture/retrieval/schema gate."
		contract.CapNewResultReplacement: {contract.ClassOptimize, contract.StatusDocumented, false},
		// "Unknown coverage; no zero-filled cost ledger."
		contract.CapUsageAttribution: {contract.ClassRecord, contract.StatusDocumented, false},
		// "Count whole serialized output; label estimates."
		contract.CapTokenEstimation: {contract.ClassRecord, contract.StatusImplementedUnverified, true},
		// "Unsupported adapter mechanism here; advisory cadence only."
		contract.CapCompactionRequest: {contract.ClassOptimize, contract.StatusUnsupported, false},
		// "Automatic optimization veto off; manual compact unblocked."
		contract.CapCompactionBlocking: {contract.ClassOptimize, contract.StatusDocumented, false},
		// "Excluded; ephemeral metadata is a future representation hint only."
		contract.CapHistoryRewriting: {contract.ClassOptimize, contract.StatusUnsupported, false},
	}

	reg := contract.DefaultCapabilityRegister()
	require.Equal(t, 1, reg.Version)
	for c, w := range want {
		rec, ok := reg.Get(c)
		require.True(t, ok, "%s missing from the default register", c)
		require.Equal(t, w.class, rec.Class, "%s: class", c)
		require.Equal(t, w.status, rec.Status, "%s: evidence status", c)
		require.Equal(t, w.enabled, rec.Enabled, "%s: enabled", c)
		require.NotEmpty(t, rec.Source, "%s: every row names where its evidence came from", c)
		require.True(t, rec.Target.Zero(),
			"%s: nothing in this build is verified_in_target, so no row may carry one", c)
	}
}

// TestDefaultCapabilityRegister_EveryOptimizeCapabilityIsDisabled is SP-19 exit criterion
// "unsupported controls remain disabled". No optimization has a target canary artifact yet (B01),
// so every one of them is off, and the register is what makes that checkable rather than a claim.
func TestDefaultCapabilityRegister_EveryOptimizeCapabilityIsDisabled(t *testing.T) {
	t.Parallel()

	reg := contract.DefaultCapabilityRegister()
	for _, rec := range reg.Records {
		if rec.Class != contract.ClassOptimize {
			continue
		}
		require.False(t, rec.Enabled,
			"%s is an optimization with no verified_in_target evidence; it may not ship enabled", rec.Capability)
		require.NotEmpty(t, rec.Fallback,
			"%s is disabled, so the register must say what happens instead", rec.Capability)
	}
}

// TestDefaultCapabilityRegister_UnsupportedMechanismsAreNamedAsSuch keeps the third of §12.1's
// three distinct outcomes visible: reported absence, unknown observation and UNSUPPORTED MECHANISM
// are not the same thing, and the two mechanisms this build has no adapter for say so.
func TestDefaultCapabilityRegister_UnsupportedMechanismsAreNamedAsSuch(t *testing.T) {
	t.Parallel()

	reg := contract.DefaultCapabilityRegister()
	for _, c := range []contract.Capability{contract.CapHistoryRewriting, contract.CapCompactionRequest} {
		rec, ok := reg.Get(c)
		require.True(t, ok)
		require.Equal(t, contract.StatusUnsupported, rec.Status, "%s", c)
		require.False(t, rec.Enabled, "%s", c)
	}
}

// TestDefaultCapabilityRegister_CompactionBlockingNeverBlocksManualCompact is the one promise
// Qompack.md §12 makes in prose that a user can actually be harmed by breaking: the plugin must
// never be the reason a manual /compact does not happen. The register carries it as data.
func TestDefaultCapabilityRegister_CompactionBlockingNeverBlocksManualCompact(t *testing.T) {
	t.Parallel()

	rec, ok := contract.DefaultCapabilityRegister().Get(contract.CapCompactionBlocking)
	require.True(t, ok)
	require.False(t, rec.Enabled)
	require.Contains(t, rec.Fallback, "manual compact never blocked")
	require.Contains(t, rec.Fallback, "automatic optimization veto off")
}

// TestDefaultCapabilityRegister_EnabledActingCapabilityNamesItsCanary: an acting capability that is
// ON must name the canary that would falsify it. "Injection works" with no canary is the exact
// shape of evidence M0-G2 refuses.
func TestDefaultCapabilityRegister_EnabledActingCapabilityNamesItsCanary(t *testing.T) {
	t.Parallel()

	rec, ok := contract.DefaultCapabilityRegister().Get(contract.CapInjection)
	require.True(t, ok)
	require.True(t, rec.Enabled)
	require.NotEmpty(t, rec.Canary)
	require.Contains(t, rec.Mechanism, "SessionStart")
	require.Contains(t, rec.Notes, "PostCompact",
		"the ledger's decision is explicit that reinjection has no PostCompact prerequisite")
}

// TestCapabilityRegister_Validate_RedCases gives every rule Validate enforces its own falsifying
// register. A validator whose rules are never observed to reject anything is decoration.
func TestCapabilityRegister_Validate_RedCases(t *testing.T) {
	t.Parallel()

	// mutate returns the default register with fn applied to the named capability's record.
	mutate := func(c contract.Capability, fn func(*contract.CapabilityRecord)) contract.CapabilityRegister {
		reg := contract.DefaultCapabilityRegister()
		recs := make([]contract.CapabilityRecord, len(reg.Records))
		copy(recs, reg.Records)
		for i := range recs {
			if recs[i].Capability == c {
				fn(&recs[i])
			}
		}
		reg.Records = recs
		return reg
	}

	target := contract.Target{Provider: "claude-code", Version: "2.1.263", Platform: "windows/amd64", Date: "2026-09-07"}

	cases := []struct {
		name string
		reg  contract.CapabilityRegister
		want string
	}{
		{
			name: "wrong version",
			reg: func() contract.CapabilityRegister {
				reg := contract.DefaultCapabilityRegister()
				reg.Version = 2
				return reg
			}(),
			want: "version",
		},
		{
			name: "a capability is missing",
			reg: func() contract.CapabilityRegister {
				reg := contract.DefaultCapabilityRegister()
				reg.Records = reg.Records[1:]
				return reg
			}(),
			want: string(contract.CapObservation),
		},
		{
			name: "a capability appears twice",
			reg: func() contract.CapabilityRegister {
				reg := contract.DefaultCapabilityRegister()
				reg.Records = append(append([]contract.CapabilityRecord(nil), reg.Records...), reg.Records[0])
				return reg
			}(),
			want: "twice",
		},
		{
			name: "an unknown capability appears",
			reg: func() contract.CapabilityRegister {
				reg := contract.DefaultCapabilityRegister()
				reg.Records = append(append([]contract.CapabilityRecord(nil), reg.Records...),
					contract.CapabilityRecord{Capability: "telepathy", Class: contract.ClassRecord, Status: contract.StatusUnknown})
				return reg
			}(),
			want: "telepathy",
		},
		{
			name: "a record is filed under the wrong class",
			reg: mutate(contract.CapCompactionBlocking, func(r *contract.CapabilityRecord) {
				r.Class = contract.ClassRecord
			}),
			want: "class",
		},
		{
			name: "an unrecognised evidence status",
			reg: mutate(contract.CapObservation, func(r *contract.CapabilityRecord) {
				r.Status = "probably-fine"
			}),
			want: "probably-fine",
		},
		{
			name: "an optimization is enabled on documented evidence",
			reg: mutate(contract.CapNewResultReplacement, func(r *contract.CapabilityRecord) {
				r.Enabled = true
			}),
			want: string(contract.StatusVerifiedInTarget),
		},
		{
			name: "an optimization is enabled as verified_in_target with no target",
			reg: mutate(contract.CapNewResultReplacement, func(r *contract.CapabilityRecord) {
				r.Enabled = true
				r.Status = contract.StatusVerifiedInTarget
				r.Canary = "TestCanary_NewResultReplacement"
			}),
			want: "target",
		},
		{
			name: "an optimization is enabled and verified against a target but names no source",
			reg: mutate(contract.CapNewResultReplacement, func(r *contract.CapabilityRecord) {
				r.Enabled = true
				r.Status = contract.StatusVerifiedInTarget
				r.Target = target
				r.Source = ""
			}),
			want: "source",
		},
		{
			name: "an acting capability is enabled on documentation alone",
			reg: mutate(contract.CapInjection, func(r *contract.CapabilityRecord) {
				r.Status = contract.StatusDocumented
			}),
			want: "documented",
		},
		{
			name: "an acting capability is enabled with no canary",
			reg: mutate(contract.CapInjection, func(r *contract.CapabilityRecord) {
				r.Canary = ""
			}),
			want: "canary",
		},
		{
			name: "an unsupported mechanism is enabled",
			reg: mutate(contract.CapCompactionRequest, func(r *contract.CapabilityRecord) {
				r.Enabled = true
			}),
			want: string(contract.StatusUnsupported),
		},
		{
			name: "an unknown mechanism is enabled",
			reg: mutate(contract.CapUsageAttribution, func(r *contract.CapabilityRecord) {
				r.Status = contract.StatusUnknown
				r.Enabled = true
			}),
			want: string(contract.StatusUnknown),
		},
		{
			name: "compaction blocking stops promising manual compact is never blocked",
			reg: mutate(contract.CapCompactionBlocking, func(r *contract.CapabilityRecord) {
				r.Fallback = "automatic optimization veto off"
			}),
			want: "manual compact never blocked",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.reg.Validate()
			require.Error(t, err, "this register must not validate")
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// TestCapabilityRegister_Get reports absence as absence — the caller must be able to tell "this
// build has no opinion about that capability" from "that capability is off".
func TestCapabilityRegister_Get(t *testing.T) {
	t.Parallel()

	reg := contract.DefaultCapabilityRegister()

	rec, ok := reg.Get(contract.CapTokenEstimation)
	require.True(t, ok)
	require.Equal(t, contract.CapTokenEstimation, rec.Capability)
	require.Contains(t, rec.Notes, "estimate")

	_, ok = reg.Get("telepathy")
	require.False(t, ok)

	_, ok = contract.CapabilityRegister{}.Get(contract.CapObservation)
	require.False(t, ok, "the zero register knows nothing; it does not report everything as off")
}
