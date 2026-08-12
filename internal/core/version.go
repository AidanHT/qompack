package core

// Version is the plugin version. It is overridden at link time by SP-17's release build via
// -X github.com/qompack/qompack/internal/core.Version=<tag>, and is the single source the
// plugin manifest, the MCP server handshake and `qompack version` all read.
var Version = "0.1.0"
