package canary

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
)

// injectionScanTailBytes bounds how much of a supplied transcript's tail is read. It matches the
// bound the shipped assertion uses (assertions.go's customInstrScanTailBytes), so the canary and
// the production check look at the same window.
const injectionScanTailBytes = 256 << 10

// TestCanary_SessionStartReinjection is the canary the capability register NAMES for injection, and
// the only thing that could move that capability from implemented_unverified to verified_in_target.
//
// The mechanism under test is the one 00-ARCHITECTURE.md §12.1 describes: SessionStart emits a
// sentinel inside additionalContext, and it is found later in the session transcript. Two properties
// of that design matter here and are asserted rather than assumed.
//
// It does not wait for PostCompact. Shared contract 5 is explicit — "SessionStart reinjection cannot
// wait for PostCompact" — because PostCompact is optional and a mechanism gated on an optional event
// cannot be relied on. This canary reads a transcript; it never looks for a PostCompact event.
//
// And what it can prove is narrow. A sentinel in the tail documents ONE DELIVERY UNDER THE TESTED
// CONTRACT. It does not prove complete context, that the model read it, or that anything else was
// delivered — which is exactly why contract.CoverageDeliveryUnderTestedContract is the strongest
// coverage the ledger can record and "complete" is not in the vocabulary.
//
// Running it requires a DISPOSABLE target session: a transcript produced by a session that was
// started for this purpose. M0-03 forbids probing the active user session, so without
// QOMPACK_CANARY_TRANSCRIPT and QOMPACK_CANARY_SENTINEL this records `skipped` and injection stays
// unverified.
func TestCanary_SessionStartReinjection(t *testing.T) {
	tgt := hostTarget(t)
	rec := Record{Name: "session_start_reinjection", Capability: contract.CapInjection, Scope: ScopeInstalledSession, Target: tgt}

	transcript := os.Getenv("QOMPACK_CANARY_TRANSCRIPT")
	sentinel := os.Getenv("QOMPACK_CANARY_SENTINEL")
	switch {
	case transcript == "" || sentinel == "":
		skipRecorded(t, rec, "no disposable target session transcript supplied "+
			"(QOMPACK_CANARY_TRANSCRIPT and QOMPACK_CANARY_SENTINEL); injection remains "+
			"implemented_unverified. Probing the active session is forbidden by M0-03.")
	case !strings.HasPrefix(sentinel, "qompack-contract-"):
		// A sentinel that is not one of ours would make this canary scan for arbitrary text in a
		// user's transcript, which is neither a contract test nor within the privacy boundary.
		skipRecorded(t, rec, "QOMPACK_CANARY_SENTINEL is not a Qompack contract sentinel; "+
			"refusing to scan a transcript for text this plugin did not mint")
	}

	found, err := contract.ScanTranscriptTail(transcript, sentinel, injectionScanTailBytes)

	// The record names the sentinel — it is a minted probe token, explicitly inside the privacy
	// boundary — and never the transcript path or any of its content.
	switch {
	case err != nil:
		rec.Outcome = OutcomeSkipped
		rec.Reason = "the supplied transcript could not be read; an unreadable transcript proves " +
			"nothing about whether the sentinel was ever emitted, so injection remains unverified"
	case found:
		rec.Outcome = OutcomeVerified
		rec.Reason = "sentinel " + sentinel + " found in the last " +
			strconv.Itoa(injectionScanTailBytes) + " bytes of a disposable target session transcript. " +
			"This documents one delivery under the tested contract — coverage " +
			contract.CoverageDeliveryUnderTestedContract + " — and does not establish complete " +
			"context, exact loaded bytes or model compliance. No PostCompact event was required " +
			"or awaited."
	default:
		rec.Outcome = OutcomeFailed
		rec.Reason = "sentinel " + sentinel + " was not found in the supplied transcript tail; " +
			"additionalContext did not reach the transcript under this host"
	}
	writeRecord(t, rec)

	if err != nil {
		t.Skipf("canary session_start_reinjection skipped: %v", err)
	}
	require.True(t, found,
		"the injection capability is enabled in the register; a supplied disposable target that "+
			"does not show the sentinel falsifies it")
}
