package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/tokens"
)

// PreCompact seals after the session's own captures (V6 close-out D53(c)).
//
// A capture can reach the daemon after the PreCompact that follows it. On a disk whose durable writes
// are slower than runtime.budgets.l0IngestMs a hook waits only its ACK deadline and then hands the
// capture to its client spool, and after three over-budget windows the daemon switches the hot path
// to spool submode, from which point no tool result, prompt or Stop of the session reaches the daemon
// except through a client spool: the hook clients stop connecting for them at all (ipc client.go Send
// step 3). Only a drain reads a client spool, and the drains a session in spool submode meets are the
// client-spool watcher, which a served request or (in spool submode) Run's idle tick kicks and which
// takes two intervals to pass a spool, and the idle drain, two quiet minutes away. PreCompact is not
// a hot-path op, so the compaction does reach the daemon, and the route sealed at once: the
// checkpoint the host's compaction then depends on lacked the session's newest tool results, and so
// did the rehydration built from it.
//
// So before it seals, the route settles the session the way a session end does (settleSession,
// C1.13): it waits for the session's ingest lane to publish every arrival leased before the
// PreCompact, replays the client spools that hold this session's captures (only those: another
// session's backlog, older in host order, must not spend this session's bound), waits for the lane
// once more for the successors the replay unparked and for a live delivery still publishing, and
// replays again while that can advance (replayOwnSpools). All of it, the looks at the spool included,
// runs inside the bound below (settleBeforeSeal), and a capture still unpublished when the bound
// expires is counted in the checkpoint's drop report (checkpoint.DropKindUnreplayedCapture), the
// newest tool results among them named by tool_use_id (DropKindUnreplayedToolResult) within their
// share of the checkpoint's budget, which the rehydration's section 7 carries: never silently missing.
//
// A client spool's name does not say whose captures it holds, so the settle learns that from the
// lines, through the daemon's index of the spools' line heads (spool_heads.go), which reads a file
// once per version: the settle indexes what it reads, and the client-spool watcher indexes every
// spool its pass leaves. A session with no client spool of its own and nothing still publishing pays
// one listing of the spool directory, lookups in that index and two in the journal; it reads only
// the client spools the index does not yet hold (a spool written since the watcher last passed, or
// any when no watcher has run), each once, and only while the bound lasts.
//
// The settle's replay takes the drain's mutex within the bound (DrainClientSpoolsWithin), so a
// client-spool watcher pass, which covers every session's spools under that mutex, would make it wait.
// The PreCompact's own request therefore kicks the watcher only after the seal (noteServed,
// handleCheckpoint): it cannot start a pass that holds the mutex against its own settle. A pass
// already running for another reason (an earlier request's kick, the idle tick, a drain the lanes ask
// for) is waited for, within the bound, and what the bound then leaves is named in the drop report
// and replayed afterwards (D55): nothing is lost.

// precompactSettleBound is how long the PreCompact route may spend settling the session before it
// seals: B-E (runtime.budgets.checkpointFinalizeMs, the gated p99 for PreCompact entry to exit) less
// the seal's own worst case inside B-E (checkpoint.MaxPreCompactWindow). The settle is timed inside
// B-E with the seal, and every part of it that grows with the spool (its looks at the client spools,
// its waits and its replay) runs against this bound, so a settle that takes its whole bound still
// leaves the seal the window it always had. What runs past it is fixed work, not the backlog's: a
// file read already under way, the spool listings, the drain's progress file and the pricing of the
// names (settleBeforeSeal lists it). 500 ms with the defaults (2000 - 1500). A B-E configured at or
// below the seal's window leaves no settle at all: the route seals at once and names what is
// unpublished, from what the spool index already holds.
//
// It is far inside the nested PreCompact deadlines (wire_checkpoint.go precompactDeadlineSlack:
// 14 s for the daemon inside the client's 15 s inside the host's 20 s), and it is a bound on a wait,
// not a latency target: a session with nothing to settle does not wait at all. It is a hard bound:
// the replay's line in flight when it expires is cancelled, not finished (DrainClientSpoolsWithin
// says why, and what that costs on the slowest disks).
func precompactSettleBound(cfg config.Config) time.Duration {
	be := time.Duration(cfg.Runtime.Budgets.CheckpointFinalizeMs) * time.Millisecond
	return max(be-checkpoint.MaxPreCompactWindow, 0)
}

