package canary

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
)

// TestMain owns the one `go build` this package performs, removing it after the last test.
func TestMain(m *testing.M) {
	code := m.Run()
	removeBuild()
	os.Exit(code)
}

// TestCanary_HostInventory is plans/MIGRATION-EVIDENCE.md B01's first named action: "Inventory
// installed Claude Code/provider/OS". It establishes nothing about any capability, and that is the
// point — it produces the target every OTHER record is attributed to.
//
// It is read-only. `claude --version` starts no session, opens no project and changes nothing.
func TestCanary_HostInventory(t *testing.T) {
	tgt := hostTarget(t)
	rec := Record{Name: "host_inventory", Capability: contract.CapObservation, Scope: ScopeInstalledCLI, Target: tgt}

	if tgt.Provider == "unknown" {
		skipRecorded(t, rec, "no `claude` executable on PATH; the host provider and version are unknown, "+
			"so no canary in this run may be attributed to a target")
	}

	rec.Outcome = OutcomeVerified
	rec.Reason = "inventory only: the Claude CLI answered `--version` on this platform. This records " +
		"WHICH host is installed; it establishes nothing about what that host supports, and no " +
		"capability may be enabled on it."
	writeRecord(t, rec)

	require.NotEmpty(t, tgt.Version, "an inventoried host must carry a version")
	require.NotEmpty(t, tgt.Platform)
	require.NotEmpty(t, tgt.Date, "evidence without a date cannot be aged out")

	// The register this build ships must not already claim a verified target. Nothing here has
	// been certified, and M0-G2 stays blocked until a canary does it.
	for _, r := range contract.DefaultCapabilityRegister().Records {
		require.True(t, r.Target.Zero(),
			"%s claims target %s; no canary in this repository has verified anything in a target",
			r.Capability, r.Target)
	}
}

// TestCanary_RegisterIsInternallyConsistent is the cheap gate the rest of the package leans on: the
// register the canaries report against must satisfy its own rules before any of them runs. A
// canary attributed to an invalid register would be evidence about nothing.
func TestCanary_RegisterIsInternallyConsistent(t *testing.T) {
	t.Parallel()
	require.NoError(t, contract.DefaultCapabilityRegister().Validate())
}
