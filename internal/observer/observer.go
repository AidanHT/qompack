package observer

import (
	"context"
	"fmt"
	"sync"
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
	"github.com/qompack/qompack/internal/tokens"
)

// Event and Output alias the hookio types every Observer method is written in terms of
// (00-ARCHITECTURE.md §5.21). They are aliases, not new types, so the interface signatures below
// are literally unchanged: observer.Event and hookio.Event are the same type, and a value of
// either satisfies both.
//
// They exist for the same reason checkpoint.Invariant aliases pins.Invariant (§5.14) — to keep a
// package's own vocabulary usable from a package that may not import the definer. Concretely:
// internal/observer/observertest's import allow-set is its own package plus testutil and core
// (§3.2), so without these aliases the conformance suite could not construct so much as an empty
// hook payload, and the §5.21 interface would have no suite at all.
type (
	// Event is one Claude Code hook payload.
	Event = hookio.Event
	// Output is one hook subcommand's JSON response.
	Output = hookio.Output
)

// Observer is the L0 semantics seam (00-ARCHITECTURE.md §5.21). One method per hook the plugin
// registers; every one of them is on a latency budget, and every one of them must fail toward
// "record nothing, block nothing" rather than toward an error the host sees (§12.3).
type Observer interface {
	// OnToolUse handles PostToolUse: chunk and store the tool result, update the DAG, the
	// sketches and the action grammar, and detect redundancy. Qompack.md §8.1 budgets it at under
	// 15 ms p99, so an overrun degrades to queue-and-drain rather than blocking.
	OnToolUse(ctx context.Context, e Event) (Output, error)
	// OnUserPrompt handles UserPromptSubmit: capture the user's intent VERBATIM and immutably
	// (G2.3), and update the changepoint features.
	OnUserPrompt(ctx context.Context, e Event) (Output, error)
	// OnStop handles Stop and SubagentStop, distinguished by subagent: capture subagent detail
	// before the host double-compresses it (G10.1).
	OnStop(ctx context.Context, e Event, subagent bool) (Output, error)
	// OnSessionStart handles SessionStart. The source switch (startup|resume|compact|clear) lives
	// here and delegates: startup/resume load the store, compact rehydrates, clear resets.
	OnSessionStart(ctx context.Context, e Event) (Output, error)
	// OnSessionEnd handles SessionEnd: flush the store, write the session index, and run GC.
	OnSessionEnd(ctx context.Context, e Event) (Output, error)
}

// Mode is the degradation mode the contract monitor has this session in (§12.1). It reaches this
// package as a plain value through Options.Mode, because observer may not import contract (§3.2).
type Mode uint8

const (
	// ModeFull is normal operation.
	ModeFull Mode = iota
	// ModePassive is ModeDegradedPassive: L0 and L1 keep observing, chunking, storing, sketching
	// and building the DAG; everything that ACTS is off (resolved decision 10).
	ModePassive
)

// SymbolLister resolves the symbol names a tool result touched. It is a seam rather than a direct
// dependency because §3.2 never gave observer internal/symbols; SP-06's store holds the real
// extractor and the daemon wires an adapter.
type SymbolLister interface {
	// Names returns the symbol names defined in b, which is path's content.
	Names(path string, b []byte) []string
}

// Rehydrator is the seam SP-11 installs so that SessionStart's compact and clear branches are a
// delegation rather than a direct call into a wave-3 package.
type Rehydrator interface {
	// OnCompact handles SessionStart with source "compact".
	OnCompact(ctx context.Context, e Event) (Output, error)
	// OnClear handles SessionStart with source "clear".
	OnClear(ctx context.Context, e Event) (Output, error)
}

// FeatureSample is one delivery of the five cheap §6.6 features. The daemon maps it onto
// scheduler.Features and calls scheduler.Runtime.Observe, which is why it carries both the turn
// and the timestamp the sample was taken at.
type FeatureSample struct {
	// Turn is the turn the sample was taken at.
	Turn core.TurnIndex
	// TS is when it was taken.
	TS core.UnixMilli
	// PathJaccard is the Jaccard similarity of the recently-touched path sets of the two windows.
	PathJaccard float64
	// ToolShift is the total variation distance between the two windows' tool distributions.
	ToolShift float64
	// LexicalCohesion is the cosine similarity of the two windows' term-frequency vectors.
	LexicalCohesion float64
	// GapSeconds is the wall-clock gap since the previous observed event.
	GapSeconds float64
	// TodoTransition is 1 when the newest event completed a todo that was not already complete.
	TodoTransition float64
}

