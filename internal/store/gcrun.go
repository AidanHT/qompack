package store

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// gcLiveDomain domain-separates the digest of the live chunk set.
//
// It is deliberately NOT one of core's five registered production domains: those key content the
// store persists, and this digest keys nothing — it exists only so a resumed sweep can tell
// whether the live set it just recomputed is the same one the interrupted pass was sweeping
// against. The same reasoning internal/tokens/calibrate.go gives for calibKeyDomain applies here.
const gcLiveDomain = "qompack.gclive.v1"

// The two files a resumable GC keeps under .qompack/state.
const (
	gcStateFile = "gc.json"
	gcLiveFile  = "gc-live.bin"
)

// gcCheckEvery is how many items pass between deadline and cancellation checks. Checking every
// item would put a clock read in the inner loop of a 50 000-object walk; checking too rarely would
// overshoot the deadline the idle scheduler granted.
const gcCheckEvery = 256

// hoursPerDay converts the retention window's days into a duration.
const hoursPerDay = 24

// gcRetentionDisabled is the resolved value for a retention axis a caller switched off.
const gcRetentionDisabled = -1

// hashToken matches any string that could be a hash reference.
//
// The "sha256:" prefix is OPTIONAL on purpose. core.Hash.String() is the canonical text form, but
// nothing in a checkpoint, pin or elimination schema FORCES a producer to use it, and a hash
// serialized bare would otherwise be invisible to the mark phase and its object silently deleted.
// Treating every 64-hex string as a live hash is a conservative superset of "pointers, pins,
// evidence and depends_on" — which is the right error direction for a collector: over-retention
// costs disk, under-retention is data loss.
var hashToken = regexp.MustCompile(`^(?:sha256:)?[0-9a-f]{64}$`)

// gcState is the resumable cursor persisted at .qompack/state/gc.json.
type gcState struct {
	V          int            `json:"v"`
	Phase      string         `json:"phase"`
	Cursor     string         `json:"cursor"`
	LiveDigest string         `json:"live_digest"`
	Roots      int            `json:"roots"`
	Scanned    int            `json:"scanned"`
	Deleted    int            `json:"deleted"`
	Freed      int64          `json:"freed"`
	Started    core.UnixMilli `json:"started"`
}

// GC runs an authoritative mark-and-sweep collection (00-ARCHITECTURE.md §5.8 GC semantics).
//
// Two different clocks are in play, deliberately. The RETENTION cutoff reads the injected
// core.Clock, so a test can age a root by moving the clock rather than by waiting. The DEADLINE and
// GCReport.Duration read wall-clock time, because Deadline is a latency budget the idle scheduler
// granted and a frozen logical clock must never make it un-expirable.
func (s *FSStore) GC(ctx context.Context, p GCPolicy) (GCReport, error) {
	if err := s.mutate(); err != nil {
		return GCReport{}, err
	}
	// GCPolicy.Deadline bounds how long a pass runs once it has started; ctx is how the CALLER
	// cancels one. They are not the same lever, and honouring only the first would let a shutdown
	// wait out a full sweep of objects/.
	if err := ctx.Err(); err != nil {
		return GCReport{}, err
	}
	started := time.Now()
	var deadline time.Time
	if p.Deadline > 0 {
		deadline = started.Add(p.Deadline)
	}

	days, sessions := s.resolveRetention(p)
	m, err := s.markPass(ctx, days, sessions, deadline)
	if err != nil {
		if errors.Is(err, errRetentionRootsUnavailable) {
			// An unreadable lease, pending-write or rollback set is indistinguishable from a FULL
			// one, so the pass collects nothing rather than reading a failure as "nothing is held"
			// (SP-20 invariant 9). The report says so; it is not a silent no-op.
			s.log.Warn("store: a gc retention-root source failed; collecting nothing this pass", "err", err)
			return GCReport{RetentionRootsError: true, Duration: time.Since(started)}, nil
		}
		return GCReport{}, err
	}
	if m.truncated {
		// A truncated mark produced an incomplete live set, and sweeping against one would delete
		// live objects — every reference the phase never reached looks dead. So the pass ends here
		// with nothing retired and nothing swept, and neither the live set nor a resume cursor is
		// persisted: a cursor recorded now would let the NEXT pass resume a sweep against this
		// incomplete set, which is the same data loss one run later.
		s.log.Warn("store: gc mark phase ran out of its deadline; nothing was collected this pass",
			"deadline", p.Deadline)
		return GCReport{Truncated: true, Duration: time.Since(started)}, nil
	}

	rep := GCReport{}
	outcomes := newOutcomeLog(p, &rep)
	// Quota runs BEFORE the outcomes are recorded and before anything is retired, because a quota
	// eviction changes a root's outcome from retained to quota_evicted — and it may only ever take
	// a root the retention window alone was holding (applyQuota).
	s.applyQuota(&m, p.QuotaBytes, &rep, outcomes)
	s.recordOutcomes(&m, &rep, outcomes)
	rep.LiveObjects, rep.Roots = len(m.liveChunks), len(m.liveRoots)

	digest := liveDigest(m.liveChunks)
	if err := s.writeLiveSet(m.liveChunks); err != nil {
		s.log.Warn("store: could not persist the gc live set; this pass will not be resumable", "err", err)
	}

	prior, resuming := s.loadGCState(digest)
	if resuming {
		rep.ScannedObjects, rep.DeletedObjects, rep.BytesFreed = prior.Scanned, prior.Deleted, prior.Freed
	}

	if !p.DryRun {
		if err := s.tombstoneDeadRoots(ctx, m.deadRoots); err != nil {
			return rep, err
		}
	}

	cursor, truncated, err := s.sweep(ctx, sweepArgs{
		live:     m.liveChunks,
		dryRun:   p.DryRun,
		deadline: deadline,
		started:  started,
		resume:   resumeCursor(prior, resuming),
		rep:      &rep,
	})
	// Pending markers are expired AFTER the sweep, never before it: this pass held their objects
	// live, so retiring the marker now leaves the objects to the NEXT pass rather than deleting
	// content and its only record of being pending in one step.
	s.expirePendingMarkers(days, p.DryRun, &rep, outcomes)
	rep.Truncated = truncated
	rep.Duration = time.Since(started)
	if err != nil {
		_ = s.saveGCState(digest, cursor, rep)
		return rep, err
	}

	if truncated {
		if serr := s.saveGCState(digest, cursor, rep); serr != nil {
			s.log.Warn("store: could not persist the gc cursor", "err", serr)
		}
		return rep, nil
	}
	s.clearGCState()
	s.compactRetentionRoots(p.DryRun, &rep)

	s.mu.Lock()
	s.statsDirty = true
	if rep.BytesFreed > 0 {
		s.bytesOnDisk -= rep.BytesFreed
		if s.bytesOnDisk < 0 {
			s.bytesOnDisk = 0
		}
	}
	s.mu.Unlock()
	return rep, nil
}

// resumeCursor returns the sweep cursor to continue from, or "" to sweep from the beginning.
func resumeCursor(prior gcState, resuming bool) string {
	if resuming && prior.Phase == "sweep" {
		return prior.Cursor
	}
	return ""
}

// resolveRetention turns a GCPolicy's two tri-state axes into concrete windows.
//
// The tri-state is normative, because the ZERO GCPolicy is what a careless caller passes: 0
// INHERITS store.retention from configuration (30 days / 10 sessions), so GC(ctx, GCPolicy{}) is a
// safe default-retention run and never a mass deletion; a positive value overrides; and only a
// NEGATIVE value disables an axis, which is what a test or `qompack fsck --gc-all` uses to force
// collection.
func (s *FSStore) resolveRetention(p GCPolicy) (days, sessions int) {
	days, sessions = p.RetainDays, p.RetainSessions
	if days == 0 {
		days = s.cfg.Store.Retention.Days
	}
	if sessions == 0 {
		sessions = s.cfg.Store.Retention.Sessions
	}
	if days < 0 {
		days = gcRetentionDisabled
	}
	if sessions < 0 {
		sessions = gcRetentionDisabled
	}
	return days, sessions
}

// gcBudget is the ctx-and-deadline pair a GC pass's long-running loops are checked against.
//
// Both levers are needed and they mean different things: ctx is how the CALLER cancels a pass, and
// an expired ctx is an error on every loop that reads it; Deadline is the latency budget the idle
// scheduler granted, and running out of it is a normal outcome that returns Truncated. Checking
// only every gcCheckEvery items keeps a time.Now() off the per-item path.
//
// The two checks are offered separately because the loops are not alike. spent is for work whose
// cost grows with the project's history and is paid to the disk — the harvest and the sweep — where
// stopping mid-phase is exactly what a latency budget is for. cancelled is for the mark's index
// walks: bounded by the in-memory indexes, microseconds of map iteration, and worth finishing even
// when the budget has run out, because abandoning them throws away a harvest that has already been
// paid for and hands the pass nothing. They still answer to ctx, since a shutdown must not wait for
// any loop at all.
type gcBudget struct {
	ctx      context.Context
	deadline time.Time
	n        int
}

func newGCBudget(ctx context.Context, deadline time.Time) *gcBudget {
	return &gcBudget{ctx: ctx, deadline: deadline}
}

