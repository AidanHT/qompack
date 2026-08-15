package ipc

import (
	"encoding/json"

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
