package paths_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// These tests pin which store owns a path, which is what WriteAtomic stages in and what the §7.4
// guard measures a path against. The owner is the nearest .qompack that is an element of the
// path's own directory chain. A .qompack that merely sits in some ancestor directory beside the
// path, like the user-global layer in a home directory above a project, owns nothing below it.
//
// Every fixture builds its own stand-in home under t.TempDir(). None of them reads or writes the
// real user's ~/.qompack.

// fakeUserStore makes <home>/.qompack the way a real user's home holds it after the plugin has
// run there (the user-global config layer, the calibration file), and returns home.
func fakeUserStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(paths.Global(home), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Global(home), "calibration.json"), []byte("{}"), 0o600))
	return home
}

// storeListing returns every path under dir, relative to it and sorted, so a test can assert that
// a write elsewhere left dir exactly as it was.
func storeListing(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	}))
	sort.Strings(out)
	return out
}

// TestWriteAtomic_NeverStagesInAStoreThatDoesNotHoldTheTarget is the w2-lifetime runs/21 failure.
// With a .qompack in the home directory, a write into a project below it that has no store yet
// staged in <home>/.qompack/tmp. The rename out of there then failed, because the target's
// directory had never been made ("The system cannot find the path specified"), and on a target
// on another filesystem than the home it would fail with a cross-device error instead. Each case
// must write where it did before a home store existed, and leave the home store untouched.
func TestWriteAtomic_NeverStagesInAStoreThatDoesNotHoldTheTarget(t *testing.T) {
	cases := []struct {
		name   string
		target func(t *testing.T, project string) string
	}{
		{
			// ipc.WriteState and the cli rows that failed: state into a project with no layout.
			name: "a project's first store file before its store exists",
			target: func(_ *testing.T, project string) string {
				return filepath.Join(paths.Of(project).Run, "state.bin")
			},
		},
		{
			// eval's corpus writer and importer write into an operator's directory like this one.
			name: "a file in no store at all",
			target: func(t *testing.T, project string) string {
				dir := filepath.Join(project, "corpus")
				require.NoError(t, os.MkdirAll(dir, 0o700))
				return filepath.Join(dir, "session.json")
			},
		},
		{
			name: "a file beside a project store but outside it",
			target: func(t *testing.T, project string) string {
				require.NoError(t, paths.EnsureLayout(paths.Of(project)))
				return filepath.Join(project, "notes.txt")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := fakeUserStore(t)
			before := storeListing(t, paths.Global(home))
			project := filepath.Join(home, "src", "project")
			require.NoError(t, os.MkdirAll(project, 0o700))
			target := tc.target(t, project)

			require.NoError(t, paths.WriteAtomic(target, []byte("payload"), 0o600))

			got, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "payload", string(got))
			require.Equal(t, before, storeListing(t, paths.Global(home)),
				"a write outside the user-global store must not stage in it or create anything in it")
		})
	}
}

// TestWriteAtomic_AProjectStoreBelowAUserStoreIsItsOwnOwner pins the ordinary product path under a
// home store: a project's own store files stage in that project's .qompack/tmp and nothing lands
// in the home's store.
func TestWriteAtomic_AProjectStoreBelowAUserStoreIsItsOwnOwner(t *testing.T) {
	home := fakeUserStore(t)
	before := storeListing(t, paths.Global(home))
	l := paths.Of(filepath.Join(home, "src", "project"))
	require.NoError(t, paths.EnsureLayout(l))
	target := filepath.Join(l.State, "store.json")

	require.NoError(t, paths.WriteAtomic(target, []byte("state"), 0o600))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "state", string(got))
	require.Equal(t, before, storeListing(t, paths.Global(home)))
	entries, err := os.ReadDir(l.Tmp)
	require.NoError(t, err)
	require.Empty(t, entries, "the project's staging directory holds no debris after a write")
}

// TestWriteAtomic_TheUserGlobalLayerWritesInItsOwnStore pins the other product path: the
// calibration file and anything else under paths.Global(home) is owned by that store, and a
// project store elsewhere under the home is not touched by it.
func TestWriteAtomic_TheUserGlobalLayerWritesInItsOwnStore(t *testing.T) {
	home := fakeUserStore(t)
	l := paths.Of(filepath.Join(home, "src", "project"))
	require.NoError(t, paths.EnsureLayout(l))
	projectBefore := storeListing(t, l.Dot)
	target := filepath.Join(paths.Global(home), "calibration.json")

	require.NoError(t, paths.WriteAtomic(target, []byte(`{"k":1.1}`), 0o600))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, `{"k":1.1}`, string(got))
	require.Equal(t, projectBefore, storeListing(t, l.Dot))
}

