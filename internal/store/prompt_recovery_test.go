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
