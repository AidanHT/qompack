package commands

import (
	"encoding/json"
	"errors"
	"fmt"
)

// EnvelopeSchema is the version of the JSON document every command writes under --json.
//
// It is a single integer rather than a semver string because the only question a reader ever has
// to answer is "can I interpret this?", and the answer is a comparison. It is bumped when a member
// changes meaning or disappears — never when one is added, since an added member is invisible to
// an older reader that does not look for it.
const EnvelopeSchema = 1

// Exit codes, mirroring the 00-ARCHITECTURE.md §2.3 policy table.
//
// They are declared here rather than read from internal/cli because the dependency runs the other
// way: cli imports commands to route the seven §7.5 names, so commands cannot import cli back.
// cli.ExitOK/ExitError/ExitUsage remain the values the process actually returns; TestExitCodes_
// MatchCLI in internal/cli pins the two tables together.
const (
	// ExitOK is success.
	ExitOK = 0
	// ExitError is a command that ran and could not answer.
	ExitError = 1
	// ExitUsage is a malformed invocation: an unknown flag, a missing required argument.
	ExitUsage = 2
)

// ErrUsage marks an invocation the user got wrong, which is the only condition that earns exit 2.
//
// The distinction is load-bearing rather than cosmetic. "You typed this wrong" and "I could not
// find out" are different answers, and reporting the second as the first sends a user to re-read
// the help for a command they had already typed correctly.
var ErrUsage = errors.New("qompack: usage")

// ErrUnsupported marks a payload this build cannot interpret — an envelope from a newer schema,
// a status section whose shape it does not recognize.
//
// It is deliberately NOT a usage error and deliberately not success: §"Rollout, rollback and
// blockers" forbids falling back to the old absent-on-error reading, in which unreadable input
// silently becomes a zero value that renders as a confident, wrong answer.
var ErrUnsupported = errors.New("qompack: unsupported schema")

// UsageErrorf builds a usage error that carries its own message and still matches ErrUsage under
// errors.Is, so the CLI maps it to exit 2 without matching on strings.
func UsageErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUsage, fmt.Sprintf(format, args...))
}

// ExitCode maps a command's returned error to its §2.3 exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, ErrUsage):
		return ExitUsage
	default:
		return ExitError
	}
}

// ErrorKind names why a command could not answer. The four are distinct because they call for
// four different next actions from whoever reads them.
type ErrorKind string

const (
	// ErrorKindUsage: the invocation was malformed. Re-read the help.
	ErrorKindUsage ErrorKind = "usage"
	// ErrorKindUnavailable: the answer exists somewhere but this build could not observe it —
	// no daemon, an unreadable spool, a dependency not built in this wave. NOT a zero value.
	ErrorKindUnavailable ErrorKind = "unavailable"
	// ErrorKindUnsupported: the payload was found and could not be interpreted.
	ErrorKindUnsupported ErrorKind = "unsupported"
	// ErrorKindFailed: the command ran and the underlying operation returned an error.
	ErrorKindFailed ErrorKind = "failed"
)

// Envelope is the stable JSON document every command writes under --json.
//
// Data is raw so a frontend can pass through bytes its owning API already marshalled rather than
// decoding and re-encoding them, which is what keeps SP-13's fidelity and coverage members intact
// on the way out instead of flattening them into whatever struct this package happened to declare.
type Envelope struct {
	Schema  int             `json:"schema"`
	Command string          `json:"command"`
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   *EnvelopeError  `json:"error,omitempty"`
}

// EnvelopeError is the failure member. It is a pointer on Envelope so a successful document omits
// it entirely rather than carrying an empty object a reader has to inspect.
type EnvelopeError struct {
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"message"`
}

// NewEnvelope returns a successful envelope for name at the current schema.
func NewEnvelope(name string) *Envelope {
	return &Envelope{Schema: EnvelopeSchema, Command: name, OK: true}
}

// Fail marks the envelope unsuccessful with a kind and a message, clearing OK.
//
// It does not clear Data: a partial answer plus an explicit failure is more useful than either
// alone, and it is the shape the status command needs when one section of many is missing.
func (e *Envelope) Fail(kind ErrorKind, format string, args ...any) {
	e.OK = false
	e.Error = &EnvelopeError{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// FailErr marks the envelope unsuccessful, classifying err into its kind.
func (e *Envelope) FailErr(err error) {
	e.Fail(KindOf(err), "%s", err.Error())
}

// KindOf classifies an error into the ErrorKind a reader should act on.
func KindOf(err error) ErrorKind {
	switch {
	case err == nil:
		return ErrorKindFailed
	case errors.Is(err, ErrUsage):
		return ErrorKindUsage
	case errors.Is(err, ErrUnsupported):
		return ErrorKindUnsupported
	default:
		return ErrorKindFailed
	}
}

// DecodeEnvelope reads an envelope written by this or an older build.
//
// A schema this build does not know is ErrUnsupported, never a partially-populated value: the
// members a newer writer added would decode as zeroes here, and a zero is indistinguishable from
// an observed zero once it has been rendered.
func DecodeEnvelope(b []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("decoding command envelope: %w", err)
	}
	if env.Schema > EnvelopeSchema {
		return Envelope{}, fmt.Errorf("%w: envelope schema %d, this build reads %d",
			ErrUnsupported, env.Schema, EnvelopeSchema)
	}
	return env, nil
}
