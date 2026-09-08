// V4 §4.1 — the PreCompact → checkpoint → SessionStart(source=compact) → rehydrate round trip.
//
// Every component in the chain is the production one: the real binary's hook subcommands, the real
// daemon composed the way internal/cli composes it (v4_harness_test.go), internal/checkpoint's
// FileWriter, the on-disk checkpoints/ tree and its MANIFEST, the daemon's rehydrate service and
// internal/rehydrate's Build. Nothing here is stubbed.
//
// Retired clauses this row must NOT assert (reconciliation map §4.1): nothing claims the host
// ACCEPTED the emitted customInstructions, and nothing claims native history was shortened. Both
// are host-side effects no in-repo test can observe; the row asserts only what Qompack wrote and
// what Qompack read back.
package e2e

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// x1v4Session is this row's session identity, distinct from every other file's so the package can
// run in one process without two rows sharing a draft, a registry entry or a segment range.
const x1v4Session = core.SessionID("sess-e2e-v4-x01")

// x1v4Intent is the verbatim first prompt. It is deliberately multi-line and carries a nonce that
// appears nowhere else in the tree, so "item 2 is byte-identical verbatim intent" is an assertion
// about THESE bytes rather than about a substring some summarizer could also have produced.
const x1v4Intent = "refresh tokens 500 on the second call, nonce Q4X1-VERBATIM-9f2c\n" +
	"reproduce with: curl -XPOST /auth/refresh twice"

// x1v4SealedSeq is the sequence the first checkpoint of a fresh project carries.
const x1v4SealedSeq = core.CheckpointSeq(1)

// x4RequireManifestVerifies re-hashes every sealed artifact against its MANIFEST.jsonl line, which
// is exactly what `qompack fsck` does, and returns the entries it verified.
func x4RequireManifestVerifies(t *testing.T, root string) []paths.ManifestEntry {
	t.Helper()
	l := paths.Of(root)
	entries, err := paths.ReadManifest(l)
	require.NoError(t, err, "checkpoints/MANIFEST.jsonl must be readable")
	require.NotEmpty(t, entries, "a sealed checkpoint must have appended a MANIFEST line")
	for _, e := range entries {
		raw, rerr := os.ReadFile(paths.Long(paths.CheckpointPath(l, e.Seq)))
		require.NoError(t, rerr, "the artifact named by manifest seq %d must exist", e.Seq)
		require.Equal(t, int64(len(raw)), e.Bytes, "manifest seq %d records the artifact's byte length", e.Seq)
		sum := sha256.Sum256(raw)
		require.Equal(t, core.Hash(sum).String(), e.SHA256,
			"manifest seq %d must re-hash to its recorded digest", e.Seq)
	}
	return entries
}

// x4InjectedSeq returns the checkpoint sequence the injected span claims, and whether the payload
// carried a well-formed open tag at all. The tag is the §8.5 identity carrier: a rehydration that
// resolved no checkpoint stamps seq=0.
func x4InjectedSeq(t *testing.T, ac string) (core.CheckpointSeq, bool) {
	t.Helper()
	const prefix = "<!-- qompack:injected seq="
	at := strings.Index(ac, prefix)
	if at < 0 {
		return 0, false
	}
	var seq int
	_, err := fmt.Sscanf(ac[at+len(prefix):], "%d", &seq)
	require.NoError(t, err, "the injection open tag must carry a numeric seq: %s", ac)
	return core.CheckpointSeq(seq), true
}

