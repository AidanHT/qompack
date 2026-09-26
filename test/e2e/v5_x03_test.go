// V5 §4.3 — SP-20 acknowledged capture survives an interrupted drain and a restart, or records an
// explicit gap.
//
// Wired: the real binary's hook subcommands (which mint the delivery nonce and, under the §12.2
// spool submode, write the request to their own client spool) → the real daemon's drain
// (internal/daemon/drain.go: lease by nonce, durable capture sidecar, dispatch to the real observer,
// committed-frontier acknowledgement) → the real journals under .qompack/state/ → a SECOND daemon
// composed over the same tree after the first one is gone.
//
// The interruption is real and it is two switches, not a stub: the drain's own context is cancelled
// mid-replay (the daemon's shutdown path cancels exactly this context), and the first daemon is then
// stopped with its drain budget already exhausted, so nothing finishes the replay before the restart.
// The restart is an in-process composition rather than a hard-killed child process because on
// Windows a killed daemon's lock is reclaimable only after the 90 s heartbeat staleness window
// (internal/daemon/lock_windows.go) — a wait that would make the row a timing gate, not a fact.
//
// Retired clauses this row must NOT assert (plan §4 row 4.3): nothing here drives `expand` or
// `re_read` and nothing asserts a `source` of "worktree". Retrieval by handle is row 4.2's
// criterion, the plan retires historical/current substitution as unsafe, and this row's current
// criterion is the SP-20 capture/acknowledgement contract. What this row keeps of the retrieval
// half is the join a retrieval would follow: every acknowledged delivery resolves in the restarted
// index, and its sidecar's verified reference names that record's root.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x3v5Session is this row's session identity.
const x3v5Session = core.SessionID("sess-e2e-v5-x03")

// The three phases' event counts. Live deliveries are acknowledged by the running daemon; spooled
// deliveries reach disk but never the daemon; the cut lands after the third spooled handler
// completion, so the interrupted pass leaves acknowledged, indexed-but-unacknowledged and untouched
// records behind at once — every state the restart has to resolve.
const (
	x3v5LiveTurns    = 6
	x3v5SpooledTurns = 8
	x3v5CutAfter     = 3
)

// The tool_use_id prefixes, which is how the completion probe tells a spooled delivery from a live
// one (the live workers also run through the same seam).
const (
	x3v5LivePrefix    = "toolu_v5x03_live_"
	x3v5SpooledPrefix = "toolu_v5x03_spool_"
)

// The delivery journals' file names, as internal/daemon/delivery_lease.go names them
// (deliveryLeaseFile, deliveryAckFile). They are unexported there; a drift shows up here as an
// empty journal and a failed count, which is the right failure.
const (
	x3v5LeaseFile = "delivery-leases.jsonl"
	x3v5AckFile   = "delivery-acks.jsonl"
)

// x3v5Lease and x3v5Ack are the two journals' line shapes, transcribed from delivery_lease.go's
// deliveryLease and deliveryAck.
type x3v5Lease struct {
	Delivery      string             `json:"delivery"`
	Session       core.SessionID     `json:"session"`
	Arrival       uint64             `json:"arrival"`
	ObservationID core.ObservationID `json:"observation_id"`
}

type x3v5Ack struct {
	Delivery      string             `json:"delivery"`
	ObservationID core.ObservationID `json:"observation_id"`
}

// x3v5Rig is one in-process daemon with the L0 observer wired, plus a probe on the ObserveTool seam.
//
// It is v4StartObserverOnly's composition with one addition: a second Bind, registered AFTER
// WireObserver's, wraps Services.ObserveTool so the test learns when the REAL observer has finished
// handling an event. The real function still runs on every call and its error is returned
// unchanged — the wrapper observes, it does not stand in.
type x3v5Rig struct {
	D    daemon.Daemon
	Opts *daemon.Options
	P    *testutil.Project
	Bin  string

	cancelRun context.CancelFunc
	runDone   chan error
	stopOnce  sync.Once
}

