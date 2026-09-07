package mcp

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// The recording collaborators the knowledge-and-index tool tests need on top of fixture_test.go's
// set, plus the two seeding helpers those tests share.
//
// fixture_test.go already owns the FAKES — fakeCheckpoints, fakeDrops, fakeWidener — which answer
// with whatever a test loaded into them. What is missing there is the other half: wrappers that
// pass a call THROUGH to the real implementation and record what it was handed. Three assertions
// in handlers_test.go are about the argument rather than the answer — the store.Query a selector
// string parsed into, the negknow.Scope already_tried resolved from configuration, and the
// checkpoint sequence numbers `why` actually read — and none of them is observable from a response
// body, because the response carries the RESULT of the call and not its inputs.
//
// Everything here wraps rather than replaces, so a spy never changes what the tool answers: the
// tests that assert an argument and the tests that assert a body are looking at the same code path.

// spyStore records every store.Query Search was handed and then delegates to the real store.
//
// It embeds the interface rather than a concrete *store.FSStore so that only Search is overridden
// and the other twenty methods stay whatever the fixture opened — a hand-written forwarder for all
// of them would be twenty chances to drift from the real one.
type spyStore struct {
	store.Store

	mu      sync.Mutex
	queries []store.Query
}

// spyStore must satisfy the interface ToolDeps declares.
var _ store.Store = (*spyStore)(nil)

// Search records q and forwards it.
func (s *spyStore) Search(ctx context.Context, q store.Query) ([]store.Hit, error) {
	s.mu.Lock()
	s.queries = append(s.queries, q)
	s.mu.Unlock()
	return s.Store.Search(ctx, q)
}

// lastQuery returns the most recent recorded query, failing the test when there was none. It fails
// rather than returning a zero Query because a zero Query is a legitimate value a handler could
// have sent, and "the handler searched for nothing" must not be indistinguishable from "the handler
// never searched".
func (s *spyStore) lastQuery(t *testing.T) store.Query {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.queries, "the tool did not reach store.Search at all")
	return s.queries[len(s.queries)-1]
}

// ledgerQuery is one recorded negknow.Ledger.Query call: the three arguments already_tried resolves
// before it asks anything.
type ledgerQuery struct {
	Target   string
	Approach string
	Scope    negknow.Scope
}

// spyLedger records what the two negative-knowledge tools handed the ledger, and can stand in for
// its answer entirely.
//
// The Answer/Err overrides exist because two of §8.3's three answers cannot be produced by driving a
// real ledger from outside it. BloomOnly is a FALSE POSITIVE of a probabilistic filter — nothing a
// test can arrange on demand — and the §12.3 ledger-failure path needs a Query that fails, which a
// healthy ledger by definition never does. Both are still real negknow.Answer values travelling the
// real handler path; only their origin is forced.
//
// It deliberately does NOT implement negknow.Maintainer. Embedding the Ledger interface promotes
// only Ledger's methods, so IngestMCP is absent and recordEliminated takes its bare-Ledger fallback
// — which is the path a conforming implementation that never opted into Maintainer would take, and
// therefore worth being able to reach from a test.
type spyLedger struct {
	negknow.Ledger

	mu      sync.Mutex
	queries []ledgerQuery
	records []negknow.Record

	// Answer, when non-nil, is returned from Query instead of the inner ledger's answer.
	Answer *negknow.Answer
	// Err, when non-nil, is returned from Query instead of an answer.
	Err error
}

// spyLedger must satisfy the interface ToolDeps declares.
var _ negknow.Ledger = (*spyLedger)(nil)

// Query records its arguments, then answers from the override when one is set and from the inner
// ledger otherwise.
func (l *spyLedger) Query(ctx context.Context, target, approach string, scope negknow.Scope) (
	negknow.Answer, error,
) {
	l.mu.Lock()
	l.queries = append(l.queries, ledgerQuery{Target: target, Approach: approach, Scope: scope})
	l.mu.Unlock()

	switch {
	case l.Err != nil:
		return negknow.Answer{}, l.Err
	case l.Answer != nil:
		return *l.Answer, nil
	}
	return l.Ledger.Query(ctx, target, approach, scope)
}

