package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// The six knowledge-and-index tools of §8.7: recall, already_tried, record_eliminated, timeline,
// why and dropped. The two span tools live in their own file, because what they share with these
// six is the run() preamble and nothing else.
//
// Two rules shape every test here, and both come out of handlers_common.go rather than out of any
// one handler.
//
// A SEMANTIC MISS IS NOT AN ERROR. An unknown decision id, an empty turn range, a query that
// matches nothing: all of them answer IsError:false with found:false, and asserting that
// distinction is most of what separates a tool the model can act on from one it can only retry.
// Every miss case below therefore asserts the absence of IsError as deliberately as it asserts the
// body.
//
// "NOT PRESENT IN THIS BUILD" IS A THIRD ANSWER. available:false is neither a miss nor an error —
// it is the honest report of a wave-3 sibling that has not merged — and a test that let it collapse
// into found:false would let a build with no checkpoint reader claim the decision does not exist.

// hashPattern is the wire spelling every content pointer this package emits must match. It is
// anchored at both ends: a summary that happened to CONTAIN a hash would satisfy an unanchored
// pattern, and a truncated digest would satisfy one anchored only at the start.
const hashPattern = `^sha256:[0-9a-f]{64}$`

// ── recall ──────────────────────────────────────────────────────────────────────────────────

// TestRecallReturnsHashesAndSummaries pins recall's whole contract in one call: it returns POINTERS
// and one-line summaries, never content (§8.7). Every hit must be expandable — which means a
// well-formed root hash — and must say enough about itself that the model can decide whether
// expanding it is worth the tokens.
func TestRecallReturnsHashesAndSummaries(t *testing.T) {
	f, _ := newSeededFixture(t)

	var body recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": "pool timeout"}, &body)

	require.NotEmpty(t, body.Hits, "the seeded corpus mentions \"pool timeout\" in several objects")
	require.True(t, body.Found, "found must agree with a non-empty hit list")
	require.Equal(t, len(body.Hits), body.Count, "count must be the length of the hit list")
	require.Equal(t, recallQuery{Text: "pool timeout"}, body.Query,
		"an unprefixed query is entirely free text")

	for i, hit := range body.Hits {
		require.Regexp(t, hashPattern, hit.Hash, "hits[%d].hash is not an expandable root hash", i)
		require.NotEmpty(t, hit.Summary, "hits[%d] carries no summary, so it says nothing about itself", i)
	}
}

// TestRecallDefaultKIsFive pins the `recall(query, k=5)` default of §8.7 as a SCHEMA default rather
// than a handler one: the caller sent no k at all, so the five can only have come from
// schemaRecall's default travelling through the run() preamble's ApplyDefaults.
func TestRecallDefaultKIsFive(t *testing.T) {
	f, c := newSeededFixture(t)
	require.GreaterOrEqual(t, len(c.Uses), 20,
		"the default-k assertion is vacuous unless far more than five objects match")

	var body recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": "a"}, &body)

	require.Equal(t, 5, body.Count, "k defaulted to something other than five")
	require.Len(t, body.Hits, 5, "count and the hit list must agree")
}

// TestRecallSelectorPrefixesParsed pins the three selector prefixes against the store.Query they
// actually produce, which is the only place the parse is observable: the response echoes the parsed
// query back, but an echo built from the same struct the search was NOT given would agree with
// itself while the search ran on something else.
func TestRecallSelectorPrefixesParsed(t *testing.T) {
	f := newFixture(t)
	spy := f.withSpyStore(t)

	var body recallBody
	f.callOK(t, ToolRecall, map[string]any{
		"query": "path:src/*.ts symbol:refreshToken tool:Read retry",
	}, &body)

	// K is the store's whole ranked answer, not the caller's k: recall takes k PERMITTED hits from
	// it, so asking for exactly k would let every withheld hit cost the caller a permitted one (D49).
	require.Equal(t, store.Query{
		Text: "retry", Path: "src/*.ts", Symbol: "refreshToken", Tool: "Read", K: store.MaxSearchK,
	}, spy.lastQuery(t), "the selector prefixes did not reach store.Search as separate fields")
	require.Equal(t, recallQuery{
		Text: "retry", Path: "src/*.ts", Symbol: "refreshToken", Tool: "Read",
	}, body.Query, "the echoed query must be the one the search was given")
}

// TestRecallEmptyResultIsNotAnError pins handlers_common.go's rule on the tool that meets it most
// often: a search that matched nothing is a fact about the index, and reporting it as isError would
// tell the model to retry the transport instead of to widen the query.
func TestRecallEmptyResultIsNotAnError(t *testing.T) {
	f, _ := newSeededFixture(t)

	var body recallBody
	resp := f.callOK(t, ToolRecall, map[string]any{"query": "zzzz"}, &body)

	require.False(t, resp.IsError, "an empty result set is a miss, never a tool error")
	require.Equal(t, 0, body.Count)
	require.False(t, body.Found)
	require.Empty(t, body.Hits, "hits must be an empty array, never null")
}

// TestRecallNilStoreReportsUnavailable pins the third answer. A build with no store cannot say
// whether anything matches, and available:false is what keeps that apart from "nothing matches".
func TestRecallNilStoreReportsUnavailable(t *testing.T) {
	f := newFixture(t, withoutStore())

	var body missBody
	resp := f.callOK(t, ToolRecall, map[string]any{"query": "pool timeout"}, &body)

	require.False(t, resp.IsError, "a missing collaborator is a degradation, not a tool error")
	require.NotNil(t, body.Available, "available must be present when the build cannot answer")
	require.False(t, *body.Available)
	require.Equal(t, "store not present in this build", body.Reason)
}

