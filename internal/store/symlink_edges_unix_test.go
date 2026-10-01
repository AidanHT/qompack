//go:build unix

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Symbolic links where the store expects its own files and directories (unix only: creating one needs
// a privilege an ordinary Windows process lacks). A link is never followed. Where a retention source
// or the delivery authority should be, it halts the GC pass, which must not sweep against what it
// could not read; in the trees the publication audit walks, it is noted and not traversed; and where a
// restore would publish, it occupies the destination (w16b-cover, C3.6).

// symlinkInPlaceOf replaces p with a symbolic link to a fresh directory outside the project.
func symlinkInPlaceOf(t *testing.T, p string) {
	t.Helper()
	require.NoError(t, os.RemoveAll(p))
	require.NoError(t, os.Symlink(t.TempDir(), p))
}

// TestGC_HaltsOnASymlinkWhereARetentionSourceOrTheDeliveryAuthorityBelongs: checkpoints/, the
// delivery head, the generation store and delivery-segments/ are each read only as themselves; a link
// in their place halts the pass and nothing is swept.
func TestGC_HaltsOnASymlinkWhereARetentionSourceOrTheDeliveryAuthorityBelongs(t *testing.T) {
	cases := map[string]func(t *testing.T, tp *testProject){
		"checkpoints": func(t *testing.T, tp *testProject) { symlinkInPlaceOf(t, paths.Of(tp.Root).Checkpoints) },
		"delivery head": func(t *testing.T, tp *testProject) {
			symlinkInPlaceOf(t, filepath.Join(paths.Of(tp.Root).State, dsegHeadFile))
		},
		"generation store": func(t *testing.T, tp *testProject) {
			symlinkInPlaceOf(t, filepath.Join(paths.Of(tp.Root).State, dsegGensDir))
		},
		"delivery-segments beside an authority": func(t *testing.T, tp *testProject) {
			installSegAuthority(t, tp, []segSpec{{0, ""}})
			symlinkInPlaceOf(t, filepath.Join(paths.Of(tp.Root).State, dsegDir))
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			tp := newTestStore(t)
			ctx := context.Background()
			doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
			plant(t, tp)

			rep, err := tp.Store.GC(ctx, forceCollect)
			require.NoError(t, err)
			require.True(t, rep.RetentionRootsError, "a link where a retention source belongs must halt the pass")
			require.Zero(t, rep.DeletedObjects)
			_, err = tp.Store.GetRoot(ctx, doomed.Hash)
			require.NoError(t, err)
		})
	}
}

// TestAuditPublication_NotesSymlinksItWillNotTraverse: a link in the pending registry, the object
// tree or a capture shard is noted and passed over, so the audit reads incomplete rather than clean.
func TestAuditPublication_NotesSymlinksItWillNotTraverse(t *testing.T) {
	tp := newTestStore(t)
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	require.NoError(t, os.WriteFile(target, []byte(`{}`), 0o600))

	pending := filepath.Join(paths.Of(tp.Root).State, pendingWriteDir)
	require.NoError(t, os.MkdirAll(pending, 0o700))
	require.NoError(t, os.Symlink(target, filepath.Join(pending, "linked"+pendingWriteSuffix)))

	h := core.HashBytes(core.DomainChunk, []byte("linked object"))
	writeBareObject(t, tp, h)
	leaf := tp.Store.objectPath(h)
	require.NoError(t, os.Remove(leaf))
	require.NoError(t, os.Symlink(target, leaf))

	seedCapture(t, tp.Root, "linked-capture", auditOpObserveTool, false, core.OutcomeOK, []byte("x"))
	sidecar, err := CaptureSidecarPath(tp.Root, auditObsID("linked-capture"))
	require.NoError(t, err)
	require.NoError(t, os.Remove(sidecar))
	require.NoError(t, os.Symlink(target, sidecar))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.True(t, a.Incomplete)
	require.Subset(t, a.Notes, []string{
		"symlink or reparse point in the pending registry was not traversed",
		"symlink or reparse point in the object tree was not traversed",
		"symlink or reparse point in the capture tree was not traversed",
	})
	require.Zero(t, a.UnindexedObjectCandidates, "a linked leaf is not counted as an object")
}

// TestMaintenanceRestore_RefusesADestinationDotThatIsASymlink: a restore never publishes through a
// link; a .qompack that is one occupies the destination, and the refusal says so.
func TestMaintenanceRestore_RefusesADestinationDotThatIsASymlink(t *testing.T) {
	tp := newTestStore(t)
	seedRoot(t, tp, "src/a.ts", "a\n")
	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(context.Background(), "s1")
	require.NoError(t, err)

	dest := t.TempDir()
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(dest, ".qompack")))
	_, err = x.Restore(context.Background(), "s1", dest)
	require.ErrorIs(t, err, ErrRestoreTargetExists)
	require.Contains(t, err.Error(), "is a symlink")
}
