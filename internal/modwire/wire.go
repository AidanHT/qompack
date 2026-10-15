package modwire

// Version is the wire version every request and response carries as "v".
const Version = 1

// MinHostVersion is the oldest Claude Code release the mod's features are enabled on. It is the
// release installed when the contract was built, a provisional minimum: the live checks that
// verify the mods surface against a pinned build confirm it or raise it. Below it the mod stays
// out of the way and the classic hooks run as before.
//
// The Go side is the only gate (MeetsMinHostVersion, applied when it serves a hello). The copy
// of this value in the mod's generated TypeScript is informational: the mod acts on the hello's
// `enabled` field, never on its own comparison.
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

	// MaxHostVersionBytes bounds the host version string a hello carries.
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

// Timing. The mod passes TimeoutMs to the host's process API for every verb; the daemon answers a
// verb within TimeoutMs - DaemonDeadlineSlackMs, so the CLI can still report a timeout before the
// mod gives up on the child. Both are provisional until measured on a warm daemon with a
// realistic backlog.
const (
	// TimeoutMs is how long the mod waits for one `qompack mod <verb>` child, in milliseconds.
	TimeoutMs = 2000

	// DaemonDeadlineSlackMs is how much earlier than TimeoutMs the daemon-side deadline falls.
	DaemonDeadlineSlackMs = 500
)

// Verb is a `qompack mod` subcommand.
type Verb string

// The Stage A verbs.
const (
	// VerbHello announces a mod session: the host version and the session id. When the project
	// is served it writes the hello file and says, in `enabled`, whether the mod's features are
	// on for this host; when it is refused it writes nothing.
	VerbHello Verb = "hello"

	// VerbBrief asks for compaction instructions before a compaction runs.
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

	// ReasonRefused means the project is not served: its root is the home directory, it has no
	// .qompack store, its runtime mode is off (as the hooks read it), or runtime.mod.enabled is
	// false. Nothing is written or spawned for it: no daemon, no store, no hello file and no brief
	// record. This is the only encoding of "switched off"; a host that is merely too old gets a
	// served hello with enabled false.
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
type HelloResponse struct {
	Status
	// Enabled is MeetsMinHostVersion(HostVersion) on a served hello, and always false on an
	// unserved one. The mod calls brief only after a hello answered enabled true.
	Enabled bool `json:"enabled"`
}

// BriefRequest is the stdin of `qompack mod brief`, sent before a compaction runs.
type BriefRequest struct {
	V         int    `json:"v"`
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	// Trigger is what is compacting, as the host names it (manual, auto, and so on). It is
	// logged, not interpreted: any plain identifier (TriggerChars, at most MaxTriggerBytes) is
	// served and recorded, so a trigger the host adds later is too. Only a malformed one is
	// refused.
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
	// Instructions is the text the mod adds to the compaction's instructions.
	Instructions string `json:"instructions"`
	// FactIDs names the stored facts the instructions were built from. They are returned to the
	// mod with the instructions; no Stage A path records them anywhere else, and a later stage may.
	FactIDs []string `json:"fact_ids"`
}
