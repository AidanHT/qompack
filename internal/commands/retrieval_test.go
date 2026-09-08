package commands_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/mcp"
)

// fakeServer is a minimal mcp.Server that holds whatever tools a test registers.
//
// internal/commands cannot import internal/mcp's own test doubles, so the fake lives here. It is
// deliberately dumb: the point of these cases is that the FRONTEND does nothing but dispatch, so
// the fake only has to record what it was asked and return what the test told it to.
type fakeServer struct{ tools []mcp.Tool }

func (f *fakeServer) Register(t mcp.Tool) error                         { f.tools = append(f.tools, t); return nil }
func (f *fakeServer) Serve(context.Context, io.Reader, io.Writer) error { return nil }
func (f *fakeServer) Tools() []mcp.Tool                                 { return f.tools }

// recorder captures the requests a frontend dispatched.
type recorder struct{ seen []mcp.Request }

// serverWith returns a server exposing one tool named name, answering with resp.
func serverWith(t *testing.T, name string, rec *recorder, resp mcp.Response, err error) mcp.Server {
	t.Helper()
	s := &fakeServer{}
	require.NoError(t, s.Register(mcp.Tool{
		Name: name,
		Handler: func(_ context.Context, r mcp.Request) (mcp.Response, error) {
			rec.seen = append(rec.seen, r)
			return resp, err
		},
	}))
	return s
}

// runWith runs the named command with the given deps and args.
func runWith(t *testing.T, d commands.Deps, name string, args ...string) (string, error) {
	t.Helper()
	for _, c := range commands.All(d) {
		if c.Name() == name {
			var out bytes.Buffer
			err := c.Run(context.Background(), args, &out)
			return out.String(), err
		}
	}
	t.Fatalf("no such command %q", name)
	return "", nil
}

// depsWith returns test deps carrying the given server.
func depsWith(s mcp.Server) commands.Deps {
	d := testDeps()
	d.MCP = s
	return d
}

// TestRecall_NoSecondImplementation is the SP14-M2-01 gate.
//
// The frontend must reach the registered SP-13 handler and must not search on its own. Deps.Store
// is nil throughout: if recall had grown its own implementation it would need one, and a second
// implementation would mean a second copy of the authorization check that runs before any preview
// is built.
func TestRecall_NoSecondImplementation(t *testing.T) {
	t.Parallel()

	var rec recorder
	s := serverWith(t, mcp.ToolRecall, &rec, mcp.Response{
		Content: []mcp.Content{{Type: "text", Text: `{"hits":[]}`}},
	}, nil)

	d := depsWith(s)
	require.Nil(t, d.Store, "the frontend must not need a store of its own")

	_, err := runWith(t, d, "recall", "open file handle")
	require.NoError(t, err)

	require.Len(t, rec.seen, 1, "exactly one dispatch, to the registered handler")
	require.Equal(t, mcp.ToolRecall, rec.seen[0].Name)

	var args mcp.RecallArgs
	require.NoError(t, json.Unmarshal(rec.seen[0].Args, &args))
	require.Equal(t, "open file handle", args.Query, "the whole phrase reaches the handler")
	require.False(t, rec.seen[0].Deadline.IsZero(), "a frontend call is bounded")
}

// TestRecall_PreservesDenialAndCoverageDistinctions is the rest of SP14-M2-01.
//
// SP-13 renders three different facts that all look alike once summarized: a plain miss
// (found:false), a policy refusal (denied:true) and an answer this build could not establish
// (unavailable). The frontend passes the handler's own bytes through, so all three survive.
func TestRecall_PreservesDenialAndCoverageDistinctions(t *testing.T) {
	t.Parallel()

	body := `{"found":false,"denied":true,"reason":"authorization denied: the associated path is outside the project or could not be resolved safely"}`

	var rec recorder
	out, err := runWith(t, depsWith(serverWith(t, mcp.ToolRecall, &rec, mcp.Response{
		Content: []mcp.Content{{Type: "text", Text: body}},
		Meta:    map[string]any{"coverage": "archive_only", "fidelity": "exact", "scope": "project"},
	}, nil)), "recall", "secret")
	require.NoError(t, err)

	require.Contains(t, out, `"denied":true`, "a refusal must not be flattened into a miss")
	require.Contains(t, out, "authorization denied")
	require.Contains(t, out, "coverage")
	require.Contains(t, out, "archive_only")
	require.Contains(t, out, "fidelity")
	require.Contains(t, out, "exact")
}

