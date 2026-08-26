package cli

import (
	"context"
	"encoding/json"
	"io"
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
	// flushReplyDeadline is flush's reply deadline (manifest hook timeout 20s). §2.4.
	flushReplyDeadline = 15 * time.Second
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
	mu   sync.Mutex
	real logging.Logger
	kv   []any
}

// newHookLogger returns a hookLogger rooted at root. Constructing one never touches the
// filesystem; only Warn/Error/Loud does, and only on first use.
func newHookLogger(root string) logging.Logger {
	return &hookLogger{root: root}
}

// materialize returns the real logger, constructing it under lock exactly once.
func (l *hookLogger) materialize() logging.Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.real != nil {
		return l.real
	}
	if isDir(paths.Of(l.root).Logs) {
		if log, _, err := logging.New(paths.Of(l.root).Logs, logging.Warn); err == nil {
			l.real = log
			return l.real
		}
	}
	// No logs directory yet, or logging.New itself failed: fall back to Nop, which still records
	// Loud calls into the process-wide ring (internal/logging's LastLoud) even with no file sink.
	l.real = logging.Nop()
	return l.real
}

func (l *hookLogger) With(kv ...any) logging.Logger {
	return &hookLogger{root: l.root, kv: append(append([]any(nil), l.kv...), kv...)}
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
// //nomagic:allow mirrors config.Defaults()'s own AckDeadlineMs (8), not a new default (§6.1).
const hookSendDeadlineFloor = 8 * time.Millisecond

// hookConnectDeadlineFloor is the minimum dial budget doHook ever gives a non-hot-path op that
// carries its own fixed spec.deadline (session-start, checkpoint, flush) — as opposed to any
// ipc.Op.HotPath() op (observe.tool, observe.prompt, observe.stop). observe.prompt also carries
// its own fixed spec.deadline (promptReplyDeadline), so "carries a fixed deadline" alone is not
// the test that selects a widened op — see doHook's own connectDeadline comment; observe.prompt
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
// accident of the very double-read I-1 correctly removed, not a real guarantee. session-start,
// checkpoint and flush all carry their own generous, manifest-derived spec.deadline (10s/15s/15s)
// precisely because they are not expected to complete in hot-path time (§2.4), so widening only
// their dial budget — never observe.tool/prompt/stop's, which stays exactly what state.bin says —
// costs nothing in the steady-state (a genuinely absent daemon still fails the dial almost
// instantly: a nonexistent named pipe/socket is a fast connection-refused, not a wait for this
// timeout to elapse) while giving a freshly-spawned or momentarily-busy daemon real room to answer.
// //nomagic:allow: a deliberately-derived floor, not a config default (§6.1) — see the comment above.
const hookConnectDeadlineFloor = 250 * time.Millisecond

// hookConnectDeadline computes the ConnectDeadline doHook hands to ipc.ClientOptions: State.
// ConnectDeadlineMs as-is for every ipc.Op.HotPath() op (observe.tool, observe.prompt,
// observe.stop), regardless of whether the op also happens to carry its own fixed spec.deadline
// the way observe.prompt does (promptReplyDeadline); only a non-hot-path Reply op with a fixed
// spec.deadline (session-start, checkpoint, flush) gets widened to at least
// hookConnectDeadlineFloor. Fix round 2, Important N-2: the original predicate (spec.deadline > 0
// alone) missed that observe.prompt also carries a fixed spec.deadline despite being squarely on
// the hot path, and would have widened its connect budget along with it. Factored out of doHook so
// it is directly unit-testable without a real client or connection.
func hookConnectDeadline(spec hookSpec, st ipc.State) time.Duration {
	connectDeadline := time.Duration(st.ConnectDeadlineMs) * time.Millisecond
	if spec.deadline > 0 && !spec.op.HotPath() && connectDeadline < hookConnectDeadlineFloor {
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
	// preSend runs once, after the project root/state are final and before the client is
	// constructed. Only session-start uses it, to call daemon.EnsureRunning (§2.4: session-start is
	// the designated daemon starter, off the hot path, with a generous hook timeout). self is
	// env.Self threaded through explicitly (see cli.Env.Self's own doc comment) rather than read
	// from a package-level seam. st is the same 32-byte state record doHook already read, so
	// preSend can honour runtime.daemon.enabled without a second disk read (fix round 2, FR-6).
	preSend func(root, self string, st ipc.State, clk core.Clock)
}

// doHook returns the Cmd.Run body for one hook subcommand, following task-6-spec.md's skeleton
// exactly: stamp TS first, resolve the root cheaply pre-stdin, read the 32-byte state record (never
// config.Load on the hot path), honour ModeOff, read the event under the state's own payload limit,
// re-resolve against the payload if it disagrees, spool, resolve the transport address, connect,
// send, and always answer with valid JSON. Every fault site (fault.go) hooks into this same
// sequence at the point task-6-spec.md's table names.
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

		root := resolveProjectRoot(env, nil)
		faultCorruptStateIfNeeded(root)
		st := ipc.ReadState(root, config.Defaults())
		if st.Mode == contract.ModeOff {
			return hookio.WriteOutput(out, hookio.Empty())
		}

		stdin := faultStdin(env.Stdin)
		ev, _, err := hookio.ReadEvent(stdin, int64(st.MaxPayloadBytes)*4)
		if err != nil {
			logQuiet(root, err, clk)
			return hookio.WriteOutput(out, hookio.Empty())
		}

		maybePanicHook() // panic:hook: "runHook panics immediately after ReadEvent" (fault table).

		// The payload is authoritative for the project root, and it only arrives now. If it
		// disagrees with the pre-stdin guess, redo the two cheap resolutions against it.
		if r2 := resolveProjectRoot(env, &ev); r2 != root {
			root = r2
			faultCorruptStateIfNeeded(root)
			st = ipc.ReadState(root, config.Defaults())
			if st.Mode == contract.ModeOff {
				return hookio.WriteOutput(out, hookio.Empty())
			}
		}

		faultCorruptConfigIfNeeded(root) // inert on the hot path; see fault.go's own doc comment.
		faultInflateToolResponse(&ev)    // oversize

		// A project root that does not exist on disk must never conjure a store: paths.Resolve's
		// own last resort is "the payload cwd itself", so a malformed payload's cwd resolves
		// cleanly to a path that simply isn't a real directory. Without this check, the spool
		// append below would MkdirAll a .qompack/spool tree at that location — a store for a
		// project that does not exist.
		if !isDir(root) {
			return hookio.WriteOutput(out, hookio.Empty())
		}

		req := ipc.Request{
			Op: spec.op, Session: ev.SessionID, TS: core.UnixMilli(ts),
			Reply: spec.reply, Event: &ev, Raw: rawExtras(spec.op, args, ev),
		}

		spoolDir := paths.Of(root).Spool
		faultLockSpoolDirIfNeeded(spoolDir)
		sp, _ := ipc.NewSpool(spoolDir)
		sp = wrapFaultSpool(sp)
		defer func() {
			if cl, ok := sp.(interface{ Close() error }); ok {
				_ = cl.Close()
			}
		}()

		if spec.preSend != nil {
			spec.preSend(root, env.Self, st, clk)
		}

		addr, aerr := ipc.Resolve(root)
		if aerr != nil {
			_ = sp.Append(req)
			logQuiet(root, aerr, clk)
			return hookio.WriteOutput(out, hookio.Empty())
		}
		addr = faultDaemonDownAddr(addr) // daemon-down: the resolved address itself must be unreachable.

		spawn := daemon.SpawnDetached
		if _, on := faultActive(faultDaemonDown); on {
			spawn = noopSpawn
		}

		connectDeadline := hookConnectDeadline(spec, st)

		c := ipc.NewClientWithOptions(addr, sp, newHookLogger(root), newHookMetrics(clk), ipc.ClientOptions{
			// State is already this hook's own single 32-byte read (st, above); NewClientWithOptions
			// trusts a non-zero State outright and never re-reads it from disk even though
			// ProjectRoot is also set (internal/ipc/client.go — fix round 1, Important I-1).
			// ProjectRoot still matters on its own: lazySpawn's lock file and externalize()'s blob
			// directory both need it independently of where State came from.
			ProjectRoot: root, State: st, Self: env.Self, Clock: clk, Spawn: spawn,
			ConnectDeadline: connectDeadline,
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

		resp, _ := c.Send(ctx, req, deadline)
		respOut := hookio.Empty()
		if resp.Output != nil {
			respOut = *resp.Output
		}
		return hookio.WriteOutput(out, respOut)
	}
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
