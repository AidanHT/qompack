package negknow

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// This file is the elimination ledger itself (00-ARCHITECTURE.md §5.10, Qompack.md §8.3).
//
// One rule governs everything below, and it is §13 invariant 3: the bloom filter is a CACHE over
// records/eliminations.jsonl, never the source of truth. Every membership answer Query returns is
// either backed by a record lookup or explicitly flagged BloomOnly, and there is no path here that
// answers "already tried" from the filter alone. That is what keeps §8.3's "false positives are
// the safe direction" true, and §12 rates the failure it prevents — a stale elimination blocking
// a now-viable approach — as the High-severity risk of the whole feature.

// Deps is the set of collaborators Open assembles a Ledger from (00-ARCHITECTURE.md §14.0 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md: §5.10's Open references this type without
// defining it; SP-01 defines it here).
type Deps struct {
	// Store is the file-version history staleness is measured against. A nil Store means
	// RefreshStaleness and dependency resolution are skipped, not that they fail.
	Store store.Store
	// Graph receives one KindElimination node per record. A nil Graph means no DAG emission.
	Graph dag.Graph
	// Session is the session every record this ledger mints belongs to, and the session
	// session-scoped records are visible to.
	Session core.SessionID
	// Redact, when non-nil, is applied to a record's free-text fields before ANYTHING is derived
	// from them — before the descriptor, before the bloom keys, before the appended line. It is
	// supplied by the composition root as a function value rather than as an interface, so this
	// package needs no import edge to whatever implements it.
	//
	// It MUST be idempotent on its own output. Record redacts once at entry and normalizeRecord
	// redacts again after truncation, and Query redacts the text it canonicalizes, so a redactor
	// that rewrote its own placeholders would produce a different key on each pass and Record and
	// Query would never meet. Replacing a match with a fixed placeholder that is not itself a
	// match satisfies this.
	Redact func([]byte) []byte

	Log     logging.Logger
	Metrics obs.Registry
	Clock   core.Clock
}

// Health summarizes the ledger's current size and the tried.bloom filter's saturation
// (00-ARCHITECTURE.md §5.10): what `/qompack:status` reads to decide whether a rebuild-with-resize
// is due (§11.4, §12 "Bloom saturation").
type Health struct {
	Records, Active, Stale int
	FillRatio, EstFPRate   float64
	NeedsResize            bool
}

// Ledger is the negative-knowledge elimination store's full seam (00-ARCHITECTURE.md §5.10):
// recording eliminations, answering the three-way already_tried question, tracking staleness
// against the store's current file versions, and rebuilding tried.bloom from active records only.
type Ledger interface {
	// Record appends r to records/eliminations.jsonl and updates tried.bloom, returning r's
	// assigned ID.
	Record(ctx context.Context, r Record) (string, error)
	// Query answers the three-way already_tried question for target/approach at scope.
	Query(ctx context.Context, target, approach string, scope Scope) (Answer, error)
	// Get looks up a Record by ID.
	Get(ctx context.Context, id string) (Record, error)
	// Active returns every StatusActive Record visible at scope.
	Active(ctx context.Context, scope Scope) ([]Record, error)
	// All returns every Record ever appended, regardless of Status.
	All(ctx context.Context) ([]Record, error)
	// MarkStale flips every Record in ids to StatusStale, recording because as StaleBecause.
	MarkStale(ctx context.Context, ids []string, because []string) error
	// RefreshStaleness compares every active record's depends_on hashes against s's current file
	// versions and flips changed ones to stale. It returns the flipped ids.
	RefreshStaleness(ctx context.Context, s store.Store) ([]string, error)
	// RebuildBloom rebuilds tried.bloom from ACTIVE RECORDS ONLY — never from a checkpoint, never
	// from context (00-ARCHITECTURE.md §3.3, §13 invariant 2). It resizes if
	// sketch.Bloom.ResizeTarget says so.
	RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error)
	// Health reports the ledger's current size and bloom saturation.
	Health() Health
	// Close releases every resource this Ledger holds.
	Close() error
}

// Maintainer is the ledger surface beyond §5.10's Ledger interface: the maintenance, ranking and
// ingest entry points later subplans reach through a type assertion on the value Open returns
// (`m, ok := led.(negknow.Maintainer)`). Open returns a Ledger rather than a concrete type, and
// the concrete type is unexported, so this interface is how SP-11 through SP-14 name what they
// need without this package exporting its implementation.
type Maintainer interface {
	// NeedsRebuild reports whether a tried.bloom rebuild is owed.
	NeedsRebuild() bool
	// MaintenanceTask returns the (name, priority, fn) triple SP-12's idle controller registers.
	MaintenanceTask(s store.Store) (name string, prio int, fn func(ctx context.Context) error)
	// TopActive returns the n highest-scoring active records visible at scope, plus how many
	// visible active records were left out — what SP-11 renders as "and N more".
	TopActive(ctx context.Context, scope Scope, n int, score map[string]float64) ([]Record, int, error)
	// Observe feeds SP-08's observer signals to the heuristic detector.
	Observe(ctx context.Context, o Observation) error
	// IngestMCP records an elimination reported through the record_eliminated MCP tool (SP-13).
	IngestMCP(ctx context.Context, a MCPArgs) (Record, []string, error)
	// IngestPin records an elimination reported through /qompack:pin --eliminated (SP-14).
	IngestPin(ctx context.Context, a PinArgs) (Record, []string, error)
	// IngestUserStatement records eliminations stated verbatim in a user prompt (SP-08).
	IngestUserStatement(ctx context.Context, p UserStatement) ([]Record, error)
}

