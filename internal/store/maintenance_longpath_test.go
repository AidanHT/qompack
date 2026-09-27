package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// A project whose .qompack path is past Windows' legacy MAX_PATH is an ordinary project: a user's
// directory nesting decides it, not Qompack. Every file the store touches there is reached through
// paths.Long's \\?\ spelling, and every maintenance operation must still work on it (C1.7,
// V6-RECOVERY-2). These rows are platform-neutral; off Windows Long is the identity and they pin
// only that nothing else about the depth matters.
//
// The defect they were written for: TakeBackup walked paths.Long(Dot), so every walked path carried
// the \\?\ prefix, and handed that spelling to the copy, whose no-follow check computed
// filepath.Rel against the unprefixed project root. filepath.Rel cannot relate two volume
// spellings, so `qompack backup create` failed with "Rel: can't make \\?\... relative to ..." on
// every such project (w3-paths runs/15, runs/16).

// deepRootMinLen is how long the deep project root must be. It is past MAX_PATH (260), so the root,
// its .qompack and everything under them are beyond it and paths.Long prefixes every one of them —
// not a borderline depth that only the longest object path crosses.
const deepRootMinLen = 270

// deepPathSegment is repeated to build that depth. It is well under NTFS's 255-character component
// limit and names itself, so a failure message shows at once why the path is long.
const deepPathSegment = "a-deliberately-long-directory-name-to-pass-max-path"

// deepDir returns a directory below base whose path is at least minLen characters, creating it.
func deepDir(t *testing.T, base string, minLen int) string {
	t.Helper()
	dir := base
	for len(dir) < minLen {
		dir = filepath.Join(dir, deepPathSegment)
	}
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	return dir
}

// newDeepProject is newProject with the project root placed past MAX_PATH. HOME, USERPROFILE and
// QOMPACK_HOME are redirected for newProject's reason: store.Open's token estimator resolves the
// user-global calibration file.
func newDeepProject(t *testing.T) *project {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	require.NoError(t, os.MkdirAll(paths.Long(home), 0o700))
	root := deepDir(t, filepath.Join(base, "project"), deepRootMinLen)
	t.Setenv("QOMPACK_PROJECT_ROOT", root)
	t.Setenv("QOMPACK_HOME", filepath.Join(home, ".qompack"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	return &project{Root: root, Cfg: config.Defaults(), Clock: newFakeClock(), Log: logging.Nop()}
}

func TestMaintenance_BackupVerifyRestoreBeyondMaxPath(t *testing.T) {
	tp := openOver(t, newDeepProject(t))
	require.GreaterOrEqual(t, len(paths.Of(tp.Root).Dot), deepRootMinLen,
		"fixture sanity: the store itself must sit past MAX_PATH")
	ctx := context.Background()
	a := seedRoot(t, tp, "src/deep/a.ts", "payload a — written below a project past MAX_PATH\n")
	b := seedRoot(t, tp, "src/deep/b.ts", "payload b — the second root, for a non-vacuous proof\n")

	x := newMaint(t, tp, leaseOK)
	man, err := x.TakeBackup(ctx, "deep")
	require.NoError(t, err, "backup create must work on a project whose .qompack is past MAX_PATH")
	require.NotEmpty(t, man.Files)
	for _, f := range man.Files {
		require.NotContains(t, f.Name, `\\?\`, "a manifest name is relative to .qompack, never a syscall spelling")
		require.False(t, strings.HasPrefix(f.Name, "/") || filepath.IsAbs(f.Name), "manifest name %q is not relative", f.Name)
	}
	require.NoFileExists(t, paths.Long(x.pendingCertification("deep")), "a certified backup leaves no pending marker")

	verified, err := x.VerifyBackup("deep")
	require.NoError(t, err, "backup verify must work past MAX_PATH")
	require.Equal(t, man.Files, verified.Files)

	// VerifyBackupAt is the read-only CLI path that opens no store.
	_, err = VerifyBackupAt(ctx, tp.Root, "deep")
	require.NoError(t, err)

	// The destination is past MAX_PATH too, so the staging tree, the no-replace publish and the
	// read-only reader proof all run on prefixed paths.
	dest := deepDir(t, filepath.Join(t.TempDir(), "restored"), deepRootMinLen)
	proof, err := x.Restore(ctx, "deep", dest)
	require.NoError(t, err, "backup restore must work past MAX_PATH; note: %s", proof.Note)
	require.True(t, proof.OpenedOK)
	require.GreaterOrEqual(t, proof.ContentRootsProven, 2, "both seeded roots must read back")

	rs := openPlain(t, dest, tp.Cfg, tp.Clock)
	for _, h := range []core.Hash{a, b} {
		got, gerr := readRoot(ctx, rs, h)
		require.NoError(t, gerr, "the restored store must serve root %s", h.Short())
		require.NotEmpty(t, got)
	}
}

// TestGC_RunsBeyondMaxPath runs a real collection over a store past MAX_PATH: the sweep walks
// paths.Long(objects), relates each walked path to that same prefixed base and removes it by that
// spelling, and the mark's retention reads go through maintNoFollow against the project root. A
// prefixed/unprefixed mismatch in either would surface as a halted mark or an object left behind.
func TestGC_RunsBeyondMaxPath(t *testing.T) {
	tp := openOver(t, newDeepProject(t))
	ctx := context.Background()
	seedRoot(t, tp, "src/deep/live.ts", "a root the collection must keep\n")

	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: 1, RetainSessions: 1})
	require.NoError(t, err, "gc must run on a store past MAX_PATH")
	require.False(t, rep.Truncated, "an undisturbed pass over a tiny store must finish")
	require.Positive(t, rep.ScannedObjects, "the sweep must have walked the prefixed objects tree")
}
