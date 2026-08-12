package canontest_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/canon/canontest"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// fakeStubRegistry mirrors the shape of an SP-01-style stub Registry: Register and Run report
// core.ErrNotImplemented, and For/Names report nil — exactly like canon.NewRegistry's own stub
// does today. It exists only to exercise RunRegistrySuite before SP-04 ships a real Registry.
type fakeStubRegistry struct{}

func (fakeStubRegistry) Register(c canon.Canonicalizer) error        { return core.ErrNotImplemented }
func (fakeStubRegistry) For(tool, path string) []canon.Canonicalizer { return nil }
func (fakeStubRegistry) Run(tool, path string, in []byte, o canon.Options) (canon.Result, error) {
	return canon.Result{}, core.ErrNotImplemented
}
func (fakeStubRegistry) Names() []string { return nil }

// TestRunRegistrySuite_StubIsSkipped proves the suite's shape block passes against a stub
// Registry and that its behaviour block is skipped with the exact Rule W-1 message. SP-04 reuses
// RunRegistrySuite unchanged, pointed at its real implementation, to flip that skip off.
func TestRunRegistrySuite_StubIsSkipped(t *testing.T) {
	canontest.RunRegistrySuite(t, "fake-stub", func(t *testing.T) canon.Registry {
		return fakeStubRegistry{}
	})
}

// TestRunRegistrySuite_AgainstQompackNewRegistryStub exercises RunRegistrySuite against the real
// canon.NewRegistry stub, end to end, so a change to its stub behaviour that breaks the
// conformance suite is caught here rather than only once SP-04 lands.
func TestRunRegistrySuite_AgainstQompackNewRegistryStub(t *testing.T) {
	canontest.RunRegistrySuite(t, "canon.NewRegistry-stub", func(t *testing.T) canon.Registry {
		return canon.NewRegistry()
	})
}

// TestRunRegistrySuite_AgainstQompackDefaultStub exercises RunRegistrySuite against the real
// canon.Default stub.
func TestRunRegistrySuite_AgainstQompackDefaultStub(t *testing.T) {
	canontest.RunRegistrySuite(t, "canon.Default-stub", func(t *testing.T) canon.Registry {
		return canon.Default(config.Defaults().Store.Canonicalize)
	})
}
