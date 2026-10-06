//go:build windows

package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// NTFS junctions where the store expects its own directories and files: the Windows half of
// symlink_edges_unix_test.go and observation_guards_unix_test.go. A symbolic link needs a privilege
// an ordinary Windows process lacks, so those rows run only on unix, but a junction needs none: it is
// the alias an unprivileged process on the user's machine can actually plant inside .qompack, and the
// guards meet it as a reparse point (os.ModeIrregular), not as os.ModeSymlink. These rows pin that
// the guards the unix rows pin refuse it too, wherever one can be planted: the GC halts rather
// than reading a retention source through it, the publication audit notes it and does not traverse
// it, the observation reader treats an aliased index as uncertain, and a restore never publishes
// through one (C3.6, Windows coverage floors).

// makeJunction creates an NTFS junction at link pointing at the directory target. It is always a
// junction, even on a host whose process could create a symbolic link (a hosted runner's
// administrator account): what these rows test is the alias every Windows user can create, and
// makeDirLink would fall back to it only where os.Symlink is refused.
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	// mklink is a cmd builtin rather than an executable, hence cmd /c: the standard library has no
	// call that creates a junction.
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	require.NoError(t, err, "mklink /J %s %s: %s", link, target, strings.TrimSpace(string(out)))
	fi, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeIrregular, "fixture: a junction must read as a reparse point")
	require.Zero(t, fi.Mode()&os.ModeSymlink, "fixture: a junction is not a symbolic link")
}

// junctionInPlaceOf replaces p with a junction to a fresh directory outside the project.
func junctionInPlaceOf(t *testing.T, p string) {
	t.Helper()
	require.NoError(t, os.RemoveAll(p))
	makeJunction(t, p, t.TempDir())
}

// TestGC_HaltsOnAJunctionWhereARetentionSourceBelongs: checkpoints/ and delivery-segments/ are each
// read only as themselves; a junction in their place halts the pass and nothing is swept. (The
// delivery head is a file, which a junction cannot stand in for; its row is the unix symlink one.
// The generation store is never read, only tested for presence, and a plain directory there halts
// the pass just as a junction does, so its row is TestDsegMigrationEvidence_NamesAJunctionedGenStore.)
func TestGC_HaltsOnAJunctionWhereARetentionSourceBelongs(t *testing.T) {
	cases := map[string]func(t *testing.T, tp *testProject){
		"checkpoints": func(t *testing.T, tp *testProject) { junctionInPlaceOf(t, paths.Of(tp.Root).Checkpoints) },
		"delivery-segments beside an authority": func(t *testing.T, tp *testProject) {
			installSegAuthority(t, tp, []segSpec{{0, ""}})
			junctionInPlaceOf(t, filepath.Join(paths.Of(tp.Root).State, dsegDir))
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
			require.True(t, rep.RetentionRootsError, "a junction where a retention source belongs must halt the pass")
			require.Zero(t, rep.DeletedObjects)
			_, err = tp.Store.GetRoot(ctx, doomed.Hash)
			require.NoError(t, err)
		})
	}
}

// TestDsegMigrationEvidence_NamesAJunctionedGenStore: the generation store's presence alone is
// migration evidence (the control, a plain directory, is reported as evidence and nothing more), but
// a junction in its place is refused by the no-follow confinement as the reparse point it is, before
// anything inspects it as a directory.
func TestDsegMigrationEvidence_NamesAJunctionedGenStore(t *testing.T) {
	tp := newTestStore(t)
	gens := filepath.Join(paths.Of(tp.Root).State, dsegGensDir)
	require.NoError(t, os.MkdirAll(gens, 0o700))
	evidence, err := tp.Store.dsegMigrationEvidence()
	require.NoError(t, err)
	require.True(t, evidence, "control: a plain generation store is migration evidence")

	junctionInPlaceOf(t, gens)
	evidence, err = tp.Store.dsegMigrationEvidence()
	require.ErrorIs(t, err, errRetentionRootsUnavailable)
	require.Contains(t, err.Error(), "is a symlink or reparse point")
	require.False(t, evidence)
}

