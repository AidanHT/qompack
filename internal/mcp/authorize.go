package mcp

import (
	"context"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// Retrieval authorization (T13-TRUST / T20-M2-04's "security boundary"): a stored content hash or
// the host's own tool_use_id is an ADDRESS, never a credential. `recall`, `expand` and `re_read`
// each run the check in this file before a preview is built or any content is materialized —
// never after, and never merely because the caller supplied a well-formed hash or id.
//
// The check has two halves, and the second is finding S-1.
//
// The first is paths.Norm against the CURRENT project root: it rejects a lexically escaping path
// outright. Applying it here closed the gap that `expand` (by tool_use_id) and `recall` (over every
// hit) used to have, where a stored path was resolved with no re-check at all.
//
// It is not sufficient on its own, and test/security measured why. Norm is the store-key
// normaliser, so when EvalSymlinks lands OUTSIDE the root it DISCARDS the resolution and keeps the
// unresolved spelling — a deliberate anti-smuggling rule that keeps keys stable. The consequence is
// that a captured file whose parent directory has since been replaced by a link pointing out of the
// project normalises perfectly cleanly, and an authorization gate built on Norm alone has nothing
// to refuse: `expand` and `re_read` served the archived content for an address a live read of the
// same path would be denied today. (No byte of the linked-to file was ever returned — the bytes
// served were the ones captured from inside the project — so it was a policy divergence, not a data
// leak. It is still the policy this file documents.)
//
// So the second half asks paths.ResolvesInside, which is the question Norm may not answer: does
// this path, resolved on disk as far as it exists, still land inside the root? Junctions and mount
// points count on Windows, and a path whose leaf no longer exists is answered by the deepest
// component that does.
//
// This layer enforces recorded provenance and current filesystem scope. Effective
// host permission is a separate policy input; containment does not establish it.

// deniedBody is the explicit refusal shape every authorization check renders: found is always
// false and denied is always true, spelled out so a caller cannot mistake a policy refusal for "it
// is not there" (a plain miss) or "this build cannot say" (unavailable). Those are three different
// facts, and only this one means "materialization was refused on purpose".
type deniedBody struct {
	Found  bool   `json:"found"`
	Denied bool   `json:"denied"`
	Reason string `json:"reason"`
}

// denied renders one authorization refusal. reason never echoes the offending path: the refusal
// itself is not an oracle a caller can use to probe which paths exist outside the project.
func denied(reason string) deniedBody {
	return deniedBody{Denied: true, Reason: reason}
}

// authorizedDenialReason is the one sentence every authorization refusal in this package uses. It
// is stated once so a model that hits it in `expand`, `re_read` or a filtered `recall` hit learns
// one rule rather than several differently-worded ones.
const authorizedDenialReason = "authorization denied: the associated path is outside the " +
	"project or could not be resolved safely"

// authorizePath reports whether path may be resolved before its content is materialized.
//
// Empty paths have no filesystem scope and are refused here. authorizeOrigin
// separately recognizes known pathless producers; a missing Read path is not one.
func (h *handlers) authorizePath(path string) (ok bool, reason string) {
	if path == "" {
		return false, authorizedDenialReason
	}
	norm, err := paths.Norm(h.root, path)
	if err != nil {
		return false, authorizedDenialReason
	}
	if !paths.ResolvesInside(h.root, norm) {
		return false, authorizedDenialReason
	}
	return true, ""
}

// authorizeOrigin preserves legitimate pathless records without upgrading a lost
// file path (including a legacy record) into a permission grant. Unknown producers
// remain denied rather than being guessed to have no file dependency.
func (h *handlers) authorizeOrigin(tool, path string) (bool, string) {
	if path != "" {
		return h.authorizePath(path)
	}
	switch tool {
	case "Bash", "PowerShell", "UserPromptSubmit", "SubagentStop", mcpToolPrefix + ToolRecordEliminated:
		return true, ""
	default:
		return false, "authorization denied: the capture has no usable path provenance"
	}
}

// authorizeHash checks the complete origin set before any chunk is fetched.
// A store that cannot supply it must not silently fall back to trusting the hash.
func (h *handlers) authorizeHash(ctx context.Context, hash core.Hash) any {
	reader, ok := h.store.(store.ProvenanceReader)
	if !ok {
		h.m.Counter("mcp.provenance_unavailable").Add(1)
		return unavailable("content provenance is unavailable in this store")
	}
	origins, err := reader.ContentOrigins(ctx, hash)
	if err != nil || len(origins) == 0 {
		h.m.Counter("mcp.provenance_incomplete").Add(1)
		return unavailable("complete content provenance could not be established")
	}
	for _, origin := range origins {
		if ok, reason := h.authorizeOrigin(origin.Tool, origin.Path); !ok {
			return denied(reason)
		}
	}
	return nil
}
