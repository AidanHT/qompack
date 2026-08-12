package sketchtest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/sketch/sketchtest"
)

// fakeStubSketch mirrors the shape of an SP-01-style stub Sketch: Header returns the zero value
// and Marshal/UnmarshalBinary report core.ErrNotImplemented, exactly like this package's own
// Bloom/CMS/HLL/MisraGries stubs do today. It exists only to exercise RunSketchSuite before SP-03
// ships a real Sketch.
type fakeStubSketch struct{}

func (fakeStubSketch) Header() sketch.Header          { return sketch.Header{} }
func (fakeStubSketch) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }
func (fakeStubSketch) UnmarshalBinary(b []byte) error { return core.ErrNotImplemented }

// TestRunSketchSuite_StubIsSkipped proves RunSketchSuite's shape block passes against a stub
// Sketch and that its behaviour block is skipped with the exact Rule W-1 message.
func TestRunSketchSuite_StubIsSkipped(t *testing.T) {
	sketchtest.RunSketchSuite(t, "fake-stub", func(t *testing.T) sketch.Sketch {
		return fakeStubSketch{}
	})
}

// TestRunSketchSuite_AgainstQompackStubs exercises RunSketchSuite against every real stub type
// this package ships (Bloom, CMS, HLL, MisraGries), end to end, so a change to any of their stub
// behaviour that breaks the conformance suite is caught here rather than only once SP-03 lands.
// Each type gets its own test function: RunSketchSuite calls t.Skip on a stub, which halts the
// entire calling test function (not just one subtest), so lumping all four into one function
// would silently stop after the first.
func TestRunSketchSuite_AgainstQompackStub_Bloom(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.Bloom-stub", func(t *testing.T) sketch.Sketch {
		return sketch.NewBloom(1, 0.5)
	})
}

func TestRunSketchSuite_AgainstQompackStub_CMS(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.CMS-stub", func(t *testing.T) sketch.Sketch {
		return sketch.NewCMS(0.5, 0.5)
	})
}

func TestRunSketchSuite_AgainstQompackStub_HLL(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.HLL-stub", func(t *testing.T) sketch.Sketch {
		return sketch.NewHLL(1)
	})
}

func TestRunSketchSuite_AgainstQompackStub_MisraGries(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.MisraGries-stub", func(t *testing.T) sketch.Sketch {
		return sketch.NewMisraGries(1)
	})
}

// TestRunBloomSuite_StubIsSkipped exercises RunBloomSuite against the real sketch.NewBloom stub.
func TestRunBloomSuite_StubIsSkipped(t *testing.T) {
	sketchtest.RunBloomSuite(t, "sketch.Bloom-stub", func(t *testing.T) *sketch.Bloom {
		return sketch.NewBloom(bloomSuiteCapacity, bloomSuiteFPRate)
	})
}

const (
	bloomSuiteCapacity = 10000
	bloomSuiteFPRate   = 0.01
)

// TestRunCMSSuite_StubIsSkipped exercises RunCMSSuite against the real sketch.NewCMS stub.
func TestRunCMSSuite_StubIsSkipped(t *testing.T) {
	sketchtest.RunCMSSuite(t, "sketch.CMS-stub", func(t *testing.T) *sketch.CMS {
		return sketch.NewCMS(0.01, 0.01)
	})
}

// TestRunHLLSuite_StubIsSkipped exercises RunHLLSuite against the real sketch.NewHLL stub.
func TestRunHLLSuite_StubIsSkipped(t *testing.T) {
	sketchtest.RunHLLSuite(t, "sketch.HLL-stub", func(t *testing.T) *sketch.HLL {
		return sketch.NewHLL(hllSuiteRegisters)
	})
}

const hllSuiteRegisters = 2048

// TestRunMisraGriesSuite_StubIsSkipped exercises RunMisraGriesSuite against the real
// sketch.NewMisraGries stub.
func TestRunMisraGriesSuite_StubIsSkipped(t *testing.T) {
	sketchtest.RunMisraGriesSuite(t, "sketch.MisraGries-stub", func(t *testing.T) *sketch.MisraGries {
		return sketch.NewMisraGries(misraGriesSuiteK)
	})
}

const misraGriesSuiteK = 16

// TestRunMinHashSuite_StubIsSkipped exercises RunMinHashSuite against the real sketch.MinHash
// stub.
func TestRunMinHashSuite_StubIsSkipped(t *testing.T) {
	sketchtest.RunMinHashSuite(t, "sketch.MinHash-stub", sketch.MinHash)
}