// TestRecallOmitsHitsWhoseStoredPathFailsAuthorization pins that authorization runs BEFORE a
// recall preview is built, over every hit — not only over an argument the caller supplied. A
// record whose stored path no longer resolves safely under the project root must never surface
// its summary or pointer in a search result, and the response must say plainly that something was
// held back rather than silently returning fewer hits than the index actually matched.
func TestRecallOmitsHitsWhoseStoredPathFailsAuthorization(t *testing.T) {
	f := newFixture(t)
	const marker = "denial-marker-alpha-9f2 pool timeout"
	f.record(t, "Bash", "../outside/leaked.txt", marker, 1)

	var body recallBody
	resp := f.callOK(t, ToolRecall, map[string]any{"query": "denial-marker-alpha-9f2"}, &body)

	require.False(t, resp.IsError)
	require.Empty(t, body.Hits, "a hit whose path fails authorization must never reach the preview")
	require.False(t, body.Found)
	require.Equal(t, 1, body.Denied, "an omission must be reported explicitly, not left silent")
	require.NotContains(t, responseText(resp), marker, "denied content must never reach the response")
}

// ── already_tried ───────────────────────────────────────────────────────────────────────────

// TestAlreadyTriedAbsent pins the answer an empty ledger gives, byte for byte. The exact rendering
// matters here and not only the state: every optional field is omitempty, so a body carrying so much
// as an empty reason would mean the tool had found a record and could not describe it.
func TestAlreadyTriedAbsent(t *testing.T) {
	f := newFixture(t)

	var res AlreadyTriedResult
	resp := f.callOK(t, ToolAlreadyTried, map[string]any{
		"target": elimTarget, "approach": elimApproach,
	}, &res)

	require.Equal(t, stateAbsent, res.State)
	require.Equal(t, `{"state":"absent"}`, responseText(resp),
		"an absent answer must carry nothing but the state")
}

// TestAlreadyTriedActiveReturnsReason pins the answer that closes G6.2: the agent is told not only
// THAT the approach was tried but why it failed and what proves it, which is what makes the warning
// actionable rather than merely discouraging.
func TestAlreadyTriedActiveReturnsReason(t *testing.T) {
	f := newFixture(t)
	_, evidence := f.seedElimination(t, negknow.ScopeSession)

	var res AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, map[string]any{
		"target": elimTarget, "approach": elimApproach,
	}, &res)

	require.Equal(t, stateActive, res.State)
	require.Equal(t, elimReason, res.Reason, "the reason must be the record's own, verbatim")
	require.Equal(t, evidence.String(), res.Evidence, "evidence must be the stored object's hash")
	require.Equal(t, string(negknow.ScopeSession), res.Scope)
	require.Empty(t, res.StaleBecause, "an active record has nothing to be stale because of")
	require.False(t, res.Degraded, "a ledger that answered is not degraded")
}

// TestAlreadyTriedStaleReturnsNote pins the third state, which is the whole reason already_tried is
// three-way: an elimination whose evidence has moved is neither a block nor an absence, and the note
// plus stale_because is what lets the agent decide whether to re-verify.
func TestAlreadyTriedStaleReturnsNote(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) {
		c.Eliminations.StaleResponse = "flag"
	}))
	id, _ := f.seedElimination(t, negknow.ScopeSession)

	because := []string{"docker-compose.yml"}
	require.NoError(t, f.Ledger.MarkStale(t.Context(), []string{id}, because), "MarkStale(%s)", id)

	var res AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, map[string]any{
		"target": elimTarget, "approach": elimApproach,
	}, &res)

	require.Equal(t, stateStale, res.State)
	require.Equal(t, negknow.StaleNote, res.Note, "the note must be Answer.Note verbatim")
	require.Equal(t, because, res.StaleBecause, "stale_because must name which dependency moved")
	require.Equal(t, elimReason, res.Reason, "a stale record still says why it was eliminated")
}

