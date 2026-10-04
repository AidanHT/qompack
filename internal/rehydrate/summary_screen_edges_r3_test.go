package rehydrate

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/skills"
)

// The D61 screen's edges as the w19c round-3 review found them (ADR 0011 §23 items 6 to 10): a
// PowerShell `$Env:` drive in any case but lower, bash ANSI-C quoting and undecoded escapes before a
// rule's literal, an outside path after a typographic quote or a Unicode space, a cut inside an
// encoded name, a quote then an escaped space after the root, a skill file the host refuses, a rule
// over the project through an alias of its root, a rooted word that is no path noted as withheld, an
// earlier spelling of the root losing its quotes, a caret-escaped separator, a glob in a multi-valued
// JSON preview, a nested shell that changes to the root, a drive-less glob, and an operation's path
// in a drop reason.

// uat12Rules are UAT-12's three Read deny rules.
var uat12Rules = []string{"./private/deny.txt", "./.env", "./secrets/**"}

// TestBuild_AnEnvDriveSpelledInAnyCaseIsWithheld: PowerShell's variable names and its Env: drive
// ignore case, and `$Env:` is the spelling its own documentation uses, but the screen read the drive
// only as `$env:`, so a path from a home directory's variable spelled `$Env:` or `$ENV:` was shown
// (D61(2)(c)).
func TestBuild_AnEnvDriveSpelledInAnyCaseIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{"echo $Env:PATH", "go env GOPATH", `$Env:GOFLAGS = "-count=1"`},
		[]string{
			`Get-Content $Env:USERPROFILE\.ssh\id_rsa`,
			`Get-Content $ENV:APPDATA\Claude\settings.json`,
			`$p = "$Env:USERPROFILE\.ssh\id_rsa"; Get-Content $p`,
			`Get-Content -Path $Env:USERPROFILE/.ssh/id_rsa`,
			`type $Env:HOMEDRIVE$Env:HOMEPATH\notes\x.txt`,
			`{"script":"Get-Content $Env:USERPROFILE\\.ssh\\id_rsa"}`,
			`gc ${Env:LOCALAPPDATA}\vault\x.txt`,
		},
		[]string{"id_rsa", "settings.json", "vault", "notes"})
}

// TestBuild_AnANSICQuoteOrAnUndecodedEscapeNeverHidesARuleLiteral: round 2 matched a rule's literal,
// and a withheld path's name, only where a name starts, and a spelling that leaves a name character
// glued before the literal once its quotes are removed hid it: bash's ANSI-C and locale quoting
// (`$'deny.txt'` reads `$deny.txt`), a backslash before a quote that cmd.exe and PowerShell keep as
// a separator (`"private\"deny.txt` is private\deny.txt there), and an escape the screen did not
// decode (`\u002f`, `\x2f`, `\057`, a twice percent-encoded `%252F`). Each is withheld; the same
// spellings naming allowed files are shown.
func TestBuild_AnANSICQuoteOrAnUndecodedEscapeNeverHidesARuleLiteral(t *testing.T) {
	root := previewRoot("proj")
	spell := func(dir, name, env, sec string) []string {
		return []string{
			`cat ` + dir + `/$'` + name + `'`,
			`cat "` + dir + `/"$'` + name + `'`,
			`cat ` + dir + `/$"` + name + `"`,
			`cat ` + dir + `/$''` + name,
			`cat $'` + env + `'`,
			`cat $"` + env + `"`,
			`tar czf x.tgz $'` + sec + `'`,
			`curl -d '{"name":"` + dir + `\u002f` + name + `"}' http://localhost:8080/read`,
			`curl -d '{"name":"x\u002f` + env + `"}' http://localhost:8080/read`,
			`curl http://h/x?f=` + dir + `%252F` + name,
			`type "` + dir + `\"` + name,
			`type "` + dir + `\"` + name + `" & echo ok`,
			`Get-Content '` + dir + `\'` + name,
			`Get-Content "` + dir + `\"` + name,
			`cat ` + dir + `$'\x2f'` + name,
			`cat ` + dir + `$'\057'` + name,
		}
	}
	// Two builds, so that section 6's share of the budget holds every pointer of each.
	allowed, denied := spell("src", "main.go", "x.txt", "assets"), spell("private", "deny.txt", ".env", "secrets")
	for _, half := range [][2]int{{0, 8}, {8, len(denied)}} {
		t.Run("rules", func(t *testing.T) {
			requireScreened(t, root, hostRules(root, uat12Rules...), nil,
				append(allowed[half[0]:half[1]:half[1]], `echo $'hello world'`, `git log --format=$'%h %s'`),
				denied[half[0]:half[1]],
				[]string{"deny.txt", ".env", "secrets"})
		})
	}
	t.Run("an apostrophe ANSI-C quoted", func(t *testing.T) {
		requireScreened(t, root, hostRules(root, "./private/John's notes.txt"), nil,
			[]string{`cat docs/$'John\'s notes.md'`},
			[]string{`cat private/$'John\'s notes.txt'`},
			[]string{"notes.txt"})
	})
	t.Run("a recorded withheld file", func(t *testing.T) {
		deny := denyFiles(root, "private/deny.txt")
		noLiterals := func() HostRules { h := deny(); h.Patterns = nil; return h }
		requireScreened(t, root, noLiterals, []string{"private/deny.txt"},
			[]string{`cat src/$'main.go'`},
			[]string{`cat private/$'deny.txt'`, `type "private\"deny.txt`, `curl http://h/x?f=private%252Fdeny.txt`},
			[]string{"deny.txt"})
	})
}

