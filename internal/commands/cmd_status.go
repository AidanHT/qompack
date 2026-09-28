package commands

import (
	"context"
	"encoding/json"
	"fmt"
)

// statusBody implements `/qompack:status [--json]`.
//
// It always succeeds. That is a deliberate choice and not an oversight: status exists to report
// what was observed, what was estimated and what is unknown, so "nothing could be reached" is one
// of its answers rather than a failure to produce one. A non-zero exit here would make a script
// that polls status treat a quiet session as a broken one, and would hide the report — which says
// exactly what could not be reached and why — behind an error line that does not.
//
// The two genuine failures remain failures: a malformed invocation is a usage error, and a report
// that cannot be encoded is an error, because in neither case is there a report to show.
func statusBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	if len(inv.Args) > 0 {
		return nil, UsageErrorf("qompack status: takes no arguments; add --json for the machine-readable form")
	}

	src := inv.Deps.Status
	if inv.Deps.Refused != nil {
		// A refused invocation is answered from the refusal alone, whatever sources were bound.
		src = StatusSources{Refused: inv.Deps.Refused}
	}
	rep := CollectStatus(ctx, src, inv.Now)

	data, err := json.Marshal(rep)
	if err != nil {
		return nil, fmt.Errorf("status: encoding the report: %w", err)
	}
	if !inv.JSON {
		if err := RenderStatus(inv.Out, rep); err != nil {
			return data, fmt.Errorf("status: writing the report: %w", err)
		}
	}
	return data, nil
}
