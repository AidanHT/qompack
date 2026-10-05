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
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
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
//
// Wave 22's verify found the class open in two ways, both shown at eca33155: a path that starts
// inside a piece after a quote, a parenthesis, a bracket, a brace or an angle bracket was read as
// relative (`"/etc/passwd"`, `'~/.ssh/id_rsa'`), and a climb after an inner `=`, `@` or `:` was
// cleaned away by containment of the whole piece (`--out=../../x` is one segment the next `..`
// removes). A path may start wherever a reader starts one, so each is judged there, a climb
// included, in both readings of a backslash; the names a project's paths hold (`[id]`, `(auth)`,
// `+page`, `c++`, `C#`) and a quoted project path are still shown. The verdicts hold under a plain
// root, one with a space, one whose own name holds an apostrophe and parentheses and one whose own
// name holds an ampersand (none of the root's characters starts a path), with the host's rules in
// force and with none.
//
// Its fix round 2 found the class still open for characters no project path holds before a separator:
// a control character, which splits a list as whitespace does (`find -print0` and `git ls-files -z`
// write NUL-separated lists, which the store's preview keeps as \u0000), a glued redirect or command
// separator (`>`, `>>`, `&`, `&&`), and a letter whose Windows ANSI best fit is ASCII punctuation
// (U+02BA reaches an ANSI program as `"`, U+01C0 as `|`), all shown at eca33155 too. Each now starts
// a path anywhere in a piece, as free text reads them; the names a project's paths hold around them
// (`R&D`, `Q&A`, an okina inside a name) are still shown.
func TestBuild_APathNamedValueHoldingSeveralPathsIsJudgedPieceByPiece(t *testing.T) {
	esc := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}, {"O'Brien (x)", "proj"}, {"R&D", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			out := esc(previewRoot("outside", "x.txt"))
			sibling := esc(filepath.Join(filepath.Dir(root), "other", "x.txt"))
			inRoot := func(elem ...string) string { return esc(filepath.Join(append([]string{root}, elem...)...)) }
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
				// Wave 22's verify: a path that starts after a quote, a parenthesis, a bracket, a brace,
				// an angle bracket or a backtick, in a later piece or a lone value.
				`{"paths":"\"src/a.ts\" \"/etc/passwd\""}`,
				`{"paths":"src/a.ts '/etc/passwd'"}`,
				`{"paths":"src/a.ts,\"/etc/passwd\""}`,
				`{"paths":["src/a.ts","\"/etc/passwd\""]}`,
				`{"paths":"src/a.ts '~/.ssh/id_rsa'"}`,
				`{"paths":"src/a.ts \"$HOME/.aws/credentials\""}`,
				`{"paths":"src/a.ts (/etc/passwd)"}`,
				`{"paths":"src/a.ts [/etc/passwd]"}`,
				`{"paths":"src/a.ts {/etc/passwd}"}`,
				`{"paths":"src/a.ts </etc/passwd>"}`,
				"{\"paths\":\"src/a.ts `/etc/passwd`\"}",
				`{"path":"\"/etc/passwd\""}`,
				`{"path":"'/etc/passwd'"}`,
				`{"path":"\"~/.ssh/id_rsa\""}`,
				`{"paths":"src/a.ts \"` + out + `\""}`,
				`{"paths":"src/a.ts '` + sibling + `'"}`,
				`{"paths":"src/a.ts \"C:secret.txt\""}`,
				`{"paths":"src/a.ts 'Temp:secret.txt'"}`,
				`{"paths":"src/a.ts \"%USERPROFILE%\\.aws\\credentials\""}`,
				`{"paths":"src/a.ts \"../outside/x.txt\""}`,
				`{"paths":"src/a.ts \"..\""}`,
				`{"paths":"src/a.ts \"` + esc(previewRoot("outside", "secret")) + `…`,
				// A path a run of punctuation leads, a short option's value and an invisible format
				// character before a path.
				`{"paths":"src/a.ts +/etc/passwd"}`,
				`{"paths":"src/a.ts #/etc/passwd"}`,
				`{"paths":"src/a.ts !/etc/passwd"}`,
				`{"paths":"src/a.ts >/etc/passwd"}`,
				`{"paths":"src/a.ts -I/etc/passwd"}`,
				`{"file":"src/a.ts` + "\u200b" + `/etc/passwd"}`,
				// A climb after an inner `=`, `@` or `:`, inert prefixes' among them, in a later piece
				// or a lone value, and a climb in either reading of a backslash.
				`{"paths":"src/a.ts --out=../../outside/x.txt"}`,
				`{"paths":"src/a.ts @../outside/x.txt"}`,
				`{"paths":"src/a.ts:../outside/x.txt"}`,
				`{"paths":"src/a.ts select:../outside/x.txt"}`,
				`{"paths":"src/a.ts sha256:../outside/x.txt"}`,
				`{"paths":"src/a.ts path:../outside/x.txt"}`,
				`{"paths":"src/a.ts=../../outside/x.txt"}`,
				`{"path":"--out=../outside/x.txt"}`,
				`{"path":"x=../outside/x.txt"}`,
				`{"paths":"src/a.ts ..\\..\\outside\\x.txt"}`,
				`{"paths":"src/a.ts .\\./outside/x.txt"}`,
				`{"path":"..\\outside\\x.txt"}`,
				// Fix round 2: a control character splits a list; a glued redirect or command separator
				// and a letter whose ANSI best fit is punctuation start a path anywhere in a piece.
				`{"paths":"src/a.ts\u0000/etc/passwd"}`,
				`{"paths":"src/a.ts\u0000~/.ssh/id_rsa"}`,
				`{"paths":"src/a.ts\u001f../../outside/x.txt"}`,
				`{"paths":"src/a.ts\u007f/etc/passwd"}`,
				`{"paths":"src/a.ts\u0001` + out + `"}`,
				`{"path":"` + inRoot("src", "a.ts") + `\u0000/etc/passwd"}`,
				`{"paths":"src/a.ts>/etc/passwd"}`,
				`{"paths":"src/a.ts>>/etc/passwd"}`,
				`{"paths":"src/a.ts2>` + out + `"}`,
				`{"paths":"src/a.ts&/etc/passwd"}`,
				`{"paths":"src/a.ts&&/etc/passwd"}`,
				`{"paths":"src/a.ts>~/.ssh/id_rsa"}`,
				`{"paths":"src/a.ts>../../outside/x.txt"}`,
				`{"paths":"src/a.ts&../outside/x.txt"}`,
				`{"paths":"src/a.ts&$HOME/.aws/credentials"}`,
				`{"path":"` + inRoot("src", "a.ts") + `>/etc/passwd"}`,
				`{"paths":"src/a.ts` + "\u02ba" + `/etc/passwd"}`,
				`{"paths":"src/a.ts` + "\u01c0" + `/etc/passwd"}`,
				`{"paths":"src/a.ts` + "\u01c3" + `/etc/passwd"}`,
				`{"paths":"src/a.ts` + "\u02bc" + `~/.ssh/id_rsa"}`,
				`{"paths":"src/a.ts` + "\u0300" + `../outside/x.txt"}`,
				`{"paths":"src/a.ts\u02ba/etc/passwd"}`,
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
				// The names a project's paths hold, a quoted or bracketed project path, the root's own
				// spelling after a quote, a parenthesis or an option's `=`, and a climb that stays in.
				`{"file":"app/(auth)/login/page.tsx"}`,
				`{"file":"pages/[slug].tsx"}`,
				`{"paths":["app/api/users/[id]/route.ts","src/routes/+page.svelte"]}`,
				`{"file":"lib/c++/vector.h"}`,
				`{"file":"docs/C#/intro.md"}`,
				`{"file":"docs/it's here.md"}`,
				`{"file":"data/export (1).csv"}`,
				`{"file":"assets/logo@2x.png"}`,
				`{"file":"src/a.ts#L10"}`,
				`{"paths":"\"src/a.ts\" \"src/b.ts\""}`,
				`{"paths":"'docs/design notes.md' (src/a.ts)"}`,
				`{"notebook_path":"\"` + inRoot("nb", "my notes.ipynb") + `\""}`,
				`{"paths":"src/a.ts (` + inRoot("docs", "b.md") + `)"}`,
				`{"paths":"src/a.ts --out=` + inRoot("docs", "b.md") + `"}`,
				`{"paths":"src/a.ts --out=docs/b.md"}`,
				`{"file":"src/../src/a.ts"}`,
				`{"file":"src\\a.ts"}`,
				// Fix round 2: an ampersand, an okina and a list of project paths a control character
				// splits.
				`{"file":"docs/R&D/plan.md"}`,
				`{"file":"notes/Q&A (draft).md"}`,
				`{"file":"docs/Hawai` + "\u02bb" + `i/notes.md"}`,
				`{"paths":"src/a.ts\u0000src/b.ts"}`,
				`{"paths":"src/a.ts\u001f` + inRoot("docs", "b.md") + `"}`,
			}
			leaks := []string{"passwd", "id_rsa", "credentials", "secret.txt", "Vendor", "../outside", `..\\outside`, "./outside"}
			for _, p := range []string{filepath.Join("outside", "x.txt"), filepath.Join("other", "x.txt"), filepath.Join("outside", "secret")} {
				leaks = append(leaks, p, esc(p))
			}
			for _, h := range []struct {
				name string
				hp   HostPaths
			}{{"host rules", denyFiles(root, "private/deny.txt")}, {"no host", nil}} {
				for _, s := range withheld {
					require.Equal(t, "withheld", summaryVerdict(t, root, h.hp, s, leaks), "%s: %q holds a path outside the project", h.name, s)
				}
				for _, s := range shown {
					require.Equal(t, "shown", summaryVerdict(t, root, h.hp, s, leaks), "%s: %q names only project paths", h.name, s)
				}
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
// most dropJudgementsBound of them by the host, first those a summary or a reason names, then in the
// order the checkpoint lists them, and withholds every later one unjudged, by hash or as a withheld
// path, still accounted for, and learned as withheld whatever its spelling, so a text that names it,
// even by a part of its path, is withheld (fail closed; wave 22's verify). With no Read rule in force
// the host's answer costs nothing and every drop is judged and shown as before.
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
		checkpoint.ToolPointer{ToolUseID: "toolu_part", Hash: hashOf("part"), Summary: `{"query":"path:file99"}`},
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
		// then the drops up to the bound: first the two a summary names (key999.txt, which the host
		// refuses, and file0.go), then in checkpoint order the denied one and the next 61 of the thousand.
		require.Equal(t, 1+dropJudgementsBound, calls, "a build judges a bounded number of path-keyed drops")
		requireNoLeak(t, res, []string{"deny.txt", "file999.go", "file63.go", "key999.txt", "secrets/"})
		_, ok := dropForKind(res.Dropped, "file_pointer", "pkg/sub11/file61.go")
		require.True(t, ok, "a drop the host judged and allows is shown as recorded")
		_, ok = dropForKind(res.Dropped, "file_pointer", "pkg/sub12/file62.go")
		require.False(t, ok, "the next drop is past the bound, withheld unjudged")
		require.Equal(t, 2+(n-(dropJudgementsBound-2)), withheldDrops(res),
			"the denied drop, the refused key999.txt and every drop past the bound are withheld, and still accounted for")
		require.Contains(t, section6(res), "- tool_use toolu_literal "+hashOf("literal").String()+" — "+withheldSummary+"\n",
			"a drop a summary names is judged first, and the refused one is learned, so its basename is withheld")
		require.Contains(t, section6(res), "- tool_use toolu_part "+hashOf("part").String()+" — "+withheldSummary+"\n",
			"a drop past the bound is learned as withheld whatever its spelling: a selector of a part of its path is withheld")
		require.Contains(t, section6(res), within, "a selector naming a judged, allowed drop is shown")
	})
	t.Run("no Read rules", func(t *testing.T) {
		res, calls := build(t, func() HostRules { return HostRules{Refuses: func(string) bool { return false }} })
		require.Equal(t, 1+n+2, calls, "with no rule in force every drop is judged, at no cost")
		require.Zero(t, withheldDrops(res), "and none is withheld")
		require.Contains(t, section6(res), within)
		require.Contains(t, section6(res), "- tool_use toolu_part "+hashOf("part").String()+` — {"query":"path:file99"}`+"\n")
	})
}

