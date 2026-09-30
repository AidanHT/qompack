package daemon

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/store"
)

// Startup publication accounting (V6-RECOVERY-1): the daemon-side half of the production discovery
// internal/store.AuditPublication performs. The store walks the tree; this turns one bounded pass
// into a narrow, non-sensitive report and a single LOUD announcement main can wire into startup.
//
// It is the analogue of sweepCheckpointIntegrity (daemon.go): an ANNOUNCEMENT, not a gate. Nothing
// is repaired, moved or deleted, both are discovery-only reads, and the daemon never refuses to start
// over a gap — a gap is surfaced so an operator or a later fsck --repair can act, not blocked on.
//
// WHERE Run calls it (accountPublicationAtStartup): immediately after d.sweepCheckpointIntegrity in
// Run, on Run's own goroutine — AFTER the startup Drain, so a capture the drain is about to publish
// is not reported as a transient gap, and BEFORE the serve loop. Never from Stop, and never before
// Drain. The store method only reads (a filesystem walk plus in-memory index lookups under a read
// lock) and writes nothing, so it is safe against the daemon's live read-write store; the position
// is about the MEANING of the answer (post-drain, so the numbers are real), not about writer
// safety.
//
// That meaning is pinned by a snapshot (store.PublicationSnapshot) taken at that position, before
// anything is served. The pass gets publicationStartupBound to finish inside the startup; one that
// does not fit is not announced as incomplete but finishes in the background against the same
// snapshot, which leaves every file written after it unclassified, so it still reports the store
// as the startup found it (V6 close-out D49). The candidate 4 live re-run's healthy three-session
// store (70 captures, about 380 objects) needed longer than the bound on Windows, and every start
// logged LOUD "publication accounting incomplete ... scan interrupted" for a scan that was simply
// cut short.
//
// The store the daemon holds may be a test double or a store without this capability; a store that
// cannot be audited yields an unobserved report and no LOUD line, exactly as a non-*FSStore does for
// RefCounter and Quarantiner.

// Counters this file registers. Read them as gap gauges: a non-zero unpublished/unindexed value is a
// V6-RECOVERY-1 gap surfaced at startup, and accounting_incomplete says the pass could not be
// exhaustive (a filesystem error, an unknown schema, or the scan cap).
const (
	publicationStartupBound               = 250 * time.Millisecond
	counterPublicationUnpublishedCaptures = "daemon.publication.unpublished_captures"
	counterPublicationUnindexedObjects    = "daemon.publication.unindexed_object_candidates"
	counterPublicationIncomplete          = "daemon.publication.accounting_incomplete"
	// counterPublicationContinued counts the startup passes that did not fit publicationStartupBound
	// and finished in the background (accountPublicationAtStartup).
	counterPublicationContinued = "daemon.publication.accounting_continued"
)

// PublicationGapReport is the daemon's narrow, non-sensitive summary of one accounting pass. Every
// field is a count, a boolean or a fixed-vocabulary note: no observation id, path, hash or byte of
// content, so main and its callers may surface or log it freely.
//
// Observed distinguishes "audited and found clean" from "could not audit": it is false when the
// daemon's store does not implement the accounting capability (a test double, an alternative store),
// so a zero report is never mistaken for a clean one.
type PublicationGapReport struct {
	Observed bool

	UnpublishedCaptures       int
	LegitimatelyUnpublished   int
	UnindexedObjectCandidates int
	PendingObjects            int
	// LegacyControlCaptures counts the sidecars builds before the V6 close-out published for drained
	// control lines (store.IsControlCaptureOp): known legacy evidence, kept, neither gap nor damage.
	LegacyControlCaptures int

	CapturesScanned int
	ObjectsScanned  int
	// PostSnapshotEntries counts the files a pass against a startup snapshot left unclassified
	// because they were written after it: live work, never a gap.
	PostSnapshotEntries int

	Incomplete bool
	Truncated  bool
	Notes      []string
}

// HasGaps reports whether the pass found a genuine, actionable gap, independent of Incomplete.
func (r PublicationGapReport) HasGaps() bool {
	return r.UnpublishedCaptures > 0 || r.UnindexedObjectCandidates > 0
}

// publicationScanCap is the daemon's explicit bound on a startup accounting pass. It is a named seam
// so a future caller (an admin route, a test) can lower it without hunting for the literal.
func (d *daemon) publicationScanCap() store.PublicationScanCap {
	return store.DefaultPublicationScanCap()
}

