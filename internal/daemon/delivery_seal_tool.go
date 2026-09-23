package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// The offline delivery-seal tool (SP20-D1 design §4.5).
//
// A v2 seal is read by every build that carries the dual reader, and a clean Release rewrites it as
// v1, so an operator needs this tool for one situation only: a project whose position files a
// daemon left in a state the binary they want to run next cannot open. Design §4.4 names them — a
// v2 file left by a crash in front of a build that predates the reader, and a seal whose slots the
// strict reader refuses (§2.9).
//
// Three properties make it safe to hand to an operator:
//
//   - It takes the daemon lock through AcquireLock, so it refuses a project a daemon is serving, and
//     it re-reads that lock before it writes anything, so a daemon that started meanwhile takes the
//     project away from the tool rather than the other way round. A repair racing the writer would
//     be the very corruption it exists to undo.
//
//     That refusal has a ceiling, and it is worth naming rather than implying. On Windows pidAlive
//     has no opinion (lock_windows.go), so a starting daemon judges this lock by daemon.hb's mtime
//     alone, and staleAfter is 90 seconds. This run refreshes that mtime only at its three holdsLock
//     calls — after both scans, and before each of the two writes — and never DURING a scan. Two
//     journals of up to 64 MiB each, or a machine that sleeps mid-run, can therefore outlive the
//     window and let a daemon reclaim the lock under the tool. The outcome is fail-closed: the next
//     holdsLock refuses, nothing is written, and the deferred Release declines to delete the new
//     owner's files. It costs a repair, never a correct file.
//   - It writes nothing until BOTH journals have loaded in full, through the journal's own scan. A
//     position that does not seal a real prefix of its journal is never written anywhere.
//   - Rule R lives here and nowhere else. The daemon's reader stays strict whatever an operator
//     decides, and the acceptance costs an explicit confirmation.
//
// It never edits a journal. Every repair it can make is to a position file, which is derived state:
// the journals are the evidence, and preserving them is what each of its refusals is for.

// deliverySealToolName is what the tool calls itself in its report and its errors. internal/cli
// registers it as `qompack admin delivery-seal`, whose final spelling SP-17 settles.
const deliverySealToolName = "delivery-seal"

// DeliverySealOptions is one run of the offline delivery-seal tool (design §4.5).
//
// Exactly one of Check and ToV1 chooses what the run does; the rest qualify it.
type DeliverySealOptions struct {
	// ProjectRoot is the project whose .qompack/state holds the two journals and their seals.
	ProjectRoot string
	// Check runs the full dual reader and the full load for both seals and reports what it found.
	// It writes nothing to the journals or their seals, whatever it finds — which is the property
	// that matters, and the whole of what it promises. It is not inert on the project as a whole:
	// holding the daemon lock creates .qompack/run/ with daemon.lock and daemon.hb, refreshes that
	// heartbeat, and leaves the directory behind when Release removes the two files.
	Check bool
	// ToV1 converts both seals to v1 with paths.WriteAtomic, and only after a successful full load.
	// The position it writes is the one the load RECOVERED, which is the position an open would
	// seal: a complete canonical tail past the seal moves it forward, exactly as design O4c does.
	ToV1 bool
	// AcceptTornSlot is Rule R, and this field is the only way to reach it (design §2.9, §4.5). It
	// accepts a seal with one valid slot beside one invalid slot, taking the valid record as the
	// position, and only when the journal then loads in full. It is refused without Confirm.
	AcceptTornSlot bool
	// Confirm is the explicit confirmation AcceptTornSlot requires. Accepting a torn slot accepts a
	// position the daemon's own reader refuses, so it is an operator's decision to record, never a
	// default to fall into.
	Confirm bool
	// Out is where the report goes. Every line the tool prints is written here.
	Out io.Writer
	// Clock is the clock AcquireLock's staleness protocol reads. A nil clock is the system clock,
	// which is what an operator's run uses.
	Clock core.Clock
	// syncData is the Sync syncJournal takes on a journal before its seal names that journal's tail.
	// A nil syncData — every caller outside this package's tests — is (*os.File).Sync, the very call
	// both open paths make in the same place.
	//
	// It is a field for one reason: a successful fsync leaves no trace a test in this process can
	// see, so the only way to pin that the RUN takes the step, rather than that the step is correct
	// when taken, is to make it fail. deliverySeal.syncData is the same seam for the same class of
	// step, and this is that convention rather than a new one.
	syncData func(*os.File) error
}

// RepairDeliverySeal runs the offline delivery-seal tool against o.ProjectRoot (design §4.5).
//
// It takes the daemon lock first and holds it for the whole run, so a project a daemon is serving is
// refused with an error satisfying errors.Is(err, ErrLockHeld) and nothing is read or written. On
// any other failure it reports what refused and leaves every file as it found it: a position file
// this tool cannot vouch for is evidence, and evidence is preserved rather than repaired.
func RepairDeliverySeal(o DeliverySealOptions) error {
	if err := o.validate(); err != nil {
		return err
	}
	addr, err := ipc.Resolve(o.ProjectRoot)
	if err != nil {
		return fmt.Errorf("%s: resolving the daemon endpoint: %w", deliverySealToolName, err)
	}
	lock, err := AcquireLock(o.ProjectRoot, addr, o.Clock)
	if err != nil {
		if errors.Is(err, ErrLockHeld) {
			return fmt.Errorf("%s: a daemon is running in %s and owns the journals; stop it first: %w",
				deliverySealToolName, o.ProjectRoot, err)
		}
		return fmt.Errorf("%s: taking the daemon lock: %w", deliverySealToolName, err)
	}
	defer func() { _ = lock.Release() }()
	return o.run(lock)
}

