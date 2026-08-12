// Package mcp implements the L6 retrieval layer of 00-ARCHITECTURE.md §5.16: a JSON-RPC 2.0
// server over STDIO, and the eight retrieval tools of Qompack.md §8.7 that let a model ask for
// what it needs instead of being handed everything up front. SP-13 owns the real implementation.
//
// mcp may import ONLY store, negknow and checkpoint, plus the foundation packages core, paths,
// config, logging and obs (00-ARCHITECTURE.md §3.2's mcp allow-set). It may not import rehydrate,
// which is why DropReporter is declared HERE and satisfied there — the `dropped` tool needs a
// rehydrator's answer without the dependency edge that would create a cycle.
//
// The transport is stdio and only stdio. Decision D10 forbids network I/O anywhere in this
// repository, and this package is the one most likely to be mistaken for an exception: an MCP
// server is a server, but this one speaks newline-delimited JSON-RPC over the io.Reader and
// io.Writer its caller hands Serve. There is no listener, no port and no socket. The one place a
// socket exists at all is internal/ipc, which talks to the local daemon.
//
// Two policies keep the retrieval layer from becoming the bloat it exists to solve (§8.7): every
// result is born ephemeral, so analyzer.Block.Ephemeral ranks it first for eviction, and every
// span returned is the minimum sufficient one — the matching function or hunk — with an explicit
// full=true escape hatch.
//
// SP-01 ships the complete §5.16 type set — Tool, Request, Content, Response, Handler, the Server
// interface, the eight tools' argument and result types, ToolDeps, DropReporter and Promoter — as
// real declarations, and every operation as a stub returning core.ErrNotImplemented (or the
// documented zero value, for the one method with no error return). There is no pure function in
// this package's §5 surface for SP-01 to implement for real.
package mcp