// Record records the Record it was handed and forwards it.
func (l *spyLedger) Record(ctx context.Context, r negknow.Record) (string, error) {
	l.mu.Lock()
	l.records = append(l.records, r)
	l.mu.Unlock()
	return l.Ledger.Record(ctx, r)
}

// queried returns a copy of the recorded Query calls.
func (l *spyLedger) queried() []ledgerQuery {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ledgerQuery, len(l.queries))
	copy(out, l.queries)
	return out
}

// spyCheckpoints records which checkpoint sequence numbers a tool actually READ, in order.
//
// It exists because `why`'s hit response reports the checkpoint it found the decision in but not
// the ones it walked past — whyBody has no searched_checkpoints field, only whyMissBody does — so
// the parent-chain walk is otherwise unobservable on the path where it succeeds. Recording the
// reads at the seam turns "it searched 7 and 6 before finding 5" back into an assertion about
// behaviour rather than about a field that is not emitted.
type spyCheckpoints struct {
	*fakeCheckpoints

	mu    sync.Mutex
	reads []core.CheckpointSeq
}

// spyCheckpoints must satisfy the interface ToolDeps declares.
var _ checkpoint.Reader = (*spyCheckpoints)(nil)

// Latest records the sequence number of whatever it returns.
func (c *spyCheckpoints) Latest(ctx context.Context, s core.SessionID) (
	checkpoint.Checkpoint, checkpoint.Ref, error,
) {
	cp, ref, err := c.fakeCheckpoints.Latest(ctx, s)
	if err == nil {
		c.note(cp.Seq)
	}
	return cp, ref, err
}

// Get records seq before answering.
func (c *spyCheckpoints) Get(ctx context.Context, seq core.CheckpointSeq) (
	checkpoint.Checkpoint, checkpoint.Ref, error,
) {
	cp, ref, err := c.fakeCheckpoints.Get(ctx, seq)
	if err == nil {
		c.note(seq)
	}
	return cp, ref, err
}

// note appends one read.
func (c *spyCheckpoints) note(seq core.CheckpointSeq) {
	c.mu.Lock()
	c.reads = append(c.reads, seq)
	c.mu.Unlock()
}

// read returns the sequence numbers read so far, in order.
func (c *spyCheckpoints) read() []core.CheckpointSeq {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]core.CheckpointSeq, len(c.reads))
	copy(out, c.reads)
	return out
}

// spyLogger captures the Loud channel and discards everything else.
//
// §12.3 makes a degraded answer a LOUD event rather than a Warn, and "the tool degraded quietly"
// and "the tool degraded and said so" are different outcomes with the same response body — so the
// only way to tell them apart is to hold the logger. It is a local capture rather than
// logging.AttachLoudObserver because that seam is process-wide: a test that installed an observer
// would also see every Loud call made by every other test sharing the binary.
type spyLogger struct {
	mu   sync.Mutex
	loud []string
}

// spyLogger must satisfy the interface ToolDeps declares.
var _ logging.Logger = (*spyLogger)(nil)

// With returns the same capture: fields are not what these tests assert on, and a derived logger
// that dropped its Loud calls would silently defeat the whole point of the type.
func (l *spyLogger) With(_ ...any) logging.Logger { return l }

// Debug discards.
func (l *spyLogger) Debug(_ string, _ ...any) {}

// Info discards.
func (l *spyLogger) Info(_ string, _ ...any) {}

// Warn discards.
func (l *spyLogger) Warn(_ string, _ ...any) {}

// Error discards.
func (l *spyLogger) Error(_ string, _ ...any) {}

// Loud captures the message.
func (l *spyLogger) Loud(msg string, _ ...any) {
	l.mu.Lock()
	l.loud = append(l.loud, msg)
	l.mu.Unlock()
}

// loudCount returns how many Loud calls were made.
func (l *spyLogger) loudCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.loud)
}

// lastLoud returns the most recent Loud message, failing the test when there was none.
func (l *spyLogger) lastLoud(t *testing.T) string {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	require.NotEmpty(t, l.loud, "nothing was written to the Loud channel")
	return l.loud[len(l.loud)-1]
}

