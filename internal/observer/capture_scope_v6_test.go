package observer

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// V6-AUTH-1 defence in depth: the observer must not launder an out-of-project file capture into the
// object store by swallowing its escaping path to "". These tests pin that a Read whose structured
// path escapes reaches neither PutBytes nor a ToolUseRecord, while legitimate in-project and
// pathless captures are untouched.

func TestOnToolUse_RefusesTraversalReadBeforePutBytes(t *testing.T) {
	h := newHarness(t)

	h.drive(readOf("toolu_out", "../../secret.txt", "SENSITIVE OUT-OF-PROJECT BYTES"))

	require.Empty(t, h.Store.Puts, "an out-of-project capture must never reach PutBytes (the object-store leak)")
	require.Empty(t, h.Store.Records, "no Path:\"\",Tool:\"FileRead\" record may be laundered in")
	require.Empty(t, h.Store.FileVersions)
	require.Equal(t, int64(1), h.counter(counterCaptureScopeRefused), "the refusal is counted under a closed label")
}

func TestOnToolUse_RefusesAbsoluteOutOfProjectRead(t *testing.T) {
	h := newHarness(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")

	h.drive(readOf("toolu_abs", outside, "SENSITIVE"))

	require.Empty(t, h.Store.Puts)
	require.Empty(t, h.Store.Records)
	require.Equal(t, int64(1), h.counter(counterCaptureScopeRefused))
}

func TestOnToolUse_RefusesDroppedPathFileReadAsUnprovable(t *testing.T) {
	h := newHarness(t)

	// A FileRead whose input names NO structured path cannot be proven inside the project, so this
	// defence-in-depth line refuses it (unprovable) rather than laundering its bytes in as pathless.
	h.drive(toolUse("toolu_drop", "Read", `{}`, `{"content":"SENSITIVE"}`))

	require.Empty(t, h.Store.Puts, "an unprovable file capture must never reach PutBytes")
	require.Empty(t, h.Store.Records)
	require.Equal(t, int64(1), h.counter(counterCaptureScopeRefused))
}

func TestOnToolUse_RefusesMultiEditWhoseSecondPathEscapes(t *testing.T) {
	h := newHarness(t)

	// A MultiEdit whose first edit is in-project and whose second escapes: the boundary must check
	// every structured path, not only the first.
	e := toolUse("toolu_multi", "MultiEdit",
		`{"file_path":"in.go","edits":[{"file_path":"a.go"},{"file_path":"../../out.go"}]}`,
		`{"content":"x"}`)
	h.drive(e)

	require.Empty(t, h.Store.Puts, "a mixed multi-edit with any escaping path is refused whole")
	require.Empty(t, h.Store.Records)
	require.Equal(t, int64(1), h.counter(counterCaptureScopeRefused))
}

func TestOnToolUse_InProjectReadStillStored(t *testing.T) {
	h := newHarness(t)

	h.drive(readOf("toolu_in", "src/auth.ts", "in-project content"))

	require.Len(t, h.Store.Puts, 1, "an in-project read is untouched by the boundary")
	require.Equal(t, "src/auth.ts", h.Store.Puts[0].Opts.Path)
	require.Len(t, h.Store.Records, 1)
	require.Equal(t, int64(0), h.counter(counterCaptureScopeRefused))
}

func TestOnToolUse_PathlessBashStillStored(t *testing.T) {
	h := newHarness(t)

	h.drive(bashOf("toolu_bash", "go test ./...", "ok\n"))

	require.Len(t, h.Store.Puts, 1, "a legitimately pathless producer is preserved")
	require.Empty(t, h.Store.Puts[0].Opts.Path)
	require.Equal(t, int64(0), h.counter(counterCaptureScopeRefused))
}