// x3v5StartRig composes and runs the daemon. onObserved, when non-nil, is called after every real
// ObserveTool completion with the event it handled.
func x3v5StartRig(t *testing.T, p *testutil.Project, onObserved func(hookio.Event)) *x3v5Rig {
	t.Helper()

	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log

	obsv, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	opts.Bind(func(s *daemon.Services) {
		real := s.ObserveTool
		require.NotNil(t, real, "WireObserver must have bound ObserveTool before this probe wraps it")
		s.ObserveTool = func(ctx context.Context, e hookio.Event) error {
			err := real(ctx, e)
			if onObserved != nil {
				onObserved(e)
			}
			return err
		}
	})

	d, err := daemon.New(opts)
	require.NoError(t, err)
	daemon.RegisterObserverIdleWork(d, obsv)

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(runCtx) }()
	r := &x3v5Rig{D: d, Opts: &opts, P: p, Bin: Build(t), cancelRun: cancelRun, runDone: runDone}
	t.Cleanup(func() { r.stop(t, context.Background()) })
	e2eWaitDaemonUp(t, p.Root)
	return r
}

// stop shuts the daemon down under ctx and releases everything the composition opened. It runs
// once; the cleanup's call after an explicit stop is a no-op.
func (r *x3v5Rig) stop(t *testing.T, ctx context.Context) {
	t.Helper()
	r.stopOnce.Do(func() {
		if err := r.D.Stop(ctx); err != nil {
			t.Errorf("e2e: stopping the V5 daemon: %v", err)
		}
		r.cancelRun()
		if err := <-r.runDone; err != nil {
			t.Errorf("e2e: the V5 daemon's Run returned: %v", err)
		}
		if led := r.Opts.LedgerHandle(); led != nil {
			_ = led.Close()
		}
		_ = r.Opts.Store.Close()
	})
}

// StopWithExhaustedDrainBudget is the interruption's second half: daemon.Stop derives its final
// drain's context from the one it is handed (daemon.go: context.WithTimeout(ctx, stopDrainBound)),
// so a context that is already cancelled is a shutdown whose drain budget expired before the replay
// could begin. Everything else Stop does — WAL close, sketch save, metrics, state.bin, server, lock
// — runs exactly as it always does.
func (r *x3v5Rig) StopWithExhaustedDrainBudget(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.stop(t, ctx)
}

// Gaps reads the drain's own accounting off the running daemon.
func (r *x3v5Rig) Gaps(t *testing.T) daemon.DrainGapState {
	t.Helper()
	rep, ok := r.D.(daemon.GapReporter)
	require.True(t, ok, "the composed daemon must implement daemon.GapReporter")
	return rep.DrainGaps()
}

// x3v5ReadJSONL hands every non-empty line of the named state file to decode and returns the raw
// bytes, so a caller can assert append-only growth as well as content. A missing file is an empty
// journal.
func x3v5ReadJSONL(t *testing.T, root, name string, decode func([]byte)) []byte {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, name)))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		decode([]byte(line))
	}
	return raw
}

func x3v5Leases(t *testing.T, root string) ([]x3v5Lease, []byte) {
	t.Helper()
	var out []x3v5Lease
	raw := x3v5ReadJSONL(t, root, x3v5LeaseFile, func(b []byte) {
		var l x3v5Lease
		require.NoError(t, json.Unmarshal(b, &l), "lease line: %s", b)
		out = append(out, l)
	})
	return out, raw
}

func x3v5Acks(t *testing.T, root string) ([]x3v5Ack, []byte) {
	t.Helper()
	var out []x3v5Ack
	raw := x3v5ReadJSONL(t, root, x3v5AckFile, func(b []byte) {
		var a x3v5Ack
		require.NoError(t, json.Unmarshal(b, &a), "ack line: %s", b)
		out = append(out, a)
	})
	return out, raw
}

