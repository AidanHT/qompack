// The real MCP retrieval surface, composed over the rig's real collaborators.
//
// test/e2e is the composition root §3.2 requires for this: internal/mcp may import neither
// internal/rehydrate nor internal/symbols, so only a root may join the real drop reporter, the real
// span widener and the real checkpoint reader to the real ToolDeps. The tools are invoked through
// their REGISTERED Handler — the same func value `qompack mcp` reaches over stdio — so the handler,
// the store, the ledger, the checkpoint reader and the drop reporter are all production code. What
// this composition does NOT cover, and TestStdioServerEndToEnd does, is the JSON-RPC transport.
package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
)

// v4CallBound is the deadline handed to a tool call. Basis: budget B-F is one second for a
// retrieval; ten seconds past it a handler has stopped making progress rather than being slow.
const v4CallBound = 10 * time.Second

// v4Widener adapts symbols.Extractor to mcp.Widener. §3.2 forbids internal/mcp importing
// internal/symbols, so a composition root must perform this adaptation; internal/cli's own
// installMCPTools performs the identical one.
type v4Widener struct{ ex symbols.Extractor }

func (w v4Widener) Widen(path string, b []byte, off, end int64) (int64, int64, bool) {
	if end <= 0 || int(end-1) >= len(b) {
		return off, end, false
	}
	sym, ok := w.ex.Enclosing(path, b, int(end-1))
	if !ok {
		return off, end, false
	}
	newEnd := int64(sym.Offset + sym.Len)
	if newEnd <= end {
		return off, end, false
	}
	return off, newEnd, true
}

func (w v4Widener) Find(path string, b []byte, name string) (int64, int64, bool) {
	for _, sym := range w.ex.Extract(path, b) {
		if sym.Name == name {
			return int64(sym.Offset), int64(sym.Offset + sym.Len), true
		}
	}
	return 0, 0, false
}

// v4ToolDeps builds the production ToolDeps over the rig's real collaborators. Overrides lets a row
// remove exactly one seam for its negative control without rebuilding the rest by hand.
func v4ToolDeps(t *testing.T, r *v4Rig) mcp.ToolDeps {
	t.Helper()
	rd, err := checkpoint.OpenReader(r.P.Root, r.P.Log, obs.New(core.SystemClock()))
	require.NoError(t, err, "the real L4 reader must open over the rig's project")
	return mcp.ToolDeps{
		Store:       r.Opts.Store,
		LedgerFn:    r.LedgerFn(),
		Checkpoints: rd,
		Rehydrator:  v4DropReporter{rep: rehydrate.NewReporter(r.P.Root, r.P.Log)},
		Cfg:         r.P.Cfg,
		Widener:     v4Widener{ex: symbols.New()},
		ProjectRoot: r.P.Root,
	}
}

// v4DropReporter adapts the REAL rehydrate.Reporter — the one the daemon's rehydrate service
// writes its drop report through — to mcp.DropReporter. The interface is declared in internal/mcp
// and satisfied in internal/rehydrate precisely because neither may import the other (§3.2).
type v4DropReporter struct{ rep rehydrate.Reporter }

func (d v4DropReporter) CurrentDrops(ctx context.Context, sess core.SessionID) ([]checkpoint.DropEntry, error) {
	return d.rep.CurrentDrops(ctx, sess)
}

// v4Server registers the eight real tools over deps and returns the server.
func v4Server(t *testing.T, deps mcp.ToolDeps) mcp.Server {
	t.Helper()
	srv := mcp.NewServer(mcp.ServerName, "v4-e2e", logging.Nop())
	require.NoError(t, mcp.RegisterAll(srv, deps), "the eight §8.7 tools must register")
	return srv
}

// v4Call invokes one registered tool's real Handler and decodes its single text block into v.
// It returns the Response so a row can assert on _meta and IsError as well as on the body.
func v4Call(t *testing.T, srv mcp.Server, sess core.SessionID, name string, args map[string]any, v any) mcp.Response {
	t.Helper()

	raw, err := json.Marshal(args)
	require.NoError(t, err)

	var tool *mcp.Tool
	for i := range srv.Tools() {
		if srv.Tools()[i].Name == name {
			tool = &srv.Tools()[i]
			break
		}
	}
	require.NotNil(t, tool, "the tool set must include %s; got %v", name, mcp.ToolNames())

	ctx, cancel := context.WithTimeout(context.Background(), v4CallBound)
	defer cancel()
	res, err := tool.Handler(ctx, mcp.Request{
		Session:  sess,
		Name:     name,
		Args:     raw,
		Deadline: time.Now().Add(v4CallBound),
	})
	require.NoError(t, err, "%s must not raise a PROTOCOL error; a failed retrieval is IsError", name)
	require.Len(t, res.Content, 1, "a tool result carries exactly one text block: %+v", res.Content)
	require.Equal(t, "text", res.Content[0].Type)
	if v != nil {
		require.NoError(t, json.Unmarshal([]byte(res.Content[0].Text), v),
			"the %s body must be JSON: %s", name, res.Content[0].Text)
	}
	return res
}

// v4EnsureLedger opens the negative-knowledge ledger the way a first compaction does, so a row
// that drives `already_tried` has a real ledger behind LedgerFn.
func v4EnsureLedger(t *testing.T, r *v4Rig, sess core.SessionID) {
	t.Helper()
	if r.Opts.Ledger != nil {
		return
	}
	r.CompactStart(t, sess)
	require.Eventually(t, func() bool { return r.Opts.Ledger != nil }, 10*time.Second, 100*time.Millisecond,
		"the first compaction must have opened the negative-knowledge ledger")
}

// v4Project is testutil.NewProject with the e2e shutdown cleanup every row needs.
func v4Project(t *testing.T) *testutil.Project {
	t.Helper()
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	return p
}

// DropReporter returns the REAL rehydrate.Reporter over this rig's project — the same type the
// daemon's rehydrate service writes its drop report through.
func (r *v4Rig) DropReporter() rehydrate.Reporter {
	return rehydrate.NewReporter(r.P.Root, r.P.Log)
}
