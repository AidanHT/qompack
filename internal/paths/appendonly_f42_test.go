package paths_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestAppendJSONL_TornTailDoesNotSwallowTheNextRecord is finding F4-2.
//
// test/fault truncated index/roots.jsonl inside its final record and found that the cut cost TWO
// records, not one: the reader steps over the damaged line, and the next AppendJSONL glued its
// bytes onto that partial tail, so the following record was inside the malformed line and gone too.
//
// The damaged line itself is not repaired and is not expected to parse — that is the cut, and it
// stays reported. What this pins is that the loss stops there.
func TestAppendJSONL_TornTailDoesNotSwallowTheNextRecord(t *testing.T) {
	p := filepath.Join(t.TempDir(), "roots.jsonl")

	require.NoError(t, paths.AppendJSONL(p, map[string]string{"id": "first"}))
	require.NoError(t, paths.AppendJSONL(p, map[string]string{"id": "second"}))

	// Tear the tail: drop the final newline and part of the last record, exactly as a torn write
	// leaves it.
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	torn := string(raw)[:len(raw)-6]
	require.False(t, strings.HasSuffix(torn, "\n"), "the fixture must end mid-record")
	require.NoError(t, os.WriteFile(p, []byte(torn), 0o600))

	require.NoError(t, paths.AppendJSONL(p, map[string]string{"id": "third"}))

	after, err := os.ReadFile(p)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(after), "\n"), "\n")
	require.Len(t, lines, 3, "the torn line stays one line and the new record is its own: %q", after)
	require.Equal(t, `{"id":"first"}`, lines[0])
	require.True(t, strings.HasPrefix(lines[1], `{"id":"sec`), "the damaged line is untouched: %q", lines[1])
	require.Equal(t, `{"id":"third"}`, lines[2],
		"the record appended after a torn tail must be readable on its own line")
}

// TestAppendJSONL_WellTerminatedFileGainsNoBlankLine is the other direction: the guard must not
// insert a newline into a file that already ends in one, which would produce an empty line every
// reader would then have to tolerate.
func TestAppendJSONL_WellTerminatedFileGainsNoBlankLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "roots.jsonl")
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, paths.AppendJSONL(p, map[string]string{"id": id}))
	}
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "{\"id\":\"a\"}\n{\"id\":\"b\"}\n{\"id\":\"c\"}\n", string(raw))
}

// TestAppendJSONL_FirstRecordInAnEmptyFileIsUnchanged: an absent or zero-length file has no tail to
// terminate, and must not gain a leading newline.
func TestAppendJSONL_FirstRecordInAnEmptyFileIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.jsonl")
	require.NoError(t, paths.AppendJSONL(fresh, map[string]string{"id": "only"}))
	raw, err := os.ReadFile(fresh)
	require.NoError(t, err)
	require.Equal(t, "{\"id\":\"only\"}\n", string(raw))

	empty := filepath.Join(dir, "empty.jsonl")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	require.NoError(t, paths.AppendJSONL(empty, map[string]string{"id": "only"}))
	raw, err = os.ReadFile(empty)
	require.NoError(t, err)
	require.Equal(t, "{\"id\":\"only\"}\n", string(raw))
}
