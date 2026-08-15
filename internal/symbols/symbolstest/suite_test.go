package symbolstest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/symbols/symbolstest"
)

// fakeStubExtractor mirrors the shape of an SP-01-style stub Extractor: every operation reports
// the documented zero value, exactly like symbols.New's own stub does today. It exists only to
// exercise RunExtractorSuite before SP-04 ships a real Extractor.
type fakeStubExtractor struct{}

func (fakeStubExtractor) Extract(path string, b []byte) []symbols.Symbol { return nil }

func (fakeStubExtractor) Enclosing(path string, b []byte, off int) (symbols.Symbol, bool) {
	return symbols.Symbol{}, false
}

func (fakeStubExtractor) References(b []byte, names []string) map[string]int { return nil }

// TestRunExtractorSuite_StubIsSkipped proves the suite's shape block passes against a stub
// Extractor and that its behaviour block is skipped with the exact Rule W-1 message. SP-04 reuses
// RunExtractorSuite unchanged, pointed at its real implementation, to flip that skip off.
func TestRunExtractorSuite_StubIsSkipped(t *testing.T) {
	symbolstest.RunExtractorSuite(t, "fake-stub", func(t *testing.T) symbols.Extractor {
		return fakeStubExtractor{}
	})
}

// TestRunExtractorSuite_AgainstQompackStub exercises RunExtractorSuite against the real
// symbols.New stub, end to end, so a change to symbols.New's stub behaviour that breaks the
// conformance suite is caught here rather than only once SP-04 lands.
func TestRunExtractorSuite_AgainstQompackStub(t *testing.T) {
	symbolstest.RunExtractorSuite(t, "symbols.New-stub", func(t *testing.T) symbols.Extractor {
		return symbols.New()
	})
}
