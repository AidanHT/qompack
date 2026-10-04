package rehydrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
)

// Wave 22's rows over audit 2's rehydrate findings (coordinator decisions D66 and D67).

// TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece is audit 2's finding 26. A
// path-named JSON value (path, file, paths, dir, cwd and their kin, or an element of such an array)
// is exempt from the free-text whitelist and was judged as ONE path: containment read a value whose
// first piece is relative as a relative path, the host joined the whole string under the root and
// refused nothing, and no rule literal or withheld name was in it, so a later piece naming a path
// outside the project (an absolute path, a POSIX root, a home, a variable, a drive-relative path, a
// PowerShell drive, a climb) was shown. Each piece of such a value, split where a list splits it, is
// judged for a path outside the project, cut or not, and so is a lone value's PowerShell drive; a
// single in-project path with a space in it, and the project root's own spelling with one, are still
// shown.
func TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece(t *testing.T) {
	esc := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			out := esc(previewRoot("outside", "x.txt"))
			sibling := esc(filepath.Join(filepath.Dir(root), "other", "x.txt"))
			withheld := []string{
				// The audit's eight shapes.
				`{"paths":"src/a.ts,` + out + `"}`,
				`{"paths":"src/a.ts ` + out + `"}`,
				`{"file":"src/a.ts /etc/passwd"}`,
				`{"paths":"src/a.ts ~/.ssh/id_rsa"}`,
				`{"paths":"src/a.ts $HOME/.aws/credentials"}`,
				`{"paths":["src/a.ts","b ` + out + `"]}`,
				`{"paths":"src/a.ts C:secret.txt"}`,
				`{"paths":"src/a.ts Temp:secret.txt"}`,
				// The class: every list separator, a path after a list's or an option's delimiter
				// inside a piece, a variable of cmd.exe, a climb, a sibling of the root, a lone
				// PowerShell drive, and the store's cut inside a later piece.
				`{"paths":"src/a.ts;` + out + `"}`,
				`{"paths":"src/a.ts|/etc/passwd"}`,
				`{"paths":"src/a.ts:/etc/passwd"}`,
				`{"paths":"src/a.ts --out=/etc/passwd"}`,
				`{"paths":"src/a.ts %USERPROFILE%\\.aws\\credentials"}`,
				`{"paths":"src/a.ts ../../outside/x.txt"}`,
				`{"paths":"src/a.ts ` + sibling + `"}`,
				`{"paths":["src/a.ts","b ~/.ssh/id_rsa"]}`,
				`{"path":"Temp:secret.txt"}`,
				`{"path":"HKCU:\\Software\\Vendor"}`,
				`{"cell_id":"c1","paths":"src/a.ts ` + esc(previewRoot("outside", "secret")) + `…`,
			}
			shown := []string{
				`{"file":"src/my notes.txt"}`,
				`{"paths":["docs/design notes.md","src/a.ts"]}`,
				`{"files":"src/main.go src/util.go"}`,
				`{"file":"src/main.go:10"}`,
				`{"file":"docs/a,b.md"}`,
				`{"notebook_path":"` + esc(filepath.Join(root, "nb", "my notes.ipynb")) + `"}`,
				`{"paths":"` + esc(filepath.Join(root, "src", "a.ts")) + ` ` + esc(filepath.Join(root, "src", "b.ts")) + `"}`,
				`{"paths":"src/a.ts ` + esc(filepath.Join(root, "docs", "b.md")) + `"}`,
			}
			leaks := []string{"passwd", "id_rsa", "credentials", "secret.txt", "Vendor", "../outside"}
			for _, p := range []string{filepath.Join("outside", "x.txt"), filepath.Join("other", "x.txt"), filepath.Join("outside", "secret")} {
				leaks = append(leaks, p, esc(p))
			}
			hp := denyFiles(root, "private/deny.txt")
			for _, s := range withheld {
				require.Equal(t, "withheld", summaryVerdict(t, root, hp, s, leaks), "%q holds a path outside the project", s)
			}
			for _, s := range shown {
				require.Equal(t, "shown", summaryVerdict(t, root, hp, s, leaks), "%q names only project paths", s)
			}
		})
	}
}

// summaryVerdict builds root's rehydration under hp with one tool pointer whose summary is s, beside a
// file pointer at private/deny.txt, and reports how section 6 renders s: "withheld" or "shown". The
// payload and the drop report must name none of leaks.
func summaryVerdict(t *testing.T, root string, hp HostPaths, s string, leaks []string) string {
	t.Helper()
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"}}
	cp.Pointers.Tools = []checkpoint.ToolPointer{{ToolUseID: "toolu_w22", Hash: hashOf(s), Summary: s}}
	d := uat05Deps(t, cp)
	d.HostPaths = hp
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root
	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireNoLeak(t, res, leaks)
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	line := "- tool_use toolu_w22 " + hashOf(s).String() + " — "
	switch {
	case strings.Contains(section6, line+withheldSummary+"\n"):
		return "withheld"
	case strings.Contains(section6, line+s+"\n"):
		return "shown"
	}
	t.Fatalf("the pointer is not in section 6: %q\n%s", s, section6)
	return ""
}
