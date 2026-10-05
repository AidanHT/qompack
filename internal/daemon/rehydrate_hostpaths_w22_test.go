package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/store"
)

// Wave 22's daemon rows over audit 2's rehydrate findings (coordinator decisions D66 and D67).

// TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece is audit 2's finding
// 26 through the real adapter, the real host rules and the store's own previews: a path-named JSON
// value holding a list whose later piece names a path outside the project was judged as one relative
// path, which the host, joining it under the root, refused nothing about, and section 6 showed it.
// Each piece is judged for a path outside the project; a single project path with a space in it, in
// a plain project and in one whose own path has a space, is still shown. Wave 22's verify found a
// path after a quote and a climb after an option's `=` or an `@` still shown (eca33155 showed them
// too): each is judged where a reader starts the path, and the names a project's paths hold are
// still shown. Its fix round 2 found a NUL-separated list (the store's preview keeps the NUL as
// \u0000), a glued redirect or `&&`, and a letter whose ANSI best fit is a quote or a bar still
// showing the outside path after them, at eca33155 too: a control character splits a list, and the
// others start a path anywhere in a piece.
func TestRehydrateHostPaths_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := uat12Project(t, elem...)
			out := filepath.Join(shortProjectDir(t, "outside"), "x.txt")
			v := func(key string, value any) string { return storePreviewOf(t, map[string]any{key: value}) }
			res := requireToolSummaries(t, root,
				[]string{
					v("file", "src/my notes.txt"),
					v("paths", []string{"docs/design notes.md", "src/main.go"}),
					v("files", "src/main.go src/util.go"),
					v("notebook_path", filepath.Join(root, "nb", "my notes.ipynb")),
					v("paths", "src/main.go "+filepath.Join(root, "docs", "b.md")),
					v("paths", []string{"app/(auth)/page.tsx", "pages/[slug].tsx", "src/routes/+page.svelte"}),
					v("paths", `"src/main.go" "src/util.go"`),
					v("notebook_path", `"`+filepath.Join(root, "nb", "a b.ipynb")+`"`),
					v("paths", "src/main.go --out=docs/b.md"),
					v("paths", "src/main.go\x00src/util.go"),
					v("file", "docs/R&D/plan.md"),
				},
				[]string{
					v("paths", "src/main.go,"+out),
					v("paths", "src/main.go "+out),
					v("file", "src/main.go /etc/passwd"),
					v("paths", "src/main.go ~/.ssh/id_rsa"),
					v("paths", "src/main.go $HOME/.aws/credentials"),
					v("paths", []string{"src/main.go", "b " + out}),
					v("paths", "src/main.go C:secret.txt"),
					v("paths", "src/main.go Temp:secret.txt"),
					v("paths", "src/main.go;"+out),
					v("paths", "src/main.go:/etc/passwd"),
					v("path", "Temp:secret.txt"),
					v("paths", `src/main.go "/etc/passwd"`),
					v("paths", `src/main.go '~/.ssh/id_rsa'`),
					v("paths", []string{"src/main.go", `"/etc/passwd"`}),
					v("path", `"/etc/passwd"`),
					v("paths", `src/main.go "`+out+`"`),
					v("paths", "src/main.go (/etc/passwd)"),
					v("paths", "src/main.go --out=../../outside/x.txt"),
					v("paths", "src/main.go @../outside/x.txt"),
					v("paths", "src/main.go:../outside/x.txt"),
					v("path", "--out=../outside/x.txt"),
					v("paths", "src/main.go\x00/etc/passwd"),
					v("paths", "src/main.go\x00~/.ssh/id_rsa"),
					v("paths", "src/main.go\x1f../../outside/x.txt"),
					v("paths", "src/main.go>/etc/passwd"),
					v("paths", "src/main.go&&/etc/passwd"),
					v("paths", "src/main.go\u02ba/etc/passwd"),
					v("paths", "src/main.go\u01c0/etc/passwd"),
					v("path", filepath.Join(root, "src", "main.go")+">/etc/passwd"),
				})
			for _, leak := range []string{filepath.Join("outside", "x.txt"), "passwd", "id_rsa", "credentials", "secret.txt", "../outside"} {
				require.NotContains(t, res.Text, leak)
			}
		})
	}
}

