package mcptest

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/mcp"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the mcptest suites (§15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md): JSON-RPC 2.0 framing, the
// initialize/tools/list/tools/call/ping methods, schema-valid responses, and
// _meta.qompack.ephemeral. All are authored now, gated behind the same Rule W-1 stub probe as the
// rest of the suite, so SP-13 inherits them rather than writing its own grader.
//
// Framing note, normative for SP-13: the stdio transport is NEWLINE-DELIMITED JSON. One request
// per line, one response per line, no Content-Length headers. Every helper below both writes and
// parses that framing, so a change of transport would be a change to this file — which is the
// point of writing it down here rather than discovering it during integration.

// The JSON-RPC 2.0 constants the suite asserts against.
const (
	jsonrpcVersion = "2.0"

	methodInitialize = "initialize"
	methodToolsList  = "tools/list"
	methodToolsCall  = "tools/call"
	methodPing       = "ping"

	// codeParseError and codeMethodNotFound are JSON-RPC 2.0's own reserved codes.
	codeParseError     = -32700
	codeMethodNotFound = -32601
)

// The probe tools the transport cases register. Their names deliberately do not collide with any
// of the eight §8.7 tools, so a server that has both registered stays unambiguous.
const (
	echoToolName      = "conformance_echo"
	ephemeralToolName = "conformance_ephemeral"
	failingToolName   = "conformance_failing"
)

// echoedText is what echoTool's handler returns, so a tools/call assertion has something exact to
// compare against.
const echoedText = "conformance echo"

// toolNamesInDesignOrder is the eight retrieval tools of Qompack.md §8.7, in the order the design
// lists them. The order is part of the contract: `tools/list` output is a golden in SP-13, and a
// reordering would churn it for no reason.
var toolNamesInDesignOrder = []string{
	"recall", "expand", "re_read", "already_tried",
	"record_eliminated", "timeline", "why", "dropped",
}

// ephemeralToolNames are the seven tools whose results are born ephemeral (§8.7).
// record_eliminated is the exception: it writes negative knowledge, it does not retrieve content,
// so nothing about it belongs in the eviction-first tier.
var ephemeralToolNames = []string{
	"recall", "expand", "re_read", "already_tried", "timeline", "why", "dropped",
}

// echoTool is a minimal registrable Tool whose handler always succeeds.
func echoTool() mcp.Tool {
	return mcp.Tool{
		Name:        echoToolName,
		Title:       "Conformance echo",
		Description: "Returns a fixed string. Registered by the conformance suite only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Handler: func(ctx context.Context, r mcp.Request) (mcp.Response, error) {
			return mcp.Response{Content: []mcp.Content{{Type: "text", Text: echoedText}}}, nil
		},
	}
}

// ephemeralTool is echoTool's counterpart for the §8.7 ephemeral-tagging case.
func ephemeralTool() mcp.Tool {
	return mcp.Tool{
		Name:        ephemeralToolName,
		Title:       "Conformance ephemeral",
		Description: "Returns a fixed string, tagged ephemeral. Registered by the conformance suite only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Ephemeral:   true,
		Handler: func(ctx context.Context, r mcp.Request) (mcp.Response, error) {
			return mcp.Response{
				Content:   []mcp.Content{{Type: "text", Text: echoedText}},
				Ephemeral: true,
			}, nil
		},
	}
}

// failingTool returns a TOOL error: something the model should read and react to, which is a
// different thing from a JSON-RPC protocol error.
func failingTool() mcp.Tool {
	return mcp.Tool{
		Name:        failingToolName,
		Title:       "Conformance failing",
		Description: "Always returns a tool error. Registered by the conformance suite only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Handler: func(ctx context.Context, r mcp.Request) (mcp.Response, error) {
			return mcp.Response{
				IsError: true,
				Content: []mcp.Content{{Type: "text", Text: "the retrieval failed"}},
			}, nil
		},
	}
}

// request renders one JSON-RPC 2.0 request line.
func request(t *testing.T, id any, method string, params map[string]any) string {
	t.Helper()
	req := map[string]any{"jsonrpc": jsonrpcVersion, "method": method}
	if id != nil {
		req["id"] = id
	}
	if params != nil {
		req["params"] = params
	}
	b, err := json.Marshal(req)
	require.NoError(t, err)
	return string(b)
}

