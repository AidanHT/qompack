package rehydrate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
)

// Coordinator decision D64 (ADR 0011 §23), on the final verify of wave 19e: the project root unit
// is held together only when the root's own spelling is built from characters no shell splits or
// reinterprets it at, and a store cut right after a PowerShell drive's or provider's `:` is withheld
// as a single-letter drive there already is. A bare drive or provider name with nothing after its
// `:` names a drive root and reveals no file, so it stays inert. The root rows and the cut row are
// red on f2171654, on Windows and in a Linux container alike.

// d64ExcludedRootSegments are root segments that each hold one character the root unit does not
// admit (D64(1)) and that this platform allows in a directory's name: a quote of either kind or a
// typographic one, a backtick, `$ ! ; & ( ) [ ] { } ^ % ~`, the `,` PowerShell and cmd.exe split an
// argument at, the `=` cmd.exe splits at, the `+` cmd.exe's copy splits at, the `#` zsh's
// EXTENDED_GLOB reads, a Unicode space, and on Linux and macOS also `" | < > * ?`, a `:` past the
// drive, a backslash and a control character.
func d64ExcludedRootSegments() []string {
	segs := []string{
		"o'brien", "a`b", "a$b", "a!b", "a;b", "a&b", "a(b)", "a[b]", "a{b}", "a^b", "a%b",
		"PROGRA~1", "a,b", "a=b", "a+b", "a#b", "a\u00a0b", "a\u3000b", "o\u2019brien",
	}
	if runtime.GOOS != "windows" {
		segs = append(segs, `a"b`, "a|b", "a<b>", "a*b", "a?b", "a:b", `a\b`, "a\tb")
	}
	return segs
}

// rootSummaries are the summaries a session leaves that spell root as free text, or as a one-word
// Read or a Glob preview of it.
func rootSummaries(root string) []string {
	return []string{
		"cd " + root + " && go test ./...",
		"git -C " + root + " status",
		root + " TODO",
		root + " **/*.go",
		filepath.Join(root, "src", "main.go"),
		"cd " + filepath.ToSlash(root) + " && make",
	}
}

// notebookPreview is the store's canonical-JSON preview of a NotebookEdit of p: one path-named value.
func notebookPreview(t *testing.T, p string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"notebook_path": p})
	require.NoError(t, err)
	return string(raw)
}

// TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit is D64(1): the root's spelling was held together
// as one token-safe unit whatever it held, so a shell that splits or reinterprets the root at one of
// its own characters read a different path than the unit stood for (`C:/q/o'brien/proj/notes.tx't`
// joins the root's apostrophe to a later one; `/q/x;y/proj` is a command `/q/x` then `y/proj`;
// PowerShell splits `C:\q\a,b\proj` into `C:\q\a` and `b\proj`), and a root with a Unicode space or a
// tab matched a sibling spelled with an ASCII space. A root holding any character outside the unit's
// set has no unit, so a summary spelling it is judged as the free text it is and withheld. A
// path-named JSON value is still a structured value (it reaches no shell), and a root built from
// letters, marks, digits, `- _ . @`, its separators and spaces keeps its unit.
func TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit(t *testing.T) {
	for _, seg := range d64ExcludedRootSegments() {
		t.Run(seg, func(t *testing.T) {
			root := previewRoot(seg, "proj")
			structured := notebookPreview(t, filepath.Join(root, "src", "x.ipynb"))
			shown, withheld := []string{structured}, rootSummaries(root)
			if classReading(root) != root {
				// A glob class in the root's own spelling reads as another path (`a[b]` as `ab`), outside the
				// project, so even the structured value is withheld (ADR 0011 §23 item 6).
				shown, withheld = nil, append(withheld, structured)
			}
			if sib := strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) {
					return ' '
				}
				return r
			}, root); sib != root {
				withheld = append(withheld, "cd "+sib+" && go test ./...", "cat "+filepath.Join(sib, "notes.txt"))
			}
			requireScreened(t, root, hostRules(root, uat12Rules...), nil, shown, withheld, []string{"deny.txt"})
		})
	}
	for _, seg := range []string{"a@b", "a-b_c.d", "John Smith", "José"} {
		t.Run(seg, func(t *testing.T) {
			root := previewRoot(seg, "proj")
			requireScreened(t, root, hostRules(root, uat12Rules...), nil, rootSummaries(root),
				[]string{"cd " + root + " && cat private/deny.txt", filepath.Join(root+"2", "x.txt")},
				[]string{"deny.txt"})
		})
	}
}