// Persister lets the daemon's idle loop checkpoint observer state. The value New returns ALWAYS
// satisfies it; observer_ops.go performs the single guarded assertion in the codebase
// (p, ok := obsv.(Persister)) and skips the idle registration when ok is false, so a future
// alternate Observer implementation cannot panic the daemon.
type Persister interface {
	// Persist writes the observer's per-session state and flushes the graph.
	Persist(ctx context.Context) error
}

// Options is the collaborator set New assembles an Observer from.
//
// 00-ARCHITECTURE.md §5.21 declares the Observer interface without a constructor, so Options is
// an SP-01 addition of the kind §14.0 of plans/V1-SP-01-foundation-toolchain-and-contracts.md
// describes: something §5 needs but does not spell out. SP-08 WIDENS it rather than renaming any
// field (Rule W-3).
//
// Grammar, Touch, Explore, Hot, Symbols, Rehydrate, Mode, OnSignals, OnFeatures and Metrics may
// each be nil; every call site guards (resolved decision 8).
type Options struct {
	// ProjectRoot is the project root the observer records under.
	ProjectRoot string
	// Cfg is the loaded configuration.
	Cfg config.Config
	// Store is the content-addressed store every observed byte is written through.
	Store store.Store
	// Graph is the dependence DAG nodes and edges are appended to.
	Graph dag.Graph
	// Grammar is the action-stream grammar tool names are folded into.
	Grammar grammar.Sequitur
	// Touch is §6.2's file-touch frequency sketch.
	Touch *sketch.CMS
	// Explore is §6.2's breadth-of-exploration cardinality sketch.
	Explore *sketch.HLL
	// Hot is the deterministic top-k `/qompack:status` reads.
	Hot *sketch.MisraGries
	// Tokens prices observed content.
	Tokens tokens.Estimator
	// Symbols resolves the symbol names a result touched.
	Symbols SymbolLister
	// Rehydrate is SP-11's SessionStart compact/clear seam.
	Rehydrate Rehydrator
	// Mode reports the current degradation mode; nil means ModeFull.
	Mode func() Mode
	// OnSignals delivers each event's task-boundary evidence to the scheduler.
	OnSignals func(core.SessionID, Signals)
	// OnFeatures delivers each BOCD feature sample to the scheduler.
	OnFeatures func(core.SessionID, FeatureSample)
	// Log is the logger; it may not be nil.
	Log logging.Logger
	// Metrics is the metrics registry; a nil Metrics must not panic a hook.
	Metrics obs.Registry
	// Clock is the only source of time in this package (§6.1): no observer code calls time.Now.
	Clock core.Clock
}

// The tuning constants of the L0 pipeline. Every one is a bound on work or memory rather than a
// configuration default, which is why none of them lives in internal/config (§11.6 D11).
const (
	// featureWindow is how many tool uses one BOCD comparison window holds (§6.6).
	featureWindow = 8
	// maxSymbolsPerResult bounds how many symbol nodes one result may mint.
	maxSymbolsPerResult = 64
	// symbolScanCap is the body size above which symbol extraction is skipped outright: the
	// observer is on budget B-C and an extractor walk is the only unbounded work in the pipeline.
	symbolScanCap = 256 << 10
	// supersessionLookback is how many prior tool uses per path supersede.go examines.
	supersessionLookback = 32
	// subagentWindowCap is how many tool uses are retained for SubagentStop linkage.
	subagentWindowCap = 256
	// thrashMinUses is the rule multiplicity above which the action grammar is thrashing.
	thrashMinUses = 3
	// cohesionTokenCap is how many tokens per window the lexical-cohesion feature scores.
	cohesionTokenCap = 4000
	// gcDeadline bounds the SessionEnd GC pass. The SessionEnd hook's own timeout is 20 s (§3.4).
	gcDeadline = 8 * time.Second
)

