package modwire

import (
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
// call, served or not, except a refused one (ReasonRefused: a home root, no store, or the switch
// off), for which nothing is written and no store is created. Its
// field set is closed and carries no content: what was asked, what it came to, how much and how
// long.
type BriefRecord struct {
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
		return fmt.Errorf("%w: trigger must be 1-%d bytes of [a-z0-9_-]", ErrInvalid, MaxTriggerBytes)
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

// ParseBriefRecord reads one line of the log (without its newline). Unknown fields are ignored.
func ParseBriefRecord(line []byte) (BriefRecord, error) {
	var r BriefRecord
	if err := json.Unmarshal(line, &r); err != nil {
		return BriefRecord{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if err := r.validate(); err != nil {
		return BriefRecord{}, err
	}
	return r, nil
}

// HelloFile is a session's hello file: the proof a mod session started and reached the binary,
// and the host version it reported. The hello verb writes it for every served hello, including
// one answered enabled false because the host is too old, and never for a refused one. Checks
// that need to know whether the mod loaded read it.
type HelloFile struct {
	SessionID string `json:"session_id"`
	// HostVersion is the host's full version string, as the hello request carried it.
	HostVersion string `json:"host_version"`
	// TS is when the hello was served.
	TS core.UnixMilli `json:"ts"`
}

func (h HelloFile) validate() error {
	if !ValidSessionID(h.SessionID) {
		return fmt.Errorf("%w: session id %q cannot name a file", ErrInvalid, h.SessionID)
	}
	if h.HostVersion == "" || len(h.HostVersion) > MaxHostVersionBytes {
		return fmt.Errorf("%w: host_version must be 1-%d bytes", ErrInvalid, MaxHostVersionBytes)
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

// ParseHelloFile reads a hello file. Unknown fields are ignored.
func ParseHelloFile(data []byte) (HelloFile, error) {
	var h HelloFile
	if err := json.Unmarshal(data, &h); err != nil {
		return HelloFile{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if err := h.validate(); err != nil {
		return HelloFile{}, err
	}
	return h, nil
}
