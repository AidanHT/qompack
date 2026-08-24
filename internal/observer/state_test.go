package observer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// stateHarness builds an observer rooted at root, so a second one can be built over the same
// state file and asked what it resumed.
func stateHarness(t *testing.T, root string) *harness {
	t.Helper()
	return newHarness(t, func(o *Options) { o.ProjectRoot = root })
}

// readStateFile returns the state file's raw bytes.
func readStateFile(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(stateFilePath(root)))
	require.NoError(t, err)
	return b
}

func TestState_RoundTripsThroughDisk(t *testing.T) {
	root := t.TempDir()
	first := stateHarness(t, root)

	first.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		readOf("toolu_2", "src/b.ts", "beta\n"),
	)
	st := first.state(testSession)
	st.mu.Lock()
	st.Turn, st.PrefixTokens = 41, 128340
	st.Segment, st.PrevSegment = 7, 6
	st.SegStartTurn, st.SegStartPos = 33, 104880
	st.LastPromptTurn, st.SubagentSince = 40, 1
	st.TodoDone["migrate schema"] = true
	st.mu.Unlock()

	require.NoError(t, first.obs.Persist(context.Background()))

	second := stateHarness(t, root)
	got := second.state(testSession)
	got.mu.Lock()
	defer got.mu.Unlock()

	require.Equal(t, core.TurnIndex(41), got.Turn)
	require.Equal(t, 128340, got.PrefixTokens)
	require.Equal(t, core.SegmentID(7), got.Segment)
	require.Equal(t, core.SegmentID(6), got.PrevSegment)
	require.Equal(t, core.TurnIndex(33), got.SegStartTurn)
	require.Equal(t, 104880, got.SegStartPos)
	require.Equal(t, core.TurnIndex(40), got.LastPromptTurn)
	require.Equal(t, 1, got.SubagentSince)
	require.Equal(t, core.ToolUseID("toolu_2"), got.LastToolUseID)
	require.Equal(t, map[string]bool{"migrate schema": true}, got.TodoDone)

	require.Len(t, got.ToolUses, 2)
	require.Equal(t, core.ToolUseID("toolu_1"), got.ToolUses[0].ID)
	require.Equal(t, "FileRead", got.ToolUses[0].Tool)
	require.Equal(t, "src/a.ts", got.ToolUses[0].Path)
	require.Equal(t, core.HashBytes(core.DomainArgs, []byte("alpha\n")), got.ToolUses[0].Root,
		"the store's own Root is the only identity; it must survive the round trip")

	require.Empty(t, got.Recent, "the feature window legitimately restarts cold")
	require.NotNil(t, got.WarnedRules, "warning-dedup state is rebuilt empty, never nil")
}

func TestState_IsByteStableAcrossRuns(t *testing.T) {
	root := t.TempDir()
	h := stateHarness(t, root)
	st := h.state(testSession)
	st.mu.Lock()
	for _, done := range []string{"zeta", "alpha", "mu"} {
		st.TodoDone[done] = true
	}
	st.mu.Unlock()

	require.NoError(t, h.obs.Persist(context.Background()))
	first := readStateFile(t, root)
	require.NoError(t, h.obs.Persist(context.Background()))
	require.Equal(t, string(first), string(readStateFile(t, root)),
		"TodoDone is persisted sorted so the file is diffable and stable")

	var ps persistedState
	require.NoError(t, json.Unmarshal(first, &ps))
	require.Equal(t, []string{"alpha", "mu", "zeta"}, ps.Sessions[string(testSession)].TodoDone)
	require.Equal(t, stateVersion, ps.Version)
}

func TestState_CorruptFileIsSetAsideAndTheSessionStartsFresh(t *testing.T) {
	root := t.TempDir()
	path := stateFilePath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(path)), stateDirPerm))
	require.NoError(t, os.WriteFile(paths.Long(path), []byte("{not json"), stateFilePerm))

	h := stateHarness(t, root)
	st := h.state(testSession)

	require.Equal(t, core.TurnIndex(0), st.Turn, "a corrupt file never blocks a session")
	require.Equal(t, int64(1), h.counter("observer.err."+stageState))
	_, err := os.Stat(paths.Long(path + corruptStateSuffix))
	require.NoError(t, err, "the unreadable file is renamed aside, not deleted")
}

func TestState_UnknownVersionIsTreatedAsCorrupt(t *testing.T) {
	root := t.TempDir()
	path := stateFilePath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(path)), stateDirPerm))
	require.NoError(t, os.WriteFile(paths.Long(path),
		[]byte(`{"version":99,"sessions":{"sess_sp08":{"turn":12}}}`), stateFilePerm))

	h := stateHarness(t, root)
	st := h.state(testSession)

	require.Equal(t, core.TurnIndex(0), st.Turn, "a future schema is the migration path, not a crash")
	_, err := os.Stat(paths.Long(path + corruptStateSuffix))
	require.NoError(t, err)
}

func TestState_MissingFileIsNotAnError(t *testing.T) {
	h := stateHarness(t, t.TempDir())

	st := h.state(testSession)

	require.Equal(t, core.TurnIndex(0), st.Turn)
	require.Equal(t, int64(0), h.counter("observer.err."+stageState), "a fresh project has no state file")
}

func TestState_UnparseableRootDropsOnlyThatEntry(t *testing.T) {
	root := t.TempDir()
	path := stateFilePath(root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(path)), stateDirPerm))

	good := core.HashBytes(core.DomainArgs, []byte("alpha")).String()
	body, err := json.Marshal(persistedState{
		Version: stateVersion,
		Sessions: map[string]persistedSession{
			string(testSession): {
				SubagentSince: 9,
				ToolUses: []persistedToolUse{
					{ID: "toolu_bad", Root: "not-a-hash", Tool: "FileRead", Path: "src/a.ts"},
					{ID: "toolu_good", Root: good, Tool: "FileRead", Path: "src/b.ts", Bytes: 5},
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(path), body, stateFilePerm))

	st := stateHarness(t, root).state(testSession)

	require.Len(t, st.ToolUses, 1, "one unreadable root drops one entry, not the window")
	require.Equal(t, core.ToolUseID("toolu_good"), st.ToolUses[0].ID)
	require.Equal(t, 1, st.SubagentSince, "the cursor is clamped to what actually survived")
}

func TestState_ToolUsesAreCappedOnWrite(t *testing.T) {
	root := t.TempDir()
	h := stateHarness(t, root)
	st := h.state(testSession)
	st.mu.Lock()
	for i := range subagentWindowCap + 50 {
		st.ToolUses = append(st.ToolUses,
			toolUseLite{ID: core.ToolUseID(fmt.Sprintf("toolu_%d", i)), Tool: "FileRead"})
	}
	st.mu.Unlock()

	require.NoError(t, h.obs.Persist(context.Background()))

	var ps persistedState
	require.NoError(t, json.Unmarshal(readStateFile(t, root), &ps))
	require.Len(t, ps.Sessions[string(testSession)].ToolUses, subagentWindowCap,
		"the window is capped on write, so the file cannot grow without bound")
}

func TestPersist_FlushesTheGraphAndHonoursCtx(t *testing.T) {
	h := stateHarness(t, t.TempDir())

	require.NoError(t, h.obs.Persist(context.Background()))
	require.Equal(t, 1, h.Graph.FlushCalls, "Persist is one of the three places Flush is called")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, h.obs.Persist(ctx), context.Canceled)
	require.Equal(t, 1, h.Graph.FlushCalls, "a cancelled Persist does no work")
}
