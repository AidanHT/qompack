package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// T28 — design §6.2, over the crash-point table of design §3. One delivery is driven through the
// real durable path (dispatchOp -> Accept -> appendWAL -> lease -> seal, then the worker's publish
// and acknowledge), with a seam at every step the table names. The path is cut at each of them in
// turn, a MACHINE-CRASH image is reconstructed from what was seen synced, and a fresh lock on a
// COPY of that image is asked what recovery makes of it.
//
// What makes the image a crash and not a snapshot: a file's bytes enter it only when a Sync that
// COVERED them returned. The page cache is lost in a machine crash, so bytes written and not yet
// flushed are not in the image, whatever the live filesystem happens to hold at the cut. That is
// the whole difference between this test and copying the directory, and it is what lets the table
// assert "the journal opens" or "the journal refuses" rather than "it depends".
//
// Every row is run in BOTH write formats (design §4.3): format 1, the shipped v1 sidecar this build
// writes, and format 2, the held A/B seal, through the lock's own sealFormat seam. The steps that
// exist in one format only (the v2 slot sync and its post-seal identity check) are skipped by the
// table's own format field, never by t.Skip.

// errCrashCut is the injected failure that stands in for the instant a machine dies. It never
// reaches a caller: the run's own goroutine is abandoned with the journal poisoned, exactly as a
// crashed process's in-memory state is abandoned, and only the image on disk is carried forward.
var errCrashCut = errors.New("daemon: crash-cut fixture: the machine stopped here")

// The files one leased, published and acknowledged delivery makes durable. A crash image is these
// five paths and nothing else: the lock and the drain's own progress are rebuilt by the next
// daemon, and everything else under the project is the store's, which this path does not write.
var crashImageFiles = []string{
	filepath.Join("state", deliveryLeaseFile),
	filepath.Join("state", deliveryPositionFile),
	filepath.Join("state", deliveryAckFile),
	filepath.Join("state", deliveryAckPositionFile),
}

// crashStep names one step of design §3's path. The names are the table's own.
//
// The steps that are NOT here are the ones with no durable image of their own. L9 (admission), L10
// (results and the job) and K9 (acknowledgement admission) are in-memory work that follows a seal
// which has already returned, so a machine crash at any of them leaves byte for byte what a crash
// at the seal before it leaves — which is what design §3 says in saying of row 9 only "as 8". The
// table covers them where their image is made, at L8 and at K6, rather than repeating one image
// under three names and implying three distinct states.
//
// L3 (checkFile) is the same case at the other end of the batch. commitLeases runs the check at
// delivery_lease.go:425 and reaches j.writer.Write only at :445, through appendLeases, so a machine
// crash inside the check leaves the WAL durable and not one journal byte written — byte for byte
// the image a crash at L4 leaves, and the §3 row-4 state L4 already asserts. A seam on the Write
// could only cut once the check had SUCCEEDED, which would name a step it did not reach, so the
// table states the check's image at L4 rather than claiming a twelfth distinct crash state.
//
// The open sequence (O1-O6) and the Release downgrade are covered by
// TestDeliverySeal_ConversionAndDowngrade and TestDeliveryJournal_RollbackDrillAcrossFormats,
// which drive those paths directly.
type crashStep string

const (
	crashW1 crashStep = "W1 the WAL batch's Write"
	crashW2 crashStep = "W2 the WAL batch's Sync"
	crashL4 crashStep = "L4 the journal Write"
	crashL5 crashStep = "L5 the journal Sync"
	crashL6 crashStep = "L6 the seal"
	crashL7 crashStep = "L7 the v2 slot's SyncData"
	crashL8 crashStep = "L8 the post-seal identity check"
	crashAK crashStep = "AK after Accept returned, before the ACK byte"
	crashK4 crashStep = "K4 the acknowledgement Write"
	crashK5 crashStep = "K5 the acknowledgement Sync"
	crashK6 crashStep = "K6 the acknowledgement seal"
)