// TestAlreadyTriedStaleDropReturnsUncertain pins BOTH layers of the deliberate belt-and-braces
// handlers.go documents. The ledger applies eliminations.staleResponse itself, and renderAnswer
// applies it again; a test that only drove the real ledger would leave the second layer unexercised
// and a future ledger change could turn a stale record into an `active` answer unnoticed.
//
// "drop" suppresses the staleness DETAIL, not the fact that an elimination is on record, so neither
// layer may answer `absent`: both know an elimination exists. Each reports `uncertain` with a
// reason and a recovery direction, and discloses nothing else about the record.
func TestAlreadyTriedStaleDropReturnsUncertain(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) {
		c.Eliminations.StaleResponse = "drop"
	}))
	id, _ := f.seedElimination(t, negknow.ScopeSession)
	require.NoError(t, f.Ledger.MarkStale(t.Context(), []string{id}, []string{"docker-compose.yml"}),
		"MarkStale(%s)", id)

	args := map[string]any{"target": elimTarget, "approach": elimApproach}

	ans, err := f.Ledger.Query(t.Context(), elimTarget, elimApproach, negknow.ScopeSession)
	require.NoError(t, err, "Ledger.Query")
	require.Equal(t, negknow.AnswerUncertain, ans.State, "the ledger layer must not assert absence")

	var viaLedger AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, args, &viaLedger)
	require.Equal(t, stateUncertain, viaLedger.State, "the ledger layer must drop a stale record")
	require.Equal(t, ans.Coverage.Reason, viaLedger.Reason, "the ledger's own omission reason must carry through")
	require.Equal(t, ans.Coverage.Recovery, viaLedger.Note, "the recovery direction must carry through")

	// The second layer: a Ledger that reported AnswerStale anyway must still be dropped here.
	rec, err := f.Ledger.Get(t.Context(), id)
	require.NoError(t, err, "Ledger.Get(%s)", id)
	spy := f.withSpyLedger(t)
	spy.Answer = &negknow.Answer{State: negknow.AnswerStale, Record: &rec, Note: negknow.StaleNote}

	var viaHandler AlreadyTriedResult
	f.callOK(t, ToolAlreadyTried, args, &viaHandler)
	require.Equal(t, stateUncertain, viaHandler.State, "renderAnswer must drop a stale answer too")
	require.Equal(t, staleDroppedReason, viaHandler.Reason)
	require.Equal(t, staleDroppedRecovery, viaHandler.Note)
	require.NotEqual(t, elimReason, viaHandler.Reason, "a dropped answer never discloses the record's reason")
	require.Empty(t, viaHandler.Evidence, "a dropped answer discloses no evidence")
	require.Empty(t, viaHandler.StaleBecause, "a dropped answer discloses no staleness detail")
	require.Empty(t, viaHandler.RecordedAt, "a dropped answer discloses no timestamp")
	require.Equal(t, staleDroppedReason, viaLedger.Reason, "both layers must agree, wording included")
}

// TestAlreadyTriedBloomOnlyReportedAsAbsent pins §13 invariant 3 at the tool boundary: the filter is
// a cache and never the source of truth, so an unbacked hit must read as "not previously tried".
// The _meta flag is what lets a caller tell a genuine absence from a false positive without
// string-matching the note.
func TestAlreadyTriedBloomOnlyReportedAsAbsent(t *testing.T) {
	f := newFixture(t)
	spy := f.withSpyLedger(t)
	spy.Answer = &negknow.Answer{State: negknow.AnswerAbsent, BloomOnly: true}

	var res AlreadyTriedResult
	resp := f.callOK(t, ToolAlreadyTried, map[string]any{
		"target": elimTarget, "approach": elimApproach,
	}, &res)

	require.Equal(t, stateAbsent, res.State, "an unbacked filter hit must never read as a block")
	require.Equal(t, bloomOnlyNote, res.Note)
	require.Equal(t, true, resp.Meta["bloom_only"], "_meta must flag the unbacked hit")
}

// TestAlreadyTriedLedgerFailureReturnsUnavailable replaces the historical error-as-absence
// assertion under ADR 0013 / T13-STATE. A failed query proves neither prior absence nor an
// active prohibition, and remains a domain outcome rather than an MCP protocol error.
func TestAlreadyTriedLedgerFailureReturnsUnavailable(t *testing.T) {
	f := newFixture(t)
	log := f.withSpyLogger(t)
	spy := f.withSpyLedger(t)
	spy.Err = errors.New("the elimination log is unreadable")

	var res AlreadyTriedResult
	resp := f.callOK(t, ToolAlreadyTried, map[string]any{
		"target": elimTarget, "approach": elimApproach,
	}, &res)

	require.False(t, resp.IsError, "a ledger failure degrades the answer; it does not fail the call")
	require.Equal(t, "unavailable", res.State)
	require.True(t, res.Degraded, "the caller must be able to see that the ledger could not answer")
	require.NotEmpty(t, res.Note, "unavailability must include a recovery direction")
	require.Equal(t, 1, log.loudCount(), "a §12.3 degradation is a Loud event, exactly once")
	require.Equal(t, "mcp: elimination ledger unavailable; already_tried cannot determine prior attempts",
		log.lastLoud(t))
}

// TestAlreadyTriedScopeFromConfig pins where the query scope comes from. already_tried takes no
// scope argument, so eliminations.defaultScope is the ONLY thing that can decide whether a
// session-scoped record of another session is visible — and the scope is not echoed in an absent
// answer, which is why this is asserted at the ledger seam.
func TestAlreadyTriedScopeFromConfig(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) {
		c.Eliminations.DefaultScope = "project"
	}))
	spy := f.withSpyLedger(t)

	f.callOK(t, ToolAlreadyTried, map[string]any{
		"target": elimTarget, "approach": elimApproach,
	}, nil)

	require.Equal(t, []ledgerQuery{{
		Target: elimTarget, Approach: elimApproach, Scope: negknow.Scope("project"),
	}}, spy.queried(), "the configured default scope did not reach Ledger.Query")
}

// ── record_eliminated ───────────────────────────────────────────────────────────────────────

// TestRecordEliminatedWritesLedgerRecord pins §8.3's source #1 end to end: the tool call becomes a
// durable, active record attributed to MCP. The source matters because §8.3 rates its four sources
// differently, and a record the ledger believed came from the DAG heuristic would be ranked as the
// least reliable of the four rather than the most.
func TestRecordEliminatedWritesLedgerRecord(t *testing.T) {
	f := newFixture(t)

	var body eliminatedBody
	f.callOK(t, ToolRecordEliminated, map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": elimReason, "scope": "project",
	}, &body)

	recs, err := f.Ledger.All(t.Context())
	require.NoError(t, err, "Ledger.All")
	require.Len(t, recs, 1, "exactly one record must have been appended")
	require.Equal(t, negknow.SourceMCP, recs[0].Source, "the record must be attributed to MCP")
	require.Equal(t, negknow.StatusActive, recs[0].Status, "a new record is never born stale")
	require.Equal(t, body.ID, recs[0].ID, "the acknowledgement must name the record it wrote")
	require.Equal(t, string(negknow.StatusActive), body.Status)
	require.Equal(t, negknow.Canonicalize(elimTarget, elimApproach, elimReason), body.Descriptor,
		"the acknowledgement must echo the canonical descriptor the record was keyed under")
}

