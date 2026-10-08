package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestCompactLoadRig_InProcessHooksSpoolToFilesOfTheirOwn is owner decision D78(b). The C1.16 rig
// runs every hook in the test process, so under pid naming all its hooks shared one client spool,
// client-<pid>.ndjson, and its one 64 MiB cap: under co-load the rig's ACK lapses filled that file,
// and a later hook's capture that never reached the daemon was dropped. A field deployment's hooks
// are separate processes; the rig's must not share a file either. With the daemon disabled every
// hook spools, and each of the rig's in-process hooks must leave a file of its own holding its one
// delivery.
func TestCompactLoadRig_InProcessHooksSpoolToFilesOfTheirOwn(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	writeProjectConfig(t, root, `{"runtime":{"daemon":{"enabled":false}}}`)
	r := &compactLoadRig{root: root, home: t.TempDir()}

	const hooks = 3
	for range hooks {
		p, err := r.readPayload(1 << 10)
		require.NoError(t, err)
		r.hook(t, []string{"observe", "tool"}, p)
	}

	files := clientSpools(t, root)
	require.Len(t, files, hooks, "each in-process hook spools to a file of its own; found %v", files)
	r.readsMu.Lock()
	ids := append([]string(nil), r.reads...)
	r.readsMu.Unlock()
	require.Len(t, ids, hooks)
	holders := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Spool, f)))
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(string(b), "\n"), "%s holds one hook's one delivery", f)
		for _, id := range ids {
			if strings.Contains(string(b), `"`+id+`"`) {
				require.Empty(t, holders[id], "%s is in %s and %s", id, holders[id], f)
				holders[id] = f
			}
		}
	}
	for _, id := range ids {
		require.NotEmpty(t, holders[id], "the Read %s is in no client spool", id)
	}
}