// The metric names this package registers on Options.Metrics.
const (
	histToolUse      = "observer.tooluse"
	histPrompt       = "observer.prompt"
	histStop         = "observer.stop"
	histSessionStart = "observer.session_start"
	histSessionEnd   = "observer.session_end"

	// counterSuperseded, counterNearDup and counterSubagentCapture are registered here — the
	// metric names belong to the package rather than to one commit — and are incremented by the
	// supersession, PostToolUse and SubagentStop paths.
	//
	// counterNearDup is bumped at the PUT (tooluse.go step 5a), NOT inside the supersession scan.
	// The store's near-duplicate signal exists for PATHLESS content — Bash and test-runner output,
	// which §8.1 item 1 names as the noisiest content class and the one where "the dedup ratio is
	// won or lost" — and supersession returns early on an empty Path, so counting it there made
	// this counter read ~0 for exactly the class it was meant to measure.
	counterSuperseded      = "observer.superseded"
	counterNearDup         = "observer.neardup"
	counterTombstone       = "observer.tombstone"
	counterSubagentCapture = "observer.subagent_capture"
	counterSignalTodo      = "observer.signal.todo"
	counterSignalTest      = "observer.signal.test"
	counterSignalGit       = "observer.signal.git"

	// counterErrPrefix is prepended to a stage name by soft.
	counterErrPrefix = "observer.err."
)

// The stage names soft reports under. They are the suffixes of observer.err.<stage>.
const (
	stagePut      = "put"
	stageIndex    = "index"
	stageFileVer  = "fileversion"
	stageDAG      = "dag"
	stageState    = "state"
	stageGraphOut = "flush"
)

// recentEvent is one entry of the BOCD feature window.
type recentEvent struct {
	Tool  string
	Paths []string
	Text  []byte
	TS    core.UnixMilli
}

// toolUseLite is the SubagentStop-linkage projection of a ToolUseRecord: everything a subagent
// capture needs to hand the parent a retrieval path into detail it never held (G10.1), and
// nothing else.
type toolUseLite struct {
	ID         core.ToolUseID
	Root       core.Hash
	Tool, Path string
	Bytes      int64
}

// sessionState is one session's in-memory state, mirrored to disk by state.go.
//
// Every field is read and written under mu (resolved decision 9). The three Seg* fields exist
// because dag.SegmentSpec needs PrevID, StartTurn and StartPos at CLOSE time and none of them is
// recoverable from store.Segment; LastToolUseID/LastToolUseTurn are carried as the raw pair rather
// than as a dag.NodeID because dag.BuildToolUse mints the NodeIDs itself and applies its
// PrevTurn < Turn guard to the turn — which is the whole defence against the parallel-sibling
// cycle of SP-07 D-7.
type sessionState struct {
	mu sync.Mutex

	Turn         core.TurnIndex
	PrefixTokens int

	Segment      core.SegmentID
	PrevSegment  core.SegmentID
	SegStartTurn core.TurnIndex
	SegStartPos  int

	// LastTS is the timestamp of the PREVIOUS event. It is assigned as the last bookkeeping
	// statement of each entry point, after features() has been consulted: assigning it earlier
	// would make GapSeconds identically zero and silently disable BOCD's time feature.
	LastTS core.UnixMilli

	LastToolUseID   core.ToolUseID
	LastToolUseTurn core.TurnIndex
	LastPromptTurn  core.TurnIndex

	// SubagentSince indexes ToolUses at the last SubagentStop or user prompt. Every front
	// eviction of the ring clamps it in the same statement.
	SubagentSince int

	Recent   []recentEvent
	ToolUses []toolUseLite

	TodoDone    map[string]bool
	WarnedRules map[grammar.RuleID]bool

	// TodoTransitioned records whether newlyCompletedTodos fired for the event currently being
	// processed. features() reads it for the §6.6 todo-transition feature; it is deliberately not
	// persisted, because it describes one event rather than the session.
	TodoTransitioned bool

	// PendingThrash is collected in OnToolUse and drained by OnUserPrompt.
	PendingThrash []grammar.Rule
}

// observer is the real L0 implementation.
type observer struct {
	opt Options

	// mu guards sess and the state-file write ONLY (resolved decision 9). It is never held while
	// a sessionState.mu is held.
	mu   sync.Mutex
	sess map[core.SessionID]*sessionState

	// sketchMu guards the three Options sketches. They are PER-PROJECT objects shared by every
	// session, so the per-session lock of decision 9 cannot serialize them, and internal/sketch's
	// own doc.go is explicit that "no mutable type here is safe for concurrent use: Bloom, CMS,
	// HLL, MisraGries and SigSketch all require external synchronisation". The daemon holds them
	// behind its session registry, but this package is handed the pointers directly and decision
	// 9 requires every entry point to be race-free under go test -race with two sessions in
	// flight — so the synchronisation for the sketch feed lives here. It is only ever taken
	// INSIDE a sessionState.mu and never around o.mu, so it introduces no new lock order.
	sketchMu sync.Mutex

	// once makes loadState run at most once per process.
	once sync.Once

	// maxResultBytes is runtime.hotPath.maxPayloadBytes, read once in New.
	maxResultBytes int

	// stateFile is <root>/.qompack/state/observer.json.
	stateFile string
}

