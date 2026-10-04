package store

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// TestContentOrigins_OneOrderOnEveryCall is audit 2's #13 (D53(a)): ContentOrigins ranged over the
// root index, the tool-use index and the file history, three maps, and returned its origins in
// their iteration order. An MCP refusal names the first refused origin, so a chunk shared by a
// denied path and an out-of-project path gave a different withheld reason from one read to the
// next. The origins now come back in one order — by path, then tool — on every call, from every
// index they are recorded in.
func TestContentOrigins_OneOrderOnEveryCall(t *testing.T) {
	p := newTestStore(t)
	ctx := context.Background()
	res, err := p.Store.PutBytes(ctx, []byte("shared content, many origins"), PutOptions{Tool: "Bash"})
	require.NoError(t, err)
	for i, path := range []string{"private/deny.txt", "../outside/x.txt", "src/a.go", "src/b.go", "docs/z.md"} {
		require.NoError(t, p.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: core.ToolUseID(fmt.Sprintf("tu-%d", i)), Session: "s", Tool: "Read", Root: res.Root.Hash, Path: path,
		}))
	}
	for _, path := range []string{"src/c.go", "a/first.go", "zz/last.go"} {
		require.NoError(t, p.Store.AppendFileVersion(ctx, path, FileVersion{TS: 1, Root: res.Root.Hash, Bytes: 1}))
	}

	byPathThenTool := func(a, b ContentOrigin) int { return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Tool, b.Tool)) }
	for _, hash := range []core.Hash{res.Root.Hash, res.Root.Chunks[0].Hash} {
		first, err := p.Store.ContentOrigins(ctx, hash)
		require.NoError(t, err)
		require.Len(t, first, 9, "the Bash put, five tool uses and three file versions")
		require.True(t, slices.IsSortedFunc(first, byPathThenTool), "origins by path, then tool: %v", first)
		for i := range 200 {
			again, err := p.Store.ContentOrigins(ctx, hash)
			require.NoError(t, err)
			require.Equal(t, first, again, "call %d returned another order", i+2)
		}
	}
}
