package paths_test

// A directory barrier that failed, or that another writer has not yet issued, is never taken as done
// (w6-ckptsync review finding 1).
//
// AppendLinesDurable, MkdirAll and EnsureLayout used to decide whether to sync a directory from an
// Lstat taken before the write: "the name did not exist, so this call created it and syncs its
// parent". A call whose parent sync failed returned the error with the name already on disk, and the
// next call's Lstat then saw the name, skipped the sync and reported success — a name that was never
// made durable, reported durable. The same held for a name another writer had created and not yet
// synced, whether that writer was a goroutine still inside its barrier or a process that exited
// before it. These tests pin that the next call issues the barrier itself.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestAppendLinesDurable_RetriesADirectoryBarrierThatFailed: the append that created the file had
// its directory sync fail, so the file's name is not durable; the next append syncs the directory
// before it reports its line durable, and only then does a later append stop paying for it.
func TestAppendLinesDurable_RetriesADirectoryBarrierThatFailed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log.jsonl")

	failed := &barrierLog{failAt: 2}
	require.ErrorIs(t, failed.barriers().AppendLinesDurable(p, []byte("{\"n\":1}\n")), errBarrier)
	require.Equal(t, []string{"file:log.jsonl", "dir:" + filepath.Base(dir)}, failed.steps)

	retry := &barrierLog{}
	require.NoError(t, retry.barriers().AppendLinesDurable(p, []byte("{\"n\":2}\n")))
	require.Equal(t, []string{"file:log.jsonl", "dir:" + filepath.Base(dir)}, retry.steps,
		"the file's name was never made durable, so this append must sync its directory")

	after := &barrierLog{}
	require.NoError(t, after.barriers().AppendLinesDurable(p, []byte("{\"n\":3}\n")))
	require.Equal(t, []string{"file:log.jsonl"}, after.steps, "once the name is durable, an append syncs only its line")
}

// TestAppendLinesDurable_SyncsTheNameOfAFileItDidNotCreate: a file some other writer created — a
// process that exited before its directory sync, or never issued one — has a name this process has
// not made durable, so this process's first durable append to it syncs its directory.
func TestAppendLinesDurable_SyncsTheNameOfAFileItDidNotCreate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log.jsonl")
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("{\"n\":0}\n"), 0o600))

	first := &barrierLog{}
	require.NoError(t, first.barriers().AppendLinesDurable(p, []byte("{\"n\":1}\n")))
	require.Equal(t, []string{"file:log.jsonl", "dir:" + filepath.Base(dir)}, first.steps)

	second := &barrierLog{}
	require.NoError(t, second.barriers().AppendLinesDurable(p, []byte("{\"n\":2}\n")))
	require.Equal(t, []string{"file:log.jsonl"}, second.steps)
}

// TestBarriersMkdirAll_RetriesAParentSyncThatFailed: when a parent sync fails, every directory the
// call created whose entry is not yet durable is synced by the next call, even though the next call
// finds them all on disk. The two rows fail the first sync (neither entry durable) and the second
// (the shard's entry durable, captures' entry not).
func TestBarriersMkdirAll_RetriesAParentSyncThatFailed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failAt    int
		wantRetry func(base string) []string
	}{
		{name: "the shard's entry failed", failAt: 1, wantRetry: func(base string) []string {
			return []string{"dir:captures", "dir:" + filepath.Base(base)}
		}},
		{name: "captures' entry failed", failAt: 2, wantRetry: func(base string) []string {
			return []string{"dir:" + filepath.Base(base)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			deep := filepath.Join(base, "captures", "ab")

			failed := &barrierLog{failAt: tc.failAt}
			require.ErrorIs(t, failed.barriers().MkdirAll(deep, 0o700), errBarrier)

			retry := &barrierLog{}
			require.NoError(t, retry.barriers().MkdirAll(deep, 0o700))
			require.Equal(t, tc.wantRetry(base), retry.steps,
				"a directory whose entry the failed call did not make durable is synced by the next call")

			after := &barrierLog{}
			require.NoError(t, after.barriers().MkdirAll(deep, 0o700))
			require.Empty(t, after.steps, "once every entry is durable, the directory costs no barrier")
		})
	}
}

// TestEnsureLayout_RetriesABarrierThatFailed: a layout whose parent syncs failed is not left looking
// finished. The next call syncs every parent again, and only a call whose syncs all succeeded writes
// .qompack/.gitignore, the file whose presence says the layout's own entries are durable.
func TestEnsureLayout_RetriesABarrierThatFailed(t *testing.T) {
	root := t.TempDir()
	l := paths.Of(root)
	gitignore := filepath.Join(l.Dot, ".gitignore")

	failed := &barrierLog{failAt: 2}
	require.ErrorIs(t, failed.barriers().EnsureLayout(l), errBarrier)
	_, err := os.Lstat(paths.Long(gitignore))
	require.ErrorIs(t, err, os.ErrNotExist, "a layout whose barriers failed is not marked complete")

	retry := &barrierLog{}
	require.NoError(t, retry.barriers().EnsureLayout(l))
	require.Equal(t, []string{"dir:.qompack", "dir:" + filepath.Base(root)}, retry.steps,
		"the entries the failed call did not make durable are synced by the next call")
	require.FileExists(t, gitignore)

	again := &barrierLog{}
	require.NoError(t, again.barriers().EnsureLayout(l))
	require.Empty(t, again.steps)
}

// TestEnsureLayout_SyncsALayoutAnotherWriterStarted: .qompack can come into existence without
// EnsureLayout — a hook spooling before any daemon has run creates .qompack/spool with a plain
// mkdir, and syncs nothing. The first EnsureLayout over such a tree finds no .gitignore, so it takes
// none of the existing entries as durable and syncs them all, the project root's entry for .qompack
// among them.
func TestEnsureLayout_SyncsALayoutAnotherWriterStarted(t *testing.T) {
	root := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(paths.Long(l.Spool), 0o700))

	b := &barrierLog{}
	require.NoError(t, b.barriers().EnsureLayout(l))
	require.Equal(t, []string{"dir:eval", "dir:.qompack", "dir:" + filepath.Base(root)}, b.steps)
	require.FileExists(t, filepath.Join(l.Dot, ".gitignore"))
}

