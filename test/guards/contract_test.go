package guards

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// notYetImplementedObserved is the Observed value StandardAssertions gives an assertion whose
// producer is absent from the build. It is duplicated here rather than exported from contract on
// purpose: the guard must fail if that string ever changes, because §12.1's rule is stated in
// terms of it and a silently-renamed marker would make this test pass vacuously.
const notYetImplementedObserved = "not-yet-implemented"

// contractStatePath is where the monitor persists degradation state, per §3.3's state/ directory.
const contractStateFile = "contract.json"

// TestGuard_FreshBuildReportsModeFull is §12.1's most important consequence.
//
// Every assertion in a wave-0 build is unimplemented. The tempting reading — "unimplemented means
// unverified means degrade" — would ship a plugin that reports itself broken on first run, and a
// degradation that fires on a healthy install trains users to ignore degradations. §12.1 therefore
// says an assertion whose PRODUCER is absent reports OK at SevInfo: not a pass, not a failure, an
// absence. This guard is what keeps that distinction from eroding as SP-05 replaces Check bodies
// one at a time.
func TestGuard_FreshBuildReportsModeFull(t *testing.T) {
	p := testutil.NewProject(t)

	m := contract.NewMonitor(p.Log, obs.New(p.Clock),
		filepath.Join(paths.Of(p.Root).State, contractStateFile))

	assertions := contract.StandardAssertions()
	require.NotEmpty(t, assertions, "StandardAssertions must not be empty — the guard would pass vacuously")
	for _, a := range assertions {
		require.NoError(t, m.Register(a))
	}

	res, mode := m.RunAll(context.Background(), contract.Env{
		ProjectRoot: p.Root,
		Cfg:         p.Cfg,
		Log:         p.Log,
		Clock:       p.Clock,
	})

	require.Equal(t, contract.ModeFull, mode,
		"a freshly built develop must report ModeFull (§12.1)")
	require.Len(t, res, len(assertions), "every registered assertion must produce a result")

	sawNotYetImplemented := false
	for _, r := range res {
		if r.Observed != notYetImplementedObserved {
			continue
		}
		sawNotYetImplemented = true
		require.True(t, r.OK,
			"assertion %s: an absent producer reports OK, never a failure (§12.1)", r.ID)
		require.Equal(t, contract.SevInfo, r.Severity,
			"assertion %s: an absent producer reports SevInfo regardless of its declared severity", r.ID)
	}
	require.True(t, sawNotYetImplemented,
		"no assertion reported %q — this build has assertions the guard cannot see", notYetImplementedObserved)
}

// TestGuard_DeclaredSeverityIsPreservedOnTheAssertion checks the half of the rule that is easy to
// lose: the RESULT reports SevInfo, but the ASSERTION must keep the severity it declares, because
// that is what will make it degrade once SP-05 gives it a real Check. Collapsing both to SevInfo
// would make every assertion permanently non-degrading and silently disable §12 for good.
func TestGuard_DeclaredSeverityIsPreservedOnTheAssertion(t *testing.T) {
	t.Parallel()

	bySeverity := map[contract.Severity]int{}
	for _, a := range contract.StandardAssertions() {
		bySeverity[a.Severity]++
	}

	require.Positive(t, bySeverity[contract.SevCritical],
		"§12.1 declares critical assertions; if none survives, nothing can ever degrade")
	require.Positive(t, bySeverity[contract.SevWarn],
		"§12.1 declares warn-severity assertions")
}
