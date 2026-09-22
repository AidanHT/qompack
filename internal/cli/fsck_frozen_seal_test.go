package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/paths"
)

// The read-only delivery row and the old-reader barrier (SP20-D4, V6 close-out C1.10). After a store
// rotates, segment 0's two seals are FROZEN so a build that predates segments refuses the journal. The
// read-only row must classify such a seal by the segment authority beside it — not report "carries no
// version", and not accept one no authority accounts for.

// writeFrozenLegacySeals writes both legacy seals as frozen documents, each checked with the daemon's
// own classifier so the fixture cannot drift from the format.
func writeFrozenLegacySeals(t *testing.T, state string) {
	t.Helper()
	for _, name := range []string{"delivery-lease-position.json", "delivery-ack-position.json"} {
		raw, err := json.Marshal(struct {
			Format  string    `json:"format"`
			Segment uint64    `json:"segment"`
			Bytes   int64     `json:"bytes"`
			Count   int       `json:"count"`
			Chain   core.Hash `json:"chain"`
		}{"qompack.delivery.frozen-seal.v1", 0, 12, 1, core.HashBytes("fsck-frozen-fixture", []byte(name))})
		require.NoError(t, err)
		_, _, ok := daemon.FrozenDeliverySeal(name, raw)
		require.True(t, ok, "fixture: %s is a frozen seal by the daemon's own classifier", name)
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(state, name)), raw, 0o600))
	}
	for _, name := range []string{"delivery-leases.jsonl", "delivery-acks.jsonl"} {
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(state, name)), []byte(`{"v":1,"x":1}`+"\n"), 0o600))
	}
}

func TestFsck_FrozenLegacySealIsClassifiedByTheSegmentAuthority(t *testing.T) {
	p := seedFsckProject(t)
	state := paths.Of(p.Root).State
	writeFrozenLegacySeals(t, state)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(state, "delivery-journal.json")),
		[]byte(`{"v":1,"format":"qompack.delivery.segments.v1","seq":1,"active":1}`), 0o600))

	_, doc, errw := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "delivery")
	detail := fsckDetail(row)
	require.Contains(t, detail, "frozen seal of the archived legacy segment", "stderr=%s", errw)
	require.Contains(t, detail, "rotated to segment 1")
	require.NotContains(t, detail, "carries no version")
	require.Equal(t, true, row["ok"], "a frozen seal the authority accounts for is not a defect: %s", detail)
}

func TestFsck_FrozenLegacySealWithoutAnAuthorityIsADefect(t *testing.T) {
	p := seedFsckProject(t)
	writeFrozenLegacySeals(t, paths.Of(p.Root).State)

	code, doc, _ := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "delivery")
	require.Equal(t, false, row["ok"])
	require.Contains(t, fsckDetail(row), "no readable segment authority")
	require.Equal(t, ExitError, code)
}
