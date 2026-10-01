package daemon

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/obs"
)

// TestSpoolSubmodeWording_NamesRealConfigKeys: the spool-submode wording every surface shows (the
// transition's log lines, status, doctor) tells the user which settings to tune. Each one it names
// must be a configuration key this build has, spelled as the configuration spells it, and the
// wording must name every one of them.
func TestSpoolSubmodeWording_NamesRealConfigKeys(t *testing.T) {
	keys := flattenConfig(config.Defaults())
	require.Len(t, obs.SpoolSubmodeKeys, 3)
	for _, key := range obs.SpoolSubmodeKeys {
		_, ok := keys[key]
		require.True(t, ok, "%s is not a configuration key", key)
		require.Contains(t, obs.SpoolSubmodeWhat+" "+obs.SpoolSubmodeTune, key)
	}
	require.Contains(t, obs.SpoolSubmodeWhat, "nothing is lost")
	require.Contains(t, obs.SpoolSubmodeUntil, "new session")
	require.Contains(t, obs.SpoolSubmodeUntil, "idle exit")
	for _, text := range []string{obs.SpoolSubmodeWhat, obs.SpoolSubmodeUntil, obs.SpoolSubmodeTune} {
		require.NotContains(t, strings.ToLower(text), "degraded")
	}
}
