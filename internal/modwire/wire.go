package modwire

// Version is the wire version every request and response carries as "v".
const Version = 1

// MinHostVersion is the oldest Claude Code release the mod's features are enabled on. It is the
// release installed when the contract was built, a provisional minimum: the live checks that
// verify the mods surface against a pinned build confirm it or raise it. Below it the mod stays
// out of the way and the classic hooks run as before.
//
// The Go side is the only gate (MeetsMinHostVersion, applied when it serves a hello and to every
// brief). The copy of this value in the mod's generated TypeScript is informational: the mod acts
// on the hello's `enabled` and `steering` fields, never on its own comparison.
const MinHostVersion = "2.1.300"

// Limits. Each one bounds something the other side cannot be trusted to bound for itself.
const (
	// BriefMaxChars is both the default and the ceiling for a brief's instructions, in CharCount
	// units. A request's max_chars of 0 means this value; a larger one is clamped to it.
	BriefMaxChars = 4000

	// MaxMessageBytes bounds one request or response object, newline included. Decode refuses a
	// larger input and Encode refuses to produce a larger output, so neither side ever depends
	// on how the host's process API truncates a long stdout.
	MaxMessageBytes = 64 << 10

	// MaxSessionIDBytes bounds a session id. Session ids name per-session files, so they are
	// also restricted to a filename-safe alphabet (ValidSessionID).
	MaxSessionIDBytes = 128

	// MaxHostVersionBytes bounds the host version string a hello or a brief carries.
	MaxHostVersionBytes = 128

	// MaxTriggerBytes bounds a brief's trigger name.
	MaxTriggerBytes = 32

	// MaxFactIDs bounds how many fact ids one brief response lists.
	MaxFactIDs = 256

	// MaxFactIDBytes bounds one fact id.
	MaxFactIDBytes = 128

	// MaxDetailBytes bounds a status's detail, the one free-text field a response carries.
	MaxDetailBytes = 256
)

// BriefMarker is the heading line a brief's served, non-empty instructions begin with. The mod adds
// those instructions to a compaction's, so the marker lets it tell instructions it already briefed
// (a second session.compact dispatch, or a chain that ran it twice) and skip a second brief. Both
// sides refuse a served brief whose instructions do not begin with it (BriefResponse.Validate, and
// the mod's decoder). It is printable ASCII without quotes, backslashes or a newline, so it is the
// same text in Go, in JSON and in the generated TypeScript, and it reads to the summarizer as an
// ordinary heading. It counts toward the instructions' cap like any other text.
const BriefMarker = "Qompack - reproduce these items verbatim:"

// Alphabets, as regular-expression character-class bodies, so the Go validators and the mod
// build their checks from the same text. All three are ASCII, so a byte count is a character
// count and nothing is lost to U+FFFD.
const (
	// SessionIDChars is what a session id may hold: it names per-session files.
	SessionIDChars = "A-Za-z0-9._-"

	// TriggerChars is what a trigger name may hold. Any plain identifier is accepted, so a
	// trigger the host adds later is served and recorded, not refused.
	TriggerChars = "A-Za-z0-9_-"

	// FactIDChars is what a fact id may hold. Every byte here encodes as itself in JSON, with
	// encoding/json's HTML escaping on (which is why < > & are absent), so MaxFactIDs ids of
	// MaxFactIDBytes cost exactly that many bytes on the wire. The stored facts' own ids fit
	// unchanged: decision ids (dec_…), pinned-invariant ids (inv_…), negative-knowledge record
	// ids (elim_…), content hashes (sha256:…) and host tool-use ids.
	FactIDChars = "A-Za-z0-9._:/-"
)

// The compaction triggers the host names (its session.compact event's trigger). A brief for
// manual, auto or plugin closes the session's open segment before it reads the store
// (ClosesSegment); a brief for precompute, or for any other well-formed trigger, leaves the segment
// open, has no segment side effect, and is still served. Malformed triggers are refused
// (TriggerChars, MaxTriggerBytes).
const (
	// TriggerManual is a compaction the person asked for.
	TriggerManual = "manual"
	// TriggerAuto is a compaction the host started on its own.
	TriggerAuto = "auto"
	// TriggerPlugin is a compaction a plugin started.
	TriggerPlugin = "plugin"
	// TriggerPrecompute is a summary the host computes ahead of a compaction and keeps for the
	// compaction that comes; the conversation goes on, so the open segment stays open.
	TriggerPrecompute = "precompute"
)

