package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/testutil"
)

// The L6 retrieval layer end to end: a real `qompack mcp` process, speaking JSON-RPC over a real
// pipe, forwarding real tools/call requests to a real daemon over the local transport, against a
// project a real hook actually wrote into.
//
// Everything in internal/mcp and internal/cli tests one side of that. This file is the only place
// the two halves meet across a process boundary, which is where the failures that matter live: a
// stdout the host cannot parse, a tool set the daemon does not share, a handshake that never
// reaches state/mcp.json, a span that is not chunk-aligned once a real store did the chunking.

// The bounds this file waits under. Each names the mechanism it waits for.
const (
	// mcpE2EReplyBound bounds one JSON-RPC round trip through the child. Basis: the stdio
	// process's own mcpCallDeadline (5 s) plus its full retry budget (10 x 150 ms) plus margin for
	// process scheduling on a loaded runner — so a timeout here means the child never answered,
	// never that it answered slowly.
	mcpE2EReplyBound = 20 * time.Second
	// mcpE2EExitBound bounds the child's exit after its stdin closes. An MCP session ends at EOF,
	// so this is one drain of an already-empty pipe plus process teardown.
	mcpE2EExitBound = 30 * time.Second
	// mcpE2EIndexBound bounds waiting for the observer to index a hook's event. The ACK is sent
	// after the WAL append, before the store write, so the bytes a recall needs land shortly
	// AFTER the hook process has already exited.
	mcpE2EIndexBound = 30 * time.Second
	mcpE2EIndexTick  = 50 * time.Millisecond
)

// mcpE2ESession is the session the seeded tool uses belong to.
const mcpE2ESession = core.SessionID("sess-e2e-mcp")

// mcpE2EToolUseID is the id of the tool use `expand` addresses. It is fixed rather than discovered
// so the whole session can be driven in one pass: expand needs an address BEFORE recall's answer
// has been read, and a host that had to round-trip recall first would be testing the test.
const mcpE2EToolUseID = "toolu_e2e_mcp_01"

// mcpE2EPath is the file the seeded tool use read.
const mcpE2EPath = "src/pool.ts"

// mcpE2EMarker is the phrase recall searches for. It appears exactly once per seeded body, so a
// hit is evidence the store indexed the content rather than evidence the query matched noise.
const mcpE2EMarker = "pgbouncer transaction mode"

// mcpE2EBodyBytes is how large the seeded tool result is: comfortably more than
// store.chunk.max (16 KiB by default), so the object is chunked into several pieces and a
// minimal-span expand has real boundaries to land on. A single-chunk object would make the
// chunk-alignment assertion below vacuously true.
const mcpE2EBodyBytes = 96 * 1024

// mcpE2EBody generates the seeded tool result: distinct, non-repeating lines so the store cannot
// deduplicate it down to one chunk, opening with the phrase recall looks for.
func mcpE2EBody() string {
	var b strings.Builder
	b.Grow(mcpE2EBodyBytes + 128)
	fmt.Fprintf(&b, "// %s\n// a pool timeout here means %s.\n", mcpE2EPath, mcpE2EMarker)
	for i := 0; b.Len() < mcpE2EBodyBytes; i++ {
		fmt.Fprintf(&b, "export const poolTimeoutMs%06d = %d; // line %d of the seeded capture\n",
			i, 30000+i, i)
	}
	return b.String()
}

// mcpE2EChild is a running `qompack mcp` process with its stdio wired to this test.
//
// stdout is drained by a goroutine into a channel rather than read on demand, for the reason every
// exec-with-pipes helper eventually learns: a child that writes more than one pipe buffer while
// the parent is blocked writing to its stdin deadlocks both. Draining continuously also makes the
// "no response to a notification" assertion possible, because an unexpected line shows up as the
// wrong id on the next await rather than as a hang.
type mcpE2EChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	stderr *bytes.Buffer
}

