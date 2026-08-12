package mcptest_test

import (
	"context"
	"io"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/mcp/mcptest"
)

// fakeStubServer mirrors the shape of an SP-01-style stub Server: every operation reports
// core.ErrNotImplemented and Tools — which has no error return — reports the documented zero
// value, exactly like mcp.NewServer's own stub does today. It exists so the suites are exercised
// against a second, independent stub as well as against the real one.
type fakeStubServer struct{}

func (fakeStubServer) Register(t mcp.Tool) error { return core.ErrNotImplemented }

func (fakeStubServer) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	return core.ErrNotImplemented
}

func (fakeStubServer) Tools() []mcp.Tool { return nil }

// toolSetFixture binds mcp.RegisterAll to a zero ToolDeps. The deps are empty because every seam
// they would hold — store.Store, negknow.Ledger, checkpoint.Reader — is itself still a stub;
// SP-13 binds real ones.
func toolSetFixture(t *testing.T) mcptest.ToolSetFixture {
	t.Helper()
	return mcptest.ToolSetFixture{
		Server:      mcp.NewServer("qompack", core.Version, logging.Nop()),
		RegisterAll: func(s mcp.Server) error { return mcp.RegisterAll(s, mcp.ToolDeps{}) },
	}
}

// TestMCPSuite_ShapePassesAgainstStub proves both mcptest suites' shape blocks pass against the
// SP-01 stubs — the real mcp.NewServer/RegisterAll stubs and an independent fake — and that every
// behaviour block is skipped with the exact Rule W-1 message. SP-13 reuses these suites
// unchanged, pointed at its real implementation, to flip those skips off.
//
// Each suite runs inside its own t.Run wrapper. That is load-bearing, not cosmetic: Rule W-1's
// t.Skip fires on the *T the suite was handed, so calling several suites directly from one test
// function would let the first stub skip abort the rest of them before they ever ran.
func TestMCPSuite_ShapePassesAgainstStub(t *testing.T) {
	t.Run("server-fake-stub", func(t *testing.T) {
		mcptest.RunMCPSuite(t, "fake-stub", func(t *testing.T) mcp.Server {
			return fakeStubServer{}
		})
	})

	t.Run("server-newserver-stub", func(t *testing.T) {
		mcptest.RunMCPSuite(t, "mcp.NewServer-stub", func(t *testing.T) mcp.Server {
			return mcp.NewServer("qompack", core.Version, logging.Nop())
		})
	})

	t.Run("tool-set-stub", func(t *testing.T) {
		mcptest.RunToolSetSuite(t, "mcp.RegisterAll-stub", toolSetFixture)
	})
}
