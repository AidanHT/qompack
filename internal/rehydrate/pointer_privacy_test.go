package rehydrate

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
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

// denyPrivate stands in for a host permissions.deny Read rule on private/**: it refuses every
// spelling of a path under <root>/private, relative or absolute.
func denyPrivate(root string) func() func(string) bool {
	return func() func(string) bool {
		return func(p string) bool {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, filepath.FromSlash(p))
			}
			rel, err := filepath.Rel(filepath.Join(root, "private"), p)
			return err == nil && !strings.HasPrefix(rel, "..")
		}
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
	d.HostPaths = func() func(string) bool { return nil }
	r := requestFor(t, cp, maxBudget())
	r.ProjectRoot = root

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	section6 := sectionBody(res.Text, sectionHeading(ItemPointers))
	require.NotContains(t, section6, "reports.py")
	require.NotContains(t, section6, "data/meta.txt")
	require.Contains(t, section6, hashOf("reports").String())
}
