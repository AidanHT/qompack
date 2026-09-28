package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// spooledOps lists the op of every line in root's client spool files.
func spooledOps(t *testing.T, root string) []string {
	t.Helper()
	var ops []string
	err := filepath.Walk(paths.Long(paths.Of(root).Spool), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, line := range strings.Split(string(b), "\n") {
			var req struct {
				Op string `json:"op"`
			}
			if json.Unmarshal([]byte(line), &req) == nil && req.Op != "" {
				ops = append(ops, req.Op)
			}
		}
		return nil
	})
	require.NoError(t, err)
	return ops
}

// spooledStartPromptBound bounds the wait for one prompt's verbatim capture to land in the rig's
// store: headroom for a loaded machine, not a latency expectation (the x14 rows use the same 30 s).
const spooledStartPromptBound = 30 * time.Second

// waitPromptCaptures waits until sess has want verbatim prompt captures in the rig's store. The
// daemon runs a prompt's §12.1 sentinel scan before it captures the prompt (runIngested), so a
// capture that has landed is a scan that has run — the only signal this row has, since a scan for
// no probe changes nothing it could poll.
func waitPromptCaptures(t *testing.T, r *v4Rig, sess core.SessionID, want int) {
	t.Helper()
	ticker := time.NewTicker(x14v5ScanTick)
	defer ticker.Stop()
	timeout := time.NewTimer(spooledStartPromptBound)
	defer timeout.Stop()
	for {
		got := 0
		for turn := range 8 {
			if _, err := r.Opts.Store.ToolUse(context.Background(), observer.VerbatimPromptID(sess, core.TurnIndex(turn))); err == nil {
				got++
			}
		}
		if got >= want {
			return
		}
		_, _ = r.D.Drain(context.Background())
		select {
		case <-ticker.C:
		case <-timeout.C:
			require.FailNowf(t, "the prompt captures never landed", "have %d of %d after %s", got, want, spooledStartPromptBound)
		}
	}
}

// TestE2E_SpooledSessionStartNeverDegradesTheProject is w2-hookout's diagnostic
// (plans/sdd/V6-closeout/w2-hookout/runs/zz_diag_hookout_test.go.txt,
// TestZZDiag_SpooledSessionStartMintsAnUndeliveredProbe) made a real test, through the real binary.
//
// A SessionStart that could not reach the daemon in time is spooled, and the hook answers the host
// without it ({}). The next daemon's startup drain replays it. Before the fix that replay minted a
// §12.1 probe into an answer nobody received; the session's next two prompts could not find it, and
// the session's next start degraded the project to passive recording with a banner blaming the host
// ("hook.additional_context_delivered … sentinel not found after two chances"). On a loaded machine a
// cold daemon slower than session-start's wait is enough to get there, and on a fresh project, where
// no probe has ever been observed, nothing stops it.
func TestE2E_SpooledSessionStartNeverDegradesTheProject(t *testing.T) {
	bin := Build(t)
	p := v4Project(t)
	transcript := x14v5Transcript(t, p.Root)
	const sess = core.SessionID("sess-e2e-spooled-start")

	env := e2eEnv(p)
	env[qompackFaultEnvKey] = "daemon-down"
	stdout, stderr, code := Run(t, bin, []string{"session-start"}, x14v5StartPayload(t, p.Root, sess, "startup", transcript), env)
	require.Equal(t, 0, code, "session-start must exit 0\nstderr:\n%s", stderr)
	require.Equal(t, "{}", strings.TrimSpace(string(stdout)), "fixture: with no daemon the hook answers without one")
	require.Contains(t, spooledOps(t, p.Root), string(ipc.OpSessionStart), "fixture: the start was spooled")

	r := v4StartRig(t, p) // its Run drains the spool before it serves
	h := contract.LoadHistory(contract.HistoryPath(p.Root))
	require.Empty(t, h.Sentinel.Token, "the replayed start minted no probe: nobody received its answer")
	require.Equal(t, 1, h.SessionCount, "the replayed start still did the start's bookkeeping")

	for i := 1; i <= x14v5SentinelChances; i++ {
		obsRunHook(t, r.Bin, []string{"observe", "prompt"},
			x14v5PromptPayload(t, p.Root, sess, transcript, fmt.Sprintf("prompt %d", i)), e2eEnv(p))
		waitPromptCaptures(t, r, sess, i)
	}
	require.Less(t, contract.LoadHistory(contract.HistoryPath(p.Root)).Sentinel.Chances, x14v5SentinelChances,
		"no probe the host never received may run out its chances")

	out := x14v5Start(t, r, x14v5StartPayload(t, p.Root, sess, "resume", transcript))
	require.Empty(t, out.SystemMessage, "no degrade banner may blame the host for a reply Qompack lost")
	require.Equal(t, contract.ModeFull.String(), e2eStatus(t, p.Root).Mode,
		"a spooled and replayed SessionStart must never degrade the project")
	require.Contains(t, x14v5AdditionalContext(t, out), "qompack-contract-probe",
		"the live start mints its own probe, into an answer that reaches the host")
}
