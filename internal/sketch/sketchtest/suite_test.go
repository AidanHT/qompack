package sketchtest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/sketch/sketchtest"
)

// These drivers are what keeps the conformance suite itself covered: every Run*Suite is exercised
// against the real sketch types this package ships, so a change to any of them that breaks the
// contract fails here rather than in the consumer that inherits the suite later (SP-09, SP-16).
//
// SP-01 shipped a fakeStubSketch and a set of *_StubIsSkipped drivers alongside these. Both are
// gone. They existed to prove the Rule W-1 skip machinery fired on a stub, and SP-03 deleted that
// machinery when it filled in the behaviour blocks — a test asserting that a skip happens is a test
// asserting that nothing was checked, and the names had already stopped describing what the
// functions did.

// The dimensions the drivers build their sketches at. They are Appendix A / Appendix C's worked
// values where those exist, so that a suite failure here is about the contract rather than about a
// shape no configuration would ever produce.
const (
	bloomSuiteCapacity = 10000
	bloomSuiteFPRate   = 0.01
	cmsSuiteEpsilon    = 0.01
	cmsSuiteDelta      = 0.01
	hllSuiteRegisters  = 2048
	misraGriesSuiteK   = 16
)

// TestRunSketchSuite_AgainstBloom runs the generic Sketch contract over sketch.Bloom. Each framed
// type gets its own driver function rather than four subtests of one: a require failure inside a
// suite halts the calling function, so lumping them together would silently stop after the first.
func TestRunSketchSuite_AgainstBloom(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.Bloom", func(t *testing.T) sketch.Sketch {
		return sketch.NewBloom(bloomSuiteCapacity, bloomSuiteFPRate)
	})
}

// TestRunSketchSuite_AgainstCMS runs the generic Sketch contract over sketch.CMS.
func TestRunSketchSuite_AgainstCMS(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.CMS", func(t *testing.T) sketch.Sketch {
		return sketch.NewCMS(cmsSuiteEpsilon, cmsSuiteDelta)
	})
}

// TestRunSketchSuite_AgainstHLL runs the generic Sketch contract over sketch.HLL.
func TestRunSketchSuite_AgainstHLL(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.HLL", func(t *testing.T) sketch.Sketch {
		return sketch.NewHLL(hllSuiteRegisters)
	})
}

// TestRunSketchSuite_AgainstMisraGries runs the generic Sketch contract over sketch.MisraGries.
func TestRunSketchSuite_AgainstMisraGries(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.MisraGries", func(t *testing.T) sketch.Sketch {
		return sketch.NewMisraGries(misraGriesSuiteK)
	})
}

// TestRunSketchSuite_AgainstSigSketch runs the generic Sketch contract over sketch.SigSketch, the
// framed form of a MinHash signature. It is included because KindMinHash reaches disk through the
// same CRC-checked container as the other four, so the envelope contract has to hold for it too.
func TestRunSketchSuite_AgainstSigSketch(t *testing.T) {
	sketchtest.RunSketchSuite(t, "sketch.SigSketch", func(t *testing.T) sketch.Sketch {
		return &sketch.SigSketch{
			Sig: sketch.MinHash([]byte("a conformance document, long enough to shingle"),
				sketch.MinHashOptions{Enabled: true}),
		}
	})
}

// TestRunBloomSuite_AgainstRealBloom exercises RunBloomSuite against sketch.NewBloom.
func TestRunBloomSuite_AgainstRealBloom(t *testing.T) {
	sketchtest.RunBloomSuite(t, "sketch.Bloom", func(t *testing.T) *sketch.Bloom {
		return sketch.NewBloom(bloomSuiteCapacity, bloomSuiteFPRate)
	})
}

// TestRunCMSSuite_AgainstRealCMS exercises RunCMSSuite against sketch.NewCMS.
func TestRunCMSSuite_AgainstRealCMS(t *testing.T) {
	sketchtest.RunCMSSuite(t, "sketch.CMS", func(t *testing.T) *sketch.CMS {
		return sketch.NewCMS(cmsSuiteEpsilon, cmsSuiteDelta)
	})
}

// TestRunHLLSuite_AgainstRealHLL exercises RunHLLSuite against sketch.NewHLL.
func TestRunHLLSuite_AgainstRealHLL(t *testing.T) {
	sketchtest.RunHLLSuite(t, "sketch.HLL", func(t *testing.T) *sketch.HLL {
		return sketch.NewHLL(hllSuiteRegisters)
	})
}

// TestRunMisraGriesSuite_AgainstRealMisraGries exercises RunMisraGriesSuite against
// sketch.NewMisraGries.
func TestRunMisraGriesSuite_AgainstRealMisraGries(t *testing.T) {
	sketchtest.RunMisraGriesSuite(t, "sketch.MisraGries", func(t *testing.T) *sketch.MisraGries {
		return sketch.NewMisraGries(misraGriesSuiteK)
	})
}

// TestRunMinHashSuite_AgainstRealMinHash exercises RunMinHashSuite against sketch.MinHash.
func TestRunMinHashSuite_AgainstRealMinHash(t *testing.T) {
	sketchtest.RunMinHashSuite(t, "sketch.MinHash", sketch.MinHash)
}
