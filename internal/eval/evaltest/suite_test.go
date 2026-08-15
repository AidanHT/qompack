package evaltest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/eval/evaltest"
)

// TestRunHarnessSuite_StubIsSkipped proves the suite's shape block passes against eval.New's stub
// Harness and that its behaviour block is skipped with the exact Rule W-1 message. SP-02 reuses
// RunHarnessSuite unchanged, pointed at its real Harness, to flip that skip off.
func TestRunHarnessSuite_StubIsSkipped(t *testing.T) {
	evaltest.RunHarnessSuite(t, "eval.New", func(t *testing.T) eval.Harness {
		return eval.New(eval.Options{})
	})
}
