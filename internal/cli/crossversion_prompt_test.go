package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// seedEarlierBuildPrompt writes one prompt delivery exactly as the builds before the V6 close-out's
// prompt link did (dev build 301a8e9, the "previous build" of the live lane's UAT-12; reproduced
// with that build's own hooks, run log plans/sdd/V6-closeout/w13-restore/runs/): the prompt IS
// published — a prompt_<session>_<turn> tool_use record whose args digest is the digest of
// {"prompt": <text>}, bound to no observation — but the capture sidecar was never joined to it, so
// it says published:false. withRecord false seeds the sidecar alone: a genuine stage-one gap.
func seedEarlierBuildPrompt(t *testing.T, p seededProject, arrival uint64, turn core.TurnIndex, text string, withRecord bool) {
	t.Helper()
	ctx := context.Background()
	sess := core.SessionID("s-fsck")
	if withRecord {
		s, err := store.Open(p.Root, config.Defaults(), store.Deps{Clock: testClock()})
		require.NoError(t, err)
		res, err := s.PutBytes(ctx, []byte(text), store.PutOptions{Tool: "UserPromptSubmit"})
		require.NoError(t, err)
		args, err := json.Marshal(struct {
			Prompt string `json:"prompt"`
		}{text})
		require.NoError(t, err)
		digest, preview := store.ArgsDigest(args)
		require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
			ID: core.ToolUseID("prompt_s-fsck_" + itoa(int(turn))), Session: sess, Turn: turn, TS: 2,
			Tool: "UserPromptSubmit", ArgsDigest: digest, ArgsPreview: preview,
			Root: res.Root.Hash, Bytes: int64(len(text)), Status: store.StatusOK,
		}))
		require.NoError(t, s.Close())
	}
	payload, err := json.Marshal(map[string]string{
		"cwd": "/fixture/proj", "hook_event_name": "UserPromptSubmit", "prompt": text,
		"session_id": string(sess), "transcript_path": "/fixture/proj/transcript.jsonl",
	})
	require.NoError(t, err)
	id, err := core.NewObservationID(sess, arrival)
	require.NoError(t, err)
	require.NoError(t, store.WriteCaptureSidecar(p.Root, store.CaptureSidecar{
		ObservationID: id, Session: sess, Arrival: arrival, Op: "observe.prompt",
		Outcome: core.OutcomeOK, Fidelity: core.FidelityExact, Bytes: payload,
	}))
}

// TestFsck_PromptCapturesAnEarlierBuildNeverLinkedReadAsPublished is D3 (cross-version) from the
// live lane's UAT-12: the candidate counted the previous build's UserPromptSubmit sidecars as
// "stage 1 only, no reference joined", so fsck's captures and publication rows failed, a backup
// taken before the upgrade restored with integrity FAILED, and the first post-upgrade daemon start
// logged LOUD unpublished_captures. The reference exists — that build published every prompt as a
// tool_use record and only never wrote the link — so such a sidecar is published, and it is read
// as published without re-minting or rewriting anything. A prompt sidecar no record accounts for is
// still a gap, and one record excuses one sidecar.
func TestFsck_PromptCapturesAnEarlierBuildNeverLinkedReadAsPublished(t *testing.T) {
	isolateUserGlobal(t)

	t.Run("each sidecar has its record", func(t *testing.T) {
		p := seedFsckProject(t)
		seedEarlierBuildPrompt(t, p, 2, 2, "Read notes.md and tell me the release marker.", true)
		seedEarlierBuildPrompt(t, p, 5, 3, "Now list the files in the project.", true)

		code, doc, errw := fsckJSON(t, p.Root)
		for _, id := range []string{"captures", "publication"} {
			row := fsckRequireRow(t, doc, id)
			require.Equal(t, true, row["ok"], "%s: %s", id, fsckDetail(row))
			require.Contains(t, fsckDetail(row), "2 prompt capture sidecar(s)")
		}
		require.Equal(t, ExitOK, code, "stderr=%s", errw)

		s, err := store.OpenReadOnly(p.Root, config.Defaults(), store.Deps{})
		require.NoError(t, err)
		audit, err := s.(store.PublicationAuditor).AuditPublication(context.Background(), store.DefaultPublicationScanCap())
		require.NoError(t, s.Close())
		require.NoError(t, err)
		require.Zero(t, audit.UnpublishedCaptures, "the daemon's startup accounting agrees: no gap")
		require.Equal(t, 2, audit.LegacyLinkedPrompts)

		code, out, stderr := fsckDispatch(t, "backup", "create", "--project", p.Root, "--id", "pre-upgrade", "--json")
		require.Equal(t, ExitOK, code, "%s %s", out, stderr)
		code, out, stderr = fsckDispatch(t, "backup", "restore", "--project", p.Root, "--id", "pre-upgrade",
			"--destination", filepath.Join(t.TempDir(), "recovered"), "--json")
		require.Equal(t, ExitOK, code, "a pre-upgrade backup restores with its integrity checks passing: %s %s", out, stderr)
	})

	t.Run("a sidecar no record accounts for is still a gap", func(t *testing.T) {
		p := seedFsckProject(t)
		seedEarlierBuildPrompt(t, p, 2, 2, "the same words", true)
		seedEarlierBuildPrompt(t, p, 5, 3, "the same words", false) // two deliveries, one record
		seedEarlierBuildPrompt(t, p, 8, 4, "words nothing recorded", false)

		code, doc, _ := fsckJSON(t, p.Root)
		row := fsckRequireRow(t, doc, "captures")
		require.Equal(t, false, row["ok"])
		require.EqualValues(t, 2, row["count"], "one record excuses one sidecar: %s", fsckDetail(row))
		pub := fsckRequireRow(t, doc, "publication")
		require.Contains(t, fsckDetail(pub), "2 unlinked captures")
		require.NotEqual(t, ExitOK, code)
	})
}