// TestAuditPublication_NotesJunctionsItWillNotTraverse: a junction in the pending registry, at an
// object leaf or at a capture sidecar is noted and passed over, so the audit reads incomplete rather
// than clean, and nothing behind it is counted.
func TestAuditPublication_NotesJunctionsItWillNotTraverse(t *testing.T) {
	tp := newTestStore(t)
	elsewhere := t.TempDir()
	// What the junctions point at looks exactly like what each walk counts, so a traversal would
	// show up in the tallies as well as in the missing notes.
	require.NoError(t, os.WriteFile(filepath.Join(elsewhere, "elsewhere"+pendingWriteSuffix), []byte(`{}`), 0o600))

	pending := filepath.Join(paths.Of(tp.Root).State, pendingWriteDir)
	require.NoError(t, os.MkdirAll(pending, 0o700))
	makeJunction(t, filepath.Join(pending, "linked"+pendingWriteSuffix), elsewhere)

	h := core.HashBytes(core.DomainChunk, []byte("junctioned object"))
	writeBareObject(t, tp, h)
	junctionInPlaceOf(t, tp.Store.objectPath(h))

	seedCapture(t, tp.Root, "junctioned-capture", auditOpObserveTool, false, core.OutcomeOK, []byte("x"))
	sidecar, err := CaptureSidecarPath(tp.Root, auditObsID("junctioned-capture"))
	require.NoError(t, err)
	junctionInPlaceOf(t, sidecar)

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.True(t, a.Incomplete)
	require.Subset(t, a.Notes, []string{
		"symlink or reparse point in the pending registry was not traversed",
		"symlink or reparse point in the object tree was not traversed",
		"symlink or reparse point in the capture tree was not traversed",
	})
	require.Zero(t, a.UnindexedObjectCandidates, "a junctioned leaf is not counted as an object")
	require.Zero(t, a.CapturesScanned, "a junctioned sidecar is not read as a capture")
}

// TestObservationGuards_JunctionedIndexRefused: an index/ that is a junction to a directory outside
// the project, holding an observations file of its own, leaves the observation sidecar uncertain;
// the reader never takes the outside file for the project's. The outside file is empty, which a
// reader that followed the junction would load cleanly (uncertain stays false): only the refusal
// itself can make the row pass.
func TestObservationGuards_JunctionedIndexRefused(t *testing.T) {
	tp := newTestStore(t)
	require.NoError(t, tp.Store.Close())
	index := paths.Of(tp.Root).Index
	require.NoError(t, os.Rename(index, index+"-original"))
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, observationsFile), nil, 0o600))
	makeJunction(t, index, destination)

	tp.Store.mu.Lock()
	tp.Store.loadObservationsLocked()
	uncertain := tp.Store.obsSidecarUncertain
	tp.Store.mu.Unlock()
	require.True(t, uncertain)
}

// TestMaintenanceRestore_RefusesADestinationDotThatIsAJunction: a restore never publishes through a
// junction; a .qompack that is one occupies the destination, the refusal names it as the reparse
// alias it is, and what it pointed at is untouched.
func TestMaintenanceRestore_RefusesADestinationDotThatIsAJunction(t *testing.T) {
	tp := newTestStore(t)
	seedRoot(t, tp, "src/a.ts", "a\n")
	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(context.Background(), "s1")
	require.NoError(t, err)

	dest := t.TempDir()
	target := t.TempDir()
	makeJunction(t, filepath.Join(dest, ".qompack"), target)
	_, err = x.Restore(context.Background(), "s1", dest)
	require.ErrorIs(t, err, ErrRestoreTargetExists)
	require.Contains(t, err.Error(), "is a special file or reparse alias")
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing is published through the junction")
}
