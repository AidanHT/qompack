package mcp

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestExpandReportsAMissingObjectAsMissing is F-C49-3 (live evidence c4/C4.9: one chunk object of a
// capture was moved out of a disposable copy). expand answered `unavailable` — right — with the
// reason "its bytes could not be read intact; a damaged object is preserved as evidence", which is
// not what happened: nothing was damaged and nothing was preserved, the object is simply gone.
// fsck names the same object "which the object store does not hold". The reason must say missing,
// and a genuinely damaged object must keep the damaged wording.
func TestExpandReportsAMissingObjectAsMissing(t *testing.T) {
	no := false

	t.Run("an object removed from objects/", func(t *testing.T) {
		f, _, _, id := spanAuthObject(t)
		root := spanRootOf(t, f, mustToolUseRoot(t, f, id))
		require.NoError(t, f.Store.Flush(context.Background()))
		require.NoError(t, os.Remove(paths.Long(corruptObjectPath(t, f.Root, root.Chunks[0].Hash))))

		resp := f.call(t, ToolExpand, map[string]any{"tool_use_id": string(id), "full": true})
		require.False(t, resp.IsError, "%s", responseText(resp))
		b := decodeMiss(t, responseText(resp))
		require.False(t, b.Found)
		require.Equal(t, &no, b.Available, "a missing object is still `unavailable`, never absent")
		require.Contains(t, b.Reason, "missing")
		require.NotContains(t, b.Reason, "damaged", "nothing was damaged: %s", b.Reason)
		require.NotContains(t, b.Reason, "preserved as evidence", "nothing was preserved: %s", b.Reason)
	})

	t.Run("a damaged object keeps the damaged wording", func(t *testing.T) {
		f, _, _, id := spanAuthObject(t)
		root := spanRootOf(t, f, mustToolUseRoot(t, f, id))
		damageObject(t, f, root.Chunks[0].Hash)

		resp := f.call(t, ToolExpand, map[string]any{"tool_use_id": string(id), "full": true})
		b := decodeMiss(t, responseText(resp))
		require.Equal(t, &no, b.Available)
		require.Contains(t, b.Reason, "preserved as evidence")
	})
}
