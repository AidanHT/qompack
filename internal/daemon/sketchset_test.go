package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// TestSketchSetNeverWritesTriedBloom pins §3.3: Save must never touch sketches/tried.bloom, which
// may be replaced only by negknow.RebuildBloom.
func TestSketchSetNeverWritesTriedBloom(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(l.Sketches, 0o700))

	triedPath := filepath.Join(l.Sketches, triedSketchFile)
	want := []byte("pre-existing tried.bloom bytes")
	require.NoError(t, os.WriteFile(triedPath, want, 0o600))
	fiBefore, err := os.Stat(triedPath)
	require.NoError(t, err)

	s := NewSketchSet(config.Defaults())
	s.Write(func(s *SketchSet) { _ = s.Tried }) // mark dirty
	s.Save(root, logging.Nop())

	got, err := os.ReadFile(triedPath)
	require.NoError(t, err)
	require.Equal(t, want, got, "Save must never rewrite tried.bloom")

	fiAfter, err := os.Stat(triedPath)
	require.NoError(t, err)
	require.Equal(t, fiBefore.ModTime(), fiAfter.ModTime(), "tried.bloom's mtime must be untouched")
}

// TestSketchSet_CarriesAllFourStructures (constructor variant) pins NewSketchSet produces all four
// non-nil resident structures from config.
func TestNewSketchSet_ConstructsAllFour(t *testing.T) {
	t.Parallel()

	s := NewSketchSet(config.Defaults())
	require.NotNil(t, s.Tried)
	require.NotNil(t, s.Touch)
	require.NotNil(t, s.Explore)
	require.NotNil(t, s.Top)
}

// TestSketchSetSaveIsNoopWhenNotDirty pins that Save does nothing (writes nothing) until Write has
// been called at least once.
func TestSketchSetSaveIsNoopWhenNotDirty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewSketchSet(config.Defaults())
	s.Save(root, logging.Nop()) // never written to: must be a no-op

	_, err := os.Stat(filepath.Join(paths.Of(root).Sketches, touchSketchFile))
	require.True(t, os.IsNotExist(err), "an untouched SketchSet must not create touch.cms")
}

// TestSketchSetLoadToleratesStubSketchPackage pins that Load never panics and keeps the
// in-memory sketch when sketch.Load reports core.ErrNotImplemented (SP-03's current stub).
func TestSketchSetLoadToleratesStubSketchPackage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := NewSketchSet(config.Defaults())
	before := s.Touch
	require.NotPanics(t, func() { s.Load(root, logging.Nop()) })
	require.Same(t, before, s.Touch, "a stubbed sketch.Load must not replace the in-memory sketch")
}
