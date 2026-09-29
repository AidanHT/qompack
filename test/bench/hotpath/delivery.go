package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// counterHotpathSampleInvalidName mirrors internal/daemon/handlers.go's own unexported
// counterHotpathSampleInvalid constant ("hotpath_sample_invalid"): the count of hot-path requests
// the daemon RECEIVED but refused to time, because validHotPathTS judged their wire timestamp
// unusable. Those requests reach ing.Accept (so they are in l0_ingest) but never reach
// hook_controlled, which is why the B-A row's own population can be shorter than the B-B row's
// even on a run where every single request was delivered. Same indirection-by-comment idiom
// measure.go's histHookControlledObservedName already uses — the daemon does not export it.
const counterHotpathSampleInvalidName = "hotpath_sample_invalid"

// spoolScanInitialBytes sizes the read buffer the census reads each spool file and the store's
// tool_use index with. It is only the starting size: a line is read whole whatever its length, so a
// legitimately large warm-up payload line is never misreported as unreadable.
const spoolScanInitialBytes = 64 << 10 // 64 KiB

// storeToolUseIndexName mirrors internal/store/open.go's own unexported toolUseFile constant
// ("tool_use.jsonl"): the store's append-only tool_use index under paths.Of(root).Index, which gets
// one record, carrying the tool use's id ("id") and session ("s"), for every tool use the daemon
// captures — live or replayed from a spool. Same indirection-by-comment idiom as
// counterHotpathSampleInvalidName. TestCensusDeliveries_FindsWhatTheRealStoreRecorded pins it against
// a record the real store writes, so a rename or a change of the line's keys fails that test instead
// of hiding every capture from the census (which a run would report, loudly, as LOST).
const storeToolUseIndexName = "tool_use.jsonl"

// lostIdentitiesShown bounds how many LOST identities reconcileDelivery names in its error; the
// count it reports is always the full one.
const lostIdentitiesShown = 8

// deliveryLedger is this harness's account of where every hot-path request it SENT actually
// ended up. It exists to separate two outcomes the earlier guard collapsed into one number:
//
//   - DEFERRED: internal/ipc/client.go's Send could not reach the daemon inside its deadlines
//     (a connect timeout, a write failure, a lost ACK, a NAK) and appended the request to
//     .qompack/spool instead, returning Response{OK:false} and letting the hook exit fast. That is
//     documented, intended §8.1/§12.2 behaviour — "degrade rather than block" — and the event is
//     durable on disk: the daemon replays it from there. Nothing is lost; the LIVE sample set is
//     simply smaller than planned.
//   - LOST: the request is nowhere it can durably be — not in a client spool, not in the daemon's
//     WAL, not in the store. Either the spool append itself was refused (internal/ipc/client.go's
//     appendToSpool drop path: no spool, an oversize line, a read-only or full spool directory) or
//     something the harness does not model swallowed it. The derived numbers cannot be trusted at
//     all, and the run must fail.
//
// Both are shortfalls against Sent, and the earlier guard failed on either. It has to fail on the
// second — but failing on the first turns a documented product behaviour into a red build, which
// is what run 32296920486 hit on macos-latest (2063 of 2064).
//
// Deferral is NOT free, though, and this ledger is only half the fix: see tailAdjustedP99
// (report.go) for why the deferred requests must be added back into the gated population as
// over-budget samples rather than quietly dropped from it.
type deliveryLedger struct {
	// Sent is how many hot-path (observe.tool) requests this harness issued, and it carries three
	// populations, not two: the B-A/B-D spawn loop's own --iterations, the warm-up's hot tranche
	// when --warm-daemon ran, and measureAckRTT's tranche — its timed samples plus the ackRTTWarmups
	// discarded in front of them (ackRTTTrancheSends, added at runHarness's own call site).
	//
	// The third is deliberately outside expectedHotPathSends, whose contract is the GATED window;
	// it is inside this field because every request in it reaches ing.Accept and is counted by
	// l0_ingest, and a Sent that did not count it would make the daemon's delivered count exceed
	// what the harness admits to having sent.
	Sent int64
	// Delivered is the daemon's own l0_ingest observation count — its ground truth for "arrived
	// live and reached ing.Accept".
	Delivered int64
	// Deferred is how many of the Sent requests did not arrive live: Sent - Delivered. It is set
	// only once reconcileDelivery has found every one of the Sent requests by its own identity
	// somewhere it is durable, so each deferred request is still in a client spool or has already
	// been replayed from one into the store.
	Deferred int64
	// Lost is how many Sent requests are nowhere at all. A reconciled ledger always carries zero
	// here, because reconcileDelivery refuses to return one that does not.
	Lost int64
}

