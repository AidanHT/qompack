package rehydrate

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The final verify of wave 19d (ADR 0011 §23): the root's spelling held a backslash as a separator on
// every platform, a URL token was split at `,` by PowerShell, a PowerShell provider drive (`Temp:`,
// `Env:`, `HKCU:` or any name New-PSDrive defines) was no path, and a `#` that starts a token hid the
// path after it. Each row is red on f3196046.

// mixedRoot is root spelled with its last separator a backslash and every other one a slash
// (`/q\proj`, `C:/q\proj`): a POSIX shell, Git Bash on Windows included, drops that backslash and reads
// a sibling of an ancestor of the root (`/qproj`, `C:/qproj`), outside the project.
func mixedRoot(root string) string {
	s := filepath.ToSlash(root)
	i := strings.LastIndexByte(s, '/')
	return s[:i] + `\` + s[i+1:]
}

// TestBuild_ABackslashInsideTheRootsSpellingIsNoRoot: the root's spelling accepted a backslash as a
// separator on every platform, so a spelling with a backslash between two of its segments was held
// together as the root and the two backslash readings never saw that backslash; a POSIX shell drops
// it and merges the two segments into a sibling of an ancestor of the root. On Linux and macOS the
// root is spelled with `/` alone, and on Windows with one separator style throughout: either slash,
// but not both (Git Bash drops a backslash after a forward-slash spelling just as bash does). A
// spelling with a backslash in it there is not the root, and is judged as the free text it is.
func TestBuild_ABackslashInsideTheRootsSpellingIsNoRoot(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			fwd := filepath.ToSlash(root)
			mixed := mixedRoot(root)
			shown := []string{
				"cat " + fwd + "/notes.txt",
				"cd " + fwd + " && make",
				`{"command":"cat ` + fwd + `/notes.txt"}`,
			}
			withheld := []string{
				"cat " + mixed + "/sibling.txt",
				"cd " + mixed + " && cat sibling.txt",
				"git -C " + mixed + " status",
				`{"command":"cat ` + jsonPath(mixed) + `/sibling.txt"}`,
				`cat "` + mixed + `/sibling.txt"`,
			}
			if runtime.GOOS == "windows" {
				// A spelling in backslashes alone is the root to cmd.exe and PowerShell.
				shown = append(shown,
					"cat "+filepath.Join(root, "notes.txt"),
					`{"command":"cat `+jsonPath(filepath.Join(root, "notes.txt"))+`"}`)
			} else {
				withheld = append(withheld, "cat "+strings.ReplaceAll(fwd, "/", `\`)+`\sibling.txt`)
			}
			requireScreened(t, root, hostRules(root, uat12Rules...), nil, shown, withheld,
				[]string{"sibling.txt"})
		})
	}
}

// TestBuild_AURLIsSafeOnlyFromAStrictCharacterSet: a URL token was checked for a path only after an
// `&`, but PowerShell splits a bare argument at `,` into an array, so `https://x,/etc/passwd` handed a
// cmdlet `/etc/passwd`. A URL token is now built only from letters, digits and `- . _ ~ : / ? # @ & =
// +` (no `,`, `;`, `!`, `$`, quote, parenthesis, glob or backslash, which a shell splits or expands),
// and each piece of it after a `=`, `:` or `@`, and each part after an `&`, is judged as a plain
// token's path start is (a root, a home, a drive, a provider drive, a `file:` URL); a `..` in it
// climbs. Its `?` and `#` stay (a glob there matches only below a directory named `http:`).
func TestBuild_AURLIsSafeOnlyFromAStrictCharacterSet(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			"https://example.com/docs#install",
			"https://example.com/search?q=go&page=2",
			"curl -s https://example.com:8080/api/v1/items",
			"https://user@example.com/x",
			`{"url":"https://example.com/a?b=c"}`,
			"https://en.wikipedia.org/wiki/Talk:Main_Page",
			"curl https://example.com/~user/",
		},
		[]string{
			"https://x.example,/etc/passwd",
			"curl -s https://x.example,/etc/passwd",
			"Invoke-WebRequest https://x.example/a,/etc/passwd",
			`{"url":"https://x.example,/etc/passwd"}`,
			"https://x.example/a,b",
			"curl https://x.example/?f=/etc/passwd",
			"curl https://x.example/?d=C:secret.txt",
			"curl https://x.example/?d=Temp:secret.txt",
			"curl https://u@/etc/passwd",
			"curl https://x.example/a/../../etc/passwd",
			"curl https://x.example/a;b",
		},
		[]string{"passwd", "secret.txt"})
}

