// This is an INTERNAL test package (package observer, not observer_test) because the PostToolUse
// pipeline's assertions are about per-session state — st.ToolUses, st.Recent, st.PrefixTokens —
// and about which collaborator was called with what. Neither is reachable through the §5.21
// interface, which is exactly why observertest's own doc comment says SP-08 must assert the
// verbatim capture and the subagent capture white-box in here.
//
// Every double in this file is local. internal/testutil is a composition root, and §3.2 forbids an
// IN-PACKAGE test file from importing one (only an external `package foo_test` file may), so the
// clock below is a two-method core.Clock double of the kind internal/dag/clock_test.go already
// carries for the same reason.
package observer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// testEpoch is the instant every fakeClock starts at. It is a fixed date rather than a wall-clock
// read so that a FeatureSample's TS and a ToolUseRecord's TS are reproducible across runs.
var testEpoch = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// fakeClock is the local core.Clock double: a monotone instant a test advances by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: testEpoch} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// Advance moves the clock forward by d.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// putCall is one recorded PutBytes call: the bytes the observer handed the store, and the options
// it chose for them — which is where per-tool canonicalizer selection is actually asserted.
type putCall struct {
	Body []byte
	Opts store.PutOptions
}

// fileVersionCall is one recorded AppendFileVersion call.
type fileVersionCall struct {
	Path    string
	Version store.FileVersion
}

// supersedeCall is one recorded MarkSuperseded call.
type supersedeCall struct {
	Older core.ToolUseID
	By    core.ToolUseID
}

// fakeStore records what the observer asked of the store. It embeds the store.Store interface so
// that a method this package never calls does not have to be written out; calling one panics on
// the nil embedded interface, which is a louder failure than a silent zero value.
type fakeStore struct {
	store.Store

	mu sync.Mutex

	Puts         []putCall
	Records      []store.ToolUseRecord
	FileVersions []fileVersionCall
	Supersedes   []supersedeCall
	ByPathCalls  int
	FlushCalls   int

	// ByPathLimits records the limit argument of every ToolUsesByPath call, so the
	// supersessionLookback cap is asserted at the CALL rather than inferred from the answer.
	ByPathLimits []int

	// Roots is this double's index/roots.jsonl: PutBytes registers every root it mints and setRoot
	// stages a prior read's chunk list. GetRoot answers out of it, which is what lets a chunk-set
	// row exist at all — PutBytes mints one chunk per payload, so a superset relation can only be
	// staged, never produced.
	Roots map[core.Hash]store.Root

	// PutErr, RecordErr, ByPathErr, GetRootErr and MarkErr make the corresponding call fail.
	PutErr     error
	RecordErr  error
	ByPathErr  error
	GetRootErr error
	MarkErr    error

	// TokenQueue supplies Root.Tokens for successive PutBytes calls; DefaultTokens is used once
	// it is exhausted.
	TokenQueue    []core.Tokens
	DefaultTokens core.Tokens

	// Signature is returned as PutResult.Signature on every call.
	Signature sketch.Signature

	// NearDup is returned as PutResult.NearDup on every call, so a test can stage the
	// near-duplicate the STORE would have found on the way in.
	NearDup *store.NearDupInfo

	// GetRoots records every root GetRoot was asked for, so a test can assert that a candidate
	// was skipped BEFORE the lookup rather than after it.
	GetRoots []core.Hash
}

func newFakeStore() *fakeStore { return &fakeStore{Roots: make(map[core.Hash]store.Root)} }

func (s *fakeStore) PutBytes(_ context.Context, b []byte, o store.PutOptions) (store.PutResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.PutErr != nil {
		return store.PutResult{}, s.PutErr
	}
	body := append([]byte(nil), b...)
	s.Puts = append(s.Puts, putCall{Body: body, Opts: o})

	tok := s.DefaultTokens
	if len(s.TokenQueue) > 0 {
		tok = s.TokenQueue[0]
		s.TokenQueue = s.TokenQueue[1:]
	}
	h := core.HashBytes(core.DomainArgs, body)
	root := store.Root{
		Hash:       h,
		Chunks:     []core.ChunkRef{{Hash: h, Len: len(body)}},
		CanonBytes: int64(len(body)),
		RawBytes:   int64(len(body)),
		Tokens:     tok,
	}
	s.Roots[h] = root
	return store.PutResult{Root: root, Novel: 1, Signature: s.Signature, NearDup: s.NearDup}, nil
}

