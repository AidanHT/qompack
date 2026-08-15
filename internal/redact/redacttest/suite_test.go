package redacttest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/redact/redacttest"
)

// fakeStubRedactor mirrors the shape of an SP-01-style stub Redactor: Redact echoes its input
// with no matches, and Rules reports none, exactly like redact.New's own stub does today. It
// exists only to exercise RunRedactorSuite before SP-06 ships a real Redactor.
type fakeStubRedactor struct{}

func (fakeStubRedactor) Redact(in []byte) (out []byte, matches []redact.Match) { return in, nil }

func (fakeStubRedactor) Rules() []string { return nil }

// TestRunRedactorSuite_StubIsSkipped proves the suite's shape block passes against a stub
// Redactor and that its behaviour block is skipped with the exact Rule W-1 message. SP-06 reuses
// RunRedactorSuite unchanged, pointed at its real implementation, to flip that skip off.
func TestRunRedactorSuite_StubIsSkipped(t *testing.T) {
	redacttest.RunRedactorSuite(t, "fake-stub", func(t *testing.T) redact.Redactor {
		return fakeStubRedactor{}
	})
}

// TestRunRedactorSuite_AgainstQompackStub exercises RunRedactorSuite against the real redact.New
// stub, end to end, so a change to redact.New's stub behaviour that breaks the conformance suite
// is caught here rather than only once SP-06 lands.
func TestRunRedactorSuite_AgainstQompackStub(t *testing.T) {
	redacttest.RunRedactorSuite(t, "redact.New-stub", func(t *testing.T) redact.Redactor {
		return redact.New(config.Defaults())
	})
}

// TestRunRedactorSuite_AgainstNop exercises RunRedactorSuite against redact.Nop: Nop is real and
// permanently a no-op (00-ARCHITECTURE.md §14.1), so it is expected to look exactly like a stub
// to isStub — Nop is documented as "tests only", never as the subject of this conformance suite,
// but this confirms the suite treats it that way instead of erroring on it.
func TestRunRedactorSuite_AgainstNop(t *testing.T) {
	redacttest.RunRedactorSuite(t, "redact.Nop", func(t *testing.T) redact.Redactor {
		return redact.Nop()
	})
}