// TestBuild_AnOutsidePathAfterATypographicQuoteOrAUnicodeSpaceIsWithheld: PowerShell reads “ ” ‘ ’
// as quotes and a no-break, em, ideographic or line-separator space as whitespace, but an absolute or
// home path started only after ASCII, so one after such a character was shown (D61(2)(c)); so was a
// drive path after `!` or `*`, which no name continues into.
func TestBuild_AnOutsidePathAfterATypographicQuoteOrAUnicodeSpaceIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root), nil,
		[]string{"echo “done”", "echo «ok»", "git commit -m ‘fix typo’", "echo\u00a0ok"},
		[]string{
			"Get-Content “D:\\secret.txt”",
			"Get-Content ‘D:\\secret.txt’",
			"Get-Content “/etc/passwd”",
			"Get-Content ‘~/.ssh/id_rsa’",
			"Get-Content\u00a0/etc/passwd",
			"type\u3000D:\\secret.txt",
			"cat\u2003/etc/passwd",
			"cat\u2028/etc/passwd",
			"cat «/etc/passwd»",
			`cmd /v /c "type !D:\secret.txt"`,
			`type *D:\secret.txt`,
		},
		[]string{"secret.txt", "passwd", "id_rsa"})
}

// cutAfter is the store's preview of a text that starts with head and is cut right after tail: head,
// padding, then tail and the store's `…` (storeCut).
func cutAfter(t *testing.T, head, tail string) string {
	t.Helper()
	pad := previewWidth - len(previewEllipsis) - len(head) - len(tail)
	require.Positive(t, pad, "fixture: %q and %q fit before the cut", head, tail)
	s := storeCut(head + strings.Repeat("a", pad) + tail + "zzzzzzzzzzzz")
	require.Equal(t, head+strings.Repeat("a", pad)+tail+previewEllipsis, s, "fixture: the store cuts after the tail")
	return s
}

