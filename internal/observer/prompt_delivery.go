package observer

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// SP08-D3, Option A: a prompt's authoritative verbatim capture is the WORKER/REPLAY's, done under the
// leased observation identity exactly as a tool result's is, so a prompt that reached the daemon only
// by WAL/spool replay is captured rather than lost. The live reply path keeps only the synchronous
// thrash-warning feature. The split is a context marker, not a second Services seam or a
// frozen-interface change: OnUserPrompt reads the context and does one job or the other.
//
// Durability is therefore ASYNCHRONOUS after the durable WAL ACK, as tool captures already are. The
// frontier ACK still follows capture/reference/link success (ingest.dispatch gates it on the
// handler's OK), so an unavailable or failed capture stays pending, never acknowledged as restored.

// opObservePrompt is the capture sidecar's Op for a prompt delivery — ipc.OpObservePrompt's wire
// form, spelled as a literal because §3.2 keeps internal/ipc out of observer (identity.go matches
// observe.tool/observe.stop the same way). publishCapture copies req.Op verbatim into the sidecar,
// so observationRecord can confirm a recognized redelivery is this op and not a recycled id.
const opObservePrompt = "observe.prompt"

// promptReplyOnlyKey marks a context as the LIVE reply path. Under it OnUserPrompt drains and returns
// the pending thrash warning under the session lock and records NOTHING and advances NO turn.
type promptReplyOnlyKey struct{}

type promptCaptureOnlyKey struct{}

// WithPromptCaptureOnly selects worker capture without consuming live warnings.
func WithPromptCaptureOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, promptCaptureOnlyKey{}, true)
}

func promptCaptureOnly(ctx context.Context) bool {
	v, _ := ctx.Value(promptCaptureOnlyKey{}).(bool)
	return v
}

// promptHostTSKey carries the host timestamp of the delivery being captured.
type promptHostTSKey struct{}

// WithHostTS attaches the host timestamp of the prompt delivery being captured — ipc.Request.TS,
// which the hook client stamps before any transport attempt — so the capture can record when the
// host sent the prompt rather than when a worker or a drain replay got to it (SP08-D3, owner
// decision D35). A zero or negative stamp is no stamp, and the capture keeps the observer's clock.
func WithHostTS(ctx context.Context, ts core.UnixMilli) context.Context {
	if ts <= 0 {
		return ctx
	}
	return context.WithValue(ctx, promptHostTSKey{}, ts)
}

// hostTSFrom returns the stamp WithHostTS attached, or 0.
func hostTSFrom(ctx context.Context) core.UnixMilli {
	ts, _ := ctx.Value(promptHostTSKey{}).(core.UnixMilli)
	return ts
}

// counterPromptOutOfHostOrder counts prompt captures that landed behind a turn their host sent
// later: the live-vs-spool race owner decision D35 rules out of the host-order guarantee.
const counterPromptOutOfHostOrder = "observer.prompt_out_of_host_order"

// notePromptHostOrder reports a prompt captured at turn behind an already-published turn its host
// sent later (SP08-D3, owner decision D35). Replayed client spools are drained in host order, but a
// prompt that reached only its hook's spool can still be published after a later prompt that
// arrived live. Published turns are not re-numbered: turn order stays publication order, and each
// record carries its host timestamp (recordPromptDurable), so the substitution is counted and
// warned here and internal/rehydrate names it where it would otherwise present a later prompt as the
// session's original request.
//
// The walk goes down from the previous turn and stops at the first prompt record stamped no later
// than this one, so a capture in host order costs one index lookup. A turn with no prompt record (a
// SubagentStop's turn, a capture an unleased caller lost) is stepped over. The warning names the
// lowest turn the walk found stamped later: that is where the order first breaks.
func (o *observer) notePromptHostOrder(ctx context.Context, s core.SessionID, turn core.TurnIndex) {
	ts := hostTSFrom(ctx)
	if ts <= 0 || turn == 0 {
		return
	}
	substituted := core.TurnIndex(-1)
	for t := turn - 1; t >= 0; t-- {
		rec, err := o.opt.Store.ToolUse(ctx, VerbatimPromptID(s, t))
		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				continue
			}
			break // best effort: the rehydrator's own check does not depend on this walk
		}
		if rec.TS <= ts {
			break
		}
		substituted = t
	}
	if substituted < 0 {
		return
	}
	o.count(counterPromptOutOfHostOrder)
	o.opt.Log.Warn("observer: prompt captured after a turn its host sent later; turns stay in "+
		"publication order",
		"session", string(s), "id", string(VerbatimPromptID(s, turn)),
		"substituted_id", string(VerbatimPromptID(s, substituted)))
}