// validate refuses an option set before the tool touches the project.
//
// The confirmation is checked here rather than in a front end so that the requirement belongs to
// the tool: every caller that can reach Rule R comes through this function.
func (o DeliverySealOptions) validate() error {
	switch {
	case o.Out == nil:
		return fmt.Errorf("%s: no writer for the report", deliverySealToolName)
	case o.ProjectRoot == "":
		return fmt.Errorf("%s: no project root", deliverySealToolName)
	case o.Check == o.ToV1:
		return fmt.Errorf("%s: run exactly one of a check and a conversion to v1", deliverySealToolName)
	case o.AcceptTornSlot && !o.Confirm:
		return fmt.Errorf("%s: accepting a torn slot accepts a position the daemon's own reader "+
			"refuses; it needs explicit confirmation", deliverySealToolName)
	}
	return nil
}

// latchingWriter is o.Out with its first write error kept.
//
// Every report line goes through fmt.Fprintf, which returns an error nobody can usefully act on
// line by line — but one of those lines is not decoration. Design §4.5 asks the tool to print
// exactly which lines --accept-torn-slot admitted, and that print is the operator's only record of
// what they took responsibility for: the accepted record seals a position the daemon's own reader
// refuses, and the lines past it are what an operator is being asked to own. Under a closed pipe
// (`qompack admin delivery-seal --to v1 --accept-torn-slot --yes | head`) the conversion would
// still happen while that record was lost.
//
// So the writes are not checked one at a time; the first failure is latched, and the run consults
// it once, at the point where losing the record would cost something.
type latchingWriter struct {
	w   io.Writer
	err error
}

func (w *latchingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}

// run is the tool with the daemon lock held.
func (o DeliverySealOptions) run(lock *Lock) error {
	report := &latchingWriter{w: o.Out}
	o.Out = report
	state := paths.Of(o.ProjectRoot).State

	fmt.Fprintf(o.Out, "%s: %s\n", deliverySealToolName, o.ProjectRoot)

	// A rotated (segmented) store is checked across its authority, every committed segment and the
	// generation store — the legacy-only path below would check only segment 0 and report the whole
	// store as fine while the active segment is corrupt. A genuinely unmigrated tree keeps the exact
	// legacy behavior; migration evidence without an authority is head loss and refuses. This read is
	// READ-ONLY: it never creates the log/dirs or recovers by writing a head (delivery_segment_readonly.go).
	auth, migrated, aerr := readonlySegmentAuthority(context.Background(), state)
	if aerr != nil {
		return fmt.Errorf("%s: %s carries delivery migration evidence its authority cannot vouch for; "+
			"nothing was written. Preserve it and roll back via a verified backup and a compatible "+
			"reader: %w", deliverySealToolName, state, aerr)
	}
	// A store whose authority still names segment 0 has never rotated: segment 0 is its one, active
	// journal, exactly as on a store written before segments, so the legacy tool below — check,
	// conversion to v1 and Rule R — applies to it unchanged (the authority chain was validated
	// read-only just above). Only a store that has rotated needs the segmented walk.
	if migrated && auth.transitions[len(auth.transitions)-1].Active >= 1 {
		return o.runSegmented(lock, state, auth)
	}

	j := newDeliveryJournal(lock, filepath.Join(state, deliveryLeaseFile))
	j.ackPath = filepath.Join(state, deliveryAckFile)

	sides, err := deliverySealSides(j)
	if err != nil {
		return err
	}
	if len(sides) == 0 {
		fmt.Fprintf(o.Out, "  no delivery journal in this project; nothing to check\n")
		return nil
	}
	// Both sides are inspected before either is written. --to v1 converts "only after a successful
	// full load" (design §4.5), and a full load is both of them: the acknowledgement scan is what
	// checks every acknowledgement against a surviving lease, reading the map the lease scan filled,
	// so the two are one answer rather than two.
	for i := range sides {
		if err := o.inspect(&sides[i]); err != nil {
			return err
		}
	}
	// The lock is read again once both journals have been scanned: before anything is written, and
	// before this run states a verdict. The per-side report lines are already out by then, and they
	// are the one thing here that does not need the lock to still hold: each says what one seal and
	// one journal held at the moment they were read, which is true whoever owns the project now.
	// Scanning two journals of up to 64 MiB each, on a machine that may sleep between the two, can
	// outlive the window AcquireLock's refusal rests on.
	if err := o.holdsLock(lock, "after reading both journals"); err != nil {
		return err
	}
	if o.Check {
		fmt.Fprintf(o.Out, "  checked; nothing was written\n")
		return nil
	}
	// A Rule R acceptance whose lines could not be reported refuses the conversion, before anything
	// is synced or written. The consent is what makes the acceptance an operator's decision rather
	// than a reader's, and the printed lines are the whole of the record of it; converting anyway
	// would leave a project whose position no daemon would have accepted and no report saying which
	// lines that covers. Nothing here is corrupted by refusing — the seals are as the run found them
	// — and the rerun is the same command with a writer that works.
	//
	// It is checked after the --check return rather than before it, because the two runs lose
	// different things: a check that could not print changed nothing and is simply repeatable, while
	// a conversion that could not print would be a change with its record missing.
	if report.err != nil && anyAcceptedATornSlot(sides) {
		return fmt.Errorf("%s: --accept-torn-slot admitted lines that could not be reported, and the "+
			"report is the record of what was accepted; nothing was synced or written: %w",
			deliverySealToolName, report.err)
	}
	// Both tails are made durable before either seal names one. A seal made durable over a tail that
	// is not is how a journal becomes permanently unopenable, and the sync is the same step, in the
	// same place, that both open paths take (syncJournal).
	for i := range sides {
		if err := o.syncJournal(&sides[i]); err != nil {
			return err
		}
	}
	for i := range sides {
		// The lock is read once more immediately before each write, so that nothing but the call it
		// authorizes stands between the ownership check and the WriteAtomic — the same ordering every
		// batch in this package gives its own append (design §2.10). It is taken here rather than
		// inside convert so that each of the two failures carries the advice that fits it: a run
		// dispossessed between the two seals must not tell an operator to rerun a command the daemon
		// that took the project now refuses.
		if err := o.holdsLock(lock, "before converting the "+sides[i].name+" seal"); err != nil {
			return halfConverted(err, sides[:i], rerunAdvice(err))
		}
		if err := o.convert(&sides[i]); err != nil {
			return halfConverted(err, sides[:i], "rerunning the same command converts only what is left")
		}
	}
	return nil
}

