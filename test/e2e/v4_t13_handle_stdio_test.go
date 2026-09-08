package e2e

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// This file closes the gap V4-report.md section 16 names:
//
//	"Installed-host discovery after compaction (T13-HANDLE) remains unverified. Four section-4
//	rows drive the MCP handlers in-process, so the stdio transport and cli's unexported Widener
//	wiring are not covered."
//
// T13-HANDLE, in V4-SP-13's own words, is "Installed agent discovers and resolves handles after
// compact, with bounded pagination and explicit unavailable objects; integration transcript."
//
// TestStdioServerEndToEnd in mcp_e2e_test.go already drives the real binary over real stdio, and
// this deliberately does not repeat it. What it does not do is cross a compaction: it initializes,
// lists, recalls and expands inside one process on a session that never compacted. The three
// clauses left unproven are exactly the ones below — a handle minted BEFORE a compaction resolving
// in a process started AFTER it, paging bounded by the server rather than by the client, and an
// uncaptured handle coming back as an explicit miss rather than as silence.
//
// The Widener matters here for a structural reason rather than a coverage one. mcp.ToolDeps.Widener
// is an interface because architecture §3.2 forbids `mcp` importing `symbols`, so the real adapter
// lives in internal/cli as the unexported symbolWidener and is assembled only by `qompack mcp`.
// internal/cli/cmd_mcp_test.go unit-tests that adapter directly and the section-4 rows substitute a
// fake, so the wire from the composition root to the handler has never carried a real symbol
// lookup. re_read with a ":<symbol>" suffix is the only path that drives it.

const (
	// t13Session is this row's session. It is distinct from mcpE2ESession so that a failure here
	// cannot be confused with one in the row that shares this package's helpers.
	t13Session = core.SessionID("sess-e2e-t13")

	// t13ToolUseID is the tool use whose handle has to survive the compaction.
	t13ToolUseID = "toolu_e2e_t13_01"

	// t13Symbol is a declaration mcpE2EBody actually emits, chosen DEEP in the file on purpose.
	//
	// mcpE2EBody writes `export const poolTimeoutMs%06d` lines of about 68 bytes until it passes
	// 96 KiB, so index 1000 sits near byte 68 000 — far past any window an unanchored expansion
	// would return from the head of the object. An early index would make the assertion below
	// vacuous: the default window already contains it, so the span would look "anchored" whether
	// the Widener ran or not. That is not hypothetical. The first version of this test used index
	// 10 and still passed with the composition root's Widener wiring deliberately severed.
	t13Symbol = "poolTimeoutMs001000"

	// t13AbsentToolUseID was never observed by anything. Expanding it is the "explicit unavailable
	// object" clause.
	t13AbsentToolUseID = "toolu_never_captured_by_any_hook"
)

