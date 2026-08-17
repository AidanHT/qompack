package evaltest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/eval/evaltest"
)

// TestRunHarnessSuite_AgainstEvalNew runs the conformance suite against eval.New.
//
// Until SP-02 the behaviour block was skipped with the exact Rule W-1 message and only the shape
// block ran. eval.New now returns a real Harness, so the stub probe reports false and BOTH blocks
// execute — which is what "the owning subplan flips those skips off" means when the skip is
// probe-driven rather than written out by hand (00-ARCHITECTURE.md §5.22, W-1).
func TestRunHarnessSuite_AgainstEvalNew(t *testing.T) {
	evaltest.RunHarnessSuite(t, "eval.New", func(t *testing.T) eval.Harness {
		return eval.New(eval.Options{})
	})
}
