package paths_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// On Windows a case variant of a protected path is the protected file itself, so every write the
// §7.4 guard refuses under the canonical spelling must be refused under the variant too, and must
// leave the protected bytes as they were. Before the guard folded case, the first two rows below
// replaced a sealed checkpoint and emptied the pins log (w3-paths runs/fix/04).

// seedProtected writes one sealed checkpoint, a one-record pins log and a bloom, and returns their
// paths with a snapshot of their bytes.
func seedProtected(t *testing.T, l paths.Layout) map[string][]byte {
	t.Helper()
	cp := paths.CheckpointPath(l, 1)
	require.NoError(t, paths.CreateNew(cp, []byte(`{"seq":1}`)))
	pins := filepath.Join(l.Pins, "invariants.jsonl")
	require.NoError(t, paths.AppendJSONL(pins, map[string]string{"id": "inv-1"}))
	bloom := filepath.Join(l.Sketches, "tried.bloom")
	require.NoError(t, os.WriteFile(bloom, []byte("bloomdata"), 0o600))

	before := map[string][]byte{}
	for _, p := range []string{cp, pins, bloom} {
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		before[p] = b
	}
	return before
}

// requireUnchanged fails if any seeded protected file's bytes moved.
func requireUnchanged(t *testing.T, before map[string][]byte) {
	t.Helper()
	for p, want := range before {
		got, err := os.ReadFile(p)
		require.NoError(t, err)
		require.Equal(t, string(want), string(got), "%s must be byte-for-byte untouched", p)
	}
}

func TestAppendOnlyGuard_RefusesCaseVariantsOnWindows(t *testing.T) {
	l := newLayout(t)
	before := seedProtected(t, l)

	t.Run("WriteAtomic over an upper-case checkpoints directory", func(t *testing.T) {
		p := filepath.Join(l.Dot, "CHECKPOINTS", filepath.Base(paths.CheckpointPath(l, 1)))
		require.ErrorIs(t, paths.WriteAtomic(p, []byte(`{"seq":1,"tampered":true}`), 0o600), core.ErrAppendOnly)
	})
	t.Run("OpenFile O_TRUNC through an upper-case pins directory", func(t *testing.T) {
		f, err := paths.OpenFile(filepath.Join(l.Dot, "PINS", "invariants.jsonl"), os.O_WRONLY|os.O_TRUNC, 0o600)
		if err == nil {
			_ = f.Close()
		}
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})
	t.Run("OpenFile non-append write through a mixed-case pins directory", func(t *testing.T) {
		f, err := paths.OpenFile(filepath.Join(l.Dot, "Pins", "invariants.jsonl"), os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
		}
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})
	t.Run("OpenSharedRW through an upper-case pins directory", func(t *testing.T) {
		f, err := paths.OpenSharedRW(filepath.Join(l.Dot, "PINS", "invariants.jsonl"))
		if err == nil {
			_ = f.Close()
		}
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})
	t.Run("WriteAtomic over an upper-case bloom name", func(t *testing.T) {
		require.ErrorIs(t, paths.WriteAtomic(filepath.Join(l.Sketches, "TRIED.BLOOM"), []byte("replaced"), 0o600),
			core.ErrAppendOnly)
	})
	t.Run("WriteAtomic through every element in another case", func(t *testing.T) {
		p := filepath.Join(swapCase(l.Root), ".QOMPACK", "CHECKPOINTS", filepath.Base(paths.CheckpointPath(l, 1)))
		require.ErrorIs(t, paths.WriteAtomic(p, []byte(`{"seq":1,"tampered":true}`), 0o600), core.ErrAppendOnly)
	})

	requireUnchanged(t, before)

	// Folding must not cost a legitimate write: an append through a case variant is still an
	// append, which §7.4 allows, and lands in the one real file.
	t.Run("AppendJSONL through an upper-case pins directory still appends", func(t *testing.T) {
		pins := filepath.Join(l.Pins, "invariants.jsonl")
		require.NoError(t, paths.AppendJSONL(filepath.Join(l.Dot, "PINS", "invariants.jsonl"), map[string]string{"id": "inv-2"}))
		got, err := os.ReadFile(pins)
		require.NoError(t, err)
		require.Equal(t, string(before[pins])+`{"id":"inv-2"}`+"\n", string(got))
	})
}

// TestAppendOnlyGuard_RefusesTheBloomsDataStreamOnWindows covers the one NTFS stream spelling the
// textual guard could not see. tried.bloom::$DATA is the file's own unnamed data stream, so opening it
// with O_TRUNC truncates the bloom, and IsProtected matched sketches/tried.bloom only exactly. Files
// under checkpoints/ and pins/ were already caught by their directory prefix.
func TestAppendOnlyGuard_RefusesTheBloomsDataStreamOnWindows(t *testing.T) {
	l := newLayout(t)
	before := seedProtected(t, l)

	for _, name := range []string{"tried.bloom::$DATA", "TRIED.BLOOM::$data", "tried.bloom:extra"} {
		t.Run(name, func(t *testing.T) {
			f, err := paths.OpenFile(filepath.Join(l.Sketches, name), os.O_WRONLY|os.O_TRUNC, 0o600)
			if err == nil {
				_ = f.Close()
			}
			require.ErrorIs(t, err, core.ErrAppendOnly)
			require.ErrorIs(t, paths.WriteAtomic(filepath.Join(l.Sketches, name), []byte("replaced"), 0o600),
				core.ErrAppendOnly)
		})
	}
	t.Run("a checkpoint's data stream", func(t *testing.T) {
		p := paths.CheckpointPath(l, 1) + "::$DATA"
		f, err := paths.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0o600)
		if err == nil {
			_ = f.Close()
		}
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})

	requireUnchanged(t, before)
}
