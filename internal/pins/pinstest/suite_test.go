package pinstest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/pins/pinstest"
)

// TestRunPinsSuite_StubIsSkipped proves the suite's shape block passes against pins.Open's stub
// Store and that its behaviour block is skipped with the exact Rule W-1 message. SP-10 reuses
// RunPinsSuite unchanged, pointed at its real Store, to flip that skip off.
func TestRunPinsSuite_StubIsSkipped(t *testing.T) {
	pinstest.RunPinsSuite(t, "pins.Open", func(t *testing.T) pins.Store {
		s, err := pins.Open(t.TempDir())
		if err != nil {
			t.Fatalf("pins.Open: %v", err)
		}
		return s
	})
}
