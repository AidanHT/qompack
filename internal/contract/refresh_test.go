package contract_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
)

// handshakeAt and scannedAt are the times a fixture history records its MCP handshake and its probe
// scan at. Both are past startSnapshot's TS (1) and before newFakeClock's now, so a row dated by the
// observation, by the start or by the read can each be told apart.
const (
	handshakeAt core.UnixMilli = 1_000
	scannedAt   core.UnixMilli = 2_000
)

// A SessionStart's RunAll runs before the host connects the MCP server or writes the probe into the
// transcript, so its snapshot reads both pending for the rest of the session unless something
// refreshes it (candidate 4 re-run, UAT-01; D50). These rows pin the refresh and the banner's
// counting rule.

// declareAll declares every standard producer, as a full daemon build does, and undoes it after.
func declareAll(t *testing.T) {
	t.Helper()
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	for _, a := range contract.StandardAssertions() {
		contract.DeclareProducer(a.ID)
	}
}

// startSnapshot is the rows a first session's start reads before either late observable arrived.
func startSnapshot() []contract.Result {
	return []contract.Result{
		{ID: contract.CSessionStartFires, OK: true, Expected: "SessionStart hook fires", Observed: "first-session", TS: 1},
		{ID: contract.CAdditionalContext, OK: true, Expected: "additionalContext reaches the transcript", Observed: "not-yet-observed", TS: 1},
		{ID: contract.CMCPRegistered, OK: true, Expected: "MCP server received initialize", Observed: "initialize-pending", TS: 1},
		{ID: contract.CTranscriptReadable, OK: true, Expected: "transcript_path exists and parses", Observed: "transcript-pending", TS: 1},
	}
}

// rowOf returns the row for id.
func rowOf(t *testing.T, rs []contract.Result, id contract.ID) contract.Result {
	t.Helper()
	for _, r := range rs {
		if r.ID == id {
			return r
		}
	}
	require.FailNow(t, "no row for "+string(id))
	return contract.Result{}
}

// TestRefreshFromHistory_AnObservedHandshakeAndSentinelReplaceTheirPendingRows is UAT-01's reading:
// history.json records the handshake and the probe, so the two rows read what their checks would
// read from it now, dated by when history.json recorded each observation (D53(a): never by the read,
// which this row once required and which made two reads of unchanged state differ).
func TestRefreshFromHistory_AnObservedHandshakeAndSentinelReplaceTheirPendingRows(t *testing.T) {
	declareAll(t)
	clk := newFakeClock()
	h := &contract.SessionHistory{MCPInitialized: true, MCPInitializedAt: handshakeAt}
	h.Sentinel.Observed = true
	h.Sentinel.ScannedAt = scannedAt

	before := startSnapshot()
	got := contract.RefreshFromHistory(before, h, clk)

	mcpRow := rowOf(t, got, contract.CMCPRegistered)
	require.True(t, mcpRow.OK)
	require.Equal(t, "initialize-received", mcpRow.Observed)
	require.Equal(t, "MCP server received initialize", mcpRow.Expected)
	require.Equal(t, handshakeAt, mcpRow.TS, "the refreshed row is dated by the handshake history.json recorded")

	probeRow := rowOf(t, got, contract.CAdditionalContext)
	require.True(t, probeRow.OK)
	require.Equal(t, "sentinel-observed", probeRow.Observed)
	require.Equal(t, scannedAt, probeRow.TS, "the refreshed row is dated by the scan that found the probe")

	require.Equal(t, "first-session", rowOf(t, got, contract.CSessionStartFires).Observed)
	require.Equal(t, "transcript-pending", rowOf(t, got, contract.CTranscriptReadable).Observed,
		"history.json records nothing about the transcript, so its row is left as the start read it")
	require.Equal(t, "initialize-pending", rowOf(t, before, contract.CMCPRegistered).Observed,
		"the caller's slice is not modified")
}

// TestRefreshFromHistory_NothingRecordedLeavesThePendingRows: a history that records neither
// observation changes nothing.
func TestRefreshFromHistory_NothingRecordedLeavesThePendingRows(t *testing.T) {
	declareAll(t)
	got := contract.RefreshFromHistory(startSnapshot(), &contract.SessionHistory{}, newFakeClock())
	require.Equal(t, startSnapshot(), got)
}

// TestRefreshFromHistory_NeverRewritesAFailingRow: a refresh only turns a row that was waiting into
// an observation. A failure the start reported stays reported until the next start re-evaluates it.
func TestRefreshFromHistory_NeverRewritesAFailingRow(t *testing.T) {
	declareAll(t)
	h := &contract.SessionHistory{MCPInitialized: true}
	failing := []contract.Result{{
		ID: contract.CMCPRegistered, OK: false, Severity: contract.SevInfo,
		Expected: "MCP server received initialize", Observed: "initialize-not-received", TS: 1,
	}}
	require.Equal(t, failing, contract.RefreshFromHistory(failing, h, newFakeClock()))
}

