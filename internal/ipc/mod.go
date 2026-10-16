package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// ModCall is what an OpMod request carries in Raw ("x"): the `qompack mod` verb, the daemon's budget
// for answering it, and the verb's request exactly as the CLI forwards it. It is internal to the CLI
// and the daemon; the mod never sees it. The request's own shape belongs to internal/modwire, which
// this package does not import: the daemon decodes it.
type ModCall struct {
	// Verb is the modwire verb ("brief").
	Verb string `json:"verb"`
	// BudgetMs is how long the daemon has to answer, in milliseconds, counted from when it receives
	// the request, and never later than the request's TS (the CLI process's start) plus the mod's
	// timeout less the CLI's two margins. The CLI computes it for each request from the mod's timeout
	// and its own start-up time; a budget that is not positive is already over.
	BudgetMs int64 `json:"budget_ms"`
	// Req is the verb's request object.
	Req json.RawMessage `json:"req"`
}

// errModCall is the error every malformed ModCall wraps.
var errModCall = errors.New("ipc: malformed mod call")

// validate refuses a call with no verb or no request object.
func (c ModCall) validate() error {
	switch {
	case c.Verb == "":
		return fmt.Errorf("%w: no verb", errModCall)
	case len(bytes.TrimSpace(c.Req)) == 0 || bytes.Equal(bytes.TrimSpace(c.Req), []byte("null")):
		return fmt.Errorf("%w: no request", errModCall)
	}
	return nil
}

// ModRequest builds the OpMod request for call: a Reply request for session, stamped ts, with call
// in Raw. It refuses a call DecodeModCall would refuse.
func ModRequest(session core.SessionID, ts core.UnixMilli, call ModCall) (Request, error) {
	if err := call.validate(); err != nil {
		return Request{}, err
	}
	raw, err := json.Marshal(call)
	if err != nil {
		return Request{}, fmt.Errorf("%w: %w", errModCall, err)
	}
	return Request{Op: OpMod, Session: session, TS: ts, Reply: true, Raw: raw}, nil
}

// DecodeModCall reads an OpMod request's Raw. It refuses a Raw that is absent, is not a JSON
// object, or names no verb or no request.
func DecodeModCall(raw json.RawMessage) (ModCall, error) {
	var c ModCall
	if err := json.Unmarshal(raw, &c); err != nil {
		return ModCall{}, fmt.Errorf("%w: %w", errModCall, err)
	}
	if err := c.validate(); err != nil {
		return ModCall{}, err
	}
	return c, nil
}
