package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// expand and re_read: the two tools that re-materialize content, and therefore the two that
// resolve a minimum sufficient span and the only two that count towards promotion.
//
// §8.7's third mechanism lives here: "repeated expansion of the same hash within a session is a
// signal, not a cost". Every successful materialization is reported to the Promoter, which fires
// at retrieval.promoteAfterExpansions so SP-16 can promote demand-proven content into the next
// checkpoint's pointer tier — a demand-driven correction to the 8–12K rehydration budget. recall
// does NOT count: it returns pointers, not bytes, so it is evidence of searching rather than of
// demand.

// The two Meta keys expand and re_read publish beyond the shared set, so a caller can page and
// see where the bytes came from without re-parsing the body.
const (
	metaSpan       = "span"
	metaTotalBytes = "total_bytes"
	metaTruncated  = "truncated"
	metaNextSpan   = "next_span"
	metaExpansions = "expansions"
	metaPromoted   = "promoted"
	metaSource     = "source"
)

// sourceStore is the one `source` value re_read ever reports: retrieval answers exclusively from
// Qompack's own captured history (T13-HISTORY). There is deliberately no "worktree" counterpart —
// see currentVersion's doc comment. A live read of the working tree is a different, separately
// authorized operation that this tool does not perform as a fallback.
const sourceStore = "store"

// symbolSuffixRe matches the ":<symbol>" form of re_read's path suffix. A digits-only suffix is
// matched first, as a line number, because "auth.ts:120" is unambiguous and "120" is also a legal
// identifier-free symbol name in no language anyone uses.
var symbolSuffixRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.$-]*$`)

// contentBody is the response body expand and re_read share. The fields neither one populates are
// omitted rather than emitted empty, so an `expand` result does not carry a meaningless "at".
type contentBody struct {
	Found      bool     `json:"found"`
	Hash       string   `json:"hash"`
	Path       string   `json:"path,omitempty"`
	Tool       string   `json:"tool,omitempty"`
	At         string   `json:"at,omitempty"`
	Source     string   `json:"source,omitempty"`
	Turn       *int     `json:"turn,omitempty"`
	Span       [2]int64 `json:"span"`
	TotalBytes int64    `json:"total_bytes"`
	Truncated  bool     `json:"truncated"`
	NextSpan   string   `json:"next_span,omitempty"`
	Widened    bool     `json:"widened"`
	Expansions int      `json:"expansions"`
	Promoted   bool     `json:"promoted"`
	Content    string   `json:"content"`
}

// missBody is the uniform semantic-miss shape: found:false plus what was looked at. It is NOT an
// error — see handlers_common.go's rule.
type missBody struct {
	Found     bool   `json:"found"`
	Searched  string `json:"searched,omitempty"`
	Available *bool  `json:"available,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// miss reports that the thing was looked for and is not there, naming where the search went.
func miss(searched string) missBody { return missBody{Searched: searched} }

// unavailable reports that this BUILD cannot answer the question at all — a nil store, a nil
// checkpoint reader — which is a different fact from "it is not there" and must not be reported
// as one.
func unavailable(reason string) missBody {
	no := false
	return missBody{Available: &no, Reason: reason}
}

// spanFailure renders a content-resolution failure, keeping §12.3's three answers apart.
//
// A store that FOUND the object and refused it is a degradation the retrieval layer continues
// through, so it answers `unavailable` — an ordinary domain outcome, not a protocol error. Anything
// else really is a failure of the call and stays an error response, which is what a model needs in
// order to stop retrying.
func (h *handlers) spanFailure(tool, verb string, err error) Response {
	if errors.Is(err, store.ErrDamaged) {
		return h.jsonResponse(tool, refusedObject(err), nil)
	}
	return errResponse(verb + " failed: " + err.Error())
}