// TestBuild_ACutInsideAnEncodedOrQuotedNameNeverShowsItsPrefix: a cut summary was withheld only when
// the summary as written ended in a prefix of a withheld name at an ASCII boundary, so a cut inside a
// stretch that only a decoded form spells (percent-encoded, a JSON string's escape before the name)
// or after a typographic quote, a short option or bash's `$'` showed the prefix (D61(2), item (8)).
func TestBuild_ACutInsideAnEncodedOrQuotedNameNeverShowsItsPrefix(t *testing.T) {
	root := previewRoot("proj")
	t.Run("rules", func(t *testing.T) {
		requireScreened(t, root, hostRules(root, uat12Rules...), nil,
			[]string{
				cutAfter(t, "curl ", " https://x.example/?f=src%2Fmai"),
				cutAfter(t, `{"script":"`, ` x\nmai`),
				cutAfter(t, "cat ", " cat “mai"),
				cutAfter(t, "tar ", " tar -C.ve"),
			},
			[]string{
				cutAfter(t, "curl ", " https://x.example/?f=private%2Fden"),
				cutAfter(t, `{"script":"`, ` x\nden`),
				cutAfter(t, `{"script":"`, ` x\tsecr`),
				cutAfter(t, `{"list":"`, ` README.md\n.en`),
				cutAfter(t, "cat ", " cat “den"),
				cutAfter(t, "tar ", " tar -C.en"),
				cutAfter(t, "cat ", " cat $'den"),
			},
			nil)
	})
	t.Run("a file URL in the project", func(t *testing.T) {
		url := "file:///" + strings.TrimPrefix(filepath.ToSlash(root), "/")
		requireScreened(t, root, hostRules(root, "./private/my secret.txt"), nil,
			[]string{cutAfter(t, "x ", " "+url+"/private/my%20not")},
			[]string{cutAfter(t, "x ", " "+url+"/private/my%20sec")},
			nil)
	})
}

// TestBuild_AQuoteThenAnEscapedSpaceAfterTheRootNamesASibling: a quote closing the root's spelling
// followed by an escaped space (`\ ` in a POSIX shell, a backtick-space in PowerShell, a caret-space
// in cmd.exe) joins one shell word, `<root> old/x.txt`, a sibling of the root, and it was shown.
func TestBuild_AQuoteThenAnEscapedSpaceAfterTheRootNamesASibling(t *testing.T) {
	root := previewRoot("proj")
	fwd := filepath.ToSlash(root)
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{`cat "` + fwd + `"/x.txt`, `cd "` + root + `" && make`, `cat '` + fwd + `' old/x.txt`},
		[]string{
			`cat "` + fwd + `"\ old/x.txt`,
			`cat '` + fwd + `'\ old/x.txt`,
			"Get-Content \"" + root + "\"` old" + string(filepath.Separator) + "x.txt",
			`type "` + root + `"^ old` + string(filepath.Separator) + `x.txt`,
		},
		nil)
}

// TestBuild_ASkillTheHostRefusesIsNeverIndexed: item 6b never consulted the build's judge, so a
// SKILL.md the host refuses had its name and description injected when it was in the compact
// index, and was named with its restore call in section 7 and dropped() when it was not; under
// unavailable rules every skill file's path was named. A skill file the build withholds is neither
// indexed nor named, as item 6a's rule files are; an allowed skill is still indexed.
func TestBuild_ASkillTheHostRefusesIsNeverIndexed(t *testing.T) {
	root := previewRoot("proj")
	secret := skills.Entry{Name: "zz-secret-skill", Description: "ZZ-SKILL-DESCRIPTION", Source: ".claude/skills/secret/SKILL.md"}
	other := skills.Entry{Name: "zz-unlisted-skill", Description: "ZZ-UNLISTED", Source: ".claude/skills/zz/SKILL.md"}
	allowed := skills.Entry{Name: "go-style", Description: "Go conventions for this repo", Source: ".claude/skills/go/SKILL.md"}
	for _, tc := range []struct {
		name  string
		hp    HostPaths
		idx   *fakeIndexer
		leaks []string
		shown bool
	}{
		{
			"in the compact index", hostRules(root, "./.claude/skills/secret/**"),
			&fakeIndexer{all: []skills.Entry{allowed, secret}, kept: []skills.Entry{allowed, secret}},
			[]string{"zz-secret-skill", "ZZ-SKILL-DESCRIPTION", ".claude/skills/secret"},
			true,
		},
		{
			"outside the compact index", hostRules(root, "./.claude/skills/secret/**"),
			&fakeIndexer{all: []skills.Entry{allowed, secret}, kept: []skills.Entry{allowed}},
			[]string{"zz-secret-skill", "ZZ-SKILL-DESCRIPTION", ".claude/skills/secret"},
			true,
		},
		{
			"unavailable rules", func() HostRules { return HostRules{} },
			&fakeIndexer{all: []skills.Entry{other}, kept: nil},
			[]string{"zz-unlisted-skill", ".claude/skills/zz"},
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp := ckUAT05()
			d := uat05Deps(t, cp)
			d.HostPaths = tc.hp
			d.Skills = tc.idx
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			requireNoLeak(t, res, tc.leaks)
			if tc.shown {
				require.Contains(t, res.Text, "- go-style: Go conventions for this repo", "an allowed skill is indexed")
			}
		})
	}
}

