//go:build windows

package hostperm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// fakeShortNames stands in for a volume that records the given 8.3 names (upper-case short name to
// long name) by replacing getLongPathName for the test. Like GetLongPathNameW it expands every
// short segment it knows, and fails when the expanded path does not exist. Every other call this
// package makes is real, so a short spelling that only the fake knows is, to the filesystem, an
// absent path: exactly what an unexpandable 8.3 name is.
//
// Whether a volume records 8.3 names is a system setting that no test may change, and this
// machine's volume records none, so without the fake the 8.3 rows here and in internal/mcp skip
// locally and ran only on the hosted runner (nightly 36820740318, race-windows), where they failed.
func fakeShortNames(t *testing.T, names map[string]string) {
	t.Helper()
	prev := getLongPathName
	t.Cleanup(func() { getLongPathName = prev })
	getLongPathName = func(p string) (string, bool) {
		vol := filepath.VolumeName(p)
		segs := strings.Split(strings.TrimPrefix(p, vol), `\`)
		for i, s := range segs {
			if l, ok := names[strings.ToUpper(s)]; ok {
				segs[i] = l
			}
		}
		long := vol + strings.Join(segs, `\`)
		if _, err := os.Lstat(paths.Long(long)); err != nil {
			return "", false
		}
		return long, true
	}
}

// shortEnv is a project at base\parentdirectory\projectroot whose directories, and the denied file
// in it, carry 8.3 names. The project root is supplied short, as the hosted runner's TEMP
// (C:\Users\RUNNER~1\...) supplies it. Rules come from the managed source, which is read at its real
// path, because a settings file under the short root is not openable without real 8.3 names.
type shortEnv struct {
	*diskEnv
	base, long, shortRoot, file string
}

func newShortEnv(t *testing.T) *shortEnv {
	t.Helper()
	e := newDiskEnv(t)
	base := t.TempDir()
	long := filepath.Join(base, "parentdirectory", "projectroot")
	file := filepath.Join(long, "configuration", "credentials.secret")
	e.write(t, file, "k")
	e.write(t, filepath.Join(long, "public.txt"), "p")
	fakeShortNames(t, map[string]string{
		"PARENT~1": "parentdirectory", "PROJEC~1": "projectroot",
		"CONFIG~1": "configuration", "CREDEN~1.SEC": "credentials.secret",
		"GONE~1.TXT": "gone-but-archived.txt",
	})
	s := &shortEnv{
		diskEnv: e, base: base, long: long, file: file,
		shortRoot: filepath.Join(base, "PARENT~1", "PROJEC~1"),
	}
	s.pol = New(Options{
		ProjectRoot: s.shortRoot, Home: e.home,
		Getenv:  func(string) string { return "" },
		Managed: &ManagedSources{Dirs: []string{e.managed}},
		Clock:   e.clock,
	})
	return s
}

// deny writes the managed deny list.
func (s *shortEnv) deny(t *testing.T, rules ...string) {
	t.Helper()
	q := make([]string, len(rules))
	for i, r := range rules {
		q[i] = `"` + strings.ReplaceAll(r, `\`, `\\`) + `"`
	}
	s.write(t, filepath.Join(s.managed, "managed-settings.json"),
		`{"permissions":{"deny":[`+strings.Join(q, ",")+`]}}`)
}

// fsRule is a `//` rule naming p.
func fsRule(p string) string {
	return "Read(//" + strings.ToLower(p[:1]) + filepath.ToSlash(p[2:]) + ")"
}

// TestShortNames_EverySpellingOfTheServedFileIsJudged reproduces nightly 36820740318's privacy red
// without real 8.3 names. There, TEMP itself was short (RUNNER~1), so the test's "long" absolute
// rule named a path whose parent was short and whose project directory was long, and the project
// root was spelled short: neither the path as served (short root) nor its long name (every segment
// long) matched the rule's mixed spelling, and the archived secret was served. A rule names a
// concrete path the host guards under every spelling, so its own 8.3 segments are expanded too.
func TestShortNames_EverySpellingOfTheServedFileIsJudged(t *testing.T) {
	s := newShortEnv(t)
	mixed := filepath.Join(s.base, "PARENT~1", "projectroot", "configuration", "credentials.secret")
	shortFile := filepath.Join(s.shortRoot, "CONFIG~1", "CREDEN~1.SEC")
	for _, tc := range []struct{ name, rule, path string }{
		{
			"mixed absolute rule, short root, long file", fsRule(mixed),
			filepath.Join(s.shortRoot, "configuration", "credentials.secret"),
		},
		{"mixed absolute rule, long path", fsRule(mixed), s.file},
		{"mixed absolute rule, short path", fsRule(mixed), shortFile},
		{"long absolute rule, short path", fsRule(s.file), shortFile},
		{"relative rule, long path", "Read(./configuration/credentials.secret)", s.file},
		{"short relative rule, long path", "Read(./CONFIG~1/CREDEN~1.SEC)", s.file},
		{"short directory glob, long path", "Read(//" + strings.ToLower(s.base[:1]) +
			filepath.ToSlash(filepath.Join(s.base, "PARENT~1", "PROJEC~1")[2:]) + "/CONFIG~1/**)", s.file},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.deny(t, tc.rule)
			d, err := s.pol.Check(tc.path)
			require.NoError(t, err)
			require.Equal(t, Deny, d.Effect, "%q under %q opens the denied file", tc.path, tc.rule)

			other := filepath.Join(s.long, "public.txt")
			d, err = s.pol.Check(other)
			require.NoError(t, err)
			require.Equal(t, Allow, d.Effect, "control: %q is not the denied file", other)
		})
	}
}

// TestShortNames_AnUnresolvableShortNameIsRefused: an 8.3-shaped segment whose entry no longer
// exists cannot be expanded, so the long name a rule may name cannot be established, and the read
// is refused rather than judged on the short spelling alone (fail closed, as an unreadable settings
// file is). With no rule in force there is nothing to judge and nothing is refused, and a long name
// that merely contains `~1` and exists is judged as itself.
func TestShortNames_AnUnresolvableShortNameIsRefused(t *testing.T) {
	s := newShortEnv(t)
	gone := filepath.Join(s.shortRoot, "CONFIG~1", "GONE~1.TXT")

	d, err := s.pol.Check(gone)
	require.NoError(t, err)
	require.Equal(t, Allow, d.Effect, "no rule is in force")

	s.deny(t, "Read(./configuration/gone-but-archived.txt)")
	d, err = s.pol.Check(gone)
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect, "the short name of a deleted file cannot be expanded")

	s.deny(t, "Read(./elsewhere/**)")
	d, err = s.pol.Check(gone)
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect, "an unexpandable 8.3 name is refused under any rule")

	tilde := filepath.Join(s.long, "configuration", "notes~1.txt")
	s.write(t, tilde, "n")
	d, err = s.pol.Check(tilde)
	require.NoError(t, err)
	require.Equal(t, Allow, d.Effect, "an existing long name with ~1 in it is its own name")
}
