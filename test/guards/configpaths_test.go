package guards

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/paths"
)

// TestGuard_ConfigFilesAreWhereThePathsLayoutPutsThem holds internal/config's two config-file
// locations to the directories internal/paths owns. It was written while §3.2 gave config the
// allow-set {core}, when config spelled both locations by hand and this package, which imports
// both, was the one place the spellings could be compared. Owner decision D22 lets config import
// paths, and config.UserConfigPath and config.ProjectConfigPath now name paths.Global and
// paths.Of(root).Dot themselves; the guard stays so that a loader that goes back to spelling a
// location by hand, and then drifts, still fails here.
func TestGuard_ConfigFilesAreWhereThePathsLayoutPutsThem(t *testing.T) {
	const configFile = "config.json"
	base := t.TempDir()
	for _, dir := range []string{
		base,
		filepath.Join(base, "a home", "with spaces"),
		filepath.Join(base, "nested", "project") + string(filepath.Separator),
	} {
		require.Equal(t, filepath.Join(paths.Global(dir), configFile), config.UserConfigPath(dir),
			"the user-global config file must be config.json in paths.Global(%q)", dir)
		require.Equal(t, filepath.Join(paths.Of(dir).Dot, configFile), config.ProjectConfigPath(dir),
			"the project config file must be config.json in paths.Of(%q).Dot", dir)
	}
}
