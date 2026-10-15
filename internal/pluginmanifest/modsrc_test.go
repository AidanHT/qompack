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

// importSpec matches the module specifier of an import or re-export declaration.
var importSpec = regexp.MustCompile(`(?m)^\s*(?:import|export)\b[^'"\n]*\bfrom\s+'([^']+)'`)

// TestModSources_ImportOnlyEachOtherAndTheHostTypes holds the validator's import rule: relative
// imports of the mod's own files and the bare `claude-code` module, nothing else, and no dynamic
// import().
func TestModSources_ImportOnlyEachOtherAndTheHostTypes(t *testing.T) {
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
