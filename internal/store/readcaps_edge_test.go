package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// Edge rows for the store's optional read capabilities (prompt recovery, provenance, the legacy prompt
// counter, the observation probe, the object-presence check) that their happy-path tests leave
// unexecuted: a closed store, deterministic tie-breaks, and each refusal (w16b-cover, C3.6).

// TestReadCapabilities_AClosedStoreAnswersNothing: every optional read capability refuses a closed
// store with the closed-store error, never an empty answer a caller could act on.
func TestReadCapabilities_AClosedStoreAnswersNothing(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "closed-store content")
	r, err := tp.Store.GetRoot(ctx, root)
	require.NoError(t, err)
	require.NotEmpty(t, r.Chunks)
	object := r.Chunks[0].Hash
	require.True(t, tp.Store.ObjectOnDisk(object), "guard: the object is on disk while the store is open")
	require.NoError(t, tp.Store.Close())

	_, _, err = tp.Store.PromptFrontier(ctx, "s", core.Hash{})
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.SessionPrompts(ctx, "s")
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.LatestPrompt(ctx, "s", 1)
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.EarliestPrompt(ctx, "s")
	require.ErrorIs(t, err, core.ErrDegraded)
	_, err = tp.Store.ContentOrigins(ctx, root)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.False(t, tp.Store.ObjectOnDisk(object), "a closed store answers false, as Has does")
}

// TestObjectOnDisk_IsTrueOnlyForAStoredObject: the presence check finds an object under either
// spelling and nothing else.
func TestObjectOnDisk_IsTrueOnlyForAStoredObject(t *testing.T) {
	tp := newTestStore(t)
	root := putRoot(t, tp, "object on disk")
	r, err := tp.Store.GetRoot(context.Background(), root)
	require.NoError(t, err)
	require.NotEmpty(t, r.Chunks)
	require.True(t, tp.Store.ObjectOnDisk(r.Chunks[0].Hash))
	require.False(t, tp.Store.ObjectOnDisk(core.HashBytes("test.absent", []byte("never stored"))))
}

// TestPromptFrontier_RefusesAnAmbiguousDigest: two prompt records of one session carrying the same
// delivery digest cannot both be the interrupted publication, so the frontier is unavailable rather
// than either one.
func TestPromptFrontier_RefusesAnAmbiguousDigest(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	digest, _ := ArgsDigest([]byte(`{"observation_id":"twice"}`))
	for _, id := range []core.ToolUseID{"prompt_s_1", "prompt_s_2"} {
		require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: id, Session: "s", Turn: 1, Tool: promptTool, ArgsDigest: digest,
		}))
	}
	_, _, err := tp.Store.PromptFrontier(ctx, "s", digest)
	require.ErrorIs(t, err, core.ErrDegraded)
}

// TestPromptOrdering_BreaksTiesDeterministically: SessionPrompts orders two prompts of one turn by id,
// and LatestPrompt prefers, between equal stamps, the higher turn and then the greater id — so neither
// answer depends on map iteration order.
func TestPromptOrdering_BreaksTiesDeterministically(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for _, rec := range []ToolUseRecord{
		{ID: "prompt_a_b", Session: "a", Turn: 1, TS: 100, Tool: promptTool},
		{ID: "prompt_a_a", Session: "a", Turn: 1, TS: 100, Tool: promptTool},
		{ID: "prompt_b_lo", Session: "b", Turn: 1, TS: 500, Tool: promptTool},
		{ID: "prompt_b_hi", Session: "b", Turn: 2, TS: 500, Tool: promptTool},
		{ID: "prompt_c_x", Session: "c", Turn: 2, TS: 500, Tool: promptTool},
	} {
		require.NoError(t, tp.Store.RecordToolUse(ctx, rec))
	}

	got, err := tp.Store.SessionPrompts(ctx, "a")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, []core.ToolUseID{"prompt_a_a", "prompt_a_b"}, []core.ToolUseID{got[0].ID, got[1].ID})

	latest, err := tp.Store.LatestPrompt(ctx, "a", 500)
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("prompt_c_x"), latest.ID, "equal stamp and turn: the greater id")
	latest, err = tp.Store.LatestPrompt(ctx, "c", 500)
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("prompt_b_hi"), latest.ID, "equal stamp: the higher turn")
}

