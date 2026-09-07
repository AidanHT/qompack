package daemon

import (
	"runtime"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
)

// hostProvider is the only host this plugin is packaged for (Qompack.md §7.1); a hook payload
// carries no host version, so the observation ledger's Target names the provider and this
// binary's platform and leaves Version empty rather than inventing one.
const hostProvider = "claude-code"

// recordCapabilityObservations appends one SessionStart's monitor results to the per-capability
// observation ledger (00-ARCHITECTURE.md §0.2.6 and the v1.5 §12.1; SP-19 commit 3). It is the
// same nine Results the monitor keeps in state/contract.json, re-read through
// contract.ClassifyResult so that an undeclared producer is recorded as unavailable and an
// unsupported mechanism as unsupported — never as verified success, which is what the legacy
// OK/SevInfo/not-yet-implemented Result reads as on its own. The ledger is a separate file
// (state/observations.json) so state/contract.json and state/history.json keep their frozen
// shapes. It runs under historyMu with the other SessionStart persistence, and a write failure is
// a Warn, never a failed hook (§13 invariant 6).
func (d *daemon) recordCapabilityObservations(results []contract.Result, sess core.SessionID, now core.UnixMilli) {
	target := contract.Target{
		Provider: hostProvider,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Date:     time.UnixMilli(int64(now)).UTC().Format("2006-01-02"),
	}
	path := contract.ObservationLedgerPath(d.root)
	led := contract.LoadObservationLedger(path)
	led.Target = target
	led.Append(contract.ObservationsOf(results, contract.DefaultCapabilityRegister(), target, string(sess))...)
	if err := contract.SaveObservationLedger(path, led); err != nil {
		d.log.Warn("daemon: failed to save capability observations", "err", err)
	}
}
