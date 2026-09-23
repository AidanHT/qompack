package eval

// ── The live fork: eval.LiveRunner backed by a real headless host session ───────────────────────
//
// V6 close-out C5.4. LiveRunner (types.go) is §6.3's tier-3 seam: given a logged session, a
// compaction point and what a policy kept, re-execute the next k actions against a real model and
// report what the model actually did. HostForkRunner is that runner. It renders the kept blocks as
// the post-compaction context, hands it to a real `claude -p` session as the opening message, and
// maps the session's main-loop requests back onto the Action shape the divergence metrics compare.
//
// It starts no process. internal/eval is foundation-only (00-ARCHITECTURE.md §3.2) and outside the
// security job's os/exec allowlist, so the session itself is run through HostSessionRunner, whose
// one real implementation lives in tools/devtool (liveeval_host.go). A test supplies a runner that
// replays a recorded stream, which is how everything below is exercised without a network.
//
// What the fork is and is not: it is the model's real behaviour from a reconstructed context. It
// is not the host's own compaction — the host's summary, its re-attached files and its system
// prompt are not reproduced — so a fork measures what a KEEP-SET is worth to a real model, and the
// task-based trials (livetrial.go) measure what the installed plugin is worth to a real host.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// HostSessionRequest is one headless host session a fork asks for.
type HostSessionRequest struct {
	// Messages are sent in order, one user turn each, each after the previous turn's result.
	Messages []string
	// MaxTurns bounds the whole session (--max-turns).
	MaxTurns int
}

// HostSessionCapture is what the host printed during one session.
type HostSessionCapture struct {
	// Stream is the raw stream-json output.
	Stream []byte
	// RecvMS is each non-empty stream line's receive time since the process started, when known.
	RecvMS []int64
	// ExitCode is the host process's exit status.
	ExitCode int
}

// HostSessionRunner runs one real headless host session. tools/devtool supplies the one
// implementation that starts a process.
type HostSessionRunner interface {
	RunSession(ctx context.Context, req HostSessionRequest) (HostSessionCapture, error)
}

// HostForkRunner is the LiveRunner that re-executes a fork in a real host session.
type HostForkRunner struct {
	// Host runs the session. It is required.
	Host HostSessionRunner
}

var _ LiveRunner = HostForkRunner{}

// ErrForkNoResult is returned when the host session ended without closing its turn.
var ErrForkNoResult = errors.New("eval: the fork's host session produced no result")

// forkResultMaxTurns is the result subtype the host reports when --max-turns stopped the loop,
// which for a fork asked for exactly k actions is the expected ending, not a failure.
const forkResultMaxTurns = "error_max_turns"

// Fork re-executes the k actions following at, given what the compaction kept.
func (f HostForkRunner) Fork(ctx context.Context, s Session, at core.TurnIndex, keep KeepSet, k int) ([]Action, error) {
	if f.Host == nil {
		return nil, fmt.Errorf("eval: HostForkRunner has no HostSessionRunner: %w", core.ErrNotImplemented)
	}
	if k <= 0 {
		return nil, nil
	}
	prompt, err := ForkPrompt(s, at, keep)
	if err != nil {
		return nil, err
	}
	capture, err := f.Host.RunSession(ctx, HostSessionRequest{Messages: []string{prompt}, MaxTurns: k})
	if err != nil {
		return nil, fmt.Errorf("eval: fork of %s at turn %d: %w", s.ID, at, err)
	}
	stream, err := ParseHostStream(bytes.NewReader(capture.Stream), capture.RecvMS)
	if err != nil {
		return nil, fmt.Errorf("eval: fork of %s at turn %d: %w", s.ID, at, err)
	}
	if len(stream.Turns) == 0 || stream.Turns[0].Result == nil {
		return nil, fmt.Errorf("%w (session %s, turn %d, exit %d)", ErrForkNoResult, s.ID, at, capture.ExitCode)
	}
	if res := stream.Turns[0].Result; res.IsError && res.Subtype != forkResultMaxTurns {
		return nil, fmt.Errorf("eval: fork of %s at turn %d ended in %s: %s",
			s.ID, at, res.Subtype, strings.Join(res.Errors, "; "))
	}
	return ForkActions(stream, at, k), nil
}

// forkContinuePrompt closes a fork's context when the log has no later user message.
const forkContinuePrompt = "Continue the task from where it stopped."

// ForkPrompt renders the post-compaction context a fork starts from: every kept block's content in
// prefix order, then the first logged user message after at. A kept ID that is not a block of the
// prefix is an error — a keep-set naming nothing is a policy defect, and rendering around it would
// hide that.
func ForkPrompt(s Session, at core.TurnIndex, keep KeepSet) (string, error) {
	index := map[string]Block{}
	for _, b := range Blocks(s, at) {
		if _, dup := index[b.ID]; !dup {
			index[b.ID] = b
		}
	}
	kept := make([]Block, 0, len(keep.IDs))
	for _, id := range keep.IDs {
		b, ok := index[id]
		if !ok {
			return "", fmt.Errorf("eval: keep-set names %q, which is not a block of %s before turn %d", id, s.ID, at)
		}
		kept = append(kept, b)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Pos != kept[j].Pos {
			return kept[i].Pos < kept[j].Pos
		}
		return kept[i].ID < kept[j].ID
	})

	var b strings.Builder
	b.WriteString("The conversation so far was compacted. What was kept follows, in its original order.\n")
	for _, blk := range kept {
		b.WriteString("\n")
		b.WriteString(renderBlock(s, blk))
		b.WriteString("\n")
	}
	b.WriteString("\n--- end of kept context ---\n\n")
	next := forkContinuePrompt
	for _, t := range s.Turns {
		if t.Index > at && t.Role == roleUser && strings.TrimSpace(t.Text) != "" {
			next = t.Text
			break
		}
	}
	b.WriteString(next)
	return b.String(), nil
}

