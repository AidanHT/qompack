package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The small response defects the V6 live lane recorded against the MCP tools (live report
// retrieval D9, C4.4 C44-1/C44-5, UAT-07 F3): an empty recall query was answered although the CLI
// refuses it; an empty already_tried target or approach answered `absent`; and a never-stored hash
// said "complete content provenance could not be established" without saying what was searched.

// TestRecallEmptyQueryIsAToolError: a query with nothing to search for — empty, blank, or only
// selectors with no value — is an argument error, as `qompack recall` with no query is a usage
// error. Answering it returned a confident, meaningless hit list.
func TestRecallEmptyQueryIsAToolError(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"", "   ", "path:", "tool: symbol:"} {
		msg := f.callErr(t, ToolRecall, map[string]any{"query": q})
		require.Equal(t, recallEmptyQueryMsg, msg, "query %q", q)
	}
}

// TestAlreadyTriedEmptyTargetOrApproachIsAToolError: `absent` for an empty target or approach
// asserts that nothing was tried for a question that was never asked.
func TestAlreadyTriedEmptyTargetOrApproachIsAToolError(t *testing.T) {
	f := newFixture(t)
	for name, args := range map[string]map[string]any{
		"empty target":   {"target": "", "approach": "widen pool timeout"},
		"blank target":   {"target": "  ", "approach": "widen pool timeout"},
		"empty approach": {"target": "src/pool.go", "approach": ""},
		"both empty":     {"target": "", "approach": ""},
	} {
		msg := f.callErr(t, ToolAlreadyTried, args)
		require.Equal(t, alreadyTriedEmptyArgsMsg, msg, name)
	}
}

// TestExpandNeverStoredHashNamesWhatWasSearched: a well-formed hash no stored root or chunk carries
// still answers without claiming absence (available:false), and now says where the search went.
func TestExpandNeverStoredHashNamesWhatWasSearched(t *testing.T) {
	f := newFixture(t)

	var body missBody
	resp := f.callOK(t, ToolExpand, map[string]any{"hash": sampleHash}, &body)

	require.False(t, resp.IsError)
	require.False(t, body.Found)
	no := false
	require.Equal(t, &no, body.Available, "a never-stored hash is still not claimed absent")
	require.Equal(t, provenanceSearched, body.Searched, "the miss must name what was searched")
	require.Contains(t, body.Reason, "no indexed root or chunk carries this hash")
	require.Contains(t, body.Reason, "provenance")
}

// TestRecallMalformedPathGlobIsAToolError: a `path:` pattern path.Match cannot parse is the caller's
// mistake, answered as a tool error rather than as "nothing matched".
func TestRecallMalformedPathGlobIsAToolError(t *testing.T) {
	f := newFixture(t)
	msg := f.callErr(t, ToolRecall, map[string]any{"query": "path:src/[*.go"})
	require.Contains(t, msg, "not a valid glob")
}

// TestRecallToolSelectorTakesTheHostName drives `tool:Read` through the MCP tool against a record
// indexed under the display name, the spelling the observer records a host Read under.
func TestRecallToolSelectorTakesTheHostName(t *testing.T) {
	f := newFixture(t)
	f.putAndRecord(t, "FileRead", "src/ledger.go", "package ledger\n", 1)

	for _, q := range []string{"tool:Read", "tool:FileRead", "tool:read path:*.go"} {
		var body recallBody
		f.callOK(t, ToolRecall, map[string]any{"query": q}, &body)
		require.Equal(t, 1, body.Count, "query %q", q)
		require.Equal(t, "src/ledger.go", body.Hits[0].Path, "query %q", q)
	}
}
