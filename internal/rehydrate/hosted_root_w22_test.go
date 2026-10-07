package rehydrate

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// Hosted CI's H2 (ci.yml run 37229942287, macos-latest and windows-latest): a row's project root
// built from t.TempDir spells the runner's temporary directory and the test's name, so on a hosted
// runner (macOS's 48-character TMPDIR, Windows's C:\Users\RUNNER~1\AppData\Local\Temp) a summary the
// row requires shown in full is longer than the store's preview width, which a recorded summary never
// is. TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames failed on both. A root that a summary
// spells is made by shortRoot, and requestFor holds every tool summary of a request to a hosted
// runner's longest temporary directory (requireSummaryFitsOnAHostedRunner).

// shortRoot is a fresh directory to build a project root under, short on every runner: off Windows
// it is made in /tmp, never in TMPDIR; on Windows in the temporary directory spelled by its long
// names (a hosted runner spells it with an 8.3 name, and a root holding a `~` has no root unit,
// D64(1)). Both its spellings, as made and as resolved (macOS's /tmp is a link to /private/tmp), are
// recorded for requireSummaryFitsOnAHostedRunner.
func shortRoot(t *testing.T) string {
	t.Helper()
	dir := ""
	if runtime.GOOS != "windows" {
		dir = "/tmp"
	}
	base, err := os.MkdirTemp(dir, "q")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(paths.Long(base)) })
	resolved, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	if runtime.GOOS == "windows" {
		base = resolved
	}
	shortBases.add(base, resolved)
	return base
}

// shortBases are the spellings of every directory shortRoot made.
var shortBases baseSpellings

// baseSpellings is a set of directory spellings, safe for parallel rows.
type baseSpellings struct {
	mu   sync.Mutex
	list []string
}

func (b *baseSpellings) add(spellings ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range spellings {
		known := false
		for _, k := range b.list {
			known = known || k == s
		}
		if !known {
			b.list = append(b.list, s)
		}
	}
}

func (b *baseSpellings) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.list...)
}

// longestTempBase is the longest temporary directory a hosted runner hands this platform's tests,
// with os.MkdirTemp's longest suffix (a uint32's ten digits) under it: off Windows macos-latest's
// 48-character TMPDIR, /var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T (hosted logs, ci 36981590450
// and 37229942287), which every POSIX row is held to; on Windows windows-latest's TEMP in its long
// form. The daemon's rows hold their store previews to the same directory.
func longestTempBase() string {
	if runtime.GOOS == "windows" {
		return `C:\Users\runneradmin\AppData\Local\Temp\q4294967295`
	}
	return "/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/q4294967295"
}

