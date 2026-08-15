package observer

import (
	"context"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// Event and Output alias the hookio types every Observer method is written in terms of
// (00-ARCHITECTURE.md §5.21). They are aliases, not new types, so the interface signatures below
// are literally unchanged: observer.Event and hookio.Event are the same type, and a value of
// either satisfies both.
//
// They exist for the same reason checkpoint.Invariant aliases pins.Invariant (§5.14) — to keep a
// package's own vocabulary usable from a package that may not import the definer. Concretely:
// internal/observer/observertest's import allow-set is its own package plus testutil and core
// (§3.2), so without these aliases the conformance suite could not construct so much as an empty
// hook payload, and the §5.21 interface would have no suite at all.
type (
	// Event is one Claude Code hook payload.
	Event = hookio.Event
	// Output is one hook subcommand's JSON response.
	Output = hookio.Output
)

// Observer is the L0 semantics seam (00-ARCHITECTURE.md §5.21). One method per hook the plugin
// registers; every one of them is on a latency budget, and every one of them must fail toward
// "record nothing, block nothing" rather than toward an error the host sees (§12.3).
type Observer interface {
	// OnToolUse handles PostToolUse: chunk and store the tool result, update the DAG, the
	// sketches and the action grammar, and detect redundancy. Qompack.md §8.1 budgets it at under
	// 15 ms p99, so an overrun degrades to queue-and-drain rather than blocking.
	OnToolUse(ctx context.Context, e Event) (Output, error)
	// OnUserPrompt handles UserPromptSubmit: capture the user's intent VERBATIM and immutably
	// (G2.3), and update the changepoint features.
	OnUserPrompt(ctx context.Context, e Event) (Output, error)
	// OnStop handles Stop and SubagentStop, distinguished by subagent: capture subagent detail
	// before the host double-compresses it (G10.1).
	OnStop(ctx context.Context, e Event, subagent bool) (Output, error)
	// OnSessionStart handles SessionStart. The source switch (startup|resume|compact|clear) lives
	// here and delegates: startup/resume load the store, compact rehydrates, clear resets.
	OnSessionStart(ctx context.Context, e Event) (Output, error)
	// OnSessionEnd handles SessionEnd: flush the store, write the session index, and run GC.
	OnSessionEnd(ctx context.Context, e Event) (Output, error)
}

// Options is the collaborator set New assembles an Observer from.
//
// 00-ARCHITECTURE.md §5.21 declares the Observer interface without a constructor, so Options is
// an SP-01 addition of the kind §14.0 of plans/V1-SP-01-foundation-toolchain-and-contracts.md
// describes: something §5 needs but does not spell out, decided once here so wave-0 composition
// roots have something to wire. Its field names are SP-08's own, so landing the real
// implementation widens this struct rather than renaming it.
type Options struct {
	// ProjectRoot is the project root the observer records under.
	ProjectRoot string
	// Cfg is the loaded configuration.
	Cfg config.Config
	// Store is the content-addressed store every observed byte is written through.
	Store store.Store
	// Graph is the dependence DAG nodes and edges are appended to.
	Graph dag.Graph
	// Grammar is the action-stream grammar tool names are folded into.
	Grammar grammar.Sequitur
	// Tokens prices observed content.
	Tokens tokens.Estimator
	// Log is the logger; a nil Log must be treated as logging.Nop.
	Log logging.Logger
	// Metrics is the metrics registry; a nil Metrics must not panic a hook.
	Metrics obs.Registry
	// Clock is the only source of time in this package (§6.1): no observer code calls time.Now.
	Clock core.Clock
}

// New returns an Observer built from o. Constructing always succeeds, so wave-0 composition roots
// can wire an observer.Observer today, but every hook entry point reports core.ErrNotImplemented
// until SP-08 lands the real L0 semantics (00-ARCHITECTURE.md §5.21).
func New(o Options) (Observer, error) { return stubObserver{}, nil }

// stubObserver is the SP-01 placeholder Observer. SP-08 owns the real implementation.
//
// Every method returns hookio.Empty() alongside core.ErrNotImplemented rather than a zero Output.
// They are the same value — Empty() is documented as the zero-cost `{}` response — but naming it
// says which of the two it is on purpose: a hook that cannot do its work must still hand the host
// a valid, do-nothing response, because §2.3 and §12.3 both require a hook to exit 0 and change
// nothing rather than surface a failure into the session.
type stubObserver struct{}

// OnToolUse always reports core.ErrNotImplemented.
func (stubObserver) OnToolUse(ctx context.Context, e Event) (Output, error) {
	return hookio.Empty(), core.ErrNotImplemented
}

// OnUserPrompt always reports core.ErrNotImplemented.
func (stubObserver) OnUserPrompt(ctx context.Context, e Event) (Output, error) {
	return hookio.Empty(), core.ErrNotImplemented
}

// OnStop always reports core.ErrNotImplemented.
func (stubObserver) OnStop(ctx context.Context, e Event, subagent bool) (Output, error) {
	return hookio.Empty(), core.ErrNotImplemented
}

// OnSessionStart always reports core.ErrNotImplemented.
func (stubObserver) OnSessionStart(ctx context.Context, e Event) (Output, error) {
	return hookio.Empty(), core.ErrNotImplemented
}

// OnSessionEnd always reports core.ErrNotImplemented.
func (stubObserver) OnSessionEnd(ctx context.Context, e Event) (Output, error) {
	return hookio.Empty(), core.ErrNotImplemented
}
