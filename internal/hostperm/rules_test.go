package hostperm

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The rule tests build a RuleSet straight from settings JSON with a chosen platform, so the POSIX
// and Windows path semantics are both exercised on every host. Nothing here touches the disk: link
// resolution is off whenever the chosen platform is not the real one, and the fake roots below are
// never real directories on any platform.

// pureEnv describes one fake machine.
type pureEnv struct {
	goos string
	root string // the project root, native spelling
	home string
	cfg  string // the user config dir
}

var (
	posixEnv = pureEnv{goos: "linux", root: "/proj", home: "/home/u", cfg: "/home/u/.claude"}
	winEnv   = pureEnv{goos: "windows", root: `C:\Proj`, home: `C:\Users\U`, cfg: `C:\Users\U\.claude`}
)

// layer is one settings document and the directory its `/path` rules are measured from.
type layer struct {
	json string
	dirs []string
}

// build compiles layers into a RuleSet for e.
func (e pureEnv) build(t *testing.T, layers ...layer) *RuleSet {
	t.Helper()
	p := New(Options{ProjectRoot: e.root, Home: e.home, goos: e.goos})
	var lists []ruleList
	for i, l := range layers {
		dirs := l.dirs
		if dirs == nil {
			dirs = []string{e.root}
		}
		got, err := parseSettings([]byte(l.json), &source{id: "layer" + string(rune('A'+i)), settingsDirs: dirs},
			e.goos, p.fold)
		require.NoError(t, err, "layer %d", i)
		lists = append(lists, got...)
	}
	return p.newRuleSet(lists)
}

// deny wraps rules into one project settings document's deny list.
func deny(rules ...string) layer { return layer{json: permsJSON("deny", rules)} }

// ask wraps rules into one project settings document's ask list.
func ask(rules ...string) layer { return layer{json: permsJSON("ask", rules)} }

