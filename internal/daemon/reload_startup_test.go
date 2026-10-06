package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// reloadWarningLines counts the reload's own Loud line among l's lines.
func reloadWarningLines(l *reloadLoudLog) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if line == "daemon: config reload warning" {
			n++
		}
	}
	return n
}

// writeProjectConfig writes body as root's .qompack/config.json.
func writeProjectConfig(t *testing.T, root, body string) {
	t.Helper()
	p := configJSONPath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(body), 0o600))
}

// TestConfigReload_StartupStampSkipsTheUnchangedFile is wave 20's re-Loud finding. The daemon's
// reload bookkeeping (lastCfgMTime, lastCfgSize) started at zero, so its first configuration check
// after a start — the idle tick or the first session.start — always reloaded a config.json the
// composition root had loaded moments before, and Louded every warning in it again. A §11.3
// violation therefore reached LOUD.log twice per daemon start (once from the startup
// LoadConfigAndReport, once as "daemon: config reload warning"), and an unchanged unknown key or
// newer-settingsVersion reset was promoted from the startup load's warn to loud on every start.
// D59's rule is one loud report per start or change. The composition root now stamps the file before
// its load and the daemon starts from that stamp, so the first check reloads only a file that
// changed since; a change is still reloaded, and Louded, at once.
func TestConfigReload_StartupStampSkipsTheUnchangedFile(t *testing.T) {
	body := `{"runtime":{"notAKey":1,"migration":{"settingsVersion":99}}}`

	t.Run("stamped at start", func(t *testing.T) {
		h := newReloadHarness(t, testConfig(), func(o *Options) {
			writeProjectConfig(t, o.ProjectRoot, body)
			o.CfgStamp = StampConfigFile(o.ProjectRoot)
		})
		loud := &reloadLoudLog{}
		h.d.log = loud

		h.d.maybeReloadConfig(context.Background(), h.d.cfgEnv)
		require.Zero(t, reloadWarningLines(loud), "the unchanged file the daemon started on is not reloaded")

		// A different size, so the change is seen whatever the file system's mtime resolution.
		writeProjectConfig(t, h.root, `{"runtime":{"notAKey":1,"alsoNotAKey":2,"migration":{"settingsVersion":99}}}`)
		h.d.maybeReloadConfig(context.Background(), h.d.cfgEnv)
		require.Equal(t, 3, reloadWarningLines(loud), "a changed file is reloaded and each warning Louded once")

		h.d.maybeReloadConfig(context.Background(), h.d.cfgEnv)
		require.Equal(t, 3, reloadWarningLines(loud), "and not again while it stays unchanged")
	})

	t.Run("no stamp", func(t *testing.T) {
		// An embedder that never stamped (Options{} or NewOptions alone) keeps the old first check:
		// it cannot know which file its Cfg came from, so it loads the file once.
		h := newReloadHarness(t, testConfig(), func(o *Options) {
			writeProjectConfig(t, o.ProjectRoot, body)
		})
		loud := &reloadLoudLog{}
		h.d.log = loud

		h.d.maybeReloadConfig(context.Background(), h.d.cfgEnv)
		require.Equal(t, 2, reloadWarningLines(loud))
		h.d.maybeReloadConfig(context.Background(), h.d.cfgEnv)
		require.Equal(t, 2, reloadWarningLines(loud))
	})

	t.Run("no file at start", func(t *testing.T) {
		h := newReloadHarness(t, testConfig(), func(o *Options) {
			o.CfgStamp = StampConfigFile(o.ProjectRoot)
		})
		require.Equal(t, ConfigFileStamp{}, h.o.CfgStamp, "an absent file stamps as zero")
		loud := &reloadLoudLog{}
		h.d.log = loud

		writeProjectConfig(t, h.root, body)
		h.d.maybeReloadConfig(context.Background(), h.d.cfgEnv)
		require.Equal(t, 2, reloadWarningLines(loud), "a file created after the start is reloaded")
	})
}