// converse feeds lines to s.Serve as newline-delimited JSON and returns the decoded response
// lines, in order. It asserts Serve itself returned no error: reaching EOF on the input is the
// normal way an MCP server's session ends, not a failure.
func converse(t *testing.T, s mcp.Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")

	require.NoError(t, s.Serve(context.Background(), in, &out),
		"Serve must return nil when its input reaches EOF")

	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), "every output line must be one JSON object: %q", line)
		responses = append(responses, m)
	}
	return responses
}

// initialized returns a server with the probe tools registered and the MCP handshake already
// exchanged, since a real server may legitimately refuse tool calls before initialize.
func initialized(t *testing.T, s mcp.Server) mcp.Server {
	t.Helper()
	require.NoError(t, s.Register(echoTool()))
	require.NoError(t, s.Register(ephemeralTool()))
	require.NoError(t, s.Register(failingTool()))
	return s
}

// initializeLine is the handshake request every multi-step conversation opens with.
func initializeLine(t *testing.T) string {
	t.Helper()
	return request(t, 1, methodInitialize, map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "qompack-conformance", "version": "0"},
	})
}

// result extracts the "result" object of a response, asserting it is a success rather than an
// error.
func result(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	require.NotContains(t, resp, "error", "expected a result, got a JSON-RPC error: %v", resp["error"])
	res, ok := resp["result"].(map[string]any)
	require.True(t, ok, "result must be an object, got %T", resp["result"])
	return res
}

// rpcError extracts the "error" object of a response, asserting it is an error rather than a
// success.
func rpcError(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	require.NotContains(t, resp, "result", "expected a JSON-RPC error, got a result")
	e, ok := resp["error"].(map[string]any)
	require.True(t, ok, "error must be an object, got %T", resp["error"])
	return e
}

// runServeEOFCase asserts the loop terminates cleanly on EOF rather than reporting a failure. An
// MCP session ends when the host closes the pipe, which is not an error condition.
func runServeEOFCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, factory(t).Serve(context.Background(), strings.NewReader(""), &out))
	require.Empty(t, out.String(), "an empty conversation produces no output")
}

// runInitializeCase asserts the MCP handshake is answered with the server's own identity and
// capabilities — the response the host uses to decide the server registered at all (the
// mcp.server_registered contract assertion of §12.1).
func runInitializeCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)), initializeLine(t))
	require.Len(t, responses, 1)

	res := result(t, responses[0])
	require.Contains(t, res, "protocolVersion", "initialize must report the protocol version it agreed to")
	require.Contains(t, res, "capabilities", "initialize must report the server's capabilities")

	info, ok := res["serverInfo"].(map[string]any)
	require.True(t, ok, "initialize must report serverInfo")
	require.NotEmpty(t, info["name"], "serverInfo.name must be the name NewServer was given")
	require.NotEmpty(t, info["version"], "serverInfo.version must be the version NewServer was given")
}

// runToolsListCase asserts every registered tool is advertised with the three fields a client
// needs to call it: a name, a description, and an input schema.
func runToolsListCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	s := initialized(t, factory(t))
	responses := converse(t, s, initializeLine(t), request(t, 2, methodToolsList, nil))
	require.Len(t, responses, 2)

	tools, ok := result(t, responses[1])["tools"].([]any)
	require.True(t, ok, "tools/list result must carry a tools array")

	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		require.True(t, ok, "each tools/list entry must be an object")
		name, _ := tool["name"].(string)
		require.NotEmpty(t, name)
		require.NotEmpty(t, tool["description"], "tool %s must advertise a description", name)
		require.Contains(t, tool, "inputSchema", "tool %s must advertise an input schema", name)
		names = append(names, name)
	}
	require.Subset(t, names, []string{echoToolName, ephemeralToolName, failingToolName},
		"tools/list must advertise every registered tool")

	// Tools() and tools/list must agree: they are two views of one registry.
	registered := make([]string, 0, len(s.Tools()))
	for _, tool := range s.Tools() {
		registered = append(registered, tool.Name)
	}
	require.ElementsMatch(t, registered, names, "Tools() and tools/list must report the same set")
}