// TestRefreshFromHistory_AnUndeclaredProducerIsLeftAlone: a row this build never declared a producer
// for reads not-yet-implemented, and a history record does not declare one.
func TestRefreshFromHistory_AnUndeclaredProducerIsLeftAlone(t *testing.T) {
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	h := &contract.SessionHistory{MCPInitialized: true}
	rows := []contract.Result{{
		ID: contract.CMCPRegistered, OK: true, Severity: contract.SevInfo,
		Expected: "MCP server received initialize", Observed: "not-yet-implemented", TS: 1,
	}}
	require.Equal(t, rows, contract.RefreshFromHistory(rows, h, newFakeClock()))
}

// TestRefreshFromHistory_MatchesWhatTheChecksRead: the rows a refresh substitutes are what the real
// checks report from the same history, so the two can never spell an observation differently. The
// one field that differs is TS, by design (D53(a)): a check run at a start is dated by that
// evaluation, and a refreshed row by the observation history.json recorded.
func TestRefreshFromHistory_MatchesWhatTheChecksRead(t *testing.T) {
	declareAll(t)
	clk := newFakeClock()
	h := &contract.SessionHistory{MCPInitialized: true, MCPInitializedAt: handshakeAt}
	h.Sentinel.Observed = true
	h.Sentinel.ScannedAt = scannedAt
	recorded := map[contract.ID]core.UnixMilli{contract.CMCPRegistered: handshakeAt, contract.CAdditionalContext: scannedAt}

	got := contract.RefreshFromHistory(startSnapshot(), h, clk)
	for _, a := range contract.StandardAssertions() {
		if a.ID != contract.CMCPRegistered && a.ID != contract.CAdditionalContext {
			continue
		}
		want := a.Check(t.Context(), contract.Env{Clock: clk, History: h})
		want.TS = recorded[a.ID]
		row := rowOf(t, got, a.ID)
		require.Equal(t, want, row, "%s", a.ID)
		require.Equal(t, contract.StandingHolding, contract.StandingOf(row), "%s", a.ID)
	}
}

// ledgerEntry is the not_observed entry a first session's start leaves in the observation ledger
// for id, as ObservationsOf writes it.
func ledgerEntry(t *testing.T, id contract.ID, observed string) contract.Observation {
	t.Helper()
	target := contract.Target{Provider: "claude-code", Platform: "windows/amd64", Date: "2026-09-30"}
	o := contract.ObservationsOf([]contract.Result{{ID: id, OK: true, Observed: observed, TS: 1}},
		contract.DefaultCapabilityRegister(), target, "sess-ledger")[0]
	require.Equal(t, contract.OutcomeNotObserved, o.Outcome, "fixture")
	return o
}

// TestRefreshObservation_AnObservedProbeIsTheInjectionCapabilitysNewestWord is doctor's half of
// UAT-01: the ledger's newest injection entry is the start's not_observed, history.json records the
// probe observed, and doctor reads the observation — with the narrow delivery coverage only an
// observed injection may claim — under the same target and scope.
func TestRefreshObservation_AnObservedProbeIsTheInjectionCapabilitysNewestWord(t *testing.T) {
	t.Parallel()

	stale := ledgerEntry(t, contract.CAdditionalContext, "not-yet-observed")
	h := &contract.SessionHistory{}
	h.Sentinel.Observed = true
	h.Sentinel.ScannedAt = scannedAt

	got, changed := contract.RefreshObservation(stale, h, contract.DefaultCapabilityRegister(), newFakeClock())
	require.True(t, changed)
	require.Equal(t, contract.OutcomeObserved, got.Outcome)
	require.Equal(t, "sentinel-observed", got.Observed)
	require.Equal(t, contract.CoverageDeliveryUnderTestedContract, got.Coverage)
	require.Equal(t, contract.CapInjection, got.Capability)
	require.Equal(t, stale.Target, got.Target)
	require.Equal(t, stale.Scope, got.Scope)
	require.Equal(t, scannedAt, got.TS, "the refreshed entry is dated by the scan that found the probe (D53(a))")
}

// TestRefreshObservation_AnObservedHandshakeRefreshesItsEntry: the same for mcp.server_registered.
func TestRefreshObservation_AnObservedHandshakeRefreshesItsEntry(t *testing.T) {
	t.Parallel()

	stale := ledgerEntry(t, contract.CMCPRegistered, "initialize-pending")
	got, changed := contract.RefreshObservation(stale, &contract.SessionHistory{MCPInitialized: true},
		contract.DefaultCapabilityRegister(), newFakeClock())
	require.True(t, changed)
	require.Equal(t, contract.OutcomeObserved, got.Outcome)
	require.Equal(t, "initialize-received", got.Observed)
	require.Equal(t, contract.CoverageNone, got.Coverage, "a handshake supports no coverage claim")
}