// GetRoot answers out of Roots, the way SP-06's own GetRoot answers out of its in-memory index. A
// root nobody stored is an error rather than a zero value, because a zero Root has an EMPTY chunk
// list and isSuperset would then quietly report "not a superset" instead of the miss it is.
func (s *fakeStore) GetRoot(_ context.Context, root core.Hash) (store.Root, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.GetRoots = append(s.GetRoots, root)
	if s.GetRootErr != nil {
		return store.Root{}, s.GetRootErr
	}
	r, ok := s.Roots[root]
	if !ok {
		return store.Root{}, fmt.Errorf("fakeStore: no such root %s", root)
	}
	return r, nil
}

func (s *fakeStore) RecordToolUse(_ context.Context, rec store.ToolUseRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.RecordErr != nil {
		return s.RecordErr
	}
	s.Records = append(s.Records, rec)
	return nil
}

func (s *fakeStore) AppendFileVersion(_ context.Context, path string, v store.FileVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FileVersions = append(s.FileVersions, fileVersionCall{Path: path, Version: v})
	return nil
}

// ToolUsesByPath serves the recorded index, newest first and capped at limit — the §5.8 contract
// supersede.go relies on, which TestSupersede_ToolUsesByPathIsMostRecentFirst pins against the REAL
// store so a drift between this double and SP-06 is caught rather than assumed away.
func (s *fakeStore) ToolUsesByPath(_ context.Context, path string, limit int) ([]store.ToolUseRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ByPathCalls++
	s.ByPathLimits = append(s.ByPathLimits, limit)
	if s.ByPathErr != nil {
		return nil, s.ByPathErr
	}
	var out []store.ToolUseRecord
	for i := len(s.Records) - 1; i >= 0; i-- {
		if s.Records[i].Path != path {
			continue
		}
		out = append(out, s.Records[i])
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// MarkSuperseded records the call AND applies it, so a second scan of the same path sees the flip.
// Without the mutation TestSupersede_SkipsAlreadySuperseded would assert nothing.
func (s *fakeStore) MarkSuperseded(_ context.Context, older, by core.ToolUseID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MarkErr != nil {
		return s.MarkErr
	}
	s.Supersedes = append(s.Supersedes, supersedeCall{Older: older, By: by})
	for i := range s.Records {
		if s.Records[i].ID == older {
			s.Records[i].Status, s.Records[i].SupersededBy = store.StatusSuperseded, by
		}
	}
	return nil
}

// seed stages tool_use index entries that the observer never wrote, so a test can describe prior
// reads whose chunk sets, signatures, timestamps or classes the pipeline cannot produce.
func (s *fakeStore) seed(recs ...store.ToolUseRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Records = append(s.Records, recs...)
}

// setRoot registers r so GetRoot can answer for a seeded record.
func (s *fakeStore) setRoot(r store.Root) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Roots[r.Hash] = r
}

func (s *fakeStore) Flush(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FlushCalls++
	return nil
}

// storeCounts is the call-count tuple TestModePassiveStillWrites compares between two runs.
type storeCounts struct{ Puts, Records, FileVersions int }

func (s *fakeStore) counts() storeCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return storeCounts{Puts: len(s.Puts), Records: len(s.Records), FileVersions: len(s.FileVersions)}
}

// putOpts returns the options of the i-th PutBytes call.
func (s *fakeStore) putOpts(i int) store.PutOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Puts[i].Opts
}

// fakeGraph is the dag.Graph double. Out and In are implemented FAITHFULLY, indexing edges by
// endpoint, because dag.BuildToolUse's turnLinkKind reads them through sharesState to decide
// whether the assistant → tool_use link is an EdgeSequence or an EdgeControlOnly. A double that
// returned nil from both would silently make every observer-produced turn link control-only.
type fakeGraph struct {
	dag.Graph

	mu sync.Mutex

	Nodes []dag.Node
	Edges []dag.Edge

	byID map[dag.NodeID]dag.Node
	out  map[dag.NodeID][]dag.Edge
	in   map[dag.NodeID][]dag.Edge

	FlushCalls int

	AddNodeErr error
	AddEdgeErr error
}

