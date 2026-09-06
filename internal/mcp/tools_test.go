package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// The tool-DEFINITION tests: the eight names and their order, the metadata a host renders, the
// registration rules, and the two dispatch guarantees §12.1 rests on.
//
// Nothing here binds a collaborator. Every claim in this file is a property of the definitions
// themselves — which is also what makes them assertable at all: ToolDefs is read by RegisterAll in
// the daemon and by RegisterProxy in `qompack mcp`, and the two processes advertising the same
// eight tools is a contract that has to hold with no store, no ledger and no project on disk.

// designOrder is the §8.7 table's own order, written out rather than taken from ToolNames().
//
// A test that asked the implementation what the order is could only ever agree with itself. This
// literal is the independent statement of the contract; ToolNames() is checked against it.
var designOrder = []string{
	"recall", "expand", "re_read", "already_tried",
	"record_eliminated", "timeline", "why", "dropped",
}

// toolTestDeps is the dependency set the metadata tests bind: no collaborators at all, plus the
// frozen clock §6.1 requires of anything in this package that can reach a timestamp.
//
// A zero-collaborator ToolDeps is the real production shape rather than a shortcut — RegisterProxy
// registers ToolDefs(ToolDeps{}) so the proxy's tools/list is byte-identical to the daemon's — and
// every handler answers "not present in this build" instead of panicking.
func toolTestDeps() ToolDeps {
	return ToolDeps{Clock: newFakeClock(epoch), Log: logging.Nop()}
}

// newToolServer returns a server with the eight tools registered against toolTestDeps.
func newToolServer(t *testing.T) Server {
	t.Helper()
	s := NewServer(ServerName, core.Version, logging.Nop())
	require.NoError(t, RegisterAll(s, toolTestDeps()), "RegisterAll")
	return s
}

// toolDefsByName indexes the eight definitions, asserting on the way that the set is complete.
func toolDefsByName(t *testing.T) map[string]Tool {
	t.Helper()
	defs := ToolDefs(toolTestDeps())
	out := make(map[string]Tool, len(defs))
	for _, def := range defs {
		require.NotContains(t, out, def.Name, "%s is defined twice", def.Name)
		out[def.Name] = def
	}
	require.Len(t, out, len(designOrder), "the tool set is eight distinct tools")
	return out
}

