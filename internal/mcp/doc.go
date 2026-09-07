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
// Retrieval metadata marks Qompack representations ephemeral; it does not control host eviction
// or establish native context retention. Content retrieval defaults to a bounded span with an
// explicit full=true escape hatch. Capture, fidelity and coverage may be partial or unknown.
//
// The handlers execute in the DAEMON, not in the `qompack mcp` process. The daemon is the single
// writer of the store and holds the warm handles, and — because Claude Code launches an MCP server
// once per client and hands it no session_id — it is also the only party that can resolve which
// session a call belongs to. The stdio process is a transcoder: it advertises this package's own
// ToolDefs, so `tools/list` is byte-identical on both sides, and forwards every `tools/call` over
// the local transport (see internal/daemon/mcpop.go and internal/cli/cmd_mcp.go).
//
// SP-01 shipped the complete §5.16 type set — Tool, Request, Content, Response, Handler, the
// Server interface, the eight tools' argument and result types, ToolDeps, DropReporter and
// Promoter — as real declarations with every operation stubbed. SP-13 replaced those stubs with
// the implementation, so core.ErrNotImplemented no longer appears anywhere in this package.
package mcp
