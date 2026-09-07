package obs_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/obs"
)

// TestUnderCoload_ReadsTheDeclaration pins the one behaviour every co-load-aware test leans on:
// the declaration is the variable's PRESENCE, not a particular value, and its absence is the
// stricter default.
func TestUnderCoload_ReadsTheDeclaration(t *testing.T) {
	t.Setenv(obs.UnderColoadEnv, "")
	require.False(t, obs.UnderCoload(), "an empty declaration is no declaration: the run judges everything")

	t.Setenv(obs.UnderColoadEnv, "1")
	require.True(t, obs.UnderCoload())

	// Any non-empty value declares it. The variable is a fact, not a switch with settings, so
	// "0" and "false" are not spellings of "not co-loaded" — a job that wants the strict run
	// leaves it unset.
	t.Setenv(obs.UnderColoadEnv, "false")
	require.True(t, obs.UnderCoload(),
		"only an UNSET variable withdraws the declaration; a value of %q still makes it", "false")
}
