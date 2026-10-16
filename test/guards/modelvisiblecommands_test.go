package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// V7 F11 (ruling R45) renders the diagnostics commands with `disable-model-invocation: true`
// (pluginmanifest.CommandDoc.UserOnly), so the host keeps them out of the model's skill listing. Text
// the model reads must then never tell it to run one: it cannot, and the host would refuse the
// attempt. Model-visible Qompack text may name only a model-invocable command (today /qompack:pin,
// which a checkpoint drop reason tells the model to run) as something to run. Any other command
// name must be phrased as the user's ("the user's /qompack:dropped shows the same list") or sit at a
// site listed below, where the text never reaches the model.

// slashCommandRe matches one /qompack:<name> mention. A format verb ("/qompack:%s") or a bare
// prefix ("/qompack:") names no command and does not match.
var slashCommandRe = regexp.MustCompile(`/qompack:([a-z][a-z0-9-]*)`)

// usersPhrasing is the text that, immediately before a mention in the same string, makes it the
// user's command rather than an instruction to the model.
const usersPhrasing = "the user's "

// commandMention is one product string that names a slash command the model may not run, and why
// that string never reaches the model.
type commandMention struct {
	file string // module-relative, slash-separated
	fn   string // FuncDecl name, or packageScope
	name string // the command name after "/qompack:"
	why  string
}

var notModelVisibleMentions = []commandMention{
	{
		file: "internal/checkpoint/inject.go", fn: packageScope, name: "session-record",
		why: "InjectionCloseTag, an HTML-comment marker that closes the injected session record; " +
			"it names no command and nothing to run",
	},
	{
		file: "internal/checkpoint/inject.go", fn: packageScope, name: "injected",
		why: "LegacyInjectionCloseTag, the pre-rename marker StripInjections still recognises; " +
			"it names no command and nothing to run",
	},
	{
		file: "internal/daemon/handlers.go", fn: "degradeBanner", name: "status",
		why: "the degraded-mode banner is SessionStart's systemMessage, which the host shows to the " +
			"user (hooks reference: \"Warning message shown to the user\"), not to the model",
	},
	{
		file: "internal/negknow/negknowtest/behaviour.go", fn: "runMaintainerSurfaceCase", name: "status",
		why: "a reason string in the ledger conformance suite's own fixture record, which only tests " +
			"run against a ledger they created",
	},
}

// TestGuard_ModelVisibleTextNamesOnlyInvocableCommands keeps notModelVisibleMentions exact: every
// string literal in product code that names a command the model may not run must be phrased as the
// user's or be a row, and every row must still match a literal. The generated command files are
// held to the same rule with no rows, because a command's body is what the model reads when the
// user runs it.
func TestGuard_ModelVisibleTextNamesOnlyInvocableCommands(t *testing.T) {
	m := pluginmanifest.Default(core.Version)
	invocable := map[string]bool{}
	var invocableNames, userOnly []string
	for _, c := range m.Commands {
		if c.UserOnly {
			userOnly = append(userOnly, c.Name)
			continue
		}
		invocable[c.Name] = true
		invocableNames = append(invocableNames, c.Name)
	}
	require.Equal(t, []string{"pin"}, invocableNames,
		"F11 (R45) leaves exactly /qompack:pin model-invocable; a change to CommandDoc.UserOnly must "+
			"re-decide this guard and efficiency/DESIGN.md §3.6")
	require.NotEmpty(t, userOnly)

	root := repoRoot(t)
	found, files := scanCommandMentions(t, root, invocable)
	require.GreaterOrEqual(t, files, minProductFiles, "the scan parsed too few product files to prove anything")

	listed := map[string]bool{}
	for _, r := range notModelVisibleMentions {
		key := r.file + ":" + r.fn + ":" + r.name
		require.False(t, listed[key], "notModelVisibleMentions names %s twice", key)
		listed[key] = true
		require.NotEmpty(t, r.why, "%s needs its reason", key)
		require.True(t, found[key], "notModelVisibleMentions row %s no longer matches a literal; remove it", key)
	}

	var unlisted []string
	for key := range found {
		if !listed[key] {
			unlisted = append(unlisted, key)
		}
	}
	sort.Strings(unlisted)
	require.Empty(t, unlisted,
		"these product strings tell the reader to run a /qompack: command the model cannot run "+
			"(F11, R45: it is user-only or not shipped). If the text reaches the model, phrase the "+
			"mention as %q or name /qompack:pin; if it never does, add a notModelVisibleMentions row "+
			"saying why", usersPhrasing)

	generated, err := m.Files()
	require.NoError(t, err)
	for rel, b := range generated {
		if !strings.HasPrefix(rel, "plugin/commands/") {
			continue
		}
		bad := mentionsOutsidePolicy(string(b), invocable)
		require.Empty(t, bad, "%s tells the model to run a command it cannot (F11, R45)", rel)
	}
}

