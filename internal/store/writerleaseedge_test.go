package store

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The single-writer lease and the new-format write log — the two places where "exactly one writer"
// stops being a claim and becomes a refusal.

// TestAcquireWriter_RefusesTheWrongOwnerAndASecondHolder.
//
// Two different conditions, and conflating them would lose the distinction the migration is built
// on. The lease being FREE is what "exactly one writer at any instant" means; the caller being the
// owner the handoff record names is what "the handoff transferred writing" means. Before cutover
// only the legacy writer may acquire, and a second acquisition of a free-but-taken lease is
// ErrWriterHeld, not a silent second writer.
func TestAcquireWriter_RefusesTheWrongOwnerAndASecondHolder(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))

	h, err := m.Handoff()
	require.NoError(t, err)
	require.Equal(t, WriterLegacy, h.Owner, "a project before cutover is owned by the legacy writer")

	_, err = m.AcquireWriter(WriterQompack)
	require.ErrorIs(t, err, ErrWriterNotQompack)
	require.ErrorContains(t, err, string(WriterLegacy), "the refusal names who does own writing")

	lease, err := m.AcquireWriter(WriterLegacy)
	require.NoError(t, err)

	_, err = m.AcquireWriter(WriterLegacy)
	require.ErrorIs(t, err, ErrWriterHeld, "the lease is a lease, not a counter")

	require.NoError(t, lease.Release())
	require.NoError(t, lease.Release(), "Release is idempotent")

	again, err := m.AcquireWriter(WriterLegacy)
	require.NoError(t, err, "a released lease is free again")
	require.NoError(t, again.Release())
}

// TestNewFormatWrites_RefuseBeforeCutoverAndOnALogThatWillNotParse.
//
// The new-format write log is the evidence a rollback drill reads to decide which phase it is in, so
// a write recorded while the legacy writer still owns writing would fabricate a cutover that never
// happened — refused with the same sentinel AcquireWriter uses. And a log line that will not parse
// stops the read: skipping it would under-report the writes that exist, and "no new-format writes
// yet" is exactly the answer that decides a drill's phase.
func TestNewFormatWrites_RefuseBeforeCutoverAndOnALogThatWillNotParse(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))
	ctx := context.Background()

	writes, err := m.NewFormatWrites()
	require.NoError(t, err)
	require.Empty(t, writes, "a log that was never written holds no writes")

	_, err = m.RecordNewFormatWrite(ctx, core.Hash{1}, "legacy-1")
	require.ErrorIs(t, err, ErrWriterNotQompack)
	require.ErrorContains(t, err, "still owns writing")

	require.NoError(t, os.MkdirAll(paths.Long(m.l.Migrate), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(m.path(newFormatFile)), []byte("{not json\n"), 0o600))
	_, err = m.NewFormatWrites()
	require.ErrorContains(t, err, "parse new-format write line")
}

// TestDemandLog_RecordRefusesAnObservationItCouldNotReadBack.
//
// The writer is this build, so it has no business minting a kind it cannot read: an unrecognized
// kind would come back from Aggregate as a telemetry gap, i.e. this build would be manufacturing
// its own missing telemetry. A keyless observation is refused for the plainer reason that demand is
// demand FOR something.
func TestDemandLog_RecordRefusesAnObservationItCouldNotReadBack(t *testing.T) {
	// Not parallel: newProject calls t.Setenv.
	p := newProject(t)
	l := OpenDemandLog(p.Root)

	require.ErrorContains(t, l.Record(DemandObservation{Kind: "invented", Key: "k", TS: 1}),
		"unknown demand kind")
	require.ErrorContains(t, l.Record(DemandObservation{Kind: DemandRequested, Key: "", TS: 1}),
		"needs a key")
	require.NoError(t, l.Record(DemandObservation{Kind: DemandRequested, Key: "k", TS: 1}))
}
