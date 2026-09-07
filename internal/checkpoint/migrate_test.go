package checkpoint_test

import (
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// TestMigrateV1IsIdentity requires Migrate to return a document already at SchemaVersion
// unchanged — same bytes, not merely equal JSON. It runs over every golden checkpoint plus the
// frozen contract fixture, so the goldens other seats add are covered as they land. It would
// fail against a Migrate that decoded and re-encoded the document on its way through (the
// re-encode reorders nothing here, but the contract fixture's inline arrays would be
// re-indented), which is exactly the "never decode the full document" rule §4 states.
func TestMigrateV1IsIdentity(t *testing.T) {
	docs := map[string][]byte{
		"contracts/checkpoint/want/0001.json": readCheckpointGolden(t, "0001.json"),
	}
	for name, raw := range readCheckpointsGoldens(t) {
		docs["checkpoints/"+name] = raw
	}

	for name, raw := range docs {
		t.Run(name, func(t *testing.T) {
			out, err := checkpoint.Migrate(raw)
			require.NoError(t, err)
			require.Equal(t, string(raw), string(out), "a current-version document must pass through byte-identically")
		})
	}
}

// TestMigrateRejectsFutureVersion requires a document written by a newer plugin to be rejected on
// its version field with core.ErrContract and a message naming the situation. This is the honest
// forward-compatibility boundary: unknown FIELDS at a known version are dropped with a Warn
// (TestUnmarshalDropsUnknownFields), but an unknown VERSION means the document's shape is simply
// not this build's to interpret. It would fail against a Migrate that fell through to a best-
// effort decode, or one that reported the generic missing-version error for the future case.
func TestMigrateRejectsFutureVersion(t *testing.T) {
	raw := fmt.Appendf(nil, `{"version":%d}`, checkpoint.SchemaVersion+1)
	_, err := checkpoint.Migrate(raw)
	require.ErrorIs(t, err, core.ErrContract)
	require.ErrorContains(t, err, "newer plugin")
}

// TestMigrateRejectsMissingVersion requires every document whose version cannot be read as a
// positive number — absent, null, non-numeric, below 1, or not an object at all — to be rejected
// with core.ErrContract before anything else looks at it, AND to say which of the three
// corruptions it was. It would fail against a Migrate that defaulted a missing version to 1 (Go's
// zero-value decode does exactly that unless the probe uses a *int, which is why versionProbe
// does) or one that let json decode errors escape unwrapped.
//
// The wantMsg column is the load-bearing half. All three rejections wrap core.ErrContract, so an
// ErrorIs assertion alone holds just as well against a Migrate that collapsed them into one
// message — which is a regression this package has already had once. A file that is not JSON, a
// JSON document with no version key, and a version key holding a nonsensical value are three
// different corruptions with three different causes, and the person reading the log is the one
// who has to tell them apart.
func TestMigrateRejectsMissingVersion(t *testing.T) {
	cases := map[string]struct {
		raw     string
		wantMsg string
	}{
		"empty object":       {raw: `{}`, wantMsg: "missing version"},
		"null version":       {raw: `{"version":null}`, wantMsg: "missing version"},
		"string version":     {raw: `{"version":"1"}`, wantMsg: "malformed JSON"},
		"zero version":       {raw: `{"version":0}`, wantMsg: "invalid version 0"},
		"negative version":   {raw: `{"version":-3}`, wantMsg: "invalid version -3"},
		"array not object":   {raw: `[]`, wantMsg: "malformed JSON"},
		"not json at all":    {raw: `not json`, wantMsg: "malformed JSON"},
		"fractional version": {raw: `{"version":1.5}`, wantMsg: "malformed JSON"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := checkpoint.Migrate([]byte(tc.raw))
			require.ErrorIs(t, err, core.ErrContract, "input %q", tc.raw)
			require.ErrorContains(t, err, tc.wantMsg, "input %q", tc.raw)
		})
	}
}
