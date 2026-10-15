package modwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The decode and validation errors. Each maps to ReasonError through FailureFor; they are distinct
// so the CLI's logs and tests can tell them apart.
var (
	// ErrUnsupportedVersion is wrapped by every *VersionError.
	ErrUnsupportedVersion = errors.New("modwire: unsupported wire version")
	// ErrMalformed means the input is not one JSON object of the expected shape.
	ErrMalformed = errors.New("modwire: malformed message")
	// ErrTooLarge means a message exceeds MaxMessageBytes.
	ErrTooLarge = errors.New("modwire: message exceeds MaxMessageBytes")
	// ErrInvalid means a field breaks the contract (an unsafe session id, a missing field, a
	// value out of bounds).
	ErrInvalid = errors.New("modwire: invalid field")
)

// VersionError reports a message whose "v" is absent (Got 0) or is not Version.
type VersionError struct {
	Got int
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("modwire: unsupported wire version %d (this side speaks %d)", e.Got, Version)
}

// Unwrap lets errors.Is match ErrUnsupportedVersion.
func (e *VersionError) Unwrap() error { return ErrUnsupportedVersion }

// FailureFor is the error object a side answers for a request it could not accept: ok false,
// ReasonError, and the error's text as the detail. The detail names the problem, never content
// from the request.
//
// The detail is valid UTF-8: each run of bytes that is not becomes one U+FFFD before the detail is
// cut to MaxDetailBytes. encoding/json would otherwise write every invalid byte as a three-byte
// U+FFFD, so the text the mod decodes, and measures, would be longer than the one checked here.
func FailureFor(err error) Status {
	return Failure(ReasonError, truncateDetail(strings.ToValidUTF8(err.Error(), "�")))
}

