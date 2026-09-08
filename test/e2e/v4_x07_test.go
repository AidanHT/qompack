// V4 §4.7 — a retrieval result is born ephemeral, that metadata survives a store reopen, and the
// scheduler's LOCAL drop-class ordering ranks it first.
//
// RETIRED CLAUSE (reconciliation map §4.7, rows V4-SP12-11, V4-SP13-14, V4-SP13-15, NC-8): the
// scenario NAME says "eviction". No native-history eviction or deletion may be inferred from
// anything here, and nothing below asserts one. "Ranks first for eviction" means exactly one thing
// on this tree — scheduler.DropClassOf puts an ephemeral record at rank 0 of the LOCAL droppable
// ordering, and the MCP result carries a representation hint. The guard at the end asserts the
// stronger reading is absent: the record is still readable, and the store still holds its bytes.
package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x7v4Session is this row's session identity.
const x7v4Session = core.SessionID("sess-e2e-v4-x07")

// x7v4Seed is the durable capture `expand` re-materializes. It is NOT born ephemeral: it is the
// control half of the ordering assertion.
const (
	x7v4SeedID   = "toolu_v4_x07_seed"
	x7v4SeedPath = "src/session.ts"
)

// x7v4OpenStore opens the real store over root.
func x7v4OpenStore(t *testing.T, p *testutil.Project) store.Store {
	t.Helper()
	st, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
	require.NoError(t, err)
	return st
}

// TestV4_EphemeralRetrievalResultsRankFirstForEviction is V4-VERIFY §4.7.
//
// The negative control is the last arm: the same record with its ephemeral flag cleared must move
// in the ordering. Without it, "ephemeral ranks first" would be satisfied by a DropClassOf that
// returns the same class for everything.
func TestV4_EphemeralRetrievalResultsRankFirstForEviction(t *testing.T) {
	ctx := context.Background()
	p := v4Project(t)

	// ── A real store, a real capture, and the real MCP retrieval surface over both ───────────────
	st := x7v4OpenStore(t, p)
	body := "export function renewSession(): void {\n  // " + strings.Repeat("renew ", 400) + "\n}\n"
	res, err := st.PutBytes(ctx, []byte(body), store.PutOptions{Tool: "FileRead", Path: x7v4SeedPath})
	require.NoError(t, err)
	require.NoError(t, st.RecordToolUse(ctx, store.ToolUseRecord{
		ID: x7v4SeedID, Session: x7v4Session, Tool: "FileRead", Path: x7v4SeedPath,
		Root: res.Root.Hash, Bytes: int64(len(body)), Status: store.StatusOK,
	}))

	srv := v4Server(t, mcp.ToolDeps{
		Store: st, Cfg: p.Cfg, ProjectRoot: p.Root, DisableWhy: true,
	})

	var expanded struct {
		Found bool   `json:"found"`
		Hash  string `json:"hash"`
	}
	out := v4Call(t, srv, x7v4Session, mcp.ToolExpand,
		map[string]any{"tool_use_id": x7v4SeedID}, &expanded)
	require.True(t, expanded.Found, "expand must re-materialize the seeded capture")
	require.True(t, out.Ephemeral,
		"§8.7: a retrieval result is born ephemeral, and the response says so through "+
			"_meta.%s.ephemeral", mcp.ServerName)

	// ── The metadata survives a store REOPEN: it is on disk, not in a handler's memory ───────────
	require.NoError(t, st.Close())
	reopened := x7v4OpenStore(t, p)
	t.Cleanup(func() { _ = reopened.Close() })

	var ephemeralIDs []core.ToolUseID
	recs, err := reopened.ToolUsesByPath(ctx, x7v4SeedPath, 50)
	require.NoError(t, err, "the reopened store must list the records filed against this path")
	require.NotEmpty(t, recs)
	for _, rec := range recs {
		if rec.Ephemeral {
			ephemeralIDs = append(ephemeralIDs, rec.ID)
			require.Contains(t, rec.Tool, mcp.ToolExpand,
				"an ephemeral-at-birth record must be filed under the retrieval tool that made it: %+v", rec)
		}
	}
	require.NotEmpty(t, ephemeralIDs,
		"the ephemeral record the retrieval wrote must still be ephemeral after a reopen; records=%+v", recs)

	seed, err := reopened.ToolUse(ctx, x7v4SeedID)
	require.NoError(t, err)
	require.False(t, seed.Ephemeral, "the DURABLE capture must not have become ephemeral")

	// ── The LOCAL drop-class ordering ranks the ephemeral records first ──────────────────────────
	ephRec, err := reopened.ToolUse(ctx, ephemeralIDs[0])
	require.NoError(t, err)

	ephClass := scheduler.DropClassOf(ephRec.Tool, ephRec.Ephemeral, false)
	seedClass := scheduler.DropClassOf(seed.Tool, seed.Ephemeral, false)
	require.Equal(t, scheduler.DropEphemeral, ephClass,
		"the retrieval-born record must land in the ephemeral class")
	require.Greater(t, ephClass.EvictionRank(), seedClass.EvictionRank(),
		"EvictionRank ASCENDS with eviction priority (ephemeral 3 > superseded 2 > ordinary 1 > "+
			"none 0), so ranking first means the HIGHER rank: ephemeral=%d durable=%d",
		ephClass.EvictionRank(), seedClass.EvictionRank())
	require.NotEqual(t, scheduler.DropNone, ephClass,
		"an ephemeral record must fall into a real droppable class, not DropNone")

	// ── NEGATIVE CONTROL: clear the ephemeral flag and the rank must move ────────────────────────
	cleared := scheduler.DropClassOf(ephRec.Tool, false, false)
	require.NotEqual(t, ephClass, cleared,
		"NEGATIVE CONTROL: the SAME record with ephemeral cleared must fall into a different drop "+
			"class — otherwise the flag is not what the ordering reads")
	require.Less(t, cleared.EvictionRank(), ephClass.EvictionRank(),
		"clearing the flag must move the record LATER in the eviction order (a lower rank), which "+
			"is what proves the flag — and not the tool name — is what put it first")

	// ── The retired clause, asserted as an absence ───────────────────────────────────────────────
	//
	// Ephemerality is a local representation hint. It deletes nothing, and it makes no claim about
	// what the host still holds in native context.
	rc, err := reopened.Open(ctx, res.Root.Hash)
	require.NoError(t, err, "an ephemeral marking must not have removed the object it points at")
	require.NotEmpty(t, x1ReadAll(t, rc),
		"the store still serves the bytes: 'ranks first' is an ORDERING, never a deletion")
	stillThere, err := reopened.ToolUse(ctx, ephemeralIDs[0])
	require.NoError(t, err,
		"the ephemeral record itself is still addressable — nothing evicted it from the archive")
	require.True(t, stillThere.Ephemeral)
}