// MCPArgs is one record_eliminated MCP tool call (SP-13).
type MCPArgs struct {
	Target, Approach, Reason string
	// Scope is "session" or "project"; "" means the configured eliminations.defaultScope.
	Scope string
	// DependsOn holds project-relative paths, resolved to []core.Dep from the store's file
	// history at ingest time.
	DependsOn []string
	// Evidence is the elimination's evidence root. The zero Hash means the Reason text is stored
	// through store.PutBytes to mint one.
	Evidence core.Hash
}

// PinArgs is one `/qompack:pin --eliminated` invocation (SP-14). It differs from MCPArgs only in
// how evidence arrives: a slash command carries text, so Evidence is the "sha256:…" spelling
// core.ParseHash reads, and "" means none was supplied.
type PinArgs struct {
	Target, Approach, Reason string
	Scope                    string
	DependsOn                []string
	Evidence                 string
}

// UserStatement is one user prompt that stated an elimination outright — negative-knowledge
// source #4 (§8.3).
type UserStatement struct {
	Prompt string
	Turn   core.TurnIndex
	// Path is the last-edited path in paths.Key form, or "" when it is not known.
	Path   string
	Symbol string
	// Approach is the approach text taken from the preceding assistant turn.
	Approach string
	// PromptRoot is the evidence: the stored, verbatim prompt.
	PromptRoot core.Hash
}

// ObsKind classifies one observer signal the heuristic detector reads (§8.3 source #3).
type ObsKind uint8

const (
	// ObsTestFail is a test run that failed.
	ObsTestFail ObsKind = iota
	// ObsTestPass is a test run that passed.
	ObsTestPass
	// ObsEdit is an edit to a file.
	ObsEdit
	// ObsRevert is an edit that undid a previous one.
	ObsRevert
)

// Observation is one signal SP-08's observer feeds the ledger, from which the heuristic detector
// infers the test-fail -> revert -> different-approach pattern.
type Observation struct {
	Turn    core.TurnIndex
	Kind    ObsKind
	Path    string
	Symbol  string
	Detail  string
	ToolUse core.ToolUseID
	Root    core.Hash
	TS      core.UnixMilli
}

// ObservationSource is the read side of the ledger's observation ring: what the Detector reads
// when it scans for the heuristic pattern.
type ObservationSource interface {
	Since(turn core.TurnIndex) []Observation
}

// StaleNote is §8.3 item 4's re-verification sentence, verbatim and exported so that exactly one
// copy of it exists in the system. SP-11's digest and SP-13's already_tried result both render
// this string; a paraphrase in either would be a second, divergent contract.
const StaleNote = "previously eliminated, but the evidence has changed since — re-verification may be warranted"

var (
	// ErrNoEvidence is what Record reports when eliminations.requireEvidence is set and the
	// caller supplied no evidence hash. Nothing is appended.
	ErrNoEvidence = errors.New("qompack: elimination requires evidence (eliminations.requireEvidence)")
	// ErrBlind is what RebuildBloom reports when the record log could not be read at Open.
	// Rebuilding from records we cannot read would replace a good on-disk cache with an empty one
	// on the strength of a transient I/O error, so the rebuild is refused instead.
	ErrBlind = errors.New("qompack: elimination ledger is in blind mode")
)

// openRefreshDeadline bounds the one staleness refresh Open performs. It is a ceiling on how long
// constructing a ledger may take, not a budget for the refresh itself: on expiry the ledger marks
// a rebuild owed and the daemon's idle task finishes the job (SP-12).
const openRefreshDeadline = 250 * time.Millisecond

// signalRing is the number of observations the ledger keeps for the heuristic detector to scan.
// It is a ring rather than a slice so that a long session's signal history stays bounded.
const signalRing = 512

// The Appendix C spellings of the eliminations block's enumerated values. They are constants
// because a typo in one of them would silently select a different branch: "nextidle" is not
// "nextIdle", and the difference is whether tried.bloom is ever rebuilt.
const (
	rebuildNextIdle  = "nextIdle"
	rebuildImmediate = "immediate"
	rebuildNever     = "never"

	staleResponseFlag = "flag"
	staleResponseDrop = "drop"
)

// The instrument names this package mints. They are strings, so nothing catches a typo at compile
// time and a second spelling produces a second, silently empty instrument; the subplan's list is
// the single authority and these constants are this package's single transcription of it.
const (
	counterRecordsAppended    = "negknow.records.appended"
	counterRecordsDeduped     = "negknow.records.deduped"
	counterRejectedNoEvidence = "negknow.records.rejected_no_evidence"
	counterQueryPrefix        = "negknow.query."
	counterStaleFlipped       = "negknow.stale.flipped"
	counterStaleSkipped       = "negknow.stale.skipped"
	counterBloomRebuilds      = "negknow.bloom.rebuilds"
	counterBlindMode          = "negknow.bloom.blind_mode"
	counterCorruptOnLoad      = "negknow.bloom.corrupt_on_load"
)