// unreplayedNamesBudgetDivisor sets the share of checkpoint.budgetTokens the per-tool-result names of
// the unreplayed report may take: one twentieth (5 %), 600 tokens of the default 12000, which names
// the newest dozen or so tool results (16 with the test fixture's short ids and the identity
// estimator; an indented entry costs 35 to 40 tokens before calibration). Every drop entry is part
// of the checkpoint document Truncate measures, and Truncate never cuts the drop report: it cuts the
// narrative and then the pointers to make room for it. Named without a bound, a spool-submode backlog
// of 300 tool results took 11147 of the default 12000 tokens
// (TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare's fixture, before this bound)
// and cost the checkpoint its pointers to the session's older work. The names are priced by the
// seal's own estimator, the project-calibrated one Truncate measures the document with
// (sealReport.drops, through checkpoint.PreCompactInput.PricedDrops), so the share holds under any
// calibration factor: a checkpoint whose pointers fit within 95 % of its budget keeps them all,
// whatever the backlog. The summary entry still counts every capture left, so nothing becomes
// silent; a tool result that is counted but not named is in the store once the daemon replays it,
// and recall finds it then. This is an owner number (V6 close-out D53(c), approved in D55): too large
// and the names cut pointers on a nearly full checkpoint; too small and fewer of the newest tool
// results are named.
const unreplayedNamesBudgetDivisor = 20

// unreplayedNamesAllowance is the token allowance of the per-tool-result names under cfg.
func unreplayedNamesAllowance(cfg config.Config) core.Tokens {
	return core.Tokens(cfg.Checkpoint.BudgetTokens / unreplayedNamesBudgetDivisor)
}

// counterPrecompactSettle counts PreCompact seals that found something of their session to settle
// first, or could not tell within the bound: a client spool holding one of its captures, an arrival
// leased before the PreCompact and not yet published, or a client spool the bound left unread.
const counterPrecompactSettle = "precompact_settle"

// counterPrecompactUnreplayed counts the captures a PreCompact seal reported as unreplayed: still in a
// client spool, the session's lane, the ring or the WAL alone when the settle's bound expired.
const counterPrecompactUnreplayed = "precompact_unreplayed_captures"

// counterPrecompactSpoolReads counts the client spool files PreCompact settles read: the files the
// spool index (spool_heads.go) did not already hold at their listed size and time when a settle's
// own look reached them. The watcher's indexing, and another session's settle running at the same
// time, read through the same index and are not counted here.
const counterPrecompactSpoolReads = "precompact_settle_spool_reads"

// unreplayedDetailFormat is the summary drop entry's detail, as the checkpoint and section 7 carry it:
// how many captures were left, of which kinds, how many of the tool results are named, and what that
// means. It gives no cause: the captures it counts were in a hook client spool, the session's lane,
// the ring, the WAL alone or a predecessor's lease (unreplayedCaptures), and only a spooled one can
// owe its wait to a slow disk, so a cause would be wrong for the others (docs/troubleshooting.md says
// which is which).
const unreplayedDetailFormat = "%d capture(s) of this session (%d tool result(s), %d prompt(s), %d other) were " +
	"still waiting to be replayed into the store when this checkpoint was sealed; the newest %d tool " +
	"result(s) are named by tool_use_id; nothing is lost: the daemon replays them, and recall or expand " +
	"finds them then"

// unknownKindClauseFormat is added to the summary's detail when some of the captures it counts are
// known to the settle only by their delivery lease (unreplayedCaptures): leased arrivals of the session
// the daemon holds in its WAL alone with no request in memory, one a daemon before this one accepted or
// one past the record of leased jobs no lane holds (delivery_unheld.go). The lease does not say what
// the capture was, so they are counted as other and not named; the daemon replays them from the WAL.
const unknownKindClauseFormat = "; %d of the other capture(s) are known here only by their delivery lease, " +
	"not by their kind"

