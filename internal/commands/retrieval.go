package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/mcp"
)

// ToolResult is one SP-13 tool answer, as a command frontend passes it on.
//
// Content and Meta are carried through unchanged rather than flattened into fields this package
// chose. That is the whole point of the frontends: `recall`, `why` and `dropped` exist to put a
// slash command in front of the retrieval handlers, and a frontend that re-derives the answer into
// its own shape loses the distinctions those handlers were written to make — a plain miss
// (found:false), a policy refusal (denied:true) and "this build cannot say" (unavailable) are
// three different facts that all look like "no result" once they have been summarized.
type ToolResult struct {
	Tool string `json:"tool"`
	// IsError marks a TOOL error: the retrieval ran and failed. It is not a protocol error and it
	// is not an absence.
	IsError bool `json:"is_error"`
	// Ephemeral mirrors the handler's own §8.7 marking. It does not claim anything about host
	// eviction (ADR 0013).
	Ephemeral bool           `json:"ephemeral,omitempty"`
	Content   []mcp.Content  `json:"content"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// callTool dispatches one SP-13 tool call and wraps its answer.
//
// It never implements a retrieval itself. A frontend whose server is absent reports unavailable —
// which is a different answer from an empty result set, and the one case where saying "no matches"
// would be a lie.
func callTool(ctx context.Context, inv Invocation, name string, args any) (ToolResult, error) {
	if inv.Deps.MCP == nil {
		return ToolResult{Tool: name}, fmt.Errorf(
			"%w: no retrieval server is wired into this build, so %q cannot be answered here",
			ErrUnavailable, name)
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return ToolResult{Tool: name}, fmt.Errorf("encoding %s arguments: %w", name, err)
	}

	resp, err := mcp.Dispatch(ctx, inv.Deps.MCP, mcp.Request{
		Name:     name,
		Args:     raw,
		Deadline: inv.Now.Add(toolDeadline),
	})
	if err != nil {
		if errors.Is(err, mcp.ErrToolNotFound) {
			return ToolResult{Tool: name}, fmt.Errorf(
				"%w: this build registers no %q tool", ErrUnavailable, name)
		}
		return ToolResult{Tool: name}, fmt.Errorf("%s: %w", name, err)
	}

	res := ToolResult{
		Tool:      name,
		IsError:   resp.IsError,
		Ephemeral: resp.Ephemeral,
		Content:   resp.Content,
		Meta:      resp.Meta,
	}
	if resp.IsError {
		// The content is kept: a failed retrieval that says why is more useful than an error
		// message this package invented on its own.
		return res, fmt.Errorf("%s: %s", name, firstText(resp.Content))
	}
	return res, nil
}

// toolDeadline bounds one frontend-issued retrieval. It is generous compared with budget B-F,
// because a person waiting at a terminal is not the model mid-turn that B-F protects.
const toolDeadline = 30 * time.Second

// firstText returns the first text block's payload, for an error message.
func firstText(blocks []mcp.Content) string {
	for _, c := range blocks {
		if c.Text != "" {
			return c.Text
		}
	}
	return "no detail reported"
}

// renderToolResult writes a tool answer for a person.
//
// The content blocks the handler produced are printed verbatim. The metadata is printed after
// them, sorted, because it is where the coverage, scope and refusal facts live and dropping it
// would leave a preview that reads as complete when it is not.
func renderToolResult(inv Invocation, res ToolResult) error {
	rw := &errWriter{w: inv.Out}

	for _, c := range res.Content {
		if c.Text == "" {
			continue
		}
		rw.printf("%s\n", strings.TrimRight(c.Text, "\n"))
	}

	if len(res.Meta) > 0 {
		rw.printf("\nevidence\n")
		for _, k := range sortedAnyKeys(res.Meta) {
			rw.printf("  %-20s %v\n", k, res.Meta[k])
		}
	}
	if res.Ephemeral {
		rw.printf("\nmarked ephemeral by Qompack; this says nothing about host eviction\n")
	}
	return rw.err
}

// sortedAnyKeys returns m's keys in sorted order.
func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// marshalResult renders a ToolResult as the envelope's Data member.
func marshalResult(res ToolResult) (json.RawMessage, error) {
	b, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("encoding %s result: %w", res.Tool, err)
	}
	return b, nil
}

// runTool is the body shape the three retrieval frontends share: dispatch, render for a person
// unless --json was asked for, and return the Data member either way.
func runTool(ctx context.Context, inv Invocation, name string, args any) (json.RawMessage, error) {
	res, callErr := callTool(ctx, inv, name, args)

	// A call that never reached a handler has nothing to show; report it and stop.
	if callErr != nil && res.Content == nil {
		return nil, callErr
	}

	data, err := marshalResult(res)
	if err != nil {
		return nil, err
	}
	if !inv.JSON {
		if renderErr := renderToolResult(inv, res); renderErr != nil {
			return data, renderErr
		}
	}
	return data, callErr
}
