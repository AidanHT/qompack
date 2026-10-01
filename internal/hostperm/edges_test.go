package hostperm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// These rows pin behaviour the platform-neutral rule and source code has on every host: the
// carve-out forms a working-directory rule meets, a rule naming the working directory itself, a
// home that cannot be found, a main checkout's own .git directory, and server-managed rules that sit
// inside an array.

func TestNegation_ALeadingSlashCarveOutIsAnchoredAtTheWorkingDirectory(t *testing.T) {
	rs := posixEnv.build(t, deny("Read(*.env)", "Read(!/sample.env)"))
	requireEffect(t, rs, Allow, "/proj/sample.env")
	requireEffect(t, rs, Deny, "/proj/sub/sample.env", "/proj/prod.env")
}

func TestNegation_AnAbsoluteOrHomeCarveOutCarvesNothing(t *testing.T) {
	// The project sits inside the home, so the carve-outs below spell the very path the rule
	// refuses. The host carves only with working-directory-relative patterns, so they still refuse.
	e := pureEnv{goos: "linux", root: "/home/u/proj", home: "/home/u", cfg: "/home/u/.claude"}
	for _, carve := range []string{"Read(!//home/u/proj/sample.env)", "Read(!~/proj/sample.env)", "Read(!~)"} {
		t.Run(carve, func(t *testing.T) {
			rs := e.build(t, deny("Read(*.env)", carve))
			requireEffect(t, rs, Deny, "/home/u/proj/sample.env", "/home/u/proj/sub/sample.env")
			requireEffect(t, rs, Allow, "/home/u/proj/readme.md")
		})
	}
}

func TestARuleNamingTheWorkingDirectoryRefusesEverythingInIt(t *testing.T) {
	for _, rule := range []string{"Read(./)", "Read(.)"} {
		t.Run(rule, func(t *testing.T) {
			rs := posixEnv.build(t, deny(rule))
			requireEffect(t, rs, Deny, "/proj/a.txt", "/proj/sub/b.txt")
			requireEffect(t, rs, Allow, "/other/a.txt", "/home/u/a.txt")
		})
	}
	t.Run("a carve-out reopens a top-level entry but nothing inside a refused directory", func(t *testing.T) {
		rs := posixEnv.build(t, deny("Read(.)", "Read(!keep.txt)"))
		requireEffect(t, rs, Allow, "/proj/keep.txt")
		requireEffect(t, rs, Deny, "/proj/sub/keep.txt", "/proj/other.txt")
	})
}

func TestAnUnknownHomeDirectoryFailsClosed(t *testing.T) {
	root := t.TempDir()
	// Options.Home empty means os.UserHomeDir, which reads HOME (USERPROFILE on Windows). With both
	// empty there is no home, so ~/ rules and the user's settings cannot be located.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	_, herr := os.UserHomeDir()
	require.Error(t, herr, "fixture: the home must be undiscoverable")

	pol := New(Options{ProjectRoot: root, Getenv: func(string) string { return "" }, Managed: &ManagedSources{}})
	require.Empty(t, pol.home())
	_, err := pol.Check(filepath.Join(root, "a.txt"))
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorContains(t, err, "home directory is unknown")
}

func TestAMainCheckoutsGitDirectoryReadsOnlyItsOwnLocalSettings(t *testing.T) {
	e := newDiskEnv(t)
	// In a main checkout .git is a directory, not a worktree's pointer file: there is no other
	// checkout to read, and the directory must not break the policy.
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(e.root, ".git")), 0o700))
	require.Empty(t, mainCheckout(e.root))
	require.Equal(t, []string{e.root}, localSettingsDirs(e.root))
	e.write(t, e.local(), `{"permissions":{"deny":["Read(./x.key)"]}}`)
	require.Equal(t, Deny, e.check(t, "x.key"))
	require.Equal(t, Allow, e.check(t, "y.key"))
}

func TestServerManagedRulesInsideAnArrayApply(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, filepath.Join(e.home, ".claude", "remote-settings.json"),
		`{"layers":[{"name":"org"},{"permissions":{"deny":["Read(./a.txt)"]}},[{"permissions":{"ask":["Read(./b.txt)"]}}]]}`)
	require.Equal(t, Deny, e.check(t, "a.txt"))
	require.Equal(t, Ask, e.check(t, "b.txt"), "a nested array is walked too")
	require.Equal(t, Allow, e.check(t, "c.txt"))
}