func newFakeGraph() *fakeGraph {
	return &fakeGraph{
		byID: make(map[dag.NodeID]dag.Node),
		out:  make(map[dag.NodeID][]dag.Edge),
		in:   make(map[dag.NodeID][]dag.Edge),
	}
}

func (g *fakeGraph) AddNode(n dag.Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.AddNodeErr != nil {
		return g.AddNodeErr
	}
	if _, ok := g.byID[n.ID]; ok {
		return nil
	}
	g.byID[n.ID] = n
	g.Nodes = append(g.Nodes, n)
	return nil
}

func (g *fakeGraph) AddEdge(e dag.Edge) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.AddEdgeErr != nil {
		return g.AddEdgeErr
	}
	for _, have := range g.Edges {
		if have.From == e.From && have.To == e.To && have.Kind == e.Kind {
			return nil
		}
	}
	g.Edges = append(g.Edges, e)
	g.out[e.From] = append(g.out[e.From], e)
	g.in[e.To] = append(g.in[e.To], e)
	return nil
}

func (g *fakeGraph) Node(id dag.NodeID) (dag.Node, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.byID[id]
	return n, ok
}

func (g *fakeGraph) Out(id dag.NodeID) []dag.Edge {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]dag.Edge(nil), g.out[id]...)
}

func (g *fakeGraph) In(id dag.NodeID) []dag.Edge {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]dag.Edge(nil), g.in[id]...)
}

func (g *fakeGraph) Flush(_ context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.FlushCalls++
	return nil
}

// has reports whether id was added as a node.
func (g *fakeGraph) has(id dag.NodeID) bool {
	_, ok := g.Node(id)
	return ok
}

// nodeCount returns how many nodes of kind are present.
func (g *fakeGraph) nodeCount(kind dag.NodeKind) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, node := range g.Nodes {
		if node.Kind == kind {
			n++
		}
	}
	return n
}

// edges returns a copy of every edge added so far.
func (g *fakeGraph) edges() []dag.Edge {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]dag.Edge(nil), g.Edges...)
}

// graphCounts is the node/edge tuple TestModePassiveStillWrites compares between two runs.
type graphCounts struct{ Nodes, Edges int }

func (g *fakeGraph) counts() graphCounts {
	g.mu.Lock()
	defer g.mu.Unlock()
	return graphCounts{Nodes: len(g.Nodes), Edges: len(g.Edges)}
}

// fakeSymbols is the SymbolLister double: a fixed name list, optionally keyed by path.
type fakeSymbols struct {
	mu sync.Mutex

	Fixed   []string
	ByPath  map[string][]string
	Calls   int
	LastLen int
}

func (f *fakeSymbols) Names(path string, b []byte) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	f.LastLen = len(b)
	if f.ByPath != nil {
		return f.ByPath[path]
	}
	return f.Fixed
}

// fakeGrammar is the grammar.Sequitur double: it records the symbol stream and returns a fixed
// thrash answer. It embeds the interface so the four methods SP-08 never calls need no body.
type fakeGrammar struct {
	grammar.Sequitur

	mu        sync.Mutex
	Appended  []grammar.Symbol
	Thrashing []grammar.Rule
}

func (g *fakeGrammar) Append(s grammar.Symbol) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Appended = append(g.Appended, s)
}

func (g *fakeGrammar) Thrash(_ int) []grammar.Rule {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]grammar.Rule(nil), g.Thrashing...)
}

func (g *fakeGrammar) appended() []grammar.Symbol {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]grammar.Symbol(nil), g.Appended...)
}

// signalCall and featureCall are one delivery through Options.OnSignals / Options.OnFeatures.
type signalCall struct {
	Session core.SessionID
	Signals Signals
}

type featureCall struct {
	Session core.SessionID
	Sample  FeatureSample
}

// harness wires a real observer to the doubles above and keeps every callback delivery.
type harness struct {
	t   *testing.T
	obs *observer

	Store   *fakeStore
	Graph   *fakeGraph
	Clock   *fakeClock
	Metrics obs.Registry
	Touch   *sketch.CMS
	Explore *sketch.HLL
	Hot     *sketch.MisraGries

	mu           sync.Mutex
	signalCalls  []signalCall
	featureCalls []featureCall
}

