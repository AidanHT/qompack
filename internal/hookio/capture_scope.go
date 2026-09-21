package hookio

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/qompack/qompack/internal/paths"
)

// The path-scope trust boundary (V6-AUTH-1, authority-review §5/§7). A capture whose STRUCTURED
// file-path target escapes the project root — or whose target cannot be PROVEN to be inside it —
// must be refused before anything servable is persisted: the retrieval gate is a read-time filter
// over data that, for an out-of-project file, should never have been retained. The decision is made
// here, once, so the hook client (before the spool/WAL), the daemon admission gate (direct IPC and
// drained lines) and the observer (defence in depth) all reach the same answer from one
// implementation.
//
// It is NOT a command parser. Only the structured path keys a tool input carries are inspected;
// a shell tool's `command` is never treated as a file policy, so a legitimately pathless Bash /
// PowerShell / UserPromptSubmit / SubagentStop delivery is preserved. Pathless output stays
// untrusted archive data governed by redaction, which is a separate boundary.
//
// Everything fails CLOSED. Malformed, truncated, or ambiguous (duplicate structured key) input is
// unprovable, not admissible: a partial JSON prefix cannot prove its own nature — field order is
// not a JSON contract, so tool_name/tool_input may sit after a cut, and a duplicate key leaves the
// effective value ambiguous. Unprovable retains no raw bytes; it is recorded as unavailable, never
// as a false absence.

// ScopeVerdict is the path-scope boundary's answer for one capture.
type ScopeVerdict int

const (
	// ScopeAllow means the input was a single, complete, unambiguous object and either no structured
	// path was submitted by a producer that is not a file-content producer, or every structured path
	// submitted resolves inside the project root both lexically and physically. Bytes may be
	// retained; the retrieval gate remains the read-time control.
	ScopeAllow ScopeVerdict = iota
	// ScopeOutOfProject means a structured path was submitted and at least one escapes the project
	// root — before or after on-disk resolution. This is a PROVEN escape: persist nothing servable
	// and resolve the delivery as denied (a decision), terminally.
	ScopeOutOfProject
	// ScopeUnprovable means containment cannot be established: a file-content producer that submitted
	// no structured path (dropped), or a payload that is malformed, truncated, or ambiguous so that
	// its producer/path metadata cannot be proven. It is NOT a proven escape and must not be recorded
	// as an absence — it is recorded as UNAVAILABLE, with no raw bytes retained.
	ScopeUnprovable
)

// Refuses reports whether the verdict forbids retaining raw bytes. Both a proven escape and an
// unprovable capture refuse; the caller distinguishes them (denied vs unavailable) via the verdict
// itself, because they are different truths — one is proven outside, the other is unproven.
func (v ScopeVerdict) Refuses() bool { return v == ScopeOutOfProject || v == ScopeUnprovable }

// Reason is a closed label for the verdict. It never contains a path or any payload-derived text,
// so it is safe to log and to carry as an audit reason.
func (v ScopeVerdict) Reason() string {
	switch v {
	case ScopeOutOfProject:
		return "path scope: out of project"
	case ScopeUnprovable:
		return "path scope: unprovable"
	default:
		return "path scope: in project"
	}
}

// Scope is CaptureScope's full answer: the verdict, and — only when the verdict is ScopeAllow and a
// structured path was present — the paths.Key of the first structured path, so a caller that already
// derives that key (the observer) reads it from the single place the containment decision was made
// rather than recomputing a normalization the boundary might disagree with.
type Scope struct {
	Verdict    ScopeVerdict
	PrimaryKey string
}

// fileContentProducers are the tools whose result IS the content of a path, in BOTH the host's own
// spelling and the observer's display spelling, because the daemon and hook client hold a raw
// tool_name while the observer holds a normalized one and all three must classify identically. A
// producer in this set that submits no provable structured path is ScopeUnprovable; a producer that
// is pathless by nature (Bash/PowerShell/Grep/Glob/Web/…) with no structured path is allowed, its
// content governed by redaction.
var fileContentProducers = map[string]bool{
	"Read": true, "NotebookRead": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true,
	"Write": true, "FileRead": true, "FileEdit": true, "FileWrite": true,
}

// CaptureScope decides the boundary for one capture from its tool name and structured tool input.
//
// EVERY structured path is checked, not just the first: a MultiEdit whose first edit is in-project
// and whose second escapes must be refused on the second. Containment is both lexical (paths.Norm,
// which rejects an escape and refuses to follow an outward symlink) and physical
// (paths.ResolvesInside, which walks the path on disk component by component and refuses a directory
// since swapped for a junction/mount pointing outside the project) — either failing is a refusal.
//
// Malformed or ambiguous (duplicate structured key) tool input is unprovable, never silently
// admitted: it fails closed.
func CaptureScope(projectRoot, toolName string, toolInput json.RawMessage) Scope {
	sp, ok := structuredPaths(toolInput)
	if !ok {
		return Scope{Verdict: ScopeUnprovable}
	}
	if len(sp) > 0 {
		var primary string
		for i, p := range sp {
			n, err := paths.Norm(projectRoot, p)
			if err != nil || !paths.ResolvesInside(projectRoot, p) {
				return Scope{Verdict: ScopeOutOfProject}
			}
			if i == 0 {
				primary = paths.Key(n)
			}
		}
		return Scope{Verdict: ScopeAllow, PrimaryKey: primary}
	}
	if fileContentProducers[toolName] {
		return Scope{Verdict: ScopeUnprovable}
	}
	return Scope{Verdict: ScopeAllow}
}

