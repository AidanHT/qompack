package rehydrate

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// Privacy (owner decision D50, C4.6; UAT-12 F4 in plans/sdd/V6-closeout/live/report-c4.md). Section 6
// of the candidate 4 block listed the deny-ruled file's path and the out-of-project file's absolute
// path, although re_read refuses the first and withholds the second. A pointer never shows a path the
// host currently denies (the rules re_read and expand apply, through Deps.HostPaths) or a path outside
// the project; it points by hash only, and so does the drop entry that restores it.

// privacyRoot is the project root the privacy rows build under: a real absolute path on every
// platform, so "outside the project" is decided the way the daemon decides it.
func privacyRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "proj")
}

// denyPrivate stands in for a host permissions.deny Read rule on private/**, Read(./private/**): it
// refuses every spelling of a path under <root>/private, relative or absolute, and hands the build
// the rule's pattern.
func denyPrivate(root string) HostPaths {
	return func() HostRules {
		return HostRules{Patterns: []string{"./private/**"}, Refuses: func(p string) bool {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, filepath.FromSlash(p))
			}
			rel, err := filepath.Rel(filepath.Join(root, "private"), p)
			return err == nil && !strings.HasPrefix(rel, "..")
		}}
	}
}

func hashOf(s string) core.Hash { return core.Hash(sha256.Sum256([]byte(s))) }