// spent counts one item and reports whether the loop must stop, for either reason.
func (b *gcBudget) spent() (truncated bool, err error) {
	b.n++
	if b.n%gcCheckEvery != 0 {
		return false, nil
	}
	if ctxErr := b.ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	if !b.deadline.IsZero() && time.Now().After(b.deadline) {
		return true, nil
	}
	return false, nil
}

// cancelled counts one item and reports only the caller's cancellation.
func (b *gcBudget) cancelled() error {
	b.n++
	if b.n%gcCheckEvery != 0 {
		return nil
	}
	return b.ctx.Err()
}

// markResult is everything one mark phase decided, beyond the two sets the sweep consumes.
//
// It exists because SP-20 needs the mark's REASONS, not only its verdicts: a per-root outcome has
// to say why a root was kept, and a quota may only evict roots the retention window alone was
// holding. hard and soft are that split - hard is a root some producer still needs (invariant 9),
// soft is a root that is merely young enough.
type markResult struct {
	liveChunks map[core.Hash]struct{}
	liveRoots  map[core.Hash]struct{}
	deadRoots  []core.Hash
	// harvested is every hash a root FILE or a RetentionRootSource named, with its class.
	harvested map[core.Hash]RetentionRoot
	// hard maps a live root to the retention claim that holds it. Never quota-evictable.
	hard map[core.Hash]RetentionRoot
	// soft maps a live root to the retention-window reason that holds it. Quota-evictable.
	soft map[core.Hash]string
	// size is each live root's canonical byte total, and total their sum: the denominator that
	// turns a compressed on-disk quota into a per-root eviction estimate.
	size  map[core.Hash]int64
	total int64
	// evicted names the roots the quota moved into deadRoots, so the expiry pass does not report
	// them a second time under the wrong result.
	evicted   map[core.Hash]struct{}
	truncated bool
}

// errRetentionRootsUnavailable reports that a RetentionRootSource could not answer. GC translates
// it into a pass that collects nothing and says so, never into an empty retention set.
var errRetentionRootsUnavailable = errors.New("qompack: gc retention roots unavailable")

// retentionUnavailable wraps errRetentionRootsUnavailable with context, so GC halts the pass with
// RetentionRootsError rather than reading a failed read as an empty retention set.
func retentionUnavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errRetentionRootsUnavailable, fmt.Sprintf(format, args...))
}

// rootedLstat Lstats base inside dir through an os.Root, symlink-non-following. The FileInfo it returns
// is HANDLE-based, so os.SameFile can compare it against an opened handle's Stat — on Windows a plain
// os.Lstat (via FindFirstFile) does not populate the file-identity fields SameFile needs, and comparing
// against it always reports "not the same file". Errors keep os.IsNotExist's shape.
func rootedLstat(dir, base string) (os.FileInfo, error) {
	root, err := os.OpenRoot(paths.Long(dir))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Lstat(base)
}

// gcDirBatch bounds how many directory entries one ReadDir call materialises, so enumerating a
// retention directory never allocates the whole listing at once and can answer the budget between
// batches.
const gcDirBatch = 512

// maxRetentionSources caps the TOTAL number of retention-root files one mark phase will accumulate
// across every directory (checkpoints, the pending-write registry, and every delivery segment). A
// bounded per-batch read does not bound the accumulated slice, so a runaway or hostile directory could
// still grow it without limit; hitting this ceiling truncates the WHOLE mark (nothing collected, no
// sweep) rather than proceeding against a set the phase decided to stop building.
const maxRetentionSources = 1 << 20

// mark computes the live chunk set and the live root set.
//
// It is the narrow, long-standing view of markPass: the two sets the sweep and the tombstone phase
// consume, and nothing else. The signature is unchanged from SP-06 deliberately - it is what
// TestGC_TombstoneRetiresOnlyMarkTimeDead exercises the mark/tombstone window through.
//
// sessions is not dead: it is threaded straight into markPass, which consumes it through
// recentSessionSet, and it selects the "whichever is longer" disjunction's session half (Qompack.md
// 8.2). What unparam sees is that every current call site happens to pass -1, because the retention
// evidence this seam carries is the day window and the mark/tombstone window rather than the
// session window. Deleting the parameter for that would leave the wrapper strictly narrower than
// the pass it wraps - the only mark path that cannot express a session window - and re-widening it
// later would change the exact signature the GC suite's retention rows are written against
// (T20-M1-07). So it stays, and the finding is suppressed on the one line that raises it.
func (s *FSStore) mark(ctx context.Context, days, sessions int, deadline time.Time) ( //nolint:unparam // see the paragraph above: markPass consumes sessions; only the call sites fix it at -1.
	liveChunks, liveRoots map[core.Hash]struct{}, deadRoots []core.Hash, truncated bool, err error,
) {
	m, err := s.markPass(ctx, days, sessions, deadline)
	if err != nil || m.truncated {
		return nil, nil, nil, m.truncated, err
	}
	return m.liveChunks, m.liveRoots, m.deadRoots, false, nil
}

// markPass computes the live sets, the dead set, and the reason behind every one of them.
//
// "Whichever is longer" (Qompack.md 8.2) is implemented as a DISJUNCTION: an entry is in-window
// when it is inside the day window OR its session is among the most recent ones. An EPHEMERAL root
// is never in-window by the age clause - retrieval spam is reclaimable precisely because objects
// are content-addressed, so a chunk it shares with a real tool result is still held alive by that
// result (Qompack.md 8.7).
//
// The two halves of the phase are budgeted differently, and the asymmetry is the point. The HARVEST
// answers to both levers: it is the disk-proportional half, and a latency budget exists to stop
// exactly that. The INDEX WALKS below answer to ctx alone - they are map iteration over indexes the
// store already holds in memory, they cost microseconds where the harvest costs milliseconds, and
// truncating them would discard a harvest already paid for to save a rounding error. A pass that
// gets through its harvest therefore always gets a complete live set to hand the sweep, and the
// deadline lands on the sweep, which can resume.
//
// A truncated mark yields an INCOMPLETE live set, which is the one thing a collector must never
// sweep against - every unvisited reference would look dead. So truncation here stops the pass at
// its caller rather than being carried forward, and neither the live set nor a resume cursor is
// persisted from it.
func (s *FSStore) markPass(ctx context.Context, days, sessions int, deadline time.Time) (markResult, error) {
	budget := newGCBudget(ctx, deadline)
	harvested, truncated, err := s.harvestHashes(budget)
	if err != nil || truncated {
		return markResult{truncated: truncated}, err
	}
	if err := s.retentionFromSources(ctx, harvested); err != nil {
		return markResult{}, err
	}
	recent := s.recentSessionSet(sessions)

	var cutoff core.UnixMilli
	if days >= 0 {
		cutoff = core.UnixMilli(s.deps.Clock.Now().Add(-time.Duration(days) * hoursPerDay * time.Hour).UnixMilli())
	}
	inAgeWindow := func(ts core.UnixMilli) bool { return days >= 0 && ts >= cutoff }

	m := markResult{
		liveRoots:  make(map[core.Hash]struct{}),
		liveChunks: make(map[core.Hash]struct{}, len(harvested)),
		harvested:  harvested,
		hard:       make(map[core.Hash]RetentionRoot),
		soft:       make(map[core.Hash]string),
		size:       make(map[core.Hash]int64),
		evicted:    make(map[core.Hash]struct{}),
	}
	// A harvested hash may name a root OR a chunk; nothing in the schemas distinguishes them, so
	// it is held live as both.
	for h := range harvested {
		m.liveChunks[h] = struct{}{}
	}

	// The index walks below run under one read lock and are released through this named unlock on
	// every path, including the cancelled early returns.
	unlocked := false
	s.mu.RLock()
	defer func() {
		if !unlocked {
			s.mu.RUnlock()
		}
	}()
	stop := func() bool {
		if e := budget.cancelled(); e != nil {
			err = e
			return true
		}
		return false
	}

	for h, e := range s.rootIndex {
		if stop() {
			return markResult{}, err
		}
		if r, ok := harvested[h]; ok {
			m.liveRoots[h] = struct{}{}
			m.hard[h] = r
			continue
		}
		if !e.Eph && inAgeWindow(e.TS) {
			m.liveRoots[h] = struct{}{}
			m.soft[h] = "inside the retention age window"
		}
	}
	for _, rec := range s.toolUse {
		if stop() {
			return markResult{}, err
		}
		if rec.Root.IsZero() {
			continue
		}
		// The ephemeral exclusion applies HERE too, not only on the root path above. 8.2: "an
		// ephemeral root is never in-window by the age clause; only the session clause and an
		// explicit root reference keep it alive." Reading the age clause without consulting Eph
		// made the documented property void on the main path rather than on an edge case - every
		// retrieval result is recorded as a tool_use, so every ephemeral root was age-live through
		// this loop no matter what the root path decided about it.
		eph := false
		if e, ok := s.rootIndex[rec.Root]; ok {
			eph = e.Eph
		}
		byAge := !eph && inAgeWindow(rec.TS)
		bySession := sessions >= 0 && recent[rec.Session]
		if !byAge && !bySession {
			continue
		}
		m.liveRoots[rec.Root] = struct{}{}
		if _, hard := m.hard[rec.Root]; !hard {
			if bySession {
				m.soft[rec.Root] = "used by one of the most recent sessions"
			} else {
				m.soft[rec.Root] = "recent tool_use inside the retention age window"
			}
		}
	}
	for _, hist := range s.fileHist {
		for _, v := range hist {
			if stop() {
				return markResult{}, err
			}
			if !inAgeWindow(v.TS) {
				continue
			}
			m.liveRoots[v.Root] = struct{}{}
			if _, hard := m.hard[v.Root]; !hard {
				m.soft[v.Root] = "a file version inside the retention age window"
			}
		}
	}
	// SP-20 invariant 9: a recovery record and the base it declares share fate. Done HERE, before
	// the chunk expansion, so a base pulled in by its delta contributes its chunks too.
	s.coupleRecoveryRootsLocked(&m)

	for h := range m.liveRoots {
		if stop() {
			return markResult{}, err
		}
		e, ok := s.rootIndex[h]
		if !ok {
			continue
		}
		var n int64
		for _, c := range e.Root.Chunks {
			m.liveChunks[c.Hash] = struct{}{}
			n += int64(c.Len)
		}
		m.size[h] = n
		m.total += n
	}
	// The dead set is fixed HERE, under the same read lock as the live set, and is the only
	// thing the tombstone phase may retire. Re-deriving it later from a fresh read of
	// s.rootIndex opens a window: a root published between this snapshot and the tombstone
	// phase is in the index but not in the live set, and would be retired seconds after it
	// was written (found by V2-VERIFY's 4.7 authoring).
	m.deadRoots = make([]core.Hash, 0, len(s.rootIndex))
	for h := range s.rootIndex {
		if stop() {
			return markResult{}, err
		}
		if _, ok := m.liveRoots[h]; !ok {
			m.deadRoots = append(m.deadRoots, h)
		}
	}
	s.mu.RUnlock()
	unlocked = true

	return m, nil
}

