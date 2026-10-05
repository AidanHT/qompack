package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/obs"
)

func TestRacePackagesRetainsAllNonE2ECoverage(t *testing.T) {
	pkgs, err := racePackages([]byte("example/internal/daemon\r\nexample/test/e2e\r\nexample/test/integration\nexample/test/e2ehelpers\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"example/internal/daemon", "example/test/integration", "example/test/e2ehelpers"}, pkgs)
	for _, list := range []string{"", "\n", "example/test/e2e\n"} {
		_, err := racePackages([]byte(list))
		require.Error(t, err, "empty selection must not silently fall back to testing the current directory")
	}
}

// TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration pins the `go test` commands `devtool
// test` runs. Every package but test/e2e runs in one parallel pass that declares co-load (and takes
// back a non-reference-disk declaration: one cause per run); test/e2e then runs alone with the
// co-load declaration taken back, as ADR 0010 decision 4 and ci.yml's `test-e2e` job run it. Each
// runs at passTimeout's hang guard (TestTestInvocations_GiveTheIsolatedPassItsOwnHangGuard).
// Before this, taskTest ran `go test ./...` with test/e2e inside the co-loaded pass, the condition
// under which its live-path rows' hooks take the designed degrade to the client spool (the wave 16
// ci seat's review).
func TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration(t *testing.T) {
	e2e := modulePath + "/test/e2e"
	core, guards := modulePath+"/internal/core", modulePath+"/test/guards"
	got := testInvocations([]string{core, e2e, guards})
	require.Len(t, got, 2, "one shared pass and test/e2e alone: %+v", got)

	timeout := "-timeout=" + wholeTreeTestTimeout
	require.Equal(t, []string{"test", timeout, core, guards}, got[0].args)
	require.Equal(t, map[string]string{obs.UnderColoadEnv: "1", obs.NonReferenceDiskEnv: ""}, got[0].env,
		"the shared pass is co-loaded and says so, and names no second cause")

	require.Equal(t, []string{"test", "-timeout=" + isolatedTestTimeout, e2e}, got[1].args)
	require.Equal(t, map[string]string{obs.UnderColoadEnv: ""}, got[1].env,
		"test/e2e alone is not co-loaded: the pass takes the declaration back and inherits the rest")

	only := testInvocations([]string{core})
	require.Len(t, only, 1, "a tree without test/e2e is one pass")
	require.Equal(t, []string{"test", timeout, core}, only[0].args)
}

// TestTestInvocations_GiveTheIsolatedPassItsOwnHangGuard pins the -timeout each `devtool test`
// pass runs at. test/e2e's binary alone takes 1513.7 s and 1529.5 s on the quiet Windows reference
// host (phase3 c5 and c6 win-e2e-timing) and 19m49s to 21m14s in hosted lint-windows' stubskips
// pass 2 (runs 36905843834, 36955046276, 36981590450), so the whole tree's 30m left it about 15%
// headroom: a slow but healthy run read as a hang (audit 2's #84). The isolated pass gets the 45m
// hang guard ci.yml's test-e2e gives the same binary; the shared pass keeps 30m. Both values are
// written out, so moving either constant is a visible change to this row.
func TestTestInvocations_GiveTheIsolatedPassItsOwnHangGuard(t *testing.T) {
	e2e := modulePath + "/test/e2e"
	core, guards := modulePath+"/internal/core", modulePath+"/test/guards"
	got := testInvocations([]string{core, e2e, guards})
	require.Len(t, got, 2, "one shared pass and test/e2e alone: %+v", got)
	require.Equal(t, []string{"test", "-timeout=30m", core, guards}, got[0].args,
		"the shared whole-tree pass keeps the whole-tree hang guard")
	require.Equal(t, []string{"test", "-timeout=45m", e2e}, got[1].args,
		"test/e2e alone runs at its own 45m hang guard, as ci.yml's test-e2e runs it")
}