// unreadSpoolsClauseFormat is added to the summary's detail when the settle's looks left a client
// spool they had to look at unread: the bound ended before they could read it, or reading it failed
// (a sharing violation, an anti-virus lock, an I/O error; spool_heads.go). Those files were listed but
// not read, so the summary cannot count this session's captures in them, if there are any.
const unreadSpoolsClauseFormat = "; %d hook client spool file(s) could not be read within the bound or " +
	"failed to read, so this session's captures in them, if any, are not counted here"

// sealReport is what the settle hands the seal: the captures it left unreplayed, how many client
// spools it could not read within its bound or failed to read, and the names' share of the
// checkpoint's budget. The seal prices the names with its own estimator (drops), the one its Truncate
// measures the document with.
type sealReport struct {
	left      []pendingCapture
	unread    int
	allowance core.Tokens
}

// drops is the report's drop entries, the names priced with est (unreplayedDrops). A nil report has
// none.
func (r *sealReport) drops(est tokens.Estimator) []checkpoint.DropEntry {
	if r == nil {
		return nil
	}
	drops := unreplayedDrops(r.left, r.allowance, est)
	if r.unread > 0 {
		drops[0].Detail += fmt.Sprintf(unreadSpoolsClauseFormat, r.unread)
	}
	return drops
}

// pricer is drops in the shape checkpoint.PreCompactInput.PricedDrops takes; nil for a nil report.
func (r *sealReport) pricer() func(tokens.Estimator) []checkpoint.DropEntry {
	if r == nil {
		return nil
	}
	return r.drops
}

// sealReportKey carries the settle's report to the bound PreCompact seam (wire_checkpoint.go), beside
// the event, which has no room for it.
type sealReportKey struct{}

func withSealReport(ctx context.Context, r *sealReport) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, sealReportKey{}, r)
}

// sealReportOf returns the report the PreCompact route's settle left for the seal, or nil.
func sealReportOf(ctx context.Context) *sealReport {
	r, _ := ctx.Value(sealReportKey{}).(*sealReport)
	return r
}

// pendingCapture is what the settle knows of a capture it may have to report: enough to count it by
// kind, name a tool result, order the names newest first and find its spool file, and nothing of its
// payload.
type pendingCapture struct {
	op        ipc.Op
	nonce     string
	toolUseID core.ToolUseID
	ts        core.UnixMilli
	// file is the base name of the client spool holding it; empty for one in the session's lane.
	file string
}

// captureOf is what the settle keeps of req.
func captureOf(req ipc.Request) pendingCapture {
	c := pendingCapture{op: req.Op, nonce: req.Nonce, ts: req.TS}
	if req.Event != nil {
		c.toolUseID = req.Event.ToolUseID
	}
	return c
}

