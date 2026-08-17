package contract

import (
	"context"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/store"
)

// Result is one assertion's observation from one RunAll (00-ARCHITECTURE.md §5.19).
//
// The JSON tags are the on-disk shape of state/contract.json's results array, which /qompack:status
// and the next SessionStart both read; §5.19 does not fix them, so SP-01 does, once.
//
// Severity here is the OBSERVED severity, which is not necessarily the declaring Assertion's: see
// Severity's own documentation, and gated (assertions.go), for the case that distinction exists for.
type Result struct {
	ID       ID       `json:"id"`
	OK       bool     `json:"ok"`
	Severity Severity `json:"severity"`
	// Expected and Observed are the two halves of the banner /qompack:status leads with when the
	// session is degraded: what the host contract promised, and what was actually seen.
	Expected string         `json:"expected"`
	Observed string         `json:"observed"`
	TS       core.UnixMilli `json:"ts"`
	Detail   string         `json:"detail,omitempty"`
}

// Assertion is one registered host-contract check (00-ARCHITECTURE.md §5.19).
//
// Severity is the assertion's DECLARED severity: how bad an observed failure would be. It is
// deliberately not the severity Check has to report when the assertion's producer is absent from
// the build — see gated (assertions.go).
type Assertion struct {
	ID          ID
	Severity    Severity
	Description string
	// Check performs the observation. It must not block indefinitely: it runs on SessionStart,
	// before any other work, and it is handed the caller's context precisely so a slow host probe
	// can be abandoned rather than delaying the session.
	Check func(ctx context.Context, e Env) Result
}

// Env is everything an Assertion.Check is allowed to observe (00-ARCHITECTURE.md §5.19). Every
// field is optional from the monitor's point of view: RunAll substitutes a system clock for a nil
// Clock and a no-op logger for a nil Log, so a caller that only has half an Env still gets results
// rather than a panic. A Check that needs a field the caller did not supply must report a failed —
// or, where the producer simply does not exist yet, a not-yet-implemented — Result, never
// dereference it blindly.
type Env struct {
	ProjectRoot string
	Event       hookio.Event
	Cfg         config.Config
	Store       store.Store
	Log         logging.Logger
	Clock       core.Clock
	// History is the cross-session record of which assertions have ever been observed to hold.
	// The assertions §12.1 phrases across sessions ("absence across two sessions", "evaluated on
	// the FOLLOWING start") read it.
	History History
}

// History is the observed-hook-firing record an Env carries (SP-01's decided spelling for the type
// §5.19 names but does not define). Implementations persist across sessions: Sessions counts how
// many sessions have been observed in total, which is what an assertion phrased as "absence across
// two sessions" needs in order to distinguish "not seen yet" from "not seen, twice".
type History interface {
	// Saw reports whether id has been observed at least once, this session or a prior one.
	Saw(id ID) bool
	// LastSeen returns when id was last observed, and false if it never was.
	LastSeen(id ID) (core.UnixMilli, bool)
	// Record notes an observation of id at time at.
	Record(id ID, at core.UnixMilli)
	// Sessions returns the number of sessions observed.
	Sessions() int
}
