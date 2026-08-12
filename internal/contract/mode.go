package contract

// Mode is the observable degradation state of the plugin (00-ARCHITECTURE.md §5.19, §12.1).
type Mode uint8

// The three modes.
//
// ModeDegradedPassive is §12.1's contract-failure state: L0 and L1 keep running (observe, chunk,
// store, sketches, DAG, verbatim capture, elimination records — the store stays correct and the
// session's data is not lost), and everything that ACTS is off (no additionalContext injection, no
// customInstructions, no scheduler-initiated checkpoints, no drop report). ModeOff is the operator
// decision — runtime.mode = "off" — not a state the monitor ever enters on its own.
const (
	ModeFull Mode = iota
	ModeDegradedPassive
	ModeOff
)

// String returns the mode's canonical spelling. These three strings are the ones §12 uses in prose
// and /qompack:status prints, and they are the on-disk spelling in state/contract.json, so they are
// frozen: a rename is a data-format change, not a cosmetic one.
func (m Mode) String() string {
	switch m {
	case ModeFull:
		return "full"
	case ModeDegradedPassive:
		return "degraded-passive"
	case ModeOff:
		return "off"
	}
	return "unknown"
}

// parseMode is String's inverse, used only when reading state/contract.json back. It reports false
// for anything it does not recognize — including "unknown" — so a corrupt or future-format state
// file falls back to ModeFull (§12.3: everything else fails toward "do nothing") rather than
// leaving the session in a mode nobody chose.
func parseMode(s string) (Mode, bool) {
	for _, m := range []Mode{ModeFull, ModeDegradedPassive, ModeOff} {
		if m.String() == s {
			return m, true
		}
	}
	return ModeFull, false
}
