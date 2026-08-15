package eval

import (
	"context"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Options configures a Harness constructed by New. It is not part of 00-ARCHITECTURE.md §5.18's
// normative type set — Harness is the seam, not its constructor — so SP-02 is free to extend it
// (for example with a logger or a metrics registry once eval's own real implementation needs
// them) without that being an amendment: nothing §5.18 lists changes. SP-01 keeps Options minimal
// and within eval's own foundation-only allow-set (00-ARCHITECTURE.md §3.2: core, paths, config).
type Options struct {
	// Cfg is the configuration in force for the harness's replay and scoring decisions. The zero
	// value is replaced by config.Defaults() in New.
	Cfg config.Config
}

// New returns a stub Harness: constructing it always succeeds — New(Options{}) is always valid,
// with no need to pre-fill Options.Cfg — so wave-0 composition roots can wire an eval.Harness
// today, but every operation reports core.ErrNotImplemented (or a documented zero value, for the
// two methods with no error return) until SP-02 lands the real replay-and-scoring implementation
// (00-ARCHITECTURE.md §5.18). The stub never reads o, since none of its methods have any real
// behaviour to configure yet; SP-02's real New is expected to fall Options.Cfg back to
// config.Defaults() once Cfg is actually consulted.
//
// New has no error return, matching every other "computational" constructor in this codebase
// (chunk.New, symbols.New, grammar.New, redact.New): building a Harness performs no I/O by
// itself, so there is nothing for a stub constructor to fail at.
func New(o Options) Harness {
	return stubHarness{}
}

// stubHarness is the SP-01 placeholder Harness. SP-02 owns the real implementation.
type stubHarness struct{}

// Load always reports core.ErrNotImplemented.
func (stubHarness) Load(dir string) ([]Session, error) { return nil, core.ErrNotImplemented }

// Replay always reports core.ErrNotImplemented.
func (stubHarness) Replay(ctx context.Context, s Session, p Policy, o ReplayOptions) (Run, error) {
	return Run{}, core.ErrNotImplemented
}

// Compare always reports the zero Divergence. Compare has no error return, and the zero value —
// no divergence detected at any turn, identical file sets, zero edit distance — is the only
// honest "no comparison has actually been performed" answer a stub can give.
func (stubHarness) Compare(uncompacted, compacted Run) Divergence { return Divergence{} }

// Belady always reports core.ErrNotImplemented.
func (stubHarness) Belady(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error) {
	return KeepSet{}, core.ErrNotImplemented
}

// ScoreRun always reports the zero Score, for the same reason as Compare: ScoreRun has no error
// return, and a zero Score is the only honest "not scored" answer a stub can give.
func (stubHarness) ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score { return Score{} }

// Report always reports core.ErrNotImplemented.
func (stubHarness) Report(ctx context.Context, scores map[string][]Score) (Report, error) {
	return Report{}, core.ErrNotImplemented
}
