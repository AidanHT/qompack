package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// SP08-D1 under owner decision D20: a directory is fsynced once per durable step. A segment
// transition and a generation commit each create exactly one entry in their directory, their head,
// by an atomic rename followed by a directory fsync; the log file they append to was created at open.
// That directory fsync makes durable every entry the step depends on, so these tests pin it as the
// step's only one.

// dirSyncCounter records every directory a step fsyncs, and fsyncs it.
type dirSyncCounter struct{ dirs []string }

func (c *dirSyncCounter) sync(dir string) error {
	c.dirs = append(c.dirs, dir)
	return paths.SyncDir(dir)
}

// TestDeliverySegments_ATransitionFsyncsTheStateDirectoryOnce pins the active-0 record and a
// rotation's transition at one fsync of the state directory each, and the authority survives a
// reopen, as the transition's own recovery tests require.
func TestDeliverySegments_ATransitionFsyncsTheStateDirectoryOnce(t *testing.T) {
	dir := t.TempDir()
	s, exists, err := openDeliverySegments(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.close() }) // close is idempotent; this only frees handles a failure left open
	require.False(t, exists)
	c := &dirSyncCounter{}
	s.syncDir = c.sync

	require.NoError(t, s.initActiveZero())
	require.Equal(t, []string{dir}, c.dirs, "the active-0 record fsyncs the state directory once")
	c.dirs = nil
	require.NoError(t, s.commitTransition(1, segRoot(1)))
	require.Equal(t, []string{dir}, c.dirs, "a rotation's transition fsyncs the state directory once")
	require.NoError(t, s.close())

	s2, exists, err := openDeliverySegments(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.close() })
	require.True(t, exists)
	require.Equal(t, uint64(1), s2.activeSeg())
	require.Equal(t, segRoot(1), s2.baseRootHex())
	require.NoError(t, s2.close())
}

// TestDeliveryGeneration_ACommitFsyncsItsDirectoryOnce pins a generation commit at one fsync of the
// generation directory (the radix's pack and pointer directories are its own, and not counted here),
// and the committed generation survives a reopen.
func TestDeliveryGeneration_ACommitFsyncsItsDirectoryOnce(t *testing.T) {
	dir := t.TempDir()
	g, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.close() }) // a second close only reports an already-closed handle
	c := &dirSyncCounter{}
	g.syncDir = c.sync
	ctx := context.Background()

	root, err := g.commit(ctx, []deliveryLease{mkGenLease(t, genNonce(0), "sess-A", 1)})
	require.NoError(t, err)
	require.Equal(t, []string{g.dir}, c.dirs, "a generation commit fsyncs its directory once")
	require.NoError(t, g.close())

	g2, err := openDeliveryGenerations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = g2.close() })
	got, err := g2.snapshot()
	require.NoError(t, err)
	require.Equal(t, root, got, "the committed generation is recovered")
	require.NoError(t, g2.close())
}