// damaged is what every retrieval tool reports for an object the store found and REFUSED: a bad
// content address, a torn zstd frame, a physical size past the read bound, a length that disagrees
// with the index. §12.3's answer to that is "quarantine the object, Loud, continue; qompack fsck
// repairs", and the retrieval layer's share of it is to say so in the one word that already means
// it — `unavailable`, an explicit third answer beside found and not-found.
//
// It is finding S-3 that this exists at all. A quarantined object reached the model as a
// protocol-level tool ERROR through one address form and as `miss` — that is, as ABSENT — through
// the other. Both are wrong, and the second is worse: it tells a model the content was never
// captured, when what happened is that the bytes were refused and preserved as evidence.
//
// The reason carries no path and no secret, and no hash: the caller supplied the address, so
// repeating it adds nothing, and the store has already Louded the full identity where an operator
// can act on it. It also does not claim an integrity failure: store.ErrDamaged covers a bad
// envelope, an indexed object whose file is gone AND a plain read refusal (a sharing violation, a
// permission error), and only the first of those was checked and quarantined. The wording is
// therefore the outcome — refused, not served — and where the evidence, if any, lives.
func damaged() missBody {
	return unavailable("the stored object was refused rather than served: its bytes could not be " +
		"read intact; a damaged object is preserved as evidence and `qompack fsck` reports it")
}

// missingObject is what every retrieval tool reports for an object the index records and the store
// no longer holds anywhere — not in objects/, not in tmp/quarantine/ (store.ErrObjectMissing). It is
// still `unavailable`, never a miss: the content was captured. But nothing was damaged and nothing
// was preserved, so damaged()'s wording would send an operator looking for evidence that does not
// exist (F-C49-3); fsck names the same object "which the object store does not hold".
func missingObject() missBody {
	return unavailable("the stored object is missing: the index records it, but its bytes are no " +
		"longer in the store, so it cannot be served; `qompack fsck` reports it")
}

// refusedObject words a store.ErrDamaged refusal by what actually happened: missing, or refused.
func refusedObject(err error) missBody {
	if errors.Is(err, store.ErrObjectMissing) {
		return missingObject()
	}
	return damaged()
}

// noCapturedHistory is what `re_read` with an empty `at` reports when nothing has ever been
// captured for the path. It is spelled as an explicit unavailable outcome — Available:false plus a
// reason — rather than a plain miss, because "Qompack has no historical record" is a stronger,
// more actionable fact than "not found" and must never be confused with, or silently answered by,
// a live read of the working tree (T13-HISTORY).
func noCapturedHistory() missBody {
	return unavailable("no historical version has been captured for this path yet")
}

// spanOptsFor builds the SpanOpts both content tools use. Every bound is read from configuration
// at the call site, never written as a literal (D11, §11.6).
func (h *handlers) spanOptsFor(full bool, explicit, path, anchorSym string, anchorLine int) SpanOpts {
	return SpanOpts{
		Full:        full || h.cfg.Retrieval.DefaultSpan == "full",
		Explicit:    explicit,
		Path:        path,
		AnchorSym:   anchorSym,
		AnchorLine:  anchorLine,
		MaxSpan:     h.cfg.Store.Chunk.Max,
		MaxResponse: h.cfg.Runtime.MCP.MaxResponseBytes,
		WidenLines:  h.cfg.Runtime.MCP.SpanWidenLines,
	}
}