// coupleRecoveryRootsLocked closes the live set over the delta/base relation. s.mu must be held.
//
// SP-20 invariant 9 forbids collecting "a root needed by a lease, delta, pending write, checkpoint,
// evidence reference, or rollback record", and a delta needs its base exactly as much as a base
// needs its delta: half a pair is a recovery claim that cannot be honoured. So liveness propagates
// in BOTH directions across Deltas, Orig and Base, and so does hardness - a delta record of a
// pinned root is not quota-evictable either, or the quota would break the pair the pin protects.
//
// It runs to a fixpoint because a coupled partner can itself declare one; in practice the chain is
// one link long and the loop makes exactly two passes.
func (s *FSStore) coupleRecoveryRootsLocked(m *markResult) {
	for {
		added := false
		for h := range m.liveRoots {
			e, ok := s.rootIndex[h]
			if !ok {
				continue
			}
			claim, isHard := m.hard[h]
			for _, partner := range [3]core.Hash{e.Deltas, e.Orig, e.Base} {
				if partner.IsZero() {
					continue
				}
				if _, known := s.rootIndex[partner]; !known {
					continue
				}
				_, live := m.liveRoots[partner]
				_, partnerHard := m.hard[partner]
				if live && (partnerHard || !isHard) {
					continue
				}
				m.liveRoots[partner] = struct{}{}
				if isHard {
					delete(m.soft, partner)
					m.hard[partner] = RetentionRoot{
						Hash: partner, Class: RetentionDeltaBase,
						Reason: "coupled to retained root " + h.Short() + ": " + claim.Reason,
					}
				} else if _, seen := m.soft[partner]; !seen {
					m.soft[partner] = "coupled to live root " + h.Short() + " as its delta base"
				}
				added = true
			}
		}
		if !added {
			return
		}
	}
}

// recentSessionSet returns the n most recent session IDs.
//
// It merges the sessions persisted in index/sessions.jsonl with the ones recomputed from the
// in-memory tool_use index, because a session that has not been Flushed yet is still the CURRENT
// session — collecting its content on the grounds that it has not been written down would be
// exactly backwards.
func (s *FSStore) recentSessionSet(n int) map[core.SessionID]bool {
	out := map[core.SessionID]bool{}
	if n < 0 {
		return out
	}

	merged := map[core.SessionID]core.UnixMilli{}
	s.mu.RLock()
	for id, e := range s.sessions {
		merged[id] = e.End
	}
	s.mu.RUnlock()
	for id, e := range s.recomputeSessions() {
		if end, ok := merged[id]; !ok || e.End > end {
			merged[id] = e.End
		}
	}

	type sess struct {
		id  core.SessionID
		end core.UnixMilli
	}
	all := make([]sess, 0, len(merged))
	for id, end := range merged {
		all = append(all, sess{id, end})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].end != all[j].end {
			return all[i].end > all[j].end
		}
		return all[i].id > all[j].id
	})
	if n < len(all) {
		all = all[:n]
	}
	for _, e := range all {
		out[e.id] = true
	}
	return out
}

// gcRootFile is one file GC harvests hash references from, together with the retention class
// every reference inside it carries. The class is what turns an aggregate "kept" into a reportable
// reason (SP-20 M1-03: "quotas and expiry are visible policy outcomes").
type gcRootFile struct {
	path   string
	class  RetentionClass
	reason string
	// lines, when non-nil, makes this file RECORD-aware instead of a flat token stream: it is
	// called with each raw line and answers the class and reason that line's hashes carry, or
	// false to skip the line entirely. Only .jsonl files whose records are one-per-line may set
	// it — a multi-line .json document has no line semantics to read.
	lines func(line []byte) (RetentionClass, string, bool)
	// required marks a source that MUST exist. An optional source that is absent is normal (a producer
	// that never shipped); a REQUIRED source that is absent — a committed delivery segment's lease/ack
	// journal, proved present at resolve time — is a referenced file that vanished mid-harvest, and it
	// halts the pass rather than being silently skipped as "empty".
	required bool
	// carry, when non-nil, marks a delivery segment's carried-lease file (dcarryFile): its header is
	// checked against the segment it names and its body against the header (harvestCarry), and each
	// body line is read through lines exactly as a lease journal line is.
	carry *dcarryExpect
}

// dcarryExpect is what a carried-lease file must declare: the segment whose directory holds it.
type dcarryExpect struct {
	segment uint64
}

// gcRootFiles returns every file whose hash references keep content alive.
//
// The files are read STRUCTURALLY rather than through internal/checkpoint, internal/pins,
// internal/negknow or internal/daemon: all of them import store, so importing them back would be
// an import cycle (00-ARCHITECTURE.md 3.2). A missing file is never an error - a producer that has
// not shipped is indistinguishable from a project that never used one, and a store that refused to
// collect until every producer existed would grow without bound.
//
// The set is SP-20 invariant 9's list, in order: pins, evidence, open delivery leases, declared
// retention roots (rollback/backup material), committed checkpoints, and the pending-write
// registry. Delta bases are not a file - they are a relation, closed over in
// coupleRecoveryRootsLocked.
//
// The two directory sources (checkpoints, pending) are enumerated through listRetentionDir, so a
// directory that only "has not been created yet" is normal absence, but a LISTING FAILURE, an aliased
// (symlinked) directory or a non-directory in its place HALTS the pass with RetentionRootsError rather
// than dropping every root that directory would have named (main's durable-data adjudication). It
// answers to the budget, so a deadline truncates the pass rather than the file list.
func (s *FSStore) gcRootFiles(budget *gcBudget) ([]gcRootFile, dsegView, bool, error) {
	out := []gcRootFile{
		{filepath.Join(s.l.Pins, invariantsFile), RetentionPin, "referenced by a pinned invariant", nil, false, nil},
		{filepath.Join(s.l.Records, eliminationsFile), RetentionEvidence, "referenced by elimination evidence", nil, false, nil},
		{filepath.Join(s.l.Records, evidenceRootsFile), RetentionEvidence, "referenced by an evidence record", nil, false, nil},
	}
	// Delivery leases: the legacy segment on an unmigrated tree; once the daemon has rotated, the active
	// segment's journal and carried leases, and every staged segment's. A torn/missing/conflicting/unknown
	// segment authority halts here. The returned frontier is rechecked AFTER the harvest (harvestHashes).
	leaseFiles, frontier, err := s.deliveryLeaseSources(budget)
	if err != nil {
		return nil, dsegView{}, false, err
	}
	out = append(out, leaseFiles...)
	out = append(out, gcRootFile{
		filepath.Join(s.l.State, retentionRootsFile), RetentionRollback, declaredRootReason,
		s.declaredRetentionLines(), false, nil,
	})

	checkpoints, truncated, err := s.listRetentionDir(s.l.Checkpoints, budget, maxRetentionSources-len(out), func(e fs.DirEntry) (gcRootFile, bool) {
		if e.IsDir() {
			return gcRootFile{}, false
		}
		switch filepath.Ext(e.Name()) {
		case ".json", ".jsonl":
			return gcRootFile{
				filepath.Join(s.l.Checkpoints, e.Name()), RetentionCheckpoint,
				"referenced by committed checkpoint " + e.Name(), nil, false, nil,
			}, true
		}
		return gcRootFile{}, false
	})
	if err != nil || truncated {
		return nil, dsegView{}, truncated, err
	}
	out = append(out, checkpoints...)

	pending, truncated, err := s.pendingRootFiles(budget, maxRetentionSources-len(out))
	if err != nil || truncated {
		return nil, dsegView{}, truncated, err
	}
	return append(out, pending...), frontier, false, nil
}

