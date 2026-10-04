package rehydrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rules"
)

// The D61 screen's edges, as the w19c round-1 review found them (ADR 0011 §23 items 6 to 9): spellings
// the screen did not read, and summaries it withheld although they name no withheld path.

// storeCut is s as the store's preview cuts it (store.previewString): at most previewWidth bytes,
// cut on a rune boundary with `…`.
func storeCut(s string) string {
	if len(s) <= previewWidth {
		return s
	}
	cut := previewWidth - len(previewEllipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + previewEllipsis
}

// cutJSONArg is the store's cut preview of a call whose canonical JSON ends in the argument name with
// value, padded through an earlier argument so that exactly keep bytes of the JSON-escaped value
// survive the cut.
func cutJSONArg(t *testing.T, name, value string, keep int) string {
	t.Helper()
	esc := strings.ReplaceAll(value, `\`, `\\`)
	require.Less(t, keep, len(esc), "fixture: the cut falls inside the value")
	head, mid := `{"cell_id":"c1","new_source":"`, `","`+name+`":"`
	pad := previewWidth - len(previewEllipsis) - len(head) - len(mid) - keep
	require.Positive(t, pad, "fixture: the value starts inside the preview")
	full := head + strings.Repeat("x", pad) + mid + esc + `"}`
	s := storeCut(full)
	require.Equal(t, head+strings.Repeat("x", pad)+mid+esc[:keep]+previewEllipsis, s, "fixture: the store's cut")
	return s
}

// TestBuild_AnAbsolutePathGluedToAFlagIsWithheld: a short option takes its value glued to it
// (`-I/usr/include`, `-oD:\stash`), and the screen started a path only after whitespace or a
// delimiter, so an absolute path outside the project glued to a flag was shown (D61(2)(c)). The
// project's own root glued to a flag is the project, and is shown.
func TestBuild_AnAbsolutePathGluedToAFlagIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		// `cmd /c dir` is over-withheld by D63: `/c` reads as a single-segment absolute path.
		[]string{"go test ./...", "gcc -I" + filepath.Join(root, "include") + " -c x.c", "ls -la src/"},
		[]string{
			"cmd /c dir",
			"git -C/home/u/other status",
			"gcc -I/opt/homebrew/include -L/opt/homebrew/lib x.c",
			"go build -o/usr/local/bin/x .",
			"ssh -i/home/u/keys/id_rsa host",
			"tar -xf/home/u/backup.tar",
			"grep -f/etc/passwd x",
			"docker run -v/home/u/data:/data img",
			`7z x -oD:\stash a.zip`,
			"tar -C/etc/ssl -xf a.tar",
		},
		[]string{"/home/u", "homebrew", "/usr/local", "id_rsa", "passwd", `D:\stash`, "/etc/ssl"})
}

// TestBuild_AnEnvironmentVariablePathIsWithheld: PowerShell spells an environment variable
// `$env:NAME` (or `${env:NAME}`), cmd.exe chains `%A%%B%` and expands `!NAME!` late, and a home
// directory variable alone (`cd $HOME && …`) is the home directory as `~` is. The screen knew
// `$NAME/`, `${NAME}/` and `%NAME%\` only, so each of these, a path outside the project, was shown.
// A variable that names no directory is a name assembled at run time and stays a recorded limit.
func TestBuild_AnEnvironmentVariablePathIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		// Criterion change (D63): any `$` at a token start is unsafe (a variable cannot be proven
		// inert), so `echo $PATH`, `go test $PKG` and `echo $HOMEPAGE is set` are over-withheld.
		[]string{"npm run build && echo done"},
		[]string{
			"echo $PATH", "go test $PKG", "echo $HOMEPAGE is set",
			`Get-Content $env:USERPROFILE\.aws\credentials`,
			`Get-Content "$env:USERPROFILE\.ssh\id_rsa"`,
			`type $env:APPDATA\Claude\settings.json`,
			`gc ${env:LOCALAPPDATA}\vault\x.txt`,
			`type %HOMEDRIVE%%HOMEPATH%\notes\x.txt`,
			`type !USERPROFILE!\notes\x.txt`,
			"cd $HOME && cat .ssh/id_rsa",
			`cd %USERPROFILE% && type .aws\credentials`,
			`Set-Location $env:USERPROFILE; gc .ssh\id_rsa`,
			`cd "$HOME" && cat .ssh/id_rsa`,
			`$env:APPDATA\x.txt`,
			`!USERPROFILE!\x.txt`,
		},
		[]string{"credentials", "id_rsa", "settings.json", "vault", "notes"})
}

// TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened: a path-named JSON argument whose value
// is not exactly one path (several paths, a line locator, a glob, a command) went to the host as one
// whole path, which refuses nothing, and was never screened, so it showed a denied name verbatim;
// the same value as a lone Glob argument was withheld. Only a rooted plain path is judged as the one
// file it names alone, and a locator (`#`, `@`) is not part of a plain path.
func TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened(t *testing.T) {
	root := previewRoot("proj")
	jsonMain := strings.ReplaceAll(filepath.Join(root, "src", "main.go"), `\`, `\\`)
	sep := string(filepath.Separator)
	requireScreened(t, root, hostRules(root, "./private/deny.txt", "./.env"), nil,
		[]string{
			`{"path":"src/main.go"}`,
			`{"paths":["src/a.go","src/b.go"]}`,
			`{"file_path":"` + jsonMain + `"}`,
			`{"relative_path":"src/main.go#L4"}`,
			`{"path":"src/a.go,src/b.go"}`,
		},
		[]string{
			`{"paths":"private/deny.txt src/main.go"}`,
			`{"file":"private/deny.txt#L10"}`,
			`{"file":"private/deny.txt:10"}`,
			`{"path":"private/deny.txt,src/a.go"}`,
			`{"files":"src/a.go;private/deny.txt"}`,
			`{"path":"cat private/deny.txt"}`,
			`{"path":".env.local .env"}`,
			`{"paths":["**/deny.txt"]}`,
			`{"relative_path":"private/deny.txt#L4"}`,
			root + sep + "private" + sep + "deny.txt#L4",
		},
		[]string{"deny.txt"})
}

// TestBuild_ARootedPathWithASpaceIsJudgedByTheHost: a Read, Write or Edit preview whose path has a
// space below the project root is several words, so it was screened as free text alone and the host
// never judged it, and a link or an 8.3 name in it went unresolved. A summary that starts at the root
// and goes on below it is judged whole by the host as well (one judgement), as a one-word one is.
func TestBuild_ARootedPathWithASpaceIsJudgedByTheHost(t *testing.T) {
	root := previewRoot("proj")
	viaLink := filepath.Join(root, "my docs", "x2.txt")
	asked := map[string]int{}
	hp := func() HostRules {
		return HostRules{Patterns: []string{"./private/**"}, Refuses: func(p string) bool {
			asked[p]++
			// `my docs` stands for a link into private/: only the host, which resolves it, knows.
			rel, err := filepath.Rel(root, p)
			return err == nil && (strings.HasPrefix(rel, "my docs") || strings.HasPrefix(rel, "private"))
		}}
	}
	requireScreened(t, root, hp, nil,
		[]string{filepath.Join(root, "my notes", "x.txt"), filepath.Join(root, "src", "main.go") + " TODO", root + " TODO"},
		[]string{viaLink},
		[]string{"x2.txt"})
	require.Equal(t, 1, asked[viaLink], "the host judges the rooted summary whole, once")
	require.Zero(t, asked[root+" TODO"], "the root followed by a word is not a path below it")
}

// TestBuild_ATruncatedSummaryNeverShowsTheCutPrefixOfAGlobRulesName: a glob rule's literal taken from
// a run with `*` or `?` before it (`.env` for `**/*.env`, `.pem` for `*.pem`) begins inside a name,
// so a cut inside `prod.env` or `server.pem` ends in its prefix with no boundary before it, and the
// cut summary was shown. Such a literal's prefix is matched at the end of the cut with no boundary.
func TestBuild_ATruncatedSummaryNeverShowsTheCutPrefixOfAGlobRulesName(t *testing.T) {
	root := previewRoot("proj")
	pad := strings.Repeat("a", 80)
	requireScreened(t, root, hostRules(root, "**/*.env", "*.pem", "./private/deny.txt"), nil,
		[]string{pad + " cat config/prod.ya…", pad + " cat docs/gu…"},
		[]string{pad + " cat config/prod.en…", pad + " openssl x509 -in certs/server.pe…", pad + " cat config/.en…"},
		nil)
}

