package eval_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// forkSession is a small logged session: a request, a read, a decision, then the compaction summary
// at turn 3, the user's next request and the work that followed it.
func forkSession() eval.Session {
	return eval.Session{
		ID: "fork-s",
		Turns: []eval.Turn{
			{Index: 0, Role: "user", Text: "fix the bug in a.go", Tokens: 10},
			{Index: 1, Role: "assistant", Text: "reading", Tokens: 5, ToolCalls: []eval.ToolCall{{
				ID: "tu1", Name: "Read", Args: json.RawMessage(`{"file_path":"a.go"}`),
				Result: json.RawMessage(`"package a // the bug is on line 3"`), Paths: []string{"a.go"},
			}}},
			{Index: 2, Role: "assistant", Text: "[decision:D1] use a map", Tokens: 5},
			{Index: 3, Role: "user", Text: "This session is being continued from a previous conversation", Tokens: 50},
			{Index: 4, Role: "user", Text: "now add tests for a.go", Tokens: 8},
			{Index: 5, Role: "assistant", Tokens: 5, ToolCalls: []eval.ToolCall{{ID: "tu2", Name: "Edit", Paths: []string{"a.go", "a_test.go"}}}},
			{Index: 6, Role: "assistant", Text: "done", Tokens: 2},
			{Index: 7, Role: "assistant", ToolCalls: []eval.ToolCall{{ID: "tu3", Name: "Bash"}}, Tokens: 2},
		},
		CompactionAt: []core.TurnIndex{3},
	}
}

func TestForkPrompt_KeptBlocksInPrefixOrderThenTheNextRequest(t *testing.T) {
	s := forkSession()
	p, err := eval.ForkPrompt(s, 3, eval.KeepSet{IDs: []string{"dec:D1", "turn:0", "tu:tu1"}})
	require.NoError(t, err)
	iUser := strings.Index(p, "[user turn 0]\nfix the bug in a.go")
	iTool := strings.Index(p, "[tool Read tu1]")
	iDec := strings.Index(p, "[decision D1, turn 2]")
	require.True(t, iUser >= 0 && iTool > iUser && iDec > iTool, "prefix order, whatever the keep-set order:\n%s", p)
	require.Contains(t, p, "result: package a // the bug is on line 3")
	require.True(t, strings.HasSuffix(p, "now add tests for a.go"), "the fork continues with the next logged request")
	require.NotContains(t, p, "reading", "an unkept block is not rendered")

	_, err = eval.ForkPrompt(s, 3, eval.KeepSet{IDs: []string{"turn:5"}})
	require.ErrorContains(t, err, "not a block", "a keep-set naming a block after the compaction is a policy defect")
}

// replayHost is a HostSessionRunner that replays a recorded stream and records what it was asked.
type replayHost struct {
	stream []byte
	err    error
	got    []eval.HostSessionRequest
}

func (h *replayHost) RunSession(_ context.Context, req eval.HostSessionRequest) (eval.HostSessionCapture, error) {
	h.got = append(h.got, req)
	return eval.HostSessionCapture{Stream: h.stream}, h.err
}