// runToolsCallCase asserts a call reaches its handler and the handler's content reaches the wire.
func runToolsCallCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)),
		initializeLine(t),
		request(t, 2, methodToolsCall, map[string]any{"name": echoToolName, "arguments": map[string]any{}}))
	require.Len(t, responses, 2)

	res := result(t, responses[1])
	content, ok := res["content"].([]any)
	require.True(t, ok, "tools/call result must carry a content array")
	require.NotEmpty(t, content)

	first, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", first["type"], "content blocks carry their protocol type")
	require.Equal(t, echoedText, first["text"], "the handler's text must reach the wire unchanged")
}

// runPingCase asserts ping is answered. It is the liveness check a client uses when nothing else
// is in flight, so a server that ignores it looks hung.
func runPingCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)), initializeLine(t), request(t, 2, methodPing, nil))
	require.Len(t, responses, 2)
	require.NotContains(t, responses[1], "error", "ping must succeed")
	require.Contains(t, responses[1], "result", "ping must carry a result, even an empty one")
}

// runFramingCase is the JSON-RPC 2.0 framing contract itself: one response per request, on its
// own line, carrying the protocol version and echoing the request's id — including when the id is
// a string rather than a number, which the spec permits and clients do use.
func runFramingCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)),
		initializeLine(t),
		request(t, "string-id", methodPing, nil),
		request(t, 3, methodToolsList, nil))
	require.Len(t, responses, 3, "exactly one response per request, each on its own line")

	require.Equal(t, jsonrpcVersion, responses[0]["jsonrpc"])
	require.InDelta(t, 1.0, responses[0]["id"], 0, "a numeric id is echoed as a number")

	require.Equal(t, jsonrpcVersion, responses[1]["jsonrpc"])
	require.Equal(t, "string-id", responses[1]["id"], "a string id is echoed as a string")

	require.Equal(t, jsonrpcVersion, responses[2]["jsonrpc"])
	require.InDelta(t, 3.0, responses[2]["id"], 0)
}

// runUnknownMethodCase asserts an unknown METHOD is a JSON-RPC protocol error with the reserved
// code, not a tool error and not silence.
func runUnknownMethodCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)),
		initializeLine(t),
		request(t, 2, "no/such/method", nil))
	require.Len(t, responses, 2)

	e := rpcError(t, responses[1])
	require.InDelta(t, float64(codeMethodNotFound), e["code"], 0, "an unknown method is -32601")
	require.NotEmpty(t, e["message"])
}

// runMalformedCase asserts a garbage line is answered with a parse error AND does not kill the
// loop: the very next valid request must still be served. A server that exits on the first bad
// byte turns a transient framing glitch into a dead session.
func runMalformedCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)),
		initializeLine(t),
		"{this is not json",
		request(t, 3, methodPing, nil))
	require.Len(t, responses, 3, "a malformed line is answered, and the loop continues")

	e := rpcError(t, responses[1])
	require.InDelta(t, float64(codeParseError), e["code"], 0, "unparseable input is -32700")

	require.Contains(t, responses[2], "result", "the request after a malformed line must still be served")
	require.InDelta(t, 3.0, responses[2]["id"], 0)
}

// runNotificationCase asserts a JSON-RPC notification — a request with no id — is acted on
// silently. Answering one would put an unsolicited object on the wire, which a strict client
// treats as a protocol violation.
func runNotificationCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)),
		initializeLine(t),
		request(t, nil, "notifications/initialized", nil),
		request(t, 3, methodPing, nil))

	require.Len(t, responses, 2, "a notification gets no response line")
	require.InDelta(t, 1.0, responses[0]["id"], 0)
	require.InDelta(t, 3.0, responses[1]["id"], 0)
}

// runDuplicateRegisterCase asserts registering the same name twice is refused. The eight tools are
// a fixed set; a duplicate is a wiring bug, and silently replacing the first registration would
// hide it until a retrieval returned the wrong thing.
func runDuplicateRegisterCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	s := factory(t)
	require.NoError(t, s.Register(echoTool()))
	require.Error(t, s.Register(echoTool()), "a duplicate tool name must be refused, not silently replace the first")
	require.Len(t, s.Tools(), 1, "a refused registration must not have added anything")
}