// accountPublicationAtStartup is Run's startup accounting. It snapshots the store before anything is
// served, gives the pass publicationStartupBound (d.publicationBound in a test) to finish inside the
// startup, and when the pass does not fit, finishes it in the background against the same snapshot
// rather than announcing a healthy store as incompletely accounted (D49). The background pass runs
// under runCtx, so Stop ends it, and on the goRun group, so Stop joins it.
//
// A store that cannot take a snapshot keeps the old shape: one bounded pass, announced as it ends.
//
// Both halves of the pass step aside for capture work (V6 close-out D51): the pass waits on
// d.capture before each unit of its I/O, so it runs between hook requests and never beside one, and
// a session's I/O does not pay for it. A unit already under way when a request arrives finishes
// first: one directory batch of at most scanDirBatch names, or one sidecar or pending-marker read
// (internal/store, PublicationScanCap.Yield). The pause has no bound and adds no number of its own:
// the pass is an announcement, a pass that never finishes delays only that announcement, and Stop
// ends a paused pass like any other. Before D51 the pass ran unpaced beside the first session; on a
// store ten times the live run's size (3800 objects in 3961 directories, 700 captures) it took
// 15.1 s cold and about 4 s warm on the Windows host, and a PutBytes beside it moved from p99 16 ms
// to 26 ms (plans/sdd/V6-closeout/w15-services/runs/review-pubscan-10x-diagnostic.txt).
func (d *daemon) accountPublicationAtStartup(runCtx context.Context) {
	auditor, ok := d.svc.Store.(store.PublicationAuditor)
	if !ok {
		return // Observed:false — this store exposes no accounting, and nothing is announced
	}
	scanCap := d.publicationScanCap()
	scanCap.Yield = d.capture.wait
	if snapper, ok := d.svc.Store.(store.PublicationSnapshotter); ok {
		if snap, err := snapper.SnapshotPublication(runCtx); err == nil {
			scanCap.Snapshot = &snap
		}
	}
	bound := d.publicationBound
	if bound <= 0 {
		bound = publicationStartupBound
	}
	auditCtx, auditCancel := context.WithTimeout(runCtx, bound)
	rep, err := d.accountPublication(auditCtx, auditor, scanCap)
	auditCancel()
	if scanCap.Snapshot != nil && errors.Is(err, context.DeadlineExceeded) && runCtx.Err() == nil {
		if d.m != nil {
			d.m.Counter(counterPublicationContinued).Add(1)
		}
		d.log.Info("daemon: publication accounting continues after startup",
			"startup_bound_ms", bound.Milliseconds(),
			"captures_scanned", rep.CapturesScanned, "objects_scanned", rep.ObjectsScanned)
		d.goRun(func() {
			full, err := d.accountPublication(runCtx, auditor, scanCap)
			d.announcePublication(full, err != nil && runCtx.Err() != nil)
		})
		return
	}
	d.announcePublication(rep, err != nil && runCtx.Err() != nil)
}

// captureGate counts the capture work in flight in this daemon, so the startup publication pass can
// step aside for it (V6 close-out D51). Capture work is a request dispatchOp is serving, a delivery a
// worker or a drain is applying (runIngested), and the work a request leaves running past its answer
// (startPromptRecording, startReplyWork, launchSessionEnd), each of which enters before its request
// has left, so one request's work holds the gate without a gap. A fire-and-forget delivery's ACK and
// its worker are the one seam: between the route's return and a worker's runIngested the delivery
// sits in the ingest ring, which a worker takes it from at once unless every worker is busy, and a
// busy worker holds the gate. The zero value is ready to use.
type captureGate struct {
	mu     sync.Mutex
	active int
	// idle is closed when active falls to zero, and made anew when it rises from zero.
	idle chan struct{}
	// onPark, when set, is called each time a wait parks. It is a test seam: a test that must see
	// the pass paused waits for it instead of sleeping.
	onPark func()
}

// enter counts one piece of capture work in.
func (g *captureGate) enter() {
	g.mu.Lock()
	if g.active == 0 {
		g.idle = make(chan struct{})
	}
	g.active++
	g.mu.Unlock()
}

// leave counts one piece of capture work out; the last one out releases every wait.
func (g *captureGate) leave() {
	g.mu.Lock()
	g.active--
	if g.active == 0 {
		close(g.idle)
	}
	g.mu.Unlock()
}

// inFlight is the capture work in flight now.
func (g *captureGate) inFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
}

// wait returns once no capture work is in flight, or with ctx's error once ctx has ended. It is the
// publication pass's store.PublicationScanCap.Yield.
func (g *captureGate) wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		if g.active == 0 {
			g.mu.Unlock()
			return ctx.Err()
		}
		idle, park := g.idle, g.onPark
		g.mu.Unlock()
		if park != nil {
			park()
		}
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// AccountPublicationGaps runs one bounded, read-only accounting pass against the daemon's store and
// returns the narrow report. It writes nothing and repairs nothing.
//
// A store that cannot be audited — one that does not implement store.PublicationAuditor — yields
// Observed:false and no counts. A store that could be asked but could not finish (a cancelled
// context at shutdown, or a closed store) yields Observed:true and Incomplete:true, because "I began
// and could not finish" is a lower bound, not a clean result; the underlying error text is not
// carried into the report, which stays strictly non-sensitive.
func (d *daemon) AccountPublicationGaps(ctx context.Context) PublicationGapReport {
	auditor, ok := d.svc.Store.(store.PublicationAuditor)
	if !ok {
		return PublicationGapReport{} // Observed:false — this store exposes no accounting
	}
	rep, _ := d.accountPublication(ctx, auditor, d.publicationScanCap())
	return rep
}

