package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// Ephemeral-at-birth (Qompack.md §8.7, gap GB): the policy half of the retrieval layer.
//
// The assertions here are deliberately split across two altitudes, because the policy has two
// halves that fail independently. What a HOST sees is `_meta.qompack.ephemeral` on the JSON-RPC
// result envelope, so the table test drives the real Serve loop over a pair of buffers rather
// than reading Response.Ephemeral — a Go field a host never reads, and one that could stay true
// while the envelope lost the key. What SP-12 sees is a store.ToolUseRecord with Ephemeral set,
// which is what makes analyzer.Block.Ephemeral rank the block first for eviction; that half is
// asserted against index/tool_use.jsonl and the store's own reader.
//
// Every failure inside the recording path is swallowed by design (a retrieval that succeeded must
// never be turned into a failure by its own bookkeeping), so the negative cases below assert on
// the ABSENCE of a record rather than on an error, and on the obs counter that makes the swallowed
// failure visible.

// ephemeralWireError is the JSON-RPC error object, decoded only so a protocol failure names
// itself instead of surfacing as a confusing zero-valued result.
type ephemeralWireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ephemeralWireResult is one response line as a HOST decodes it. It is hand-declared rather than
// reusing the server's own rpcResponse/toolCallResult because those are the encode side: a test
// that asserted against them would prove the server agrees with itself, not that the bytes on the
// wire carry the key §8.7 requires.
type ephemeralWireResult struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		Content []Content                 `json:"content"`
		IsError bool                      `json:"isError"`
		Meta    map[string]map[string]any `json:"_meta"`
	} `json:"result"`
	Error *ephemeralWireError `json:"error"`
}

// qompackMeta returns the `_meta.qompack` object, or nil when the envelope carried no namespaced
// metadata at all — which is itself a §8.7 violation and must be distinguishable from `false`.
func (r ephemeralWireResult) qompackMeta() map[string]any { return r.Result.Meta[metaNamespace] }

// serveEphemeralCall writes one `tools/call` line into a real Serve loop and decodes the one
// response line it writes back.
//
// Serve returns at EOF, so a single-line reader makes the whole round trip synchronous and needs
// no goroutine, no timeout and no sleep — §6.1 bans the last of those outright.
func serveEphemeralCall(t *testing.T, srv Server, name string, args map[string]any) ephemeralWireResult {
	t.Helper()

	raw, err := json.Marshal(args)
	require.NoError(t, err, "marshalling arguments for %s", name)
	line, err := json.Marshal(map[string]any{
		"jsonrpc": jsonrpcVersion, "id": 1, "method": methodToolsCall,
		"params": map[string]any{"name": name, "arguments": json.RawMessage(raw)},
	})
	require.NoError(t, err, "marshalling the tools/call line for %s", name)

	var out bytes.Buffer
	require.NoError(t, srv.Serve(context.Background(), bytes.NewReader(append(line, '\n')), &out),
		"Serve(%s)", name)

	var got ephemeralWireResult
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &got),
		"%s: the server wrote something that is not one JSON-RPC line: %q", name, out.String())
	require.Nil(t, got.Error, "%s answered with a JSON-RPC protocol error: %+v", name, got.Error)
	return got
}

// ephemeralIndexLines returns every index/tool_use.jsonl line mentioning the `mcp__qompack__`
// prefix — the durable half of the policy, read off disk rather than out of the store's memory.
//
// It reads through paths.ReadFileShared because the store holds an append-only handle open on the
// same file for the life of the fixture, and a plain os.ReadFile races that handle's share mode on
// Windows.
func ephemeralIndexLines(t *testing.T, root string) []string {
	t.Helper()

	b, err := paths.ReadFileShared(filepath.Join(paths.Of(root).Index, "tool_use.jsonl"))
	if err != nil {
		// No index file at all is the strongest possible form of "nothing was recorded".
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, mcpToolPrefix) {
			out = append(out, line)
		}
	}
	return out
}

// syntheticEphemeralID rebuilds the id recordEphemeral mints, so a test can assert determinism
// against the derivation itself rather than against a value copied out of a previous run.
func syntheticEphemeralID(sess core.SessionID, tool string, args json.RawMessage,
	ts core.UnixMilli,
) core.ToolUseID {
	return core.ToolUseID(ephemeralIDPrefix +
		core.HashBytes(domainMCPToolUse, ephemeralSeed(sess, tool, args, ts)).Short())
}

