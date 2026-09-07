package scheduler

import "strings"

// DropClass is the §8.4/§8.7 droppable-block ranking behind reclaimable(p). It lives in
// scheduler rather than daemon for two reasons: it is pure spec (the §2.2 compactable tool set)
// needing no import beyond the standard library, and test/replay/l3policy must be able to score
// candidates without importing internal/daemon, which is a composition root that nothing may
// import (00-ARCHITECTURE §3.2). daemon.ClassifyDrop is the adapter from store.ToolUseRecord
// onto DropClassOf.
type DropClass uint8

// The droppable classes, in ascending eviction priority.
const (
	// DropNone is not droppable: AgentTool, MCP results, anything unlisted.
	DropNone DropClass = iota
	// DropOrdinary is the §2.2 compactable tool set.
	DropOrdinary
	// DropSuperseded is store.StatusSuperseded (§8.1 item 3 "first candidates for eviction").
	DropSuperseded
	// DropEphemeral is retrieval-born content (§8.7) — "the FIRST eviction candidate, ahead of
	// ordinary tool results".
	DropEphemeral
)

// EvictionRank orders the §8.4 droppable ranking: ephemeral (3) > superseded (2) > ordinary (1)
// > none (0).
func (c DropClass) EvictionRank() int { return int(c) }

// compactableTools is Qompack.md §2.2 verbatim:
//
//	"FileRead, Bash/PowerShell, Grep, Glob, WebSearch, WebFetch, FileEdit, FileWrite"
//
// plus the wire names Claude Code actually sends for the same operations. Comparison is
// case-folded. "Only high-volume, reproducible results are targeted."
var compactableTools = map[string]struct{}{
	"fileread": {}, "read": {},
	"bash": {}, "powershell": {},
	"grep": {}, "glob": {},
	"websearch": {}, "webfetch": {},
	"fileedit": {}, "edit": {}, "multiedit": {},
	"filewrite": {}, "write": {},
}

// preservedTools is the other half of §2.2: "AgentTool and MCP results are preserved."
var preservedTools = map[string]struct{}{
	"agenttool": {}, "agent": {}, "task": {},
}

// mcpToolPrefix is the wire prefix every MCP tool name carries.
const mcpToolPrefix = "mcp__"

// DropClassOf assigns a droppable class. Order matters: ephemeral first, then superseded,
// then ordinary — this IS the §8.7 eviction order. A superseded MCP or Task result is still
// droppable: supersession is a stronger statement than the tool class ("Superseded reads …
// should never appear in a summary", §8.1 item 3).
func DropClassOf(tool string, ephemeral, superseded bool) DropClass {
	if ephemeral {
		return DropEphemeral
	}
	if superseded {
		return DropSuperseded
	}
	name := strings.ToLower(strings.TrimSpace(tool))
	if strings.HasPrefix(name, mcpToolPrefix) {
		return DropNone
	}
	if _, ok := preservedTools[name]; ok {
		return DropNone
	}
	if _, ok := compactableTools[name]; ok {
		return DropOrdinary
	}
	return DropNone
}
