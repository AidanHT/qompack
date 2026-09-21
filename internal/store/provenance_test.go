package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestContentOrigins_TracksDeduplicatedOriginsAcrossReopen(t *testing.T) {
	p := newTestStore(t)
	ctx := context.Background()
	res, err := p.Store.PutBytes(ctx, []byte("one object, two independently authorized origins"), PutOptions{Tool: "Bash"})
	require.NoError(t, err)
	require.NoError(t, p.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "file-origin", Session: "origin-session", Tool: "Read", Root: res.Root.Hash, Path: "src/a.go",
	}))
	require.NoError(t, p.Store.Flush(ctx))
	for _, hash := range []core.Hash{res.Root.Hash, res.Root.Chunks[0].Hash} {
		origins, err := p.Store.ContentOrigins(ctx, hash)
		require.NoError(t, err)
		require.ElementsMatch(t, []ContentOrigin{{Tool: "Bash"}, {Tool: "Read", Path: "src/a.go"}}, origins)
	}
	// Read-only loading uses exactly the persisted legacy indices; no new wire field is needed.
	loaded, err := OpenReadOnly(p.Root, p.Store.cfg, Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, loaded.Close()) })
	ro, ok := loaded.(readOnlyStore)
	require.True(t, ok)
	origins, err := ro.fs.ContentOrigins(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.ElementsMatch(t, []ContentOrigin{{Tool: "Bash"}, {Tool: "Read", Path: "src/a.go"}}, origins)
}

func TestContentOrigins_IncompleteScanReturnsNoPartialAuthority(t *testing.T) {
	p := newTestStore(t)
	ctx := context.Background()
	res, err := p.Store.PutBytes(ctx, []byte("bounded authority fixture"), PutOptions{Tool: "Bash"})
	require.NoError(t, err)
	p.Store.mu.Lock()
	p.Store.rootIndex[res.Root.Hash].Root.Chunks = make([]core.ChunkRef, maxProvenanceEntries+1)
	p.Store.mu.Unlock()
	origins, err := p.Store.ContentOrigins(ctx, res.Root.Hash)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Nil(t, origins)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	origins, err = p.Store.ContentOrigins(cancelled, res.Root.Hash)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, origins)
}
