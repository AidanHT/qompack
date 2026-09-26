package eval

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// TestSpliceForks_WindowStopsAtTheNextCompaction: two compaction events three turns apart with a
// horizon of five; the first fork's window ends at the second event, so its later actions are cut,
// and every logged action outside both windows survives in order.
func TestSpliceForks_WindowStopsAtTheNextCompaction(t *testing.T) {
	var logged []Action
	for i := range 12 {
		logged = append(logged, Action{Turn: core.TurnIndex(i), Tool: "log"})
	}
	forks := map[core.TurnIndex][]Action{
		2: {{Turn: 3, Tool: "a"}, {Turn: 4, Tool: "a"}, {Turn: 5, Tool: "a"}, {Turn: 6, Tool: "a"}},
		5: {{Turn: 6, Tool: "b"}},
	}
	got := spliceForks(logged, []core.TurnIndex{5, 2}, forks, 5)

	var turns []core.TurnIndex
	var tools []string
	for _, a := range got {
		turns = append(turns, a.Turn)
		tools = append(tools, a.Tool)
	}
	require.Equal(t, []core.TurnIndex{0, 1, 2, 3, 4, 5, 6, 11}, turns)
	require.Equal(t, []string{"log", "log", "log", "a", "a", "a", "b", "log"}, tools,
		"event 2's window is (2, 5]; event 5's is (5, 10]; turn 11 is outside both")
}

// TestSpliceForks_WindowIsTheDivergenceHorizon pins the live window to what divergence scores:
// for an event at `at` with horizon k, the fork replaces exactly the turns horizonActions scores,
// (at, at+k], so every action inside the scored horizon is the model's and every one outside it
// is the log's. (The deterministic demand window, Demands(s, at, at+k), is [at+1, at+k) — one
// turn shorter; see the note in Replay.)
func TestSpliceForks_WindowIsTheDivergenceHorizon(t *testing.T) {
	const at, k = core.TurnIndex(3), 5
	var logged, forked []Action
	for i := range 14 {
		logged = append(logged, Action{Turn: core.TurnIndex(i), Tool: "log"})
	}
	for i := range k + 3 {
		forked = append(forked, Action{Turn: at + core.TurnIndex(i+1), Tool: "fork"})
	}
	got := spliceForks(logged, []core.TurnIndex{at}, map[core.TurnIndex][]Action{at: forked}, k)

	scored := horizonActions(got, at, k)
	require.Len(t, scored, k)
	for _, a := range scored {
		require.Equal(t, "fork", a.Tool, "turn %d inside the horizon is the model's action", a.Turn)
	}
	for _, a := range got {
		inside := a.Turn > at && a.Turn <= at+core.TurnIndex(k)
		require.Equal(t, inside, a.Tool == "fork", "turn %d", a.Turn)
	}
}
