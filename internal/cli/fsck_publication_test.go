package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFsck_UnknownObservationIntentIsIncompleteAndReadOnly(t *testing.T) {
	p := seedFsckProject(t)
	file := filepath.Join(p.Layot.Index, "observations.jsonl")
	require.NoError(t, os.WriteFile(file, []byte("{\"v\":999,\"unknown\":\"private-test-body\"}\n"), 0o600))
	before := snapshotQompack(t, p.Layot)
	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "publication")
	require.Equal(t, false, row["ok"])
	require.Contains(t, fsckDetail(row), "incomplete")
	require.NotContains(t, fsckDetail(row), "private-test-body")
	require.Equal(t, before, snapshotQompack(t, p.Layot), "audit must preserve unknown intent evidence")
}
