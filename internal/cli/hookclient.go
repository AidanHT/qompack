package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// Per-subcommand reply deadlines (task-6-spec.md's wiring table). observe.tool and observe.stop
// use AckDeadline straight from the hot-path state record instead — see hookSpec.deadline's own
// doc comment.
const (
	// promptReplyDeadline mirrors internal/daemon/handlers.go's own unexported promptReplyDeadline
	// (250ms). The daemon does not export it, so this is a cli-local copy tied to it by this
	// comment: change one, change the other. §2.4/§12.
	promptReplyDeadline = 250 * time.Millisecond
	// sessionStartReplyDeadline is session.start's reply deadline (manifest hook timeout 15s). §2.4.
	sessionStartReplyDeadline = 10 * time.Second
	// checkpointReplyDeadline is checkpoint's reply deadline (manifest hook timeout 20s). §2.4.
	checkpointReplyDeadline = 15 * time.Second
)

// flush has no reply deadline since C1.15. Claude Code gives a plugin's SessionEnd hooks one SHARED
// 1.5 s budget, which a timeout set on a plugin-provided hook does not raise, and cancels a hook still
// running when it runs out; the flush used to wait up to 15 s for the daemon to end the session, and
// the host cancelled it every time. The flush is now fire-and-forget: the daemon ACKs it once its
// line is in the WAL and leased, within flushAckDeadline, and ends the session on its own
// (internal/daemon/session_end.go). A missed ACK spools the flush like any hook; its nonce makes the
// spooled copy a duplicate the daemon absorbs.
const (
	// sessionEndHostBudget is the one budget Claude Code shares among a plugin's SessionEnd hooks
	// (the hooks reference; plans/sdd/V6-closeout/packaging/evidence/live-s{1,2}-*/stderr.txt).
	sessionEndHostBudget = 1500 * time.Millisecond
	// flushAckDeadline is how long the flush waits for its ACK. The observe hot path's AckDeadline
	// (17/73/45 ms by platform) is sized for an observe event, which the daemon acknowledges after a
	// WAL append and a lease; before it acknowledges a flush it also takes the flush's in-process
	// ownership and rewrites the session recovery record, and a missed ACK leaves a spooled duplicate
	// for every session that ends. The dial (hookConnectDeadlineFloor) and the ACK together get half
	// of the host's budget; the other half is the process's own start-up, its input read and, when the
	// ACK is missed after all, its spool append.
	flushAckDeadline = sessionEndHostBudget/2 - hookConnectDeadlineFloor
)

// newHookMetrics returns the obs.Registry every hook body's ipc.Client is constructed with: a
// real, freshly-constructed SP-01 registry (obs.New), per task-6-spec.md's own skeleton comment
// naming "metrics" alongside "log" as a real value, not the nil this package shipped in fix
// round 0. A short-lived, once-per-hook-call process never Persists it (only the resident daemon's
// own Registry, internal/daemon/metrics.go, does that) — its whole job here is to make
// ipc.Client's l0_spooled/l0_dropped increments land somewhere real instead of a nil-guarded no-op,
// so a caller with the client in hand (a unit test, a future in-process caller) can actually read
// them back.
func newHookMetrics(clk core.Clock) obs.Registry { return obs.New(clk) }

// hookLogger is the logging.Logger every hook body's ipc.Client is constructed with. It defers
// opening a real, file-backed sink until the FIRST Warn/Error/Loud call, and even then only if
// root's .qompack/logs directory already exists — the identical rule logQuiet follows, so a hook
// on a project that has never been touched still never creates a directory on its own error path.
// Debug/Info are permanent no-ops: nothing on the hot path logs at those levels through this
// seam, so there is no reason to ever materialize for them. A hook run with nothing to report
// therefore allocates nothing beyond this thin wrapper — the same "zero-failure path allocates
// nothing" property fix round 0's nil log/metrics aimed for, but achieved without also making
// every failure path invisible (fix round 1, Critical/Important C-2/I-2): a spool-readonly,
// spool-full or disk-full drop now reaches both the process-wide Loud ring AND, when the project
// has a logs directory, a durable LOUD.log line and the day log — exactly what the fault table's
// "one Loud" expectation requires.
type hookLogger struct {
	root string
	// home is the user's home directory, when the caller knows it. It enables the user-level
	// fallback sink below; an empty home simply means there is no second place to try.
	home string
	mu   sync.Mutex
	real logging.Logger
	// closer releases the file handles the materialized sink holds. A hook process exits within
	// milliseconds of its last log line, so production never needs it; an IN-PROCESS caller does,
	// because Windows refuses to delete a directory whose files are still open.
	closer io.Closer
	kv     []any
}

