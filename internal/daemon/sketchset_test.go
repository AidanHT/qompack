package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
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

// realLogger builds a logger whose Loud writes a durable LOUD.log line, and returns a reader for
// that file. The distinction is the whole point of the two tests below: sketch.Load hands
// LoadWithLog a logging.Nop, which still fires the process-wide Loud ring but writes NO line
// anywhere, so a test that watched the ring would pass against the very defect this pins.
func realLogger(t *testing.T, root string) (logging.Logger, func() string) {
	t.Helper()
	logDir := paths.Of(root).Logs
	log, closer, err := logging.New(logDir, logging.Debug)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closer.Close()) })
	return log, func() string {
		b, readErr := os.ReadFile(filepath.Join(logDir, "LOUD.log"))
		if os.IsNotExist(readErr) {
			return ""
		}
		require.NoError(t, readErr)
		return string(b)
	}
}

// TestSketchSetLoad_CorruptFileIsLoud is the defect the 2026-08-22 audit found: the daemon read
// sketches through the silent sketch.Load and then classified on core.ErrNotFound, which
// LoadWithLog returns for EVERY failure — so a CRC-failed sketch was filed under "expected, Debug"
// with no durable line written anywhere. §13 invariant 10 says degradation is loud.
func TestSketchSetLoad_CorruptFileIsLoud(t *testing.T) {
	root := t.TempDir()
	log, loudLog := realLogger(t, root)

	dir := paths.Of(root).Sketches
	require.NoError(t, os.MkdirAll(dir, 0o700))
	cms := sketch.NewCMS(0.001, 0.001)
	require.NoError(t, sketch.Save(filepath.Join(dir, touchSketchFile), cms))
	// Flip a payload byte: magic and version still read, the CRC no longer matches.
	p := filepath.Join(dir, touchSketchFile)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	b[len(b)-1] ^= 0xFF
	require.NoError(t, os.WriteFile(p, b, 0o600))

	s := NewSketchSet(config.Defaults())
	before := s.Touch
	s.Load(root, log)

	require.Same(t, before, s.Touch, "a failed load must keep the in-memory sketch")
	got := loudLog()
	require.NotEmpty(t, got, "a corrupt sketch must write a durable LOUD line, not only reach the ring")
	require.Contains(t, got, touchSketchFile)
}

// TestSketchSetLoad_AbsentFileIsQuiet is the other half, and the mutation guard for the test above:
// classifying everything as Loud would train an operator to ignore the channel corruption uses. A
// project's first session has no sketches at all and must say nothing.
func TestSketchSetLoad_AbsentFileIsQuiet(t *testing.T) {
	root := t.TempDir()
	log, loudLog := realLogger(t, root)

	s := NewSketchSet(config.Defaults())
	s.Load(root, log)

	require.Empty(t, loudLog(), "a cold start must not be loud")
}