// The per-query counter suffixes. The three answer states are spelled as their own names, per the
// subplan's "negknow.query.<state>, suffixed by the state's own name".
const (
	queryStateAbsent    = "absent"
	queryStateActive    = "active"
	queryStateStale     = "stale"
	queryStateBloomOnly = "bloom_only"
)

// The latency histograms.
const (
	histRecord  = "negknow.record"
	histQuery   = "negknow.query"
	histRefresh = "negknow.refresh"
	histRebuild = "negknow.rebuild"
)

// maintenanceTaskName and maintenancePriority are the idle-task identity SP-12 registers. The
// priority places the ledger's maintenance after frontier advancement and GC in SP-12's ordering;
// SP-12 may register it with a different one.
const (
	maintenanceTaskName = "negknow.maintain"
	maintenancePriority = 30
)

// ledger is the real elimination ledger. Every mutable field is behind mu: readers take RLock,
// appenders Lock, and -race is what proves it.
type ledger struct {
	mu   sync.RWMutex
	root string
	cfg  config.Config
	// elim is cfg.Eliminations after every out-of-domain value has been replaced by its
	// Appendix C default, so the rest of the file never has to re-validate.
	elim  config.EliminationsCfg
	bloom *sketch.Bloom
	// blind reports that records/eliminations.jsonl could not be read at Open. Under blind, Query
	// answers AnswerAbsent for everything: a membership answer with no record behind it is the
	// one thing §12.3 forbids here.
	blind bool
	recs  []Record
	byID  map[string]int
	// byMatch maps MatchHex to the record indices carrying it, in insertion order. This is the
	// index Query reads, which is why the query path performs no scan.
	byMatch map[string][]int
	// byKey maps dedupHex(session, scope, Desc.Key()) to the most recently appended record with
	// that identity — the idempotence index. Status is read from recs, so MarkStale never touches
	// this map.
	byKey map[string]int
	// ring is the bounded history of the last signalRing observations, for the heuristic detector.
	ring []Observation
	lay  paths.Layout
	// f is the append handle on records/eliminations.jsonl, or nil when the log could not be
	// opened for appending. Every append goes through it under mu.
	f      io.WriteCloser
	closed bool
	// pending reports that a bloom rebuild is owed (rebuildOnStale == "nextIdle").
	pending bool
	// seq is the rebuild generation. It is seeded at Open from paths.HighestBloomBackupSeq so it
	// resumes above every surviving tried.bloom.<n>.bak; nothing else on disk records it, and a
	// private counter file that could disagree with the backup names would be a second source of
	// truth for a number the filesystem already carries.
	seq  int
	deps Deps
	log  logging.Logger
	m    obs.Registry
	clk  core.Clock
}

// The compile-time assertion that keeps Open's return type and this implementation in sync. The
// Maintainer assertion arrives with the ingest and observation methods that complete it.
var _ Ledger = (*ledger)(nil)

// Open returns the elimination ledger rooted at root.
//
// Constructing ALWAYS succeeds. SP-05's daemon holds a negknow.Ledger from process start, and a
// hook that dies takes observability with it (§12.3), so every failure below degrades into a
// usable ledger with a loud line rather than into an error: unreadable records become blind mode,
// an unreadable or corrupt filter becomes a rebuild, and out-of-domain configuration becomes the
// Appendix C default. The error return is part of §5.10's declared signature and is retained for
// callers already written against it; this implementation never populates it.
//
// b is the filter to adopt — the daemon has usually loaded it already. A nil b makes Open load
// sketches/tried.bloom itself, through sketch.LoadWithLog and never sketch.Load: Load runs on a
// logging.Nop() and writes no line anywhere, which would make a corrupt filter indistinguishable
// from a first session and violate §13 invariant 10.
func Open(root string, cfg config.Config, b *sketch.Bloom, deps Deps) (Ledger, error) {
	if deps.Clock == nil {
		deps.Clock = core.SystemClock()
	}
	if deps.Log == nil {
		deps.Log = logging.Nop()
	}
	if deps.Metrics == nil {
		// internal/obs ships no no-op constructor and Rule W-3 forbids adding one, so the ledger
		// carries its own.
		deps.Metrics = nopRegistry{}
	}

	l := &ledger{
		root:    root,
		cfg:     cfg,
		byID:    make(map[string]int),
		byMatch: make(map[string][]int),
		byKey:   make(map[string]int),
		lay:     paths.Of(root),
		deps:    deps,
		log:     deps.Log,
		m:       deps.Metrics,
		clk:     deps.Clock,
	}
	l.elim = normalizeEliminations(cfg.Eliminations, l.log)

	// EnsureLayout is what every composition root runs, and this one needs it before either of
	// the next two steps: paths.AppendOnly does not create parent directories, and
	// sketch.ReplaceGenerational needs sketches/ to exist.
	if err := paths.EnsureLayout(l.lay); err != nil {
		l.log.Loud("negknow: could not create the .qompack layout", "root", root, "err", err)
	}

	// Seed the rebuild counter from the disk itself. A missing sketches/ is "no backups" and not
	// an error; a genuine read error leaves the counter at zero, which ReplaceGenerational's
	// pre-flight will refuse rather than silently mis-sequence.
	switch seq, ok, err := paths.HighestBloomBackupSeq(l.lay); {
	case err != nil:
		l.log.Debug("negknow: could not read the surviving tried.bloom backups", "err", err)
	case ok:
		l.seq = seq
	}

	l.loadRecords()
	loadFailed := l.acquireBloom(b)
	l.reconcileBloom(loadFailed)
	l.refreshAtOpen()
	return l, nil
}

