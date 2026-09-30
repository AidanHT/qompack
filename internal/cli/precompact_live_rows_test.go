package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
)

// F-UAT05-2 of the phase 4 live lane (plans/sdd/V6-closeout/live/report.md), reproduced through the
// shipped composition: runDaemon through Dispatch, the shipped hook clients in process, and the
// shipped `qompack pin` frontend. No idle pass is driven by the test. A /qompack:pin made while the
// daemon ran reached pins/invariants.jsonl but neither the pins/invariants.json view nor the next
// PreCompact checkpoint, and the rehydration after it omitted the pin with dropped [] and degraded
// false.

// livePinText is the invariant the UAT-05 session pinned, verbatim.
const livePinText = "Never write to prod.db from the export code."

// runPinCommand runs `qompack pin <text>` against root the way the slash command shells out to it,
// while the rig's daemon is running, and returns its stdout.
func runPinCommand(t *testing.T, r *compactLoadRig, text string) string {
	t.Helper()
	var out, errw bytes.Buffer
	env := rootedEnv(r.root, "")
	env.HomeDir = r.home
	code := Dispatch(context.Background(), All(), []string{"qompack", "pin", text}, env, &out, &errw)
	require.Equal(t, ExitOK, code, "qompack pin: stdout=%s stderr=%s", out.String(), errw.String())
	return out.String()
}

// latestCheckpoint reads the newest sealed checkpoint of the rig's session through the shipped
// reader, which re-hashes it against its manifest line.
func latestCheckpoint(t *testing.T, root string) checkpoint.Checkpoint {
	t.Helper()
	rd, err := checkpoint.OpenReader(root, logging.Nop(), obs.New(testClock()))
	require.NoError(t, err)
	cp, _, err := rd.Latest(context.Background(), compactLoadSession)
	require.NoError(t, err, "the PreCompact must have sealed a checkpoint for the session")
	return cp
}

// TestLivePinReachesTheNextCheckpointAndBlock is F-UAT05-2. The pin is made by the CLI process
// while the resident daemon holds its own pin store; the very next PreCompact must seal it, the
// view must list it, and the rehydration must carry it.
func TestLivePinReachesTheNextCheckpointAndBlock(t *testing.T) {
	r, stop := newCompactLoadRig(t)
	defer stop()
	r.prime(t)

	require.Contains(t, runPinCommand(t, r, livePinText), "pinned "+pins.MintID(livePinText))

	out, _ := r.compact(t)

	cp := latestCheckpoint(t, r.root)
	var ids []string
	for _, inv := range cp.Invariants {
		ids = append(ids, inv.ID)
	}
	require.Contains(t, ids, pins.MintID(livePinText),
		"a pin made while the daemon runs must reach the very next checkpoint (F-UAT05-2)")

	view, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(r.root).Pins, "invariants.json")))
	require.NoError(t, err)
	require.Contains(t, string(view), livePinText,
		"the daemon's seal re-materializes the view; it must not drop a pin another process appended")

	require.NotNil(t, out.HookSpecificOutput, "the compact SessionStart answers with the rehydration")
	require.Contains(t, out.HookSpecificOutput.AdditionalContext, livePinText,
		"the rehydration block after the compaction must carry the pin")
}

// TestLivePinAfterACompactionReachesTheNextOne is the same finding one compaction later. A seal
// opens its successor draft at once, seeding that draft's invariants from the pins it can see at
// that moment; a pin made after it must still reach the checkpoint the NEXT compaction seals.
func TestLivePinAfterACompactionReachesTheNextOne(t *testing.T) {
	r, stop := newCompactLoadRig(t)
	defer stop()
	r.prime(t)
	r.compact(t)

	require.Contains(t, runPinCommand(t, r, livePinText), "pinned "+pins.MintID(livePinText))
	out, _ := r.compact(t)

	cp := latestCheckpoint(t, r.root)
	require.Equal(t, 2, int(cp.Seq), "fixture sanity: this is the second compaction's checkpoint")
	var ids []string
	for _, inv := range cp.Invariants {
		ids = append(ids, inv.ID)
	}
	require.Contains(t, ids, pins.MintID(livePinText),
		"a pin made after the successor draft opened must reach the next seal")
	require.NotNil(t, out.HookSpecificOutput)
	require.Contains(t, out.HookSpecificOutput.AdditionalContext, livePinText)
}