// TestRetrieval_MetaReachesTheJSONEnvelopeToo keeps the two renderings at parity: whatever a
// person can see about coverage, a scripted caller can read.
func TestRetrieval_MetaReachesTheJSONEnvelopeToo(t *testing.T) {
	t.Parallel()

	var rec recorder
	out, err := runWith(t, depsWith(serverWith(t, mcp.ToolRecall, &rec, mcp.Response{
		Content:   []mcp.Content{{Type: "text", Text: `{"hits":[{"hash":"sha256:ab"}]}`}},
		Meta:      map[string]any{"coverage": "qompack_included", "truncated": true},
		Ephemeral: true,
	}, nil)), "recall", "--json", "handle")
	require.NoError(t, err)

	env, err := commands.DecodeEnvelope([]byte(out))
	require.NoError(t, err)
	require.True(t, env.OK)

	var res commands.ToolResult
	require.NoError(t, json.Unmarshal(env.Data, &res))
	require.Equal(t, mcp.ToolRecall, res.Tool)
	require.True(t, res.Ephemeral)
	require.Equal(t, "qompack_included", res.Meta["coverage"])
	require.Equal(t, true, res.Meta["truncated"])
	require.Len(t, res.Content, 1)
}

// TestRetrieval_ToolErrorIsNotSuccess keeps a failed retrieval failed. The handler's own
// explanation is kept, because an error the tool wrote is more useful than one this package
// would invent.
func TestRetrieval_ToolErrorIsNotSuccess(t *testing.T) {
	t.Parallel()

	var rec recorder
	out, err := runWith(t, depsWith(serverWith(t, mcp.ToolWhy, &rec, mcp.Response{
		IsError: true,
		Content: []mcp.Content{{Type: "text", Text: "decision index unreadable"}},
	}, nil)), "why", "--json", "d-1")

	require.Error(t, err)
	require.Equal(t, commands.ExitError, commands.ExitCode(err))
	require.Contains(t, err.Error(), "decision index unreadable")

	env, decodeErr := commands.DecodeEnvelope([]byte(out))
	require.NoError(t, decodeErr)
	require.False(t, env.OK)
	require.NotNil(t, env.Data, "the failing answer is still carried, not discarded")

	var res commands.ToolResult
	require.NoError(t, json.Unmarshal(env.Data, &res))
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].Text, "decision index unreadable")
}

// TestRetrieval_NoServerIsUnavailableNotEmpty is the distinction that matters most here. With no
// retrieval server, "no matches" would be a false statement about the archive; the only true
// answer is that nothing could be consulted.
func TestRetrieval_NoServerIsUnavailableNotEmpty(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"recall", []string{"anything"}},
		{"why", []string{"d-1"}},
		{"dropped", nil},
	} {
		out, err := runWith(t, testDeps(), tc.cmd, tc.args...)
		require.ErrorIs(t, err, commands.ErrUnavailable, tc.cmd)
		require.Equal(t, commands.ErrorKindUnavailable, commands.KindOf(err), tc.cmd)
		require.NotContains(t, out, "no matches")
		require.NotContains(t, out, "0 results")
	}
}

