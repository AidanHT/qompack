//go:build windows

package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNTPathPreservesUNCServerAndShare is deliberately conversion-only: UNC fixtures must not
// make a network request merely to prove the object-manager spelling retains the server/share
// boundary that FileRenameInfoEx receives.
func TestNTPathPreservesUNCServerAndShare(t *testing.T) {
	const want = `\??\UNC\server\share\nested\state.bin`
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "UNC", path: `\\server\share\nested\state.bin`},
		{name: "extended UNC", path: `\\?\UNC\server\share\nested\state.bin`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ntPath(tc.path)
			require.True(t, ok)
			require.Equal(t, want, got)
		})
	}
}

func requireSentinelAndNoAtomicStages(t *testing.T, sentinel, want, tmp string) {
	t.Helper()
	got, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	require.Equal(t, want, string(got), "invalid path input must not replace the sentinel")
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), tempFilePrefix), "leftover staging file: %s", entry.Name())
	}
}

// TestWindowsPathAPIsRejectEmbeddedNULWithoutSideEffects keeps malformed paths on the error path
// before a source handle is opened, a destination is replaced, or WriteAtomic's temporary file is
// left behind. The sentinel and every fixture are test-owned under TempDir.
func TestWindowsPathAPIsRejectEmbeddedNULWithoutSideEffects(t *testing.T) {
	root := t.TempDir()
	layout := Of(root)
	require.NoError(t, EnsureLayout(layout))
	sentinel := filepath.Join(layout.State, "sentinel.bin")
	const before = "durable sentinel"
	require.NoError(t, os.WriteFile(sentinel, []byte(before), 0o600))

	t.Run("source", func(t *testing.T) {
		source := filepath.Join(layout.Tmp, "source.bin")
		require.NoError(t, os.WriteFile(source, []byte("staged source"), 0o600))

		err := posixReplace(source+"\x00suffix", sentinel)
		require.Error(t, err)
		requireSentinelAndNoAtomicStages(t, sentinel, before, layout.Tmp)
		require.NoError(t, os.Remove(source), "a rejected source path must not leave its handle open")
	})

	t.Run("destination", func(t *testing.T) {
		err := WriteAtomic(sentinel+"\x00suffix", []byte("replacement"), 0o600)
		require.Error(t, err)
		requireSentinelAndNoAtomicStages(t, sentinel, before, layout.Tmp)
	})

	t.Run("read", func(t *testing.T) {
		f, err := openShared(sentinel + "\x00suffix")
		require.Error(t, err)
		require.Nil(t, f)
		requireSentinelAndNoAtomicStages(t, sentinel, before, layout.Tmp)
	})
}
