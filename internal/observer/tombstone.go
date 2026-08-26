package observer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/store"
)

// bytesPerKB is the binary divisor humanBytes renders sizes with: 1 KB is 1024 bytes, not 1000.
// The distinction is load-bearing rather than pedantic — 2457 bytes renders as "2.4KB" at 1024
// and "2.5KB" at 1000, and Qompack.md §8.1's example tombstone says 2.4KB, so a decimal divisor
// would silently break the golden.
const bytesPerKB = 1024 //nomagic:allow binary byte-unit divisor for size rendering, not store.chunk.min

// sizeUnits are the suffixes humanBytes steps through above bytesPerKB. A tool result larger than
// the last one is rendered in that unit rather than overflowing into an unnamed one.
var sizeUnits = [...]string{"KB", "MB", "GB"}

// The literal parts of the marker Qompack.md §8.1 item 2 specifies. They are named rather than
// inlined so tombstoneFixedBytes below can be computed from them instead of being a number that
// silently stops matching the format string it was derived from.
const (
	// tombstoneOpen begins every marker and carries the hash algorithm the short form belongs to.
	tombstoneOpen = "[cleared: sha256:"
	// tombstoneSep is the field separator: U+00B7 MIDDLE DOT with one space on each side, exactly
	// as the design document draws it.
	tombstoneSep = " · "
	// tombstoneEllipsis follows the short hash, saying it is an elision of the full digest rather
	// than a differently-shaped identifier.
	tombstoneEllipsis = "…"
	// tombstoneEphemeral marks a result Qompack itself put in the window (§8.7).
	tombstoneEphemeral = "ephemeral"
	// tombstoneSuperseded marks a result a later tool use on the same path replaced (§8.1 item 3).
	tombstoneSuperseded = "superseded"
	// tombstoneClose ends every marker with the promise the whole design rests on.
	tombstoneClose = "re-expandable]"
)

// tombstoneShortHashLen is the width of core.Hash.Short: the first 12 hex characters of the digest
// (00-ARCHITECTURE.md §4). It is fixed for every hash, so it belongs in the Grow estimate below.
const tombstoneShortHashLen = 12

// tombstonePreviewRunes caps the ArgsPreview fallback subject. A tombstone is a one-line marker
// injected into a context window that is already under pressure, so an unbounded subject would let
// one pathological command line cost more than the result it replaced. store.ToolUseRecord caps
// ArgsPreview at 120 characters of its own; this is the tighter cap the marker itself needs.
const tombstonePreviewRunes = 48

// tombstoneFixedBytes is the worst-case byte length of every part of a marker that does not depend
// on a variable-length field: the literal open and close, the short hash and its ellipsis, the
// three separators that are always present, the space before a subject, and both optional status
// segments. It is only a strings.Builder.Grow hint — an over-estimate costs a few bytes of slack,
// while an under-estimate would cost a second allocation on the hot path — so it is deliberately
// the largest a marker's fixed part can be.
const tombstoneFixedBytes = len(tombstoneOpen) + tombstoneShortHashLen + len(tombstoneEllipsis) +
	3*len(tombstoneSep) + 1 +
	len(tombstoneSep) + len(tombstoneEphemeral) +
	len(tombstoneSep) + len(tombstoneSuperseded) +
	len(tombstoneClose)

// Tombstone renders the addressable marker of Qompack.md §8.1 item 2: the one-line trace a
// cleared tool result leaves behind, carrying enough identity to fetch the content back.
//
//	[cleared: sha256:a3f2c9e14b70… · 2.4KB · FileRead src/auth.ts · re-expandable]
//
// The point of the marker is that clearing a result is reversible: the short hash is a real store
// address, so `expand` can retrieve exactly what was cleared. A tombstone that merely said
// "[cleared]" would turn a cache eviction into data loss, which is what closes G3.2 at near-zero
// cost.
//
// The grammar, in order: the "sha256:" prefix and the 12 hex characters core.Hash.Short produces,
// so a marker can be pasted straight back into a retrieval call; the ellipsis that says those 12
// are an elision; the size; the DISPLAY tool name (§2.2), never the host's own spelling; the
// subject, which is the record's path when it has one and otherwise its argument preview truncated
// to tombstonePreviewRunes; then ` · ephemeral` and ` · superseded` when they apply, in that order;
// then the closing promise. A record with neither a path nor a preview omits the subject group and
// its leading space entirely rather than rendering a double separator.
//
// Tombstone renders for ANY record, including one IsCompactable rejects: a caller may want an
// addressable marker for a result it is eliding for its own budget reasons, and whether a result
// MAY be elided is the caller's decision rather than the renderer's.
//
// It performs no I/O and takes no lock, so it is safe to call from anywhere on the hot path.
func Tombstone(rec store.ToolUseRecord) string {
	tool := NormalizeToolName(rec.Tool)
	size := humanBytes(rec.Bytes)
	subject := tombstoneSubject(rec)

	var b strings.Builder
	b.Grow(tombstoneFixedBytes + len(size) + len(tool) + len(subject))

	b.WriteString(tombstoneOpen)
	b.WriteString(rec.Root.Short())
	b.WriteString(tombstoneEllipsis)
	b.WriteString(tombstoneSep)
	b.WriteString(size)
	b.WriteString(tombstoneSep)
	b.WriteString(tool)
	if subject != "" {
		b.WriteByte(' ')
		b.WriteString(subject)
	}
	if rec.Ephemeral {
		b.WriteString(tombstoneSep)
		b.WriteString(tombstoneEphemeral)
	}
	if rec.Status == store.StatusSuperseded {
		b.WriteString(tombstoneSep)
		b.WriteString(tombstoneSuperseded)
	}
	b.WriteString(tombstoneSep)
	b.WriteString(tombstoneClose)

	return b.String()
}