// renderBlock is one kept block as text.
func renderBlock(s Session, b Block) string {
	turn, ok := turnAt(s, b.Turn)
	switch {
	case !ok:
		return fmt.Sprintf("[%s]", b.ID)
	case strings.HasPrefix(b.ID, turnBlockPrefix):
		return fmt.Sprintf("[%s turn %d]\n%s", turn.Role, turn.Index, turn.Text)
	case strings.HasPrefix(b.ID, toolBlockPrefix):
		id := core.ToolUseID(strings.TrimPrefix(b.ID, toolBlockPrefix))
		for _, tc := range turn.ToolCalls {
			if tc.ID == id {
				return fmt.Sprintf("[tool %s %s]\ninput: %s\nresult: %s", tc.Name, tc.ID, compactJSON(tc.Args), resultText(tc.Result))
			}
		}
	case strings.HasPrefix(b.ID, fileBlockPrefix):
		for _, tc := range turn.ToolCalls {
			if fileBlockTools[tc.Name] && len(b.Paths) > 0 && containsPathKey(tc.Paths, b.Paths[0]) {
				return fmt.Sprintf("[file %s, as of turn %d]\n%s", b.Paths[0], turn.Index, resultText(tc.Result))
			}
		}
	case strings.HasPrefix(b.ID, elimBlockPrefix):
		for _, tc := range turn.ToolCalls {
			if tc.Name == toolRecordEliminated {
				target, approach := eliminationArgs(tc.Args)
				if elimBlockID(target, approach) == b.ID {
					return fmt.Sprintf("[eliminated] %s: %s", target, approach)
				}
			}
		}
	case strings.HasPrefix(b.ID, decisionBlockPrefix):
		return fmt.Sprintf("[decision %s, turn %d]\n%s", strings.TrimPrefix(b.ID, decisionBlockPrefix), turn.Index, turn.Text)
	}
	return fmt.Sprintf("[%s]", b.ID)
}

func turnAt(s Session, i core.TurnIndex) (Turn, bool) {
	if int(i) >= 0 && int(i) < len(s.Turns) && s.Turns[i].Index == i {
		return s.Turns[i], true
	}
	for _, t := range s.Turns {
		if t.Index == i {
			return t, true
		}
	}
	return Turn{}, false
}

func containsPathKey(ps []string, key string) bool {
	for _, p := range ps {
		if paths.Key(p) == key {
			return true
		}
	}
	return false
}

// resultText renders a tool result: a JSON string as its text, anything else as compact JSON.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return compactJSON(raw)
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

// forkPathKeys are the tool-input fields that name a file.
var forkPathKeys = []string{"file_path", "path", "notebook_path"}

// ForkActions maps a fork session's main-loop requests onto Actions numbered from at+1: one action
// per request, as BaselineRun has one per logged turn, with the first tool call's name, every path
// the request's tool calls named, and the first [decision:<id>] its text minted. At most k are
// returned. Subagent requests are not actions of the forked agent and are skipped.
func ForkActions(s HostStream, at core.TurnIndex, k int) []Action {
	var out []Action
	for _, t := range s.Turns {
		for _, r := range t.Requests {
			if r.ParentToolUseID != "" {
				continue
			}
			if len(out) >= k {
				return out
			}
			a := Action{Turn: at + core.TurnIndex(len(out)+1)}
			if len(r.ToolUses) > 0 {
				a.Tool = r.ToolUses[0].Name
			}
			seen := map[string]bool{}
			for _, u := range r.ToolUses {
				for _, p := range toolInputPaths(u.Input) {
					if !seen[p] {
						seen[p] = true
						a.Paths = append(a.Paths, p)
					}
				}
			}
			if ids := decisionIDs(r.Text); len(ids) > 0 {
				a.Decision = ids[0]
			}
			out = append(out, a)
		}
	}
	return out
}

func toolInputPaths(raw json.RawMessage) []string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	var out []string
	for _, k := range forkPathKeys {
		if v, ok := m[k].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// spliceForks replaces, for every compaction event, the logged actions in the event's window —
// (at, min(at+k, next at)] — with what the live fork actually did, truncated to the window. Logged
// actions outside every window are kept as logged. actions must be in turn order, as BaselineRun
// builds them.
func spliceForks(actions []Action, at []core.TurnIndex, forks map[core.TurnIndex][]Action, k int) []Action {
	events := append([]core.TurnIndex(nil), at...)
	sort.Slice(events, func(i, j int) bool { return events[i] < events[j] })
	out := make([]Action, 0, len(actions))
	i := 0
	for e, from := range events {
		end := from + core.TurnIndex(k)
		if e+1 < len(events) && events[e+1] < end {
			end = events[e+1]
		}
		for i < len(actions) && actions[i].Turn <= from {
			out = append(out, actions[i])
			i++
		}
		for i < len(actions) && actions[i].Turn <= end {
			i++
		}
		for _, a := range forks[from] {
			if a.Turn <= end {
				out = append(out, a)
			}
		}
	}
	return append(out, actions[i:]...)
}