// TestBuild_ARuleOverTheProjectThroughAnAliasWithholdsEveryFreeText: a rule anchored outside the
// project is matched against the root as the request spells it (rootCover), but the host resolves
// links, junctions, 8.3 names and a rule written through a link, so a rule that covers the project
// through another spelling of its root refused every project path while free text naming project
// files by their relative paths was shown. When a rule anchored outside the project is in force, the
// build asks the host once whether it refuses a fresh name below the root, and when it does every
// free text is withheld, as under a rule with no literal. A rule anchored outside the project that
// covers nothing in it leaves free text to the screen, and no project-relative rule asks.
func TestBuild_ARuleOverTheProjectThroughAnAliasWithholdsEveryFreeText(t *testing.T) {
	root := previewRoot("proj")
	free := []string{"cat src/main.go", "go vet ./src/main.go", "git status", `{"query":"retry logic"}`}
	for _, tc := range []struct {
		name   string
		rules  []string
		covers bool
		probes int
	}{
		{"covered through an alias of the root", []string{"//real/work/**"}, true, 1},
		{"an anchored rule that covers nothing in the project", []string{"~/.ssh/**"}, false, 1},
		{"project-relative rules", uat12Rules, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := map[string]int{}
			hp := func() HostRules {
				return HostRules{Patterns: tc.rules, Refuses: func(p string) bool {
					asked[p]++
					// The host resolves the root's alias: every project path is under real/work.
					return tc.covers && (!filepath.IsAbs(p) || strings.HasPrefix(p, root))
				}}
			}
			if tc.covers {
				requireScreened(t, root, hp, nil, nil, free, nil)
			} else {
				requireScreened(t, root, hp, nil, free, nil, nil)
			}
			total := 0
			for p, n := range asked {
				total += n
				require.Equal(t, 1, n, "the host is asked about %q once", p)
				require.NotContains(t, p, "/", "the host is asked about one fresh name below the root, %q", p)
			}
			require.Equal(t, tc.probes, total,
				"the host is asked about a fresh name below the root only under a rule anchored outside the project: %v", asked)
		})
	}
}

// TestBuild_ARootedWordThatIsNoPathPoisonsNoFreeText: a one-word summary that looks rooted but names
// no path (a SlashCommand preview `/review`, a Grep route `/api/v1/users`, a backslash-led regular
// expression without regexLike's signatures `\.test\.ts`) is withheld as a path outside the project,
// as its shape says, but was also noted as a withheld path whose last segment withheld unrelated free
// text in the same build. A one-segment POSIX word is no path (D61(2)(c)), a backslash-led word is no
// POSIX path, and no Windows tool takes a drive-less path, so none of them is noted.
func TestBuild_ARootedWordThatIsNoPathPoisonsNoFreeText(t *testing.T) {
	root := previewRoot("proj")
	cases := []struct{ poison, victim string }{
		{"/review", "git log --grep=review"},
		{"/compact", `{"query":"compact summary"}`},
		{`\.test\.ts`, `find src -name "*.ts"`},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			struct{ poison, victim string }{"/api/v1/users", "go test ./internal/users/..."},
			struct{ poison, victim string }{"/api/v1/users", `{"query":"how are users created"}`})
	}
	for _, tc := range cases {
		t.Run(tc.poison+" beside "+tc.victim, func(t *testing.T) {
			requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
				[]string{tc.victim}, []string{tc.poison}, nil)
		})
	}
}

