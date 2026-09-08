package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/pins"
)

// Pin sources. pins.Invariant.Source is "user" or "agent" and nothing else; the two are not
// interchangeable, because §8.3's authority order treats a user correction as outranking an agent
// claim and a store that cannot tell them apart cannot apply it.
const (
	sourceUser  = "user"
	sourceAgent = "agent"
)

// PinList is what `pin --list` reports.
type PinList struct {
	Invariants []pins.Invariant `json:"invariants"`
}

// PinChange is what an add or a remove reports.
type PinChange struct {
	Action string `json:"action"`
	ID     string `json:"id"`
	Source string `json:"source,omitempty"`
	// Existing marks an add that changed nothing because the invariant was already live. It is
	// reported rather than hidden: a caller that pinned the same text twice should be able to see
	// that the second call was a no-op, not assume it re-established something.
	Existing bool `json:"existing,omitempty"`
}

// pinBody implements `/qompack:pin`, which has four modes: add, list, remove and record an
// elimination.
func pinBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	_, list := inv.Flag("list")
	removeID, remove := inv.Flag("remove")
	_, eliminated := inv.Flag("eliminated")

	if err := exactlyOneMode(inv, list, remove, eliminated); err != nil {
		return nil, err
	}

	switch {
	case list:
		return pinListBody(ctx, inv)
	case remove:
		return pinRemoveBody(ctx, inv, removeID)
	case eliminated:
		return pinEliminatedBody(ctx, inv)
	default:
		return pinAddBody(ctx, inv)
	}
}

// exactlyOneMode rejects a combination that would silently pick one behaviour over another.
func exactlyOneMode(inv Invocation, list, remove, eliminated bool) error {
	n := 0
	for _, on := range []bool{list, remove, eliminated} {
		if on {
			n++
		}
	}
	if n > 1 {
		return UsageErrorf("qompack pin: --list, --remove and --eliminated are separate modes; pick one")
	}
	if n == 1 && len(inv.Args) > 0 && !eliminated {
		return UsageErrorf("qompack pin: %q is not used in this mode", strings.Join(inv.Args, " "))
	}
	return nil
}

// pinAddBody adds an invariant.
//
// Source defaults to "user" because the slash command is something a person types. An agent that
// shells out to the same binary must say so with --source agent: §8.3 lets a user correction
// outrank an agent claim, and an agent that could record its own claim as a user instruction would
// be able to promote its own authority — the one upgrade this frontend must never perform.
//
// A re-add of live text changes nothing, because pins.Store.Add is idempotent by an id minted from
// the text. That is also what protects an existing record's source: re-running the command cannot
// rewrite an agent pin into a user one.
func pinAddBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	text := strings.TrimSpace(strings.Join(inv.Args, " "))
	if text == "" {
		return nil, UsageErrorf("qompack pin: some text to pin is required, e.g. qompack pin \"the API is versioned\"")
	}

	source, err := pinSource(inv)
	if err != nil {
		return nil, err
	}
	if inv.Deps.Pins == nil {
		return nil, fmt.Errorf("%w: no pin store is wired into this build", ErrUnavailable)
	}

	id := pins.MintID(text)
	before, listErr := inv.Deps.Pins.All(ctx)
	if listErr != nil {
		return nil, fmt.Errorf("pin: reading current pins: %w", listErr)
	}
	existing := containsPin(before, id)

	if err := inv.Deps.Pins.Add(ctx, pins.Invariant{
		ID:     id,
		Text:   text,
		Source: source,
		Pinned: core.UnixMilli(inv.Now.UnixMilli()),
	}); err != nil {
		return nil, fmt.Errorf("pin: %w", err)
	}

	change := PinChange{Action: "add", ID: id, Source: source, Existing: existing}
	if !inv.JSON {
		if existing {
			fmt.Fprintf(inv.Out, "already pinned as %s (unchanged; its recorded source stands)\n", id)
		} else {
			fmt.Fprintf(inv.Out, "pinned %s (source: %s)\n", id, source)
		}
	}
	return json.Marshal(change)
}