// Undelivered is how many of the Sent requests are absent from the daemon's LIVE hot-path
// population. After reconcileDelivery has returned successfully this equals Deferred (Lost is
// zero by construction) — it is spelled as its own method because that is the quantity the
// percentile accounting needs: the number of samples missing from the histogram, whatever the
// reason they are missing.
func (l deliveryLedger) Undelivered() int64 { return l.Sent - l.Delivered }

// expectedHotPathSends is the exact population this harness plans to put through the daemon's
// hot-path histograms: --iterations real hook spawns from the B-A/B-D loop, plus warmHotTranche
// in-process observe.tool requests when --warm-daemon ran. The rest of warmIterations is
// admin.ping traffic (measure.go's warmDaemon), which is not req.Op.HotPath() and never touches
// l0_ingest or hook_controlled at all (FIX ROUND 2, N-1).
func expectedHotPathSends(iterations int, warmDaemonRan bool) int64 {
	want := int64(iterations)
	if warmDaemonRan {
		want += warmHotTranche
	}
	return want
}

// ackRTTTrancheSends is how many hot-path requests measureAckRTT puts through the daemon to return
// samples timed round trips: the timed samples themselves plus the ackRTTWarmups it discards in
// front of them.
//
// Every one of them reaches ing.Accept and is counted by l0_ingest, so the ledger's Sent must count
// them too. Counting only the timed samples would make the daemon's own delivered count exceed what
// this harness admits to having sent, and reconcileDelivery refuses that population outright ("some
// OTHER client is feeding the daemon this run measures") — a discarded warm-up is still a delivery.
//
// It is deliberately NOT folded into expectedHotPathSends: that function's contract is the gated
// window — iterations plus the warm-up's own hot tranche — and gatedLedger scopes the missing-sample
// accounting to it. This tranche is sent after the snapshot that window is read from.
func ackRTTTrancheSends(samples int) int64 { return int64(len(ackRTTTrancheSeqs(samples))) }

// harnessHotPathSessions is the set of session ids this harness stamps onto the hot-path requests
// it sends — baSessionID for every spawned `qompack observe tool` (the hook copies Event.SessionID
// onto the request, internal/cli/hookclient.go), warmSessionID for the warm-up's own hot tranche,
// and ackRTTSessionID for the in-process tranche the hook_ack_rtt row times (measureAckRTT). The
// first two are the population expectedHotPathSends counts; the third is added to it at
// runHarness's own call site, since it is no part of that function's pinned contract.
//
// Filtering the census by this set is what keeps a spool line, WAL line or store record that
// belongs to something else (the resident project's own history, a concurrent client, the
// harness's own admin.ping and checkpoint probes) out of this run's accounting.
func harnessHotPathSessions() map[core.SessionID]bool {
	return map[core.SessionID]bool{baSessionID: true, warmSessionID: true, ackRTTSessionID: true}
}

// deliveryIdentity names one hot-path request this harness sent: the session it stamped and the
// tool_use_id its event carries. No two requests of one run share an identity (baToolUseID,
// warmToolUseID and ackRTTToolUseID, payload.go), so an identity found anywhere is exactly one
// request found, and a duplicate copy of a request — the daemon accepted it AND the client spooled
// it after a lost ACK — is still one request, never two.
type deliveryIdentity struct {
	Session core.SessionID
	ToolUse core.ToolUseID
}