// TestRefreshObservation_LeavesEverythingElse: an entry history does not prove, an entry that is not
// a pending not_observed one, and a failed entry are returned unchanged.
func TestRefreshObservation_LeavesEverythingElse(t *testing.T) {
	t.Parallel()

	reg := contract.DefaultCapabilityRegister()
	proves := &contract.SessionHistory{MCPInitialized: true}
	proves.Sentinel.Observed = true

	pending := ledgerEntry(t, contract.CAdditionalContext, "not-yet-observed")
	got, changed := contract.RefreshObservation(pending, &contract.SessionHistory{}, reg, newFakeClock())
	require.False(t, changed, "history records nothing")
	require.Equal(t, pending, got)

	idle := ledgerEntry(t, contract.CSessionStartFires, "first-session")
	got, changed = contract.RefreshObservation(idle, proves, reg, newFakeClock())
	require.False(t, changed, "not a history-proven assertion, and not pending")
	require.Equal(t, idle, got)

	failed := contract.ObservationsOf([]contract.Result{{
		ID: contract.CMCPRegistered, OK: false, Observed: "initialize-not-received", TS: 1,
	}}, reg, contract.Target{}, "sess-ledger")[0]
	got, changed = contract.RefreshObservation(failed, proves, reg, newFakeClock())
	require.False(t, changed, "a failure is never rewritten")
	require.Equal(t, failed, got)
}

// TestStandingOf_CountsOnlyObservationsAsHolding is the banner's rule (D50): a row still waiting for
// its observation is pending, a row with nothing to judge is neither pending nor holding, and only a
// real observation holds.
func TestStandingOf_CountsOnlyObservationsAsHolding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		r    contract.Result
		want contract.Standing
	}{
		{contract.Result{OK: true, Observed: "not-yet-observed"}, contract.StandingPending},
		{contract.Result{OK: true, Observed: "initialize-pending"}, contract.StandingPending},
		{contract.Result{OK: true, Observed: "transcript-pending"}, contract.StandingPending},
		{contract.Result{OK: true, Observed: "marker-absent-once"}, contract.StandingPending},
		{contract.Result{OK: true, Observed: "first-session"}, contract.StandingIdle},
		{contract.Result{OK: true, Observed: "retired"}, contract.StandingIdle},
		{contract.Result{OK: true, Observed: "unset"}, contract.StandingIdle},
		{contract.Result{OK: true, Observed: "no observation yet"}, contract.StandingIdle},
		{contract.Result{OK: true, Severity: contract.SevInfo, Observed: "not-yet-implemented"}, contract.StandingIdle},
		{contract.Result{OK: true, Observed: "sentinel-observed"}, contract.StandingHolding},
		{contract.Result{OK: true, Observed: "initialize-received"}, contract.StandingHolding},
		{contract.Result{OK: true, Observed: "same-session-restart"}, contract.StandingHolding},
		{contract.Result{OK: false, Observed: "initialize-not-received"}, contract.StandingFailing},
		{contract.Result{OK: false, Observed: "not-yet-observed"}, contract.StandingFailing},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, contract.StandingOf(tc.r), "%+v", tc.r)
	}
}

// TestRefreshFromHistory_SpentChancesTurnThePendingProbeRowFailing: history.json can also record the
// probe's failure. Once the session's prompts have missed it twice, the check reads "not found after
// two chances" at critical severity, and a status page that kept calling the row pending would
// contradict what history.json already knows. A single miss is still pending.
func TestRefreshFromHistory_SpentChancesTurnThePendingProbeRowFailing(t *testing.T) {
	declareAll(t)

	once := &contract.SessionHistory{}
	once.Sentinel.Chances = 1
	require.Equal(t, startSnapshot(), contract.RefreshFromHistory(startSnapshot(), once, newFakeClock()),
		"one missed chance is still a row waiting for its observation")

	spent := &contract.SessionHistory{}
	spent.Sentinel.Chances = 2
	spent.Sentinel.ScannedAt = scannedAt
	got := contract.RefreshFromHistory(startSnapshot(), spent, newFakeClock())

	row := rowOf(t, got, contract.CAdditionalContext)
	require.False(t, row.OK)
	require.Equal(t, contract.StandingFailing, contract.StandingOf(row))
	require.Equal(t, "sentinel not found after two chances", row.Observed)
	require.Equal(t, contract.SevCritical, row.Severity, "the assertion's declared severity, as a start reports it")
	require.Equal(t, "additionalContext reaches the transcript", row.Expected)
	require.Equal(t, scannedAt, row.TS, "the refreshed row is dated by the miss that spent the last chance (D53(a))")
	require.Contains(t, row.Detail, "state/history.json", "the row says where the failure was read")
	require.Contains(t, row.Detail, "next SessionStart", "the row says the mode is not changed by the read")
	require.Equal(t, "initialize-pending", rowOf(t, got, contract.CMCPRegistered).Observed,
		"a history without the handshake leaves the MCP row pending")
}

