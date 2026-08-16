package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// TestResolveBlob_NilEventLeavesBlobFileUntouched pins Minor 6: a blob descriptor on a request
// with a nil Event has nowhere to restore the bytes into, so resolveBlob must leave both the
// descriptor and the blob file alone — not read it, not delete it, not clear Raw.
func TestResolveBlob_NilEventLeavesBlobFileUntouched(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))
	blobPath := filepath.Join(dir, "blob-1-1.bin")
	require.NoError(t, os.WriteFile(blobPath, []byte("payload"), 0o600))

	ref, err := json.Marshal(blobRef{Blob: "blob-1-1.bin", Bytes: 7, Field: drainBlobToolResponse})
	require.NoError(t, err)

	req := ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1", Event: nil, Raw: ref}
	got := resolveBlob(root, logging.Nop(), req)

	require.Equal(t, ref, []byte(got.Raw), "the descriptor must be left in place when there is nowhere to restore it")
	require.FileExists(t, blobPath, "the blob file must not be deleted when Event is nil")
}

// TestResolveBlob_NonBlobRawIsUntouched pins the ordinary case: a request whose Raw is unrelated
// JSON (not a blob descriptor) passes through unchanged.
func TestResolveBlob_NonBlobRawIsUntouched(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	req := ipc.Request{Op: ipc.OpStatus, Raw: json.RawMessage(`{"unrelated":true}`)}
	got := resolveBlob(root, logging.Nop(), req)
	require.Equal(t, req.Raw, got.Raw)
}

// TestResolveBlob_RefusesPathTraversal is the fix round 2 FR-3 regression: a hostile or corrupt
// spool/WAL line naming a Blob outside spool/ (or lacking the shipped client's blob- prefix) must
// never be joined and read — resolveBlob would otherwise happily read (and, on success, delete)
// whatever file the descriptor names.
func TestResolveBlob_RefusesPathTraversal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))

	// A file OUTSIDE spool/ that a successful traversal would read and delete.
	secretPath := filepath.Join(root, "secret.txt")
	require.NoError(t, os.WriteFile(secretPath, []byte("do not touch"), 0o600))

	cases := []struct {
		name string
		blob string
	}{
		{"parent-traversal", "../secret.txt"},
		{"nested-traversal", "blob-1-1/../../secret.txt"},
		{"absolute-path", filepath.ToSlash(secretPath)},
		{"missing-prefix", "not-a-blob-1-1.bin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref, err := json.Marshal(blobRef{Blob: c.blob, Bytes: 4, Field: drainBlobToolResponse})
			require.NoError(t, err)

			req := ipc.Request{Op: ipc.OpObserveTool, Event: &hookio.Event{}, Raw: ref}
			got := resolveBlob(root, logging.Nop(), req)

			require.Equal(t, ref, []byte(got.Raw), "an unsafe descriptor must be left in place, not resolved")
			require.Nil(t, got.Event.ToolResponse, "the traversal target's content must never be read into ToolResponse")
		})
	}

	b, err := os.ReadFile(secretPath)
	require.NoError(t, err)
	require.Equal(t, "do not touch", string(b), "the traversal target must survive untouched and undeleted")
}

// TestResolveBlob_RestoresAndDeletes pins the ordinary success path directly against the shared
// helper (independent of the ingest/drain end-to-end tests, which exercise the same code through
// their own callers).
func TestResolveBlob_RestoresAndDeletes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(dir, 0o700))
	blobPath := filepath.Join(dir, "blob-1-1.bin")
	payload := []byte(`{"restored":true}`)
	require.NoError(t, os.WriteFile(blobPath, payload, 0o600))

	ref, err := json.Marshal(blobRef{Blob: "blob-1-1.bin", Bytes: len(payload), Field: drainBlobToolResponse})
	require.NoError(t, err)

	req := ipc.Request{Op: ipc.OpObserveTool, Event: &hookio.Event{}, Raw: ref}
	got := resolveBlob(root, logging.Nop(), req)

	require.JSONEq(t, string(payload), string(got.Event.ToolResponse))
	require.Empty(t, got.Raw)
	_, statErr := os.Stat(blobPath)
	require.True(t, os.IsNotExist(statErr), "the blob file must be deleted once resolved")
}
