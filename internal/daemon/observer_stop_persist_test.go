package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// TestStop_PersistsTheObserversGraphAndStateWithoutASessionEnd: the observer keeps the dependence
// graph's new records and its per-session state in memory, and only its SessionEnd and its idle
// Persist task write them out (dag.Graph.Flush's own doc: the daemon flushes "from its idle loop and
// at shutdown"). Stop did neither. A Stop that meets a session whose end has not run — since C1.15 the
// end runs on its own goroutine after the flush hook has answered, and Stop cancels it once its grace
// is over — therefore lost every graph record since the last flush, and the next daemon cannot get
// them back: it replays the unacknowledged flush, but the session's tool deliveries are already on
// the committed frontier, and a redelivery never recomputes a first run's graph (observer step 6c).
// Under -race and CPU co-load on Linux, TestE2E_ThinSliceDropsControlOnlyEdges met exactly that: the
// shutdown right after its flush hook cut the end ("a session end ignored cancellation", "SessionEnd
// failed: context canceled"), and the persisted graph held 0 nodes for 48 indexed tool uses. Here two
// tool uses are recorded with no SessionEnd, the daemon stops, and the graph and the state must be on
// disk.
func TestStop_PersistsTheObserversGraphAndStateWithoutASessionEnd(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	const sess core.SessionID = "sess-stop-persists"
	ctx := context.Background()

	read := hookio.Event{
		HookEventName: "PostToolUse", SessionID: sess, CWD: root, ToolName: "Read",
		ToolUseID: "toolu_stop_read", ToolInput: json.RawMessage(`{"file_path":"src/a.go"}`),
		ToolResponse: json.RawMessage(`{"content":"package a\n"}`),
	}
	bash := hookio.Event{
		HookEventName: "PostToolUse", SessionID: sess, CWD: root, ToolName: "Bash",
		ToolUseID: "toolu_stop_bash", ToolInput: json.RawMessage(`{"command":"echo hi"}`),
		ToolResponse: json.RawMessage(`{"stdout":"hi\n"}`),
	}
	require.NoError(t, dd.svc.ObserveTool(ctx, read))
	require.NoError(t, dd.svc.ObserveTool(ctx, bash))
	st := o.Graph.Stats()
	require.Positive(t, st.PendingRecords, "fixture: the tool uses' graph records are in memory only")
	require.Zero(t, st.LogRecords, "fixture: nothing has flushed the graph yet")

	require.NoError(t, dd.Stop(ctx))

	g, err := dag.Open(root, testConfig(), logging.Nop())
	require.NoError(t, err)
	for _, id := range []core.ToolUseID{read.ToolUseID, bash.ToolUseID} {
		_, ok := g.Node(dag.ToolUseNode(id))
		require.True(t, ok, "Stop must write the observer's graph out: %s's node is not in dag/deps.jsonl", id)
	}
	controlOnly := false
	for _, e := range g.In(dag.ToolUseNode(bash.ToolUseID)) {
		controlOnly = controlOnly || e.Kind == dag.EdgeControlOnly
	}
	require.True(t, controlOnly, "the Bash call shares nothing with the Read before it, so its link is control-only")

	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, "observer.json")))
	require.NoError(t, err, "Stop must write the observer's session state out")
	var state struct {
		Sessions map[string]struct {
			LastToolUseID string `json:"last_tool_use_id"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(raw, &state))
	require.Contains(t, state.Sessions, string(sess), "the session its end never closed stays on record for the replay")
	require.Equal(t, string(bash.ToolUseID), state.Sessions[string(sess)].LastToolUseID,
		"the replayed end resumes from the session as it stood, its last tool use included")
}