// rewire rebuilds f.Server from the CURRENT f.Deps.
//
// The eight handlers close over the collaborator set at RegisterAll time — newHandlers copies
// ToolDeps into an unexported struct — so swapping a dep after newFixture has run has no effect
// until the tools are registered again. Rebuilding the server rather than calling a handler
// directly is what keeps a spy-driven test on the same Dispatch path as every other test, schema
// validation and the run() preamble included.
func (f *fixture) rewire(t *testing.T) {
	t.Helper()
	f.Server = NewServerWithOptions(ServerOptions{
		Name: ServerName, Version: core.Version, Log: logging.Nop(),
	})
	require.NoError(t, RegisterAll(f.Server, f.Deps), "RegisterAll after rewiring")
}

// withSpyStore puts a recording wrapper in front of the fixture's store and re-registers the tools.
// f.Store keeps naming the REAL store, so a test can still seed and read through it directly.
func (f *fixture) withSpyStore(t *testing.T) *spyStore {
	t.Helper()
	require.NotNil(t, f.Store, "withSpyStore needs a store to wrap")
	spy := &spyStore{Store: f.Store}
	f.Deps.Store = spy
	f.rewire(t)
	return spy
}

// withSpyLedger puts a recording wrapper in front of the fixture's ledger and re-registers the
// tools. f.Ledger keeps naming the real ledger.
func (f *fixture) withSpyLedger(t *testing.T) *spyLedger {
	t.Helper()
	require.NotNil(t, f.Ledger, "withSpyLedger needs a ledger to wrap")
	spy := &spyLedger{Ledger: f.Ledger}
	f.Deps.Ledger = spy
	f.rewire(t)
	return spy
}

// withSpyCheckpoints puts a recording wrapper in front of the fixture's checkpoint fake and
// re-registers the tools. f.Checks keeps naming the fake, so a test loads the chain the same way.
func (f *fixture) withSpyCheckpoints(t *testing.T) *spyCheckpoints {
	t.Helper()
	require.NotNil(t, f.Checks, "withSpyCheckpoints needs a checkpoint reader to wrap")
	spy := &spyCheckpoints{fakeCheckpoints: f.Checks}
	f.Deps.Checkpoints = spy
	f.rewire(t)
	return spy
}

// withSpyLogger swaps the fixture's logging.Nop() for a Loud capture and re-registers the tools.
func (f *fixture) withSpyLogger(t *testing.T) *spyLogger {
	t.Helper()
	spy := &spyLogger{}
	f.Deps.Log = spy
	f.rewire(t)
	return spy
}

// The one elimination the negative-knowledge tests key on, taken from §8.3's own worked example so
// that a target/approach/reason triple in a failure message is one a reader has already seen. The
// reason is the exact sentence the contract fixture's eliminated[0] carries.
const (
	elimTarget   = "src/auth.ts:refreshToken"
	elimApproach = "widen pool timeout"
	elimReason   = "pgbouncer 1.18 ignores it in transaction mode"
)

