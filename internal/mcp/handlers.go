package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The six tools that do not resolve a span: the index (recall, timeline), negative knowledge
// (already_tried, record_eliminated) and the two that read what a checkpoint or a rehydration
// left behind (why, dropped).
//
// Every one of them obeys handlers_common.go's rule about misses, and every one of them tolerates
// a nil collaborator by saying so. A build without SP-10 has no checkpoint reader and a build
// without SP-11 has no drop reporter; `available:false` is the honest answer, and it is a
// different answer from `found:false`.

// maxWhyCheckpoints bounds how far back `why` walks the checkpoint chain. A decision the model is
// asking about is by construction one it saw recently — in a rehydrated digest or in the latest
// checkpoint — so an unbounded walk would spend budget B-F re-reading a year of artifacts to
// confirm an id that was mistyped.
const maxWhyCheckpoints = 32

// The three `recall` query selectors. They are prefixes rather than separate arguments because a
// model composes one search string far more reliably than it fills four fields, and because
// `path:src/*.ts symbol:refreshToken retry` reads the way a developer would type it.
const (
	selectorPath   = "path:"
	selectorSymbol = "symbol:"
	selectorTool   = "tool:"
)

// Legacy ledger answers plus the two outcomes that are neither an answer nor an absence
// (architecture §0.2 / ADR 0013, §11.3 Required invariants item 8).
//
// stateUnavailable and stateUncertain are the tool-boundary spelling of negknow's AnswerUnavailable
// and AnswerUncertain. They exist because the three legacy states cannot express "the ledger could
// not back an answer": rendering either of them as stateAbsent would assert that an approach was
// never tried on the strength of a blind ledger, an unrecognized record status, a suppressed stale
// match or an unverified coverage watermark — the false negative invariant 8 forbids outright.
const (
	stateAbsent      = "absent"
	stateActive      = "active"
	stateStale       = "stale"
	stateUnavailable = "unavailable"
	stateUncertain   = "uncertain"
)

// The reason/recovery pair renderAnswer supplies when IT applies eliminations.staleResponse
// "drop", rather than the ledger. The wording deliberately matches negknow's own pair for the same
// case: negknow owns the canonical text and mcp may not import its unexported constants, so the two
// belt-and-braces layers are kept in agreement by restating it here. A test drives both layers.
const (
	staleDroppedReason   = `a matching elimination is stale and eliminations.staleResponse is "drop", so its current applicability is not disclosed`
	staleDroppedRecovery = `set eliminations.staleResponse to "flag" to see the staleness detail, or re-verify the approach directly`
)

// coverageFallbackReason and coverageFallbackRecovery fill in for an Answer that names an
// uncertain state but carries no Coverage. A conforming Ledger always sets one; a hand-built
// Answer, an in-test fake or a future ledger need not, and an `uncertain` result that explains
// nothing is barely better than the absence it replaced.
const (
	coverageFallbackReason   = "the elimination ledger could not establish coverage for this question"
	coverageFallbackRecovery = "re-verify the approach directly; this is not evidence the approach is untried"
)

// bloomOnlyNote is what a filter hit with no backing record is reported as. It is stated as a
// possible false positive and explicitly as "treat as not previously tried", because §13
// invariant 3 makes the bloom a cache and never the source of truth: an unbacked hit must never
// read as a block.
const bloomOnlyNote = "a filter hit was recorded but no backing record exists " +
	"(possible false positive); treat as not previously tried"

// ── recall ──────────────────────────────────────────────────────────────────────────────────

// recallQuery echoes back how the query string was parsed, so a model whose selector did not
// parse the way it expected can see that immediately rather than inferring it from odd results.
type recallQuery struct {
	Text   string `json:"text"`
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Tool   string `json:"tool"`
}

// recallBody is `recall`'s response: pointers and summaries, never content.
type recallBody struct {
	Hits  []RecallHit `json:"hits"`
	Count int         `json:"count"`
	Found bool        `json:"found"`
	Query recallQuery `json:"query"`
	// Denied counts hits the search actually matched but whose stored path failed authorization
	// before the preview was built (T13-TRUST). It is reported explicitly, never left for the
	// caller to infer from a shorter-than-expected hit list, and it never names which paths.
	Denied int `json:"denied,omitempty"`
	// SummariesWithheld says every summary was suppressed because this build wired no retrieval
	// -side Redactor, and Reason says why in one sentence.
	//
	// A recall hit is a POINTER — hash, path, tool, score — and those are still true and still
	// useful. The summary is the one piece of ARCHIVE TEXT in the response, so it is the one piece
	// that must not be served unchecked. Both keys carry omitempty, so a build with a redactor
	// emits the shape §5.16 declares, byte for byte.
	SummariesWithheld bool   `json:"summaries_withheld,omitempty"`
	Reason            string `json:"reason,omitempty"`
	// HostPolicy is set when at least one of the Denied hits was withheld because the host's
	// permission settings could not be read or parsed (V6-HOST-1's fail-closed answer), so a
	// caller can tell "the host denies these" from "the host's rules could not be established".
	HostPolicy string `json:"host_policy,omitempty"`
}

