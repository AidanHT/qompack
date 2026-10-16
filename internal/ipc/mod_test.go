package ipc_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// updateGolden is the package's -update flag: `go test ./internal/ipc -run TestModRequest_Golden
// -update` rewrites testdata/mod_request.json from goldenModCall. It is the only way that file
// changes.
var updateGolden = func() func() bool {
	if f := flag.Lookup("update"); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	v := flag.Bool("update", false, "rewrite golden files instead of comparing against them")
	return func() bool { return *v }
}()

// goldenModCall is a brief as the CLI hands it to the daemon: the verb, the daemon's budget, and the
// mod's request re-encoded by the CLI.
var goldenModCall = ipc.ModCall{
	Verb:     "brief",
	BudgetMs: 1400,
	Req: json.RawMessage(`{"v":1,"session_id":"3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b",` +
		`"cwd":"/home/dev/project","host_version":"2.1.300","trigger":"auto",` +
		`"user_instructions_present":false,"max_chars":0}`),
}

// TestModRequest_Golden pins the bytes of one OpMod request line: a Reply request whose Raw ("x")
// holds the verb, the budget and the request.
func TestModRequest_Golden(t *testing.T) {
	req, err := ipc.ModRequest("3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b", 1760000000000, goldenModCall)
	require.NoError(t, err)
	require.Equal(t, ipc.OpMod, req.Op)
	require.True(t, req.Reply, "a mod verb waits for its answer")
	got, err := ipc.EncodeRequest(req)
	require.NoError(t, err)

	path := filepath.Join("testdata", "mod_request.json")
	if updateGolden() {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644)) // #nosec G306 -- test fixture
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden %s is missing; run go test ./internal/ipc -run TestModRequest_Golden -update", path)
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	require.Equal(t, string(want), string(got))
}

// TestDecodeModCall_RoundTrips: the daemon reads back exactly the call the CLI built.
func TestDecodeModCall_RoundTrips(t *testing.T) {
	req, err := ipc.ModRequest("s", 1, goldenModCall)
	require.NoError(t, err)
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	back, err := ipc.DecodeRequest(line)
	require.NoError(t, err)

	call, err := ipc.DecodeModCall(back.Raw)
	require.NoError(t, err)
	require.Equal(t, goldenModCall.Verb, call.Verb)
	require.Equal(t, goldenModCall.BudgetMs, call.BudgetMs)
	require.JSONEq(t, string(goldenModCall.Req), string(call.Req))
}

// TestDecodeModCall_RefusesAnIncompleteCall: a call with no verb or no request is refused, and so
// is a Raw that is not a call at all.
func TestDecodeModCall_RefusesAnIncompleteCall(t *testing.T) {
	for name, raw := range map[string]string{
		"absent":     ``,
		"not json":   `brief`,
		"not object": `[1]`,
		"no verb":    `{"budget_ms":1,"req":{"v":1}}`,
		"no request": `{"verb":"brief","budget_ms":1}`,
		"null req":   `{"verb":"brief","budget_ms":1,"req":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ipc.DecodeModCall(json.RawMessage(raw))
			require.Error(t, err)
		})
	}
}

// TestModRequest_RefusesAnIncompleteCall: the CLI cannot build a call the daemon would refuse.
func TestModRequest_RefusesAnIncompleteCall(t *testing.T) {
	_, err := ipc.ModRequest("s", 1, ipc.ModCall{Req: goldenModCall.Req})
	require.Error(t, err, "no verb")
	_, err = ipc.ModRequest("s", 1, ipc.ModCall{Verb: "brief"})
	require.Error(t, err, "no request")
}
