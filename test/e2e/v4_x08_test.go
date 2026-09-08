// V4 §4.8 — the §4.6 injection tags are what keep rehydrated material out of the next checkpoint.
//
// Wired: the real observer's L0 verbatim capture of a prompt that CARRIES a previously injected
// span, the real internal/rehydrate Build that reads that capture, and the real checkpoint writer
// that seals the next artifact from the same L0 material.
//
// The mechanism under test is checkpoint.StripInjections, whose one production consumer is
// rehydrate.readL0Intent: material that arrived inside injection tags is removed before it can be
// re-injected, so a compaction never compresses a compression. The negative control is the same
// bytes with the tags removed — the nonce must then appear, which is what proves the tags, and not
// some incidental filter, are doing the work.
package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The two session identities: the tagged arm and its untagged negative control.
const (
	x8v4Session    = core.SessionID("sess-e2e-v4-x08")
	x8v4CtlSession = core.SessionID("sess-e2e-v4-x08-ctl")
)

// x8v4Nonce appears nowhere else in the tree. Every assertion below is about THIS string, never
// about a field being empty — an empty field would be satisfied by a rehydration that produced
// nothing at all.
const x8v4Nonce = "REINJECTED-NONCE-x08-4c71ab"

// x8v4Ask is the genuine part of the prompt: it must survive, so that "the nonce is gone" cannot
// be satisfied by the whole prompt being dropped.
const x8v4Ask = "fix the refresh handler returning 500 on the second call"

// x8v4Prompt is a first prompt carrying a previously injected span — the shape a transcript has
// after a compaction folded Qompack's own additionalContext into the next user message. tagged
// selects whether the span keeps its §4.6 tags.
func x8v4Prompt(tagged bool) string {
	body := "## 5. Current work\n" + x8v4Nonce + "\nrestored from a previous checkpoint"
	if !tagged {
		return x8v4Ask + "\n\n" + body
	}
	return x8v4Ask + "\n\n" +
		fmt.Sprintf(checkpoint.InjectionOpenTag, 1, 1) + "\n" +
		body + "\n" + checkpoint.InjectionCloseTag
}

// TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint is V4-VERIFY §4.8.
func TestV4_InjectionTaggingKeepsRehydratedMaterialOutOfTheNextCheckpoint(t *testing.T) {
	p := v4Project(t)
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	// Sanity on the fixture itself: the tagged and untagged prompts differ ONLY in the tags, and
	// both carry the nonce. Without this the two arms could differ for some other reason.
	tagged, untagged := x8v4Prompt(true), x8v4Prompt(false)
	require.Contains(t, tagged, x8v4Nonce)
	require.Contains(t, untagged, x8v4Nonce)
	require.Equal(t, untagged, checkpoint.StripInjections(strings.ReplaceAll(untagged, "\r\n", "\n")),
		"the control prompt must carry no injection span at all")

	// ── Arm 1: the tagged prompt ─────────────────────────────────────────────────────────────────
	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x8v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"}, obsPromptPayload(t, p.Root, x8v4Session, tagged), env)
	r.SeedTurns(t, x8v4Session, "v4x08", 3)
	r.ArmCheckpointSources(t, x8v4Session)

	ac := r.CompactStart(t, x8v4Session)
	require.NotEmpty(t, ac, "a compact SessionStart must inject a rehydrated context")
	require.Contains(t, ac, x8v4Ask,
		"the genuine part of the prompt must survive — otherwise 'the nonce is gone' would be "+
			"satisfied by the whole item being dropped: %s", ac)
	require.NotContains(t, ac, x8v4Nonce,
		"no byte of the previously injected span may be re-injected: the §4.6 tags are what "+
			"exclude it (checkpoint.StripInjections, rehydrate.readL0Intent): %s", ac)
	require.Contains(t, ac, checkpoint.InjectionCloseTag,
		"the payload the daemon emits is itself tagged, so the NEXT rehydration can strip it too")

	// The next checkpoint, sealed from the same L0 material after that rehydration.
	_, instr := r.PreCompact(t, x8v4Session)
	require.NotEmpty(t, instr)
	require.NotContains(t, instr, x8v4Nonce,
		"the emitted customInstructions must not carry the re-injected span either")

	artifacts := cpCheckpointArtifacts(t, p.Root)
	require.NotEmpty(t, artifacts, "the PreCompact must have sealed an artifact to inspect")
	for _, name := range artifacts {
		seq := core.CheckpointSeq(0)
		_, err := fmt.Sscanf(strings.TrimSuffix(name, ".json"), "%d", &seq)
		require.NoError(t, err)
		raw, err := os.ReadFile(paths.Long(paths.CheckpointPath(paths.Of(p.Root), seq)))
		require.NoError(t, err)
		require.NotContains(t, string(raw), x8v4Nonce,
			"no field of checkpoint %s may carry a byte of the re-injected span", name)
	}

	// ── Arm 2, the NEGATIVE CONTROL: the SAME bytes with the tags stripped before the observer ───
	//
	// A separate project, because the assertion is about what a rehydration of THIS prompt does and
	// the two sessions must not share an L0 capture.
	pc := v4Project(t)
	rc := v4StartRig(t, pc)
	envc := e2eEnv(pc)

	obsRunHook(t, rc.Bin, []string{"session-start"}, sessionStartFor(t, pc.Root, x8v4CtlSession), envc)
	obsRunHook(t, rc.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, pc.Root, x8v4CtlSession, untagged), envc)
	rc.SeedTurns(t, x8v4CtlSession, "v4x08c", 3)
	rc.ArmCheckpointSources(t, x8v4CtlSession)

	ctlAC := rc.CompactStart(t, x8v4CtlSession)
	require.NotEmpty(t, ctlAC)
	require.Contains(t, ctlAC, x8v4Nonce,
		"NEGATIVE CONTROL: with the tags removed the very same bytes MUST reach the rehydrated "+
			"payload. If they do not, arm 1 proves nothing about the tags: %s", ctlAC)
}