// expand re-materializes a cleared tool result, addressed either by content hash or by the host's
// own tool_use_id.
func (h *handlers) expand(ctx context.Context, r Request, raw json.RawMessage) (Response, error) {
	var a ExpandArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errResponse("invalid arguments for " + ToolExpand + ": " + err.Error()), nil
	}
	// Exactly one, not "at least one": two addresses that disagree have no defensible resolution,
	// and picking one silently would hand back content the caller did not ask for.
	if (a.Hash == "") == (a.ToolUseID == "") {
		return errResponse("expand requires exactly one of hash or tool_use_id"), nil
	}
	if h.store == nil {
		return h.jsonResponse(ToolExpand, unavailable("store not present in this build"), nil), nil
	}

	root, path, tool, inline, res, err := h.resolveExpandTarget(ctx, a)
	if err != nil {
		return h.spanFailure(ToolExpand, "expand", err), nil
	}
	if res != nil {
		return h.jsonResponse(ToolExpand, res, nil), nil
	}

	// This is where the tool_use_id form actually meets a damaged object. GetRoot answers from the
	// in-memory index and succeeds; the bytes are not touched until here, so this — not the lookup
	// above — is the branch finding S-3 was measured on.
	span, err := h.resolveContent(ctx, root, path, inline, h.spanOptsFor(a.Full, a.Span, path, "", 0))
	if err != nil {
		return h.spanFailure(ToolExpand, "expand", err), nil
	}

	content, ok := h.redactForRetrieval(ToolExpand, span.Body)
	if !ok {
		return h.jsonResponse(ToolExpand, unavailable(redactorMissingReason), nil), nil
	}

	count, promoted := h.noteExpansion(ctx, r.Session, root.Hash)
	body := contentBody{
		Found: true, Hash: root.Hash.String(), Path: path, Tool: tool,
		Span: [2]int64{span.Off, span.End}, TotalBytes: span.Total,
		Truncated: span.Truncated, NextSpan: span.NextSpan, Widened: span.Widened,
		Expansions: count, Promoted: promoted,
		Content: string(content),
	}
	return h.jsonResponse(ToolExpand, body, spanMeta(span, path, "", count, promoted)), nil
}

// resolveExpandTarget turns expand's two address forms into a root.
//
// inline is non-nil only for the chunk-hash form, where the bytes have already been fetched and
// the synthesized Root is NOT in the store's root index — see ResolveSpanFromBytes. res is a
// finished miss body the caller should render as-is.
func (h *handlers) resolveExpandTarget(ctx context.Context, a ExpandArgs) (
	root store.Root, path, tool string, inline []byte, res any, err error,
) {
	if a.ToolUseID != "" {
		rec, terr := h.store.ToolUse(ctx, core.ToolUseID(a.ToolUseID))
		if errors.Is(terr, core.ErrNotFound) {
			return store.Root{}, "", "", nil, miss("tool_use index"), nil
		}
		if terr != nil {
			return store.Root{}, "", "", nil, nil, terr
		}
		// Authorization runs BEFORE the root is even looked up: a tool_use_id resolved a stored
		// path, and that path is re-checked against the CURRENT path/symlink policy — a hash or id
		// is an address, not a credential (T13-TRUST / T20-M2-04).
		if refusal := h.authorizeOrigin(ctx, rec.Tool, rec.Path); refusal != nil {
			return store.Root{}, "", "", nil, refusal, nil
		}
		rt, gerr := h.store.GetRoot(ctx, rec.Root)
		if errors.Is(gerr, store.ErrDamaged) {
			return store.Root{}, "", "", nil, refusedObject(gerr), nil
		}
		if errors.Is(gerr, core.ErrNotFound) {
			return store.Root{}, "", "", nil, miss("tool_use index, object store"), nil
		}
		if gerr != nil {
			return store.Root{}, "", "", nil, nil, gerr
		}
		return rt, rec.Path, rec.Tool, nil, nil, nil
	}

	hash, perr := core.ParseHash(a.Hash)
	if perr != nil {
		return store.Root{}, "", "", nil, nil, errors.New(`hash must be "sha256:" followed by 64 hex characters`)
	}
	if refusal := h.authorizeHash(ctx, hash); refusal != nil {
		return store.Root{}, "", "", nil, refusal, nil
	}
	rt, gerr := h.store.GetRoot(ctx, hash)
	if gerr == nil {
		return rt, "", "", nil, nil, nil
	}
	if !errors.Is(gerr, core.ErrNotFound) {
		return store.Root{}, "", "", nil, nil, gerr
	}
	// A model that pasted a CHUNK hash rather than a root hash asked a reasonable question with
	// the wrong noun. The chunk is addressable, so answer it rather than reporting a miss.
	b, cerr := h.store.GetChunk(ctx, hash)
	if errors.Is(cerr, store.ErrDamaged) {
		// Finding S-3's second address form. This branch used to map EVERY failure here to miss(),
		// which renders as ABSENT — the one answer §12.3 forbids for a quarantined object, because
		// it tells a model the content was never there when in fact it was refused and preserved.
		return store.Root{}, "", "", nil, refusedObject(cerr), nil
	}
	if cerr != nil {
		return store.Root{}, "", "", nil, miss("object store (root and chunk index)"), nil
	}
	return store.Root{
		Hash:       hash,
		Chunks:     []core.ChunkRef{{Hash: hash, Len: len(b)}},
		CanonBytes: int64(len(b)),
		RawBytes:   int64(len(b)),
	}, "", "", b, nil, nil
}