// TestV4_PreCompactToCheckpointToRehydrateRoundTrip is V4-VERIFY §4.1.
//
// The negative control is the last arm, and it is what makes the identity assertion non-vacuous:
// with the sealed artifact deleted, the same rehydration must NOT still report that checkpoint and
// must not silently substitute a parent. A rehydration that answered seq=1 either way would prove
// nothing about which checkpoint was resolved.
func TestV4_PreCompactToCheckpointToRehydrateRoundTrip(t *testing.T) {
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	// The live half of a session, through the real binary: registration, the verbatim first
	// prompt, and six tool uses the checkpoint's pointers are built from.
	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x1v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"}, obsPromptPayload(t, p.Root, x1v4Session, x1v4Intent), env)
	r.SeedTurns(t, x1v4Session, "v4x01", 6)

	// The two steps a daemon needs before its FIRST PreCompact can seal — themselves asserted; see
	// ArmCheckpointSources and the harness header's production seam-gap note.
	r.ArmCheckpointSources(t, x1v4Session)

	// ── PreCompact: the real writer seals the artifact ───────────────────────────────────────────
	out, instr := r.PreCompact(t, x1v4Session)
	require.NotNil(t, out.HookSpecificOutput,
		"in ModeFull the bound Services.PreCompact seam must answer through hookSpecificOutput")
	require.NotEmpty(t, instr, "a full-mode PreCompact must emit customInstructions")
	require.Contains(t, instr, checkpoint.SentinelPhrase,
		"ForbidSnippets is always true on the PreCompact path, so the sentinel rides every payload")

	require.Equal(t, []string{"0001.json"}, cpCheckpointArtifacts(t, p.Root),
		"the PreCompact hook must have sealed exactly one artifact")
	entries := x4RequireManifestVerifies(t, p.Root)
	require.Len(t, entries, 1, "one finalize appends exactly one MANIFEST.jsonl line")
	require.Equal(t, x1v4SealedSeq, entries[0].Seq)

	// The bytes are a real §8.5 document, and it is THIS session's.
	raw, err := os.ReadFile(paths.Long(paths.CheckpointPath(paths.Of(p.Root), x1v4SealedSeq)))
	require.NoError(t, err)
	sealed, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err, "0001.json must parse as a versioned Checkpoint: %s", raw)
	require.Equal(t, x1v4Session, sealed.Session)
	require.Equal(t, x1v4SealedSeq, sealed.Seq)

	// ── SessionStart(source=compact): the real rehydrator resolves THAT checkpoint ───────────────
	ac := r.CompactStart(t, x1v4Session)
	require.NotEmpty(t, ac, "a compact SessionStart must inject a rehydrated context")

	seq, tagged := x4InjectedSeq(t, ac)
	require.True(t, tagged, "the payload must open with the §8.5 injection tag: %s", ac)
	require.Equal(t, x1v4SealedSeq, seq,
		"the checkpoint the PreCompact hook sealed must be the one Latest resolves at SessionStart; "+
			"seq=0 means the rehydrator took its no-checkpoint path: %s", ac)
	require.Contains(t, ac, checkpoint.InjectionCloseTag,
		"the injected span must close, or the next checkpoint re-encodes it (§4.6)")
	require.Contains(t, ac, scProbePrefix,
		"the daemon appends a §12.1 contract sentinel probe to every additionalContext it emits")

	// ── Item 2: the verbatim intent, byte for byte, from L0 ──────────────────────────────────────
	require.Contains(t, ac, "## 2. Original user intent",
		"§8.6 item 2's heading must be present: %s", ac)
	for _, line := range strings.Split(x1v4Intent, "\n") {
		require.Contains(t, ac, "> "+line,
			"item 2 must carry the L0 prompt VERBATIM (rehydrate quotes each line with a > prefix), "+
				"not a summary of it; missing line %q in: %s", line, ac)
	}
	require.Contains(t, ac, "Q4X1-VERBATIM-9f2c",
		"the intent nonce is what distinguishes verbatim carriage from a regenerated paraphrase")

	// ── The negative control: the sealed artifact removed, the same rehydration re-run ───────────
	//
	// This is the mechanism-removal arm. It fails if rehydration is ever changed to fall back to a
	// parent checkpoint, to cache a previously resolved Ref, or to report a seq it did not read.
	ctlPath := paths.Long(paths.CheckpointPath(paths.Of(p.Root), x1v4SealedSeq))
	require.NoError(t, os.Chmod(ctlPath, 0o600), "a sealed artifact is 0444; unlink needs the write bit back")
	require.NoError(t, os.Remove(ctlPath), "removing the newest checkpoint")
	require.NoFileExists(t, ctlPath)

	ctlAC := r.CompactStart(t, x1v4Session)
	ctlSeq, ctlTagged := x4InjectedSeq(t, ctlAC)
	if ctlTagged {
		require.NotEqual(t, x1v4SealedSeq, ctlSeq,
			"NEGATIVE CONTROL: with the sealed artifact deleted the rehydrator must not still "+
				"report seq=%d — that would mean the identity assertion above is satisfied by "+
				"something other than reading the file: %s", x1v4SealedSeq, ctlAC)
	}
}
