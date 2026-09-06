package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// The human-readable MCP handshake record, state/mcp.json.
//
// The distinction these tests pin is the one observable.go's own comment calls load-bearing, and
// it is worth restating because getting it wrong is silent. state/mcp.json is NOT the
// mcp.server_registered contract observable: that is contract.SessionHistory.MCPInitialized in
// state/history.json, and state/contract.json is the Monitor's own persisted mode/reason/results.
// A build that wrote the handshake into either of those would leave SP-05's assertion reading a
// file written by the wrong producer — reporting "not-yet-implemented" for ever, or worse,
// overwriting the Monitor's state — while a file on disk said the server was up.
//
// The write path is driven through a real `initialize` where it can be, because Tools:8 is only a
// meaningful number when it was counted off a server that actually has eight tools registered.

// observableRecorder is the ServerOptions.OnInitialize callback under test, plus the two things a
// test needs to check afterwards: how many handshakes it saw, and whether the write failed.
//
// It restamps Observable.TS from the FakeClock. The server itself stamps that field from
// time.Now() — it carries no core.Clock seam — so a test that did not restamp would be asserting
// against the wall clock, and two handshakes in the same millisecond would produce an identical
// timestamp with no bug present. Restamping keeps §6.1's determinism rule intact for the half of
// the record this package's tests actually own.
type observableRecorder struct {
	root  string
	clk   *fakeClock
	calls int
	err   error
	last  Observable
}

// onInitialize persists o under the recorder's root. It runs synchronously inside
// handleInitialize, on whichever goroutine called Serve — the test's own — so it needs no lock.
func (w *observableRecorder) onInitialize(o Observable) {
	o.TS = w.clk.Now().UnixMilli()
	w.calls++
	w.last = o
	w.err = WriteInitializedObservable(w.root, o)
}

// newObservableServer returns a server with all eight tools registered and w wired as its
// initialize observer.
//
// The ToolDeps are ZERO deliberately: the handshake record's Tools count is a property of the
// DEFINITIONS, and binding a store would make this test depend on a project on disk for a number
// that never varies with one.
func newObservableServer(t *testing.T, w *observableRecorder) Server {
	t.Helper()

	srv := NewServerWithOptions(ServerOptions{
		Name: ServerName, Version: core.Version, Log: logging.Nop(), OnInitialize: w.onInitialize,
	})
	require.NoError(t, RegisterAll(srv, ToolDeps{}), "RegisterAll")
	return srv
}

// serveInitialize drives one `initialize` handshake through the real Serve loop.
//
// Serve returns at EOF, so a single-line reader makes the round trip synchronous: no goroutine, no
// timeout, and no sleep, which §6.1 bans outright.
func serveInitialize(t *testing.T, srv Server, clientName, clientVersion string) {
	t.Helper()

	line, err := json.Marshal(map[string]any{
		"jsonrpc": jsonrpcVersion, "id": 1, "method": methodInitialize,
		"params": map[string]any{
			"protocolVersion": supportedProtocolVersions[0],
			"clientInfo":      map[string]any{"name": clientName, "version": clientVersion},
		},
	})
	require.NoError(t, err, "marshalling the initialize line")

	var out bytes.Buffer
	require.NoError(t, srv.Serve(context.Background(), bytes.NewReader(append(line, '\n')), &out),
		"Serve(initialize)")
	require.Contains(t, out.String(), `"serverInfo"`, "the handshake must be answered on the wire")
}

// requireSingleJSONValue asserts b is exactly one JSON value, which is what makes state/mcp.json a
// RECORD rather than an append-only log a reader would have to work backwards through.
func requireSingleJSONValue(t *testing.T, b []byte) {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(b))
	var first json.RawMessage
	require.NoError(t, dec.Decode(&first), "the file must hold valid JSON: %q", string(b))
	require.False(t, dec.More(), "the file must hold exactly one record, not a log of handshakes")
}

// TestWriteInitializedObservable asserts a real handshake leaves the record /qompack:status reads.
func TestWriteInitializedObservable(t *testing.T) {
	root := newFixtureRoot(t, nil)
	w := &observableRecorder{root: root, clk: newFakeClock(epoch)}

	serveInitialize(t, newObservableServer(t, w), "claude-code", "1.2.3")
	require.NoError(t, w.err, "WriteInitializedObservable")
	require.Equal(t, 1, w.calls, "one handshake is one callback")

	b, err := paths.ReadFileShared(ObservablePath(root))
	require.NoError(t, err, "%s must exist after a handshake", ObservablePath(root))
	requireSingleJSONValue(t, b)

	var o Observable
	require.NoError(t, json.Unmarshal(b, &o), "state/mcp.json must unmarshal into an Observable")
	require.True(t, o.Initialized, "a written handshake record means the server came up")
	require.Len(t, ToolNames(), 8, "§8.7 is a closed set of eight tools")
	require.Equal(t, 8, o.Tools, "the record must count the tools that were actually registered")
	require.Equal(t, supportedProtocolVersions[0], o.ProtocolVersion, "the negotiated version must be recorded")
	require.Equal(t, "claude-code", o.ClientName, "the record names which client connected")
	require.Equal(t, "1.2.3", o.ClientVersion, "the record names the client's version")
	require.Equal(t, core.Version, o.ServerVersion, "the record names this build")
	require.Equal(t, os.Getpid(), o.PID, "the record names the process the server ran in")

	read, err := ReadInitializedObservable(root)
	require.NoError(t, err, "ReadInitializedObservable")
	require.Equal(t, o, read, "the reader must return exactly what was written")
}