// runSegmented is the tool against a rotated (segmented) store. --check validates the whole store
// read-only: the generation manifest+head chain, and every committed segment's lease and ack journals
// against their seals — segment 0 (the legacy four files) AND every later segment — with predecessor-
// base arrivals and archived-ACK exact joins through the generation store.
//
// Rule R (--accept-torn-slot, with its confirmation) applies to the ACTIVE segment only: its seal is the
// only one a crash can tear mid-write, since an archived segment's last seal completed before it
// rotated, and an archived legacy segment 0 carries a frozen seal replaced whole. With --check the
// accepted lines are reported and nothing is written. With --to v1 it is the repair: once the whole
// store has checked, each ACCEPTED active-segment seal is rewritten as v1 at the position its journal's
// full scan recovered, the journal made durable first. --to v1 without a torn slot to repair is
// REFUSED, because converting a seal does not make an old writer able to read the segmented
// authority/history, and this tool does not pretend it does.
func (o DeliverySealOptions) runSegmented(lock *Lock, state string, auth segmentAuthorityReading) error {
	if o.ToV1 && !o.AcceptTornSlot {
		return o.refuseSegmentedConversion(state)
	}
	if auth.recoveredTail {
		fmt.Fprintf(o.Out, "  note: a complete committed transition beyond the atomic head is present and was "+
			"carried forward as the active view; no on-disk checkpoint was written\n")
	}
	ctx := context.Background()
	// Anchor the trusted state root; every descendant directory and file is reached through pinned
	// os.Root handles (rejecting static symlink aliases and confirming SameFile), not absolute paths.
	stateRoot, err := pinDeliveryDirectory(state)
	if err != nil {
		return fmt.Errorf("%s: %s could not be pinned as the trusted state root; nothing was written: %w",
			deliverySealToolName, state, deliveryJournalError())
	}
	defer func() { _ = stateRoot.Close() }()

	gv, genTail, err := openGenReadonly(ctx, stateRoot)
	if err != nil {
		return fmt.Errorf("%s: a segmented store requires its generation store, which did not validate "+
			"read-only; nothing was written: %w", deliverySealToolName, err)
	}
	defer func() { _ = gv.close() }()
	if genTail {
		fmt.Fprintf(o.Out, "  note: a complete committed generation beyond the manifest head is present and "+
			"was carried forward as the recovered root; no on-disk checkpoint was written\n")
	}
	// A store that has never rotated archives nothing, so an empty generation store is exactly what it
	// should have. Once a segment past 0 is committed its base root was archived first, and an empty
	// store there is lost history — the producer refuses to open it, and so does this check.
	if gv.root.isZero() && auth.transitions[len(auth.transitions)-1].Active >= 1 {
		return fmt.Errorf("%s: the authority names segment %d, but the generation store holds no archived "+
			"history; nothing was written: %w", deliverySealToolName,
			auth.transitions[len(auth.transitions)-1].Active, errSegmentReaderRefused)
	}

	// The segments directory is pinned once, only if any non-legacy segment exists.
	var segsRoot *os.Root
	if auth.transitions[len(auth.transitions)-1].Active >= 1 {
		segsRoot, err = pinDeliveryChild(stateRoot, deliverySegmentsDir, false)
		if err != nil {
			return fmt.Errorf("%s: %s could not be pinned; nothing was written: %w",
				deliverySealToolName, deliverySegmentsDir, deliveryJournalError())
		}
		defer func() { _ = segsRoot.Close() }()
	}

	var repairs []segmentSealRepair
	for i := range auth.transitions {
		last := i == len(auth.transitions)-1
		found, err := o.checkSegment(ctx, state, stateRoot, segsRoot, auth.transitions[i], gv, last)
		if err != nil {
			return err
		}
		repairs = append(repairs, found...)
	}
	// The lock is re-read after the whole read-only scan and before the verdict, exactly as the legacy
	// path does: scanning many segments can outlive the staleness window, and a run that can no longer
	// vouch for the lock refuses rather than pretending. Nothing was written regardless.
	if err := o.holdsLock(lock, "after reading the segmented store"); err != nil {
		return err
	}
	active := auth.transitions[len(auth.transitions)-1].Active
	if o.Check {
		fmt.Fprintf(o.Out, "  checked %d committed segment(s) through segment %d and the generation store; "+
			"nothing was written\n", len(auth.transitions), active)
		return nil
	}
	if len(repairs) == 0 {
		return o.refuseSegmentedConversion(state)
	}
	// As in the legacy path: an acceptance whose lines could not be reported refuses the repair before
	// anything is synced or written, since the report is the whole record of what was accepted.
	if report, ok := o.Out.(*latchingWriter); ok && report.err != nil {
		return fmt.Errorf("%s: --accept-torn-slot admitted lines that could not be reported, and the "+
			"report is the record of what was accepted; nothing was synced or written: %w",
			deliverySealToolName, report.err)
	}
	for i := range repairs {
		if err := o.syncSegmentJournal(repairs[i]); err != nil {
			return err
		}
	}
	for i := range repairs {
		if err := o.holdsLock(lock, "before repairing the "+repairs[i].name+" seal of segment "+
			fmt.Sprint(active)); err != nil {
			return err
		}
		r := repairs[i]
		if err := writeDeliveryPositionV1(r.seal, r.recovered.Bytes, r.recovered.Count, r.recovered.Chain); err != nil {
			return fmt.Errorf("%s: segment %d %s: writing the v1 seal at %s: %w",
				deliverySealToolName, active, r.name, r.seal, err)
		}
		fmt.Fprintf(o.Out, "  segment %d %s seal %s: wrote v1 at %d entries, %d bytes (the accepted record "+
			"sealed %d entries, %d bytes)\n", active, r.name, r.seal, r.recovered.Count, r.recovered.Bytes,
			r.accepted.Count, r.accepted.Bytes)
	}
	return nil
}

