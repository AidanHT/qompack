package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// The three `source` values re_read reports: where the bytes actually came from.
const (
	sourceWorktree = "worktree"
	sourceStore    = "store"
)

// worktreeReadFactor bounds the worktree file re_read is willing to slurp before falling back to
// the store, as a multiple of the response budget. Four times is generous enough that a large
// source file is still read whole and narrowed to its matching function, and small enough that a
// stray multi-gigabyte artifact in the tree cannot be pulled into memory by one tool call.
const worktreeReadFactor = 4

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
		return errResponse("expand failed: " + err.Error()), nil
	}
	if res != nil {
		return h.jsonResponse(ToolExpand, res, nil), nil
	}

	span, err := h.resolveContent(ctx, root, path, inline, h.spanOptsFor(a.Full, a.Span, path, "", 0))
	if err != nil {
		return errResponse("expand failed: " + err.Error()), nil
	}

	count, promoted := h.noteExpansion(ctx, r.Session, root.Hash)
	body := contentBody{
		Found: true, Hash: root.Hash.String(), Path: path, Tool: tool,
		Span: [2]int64{span.Off, span.End}, TotalBytes: span.Total,
		Truncated: span.Truncated, NextSpan: span.NextSpan, Widened: span.Widened,
		Expansions: count, Promoted: promoted, Content: string(span.Body),
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
		rt, gerr := h.store.GetRoot(ctx, rec.Root)
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
	norm, err := paths.Norm(h.root, base)
	if err != nil {
		return errResponse("path escapes the project root"), nil
	}
	key := paths.Key(norm)

	root, source, turn, ok, err := h.resolveVersion(ctx, a.At, norm, key)
	if err != nil {
		return errResponse(err.Error()), nil
	}
	if !ok {
		return h.jsonResponse(ToolReRead, miss("worktree, file version history"), nil), nil
	}

	span, err := h.resolveContent(ctx, root, key, nil, h.spanOptsFor(a.Full, "", key, sym, line))
	if err != nil {
		return errResponse("re_read failed: " + err.Error()), nil
	}

	count, promoted := h.noteExpansion(ctx, r.Session, root.Hash)
	body := contentBody{
		Found: true, Hash: root.Hash.String(), Path: norm, At: a.At, Source: source, Turn: turn,
		Span: [2]int64{span.Off, span.End}, TotalBytes: span.Total,
		Truncated: span.Truncated, NextSpan: span.NextSpan, Widened: span.Widened,
		Expansions: count, Promoted: promoted, Content: string(span.Body),
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

// resolveVersion turns re_read's `at` into a root: the working tree, a timestamp, a root hash, or
// a turn index.
func (h *handlers) resolveVersion(ctx context.Context, at, norm, key string) (
	root store.Root, source string, turn *int, ok bool, err error,
) {
	switch {
	case at == "":
		return h.currentVersion(ctx, norm, key)

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

// currentVersion reads the working-tree file when there is one, and falls back to the newest
// recorded version when there is not.
//
// The worktree comes FIRST on purpose: `re_read` with no `at` means "what does this look like
// now", and the store's newest version is only as fresh as the last tool call that touched the
// file. A file the user edited by hand since then is exactly the case where a stale answer is
// worst.
func (h *handlers) currentVersion(ctx context.Context, norm, key string) (
	store.Root, string, *int, bool, error,
) {
	full := filepath.Join(h.root, filepath.FromSlash(norm))
	if st, serr := os.Stat(paths.Long(full)); serr == nil && st.Mode().IsRegular() &&
		st.Size() <= int64(h.cfg.Runtime.MCP.MaxResponseBytes)*worktreeReadFactor {
		if b, rerr := os.ReadFile(paths.Long(full)); rerr == nil {
			pr, perr := h.store.PutBytes(ctx, b, store.PutOptions{
				Tool: mcpToolPrefix + ToolReRead, Path: key, Ephemeral: true,
			})
			if perr == nil {
				return pr.Root, sourceWorktree, nil, true, nil
			}
			h.log.Warn("mcp: could not store the worktree version re_read read",
				"path", key, "err", perr.Error())
		}
	}

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