// recall searches the store by content, path or symbol and returns hashes and summaries. It does
// NOT count towards promotion: it returns pointers rather than bytes, so it is evidence of
// searching, not of demand (§8.7).
func (h *handlers) recall(ctx context.Context, _ Request, raw json.RawMessage) (Response, error) {
	var a RecallArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errResponse("invalid arguments for " + ToolRecall + ": " + err.Error()), nil
	}
	if h.store == nil {
		return h.jsonResponse(ToolRecall, unavailable("store not present in this build"), nil), nil
	}

	q := parseRecallQuery(a.Query)
	hits, err := h.store.Search(ctx, store.Query{
		Text: q.Text, Path: q.Path, Symbol: q.Symbol, Tool: q.Tool, K: a.K,
	})
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		h.log.Warn("mcp: recall search failed", "err", err.Error())
		return errResponse("recall failed: " + err.Error()), nil
	}

	// FAIL CLOSED on the summaries (T20-M2-04). With no Redactor this build cannot tell a summary
	// drawn from a clean record from one drawn from a record captured before today's rules existed,
	// so it returns NO summary text at all and says so. The pointers survive because they are not
	// archive text and were never redaction's subject; `expand` and `re_read`, whose entire output
	// IS archive text, report themselves unavailable instead.
	withheld := h.redactor == nil
	if withheld {
		h.log.Loud("mcp: recall withheld every summary: no retrieval-side redactor is wired",
			"tool", ToolRecall, "hits", len(hits))
	}

	out := make([]RecallHit, 0, len(hits))
	var deniedCount int
	var hostUnavailable bool
	for _, hit := range hits {
		// Authorization runs BEFORE the preview is built, over every hit the search actually
		// matched — a hash or a stored path is never itself proof that this hit may be shown
		// (T13-TRUST). A hit that fails is omitted rather than rendered with its summary redacted:
		// the summary itself is the thing being protected. That includes a hit whose path the
		// host's current Read rules deny or ask about (V6-HOST-1): its summary is a preview of the
		// file's content.
		if refusal := h.authorizeOrigin(ctx, hit.Tool, hit.Path); refusal != nil {
			deniedCount++
			hostUnavailable = hostUnavailable || isHostUnavailable(refusal)
			continue
		}
		// A summary is archive text like any other, so it goes through today's policy before it is
		// rendered. ok is false only in the withheld case above, and the summary is then dropped.
		summary, _ := h.redactForRetrieval(ToolRecall, []byte(hit.Summary))
		out = append(out, RecallHit{
			Hash: hit.Root.String(), Path: hit.Path, Tool: hit.Tool,
			Summary: string(summary),
			TS:      rfc3339(hit.TS), Score: hit.Score,
			ToolUseID: string(hit.ToolUseID), Span: hit.Span,
		})
	}
	body := recallBody{Hits: out, Count: len(out), Found: len(out) > 0, Query: q, Denied: deniedCount}
	if hostUnavailable {
		body.HostPolicy = hostUnavailableReason
	}
	if withheld {
		body.SummariesWithheld, body.Reason = true, redactorMissingReason
	}
	return h.jsonResponse(ToolRecall, body, map[string]any{metaUntrusted: true}), nil
}

// parseRecallQuery splits space-separated selector prefixes out of the query string; every word
// that carries no prefix is free text.
func parseRecallQuery(q string) recallQuery {
	var out recallQuery
	var text []string
	for _, word := range strings.Fields(q) {
		switch {
		case strings.HasPrefix(word, selectorPath):
			out.Path = strings.TrimPrefix(word, selectorPath)
		case strings.HasPrefix(word, selectorSymbol):
			out.Symbol = strings.TrimPrefix(word, selectorSymbol)
		case strings.HasPrefix(word, selectorTool):
			out.Tool = strings.TrimPrefix(word, selectorTool)
		default:
			text = append(text, word)
		}
	}
	out.Text = strings.Join(text, " ")
	return out
}

// ── already_tried ───────────────────────────────────────────────────────────────────────────

// alreadyTried answers the three-way question that closes G6.2 — the direct cause of the
// most-reported failure mode, an agent re-attempting something already eliminated.
//
// Two rows of the mapping are deliberate belt-and-braces, and it matters that a reader knows it
// rather than discovering it. SP-09's Ledger.Query ALREADY reports AnswerAbsent with BloomOnly
// when the filter hits with no backing record, and ALREADY applies eliminations.staleResponse
// "drop" internally. Restating both here is what makes this tool's contract hold against any
// conforming Ledger — including the in-test fakes — and what stops a future ledger change from
// silently turning a stale record into an `active` answer. The two layers must agree; a test
// asserts each.
func (h *handlers) alreadyTried(ctx context.Context, _ Request, raw json.RawMessage) (Response, error) {
	var a AlreadyTriedArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errResponse("invalid arguments for " + ToolAlreadyTried + ": " + err.Error()), nil
	}
	l := h.ledger()
	if l == nil {
		// No ledger could be opened. This is the documented `unavailable` state, never a body of
		// another shape: a caller holding the tool's five states must be able to read it, and it
		// must not read as absence (§11.3 invariant 8).
		h.log.Loud("mcp: elimination ledger unavailable; already_tried cannot determine prior attempts")
		return h.jsonResponse(ToolAlreadyTried, ledgerUnavailableResult(), nil), nil
	}

	scope := negknow.Scope(h.cfg.Eliminations.DefaultScope)
	ans, err := l.Query(ctx, a.Target, a.Approach, scope)
	if err != nil {
		// A failed query is not evidence of absence. Do not expose a backend error that may
		// contain private paths, query text or stored evidence in the response or diagnostic.
		h.log.Loud("mcp: elimination ledger unavailable; already_tried cannot determine prior attempts")
		return h.jsonResponse(ToolAlreadyTried, ledgerUnavailableResult(), nil), nil
	}
	return h.jsonResponse(ToolAlreadyTried, h.renderAnswer(ans), answerMeta(ans)), nil
}

