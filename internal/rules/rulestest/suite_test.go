package rulestest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/rules/rulestest"
)

// TestRunScannerSuite_StubIsSkipped proves the suite's shape block passes against rules.New's
// stub Scanner and that its behaviour block is skipped with the exact Rule W-1 message. SP-11
// reuses RunScannerSuite unchanged, pointed at its real Scanner, to flip that skip off.
func TestRunScannerSuite_StubIsSkipped(t *testing.T) {
	rulestest.RunScannerSuite(t, "rules.New", func(t *testing.T) rules.Scanner {
		return rules.New()
	})
}
