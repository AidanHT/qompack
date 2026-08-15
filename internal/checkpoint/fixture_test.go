package checkpoint_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// goldenCheckpointDir is testdata/golden/contracts/checkpoint/want/, relative to this package's
// own directory (internal/checkpoint). These fixtures are frozen (Rule W-2): this test may never
// edit them, only prove checkpoint's types reproduce them.
const goldenCheckpointDir = "../../testdata/golden/contracts/checkpoint/want"

// readCheckpointGolden reads one frozen fixture file, failing with a clear message if it is
// missing rather than a bare os.ReadFile error, since a missing frozen fixture is a repository
// problem this test should surface loudly.
func readCheckpointGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenCheckpointDir, name))
	require.NoError(t, err, "frozen contract fixture missing: %s", name)
	require.NotEmpty(t, b)
	return b
}

// TestCheckpoint_DecodesFrozenFixture asserts checkpoint.Checkpoint decodes
// testdata/golden/contracts/checkpoint/want/0001.json — the Qompack.md §8.5 artifact with every
// tier populated — into exactly the values the fixture spells, field for field. This is the
// assertion that catches a renamed or retyped json tag, which would otherwise only surface once
// SP-10 wrote a real artifact nothing could read back.
func TestCheckpoint_DecodesFrozenFixture(t *testing.T) {
	golden := readCheckpointGolden(t, "0001.json")

	var c checkpoint.Checkpoint
	require.NoError(t, json.Unmarshal(golden, &c))

	require.Equal(t, checkpoint.SchemaVersion, c.Version)
	require.Equal(t, core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A"), c.Session)
	require.Equal(t, core.CheckpointSeq(1), c.Seq)
	require.Equal(t, "2026-01-01T00:12:30.000Z", c.Created)
	require.Equal(t, "", c.Parent, "seq 1 has no parent")
	require.Equal(t, []core.SegmentID{12, 13, 14}, c.EncodedSegments)

	// ── Tier 1 ──
	require.Len(t, c.Invariants, 2)
	require.Equal(t, "inv_7c1a9e2f4b60", c.Invariants[0].ID)
	require.Equal(t, "user", c.Invariants[0].Source)
	require.Equal(t, core.UnixMilli(1767225120000), c.Invariants[0].Pinned)
	require.Equal(t, "agent", c.Invariants[1].Source)

	require.Contains(t, c.UserIntent.Original, "POST /api/session/refresh")
	require.Len(t, c.UserIntent.Evolution, 2)

	require.Len(t, c.Eliminated, 1)
	elim := c.Eliminated[0]
	require.Equal(t, "elim_3f9b2c7d1a48", elim.ID)
	require.Equal(t, "src/auth.ts:refreshToken", elim.Target)
	require.Equal(t, "widen pool timeout", elim.Approach)
	require.Equal(t, "src/auth.ts", elim.Desc.NormalizedPath)
	require.Equal(t, "refreshToken", elim.Desc.Symbol)
	require.Equal(t, "widen-timeout", elim.Desc.ApproachClass)
	require.Len(t, elim.DependsOn, 2)
	require.Equal(t, "docker-compose.yml", elim.DependsOn[0].Path)
	require.Equal(t, negknow.ScopeProject, elim.Scope)
	require.Equal(t, negknow.StatusActive, elim.Status)
	require.Equal(t, negknow.SourceSlashCommand, elim.Source)

	// ── Tier 2 ──
	require.Len(t, c.Decisions, 1)
	require.Equal(t, core.DecisionID("dec_a3f2c9e14b70"), c.Decisions[0].ID)
	require.Len(t, c.Decisions[0].AlternativesRejected, 2)
	require.Equal(t,
		"sha256:c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9",
		c.Decisions[0].Evidence.String())
	require.Equal(t, core.TurnIndex(61), c.Decisions[0].Turn)

	require.Len(t, c.OpenQuestions, 2)
	require.Contains(t, c.CurrentWork.Goal, "pool exhaustion")
	require.NotEmpty(t, c.CurrentWork.NextStep)
	require.Nil(t, c.CurrentWork.BlockedOn, `"blocked_on": null must decode to a nil *string, not ""`)

	// ── Tier 3 ──
	require.Len(t, c.Pointers.Files, 2)
	require.Equal(t, "src/auth.ts", c.Pointers.Files[0].Path)
	require.Equal(t,
		"sha256:0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c",
		c.Pointers.Files[0].Hash.String())
	require.NotEmpty(t, c.Pointers.Files[0].Why)
	require.Len(t, c.Pointers.Tools, 1)
	require.Equal(t, core.ToolUseID("toolu_01A2B3C4D5E6F7G8H9J0K1L2"), c.Pointers.Tools[0].ToolUseID)
	require.NotEmpty(t, c.Pointers.Tools[0].Summary)
	require.NotEmpty(t, c.Narrative)

	// ── Metadata ──
	require.Equal(t, map[string]string{"tried": "tried.bloom", "touch": "touch.cms"}, c.SketchRefs)
	require.Len(t, c.Dropped, 2)
	require.Equal(t, "path_rule", c.Dropped[0].Kind)
	require.Equal(t, "api-conventions.md", c.Dropped[0].ID)
	require.NotEmpty(t, c.Dropped[0].Detail)
	require.Equal(t, 148230, c.Cache.PChosen)
	require.Equal(t, 18770, c.Cache.RewriteTokens)
	require.Equal(t, "warm", c.Cache.TTLState)
}

// TestCheckpoint_RoundTripsFrozenFixture asserts the frozen §8.5 artifact survives a
// decode/encode/decode cycle with no divergence at all.
//
// The fixture originally spelled its absent parent as an explicit "parent": "", which cannot
// survive a byte-exact re-marshal because 00-ARCHITECTURE.md §5.14 declares Parent as
// `json:"parent,omitempty"`. §8.5 shows no parent key, and §5 is normative, so the fixture was
// corrected before it was ever consumed rather than frozen against the interface that has to
// reproduce it. That ordering is the whole point of Rule W-2: a fixture the real implementation
// cannot reproduce is a verification failure, and one shipped knowing it cannot be reproduced is
// a trap set for its owner.
func TestCheckpoint_RoundTripsFrozenFixture(t *testing.T) {
	golden := readCheckpointGolden(t, "0001.json")

	var c checkpoint.Checkpoint
	require.NoError(t, json.Unmarshal(golden, &c))

	remarshaled, err := json.Marshal(c)
	require.NoError(t, err)

	require.JSONEq(t, string(golden), string(remarshaled),
		"the frozen artifact must re-marshal identically, with no exceptions")

	var goldenFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(golden, &goldenFields))
	require.NotContains(t, goldenFields, "parent",
		"§5.14 declares parent omitempty, so a root checkpoint's artifact must not carry the key")

	var c2 checkpoint.Checkpoint
	require.NoError(t, json.Unmarshal(remarshaled, &c2))
	require.Equal(t, c, c2, "decode(encode(decode(golden))) must be value-identical")
}