// newHookLogger returns a hookLogger rooted at root with no user-level fallback. Constructing one
// never touches the filesystem; only Warn/Error/Loud does, and only on first use.
//
// It is what the seams with no Env in hand use (session-start's preSend, self-test's probes). Every
// caller that can name the home directory should use newHookLoggerWithHome instead, because that is
// the one that survives a project store this process cannot write to.
func newHookLogger(root string) logging.Logger {
	return &hookLogger{root: root}
}

// newHookLoggerWithHome is newHookLogger plus the user-level fallback sink.
func newHookLoggerWithHome(root, home string) logging.Logger {
	return &hookLogger{root: root, home: home}
}

// materialize returns the real logger, constructing it under lock exactly once.
//
// Three sinks are tried in order, and the second of them is finding F-2. A project whose .qompack
// directory has been made read-only underneath a live session produced NO durable evidence of the
// degradation at all: logging.New failed, this fell straight to logging.Nop, and the only trace —
// the l0.dropped counter — died with the hook process. §13 invariant 10 says degradation is loud,
// and a Loud nobody can read afterwards is not loud.
//
//  1. the project's own <root>/.qompack/logs, when it exists and can be opened;
//  2. the user-level <home>/.qompack/logs, one of the five §3.3 write locations, tried only when the
//     project's log directory EXISTS and could not be opened — which is the degradation. A project
//     with no log directory at all has not been written to yet and has not degraded either, and a
//     hook must never conjure state for one out of a diagnostic;
//  3. logging.Nop, which still records Loud calls into the process-wide ring (internal/logging's
//     LastLoud) even with no file sink.
//
// Falling back to (2) also emits one best-effort stderr line naming the root and the reason, so the
// degradation is visible in the host's own transcript and not only in a file the operator has to
// know to look for.
func (l *hookLogger) materialize() logging.Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.real != nil {
		return l.real
	}
	projectLogs := paths.Of(l.root).Logs
	reason := ""
	if isDir(projectLogs) {
		log, closer, err := logging.New(projectLogs, logging.Warn)
		if err == nil {
			l.real, l.closer = log, closer
			return l.real
		}
		reason = err.Error()
	}
	if reason != "" && l.home != "" {
		if homeLogs, ok := openHomeFallbackLogs(l.home); ok {
			fmt.Fprintf(os.Stderr,
				"qompack: cannot write the project log for %s (%s); degradation for this session is "+
					"being recorded under %s instead\n", l.root, reason, homeLogs.dir)
			l.real, l.closer = homeLogs.log, homeLogs.closer
			return l.real
		}
	}
	l.real = logging.Nop()
	return l.real
}

// homeFallbackSink is the user-level logger openHomeFallbackLogs returns, with the directory it
// opened so the stderr line can name it.
type homeFallbackSink struct {
	log    logging.Logger
	closer io.Closer
	dir    string
}

// openHomeFallbackLogs opens a logger on <home>/.qompack/logs, creating it.
//
// Unlike the project sink this one DOES create its directory — logging.New's own MkdirAll does the
// work — because it is the fallback: requiring it to already exist would make it unavailable
// exactly when it is needed. <home>/.qompack is a permitted write location (§3.3,
// test/guards/writeset_test.go), and nothing here can fail the hook.
func openHomeFallbackLogs(home string) (homeFallbackSink, bool) {
	dir := filepath.Join(paths.Global(home), "logs")
	log, closer, err := logging.New(dir, logging.Warn)
	if err != nil {
		return homeFallbackSink{}, false
	}
	return homeFallbackSink{log: log, closer: closer, dir: dir}, true
}

// closeSink releases whatever sink this logger materialized, and is a no-op for one that never did.
// Only in-process callers need it; see the closer field.
func (l *hookLogger) closeSink() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closer != nil {
		_ = l.closer.Close()
		l.closer = nil
	}
}

func (l *hookLogger) With(kv ...any) logging.Logger {
	return &hookLogger{root: l.root, home: l.home, kv: append(append([]any(nil), l.kv...), kv...)}
}

func (l *hookLogger) Debug(string, ...any) {}
func (l *hookLogger) Info(string, ...any)  {}

func (l *hookLogger) Warn(msg string, kv ...any) {
	l.materialize().Warn(msg, append(append([]any(nil), l.kv...), kv...)...)
}

func (l *hookLogger) Error(msg string, kv ...any) {
	l.materialize().Error(msg, append(append([]any(nil), l.kv...), kv...)...)
}

func (l *hookLogger) Loud(msg string, kv ...any) {
	l.materialize().Loud(msg, append(append([]any(nil), l.kv...), kv...)...)
}