// crashRun drives one delivery through the real path with every durability point observed.
//
// durable is the image: for each file, the bytes a Sync that covered them returned for. It is
// written only by a seam that saw its own Sync return nil, so it is what survives a machine crash
// at any instant, and never what the page cache happened to hold.
type crashRun struct {
	t       *testing.T
	root    string
	dd      *daemon
	lock    *Lock
	journal *deliveryJournal
	calls   func() int

	mu      sync.Mutex
	durable map[string][]byte
	// walName is the base name of the session's WAL segment, learnt from the first WAL Sync.
	walName string
	// cut is the step this run stops at, and cutDone records that it was reached.
	cut     crashStep
	cutDone bool
}

// newCrashRun builds a daemon on a fresh root whose journal writes seals in format, opens the
// journal so that its seams can be installed, and returns the run with its image seeded from the
// files a fresh open leaves behind.
func newCrashRun(t *testing.T, format int, cut crashStep) *crashRun {
	t.Helper()
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	lock.sealFormat = format
	t.Cleanup(func() { _ = lock.Release() })

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	requireNoLeakedInflight(t, journal)

	r := &crashRun{t: t, root: root, dd: dd, lock: lock, journal: journal, calls: calls, cut: cut}
	r.durable = map[string][]byte{}
	// A fresh open has already created and sealed both journals, through WriteAtomic in either
	// format, so those bytes are durable before the delivery starts.
	for _, rel := range crashImageFiles {
		r.snapshot(rel)
	}
	r.install()
	return r
}

// snapshot records rel's current bytes as durable. A file that does not exist is recorded as
// absent, which is itself a durable fact: recovery must find no file there.
func (r *crashRun) snapshot(rel string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(paths.Long(filepath.Join(r.root, ".qompack", rel)))
	if err != nil {
		delete(r.durable, rel)
		return
	}
	r.durable[rel] = slices.Clone(b)
}

// snapshotWAL records the session's WAL segment, whose name is only known once it is opened.
func (r *crashRun) snapshotWAL(name string) {
	r.mu.Lock()
	r.walName = name
	r.mu.Unlock()
	r.snapshot(filepath.Join("spool", name))
}

// stop reports whether step is the one this run is cut at, and records that it was reached. It
// answers true exactly once, so a seam on a path taken more than once cuts its first crossing.
func (r *crashRun) stop(step crashStep) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cut != step || r.cutDone {
		return false
	}
	r.cutDone = true
	return true
}