// normalizeEliminations replaces every out-of-domain eliminations value with its Appendix C
// default and reports the substitution through Loud — never a crash (00-ARCHITECTURE.md §11.3).
//
// An EMPTY value is not a violation. config.Load always fills these keys in, so "" only reaches
// here from a caller holding a zero config.Config — a composition root that has not loaded
// configuration yet — and shouting three times at such a caller would train an operator to ignore
// the channel real corruption uses. RequireEvidence is a bool and has no out-of-domain value.
func normalizeEliminations(in config.EliminationsCfg, log logging.Logger) config.EliminationsCfg {
	out := in
	out.DefaultScope = pickEnum(out.DefaultScope, string(ScopeSession),
		"eliminations.defaultScope", log, string(ScopeSession), string(ScopeProject))
	out.RebuildOnStale = pickEnum(out.RebuildOnStale, rebuildNextIdle,
		"eliminations.rebuildOnStale", log, rebuildNextIdle, rebuildImmediate, rebuildNever)
	out.StaleResponse = pickEnum(out.StaleResponse, staleResponseFlag,
		"eliminations.staleResponse", log, staleResponseFlag, staleResponseDrop)
	return out
}

// pickEnum returns got when it is one of allowed, fallback when got is empty, and fallback with
// one Loud line when got is a value outside the domain.
func pickEnum(got, fallback, key string, log logging.Logger, allowed ...string) string {
	if got == "" {
		return fallback
	}
	for _, a := range allowed {
		if got == a {
			return got
		}
	}
	log.Loud("negknow: configuration value is outside its documented domain; using the default",
		"key", key, "got", got, "using", fallback)
	return fallback
}

// loadRecords opens the append handle and materializes the log behind it.
//
// The append handle is taken FIRST, because opening it with O_CREATE is also what makes a first
// session's read succeed on an empty file rather than report the file missing. A log that cannot
// be appended to is a Warn and a nil handle — this session records nothing, but everything already
// on disk still answers. A log that cannot be READ is blind mode, which is the loud one.
func (l *ledger) loadRecords() {
	p := logPath(l.root)

	w, err := openLog(l.root)
	if err != nil {
		l.log.Warn("negknow: the elimination log cannot be appended to; nothing recorded this session",
			"path", p, "err", err)
	} else {
		l.f = w
	}

	b, err := paths.ReadFileShared(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// A project whose first elimination has not been recorded. Nothing is wrong.
		return
	case err != nil:
		l.goBlind(err)
		return
	}

	recs, byID, _, err := replayLog(bytes.NewReader(b), l.log, l.m)
	if err != nil {
		// replayLog degrades every per-line problem and returns an error only when the READER
		// failed, i.e. when the materialization is partial. Reporting a partial one as complete
		// would silently un-eliminate whatever came after the failure.
		l.goBlind(err)
		return
	}
	l.recs, l.byID = recs, byID
	l.reindex()
}

// goBlind enters blind mode: the ledger stays usable, Query answers absent for everything, and the
// degradation is loud exactly once (§12.3, §13 invariant 10).
func (l *ledger) goBlind(err error) {
	l.blind = true
	l.recs, l.byID = nil, make(map[string]int)
	l.byMatch, l.byKey = make(map[string][]int), make(map[string]int)
	l.m.Counter(counterBlindMode).Add(1)
	l.log.Loud("negknow: elimination records unreadable; already_tried will answer absent for everything",
		"path", logPath(l.root), "err", err)
}

// reindex rebuilds byMatch and byKey from recs. byID is replayLog's, since the replay is what
// decides which of two lines sharing an id survives.
func (l *ledger) reindex() {
	l.byMatch = make(map[string][]int, len(l.recs))
	l.byKey = make(map[string]int, len(l.recs))
	for i, r := range l.recs {
		mh := r.Desc.MatchHex()
		l.byMatch[mh] = append(l.byMatch[mh], i)
		// The LAST line with an identity wins: byKey is the idempotence index, and what a caller
		// re-recording an identity should collapse onto is the most recent one.
		l.byKey[dedupHex(r)] = i
	}
}