// accountPublication is one pass under scanCap, returning the narrow report and the store's error
// (a context that ended, or a closed store), which the report itself never carries.
func (d *daemon) accountPublication(ctx context.Context, auditor store.PublicationAuditor,
	scanCap store.PublicationScanCap,
) (PublicationGapReport, error) {
	a, err := auditor.AuditPublication(ctx, scanCap)
	rep := PublicationGapReport{
		Observed:                  true,
		UnpublishedCaptures:       a.UnpublishedCaptures,
		LegitimatelyUnpublished:   a.LegitimatelyUnpublished,
		UnindexedObjectCandidates: a.UnindexedObjectCandidates,
		PendingObjects:            a.PendingObjects,
		LegacyControlCaptures:     a.LegacyControlCaptures,
		CapturesScanned:           a.CapturesScanned,
		ObjectsScanned:            a.ObjectsScanned,
		PostSnapshotEntries:       a.PostSnapshotEntries,
		Incomplete:                a.Incomplete,
		Truncated:                 a.Truncated,
		Notes:                     a.Notes,
	}
	if err != nil {
		// A store that answered an error (context cancelled mid-pass at shutdown, or a closed store)
		// made no exhaustive statement. Mark it incomplete without leaking the error text, which can
		// carry a path.
		rep.Incomplete = true
		rep.Notes = appendGenericNote(rep.Notes, "publication accounting did not finish")
	}
	return rep, err
}

// LoudPublicationGaps runs AccountPublicationGaps and, when it finds a gap OR could not complete,
// emits ONE LOUD line naming the counts. It is the startup announcement half — callable by main — and
// returns the report so main may also record or test it.
//
// The LOUD payload is counts and booleans only (and the fixed-vocabulary notes): a generic diagnostic
// that names WHAT is unaccounted for and HOW MANY, never a single observation id, path or hash. A
// clean, complete pass is quiet (a Debug line), so an operator's LOUD.log stays a list of things that
// actually need attention (§12).
func (d *daemon) LoudPublicationGaps(ctx context.Context) PublicationGapReport {
	rep := d.AccountPublicationGaps(ctx)
	d.announcePublication(rep, false)
	return rep
}

// announcePublication is LoudPublicationGaps' announcement half. A found gap is LOUD whether or not
// the pass finished, because a gap found is real. A pass that could not finish is LOUD too, except
// one the daemon's own stop cut short (stopped): that pass was not unable to finish, it was told to
// stop, and it is a Warn in the day log rather than an operator's alarm.
func (d *daemon) announcePublication(rep PublicationGapReport, stopped bool) {
	if !rep.Observed {
		return
	}

	if d.m != nil {
		d.m.Counter(counterPublicationUnpublishedCaptures).Add(int64(rep.UnpublishedCaptures))
		d.m.Counter(counterPublicationUnindexedObjects).Add(int64(rep.UnindexedObjectCandidates))
		if rep.Incomplete {
			d.m.Counter(counterPublicationIncomplete).Add(1)
		}
	}

	switch {
	case rep.HasGaps():
		d.log.Loud("daemon: unpublished captures or unindexed objects found at startup",
			"unpublished_captures", rep.UnpublishedCaptures,
			"unindexed_object_candidates", rep.UnindexedObjectCandidates,
			"pending_objects", rep.PendingObjects,
			"captures_scanned", rep.CapturesScanned,
			"objects_scanned", rep.ObjectsScanned,
			"incomplete", rep.Incomplete,
			"truncated", rep.Truncated,
			"notes", notesField(rep.Notes),
		)
	case rep.Incomplete && stopped:
		d.log.Warn("daemon: publication accounting stopped by the daemon's stop before it finished",
			"captures_scanned", rep.CapturesScanned,
			"objects_scanned", rep.ObjectsScanned,
		)
	case rep.Incomplete:
		// No gap was found, but the pass could not prove there is none. Say so out loud rather than
		// letting a truncated or unreadable scan pass for a clean bill of health.
		d.log.Loud("daemon: publication accounting incomplete; a zero gap count is only a lower bound",
			"captures_scanned", rep.CapturesScanned,
			"objects_scanned", rep.ObjectsScanned,
			"truncated", rep.Truncated,
			"notes", notesField(rep.Notes),
		)
	default:
		d.log.Debug("daemon: publication accounting clean",
			"captures_scanned", rep.CapturesScanned,
			"objects_scanned", rep.ObjectsScanned,
			"legacy_control_captures", rep.LegacyControlCaptures,
			"post_snapshot_entries", rep.PostSnapshotEntries,
		)
	}
}

// notesField renders the generic note list as a single log value. The notes are a fixed vocabulary
// (internal/store's accounting), so joining them carries nothing sensitive.
func notesField(notes []string) string {
	if len(notes) == 0 {
		return "none"
	}
	out := notes[0]
	for _, n := range notes[1:] {
		out += "; " + n
	}
	return out
}

// appendGenericNote adds a fixed note without duplicating one already present.
func appendGenericNote(notes []string, note string) []string {
	for _, n := range notes {
		if n == note {
			return notes
		}
	}
	return append(notes, note)
}