// TestBuild_ADropPastTheJudgementBoundIsWithheldWhereverItIsNamed is wave 22's verify of finding 28:
// a path-keyed drop past the bound was withheld in sections 6 and 7 but learned as a withheld path
// only when its spelling held a rule's literal, so a drop the host refuses only through a link, a
// junction or an 8.3 name (lnk/token.txt, lnk a link to secrets, under Read(./secrets/**)) went
// unlearned after 64 fresh drop paths, and every text that named it was shown: a free text, a
// selector by basename or by a part of its path, a glob that selects it, a cut summary that ends in
// the start of its path, and a drop reason. eca33155 judged every drop and withheld them all. A drop
// past the bound is learned as withheld whatever its spelling, so each is withheld again, and the
// drops a text names are judged first, so a named drop the host allows is shown, in section 6 and in
// section 7. The host's judgements stay bounded: a thousand drops cost what seventy do.
func TestBuild_ADropPastTheJudgementBoundIsWithheldWhereverItIsNamed(t *testing.T) {
	root := previewRoot("proj")
	hp := func(calls *int) HostPaths {
		return func() HostRules {
			return HostRules{Patterns: []string{"./secrets/**"}, Refuses: func(p string) bool {
				*calls++
				if !filepath.IsAbs(p) {
					p = filepath.Join(root, filepath.FromSlash(p))
				}
				rel, err := filepath.Rel(root, filepath.Clean(p))
				if err != nil {
					return false
				}
				// The host resolves the link lnk -> secrets, as hostperm does.
				rel = filepath.ToSlash(rel)
				return strings.HasPrefix(rel, "secrets/") || strings.HasPrefix(rel, "lnk/")
			}}
		}
	}
	withheld := []string{
		"cat lnk/token.txt",
		`{"query":"path:token.txt"}`,
		`{"query":"path:lnk/tok"}`,
		"**/tok*.txt",
		"cat lnk/tok…",
	}
	const named = "cat pkg/f69.go"
	reason := checkpoint.DropEntry{
		Kind: "pointer_git_unavailable", Detail: "checkpoint: git index unsupported: open lnk/token.txt: Access is denied.",
	}
	build := func(t *testing.T, fillers int) (Result, int) {
		cp := ckUAT05()
		cp.Dropped = nil
		for i := 0; i < fillers; i++ {
			cp.Dropped = append(cp.Dropped, checkpoint.DropEntry{
				Kind: "file_pointer", ID: fmt.Sprintf("pkg/f%d.go", i), Detail: "truncated at budget; re_read(path) still resolves",
			})
		}
		cp.Dropped = append(cp.Dropped,
			checkpoint.DropEntry{Kind: "file_pointer", ID: "lnk/token.txt", Detail: "truncated at budget; re_read(path) still resolves"},
			reason)
		cp.Pointers.Tools = nil
		for i, s := range append(append([]string(nil), withheld...), named) {
			cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
				ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_w22_%02d", i)), Hash: hashOf(s), Summary: s,
			})
		}
		calls := 0
		d := uat05Deps(t, cp)
		d.HostPaths = hp(&calls)
		r := requestFor(t, cp, maxBudget())
		r.ProjectRoot = root
		res, err := Build(context.Background(), r, d)
		require.NoError(t, err)
		section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
		for i, s := range withheld {
			line := "- tool_use " + fmt.Sprintf("toolu_w22_%02d", i) + " " + hashOf(s).String() + " — "
			require.Contains(t, section6, line+withheldSummary+"\n", "%d fillers: %q names the refused drop", fillers, s)
		}
		requireNoLeak(t, res, []string{"lnk/token.txt", "token.txt"})
		if fillers > 69 {
			line := "- tool_use " + fmt.Sprintf("toolu_w22_%02d", len(withheld)) + " " + hashOf(named).String() + " — "
			require.Contains(t, section6, line+named+"\n", "%d fillers: a named drop the host allows is judged and shown", fillers)
			_, ok := dropForKind(res.Dropped, "file_pointer", "pkg/f69.go")
			require.True(t, ok, "%d fillers: section 7 names the judged, allowed drop as recorded", fillers)
		}
		return res, calls
	}
	_, calls0 := build(t, 0)
	_, calls70 := build(t, 70)
	_, calls1000 := build(t, 1000)
	require.Equal(t, calls70, calls1000, "past the bound, more drops cost no more host judgements")
	require.Less(t, calls70, calls0+70, "the bound holds with seventy drops")
}