// refuseSegmentedConversion is the --to v1 refusal for a segmented store with nothing to repair.
func (o DeliverySealOptions) refuseSegmentedConversion(state string) error {
	return fmt.Errorf("%s: %s is a segmented delivery store; --to v1 is refused. A seal conversion "+
		"does NOT make an old writer able to read the segment authority or history, and this tool will "+
		"not pretend it does. Every file is preserved as it is; roll a segmented store back only through "+
		"a verified backup restored by a compatible reader. (--to v1 --accept-torn-slot repairs a torn "+
		"slot in the ACTIVE segment's seal, and nothing else.)", deliverySealToolName, state)
}

// segmentSealRepair is one active-segment seal Rule R accepted: the absolute journal and seal paths,
// the record accepted, the position the journal's full scan recovered, and the journal file scanned.
type segmentSealRepair struct {
	name, journal, seal string
	accepted, recovered deliveryPosition
	info                os.FileInfo
}

// syncSegmentJournal makes a repaired journal durable before its seal is written to name its tail —
// syncJournal's step, for a segment's journal: the file must still be the one the scan read, at the
// length the scan ended at.
func (o DeliverySealOptions) syncSegmentJournal(r segmentSealRepair) error {
	side := deliverySealSide{
		name: r.name, journal: r.journal, info: r.info,
		recovered: func() deliveryPosition { return r.recovered },
	}
	return o.syncJournal(&side)
}

// checkSegment validates one committed segment read-only, THROUGH a pinned os.Root: its four files
// present (and re-verified under the pinned identity), its lease journal against its seal with arrivals
// resuming from the segment's predecessor (base) root, and its ack journal against its seal with every
// acknowledgement joined to its original lease — in the active segment's own window (active is true for
// the last committed segment, which is archived only when it rotates) or in the generation store.
// Segment 0 is the legacy four files under the state root (arrivals from 1); a later segment is pinned
// under the segments root and resumes from base_root. It returns the seals of this segment Rule R
// accepted (only ever the active segment's), with the accepted lines already reported.
func (o DeliverySealOptions) checkSegment(ctx context.Context, state string, stateRoot, segsRoot *os.Root, t segTransition, gv *genReadonly, active bool) ([]segmentSealRepair, error) {
	segRoot := stateRoot
	if t.Active >= 1 {
		pinned, err := pinDeliveryChild(segsRoot, segmentSeqName(t.Active), false)
		if err != nil {
			return nil, fmt.Errorf("%s: segment %d: its directory is missing, not a directory, or a static "+
				"alias; the authority names a segment whose evidence cannot be pinned, and nothing was "+
				"written: %w", deliverySealToolName, t.Active, deliveryJournalError())
		}
		defer func() { _ = pinned.Close() }()
		segRoot = pinned
	}
	// Verify the four files are present as regular files under the pinned identity before the scan.
	for _, name := range []string{deliveryLeaseFile, deliveryPositionFile, deliveryAckFile, deliveryAckPositionFile} {
		info, err := segRoot.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: segment %d: %s is missing or not a regular file; the authority names a "+
				"segment whose evidence is incomplete, and nothing was written: %w",
				deliverySealToolName, t.Active, name, deliveryJournalError())
		}
	}

	var (
		arrivalBase func(core.SessionID) (uint64, bool, error)
		br          radixHash
	)
	if t.Active >= 1 {
		var ok bool
		br, ok = hexToRadixHash(t.BaseRoot)
		if !ok {
			return nil, fmt.Errorf("%s: segment %d: its base root is malformed; nothing was written: %w",
				deliverySealToolName, t.Active, deliveryJournalError())
		}
		// The predecessor arrival is looked up at the segment's base root; a missing/corrupt page is an
		// ERROR the scan propagates (never a silent "new session at 1").
		arrivalBase = func(s core.SessionID) (uint64, bool, error) { return gv.arrivalAt(ctx, br, s) }
	}

	// Only the ARCHIVED legacy segment carries frozen seals (the old-reader barrier), and only the
	// ACTIVE segment's seals are within Rule R's reach.
	opts := segSealOptions{frozenOK: t.Active == 0 && !active, ruleR: active && o.AcceptTornSlot}
	dir := segmentDir(state, t.Active)
	var repairs []segmentSealRepair
	leasePos, window, leaseAccepted, err := gv.checkLeaseJournal(ctx, segRoot, deliveryLeaseFile, deliveryPositionFile, arrivalBase, !active, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: segment %d: the lease journal does not check read-only against its seal, "+
			"its predecessor arrivals and the generation store; nothing was written: %w",
			deliverySealToolName, t.Active, err)
	}
	fmt.Fprintf(o.Out, "  segment %d lease journal: loads, %d entries, %d bytes, chain %s\n",
		t.Active, leasePos.Count, leasePos.Bytes, leasePos.Chain)
	if leaseAccepted {
		r, err := o.acceptedSegmentSeal(segRoot, "lease", dir, deliveryLeaseFile, deliveryPositionFile,
			deliveryChainSeed, deliveryChainDomain, leasePos)
		if err != nil {
			return nil, err
		}
		repairs = append(repairs, r)
	}

	// The active segment is not archived yet: its acknowledgements join its own window first.
	if !active {
		window = nil
	}
	ackPos, ackAccepted, err := gv.checkAckJournal(ctx, segRoot, deliveryAckFile, deliveryAckPositionFile, window, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: segment %d: the ack journal does not check read-only against its seal and "+
			"the generation store; nothing was written: %w", deliverySealToolName, t.Active, err)
	}
	// Re-verify the two journals still resolve to regular files under the pinned identity after the scan.
	for _, name := range []string{deliveryLeaseFile, deliveryAckFile} {
		if info, err := segRoot.Lstat(name); err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: segment %d: %s changed under the pinned scan; nothing was written: %w",
				deliverySealToolName, t.Active, name, deliveryJournalError())
		}
	}
	fmt.Fprintf(o.Out, "  segment %d ack journal: loads, %d entries, %d bytes (archived ACKs joined to "+
		"their original leases)\n", t.Active, ackPos.Count, ackPos.Bytes)
	if t.Active >= 1 {
		carried, err := gv.checkCarry(ctx, segRoot, t.Active, br)
		if err != nil {
			return nil, fmt.Errorf("%s: segment %d: its carried-lease file does not check read-only against "+
				"its header and the generation store at the segment's base root; nothing was written: %w",
				deliverySealToolName, t.Active, err)
		}
		fmt.Fprintf(o.Out, "  segment %d carried leases: %d archived with no acknowledgement, each resolved "+
			"at the base root\n", t.Active, carried)
	}
	if ackAccepted {
		r, err := o.acceptedSegmentSeal(segRoot, "ack", dir, deliveryAckFile, deliveryAckPositionFile,
			deliveryAckChainSeed, deliveryAckChainDomain, ackPos)
		if err != nil {
			return nil, err
		}
		repairs = append(repairs, r)
	}
	return repairs, nil
}

