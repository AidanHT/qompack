package rehydrate

import (
	"bytes"
	"context"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// Small tool results are restored inline under their pointer, fenced, instead of by pointer alone.
//
// The live eval (c55-c8) made no Qompack MCP call in 20 sessions, so a result restored only as a
// pointer was a result lost: a 22-byte seed printed by `go run ./cmd/seed` was not recalled. A
// result this small costs less inline than the expand call that would fetch it.
//
// What is inlined is exactly what expand(tool_use_id=…) would return for the record, and nothing
// expand would refuse: the same tool_use index entry and object-store root, read through Store.Open;
// the same origin rule (a path-bearing record's path must resolve inside the project and pass the
// host's Read rules, through this build's pathJudge; a pathless record must come from a producer
// internal/mcp's authorizeOrigin accepts); and today's redaction policy applied to the bytes, as
// expand's redactForRetrieval does. Without Deps.Redact nothing is inlined (fail closed, as expand
// serves no archive text without a redactor). A pointer whose summary the build withholds, a
// superseded record, bytes that are not UTF-8 text, and bytes holding a Qompack tag are never
// inlined either; the pointer line alone stands for them, as before.
const (
	// inlineMaxBytes is the largest result inlined (after redaction).
	inlineMaxBytes = 512
	// inlineTotalBytes bounds the inlined bytes of one payload, in builder order, to eight
	// full-size results, so that the block cannot balloon whatever a checkpoint holds. Every
	// inlined byte is also priced against the budget and the host ceiling like any other unit text.
	inlineTotalBytes = 8 * inlineMaxBytes
	// inlineMaxAttempts bounds the inline reads of one build: each is an index lookup, a store read
	// and a path resolution on the SessionStart(compact) path, paid whether or not the budget later
	// keeps the pointer, so a checkpoint's tool-pointer count must not set their number.
	inlineMaxAttempts = 16
)

// inlinePathless are the producers of a pathless record whose result may be inlined: the pathless
// producers internal/mcp's authorizeOrigin accepts for expand, less its own retrieval tools.
var inlinePathless = map[string]bool{"Bash": true, "PowerShell": true, "UserPromptSubmit": true, "SubagentStop": true}

// inlineOutput returns the fenced block to render under tool pointer t, and the number of content
// bytes it carries, or ("", 0) when the result must not or need not be inlined. room is how many
// inlined bytes this payload still allows.
func inlineOutput(ctx context.Context, r Request, d Deps, j pathJudge, t checkpoint.ToolPointer, room int) (string, int) {
	if d.Store == nil || d.Redact == nil || room <= 0 {
		return "", 0
	}
	rec, err := d.Store.ToolUse(ctx, t.ToolUseID)
	if err != nil || rec.Root == (core.Hash{}) || rec.Status != store.StatusOK {
		return "", 0
	}
	if t.Hash != (core.Hash{}) && t.Hash != rec.Root {
		// The checkpoint names other content than the index now holds: inline neither.
		return "", 0
	}
	if rec.Bytes > inlineMaxBytes {
		return "", 0
	}
	if !inlineOriginAllowed(r, j, rec) {
		return "", 0
	}

	rc, err := d.Store.Open(ctx, rec.Root)
	if err != nil || rc == nil {
		return "", 0
	}
	raw, readErr := io.ReadAll(io.LimitReader(rc, inlineMaxBytes+1))
	_ = rc.Close()
	if readErr != nil || len(raw) > inlineMaxBytes {
		return "", 0
	}
	out := d.Redact(raw)
	if len(out) == 0 || len(out) > inlineMaxBytes || len(out) > room {
		return "", 0
	}
	if !utf8.Valid(out) || bytes.IndexByte(out, 0) >= 0 || hasQompackTag(out) {
		// Binary bytes do not belong in a text payload, and a Qompack tag inside the payload would
		// end the tagged span early when the transcript is next read.
		return "", 0
	}
	return fenceBlock(string(out)), len(out)
}

// hasQompackTag reports whether b holds the start of a Qompack comment tag, open or close, in any
// spelling: only a tag can end the payload's tagged span early. Prose naming a slash command
// (/qompack:status) is not one.
func hasQompackTag(b []byte) bool {
	return bytes.Contains(b, []byte("<!-- qompack:")) || bytes.Contains(b, []byte("<!-- /qompack:"))
}

// inlineOriginAllowed is expand's origin rule for rec (internal/mcp authorizeOrigin), judged with
// this build's path judge.
func inlineOriginAllowed(r Request, j pathJudge, rec store.ToolUseRecord) bool {
	if rec.Path == "" {
		return inlinePathless[rec.Tool]
	}
	if j.rulesUnavailable() || j.withheld(rec.Path) {
		return false
	}
	norm, err := paths.Norm(r.ProjectRoot, rec.Path)
	return err == nil && paths.ResolvesInside(r.ProjectRoot, norm)
}

// fenceBlock fences s verbatim with a backtick run longer than any s holds, so no line of s can
// close it, and ends s with a newline when it has none before the closing fence.
func fenceBlock(s string) string {
	fence := codeFence
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return fence + "\n" + s + fence + "\n"
}
