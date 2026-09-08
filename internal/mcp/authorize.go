package mcp

import "github.com/qompack/qompack/internal/paths"

// Retrieval authorization (T13-TRUST / T20-M2-04's "security boundary"): a stored content hash or
// the host's own tool_use_id is an ADDRESS, never a credential. `recall`, `expand` and `re_read`
// each run the check in this file before a preview is built or any content is materialized —
// never after, and never merely because the caller supplied a well-formed hash or id.
//
// The check itself is the same one `re_read` already ran for its caller-given path: paths.Norm
// against the CURRENT project root and symlink state. Applying it here too closes the gap that
// mattered — `expand` (by tool_use_id) and `recall` (over every hit) previously resolved a stored
// path with no re-check at all, so a path that was inside the project when it was captured, but
// has since become — or been replaced by — a symlink escaping the project, could be walked back
// into through the archive even though a live read of the same path today would be refused.
// Archived reads may not bypass a host-denied path.

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
// An empty path — a capture with no associated file, such as a Bash result or an elimination's
// evidence bytes — carries no path policy to apply and is authorized by identity alone: expand and
// re_read never grant a path-bearing capability beyond what paths.Norm would grant a live read of
// the same path today.
func (h *handlers) authorizePath(path string) (ok bool, reason string) {
	if path == "" {
		return true, ""
	}
	if _, err := paths.Norm(h.root, path); err != nil {
		return false, authorizedDenialReason
	}
	return true, ""
}