// hookSendDeadlineFloor is the minimum deadline doHook ever hands to Client.Send. A fire-and-forget
// op's own deadline is derived from the hot-path state record's AckDeadlineMs field; a
// hand-crafted, corrupt, or otherwise zero-valued state.bin must never let that reach Send as
// literally 0, which is indistinguishable from "already timed out" (fix round 1, Minor M-6).
// //nomagic:allow a floor against a zero-valued state record, not a config default (§6.1). It was
// config.Defaults()'s own AckDeadlineMs when it was written; SP20-D1's measured re-budget raised
// that to 17/53/45 ms by platform (internal/config/deadlines.go), so 8 ms now sits strictly below
// every shipped value and this floor can only ever bind on a record carrying no deadline at all.
const hookSendDeadlineFloor = 8 * time.Millisecond

// hookConnectDeadlineFloor is the minimum dial budget doHook ever gives a non-hot-path op
// (session-start, checkpoint, flush) — as opposed to any ipc.Op.HotPath() op (observe.tool,
// observe.prompt, observe.stop). observe.prompt also carries its own fixed spec.deadline
// (promptReplyDeadline), so "carries a fixed deadline" was never the test that selects a widened op
// — see hookConnectDeadline; observe.prompt
// keeps State.ConnectDeadlineMs's tight, tuned-for-a-warm-daemon budget untouched, same as
// observe.tool and observe.stop.
//
// Root-caused during fix round 1 by bisecting against the pre-fix-round commit: State.
// ConnectDeadlineMs (config.Defaults() ships a budget sized for the hot path's own
// already-established connection cadence — 5ms, or 25ms on Windows, where it also has to clear the
// named-pipe dial's own retry quantum; see internal/config/deadlines.go) is too tight for
// session-start's very first dial, which lands moments after preSend's own daemon.EnsureRunning
// call — a daemon that has JUST finished spawning or has JUST accepted-and-closed EnsureRunning's
// own liveness probe is not guaranteed to have its next ConnectNamedPipe/accept re-posted inside a
// hot-path budget on a loaded host; go-winio's DialPipe then legitimately times out and doHook
// falls back to spooling a request the daemon was, in fact, a few milliseconds away from
// accepting. Before I-1's fix (internal/ipc/client.go), this
// never surfaced: NewClientWithOptions unconditionally re-read state.bin for itself whenever
// ProjectRoot was set, and that incidental extra disk round trip happened to burn just enough wall
// clock between EnsureRunning's return and the real dial to dodge the race in practice — an
// accident of the very double-read I-1 correctly removed, not a real guarantee. session-start and
// checkpoint carry their own generous, manifest-derived spec.deadline (10s/15s) precisely because
// they are not expected to complete in hot-path time (§2.4), and the flush — fire-and-forget since
// C1.15 — is the last hook of a session, sent to a daemon that may be busy ending another. Widening
// only their dial budget — never observe.tool/prompt/stop's, which stays exactly what state.bin says —
// costs nothing in the steady-state (a genuinely absent daemon still fails the dial almost
// instantly: a nonexistent named pipe/socket is a fast connection-refused, not a wait for this
// timeout to elapse) while giving a freshly-spawned or momentarily-busy daemon real room to answer.
// //nomagic:allow: a deliberately-derived floor, not a config default (§6.1) — see the comment above.
const hookConnectDeadlineFloor = 250 * time.Millisecond

// hookConnectDeadline computes the ConnectDeadline doHook hands to ipc.ClientOptions: State.
// ConnectDeadlineMs as-is for every ipc.Op.HotPath() op (observe.tool, observe.prompt,
// observe.stop), regardless of whether the op also happens to carry its own fixed spec.deadline
// the way observe.prompt does (promptReplyDeadline); every op off the hot path (session-start,
// checkpoint, flush) gets widened to at least hookConnectDeadlineFloor. Fix round 2, Important N-2:
// the original predicate (spec.deadline > 0 alone) missed that observe.prompt also carries a fixed
// spec.deadline despite being squarely on the hot path, and would have widened its connect budget
// along with it. The widening no longer asks for a fixed spec.deadline at all: since C1.15 the flush
// is fire-and-forget and carries none, yet it is exactly the op whose dial must not lose a race with a
// momentarily busy daemon at the end of a session. Factored out of doHook so it is directly
// unit-testable without a real client or connection.
func hookConnectDeadline(spec hookSpec, st ipc.State) time.Duration {
	connectDeadline := time.Duration(st.ConnectDeadlineMs) * time.Millisecond
	if !spec.op.HotPath() && connectDeadline < hookConnectDeadlineFloor {
		connectDeadline = hookConnectDeadlineFloor
	}
	return connectDeadline
}

