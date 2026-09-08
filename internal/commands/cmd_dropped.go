package commands

import (
	"context"
	"encoding/json"

	"github.com/qompack/qompack/internal/mcp"
)

// droppedBody implements `/qompack:dropped`.
//
// It reuses SP-13's `dropped` handler, which reports Qompack's own omission records: what Qompack
// chose not to carry forward, and how to get it back.
//
// It reports nothing about what the HOST dropped. Qompack observes its own compaction decisions
// and cannot see native eviction, so a report that presented these categories as the full account
// of what left the context would be claiming a coverage nothing in this build has. The handler's
// own coverage members say which of the two this is; they are passed through rather than
// summarized away.
func droppedBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	if len(inv.Args) > 0 {
		return nil, UsageErrorf(
			"qompack dropped: takes no arguments; what was dropped is a property of the session, not of the question")
	}
	return runTool(ctx, inv, mcp.ToolDropped, mcp.DroppedArgs{})
}