// TestBuild_ACutCommandThatStartsLikeJSONIsScreened: any summary that started `{"` or `["` and ended
// in `…` was read as cut canonical JSON, and only its quoted strings were screened, so the rest of a
// command of that shape went unread. A cut preview is read as JSON only when everything outside its
// strings is JSON's own grammar.
func TestBuild_ACutCommandThatStartsLikeJSONIsScreened(t *testing.T) {
	root := previewRoot("proj")
	tail := " " + strings.Repeat("a", 120)
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{
			storeCut(`{"description":"run the tests","prompt":"go test ./... and report the failures` + tail + `"}`),
			storeCut(`{"limit":100,"query":"retry backoff","offset":123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890}`),
		},
		[]string{
			storeCut(`{"x":1} && cat private/deny.txt && echo` + tail),
			storeCut(`["$x" ] && cat /etc/passwd && echo` + tail),
			storeCut(`{"a":"b"}; cat C:\Users\x\notes.txt` + tail),
		},
		[]string{"deny.txt", "passwd", "notes.txt"})
}

// TestBuild_AnEscapedLineBreakNeverSplitsADeniedName: a line continuation inside a word (`\`, `^` or
// a backtick before a newline) joins the word, and the store's preview collapses the newline to a
// space, so the screen read `de\ ny` as `de ny`, which spells no literal. A POSIX shell reads `.\.`
// as `..`, which the screen read as two `.` segments. Under D63 a backslash that ends a token (an
// escaped space or a collapsed continuation) makes the summary unsafe, a caret or a backtick is
// outside the whitelist, and the path check reads a backslash both as a separator and removed, so
// each is withheld.
//
// Criterion change (D63): `cat docs/my\ notes.md`, an allowed file behind an escaped space, is
// over-withheld with them, since a token-final backslash is never proven inert.
func TestBuild_AnEscapedLineBreakNeverSplitsADeniedName(t *testing.T) {
	root := previewRoot("proj")
	slash := strings.ReplaceAll(root, `\`, "/")
	requireScreened(t, root, hostRules(root, "./private/deny.txt", "./.env"), nil,
		[]string{`cat ./src/a.go`, `type .\src\a.go`, `cat ` + slash + `/./src/a.go`},
		[]string{
			`cat private/de\ ny.txt`,
			`type private\de^ ny.txt`,
			`cat .e\ nv`,
			"Get-Content private/de` ny.txt",
			`cat .\./outside/x.txt`,
			`cat ` + slash + `/.\./x.txt`,
			`cat docs/my\ notes.md`,
		},
		nil)
}

// TestBuild_APathSelectorAfterAQuoteOrEqualsIsJudged: recall's path: selector was found only at the
// start of a text or after whitespace or `(`, so on a command line, where a quote or `=` stands
// before it, a selector that selects a withheld path was shown.
func TestBuild_APathSelectorAfterAQuoteOrEqualsIsJudged(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), []string{"private/deny.txt"},
		[]string{`qompack recall "path:src/main.go"`, "qompack recall --query=path:reports"},
		[]string{`qompack recall "path:deny"`, `qompack recall 'path:deny'`, "qompack recall --query=path:deny"},
		nil)
}