// deliveryLeaseSources returns the open-lease harvest sources and the frontier witness for the
// post-harvest stable-frontier recheck.
//
// On a genuinely unmigrated tree it is the single legacy lease journal, harvested exactly as before
// segments existed. Once the daemon has rotated it is BOUNDED by the active window, not by the
// project's history (review finding 1): the ACTIVE segment's lease journal, which resolveDeliverySegments
// has already proved present and which is marked REQUIRED so a mid-harvest disappearance halts; from
// segment 1 on, the active segment's carried-lease file (dcarryFile), equally required, which the daemon
// wrote at the rotation that opened the segment and which names every archived lease that had no
// acknowledgement then; and every STAGED (uncommitted) segment's lease journal and carry, conservatively
// retained but not required. Acknowledgements fold from the same segments, so an acknowledgement the
// active segment records settles a carried lease of any older segment. An archived segment's own
// journals are not read: every lease in them is either acknowledged, and so released, or carried.
// Any unreadable or conflicting authority is returned as errRetentionRootsUnavailable, which halts the
// pass — the reader never reverts to the legacy segment when a rotation's authority is present but
// cannot be read.
func (s *FSStore) deliveryLeaseSources(budget *gcBudget) ([]gcRootFile, dsegView, error) {
	view, err := s.resolveDeliverySegments(budget)
	if err != nil {
		return nil, dsegView{}, err
	}
	if len(view.committed) == 0 {
		return nil, dsegView{}, retentionUnavailable("delivery segment authority names no committed segment")
	}
	active := view.committed[len(view.committed)-1]
	segs := make([]uint64, 0, 1+len(view.staged))
	segs = append(segs, active)
	segs = append(segs, view.staged...)

	ackPaths := make([]string, 0, len(segs))
	for _, seq := range segs {
		ackPaths = append(ackPaths, filepath.Join(s.dsegSegmentDir(seq), deliveryAckFile))
	}
	acked := s.acknowledgedDeliveriesFrom(ackPaths)

	out := make([]gcRootFile, 0, 2*len(segs))
	for i, seq := range segs {
		staged := i > 0
		reason, required := openLeaseReason, !view.legacy && !staged
		if staged {
			reason = "held by a staged (uncommitted) delivery segment lease"
		}
		out = append(out, gcRootFile{
			filepath.Join(s.dsegSegmentDir(seq), deliveryLeaseFile), RetentionLease, reason,
			openLeaseLines(acked), required, nil,
		})
		if seq == 0 {
			continue // the original segment carries nothing: nothing was archived before it
		}
		out = append(out, gcRootFile{
			filepath.Join(s.dsegSegmentDir(seq), dcarryFile), RetentionLease, reason,
			openLeaseLines(acked), required, &dcarryExpect{segment: seq},
		})
	}
	return out, view, nil
}

// pendingRootFiles lists the durable pending-write registry: one marker per Put that has written
// objects but has not yet appended its roots.jsonl line.
//
// This is what replaces the sweep's incidental freshness luck as the PRIMARY mechanism. That guard
// spares an object younger than the running pass, which covers the window only for as long as the
// process lives; a marker survives the crash itself, so the next pass - hours or days later - still
// sees the write and does not collect content whose index line never landed.
//
// A registry directory that has never been created is normal absence (no Put has left a marker yet);
// a listing failure or a non-directory in its place HALTS, so the registry is never silently dropped.
func (s *FSStore) pendingRootFiles(budget *gcBudget, limit int) ([]gcRootFile, bool, error) {
	dir := filepath.Join(s.l.State, pendingWriteDir)
	return s.listRetentionDir(dir, budget, limit, func(e fs.DirEntry) (gcRootFile, bool) {
		if e.IsDir() || filepath.Ext(e.Name()) != pendingWriteSuffix {
			return gcRootFile{}, false
		}
		return gcRootFile{
			filepath.Join(dir, e.Name()), RetentionPending,
			"written but not yet rooted (pending marker " + e.Name() + ")", nil, false, nil,
		}, true
	})
}

// listRetentionDir enumerates one retention directory in bounded batches, returning a gcRootFile for
// every entry want accepts. A directory that does not exist yet is normal absence (a producer that has
// not shipped). A symlink component anywhere between the trusted project root and the directory, a
// symlink alias or non-directory in its place, an identity change under the open handle, or a listing
// that fails partway is an UNREADABLE retention source and halts the pass with errRetentionRootsUnavailable
// rather than being read as an empty listing.
//
// limit caps how many entries this call may ADD (the caller passes its remaining share of
// maxRetentionSources); reaching it truncates the whole mark. ctx and the deadline are checked BETWEEN
// batches: ctx cancellation is an error (never a truncation), an expired deadline truncates (nothing
// collected, resumable next pass).
func (s *FSStore) listRetentionDir(
	dir string, budget *gcBudget, limit int, want func(fs.DirEntry) (gcRootFile, bool),
) ([]gcRootFile, bool, error) {
	// Reject a symlink/reparse component between the trusted project root and this directory, so an
	// aliased .qompack or .qompack/state cannot redirect the enumeration outside the project.
	if nf := maintNoFollow(s.root, dir); nf != nil {
		if os.IsNotExist(nf) {
			return nil, false, nil // a component (or the dir) has not been created: normal absence
		}
		return nil, false, retentionUnavailable("confine retention dir %s: %v", dir, nf)
	}
	fi, lerr := rootedLstat(filepath.Dir(dir), filepath.Base(dir))
	switch {
	case lerr != nil && os.IsNotExist(lerr):
		return nil, false, nil // never created; see gcRootFiles
	case lerr != nil:
		return nil, false, retentionUnavailable("stat retention dir %s: %v", dir, lerr)
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, false, retentionUnavailable("retention dir %s is a symlink alias", dir)
	case !fi.IsDir():
		return nil, false, retentionUnavailable("retention dir %s is not a directory", dir)
	}
	d, oerr := os.Open(paths.Long(dir))
	if oerr != nil {
		// It existed at the Lstat above; if it is gone or unopenable now, that is observed evidence
		// failing under the pass, not absence.
		return nil, false, retentionUnavailable("open retention dir %s: %v", dir, oerr)
	}
	defer func() { _ = d.Close() }()
	dfi, serr := d.Stat()
	if serr != nil {
		return nil, false, retentionUnavailable("stat retention dir %s: %v", dir, serr)
	}
	if !dfi.IsDir() || !os.SameFile(fi, dfi) {
		// A non-directory, or a different object than the one Lstat accepted (swapped between the
		// Lstat and the open): refuse rather than enumerate what we cannot vouch for.
		return nil, false, retentionUnavailable("retention dir %s changed identity while opening", dir)
	}

	var out []gcRootFile
	for {
		ents, rerr := d.ReadDir(gcDirBatch)
		for _, e := range ents {
			f, ok := want(e)
			if !ok {
				continue
			}
			if len(out) >= limit {
				return nil, true, nil // total retention-source ceiling: truncate the whole mark
			}
			out = append(out, f)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return out, false, nil
			}
			return nil, false, retentionUnavailable("read retention dir %s: %v", dir, rerr)
		}
		// A batch returned entries and there may be more. Check the budget BETWEEN batches — never before
		// the first read — so a trivially small (or empty) directory always completes, exactly as the old
		// os.ReadDir did, while a genuinely large enumeration still answers to ctx and the deadline. ctx
		// cancellation is an error; an expired deadline truncates (nothing collected, resumable).
		if cerr := budget.ctx.Err(); cerr != nil {
			return nil, false, cerr
		}
		if !budget.deadline.IsZero() && time.Now().After(budget.deadline) {
			return nil, true, nil
		}
	}
}

// compactRetentionRoots sheds duplicate declarations from retention-roots.jsonl at the end of a
// completed pass, which is what gives that file a bounded lifecycle instead of one line per
// delivery forever.
//
// It runs LAST, after the sweep and the tombstones, so this pass marked against the file exactly as
// it found it and the compaction can never change what was collected. It is best effort: a failure
// is counted and logged, never returned, because a pass that swept correctly must not be reported
// as failed because a housekeeping rewrite could not run. A dry run rewrites nothing at all.
func (s *FSStore) compactRetentionRoots(dryRun bool, rep *GCReport) {
	if dryRun {
		return
	}
	res, err := CompactRetentionRoots(s.root)
	if err != nil {
		s.count("store.retentionroots.compactfailed", 1)
		s.log.Debug("store: could not compact the retention-root file", "err", err)
		return
	}
	if !res.Compacted {
		return
	}
	rep.RetentionRootsShed = res.LinesBefore - res.LinesAfter
	s.count("store.retentionroots.shed", int64(rep.RetentionRootsShed))
}

// The two reasons the record-aware root files report when the record itself supplies none.
const (
	openLeaseReason    = "held by an open delivery lease"
	declaredRootReason = "declared as a retention root"
)

