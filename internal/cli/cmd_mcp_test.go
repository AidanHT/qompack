package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/symbols"
)

// The `qompack mcp` subcommand's own tests: the two absolute rules of cmd_mcp.go — NOTHING but
// JSON-RPC reaches stdout, and NOTHING is ever spooled — plus the retry loop that carries a call
// across a cold daemon start, and the two mcpwire.go seams nothing else exercises.
//
// mcpRetryDelay (150 ms) and mcpRetryAttempts (10) are package constants and are deliberately NOT
// made injectable: every row below either drives the unexported forwardMCPCall/buildMCPProxy
// directly with a listener that appears on cue, or pays exactly one full retry budget (1.5 s) once.
// A seam added purely so a test could shrink a constant would change the shipped binary to suit
// the test, which is the wrong direction of dependency.

// mcpCmdRoot lays out a fresh project root for one subcommand-level test.
//
// It is EnsureLayout'd up front rather than left to runMCP, because several rows below read
// .qompack/logs and .qompack/spool to prove where a diagnostic went, and a directory that the
// code under test created is a weaker premise than one that already existed.
func mcpCmdRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)), "EnsureLayout(%s)", root)
	return root
}

// mcpCmdEnv is the Env every in-process `qompack mcp` run uses: the project pinned by environment
// so resolveProjectRoot never falls back to the process cwd (which, under `go test
// ./internal/cli/`, is this checkout), a frozen clock, and — critically — Self left EMPTY, which
// is what disables ipc's lazy spawn and keeps a test from launching a real detached daemon.
func mcpCmdEnv(t *testing.T, root string, stdin string) Env {
	t.Helper()
	return Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
		Stdin:   strings.NewReader(stdin),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}
}

// mcpCmdWire is one decoded JSON-RPC line off the server's stdout. Result and Error are raw so a
// row can assert "this line is a well-formed response" without committing to either shape.
type mcpCmdWire struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// mcpCmdLines joins request lines into the newline-delimited stream the server reads.
func mcpCmdLines(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// mcpCmdDecodeStream splits a served stdout into decoded responses, failing on the first line that
// is not one. It is the assertion the "nothing but JSON-RPC" rule reduces to: a stray Fprintln,
// a logger that fell back to stdout or a panic trace all fail here on the line that carries them.
func mcpCmdDecodeStream(t *testing.T, out string) []mcpCmdWire {
	t.Helper()
	var got []mcpCmdWire
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var w mcpCmdWire
		require.NoError(t, json.Unmarshal([]byte(line), &w),
			"stdout line %d is not JSON-RPC: %q", i+1, line)
		require.Equal(t, "2.0", w.JSONRPC, "stdout line %d must carry jsonrpc 2.0: %q", i+1, line)
		require.True(t, len(w.Result) > 0 || len(w.Error) > 0,
			"stdout line %d is neither a result nor an error: %q", i+1, line)
		got = append(got, w)
	}
	return got
}

// mcpCmdToolResult is the tools/call result envelope, as the model reads it.
type mcpCmdToolResult struct {
	Content []mcp.Content   `json:"content"`
	IsError bool            `json:"isError"`
	Meta    map[string]any  `json:"_meta"`
	Tools   json.RawMessage `json:"tools"`
}

// mcpCmdCallResult decodes the tools/call result carried by the response with the given id.
func mcpCmdCallResult(t *testing.T, responses []mcpCmdWire, id string) mcpCmdToolResult {
	t.Helper()
	for _, r := range responses {
		if string(r.ID) != id {
			continue
		}
		require.Empty(t, string(r.Error), "response id=%s must be a result, not a protocol error", id)
		var res mcpCmdToolResult
		require.NoError(t, json.Unmarshal(r.Result, &res), "decoding the result of id=%s", id)
		return res
	}
	require.FailNow(t, "no response carried id="+id)
	return mcpCmdToolResult{}
}

// mcpCmdText concatenates a tool result's text blocks.
func mcpCmdText(res mcpCmdToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// mcpCmdOfflineClient builds the transport a test uses when the point is that NO daemon answers:
// Spawn nil, so nothing is ever launched, and a nopSpool so nothing is ever written.
func mcpCmdOfflineClient(t *testing.T, root string) ipc.Client {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err, "ipc.Resolve(%s)", root)
	return ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), obs.New(testClock()),
		ipc.ClientOptions{ProjectRoot: root, Spawn: nil, Clock: testClock()})
}

