//go:build windows

package hostperm

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// shortPath returns p's 8.3 spelling as the volume records it.
func shortPath(t *testing.T, p string) string {
	t.Helper()
	u, err := syscall.UTF16PtrFromString(p)
	require.NoError(t, err)
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n, err := syscall.GetShortPathName(u, &buf[0], uint32(len(buf)))
	require.NoError(t, err, "GetShortPathName(%s)", p)
	return syscall.UTF16ToString(buf[:n])
}

// requireShortNames skips when the volume holding dir records no 8.3 names: the spelling under
// test does not exist there, so there is nothing to judge.
func requireShortNames(t *testing.T, long string) string {
	t.Helper()
	s := shortPath(t, long)
	if strings.EqualFold(s, long) {
		t.Skip("platform: this volume records no 8.3 names, so there is no short spelling to test")
	}
	return s
}

// TestWindowsAliasesOfADeniedFileAreJudged is C1.9 review finding 1 at the rule layer: the Win32
// layer opens the same file under several spellings, and a rule on the file's real name must hold
// for every one of them. A spelling Windows does not open as that file stays its own path.
func TestWindowsAliasesOfADeniedFileAreJudged(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, filepath.Join(e.root, "configuration", "credentials.secret"), "k")
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./configuration/credentials.secret)"]}}`)
	short := requireShortNames(t, filepath.Join(e.root, "configuration", "credentials.secret"))
	shortRel := strings.TrimPrefix(short, shortPath(t, e.root)+`\`)

	for _, rel := range []string{
		`configuration\credentials.secret.`,
		`configuration\credentials.secret `,
		`configuration\credentials.secret...`,
		`configuration\credentials.secret. . `,
		`configuration.\credentials.secret`,
		`configuration\credentials.secret::$DATA`,
		`configuration\credentials.secret:hidden`,
		`configuration\credentials.secret:hidden:$DATA`,
		shortRel,
	} {
		d, err := e.pol.Check(e.root + `\` + rel)
		require.NoError(t, err)
		require.Equal(t, Deny, d.Effect, "%q opens the denied file", rel)
	}
	d, err := e.pol.Check(short)
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect, "the fully short spelling %q opens the denied file", short)

	for _, rel := range []string{`configuration..\credentials.secret`, `configuration \credentials.secret`} {
		d, err := e.pol.Check(e.root + `\` + rel)
		require.NoError(t, err)
		require.Equal(t, Allow, d.Effect, "%q is a different, absent path and stays its own", rel)
	}
}

// TestAShortDirectoryOfADeletedFileIsExpanded: a historical read may name a file that is gone. The
// directories that still exist are expanded, so a rule on the real directory name still holds.
func TestAShortDirectoryOfADeletedFileIsExpanded(t *testing.T) {
	e := newDiskEnv(t)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(e.root, "configuration")), 0o700))
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./configuration/**)"]}}`)
	short := requireShortNames(t, filepath.Join(e.root, "configuration"))
	d, err := e.pol.Check(short + `\gone.txt`)
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect)
}

// TestAnAliasOfALinkIsJudgedAsTheLink: a rule written on a link's own spelling holds when the link
// is named through a trailing dot or its 8.3 name, which a Readlink of the literal spelling would
// not see.
func TestAnAliasOfALinkIsJudgedAsTheLink(t *testing.T) {
	e := newDiskEnv(t)
	real := filepath.Join(e.root, "real")
	e.write(t, filepath.Join(real, "key.pem"), "k")
	link := filepath.Join(e.root, "linkeddirectory")
	if err := makeDirLink(link, real); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(paths.Long(link)) })
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./linkeddirectory/**)"]}}`)
	short := requireShortNames(t, link)

	for _, p := range []string{link + `.\key.pem`, short + `\key.pem`, link + `\key.pem`} {
		d, err := e.pol.Check(p)
		require.NoError(t, err)
		require.Equal(t, Deny, d.Effect, "%q names the file through the denied link", p)
	}
}

// TestAShortSpelledProjectRootAnchorsRulesAtItsRealName: the project root a caller supplies may be
// spelled with 8.3 names. `./` rules are then measured from the root's real name as well, and a
// path named by its real name is judged against them.
func TestAShortSpelledProjectRootAnchorsRulesAtItsRealName(t *testing.T) {
	e := newDiskEnv(t)
	file := filepath.Join(e.root, "configuration", "credentials.secret")
	e.write(t, file, "k")
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./configuration/credentials.secret)"]}}`)
	shortRoot := requireShortNames(t, e.root)
	pol := New(Options{
		ProjectRoot: shortRoot, Home: e.home,
		Getenv:  func(string) string { return "" },
		Managed: &ManagedSources{Dirs: []string{e.managed}},
		Clock:   e.clock,
	})
	for _, p := range []string{file, shortRoot + `\configuration\credentials.secret`, shortPath(t, file)} {
		d, err := pol.Check(p)
		require.NoError(t, err)
		require.Equal(t, Deny, d.Effect, "%q is the denied file", p)
	}
}
