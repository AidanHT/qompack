package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// The framing and codec half of the protocol tests: what readLine accepts, what a refused frame
// does to the stream that follows it, and what may appear on the out writer at all.
//
// These tests drive the server over STRINGS rather than over the fixture's Dispatch seam, because
// framing is the one property a Dispatch-level test cannot observe: a server that answered every
// call correctly and wrote two responses onto one line would pass every handler test in this
// package and fail against a real host on the first turn.
//
// The helpers below are shared with server_test.go and live here because the codec is what they
// speak. Nothing here needs a store, a ledger or a clock — a protocol failure must be diagnosable
// without a project on disk, and a fixture would only add ways for these tests to fail for
// reasons that are not about the protocol.

// protoVersionUnderTest is the server version the handshake tests report. It is a literal rather
// than core.Version so that testdata/golden/mcp/initialize.json does not churn on every release.
const protoVersionUnderTest = "0.1.0"

// probeSchema is the argument schema every probe tool in these files declares: an object that
// takes nothing. Register compiles it, so it has to be a real schema rather than an empty one.
const probeSchema = `{"type":"object","properties":{},"additionalProperties":false}`

// wireResponse is one decoded response line, as a CLIENT reads it.
//
// It is spelled out here rather than reusing the server's own rpcResponse so that a rename on the
// implementation side surfaces as a failing decode instead of silently renaming both halves of
// the contract at once. ID and Result stay raw: "which id was echoed, in which JSON type" is the
// assertion, and decoding an id into any would turn 7 into 7.0 before the test could look at it.
type wireResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *wireError      `json:"error"`
}

// wireError is the error half of a response line, decoded the way a client would.
type wireError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

// wireToolResult is a tools/call result, decoded the way a client would.
type wireToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool           `json:"isError"`
	Meta    map[string]any `json:"_meta"`
}

// text concatenates the result's text blocks, which is what the model actually reads.
func (r wireToolResult) text() string {
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// protoServer builds a bare server for a protocol test, filling the three ServerOptions fields
// every one of them would otherwise repeat. Zero tools is the default on purpose: initialize,
// ping, notifications and every error path are properties of the transport, and a registered tool
// would only give them something extra to be wrong about.
func protoServer(t *testing.T, o ServerOptions) Server {
	t.Helper()
	if o.Name == "" {
		o.Name = ServerName
	}
	if o.Version == "" {
		o.Version = protoVersionUnderTest
	}
	if o.Log == nil {
		o.Log = logging.Nop()
	}
	return NewServerWithOptions(o)
}

// protoStream joins request lines into the newline-delimited framing the stdio transport speaks.
//
// An empty conversation stays empty rather than becoming a bare newline: a blank line is itself an
// input the server has to decide about, and a helper that invented one would make "no requests"
// untestable.
func protoStream(lines ...string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// protoServe runs one conversation to EOF and returns the RAW output stream, framing intact.
//
// It asserts Serve returned nil: an MCP session ends when the host closes the pipe, so EOF is the
// normal ending and reporting it as a failure would make every clean shutdown look like a crash.
func protoServe(t *testing.T, s Server, lines ...string) string {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, s.Serve(context.Background(), strings.NewReader(protoStream(lines...)), &out),
		"Serve must return nil when its input reaches EOF")
	return out.String()
}

// protoDecode splits a raw output stream into decoded response lines.
//
// The framing contract is asserted HERE rather than in each caller — one JSON object per line,
// every one carrying the protocol version — so that every test in these files gets it for free and
// none of them can forget to.
func protoDecode(t *testing.T, raw string) []wireResponse {
	t.Helper()
	var out []wireResponse
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var resp wireResponse
		require.NoError(t, json.Unmarshal([]byte(line), &resp),
			"every output line must be exactly one JSON object: %q", line)
		require.Equal(t, jsonrpcVersion, resp.JSONRPC,
			"every response line carries the protocol version: %q", line)
		out = append(out, resp)
	}
	return out
}

// protoConverse runs one conversation and returns its decoded response lines, in order.
func protoConverse(t *testing.T, s Server, lines ...string) []wireResponse {
	t.Helper()
	return protoDecode(t, protoServe(t, s, lines...))
}

// protoRequest renders one request line.
//
// A nil id OMITS the member entirely, which is what makes the request a notification: "which id
// was it" and "was there an id at all" are different questions, and the codec answers them
// differently. Passing json.RawMessage("null") is how a test asks the other question.
func protoRequest(t *testing.T, id any, method string, params any) string {
	t.Helper()
	req := map[string]any{"jsonrpc": jsonrpcVersion, "method": method}
	if id != nil {
		req["id"] = id
	}
	if params != nil {
		req["params"] = params
	}
	b, err := json.Marshal(req)
	require.NoError(t, err, "marshalling a %s request line", method)
	return string(b)
}