// deliveryAckSetMax bounds the acknowledged-delivery set one pass builds, so a runaway or hostile
// frontier journal cannot cost a GC pass unbounded memory. It matches internal/daemon's own
// per-journal entry bound. Stopping at it leaves the remaining leases OPEN, which is the safe
// direction: the pass over-retains rather than closing a lease it never read the ack for. A variable
// only so a test can make it bind at fixture scale.
var deliveryAckSetMax = 1 << 16

// acknowledgedDeliveries reads the daemon's committed-frontier journal and returns the set of
// delivery nonces whose publication is complete.
//
// This is the half of the lease contract the retention set was missing. delivery-leases.jsonl
// records an ASSIGNMENT — a delivery has an identity and may be worked on — and says nothing about
// whether its object and reference ever landed; delivery-acks.jsonl is the record that says they
// did. Reading only the first made every delivery a daemon had ever handled a permanent retention
// root, so the retention set only ever grew (SP-20 invariant 9 retains what a lease NEEDS).
//
// The file is read structurally, like every other root file: internal/daemon imports store, so
// store cannot import it back (00-ARCHITECTURE.md §3.2).
//
// Every failure direction answers "fewer acknowledgements", never "more". A missing, unopenable,
// non-regular, truncated, oversized or half-parseable journal yields only the acks actually read, so
// any lease it could not vouch for stays open and stays retained: over-retention costs disk,
// under-retention is data loss. An ack is admitted only under a KNOWN version and a VALID observation
// identity, and it is indexed by nonce AND observation, because releasing a lease is a join on both:
// an ack whose version this build cannot read, or whose observation identity is malformed, must not
// free anything.
//
// The journal is opened through the same confined, non-regular-rejecting, shared handle the retention
// files use: a directory, symlink or FIFO where the ack journal belongs is refused BEFORE any blocking
// os.Open (a FIFO would otherwise hang), and the parent is confined so another tree's file cannot
// stand in. Because failing to read acks only RETAINS more, any such refusal is swallowed to nil here
// rather than halting the pass.
//
// The return is keyed nonce → the set of observation identities acknowledged under it. openLeaseLines
// releases a lease only when its own observation identity is one of them (main's adjudication:
// invalid/unknown/conflicting ack evidence conservatively retains, never releases a different lease).
//
// ackPaths is one ack journal per harvested delivery segment (segment 0 alone in the legacy case; the
// active segment and any staged ones once the daemon has rotated). An ACK in the active segment may
// settle a lease an OLDER segment archived (and carried), so every harvested segment's acks fold into
// the ONE returned set, which openLeaseLines then applies to every harvested lease line. The
// deliveryAckSetMax cap is on the TOTAL admitted (nonce, observation) pairs across those journals —
// the daemon bounds each at the same count, so a committed store never reaches it; hitting it leaves
// the rest of the leases open (over-retain). A per-file open/read failure contributes nothing and
// never halts.
func (s *FSStore) acknowledgedDeliveriesFrom(ackPaths []string) map[string]map[core.ObservationID]struct{} {
	acked := make(map[string]map[core.ObservationID]struct{})
	total := 0 // total admitted (nonce, observation) pairs across all segments
	for _, path := range ackPaths {
		if total >= deliveryAckSetMax {
			break
		}
		f, missing, err := s.openRetentionRoot(path)
		if err != nil || missing || f == nil {
			continue // an unreadable ack journal retains more; never hang, never halt
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)
		for sc.Scan() {
			if total >= deliveryAckSetMax {
				break // stopping leaves the remaining leases OPEN — the safe, over-retaining direction
			}
			var rec struct {
				Version       int                `json:"v"`
				Delivery      string             `json:"delivery"`
				ObservationID core.ObservationID `json:"observation_id"`
			}
			// A torn final line is the normal shape of a crash mid-append and does not parse, so the
			// delivery it was about is simply not acknowledged yet. An unknown version has semantics this
			// build cannot vouch for, and a malformed/zero identity cannot be joined to a lease: either
			// way the safe direction is to admit no acknowledgement for it.
			if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Version != core.EvidenceVersion || rec.Delivery == "" {
				continue
			}
			if !validRetentionObservation(string(rec.ObservationID)) {
				continue
			}
			set := acked[rec.Delivery]
			if set == nil {
				set = make(map[core.ObservationID]struct{})
				acked[rec.Delivery] = set
			}
			if _, dup := set[rec.ObservationID]; !dup {
				set[rec.ObservationID] = struct{}{}
				total++
			}
		}
		_ = f.Close()
	}
	return acked
}

