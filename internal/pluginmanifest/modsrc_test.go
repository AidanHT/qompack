package pluginmanifest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/pluginmanifest"
)

// modSourceNames is the mod's shipped source set, written out so a file added to or dropped from
// modsrc/ is a reviewed change rather than a silent one.
var modSourceNames = []string{"compact.ts", "format.ts", "register.ts", "target.gen.ts", "wire.gen.ts"}

func TestModSources_ShipsTheTopLevelSourcesAndNoTests(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		src, err := pluginmanifest.ModSources(goos)
		require.NoError(t, err)
		names := make([]string, 0, len(src))
		for name := range src {
			names = append(names, name)
		}
		sort.Strings(names)
		require.Equal(t, modSourceNames, names, "goos %s: tests under modsrc/tests are never shipped", goos)
		require.Equal(t, pluginmanifest.ModTargetSource(goos), src[pluginmanifest.ModTargetFile],
			"goos %s carries its own target file", goos)
	}
}

// TestModSources_EmbeddedTargetIsTheCommittedRendering keeps the committed target.gen.ts in step with
// ModTargetSource; `devtool gen-mod-wire --check` asserts the same from the other side.
func TestModSources_EmbeddedTargetIsTheCommittedRendering(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("modsrc", pluginmanifest.ModTargetFile))
	require.NoError(t, err)
	got = []byte(strings.ReplaceAll(string(got), "\r\n", "\n"))
	require.Equal(t, string(pluginmanifest.ModTargetSource(pluginmanifest.CommittedGOOS)), string(got))
}

func TestModSources_AreIdenticalAcrossTargetsButTheTargetFile(t *testing.T) {
	linux, err := pluginmanifest.ModSources("linux")
	require.NoError(t, err)
	windows, err := pluginmanifest.ModSources("windows")
	require.NoError(t, err)
	for name, b := range linux {
		if name == pluginmanifest.ModTargetFile {
			require.NotEqual(t, b, windows[name])
			continue
		}
		require.Equal(t, b, windows[name], name)
	}
}

// dollarUse matches the host API object used anywhere: a call or member access on `$`, or `$`
// passed or declared as a parameter.
var dollarUse = regexp.MustCompile(`\$\.|[(,]\s*\$\s*[,):]`)

// TestModSources_OnlyRegisterTouchesTheHostAPI holds the static-analysis rule the host's validator
// documents: `$` may be passed only to a top-level function of the same file, so every host call
// stays in register.ts and every other source is pure.
func TestModSources_OnlyRegisterTouchesTheHostAPI(t *testing.T) {
	src, err := pluginmanifest.ModSources(pluginmanifest.CommittedGOOS)
	require.NoError(t, err)
	for name, b := range src {
		if name == "register.ts" {
			continue
		}
		require.False(t, dollarUse.Match(b), "%s must not take or call `$`; host calls live in register.ts", name)
	}
	require.True(t, dollarUse.MatchString("on('session.start', async ($, e, next) => next(e))"),
		"the pattern must recognise a hook's `$` parameter, or the check above is vacuous")
	require.True(t, dollarUse.MatchString("await $.process.run(argv)"))
}

// importSpec matches the module specifier of an import or re-export declaration, including one
// whose braces span several lines, and of a bare side-effect import.
var importSpec = regexp.MustCompile(`(?m)^\s*(?:(?:import|export)\b[^'"]*?\bfrom|import)\s+['"]([^'"]+)['"]`)

// TestModSources_ImportOnlyEachOtherAndTheHostTypes holds the validator's import rule: relative
// imports of the mod's own files and the bare `claude-code` module, nothing else, and no dynamic
// import().
func TestModSources_ImportOnlyEachOtherAndTheHostTypes(t *testing.T) {
	for in, want := range map[string]string{
		"import {\n  A,\n  B,\n} from './wire.gen.ts'\n": "./wire.gen.ts",
		"import type { R } from 'claude-code'\n":         "claude-code",
		"import './side.ts'\n":                           "./side.ts",
		"export { x } from \"./x.ts\"\n":                 "./x.ts",
	} {
		m := importSpec.FindStringSubmatch(in)
		require.Len(t, m, 2, "the pattern must see %q, or the check below is vacuous", in)
		require.Equal(t, want, m[1])
	}
	require.Nil(t, importSpec.FindStringSubmatch("export const VERB_HELLO: Verb = 'hello'\n"))

	src, err := pluginmanifest.ModSources(pluginmanifest.CommittedGOOS)
	require.NoError(t, err)
	for name, b := range src {
		require.NotContains(t, string(b), "import(", "%s: a module holding import() does not load", name)
		require.NotContains(t, string(b), "require(", "%s: hooks modules are ES modules", name)
		for _, m := range importSpec.FindAllStringSubmatch(string(b), -1) {
			spec := m[1]
			if spec == "claude-code" {
				continue
			}
			require.True(t, strings.HasPrefix(spec, "./"), "%s imports %q", name, spec)
			_, shipped := src[strings.TrimPrefix(spec, "./")]
			require.True(t, shipped, "%s imports %q, which is not a shipped mod source", name, spec)
		}
	}
}

