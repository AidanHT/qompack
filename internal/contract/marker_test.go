package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// TestMarkerPath_IsRunMarkerJSON pins MarkerPath's exact location: task-4-spec.md fixes
// <projectRoot>/.qompack/run/marker.json, which .qompack/run/ leaves outside the §3.3 append-only
// set precisely because a marker is a mutable one-record view, not an append-only artifact.
func TestMarkerPath_IsRunMarkerJSON(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, ".qompack", "run", "marker.json")
	require.Equal(t, want, MarkerPath(root))
}

// TestWriteMarker_RoundTripsThroughReadMarker asserts WriteMarker and readMarker agree on the
// on-disk shape: {"session":"<id>","ts":<unixMilli>}.
func TestWriteMarker_RoundTripsThroughReadMarker(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, WriteMarker(root, core.SessionID("sess-1"), core.UnixMilli(1000)))

	rec, err := readMarker(root)
	require.NoError(t, err)
	require.Equal(t, core.SessionID("sess-1"), rec.Session)
	require.Equal(t, core.UnixMilli(1000), rec.TS)

	b, err := os.ReadFile(MarkerPath(root))
	require.NoError(t, err)
	require.Contains(t, string(b), `"session":"sess-1"`)
	require.Contains(t, string(b), `"ts":1000`)
}

// TestReadMarker_MissingFileIsAnError asserts a project with no marker.json yet — the ordinary
// first-terminal-hook-not-fired-yet case — reports an error, which every caller in this package
// treats as "no observation yet", never as a panic or a fabricated record.
func TestReadMarker_MissingFileIsAnError(t *testing.T) {
	_, err := readMarker(t.TempDir())
	require.Error(t, err)
}

// TestWriteMarker_OverwritesRatherThanAppends asserts the second write replaces the first: a
// marker is a mutable one-record view (task-4-spec.md), and WriteAtomic is the reason it can be.
func TestWriteMarker_OverwritesRatherThanAppends(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, WriteMarker(root, core.SessionID("sess-1"), core.UnixMilli(1000)))
	require.NoError(t, WriteMarker(root, core.SessionID("sess-2"), core.UnixMilli(2000)))

	rec, err := readMarker(root)
	require.NoError(t, err)
	require.Equal(t, core.SessionID("sess-2"), rec.Session)
	require.Equal(t, core.UnixMilli(2000), rec.TS)
}

// TestWriteMarker_IsTheOnlyMarkerWriterInContract is the contract-side half of task-4-spec.md's
// TestMarkerIsWrittenByFlushAndCheckpointOnly — the daemon-route half (driving session.start/
// flush/checkpoint and asserting marker.json's presence) belongs to the NEXT task, which wires
// WriteMarker into the daemon's flush and checkpoint routes. What THIS package must guarantee on
// its own is narrower but just as load-bearing: nothing inside internal/contract itself ever CALLS
// WriteMarker except tests — in particular, no Assertion.Check may write the very marker
// session_start.fires reads, which would let an assertion "observe" its own side effect.
func TestWriteMarker_IsTheOnlyMarkerWriterInContract(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	var callSites []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.Contains(trimmed, "WriteMarker(") {
				continue
			}
			if strings.HasPrefix(trimmed, "func WriteMarker(") || strings.HasPrefix(trimmed, "// ") {
				continue // the definition itself and its doc comment are not call sites
			}
			callSites = append(callSites, f+": "+trimmed)
		}
	}

	require.Empty(t, callSites,
		"WriteMarker must be called only by the daemon's flush and checkpoint routes (SP-05's next "+
			"task), never from within internal/contract's own production code; found: %v", callSites)
}
