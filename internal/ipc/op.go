package ipc

import "sort"

// The five admin.* operations (00-ARCHITECTURE.md §5.4's reserved admin namespace), built from the
// shipped OpAdminPrefix rather than a hand-respelled literal so the two can never drift apart.
// Routing them is a later task's job (daemon.Options' handler map); this package only names them.
const (
	OpAdminPing     Op = OpAdminPrefix + "ping"
	OpAdminDrain    Op = OpAdminPrefix + "drain"
	OpAdminReload   Op = OpAdminPrefix + "reload"
	OpAdminIdle     Op = OpAdminPrefix + "idle"
	OpAdminShutdown Op = OpAdminPrefix + "shutdown"
)

// KnownOps returns every operation this build understands — the eight §5.4 operations plus the
// five admin.* ones above — sorted, so `self-test` and `/qompack:status` render a stable list and
// Op.Valid has a single source of truth to check against.
func KnownOps() []Op {
	ops := []Op{
		OpObserveTool, OpObservePrompt, OpObserveStop, OpSessionStart,
		OpCheckpoint, OpFlush, OpStatus, OpMCP,
		OpAdminPing, OpAdminDrain, OpAdminReload, OpAdminIdle, OpAdminShutdown,
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i] < ops[j] })
	return ops
}

// Valid reports whether o is one of KnownOps — the check a router applies before dispatching, and
// self-test applies before trusting a spelling it did not itself produce.
func (o Op) Valid() bool {
	for _, k := range KnownOps() {
		if o == k {
			return true
		}
	}
	return false
}

// HotPath reports whether o is one of the three operations that flow through §8.1/§12.2's hot-path
// budget and its spool-on-breach fallback: observe.tool, observe.prompt, observe.stop. Every other
// op — session.start, checkpoint, flush, status, mcp, and the admin.* namespace — is off the hot
// path and never consults HotPathMode.
func (o Op) HotPath() bool {
	switch o {
	case OpObserveTool, OpObservePrompt, OpObserveStop:
		return true
	default:
		return false
	}
}
