package commands

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/qompack/qompack/internal/mcp"
)

// whyBody implements `/qompack:why <decision-id>`.
//
// It reuses SP-13's `why` handler, which answers from recorded decisions only.
//
// The frontend adds no interpretation on top of that answer, and that restraint is the rule the
// plan states as "no upgrade of agent claim to user instruction": the authority a decision was
// recorded with is a property of the record, and a command that re-narrated the result would be
// free to describe a hypothesis as a decision.
func whyBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	id := strings.TrimSpace(strings.Join(inv.Args, " "))
	if id == "" {
		return nil, UsageErrorf("qompack why: a decision id is required, e.g. qompack why d-4f2a")
	}
	return runTool(ctx, inv, mcp.ToolWhy, mcp.WhyArgs{DecisionID: id})
}