// TestBuild_ARuleAndASkillWhoseDropsLiePastTheBoundAreStillRestored is wave 22's verify, fix round
// 2, of finding 28: a path-keyed drop past the judgement bound was answered as refused without a host
// judgement, and that answer went into the build's memo of host judgements, which every later
// judgement of the same key reads. Item 6a judges a rule file, and item 6b a skill's file, by the
// project-relative key a file pointer's drop carries, so in a session that had once read a nested
// CLAUDE.md, a .claude/rules file or a SKILL.md whose pointer the checkpoint's budget cut past the
// bound, under any Read rule, the rule was not restored and the skill not indexed, and no drop entry
// named either. eca33155 judged every drop by the host and restored both. The unjudged answer is the
// drops' own (section 7 and dropped() withhold such a drop); items 6a and 6b ask the host about their
// files, so a file it allows is restored and indexed and one it refuses is not, however many drops
// come before, and past the bound more drops still cost no more host judgements.
func TestBuild_ARuleAndASkillWhoseDropsLiePastTheBoundAreStillRestored(t *testing.T) {
	const (
		truncated = "truncated at budget; re_read(path) still resolves"
		rule      = ".claude/rules/zzrule.md"
		nested    = "pkg/sub/CLAUDE.md"
		skill     = ".claude/skills/zzskill/SKILL.md"
	)
	root := previewRoot("proj")
	bodies := []string{"ZZRULEBODY", "ZZNESTEDBODY", "ZZSKILLDESC"}
	for _, refused := range []bool{false, true} {
		calls := map[int]int{}
		for _, fillers := range []int{0, 10, 70, 200, 1000} {
			cp := ckUAT05()
			cp.Dropped = nil
			for i := 0; i < fillers; i++ {
				cp.Dropped = append(cp.Dropped, checkpoint.DropEntry{Kind: "file_pointer", ID: fmt.Sprintf("pkg/f%d.go", i), Detail: truncated})
			}
			for _, id := range []string{rule, nested, skill} {
				cp.Dropped = append(cp.Dropped, checkpoint.DropEntry{Kind: "file_pointer", ID: id, Detail: truncated})
			}
			denied := []string{".env"}
			if refused {
				denied = append(denied, rule, nested, skill)
			}
			hp := denyFiles(root, denied...)
			n := 0
			d := uat05Deps(t, cp)
			d.HostPaths = func() HostRules {
				h := hp()
				refuses := h.Refuses
				h.Refuses = func(p string) bool { n++; return refuses(p) }
				return h
			}
			d.Rules = &fakeScanner{
				pathScoped: []rules.Rule{{Path: rule, Globs: []string{"**"}, Body: bodies[0]}},
				nested:     []rules.Rule{{Path: nested, Body: bodies[1]}},
			}
			sk := skills.Entry{Name: "zzskill", Description: bodies[2], Source: skill}
			d.Skills = &fakeIndexer{all: []skills.Entry{sk}, kept: []skills.Entry{sk}}
			r := requestFor(t, cp, maxBudget())
			r.ProjectRoot = root
			res, err := Build(context.Background(), r, d)
			require.NoError(t, err)
			for _, b := range bodies {
				if refused {
					require.NotContains(t, res.Text, b, "%d fillers: the host refuses the file, so it is neither restored nor indexed", fillers)
				} else {
					require.Contains(t, res.Text, b, "%d fillers: the host allows the file, so it is restored or indexed", fillers)
				}
			}
			calls[fillers] = n
		}
		require.Equal(t, calls[70], calls[1000], "refused=%v: past the bound, more drops cost no more host judgements", refused)
		require.Less(t, calls[70], calls[0]+70, "refused=%v: the bound holds with seventy drops", refused)
	}
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