// pinSource resolves --source, defaulting to "user".
func pinSource(inv Invocation) (string, error) {
	raw, ok := inv.Flag("source")
	if !ok {
		return sourceUser, nil
	}
	switch raw {
	case sourceUser, sourceAgent:
		return raw, nil
	default:
		return "", UsageErrorf("qompack pin: --source takes %q or %q, got %q", sourceUser, sourceAgent, raw)
	}
}

// containsPin reports whether id is already live.
func containsPin(all []pins.Invariant, id string) bool {
	for _, p := range all {
		if p.ID == id {
			return true
		}
	}
	return false
}

// pinListBody reports the invariants currently in scope, with the source each was recorded under.
func pinListBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	if inv.Deps.Pins == nil {
		return nil, fmt.Errorf("%w: no pin store is wired into this build", ErrUnavailable)
	}
	all, err := inv.Deps.Pins.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("pin --list: %w", err)
	}

	if !inv.JSON {
		if len(all) == 0 {
			fmt.Fprintln(inv.Out, "no invariants are pinned in this project")
		}
		for _, p := range all {
			fmt.Fprintf(inv.Out, "%s  %-6s  %s\n", p.ID, p.Source, p.Text)
		}
	}
	return json.Marshal(PinList{Invariants: all})
}

// pinRemoveBody tombstones an invariant.
//
// The log keeps the original add record: pins.Store.Remove appends a tombstone rather than
// rewriting history, so what was pinned, by whom, and when it was withdrawn all remain readable.
func pinRemoveBody(ctx context.Context, inv Invocation, id string) (json.RawMessage, error) {
	if strings.TrimSpace(id) == "" {
		return nil, UsageErrorf("qompack pin: --remove needs an invariant id, e.g. --remove inv_7c1a9e2f4b60")
	}
	if inv.Deps.Pins == nil {
		return nil, fmt.Errorf("%w: no pin store is wired into this build", ErrUnavailable)
	}

	if err := inv.Deps.Pins.Remove(ctx, id); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil, fmt.Errorf("pin --remove: no invariant %q has ever been recorded here", id)
		}
		return nil, fmt.Errorf("pin --remove: %w", err)
	}

	if !inv.JSON {
		fmt.Fprintf(inv.Out, "removed %s (the add record is retained; a tombstone was appended)\n", id)
	}
	return json.Marshal(PinChange{Action: "remove", ID: id})
}

// pinEliminatedBody records an elimination through SP-13's own write handler.
//
// --depends-on is REQUIRED here even though the tool's schema tolerates its absence. An
// elimination with no dependencies can never go stale (§8.3), and negative knowledge that cannot
// expire eventually blocks an approach that has since become viable. A command that made it easy
// to record one would be building that failure in.
func pinEliminatedBody(ctx context.Context, inv Invocation) (json.RawMessage, error) {
	args := mcp.RecordEliminatedArgs{
		Target:   strings.TrimSpace(flagValue(inv, "target")),
		Approach: strings.TrimSpace(flagValue(inv, "approach")),
		Reason:   strings.TrimSpace(flagValue(inv, "reason")),
		Scope:    strings.TrimSpace(flagValue(inv, "scope")),
	}
	if deps := strings.TrimSpace(flagValue(inv, "depends-on")); deps != "" {
		for _, p := range strings.Split(deps, ",") {
			if p = strings.TrimSpace(p); p != "" {
				args.DependsOn = append(args.DependsOn, p)
			}
		}
	}

	switch {
	case args.Target == "":
		return nil, UsageErrorf("qompack pin --eliminated: --target is required")
	case args.Approach == "":
		return nil, UsageErrorf("qompack pin --eliminated: --approach is required")
	case args.Reason == "":
		return nil, UsageErrorf("qompack pin --eliminated: --reason is required")
	case len(args.DependsOn) == 0:
		return nil, UsageErrorf(
			"qompack pin --eliminated: --depends-on is required; an elimination with no dependencies " +
				"can never go stale, and one that cannot expire eventually blocks a viable approach")
	}

	return runTool(ctx, inv, mcp.ToolRecordEliminated, args)
}

// flagValue returns a flag's value, or "" when it was not given.
func flagValue(inv Invocation, name string) string {
	v, _ := inv.Flag(name)
	return v
}