// protoPing renders a ping with the given id. Ping is the request the framing tests interleave
// with everything else, because it is the only method with no arguments and no state.
func protoPing(t *testing.T, id any) string {
	t.Helper()
	return protoRequest(t, id, methodPing, nil)
}

// protoResult decodes a response line's result, asserting it carried one rather than an error.
func protoResult(t *testing.T, resp wireResponse, v any) {
	t.Helper()
	require.Nil(t, resp.Error, "expected a result, got a JSON-RPC error: %+v", resp.Error)
	require.NotEmpty(t, resp.Result, "a success response must carry a result")
	require.NoError(t, json.Unmarshal(resp.Result, v), "decoding result %s", resp.Result)
}

// protoRPCError asserts a response line carried a protocol error with the given code, and returns
// it. The code is the assertion: a client branches on it, and a -32600 where a -32700 belongs
// tells the host the wrong thing about whether to resend.
func protoRPCError(t *testing.T, resp wireResponse, code int) wireError {
	t.Helper()
	require.Empty(t, resp.Result, "expected a JSON-RPC error, got a result: %s", resp.Result)
	require.NotNil(t, resp.Error, "expected a JSON-RPC error object")
	require.Equal(t, code, resp.Error.Code, "wrong error code for %q", resp.Error.Message)
	require.NotEmpty(t, resp.Error.Message, "an error must say something a human can act on")
	return *resp.Error
}

// TestReadLineFramesAndResynchronizes is the codec contract at its own level: a frame within the
// ceiling arrives whole, a frame over it is DISCARDED rather than truncated, and the frame after a
// discarded one is still readable.
//
// The last of the three is the one that matters. Returning early on an oversized line would leave
// its tail in the stream to be parsed as the next request, which turns one bad frame into an
// unbounded run of them — a failure mode that is invisible at the server level because every one
// of those synthetic frames gets a plausible-looking parse error.
func TestReadLineFramesAndResynchronizes(t *testing.T) {
	const limit = 16

	br := bufio.NewReaderSize(
		strings.NewReader("ok\n"+strings.Repeat("x", 64)+"\nnext\n"), readBufferSize)

	line, tooLong, err := readLine(br, limit)
	require.NoError(t, err)
	require.False(t, tooLong)
	require.Equal(t, "ok\n", string(line), "a frame within the ceiling arrives whole, newline included")

	line, tooLong, err = readLine(br, limit)
	require.NoError(t, err)
	require.True(t, tooLong, "a frame over the ceiling is refused")
	require.Empty(t, line, "a refused frame is discarded, never truncated and handed on to be parsed")

	line, tooLong, err = readLine(br, limit)
	require.NoError(t, err)
	require.False(t, tooLong)
	require.Equal(t, "next\n", string(line), "the stream resynchronizes: the frame after a refused one is served")

	_, _, err = readLine(br, limit)
	require.ErrorIs(t, err, io.EOF, "a drained stream reports EOF, which is how a session ends")
}

// TestReadLineAssemblesAcrossBufferRefills pins the distinction between the reader's BUFFER and
// the accepted line CEILING. Conflating them would refuse every frame larger than 64 KiB while
// reporting a 1 MiB limit, and the refusal would look like a client bug.
func TestReadLineAssemblesAcrossBufferRefills(t *testing.T) {
	// The smallest buffer bufio honours, so the assembly loop runs several times over a line that
	// is still far inside the ceiling.
	const tinyBuffer = 16
	const bodyBytes = 100

	br := bufio.NewReaderSize(strings.NewReader(strings.Repeat("y", bodyBytes)+"\nz\n"), tinyBuffer)

	line, tooLong, err := readLine(br, defaultMaxLine)
	require.NoError(t, err)
	require.False(t, tooLong, "a line larger than the buffer but inside the ceiling is accepted")
	require.Len(t, line, bodyBytes+len("\n"), "it is reassembled whole across refills")

	line, tooLong, err = readLine(br, defaultMaxLine)
	require.NoError(t, err)
	require.False(t, tooLong)
	require.Equal(t, "z\n", string(line))
}

// TestMalformedJSONReturnsParseErrorAndKeepsServing asserts a garbage line is answered with
// -32700 carrying a NULL id — there is no id to echo, and the specification requires the member
// to be present anyway — and that the session survives it.
//
// A server that exited on the first unparseable byte would turn a transient framing glitch into a
// dead session and a model mid-turn with no tools.
func TestMalformedJSONReturnsParseErrorAndKeepsServing(t *testing.T) {
	resps := protoConverse(t, protoServer(t, ServerOptions{}), `{"jsonrpc":`, protoPing(t, 2))
	require.Len(t, resps, 2, "the malformed line is answered and the loop continues")

	protoRPCError(t, resps[0], codeParseError)
	require.Equal(t, "null", string(resps[0].ID), "a parse error has no id to echo, so it echoes null")

	require.Nil(t, resps[1].Error, "the request after a malformed line must still be served")
	require.Equal(t, "2", string(resps[1].ID))
}

