package paths_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// barrierLog is a paths.Barriers that records every barrier as file:<base> or dir:<base>, performs
// the real call, and can fail the n-th one instead of performing it.
type barrierLog struct {
	steps  []string
	failAt int
}

var errBarrier = errors.New("injected barrier failure")

func (b *barrierLog) barriers() paths.Barriers {
	return paths.Barriers{
		SyncFile: func(f *os.File) error {
			if b.record("file:" + filepath.Base(f.Name())) {
				return errBarrier
			}
			return f.Sync()
		},
		SyncDir: func(dir string) error {
			if b.record("dir:" + filepath.Base(dir)) {
				return errBarrier
			}
			return paths.SyncDir(dir)
		},
	}
}

// record appends step and reports whether it is the one to fail.
func (b *barrierLog) record(step string) bool {
	b.steps = append(b.steps, step)
	return len(b.steps) == b.failAt
}

// TestAppendManifest_SyncsTheArtifactNameBeforeTheLine pins AppendManifest's barrier order: the
// checkpoints directory (the indexed artifact's name) before the line, the line's own sync, and a
// second directory sync only when the append created the manifest.
func TestAppendManifest_SyncsTheArtifactNameBeforeTheLine(t *testing.T) {
	l := newLayout(t)
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(l, 1), []byte(`{"seq":1}`)))

	first := &barrierLog{}
	require.NoError(t, first.barriers().AppendManifest(l, paths.ManifestEntry{Seq: 1, SHA256: "a", Bytes: 9, Created: 1}))
	require.Equal(t, []string{"dir:checkpoints", "file:MANIFEST.jsonl", "dir:checkpoints"}, first.steps,
		"the append that creates the manifest syncs the directory again for the manifest's own name")

	second := &barrierLog{}
	require.NoError(t, second.barriers().AppendManifest(l, paths.ManifestEntry{Seq: 2, SHA256: "b", Bytes: 9, Created: 2}))
	require.Equal(t, []string{"dir:checkpoints", "file:MANIFEST.jsonl"}, second.steps,
		"an existing manifest's name was made durable by the first directory sync; no second one")

	got, err := paths.ReadManifest(l)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

// TestAppendManifest_ABarrierFailureWritesNoLaterStep: a failed barrier stops the append where it
// is. A failed directory sync appends no line, so no line can name an artifact whose name is not
// durable; a failed line sync reports the failure rather than a seal.
func TestAppendManifest_ABarrierFailureWritesNoLaterStep(t *testing.T) {
	l := newLayout(t)
	b := &barrierLog{failAt: 1}
	err := b.barriers().AppendManifest(l, paths.ManifestEntry{Seq: 1, SHA256: "a", Bytes: 1, Created: 1})
	require.ErrorIs(t, err, errBarrier)
	_, statErr := os.Stat(paths.Long(paths.ManifestPath(l)))
	require.ErrorIs(t, statErr, os.ErrNotExist, "no line is appended once the artifact-name barrier fails")

	b = &barrierLog{failAt: 2}
	err = b.barriers().AppendManifest(l, paths.ManifestEntry{Seq: 1, SHA256: "a", Bytes: 1, Created: 1})
	require.ErrorIs(t, err, errBarrier, "a line whose sync failed is not reported sealed")
	require.Equal(t, []string{"dir:checkpoints", "file:MANIFEST.jsonl"}, b.steps, "nothing runs after the failed barrier")
}

// TestAppendJSONLDurable_SyncsTheLineAndANewFilesName pins the durable append: the line's sync
// always, and the directory's sync exactly when the append created the file.
func TestAppendJSONLDurable_SyncsTheLineAndANewFilesName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log.jsonl")

	created := &barrierLog{}
	require.NoError(t, created.barriers().AppendJSONLDurable(p, map[string]int{"n": 1}))
	require.Equal(t, []string{"file:log.jsonl", "dir:" + filepath.Base(dir)}, created.steps)

	existing := &barrierLog{}
	require.NoError(t, existing.barriers().AppendJSONLDurable(p, map[string]int{"n": 2}))
	require.Equal(t, []string{"file:log.jsonl"}, existing.steps)

	raw, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.Equal(t, "{\"n\":1}\n{\"n\":2}\n", string(raw), "the same bytes AppendJSONL would append")
}

// TestAppendJSONLDurable_KeepsAppendJSONLsGuards: the durable append is AppendJSONL's append plus
// barriers, so it refuses what AppendJSONL refuses and terminates a torn tail the same way.
func TestAppendJSONLDurable_KeepsAppendJSONLsGuards(t *testing.T) {
	dir := t.TempDir()
	require.Error(t, paths.AppendJSONLDurable(filepath.Join(dir, "view.json"), 1),
		"only an append-only extension may be appended to")

	p := filepath.Join(dir, "torn.jsonl")
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(`{"n":1}`+"\n"+`{"torn`), 0o600))
	require.NoError(t, paths.AppendJSONLDurable(p, map[string]int{"n": 2}))
	raw, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.Equal(t, "{\"n\":1}\n{\"torn\n{\"n\":2}\n", string(raw), "the torn tail is terminated, not glued to")
}

// TestCreateNew_SyncsItsBytesThroughTheBarriers: CreateNew's one barrier is the file's own sync.
// It deliberately does not sync the directory (CreateNew's comment says why); the caller whose file
// must keep its name does, as AppendManifest does for a checkpoint artifact.
func TestCreateNew_SyncsItsBytesThroughTheBarriers(t *testing.T) {
	l := newLayout(t)
	b := &barrierLog{}
	require.NoError(t, b.barriers().CreateNew(paths.CheckpointPath(l, 3), []byte(`{"seq":3}`)))
	require.Equal(t, []string{"file:0003.json"}, b.steps)

	failing := &barrierLog{failAt: 1}
	err := failing.barriers().CreateNew(paths.CheckpointPath(l, 4), []byte(`{"seq":4}`))
	require.ErrorIs(t, err, errBarrier, "a file whose sync failed is not reported written")
}

// TestAppendLinesDurable_SyncsABatchOnce: a batch of records is one write and one sync (and one
// directory sync when it creates the file), and a batch that does not end in a newline is refused
// before anything is written.
func TestAppendLinesDurable_SyncsABatchOnce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "roots.jsonl")

	b := &barrierLog{}
	require.NoError(t, b.barriers().AppendLinesDurable(p, []byte("{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")))
	require.Equal(t, []string{"file:roots.jsonl", "dir:" + filepath.Base(dir)}, b.steps)

	require.Error(t, paths.AppendLinesDurable(p, []byte(`{"n":4}`)), "an unterminated batch is refused")
	require.Error(t, paths.AppendLinesDurable(p, nil), "an empty batch is refused")
	raw, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.Equal(t, "{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n", string(raw), "a refused batch writes nothing")
}