// ledgerUnavailableReason is the one sentence both ledger tools use when no ledger can back them.
const ledgerUnavailableReason = "elimination ledger unavailable"

// ledgerUnavailableResult is already_tried's answer when the ledger cannot be consulted at all —
// none could be opened, or the query failed.
func ledgerUnavailableResult() AlreadyTriedResult {
	return AlreadyTriedResult{
		State: stateUnavailable, Degraded: true, Reason: ledgerUnavailableReason,
		Note: "Prior attempts are unknown. Retry after ledger recovery; this is not evidence against the approach.",
	}
}

// renderAnswer maps a negknow.Answer onto the wire result.
//
// THE TWO UNCERTAIN STATES ARE RESOLVED FIRST, and the order is load-bearing rather than
// stylistic. Both of the branches below them collapse to stateAbsent — BloomOnly does, and so does
// the `Record == nil` catch-all — so an AnswerUncertain minted from an unrecognized record status
// (which carries BloomOnly) or from a suppressed stale match (which carries no Record) would be
// rendered as a confident absence by whichever branch reached it first. That is §11.3 invariant 8's
// prohibited case: no amount of degradation may turn uncertainty into an assertion of absence.
func (h *handlers) renderAnswer(ans negknow.Answer) AlreadyTriedResult {
	switch ans.State {
	case negknow.AnswerUnavailable:
		// Nothing about the record is disclosed even if a hand-built Answer attached one: a ledger
		// that could not be consulted has nothing to disclose, and Degraded says exactly that.
		out := coverageResult(stateUnavailable, ans.Coverage, nil)
		out.Degraded = true
		return out
	case negknow.AnswerUncertain:
		return coverageResult(stateUncertain, ans.Coverage, ans.Record)
	}
	if ans.BloomOnly {
		return AlreadyTriedResult{State: stateAbsent, Note: bloomOnlyNote}
	}
	if ans.State == negknow.AnswerStale && h.cfg.Eliminations.StaleResponse == "drop" {
		// "drop" suppresses the staleness DETAIL, not the fact that an elimination is on record.
		// The record's reason, evidence and stale_because stay hidden, but the state may not claim
		// absence: this handler has just been told an elimination exists.
		return AlreadyTriedResult{
			State: stateUncertain, Reason: staleDroppedReason, Note: staleDroppedRecovery,
		}
	}
	if ans.Record == nil || ans.State == negknow.AnswerAbsent {
		return AlreadyTriedResult{State: stateAbsent, Note: ans.Note}
	}

	rec := ans.Record
	out := AlreadyTriedResult{
		Reason:     rec.Reason,
		Note:       ans.Note,
		Scope:      string(rec.Scope),
		RecordedAt: rfc3339(rec.TS),
		DependsOn:  rec.DependsOn,
	}
	if !rec.Evidence.IsZero() {
		out.Evidence = rec.Evidence.String()
	}
	switch ans.State {
	case negknow.AnswerStale:
		out.State = stateStale
		out.StaleBecause = rec.StaleBecause
	case negknow.AnswerActive:
		out.State = stateActive
	case negknow.AnswerAbsent:
		out.State = stateAbsent
	}
	return out
}

// coverageResult renders one uncertain outcome: the omission's reason as the result's reason and
// its recovery direction as the note, which is the mapping negknow.Answer.MCPResult already uses.
//
// rec is attached only where it is genuinely known — the unverified-dependency-coverage case, where
// the record itself is on file and only its freshness is unconfirmed. Its metadata (scope, when it
// was recorded, what it depends on, what proves it) is what a caller acts on to resolve the
// uncertainty; the coverage reason still occupies Reason, because the state being reported is the
// coverage failure and not the elimination.
func coverageResult(state string, cov core.Omission, rec *negknow.Record) AlreadyTriedResult {
	out := AlreadyTriedResult{State: state, Reason: cov.Reason, Note: cov.Recovery}
	if out.Reason == "" {
		out.Reason = coverageFallbackReason
	}
	if out.Note == "" {
		out.Note = coverageFallbackRecovery
	}
	if rec != nil {
		out.Scope = string(rec.Scope)
		out.RecordedAt = rfc3339(rec.TS)
		out.DependsOn = rec.DependsOn
		if !rec.Evidence.IsZero() {
			out.Evidence = rec.Evidence.String()
		}
	}
	return out
}

// answerMeta surfaces the bloom-only flag in _meta so a caller can distinguish a genuine absence
// from an unbacked filter hit without string-matching the note.
func answerMeta(ans negknow.Answer) map[string]any {
	if !ans.BloomOnly {
		return nil
	}
	return map[string]any{"bloom_only": true}
}

// ── record_eliminated ───────────────────────────────────────────────────────────────────────

