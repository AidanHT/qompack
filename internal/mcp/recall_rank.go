package mcp

import (
	"strings"

	"github.com/qompack/qompack/internal/store"
)

// hostQompackToolPrefix begins the name the host gives every tool of the Qompack plugin's own MCP
// server: `mcp__plugin_<plugin>_<server>__<tool>`, with both the plugin and the server named
// qompack. The observer records a host tool under the name the host sent, so the host's capture of a
// model's call to recall, expand or re_read is filed under it (the V6 live lane's UAT-07 and UAT-12
// recall answers carried such records).
const hostQompackToolPrefix = "mcp__plugin_" + ServerName + "_" + ServerName + "__"

// isRetrievalSelfRecord reports whether a record's tool names one of Qompack's own retrieval
// tools: the MCP server's own ephemeral record of an answer (mcpToolPrefix) or the host's capture of
// a call to one of those tools (hostQompackToolPrefix). Either repeats content an original capture
// already holds, so recall ranks it after every other hit (D49).
func isRetrievalSelfRecord(tool string) bool {
	return strings.HasPrefix(tool, mcpToolPrefix) || strings.HasPrefix(tool, hostQompackToolPrefix)
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