// TestBuild_AReasonHoldsTheRootASummaryDoesNot pins D64(1)'s scope: a drop entry's reason is
// Qompack's own error prose, not a shell command, and its screen keeps holding the root together
// whatever the root holds, which is what finds a withheld project path named absolutely (a root left
// unheld would read `<root>/private/deny.txt` as the tail of a longer path and show it) and keeps
// an allowed one, in a root with an apostrophe, a `;` and a space.
func TestBuild_AReasonHoldsTheRootASummaryDoesNot(t *testing.T) {
	for _, elem := range [][]string{{"o'brien", "proj"}, {"John O'Brien", "proj"}, {"a;b c", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			index := "checkpoint: git index unsupported: reading index: open " + filepath.Join(root, ".git", "index") + ": gone"
			cp := ckUAT05()
			cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"}}
			cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: index},
				checkpoint.DropEntry{
					Kind:   "pointer_git_unavailable",
					Detail: "rules: read " + filepath.Join(root, "private", "deny.txt") + ": Access is denied.",
				})
			d := uat05Deps(t, cp)
			d.HostPaths = hostRules(root, uat12Rules...)
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			requireNoLeak(t, res, []string{"deny.txt"})
			var details []string
			for _, e := range res.Dropped {
				if e.Kind == "pointer_git_unavailable" {
					details = append(details, e.Detail)
				}
			}
			require.Len(t, details, 2, "every git drop is still reported: %v", res.Dropped)
			require.Contains(t, details, index, "a project path is shown")
			require.Contains(t, details, "rules: "+withheldDropID+": Access is denied.")
		})
	}
}

// TestBuild_ACutRightAfterAProviderDriveColonIsWithheld is D64(2): a store cut right after the `:`
// of a single-letter drive (`Get-Content C:…`) was withheld, but one right after a PowerShell drive's
// or provider's (`Temp:`, `HKCU:`, `Env:`, `FileSystem:`), at any path start, in a quoted run, a JSON
// string or a URL, was shown, though the cut may hide the file after it (`Temp:secret.txt`). The cut
// word is judged as if a name followed its `:`. An inert prefix (`path:`, `sha256:`) names no drive.
func TestBuild_ACutRightAfterAProviderDriveColonIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			cutAfter(t, "qompack recall ", " path:"),
			cutAfter(t, "expand ", " sha256:"),
			cutAfter(t, "go test ", " ./internal/"),
		},
		[]string{
			cutAfter(t, "Get-Content ", " Temp:"),
			cutAfter(t, "Get-Content ", " HKCU:"),
			cutAfter(t, "Get-Content ", " Env:"),
			cutAfter(t, "Get-Content ", " FileSystem:"),
			cutAfter(t, "Get-Content ", ` Temp:\`),
			cutAfter(t, "cat ", " x,Temp:"),
			cutAfter(t, "cat ", " --dir=Temp:"),
			cutAfter(t, "cat ", " -oTemp:"),
			cutAfter(t, "cat ", " a|Temp:"),
			cutAfter(t, `pwsh -c "gc `, " Temp:"),
			cutAfter(t, `{"command":"Get-Content `, " Temp:"),
			cutAfter(t, "curl ", " https://x.example/a=Temp:"),
			cutAfter(t, "Get-Content ", " C:"),
		},
		nil)
}

// TestBuild_ABareDriveNameNamesADriveRoot pins D64(3), a ruling with no code change: a drive or
// provider name with nothing after its `:` (`Temp:`, `Env:`, `HKCU:`, a conventional commit's `fix:`
// and `feat:`) names a drive root and reveals no file, so it is no path outside the project under D50
// and D63, and withholding it would hide every conventional commit message. A bare single-letter
// drive (`C:`, `D:`) stays withheld, and so does a drive name with a path after its `:`.
func TestBuild_ABareDriveNameNamesADriveRoot(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{
			"Get-ChildItem Temp:", "Get-ChildItem Env:", "Get-ChildItem HKCU:",
			`git commit -m "fix: handle a nil root"`, `git commit -m "feat: add the users endpoint"`,
			`{"query":"fix: retry backoff"}`,
		},
		[]string{
			"cd C:", "Get-ChildItem D:", "cd C: && ls", "Get-Content Temp:secret.txt", "cat x,Env:HOME",
			"Get-ChildItem HKCU:Software",
		},
		[]string{"secret.txt"})
}

// TestBuild_APathOrNameAfterAPlusIsJudged: deciding `+` for the root unit (D64(1)) found that cmd.exe's
// copy starts its next source after a `+` glued into a word (`copy a.txt+b.txt c.txt` concatenates
// both), but the free-text screen read `+` as continuing a name, so neither a path nor a name started
// after it: `copy a.txt+\Windows\win.ini out.txt` named a drive-rooted file and `copy a.txt+.env
// out.txt` a denied one, and both were shown. A path, and a name, may now start after `+`; a `+` with
// no path after it (`g++`, `date +%s`, `1+1`) is still safe, and a `c++/` directory in a path is
// over-withheld (`/v1` starts after its second `+`).
func TestBuild_APathOrNameAfterAPlusIsJudged(t *testing.T) {
	root := previewRoot("proj")
	requireScreened(t, root, hostRules(root, uat12Rules...), nil,
		[]string{"copy a.txt+b.txt c.txt", "g++ -O2 -o main main.cpp", "date +%s", "echo 1+1"},
		[]string{
			`copy a.txt+\Windows\win.ini out.txt`, "copy a.txt+C:secret.txt out.txt",
			"copy a.txt+Temp:secret.txt out.txt", "copy a.txt+/etc/passwd out.txt", "copy a.txt+~/x out.txt",
			`copy a.txt+\server\share\x out.txt`, "copy a.txt+.env out.txt",
			`copy a.txt+secrets\token.txt out.txt`, "ls include/c++/v1",
		},
		[]string{"win.ini", "passwd", "secret.txt", "token.txt"})
}
