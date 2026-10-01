package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// PreCompact seals after the session's own captures (V6 close-out D53(c)).
//
// A capture can reach the daemon after the PreCompact that follows it. On a disk whose durable writes
// are slower than runtime.budgets.l0IngestMs a hook waits only its ACK deadline and then hands the
// capture to its client spool, and after three over-budget windows the daemon switches the hot path
// to spool submode, from which point no tool result, prompt or Stop of the session reaches the daemon
// except through a client spool: the hook clients stop connecting for them at all (ipc client.go Send
// step 3). Only a drain reads a client spool, and the drains a session in spool submode meets are the
// client-spool watcher, which a served request kicks and which takes two intervals to pass a spool,
// and the idle drain, two quiet minutes away. PreCompact is not a hot-path op, so the compaction does
// reach the daemon, and the route sealed at once: the checkpoint the host's compaction then depends
// on lacked the session's newest tool results, and so did the rehydration built from it.
//
// So before it seals, the route settles the session the way a session end does (settleSession,
// C1.13): it waits for the session's ingest lane to publish every arrival leased before the
// PreCompact, replays the hooks' client spools, and waits for the lane once more for the successors
// the replay unparked. It does so only inside the bound below, and a capture still unpublished when
// the bound expires is named in the checkpoint's drop report (checkpoint.DropKindUnreplayedCapture,
// with DropKindUnreplayedToolResult per tool result), which the rehydration's section 7 carries:
// never silently missing. A session with nothing spooled and nothing still publishing pays one
// directory listing and two in-memory lookups (settleFast).

// precompactSettleBound is how long the PreCompact route may spend settling the session before it
// seals: B-E (runtime.budgets.checkpointFinalizeMs, the gated p99 for PreCompact entry to exit) less
// the seal's own worst case inside B-E (checkpoint.MaxPreCompactWindow). The settle is timed inside
// B-E with the seal, so a settle that takes its whole bound still leaves the seal the window it
// always had, and B-E's limit is not exceeded on the seal's account. 500 ms with the defaults
// (2000 - 1500). A B-E configured at or below the seal's window leaves no settle at all: the route
// seals at once and names what is unpublished.
//
// It is far inside the nested PreCompact deadlines (wire_checkpoint.go precompactDeadlineSlack:
// 14 s for the daemon inside the client's 15 s inside the host's 20 s), and it is a bound on a wait,
// not a latency target: a session with nothing to settle does not wait at all.
func precompactSettleBound(cfg config.Config) time.Duration {
	be := time.Duration(cfg.Runtime.Budgets.CheckpointFinalizeMs) * time.Millisecond
	return max(be-checkpoint.MaxPreCompactWindow, 0)
}

// counterPrecompactSettle counts PreCompact seals that found something of their session to settle
// first: a client spool, or an arrival leased before the PreCompact and not yet published.
const counterPrecompactSettle = "precompact_settle"

// counterPrecompactUnreplayed counts the captures a PreCompact seal named as unreplayed: still in a
// client spool or the session's lane when the settle's bound expired.
const counterPrecompactUnreplayed = "precompact_unreplayed_captures"

// unreplayedDetailFormat is the summary drop entry's detail, as the checkpoint and section 7 carry it:
// how many captures were left, of which kinds, and what that means.
const unreplayedDetailFormat = "%d capture(s) of this session (%d tool result(s), %d prompt(s), %d other) were " +
	"still waiting to be replayed into the store when this checkpoint was sealed (durable writes on this disk " +
	"were slower than their budget); nothing is lost: the daemon replays them, and recall or expand finds " +
	"them then"

// sealDropsKey carries the settle's drop entries to the bound PreCompact seam (wire_checkpoint.go),
// beside the event, which has no room for them.
type sealDropsKey struct{}

func withSealDrops(ctx context.Context, drops []checkpoint.DropEntry) context.Context {
	if len(drops) == 0 {
		return ctx
	}
	return context.WithValue(ctx, sealDropsKey{}, drops)
}

