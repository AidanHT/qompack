package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The dispatch and lifecycle half of the protocol tests: what each of the five methods answers,
// what a notification does NOT answer, what a panicking handler turns into, and how Serve ends.
//
// The framing helpers these tests drive the server with live in jsonrpc_test.go. Everything here
// is a bare server with zero or one probe tool: the eight §8.7 tools are exercised through the
// fixture in the handler tests, and binding a store here would make a dispatch failure depend on
// a project on disk.

// The protocol versions the handshake tests pin.
//
// They are literals rather than reads of supportedProtocolVersions, and that is the point: the
// version this build negotiates is a CONTRACT with the host, so a test that read the same slice
// the implementation reads would agree with any change to it, including a wrong one.
const (
	// protoPreferredVersion is the newest version this build speaks, and the one an unrecognised
	// request is answered in.
	protoPreferredVersion = "2025-06-18"
	// protoLegacyVersion is an older supported version, echoed back rather than upgraded.
	protoLegacyVersion = "2025-03-26"
	// protoUnknownVersion is a version no build speaks, which is how the fallback is reached.
	protoUnknownVersion = "1999-01-01"
)

// The client identity the handshake tests send. It is the real host's own spelling, so a failure
// message reads like the session it models rather than like a fixture.
const (
	protoClientName    = "claude-code"
	protoClientVersion = "2.1"
)

// wireInitializeResult is the handshake result as a HOST reads it — the object §12.1's
// mcp.server_registered observable is decided from.
//
// Spelled out here rather than reusing initializeResult so that a rename on the implementation
// side fails this decode instead of quietly renaming both halves of the contract at once.
type wireInitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    struct {
		Tools struct {
			ListChanged bool `json:"listChanged"`
		} `json:"tools"`
	} `json:"capabilities"`
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Instructions string `json:"instructions"`
}

// protoInitialize renders the handshake request line, with the client identity a real host sends.
func protoInitialize(t *testing.T, id any, version string) string {
	t.Helper()
	return protoRequest(t, id, methodInitialize, map[string]any{
		"protocolVersion": version,
		"clientInfo":      map[string]any{"name": protoClientName, "version": protoClientVersion},
	})
}

// protoHandshake runs one handshake against a zero-tool server and returns the decoded result.
func protoHandshake(t *testing.T, version string) wireInitializeResult {
	t.Helper()
	resps := protoConverse(t, protoServer(t, ServerOptions{}), protoInitialize(t, 1, version))
	require.Len(t, resps, 1)

	var res wireInitializeResult
	protoResult(t, resps[0], &res)
	return res
}

// TestInitializeEchoesSupportedProtocolVersion asserts a client asking for a version this build
// speaks is answered in that version rather than being silently upgraded. A host that asked for
// 2025-03-26 and was answered in 2025-06-18 would go on to send fields the older version does not
// define, and would be right to.
func TestInitializeEchoesSupportedProtocolVersion(t *testing.T) {
	res := protoHandshake(t, protoLegacyVersion)

	require.Equal(t, protoLegacyVersion, res.ProtocolVersion,
		"a supported version is echoed, not upgraded")
	require.Equal(t, ServerName, res.ServerInfo.Name,
		"serverInfo.name is the one spelling plugin/.mcp.json and the mcp__qompack__ tool names share")
	require.Equal(t, protoVersionUnderTest, res.ServerInfo.Version)
}

// TestInitializeFallsBackToPreferredVersion asserts a version this build does not speak is
// answered in the newest one it does, which is what the MCP specification prescribes: the client
// then decides whether it can live with the answer, rather than the handshake failing outright.
func TestInitializeFallsBackToPreferredVersion(t *testing.T) {
	res := protoHandshake(t, protoUnknownVersion)

	require.Equal(t, protoPreferredVersion, res.ProtocolVersion,
		"an unsupported request is answered in the newest version this build speaks")
	require.False(t, res.Capabilities.Tools.ListChanged,
		"the eight tools are a closed set, so no client ever needs to re-poll")
}

