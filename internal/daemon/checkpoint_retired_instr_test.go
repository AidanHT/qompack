package daemon

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// TestCheckpointRoute_SealsWithoutRenderingOrRecordingAnInstruction is C1.18's regression test.
// No host accepts a PreCompact instruction: Claude Code has no PreCompact hookSpecificOutput variant
// and 2.1.280 rejected the whole response over one (C1.12), and custom_instructions is PreCompact
// INPUT, never a summarizer-output setter (Qompack.md §7.3, §8.5 "Retire O1's output setter"). The
// daemon nonetheless kept producing one — the checkpoint seam returned it on the IPC reply and the
// route copied it into contract history for precompact.custom_instructions_accepted to probe — and
// the hook client stripped it on the last hop. The producer is retired: the route through the REAL
// bound seam seals the checkpoint and answers the empty object, and records no instruction.
func TestCheckpointRoute_SealsWithoutRenderingOrRecordingAnInstruction(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 1, 3)

	o := &Options{ProjectRoot: f.root, Cfg: f.cfg, Log: logging.Nop(), Clock: f.clk}
	BindCheckpoint(o, f.cfg, f.w, f.src)
	d, err := New(*o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	ev := &hookio.Event{HookEventName: "PreCompact", SessionID: cpSession, Trigger: "auto", CWD: f.root}
	resp := dd.dispatchOp(context.Background(), ipc.Request{
		Op: ipc.OpCheckpoint, Session: cpSession, Reply: true, Event: ev, TS: core.NowMilli(f.clk),
	})
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Equal(t, hookio.Empty(), *resp.Output,
		"the checkpoint route answers the empty object: there is no instruction for any hop to carry")

	h := contract.LoadHistory(contract.HistoryPath(f.root))
	require.Empty(t, h.PrecompactInstr, "a retired instruction is never recorded into contract history")
	require.Equal(t, cpSession, h.LastPrecompactSession, "the PreCompact observation itself is still recorded")
	require.NotEmpty(t, h.PrecompactWallMs, "and so is the route's wall-time sample")

	entries, err := os.ReadDir(paths.Long(f.l.Checkpoints))
	require.NoError(t, err)
	var sealed []string
	for _, e := range entries {
		sealed = append(sealed, e.Name())
	}
	require.Contains(t, sealed, "0001.json", "the checkpoint is still written: retiring the instruction is not retiring the seal")
}

// TestCheckpointRoute_AnswersEmptyWhateverTheSeamReturns makes the retirement structural rather than
// a property of today's seam: an embedder's PreCompact seam that still returns an instruction — the
// shape every seam returned before C1.18 — is still CALLED in ModeFull (it is what seals), but its
// Output is neither replied nor recorded.
func TestCheckpointRoute_AnswersEmptyWhateverTheSeamReturns(t *testing.T) {
	root := t.TempDir()
	called := false
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.PreCompact = func(context.Context, hookio.Event) (hookio.Output, error) {
			called = true
			return hookio.Output{
				SystemMessage: "a banner the host discards for PreCompact",
				HookSpecificOutput: &hookio.HSO{
					HookEventName:      hookio.EventPreCompact,
					CustomInstructions: "Encode what a competent engineer with no session history would get wrong.",
				},
			}, nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	require.Equal(t, contract.ModeFull, dd.monitor.Mode(), "fixture: the route may act")

	ev := &hookio.Event{HookEventName: "PreCompact", SessionID: "sess-any-seam", Trigger: "manual", CWD: root}
	resp := dd.dispatchOp(context.Background(), ipc.Request{
		Op: ipc.OpCheckpoint, Session: "sess-any-seam", Reply: true, Event: ev, TS: core.NowMilli(dd.clk),
	})
	require.True(t, resp.OK)
	require.True(t, called, "in ModeFull the seam is still called: it is what seals the checkpoint")
	require.NotNil(t, resp.Output)
	require.Equal(t, hookio.Empty(), *resp.Output, "the route never relays a PreCompact seam's Output")
	require.Empty(t, contract.LoadHistory(contract.HistoryPath(root)).PrecompactInstr,
		"and never records an instruction into the contract history")
}
