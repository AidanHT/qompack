package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The `tools/list` WIRE tests: what a host actually reads off the pipe.
//
// tools_test.go asserts properties of the definitions; this file asserts properties of the bytes.
// The distinction earns its keep in exactly one place, and it is the place most likely to break: a
// Go-level change to Tool — a renamed field, a dropped json tag, a struct reordered — leaves every
// definition test green while changing what the host is told. The golden here is the only thing
// that catches it.

// toolsListRequestLine is the JSON-RPC line that asks for the tool set.
const toolsListRequestLine = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

// serveToolsWire drives s over a finite reader and returns one decoded response per output line.
//
// It is a one-line alias for jsonrpc_test.go's protoConverse, kept because the name says what this
// file is about. The decode types (wireResponse, wireError) live there, deliberately spelled out
// as a CLIENT reads them rather than reusing the server's own rpcResponse.
func serveToolsWire(t *testing.T, s Server, lines ...string) []wireResponse {
	t.Helper()
	return protoConverse(t, s, lines...)
}

// TestToolsListMatchesGolden freezes the exact `tools/list` result the host receives.
//
// This golden is the published interface. A model reads these descriptions to decide whether to
// call a tool and these schemas to decide how, so a change here is a change to an API that other
// software depends on — and it should have to be approved, not merely compile. Reviewing the diff
// under -update IS the approval step.
func TestToolsListMatchesGolden(t *testing.T) {
	t.Parallel()

	got := serveToolsWire(t, newToolServer(t), toolsListRequestLine)
	require.Len(t, got, 1, "one request, one reply")
	require.Nil(t, got[0].Error, "tools/list must not be a protocol error")

	// Re-indented before comparison so the golden is a file a human can read and review. The
	// SERVER emits compact bytes; what is frozen here is the structure, not the whitespace.
	var pretty strings.Builder
	enc := json.NewEncoder(&pretty)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	var v any
	require.NoError(t, json.Unmarshal(got[0].Result, &v), "decoding the tools/list result")
	require.NoError(t, enc.Encode(v), "re-encoding the tools/list result")

	requireGolden(t, "testdata/golden/mcp/tools-list.json", []byte(pretty.String()))
}

// TestUnknownToolNameIsToolErrorNotRPCError asserts a misspelt tool name is answered, not refused.
//
// It is the §5.16 distinction that matters most in practice. -32601 means "this method does not
// exist", which a CLIENT handles by giving up; `result.isError` means "the tool you named is not
// one of mine", which a MODEL handles by calling tools/list and trying again. Returning the
// protocol error would end the exchange over a typo.
func TestUnknownToolNameIsToolErrorNotRPCError(t *testing.T) {
	t.Parallel()

	line := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope","arguments":{}}}`
	got := serveToolsWire(t, newToolServer(t), line)
	require.Len(t, got, 1, "one request, one reply")
	require.Nil(t, got[0].Error, "an unknown tool NAME must not be a JSON-RPC error member")

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(got[0].Result, &result), "decoding the tools/call result")
	require.True(t, result.IsError, "an unknown tool name is reported as a tool error")
	require.NotEmpty(t, result.Content, "a tool error still carries text the model can read")
	require.Contains(t, result.Content[0].Text, "nope", "the message names the tool that was asked for")
}

// TestEveryToolRejectsUnknownArgument asserts the published schemas are CLOSED.
//
// additionalProperties:false is what turns a hallucinated argument into an immediate, specific
// error instead of a silently ignored one. A model that passes `k` to a tool with no `k` should be
// told so on the call it made — not left to infer it from a result that looks subtly wrong.
func TestEveryToolRejectsUnknownArgument(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	for _, name := range ToolNames() {
		t.Run(name, func(t *testing.T) {
			args := validArgsFor(name)
			args["zzz"] = 1
			msg := f.callErr(t, name, args)
			require.True(t, strings.HasPrefix(msg, "invalid arguments for "+name+":"),
				"%s must reject an unknown argument with the uniform prefix, got: %s", name, msg)
			require.Contains(t, msg, "/zzz", "the violation names the offending property by JSON pointer")
		})
	}
}

// validArgsFor returns arguments that satisfy each tool's REQUIRED properties.
//
// The unknown-argument test has to be the only thing wrong with the call it makes. Sending `{}`
// plus a stray key to a tool with a required property would produce two violations and prove
// nothing about which one the schema caught.
func validArgsFor(name string) map[string]any {
	switch name {
	case ToolRecall:
		return map[string]any{"query": "pool"}
	case ToolExpand:
		return map[string]any{"tool_use_id": "tu-read-1"}
	case ToolReRead:
		return map[string]any{"path": "src/auth.ts"}
	case ToolAlreadyTried:
		return map[string]any{"target": "src/auth.ts", "approach": "rewrite"}
	case ToolRecordEliminated:
		return map[string]any{"target": "src/auth.ts", "approach": "rewrite", "reason": "it does not compile"}
	case ToolWhy:
		return map[string]any{"decision_id": "dec_000000000000"}
	default:
		// `timeline` and `dropped` require nothing.
		return map[string]any{}
	}
}

// TestToolsListIsIdenticalAcrossCalls asserts the result does not depend on call order or map
// iteration, which is what makes freezing it as a golden legitimate in the first place.
func TestToolsListIsIdenticalAcrossCalls(t *testing.T) {
	t.Parallel()

	s := newToolServer(t)
	first := serveToolsWire(t, s, toolsListRequestLine)
	second := serveToolsWire(t, s, toolsListRequestLine)
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.Equal(t, string(first[0].Result), string(second[0].Result))
}

// toolsListNames is unused by the assertions above and exists for a failure message: when a golden
// diff is large, naming the tools that were advertised is faster to read than the diff.
func toolsListNames(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(raw, &result), "decoding tools/list")
	out := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		out = append(out, tool.Name)
	}
	return out
}

// compile-time assertion that the proxy handler shape the tests use still matches Handler.
var _ Handler = func(context.Context, Request) (Response, error) { return Response{}, nil }