// onAHostedRunner is s with each of bases respelled as long: longest first, so that a spelling that
// holds another (macOS's /private/tmp/q… holds /tmp/q…) is replaced whole; in either slash style and
// JSON-escaped; and in any ASCII case where fold is set, as the platform's paths fold (a row may
// spell its root upper-cased).
func onAHostedRunner(s string, bases []string, long string, fold bool) string {
	sorted := append([]string(nil), bases...)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, b := range sorted {
		s = replaceFolded(s, strings.ReplaceAll(b, `\`, `\\`), strings.ReplaceAll(long, `\`, `\\`), fold)
		s = replaceFolded(s, b, long, fold)
		s = replaceFolded(s, forwardSlashes(b), forwardSlashes(long), fold)
	}
	return s
}

// forwardSlashes is p with each backslash a forward slash, on every platform, so that a row pins a
// Windows base's respelling wherever it runs.
func forwardSlashes(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// replaceFolded is s with every occurrence of old replaced by repl, an ASCII letter's case folded
// when fold is set and no other character's.
func replaceFolded(s, old, repl string, fold bool) string {
	if old == "" {
		return s
	}
	if !fold {
		return strings.ReplaceAll(s, old, repl)
	}
	first := indexFolded(s, old)
	if first < 0 {
		return s
	}
	var b strings.Builder
	b.WriteString(s[:first])
	for i := first; i < len(s); {
		if i+len(old) <= len(s) && asciiEqualFold(s[i:i+len(old)], old) {
			b.WriteString(repl)
			i += len(old)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// indexFolded is the index of old's first occurrence in s, an ASCII letter's case folded, or -1.
func indexFolded(s, old string) int {
	for i := 0; i+len(old) <= len(s); i++ {
		if asciiEqualFold(s[i:i+len(old)], old) {
			return i
		}
	}
	return -1
}

// asciiEqualFold reports whether a and b, of one length, are equal once their ASCII letters are
// lower-cased.
func asciiEqualFold(a, b string) bool {
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// spellsTestTempDir reports whether s spells a directory t.TempDir made for the test named name or
// a subtest of it: the directory t.TempDir makes them under (tmp, testTempBase), as given or as
// resolved, then the top-level test's name as t.TempDir spells it (tempDirPattern). A subtest's
// directory is named from its whole name (`TestX/sub` is `TestXsub…`), so it starts with that too.
func spellsTestTempDir(s, name, tmp string, fold bool) bool {
	top, _, _ := strings.Cut(name, "/")
	top = tempDirPattern(top)
	for _, sp := range tempSpellings(tmp) {
		dir := filepath.Join(sp, top)
		for _, form := range []string{dir, forwardSlashes(dir), strings.ReplaceAll(dir, `\`, `\\`)} {
			if (fold && indexFolded(s, form) >= 0) || (!fold && strings.Contains(s, form)) {
				return true
			}
		}
	}
	return false
}

// tempDirPattern is name as go1.26.6's t.TempDir spells it in its directory's name
// (testing.common.makeTempDir): cut to its first 64 bytes, then with every character dropped but a
// letter, a number and one of `!#$%&()+,-.=@^_{}~ ` (removeSymbolsExcept, which also drops a rune
// the cut split). os.MkdirTemp then appends its random suffix (candidate 8's diff verify, finding 10:
// the guard compared the uncut name, so it never caught a row whose name is longer).
func tempDirPattern(name string) string {
	name = name[:min(len(name), 64)]
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || strings.ContainsRune("!#$%&()+,-.=@^_{}~ ", r) {
			return r
		}
		return -1
	}, name)
}

// tempSpellings is tmp and, when it differs, its resolved spelling, resolved once per directory.
func tempSpellings(tmp string) []string {
	tempResolved.mu.Lock()
	defer tempResolved.mu.Unlock()
	if s, ok := tempResolved.m[tmp]; ok {
		return s
	}
	s := []string{tmp}
	if r, err := filepath.EvalSymlinks(tmp); err == nil && r != tmp {
		s = append(s, r)
	}
	if tempResolved.m == nil {
		tempResolved.m = make(map[string][]string)
	}
	tempResolved.m[tmp] = s
	return s
}

var tempResolved struct {
	mu sync.Mutex
	m  map[string][]string
}

// requireSummaryFitsOnAHostedRunner requires a tool summary a row hands Build to spell no directory
// t.TempDir made for the row, and, when it spells a directory shortRoot made, to be within the
// store's preview width (previewWidth bytes, store.argsPreviewMax) with that directory respelled as
// longestTempBase: a summary a hosted runner's store records uncut. A row that builds a cut summary
// builds it within the width, as the store does, so it is held to the same bound. A summary that
// spells no root is the same on every runner and is not judged here (a row may pass Build a summary
// over the width on purpose).
func requireSummaryFitsOnAHostedRunner(t *testing.T, s string) {
	t.Helper()
	fold := paths.DefaultFold()
	require.False(t, spellsTestTempDir(s, t.Name(), testTempBase(), fold),
		"fixture: %q spells t.TempDir(), whose length is the runner's; build the root under shortRoot", s)
	if hosted := onAHostedRunner(s, shortBases.snapshot(), longestTempBase(), fold); hosted != s {
		require.LessOrEqual(t, len(hosted), previewWidth,
			"fixture: on a hosted runner the store would cut %q, which reads %q there", s, hosted)
	}
}

// TestShortRoot_IgnoresTheTemporaryDirectory is H2's guard on where a root is made: with TMPDIR set
// to a directory of at least 48 characters (macOS hosted's length), shortRoot's directory is not
// under it, on any platform, so a summary spelling the root is as long on a hosted runner as here.
func TestShortRoot_IgnoresTheTemporaryDirectory(t *testing.T) {
	long := filepath.Join(os.TempDir(), strings.Repeat("t", 48))
	require.NoError(t, os.MkdirAll(paths.Long(long), 0o700))
	t.Cleanup(func() { _ = os.RemoveAll(paths.Long(long)) })
	require.GreaterOrEqual(t, len(long), 48, "fixture: the simulated TMPDIR is at least as long as macOS hosted's")
	t.Setenv("TMPDIR", long)

	base := shortRoot(t)
	require.False(t, strings.HasPrefix(paths.Key(base), paths.Key(long)),
		"the root's directory %s is under TMPDIR %s", base, long)
	if runtime.GOOS != "windows" {
		require.LessOrEqual(t, len(base), len("/private/tmp/q4294967295"),
			"off Windows the root's directory is /tmp's, as made or as resolved, and its suffix")
	}
}

// TestOnAHostedRunner_RespellsEveryBaseWhole pins requireSummaryFitsOnAHostedRunner's respelling:
// a base whose resolved spelling holds the spelling it was made with (macOS's /tmp/q… resolves to
// /private/tmp/q…) is respelled once, as the hosted directory, not as /private before it; a Windows
// base in each of its spellings (JSON-escaped, forward slashes, upper-cased where paths fold); and a
// summary that fits here and not on a hosted runner is caught.
func TestOnAHostedRunner_RespellsEveryBaseWhole(t *testing.T) {
	const posixLong = "/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/q4294967295"
	mac := []string{"/tmp/q1234567890", "/private/tmp/q1234567890"}
	require.Equal(t, posixLong+"/\u0130ris berg/proj/private/deny.txt",
		onAHostedRunner("/private/tmp/q1234567890/\u0130ris berg/proj/private/deny.txt", mac, posixLong, true))
	require.Equal(t, "cat "+posixLong+"/proj/a.go",
		onAHostedRunner("cat /tmp/q1234567890/proj/a.go", mac, posixLong, true))

	const winLong = `C:\Users\runneradmin\AppData\Local\Temp\q4294967295`
	win := []string{`C:\Users\Quant\AppData\Local\Temp\q123`}
	for _, tc := range []struct{ in, want string }{
		{`C:\Users\Quant\AppData\Local\Temp\q123\proj`, winLong + `\proj`},
		{`{"file":"C:\\Users\\Quant\\AppData\\Local\\Temp\\q123\\proj"}`, `{"file":"` + strings.ReplaceAll(winLong, `\`, `\\`) + `\\proj"}`},
		{`git -C C:/Users/Quant/AppData/Local/Temp/q123/proj status`, "git -C " + forwardSlashes(winLong) + "/proj status"},
		{`C:\USERS\QUANT\APPDATA\LOCAL\TEMP\Q123\PROJ`, winLong + `\PROJ`},
	} {
		require.Equal(t, tc.want, onAHostedRunner(tc.in, win, winLong, true), "%q", tc.in)
	}
	require.Equal(t, `C:\USERS\QUANT\APPDATA\LOCAL\TEMP\Q123\PROJ`,
		onAHostedRunner(`C:\USERS\QUANT\APPDATA\LOCAL\TEMP\Q123\PROJ`, win, winLong, false),
		"where paths do not fold, another case is another directory")

	fits := "/tmp/q1234567890/" + strings.Repeat("x", previewWidth-len("/tmp/q1234567890/"))
	require.Len(t, fits, previewWidth, "fixture: the summary fits the width here")
	require.Greater(t, len(onAHostedRunner(fits, mac, posixLong, false)), previewWidth,
		"and is over it on a hosted runner")

	require.True(t, spellsTestTempDir(filepath.Join(os.TempDir(), "TestX123", "001", "proj"), "TestX/sub", os.TempDir(), false),
		"a summary spelling a t.TempDir() of the row is caught")
	require.False(t, spellsTestTempDir(filepath.Join(os.TempDir(), "q123", "proj"), "TestX", os.TempDir(), false),
		"a shortRoot's is not")
}

// TestSpellsTestTempDir_CatchesTheDirectoryOfARowWhoseNameIsLongerThanTheCut is candidate 8's diff
// verify, finding 10. go1.26.6's t.TempDir names its directory after the test's name cut to 64 bytes
// (testing.common.makeTempDir), and the guard looked for the whole top-level name, so it never caught
// a directory of a row whose name is longer: 11 daemon preview rows and
// TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements have such names, and a future
// one could build its root under t.TempDir again and bring back hosted CI's H2 unseen. This row's own
// name is longer than the cut; the guard catches its t.TempDir() and its subtest's, in each spelling
// requireSummaryFitsOnAHostedRunner reads, and a shortRoot's is still not caught.
func TestSpellsTestTempDir_CatchesTheDirectoryOfARowWhoseNameIsLongerThanTheCut(t *testing.T) {
	require.Greater(t, len(t.Name()), 64, "fixture: the row's name is longer than t.TempDir's cut")
	fold := paths.DefaultFold()
	caught := func(t *testing.T, dir string) {
		t.Helper()
		p := filepath.Join(dir, "proj", "a.go")
		for _, s := range []string{p, forwardSlashes(p), strings.ReplaceAll(p, `\`, `\\`), "cat " + p} {
			require.True(t, spellsTestTempDir(s, t.Name(), testTempBase(), fold), "%q spells a t.TempDir() of %s", s, t.Name())
		}
	}
	caught(t, t.TempDir())
	t.Run("sub", func(t *testing.T) { caught(t, t.TempDir()) })
	require.False(t, spellsTestTempDir(filepath.Join(shortRoot(t), "proj"), t.Name(), testTempBase(), fold), "a shortRoot's is not")

	long := "Test" + strings.Repeat("Long", 20)
	require.True(t, spellsTestTempDir(filepath.Join(os.TempDir(), long[:64]+"123", "001"), long+"/sub", os.TempDir(), false),
		"the name is cut to 64 bytes before os.MkdirTemp's suffix")
}

// testTempBase is the directory t.TempDir makes its directories under: GOTMPDIR when it is set, as
// os.MkdirTemp(os.Getenv("GOTMPDIR"), …) reads it in testing.common.makeTempDir, else the temporary
// directory.
func testTempBase() string {
	if d := os.Getenv("GOTMPDIR"); d != "" {
		return d
	}
	return os.TempDir()
}
