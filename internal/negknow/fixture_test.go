package negknow_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// goldenNegknowDir is testdata/golden/contracts/negknow/want/, relative to this package's own
// directory (internal/negknow). Frozen (Rule W-2): this test may never edit it, only prove
// negknow.Record and negknow.Descriptor reproduce it.
const goldenNegknowDir = "../../testdata/golden/contracts/negknow/want"

// goldenCheckpointDir is testdata/golden/contracts/checkpoint/want/. negknow does not own this
// fixture (checkpoint/SP-10 does), but the task brief requires negknow.Record's json tags to
// match its eliminated[] entries, so this package reads it read-only, exactly as it reads its own
// fixtures — it is never written here.
const goldenCheckpointDir = "../../testdata/golden/contracts/checkpoint/want"

func readNegknowGolden(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err, "frozen contract fixture missing: %s/%s", dir, name)
	require.NotEmpty(t, b)
	return b
}

// TestRecord_RoundTripsFrozenEliminationFixture asserts negknow.Record reproduces
// testdata/golden/contracts/negknow/want/elimination_record.jsonl exactly (Rule W-2): unmarshal
// the frozen line, pin its decoded fields against the fixture's own literal values, marshal it
// back, and compare the two JSON documents for semantic equality (require.JSONEq, not a raw byte
// comparison, for the same whole-number-float-rendering reason internal/dag's own fixture test
// documents — this fixture happens to have no float fields, but the rest of this suite does, so
// JSONEq is used uniformly rather than switching comparison strategy fixture by fixture).
func TestRecord_RoundTripsFrozenEliminationFixture(t *testing.T) {
	golden := readNegknowGolden(t, goldenNegknowDir, "elimination_record.jsonl")

	var rec negknow.Record
	require.NoError(t, json.Unmarshal(golden, &rec))

	require.Equal(t, "elim_3f9b2c7d1a48", rec.ID)
	require.Equal(t, core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A"), rec.Session)
	require.Equal(t, core.UnixMilli(1767225480000), rec.TS)
	require.Equal(t, "src/auth.ts:refreshToken", rec.Target)
	require.Equal(t, "widen pool timeout", rec.Approach)
	require.Equal(t, "pgbouncer 1.18 ignores statement_timeout in transaction pooling mode, so the widened timeout never takes effect", rec.Reason)

	require.Equal(t, "src/auth.ts", rec.Desc.NormalizedPath)
	require.Equal(t, "refreshToken", rec.Desc.Symbol)
	require.Equal(t, "widen-timeout", rec.Desc.ApproachClass)
	require.Equal(t, "sha256:9f2c4a7e1b8d3506e9a1c4f7b2d508e3a6c9f1b4d7e0a3c6f9b2d5e8a1c4f7b0", rec.Desc.ReasonHash.String())

	require.Equal(t, "sha256:4d7a0c3f6b9e2158a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3", rec.Evidence.String())
	require.Len(t, rec.DependsOn, 1)
	require.Equal(t, "docker-compose.yml", rec.DependsOn[0].Path)
	require.Equal(t, "sha256:1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d", rec.DependsOn[0].Hash.String())

	require.Equal(t, negknow.ScopeProject, rec.Scope)
	require.Equal(t, negknow.StatusActive, rec.Status)
	require.Equal(t, core.UnixMilli(0), rec.StaleSince, "active record must not carry a stale_since")
	require.Empty(t, rec.StaleBecause, "active record must not carry stale_because")
	require.Equal(t, negknow.SourceSlashCommand, rec.Source)

	remarshaled, err := json.Marshal(rec)
	require.NoError(t, err)
	require.JSONEq(t, string(golden), string(remarshaled))

	var rec2 negknow.Record
	require.NoError(t, json.Unmarshal(remarshaled, &rec2))
	require.Equal(t, rec, rec2)
}

// TestRecord_MatchesCheckpointFixtureEliminatedEntry asserts negknow.Record's json tags match
// testdata/golden/contracts/checkpoint/want/0001.json's eliminated[0] entry exactly (Rule W-2,
// per the task brief): decode ONLY the "eliminated" key (encoding/json ignores every other key it
// is not told about, so this does not require importing the checkpoint package this package may
// not depend on), extract eliminated[0] as raw JSON, and prove negknow.Record round-trips it.
func TestRecord_MatchesCheckpointFixtureEliminatedEntry(t *testing.T) {
	golden := readNegknowGolden(t, goldenCheckpointDir, "0001.json")

	var wrapper struct {
		Eliminated []json.RawMessage `json:"eliminated"`
	}
	require.NoError(t, json.Unmarshal(golden, &wrapper))
	require.Len(t, wrapper.Eliminated, 1, "fixture sanity: 0001.json must carry exactly one eliminated[] entry")

	var rec negknow.Record
	require.NoError(t, json.Unmarshal(wrapper.Eliminated[0], &rec))

	require.Equal(t, "elim_3f9b2c7d1a48", rec.ID)
	require.Equal(t, "src/auth.ts", rec.Desc.NormalizedPath)
	require.Equal(t, "refreshToken", rec.Desc.Symbol)
	require.Equal(t, "widen-timeout", rec.Desc.ApproachClass)
	require.Len(t, rec.DependsOn, 2, "the checkpoint fixture's depends_on has two entries, unlike the dedicated elimination_record.jsonl fixture's one")
	require.Equal(t, "docker-compose.yml", rec.DependsOn[0].Path)
	require.Equal(t, "package-lock.json", rec.DependsOn[1].Path)
	require.Equal(t, negknow.ScopeProject, rec.Scope)
	require.Equal(t, negknow.StatusActive, rec.Status)
	require.Equal(t, negknow.SourceSlashCommand, rec.Source)

	remarshaled, err := json.Marshal(rec)
	require.NoError(t, err)
	require.JSONEq(t, string(wrapper.Eliminated[0]), string(remarshaled))
}