// TestBuild_AnEarlierSpellingOfTheRootKeepsItsQuotes: the quote state at a later spelling of the
// root was read from the held text, where every earlier spelling is one mark, so the quotes an
// earlier spelling opened or closed inside itself were lost, and a quoted sibling after it read as
// the root followed by a word and was shown.
func TestBuild_AnEarlierSpellingOfTheRootKeepsItsQuotes(t *testing.T) {
	sep := string(filepath.Separator)
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			p := root[:len(root)-len("proj")]
			requireScreened(t, root, hostRules(root, uat12Rules...), nil,
				[]string{
					`cd ` + p + `"proj" && make`,
					`type ` + p + `"proj` + sep + `a.txt" & cd ` + root + ` && dir`,
				},
				[]string{
					`cat ` + p + `"proj` + sep + `a.txt" ` + p + `"proj old` + sep + `x.txt"`,
					`type ` + p + `"proj` + sep + `a.txt" & type ` + p + `"proj old` + sep + `x.txt"`,
					`cd ` + p + `"proj" && cat "` + p + `proj old` + sep + `x.txt"`,
				},
				nil)
		})
	}
}

// TestBuild_ACaretEscapedSeparatorIsASeparator: a caret before a backslash was kept as a regular
// expression's anchor, but cmd.exe reads `^\` as `\`, so an absolute, home or `..` path spelled with
// caret-escaped separators was shown. Only a caret before `/` is kept (`^/api/`); a backslash-led
// regular expression after a caret is still one (`^\s*func\b`).
func TestBuild_ACaretEscapedSeparatorIsASeparator(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root), nil,
		[]string{`^\s*func\b`, `^/api/v1/users`, `rg "^\s*//" src/`, `echo a^&b`},
		[]string{
			`type ^\Users^\Quant^\.ssh^\id_rsa`,
			`type ..^\..^\outside.txt`,
			`type %USERPROFILE%^\.ssh^\id_rsa`,
			`cd ^\Users^\Quant && type .ssh\id_rsa`,
		},
		[]string{"id_rsa", "outside.txt"})
}

// TestBuild_AGlobInAMultiValuedJSONPreviewIsJudgedByWhatItSelects: a JSON preview with several
// path-named values is free text alone, so that it costs no host judgement, but it also lost the
// structured checks that cost none, and a glob in it that selects a withheld path was shown.
func TestBuild_AGlobInAMultiValuedJSONPreviewIsJudgedByWhatItSelects(t *testing.T) {
	root := previewRoot("proj")
	asked := map[string]int{}
	inner := hostRules(root, uat12Rules...)
	hp := func() HostRules {
		h := inner()
		refuses := h.Refuses
		h.Refuses = func(p string) bool { asked[p]++; return refuses(p) }
		return h
	}
	requireScreened(t, root, hp, []string{"private/deny.txt"},
		[]string{`{"paths":["src/*.go","docs/*.md"]}`, `{"file":"src/ma?n.go","path":"docs"}`},
		[]string{
			`{"paths":["private/*.txt","src/a.go"]}`,
			`{"paths":["private/d*","src/*.go"]}`,
			`{"file":"private/de?y.txt","path":"src"}`,
		},
		nil)
	require.Equal(t, map[string]int{"private/deny.txt": 1}, asked, "a multi-valued JSON preview asks the host nothing")
}

// TestBuild_ANestedShellThatChangesToTheRootIsShown: inside a quoted argument the root followed by a
// space was always read as a sibling, so a nested shell that changes to the project root (`bash -c
// "cd <root> && go test ./..."`) was withheld in every project. No sibling's name goes on with a
// shell operator (`&&`, `||`, `|`, `;`, `<`, `>`); a sibling whose name holds ` & ` (`proj & co`, a
// Windows folder name) or another word is still withheld.
func TestBuild_ANestedShellThatChangesToTheRootIsShown(t *testing.T) {
	sep := string(filepath.Separator)
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			fwd := filepath.ToSlash(root)
			requireScreened(t, root, hostRules(root, uat12Rules...), nil,
				[]string{
					`bash -c "cd ` + fwd + ` && go test ./..."`,
					`cmd /c "cd /d ` + root + ` && go test ./..."`,
					`sh -c "cd ` + fwd + ` || exit 1"`,
				},
				[]string{
					`bash -c "cat ` + fwd + ` old/x.txt"`,
					`cat "` + root + ` & co` + sep + `x.txt"`,
					`bash -c "cd ` + fwd + ` && cat private/deny.txt"`,
				},
				[]string{"deny.txt"})
		})
	}
}