// TestRetrieval_UnregisteredToolIsUnavailable separates "this build has no such tool" from "the
// tool ran and failed", which is the distinction mcp.Dispatch draws with ErrToolNotFound.
func TestRetrieval_UnregisteredToolIsUnavailable(t *testing.T) {
	t.Parallel()

	var rec recorder
	// A server that exposes only `why` cannot answer `dropped`.
	_, err := runWith(t, depsWith(serverWith(t, mcp.ToolWhy, &rec, mcp.Response{}, nil)), "dropped")
	require.ErrorIs(t, err, commands.ErrUnavailable)
	require.Contains(t, err.Error(), mcp.ToolDropped)
	require.Empty(t, rec.seen, "the registered tool must not be called in its place")
}

// TestRecall_PaginationFlagReachesTheHandler covers the --k half of the recall surface.
func TestRecall_PaginationFlagReachesTheHandler(t *testing.T) {
	t.Parallel()

	var rec recorder
	_, err := runWith(t, depsWith(serverWith(t, mcp.ToolRecall, &rec, mcp.Response{}, nil)),
		"recall", "--k", "20", "handle leak")
	require.NoError(t, err)

	var args mcp.RecallArgs
	require.NoError(t, json.Unmarshal(rec.seen[0].Args, &args))
	require.Equal(t, 20, args.K)
	require.Equal(t, "handle leak", args.Query)
}

// TestRecall_RejectsANonsenseK keeps a bad page size from silently becoming zero results.
func TestRecall_RejectsANonsenseK(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"0", "-3", "many"} {
		var rec recorder
		_, err := runWith(t, depsWith(serverWith(t, mcp.ToolRecall, &rec, mcp.Response{}, nil)),
			"recall", "--k", bad, "query")
		require.ErrorIs(t, err, commands.ErrUsage, "--k %s", bad)
		require.Empty(t, rec.seen, "--k %s must not reach the handler", bad)
	}
}

// TestRetrieval_RequiredArgumentsAreUsageErrors keeps a missing argument from being dispatched as
// an empty query, which a handler would answer with a confident and meaningless result set.
func TestRetrieval_RequiredArgumentsAreUsageErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ cmd, tool string }{
		{"recall", mcp.ToolRecall},
		{"why", mcp.ToolWhy},
	} {
		var rec recorder
		_, err := runWith(t, depsWith(serverWith(t, tc.tool, &rec, mcp.Response{}, nil)), tc.cmd)
		require.ErrorIs(t, err, commands.ErrUsage, tc.cmd)
		require.Equal(t, commands.ExitUsage, commands.ExitCode(err), tc.cmd)
		require.Empty(t, rec.seen, "%s must not dispatch an empty argument", tc.cmd)
	}
}

// TestDropped_TakesNoArguments matches the tool's own contract: what was dropped is a property of
// the session, not of the question.
func TestDropped_TakesNoArguments(t *testing.T) {
	t.Parallel()

	var rec recorder
	s := depsWith(serverWith(t, mcp.ToolDropped, &rec, mcp.Response{}, nil))

	_, err := runWith(t, s, "dropped", "yesterday")
	require.ErrorIs(t, err, commands.ErrUsage)
	require.Empty(t, rec.seen)

	_, err = runWith(t, s, "dropped")
	require.NoError(t, err)
	require.Len(t, rec.seen, 1)
}

// TestDropped_EphemeralNoteClaimsNothingAboutTheHost keeps the §8.7 marking from being read as a
// statement about native eviction, which ADR 0013 says it is not.
func TestDropped_EphemeralNoteClaimsNothingAboutTheHost(t *testing.T) {
	t.Parallel()

	var rec recorder
	out, err := runWith(t, depsWith(serverWith(t, mcp.ToolDropped, &rec, mcp.Response{
		Content:   []mcp.Content{{Type: "text", Text: `{"categories":[]}`}},
		Ephemeral: true,
	}, nil)), "dropped")
	require.NoError(t, err)
	require.Contains(t, out, "says nothing about host eviction")
}
