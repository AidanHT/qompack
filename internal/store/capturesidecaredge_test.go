package store

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The capture sidecar's identity rule, its forward compatibility, and the one case where it makes
// no retention claim.

// TestCaptureSidecarPath_RefusesAnObservationIdThatIsNotADigest.
//
// The id becomes a path element — a shard directory and a filename — so anything that is not a
// digest is refused before it reaches the filesystem: a short id, an uppercase one (which would
// collide with its own lowercase spelling on a case-insensitive filesystem), and one that is the
// right length but not hex at all.
func TestCaptureSidecarPath_RefusesAnObservationIdThatIsNotADigest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	digest := strings.Repeat("ab", captureSidecarIDHexLen/2)

	got, err := CaptureSidecarPath(root, core.ObservationID(digest))
	require.NoError(t, err)
	require.Contains(t, got, digest, "a digest id names its own file")

	got, err = CaptureSidecarPath(root, core.ObservationID("sha256:"+digest))
	require.NoError(t, err, "the canonical prefix is accepted and stripped")
	require.NotContains(t, got, "sha256:")

	for _, c := range []struct{ name, id string }{
		{"too short", "abcd"},
		{"uppercase", strings.ToUpper(digest)},
		{"the right length but not hex", strings.Repeat("zz", captureSidecarIDHexLen/2)},
		{"empty", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := CaptureSidecarPath(root, core.ObservationID(c.id))
			require.ErrorIs(t, err, core.ErrContract)
			require.ErrorContains(t, err, "not a digest")
		})
	}
}

// TestCaptureSidecar_PreservesAFutureWritersFields.
//
// A sidecar written by a newer build and then read, modified and rewritten by this one must come
// back whole: the publication join rewrites the file in place, and a round trip that dropped the
// keys this build has no name for would silently delete a newer build's evidence. Keys this build
// DOES name are not shadowed by the carried copy — the named field is the one that wins.
func TestCaptureSidecar_PreservesAFutureWritersFields(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"v":1,"observation_id":"obs-1","session":"s-1","outcome":"captured",` +
		`"future_field":{"a":1},"another":"kept"}`)

	var sc CaptureSidecar
	require.NoError(t, json.Unmarshal(raw, &sc))
	require.Len(t, sc.Unknown, 2, "exactly the two keys this build does not name")

	back, err := json.Marshal(sc)
	require.NoError(t, err)
	var round map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(back, &round))
	require.JSONEq(t, `{"a":1}`, string(round["future_field"]))
	require.JSONEq(t, `"kept"`, string(round["another"]))
	require.JSONEq(t, `"obs-1"`, string(round["observation_id"]),
		"a named field is still written from the struct, not from the carried copy")

	var again CaptureSidecar
	require.NoError(t, json.Unmarshal(back, &again))
	require.Equal(t, sc.Unknown, again.Unknown, "a second round trip carries the same keys")
}

// TestWriteCaptureSidecar_DeclaresARetentionRootOnlyWhenItRetainedBytes.
//
// The retention root is what holds the captured bytes live against GC, so it is declared exactly
// when there are bytes to hold. A sidecar that recorded a capture FAILURE has none, and declaring a
// retention root for its zero hash would put a claim on nothing into the file GC treats as a set of
// live references.
func TestWriteCaptureSidecar_DeclaresARetentionRootOnlyWhenItRetainedBytes(t *testing.T) {
	// Not parallel: newProject calls t.Setenv.
	p := newProject(t)
	id := core.ObservationID(strings.Repeat("cd", captureSidecarIDHexLen/2))

	require.NoError(t, WriteCaptureSidecar(p.Root, CaptureSidecar{
		Version: CaptureSidecarVersion, ObservationID: id, Session: "s-1", Outcome: "unavailable",
	}))
	_, statErr := os.Stat(paths.Long(RetentionRootsPath(p.Root)))
	require.ErrorIs(t, statErr, os.ErrNotExist,
		"a sidecar with no bytes declares no retention root")

	back, err := ReadCaptureSidecar(p.Root, id)
	require.NoError(t, err)
	require.True(t, back.BytesHash.IsZero())
	require.False(t, back.Published, "a durable capture with no reference yet is not published")

	other := core.ObservationID(strings.Repeat("ef", captureSidecarIDHexLen/2))
	require.NoError(t, WriteCaptureSidecar(p.Root, CaptureSidecar{
		Version: CaptureSidecarVersion, ObservationID: other, Session: "s-1",
		Outcome: "captured", Bytes: []byte("captured bytes\n"),
	}))
	declared, err := os.ReadFile(paths.Long(RetentionRootsPath(p.Root)))
	require.NoError(t, err, "retained bytes DO get a retention root")
	require.Contains(t, string(declared), string(RetentionEvidence))
	require.Contains(t, string(declared), string(other))
}
