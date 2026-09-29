package store

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestPromptFrontier_DistinguishesTurnClosingRecords(t *testing.T) {
	p := newTestStore(t)
	ctx := context.Background()
	digest, _ := ArgsDigest([]byte(`{"observation_id":"leased"}`))
	for _, rec := range []ToolUseRecord{
		{ID: "prompt_s_2", Session: "s", Turn: 2, Tool: "UserPromptSubmit", ArgsDigest: digest},
		{ID: "tool", Session: "s", Turn: 7, Tool: "Read"},
		{ID: "unrelated", Session: "other", Turn: 100, Tool: "UserPromptSubmit"},
	} {
		require.NoError(t, p.Store.RecordToolUse(ctx, rec))
	}
	next, found, err := p.Store.PromptFrontier(ctx, "s", digest)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(7), next, "tool use occupies the current turn without closing it")
	require.Equal(t, core.ToolUseID("prompt_s_2"), found.ID)
	require.NoError(t, p.Store.RecordToolUse(ctx, ToolUseRecord{ID: "stop", Session: "s", Turn: 7, Tool: "SubagentStop"}))
	next, _, err = p.Store.PromptFrontier(ctx, "s", digest)
	require.NoError(t, err)
	require.Equal(t, core.TurnIndex(8), next)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = p.Store.PromptFrontier(cancelled, "s", digest)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSyncPublication_RequiresEveryRecoveryObjectAndOpenWriters(t *testing.T) {
	p := newTestStore(t)
	ctx := context.Background()
	res, err := p.Store.PutBytes(ctx, []byte("original\r\nwith CRLF\r\n"), PutOptions{Tool: "UserPromptSubmit", KeepRaw: true})
	require.NoError(t, err)
	require.NoError(t, p.Store.SyncPublication(ctx, res.Root.Hash))
	objects, err := p.Store.publicationObjects(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.NotEmpty(t, objects)
	for hash := range objects {
		require.NoError(t, os.Remove(p.Store.objectPath(hash)))
		break
	}
	require.Error(t, p.Store.SyncPublication(ctx, res.Root.Hash), "missing content cannot be certified by flushing indices")
	require.NoError(t, p.Store.Close())
	require.ErrorIs(t, p.Store.SyncPublication(ctx, res.Root.Hash), core.ErrDegraded)
}

// TestEarliestPrompt_PicksTheEarliestHostStampedPromptOfTheSession: the rehydrator's check that
// prompt_<s>_0 is the host-first prompt (SP08-D3, D35) reads the session's prompt with the lowest
// TS, the lower turn on a tie, and nothing from another session or another tool.
func TestEarliestPrompt_PicksTheEarliestHostStampedPromptOfTheSession(t *testing.T) {
	p := newTestStore(t)
	ctx := context.Background()

	_, err := p.Store.EarliestPrompt(ctx, "s")
	require.ErrorIs(t, err, core.ErrNotFound, "a session with no prompt has no earliest one")

	for _, rec := range []ToolUseRecord{
		{ID: "prompt_s_0", Session: "s", Turn: 0, TS: 200, Tool: "UserPromptSubmit"},
		{ID: "prompt_s_1", Session: "s", Turn: 1, TS: 100, Tool: "UserPromptSubmit"},
		{ID: "prompt_s_2", Session: "s", Turn: 2, TS: 100, Tool: "UserPromptSubmit"},
		{ID: "tool", Session: "s", Turn: 1, TS: 50, Tool: "Read"},
		{ID: "stop", Session: "s", Turn: 3, TS: 10, Tool: "SubagentStop"},
		{ID: "prompt_other_0", Session: "other", Turn: 0, TS: 1, Tool: "UserPromptSubmit"},
	} {
		require.NoError(t, p.Store.RecordToolUse(ctx, rec))
	}
	got, err := p.Store.EarliestPrompt(ctx, "s")
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("prompt_s_1"), got.ID,
		"lowest TS among this session's prompts, and the lower turn of two equal stamps")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = p.Store.EarliestPrompt(cancelled, "s")
	require.ErrorIs(t, err, context.Canceled)
}