// hookSpec is what differs between the six thin hook clients (task-6-spec.md's wiring table).
type hookSpec struct {
	op    ipc.Op
	reply bool
	// deadline is the fixed reply deadline for a Reply request. Zero means "derive it from the
	// hot-path state record's AckDeadlineMs" — observe.tool's and observe.stop's row in the wiring
	// table, both fire-and-forget ops with no fixed deadline of their own.
	deadline time.Duration
	// ackDeadline bounds writing a fire-and-forget request and waiting for its ACK. Zero means the
	// hot-path state record's AckDeadlineMs, which is every observe op's; only the flush, off the hot
	// path and with the host's SessionEnd budget to spend, sets its own (flushAckDeadline).
	ackDeadline time.Duration
	// hostTimeout is the host's manifest timeout for this hook. When it is set, doHook shares it out
	// (hookBudget, V6 close-out D17b): preSend is told the instant it has to be done by, and the
	// reply wait is cut to what is left when the steps before the dial ran over, so the whole
	// invocation ends inside the timeout. Only session-start sets it; zero keeps the fixed deadlines.
	hostTimeout time.Duration
	// minReply is the least reply wait preSend's find/start step leaves when it borrows the reply
	// wait's idle time (hookBudget.borrowBy, V6 close-out D21): session-start's is D9's compact bound
	// (daemon.CompactAnswerBudget), so a compaction's answer at that bound is still heard. Zero lends
	// nothing, and it has no effect without a hostTimeout.
	minReply time.Duration
	// preSend runs once, after the project root/state are final and before the client is
	// constructed. Only session-start uses it, to call daemon.EnsureRunningUntil (§2.4: session-start
	// is the designated daemon starter, off the hot path, with a generous hook timeout). self is
	// env.Self threaded through explicitly (see cli.Env.Self's own doc comment) rather than read
	// from a package-level seam. st is the same 32-byte state record doHook already read, so
	// preSend can honour runtime.daemon.enabled without a second disk read (fix round 2, FR-6). b is
	// the hook's budget: returning by preSendBy keeps the full reply deadline, the find/start step may
	// wait for a daemon until borrowBy, and latestPoll is when any wait must end; all are zero when
	// the hook has no hostTimeout.
	preSend func(root, self string, st ipc.State, clk core.Clock, b hookBudget)
}

