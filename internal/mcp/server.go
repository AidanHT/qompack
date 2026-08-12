package mcp

import (
	"context"
	"io"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
)

// Server is the JSON-RPC 2.0 server of 00-ARCHITECTURE.md §5.16. Serve reads requests from an
// io.Reader and writes responses to an io.Writer — stdio, never a socket (D10) — so the same
// server is driven by the host over the plugin's stdio pipe and by a test over a pair of buffers.
type Server interface {
	// Register adds t to the tool set. Registering the same name twice is an error, not a
	// silent replacement: the eight tools are a fixed set, and a duplicate means a wiring bug.
	Register(t Tool) error
	// Serve runs the JSON-RPC 2.0 loop over in/out until in reaches EOF or ctx is done. Framing
	// is newline-delimited JSON.
	Serve(ctx context.Context, in io.Reader, out io.Writer) error
	// Tools returns the registered tools, in registration order.
	Tools() []Tool
}

// NewServer returns a Server that will advertise itself as name/version and log through log.
// Constructing always succeeds, so wave-0 composition roots can wire an mcp.Server today, but
// every operation is a stub until SP-13 lands the real JSON-RPC server (00-ARCHITECTURE.md
// §5.16).
//
// NewServer has no error return, matching every other purely computational constructor in this
// codebase (grammar.New, chunk.New, redact.New): building a server performs no I/O by itself —
// Serve is where the pipes arrive — so there is nothing for the constructor to fail at.
func NewServer(name, version string, log logging.Logger) Server { return stubServer{} }

// stubServer is the SP-01 placeholder Server. SP-13 owns the real implementation.
type stubServer struct{}

// Register always reports core.ErrNotImplemented.
func (stubServer) Register(t Tool) error { return core.ErrNotImplemented }

// Serve always reports core.ErrNotImplemented. It returns immediately without reading from in or
// writing to out: a stub that consumed the caller's stdin would make the host's own MCP handshake
// hang rather than fail fast.
func (stubServer) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	return core.ErrNotImplemented
}

// Tools always returns nil. Tools has no error return, so nil — §14.1 rule 1's documented zero
// value — is the only honest answer: Register never accepted anything, so nothing is registered.
func (stubServer) Tools() []Tool { return nil }
