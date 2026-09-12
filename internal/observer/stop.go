package observer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// The Stop / SubagentStop half of L0: §8.1 item 8, which closes G10.1.
//
// G10.1 is "subagent output is compressed twice": a subagent returns a prose summary, the host
// later summarizes that summary, and because the parent never held the underlying tool results
// there is nothing to recover at either level. What this file writes is the missing bottom layer —
// the summary AND a hash list naming every tool result the subagent produced — so that
// expand(subagent_<session>_<turn>) returns the capture and every `root` inside it is itself
// independently expand-able.

// subagentStop is the tool name a capture is filed under, in both PutOptions.Tool and
// ToolUseRecord.Tool. It matches the host's own hook name so a reader of index/tool_use.jsonl
// needs no second vocabulary.
const subagentStop = "SubagentStop"

// stopSymbol is the action-grammar symbol a main-agent Stop contributes (§8.1 item 6). A
// SubagentStop contributes none: the parent's action stream is what the grammar models, and a
// subagent's turn boundary is not a parent action.
const stopSymbol = "stop"

// subagentIDPrefix begins every SubagentCaptureID.
const subagentIDPrefix = "subagent"

// defaultSubagentName is the agent name a capture carries when the host did not name one. It is a
// literal rather than "" so that every capture is attributable to *something* and the JSON shape
// is the same either way.
const defaultSubagentName = "subagent"

// extraAgentKey is the ONE hookio.Event.Extra key this package reads. See subagentName.
const extraAgentKey = "agent"

// transcriptTailBytes is how much of the transcript's tail is read when the payload carried no
// summary. A JSONL transcript is append-only and can reach hundreds of megabytes in a long
// session; the last assistant message is always within the final few hundred kilobytes, and the
// hook is on budget B-C, so the read is bounded rather than complete.
const transcriptTailBytes int64 = 512 << 10

// The soft-failure stages the Stop path reports under, as observer.err.<stage>. They are distinct
// from the tool path's stagePut/stageDAG because a lost SUBAGENT capture is a G10.1 failure and
// must be countable on its own rather than averaged into the much larger tool-result traffic.
const (
	stageStopMarshal = "stop.marshal"
	stageStopPut     = "stop.put"
	stageStopFlush   = "stop.flush"
)

// The transcript-line shapes tailAssistantText recognizes.
const (
	transcriptRoleAssistant = "assistant"
	transcriptBlockText     = "text"
)

// SubagentToolRef is one tool result a subagent produced, named by content address.
//
// Root is the core.Hash.String() form rather than a core.Hash so that a capture blob stays
// greppable and so that the identity in it is the STORE's own root — the thing `expand` takes —
// rather than a second encoding only this package can read.
type SubagentToolRef struct {
	ToolUseID core.ToolUseID `json:"tool_use_id"`
	Root      string         `json:"root"`
	Tool      string         `json:"tool"`
	Path      string         `json:"path"`
	Bytes     int64          `json:"bytes"`
}

// SubagentCapture is the whole of what a SubagentStop preserves: the summary the parent's context
// received, plus the hash list of the detail it never held (§8.1 item 8, G10.1).
//
// It is marshalled with encoding/json over this declaration order, so the bytes are deterministic
// for a given capture: two observers handed the same session, agent, turn, instant, summary and
// tool window produce byte-identical blobs and therefore one content-addressed object.
type SubagentCapture struct {
	Session     core.SessionID    `json:"session"`
	Agent       string            `json:"agent"`
	Turn        core.TurnIndex    `json:"turn"`
	TS          core.UnixMilli    `json:"ts"`
	Summary     string            `json:"summary"`
	ToolResults []SubagentToolRef `json:"tool_results"`
}