func permsJSON(key string, rules []string) string {
	quoted := make([]string, len(rules))
	for i, r := range rules {
		quoted[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(r, `\`, `\\`), `"`, `\"`) + `"`
	}
	return `{"permissions":{"` + key + `":[` + strings.Join(quoted, ",") + `]}}`
}

// requireEffect evaluates each path and compares.
func requireEffect(t *testing.T, rs *RuleSet, want Effect, abs ...string) {
	t.Helper()
	for _, a := range abs {
		require.Equal(t, want, rs.Evaluate(a).Effect, "%s", a)
	}
}

func TestPatternForms_POSIX(t *testing.T) {
	e := posixEnv
	for _, tc := range []struct {
		name    string
		layer   layer
		denied  []string
		allowed []string
	}{
		{
			"bare filename matches at any depth", deny("Read(.env)"),
			[]string{"/proj/.env", "/proj/a/b/.env"},
			[]string{"/proj/.envrc", "/other/.env", "/.env"},
		},
		{
			"./ is the working directory", deny("Read(./.env)"),
			[]string{"/proj/.env"},
			[]string{"/proj/.env.example"},
		},
		{
			"glob in a bare name", deny("Read(*.pem)"),
			[]string{"/proj/key.pem", "/proj/x/y/key.pem"},
			[]string{"/proj/key.pem.txt"},
		},
		{
			"question mark and class", deny("Read(key[0-9]?.txt)"),
			[]string{"/proj/key1a.txt", "/proj/d/key9z.txt"},
			[]string{"/proj/keyXa.txt", "/proj/key1.txt"},
		},
		{
			"// is the filesystem root", deny("Read(//etc/**)"),
			[]string{"/etc/passwd", "/etc/ssh/sshd_config"},
			[]string{"/proj/etc/passwd", "/etcx/a"},
		},
		{
			"//**/name anywhere on the filesystem", deny("Read(//**/.env)"),
			[]string{"/proj/.env", "/srv/app/.env"},
			[]string{"/proj/env"},
		},
		{
			"~/ is the home directory", deny("Read(~/.ssh/**)"),
			[]string{"/home/u/.ssh/id_rsa"},
			[]string{"/proj/.ssh/id_rsa", "/home/u/ssh/x"},
		},
		{
			"~ alone is the whole home directory", deny("Read(~)"),
			[]string{"/home/u/a", "/home/u"},
			[]string{"/proj/a"},
		},
		{
			"/ in project settings is the project", deny("Read(/src/**)"),
			[]string{"/proj/src/app.ts"},
			[]string{"/proj/vendor/pkg/src/lib.js", "/src/app.ts"},
		},
		{
			"single directory segment matches at any depth for deny", deny("Read(src/**)"),
			[]string{"/proj/src/app.ts", "/proj/vendor/pkg/src/lib.js"},
			[]string{"/proj/srcx/a", "/proj/src"},
		},
		{
			"multi-segment relative pattern is anchored", deny("Read(src/components/**)"),
			[]string{"/proj/src/components/a.tsx"},
			[]string{"/proj/x/src/components/a.tsx"},
		},
		{
			"**/dir/** anywhere under the project", deny("Read(**/src/**)"),
			[]string{"/proj/src/a", "/proj/x/src/a"},
			[]string{"/src/a"},
		},
		{
			"trailing slash names a directory and blocks its contents", deny("Read(secrets/)"),
			[]string{"/proj/secrets/a", "/proj/x/secrets/b/c"},
			[]string{"/proj/secretsx"},
		},
		{
			"a plain directory name blocks its contents", deny("Read(//var/secret)"),
			[]string{"/var/secret", "/var/secret/key"},
			[]string{"/var/secretive"},
		},
		{
			"leading .. climbs above the project", deny("Read(../shared/token)"),
			[]string{"/shared/token"},
			[]string{"/proj/shared/token"},
		},
		{
			"an unusable glob still guards its exact path", deny("Read(./[)"),
			[]string{"/proj/["},
			[]string{"/proj/a"},
		},
		{
			"parentheses in a path are literal", deny("Read(./Finance (2024)/**)"),
			[]string{"/proj/Finance (2024)/q1.xlsx"},
			[]string{"/proj/Finance/q1.xlsx"},
		},
		{
			"matching is case-sensitive on Linux", deny("Read(./Secret.txt)"),
			[]string{"/proj/Secret.txt"},
			[]string{"/proj/secret.txt"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := e.build(t, tc.layer)
			requireEffect(t, rs, Deny, tc.denied...)
			requireEffect(t, rs, Allow, tc.allowed...)
		})
	}
}

func TestSettingsRelativeAnchorFollowsTheSource(t *testing.T) {
	e := posixEnv
	user := layer{json: permsJSON("deny", []string{"Read(/secrets/**)"}), dirs: []string{e.cfg}}
	rs := e.build(t, user)
	requireEffect(t, rs, Deny, "/home/u/.claude/secrets/token")
	requireEffect(t, rs, Allow, "/proj/secrets/token")
}

func TestToolLevelAndToolGlobRules(t *testing.T) {
	e := posixEnv
	for _, rule := range []string{"Read", "Read()", "*", "R*", "Rea?"} {
		t.Run(rule, func(t *testing.T) {
			requireEffect(t, e.build(t, deny(rule)), Deny, "/proj/anything", "/elsewhere/x")
		})
	}
	for _, rule := range []string{"Bash", "Bash(*)", "Edit(./x)", "mcp__*", "Write(./x)", "Glob(./x)", "ReadX"} {
		t.Run("irrelevant "+rule, func(t *testing.T) {
			require.True(t, e.build(t, deny(rule)).Empty(), "%s must not govern Read", rule)
		})
	}
}

func TestParameterRulesGovernNoArchivedRead(t *testing.T) {
	e := posixEnv
	// file_path is the host's primary content field: it ignores a parameter rule on it. The other
	// Read parameters match only a call that sets them, which a retrieval never is.
	for _, rule := range []string{"Read(file_path:/proj/x)", "Read(offset:*)", "Read( limit : 5)", "Read(pages:1-3)"} {
		require.True(t, e.build(t, deny(rule)).Empty(), "%s", rule)
	}
	// A colon in a path that is not a Read parameter is a path.
	requireEffect(t, e.build(t, deny("Read(./a:b)")), Deny, "/proj/a:b")
}

func TestAskRulesAndPrecedence(t *testing.T) {
	e := posixEnv
	rs := e.build(t, ask("Read(*.env)"))
	requireEffect(t, rs, Ask, "/proj/a.env")
	requireEffect(t, rs, Allow, "/proj/a.txt")

	both := e.build(t, ask("Read(*.env)"), deny("Read(prod.env)"))
	requireEffect(t, both, Deny, "/proj/prod.env")
	requireEffect(t, both, Ask, "/proj/dev.env")
}

func TestNegationCarvesOnlyWhatTheHostCarves(t *testing.T) {
	e := posixEnv
	t.Run("carves the earlier relative rule in the same list", func(t *testing.T) {
		rs := e.build(t, deny("Read(*.env)", "Read(!sample.env)"))
		requireEffect(t, rs, Allow, "/proj/sample.env", "/proj/x/sample.env")
		requireEffect(t, rs, Deny, "/proj/prod.env")
	})
	t.Run("a carve-out listed first carves nothing", func(t *testing.T) {
		requireEffect(t, e.build(t, deny("Read(!sample.env)", "Read(*.env)")), Deny, "/proj/sample.env")
	})
	t.Run("never across sources", func(t *testing.T) {
		requireEffect(t, e.build(t, deny("Read(*.env)"), deny("Read(!sample.env)")), Deny, "/proj/sample.env")
	})
	t.Run("never across deny and ask", func(t *testing.T) {
		rs := e.build(t, layer{json: `{"permissions":{"deny":["Read(*.env)"],"ask":["Read(!sample.env)"]}}`})
		requireEffect(t, rs, Deny, "/proj/sample.env")
	})
	t.Run("cannot reopen inside a blocked directory", func(t *testing.T) {
		rs := e.build(t, deny("Read(secrets/**)", "Read(!secrets/public/**)"))
		requireEffect(t, rs, Deny, "/proj/secrets/public/readme.md")
	})
	t.Run("reopens a file the directory rule only matched by content", func(t *testing.T) {
		rs := e.build(t, deny("Read(/src/**)", "Read(src/**)", "Read(!src/keep.txt)"))
		// /src/** is anchored and cannot be carved, so it still refuses.
		requireEffect(t, rs, Deny, "/proj/src/keep.txt")
		rs = e.build(t, deny("Read(src/**)", "Read(!src/keep.txt)"))
		requireEffect(t, rs, Allow, "/proj/src/keep.txt")
		requireEffect(t, rs, Deny, "/proj/src/other.txt", "/proj/lib/src/keep.txt")
	})
	t.Run("cannot reach an anchored rule", func(t *testing.T) {
		rs := e.build(t, deny("Read(~/notes/**)", "Read(!~/notes/public/**)"))
		requireEffect(t, rs, Deny, "/home/u/notes/public/a.md")
	})
}

func TestMalformedEntries(t *testing.T) {
	e := posixEnv
	p := New(Options{ProjectRoot: e.root, Home: e.home, goos: e.goos})
	src := &source{id: "bad", settingsDirs: []string{e.root}}
	for name, doc := range map[string]string{
		"invalid JSON":              `{"permissions":`,
		"empty file":                ``,
		"top level not an object":   `["Read"]`,
		"permissions not object":    `{"permissions":["Read(./x)"]}`,
		"permissions null":          `{"permissions":null}`,
		"deny not an array":         `{"permissions":{"deny":"Read(./x)"}}`,
		"ask not an array":          `{"permissions":{"ask":{"a":1}}}`,
		"non-string entry":          `{"permissions":{"deny":[42]}}`,
		"unbalanced Read rule":      `{"permissions":{"deny":["Read(./secret"]}}`,
		"unbalanced tool-glob rule": `{"permissions":{"ask":["*(./secret"]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseSettings([]byte(doc), src, e.goos, p.fold)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrUnavailable), "%v", err)
		})
	}
	for name, doc := range map[string]string{
		"unbalanced rule for another tool": `{"permissions":{"deny":["Bash(rm -rf"]}}`,
		"no permissions at all":            `{"model":"x"}`,
		"unknown permission keys":          `{"permissions":{"allow":[1,2],"defaultMode":"plan"}}`,
		"byte order mark":                  "\ufeff" + `{"permissions":{"deny":["Read(./x)"]}}`,
		"empty rule string":                `{"permissions":{"deny":[""," "]}}`,
	} {
		t.Run("tolerated: "+name, func(t *testing.T) {
			_, err := parseSettings([]byte(doc), src, e.goos, p.fold)
			require.NoError(t, err)
		})
	}
}

func TestWindowsPathsAreComparedInPOSIXFormWithoutCase(t *testing.T) {
	e := winEnv
	for _, tc := range []struct {
		name    string
		layer   layer
		denied  []string
		allowed []string
	}{
		{
			"//c/ is the drive root", deny("Read(//c/**/.env)"),
			[]string{`C:\Proj\.env`, `c:\other\deep\.env`},
			[]string{`D:\Proj\.env`},
		},
		{
			"//**/ spans every drive", deny("Read(//**/.env)"),
			[]string{`C:\Proj\.env`, `D:\x\.env`},
			[]string{`C:\Proj\env`},
		},
		{
			"relative rules ignore case", deny("Read(./SECRET.txt)"),
			[]string{`C:\Proj\secret.TXT`, `c:\proj\Secret.txt`},
			[]string{`C:\Proj\secret.md`},
		},
		{
			"home rules ignore case", deny("Read(~/Documents/*.pdf)"),
			[]string{`C:\users\u\documents\A.PDF`},
			[]string{`C:\Proj\Documents\a.pdf`},
		},
		{
			"a drive-letter rule is also read as the path it names", deny(`Read(C:\Proj\config\prod.json)`),
			[]string{`C:\Proj\config\prod.json`},
			[]string{`C:\Proj\config\dev.json`},
		},
		{
			"a backslash rule is also read with slashes", deny(`Read(.\secrets\**)`),
			[]string{`C:\Proj\secrets\a.key`},
			[]string{`C:\Proj\public\a.key`},
		},
		{
			"extended-length paths", deny("Read(./.env)"),
			[]string{`\\?\C:\Proj\.env`},
			nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := e.build(t, tc.layer)
			requireEffect(t, rs, Deny, tc.denied...)
			requireEffect(t, rs, Allow, tc.allowed...)
		})
	}
}