// TestBuild_ADriveLessGlobOutsideTheProjectIsWithheld: a backslash-led word holding `*` or `?` was
// read as a regular expression, so a drive-less Windows path outside the project with a glob in it
// (`dir \Users\x\.ssh\*`, the common PowerShell and cmd.exe listing) was shown, as free text and as
// a one-word Glob preview (D61(2)(c)); so was one holding a `+` or a `$` a Windows name holds
// (`notes+old.txt`, `c++`, `$Recycle.Bin`). Neither a glob character nor such a `+` or `$` is a
// regular expression's signature; a class escape, `( ) { } | ^ [ ]`, `.+` or a closing `$` still is.
func TestBuild_ADriveLessGlobOutsideTheProjectIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{
			`\bConfigLoader\b`, `\w+Error\b`, `\s*func`, `\.Evaluate\(`, `\[DEBUG\]`, `\s+$`, `\d+\.\d+`, `\d?`,
			`grep -E "\s*TODO\b" -r src/`,
		},
		[]string{
			`dir \Users\someone\.ssh\*`,
			`Get-ChildItem \Users\someone\.aws\*`,
			`Get-ChildItem \Users\someone\.ssh\id_*`,
			`type \Users\someone\.aws\credential?`,
			`type \Users\someone\Documents\*.txt`,
			`del \Users\someone\AppData\Local\Temp\*.log`,
			`\Users\someone\.ssh\*`,
			`\Users\Quant\.aws\cred*`,
			`type \Users\someone\notes+old.txt`,
			`type \Users\someone\c++\notes.txt`,
			`type \$Recycle.Bin\x.txt`,
		},
		[]string{".ssh", ".aws", "credential", "Documents", "AppData", "notes", "Recycle"})
}

// TestBuild_AReasonNamingAPathAfterAnOperationIsRedacted: a drop reason's error chain names a path
// after its operation (`open <path>`, `CreateFile <path>`), and a gitdir outside the project whose
// name is the root's own last segment, a space and more (a main checkout `<root> main`) read as the
// root followed by a word, and a single-segment POSIX gitdir (`/repo.git`) as no path, so both were
// shown in section 7 and dropped() (D61(3)). An operation's path is judged whole; the error kind and
// the system's message stay.
func TestBuild_AReasonNamingAPathAfterAnOperationIsRedacted(t *testing.T) {
	root := previewRoot("proj")
	main := filepath.Join(root+" main", ".git", "worktrees", "wt", "index")
	cp := ckUAT05()
	cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
		checkpoint.DropEntry{
			Kind: "pointer_git_unavailable",
			Detail: "checkpoint: git index unsupported: reading index: CreateFile " + main +
				": Access is denied.",
		},
		checkpoint.DropEntry{
			Kind:   "pointer_git_unavailable",
			Detail: "checkpoint: git index unsupported: reading index: open /repo.git: permission denied",
		},
		checkpoint.DropEntry{
			Kind:   "pointer_git_unavailable",
			Detail: "checkpoint: git index unsupported: reading index: open " + filepath.Join(root, ".git", "index") + ": gone",
		})
	d := uat05Deps(t, cp)
	d.HostPaths = hostRules(root, uat12Rules...)
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireNoLeak(t, res, []string{"proj main", "worktrees", "/repo.git"})
	var kinds []string
	for _, e := range res.Dropped {
		if e.Kind == "pointer_git_unavailable" {
			require.Contains(t, e.Detail, "git index unsupported: reading index: ", "the error kind is kept: %+v", e)
			kinds = append(kinds, e.Detail)
		}
	}
	require.Len(t, kinds, 3, "every git drop is still reported: %v", res.Dropped)
	require.Contains(t, kinds, "checkpoint: git index unsupported: reading index: "+withheldDropID+": Access is denied.")
	require.Contains(t, kinds, "checkpoint: git index unsupported: reading index: "+withheldDropID+": permission denied")
	require.Contains(t, kinds, "checkpoint: git index unsupported: reading index: open "+filepath.Join(root, ".git", "index")+": gone",
		"a path inside the project is shown")
}