// doHook stamps TS, resolves state, honors ModeOff and reads bounded raw input. The initial
// process/env root selects privacy policy before an Event is derived. A permitted destination
// root can apply an additional policy before any spool, blob, client or daemon-start operation.
// This corrects the historical state-only path: state.bin has no complete privacy policy.
// The added configuration/policy cost needs quiet hot-path measurement under SP-20.
//
// The shipped panic-recovery framework (dispatch.go's Dispatch -> recover.go's runGuarded) already
// gives every Cmd{Hook:true} the §12.3 "any hook panic -> recovered, logged, exit 0 with empty
// output" guarantee, so this function does not duplicate it: a panic here (including the
// panic:hook / panic:client fault sites) propagates straight up to runGuarded's own recover().
func doHook(spec hookSpec) func(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
	return func(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
		clk := env.Clock
		if clk == nil {
			clk = core.SystemClock()
		}
		ts := clk.Now().UnixMilli() // FIRST statement — the B-A origin.
		// The same instant on the wall clock the host's timeout runs on, whatever clk is (D17b).
		began := time.Now()

		root := resolveProjectRoot(env, nil)
		// D18: a session whose project root is the home directory records nothing. The check comes
		// before anything reads from that root, writes to it — a fault site, a quiet log, a config
		// violation list — or starts a daemon for it, because its .qompack is the user-global layer.
		if isHomeRoot(env, root) {
			return writeRefusedHookOutput(spec.op, args, out)
		}
		faultCorruptStateIfNeeded(root)
		st := ipc.ReadState(root, config.Defaults())
		if st.Mode == contract.ModeOff {
			return hookio.WriteOutput(out, hookio.Empty())
		}

		stdin := faultStdin(env.Stdin)
		in, rerr := readHookCapture(stdin, hookCaptureLimit(int64(st.MaxPayloadBytes)))
		if rerr != nil && !hookRefusalIsRecordable(root, in) {
			// Either nothing arrived, so there is nothing for a policy to classify, or the
			// allocation bound refused a delivery in a project with no .qompack store to record
			// the refusal in. Refuse here, before any configuration is loaded, exactly as the hard
			// allocation bound requires — and, in the second case, without creating the store a
			// project that has not opted in never asked for.
			logQuiet(root, rerr, clk)
			return hookio.WriteOutput(out, hookio.Empty())
		}

		maybePanicHook() // Inject after bounded input, before admission or event persistence.
		in.Raw = faultInflateHookCapture(in.Raw)
		capture, ev, cfg, err := admitHookCapture(env, root, in)
		if rerr != nil {
			// A short read that did deliver bytes still reaches admission, so the delivery is
			// classified (FidelityPartial) instead of discarded unrecorded. The read's own error is
			// what an operator needs to see, so that is the one logged.
			err = rerr
		}
		if err != nil {
			logQuiet(root, err, clk)
		}
		// An admission that refused this delivery for observation still CLASSIFIED what arrived:
		// a bounded prefix cleared by the operator's own rules, the fidelity that describes it, and
		// the capture error naming why it was refused. A refusal is not an absence, so that record
		// travels to the store exactly like an admitted one — carrying no Event, because none was
		// derived and none may be invented from a payload this process could not admit whole.
		//
		// This branch used to return here for every non-OK outcome, which is what made an
		// over-budget delivery vanish: a project that lowered runtime.hotPath.maxPayloadBytes lost
		// every hook delivery above it with no spool line, no observation and no trace that the
		// host had delivered anything at all (SP-20 invariant 4, and invariant 1's requirement that
		// a missing original stay explicitly unavailable rather than simply absent).
		//
		// Only an admission that produced NO record stops here: runtime mode off, or a configuration
		// or privacy policy that never loaded. A payload the hard allocation bound refused no longer
		// reaches this point unrecorded — it is classified from the prefix and the observed size that
		// bound kept, or it never got past hookRefusalIsRecordable because the project has no store to
		// record it in. Publishing a zero capture would assert a classification this process never
		// made, and — with no compiled policy — could not have made safely.
		degraded := capture.Outcome != core.OutcomeOK
		if degraded && !capture.Recorded() {
			return hookio.WriteOutput(out, hookio.Empty())
		}

		// Only the admitted payload can select another destination, and only an admitted payload
		// has an Event to select one WITH. A degraded delivery keeps the trusted initial root: its
		// prefix is not a document whose cwd may be read, and re-admitting a prefix under a second
		// policy is precisely the second pass that must never restore removed content.
		if !degraded {
			if r2 := resolveProjectRoot(env, &ev); r2 != root {
				// The payload's own root is refused exactly like the process's (D18), before its state
				// is read or its configuration is loaded and reported under it.
				if isHomeRoot(env, r2) {
					return writeRefusedHookOutput(spec.op, args, out)
				}
				root = r2
				faultCorruptStateIfNeeded(root)
				st = ipc.ReadState(root, config.Defaults())
				if st.Mode == contract.ModeOff {
					return hookio.WriteOutput(out, hookio.Empty())
				}
				prior := capture
				capture, ev, cfg, err = admitHookCapture(env, root, hookInput{Raw: prior.Bytes})
				if err != nil || capture.Outcome != core.OutcomeOK {
					logQuiet(root, err, clk)
					return hookio.WriteOutput(out, hookio.Empty())
				}
				capture = composeHookCapture(prior, capture)
			}
			if resolveProjectRoot(env, &ev) != root {
				return hookio.WriteOutput(out, hookio.Empty())
			}
		}

		// A project root that does not exist on disk must never conjure a store: paths.Resolve's
		// own last resort is "the payload cwd itself", so a malformed payload's cwd resolves
		// cleanly to a path that simply isn't a real directory. Without this check, the spool
		// append below would MkdirAll a .qompack/spool tree at that location — a store for a
		// project that does not exist.
		if !isDir(root) {
			return hookio.WriteOutput(out, hookio.Empty())
		}
		st.DaemonEnabled = st.DaemonEnabled && cfg.Runtime.Daemon.Enabled
		st.SpoolOnBreach = st.SpoolOnBreach && cfg.Runtime.HotPath.SpoolOnBreach

		// The nonce is minted here, once, before any transport attempt: it labels this host
		// invocation, not the attempt that happens to carry it. Everything below — the daemon
		// connect, the spool fallback that catches a failed connect, and any later retry of the
		// spooled line — reuses this one Request value, so the same delivery keeps one nonce while
		// a second invocation of the same bytes gets its own. A minting failure leaves it empty
		// rather than substituting a value that would read as an identity it cannot support.
		nonce, nerr := ipc.NewDeliveryNonce()
		if nerr != nil {
			logQuiet(root, nerr, clk)
		}
		// The permitted capture now travels beside the derived Event, so a host key this build does
		// not name is no longer dropped at the encode boundary. Object/frontier publication of it
		// remains a separately gated M1 requirement.
		// A degraded delivery travels as its capture record and nothing else: hookio derived no
		// Event, and a zero-valued one on the wire would read as a host that sent empty fields
		// rather than as a payload that was never admitted — the synthetic default the capture
		// contract exists to prevent. Request.Event is omitempty and the daemon's own resolveEvent
		// already treats an absent Event as absent, so the record stays honest end to end.
		var evp *hookio.Event
		if !degraded {
			evp = &ev
		}
		req := ipc.WithCapture(ipc.Request{
			Op: spec.op, Session: ev.SessionID, TS: core.UnixMilli(ts),
			Reply: spec.reply, Event: evp, Raw: rawExtras(spec.op, args, ev), Nonce: nonce,
		}, capture)

		spoolDir := paths.Of(root).Spool
		faultLockSpoolDirIfNeeded(spoolDir)
		// The spool gets THIS hook's logger and metrics, not a Nop, and that is finding F-2: an
		// ordinary spool write failure is handled entirely inside spool.Append, which drops, counts
		// and Louds and then returns nil, so the client's own drop branch never runs. Built through
		// ipc.NewSpool the whole announcement went to logging.Nop() and a read-only .qompack left no
		// durable evidence of the degradation anywhere.
		hookLog := newHookLoggerWithHome(root, homeDir(env))
		// A hook process exits within milliseconds of its last log line, so production never needs
		// this; an IN-PROCESS caller does, and test/guards runs all six hooks in process on purpose.
		// Windows will not delete a directory whose files are still open.
		if hl, ok := hookLog.(*hookLogger); ok {
			defer hl.closeSink()
		}
		hookMetrics := newHookMetrics(clk)
		sp := ipc.NewSpoolWithObs(spoolDir, hookLog, hookMetrics)
		sp = wrapFaultSpool(sp)
		defer func() {
			if cl, ok := sp.(interface{ Close() error }); ok {
				_ = cl.Close()
			}
		}()

		connectDeadline := hookConnectDeadline(spec, st)
		budget := newHookBudget(began, spec.hostTimeout, spec.deadline, connectDeadline, spec.minReply)
		if spec.preSend != nil {
			spec.preSend(root, env.Self, st, clk, budget)
		}

		addr, aerr := ipc.Resolve(root)
		if aerr != nil {
			spoolUnsent(sp, req, hookLog)
			logQuiet(root, aerr, clk)
			return hookio.WriteOutput(out, hookio.Empty())
		}
		addr = faultDaemonDownAddr(addr) // daemon-down: the resolved address itself must be unreachable.

		spawn := daemon.SpawnDetached
		if _, on := faultActive(faultDaemonDown); on {
			spawn = noopSpawn
		}

		c := ipc.NewClientWithOptions(addr, sp, hookLog, hookMetrics, ipc.ClientOptions{
			// State is already this hook's own single 32-byte read (st, above); NewClientWithOptions
			// trusts a non-zero State outright and never re-reads it from disk even though
			// ProjectRoot is also set (internal/ipc/client.go — fix round 1, Important I-1).
			// ProjectRoot still matters on its own: lazySpawn's lock file and externalize()'s blob
			// directory both need it independently of where State came from.
			ProjectRoot: root, State: st, Self: env.Self, Clock: clk, Spawn: spawn,
			ConnectDeadline: connectDeadline, AckDeadline: spec.ackDeadline,
		})
		c = wrapFaultClient(c)
		defer func() { _ = c.Close() }()

		deadline := spec.deadline
		if deadline <= 0 {
			deadline = time.Duration(st.AckDeadlineMs) * time.Millisecond
		}
		if deadline <= 0 {
			deadline = hookSendDeadlineFloor
		}

		// With deadline <= 0 (D17b), the steps before the dial used the whole budget, so no answer
		// could be waited for before the host's timeout. The request is spooled as a start that missed
		// its deadline is, without dialling a daemon that would then answer into nothing, and the
		// answer below is the one an unanswered start gets.
		var resp ipc.Response
		if deadline = budget.replyDeadline(time.Now(), deadline, connectDeadline); deadline > 0 {
			resp, _ = c.Send(ctx, req, deadline)
		} else if spoolUnsent(sp, req, hookLog) {
			hookLog.Warn("hook: no time left to wait for the daemon's answer; the request was spooled",
				"op", string(spec.op), "overrun_ms", -deadline.Milliseconds())
		}
		respOut := hookio.Empty()
		if resp.Output != nil {
			respOut = *resp.Output
		} else if compactUnanswered(spec, ev, degraded, st, cfg, resp) {
			// C1.16: the daemon never answered a compact SessionStart (it did not reply within the
			// deadline, or could not be reached), so no rehydration is coming. Say so, rather than
			// answer {} and leave the model with a compacted context and no word of what it lost.
			respOut = hookio.SessionStartOutput(daemon.CompactDeferredNote(ev.SessionID, daemon.DeferredNoAnswer))
		}
		// The daemon's reply is an internal protocol; the host's schema is not. One field the host
		// does not accept for this event makes it reject the whole response, and for PreCompact it
		// then replays the rejection into the post-compaction context (C1.12). So the reply is
		// reduced to what the host accepts and acts on for the event it is running, whatever the
		// daemon — this build's or a still-resident older one — happened to send.
		event := hookEvent(spec.op, args)
		conformed := hookio.ConformOutput(event, respOut)
		// The host accepts a field over its per-field cap but hands Claude only a 2,000-character
		// preview of it, so a large rehydration is cut without any check failing. Say so, loudly,
		// with sizes only (a log is not a content store, §7.4).
		for _, over := range hookio.HostCapOverruns(conformed) {
			hookLog.Loud("hook output exceeds the host's per-field cap; Claude sees only a preview of it",
				"event", event, "field", over.Field, "chars", over.Chars,
				"cap", hookio.HostFieldMaxChars, "preview", hookio.HostFieldPreviewChars)
		}
		return hookio.WriteOutput(out, conformed)
	}
}

