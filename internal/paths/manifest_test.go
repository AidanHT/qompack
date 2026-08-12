package paths_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

func TestCheckpointPath_Format(t *testing.T) {
	l := newLayout(t)
	require.Equal(t, filepath.Join(l.Checkpoints, "0007.json"), paths.CheckpointPath(l, core.CheckpointSeq(7)))
	require.Equal(t, filepath.Join(l.Checkpoints, "0001.json"), paths.CheckpointPath(l, core.CheckpointSeq(1)))
}

func TestManifestPath_LivesUnderCheckpointsAndIsProtected(t *testing.T) {
	l := newLayout(t)
	require.Equal(t, filepath.Join(l.Checkpoints, "MANIFEST.jsonl"), paths.ManifestPath(l))
	require.True(t, paths.IsProtected(l.Root, paths.ManifestPath(l)))
}

func TestManifest_AppendAndRead(t *testing.T) {
	l := newLayout(t)

	entries := []paths.ManifestEntry{
		{Seq: 1, SHA256: "aaa", Bytes: 100, Created: 1000},
		{Seq: 2, SHA256: "bbb", Bytes: 200, Created: 2000},
		{Seq: 3, SHA256: "ccc", Bytes: 300, Created: 3000},
	}
	for _, e := range entries {
		require.NoError(t, paths.AppendManifest(l, e))
	}

	got, err := paths.ReadManifest(l)
	require.NoError(t, err)
	require.Equal(t, entries, got)
}

func TestManifest_ReadMissingReturnsEmptyNotError(t *testing.T) {
	l := newLayout(t)
	got, err := paths.ReadManifest(l)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestManifest_OnlyAppendManifestCanWriteIt(t *testing.T) {
	l := newLayout(t)
	require.NoError(t, paths.AppendManifest(l, paths.ManifestEntry{Seq: 1, SHA256: "x", Bytes: 1, Created: 1}))

	err := paths.WriteAtomic(paths.ManifestPath(l), []byte("tampered"), 0o600)
	require.ErrorIs(t, err, core.ErrAppendOnly)
}

func TestManifest_SkipsBlankLines(t *testing.T) {
	l := newLayout(t)
	// Written directly (bypassing AppendManifest) purely as a malformed-input fixture: a blank
	// line between two otherwise valid records.
	content := `{"seq":1,"sha256":"a","bytes":1,"created":1}` + "\n\n" +
		`{"seq":2,"sha256":"b","bytes":2,"created":2}` + "\n"
	require.NoError(t, os.WriteFile(paths.ManifestPath(l), []byte(content), 0o600))

	got, err := paths.ReadManifest(l)
	require.NoError(t, err)
	require.Equal(t, []paths.ManifestEntry{
		{Seq: 1, SHA256: "a", Bytes: 1, Created: 1},
		{Seq: 2, SHA256: "b", Bytes: 2, Created: 2},
	}, got)
}

func TestManifest_ReadCorruptLineErrors(t *testing.T) {
	l := newLayout(t)
	require.NoError(t, os.WriteFile(paths.ManifestPath(l), []byte("not valid json\n"), 0o600))

	_, err := paths.ReadManifest(l)
	require.Error(t, err)
}

func TestManifest_ReadOversizedLineErrors(t *testing.T) {
	l := newLayout(t)
	// A single line well past bufio.Scanner's default token limit (64 KiB); the scanner must
	// fail rather than truncate it silently.
	huge := `{"seq":1,"sha256":"` + strings.Repeat("a", 70000) + `"}`
	require.NoError(t, os.WriteFile(paths.ManifestPath(l), []byte(huge+"\n"), 0o600))

	_, err := paths.ReadManifest(l)
	require.Error(t, err)
}

// TestManifest_ReadPropagatesNonNotExistOpenError drives ReadManifest's own os.Open failure path
// for an error that is NOT "does not exist" (a NUL byte anywhere in a Windows path is invalid,
// which os.IsNotExist correctly does not classify as a missing file). A Layout can be built
// purely from strings via Of, with no filesystem access, so this needs no real .qompack tree.
func TestManifest_ReadPropagatesNonNotExistOpenError(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("platform: this exercises the windows-specific invalid-path error path")
	}
	l := paths.Of("root\x00nul")

	_, err := paths.ReadManifest(l)
	require.Error(t, err)
}
