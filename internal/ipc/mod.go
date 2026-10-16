package ipc

import (
	"encoding/json"
	"errors"

	"github.com/qompack/qompack/internal/core"
)

// ModCall is what an OpMod request carries in Raw. It is internal to the CLI and the daemon: the
// mod never sees it.
type ModCall struct {
	Verb     string          `json:"verb"`
	BudgetMs int64           `json:"budget_ms"`
	Req      json.RawMessage `json:"req"`
}

// ModRequest builds the OpMod request for call.
func ModRequest(session core.SessionID, ts core.UnixMilli, call ModCall) (Request, error) {
	return Request{}, errors.New("ipc: ModRequest: not implemented")
}

// DecodeModCall reads an OpMod request's Raw.
func DecodeModCall(raw json.RawMessage) (ModCall, error) {
	return ModCall{}, errors.New("ipc: DecodeModCall: not implemented")
}