// acquireBloom adopts b, or loads sketches/tried.bloom when b is nil. It reports whether the load
// failed, which is reconcileBloom's "the filter is missing every key" case.
func (l *ledger) acquireBloom(b *sketch.Bloom) (loadFailed bool) {
	if b != nil {
		if m, _ := b.Bits(); m == 0 {
			// An UNSIZED filter — sketch.Bloom's zero value, which a caller can spell as
			// &sketch.Bloom{}. It holds no bits at all, so Test answers false for everything and
			// Add records nothing: adopting it would silently switch already_tried off, with no
			// symptom anywhere. It is treated exactly as a failed load, and reconcileBloom then
			// rebuilds from the records, which is the only place a correct filter comes from.
			l.log.Debug("negknow: the bloom passed to Open is unsized; rebuilding from the records")
			l.bloom = l.newConfiguredBloom()
			return true
		}
		l.bloom = b
		return false
	}

	nb := l.newConfiguredBloom()
	err := sketch.LoadWithLog(filepath.Join(l.lay.Sketches, sketch.TriedBloomBase), nb, l.log)
	l.bloom = nb
	if err == nil {
		return false
	}
	// core.ErrNotFound covers missing AND CRC-failed (§5.7); both mean "start from the records",
	// which reconcileBloom does next. The two are told apart for the log and the counter only:
	// bit rot must be distinguishable from an ordinary first session. LoadWithLog has already
	// written the Loud line for the corrupt case, so this branch writes no second one.
	if errors.Is(err, sketch.ErrCorrupt) {
		l.m.Counter(counterCorruptOnLoad).Add(1)
	}
	return true
}

// newConfiguredBloom builds an empty filter at the configured capacity and false-positive rate.
//
// sketch's constructors clamp rather than reject, so reading the clamped pair back IS the report
// of an out-of-range configuration — and it is how that is checked without spelling an Appendix C
// number here (nomagic, §11.6).
//
// The ZERO pair is exempt, for the reason pickEnum exempts "": config.Load always fills these keys
// in, so (0, 0) reaches here only from a caller holding a zero config.Config — a composition root
// that has not loaded configuration yet — and shouting at it would train an operator to ignore the
// channel real corruption uses. A non-zero pair outside its range is a genuine mistake, and loud.
func (l *ledger) newConfiguredBloom() *sketch.Bloom {
	capacity, fpRate := l.cfg.Sketches.Bloom.Capacity, l.cfg.Sketches.Bloom.FPRate
	nb := sketch.NewBloom(capacity, fpRate)
	if capacity == 0 && fpRate == 0 {
		return nb
	}
	if n, p := nb.Capacity(); n != capacity || p != fpRate {
		l.log.Loud("negknow: sketches.bloom values are outside their documented range and were clamped",
			"capacity", capacity, "fpRate", fpRate, "usingCapacity", n, "usingFPRate", p)
	}
	return nb
}

// reconcileBloom is §13 invariant 3 made operational: the filter must hold at least the keys the
// records imply, or the feature silently stops working.
//
// It is skipped entirely under blind mode. The records that would feed a rebuild are unreadable,
// so rebuilding would replace a good on-disk cache with an empty one on the strength of a
// transient I/O error.
func (l *ledger) reconcileBloom(loadFailed bool) {
	if l.blind {
		return
	}
	want := 2 * len(l.visibleActive())

	if loadFailed || l.bloom.Count() < want {
		// Missing keys are false NEGATIVES, which defeat the feature without any symptom. This is
		// the common case rather than an exception: Record adds keys to the in-memory filter only,
		// and §3.3 lets nothing but a rebuild replace tried.bloom, so the on-disk filter always
		// lags the log by whatever has been recorded since the last rebuild.
		if _, _, err := l.rebuildLocked(context.Background()); err != nil {
			// The rebuilt filter is correct even when only its persistence failed. rebuildLocked
			// has already adopted it and left pending set, so the write is retried at idle.
			l.log.Debug("negknow: the rebuild at Open could not persist tried.bloom", "err", err)
		}
		return
	}

	if l.bloom.Count() > want {
		// Extra keys are SAFE: each costs one record lookup that then answers AnswerAbsent with
		// BloomOnly, or AnswerStale — both correct. So the rebuild is scheduled, not forced.
		switch l.elim.RebuildOnStale {
		case rebuildImmediate:
			if _, _, err := l.rebuildLocked(context.Background()); err != nil {
				l.pending = true
			}
		case rebuildNextIdle:
			l.pending = true
		}
	}
}

// refreshAtOpen runs one bounded staleness refresh, so that a ledger is correct in wave 2 with no
// daemon wiring at all while still being cheap. On expiry the rebuild is simply owed.
func (l *ledger) refreshAtOpen() {
	if l.deps.Store == nil || l.blind || l.elim.RebuildOnStale == rebuildNever {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), openRefreshDeadline)
	defer cancel()
	if _, err := l.RefreshStaleness(ctx, l.deps.Store); err != nil {
		l.log.Debug("negknow: the staleness refresh at Open did not finish in its window", "err", err)
		l.pending = true
	}
}

// visible reports whether r is answerable under the requested query scope (§8.3 item 5).
//
//	ScopeProject       -> project-scoped records only, which is the cross-session carry-over
//	ScopeSession or "" -> project-scoped records, plus session-scoped records of THIS session
//
// The caller holds mu.
func (l *ledger) visible(r Record, q Scope) bool {
	if r.Scope == ScopeProject {
		return true
	}
	if q == ScopeProject {
		return false
	}
	return r.Session == l.deps.Session
}

