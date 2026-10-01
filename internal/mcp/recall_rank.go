package mcp

import (
	"slices"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// isHostQompackToolCall reports whether tool is the host's name for one of Qompack's own MCP tools:
// mcp__plugin_<segment>_qompack__<tool> (core.CutHostPluginTool, which internal/grammar's
// self-suppression shares), with <tool> exactly one of the eight Qompack tools. The observer
// records a host tool under the name the host sent, so the host's capture of a model's call to
// recall, expand or re_read is filed under it (the V6 live lane's UAT-07 and UAT-12 recall answers
// carried such records).
//
// The <segment> is NOT assumed to be qompack. Every install observed so far came from a marketplace
// entry named qompack, so the host named the tools mcp__plugin_qompack_qompack__<tool>. A release
// installs from an entry named qompack-<os>-<arch>, which the host lists as
// qompack-windows-amd64@qompack (packaging/evidence/c7.5-marketplace/entry-name-probe.txt), and the
// segment it derives from such an entry has never been observed (audit F2, V6 close-out D53(e)).
// What does not vary is the server segment, which is .mcp.json's key (ServerName), and the tool,
// which is one of ToolNames(). Another plugin's tool, a tool of another server, or a Qompack-like
// name that is not one of the eight is an ordinary capture.
func isHostQompackToolCall(tool string) bool {
	name, ok := core.CutHostPluginTool(tool, ServerName)
	return ok && slices.Contains(toolNamesInDesignOrder, name)
}

// isRetrievalSelfRecord reports whether a record's tool names one of Qompack's own retrieval
// tools: the MCP server's own ephemeral record of an answer (mcpToolPrefix) or the host's capture of
// a call to one of those tools (isHostQompackToolCall). Either repeats content an original capture
// already holds, so recall ranks it after every other hit (D49).
func isRetrievalSelfRecord(tool string) bool {
	return strings.HasPrefix(tool, mcpToolPrefix) || isHostQompackToolCall(tool)
}

// selfRecordsLast returns hits with every retrieval self-record moved after every other hit. The
// move is stable on both sides, so the store's ranking — score, then recency, then id — still orders
// the originals among themselves and the echoes among themselves.
func selfRecordsLast(hits []store.Hit) []store.Hit {
	out := make([]store.Hit, 0, len(hits))
	var echoes []store.Hit
	for _, h := range hits {
		if isRetrievalSelfRecord(h.Tool) {
			echoes = append(echoes, h)
			continue
		}
		out = append(out, h)
	}
	return append(out, echoes...)
}
