package rehydrate

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
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

// TestBuild_AQompackCommandInADropReasonIsNoPath is audit 2's finding 27: the drop-reason screen read
// every whitespace token led by `/` as an absolute path outside the project, so the checkpointer's
// own recovery instruction for pins it could not re-read at the seal (`run /qompack:pin --list`)
// was redacted to `(path withheld)`, with the error after it. One of Qompack's own slash commands
// is no path; a real absolute path in a reason, of one segment or more, is still withheld.
func TestBuild_AQompackCommandInADropReasonIsNoPath(t *testing.T) {
	pins := checkpoint.DropEntry{
		Kind: "invariants", ID: "pins",
		Detail: "pins could not be re-read at the seal, so pins made after the draft began may be missing; " +
			"run /qompack:pin --list: pins: store closed",
	}
	redacted := []checkpoint.DropEntry{
		{Kind: "pointer_git_unavailable", ID: "git1", Detail: "checkpoint: git unavailable: not a git repository: /repo.git"},
		{Kind: "pointer_git_unavailable", ID: "git2", Detail: "checkpoint: git index unsupported: cannot read /secrets.txt now"},
		{Kind: "pointer_git_unavailable", ID: "git3", Detail: "checkpoint: git index unsupported: see /etc/qompack/x.conf"},
		{Kind: "pointer_git_unavailable", ID: "git4", Detail: "checkpoint: run /qompack:pin/../../etc/passwd"},
	}
	for _, tc := range []struct {
		name string
		host func(root string) HostPaths
	}{
		{"no host rules", func(string) HostPaths { return nil }},
		{"a deny rule", func(root string) HostPaths { return denyFiles(root, "private/deny.txt") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := privacyRoot(t)
			cp := ckUAT05()
			cp.Dropped = append(append(append([]checkpoint.DropEntry(nil), cp.Dropped...), pins), redacted...)
			d := uat05Deps(t, cp)
			d.HostPaths = tc.host(root)
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			got, ok := dropForKind(res.Dropped, pins.Kind, pins.ID)
			require.True(t, ok)
			require.Equal(t, pins.Detail, got.Detail, "the recovery instruction and its error are kept")
			for _, e := range redacted {
				got, ok := dropForKind(res.Dropped, e.Kind, e.ID)
				require.True(t, ok)
				require.Contains(t, got.Detail, withheldDropID, "%q names a path outside the project", e.Detail)
			}
			requireNoLeak(t, res, []string{"/repo.git", "/secrets.txt", "/etc/qompack", "passwd"})
		})
	}
}

// TestBuild_ToolSearchsSelectorIsShown is audit 2's finding 31 under coordinator decision D67(l):
// ToolSearch's documented selector, `select:`, reads as a PowerShell drive named select, so the
// call Claude Code makes to load a deferred tool, Qompack's own among them, was withheld in every
// project. `select` joins the inert prefixes, with the same accepted limit as `path` (a drive a user
// names so). Another name, and a path after the selector's colon, are still withheld.
func TestBuild_ToolSearchsSelectorIsShown(t *testing.T) {
	shown := []string{
		`{"max_results":1,"query":"select:mcp__plugin_qompack_qompack__record_eliminated"}`,
		`{"max_results":3,"query":"select:Read,Edit,Grep"}`,
		"select:Read",
	}
	withheld := []string{
		`{"query":"select:/etc/passwd"}`,
		`{"query":"select:../outside/x.txt"}`,
		`{"query":"selector:mcp__x"}`,
		`{"query":"select:C:\\x"}`,
	}
	for _, tc := range []struct {
		name string
		host func(root string) HostPaths
	}{
		{"no host rules", func(string) HostPaths { return nil }},
		{"UAT-12 rules", func(root string) HostPaths { return denyFiles(root, "private/deny.txt", ".env") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := privacyRoot(t)
			cp := ckUAT05()
			cp.Pointers.Tools = nil
			for i, s := range append(append([]string(nil), shown...), withheld...) {
				id := fmt.Sprintf("toolu_ok_%02d", i)
				if i >= len(shown) {
					id = fmt.Sprintf("toolu_no_%02d", i)
				}
				cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
					ToolUseID: core.ToolUseID(id), Hash: hashOf(id), Summary: s,
				})
			}
			d := uat05Deps(t, cp)
			d.HostPaths = tc.host(root)
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root

			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
			for _, tp := range cp.Pointers.Tools {
				line := "- tool_use " + string(tp.ToolUseID) + " " + tp.Hash.String() + " — "
				if strings.HasPrefix(string(tp.ToolUseID), "toolu_no_") {
					require.Contains(t, section6, line+withheldSummary+"\n", "%q", tp.Summary)
					continue
				}
				require.Contains(t, section6, line+tp.Summary+"\n", "%q", tp.Summary)
			}
		})
	}
}