func (id deliveryIdentity) String() string { return string(id.Session) + "/" + string(id.ToolUse) }

// sentIdentities is the identity of every hot-path request this harness sends in a run, in the
// order it sends them: the warm-up's hot tranche when --warm-daemon ran, the B-A/B-D spawn loop's
// iterations, and the hook_ack_rtt tranche of ackRTTSamples timed samples with its discarded
// warm-ups. It has exactly expectedHotPathSends(iterations, warmDaemonRan) +
// ackRTTTrancheSends(ackRTTSamples) entries, and every one of them is built by the same function
// the request's payload is (TestSentIdentities_AreExactlyTheRequestsTheHarnessSends).
func sentIdentities(iterations int, warmDaemonRan bool, ackRTTSamples int) []deliveryIdentity {
	ids := gatedIdentities(iterations, warmDaemonRan)
	for _, seq := range ackRTTTrancheSeqs(ackRTTSamples) {
		ids = append(ids, deliveryIdentity{Session: ackRTTSessionID, ToolUse: ackRTTToolUseID(seq)})
	}
	return ids
}

// gatedIdentities is expectedHotPathSends' population by identity: the warm-up's hot tranche when
// --warm-daemon ran, then the B-A/B-D spawn loop's iterations.
func gatedIdentities(iterations int, warmDaemonRan bool) []deliveryIdentity {
	ids := make([]deliveryIdentity, 0, expectedHotPathSends(iterations, warmDaemonRan))
	if warmDaemonRan {
		for i := 0; i < warmHotTranche; i++ {
			ids = append(ids, deliveryIdentity{Session: warmSessionID, ToolUse: warmToolUseID(i)})
		}
	}
	for seq := 0; seq < iterations; seq++ {
		ids = append(ids, deliveryIdentity{Session: baSessionID, ToolUse: baToolUseID(seq)})
	}
	return ids
}

// deliveryCensus is one read of every place a hot-path request this harness sent can durably be:
// the project's client spools, the daemon's WAL segments beside them, and the store's tool_use
// index.
type deliveryCensus struct {
	// Found holds the identity of every request of one of this harness's own sessions that the
	// census found in at least one of those places.
	Found map[deliveryIdentity]bool
	// Unreadable is the number of lines the census could not classify: a spool or WAL line that
	// would not decode, one of this harness's own observe.tool lines that names no tool use, or a
	// tool_use index line that would not parse. A line the census cannot read is a request it cannot
	// place, so reconcileDelivery treats any of them as a reconciliation failure rather than
	// guessing.
	Unreadable int64
	// ClientFiles and WALSegments are how many of each were read, for the diagnostic message only.
	ClientFiles, WALSegments int
}

// clientSpoolNameShape derives the base-name prefix and extension internal/ipc gives a CLIENT
// spool file from a real ipc.SpoolWriter's own Path() — "client-" and ".ndjson" today
// (internal/ipc/spool.go's newSpool builds the name as prefix + os.Getpid() + ext), neither of
// which that package exports.
//
// The census reads the daemon's WAL segments (wal-*.ndjson, in the SAME directory) as well, and
// counts what it finds in either family the same way, but it still tells them apart: a client
// spool is read to its last byte, while a WAL segment's unterminated tail is a line the ingest is
// still writing (censusSpoolFile). Identifying client files POSITIVELY keeps that distinction from
// resting on a prefix this program would have to spell itself, and a rename that broke it fails
// the other way: the file is read as a WAL segment and its torn tail, if it had one, is skipped.
func clientSpoolNameShape(ownSpoolPath string) (prefix, ext string, err error) {
	base := filepath.Base(ownSpoolPath)
	pid := strconv.Itoa(os.Getpid())
	i := strings.LastIndex(base, pid)
	if i <= 0 {
		return "", "", fmt.Errorf(
			"hotpath: cannot derive the client spool name shape: this process's own spool path %q does not embed its pid %s the way internal/ipc/spool.go builds it",
			ownSpoolPath, pid)
	}
	prefix, ext = base[:i], base[i+len(pid):]
	if prefix == "" || ext == "" {
		return "", "", fmt.Errorf(
			"hotpath: cannot derive the client spool name shape from %q (prefix=%q ext=%q)", base, prefix, ext)
	}
	return prefix, ext, nil
}