// install wires the seams of every step in design §3's table onto this run's ingest and journal.
//
// Each seam does one of two things, and the difference is the table's own: a step that names an
// operation cuts BEFORE that operation runs (nothing of it reached the disk), and a step that names
// what follows one runs the operation, snapshots what its Sync made durable, and cuts after.
func (r *crashRun) install() {
	ing, j := r.dd.ing, r.journal

	// W1/W2: the WAL's Write and Sync. A Sync that returns nil makes the segment durable up to the
	// bytes the Write put there, which is what the image records.
	writeWAL := ing.writeWAL
	ing.writeWAL = func(f *os.File, b []byte) (int, error) {
		if r.stop(crashW1) {
			return 0, errCrashCut // the machine stopped before the line reached the file
		}
		return writeWAL(f, b)
	}
	syncWAL := ing.syncWAL
	ing.syncWAL = func(f *os.File) error {
		if r.stop(crashW2) {
			return errCrashCut // written, never flushed: a machine crash loses the page cache
		}
		if err := syncWAL(f); err != nil {
			return err
		}
		r.snapshotWAL(filepath.Base(f.Name()))
		return nil
	}

	// L4-L5: the lease journal's per-batch Write and Sync, through the journal's own writer seam.
	// The batch's own checkFile (L3) runs before both and has already SUCCEEDED by the time this
	// seam is reached (delivery_lease.go:425, then :445 through appendLeases), so it is not a cut of
	// its own; the image a crash inside it leaves is L4's, as crashStep's comment records.
	file := j.file
	j.writer = leaseFaultWriter{
		file: file,
		write: func(b []byte) (int, error) {
			if r.stop(crashL4) {
				return 0, errCrashCut
			}
			return file.Write(b)
		},
		sync: func() error {
			if r.stop(crashL5) {
				return errCrashCut
			}
			if err := file.Sync(); err != nil {
				return err
			}
			r.snapshot(filepath.Join("state", deliveryLeaseFile))
			return nil
		},
	}

	// L6-L8: the seal. In format 1 the whole seal is one paths.WriteAtomic, so L6 is the only step
	// it has; in format 2 the slot's SyncData (L7) and the post-seal identity check (L8) are their
	// own steps, and the seal's own syncData seam is where they are cut.
	sealLease := j.sealLease
	j.sealLease = func(size int64, count int, chain core.Hash) error {
		if r.stop(crashL6) {
			return errCrashCut
		}
		if err := sealLease(size, count, chain); err != nil {
			return err
		}
		r.snapshot(filepath.Join("state", deliveryPositionFile))
		return nil
	}
	if j.seal != nil {
		syncData := j.seal.syncData
		j.seal.syncData = func(f *os.File) error {
			if r.stop(crashL7) {
				return errCrashCut // the slot's bytes are written and never flushed
			}
			if err := syncData(f); err != nil {
				return err
			}
			// The slot is durable HERE, before the post-seal identity check, which is what makes
			// L8 a crash point of its own (design §3 rows 7 and 8).
			r.snapshot(filepath.Join("state", deliveryPositionFile))
			if r.stop(crashL8) {
				return errCrashCut
			}
			return nil
		}
	}

	// K4-K6: the acknowledgement pipeline, the same shape on its own files.
	ackFile := j.ackFile
	j.ackWriter = leaseFaultWriter{
		file: ackFile,
		write: func(b []byte) (int, error) {
			if r.stop(crashK4) {
				return 0, errCrashCut
			}
			return ackFile.Write(b)
		},
		sync: func() error {
			if r.stop(crashK5) {
				return errCrashCut
			}
			if err := ackFile.Sync(); err != nil {
				return err
			}
			r.snapshot(filepath.Join("state", deliveryAckFile))
			return nil
		},
	}
	sealAck := j.sealAck
	j.sealAck = func(size int64, count int, chain core.Hash) error {
		if r.stop(crashK6) {
			return errCrashCut
		}
		if err := sealAck(size, count, chain); err != nil {
			return err
		}
		r.snapshot(filepath.Join("state", deliveryAckPositionFile))
		return nil
	}
}

// deliver runs one delivery through the live route, exactly as a hook's request reaches the daemon,
// and then the worker's own dispatch (publish, observe, acknowledge). It returns the response the
// ACK byte would have been written for.
//
// A cut at or before L9 fails the lease, which Accept counts as an unleased gap and answers OK for,
// so the response alone never decides a row; the image and the recovery below do.
func (r *crashRun) deliver(nonce string) ipc.Response {
	r.t.Helper()
	req := observeRequest(nonce, crashSession, `{"hook_event_name":"PostToolUse"}`)
	resp := r.dd.dispatchOp(context.Background(), req)
	if r.cut == crashAK {
		// Accept returned and the job is queued; the machine stops before the ACK byte is written.
		// Nothing of the worker's own publication has run, which is the row's point.
		r.stop(crashAK)
		return resp
	}
	drainRing(r.t, r.dd)
	return resp
}

// crashSession is the session every crash-cut delivery carries.
const crashSession = "sess-crash-cut"

// image is the machine-crash image: the durable bytes of every file, by path relative to .qompack.
func (r *crashRun) image() map[string][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]byte, len(r.durable))
	for k, v := range r.durable {
		out[k] = slices.Clone(v)
	}
	return out
}