// TestBuild_ARuleAnchoredOutsideTheProjectWithholdsOnlyWhatItCovers: a rule anchored outside the
// project whose literal merely occurred in the root's path as a substring withheld every free text,
// though it refused no project path (`~/Documents/*.pdf` for a project under Documents, `~/.kube/config`
// beside `config-service`, `//etc/**` beside `fetcher`). Every free text is withheld only when the
// rule, segment by segment, may refuse the root or a path below it whose relative spelling need hold
// no literal; one that may refuse project paths through a later glob screens that glob's literal.
func TestBuild_ARuleAnchoredOutsideTheProjectWithholdsOnlyWhatItCovers(t *testing.T) {
	useful := []string{"git status", "go test ./...", "npm test", `{"k":5,"query":"retry backoff"}`}
	for _, tc := range []struct {
		name     string
		root     string
		rule     string
		shown    []string
		withheld []string
		// covers: the rule may refuse every project path, so every free text is withheld.
		covers bool
	}{
		{
			"pdf under Documents", previewRoot("Documents", "Programming", "Projects", "proj"), "~/Documents/*.pdf",
			[]string{"cat report.pdf"},
			[]string{"open ~/Documents/tax.pdf"},
			false,
		},
		{"kube config", previewRoot("s4", "config-service"), "~/.kube/config", nil, []string{"cat ~/.kube/config"}, false},
		{"etc", previewRoot("fetcher"), "//etc/**", nil, []string{"cat /etc/hosts"}, false},
		{
			"keys below Documents", previewRoot("Documents", "proj"), "~/Documents/**/*.key",
			[]string{"cat src/a.go"},
			[]string{"cat src/a.key", "cat ~/Documents/x.key"},
			false,
		},
		{"a directory the root is in", previewRoot("config", "proj"), "~/config/**", nil, useful, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shown := tc.shown
			if !tc.covers {
				shown = append(append([]string(nil), tc.shown...), useful...)
			}
			requireScreened(t, tc.root, hostRules(tc.root, tc.rule), nil, shown, tc.withheld, nil)
		})
	}
}

