package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The mod's TypeScript sources under internal/pluginmanifest/modsrc are the repository's first
// product .ts files. gofumpt never sees them, the docmarkers scan reads only Markdown, and the
// catch-all `* text=auto` attribute is the only EOL rule that would otherwise reach them. These
// rows pin the explicit rules each of those three gets for them.

// modsrcFixture builds a module root with a modsrc tree and returns it.
func modsrcFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	return dir
}

func TestModSourceFiles_ListsEveryTSFileUnderModsrcOnly(t *testing.T) {
	dir := modsrcFixture(t, map[string]string{
		"internal/pluginmanifest/modsrc/register.ts":         "x\n",
		"internal/pluginmanifest/modsrc/tests/a.test.ts":     "x\n",
		"internal/pluginmanifest/modsrc/README.md":           "x\n",
		"internal/pluginmanifest/manifest.go":                "package p\n",
		"internal/canon/testdata/sample.ts":                  "x \r\n",
		"internal/pluginmanifest/modsrc/nested/deep/more.ts": "x\n",
	})
	got, err := modSourceFiles(dir)
	require.NoError(t, err)
	require.Equal(t, []string{
		"internal/pluginmanifest/modsrc/nested/deep/more.ts",
		"internal/pluginmanifest/modsrc/register.ts",
		"internal/pluginmanifest/modsrc/tests/a.test.ts",
	}, got, "testdata .ts files elsewhere are fixtures, not mod sources, and stay out of scope")
}

