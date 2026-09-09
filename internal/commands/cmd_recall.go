package commands

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/mcp"
)

// recallBody implements `/qompack:recall <query> [--k N]`.
//
// It is a frontend and nothing more: the query goes to SP-13's registered `recall` handler, which
// already runs the authorization check before any preview is built, and whatever comes back is
// passed on. There is deliberately no search here — a second implementation would be a second set
// of authorization rules to keep in step with the first.
func recallBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	query := strings.TrimSpace(strings.Join(inv.Args, " "))
	if query == "" {
		return nil, UsageErrorf("qompack recall: a query is required, e.g. qompack recall \"open file handle\"")
	}

	args := recallArgs{Query: query}
	if raw, ok := inv.Flag("k"); ok {
		k, err := strconv.Atoi(raw)
		if err != nil || k <= 0 {
			return nil, UsageErrorf("qompack recall: --k takes a positive whole number, got %q", raw)
		}
		args.K = k
	}

	return runTool(ctx, inv, mcp.ToolRecall, args)
}

// recallArgs is the wire shape the frontend sends to the `recall` handler.
//
// It is deliberately not mcp.RecallArgs. That type's K carries no omitempty, so a query with no
// --k would be sent as k:0 — and the handler's schema declares k with minimum 1, so every
// `qompack recall <query>` was refused with "/k: below minimum" through the real binary while the
// frontend's own tests, which record the call without applying the schema, passed. The schema's
// default of 5 applies only when k is ABSENT, and absence is what an unset flag must encode as.
type recallArgs struct {
	Query string `json:"query"`
	K     int    `json:"k,omitempty"`
}