// TestRefreshObservation_SpentChancesReadAsAFailedInjection is the same for doctor: a pending
// injection entry whose probe history.json records as missed twice reads as a failed observation,
// which supports no coverage claim.
func TestRefreshObservation_SpentChancesReadAsAFailedInjection(t *testing.T) {
	t.Parallel()

	stale := ledgerEntry(t, contract.CAdditionalContext, "not-yet-observed")
	h := &contract.SessionHistory{}
	h.Sentinel.Chances = 2

	got, changed := contract.RefreshObservation(stale, h, contract.DefaultCapabilityRegister(), newFakeClock())
	require.True(t, changed)
	require.Equal(t, contract.OutcomeFailed, got.Outcome)
	require.Equal(t, "sentinel not found after two chances", got.Observed)
	require.Equal(t, contract.CoverageNone, got.Coverage)
	require.Equal(t, stale.Target, got.Target)
	require.Equal(t, stale.Scope, got.Scope)
}

// TestRefreshFromHistory_AReadNeverRedatesARow: two refreshes of the same history at different
// times return equal rows (D53(a); candidate 5's X01 live row read hook.additional_context_delivered
// twice with nothing changed and got two TS values). A history that recorded no time for an
// observation (one an older build wrote) dates the refreshed row by the start row it replaces.
func TestRefreshFromHistory_AReadNeverRedatesARow(t *testing.T) {
	declareAll(t)

	stamped := &contract.SessionHistory{MCPInitialized: true, MCPInitializedAt: handshakeAt}
	stamped.Sentinel.Chances = 2
	stamped.Sentinel.ScannedAt = scannedAt
	unstamped := &contract.SessionHistory{MCPInitialized: true}
	unstamped.Sentinel.Observed = true

	early := newFakeClock()
	late := &fakeClock{now: early.now.Add(time.Hour)}
	for name, h := range map[string]*contract.SessionHistory{"stamped": stamped, "unstamped": unstamped} {
		first := contract.RefreshFromHistory(startSnapshot(), h, early)
		second := contract.RefreshFromHistory(startSnapshot(), h, late)
		require.Equal(t, first, second, "%s: a read an hour later must not change any row", name)
		for _, id := range []contract.ID{contract.CMCPRegistered, contract.CAdditionalContext} {
			require.NotEqual(t, contract.StandingPending, contract.StandingOf(rowOf(t, first, id)), "%s %s: fixture", name, id)
		}
	}
	got := contract.RefreshFromHistory(startSnapshot(), unstamped, late)
	require.Equal(t, rowOf(t, startSnapshot(), contract.CMCPRegistered).TS, rowOf(t, got, contract.CMCPRegistered).TS,
		"no recorded time: the row keeps the time of the start's evaluation it refreshes")
	require.Equal(t, rowOf(t, startSnapshot(), contract.CAdditionalContext).TS, rowOf(t, got, contract.CAdditionalContext).TS)
}

// TestRefreshObservation_AReadNeverRedatesAnEntry is the same for doctor's ledger entry.
func TestRefreshObservation_AReadNeverRedatesAnEntry(t *testing.T) {
	t.Parallel()

	reg := contract.DefaultCapabilityRegister()
	stale := ledgerEntry(t, contract.CAdditionalContext, "not-yet-observed")
	early := newFakeClock()
	late := &fakeClock{now: early.now.Add(time.Hour)}

	unstamped := &contract.SessionHistory{}
	unstamped.Sentinel.Observed = true
	first, changed := contract.RefreshObservation(stale, unstamped, reg, early)
	require.True(t, changed)
	second, _ := contract.RefreshObservation(stale, unstamped, reg, late)
	require.Equal(t, first, second)
	require.Equal(t, stale.TS, first.TS, "no recorded time: the entry keeps its own")

	stamped := &contract.SessionHistory{}
	stamped.Sentinel.Chances = 2
	stamped.Sentinel.ScannedAt = scannedAt
	first, changed = contract.RefreshObservation(stale, stamped, reg, early)
	require.True(t, changed)
	second, _ = contract.RefreshObservation(stale, stamped, reg, late)
	require.Equal(t, first, second)
	require.Equal(t, scannedAt, first.TS)
}