// x3v5AckedIDs resolves every acknowledged delivery to the tool_use_id its capture sidecar was
// published as, asserting on the way that the sidecar is readable, published, and carries the host
// payload that names that same id. That chain — frontier → identity → durable capture → verified
// reference → host bytes — is what "acknowledged capture" means on this tree.
func x3v5AckedIDs(t *testing.T, root string, acks []x3v5Ack) map[string]core.ObservationID {
	t.Helper()
	out := map[string]core.ObservationID{}
	for _, a := range acks {
		sc, err := store.ReadCaptureSidecar(root, a.ObservationID)
		require.NoError(t, err, "an acknowledged delivery must have a readable capture sidecar: %+v", a)
		require.Equal(t, a.ObservationID, sc.ObservationID)
		require.Equal(t, a.Delivery, sc.Delivery, "the sidecar must name the delivery it was made for")
		require.Equal(t, x3v5Session, sc.Session)
		require.True(t, sc.Published, "an acknowledged delivery's sidecar must carry its verified reference")
		require.NotEmpty(t, sc.ToolUseID)
		require.False(t, sc.Root.IsZero(), "the verified reference must name a content root")
		require.NotEmpty(t, sc.Bytes, "the permitted host payload must be retained")
		var host struct {
			ToolUseID string `json:"tool_use_id"`
		}
		require.NoError(t, json.Unmarshal(sc.Bytes, &host), "sidecar bytes must be the host payload")
		require.Equal(t, string(sc.ToolUseID), host.ToolUseID,
			"the reference must name the tool_use_id the retained host payload carries")
		_, dup := out[host.ToolUseID]
		require.False(t, dup, "one delivery, one acknowledgement: %s was acknowledged twice", host.ToolUseID)
		out[host.ToolUseID] = a.ObservationID
	}
	return out
}

// x3v5ClientSpoolLines returns every decodable request line across the client-*.ndjson spool
// files, keyed by file, and asserts each carries a delivery nonce — the thing that makes it
// leasable after a restart.
func x3v5ClientSpoolLines(t *testing.T, root string) map[string][]ipc.Request {
	t.Helper()
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	out := map[string][]ipc.Request{}
	for _, f := range files {
		base := filepath.Base(f)
		if !strings.HasPrefix(base, obsClientSpoolPrefix) {
			continue
		}
		raw, rerr := os.ReadFile(paths.Long(f))
		require.NoError(t, rerr)
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			req, derr := ipc.DecodeRequest([]byte(line))
			require.NoError(t, derr, "a client spool line the hook wrote must decode: %s", line)
			require.NotEmpty(t, req.Nonce, "a spooled delivery must carry its nonce or it can never be leased")
			out[base] = append(out[base], req)
		}
	}
	return out
}

// x3v5SpoolLineCount counts the client spool lines whose tool_use_id carries prefix — a phase's own
// deliveries. An empty prefix counts every line. Filtering matters because a hook of ANOTHER phase
// can legitimately degrade to the spool under load (a live hook that missed its ACK deadline spools
// the same delivery a second time, under the same nonce); such a line is the daemon's to reconcile,
// and it must not be read as one of this phase's.
func x3v5SpoolLineCount(t *testing.T, root, prefix string) int {
	t.Helper()
	n := 0
	for _, reqs := range x3v5ClientSpoolLines(t, root) {
		for _, req := range reqs {
			if prefix == "" || (req.Event != nil && strings.HasPrefix(string(req.Event.ToolUseID), prefix)) {
				n++
			}
		}
	}
	return n
}

// x3v5SetHot flips the §12.2 hot-path submode in run/state.bin, which the hook client reads at
// construction: HotSpool makes every hot-path hook append to its own client spool without dialling
// the daemon (internal/ipc/client.go Send). It is the real switch TestE2ESpoolSubmodeEndToEnd uses.
func x3v5SetHot(t *testing.T, p *testutil.Project, hot ipc.HotPathMode) {
	t.Helper()
	st := ipc.ReadState(p.Root, p.Cfg)
	st.Hot = hot
	require.NoError(t, ipc.WriteState(p.Root, st))
}

func x3v5Observe(t *testing.T, r *x3v5Rig, id, path string) {
	t.Helper()
	obsRunHook(t, r.Bin, []string{"observe", "tool"},
		obsToolPayload(t, r.P.Root, x3v5Session, id, path,
			fmt.Sprintf("package x03\n\n// %s\nfunc handler() error { return nil }\n", id)), e2eEnv(r.P))
}

// x3v5Diag renders the publication state at FORMAT time (the same reason obsWaitDiag exists:
// require.Eventually evaluates its message arguments before the wait), so a failed wait reports the
// journals, the counters and the drain accounting as they stood when it expired.
type x3v5Diag struct {
	t *testing.T
	r *x3v5Rig
}