// censusDeliveries finds, by identity, every request of this harness's own sessions that is
// durably somewhere in projectRoot: a line in a client spool (deferred, not yet replayed), a line
// in one of the daemon's WAL segments (accepted live, not yet published), or a record in the
// store's tool_use index (published, whether it arrived live or was replayed from a spool).
//
// It has to look in all three, and in that order, because the daemon moves a request between them
// while the census runs. Since the V6 close-out's C1.13 the daemon's client-spool watcher
// (internal/daemon/spool_watch.go) replays a hook's client spool while the session is still
// active, and a replayed line never reaches l0_ingest (drainDispatch routes it straight to
// runIngested) and its file is removed once it is consumed. A census of spool lines alone
// therefore misses every deferral the watcher has already replayed, and counted against
// l0_ingest those requests are in neither place: w9's Phase 3 run reported 18 "LOST", and a forced
// reproduction reported 20 LOST while all 26 requests the watcher had replayed were in the store
// (plans/sdd/V6-closeout/w10-lostev).
//
// The order is what makes one pass enough. The daemon removes a file from the spool tier — a
// client spool, a WAL segment — only once the drain has consumed every line in it
// (internal/daemon/drain.go's removeCompletedFile), and it consumes one of this harness's lines only
// after publishing it, its capture recorded in the tool_use index, or after finding its first copy
// already published (a duplicate). So a request that is gone from the spool tier by the time its
// file is read is already in the index, and the index, read after every spool and WAL file, still
// has it: the store never removes a record. Reading the index first would open exactly the window
// the watcher fell into. (A line the drain retires with no capture at all — a policy denial, a line
// that failed admission — leaves no record anywhere, and the census rightly reports it LOST.)
//
// A file that disappears between the directory listing and the open is therefore not an error;
// neither is an absent spool directory or an absent index (nothing spooled, nothing captured).
func censusDeliveries(projectRoot, ownSpoolPath string, sessions map[core.SessionID]bool) (deliveryCensus, error) {
	return censusDeliveriesAround(projectRoot, ownSpoolPath, sessions, nil)
}

// censusDeliveriesAround is censusDeliveries with afterSpool run between the spool-tier reads and
// the store read, the one point the daemon's concurrent drain can matter. Tests use it to move a
// request from a spool into the store exactly there
// (TestCensusDeliveries_AReplayDuringTheCensusIsStillFound); production passes nil.
func censusDeliveriesAround(projectRoot, ownSpoolPath string, sessions map[core.SessionID]bool,
	afterSpool func(),
) (deliveryCensus, error) {
	prefix, ext, err := clientSpoolNameShape(ownSpoolPath)
	if err != nil {
		return deliveryCensus{}, err
	}
	spoolDir := paths.Of(projectRoot).Spool
	files, err := ipc.SpoolFiles(spoolDir)
	if err != nil {
		return deliveryCensus{}, fmt.Errorf("hotpath: reading the spool directory %s: %w", spoolDir, err)
	}

	c := deliveryCensus{Found: map[deliveryIdentity]bool{}}
	for _, p := range files {
		base := filepath.Base(p)
		// ipc.SpoolFiles lists exactly two families: the hooks' client spools and the daemon's WAL.
		client := strings.HasPrefix(base, prefix) && strings.HasSuffix(base, ext)
		if client {
			c.ClientFiles++
		} else {
			c.WALSegments++
		}
		if err := censusSpoolFile(p, client, sessions, &c); err != nil {
			return deliveryCensus{}, err
		}
	}
	if afterSpool != nil {
		afterSpool()
	}
	if err := censusToolUseIndex(filepath.Join(paths.Of(projectRoot).Index, storeToolUseIndexName), sessions, &c); err != nil {
		return deliveryCensus{}, err
	}
	return c, nil
}

