package canon

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Registry holds a set of Canonicalizers and dispatches a tool's output through whichever of them
// apply, in registration order (00-ARCHITECTURE.md §5.6).
type Registry interface {
	// Register adds c to the registry; it errors on a duplicate Name.
	Register(c Canonicalizer) error
	// For returns every registered Canonicalizer that applies to tool's output at path, in
	// registration order.
	For(tool, path string) []Canonicalizer
	// Run applies every applicable Canonicalizer to in, in registration order, and returns the
	// combined Result.
	Run(tool, path string, in []byte, o Options) (Result, error)
	// Names returns every registered Canonicalizer's Name, in registration order.
	Names() []string
}

// NewRegistry returns an empty Registry. Constructing always succeeds, so wave-0 composition
// roots can wire a canon.Registry today, but every operation is a stub until SP-04 lands the real
// registration/dispatch logic and the twelve canonicalizers it registers (00-ARCHITECTURE.md
// §5.6): Register and Run report core.ErrNotImplemented, and For/Names (which have no error
// return) report the documented nil.
func NewRegistry() Registry {
	return stubRegistry{}
}

// Default returns a Registry pre-populated the way a real implementation would: registering, in
// order, crlf, ansi, timestamps, durations, pids, addresses, tmpPaths, then the per-tool passes
// bash, testrunner (go/jest/pytest/cargo), grep, glob, fileread, webfetch, git
// (00-ARCHITECTURE.md §5.6). cfg is accepted (and ignored) by the stub so the signature matches
// what SP-04's real constructor needs; the stub itself registers nothing, since the twelve
// concrete canonicalizers are SP-04's implementation, not SP-01's.
//
// Default has no error return, matching every other "computational" constructor in this codebase:
// building a Registry performs no I/O by itself, so there is nothing for a stub constructor to
// fail at.
func Default(cfg config.CanonicalizeCfg) Registry {
	return stubRegistry{}
}

// stubRegistry is the SP-01 placeholder Registry returned by both NewRegistry and Default. SP-04
// owns the real implementation and the twelve canonicalizers it registers.
type stubRegistry struct{}

// Register always reports core.ErrNotImplemented.
func (stubRegistry) Register(c Canonicalizer) error { return core.ErrNotImplemented }

// For always returns nil: For has no error return, so nil — Rule 1's documented zero value — is
// the only honest answer: the stub has no Canonicalizers registered, because Register never
// succeeds.
func (stubRegistry) For(tool, path string) []Canonicalizer { return nil }

// Run always reports core.ErrNotImplemented.
func (stubRegistry) Run(tool, path string, in []byte, o Options) (Result, error) {
	return Result{}, core.ErrNotImplemented
}

// Names always returns nil, for the same reason as For.
func (stubRegistry) Names() []string { return nil }
