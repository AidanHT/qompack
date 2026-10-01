package paths

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEntryLedger_ASyncDecidedOnAnOlderGenerationCreditsNothing pins the ledger's generation rule: a
// name re-created while a barrier for its previous incarnation was in flight is not made durable by
// that barrier, only by one decided on after the re-create.
func TestEntryLedger_ASyncDecidedOnAnOlderGenerationCreditsNothing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.jsonl")
	t.Cleanup(func() { ForgetEntriesUnder(filepath.Dir(p)) })

	entries.creating(p)
	_, stale := entries.look(p)
	entries.creating(p) // removed and re-created while the first barrier was in flight
	entries.credit(p, stale)
	st, current := entries.look(p)
	require.Equal(t, entryPending, st, "the stale barrier must not credit the re-created name")

	entries.credit(p, current)
	st, _ = entries.look(p)
	require.Equal(t, entryDurable, st)
}

// TestSyncEntries_SyncsEveryParentOnceDeepestFirstThenByName pins syncEntries' order: each distinct
// parent once, a deeper directory before a shallower one, and two parents of one depth in name order,
// so a run is reproducible and a directory's own entry is synced only after the entries inside it.
func TestSyncEntries_SyncsEveryParentOnceDeepestFirstThenByName(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { ForgetEntriesUnder(root) })
	names := []string{
		filepath.Join(root, "b", "x"),
		filepath.Join(root, "a", "y"),
		filepath.Join(root, "a", "d", "z"),
		filepath.Join(root, "a", "w"),
	}
	var synced []string
	x := Barriers{SyncDir: func(dir string) error {
		synced = append(synced, dir)
		return nil
	}}
	require.NoError(t, x.syncEntries("test", names))
	require.Equal(t, []string{
		filepath.Join(root, "a", "d"),
		filepath.Join(root, "a"),
		filepath.Join(root, "b"),
	}, synced)

	errStop := errors.New("injected")
	x.SyncDir = func(string) error { return errStop }
	err := x.syncEntries("caller", names)
	require.ErrorIs(t, err, errStop)
	require.Contains(t, err.Error(), "caller", "the error names the operation that asked for the barrier")
}