// TestCheckpointGolden_ContainsNoCodeBlocks is §13 invariant 5 asserted against the frozen
// artifact itself: a checkpoint carries pointers and reasons, never code (Qompack.md §4.4). A
// fenced block appearing in a hand-authored fixture would legitimize one appearing in a generated
// one, so the fixture is held to the same rule the generator is.
func TestCheckpointGolden_ContainsNoCodeBlocks(t *testing.T) {
	golden := string(readCheckpointGolden(t, "0001.json"))
	require.NotContains(t, golden, "```", "§13 invariant 5: no fenced code blocks in a checkpoint")
	require.NotContains(t, golden, "\\u0060\\u0060\\u0060",
		"§13 invariant 5: no escaped fenced code blocks in a checkpoint either")
}

// TestCheckpointGolden_HasNoTrailingWhitespaceDrift is a cheap guard that the fixture is a single
// JSON document with a trailing newline, the shape every other frozen fixture in this repository
// uses. It exists so a future regeneration cannot quietly change the file's framing.
func TestCheckpointGolden_HasNoTrailingWhitespaceDrift(t *testing.T) {
	golden := string(readCheckpointGolden(t, "0001.json"))
	require.True(t, strings.HasSuffix(golden, "}\n"), "fixture must end with a closed object and one newline")
	require.False(t, strings.HasSuffix(golden, "}\n\n"), "fixture must not end with a blank line")
}