// compactUnanswered reports whether a session-start invocation is a compact SessionStart that got
// no answer from the daemon at all — Send spooled it (a missed reply deadline, a failed dial) or
// dropped it — in a project where the daemon would have been allowed to rehydrate: the daemon is
// enabled, the mode this client read may act (§12.1: degraded-passive and off inject nothing, a
// note included), and the reinjection kill switch (SP-19) is on. Only then is the deferred note
// honest: in every other case {} is the designed answer, not a lost one.
func compactUnanswered(spec hookSpec, ev hookio.Event, degraded bool, st ipc.State, cfg config.Config, resp ipc.Response) bool {
	switch {
	case spec.op != ipc.OpSessionStart, degraded, ev.Source != compactSource:
		return false
	case resp.OK || resp.Output != nil:
		return false // the daemon answered; what it said is the answer
	case !st.DaemonEnabled, !st.Mode.MayAct():
		return false
	case cfg.Runtime.Mode == configModeOff || cfg.Runtime.Mode == configModePassive:
		return false
	case !cfg.Runtime.Migration.Reinjection.SessionStartCompact:
		return false
	}
	return true
}

// unsentSpoolRefusedMsg is the Loud a hook writes when a request it spools without sending is
// refused by the spool, and so is lost (spoolUnsent).
const unsentSpoolRefusedMsg = "hook: the request could not be spooled and is lost"