// openLeaseLines returns the per-line filter that narrows the lease harvest to OPEN leases.
//
// A line the filter cannot read is treated as open. That is deliberate and is the same direction
// every other guard here takes: a lease whose nonce GC cannot recover must keep retaining, because
// the alternative is collecting content a delivery still in flight is about to publish.
//
// Releasing is a JOIN on nonce AND observation identity AND a known version, not on the nonce alone:
// an acknowledgement for delivery N under observation A must not release a lease for delivery N under
// observation B, and a lease whose version this build cannot read is retained. A lease that predates
// the observation_id field carries no identity to match; the nonce uniquely names the delivery, so a
// valid acknowledgement for it still releases the legacy lease, while a lease that DOES carry an
// identity is released only on an exact match (main's adjudication: never release a different lease).
func openLeaseLines(acked map[string]map[core.ObservationID]struct{}) func([]byte) (RetentionClass, string, bool) {
	return func(line []byte) (RetentionClass, string, bool) {
		if len(acked) == 0 {
			return RetentionLease, openLeaseReason, true
		}
		var rec struct {
			Version       int             `json:"v"`
			Delivery      string          `json:"delivery"`
			ObservationID json.RawMessage `json:"observation_id"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Delivery == "" {
			return RetentionLease, openLeaseReason, true
		}
		obsSet, seen := acked[rec.Delivery]
		if !seen || rec.Version != core.EvidenceVersion {
			// No acknowledgement for this nonce, or a lease shape this build cannot vouch for: open.
			return RetentionLease, openLeaseReason, true
		}
		if len(rec.ObservationID) == 0 {
			return "", "", false // legacy lease with no identity: a valid ack for its unique nonce releases it
		}
		var observation string
		if json.Unmarshal(rec.ObservationID, &observation) != nil || !validRetentionObservation(observation) {
			return RetentionLease, openLeaseReason, true
		}
		if _, ok := obsSet[core.ObservationID(observation)]; ok {
			return "", "", false // acknowledged under this exact identity: released
		}
		return RetentionLease, openLeaseReason, true // an ack exists, but for a different observation: retain
	}
}

// Observation identities on current leases are canonical, nonzero hashes. Only
// a genuinely absent field selects the historical nonce-only compatibility path;
// null, empty, zero and alternate hash spellings cannot authorize collection.
func validRetentionObservation(raw string) bool {
	h, err := core.ParseHash(raw)
	return err == nil && !h.IsZero() && h.String() == raw
}

// declaredRetentionLines returns the per-line reader for state/retention-roots.jsonl, so the class
// and reason the PRODUCER wrote are the ones the report gives back.
//
// The file carries a class per line and used to be harvested as an undifferentiated token stream
// under a blanket "rollback" label, which reported published evidence as rollback material. A line
// that does not parse still retains everything on it, under that same blanket label: an
// unrecognized declaration is a claim this build cannot read, never a claim it may ignore.
//
// Retaining it is safe; saying nothing about it is not, and that is finding F4-5. test/fault
// truncated this file mid-record and found a pass that completed with RetentionRootsError=false
// while quietly over-retaining everything the damaged line names — reported on no surface at all.
// §13 invariant 10 makes it Loud, ONCE per pass: the closure is built per call of gcRootFiles,
// which is once per mark phase, so a file with a thousand bad lines produces one line and not a
// thousand. The counter is per bad line so a later pass can be compared against the file.
func (s *FSStore) declaredRetentionLines() func([]byte) (RetentionClass, string, bool) {
	louded := false
	return func(line []byte) (RetentionClass, string, bool) {
		var r RetentionRoot
		if json.Unmarshal(line, &r) != nil || r.Class == "" {
			s.count("store.retention_roots_badline", 1)
			if !louded {
				louded = true
				s.log.Loud("store: retention-roots line unreadable; everything it names is retained "+
					"under the blanket rollback class rather than its declared one",
					"file", retentionRootsFile, "class", string(RetentionRollback))
			}
			return RetentionRollback, declaredRootReason, true
		}
		if r.Reason == "" {
			return r.Class, declaredRootReason, true
		}
		return r.Class, declaredRootReason + ": " + r.Reason, true
	}
}

// retentionFromSources folds every in-process RetentionRootSource into the harvested set.
//
// A source that fails is NOT read as "nothing to retain": errRetentionRootsUnavailable stops the
// whole pass, because an unreadable lease set and an empty one are indistinguishable and only one
// of the two is safe to act on.
func (s *FSStore) retentionFromSources(ctx context.Context, into map[core.Hash]RetentionRoot) error {
	for _, src := range s.deps.RetentionRoots {
		if src == nil {
			continue
		}
		roots, err := src.RetentionRoots(ctx)
		if err != nil {
			return fmt.Errorf("%w: %w", errRetentionRootsUnavailable, err)
		}
		for _, r := range roots {
			if r.Hash.IsZero() {
				continue
			}
			if _, seen := into[r.Hash]; seen {
				continue
			}
			if r.Class == "" {
				r.Class = RetentionLease
			}
			if r.Reason == "" {
				r.Reason = "held by a retention-root source"
			}
			into[r.Hash] = r
		}
	}
	return nil
}

// harvestHashes collects every hash-shaped string token from the GC root files.
//
// This is the unbounded half of the mark phase: its cost is the size of every checkpoint, pin,
// elimination, lease and pending-write file a project has ever written, streamed token by token. It
// answers to the budget for that reason, and a truncated harvest is reported rather than returned
// as a short set - a partial harvest is a live set with references missing from it.
func (s *FSStore) harvestHashes(budget *gcBudget) (map[core.Hash]RetentionRoot, bool, error) {
	out := make(map[core.Hash]RetentionRoot)
	files, frontier, truncated, err := s.gcRootFiles(budget)
	if err != nil || truncated {
		return nil, truncated, err
	}
	for _, f := range files {
		truncated, err := s.harvestFile(f, out, budget)
		if err != nil || truncated {
			return nil, truncated, err
		}
	}
	if hook := gcAfterHarvest.Load(); hook != nil {
		(*hook)()
	}
	// Stable-frontier recheck AFTER every retention source has been harvested: prove the delivery
	// segment authority did not switch, and no required segment file vanished, under the pass. A
	// committed rotation is a durable log append that precedes the head checkpoint, so this recheck
	// covers the head AND the whole log. Any change halts — nothing is swept against a stale frontier.
	if err := s.dsegRecheckFrontier(frontier); err != nil {
		return nil, false, err
	}
	return out, false, nil
}

// openRetentionRoot opens a GC retention-root file, distinguishing the states the collector must
// never confuse (main's durable-data adjudication):
//
//   - the parent directory, or the file itself, was NEVER CREATED → missing=true, normal absence (a
//     producer that has not shipped);
//   - the parent exists but the entry is a directory, a symlink or any other non-regular alias, or a
//     stat/open fails, or the entry vanished after we had just stat'd it → errRetentionRootsUnavailable
//     (an UNREADABLE source is not an empty one; reading it as empty would sweep what it names);
//   - a regular file → handed back for reading.
//
// Confinement and aliasing. Every path COMPONENT between the trusted project root (s.root) and the
// target is checked with maintNoFollow, so an aliased .qompack, .qompack/state, or any ancestor within
// the project cannot redirect the read outside it — pinning only the immediate parent through os.Root
// would still follow an already-aliased parent. A directory, FIFO or other non-regular entry AT the
// target is then rejected by os.Lstat, statically, before any open. The read itself goes through
// paths.OpenShared so a concurrent WriteAtomic replace (the daemon's journal rewrite, retention-roots
// compaction) is never blocked on Windows; OpenShared opens BY PATH, so os.SameFile compares the Lstat
// with the opened handle and refuses a mismatch — this DETECTS a swap between the Lstat and the open.
// It does NOT prevent a symlink being followed during a blocking open, and a path-based shared open
// does not itself pin every ancestor (maintNoFollow does the ancestor check, once, before the open):
// those residual concurrent-swap/FIFO races are documented in the work record and bounded by the
// retention paths living under the project's own .qompack tree, not a shared location.
func (s *FSStore) openRetentionRoot(path string) (fh *os.File, missing bool, err error) {
	if nf := maintNoFollow(s.root, path); nf != nil {
		if os.IsNotExist(nf) {
			return nil, true, nil // a component (or the file) has not been created: normal absence
		}
		return nil, false, retentionUnavailable("confine %s: %v", path, nf)
	}
	li, lerr := rootedLstat(filepath.Dir(path), filepath.Base(path))
	switch {
	case lerr != nil && os.IsNotExist(lerr):
		return nil, true, nil // the file itself has not been created: still absence, not a failure
	case lerr != nil:
		return nil, false, retentionUnavailable("stat retention root %s: %v", path, lerr)
	case !li.Mode().IsRegular():
		return nil, false, retentionUnavailable("retention root %s is not a regular file", path)
	}

	f, oerr := paths.OpenShared(path)
	if oerr != nil {
		// The entry was a regular file at the Lstat a moment ago. If it is gone or unopenable now, that
		// is observed evidence disappearing under the pass, NOT a producer that never shipped.
		return nil, false, retentionUnavailable("open retention root %s: %v", path, oerr)
	}
	ofi, serr := f.Stat()
	if serr != nil {
		_ = f.Close()
		return nil, false, retentionUnavailable("stat retention root %s: %v", path, serr)
	}
	if !ofi.Mode().IsRegular() || !os.SameFile(li, ofi) {
		_ = f.Close()
		return nil, false, retentionUnavailable("retention root %s changed identity while opening", path)
	}
	return f, false, nil
}

// harvestFile adds every hash-shaped string one root file names.
//
// A file with no per-line reader is walked as one token stream, which is what a multi-line .json
// checkpoint needs. A file that sets one is read line by line first, so a record can decide the
// class its hashes carry — or that they are not retained at all, which is how an ACKNOWLEDGED
// delivery lease stops pinning what it once needed.
//
// The FIRST file to name a hash owns its retention class. gcRootFiles returns a fixed order, so
// the reason a report gives for one hash is stable across passes.
//
// A read that stops SHORT of the file's end — an unreadable/aliased path, a line past scannerMaxBuf,
// a torn or malformed record (harvestTokens) — is an INCOMPLETE harvest: the references after the
// break were never seen, and sweeping against the short set would delete them as if they were absent.
// Every such case therefore halts the pass with errRetentionRootsUnavailable (main's adjudication:
// over-retain by collecting nothing, never sweep against unreadable evidence), and this build repairs
// nothing on disk.
func (s *FSStore) harvestFile(f gcRootFile, into map[core.Hash]RetentionRoot, budget *gcBudget) (bool, error) {
	fh, missing, err := s.openRetentionRoot(f.path)
	if err != nil {
		return false, err
	}
	if missing {
		if f.required {
			// A required source proved present at resolve time but is absent now: a referenced file
			// vanished mid-harvest. Never silently skip it as an optional empty source.
			return false, retentionUnavailable("required retention source %s vanished during the harvest", f.path)
		}
		return false, nil
	}
	defer func() { _ = fh.Close() }()

	if f.carry != nil {
		return s.harvestCarry(fh, f, into, budget)
	}
	if f.lines == nil {
		return s.harvestTokens(fh, f.class, f.reason, into, budget)
	}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		class, reason, retains := f.lines(line)
		if !retains {
			continue
		}
		truncated, harvestErr := s.harvestTokens(bytes.NewReader(line), class, reason, into, budget)
		if harvestErr != nil || truncated {
			return truncated, harvestErr
		}
	}
	if scErr := sc.Err(); scErr != nil {
		// An overlong line or a mid-file read error cut the scan short: the lines after it were
		// never read, so the harvest is incomplete and the pass must not sweep against it.
		return false, fmt.Errorf("%w: read retention root %s: %w", errRetentionRootsUnavailable, f.path, scErr)
	}
	return false, nil
}

// harvestCarry harvests a segment's carried-lease file. The header must be canonical, of this build's
// format, name the segment whose directory holds the file, and carry no more than dcarryMaxLeases; each
// body line is filtered by f.lines and harvested exactly as a lease journal line is; and the body must
// end exactly where the header says, with the digest it names. Anything else halts the pass: the carry
// is the only record of the archived leases that are still open, so a short or altered one would read
// as "fewer open leases" and sweep what they need.
func (s *FSStore) harvestCarry(r io.Reader, f gcRootFile, into map[core.Hash]RetentionRoot, budget *gcBudget) (bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return false, retentionUnavailable("read carried leases %s: %v", f.path, err)
		}
		return false, retentionUnavailable("carried-lease file %s is empty", f.path)
	}
	raw := sc.Bytes()
	var h dcarryHeader
	if json.Unmarshal(raw, &h) != nil || h.Version != dcarryVersion || h.Format != dcarryFormat ||
		h.Segment != f.carry.segment || h.Count < 0 || h.Bytes < 0 {
		return false, retentionUnavailable("carried-lease file %s has a malformed, unknown or misplaced header", f.path)
	}
	if canon, err := json.Marshal(h); err != nil || !bytes.Equal(canon, raw) {
		return false, retentionUnavailable("carried-lease file %s has a noncanonical header", f.path)
	}
	want, ok := dsegHexHash(h.Digest)
	if !ok {
		return false, retentionUnavailable("carried-lease file %s has a malformed digest", f.path)
	}
	if h.Count > dcarryMaxLeases {
		return false, retentionUnavailable("carried-lease file %s carries %d leases, over the %d-lease harvest bound",
			f.path, h.Count, dcarryMaxLeases)
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(dcarryFormat))
	_, _ = digest.Write([]byte{0})
	n, size := 0, int64(0)
	for sc.Scan() {
		line := sc.Bytes()
		n++
		size += int64(len(line)) + 1
		if n > h.Count || size > h.Bytes {
			return false, retentionUnavailable("carried-lease file %s runs past its header", f.path)
		}
		_, _ = digest.Write(line)
		_, _ = digest.Write([]byte{'\n'})
		class, reason, retains := f.lines(line)
		if !retains {
			continue
		}
		truncated, err := s.harvestTokens(bytes.NewReader(line), class, reason, into, budget)
		if err != nil || truncated {
			return truncated, err
		}
	}
	if err := sc.Err(); err != nil {
		return false, retentionUnavailable("read carried leases %s: %v", f.path, err)
	}
	var got [32]byte
	digest.Sum(got[:0])
	if n != h.Count || size != h.Bytes || got != want {
		return false, retentionUnavailable("carried-lease file %s does not match its header", f.path)
	}
	return false, nil
}

// harvestTokens walks one JSON token stream, adding every hash-shaped string it finds under the
// given class and reason.
//
// json.Decoder.Token streams through CONCATENATED top-level values, so one decoder handles a
// single-document .json and a many-document .jsonl identically. A CLEAN end of stream (io.EOF) is the
// normal terminator and keeps what was collected. Any OTHER decode error is a torn or malformed
// retention document: the tokens after the break — which may name live references — were never read,
// so treating the file as fully harvested would sweep them as absent. Such a stream halts the pass
// with errRetentionRootsUnavailable rather than silently truncating the live set (main's durable-data
// adjudication). This build never repairs the file.
func (s *FSStore) harvestTokens(
	r io.Reader, class RetentionClass, reason string, into map[core.Hash]RetentionRoot, budget *gcBudget,
) (bool, error) {
	dec := json.NewDecoder(r)
	for {
		if truncated, budgetErr := budget.spent(); budgetErr != nil || truncated {
			return truncated, budgetErr
		}
		tok, tokErr := dec.Token()
		if tokErr != nil {
			if errors.Is(tokErr, io.EOF) {
				return false, nil // the clean end of the token stream
			}
			return false, fmt.Errorf("%w: parse retention json (%s): %w", errRetentionRootsUnavailable, reason, tokErr)
		}
		str, ok := tok.(string)
		if !ok || !hashToken.MatchString(str) {
			continue
		}
		h, perr := core.ParseHash(str)
		if perr != nil {
			continue
		}
		if _, seen := into[h]; seen {
			continue
		}
		into[h] = RetentionRoot{Hash: h, Class: class, Reason: reason}
	}
}

// outcomeLog appends per-root outcomes to a report under a bound.
//
// The bound is on the LIST, never on the counters: a 50 000-root store must not materialize 50 000
// records to answer "what did this pass decide", but "how many were expired" must still be exact,
// or a capped list would quietly become a silent drop - the one thing SP-20 M1-03 forbids.
type outcomeLog struct {
	max int
	rep *GCReport
}

func newOutcomeLog(p GCPolicy, rep *GCReport) *outcomeLog {
	max := p.MaxOutcomes
	if max == 0 {
		max = defaultMaxOutcomes
	}
	return &outcomeLog{max: max, rep: rep}
}

// add records one outcome, marking the list truncated once it is full.
func (o *outcomeLog) add(rec RootOutcome) {
	if o.max < 0 || len(o.rep.Outcomes) >= o.max {
		o.rep.OutcomesTruncated = true
		return
	}
	o.rep.Outcomes = append(o.rep.Outcomes, rec)
}

// applyQuota sheds in-window content until the store is back under its size quota.
//
// Three rules make this safe. It may only take roots the RETENTION WINDOW alone was holding
// (markResult.soft); a lease, pending write, checkpoint, pin, evidence reference, delta base or
// rollback record is off limits and is reported as unsafe-to-collect instead. It takes the OLDEST
// first, so a quota degrades the store's history from the far end rather than at random. And when
// it cannot get under the quota that way it says so - GCReport.QuotaExceeded - rather than reaching
// for something it may not have.
//
// The per-root estimate is canonical bytes scaled by the store's observed compression ratio,
// because the quota is measured in COMPRESSED on-disk bytes and a root's index entry records
// uncompressed ones. It is an estimate and is reported as one: GCReport.BytesFreed, measured by the
// sweep, is the truth.
func (s *FSStore) applyQuota(m *markResult, quota int64, rep *GCReport, outcomes *outcomeLog) {
	if quota <= 0 {
		return
	}
	s.mu.RLock()
	onDisk := s.bytesOnDisk
	s.mu.RUnlock()

	rep.QuotaBytes, rep.QuotaBytesBefore = quota, onDisk
	if onDisk <= quota {
		return
	}
	need := onDisk - quota
	rep.QuotaTargetBytes = need

	scale := 1.0
	if m.total > 0 {
		scale = float64(onDisk) / float64(m.total)
	}

	type candidate struct {
		hash  core.Hash
		ts    core.UnixMilli
		bytes int64
	}
	cands := make([]candidate, 0, len(m.soft))
	s.mu.RLock()
	for h := range m.soft {
		var ts core.UnixMilli
		if e, ok := s.rootIndex[h]; ok {
			ts = e.TS
		}
		cands = append(cands, candidate{hash: h, ts: ts, bytes: m.size[h]})
	}
	s.mu.RUnlock()
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].ts != cands[j].ts {
			return cands[i].ts < cands[j].ts
		}
		return cands[i].hash.String() < cands[j].hash.String()
	})

	var freed int64
	for _, c := range cands {
		if freed >= need {
			break
		}
		delete(m.liveRoots, c.hash)
		delete(m.soft, c.hash)
		m.deadRoots = append(m.deadRoots, c.hash)
		m.evicted[c.hash] = struct{}{}
		freed += int64(float64(c.bytes) * scale)
		rep.QuotaEvicted++
		outcomes.add(RootOutcome{
			Root: c.hash, Result: RootQuotaEvicted, Bytes: c.bytes,
			Reason: fmt.Sprintf("store is over its %d-byte quota by %d; evicted oldest-first inside the retention window", quota, need),
		})
	}
	if freed < need {
		rep.QuotaExceeded = true
		blockers := make([]core.Hash, 0, len(m.hard))
		for h := range m.hard {
			blockers = append(blockers, h)
		}
		sort.Slice(blockers, func(i, j int) bool { return blockers[i].String() < blockers[j].String() })
		for _, h := range blockers {
			r := m.hard[h]
			rep.Unsafe++
			outcomes.add(RootOutcome{
				Root: h, Result: RootUnsafeToCollect, Class: r.Class, Bytes: m.size[h],
				Reason: "quota shortfall, but this root is " + r.Reason,
			})
		}
		s.log.Warn("store: gc could not bring the store under its quota without collecting a retained root",
			"quota", quota, "onDisk", onDisk, "estimatedFreed", freed)
	}
	s.rebuildLiveChunks(m)
}

// rebuildLiveChunks recomputes the live chunk set after the quota removed roots from it.
//
// It is a full recompute rather than a subtraction on purpose: chunks are SHARED, so an evicted
// root's chunk may still belong to a root that stayed, and subtracting would delete content a live
// root still points at.
func (s *FSStore) rebuildLiveChunks(m *markResult) {
	live := make(map[core.Hash]struct{}, len(m.liveChunks))
	for h := range m.harvested {
		live[h] = struct{}{}
	}
	s.mu.RLock()
	for h := range m.liveRoots {
		if e, ok := s.rootIndex[h]; ok {
			for _, c := range e.Root.Chunks {
				live[c.Hash] = struct{}{}
			}
		}
	}
	s.mu.RUnlock()
	m.liveChunks = live
}

// recordOutcomes writes one explicit result per root the pass decided about.
//
// Retained roots come first, so a bounded list shows what was KEPT and why before it shows what
// went - the direction an operator reads a collection report in. Quota evictions were already
// recorded by applyQuota and are skipped here rather than reported twice under two results.
func (s *FSStore) recordOutcomes(m *markResult, rep *GCReport, outcomes *outcomeLog) {
	hard := make([]core.Hash, 0, len(m.hard))
	for h := range m.hard {
		hard = append(hard, h)
	}
	sort.Slice(hard, func(i, j int) bool { return hard[i].String() < hard[j].String() })
	for _, h := range hard {
		r := m.hard[h]
		rep.Retained++
		outcomes.add(RootOutcome{
			Root: h, Result: RootRetained, Class: r.Class, Reason: r.Reason, Bytes: m.size[h],
		})
	}

	soft := make([]core.Hash, 0, len(m.soft))
	for h := range m.soft {
		soft = append(soft, h)
	}
	sort.Slice(soft, func(i, j int) bool { return soft[i].String() < soft[j].String() })
	for _, h := range soft {
		rep.Retained++
		outcomes.add(RootOutcome{Root: h, Result: RootRetained, Reason: m.soft[h], Bytes: m.size[h]})
	}

	dead := append([]core.Hash(nil), m.deadRoots...)
	sort.Slice(dead, func(i, j int) bool { return dead[i].String() < dead[j].String() })
	for _, h := range dead {
		if _, evicted := m.evicted[h]; evicted {
			continue
		}
		rep.Expired++
		outcomes.add(RootOutcome{
			Root: h, Result: RootExpired, Bytes: m.size[h],
			Reason: "outside the retention window and referenced by no lease, pending write, checkpoint, pin, evidence record, delta base or rollback record",
		})
	}
}

// expirePendingMarkers retires pending-write markers whose Put never completed.
//
// A crash leaves a marker behind by design, and without an expiry the registry would grow forever
// and hold its objects live forever with it. Expiry is a VISIBLE outcome: the marker's root is
// reported as expired with its reason, never removed quietly. It runs after the sweep, so this
// pass still retained the objects the marker named and the next pass is the one that collects them.
//
// A disabled day window (days < 0) expires nothing: "collect everything" is about the retention
// window, and an in-flight write is not covered by it.
func (s *FSStore) expirePendingMarkers(days int, dryRun bool, rep *GCReport, outcomes *outcomeLog) {
	dir := filepath.Join(s.l.State, pendingWriteDir)
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		return
	}
	cutoff := s.deps.Clock.Now().Add(-time.Duration(days) * hoursPerDay * time.Hour)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != pendingWriteSuffix {
			continue
		}
		rep.PendingWrites++
		if days < 0 {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		root := pendingMarkerRoot(p)
		if !dryRun {
			if rmErr := os.Remove(paths.Long(p)); rmErr != nil {
				s.log.Debug("store: could not remove an expired pending marker", "path", e.Name(), "err", rmErr)
				continue
			}
		}
		rep.PendingExpired++
		outcomes.add(RootOutcome{
			Root: root, Result: RootExpired, Class: RetentionPending,
			Reason: "pending write was never rooted and is past the retention window",
		})
	}
}

// pendingMarkerRoot reads the root a pending marker names, reporting the zero hash for a marker
// that is unreadable or was torn by the very crash it records.
func pendingMarkerRoot(p string) core.Hash {
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return core.Hash{}
	}
	var w pendingWire
	if json.Unmarshal(b, &w) != nil {
		return core.Hash{}
	}
	h, err := core.ParseHash(w.Root)
	if err != nil {
		return core.Hash{}
	}
	return h
}

// liveDigest is the domain-separated digest of the sorted live chunk set.
func liveDigest(live map[core.Hash]struct{}) string {
	return core.HashBytes(gcLiveDomain, sortedLiveBytes(live)).String()
}

// sortedLiveBytes renders the live set as sorted, concatenated 32-byte hashes — the same form
// writeLiveSet persists, so the digest and the file always agree.
func sortedLiveBytes(live map[core.Hash]struct{}) []byte {
	all := make([]core.Hash, 0, len(live))
	for h := range live {
		all = append(all, h)
	}
	sort.Slice(all, func(i, j int) bool {
		for k := range all[i] {
			if all[i][k] != all[j][k] {
				return all[i][k] < all[j][k]
			}
		}
		return false
	})
	buf := make([]byte, 0, len(all)*len(core.Hash{}))
	for _, h := range all {
		buf = append(buf, h[:]...)
	}
	return buf
}

// writeLiveSet persists the live chunk set so a resumed sweep can binary-search it without redoing
// the mark.
func (s *FSStore) writeLiveSet(live map[core.Hash]struct{}) error {
	return paths.WriteAtomic(filepath.Join(s.l.State, gcLiveFile), sortedLiveBytes(live), 0o600)
}

// loadGCState reads the persisted cursor, reporting whether it may be resumed from.
//
// A pass resumes ONLY when the live digest it just computed matches the one the interrupted pass
// recorded. Otherwise new roots have appeared since, and continuing from the old cursor would sweep
// the tail of the object tree against a stale live set — the one way this collector could delete
// something reachable.
func (s *FSStore) loadGCState(digest string) (gcState, bool) {
	b, err := os.ReadFile(paths.Long(filepath.Join(s.l.State, gcStateFile)))
	if err != nil {
		return gcState{}, false
	}
	var st gcState
	if err := json.Unmarshal(b, &st); err != nil {
		return gcState{}, false
	}
	if st.LiveDigest != digest {
		s.log.Debug("store: gc live set changed since the interrupted pass; restarting the mark phase")
		return gcState{}, false
	}
	return st, true
}

// saveGCState persists the cursor so the next pass can continue.
func (s *FSStore) saveGCState(digest, cursor string, rep GCReport) error {
	st := gcState{
		V: indexRecordVersion, Phase: "sweep", Cursor: cursor, LiveDigest: digest,
		Roots: rep.Roots, Scanned: rep.ScannedObjects, Deleted: rep.DeletedObjects,
		Freed: rep.BytesFreed, Started: s.now(),
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return paths.WriteAtomic(filepath.Join(s.l.State, gcStateFile), b, 0o600)
}

// clearGCState removes the cursor after a pass finishes.
func (s *FSStore) clearGCState() {
	_ = os.Remove(paths.Long(filepath.Join(s.l.State, gcStateFile)))
}

// tombstoneDeadRoots retires exactly the roots the mark phase found dead at its snapshot.
//
// It deliberately does NOT re-read s.rootIndex: dead is fixed at mark time, so a root published
// after the snapshot cannot be retired by this pass no matter how the phases interleave with
// concurrent writes. Retirement is an APPEND to index/roots.jsonl, never a rewrite of the line
// that created the root (Qompack.md §7.4), so the file stays append-only and the original record
// remains readable.
func (s *FSStore) tombstoneDeadRoots(ctx context.Context, dead []core.Hash) error {
	sort.Slice(dead, func(i, j int) bool { return dead[i].String() < dead[j].String() })
	for i, h := range dead {
		if i%gcCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := s.appendGCTombstone(h); err != nil {
			return err
		}
	}
	return nil
}

// sweepArgs bundles one sweep's inputs, so the sweep signature stays readable.
type sweepArgs struct {
	live     map[core.Hash]struct{}
	dryRun   bool
	deadline time.Time
	started  time.Time
	resume   string
	rep      *GCReport
}

// sweep walks objects/ in lexicographic order and collects everything the live set does not hold.
//
// The walk order is what makes the pass resumable: a cursor is only meaningful if the next run
// visits the same objects in the same sequence. This loop and the mark phase's harvest — the pass's
// two disk-proportional loops — check the deadline and the context every gcCheckEvery items, and an
// expired deadline returns a cursor rather than an error: a truncated GC is a normal outcome of
// idle work, not a failure. (The mark's in-memory index walks check ctx only; see mark.)
//
// The two phases differ in what truncation MEANS, which is why only this one yields a cursor. A
// truncated sweep has a complete live set and has simply not finished walking objects/, so it
// resumes; a truncated mark has an incomplete live set and cannot be resumed or swept against at
// all, so GC ends that pass with nothing collected (see mark).
func (s *FSStore) sweep(ctx context.Context, a sweepArgs) (cursor string, truncated bool, err error) {
	base := paths.Long(s.l.Objects)
	seen := 0
	// lastDone is the last object this sweep FINISHED with, and it is what the cursor records.
	//
	// Recording the object the walk was about to start instead would lose exactly one object per
	// resumption: the resume test skips everything at or before the cursor, so an object named as
	// the stopping point but never processed would be skipped forever.
	lastDone := ""

	walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable subtree must not abort the whole sweep
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if a.resume != "" && rel <= a.resume {
			return nil
		}

		seen++
		if seen%gcCheckEvery == 0 {
			if cerr := ctx.Err(); cerr != nil {
				cursor, err = lastDone, cerr
				return filepath.SkipAll
			}
			if !a.deadline.IsZero() && time.Now().After(a.deadline) {
				cursor, truncated = lastDone, true
				return filepath.SkipAll
			}
		}

		a.rep.ScannedObjects++
		h, ok := objectHashOf(rel)
		if !ok {
			return nil
		}
		if _, live := a.live[h]; live {
			lastDone = rel
			return nil
		}

		info, ierr := d.Info()
		// The freshness guard, paired with mark's snapshot semantics: an object written after
		// the pass started cannot be in the live set no matter how live it is, because the live
		// set predates it. Sparing everything younger than the pass leaves such objects for the
		// next pass, whose mark will see their roots. An unreadable Info errs on the side of
		// sparing: skipping one object for one pass is free, deleting a live one is not.
		if ierr != nil || info.ModTime().After(a.started) {
			lastDone = rel
			return nil
		}
		size := info.Size()
		if !a.dryRun {
			if rmErr := os.Remove(p); rmErr != nil {
				// A Windows sharing violation loses one object this pass, never the session.
				s.log.Debug("store: gc could not remove an object", "path", rel, "err", rmErr)
				s.count("store.gc.skipped", 1)
				return nil
			}
		}
		a.rep.DeletedObjects++
		a.rep.BytesFreed += size
		lastDone = rel
		return nil
	})
	if walkErr != nil && err == nil && !os.IsNotExist(walkErr) {
		return cursor, truncated, fmt.Errorf("store: gc sweep: %w", walkErr)
	}
	return cursor, truncated, err
}

// objectHashOf recovers an object's hash from its path under objects/, tolerating both the
// compressed and the bare filename.
func objectHashOf(rel string) (core.Hash, bool) {
	name := filepath.Base(rel)
	if ext := filepath.Ext(name); ext == objectSuffix {
		name = name[:len(name)-len(ext)]
	}
	h, err := core.ParseHash(name)
	if err != nil {
		return core.Hash{}, false
	}
	return h, true
}