// privacyCheckpoint carries one pointer of every shape F4 saw, plus an allowed one of each.
func privacyCheckpoint(root string) (checkpoint.Checkpoint, []string) {
	outside := filepath.Join(filepath.Dir(root), "outside", "outside.txt")
	deniedAbs := filepath.Join(root, "private", "deny.txt")
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"},
		{Path: outside, Hash: hashOf("outside"), Why: "referenced"},
		{Path: "../outside/escape.txt", Hash: hashOf("escape"), Why: "referenced"},
		{Path: "reports.py", Hash: hashOf("reports"), Why: "referenced"},
	}
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_deniedabs", Hash: hashOf("deny"), Summary: deniedAbs},
		{ToolUseID: "toolu_outside", Hash: hashOf("outside"), Summary: outside},
		{ToolUseID: "toolu_deniedrel", Hash: hashOf("deny2"), Summary: "private/deny.txt"},
		{ToolUseID: "toolu_bash", Hash: hashOf("bash"), Summary: "cat private/deny.txt"},
		{
			ToolUseID: "toolu_json", Hash: hashOf("json"),
			Summary: `{"file_path":"` + strings.ReplaceAll(outside, `\`, `\\`) + `"}`,
		},
		{ToolUseID: "toolu_ok", Hash: hashOf("ok"), Summary: "cat data/meta.txt"},
	}
	// The spellings that must never reach the payload or the drop report.
	leaks := []string{
		"private/deny.txt", "private" + string(filepath.Separator) + "deny.txt", outside,
		strings.ReplaceAll(outside, `\`, `\\`), "outside.txt", "escape.txt",
	}
	return cp, leaks
}

func requireNoLeak(t *testing.T, res Result, leaks []string) {
	t.Helper()
	for _, l := range leaks {
		require.NotContains(t, res.Text, l, "the payload shows a withheld path")
		for _, e := range res.Dropped {
			require.NotContains(t, e.ID+" "+e.Detail, l, "the drop report shows a withheld path: %+v", e)
		}
	}
}

func TestBuild_PointersNeverShowAWithheldPath(t *testing.T) {
	root := privacyRoot(t)
	cp, leaks := privacyCheckpoint(root)
	d := uat05Deps(t, cp)
	d.HostPaths = denyPrivate(root)
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	requireNoLeak(t, res, leaks)

	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	for _, h := range []string{"deny", "outside", "escape"} {
		require.Contains(t, section6, hashOf(h).String(), "a withheld file pointer still points by hash")
	}
	for _, id := range []string{"toolu_deniedabs", "toolu_outside", "toolu_deniedrel", "toolu_bash", "toolu_json"} {
		require.Contains(t, section6, "- tool_use "+id+" ", "a withheld tool pointer still points by id and hash")
	}
	require.Contains(t, section6, "- reports.py "+hashOf("reports").String(), "an allowed path is still shown")
	require.Contains(t, section6, "cat data/meta.txt", "an allowed summary is still shown")
}

// TestBuild_DroppedPointersNeverShowAWithheldPath: the same pointers at a budget that drops them,
// so each is named in section 7 and in dropped() — by hash, never by the withheld path.
func TestBuild_DroppedPointersNeverShowAWithheldPath(t *testing.T) {
	root := privacyRoot(t)
	cp, leaks := privacyCheckpoint(root)
	d := uat05Deps(t, cp)
	d.HostPaths = denyPrivate(root)
	r := requestFor(t, cp, core.Tokens(400))
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	var named int
	for _, e := range res.Dropped {
		if e.Kind == dropKindPointer {
			named++
		}
	}
	require.Positive(t, named, "fixture sanity: pointers were dropped: %v", res.Dropped)
	requireNoLeak(t, res, leaks)
	_, ok := dropForKind(res.Dropped, dropKindPointer, hashOf("deny").String())
	require.True(t, ok, "a dropped withheld file pointer is named by its hash: %v", res.Dropped)
}

// TestBuild_UnavailableHostRulesWithholdEveryPath is re_read's fail-closed rule: when the host's
// rules cannot be established, every path-bearing pointer is withheld.
func TestBuild_UnavailableHostRulesWithholdEveryPath(t *testing.T) {
	root := privacyRoot(t)
	cp, _ := privacyCheckpoint(root)
	d := uat05Deps(t, cp)
	d.HostPaths = func() HostRules { return HostRules{} }
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	require.NotContains(t, section6, "reports.py")
	require.NotContains(t, section6, "data/meta.txt")
	require.Contains(t, section6, hashOf("reports").String())
}

// TestBuild_PointersNeverShowAHomeOrVariablePath: a path spelled from the home directory or an
// environment variable is outside the project whatever it expands to, so it is withheld like an
// absolute one (w15-rehydrate review). It is neither rooted nor "..", so containment read it as
// project-relative, and the daemon's host adapter then joined it under the project root, where a
// Read deny rule on ~/.ssh/** never matches it.
func TestBuild_PointersNeverShowAHomeOrVariablePath(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "~/.ssh/id_rsa", Hash: hashOf("tilde"), Why: "referenced"},
		{Path: "~admin/.netrc", Hash: hashOf("tildeuser"), Why: "referenced"},
		{Path: "$HOME/.aws/credentials", Hash: hashOf("home"), Why: "referenced"},
		{Path: "${XDG_CONFIG_HOME}/gh/hosts.yml", Hash: hashOf("xdg"), Why: "referenced"},
		{Path: `%USERPROFILE%\.aws\credentials`, Hash: hashOf("profile"), Why: "referenced"},
		{Path: "reports.py", Hash: hashOf("reports"), Why: "referenced"},
	}
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{ToolUseID: "toolu_tilde", Hash: hashOf("t1"), Summary: "cat ~/.ssh/id_rsa"},
		{ToolUseID: "toolu_home", Hash: hashOf("t2"), Summary: "cat $HOME/.aws/credentials"},
		{ToolUseID: "toolu_brace", Hash: hashOf("t3"), Summary: "cat ${HOME}/.aws/credentials"},
		{ToolUseID: "toolu_profile", Hash: hashOf("t4"), Summary: `type %USERPROFILE%\.aws\credentials`},
		{ToolUseID: "toolu_pf", Hash: hashOf("t5"), Summary: `type "%ProgramFiles(x86)%\vault\app.cfg"`},
		{ToolUseID: "toolu_flag", Hash: hashOf("t6"), Summary: "ssh -i~/.ssh/deploy_key host"},
		{ToolUseID: "toolu_ok", Hash: hashOf("ok"), Summary: "cat data/meta.txt"},
	}
	leaks := []string{"id_rsa", ".netrc", "credentials", "hosts.yml", "vault", "deploy_key"}
	d := uat05Deps(t, cp)
	d.HostPaths = func() HostRules { return HostRules{Refuses: func(string) bool { return false }} }
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireNoLeak(t, res, leaks)
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	for _, h := range []string{"tilde", "tildeuser", "home", "xdg", "profile"} {
		require.Contains(t, section6, hashOf(h).String(), "a withheld file pointer still points by hash")
	}
	for _, id := range []string{"toolu_tilde", "toolu_home", "toolu_brace", "toolu_profile", "toolu_pf", "toolu_flag"} {
		require.Contains(t, section6, "- tool_use "+id+" ", "a withheld tool pointer still points by id and hash")
	}
	require.Contains(t, section6, "- reports.py "+hashOf("reports").String(), "an allowed path is still shown")
	require.Contains(t, section6, "cat data/meta.txt", "an allowed summary is still shown")
}

// TestAbsLike_HomeAndVariableSpellingsAreRooted pins homeOrVarRoot's edges: the home and variable
// spellings count as rooted, and project-relative names that merely contain `~` or `$` do not.
func TestAbsLike_HomeAndVariableSpellingsAreRooted(t *testing.T) {
	for _, p := range []string{
		"~", "~/.ssh/id_rsa", `~\.ssh\id_rsa`, "~admin/.netrc", "$HOME/.aws/credentials",
		"${HOME}/.aws/credentials", "$XDG_CONFIG_HOME", `%USERPROFILE%\.aws\credentials`,
		`%ProgramFiles(x86)%\app\cfg`,
	} {
		require.True(t, absLike(p), "%q must count as rooted", p)
	}
	for _, p := range []string{"~$report.docx", "notes~/draft.md", "$1", "a$HOME/b", "100%/x", "reports.py"} {
		require.False(t, absLike(p), "%q is project-relative", p)
	}
}

// TestBuild_CheckpointDropsNeverShowAWithheldPath: the checkpointer keys five drop kinds by a file
// pointer's path — a pointer cut at the checkpoint's own budget (truncate.go cutFilePointers) and
// ValidatePointers' missing, invalid, untracked and dirty pointers — and section 7 rendered them
// verbatim, so a denied or out-of-project path section 6 withholds reached the payload one section
// later (w19-rehydrate review). A gitignored, host-denied file is the likely live shape: finalize
// always validates, and keeps an untracked pointer. Each is named the way section 6 names it, by
// the pointer's hash while the checkpoint still holds it, and never with a re_read(path) call.
func TestBuild_CheckpointDropsNeverShowAWithheldPath(t *testing.T) {
	root := privacyRoot(t)
	outside := filepath.Join(filepath.Dir(root), "outside", "outside.txt")
	cp := ckUAT05()
	cp.Pointers.Files = append(cp.Pointers.Files,
		checkpoint.FilePointer{Path: "private/kept.env", Hash: hashOf("kept"), Why: "referenced"})
	withheld := []checkpoint.DropEntry{
		{Kind: "file_pointer", ID: "private/deny.txt", Detail: "truncated at budget; re_read(path) still resolves"},
		{Kind: "pointer_invalid", ID: outside, Detail: "path escapes the project root"},
		{Kind: "pointer_missing", ID: "private/gone.txt", Detail: "file no longer exists in the working tree"},
		{Kind: "pointer_untracked", ID: "private/kept.env", Detail: "not tracked by git"},
		{Kind: "pointer_dirty", ID: "private/dirty.txt", Detail: "modified since index on main"},
	}
	allowed := checkpoint.DropEntry{
		Kind: "file_pointer", ID: "docs/guide.md", Detail: "truncated at budget; re_read(path) still resolves",
	}
	cp.Dropped = append(append(append([]checkpoint.DropEntry(nil), cp.Dropped...), withheld...), allowed)

	d := uat05Deps(t, cp)
	d.HostPaths = denyPrivate(root)
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireNoLeak(t, res, []string{
		"private/", "deny.txt", "gone.txt", "kept.env", "dirty.txt", outside, "outside.txt",
		strings.ReplaceAll(outside, `\`, `\\`),
	})

	section7 := sectionBody(res.Text, sectionHeading(ItemDropReport))
	for _, e := range withheld {
		require.Contains(t, section7, "- "+e.Kind+" ", "the drop is still named by its kind: %s", e.Kind)
	}
	require.Contains(t, section7, "- pointer_untracked "+hashOf("kept").String()+" — not tracked by git",
		"a pointer the checkpoint still holds is named by its hash, as section 6 names it")
	require.Contains(t, section7, "restore: expand(hash="+hashOf("kept").String()+")")
	require.NotContains(t, section7, "- file_pointer "+withheldDropID+" — truncated at budget; re_read",
		"a withheld path's drop never offers re_read(path), which refuses it")
	require.Contains(t, section7, dropLine(allowed), "a path the payload may show is shown as recorded")

	var named int
	for _, e := range res.Dropped {
		if e.ID == withheldDropID || e.ID == hashOf("kept").String() && e.Kind == "pointer_untracked" {
			named++
		}
	}
	require.Equal(t, len(withheld), named, "every withheld drop is still accounted for: %v", res.Dropped)
}

// TestCheckpointPathDrops_AreTheCheckpointersOwn pins checkpointPathDrops to what internal/checkpoint
// mints: every drop the real ValidatePointers and Truncate key by a file pointer's path carries a
// kind the gate knows, so a renamed or added kind cannot slip a path past section 7 unjudged.
func TestCheckpointPathDrops_AreTheCheckpointersOwn(t *testing.T) {
	root := t.TempDir()
	cp := ckUAT05()
	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "../escape.txt", Hash: hashOf("escape")},
		{Path: "gone.txt", Hash: hashOf("gone")},
		{Path: ".", Hash: hashOf("dir")},
	}
	validated, err := checkpoint.ValidatePointers(context.Background(), root, cp.Pointers)
	require.NoError(t, err)

	cp.Pointers.Files = []checkpoint.FilePointer{
		{Path: "a.txt", Hash: hashOf("a"), Why: "referenced"},
		{Path: "b.txt", Hash: hashOf("b"), Why: "referenced"},
	}
	_, truncated := checkpoint.Truncate(cp, core.Tokens(1), config.Defaults().Checkpoint.Tiers,
		tokens.New(config.Defaults(), ""))

	keyed := map[string]bool{"../escape.txt": true, "gone.txt": true, ".": true, "a.txt": true, "b.txt": true}
	seen := map[string]bool{}
	for _, e := range append(validated, truncated...) {
		if keyed[e.ID] {
			seen[e.ID] = true
			require.True(t, checkpointPathDrops[e.Kind], "a drop keyed by a pointer's path has a kind the gate knows: %+v", e)
		}
	}
	require.Len(t, seen, len(keyed), "fixture sanity: every pointer was dropped by its path: %v %v", validated, truncated)
}

// TestBuild_HostRulesAreEstablishedOncePerBuild: section 6 and the checkpoint's drop entries are
// judged against one snapshot of the host's rules, so an unavailable policy is reported once.
func TestBuild_HostRulesAreEstablishedOncePerBuild(t *testing.T) {
	root := privacyRoot(t)
	cp, _ := privacyCheckpoint(root)
	cp.Dropped = append(cp.Dropped, checkpoint.DropEntry{Kind: "pointer_missing", ID: "private/gone.txt"})
	d := uat05Deps(t, cp)
	calls := 0
	deny := denyPrivate(root)
	d.HostPaths = func() HostRules { calls++; return deny() }
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	_, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

// TestBuild_APathKnownOnlyFromACheckpointDropIsNamedByNoSelector is the w19 verifier's V3. A
// denied path the checkpoint records only as a path-keyed drop (pointer_missing: the file is gone
// from the working tree; file_pointer: the checkpoint cut the pointer at its own budget) never
// entered the paths a selector or a glob is matched against, so `path:deny.txt` (recall's plain
// selector selects by path-segment suffix) and `path:**/deny.txt` were shown in section 6 while
// section 7 withheld the same path.
func TestBuild_APathKnownOnlyFromACheckpointDropIsNamedByNoSelector(t *testing.T) {
	root := privacyRoot(t)
	cp := ckUAT05()
	cp.Dropped = append(append([]checkpoint.DropEntry(nil), cp.Dropped...),
		checkpoint.DropEntry{Kind: "pointer_missing", ID: "private/deny.txt", Detail: "file no longer exists in the working tree"},
		checkpoint.DropEntry{Kind: "file_pointer", ID: "vault/keys.txt", Detail: "truncated at budget; re_read(path) still resolves"},
	)
	withheld := []checkpoint.ToolPointer{
		{ToolUseID: "toolu_basename", Hash: hashOf("b1"), Summary: `{"query":"path:deny.txt"}`},
		{ToolUseID: "toolu_globsel", Hash: hashOf("b2"), Summary: `{"query":"path:**/deny.txt"}`},
		{ToolUseID: "toolu_glob", Hash: hashOf("b3"), Summary: "**/deny.txt"},
		{ToolUseID: "toolu_cutbasename", Hash: hashOf("b4"), Summary: `{"query":"path:keys.txt"}`},
		{ToolUseID: "toolu_cutglob", Hash: hashOf("b5"), Summary: "**/keys.*"},
	}
	allowed := []checkpoint.ToolPointer{
		{ToolUseID: "toolu_ok_selector", Hash: hashOf("c1"), Summary: `{"query":"path:reports.py"}`},
		{ToolUseID: "toolu_ok_glob", Hash: hashOf("c2"), Summary: "**/*.py"},
	}
	cp.Pointers.Tools = append(append([]checkpoint.ToolPointer(nil), withheld...), allowed...)

	d := uat05Deps(t, cp)
	d.HostPaths = denyFiles(root, "private/deny.txt", "vault/keys.txt")
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	requireNoLeak(t, res, []string{"deny.txt", "keys.txt", "keys.*"})

	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	for _, tp := range withheld {
		require.Contains(t, section6, "- tool_use "+string(tp.ToolUseID)+" "+tp.Hash.String()+" — "+withheldSummary,
			"%s names a path section 7 withholds", tp.Summary)
	}
	for _, tp := range allowed {
		require.Contains(t, section6, "- tool_use "+string(tp.ToolUseID)+" "+tp.Hash.String()+" — "+tp.Summary)
	}
}
