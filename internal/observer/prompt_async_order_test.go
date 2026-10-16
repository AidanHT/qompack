package observer

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/store"
)

// V7 F4b renders UserPromptSubmit async (pluginmanifest.WithAsyncPrompt). The host then no longer
// waits for the prompt hook before the turn runs, so the prompt's capture can reach the observer
// after its turn's first tool capture, or even after the turn's Stop. The observer numbers turns by
// arrival (decision 4: a prompt is recorded AT Turn and moves it on, a main-agent Stop moves it on,
// a tool use is recorded at whatever Turn is when it arrives).
//
// This test pins what each arrival order produces, beside the synchronous order, so a change to the
// attribution is a visible diff and G0.7's third run (amendment 5) is checked against a known
// baseline: the tool uses' turns, Turn, PromptSince and LastPromptTurn, and the session's prompt
// records (store.SessionPrompts), which are what internal/checkpoint derives intent:original (the
// first) and intent:evolution:<n> (the rest, in turn order) from, verbatim.
//
// What it shows: the user's intent is unaffected — both prompts are captured verbatim, in host
// order, under every arrival — but a tool use that arrives before its prompt is recorded at the
// prompt's own turn, not the turn after it, and PromptSince no longer starts at the turn's first
// tool use. That is the misattribution G0.7's "no misassigned turn" check exists to catch before
// F4b can be switched on.

const (
	asyncOrderPromptA = "why did the pool reset?\n  (verbatim: two spaces, a tab\there)"
	asyncOrderPromptB = "and what changed just before that?"
)

// asyncOrderWant is what one arrival order produces.
type asyncOrderWant struct {
	toolTurns      map[core.ToolUseID]core.TurnIndex
	promptTurns    []core.TurnIndex // prompt A's, then prompt B's
	turn           core.TurnIndex
	promptSince    int
	lastPromptTurn core.TurnIndex
}

func TestPromptAsyncOrder_AttributionUnderEachArrivalOrder(t *testing.T) {
	type step func(r *rdxRig)
	prompt := func(text string) step {
		return func(r *rdxRig) {
			r.t.Helper()
			_, err := r.o.OnUserPrompt(context.Background(), promptOf(text))
			require.NoError(r.t, err)
		}
	}
	tool := func(id string) step {
		return func(r *rdxRig) {
			r.t.Helper()
			out, err := r.o.OnToolUse(context.Background(), readOf(id, "src/"+id+".go", "package p // "+id+"\n"))
			require.NoError(r.t, err)
			require.Equal(r.t, hookio.Empty(), out)
		}
	}
	stop := func(r *rdxRig) {
		r.t.Helper()
		out, err := r.o.OnStop(context.Background(), stopOf(false), false)
		require.NoError(r.t, err)
		require.Equal(r.t, hookio.Empty(), out)
	}
	// Every order shares the first turn (prompt A, a1, Stop), captured synchronously; only where
	// prompt B lands among its turn's b1, b2 and Stop differs.
	firstTurn := []step{prompt(asyncOrderPromptA), tool("a1"), stop}

	for _, tc := range []struct {
		name   string
		second []step
		want   asyncOrderWant
	}{
		{
			name:   "synchronous: prompt before its tools",
			second: []step{prompt(asyncOrderPromptB), tool("b1"), tool("b2"), stop},
			want: asyncOrderWant{
				toolTurns:   map[core.ToolUseID]core.TurnIndex{"a1": 1, "b1": 3, "b2": 3},
				promptTurns: []core.TurnIndex{0, 2},
				turn:        4, promptSince: 1, lastPromptTurn: 2,
			},
		},
		{
			name:   "async: prompt after its turn's first tool capture",
			second: []step{tool("b1"), prompt(asyncOrderPromptB), tool("b2"), stop},
			want: asyncOrderWant{
				// b1 is recorded at the prompt's turn, 2, where the synchronous order has 3.
				toolTurns:   map[core.ToolUseID]core.TurnIndex{"a1": 1, "b1": 2, "b2": 3},
				promptTurns: []core.TurnIndex{0, 2},
				turn:        4, promptSince: 2, lastPromptTurn: 2,
			},
		},
		{
			name:   "async: prompt after its turn's Stop",
			second: []step{tool("b1"), tool("b2"), stop, prompt(asyncOrderPromptB)},
			want: asyncOrderWant{
				// Both tool uses take turn 2, the Stop moves Turn to 3, and the prompt is recorded
				// there: the turn after the work it asked for.
				toolTurns:   map[core.ToolUseID]core.TurnIndex{"a1": 1, "b1": 2, "b2": 2},
				promptTurns: []core.TurnIndex{0, 3},
				turn:        4, promptSince: 3, lastPromptTurn: 3,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRdxRig(t)
			for _, s := range append(append([]step{}, firstTurn...), tc.second...) {
				s(r)
			}
			ctx := context.Background()

			for id, want := range tc.want.toolTurns {
				rec, err := r.st.ToolUse(ctx, id)
				require.NoError(t, err, id)
				require.Equal(t, want, rec.Turn, "tool use %s", id)
			}

			st := r.o.session(testSession)
			st.mu.Lock()
			gotTurn, gotSince, gotLast := st.Turn, st.PromptSince, st.LastPromptTurn
			st.mu.Unlock()
			require.Equal(t, tc.want.turn, gotTurn, "Turn")
			require.Equal(t, tc.want.promptSince, gotSince, "PromptSince")
			require.Equal(t, tc.want.lastPromptTurn, gotLast, "LastPromptTurn")

			// The prompt records the checkpoint reads intent from: A first (intent:original), then
			// B (intent:evolution:1), each byte for byte what the user typed.
			sp, ok := r.st.(store.SessionPrompts)
			require.True(t, ok, "the real store answers SessionPrompts")
			recs, err := sp.SessionPrompts(ctx, testSession)
			require.NoError(t, err)
			require.Len(t, recs, 2)
			for i, want := range []string{asyncOrderPromptA, asyncOrderPromptB} {
				require.Equal(t, VerbatimPromptID(testSession, tc.want.promptTurns[i]), recs[i].ID, "prompt %d", i)
				require.Equal(t, tc.want.promptTurns[i], recs[i].Turn, "prompt %d", i)
				rc, err := r.st.Open(ctx, recs[i].Root)
				require.NoError(t, err)
				body, err := io.ReadAll(rc)
				require.NoError(t, rc.Close())
				require.NoError(t, err)
				require.Equal(t, want, string(body), "prompt %d is stored verbatim", i)
			}
		})
	}
}