// TestContentOrigins_AttributesAFileVersionAsARead: a root recorded as a file's version is an origin
// of that file through a Read, beside the root's own producer; an address nothing recorded has no
// origin at all.
func TestContentOrigins_AttributesAFileVersionAsARead(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "file version content")
	require.NoError(t, tp.Store.AppendFileVersion(ctx, "docs/notes.md", FileVersion{TS: 1, Root: root, Turn: 1, Bytes: 20}))

	got, err := tp.Store.ContentOrigins(ctx, root)
	require.NoError(t, err)
	require.Contains(t, got, ContentOrigin{Path: storeKey("docs/notes.md"), Tool: "Read"})
	require.Contains(t, got, ContentOrigin{Path: "src/x.ts", Tool: "FileRead"})

	_, err = tp.Store.ContentOrigins(ctx, core.HashBytes("test.absent", []byte("nothing recorded")))
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestLegacyPromptRecords_CountOnlyRecordsBeforeASessionsFirstBoundPrompt pins which prompt records
// may account for an earlier build's unlinked prompt sidecar: a session's records before its first
// observation-bound prompt, counted by (session, digest), and nothing from that turn on — those are
// the current build's. ClaimLegacyPrompt consumes one count per claim and claims nothing for a
// payload that names no prompt.
func TestLegacyPromptRecords_CountOnlyRecordsBeforeASessionsFirstBoundPrompt(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	payload := []byte(`{"prompt":"fix the flaky test"}`)
	d, ok := LegacyPromptDigest(payload)
	require.True(t, ok)
	bound := putRoot(t, tp, "the bound prompt")

	for _, rec := range []ToolUseRecord{
		{ID: "prompt_sess-obs_1", Session: "sess-obs", Turn: 1, TS: 1, Tool: promptTool, ArgsDigest: d},
		{ID: "prompt_sess-obs_4", Session: "sess-obs", Turn: 4, TS: 4, Tool: promptTool, ArgsDigest: d},
		{ID: "prompt_other_1", Session: "other", Turn: 1, TS: 1, Tool: promptTool, ArgsDigest: d},
		{ID: "prompt_other_2", Session: "other", Turn: 2, TS: 2, Tool: promptTool},
		{ID: "toolu_read", Session: "other", Turn: 3, TS: 3, Tool: "FileRead", ArgsDigest: d},
	} {
		require.NoError(t, tp.Store.RecordToolUse(ctx, rec))
	}
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "prompt_sess-obs_3", Session: "sess-obs", Turn: 3, TS: 3, Tool: promptTool,
		Root: bound, Path: "src/x.ts", Bytes: 5, Tokens: 2, Observation: obsID(t, 1),
	}))

	records := tp.Store.LegacyPromptRecords()
	require.Equal(t, map[LegacyPromptKey]int{
		{Session: "sess-obs", Digest: d}: 1,
		{Session: "other", Digest: d}:    1,
	}, records, "turn 4 follows the session's first bound prompt; a digestless or non-prompt record never counts")

	require.True(t, ClaimLegacyPrompt(records, "sess-obs", payload))
	require.False(t, ClaimLegacyPrompt(records, "sess-obs", payload), "one record accounts for one sidecar")
	require.False(t, ClaimLegacyPrompt(records, "other", []byte(`{}`)), "a payload naming no prompt claims nothing")
	require.False(t, ClaimLegacyPrompt(records, "other", []byte(`not json`)))
	require.True(t, ClaimLegacyPrompt(records, "other", payload))

	// The read-only open that `qompack fsck` uses offers the same counter and the audit capabilities.
	require.NoError(t, tp.Store.Close())
	ro, err := OpenReadOnly(tp.Root, tp.Cfg, Deps{Clock: tp.Clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ro.Close() })
	counter, ok := ro.(LegacyPromptCounter)
	require.True(t, ok)
	require.Equal(t, tp.Store.LegacyPromptRecords(), counter.LegacyPromptRecords())
	auditor, ok := ro.(interface {
		SnapshotPublication(context.Context) (PublicationSnapshot, error)
		AuditPublication(context.Context, PublicationScanCap) (PublicationAudit, error)
	})
	require.True(t, ok)
	snap, err := auditor.SnapshotPublication(ctx)
	require.NoError(t, err)
	audit, err := auditor.AuditPublication(ctx, PublicationScanCap{Snapshot: &snap})
	require.NoError(t, err)
	require.False(t, audit.Truncated)
}

// TestProbeObservation_FindsTheObservationPastNestedValues: a sidecar line that does not decode is
// still probed for its observation id, so a torn or foreign intent marks that observation unavailable
// rather than leaving a stale binding answerable. The probe steps over nested objects and arrays
// before "obs", and yields nothing for anything that is not an object with a canonical id.
func TestProbeObservation_FindsTheObservationPastNestedValues(t *testing.T) {
	obs := obsID(t, 7)
	got := probeObservation([]byte(`{"v":9,"rec":{"a":[1,{"b":[2]}],"c":{}},"sup":[],"obs":"` + string(obs) + `"}`))
	require.Equal(t, obs, got)

	for why, line := range map[string]string{
		"empty":                  ``,
		"not an object":          `[1]`,
		"empty object":           `{}`,
		"torn inside a nested":   `{"rec":{"a":[1,`,
		"torn after a value":     `{"v":1`,
		"torn before a value":    `{"v":`,
		"obs is not a string":    `{"obs":5}`,
		"obs is not a canonical": `{"obs":"nope"}`,
	} {
		require.Empty(t, probeObservation([]byte(line)), why)
	}
	_, ok := decodeObservationLine([]byte(`not json`))
	require.False(t, ok)
}

// TestFSStore_PublishesObservationsDurably: the built-in store declares the durable-publication
// capability the daemon relies on before it acknowledges an observation-bearing capture.
func TestFSStore_PublishesObservationsDurably(t *testing.T) {
	tp := newTestStore(t)
	var c interface{ PublishesObservationsDurably() bool } = tp.Store
	require.True(t, c.PublishesObservationsDurably())
}
