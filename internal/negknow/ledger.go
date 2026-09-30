package negknow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
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
// is due (§11.4, §12 "Bloom saturation"), and whether Query's answers are currently backed by
// complete coverage (§11.3 Required invariants item 8).
type Health struct {
	Records, Active, Stale int
	FillRatio, EstFPRate   float64
	NeedsResize            bool
	// FilterGeneration is tried.bloom's current rebuild sequence number (bloom.go persistBloom's
	// seq counter): a freshness watermark a caller compares across two Health snapshots to tell
	// whether a rebuild has run between them.
	FilterGeneration int
	// DependencyCoverage is non-zero when the last RefreshStaleness could not compare dependency
	// hashes against the store: every active record with a dependency answers AnswerUncertain
	// from Query until a later refresh succeeds. It is the zero core.Omission when coverage is
	// current.
	DependencyCoverage core.Omission
}

// Ledger is the negative-knowledge elimination store's full seam (00-ARCHITECTURE.md §5.10):
// recording eliminations, answering the three-way already_tried question, tracking staleness
// against the store's current file versions, and rebuilding tried.bloom from the records.
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
	// RebuildBloom rebuilds tried.bloom from the RECORDS, active and stale — never from a
	// checkpoint, never from context (00-ARCHITECTURE.md §3.3, §13 invariant 2; coordinator
	// decision D49 widened "active records only", so a record goes stale and never absent). It
	// resizes if sketch.Bloom.ResizeTarget says so.
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

// The reason/recovery pairs behind every AnswerUnavailable or AnswerUncertain Query returns
// (00-ARCHITECTURE.md §11.3 Required invariants item 8). Each is a fixed pair rather than an
// interpolated message: a caller-facing Coverage value is domain data, not a log line, and a
// fixed pair is what makes it independently assertable rather than fuzzy-matched.
const (
	// reasonBlind and recoveryBlind explain an AnswerUnavailable produced under blind mode: the
	// elimination log itself could not be read at Open, so nothing on record can be confirmed
	// either way.
	reasonBlind   = "the elimination ledger is in blind mode: records/eliminations.jsonl could not be read"
	recoveryBlind = "repair or restore the elimination log and restart; this is not evidence the approach is untried"

	// reasonUnknownStatus and recoveryUnknownStatus explain an AnswerUncertain produced when a
	// bloom hit resolves to a visible record whose Status this reader does not recognize (a log
	// written by a newer plugin, or edited by hand).
	reasonUnknownStatus   = "a matching elimination record carries a status this reader does not recognize"
	recoveryUnknownStatus = "use a reader that understands the record's status, or inspect it directly; this is not evidence the approach is untried"

	// reasonStaleDropped and recoveryStaleDropped explain an AnswerUncertain produced when every
	// visible match is stale and eliminations.staleResponse is "drop": the staleness DETAIL is
	// suppressed by configuration, not the fact that an elimination is on record.
	reasonStaleDropped   = `a matching elimination is stale and eliminations.staleResponse is "drop", so its current applicability is not disclosed`
	recoveryStaleDropped = `set eliminations.staleResponse to "flag" to see the staleness detail, or re-verify the approach directly`

	// reasonDepCoverage and recoveryDepCoverage explain an AnswerUncertain produced when the last
	// RefreshStaleness could not compare a record's depends_on hashes against the store
	// (staleness.go).
	reasonDepCoverage   = "the last dependency-hash comparison against the file store failed, so this record's freshness cannot be confirmed"
	recoveryDepCoverage = "retry after the store recovers; a successful staleness refresh resolves this"

	// reasonFlipUnrecorded and recoveryFlipUnrecorded explain an AnswerUncertain produced when a
	// query's own dependency comparison (refreshMatches) proved a matching record stale but the
	// flip could not be appended to the log, so the record still reads active in memory.
	reasonFlipUnrecorded   = "a dependency of this record has changed, but its stale flip could not be recorded"
	recoveryFlipUnrecorded = "re-verify the approach directly; this is not evidence the approach is untried"
)

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