func (d x3v5Diag) String() string {
	leases, _ := x3v5Leases(d.t, d.r.P.Root)
	acks, _ := x3v5Acks(d.t, d.r.P.Root)
	snap := d.r.Opts.Metrics.Snapshot()
	var counters []string
	for name, v := range snap.Counters {
		if v != 0 && (strings.HasPrefix(name, "drain_") || strings.HasPrefix(name, "delivery_") ||
			strings.HasPrefix(name, "sidecar_") || strings.HasPrefix(name, "l0_")) {
			counters = append(counters, fmt.Sprintf("%s=%d", name, v))
		}
	}
	return fmt.Sprintf("%s; leases=%d acks=%d; counters=%v; gaps=%+v; LOUD=%v",
		obsWaitDiag{d.r.P.Root}, len(leases), len(acks), counters, d.r.Gaps(d.t), loudLines(d.t, d.r.P.Root))
}

// WaitAcknowledged drives Drain until the index holds want records, the committed frontier names
// want deliveries, AND no client spool line is left. Driving Drain is what makes a hot-path hook
// that degraded to its client spool under load (obsProcessBound's second mechanism) count here
// without waiting on the daemon's own idle tick; a drain of an already-acknowledged line is a no-op
// by construction. The empty-spool clause is why the wait cannot end on a duplicate the drain has
// refused once ("delivery still in progress" while the live worker held the key): the next pass
// sees the key completed and releases the file.
func (r *x3v5Rig) WaitAcknowledged(t *testing.T, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, _ = r.D.Drain(context.Background())
		acks, _ := x3v5Acks(t, r.P.Root)
		return len(obsToolUseLines(r.P.Root)) >= want && len(acks) >= want &&
			x3v5SpoolLineCount(t, r.P.Root, "") == 0
	}, obsProcessBound, obsProcessTick,
		"the deliveries never reached the index and the committed frontier: %s", x3v5Diag{t, r})
}

func x3v5GapKinds(st daemon.DrainGapState) map[daemon.DrainGapKind]int {
	out := map[daemon.DrainGapKind]int{}
	for _, g := range st.Gaps {
		out[g.Kind] += g.Count
	}
	return out
}