// settleBeforeSeal settles sess before its PreCompact seal (see above) and returns the report of what
// it could not settle, or nil when nothing is left. at is the PreCompact's hook time: a
// client-spooled capture fired after it does not belong to this compaction.
//
// The bound's deadline starts before anything else is done, and every part of the settle that grows
// with the spool runs against it:
//   - the first look at the client spools (scanClientSpools): a listing, and a read of each file the
//     spool index does not hold at its listed size and time, each started only before the deadline;
//   - the waits for the session's lane and the replays of the files holding its captures, with the
//     looks at those files between replays (replayOwnSpools), which run to the same deadline: no
//     time is held back for the last look, so a first look slowed by other
//     sessions' cold spools leaves the replay whatever it did not use;
//   - the last look, which keeps the first look's result and reads again only the files that look
//     named and the ones listed since it (or left unread by it), again only before the deadline. Most
//     of what it looks at it takes from the spool index: a file the replay released is no longer
//     listed, and one it left is unchanged, since a drain records its progress elsewhere. A file it
//     would have to read once the deadline has passed (one listed since the first look, or one a hook
//     appended to) it counts as unread instead.
//
// What can run past the deadline is fixed work: a file read already under way, the spool listings,
// the drain's progress file and the pricing of the names, which the seal does (namesWithin measures a
// handful of candidate reports, whatever the backlog). A file a look could not read in time, or
// failed to read, is counted in the summary as not read.
func (d *daemon) settleBeforeSeal(ctx context.Context, sess core.SessionID, at core.UnixMilli) *sealReport {
	cfg := d.currentCfg()
	bound := precompactSettleBound(cfg)
	sctx, cancel := context.WithDeadline(ctx, time.Now().Add(bound))
	defer cancel()
	var reads int64 // the files this settle's own looks read
	defer func() {
		if d.m != nil {
			d.m.Counter(counterPrecompactSpoolReads).Add(reads)
		}
	}()

	upTo := d.leasedUpTo(sess)
	first := d.scanClientSpools(sctx, sess, at, nil)
	reads += first.reads
	own := map[string]bool{}
	for _, c := range first.caps {
		own[c.file] = true
	}
	if len(own) == 0 && len(first.unread) == 0 && d.arrivalsSettled(sess, upTo) {
		return nil // nothing of this session's to settle
	}
	if d.m != nil {
		d.m.Counter(counterPrecompactSettle).Add(1)
	}

	// The waits and the replay run to the bound's own deadline. Nothing is held back for the last
	// look: what it reads past the deadline it counts as unread, which keeps the report honest
	// without spending the replay's time on other sessions' files.
	d.awaitArrivals(sctx, sess, upTo)
	if len(own) > 0 && sctx.Err() == nil {
		if dr := d.drain.Load(); dr != nil {
			reads += d.replayOwnSpools(sctx, dr, sess, at, own)
		}
		d.awaitArrivals(sctx, sess, upTo)
	}

	last := d.scanClientSpools(sctx, sess, at, func(base string) bool {
		return own[base] || !first.listed[base] || first.unread[base]
	})
	reads += last.reads
	left := d.unreplayedCaptures(sess, upTo, last.caps)
	if len(left) == 0 && len(last.unread) == 0 {
		return nil
	}
	if d.m != nil {
		d.m.Counter(counterPrecompactUnreplayed).Add(int64(len(left)))
	}
	d.log.Warn("daemon: PreCompact sealed before some of the session's captures were replayed; "+
		"the checkpoint's drop report counts them, and the daemon replays them next",
		"session", string(sess), "captures", len(left), "unread_spools", len(last.unread), "bound", bound.String())
	return &sealReport{left: left, unread: len(last.unread), allowance: unreplayedNamesAllowance(cfg)}
}

// replayOwnSpools replays own, the client spools holding sess's captures, within ctx, and replays
// them again while that can advance. It returns how many client spool files its looks read.
//
// One replay is not always enough (w16d-sealrow). A hook whose ACK deadline lapsed spools a copy of a
// delivery the daemon may still be taking: leased only after the settle looked at the session's leases
// (leasedUpTo), and publishing when the replay meets its copy. The replay leaves the copy to the live
// publication, and the session's later captures to the ordering gate behind it, so it can publish
// nothing with nearly all of the bound left. So after a replay the settle looks at own again (from the
// spool index, unless a hook appended) and, while a capture of sess is left there, waits for the lane
// to publish every arrival of sess up to the earliest capture left, that one included (the live
// delivery a copy stands for), and replays again once every predecessor of that capture is
// acknowledged. Anything else ends the loop: no capture left, a file the look could not read, a
// capture the replay did not lease, a replay that ended early, a predecessor still unpublished when
// the lane went quiet, or a replay that published nothing while held at the same capture as the one
// before, so the loop never spins on a line only a later pass can take.
func (d *daemon) replayOwnSpools(ctx context.Context, dr *drainer, sess core.SessionID, at core.UnixMilli,
	own map[string]bool,
) (reads int64) {
	var held uint64 // the earliest capture left by the last replay that published nothing
	for ctx.Err() == nil {
		// A replay is capture work, as every drain is (D51).
		d.capture.enter()
		n, err := dr.DrainClientSpoolsWithin(ctx, own)
		d.capture.leave()
		if err != nil || ctx.Err() != nil {
			if err != nil && ctx.Err() == nil {
				d.log.Debug("daemon: PreCompact: the client-spool replay before the seal ended early", "err", err)
			}
			return reads
		}
		rest := d.scanClientSpools(ctx, sess, at, func(base string) bool { return own[base] })
		reads += rest.reads
		if len(rest.caps) == 0 || len(rest.unread) > 0 {
			return reads
		}
		earliest, ok := d.earliestLeasedArrival(rest.caps)
		if !ok || (n == 0 && earliest == held) {
			return reads
		}
		if n == 0 {
			held = earliest
		}
		d.awaitArrivals(ctx, sess, earliest+1)
		if delivered, _ := d.sessionDelivered(sess, earliest); !delivered {
			return reads
		}
	}
	return reads
}

