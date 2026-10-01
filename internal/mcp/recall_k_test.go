package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// recall's k and the rank of Qompack's own retrieval echoes (V6 close-out D49, F1 of the candidate 4
// live re-run, plans/sdd/V6-closeout/live/rerun-c4/UAT-12/).
//
// k is the number of PERMITTED hits the caller gets back when that many exist. The live run asked
// store.Search for exactly k hits and dropped the denied ones afterwards, so a default k=5 recall
// answered 2 hits and "denied":3 while three more permitted hits existed, the original capture of
// README.md among them. Denied stays a count of what was withheld; it never takes a permitted hit's
// place.
//
// A retrieval self-record — the MCP server's own mcp__qompack__* echo of an answer, or the host's
// capture of a call to one of Qompack's tools (mcp__plugin_qompack_qompack__*) — repeats content an
// original capture already holds. The live run ranked them above that capture (UAT-07, UAT-12); they
// rank after every other hit.

// recallMarker is the text every record in these tests carries, so one query matches all of them.
const recallMarker = "heron-43-k-marker"

// TestRecallKCountsPermittedHits seeds more strongly matching records whose stored path fails
// authorization than the caller's k, and as many permitted ones as k. Every permitted hit must come
// back, and the withheld ones must be counted, not returned.
func TestRecallKCountsPermittedHits(t *testing.T) {
	f := newFixture(t)
	const k = 3
	for i := 0; i < k; i++ {
		// Denied: outside the project root. The marker repeats, so each outscores every permitted
		// record and would fill a top-k cut on its own.
		f.record(t, "Bash", fmt.Sprintf("../outside/denied-%d.txt", i),
			strings.Repeat(recallMarker+" ", 8), core.TurnIndex(10+i))
	}
	permitted := map[string]bool{}
	for i := 0; i < k; i++ {
		p := fmt.Sprintf("src/permitted-%d.txt", i)
		permitted[p] = true
		f.record(t, "FileRead", p, recallMarker, core.TurnIndex(20+i))
	}

	var body recallBody
	resp := f.callOK(t, ToolRecall, map[string]any{"query": recallMarker, "k": k}, &body)

	require.False(t, resp.IsError)
	require.Len(t, body.Hits, k, "k permitted hits exist, so k must come back")
	for _, h := range body.Hits {
		require.True(t, permitted[h.Path], "hit %q is not one of the permitted records", h.Path)
	}
	require.Equal(t, k, body.Count)
	require.Equal(t, k, body.Denied, "the withheld hits ranked ahead of the answer are counted")
	require.NotContains(t, responseText(resp), "denied-", "a denied record's path never reaches the response")
}

// TestRecallDeniedIsACountWhenFewerPermittedExist pins the other side: when fewer permitted hits
// exist than k, all of them come back and every withheld match is counted.
func TestRecallDeniedIsACountWhenFewerPermittedExist(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 4; i++ {
		f.record(t, "Bash", fmt.Sprintf("../outside/denied-%d.txt", i), recallMarker, core.TurnIndex(10+i))
	}
	f.record(t, "FileRead", "src/only.txt", recallMarker, 30)

	var body recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": recallMarker, "k": 5}, &body)

	require.Len(t, body.Hits, 1)
	require.Equal(t, "src/only.txt", body.Hits[0].Path)
	require.Equal(t, 4, body.Denied)
}

// TestRecallRanksSelfRecordsAfterOriginalCaptures seeds the shape the live run met: the original
// capture of a file, then two echoes of it that match the query more often — the server's own
// ephemeral record of a re_read answer and the host's capture of the model's call to re_read. The
// original capture must rank first, and at k=1 it must be the one hit.
func TestRecallRanksSelfRecordsAfterOriginalCaptures(t *testing.T) {
	f := newFixture(t)
	f.record(t, "FileRead", "readme.md", recallMarker, 1)
	echo := strings.Repeat(recallMarker+" ", 6)
	f.record(t, mcpToolPrefix+ToolReRead, "readme.md", echo, 5)
	f.record(t, "mcp__plugin_qompack_qompack__"+ToolExpand, "readme.md", echo, 6)

	var body recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": recallMarker, "k": 5}, &body)

	require.Len(t, body.Hits, 3)
	require.Equal(t, "FileRead", body.Hits[0].Tool,
		"the original capture must rank ahead of Qompack's own retrieval echoes")
	for _, h := range body.Hits[1:] {
		require.True(t, isRetrievalSelfRecord(h.Tool), "hit %q should be a self-record", h.Tool)
	}

	var one recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": recallMarker, "k": 1}, &one)
	require.Len(t, one.Hits, 1)
	require.Equal(t, "FileRead", one.Hits[0].Tool, "at k=1 the original capture is the answer")
}