// acceptedSegmentSeal records one Rule R acceptance on the active segment and reports exactly which
// lines it admitted (design §4.5): the record accepted, and every complete line past it.
func (o DeliverySealOptions) acceptedSegmentSeal(segRoot *os.Root, name, dir, journal, seal string, seed core.Hash, domain string, recovered deliveryPosition) (segmentSealRepair, error) {
	read, err := readSealConfined(segRoot, seal, seed, domain, segSealOptions{ruleR: true})
	if err != nil || !read.accepted {
		return segmentSealRepair{}, fmt.Errorf("%s: %s seal %s changed during the check; nothing was written: %w",
			deliverySealToolName, name, seal, deliveryJournalError())
	}
	info, err := segRoot.Lstat(journal)
	if err != nil || !info.Mode().IsRegular() || info.Size() != recovered.Bytes {
		return segmentSealRepair{}, fmt.Errorf("%s: %s journal %s changed during the check; nothing was written: %w",
			deliverySealToolName, name, journal, deliveryJournalError())
	}
	r := segmentSealRepair{
		name: name, journal: filepath.Join(dir, journal), seal: filepath.Join(dir, seal),
		accepted: read.position, recovered: recovered, info: info,
	}
	fmt.Fprintf(o.Out, "  %s seal %s: v2 with one torn slot; accepting %d entries, %d bytes\n",
		name, r.seal, read.position.Count, read.position.Bytes)
	side := deliverySealSide{name: name, journal: r.journal}
	if err := o.reportAcceptedLines(&side, read.position); err != nil {
		return segmentSealRepair{}, err
	}
	return r, nil
}

// halfConverted names the seals a conversion had already written when a later one failed.
//
// The seals are converted one after another, so a failure on the second leaves the pair half
// converted — which is the very state the operator ran the tool to leave behind, since a pre-step-1
// binary still refuses whichever seal is still v2. Nothing is corrupted and the state is
// self-healing: a rerun reads the v1 seal this run wrote through the dual reader's v1 branch and
// converts what is left. The error says so, rather than leaving an operator to infer it from the
// report.
//
// next is what to do about the rest, and the caller chooses it because the failures differ in
// exactly that: a failed write leaves the project to this operator, while a run dispossessed
// mid-conversion leaves it to the daemon that took it, which refuses the same rerun with
// ErrLockHeld until it is stopped. A run whose own heartbeat failed has no such daemon to stop and
// is told to rerun, which rerunAdvice decides from the refusal itself. done is what was converted
// before the failure; an empty done is the first seal's own failure, which leaves nothing of the
// kind to say.
func halfConverted(err error, done []deliverySealSide, next string) error {
	if len(done) == 0 {
		return err
	}
	names := make([]string, 0, len(done))
	for _, converted := range done {
		names = append(names, converted.name)
	}
	return fmt.Errorf("%w; the %s seal is already v1, so %s", err, strings.Join(names, " and "), next)
}

// holdsLock refuses the run unless this process still owns the daemon lock.
//
// AcquireLock's refusal is the front door, and it is not the only door that matters: a daemon may
// start while the tool is running. On Windows pidAlive has no opinion at all (lock_windows.go), so
// the staleness protocol falls through to daemon.hb's mtime, and a run that outlives staleAfter — a
// suspended process, a machine that slept — lets a starting daemon judge this lock stale, remove it
// and take the project over. Without this the tool would go on to write both seals over a project
// that daemon is now serving, which is exactly what this file's doc comment promises it never does.
//
// Every other writer in this package re-checks the same way: a lease batch and an acknowledgement
// batch each read the lock file once their members have arrived and before anything is appended
// (design §2.10), and Release re-checks before it deletes. This is the tool's version of that rule,
// taken immediately before each write, so what it writes it owned at the moment it wrote it.
//
// It goes through Heartbeat rather than ownedByFile alone because that answers both questions in
// one call: Heartbeat refuses unless owned() still holds, and it refreshes the very mtime the
// staleness protocol reads, which shrinks the window instead of only reporting it afterwards. A
// heartbeat that cannot be written is a lock this run cannot keep either, so it refuses too.
//
// The two refusals are worded apart, because they send an operator to different places. Heartbeat
// fails for two unrelated reasons — the lock file no longer names this acquisition, and the write
// of daemon.hb itself failing (the run directory removed under the tool, os.Chtimes denied, an
// anti-virus or backup agent holding the file) — and only the FIRST means a daemon owns the
// journals now. Telling an operator to stop a daemon that is not there, while the rerun that would
// actually work goes unmentioned, is the same class of wrong advice halfConverted's `next` clause
// was split to avoid, one layer down.
//
// The discriminator is the lock file itself, not the error: a daemon that started meanwhile has
// WRITTEN a lock file naming its own acquisition, so "a readable lock file that is not ours" is a
// takeover and everything else — our own lock file still there, or no lock file at all — is this
// run losing a lock nobody else took. Only the first carries errDaemonTookTheProject, which is what
// the conversion loop reads to choose its advice.
func (o DeliverySealOptions) holdsLock(lock *Lock, doing string) error {
	err := lock.Heartbeat()
	if err == nil {
		return nil
	}
	if _, present := readLockFile(lock.path); present && !lock.ownedByFile() {
		return fmt.Errorf("%s: %s: this process no longer owns the daemon lock in %s; a daemon that "+
			"started meanwhile owns the journals now, and this step was refused rather than written: "+
			"%w: %w", deliverySealToolName, doing, o.ProjectRoot, errDaemonTookTheProject, err)
	}
	return fmt.Errorf("%s: %s: this run can no longer vouch for the daemon lock in %s — no other "+
		"daemon has taken the project, but its own heartbeat could not be written, so the lock may be "+
		"judged stale and reclaimed under it. This step was refused rather than written: %w",
		deliverySealToolName, doing, o.ProjectRoot, err)
}

