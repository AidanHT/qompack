// Package sketchtest is the conformance suite for the sketch package (00-ARCHITECTURE.md §5.22):
// every implementation SP-03 ships must pass RunSketchSuite, plus the per-sketch-type suites below
// (RunBloomSuite, RunCMSSuite, RunHLLSuite, RunMisraGriesSuite, RunMinHashSuite) that exercise the
// accuracy guarantee specific to each algorithm. SP-01 ships every suite, including the behaviour
// assertions SP-03 inherits (Rule W-1) — only each guarded /behaviour block is skipped until a
// real implementation lands.
package sketchtest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// RunSketchSuite is the conformance suite for the generic sketch.Sketch interface. name
// distinguishes multiple factories run in the same test binary; factory must return a fresh,
// ready-to-use Sketch on every call, and every call must return the SAME concrete type (the
// round-trip and CRC-rejection cases below construct a second instance via factory and expect it
// to be compatible with the first's encoded bytes).
func RunSketchSuite(t *testing.T, name string, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)

		// Header has no error return; any Header it produces, including the zero value, is
		// shape-valid.
		_ = s.Header()

		_, err := s.MarshalBinary()
		requireKnownError(t, err)
		requireKnownError(t, s.UnmarshalBinary(nil))

		dir := t.TempDir()
		requireKnownError(t, sketch.Save(filepath.Join(dir, "shape-probe.bin"), s))
		requireKnownError(t, sketch.Load(filepath.Join(dir, "shape-probe.bin"), s))
	})

	if skipIfStubSketch(t, factory(t)) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("marshal_unmarshal_round_trip", func(t *testing.T) { runRoundTripCase(t, factory) })
		t.Run("crc_rejection", func(t *testing.T) { runCRCRejectionCase(t, factory) })
	})
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// stub and every real implementation is allowed to return from an operation
// (00-ARCHITECTURE.md §5.22; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// isStubSketch reports whether s is still a stub, using MarshalBinary as the probe
// (plans/OWNERS.tsv: sketch's probe method is MarshalBinary). MarshalBinary has an error return,
// so — unlike chunk, symbols, redact and grammar's suites — this can check core.IsNotImplemented
// directly rather than relying on an inferred zero-value heuristic.
func isStubSketch(t *testing.T, s sketch.Sketch) bool {
	t.Helper()
	_, err := s.MarshalBinary()
	return core.IsNotImplemented(err)
}

// skipIfStubSketch calls t.Skip with the exact Rule W-1 message when s is still a stub, and
// reports whether it did. It is shared by every suite in this package (RunSketchSuite and every
// per-type suite below), since Bloom, CMS, HLL and MisraGries all implement sketch.Sketch and so
// all share the same MarshalBinary probe.
func skipIfStubSketch(t *testing.T, s sketch.Sketch) bool {
	t.Helper()
	if isStubSketch(t, s) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// runRoundTripCase asserts that marshaling one Sketch and unmarshaling the result into a second,
// freshly constructed instance reproduces an equal Header.
func runRoundTripCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()
	s1 := factory(t)
	data, err := s1.MarshalBinary()
	require.NoError(t, err)
	require.NotEmpty(t, data, "fixture sanity: MarshalBinary must produce a non-empty encoding")

	s2 := factory(t)
	require.NoError(t, s2.UnmarshalBinary(data))
	require.Equal(t, s1.Header(), s2.Header(),
		"UnmarshalBinary(MarshalBinary(s)) must reproduce an equal Header")
}

// runCRCRejectionCase asserts Load rejects a corrupted sketch file (00-ARCHITECTURE.md §5.7:
// "CRC + version checked; corrupt → ErrNotFound").
func runCRCRejectionCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()
	s := factory(t)
	p := filepath.Join(t.TempDir(), "sketch.bin")
	require.NoError(t, sketch.Save(p, s))

	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	require.NotEmpty(t, raw, "fixture sanity: Save must write a non-empty file")

	corrupted := append([]byte(nil), raw...)
	corrupted[len(corrupted)-1] ^= 0xFF
	require.NoError(t, os.WriteFile(p, corrupted, 0o600))

	s2 := factory(t)
	err = sketch.Load(p, s2)
	require.Error(t, err, "Load must reject a corrupted sketch file")
	require.ErrorIs(t, err, core.ErrNotFound, "§5.7: a corrupt sketch file reports ErrNotFound")
}