// The value New returns satisfies both seams.
var (
	_ Observer  = (*observer)(nil)
	_ Persister = (*observer)(nil)
)

// New returns an Observer built from o.
//
// It reports an error only for a collaborator without which L0 cannot record anything at all:
// an empty ProjectRoot, or a nil Store, Graph, Log or Clock (resolved decision 8). Every other
// field is optional and every call site guards. The returned value ALWAYS satisfies Persister.
func New(o Options) (Observer, error) {
	switch {
	case o.ProjectRoot == "":
		return nil, fmt.Errorf("observer: New: ProjectRoot is required")
	case o.Store == nil:
		return nil, fmt.Errorf("observer: New: Store is required")
	case o.Graph == nil:
		return nil, fmt.Errorf("observer: New: Graph is required")
	case o.Log == nil:
		return nil, fmt.Errorf("observer: New: Log is required")
	case o.Clock == nil:
		return nil, fmt.Errorf("observer: New: Clock is required")
	}

	return &observer{
		opt:            o,
		sess:           make(map[core.SessionID]*sessionState),
		maxResultBytes: maxResultBytes(o.Cfg),
		stateFile:      stateFilePath(o.ProjectRoot),
	}, nil
}

// hotPathMaxPayloadKey is the dotted config path the hot-path payload cap is read from.
const hotPathMaxPayloadKey = "runtime.hotPath.maxPayloadBytes"

// maxResultBytes reads the configured hot-path payload cap, falling back to the DEFAULT rather
// than to a literal when the key is absent, not an int, or non-positive — which is what keeps
// §11.6's no-hardcoding rule satisfied here without a //nomagic:allow.
func maxResultBytes(cfg config.Config) int {
	if v, ok := cfg.Get(hotPathMaxPayloadKey); ok {
		if n, isInt := v.(int); isInt && n > 0 {
			return n
		}
	}
	return config.Defaults().Runtime.HotPath.MaxPayloadBytes
}

// mode reports the current degradation mode. A nil Options.Mode means ModeFull.
func (o *observer) mode() Mode {
	if o.opt.Mode == nil {
		return ModeFull
	}
	return o.opt.Mode()
}

// now is the ONLY Clock call site in this package (resolved decision 11).
func (o *observer) now() core.UnixMilli {
	return core.UnixMilli(o.opt.Clock.Now().UnixMilli())
}

// soft absorbs one stage failure: no I/O failure ever escapes an Observer method (decision 7).
func (o *observer) soft(stage string, err error) {
	if err == nil {
		return
	}
	o.count(counterErrPrefix + stage)
	o.opt.Log.Warn("observer: stage failed", "stage", stage, "err", err)
}

// count bumps a counter. It is nil-safe on Options.Metrics and is the only place in this package
// that names an obs COUNTER method, so adapting to a different spelling is a one-line change.
func (o *observer) count(name string) {
	if o.opt.Metrics == nil {
		return
	}
	o.opt.Metrics.Counter(name).Add(1)
}

// timed runs f under the named histogram. It is the only place in this package that names an obs
// HISTOGRAM method, for the same reason count is the only one that names a counter method.
func (o *observer) timed(name string, f func() error) error {
	if o.opt.Metrics == nil {
		return f()
	}
	return obs.Timed(o.opt.Metrics.Hist(name), f)
}

// session returns s's state, creating it on first use. It is the only accessor of the session
// map: it takes o.mu for the lookup/insert and releases it before returning, so a caller then
// takes the returned state's OWN mutex with o.mu already free (resolved decision 9).
func (o *observer) session(s core.SessionID) *sessionState {
	o.once.Do(o.loadState)

	o.mu.Lock()
	defer o.mu.Unlock()
	if st, ok := o.sess[s]; ok {
		return st
	}
	st := &sessionState{
		TodoDone:    make(map[string]bool),
		WarnedRules: make(map[grammar.RuleID]bool),
	}
	o.sess[s] = st
	return st
}

// OnSessionStart and OnSessionEnd live in session.go.