// errDaemonTookTheProject marks the holdsLock refusal that means another daemon owns the journals
// now, as opposed to one whose own heartbeat merely could not be written. The advice differs by
// exactly that, and nothing else in this file branches on it.
var errDaemonTookTheProject = errors.New(deliverySealToolName + ": a daemon owns the journals now")

// rerunAdvice is what to do about the seals a dispossessed run did not convert.
//
// A run the daemon took the project from must not tell an operator to rerun the same command: the
// rerun is refused with ErrLockHeld until that daemon is stopped. A run that merely could not
// heartbeat has no such daemon to stop, and the same command is exactly what finishes the pair.
func rerunAdvice(err error) string {
	if errors.Is(err, errDaemonTookTheProject) {
		return "stopping that daemon and rerunning converts what is left"
	}
	return "rerunning the same command converts what is left"
}

// deliverySealSide is one journal and the seal that seals it: the unit the tool checks and converts.
type deliverySealSide struct {
	// name is what the report calls this journal.
	name string
	// journal is the .jsonl file, and seal the position file beside it.
	journal, seal string
	// domain and seed are the chain identity every record of this seal is bound to, which is what
	// keeps the two seals from ever being read against each other.
	domain string
	seed   core.Hash
	// scan runs the journal's own load against a position the tool read, filling the journal's
	// in-memory state, and returns the file it scanned.
	scan func(position deliveryPosition, older *sealRecord) (os.FileInfo, error)
	// recovered is the position the scan ended at: the one an open would seal.
	recovered func() deliveryPosition
	// position is what the seal on disk holds, once inspect has read it.
	position deliveryPosition
	// accepted records that Rule R chose this side's position, so the run can refuse a conversion
	// whose consent record could not be printed.
	accepted bool
	// info is the journal file the scan read, once inspect has run: the file, and the only file,
	// whose tail this side's seal may be made to name.
	info os.FileInfo
}

// deliverySealSides is the two sides in the order they must be scanned: leases, then the
// acknowledgements that name them.
//
// A pair whose journal and seal are both ABSENT is neither a defect nor a repair: the open creates
// both on a project's first use, and a store written before the acknowledgement journal existed has
// no ack pair at all. Such a pair is left out, so the tool reports what is there and creates
// nothing. A HALF-present pair is refused, exactly as openDeliveryJournal refuses it: which of the
// two to believe is a recovery decision, not a repair a tool may make on its own.
func deliverySealSides(j *deliveryJournal) ([]deliverySealSide, error) {
	lease := deliverySealSide{
		name: "lease", journal: j.path, seal: j.positionPath(),
		domain: deliveryChainDomain, seed: deliveryChainSeed,
		scan: func(position deliveryPosition, older *sealRecord) (os.FileInfo, error) {
			return j.loadFrom(position, older)
		},
		recovered: func() deliveryPosition {
			return deliveryPosition{
				Version: core.EvidenceVersion, Bytes: j.bytes, Count: len(j.leases), Chain: j.chain,
			}
		},
	}
	ack := deliverySealSide{
		name: "ack", journal: j.ackPath, seal: j.ackSealPath(),
		domain: deliveryAckChainDomain, seed: deliveryAckChainSeed,
		scan: func(position deliveryPosition, older *sealRecord) (os.FileInfo, error) {
			return j.loadAcksFrom(position, older)
		},
		recovered: func() deliveryPosition {
			return deliveryPosition{
				Version: core.EvidenceVersion, Bytes: j.ackBytes, Count: len(j.acks), Chain: j.ackChain,
			}
		},
	}

	var sides []deliverySealSide
	for _, s := range []deliverySealSide{lease, ack} {
		present, err := deliverySealPairPresent(s)
		if err != nil {
			return nil, err
		}
		if present {
			sides = append(sides, s)
		}
	}
	// An acknowledgement journal without its leases cannot be scanned at all: every acknowledgement
	// it holds must name a lease the scan recovered, and there is no lease journal to recover one
	// from. That is the same half-present refusal, one level up.
	if len(sides) == 1 && sides[0].name == ack.name {
		return nil, fmt.Errorf("%s: %s is present but the lease journal its rows name is not; "+
			"this is a recovery decision, not a repair", deliverySealToolName, ack.journal)
	}
	return sides, nil
}

