package main

import (
	"testing"

	"github.com/stretchr/testify/require"
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
