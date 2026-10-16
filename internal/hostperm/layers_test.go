package hostperm

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// layerEnv builds a diskEnv and the Options ReadLayers takes for it.
func layerEnv(t *testing.T) (*diskEnv, Options) {
	t.Helper()
	e := newDiskEnv(t)
	return e, Options{
		ProjectRoot: e.root, Home: e.home,
		Getenv:  func(k string) string { return e.vars[k] },
		Managed: &ManagedSources{Dirs: []string{e.managed}},
	}
}

// layerBySource indexes ReadLayers' answer by Source, failing on a duplicate.
func layerBySource(t *testing.T, layers []Layer) map[string]Layer {
	t.Helper()
	out := map[string]Layer{}
	for _, l := range layers {
		_, dup := out[l.Source]
		require.False(t, dup, "ReadLayers named %s twice", l.Source)
		out[l.Source] = l
	}
	return out
}

func TestReadLayersReadsEveryLayerRaw(t *testing.T) {
	e, o := layerEnv(t)
	files := map[string]struct {
		kind LayerKind
		body string
	}{
		filepath.Join(e.managed, "managed-settings.json"):           {LayerManaged, `{"autoCompactWindow":300000}`},
		filepath.Join(e.managed, "managed-settings.d", "10-a.json"): {LayerManagedDropIn, `{"idleCompaction":false}`},
		e.remote():  {LayerRemote, `{"envelope":{"autoCompactWindow":1}}`},
		e.user():    {LayerUser, `{"autoCompactEnabled":true}`},
		e.project(): {LayerProject, `{"autoCompactWindow":500000,"modelSettings":{"m":{"autoCompactWindow":200000}}}`},
		e.local():   {LayerLocal, "\xEF\xBB\xBF{\"idleCompaction\":true}"},
	}
	for f, w := range files {
		e.write(t, f, w.body)
	}

	got := layerBySource(t, ReadLayers(o))
	for f, w := range files {
		l, ok := got[f]
		require.True(t, ok, "no layer for %s in %v", f, got)
		require.Equal(t, w.kind, l.Kind, "%s", f)
		require.True(t, l.Present, "%s", f)
		require.NoError(t, l.Err, "%s", f)
		require.Equal(t, w.body, string(l.Data), "%s: the reader returns the bytes as written", f)
	}
}

func TestReadLayersNeverDecodes(t *testing.T) {
	e, o := layerEnv(t)
	e.write(t, e.user(), `{not json`)

	l := layerBySource(t, ReadLayers(o))[e.user()]
	require.True(t, l.Present)
	require.NoError(t, l.Err, "a document that does not parse is still read; judging it is the caller's job")
	require.Equal(t, `{not json`, string(l.Data))
}

func TestReadLayersReportsAbsentFilesAsAbsent(t *testing.T) {
	_, o := layerEnv(t)

	layers := ReadLayers(o)
	require.NotEmpty(t, layers)
	for _, l := range layers {
		require.False(t, l.Present, "%s", l.Source)
		require.NoError(t, l.Err, "%s: an absent file is not an error", l.Source)
		require.Nil(t, l.Data, "%s", l.Source)
	}
}

func TestReadLayersWorksWithAnEmptyProjectRoot(t *testing.T) {
	e, o := layerEnv(t)
	o.ProjectRoot = ""
	e.write(t, e.user(), `{"autoCompactWindow":400000}`)

	kinds := map[LayerKind]int{}
	for _, l := range ReadLayers(o) {
		kinds[l.Kind]++
		require.NoError(t, l.Err, "%s", l.Source)
	}
	require.Equal(t, map[LayerKind]int{LayerManaged: 1, LayerRemote: 1, LayerUser: 1}, kinds,
		"with no project there is no project or local layer, and the others are still read")
	require.Equal(t, `{"autoCompactWindow":400000}`, string(layerBySource(t, ReadLayers(o))[e.user()].Data))
}

func TestReadLayersFollowsClaudeConfigDir(t *testing.T) {
	e, o := layerEnv(t)
	cfg := t.TempDir()
	e.vars["CLAUDE_CONFIG_DIR"] = cfg
	f := filepath.Join(cfg, "settings.json")
	e.write(t, f, `{"autoCompactWindow":600000}`)

	got := layerBySource(t, ReadLayers(o))
	require.Equal(t, LayerUser, got[f].Kind)
	require.True(t, got[f].Present)
	require.Contains(t, got, filepath.Join(cfg, "remote-settings.json"))
	require.NotContains(t, got, e.user(), "the home's .claude is not read when CLAUDE_CONFIG_DIR is set")
}

func TestReadLayersMarksAnOpaqueProfileWithoutReadingIt(t *testing.T) {
	e, o := layerEnv(t)
	plist := filepath.Join(e.managed, "profile.plist")
	o.Managed.Opaque = []string{plist}
	e.write(t, plist, "binary plist")

	l := layerBySource(t, ReadLayers(o))[plist]
	require.Equal(t, LayerManagedProfile, l.Kind)
	require.True(t, l.Present)
	require.True(t, l.Opaque)
	require.Nil(t, l.Data, "an opaque profile is reported, never read")
}

func TestReadLayersRegistryValue(t *testing.T) {
	_, o := layerEnv(t)
	r := RegistryValue{Hive: "HKCU", Key: `SOFTWARE\QompackTests\NoSuchKey`, Name: "Settings"}
	o.Managed.Registry = []RegistryValue{r}

	l := layerBySource(t, ReadLayers(o))[r.id()]
	require.Equal(t, LayerRegistry, l.Kind)
	require.False(t, l.Present)
	if runtime.GOOS == "windows" {
		require.NoError(t, l.Err, "a registry key that does not exist is absent")
	} else {
		require.Error(t, l.Err, "a named registry value on a platform without a registry is unreadable")
	}
}

// TestReadLayersNamesThePolicysSources keeps the two enumerations in step: with a project root,
// ReadLayers lists exactly the sources the permission policy reads, in the same order.
func TestReadLayersNamesThePolicysSources(t *testing.T) {
	e, o := layerEnv(t)
	e.write(t, filepath.Join(e.managed, "managed-settings.d", "a.json"), `{}`)
	e.write(t, filepath.Join(e.managed, "managed-settings.d", "b.json"), `{}`)
	o.Managed.Opaque = []string{filepath.Join(e.managed, "profile.plist")}

	srcs, err := New(o).sources()
	require.NoError(t, err)
	var want, got []string
	for _, s := range srcs {
		want = append(want, s.id)
	}
	for _, l := range ReadLayers(o) {
		got = append(got, l.Source)
	}
	require.Equal(t, want, got)
}