// TestHostForkRunner_RunsOneSessionAndMapsItsRequests: the fork runs one host session opened with
// the rendered prompt and bounded to k turns, and each main-loop request becomes one action.
func TestHostForkRunner_RunsOneSessionAndMapsItsRequests(t *testing.T) {
	stock := liveFixture(t, smoke1Stream)
	first := strings.Join(strings.SplitAfter(string(stock), "\n")[:13], "") // turn 0 only: Read, then answer
	host := &replayHost{stream: []byte(first)}
	r := eval.HostForkRunner{Host: host}

	actions, err := r.Fork(context.Background(), forkSession(), 3, eval.KeepSet{IDs: []string{"turn:0"}}, 5)
	require.NoError(t, err)
	require.Len(t, host.got, 1)
	require.Equal(t, 5, host.got[0].MaxTurns)
	require.Len(t, host.got[0].Messages, 1)
	require.Contains(t, host.got[0].Messages[0], "fix the bug in a.go")

	require.Len(t, actions, 2)
	require.Equal(t, core.TurnIndex(4), actions[0].Turn)
	require.Equal(t, "Read", actions[0].Tool)
	require.Len(t, actions[0].Paths, 1)
	require.True(t, strings.HasSuffix(actions[0].Paths[0], "notes.txt"))
	require.Equal(t, core.TurnIndex(5), actions[1].Turn)
	require.Empty(t, actions[1].Tool)

	capped, err := r.Fork(context.Background(), forkSession(), 3, eval.KeepSet{}, 1)
	require.NoError(t, err)
	require.Len(t, capped, 1, "at most k actions")

	none, err := r.Fork(context.Background(), forkSession(), 3, eval.KeepSet{}, 0)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestHostForkRunner_Failures(t *testing.T) {
	_, err := eval.HostForkRunner{}.Fork(context.Background(), forkSession(), 3, eval.KeepSet{}, 2)
	require.ErrorIs(t, err, core.ErrNotImplemented)

	_, err = eval.HostForkRunner{Host: &replayHost{}}.Fork(context.Background(), forkSession(), 3, eval.KeepSet{}, 2)
	require.ErrorIs(t, err, eval.ErrForkNoResult, "an empty stream closed no turn")

	boom := errors.New("host did not start")
	_, err = eval.HostForkRunner{Host: &replayHost{err: boom}}.Fork(context.Background(), forkSession(), 3, eval.KeepSet{}, 2)
	require.ErrorIs(t, err, boom)

	errored := `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["overloaded"],"modelUsage":{}}` + "\n"
	_, err = eval.HostForkRunner{Host: &replayHost{stream: []byte(errored)}}.Fork(context.Background(), forkSession(), 3, eval.KeepSet{}, 2)
	require.ErrorContains(t, err, "overloaded")

	maxTurns := `{"type":"result","subtype":"error_max_turns","is_error":true,"modelUsage":{}}` + "\n"
	actions, err := eval.HostForkRunner{Host: &replayHost{stream: []byte(maxTurns)}}.Fork(context.Background(), forkSession(), 3, eval.KeepSet{}, 2)
	require.NoError(t, err, "stopping at k turns is the fork doing what it was asked")
	require.Empty(t, actions)
}

// fixedFork is a LiveRunner returning a scripted fork.
type fixedFork struct {
	actions []eval.Action
	calls   []core.TurnIndex
}

func (f *fixedFork) Fork(_ context.Context, _ eval.Session, at core.TurnIndex, _ eval.KeepSet, _ int) ([]eval.Action, error) {
	f.calls = append(f.calls, at)
	return f.actions, nil
}

// TestReplay_LiveModeSplicesTheForkIntoTheWindow: with the gate open and a runner installed, each
// compaction event is forked once and the fork's actions replace the logged actions of the event's
// window; turns outside the window keep their logged actions and no modelled repair is inserted.
func TestReplay_LiveModeSplicesTheForkIntoTheWindow(t *testing.T) {
	t.Setenv("QOMPACK_EVAL_LIVE", "1")
	h := eval.New(eval.Options{})
	live := &fixedFork{actions: []eval.Action{{Turn: 4, Tool: "Grep"}, {Turn: 5, Tool: "Write", Paths: []string{"b.go"}}}}
	setter, ok := h.(interface{ SetLiveRunner(eval.LiveRunner) })
	require.True(t, ok, "the concrete harness exposes SetLiveRunner")
	setter.SetLiveRunner(live)

	s := forkSession()
	det, err := h.Replay(context.Background(), s, eval.NewNullPolicy(config.Defaults()), eval.ReplayOptions{Deterministic: true, K: 3, Budget: eval.DefaultKeepBudget})
	require.NoError(t, err)
	require.Empty(t, live.calls, "deterministic mode never calls the runner")
	require.Contains(t, toolsOf(det), "FileRead", "deterministic mode models re-reading the dropped a.go")

	run, err := h.Replay(context.Background(), s, eval.NewNullPolicy(config.Defaults()), eval.ReplayOptions{Deterministic: false, K: 3, Budget: eval.DefaultKeepBudget})
	require.NoError(t, err)
	require.Equal(t, []core.TurnIndex{3}, live.calls)

	require.Equal(t, []string{"", "Read", "", "", "Grep", "Write", "Bash"}, toolsOf(run),
		"turns 0-3 as logged; the window (3, 6] is what the fork did, no modelled repair; turn 7 as logged")
	require.Equal(t, core.TurnIndex(7), run.Actions[6].Turn)
}

func toolsOf(r eval.Run) []string {
	out := make([]string, 0, len(r.Actions))
	for _, a := range r.Actions {
		out = append(out, a.Tool)
	}
	return out
}
