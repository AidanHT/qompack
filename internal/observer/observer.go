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

// ErrUnpublished means tool capture failed before a required content or reference write
// completed. The daemon must keep the delivery retryable. Host output remains empty; this
// error describes recording availability and does not deny the host's original tool result.
var ErrUnpublished = fmt.Errorf("observer: tool capture unpublished: %w", core.ErrDegraded)

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

// Progress is where one session stands, as the observer holds it in memory: the turn its next
// event will be filed at, the segment its events are being enrolled into, and what that segment
// has accumulated so far.
type Progress struct {
	Turn          core.TurnIndex
	Segment       core.SegmentID
	SegmentTokens core.Tokens
}

// ProgressReporter is the read-only view of a session's in-memory position. The value New returns
// ALWAYS satisfies it; like Persister it is a separate interface, so the §5.21 Observer interface is
// unchanged and a caller asserts for it.
//
// It exists for the two answers only this state can give (V6 live lane, F-UAT01-2 and retrieval
// D8). The daemon's MCP handlers file a record for every retrieval they answer and must place it at
// the session's CURRENT turn, and `timeline` must report an open segment's live end: the store's
// segment log learns a segment's end turn and tokens only when the segment closes, and the tool_use
// index lags this state by whatever the workers have not yet published.
type ProgressReporter interface {
	// Progress reports s's position, or false when this observer holds no state for s.
	Progress(s core.SessionID) (Progress, bool)
}

// SpooledReplyRearmer is how the daemon reports that a prompt's reply reached no hook although the
// observer handed it a warning: the hook gave up waiting and spooled the prompt, and the drain is now
// settling that spooled copy. The value New returns ALWAYS satisfies it; like ProgressReporter it is a
// separate interface, so the §5.21 Observer interface is unchanged and a caller asserts for it.
type SpooledReplyRearmer interface {
	// PromptReplySpooled re-arms the warning the reply to the prompt with this nonce carried, as a
	// refused claim does (rearmUndelivered), and reports whether there was one. A nonce whose reply
	// carried no warning, or that this observer does not remember, changes nothing.
	PromptReplySpooled(s core.SessionID, nonce string) bool
}

// Options is the collaborator set New assembles an Observer from.
//
// 00-ARCHITECTURE.md §5.21 declares the Observer interface without a constructor, so Options is
// an SP-01 addition of the kind §14.0 of plans/V1-SP-01-foundation-toolchain-and-contracts.md
// describes: something §5 needs but does not spell out. SP-08 WIDENS it rather than renaming any
// field (Rule W-3).
//
// Grammar, Touch, Explore, Hot, Symbols, Rehydrate, Mode, OnSignals, OnFeatures, OnPromptCaptured
// and Metrics may each be nil; every call site guards (resolved decision 8).
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
	// OnPromptCaptured is called once per prompt this observer has just captured durably — never
	// for the reply-only path, a recognized redelivery or a capture that failed — after the
	// session lock is released. The daemon feeds it to negknow's user-statement ingest. It has no
	// return value: nothing it does may fail or delay the capture it reports.
	OnPromptCaptured func(context.Context, PromptCapture)
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

	// counterSegmentFollowed counts the segment rolls the observer did not make and caught up
	// with (session.go followSegmentRoll): one per catch-up, however many segments it covered.
	counterSegmentFollowed = "observer.segment.followed"

	// counterErrPrefix is prepended to a stage name by soft.
	counterErrPrefix = "observer.err."
)

// The stage names soft reports under. They are the suffixes of observer.err.<stage>.
const (
	stagePut      = "put"
	stageIndex    = "index"
	stageLink     = "link"
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

	// LastStopObs is the observation identity of the last leased main-agent Stop this session
	// applied, which is how a replay of it is recognized (stop.go mainAgentStop). It is persisted
	// with Turn, whose increment it describes.
	LastStopObs core.ObservationID

	// SubagentSince indexes ToolUses at the last SubagentStop or user prompt. Every front
	// eviction of the ring clamps it in the same statement.
	SubagentSince int
	// PromptSince indexes ToolUses at the last captured user prompt, so a prompt's LastEditPath
	// looks only at the turn the user is reacting to. SubagentStop moves SubagentSince but not
	// this. It is not persisted: a reloaded session starts it at the end of the window, which
	// errs towards no target (an unresolved statement) rather than a stale one.
	PromptSince int

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
	// WarningTurn is fixed when the queue becomes nonempty, before worker/reply scheduling.
	WarningTurn core.TurnIndex
	// ThrashFloor holds each rule whose warning a reply drained but could not deliver, at the
	// reference count it had then (rearmUndelivered). collectThrash queues such a rule again only
	// once Sequitur reports it referenced more often. Not persisted, like WarnedRules.
	ThrashFloor map[grammar.RuleID]int
	// ReplyWarning is the latest warning a claimed reply carried, keyed by that prompt's nonce, so a
	// spooled copy of the prompt can re-arm it (PromptReplySpooled). One per session, in memory only:
	// not persisted, like WarnedRules.
	ReplyWarning replyWarning
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

	// idx is opt.Store's store.SupersedingRecorder capability, or nil for a Store without it.
	//
	// The assertion is made ONCE, in New, and the nil case is loud rather than silent, because the
	// path a nil idx falls back to is defect-bearing by design: RecordToolUse followed by one
	// MarkSuperseded per mark is the 1+N write shape carried defect SP08-D2's second mechanism
	// lives in. A handler cancelled between the record and its marks leaves a record whose marks
	// never landed, and the at-least-once redelivery that follows appends those marks alone, behind
	// a record line that predates the flush.
	//
	// Production always has the capability — store.Open returns *FSStore and no production type
	// wraps store.Store — and internal/daemon's TestWireObserverStoreSupportsSupersedingRecorder
	// pins that for the real composition.
	//
	// What lands on the legacy path is not only this package's fakes, and it is worth being exact
	// about, because the honest version is a broader claim rather than a narrower one.
	// store.SupersedingRecorder is deliberately outside §5.8's frozen store.Store interface, which
	// declares RecordToolUse and MarkSuperseded only — so a type that embeds the store.Store
	// INTERFACE cannot promote RecordToolUseSuperseding and fails this assertion however real its
	// backing store is. Every store wrapper in the tree is written that way (internal/daemon's
	// publicationFaultStore and gatedToolStore, test/e2e's x4RecordingStore, this package's own
	// fakes), and WireObserver opens a store only when Options.Store is nil, so a wrapper a caller
	// presets reaches this assertion unchanged. A wrapper that means to keep the atomic path has to
	// forward the method explicitly.
	//
	// That is exactly why a production composition which silently joined them would be invisible:
	// every unit test would stay green and only the e2e x09 flush arm would go red. The Loud and the
	// counter New writes are what make that arrival observable instead.
	idx store.SupersedingRecorder

	// stateFile is <root>/.qompack/state/observer.json.
	stateFile string
}