// longestTempBase is the longest temporary directory a hosted runner hands this platform's tests,
// with os.MkdirTemp's longest suffix (a uint32's ten digits) under it: on macOS the TMPDIR
// /var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T (48 characters, read from hosted logs, ci
// 36981590450 and nightly 36981711009), which every POSIX row is held to; on Windows windows-latest's
// TEMP in its long form, C:\Users\runneradmin\AppData\Local\Temp, where JSON's doubled backslash is
// the tighter limit. audit 2's finding 36: shortProjectDir spelled a macOS root from TMPDIR, and the
// Docker fixture's preview, about 113 characters plus the suffix, passed the store's 120-character
// width only for a suffix of seven digits or fewer.
func longestTempBase() string {
	if runtime.GOOS == "windows" {
		return `C:\Users\runneradmin\AppData\Local\Temp\q4294967295`
	}
	return "/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/q4294967295"
}

// TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory is finding 36's regression
// row: the UAT-12 rows' previews that spell the project root, built under the longest temporary
// directory a hosted runner spells, are each uncut, so a row cannot pass on a short local temporary
// directory and fail on a hosted runner. Hosted CI's H2 adds wave 19i's r5 previews, which macOS cut
// for U+0130, U+212A and U+212B (a NotebookEdit of the denied file under the variant's spaced root:
// canonical JSON, and three bytes for each of those letters). storePreview and storePreviewOf hold
// every other row's previews to the same directory.
func TestRehydrateHostPaths_RootPreviewsFitUnderTheLongestTemporaryDirectory(t *testing.T) {
	root := filepath.Join(longestTempBase(), "John Smith", "proj")
	shown, withheld := commonIdiomPreviews(root)
	us, uw := usefulSummaryPreviews(root)
	all := append(append(append(shown, withheld...), us...), uw...)
	for _, rc := range unicodeRootVariants {
		for _, seg := range []string{rc.variant, rc.variant + " berg"} {
			all = append(all, map[string]any{"notebook_path": filepath.Join(longestTempBase(), seg, "proj", "private", "deny.txt")})
		}
	}
	for _, args := range all {
		raw, err := json.Marshal(args)
		require.NoError(t, err)
		_, preview := store.ArgsDigest(raw)
		require.False(t, strings.HasSuffix(preview, "…"), "the store cuts the preview of %v", args)
	}
}

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
// a subtest of it: the temporary directory, as tmp or its resolved spelling, then the top-level
// test's name (t.TempDir names its directory after the test, dropping a subtest's `/`).
func spellsTestTempDir(s, name, tmp string, fold bool) bool {
	top, _, _ := strings.Cut(name, "/")
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

// TestRehydrateHostPaths_AHostedRunnersSpellingRespellsEveryBaseWhole pins storePreview's respelling
// (hosted CI's H2): a base whose resolved spelling holds the spelling it was made with (macOS's
// /tmp/q… resolves to /private/tmp/q…, the spelling the r5 rows' canonical roots have) is respelled
// once, as the hosted directory, not as /private before it, which put the r5 previews one or two
// bytes over the store's width; a Windows base in each of its spellings (JSON-escaped, forward
// slashes, upper-cased where paths fold); and a value spelling a t.TempDir() of the row is caught.
func TestRehydrateHostPaths_AHostedRunnersSpellingRespellsEveryBaseWhole(t *testing.T) {
	const posixLong = "/var/folders/36/tjdph2t965j8snz9_vkdnw0r0000gn/T/q4294967295"
	mac := []string{"/tmp/q1234567890", "/private/tmp/q1234567890"}
	require.Equal(t, posixLong+"/\u0130ris berg/proj/private/deny.txt",
		onAHostedRunner("/private/tmp/q1234567890/\u0130ris berg/proj/private/deny.txt", mac, posixLong, true))
	require.Equal(t, "cat "+posixLong+"/proj/a.go", onAHostedRunner("cat /tmp/q1234567890/proj/a.go", mac, posixLong, true))

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

	require.True(t, spellsTestTempDir(filepath.Join(os.TempDir(), "TestX123", "001", "proj"), "TestX/sub", os.TempDir(), false),
		"a value spelling a t.TempDir() of the row is caught")
	require.False(t, spellsTestTempDir(filepath.Join(os.TempDir(), "q123", "proj"), "TestX", os.TempDir(), false),
		"a shortProjectDir's is not")
}