// TestBuild_ACutPathArgumentIsAFragmentNotAPath: the store cuts a canonical-JSON preview inside a
// path-named argument (NotebookEdit's notebook_path sorts after new_source), and the fragment was
// judged as a whole path: cut inside the root's own spelling it was outside the project, and a
// withheld fragment's cut basename (`Jo`, `to`) became a withheld name that withheld every free text
// holding those letters. A cut value is the start of a path: a prefix of the root is the project, a
// fragment is judged by its directory, and it is never a withheld name.
func TestBuild_ACutPathArgumentIsAFragmentNotAPath(t *testing.T) {
	t.Run("no rules", func(t *testing.T) {
		root := previewRoot("Johnathan", "proj")
		value := filepath.Join(root, "nb", "a.ipynb")
		esc := strings.ReplaceAll(value, `\`, `\\`)
		inRoot := cutJSONArg(t, "notebook_path", value, strings.Index(esc, "Johnathan")+2)
		inProject := cutJSONArg(t, "notebook_path", filepath.Join(root, "nb", "analysis.ipynb"),
			len(strings.ReplaceAll(filepath.Join(root, "nb", "analysis.ip"), `\`, `\\`)))
		requireScreened(t, root, hostRules(root), nil,
			[]string{inRoot, inProject, `{"k":5,"query":"join the major tables"}`, "go test ./internal/nb/..."},
			nil, nil)
	})
	t.Run("a directory rule", func(t *testing.T) {
		root := previewRoot("proj")
		value := filepath.Join(root, "secrets", "token.txt")
		esc := strings.ReplaceAll(value, `\`, `\\`)
		secret := cutJSONArg(t, "notebook_path", value, strings.Index(esc, "token")+2)
		requireScreened(t, root, hostRules(root, "./secrets/**"), nil,
			[]string{"go test ./internal/auto/...", "npm run tox", `{"k":5,"query":"photo upload"}`},
			[]string{secret},
			[]string{"secrets" + string(filepath.Separator) + "to", `secrets\\to`})
	})
}

// TestBuild_ACutInsideASecondSpellingOfTheRootIsWithheld: a command naming two of the project's
// absolute paths, cut by the store inside the second spelling of the root, ends in a drive fragment
// that is not the whole root; D63 no longer reassembles a cut root (deleted cutRoot machinery), so the
// fragment reads as a path outside the project and the summary is over-withheld (the first, complete
// root is still held together, but the cut fragment's `C:\q\John` token is a drive path). A cut inside
// a real sibling stays withheld.
func TestBuild_ACutInsideASecondSpellingOfTheRootIsWithheld(t *testing.T) {
	root := previewRoot("John Smith", "proj")
	cutAfter := func(head, rest string, keep int) string {
		pad := previewWidth - len(previewEllipsis) - len(head) - keep
		require.Positive(t, pad, "fixture: the head fits the preview")
		s := storeCut(head + strings.Repeat("a", pad) + " " + rest)
		require.True(t, strings.HasSuffix(s, rest[:keep-1]+previewEllipsis), "fixture: the cut falls inside %q: %q", rest, s)
		return s
	}
	head := "cat " + filepath.Join(root, "src", "main.go") + " "
	other := previewRoot("John Smith", "other", "secret.txt")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		nil,
		[]string{
			cutAfter(head, filepath.Join(root, "pkg", "x1.go"), len(root)-3),
			cutAfter(head, filepath.Join(root, "pkg", "x1.go"), len(root)/2),
			cutAfter(head, other, len(other)-6),
		},
		nil)
}

// TestBuild_AWithheldNameIsMatchedOnlyWhereANameStarts: the screen matched a withheld path's basename
// anywhere in a text, so one out-of-project Read of `out.txt` or `config` withheld `cat layout.txt` and
// `cat tsconfig.json` for the whole build. A withheld name glued to a preceding name character is a
// different file; it is matched where a name starts, and runs on (`my secret.txt.bak` is still
// withheld, as D61 accepts).
func TestBuild_AWithheldNameIsMatchedOnlyWhereANameStarts(t *testing.T) {
	root := previewRoot("proj")
	outside := []string{
		previewRoot("gomod", "cobra@v1.8.0", "README.md"),
		previewRoot("scratch", "out.txt"),
		previewRoot("home", ".ssh", "config"),
	}
	requireScreened(t, root, hostRules(root), nil,
		[]string{"cat layout.txt", "go test ./... | tee stdout.txt", "cat tsconfig.json", "go vet ./xconfig/..."},
		append(append([]string(nil), outside...), "cat out.txt", "git diff README.md", "cat ./config", "cat out.txt.bak"),
		nil)
}

// TestBuild_ACommentMarkerIsWithheld: under D63 a token led by `/` names an absolute path, so a
// comment marker (`// TODO`, `//nolint`, `//go:build`) is over-withheld along with the UNC shares and
// device prefixes it used to be confused with. The privacy guarantee is unchanged: no outside path is
// shown.
func TestBuild_ACommentMarkerIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		nil,
		[]string{
			`grep -rn "// TODO" internal/`, `rg -n "//nolint" internal/`, root + " //go:build",
			"// Deprecated: use X",
			"//fileserver/share/payroll.xlsx", `type \\fileserver\share\x.txt`, `type \\?\C:\x\y.txt`, "cat //etc/passwd",
		},
		[]string{"payroll", "fileserver", "passwd"})
}

// TestBuild_ADevicePathIsNotOutsideTheProject: `/dev/null` and the other standard devices name no
// file content, and `2>/dev/null` withheld ordinary commands as naming an absolute path outside the
// project. A fixed list of device paths is exempt; any other path under /dev is not. Under D63 the
// exemption is a whitelist of whole tokens (nullDevices: the null device's redirects and the three
// standard streams).
//
// Criterion change (D63): `exec 3>/dev/fd/3` is withheld, since a descriptor path is not on the list.
func TestBuild_ADevicePathIsNotOutsideTheProject(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		[]string{
			"ls -la src/ 2>/dev/null || true", "which go node python3 2>/dev/null",
			"go build ./... >/dev/null && echo ok", "cat /dev/stdin | wc -l",
		},
		[]string{"cat /dev/disk/by-id/x", "cat /devices/x/y", "exec 3>/dev/fd/3"},
		nil)
}

// TestBuild_ADockerBindMountIsWithheld: a bind mount names a container path (`:/src`, `-w /src`) that
// reads as an absolute path, so under D63 a Docker bind mount of the project is over-withheld. The
// mount of a path outside the project stays withheld. No outside path is shown.
func TestBuild_ADockerBindMountIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	slash := strings.ReplaceAll(root, `\`, "/")
	requireScreened(t, root, hostRules(root, "./private/deny.txt"), nil,
		nil,
		[]string{
			`docker run --rm -v "` + root + `:/src" -w /src golang:1.23 go test ./...`,
			`docker run --rm -v "` + slash + `:/app" node:20 npm test`,
			`docker run -v "` + root + `2:/src" img`, "docker run -v /home/u/data:/data img",
		},
		[]string{"/home/u"})
}

// TestBuild_AReasonKeepsItsRestoreBesideAWithheldNamesake: a drop reason was screened for withheld
// paths by bare basename, so an allowed README.md and a nested src/CLAUDE.md lost their restore
// clauses beside a withheld private/README.md and an out-of-project CLAUDE.md, though the entry's id
// named the path anyway. A reason is screened for a withheld path's relative path as a whole path,
// and for absolute paths outside the project; only the clause that names it is redacted.
func TestBuild_AReasonKeepsItsRestoreBesideAWithheldNamesake(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "private/README.md", Hash: hashOf("private readme"), Why: "referenced"},
		{Path: "README.md", Hash: hashOf("readme"), Why: "referenced"},
	}
	cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
		ToolUseID: "toolu_home_claude", Hash: hashOf("claude"),
		Summary: filepath.Join(filepath.Dir(root), "home", ".claude", "CLAUDE.md"),
	})
	cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
		checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: "checkpoint: git index unsupported: reading index: open private/README.md: denied"},
		checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: "checkpoint: scan failed; skipped private/README.md; kept the rest"},
	)
	d := uat05Deps(t, cp)
	d.HostPaths = hostRules(root, "./private/**")
	d.Rules = &fakeScanner{
		pathScoped: []rules.Rule{{Path: ".claude/rules/docs.md", Globs: []string{"README.md"}, Body: "Keep the readme short."}},
		nested:     []rules.Rule{{Path: "src/CLAUDE.md", Body: "Nested guidance for src."}},
	}
	r := requestFor(t, cp, core.Tokens(400))
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireNoLeak(t, res, []string{"private/README.md", ".claude" + string(filepath.Separator) + "CLAUDE.md"})
	rule, ok := dropForKind(res.Dropped, dropKindPathRule, ".claude/rules/docs.md")
	require.True(t, ok, "fixture: the path rule did not fit: %v", res.Dropped)
	require.Equal(t, "matched README.md; did not fit the rehydration budget; restore: Read .claude/rules/docs.md", rule.Detail)
	nested, ok := dropForKind(res.Dropped, dropKindNestedClaudeMD, "src/CLAUDE.md")
	require.True(t, ok, "fixture: the nested CLAUDE.md did not fit: %v", res.Dropped)
	require.Equal(t, "did not fit the rehydration budget; restore: Read src/CLAUDE.md", nested.Detail)
	var details []string
	for _, e := range res.Dropped {
		if e.Kind == "pointer_git_unavailable" {
			details = append(details, e.Detail)
		}
	}
	require.Equal(t, []string{
		"checkpoint: git index unsupported: reading index: " + withheldDropID + ": denied",
		"checkpoint: scan failed; " + withheldDropID + "; kept the rest",
	}, details, "a reason naming the withheld path is redacted, clause by clause")
}

// TestBuild_AOneWordRevisionPoisonsNoFreeText: on Windows the host refuses an 8.3-shaped word it
// cannot resolve (`HEAD~1`, `PROGRA~1`) under any Read rule, so a one-word summary of it is withheld,
// and it was noted as a withheld path, which withheld every `git diff HEAD~1` in the build. A
// one-word relative value with no separator is a Glob or Grep argument, not a recorded path, and is
// not noted.
func TestBuild_AOneWordRevisionPoisonsNoFreeText(t *testing.T) {
	root := previewRoot("proj")
	inner := hostRules(root, "./private/deny.txt")
	shortShaped := func() HostRules {
		h := inner()
		refuses := h.Refuses
		h.Refuses = func(p string) bool { return strings.Contains(filepath.Base(p), "~1") || refuses(p) }
		return h
	}
	requireScreened(t, root, shortShaped, nil,
		[]string{"git diff HEAD~1", "dir PROGRA~1"},
		[]string{"HEAD~1", "PROGRA~1"},
		nil)
}

// TestScreenLiteral_ResolvesDotDotAsTheHostDoes: hostperm resolves `..` in a rule lexically
// (cleanLiteral), so `Read(./private/deny.txt/..)` refuses private/**; the screen's literal is
// therefore `private`, not `deny.txt`.
func TestScreenLiteral_ResolvesDotDotAsTheHostDoes(t *testing.T) {
	lit, anchored := screenLiteral("./private/deny.txt/..")
	require.Equal(t, "private", lit)
	require.False(t, anchored)
	lit, anchored = screenLiteral("~/a/../.ssh/**")
	require.Equal(t, paths.Key(".ssh"), lit)
	require.True(t, anchored)
}