// earliestLeasedArrival is the lowest arrival among caps' leases. ok is false when one of them holds
// no lease the journal can read.
func (d *daemon) earliestLeasedArrival(caps []pendingCapture) (uint64, bool) {
	j, err := d.deliveryJournal()
	if err != nil || j == nil {
		return 0, false
	}
	var earliest uint64
	for _, c := range caps {
		l, held, err := j.leaseHeld(c.nonce)
		if err != nil || !held {
			return 0, false
		}
		if earliest == 0 || l.ArrivalSeq < earliest {
			earliest = l.ArrivalSeq
		}
	}
	return earliest, earliest > 0
}

// leasedUpTo is one past sess's newest leased arrival, 0 when the journal cannot say.
func (d *daemon) leasedUpTo(sess core.SessionID) uint64 {
	if j, err := d.deliveryJournal(); err == nil && j != nil {
		if last, ok := j.lastArrival(sess); ok {
			return last + 1
		}
	}
	return 0
}

// arrivalsSettled reports whether every leased arrival of sess before upTo is on the committed
// frontier, from the journal's memory.
func (d *daemon) arrivalsSettled(sess core.SessionID, upTo uint64) bool {
	if upTo <= 1 {
		return true // no leased arrival before the PreCompact, or none the journal can name
	}
	delivered, known := d.sessionDelivered(sess, upTo)
	return known && delivered
}

// awaitArrivals waits, within ctx, until every leased arrival of sess before upTo is on the committed
// frontier, or the session's lane has gone quiet with some still unpublished (their predecessor is in
// a client spool, which only a drain can publish), or ctx ends. It waits on the lane's own settles,
// never on a clock.
func (d *daemon) awaitArrivals(ctx context.Context, sess core.SessionID, upTo uint64) {
	if upTo <= 1 {
		return
	}
	for ctx.Err() == nil {
		if delivered, known := d.sessionDelivered(sess, upTo); delivered || !known {
			return
		}
		changed, _, quiet := d.ing.lanes.watch(sess)
		if quiet {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
		}
	}
}

// spoolScan is one look at the client spools for a session: its captures found there, every client
// spool listed, the ones the look had to read but could not before its context ended, and how many
// it read.
type spoolScan struct {
	caps   []pendingCapture
	listed map[string]bool
	unread map[string]bool
	reads  int64
}

// scanClientSpools looks at the hook client spools for sess's hot-path captures fired at or before
// at that no drain has consumed and whose delivery is not already on the committed frontier through
// another copy (a late ACK's spool copy of a delivery the lane published). It looks into the files
// look accepts, all of them when look is nil. Their heads come from the spool index
// (spool_heads.go); a file the index does not hold at its listed size and time is read, but only
// while ctx has not ended, and is otherwise reported unread. With no drainer nothing could replay
// the spools, and nothing is returned. It reads the drain's progress only when a line of sess is
// found.
func (d *daemon) scanClientSpools(ctx context.Context, sess core.SessionID, at core.UnixMilli,
	look func(base string) bool,
) spoolScan {
	s := spoolScan{listed: map[string]bool{}}
	dr := d.drain.Load()
	if dr == nil {
		return s
	}
	var st drainState
	loaded := false
	for _, l := range listClientSpools(d.root) {
		s.listed[l.base] = true
		if look != nil && !look(l.base) {
			continue
		}
		lines, ok, read := d.spoolHeads.heads(ctx, d.root, l)
		if read {
			s.reads++
		}
		if !ok {
			if s.unread == nil {
				s.unread = map[string]bool{}
			}
			s.unread[l.base] = true
			continue
		}
		for _, ln := range lines {
			if ln.sess != sess || (ln.c.ts > 0 && at > 0 && ln.c.ts > at) {
				continue
			}
			if !loaded {
				// What the passes consumed of each file: everything before its recorded offset. A file
				// whose progress cannot be read is taken from its start; whatever a pass did consume of
				// it is acknowledged, and notYetAcknowledged drops it on that ground.
				st, _ = dr.loadState()
				loaded = true
			}
			if fs := st[l.base]; fs != nil && fs.Offset > 0 && fs.Offset <= l.size && ln.start < fs.Offset {
				continue
			}
			s.caps = append(s.caps, ln.c)
		}
	}
	d.spoolHeads.forget(s.listed)
	s.caps = d.notYetAcknowledged(s.caps)
	return s
}