// failingPutStore is a store.Store whose PutBytes always fails and whose every other method is the
// real one.
//
// Embedding rather than reimplementing is the point: §8.7's guarantee is that a retrieval which
// SUCCEEDED is not turned into a failure by its bookkeeping, so the tool must still resolve its
// content through a fully working store and only the ephemeral write may break.
type failingPutStore struct {
	store.Store
	// err is what PutBytes reports. It is a field rather than a constant so a failure message can
	// name the exact error the handler was supposed to swallow.
	err error
}

// PutBytes always fails, which is the only reason this type exists.
func (s *failingPutStore) PutBytes(context.Context, []byte, store.PutOptions) (store.PutResult, error) {
	return store.PutResult{}, s.err
}

// failingPutStore must still satisfy the full Store contract, or RegisterAll would not accept it.
var _ store.Store = (*failingPutStore)(nil)

// TestEveryRetrievalResponseCarriesEphemeralMeta asserts the §8.7 flag reaches the HOST, for each
// of the seven retrieval tools, through the real JSON-RPC transport.
//
// The seven are asserted one by one rather than as a set because the flag is computed per tool at
// registration (`ephemeralTools[name] && cfg.Retrieval.EphemeralResults`), so a single missing map
// entry would be invisible in any assertion that only checked "at least one tool is ephemeral".
func TestEveryRetrievalResponseCarriesEphemeralMeta(t *testing.T) {
	f, c := newSeededFixture(t)

	cases := []struct {
		tool string
		args map[string]any
	}{
		{ToolRecall, map[string]any{"query": "pool timeout"}},
		{ToolExpand, map[string]any{"hash": c.AuthRoot.String()}},
		{ToolReRead, map[string]any{"path": "src/auth.ts"}},
		{ToolAlreadyTried, map[string]any{"target": "src/auth.ts", "approach": "widen pool timeout"}},
		{ToolTimeline, map[string]any{}},
		{ToolWhy, map[string]any{"decision_id": "dec_0123456789ab"}},
		{ToolDropped, map[string]any{}},
	}
	require.Len(t, cases, len(ephemeralTools), "the table must cover every ephemeral tool")

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			got := serveEphemeralCall(t, f.Server, tc.tool, tc.args)

			meta := got.qompackMeta()
			require.NotNil(t, meta, "%s: the result envelope carries no _meta.%s at all", tc.tool, metaNamespace)
			require.Equal(t, true, meta[metaEphemeral],
				"%s: a host must see _meta.%s.%s = true (§8.7)", tc.tool, metaNamespace, metaEphemeral)
			require.True(t, ephemeralTools[tc.tool], "%s must be in ephemeralTools", tc.tool)
		})
	}
}

// TestEphemeralToolUseRecordWritten asserts the durable half: the response body is stored and
// indexed under the synthetic id the envelope advertises, with Ephemeral set and filed under the
// host's own `mcp__<server>__<tool>` spelling.
//
// Both halves matter to a different consumer. Ephemeral is what SP-12's droppable-block ranking
// reads; Tool is what makes a retrieval-written record recognisable as one in a tombstone and in a
// log line.
func TestEphemeralToolUseRecordWritten(t *testing.T) {
	f, c := newSeededFixture(t)

	resp := f.callOK(t, ToolExpand, map[string]any{"hash": c.AuthRoot.String()}, nil)

	id, ok := resp.Meta[metaToolUseID].(string)
	require.True(t, ok, "expand published no %s in _meta: %+v", metaToolUseID, resp.Meta)
	require.True(t, strings.HasPrefix(id, ephemeralIDPrefix),
		"a synthetic id must be distinguishable from a host-issued toolu_ id at a glance, got %q", id)

	rec, err := f.Store.ToolUse(t.Context(), core.ToolUseID(id))
	require.NoError(t, err, "ToolUse(%s)", id)
	require.True(t, rec.Ephemeral, "the retrieval record must be born ephemeral (§8.7)")
	require.Equal(t, mcpToolPrefix+ToolExpand, rec.Tool,
		"the record must be filed under the host's own mcp__<server>__<tool> spelling")
	require.Equal(t, testSession, rec.Session, "the record must belong to the calling session")

	hash, ok := resp.Meta[metaHash].(string)
	require.True(t, ok, "expand published no %s in _meta: %+v", metaHash, resp.Meta)
	require.Equal(t, hash, rec.Root.String(), "the advertised hash must be the record's own root")
}