// TestObservableOverwrittenOnSecondInitialize asserts the record is REPLACED, not appended to.
//
// There is exactly one current answer to "is the MCP server up, and against which client"; a log
// of past handshakes would make a reader work out which line is live, which is precisely the
// question the file exists to answer at a glance.
func TestObservableOverwrittenOnSecondInitialize(t *testing.T) {
	root := newFixtureRoot(t, nil)
	clk := newFakeClock(epoch)
	w := &observableRecorder{root: root, clk: clk}
	srv := newObservableServer(t, w)

	serveInitialize(t, srv, "claude-code", "1.2.3")
	require.NoError(t, w.err, "the first WriteInitializedObservable")
	first, err := ReadInitializedObservable(root)
	require.NoError(t, err, "reading the first record")

	clk.Advance(90 * time.Second)
	serveInitialize(t, srv, "another-host", "9.9.9")
	require.NoError(t, w.err, "the second WriteInitializedObservable")
	require.Equal(t, 2, w.calls, "two handshakes are two callbacks")

	b, err := paths.ReadFileShared(ObservablePath(root))
	require.NoError(t, err, "reading %s after the second handshake", ObservablePath(root))
	requireSingleJSONValue(t, b)

	second, err := ReadInitializedObservable(root)
	require.NoError(t, err, "reading the second record")
	require.Equal(t, epoch.Add(90*time.Second).UnixMilli(), second.TS,
		"the second handshake must restamp the record from the advanced clock")
	require.Greater(t, second.TS, first.TS, "the timestamp must move forward, not stay put")
	require.Equal(t, "another-host", second.ClientName, "the record must name the client that connected last")
	require.True(t, second.Initialized, "the replaced record still reports the server as up")
}

// TestReadInitializedObservableMissingFileIsZero asserts "no handshake has happened" is an ANSWER,
// not a failure: /qompack:status renders it as "not connected" rather than as an error.
func TestReadInitializedObservableMissingFileIsZero(t *testing.T) {
	root := newFixtureRoot(t, nil)

	o, err := ReadInitializedObservable(root)
	require.NoError(t, err, "a missing handshake record is not an error")
	require.Equal(t, Observable{}, o, "a missing handshake record reads as the zero Observable")
	require.False(t, o.Initialized, "nothing has initialized, so Initialized is false")

	empty, err := ReadInitializedObservable("")
	require.NoError(t, err, "an empty project root is not an error on the read side")
	require.Equal(t, Observable{}, empty, "an empty project root reads as the zero Observable")
}

// TestReadInitializedObservableCorruptFileIsError asserts an unreadable record IS an error, and
// that the error names the path.
//
// The asymmetry with the missing-file case is deliberate: "there is no record" is a fact about the
// session, while "there is a record and it is gibberish" is a fact about the filesystem that a
// user has to be pointed at — which they cannot be unless the message says where to look.
func TestReadInitializedObservableCorruptFileIsError(t *testing.T) {
	root := newFixtureRoot(t, nil)
	path := ObservablePath(root)
	require.NoError(t, paths.WriteAtomic(path, []byte("{ not json"), observablePerm), "seeding %s", path)

	o, err := ReadInitializedObservable(root)
	require.Error(t, err, "a corrupt handshake record must not read as a zero-valued success")
	require.ErrorContains(t, err, path, "the error must name the file a user has to look at")
	require.Equal(t, Observable{}, o, "a failed read must return no partially-decoded record")
}

// TestObservablePathIsUnderStateNotContractJSON pins the distinction observable.go's comment calls
// load-bearing: this file is the human-readable record, not the contract observable and not the
// Monitor's own persisted state.
func TestObservablePathIsUnderStateNotContractJSON(t *testing.T) {
	root := newFixtureRoot(t, nil)
	path := ObservablePath(root)

	require.Equal(t, "mcp.json", observableFileName, "the basename is contract with /qompack:status and test/e2e")
	require.Equal(t, filepath.Join(paths.Of(root).State, observableFileName), path,
		"the handshake record lives under .qompack/state/")
	require.True(t, hasSuffixSlash(path, "state/mcp.json"), "ObservablePath must end in state/mcp.json, got %s", path)

	require.NotEqual(t, filepath.Join(paths.Of(root).State, "contract.json"), path,
		"state/contract.json is the Monitor's own persisted mode/reason/results, not the handshake record")
	require.NotEqual(t, filepath.Join(paths.Of(root).State, "history.json"), path,
		"state/history.json holds contract.SessionHistory.MCPInitialized — SP-05's assertion reads that, not this")
}

// hasSuffixSlash reports whether p, in slash form, ends with suffix. Comparing in slash form is
// what keeps the assertion identical on Windows, where ObservablePath returns backslashes.
func hasSuffixSlash(p, suffix string) bool {
	s := filepath.ToSlash(p)
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// TestWriteInitializedObservableRejectsEmptyRoot asserts a write with nowhere to go is refused
// rather than silently landing in the process's working directory.
//
// The read side tolerates an empty root because "no project, no handshake" is an answer; the write
// side cannot, because there is no defensible place to put the file and creating state/mcp.json
// next to whatever the daemon happened to be started from is worse than failing.
func TestWriteInitializedObservableRejectsEmptyRoot(t *testing.T) {
	err := WriteInitializedObservable("", Observable{Initialized: true, Tools: 8})
	require.Error(t, err, "WriteInitializedObservable must refuse an empty project root")
	require.ErrorContains(t, err, observableFileName, "the error must name the file it could not write")
}
