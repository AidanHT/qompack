// V4 §4.11 — the live-session write set, append-only growth, and checkpoint immutability, with
// checkpoints, pins, the ledger, the MCP surface and the frontier advancer all resident.
//
// Successor to TestV3_LiveSessionWriteSetAndAppendOnly, whose helpers (x9Snapshot, x9AssertConfined,
// x9ReadLogs, x9ListFiles) this row reuses rather than re-deriving: the write-set question is the
// same question, and the wave-3 subsystems are the new variable.
//
// PENDING-A2 (reconciliation map §4.11): durable publication, restart-manifest and crash-cut
// coverage belong to unit A2, which is NOT in this base. This row asserts the write-set and
// immutability clauses only; the crash-cut arm is deliberately absent rather than faked, and is
// flagged in the V4 report.
package e2e

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// x11v4Session is this row's session identity.
const x11v4Session = core.SessionID("sess-e2e-v4-x11")

// x11v4Turns is how many tool uses the live sequence replays. Enough that the append-only logs are
// genuinely appended to between the two snapshots, and small enough that every one is a real
// process spawn the row can afford.
const x11v4Turns = 12

// TestV4_LiveSessionWriteSetAppendOnlyAndImmutability is V4-VERIFY §4.11.
//
// The negative control is the last arm: a byte flipped inside a finalized checkpoint must be
// REJECTED by the reader with core.ErrContract, and the manifest verification must name that
// sequence. Without it, "the artifact is immutable" would be satisfied by an artifact nobody ever
// re-reads.
func TestV4_LiveSessionWriteSetAppendOnlyAndImmutability(t *testing.T) {
	p := v4Project(t)
	home := p.Home()

	// The whole-filesystem "before" snapshot: project tree + HOME, taken before the first event.
	before := x9Snapshot(t, p.Root, home)

	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x11v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x11v4Session, "keep every write inside .qompack while wave 3 is resident"), env)
	r.SeedTurns(t, x11v4Session, "v4x11", x11v4Turns)

	// The append-only logs, mid-session. The second reading must EXTEND these, byte for byte.
	midLogs := x9ReadLogs(t, p.Root)

	// More traffic, then a real checkpoint: the wave-3 writers all take their turn.
	r.SeedTurns(t, x11v4Session, "v4x11b", x11v4Turns)
	cpCloseObserverSegment(t, r.Segs, x11v4Session)
	cpCloseSegment(t, r.Segs, x11v4Session, 0, 9)
	r.RunIdle(t)
	_, instr := r.PreCompact(t, x11v4Session)
	require.NotEmpty(t, instr, "the row needs a sealed checkpoint to assert immutability on")
	obsRunHook(t, r.Bin, []string{"flush"}, obsFlushPayload(t, p.Root, x11v4Session), env)

	// ── Every write lands under .qompack/ ────────────────────────────────────────────────────────
	after := x9Snapshot(t, p.Root, home)
	x9AssertConfined(t, before, after, p.Root, home)

	// ── Append-only files are only ever appended ─────────────────────────────────────────────────
	endLogs := x9ReadLogs(t, p.Root)
	checked := 0
	for rel, midBytes := range midLogs {
		endBytes, ok := endLogs[rel]
		require.True(t, ok, "%s existed mid-session and must not have been removed", rel)
		require.GreaterOrEqual(t, len(endBytes), len(midBytes),
			"%s must not have shrunk: an append-only log is never rewritten", rel)
		require.True(t, strings.HasPrefix(string(endBytes), string(midBytes)),
			"%s must still begin with exactly the bytes it held mid-session — a differing prefix "+
				"means a line was rewritten in place, which is the failure this asserts", rel)
		checked++
	}
	require.Positive(t, checked,
		"at least one append-only log must have existed mid-session, or this assertion is vacuous")

	// ── Finalized checkpoints are read-only ──────────────────────────────────────────────────────
	artifacts := cpCheckpointArtifacts(t, p.Root)
	require.NotEmpty(t, artifacts, "the PreCompact must have sealed an artifact")
	l := paths.Of(p.Root)
	var sealed core.CheckpointSeq
	entries := x4RequireManifestVerifies(t, p.Root)
	for _, e := range entries {
		fi, err := os.Stat(paths.Long(paths.CheckpointPath(l, e.Seq)))
		require.NoError(t, err)
		require.Zero(t, fi.Mode().Perm()&0o222,
			"paths.CreateNew chmods every checkpoint to 0o444 (FILE_ATTRIBUTE_READONLY on Windows): "+
				"a finalized checkpoint carries no write bit (§7.4)")
		sealed = e.Seq
	}
	require.NotZero(t, sealed)

	// ── NEGATIVE CONTROL: flip one byte inside the artifact ──────────────────────────────────────
	//
	// Immutability is only meaningful if something re-reads and rejects. The reader must answer
	// core.ErrContract, and the manifest verification must NAME the corrupted sequence.
	cpPath := paths.Long(paths.CheckpointPath(l, sealed))
	raw, err := os.ReadFile(cpPath)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(cpPath, 0o600), "a sealed artifact is 0444; the flip needs the write bit")

	flipped := append([]byte(nil), raw...)
	at := len(flipped) / 2
	flipped[at] ^= 0x20 // a printable-to-printable flip: the file stays JSON-shaped, only its bytes change
	require.NotEqual(t, raw, flipped, "the flip must actually change the file")
	require.NoError(t, os.WriteFile(cpPath, flipped, 0o600))

	rd, err := checkpoint.OpenReader(p.Root, p.Log, r.Opts.Metrics)
	require.NoError(t, err)
	bad, err := rd.Verify(t.Context())
	require.NoError(t, err, "Verify reports mismatches through its result, not through an error")
	require.Contains(t, bad, sealed,
		"NEGATIVE CONTROL: `qompack fsck`'s own re-hash must name checkpoint %d as not matching its "+
			"MANIFEST line; verified=%v", sealed, bad)

	_, _, getErr := rd.Get(t.Context(), sealed)
	require.Error(t, getErr, "a corrupted artifact must not be served as if it were intact")
	require.True(t, errors.Is(getErr, core.ErrContract),
		"a byte flipped inside a finalized checkpoint is a CONTRACT failure, not a parse error: %v", getErr)

	// Exactly one Loud line about this artifact: loud once, not on every read.
	louds := 0
	for _, line := range loudLines(t, p.Root) {
		if strings.Contains(line, "checkpoint") && (strings.Contains(line, "hash") ||
			strings.Contains(line, "mismatch") || strings.Contains(line, "contract")) {
			louds++
		}
	}
	require.LessOrEqual(t, louds, 1,
		"a corrupted checkpoint is Loud at most once per process, never once per read: %v",
		loudLines(t, p.Root))
}
