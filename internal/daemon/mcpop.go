package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/mcp"
)

// The `mcp` op: the daemon side of the L6 retrieval layer.
//
// The handlers execute HERE, not in the `qompack mcp` process, and that split is the whole design
// (00-ARCHITECTURE.md §2.4, D6). The daemon is the single writer of the store and holds the warm
// handles — the object index, the elimination ledger, the segment log — so a retrieval executed
// daemon-side reads state that is already in memory and cannot race the observer's own writes. The
// stdio process is a transcoder: it speaks JSON-RPC to the host, forwards `tools/call` over the
// local transport, and renders whatever comes back.
//
// It also has no session identity, which is the second reason. Claude Code launches a stdio MCP
// server once per client and hands it no session_id; `initialize` carries only clientInfo. The
// daemon is the only party that knows which sessions are live, so it resolves both the session and
// the turn — see resolveSession and resolveTurn below.

// The two kinds of `mcp` op payload.
const (
	// MCPKindCall is one tools/call forwarded from the stdio process.
	MCPKindCall = "call"
	// MCPKindInitialized reports that the stdio server completed the MCP handshake. It is what
	// flips the mcp.server_registered observable (§12.1).
	MCPKindInitialized = "initialized"
)

// mcpCallTimeout bounds one daemon-side tools/call, matching the deadline the stdio process waits
// within so neither side gives up while the other is still working.
const mcpCallTimeout = 5 * time.Second