// reRead returns a file's current or historical content, narrowed to the minimum sufficient span
// around whatever the path's suffix anchored on.
func (h *handlers) reRead(ctx context.Context, r Request, raw json.RawMessage) (Response, error) {
	var a ReReadArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errResponse("invalid arguments for " + ToolReRead + ": " + err.Error()), nil
	}
	if h.store == nil {
		return h.jsonResponse(ToolReRead, unavailable("store not present in this build"), nil), nil
	}

	base, sym, line := splitPathAnchor(a.Path)
	// This IS re_read's authorization gate (T13-TRUST / T20-M2-04): it runs before any store access,
	// on every `at` form, and a path that fails it never reaches resolveVersion at all.
	norm, err := paths.Norm(h.root, base)
	if err != nil {
		return errResponse("path escapes the project root"), nil
	}
	// The second half of the gate, and the second call site finding S-1 names. A LEXICAL escape is
	// refused above as an error, deliberately: a miss would read as "that file does not exist" and
	// invite a cleverer spelling. A path that normalises cleanly but RESOLVES outside the root today
	// — its parent replaced by a link or a junction pointing out of the project — is a policy
	// refusal, so it gets the explicit denied envelope: found:false, denied:true, no preview bytes,
	// and a reason that does not echo the path back.
	if refusal := h.authorizePath(ctx, base); refusal != nil {
		return h.jsonResponse(ToolReRead, refusal, nil), nil
	}
	// The hash form names content by address, and an address is not a credential: the path above
	// was authorized, but the root it names may have been captured from a different path entirely.
	// Its complete origin set is checked exactly as `expand` by hash checks it, or re_read would
	// serve any archived object under the name of any path that passes (V6-AUTH-2's hash rule,
	// which the path check alone did not cover here; found while enumerating C1.9's forms).
	if strings.HasPrefix(a.At, "sha256:") {
		if hash, perr := core.ParseHash(a.At); perr == nil {
			if refusal := h.authorizeHash(ctx, hash); refusal != nil {
				return h.jsonResponse(ToolReRead, refusal, nil), nil
			}
		}
	}
	key := paths.Key(norm)

	root, source, turn, ok, err := h.resolveVersion(ctx, a.At, key)
	if err != nil {
		return errResponse(err.Error()), nil
	}
	if !ok {
		if a.At == "" {
			// Empty `at` means "the most recent version Qompack has actually captured", and there is
			// none — not "the file does not exist" (a live disk read could answer that, and does not
			// belong inside a HISTORICAL tool) and not a silent miss that leaves the caller to guess
			// whether anything was even captured (T13-HISTORY).
			return h.jsonResponse(ToolReRead, noCapturedHistory(), nil), nil
		}
		return h.jsonResponse(ToolReRead, miss("file version history"), nil), nil
	}

	span, err := h.resolveContent(ctx, root, key, nil, h.spanOptsFor(a.Full, "", key, sym, line))
	if err != nil {
		return h.spanFailure(ToolReRead, "re_read", err), nil
	}

	content, ok := h.redactForRetrieval(ToolReRead, span.Body)
	if !ok {
		return h.jsonResponse(ToolReRead, unavailable(redactorMissingReason), nil), nil
	}

	count, promoted := h.noteExpansion(ctx, r.Session, root.Hash)
	body := contentBody{
		Found: true, Hash: root.Hash.String(), Path: norm, At: a.At, Source: source, Turn: turn,
		Span: [2]int64{span.Off, span.End}, TotalBytes: span.Total,
		Truncated: span.Truncated, NextSpan: span.NextSpan, Widened: span.Widened,
		Expansions: count, Promoted: promoted,
		Content: string(content),
	}
	return h.jsonResponse(ToolReRead, body, spanMeta(span, key, source, count, promoted)), nil
}