// SubagentCaptureID is the tool_use identity of one subagent capture: "subagent_<session>_<turn>".
//
// A SubagentStop payload carries no tool_use_id, so the identity is derived from the only two
// values that are already unique together — exactly as VerbatimPromptID is. It is exported because
// retrieval reaches a capture by this id and must not re-derive the spelling.
func SubagentCaptureID(s core.SessionID, t core.TurnIndex) core.ToolUseID {
	return core.ToolUseID(fmt.Sprintf("%s_%s_%d", subagentIDPrefix, s, t))
}

// OnStop closes the assistant turn and, for a subagent, captures the detail the parent never held.
//
// It returns an error ONLY for ctx.Err(); every I/O failure is absorbed by soft (decision 7), and
// the Output is always empty because neither Stop nor SubagentStop emits anything.
func (o *observer) OnStop(ctx context.Context, e Event, subagent bool) (Output, error) {
	var out Output
	err := o.timed(histStop, func() error {
		var err error
		out, err = o.onStop(ctx, e, subagent)
		return err
	})
	return out, err
}

func (o *observer) onStop(ctx context.Context, e Event, subagent bool) (Output, error) {
	// Nothing before the ctx check, and the clock is read exactly once (decision 11).
	if err := ctx.Err(); err != nil {
		return hookio.Empty(), err
	}
	now := o.now()
	st := o.session(e.SessionID)
	st.mu.Lock()
	defer st.mu.Unlock()

	if subagent {
		o.captureSubagent(ctx, st, e, now)
	} else {
		o.mainAgentStop(ctx, st, now)
	}
	return hookio.Empty(), nil
}

// mainAgentStop is the subagent == false branch: a turn boundary and nothing else.
//
// There is deliberately NO store write. The assistant's own text is not a tool result — §8.1 item
// 1 is about tool output — and Claude Code's transcript already holds it verbatim, so storing it
// here would duplicate the host's own durable copy at the cost of a chunking pass on every turn.
func (o *observer) mainAgentStop(ctx context.Context, st *sessionState, now core.UnixMilli) {
	if o.opt.Grammar != nil {
		o.opt.Grammar.Append(grammar.Symbol(stopSymbol))
	}
	// Decision 4: the assistant turn has ended, so the counter moves in this direction too.
	st.Turn++
	o.soft(stageStopFlush, o.opt.Graph.Flush(ctx))
	st.LastTS = now
}