// TestV4_T13HandleResolvesAfterCompactionOverStdio is the T13-HANDLE integration transcript.
func TestV4_T13HandleResolvesAfterCompactionOverStdio(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	shutdown := sync.OnceFunc(func() { e2eShutdownIfReachable(t, p.Root) })
	t.Cleanup(shutdown)
	env := e2eEnv(p)

	// A real daemon and a real captured tool use, both through real hooks. The ACK precedes the
	// store write, so the index is polled rather than assumed — the same bound mcp_e2e_test.go uses.
	obsRunHook(t, bin, []string{"session-start"}, t13StartPayload(t, p.Root, "startup"), env)
	e2eWaitDaemonUp(t, p.Root)

	obsRunHook(t, bin, []string{"observe", "tool"},
		obsToolPayload(t, p.Root, t13Session, t13ToolUseID, mcpE2EPath, mcpE2EBody()), env)
	require.Eventually(t, func() bool {
		for _, line := range obsToolUseLines(p.Root) {
			if strings.Contains(line, t13ToolUseID) {
				return true
			}
		}
		return false
	}, mcpE2EIndexBound, mcpE2EIndexTick,
		"the observer never indexed %s into index/tool_use.jsonl", t13ToolUseID)

	// ---- Before the compaction: mint a handle, then leave. ----
	//
	// The process is deliberately shut down before the compaction. A handle that only resolves
	// because the same process still holds it in memory would prove nothing about an installed
	// agent, which gets a fresh `qompack mcp` whenever the host restarts one.
	before := mcpE2EStart(t, bin, p)
	t13Initialize(t, before, 1)

	var recalled struct {
		Hits []struct {
			Hash      string `json:"hash"`
			Path      string `json:"path"`
			ToolUseID string `json:"tool_use_id"`
		} `json:"hits"`
		Found bool `json:"found"`
	}
	mcpE2ECall(t, before, 2, mcp.ToolRecall, map[string]any{"query": mcpE2EMarker, "k": 5}, &recalled)
	require.True(t, recalled.Found, "recall must find the content the hook just stored")

	var handle string
	for _, hit := range recalled.Hits {
		if hit.ToolUseID == t13ToolUseID {
			handle = hit.Hash
			require.Equal(t, mcpE2EPath, hit.Path)
		}
	}
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, handle,
		"recall must return a content-addressed handle for %s; hits=%+v", t13ToolUseID, recalled.Hits)

	before.finish(t)

	// ---- The compaction. ----
	//
	// SessionStart(source=compact) is the event the host sends after it compacts, and is what the
	// SP-11 rows in sessionstart_compact_test.go drive. scRunStart asserts §2.3's only permitted
	// outcome: exit 0 carrying one valid hookio.Output.
	//
	// Nothing is asserted about the injected context. Whether a digest comes back is SP-11's row
	// and depends on a checkpoint this test never writes; what T13-HANDLE needs is only that the
	// compaction actually happened before the handle is used again.
	scRunStart(t, bin, env, t13StartPayload(t, p.Root, "compact"))

	// ---- After the compaction: a NEW process, the way an installed agent gets one. ----
	after := mcpE2EStart(t, bin, p)

	// Discovery survives. Both halves matter: the standing instruction is policy the model reads,
	// and the tool set is the closed §8.7 eight.
	initialized := t13Initialize(t, after, 1)
	require.Contains(t, initialized, mcp.StandingInstruction,
		"a post-compaction session must still receive the standing instruction")

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	after.send(t, mcpE2ERequest(t, 2, "tools/list", nil))
	require.NoError(t, json.Unmarshal(after.await(t, 2), &listed))
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	require.Equal(t, mcp.ToolNames(), names,
		"the tool set must survive compaction unchanged, in §8.7 order")

	// The handle minted before the compaction resolves in this new process.
	var expanded struct {
		Found      bool     `json:"found"`
		Hash       string   `json:"hash"`
		Path       string   `json:"path"`
		Span       [2]int64 `json:"span"`
		TotalBytes int64    `json:"total_bytes"`
		Truncated  bool     `json:"truncated"`
		NextSpan   string   `json:"next_span"`
		Content    string   `json:"content"`
	}
	mcpE2ECall(t, after, 3, mcp.ToolExpand, map[string]any{"tool_use_id": t13ToolUseID}, &expanded)
	require.True(t, expanded.Found,
		"a handle minted before the compaction must still resolve after it")
	require.Equal(t, handle, expanded.Hash,
		"the post-compaction expansion must resolve to the same object recall pointed at")
	require.Equal(t, mcpE2EPath, expanded.Path)
	require.NotEmpty(t, expanded.Content)
	require.EqualValues(t, len(expanded.Content), expanded.Span[1]-expanded.Span[0],
		"the returned bytes must be exactly the span the response reports")

	// Bounded pagination, clause two. The first span is short of the whole object and says so, and
	// the cursor it hands back advances rather than repeating — a next_span that returned the same
	// window would page forever.
	require.True(t, expanded.Truncated,
		"a %d KiB object must not come back whole at the default minimal span", mcpE2EBodyBytes/1024)
	require.NotEmpty(t, expanded.NextSpan, "a truncated span must tell the model how to page on")
	require.Less(t, expanded.Span[1], expanded.TotalBytes,
		"a truncated span must end before the object does")

	var paged struct {
		Found bool     `json:"found"`
		Span  [2]int64 `json:"span"`
	}
	mcpE2ECall(t, after, 4, mcp.ToolExpand,
		map[string]any{"tool_use_id": t13ToolUseID, "span": expanded.NextSpan}, &paged)
	require.True(t, paged.Found, "the cursor from a truncated span must resolve")
	require.GreaterOrEqual(t, paged.Span[0], expanded.Span[1],
		"paging on must advance past the window already returned, not repeat it")

	// The composition root's symbol widener, reached over the wire for the first time. A
	// ":<symbol>" suffix is the only argument shape that drives it.
	var anchored struct {
		Found bool     `json:"found"`
		Path  string   `json:"path"`
		Span  [2]int64 `json:"span"`
		Text  string   `json:"content"`
	}
	mcpE2ECall(t, after, 5, mcp.ToolReRead,
		map[string]any{"path": mcpE2EPath + ":" + t13Symbol}, &anchored)
	require.True(t, anchored.Found, "re_read must resolve the captured file after compaction")
	require.Contains(t, anchored.Text, t13Symbol,
		"a :%s anchor must return the span containing that declaration", t13Symbol)
	require.Less(t, anchored.Span[1]-anchored.Span[0], expanded.TotalBytes,
		"an anchored span must be narrower than the whole file, or it anchored nothing")

	// This is the assertion that actually proves the wiring. handlers_span.go tolerates a nil
	// Widener and answers with an unanchored window from the head of the object, so a span that
	// STARTS deep in the file is the only evidence that the composition root's symbolWidener was
	// built, passed down and consulted. Severing that wiring moves this start back to zero.
	require.Greater(t, anchored.Span[0], int64(0),
		"a :%s anchor must start at the declaration, not at the head of the file; span %v means the "+
			"Widener was never consulted", t13Symbol, anchored.Span)

	// Clause three: an uncaptured handle is explicitly unavailable, never silently absent and
	// never quietly successful. Both shapes are accepted — a tool error, or a body reporting
	// found=false — because either tells the model the truth. What fails here is a hit.
	after.send(t, mcpE2ERequest(t, 6, "tools/call", map[string]any{
		"name":      mcp.ToolExpand,
		"arguments": map[string]any{"tool_use_id": t13AbsentToolUseID},
	}))
	var missing mcpE2ECallResult
	require.NoError(t, json.Unmarshal(after.await(t, 6), &missing))
	require.Len(t, missing.Content, 1, "even a miss carries exactly one text block")
	if !missing.IsError {
		var body struct {
			Found bool `json:"found"`
		}
		require.NoError(t, json.Unmarshal([]byte(missing.Content[0].Text), &body),
			"a non-error miss must still be JSON: %s", missing.Content[0].Text)
		require.False(t, body.Found,
			"expanding a handle nothing ever captured must not report found: %s",
			missing.Content[0].Text)
	}

	after.finish(t)
}

// t13Initialize performs the MCP handshake and returns the server's instructions.
func t13Initialize(t *testing.T, c *mcpE2EChild, id int) string {
	t.Helper()

	c.send(t, mcpE2ERequest(t, id, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"clientInfo":      map[string]string{"name": "qompack-t13", "version": "1.0"},
	}))

	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		Instructions    string `json:"instructions"`
	}
	require.NoError(t, json.Unmarshal(c.await(t, id), &initialized))
	require.Equal(t, "2025-06-18", initialized.ProtocolVersion, "a supported version must be echoed")
	return initialized.Instructions
}

// t13StartPayload builds a SessionStart event for this row's session.
//
// sessionstart_compact_test.go's scStartPayload hardcodes its own session id, and the handle under
// test belongs to t13Session, so this row needs its own builder rather than that one. scRunStart
// itself is reused unchanged.
func t13StartPayload(t *testing.T, root, source string) []byte {
	t.Helper()

	b, err := json.Marshal(hookio.Event{
		HookEventName: "SessionStart",
		SessionID:     t13Session,
		CWD:           root,
		Source:        source,
	})
	require.NoError(t, err, "marshalling a %s SessionStart", source)
	return b
}