// splitPathAnchor separates re_read's optional ":<symbol>" or ":<line>" suffix from the path.
//
// The LAST colon is the separator, and a digits-only tail wins over a symbol-shaped one, so
// "src/auth.ts:120" is line 120 and "src/auth.ts:refreshToken" is a symbol. A Windows drive
// letter is not a hazard here: the argument is project-relative by contract, and a path that is
// not gets rejected by paths.Norm a moment later either way.
func splitPathAnchor(p string) (base, sym string, line int) {
	i := strings.LastIndex(p, ":")
	if i <= 0 || i == len(p)-1 {
		return p, "", 0
	}
	head, tail := p[:i], p[i+1:]
	if n, err := strconv.Atoi(tail); err == nil && n > 0 {
		return head, "", n
	}
	if symbolSuffixRe.MatchString(tail) {
		return head, tail, 0
	}
	return p, "", 0
}

// resolveVersion turns re_read's `at` into a root: the newest captured version, a timestamp, a
// root hash, or a turn index. Every form resolves EXCLUSIVELY against Qompack's own captured
// history; none of them ever reads the working tree (T13-HISTORY).
func (h *handlers) resolveVersion(ctx context.Context, at, key string) (
	root store.Root, source string, turn *int, ok bool, err error,
) {
	switch {
	case at == "":
		return h.currentVersion(ctx, key)

	case strings.HasPrefix(at, "sha256:"):
		hash, perr := core.ParseHash(at)
		if perr != nil {
			return store.Root{}, "", nil, false, errors.New(atFormatMsg)
		}
		rt, gerr := h.store.GetRoot(ctx, hash)
		if gerr != nil {
			return store.Root{}, "", nil, false, nil
		}
		return rt, sourceStore, nil, true, nil

	case strings.HasPrefix(at, "turn:"):
		n, cerr := strconv.Atoi(strings.TrimPrefix(at, "turn:"))
		if cerr != nil {
			return store.Root{}, "", nil, false, errors.New(atFormatMsg)
		}
		return h.versionAtTurn(ctx, key, core.TurnIndex(n))

	default:
		t, terr := time.Parse(time.RFC3339, at)
		if terr != nil {
			return store.Root{}, "", nil, false, errors.New(atFormatMsg)
		}
		fv, ferr := h.store.FileAt(ctx, key, t)
		if ferr != nil {
			return store.Root{}, "", nil, false, nil
		}
		return h.rootOfVersion(ctx, fv, sourceStore)
	}
}

// atFormatMsg is re_read's one argument-format error, stated exactly once so the tool always says
// the same thing about the same mistake.
const atFormatMsg = "at must be empty, an RFC3339 timestamp, sha256:<hex>, or turn:<N>"