// TestWriteAtomic_AStrayStoreInsideAProtectedDirectoryCannotUnprotectIt pins the §7.4 guard's
// side of the same rule. A directory named .qompack inside checkpoints/, pins/ or sketches/ is
// beside the protected files, not on their path, so it cannot become their owner. The guard once
// measured a checkpoint against such a directory, found it outside that "store", and let
// WriteAtomic replace the sealed file.
func TestWriteAtomic_AStrayStoreInsideAProtectedDirectoryCannotUnprotectIt(t *testing.T) {
	l := newLayout(t)
	cp := paths.CheckpointPath(l, 1)
	require.NoError(t, paths.CreateNew(cp, []byte(`{"seq":1}`)))
	pins := filepath.Join(l.Pins, "invariants.jsonl")
	require.NoError(t, paths.AppendJSONL(pins, map[string]string{"id": "inv-1"}))
	pinsBefore, err := os.ReadFile(pins)
	require.NoError(t, err)
	bloom := filepath.Join(l.Sketches, "tried.bloom")
	require.NoError(t, os.WriteFile(bloom, []byte("bloomdata"), 0o600))
	for _, dir := range []string{l.Checkpoints, l.Pins, l.Sketches} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".qompack"), 0o700))
	}

	require.ErrorIs(t, paths.WriteAtomic(cp, []byte(`{"seq":1,"tampered":true}`), 0o600), core.ErrAppendOnly)
	require.ErrorIs(t, paths.WriteAtomic(bloom, []byte("replacement"), 0o600), core.ErrAppendOnly)
	_, err = paths.OpenFile(pins, os.O_WRONLY|os.O_TRUNC, 0o600)
	require.ErrorIs(t, err, core.ErrAppendOnly)
	_, err = paths.OpenSharedRW(pins)
	require.ErrorIs(t, err, core.ErrAppendOnly)

	got, err := os.ReadFile(cp)
	require.NoError(t, err)
	require.Equal(t, `{"seq":1}`, string(got))
	got, err = os.ReadFile(pins)
	require.NoError(t, err)
	require.Equal(t, pinsBefore, got)
	got, err = os.ReadFile(bloom)
	require.NoError(t, err)
	require.Equal(t, "bloomdata", string(got))
}

// TestWriteAtomic_RefusesAProtectedPathNamedRelatively pins that the guard asks about the path it
// is given, however it is spelled. A relative path was measured against the absolute root as
// given, filepath.Rel cannot relate the two, and the guard answered "not protected".
func TestWriteAtomic_RefusesAProtectedPathNamedRelatively(t *testing.T) {
	l := newLayout(t)
	cp := paths.CheckpointPath(l, 1)
	require.NoError(t, paths.CreateNew(cp, []byte(`{"seq":1}`)))
	pins := filepath.Join(l.Pins, "invariants.jsonl")
	require.NoError(t, paths.AppendJSONL(pins, map[string]string{"id": "inv-1"}))
	t.Chdir(l.Root)
	relCP, err := filepath.Rel(l.Root, cp)
	require.NoError(t, err)
	relPins, err := filepath.Rel(l.Root, pins)
	require.NoError(t, err)

	require.ErrorIs(t, paths.WriteAtomic(relCP, []byte(`{"seq":1,"tampered":true}`), 0o600), core.ErrAppendOnly)
	_, err = paths.OpenFile(relPins, os.O_WRONLY|os.O_TRUNC, 0o600)
	require.ErrorIs(t, err, core.ErrAppendOnly)
	_, err = paths.OpenSharedRW(relPins)
	require.ErrorIs(t, err, core.ErrAppendOnly)

	got, err := os.ReadFile(cp)
	require.NoError(t, err)
	require.Equal(t, `{"seq":1}`, string(got))
}

// TestWriteAtomic_AStrayStoreOnThePathCannotUnprotectWhatIsBelowIt is the other half of the stray
// .qompack rule. A stray directory inside checkpoints/ or pins/ is the NEAREST store on the path of
// anything written through it, so it owns such a write; but the real store's checkpoints/ and
// pins/ still enclose the path, and the §7.4 guard asks every store on the path, not only the
// owner. Asked of the stray alone, the guard found the write outside its checkpoints/ and let it
// through.
func TestWriteAtomic_AStrayStoreOnThePathCannotUnprotectWhatIsBelowIt(t *testing.T) {
	l := newLayout(t)
	strayCheckpoints := filepath.Join(l.Checkpoints, ".qompack")
	strayPins := filepath.Join(l.Pins, ".qompack")
	for _, dir := range []string{strayCheckpoints, strayPins} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
	}
	throughCheckpoints := filepath.Join(strayCheckpoints, "0002.json")
	throughPins := filepath.Join(strayPins, "invariants.jsonl")
	require.NoError(t, paths.AppendJSONL(throughPins, map[string]string{"id": "inv-1"}))
	pinsBefore, err := os.ReadFile(throughPins)
	require.NoError(t, err)

	require.ErrorIs(t, paths.WriteAtomic(throughCheckpoints, []byte(`{"seq":2}`), 0o600), core.ErrAppendOnly)
	_, err = paths.OpenFile(throughPins, os.O_WRONLY|os.O_TRUNC, 0o600)
	require.ErrorIs(t, err, core.ErrAppendOnly)
	_, err = paths.OpenSharedRW(throughPins)
	require.ErrorIs(t, err, core.ErrAppendOnly)

	require.NoFileExists(t, throughCheckpoints, "a refused WriteAtomic must leave nothing behind")
	got, err := os.ReadFile(throughPins)
	require.NoError(t, err)
	require.Equal(t, pinsBefore, got)
}

// TestWriteAtomic_AStorelessProjectInsideAnotherStoresTreeStagesBesideItself pins that the owner is
// the NEAREST .qompack on the path, whether or not it exists yet. A project whose own store has not
// been made is owned by no store, even when its whole tree lies inside another store's, so its first
// write stages beside itself and makes its directory, exactly as it does with no store above it.
// Passing over the missing store to the enclosing one staged in that store's tmp/ and then failed
// the rename, because the project's directory had never been made.
func TestWriteAtomic_AStorelessProjectInsideAnotherStoresTreeStagesBesideItself(t *testing.T) {
	outer := newLayout(t)
	project := filepath.Join(outer.Eval, "replay", "project")
	require.NoError(t, os.MkdirAll(project, 0o700))
	outerTmpBefore := storeListing(t, outer.Tmp)
	target := filepath.Join(paths.Of(project).Run, "state.bin")

	require.NoError(t, paths.WriteAtomic(target, []byte("state"), 0o600))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "state", string(got))
	require.Equal(t, outerTmpBefore, storeListing(t, outer.Tmp),
		"a write owned by no store must not stage in the store that encloses it")
}