// TestRecordEliminatedStoresEvidence pins the half of §8.3 that makes negative knowledge checkable:
// the reason is not merely recorded as text on a line, it is stored as a retrievable object and the
// record points at it. A hash of something nobody kept would be indistinguishable from this until
// somebody tried to read it.
func TestRecordEliminatedStoresEvidence(t *testing.T) {
	f := newFixture(t)

	var body eliminatedBody
	f.callOK(t, ToolRecordEliminated, map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": elimReason,
	}, &body)

	require.Regexp(t, hashPattern, body.Evidence, "the evidence pointer must be a root hash")
	h, err := core.ParseHash(body.Evidence)
	require.NoError(t, err, "parsing the evidence hash %q", body.Evidence)

	rc, err := f.Store.Open(t.Context(), h)
	require.NoError(t, err, "the evidence hash must resolve in the store")
	defer func() { require.NoError(t, rc.Close(), "closing the evidence reader") }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err, "reading the evidence back")
	require.Equal(t, elimReason, string(got), "the evidence object must be the reason, verbatim")
}

// TestRecordEliminatedResolvesDependsOnHashes pins the §8.3 staleness guard's baseline. A dependency
// is only a guard if it is pinned to the version that was current when the reason was written, so
// the recorded hash must be the file's NEWEST stored root and not merely its path.
func TestRecordEliminatedResolvesDependsOnHashes(t *testing.T) {
	f, _ := newSeededFixture(t)

	deps := []string{"docker-compose.yml", "package-lock.json"}
	var body eliminatedBody
	f.callOK(t, ToolRecordEliminated, map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": elimReason,
		"depends_on": deps,
	}, &body)

	require.Empty(t, body.DependsOnUnresolved, "both paths are in the seeded history")
	require.Equal(t, newestDeps(t, f, deps), body.DependsOn,
		"each dependency must pin the file's newest stored root, ascending by path")
}

// newestDeps builds the core.Dep list paths ought to resolve to: each path's newest stored root, in
// the ascending-by-path order normalizeRecord writes a record's dependencies in.
func newestDeps(t *testing.T, f *fixture, paths []string) []core.Dep {
	t.Helper()
	out := make([]core.Dep, 0, len(paths))
	for _, p := range paths {
		hist, err := f.Store.FileHistory(t.Context(), p)
		require.NoError(t, err, "FileHistory(%s)", p)
		require.NotEmpty(t, hist, "%s has no stored version to pin", p)
		out = append(out, core.Dep{Path: p, Hash: hist[len(hist)-1].Root})
	}
	return out
}

// TestRecordEliminatedUnresolvedDependencyReported pins what happens to a dependency the store has
// never seen. It is SKIPPED rather than recorded with a zero hash — a guard that can never fire is
// worse than none, because it looks like one — and the tool says which path it dropped, derived by
// comparison rather than by parsing the ledger's warning text.
func TestRecordEliminatedUnresolvedDependencyReported(t *testing.T) {
	f, _ := newSeededFixture(t)

	var body eliminatedBody
	f.callOK(t, ToolRecordEliminated, map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": elimReason,
		"depends_on": []string{"ghost.yml"},
	}, &body)

	require.NotEmpty(t, body.ID, "an unresolvable dependency must not cost the record itself")
	require.Equal(t, []string{"ghost.yml"}, body.DependsOnUnresolved)
	require.Empty(t, body.DependsOn, "nothing may be pinned to a version that does not exist")
	require.Contains(t, body.Warnings,
		"no stored version for ghost.yml; not used as a staleness dependency")

	recs, err := f.Ledger.All(t.Context())
	require.NoError(t, err, "Ledger.All")
	require.Len(t, recs, 1, "the record is still written")
}

// TestRecordEliminatedDefaultsScopeFromConfig pins the distinction handlers.go's `explicit` variable
// exists for: an OMITTED scope must take eliminations.defaultScope, which a project may have set to
// "project", and the schema's own literal default of "session" cannot know that.
func TestRecordEliminatedDefaultsScopeFromConfig(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.Config) {
		c.Eliminations.DefaultScope = "project"
	}))

	var body eliminatedBody
	f.callOK(t, ToolRecordEliminated, map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": elimReason,
	}, &body)

	require.Equal(t, string(negknow.ScopeProject), body.Scope,
		"the schema default must not win over the configured one")

	recs, err := f.Ledger.All(t.Context())
	require.NoError(t, err, "Ledger.All")
	require.Len(t, recs, 1)
	require.Equal(t, negknow.ScopeProject, recs[0].Scope, "the STORED scope is what visibility reads")
}

// TestRecordEliminatedRejectsUnknownScope pins the schema enum as a real gate. Scope decides
// visibility, so a value outside the enum is not a value to be normalized away — it is a record
// nobody would be able to predict the audience of.
func TestRecordEliminatedRejectsUnknownScope(t *testing.T) {
	f := newFixture(t)

	msg := f.callErr(t, ToolRecordEliminated, map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": elimReason, "scope": "global",
	})
	require.Equal(t, "invalid arguments for record_eliminated: /scope: value not in enum", msg)

	recs, err := f.Ledger.All(t.Context())
	require.NoError(t, err, "Ledger.All")
	require.Empty(t, recs, "a rejected call must not reach the ledger")
}

