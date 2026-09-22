package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The FIRST PreCompact of a daemon's life, driven through the shipped composition.
//
// This is the most common real-world path there is — a brand-new session compacting for the first
// time — and it was the one path the wiring could not serve. wireCheckpointSources published a
// SourceSet whose Ledger was nil (negknow.Open is lazy on purpose), FileWriter.SetSources dropped
// it without a word because SourceSet.Validate rejects a nil Ledger, and the ledger itself was
// opened only by WireRehydrator on the first COMPACTION — the SessionStart(source=compact) that
// arrives AFTER the PreCompact that needed it. The user-visible result was
// `hookSpecificOutput: null`, one Warn reading "checkpoint: SourceSet.Store is nil", and no
// checkpoint at all.
//
// Everything below goes through the real Dispatch surface against a real resident daemon on a
// FRESH state directory: no idle pass is driven, no rehydration is driven, and nothing arms the
// writer on the test's behalf. If the composition cannot seal here, it cannot seal in production.

// precompactPayload is the PreCompact hook event the host sends to `qompack checkpoint`.
func precompactPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "PreCompact",
		"session_id":      sess,
		"cwd":             root,
		"transcript_path": filepath.Join(root, "transcript.jsonl"),
		"trigger":         "auto",
	})
	require.NoError(t, err)
	return b
}

// runCheckpointHook drives the shipped `qompack checkpoint` client in process and returns the one
// hookio.Output §2.3 promises on every path.
func runCheckpointHook(t *testing.T, root string, sess core.SessionID) hookio.Output {
	t.Helper()
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), hookCmds(),
		[]string{"qompack", "checkpoint", "--project", root},
		Env{
			Getenv:  noEnv,
			Stdin:   bytes.NewReader(precompactPayload(t, root, sess)),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		},
		&out, &errw)
	require.Equal(t, ExitOK, code, "a hook must always exit 0\nstdout:\n%s\nstderr:\n%s", out.String(), errw.String())

	var got hookio.Output
	require.NoError(t, json.Unmarshal(out.Bytes(), &got),
		"`qompack checkpoint` must emit exactly one hookio.Output:\n%s", out.String())
	return got
}

// checkpointArtifacts lists the sealed checkpoint artifacts under root.
func checkpointArtifacts(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.Long(paths.Of(root).Checkpoints))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestDaemonFirstPreCompactSealsCheckpoint is the end-to-end proof for the seam gap: one fresh
// daemon, one fresh session, one PreCompact, and a checkpoint that exists and reads back.
func TestDaemonFirstPreCompactSealsCheckpoint(t *testing.T) {
	const sess = core.SessionID("sess_first_precompact")

	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	defer stop()

	require.Empty(t, checkpointArtifacts(t, root),
		"fixture sanity: a fresh state directory holds no checkpoint")

	out := runCheckpointHook(t, root, sess)

	// The host's half of the contract is the EMPTY response. This row used to require
	// customInstructions on stdout, as its signal that the seal happened; Claude Code 2.1.280
	// rejects exactly that shape ("hookSpecificOutput.hookEventName: expected one of …", C1.12),
	// because no PreCompact hookSpecificOutput variant exists. So the seal is proven below from the
	// artifact itself — which is what the defect this row was written for actually lacked — and
	// stdout is held to the documented contract instead.
	require.Nil(t, out.HookSpecificOutput,
		"PreCompact accepts no hookSpecificOutput; the host rejects the whole response over one")
	require.Empty(t, out.SystemMessage, "the host discards a PreCompact systemMessage")

	// The durable half: an artifact on disk, chained at seq 1, readable and verifiable through the
	// shipped reader rather than by re-parsing the file this test just found.
	require.Len(t, checkpointArtifacts(t, root), 1,
		"the first PreCompact must have sealed exactly one checkpoint artifact")

	r, err := checkpoint.OpenReader(root, logging.Nop(), obs.New(testClock()))
	require.NoError(t, err, "checkpoint.OpenReader(%s)", root)
	cp, ref, err := r.Latest(context.Background(), sess)
	require.NoError(t, err, "the sealed checkpoint must read back")
	require.Equal(t, core.CheckpointSeq(1), ref.Seq, "the first checkpoint of a project is seq 1")
	require.Equal(t, sess, cp.Session, "the checkpoint must belong to the session that compacted")

	bad, err := r.Verify(context.Background())
	require.NoError(t, err, "Verify must run over the manifest the seal appended to")
	require.Empty(t, bad, "the sealed artifact must hash to its manifest line")
}