// pointersLegend is the one line under section 6's heading that explains a withheld pointer.
const pointersLegendW22 = "Withheld entries name a path the host's permission rules refuse, or one outside the project; " +
	"restore them by hash."

// TestBuild_AWithheldPointerIsExplainedOnceInItsSection is audit 2's finding 30: every withheld
// pointer line repeated a 97-character explanation (a withheld file's label about 84), charged to the
// payload's fixed character ceiling, so the boilerplate pushed real pointers out of section 6. The
// explanation is one line under the section's heading, present only when the section holds a withheld
// pointer, and each withheld line reads `(summary withheld)` or `file (path withheld)`. The line is
// priced exactly: the character ceiling never sets off the hard cap's re-truncation, and no token
// budget is exceeded.
func TestBuild_AWithheldPointerIsExplainedOnceInItsSection(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Files = append(cp.Pointers.Files,
		checkpoint.FilePointer{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"})
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_ok", Hash: hashOf("ok"), Summary: "cat data/meta.txt"},
		{ToolUseID: "toolu_w1", Hash: hashOf("w1"), Summary: "cat private/deny.txt"},
		{ToolUseID: "toolu_w2", Hash: hashOf("w2"), Summary: "cat ../outside/x.txt"},
	}
	for i := 0; i < 120; i++ {
		cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_zz_%03d", i)), Hash: hashOf(fmt.Sprint("many", i)),
			Summary: fmt.Sprintf("cat ../outside/%d.txt", i),
		})
	}
	build := func(budget core.Tokens) (Result, *spyLogger) {
		d := uat05Deps(t, cp)
		d.HostPaths = denyFiles(root, "private/deny.txt")
		log := &spyLogger{}
		d.Log = log
		r := requestFor(t, cp, budget)
		r.ProjectRoot = root
		res, err := Build(context.Background(), r, d)
		require.NoError(t, err)
		return res, log
	}

	res, log := build(0)
	requireInsideTheHostCeiling(t, res, cp.Session)
	for _, m := range log.msgs {
		require.NotContains(t, m, "re-truncating", "the legend is priced exactly against the ceiling")
	}
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	require.True(t, strings.HasPrefix(section6, pointersLegendW22+"\n"), "the legend opens section 6:\n%s", section6)
	require.Equal(t, 1, strings.Count(res.Text, pointersLegendW22), "the explanation is given once")
	require.Contains(t, section6, "- file (path withheld) "+hashOf("deny").String()+"\n")
	require.Contains(t, section6, "- tool_use toolu_w1 "+hashOf("w1").String()+" — (summary withheld)\n")
	require.Contains(t, section6, "- tool_use toolu_w2 "+hashOf("w2").String()+" — (summary withheld)\n")
	require.Contains(t, section6, "- tool_use toolu_ok "+hashOf("ok").String()+" — cat data/meta.txt\n")

	for _, budget := range []core.Tokens{300, 600, 900, 1500, 2500} {
		res, _ := build(budget)
		require.LessOrEqual(t, res.Tokens, budget, "budget %d", budget)
		requireInsideTheHostCeiling(t, res, cp.Session)
	}

	// Nothing withheld: no legend.
	plain := ckUAT05()
	plain.Pointers.Tools = []checkpoint.ToolPointer{{ToolUseID: "toolu_ok", Hash: hashOf("ok"), Summary: "cat data/meta.txt"}}
	d := uat05Deps(t, plain)
	d.HostPaths = denyFiles(root, "private/deny.txt")
	r := requestFor(t, plain, maxBudget())
	r.ProjectRoot = root
	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	require.NotContains(t, res.Text, pointersLegendW22)
	require.Contains(t, sectionBody(res.Text, sectionHeading(ItemPointers)), "- reports.py ")
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
