package contract

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// notYetImplementedObserved is the exact Observed string §12.1 gives an assertion whose producer is
// absent from the build. It is matched literally by test/guards' §12.1 assertion and by the
// contract golden fixture, so it is a frozen string, not a message.
const notYetImplementedObserved = "not-yet-implemented"

// StandardAssertions returns the nine host-contract assertions of 00-ARCHITECTURE.md §5.19, in the
// order §12.1's table lists them, each carrying its real DECLARED severity.
//
// Every Check here is notYetImplemented: the observations belong to SP-05 (and, for the four
// assertions whose producer is a later wave's subsystem, to SP-10/SP-11/SP-13). Registering these
// against a fresh monitor and calling RunAll therefore reports ModeFull, which is precisely what
// §12.1 requires and what test/guards asserts.
func StandardAssertions() []Assertion {
	return []Assertion{
		notYetImplemented(CSessionStartFires, SevCritical, "SessionStart hook fires"),
		notYetImplemented(CSessionStartSourceCompact, SevCritical, "SessionStart arrives with source=compact after PreCompact"),
		notYetImplemented(CAdditionalContext, SevCritical, "additionalContext reaches the transcript"),
		notYetImplemented(CPreCompactTiming, SevWarn, "PreCompact has time to write"),
		notYetImplemented(CPreCompactCustomInstr, SevWarn, "custom_instructions accepted"),
		notYetImplemented(CHookPayloadShape, SevCritical, "hook payload shape matches hookio.Event"),
		notYetImplemented(CMCPRegistered, SevInfo, "MCP server received initialize"),
		notYetImplemented(CTranscriptReadable, SevWarn, "transcript_path exists and parses"),
		notYetImplemented(CPluginRootResolves, SevWarn, "CLAUDE_PLUGIN_ROOT expands to an existing binary"),
	}
}

// notYetImplemented builds an assertion that DECLARES severity `declared` but whose Check reports
// OK with SevInfo.
//
// That asymmetry is the whole mechanism, and it is worth being explicit about because it looks like
// a bug: the Assertion carries the severity an eventual real failure would have, while the Result
// carries the severity of what was actually observed — and what was observed is "the subsystem that
// would produce this signal is not in this build", which is information, not a degradation. RunAll
// reads Result.Severity, never Assertion.Severity, so a fresh build reports ModeFull instead of
// putting every wave-1 and wave-2 verification run into degraded-passive and silently disabling the
// very paths those waves are testing (§12.1).
//
// Check reads e.Clock directly, exactly as §14.1 of the subplan spells it. RunAll substitutes a
// system clock for a nil Env.Clock before invoking any Check, so a caller that omits the clock gets
// a timestamped result rather than a nil dereference.
func notYetImplemented(id ID, declared Severity, desc string) Assertion {
	return Assertion{
		ID: id, Severity: declared, Description: desc,
		Check: func(ctx context.Context, e Env) Result {
			// §12.1: an assertion whose PRODUCER is absent from the build reports
			// OK/SevInfo, never a degradation. SP-05 replaces Check, not this rule.
			return Result{
				ID: id, OK: true, Severity: SevInfo,
				Expected: desc, Observed: notYetImplementedObserved, TS: core.NowMilli(e.Clock),
			}
		},
	}
}