// visibleActive returns every active record visible to this session. It is the ONE source the
// bloom rebuild draws its keys from. The caller holds mu.
func (l *ledger) visibleActive() []Record {
	out := make([]Record, 0, len(l.recs))
	for _, r := range l.recs {
		if r.Status == StatusActive && l.visible(r, ScopeSession) {
			out = append(out, r)
		}
	}
	return out
}

// dedupHex is a record's append-time identity: the domain-separated digest of its session, its
// scope and its descriptor key.
//
// It is deliberately not the record ID. recordID mixes in TS, so an MCP retry a second later
// mints a different id for the same elimination and an id-based dedup would let the log grow a
// duplicate line for it.
func dedupHex(r Record) string {
	var b bytes.Buffer
	b.WriteString(string(r.Session))
	b.WriteByte(fieldSep)
	b.WriteString(string(r.Scope))
	b.WriteByte(fieldSep)
	b.Write(r.Desc.Key())
	h := core.HashBytes(domainDedup, b.Bytes())
	return hex.EncodeToString(h[:])
}

// redact applies Deps.Redact to each field in place, when one was supplied.
//
// deps is written once, in Open, before the ledger escapes to any other goroutine, so reading
// deps.Redact does not need the lock.
func (l *ledger) redact(fields ...*string) {
	if l.deps.Redact == nil {
		return
	}
	for _, f := range fields {
		*f = string(l.deps.Redact([]byte(*f)))
	}
}

// Record appends one elimination and returns its id.
func (l *ledger) Record(ctx context.Context, r Record) (string, error) {
	start := l.clk.Now()
	defer func() { l.m.Hist(histRecord).Observe(l.clk.Since(start)) }()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return "", os.ErrClosed
	}

	// Redaction runs FIRST — before the descriptor, before the id, before anything is derived
	// from the text at all (R19). The descriptor is not an opaque digest: its NormalizedPath and
	// Symbol are lifted verbatim out of the target and are persisted as plaintext on the record
	// line, and its ReasonHash is a digest an attacker holding a candidate secret can confirm by
	// recomputing it. Canonicalizing before redacting would write a secret into an append-only
	// file (§7.4) that nothing in the system ever rewrites.
	l.redact(&r.Target, &r.Approach, &r.Reason)

	// The descriptor is recomputed unconditionally, so a caller can neither leave it zero nor
	// inject one that disagrees with the text fields. It is computed BEFORE normalizeRecord's
	// TRUNCATION, not after: Query canonicalizes the caller's own untruncated target, so a
	// descriptor keyed on the truncated text would be a key no query could ever produce. Query
	// redacts before it canonicalizes for exactly the same reason, so the two still agree.
	r.Desc = Canonicalize(r.Target, r.Approach, r.Reason)

	if r.Session == "" {
		r.Session = l.deps.Session
	}
	if r.TS == 0 {
		r.TS = core.UnixMilli(l.clk.Now().UnixMilli())
	}
	if r.Scope == "" {
		r.Scope = Scope(l.elim.DefaultScope)
	}
	if r.Status == StatusStale {
		l.log.Warn("negknow: a record may not be created stale; storing it active", "target", r.Target)
		r.Status = ""
		r.StaleSince, r.StaleBecause = 0, nil
	}
	if r.Status == "" {
		r.Status = StatusActive
	}

	normalizeRecord(&r, l.deps.Redact, func(s string) { l.log.Warn("negknow: " + s) })

	if l.elim.RequireEvidence && r.Evidence.IsZero() {
		l.m.Counter(counterRejectedNoEvidence).Add(1)
		return "", ErrNoEvidence
	}

	// Identity dedup, before the id is minted. A hit that is still active means this exact
	// elimination is already on record; recording it twice is a no-op, not an error, because MCP
	// retries and the heuristic detector both re-propose. A hit that is STALE falls through: a
	// re-record after a staleness flip is exactly the re-verification §8.3 asks for, and it must
	// produce a new active record.
	dk := dedupHex(r)
	if i, ok := l.byKey[dk]; ok && l.recs[i].Status == StatusActive {
		l.m.Counter(counterRecordsDeduped).Add(1)
		return l.recs[i].ID, nil
	}

	if r.ID == "" {
		r.ID = recordID(r.Session, r.TS, r.Desc)
	}
	// An id that names an ACTIVE record is the same no-op the identity dedup above is: return it.
	//
	// An id that names a STALE one is not, and this is where the re-verification §8.3 asks for
	// would otherwise be lost. recordID mixes in TS, so re-recording an elimination that was
	// flipped stale inside the same clock millisecond mints the id the stale record already
	// carries — and replayLog keeps the FIRST of two lines sharing an id, so appending under it
	// would produce a record that answers correctly this session and vanishes at the next Open.
	// Advancing TS until the id is free is what keeps the new record separately addressable; each
	// step re-derives the id from a different preimage, so the loop cannot spin.
	for {
		i, ok := l.byID[r.ID]
		if !ok {
			break
		}
		if l.recs[i].Status == StatusActive {
			return l.recs[i].ID, nil
		}
		r.TS++
		r.ID = recordID(r.Session, r.TS, r.Desc)
	}

	if l.f == nil {
		return "", fmt.Errorf("%w: negknow: the elimination log is not appendable", core.ErrDegraded)
	}
	if err := appendLine(l.f, r); err != nil {
		// The in-memory index is updated only when the append succeeded: an index holding a
		// record the log does not is a ledger that forgets on the next Open.
		return "", err
	}

	idx := len(l.recs)
	l.recs = append(l.recs, r)
	l.byID[r.ID] = idx
	mh := r.Desc.MatchHex()
	l.byMatch[mh] = append(l.byMatch[mh], idx)
	l.byKey[dk] = idx
	// Both keys: Key is the identity, MatchKey is what Query tests. Effective capacity
	// consumption is therefore 2 x records, which is what every "2 *" in this package is.
	l.bloom.Add(r.Desc.Key())
	l.bloom.Add(r.Desc.MatchKey())
	if l.elim.RebuildOnStale == rebuildNextIdle {
		// The added keys live only in memory until a rebuild. Without this the next Open would
		// have to rebuild synchronously to recover them.
		l.pending = true
	}

	l.emitDAG(r)
	l.m.Counter(counterRecordsAppended).Add(1)
	return r.ID, nil
}