// The per-query counter suffixes. The answer states are spelled as their own names, per the
// subplan's "negknow.query.<state>, suffixed by the state's own name".
const (
	queryStateAbsent      = "absent"
	queryStateActive      = "active"
	queryStateStale       = "stale"
	queryStateBloomOnly   = "bloom_only"
	queryStateUnavailable = "unavailable"
	queryStateUncertain   = "uncertain"
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
	// answers AnswerUnavailable for everything: asserting absence, or activity, with no record
	// behind it is the one thing §12.3 and §11.3 invariant 8 forbid here.
	blind bool
	recs  []Record
	byID  map[string]int
	// byMatch maps a descriptor's match digest (MatchKey, the digest MatchHex spells) to the
	// record indices carrying it, in insertion order. This is the index Query reads, which is why
	// the query path performs no scan.
	//
	// Both indices are keyed on the raw 32-byte digests rather than on their 64-character hex
	// spelling, which is the re-keying ruling R27 left available (see budgetResidentBytes): the
	// hex form cost two string allocations per record at every Open and a 64-byte hash per lookup,
	// and it named exactly the same thing (SP09-D1).
	byMatch map[core.Hash][]int
	// byKey maps dedupDigest(session, scope, Desc.Key()) — the digest dedupHex spells — to the
	// most recently appended record with that identity: the idempotence index. Status is read from
	// recs, so MarkStale never touches this map.
	byKey map[core.Hash]int
	// ring is the bounded history of the last signalRing observations, for the heuristic detector.
	ring []Observation
	lay  paths.Layout
	// f is the append handle on records/eliminations.jsonl, or nil when the log could not be
	// opened for appending. Every append goes through it under mu.
	f      io.WriteCloser
	closed bool
	// barriers are what syncAcknowledged makes an acknowledged record durable through (durable.go):
	// the log's file sync and, once per ledger lifetime, the records directory's. The zero value is
	// the real thing and is all production uses; a test counts or cuts them. logNameDurable records
	// that the directory barrier has succeeded. Both are guarded by mu.
	barriers       paths.Barriers
	logNameDurable bool
	// pending reports that a bloom rebuild is owed (rebuildOnStale == "nextIdle").
	pending bool
	// seq is the rebuild generation. It is seeded at Open from paths.HighestBloomBackupSeq so it
	// resumes above every surviving tried.bloom.<n>.bak; nothing else on disk records it, and a
	// private counter file that could disagree with the backup names would be a second source of
	// truth for a number the filesystem already carries.
	seq int
	// depCoverage is non-zero when the last RefreshStaleness could not compare dependency hashes
	// against the store: Query downgrades an affected AnswerActive to AnswerUncertain until a
	// later refresh clears it (00-ARCHITECTURE.md §11.3 Required invariants item 8). Guarded by mu
	// like every other mutable field.
	depCoverage core.Omission
	deps        Deps
	log         logging.Logger
	m           obs.Registry
	clk         core.Clock
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
		byMatch: make(map[core.Hash][]int),
		byKey:   make(map[core.Hash]int),
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

	keys := l.loadRecords()
	loadFailed := l.acquireBloom(b)
	l.reconcileBloom(loadFailed, keys)
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
//
// It returns reindex's per-record bloom keys for the rebuild Open may owe next, or nil when
// nothing was materialized.
func (l *ledger) loadRecords() []recordKeys {
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
		return nil
	case err != nil:
		l.goBlind(err)
		return nil
	}

	// The file is already in hand, so the replay can be told how many records it can hold and size
	// its slice and index once instead of growing them a doubling at a time. replayCapacityHint
	// bounds that by the line count and the byte count both, so the reservation stays proportional
	// to the file however few of its lines turn out to be records.
	recs, byID, _, err := replayLogSized(bytes.NewReader(b), replayCapacityHint(b), l.log, l.m)
	if err != nil {
		// replayLog degrades every per-line problem and returns an error only when the READER
		// failed, i.e. when the materialization is partial. Reporting a partial one as complete
		// would silently un-eliminate whatever came after the failure.
		l.goBlind(err)
		return nil
	}
	l.recs, l.byID = recs, byID
	return l.reindex()
}