// eliminatedBody is `record_eliminated`'s acknowledgement: the id the ledger assigned, the
// canonical descriptor it was keyed under, and what actually became a staleness dependency.
type eliminatedBody struct {
	ID                  string             `json:"id"`
	Descriptor          negknow.Descriptor `json:"descriptor"`
	Scope               string             `json:"scope"`
	Evidence            string             `json:"evidence"`
	DependsOn           []core.Dep         `json:"depends_on"`
	DependsOnUnresolved []string           `json:"depends_on_unresolved"`
	Status              string             `json:"status"`
	Warnings            []string           `json:"warnings,omitempty"`
}

// recordEliminated writes negative knowledge (§8.3 source #1). Its result is the acknowledgement
// of a DURABLE fact, not retrieved content, which is why it is the one tool of the eight that is
// not born ephemeral.
func (h *handlers) recordEliminated(ctx context.Context, r Request, raw json.RawMessage) (Response, error) {
	var a RecordEliminatedArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errResponse("invalid arguments for " + ToolRecordEliminated + ": " + err.Error()), nil
	}
	if h.ledger() == nil {
		// Nothing was recorded, and the acknowledgement is the only signal the model has that a
		// durable write happened, so this is the tool error every other failed write here is —
		// not a success-shaped body a caller could mistake for a recorded elimination.
		h.log.Loud("mcp: elimination ledger unavailable; record_eliminated recorded nothing")
		return errResponse("cannot record elimination: " + ledgerUnavailableReason +
			", so nothing was recorded; retry later"), nil
	}
	if msg := validateElimination(a); msg != "" {
		return errResponse(msg), nil
	}

	// The scope the caller ACTUALLY set, as opposed to the one the schema defaulted. They are
	// different questions: an omitted scope must take eliminations.defaultScope, which a project
	// may have set to "project", and the schema's own literal default cannot know that.
	explicit := ""
	if argPresent(r.Args, "scope") {
		explicit = a.Scope
	}
	effective := explicit
	if effective == "" {
		effective = h.cfg.Eliminations.DefaultScope
	}

	// A session-scoped record in a session that does not exist is invisible for ever: SP-09's
	// visibility filter can never return it. Refusing clearly beats writing a record nothing will
	// ever read.
	if r.Session == "" && effective == string(negknow.ScopeSession) {
		return errResponse("cannot record a session-scoped elimination: no live session — " +
			`pass scope="project", or retry once a session is active`), nil
	}

	rec, warnings, err := h.ingestElimination(ctx, a, explicit)
	if err != nil {
		h.log.Loud("mcp: could not record an elimination", "err", err.Error())
		return errResponse("cannot record elimination: " + err.Error()), nil
	}

	h.m.Counter("mcp.eliminations.recorded").Add(1)
	body := eliminatedBody{
		ID: rec.ID, Descriptor: rec.Desc, Scope: string(rec.Scope),
		DependsOn: depsOf(rec.DependsOn), DependsOnUnresolved: unresolvedDeps(a.DependsOn, rec.DependsOn),
		Status: string(rec.Status), Warnings: warnings,
	}
	if !rec.Evidence.IsZero() {
		body.Evidence = rec.Evidence.String()
	}
	return h.jsonResponse(ToolRecordEliminated, body, nil), nil
}

// ingestElimination routes through SP-09's own MCP ingest seam when the ledger offers one.
//
// negknow.Maintainer.IngestMCP exists precisely for this tool — its doc comment says so — and it
// already does scope resolution, dependency resolution with warnings, evidence minting under
// eliminations.requireEvidence, and the id-collision retry that a hand-rolled Record call would
// silently skip. Reimplementing it here would be a second, diverging copy of SP-09's rules inside
// the package the out-of-scope table calls "a thin front over negknow.Ledger".
//
// The fallback is for a Ledger that is only a Ledger — the in-test fakes, and any future
// implementation that does not opt into Maintainer. It records the same shape by hand.
func (h *handlers) ingestElimination(ctx context.Context, a RecordEliminatedArgs, scope string) (
	negknow.Record, []string, error,
) {
	if m, ok := h.ledger().(negknow.Maintainer); ok {
		return m.IngestMCP(ctx, negknow.MCPArgs{
			Target: a.Target, Approach: a.Approach, Reason: a.Reason,
			Scope: scope, DependsOn: a.DependsOn,
		})
	}
	return h.ingestEliminationFallback(ctx, a, scope)
}

