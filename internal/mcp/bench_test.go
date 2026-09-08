package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/obs"
)

// Budget B-F, measured rather than asserted, plus the three benchmarks that say where its time
// goes.
//
// B-F is `mcp_tool_call`: request to response, gated at p95 (internal/obs/budgets.go). Everything
// here runs against benchfixture_test.go's forty-megabyte corpus, because a budget met on a store
// with six objects in it is not a statement about anything — recall's scan bound (512 candidates,
// 32 MB) never engages below a few hundred tool uses, and the whole question is whether retrieval
// still answers inside a quarter of a second once a session has actually been worked in.

// The call mix TestBudgetBF drives, summing to 200. It is weighted the way a session uses the
// tools: expand dominates because a tombstone is the common entry point, recall is the next most
// frequent, and re_read is the one a model reaches for when it wants a file rather than a result.
const (
	budgetBFExpands = 100
	budgetBFRecalls = 60
	budgetBFReReads = 40
	budgetBFCalls   = budgetBFExpands + budgetBFRecalls + budgetBFReReads
)

// budgetBFStride spreads the expand/re_read targets across the whole corpus instead of walking it
// in order, so the measurement is not quietly reading two thousand adjacent objects the operating
// system has already paged in. It is coprime with bigFixtureUses, so the walk visits every index
// before repeating one.
const budgetBFStride = 17

// budgetBFQuery is the broad text query the recall rows use: no path or symbol selector, which is
// deliberately the most expensive shape recall has — every candidate has to be materialized and
// scored, up to the store's own scan bound.
const budgetBFQuery = "pool timeout"

// bfSnapshotMsg renders a histogram snapshot as the four numbers a failure has to show. A budget
// failure that printed only "p95 too high" would send the next reader back to re-run the test just
// to find out by how much and whether the tail or the body moved.
func bfSnapshotMsg(snap obs.HistSnapshot) string {
	return fmt.Sprintf("n=%d p50=%s p95=%s p99=%s max=%s",
		snap.N, snap.P50, snap.P95, snap.P99, snap.Max)
}

// bfExpandTarget returns the tool_use_id of the n-th expand target.
func bfExpandTarget(n int) string {
	return string(bigFixtureToolUseID((n * budgetBFStride) % bigFixtureUses))
}

// bfReReadTarget returns the path of the n-th re_read target, skipping the one in four tool uses
// that is a pathless Bash capture and therefore has no file-version history to resolve.
func bfReReadTarget(n int) string {
	i := (n * budgetBFStride) % bigFixtureUses
	if i%bigFixturePathEvery == 0 {
		i++
	}
	return bigFixturePath(i)
}

// TestBudgetBF is budget B-F as a gate rather than as a number in a design document.
//
// Two things are asserted, and they are not the same thing. The first is the raw percentile
// against the configured limit, which is what a reader wants to see. The second is the Registry's
// own CheckBudgets — the code path the daemon actually uses to decide it has breached — because a
// budget that is only ever checked by its own test is a budget nothing in production enforces.
//
// The histogram is Reset first because the corpus is shared: a benchmark that ran earlier in the
// same binary would otherwise contribute its own samples to this percentile.
// Under declared co-load the behavior and sample-count assertions still apply; the two wall
// judgements are reported here and enforced by the isolated CI timing lane (ADR-0010).
func TestBudgetBF(t *testing.T) {
	if testing.Short() {
		t.Skip(bigFixtureSkip)
	}

	f := newBigFixture(t)

	hist := bfHistName()
	require.NotEmpty(t, hist, "obs.Budgets() no longer declares a B-F budget")
	f.Metrics.Hist(hist).Reset()

	// Interleaved rather than run in three phases, so no one tool's samples occupy a contiguous
	// stretch of the run and inherit whatever the operating system cached for the tool before it.
	expands, recalls, reReads := 0, 0, 0
	for i := 0; i < budgetBFCalls; i++ {
		switch {
		case i%5 < 3 && expands < budgetBFExpands:
			resp := f.call(t, ToolExpand, map[string]any{"tool_use_id": bfExpandTarget(expands)})
			require.False(t, resp.IsError, "expand #%d: %s", expands, responseText(resp))
			expands++
		case i%5 == 3 && recalls < budgetBFRecalls:
			resp := f.call(t, ToolRecall, map[string]any{"query": budgetBFQuery, "k": 5})
			require.False(t, resp.IsError, "recall #%d: %s", recalls, responseText(resp))
			recalls++
		case reReads < budgetBFReReads:
			resp := f.call(t, ToolReRead, map[string]any{"path": bfReReadTarget(reReads)})
			require.False(t, resp.IsError, "re_read #%d: %s", reReads, responseText(resp))
			reReads++
		case expands < budgetBFExpands:
			resp := f.call(t, ToolExpand, map[string]any{"tool_use_id": bfExpandTarget(expands)})
			require.False(t, resp.IsError, "expand #%d: %s", expands, responseText(resp))
			expands++
		default:
			resp := f.call(t, ToolRecall, map[string]any{"query": budgetBFQuery, "k": 5})
			require.False(t, resp.IsError, "recall #%d: %s", recalls, responseText(resp))
			recalls++
		}
	}
	require.Equal(t, budgetBFExpands, expands, "the mix must be the one this test documents")
	require.Equal(t, budgetBFRecalls, recalls)
	require.Equal(t, budgetBFReReads, reReads)

	snap := f.Metrics.Hist(hist).Snapshot()
	require.EqualValues(t, budgetBFCalls, snap.N,
		"every dispatched call must have been observed into %s", hist)

	limit := time.Duration(f.Cfg.Runtime.Budgets.MCPToolCallMs) * time.Millisecond
	if obs.UnderCoload() {
		t.Logf("B-F wall judgement deferred to CI timing: limit=%s %s; all %d dispatch and histogram assertions passed",
			limit, bfSnapshotMsg(snap), budgetBFCalls)
		return
	}
	require.Less(t, snap.P95, limit,
		"B-F breached over %d tool uses / ~%d MB: limit=%s %s",
		bigFixtureUses, bigFixtureUses*bigFixtureUseBytes/(1<<20), limit, bfSnapshotMsg(snap))

	for _, b := range f.Metrics.CheckBudgets(f.Cfg) {
		require.NotEqual(t, string(obs.BF), b.Budget,
			"the Registry's own budget check reports a B-F breach: observed=%s limit=%s windows=%d; %s",
			b.Observed, b.Limit, b.Windows, bfSnapshotMsg(snap))
	}

	t.Logf("B-F over %d tool uses: limit=%s %s", bigFixtureUses, limit, bfSnapshotMsg(snap))
}

