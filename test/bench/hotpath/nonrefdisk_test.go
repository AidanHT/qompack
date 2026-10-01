package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/obs"
)

// TestParseFlags_NonReferenceDiskIsHonouredOnlyOnGitHubActions pins where the harness takes the
// non-reference-disk declaration from (the environment, obs.NonReferenceDisk) and that a run
// outside GitHub Actions which sets it still gates every row, and is marked as having ignored it.
func TestParseFlags_NonReferenceDiskIsHonouredOnlyOnGitHubActions(t *testing.T) {
	for _, c := range []struct {
		declared, actions   string
		honoured, ignored   bool
		waivesWithoutCoload bool
	}{
		{declared: "", actions: "", honoured: false, ignored: false},
		{declared: "", actions: "true", honoured: false, ignored: false},
		{declared: "1", actions: "", honoured: false, ignored: true},
		{declared: "1", actions: "false", honoured: false, ignored: true},
		{declared: "1", actions: "true", honoured: true, ignored: false, waivesWithoutCoload: true},
	} {
		t.Setenv(obs.NonReferenceDiskEnv, c.declared)
		t.Setenv(obs.GitHubActionsEnv, c.actions)
		f, err := parseFlags(nil, io.Discard)
		require.NoError(t, err)
		what := obs.NonReferenceDiskEnv + "=" + c.declared + " " + obs.GitHubActionsEnv + "=" + c.actions
		require.Equal(t, c.honoured, f.nonrefDisk, what)
		require.Equal(t, c.ignored, f.nonrefDiskIgnored, what)
		require.Equal(t, c.waivesWithoutCoload, f.waiver().waives(), what)
	}
}

// TestBuildDaemonRows_NonReferenceDiskReportsBothDaemonRowsWithItsOwnReason pins that the
// non-reference-disk declaration reports B-A and B-B exactly as --under-coload does (limit_ms and
// pass null, the number the gate would have read still in p99), and that the note on each row names
// THIS declaration, not co-load: a reader of a hosted artifact must see which reason applied.
func TestBuildDaemonRows_NonReferenceDiskReportsBothDaemonRowsWithItsOwnReason(t *testing.T) {
	cfg := config.Defaults()
	snap := obs.HistSnapshot{
		N: 2064, P50: 45 * time.Millisecond, P95: 80 * time.Millisecond, P99: 106 * time.Millisecond,
		P999: 180 * time.Millisecond, Max: 883 * time.Millisecond,
	}
	ba, bb, notes := buildDaemonRows(cfg, snap, snap, 0, 0, wallWaiver{nonrefDisk: true})
	require.Nil(t, ba.LimitMs)
	require.Nil(t, ba.Pass)
	require.Nil(t, bb.LimitMs)
	require.Nil(t, bb.Pass)
	require.InDelta(t, 106.0, bb.P99, 0.001, "the reported row still carries the number the gate would have read")
	require.Equal(t, []string{
		"", daemonRowNonrefDiskNote(obs.BA, budgetLimit(cfg, obs.BA)),
		"", daemonRowNonrefDiskNote(obs.BB, budgetLimit(cfg, obs.BB)),
	}, notes, "one non-reference-disk note per daemon row, in row order, and no co-load note")
	require.Contains(t, notes[1], string(obs.BA)+"'s row is "+nonrefDiskReportedPhrase)
	require.Contains(t, notes[3], string(obs.BB)+"'s row is "+nonrefDiskReportedPhrase)
	for _, n := range notes {
		require.NotContains(t, n, "--under-coload", "a non-reference-disk run must not name co-load as its reason")
		require.NotContains(t, n, budgetIDBECPU, "only B-E's waiver may name the row that still enforces its limit")
	}

	// Both declarations: each reason writes its own note.
	_, _, notes = buildDaemonRows(cfg, snap, snap, 0, 0, wallWaiver{coload: true, nonrefDisk: true})
	require.Len(t, notes, 6)
	require.Contains(t, notes[1], "--under-coload")
	require.Contains(t, notes[2], nonrefDiskReportedPhrase)
}

// TestBEWallNonrefDiskNote_NamesTheLimitAndTheRowThatStillEnforcesIt pins B-E's disclosure under
// the declaration: the reason, the limit not applied to the wall row, and B-E_cpu, the row that
// still enforces that limit in the same run.
func TestBEWallNonrefDiskNote_NamesTheLimitAndTheRowThatStillEnforcesIt(t *testing.T) {
	limit := budgetLimit(config.Defaults(), obs.BE)
	note := beWallNonrefDiskNote(limit)
	require.True(t, strings.HasPrefix(note, string(obs.BE)+"'s wall-clock row is "+nonrefDiskReportedPhrase), note)
	require.Contains(t, note, budgetIDBECPU)
	require.Contains(t, note, obs.GitHubActionsEnv+"=true")
	require.Contains(t, note, "Q1")
	require.NotContains(t, note, "--under-coload")

	ignored := nonrefDiskIgnoredNote()
	require.Contains(t, ignored, obs.NonReferenceDiskEnv+" is set but IGNORED")
	require.NotContains(t, ignored, nonrefDiskReportedPhrase, "an ignored declaration reports nothing")
}
