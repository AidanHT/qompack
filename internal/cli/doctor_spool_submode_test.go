package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// D53(c): spool submode on a slow disk is the designed behaviour of a long session and loses
// nothing, so `qompack doctor` must not mark the project degraded for it, while still showing it.
// A spool the state file pins in spool submode with NO daemon serving is different: nothing replays
// it until a new session starts, and the row stays degraded and says why.

// writeClientSpool leaves one hook client spool file in root's spool directory.
func writeClientSpool(t *testing.T, root string) {
	t.Helper()
	writeSpoolFile(t, root, "client-4242.ndjson")
}

// writeSpoolFile leaves the spool file base, one line, in root's spool directory.
func writeSpoolFile(t *testing.T, root, base string) {
	t.Helper()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, base)), []byte("{}\n"), 0o600))
}

func TestDoctor_SpoolSubmodeIsInformational(t *testing.T) {
	t.Run("a daemon is serving", func(t *testing.T) {
		p := seedFsckProject(t)
		stop := bootstrapDaemon(t, p.Root)
		defer stop()
		enterSpoolSubmode(t, p.Root)
		writeClientSpool(t, p.Root)

		_, doc, errw := doctorJSON(t, p.Root)
		row := doctorFindRow(t, doc, "recording", "spool.pending")
		require.Equal(t, doctorOK, row["status"], "spool files in spool submode are the designed path; stderr=%s", errw)
		require.Contains(t, row["observed"], "file(s)", "the count is still shown")
		detail, _ := row["detail"].(string)
		for _, want := range []string{"spool submode", "nothing is lost", "a new session", "idle exit"} {
			require.Contains(t, detail, want)
		}
		for _, key := range obs.SpoolSubmodeKeys {
			require.Contains(t, detail, key)
		}
	})

	t.Run("no daemon is serving", func(t *testing.T) {
		p := seedFsckProject(t)
		enterSpoolSubmode(t, p.Root)
		writeClientSpool(t, p.Root)

		_, doc, errw := doctorJSON(t, p.Root)
		row := doctorFindRow(t, doc, "recording", "spool.pending")
		require.Equal(t, doctorDegraded, row["status"], "nothing replays these files; stderr=%s", errw)
		require.Contains(t, row["detail"], "no daemon is serving")
	})

	// Sync submode with a daemon serving keeps its degraded verdict (a client spool that stays while a
	// daemon serves is the one sign that the replay is not keeping up), but the detail names the
	// slow-disk cause, that nothing is lost, and what to look for.
	t.Run("sync submode with a daemon serving says why", func(t *testing.T) {
		p := seedFsckProject(t)
		stop := bootstrapDaemon(t, p.Root)
		defer stop()
		writeClientSpool(t, p.Root)

		_, doc, errw := doctorJSON(t, p.Root)
		row := doctorFindRow(t, doc, "recording", "spool.pending")
		require.Equal(t, doctorDegraded, row["status"], "stderr=%s", errw)
		detail, _ := row["detail"].(string)
		for _, want := range []string{"ACK deadline", "nothing is lost", "not keeping up"} {
			require.Contains(t, detail, want)
		}
		for _, key := range obs.SpoolSubmodeKeys {
			require.Contains(t, detail, key)
		}
	})

	// The spool directory also holds the daemon's WAL segments (wal-*.ndjson), which the worker pool
	// replays, not the client-spool watcher, and no hook's ACK deadline put there (D55, wave 16b). The
	// detail must not call them client spools: it counts each kind and says what replays it.
	t.Run("sync submode with only WAL segments does not call them client spools", func(t *testing.T) {
		p := seedFsckProject(t)
		stop := bootstrapDaemon(t, p.Root)
		defer stop()
		writeSpoolFile(t, p.Root, "wal-sess-doctor.ndjson")

		_, doc, errw := doctorJSON(t, p.Root)
		row := doctorFindRow(t, doc, "recording", "spool.pending")
		require.Equal(t, doctorDegraded, row["status"], "stderr=%s", errw)
		detail, _ := row["detail"].(string)
		require.Contains(t, detail, "1 daemon WAL segment(s)")
		require.Contains(t, detail, "worker pool")
		require.NotContains(t, detail, "client spool")
		require.NotContains(t, detail, "ACK deadline")
	})

	t.Run("sync submode with both kinds counts each", func(t *testing.T) {
		p := seedFsckProject(t)
		stop := bootstrapDaemon(t, p.Root)
		defer stop()
		writeClientSpool(t, p.Root)
		writeSpoolFile(t, p.Root, "wal-sess-doctor.ndjson")

		_, doc, errw := doctorJSON(t, p.Root)
		row := doctorFindRow(t, doc, "recording", "spool.pending")
		require.Equal(t, doctorDegraded, row["status"], "stderr=%s", errw)
		detail, _ := row["detail"].(string)
		for _, want := range []string{"1 hook client spool(s)", "ACK deadline", "1 daemon WAL segment(s)", "worker pool"} {
			require.Contains(t, detail, want)
		}
	})

	t.Run("sync submode keeps the old verdict", func(t *testing.T) {
		p := seedFsckProject(t)
		writeClientSpool(t, p.Root)

		_, doc, _ := doctorJSON(t, p.Root)
		row := doctorFindRow(t, doc, "recording", "spool.pending")
		require.Equal(t, doctorDegraded, row["status"])
	})
}

// TestDoctorHotPathRow: the status section reports the hot path's submode from the status snapshot,
// spool submode as ok with the explanation, and an absent snapshot as unknown.
func TestDoctorHotPathRow(t *testing.T) {
	t.Parallel()
	row := doctorHotPathRow(commands.StatusReport{Snapshot: &commands.DaemonStatus{Hot: "spool"}})
	require.Equal(t, doctorRow{
		ID: "status.hotPath", Status: doctorOK, Observed: "spool",
		Detail: obs.SpoolSubmodeWhat + ". It lasts until " + obs.SpoolSubmodeUntil + ". To tune it: " + obs.SpoolSubmodeTune,
	}, row)
	require.Equal(t, doctorOK, doctorHotPathRow(commands.StatusReport{Snapshot: &commands.DaemonStatus{Hot: "sync"}}).Status)
	require.Equal(t, doctorUnknown, doctorHotPathRow(commands.StatusReport{}).Status)
}