// bareDollar matches `$` as a token of its own: not part of a longer identifier, and not the `${`
// of a template literal's interpolation.
var bareDollar = regexp.MustCompile(`(?:^|[^\w$])\$(?:[^\w${]|$)`)

// dollarMember is a member access on `$` with a name after the dot (`$.session.id()`), one of the
// two places register.ts may write `$`. The other is a parameter or argument position
// (`($, e, next)`, `hello($)`, `function hello($: Host)`), which hostAliasUses checks by the
// characters on either side.
var dollarMember = regexp.MustCompile(`^\$\s*\.\s*[A-Za-z_]`)

// codeOnly returns src with comments removed and the contents of '...' and "..." literals blanked,
// so neither comment text nor a string's characters reach the scan, and a // or /* inside a string
// does not swallow the code after it. A newline is kept wherever one was, so lines stay lines.
// Template literals are left whole (their ${ is excluded by bareDollar, and any other `$` in one
// counts, which fails closed). Regular-expression literals are not recognised; register.ts has
// none. The scan of register.ts for host calls (devtool mod-validate) still counts comment text,
// so register.ts keeps `$.` out of its comments anyway.
func codeOnly(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				b.WriteByte('\n')
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				end = len(src) - i - 2
			}
			b.WriteString(strings.Repeat("\n", strings.Count(src[i:i+2+end], "\n")))
			i += 2 + end + 1
		case c == '\'' || c == '"':
			b.WriteByte(c)
			for i++; i < len(src) && src[i] != c && src[i] != '\n'; i++ {
				if src[i] == '\\' && i+1 < len(src) {
					i++
				}
				b.WriteByte(' ')
			}
			if i < len(src) {
				b.WriteByte(src[i])
			}
		case c == '`':
			b.WriteByte(c)
			for i++; i < len(src) && src[i] != '`'; i++ {
				if src[i] == '\\' && i+1 < len(src) {
					b.WriteByte(src[i])
					i++
				}
				b.WriteByte(src[i])
			}
			if i < len(src) {
				b.WriteByte('`')
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// hostAliasUses returns each use of `$` in src that is neither a member access nor a parameter or
// argument: an alias (`const h = $`), a destructuring (`const { fs } = $`), a bracket access
// (`$['fs']`), a spread, a return. The host's validator reads `$` calls by their written-out form,
// and the call scan mod-validate runs is a regular expression, so a call made through any of these
// is invisible to both.
func hostAliasUses(src string) []string {
	code := codeOnly(src)
	var bad []string
	for _, loc := range bareDollar.FindAllStringIndex(code, -1) {
		at := strings.IndexByte(code[loc[0]:loc[1]], '$') + loc[0]
		before := strings.TrimRight(code[:at], " \t\r\n")
		after := strings.TrimLeft(code[at+1:], " \t\r\n")
		param := strings.HasSuffix(before, "(") || strings.HasSuffix(before, ",")
		param = param && after != "" && strings.ContainsRune(",):", rune(after[0]))
		if dollarMember.MatchString(code[at:]) || param {
			continue
		}
		lineStart := strings.LastIndex(code[:at], "\n") + 1
		lineEnd := strings.IndexByte(code[at:], '\n')
		if lineEnd < 0 {
			lineEnd = len(code) - at
		}
		bad = append(bad, strings.TrimSpace(code[lineStart:at+lineEnd]))
	}
	return bad
}

// TestModSources_RegisterNeverAliasesTheHost bans every use of `$` in register.ts that would hide
// a host call from the validator and from mod-validate's scan.
func TestModSources_RegisterNeverAliasesTheHost(t *testing.T) {
	for _, bad := range []string{
		"const h = $",
		"const h = $;",
		"const { fs } = $",
		"const { process: p } = $",
		"$['fs'].read(p)",
		"$ ['fs']",
		"$?.['fs']",
		"return $",
		"const all = [$]",
		"use(...$)",
		"x = cond ? $ : other",
		"const u = 'a//b'; const h = $",
		"const u = \"x/*\"; const h = $",
		"const u = 'it\\'s // here'; const h = $",
	} {
		require.NotEmpty(t, hostAliasUses(bad), "the check must catch %q, or it is vacuous", bad)
	}
	for _, good := range []string{
		"on('session.start', async ($, e, next) => next(e))",
		"await hello($)",
		"await log(e, $)",
		"export async function hello($: EngineInterface): Promise<void> {}",
		"const id = await $.session.id()",
		"const bin = $ . plugin . root",
		"const s = `${value}`",
		"// a comment about the host's $ and `$`",
		"/* $ in a block comment */",
		"const fee$ = 1; const $x = 2",
		"const s = 'costs $5' // and $ here",
	} {
		require.Empty(t, hostAliasUses(good), "%q is a permitted use", good)
	}

	src, err := pluginmanifest.ModSources(pluginmanifest.CommittedGOOS)
	require.NoError(t, err)
	require.Empty(t, hostAliasUses(string(src["register.ts"])),
		"register.ts must write every host call out in full and pass `$` only as a parameter or argument")
}