// TestV5_HookEventToTombstoneToRetrievalAfterRestart is V5-VERIFY §4.3.
//
// The negative controls are built in rather than appended: the interrupted pass MUST return
// context.Canceled and report an unacknowledged gap (a drain that could not be interrupted would
// fail there), the exhausted-budget shutdown MUST leave the acknowledgement journal byte-identical
// (a shutdown that finished the replay would fail there), and the final subtest corrupts a real
// spool line and asserts the same reader that answered Complete above now answers a named gap and
// the store answers not-found for the lost record — never coverage.
func TestV5_HookEventToTombstoneToRetrievalAfterRestart(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

	// The completion probe: counts spooled deliveries the REAL observer has finished, records the
	// one at the cut, and cancels the drain's context at that moment. The seam runs on the drain's
	// own goroutine, so the cancel lands between the handler's return and the frontier commit.
	var spooledDone atomic.Int64
	var cutID atomic.Value
	drainCtx, cancelDrain := context.WithCancel(ctx)
	defer cancelDrain()
	probe := func(e hookio.Event) {
		if !strings.HasPrefix(string(e.ToolUseID), x3v5SpooledPrefix) {
			return
		}
		if spooledDone.Add(1) == x3v5CutAfter {
			cutID.Store(string(e.ToolUseID))
			cancelDrain()
		}
	}
	first := x3v5StartRig(t, p, probe)
	env := e2eEnv(p)

	// ── Phase 1: live deliveries, acknowledged by the running daemon ─────────────────────────────
	obsRunHook(t, first.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x3v5Session), env)
	for i := range x3v5LiveTurns {
		x3v5Observe(t, first, fmt.Sprintf("%s%02d", x3v5LivePrefix, i), fmt.Sprintf("src/x03_live_%02d.go", i))
	}
	first.WaitAcknowledged(t, x3v5LiveTurns)

	liveLeases, _ := x3v5Leases(t, p.Root)
	liveAcks, _ := x3v5Acks(t, p.Root)
	require.Len(t, liveLeases, x3v5LiveTurns, "one lease per live delivery")
	require.Len(t, liveAcks, x3v5LiveTurns, "one committed-frontier record per live delivery")
	liveIDs := x3v5AckedIDs(t, p.Root, liveAcks)
	for i := range x3v5LiveTurns {
		require.Contains(t, liveIDs, fmt.Sprintf("%s%02d", x3v5LivePrefix, i))
	}

	// ── Phase 2: deliveries that reach disk but never the daemon ─────────────────────────────────
	x3v5SetHot(t, p, ipc.HotSpool)
	for i := range x3v5SpooledTurns {
		x3v5Observe(t, first, fmt.Sprintf("%s%02d", x3v5SpooledPrefix, i), fmt.Sprintf("src/x03_spool_%02d.go", i))
	}
	x3v5SetHot(t, p, ipc.HotSync)

	require.Equal(t, x3v5SpooledTurns, x3v5SpoolLineCount(t, p.Root, x3v5SpooledPrefix),
		"every spool-submode hook must have appended exactly one line to its own client spool")
	require.Len(t, obsToolUseLines(p.Root), x3v5LiveTurns,
		"a spooled delivery must not have reached the observer")
	leasesBeforeDrain, _ := x3v5Leases(t, p.Root)
	require.Len(t, leasesBeforeDrain, x3v5LiveTurns,
		"a delivery the daemon never saw has no identity yet — that is a gap, and it is recorded as one below")
	require.Zero(t, spooledDone.Load())

	// ── Phase 3: the interrupted drain ───────────────────────────────────────────────────────────
	drained, err := first.D.Drain(drainCtx)
	require.ErrorIs(t, err, context.Canceled,
		"the drain must report the interruption to its caller, not swallow it")
	require.Equal(t, x3v5CutAfter-1, drained,
		"only deliveries whose frontier record was committed count as drained; the cut one was handled but not acknowledged")
	require.EqualValues(t, x3v5CutAfter, spooledDone.Load(), "the probe must have cancelled at the cut, not later")
	cut, _ := cutID.Load().(string)
	require.NotEmpty(t, cut)

	gaps := first.Gaps(t)
	require.True(t, gaps.Observed, "a pass ran, so the accounting must be an observation, not unknown")
	require.False(t, gaps.Complete, "an interrupted pass must never report the spool fully replayed")
	kinds := x3v5GapKinds(gaps)
	require.Equal(t, 1, kinds[daemon.DrainGapUnacknowledged],
		"the cut delivery's publication did not reach the frontier and must be recorded as exactly that: %+v", gaps)
	require.Positive(t, gaps.PendingBytes, "the cut file's unread bytes must be reported as pending: %+v", gaps)
	require.Zero(t, kinds[daemon.DrainGapUnleased], "every spooled line carried a nonce; none may be unleased: %+v", gaps)
	require.Zero(t, kinds[daemon.DrainGapCorruptLine], "nothing was corrupt: %+v", gaps)

	// On disk: the cut delivery is leased, captured, indexed and LINKED, but not acknowledged —
	// the precise state SP05-D1 named, now with every stage durable and the frontier withheld.
	midLeases, leaseBytesMid := x3v5Leases(t, p.Root)
	midAcks, ackBytesMid := x3v5Acks(t, p.Root)
	require.Len(t, midLeases, x3v5LiveTurns+x3v5CutAfter, "the cut delivery took a lease before it was handled")
	require.Len(t, midAcks, x3v5LiveTurns+x3v5CutAfter-1, "the cut delivery must not be acknowledged")
	midIDs := x3v5AckedIDs(t, p.Root, midAcks)
	require.NotContains(t, midIDs, cut)
	require.Len(t, obsToolUseLines(p.Root), x3v5LiveTurns+x3v5CutAfter,
		"the observer handled the cut delivery before the frontier was refused")
	var cutObs core.ObservationID
	for _, l := range midLeases {
		sc, rerr := store.ReadCaptureSidecar(p.Root, l.ObservationID)
		require.NoError(t, rerr, "every leased delivery's capture must be durable, acknowledged or not")
		if string(sc.ToolUseID) == cut {
			cutObs = l.ObservationID
			require.True(t, sc.Published, "the cut delivery's reference stage completed before the cut")
		}
	}
	require.NotEmpty(t, cutObs, "the cut delivery's sidecar must be linked to its tool_use_id")
	require.GreaterOrEqual(t, x3v5SpoolLineCount(t, p.Root, x3v5SpooledPrefix), x3v5SpooledTurns-(x3v5CutAfter-1),
		"every unacknowledged line must still be on disk for the restart to replay")

	// The exhausted-budget shutdown: the daemon goes away and the replay stays unfinished.
	first.StopWithExhaustedDrainBudget(t)
	_, ackBytesAfterStop := x3v5Acks(t, p.Root)
	require.Equal(t, ackBytesMid, ackBytesAfterStop,
		"NEGATIVE CONTROL: a shutdown whose drain budget was already spent must not have finished the replay; "+
			"if it did, nothing below is testing a restart")
	require.GreaterOrEqual(t, x3v5SpoolLineCount(t, p.Root, x3v5SpooledPrefix), x3v5SpooledTurns-(x3v5CutAfter-1))

	// ── Phase 4: the restart, over the same tree ─────────────────────────────────────────────────
	second := x3v5StartRig(t, p, nil)
	require.Eventually(t, func() bool {
		acks, _ := x3v5Acks(t, p.Root)
		return len(acks) >= x3v5LiveTurns+x3v5SpooledTurns && second.Gaps(t).Observed
	}, obsProcessBound, obsProcessTick,
		"the restarted daemon's startup drain never acknowledged every spooled delivery: %s", x3v5Diag{t, second})

	after := second.Gaps(t)
	require.True(t, after.Complete, "after the restart's replay the spool must be fully accounted for: %+v", after)
	require.Empty(t, after.Gaps)
	require.Zero(t, after.PendingBytes)

	finalLeases, leaseBytesAfter := x3v5Leases(t, p.Root)
	finalAcks, ackBytesAfter := x3v5Acks(t, p.Root)
	require.Len(t, finalLeases, x3v5LiveTurns+x3v5SpooledTurns,
		"the redelivered cut line must take its ORIGINAL lease back, not mint a second identity")
	require.Len(t, finalAcks, x3v5LiveTurns+x3v5SpooledTurns, "every delivery reaches the frontier exactly once")
	require.True(t, strings.HasPrefix(string(leaseBytesAfter), string(leaseBytesMid)),
		"the lease journal is append-only across the restart")
	require.True(t, strings.HasPrefix(string(ackBytesAfter), string(ackBytesMid)),
		"the acknowledgement journal is append-only across the restart: nothing acknowledged before it was rewritten")

	finalIDs := x3v5AckedIDs(t, p.Root, finalAcks)
	for id, obs := range liveIDs {
		require.Equal(t, obs, finalIDs[id], "a live delivery's identity must be the same after the restart: %s", id)
	}
	require.Equal(t, cutObs, finalIDs[cut],
		"the interrupted delivery must be acknowledged under the identity it was leased before the cut")
	for i := range x3v5SpooledTurns {
		require.Contains(t, finalIDs, fmt.Sprintf("%s%02d", x3v5SpooledPrefix, i))
	}

	// The reference side agrees with the frontier side, read through the restarted store.
	require.Len(t, obsToolUseLines(p.Root), x3v5LiveTurns+x3v5SpooledTurns,
		"the redelivered cut line must not be indexed twice — RecordToolUse is idempotent for one id and root")
	for id, obs := range finalIDs {
		rec, terr := second.Opts.Store.ToolUse(ctx, core.ToolUseID(id))
		require.NoError(t, terr, "every acknowledged delivery must resolve in the restarted index: %s", id)
		sc, serr := store.ReadCaptureSidecar(p.Root, obs)
		require.NoError(t, serr)
		require.Equal(t, rec.Root, sc.Root, "the sidecar's verified reference must name the index record's root: %s", id)
	}
	require.Zero(t, x3v5SpoolLineCount(t, p.Root, ""), "an acknowledged client spool is released")

	// A second pass is idempotent: nothing left to replay, nothing redelivered.
	again, err := second.D.Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, again)
	require.True(t, second.Gaps(t).Complete)

	// SessionEnd through the real binary against the restarted daemon: a flush whose replay is
	// complete clears its recovery marker, so a marker that survives is a real interruption.
	//
	// Since C1.15 the hook answers once the flush is durable, with the session already marked as
	// needing recovery, and the daemon ends the session on its own. obsRunFlush waits for that end to
	// get past SessionEnd; the marker is cleared at its very end, after the end's replay, so it is
	// waited for as well rather than read at an instant the end may not have reached.
	obsRunFlush(t, second.Bin, p.Root, x3v5Session, env)
	var sr daemon.SessionRecovery
	require.Eventually(t, func() bool {
		var lerr error
		sr, lerr = daemon.LoadSessionRecovery(p.Root)
		_, marked := sr.Sessions[x3v5Session]
		return lerr == nil && !marked
	}, obsProcessBound, obsProcessTick,
		"a completed flush must not leave the session marked as needing recovery: %+v", &sr)

	p.AssertAppendOnly(t)

	// ── NEGATIVE CONTROL: a corrupt spool line is an explicit gap, never coverage ─────────────────
	t.Run("corrupt spool line is recorded as a gap", func(t *testing.T) {
		x3v5CorruptSpoolLineIsAGap(t)
	})
}

