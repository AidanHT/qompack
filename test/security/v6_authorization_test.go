package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// TestV6_ArchivedReadRetainsItsAuthorizationBoundary covers the legacy stored
// shape produced before capture-scope refusal: Read bytes with a dropped path.
// The separate posture test verifies that new packaged captures refuse it.
func TestV6_ArchivedReadRetainsItsAuthorizationBoundary(t *testing.T) {
	b := assembledBundle(t)
	base := tempBase(t)
	p := newProjectAt(t, base, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	const id = "toolu_v6_outside_read"
	const marker = "V6-OUTSIDE-READ-CONTENT-847ea021"
	const sess = core.SessionID("sess-v6-outside-read")
	s := openStoreAt(t, p.Root)
	legacy, err := s.PutBytes(context.Background(), []byte(marker+"\n"), store.PutOptions{Tool: "Read"})
	require.NoError(t, err)
	require.NoError(t, s.RecordToolUse(context.Background(), store.ToolUseRecord{
		ID: core.ToolUseID(id), Session: sess, Tool: "Read", Root: legacy.Root.Hash, Bytes: legacy.Root.RawBytes,
	}))
	require.NoError(t, s.Close())

	child := startMCP(t, b.Bin, p)
	t.Cleanup(func() { child.stop(t) })
	child.handshake(t)
	res := child.call(t, mcp.ToolExpand, map[string]any{"tool_use_id": id, "full": true})
	child.finish(t)
	v6RequireDenied(t, "v6_outside_read_by_id", res, marker)
}

// TestV6_HashAddressesDoNotBypassPathAuthorization captures an in-project file
// through the hook, then replaces its parent with a link outside the project.
// The ID refusal is the control. Hash addresses must enforce the same boundary.
func TestV6_HashAddressesDoNotBypassPathAuthorization(t *testing.T) {
	b := assembledBundle(t)
	base := tempBase(t)
	p := newProjectAt(t, base, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })
	const id = "toolu_v6_hash_escape"
	const marker = "V6-CAPTURED-PATH-CONTENT-541dc897"
	const sess = core.SessionID("sess-v6-hash-escape")
	mechanism, skip := linkMechanism(true)
	if skip != "" {
		rec := newRecord(t, "v6_hash_path_link_unavailable")
		rec.Capability = CapArchiveTrust
		skipRecorded(t, rec, skip)
	}
	writeProjectFile(t, p, deniedPath, marker+"\n")
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess))
	require.True(t, waitDaemonUp(t, p.Root))
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, sess, id, deniedPath, marker+"\n"))
	requireIndexed(t, p.Root, id)
	shutdownIfReachable(t, p.Root)

	s := openStoreAt(t, p.Root)
	rec, err := s.ToolUse(context.Background(), core.ToolUseID(id))
	require.NoError(t, err)
	root, err := s.GetRoot(context.Background(), rec.Root)
	require.NoError(t, err)
	require.NotEmpty(t, root.Chunks)
	require.NoError(t, s.Close())

	child := startMCP(t, b.Bin, p)
	t.Cleanup(func() { child.stop(t) })
	child.handshake(t)
	control := child.call(t, mcp.ToolExpand, map[string]any{"tool_use_id": id, "full": true})
	require.Contains(t, control.Text, marker, "the original authorized address must resolve before the path changes")
	src := filepath.Join(p.Root, "src")
	stash := filepath.Join(base, "captured-src")
	out := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(paths.Long(out), 0o700))
	require.NoError(t, os.Rename(paths.Long(src), paths.Long(stash)))
	require.NoError(t, makeLink(src, out, true), mechanism)
	t.Cleanup(func() { _ = os.Remove(paths.Long(src)) })
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"tool_use_id", map[string]any{"tool_use_id": id, "full": true}},
		{"root_hash", map[string]any{"hash": root.Hash.String(), "full": true}},
		{"chunk_hash", map[string]any{"hash": root.Chunks[0].Hash.String(), "full": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := child.call(t, mcp.ToolExpand, tc.args)
			v6RequireDenied(t, "v6_escape_"+tc.name, res, marker)
		})
	}
	child.finish(t)
}

// Persist the failed envelope classification before failing the assertion, so a
// Go failure never discards the evidence needed to adjudicate the release gate.
func v6RequireDenied(t *testing.T, name string, res callResult, forbidden string) {
	t.Helper()
	rec := newRecord(t, name)
	rec.Capability = CapArchiveTrust
	envelope := describeEnvelope(res)
	exposed := strings.Contains(res.Text, forbidden)
	rec.Detail = fmt.Sprintf("envelope=%s; synthetic forbidden marker exposed=%t", envelope, exposed)
	rec.Outcome = OutcomeVerified
	rec.Reason = "the packaged MCP server denied the address without materializing forbidden content"
	if envelope != "denied(found=false)" || exposed {
		rec.Outcome = OutcomeFailed
		rec.Reason = "the archived address did not retain its path authorization boundary; owner internal/mcp + internal/observer"
	}
	writeRecord(t, rec)
	require.False(t, exposed, "%s materialized content outside the authorized path scope", name)
	require.Equal(t, "denied(found=false)", envelope, "%s must distinguish refusal from absence", name)
}