// ClosesSegment reports whether a brief for this trigger closes the session's open segment before
// it reads: true for TriggerManual, TriggerAuto and TriggerPlugin, exactly as spelled, and false
// for everything else.
func ClosesSegment(trigger string) bool {
	switch trigger {
	case TriggerManual, TriggerAuto, TriggerPlugin:
		return true
	}
	return false
}

// Timing, in three layers, each inside the one around it. The mod passes TimeoutMs to the host's
// process API for every verb, and the host kills a child that outlives it. A verb that asks the
// daemon (brief) waits for the daemon's answer for TimeoutMs, less the time since the CLI process
// started, less RecordMarginMs, so it can still write its record and its answer before the mod gives
// up on it. It sends the daemon a budget of that reply wait less DaemonReplyMarginMs, and the daemon
// answers within that budget of receiving the request. The CLI's start-up time varies, so the budget
// is computed for each request rather than fixed; a budget that is not positive is a timeout the CLI
// records without asking the daemon. TimeoutMs is a floor: measurement on a warm daemon with a
// realistic backlog may raise it, never lower it.
const (
	// TimeoutMs is how long the mod waits for one `qompack mod <verb>` child, in milliseconds.
	TimeoutMs = 2000

	// RecordMarginMs is how much of TimeoutMs the CLI keeps, after its reply wait, to write the
	// brief's record and its answer, in milliseconds.
	RecordMarginMs = 100

	// DaemonReplyMarginMs is how much earlier than the end of the CLI's reply wait the daemon's
	// budget runs out, in milliseconds, so its answer reaches the CLI before the CLI stops waiting.
	DaemonReplyMarginMs = 200
)

// Verb is a `qompack mod` subcommand.
type Verb string

// The Stage A verbs.
const (
	// VerbHello announces a mod session: the host version and the session id. When the project
	// is served it writes the hello file and says, in `enabled`, whether the mod's features are
	// on for this host; when it is refused it writes nothing.
	VerbHello Verb = "hello"

	// VerbBrief asks for compaction instructions before a compaction runs. The mod sends it only
	// after a hello answered steering true.
	VerbBrief Verb = "brief"
)

// Verbs lists every verb in declaration order.
func Verbs() []Verb { return []Verb{VerbHello, VerbBrief} }

// Reason says why a verb was not served. It is set exactly when ok is false.
type Reason string

// The reasons, one per distinct unserved outcome.
const (
	// ReasonCold means no daemon was listening. The CLI starts the detached daemon and returns
	// at once, without waiting for it.
	ReasonCold Reason = "cold"

	// ReasonBusy means a daemon exists but did not accept the connection within the deadline.
	ReasonBusy Reason = "busy"

	// ReasonTimeout means the daemon accepted but did not answer within its deadline.
	ReasonTimeout Reason = "timeout"

	// ReasonRefused means the verb is not served for this project. Every verb is refused when the
	// project root is the home directory, it has no .qompack store, runtime.mod.enabled is false,
	// or its runtime mode is off (as the hooks read it). A brief is also refused when
	// runtime.mod.steering is false, or when its host_version is below MinHostVersion or
	// unreadable (MeetsMinHostVersion). Nothing is written or spawned for a refused verb: no
	// daemon, no store, no hello file and no brief record. This is the only encoding of "switched
	// off"; a hello from a host that is merely too old is served with enabled false, so its
	// version is on record.
	ReasonRefused Reason = "refused"

	// ReasonError is anything else, including a malformed or unsupported request.
	ReasonError Reason = "error"
)

// Reasons lists every reason in declaration order.
func Reasons() []Reason {
	return []Reason{ReasonCold, ReasonBusy, ReasonTimeout, ReasonRefused, ReasonError}
}

// Outcome is what one brief call came to, as the brief-outcome record logs it.
type Outcome string

// The outcomes: served, or the reason it was not, spelled identically.
const (
	// OutcomeServed means the brief was produced and returned.
	OutcomeServed Outcome = "served"
	// OutcomeCold mirrors ReasonCold.
	OutcomeCold Outcome = "cold"
	// OutcomeBusy mirrors ReasonBusy.
	OutcomeBusy Outcome = "busy"
	// OutcomeTimeout mirrors ReasonTimeout.
	OutcomeTimeout Outcome = "timeout"
	// OutcomeRefused mirrors ReasonRefused. A refused call writes nothing, so the log itself never
	// carries it; it exists so OutcomeFor maps every reason.
	OutcomeRefused Outcome = "refused"
	// OutcomeError mirrors ReasonError.
	OutcomeError Outcome = "error"
)

// Outcomes lists every outcome in declaration order.
func Outcomes() []Outcome {
	return []Outcome{OutcomeServed, OutcomeCold, OutcomeBusy, OutcomeTimeout, OutcomeRefused, OutcomeError}
}

