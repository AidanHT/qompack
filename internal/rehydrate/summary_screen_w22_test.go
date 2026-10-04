package rehydrate

import (
	"context"
	"fmt"
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

// TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements is audit 2's finding 28. Each
// path-keyed checkpoint drop (file_pointer, pointer_missing, pointer_invalid, pointer_untracked,
// pointer_dirty) cost one host judgement, uncached on disk, and their number grows with the session:
// the checkpointer keeps every touched file as a pointer and its budget cut names each pointer it
// cuts, so a long session handed the build a thousand of them and the build's cost grew without
// bound towards the compaction answer's budget. While a Read rule is in force the build judges at
// most dropJudgementsBound of them by the host, in the order the checkpoint lists them, and withholds
// every later one unjudged, by hash or as a withheld path, still accounted for (fail closed). A drop
// past the bound whose spelling names a rule's literal is learned as a withheld path, so a selector
// naming it by its basename is withheld; any other is to the free-text screen a path Qompack never
// recorded (ADR 0011 §23 item 2's limits). With no Read rule in force the host's answer costs nothing
// and every drop is judged and shown as before.
func TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements(t *testing.T) {
	const dropJudgementsBound = 64 // ADR 0011 §23 item 10
	const n = 1000
	root := privacyRoot(t)
	truncated := "truncated at budget; re_read(path) still resolves"
	cp := ckUAT05()
	cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
		checkpoint.DropEntry{Kind: "file_pointer", ID: "private/deny.txt", Detail: truncated})
	for i := 0; i < n; i++ {
		cp.Dropped = append(cp.Dropped,
			checkpoint.DropEntry{Kind: "file_pointer", ID: fmt.Sprintf("pkg/sub%d/file%d.go", i%50, i), Detail: truncated})
	}
	cp.Dropped = append(cp.Dropped, checkpoint.DropEntry{Kind: "file_pointer", ID: "secrets/k/key999.txt", Detail: truncated})
	cp.Pointers.Tools = append(cp.Pointers.Tools,
		checkpoint.ToolPointer{ToolUseID: "toolu_literal", Hash: hashOf("literal"), Summary: `{"query":"path:key999.txt"}`},
		checkpoint.ToolPointer{ToolUseID: "toolu_within", Hash: hashOf("within"), Summary: `{"query":"path:pkg/sub0/file0.go"}`},
	)
	build := func(t *testing.T, hp HostPaths) (Result, int) {
		calls := 0
		d := uat05Deps(t, cp)
		d.HostPaths = func() HostRules {
			h := hp()
			refuses := h.Refuses
			h.Refuses = func(p string) bool { calls++; return refuses(p) }
			return h
		}
		r := requestFor(t, cp, maxBudget())
		r.ProjectRoot = root
		res, err := Build(context.Background(), r, d)
		require.NoError(t, err)
		return res, calls
	}
	withheldDrops := func(res Result) int {
		k := 0
		for _, e := range res.Dropped {
			if e.Kind == "file_pointer" && e.ID == withheldDropID {
				k++
			}
		}
		return k
	}
	section6 := func(res Result) string { return sectionBody(res.Text, sectionHeading(ItemPointers)) }
	within := `- tool_use toolu_within ` + hashOf("within").String() + ` — {"query":"path:pkg/sub0/file0.go"}` + "\n"

	t.Run("UAT-12 rules", func(t *testing.T) {
		res, calls := build(t, hostRules(root, uat12Rules...))
		// ckUAT05's one file pointer, reports.py (its one-word summary is the same path, judged once),
		// then the drops up to the bound: the denied one and the first 63 of the thousand.
		require.Equal(t, 1+dropJudgementsBound, calls, "a build judges a bounded number of path-keyed drops")
		requireNoLeak(t, res, []string{"deny.txt", "file999.go", "file63.go", "key999.txt", "secrets/"})
		_, ok := dropForKind(res.Dropped, "file_pointer", "pkg/sub12/file62.go")
		require.True(t, ok, "a drop the host judged and allows is shown as recorded")
		require.Equal(t, 1+(n-(dropJudgementsBound-1))+1, withheldDrops(res),
			"the denied drop and every drop past the bound are withheld, and still accounted for")
		require.Contains(t, section6(res), "- tool_use toolu_literal "+hashOf("literal").String()+" — "+withheldSummary+"\n",
			"a drop past the bound that names a rule's literal is learned, so its basename is withheld")
		require.Contains(t, section6(res), within, "a selector naming a judged, allowed drop is shown")
	})
	t.Run("no Read rules", func(t *testing.T) {
		res, calls := build(t, func() HostRules { return HostRules{Refuses: func(string) bool { return false }} })
		require.Equal(t, 1+n+2, calls, "with no rule in force every drop is judged, at no cost")
		require.Zero(t, withheldDrops(res), "and none is withheld")
		require.Contains(t, section6(res), within)
	})
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