// mcpE2EStart launches the real binary's `mcp` subcommand against the project p.
func mcpE2EStart(t *testing.T, bin string, p *testutil.Project) *mcpE2EChild {
	t.Helper()
	return mcpE2EStartWithEnv(t, bin, e2eEnv(p))
}

// mcpE2EStartWithEnv is mcpE2EStart over an explicit environment, for a caller whose project root
// is not a testutil.Project's own — an installation rehearsal driving a RESTORED backup root
// (install_test.go). It is the same child in every other respect, so the two paths cannot drift.
func mcpE2EStartWithEnv(t *testing.T, bin string, env map[string]string) *mcpE2EChild {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), bin, "mcp")
	// The build directory, deliberately: a neutral cwd that is not the repository, so a root
	// resolution that fell back to the process cwd could not reach this checkout.
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err, "stdin pipe")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err, "stdout pipe")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	require.NoError(t, cmd.Start(), "starting %s mcp", bin)

	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		// A tools/list result carrying eight schemas, and an expand result carrying a 16 KiB
		// span, both exceed bufio's 64 KiB default line limit.
		sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	return &mcpE2EChild{cmd: cmd, stdin: stdin, lines: lines, stderr: &stderr}
}

// send writes one JSON-RPC line to the child.
func (c *mcpE2EChild) send(t *testing.T, line string) {
	t.Helper()
	_, err := io.WriteString(c.stdin, line+"\n")
	require.NoError(t, err, "writing to the child's stdin: %s", c.stderr.String())
}

