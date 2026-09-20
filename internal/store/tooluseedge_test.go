package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// The tool-use index's own contract, and the preview that goes into index/tool_use.jsonl beside it.

// TestRecordToolUse_RefusesARecordWithNoId. The id is the record's identity: it is what a
// checkpoint's tool pointer names, what MarkSuperseded flips, and what the append-only conflict
// check compares. A record without one cannot be referenced or superseded, so it is refused rather
// than appended under the empty string, where the second such record would collide with the first.
func TestRecordToolUse_RefusesARecordWithNoId(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()

	err := tp.Store.RecordToolUse(ctx, ToolUseRecord{
		Session: core.SessionID("s-1"), Turn: 1, TS: 1, Tool: "Read", Path: "a.go",
	})
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Contains(t, err.Error(), "no id")
}

// TestToolUsesByPath_RefusesAClosedStore. Like FileHistory it answers from memory, and memory
// outlives Close; the guard is what stops a closed store from serving retrieval questions out of
// indices it has already released.
func TestToolUsesByPath_RefusesAClosedStore(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: core.ToolUseID("tu-1"), Session: core.SessionID("s-1"), Turn: 1, TS: 1,
		Tool: "Read", Root: core.Hash{1}, Path: "a.go",
	}))
	require.NoError(t, tp.Store.Close())

	_, err := tp.Store.ToolUsesByPath(ctx, "a.go", 0)
	require.ErrorIs(t, err, core.ErrDegraded)
}

// TestArgsPreview_NamesTheArgumentsACallWasAboutAndStaysBounded.
//
// The preview is what an operator reads in a tool-use row, so it has to name the thing the call was
// about — the file, the pattern, the command — rather than the whole argument object. When none of
// the identifying keys is there it falls back to the canonical JSON, because an empty preview is
// worse than a dense one. Either way the result is bounded and carries no control characters: this
// string goes into a JSONL line and then into a report.
func TestArgsPreview_NamesTheArgumentsACallWasAboutAndStaysBounded(t *testing.T) {
	t.Parallel()

	canonical := []byte(`{"depth":2}`)
	require.Equal(t, "src/a.go",
		argsPreview(map[string]any{"file_path": "src/a.go", "depth": "2"}, canonical),
		"an identifying key is the preview")
	require.Equal(t, "src/a.go TODO",
		argsPreview(map[string]any{"path": "src/a.go", "pattern": "TODO"}, canonical),
		"several identifying keys read in their declared order")
	require.Equal(t, `{"depth":2}`,
		argsPreview(map[string]any{"depth": "2"}, canonical),
		"no identifying key falls back to the canonical arguments")
	require.Equal(t, `{"depth":2}`, argsPreview([]any{}, canonical),
		"arguments that are not an object have no keys to pick from")
	require.Equal(t, `{"depth":2}`,
		argsPreview(map[string]any{"file_path": ""}, canonical),
		"an identifying key present but empty names nothing")
}

// TestPreviewString_StripsControlCharactersAndCutsOnARuneBoundary.
//
// Two separate hazards. A control character would corrupt the line the preview is written on, and a
// byte-wise truncation of a multi-byte rune would leave an invalid UTF-8 tail in a file every
// downstream reader parses as JSON. The ellipsis is what tells a reader the string was cut rather
// than being that short.
func TestPreviewString_StripsControlCharactersAndCutsOnARuneBoundary(t *testing.T) {
	t.Parallel()

	require.Equal(t, "a b", previewString("a\n\tb"), "control characters collapse into one space")
	require.Equal(t, "a b", previewString("a    b"), "runs of whitespace collapse")
	require.Equal(t, "", previewString(""))

	long := previewString(strings.Repeat("é", argsPreviewMax))
	require.LessOrEqual(t, len(long), argsPreviewMax, "the preview stays inside its byte budget")
	require.True(t, strings.HasSuffix(long, previewEllipsis), "a cut preview says it was cut")
	require.True(t, utf8.ValidString(long), "the cut landed on a rune boundary")
}

// TestToolUseRecord_UnmarshalJSONRefusesABodyThatIsNotARecord.
//
// ToolUseRecord's wire shape is frozen to a contract fixture, and its decoder is what reads a
// checkpoint's embedded tool pointers as well as the index. A body that is not an object has to
// report the decode failure rather than leave a zero record behind: a zero record has an empty id,
// which every reader downstream treats as a record it may not reference.
func TestToolUseRecord_UnmarshalJSONRefusesABodyThatIsNotARecord(t *testing.T) {
	t.Parallel()

	var rec ToolUseRecord
	require.Error(t, json.Unmarshal([]byte(`"not a record"`), &rec))
	require.Empty(t, rec.ID)

	require.NoError(t, json.Unmarshal([]byte(`{"id":"tu-1","tool":"Read","path":"a.go"}`), &rec))
	require.Equal(t, core.ToolUseID("tu-1"), rec.ID)
	require.Equal(t, "a.go", rec.Path)
}