// seedElimination appends one active, evidence-backed elimination straight to the ledger and
// returns its id and its evidence hash.
//
// It goes through Ledger.Record rather than through the record_eliminated TOOL on purpose: an
// already_tried test must not depend on record_eliminated being correct, or a single bug in the
// write path would take both halves of §8.3 down together and neither failure would name it.
// Evidence is minted explicitly because eliminations.requireEvidence defaults to true, so a record
// without one is refused before it is ever indexed.
func (f *fixture) seedElimination(t *testing.T, scope negknow.Scope) (id string, evidence core.Hash) {
	t.Helper()

	res, err := f.Store.PutBytes(t.Context(), []byte(elimReason), store.PutOptions{
		Tool: ToolRecordEliminated,
	})
	require.NoError(t, err, "storing the elimination's evidence")

	id, err = f.Ledger.Record(t.Context(), negknow.Record{
		Target: elimTarget, Approach: elimApproach, Reason: elimReason,
		Evidence: res.Root.Hash, Scope: scope,
		Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err, "Ledger.Record")
	require.NotEmpty(t, id, "Ledger.Record returned an empty id")
	return id, res.Root.Hash
}

// seedSegment opens one segment over [start, end] and closes it, returning the stored Segment.
//
// span is how far the clock advances between the open and the close, and it is the ONLY way a test
// can give two segments distinct end timestamps: SegmentLog.Close stamps EndTS from the log's own
// clock and accepts no override, whereas Open honours a supplied StartTS. Every segment therefore
// starts where the previous one ended, which is also what a real session's segments do.
func (f *fixture) seedSegment(t *testing.T, start, end core.TurnIndex, span time.Duration) store.Segment {
	t.Helper()

	segs := f.Store.Segments()
	require.NotNil(t, segs, "the fixture store must expose a segment log")

	id, err := segs.Open(t.Context(), store.Segment{
		Session: testSession, StartTurn: start, StartTS: core.NowMilli(f.Clock),
	})
	require.NoError(t, err, "SegmentLog.Open(start=%d)", start)

	f.Clock.Advance(span)
	require.NoError(t, segs.Close(t.Context(), id, end, map[string]float64{
		"tokens": segFixtureTokens, "entropy": segFixtureEntropy,
	}), "SegmentLog.Close(%d, end=%d)", id, end)

	seg, err := segs.Get(t.Context(), id)
	require.NoError(t, err, "SegmentLog.Get(%d)", id)
	return seg
}

// The feature values every seeded segment closes with. They are fixed so that a timeline assertion
// about Tokens or Features is an assertion about what the tool COPIED, not about what the segment
// log happened to compute.
const (
	segFixtureTokens  = 1200
	segFixtureEntropy = 0.5
)

// contractCheckpointPath is the frozen §8.5 checkpoint artifact, repository-relative.
//
// It is the ONLY checkpoint fixture that exists. The subplan names
// testdata/golden/contracts/checkpoint/{full,minimal,empty}.json; that directory holds MANIFEST.json
// and want/0001.json and nothing else, and MANIFEST.json declares want/0001.json frozen and owned by
// SP-10. Adding the three named files would be minting a contract this seat does not own, so the
// one real artifact is loaded here and the chain variants `why` needs are built from it in Go.
const contractCheckpointPath = "testdata/golden/contracts/checkpoint/want/0001.json"

// loadContractCheckpoint decodes the frozen checkpoint contract fixture.
//
// Reading it with os.ReadFile rather than through internal/testutil.ContractFixture is forced:
// testutil imports internal/cli, cli imports internal/mcp since SP-13, and an mcp test importing
// testutil closes an import cycle the test build refuses (see fakeclock_test.go). goldenPath
// resolves the repository root the same way testutil does.
func loadContractCheckpoint(t *testing.T) checkpoint.Checkpoint {
	t.Helper()

	raw, err := os.ReadFile(goldenPath(t, contractCheckpointPath))
	require.NoError(t, err, "reading %s", contractCheckpointPath)

	var cp checkpoint.Checkpoint
	require.NoError(t, json.Unmarshal(raw, &cp), "decoding %s", contractCheckpointPath)
	require.NotEmpty(t, cp.Decisions, "%s must carry at least one decision", contractCheckpointPath)
	return cp
}

// checkpointAtSeq returns cp re-stamped at seq and carrying exactly decisions.
//
// It is how a multi-checkpoint chain is built out of the single frozen artifact: the values stay the
// contract's own, and only the two fields the chain walk reads — the sequence number and the
// decision list — differ between links. A nil decisions slice makes a link that holds none, which is
// what forces `why` past it into its parent.
func checkpointAtSeq(cp checkpoint.Checkpoint, seq core.CheckpointSeq,
	decisions []checkpoint.Decision,
) checkpoint.Checkpoint {
	out := cp
	out.Seq = seq
	out.Decisions = decisions
	return out
}

// refsFor renders one Ref per sequence number, in the ascending order Reader.List promises.
func refsFor(seqs ...core.CheckpointSeq) []checkpoint.Ref {
	out := make([]checkpoint.Ref, 0, len(seqs))
	for _, s := range seqs {
		out = append(out, checkpoint.Ref{Seq: s})
	}
	return out
}
