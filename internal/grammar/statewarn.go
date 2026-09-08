package grammar

import (
	"bytes"
	"slices"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// This file implements contract §5's state-aware loop detector: the bounded, WARNING-ONLY layer
// that sits beside the frozen Warning/FormatWarning pair rather than replacing them.
//
// The premise, from plans/V5-SP-15-analyzer-selection-and-grammar.md §3, is that a repeated ACTION
// is not a loop. A session that runs the same test command five times while the file under it
// changes is working; a session that runs it five times against an unchanged file with an unchanged
// failure is stuck. Everything below exists to keep those two apart, and every default is chosen so
// that the ambiguous case produces silence rather than a warning — a false alarm here costs the
// user's attention and the context window this system exists to conserve, and a missed warning
// costs nothing that was not already lost.
//
// The six bounds of contract §5, and where each one lives:
//
//	1. advisory only        no method and no field on StateWarning can express a constraint;
//	                        Observe's only output is a StateWarning (see the type in types.go)
//	2. progress suppresses  Detector.Observe, the progressObserved latch
//	3. unknown ⇒ uncertain  Detector.Observe, the unknownSeen latch and StateWarning.Uncertain
//	4. dedup + session cap  Detector.Observe, dedupKey and DetectorConfig.MaxPerSession
//	5. self-suppression     Observation.SelfOriginated and IsSelfOriginated
//	6. separate accounting  DetectorStats, RecordOutcome
//
// The detector holds NO clock and does NO I/O. Every bound is expressed in core.TurnIndex, so a
// replay harness that feeds the same observations in the same order gets byte-identical warnings —
// which is what makes the M6-G15-A false-alarm/usefulness report reproducible rather than a
// description of one wall-clock afternoon.

// stateSignatureDomain is the hash domain StateSignature.Key mints under. It is declared here,
// unexported, rather than added to internal/core/hash.go's registry, because that is the pattern
// every package minting its own keys already follows — internal/sketch/hash.go's three sketch
// domains, internal/negknow/descriptor.go's four, internal/daemon/ingest.go's three. core's registry
// carries the SHARED domains, the ones two packages have to agree on; this one has exactly one
// producer and nothing outside this package may mint a state-signature key.
//
// Why not reuse one of core's exported domains: each is wrong, and reusing the nearest one would
// defeat the mechanism. core.DomainNegKnow is the bloom key of an ELIMINATION — the one thing
// contract §5.1 says a StateWarning must never become — so minting warning keys under it would put
// the two in one collision space, precisely where an accidental collision would be read as "this
// approach was already ruled out". core.DomainDecision, core.DomainArgs, core.DomainChunk and
// core.DomainRoot each name something a state signature is not. Domain separation exists so a
// digest minted for one purpose cannot be confused with another's; borrowing a domain because it is
// nearby is how that guarantee is thrown away.
//
// Treat this string as a wire format, not an identifier: changing it re-keys every dedup key a
// running session is holding.
const stateSignatureDomain = "qompack.grammar.state.v1"

// stateKeyPrefix makes a state key recognizable on sight in a log line or a dedup map, the same way
// internal/eval/blocks.go prefixes an elimination block and core.NewDecisionID prefixes "dec_".
const stateKeyPrefix = "gstate_"

// stateFieldSep separates StateSignature's four fields in the hash preimage. It is 0x1f (ASCII Unit
// Separator), the same separator negknow.Descriptor.Key uses, so the two constructions can be read
// against each other.
const stateFieldSep = 0x1f

// Key returns a stable, deterministic, domain-separated identity for the state s describes
// (contract §5). Equal signatures always produce equal keys and, because the preimage is a fixed
// field order rather than anything map-shaped, the same signature produces the same key in every
// process and every run.
//
// It returns a DIGEST rather than the fields themselves for a reason that is about where the key
// ends up, not about size. A key becomes StateWarning.DedupKey, which is carried in the warning
// record and is the natural thing for the daemon and the M6-G15-A report to log. The fields are a
// goal in the user's own words, a repository path and a failure signature — content this plugin
// tries hard not to spray into logs. Twelve hex characters identify the state exactly as well for
// dedup purposes and disclose none of it.
//
// Collision analysis, because a hash truncated to 48 bits deserves one. Distinct keys are needed
// only inside a single session's dedup map, which holds at most a few hundred states; at that scale
// an accidental 48-bit collision is not a risk anyone will meet. The 0x1f separator is likewise
// ambiguous in principle — a Failure string containing a literal 0x1f could be re-split — but both
// failure modes point the same, safe way: two states that collide share a DedupKey, so the second
// one's warning is SUPPRESSED. This layer errs toward saying nothing, and a collision here can only
// make it quieter. That is the opposite of negknow.Descriptor.Key, where a collision would widen the
// set of records a query matches, and it is why the same construction is acceptable here without
// the length-prefixed preimage a binding decision would demand.
func (s StateSignature) Key() string {
	var b bytes.Buffer
	b.Grow(len(s.Goal) + len(s.Target) + len(s.Action) + len(s.Failure) + 3)
	b.WriteString(s.Goal)
	b.WriteByte(stateFieldSep)
	b.WriteString(s.Target)
	b.WriteByte(stateFieldSep)
	b.WriteString(s.Action)
	b.WriteByte(stateFieldSep)
	b.WriteString(s.Failure)
	h := core.HashBytes(stateSignatureDomain, b.Bytes())
	return stateKeyPrefix + h.Short()
}

// The markers that identify a symbol as Qompack's own, for contract §5.5's self-suppression bound.
// Each names a real surface this plugin puts into a session, with the file that owns the spelling:
//
//   - selfMarkerMCPTool is the ephemeral retrieval tool prefix internal/mcp/ephemeral.go writes and
//     internal/observer/doc.go item 6 classifies ("a tool whose normalized name begins with
//     mcp__qompack__ is a retrieval result").
//   - selfMarkerSlashCommand covers SP-14's slash commands and the injection markers built from the
//     same spelling, such as internal/checkpoint's InjectionCloseTag.
//   - selfMarkerWarning is the prefix FormatWarning itself emits. It is the direct feedback edge:
//     a warning injected into the prompt becomes part of the next turn's text, and a goal or failure
//     signature derived from that text must not be able to produce the next warning.
//   - selfMarkerStateDir is the plugin's own state directory (internal/paths/layout.go's dotDir), so
//     an action targeting Qompack's files is never mistaken for the session's own work.
const (
	selfMarkerMCPTool      = "mcp__qompack__"
	selfMarkerSlashCommand = "/qompack:"
	selfMarkerWarning      = "[qompack]"
	selfMarkerStateDir     = ".qompack"
)

// IsSelfOriginated reports whether s describes Qompack's own injection, retrieval or warning path
// rather than the session's work (contract §5.5).
//
// It exists as a second, independent guard behind Observation.SelfOriginated, and the duplication is
// deliberate. The explicit flag is the correct mechanism — the composition root knows exactly which
// records it produced — but bound 5 is the one whose failure mode is a runaway: a warning that is
// injected, observed, and turned into the next warning is a feedback loop inside the component whose
// entire job is to notice feedback loops. A wiring change that forgets to set the flag would be an
// ordinary oversight anywhere else and a self-amplifying one here, so the classifier below catches
// the marked surfaces even when the flag is missing.
//
// All four fields are scanned, not just Action, because the loop can close through any of them: a
// retrieval result arrives as an ACTION, an injected warning lands in the next turn's GOAL or
// FAILURE text, and Qompack's own state files appear as a TARGET.
//
// Contains rather than HasPrefix for the two textual markers, since an injected line is quoted
// inside a larger message far more often than it starts one. A false positive here suppresses a
// warning, which is the direction this whole file leans, so the loose match is the safe one.
func IsSelfOriginated(s StateSignature) bool {
	for _, f := range [...]string{s.Goal, s.Target, s.Action, s.Failure} {
		switch {
		case strings.HasPrefix(f, selfMarkerMCPTool),
			strings.Contains(f, selfMarkerSlashCommand),
			strings.Contains(f, selfMarkerWarning):
			return true
		case f == selfMarkerStateDir || strings.HasPrefix(f, selfMarkerStateDir+"/"):
			// A paths.Key-form path is slash-separated and normalized, so the directory test is a
			// prefix test on the one separator that form uses.
			return true
		}
	}
	return false
}

// Detector defaults. Every one of them is a bound on how loud this feature is allowed to be, so each
// is set by asking what the WRONG answer costs rather than by what would detect the most loops.
const (
	// defaultMinRepeats is how many occurrences of one signature, with nothing observed to change,
	// it takes before a warning is possible. Two is an ordinary re-check — a command re-run to read
	// its output again, a test repeated after a change the observer did not see. Three identical
	// (goal, target, action, failure) tuples is the smallest count that is hard to explain as
	// deliberate, and warning at two is exactly how a detector earns a reputation for crying wolf.
	defaultMinRepeats = 3
	// defaultWindowTurns is the width, in turns, of the dedup window (contract §5.4) and therefore
	// also the detector's memory bound: at most this many distinct turn indices can fall inside one
	// window, and windows older than the previous one are pruned. Real loops are tight — the same
	// state recurs within a handful of turns — so twenty comfortably contains one, while a state
	// legitimately revisited much later in a session lands in a later window and is allowed to be
	// reported once more.
	defaultWindowTurns core.TurnIndex = 20
	// defaultMaxPerSession is the hard cap on delivered warnings (contract §5.4). A warning is
	// injected through UserPromptSubmit, so it is spent from the context budget this plugin exists
	// to conserve. Three is enough that a real loop is hard to miss and few enough that a
	// mis-tuned detector cannot become the thing filling the window.
	defaultMaxPerSession = 3
	// defaultExpiryTurns is how many turns a warning stays deliverable after it is produced
	// (StateWarning.ExpiresAt). A warning describes what the session was doing at a moment; arriving
	// ten turns after the session moved on, it is not just noise but noise that reads as the plugin
	// being confused about the present.
	defaultExpiryTurns core.TurnIndex = 5
	// retainedWindows is how many window ordinals the detector keeps state for: the current one and
	// its predecessor. The predecessor is kept so an observation arriving just after a window
	// boundary still joins the run it belongs to instead of starting a fresh count.
	retainedWindows = 1
)

// The two Message strings a StateWarning carries into the frozen FormatWarning.
//
// FormatWarning's TEMPLATE is frozen and this file does not touch it; Message is the one part of a
// Warning that has always been supplied per warning ("a short, human-readable suggestion", types.go).
// The confident phrasing is the exact string 00-ARCHITECTURE.md §14.1's worked example uses, so the
// ordinary case invents no wording at all.
//
// The uncertain phrasing exists because bound 3 would otherwise be invisible where it matters. A
// warning produced from incomplete observation coverage must read as a question; if it rendered
// identically to a confident one, Uncertain would be a field only a program could see, and the user
// would be told a loop is happening on evidence that cannot establish it. The frozen template
// already hedges with "possible loop", and this completes the sentence in the same register.
const (
	messageRepeatedState          = "consider a different approach"
	messageRepeatedStateUncertain = "observation coverage is incomplete, so this may not be a loop"
)

// DetectorConfig bounds how much a Detector may say. The zero value is valid and means "every
// default": a caller that has not decided yet gets the shipped bounds rather than a detector that
// divides by a zero window or warns on the first repeat.
//
// The switch that decides whether the detector runs AT ALL is not here. Contract §7 gives that to
// runtime.selection.loopWarningsEnabled, defaulting false, owned by Main at the composition root.
// This package deliberately does not read config: a component that can silence itself is a component
// whose tests have to reproduce the whole configuration stack to prove it said anything.
type DetectorConfig struct {
	// MinRepeats is how many occurrences of one signature inside a window it takes to warn.
	// Values below 2 are raised to the default: a "loop" of one occurrence is a category error.
	MinRepeats int
	// WindowTurns is the dedup window width in turns. Non-positive values take the default.
	WindowTurns core.TurnIndex
	// MaxPerSession caps delivered warnings for the Detector's lifetime. Zero takes the default;
	// use a negative value to mean "none", which is the in-code equivalent of the config switch
	// being off and is exercised by the bounds tests.
	MaxPerSession int
	// ExpiryTurns is how long a produced warning stays deliverable. Non-positive values take the
	// default.
	ExpiryTurns core.TurnIndex
}

// DefaultDetectorConfig returns the shipped bounds. It is exported so a caller can read, log or
// adjust one field without having to know which zero values mean "default" — and so the numbers in
// a M6-G15-A report can be printed from the same place the detector reads them.
func DefaultDetectorConfig() DetectorConfig {
	return DetectorConfig{
		MinRepeats:    defaultMinRepeats,
		WindowTurns:   defaultWindowTurns,
		MaxPerSession: defaultMaxPerSession,
		ExpiryTurns:   defaultExpiryTurns,
	}
}

// normalized fills in defaults for the zero and nonsensical values described on each field. It is
// applied once, in NewDetector, so nothing downstream has to re-check a bound and no arithmetic in
// this file can meet a zero window width.
func (c DetectorConfig) normalized() DetectorConfig {
	out := c
	if out.MinRepeats < 2 {
		out.MinRepeats = defaultMinRepeats
	}
	if out.WindowTurns <= 0 {
		out.WindowTurns = defaultWindowTurns
	}
	if out.MaxPerSession == 0 {
		out.MaxPerSession = defaultMaxPerSession
	}
	if out.ExpiryTurns <= 0 {
		out.ExpiryTurns = defaultExpiryTurns
	}
	return out
}

// Observation is one thing the session did, as the detector's input. It is a value, it carries no
// pointers into caller state, and the detector never retains the slice inside it.
type Observation struct {
	// Signature is the state this observation is an instance of.
	Signature StateSignature
	// Turn is when it happened. Every bound in this file is expressed in turns, so this is the only
	// notion of time the detector has.
	Turn core.TurnIndex
	// Progress is what was observed to change since the previous occurrence of Signature. It is the
	// field that keeps ordinary work from being called a loop, and a value outside the three
	// declared constants is treated as ProgressUnknown — an unrecognized progress report is missing
	// coverage, not an absence of change.
	Progress Progress
	// SelfOriginated marks an observation as Qompack's own injection, retrieval or warning activity
	// (contract §5.5). The caller sets it; IsSelfOriginated is the independent backstop.
	SelfOriginated bool
	// PartialCoverage marks an observation whose dependency coverage is incomplete even though
	// Progress is known. Contract §5 lists it as a second source of Uncertain, separate from
	// ProgressUnknown, so that "we watched everything and nothing changed" and "we watched some of
	// it" cannot be reported with the same confidence.
	PartialCoverage bool
	// Symbols is the action sequence this observation covers, used only to render the warning
	// through FormatWarning. It is optional: when empty the warning is rendered from the signature
	// itself (see stateExpansion). The detector never compares Symbols and never groups by them —
	// grouping is by Signature.Key alone, which is exactly the simpler baseline M6-G15-B's ablation
	// measures Sequitur against.
	Symbols []Symbol
}

// Outcome is what a delivered warning turned out to be worth, recorded by whoever can tell
// (contract §5.6). It is feedback about a warning already delivered and can never change what the
// detector will deliver: nothing in this file reads an Outcome back into a decision.
type Outcome uint8

const (
	// OutcomeUnknown is a delivered warning nobody has judged yet. It is the state every warning
	// starts in and the one most of them stay in.
	OutcomeUnknown Outcome = iota
	// OutcomeUseful means the warning named something real.
	OutcomeUseful
	// OutcomeFalseAlarm means it did not.
	OutcomeFalseAlarm
)

// String renders o for the M6-G15-A report and for logs.
func (o Outcome) String() string {
	switch o {
	case OutcomeUnknown:
		return "unknown"
	case OutcomeUseful:
		return "useful"
	case OutcomeFalseAlarm:
		return "false-alarm"
	default:
		return "invalid"
	}
}

// DetectorStats is the detector's own accounting, and its shape is contract §5.6's requirement
// rather than a convenience: usefulness and false alarms are counted SEPARATELY from raw
// repeated-action frequency.
//
// The separation is the point. RepeatedStates counts how often the session repeated a state, which
// is a property of the session and rises with any busy afternoon; Delivered counts how often this
// detector spoke; Useful and FalseAlarms are the only two numbers that say whether speaking was
// worth it. A component reported on by its own activity level will always look successful, and the
// M6-G15-A gate exists to stop that particular argument from being made here.
//
// Every field is a plain count with no averaging or weighting, so the report can compute its own
// ratios and disagree with any this package might have chosen.
type DetectorStats struct {
	// Observations is how many observations were handed to Observe, before any bound was applied.
	Observations int
	// RepeatedStates is raw repeated-action frequency: occurrences of a signature already seen in
	// the current window. It counts regardless of whether anything was ever warned about.
	RepeatedStates int
	// Ignored counts observations rejected as unusable input — presently the wholly empty signature,
	// which is a wiring bug rather than a state.
	Ignored int
	// SelfSuppressed counts observations excluded by bound 5.
	SelfSuppressed int
	// ProgressSuppressed counts observations whose window had observed progress (bound 2).
	ProgressSuppressed int
	// Deduplicated counts would-be warnings collapsed into one already delivered for the same
	// DedupKey (bound 4).
	Deduplicated int
	// CapSuppressed counts would-be warnings refused because MaxPerSession was already reached
	// (bound 4).
	CapSuppressed int
	// Expired counts delivery attempts refused because the warning was past ExpiresAt.
	Expired int
	// Delivered counts warnings Observe actually produced.
	Delivered int
	// Useful and FalseAlarms count judged outcomes; both are bounded by Delivered.
	Useful      int
	FalseAlarms int
}

// stateWindow is one signature's state inside one dedup window. Everything in it is a latch or a
// bounded list: the detector must not accumulate per-signature history without limit in a long
// session, which is why turns holds only the distinct turns of a single window (at most
// WindowTurns of them, by construction) and why windows older than the previous ordinal are pruned.
type stateWindow struct {
	// ordinal is the window this state belongs to; it is what pruning compares.
	ordinal int
	// occurrences counts observations of the signature in this window, including the first.
	occurrences int
	// turns are the distinct turns those occurrences fell on, in arrival order.
	turns []core.TurnIndex
	// symbols is the rendering hint from the most recent occurrence that supplied one.
	symbols []Symbol
	// progressObserved latches bound 2: once anything was observed to change in this window, the
	// window is a working loop and produces no warning at all. It LATCHES rather than resets the
	// count because a run that made progress once is a run that is moving, and re-arming on the next
	// unchanged observation would report the tail of ordinary work as a loop.
	progressObserved bool
	// unknownSeen latches bound 3: any window containing an unknown-coverage observation can only
	// produce an Uncertain warning, because part of the run it describes was never observed.
	unknownSeen bool
	// partialCoverage latches contract §5's second source of uncertainty.
	partialCoverage bool
}

// Detector turns a stream of Observations into at most a few advisory StateWarnings.
//
// It is NOT safe for concurrent use, deliberately. One session's observations arrive in turn order
// through one composition root, and a mutex here would buy nothing except the illusion that
// out-of-order concurrent observation is a supported shape — which it is not, since every bound in
// this file is defined over turns.
type Detector struct {
	cfg DetectorConfig
	// windows holds live per-signature state, keyed by DedupKey (signature key plus window
	// ordinal). Pruned to the last retainedWindows+1 ordinals on every advance.
	windows map[string]*stateWindow
	// warned records every DedupKey already delivered and what it turned out to be worth. It is
	// bounded by MaxPerSession by construction — an entry is only ever added when a warning is
	// delivered, and delivery stops at the cap — which is what lets it outlive pruning and stop a
	// late, out-of-order observation from re-warning about a window already reported.
	warned map[string]Outcome
	// maxOrdinal is the highest window ordinal seen, the reference point for pruning.
	maxOrdinal int
	// seenOrdinal records whether maxOrdinal means anything yet, since ordinal 0 is a real window.
	seenOrdinal bool
	delivered   int
	stats       DetectorStats
}

// NewDetector returns a Detector bounded by cfg, with cfg's zero values replaced by the shipped
// defaults. It never fails and does no I/O, matching every other computational constructor in this
// codebase (chunk.New, symbols.New, grammar.New).
func NewDetector(cfg DetectorConfig) *Detector {
	return &Detector{
		cfg:     cfg.normalized(),
		windows: make(map[string]*stateWindow, 8),
		warned:  make(map[string]Outcome, defaultMaxPerSession),
	}
}

// Config returns the bounds this Detector is actually running under, after normalization. The
// M6-G15-A report has to state the thresholds its numbers were produced at, and reading them back
// from the detector is the only way to be sure the report and the run agree.
func (d *Detector) Config() DetectorConfig { return d.cfg }

// Stats returns a copy of the detector's accounting (contract §5.6). It is a copy so a caller
// holding it across further observations cannot be surprised, and so nothing outside this file can
// write a count.
func (d *Detector) Stats() DetectorStats { return d.stats }

// Observe folds one observation into the detector and reports the warning it produced, if any.
//
// The order of the checks below is part of the contract, not an implementation detail, because each
// one attributes a suppression to a different bound and the counts are what M6-G15-A reads:
//
//  1. self-origination first, since a self-originated record is not input at all (bound 5) and
//     must not even contribute to repeat frequency, or a burst of retrieval results would show up
//     in the report as session repetition;
//  2. window bookkeeping next, so raw repeat frequency is counted whether or not anything is ever
//     warned about — that independence is precisely what bound 6 asks for;
//  3. progress suppression (bound 2) before dedup and cap, so a window that is legitimately making
//     progress is never charged against the session's warning budget;
//  4. the repeat threshold;
//  5. dedup before the cap (bound 4), so a repeat of an already-delivered warning cannot consume a
//     delivery slot that a genuinely new loop should get.
//
// The returned StateWarning owns its slices; the detector keeps no reference the caller can mutate.
func (d *Detector) Observe(o Observation) (StateWarning, bool) {
	d.stats.Observations++

	if o.SelfOriginated || IsSelfOriginated(o.Signature) {
		d.stats.SelfSuppressed++
		return StateWarning{}, false
	}
	if o.Signature == (StateSignature{}) {
		// A wholly empty signature is a wiring bug, not a state: it describes no goal, no target, no
		// action and no failure, so a warning about it could name nothing. Refusing it also keeps
		// stateExpansion's invariant — a warning always has at least one symbol to render — true by
		// construction rather than by a fallback nobody would ever read.
		d.stats.Ignored++
		return StateWarning{}, false
	}

	key := o.Signature.Key()
	ordinal := windowOrdinal(o.Turn, d.cfg.WindowTurns)
	dedupKey := dedupKeyFor(key, ordinal)
	d.advanceTo(ordinal)

	w := d.windows[dedupKey]
	if w == nil {
		w = &stateWindow{ordinal: ordinal}
		d.windows[dedupKey] = w
	} else {
		d.stats.RepeatedStates++
	}
	w.occurrences++
	if !slices.Contains(w.turns, o.Turn) {
		w.turns = append(w.turns, o.Turn)
	}
	if len(o.Symbols) > 0 {
		w.symbols = slices.Clone(o.Symbols)
	}
	switch o.Progress {
	case ProgressObserved:
		w.progressObserved = true
	case ProgressNone:
	default:
		// ProgressUnknown, and anything not in the declared set. An unrecognized progress value is
		// missing coverage rather than an absence of change, so it takes the cautious branch.
		w.unknownSeen = true
	}
	if o.PartialCoverage {
		w.partialCoverage = true
	}

	if w.progressObserved {
		d.stats.ProgressSuppressed++
		return StateWarning{}, false
	}
	if w.occurrences < d.cfg.MinRepeats {
		return StateWarning{}, false
	}
	if _, already := d.warned[dedupKey]; already {
		d.stats.Deduplicated++
		return StateWarning{}, false
	}
	if d.delivered >= d.cfg.MaxPerSession {
		d.stats.CapSuppressed++
		return StateWarning{}, false
	}

	warning := d.render(o.Signature, w, dedupKey, o.Turn)
	d.warned[dedupKey] = OutcomeUnknown
	d.delivered++
	d.stats.Delivered++
	return warning, true
}

// Deliverable reports whether w may still be shown at turn `at`, and is the enforcement point for
// StateWarning.ExpiresAt.
//
// Expiry is checked at DELIVERY rather than at production because those are different moments: the
// detector runs on PostToolUse and the warning is injected on the next UserPromptSubmit, and between
// them the session may have gone somewhere else entirely. It lives on the Detector rather than on
// StateWarning so that a refusal can be counted into Stats (bound 6 wants to know how many warnings
// were produced and then dropped) and so StateWarning keeps no method at all — see bound 1.
func (d *Detector) Deliverable(w StateWarning, at core.TurnIndex) bool {
	if at > w.ExpiresAt {
		d.stats.Expired++
		return false
	}
	return true
}

// RecordOutcome records what a delivered warning turned out to be worth and reports whether the
// record was accepted (contract §5.6).
//
// It accepts only a DedupKey this detector actually delivered, and only the first judgement for
// that key: an outcome that could be revised would make the false-alarm rate a function of who
// filed the last report. Feedback flows one way — into the counters, never back into a decision —
// so no sequence of outcomes can change which warnings a detector produces.
func (d *Detector) RecordOutcome(dedupKey string, o Outcome) bool {
	prev, delivered := d.warned[dedupKey]
	if !delivered || prev != OutcomeUnknown || o == OutcomeUnknown {
		return false
	}
	d.warned[dedupKey] = o
	switch o {
	case OutcomeUseful:
		d.stats.Useful++
	case OutcomeFalseAlarm:
		d.stats.FalseAlarms++
	}
	return true
}

// render builds the StateWarning for a window that has passed every bound. The Uncertain flag and
// the Progress field are derived from the window's latches, never from the single observation that
// happened to cross the threshold: bound 3 is about the run, so one confidently-unchanged
// observation at the end of a partially-unobserved run must not launder the whole run into a
// confident warning.
func (d *Detector) render(sig StateSignature, w *stateWindow, dedupKey string, at core.TurnIndex) StateWarning {
	progress := ProgressNone
	if w.unknownSeen {
		progress = ProgressUnknown
	}
	uncertain := w.unknownSeen || w.partialCoverage

	message := messageRepeatedState
	if uncertain {
		message = messageRepeatedStateUncertain
	}

	// Sorted so the rendered turn range and the record itself are independent of arrival order, and
	// cloned so the caller cannot reach back into the detector's window state.
	turns := slices.Clone(w.turns)
	slices.Sort(turns)

	return StateWarning{
		Signature: sig,
		Repeats:   w.occurrences,
		Turns:     turns,
		Progress:  progress,
		Uncertain: uncertain,
		DedupKey:  dedupKey,
		ExpiresAt: at + d.cfg.ExpiryTurns,
		Warning: Warning{
			Rule:    Rule{Expansion: stateExpansion(sig, w.symbols)},
			Repeats: w.occurrences,
			Message: message,
			Turns:   slices.Clone(turns),
		},
	}
}

// advanceTo moves the detector's window horizon to ordinal and drops state for windows older than
// the retention horizon.
//
// The prune is what makes "bounded window" a memory statement as well as a dedup statement: without
// it, a long session accumulates one live entry per distinct signature forever, and the component
// meant to save context would instead leak. Deleting from a map while ranging over it is defined in
// Go, and the outcome here does not depend on iteration order because the predicate is per-entry.
func (d *Detector) advanceTo(ordinal int) {
	if d.seenOrdinal && ordinal <= d.maxOrdinal {
		return
	}
	d.maxOrdinal = ordinal
	d.seenOrdinal = true
	horizon := ordinal - retainedWindows
	for k, w := range d.windows {
		if w.ordinal < horizon {
			delete(d.windows, k)
		}
	}
}

// windowOrdinal maps a turn onto its dedup window (contract §5.4: "the window ordinal").
//
// The floor correction matters for exactly one reason: Go's integer division truncates toward zero,
// so a negative turn would share ordinal 0 with turns 0..width-1 and silently produce a window twice
// the intended width. core.TurnIndex is documented as 0-based so no correct caller passes a negative
// turn, but a window whose width depends on the sign of its input is the kind of thing that is
// cheaper to make impossible than to explain in a bug report. width is always positive here, having
// been through DetectorConfig.normalized.
func windowOrdinal(t core.TurnIndex, width core.TurnIndex) int {
	q := int(t) / int(width)
	if t < 0 && t%width != 0 {
		q--
	}
	return q
}

// dedupKeyFor builds contract §5's DedupKey: the signature key plus the window ordinal. The two are
// joined with "#" rather than concatenated because a key and an ordinal that ran together would make
// gstate_abc + "12" and gstate_abc1 + "2" the same string, and a dedup key that can alias is a
// warning that can go missing.
func dedupKeyFor(key string, ordinal int) string {
	return key + "#" + strconv.Itoa(ordinal)
}

// stateExpansion chooses what FormatWarning renders as the repeated thing.
//
// The caller's own symbols win when it supplied them, because "Read→Edit→Bash" says more than any
// single field could. Otherwise the first non-empty field of the signature is used, in the order a
// reader would want to see: what was done, then to what, then toward what goal, then how it failed.
// Observe refuses the wholly empty signature, so this always returns at least one symbol and
// FormatWarning never renders an empty arrow chain.
func stateExpansion(sig StateSignature, symbols []Symbol) []Symbol {
	if len(symbols) > 0 {
		return slices.Clone(symbols)
	}
	for _, f := range [...]string{sig.Action, sig.Target, sig.Goal, sig.Failure} {
		if f != "" {
			return []Symbol{Symbol(f)}
		}
	}
	return nil
}