// TestInitializeResultMatchesGolden freezes the whole handshake object, field order included.
//
// The result is INDENTED rather than re-marshalled: json.Indent preserves the wire's own field
// order, and the field order is part of what the golden exists to hold still. Round-tripping
// through map[string]any would sort the keys and freeze a shape no host ever sees.
func TestInitializeResultMatchesGolden(t *testing.T) {
	resps := protoConverse(t, protoServer(t, ServerOptions{Version: protoVersionUnderTest}),
		protoInitialize(t, 1, protoPreferredVersion))
	require.Len(t, resps, 1)
	require.Nil(t, resps[0].Error)

	var pretty bytes.Buffer
	require.NoError(t, json.Indent(&pretty, resps[0].Result, "", "  "), "indenting %s", resps[0].Result)
	pretty.WriteByte('\n')

	// Preserve initialize.json as the historical contract. v2 retires unsupported completeness
	// and native-eviction promises without changing protocol negotiation or response shape.
	requireGolden(t, "testdata/golden/mcp/initialize.v2.json", pretty.Bytes())
}

// TestInitializeInstructionsCarryStandingInstruction is G6.2 made checkable. §8.7 requires
// already_tried to be surfaced as a STANDING INSTRUCTION rather than merely as an available tool,
// and the handshake instructions are the one place a host shows the model something once, before
// any tool has been called. An affordance nobody is told to reach for goes unused.
func TestInitializeInstructionsCarryStandingInstruction(t *testing.T) {
	res := protoHandshake(t, protoPreferredVersion)

	require.NotEmpty(t, res.Instructions, "the handshake must tell the model what this server is for")
	require.True(t, strings.Contains(res.Instructions, StandingInstruction),
		"the §8.7 standing instruction must reach the model verbatim; got: %s", res.Instructions)
}

// TestNotificationsInitializedProducesNoResponse asserts the lifecycle notification is acted on in
// silence, and that the loop is still serving afterwards. Writing a response to a notification
// puts an unsolicited object on the wire, which a strict client treats as a protocol violation and
// a line-oriented one treats as the answer to its next request.
func TestNotificationsInitializedProducesNoResponse(t *testing.T) {
	require.Empty(t,
		protoServe(t, protoServer(t, ServerOptions{}), protoRequest(t, nil, methodInitialized, nil)),
		"a notification produces no response line at all")

	resps := protoConverse(t, protoServer(t, ServerOptions{}),
		protoRequest(t, nil, methodInitialized, nil), protoPing(t, 1))
	require.Len(t, resps, 1, "only the ping is answered")
	require.Equal(t, "1", string(resps[0].ID), "and the ping's own id comes back, not the notification's absence")
	require.JSONEq(t, "{}", string(resps[0].Result))
}

// TestNotificationWithUnknownMethodIgnored asserts an unrecognised notification is dropped rather
// than refused. There is no id to put a refusal on, so the only alternatives are silence and a
// protocol violation.
func TestNotificationWithUnknownMethodIgnored(t *testing.T) {
	require.Empty(t, protoServe(t, protoServer(t, ServerOptions{}),
		protoRequest(t, nil, "notifications/cancelled", map[string]any{})),
		"a notification this build does not recognise is not an error to anyone")
}

// TestPingReturnsEmptyObject pins the whole response line, not just its result. Ping is the
// liveness check a client sends when nothing else is in flight, so the exact bytes are the
// cheapest possible regression detector for the response envelope: version, echoed id, empty
// result, no error member, one trailing newline.
func TestPingReturnsEmptyObject(t *testing.T) {
	raw := protoServe(t, protoServer(t, ServerOptions{}), `{"jsonrpc":"2.0","id":"p","method":"ping"}`)

	require.Equal(t, `{"jsonrpc":"2.0","id":"p","result":{}}`+"\n", raw,
		"ping's response envelope is fixed down to the byte")
}

// TestUnknownMethodReturnsMethodNotFound asserts an unknown METHOD is -32601 carrying the method
// that was asked for. The name in data is what lets a host log something actionable instead of
// "the server said no"; tools/subscribe is the realistic case, since a client that assumes
// subscriptions exist will try exactly that.
func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	const unknownMethod = "tools/subscribe"

	resps := protoConverse(t, protoServer(t, ServerOptions{}), protoRequest(t, 1, unknownMethod, nil))
	require.Len(t, resps, 1)

	rpcErr := protoRPCError(t, resps[0], codeMethodNotFound)
	require.Equal(t, unknownMethod, rpcErr.Data["method"],
		"the refusal names what was asked for, so a host logs something actionable")
	require.Equal(t, "1", string(resps[0].ID))
}

