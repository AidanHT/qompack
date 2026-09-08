package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// CheckpointOutcome is how a local checkpoint-now request ended.
type CheckpointOutcome string

const (
	// CheckpointSealed: an immutable checkpoint was written.
	CheckpointSealed CheckpointOutcome = "sealed"
	// CheckpointTruncated: one was written, and material was dropped to fit the budget. It is a
	// separate outcome from "sealed" because a caller who needs everything must be able to tell.
	CheckpointTruncated CheckpointOutcome = "truncated"
	// CheckpointEmpty: there was nothing unencoded to seal. Not a failure.
	CheckpointEmpty CheckpointOutcome = "empty"
	// CheckpointFailed: the attempt ran and did not produce a checkpoint.
	CheckpointFailed CheckpointOutcome = "failed"
)

// CheckpointResult is what the checkpoint command reports.
//
// RequestedNativeCompaction is present, and always false, on purpose. §7.1 makes Qompack a
// sidecar: it observes the host's compaction and never drives it. A reader of this record — a
// person, a later audit, an evaluation harness — should be able to confirm that from the record
// itself rather than by inspecting the code that wrote it.
type CheckpointResult struct {
	Outcome CheckpointOutcome `json:"outcome"`
	// Seq is the sealed checkpoint's sequence number, when one was written.
	Seq core.CheckpointSeq `json:"seq,omitempty"`
	// Reason is the caller's own note, recorded with the checkpoint.
	Reason string `json:"reason,omitempty"`
	// Dropped describes what did not fit, when the outcome is truncated.
	Dropped []string `json:"dropped,omitempty"`
	// Detail carries the failure explanation, when the outcome is failed.
	Detail                    string `json:"detail,omitempty"`
	RequestedNativeCompaction bool   `json:"requested_native_compaction"`
}

// CheckpointNow requests one Qompack-local checkpoint.
//
// It is a seam rather than a direct call into internal/checkpoint because sealing is the daemon's
// job: the daemon holds the open segment log and the frontier, and a second writer reaching into
// the same store from a short-lived process is how a DPI violation gets written. internal/cli
// binds this to a daemon request; a build with no daemon route leaves it nil, and the command
// reports unavailable.
//
// It deliberately does NOT reuse ipc.OpCheckpoint. That route is the PreCompact hook's: it records
// a PreCompact observation and sets AwaitingCompactStart, which is a claim that the HOST is about
// to compact. A user asking for a checkpoint has made no such claim, and recording one would
// corrupt the contract monitor's view of what the host did.
type CheckpointNow func(ctx context.Context, reason string) (CheckpointResult, error)

// checkpointBody implements `/qompack:checkpoint [--reason <text>]`.
func checkpointBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	if len(inv.Args) > 0 {
		return nil, UsageErrorf(
			"qompack checkpoint: takes no positional arguments; use --reason \"why\" to record a note")
	}
	reason := strings.TrimSpace(flagValue(inv, "reason"))

	if inv.Deps.CheckpointNow == nil {
		return nil, fmt.Errorf(
			"%w: this build has no local checkpoint route wired, so no checkpoint was requested",
			ErrUnavailable)
	}

	res, err := inv.Deps.CheckpointNow(ctx, reason)
	res.RequestedNativeCompaction = false
	res.Reason = reason

	data, marshalErr := json.Marshal(res)
	if marshalErr != nil {
		return nil, fmt.Errorf("checkpoint: encoding result: %w", marshalErr)
	}
	if !inv.JSON {
		renderCheckpoint(inv, res)
	}
	if err != nil {
		return data, fmt.Errorf("checkpoint: %w", err)
	}
	if res.Outcome == CheckpointFailed {
		return data, fmt.Errorf("checkpoint: %s", orUnknown(res.Detail))
	}
	return data, nil
}

// renderCheckpoint writes the human form, keeping the outcomes distinct.
func renderCheckpoint(inv Invocation, res CheckpointResult) {
	switch res.Outcome {
	case CheckpointSealed:
		fmt.Fprintf(inv.Out, "sealed checkpoint %d\n", res.Seq)
	case CheckpointTruncated:
		fmt.Fprintf(inv.Out, "sealed checkpoint %d, with material dropped to fit the budget\n", res.Seq)
		for _, d := range res.Dropped {
			fmt.Fprintf(inv.Out, "  dropped: %s\n", d)
		}
	case CheckpointEmpty:
		fmt.Fprintln(inv.Out, "nothing to seal: no closed, unencoded segments since the last checkpoint")
	case CheckpointFailed:
		fmt.Fprintf(inv.Out, "no checkpoint was written: %s\n", orUnknown(res.Detail))
	default:
		fmt.Fprintf(inv.Out, "unrecognized checkpoint outcome %q\n", res.Outcome)
	}
	if res.Reason != "" {
		fmt.Fprintf(inv.Out, "reason: %s\n", res.Reason)
	}
	fmt.Fprintln(inv.Out, "no native compaction was requested; Qompack observes the host's, it does not drive it")
}
