package daemon

import (
	"context"

	"github.com/qompack/qompack/internal/contract"
)

// contractSnapshot is the contract rows the status op answers: the monitor's last RunAll, with
// every row still waiting for an observation that state/history.json now records replaced by that
// observation (contract.RefreshFromHistory). The monitor runs at SessionStart, before the host has
// connected the MCP server or delivered the probe into the transcript, and its rows would otherwise
// read both pending until the next start while history.json already records them (candidate 4
// re-run, UAT-01; D50). doctor applies the same refresh to the observation ledger's newest entry
// (internal/cli/doctor.go, capabilityRow); the ledger itself keeps one run per SessionStart.
//
// A handshake this daemon's own seam saw counts although history.json does not record it, as it
// does at a start (handleSessionStart): the history write can be lost to a start's save of the copy
// it loaded before the handshake landed, or refused by the disk. The history is read under
// historyMu, like every other reader of it in this package; nothing is written.
func (d *daemon) contractSnapshot(ctx context.Context) []contract.Result {
	d.historyMu.Lock()
	h := contract.LoadHistory(contract.HistoryPath(d.root))
	d.historyMu.Unlock()
	if d.svc.MCPInitialized != nil && !h.MCPInitialized && d.svc.MCPInitialized(ctx) {
		h.MCPInitialized = true
	}
	return contract.RefreshFromHistory(d.monitor.Report(), h, d.clk)
}