// deliverySealPairPresent reports whether a journal and its seal are both there.
func deliverySealPairPresent(s deliverySealSide) (bool, error) {
	_, journalErr := os.Lstat(paths.Long(s.journal))
	_, sealErr := os.Lstat(paths.Long(s.seal))
	switch {
	case os.IsNotExist(journalErr) && os.IsNotExist(sealErr):
		return false, nil
	case journalErr != nil || sealErr != nil:
		return false, fmt.Errorf("%s: %s: %s and %s must both be present or both absent; "+
			"this is a recovery decision, not a repair",
			deliverySealToolName, s.name, s.journal, s.seal)
	}
	return true, nil
}

// sealReading is what the tool made of one seal file.
type sealReading struct {
	// position is the sealed position to scan the journal against.
	position deliveryPosition
	// older is the record one batch behind it, when a v2 image carries one, for the scan's
	// older-seal checkpoint.
	older *sealRecord
	// accepted records that Rule R chose this position, which is what makes the tool print the
	// lines the acceptance admitted.
	accepted bool
}

// inspect reads one side's seal through the dual reader and scans its journal against it: design
// §4.5's "--check runs the full dual reader and load for both seals", and the step --to v1 converts
// only after.
func (o DeliverySealOptions) inspect(s *deliverySealSide) error {
	read, err := o.readSeal(s)
	if err != nil {
		return err
	}
	s.position = read.position
	info, err := s.scan(read.position, read.older)
	if err != nil {
		return fmt.Errorf("%s: %s: %s does not load against that seal; nothing was written: %w",
			deliverySealToolName, s.name, s.journal, err)
	}
	s.info = info
	s.accepted = read.accepted
	recovered := s.recovered()
	fmt.Fprintf(o.Out, "  %s journal %s: loads, %d entries, %d bytes, chain %s\n",
		s.name, s.journal, recovered.Count, recovered.Bytes, recovered.Chain)
	if !read.accepted {
		return nil
	}
	return o.reportAcceptedLines(s, read.position)
}

// anyAcceptedATornSlot reports whether Rule R chose the position of any side in this run.
func anyAcceptedATornSlot(sides []deliverySealSide) bool {
	for i := range sides {
		if sides[i].accepted {
			return true
		}
	}
	return false
}

// readSeal reads one seal file: the dual reader, and Rule R when an operator asked for it and the
// strict reader refused.
func (o DeliverySealOptions) readSeal(s *deliverySealSide) (sealReading, error) {
	image := readDeliverySealImage(s.seal)
	if image == nil {
		// Not a v2 image: today's v1 sidecar, or a file that is neither, which the v1 reader
		// refuses. This is loadDeliverySeal's own branch, taken here so the report can name the
		// format it found.
		position, err := loadDeliveryPosition(s.seal, s.seed)
		if err != nil {
			return sealReading{}, fmt.Errorf("%s: %s: %s does not read as a v1 seal: %w",
				deliverySealToolName, s.name, s.seal, err)
		}
		fmt.Fprintf(o.Out, "  %s seal %s: v1, %d entries, %d bytes\n",
			s.name, s.seal, position.Count, position.Bytes)
		return sealReading{position: position}, nil
	}

	effective, older, err := selectSeal(image, s.domain, s.seed)
	if err == nil {
		fmt.Fprintf(o.Out, "  %s seal %s: v2, seq %d, %d entries, %d bytes\n",
			s.name, s.seal, effective.Seq, effective.Count, effective.Bytes)
		return sealReading{position: sealedPosition(effective), older: older}, nil
	}
	if !o.AcceptTornSlot {
		return sealReading{}, fmt.Errorf("%s: %s: %s is a v2 seal the strict reader refuses, and it "+
			"is preserved as it is. --accept-torn-slot accepts one valid slot beside one torn slot, "+
			"with its confirmation: %w", deliverySealToolName, s.name, s.seal, err)
	}
	valid, ruleErr := sealRuleR(image, s.domain, s.seed)
	if ruleErr != nil {
		return sealReading{}, fmt.Errorf("%s: %s: %s is refused for a reason --accept-torn-slot does "+
			"not cover, and it is preserved as it is: %w", deliverySealToolName, s.name, s.seal, err)
	}
	fmt.Fprintf(o.Out, "  %s seal %s: v2 with one torn slot; accepting seq %d, %d entries, %d bytes\n",
		s.name, s.seal, valid.Seq, valid.Count, valid.Bytes)
	return sealReading{position: sealedPosition(valid), accepted: true}, nil
}

// reportAcceptedLines prints exactly which lines Rule R admitted (design §4.5).
//
// The scan has just proved that every one of them is a complete canonical line of this journal with
// its arrival sequence in place. They are the lines no seal covers, which is precisely what the
// operator is being asked to take on, so they are printed rather than counted.
func (o DeliverySealOptions) reportAcceptedLines(s *deliverySealSide, position deliveryPosition) error {
	lines, err := deliverySealTail(s.journal, position.Bytes)
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "  %s: --accept-torn-slot admitted %d line(s) past the accepted record:\n",
		s.name, len(lines))
	for _, line := range lines {
		fmt.Fprintf(o.Out, "    %s\n", line)
	}
	return nil
}

