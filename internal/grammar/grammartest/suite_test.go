package grammartest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/grammar/grammartest"
)

// fakeStubSequitur mirrors the shape of an SP-01-style stub Sequitur: Append and Reset are
// no-ops, Rules/Thrash/Compressed report nil, and Marshal/UnmarshalBinary report
// core.ErrNotImplemented — exactly like grammar.New's own stub does today. It exists only to
// exercise RunSequiturSuite before SP-15 ships a real Sequitur.
type fakeStubSequitur struct{}

func (fakeStubSequitur) Append(s grammar.Symbol)           {}
func (fakeStubSequitur) Rules() []grammar.Rule             { return nil }
func (fakeStubSequitur) Thrash(minUses int) []grammar.Rule { return nil }
func (fakeStubSequitur) Compressed() []grammar.Symbol      { return nil }
func (fakeStubSequitur) Reset()                            {}

func (fakeStubSequitur) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }
func (fakeStubSequitur) UnmarshalBinary(b []byte) error { return core.ErrNotImplemented }

// TestRunSequiturSuite_StubIsSkipped proves the suite's shape block passes against a stub
// Sequitur and that its behaviour block is skipped with the exact Rule W-1 message. SP-15 reuses
// RunSequiturSuite unchanged, pointed at its real implementation, to flip that skip off.
func TestRunSequiturSuite_StubIsSkipped(t *testing.T) {
	grammartest.RunSequiturSuite(t, "fake-stub", func(t *testing.T) grammar.Sequitur {
		return fakeStubSequitur{}
	})
}

// TestRunSequiturSuite_AgainstQompackStub exercises RunSequiturSuite against the real
// grammar.New stub, end to end, so a change to grammar.New's stub behaviour that breaks the
// conformance suite is caught here rather than only once SP-15 lands.
func TestRunSequiturSuite_AgainstQompackStub(t *testing.T) {
	grammartest.RunSequiturSuite(t, "grammar.New-stub", func(t *testing.T) grammar.Sequitur {
		return grammar.New()
	})
}