// TestGuard_CommandMentionScannerSeesEveryShape is the scanner's self-test: interpreted and raw
// literals at package scope and inside a function's closure are seen, a comment, a format verb and
// a bare prefix are not, and the user's phrasing and an invocable command are not recorded.
func TestGuard_CommandMentionScannerSeesEveryShape(t *testing.T) {
	src := "package sample\n\n" +
		"// A comment naming /qompack:status is not text anyone receives.\n" +
		"const marker = \"<!-- /qompack:injected -->\"\n" +
		"const prefix = \"/qompack:\"\n\n" +
		"func direct() []string {\n" +
		"\treturn []string{\n" +
		"\t\t\"run /qompack:pin --list\",\n" +
		"\t\t\"the user's /qompack:dropped shows the same list\",\n" +
		"\t\t\"usage: /qompack:%s\",\n" +
		"\t\t\"See /qompack:status.\",\n" +
		"\t}\n" +
		"}\n\n" +
		"func closure() func() string {\n" +
		"\treturn func() string { return `try /qompack:why dec_1 or /qompack:checkpoint` }\n" +
		"}\n\n" +
		"func mixed() string { return \"the user's /qompack:eval, then /qompack:recall\" }\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments|parser.SkipObjectResolution)
	require.NoError(t, err)

	got := map[string]bool{}
	commandMentionsInFile(f, "sample.go", map[string]bool{"pin": true}, got)
	require.Equal(t, map[string]bool{
		"sample.go:" + packageScope + ":injected": true,
		"sample.go:direct:status":                 true,
		"sample.go:closure:why":                   true,
		"sample.go:closure:checkpoint":            true,
		"sample.go:mixed:recall":                  true,
	}, got, "the scan must see every literal shape and skip comments, format verbs, the user's "+
		"phrasing and invocable commands")
}

// scanCommandMentions parses every non-test Go file under productReadRoots and returns the
// "file:fn:name" keys of the string literals that name a command outside the policy, with the number
// of files parsed.
func scanCommandMentions(t *testing.T, root string, invocable map[string]bool) (map[string]bool, int) {
	t.Helper()
	found := map[string]bool{}
	files := 0
	for _, top := range productReadRoots {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return rerr
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			files++
			commandMentionsInFile(f, filepath.ToSlash(rel), invocable, found)
			return nil
		})
		require.NoError(t, err, "scanning %s", top)
	}
	return found, files
}

// commandMentionsInFile adds to into the key of every string literal in f, per declaration, that
// names a command outside the policy.
func commandMentionsInFile(f *ast.File, rel string, invocable map[string]bool, into map[string]bool) {
	for _, decl := range f.Decls {
		fn := packageScope
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name != nil {
			fn = fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				s = lit.Value
			}
			for _, name := range mentionsOutsidePolicy(s, invocable) {
				into[rel+":"+fn+":"+name] = true
			}
			return true
		})
	}
}

// mentionsOutsidePolicy returns the command names s mentions that are neither invocable nor phrased
// as the user's.
func mentionsOutsidePolicy(s string, invocable map[string]bool) []string {
	var out []string
	for _, loc := range slashCommandRe.FindAllStringSubmatchIndex(s, -1) {
		name := s[loc[2]:loc[3]]
		if invocable[name] || strings.HasSuffix(s[:loc[0]], usersPhrasing) {
			continue
		}
		out = append(out, name)
	}
	return out
}