// captureSubagent is the subagent == true branch: §8.1 item 8's nine steps.
//
// The ordering matters in one place beyond the obvious: the record, the DAG node, the id and the
// capture's own Turn field are all minted from the PRE-increment st.Turn, so they agree with each
// other, and only then does the turn advance (resolved decision 4). Step 0's turn resolution runs
// BEFORE the blob is built for exactly that reason — it moves st.Turn, so it has to move it while
// the four still agree.
func (o *observer) captureSubagent(ctx context.Context, st *sessionState, e Event, now core.UnixMilli) {
	// 0. SP08-D2, the identity rule. A SubagentStop payload carries no tool_use_id, so the record's
	//    id is DERIVED from this process's turn counter — which a redelivery re-derives differently
	//    (0 on a fresh daemon, or the restored turn) and so writes a second capture for one host
	//    event. Both halves of the rule are here:
	//
	//    (a) Recognition. If this observation already published a capture, absorb the redelivery:
	//        no blob, no record, no DAG, no second turn consumed. The session still advances past
	//        the record that was found, or the next Stop would re-mint the id just recognized.
	//
	//        Three pieces of bookkeeping every OTHER exit of this function performs are deliberately
	//        skipped here, because this delivery is not a new host event — it is one already
	//        observed, arriving again:
	//
	//          - st.SubagentSince stays put. The window closed on the first run; re-closing it now
	//            would drop every tool use that arrived since from the NEXT capture's hash list,
	//            which is the G10.1 detail the capture exists for.
	//          - st.LastTS stays put, so it keeps naming the last HOST event. Advancing it would
	//            claim the event happened at the redelivery's instant — minutes later, at flush
	//            time — and §6.6's GapSeconds, measured against it, would understate the real idle
	//            gap before the next event. In this process the first run already set it correctly.
	//          - the DAG is not flushed, because an absorbed redelivery adds no node and no edge.
	//
	//        TestRedelivery_AbsorbedStopDoesNotRestampTheSessionClock pins the first two.
	obs := ObservationFrom(ctx)
	if prior, ok := o.observationRecord(ctx, obs, e.SessionID, opObserveStop); ok {
		adoptTurn(st, prior)
		o.count(counterRedelivery)
		return
	}
	//    (b) Collision-aware minting, for a delivery nobody has published yet. Recognition cannot
	//        help there, and the turn this process holds may already be spoken for: two Stops of one
	//        session can be in flight together (the worker pool has at least two workers and the
	//        transport ACK is written before a worker touches the job), so turns are taken in an
	//        order that is not the WAL's. Minting blind into an occupied turn makes RecordToolUse
	//        answer ErrAppendOnly and the capture is soft-dropped — one host event, no record.
	//        Gated on a leased identity: an in-process caller has no delivery to lose, and
	//        TestOnStop_CaptureIsDeterministic pins that it keeps re-minting the SAME id.
	if obs != "" {
		if t, found := o.freeDerivedTurn(ctx, int(st.Turn), func(i int) core.ToolUseID {
			return SubagentCaptureID(e.SessionID, core.TurnIndex(i))
		}); found {
			st.Turn = core.TurnIndex(t)
		}
	}

	// 1-2. Who produced this, and what prose the parent's context received.
	agent := subagentName(e)
	summary := subagentSummary(e)
	if summary == "" {
		// Not a soft failure: a subagent that returned nothing quotable is ordinary, and the hash
		// list below is the half of G10.1 that cannot be reconstructed from anywhere else.
		o.opt.Log.Debug("observer: subagent capture has no summary",
			"session", e.SessionID, "agent", agent)
	}

	// 3. The tool-result window. min() is what keeps the slice legal after a ring eviction moved
	//    SubagentSince past the retained prefix (rememberToolUse clamps it, and this re-clamps at
	//    the read for a state file restored from an older, longer ring).
	window := st.ToolUses[min(st.SubagentSince, len(st.ToolUses)):]
	refs := make([]SubagentToolRef, 0, len(window))
	for _, t := range window {
		refs = append(refs, SubagentToolRef{
			ToolUseID: t.ID, Root: t.Root.String(), Tool: t.Tool, Path: t.Path, Bytes: t.Bytes,
		})
	}

	// 4. The blob. `capture`, never `cap`: the builtin would be shadowed for the rest of the body.
	capture := SubagentCapture{
		Session: e.SessionID, Agent: agent, Turn: st.Turn, TS: now,
		Summary: summary, ToolResults: refs,
	}
	blob, err := json.Marshal(capture)
	if err != nil {
		// Unreachable for this struct — every field is a string, an int or a slice of those — but
		// absorbed rather than ignored, because a capture that was never serialized must not be
		// followed by an index entry pointing at an object that does not exist.
		o.soft(stageStopMarshal, err)
		return
	}

	// 5. §8.1 item 1's single choke point. verbatimOptions for the same reason a prompt gets it:
	//    this is JSON this package generated, not host content, so there is nothing volatile to
	//    normalize and no near-duplicate space worth forking.
	res, err := o.opt.Store.PutBytes(ctx, blob, store.PutOptions{
		Tool: subagentStop, Path: "", Canon: verbatimOptions(), KeepRaw: true,
	})
	if err != nil {
		// The capture is lost; the TURN is not. Renumbering every later artifact around a turn the
		// session actually took would be the larger corruption (prompt.go makes the same trade).
		o.soft(stageStopPut, err)
		st.Turn++
		st.LastTS = now
		return
	}
	tok := res.Root.Tokens
	if tok == 0 && o.opt.Tokens != nil {
		tok = o.opt.Tokens.EstimateRoot(ctx, res.Root.Chunks, tokens.ClassJSON)
	}

	// 6. The index entry, at the pre-increment turn and under the derived id.
	id := SubagentCaptureID(e.SessionID, st.Turn)
	digest, preview := store.ArgsDigest(subagentArgs(agent, summary))
	rec := store.ToolUseRecord{
		ID: id, Session: e.SessionID, Turn: st.Turn, TS: now, Tool: subagentStop,
		ArgsDigest: digest, ArgsPreview: preview,
		Root: res.Root.Hash, Bytes: int64(len(blob)), Tokens: tok,
		Status: store.StatusOK, Subagent: agent, Observation: obs,
	}
	if err := o.opt.Store.RecordToolUse(ctx, rec); err != nil {
		o.soft(stageIndex, err)
	} else {
		// 6a. Publication order's second stage, which the Stop path did not previously complete:
		//     the reference joined to the capture sidecar this delivery already made durable. It is
		//     what step 0 recognizes a redelivery BY, so without it a Stop can only ever be
		//     re-captured. It stays SOFT here, unlike the tool path's: a Stop that reaches this line
		//     has already stored its blob and its record, and refusing the delivery over a failed
		//     link would redeliver a capture that is durably published — trading a missing join for
		//     a duplicate capture, which is the defect this closes.
		o.soft(stageLink, o.linkObservation(obs, rec))
	}

	// 7. §8.1 item 4. This is the one node set SP-08 still builds by hand — a subagent capture is
	//    not a transcript tool call, so there is no dag.Observed* shape for it — but the ids still
	//    come from dag's constructors and never from string concatenation.
	o.soft(stageDAG, o.opt.Graph.AddNode(dag.Node{
		ID: dag.ToolUseNode(id), Kind: dag.KindToolUse, Turn: st.Turn, TS: now,
		Pos: o.advancePos(st, tok), Ref: subagentStop, Root: res.Root.Hash, Tokens: tok,
	}))
	for _, r := range refs {
		// D-1, no exception: the subagent's RESULTS are the earlier/producer end and the capture
		// is the consumer, so every edge runs toolresult:<ref> → tooluse:<capture>. Reversed, a
		// backward slice from the capture would never reach the detail it exists to point at.
		o.soft(stageDAG, o.opt.Graph.AddEdge(dag.Edge{
			From: dag.ToolResultNode(r.ToolUseID), To: dag.ToolUseNode(id),
			Kind: dag.EdgeConsumes, Weight: edgeWeight, Turn: st.Turn,
		}))
	}
	o.enrol(st, dag.ToolUseNode(id))

	// 8. The window closes here, and the capture occupies a turn slot of its own (decision 4).
	st.SubagentSince = len(st.ToolUses)
	st.Turn++
	st.LastTS = now
	o.count(counterSubagentCapture)
	o.soft(stageStopFlush, o.opt.Graph.Flush(ctx))
}

