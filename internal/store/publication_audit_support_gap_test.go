package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// A sidecar written by a NEWER build is a support gap (Qompack.md §7.1: report the unsupported
// capability, never guess), and fsck's own captures row says exactly that. The pass stays Incomplete
// for it — this build cannot classify the sidecar, so a zero gap count is still only a lower bound —
// but a consumer must be able to tell that ONE cause apart from every other, or it has to call a
// downgraded project damaged. These rows pin that the distinction is exact: it holds only when the
// newer schema is the sole cause, and any other cause of incompleteness beside it defeats it.

func newerSchemaSidecar() []byte {
	return []byte(fmt.Sprintf(`{"v":%d,"op":"observe.tool","published":false,"outcome":"ok","bytes_hash":"sha256:%s"}`,
		CaptureSidecarVersion+1, nonzeroHex()))
}

// TestAuditPublication_NewerSchemaAloneIsASupportGap: incomplete, never classified, counted, and
// recognizable as the only cause.
func TestAuditPublication_NewerSchemaAloneIsASupportGap(t *testing.T) {
	tp := newTestStore(t)
	writeRawSidecar(t, tp.Root, "newer-one", newerSchemaSidecar())
	writeRawSidecar(t, tp.Root, "newer-two", newerSchemaSidecar())
	seedCapture(t, tp.Root, "current-published", auditOpObserveTool, true, "ok", []byte("body"))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)

	require.True(t, a.Incomplete, "this build cannot classify a newer sidecar: still a lower bound")
	require.Zero(t, a.UnpublishedCaptures, "a newer schema's fields are not this build's to call a gap")
	require.Equal(t, 2, a.NewerSchemaCaptures)
	require.True(t, a.IncompleteOnlyForNewerSchemas(), "notes=%v", a.Notes)
}

// TestAuditPublication_NewerSchemaBesideOtherIncompletenessIsNotASupportGap: every other cause keeps
// the pass an uncertified one, whether it is another unreadable record or a stray tree entry.
func TestAuditPublication_NewerSchemaBesideOtherIncompletenessIsNotASupportGap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, root string)
	}{
		{"an older schema", func(t *testing.T, root string) {
			writeRawSidecar(t, root, "older", []byte(`{"op":"observe.tool","published":false,"outcome":"ok"}`))
		}},
		{"an unknown op", func(t *testing.T, root string) {
			writeRawSidecar(t, root, "unknown-op",
				[]byte(fmt.Sprintf(`{"v":%d,"op":"observe.future","published":false,"outcome":"ok"}`, CaptureSidecarVersion)))
		}},
		{"an unexpected entry in the capture tree", func(t *testing.T, root string) {
			p := filepath.Join(paths.Of(root).Records, captureSidecarDir, "stray.json")
			require.NoError(t, os.WriteFile(paths.Long(p), []byte(`{}`), 0o600))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestStore(t)
			writeRawSidecar(t, tp.Root, "newer", newerSchemaSidecar())
			tc.plant(t, tp.Root)

			a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
			require.NoError(t, err)
			require.True(t, a.Incomplete)
			require.Equal(t, 1, a.NewerSchemaCaptures)
			require.False(t, a.IncompleteOnlyForNewerSchemas(), "notes=%v", a.Notes)
		})
	}
}

// TestAuditPublication_CompletePassIsNotASupportGap: the predicate is about an INCOMPLETE pass; a
// complete one has nothing to excuse.
func TestAuditPublication_CompletePassIsNotASupportGap(t *testing.T) {
	tp := newTestStore(t)
	seedCapture(t, tp.Root, "current-published", auditOpObserveTool, true, "ok", []byte("body"))

	a, err := tp.Store.AuditPublication(context.Background(), DefaultPublicationScanCap())
	require.NoError(t, err)
	require.False(t, a.Incomplete, "notes=%v", a.Notes)
	require.Zero(t, a.NewerSchemaCaptures)
	require.False(t, a.IncompleteOnlyForNewerSchemas())
}