// ingestEliminationFallback records an elimination against a bare Ledger.
func (h *handlers) ingestEliminationFallback(ctx context.Context, a RecordEliminatedArgs, scope string) (
	negknow.Record, []string, error,
) {
	if scope == "" {
		scope = h.cfg.Eliminations.DefaultScope
	}

	var evidence core.Hash
	if h.store != nil {
		pr, err := h.store.PutBytes(ctx, []byte(a.Reason), store.PutOptions{
			Tool: mcpToolPrefix + ToolRecordEliminated,
		})
		switch {
		case err == nil:
			evidence = pr.Root.Hash
		case h.cfg.Eliminations.RequireEvidence:
			return negknow.Record{}, nil, errors.New("evidence could not be stored")
		default:
			h.log.Warn("mcp: recording an elimination without evidence", "err", err.Error())
		}
	} else if h.cfg.Eliminations.RequireEvidence {
		return negknow.Record{}, nil, errors.New("evidence could not be stored")
	}

	deps, warnings := h.resolveFallbackDeps(ctx, a.DependsOn)
	// The session is the caller's (invoke attaches it), set explicitly rather than left for the
	// ledger to default: a Ledger that is only a Ledger need not read the caller off the context.
	caller, _ := negknow.CallerFrom(ctx)
	rec := negknow.Record{
		Session: caller.Session, TS: h.nowMilli(),
		Target: a.Target, Approach: a.Approach, Reason: a.Reason,
		Desc:     negknow.Canonicalize(a.Target, a.Approach, a.Reason),
		Evidence: evidence, DependsOn: deps,
		Scope: negknow.Scope(scope), Status: negknow.StatusActive, Source: negknow.SourceMCP,
	}
	id, err := h.ledger().Record(ctx, rec)
	if err != nil {
		return negknow.Record{}, warnings, err
	}
	rec.ID = id
	return rec, warnings, nil
}

// resolveFallbackDeps resolves each declared path to its newest stored root, reporting the ones
// that have none. A dependency with no stored version cannot flip anything to stale, so recording
// it would be a staleness guard that never fires — worse than none, because it looks like one.
func (h *handlers) resolveFallbackDeps(ctx context.Context, want []string) ([]core.Dep, []string) {
	var (
		deps     []core.Dep
		warnings []string
	)
	for _, p := range want {
		key := paths.Key(p)
		if h.store != nil {
			if hist, err := h.store.FileHistory(ctx, key); err == nil && len(hist) > 0 {
				deps = append(deps, core.Dep{Path: key, Hash: hist[len(hist)-1].Root})
				continue
			}
		}
		warnings = append(warnings,
			"no stored version for "+p+"; not used as a staleness dependency")
	}
	return deps, warnings
}

// validateElimination applies the §12.3 bounds before anything reaches the ledger, naming the
// offending field so the model can fix one thing rather than guess.
func validateElimination(a RecordEliminatedArgs) string {
	switch {
	case strings.TrimSpace(a.Target) == "":
		return "record_eliminated requires a non-empty target"
	case len(a.Target) > maxElimTextBytes:
		return "record_eliminated: target is too long (max " + strconv.Itoa(maxElimTextBytes) + " bytes)"
	case strings.TrimSpace(a.Approach) == "":
		return "record_eliminated requires a non-empty approach"
	case len(a.Approach) > maxElimTextBytes:
		return "record_eliminated: approach is too long (max " + strconv.Itoa(maxElimTextBytes) + " bytes)"
	case strings.TrimSpace(a.Reason) == "":
		return "record_eliminated requires a non-empty reason"
	case len(a.Reason) > maxElimReasonBytes:
		return "record_eliminated: reason is too long (max " + strconv.Itoa(maxElimReasonBytes) + " bytes)"
	}
	return ""
}

// The bounds normalizeRecord already enforces inside negknow, restated here so the tool REFUSES
// an over-long field rather than silently truncating one: a reason cut in half is negative
// knowledge that no longer says what it meant.
const (
	maxElimTextBytes   = 512
	maxElimReasonBytes = 4 * maxElimTextBytes
)