// truncateDetail cuts a detail to MaxDetailBytes without splitting a UTF-8 sequence.
func truncateDetail(s string) string {
	if len(s) <= MaxDetailBytes {
		return s
	}
	cut := MaxDetailBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// DecodeHelloRequest reads and validates one hello request.
func DecodeHelloRequest(r io.Reader) (HelloRequest, error) {
	var req HelloRequest
	if err := decode(r, &req); err != nil {
		return HelloRequest{}, err
	}
	if err := validateCommon(req.SessionID, req.CWD); err != nil {
		return HelloRequest{}, err
	}
	if err := validHostVersionField(req.HostVersion); err != nil {
		return HelloRequest{}, err
	}
	return req, nil
}

// DecodeBriefRequest reads and validates one brief request.
func DecodeBriefRequest(r io.Reader) (BriefRequest, error) {
	var req BriefRequest
	if err := decode(r, &req); err != nil {
		return BriefRequest{}, err
	}
	if err := validateCommon(req.SessionID, req.CWD); err != nil {
		return BriefRequest{}, err
	}
	if err := validHostVersionField(req.HostVersion); err != nil {
		return BriefRequest{}, err
	}
	if !validTrigger(req.Trigger) {
		return BriefRequest{}, fmt.Errorf("%w: trigger must be 1-%d bytes of [%s]", ErrInvalid, MaxTriggerBytes, TriggerChars)
	}
	if req.MaxChars < 0 {
		return BriefRequest{}, fmt.Errorf("%w: max_chars must not be negative", ErrInvalid)
	}
	return req, nil
}

// decode reads at most MaxMessageBytes, refuses an absent or unknown "v" before anything else is
// interpreted, then unmarshals into dst. Unknown fields are ignored; trailing data is malformed.
func decode(r io.Reader, dst any) error {
	data, err := io.ReadAll(io.LimitReader(r, MaxMessageBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read: %w", ErrMalformed, err)
	}
	if len(data) > MaxMessageBytes {
		return ErrTooLarge
	}
	var env struct {
		V *int `json:"v"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if env.V == nil {
		return &VersionError{}
	}
	if *env.V != Version {
		return &VersionError{Got: *env.V}
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return nil
}

func validateCommon(session, cwd string) error {
	if !ValidSessionID(session) {
		return fmt.Errorf("%w: session_id must be 1-%d bytes of [A-Za-z0-9._-], not . or ..",
			ErrInvalid, MaxSessionIDBytes)
	}
	if cwd == "" || strings.ContainsRune(cwd, 0) {
		return fmt.Errorf("%w: cwd must be a non-empty path", ErrInvalid)
	}
	return nil
}

// validHostVersionField checks a request's host_version is present and bounded. Whether it is
// readable, and new enough, is MeetsMinHostVersion's question, answered when the verb is served.
func validHostVersionField(v string) error {
	if v == "" || len(v) > MaxHostVersionBytes {
		return fmt.Errorf("%w: host_version must be 1-%d bytes", ErrInvalid, MaxHostVersionBytes)
	}
	return nil
}

// The validators are built from the published alphabets, so the Go checks and the mod's cannot
// disagree about which bytes are allowed.
var (
	sessionIDPattern = regexp.MustCompile("^[" + SessionIDChars + "]+$")
	triggerPattern   = regexp.MustCompile("^[" + TriggerChars + "]+$")
	factIDPattern    = regexp.MustCompile("^[" + FactIDChars + "]+$")
)

// validTrigger accepts any plain identifier: 1 to MaxTriggerBytes bytes of TriggerChars.
func validTrigger(s string) bool {
	return len(s) <= MaxTriggerBytes && triggerPattern.MatchString(s)
}

// ValidSessionID reports whether s can name a per-session file: 1 to MaxSessionIDBytes bytes of
// ASCII letters, digits, '.', '_' and '-', and neither "." nor "..". Host session ids are UUIDs,
// which pass; anything that could escape the state directory does not.
func ValidSessionID(s string) bool {
	return len(s) <= MaxSessionIDBytes && s != "." && s != ".." && sessionIDPattern.MatchString(s)
}

func knownReason(r Reason) bool {
	for _, k := range Reasons() {
		if r == k {
			return true
		}
	}
	return false
}

// validStatus checks the status invariants every response obeys.
func validStatus(s Status) error {
	switch {
	case s.V != Version:
		return fmt.Errorf("%w: v must be %d", ErrInvalid, Version)
	case s.OK && s.Reason != "":
		return fmt.Errorf("%w: a served response carries no reason", ErrInvalid)
	case !s.OK && !knownReason(s.Reason):
		return fmt.Errorf("%w: an unserved response needs a known reason, got %q", ErrInvalid, s.Reason)
	case len(s.Detail) > MaxDetailBytes:
		return fmt.Errorf("%w: detail is over %d bytes", ErrInvalid, MaxDetailBytes)
	}
	return nil
}

// Validate checks a brief response against the contract before the CLI writes it: the status
// invariants, instructions within maxChars (CharCount units) and only when served, and the fact
// id bounds.
func (r BriefResponse) Validate(maxChars int) error {
	if err := validStatus(r.Status); err != nil {
		return err
	}
	if !r.OK && (r.Instructions != "" || len(r.FactIDs) > 0) {
		return fmt.Errorf("%w: an unserved brief carries no instructions or fact ids", ErrInvalid)
	}
	if n := CharCount(r.Instructions); n > maxChars {
		return fmt.Errorf("%w: instructions are %d chars, over the cap of %d", ErrInvalid, n, maxChars)
	}
	if len(r.FactIDs) > MaxFactIDs {
		return fmt.Errorf("%w: %d fact ids, over the cap of %d", ErrInvalid, len(r.FactIDs), MaxFactIDs)
	}
	for _, id := range r.FactIDs {
		if len(id) > MaxFactIDBytes || !factIDPattern.MatchString(id) {
			return fmt.Errorf("%w: a fact id must be 1-%d bytes of [%s]", ErrInvalid, MaxFactIDBytes, FactIDChars)
		}
	}
	return nil
}

// MarshalJSON writes fact_ids as [] rather than null, so the field is always an array.
func (r BriefResponse) MarshalJSON() ([]byte, error) {
	type plain BriefResponse
	if r.FactIDs == nil {
		r.FactIDs = []string{}
	}
	return json.Marshal(plain(r))
}

// Encode renders one message as the bridge frames it: compact JSON on one line, ending in a
// newline, at most MaxMessageBytes long.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("modwire: encode: %w", err)
	}
	if buf.Len() > MaxMessageBytes {
		return nil, ErrTooLarge
	}
	return buf.Bytes(), nil
}

// CharCount measures text the way the mod's JavaScript does (String.prototype.length): in UTF-16
// code units. Every character cap in this contract is in these units, so the Go side and the mod
// agree on whether a text fits. Invalid UTF-8 counts as U+FFFD, one unit per bad byte.
func CharCount(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
