package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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

// spoolScanInitialBytes sizes the read buffer censusClientSpool scans each spool file with. The
// cap is ipc.MaxLineBytes (the protocol's own frame limit), so a legitimately large warm-up
// payload line is read whole rather than being misreported as unreadable.
const spoolScanInitialBytes = 64 << 10 // 64 KiB

// deliveryLedger is this harness's account of where every hot-path request it SENT actually
// ended up. It exists to separate two outcomes the earlier guard collapsed into one number:
//
//   - DEFERRED: internal/ipc/client.go's Send could not reach the daemon inside its deadlines
//     (a connect timeout, a write failure, a lost ACK, a NAK) and appended the request to
//     .qompack/spool instead, returning Response{OK:false} and letting the hook exit fast. That is
//     documented, intended §8.1/§12.2 behaviour — "degrade rather than block" — and the event is
//     durable on disk: the daemon replays it on its next drain. Nothing is lost; the LIVE sample
//     set is simply smaller than planned.
//   - LOST: the request is in neither place. Either the spool append itself was refused
//     (internal/ipc/client.go's appendToSpool drop path: no spool, an oversize line, a read-only
//     or full spool directory) or something the harness does not model swallowed it. The derived
//     numbers cannot be trusted at all, and the run must fail.
//
// Both are shortfalls against Sent, and the earlier guard failed on either. It has to fail on the
// second — but failing on the first turns a documented product behaviour into a red build, which
// is what run 32296920486 hit on macos-latest (2063 of 2064).
//
// Deferral is NOT free, though, and this ledger is only half the fix: see tailAdjustedP99
// (report.go) for why the deferred requests must be added back into the gated population as
// over-budget samples rather than quietly dropped from it.
type deliveryLedger struct {
	// Sent is how many hot-path (observe.tool) requests this harness issued: the B-A/B-D spawn
	// loop's own --iterations, plus the warm-up's hot tranche when --warm-daemon ran.
	Sent int64
	// Delivered is the daemon's own l0_ingest observation count — its ground truth for "arrived
	// live and reached ing.Accept".
	Delivered int64
	// Deferred is how many of the Sent requests are accounted for by a real, decodable request
	// line sitting in a CLIENT spool file (.qompack/spool/client-<pid>.ndjson) instead.
	Deferred int64
	// Lost is Sent - Delivered - Deferred: requests in neither place. A reconciled ledger always
	// carries zero here, because reconcileDelivery refuses to return one that does not.
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

// harnessHotPathSessions is the set of session ids this harness stamps onto the hot-path requests
// counted by expectedHotPathSends — baSessionID for every spawned `qompack observe tool` (the
// hook copies Event.SessionID onto the request, internal/cli/hookclient.go) and warmSessionID for
// the warm-up's own hot tranche. Filtering the spool census by this set is what keeps a spool
// entry that belongs to something else (a pre-existing project spool, a concurrent client, the
// harness's own admin.ping probes) from being miscounted as one of THIS run's deferrals — which
// would inflate Deferred and could mask a real loss.
func harnessHotPathSessions() map[core.SessionID]bool {
	return map[core.SessionID]bool{baSessionID: true, warmSessionID: true}
}

// spoolCensus is one read of the project's client-side spool tier.
type spoolCensus struct {
	// Deferred is the number of decodable observe.tool lines belonging to one of this harness's
	// own hot-path sessions.
	Deferred int64
	// Unreadable is the number of non-empty lines that would not decode. A spool line this
	// harness cannot read is a line it cannot classify, so reconcileDelivery treats any of them
	// as a reconciliation failure rather than guessing.
	Unreadable int64
	// Files is how many client spool files were scanned, for the diagnostic message only.
	Files int
}

// clientSpoolNameShape derives the base-name prefix and extension internal/ipc gives a CLIENT
// spool file from a real ipc.SpoolWriter's own Path() — "client-" and ".ndjson" today
// (internal/ipc/spool.go's newSpool builds the name as prefix + os.Getpid() + ext), neither of
// which that package exports.
//
// Deriving it matters for a reason beyond taste. The daemon's own WAL segments (wal-*.ndjson)
// live in the SAME directory and hold exactly the requests that WERE delivered, so a census that
// identified client files by excluding the wal- prefix would, if internal/ipc ever renamed either
// family, silently start counting delivered requests as deferred — inflating Deferred and MASKING
// a real loss. Identifying client files POSITIVELY fails the other way: a rename makes the census
// find nothing, the shortfall goes unexplained, and the run fails loudly. That is the safe
// direction for a guard whose whole job is to refuse to report on partial data.
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

// censusClientSpool counts this run's own deferred hot-path requests by reading the project's
// spool directory: every CLIENT spool file (never a daemon WAL segment — see clientSpoolNameShape),
// every decodable line, keeping the ones whose Op is observe.tool and whose Session is one this
// harness itself stamped.
//
// A file that disappears between the directory listing and the open is not an error: the daemon
// deletes a client spool file once it has fully drained it (internal/daemon/drain.go's
// shouldDelete), and losing that race only ever makes this census SMALLER, which surfaces as an
// unexplained shortfall and a loud failure rather than as a silently-passed gate.
func censusClientSpool(spoolDir, ownSpoolPath string, sessions map[core.SessionID]bool) (spoolCensus, error) {
	prefix, ext, err := clientSpoolNameShape(ownSpoolPath)
	if err != nil {
		return spoolCensus{}, err
	}
	files, err := ipc.SpoolFiles(spoolDir)
	if err != nil {
		return spoolCensus{}, fmt.Errorf("hotpath: reading the spool directory %s: %w", spoolDir, err)
	}

	var c spoolCensus
	for _, p := range files {
		base := filepath.Base(p)
		if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, ext) {
			continue // a daemon WAL segment (or anything else), not a client-side spool file
		}
		c.Files++
		if err := censusSpoolFile(p, sessions, &c); err != nil {
			return spoolCensus{}, err
		}
	}
	return c, nil
}