// count increments negknow.query.<state>.
func (l *ledger) count(state string) { l.m.Counter(counterQueryPrefix + state).Add(1) }

// Query answers the three-way already_tried question (§8.3 item 4).
//
// Every path either returns AnswerAbsent, or returns a record the index actually holds, or flags
// BloomOnly. There is no fourth path, and that is §13 invariant 3.
func (l *ledger) Query(ctx context.Context, target, approach string, scope Scope) (Answer, error) {
	start := l.clk.Now()
	defer func() { l.m.Hist(histQuery).Observe(l.clk.Since(start)) }()

	// Redaction runs before canonicalization here for the same reason it does in Record, and it
	// has to: Record persisted a descriptor over REDACTED text, so a query that canonicalized the
	// raw text would compute a different MatchKey and the two would never meet (R19).
	l.redact(&target, &approach)

	// The reason is unknown at query time, which is exactly why MatchKey excludes it.
	d := Canonicalize(target, approach, "")
	mk := d.MatchKey()

	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.blind {
		return Answer{State: AnswerAbsent}, nil
	}
	if l.bloom == nil || !l.bloom.Test(mk) {
		l.count(queryStateAbsent)
		return Answer{State: AnswerAbsent}, nil
	}

	idx := l.byMatch[hex.EncodeToString(mk)]
	cands := make([]Record, 0, len(idx))
	for _, i := range idx {
		if l.visible(l.recs[i], scope) {
			cands = append(cands, l.recs[i])
		}
	}
	if len(cands) == 0 {
		// The bloom said yes and the records say no: a false positive, reported as one.
		l.count(queryStateBloomOnly)
		return Answer{State: AnswerAbsent, BloomOnly: true}, nil
	}

	if act := pick(cands, StatusActive); act != nil {
		l.count(queryStateActive)
		return Answer{State: AnswerActive, Record: act}, nil
	}

	st := pick(cands, StatusStale)
	if st == nil {
		// Neither active nor stale: a materialized line carrying a status no version of this
		// package mints — a log written by a newer plugin, or edited by hand. The record exists,
		// so Health counts it in Records, but it is not an ANSWER: returning AnswerStale with a
		// nil Record would hand SP-13 a state whose contract promises a reason, and MCPResult
		// would answer "absent" for it anyway. Reporting the bloom hit as unbacked is both true
		// and the safe direction (R20). The status is left exactly as the log spelled it —
		// rewriting it at materialization would destroy the forward compatibility the unknown-op
		// rule exists to provide.
		l.count(queryStateBloomOnly)
		return Answer{State: AnswerAbsent, BloomOnly: true}, nil
	}

	// Every visible match is stale.
	if l.elim.StaleResponse == staleResponseDrop {
		l.count(queryStateAbsent)
		return Answer{State: AnswerAbsent}, nil
	}
	l.count(queryStateStale)
	return Answer{State: AnswerStale, Record: st, Note: StaleNote}, nil
}

