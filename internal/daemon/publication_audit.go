package daemon

import (
	"context"
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
// WHERE main should call LoudPublicationGaps: immediately after the existing
// d.sweepCheckpointIntegrity(runCtx) call in Run (today daemon.go:594), on Run's own goroutine —
// AFTER the startup Drain, so a capture the drain is about to publish is not reported as a transient
// gap, and BEFORE the serve loop. Never from Stop, and never before Drain. The store method only
// reads (a filesystem walk plus in-memory index lookups under a read lock) and writes nothing, so it
// is safe against the daemon's live read-write store at that position; the position is about the
// MEANING of the answer (post-drain, so the numbers are real), not about writer safety.
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

	CapturesScanned int
	ObjectsScanned  int

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
	a, err := auditor.AuditPublication(ctx, d.publicationScanCap())
	rep := PublicationGapReport{
		Observed:                  true,
		UnpublishedCaptures:       a.UnpublishedCaptures,
		LegitimatelyUnpublished:   a.LegitimatelyUnpublished,
		UnindexedObjectCandidates: a.UnindexedObjectCandidates,
		PendingObjects:            a.PendingObjects,
		CapturesScanned:           a.CapturesScanned,
		ObjectsScanned:            a.ObjectsScanned,
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
	return rep
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
	if !rep.Observed {
		return rep
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
		)
	}
	return rep
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