func TestModSourceFiles_NoTreeIsNoFiles(t *testing.T) {
	got, err := modSourceFiles(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestModSourceProblems(t *testing.T) {
	require.Empty(t, modSourceProblems([]byte("export const a = 1\n\nexport const b = 2\n")))

	cases := map[string]string{
		"\ufeffexport const a = 1\n":  "byte-order mark",
		"export const a = 1\r\n":      "CR",
		"export const a = 1  \n":      "trailing whitespace",
		"export const a = 1\t\n":      "trailing whitespace",
		"export const a = 1":          "newline",
		"export const a = 1\n\n":      "newline",
		"":                            "newline",
		"a\n  \nb\n":                  "trailing whitespace",
		"export const a = 1\r\nb\r\n": "CR",
	}
	for in, want := range cases {
		probs := modSourceProblems([]byte(in))
		require.NotEmpty(t, probs, "%q", in)
		require.Contains(t, strings.Join(probs, "; "), want, "%q", in)
	}
}

func TestFixModSource_RepairsEveryProblemItReports(t *testing.T) {
	for _, in := range []string{
		"\ufeffexport const a = 1  \r\nb\t\r\n\r\n\r\n",
		"no final newline",
		"a\n  \nb\n\n\n",
	} {
		fixed := fixModSource([]byte(in))
		require.Empty(t, modSourceProblems(fixed), "fix(%q) = %q", in, fixed)
	}
	require.Equal(t, "a\n\nb\n", string(fixModSource([]byte("a  \r\n\r\nb"))))
	clean := []byte("export const a = 1\n")
	require.Equal(t, clean, fixModSource(clean), "a conforming file is left byte-identical")
}

func TestCheckAndFixModSources(t *testing.T) {
	dir := modsrcFixture(t, map[string]string{
		"internal/pluginmanifest/modsrc/good.ts":        "export const a = 1\n",
		"internal/pluginmanifest/modsrc/bad.ts":         "export const a = 1  \r\n",
		"internal/pluginmanifest/modsrc/tests/bad.ts":   "x",
		"internal/canon/testdata/fixture-left-alone.ts": "x \r\n",
	})
	err := checkModSources(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "internal/pluginmanifest/modsrc/bad.ts")
	require.Contains(t, err.Error(), "internal/pluginmanifest/modsrc/tests/bad.ts")
	require.NotContains(t, err.Error(), "good.ts")

	require.NoError(t, fixModSources(dir))
	require.NoError(t, checkModSources(dir))

	fixture, readErr := os.ReadFile(filepath.Join(dir, "internal", "canon", "testdata", "fixture-left-alone.ts"))
	require.NoError(t, readErr)
	require.Equal(t, "x \r\n", string(fixture), "fmt never rewrites a .ts file outside modsrc")
}

func TestFindModSourceMarkers_ReadsCommentsOnly(t *testing.T) {
	src := strings.Join([]string{
		"// <FILL-IN the binary path>",
		"/**",
		" * Returns <TODO-WRITE this> for now.",
		" */",
		"export function f<T extends string>(x: Promise<HelloResponse>, m: Record<K, V>): T {",
		"  return x as unknown as T // <N> is fine notation",
		"}",
		"const s = 'a <NOT-A-COMMENT> string'",
	}, "\n")
	got := findModSourceMarkers("internal/pluginmanifest/modsrc/x.ts", src)
	tokens := make([]string, 0, len(got))
	for _, m := range got {
		tokens = append(tokens, m.token)
	}
	require.Equal(t, []string{"<FILL-IN the binary path>", "<TODO-WRITE this>"}, tokens,
		"placeholders in comments are caught; generics and string literals are code, not prose")
	require.Equal(t, 1, got[0].line)
	require.Equal(t, 3, got[1].line)
}

func TestMarkerSources_IncludesModSources(t *testing.T) {
	dir := modsrcFixture(t, map[string]string{
		"internal/pluginmanifest/modsrc/register.ts": "// <UNFILLED-PLACEHOLDER here>\nexport const a = 1\n",
	})
	prev := root
	root = dir
	t.Cleanup(func() { root = prev })

	found, scanned, err := modSourceMarkers()
	require.NoError(t, err)
	require.Equal(t, 1, scanned)
	require.Len(t, found, 1)
	require.Equal(t, "internal/pluginmanifest/modsrc/register.ts", found[0].file)
}

// TestGitattributes_ModSourcesAreLF pins the explicit EOL rule. The bytes under modsrc are embedded
// into the binary and shipped in every bundle, so a CRLF checkout must not change them.
func TestGitattributes_ModSourcesAreLF(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(testModuleRoot(t), ".gitattributes"))
	require.NoError(t, err)
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	require.Contains(t, lines, modSourceDir+"/**/*.ts text eol=lf")
}

// TestUnfilledMarkers_IsTheOneRuleBothScansUse: the plan-document scan and the mod-source scan
// share one matcher, so the two cannot drift. Each corpus line must give the same tokens from
// unfilledMarkers, from the Markdown scan of the line, and from the mod-source scan of the line
// written as a comment.
func TestUnfilledMarkers_IsTheOneRuleBothScansUse(t *testing.T) {
	corpus := map[string][]string{
		"<FILL-IN the path>":                   {"<FILL-IN the path>"},
		"<TODO write this>":                    {"<TODO write this>"},
		"<COLLISION-LOG>":                      {"<COLLISION-LOG>"},
		"<N> and <EMAIL> are notation":         nil,
		"<ISO-8601> and <SP-NN> and <P 12>":    nil,
		"<TempDir> is a metavariable":          nil,
		"two: <A-B> then <C d>":                {"<A-B>", "<C d>"},
		"plain prose with no markers at all":   nil,
		"generic-looking <T> stays notation":   nil,
		"head then tail <WRITE-ME please now>": {"<WRITE-ME please now>"},
	}
	for line, want := range corpus {
		require.Equal(t, want, unfilledMarkers(line), "unfilledMarkers(%q)", line)

		var plan, mod []string
		for _, m := range findPlanMarkers("p.md", line) {
			plan = append(plan, m.token)
		}
		for _, m := range findModSourceMarkers("m.ts", "// "+line) {
			mod = append(mod, m.token)
		}
		require.Equal(t, want, plan, "plan scan of %q", line)
		require.Equal(t, want, mod, "mod-source scan of %q", line)
	}
}
