package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// TestMaintenance_RestoreAcceptsAToolRefAGCTombstoneAccountsFor is F-C49-2: the MCP server's own
// ephemeral record (a recall answer, stored born-ephemeral and indexed under a qompack-mcp: id) is
// collected by the daemon's GC, which writes a gc tombstone for its root while index/tool_use.jsonl
// keeps the record. fsck reads that reference as "accounted for by a gc tombstone" — an ok row —
// but the restore's reader proof failed the whole restore on it ("restored tool reference root
// ... does not resolve"), so every backup taken after a session that called an MCP tool and then
// idled could not be restored. The proof must judge the reference the way fsck does.
func TestMaintenance_RestoreAcceptsAToolRefAGCTombstoneAccountsFor(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "payload a — must survive a backup and restore\n")

	eph := gcSeedEphemeral(t, tp, "", "recall answer the MCP server stored for itself\n")
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: core.ToolUseID("qompack-mcp:0123456789ab"), Turn: 0, TS: 1, Tool: "mcp__qompack__recall",
		Root: eph.Hash, Ephemeral: true, Status: StatusOK,
	}))
	_, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	_, err = tp.Store.GetRoot(ctx, eph.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "fixture: the GC collected the ephemeral root")
	require.NotEmpty(t, tp.indexLinesContaining(t, rootsFile, `"op":"gc"`), "fixture: and tombstoned it")
	// Content the session kept, stored after the pass so the forced collection cannot take it.
	seedRoot(t, tp, "src/a.ts", "payload a — must survive a backup and restore\n")

	x := newMaint(t, tp, leaseOK)
	_, err = x.TakeBackup(ctx, "after-gc")
	require.NoError(t, err)
	_, err = x.VerifyBackup("after-gc")
	require.NoError(t, err)

	proof, err := x.Restore(ctx, "after-gc", filepath.Join(t.TempDir(), "restored"))
	require.NoError(t, err, "a tool reference a gc tombstone accounts for is not a restore failure")
	require.Equal(t, 1, proof.ToolRefsTombstoned, "the collected reference is counted, not hidden")
	require.Positive(t, proof.ContentRootsProven)
}

// TestMaintenance_RestoreStillRefusesAToolRefNothingAccountsFor keeps the proof's teeth: a tool
// reference whose root is simply gone — no tombstone retired it — still fails the restore.
func TestMaintenance_RestoreStillRefusesAToolRefNothingAccountsFor(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "payload a\n")
	missing := core.HashBytes(core.DomainChunk, []byte("a root no roots.jsonl line ever recorded"))
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: core.ToolUseID("toolu_dangling"), Session: core.SessionID("s1"), Turn: 1, TS: 1, Tool: "Read",
		Root: missing, Status: StatusOK,
	}))
	require.NoError(t, tp.Store.Flush(ctx))

	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(ctx, "dangling")
	require.NoError(t, err)
	_, err = x.Restore(ctx, "dangling", filepath.Join(t.TempDir(), "restored"))
	require.ErrorContains(t, err, "does not resolve")
}