// A leased prompt has synthetic arguments bound to its delivery, not just its
// text. This preserves the wire shape while making an index-before-link crash
// recoverable even after several turns and loss of observer.json.
func promptDeliveryDigest(prompt string, obs core.ObservationID) core.Hash {
	if obs == "" {
		digest, _ := store.ArgsDigest(promptArgs(prompt))
		return digest
	}
	args, _ := json.Marshal(struct {
		Prompt      string             `json:"prompt"`
		Observation core.ObservationID `json:"observation_id"`
	}{prompt, obs})
	digest, _ := store.ArgsDigest(args)
	return digest
}

func (o *observer) syncObservation(ctx context.Context, root core.Hash) error {
	sync, ok := o.opt.Store.(store.PublicationSync)
	if !ok || sync.SyncPublication(ctx, root) != nil {
		return o.unpublished(stageIndex)
	}
	return nil
}

func (o *observer) recoverPrompt(ctx context.Context, st *sessionState, e Event, obs core.ObservationID) (bool, error) {
	if obs == "" {
		return false, nil
	}
	recovery, ok := o.opt.Store.(store.PromptRecovery)
	if !ok {
		return false, o.unpublished(stageIndex)
	}
	next, rec, err := recovery.PromptFrontier(ctx, e.SessionID, promptDeliveryDigest(e.Prompt, obs))
	if err != nil {
		return false, o.unpublished(stageIndex)
	}
	if next > st.Turn {
		st.Turn = next
	}
	if rec.ID == "" {
		return false, nil
	}
	if err := o.syncObservation(ctx, rec.Root); err != nil {
		return false, err
	}
	if err := o.linkObservation(obs, rec); err != nil {
		return false, o.unpublished(stageLink)
	}
	adoptTurn(st, rec)
	o.count(counterRedelivery)
	return true, nil
}

// WithPromptReplyOnly marks ctx as the reply-only prompt path. handleObservePrompt wraps the reply
// call with it; the worker and the drain replay use a normal context (carrying WithObservation) and
// do the durable capture.
func WithPromptReplyOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, promptReplyOnlyKey{}, true)
}

func promptReplyOnly(ctx context.Context) bool {
	v, _ := ctx.Value(promptReplyOnlyKey{}).(bool)
	return v
}

// PromptReplyOnly reports whether ctx is the reply-only prompt path WithPromptReplyOnly marked. The
// daemon's scheduler tap reads it so the work it keeps off the reply deadline (binding a session on
// its first hook) waits for the worker's capture of the same prompt.
func PromptReplyOnly(ctx context.Context) bool { return promptReplyOnly(ctx) }

// promptReplyOutput is the reply-only path's whole job: the queued thrash warning, and only in
// ModeFull (§12 forbids injection while the contract is degraded). It drains the queue — a warning is
// shown once — and does not touch the store, the DAG, the grammar or the turn: those belong to the
// authoritative worker/replay capture. The caller holds st.mu.
func (o *observer) promptReplyOutput(st *sessionState) Output {
	out := hookio.Empty()
	if o.mode() != ModeFull {
		return out
	}
	if lines := o.pendingThrashAt(st, st.WarningTurn); len(lines) > 0 {
		out.HookSpecificOutput = &hookio.HSO{
			HookEventName:     userPromptSubmit,
			AdditionalContext: boundThrashWarning(lines),
		}
	}
	return out
}