// argPresent reports whether the raw argument object carries key at all — as distinct from
// carrying it with a zero value, which is what a schema default produces.
func argPresent(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// depsOf normalizes a nil dependency slice to an empty one, so the model reads "none" rather than
// "unknown".
func depsOf(in []core.Dep) []core.Dep {
	if in == nil {
		return []core.Dep{}
	}
	return in
}

// unresolvedDeps names the declared paths that did not become dependencies. It is derived by
// COMPARISON rather than by parsing the ledger's warning text, so a reworded warning cannot
// silently empty this list.
func unresolvedDeps(want []string, got []core.Dep) []string {
	resolved := make(map[string]bool, len(got))
	for _, d := range got {
		resolved[d.Path] = true
	}
	out := []string{}
	for _, p := range want {
		if !resolved[paths.Key(p)] {
			out = append(out, p)
		}
	}
	return out
}

// ── timeline ────────────────────────────────────────────────────────────────────────────────

// timelineSegment is one segment as `timeline` renders it. Every field is copied straight off
// store.Segment and nothing is derived.
//
// There is deliberately no tool-use count. The store exposes no API that lists tool uses by turn
// range — ToolUsesByPath needs a path, and Search with an empty query returns K hits rather than
// "all" — and adding one would be a Rule W-3 amendment against SP-06. Tokens and EncodedOnce
// carry the same "how much happened here" signal without inventing a query.
type timelineSegment struct {
	ID            core.SegmentID     `json:"id"`
	StartTurn     core.TurnIndex     `json:"start_turn"`
	EndTurn       core.TurnIndex     `json:"end_turn"`
	StartTS       string             `json:"start_ts"`
	EndTS         string             `json:"end_ts"`
	Tokens        core.Tokens        `json:"tokens"`
	Closed        bool               `json:"closed"`
	EncodedOnce   bool               `json:"encoded_once"`
	CheckpointSeq core.CheckpointSeq `json:"checkpoint_seq"`
	Features      map[string]float64 `json:"features"`
}

// timelineBody is `timeline`'s response. It echoes the resolved bounds AND the encoding frontier,
// so the model can see how much of the range a checkpoint already covers.
type timelineBody struct {
	From     core.TurnIndex    `json:"from"`
	To       core.TurnIndex    `json:"to"`
	Frontier core.TurnIndex    `json:"frontier"`
	Segments []timelineSegment `json:"segments"`
	Count    int               `json:"count"`
	Found    bool              `json:"found"`
}

// timeline reports what happened between two points.
func (h *handlers) timeline(ctx context.Context, r Request, raw json.RawMessage) (Response, error) {
	var a TimelineArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errResponse("invalid arguments for " + ToolTimeline + ": " + err.Error()), nil
	}
	if h.store == nil {
		return h.jsonResponse(ToolTimeline, unavailable("store not present in this build"), nil), nil
	}
	segs := h.store.Segments()
	if segs == nil {
		return h.jsonResponse(ToolTimeline, unavailable("segment log not present in this build"), nil), nil
	}

	all, err := segs.Range(ctx, 0, math.MaxInt32)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return errResponse("timeline failed: " + err.Error()), nil
	}
	liveProgress(ctx, all)

	from, ok := resolveBound(a.From, all, true)
	if !ok {
		return errResponse("timeline: from must be a turn index, an RFC3339 timestamp, or empty"), nil
	}
	to, ok := resolveBound(a.To, all, false)
	if !ok {
		return errResponse("timeline: to must be a turn index, an RFC3339 timestamp, or empty"), nil
	}
	if strings.TrimSpace(a.From) != "" && strings.TrimSpace(a.To) != "" && from > to {
		// Both bounds were the caller's own and they are inverted. Answering would be a lie either
		// way: the open-segment rule below matches a segment on its start alone, so an inverted
		// range still returned the live segment, dressed as the answer to a question no range asks.
		return errResponse("timeline: from is after to; pass from at or before to"), nil
	}

	window, err := segs.Range(ctx, from, to)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return errResponse("timeline failed: " + err.Error()), nil
	}
	liveProgress(ctx, window)

	var frontier core.TurnIndex
	if f, ferr := segs.Frontier(ctx, r.Session); ferr == nil {
		frontier = f
	}

	out := make([]timelineSegment, 0, len(window))
	for _, s := range window {
		out = append(out, timelineSegment{
			ID: s.ID, StartTurn: s.StartTurn, EndTurn: s.EndTurn,
			StartTS: rfc3339(s.StartTS), EndTS: rfc3339(s.EndTS),
			Tokens: s.Tokens, Closed: s.Closed, EncodedOnce: s.EncodedOnce,
			CheckpointSeq: s.CheckpointSeq, Features: s.Features,
		})
	}
	body := timelineBody{
		From: from, To: to, Frontier: frontier,
		Segments: out, Count: len(out), Found: len(out) > 0,
	}
	return h.jsonResponse(ToolTimeline, body, nil), nil
}

// liveProgress brings each OPEN segment in segs up to where its session actually is, when the
// daemon attached a live view that holds that session.
//
// The segment log records a segment's end turn and token count only when the segment closes; until
// then it holds StartTurn as the end and 0 tokens. A session whose single segment stays open for its
// whole life — the common case, since only a changepoint or the session's end closes one — was
// therefore reported as "turns 0-0, 0 tokens" after any number of turns (retrieval D8). The end is
// raised to the session's current turn. The running token count is taken only for the segment the
// observer is itself enrolling events into: a segment the scheduler rolled open since has no
// running count the observer holds, and keeps the log's value rather than borrowing another
// segment's.
func liveProgress(ctx context.Context, segs []store.Segment) {
	for i := range segs {
		s := &segs[i]
		if s.Closed {
			continue
		}
		p, ok := progressOf(ctx, s.Session)
		if !ok {
			continue
		}
		if p.Turn > s.EndTurn {
			s.EndTurn = p.Turn
		}
		if p.Segment == s.ID && p.SegmentTokens > s.Tokens {
			s.Tokens = p.SegmentTokens
		}
	}
}

// resolveBound turns one timeline bound into a turn index.
//
// An empty upper bound resolves to the greatest EndTurn in the log, NOT to SegmentLog.Frontier.
// Frontier is the end of the last CONTIGUOUSLY ENCODED segment, which is 0 in any session that
// has not checkpointed yet — using it would make the default range empty in exactly the session
// where the timeline is most useful.
//
// An empty LOG resolves to 0 rather than to the math.MaxInt32 the sweep above is bounded by. The
// bound is echoed back in the response, and a `to` of 2147483647 tells the model an implementation
// detail about the sweep rather than a fact about the session; 0 says what is true, which is that
// there is nothing here yet.
func resolveBound(s string, all []store.Segment, lower bool) (core.TurnIndex, bool) {
	if strings.TrimSpace(s) == "" {
		if lower {
			return 0, true
		}
		var end core.TurnIndex
		for _, seg := range all {
			if seg.EndTurn > end {
				end = seg.EndTurn
			}
		}
		return end, true
	}
	if n, err := strconv.Atoi(s); err == nil {
		return core.TurnIndex(n), true
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, false
	}
	cutoff := core.UnixMilli(t.UnixMilli())
	if lower {
		for _, seg := range all {
			if seg.StartTS >= cutoff {
				return seg.StartTurn, true
			}
		}
		return math.MaxInt32, true
	}
	var out core.TurnIndex
	for _, seg := range all {
		if seg.EndTS <= cutoff {
			out = seg.EndTurn
		}
	}
	return out, true
}