// runEphemeralMetaCase is §8.7's eviction policy made observable: a retrieval result is born
// ephemeral, and the client is told so through _meta.qompack.ephemeral. Without the flag the
// result would rank like ordinary content and the retrieval layer would recreate the bloat it
// exists to solve.
func runEphemeralMetaCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)),
		initializeLine(t),
		request(t, 2, methodToolsCall, map[string]any{"name": ephemeralToolName, "arguments": map[string]any{}}),
		request(t, 3, methodToolsCall, map[string]any{"name": echoToolName, "arguments": map[string]any{}}))
	require.Len(t, responses, 3)

	meta, ok := result(t, responses[1])["_meta"].(map[string]any)
	require.True(t, ok, "an ephemeral result must carry _meta")
	qompack, ok := meta["qompack"].(map[string]any)
	require.True(t, ok, "_meta must carry the qompack namespace")
	require.Equal(t, true, qompack["ephemeral"], "_meta.qompack.ephemeral must be true for an ephemeral result")

	// A non-ephemeral tool must not claim to be one.
	if meta, ok := result(t, responses[2])["_meta"].(map[string]any); ok {
		if qompack, ok := meta["qompack"].(map[string]any); ok {
			require.NotEqual(t, true, qompack["ephemeral"],
				"a non-ephemeral tool must not be tagged ephemeral")
		}
	}
}

// runToolErrorCase pins the distinction §5.16 draws: a failed retrieval is a RESULT with isError
// set, which the model reads and reacts to, not a JSON-RPC error, which it never sees.
func runToolErrorCase(t *testing.T, factory func(t *testing.T) mcp.Server) {
	t.Helper()
	responses := converse(t, initialized(t, factory(t)),
		initializeLine(t),
		request(t, 2, methodToolsCall, map[string]any{"name": failingToolName, "arguments": map[string]any{}}))
	require.Len(t, responses, 2)

	res := result(t, responses[1])
	require.Equal(t, true, res["isError"], "a failed retrieval is a tool error, reported inside the result")
	content, ok := res["content"].([]any)
	require.True(t, ok, "a tool error still carries content the model can read")
	require.NotEmpty(t, content)
}

// runToolSetCase asserts RegisterAll registers exactly the eight retrieval tools of Qompack.md
// §8.7, in the order the design lists them.
func runToolSetCase(t *testing.T, factory func(t *testing.T) ToolSetFixture) {
	t.Helper()
	f := factory(t)
	require.NoError(t, f.RegisterAll(f.Server))

	names := make([]string, 0, len(f.Server.Tools()))
	for _, tool := range f.Server.Tools() {
		names = append(names, tool.Name)
	}
	require.Equal(t, toolNamesInDesignOrder, names,
		"RegisterAll must register exactly the eight §8.7 tools, in design order")
}

// runToolMetadataCase asserts every registered tool is callable by a client that has only read
// tools/list: it needs a description to decide, a schema to build arguments, and a handler to
// reach.
func runToolMetadataCase(t *testing.T, factory func(t *testing.T) ToolSetFixture) {
	t.Helper()
	f := factory(t)
	require.NoError(t, f.RegisterAll(f.Server))

	for _, tool := range f.Server.Tools() {
		require.NotEmpty(t, tool.Description, "tool %s must describe itself to the model", tool.Name)
		require.NotEmpty(t, tool.InputSchema, "tool %s must advertise an input schema", tool.Name)
		require.NotNil(t, tool.Handler, "tool %s must have a handler bound", tool.Name)

		var schema map[string]any
		require.NoError(t, json.Unmarshal(tool.InputSchema, &schema),
			"tool %s's input schema must be valid JSON", tool.Name)
		require.Equal(t, "object", schema["type"], "tool %s's arguments must be a JSON object", tool.Name)
	}
}

// runToolSetEphemeralCase asserts the seven retrieval tools are born ephemeral and
// record_eliminated is not: it writes negative knowledge rather than returning content, so
// nothing about it belongs in the first-eviction tier (§8.7).
func runToolSetEphemeralCase(t *testing.T, factory func(t *testing.T) ToolSetFixture) {
	t.Helper()
	f := factory(t)
	require.NoError(t, f.RegisterAll(f.Server))

	got := make([]string, 0, len(ephemeralToolNames))
	for _, tool := range f.Server.Tools() {
		if tool.Ephemeral {
			got = append(got, tool.Name)
		}
	}
	require.ElementsMatch(t, ephemeralToolNames, got,
		"exactly the seven retrieval tools are born ephemeral; record_eliminated is not")
}