// TestRecordEliminatedRejectsEmptyReason pins §12.3's field bounds naming the OFFENDING FIELD, so
// the model can fix one thing rather than guess. A reason is the entire payload of an elimination:
// an empty one records that something failed while withholding what a later agent would need.
func TestRecordEliminatedRejectsEmptyReason(t *testing.T) {
	f := newFixture(t)

	msg := f.callErr(t, ToolRecordEliminated, map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": "",
	})
	require.Equal(t, "record_eliminated requires a non-empty reason", msg)
	require.Contains(t, msg, "reason", "the message must name the field that is wrong")
}

// TestRecordEliminatedRejectsOversizeFields pins the other direction of the same bounds. The tool
// REFUSES rather than truncating, because a target cut in half names a different thing and a reason
// cut in half no longer says what it meant.
func TestRecordEliminatedRejectsOversizeFields(t *testing.T) {
	f := newFixture(t)

	msg := f.callErr(t, ToolRecordEliminated, map[string]any{
		"target": strings.Repeat("t", 600), "approach": elimApproach, "reason": elimReason,
	})
	require.Equal(t, "record_eliminated: target is too long (max 512 bytes)", msg)
}

// TestRecordEliminatedRefusesSessionScopeWithoutSession pins the refusal that keeps an invisible
// record from ever being written: SP-09's visibility filter can never return a session-scoped record
// belonging to no session, so writing one would be silently discarding the negative knowledge the
// agent just took the trouble to state.
//
// It calls Dispatch directly because fixture.call always supplies testSession, and an EMPTY session
// is precisely the condition under test.
func TestRecordEliminatedRefusesSessionScopeWithoutSession(t *testing.T) {
	f := newFixture(t)

	sessionless := func(t *testing.T, args map[string]any) Response {
		t.Helper()
		raw, err := json.Marshal(args)
		require.NoError(t, err, "marshalling arguments")
		resp, err := Dispatch(t.Context(), f.Server, Request{
			Session: "", Name: ToolRecordEliminated, Args: raw, Turn: 1,
			Deadline: f.Clock.Now().Add(time.Minute),
		})
		require.NoError(t, err, "Dispatch(%s)", ToolRecordEliminated)
		return resp
	}

	base := map[string]any{"target": elimTarget, "approach": elimApproach, "reason": elimReason}

	refused := sessionless(t, base)
	require.True(t, refused.IsError, "a session-scoped record with no session must be refused")
	require.Equal(t, `cannot record a session-scoped elimination: no live session — `+
		`pass scope="project", or retry once a session is active`, responseText(refused))

	recs, err := f.Ledger.All(t.Context())
	require.NoError(t, err, "Ledger.All")
	require.Empty(t, recs, "the refusal must leave the ledger untouched")

	// The same call, scoped to the project, has a real audience and therefore succeeds.
	scoped := map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": elimReason, "scope": "project",
	}
	accepted := sessionless(t, scoped)
	require.False(t, accepted.IsError, "project scope needs no session: %s", responseText(accepted))

	recs, err = f.Ledger.All(t.Context())
	require.NoError(t, err, "Ledger.All")
	require.Len(t, recs, 1, "the project-scoped record must have been written")
	require.Equal(t, negknow.ScopeProject, recs[0].Scope)
}

// TestRecordEliminatedIsNotEphemeral pins the one exception in ephemeralTools. The other seven tools
// return retrieved content, which is re-fetchable and therefore safe to evict first; this one
// returns the acknowledgement of a DURABLE fact, and filing that in the first-eviction tier — or
// storing a second copy of it under a synthetic tool_use_id — would be filing a write as a read.
func TestRecordEliminatedIsNotEphemeral(t *testing.T) {
	f := newFixture(t)

	resp := f.callOK(t, ToolRecordEliminated, map[string]any{
		"target": elimTarget, "approach": elimApproach, "reason": elimReason, "scope": "project",
	}, nil)

	require.False(t, resp.Ephemeral, "record_eliminated results are not born ephemeral")
	require.NotContains(t, resp.Meta, metaToolUseID, "no ephemeral record may have been minted")
	require.NotContains(t, resp.Meta, metaHash)

	hits, err := f.Store.Search(t.Context(), store.Query{
		Tool: mcpToolPrefix + ToolRecordEliminated, K: 50,
	})
	require.NoError(t, err, "Search for ephemeral record_eliminated captures")
	require.Empty(t, hits, "the tool-use index must hold no ephemeral record for this call")
}

// ── timeline ────────────────────────────────────────────────────────────────────────────────