// subagentName reads the subagent's name out of the ONE Extra key the daemon restores.
//
// The three-way host-name resolution (subagent_type → agent_name → agent) happens in the HOOK
// CLIENT, not here: hookio.Event.Extra is tagged `json:"-"`, so it is populated by
// hookio.ReadEvent in the client process and dropped by ipc.EncodeRequest. The client's rawExtras
// sends {"subagent":true,"agent":"<name>"} and the daemon's resolveEvent merges it back into
// Extra, which is why this function reads a single key and falls back to the literal
// defaultSubagentName when it is absent, malformed, not a string, or an empty one.
func subagentName(e Event) string {
	var name string
	if err := json.Unmarshal(e.Extra[extraAgentKey], &name); err == nil && name != "" {
		return name
	}
	return defaultSubagentName
}

// subagentArgsDoc is the shape store.ArgsDigest digests a capture as. A SubagentStop payload has
// no tool_input, so one is synthesized: without it every capture in every session would collapse
// onto the single empty-args digest, and ArgsPreview — §5.8's ≤120-byte cap — would carry nothing.
type subagentArgsDoc struct {
	Agent   string `json:"agent"`
	Summary string `json:"summary"`
}

// subagentArgs renders {"agent":…,"summary":…} for store.ArgsDigest.
func subagentArgs(agent, summary string) json.RawMessage {
	// Marshalling a two-string struct cannot fail, and ArgsDigest digests bytes it cannot parse
	// rather than rejecting them, so even the impossible branch still yields a stable digest.
	b, _ := json.Marshal(subagentArgsDoc{Agent: agent, Summary: summary})
	return b
}

