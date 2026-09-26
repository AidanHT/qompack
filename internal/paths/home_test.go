package paths_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// Owner decision D18 (2026-09-26): a session whose project root resolves to the home directory is
// refused, because the project store would then be <home>/.qompack — the user-global layer's own
// directory. paths.IsHome is the one question every entry point asks, so these rows pin what it
// counts as "the home directory". Every home here is a fake one under t.TempDir(); the real home is
// never named.

// fakeHome returns a fresh directory standing in for the user's home, with the user-global layer a
// real installation has (<home>/.qompack/config.json), so a row cannot pass merely because the
// directory is empty.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(paths.Global(home), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Global(home), "config.json"), []byte("{}\n"), 0o600))
	return home
}

func TestIsHome_TheHomeDirectoryItselfIsHome(t *testing.T) {
	home := fakeHome(t)

	require.True(t, paths.IsHome(home, home), "the home directory, spelled as given")
	require.True(t, paths.IsHome(home+string(filepath.Separator), home), "a trailing separator is still home")
	require.True(t, paths.IsHome(filepath.Join(home, "sub", ".."), home), "an uncleaned spelling is still home")
	require.True(t, paths.IsHome(home, filepath.Join(home, "."), ""), "an uncleaned home and an empty candidate")
	require.True(t, paths.IsHome(home, filepath.Join(t.TempDir(), "elsewhere"), home),
		"any one of several home candidates matching is enough")
}

func TestIsHome_ARelativeSpellingOfHomeIsHome(t *testing.T) {
	home := fakeHome(t)
	t.Chdir(home)

	require.True(t, paths.IsHome(".", home), "`--project .` typed in the home directory names the home directory")
}

func TestIsHome_EverythingElseIsNotHome(t *testing.T) {
	home := fakeHome(t)
	below := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(below, 0o755))
	sibling := filepath.Join(filepath.Dir(home), "other")
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	for _, tc := range []struct {
		name  string
		root  string
		homes []string
	}{
		{"a project below home", below, []string{home}},
		{"the directory above home", filepath.Dir(home), []string{home}},
		{"a sibling of home", sibling, []string{home}},
		{"the user-global layer itself", paths.Global(home), []string{home}},
		{"no root resolved", "", []string{home}},
		{"no home known", home, nil},
		{"only empty home candidates", home, []string{"", ""}},
		{"a root that does not exist", filepath.Join(home, "missing"), []string{home}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.False(t, paths.IsHome(tc.root, tc.homes...), "root %q, homes %q", tc.root, tc.homes)
		})
	}
}

// TestIsHome_ARelativeHomeCandidateIsIgnored pins that a HOME which is not an absolute path names
// no directory at all. Resolving it against the working directory would refuse whatever project a
// session happened to start in.
func TestIsHome_ARelativeHomeCandidateIsIgnored(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	require.False(t, paths.IsHome(root, "."), "HOME=. is not a home directory")
	require.False(t, paths.IsHome(root, filepath.Base(root)), "a bare name is not a home directory")
}

// TestIsHome_ALinkToHomeIsHome is the junction/symlink half of D18: a project root that reaches the
// home directory through a link is the home directory, however it is spelled. makeDirLink uses a
// real symlink where the host allows one and an NTFS junction otherwise.
func TestIsHome_ALinkToHomeIsHome(t *testing.T) {
	home := fakeHome(t)
	link := filepath.Join(t.TempDir(), "via-link")
	require.NoError(t, makeDirLink(link, home))

	require.True(t, paths.IsHome(link, home), "a link to home, asked about with the real home")
	require.True(t, paths.IsHome(home, link), "the real home, asked about with a home that is itself a link")

	below := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(below, 0o755))
	require.False(t, paths.IsHome(filepath.Join(link, "proj"), home),
		"a project below home, reached through the link, is still a project")
}

func TestRefuseHome_NamesTheRootAndWrapsTheSentinel(t *testing.T) {
	home := fakeHome(t)

	err := paths.RefuseHome(home, home)
	require.Error(t, err)
	require.True(t, errors.Is(err, paths.ErrHomeRoot), "callers branch on paths.ErrHomeRoot: %v", err)
	require.Contains(t, err.Error(), home, "the refusal names the root it refused")
	require.Contains(t, err.Error(), "project directory", "the refusal says how to fix it")

	below := filepath.Join(home, "proj")
	require.NoError(t, os.MkdirAll(below, 0o755))
	require.NoError(t, paths.RefuseHome(below, home), "a project below home is not refused")
}

func TestHomeDirs_ReadsHOMEAndUSERPROFILEOnceEach(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")

	require.Equal(t, []string{a, b},
		paths.HomeDirs(getenvFunc(map[string]string{"HOME": a, "USERPROFILE": b})))
	require.Equal(t, []string{a},
		paths.HomeDirs(getenvFunc(map[string]string{"HOME": a, "USERPROFILE": a})), "the same home once")
	require.Equal(t, []string{b},
		paths.HomeDirs(getenvFunc(map[string]string{"USERPROFILE": b})), "USERPROFILE alone")
	require.Empty(t, paths.HomeDirs(getenvFunc(nil)), "no home in the environment")
}

// BenchmarkIsHome_AProjectBelowHome prices the check every hook now pays once per resolved root:
// the textual comparison fails, so it costs two Stats and, on Windows, the file-identity load
// os.SameFile performs for each.
func BenchmarkIsHome_AProjectBelowHome(b *testing.B) {
	home := filepath.Join(b.TempDir(), "home")
	root := filepath.Join(home, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if paths.IsHome(root, home) {
			b.Fatal("a project below home must not be home")
		}
	}
}
