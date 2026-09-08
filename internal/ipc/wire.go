package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// Op names one daemon operation (00-ARCHITECTURE.md §5.4). The wire spelling of each is stable
// data: it appears in every spooled NDJSON line, and a drain of yesterday's spool by today's
// daemon has to understand it.
type Op string

// The eight concrete operations of §5.4. Everything the plugin does over the transport is one of
// these, plus the reserved admin namespace below.
const (
	OpObserveTool   Op = "observe.tool"
	OpObservePrompt Op = "observe.prompt"
	OpObserveStop   Op = "observe.stop"
	OpSessionStart  Op = "session.start"
	OpCheckpoint    Op = "checkpoint"
	OpFlush         Op = "flush"
	OpStatus        Op = "status"
	OpMCP           Op = "mcp"
)

// OpAdminPrefix is the reserved namespace §5.4 writes as "admin.*": operations that administer the
// daemon itself rather than observing a session. It is a prefix, not a valid Op, so it is typed as
// a plain string — an Op is matched against it, never set to it.
const OpAdminPrefix = "admin."

// HotPathMode is the submode a Response reports and clients honour immediately (§12.2). §5.4 gives
// it only as a comment; SP-01 declares it here.
//
// HotSync is the zero value because it is the normal state: a Response that says nothing about the
// hot path is saying "keep connecting". HotSpool is the §8.1 overrun fallback — the client stops
// connecting altogether and appends straight to the spool for the rest of the session, so data is
// not lost, only freshness.
type HotPathMode uint8

// The two hot-path submodes.
const (
	HotSync HotPathMode = iota
	HotSpool
)

// ACK and NAK are §2.4's single-byte responses to a fire-and-forget request: the daemon writes ACK
// after the WAL append returns, before any indexing work, which is what converts "usually
// delivered" into "provably enqueued" for about 50 microseconds. They are the ASCII control
// characters of the same name, and they are wire format — never respell them.
const (
	ACK byte = 0x06
	NAK byte = 0x15
)

// MaxLineBytes is §2.4's "1 MiB max line": the absolute ceiling on one NDJSON frame, above which a
// reader must reject rather than allocate.
//
// It is a protocol constant, not a tunable, and it is not a duplicate of a config default: the
// OPERATIONAL accept limit is runtime.hotPath.maxPayloadBytes, which an operator may lower and
// which hookio.ReadEvent already honours. A real reader takes the smaller of the two — the config
// key can tighten this ceiling, never raise it.
const MaxLineBytes = 1 << 20

// Request is one NDJSON line from a hook client to the daemon (00-ARCHITECTURE.md §5.4). The short
// JSON keys are deliberate: every one of these is written on the hot path, once per tool call.
type Request struct {
	Op      Op             `json:"op"`
	Session core.SessionID `json:"s"`
	TS      core.UnixMilli `json:"t"`
	// Reply asks for a full NDJSON response line instead of the one-byte ACK. Set by the requests
	// that need data back: UserPromptSubmit's additionalContext, MCP-over-daemon, and status.
	Reply bool `json:"r,omitempty"`
	// Event is the hook payload, carried verbatim so the daemon observes exactly what the host
	// sent rather than a re-encoding of it.
	Event *hookio.Event   `json:"e,omitempty"`
	Raw   json.RawMessage `json:"x,omitempty"`
	// Capture carries the admitted host payload beside the derived Event: its permitted bytes, the
	// fidelity and capture error the policy determined, and the transform/hash versions those were
	// determined under. hookio.Event.Extra is tagged `json:"-"`, so before this field every host key
	// this build does not name was dropped here, at the encode boundary, and the two-case Raw
	// allow-list was the only thing that survived. Both remain: Raw is unchanged and Capture is
	// additive, so a daemon built before this field ignores it and a request written without it
	// decodes here as no capture at all.
	Capture *hookio.Capture `json:"c,omitempty"`
	// Nonce labels one host invocation of a hook, minted once by the client with crypto/rand and
	// carried unchanged through the daemon connect, the spool fallback and every retry of that same
	// delivery. It is not a content hash and not an identity the daemon assigns: two byte-identical
	// payloads from two invocations carry two nonces, and one payload delivered twice carries one.
	// It is empty when minting failed — an absent label, never a synthetic substitute for one.
	Nonce string `json:"n,omitempty"`
}

// NewDeliveryNonce mints one delivery nonce. It is called once per host invocation, before any
// transport attempt, so that the nonce is a property of the delivery rather than of the attempt
// that happened to succeed. The error is returned rather than swallowed because the alternative to
// a real random label is no label: a counter or a timestamp would collide across concurrent hooks
// and would read as an identity it cannot support.
func NewDeliveryNonce() (string, error) {
	var b [deliveryNonceBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("ipc: delivery nonce unavailable: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// deliveryNonceBytes is 128 bits of entropy: enough that two nonces never collide in practice
// across every hook invocation a machine will ever run, and short enough to sit on the hot path.
const deliveryNonceBytes = 16

// CaptureFrameBudget bounds the permitted capture bytes one request may carry. JSON encodes []byte
// as base64, four characters per three bytes, so this budget leaves the Event and the frame's own
// keys at least half of MaxLineBytes. A frame no reader will accept is worse than one carrying less
// evidence: the spool's own line reader rejects an oversize line outright, which would lose the
// whole delivery rather than only the capture.
const CaptureFrameBudget = MaxLineBytes * 3 / 8

// WithCapture returns req carrying c. When c's permitted bytes would not fit CaptureFrameBudget,
// the bytes are left behind and the record travels as an explicitly unavailable, oversize capture
// instead of an apparently complete one — the fidelity record must never overstate what crossed.
func WithCapture(req Request, c hookio.Capture) Request {
	if len(c.Bytes) > CaptureFrameBudget {
		c.Bytes = nil
		c.Outcome, c.Fidelity = core.OutcomeUnavailable, core.FidelityTruncated
		c.Truncated, c.CaptureError = true, core.CaptureErrorOversize
	}
	req.Capture = &c
	return req
}

// Response is the daemon's NDJSON reply to a Request with Reply set (00-ARCHITECTURE.md §5.4).
//
// Mode and Hot are on every response, including failures, because both are things the client must
// honour immediately: Mode is §12.1's contract state (a degraded session stops acting), and Hot is
// §12.2's hot-path submode (a spooling client stops connecting).
type Response struct {
	OK     bool            `json:"ok"`
	Mode   contract.Mode   `json:"mode"`
	Hot    HotPathMode     `json:"hot"`
	Output *hookio.Output  `json:"out,omitempty"`
	Err    string          `json:"err,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}
