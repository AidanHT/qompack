package modwire

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// StateDir is the directory, inside a project's .qompack store, that holds both records.
const StateDir = "state"

// The record file names are these prefixes, the session id, and these suffixes.
const (
	briefRecordPrefix = "mod-brief-"
	briefRecordSuffix = ".jsonl"
	helloFilePrefix   = "mod-hello-"
	helloFileSuffix   = ".json"
)

// BriefRecordName is the base name of a session's brief-outcome log, mod-brief-<session>.jsonl,
// inside StateDir. It refuses a session id ValidSessionID rejects.
func BriefRecordName(session string) (string, error) {
	if !ValidSessionID(session) {
		return "", fmt.Errorf("%w: session id %q cannot name a file", ErrInvalid, session)
	}
	return briefRecordPrefix + session + briefRecordSuffix, nil
}

// HelloFileName is the base name of a session's hello file, mod-hello-<session>.json, inside
// StateDir. It refuses a session id ValidSessionID rejects.
func HelloFileName(session string) (string, error) {
	if !ValidSessionID(session) {
		return "", fmt.Errorf("%w: session id %q cannot name a file", ErrInvalid, session)
	}
	return helloFilePrefix + session + helloFileSuffix, nil
}

// BriefRecord is one line of a session's brief-outcome log. The CLI appends one for every brief
// call, served or not, except a refused one (ReasonRefused: a home root, no store, either mod
// switch off, runtime mode off, or a host below MinHostVersion), for which nothing is written and
// no store is created. Its field set is closed and carries no content: what was asked, what it
// came to, how much and how long. Like a message it carries "v": Version, and a line whose v is
// absent or another version is refused.
type BriefRecord struct {
	V int `json:"v"`
	// TS is when the call finished.
	TS core.UnixMilli `json:"ts"`
	// Trigger is the request's trigger.
	Trigger string `json:"trigger"`
	// Outcome is OutcomeFor of the response.
	Outcome Outcome `json:"outcome"`
	// Chars is CharCount of the served instructions; 0 when not served.
	Chars int `json:"chars"`
	// MS is the call's wall time in milliseconds.
	MS int64 `json:"ms"`
}

func (r BriefRecord) validate() error {
	if r.V != Version {
		return &VersionError{Got: r.V}
	}
	known := false
	for _, o := range Outcomes() {
		if r.Outcome == o {
			known = true
		}
	}
	switch {
	case !known:
		return fmt.Errorf("%w: unknown outcome %q", ErrInvalid, r.Outcome)
	case !validTrigger(r.Trigger):
		return fmt.Errorf("%w: trigger must be 1-%d bytes of [%s]", ErrInvalid, MaxTriggerBytes, TriggerChars)
	case r.Chars < 0 || r.Chars > BriefMaxChars:
		return fmt.Errorf("%w: chars must be 0-%d", ErrInvalid, BriefMaxChars)
	case r.Outcome != OutcomeServed && r.Chars != 0:
		return fmt.Errorf("%w: an unserved brief has no chars", ErrInvalid)
	case r.MS < 0:
		return fmt.Errorf("%w: ms must not be negative", ErrInvalid)
	}
	return nil
}

// Line renders the record as one JSONL line, newline included.
func (r BriefRecord) Line() ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	return Encode(r)
}

// ParseBriefRecord reads one line of the log (without its newline). It refuses an absent or other
// "v" before anything else, as a message decode does; unknown fields are ignored.
func ParseBriefRecord(line []byte) (BriefRecord, error) {
	var r BriefRecord
	if err := decode(bytes.NewReader(line), &r); err != nil {
		return BriefRecord{}, err
	}
	if err := r.validate(); err != nil {
		return BriefRecord{}, err
	}
	return r, nil
}

// HelloFile is a session's hello file: the proof a mod session started and reached the binary,
// the host version it reported, and what the hello answered (enabled, steering). The hello verb
// writes it for every served hello, including one answered enabled false because the host is too
// old, and never for a refused one. Checks that need to know whether the mod loaded, and whether
// it was told it may steer, read it. It carries "v": Version like a message, and a file that lacks
// enabled or steering is refused, so an absent answer is never read as false.
type HelloFile struct {
	V         int    `json:"v"`
	SessionID string `json:"session_id"`
	// HostVersion is the host's full version string, as the hello request carried it.
	HostVersion string `json:"host_version"`
	// Enabled is the served hello's HelloResponse.Enabled.
	Enabled bool `json:"enabled"`
	// Steering is the served hello's HelloResponse.Steering; true implies Enabled true.
	Steering bool `json:"steering"`
	// TS is when the hello was served.
	TS core.UnixMilli `json:"ts"`
}

func (h HelloFile) validate() error {
	if h.V != Version {
		return &VersionError{Got: h.V}
	}
	if !ValidSessionID(h.SessionID) {
		return fmt.Errorf("%w: session id %q cannot name a file", ErrInvalid, h.SessionID)
	}
	if h.HostVersion == "" || len(h.HostVersion) > MaxHostVersionBytes {
		return fmt.Errorf("%w: host_version must be 1-%d bytes", ErrInvalid, MaxHostVersionBytes)
	}
	if h.Steering && !h.Enabled {
		return fmt.Errorf("%w: a hello that steers is enabled", ErrInvalid)
	}
	return nil
}

// Encode renders the hello file's bytes: one JSON object and a newline.
func (h HelloFile) Encode() ([]byte, error) {
	if err := h.validate(); err != nil {
		return nil, err
	}
	return Encode(h)
}

// ParseHelloFile reads a hello file. It refuses an absent or other "v" before anything else, as a
// message decode does, then a file whose enabled or steering is absent or null; unknown fields are
// ignored.
func ParseHelloFile(data []byte) (HelloFile, error) {
	var h HelloFile
	if err := decode(bytes.NewReader(data), &h); err != nil {
		return HelloFile{}, err
	}
	var present struct {
		Enabled  *bool `json:"enabled"`
		Steering *bool `json:"steering"`
	}
	if err := json.Unmarshal(data, &present); err != nil {
		return HelloFile{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if present.Enabled == nil || present.Steering == nil {
		return HelloFile{}, fmt.Errorf("%w: a hello file needs enabled and steering", ErrInvalid)
	}
	if err := h.validate(); err != nil {
		return HelloFile{}, err
	}
	return h, nil
}
