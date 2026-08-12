package logging

import "fmt"

// Level is a log severity. Levels order by number, lowest-severity first, so a logger's minimum
// level filters with a plain "<" comparison and Loud — always the highest value — can never be
// suppressed by any configured minimum (§12: the Loud channel must never be silent).
type Level uint8

// The five levels, in increasing severity. Loud is not merely "above Error": it is a distinct
// channel with its own destinations (see loud.go), reserved for contract violations and
// degradation transitions.
const (
	Debug Level = iota
	Info
	Warn
	Error
	Loud
)

// String renders the level the way every log line's "level=" field and runtime.logging.level
// spell it.
func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	case Loud:
		return "loud"
	default:
		return "unknown"
	}
}

// ParseLevel parses the runtime.logging.level enum (debug|info|warn|error — Loud is never a
// configurable minimum, since it is always emitted regardless of the configured floor).
func ParseLevel(s string) (Level, error) {
	switch s {
	case "debug":
		return Debug, nil
	case "info":
		return Info, nil
	case "warn":
		return Warn, nil
	case "error":
		return Error, nil
	default:
		return Info, fmt.Errorf("logging: unknown level %q", s)
	}
}
