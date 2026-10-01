package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// D53(c), from the goal-and-metrics audit's section 2.2: on a disk whose fsync is slower than
// runtime.budgets.l0IngestMs the daemon moves to spool submode, and from then on the hook clients
// never connect for a hot-path event (internal/ipc client.go Send step 3): every tool result, prompt
// and Stop of the session goes only to the hook's client spool, which only a drain reads. PreCompact
// is not a hot-path op, so the compaction does reach the daemon, and it sealed at once, without
// replaying those spools: the checkpoint the host's compaction is about to depend on lacked the
// session's newest tool results. These rows run the shipped composition (the compactLoadRig) with
// the hook clients in spool submode and nothing else draining: no admin.drain, no flush, and no idle
// drain, which needs two quiet minutes.

// spoolSubmodeRowBE is the B-E (runtime.budgets.checkpointFinalizeMs) this file's daemon runs with:
// the hook client's checkpoint reply deadline less the seal's own window, so that the settle's bound
// (precompactSettleBound: B-E less checkpoint.MaxPreCompactWindow, 12 s here) and the seal's window
// together come to 13.5 s, a whole seal window before the PreCompact hook stops waiting for its
// reply (15 s). The bound is a ceiling, not a wait: a settle returns as soon as the replay is done.
//
// Why not the default 2000 ms, whose bound is 500 ms: this row's subject is that the PreCompact
// replays the session's spooled captures before it seals, not how many of them a 500 ms bound
// fits. Three replayed lines took 275-320 ms of that bound on a loaded Windows host under
// -covermode=atomic, and the hosted cover job, which runs every package at once on a runner whose
// fsync has a latency tail (Q1), ran out of it before the third line, the Stop, and sealed with it
// named as unreplayed (job 110516048246): the designed degrade, on a wall clock the row does not
// control. The default bound and that degrade have their own rows in internal/daemon:
// TestPrecompactSettleBound_IsWhatBELeavesTheSeal pins 500 ms, and
// TestPreCompactSettle_NamesWhatTheBoundLeftUnreplayed and
// TestUnreplayedDrops_CountsEveryCaptureAndNamesEachToolResult pin what a seal reports when the
// bound runs out. A settle that never replays fails this row whatever its bound.
const spoolSubmodeRowBE = checkpointReplyDeadline - checkpoint.MaxPreCompactWindow

// spoolSubmodeRowConfig is the project config that gives this file's daemon spoolSubmodeRowBE.
var spoolSubmodeRowConfig = fmt.Sprintf(`{"runtime":{"budgets":{"checkpointFinalizeMs":%d}}}`,
	spoolSubmodeRowBE/time.Millisecond)

// spoolSubmodeReadIDs are the Reads the session makes after the switch to spool submode.
var spoolSubmodeReadIDs = []string{"toolu_d53c_spooled_read_1", "toolu_d53c_spooled_read_2"}

// enterSpoolSubmode puts the rig's hook clients into spool submode the way the daemon's own
// transition does for them: state.bin, which every hook client reads once at construction, says
// spool (daemon handlers.go persistHotMode). The daemon's breach detector needs 3 x 512 over-budget
// hot-path samples to get there, which is the disk's behaviour, not this row's subject.
func enterSpoolSubmode(t *testing.T, root string) {
	t.Helper()
	st := ipc.ReadState(root, config.Defaults())
	st.Hot = ipc.HotSpool
	require.NoError(t, ipc.WriteState(root, st))
}

// clientSpools lists the hook client spools waiting in the rig's spool directory.
func clientSpools(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.Long(paths.Of(root).Spool))
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "client-") && strings.HasSuffix(e.Name(), ".ndjson") {
			out = append(out, e.Name())
		}
	}
	return out
}

