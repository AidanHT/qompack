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
// co-load declaration taken back, as ADR 0010 decision 4 and ci.yml's `test-e2e` job run it. Both
// keep wholeTreeTestTimeout. Before this, taskTest ran `go test ./...` with test/e2e inside the
// co-loaded pass, the condition under which its live-path rows' hooks take the designed degrade to
// the client spool (the wave 16 ci seat's review).
func TestTestInvocations_RunsE2EAloneWithoutTheColoadDeclaration(t *testing.T) {
	e2e := modulePath + "/test/e2e"
	core, guards := modulePath+"/internal/core", modulePath+"/test/guards"
	got := testInvocations([]string{core, e2e, guards})
	require.Len(t, got, 2, "one shared pass and test/e2e alone: %+v", got)

	timeout := "-timeout=" + wholeTreeTestTimeout
	require.Equal(t, []string{"test", timeout, core, guards}, got[0].args)
	require.Equal(t, map[string]string{obs.UnderColoadEnv: "1", obs.NonReferenceDiskEnv: ""}, got[0].env,
		"the shared pass is co-loaded and says so, and names no second cause")

	require.Equal(t, []string{"test", timeout, e2e}, got[1].args)
	require.Equal(t, map[string]string{obs.UnderColoadEnv: ""}, got[1].env,
		"test/e2e alone is not co-loaded: the pass takes the declaration back and inherits the rest")

	only := testInvocations([]string{core})
	require.Len(t, only, 1, "a tree without test/e2e is one pass")
	require.Equal(t, []string{"test", timeout, core}, only[0].args)
}