// goBlind enters blind mode: the ledger stays usable, Query answers unavailable for everything,
// and the degradation is loud exactly once (§12.3, §13 invariant 10, §11.3 invariant 8).
func (l *ledger) goBlind(err error) {
	l.blind = true
	l.recs, l.byID = nil, make(map[string]int)
	l.byMatch, l.byKey = make(map[core.Hash][]int), make(map[core.Hash]int)
	l.m.Counter(counterBlindMode).Add(1)
	l.log.Loud("negknow: elimination records unreadable; already_tried will answer unavailable for everything",
		"path", logPath(l.root), "err", err)
}

// recordKeys is one record's two bloom keys as digests: Desc.Key and Desc.MatchKey.
type recordKeys struct{ key, match core.Hash }

// reindex rebuilds byMatch and byKey from recs. byID is replayLog's, since the replay is what
// decides which of two lines sharing an id survives.
//
// Indexing derives both of every record's bloom keys — MatchKey for byMatch, Key inside the dedup
// identity — and it returns them, keys[i] for recs[i], because the rebuild Open owes next is built
// from exactly those digests; handing them over is what spares Open deriving each one twice.
func (l *ledger) reindex() []recordKeys {
	l.byMatch = make(map[core.Hash][]int, len(l.recs))
	l.byKey = make(map[core.Hash]int, len(l.recs))
	keys := make([]recordKeys, len(l.recs))
	for i := range l.recs {
		r, k := &l.recs[i], &keys[i]
		k.key, k.match = r.Desc.keyHash(), r.Desc.matchHash()
		l.byMatch[k.match] = append(l.byMatch[k.match], i)
		// The LAST line with an identity wins: byKey is the idempotence index, and what a caller
		// re-recording an identity should collapse onto is the most recent one.
		l.byKey[dedupDigest(r.Session, r.Scope, k.key)] = i // dedupHex(*r), as a digest
	}
	return keys
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
// records imply, or the feature silently stops working. The records are every record in this
// ledger's view, active AND stale (filterRecords, D49).
//
// It is skipped entirely under blind mode. The records that would feed a rebuild are unreadable,
// so rebuilding would replace a good on-disk cache with an empty one on the strength of a
// transient I/O error.
//
// keys is loadRecords' per-record bloom keys, handed to any rebuild this makes so that it does not
// derive them again; nil derives them in the rebuild as usual.
func (l *ledger) reconcileBloom(loadFailed bool, keys []recordKeys) {
	if l.blind {
		return
	}
	want := 2 * l.filterRecordCount()

	if loadFailed || l.filterMissesARecord(keys) {
		// Missing keys are false NEGATIVES, which defeat the feature without any symptom. This is
		// the common case rather than an exception: Record adds keys to the in-memory filter only,
		// and §3.3 lets nothing but a rebuild replace tried.bloom, so the on-disk filter always
		// lags the log by whatever has been recorded since the last rebuild.
		//
		// The question is asked per record, not by comparing Count against want. A count cannot
		// see WHICH key is missing: a filter written when a record was active, or by a build whose
		// rebuild held active records only, can hold as many keys as the log implies and still
		// lack a stale record's, and that record then answered absent after every restart (R4-1).
		if _, _, err := l.rebuildWith(context.Background(), keys); err != nil {
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
			if _, _, err := l.rebuildWith(context.Background(), keys); err != nil {
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

// visible reports whether r is answerable to session sess under the requested query scope (§8.3
// item 5).
//
//	ScopeProject       -> project-scoped records only, which is the cross-session carry-over
//	ScopeSession or "" -> project-scoped records, plus session-scoped records of sess
//
// sess is the caller's session (sessionFor), never assumed to be the one the ledger was opened
// for: the daemon's ledger serves every session of the project.
//
// The caller holds mu.
func (l *ledger) visible(r Record, q Scope, sess core.SessionID) bool {
	if r.Scope == ScopeProject {
		return true
	}
	if q == ScopeProject {
		return false
	}
	return r.Session == sess
}

// inView reports whether r belongs to what this ledger can be asked about at all. A ledger opened
// for one session (Deps.Session set) answers for that session alone, so it is that session's view.
// A ledger opened with no session is the daemon's multi-session ledger: any session may ask, each
// sees its own session-scoped records, so every record is in view and visible() decides per call.
//
// The caller holds mu.
func (l *ledger) inView(r Record) bool {
	if l.deps.Session == "" {
		return true
	}
	return l.visible(r, ScopeSession, l.deps.Session)
}

// filterRecords yields the position in recs of every record in this ledger's view (inView), active
// and stale alike, in log order. It is the ONE source the bloom rebuild draws its keys from, and
// what reconcileBloom checks the filter against. It yields positions rather than copies because
// every caller reads the records in place: a copy of the whole visible set is 5.9 MB at 20 000
// records, and Open used to make three of them. The caller holds mu, for as long as it is
// iterating.
//
// Stale records are in it because a record goes stale, never absent (coordinator decision D49).
// Query tests the filter BEFORE it reads a record, so a record whose keys the filter does not hold
// answers absent whatever its status; filtered to active records, as §3.3 first read, a stale
// record answered stale only until the next rebuild or daemon restart and absent after (R4-1).
// A record of a status this reader does not know is in it for the same reason: Query answers it
// uncertain, and only while the filter holds its key.
//
// For the daemon's multi-session ledger that is every record of every session. The filter is a
// cache over the records (§13 invariant 3), so a key another session's record contributes costs a
// query from this session one record lookup that visible() then refuses — a plain absence (Query),
// since the hit is true and only out of this session's scope — while leaving a session's own
// records out of the filter would make them unanswerable after the next rebuild.
func (l *ledger) filterRecords() iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range l.recs {
			if l.inView(l.recs[i]) && !yield(i) {
				return
			}
		}
	}
}

// filterRecordCount is how many records filterRecords yields. The caller holds mu.
func (l *ledger) filterRecordCount() int {
	n := 0
	for range l.filterRecords() {
		n++
	}
	return n
}

// filterMissesARecord reports whether the filter lacks the MatchKey of any record filterRecords
// yields. MatchKey is the key Query tests, so it is the one whose absence is a false negative; the
// identity Key is never tested and its absence costs nothing. keys is loadRecords' per-record
// digests when it lines up with recs, and nil otherwise. The caller holds mu.
func (l *ledger) filterMissesARecord(keys []recordKeys) bool {
	if len(keys) != len(l.recs) {
		keys = nil
	}
	for i := range l.filterRecords() {
		var mh core.Hash
		if keys != nil {
			mh = keys[i].match
		} else {
			mh = l.recs[i].Desc.matchHash()
		}
		if !l.bloom.Test(mh[:]) {
			return true
		}
	}
	return false
}

// visibleActive yields the position in recs of every ACTIVE record in this ledger's view (inView),
// in log order: what Health counts as Active. The caller holds mu, for as long as it is iterating.
func (l *ledger) visibleActive() iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range l.recs {
			if l.recs[i].Status == StatusActive && l.inView(l.recs[i]) && !yield(i) {
				return
			}
		}
	}
}

// visibleActiveCount is how many records visibleActive yields. The caller holds mu.
func (l *ledger) visibleActiveCount() int {
	n := 0
	for range l.visibleActive() {
		n++
	}
	return n
}

// dedupHex is a record's append-time identity: the domain-separated digest of its session, its
// scope and its descriptor key.
//
// It is deliberately not the record ID. recordID mixes in TS, so an MCP retry a second later
// mints a different id for the same elimination and an id-based dedup would let the log grow a
// duplicate line for it.
func dedupHex(r Record) string { return dedupHexKey(r.Session, r.Scope, r.Desc.keyHash()) }

// dedupOf is dedupHex as the digest byKey is keyed on.
func dedupOf(r Record) core.Hash { return dedupDigest(r.Session, r.Scope, r.Desc.keyHash()) }

// dedupHexKey is dedupHex over a record's parts, for a caller holding its descriptor key as an
// already-derived digest. The preimage is dedupHex's exactly — session, fieldSep, scope,
// fieldSep, then the key's 32 bytes — assembled by append into a stack buffer.
func dedupHexKey(sess core.SessionID, scope Scope, key core.Hash) string {
	return hexString(dedupDigest(sess, scope, key))
}

// dedupDigest is the digest dedupHexKey spells in hex, and the key byKey is indexed on.
func dedupDigest(sess core.SessionID, scope Scope, key core.Hash) core.Hash {
	var buf [keyPreimageBuf]byte
	b := append(buf[:0], sess...)
	b = append(b, fieldSep)
	b = append(b, scope...)
	b = append(b, fieldSep)
	b = append(b, key[:]...)
	return core.HashBytes(domainDedup, b)
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
		r.Session = l.sessionFor(ctx)
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
	dk := dedupOf(r)
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
	mh := r.Desc.matchHash()
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

	l.emitDAG(r, turnFor(ctx))
	l.m.Counter(counterRecordsAppended).Add(1)
	return r.ID, nil
}

// count increments negknow.query.<state>.
func (l *ledger) count(state string) { l.m.Counter(counterQueryPrefix + state).Add(1) }

// Query answers the already_tried question (§8.3 item 4, §11.3 Required invariants item 8).
//
// Every path either returns AnswerAbsent, or returns a record the index actually holds (possibly
// downgraded to AnswerUncertain when its dependency coverage is unverified), or flags BloomOnly,
// or reports AnswerUnavailable/AnswerUncertain with a Coverage reason and recovery direction when
// the ledger cannot back any of the first three with confidence. There is no fifth path: §13
// invariant 3 and §11.3 invariant 8 both hold across every return in this function.
func (l *ledger) Query(ctx context.Context, target, approach string, scope Scope) (Answer, error) {
	start := l.clk.Now()
	defer func() { l.m.Hist(histQuery).Observe(l.clk.Since(start)) }()

	// Redaction runs before canonicalization here for the same reason it does in Record, and it
	// has to: Record persisted a descriptor over REDACTED text, so a query that canonicalized the
	// raw text would compute a different MatchKey and the two would never meet (R19).
	l.redact(&target, &approach)

	// The reason is unknown at query time, which is exactly why MatchKey excludes it.
	d := Canonicalize(target, approach, "")
	mh := d.matchHash()
	mk := mh[:] // d.MatchKey(), whose array is this call's own
	sess := l.sessionFor(ctx)

	// The records this question could be answered from are brought up to date with the store
	// BEFORE the answer is read, so a dependency change captured earlier in this very session is
	// reflected now rather than at the next idle refresh (refreshMatches).
	verified, cov := l.refreshMatches(ctx, mh, scope, sess)

	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.blind {
		// The log itself could not be read: nothing on record can be confirmed OR ruled out, so
		// asserting AnswerAbsent here would be exactly the false negative §12.3 and §11.3
		// invariant 8 forbid — a stale-or-worse elimination silently vanishing because the reader
		// that would have found it could not open the log.
		l.count(queryStateUnavailable)
		return Answer{
			State:    AnswerUnavailable,
			Coverage: core.Omission{Reason: reasonBlind, Recovery: recoveryBlind},
		}, nil
	}
	if l.bloom == nil || !l.bloom.Test(mk) {
		l.count(queryStateAbsent)
		return Answer{State: AnswerAbsent}, nil
	}

	idx := l.byMatch[mh]
	cands := make([]Record, 0, len(idx))
	backed := false
	for _, i := range idx {
		backed = backed || l.inView(l.recs[i])
		if l.visible(l.recs[i], scope, sess) {
			cands = append(cands, l.recs[i])
		}
	}
	if len(cands) == 0 {
		if backed {
			// The filter was right: a record in this ledger's view holds the key, and the record
			// lookup scoped it out — another session's session-scoped record in the daemon's
			// ledger, or a session-scoped one under a project-scope query. That is a plain,
			// record-backed absence, counted as one. Flagging it BloomOnly would tell the caller no
			// backing record exists when one does, would reveal that some session tried the
			// approach, and would count a true hit as a filter false positive.
			l.count(queryStateAbsent)
			return Answer{State: AnswerAbsent}, nil
		}
		// The bloom said yes and no record in this ledger's view backs it: a false positive — or
		// a filter not yet rebuilt since the key left the view — reported as one.
		l.count(queryStateBloomOnly)
		return Answer{State: AnswerAbsent, BloomOnly: true}, nil
	}

	if act := pick(cands, StatusActive); act != nil {
		if cov == (core.Omission{}) && !verified {
			cov = l.depCoverage
		}
		if cov != (core.Omission{}) && len(act.DependsOn) > 0 {
			// A dependency-hash comparison failed — this query's own, or the last full refresh
			// when this query could not re-check the record itself — and THIS record has a
			// dependency it would have covered: its freshness cannot currently be confirmed, so it
			// is not backed as active (§11.3 invariant 8). A record with no dependency at all is
			// unaffected — there was nothing for the failed comparison to have told it anyway.
			l.count(queryStateUncertain)
			return Answer{State: AnswerUncertain, Record: act, Coverage: cov}, nil
		}
		l.count(queryStateActive)
		return Answer{State: AnswerActive, Record: act}, nil
	}

	st := pick(cands, StatusStale)
	if st == nil {
		// Neither active nor stale: a materialized line carrying a status no version of this
		// package mints — a log written by a newer plugin, or edited by hand. The record exists,
		// so Health counts it in Records, but it is not an ANSWER: returning AnswerAbsent would
		// assert there is no elimination on record when there plainly is one, which is exactly
		// what §11.3 invariant 8 forbids, and returning AnswerStale with a nil Record would hand
		// SP-13 a state whose contract promises a reason (R20). AnswerUncertain says what is
		// actually true — something is here, but this reader cannot resolve it to active, stale
		// or absent. The status is left exactly as the log spelled it — rewriting it at
		// materialization would destroy the forward compatibility the unknown-op rule exists to
		// provide.
		l.count(queryStateUncertain)
		return Answer{
			State: AnswerUncertain, BloomOnly: true,
			Coverage: core.Omission{Reason: reasonUnknownStatus, Recovery: recoveryUnknownStatus},
		}, nil
	}

	// Every visible match is stale.
	if l.elim.StaleResponse == staleResponseDrop {
		// "drop" suppresses the staleness DETAIL, not the fact that an elimination is on record.
		// AnswerAbsent would assert this approach was never tried, which the ledger knows to be
		// false (§11.3 invariant 8); AnswerUncertain reports honestly that applicability cannot be
		// confirmed, without disclosing the stale record's detail the configuration asked to hide.
		l.count(queryStateUncertain)
		return Answer{
			State:    AnswerUncertain,
			Coverage: core.Omission{Reason: reasonStaleDropped, Recovery: recoveryStaleDropped},
		}, nil
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
// AnswerUnavailable and AnswerUncertain are rendered from Coverage regardless of Record, because
// §11.3 invariant 8 applies whether or not a record happens to be attached. For every other
// state, a nil Record is answered "absent": the active and stale branches are only reachable with
// a non-nil Record by construction, but a hand-built Answer is not, and panicking inside an MCP
// tool call is not a failure mode this returns.
func (a Answer) MCPResult() (state, reason, note, evidence string) {
	switch a.State {
	case AnswerUnavailable:
		return queryStateUnavailable, a.Coverage.Reason, a.Coverage.Recovery, ""
	case AnswerUncertain:
		return queryStateUncertain, a.Coverage.Reason, a.Coverage.Recovery, ""
	}
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
	sess := l.sessionFor(ctx)
	out := make([]Record, 0, len(l.recs))
	for _, r := range l.recs {
		if r.Status == StatusActive && l.visible(r, scope, sess) {
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
	h := Health{
		Records: len(l.recs), Active: l.visibleActiveCount(),
		FilterGeneration: l.seq, DependencyCoverage: l.depCoverage,
	}
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