// x3v5CorruptSpoolLineIsAGap severs one real spooled delivery by corrupting its bytes in place, then
// drains. The lines around it must still be delivered, the lost one must be reported as
// corrupt_line by name and counted, the pass must not report Complete, and the store must answer
// not-found for the lost id rather than anything that could be read as a record.
func x3v5CorruptSpoolLineIsAGap(t *testing.T) {
	const corruptTurns = 4
	ctx := context.Background()
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	r := x3v5StartRig(t, p, nil)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x3v5Session), env)
	x3v5SetHot(t, p, ipc.HotSpool)
	for i := range corruptTurns {
		x3v5Observe(t, r, fmt.Sprintf("%sctl_%02d", x3v5SpooledPrefix, i), fmt.Sprintf("src/x03_ctl_%02d.go", i))
	}
	x3v5SetHot(t, p, ipc.HotSync)
	lines := x3v5ClientSpoolLines(t, p.Root)
	require.Equal(t, corruptTurns, x3v5SpoolLineCount(t, p.Root, x3v5SpooledPrefix))

	// Corrupt the first line of the first client file that holds one of THIS phase's deliveries
	// (ipc.SpoolFiles sorts them, so the drain reads it first): its opening brace becomes a byte no
	// JSON decoder accepts, in place, same length.
	files, err := ipc.SpoolFiles(paths.Of(p.Root).Spool)
	require.NoError(t, err)
	var victim, lostID string
	for _, f := range files {
		reqs := lines[filepath.Base(f)]
		if len(reqs) == 0 || reqs[0].Event == nil ||
			!strings.HasPrefix(string(reqs[0].Event.ToolUseID), x3v5SpooledPrefix) {
			continue
		}
		victim, lostID = f, string(reqs[0].Event.ToolUseID)
		break
	}
	require.NotEmpty(t, victim)
	raw, err := os.ReadFile(paths.Long(victim))
	require.NoError(t, err)
	require.Equal(t, byte('{'), raw[0])
	raw[0] = 'X'
	require.NoError(t, os.WriteFile(paths.Long(victim), raw, 0o600))

	drained, err := r.D.Drain(ctx)
	require.NoError(t, err, "a corrupt line is skipped and reported; it must not abort the replay")
	require.Equal(t, corruptTurns-1, drained, "every intact line must still be delivered")

	gaps := r.Gaps(t)
	require.True(t, gaps.Observed)
	require.False(t, gaps.Complete,
		"NEGATIVE CONTROL: the reader that answered Complete for a clean replay must answer otherwise "+
			"when a delivery was lost, or Complete above proves nothing")
	var named bool
	for _, g := range gaps.Gaps {
		if g.Kind == daemon.DrainGapCorruptLine {
			named = true
			require.Equal(t, filepath.Base(victim), g.File, "the gap must name the file that lost a record")
			require.Equal(t, 1, g.Count)
		}
	}
	require.True(t, named, "the lost record must be recorded as corrupt_line: %+v", gaps)
	require.EqualValues(t, 1, r.Opts.Metrics.Snapshot().Counters["drain_file_error"],
		"the gap must also be counted where /qompack:status reads it")

	acks, _ := x3v5Acks(t, p.Root)
	ids := x3v5AckedIDs(t, p.Root, acks)
	require.Len(t, ids, corruptTurns-1)
	require.NotContains(t, ids, lostID)
	_, err = r.Opts.Store.ToolUse(ctx, core.ToolUseID(lostID))
	require.ErrorIs(t, err, core.ErrNotFound,
		"a lost delivery must be absent from the index — an explicit gap, not a record of any kind")
	require.Len(t, obsToolUseLines(p.Root), corruptTurns-1)

	p.AssertAppendOnly(t)
}
