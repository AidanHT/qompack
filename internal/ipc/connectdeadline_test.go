package ipc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// TestConnectDeadlineDefaultClearsTheBusyRetryQuantum is the anti-drift guard between two
// constants that cannot see each other: runtime.daemon.connectDeadlineMs's built-in default, which
// lives in internal/config (deadlines.go), and dialBusyRetryQuantum, which lives here beside the
// dial it describes. §3.2 gives internal/config only internal/core, so config can never import
// this package and derive the default at compile time; and copying the quantum into config would
// make it exactly the second, driftable spelling D11 forbids. A test is the only place both are
// in scope, so this is where the derivation is actually enforced.
//
// The bound is the one the platform's own dial arithmetic requires. go-winio answers
// ERROR_PIPE_BUSY with a fixed sleep of one quantum and re-reads the caller's deadline only at
// the top of the next iteration (dialBusyRetryQuantum's own comment cites the lines), so a budget
// buys one CreateFile attempt plus one more per whole quantum that still fits strictly inside it:
// under one quantum buys a single attempt and no retry at all, and two quanta is the smallest
// budget that buys the retry the loop exists to perform. Anything at or above 2 x the quantum
// therefore has the retry; anything below it does not.
//
// On a platform whose dial has no busy-retry sleep the quantum is zero by construction
// (dial_other.go: net.Dialer enforces Timeout itself and there are no repeated attempts), so the
// bound is exact there too rather than skipped — there is no retry to buy and no overshoot to
// cover.
//
// Both directions are load-bearing and both were mutation-checked when this landed: lowering the
// Windows default below 2 x the quantum fails here, and doubling the quantum fails here.
func TestConnectDeadlineDefaultClearsTheBusyRetryQuantum(t *testing.T) {
	got := time.Duration(config.Defaults().Runtime.Daemon.ConnectDeadlineMs) * time.Millisecond

	require.Positive(t, got, "a connect deadline of zero is indistinguishable from already timed out")

	require.GreaterOrEqual(t, got, 2*dialBusyRetryQuantum,
		"config.Defaults() ships connectDeadlineMs=%s, which is under 2 x this platform's dial "+
			"busy-retry quantum (%s): a hot-path client that meets a momentarily-busy endpoint "+
			"gets one CreateFile attempt and no retry, and spools instead. Raise the default in "+
			"internal/config/deadlines.go, do not lower this bound",
		got, dialBusyRetryQuantum)
}