// TestWrongJSONRPCVersionRejected asserts a request that names another protocol version is an
// invalid request, not a parse error: the bytes parsed fine, and what failed was the envelope.
// The id IS echoed here, because there was one.
func TestWrongJSONRPCVersionRejected(t *testing.T) {
	resps := protoConverse(t, protoServer(t, ServerOptions{}), `{"jsonrpc":"1.0","id":1,"method":"ping"}`)
	require.Len(t, resps, 1)

	protoRPCError(t, resps[0], codeInvalidRequest)
	require.Equal(t, "1", string(resps[0].ID), "an invalid request still echoes the id it carried")
}

// TestOversizedLineRejectedAndStreamResynchronizes is the readLine resynchronization contract seen
// from the wire: a frame over the configured ceiling is refused with -32600 and a null id, and the
// very next request is answered normally.
//
// The oversized frame is a WELL-FORMED request padded past the limit, not four kilobytes of
// noise, so the test proves the ceiling is what refused it rather than the parser.
func TestOversizedLineRejectedAndStreamResynchronizes(t *testing.T) {
	const tightMaxLine = 1024
	const oversizedBytes = 4096

	padded := protoRequest(t, 1, methodPing, map[string]any{"pad": ""})
	pad := strings.Repeat("a", oversizedBytes-len(padded))
	oversized := protoRequest(t, 1, methodPing, map[string]any{"pad": pad})
	require.Greater(t, len(oversized), tightMaxLine, "the frame must actually exceed the ceiling")

	resps := protoConverse(t, protoServer(t, ServerOptions{MaxLine: tightMaxLine}),
		oversized, protoPing(t, 2))
	require.Len(t, resps, 2, "the refused frame is answered and the next one is served")

	protoRPCError(t, resps[0], codeInvalidRequest)
	require.Equal(t, "null", string(resps[0].ID),
		"a frame refused for length was never parsed, so there is no id to echo")

	require.Nil(t, resps[1].Error, "the stream resynchronizes on the next newline")
	require.Equal(t, "2", string(resps[1].ID))
}