// TestEphemeralRecordCarriesTurn asserts Request.Turn reaches the record.
//
// Turn is the SP-13 additive field the daemon resolves from the store's open segment, and it is
// what orders an ephemeral record against the ordinary tool results around it. It is driven
// through Dispatch rather than through the fixture's call helper because that helper pins Turn to
// 1, and 1 is indistinguishable from a field nobody wired.
func TestEphemeralRecordCarriesTurn(t *testing.T) {
	f, c := newSeededFixture(t)

	const turn core.TurnIndex = 37
	args, err := json.Marshal(map[string]any{"hash": c.AuthRoot.String()})
	require.NoError(t, err, "marshalling expand arguments")

	resp, err := Dispatch(t.Context(), f.Server, Request{
		Session: testSession, Name: ToolExpand, Args: args, Turn: turn,
		Deadline: f.Clock.Now().Add(time.Minute),
	})
	require.NoError(t, err, "Dispatch(expand)")
	require.False(t, resp.IsError, "expand reported a tool error: %s", responseText(resp))

	id, ok := resp.Meta[metaToolUseID].(string)
	require.True(t, ok, "expand published no %s in _meta: %+v", metaToolUseID, resp.Meta)

	rec, err := f.Store.ToolUse(t.Context(), core.ToolUseID(id))
	require.NoError(t, err, "ToolUse(%s)", id)
	require.Equal(t, turn, rec.Turn, "the ephemeral record must carry the request's turn, not 0")
}

// TestEphemeralDisabledByConfig asserts retrieval.ephemeralResults:false switches BOTH halves off.
//
// Off must mean off end to end: a build that still flagged the envelope would have SP-12 evicting
// blocks first that were never indexed for re-expansion, which is the one combination that loses
// content the operator asked to keep.
func TestEphemeralDisabledByConfig(t *testing.T) {
	f, c := newSeededFixture(t, withConfig(func(cfg *config.Config) {
		cfg.Retrieval.EphemeralResults = false
	}))

	resp := f.callOK(t, ToolExpand, map[string]any{"hash": c.AuthRoot.String()}, nil)
	require.False(t, resp.Ephemeral, "ephemeralResults:false must clear Response.Ephemeral")
	require.NotContains(t, resp.Meta, metaToolUseID, "a disabled build must publish no synthetic id")
	require.NotContains(t, resp.Meta, metaHash, "a disabled build must publish no ephemeral hash")

	got := serveEphemeralCall(t, f.Server, ToolExpand, map[string]any{"hash": c.AuthRoot.String()})
	require.Equal(t, false, got.qompackMeta()[metaEphemeral],
		"a host must see _meta.%s.%s = false when the policy is off", metaNamespace, metaEphemeral)

	require.Empty(t, ephemeralIndexLines(t, f.Root),
		"ephemeralResults:false must write no %s record at all", mcpToolPrefix)
}

// TestEphemeralStoreFailureDoesNotFailTheCall asserts the swallow rule: the model already has the
// bytes, so a bookkeeping failure costs the eviction ranking and nothing else.
//
// The obs counter is asserted alongside the result because "swallowed" must not mean "silent":
// §11.2's retrieval hit rate is computed from these counters, and a failure that incremented
// nothing would be indistinguishable from a build where the policy is simply off.
func TestEphemeralStoreFailureDoesNotFailTheCall(t *testing.T) {
	f, c := newSeededFixture(t)

	deps := f.Deps
	deps.Store = &failingPutStore{Store: f.Store, err: errors.New("no space left on device")}
	srv := NewServerWithOptions(ServerOptions{Name: ServerName, Version: core.Version, Log: logging.Nop()})
	require.NoError(t, RegisterAll(srv, deps), "RegisterAll over a failing-put store")

	args, err := json.Marshal(map[string]any{"hash": c.AuthRoot.String()})
	require.NoError(t, err, "marshalling expand arguments")

	resp, err := Dispatch(t.Context(), srv, Request{
		Session: testSession, Name: ToolExpand, Args: args, Turn: 1,
		Deadline: f.Clock.Now().Add(time.Minute),
	})
	require.NoError(t, err, "Dispatch(expand)")
	require.False(t, resp.IsError, "a failed ephemeral write must not fail the retrieval: %s", responseText(resp))
	require.NotEmpty(t, responseText(resp), "the retrieval must still hand back its content")
	require.NotContains(t, resp.Meta, metaToolUseID,
		"an id that was never indexed must not be advertised as expandable")

	require.Equal(t, int64(1), f.Metrics.Snapshot().Counters[counterEphemeralFailed],
		"a swallowed failure must still be counted (§11.2)")
}