// spoolUnsent appends req to the hook's spool on the branches where doHook sends nothing at all —
// no address resolved, or no time left to wait for an answer — and reports whether it was spooled.
// The client's own spool path counts and Louds a refused append (ipc's appendToSpool); a refusal
// here was dropped silently, and the no-time-left branch then logged the request as spooled
// (w5-coldstart review nit). spool.Append already drops, counts and Louds an ordinary write failure
// and returns nil; the error it does return — the frame-size refusal, or a fault site's — means the
// request is lost, so that is Louded here.
func spoolUnsent(sp ipc.SpoolWriter, req ipc.Request, log logging.Logger) bool {
	if err := sp.Append(req); err != nil {
		log.Loud(unsentSpoolRefusedMsg, "op", string(req.Op), "err", err)
		return false
	}
	return true
}

// compactSource is the SessionStart source a host sends after compacting a conversation.
const compactSource = "compact"

// configModeOff and configModePassive are the runtime.mode values under which Qompack acts on
// nothing (contract.Monitor.RunAll forces the mode from them).
const (
	configModeOff     = "off"
	configModePassive = "passive"
)

// hookEvent is the host event a hook invocation answers: the event internal/pluginmanifest
// registers that subcommand for. `observe stop` serves two events, told apart by the --subagent
// flag the SubagentStop entry passes. An op that is not a hook maps to "", which ConformOutput
// answers with the empty response.
//
// TestHookOutput_EveryEntryPointConformsToTheHostSchema drives every manifest entry point through
// this mapping, so a hook registered for a new event, or re-routed, fails there rather than having
// its output silently emptied.
func hookEvent(op ipc.Op, args []string) string {
	switch op {
	case ipc.OpObserveTool:
		return hookio.EventPostToolUse
	case ipc.OpObservePrompt:
		return hookio.EventUserPromptSubmit
	case ipc.OpObserveStop:
		if hasFlag(args, "--subagent") {
			return hookio.EventSubagentStop
		}
		return hookio.EventStop
	case ipc.OpSessionStart:
		return hookio.EventSessionStart
	case ipc.OpCheckpoint:
		return hookio.EventPreCompact
	case ipc.OpFlush:
		return hookio.EventSessionEnd
	}
	return ""
}

