package rehydrate

import (
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
)

// A checkpoint fallback is never silent (owner decision D49, F-C4-UAT03-1).
//
// checkpoint.Reader.Latest steps over a checkpoint that does not verify and returns its parent
// (§12: refuse the affected checkpoint, fall back). On candidate 4 that reached the model as
// "checkpoint 0001" with no section 7, an empty drop report and degraded false: an older state
// presented as the current one. Request.Ref.Refused carries what Latest stepped over, and a Build
// with any refusal names it in four places: the document header ("rolled back from"), section 7's
// first line (dropKindCheckpointFallback sorts with the overflows), Result.Dropped (what dropped()
// answers with), and Result.Degraded with DegradedReason. The daemon logs the rollback Loud.

// dropKindCheckpointFallback is the kind of the one drop entry naming a fallback. Its ID is the
// newest refused checkpoint.
const dropKindCheckpointFallback = "checkpoint_fallback"

// fellBack reports whether r was rebuilt from something older than the newest checkpoint.
func fellBack(r Request) bool { return len(r.Ref.Refused) > 0 }

// seqs renders checkpoint sequence numbers as "0003", "0003 and 0002", "0004, 0003 and 0002".
func seqs(s []core.CheckpointSeq) string {
	parts := make([]string, len(s))
	for i, q := range s {
		parts[i] = fmt.Sprintf("%04d", int(q))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// rolledBackTo names what r was rebuilt from: "0001", or "no checkpoint" when nothing verified.
func rolledBackTo(r Request) string {
	if r.Ref.Seq == 0 {
		return "no checkpoint"
	}
	return fmt.Sprintf("%04d", int(r.Ref.Seq))
}

// degradedReason is Result.DegradedReason for a fallback, "" otherwise.
func degradedReason(r Request) string {
	if !fellBack(r) {
		return ""
	}
	verb := "does"
	if len(r.Ref.Refused) > 1 {
		verb = "do"
	}
	return "checkpoint " + seqs(r.Ref.Refused) + " " + verb + " not verify; rolled back to " + rolledBackTo(r)
}

// fallbackDrop is the drop entry naming r's fallback: which checkpoints were refused, what the
// rehydration was rebuilt from, what may be missing, and how to restore it.
func fallbackDrop(r Request) checkpoint.DropEntry {
	refused := r.Ref.Refused
	noun, verb := "checkpoint", "does"
	if len(refused) > 1 {
		noun, verb = "checkpoints", "do"
	}
	from := "rebuilt from checkpoint " + rolledBackTo(r) + ", so anything recorded after it"
	if r.Ref.Seq == 0 {
		from = "rebuilt without a checkpoint, from the captured prompts and the ledger only, so anything a " +
			"checkpoint recorded"
	}
	return checkpoint.DropEntry{
		Kind: dropKindCheckpointFallback,
		ID:   fmt.Sprintf("%04d", int(refused[0])),
		Detail: noun + " " + seqs(refused) + " " + verb + " not verify and " + verb + " not describe this " +
			"rehydration; it was " + from + " (decisions, eliminations, pins, corrections to the request, " +
			"current work) may be missing; restore: recall() and timeline() reach the later turns in the " +
			"captured history, `qompack fsck` names the damaged artifact, and `qompack backup restore` " +
			"restores a verified backup",
	}
}