// logDaemonOnFailure logs, when t has failed, what the rig's daemon wrote to its logs and which
// client spools are still waiting, once the daemon has stopped and before root is removed: the
// settle's own account of a seal that left captures out.
func logDaemonOnFailure(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		logs, err := filepath.Glob(filepath.Join(paths.Of(root).Logs, "*.log"))
		if err != nil {
			t.Logf("listing the daemon's logs: %v", err)
		}
		for _, p := range logs {
			b, rerr := paths.ReadFileShared(p)
			t.Logf("%s (read error %v):\n%s", filepath.Base(p), rerr, b)
		}
		t.Logf("client spools left: %v", clientSpools(t, root))
	})
}

// TestPreCompactInSpoolSubmodeSealsTheSpooledReads: a session in spool submode makes two Reads and
// a Stop, and compacts. They sit only in client spools when PreCompact arrives; the checkpoint it
// seals must point to both Reads and report nothing unreplayed, and the rehydration after the
// compaction must be built from that checkpoint. The daemon runs with spoolSubmodeRowBE.
func TestPreCompactInSpoolSubmodeSealsTheSpooledReads(t *testing.T) {
	r, stop := newCompactLoadRigWithConfig(t, spoolSubmodeRowConfig)
	logDaemonOnFailure(t, r.root)
	defer stop()
	cfg, _, warns, err := config.Load(config.Env{ProjectRoot: r.root, HomeDir: r.home, Getenv: noEnv})
	require.NoError(t, err)
	require.Empty(t, warns, "fixture sanity: the row's project config loads cleanly")
	require.Equal(t, int(spoolSubmodeRowBE/time.Millisecond), cfg.Runtime.Budgets.CheckpointFinalizeMs,
		"fixture sanity: the daemon runs with the row's B-E")

	start := r.base("SessionStart")
	start["source"] = "startup"
	r.hook(t, []string{"session-start"}, start)
	prompt := r.base("UserPromptSubmit")
	prompt["prompt"] = "We are preparing the Tern key migration. Read src/spooled_1.py and src/spooled_2.py."
	r.hook(t, []string{"observe", "prompt"}, prompt)

	enterSpoolSubmode(t, r.root)
	require.NoError(t, os.MkdirAll(filepath.Join(r.root, "src"), 0o700))
	for i, id := range spoolSubmodeReadIDs {
		rel := filepath.ToSlash(filepath.Join("src", fmt.Sprintf("spooled_%d.py", i+1)))
		content := fmt.Sprintf("SPOOLED%d = %d\n", i+1, 7700+i)
		require.NoError(t, os.WriteFile(filepath.Join(r.root, filepath.FromSlash(rel)), []byte(content), 0o600))
		r.hook(t, []string{"observe", "tool"}, uat03Read(r, id, rel, content))
	}
	r.hook(t, []string{"observe", "stop"}, r.base("Stop"))
	require.NotEmpty(t, clientSpools(t, r.root), "fixture sanity: in spool submode the hooks spooled their events")
	require.False(t, indexedToolUses(r.root, spoolSubmodeReadIDs),
		"fixture sanity: the spooled Reads have not reached the store before the compaction")

	out, _ := r.compact(t)

	cp := latestCheckpoint(t, r.root)
	var tools []string
	for _, tp := range cp.Pointers.Tools {
		tools = append(tools, string(tp.ToolUseID))
	}
	require.Subset(t, tools, spoolSubmodeReadIDs,
		"PreCompact must replay the session's client-spooled captures before it seals (D53(c)); dropped: %+v",
		cp.Dropped)
	require.True(t, indexedToolUses(r.root, spoolSubmodeReadIDs), "the replay published both Reads")
	for _, d := range cp.Dropped {
		require.NotContains(t, d.Kind, "unreplayed", "nothing was left unreplayed: %+v", d)
	}
	require.Equal(t, "rehydration", compactAnswer(out), "the compact SessionStart answers with the rehydration")
	ac := out.HookSpecificOutput.AdditionalContext
	for _, id := range spoolSubmodeReadIDs {
		require.Contains(t, ac, id, "the rehydration's pointers name the spooled Read %s", id)
	}
}