// OutcomeFor maps a response status to the outcome its record logs. An unrecognised reason is
// logged as an error rather than dropped.
func OutcomeFor(s Status) Outcome {
	if s.OK {
		return OutcomeServed
	}
	if knownReason(s.Reason) {
		return Outcome(s.Reason)
	}
	return OutcomeError
}

// Status opens every response: the wire version, whether the verb was served, and when it was
// not, the reason and an optional one-line detail for logs. Detail never carries content.
type Status struct {
	V      int    `json:"v"`
	OK     bool   `json:"ok"`
	Reason Reason `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Served is the status of a served verb.
func Served() Status { return Status{V: Version, OK: true} }

// Failure is the status of an unserved verb.
func Failure(r Reason, detail string) Status { return Status{V: Version, Reason: r, Detail: detail} }

// HelloRequest is the stdin of `qompack mod hello`, sent once when a mod session starts.
type HelloRequest struct {
	V int `json:"v"`
	// SessionID is the host's session id; it names the hello file.
	SessionID string `json:"session_id"`
	// CWD is the session's root as the host reports it. The CLI resolves the project root from
	// it the way the classic hooks do; the mod never decides the project root itself.
	CWD string `json:"cwd"`
	// HostVersion is the host's full version string.
	HostVersion string `json:"host_version"`
}

// HelloResponse is the stdout of `qompack mod hello`. Its two "off" answers are distinct:
//
//   - ok false, reason refused: the project is not served (ReasonRefused); nothing is written.
//   - ok true, enabled false: the project is served (it has a store, its runtime mode is not off
//     and runtime.mod.enabled is on) and the hello file is written, recording the host version
//     for diagnosis, but the host does not meet MinHostVersion or its version is unreadable
//     (MeetsMinHostVersion).
//
// A served, enabled hello then says in Steering whether the mod may steer compactions with a
// brief. Steering true implies Enabled true; Validate refuses any other combination.
type HelloResponse struct {
	Status
	// Enabled is MeetsMinHostVersion(HostVersion) on a served hello, and always false on an
	// unserved one.
	Enabled bool `json:"enabled"`
	// Steering is true when the hello is served with Enabled true and runtime.mod.steering is on,
	// and false otherwise. The mod calls brief only after a hello answered steering true; a brief
	// sent while runtime.mod.steering is off is refused.
	Steering bool `json:"steering"`
}

// BriefRequest is the stdin of `qompack mod brief`, sent before a compaction runs.
type BriefRequest struct {
	V         int    `json:"v"`
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	// HostVersion is the host's full version string, as `$.session.version()` reports it. The Go
	// side applies MeetsMinHostVersion to every brief, not only to the hello before it: a brief
	// from a host below MinHostVersion, or with an unreadable version, is refused. Decoding
	// requires only that it is present and bounded (1 to MaxHostVersionBytes bytes).
	HostVersion string `json:"host_version"`
	// Trigger is what is compacting, as the host names it. TriggerManual, TriggerAuto and
	// TriggerPlugin make the brief close the session's open segment before it reads
	// (ClosesSegment); TriggerPrecompute and any other well-formed trigger leave the segment open,
	// with no segment side effect, and are still served and recorded, so a trigger the host adds
	// later is too. Any plain identifier is well formed (TriggerChars, at most MaxTriggerBytes);
	// only a malformed one is refused.
	Trigger string `json:"trigger"`
	// UserInstructionsPresent says whether the person gave their own compaction instructions.
	// Only the fact is sent, never the text.
	UserInstructionsPresent bool `json:"user_instructions_present"`
	// MaxChars caps the instructions, in CharCount units: 0 means BriefMaxChars, and a larger
	// value is clamped to it.
	MaxChars int `json:"max_chars"`
}

// EffectiveMaxChars is the cap the CLI applies to this request's instructions.
func (r BriefRequest) EffectiveMaxChars() int {
	if r.MaxChars <= 0 || r.MaxChars > BriefMaxChars {
		return BriefMaxChars
	}
	return r.MaxChars
}

// BriefResponse is the stdout of `qompack mod brief`. Instructions and FactIDs are always
// present; both are empty when the brief was not served.
type BriefResponse struct {
	Status
	// Instructions is the text the mod adds to the compaction's instructions. When served and
	// non-empty it begins with BriefMarker; a served brief with nothing to say is empty.
	Instructions string `json:"instructions"`
	// FactIDs names the stored facts the instructions were built from. They are returned to the
	// mod with the instructions; no Stage A path records them anywhere else, and a later stage may.
	FactIDs []string `json:"fact_ids"`
}