// pick returns the candidate with the given status having the greatest TS, ties broken by the
// lexicographically greatest ID, or nil when none match.
//
// cands already holds copies, and the returned pointer addresses a further copy, so a caller can
// never reach into ledger state through the Answer it was handed.
func pick(cands []Record, status Status) *Record {
	best := -1
	for i := range cands {
		if cands[i].Status != status {
			continue
		}
		if best < 0 || cands[i].TS > cands[best].TS ||
			(cands[i].TS == cands[best].TS && cands[i].ID > cands[best].ID) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	out := cands[best]
	return &out
}

// MCPResult renders a as SP-13's AlreadyTriedResult fields.
//
// A nil Record is answered "absent" whatever the state says. The active and stale branches are
// only reachable with a non-nil Record by construction, but a hand-built Answer is not, and
// panicking inside an MCP tool call is not a failure mode this returns.
func (a Answer) MCPResult() (state, reason, note, evidence string) {
	if a.Record == nil {
		return queryStateAbsent, "", "", ""
	}
	switch a.State {
	case AnswerActive:
		return queryStateActive, a.Record.Reason, "", a.Record.Evidence.String()
	case AnswerStale:
		return queryStateStale, a.Record.Reason, a.Note, a.Record.Evidence.String()
	default:
		return queryStateAbsent, "", "", ""
	}
}

// Get returns a copy of the record with the given id, or core.ErrNotFound.
func (l *ledger) Get(ctx context.Context, id string) (Record, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	i, ok := l.byID[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: negknow: no elimination record %q", core.ErrNotFound, id)
	}
	return l.recs[i], nil
}

// Active returns every active record visible at scope, ordered by TS ascending then ID ascending.
func (l *ledger) Active(ctx context.Context, scope Scope) ([]Record, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Record, 0, len(l.recs))
	for _, r := range l.recs {
		if r.Status == StatusActive && l.visible(r, scope) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS < out[j].TS
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// All returns every materialized record in log order, regardless of status, scope or session.
// This is what `qompack fsck` and SP-16's warm start read.
func (l *ledger) All(ctx context.Context) ([]Record, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Record, len(l.recs))
	copy(out, l.recs)
	return out, nil
}

// TopActive returns the n highest-scoring visible active records and how many were left out.
//
// An unscored record scores zero; ties fall back to TS descending, then ID ascending, so the
// order is total and a digest never reshuffles between two renders of the same ledger.
func (l *ledger) TopActive(ctx context.Context, scope Scope, n int, score map[string]float64) ([]Record, int, error) {
	all, err := l.Active(ctx, scope)
	if err != nil {
		return nil, 0, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		si, sj := score[all[i].ID], score[all[j].ID]
		if si != sj {
			return si > sj
		}
		if all[i].TS != all[j].TS {
			return all[i].TS > all[j].TS
		}
		return all[i].ID < all[j].ID
	})
	if n < 0 {
		n = 0
	}
	if n > len(all) {
		n = len(all)
	}
	return all[:n], len(all) - n, nil
}

// health is Health's body for a caller already holding mu.
func (l *ledger) health() Health {
	h := Health{Records: len(l.recs), Active: len(l.visibleActive())}
	for _, r := range l.recs {
		if r.Status == StatusStale {
			h.Stale++
		}
	}
	if l.blind || l.bloom == nil {
		return h
	}
	h.FillRatio = l.bloom.FillRatio()
	h.EstFPRate = l.bloom.EstimatedFPRate()
	_, _, h.NeedsResize = l.bloom.ResizeTarget()
	return h
}

// Health reports the ledger's size and the filter's saturation — the struct /qompack:status
// prints and §11.4's "monitor fill ratio and resize" watch-for.
func (l *ledger) Health() Health {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.health()
}

// Close releases the append handle. It is idempotent, and it deliberately does no work: a Close
// that rebuilt the filter or ran a refresh could block a SessionEnd hook, and everything it would
// do is recoverable from the log at the next Open.
//
// After Close, the read methods keep answering from the in-memory index exactly as before, and
// every write path reports os.ErrClosed without appending.
func (l *ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// nopRegistry is the obs.Registry a ledger constructed without one uses. internal/obs ships no
// no-op constructor and Rule W-3 forbids adding one there, so it lives here; every method
// discards what it is given.
type nopRegistry struct{}

func (nopRegistry) Hist(string) obs.Histogram    { return nopHistogram{} }
func (nopRegistry) Counter(string) obs.Counter   { return nopCounter{} }
func (nopRegistry) Gauge(string) obs.Gauge       { return nopGauge{} }
func (nopRegistry) Snapshot() obs.Snapshot       { return obs.Snapshot{} }
func (nopRegistry) Persist(l paths.Layout) error { return nil }

func (nopRegistry) CheckBudgets(cfg config.Config) []obs.BudgetBreach { return nil }

type nopHistogram struct{}

func (nopHistogram) Observe(time.Duration)      {}
func (nopHistogram) Snapshot() obs.HistSnapshot { return obs.HistSnapshot{} }
func (nopHistogram) Reset()                     {}

type nopCounter struct{}

func (nopCounter) Add(int64)    {}
func (nopCounter) Value() int64 { return 0 }

type nopGauge struct{}

func (nopGauge) Set(int64)    {}
func (nopGauge) Add(int64)    {}
func (nopGauge) Value() int64 { return 0 }

// ─── SP-09 Commit 6: the one accessor the heuristic detector reaches through ───────────────────
//
// Everything else the detector needs from a ledger lives in ingest.go (resolveDeps, Since); this
// is the second half of detector.go's metricsSource seam and is the only line Commit 6 adds to
// this file.

// registry returns the ledger's instrument registry.
//
// It exists so that detector.go can type-assert metricsSource on its ObservationSource rather than
// on *ledger: NewDetector accepts any ObservationSource, and a concrete-type assertion there would
// make the detector's counters work for this package's ledger and silently vanish for every other
// source. m is written once, in Open, before the ledger escapes to any other goroutine, so reading
// it needs no lock.
func (l *ledger) registry() obs.Registry { return l.m }