// MCPOpRequest is the `mcp` op's request payload, carried in ipc.Request.Raw.
type MCPOpRequest struct {
	Kind string          `json:"kind"`
	Name string          `json:"name,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
	// Turn is the caller's own turn index when it has one; 0 means "resolve it here".
	Turn core.TurnIndex `json:"turn,omitempty"`
	// Observ is the handshake record, on a kind=="initialized" request.
	Observ *mcp.Observable `json:"observable,omitempty"`
}

// MCPOpResponse is the `mcp` op's response payload, carried in ipc.Response.Data.
type MCPOpResponse struct {
	Content   []mcp.Content  `json:"content"`
	IsError   bool           `json:"is_error"`
	Ephemeral bool           `json:"ephemeral"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// InstallMCPOp registers the `mcp` op and binds the MCPInitialized seam.
//
// All of it must happen BEFORE daemon.New(o). New registers its own fallback `mcp` route only for
// ops that are not already registered, and it calls DeclareProducers AFTER applying every Bind —
// so a Bind registered later never runs, and the contract producer is never declared. The
// o.Bind call below is specifically what makes contract.DeclareProducers declare CMCPRegistered,
// which is what stops the mcp.server_registered assertion reporting "not-yet-implemented" and
// starts it reporting a real observation.
func InstallMCPOp(o *Options, d mcp.ToolDeps) error {
	if o == nil {
		return errors.New("qompack: daemon: InstallMCPOp needs an Options")
	}

	srv := mcp.NewServerWithOptions(mcp.ServerOptions{
		Name:    mcp.ServerName,
		Version: core.Version,
		Log:     o.Log,
		MaxLine: o.Cfg.Runtime.HotPath.MaxPayloadBytes,
	})
	if err := mcp.RegisterAll(srv, d); err != nil {
		return err
	}

	var initialized atomic.Bool

	o.Handle(ipc.OpMCP, func(ctx context.Context, req ipc.Request) ipc.Response {
		return handleMCPOp(ctx, o, srv, &initialized, req)
	})
	o.Bind(func(s *Services) {
		s.MCPInitialized = func(context.Context) bool { return initialized.Load() }
	})
	return nil
}

// handleMCPOp routes one `mcp` op.
func handleMCPOp(ctx context.Context, o *Options, srv mcp.Server,
	initialized *atomic.Bool, req ipc.Request,
) ipc.Response {
	var m MCPOpRequest
	if len(req.Raw) > 0 {
		if err := json.Unmarshal(req.Raw, &m); err != nil {
			return ipc.Response{OK: false, Err: "mcp: malformed op payload: " + err.Error()}
		}
	}

	switch m.Kind {
	case MCPKindInitialized:
		initialized.Store(true)
		// The history read-modify-write is serialized with every other route that loads and saves
		// state/history.json — above all the session.start route, which the host runs beside this
		// handshake — through the daemon's historyMu, so neither save drops the other's change.
		if d, ok := DaemonFrom(ctx).(*daemon); ok && d != nil {
			d.historyMu.Lock()
			defer d.historyMu.Unlock()
		}
		recordMCPHandshake(o, m.Observ)
		return ipc.Response{OK: true}
	case MCPKindCall, "":
		return dispatchMCPCall(ctx, o, srv, req, m)
	default:
		return ipc.Response{OK: false, Err: `mcp: unknown op kind "` + m.Kind + `"`}
	}
}

// recordMCPHandshake sets the §12.1 observable and, when the stdio process supplied one, the
// human-readable handshake record beside it.
//
// The read-modify-write goes through contract.LoadHistory/SaveHistory, which are POINTER-based
// precisely because the read-modify-write is the point: a value copy would silently lose the
// modification. The path is contract.HistoryPath — state/history.json — and never
// state/contract.json, which is the Monitor's own persisted mode/reason/results file. Writing a
// SessionHistory over that file is the corruption internal/contract/history.go warns about, and
// since the assertion reads HistoryPath anyway, a write to the wrong file would leave
// mcp.server_registered observing "initialize-not-received" for ever.
//
// Every failure here is a Warn and a shrug. A failed observable must never fail a retrieval
// session: the assertion's declared severity is SevInfo, so a missing handshake is surfaced and
// can never degrade a session on its own.
func recordMCPHandshake(o *Options, obs *mcp.Observable) {
	path := contract.HistoryPath(o.ProjectRoot)
	h := contract.LoadHistory(path)
	if !h.MCPInitialized {
		h.MCPInitialized = true
		if err := contract.SaveHistory(path, h); err != nil {
			o.Log.Warn("mcp: could not record the server_registered observable", "err", err.Error())
		}
	}
	if obs == nil {
		return
	}
	if err := mcp.WriteInitializedObservable(o.ProjectRoot, *obs); err != nil {
		o.Log.Warn("mcp: could not write the handshake record", "err", err.Error())
	}
}

// dispatchMCPCall resolves the caller's session and turn, runs the tool, and packs the result.
//
// An unknown tool is OK:true with an IsError payload, never ipc.Response{OK:false}: the transport
// is healthy and the op routed correctly; it was the tool NAME that was wrong, and that is
// something the model reads rather than something the client retries.
func dispatchMCPCall(ctx context.Context, o *Options, srv mcp.Server,
	req ipc.Request, m MCPOpRequest,
) ipc.Response {
	session := req.Session
	if session == "" {
		session = resolveSession(ctx)
	}
	turn := m.Turn
	if turn == 0 {
		turn = resolveTurn(ctx, session)
	}

	callCtx, cancel := context.WithTimeout(ctx, mcpCallTimeout)
	defer cancel()

	resp, err := mcp.Dispatch(callCtx, srv, mcp.Request{
		Session:  session,
		Name:     m.Name,
		Args:     m.Args,
		Turn:     turn,
		Deadline: time.Now().Add(mcpCallTimeout),
	})
	if err != nil {
		if !errors.Is(err, mcp.ErrToolNotFound) {
			o.Log.Warn("mcp: op dispatch failed", "tool", m.Name, "err", err.Error())
		}
		resp = mcp.Response{
			IsError: true,
			Content: []mcp.Content{{
				Type: "text",
				Text: `unknown tool "` + m.Name + `"; call tools/list for the eight available tools`,
			}},
		}
	}

	data, merr := json.Marshal(MCPOpResponse{
		Content: resp.Content, IsError: resp.IsError, Ephemeral: resp.Ephemeral, Meta: resp.Meta,
	})
	if merr != nil {
		return ipc.Response{OK: false, Err: "mcp: could not render the tool result: " + merr.Error()}
	}
	return ipc.Response{OK: true, Data: data}
}

// resolveSession picks the session an MCP call belongs to.
//
// The stdio process has none to send: Claude Code launches an MCP server once per client and hands
// it no session_id. The daemon is the only party that can answer, so it prefers the live session
// with the most recent activity, falls back to the most recent session of any kind, and returns
// "" when it tracks none at all. Every handler degrades cleanly on "" — an ephemeral record is
// still addressable by hash, and `dropped` says `available:false` rather than guessing.
func resolveSession(ctx context.Context) core.SessionID {
	reg := RegistryFrom(ctx)
	if reg == nil {
		return ""
	}
	var best SessionState
	var haveLive, haveAny bool
	for _, s := range reg.Snapshot() {
		switch {
		case s.Live && (!haveLive || s.LastActivity > best.LastActivity):
			best, haveLive, haveAny = s, true, true
		case !haveLive && (!haveAny || s.LastActivity > best.LastActivity):
			best, haveAny = s, true
		}
	}
	if !haveAny {
		return ""
	}
	return best.ID
}

// resolveTurn reports where the session currently is, as a lower bound.
//
// SP-05's SessionState carries no turn index and SP-13 may not add one (Rule W-3), so the answer
// comes from the store's currently open segment instead: SP-06 initialises an open segment's
// EndTurn to its StartTurn, which makes max(StartTurn, EndTurn) a defined, monotone lower bound on
// "where we are now". That is honest under-approximation rather than a guess — an ephemeral
// record's turn is used only for ordering, and ordering by segment start is correct.
func resolveTurn(ctx context.Context, session core.SessionID) core.TurnIndex {
	svc := ServicesFrom(ctx)
	if svc == nil || svc.Store == nil || session == "" {
		return 0
	}
	segs := svc.Store.Segments()
	if segs == nil {
		return 0
	}
	seg, err := segs.Current(ctx, session)
	if err != nil {
		return 0
	}
	if seg.EndTurn > seg.StartTurn {
		return seg.EndTurn
	}
	return seg.StartTurn
}