// walRel is the image path of the session's WAL segment, or "" when no WAL Sync ever returned.
func (r *crashRun) walRel() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.walName == "" {
		return ""
	}
	return filepath.Join("spool", r.walName)
}

// restoreCrashImage writes image into a fresh project root and returns it: the disk a machine that
// died at the cut comes back to. Every file the image does not name is absent, which is the state a
// crash leaves for bytes no Sync ever covered.
func restoreCrashImage(t *testing.T, image map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	for rel, b := range image {
		p := filepath.Join(root, ".qompack", rel)
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(p), b, 0o600))
	}
	return root
}

// crashOutcome is what recovery makes of one image.
type crashOutcome struct {
	// opened is whether a fresh lock could open the journal at all.
	opened bool
	// leases and acks are the identities the open recovered.
	leases, acks int
	// dispatched is how many deliveries the drain consumed, observed how many times the handler
	// actually ran (the witness a republication shows up in), and published how many capture
	// sidecars exist afterwards.
	dispatched, observed, published int
	// complete is the drain's own gap verdict.
	complete bool
}

// recover opens image under a fresh lock, drains whatever the spool holds, and reports what
// recovery made of it. It is the next daemon, in full: the real AcquireLock, the real
// openDeliveryJournal with its load and its re-seal, and the real Drain.
func recoverCrashImage(t *testing.T, root string, format int) crashOutcome {
	t.Helper()
	dd, calls := newObservingDaemon(t, root)
	lock, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	lock.sealFormat = format
	dd.startMu.Lock()
	dd.lock = lock
	dd.startMu.Unlock()
	t.Cleanup(func() { _ = lock.Release() })

	journal, err := dd.deliveryJournal()
	if err != nil {
		// A refused open is the whole outcome: the evidence is preserved and nothing is drained.
		require.Nil(t, journal)
		return crashOutcome{}
	}
	requireNoLeakedInflight(t, journal)

	// The identities the OPEN recovered, read BEFORE the drain. The drain may go on to lease a copy
	// the crash never got an identity for, and "what recovery found on disk" and "what the drain
	// then made of the spool" are two different facts: folding them together would let a row that
	// recovers nothing pass because the drain minted one afterwards.
	out := crashOutcome{opened: true, leases: len(journal.leases), acks: len(journal.acks)}

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	out.dispatched, out.observed = n, calls()
	out.published, out.complete = len(sidecarFiles(t, root)), dd.DrainGaps().Complete
	return out
}

// crashRow is one row of design §3's table.
type crashRow struct {
	step crashStep
	// formats is the write formats the row applies to; nil means both.
	formats []int
	// opens is whether a fresh open of the image succeeds.
	opens bool
	// leases is how many identities the open recovers, and acks how many are on the frontier.
	leases, acks int
	// why names the design's own reading of the row.
	why string
}

func (c crashRow) appliesTo(format int) bool {
	return len(c.formats) == 0 || slices.Contains(c.formats, format)
}