// notYetAcknowledged drops from caps every capture whose delivery is already on the committed
// frontier: a spool copy of a delivery the lane, or a drain, has published.
func (d *daemon) notYetAcknowledged(caps []pendingCapture) []pendingCapture {
	if len(caps) == 0 {
		return caps
	}
	j, _ := d.deliveryJournal()
	out := caps[:0]
	for _, c := range caps {
		if c.nonce != "" && j != nil {
			if l, held, err := j.leaseHeld(c.nonce); err == nil && held && j.acknowledged(l.Delivery) {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// unreplayedCaptures returns the captures of sess the seal will not hold: every leased arrival of sess
// before upTo not yet settled, wherever the daemon holds it, and spooled, what the settle's last look
// at the client spools found. A delivery held in two places (a late ACK's copy beside the lane's job)
// is returned once.
//
// The leased arrivals are the lane's jobs, the jobs no lane holds (delivery_unheld.go: still in the
// ring, dropped from a full ring, refused by full lanes, or handed back by a flush; D73(b)), and then
// whatever else the delivery journal holds unsettled (unsettledLeases): an arrival a daemon before this
// one accepted, or one past the record's bound. The journal leased every one of them, so it is the
// authority on which are left; the lane, the record and the spools say what each one was. The lane and
// the record are read as of one moment (leasedOf), so a job moving between them is named. One only the
// journal knows is counted by its lease alone: no op, so no name (unreplayedDrops says so).
func (d *daemon) unreplayedCaptures(sess core.SessionID, upTo uint64, spooled []pendingCapture) []pendingCapture {
	j, _ := d.deliveryJournal()
	named := map[string]bool{}
	var left []pendingCapture
	add := func(c pendingCapture) {
		if c.nonce != "" {
			if named[c.nonce] {
				return
			}
			named[c.nonce] = true
		}
		left = append(left, c)
	}
	queued, unheld := d.ing.lanes.leasedOf(sess)
	for _, jb := range queued {
		if jb.leased && jb.lease.ArrivalSeq >= upTo {
			continue // arrived after the PreCompact
		}
		if jb.leased && j != nil && j.acknowledged(jb.lease.Delivery) {
			continue
		}
		add(captureOf(jb.req))
	}
	var settled []string // recorded jobs the journal shows settled, which the record can forget
	for _, u := range unheld {
		switch {
		case u.lease.ArrivalSeq >= upTo:
			// arrived after the PreCompact
		case j != nil && leaseSettled(j, u.lease):
			settled = append(settled, u.lease.Delivery)
		default:
			add(u.capture)
		}
	}
	d.ing.lanes.forgetUnheld(sess, settled)
	for _, c := range spooled {
		add(c)
	}
	if j != nil {
		leases, _ := j.unsettledLeases(sess, upTo)
		for _, l := range leases {
			add(pendingCapture{nonce: l.Delivery})
		}
	}
	return left
}

// unreplayedDrops is the drop report for left: one summary entry counting every capture by kind, and
// one entry per tool result naming its tool_use_id (checkpoint.DropKindUnreplayedToolResult), newest
// first, for as many as fit allowance as the checkpoint measures them (est, the estimator the seal's
// Truncate prices the whole document with). The summary says how many are named, and how many of the
// others are known only by their lease (unknownKindClauseFormat). The named entries carry no detail,
// because the summary already says what they mean.
func unreplayedDrops(left []pendingCapture, allowance core.Tokens, est tokens.Estimator) []checkpoint.DropEntry {
	var tools []pendingCapture
	prompts, unknown := 0, 0
	for _, c := range left {
		switch {
		case c.toolUseID != "":
			tools = append(tools, c)
		case c.op == ipc.OpObservePrompt:
			prompts++
		case c.op == "":
			unknown++ // known by its lease alone, and counted as other
		}
	}
	slices.SortStableFunc(tools, func(a, b pendingCapture) int { return cmp.Compare(b.ts, a.ts) })
	named := namesWithin(tools, allowance, est)
	summary := checkpoint.DropEntry{
		Kind: checkpoint.DropKindUnreplayedCapture,
		Detail: fmt.Sprintf(unreplayedDetailFormat, len(left), len(tools), prompts, len(left)-len(tools)-prompts,
			len(named)),
	}
	if unknown > 0 {
		summary.Detail += fmt.Sprintf(unknownKindClauseFormat, unknown)
	}
	return append([]checkpoint.DropEntry{summary}, named...)
}

// namesWithin returns the drop entries naming the longest prefix of tools whose cost in a checkpoint
// document is at most allowance. The cost of a prefix grows with its length, so it is found by binary
// search: a handful of measurements, whatever the backlog. A prefix that cannot be measured does not
// fit, and neither does any without an estimator to measure it.
func namesWithin(tools []pendingCapture, allowance core.Tokens, est tokens.Estimator) []checkpoint.DropEntry {
	if allowance <= 0 || len(tools) == 0 || est == nil {
		return nil
	}
	entries := make([]checkpoint.DropEntry, len(tools))
	for i, c := range tools {
		entries[i] = checkpoint.DropEntry{Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(c.toolUseID)}
	}
	base, ok := dropReportCost(est, nil)
	if !ok {
		return nil
	}
	n := sort.Search(len(entries), func(i int) bool {
		cost, ok := dropReportCost(est, entries[:i+1])
		return !ok || cost-base > allowance
	})
	return entries[:n]
}

// dropReportCost is what drops cost in a checkpoint document, as Truncate measures one.
func dropReportCost(est tokens.Estimator, drops []checkpoint.DropEntry) (core.Tokens, bool) {
	b, err := checkpoint.Marshal(checkpoint.Checkpoint{Dropped: drops})
	if err != nil {
		return 0, false
	}
	return est.Estimate(b, tokens.ClassJSON), true
}

// leasedOf returns, as of one moment, where the lanes hold sess's jobs: queued, a copy of the requests
// sess's lane still holds, in arrival order (queued, parked, or being published by the lane's owner),
// and unheld, a copy of sess's record of leased jobs no lane holds (unheldOf). It reads both under one
// hold of the lanes' mutex, which is what every move between them holds (join takes a job from the
// record into the lane, and a refusal, an eviction or a forget puts one back), so a job moving
// between them is in exactly one of the two. Read under two holds, a job a worker routed from the
// ring into its lane between them was in neither, and the settle could count it only by its journal
// lease, without its name (TestPreCompactSettle_NamesALeasedReadMovingIntoItsLane).
func (ls *dispatchLanes) leasedOf(sess core.SessionID) (queued []job, unheld []unheldJob) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if l := ls.lanes[sess]; l != nil {
		queued = append([]job(nil), l.jobs...)
	}
	return queued, ls.unheldOfLocked(sess)
}

// spoolLineHead is the part of a spooled ipc.Request the settle reads: the fields captureOf and
// resolveEvent's session use, under the same JSON names, and none of the payload. Decoding only these
// keeps the scan of a large backlog from allocating every spooled tool result
// (TestSpoolLineHead_ReadsWhatDecodeRequestReads pins the agreement with ipc.DecodeRequest).
type spoolLineHead struct {
	Op      ipc.Op         `json:"op"`
	Session core.SessionID `json:"s"`
	TS      core.UnixMilli `json:"t"`
	Nonce   string         `json:"n"`
	Event   *struct {
		SessionID core.SessionID `json:"session_id"`
		ToolUseID core.ToolUseID `json:"tool_use_id"`
	} `json:"e"`
}

func decodeSpoolLineHead(line []byte) (spoolLineHead, error) {
	var h spoolLineHead
	err := json.Unmarshal(line, &h)
	return h, err
}

// session is the session resolveEvent would give the full request.
func (h spoolLineHead) session() core.SessionID {
	if h.Event != nil && h.Event.SessionID != "" {
		return h.Event.SessionID
	}
	return h.Session
}

// capture is captureOf the full request.
func (h spoolLineHead) capture() pendingCapture {
	c := pendingCapture{op: h.Op, nonce: h.Nonce, ts: h.TS}
	if h.Event != nil {
		c.toolUseID = h.Event.ToolUseID
	}
	return c
}
