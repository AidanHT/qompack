package contract_test

import (
	"context"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// TestStandardAssertions_MatchTheNormativeTable pins the nine assertions of
// 00-ARCHITECTURE.md §5.19 / §12.1: their order, their IDs, their DECLARED severities and their
// descriptions. SP-05 replaces each Check; it may not renumber, reorder, re-describe or
// re-grade the set without amending §5.19.
func TestStandardAssertions_MatchTheNormativeTable(t *testing.T) {
	want := []struct {
		id       contract.ID
		severity contract.Severity
		desc     string
	}{
		{contract.CSessionStartFires, contract.SevCritical, "SessionStart hook fires"},
		{contract.CSessionStartSourceCompact, contract.SevCritical, "SessionStart arrives with source=compact after PreCompact"},
		{contract.CAdditionalContext, contract.SevCritical, "additionalContext reaches the transcript"},
		{contract.CPreCompactTiming, contract.SevWarn, "PreCompact has time to write"},
		{contract.CPreCompactCustomInstr, contract.SevWarn, "custom_instructions accepted"},
		{contract.CHookPayloadShape, contract.SevCritical, "hook payload shape matches hookio.Event"},
		{contract.CMCPRegistered, contract.SevInfo, "MCP server received initialize"},
		{contract.CTranscriptReadable, contract.SevWarn, "transcript_path exists and parses"},
		{contract.CPluginRootResolves, contract.SevWarn, "CLAUDE_PLUGIN_ROOT expands to an existing binary"},
	}

	got := contract.StandardAssertions()
	require.Len(t, got, len(want))
	for i, w := range want {
		require.Equal(t, w.id, got[i].ID, "assertion %d has the wrong ID", i)
		require.Equal(t, w.severity, got[i].Severity, "assertion %s declares the wrong severity", w.id)
		require.Equal(t, w.desc, got[i].Description, "assertion %s has the wrong description", w.id)
		require.NotNil(t, got[i].Check, "assertion %s has no Check", w.id)
	}
}

// TestStandardAssertions_IDsAreUnique guards against a copy-paste that would silently drop an
// assertion: RunAll executes whatever is registered, so two entries sharing an ID would report
// twice for one contract and never for another.
func TestStandardAssertions_IDsAreUnique(t *testing.T) {
	seen := make(map[contract.ID]bool)
	for _, a := range contract.StandardAssertions() {
		require.False(t, seen[a.ID], "duplicate assertion ID %s", a.ID)
		seen[a.ID] = true
	}
	require.Len(t, seen, 9)
}

// TestStandardAssertions_EveryCheckReportsNotYetImplemented is the §12.1 rule stated directly: an
// assertion whose producer is absent from the build reports OK: true, Severity: SevInfo and
// Observed: "not-yet-implemented" — regardless of the severity it DECLARES. This is what makes a
// fresh build ModeFull, and it is the property the CI guard depends on.
//
// This test is expected to be EDITED, not deleted, as SP-05 lands real observations: an assertion
// whose producer has arrived reports a real result, and the loop below must then exclude it by
// name. That edit is the point — it forces each implemented assertion to be an explicit decision.
// The rule itself, "a result reporting OK/SevInfo never degrades the session", is asserted
// implementation-independently in contracttest and stays true forever.
func TestStandardAssertions_EveryCheckReportsNotYetImplemented(t *testing.T) {
	clk := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	env := contract.Env{Clock: clk}

	for _, a := range contract.StandardAssertions() {
		r := a.Check(context.Background(), env)
		require.Equal(t, a.ID, r.ID)
		require.True(t, r.OK, "assertion %s must report OK while its producer is absent", a.ID)
		require.Equal(t, contract.SevInfo, r.Severity,
			"assertion %s declares %d but must OBSERVE SevInfo while its producer is absent", a.ID, a.Severity)
		require.Equal(t, "not-yet-implemented", r.Observed)
		require.Equal(t, a.Description, r.Expected)
		require.Equal(t, core.NowMilli(clk), r.TS, "the result must be stamped from Env.Clock, not from wall time")
	}
}

// TestStandardAssertions_DeclaredSeveritiesAreNotFlattened is the other half of the previous test,
// and the reason both exist: the DECLARED severities must survive on the Assertion even though
// every Result currently reports SevInfo (because no producer is declared in this test binary — see
// gated in assertions.go). If a future edit "simplified" gated by declaring SevInfo too, the
// not-yet-implemented rule would still hold and this repository would have quietly lost the record
// of which contracts actually matter.
func TestStandardAssertions_DeclaredSeveritiesAreNotFlattened(t *testing.T) {
	var critical, warn, info int
	for _, a := range contract.StandardAssertions() {
		switch a.Severity {
		case contract.SevCritical:
			critical++
		case contract.SevWarn:
			warn++
		case contract.SevInfo:
			info++
		}
	}
	require.Equal(t, 4, critical)
	require.Equal(t, 4, warn)
	require.Equal(t, 1, info)
}
