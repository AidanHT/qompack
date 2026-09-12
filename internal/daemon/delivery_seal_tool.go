package daemon

import (
	"bytes"
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

// run is the tool with the daemon lock held.
func (o DeliverySealOptions) run(lock *Lock) error {
	state := paths.Of(o.ProjectRoot).State
	j := newDeliveryJournal(lock, filepath.Join(state, deliveryLeaseFile))
	j.ackPath = filepath.Join(state, deliveryAckFile)

	fmt.Fprintf(o.Out, "%s: %s\n", deliverySealToolName, o.ProjectRoot)

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
			return halfConverted(err, sides[:i], "stopping that daemon and rerunning converts what is left")
		}
		if err := o.convert(&sides[i]); err != nil {
			return halfConverted(err, sides[:i], "rerunning the same command converts only what is left")
		}
	}
	return nil
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
// next is what to do about the rest, and the caller chooses it because the two failures differ in
// exactly that: a failed write leaves the project to this operator, while a run dispossessed
// mid-conversion leaves it to the daemon that took it, which refuses the same rerun with
// ErrLockHeld until it is stopped. done is what was converted before the failure; an empty done is
// the first seal's own failure, which leaves nothing of the kind to say.
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
func (o DeliverySealOptions) holdsLock(lock *Lock, doing string) error {
	if err := lock.Heartbeat(); err != nil {
		return fmt.Errorf("%s: %s: this process no longer owns the daemon lock in %s; a daemon that "+
			"started meanwhile owns the journals now, and this step was refused rather than written: %w",
			deliverySealToolName, doing, o.ProjectRoot, err)
	}
	return nil
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
	recovered := s.recovered()
	fmt.Fprintf(o.Out, "  %s journal %s: loads, %d entries, %d bytes, chain %s\n",
		s.name, s.journal, recovered.Count, recovered.Bytes, recovered.Chain)
	if !read.accepted {
		return nil
	}
	return o.reportAcceptedLines(s, read.position)
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
