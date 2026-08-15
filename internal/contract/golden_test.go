package contract_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// resultSetGolden is the frozen §16 format fixture: the nine §5.19 assertions as RunAll reports
// them on a build where every producer is still absent.
const resultSetGolden = "../../testdata/golden/contracts/contract/want/result_set.json"

// resultSetClock is the instant the fixture is frozen at. It sits inside the window the rest of the
// contract corpus shares — the same session, just after the manifest entry at 1767225500000 — so
// the fixtures read as one coherent scenario rather than nine unrelated timestamps.
var resultSetClock = &fakeClock{now: time.UnixMilli(1767225510000).UTC()}

// TestResultSet_MatchesFrozenGolden pins the on-disk shape of state/contract.json's results array.
//
// §16 lists the contract result set among the format fixtures that are authorable and frozen at
// SP-01, and it had no fixture: the manifest declared only a behaviour fixture deferred to SP-05.
// That left the one artifact two other subsystems read — /qompack:status renders it, and the next
// SessionStart loads it — with no frozen shape at all, so any wave could have respelled a JSON tag
// without anything noticing.
//
// The assertion is byte-level against the committed golden rather than field-by-field, because the
// bytes are the interface here. A field renamed, dropped, or newly emitted all change them, and all
// three are breaking changes to a file other components parse.
func TestResultSet_MatchesFrozenGolden(t *testing.T) {
	t.Parallel()

	m := contract.NewMonitor(logging.Nop(), obs.New(resultSetClock), filepath.Join(t.TempDir(), "contract.json"))
	for _, a := range contract.StandardAssertions() {
		require.NoError(t, m.Register(a))
	}

	results, mode := m.RunAll(context.Background(), contract.Env{Clock: resultSetClock})

	// §12.1: a build whose producers are all absent is ModeFull, never degraded. If this ever
	// flips, the golden below is the least of the problems — every wave-1 and wave-2 verification
	// run would be silently disabling the paths it is meant to be testing.
	require.Equal(t, contract.ModeFull, mode,
		"a fresh build reports every assertion OK/SevInfo, so the monitor must stay in full mode")
	require.Len(t, results, 9, "§5.19 fixes the assertion set at nine")

	got, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')

	want, err := os.ReadFile(resultSetGolden)
	require.NoError(t, err, "the frozen result-set fixture is missing")

	require.Equal(t, string(want), string(got),
		"the contract result set drifted from its frozen §16 fixture.\n"+
			"state/contract.json is read by /qompack:status and by the next SessionStart, so this "+
			"is a wire-format change, not a formatting one. Rule W-2 applies: a fixture the "+
			"implementation cannot reproduce is a verification failure, not a fixture bug.")
}

// TestResultSet_GoldenRoundTripsLosslessly is the other half of Rule W-2: the frozen bytes must be
// consumable by the declared type, not merely producible by it. A field on disk that []Result does
// not model would be dropped here in silence, and the golden would become unreproducible by the
// first implementation that tried.
func TestResultSet_GoldenRoundTripsLosslessly(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(resultSetGolden)
	require.NoError(t, err)

	var results []contract.Result
	require.NoError(t, json.Unmarshal(raw, &results))
	require.Len(t, results, 9)

	round, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	require.Equal(t, string(raw), string(round)+"\n")

	for _, r := range results {
		require.True(t, r.OK, "%s: a fresh build reports every assertion OK", r.ID)
		require.Equal(t, contract.SevInfo, r.Severity,
			"%s: the OBSERVED severity is SevInfo even where the assertion DECLARES SevCritical", r.ID)
		require.NotEmpty(t, r.Expected, "%s: Expected is half the degraded banner", r.ID)
		require.NotEmpty(t, r.Observed, "%s: Observed is the other half", r.ID)
		require.Positive(t, int64(r.TS), "%s: an untimestamped result cannot be aged out", r.ID)
	}
}