// resolveProjectRoot is §3.3's resolution order, called twice per hook (task-6-spec.md): once
// before stdin is read (ev == nil, so the process's own cwd stands in for a payload cwd), and once
// after (ev's own CWD). paths.Resolve already implements "QOMPACK_PROJECT_ROOT env override, else
// walk the given cwd up to the nearest .git, else that cwd itself" — this wrapper only supplies the
// cwd paths.Resolve needs and falls back to that same cwd on the one error paths.Resolve can return
// (an empty cwd with no env override, which cannot happen once currentDir() has succeeded).
func resolveProjectRoot(env Env, ev *hookio.Event) string {
	cwd := ""
	if ev != nil {
		cwd = ev.CWD
	}
	if cwd == "" {
		if wd, err := currentDir(); err == nil {
			cwd = wd
		}
	}
	root, err := paths.Resolve(env.Getenv, cwd)
	if err != nil {
		return cwd
	}
	return root
}

// quietLogLine is logQuiet's one JSONL record shape.
type quietLogLine struct {
	TS  string `json:"ts"`
	Err string `json:"err"`
}

// logQuiet appends err to .qompack/logs/hook-quiet-YYYYMMDD.jsonl, but ONLY if that directory
// already exists — a hook must never create directories on a failing project's error path (a
// payload naming a root that does not exist, or one whose .qompack/logs has never been created by
// anything else, must stay exactly as untouched as it was before this call). clk stamps the
// record and names the day file, per the repo-wide "every time-taking component takes a
// core.Clock" rule (fix round 1, Minor M-3) — never time.Now() directly.
func logQuiet(root string, err error, clk core.Clock) {
	if err == nil || root == "" {
		return
	}
	logs := paths.Of(root).Logs
	if !isDir(logs) {
		return
	}
	if clk == nil {
		clk = core.SystemClock()
	}
	now := clk.Now().UTC()
	p := filepath.Join(logs, "hook-quiet-"+now.Format("20060102")+".jsonl")
	_ = paths.AppendJSONL(p, quietLogLine{TS: now.Format(time.RFC3339Nano), Err: err.Error()})
}

// subagentExtras is `observe stop --subagent`'s Request.Raw shape. Agent is omitempty so an
// unnamed subagent marshals to exactly {"subagent":true} — byte-for-byte what shipped before the
// name was resolved here, which is what keeps an already-installed daemon's decodeSubagent working
// against a newer hook client and vice versa.
type subagentExtras struct {
	Subagent bool   `json:"subagent"`
	Agent    string `json:"agent,omitempty"`
}

// subagentNameKeys are the payload keys a subagent's name may arrive under, in priority order.
// They are read from hookio.Event.Extra, which is where ReadEvent files every top-level key
// Event's own struct tags do not claim.
var subagentNameKeys = []string{"subagent_type", "agent_name", "agent"}

// subagentName resolves the agent's name out of ev.Extra, or "" when no key holds one.
//
// It MUST run here, in the hook-client process. hookio.Event.Extra is tagged `json:"-"`, so
// ReadEvent fills it on this side of the IPC boundary and ipc.EncodeRequest then drops it: a
// daemon-side reader of e.Extra sees an empty map in production, and every subagent capture would
// be named "subagent". Request.Raw is the field that does cross, so the resolution happens where
// the data still exists and the ANSWER is what travels.
//
// A key whose value is not a non-empty JSON string — a number, an object, an empty string, or the
// key simply being absent — falls through to the next candidate rather than winning with a
// nonsense name.
func subagentName(ev hookio.Event) string {
	for _, k := range subagentNameKeys {
		var name string
		if err := json.Unmarshal(ev.Extra[k], &name); err == nil && name != "" {
			return name
		}
	}
	return ""
}

// rawExtras builds Request.Raw for the two hook subcommands that carry dispatch data beyond the
// parsed Event: {"subagent":true,"agent":"<name>"} for `observe stop --subagent` (the flag the
// plugin manifest's SubagentStop entry actually passes — internal/pluginmanifest's hookSpecs), and
// {"trigger":"<manual|auto>"} for checkpoint. checkpoint's trigger comes from the EVENT's own
// Trigger field, not a CLI flag: the manifest invokes `checkpoint` with no flags at all (PreCompact
// carries its manual/auto discriminator in the hook payload itself, per hookio.Event.Trigger's own
// doc comment), so args has nothing to read for it. Every other subcommand carries nil.
func rawExtras(op ipc.Op, args []string, ev hookio.Event) json.RawMessage {
	switch op {
	case ipc.OpObserveStop:
		if hasFlag(args, "--subagent") {
			b, _ := json.Marshal(subagentExtras{Subagent: true, Agent: subagentName(ev)})
			return b
		}
	case ipc.OpCheckpoint:
		if ev.Trigger != "" {
			b, _ := json.Marshal(map[string]string{"trigger": ev.Trigger})
			return b
		}
	}
	return nil
}

// hasFlag reports whether flag appears verbatim among args.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
