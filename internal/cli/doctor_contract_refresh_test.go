package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
)

// doctor's capability rows read the observation ledger's newest entry per capability, and the ledger
// is written at SessionStart, before the probe the start minted reaches the transcript. The candidate
// 4 live re-run's UAT-01 doctor read injection's newest observation as not_observed while
// state/history.json recorded the sentinel observed (D50: status and doctor read what history.json
// already knows).

// seedStartLedger writes the ledger a first session's start leaves for the injection capability,
// and a history that records the probe observed or not.
func seedStartLedger(t *testing.T, root string, sentinelObserved bool) {
	t.Helper()
	target := contract.Target{Provider: "claude-code", Platform: "windows/amd64", Date: "2026-09-30"}
	led := &contract.ObservationLedger{Target: target}
	led.Append(contract.ObservationsOf([]contract.Result{{
		ID: contract.CAdditionalContext, OK: true, Expected: "additionalContext reaches the transcript",
		Observed: "not-yet-observed", TS: 1790729049437,
	}}, contract.DefaultCapabilityRegister(), target, "sess-doctor-refresh")...)
	require.NoError(t, contract.SaveObservationLedger(contract.ObservationLedgerPath(root), led))

	h := &contract.SessionHistory{SessionCount: 1}
	h.Sentinel.Token = "qompack-contract-doctor"
	h.Sentinel.Observed = sentinelObserved
	require.NoError(t, contract.SaveHistory(contract.HistoryPath(root), h))
}

// TestDoctor_AnObservedProbeRefreshesTheInjectionRow: the ledger's newest injection entry is the
// start's not_observed and history.json records the probe observed, so doctor reports the
// observation, with the delivery coverage an observed injection supports.
func TestDoctor_AnObservedProbeRefreshesTheInjectionRow(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	seedStartLedger(t, p.Root, true)
	_, doc, errw := doctorJSON(t, p.Root)

	row := doctorFindRow(t, doc, "capabilities", string(contract.CapInjection))
	detail, _ := row["detail"].(string)
	require.Contains(t, detail, "newest observation: outcome observed", "stderr=%s row=%v", errw, row)
	require.Contains(t, detail, `scope "sess-doctor-refresh"`, "row=%v", row)
	require.Contains(t, detail, "state/history.json", "the row says where the observation was read: %v", row)
	require.Equal(t, contract.CoverageDeliveryUnderTestedContract, row["coverage"], "row=%v", row)
}

// TestDoctor_AnUnobservedProbeKeepsTheLedgersWord: with no observation in history.json the row
// reports the ledger's not_observed as it always did.
func TestDoctor_AnUnobservedProbeKeepsTheLedgersWord(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	seedStartLedger(t, p.Root, false)
	_, doc, errw := doctorJSON(t, p.Root)

	row := doctorFindRow(t, doc, "capabilities", string(contract.CapInjection))
	detail, _ := row["detail"].(string)
	require.Contains(t, detail, "newest observation: outcome not_observed", "stderr=%s row=%v", errw, row)
	require.Equal(t, contract.CoverageNone, row["coverage"], "row=%v", row)
}

// TestDoctor_SpentProbeChancesReadAsAFailedInjection: history.json records the probe missed twice,
// so the injection row reports the failure the next start will record, read from history.json.
func TestDoctor_SpentProbeChancesReadAsAFailedInjection(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	seedStartLedger(t, p.Root, false)
	h := contract.LoadHistory(contract.HistoryPath(p.Root))
	h.Sentinel.Chances = 2
	require.NoError(t, contract.SaveHistory(contract.HistoryPath(p.Root), h))
	_, doc, errw := doctorJSON(t, p.Root)

	row := doctorFindRow(t, doc, "capabilities", string(contract.CapInjection))
	detail, _ := row["detail"].(string)
	require.Contains(t, detail, "newest observation: outcome failed", "stderr=%s row=%v", errw, row)
	require.Contains(t, detail, "state/history.json", "row=%v", row)
	require.Equal(t, contract.CoverageNone, row["coverage"], "row=%v", row)
}