// TestDeliveryPath_CrashCutAtEveryStep is T28. Every step of design §3 is cut in turn; the image
// holds only what was seen synced; and a fresh lock on a copy of it is asked what it recovers.
//
// The rows say what the design says. A cut before the lease's own Write leaves a durable WAL line
// and no identity, and the drain leases it fresh. A cut from the seal onwards leaves a complete
// journal tail that the open recovers and re-seals, so the identity the dying process assigned
// comes back rather than being minted a second time. A cut inside the acknowledgement pipeline
// leaves a lease with no frontier record, and the drain republishes it under the identity it already
// had. None of it loses an identity, and none of it mints two for one nonce.
func TestDeliveryPath_CrashCutAtEveryStep(t *testing.T) {
	rows := []crashRow{
		{
			step: crashW1, opens: true, leases: 0, acks: 0,
			why: "§3 row 1: nothing of the line reached the WAL, so there is nothing to recover and no identity ever existed",
		},
		{
			step: crashW2, opens: true, leases: 0, acks: 0,
			why: "§3 row 2: the line was written and never flushed; a machine crash takes the page cache with it",
		},
		{
			step: crashL4, opens: true, leases: 0, acks: 0,
			why: "§3 row 4: the WAL line is durable and no journal byte precedes the sync that covers it, so the drain leases it fresh — the image a crash anywhere from W3 through the batch's own checkFile (L3) leaves",
		},
		{
			step: crashL5, opens: true, leases: 0, acks: 0,
			why: "§3 row 5: the journal line was written and never flushed, so the image holds none of it",
		},
		{
			step: crashL6, opens: true, leases: 1, acks: 0,
			why: "§3 row 6: a complete journal tail past the seal is recovered and re-sealed at open, before any caller can reuse it",
		},
		{
			step: crashL7, formats: []int{2}, opens: true, leases: 1, acks: 0,
			why: "§3 row 7: the slot was written and never flushed, so the OTHER slot is still effective and the tail is recovered past it",
		},
		{
			step: crashL8, formats: []int{2}, opens: true, leases: 1, acks: 0,
			why: "§3 row 8: the new seal is durable and the batch was never released; the identity is recovered as a known nonce",
		},
		{
			step: crashAK, opens: true, leases: 1, acks: 0,
			why: "§3 row 10: the lease is durable and no ACK went out, so the client's copy and the WAL copy share one identity",
		},
		{
			step: crashK4, opens: true, leases: 1, acks: 0,
			why: "§3 row 12: the acknowledgement was never written; the lease survives and the delivery is republished under it",
		},
		{
			step: crashK5, opens: true, leases: 1, acks: 0,
			why: "§3 row 12: written and never flushed — the same outcome, since only a returned Sync makes a frontier record",
		},
		{
			step: crashK6, opens: true, leases: 1, acks: 1,
			why: "§3 row 12: a complete acknowledgement tail is recovered and re-sealed by openAckLocked",
		},
	}

	for _, format := range []int{1, 2} {
		t.Run(formatName(format), func(t *testing.T) {
			for _, row := range rows {
				if !row.appliesTo(format) {
					continue
				}
				t.Run(string(row.step), func(t *testing.T) {
					runCrashRow(t, format, row)
				})
			}
		})
	}
}

// formatName names a write format for a subtest.
func formatName(format int) string {
	if format == 2 {
		return "format 2, the held A/B seal"
	}
	return "format 1, the v1 sidecar"
}