// ── why ─────────────────────────────────────────────────────────────────────────────────────

// whyBody is `why`'s response: the decision, its evidence pointer, and how to read that evidence.
type whyBody struct {
	Found                bool               `json:"found"`
	DecisionID           string             `json:"decision_id"`
	What                 string             `json:"what"`
	Why                  string             `json:"why"`
	AlternativesRejected []string           `json:"alternatives_rejected"`
	Evidence             string             `json:"evidence,omitempty"`
	Turn                 core.TurnIndex     `json:"turn"`
	CheckpointSeq        core.CheckpointSeq `json:"checkpoint_seq"`
	EvidenceBytes        *int64             `json:"evidence_bytes,omitempty"`
	Hint                 string             `json:"hint,omitempty"`
	// EvidenceWithheld is the refusal reason when the evidence's recorded origins fail
	// authorization today — outside the project, lost path provenance, or a path the host's current
	// Read rules deny or ask about, or whose rules cannot be read. The decision itself is still
	// answered; the evidence's size preview and the expand hint are not, because expanding it
	// would be refused.
	EvidenceWithheld string `json:"evidence_withheld,omitempty"`
}

// whyMissBody names which checkpoints were actually read, so "not found" is a statement about a
// search rather than an assertion about the world.
type whyMissBody struct {
	Found               bool                 `json:"found"`
	DecisionID          string               `json:"decision_id"`
	SearchedCheckpoints []core.CheckpointSeq `json:"searched_checkpoints"`
}

// why retrieves a decision and its evidence from the checkpoint chain.
func (h *handlers) why(ctx context.Context, r Request, raw json.RawMessage) (Response, error) {
	if h.disableWhy {
		return h.jsonResponse(ToolWhy, unsupported(ToolWhy), nil), nil
	}
	var a WhyArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return errResponse("invalid arguments for " + ToolWhy + ": " + err.Error()), nil
	}
	if strings.TrimSpace(a.DecisionID) == "" {
		return errResponse("why requires a non-empty decision_id"), nil
	}
	if h.ckpt == nil {
		return h.jsonResponse(ToolWhy, unavailable("checkpoint reader not present in this build"), nil), nil
	}

	searched := []core.CheckpointSeq{}

	cp, _, err := h.ckpt.Latest(ctx, r.Session)
	if core.IsNotImplemented(err) {
		return h.jsonResponse(ToolWhy, unavailable("checkpoint reader not present in this build"), nil), nil
	}
	if err == nil {
		searched = append(searched, cp.Seq)
		if d, ok := findDecision(cp, a.DecisionID); ok {
			return h.jsonResponse(ToolWhy, h.whyFound(ctx, d, cp.Seq), nil), nil
		}
	}

	refs, err := h.ckpt.List(ctx)
	if core.IsNotImplemented(err) {
		return h.jsonResponse(ToolWhy, unavailable("checkpoint reader not present in this build"), nil), nil
	}
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return errResponse("why failed: " + err.Error()), nil
	}

	for i, walked := len(refs)-1, 0; i >= 0 && walked < maxWhyCheckpoints; i, walked = i-1, walked+1 {
		seq := refs[i].Seq
		if containsSeq(searched, seq) {
			walked--
			continue
		}
		other, _, gerr := h.ckpt.Get(ctx, seq)
		if gerr != nil {
			continue
		}
		searched = append(searched, seq)
		if d, ok := findDecision(other, a.DecisionID); ok {
			return h.jsonResponse(ToolWhy, h.whyFound(ctx, d, seq), nil), nil
		}
	}
	return h.jsonResponse(ToolWhy, whyMissBody{
		DecisionID: a.DecisionID, SearchedCheckpoints: searched,
	}, nil), nil
}

// whyFound renders a located decision, sizing its evidence when the store has it so the model can
// decide whether expanding it is worth the tokens before it spends them.
func (h *handlers) whyFound(ctx context.Context, d checkpoint.Decision, seq core.CheckpointSeq) whyBody {
	body := whyBody{
		Found: true, DecisionID: string(d.ID), What: d.What, Why: d.Why,
		AlternativesRejected: stringsOf(d.AlternativesRejected),
		Turn:                 d.Turn, CheckpointSeq: seq,
	}
	if d.Evidence.IsZero() {
		return body
	}
	body.Evidence = d.Evidence.String()
	body.Hint = "call expand with hash=" + d.Evidence.String() + " to read the evidence"
	if h.store == nil {
		return body
	}
	refusal := h.authorizeHash(ctx, d.Evidence)
	if reason := withheldReason(refusal); reason != "" {
		body.EvidenceWithheld, body.Hint = reason, ""
		return body
	}
	if refusal == nil {
		if root, err := h.store.GetRoot(ctx, d.Evidence); err == nil {
			n := root.CanonBytes
			body.EvidenceBytes = &n
		}
	}
	return body
}

