package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// Retrieved content is born ephemeral (Qompack.md §8.7, closing gap GB).
//
// There is a self-defeating loop hiding in the retrieval layer: every expand and re_read returns
// content INTO the context window, where it accumulates like any other tool result — unpoliced,
// L6 recreates the exact bloat it exists to solve. The fix follows from the store's own
// guarantee: retrieved content is already indexed, so re-clearing it is free and lossless. It can
// always be retrieved again for the price of a tool call.
//
// So every retrieval result is written back into the store with Ephemeral set and indexed under a
// synthetic tool_use_id. analyzer.Block.Ephemeral then ranks it FIRST for eviction, ahead of
// ordinary tool results, in SP-12's droppable-block ranking (§8.4) — SP-13 sets the flag, SP-12
// ranks on it — and the model can expand its own retrieval output later by the id this file
// mints.
//
// Every failure in here is logged and swallowed. A retrieval that succeeded must never be turned
// into a failure by its own bookkeeping: the model already has the bytes, and the worst case of a
// missed record is that this particular result is evicted as an ordinary block rather than first.

// mcpToolPrefix is the tool name every ephemeral record is filed under, matching the host's own
// `mcp__<server>__<tool>` spelling so a record written by retrieval is recognisable as one.
const mcpToolPrefix = "mcp__qompack__"

// ephemeralIDPrefix distinguishes a synthetic retrieval id from a host-issued `toolu_…` one at a
// glance, in a log line and in a tombstone alike.
const ephemeralIDPrefix = "qompack-mcp:"

// SelfRecordIDPrefix is ephemeralIDPrefix for readers outside this package: `qompack fsck` uses it
// to recognise the retrieval self-records builds before V6 wave 13 filed at turn 0 (recordTurn).
const SelfRecordIDPrefix = ephemeralIDPrefix

// domainMCPToolUse is the hash domain the synthetic tool_use_id is derived under.
//
// It is declared HERE rather than in core's domain registry because core is SP-01's package and
// this is the only consumer: the registry's own comment treats those strings as a wire format, and
// adding to it would edit a package SP-13 does not own for a value nothing else reads. The
// ARGUMENT digest below deliberately does the opposite and reuses core.DomainArgs, because
// store.ToolUseRecord.ArgsDigest is documented as being that domain — a second spelling would make
// a retrieval-written record incomparable with an observer-written one for no gain.
const domainMCPToolUse = "qompack.mcp.tooluse.v1"

// The obs counters this file increments. §11.2's "retrieval hit rate" is computed by SP-02's
// replay harness from these.
const (
	counterEphemeralRecorded = "mcp.ephemeral.recorded"
	counterEphemeralFailed   = "mcp.ephemeral.failed"
)

// ephemeralRec is what recordEphemeral hands back: the two identifiers a model needs to expand
// its own retrieval output later. A zero value means nothing was recorded, which is a legitimate
// outcome and never an error.
type ephemeralRec struct {
	ToolUseID core.ToolUseID
	Hash      core.Hash
}