func TestMacOSComparesWithoutCase(t *testing.T) {
	e := pureEnv{goos: "darwin", root: "/Users/a/proj", home: "/Users/a", cfg: "/Users/a/.claude"}
	requireEffect(t, e.build(t, deny("Read(./Secret.txt)")), Deny, "/users/A/PROJ/secret.TXT")
}

func TestMatchSegmentsStaysPolynomialOnHostilePatterns(t *testing.T) {
	pat := strings.Split(strings.Repeat("**/a*a*a*a*/", 40)+"b", "/")
	segs := strings.Split(strings.Repeat("aaaaaaaaaaaaaaaa/", 200)+"c", "/")
	require.False(t, matchSegments(pat, segs))
}

func TestEvaluateOnAnEmptySetIsAllow(t *testing.T) {
	var rs *RuleSet
	require.True(t, rs.Empty())
	require.Equal(t, Allow, rs.Evaluate("/proj/x").Effect)
	require.Equal(t, Allow, posixEnv.build(t, deny("Read(./x)")).Evaluate("").Effect)
	require.Equal(t, "deny", Deny.String())
	require.Equal(t, "ask", Ask.String())
	require.Equal(t, "allow", Allow.String())
}

func TestLiteralPatternsCleanTheirDotDots(t *testing.T) {
	e := posixEnv
	rs := e.build(t, deny("Read(./a/*/../[bad)"))
	// `..` after a glob segment is not a usable gitignore pattern: the host still guards the exact
	// path it spells, and `[bad` makes it literal either way.
	requireEffect(t, rs, Deny, "/proj/a/*/../[bad", "/proj/a/[bad")
	requireEffect(t, rs, Allow, "/proj/a/b/[bad", "/proj/a")
	require.Equal(t, []string{"b"}, cleanLiteral([]string{"..", "a", "..", "b"}))
}
