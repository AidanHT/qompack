package core

import "strings"

// hostToDisplay maps Claude Code's own tool names onto the display names Qompack.md §2.2 and §8.1
// are written in terms of. The indirection exists because the host's vocabulary is not stable and
// is not ours: Read/NotebookRead are one kind of thing to this system, Edit/MultiEdit/NotebookEdit
// another, and Task is the agent tool §2.2 preserves rather than compacts. Collapsing them means the
// observer — supersession classes, the compactable predicate, the tombstone, the action grammar —
// reasons about four file operations and a handful of searches instead of fifteen host spellings,
// and it is the name every tool-use record is indexed under.
//
// It lives here rather than in internal/observer because two sides must agree on it: the observer
// that records a host tool under its display name, and the store's recall `tool:` selector, which
// has to answer a host name ("Read") with the records indexed under the display name ("FileRead").
// A second copy of the table in either place would drift the first time the host adds a tool.
//
// A name that misses this table passes through unchanged, which is what keeps qompack's own
// mcp__qompack__* retrieval results recognizable to the §8.7 ephemeral rule.
var hostToDisplay = map[string]string{
	"Read": "FileRead", "NotebookRead": "FileRead", "Edit": "FileEdit",
	"MultiEdit": "FileEdit", "NotebookEdit": "FileEdit", "Write": "FileWrite",
	"Bash": "Bash", "BashOutput": "Bash", "PowerShell": "PowerShell",
	"Grep": "Grep", "Glob": "Glob", "WebSearch": "WebSearch", "WebFetch": "WebFetch",
	"Task": "AgentTool", "TodoWrite": "TodoWrite",
}

// DisplayToolName returns the display name for a host tool name — "Read" becomes "FileRead" — and
// returns hostName unchanged when the table does not claim it, so a tool Claude Code adds tomorrow
// is still recorded under a name rather than lost. The lookup is exact: the host spells its tool
// names one way, and the observer records exactly what the host sent.
func DisplayToolName(hostName string) string {
	if display, ok := hostToDisplay[hostName]; ok {
		return display
	}
	return hostName
}

// DisplayToolNameFold is DisplayToolName for a name a PERSON or a model typed, where the case is
// not the host's: "read" and "READ" both answer "FileRead". An exact spelling wins, and a name the
// table does not claim in any case passes through unchanged.
func DisplayToolNameFold(name string) string {
	if display, ok := hostToDisplay[name]; ok {
		return display
	}
	for host, display := range hostToDisplay {
		if strings.EqualFold(host, name) {
			return display
		}
	}
	return name
}