// currentVersion returns the newest CAPTURED historical version for key — never a live read of the
// working tree.
//
// `re_read` with no `at` means "the most recent version Qompack has actually observed", which is
// NOT the same claim as "what is on disk right now": a live read would bypass every capture-time
// policy (redaction, size bounds, host-denied paths) a real capture goes through, and silently
// substituting current disk contents for a missing historical original is precisely the defect
// T13-HISTORY forbids. A current-file read is the host's own, separately authorized operation
// (interface contract, "A current-file read remains the host's separately authorized operation");
// re_read does not perform one as a fallback, and no ninth tool is added here to provide one.
func (h *handlers) currentVersion(ctx context.Context, key string) (
	store.Root, string, *int, bool, error,
) {
	hist, herr := h.store.FileHistory(ctx, key)
	if herr != nil || len(hist) == 0 {
		return store.Root{}, "", nil, false, nil
	}
	return h.rootOfVersion(ctx, hist[len(hist)-1], sourceStore)
}

// versionAtTurn returns the newest recorded version at or before turn n.
func (h *handlers) versionAtTurn(ctx context.Context, key string, n core.TurnIndex) (
	store.Root, string, *int, bool, error,
) {
	hist, err := h.store.FileHistory(ctx, key)
	if err != nil || len(hist) == 0 {
		return store.Root{}, "", nil, false, nil
	}
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Turn <= n {
			return h.rootOfVersion(ctx, hist[i], sourceStore)
		}
	}
	return store.Root{}, "", nil, false, nil
}

// rootOfVersion resolves a FileVersion's root and carries its turn through to the response.
func (h *handlers) rootOfVersion(ctx context.Context, fv store.FileVersion, source string) (
	store.Root, string, *int, bool, error,
) {
	rt, err := h.store.GetRoot(ctx, fv.Root)
	if err != nil {
		return store.Root{}, "", nil, false, nil
	}
	turn := int(fv.Turn)
	return rt, source, &turn, true, nil
}

// resolveContent runs the span resolver and Loud-logs the one index inconsistency it can see: a
// root whose chunk lengths do not add up to its own recorded canonical size. It proceeds with the
// chunk sum, because that is what the reads are actually addressed against.
func (h *handlers) resolveContent(ctx context.Context, root store.Root, path string, inline []byte,
	o SpanOpts,
) (SpanResult, error) {
	if sum := chunkTotal(root); root.CanonBytes != 0 && sum != root.CanonBytes {
		h.log.Loud("root chunk lengths disagree with CanonBytes",
			"root", root.Hash.String(), "path", path,
			"chunk_sum", strconv.FormatInt(sum, 10),
			"canon_bytes", strconv.FormatInt(root.CanonBytes, 10))
	}
	if inline != nil {
		return ResolveSpanFromBytes(inline, root, h.wide, o)
	}
	return ResolveSpan(ctx, h.store, root, h.wide, o)
}

// noteExpansion reports one materialization to the Promoter. A failure is advisory: counting is a
// signal for the next checkpoint, never a precondition for answering this call.
func (h *handlers) noteExpansion(ctx context.Context, sess core.SessionID, root core.Hash) (int, bool) {
	if h.prom == nil {
		return 0, false
	}
	count, promoted, err := h.prom.NoteExpansion(ctx, sess, root)
	if err != nil {
		h.log.Warn("mcp: could not count an expansion", "err", err.Error())
	}
	return count, promoted
}

// spanMeta builds the _meta.qompack fields a content tool publishes.
func spanMeta(s SpanResult, path, source string, count int, promoted bool) map[string]any {
	m := map[string]any{
		metaSpan:       [2]int64{s.Off, s.End},
		metaTotalBytes: s.Total,
		metaTruncated:  s.Truncated,
		metaExpansions: count,
		metaPromoted:   promoted,
		// Every content tool renders retrieved archive bytes, never text it composed itself.
		metaUntrusted: true,
	}
	if s.NextSpan != "" {
		m[metaNextSpan] = s.NextSpan
	}
	if path != "" {
		m[metaPath] = path
	}
	if source != "" {
		m[metaSource] = source
	}
	return m
}