// CaptureScopeRaw decides the boundary from a raw host payload — the caller that has no trusted
// Event: a delivery admitted only as an evidence prefix, a request that arrived as Raw alone, or a
// forged capture whose already-redacted bytes must be scoped independently.
//
// It makes NO assumption about field order: a partial prefix whose tool_name/tool_input arrive after
// a cut, a truncated value, a non-object, or a duplicate tool_name/tool_input all fail closed to
// ScopeUnprovable. Only a single, complete, unambiguous object is decided on its merits — so a
// prefix that cannot prove its own nature never passes opaque bytes forward.
func CaptureScopeRaw(projectRoot string, raw []byte) Scope {
	name, input, ok := rawToolFields(raw)
	if !ok {
		return Scope{Verdict: ScopeUnprovable}
	}
	return CaptureScope(projectRoot, name, input)
}

// scopePathInput mirrors the structured path keys observer.PathsFromInput reads. It is duplicated
// here rather than imported because observer imports hookio, not the reverse, and the key set is a
// property of Claude Code's tool inputs, not of either package.
type scopePathInput struct {
	FilePath     string `json:"file_path"`
	Path         string `json:"path"`
	NotebookPath string `json:"notebook_path"`
}

// structuredPaths extracts every structured path a tool input names, in PathsFromInput's order,
// first-appearance deduplicated, with empties dropped. It reports ok=false when the input cannot be
// trusted to be exhaustive: a non-object, a malformed or truncated object, or a DUPLICATE of any
// structured path key (which leaves the effective value ambiguous). An empty input is a well-formed
// "no paths". A shell command is never read as a path.
func structuredPaths(toolInput json.RawMessage) ([]string, bool) {
	if len(toolInput) == 0 || bytes.Equal(bytes.TrimSpace(toolInput), []byte("null")) {
		return nil, true
	}
	dec := json.NewDecoder(bytes.NewReader(toolInput))
	t, err := dec.Token()
	if err != nil || t != json.Delim('{') {
		return nil, false // not a single well-formed object: cannot be proven exhaustive
	}
	var v scopePathInput
	var editPaths []string
	seen := map[string]int{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, isStr := keyTok.(string)
		if !isStr {
			return nil, false
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, false
		}
		seen[key]++
		switch key {
		case "file_path":
			if json.Unmarshal(val, &v.FilePath) != nil {
				return nil, false
			}
		case "path":
			if json.Unmarshal(val, &v.Path) != nil {
				return nil, false
			}
		case "notebook_path":
			if json.Unmarshal(val, &v.NotebookPath) != nil {
				return nil, false
			}
		case "edits":
			var edits []json.RawMessage
			if json.Unmarshal(val, &edits) != nil {
				return nil, false
			}
			for _, edit := range edits {
				ep, ok := structuredPaths(edit)
				if !ok {
					return nil, false
				}
				editPaths = append(editPaths, ep...)
			}
		}
	}
	if !closesCleanly(dec) {
		return nil, false
	}
	if seen["file_path"] > 1 || seen["path"] > 1 || seen["notebook_path"] > 1 || seen["edits"] > 1 {
		return nil, false // ambiguous: a duplicated structured key cannot be resolved safely
	}

	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		for _, s := range out {
			if s == p {
				return
			}
		}
		out = append(out, p)
	}
	add(v.FilePath)
	add(v.Path)
	add(v.NotebookPath)
	for _, p := range editPaths {
		add(p)
	}
	return out, true
}

// rawToolFields reads the top-level tool_name and tool_input from a raw payload, requiring a single,
// complete, unambiguous object. Any truncation, malformation, non-object, non-string key, or
// duplicate tool_name/tool_input fails closed (ok=false), because a prefix cannot prove there is no
// later path field or a shadowing duplicate.
func rawToolFields(raw []byte) (name string, input json.RawMessage, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil || t != json.Delim('{') {
		return "", nil, false
	}
	seen := map[string]int{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return "", nil, false
		}
		key, isStr := keyTok.(string)
		if !isStr {
			return "", nil, false
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return "", nil, false
		}
		seen[key]++
		switch key {
		case "tool_name":
			if json.Unmarshal(val, &name) != nil {
				return "", nil, false
			}
		case "tool_input":
			input = val
		}
	}
	if !closesCleanly(dec) {
		return "", nil, false
	}
	if seen["tool_name"] > 1 || seen["tool_input"] > 1 {
		return "", nil, false
	}
	return name, input, true
}

// closesCleanly consumes the object's closing brace and confirms nothing follows it, so a decoder
// left mid-stream (a truncated object) or one with trailing garbage after a complete object both
// fail. It is what turns "field order is not a contract" into a refusal rather than a guess.
func closesCleanly(dec *json.Decoder) bool {
	t, err := dec.Token()
	if err != nil || t != json.Delim('}') {
		return false
	}
	_, err = dec.Token()
	return err == io.EOF
}
