//go:build windows

package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hostperm"
)

const (
	snLong = "configuration/credentials.secret"
	snBody = "archived marker SHORTNAME-ARCHIVE-5d20e7a4 for the rotation window\n"
	snMark = "SHORTNAME-ARCHIVE-5d20e7a4"
)

// shortPath returns p's 8.3 spelling as the volume records it.
func shortPath(t *testing.T, p string) string {
	t.Helper()
	u, err := syscall.UTF16PtrFromString(p)
	require.NoError(t, err)
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n, err := syscall.GetShortPathName(u, &buf[0], uint32(len(buf)))
	require.NoError(t, err, "GetShortPathName(%s)", p)
	return syscall.UTF16ToString(buf[:n])
}

// posixOf spells an absolute Windows path the way a `//` rule names it: C:\x is /c/x.
func posixOf(p string) string {
	return "/" + strings.ToLower(p[:1]) + filepath.ToSlash(p[2:])
}

// requireShortRefused asserts a refusal that serves no byte of the short-name fixture's secret.
func requireShortRefused(t *testing.T, text string, want deniedBody) {
	t.Helper()
	require.NotContains(t, text, snMark, "archived content was served: %s", text)
	require.NotContains(t, text, "credentials", "a refusal must not echo the path")
	var d deniedBody
	require.NoError(t, json.Unmarshal([]byte(text), &d), text)
	require.Equal(t, want, d, text)
}

// TestHostPolicy_AShortNameSpellingIsRefused is review finding 1 of C1.9 on its 8.3 axis: an 8.3
// spelling of a denied file resolves, through paths.Norm, to the file's real history, so the host
// check has to judge the name that is served and not only the name that was asked for. It covers
// the caller's spelling (re_read), a record captured under a short spelling (expand, recall,
// dropped) and a project root spelled with 8.3 names.
func TestHostPolicy_AShortNameSpellingIsRefused(t *testing.T) {
	f := newHPFixture(t,
		withConfig(func(c *config.Config) { c.Retrieval.EphemeralResults = false }),
		withFiles(map[string]string{snLong: snBody}))
	full := filepath.Join(f.Root, filepath.FromSlash(snLong))
	shortFull, shortRoot := shortPath(t, full), shortPath(t, f.Root)
	rel := filepath.ToSlash(strings.TrimPrefix(shortFull, shortRoot+`\`))
	if strings.EqualFold(rel, snLong) {
		t.Skip("precondition: this volume records no 8.3 names, so there is no short spelling to test")
	}
	f.putAndRecord(t, "Read", snLong, snBody, 4)
	_, shortID := f.putAndRecord(t, "Read", rel, snBody, 5)
	at := core.NowMilli(f.Clock).Time().Add(time.Hour).Format(time.RFC3339)

	control := responseText(f.call(t, ToolReRead, map[string]any{"path": rel, "full": true}))
	require.Contains(t, control, snMark, "control: the short spelling serves before a rule exists")

	f.writeSettings(t, projectSettings, denyRules("Read(./"+snLong+")"))
	forms := map[string]struct {
		tool string
		args map[string]any
	}{
		"re_read/latest":         {ToolReRead, map[string]any{"path": rel, "full": true}},
		"re_read/at_turn":        {ToolReRead, map[string]any{"path": rel, "at": "turn:4", "full": true}},
		"re_read/at_timestamp":   {ToolReRead, map[string]any{"path": rel, "at": at, "full": true}},
		"re_read/absolute_short": {ToolReRead, map[string]any{"path": shortFull, "full": true}},
		"expand/short_record":    {ToolExpand, map[string]any{"tool_use_id": string(shortID), "full": true}},
	}
	for name, form := range forms {
		t.Run(name, func(t *testing.T) {
			requireShortRefused(t, responseText(f.call(t, form.tool, form.args)), denied(hostDeniedReason))
		})
	}

	t.Run("recall withholds the short-spelled hit", func(t *testing.T) {
		text := responseText(f.callOK(t, ToolRecall, map[string]any{"query": "rotation window", "k": 10}, nil))
		require.NotContains(t, text, snMark)
		require.NotContains(t, text, rel)
	})

	t.Run("dropped withholds pointers spelled short", func(t *testing.T) {
		f.Drops.Entries = []checkpoint.DropEntry{
			{Kind: "file_pointer", ID: rel, Detail: "truncated at budget; re_read(path) still resolves"},
			{Kind: "tool_pointer", ID: string(shortID), Detail: "truncated at budget; expand(hash) still resolves"},
			{Kind: "file_pointer", ID: hpOK, Detail: "truncated at budget; re_read(path) still resolves"},
		}
		var got droppedBody
		f.callOK(t, ToolDropped, map[string]any{}, &got)
		require.Equal(t, f.Drops.Entries[2:], got.Drops)
		require.Equal(t, 2, got.Denied)
	})

	// A project root spelled with 8.3 names: an absolute rule names the long path, and a
	// project-relative rule is measured from the short root while the caller names the long file.
	for name, tc := range map[string]struct{ rule, path string }{
		"absolute rule, relative path": {"Read(/" + posixOf(full) + ")", snLong},
		"relative rule, long absolute": {"Read(./" + snLong + ")", full},
	} {
		t.Run("short project root/"+name, func(t *testing.T) {
			f.writeSettings(t, projectSettings, denyRules(tc.rule))
			d := f.Deps
			d.ProjectRoot = shortRoot
			d.HostPolicy = hostperm.New(hostperm.Options{
				ProjectRoot: shortRoot, Home: f.Home,
				Getenv:  func(string) string { return "" },
				Managed: &hostperm.ManagedSources{Dirs: []string{f.Managed}},
			})
			h := newHandlers(d)
			raw, err := json.Marshal(map[string]any{"path": tc.path, "full": true})
			require.NoError(t, err)
			resp, err := h.reRead(h.withHostSnapshot(context.Background()), Request{}, raw)
			require.NoError(t, err)
			requireShortRefused(t, responseText(resp), denied(hostDeniedReason))
		})
	}
}
