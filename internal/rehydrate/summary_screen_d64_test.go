package rehydrate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
)

// Coordinator decision D64 (ADR 0011 §23), on the final verify of wave 19e: the project root unit
// is held together only when the root's own spelling is built from characters no shell splits or
// reinterprets it at, and a store cut right after a PowerShell drive's or provider's `:` is withheld
// as a single-letter drive there already is. A bare drive or provider name with nothing after its
// `:` names a drive root and reveals no file, so it stays inert. Deciding `+` for the root unit found
// that a path and a name may start after a `+` in free text too (cmd.exe's copy). The root rows, the
// cut row and the `+` row are red on f2171654, on Windows and in a Linux container alike; the reason
// and bare-drive rows pin what D64 leaves as it is.

// d64ExcludedRootSegments are root segments that each hold one character the root unit does not
// admit (D64(1)) and that this platform allows in a directory's name: a quote of either kind or a
// typographic one, a backtick, `$ ! ; & ( ) [ ] { } ^ % ~`, the `,` PowerShell and cmd.exe split an
// argument at, the `=` cmd.exe splits at, the `+` cmd.exe's copy splits at, the `#` zsh's
// EXTENDED_GLOB reads, the `@` PowerShell splats a word of the root at (`John @Work`), a Unicode
// space, a run of ASCII spaces (which the store's preview spells as one, so no text spells the root
// exactly), and on Linux and macOS also `" | < > * ?`, a `:` past the drive, a backslash and a
// control character.
func d64ExcludedRootSegments() []string {
	segs := []string{
		"o'brien", "a`b", "a$b", "a!b", "a;b", "a&b", "a(b)", "a[b]", "a{b}", "a^b", "a%b",
		"PROGRA~1", "a,b", "a=b", "a+b", "a#b", "a\u00a0b", "a\u3000b", "o\u2019brien",
		"a@b", "John @Work", "a  b",
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
// letters, marks, digits, `- _ .`, its separators and single spaces keeps its unit. Since the
// round-2 verify of D64, a root with an `@` (PowerShell splats a word of the root that is a whole
// `@name`) or a run of spaces (the store's preview spells it as one space, so a sibling spelled with
// one space reads as the root) has none either.
func TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit(t *testing.T) {
	for _, seg := range d64ExcludedRootSegments() {
		t.Run(seg, func(t *testing.T) {
			root := previewRoot(seg, "proj")
			structured := notebookPreview(t, filepath.Join(root, "src", "x.ipynb"))
			shown := []string{structured}
			withheld := append(rootSummaries(root),
				"cd "+root+" && cat private/deny.txt", filepath.Join(root+"2", "x.txt"))
			if classReading(root) != root {
				// A glob class in the root's own spelling reads as another path (`a[b]` as `ab`), outside the
				// project, so even the structured value is withheld (ADR 0011 §23 item 6).
				shown, withheld = nil, append(withheld, structured)
			}
			if sib := whitespaceSibling(root); sib != root {
				withheld = append(withheld, "cd "+sib+" && go test ./...", "cat "+filepath.Join(sib, "notes.txt"))
			}
			requireScreened(t, root, hostRules(root, uat12Rules...), nil, shown, withheld, []string{"deny.txt"})
		})
	}
	for _, seg := range []string{"a-b_c.d", "John Smith", "José"} {
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
// whatever characters the root holds, which is what finds a withheld project path named absolutely
// (a root left unheld would read `<root>/private/deny.txt` as the tail of a longer path and show it)
// and keeps an allowed one, in a root with an apostrophe, a `;` and a space. It holds only a root a
// sanitized text spells exactly (TestBuild_ARootNoTextSpellsExactlyIsNeverHeld).
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

// whitespaceSibling is root as a sanitized text spells it (sanitize, the store's preview): every run
// of whitespace, a tab or a Unicode space among it, one ASCII space, and the ends trimmed. When root's
// own spelling has any other whitespace it is a directory beside the project.
func whitespaceSibling(root string) string { return strings.Join(strings.Fields(root), " ") }

// TestBuild_ARootNoTextSpellsExactlyIsNeverHeld is the round-2 verify of D64(1). A drop entry's
// reason held the root together whatever it held (holdRoot), and holdRoot found the root by its
// spelling with every run of whitespace folded to one ASCII space, as a sanitized text spells it. So
// under a root with a Unicode space, a tab, a run of spaces or a trailing space, a reason that named
// the sibling spelled with one space, outside the project (a linked worktree's gitdir, a rule file
// a scan could not read), read it as the root and showed it; and under a root with a run of spaces or
// a trailing space, which kept its unit, a summary naming that sibling was shown too. A root that no
// sanitized text spells exactly is held in neither: a reason then splits at the root's own whitespace,
// whose first piece is outside the project, so a reason naming a path under such a root is redacted
// as well (it fails closed), and every summary that spells the root is withheld.
func TestBuild_ARootNoTextSpellsExactlyIsNeverHeld(t *testing.T) {
	roots := [][]string{{"a b", "proj"}, {"a　b", "proj"}, {"a  b", "proj"}}
	if runtime.GOOS != "windows" {
		roots = append(roots, []string{"a\tb", "proj"}, []string{"a", "proj "})
	}
	for _, elem := range roots {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			sib := whitespaceSibling(root)
			require.NotEqual(t, root, sib, "fixture: the sibling is another directory")
			requireScreened(t, root, hostRules(root, uat12Rules...), nil, nil,
				append(rootSummaries(root), "cd "+sib+" && go test ./...", "cat "+filepath.Join(sib, "notes.txt")),
				[]string{"deny.txt"})

			gitdir := "checkpoint: git worktree gitdir at " + filepath.Join(sib, ".git", "worktrees", "wt") + " is unreadable"
			rules := "skills: reading rules at " + filepath.Join(sib, "notes", "x.md") + " failed"
			index := "checkpoint: git index unsupported: reading index: open " + filepath.Join(root, ".git", "index") + ": gone"
			cp := ckUAT05()
			cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: gitdir},
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: rules},
				checkpoint.DropEntry{Kind: "pointer_git_unavailable", Detail: index})
			d := uat05Deps(t, cp)
			d.HostPaths = hostRules(root, uat12Rules...)
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			requireNoLeak(t, res, []string{filepath.Join(sib, ".git"), filepath.Join(sib, "notes")})
			var details []string
			for _, e := range res.Dropped {
				if e.Kind == "pointer_git_unavailable" {
					details = append(details, e.Detail)
				}
			}
			require.Len(t, details, 3, "every git drop is still reported: %v", res.Dropped)
			require.NotContains(t, details, gitdir, "the sibling's gitdir is outside the project")
			require.NotContains(t, details, rules, "the sibling's rule file is outside the project")
			require.NotContains(t, details, index, "a path under a root no text spells exactly is redacted")
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