// panickyToolName is the probe tool that panics. The name is load-bearing: the recovered text the
// model reads is built from it, and TestHandlerPanicIsolated asserts that exact sentence.
const panickyToolName = "panicky"

// panickyTool returns a tool whose handler panics outright, standing in for the class of failure
// nobody anticipated — a nil map write, an index off the end of a decoded slice.
func panickyTool() Tool {
	return Tool{
		Name:        panickyToolName,
		Title:       "Panicky",
		Description: "Panics. Registered by the protocol tests only.",
		InputSchema: json.RawMessage(probeSchema),
		Handler: func(context.Context, Request) (Response, error) {
			panic("boom")
		},
	}
}

// TestHandlerPanicIsolated is §12.1's isolation rule: an MCP tool panic is recovered at the
// handler boundary, returned as a tool error, and never kills the server.
//
// Both halves matter. The panic becomes a RESULT the model can read and react to, because a
// protocol error it never sees would leave it waiting on a tool that silently stopped existing;
// and the session keeps serving, because one bad retrieval must not cost the model its remaining
// turn.
func TestHandlerPanicIsolated(t *testing.T) {
	s := protoServer(t, ServerOptions{})
	require.NoError(t, s.Register(panickyTool()))

	resps := protoConverse(t, s,
		protoRequest(t, 1, methodToolsCall,
			map[string]any{"name": panickyToolName, "arguments": map[string]any{}}),
		protoPing(t, 2))
	require.Len(t, resps, 2, "the panicking call is answered and the session survives it")

	var res wireToolResult
	protoResult(t, resps[0], &res)
	require.True(t, res.IsError, "a recovered panic is a tool error the model reads, not a protocol error")
	require.Equal(t, "internal error in tool "+panickyToolName, res.text(),
		"the recovered text names the tool, so the model knows which affordance to stop using")

	require.Nil(t, resps[1].Error, "the server keeps serving after a panicking handler")
	require.Equal(t, "2", string(resps[1].ID))
}

// TestServeReturnsNilOnEOF asserts the loop ends cleanly when the input is exhausted. An MCP
// session ends when the host closes the pipe; reporting that as an error would make every normal
// shutdown look like a crash in the logs a user is told to read.
func TestServeReturnsNilOnEOF(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t,
		protoServer(t, ServerOptions{}).Serve(context.Background(), strings.NewReader(""), &out),
		"EOF is how a session ends, not how it fails")
	require.Empty(t, out.String(), "an empty conversation produces no output")
}

// TestServeReturnsOnContextCancel asserts a cancelled context ends the loop promptly and cleanly,
// even though the transport itself is a pipe with nothing on it.
//
// That is the whole reason the blocking read lives on its own goroutine: without it, a cancelled
// context would only take effect after the next line the host happened to send, which for an idle
// session is never.
func TestServeReturnsOnContextCancel(t *testing.T) {
	pr, pw := io.Pipe()
	// Serve leaves its read goroutine parked inside Read — inherent to io.Reader, and documented
	// as such. Closing the writer here releases it once the test is done.
	t.Cleanup(func() { _ = pw.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	require.NoError(t, protoServer(t, ServerOptions{}).Serve(ctx, pr, &out),
		"a cancelled context ends the loop cleanly; the caller asked for this")
	require.Empty(t, out.String(), "nothing was asked, so nothing is answered")
}

// TestServeRejectsNilStreams asserts Serve reports a missing reader or writer instead of starting
// its read goroutine on one. A nil io.Reader panics inside that goroutine, where no caller's
// recover can reach it, and takes the whole process with it — which for `qompack mcp` is the
// host's MCP session and for the guard walk in test/guards is the test binary. A seam fails by
// reporting, never by panicking (§12.3).
func TestServeRejectsNilStreams(t *testing.T) {
	srv := protoServer(t, ServerOptions{})
	var out bytes.Buffer

	require.Error(t, srv.Serve(context.Background(), nil, &out), "a nil reader must be reported")
	require.Error(t, srv.Serve(context.Background(), strings.NewReader(""), nil), "a nil writer must be reported")
	require.Error(t, srv.Serve(context.Background(), nil, nil))
	require.Empty(t, out.String(), "a refused Serve writes nothing")
}