// TestSyntheticToolUseIDIsDeterministic asserts the derivation of §6.1's determinism rule: the
// same (session, tool, args, timestamp) always mints the same id, and the timestamp is genuinely
// part of the seed.
//
// The timestamp's presence is what stops the SAME call made twice from collapsing into one record
// silently overwriting the other, so it is asserted as a difference rather than assumed from the
// seed's shape.
func TestSyntheticToolUseIDIsDeterministic(t *testing.T) {
	args := json.RawMessage(`{"hash":"sha256:` + strings.Repeat("ab", 32) + `"}`)
	const ts core.UnixMilli = 1767225600000

	first := syntheticEphemeralID(testSession, ToolExpand, args, ts)
	second := syntheticEphemeralID(testSession, ToolExpand, args, ts)
	require.Equal(t, first, second, "the same inputs must mint the same id (§6.1 determinism)")

	later := syntheticEphemeralID(testSession, ToolExpand, args, ts+1)
	require.NotEqual(t, first, later, "a different timestamp must mint a different id")

	other := syntheticEphemeralID(testSession, ToolReRead, args, ts)
	require.NotEqual(t, first, other, "a different tool must mint a different id")

	otherSession := syntheticEphemeralID(testSession+"-b", ToolExpand, args, ts)
	require.NotEqual(t, first, otherSession, "a different session must mint a different id")

	// The 0x00 separators are what keep ("ab","c") and ("a","bc") distinct; without them the two
	// seeds below would be the same bytes and the two ids would collide.
	require.NotEqual(t,
		syntheticEphemeralID("ab", "c", args, ts),
		syntheticEphemeralID("a", "bc", args, ts),
		"the seed must be domain-separated by 0x00, not concatenated")
}

// TestRecordEliminatedIsNotTaggedEphemeral asserts the one exception in the eight.
//
// record_eliminated WRITES negative knowledge rather than returning retrieved content, and the
// acknowledgement of a durable fact has no business in the first-eviction tier: evicting it first
// would make the write look like a retrieval the model could simply repeat.
func TestRecordEliminatedIsNotTaggedEphemeral(t *testing.T) {
	f := newFixture(t)

	resp := f.callOK(t, ToolRecordEliminated, map[string]any{
		"target": "src/auth.ts", "approach": "widen pool timeout",
		"reason": "pgbouncer is in transaction mode, so the timeout is not the binding constraint",
	}, nil)

	require.False(t, resp.Ephemeral, "record_eliminated must not be born ephemeral (§8.7)")
	require.False(t, ephemeralTools[ToolRecordEliminated], "record_eliminated must be absent from ephemeralTools")
	require.NotContains(t, resp.Meta, metaToolUseID, "a write acknowledgement is not expandable content")

	got := serveEphemeralCall(t, f.Server, ToolRecordEliminated, map[string]any{
		"target": "src/pool.ts", "approach": "raise max connections",
		"reason": "the pool is not the bottleneck; the upstream connection limit is",
	})
	require.Equal(t, false, got.qompackMeta()[metaEphemeral],
		"a host must see _meta.%s.%s = false for record_eliminated", metaNamespace, metaEphemeral)

	for _, line := range ephemeralIndexLines(t, f.Root) {
		require.NotContains(t, line, mcpToolPrefix+ToolRecordEliminated,
			"no %s%s record may reach index/tool_use.jsonl", mcpToolPrefix, ToolRecordEliminated)
	}
}