// TestTimelineRangeByTurn pins the range predicate as INTERSECTION rather than containment. A
// segment that straddles a bound is part of what happened in the window, and excluding it would hide
// exactly the turns at the edges of the range the model asked about.
func TestTimelineRangeByTurn(t *testing.T) {
	f := newFixture(t)
	segs := seedFourSegments(t, f)

	var body timelineBody
	f.callOK(t, ToolTimeline, map[string]any{"from": "12", "to": "30"}, &body)

	require.Equal(t, core.TurnIndex(12), body.From, "the resolved bounds must be echoed back")
	require.Equal(t, core.TurnIndex(30), body.To)
	require.True(t, body.Found)
	require.Equal(t, []core.SegmentID{segs[1].ID, segs[2].ID, segs[3].ID}, segmentIDs(body.Segments),
		"[0,9] does not intersect [12,30]; the other three do")
	require.Equal(t, 3, body.Count)

	// Every rendered field is copied straight off store.Segment; nothing is derived.
	got, want := body.Segments[0], segs[1]
	require.Equal(t, want.StartTurn, got.StartTurn)
	require.Equal(t, want.EndTurn, got.EndTurn)
	require.Equal(t, rfc3339(want.StartTS), got.StartTS)
	require.Equal(t, rfc3339(want.EndTS), got.EndTS)
	require.Equal(t, core.Tokens(segFixtureTokens), got.Tokens)
	require.Equal(t, map[string]float64{"entropy": segFixtureEntropy}, got.Features)
	require.True(t, got.Closed)
	require.False(t, got.EncodedOnce, "nothing has checkpointed these segments")
	require.Equal(t, core.CheckpointSeq(0), got.CheckpointSeq)
}

// TestTimelineEmptyToUsesLastSegmentEnd pins resolveBound's upper-bound default against the trap it
// documents. Frontier is the end of the last CONTIGUOUSLY ENCODED segment, which is 0 in any session
// that has not checkpointed yet, so defaulting `to` to it would make the default range empty in
// exactly the session where the timeline is most useful. The frontier is still REPORTED — it is how
// the model sees how much of the range a checkpoint already covers — it is just not the bound.
func TestTimelineEmptyToUsesLastSegmentEnd(t *testing.T) {
	f := newFixture(t)
	segs := seedFourSegments(t, f)
	last := segs[len(segs)-1]

	var body timelineBody
	f.callOK(t, ToolTimeline, map[string]any{"from": "0"}, &body)

	require.Equal(t, last.EndTurn, body.To, "an empty upper bound is the greatest EndTurn in the log")
	require.Equal(t, core.TurnIndex(0), body.Frontier, "no segment has been encoded, so the frontier is 0")
	require.NotEqual(t, body.Frontier, body.To, "the bound must not have come from the frontier")
	require.Equal(t, len(segs), body.Count, "the default range must cover every segment")
	require.Equal(t, segmentIDs(body.Segments), []core.SegmentID{
		segs[0].ID, segs[1].ID, segs[2].ID, segs[3].ID,
	})
}

// TestTimelineByTimestamp pins the other bound spelling. A model that knows when something happened
// but not at which turn must be able to ask in the vocabulary it has, and the two spellings must
// select the same segments.
func TestTimelineByTimestamp(t *testing.T) {
	f := newFixture(t)
	segs := seedFourSegments(t, f)

	// Each seeded segment spans one minute from the epoch, so these bounds name segment 2's start
	// and segment 3's end exactly.
	from := epoch.Add(time.Minute).Format(time.RFC3339)
	to := epoch.Add(3 * time.Minute).Format(time.RFC3339)

	var body timelineBody
	f.callOK(t, ToolTimeline, map[string]any{"from": from, "to": to}, &body)

	require.Equal(t, segs[1].StartTurn, body.From, "the lower bound resolves to a segment's start turn")
	require.Equal(t, segs[2].EndTurn, body.To, "the upper bound resolves to a segment's end turn")
	require.Equal(t, []core.SegmentID{segs[1].ID, segs[2].ID}, segmentIDs(body.Segments))
	require.Equal(t, 2, body.Count)
}

// TestTimelineBadBoundIsError pins the one timeline case that IS an error. An unparseable bound is an
// invalid argument, not a miss: answering it with an empty range would tell the model its session
// contains nothing when in fact its question was malformed.
func TestTimelineBadBoundIsError(t *testing.T) {
	f := newFixture(t)
	seedFourSegments(t, f)

	msg := f.callErr(t, ToolTimeline, map[string]any{"from": "soon"})
	require.Equal(t, "timeline: from must be a turn index, an RFC3339 timestamp, or empty", msg)
}

// TestTimelineEmptyRangeFoundFalse pins the miss. A window past the end of the session is a
// well-formed question with an empty answer, which is information rather than a failure.
func TestTimelineEmptyRangeFoundFalse(t *testing.T) {
	f := newFixture(t)
	seedFourSegments(t, f)

	var body timelineBody
	resp := f.callOK(t, ToolTimeline, map[string]any{"from": "900", "to": "999"}, &body)

	require.False(t, resp.IsError, "an empty range is a miss, never a tool error")
	require.Equal(t, 0, body.Count)
	require.False(t, body.Found)
	require.Empty(t, body.Segments, "segments must be an empty array, never null")
}

// seedFourSegments lays down the four-segment session every timeline test reads: turns 0-41, closed,
// one minute of fake clock each, none of them encoded.
//
// The turn ranges are chosen so that the [12,30] window straddles a boundary at each end — segment 2
// starts before 12 and segment 4 starts exactly at 30 — which is what makes an intersection test
// distinguishable from a containment test.
func seedFourSegments(t *testing.T, f *fixture) []store.Segment {
	t.Helper()
	return []store.Segment{
		f.seedSegment(t, 0, 9, time.Minute),
		f.seedSegment(t, 10, 19, time.Minute),
		f.seedSegment(t, 20, 29, time.Minute),
		f.seedSegment(t, 30, 41, time.Minute),
	}
}

