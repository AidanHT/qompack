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

	args := mcp.RecallArgs{Query: query}
	if raw, ok := inv.Flag("k"); ok {
		k, err := strconv.Atoi(raw)
		if err != nil || k <= 0 {
			return nil, UsageErrorf("qompack recall: --k takes a positive whole number, got %q", raw)
		}
		args.K = k
	}

	return runTool(ctx, inv, mcp.ToolRecall, args)
}
