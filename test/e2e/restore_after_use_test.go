package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// raIdleExitSeconds is the idle-exit window of the restore-after-use rows. The row needs the idle
// frontier advance to run AFTER SessionEnd closes the session's last segment and BEFORE the daemon
// leaves: the idle tick is a tenth of this (daemon.go idleTickMax), so twenty seconds gives the
// scheduler's one-second quiet detection (raConfig) several ticks to advance in, and the whole
// row stays well under a minute.
const raIdleExitSeconds = 20

// raIdleExitBound bounds the wait for the idle exit itself: the window, plus the daemon's own
// shutdown cleanup and the history-convergence slack every other lifecycle row allows.
const raIdleExitBound = raIdleExitSeconds*time.Second + e2eHistoryConvergeBound

// raConfig lets the idle pass start one second after the last hook instead of the default.
const raConfig = `{"scheduler":{"idle":{"detectAfterSeconds":1}}}`

// raEncodeLine is one index/segments.jsonl encode record.
type raEncodeLine struct {
	Op  string `json:"op"`
	ID  int64  `json:"id"`
	Seq int64  `json:"seq"`
}

// TestE2E_IdleExitLeavesNoUnsealedEncodeClaim is F-UAT03-2 (= F-UAT02-4) reproduced with the real
// binary and a real idle exit. A session compacts once (which opens the elimination ledger, so the
// idle frontier advance has sources), ends (SessionEnd closes its last segment), and the daemon
// then idles out. The idle advance encodes that segment into the session's next DRAFT, and no
// compaction ever seals it.
//
// Before the fix, the advance appended a durable "encode" record naming the draft's sequence
// number, so after the clean exit index/segments.jsonl claimed a checkpoint that was never
// written: fsck failed index.segments and a backup of the store restored with integrity FAILED.
// The index may only name a checkpoint that has been published.
func TestE2E_IdleExitLeavesNoUnsealedEncodeClaim(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t, testutil.WithConfig(raConfig))
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	env[idleExitSecondsEnvKey] = strconv.Itoa(raIdleExitSeconds)

	stdout, stderr, code := Run(t, bin, []string{"session-start"}, sessionStartPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)
	e2eWaitDaemonUp(t, p.Root)

	for i, id := range []core.ToolUseID{"toolu_ra_1", "toolu_ra_2"} {
		stdout, stderr, code = Run(t, bin, []string{"observe", "tool"}, schedObserveToolPayload(t, p.Root, id), env)
		require.Equal(t, 0, code, "observe tool #%d: stderr:\n%s", i, stderr)
		requireParsesAsOutput(t, stdout)
	}
	stdout, stderr, code = Run(t, bin, []string{"checkpoint"}, cpPreCompactPayload(t, p.Root, e2eSession), env)
	require.Equal(t, 0, code, "checkpoint: stdout:\n%s\nstderr:\n%s", stdout, stderr)
	stdout, stderr, code = Run(t, bin, []string{"flush"}, sessionEndPayload(t, p.Root), env)
	require.Equal(t, 0, code, "flush: stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)

	l := paths.Of(p.Root)
	lockPath := filepath.Join(l.Run, "daemon.lock")
	require.True(t, pollUntil(raIdleExitBound, e2eDaemonDownTick, func() bool {
		_, err := os.Stat(lockPath)
		return os.IsNotExist(err)
	}), "the daemon did not idle-exit within %s", raIdleExitBound)

	// The precondition that makes this row mean anything: the idle work DID encode the closed
	// segment into the session's next draft before the exit. Without it the row would pass on a
	// build whose advance never ran.
	draft := raReadDraft(t, filepath.Join(l.State, "draft-"+string(e2eSession)+".json"))
	require.NotEmpty(t, draft.Encoded, "the idle advance encoded no segment into the draft before the exit")

	sealed := map[int64]bool{}
	entries, err := paths.ReadManifest(l)
	require.NoError(t, err)
	for _, e := range entries {
		sealed[int64(e.Seq)] = true
	}
	for _, rec := range raEncodeRecords(t, filepath.Join(l.Index, "segments.jsonl")) {
		require.True(t, sealed[rec.Seq],
			"index/segments.jsonl records segment %d encoded into checkpoint %04d, which the manifest "+
				"does not record (sealed: %v); the unsealed draft holds seq %d", rec.ID, rec.Seq, sealed, draft.Seq)
	}

	// The operator's view of the same store: fsck and a backup/restore round trip are clean.
	stdout, stderr, code = Run(t, bin, []string{"fsck", "--json"}, nil, env)
	require.Equal(t, 0, code, "fsck after a clean idle exit: stdout:\n%s\nstderr:\n%s", stdout, stderr)
	for _, verb := range []string{"create", "verify"} {
		stdout, stderr, code = Run(t, bin, []string{"backup", verb, "--id", "after-idle", "--json"}, nil, env)
		require.Equal(t, 0, code, "backup %s: stdout:\n%s\nstderr:\n%s", verb, stdout, stderr)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	stdout, stderr, code = Run(t, bin,
		[]string{"backup", "restore", "--id", "after-idle", "--destination", dest, "--json"}, nil, env)
	require.Equal(t, 0, code, "backup restore after a clean idle exit: stdout:\n%s\nstderr:\n%s", stdout, stderr)
}

// raDraft is the subset of state/draft-<session>.json this row reads.
type raDraft struct {
	Seq     int64   `json:"seq"`
	Encoded []int64 `json:"encoded"`
}

func raReadDraft(t *testing.T, p string) raDraft {
	t.Helper()
	b, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err, "the session's draft must be persisted at %s", p)
	var d raDraft
	require.NoError(t, json.Unmarshal(b, &d))
	return d
}

func raEncodeRecords(t *testing.T, p string) []raEncodeLine {
	t.Helper()
	b, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	var out []raEncodeLine
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var rec raEncodeLine
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Op == "encode" {
			out = append(out, rec)
		}
	}
	require.NoError(t, sc.Err())
	return out
}
