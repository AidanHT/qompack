package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestCaptureSidecar_ANewShardIsDurableBeforeTheSidecarIsWritten: a capture sidecar is publication
// order's first stage, durable before any reference or frontier ACK names it. It lives in
// records/captures/<shard>/, made on demand, and WriteAtomic syncs only the shard directory — so the
// directories the write creates are synced into their parents first: captures/ and records/ for the
// project's first capture, captures/ alone for a new shard, nothing for a shard that exists. Each
// directory sync runs before the sidecar exists.
func TestCaptureSidecar_ANewShardIsDurableBeforeTheSidecarIsWritten(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))

	// Three observations: two that share a shard, one in another shard.
	var ids []core.ObservationID
	byShard := map[string][]core.ObservationID{}
	for arrival := uint64(1); len(ids) < 3 && arrival < 4096; arrival++ {
		id := testObservationID(t, "sidecar-durability", arrival)
		p, err := CaptureSidecarPath(root, id)
		require.NoError(t, err)
		shard := filepath.Base(filepath.Dir(p))
		switch {
		case len(ids) == 0, len(ids) == 1 && len(byShard[shard]) == 1, len(ids) == 2 && len(byShard[shard]) == 0:
			ids = append(ids, id)
			byShard[shard] = append(byShard[shard], id)
		}
	}
	require.Len(t, ids, 3, "fixture: two observations share a shard and a third has its own")

	var steps []string
	var sidecarExisted []bool
	current := ""
	b := paths.Barriers{SyncDir: func(dir string) error {
		steps = append(steps, "dir:"+filepath.Base(dir))
		_, err := os.Lstat(paths.Long(current))
		sidecarExisted = append(sidecarExisted, err == nil)
		return paths.SyncDir(dir)
	}}
	write := func(id core.ObservationID) []string {
		steps, sidecarExisted = nil, nil
		p, err := CaptureSidecarPath(root, id)
		require.NoError(t, err)
		current = p
		require.NoError(t, writeCaptureSidecar(root, CaptureSidecar{
			ObservationID: id, Session: "sidecar-durability", Op: "observe.tool",
			HashVersion: core.EvidenceHashVersion, Fidelity: core.FidelityExact, Outcome: core.OutcomeOK,
			Bytes: []byte(`{"tool_response":"ok"}`),
		}, b))
		for i, existed := range sidecarExisted {
			require.Falsef(t, existed, "directory barrier %d (%s) runs before the sidecar is written", i, steps[i])
		}
		return steps
	}

	require.Equal(t, []string{"dir:captures", "dir:records"}, write(ids[0]),
		"the first capture syncs its shard's entry in captures/ and captures' entry in records/")
	require.Empty(t, write(ids[1]), "a sidecar in an existing shard pays no directory barrier")
	require.Equal(t, []string{"dir:captures"}, write(ids[2]), "a new shard syncs its entry in captures/")
}