// runCrashRow drives one delivery, cuts it at the row's step, and asserts what recovery makes of
// the image — including what the drain does with the WAL copy and a client copy of the same nonce.
func runCrashRow(t *testing.T, format int, row crashRow) {
	nonce := testDeliveryToken('c')
	r := newCrashRun(t, format, row.step)
	r.deliver(nonce)
	require.True(t, r.cutDone, "the cut at %s was never reached: the row pins a step this path no longer takes", row.step)

	image := r.image()
	walRel := r.walRel()

	t.Run("the machine-crash image", func(t *testing.T) {
		root := restoreCrashImage(t, image)
		// The hook's own copy of the delivery, spooled because its one-byte ACK never arrived. It
		// carries the same nonce as the WAL copy, which is what makes the two one delivery.
		writeSpoolLine(t, root, "client-00007.ndjson",
			observeRequest(nonce, crashSession, `{"hook_event_name":"PostToolUse"}`))

		got := recoverCrashImage(t, root, format)
		require.Equal(t, row.opens, got.opened, "%s: %s", row.step, row.why)
		if !row.opens {
			return
		}
		require.Equal(t, row.leases, got.leases, "%s: recovered identities — %s", row.step, row.why)
		require.Equal(t, row.acks, got.acks, "%s: recovered frontier records — %s", row.step, row.why)

		// The drain's own outcome for the copies of one nonce — the client's, plus the WAL's whenever
		// the crash image holds a segment a Sync returned for. Every copy here is complete, so the
		// pass consumes them all and reports no gap; the torn variants below are the other half of
		// that statement.
		assertDrainOutcome(t, got, row.acks, true, row.why)
	})

	// The torn variants of the extent that was in flight at the cut. A torn journal tail refuses the
	// open outright (the evidence is preserved), and so does a torn v2 slot: strict, never a misread.
	for name, torn := range tornVariants(image, walRel, format, row.leases) {
		t.Run("torn: "+name, func(t *testing.T) {
			root := restoreCrashImage(t, torn.image)
			// The same client copy the untorn image gets. A torn variant tears one extent of the
			// crash image and nothing else, so the hook's spooled copy is still there — and it is
			// what lets the drain assertion below tell a torn extent from a complete one instead of
			// asserting something true either way.
			writeSpoolLine(t, root, "client-00007.ndjson",
				observeRequest(nonce, crashSession, `{"hook_event_name":"PostToolUse"}`))

			got := recoverCrashImage(t, root, format)
			require.Equal(t, torn.opens, got.opened, torn.why)
			if torn.opens {
				require.Equal(t, torn.leases, got.leases, torn.why)
				assertDrainOutcome(t, got, row.acks, torn.complete, torn.why)
				return
			}
			// A REFUSED open writes nothing: the evidence is exactly as the crash left it, which is
			// the strict reader's own stated reason for refusing (design §2.9).
			//
			// It is a refusal's assertion alone. An open that SUCCEEDS is entitled to write, and
			// does: it re-seals a complete tail recovered past a stale seal (design O4c) before any
			// caller can reuse those identities, and the drain then consumes what it published.
			for rel, want := range torn.image {
				b, err := os.ReadFile(paths.Long(filepath.Join(root, ".qompack", rel)))
				require.NoError(t, err)
				require.Equal(t, want, b, "a refused open must leave %s exactly as it found it", rel)
			}
		})
	}
}

// assertDrainOutcome asserts what the drain made of the copies of one nonce in a restored image.
//
// A delivery the frontier already names is skipped entirely. One it does not is dispatched exactly
// ONCE however many complete copies of it the spool holds — every copy carries the same nonce and
// takes the same lease — and it leaves exactly one capture sidecar.
//
// complete is the drain's own gap verdict, and it is the assertion that depends on the extent a
// torn variant tore. An incomplete trailing line is pending input rather than a record
// (TestDrainTrailingIncompleteLineWaitsForCompletion), so a pass that leaves one behind is NOT
// complete, while the same image untorn is: the two subtests over one image are each other's
// control, which is what keeps the torn variant from asserting something true either way.
func assertDrainOutcome(t *testing.T, got crashOutcome, acks int, complete bool, why string) {
	t.Helper()
	if acks > 0 {
		require.Zero(t, got.observed,
			"a delivery the frontier already names must advance its offset without republishing — %s", why)
		require.Zero(t, got.dispatched, "and it is not dispatched a second time either — %s", why)
	} else {
		require.Equal(t, 1, got.dispatched,
			"however many copies carry the nonce, they are one delivery and are dispatched once — %s", why)
		require.Equal(t, 1, got.observed, "so the handler runs exactly once — %s", why)
		require.Equal(t, 1, got.published, "one delivery leaves one capture sidecar")
	}
	require.Equal(t, complete, got.complete, "the drain's own gap verdict — %s", why)
}

// tornImage is one torn variant of a crash image.
type tornImage struct {
	image  map[string][]byte
	opens  bool
	leases int
	// complete is the drain's own gap verdict for this variant, for a variant whose open succeeds.
	// Tearing an extent the drain reads turns it from true to false, which is the assertion that
	// makes the variant depend on what it tore.
	complete bool
	why      string
}

