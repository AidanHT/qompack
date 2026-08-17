package symbols_test

import (
	"testing"

	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/symbols/symbolstest"
)

// TestSymbolsConformance runs SP-01's Extractor conformance suite against SP-04's real extractor
// and requires it to pass with ZERO skips.
//
// symbolstest.isStub probes by asking whether Extract finds the one top-level function in its
// probe source, because §5.22b gives Extract no error return and so leaves it nothing else to
// probe with. That makes this test the thing that flips the suite's behaviour block on: the
// smallest-enclosing-span and CRLF-stability assertions SP-01 wrote run here for the first time.
func TestSymbolsConformance(t *testing.T) {
	symbolstest.RunExtractorSuite(t, "symbols.New", func(t *testing.T) symbols.Extractor {
		return symbols.New()
	})
}
