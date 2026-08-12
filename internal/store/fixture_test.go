package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// goldenStoreDir is testdata/golden/contracts/store/want/, relative to this package's own
// directory (internal/store). These fixtures are frozen (Rule W-2): this test may never edit
// them, only prove store's types reproduce them.
const goldenStoreDir = "../../testdata/golden/contracts/store/want"

// readStoreGolden reads one frozen fixture file, failing the test with a clear message if it is
// missing rather than a bare os.ReadFile error, since a missing frozen fixture is a repository
// problem this test should surface loudly.
func readStoreGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenStoreDir, name))
	require.NoError(t, err, "frozen contract fixture missing: %s", name)
	require.NotEmpty(t, b)
	return b
}

// TestToolUseRecord_RoundTripsFrozenFixture asserts store.ToolUseRecord reproduces
// testdata/golden/contracts/store/want/tool_use_line.jsonl exactly (Rule W-2): unmarshal the
// frozen line, pin its decoded fields against the fixture's own literal values, marshal it back,
// and compare the two JSON documents for semantic equality.
//
// require.JSONEq, not a raw byte comparison, is used deliberately: it is what this repository's
// sibling fixture tests (e.g. internal/dag's) use for exactly this reason, and it is robust to
// encoding/json's own whole-number-float rendering quirk even though this particular fixture has
// no float fields.
func TestToolUseRecord_RoundTripsFrozenFixture(t *testing.T) {
	golden := readStoreGolden(t, "tool_use_line.jsonl")

	var rec store.ToolUseRecord
	require.NoError(t, json.Unmarshal(golden, &rec))

	require.Equal(t, core.ToolUseID("toolu_01A2B3C4D5E6F7G8H9J0K1L2"), rec.ID)
	require.Equal(t, core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A"), rec.Session)
	require.Equal(t, core.TurnIndex(61), rec.Turn)
	require.Equal(t, core.UnixMilli(1767225480000), rec.TS)
	require.Equal(t, "FileRead", rec.Tool)
	require.Equal(t, "sha256:2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c", rec.ArgsDigest.String())
	require.Equal(t, "src/auth.ts", rec.ArgsPreview)
	require.Equal(t, "sha256:0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c", rec.Root.String())
	require.Equal(t, "src/auth.ts", rec.Path)
	require.Equal(t, int64(2457), rec.Bytes)
	require.Equal(t, core.Tokens(683), rec.Tokens)
	require.Equal(t, uint16(4), rec.Signature.Perms)
	require.Equal(t, []uint64{184467440737095516, 922337203685477580, 1229782938247303441, 1537228672809129301}, rec.Signature.Mins)
	require.Equal(t, store.StatusOK, rec.Status)
	require.Equal(t, core.ToolUseID(""), rec.SupersededBy)
	require.False(t, rec.Ephemeral)
	require.Equal(t, "", rec.Subagent)

	remarshaled, err := json.Marshal(rec)
	require.NoError(t, err)
	require.JSONEq(t, string(golden), string(remarshaled))

	var rec2 store.ToolUseRecord
	require.NoError(t, json.Unmarshal(remarshaled, &rec2))
	require.Equal(t, rec, rec2)
}

// TestRoot_RoundTripsFrozenFixture is TestToolUseRecord_RoundTripsFrozenFixture's sibling for
// testdata/golden/contracts/store/want/roots_line.jsonl.
func TestRoot_RoundTripsFrozenFixture(t *testing.T) {
	golden := readStoreGolden(t, "roots_line.jsonl")

	var root store.Root
	require.NoError(t, json.Unmarshal(golden, &root))

	require.Equal(t, "sha256:0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c", root.Hash.String())
	require.Len(t, root.Chunks, 2)
	require.Equal(t, "sha256:a1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4", root.Chunks[0].Hash.String())
	require.Equal(t, 1204, root.Chunks[0].Len)
	require.Equal(t, "sha256:b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1", root.Chunks[1].Hash.String())
	require.Equal(t, 1253, root.Chunks[1].Len)
	require.Equal(t, int64(2457), root.CanonBytes)
	require.Equal(t, int64(2610), root.RawBytes)
	require.Equal(t, core.Tokens(683), root.Tokens)

	remarshaled, err := json.Marshal(root)
	require.NoError(t, err)
	require.JSONEq(t, string(golden), string(remarshaled))

	var root2 store.Root
	require.NoError(t, json.Unmarshal(remarshaled, &root2))
	require.Equal(t, root, root2)
}

// TestSegment_RoundTripsFrozenFixture is TestToolUseRecord_RoundTripsFrozenFixture's sibling for
// testdata/golden/contracts/store/want/segments_line.jsonl. require.JSONEq is load-bearing here,
// not just precautionary: Features carries "todo_transition":0.0, and encoding/json renders a
// whole-number float64 without a decimal point (json.Marshal(0.0) produces "0", not "0.0"), so a
// byte-exact comparison would fail on formatting alone even though the decoded value is
// unchanged — the same trap a sibling agent hit on dag's "weight":1.0.
func TestSegment_RoundTripsFrozenFixture(t *testing.T) {
	golden := readStoreGolden(t, "segments_line.jsonl")

	var seg store.Segment
	require.NoError(t, json.Unmarshal(golden, &seg))

	require.Equal(t, core.SegmentID(12), seg.ID)
	require.Equal(t, core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A"), seg.Session)
	require.Equal(t, core.TurnIndex(48), seg.StartTurn)
	require.Equal(t, core.TurnIndex(61), seg.EndTurn)
	require.Equal(t, core.UnixMilli(1767224100000), seg.StartTS)
	require.Equal(t, core.UnixMilli(1767225480000), seg.EndTS)
	require.InDelta(t, 0.72, seg.Features["path_jaccard"], 0)
	require.InDelta(t, 0.18, seg.Features["tool_shift"], 0)
	require.InDelta(t, 0.64, seg.Features["lexical_cohesion"], 0)
	require.InDelta(t, 12.5, seg.Features["gap_seconds"], 0)
	require.InDelta(t, 0.0, seg.Features["todo_transition"], 0)
	require.Equal(t, core.Tokens(18420), seg.Tokens)
	require.True(t, seg.EncodedOnce)
	require.Equal(t, core.CheckpointSeq(1), seg.CheckpointSeq)
	require.True(t, seg.Closed)
	require.Equal(t, "", seg.BloomRef)

	remarshaled, err := json.Marshal(seg)
	require.NoError(t, err)
	require.JSONEq(t, string(golden), string(remarshaled))

	var seg2 store.Segment
	require.NoError(t, json.Unmarshal(remarshaled, &seg2))
	require.Equal(t, seg, seg2)
}
