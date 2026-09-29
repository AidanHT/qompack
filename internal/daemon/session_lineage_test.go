package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/store"
)

// F-UAT06-1 through the daemon: the session.start route records a fork's lineage from the host's
// SessionStart(source=fork), and the compact rehydration of that fork reads it, so section 2 shows
// the parent's original instead of the fork's first prompt.

// The UAT-06 conversation (plans/sdd/V6-closeout/live/uat/UAT-06/section2-in-order.txt).
const (
	lineageParentAsk = "We are building a rate limiter for the Kite API gateway. Requirement: allow 100 " +
		"requests per minute per client."
	lineageCorrection = "Correction: the limit must be 60 requests per minute per client, not 100."
	lineageForkFirst  = "We are continuing in a forked session. In one sentence: what is the current " +
		"per-client rate limit requirement?"
)

const (
	lineageParent = core.SessionID("sess-lineage-parent")
	lineageFork   = core.SessionID("sess-lineage-fork")
)

func TestSessionStartFork_RecordsTheForksLineage(t *testing.T) {
	root := t.TempDir()
	clk := core.SystemClock()
	w, err := checkpoint.OpenWriter(root, testConfig(), logging.Nop(), obs.New(clk), clk)
	require.NoError(t, err)
	o := NewOptions(root, testConfig())
	o.Bind(func(s *Services) { s.Checkpoints = w })
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	t.Cleanup(func() { joinReplyWork(t, dd) })

	for _, source := range []string{"startup", "resume"} {
		sess := core.SessionID("sess-lineage-" + source)
		resp := dd.dispatchOp(context.Background(), startRequest(dd, sess, source, "", ""))
		require.True(t, resp.OK)
		l, err := checkpoint.ReadLineage(paths.Of(root), sess)
		require.NoError(t, err)
		require.Nil(t, l, "a %s session is not a fork", source)
	}

	// A start the host stamped a minute before the daemon handles it (a spooled start, replayed):
	// the fork's moment is the host's stamp, which is what the parent's prompt records carry too.
	req := startRequest(dd, lineageFork, "fork", "", "")
	req.TS -= core.UnixMilli(time.Minute / time.Millisecond)
	resp := dd.dispatchOp(context.Background(), req)
	require.True(t, resp.OK)
	l, err := checkpoint.ReadLineage(paths.Of(root), lineageFork)
	require.NoError(t, err)
	require.NotNil(t, l, "SessionStart(source=fork) records the fork's lineage")
	require.Equal(t, checkpoint.LineageFork, l.Source)
	require.Zero(t, l.ParentSeq, "no checkpoint existed, so the parent is recorded as unknown")
	require.Empty(t, l.ParentSession, "no other session had said anything")
	require.Equal(t, req.TS, l.At, "the fork started when the host says it did")
}

// lineageReader answers every read with the fork's checkpoint, as the fixed checkpointer seals it.
type lineageReader struct{ cp checkpoint.Checkpoint }

func (r lineageReader) Latest(context.Context, core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	return r.cp, checkpoint.Ref{Seq: r.cp.Seq}, nil
}

func (r lineageReader) Get(context.Context, core.CheckpointSeq) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	return r.cp, checkpoint.Ref{Seq: r.cp.Seq}, nil
}
func (r lineageReader) List(context.Context) ([]checkpoint.Ref, error) { return nil, nil }
func (r lineageReader) Chain(context.Context, core.CheckpointSeq) ([]checkpoint.Checkpoint, error) {
	return []checkpoint.Checkpoint{r.cp}, nil
}
func (r lineageReader) Verify(context.Context) ([]core.CheckpointSeq, error) { return nil, nil }

// capturePrompt records one verbatim prompt capture the way the observer does: the bytes, and the
// prompt_<session>_<turn> index record.
func capturePrompt(t *testing.T, st store.Store, s core.SessionID, turn core.TurnIndex, text string) {
	t.Helper()
	ctx := context.Background()
	res, err := st.PutBytes(ctx, []byte(text), store.PutOptions{
		Tool: "UserPromptSubmit", Canon: canon.Options{Strip: []canon.Class{}},
	})
	require.NoError(t, err)
	require.NoError(t, st.RecordToolUse(ctx, store.ToolUseRecord{
		ID: core.ToolUseID("prompt_" + string(s) + "_" + strconv.Itoa(int(turn))), Session: s, Turn: turn,
		TS: core.UnixMilli(1_700_000_000_000 + int64(turn)), Tool: "UserPromptSubmit", Root: res.Root.Hash,
	}))
}

func TestCompactRehydration_ForkShowsTheParentsOriginal(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig()
	st, err := store.Open(root, cfg, store.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	capturePrompt(t, st, lineageParent, 0, lineageParentAsk)
	capturePrompt(t, st, lineageParent, 2, lineageCorrection)
	capturePrompt(t, st, lineageFork, 0, lineageForkFirst)

	// The lineage NoteFork records when the fork starts after the parent's checkpoint 0002.
	rec, err := json.Marshal(checkpoint.Lineage{
		Version: 1, Session: lineageFork, Source: checkpoint.LineageFork,
		ParentSeq: 2, ParentSession: lineageParent, OriginSession: lineageParent, At: 1,
	})
	require.NoError(t, err)
	state := paths.Of(root).State
	require.NoError(t, os.MkdirAll(paths.Long(state), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(state, "lineage-"+string(lineageFork)+".json")), rec, 0o600))

	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	cp.Session, cp.Seq = lineageFork, 4
	cp.UserIntent.Original = lineageParentAsk
	cp.UserIntent.Evolution = []string{lineageCorrection, lineageForkFirst}

	log := newRecordingLogger()
	o := NewOptions(root, cfg)
	o.Log = log
	var mode func() contract.Mode
	o.Bind(func(s *Services) { mode = s.Mode })
	svc := NewRehydrateService(RehydrateOptions{
		ProjectRoot: root,
		Cfg:         cfg,
		Checkpoints: lineageReader{cp: cp},
		Deps:        rehydrate.Deps{Store: st},
		Reporter:    rehydrate.NewReporter(root, log),
		Mode:        func() contract.Mode { return mode() },
		Log:         log,
	})
	BindRehydrate(&o, svc)
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	t.Cleanup(func() { joinReplyWork(t, dd) })
	// This row is about what the rehydration says, not how fast: the answer budget is widened so a
	// loaded machine cannot turn the rehydration into the deferred note (timing is pinned by
	// session_start_compact_test.go).
	dd.compactBudget = time.Minute

	resp := dd.dispatchOp(context.Background(), compactRequest(root, lineageFork))
	require.True(t, resp.OK)
	ac := additionalContext(resp.Output)

	require.Contains(t, ac, "> "+lineageParentAsk+"\n", "section 2's original is the parent's")
	require.Contains(t, ac, "(forked session: the original request of session "+string(lineageParent)[:8])
	require.Contains(t, ac, "> "+lineageForkFirst+"\n", "the fork's first prompt is an evolution entry")
	require.Less(t, strings.Index(ac, lineageParentAsk), strings.Index(ac, lineageForkFirst))
	require.NotContains(t, ac, "intent_mismatch", "the parent's original is not overridden")
}