// segmentIDs lifts the ids out of a rendered timeline, so a failure names segments rather than
// printing four full structs.
func segmentIDs(segs []timelineSegment) []core.SegmentID {
	out := make([]core.SegmentID, 0, len(segs))
	for _, s := range segs {
		out = append(out, s.ID)
	}
	return out
}

// ── why ─────────────────────────────────────────────────────────────────────────────────────

// TestWhyFindsDecisionInLatestCheckpoint pins `why` against the frozen §8.5 artifact itself, field
// for field. The values are SP-10's contract rather than this test's invention, so an assertion that
// passes here is an assertion about a decision the checkpointer really writes.
func TestWhyFindsDecisionInLatestCheckpoint(t *testing.T) {
	f := newFixture(t)
	cp := loadContractCheckpoint(t)
	f.Checks.Chained = []checkpoint.Checkpoint{cp}
	f.Checks.Refs = refsFor(cp.Seq)

	want := cp.Decisions[0]
	var body whyBody
	f.callOK(t, ToolWhy, map[string]any{"decision_id": string(want.ID)}, &body)

	require.True(t, body.Found)
	require.Equal(t, string(want.ID), body.DecisionID)
	require.Equal(t, want.What, body.What)
	require.Equal(t, want.Why, body.Why)
	require.Equal(t, want.AlternativesRejected, body.AlternativesRejected)
	require.Equal(t, want.Evidence.String(), body.Evidence)
	require.Equal(t, want.Turn, body.Turn)
	require.Equal(t, cp.Seq, body.CheckpointSeq)
	require.Equal(t, "call expand with hash="+want.Evidence.String()+" to read the evidence", body.Hint)
	require.Nil(t, body.EvidenceBytes, "the fixture's evidence object is not in this store to size")
}

// TestWhySearchesParentChain pins the walk. A decision the model is asking about may have been made
// several checkpoints ago, and stopping at the latest one would answer "no such decision" for a
// decision the chain plainly holds.
//
// The checkpoints actually READ are asserted at the reader seam rather than from the response,
// because whyBody carries no searched_checkpoints field — only whyMissBody does. That asymmetry is
// deliberate in the miss body's doc comment and is reported as a finding for the found path; the
// assertion below is what the subplan's "searched_checkpoints includes 7 and 6" can mean given the
// shape the implementation emits.
func TestWhySearchesParentChain(t *testing.T) {
	f := newFixture(t)
	cp := loadContractCheckpoint(t)
	want := cp.Decisions[0]

	f.Checks.Chained = []checkpoint.Checkpoint{
		checkpointAtSeq(cp, 7, nil),
		checkpointAtSeq(cp, 6, nil),
		checkpointAtSeq(cp, 5, cp.Decisions),
	}
	f.Checks.Refs = refsFor(5, 6, 7)
	spy := f.withSpyCheckpoints(t)

	var body whyBody
	f.callOK(t, ToolWhy, map[string]any{"decision_id": string(want.ID)}, &body)

	require.True(t, body.Found, "the decision is at seq 5 and the chain reaches it")
	require.Equal(t, core.CheckpointSeq(5), body.CheckpointSeq)
	require.Equal(t, want.What, body.What)
	require.Equal(t, []core.CheckpointSeq{7, 6, 5}, spy.read(),
		"the walk must start at the latest checkpoint and descend without re-reading it")
}

// TestWhyNotFoundReturnsFoundFalse pins the miss shape. "I read checkpoint 7 and it is not there" is
// something the model can act on — it can stop asking — whereas isError would only tell it the call
// failed, which is a reason to retry.
func TestWhyNotFoundReturnsFoundFalse(t *testing.T) {
	f := newFixture(t)
	cp := loadContractCheckpoint(t)
	f.Checks.Chained = []checkpoint.Checkpoint{checkpointAtSeq(cp, 7, cp.Decisions)}
	f.Checks.Refs = refsFor(7)

	var body whyMissBody
	resp := f.callOK(t, ToolWhy, map[string]any{"decision_id": "dec_ffffffffffff"}, &body)

	require.False(t, resp.IsError, "an unknown decision id is a miss, never a tool error")
	require.False(t, body.Found)
	require.Equal(t, "dec_ffffffffffff", body.DecisionID)
	require.Equal(t, []core.CheckpointSeq{7}, body.SearchedCheckpoints,
		"a miss must name what it actually read")
}

// TestWhyNilReaderReportsUnavailable pins SP-10's pre-merge state. A build with no checkpoint reader
// has read nothing, and reporting found:false would be an assertion about the world made by a tool
// that never looked at it.
func TestWhyNilReaderReportsUnavailable(t *testing.T) {
	f := newFixture(t, withoutCheckpoints())

	var body missBody
	resp := f.callOK(t, ToolWhy, map[string]any{"decision_id": "dec_a3f2c9e14b70"}, &body)

	require.False(t, resp.IsError, "a missing collaborator is a degradation, not a tool error")
	require.NotNil(t, body.Available)
	require.False(t, *body.Available)
	require.Equal(t, "checkpoint reader not present in this build", body.Reason)
}

// TestWhyEmptyIDIsError pins the argument gate. An empty decision id is not a decision that cannot be
// found: there is nothing to look for, and walking thirty-two checkpoints to say so would spend
// budget B-F proving that "" is not an id.
func TestWhyEmptyIDIsError(t *testing.T) {
	f := newFixture(t)
	f.Checks.Chained = []checkpoint.Checkpoint{loadContractCheckpoint(t)}

	msg := f.callErr(t, ToolWhy, map[string]any{"decision_id": ""})
	require.Equal(t, "why requires a non-empty decision_id", msg)
}