// tornVariants returns the torn states of the extents that a crash can leave half-written: the WAL
// segment's last line, the journal's last line, and — in format 2 — the slot being written.
//
// A device that writes a 4 KiB block atomically cannot produce the torn slot; the design assumes
// that and the strict reader refuses the state anyway rather than guessing (design §2.9, R4).
// recovered is the identity count the row's own image recovers, which a torn WAL line does not
// change: the journal's identities come from delivery-leases.jsonl, and the WAL is only where a
// delivery's BYTES live.
func tornVariants(image map[string][]byte, walRel string, format, recovered int) map[string]tornImage {
	out := map[string]tornImage{}
	leaseRel := filepath.Join("state", deliveryLeaseFile)

	if b, ok := image[walRel]; ok && len(b) > 1 {
		torn := cloneImage(image)
		torn[walRel] = b[:len(b)-1]
		out["the WAL's last line"] = tornImage{
			image: torn, opens: true, leases: recovered, complete: false,
			why: "a torn WAL line changes nothing the journal recovered — the identities are the lease journal's, not the " +
				"WAL's — and the incomplete line itself is NOT dispatched: it is pending input rather than a record and " +
				"waits for its writer (TestDrainTrailingIncompleteLineWaitsForCompletion), so the client's complete copy " +
				"still publishes the delivery exactly once while the pass that left the fragment behind reports a gap, " +
				"which the same image untorn does not",
		}
	}
	if b, ok := image[leaseRel]; ok && len(b) > 1 {
		torn := cloneImage(image)
		torn[leaseRel] = b[:len(b)-1]
		out["the journal's last line"] = tornImage{
			image: torn, opens: false,
			why: "§3 row 5: a torn journal tail refuses the open; the journal is unavailable and the evidence is preserved",
		}
	}
	if format == 2 {
		posRel := filepath.Join("state", deliveryPositionFile)
		if b, ok := image[posRel]; ok && isDeliverySealImage(b) {
			torn := cloneImage(image)
			torn[posRel] = tearSealSlot(b)
			out["the v2 seal's slot"] = tornImage{
				image: torn, opens: false,
				why: "§3 row 7: a torn slot is refused by the strict reader, never misread",
			}
		}
	}
	return out
}

// cloneImage is a deep copy of an image.
func cloneImage(image map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(image))
	for k, v := range image {
		out[k] = slices.Clone(v)
	}
	return out
}

// tearSealSlot returns img with the effective slot's record torn: its first half kept and the rest
// overwritten with the padding a half-written region would leave. The result is neither the old
// record nor a new one, which is exactly what classifySlot must refuse.
func tearSealSlot(img []byte) []byte {
	out := slices.Clone(img)
	for _, slot := range []sealSlot{sealSlotA, sealSlotB} {
		region := slot.region(out)
		end := strings.IndexByte(string(region), deliverySealPad)
		if end <= 1 {
			continue
		}
		for i := end / 2; i < end; i++ {
			region[i] = deliverySealPad
		}
		return out
	}
	return out
}