// syncJournal makes one side's journal durable before its seal is written to name that journal's
// tail: invariant I3's order — the bytes first, the seal after — for the one writer that arrives
// at an already-written tail rather than appending its own.
//
// The position --to v1 writes is the one the scan RECOVERED, and a complete canonical tail past the
// old seal moves that position forward (deviation D5). Such a tail is exactly what a process crash
// between the journal's Write and its Sync leaves (design §3 row 5, the "complete tail" variant):
// visible to every reader, and only in the page cache. writeDeliveryPositionV1 goes through
// paths.WriteAtomic, which makes the SEAL durable by construction — a temp write, its Sync, and a
// rename — so without this step a power loss can leave a seal ahead of the journal it seals, which
// loadFrom then refuses for good (info.Size() < position.Bytes) and a second run of this tool
// cannot repair either. Rule R's path always has that shape, since the record it accepts is behind
// the tail by construction.
//
// openDeliveryJournal syncs the journal for this reason before openSeal, and openAckLocked before
// openAckSeal. This is the same step in the same place, with the same handle: O_WRONLY|O_APPEND,
// through which nothing is ever written. Before the Sync the file must still be the one the scan
// read, at the size the scan ended at — a journal replaced since then is not the journal this seal
// would describe, and that is openDeliveryJournal's check too.
func (o DeliverySealOptions) syncJournal(s *deliverySealSide) error {
	fail := func(err error) error {
		return fmt.Errorf("%s: %s: making %s durable before its seal names its tail: %w",
			deliverySealToolName, s.name, s.journal, err)
	}
	sync := (*os.File).Sync
	if o.syncData != nil {
		sync = o.syncData
	}
	f, err := paths.OpenFile(s.journal, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fail(err)
	}
	err = func() error {
		opened, statErr := f.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(s.info, opened) ||
			opened.Size() != s.recovered().Bytes {
			return deliveryJournalError()
		}
		return sync(f)
	}()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fail(err)
	}
	return nil
}

// convert writes one side's v1 seal for the position its scan recovered (design §4.5, --to v1).
//
// Its caller reads the daemon lock immediately before this call and calls nothing else in between,
// so what this function writes, the run owned at the moment it wrote it (design §2.10).
func (o DeliverySealOptions) convert(s *deliverySealSide) error {
	recovered := s.recovered()
	if err := writeDeliveryPositionV1(s.seal, recovered.Bytes, recovered.Count, recovered.Chain); err != nil {
		return fmt.Errorf("%s: %s: writing the v1 seal at %s: %w",
			deliverySealToolName, s.name, s.seal, err)
	}
	fmt.Fprintf(o.Out, "  %s seal %s: wrote v1 at %d entries, %d bytes (it sealed %d entries, %d bytes)\n",
		s.name, s.seal, recovered.Count, recovered.Bytes, s.position.Count, s.position.Bytes)
	return nil
}

// sealRuleR is Rule R, and this function is the only place it exists (design §2.9, §4.5).
//
// The strict reader refuses an image with an invalid slot, because every crash-reachable image has
// two good slots: a seal is written only after the bytes it seals are durable, and always into the
// slot holding seq-1. An invalid slot is therefore media damage or a foreign write, and refusing it
// preserves the evidence rather than believing half of it.
//
// Rule R accepts such an image by taking the VALID record as the position. What makes that safe
// enough to offer at all is the caller's scan: the journal must load in full from there, so the
// accepted record seals a real prefix of it and every line past that prefix is a complete canonical
// line. What it costs is what §2.9 says it costs — the state it accepts is also the state that "rot
// of the newest slot plus a line-aligned truncation inside the last batch" produces, in which
// identities the daemon had already released are gone from the tail and no reader can tell. That is
// why it is an operator's decision, taken with consent, against a stopped daemon, and never a
// reader's.
//
// What Rule R does NOT accept is the whole of what makes it offerable, so it is spelled out here
// and pinned by TestDeliverySealRuleR_AcceptsOnlyOneValidSlotBesideOneTornSlot:
//
//   - Two valid slots, whatever the strict reader made of them: a seq gap, or an older record that
//     seals no less. Both slots survive, so nothing about this image is a torn write, and the one
//     of them Rule R would have to prefer is a guess.
//   - Anything beside an EMPTY slot, at any seq. An empty slot is not damage: it is the exact
//     bytes deliverySealEmpty, which is what a fresh or converted file carries and what a foreign
//     write or a stale restore can leave beside a much later seq. That is selectSeal's one
//     documented rollback window, and widening Rule R over it would turn a window into a door.
//   - Two invalid slots, and two empty ones: there is no valid record to take.
//   - The images the strict reader ACCEPTS. Those never reach here, and they are refused anyway,
//     so that this function answers for itself rather than for its caller's control flow.
//
// A slot holding a record with the wrong parity, a sum for the other slot, or a sum for the other
// journal is INVALID, not valid — classifySlot decides validity with sealAdmissible and the sum —
// so such an image is one valid slot beside one torn slot and Rule R accepts it with consent. That
// is the rule's own domain: media damage and foreign writes are exactly what it exists to let an
// operator take responsibility for.
//
// image must be a v2 image (isDeliverySealImage), which readDeliverySealImage has already
// established for every caller here. The check is repeated anyway, as defense in depth: the slot
// regions are taken by offset, so a unit that trusted that promise would index past a short slice
// rather than refuse.
func sealRuleR(image []byte, domain string, seed core.Hash) (sealRecord, error) {
	if !isDeliverySealImage(image) {
		return sealRecord{}, deliveryJournalError()
	}
	a, aState := classifySlot(sealSlotA.region(image), sealSlotA, domain, seed)
	b, bState := classifySlot(sealSlotB.region(image), sealSlotB, domain, seed)
	switch {
	case aState == sealSlotValid && bState == sealSlotInvalid:
		return a, nil
	case bState == sealSlotValid && aState == sealSlotInvalid:
		return b, nil
	}
	return sealRecord{}, deliveryJournalError()
}

// deliverySealTail is the journal's lines past from: the lines Rule R admitted.
//
// It is called only after the scan has succeeded, so from is a line boundary, the file ends with a
// terminator, and every line between them is canonical. The read goes through paths.ReadFileShared
// for the reason every other reader of a file a daemon writes does.
func deliverySealTail(p string, from int64) ([][]byte, error) {
	b, err := paths.ReadFileShared(p)
	if err != nil || int64(len(b)) < from {
		return nil, fmt.Errorf("%s: re-reading %s to report the lines accepted: %w",
			deliverySealToolName, p, deliveryJournalError())
	}
	tail := b[from:]
	if len(tail) == 0 {
		return nil, nil
	}
	return bytes.Split(bytes.TrimSuffix(tail, []byte("\n")), []byte("\n")), nil
}
