package daemon

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
)

// The rehydration's section 6 follows the host rules re_read follows (owner decision D50, C4.6,
// UAT-12 F4): the daemon hands rehydrate the host's current Read rules through Deps.HostPaths. These
// rows pin the adapter over a hermetic policy (mcpOpHostPolicy: no home, no environment, no managed
// source) with a project deny rule, and that WireRehydrator installs it.

func writeProjectSettings(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, "settings.json")), []byte(body), 0o600))
}

func TestRehydrateHostPaths_RefusesWhatTheHostDenies(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/**)"],"ask":["Read(./ask/**)"]}}`)

	refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())()
	require.NotNil(t, refuses)
	require.True(t, refuses("private/deny.txt"), "a deny rule refuses the relative spelling")
	require.True(t, refuses(filepath.Join(root, "private", "deny.txt")), "and the absolute one")
	require.True(t, refuses("ask/q.txt"), "an ask rule refuses too: an archived rehydration cannot ask")
	require.False(t, refuses("reports.py"))
}

func TestRehydrateHostPaths_UnreadableRulesFailClosed(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, `{"permissions":`)

	require.Nil(t, rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())(),
		"rules that cannot be established withhold every path, as re_read does")
}

func TestWireRehydrator_InstallsTheHostPathRules(t *testing.T) {
	root := t.TempDir()
	o := Options{ProjectRoot: root, Cfg: testConfig(), HostPolicy: mcpOpHostPolicy(t, root)}
	svc, ok := WireRehydrator(&o).(*rehydrateService)
	require.True(t, ok)
	require.NotNil(t, svc.o.Deps.HostPaths, "section 6 must be judged against the host's rules")
}

// TestRehydrateHostPaths_ASelectorNamingADeniedFileIsWithheld is UAT-12 F1 on candidate 7 through the
// adapter and the real host rules: the project deny rule the live run wrote, Read(./private/deny.txt),
// and the recall call whose path: selector named that file. Section 6 showed its argument summary,
// {"query":"path:private/deny.txt"}; it is withheld, and the pointer still points by id and hash.
func TestRehydrateHostPaths_ASelectorNamingADeniedFileIsWithheld(t *testing.T) {
	root := t.TempDir()
	writeProjectSettings(t, root, `{"permissions":{"deny":["Read(./private/deny.txt)"]}}`)

	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session = core.SessionID("2a4952b4-730f-41f8-954b-696d176baba8")
	cp.Seq = core.CheckpointSeq(3)
	cp.Pointers.Tools = []checkpoint.ToolPointer{
		{
			ToolUseID: "toolu_017m9djGav7vcngkniobgwim", Hash: core.Hash(sha256.Sum256([]byte("selector"))),
			Summary: `{"query":"path:private/deny.txt"}`,
		},
		{
			ToolUseID: "toolu_0151dDKt7HtDjJ4EY3WA5cbU", Hash: core.Hash(sha256.Sum256([]byte("marker"))),
			Summary: `{"query":"ORCHID-DENY-8842"}`,
		},
	}
	req := rehydrate.Request{
		Session: cp.Session, Source: "compact", ProjectRoot: root, Checkpoint: cp, Cfg: testConfig(),
		Ref: checkpoint.Ref{Seq: cp.Seq, Path: filepath.Join(root, ".qompack", "checkpoints", "0003.json")},
	}
	deps := rehydrate.Deps{HostPaths: rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())}

	res, err := rehydrate.Build(context.Background(), req, deps)
	require.NoError(t, err)
	require.NotContains(t, res.Text, "deny.txt", "the payload shows the denied path")
	require.Contains(t, res.Text, "- tool_use toolu_017m9djGav7vcngkniobgwim "+
		core.Hash(sha256.Sum256([]byte("selector"))).String()+" — summary withheld")
	require.Contains(t, res.Text, `{"query":"ORCHID-DENY-8842"}`, "a summary that names no path is shown")
}