// sealDrops returns the drop entries the PreCompact route's settle left for the seal, if any.
func sealDrops(ctx context.Context) []checkpoint.DropEntry {
	drops, _ := ctx.Value(sealDropsKey{}).([]checkpoint.DropEntry)
	return drops
}

// settleBeforeSeal settles sess before its PreCompact seal (see above) and returns the drop entries
// naming what it could not settle. at is the PreCompact's hook time: a client-spooled capture fired
// after it does not belong to this compaction.
func (d *daemon) settleBeforeSeal(ctx context.Context, sess core.SessionID, at core.UnixMilli) []checkpoint.DropEntry {
	upTo, spooled, settled := d.settleFast(sess)
	if settled {
		return nil
	}
	if d.m != nil {
		d.m.Counter(counterPrecompactSettle).Add(1)
	}
	bound := precompactSettleBound(d.currentCfg())
	sctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	d.awaitArrivals(sctx, sess, upTo)
	if spooled && sctx.Err() == nil {
		if dr := d.drain.Load(); dr != nil {
			// A replay is capture work, as every drain is (D51).
			d.capture.enter()
			_, err := dr.DrainClientSpoolsWithin(sctx)
			d.capture.leave()
			if err != nil && sctx.Err() == nil {
				d.log.Debug("daemon: PreCompact: the client-spool replay before the seal ended early", "err", err)
			}
		}
		d.awaitArrivals(sctx, sess, upTo)
	}

	left := d.unreplayedCaptures(sess, upTo, at)
	if len(left) == 0 {
		return nil
	}
	if d.m != nil {
		d.m.Counter(counterPrecompactUnreplayed).Add(int64(len(left)))
	}
	d.log.Warn("daemon: PreCompact sealed before some of the session's captures were replayed; "+
		"the checkpoint's drop report names them, and the daemon replays them next",
		"session", string(sess), "captures", len(left), "bound", bound.String())
	return unreplayedDrops(left)
}

// settleFast reports what the settle has to wait for. upTo is one past the session's newest leased
// arrival when the PreCompact arrived (0 when the journal cannot say), spooled whether any hook
// client spool is waiting, and settled is true when neither leaves anything to do: no client spool,
// and every arrival before upTo already on the committed frontier. That answer costs one listing of
// the spool directory and two lookups in memory.
func (d *daemon) settleFast(sess core.SessionID) (upTo uint64, spooled, settled bool) {
	spooled = d.clientSpoolWaiting()
	if j, err := d.deliveryJournal(); err == nil && j != nil {
		if last, ok := j.lastArrival(sess); ok {
			upTo = last + 1
		}
	}
	if spooled {
		return upTo, true, false
	}
	if upTo <= 1 {
		return upTo, false, true // no leased arrival before the PreCompact, or none the journal can name
	}
	delivered, known := d.sessionDelivered(sess, upTo)
	return upTo, false, known && delivered
}

// clientSpoolWaiting reports whether the spool directory holds any hook client spool.
func (d *daemon) clientSpoolWaiting() bool {
	entries, err := os.ReadDir(paths.Long(paths.Of(d.root).Spool))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Type().IsRegular() && isClientSpoolName(e.Name()) {
			return true
		}
	}
	return false
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

