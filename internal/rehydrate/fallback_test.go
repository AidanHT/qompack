package rehydrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

// F-C4-UAT03-1 (owner decision D49): with checkpoint 0002 corrupted by one byte, SessionStart(compact)
// built the payload from 0001 and presented it as current — "checkpoint 0001" in the header, no
// section 7, dropped [], degraded false. A fallback is never silent: the payload names it (which
// checkpoint was refused, what it was rebuilt from, what may be missing, how to restore), the result
// is degraded, and the drop report — and so dropped() — carries it.

// fallbackRequest is r rebuilt from checkpoint `from` after `refused` (newest first) did not verify.
func fallbackRequest(r Request, from core.CheckpointSeq, refused ...core.CheckpointSeq) Request {
	r.Ref.Seq = from
	r.Ref.Refused = refused
	return r
}

// fallbackEntry returns the drop entry naming the fallback.
func fallbackEntry(t *testing.T, drops []checkpoint.DropEntry) checkpoint.DropEntry {
	t.Helper()
	for _, e := range drops {
		if e.Kind == dropKindCheckpointFallback {
			return e
		}
	}
	require.Failf(t, "the fallback is not in the drop report", "%v", drops)
	return checkpoint.DropEntry{}
}

func TestBuild_CheckpointFallbackIsNamed(t *testing.T) {
	cp := ckFull(t)
	r := fallbackRequest(requestFor(t, cp, 0), 1, 2)

	res, err := Build(context.Background(), r, fullDeps(t, cp))
	require.NoError(t, err)
	require.True(t, res.Degraded, "a rehydration rebuilt from an older checkpoint is degraded")
	require.Equal(t, "checkpoint 0002 does not verify; rolled back to 0001", res.DegradedReason)

	header := strings.SplitN(res.Text, "\n", 3)[1]
	require.Contains(t, header, "checkpoint 0001")
	require.Contains(t, header, "rolled back from 0002",
		"the header must not present the older checkpoint as the current one: %q", header)

	e := fallbackEntry(t, res.Dropped)
	require.Equal(t, "0002", e.ID)
	for _, want := range []string{
		"checkpoint 0002 does not verify", // which one was refused
		"rebuilt from checkpoint 0001",    // what it was rebuilt from
		"may be missing",                  // what may be missing
		"restore: ",                       // how to restore
		"timeline()", "qompack backup restore",
	} {
		require.Contains(t, e.Detail, want)
	}
	lines := strings.Split(sectionBody(res.Text, sectionHeading(ItemDropReport)), "\n")
	require.Equal(t, dropLine(e), lines[0]+"\n",
		"the fallback is the first line of section 7, where a counted tail cannot swallow it")
	requireInsideTheHostCeiling(t, res, cp.Session)
}

// TestBuild_CheckpointFallbackSurvivesATinyBudget: at a budget that leaves section 7 only its counted
// tail, the fallback is still what the report names first, and Result.Dropped (what dropped() reads)
// always has it.
func TestBuild_CheckpointFallbackSurvivesATinyBudget(t *testing.T) {
	cp := ckFull(t)
	r := fallbackRequest(requestFor(t, cp, degradedBudget), 1, 3, 2)

	res, err := Build(context.Background(), r, fullDeps(t, cp))
	require.NoError(t, err)
	require.True(t, res.Degraded)
	e := fallbackEntry(t, res.Dropped)
	require.Equal(t, "0003", e.ID, "the entry is keyed by the newest refused checkpoint")
	require.Contains(t, e.Detail, "0003 and 0002 do not verify")
	require.Equal(t, dropKindCheckpointFallback, buildDropReport(res.Dropped).units[0].text[2:2+len(dropKindCheckpointFallback)],
		"the fallback sorts first in the report")
}

// TestBuild_FallbackToNoCheckpointIsNamed: when no recorded checkpoint verifies, the rehydration is
// rebuilt without one — from L0 and the ledger — and says so rather than reading as a project that
// never had a checkpoint.
func TestBuild_FallbackToNoCheckpointIsNamed(t *testing.T) {
	cp := ckEmpty()
	r := requestFor(t, cp, 0)
	r.Checkpoint = checkpoint.Checkpoint{}
	r.Ref = checkpoint.Ref{}
	r = fallbackRequest(r, 0, 1)
	d := fullDeps(t, cp)
	d.Store = newFakeStore().withPrompt(firstPromptID(r.Session), r.Session, 0, "Fix the webhook retries.")

	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	require.True(t, res.Degraded)
	e := fallbackEntry(t, res.Dropped)
	require.Equal(t, "0001", e.ID)
	require.Contains(t, e.Detail, "checkpoint 0001 does not verify")
	require.Contains(t, e.Detail, "rebuilt without a checkpoint")
	require.Contains(t, res.Text, dropLine(e))
}

// TestBuild_NoFallbackNoEntry: the ordinary build carries no fallback line and its header is
// unchanged, so the goldens and every payload without a fallback stay byte-identical.
func TestBuild_NoFallbackNoEntry(t *testing.T) {
	cp := ckFull(t)
	res, err := Build(context.Background(), requestFor(t, cp, 0), fullDeps(t, cp))
	require.NoError(t, err)
	for _, e := range res.Dropped {
		require.NotEqual(t, dropKindCheckpointFallback, e.Kind)
	}
	require.Contains(t, res.Text, "\n# Qompack rehydration — checkpoint 0001, session ")
}
