package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
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
// client-spool watcher, which a served request kicks and which takes two intervals to pass a spool,
// and the idle drain, two quiet minutes away. PreCompact is not a hot-path op, so the compaction does
// reach the daemon, and the route sealed at once: the checkpoint the host's compaction then depends
// on lacked the session's newest tool results, and so did the rehydration built from it.
//
// So before it seals, the route settles the session the way a session end does (settleSession,
// C1.13): it waits for the session's ingest lane to publish every arrival leased before the
// PreCompact, replays the client spools that hold this session's captures (only those: another
// session's backlog, older in host order, must not spend this session's bound), and waits for the
// lane once more for the successors the replay unparked. It does so only inside the bound below, and
// a capture still unpublished when the bound expires is counted in the checkpoint's drop report
// (checkpoint.DropKindUnreplayedCapture), the newest tool results among them named by tool_use_id
// (DropKindUnreplayedToolResult) within their share of the checkpoint's budget, which the
// rehydration's section 7 carries: never silently missing. A session with no client spool waiting
// and nothing still publishing pays one directory listing and two in-memory lookups (settleFast);
// when another session's spools are waiting it also reads them, to find none of its own.

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
// not a latency target: a session with nothing to settle does not wait at all. It is a hard bound:
// the replay's line in flight when it expires is cancelled, not finished (DrainClientSpoolsWithin
// says why, and what that costs on the slowest disks).
func precompactSettleBound(cfg config.Config) time.Duration {
	be := time.Duration(cfg.Runtime.Budgets.CheckpointFinalizeMs) * time.Millisecond
	return max(be-checkpoint.MaxPreCompactWindow, 0)
}

// unreplayedNamesBudgetDivisor sets the share of checkpoint.budgetTokens the per-tool-result names of
// the unreplayed report may take: one twentieth (5 %), 600 tokens of the default 12000, which names
// the newest dozen or so tool results (16 with the test fixture's short ids; an indented entry costs
// 35 to 40 tokens). Every drop entry is part of the checkpoint document Truncate
// measures, and Truncate never cuts the drop report: it cuts the narrative and then the pointers to
// make room for it. Named without a bound, a spool-submode backlog of 300 tool results took 11147
// of the default 12000 tokens (TestPreCompactSettle_ABacklogIsCountedInFullAndNamedWithinItsShare's
// fixture, before this bound) and cost the checkpoint its pointers to the session's older work. With
// it, a checkpoint whose pointers fit within 95 % of its budget keeps them all, whatever the backlog.
// The summary entry still counts every capture left, so nothing becomes silent; a tool result that is
// counted but not named is in the store once the daemon replays it, and recall finds it then.
// This is an owner number (V6 close-out D53(c)): too large and the names cut pointers on a nearly
// full checkpoint; too small and fewer of the newest tool results are named.
const unreplayedNamesBudgetDivisor = 20

// unreplayedNamesAllowance is the token allowance of the per-tool-result names under cfg.
func unreplayedNamesAllowance(cfg config.Config) core.Tokens {
	return core.Tokens(cfg.Checkpoint.BudgetTokens / unreplayedNamesBudgetDivisor)
}

// counterPrecompactSettle counts PreCompact seals that found something of their session to settle
// first: a client spool holding one of its captures, or an arrival leased before the PreCompact and
// not yet published.
const counterPrecompactSettle = "precompact_settle"

// counterPrecompactUnreplayed counts the captures a PreCompact seal reported as unreplayed: still in a
// client spool or the session's lane when the settle's bound expired.
const counterPrecompactUnreplayed = "precompact_unreplayed_captures"

// unreplayedDetailFormat is the summary drop entry's detail, as the checkpoint and section 7 carry it:
// how many captures were left, of which kinds, how many of the tool results are named, and what that
// means.
const unreplayedDetailFormat = "%d capture(s) of this session (%d tool result(s), %d prompt(s), %d other) were " +
	"still waiting to be replayed into the store when this checkpoint was sealed (durable writes on this disk " +
	"were slower than their budget); the newest %d tool result(s) are named by tool_use_id; nothing is lost: " +
	"the daemon replays them, and recall or expand finds them then"

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

