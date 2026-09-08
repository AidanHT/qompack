package grammar

import (
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// Symbol is one token of the action stream Sequitur folds into a grammar: a tool name, the
// literal "user" for a user turn, or a test-outcome marker such as "test:pass"/"test:fail"
// (00-ARCHITECTURE.md §5.11).
type Symbol string

// RuleID identifies one grammar rule Sequitur has induced.
type RuleID int

// Rule is one induced grammar rule: its own ID, the digram (or longer body) it was formed from,
// how many times it has been used, and its full expansion back to terminal Symbols.
type Rule struct {
	// ID identifies this rule.
	ID RuleID
	// Body is the rule's immediate right-hand side: a sequence of Symbols and/or other rules'
	// references, exactly as Sequitur's grammar induction produced it.
	Body []Symbol
	// Uses counts how many times this rule has been referenced.
	Uses int
	// Expansion is Body fully expanded back to terminal Symbols.
	Expansion []Symbol
	// Span is the number of terminal Symbols Expansion covers.
	Span int
}

// Warning is one loop-detection result: the thrashing Rule, how many times it repeated, the
// turns it spanned, and a human-readable message. FormatWarning renders it for injection through
// UserPromptSubmit.
type Warning struct {
	// Rule is the thrashing rule.
	Rule Rule
	// Repeats is how many times Rule's expansion repeated.
	Repeats int
	// Message is a short, human-readable suggestion.
	Message string
	// Turns lists the turn indices the repeats spanned.
	Turns []core.TurnIndex
}

// ----------------------------------------------------------------------------------------------
// SP-15 state-aware warnings. Warning and FormatWarning above are FROZEN — formatwarning_test.go
// and SP-08's UserPromptSubmit injection assert their wording byte for byte — so the state-aware
// layer is added alongside them and renders THROUGH FormatWarning rather than replacing it. The
// frozen contract these declarations implement is plans/sdd/V5-SP-15/contract.md §5.
// ----------------------------------------------------------------------------------------------

// CodecVersion is the version byte MarshalBinary writes and UnmarshalBinary accepts up to
// (contract §4). A reader that meets a HIGHER version reports core.ErrDegraded and leaves its
// receiver untouched: refusing to read forward is the compatibility behaviour, and quietly
// yielding an empty grammar instead would look like a session with no repeated actions in it.
const CodecVersion = 1

// ruleRefPrefix is what distinguishes a rule REFERENCE from a terminal Symbol inside a Rule.Body
// or a Compressed() sequence. Rule.Body is documented as "a sequence of Symbols and/or other
// rules' references", and Symbol is a string, so the two have to share one space.
//
// NUL is the separator because Symbol's real inhabitants are tool names, the literal "user" and
// test-outcome markers such as "test:fail" — none of which can contain a NUL byte. A printable
// sigil like "R" or "$" could collide with a tool named that; this cannot.
const ruleRefPrefix = "\x00R"

// RuleRef renders id as the Symbol that references it. RuleRef and ParseRuleRef are declared here,
// in Main's shared types, rather than in either implementation file, because the grammar core and
// the codec both have to agree on the encoding and neither owns the other's file.
func RuleRef(id RuleID) Symbol { return Symbol(ruleRefPrefix + strconv.Itoa(int(id))) }

// ParseRuleRef reports whether s is a rule reference and, if so, which rule it names. A Symbol
// that merely starts with the prefix but carries an unparseable remainder is NOT a reference: it
// is returned as not-a-reference rather than as rule zero, so a corrupt or hand-written stream
// degrades into an odd-looking terminal instead of silently aliasing a real rule.
func ParseRuleRef(s Symbol) (RuleID, bool) {
	rest, ok := strings.CutPrefix(string(s), ruleRefPrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false
	}
	return RuleID(n), true
}

// Snapshot is an induced grammar's complete serializable state (contract §4). It is the seam
// between the grammar core and the codec: the core produces and restores a Snapshot, the codec
// turns one into bytes and back, and neither has to read the other's file to do it.
type Snapshot struct {
	// Rules are every rule in the grammar, ordered by ID ascending.
	Rules []Rule
	// Sequence is the top-level sequence, terminals and rule references interleaved.
	Sequence []Symbol
	// NextID is the ID the next induced rule will take, so a restored grammar keeps minting
	// fresh IDs rather than reusing one a restored rule already holds.
	NextID RuleID
}

// StateSignature is the four-part description of what a session was trying to do at one moment:
// its goal, the target it was acting on, the action it took, and how that action failed
// (contract §5).
//
// It exists because a repeated ACTION is not a loop. Running the same test command twice while the
// file under it changes is ordinary progress; running it twice against an unchanged file after an
// unchanged failure is the thing worth a warning. Comparing signatures rather than action
// frequency is what separates the two, and it is also the simpler baseline Sequitur has to beat in
// M6-G15-B's ablation before any further grammar machinery earns its place.
type StateSignature struct {
	// Goal is the canonicalized goal in force.
	Goal string
	// Target is what the action was applied to, normally a paths.Key-form path.
	Target string
	// Action is the canonicalized action, normally a tool name.
	Action string
	// Failure is the canonicalized failure signature, empty when the action did not fail.
	Failure string
}

// Progress is what was observed to change between two occurrences of one StateSignature
// (contract §5). It is the field that keeps ordinary work from being reported as a loop.
type Progress uint8

const (
	// ProgressNone means nothing observably changed: same files, same environment, same failure.
	ProgressNone Progress = iota
	// ProgressObserved means something did change — a file, the environment, or the failure
	// signature — which SUPPRESSES the warning. An edit-test-edit loop that is moving is not a
	// loop, and treating it as one is the false positive that would make the whole feature a
	// liability.
	ProgressObserved
	// ProgressUnknown means observation coverage is missing for this window, so neither change nor
	// stasis can be established. It may only ever produce an Uncertain warning.
	ProgressUnknown
)

// String renders p for logs and the usefulness/false-alarm report.
func (p Progress) String() string {
	switch p {
	case ProgressNone:
		return "none"
	case ProgressObserved:
		return "observed"
	case ProgressUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

// StateWarning is one bounded, deduplicated, WARNING-ONLY loop report (contract §5).
//
// Warning-only is the whole disposition and it is not a starting position to be tightened later:
// a StateWarning creates no elimination, no prohibition and no binding constraint. It says what it
// observed and lets the session decide. Everything else here is a bound — Uncertain qualifies it,
// DedupKey collapses it, ExpiresAt ends it — because an unbounded warning stream is itself a way
// to spend the context this system exists to save.
type StateWarning struct {
	// Signature is the repeated state.
	Signature StateSignature
	// Repeats is how many times Signature recurred inside the window.
	Repeats int
	// Turns lists the turn indices the repeats spanned.
	Turns []core.TurnIndex
	// Progress is what was observed to change across the repeats.
	Progress Progress
	// Uncertain marks a warning that must be read as a question rather than a finding: set for
	// ProgressUnknown and for partial dependency coverage.
	Uncertain bool
	// DedupKey collapses repeats of one warning inside a bounded window.
	DedupKey string
	// ExpiresAt is the turn after which this warning is no longer delivered.
	ExpiresAt core.TurnIndex
	// Warning is the renderable form, which FormatWarning turns into the frozen one-line text.
	Warning Warning
}
