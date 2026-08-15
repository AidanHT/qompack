// Package mcptest is the conformance suite for mcp.Server and mcp.RegisterAll
// (00-ARCHITECTURE.md §5.22): every implementation SP-13 ships must pass RunMCPSuite and
// RunToolSetSuite. SP-01 ships the suites themselves, including the behaviour assertions SP-13
// inherits (Rule W-1) — only each guarded /behaviour block is skipped until a real server lands.
//
// Everything here drives the server the way the host does: bytes in through an io.Reader, bytes
// out through an io.Writer, newline-delimited JSON-RPC 2.0. No socket is opened and none may be
// (D10), which is also why the suite needs no goroutine and no timeout: Serve is handed a
// finite reader and returns when it hits EOF.
package mcptest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// ToolSetFixture is everything RunToolSetSuite needs: a fresh Server, and RegisterAll already
// bound to the caller's ToolDeps. The deps are the caller's to build because mcp.ToolDeps holds a
// store.Store, a negknow.Ledger and a checkpoint.Reader, none of which this package may construct
// (00-ARCHITECTURE.md §3.2).
type ToolSetFixture struct {
	// Server is the server the tool set is registered on.
	Server mcp.Server
	// RegisterAll registers the eight §8.7 tools on s, bound to the caller's ToolDeps.
	RegisterAll func(s mcp.Server) error
}

// RunMCPSuite is the conformance suite for mcp.Server: the JSON-RPC 2.0 transport and the four
// methods §5.22 names. name distinguishes multiple factories run in the same test binary; factory
// must return a fresh, empty Server on every call.
func RunMCPSuite(t *testing.T, name string, factory func(t *testing.T) mcp.Server) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)

		requireKnownError(t, s.Register(echoTool()))

		// Tools has no error return; any slice — including nil — is shape-valid.
		_ = s.Tools()

		// An empty reader is EOF on the first read, so a real Serve returns immediately and a
		// stub Serve never touches it. Either way this cannot block.
		requireKnownError(t, s.Serve(context.Background(), strings.NewReader(""), io.Discard))

		requireKnownError(t, mcp.RegisterAll(factory(t), mcp.ToolDeps{}))
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("serve_returns_nil_at_eof", func(t *testing.T) { runServeEOFCase(t, factory) })
		t.Run("initialize_is_answered_with_server_info", func(t *testing.T) { runInitializeCase(t, factory) })
		t.Run("tools_list_advertises_every_registered_tool", func(t *testing.T) { runToolsListCase(t, factory) })
		t.Run("tools_call_returns_content", func(t *testing.T) { runToolsCallCase(t, factory) })
		t.Run("ping_is_answered", func(t *testing.T) { runPingCase(t, factory) })
		t.Run("every_response_is_jsonrpc_2_0_and_echoes_its_id", func(t *testing.T) { runFramingCase(t, factory) })
		t.Run("an_unknown_method_is_a_protocol_error", func(t *testing.T) { runUnknownMethodCase(t, factory) })
		t.Run("malformed_json_does_not_kill_the_loop", func(t *testing.T) { runMalformedCase(t, factory) })
		t.Run("a_notification_gets_no_response", func(t *testing.T) { runNotificationCase(t, factory) })
		t.Run("registering_a_duplicate_name_is_an_error", func(t *testing.T) { runDuplicateRegisterCase(t, factory) })
		t.Run("ephemeral_results_carry_meta_qompack_ephemeral", func(t *testing.T) { runEphemeralMetaCase(t, factory) })
		t.Run("a_tool_error_is_not_a_protocol_error", func(t *testing.T) { runToolErrorCase(t, factory) })
	})
}

// RunToolSetSuite is the conformance suite for mcp.RegisterAll: that it registers exactly the
// eight retrieval tools of Qompack.md §8.7, in the order the design lists them. name
// distinguishes multiple factories run in the same test binary; factory must return a fresh
// fixture on every call.
func RunToolSetSuite(t *testing.T, name string, factory func(t *testing.T) ToolSetFixture) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		f := factory(t)
		require.NotNil(t, f.Server)
		require.NotNil(t, f.RegisterAll)
		requireKnownError(t, f.RegisterAll(f.Server))
	})

	if skipIfStubToolSet(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("registers_exactly_the_eight_tools_in_design_order", func(t *testing.T) {
			runToolSetCase(t, factory)
		})
		t.Run("every_tool_advertises_a_schema_and_a_description", func(t *testing.T) {
			runToolMetadataCase(t, factory)
		})
		t.Run("retrieval_tools_are_born_ephemeral", func(t *testing.T) { runToolSetEphemeralCase(t, factory) })
	})
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// stub and every real implementation is allowed to return from an operation
// (00-ARCHITECTURE.md §5.22; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// isStub reports whether factory currently produces a stub Server, using Serve as the probe
// (plans/OWNERS.tsv: mcp's probe method is Serve). An empty reader makes the probe free for a
// real implementation: it reads EOF and returns.
func isStub(t *testing.T, factory func(t *testing.T) mcp.Server) bool {
	t.Helper()
	err := factory(t).Serve(context.Background(), strings.NewReader(""), io.Discard)
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Server, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) mcp.Server) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// isStubToolSet reports whether factory currently produces a stub RegisterAll.
func isStubToolSet(t *testing.T, factory func(t *testing.T) ToolSetFixture) bool {
	t.Helper()
	f := factory(t)
	return core.IsNotImplemented(f.RegisterAll(f.Server))
}

// skipIfStubToolSet calls t.Skip with the exact Rule W-1 message when factory still produces a
// stub RegisterAll, and reports whether it did.
func skipIfStubToolSet(t *testing.T, factory func(t *testing.T) ToolSetFixture) bool {
	t.Helper()
	if isStubToolSet(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