// TestResponseEchoesRequestID asserts the id goes back in the JSON TYPE it arrived in. A client
// correlates responses by comparing ids, and a numeric 7 returned as "7" correlates with nothing.
//
// The explicit-null case is included because it is the one that distinguishes an ABSENT id from a
// null one: an absent id is a notification and gets no response at all, whereas `"id":null` is a
// request whose id happens to be null and is answered.
func TestResponseEchoesRequestID(t *testing.T) {
	resps := protoConverse(t, protoServer(t, ServerOptions{}),
		protoPing(t, 7),
		protoPing(t, "string-id"),
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`)
	require.Len(t, resps, 3)

	require.Equal(t, "7", string(resps[0].ID), "a numeric id is echoed as a number")
	require.Equal(t, `"string-id"`, string(resps[1].ID), "a string id is echoed as a string")
	require.Equal(t, "null", string(resps[2].ID),
		"an explicitly null id is a request with a null id, not a notification")
}

// noisyToolName is the probe tool whose handler logs while it runs. It does not collide with any
// of the eight §8.7 tools or with mcptest's own probes.
const noisyToolName = "protocol_noisy"

// noisyToolText is what noisyTool answers with, so the assertion has something exact to match.
const noisyToolText = "answered"

// noisyTool returns a tool whose handler writes diagnostics at every level before answering.
//
// The logger is the ONLY place a handler may write. The out stream carries JSON-RPC framing and
// nothing else, and a stray Println there does not merely add noise: it lands mid-stream, the
// host's line-oriented parser reads it as a response, and the session desynchronizes permanently.
func noisyTool(log logging.Logger) Tool {
	return Tool{
		Name:        noisyToolName,
		Title:       "Protocol noisy",
		Description: "Logs at every level, then answers. Registered by the protocol tests only.",
		InputSchema: json.RawMessage(probeSchema),
		Handler: func(context.Context, Request) (Response, error) {
			log.Debug("mcp: protocol probe debug", "tool", noisyToolName)
			log.Info("mcp: protocol probe info", "tool", noisyToolName)
			log.Warn("mcp: protocol probe warn", "tool", noisyToolName)
			log.Error("mcp: protocol probe error", "tool", noisyToolName)
			return Response{Content: []Content{{Type: "text", Text: noisyToolText}}}, nil
		},
	}
}

// protoLogContents concatenates every file the logger wrote under dir.
//
// The day log's name carries the real calendar date, so it is enumerated rather than
// reconstructed: reconstructing it would make the test fail once a year, at midnight UTC, in a way
// nobody would connect to this file.
func protoLogContents(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "reading log dir %s", dir)

	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err, "reading %s", e.Name())
		b.Write(body)
	}
	return b.String()
}

// TestNoStdoutPollution asserts the out stream carries JSON-RPC lines and nothing else, even while
// the handler behind the call is logging at every level.
//
// The log file is checked too, and not as decoration: without it the assertion would pass just as
// happily against a handler that logged nothing at all, which is the shape of test that looks
// green and proves nothing.
func TestNoStdoutPollution(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := logging.New(dir, logging.Debug)
	require.NoError(t, err, "logging.New(%s)", dir)
	t.Cleanup(func() { _ = closer.Close() })

	s := protoServer(t, ServerOptions{Log: log})
	require.NoError(t, s.Register(noisyTool(log)))

	raw := protoServe(t, s, protoRequest(t, 1, methodToolsCall,
		map[string]any{"name": noisyToolName, "arguments": map[string]any{}}))

	require.Equal(t, 1, strings.Count(raw, "\n"), "one call produces exactly one line: %q", raw)
	require.True(t, strings.HasSuffix(raw, "\n"), "every response line is newline-terminated: %q", raw)

	resps := protoDecode(t, raw)
	require.Len(t, resps, 1)

	var res wireToolResult
	protoResult(t, resps[0], &res)
	require.False(t, res.IsError)
	require.Equal(t, noisyToolText, res.text())

	// Closed before reading so the day log is flushed and released; sink.Close is idempotent, so
	// the cleanup above is still safe.
	require.NoError(t, closer.Close())
	require.Contains(t, protoLogContents(t, dir), noisyToolName,
		"the handler's diagnostics must have gone SOMEWHERE, or this test asserts nothing")
}

// The concurrency shape of TestConcurrentCallsProduceWellFormedLines: eight writers is more than
// any real host uses and enough that the write mutex is genuinely contended, and twenty-five
// requests each is two hundred lines — long enough for an interleave to be near-certain if the
// mutex were missing, short enough to stay well inside a race-detector run's budget.
const (
	protoConcurrentWriters = 8
	protoPingsPerWriter    = 25
)

// TestConcurrentCallsProduceWellFormedLines drives one server from eight goroutines at once and
// asserts every response is a whole line: two hundred of them, each one JSON object, each id
// answered exactly once.
//
// It is the test that justifies server.wmu. Run under -race it also covers the atomics the
// initialize and oversize paths touch. The requests are built up front, on the test goroutine,
// because require calls t.FailNow and that is only legal from the goroutine running the test.
func TestConcurrentCallsProduceWellFormedLines(t *testing.T) {
	batches := make([][]string, protoConcurrentWriters)
	for w := range batches {
		for i := 0; i < protoPingsPerWriter; i++ {
			batches[w] = append(batches[w], protoPing(t, fmt.Sprintf("w%d-%d", w, i))+"\n")
		}
	}

	pr, pw := io.Pipe()
	// A plain buffer is safe here without a mutex of its own: Serve writes to it from exactly one
	// goroutine, and the channel receive below happens after that goroutine has returned.
	var out bytes.Buffer
	served := make(chan error, 1)
	go func() { served <- protoServer(t, ServerOptions{}).Serve(context.Background(), pr, &out) }()

	var wg sync.WaitGroup
	for w := range batches {
		wg.Add(1)
		go func(lines []string) {
			defer wg.Done()
			for _, line := range lines {
				// One Write per line. io.Pipe gates parallel writes sequentially, so a whole line
				// arrives whole; splitting a line across two writes would let another writer land
				// between them, which would be this test's bug rather than the server's.
				if _, err := io.WriteString(pw, line); err != nil {
					return
				}
			}
		}(batches[w])
	}
	wg.Wait()
	require.NoError(t, pw.Close())
	require.NoError(t, <-served, "Serve must return nil when the writer closes the pipe")

	resps := protoDecode(t, out.String())
	require.Len(t, resps, protoConcurrentWriters*protoPingsPerWriter,
		"one whole response line per request, none merged and none lost")

	seen := make(map[string]bool, len(resps))
	for _, resp := range resps {
		require.Nil(t, resp.Error, "a ping never fails")
		require.JSONEq(t, "{}", string(resp.Result))
		id := string(resp.ID)
		require.False(t, seen[id], "id %s was answered twice", id)
		seen[id] = true
	}
	require.Len(t, seen, protoConcurrentWriters*protoPingsPerWriter, "every id was answered exactly once")
}