// subagentSummary is the prose half of the capture, from whichever of two sources has it.
//
// The payload is preferred because it is what the parent's context actually received. The
// transcript tail is the fallback for the hosts and versions that do not put the subagent's reply
// in tool_response, and "" is an acceptable third answer: the hash list is the part of G10.1 that
// nothing else can reconstruct, so a missing summary must never abort the capture.
func subagentSummary(e Event) string {
	if len(e.ToolResponse) > 0 {
		if s := responseText(e); len(s) > 0 {
			return string(s)
		}
	}
	return tailAssistantText(e.TranscriptPath, transcriptTailBytes)
}

// transcriptLine is the subset of one Claude Code transcript JSONL line this package reads.
//
// Type and IsSidechain are parsed but deliberately NOT filtered on. A subagent's messages are
// written into the parent's transcript tagged isSidechain, so narrowing the scan to them would
// look tighter — and would return "" for every host build that spells the tag differently or omits
// it. The predicate is the one §8.1 item 8 states, "the last assistant message with a text block";
// the two fields stay in the shape so the line's discriminators are visible to the next reader
// rather than rediscovered from a transcript dump.
type transcriptLine struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// tailAssistantText returns the text of the last assistant message in path's final maxBytes.
//
// Every failure — a missing file, a directory, a permission error, a short read, an unparseable
// line — returns "" rather than an error, because this is a best-effort enrichment of a capture
// that is written either way, and because §12.3 gives a hook no failure mode but "record less".
func tailAssistantText(path string, maxBytes int64) string {
	if path == "" || maxBytes <= 0 {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return ""
	}
	f, err := os.Open(path) //nolint:gosec // the host names its own transcript; §3.4 has no allow-list
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	off := max(int64(0), fi.Size()-maxBytes)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return ""
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}

	// The drop is CONDITIONAL on having actually truncated, which is what max(0, size-maxBytes)
	// above is saying: when the window covers the whole file, line 1 is a whole line, and
	// discarding it would lose a short transcript's only assistant message.
	if off > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return "" // the window landed inside a single enormous line: no complete line at all
		}
		b = b[i+1:]
	}

	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if text, ok := assistantText(lines[i]); ok {
			return text
		}
	}
	return ""
}

// assistantText returns the joined text blocks of one transcript line, and whether the line was an
// assistant message carrying at least one.
//
// Only blocks whose type is "text" contribute. An assistant turn that made a tool call carries
// tool_use blocks alongside its prose, and those have no text at all, so including them would
// pad the summary with empty lines that mean nothing to a reader of the capture.
func assistantText(line []byte) (string, bool) {
	if len(bytes.TrimSpace(line)) == 0 {
		return "", false
	}
	var parsed transcriptLine
	if err := json.Unmarshal(line, &parsed); err != nil {
		return "", false
	}
	if parsed.Message.Role != transcriptRoleAssistant {
		return "", false
	}
	var texts []string
	for _, block := range parsed.Message.Content {
		if block.Type == transcriptBlockText {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) == 0 {
		return "", false
	}
	return strings.Join(texts, "\n"), true
}