// censusSpoolFile folds one client spool file into c.
func censusSpoolFile(path string, sessions map[core.SessionID]bool, c *spoolCensus) error {
	f, err := os.Open(paths.Long(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // drained and removed between the listing and this open
		}
		return fmt.Errorf("hotpath: opening spool file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, spoolScanInitialBytes), ipc.MaxLineBytes+1)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		req, derr := ipc.DecodeRequest(line)
		if derr != nil {
			c.Unreadable++
			continue
		}
		if req.Op == ipc.OpObserveTool && sessions[req.Session] {
			c.Deferred++
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("hotpath: reading spool file %s: %w", path, err)
	}
	return nil
}

// reconcileDelivery is the guard FIX ROUND 1's I-2 check grew into. It still refuses to report a
// Report on partial data — that reason was always sound and is not relaxed here — but it now
// establishes WHICH kind of partial the run actually hit before deciding.
//
// It fails, exactly as before, when:
//
//   - a request is in neither the daemon nor the spool (Lost > 0): the spool append itself was
//     refused, or something outside this harness's model swallowed it. Nothing on disk can be
//     replayed and no derived number is trustworthy.
//   - the daemon observed MORE hot-path requests than this harness sent: some other client is
//     writing into the same daemon, so the gated population is not the one this run measured.
//   - a client spool line will not decode: an unclassifiable line is not evidence of anything,
//     and guessing which side of the deferred/lost split it belongs on is precisely the mistake
//     this function exists to stop making.
//
// It no longer fails when the shortfall is fully accounted for by real, decodable request lines
// sitting in the client spool. Those requests were DEFERRED, not lost: §8.1/§12.2's documented
// degrade-rather-than-block path put them on disk and the daemon replays them on its next drain
// (note that internal/daemon/daemon.go's drainDispatch routes a replayed hot-path line straight
// to runIngested, deliberately bypassing ing.Accept — so a drained request never appears in
// l0_ingest, and this shortfall is permanent, not a race that would settle if the harness waited).
//
// Passing this check is NOT the end of the accounting. A deferred request is a sample missing
// from the upper tail of the gated population, and report.go's tailAdjustedP99 puts it back in.
func reconcileDelivery(sent, delivered int64, census spoolCensus) (deliveryLedger, error) {
	if census.Unreadable > 0 {
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: delivery reconciliation failed: %d line(s) across %d client spool file(s) would not decode as a request — the harness cannot tell whether they are deferred hot-path events or something else, so it refuses to classify this run's shortfall at all rather than guess",
			census.Unreadable, census.Files)
	}
	if delivered > sent {
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: delivery reconciliation failed: the daemon's own l0_ingest histogram observed %d requests but this harness only sent %d — some OTHER client is feeding the daemon this run measures, so the gated population is not the one that was measured; refusing to report a Report over a population the harness does not control",
			delivered, sent)
	}

	l := deliveryLedger{Sent: sent, Delivered: delivered}
	l.Deferred = census.Deferred
	if shortfall := l.Undelivered(); l.Deferred > shortfall {
		// More spooled lines than missing samples: at least one request was BOTH delivered and
		// spooled (internal/ipc/client.go spools on a lost ACK too, after the daemon has already
		// accepted the line — the daemon's own seen-set collapses the duplicate on drain). Those
		// are duplicates, not missing samples, so only the shortfall itself is counted as
		// deferred; the rest costs nothing but disk.
		l.Deferred = shortfall
	}
	l.Lost = l.Undelivered() - l.Deferred

	if l.Lost > 0 {
		return deliveryLedger{}, fmt.Errorf(
			"hotpath: delivery integrity check failed: of the %d hot-path requests this harness sent, the daemon's own l0_ingest histogram observed %d and only %d are accounted for by a deferred request line in the client spool — %d are LOST (in neither place, so the spool append itself was refused: see internal/ipc/client.go's appendToSpool drop path). A lost event biases every derived number and cannot be replayed; refusing to report a Report rather than silently passing a gate on partial data",
			sent, delivered, l.Deferred, l.Lost)
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
