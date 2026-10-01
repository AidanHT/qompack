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
// long name) by replacing getLongPathName and getShortPathName for the test. Like GetLongPathNameW
// and GetShortPathNameW it rewrites every segment it knows, and fails when the path does not
// exist. Every other call this package makes is real, so a short spelling that only the fake knows
// is, to the filesystem, an absent path: exactly what an unexpandable 8.3 name is.
//
// Whether a volume records 8.3 names is a system setting that no test may change. This machine's
// volume does record them, and the internal/mcp row runs here, but its TEMP has no short segment,
// so the hosted runner's shape (a short TEMP parent, C:\Users\RUNNER~1, under a rule mixing short
// and long names: nightly 36820740318, race-windows) never arose locally. The fake makes that
// shape, an unexpandable short name and a rule naming a short name after a glob deterministic on
// any volume.
func fakeShortNames(t *testing.T, names map[string]string) {
	t.Helper()
	shortOf := map[string]string{}
	for s, l := range names {
		shortOf[strings.ToLower(l)] = s
	}
	rewrite := func(p string, to func(seg string) string) (string, bool) {
		vol := filepath.VolumeName(p)
		segs := strings.Split(strings.TrimPrefix(p, vol), `\`)
		long := make([]string, len(segs))
		for i, s := range segs {
			long[i] = s
			if l, ok := names[strings.ToUpper(s)]; ok {
				long[i] = l
			}
			segs[i] = to(s)
		}
		if _, err := os.Lstat(paths.Long(vol + strings.Join(long, `\`))); err != nil {
			return "", false
		}
		return vol + strings.Join(segs, `\`), true
	}
	prevLong, prevShort := getLongPathName, getShortPathName
	t.Cleanup(func() { getLongPathName, getShortPathName = prevLong, prevShort })
	getLongPathName = func(p string) (string, bool) {
		return rewrite(p, func(s string) string {
			if l, ok := names[strings.ToUpper(s)]; ok {
				return l
			}
			return s
		})
	}
	getShortPathName = func(p string) (string, bool) {
		return rewrite(p, func(s string) string {
			if sh, ok := shortOf[strings.ToLower(s)]; ok {
				return sh
			}
			return s
		})
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
		// A short name after a glob, or in a literal rule, names no concrete directory the rule could
		// be expanded at, so each segment of the path is also judged by its own 8.3 name.
		{"short bare name, long path", "Read(CREDEN~1.SEC)", s.file},
		{"short directory after a glob, long path", "Read(**/CONFIG~1/**)", s.file},
		{"short directory after a glob, long file", "Read(**/CONFIG~1/credentials.secret)", s.file},
		{
			"short bare name, short root, long file", "Read(CREDEN~1.SEC)",
			filepath.Join(s.shortRoot, "configuration", "credentials.secret"),
		},
		{"literal short rule, long path", "Read(./x/../CONFIG~1/CREDEN~1.SEC)", s.file},
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

// TestShortNames_AShortRuleAfterAGlobRefusesWhatItCannotJudge: a rule naming an 8.3 name after a
// glob is matched against each path segment's 8.3 name, which only a file that exists has. A file
// that no longer exists (the archive serves deleted files' history) has no 8.3 name to compare, so
// while such a rule is in force its read is refused rather than judged on its long name alone. A
// carve-out spelled short reopens nothing, even while a rule naming a short name after a glob
// turns the 8.3 comparison on: that comparison only ever adds refusals.
func TestShortNames_AShortRuleAfterAGlobRefusesWhatItCannotJudge(t *testing.T) {
	s := newShortEnv(t)
	deleted := filepath.Join(s.long, "configuration", "gone-but-archived.txt")

	s.deny(t, "Read(**/GONE~1.TXT)")
	d, err := s.pol.Check(deleted)
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect, "a deleted file's own 8.3 name cannot be established")

	d, err = s.pol.Check(filepath.Join(s.long, "public.txt"))
	require.NoError(t, err)
	require.Equal(t, Allow, d.Effect, "an existing file's 8.3 names are all known, and none matches")

	s.deny(t, "Read(./elsewhere/**)")
	d, err = s.pol.Check(deleted)
	require.NoError(t, err)
	require.Equal(t, Allow, d.Effect, "with no 8.3 name in the rules, a deleted file is judged as before")

	s.deny(t, "Read(*.secret)", "Read(**/OTHER~1/**)", "Read(!CONFIG~1/CREDEN~1.SEC)")
	d, err = s.pol.Check(s.file)
	require.NoError(t, err)
	require.Equal(t, Deny, d.Effect, "a carve-out spelled short reopens nothing")
}

// TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles: only a rule segment of 8.3 shape (a
// tilde followed by a digit, or by a glob metacharacter that can stand for one, as in
// CREDEN~?.SEC, or a tilde inside a bracket expression, as in CREDEN[~]1.SEC) names an 8.3 name.
// A common backup-file rule such as Read(**/*~) has a tilde but names no 8.3 name, so it does not
// turn on the 8.3 comparison, and a deleted file's history is judged on its long name as before
// (docs/security.md, doc.go: "only while a rule names an 8.3 name"). The rule itself still holds for a file it names. An 8.3-shaped rule after a glob keeps
// the fail-closed refusal of a path that does not exist (D55).
func TestShortNames_ATildeThatNamesNoShortNameJudgesDeletedFiles(t *testing.T) {
	s := newShortEnv(t)
	deleted := filepath.Join(s.long, "configuration", "gone-but-archived.txt")
	backup := filepath.Join(s.long, "foo.txt~")
	s.write(t, backup, "b")

	for _, rule := range []string{"Read(**/*~)", "Read(*~)", "Read(**/*.txt~)", "Read(./foo.txt~)"} {
		t.Run(rule, func(t *testing.T) {
			s.deny(t, rule)
			d, err := s.pol.Check(deleted)
			require.NoError(t, err)
			require.Equal(t, Allow, d.Effect, "%q names no 8.3 name, so a deleted file is judged", rule)

			d, err = s.pol.Check(backup)
			require.NoError(t, err)
			require.Equal(t, Deny, d.Effect, "%q still denies the file it names", rule)
		})
	}

	for _, rule := range []string{
		"Read(**/CREDEN~1.SEC)", "Read(**/CREDEN~?.SEC)", "Read(**/CONFIG~*/**)",
		"Read(**/CREDEN[~]1.SEC)", "Read(**/CREDEN[}-~]1.SEC)",
	} {
		t.Run(rule, func(t *testing.T) {
			s.deny(t, rule)
			d, err := s.pol.Check(deleted)
			require.NoError(t, err)
			require.Equal(t, Deny, d.Effect, "%q names an 8.3 name: a deleted file is refused", rule)
			require.Equal(t, unknownShortName, d.Rule)

			d, err = s.pol.Check(s.file)
			require.NoError(t, err)
			require.Equal(t, Deny, d.Effect, "%q holds at the long name", rule)

			d, err = s.pol.Check(filepath.Join(s.long, "public.txt"))
			require.NoError(t, err)
			require.Equal(t, Allow, d.Effect, "control: public.txt is not the denied file")
		})
	}
}
