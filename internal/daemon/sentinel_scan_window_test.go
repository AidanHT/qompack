package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The §12.1 probe is found by scanning the host's transcript on the prompts after the start that
// minted it. The live lane (V6 close-out Phase 4, retrieval D4, UAT-11) put the probe 798 bytes into
// a transcript and then a 363 KB Read in the first turn: the next prompt began at byte 954,253, a
// scan of the last 256 KiB missed the probe on both chances, and the monitor degraded the session to
// passive, so SessionStart:compact injected nothing. These rows rebuild that transcript shape.

// appendTranscript appends lines to the transcript at p, creating it when it does not exist yet.
func appendTranscript(t *testing.T, p string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(paths.Long(p), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	require.NoError(t, err)
	for _, l := range lines {
		_, err = f.WriteString(l + "\n")
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
}

// largeToolResult is one transcript line carrying a tool result bigger than a 256 KiB tail: the
// UAT-11 Read of a 363,000-byte file, which the host delivered as a 376,892-character result.
func largeToolResult() string {
	return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` +
		strings.Repeat("x", 376_892) + `"}]}}`
}

// probeLine is the host's record of a SessionStart answer carrying token in its additionalContext.
func probeLine(token string) string {
	return `{"type":"attachment","attachment":{"type":"hook_additional_context","content":["<!-- qompack-contract-probe ` +
		token + ` -->"]}}`
}

// TestSentinelScan_ALargeFirstResultDoesNotHideTheProbe is the UAT-11 shape in -p mode: the host
// creates the transcript only after SessionStart:startup, writes the probe near its start, and a
// first-turn tool result larger than the tail window follows before the next prompt is scanned.
func TestSentinelScan_ALargeFirstResultDoesNotHideTheProbe(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-large-first-result")
	transcript := filepath.Join(dd.root, "late-transcript.jsonl")

	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-start")).OK)
	h := history(t, dd)
	require.NotEmpty(t, h.Sentinel.Token)

	appendTranscript(t, transcript,
		`{"type":"queue-operation","operation":"enqueue","content":"read notes.md and big.log"}`,
		probeLine(h.Sentinel.Token),
		`{"type":"user","message":{"role":"user","content":"read notes.md and big.log"}}`,
		largeToolResult(),
		`{"type":"user","message":{"role":"user","content":"now read odd.txt"}}`,
	)
	fi, err := os.Stat(paths.Long(transcript))
	require.NoError(t, err)
	require.Greater(t, fi.Size(), int64(sentinelScanTailBytes)+int64(len(probeLine(h.Sentinel.Token))),
		"fixture: the probe must sit farther from the end than the tail window reaches")

	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+1)
	require.True(t, history(t, dd).Sentinel.Observed,
		"a probe the host delivered before one large tool result must still be found")
}

// TestSentinelScan_AProbeMintedMidTranscriptIsFoundPastALargeResult is the same failure for a start
// whose transcript already holds a long history — a compact or a resume: the probe lands after
// everything written before the mint, and a large result after it must not hide it either.
func TestSentinelScan_AProbeMintedMidTranscriptIsFoundPastALargeResult(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-mid-transcript-probe")
	transcript := filepath.Join(dd.root, "long-transcript.jsonl")
	appendTranscript(t, transcript, largeToolResult(), largeToolResult())

	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "compact", transcript, "nonce-compact")).OK)
	h := history(t, dd)
	require.NotEmpty(t, h.Sentinel.Token)

	appendTranscript(t, transcript,
		`{"type":"system","subtype":"compact_boundary"}`,
		probeLine(h.Sentinel.Token),
		largeToolResult(),
		`{"type":"user","message":{"role":"user","content":"continue"}}`,
	)

	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+1)
	require.True(t, history(t, dd).Sentinel.Observed,
		"a probe written after a long history and followed by a large result must still be found")
}

// TestSentinelScan_AnAbsentProbeStillSpendsAChance: the wider scan changes where the probe is looked
// for, not what a miss means. A transcript the probe never reached — however large — still spends a
// chance per prompt, and two of them still fail the assertion.
func TestSentinelScan_AnAbsentProbeStillSpendsAChance(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-probe-never-delivered")
	transcript := filepath.Join(dd.root, "no-probe-transcript.jsonl")

	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-start")).OK)
	h := history(t, dd)
	appendTranscript(t, transcript,
		`{"type":"user","message":{"role":"user","content":"read big.log"}}`,
		largeToolResult(),
		`{"type":"user","message":{"role":"user","content":"again"}}`,
	)

	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+1)
	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+2)
	after := history(t, dd)
	require.False(t, after.Sentinel.Observed)
	require.Equal(t, 2, after.Sentinel.Chances, "each prompt that missed the probe spends one chance")
}