// await reads the next line the child emits and asserts it is the response to id.
//
// It reads exactly ONE line, which is what makes the notification assertion meaningful: a server
// that wrongly answered `notifications/initialized` would put that answer here, and the id would
// not match.
func (c *mcpE2EChild) await(t *testing.T, id int) json.RawMessage {
	t.Helper()

	timer := time.NewTimer(mcpE2EReplyBound)
	defer timer.Stop()

	select {
	case line, ok := <-c.lines:
		require.True(t, ok, "the child closed its stdout before answering id=%d; stderr:\n%s", id, c.stderr.String())

		var w struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int            `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &w),
			"every stdout line must be JSON-RPC; got: %q", line)
		require.Equal(t, "2.0", w.JSONRPC)
		require.Empty(t, string(w.Error), "id=%d answered with a protocol error: %s", id, w.Error)
		require.NotNil(t, w.ID, "id=%d: a response must echo its request id; got: %q", id, line)
		require.Equal(t, id, *w.ID, "responses must arrive in request order; got: %q", line)
		require.NotEmpty(t, w.Result)
		return w.Result

	case <-timer.C:
		t.Fatalf("no response to id=%d within %s; stderr:\n%s", id, mcpE2EReplyBound, c.stderr.String())
		return nil
	}
}

// finish closes the child's stdin, drains whatever it writes on the way out, and asserts a clean
// exit with an empty stderr.
//
// An MCP session ends when the host closes the pipe, and `qompack mcp` exits 0 on that EOF: a
// non-zero exit here would mean stdio itself could not be served, which is the only failure the
// subcommand is allowed to surface.
func (c *mcpE2EChild) finish(t *testing.T) {
	t.Helper()
	require.NoError(t, c.stdin.Close(), "closing the child's stdin")

	var trailing []string
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for line := range c.lines {
			trailing = append(trailing, line)
		}
	}()

	waited := make(chan error, 1)
	go func() { waited <- c.cmd.Wait() }()

	timer := time.NewTimer(mcpE2EExitBound)
	defer timer.Stop()
	select {
	case err := <-waited:
		<-drained
		require.NoError(t, err, "the mcp server must exit 0 on EOF; stderr:\n%s", c.stderr.String())
	case <-timer.C:
		_ = c.cmd.Process.Kill()
		t.Fatalf("the mcp server did not exit within %s of its stdin closing; stderr:\n%s",
			mcpE2EExitBound, c.stderr.String())
	}

	require.Empty(t, trailing, "the server wrote unsolicited lines after the last request: %v", trailing)
	require.Empty(t, c.stderr.String(), "nothing may reach the child's stderr on a clean session")
}

// mcpE2ERequest renders one JSON-RPC request line.
func mcpE2ERequest(t *testing.T, id int, method string, params any) string {
	t.Helper()
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	b, err := json.Marshal(payload)
	require.NoError(t, err, "marshalling a %s request", method)
	return string(b)
}

// mcpE2ECallResult is the tools/call envelope as it reaches the host.
type mcpE2ECallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool                      `json:"isError"`
	Meta    map[string]map[string]any `json:"_meta"`
}

// mcpE2ECall drives one tools/call and decodes both the envelope and the tool's JSON body into v.
func mcpE2ECall(t *testing.T, c *mcpE2EChild, id int, name string, args map[string]any, v any) mcpE2ECallResult {
	t.Helper()

	c.send(t, mcpE2ERequest(t, id, "tools/call", map[string]any{"name": name, "arguments": args}))
	raw := c.await(t, id)

	var res mcpE2ECallResult
	require.NoError(t, json.Unmarshal(raw, &res), "decoding the %s result", name)
	require.False(t, res.IsError, "%s reported a tool error: %+v", name, res.Content)
	require.Len(t, res.Content, 1, "a tool result carries exactly one text block")
	require.Equal(t, "text", res.Content[0].Type)

	if v != nil {
		require.NoError(t, json.Unmarshal([]byte(res.Content[0].Text), v),
			"the %s body must be JSON: %s", name, res.Content[0].Text)
	}
	return res
}

// mcpE2EChunkBoundaries returns every legal span edge of the object at h: 0, the cumulative end of
// each chunk, and therefore the object's own end.
//
// It reads through a real store rather than trusting the response, which is the whole point: the
// §8.7 span contract says a minimal span begins and ends on a CHUNK boundary, and only the store
// that did the chunking knows where those are. It must be called with the daemon already stopped —
// the store is single-writer, and a second handle over one append log races its offsets.
func mcpE2EChunkBoundaries(t *testing.T, p *testutil.Project, h core.Hash) (bounds map[int64]bool, canonBytes int64) {
	t.Helper()

	st := p.Store(t)
	rt, err := st.GetRoot(context.Background(), h)
	require.NoError(t, err, "GetRoot(%s)", h)
	require.NotEmpty(t, rt.Chunks, "a stored object must name its chunks")

	bounds = map[int64]bool{0: true}
	var at int64
	for _, c := range rt.Chunks {
		at += int64(c.Len)
		bounds[at] = true
	}
	require.Equal(t, rt.CanonBytes, at, "the chunk lengths must sum to the canonical size")
	return bounds, rt.CanonBytes
}

// TestStdioServerEndToEnd is the SP-13 integration row: build the binary, bring a real daemon up,
// seed it through a real hook, and then drive a full MCP session over the child's stdio.
//
// The order — initialize, notifications/initialized, tools/list, tools/call — is the host's own,
// and it is asserted rather than assumed: every response is read as the NEXT line, so a server
// that answered the notification, or answered out of order, fails on the id.
func TestStdioServerEndToEnd(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	shutdown := sync.OnceFunc(func() { e2eShutdownIfReachable(t, p.Root) })
	t.Cleanup(shutdown)
	env := e2eEnv(p)

	// A real daemon, brought up the way a session brings one up.
	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, mcpE2ESession), env)
	e2eWaitDaemonUp(t, p.Root)

	// A real tool use, observed through a real hook. The ACK is sent after the WAL append and
	// before the store write, so the index is polled rather than assumed.
	obsRunHook(t, bin, []string{"observe", "tool"},
		obsToolPayload(t, p.Root, mcpE2ESession, mcpE2EToolUseID, mcpE2EPath, mcpE2EBody()), env)
	require.Eventually(t, func() bool {
		for _, line := range obsToolUseLines(p.Root) {
			if strings.Contains(line, mcpE2EToolUseID) {
				return true
			}
		}
		return false
	}, mcpE2EIndexBound, mcpE2EIndexTick,
		"the observer never indexed %s into index/tool_use.jsonl", mcpE2EToolUseID)

	child := mcpE2EStart(t, bin, p)

	// 1. initialize.
	child.send(t, mcpE2ERequest(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"clientInfo":      map[string]string{"name": "qompack-e2e", "version": "1.0"},
	}))
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	require.NoError(t, json.Unmarshal(child.await(t, 1), &initialized))
	require.Equal(t, "2025-06-18", initialized.ProtocolVersion, "a supported version must be echoed")
	require.Equal(t, mcp.ServerName, initialized.ServerInfo.Name)
	require.False(t, initialized.Capabilities.Tools.ListChanged, "the eight tools are a closed set")
	require.Contains(t, initialized.Instructions, mcp.StandingInstruction,
		"the standing instruction must reach the model as policy, not merely as an available tool")

	// 2. notifications/initialized — a notification, which carries no id and must therefore
	// produce NO response. A response to a notification is itself a protocol violation, and the
	// proof that none was written is that the very next line the child emits answers id=3.
	child.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	// 3. tools/list.
	child.send(t, mcpE2ERequest(t, 3, "tools/list", nil))
	var listed struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(child.await(t, 3), &listed))
	require.Len(t, listed.Tools, 8, "§8.7 declares exactly eight tools")
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		require.NotEmpty(t, tool.Description, "%s must carry a description the model can act on", tool.Name)
		require.NotEmpty(t, tool.InputSchema, "%s must advertise an input schema", tool.Name)
		names = append(names, tool.Name)
	}
	require.Equal(t, mcp.ToolNames(), names, "the proxied tool set must be the daemon's, in §8.7 order")

	// 4. tools/call recall — the whole point of the layer: the daemon holds the warm store handle,
	// and the stdio process only transcodes.
	var recalled struct {
		Hits []struct {
			Hash      string   `json:"hash"`
			Path      string   `json:"path"`
			Tool      string   `json:"tool"`
			ToolUseID string   `json:"tool_use_id"`
			Span      [2]int64 `json:"span"`
		} `json:"hits"`
		Count int  `json:"count"`
		Found bool `json:"found"`
	}
	mcpE2ECall(t, child, 4, mcp.ToolRecall, map[string]any{"query": mcpE2EMarker, "k": 5}, &recalled)
	require.True(t, recalled.Found, "recall must find the content a hook just stored")
	require.NotEmpty(t, recalled.Hits)
	require.Equal(t, len(recalled.Hits), recalled.Count)

	var matched bool
	for _, hit := range recalled.Hits {
		if hit.ToolUseID == mcpE2EToolUseID {
			matched = true
			require.Equal(t, mcpE2EPath, hit.Path, "a hit must name the path it came from")
			require.Regexp(t, `^sha256:[0-9a-f]{64}$`, hit.Hash, "a hit is a POINTER, ready for expand")
		}
	}
	require.True(t, matched, "recall must return the seeded tool use; hits=%+v", recalled.Hits)

	// 5. tools/call expand — by the id the hook itself used, at the default minimal span.
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
	res := mcpE2ECall(t, child, 5, mcp.ToolExpand,
		map[string]any{"tool_use_id": mcpE2EToolUseID}, &expanded)
	require.True(t, expanded.Found, "expand must re-materialize the cleared result")
	require.NotEmpty(t, expanded.Content)
	require.Equal(t, mcpE2EPath, expanded.Path)
	require.Greater(t, expanded.Span[1], expanded.Span[0], "a span must be non-empty")
	require.EqualValues(t, len(expanded.Content), expanded.Span[1]-expanded.Span[0],
		"the returned bytes must be exactly the span the response reports")
	require.True(t, expanded.Truncated,
		"a %d KiB object must not come back whole at the default minimal span", mcpE2EBodyBytes/1024)
	require.NotEmpty(t, expanded.NextSpan, "a truncated span must tell the model how to page on")

	ephemeral, ok := res.Meta[mcp.ServerName]["ephemeral"].(bool)
	require.True(t, ok, "the result must carry _meta.%s.ephemeral; _meta=%v", mcp.ServerName, res.Meta)
	require.True(t, ephemeral, "a retrieval result is born ephemeral (§8.7)")

	child.finish(t)

	// The handshake record, written by the stdio process at initialize and by the daemon when the
	// forwarded `initialized` report arrived. Either writer satisfies it; a session in which
	// NEITHER ran is what this catches.
	statePath := filepath.Join(paths.Of(p.Root).State, "mcp.json")
	require.FileExists(t, paths.Long(statePath), "the MCP handshake must be recorded in state/mcp.json")

	observable, err := mcp.ReadInitializedObservable(p.Root)
	require.NoError(t, err, "state/mcp.json must be readable")
	require.True(t, observable.Initialized)
	require.Equal(t, 8, observable.Tools, "the handshake record must name the eight tools it served")
	require.Equal(t, "2025-06-18", observable.ProtocolVersion)
	require.NotZero(t, observable.PID)

	// The span contract, checked against the store that actually did the chunking. The daemon is
	// stopped first: the store is single-writer, and a second handle over one append log would
	// race its offsets.
	shutdown()

	hash, err := core.ParseHash(expanded.Hash)
	require.NoError(t, err, "expand must report a parseable root hash")

	bounds, canonBytes := mcpE2EChunkBoundaries(t, p, hash)
	require.Equal(t, canonBytes, expanded.TotalBytes,
		"the response's total_bytes must be the object's canonical size")
	require.True(t, bounds[expanded.Span[0]],
		"span start %d is not a chunk boundary of %s (boundaries: %v)", expanded.Span[0], hash, bounds)
	require.True(t, bounds[expanded.Span[1]],
		"span end %d is not a chunk boundary of %s (boundaries: %v)", expanded.Span[1], hash, bounds)
	require.Greater(t, len(bounds), 2,
		"the seeded object must span several chunks, or the alignment assertion proves nothing")
}

// TestStandingInstructionsAgree is the one-line contract two subplans have to keep across a
// package boundary: `already_tried`'s tool description and the rehydrated context's item 3 quote
// the SAME sentence, so a model that reads either learns the same rule.
//
// SP-01 shipped rehydrate.StandingInstruction() for real — ahead of the rest of that package,
// which still reports core.ErrNotImplemented — precisely so this can be asserted before SP-11
// merges. No skip is needed: the symbol exists on this branch.
func TestStandingInstructionsAgree(t *testing.T) {
	t.Parallel()

	require.Equal(t, rehydrate.StandingInstruction(), mcp.StandingInstruction,
		"mcp.StandingInstruction and rehydrate.StandingInstruction() must agree byte for byte")
	require.NotEmpty(t, mcp.StandingInstruction, "an empty standing instruction would agree vacuously")

	// And it is not merely equal in the constants: it reaches the model through both surfaces the
	// design names — the server's connect-time instructions, and already_tried's own description.
	srv := mcp.NewServer(mcp.ServerName, "test", nil)
	require.NoError(t, mcp.RegisterAll(srv, mcp.ToolDeps{}))

	var found bool
	for _, tool := range srv.Tools() {
		if tool.Name == mcp.ToolAlreadyTried {
			found = true
			require.Contains(t, tool.Description, mcp.StandingInstruction,
				"already_tried's description must carry the standing instruction (G6.2)")
		}
	}
	require.True(t, found, "the tool set must include %s", mcp.ToolAlreadyTried)
}
