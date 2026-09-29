package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The two wave-14 follow-ups of D46, reproduced through the shipped composition (the
// compactLoadRig: runDaemon through Dispatch and the shipped hook clients in process).
//
//   - The observer adopted a segment only at SessionStart, so after a scheduler roll that no
//     SessionStart followed it kept enrolling against the closed segment, and SessionEnd closed that
//     one again while the successor stayed open and was never encoded. The roll here is the one the
//     live lane met: a compaction the host started (PreCompact) and never finished (no
//     SessionStart(compact)).
//   - A daemon restarted in the middle of a session left the scheduler bound to nothing for the rest
//     of it, because no SessionStart reaches the new daemon.

// rolledSegment is one of the rig session's segments as index/segments.jsonl records it.
type rolledSegment struct {
	ID        core.SegmentID
	StartTurn core.TurnIndex
	Closed    bool
	EndTurn   core.TurnIndex
}

// rigSegments replays index/segments.jsonl's open and close records for the rig's session, in id
// order.
func rigSegments(t *testing.T, root string) []rolledSegment {
	t.Helper()
	f, err := os.Open(paths.Long(filepath.Join(paths.Of(root).Index, "segments.jsonl")))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	byID := map[core.SegmentID]*rolledSegment{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec struct {
			Op string         `json:"op"`
			ID core.SegmentID `json:"id"`
			S  core.SessionID `json:"s"`
			St core.TurnIndex `json:"st"`
			ET core.TurnIndex `json:"et"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue // a torn tail the daemon is still writing
		}
		switch rec.Op {
		case "open":
			if rec.S == compactLoadSession {
				byID[rec.ID] = &rolledSegment{ID: rec.ID, StartTurn: rec.St}
			}
		case "close":
			if seg := byID[rec.ID]; seg != nil {
				seg.Closed, seg.EndTurn = true, rec.ET
			}
		}
	}
	require.NoError(t, sc.Err())
	out := make([]rolledSegment, 0, len(byID))
	for _, seg := range byID {
		out = append(out, *seg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// depsLine is the part of a dag/deps.jsonl node or edge record these rows read.
type depsLine struct {
	Type   string      `json:"type"`
	ID     string      `json:"id"`
	From   string      `json:"from"`
	To     string      `json:"to"`
	Tokens core.Tokens `json:"tokens"`
	Ref    string      `json:"ref"`
}

// rigGraph reads dag/deps.jsonl into its node records, by id, and its edges as from -> to pairs.
func rigGraph(t *testing.T, root string) (map[string]depsLine, map[[2]string]bool) {
	t.Helper()
	f, err := os.Open(paths.Long(filepath.Join(paths.Of(root).DAG, "deps.jsonl")))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	nodes, edges := map[string]depsLine{}, map[[2]string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var l depsLine
		require.NoError(t, json.Unmarshal(sc.Bytes(), &l))
		switch l.Type {
		case "node":
			nodes[l.ID] = l
		case "edge":
			edges[[2]string{l.From, l.To}] = true
		}
	}
	require.NoError(t, sc.Err())
	return nodes, edges
}

// rigReadAndSettle makes one Read of rel, a Stop, and waits until the daemon has observed both: the
// Read indexed, and a reply-bearing prompt answered after it (the daemon publishes a session's
// leased arrivals in order, so that answer is the point by which the Read's observer and the
// scheduler's tap have run).
func rigReadAndSettle(t *testing.T, r *compactLoadRig, id, rel, content, next string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(r.root, "src"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(r.root, filepath.FromSlash(rel)), []byte(content), 0o600))
	r.hook(t, []string{"observe", "tool"}, uat03Read(r, id, rel, content))
	r.hook(t, []string{"observe", "stop"}, r.base("Stop"))
	require.Eventually(t, func() bool {
		adminDrain(r.root)
		return indexedToolUses(r.root, []string{id})
	}, bootstrapUpBound, bootstrapUpTick, "%s never reached index/tool_use.jsonl", id)
	prompt := r.base("UserPromptSubmit")
	prompt["prompt"] = next
	r.hook(t, []string{"observe", "prompt"}, prompt)
}

// TestAHostFailedCompactionsSuccessorClosesAtSessionEnd: a Read, a compaction the host starts and
// never finishes, a second Read, and the session's end. The compaction rolls the segment; the
// second Read must be enrolled in the successor, the rolled segment must get its DAG node, and
// SessionEnd must close the successor rather than refuse to close the rolled one again.
func TestAHostFailedCompactionsSuccessorClosesAtSessionEnd(t *testing.T) {
	r, stop := newCompactLoadRig(t)
	defer stop()
	const before, after = "toolu_w14_before_compact", "toolu_w14_after_compact"

	start := r.base("SessionStart")
	start["source"] = "startup"
	r.hook(t, []string{"session-start"}, start)
	first := r.base("UserPromptSubmit")
	first["prompt"] = "Read src/roll_1.py."
	r.hook(t, []string{"observe", "prompt"}, first)
	rigReadAndSettle(t, r, before, "src/roll_1.py", "A = 1\n", "Now compact.")

	pc := r.base("PreCompact")
	pc["trigger"] = "auto"
	r.hook(t, []string{"checkpoint"}, pc) // no SessionStart(compact) follows: the compaction failed
	segs := rigSegments(t, r.root)
	require.Len(t, segs, 2, "fixture sanity: the compaction rolled the session's segment")
	require.True(t, segs[0].Closed)
	require.False(t, segs[1].Closed)

	rigReadAndSettle(t, r, after, "src/roll_2.py", "B = 2\n", "That is all.")
	r.hook(t, []string{"flush"}, r.base("SessionEnd"))
	require.Eventually(t, func() bool {
		s := rigSegments(t, r.root)
		return len(s) == 2 && s[1].Closed
	}, bootstrapUpBound, bootstrapUpTick, "SessionEnd must close the session's open segment, the compaction's successor")
	stop() // the graph was flushed at SessionEnd; stopping the daemon leaves deps.jsonl final

	segs = rigSegments(t, r.root)
	rolled, succ := fmt.Sprintf("segment:%d", segs[0].ID), fmt.Sprintf("segment:%d", segs[1].ID)
	nodes, edges := rigGraph(t, r.root)
	require.Contains(t, nodes, rolled, "the rolled segment gets its DAG segment node")
	require.Positive(t, nodes[rolled].Tokens, "spanning the Read enrolled in it")
	require.Equal(t, fmt.Sprintf("%d-%d", segs[0].StartTurn, segs[0].EndTurn), nodes[rolled].Ref)
	require.Contains(t, nodes, succ, "SessionEnd builds the successor's node")
	require.True(t, edges[[2]string{rolled, succ}], "the chain edge links the two")
	require.True(t, edges[[2]string{"toolresult:" + after, succ}], "the Read after the roll is enrolled in the successor")
	require.False(t, edges[[2]string{"toolresult:" + after, rolled}], "and not against the closed segment")
	require.True(t, edges[[2]string{"toolresult:" + before, rolled}], "the Read before it stays in the rolled one")
}

// schedulerDoc is the part of state/scheduler.json these rows read.
type schedulerDoc struct {
	Session           core.SessionID `json:"session"`
	MaxTurn           core.TurnIndex `json:"max_turn"`
	OpenSegmentTokens core.Tokens    `json:"open_segment_tokens"`
}

func readSchedulerDoc(t *testing.T, root string) schedulerDoc {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, "scheduler.json")))
	require.NoError(t, err)
	var doc schedulerDoc
	require.NoError(t, json.Unmarshal(b, &doc))
	return doc
}

// TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook: the daemon stops in the middle of a
// session and a new one starts; the session goes on with a prompt and a Read and no SessionStart.
// The new daemon's scheduler must bind the session on its first hook, restoring what its
// predecessor persisted, so its own shutdown persists the session's account with the new Read in it.
// Unbound, it persisted nothing and the state file still described the session as the first daemon
// left it.
func TestARestartedDaemonBindsTheLiveSessionOnItsFirstHook(t *testing.T) {
	r, stop := newCompactLoadRig(t)
	defer stop()
	const before, after = "toolu_w14_before_restart", "toolu_w14_after_restart"

	start := r.base("SessionStart")
	start["source"] = "startup"
	r.hook(t, []string{"session-start"}, start)
	first := r.base("UserPromptSubmit")
	first["prompt"] = "Read src/restart_1.py."
	r.hook(t, []string{"observe", "prompt"}, first)
	rigReadAndSettle(t, r, before, "src/restart_1.py", "A = 1\n", "Wait a moment.")
	stop() // the daemon exits mid-session; its shutdown persists the scheduler's state
	require.Equal(t, compactLoadSession, readSchedulerDoc(t, r.root).Session, "fixture sanity")

	restart := bootstrapDaemon(t, r.root)
	defer restart()
	next := r.base("UserPromptSubmit")
	next["prompt"] = "Now read src/restart_2.py."
	r.hook(t, []string{"observe", "prompt"}, next)
	rigReadAndSettle(t, r, after, "src/restart_2.py", "B = 2\n", "Thanks.")
	restart() // the restarted daemon's shutdown persists the scheduler's state, when it is bound

	st, err := store.Open(r.root, config.Defaults(), store.Deps{Clock: testClock()})
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	recBefore, err := st.ToolUse(ctx, before)
	require.NoError(t, err)
	recAfter, err := st.ToolUse(ctx, after)
	require.NoError(t, err)
	require.Positive(t, recAfter.Tokens, "fixture sanity")

	doc := readSchedulerDoc(t, r.root)
	require.Equal(t, compactLoadSession, doc.Session)
	require.Equal(t, recAfter.Turn, doc.MaxTurn, "the restarted daemon's scheduler observed the live session's Read")
	require.Equal(t, recBefore.Tokens+recAfter.Tokens, doc.OpenSegmentTokens,
		"its open-segment account is the restored one plus the new Read")
}