// settleBeforeSeal settles sess before its PreCompact seal (see above) and returns the drop entries
// reporting what it could not settle. at is the PreCompact's hook time: a client-spooled capture
// fired after it does not belong to this compaction.
func (d *daemon) settleBeforeSeal(ctx context.Context, sess core.SessionID, at core.UnixMilli) []checkpoint.DropEntry {
	upTo, files, settled := d.settleFast(sess, at)
	if settled {
		return nil
	}
	if d.m != nil {
		d.m.Counter(counterPrecompactSettle).Add(1)
	}
	cfg := d.currentCfg()
	bound := precompactSettleBound(cfg)
	sctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	d.awaitArrivals(sctx, sess, upTo)
	if len(files) > 0 && sctx.Err() == nil {
		if dr := d.drain.Load(); dr != nil {
			// A replay is capture work, as every drain is (D51).
			d.capture.enter()
			_, err := dr.DrainClientSpoolsWithin(sctx, files)
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
	drops := unreplayedDrops(left, unreplayedNamesAllowance(cfg), tokens.New(cfg, ""))
	d.log.Warn("daemon: PreCompact sealed before some of the session's captures were replayed; "+
		"the checkpoint's drop report counts them, and the daemon replays them next",
		"session", string(sess), "captures", len(left), "named", len(drops)-1, "bound", bound.String())
	return drops
}

// settleFast reports what the settle has to wait for. upTo is one past the session's newest leased
// arrival when the PreCompact arrived (0 when the journal cannot say), files the client spools that
// hold the session's unreplayed captures fired at or before at, and settled is true when neither
// leaves anything to do: no such spool, and every arrival before upTo already on the committed
// frontier. With no client spool waiting at all, that answer costs one listing of the spool directory
// and two lookups in memory; with other sessions' spools waiting, it also reads them.
func (d *daemon) settleFast(sess core.SessionID, at core.UnixMilli) (upTo uint64, files map[string]bool, settled bool) {
	if j, err := d.deliveryJournal(); err == nil && j != nil {
		if last, ok := j.lastArrival(sess); ok {
			upTo = last + 1
		}
	}
	for _, c := range d.spooledCaptures(sess, at) {
		if files == nil {
			files = map[string]bool{}
		}
		files[c.file] = true
	}
	if len(files) > 0 {
		return upTo, files, false
	}
	if upTo <= 1 {
		return upTo, nil, true // no leased arrival before the PreCompact, or none the journal can name
	}
	delivered, known := d.sessionDelivered(sess, upTo)
	return upTo, nil, known && delivered
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

// spooledCaptures returns sess's hot-path captures fired at or before at that sit in a hook client
// spool, unconsumed by any drain and not already published through another copy of their delivery
// (a late ACK's spool copy of a delivery the lane published).
func (d *daemon) spooledCaptures(sess core.SessionID, at core.UnixMilli) []pendingCapture {
	dr := d.drain.Load()
	if dr == nil {
		return nil
	}
	caps := dr.pendingClientCaptures(sess, at)
	if len(caps) == 0 {
		return nil
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

// unreplayedCaptures returns the captures of sess the seal will not hold: the lane's jobs leased before
// upTo and not yet settled, and the client-spooled hot-path requests fired at or before at that no
// drain has consumed and whose delivery is not on the committed frontier. A delivery the lane and a
// spool both hold (a late ACK's copy) is returned once.
func (d *daemon) unreplayedCaptures(sess core.SessionID, upTo uint64, at core.UnixMilli) []pendingCapture {
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
	for _, jb := range d.ing.lanes.pending(sess) {
		if jb.leased && jb.lease.ArrivalSeq >= upTo {
			continue // arrived after the PreCompact
		}
		if jb.leased && j != nil && j.acknowledged(jb.lease.Delivery) {
			continue
		}
		add(captureOf(jb.req))
	}
	for _, c := range d.spooledCaptures(sess, at) {
		add(c)
	}
	return left
}

// unreplayedDrops is the drop report for left: one summary entry counting every capture by kind, and
// one entry per tool result naming its tool_use_id (checkpoint.DropKindUnreplayedToolResult), newest
// first, for as many as fit allowance as the checkpoint measures them (est, priced the way Truncate
// prices the whole document). The summary says how many are named. The named entries carry no detail,
// because the summary already says what they mean.
func unreplayedDrops(left []pendingCapture, allowance core.Tokens, est tokens.Estimator) []checkpoint.DropEntry {
	var tools []pendingCapture
	prompts := 0
	for _, c := range left {
		switch {
		case c.toolUseID != "":
			tools = append(tools, c)
		case c.op == ipc.OpObservePrompt:
			prompts++
		}
	}
	slices.SortStableFunc(tools, func(a, b pendingCapture) int { return cmp.Compare(b.ts, a.ts) })
	named := namesWithin(tools, allowance, est)
	summary := checkpoint.DropEntry{
		Kind: checkpoint.DropKindUnreplayedCapture,
		Detail: fmt.Sprintf(unreplayedDetailFormat, len(left), len(tools), prompts, len(left)-len(tools)-prompts,
			len(named)),
	}
	return append([]checkpoint.DropEntry{summary}, named...)
}

// namesWithin returns the drop entries naming the longest prefix of tools whose cost in a checkpoint
// document is at most allowance. The cost of a prefix grows with its length, so it is found by binary
// search: a handful of measurements, whatever the backlog. A prefix that cannot be measured does not
// fit.
func namesWithin(tools []pendingCapture, allowance core.Tokens, est tokens.Estimator) []checkpoint.DropEntry {
	if allowance <= 0 || len(tools) == 0 {
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

// pendingClientCaptures reads the hook client spools and returns the hot-path captures of sess fired at
// or before at that no drain has consumed yet: each file past the offset state/drain.json records for
// it, or from its start when the progress cannot be read (whatever a pass did consume is acknowledged,
// and the caller drops it on that ground). With no client spool listed it reads nothing more. It
// takes no lock: it only reads, through shared handles, so it never keeps a concurrent pass from
// replacing or removing a file. It decodes each line's head only (spoolLineHead).
func (dr *drainer) pendingClientCaptures(sess core.SessionID, at core.UnixMilli) []pendingCapture {
	files, err := ipc.SpoolFiles(paths.Of(dr.cfg.Root).Spool)
	if err != nil {
		return nil
	}
	files = slices.DeleteFunc(files, func(path string) bool { return !isClientSpoolName(filepath.Base(path)) })
	if len(files) == 0 {
		return nil
	}
	st, err := dr.loadState()
	if err != nil {
		st = nil
	}
	var out []pendingCapture
	for _, path := range files {
		base := filepath.Base(path)
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
			h, err := decodeSpoolLineHead(line)
			if err != nil || !h.Op.HotPath() || h.session() != sess {
				continue
			}
			if h.TS > 0 && at > 0 && h.TS > at {
				continue
			}
			c := h.capture()
			c.file = base
			out = append(out, c)
		}
	}
	return out
}
