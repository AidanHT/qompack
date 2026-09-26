package guards

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/paths"
)

// TestGuard_ConfigFilesAreWhereThePathsLayoutPutsThem holds internal/config's spelling of the two
// config-file locations to the one internal/paths owns. §3.2 gives config the allow-set {core}, so
// config.UserConfigPath and config.ProjectConfigPath cannot call paths.Global and paths.Of; this
// package imports both and is the one place the two spellings can be compared. If the store
// directory's name or the user-global layer's root ever moves in paths, the config loaders would
// otherwise keep reading the old place without a word.
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
