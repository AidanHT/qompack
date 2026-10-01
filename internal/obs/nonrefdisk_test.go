package obs_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/obs"
)

// TestNonReferenceDisk_HonouredOnlyUnderGitHubActions pins the declaration's two conditions: it
// is the variable's presence, and it counts only on a runner GitHub Actions started
// (GITHUB_ACTIONS=true). Anywhere else a run that sets it still judges every row, so a local run
// can never waive an fsync-bound gate with it.
func TestNonReferenceDisk_HonouredOnlyUnderGitHubActions(t *testing.T) {
	cases := []struct {
		declared, actions string
		honoured          bool
	}{
		{declared: "", actions: "", honoured: false},
		{declared: "", actions: "true", honoured: false},
		{declared: "1", actions: "", honoured: false},
		{declared: "1", actions: "false", honoured: false},
		{declared: "1", actions: "1", honoured: false},
		{declared: "1", actions: "TRUE", honoured: false},
		{declared: "1", actions: "true", honoured: true},
		{declared: "false", actions: "true", honoured: true}, // presence declares, as UnderColoadEnv
	}
	for _, c := range cases {
		t.Setenv(obs.NonReferenceDiskEnv, c.declared)
		t.Setenv(obs.GitHubActionsEnv, c.actions)
		require.Equal(t, c.declared != "", obs.NonReferenceDiskDeclared(), "%s=%q", obs.NonReferenceDiskEnv, c.declared)
		require.Equal(t, c.honoured, obs.NonReferenceDisk(), "%s=%q %s=%q",
			obs.NonReferenceDiskEnv, c.declared, obs.GitHubActionsEnv, c.actions)
	}
}