// unreplayedCaptures returns the captures of sess the seal will not hold: the lane's jobs leased before
// upTo and not yet settled, and the client-spooled hot-path requests fired at or before at that no
// drain has consumed and whose delivery is not on the committed frontier. A delivery the lane and a
// spool both hold (a late ACK's copy) is returned once.
func (d *daemon) unreplayedCaptures(sess core.SessionID, upTo uint64, at core.UnixMilli) []ipc.Request {
	j, _ := d.deliveryJournal()
	named := map[string]bool{}
	var left []ipc.Request
	add := func(req ipc.Request) {
		if req.Nonce != "" {
			if named[req.Nonce] {
				return
			}
			named[req.Nonce] = true
		}
		left = append(left, req)
	}
	for _, jb := range d.ing.lanes.pending(sess) {
		if jb.leased && jb.lease.ArrivalSeq >= upTo {
			continue // arrived after the PreCompact
		}
		if jb.leased && j != nil && j.acknowledged(jb.lease.Delivery) {
			continue
		}
		add(jb.req)
	}
	if dr := d.drain.Load(); dr != nil {
		for _, req := range dr.pendingClientRequests(sess, at) {
			if req.Nonce != "" && j != nil {
				if l, held, err := j.leaseHeld(req.Nonce); err == nil && held && j.acknowledged(l.Delivery) {
					continue // a copy of a delivery already published
				}
			}
			add(req)
		}
	}
	return left
}

// unreplayedDrops is the drop report for left: one summary entry counting every capture by kind, and
// one entry per tool result naming its tool_use_id (checkpoint.DropKindUnreplayedToolResult). The
// per-result entries carry no detail, because every entry counts against the checkpoint's own token
// budget and the summary already says what they mean.
func unreplayedDrops(left []ipc.Request) []checkpoint.DropEntry {
	var tools, prompts int
	var named []checkpoint.DropEntry
	for _, req := range left {
		switch {
		case req.Event != nil && req.Event.ToolUseID != "":
			tools++
			named = append(named, checkpoint.DropEntry{
				Kind: checkpoint.DropKindUnreplayedToolResult, ID: string(req.Event.ToolUseID),
			})
		case req.Op == ipc.OpObservePrompt:
			prompts++
		}
	}
	summary := checkpoint.DropEntry{
		Kind:   checkpoint.DropKindUnreplayedCapture,
		Detail: fmt.Sprintf(unreplayedDetailFormat, len(left), tools, prompts, len(left)-tools-prompts),
	}
	return append([]checkpoint.DropEntry{summary}, named...)
}

// pending returns a copy of the requests sess's lane still holds, in arrival order: queued, parked, or
// being published by the lane's owner.
func (ls *dispatchLanes) pending(sess core.SessionID) []job {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.lanes[sess]
	if l == nil {
		return nil
	}
	return append([]job(nil), l.jobs...)
}

// pendingClientRequests reads the hook client spools and returns the hot-path requests of sess fired at
// or before at that no drain has consumed yet: each file past the offset state/drain.json records for
// it, or from its start when the progress cannot be read (whatever a pass did consume is acknowledged,
// and the caller drops it on that ground). It takes no lock: it only reads, through shared handles, so
// it never keeps a concurrent pass from replacing or removing a file.
func (dr *drainer) pendingClientRequests(sess core.SessionID, at core.UnixMilli) []ipc.Request {
	files, err := ipc.SpoolFiles(paths.Of(dr.cfg.Root).Spool)
	if err != nil {
		return nil
	}
	st, err := dr.loadState()
	if err != nil {
		st = nil
	}
	var out []ipc.Request
	for _, path := range files {
		base := filepath.Base(path)
		if !isClientSpoolName(base) {
			continue
		}
		b, err := paths.ReadFileShared(path)
		if err != nil {
			continue // consumed and removed since the listing, or unreadable: nothing to name from it
		}
		if fs := st[base]; fs != nil && fs.Offset > 0 && fs.Offset <= int64(len(b)) {
			b = b[fs.Offset:]
		}
		for len(b) > 0 {
			i := bytes.IndexByte(b, '\n')
			if i < 0 {
				break // a trailing partial line: its hook is still writing it
			}
			var line []byte
			line, b = b[:i], b[i+1:]
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			req, err := ipc.DecodeRequest(line)
			if err != nil || !req.Op.HotPath() || resolveEvent(req).SessionID != sess {
				continue
			}
			if req.TS > 0 && at > 0 && req.TS > at {
				continue
			}
			out = append(out, req)
		}
	}
	return out
}
