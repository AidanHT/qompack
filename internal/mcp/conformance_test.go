package mcp_test

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/mcp/mcptest"
)

// The SP-01 conformance suite, run against the real server (Rule W-1).
//
// Both entry points live in an EXTERNAL test package on purpose: mcptest imports mcp, so an
// internal `package mcp` test file importing mcptest would be an import cycle. Everything the
// suite needs is exported, so nothing is lost by driving the package from outside it — and being
// forced to do so is a useful check that the public surface is complete.
//
// The suite skips itself while Serve still returns core.ErrNotImplemented (Rule W-1's stub probe).
// These two tests therefore go from "skipped with the mandated reason" to "running every
// behaviour case" in the commit that lands the server, with no change to this file — which is
// exactly the handover Rule W-1 is for. devtool lint's stubskips sub-check greps for the skip
// string, so a suite that silently stopped running would be caught there rather than here.

// TestMCPConformance runs the transport-and-methods suite against mcp.NewServer.
func TestMCPConformance(t *testing.T) {
	mcptest.RunMCPSuite(t, "mcp.NewServer", func(t *testing.T) mcp.Server {
		t.Helper()
		return mcp.NewServer(mcp.ServerName, core.Version, logging.Nop())
	})
}

// TestToolSetConformance runs the tool-set suite against mcp.RegisterAll.
//
// The ToolDeps are ZERO, and that is the point rather than a shortcut: the suite asserts the eight
// names, their order, their schemas and their ephemeral flags, all of which are properties of the
// DEFINITIONS. Binding a real store would make the conformance of the tool set depend on a project
// on disk, and every handler already tolerates a nil collaborator by answering "unavailable".
func TestToolSetConformance(t *testing.T) {
	mcptest.RunToolSetSuite(t, "mcp.RegisterAll", func(t *testing.T) mcptest.ToolSetFixture {
		t.Helper()
		return mcptest.ToolSetFixture{
			Server:      mcp.NewServer(mcp.ServerName, core.Version, logging.Nop()),
			RegisterAll: func(s mcp.Server) error { return mcp.RegisterAll(s, mcp.ToolDeps{}) },
		}
	})
}
