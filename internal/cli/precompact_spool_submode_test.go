package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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

// TestPreCompactInSpoolSubmodeSealsTheSpooledReads: a session in spool submode makes two Reads and
// compacts. The Reads sit only in client spools when PreCompact arrives; the checkpoint it seals
// must point to both, and the rehydration after the compaction must be built from that checkpoint.
func TestPreCompactInSpoolSubmodeSealsTheSpooledReads(t *testing.T) {
	r, stop := newCompactLoadRig(t)
	defer stop()

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
		"PreCompact must replay the session's client-spooled captures before it seals (D53(c))")
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