// The value New returns satisfies both seams.
var (
	_ Observer            = (*observer)(nil)
	_ Persister           = (*observer)(nil)
	_ ProgressReporter    = (*observer)(nil)
	_ SpooledReplyRearmer = (*observer)(nil)
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

	ob := &observer{
		opt:            o,
		sess:           make(map[core.SessionID]*sessionState),
		maxResultBytes: maxResultBytes(o.Cfg),
		stateFile:      stateFilePath(o.ProjectRoot),
	}
	// The capability is resolved here, not per event: the answer cannot change for a given Store,
	// and asserting on the hot path would put a type switch inside budget B-C for no benefit. The
	// absence is ACTED on rather than merely consumed — see the idx field.
	if idx, ok := o.Store.(store.SupersedingRecorder); ok {
		ob.idx = idx
	} else {
		ob.opt.Log.Loud("observer: store cannot append a tool_use record and its supersede marks as "+
			"one write; using the separate-writes path, which is carried defect SP08-D2's second "+
			"mechanism", "store", fmt.Sprintf("%T", o.Store))
		ob.count(counterLegacySupersede)
	}
	return ob, nil
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

// soft records a secondary stage failure. Required tool content/reference writes use unpublished
// instead so their failure cannot become a successful drain acknowledgement.
func (o *observer) soft(stage string, err error) {
	if err == nil {
		return
	}
	o.count(counterErrPrefix + stage)
	o.opt.Log.Warn("observer: stage failed", "stage", stage, "err", err)
}

// unpublished preserves the stage without logging an error that may contain private payloads.
func (o *observer) unpublished(stage string) error {
	o.count(counterErrPrefix + stage)
	o.opt.Log.Warn("observer: tool capture unpublished", "stage", stage)
	return ErrUnpublished
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

// Progress reports s's in-memory position without creating state for a session it has never seen.
//
// It takes the session's own lock, briefly, the same one every hook for that session serializes on,
// so the turn it reports is one an event for the session could actually have been filed at. It never
// holds o.mu while waiting for it (decision 9's lock order).
func (o *observer) Progress(s core.SessionID) (Progress, bool) {
	o.once.Do(o.loadState)

	o.mu.Lock()
	st, ok := o.sess[s]
	o.mu.Unlock()
	if !ok {
		return Progress{}, false
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	return Progress{Turn: st.Turn, Segment: st.Segment, SegmentTokens: core.Tokens(segmentTokens(st))}, true
}

// OnSessionStart and OnSessionEnd live in session.go.

// ---------------------------------------------------------------------------
// Observation identity (T20-M1-02)
//
// Event is an alias for hookio.Event, whose fields are the host's payload; the daemon-assigned
// observation identity is not the host's and must not be smuggled into it. It travels on the
// context instead, from the ingest worker (or the drain) that took the lease down to the publishing
// stage here, which is the same channel the daemon already uses for the Services/Registry values
// every route reads.
type observationKey struct{}

// WithObservation attaches the durable identity the daemon assigned to this delivery.
func WithObservation(ctx context.Context, id core.ObservationID) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, observationKey{}, id)
}

// ObservationFrom returns the identity WithObservation attached, or "" when this delivery has none
// — an unleased delivery, or an in-process caller with no daemon behind it. Empty is a gap and is
// treated as one: nothing claims a durable identity it was not given.
func ObservationFrom(ctx context.Context) core.ObservationID {
	id, _ := ctx.Value(observationKey{}).(core.ObservationID)
	return id
}