// recordEphemeral stores body as an ephemeral object and indexes it under a synthetic tool_use_id
// derived from (session, tool, args, timestamp).
//
// The id is DERIVED rather than random for two reasons that matter more than novelty: a derived
// id is reproducible, so a replay of the same session produces the same index (§6.1's determinism
// rule), and it needs no counter, so two retrieval processes cannot mint the same id by racing.
// The timestamp is in the seed precisely so that the SAME call made twice is two records rather
// than one silently overwritten.
// It returns NO error, and that is the contract rather than an omission: every failure below is a
// Warn, a counter and a degraded answer, because an ephemeral record is bookkeeping and the tool
// result is the deliverable. A signature that could report failure would invite a caller to treat
// one as the other.
func (h *handlers) recordEphemeral(ctx context.Context, r Request, toolName string,
	body []byte, path string,
) ephemeralRec {
	if !h.cfg.Retrieval.EphemeralResults || h.store == nil || len(body) == 0 {
		return ephemeralRec{}
	}

	// PutOptions.Canon is left at its zero value and this package never names canon.Options:
	// §3.2 does not permit mcp to import canon, and the import-graph check would fail if it did.
	// It costs nothing here. The bytes being stored are either already-canonical store content
	// being re-materialized or a JSON body this package just produced, neither of which has
	// volatile substrings to strip; the one exception, a worktree file re_read took off disk, is
	// stored as-is, which is correct for a record whose only job is to be re-expandable.
	pr, err := h.store.PutBytes(ctx, body, store.PutOptions{
		Tool:      mcpToolPrefix + toolName,
		Path:      path,
		Ephemeral: true,
	})
	if err != nil {
		h.log.Warn("mcp: could not store an ephemeral retrieval result",
			"tool", toolName, "err", err.Error())
		h.m.Counter(counterEphemeralFailed).Add(1)
		return ephemeralRec{}
	}

	ts := h.nowMilli()
	id := core.ToolUseID(ephemeralIDPrefix +
		core.HashBytes(domainMCPToolUse, ephemeralSeed(r.Session, toolName, r.Args, ts)).Short())

	rec := store.ToolUseRecord{
		ID:          id,
		Session:     r.Session,
		Turn:        recordTurn(ctx, r),
		TS:          ts,
		Tool:        mcpToolPrefix + toolName,
		ArgsDigest:  core.HashBytes(core.DomainArgs, r.Args),
		ArgsPreview: argsPreview(r.Args),
		Root:        pr.Root.Hash,
		Path:        path,
		Bytes:       int64(len(body)),
		Tokens:      pr.Root.Tokens,
		Signature:   pr.Signature,
		Ephemeral:   true,
	}
	if err := h.store.RecordToolUse(ctx, rec); err != nil {
		h.log.Warn("mcp: could not index an ephemeral retrieval result",
			"tool", toolName, "id", string(id), "err", err.Error())
		h.m.Counter(counterEphemeralFailed).Add(1)
		// The object IS stored, so the hash is still expandable even though the id is not
		// indexed. Reporting the hash and dropping the id is the honest half-answer.
		return ephemeralRec{Hash: pr.Root.Hash}
	}

	h.m.Counter(counterEphemeralRecorded).Add(1)
	return ephemeralRec{ToolUseID: id, Hash: pr.Root.Hash}
}

// recordTurn is the turn an ephemeral record is filed at: the call's own turn, raised to the calling
// session's current turn when the daemon resolved the call's turn and can re-resolve it now.
//
// Re-resolving at the moment of the write, rather than trusting the value resolved at dispatch, is
// what keeps index/tool_use.jsonl monotone per session (fsck index.tool_use): hook deliveries the
// daemon's workers publish WHILE this retrieval runs carry the turn the observer holds when they
// are published, and a self-record written after them at an older turn would read as a regression.
// It never lowers a turn: the dispatch-time value is already a lower bound on where the session is.
func recordTurn(ctx context.Context, r Request) core.TurnIndex {
	turn := r.Turn
	if f := liveFrom(ctx).Turn; f != nil {
		if now := f(); now > turn {
			turn = now
		}
	}
	return turn
}

// ephemeralSeed builds the domain-separated seed the synthetic id is derived from:
// session ‖ 0x00 ‖ tool ‖ 0x00 ‖ args ‖ 0x00 ‖ decimal(tsMillis). The 0x00 separators are what
// keep ("ab","c") and ("a","bc") distinct, the same reason core.HashBytes prepends one.
func ephemeralSeed(sess core.SessionID, tool string, args json.RawMessage, ts core.UnixMilli) []byte {
	var b bytes.Buffer
	b.WriteString(string(sess))
	b.WriteByte(0x00)
	b.WriteString(tool)
	b.WriteByte(0x00)
	b.Write(args)
	b.WriteByte(0x00)
	b.WriteString(strconv.FormatInt(int64(ts), 10))
	return b.Bytes()
}

// argsPreview renders a human-readable argument preview, truncated to the width §5.8 caps
// ToolUseRecord.ArgsPreview at. It counts RUNES, not bytes, so a multi-byte path is never cut
// mid-character into invalid UTF-8 on its way to a JSONL line.
func argsPreview(args json.RawMessage) string {
	compact := bytes.TrimSpace(args)
	if len(compact) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, compact); err == nil {
		compact = buf.Bytes()
	}
	runes := []rune(string(compact))
	if len(runes) <= argsPreviewRunes {
		return string(runes)
	}
	return string(runes[:argsPreviewRunes])
}