// tombstoneSubject returns the marker's subject: the record's path when it has one, else its
// argument preview truncated to tombstonePreviewRunes, else "" for a record that identifies
// nothing. The path is never truncated — it is the retrieval key a reader is most likely to want
// whole.
func tombstoneSubject(rec store.ToolUseRecord) string {
	if rec.Path != "" {
		return rec.Path
	}
	return truncateRunes(rec.ArgsPreview, tombstonePreviewRunes)
}

// truncateRunes returns s unchanged when it is at most limit runes long, and otherwise its first
// limit-1 runes followed by the ellipsis, so the result is exactly limit runes wide. It counts
// runes rather than bytes because a preview can carry any UTF-8 the host sent, and slicing bytes
// would be able to cut a rune in half.
func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	n := 0
	for i := range s {
		if n == limit-1 {
			return s[:i] + tombstoneEllipsis
		}
		n++
	}
	return s
}

// tombstoneNoteText is the expand affordance: the one line that tells a reader what to do with the
// markers above it. SP-11 emits it once per rehydration block and SP-13 once per `expand` response.
// It names both addresses a marker can be resolved by, because the sha256 survives a session and
// the tool_use_id is what the transcript itself carries.
const tombstoneNoteText = "Cleared tool results above are re-expandable: call the qompack MCP tool " +
	"expand with the sha256 shown in the marker, or with the tool_use_id."

// TombstoneNote returns the expand affordance line that accompanies a block of markers. It is one
// line, always: it is injected into structured output whose shape a second line would break.
func TombstoneNote() string { return tombstoneNoteText }

// hostToDisplay maps Claude Code's own tool names onto the display names Qompack.md §2.2 and §8.1
// are written in terms of. The indirection exists because the host's vocabulary is not stable and
// is not ours: Read/NotebookRead are one kind of thing to this system, Edit/MultiEdit/NotebookEdit
// another, and Task is the agent tool §2.2 preserves rather than compacts. Collapsing them here
// means the rest of the package — supersession classes, the compactable predicate, the tombstone,
// the action grammar — reasons about four file operations and a handful of searches instead of
// fifteen host spellings.
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

// NormalizeToolName returns the display name for a host tool name — "Read" becomes "FileRead" —
// and returns hostName unchanged when the table does not claim it, so a tool Claude Code adds
// tomorrow is still recorded under a name rather than lost.
func NormalizeToolName(hostName string) string {
	if display, ok := hostToDisplay[hostName]; ok {
		return display
	}
	return hostName
}

// compactableTools is the §2.2 set: the high-volume, reproducible results a tombstone may replace.
// Everything else — AgentTool and MCP results, in the design's own words — is preserved, because a
// subagent's returned summary and a retrieval result cannot be reproduced by re-running a tool.
var compactableTools = map[string]bool{
	"FileRead": true, "Bash": true, "PowerShell": true, "Grep": true, "Glob": true,
	"WebSearch": true, "WebFetch": true, "FileEdit": true, "FileWrite": true,
}

// IsCompactable reports whether a result produced by tool may legally be represented by a marker
// instead of its content (Qompack.md §2.2). It accepts either spelling — a host name or a display
// name — because a caller holding the raw tool_name off a hook payload should get the same answer
// as one holding a store.ToolUseRecord.Tool.
//
// It reports Claude Code's §2.2 host set and nothing else. Qompack's own retrieval results are
// governed by the §8.7 ephemeral rule carried on store.ToolUseRecord.Ephemeral instead, which is
// why an mcp__qompack__… result is simultaneously not compactable and first in the eviction order.
func IsCompactable(tool string) bool { return compactableTools[NormalizeToolName(tool)] }

// The supersession classes of §8.1 item 3. Supersession is keyed on the class rather than the
// exact tool because a FileWrite genuinely replaces an earlier FileRead of the same path, while a
// Grep of that path replaces neither.
const (
	// classFileContent covers the tools whose result IS the content of a path.
	classFileContent = "filecontent"
	// classSearch covers the tools whose result is a set of matches within a tree.
	classSearch = "search"
	// classExec covers the shells, whose result is the output of a command.
	classExec = "exec"
	// classWeb covers the network tools, whose result is a remote document.
	classWeb = "web"
)

// supersedableClasses maps a display name onto its supersession class. A tool absent from this
// table never supersedes and is never superseded, which is the correct answer for AgentTool (a
// subagent's summary is not a re-readable resource), for TodoWrite (each write is a distinct
// event), and for retrieval results.
var supersedableClasses = map[string]string{
	"FileRead": classFileContent, "FileEdit": classFileContent, "FileWrite": classFileContent,
	"Grep": classSearch, "Glob": classSearch,
	"Bash": classExec, "PowerShell": classExec,
	"WebFetch": classWeb, "WebSearch": classWeb,
}

// supersedableClass returns the supersession class of tool, or "" when results of that tool
// participate in supersession in neither direction. Like IsCompactable it accepts a host name or a
// display name.
func supersedableClass(tool string) string { return supersedableClasses[NormalizeToolName(tool)] }

// humanBytes renders n as a compact size with one decimal place and no space before the unit:
// under one KB as a whole number of bytes ("973B"), and above it in KB, MB or GB scaled by
// bytesPerKB ("2.4KB"). Sizes at or beyond the largest unit stay in that unit.
func humanBytes(n int64) string {
	if n < bytesPerKB {
		return strconv.FormatInt(n, 10) + "B"
	}
	v := float64(n) / bytesPerKB
	i := 0
	for i < len(sizeUnits)-1 && v >= bytesPerKB {
		v /= bytesPerKB
		i++
	}
	return fmt.Sprintf("%.1f%s", v, sizeUnits[i])
}