// TestCmdMCPWritesNothingButJSONRPCToStdout is cmd_mcp.go's first absolute rule, driven through
// the real subcommand: every line the host would read parses as a JSON-RPC response.
//
// The request stream is chosen to reach every writer inside the server that could plausibly leak —
// a parse error, an invalid-version request, an id-less request the server logs at Debug, and an
// oversized frame that provokes a Loud. maxPayloadBytes is tightened to its legal minimum so the
// oversize case costs 5 KB rather than a megabyte.
//
// The LOUD.log assertion is what stops this test from passing vacuously: it proves the oversize
// diagnostic really was emitted, and that it went to the log tree rather than to the stream.
func TestCmdMCPWritesNothingButJSONRPCToStdout(t *testing.T) {
	root := mcpCmdRoot(t)
	oversize := `{"jsonrpc":"2.0","id":9,"method":"ping","pad":"` + strings.Repeat("x", 5000) + `"}`

	stdin := mcpCmdLines(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",`+
			`"clientInfo":{"name":"seat-f","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`this line is not JSON at all`,
		`{"jsonrpc":"2.0","method":"totally/unknown"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"1.0","id":4,"method":"tools/list"}`,
		oversize,
	)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "mcp", Run: runMCP}},
		[]string{"qompack", "mcp", "--set", "runtime.hotPath.maxPayloadBytes=4096"},
		mcpCmdEnv(t, root, stdin), &out, &errw)

	require.Equal(t, ExitOK, code, "stderr:\n%s", errw.String())
	require.Empty(t, errw.String(), "the subcommand must not diagnose anything on a healthy run")

	responses := mcpCmdDecodeStream(t, out.String())
	require.Len(t, responses, 6,
		"six id-carrying requests and two notifications must produce exactly six responses:\n%s", out.String())

	loud, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Logs, "LOUD.log")))
	require.NoError(t, err, "the oversize frame must have produced a LOUD line in the log tree")
	require.Contains(t, string(loud), "exceeded the accepted limit",
		"the oversize diagnostic must land in LOUD.log, never on stdout")
}

// TestCmdMCPDaemonUnavailableReturnsToolError is §12.3's transport failure made readable: with no
// daemon and lazy spawn disabled outright, a forwarded tools/call comes back as a TOOL error
// carrying daemonUnavailableMsg verbatim — and the server keeps serving, which the trailing ping
// proves.
//
// "Keeps serving" is the half that is easy to lose: an unavailable daemon is a transport fact, and
// a server that treated it as fatal would end the session over a daemon that is about to come up.
func TestCmdMCPDaemonUnavailableReturnsToolError(t *testing.T) {
	root := mcpCmdRoot(t)
	client := mcpCmdOfflineClient(t, root)
	t.Cleanup(func() { _ = client.Close() })

	srv, err := buildMCPProxy(context.Background(), root, config.Defaults(), client, logging.Nop())
	require.NoError(t, err, "buildMCPProxy")

	stdin := mcpCmdLines(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"recall","arguments":{"query":"pool timeout"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	)
	var out bytes.Buffer
	require.NoError(t, srv.Serve(context.Background(), strings.NewReader(stdin), &out), "Serve")

	responses := mcpCmdDecodeStream(t, out.String())
	require.Len(t, responses, 2)

	call := mcpCmdCallResult(t, responses, "1")
	require.True(t, call.IsError, "an unreachable daemon must be a tool error the model reads")
	require.Equal(t, daemonUnavailableMsg, mcpCmdText(call),
		"the message must be the constant verbatim: a model that reads 'retrieval is broken' stops trying")

	require.Equal(t, "2", string(responses[1].ID), "the server must keep serving after a failed call")
	require.NotEmpty(t, responses[1].Result, "ping must still be answered")
}

// mcpCmdSpyDaemon is a real ipc endpoint that refuses its first request and answers every later
// one, standing in for a daemon that is up but not yet ready to serve retrieval.
type mcpCmdSpyDaemon struct {
	mu       sync.Mutex
	requests int
	spawns   int
}

// serve binds a real listener at root's resolved address and starts accepting. It returns the
// cancel that stops it.
func (d *mcpCmdSpyDaemon) serve(t *testing.T, root string) func() {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err, "ipc.Resolve(%s)", root)

	srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
	require.NoError(t, err, "ipc.NewServer(%s)", addr.Path)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, d.handle)
	}()
	return func() {
		cancel()
		_ = srv.Close()
		<-done
	}
}

// handle refuses request #1 with OK:false — the shape a client retries — and answers the rest with
// a decodable MCPOpResponse.
func (d *mcpCmdSpyDaemon) handle(_ context.Context, _ ipc.Request) ipc.Response {
	d.mu.Lock()
	d.requests++
	n := d.requests
	d.mu.Unlock()

	if n == 1 {
		return ipc.Response{OK: false, Err: "still warming up"}
	}
	data, err := json.Marshal(daemon.MCPOpResponse{
		Content: []mcp.Content{{Type: "text", Text: `{"found":true}`}},
	})
	if err != nil {
		return ipc.Response{OK: false, Err: err.Error()}
	}
	return ipc.Response{OK: true, Data: data}
}

// counts reports the spawn and request tallies under the lock.
func (d *mcpCmdSpyDaemon) counts() (spawns, requests int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.spawns, d.requests
}

// TestCmdMCPRetriesUntilListenerAppears drives forwardMCPCall's whole reason for existing: the
// host may call a tool before any daemon is listening, and the answer must be the retrieval, not
// an apology.
//
// The listener appears exactly the way it does in production — inside the lazy-spawn callback, on
// the first failed connect — and then refuses its first request, so the call lands on the THIRD
// attempt: connect-fail, refuse, serve. Spawn is asserted at exactly one invocation because
// ipc's spawnOnce is what stops a retry loop from launching ten daemons, and a regression there
// would be invisible from the response alone.
func TestCmdMCPRetriesUntilListenerAppears(t *testing.T) {
	root := mcpCmdRoot(t)
	spy := &mcpCmdSpyDaemon{}

	var stop func()
	t.Cleanup(func() {
		if stop != nil {
			stop()
		}
	})

	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	client := ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), obs.New(testClock()),
		ipc.ClientOptions{
			ProjectRoot: root,
			Self:        "seat-f-not-a-real-executable",
			Clock:       testClock(),
			Spawn: func(string, string) error {
				spy.mu.Lock()
				spy.spawns++
				spy.mu.Unlock()
				// Binding happens synchronously inside ipc.NewServer, so by the time the retry
				// loop's next attempt dials — one mcpRetryDelay later — the endpoint exists.
				stop = spy.serve(t, root)
				return nil
			},
		})
	t.Cleanup(func() { _ = client.Close() })

	handler := forwardMCPCall(client, logging.Nop())
	resp, err := handler(context.Background(), mcp.Request{
		Name: mcp.ToolRecall, Args: json.RawMessage(`{"query":"pool timeout"}`),
	})
	require.NoError(t, err, "a transport retry must never surface as a protocol error")
	require.False(t, resp.IsError, "the third attempt served the call: %v", resp.Content)
	require.Len(t, resp.Content, 1)
	require.Equal(t, `{"found":true}`, resp.Content[0].Text)

	spawns, requests := spy.counts()
	require.Equal(t, 1, spawns, "ipc's spawnOnce must launch at most one daemon per client")
	require.Equal(t, 2, requests, "one refused request and one served: three attempts in all")
	require.LessOrEqual(t, requests+1, mcpRetryAttempts,
		"the call must land well inside the %d-attempt budget", mcpRetryAttempts)
}

// TestCmdMCPNeverSpools is cmd_mcp.go's second absolute rule: a retrieval is request/response or
// it is nothing.
//
// It drives the PRODUCTION client constructor, newMCPClient, because that is the single place the
// nopSpool decision is made; a test that built its own client would assert its own wiring. The
// control at the end is what keeps the assertion honest — the same failure through a real
// ipc.Spool does create a file, so "no file" is a statement about the spool that was chosen and
// not about the directory being unreachable.
func TestCmdMCPNeverSpools(t *testing.T) {
	root := mcpCmdRoot(t)
	spoolDir := paths.Of(root).Spool

	before, err := ipc.SpoolFiles(spoolDir)
	require.NoError(t, err)
	require.Empty(t, before, "the spool must start empty")

	client := newMCPClient(root, config.Defaults(),
		Env{Getenv: envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}), Clock: testClock()},
		logging.Nop(), obs.New(testClock()), testClock())
	t.Cleanup(func() { _ = client.Close() })

	resp, err := forwardMCPCall(client, logging.Nop())(context.Background(), mcp.Request{
		Name: mcp.ToolExpand, Args: json.RawMessage(`{"hash":"sha256:` + strings.Repeat("0", 64) + `"}`),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError, "with no daemon the call must end in the tool error, not silently")

	after, err := ipc.SpoolFiles(spoolDir)
	require.NoError(t, err)
	require.Empty(t, after, "an MCP call must never spool; found %v", after)

	entries, err := os.ReadDir(paths.Long(spoolDir))
	require.NoError(t, err)
	require.Empty(t, entries, "nothing at all — not a blob, not a partial — may be written to %s", spoolDir)

	// The control: a REAL spool over the same directory does create a file, so the assertions
	// above are about nopSpool and not about an unwritable path.
	control, err := ipc.NewSpool(spoolDir)
	require.NoError(t, err)
	if closer, ok := control.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	require.NoError(t, control.Append(ipc.Request{Op: ipc.OpMCP, TS: core.NowMilli(testClock())}))
	controlFiles, err := ipc.SpoolFiles(spoolDir)
	require.NoError(t, err)
	require.NotEmpty(t, controlFiles, "the control append must prove the spool directory is writable")
}

// countingMCPClient counts the Sends forwardMCPCall makes through it. stall, when set, is how long
// each Send waits before it is forwarded: a host that descheduled the call.
type countingMCPClient struct {
	ipc.Client
	stall time.Duration
	sends atomic.Int32
}

func (c *countingMCPClient) Send(ctx context.Context, req ipc.Request, d time.Duration) (ipc.Response, error) {
	c.sends.Add(1)
	if c.stall > 0 {
		<-time.After(c.stall)
	}
	return c.Client.Send(ctx, req, d)
}

// TestCmdMCPRetryIsCancellable pins the reason mcpRetryDelay is a time.After inside a select and
// not a sleep: the host can close the session mid-retry, and a sleeping goroutine cannot be told.
//
// The context is cancelled BEFORE the handler runs, which is the strictest form of the property:
// the loop must notice at its first opportunity, so it makes exactly one Send and never a retry.
// That count is the verdict. The row used to give the call a fifth of the retry budget (300 ms) on
// a timer, so a host that descheduled the call failed a loop that had noticed at once; its client
// now stalls every Send by 400 ms to pin that this row does not read the clock. The only timer left
// is mcpRetryHangGuard, a hang guard far past the whole retry budget.
func TestCmdMCPRetryIsCancellable(t *testing.T) {
	root := mcpCmdRoot(t)
	client := &countingMCPClient{Client: mcpCmdOfflineClient(t, root), stall: 400 * time.Millisecond}
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	type outcome struct {
		resp mcp.Response
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := forwardMCPCall(client, logging.Nop())(ctx, mcp.Request{
			Name: mcp.ToolDropped, Args: json.RawMessage(`{}`),
		})
		done <- outcome{resp, err}
	}()

	guard := time.NewTimer(mcpRetryHangGuard)
	defer guard.Stop()

	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.True(t, got.resp.IsError, "a cancelled retry still answers the model")
		require.Equal(t, unavailableResponse(), got.resp,
			"a cancelled retry returns the same tool error an exhausted one does")
	case <-guard.C:
		t.Fatalf("forwardMCPCall had not returned after %s with a cancelled context (retry budget %s)",
			mcpRetryHangGuard, time.Duration(mcpRetryAttempts)*mcpRetryDelay)
	}
	require.EqualValues(t, 1, client.sends.Load(),
		"a cancelled context is noticed before the first retry: one Send, not %d", mcpRetryAttempts)
}

// mcpRetryHangGuard bounds TestCmdMCPRetryIsCancellable's wait for a forwardMCPCall that ignored
// cancellation and never returned. It is a hang guard, not the verdict: the Send count is.
const mcpRetryHangGuard = time.Minute

// TestCmdMCPToolsListMatchesDaemonToolSet is the transcoder claim, checked rather than asserted in
// prose: the proxy advertises exactly the eight names of §8.7, in design order, because both sides
// read the same mcp.ToolDefs.
func TestCmdMCPToolsListMatchesDaemonToolSet(t *testing.T) {
	root := mcpCmdRoot(t)
	client := mcpCmdOfflineClient(t, root)
	t.Cleanup(func() { _ = client.Close() })

	srv, err := buildMCPProxy(context.Background(), root, config.Defaults(), client, logging.Nop())
	require.NoError(t, err, "buildMCPProxy")

	var out bytes.Buffer
	require.NoError(t, srv.Serve(context.Background(),
		strings.NewReader(mcpCmdLines(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)), &out))

	responses := mcpCmdDecodeStream(t, out.String())
	require.Len(t, responses, 1)

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(responses[0].Result, &listed))

	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	require.Equal(t, mcp.ToolNames(), names,
		"the proxied tool set must be byte-identical to the daemon's, in §8.7 design order")
}

// TestNewToolDepsNilExtractorLeavesWidenerNil covers the one branch NewToolDeps has, and the
// reason it has it: ToolDeps.Widener is an interface, so assigning a typed nil extractor into it
// would produce a NON-nil interface holding a nil value — a widener every span resolution would
// then call and every call would have to nil-check internally. The guard keeps the field honestly
// nil, which is what handlers_span.go's "tolerates a nil Widener" actually relies on.
func TestNewToolDepsNilExtractorLeavesWidenerNil(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults()

	bare := NewToolDeps(root, cfg, nil, nil, nil, nil, nil, nil, logging.Nop(), nil, testClock())
	require.Nil(t, bare.Widener, "a nil extractor must leave Widener nil, not a non-nil typed nil")
	require.Equal(t, root, bare.ProjectRoot)
	require.Equal(t, cfg, bare.Cfg)

	wired := NewToolDeps(root, cfg, nil, nil, nil, nil, nil, symbols.New(), logging.Nop(), nil, testClock())
	require.NotNil(t, wired.Widener, "a real extractor must be adapted onto the mcp.Widener port")
	require.IsType(t, symbolWidener{}, wired.Widener)
}

// mcpCmdSource is the fixture the widener rows scan. It is Go rather than TypeScript because the
// symbol offsets have to be derivable from the text itself — every assertion below is expressed
// as an index into this string, so a change to the extractor's dialect table fails loudly instead
// of silently shifting a hardcoded number.
const mcpCmdSource = `package pool

// Connect opens the pool.
func Connect(dsn string) error {
	return nil
}

func refreshToken(token string) string {
	// the body the widener must reach the end of
	return token
}

func logout() {}
`

// TestSymbolWidenerFindsAndWidens exercises both halves of the mcpwire.go adapter and all four of
// its refusals, because the refusals are the contract: every "nothing to do" case must report
// ok=false rather than an error, since the caller's response to all of them is identical — keep
// the chunk-aligned window.
func TestSymbolWidenerFindsAndWidens(t *testing.T) {
	t.Parallel()

	b := []byte(mcpCmdSource)
	w := symbolWidener{ex: symbols.New()}
	const path = "internal/pool/pool.go"

	start, end, ok := w.Find(path, b, "refreshToken")
	require.True(t, ok, "the extractor must locate refreshToken in %d bytes", len(b))
	require.Equal(t, int64(strings.Index(mcpCmdSource, "func refreshToken")), start,
		"Find must report the declaration's own offset")
	require.Greater(t, end, start)
	require.Contains(t, mcpCmdSource[start:end], "return token",
		"the found span must cover the whole body, not just the signature")

	// A cut that lands INSIDE refreshToken is the case §8.7's widener exists for: the chunk
	// boundary fell mid-function, and the model would otherwise read half a body.
	cut := start + int64(len("func refreshToken(token string) string {\n"))
	gotOff, gotEnd, ok := w.Widen(path, b, start, cut)
	require.True(t, ok, "a cut inside a symbol must widen")
	require.Equal(t, start, gotOff, "Widen never moves the start")
	require.Equal(t, end, gotEnd, "Widen must land on the enclosing symbol's end")

	// A cut already past the symbol's end has nothing to widen to.
	_, _, ok = w.Widen(path, b, start, end)
	require.False(t, ok, "widening must not move an end that already covers the symbol")

	_, _, ok = w.Widen(path, b, 0, int64(len(b))+1)
	require.False(t, ok, "an end outside the buffer is a refusal, not a panic")

	_, _, ok = w.Widen(path, b, 0, 0)
	require.False(t, ok, "a zero end is a refusal")

	_, _, ok = w.Find(path, b, "noSuchSymbol")
	require.False(t, ok, "an unknown symbol is a refusal, not an error")

	_, _, ok = w.Find(path, b, "")
	require.False(t, ok, "an empty name is a refusal")

	// A nil extractor is the wave-2 build NewToolDeps is written for; both methods must be inert.
	nilw := symbolWidener{}
	off, endOut, ok := nilw.Widen(path, b, 3, 7)
	require.False(t, ok)
	require.Equal(t, int64(3), off)
	require.Equal(t, int64(7), endOut, "a refusing widener returns the window it was given")
	_, _, ok = nilw.Find(path, b, "refreshToken")
	require.False(t, ok)
}

// TestNopSpoolDiscards pins the type cmd_mcp.go hands its client. It is three lines because the
// type is three lines, and it is here because nothing else in the tree constructs one: without it,
// deleting nopSpool's Path() and letting the client fall back to a real spool would compile.
func TestNopSpoolDiscards(t *testing.T) {
	t.Parallel()

	var sw ipc.SpoolWriter = nopSpool{}
	require.NoError(t, sw.Append(ipc.Request{Op: ipc.OpMCP}), "a discarding spool has nothing to fail at")
	require.Empty(t, sw.Path(), "no spool file means no path for /qompack:status to render")
}
