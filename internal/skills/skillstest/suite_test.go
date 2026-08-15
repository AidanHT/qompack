package skillstest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/skills/skillstest"
)

// TestRunIndexerSuite_StubIsSkipped proves the suite's shape block passes against skills.New's
// stub Indexer and that its behaviour block is skipped with the exact Rule W-1 message. SP-11
// reuses RunIndexerSuite unchanged, pointed at its real Indexer, to flip that skip off.
func TestRunIndexerSuite_StubIsSkipped(t *testing.T) {
	skillstest.RunIndexerSuite(t, "skills.New", func(t *testing.T) skills.Indexer {
		return skills.New()
	})
}