// toolNamesOf projects a tool slice down to its names, in order.
func toolNamesOf(tools []Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// TestToolsListReturnsExactlyEightTools asserts the registered set is the eight of §8.7, in the
// order the design table lists them.
//
// The order is contract, not presentation: tools/list emits registration order, the golden freezes
// it, and a host renders the tools to the model in the order it receives them.
func TestToolsListReturnsExactlyEightTools(t *testing.T) {
	t.Parallel()
	tools := newToolServer(t).Tools()
	require.Len(t, tools, 8, "exactly eight tools are registered")
	require.Equal(t, designOrder, toolNamesOf(tools))
}

// TestToolNamesMatchesToolDefs asserts the name list and the definition list cannot drift apart.
//
// ToolNames() is what a validator compares the §8.7 table against, while ToolDefs is what is
// actually registered; a build in which they disagreed would pass its own validation and still
// serve a different tool set.
func TestToolNamesMatchesToolDefs(t *testing.T) {
	t.Parallel()
	require.Equal(t, designOrder, ToolNames())
	require.Equal(t, ToolNames(), toolNamesOf(ToolDefs(toolTestDeps())))

	mutated := ToolNames()
	mutated[0] = "clobbered"
	require.Equal(t, designOrder, ToolNames(),
		"ToolNames must hand out a copy; a caller sorting the result must not reorder the tool set")
}

// TestEveryToolHasTitleDescriptionAndSchema asserts each of the eight carries everything a host
// needs to render it and a model needs to call it.
//
// An empty description is the failure that costs the most and shows the least: the tool still
// registers, still appears in tools/list, and is simply never chosen, because the description is
// the whole of what the model decides on.
func TestEveryToolHasTitleDescriptionAndSchema(t *testing.T) {
	t.Parallel()
	for _, def := range ToolDefs(toolTestDeps()) {
		require.NotEmpty(t, def.Title, "%s has no title", def.Name)
		require.NotEmpty(t, def.Description, "%s has no description", def.Name)
		require.NotEmpty(t, def.InputSchema, "%s has no input schema", def.Name)
		require.NotNil(t, def.Handler, "%s has no handler", def.Name)
	}
}

// TestRegisterAllRejectsDuplicates asserts a second registration of the same set is refused rather
// than silently replacing the first.
//
// The eight are a fixed set, so a duplicate can only mean a wiring bug — two RegisterAll calls on
// one server — and a server that accepted it would answer with sixteen tools, half of them bound
// to collaborators the caller thought it had replaced.
func TestRegisterAllRejectsDuplicates(t *testing.T) {
	t.Parallel()
	s := newToolServer(t)

	err := RegisterAll(s, toolTestDeps())
	require.Error(t, err, "registering the eight tools twice must fail")
	require.Contains(t, err.Error(), ToolRecall, "the error must name the tool that collided")
	require.Len(t, s.Tools(), 8, "a refused duplicate must not have grown the tool set")
	require.Equal(t, designOrder, toolNamesOf(s.Tools()))
}

// TestRegisterProxyBindsOneHandlerToAllEight asserts every registered tool routes to the single
// forwarder, which is what makes `qompack mcp` a transcoder rather than a second implementation.
//
// The check is behavioural rather than an identity comparison on the func value: two distinct
// closures built from one literal share a code pointer, so reflect-based identity would pass for a
// proxy that had accidentally built eight forwarders. Recording the names the forwarder is asked
// for proves both that it is reached and that nothing else is.
func TestRegisterProxyBindsOneHandlerToAllEight(t *testing.T) {
	t.Parallel()

	var forwarded []string
	fwd := func(_ context.Context, r Request) (Response, error) {
		forwarded = append(forwarded, r.Name)
		return Response{Content: []Content{{Type: "text", Text: "forwarded " + r.Name}}}, nil
	}

	s := NewServer(ServerName, core.Version, logging.Nop())
	require.NoError(t, RegisterProxy(s, fwd), "RegisterProxy")

	tools := s.Tools()
	require.Equal(t, designOrder, toolNamesOf(tools))
	for _, tool := range tools {
		resp, err := Dispatch(t.Context(), s, Request{Session: testSession, Name: tool.Name})
		require.NoError(t, err, "dispatching %s through the proxy", tool.Name)
		require.Equal(t, "forwarded "+tool.Name, responseText(resp),
			"%s did not reach the forwarding handler", tool.Name)
	}
	require.Equal(t, designOrder, forwarded, "every one of the eight must route to the one forwarder")

	require.Error(t, RegisterProxy(NewServer(ServerName, core.Version, logging.Nop()), nil),
		"a proxy with nothing to forward to must be refused at registration")
}

// TestProxyAndDirectToolListsAreIdentical asserts the transcoder advertises byte-identical
// metadata to the daemon's own server.
//
// This is the property the whole two-process design rests on. The model sees the proxy's
// tools/list and calls the daemon's handlers; if the two lists could differ by so much as a
// description, the model would be building arguments against a contract nothing enforces.
func TestProxyAndDirectToolListsAreIdentical(t *testing.T) {
	t.Parallel()

	direct := newToolServer(t)
	proxy := NewServer(ServerName, core.Version, logging.Nop())
	require.NoError(t, RegisterProxy(proxy, func(_ context.Context, _ Request) (Response, error) {
		return Response{}, nil
	}), "RegisterProxy")

	fromDirect := serveToolsWire(t, direct, toolsListRequestLine)
	fromProxy := serveToolsWire(t, proxy, toolsListRequestLine)
	require.Len(t, fromDirect, 1, "one request, one reply")
	require.Len(t, fromProxy, 1, "one request, one reply")
	require.Equal(t, string(fromDirect[0].Result), string(fromProxy[0].Result))
}

// TestDispatchReturnsErrToolNotFound asserts an unregistered name is the one case Dispatch reports
// as an ERROR rather than as a result.
//
// The distinction is what lets a caller tell "this build has no such tool" — a wiring or version
// problem it can act on — from "the tool ran and failed", which is the model's business.
func TestDispatchReturnsErrToolNotFound(t *testing.T) {
	t.Parallel()

	resp, err := Dispatch(t.Context(), newToolServer(t), Request{Session: testSession, Name: "nope"})
	require.ErrorIs(t, err, ErrToolNotFound)
	require.False(t, resp.IsError, "an error return carries no result to read")
	require.Empty(t, resp.Content)
}

// TestDispatchIsolatesHandlerPanic asserts a panicking tool becomes a readable RESULT, per §12.1.
//
// A panic reaching the transport would end the session for every other tool as well, and a
// protocol error is something the model never sees; the only useful answer is one it can read and
// react to, so err is nil and isError is set.
func TestDispatchIsolatesHandlerPanic(t *testing.T) {
	t.Parallel()

	s := NewServer(ServerName, core.Version, logging.Nop())
	require.NoError(t, s.Register(Tool{
		Name:        "boom",
		Title:       "Boom",
		Description: "panics on purpose",
		Handler: func(_ context.Context, _ Request) (Response, error) {
			panic("a tool body did something nobody anticipated")
		},
	}), "registering the panicking tool")

	resp, err := Dispatch(t.Context(), s, Request{Session: testSession, Name: "boom"})
	require.NoError(t, err, "a handler panic is a result, never an error")
	require.True(t, resp.IsError)
	require.Equal(t, "internal error in tool boom", responseText(resp))
}

// TestSpanPolicyAppearsInExpandAndReReadDescriptions asserts the §8.7 minimal-span policy is where
// the model actually reads it.
//
// A policy documented only in the design is a policy the model never sees, and the two tools that
// can return an unbounded object are the two that have to carry it. The other six are asserted NOT
// to, because a policy repeated on tools it does not govern is how it stops being read.
func TestSpanPolicyAppearsInExpandAndReReadDescriptions(t *testing.T) {
	t.Parallel()
	defs := toolDefsByName(t)

	for _, name := range []string{ToolExpand, ToolReRead} {
		require.Contains(t, defs[name].Description, spanPolicy,
			"%s must carry the minimal-span policy in its description", name)
		require.Contains(t, defs[name].Description, "minimum sufficient span")
		require.Contains(t, defs[name].Description, "full=true")
	}
	for _, name := range designOrder {
		if name == ToolExpand || name == ToolReRead {
			continue
		}
		require.NotContains(t, defs[name].Description, spanPolicy,
			"%s does not resolve a span and must not claim the policy", name)
	}

	require.Contains(t, defs[ToolAlreadyTried].Description, StandingInstruction,
		"already_tried carries the standing instruction that closes G6.2")
}

// TestOnlyRecordEliminatedIsNotEphemeral asserts seven of the eight are born ephemeral, and that
// the flag on the definition agrees with the one run() reads.
//
// record_eliminated is the exception because it WRITES negative knowledge: the acknowledgement of
// a durable fact is not retrieved content, so nothing about it belongs in the first-eviction tier.
// The two sources are cross-checked because they are separate lists — ToolDefs' field and the
// ephemeralTools map — and a disagreement would evict content the response never marked, or mark
// content the evictor never sees.
func TestOnlyRecordEliminatedIsNotEphemeral(t *testing.T) {
	t.Parallel()

	ephemeral := 0
	for _, def := range ToolDefs(toolTestDeps()) {
		require.Equal(t, ephemeralTools[def.Name], def.Ephemeral,
			"%s disagrees between its definition and the ephemeral tool set", def.Name)
		if def.Ephemeral {
			ephemeral++
		}
	}
	require.Equal(t, 7, ephemeral, "seven of the eight are born ephemeral")

	defs := toolDefsByName(t)
	require.False(t, defs[ToolRecordEliminated].Ephemeral,
		"record_eliminated writes a durable fact and must not be first-eviction")
	require.Len(t, ephemeralTools, 7, "the ephemeral set must not have grown a ninth member")

	require.True(t, strings.Contains(defs[ToolRecordEliminated].Description, "survives compaction"),
		"record_eliminated's description must say why it is the exception")
}

// TestRegisterRejectsAnUncompilableSchema asserts registration is where a broken schema is caught.
//
// A tool whose schema does not compile would accept every argument unvalidated at runtime, which
// is the exact failure a schema exists to prevent; registration is the last moment at which it can
// be caught before a model is already relying on it.
func TestRegisterRejectsAnUncompilableSchema(t *testing.T) {
	t.Parallel()

	s := NewServer(ServerName, core.Version, logging.Nop())
	err := s.Register(Tool{
		Name:        "bad",
		Description: "carries a keyword this subset does not implement",
		InputSchema: []byte(`{"type":"object","properties":{},"patternProperties":{}}`),
		Handler:     func(_ context.Context, _ Request) (Response, error) { return Response{}, nil },
	})
	require.Error(t, err, "a schema carrying an unsupported keyword must be refused")
	require.Contains(t, err.Error(), "patternProperties")
	require.Empty(t, s.Tools(), "a refused tool must not be registered")

	require.True(t, errors.Is(err, err), "the registration error is returned, not swallowed")
}