// BenchmarkDispatchExpandMinimal measures the tool a post-compaction session leans on hardest:
// re-materializing a cleared result by id, at the default minimal span.
//
// It goes through Dispatch rather than calling the handler, because the preamble Dispatch runs —
// schema defaults, validation, B-F metering and the ephemeral re-store — is part of what one
// expand costs, and a benchmark that skipped it would report a number no caller can ever observe.
func BenchmarkDispatchExpandMinimal(b *testing.B) {
	if testing.Short() {
		b.Skip(bigFixtureSkip)
	}
	f := newBigFixture(b)
	ctx := context.Background()

	args := make([]json.RawMessage, bigFixtureUses)
	for i := range args {
		args[i] = json.RawMessage(`{"tool_use_id":"` + string(bigFixtureToolUseID(i)) + `"}`)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := Dispatch(ctx, f.Server, Request{
			Session: testSession, Name: ToolExpand, Turn: 1,
			Args: args[(i*budgetBFStride)%bigFixtureUses],
		})
		if err != nil || resp.IsError {
			b.Fatalf("expand failed at iteration %d: err=%v resp=%s", i, err, responseText(resp))
		}
	}
}

// BenchmarkDispatchRecall measures the most expensive shape recall has: a bare text query with no
// path or symbol selector, so every candidate the store's scan bound admits is materialized and
// scored.
func BenchmarkDispatchRecall(b *testing.B) {
	if testing.Short() {
		b.Skip(bigFixtureSkip)
	}
	f := newBigFixture(b)
	ctx := context.Background()
	args := json.RawMessage(`{"query":"` + budgetBFQuery + `","k":5}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := Dispatch(ctx, f.Server, Request{
			Session: testSession, Name: ToolRecall, Turn: 1, Args: args,
		})
		if err != nil || resp.IsError {
			b.Fatalf("recall failed at iteration %d: err=%v resp=%s", i, err, responseText(resp))
		}
	}
}

// BenchmarkToolsListMarshal measures what every MCP session pays exactly once, before it can call
// anything: rendering the eight tool descriptors and their schemas onto the wire.
//
// It needs no corpus — tools/list reads no store — which is why it is not gated on -short. The
// schemas are raw literals, so this is a pure marshalling cost, and it is worth watching precisely
// because it is on the handshake path: a regression here delays every session's first tool call.
func BenchmarkToolsListMarshal(b *testing.B) {
	srv := NewServerWithOptions(ServerOptions{Name: ServerName, Version: "bench"})
	if err := RegisterAll(srv, ToolDeps{}); err != nil {
		b.Fatalf("RegisterAll: %v", err)
	}
	concrete, ok := srv.(*server)
	if !ok {
		b.Fatalf("NewServerWithOptions no longer returns *server; the list result is unreachable")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := json.Marshal(concrete.toolsListResult())
		if err != nil {
			b.Fatalf("marshalling the tools/list result: %v", err)
		}
		if len(out) == 0 {
			b.Fatal("the tools/list result marshalled to nothing")
		}
	}
}