// TestSessionPrompts_IsOneSessionsPromptsInTurnOrder: internal/checkpoint reads a session's intent
// from these records, so they must be that session's prompts only, oldest turn first — never
// another session's, never a tool record, whatever order they were indexed in.
func TestSessionPrompts_IsOneSessionsPromptsInTurnOrder(t *testing.T) {
	p := newTestStore(t)
	ctx := context.Background()

	got, err := p.Store.SessionPrompts(ctx, "s")
	require.NoError(t, err)
	require.Empty(t, got, "a session with no prompt has none")

	for _, rec := range []ToolUseRecord{
		{ID: "prompt_s_7", Session: "s", Turn: 7, TS: 300, Tool: "UserPromptSubmit"},
		{ID: "prompt_s_0", Session: "s", Turn: 0, TS: 200, Tool: "UserPromptSubmit"},
		{ID: "tool", Session: "s", Turn: 1, TS: 50, Tool: "Read"},
		{ID: "stop", Session: "s", Turn: 3, TS: 10, Tool: "SubagentStop"},
		{ID: "prompt_s_2", Session: "s", Turn: 2, TS: 100, Tool: "UserPromptSubmit"},
		{ID: "prompt_other_1", Session: "other", Turn: 1, TS: 1, Tool: "UserPromptSubmit"},
	} {
		require.NoError(t, p.Store.RecordToolUse(ctx, rec))
	}
	got, err = p.Store.SessionPrompts(ctx, "s")
	require.NoError(t, err)
	ids := make([]core.ToolUseID, 0, len(got))
	for _, rec := range got {
		ids = append(ids, rec.ID)
	}
	require.Equal(t, []core.ToolUseID{"prompt_s_0", "prompt_s_2", "prompt_s_7"}, ids)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = p.Store.SessionPrompts(cancelled, "s")
	require.ErrorIs(t, err, context.Canceled)
}

// TestLatestPrompt_IsTheNewestOtherSessionsPromptAtOrBefore: internal/checkpoint takes this record's
// session as the one a fork continues, so it must be another session's prompt, never the fork's
// own, never one stamped after the fork started, and never a tool record.
func TestLatestPrompt_IsTheNewestOtherSessionsPromptAtOrBefore(t *testing.T) {
	p := newTestStore(t)
	ctx := context.Background()

	_, err := p.Store.LatestPrompt(ctx, "fork", 1_000)
	require.ErrorIs(t, err, core.ErrNotFound, "an empty index names no one")

	for _, rec := range []ToolUseRecord{
		{ID: "prompt_old_0", Session: "old", Turn: 0, TS: 100, Tool: "UserPromptSubmit"},
		{ID: "prompt_parent_3", Session: "parent", Turn: 3, TS: 400, Tool: "UserPromptSubmit"},
		{ID: "tool_parent", Session: "parent", Turn: 4, TS: 450, Tool: "Read"},
		{ID: "prompt_fork_0", Session: "fork", Turn: 0, TS: 480, Tool: "UserPromptSubmit"},
		{ID: "prompt_later_0", Session: "later", Turn: 0, TS: 600, Tool: "UserPromptSubmit"},
	} {
		require.NoError(t, p.Store.RecordToolUse(ctx, rec))
	}
	got, err := p.Store.LatestPrompt(ctx, "fork", 500)
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("prompt_parent_3"), got.ID)

	got, err = p.Store.LatestPrompt(ctx, "fork", 400)
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("prompt_parent_3"), got.ID, "a stamp equal to the moment counts")

	got, err = p.Store.LatestPrompt(ctx, "fork", 399)
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("prompt_old_0"), got.ID)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = p.Store.LatestPrompt(cancelled, "fork", 500)
	require.ErrorIs(t, err, context.Canceled)
}