// A delivery whose lease batch was cut leaves no identity, and the drain that follows mints one
// fresh — the ordinary "crash between the WAL and the lease" outcome of §3 row 4. This pins the
// half the table states in prose: the identity the drain assigns is the FIRST one for that nonce,
// not a second one beside an identity the crash had already made durable.
func TestDeliveryPath_CrashBeforeTheLeaseMintsExactlyOneIdentity(t *testing.T) {
	for _, format := range []int{1, 2} {
		t.Run(formatName(format), func(t *testing.T) {
			nonce := testDeliveryToken('b')
			r := newCrashRun(t, format, crashL4)
			r.deliver(nonce)
			require.True(t, r.cutDone)

			root := restoreCrashImage(t, r.image())
			writeSpoolLine(t, root, "client-00008.ndjson",
				observeRequest(nonce, crashSession, `{"hook_event_name":"PostToolUse"}`))

			dd, _ := newObservingDaemon(t, root)
			lock, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			lock.sealFormat = format
			dd.startMu.Lock()
			dd.lock = lock
			dd.startMu.Unlock()
			t.Cleanup(func() { _ = lock.Release() })

			journal, err := dd.deliveryJournal()
			require.NoError(t, err)
			require.Empty(t, journal.leases, "the crash left no identity for the nonce")

			_, err = dd.Drain(context.Background())
			require.NoError(t, err)
			lease, ok := journal.leases[nonce]
			require.True(t, ok, "the drain leases the recovered copy")
			require.Equal(t, uint64(1), lease.ArrivalSeq, "it is the session's FIRST arrival, not a second one")
			require.Len(t, journal.leases, 1, "two copies of one nonce take one identity")

			// A second pass changes nothing: the nonce is now known, and a known nonce appends
			// nothing and mints nothing.
			before := readTestFile(t, journal.path)
			_, err = dd.Drain(context.Background())
			require.NoError(t, err)
			require.Equal(t, before, readTestFile(t, journal.path))
			require.Len(t, journal.leases, 1)
		})
	}
}

// newCrashIngest is an ingest on its own root with the WAL seams a crash row needs, for the rows
// that never reach the journal at all. It exists so that the WAL half of design §3 can be cut
// without a daemon, which is how the table's first rows are stated.
func newCrashIngest(t *testing.T, root string) *ingest {
	t.Helper()
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, newFakeClock(epoch))
	t.Cleanup(func() { _ = ing.Close() })
	return ing
}

// The WAL half of design §3 rows 1-3, stated on the ingest alone: a batch cut before its Write
// leaves the segment as it was, one cut before its Sync leaves bytes a machine crash takes, and a
// rotation syncs the outgoing segment before it closes it, so a batch's lines are never lost to the
// rotation itself (§3 row 3).
func TestDeliveryPath_WALCrashCutsLeaveOnlyWhatWasSynced(t *testing.T) {
	const sess = core.SessionID("sess-wal-crash")

	t.Run("a Write that never landed leaves the segment as it was", func(t *testing.T) {
		root := t.TempDir()
		ing := newCrashIngest(t, root)
		require.NoError(t, ing.Accept(ipc.Request{Op: ipc.OpObserveTool, Session: sess},
			[]byte(`{"op":"observe.tool","s":"sess-wal-crash","t":1}`)))
		before := readTestFile(t, walPath(paths.Of(root).Spool, sess, 0))

		ing.writeWAL = func(*os.File, []byte) (int, error) { return 0, errCrashCut }
		require.ErrorIs(t, ing.Accept(ipc.Request{Op: ipc.OpObserveTool, Session: sess},
			[]byte(`{"op":"observe.tool","s":"sess-wal-crash","t":2}`)), errCrashCut)
		require.Equal(t, before, readTestFile(t, walPath(paths.Of(root).Spool, sess, 0)),
			"a Write that failed puts no bytes in the segment")
	})

	t.Run("a Sync that never returned leaves the line unacknowledged", func(t *testing.T) {
		root := t.TempDir()
		ing := newCrashIngest(t, root)
		ing.syncWAL = func(*os.File) error { return errCrashCut }
		require.ErrorIs(t, ing.Accept(ipc.Request{Op: ipc.OpObserveTool, Session: sess},
			[]byte(`{"op":"observe.tool","s":"sess-wal-crash","t":1}`)), errCrashCut,
			"Accept must not report a line durable when the Sync covering it failed")
		// The bytes are in the file, and a machine crash takes them with the page cache. What the
		// test can assert here is the half that is observable in process: nothing was acknowledged.
		synced, held := ing.syncedWAL(walPath(paths.Of(root).Spool, sess, 0))
		require.True(t, held)
		require.Zero(t, synced, "no Sync returned, so nothing of the segment is known durable")
	})
}