// The sketch dimensions the harness builds. They are test fixtures, not config defaults: a CMS
// this small still answers Estimate exactly for the handful of keys one test feeds it.
const (
	testCMSEpsilon = 0.01
	testCMSDelta   = 0.05
	testHLLRegs    = 512
	testMGCounters = 16
)

// newHarness builds an observer over the doubles, applying every mutator to Options first.
func newHarness(t *testing.T, mutators ...func(*Options)) *harness {
	t.Helper()

	h := &harness{
		t:       t,
		Store:   newFakeStore(),
		Graph:   newFakeGraph(),
		Clock:   newFakeClock(),
		Touch:   sketch.NewCMS(testCMSEpsilon, testCMSDelta),
		Explore: sketch.NewHLL(testHLLRegs),
		Hot:     sketch.NewMisraGries(testMGCounters),
	}
	h.Metrics = obs.New(h.Clock)

	o := Options{
		ProjectRoot: t.TempDir(),
		Cfg:         config.Defaults(),
		Store:       h.Store,
		Graph:       h.Graph,
		Touch:       h.Touch,
		Explore:     h.Explore,
		Hot:         h.Hot,
		Log:         logging.Nop(),
		Metrics:     h.Metrics,
		Clock:       h.Clock,
		OnSignals: func(s core.SessionID, sig Signals) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.signalCalls = append(h.signalCalls, signalCall{Session: s, Signals: sig})
		},
		OnFeatures: func(s core.SessionID, f FeatureSample) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.featureCalls = append(h.featureCalls, featureCall{Session: s, Sample: f})
		},
	}
	for _, m := range mutators {
		m(&o)
	}

	built, err := New(o)
	require.NoError(t, err)
	impl, ok := built.(*observer)
	require.True(t, ok, "New must return the concrete observer")
	h.obs = impl
	return h
}

// counter reads a metric the observer bumped.
func (h *harness) counter(name string) int64 { return h.Metrics.Counter(name).Value() }

// signals returns a copy of every OnSignals delivery so far.
func (h *harness) signals() []signalCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]signalCall(nil), h.signalCalls...)
}

// features returns a copy of every OnFeatures delivery so far.
func (h *harness) features() []featureCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]featureCall(nil), h.featureCalls...)
}

// state returns the session state for s, created exactly the way an entry point would create it.
func (h *harness) state(s core.SessionID) *sessionState { return h.obs.session(s) }

// testSession is the session id every single-session test uses.
const testSession = core.SessionID("sess_sp08")

// toolUse builds a PostToolUse event. input and response are raw JSON so a test pins the exact
// bytes the host would have sent.
func toolUse(id, tool, input, response string) Event {
	e := Event{
		HookEventName: "PostToolUse",
		SessionID:     testSession,
		ToolName:      tool,
		ToolUseID:     core.ToolUseID(id),
	}
	if input != "" {
		e.ToolInput = json.RawMessage(input)
	}
	if response != "" {
		e.ToolResponse = json.RawMessage(response)
	}
	return e
}

// readOf builds a Read of path whose result is body.
func readOf(id, path, body string) Event {
	return toolUse(id, "Read",
		fmt.Sprintf(`{"file_path":%s}`, jsonString(path)),
		fmt.Sprintf(`{"content":%s}`, jsonString(body)))
}

// bashOf builds a Bash call running command whose stdout is out.
func bashOf(id, command, out string) Event {
	return toolUse(id, "Bash",
		fmt.Sprintf(`{"command":%s}`, jsonString(command)),
		fmt.Sprintf(`{"exit_code":0,"stdout":%s}`, jsonString(out)))
}

// jsonString renders s as a JSON string literal.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// drive feeds every event through OnToolUse, failing on the first error and asserting the
// PostToolUse silence rule as it goes.
func (h *harness) drive(events ...Event) {
	h.t.Helper()
	for _, e := range events {
		out, err := h.obs.OnToolUse(context.Background(), e)
		require.NoError(h.t, err)
		require.Equal(h.t, hookio.Empty(), out)
	}
}
