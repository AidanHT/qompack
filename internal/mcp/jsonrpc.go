package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// The JSON-RPC 2.0 wire types and codec, over newline-delimited stdio.
//
// This is hand-rolled rather than taken from a library (decision D6), for the reason that governs
// every dependency choice in this repository: the protocol surface an MCP server needs is five
// methods and one error envelope, and a general JSON-RPC library brings a transport abstraction,
// a batching layer and a $ref resolver that D10 and §7.1 forbid it from ever using. What it would
// save is fifty lines; what it would cost is a dependency that can open a socket.
//
// Framing is one request per line, one response per line, no Content-Length headers — the shape
// internal/mcp/mcptest fixes as normative and the shape the host actually speaks.

// The JSON-RPC 2.0 protocol version string. It is wire format, not a version number this
// repository chooses.
const jsonrpcVersion = "2.0"

// rpcRequest is one decoded request line. An ABSENT id makes it a notification, which is why ID
// is a json.RawMessage and not a string or an int: "which id was it" and "was there an id at all"
// are different questions, and a typed field would answer only the first.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcError is JSON-RPC 2.0's error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// rpcResponse is one response line. ID carries no omitempty on purpose: a parse error has no id to
// echo and the specification requires it to be present and null, which a nil RawMessage marshals
// as.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// The JSON-RPC 2.0 reserved error codes. Every one of them is a PROTOCOL failure — something the
// model never sees. A failed retrieval is a result with isError set, which it does see.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// The five methods this server answers. Anything else is -32601.
const (
	methodInitialize  = "initialize"
	methodInitialized = "notifications/initialized"
	methodToolsList   = "tools/list"
	methodToolsCall   = "tools/call"
	methodPing        = "ping"
)

// notificationPrefix is the namespace every JSON-RPC notification the MCP specification defines
// lives under. One that is not recognised is IGNORED rather than refused: a notification has no
// id, so there is nowhere to put a refusal, and writing an unsolicited object onto the wire is
// itself a protocol violation.
const notificationPrefix = "notifications/"

// defaultMaxLine is the accepted request-line ceiling when a caller supplies none: 1 MiB, the same
// absolute frame limit ipc.MaxLineBytes sets for the daemon transport. Callers pass
// cfg.Runtime.HotPath.MaxPayloadBytes instead, so an operator who tightened that limit tightens
// this one too.
const defaultMaxLine = 1 << 20

// readBufferSize is the bufio.Reader's own buffer. It is not a line limit: a longer line is
// assembled across refills, and the maxLine ceiling is what actually bounds one.
const readBufferSize = 64 << 10

// readLine reads one newline-terminated line, refusing at most maxLine bytes of it.
//
// A line over the ceiling is NOT an early return: the remainder is consumed up to the next
// newline, so the stream resynchronizes and the request after an oversized one is still served.
// Returning early instead would leave the tail of the discarded payload to be parsed as the next
// request, which turns one bad frame into an unbounded run of them.
func readLine(br *bufio.Reader, maxLine int) (line []byte, tooLong bool, err error) {
	for {
		frag, ferr := br.ReadSlice('\n')
		if len(frag) > 0 {
			if !tooLong && len(line)+len(frag) > maxLine {
				tooLong, line = true, nil
			}
			if !tooLong {
				line = append(line, frag...)
			}
		}
		if errors.Is(ferr, bufio.ErrBufferFull) {
			continue
		}
		return line, tooLong, ferr
	}
}

// writeResponse marshals and writes one response line. Every write goes through the server's own
// mutex, so two goroutines answering concurrently can never interleave bytes on one line.
func (s *server) writeResponse(resp rpcResponse) error {
	resp.JSONRPC = jsonrpcVersion
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.enc == nil {
		return io.ErrClosedPipe
	}
	return s.enc.Encode(resp)
}

// writeError writes one protocol error, echoing id when there was one.
func (s *server) writeError(id json.RawMessage, code int, msg string, data any) error {
	return s.writeResponse(rpcResponse{ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}})
}