// censusSpoolFile folds one client spool (client true) or WAL segment into c.
//
// A client spool is read to its last byte: the hook that wrote it has exited, so a line without its
// newline is a torn write, and it is counted Unreadable like any line that will not decode. A WAL
// segment's unterminated tail is a line the ingest is appending right now, and it is left alone: a
// request the ingest has not finished writing was not acknowledged to its client, which spooled it.
func censusSpoolFile(path string, client bool, sessions map[core.SessionID]bool, c *deliveryCensus) error {
	return readLines(path, func(line []byte, complete bool) {
		if !complete && !client {
			return
		}
		req, derr := ipc.DecodeRequest(line)
		if derr != nil {
			c.Unreadable++
			return
		}
		if req.Op != ipc.OpObserveTool || !sessions[req.Session] {
			return // not this run's hot-path population: an admin probe, a checkpoint, another session
		}
		if req.Event == nil || req.Event.ToolUseID == "" {
			c.Unreadable++ // this harness's own session, and nothing to match it to a sent request by
			return
		}
		c.Found[deliveryIdentity{Session: req.Session, ToolUse: req.Event.ToolUseID}] = true
	})
}

// censusToolUseIndex folds the store's tool_use index into c: every record ("op" absent) whose
// session is one of this harness's own. A "supersede" mutation names a record rather than being one,
// and is skipped. An unterminated tail is a record the store is appending right now, whose request
// was still in the spool tier when that was read (censusDeliveries), and is skipped too.
func censusToolUseIndex(path string, sessions map[core.SessionID]bool, c *deliveryCensus) error {
	return readLines(path, func(line []byte, complete bool) {
		if !complete {
			return
		}
		var rec struct {
			Op      string         `json:"op"`
			ID      core.ToolUseID `json:"id"`
			Session core.SessionID `json:"s"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			c.Unreadable++
			return
		}
		if rec.Op != "" || !sessions[rec.Session] || rec.ID == "" {
			return
		}
		c.Found[deliveryIdentity{Session: rec.Session, ToolUse: rec.ID}] = true
	})
}

// readLines calls fn for every non-blank line of the file at path, trimmed, with complete false
// only for a final line that has no newline yet. A missing file has no lines.
func readLines(path string, fn func(line []byte, complete bool)) error {
	f, err := os.Open(paths.Long(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // drained and removed between the listing and this open, or never written
		}
		return fmt.Errorf("hotpath: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(f, spoolScanInitialBytes)
	for {
		raw, rerr := r.ReadBytes('\n')
		if line := bytes.TrimSpace(raw); len(line) > 0 {
			fn(line, rerr == nil)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return fmt.Errorf("hotpath: reading %s: %w", path, rerr)
		}
	}
}

// refuseInheritedIdentities is the census's precondition, checked before this run sends anything:
// no request of this harness's own sessions may already be in the project. The census cannot tell
// a request this run sent from an earlier run's request with the same identity (a --project
// directory reused across runs), and counting the earlier one would hide a loss of this run's. An
// unreadable line is left to the census after the run, which refuses it there.
func refuseInheritedIdentities(projectRoot, ownSpoolPath string) error {
	c, err := censusDeliveries(projectRoot, ownSpoolPath, harnessHotPathSessions())
	if err != nil {
		return err
	}
	if len(c.Found) > 0 {
		return fmt.Errorf(
			"hotpath: the project %s already holds %d hot-path request(s) of this harness's own sessions (e.g. %s) before this run sent any — the delivery census could not tell them from this run's own, so it refuses to run over them; use a fresh --project",
			projectRoot, len(c.Found), sortedIdentities(c.Found, 1)[0])
	}
	return nil
}

// sortedIdentities returns up to limit of set's identities, sorted, for a stable error message.
func sortedIdentities(set map[deliveryIdentity]bool, limit int) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id.String())
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// reconcileDelivery is the guard FIX ROUND 1's I-2 check grew into. It still refuses to report a
// Report on partial data — that reason was always sound and is not relaxed here — but it now
// establishes WHICH kind of partial the run actually hit before deciding, and it establishes it
// request by request.
//
// It fails when:
//
//   - a request this harness sent is nowhere (Lost > 0): not in a client spool, not in the
//     daemon's WAL, not in the store's tool_use index. The spool append itself was refused, or
//     something outside this harness's model swallowed it. Nothing on disk can be replayed and no
//     derived number is trustworthy. The error names the lost identities.
//   - the daemon observed MORE hot-path requests than this harness sent: some other client is
//     writing into the same daemon, so the gated population is not the one this run measured.
//   - the census found a request of this harness's own sessions that this run never sent: the same
//     conclusion from the other side.
//   - a line will not decode or parse: an unclassifiable line is not evidence of anything, and
//     guessing which side of the deferred/lost split it belongs on is precisely the mistake this
//     function exists to stop making.
//
// It does not fail when a request the daemon never received live is still in a client spool, or
// has already been replayed from one into the store. Those requests were DEFERRED, not lost:
// §8.1/§12.2's documented degrade-rather-than-block path put them on disk and the daemon replays
// them — while the run is still going, since C1.13 (censusDeliveries says why that forced the
// census to look in the store). A replayed request never appears in l0_ingest (drainDispatch
// bypasses ing.Accept), so the shortfall against l0_ingest is permanent, and Deferred is exactly
// Sent - Delivered.
//
// The accounting used to be a count — spooled lines against the shortfall — and a count cannot do
// either half of this. It could not see a replayed request at all, and it could not tell a spooled
// DUPLICATE of a delivered request from a deferral, so one duplicate could stand in for one loss.
// An identity is found or it is not.
//
// Passing this check is NOT the end of the accounting. A deferred request is a sample missing
// from the upper tail of the gated population, and report.go's tailAdjustedP99 puts it back in.
func reconcileDelivery(sent []deliveryIdentity, delivered int64, census deliveryCensus) (deliveryLedger, error) {
	if census.Unreadable > 0 {
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: delivery reconciliation failed: %d line(s) across %d client spool file(s), %d WAL segment(s) and the store's tool_use index would not decode as a request or a record — the harness cannot tell whether they are this run's hot-path events or something else, so it refuses to classify this run's shortfall at all rather than guess",
			census.Unreadable, census.ClientFiles, census.WALSegments)
	}
	n := int64(len(sent))
	if delivered > n {
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: delivery reconciliation failed: the daemon's own l0_ingest histogram observed %d requests but this harness only sent %d — some OTHER client is feeding the daemon this run measures, so the gated population is not the one that was measured; refusing to report a Report over a population the harness does not control",
			delivered, n)
	}

	want := make(map[deliveryIdentity]bool, len(sent))
	var lost []string
	for _, id := range sent {
		want[id] = true
		if !census.Found[id] {
			lost = append(lost, id.String())
		}
	}
	foreign := map[deliveryIdentity]bool{}
	for id := range census.Found {
		if !want[id] {
			foreign[id] = true
		}
	}
	if len(foreign) > 0 {
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: delivery reconciliation failed: %d hot-path request(s) of this harness's own sessions are in the project that this run never sent (e.g. %v) — some OTHER client is sending as this harness, so the population is not the one that was measured",
			len(foreign), sortedIdentities(foreign, lostIdentitiesShown))
	}
	if len(lost) > 0 {
		shown := lost[:min(len(lost), lostIdentitiesShown)]
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: delivery integrity check failed: of the %d hot-path requests this harness sent, the daemon's own l0_ingest histogram observed %d, and %d are LOST — in no client spool, no daemon WAL segment and not in the store's tool_use index (first: %v), so the spool append itself was refused (see internal/ipc/client.go's appendToSpool drop path) or something else swallowed them. A lost event biases every derived number and cannot be replayed; refusing to report a Report rather than silently passing a gate on partial data",
			n, delivered, len(lost), shown)
	}
	return deliveryLedger{Sent: n, Delivered: delivered, Deferred: n - delivered}, nil
}

// gatedLedger scopes a reconciled ledger to the population the daemon's own GATED rows are built
// from, which is not the whole run's.
//
// runHarness reads the status op twice: once before the hook_ack_rtt tranche, for the percentiles
// the B-A and B-B rows publish, and once after it, for the delivered count the ledger reconciles.
// The tranche is real hot-path traffic sent between the two, so the full ledger above counts it and
// the gated histograms do not contain it, and the missing-sample accounting has to be done against
// the window the gated snapshot actually covers: sent is what this harness had sent by then
// (expectedHotPathSends' own pinned contract), delivered is that snapshot's own l0_ingest count.
//
// The deferrals come from the full ledger, because the census finds each request where it durably
// is, not whether it arrived live, so it cannot attribute a deferral to a tranche. It does not need
// to. Counting a deferral from outside this window against it can only ever make the accounting
// MORE conservative — a missing sample counted back in as over-budget that was never in the
// population — never less,
// and a genuine LOSS is caught by reconcileDelivery over the whole run before this is reached. The
// Lost check below is kept anyway: it costs nothing and it refuses rather than guesses.
func gatedLedger(total deliveryLedger, sent, delivered int64) (deliveryLedger, error) {
	if delivered > sent {
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: the daemon's own l0_ingest histogram had observed %d requests before the %s tranche was sent, but this harness had only sent %d by then — some OTHER client is feeding the daemon this run measures, so the gated population is not the one that was measured",
			delivered, budgetIDHookAckRTT, sent)
	}

	l := deliveryLedger{Sent: sent, Delivered: delivered, Deferred: total.Deferred}
	if shortfall := l.Undelivered(); l.Deferred > shortfall {
		l.Deferred = shortfall
	}
	l.Lost = l.Undelivered() - l.Deferred

	if l.Lost > 0 {
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: of the %d hot-path requests this harness sent before the %s tranche, the daemon's own l0_ingest histogram observed %d and only %d are accounted for by a deferred request line in the client spool — %d are LOST; refusing to report a Report over a gated population the harness cannot account for",
			sent, budgetIDHookAckRTT, delivered, l.Deferred, l.Lost)
	}
	return l, nil
}

// hookControlledShortfall reports how many of the ledger's Sent requests are absent from the
// daemon's hook_controlled population (the series controller ruling #29 made the gated B-A row),
// and refuses to return a number it cannot explain.
//
// hook_controlled and l0_ingest are written at two different points of the same request's life —
// dispatchOp's recordHotPathSample and acceptHotPathEvent's ing.Accept respectively
// (internal/daemon/handlers.go) — and exactly one thing separates them: validHotPathTS, which
// drops a sample whose wire timestamp is absent, implausibly far ahead of the daemon's own clock,
// or more than hotPathSampleMaxAge (10s) stale, counting it as hotpath_sample_invalid. That last
// case is a SLOW sample being discarded, so it truncates the same upper tail a deferral does, and
// it must be accounted for the same way rather than left to silently shrink B-A's population.
func hookControlledShortfall(l deliveryLedger, observed int64, counters map[string]int64) (int64, error) {
	if observed > l.Sent {
		return 0, fmt.Errorf(
			"hotpath: the daemon's hook_controlled histogram observed %d samples but this harness only sent %d hot-path requests — the gated B-A population is not this run's own",
			observed, l.Sent)
	}
	missing := l.Sent - observed
	invalid := counters[counterHotpathSampleInvalidName]
	if explained := l.Deferred + invalid; missing > explained {
		return 0, fmt.Errorf(
			"hotpath: the daemon's hook_controlled histogram observed %d of the %d requests this harness sent, a shortfall of %d, but only %d are explained (%d deferred to the spool + %d counted as %s) — %d samples went missing from the GATED B-A population with no accounting, which would bias exactly the percentile the gate reads; refusing to report a Report rather than silently passing a gate on partial data",
			observed, l.Sent, missing, explained, l.Deferred, invalid, counterHotpathSampleInvalidName, missing-explained)
	}
	return missing, nil
}
