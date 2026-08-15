package contract

// Severity grades a contract assertion (00-ARCHITECTURE.md §5.19). It appears in two distinct
// places with two distinct meanings, and conflating them is the single easiest way to break §12.1:
//
//   - Assertion.Severity is the DECLARED severity: how bad it would be if this assertion were
//     actually observed to fail.
//   - Result.Severity is the OBSERVED severity of one particular run, and it is what the mode
//     transition in RunAll reads. An assertion whose producer is absent from the build declares
//     SevCritical and still reports SevInfo, which is exactly why a fresh build is ModeFull.
type Severity uint8

// The three severities. Only an OBSERVED SevCritical failure degrades the session (§12.1); a
// SevWarn failure is logged and surfaced but never changes the mode, and SevInfo is never a
// failure signal at all.
const (
	SevInfo Severity = iota
	SevWarn
	SevCritical
)

// label renders s for a log line. It is deliberately unexported: 00-ARCHITECTURE.md §5.19 gives
// Severity no String method, and /qompack:status (SP-14) owns the user-facing rendering, so
// exporting one here would pre-empt a decision that is not SP-01's to make.
func (s Severity) label() string {
	switch s {
	case SevInfo:
		return "info"
	case SevWarn:
		return "warn"
	case SevCritical:
		return "critical"
	}
	return "unknown"
}
