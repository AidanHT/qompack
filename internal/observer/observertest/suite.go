// Package observertest is the conformance suite for observer.Observer and observer.ExtractSignals
// (00-ARCHITECTURE.md §5.22): every implementation SP-08 ships must pass RunObserverSuite. SP-01
// ships the suite itself, including the behaviour assertions SP-08 inherits (Rule W-1) — only the
// guarded /behaviour block is skipped until a real Observer lands.
//
// Two notes on what is and is not here.
//
// The suite constructs hook payloads as observer.Event values. That alias exists for this
// package's benefit: a <pkg>test subpackage may import only its own package, testutil and core
// (00-ARCHITECTURE.md §3.2), so without observer.Event aliasing hookio.Event there would be no
// legal way to build so much as an empty payload, and §5.21's interface would have no suite at
// all.
//
// observer.Tombstone is NOT asserted here, and that is a §3.2 consequence rather than an
// oversight: its argument is a store.ToolUseRecord, which this package may not name. Its
// byte-for-byte assertion against Qompack.md §8.1's example therefore lives in
// internal/observer/tombstone_test.go, which may import store. SP-08 owns internal/observer and
// should keep it there, or add a record-building helper to internal/testutil (which every
// <pkg>test package may import) and move the case into this suite.
package observertest

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/observer"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// RunObserverSuite is the conformance suite for observer.Observer. name distinguishes multiple
// factories run in the same test binary; factory must return a fresh, ready-to-use Observer on
// every call.
func RunObserverSuite(t *testing.T, name string, factory func(t *testing.T) observer.Observer) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		o := factory(t)
		require.NotNil(t, o)
		ctx := context.Background()

		_, err := o.OnToolUse(ctx, toolUseEvent())
		requireKnownError(t, err)

		_, err = o.OnUserPrompt(ctx, userPromptEvent())
		requireKnownError(t, err)

		_, err = o.OnStop(ctx, stopEvent(), false)
		requireKnownError(t, err)

		_, err = o.OnStop(ctx, stopEvent(), true)
		requireKnownError(t, err)

		_, err = o.OnSessionStart(ctx, sessionStartEvent(SourceStartup))
		requireKnownError(t, err)

		_, err = o.OnSessionEnd(ctx, sessionEndEvent())
		requireKnownError(t, err)
	})

	if skipIfStub(t, factory) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("post_tool_use_never_blocks_the_tool_call", func(t *testing.T) { runToolUseIsSilentCase(t, factory) })
		t.Run("user_prompt_capture_never_blocks_the_prompt", func(t *testing.T) { runUserPromptCase(t, factory) })
		t.Run("session_start_branches_on_source", func(t *testing.T) { runSessionStartSourceCase(t, factory) })
		t.Run("stop_and_subagent_stop_are_both_accepted", func(t *testing.T) { runStopCase(t, factory) })
		t.Run("session_end_flushes_without_reporting_an_error", func(t *testing.T) { runSessionEndCase(t, factory) })
		t.Run("every_entry_point_tolerates_a_malformed_event", func(t *testing.T) { runDegradeCase(t, factory) })
		t.Run("replaying_one_event_twice_is_not_an_error", func(t *testing.T) { runReplayCase(t, factory) })
		t.Run("extract_signals_detects_todo_test_and_git", func(t *testing.T) { runExtractSignalsCase(t) })
	})
}

// requireKnownError fails the test unless err is nil or wraps one of the four sentinels every
// stub and every real implementation is allowed to return from an operation
// (00-ARCHITECTURE.md §5.22; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md).
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known, "unexpected error: %v", err)
}

// isStub reports whether factory currently produces a stub Observer, using OnToolUse as the probe
// (plans/OWNERS.tsv: observer's probe method is OnToolUse).
func isStub(t *testing.T, factory func(t *testing.T) observer.Observer) bool {
	t.Helper()
	_, err := factory(t).OnToolUse(context.Background(), toolUseEvent())
	return core.IsNotImplemented(err)
}

// skipIfStub calls t.Skip with the exact Rule W-1 message when factory still produces a stub
// Observer, and reports whether it did.
func skipIfStub(t *testing.T, factory func(t *testing.T) observer.Observer) bool {
	t.Helper()
	if isStub(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
