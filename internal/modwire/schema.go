package modwire

import "reflect"

// SchemaDef is the contract as data: the input `devtool gen-mod-wire` renders into the mod's
// wire.gen.ts. A test holds it to the package's exported declarations, so nothing exported here
// can be missing from the TypeScript side.
type SchemaDef struct {
	Consts   []Const
	Enums    []Enum
	Messages []Message
	// Records are the Go-only record types. The mod never reads or writes them, so they are
	// listed for completeness and not rendered.
	Records []reflect.Type
}

// Const is one constant, with its Go name, its TypeScript name and its value.
type Const struct {
	Go, TS string
	Value  any
	Doc    string
}

// Enum is one string type with a closed set of values; it renders as a TypeScript union.
type Enum struct {
	TS      string
	Type    reflect.Type
	Members []Member
	Doc     string
}

// Member is one value of an Enum.
type Member struct {
	Go, TS, Value string
}

// Message is one request or response shape; it renders as a TypeScript type.
type Message struct {
	Type reflect.Type
	Doc  string
}

// Schema returns the whole contract in declaration order.
func Schema() SchemaDef {
	return SchemaDef{
		Consts: []Const{
			{"Version", "WIRE_VERSION", Version, "The wire version every request and response carries as `v`."},
			{
				"MinHostVersion", "MIN_HOST_VERSION", MinHostVersion,
				"The oldest Claude Code release the mod's features are enabled on (provisional). " +
					"Informational: the Go side is the only gate, on the hello and on every brief.",
			},
			{
				"BriefMaxChars", "BRIEF_MAX_CHARS", BriefMaxChars,
				"Default and ceiling for a brief's instructions, in UTF-16 code units (String length).",
			},
			{
				"BriefMarker", "BRIEF_MARKER", BriefMarker,
				"The heading line served, non-empty brief instructions begin with; its presence means a brief is already in.",
			},
			{
				"MaxMessageBytes", "MAX_MESSAGE_BYTES", MaxMessageBytes,
				"The largest request or response object, newline included.",
			},
			{"MaxSessionIDBytes", "MAX_SESSION_ID_BYTES", MaxSessionIDBytes, "The longest session id."},
			{"MaxHostVersionBytes", "MAX_HOST_VERSION_BYTES", MaxHostVersionBytes, "The longest host version string a hello or brief carries."},
			{"MaxTriggerBytes", "MAX_TRIGGER_BYTES", MaxTriggerBytes, "The longest trigger name."},
			{"MaxFactIDs", "MAX_FACT_IDS", MaxFactIDs, "The most fact ids one brief response lists."},
			{"MaxFactIDBytes", "MAX_FACT_ID_BYTES", MaxFactIDBytes, "The longest fact id."},
			{"MaxDetailBytes", "MAX_DETAIL_BYTES", MaxDetailBytes, "The longest status detail."},
			{
				"SessionIDChars", "SESSION_ID_CHARS", SessionIDChars,
				"The bytes a session id may hold, as a regular-expression character-class body.",
			},
			{
				"TriggerChars", "TRIGGER_CHARS", TriggerChars,
				"The bytes a trigger name may hold, as a regular-expression character-class body.",
			},
			{
				"FactIDChars", "FACT_ID_CHARS", FactIDChars,
				"The bytes a fact id may hold, as a regular-expression character-class body; each encodes as itself in JSON.",
			},
			{
				"TriggerManual", "TRIGGER_MANUAL", TriggerManual,
				"A compaction the person asked for; its brief closes the open segment first.",
			},
			{
				"TriggerAuto", "TRIGGER_AUTO", TriggerAuto,
				"A compaction the host started on its own; its brief closes the open segment first.",
			},
			{
				"TriggerPlugin", "TRIGGER_PLUGIN", TriggerPlugin,
				"A compaction a plugin started; its brief closes the open segment first.",
			},
			{
				"TriggerPrecompute", "TRIGGER_PRECOMPUTE", TriggerPrecompute,
				"A summary computed ahead of a compaction; its brief, like any other trigger's, leaves the open segment open.",
			},
			{
				"TimeoutMs", "TIMEOUT_MS", TimeoutMs,
				"How long the mod waits for one `qompack mod <verb>` child, in milliseconds (provisional).",
			},
			{
				"DaemonDeadlineSlackMs", "DAEMON_DEADLINE_SLACK_MS", DaemonDeadlineSlackMs,
				"How much earlier than TIMEOUT_MS the daemon-side deadline falls, in milliseconds.",
			},
			{"StateDir", "STATE_DIR", StateDir, "The store directory that holds the per-session records."},
		},
		Enums: []Enum{
			{
				TS: "Verb", Type: reflect.TypeFor[Verb](), Doc: "A `qompack mod` subcommand.",
				Members: []Member{
					{"VerbHello", "VERB_HELLO", string(VerbHello)},
					{"VerbBrief", "VERB_BRIEF", string(VerbBrief)},
				},
			},
			{
				TS: "Reason", Type: reflect.TypeFor[Reason](), Doc: "Why a verb was not served; set exactly when `ok` is false.",
				Members: []Member{
					{"ReasonCold", "REASON_COLD", string(ReasonCold)},
					{"ReasonBusy", "REASON_BUSY", string(ReasonBusy)},
					{"ReasonTimeout", "REASON_TIMEOUT", string(ReasonTimeout)},
					{"ReasonRefused", "REASON_REFUSED", string(ReasonRefused)},
					{"ReasonError", "REASON_ERROR", string(ReasonError)},
				},
			},
			{
				TS: "Outcome", Type: reflect.TypeFor[Outcome](), Doc: "What one brief call came to, as its record logs it.",
				Members: []Member{
					{"OutcomeServed", "OUTCOME_SERVED", string(OutcomeServed)},
					{"OutcomeCold", "OUTCOME_COLD", string(OutcomeCold)},
					{"OutcomeBusy", "OUTCOME_BUSY", string(OutcomeBusy)},
					{"OutcomeTimeout", "OUTCOME_TIMEOUT", string(OutcomeTimeout)},
					{"OutcomeRefused", "OUTCOME_REFUSED", string(OutcomeRefused)},
					{"OutcomeError", "OUTCOME_ERROR", string(OutcomeError)},
				},
			},
		},
		Messages: []Message{
			{reflect.TypeFor[Status](), "Opens every response."},
			{reflect.TypeFor[HelloRequest](), "The stdin of `qompack mod hello`."},
			{reflect.TypeFor[HelloResponse](), "The stdout of `qompack mod hello`."},
			{reflect.TypeFor[BriefRequest](), "The stdin of `qompack mod brief`."},
			{reflect.TypeFor[BriefResponse](), "The stdout of `qompack mod brief`."},
		},
		Records: []reflect.Type{reflect.TypeFor[BriefRecord](), reflect.TypeFor[HelloFile]()},
	}
}
