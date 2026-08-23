package store

import (
	"context"
	"encoding/json"
	"fmt"
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
	if err := s.use(); err != nil {
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
	liveChunks, liveRoots, deadRoots, markTruncated, err := s.mark(ctx, days, sessions, deadline)
	if err != nil {
		return GCReport{}, err
	}
	if markTruncated {
		// A truncated mark produced an incomplete live set, and sweeping against one would delete
		// live objects — every reference the phase never reached looks dead. So the pass ends here
		// with nothing retired and nothing swept, and neither the live set nor a resume cursor is
		// persisted: a cursor recorded now would let the NEXT pass resume a sweep against this
		// incomplete set, which is the same data loss one run later.
		s.log.Warn("store: gc mark phase ran out of its deadline; nothing was collected this pass",
			"deadline", p.Deadline)
		return GCReport{Truncated: true, Duration: time.Since(started)}, nil
	}

	digest := liveDigest(liveChunks)
	if err := s.writeLiveSet(liveChunks); err != nil {
		s.log.Warn("store: could not persist the gc live set; this pass will not be resumable", "err", err)
	}

	rep := GCReport{LiveObjects: len(liveChunks), Roots: len(liveRoots)}
	prior, resuming := s.loadGCState(digest)
	if resuming {
		rep.ScannedObjects, rep.DeletedObjects, rep.BytesFreed = prior.Scanned, prior.Deleted, prior.Freed
	}

	if !p.DryRun {
		if err := s.tombstoneDeadRoots(ctx, deadRoots); err != nil {
			return rep, err
		}
	}

	cursor, truncated, err := s.sweep(ctx, sweepArgs{
		live:     liveChunks,
		dryRun:   p.DryRun,
		deadline: deadline,
		started:  started,
		resume:   resumeCursor(prior, resuming),
		rep:      &rep,
	})
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

// gcBudget is the ctx-and-deadline pair every unbounded-length phase of a GC pass is checked
// against, so the mark phase and the sweep answer to the same two levers in the same way.
//
// Both are needed and they mean different things: ctx is how the CALLER cancels a pass, and an
// expired ctx is an error; Deadline is the latency budget the idle scheduler granted, and running
// out of it is a normal outcome that returns Truncated. Checking only every gcCheckEvery items
// keeps a time.Now() off the per-item path.
type gcBudget struct {
	ctx      context.Context
	deadline time.Time
	n        int
}

func newGCBudget(ctx context.Context, deadline time.Time) *gcBudget {
	return &gcBudget{ctx: ctx, deadline: deadline}
}

// spent counts one item and reports whether the phase must stop.
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

// mark computes the live chunk set and the live root set.
//
// "Whichever is longer" (Qompack.md §8.2) is implemented as a DISJUNCTION: an entry is in-window
// when it is inside the day window OR its session is among the most recent ones. An EPHEMERAL root
// is never in-window by the age clause — retrieval spam is reclaimable precisely because objects
// are content-addressed, so a chunk it shares with a real tool result is still held alive by that
// result (Qompack.md §8.7).
//
// It is bounded by the same budget the sweep is, and until the 2026-08-22 audit it was not: it took
// neither ctx nor deadline, so harvestHashes streamed every checkpoint, pin and elimination file
// token by token with nothing able to stop it, on a phase whose cost grows with the project's whole
// history. The sweep's own comment claimed otherwise.
//
// A truncated mark yields an INCOMPLETE live set, which is the one thing a collector must never
// sweep against — every unvisited reference would look dead. So truncation here stops the pass at
// its caller rather than being carried forward, and neither the live set nor a resume cursor is
// persisted from it.
func (s *FSStore) mark(ctx context.Context, days, sessions int, deadline time.Time) (
	liveChunks, liveRoots map[core.Hash]struct{}, deadRoots []core.Hash, truncated bool, err error,
) {
	budget := newGCBudget(ctx, deadline)
	harvested, truncated, err := s.harvestHashes(budget)
	if err != nil || truncated {
		return nil, nil, nil, truncated, err
	}
	recent := s.recentSessionSet(sessions)

	var cutoff core.UnixMilli
	if days >= 0 {
		cutoff = core.UnixMilli(s.deps.Clock.Now().Add(-time.Duration(days) * hoursPerDay * time.Hour).UnixMilli())
	}
	inAgeWindow := func(ts core.UnixMilli) bool { return days >= 0 && ts >= cutoff }

	liveRoots = make(map[core.Hash]struct{})
	liveChunks = make(map[core.Hash]struct{}, len(harvested))
	// A harvested hash may name a root OR a chunk; nothing in the schemas distinguishes them, so
	// it is held live as both.
	for h := range harvested {
		liveChunks[h] = struct{}{}
	}

	// The index walks below run under one read lock and are released through this named unlock on
	// every path, including the budgeted early returns.
	unlocked := false
	s.mu.RLock()
	defer func() {
		if !unlocked {
			s.mu.RUnlock()
		}
	}()
	stop := func() bool {
		t, e := budget.spent()
		if e != nil {
			truncated, err = false, e
			return true
		}
		if t {
			truncated = true
			return true
		}
		return false
	}

	for h, e := range s.rootIndex {
		if stop() {
			return nil, nil, nil, truncated, err
		}
		if _, ok := harvested[h]; ok {
			liveRoots[h] = struct{}{}
			continue
		}
		if !e.Eph && inAgeWindow(e.TS) {
			liveRoots[h] = struct{}{}
		}
	}
	for _, rec := range s.toolUse {
		if stop() {
			return nil, nil, nil, truncated, err
		}
		if rec.Root.IsZero() {
			continue
		}
		// The ephemeral exclusion applies HERE too, not only on the root path above. §8.2: "an
		// ephemeral root is never in-window by the age clause; only the session clause and an
		// explicit root reference keep it alive." Reading the age clause without consulting Eph
		// made the documented property void on the main path rather than on an edge case — every
		// retrieval result is recorded as a tool_use, so every ephemeral root was age-live through
		// this loop no matter what the root path decided about it.
		eph := false
		if e, ok := s.rootIndex[rec.Root]; ok {
			eph = e.Eph
		}
		if (!eph && inAgeWindow(rec.TS)) || (sessions >= 0 && recent[rec.Session]) {
			liveRoots[rec.Root] = struct{}{}
		}
	}
	for _, hist := range s.fileHist {
		for _, v := range hist {
			if stop() {
				return nil, nil, nil, truncated, err
			}
			if inAgeWindow(v.TS) {
				liveRoots[v.Root] = struct{}{}
			}
		}
	}
	for h := range liveRoots {
		if stop() {
			return nil, nil, nil, truncated, err
		}
		if e, ok := s.rootIndex[h]; ok {
			for _, c := range e.Root.Chunks {
				liveChunks[c.Hash] = struct{}{}
			}
		}
	}
	// The dead set is fixed HERE, under the same read lock as the live set, and is the only
	// thing the tombstone phase may retire. Re-deriving it later from a fresh read of
	// s.rootIndex opens a window: a root published between this snapshot and the tombstone
	// phase is in the index but not in the live set, and would be retired seconds after it
	// was written (found by V2-VERIFY's §4.7 authoring).
	deadRoots = make([]core.Hash, 0, len(s.rootIndex))
	for h := range s.rootIndex {
		if stop() {
			return nil, nil, nil, truncated, err
		}
		if _, ok := liveRoots[h]; !ok {
			deadRoots = append(deadRoots, h)
		}
	}
	s.mu.RUnlock()
	unlocked = true

	return liveChunks, liveRoots, deadRoots, false, nil
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

// gcRootFiles returns every file whose hash references keep content alive.
//
// The files are read STRUCTURALLY rather than through internal/checkpoint, internal/pins or
// internal/negknow: all three import store, so importing them back would be an import cycle
// (00-ARCHITECTURE.md §3.2). A missing file is never an error — waves 3 to 5 have not shipped
// these producers yet, and a store that refused to collect until they did would grow without bound.
func (s *FSStore) gcRootFiles() []string {
	out := []string{
		filepath.Join(s.l.Pins, "invariants.jsonl"),
		filepath.Join(s.l.Records, "eliminations.jsonl"),
	}
	entries, err := os.ReadDir(paths.Long(s.l.Checkpoints))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch filepath.Ext(e.Name()) {
		case ".json", ".jsonl":
			out = append(out, filepath.Join(s.l.Checkpoints, e.Name()))
		}
	}
	return out
}

// harvestHashes collects every hash-shaped string token from the GC root files.
//
// This is the unbounded half of the mark phase: its cost is the size of every checkpoint, pin and
// elimination file a project has ever written, streamed token by token. It answers to the budget
// for that reason, and a truncated harvest is reported rather than returned as a short set — a
// partial harvest is a live set with references missing from it.
func (s *FSStore) harvestHashes(budget *gcBudget) (map[core.Hash]struct{}, bool, error) {
	out := make(map[core.Hash]struct{})
	for _, p := range s.gcRootFiles() {
		truncated, err := s.harvestFile(p, out, budget)
		if err != nil || truncated {
			return nil, truncated, err
		}
	}
	return out, false, nil
}

// harvestFile walks one file's JSON tokens, adding every hash-shaped string it finds.
//
// json.Decoder.Token streams through CONCATENATED top-level values, so one decoder handles a
// single-document .json and a many-document .jsonl identically. A decode error stops the walk but
// keeps what was already collected: a half-written final line must not cost the whole file's
// references.
func (s *FSStore) harvestFile(p string, into map[core.Hash]struct{}, budget *gcBudget) (bool, error) {
	f, err := os.Open(paths.Long(p))
	if err != nil {
		return false, nil // missing is normal; see gcRootFiles
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	for {
		if truncated, budgetErr := budget.spent(); budgetErr != nil || truncated {
			return truncated, budgetErr
		}
		tok, tokErr := dec.Token()
		if tokErr != nil {
			return false, nil
		}
		str, ok := tok.(string)
		if !ok || !hashToken.MatchString(str) {
			continue
		}
		if h, perr := core.ParseHash(str); perr == nil {
			into[h] = struct{}{}
		}
	}
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
// visits the same objects in the same sequence. Both this loop and the mark phase check the
// deadline and the context every gcCheckEvery items, and an expired deadline returns a cursor
// rather than an error — a truncated GC is a normal outcome of idle work, not a failure.
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