// recordPromptDurable publishes the verbatim record and observation link before
// advancing the session. DAG updates remain secondary derived state.
//
// For a LEASED delivery a lost index write or a lost reference link returns ErrUnpublished so the
// frontier is never acknowledged over a capture that did not become durable (SP08-D3): the daemon
// keeps the delivery pending and a later drain re-runs it. The DAG node is soft (a slice can degrade
// without losing the record). An UNLEASED, in-process caller (obs == "") has no frontier to protect
// and keeps the base behaviour — a soft failure that still lets the session move on — because
// nothing downstream can retry it and stalling it would only lose the turns behind it.
//
// The index's synthetic-argument digest binds the leased delivery before the
// sidecar link is written. recoverPrompt repairs that cut after restart; legacy
// unlinked records without this binding do not establish delivery identity.
func (o *observer) recordPromptDurable(ctx context.Context, st *sessionState, e Event,
	res store.PutResult, body []byte, now core.UnixMilli, obs core.ObservationID,
) error {
	id := VerbatimPromptID(e.SessionID, st.Turn)
	tok := res.Root.Tokens
	if tok == 0 && o.opt.Tokens != nil {
		tok = o.opt.Tokens.EstimateString(e.Prompt, tokens.ClassProse)
	}
	_, preview := store.ArgsDigest(promptArgs(e.Prompt))
	digest := promptDeliveryDigest(e.Prompt, obs)
	// The record carries the host's timestamp when the daemon passed one (WithHostTS): turn order is
	// publication order, and the host stamp is what still shows the order the host sent prompts in
	// (SP08-D3, D35). The DAG node and the session features keep the observer's clock below.
	recTS := now
	if h := hostTSFrom(ctx); h > 0 {
		recTS = h
	}
	rec := store.ToolUseRecord{
		ID: id, Session: e.SessionID, Turn: st.Turn, TS: recTS, Tool: userPromptSubmit,
		ArgsDigest: digest, ArgsPreview: preview,
		Root: res.Root.Hash, Bytes: int64(len(body)), Tokens: tok, Status: store.StatusOK,
		Observation: obs,
	}
	// The §0.2.2 barriers around a leased write: root durable before the record, record durable
	// before the link. A recorder that declares them makes both inside RecordToolUse, and repeating
	// them here would only re-sync the same root (SP08-D1).
	storeSynced := obs != "" && recorderPublishesDurably(o.opt.Store)
	if obs != "" && !storeSynced {
		if err := o.syncObservation(ctx, rec.Root); err != nil {
			return err
		}
	}
	if err := o.opt.Store.RecordToolUse(ctx, rec); err != nil {
		if obs != "" {
			return o.unpublished(stageIndex)
		}
		o.soft(stageIndex, err)
		return nil
	}
	if obs != "" && !storeSynced {
		if err := o.syncObservation(ctx, rec.Root); err != nil {
			return err
		}
	}
	if err := o.linkObservation(obs, rec); err != nil {
		if obs != "" {
			return o.unpublished(stageLink)
		}
		o.soft(stageLink, err)
		return nil
	}

	// The §8.2 DAG artifacts, unchanged from the pre-SP08-D3 recordPrompt and soft throughout: a
	// prompt whose bytes and index landed is captured even if a slice edge is momentarily missing.
	o.soft(stageDAG, dag.BuildUserPrompt(o.opt.Graph, dag.ObservedPrompt{
		Turn: st.Turn, TS: now, Pos: o.advancePos(st, tok), Tokens: tok, Ref: string(id),
	}))
	o.soft(stageDAG, o.opt.Graph.AddEdge(dag.Edge{
		From: dag.UserPromptNode(st.Turn), To: dag.AssistantNode(st.Turn + 1),
		Kind: dag.EdgeConsumes, Weight: edgeWeight, Turn: st.Turn,
	}))
	o.enrol(st, dag.UserPromptNode(st.Turn))
	return nil
}
