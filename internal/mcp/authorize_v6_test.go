package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

func TestV6_LostFilePathDoesNotBecomePathlessAuthority(t *testing.T) {
	f := newFixture(t)
	const body = "V6 archived file with discarded authorization path"
	root, id := f.putAndRecord(t, "Read", "", body, 1)
	for _, address := range []map[string]any{
		{"tool_use_id": string(id)},
		{"hash": root.String()},
	} {
		var denial deniedBody
		f.callOK(t, ToolExpand, address, &denial)
		require.True(t, denial.Denied)
		require.False(t, denial.Found)
	}
	var recalled recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": "discarded authorization"}, &recalled)
	require.Empty(t, recalled.Hits)
	require.Positive(t, recalled.Denied)
}

func TestV6_DedupDoesNotLaunderARestrictedHash(t *testing.T) {
	f := newFixture(t)
	const text = "V6 shared content with independently scoped origins"
	root, shellID := f.putAndRecord(t, "Bash", "", text, 1)
	var content contentBody
	f.callOK(t, ToolExpand, map[string]any{"hash": root.String()}, &content)
	require.Equal(t, text, content.Content, "genuinely pathless shell output remains usable")
	other, _ := f.putAndRecord(t, "Read", "", text, 2)
	require.Equal(t, root, other, "the fixture must exercise content deduplication")
	rt, err := f.Store.GetRoot(context.Background(), root)
	require.NoError(t, err)
	for _, hash := range []core.Hash{root, rt.Chunks[0].Hash} {
		var denial deniedBody
		f.callOK(t, ToolExpand, map[string]any{"hash": hash.String()}, &denial)
		require.True(t, denial.Denied, "a bare hash must not choose only its permissive origin")
	}
	f.callOK(t, ToolExpand, map[string]any{"tool_use_id": string(shellID)}, &content)
	require.Equal(t, text, content.Content, "a specific permitted origin remains independently addressable")
}

// Hiding the optional provenance capability must not recover the old hash bypass.
type v6StoreWithoutProvenance struct{ store.Store }

func TestV6_HashRefusesStoreWithoutProvenance(t *testing.T) {
	f := newFixture(t)
	root, _ := f.putAndRecord(t, "Bash", "", "known bytes", 1)
	d := f.Deps
	d.Store = v6StoreWithoutProvenance{f.Store}
	h := newHandlers(d)
	refusal := h.authorizeHash(context.Background(), root)
	body, ok := refusal.(missBody)
	require.True(t, ok)
	no := false
	require.Equal(t, &no, body.Available)
}