// TestBuild_APowerShellProviderDrivePathIsWithheld: only a single letter followed by `:` was read as
// a drive, but PowerShell's provider drives (`Temp:`, `Env:`, `HKLM:`, `HKCU:`, `Cert:`, `Function:`,
// `Alias:`, `Variable:`, `WSMan:`) and any drive New-PSDrive defines make `Temp:secret.txt` a path
// outside the project, and a provider-qualified path (`Registry::…`, `FileSystem::…`) one too. A
// name, a `:` and more at any place a path may start is withheld, unless the name holds a character
// PowerShell refuses in a drive's name (`.`, `~`) or is one of the inert prefixes Qompack's own
// previews need: recall's `path:` selector, a hash's `sha256:` and an http(s) URL's scheme before its
// `//`. A name with nothing after its `:` (`fix:` in a commit message) names no path.
func TestBuild_APowerShellProviderDrivePathIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	shown := []string{
		`{"query":"path:src/main.go"}`,
		`{"hash":"sha256:9f2c0b4e1d7a"}`,
		`git commit -m "fix: handle a nil root"`,
		"git clone git@github.com:org/repo.git",
		"pytest tests/test_api.py::test_create_user -v",
		"curl -s http://127.0.0.1:8080/healthz",
	}
	leaks := []string{"secret.txt", "USERPROFILE", "Software", "SOFTWARE", "HKEY_CURRENT_USER", "mydocs"}
	// Two builds, so that section 6 holds every pointer within its share.
	requireScreened(t, root, hostRules(root, uat12Rules...), nil, shown,
		[]string{
			"Get-Content Temp:secret.txt",
			"gc Env:USERPROFILE",
			"Get-ChildItem HKCU:Software",
			"Get-ChildItem HKLM:SOFTWARE",
			"Get-Content Function:prompt",
			"Get-Item Alias:ls",
			"Get-Content Variable:HOME",
			"Get-ChildItem WSMan:localhost",
			"Get-ChildItem Cert:CurrentUser",
			"Get-Content mydocs:secret.txt",
		}, leaks)
	requireScreened(t, root, hostRules(root, uat12Rules...), nil, shown,
		[]string{
			"Get-Content Registry::HKEY_CURRENT_USER",
			`Get-Content Microsoft.PowerShell.Core\Registry::HKEY_CURRENT_USER`,
			"Get-Content FileSystem::secret.txt",
			"cat --path=Temp:secret.txt",
			"cat a,Temp:secret.txt",
			"cat -oTemp:secret.txt",
			`{"query":"Temp:secret.txt"}`,
			`cat "Temp:secret.txt"`,
			"cat http:secret.txt",
			`{"query":"symbol:secret.txt"}`,
		}, leaks)
}

// TestBuild_APathAfterACommentMarkIsJudged: a token that starts with `#` (a comment to a POSIX shell
// and PowerShell, an ordinary argument to cmd.exe) got no path start after the `#`, so
// `#/home/u/.ssh/id_rsa` was shown while `# /home/u/...` was withheld. A path may start after a `#`.
func TestBuild_APathAfterACommentMarkIsJudged(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{"# just a comment", `git commit -m "fix #42"`, "echo #42"},
		[]string{
			"#/home/u/.ssh/id_rsa",
			"echo #/etc/passwd",
			"#~/.ssh/id_rsa",
			"#C:secret.txt",
			`#\Users\me\secret.txt`,
			`git commit -m "fix #/etc/passwd"`,
			"#Temp:secret.txt",
		},
		[]string{"id_rsa", "passwd", "secret.txt"})
}
