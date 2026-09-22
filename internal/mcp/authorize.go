package mcp

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hostperm"
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
// This layer enforces recorded provenance and current filesystem scope, and then — for every
// record that has a path — the part of the host's current Read permission policy a plugin can
// reconstruct (V6-HOST-1, see authorizeHost). Containment alone never establishes host permission.

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

// Host-policy refusal reasons (V6-HOST-1). Each is its own sentence so a model, an operator reading
// a log and a test can tell the three outcomes apart, and none of them echoes the path or the rule:
// a rule spells the very path it protects.
const (
	// hostDeniedReason: a permissions.deny Read rule in the host's settings matches the path today.
	hostDeniedReason = "authorization denied: the host's current permission rules deny reading " +
		"the associated path"
	// hostAskReason: an ask rule matches. The host would prompt; an archived retrieval cannot, so
	// it refuses rather than answering on the user's behalf.
	hostAskReason = "authorization denied: the host's current permission rules require approval " +
		"to read the associated path, and an archived retrieval cannot ask for it"
	// hostUnavailableReason: a settings source exists but cannot be read or parsed, so the rules
	// cannot be established and path-bearing content is withheld (fail closed).
	hostUnavailableReason = "host policy unavailable: the host's permission settings could not be " +
		"read or parsed, so content with a file path is withheld"
)

// hostSnapshotKey carries one request's lazily loaded host rule set through its context.
type hostSnapshotKey struct{}

// hostSnapshot is loaded at most once per tool call: every path a call checks is judged against
// the same rules, and a call that checks none pays nothing.
type hostSnapshot struct {
	once  sync.Once
	rules *hostperm.RuleSet
	err   error
}

// withHostSnapshot attaches an unloaded snapshot to ctx. invoke calls it for every tool call.
func (h *handlers) withHostSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, hostSnapshotKey{}, &hostSnapshot{})
}

// hostRules returns this call's rule set, loading it on first use.
func (h *handlers) hostRules(ctx context.Context) (*hostperm.RuleSet, error) {
	if h.host == nil {
		return nil, hostperm.ErrUnavailable
	}
	snap, ok := ctx.Value(hostSnapshotKey{}).(*hostSnapshot)
	if !ok {
		return h.host.Snapshot()
	}
	snap.once.Do(func() {
		snap.rules, snap.err = h.host.Snapshot()
		if snap.err != nil {
			h.m.Counter("mcp.host_policy_unavailable").Add(1)
			h.log.Loud("mcp: host permission policy unavailable; content with a path is withheld",
				"err", snap.err.Error())
		}
	})
	return snap.rules, snap.err
}

// authorizeHost applies the host's current Read deny and ask rules to one path that has already
// passed containment. It returns nil when no rule this package can see refuses the read; otherwise
// the refusal body to render.
//
// path is the spelling the record (or the caller) gave — project-relative or absolute — never the
// paths.Norm result. Norm adopts an in-project symlink's target, and the host applies a deny rule
// when EITHER a link's own path or its target matches: judging the normalized path alone lost the
// link's spelling on every platform whose EvalSymlinks follows links (reproduced on Linux,
// plans/sdd/V6-closeout/hostperm/runs/04-link-spelling-linux-red.log). hostperm resolves the
// spelling itself, so both are checked.
//
// It is deliberately NOT a claim that the host would allow the read. Session-only rules, CLI
// flags, hooks and an embedding host's policy are invisible to a plugin (internal/hostperm's
// package comment lists them); what this enforces is every rule saved in a settings file the host
// reads, fail-closed when one of those files cannot be read.
func (h *handlers) authorizeHost(ctx context.Context, path string) any {
	rules, err := h.hostRules(ctx)
	if err != nil {
		return unavailable(hostUnavailableReason)
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(h.root, filepath.FromSlash(path))
	}
	d := rules.Evaluate(abs)
	switch d.Effect {
	case hostperm.Deny:
		h.m.Counter("mcp.host_policy_denied").Add(1)
		return denied(hostDeniedReason)
	case hostperm.Ask:
		h.m.Counter("mcp.host_policy_ask").Add(1)
		return denied(hostAskReason)
	}
	return nil
}

// authorizePath reports whether path may be resolved before its content is materialized: nil
// when it may, otherwise the refusal body to render.
//
// Empty paths have no filesystem scope and are refused here. authorizeOrigin
// separately recognizes known pathless producers; a missing Read path is not one.
func (h *handlers) authorizePath(ctx context.Context, path string) any {
	if path == "" {
		return denied(authorizedDenialReason)
	}
	norm, err := paths.Norm(h.root, path)
	if err != nil {
		return denied(authorizedDenialReason)
	}
	if !paths.ResolvesInside(h.root, norm) {
		return denied(authorizedDenialReason)
	}
	return h.authorizeHost(ctx, path)
}

// authorizeOrigin preserves legitimate pathless records without upgrading a lost
// file path (including a legacy record) into a permission grant. Unknown producers
// remain denied rather than being guessed to have no file dependency. A pathless record has no
// path for a host rule to match, so it keeps exactly this V6-AUTH behaviour.
func (h *handlers) authorizeOrigin(ctx context.Context, tool, path string) any {
	if path != "" {
		return h.authorizePath(ctx, path)
	}
	switch tool {
	case "Bash", "PowerShell", "UserPromptSubmit", "SubagentStop", mcpToolPrefix + ToolRecordEliminated:
		return nil
	default:
		return denied("authorization denied: the capture has no usable path provenance")
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
		if refusal := h.authorizeOrigin(ctx, origin.Tool, origin.Path); refusal != nil {
			return refusal
		}
	}
	return nil
}

// withheldReason names why a refusal withheld content, for the tools that report a refusal
// alongside other data rather than as their whole answer (`why`, `recall`, `dropped`). It is ""
// for an unavailable answer that is not a policy outcome, such as incomplete provenance.
func withheldReason(refusal any) string {
	switch r := refusal.(type) {
	case deniedBody:
		return r.Reason
	case missBody:
		if r.Reason == hostUnavailableReason {
			return r.Reason
		}
	}
	return ""
}

// isHostUnavailable reports whether a refusal is the fail-closed host-policy answer.
func isHostUnavailable(refusal any) bool {
	r, ok := refusal.(missBody)
	return ok && r.Reason == hostUnavailableReason
}