// findDecision looks one decision id up in a checkpoint's tier-2 decision list.
func findDecision(cp checkpoint.Checkpoint, id string) (checkpoint.Decision, bool) {
	for _, d := range cp.Decisions {
		if string(d.ID) == id {
			return d, true
		}
	}
	return checkpoint.Decision{}, false
}

// containsSeq reports whether seq has already been searched.
func containsSeq(all []core.CheckpointSeq, seq core.CheckpointSeq) bool {
	for _, s := range all {
		if s == seq {
			return true
		}
	}
	return false
}

// stringsOf normalizes a nil slice to an empty one.
func stringsOf(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// ── dropped ─────────────────────────────────────────────────────────────────────────────────

// droppedBody is `dropped`'s response: the explicit drop report that closes G4.5.
//
// It carries its own Available/Reason pair rather than reusing missBody, because "this build has
// no rehydrator" and "nothing was dropped" must both still answer with an empty drops array. A
// model that got `found:false` and no array would have to guess which of the two it was looking
// at, and the wrong guess is the one where it stops asking.
type droppedBody struct {
	Drops     []checkpoint.DropEntry `json:"drops"`
	Count     int                    `json:"count"`
	Available *bool                  `json:"available,omitempty"`
	Reason    string                 `json:"reason,omitempty"`
	// Denied counts drop entries withheld because they point at archived content whose path the
	// host's current Read rules deny or ask about (V6-HOST-1), and HostPolicy says when some were
	// withheld because those rules could not be read. Neither names which entries.
	Denied     int    `json:"denied,omitempty"`
	HostPolicy string `json:"host_policy,omitempty"`
}

// droppedUnavailable is the empty-but-explained drop report.
func droppedUnavailable(reason string) droppedBody {
	no := false
	return droppedBody{Drops: []checkpoint.DropEntry{}, Available: &no, Reason: reason}
}

// dropped reports what is currently out of context, so the model can ask for something back
// instead of assuming it never existed.
func (h *handlers) dropped(ctx context.Context, r Request, _ json.RawMessage) (Response, error) {
	if h.disableDropped {
		return h.jsonResponse(ToolDropped, droppedUnavailable(unsupportedReason(ToolDropped)), nil), nil
	}
	if h.drops == nil {
		return h.jsonResponse(ToolDropped, droppedUnavailable("rehydrator not present in this build"), nil), nil
	}
	if r.Session == "" {
		return h.jsonResponse(ToolDropped, droppedUnavailable("no live session"), nil), nil
	}

	entries, err := h.drops.CurrentDrops(ctx, r.Session)
	if core.IsNotImplemented(err) {
		return h.jsonResponse(ToolDropped, droppedUnavailable("rehydrator not present in this build"), nil), nil
	}
	if err != nil {
		h.log.Loud("mcp: the drop reporter failed", "session", string(r.Session), "err", err.Error())
		return errResponse("dropped failed: " + err.Error()), nil
	}
	entries = dropEntriesOf(entries)
	kept, deniedCount, hostUnavailable := h.filterDrops(ctx, entries)
	body := droppedBody{Drops: kept, Count: len(kept), Denied: deniedCount}
	if hostUnavailable {
		body.HostPolicy = hostUnavailableReason
	}
	return h.jsonResponse(ToolDropped, body, nil), nil
}

// filterDrops withholds the drop entries that point at archived content whose path the host's
// current Read rules refuse (V6-HOST-1).
//
// A drop entry is a pointer, not content, but it is a pointer INTO the archive — a file path the
// model is told `re_read` still resolves, or a tool_use_id `expand` still resolves — and recall
// withholds its pointers on the same grounds. An entry points at archived content when its ID is a
// stored tool_use_id with a path, or a path Qompack has captured versions of. Every other entry
// (a decision id, an open question, a budget note, a file never captured) carries no archived
// content and is kept exactly as before, and so is every entry on a build with no store.
func (h *handlers) filterDrops(ctx context.Context, entries []checkpoint.DropEntry,
) (kept []checkpoint.DropEntry, deniedCount int, hostUnavailable bool) {
	kept = make([]checkpoint.DropEntry, 0, len(entries))
	for _, e := range entries {
		p := h.dropEntryPath(ctx, e)
		if p == "" {
			kept = append(kept, e)
			continue
		}
		norm, err := paths.Norm(h.root, p)
		if err != nil {
			// Out of scope for a path rule to name; containment is not this tool's check.
			kept = append(kept, e)
			continue
		}
		if refusal := h.authorizeHost(ctx, p, norm); refusal != nil {
			deniedCount++
			hostUnavailable = hostUnavailable || isHostUnavailable(refusal)
			continue
		}
		kept = append(kept, e)
	}
	return kept, deniedCount, hostUnavailable
}

// dropEntryPath returns the archived path a drop entry points at, or "" when it points at none.
func (h *handlers) dropEntryPath(ctx context.Context, e checkpoint.DropEntry) string {
	if h.store == nil || e.ID == "" {
		return ""
	}
	if rec, err := h.store.ToolUse(ctx, core.ToolUseID(e.ID)); err == nil {
		return rec.Path
	}
	norm, err := paths.Norm(h.root, e.ID)
	if err != nil {
		return ""
	}
	if hist, err := h.store.FileHistory(ctx, paths.Key(norm)); err == nil && len(hist) > 0 {
		return e.ID
	}
	return ""
}