// ── dropped ─────────────────────────────────────────────────────────────────────────────────

// TestDroppedReturnsDropReport pins G4.5's explicit drop report. Order is part of the contract: the
// rehydrator reports what it dropped in the order it dropped it, and a tool that re-sorted the list
// would be presenting its own ranking as the rehydrator's.
func TestDroppedReturnsDropReport(t *testing.T) {
	f := newFixture(t)
	f.Drops.Entries = []checkpoint.DropEntry{
		{
			Kind: "path_rule", ID: "api-conventions.md",
			Detail: "path-scoped rule for src/api/**; no pointer in this checkpoint matched its globs",
		},
		{
			Kind: "tool_output", ID: "toolu_01M3N4P5Q6R7S8T9U0V1W2X3",
			Detail: "superseded by a later read of the same file",
		},
		{Kind: "narrative", ID: "0006.json"},
	}

	var body droppedBody
	resp := f.callOK(t, ToolDropped, map[string]any{}, &body)

	require.False(t, resp.IsError)
	require.Equal(t, len(f.Drops.Entries), body.Count)
	require.Equal(t, f.Drops.Entries, body.Drops, "every entry must round-trip unchanged, in order")
	require.Nil(t, body.Available, "a build that CAN answer says nothing about availability")
	require.Equal(t, 1, f.Drops.Calls, "the tool must consult the reporter exactly once")
}

// TestDroppedNilReporterReportsUnavailable pins why droppedBody carries its own availability pair
// instead of reusing missBody: "this build has no rehydrator" and "nothing was dropped" must both
// answer with an empty drops array, and a model that could not tell them apart would guess — and the
// wrong guess is the one where it stops asking.
func TestDroppedNilReporterReportsUnavailable(t *testing.T) {
	f := newFixture(t, withoutDrops())

	var body droppedBody
	resp := f.callOK(t, ToolDropped, map[string]any{}, &body)

	require.False(t, resp.IsError, "a missing collaborator is a degradation, not a tool error")
	require.NotNil(t, body.Available)
	require.False(t, *body.Available)
	require.Equal(t, 0, body.Count)
	require.Empty(t, body.Drops, "drops must be an empty array, never null")
	require.Equal(t, "rehydrator not present in this build", body.Reason)
}

// TestDroppedReporterErrorIsToolError pins the one dropped case that is NOT a degradation. A
// reporter that is present and failed is a backend failure, which handlers_common.go reserves
// isError for — and it is loud, because a rehydrator that cannot say what it dropped has broken the
// guarantee G4.5 rests on.
func TestDroppedReporterErrorIsToolError(t *testing.T) {
	f := newFixture(t)
	log := f.withSpyLogger(t)
	f.Drops.Err = errors.New("the drop report is unreadable")

	msg := f.callErr(t, ToolDropped, map[string]any{})

	require.Equal(t, "dropped failed: the drop report is unreadable", msg)
	require.Equal(t, 1, log.loudCount(), "a broken drop reporter is a Loud event, exactly once")
	require.Equal(t, "mcp: the drop reporter failed", log.lastLoud(t))
}

// ── checkpoint-tool gating ──────────────────────────────────────────────────────────────────

// TestCheckpointDependentToolsAreExplicitlyGateable pins the SP-13 interface contract's escape
// from a circular dependency: `why` and `dropped` are the only two of the eight tools that depend
// on checkpoint/rehydration state, and this is what lets a build ship and verify the other six
// (core archive retrieval) without waiting on that work. A REAL checkpoint reader and rehydrator
// ARE wired below — which is exactly what distinguishes "disabled" from "not present in this
// build": the collaborator exists, is never consulted, and the gate still refuses the call with an
// explicit reason rather than silently answering as if the collaborator were absent.
func TestCheckpointDependentToolsAreExplicitlyGateable(t *testing.T) {
	f := newFixture(t, withCheckpointToolsDisabled())
	f.Checks.Chained = []checkpoint.Checkpoint{loadContractCheckpoint(t)}

	for name, args := range map[string]map[string]any{
		ToolWhy:     {"decision_id": "dec_000000000000"},
		ToolDropped: {},
	} {
		t.Run(name, func(t *testing.T) {
			var body missBody
			resp := f.callOK(t, name, args, &body)

			require.False(t, resp.IsError, "a disabled tool is a domain outcome, not a tool failure")
			require.NotNil(t, body.Available, "a disabled tool must state availability explicitly")
			require.False(t, *body.Available)
			require.Contains(t, body.Reason, "disabled", "the reason must say WHY, not just that it is unavailable")
		})
	}
	require.Equal(t, 0, f.Drops.Calls, "a disabled tool must never consult its collaborator")
}

// TestCheckpointDependentToolsRemainListedWhenDisabled pins "not silently absent": tools/list
// still advertises `why` and `dropped` even when the gate is off, so a host does not have to
// special-case tool discovery around a purely runtime, per-call decision.
func TestCheckpointDependentToolsRemainListedWhenDisabled(t *testing.T) {
	f := newFixture(t, withCheckpointToolsDisabled())

	names := map[string]bool{}
	for _, tool := range f.Server.Tools() {
		names[tool.Name] = true
	}
	require.True(t, names[ToolWhy], "a disabled tool must remain advertised in tools/list")
	require.True(t, names[ToolDropped], "a disabled tool must remain advertised in tools/list")
}
