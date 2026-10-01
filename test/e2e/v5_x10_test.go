// V5 §4.10 — the grammar thrash warning is warning-only, delivered once per rule through the prompt
// hook and nowhere durable, and no warning Qompack emits can become the next warning's input.
//
// Two real producers exist for this row on this tree, and the row drives both:
//
//   - The SHIPPED warning path: internal/grammar's Sequitur, folded per tool use by
//     internal/observer, whose Thrash() is queued on PostToolUse and drained into UserPromptSubmit's
//     additionalContext — and only in contract.ModeFull (§12.1). Arms 1 and 2 drive it through the
//     real binary's hook subcommands against the daemon composed by x10v5StartRig.
//   - SP-15's state-aware detector, grammar.Detector, which owns the criterion's "progress-aware
//     dedup" and "self-generated warnings cannot feed back" bounds. It ships behind
//     runtime.selection.loopWarningsEnabled (default false) and NO production composition root
//     constructs it on this tree, so arm 3 records the switch as DISABLED through the real config
//     surface and then exercises the detector in process, with its warnings rendered through the
//     same FormatWarning the observer injects.
//
// Composition note, stated rather than hidden: internal/cli's runDaemon never sets
// daemon.Options.Grammar, so the installed binary's observer has a nil grammar and its checkpoint
// wiring builds a second, empty one. x10v5StartRig is v4StartRig with that one field set — the
// seam daemon.Options exposes and WireObserver already honours — and shares the instance with the
// checkpoint SourceSet exactly as wireCheckpointSources would if Options.Grammar were non-nil. The
// gap itself is recorded in plans/sdd/V5-VERIFY/x10-disposition.md, not papered over here.
//
// What the historical text expected and this row does NOT assert, with the reason:
//
//   - "repeated 11×": Sequitur's Thrash reports a rule's REFERENCE count, and the observer queues
//     a rule the first time that count exceeds thrashMinUses (3). The warning therefore says
//     "repeated 4×", however long the loop ran — unless the reply that should have carried it was
//     lost (late or deferred on a slow disk, which only a run declaring a non-reference disk or
//     co-load may answer with recovery), in which case the rule is re-armed and warns afresh at
//     its grown count once the loop occurs again (x10v5DeliverOrRecover). A pure periodic cycle
//     never gets there at all — hierarchical folding keeps every rule at ≤ 3 references — which is
//     why the replayed loop separates its cycles with distinct one-off tools.
//   - "the cycle appears in the checkpoint's action history": checkpoint.SourceSet carries a
//     Grammar but nothing reads it — Compressed() has no production consumer — so the checkpoint
//     arm asserts only that a warning lands on NO durable surface, which is what warning-only means.
//   - "status shows the thrash": no status field carries warnings; status carries the §12.1 mode,
//     which is what the degraded arm reads from it.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// The session identities: the full-mode loop, the degraded-passive loop, and the later session
// whose clean start restores the degraded daemon.
const (
	x10v5Session         = core.SessionID("sess-e2e-v5-x10")
	x10v5DegradedSession = core.SessionID("sess-e2e-v5-x10-degraded")
	x10v5RestoreSession  = core.SessionID("sess-e2e-v5-x10-restore")
)

// x10v5Cycles is the historical text's eleven Read→Edit→Bash cycles.
const x10v5Cycles = 11

// x10v5ExtraCycles is the tail replayed AFTER the warning: the rule stays above threshold for the
// rest of the session and must not be reported a second time.
const x10v5ExtraCycles = 3

// x10v5EchoPrompts is how many times the delivered warning is quoted back as a user prompt.
const x10v5EchoPrompts = 3

// x10v5FirstThrashUses is the multiplicity the shipped warning reports. internal/observer queues a
// rule the first time Sequitur's reference count EXCEEDS thrashMinUses (3), and the queued Rule
// value is the one rendered, so the count is 4 on the first prompt after the fourth cycle — never
// the number of cycles the loop eventually ran. Verified against the real grammar before this row
// was written; see the file comment.
const x10v5FirstThrashUses = 4

// The frozen pieces of the one-line warning grammar.FormatWarning renders
// (internal/grammar/formatwarning.go): its prefix, the expansion of the rule this stream induces,
// and the advice internal/observer appends.
const (
	x10v5WarningPrefix = "[qompack] possible loop:"
	x10v5LoopExpansion = "FileRead→FileEdit→Bash"
	x10v5WarningAdvice = "consider a different approach"
)

// x10v5LoopFile is the path the loop keeps reading and editing.
const x10v5LoopFile = "src/auth.go"

// x10v5SealedSeq is the sequence the first checkpoint of a fresh project carries.
const x10v5SealedSeq = core.CheckpointSeq(1)

// x10v5Spacers are the host tool names placed between cycles. Each normalizes to a DISTINCT display
// symbol that is not FileRead, FileEdit, Bash or the prompt symbol, so no digram involving a spacer
// ever repeats: that is what leaves the Read→Edit→Bash rule referenced from the top-level sequence
// once per cycle instead of being folded into a rule-of-rules with ≤ 3 references. Eleven cycles
// need ten, and the three extra cycles need two more.
var x10v5Spacers = []string{
	"Grep", "Glob", "WebSearch", "WebFetch", "Task", "TodoWrite", "PowerShell", "Write", "LS", "KillShell",
	"ExitPlanMode", "SlashCommand",
}

// x10v5StartRig composes the daemon exactly as v4StartRig does, with ONE addition: a real
// grammar.New() is placed on Options.Grammar before WireObserver, so the observer folds the action
// stream into it, and the same instance is handed to the checkpoint SourceSet — which is the
// composition internal/cli's wireCheckpointSources already describes for a non-nil Options.Grammar.
func x10v5StartRig(t *testing.T, p *testutil.Project) *v4Rig {
	t.Helper()
	return x10v5StartRigWith(t, p, nil)
}

// x10v5ReplyHold holds the daemon's prompt REPLY path — the reply-only ObservePrompt call, never the
// worker's capture — while armed: the call signals entered and waits for release before the observer
// sees it, exactly as a session lock held by a slow disk's durable capture would keep it waiting.
type x10v5ReplyHold struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func newX10v5ReplyHold() *x10v5ReplyHold {
	return &x10v5ReplyHold{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

// x10v5StartRigWith is x10v5StartRig with an optional reply hold bound in front of the observer.
func x10v5StartRigWith(t *testing.T, p *testutil.Project, hold *x10v5ReplyHold) *v4Rig {
	t.Helper()

	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	gram := grammar.New()
	opts.Grammar = gram

	obsv, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	if hold != nil {
		// Bound after WireObserver's own bind, so it wraps the observer's seam.
		opts.Bind(func(s *daemon.Services) {
			inner := s.ObservePrompt
			s.ObservePrompt = func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
				if observer.PromptReplyOnly(ctx) && hold.armed.CompareAndSwap(true, false) {
					hold.entered <- struct{}{}
					<-hold.release
				}
				return inner(ctx, e)
			}
		})
	}
	t.Cleanup(func() { _ = opts.Store.Close() })
	t.Cleanup(func() {
		if led := opts.LedgerHandle(); led != nil {
			_ = led.Close()
		}
	})

	pinStore, err := pins.OpenWith(p.Root, p.Log, opts.Metrics, opts.Clock)
	require.NoError(t, err)

	w, err := checkpoint.OpenWriter(p.Root, p.Cfg, p.Log, opts.Metrics, opts.Clock)
	require.NoError(t, err)

	toks := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)

	sources := func() (checkpoint.SourceSet, error) {
		var segs store.SegmentLog
		if opts.Store != nil {
			segs = opts.Store.Segments()
		}
		src := checkpoint.SourceSet{
			Store:    opts.Store,
			Segments: segs,
			Ledger:   opts.LedgerHandle(),
			LedgerFn: opts.LedgerHandle,
			Pins:     pinStore,
			Graph:    opts.Graph,
			Grammar:  gram,
			Tokens:   toks,
		}
		if _, valErr := src.Resolve(); valErr != nil {
			return src, fmt.Errorf("%w: %w", valErr, core.ErrDegraded)
		}
		return src, nil
	}

	opts.Checkpoints = w
	snapshot, _ := sources()
	daemon.BindCheckpoint(&opts, p.Cfg, w, snapshot, daemon.WithSourceSupplier(sources))

	d, err := daemon.New(opts)
	require.NoError(t, err)
	daemon.RegisterObserverIdleWork(d, obsv)
	daemon.WireCheckpoint(d, p.Cfg, w, snapshot, daemon.WithSourceSupplier(sources))

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(runCtx) }()
	t.Cleanup(func() {
		if stopErr := d.Stop(context.Background()); stopErr != nil {
			t.Errorf("e2e: stopping the V5 x10 daemon: %v", stopErr)
		}
		cancelRun()
		if runErr := <-runDone; runErr != nil {
			t.Errorf("e2e: the V5 x10 daemon's Run returned: %v", runErr)
		}
	})
	e2eWaitDaemonUp(t, p.Root)

	return &v4Rig{D: d, W: w, Segs: opts.Store.Segments(), Src: sources, Opts: &opts, P: p, Bin: Build(t)}
}

// x10v5Feed drives one session's hook events through the real binary IN ORDER: after every event
// it waits for the event's own index record before sending the next. The daemon dispatches from a
// worker pool, and the action grammar is a sequence, so without this wait two consecutive tool
// uses could reach the grammar in either order and the induced rules would depend on scheduling.
// The wait is exact because observer.onToolUse holds the session lock from the index write through
// the grammar append: once event N's record is visible, event N+1's handler cannot fold before N's
// fold has finished.
//
// The wait counts PRIMARY records — the lines that carry a "tool" field — and not every line of
// index/tool_use.jsonl: re-reading the loop's file makes the observer mark the earlier read
// SUPERSEDED (§8.1 item 3), and store.MarkSuperseded appends that flip as its own tool-less line, so
// the raw line count runs ahead of the events and a wait on it would return before the newest
// event had landed.
type x10v5Feed struct {
	t    *testing.T
	r    *v4Rig
	sess core.SessionID
	env  map[string]string
	// indexed is how many primary index records this feed has waited for so far — one per tool use
	// and one per captured prompt.
	indexed int
	nextID  int
}

// waitPrimary drives the daemon's drain until the index holds at least want primary records, the
// way v4Rig.WaitIndexed does for raw lines.
func (f *x10v5Feed) waitPrimary(want int) {
	f.t.Helper()
	ctx := context.Background()
	ticker := time.NewTicker(obsProcessTick)
	defer ticker.Stop()
	timeout := time.NewTimer(obsProcessBound)
	defer timeout.Stop()

	// ONE Drain, unconditionally, before the index is read for the first time. The loop below
	// evaluates its condition FIRST, so a wait whose record the async ingest had already published
	// drives no Drain at all while a slower one drives several — and Drain is not side-effect-free:
	// drainer.Drain calls saveState unconditionally (internal/daemon/drain.go), which always goes
	// through paths.WriteAtomic, so every drain rewrites state/drain.json and mints a transient
	// tmp/wa-* staging file. Without this drive, whether those artifacts exist is decided purely by
	// timing — and this feed waits once per event fed, so the divergence multiplies by the length of
	// the feed rather than being a single coin flip. v4Rig.WaitIndexed carries the same drive for the
	// same reason, and §4.13's write-set comparison is where it was actually caught. Do not
	// "simplify" it back into the loop.
	_, _ = f.r.D.Drain(ctx)

	for len(x10v5PrimaryRecords(f.t, f.r.P.Root)) < want {
		_, _ = f.r.D.Drain(ctx)
		if len(x10v5PrimaryRecords(f.t, f.r.P.Root)) >= want {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			require.FailNowf(f.t, "the replayed event never reached the index",
				"index/tool_use.jsonl never held %d primary records within %s (have %v); LOUD: %v",
				want, obsProcessBound, x10v5PrimaryRecords(f.t, f.r.P.Root), loudLines(f.t, f.r.P.Root))
		}
	}
}

func x10v5NewFeed(t *testing.T, r *v4Rig, sess core.SessionID) *x10v5Feed {
	t.Helper()
	return &x10v5Feed{t: t, r: r, sess: sess, env: e2eEnv(r.P)}
}

// tool sends one PostToolUse for host tool `name` and waits for it to be indexed.
func (f *x10v5Feed) tool(name string, input, response map[string]any) {
	f.t.Helper()
	f.nextID++
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse", "session_id": f.sess, "cwd": f.r.P.Root,
		"tool_name": name, "tool_use_id": fmt.Sprintf("toolu_v5x10_%03d", f.nextID),
		"tool_input": input, "tool_response": response,
	})
	require.NoError(f.t, err)
	obsRunHook(f.t, f.r.Bin, []string{"observe", "tool"}, b, f.env)
	f.indexed++
	f.waitPrimary(f.indexed)
}

// prompt sends one UserPromptSubmit, waits for its verbatim capture to be indexed, and returns the
// hook's decoded stdout — the only channel a thrash warning has.
func (f *x10v5Feed) prompt(text string) hookio.Output {
	f.t.Helper()
	out, _ := f.promptSpooled(text)
	return out
}

// promptSpooled is prompt, also reporting whether the hook DEFERRED the prompt: appended it to its
// client spool because no reply reached it in time (ipc client.awaitReply) or it could not connect.
// The spool is read before the feed's wait drives a Drain, which consumes it; the daemon's own
// watcher passes a spool only once it has stood unchanged for a whole check interval.
func (f *x10v5Feed) promptSpooled(text string) (hookio.Output, bool) {
	f.t.Helper()
	before := x10v5SpooledPrompts(f.t, f.r.P.Root, f.sess, text)
	out := f.r.Hook(f.t, []string{"observe", "prompt"}, obsPromptPayload(f.t, f.r.P.Root, f.sess, text))
	spooled := x10v5SpooledPrompts(f.t, f.r.P.Root, f.sess, text) > before
	f.indexed++
	f.waitPrimary(f.indexed)
	return out, spooled
}

// x10v5SpooledPrompts counts the client spool lines that carry an observe.prompt of sess with text.
func x10v5SpooledPrompts(t *testing.T, root string, sess core.SessionID, text string) int {
	t.Helper()
	n := 0
	for _, reqs := range x3v5ClientSpoolLines(t, root) {
		for _, req := range reqs {
			if req.Op == ipc.OpObservePrompt && req.Event != nil && req.Event.SessionID == sess && req.Event.Prompt == text {
				n++
			}
		}
	}
	return n
}

// cycleAfter replays one more Read→Edit→Bash cycle of the loop, after the spacer tool `spacer`.
func (f *x10v5Feed) cycleAfter(spacer string) {
	f.t.Helper()
	f.tool(spacer, map[string]any{"pattern": "errExpired", "file_path": "notes.md", "command": "Get-Date", "query": "errExpired"},
		map[string]any{"content": "no result"})
	f.cycles(1, nil)
}

// cycles replays n Read→Edit→Bash cycles against the same file with the same failing build, each
// separated from the next by one spacer tool; len(spacers) must be at least n-1.
func (f *x10v5Feed) cycles(n int, spacers []string) {
	f.t.Helper()
	require.GreaterOrEqual(f.t, len(spacers), n-1, "every gap between cycles needs its own spacer")
	for i := range n {
		f.tool("Read", map[string]any{"file_path": x10v5LoopFile},
			map[string]any{"content": "package auth\n\nfunc refresh() error { return errExpired }\n"})
		f.tool("Edit", map[string]any{"file_path": x10v5LoopFile, "old_string": "errExpired", "new_string": "errExpired"},
			map[string]any{"content": "The file " + x10v5LoopFile + " has been updated."})
		f.tool("Bash", map[string]any{"command": "go build ./..."},
			map[string]any{"exit_code": 1, "stdout": "", "stderr": "./src/auth.go:3: undefined: errExpired"})
		if i < n-1 {
			f.tool(spacers[i], map[string]any{"pattern": "errExpired", "file_path": "notes.md", "command": "Get-Date", "query": "errExpired"},
				map[string]any{"content": "no result"})
		}
	}
}

// x10v5Status runs `qompack status --json` through the real binary and returns the daemon mode it
// reports plus the raw report, which is the status surface this row's identifier names.
func x10v5Status(t *testing.T, r *v4Rig) (mode string, report []byte) {
	t.Helper()
	stdout, stderr, code := Run(t, r.Bin, []string{"status", "--json"}, nil, e2eEnv(r.P))
	require.Equal(t, 0, code, "`qompack status --json` always exits 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	env, err := commands.DecodeEnvelope(stdout)
	require.NoError(t, err, "stdout must be one command envelope:\n%s", stdout)
	require.True(t, env.OK, "status must not report an error: %s", stdout)
	var rep commands.StatusReport
	require.NoError(t, json.Unmarshal(env.Data, &rep), "envelope data must be a StatusReport:\n%s", env.Data)
	require.NotNil(t, rep.Snapshot, "the daemon must have been reachable for a snapshot: %s", env.Data)
	return rep.Snapshot.Mode, env.Data
}

// x10v5RequireLoopWarning asserts ac is exactly one shipped thrash warning for the replayed loop.
func x10v5RequireLoopWarning(t *testing.T, ac string) {
	t.Helper()
	require.True(t, strings.HasPrefix(ac, x10v5WarningPrefix),
		"additionalContext must begin with FormatWarning's frozen prefix: %q", ac)
	require.NotContains(t, ac, "\n", "one thrashing rule must render as exactly one line: %q", ac)
	require.Contains(t, ac, x10v5LoopExpansion, "the warning must name the induced rule's expansion: %q", ac)
	require.Contains(t, ac, fmt.Sprintf("repeated %d×", x10v5FirstThrashUses),
		"the rule is queued the first time its reference count exceeds thrashMinUses, so the "+
			"rendered multiplicity is that count and not the number of cycles replayed: %q", ac)
	require.True(t, strings.HasSuffix(ac, x10v5WarningAdvice), "the warning ends with the observer's advice: %q", ac)
}

// The daemon counters the late-reply branch reads, respelled because both are unexported:
// internal/daemon's counterPromptReplyLate (a reply that went out empty because its deadline ran
// out) and internal/observer's counterThrashUndelivered (a warning a reply drained but could not
// deliver, re-armed for the loop's next occurrence).
const (
	x10v5CounterReplyLate   = "l0_prompt_reply_late"
	x10v5CounterUndelivered = "observer.thrash_undelivered"
)

// x10v5RecoveryPrompt is the prompt each recovery attempt sends (x10v5DeliverOrRecover).
const x10v5RecoveryPrompt = "still red after one more try"

// x10v5RecoverySpacers are the spacers of the recovery cycles, one per attempt: host tool names the
// display table does not claim, so each passes through as a symbol that occurs nowhere else in the
// stream, exactly like x10v5Spacers. Their number is the number of attempts: a disk on which three
// consecutive prompt replies all miss their deadline cannot show a warning at all, and the row says
// so rather than looping.
var x10v5RecoverySpacers = []string{"ListMcpResourcesTool", "ReadMcpResourceTool", "AskUserQuestion"}

// x10v5Delivery is what x10v5DeliverOrRecover found: the hook output that carried the warning, the
// warning, the prompt it rode on, and whether a lost reply re-armed its rule on the way.
type x10v5Delivery struct {
	out     hookio.Output
	ac      string
	with    string
	rearmed bool
}

// x10v5Counters is what the two late-branch counters read at one instant.
type x10v5Counters struct{ late, undelivered int64 }

func x10v5ReadCounters(r *v4Rig) x10v5Counters {
	return x10v5Counters{
		late:        r.Opts.Metrics.Counter(x10v5CounterReplyLate).Value(),
		undelivered: r.Opts.Metrics.Counter(x10v5CounterUndelivered).Value(),
	}
}

// x10v5StepSummaryEnv is the file GitHub Actions builds a job's summary page from. `go test` in
// package-list mode drops a passing package's test output (ci.yml's test-e2e runs `go test
// ./test/e2e` without -v), so t.Log alone would leave no trace of a recovery branch in a green
// hosted run; x10v5ReportBranch also appends the branch to this file.
const x10v5StepSummaryEnv = "GITHUB_STEP_SUMMARY"

// x10v5ReportBranch logs a late-or-deferred branch the row took and, when a declaration the run made
// licensed it (declared, x10v5RecoveryLicence) rather than the arm forcing it through a seam, appends
// the same line to the GitHub Actions job summary, so a green hosted run still says that it proved
// recovery and not first-prompt delivery. Outside GitHub Actions the variable is unset and the log
// line is all there is. A summary GitHub names but that cannot be appended to fails the row: the
// branch is never silent.
func x10v5ReportBranch(t *testing.T, declared bool, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	t.Log(msg)
	path := os.Getenv(x10v5StepSummaryEnv)
	if !declared || path == "" {
		return
	}
	sf, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err, "X10's recovery branch must reach the job summary %s names", x10v5StepSummaryEnv)
	_, werr := fmt.Fprintf(sf, "- X10 `%s`: %s\n", t.Name(), msg)
	cerr := sf.Close()
	require.NoError(t, werr, "X10's recovery branch must reach the job summary %s names", x10v5StepSummaryEnv)
	require.NoError(t, cerr, "X10's recovery branch must reach the job summary %s names", x10v5StepSummaryEnv)
}

// x10v5RecoveryLicence says why a late or deferred prompt reply may be answered with recovery instead
// of failing the row, and whether the reason is a declaration the run made (declared) rather than the
// arm forcing the lateness itself (forced: x10v5LateReplyArm's reply hold). It is empty when nothing
// licenses it: on a reference disk, judged alone, the first prompt after the loop must carry the
// warning. The two declarations are the ones X11 reports its fsync-bound rows under:
// obs.NonReferenceDisk (Q1, D53(e); honoured only on GitHub Actions, and ci.yml's test-e2e sets it)
// and obs.UnderCoload (ADR 0010; never set where test/e2e runs alone, locally or hosted).
func x10v5RecoveryLicence(forced bool) (reason string, declared bool) {
	switch {
	case forced:
		return "forced by the arm's reply hold (x10v5ReplyHold)", false
	case obs.NonReferenceDisk():
		return "non-reference disk declared via " + obs.NonReferenceDiskEnv + " (Q1, D53(e))", true
	case obs.UnderCoload():
		return "co-loaded run declared via " + obs.UnderColoadEnv + " (ADR 0010)", true
	}
	return "", false
}

// x10v5DeliverOrRecover takes the hook output of the first prompt that must carry the warning (out,
// for the prompt sentWith, sent after the counters read before; spooled says whether its hook
// deferred it) and returns the delivered warning. must is what the row requires of that prompt;
// forced says the arm itself made that prompt's reply late (x10v5LateReplyArm).
//
// On a reference disk that prompt carries it, and a late or deferred reply there fails the row, as
// it did before the recovery branch existed. A prompt reply that is LATE (the daemon counted
// l0_prompt_reply_late) or DEFERRED (the hook found no reply in time and spooled the prompt) cannot
// carry anything; a hosted runner's disk does that (Q1, QOMPACK_NONREFERENCE_DISK), and where the run
// has declared so (x10v5RecoveryLicence) the row proves recovery instead of delivery: a warning the
// host never received does not count as delivered, so one more loop cycle must bring it on the next
// prompt. Each such branch is logged and, on GitHub Actions, written to the job summary
// (x10v5ReportBranch), never silent, and bounded by x10v5RecoverySpacers. A prompt whose reply was on
// time and still carried nothing fails exactly as before, under every licence. forced covers only the
// prompt the arm held: a recovery prompt that is late again needs a declaration like any other.
//
// A late reply's own observer call may still be waiting for the session lock when the hook gives up,
// and it is that call that re-arms the rule, so the row waits for the re-arm before it replays the
// cycle — otherwise the cycle could fold before the rule is re-armed and the row would be racing the
// product.
func x10v5DeliverOrRecover(t *testing.T, r *v4Rig, f *x10v5Feed, before x10v5Counters, sentWith string,
	out hookio.Output, spooled, forced bool, must string,
) x10v5Delivery {
	t.Helper()
	m := r.Opts.Metrics
	d := x10v5Delivery{with: sentWith}
	lateBefore, rearmed := before.late, before.undelivered
	late := m.Counter(x10v5CounterReplyLate).Value()
	declaredAny := false
	for attempt := 0; out.HookSpecificOutput == nil; attempt++ {
		wasLate := late > lateBefore
		require.True(t, wasLate || spooled,
			"%s: the reply to %q was neither late (%s %d -> %d) nor deferred to the hook's client spool, so a "+
				"reply that was on time carried nothing", must, d.with, x10v5CounterReplyLate, lateBefore, late)
		licence, declared := x10v5RecoveryLicence(forced && attempt == 0)
		require.NotEmpty(t, licence,
			"%s: a reference disk must deliver the warning on the first prompt after the loop, but the reply "+
				"to %q was late (%s %d -> %d) or deferred to the hook's client spool (%v). Only a run that "+
				"declares a non-reference disk (%s, on GitHub Actions) or co-load (%s) may answer that with "+
				"recovery", must, d.with, x10v5CounterReplyLate, lateBefore, late, spooled,
			obs.NonReferenceDiskEnv, obs.UnderColoadEnv)
		require.Less(t, attempt, len(x10v5RecoverySpacers),
			"every prompt reply was late or deferred for %d recovery cycles in a row: this disk delivers no warning at all",
			len(x10v5RecoverySpacers))
		if wasLate {
			require.Eventually(t, func() bool { return m.Counter(x10v5CounterUndelivered).Value() > rearmed },
				obsProcessBound, obsProcessTick,
				"a late reply's warning must be re-armed (%s), not consumed", x10v5CounterUndelivered)
		}
		if spooled {
			// A deferred reply is settled only when a drain consumes the hook's spooled copy: that is
			// where a claimed-but-undelivered warning is re-armed (w16f-rearm), and where a prompt no
			// live reply answered is captured with its warning still queued. Until then the copy can
			// sit behind its unacknowledged live twin, and a recovery cycle replayed meanwhile would
			// fold into the re-arm floor and waste the attempt, so wait for the drain to consume it.
			require.Eventually(t, func() bool {
				_, _ = f.r.D.Drain(context.Background())
				return x10v5SpooledPrompts(t, r.P.Root, f.sess, d.with) == 0
			}, obsProcessBound, obsProcessTick,
				"the hook's spooled copy of %q must be settled by a drain before the recovery cycle", d.with)
		}
		declaredAny = declaredAny || declared
		x10v5ReportBranch(t, declared, "LATE OR DEFERRED PROMPT REPLY (%s): the reply to %q carried no "+
			"warning; late %s %d -> %d, hook deferred it to its client spool: %v, %s now %d. Recovery attempt "+
			"%d of %d replays one more loop cycle and requires the next prompt to carry the warning.",
			licence, d.with, x10v5CounterReplyLate, lateBefore, late, spooled,
			x10v5CounterUndelivered, m.Counter(x10v5CounterUndelivered).Value(), attempt+1, len(x10v5RecoverySpacers))
		lateBefore = late
		rearmed = m.Counter(x10v5CounterUndelivered).Value()
		f.cycleAfter(x10v5RecoverySpacers[attempt])
		d.with = x10v5RecoveryPrompt
		out, spooled = f.promptSpooled(x10v5RecoveryPrompt)
		late = m.Counter(x10v5CounterReplyLate).Value()
	}
	require.Equal(t, "UserPromptSubmit", out.HookSpecificOutput.HookEventName,
		"additionalContext reaches the transcript only under the hook's own event name")
	d.out, d.ac = out, out.HookSpecificOutput.AdditionalContext
	d.rearmed = m.Counter(x10v5CounterUndelivered).Value() > before.undelivered
	if d.with != sentWith {
		x10v5ReportBranch(t, declaredAny, "RECOVERED: %q carried the warning after the lost reply (re-armed: %v): %q",
			d.with, d.rearmed, d.ac)
	}
	if d.rearmed {
		x10v5RequireRecoveredLoopWarning(t, d.ac)
	} else {
		x10v5RequireLoopWarning(t, d.ac)
	}
	return d
}

// x10v5RequireRecoveredLoopWarning is x10v5RequireLoopWarning for a warning re-armed after a lost
// reply. Every property is the same but the multiplicity: the rule is queued again only once
// Sequitur reports it referenced more often than when the lost reply re-armed it, which was at
// least the x10v5FirstThrashUses it was first queued at, so it renders strictly more.
func x10v5RequireRecoveredLoopWarning(t *testing.T, ac string) {
	t.Helper()
	require.True(t, strings.HasPrefix(ac, x10v5WarningPrefix),
		"additionalContext must begin with FormatWarning's frozen prefix: %q", ac)
	require.NotContains(t, ac, "\n", "one thrashing rule must render as exactly one line: %q", ac)
	require.Contains(t, ac, x10v5LoopExpansion, "the warning must name the induced rule's expansion: %q", ac)
	require.True(t, strings.HasSuffix(ac, x10v5WarningAdvice), "the warning ends with the observer's advice: %q", ac)
	const repeated = "repeated "
	require.Contains(t, ac, repeated, "the warning states its multiplicity: %q", ac)
	var uses int
	_, err := fmt.Sscanf(ac[strings.Index(ac, repeated)+len(repeated):], "%d×", &uses)
	require.NoError(t, err, "the warning states its multiplicity: %q", ac)
	require.Greater(t, uses, x10v5FirstThrashUses,
		"a re-armed rule warns afresh only once the loop has occurred again, at its grown multiplicity: %q", ac)
}

// x10v5DeliveredWith is the prompt the loop warning is delivered with: the first prompt after the
// loop crossed the threshold.
const x10v5DeliveredWith = "still red — try the exact same fix again"

// x10v5RequireWarningOnlyAsEchoedPrompts holds the sealed checkpoint raw to what this row protects:
// Qompack never puts its own warning on a durable surface.
//
// Criterion change (D53(a), after D45/D46): this row used to require that the checkpoint carry no
// warning text at all. The arm above echoes the delivered warning back as the user's prompt, and
// since D45/D46 every user prompt reaches user_intent.evolution verbatim; a user's words are never
// stripped or rewritten, even when they quote a warning. So the warning text may appear, and only,
// as an evolution entry that is EXACTLY an echoed prompt (ac, which the arm sent as the prompt
// text). It may appear in no other field, inside no other entry, and in particular not attached to
// the prompt it was delivered with (deliveredWith: x10v5DeliveredWith, or x10v5RecoveryPrompt when a
// late reply made the row prove recovery), whose entry must be that prompt's own words and nothing
// more. Every string in the artifact is checked, so a warning that Qompack wrote into
// any field still fails here.
func x10v5RequireWarningOnlyAsEchoedPrompts(t *testing.T, raw []byte, ac, deliveredWith string) {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc), "the sealed checkpoint must be JSON:\n%s", raw)

	intent, ok := doc["user_intent"].(map[string]any)
	require.True(t, ok, "the sealed checkpoint carries user_intent:\n%s", raw)
	evolution, ok := intent["evolution"].([]any)
	require.True(t, ok, "user_intent.evolution is a list:\n%s", raw)
	echoed := map[int]bool{}
	sawDeliveredWith := false
	for i, e := range evolution {
		s, _ := e.(string)
		switch {
		case s == ac:
			echoed[i] = true
		case s == deliveredWith:
			sawDeliveredWith = true
		}
	}
	require.True(t, sawDeliveredWith,
		"the prompt the warning was delivered with is in evolution as the user's own words, unchanged: %q", evolution)
	require.NotEmpty(t, echoed, "fixture: the echoed prompts reach evolution verbatim (D45/D46): %q", evolution)

	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, c := range v {
				walk(path+"."+k, c)
			}
		case []any:
			for i, c := range v {
				if path == ".user_intent.evolution" && echoed[i] {
					continue // an echoed prompt, verbatim: the user's words, not Qompack's warning
				}
				walk(fmt.Sprintf("%s[%d]", path, i), c)
			}
		case string:
			require.NotContains(t, v, x10v5WarningPrefix,
				"a warning is transient by design: Qompack must not write it into %s of the sealed checkpoint", path)
		}
	}
	walk("", doc)
}

// x10v5EliminationLines counts the non-empty lines of records/eliminations.jsonl, or 0 before the
// ledger has written one. A thrash warning must never mint one: it is advisory, not an elimination.
func x10v5EliminationLines(t *testing.T, root string) int {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Records, "eliminations.jsonl")))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return countNonEmptyLines(string(b))
}

// x10v5PrimaryRecords renders every PRIMARY index record — a tool use or a captured prompt, the
// lines with a "tool" field — as "tool:id", so a count mismatch names the records rather than the
// bytes. Supersession flips (op "supersede", no tool) are skipped; see x10v5Feed.
func x10v5PrimaryRecords(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, line := range obsToolUseLines(root) {
		var rec struct {
			ID   string `json:"id"`
			Tool string `json:"tool"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "index line must be JSON: %s", line)
		if rec.Tool == "" {
			continue
		}
		out = append(out, rec.Tool+":"+rec.ID)
	}
	return out
}

// x10v5PromptRecords counts the index records filed under the UserPromptSubmit pseudo-tool.
func x10v5PromptRecords(root string) int {
	n := 0
	for _, line := range obsToolUseLines(root) {
		if strings.Contains(line, `"UserPromptSubmit"`) {
			n++
		}
	}
	return n
}

// TestV5_ThrashWarningVisibleInStatusAndCheckpoint is V5-VERIFY §4.10.
//
// Arm 2 is the negative control, through a real switch: the daemon starts in
// contract.ModeDegradedPassive (persisted in state/contract.json, the same seam v3_x08 and the SP-10
// degraded row use), the identical loop is replayed, and the prompt hook must emit NOTHING — while
// the grammar has still recorded it, which is proven without touching the grammar: two clean
// contract runs restore ModeFull and the very next prompt delivers the warning that was queued
// while degraded. Arm 3 carries its own switch-level control: a detector whose delivery cap is the
// in-code equivalent of runtime.selection.loopWarningsEnabled=false delivers nothing on the same
// stream that made the default detector warn.
func TestV5_ThrashWarningVisibleInStatusAndCheckpoint(t *testing.T) {
	t.Run("sequitur_warning_is_warning_only_and_once_per_rule", x10v5FullModeArm)
	t.Run("degraded_passive_records_the_loop_and_says_nothing_until_restored", x10v5DegradedArm)
	t.Run("state_aware_detector_is_progress_aware_and_cannot_feed_itself", x10v5DetectorArm)
	t.Run("a_late_reply_does_not_count_as_delivered_and_the_loop_warns_afresh", x10v5LateReplyArm)
}

func x10v5FullModeArm(t *testing.T) {
	p := v4Project(t)
	r := x10v5StartRig(t, p)
	f := x10v5NewFeed(t, r, x10v5Session)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x10v5Session), f.env)
	mode, _ := x10v5Status(t, r)
	require.Equal(t, contract.ModeFull.String(), mode, "a fresh project starts in ModeFull")

	out := f.prompt("make TestRefresh pass without touching the token store")
	require.Nil(t, out.HookSpecificOutput, "nothing has looped yet, so the first prompt gets no context")

	// ── The loop, through the real binary ────────────────────────────────────────────────────────
	f.cycles(x10v5Cycles, x10v5Spacers[:x10v5Cycles-1])

	// On a reference disk the first prompt after the loop carries the warning. Where its reply was
	// observably late or deferred and the run declared a non-reference disk or co-load, the row
	// proves recovery instead (x10v5DeliverOrRecover); undeclared, a late reply fails the row.
	counters := x10v5ReadCounters(r)
	out, spooled := f.promptSpooled(x10v5DeliveredWith)
	delivered := x10v5DeliverOrRecover(t, r, f, counters, x10v5DeliveredWith, out, spooled, false,
		"the prompt after a thrashing loop must carry the warning")
	out = delivered.out
	ac := delivered.ac

	// ── Warning-only: additionalContext is the ONLY channel, and nothing durable changes ─────────
	require.Empty(t, out.HookSpecificOutput.CustomInstructions, "a prompt-hook warning never rides customInstructions")
	require.Nil(t, out.Continue, "a warning must not steer the host's continue flag")
	require.Nil(t, out.SuppressOutput, "a warning must not suppress anything")
	require.Empty(t, out.SystemMessage, "a warning must not become a systemMessage")
	require.Zero(t, x10v5EliminationLines(t, p.Root), "a thrash warning must never mint an elimination record")
	mode, report := x10v5Status(t, r)
	require.Equal(t, contract.ModeFull.String(), mode, "warning does not change the contract mode")
	require.NotContains(t, string(report), x10v5WarningPrefix,
		"status carries the mode, not warnings: the warning has exactly one surface")

	// ── Once per rule ────────────────────────────────────────────────────────────────────────────
	out = f.prompt("ok, same thing once more")
	require.Nil(t, out.HookSpecificOutput, "the same rule must not be reported on the next prompt")
	f.cycles(x10v5ExtraCycles, x10v5Spacers[x10v5Cycles-1:])
	out = f.prompt("and again")
	require.Nil(t, out.HookSpecificOutput,
		"the rule stays above threshold for the rest of the session and must still be reported only once")

	// ── The feedback edge: the delivered warning, quoted back as the user's own words ────────────
	// A host injects additionalContext into the model's context; the shortest loop back into
	// Qompack is a prompt that carries the warning's text. The observer captures each such prompt
	// verbatim and folds only the prompt SYMBOL into the grammar, so the text can induce nothing.
	before := x10v5PromptRecords(p.Root)
	for range x10v5EchoPrompts {
		out = f.prompt(ac)
		require.Nil(t, out.HookSpecificOutput, "a prompt quoting the warning must not produce another warning")
	}
	require.Equal(t, before+x10v5EchoPrompts, x10v5PromptRecords(p.Root),
		"every echoed prompt is still captured verbatim — suppression is about the grammar, not recording")

	// ── The checkpoint: the warning lands on no durable surface ──────────────────────────────────
	// Criterion change (C1.18): the route's reply used to carry a focus instruction this row held
	// free of the warning. The instruction is retired — no host accepts one — so the reply is the
	// empty object, and the sealed checkpoint is the durable surface the warning must stay off.
	cpRequireNoInstructionReply(t, r.PreCompactReply(t, x10v5Session))
	require.Equal(t, []string{"0001.json"}, cpCheckpointArtifacts(t, p.Root), "exactly one artifact must be sealed")
	x4RequireManifestVerifies(t, p.Root)
	raw, err := os.ReadFile(paths.Long(paths.CheckpointPath(paths.Of(p.Root), x10v5SealedSeq)))
	require.NoError(t, err)
	x10v5RequireWarningOnlyAsEchoedPrompts(t, raw, ac, delivered.with)
	require.Zero(t, x10v5EliminationLines(t, p.Root),
		"opening the ledger on the first PreCompact must not have turned the warning into a record")
	// testutil's append-only probe seeds checkpoints/0001.json itself, so it belongs to the arm
	// that seals no checkpoint (arm 2), not here.
}

// x10v5LateReplyArm drives arm 1's late branch on purpose, through a deterministic seam rather than
// a slow disk: the reply path of the first prompt after the loop is held (x10v5ReplyHold) until the
// hook process has exited, so that reply goes out empty and is counted late. The warning it would
// have carried must not count as delivered: it is not replayed by itself, and one more loop cycle
// brings it, re-armed, on the next prompt — exactly once.
func x10v5LateReplyArm(t *testing.T) {
	p := v4Project(t)
	hold := newX10v5ReplyHold()
	r := x10v5StartRigWith(t, p, hold)
	f := x10v5NewFeed(t, r, x10v5Session)
	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x10v5Session), f.env)

	require.Nil(t, f.prompt("make TestRefresh pass without touching the token store").HookSpecificOutput)
	f.cycles(x10v5Cycles, x10v5Spacers[:x10v5Cycles-1])

	counters := x10v5ReadCounters(r)
	hold.armed.Store(true)
	out, spooled := f.promptSpooled(x10v5DeliveredWith)
	select {
	case <-hold.entered:
	case <-time.After(obsProcessBound):
		require.FailNow(t, "the held reply path was never called for the prompt after the loop")
	}
	// The hook has exited, its reply long gone; only now does the observer see the reply call.
	close(hold.release)
	require.Nil(t, out.HookSpecificOutput, "a held reply goes out empty")
	require.Greater(t, x10v5ReadCounters(r).late, counters.late, "and the daemon counts it late")

	delivered := x10v5DeliverOrRecover(t, r, f, counters, x10v5DeliveredWith, out, spooled, true,
		"the held reply must be counted late or deferred")
	require.True(t, delivered.rearmed, "the warning the late reply drained must have been re-armed, not consumed")
	require.Equal(t, x10v5RecoveryPrompt, delivered.with, "and it arrives on the prompt after one more loop cycle")
	require.Empty(t, delivered.out.HookSpecificOutput.CustomInstructions)
	require.Nil(t, delivered.out.Continue)
	require.Nil(t, delivered.out.SuppressOutput)
	require.Empty(t, delivered.out.SystemMessage)

	// Once delivered, once per rule, however long the loop runs.
	require.Nil(t, f.prompt("ok, same thing once more").HookSpecificOutput,
		"the same rule must not be reported on the next prompt")
	f.cycles(x10v5ExtraCycles, x10v5Spacers[x10v5Cycles-1:])
	require.Nil(t, f.prompt("and again").HookSpecificOutput,
		"a re-armed rule, once delivered, is reported only once")
	require.Zero(t, x10v5EliminationLines(t, p.Root), "a thrash warning must never mint an elimination record")
}

func x10v5DegradedArm(t *testing.T) {
	p := v4Project(t)

	// ── Force ModeDegradedPassive before the daemon exists ───────────────────────────────────────
	// The daemon constructs its monitor over state/contract.json inside New and §12.1 requires a
	// degradation to survive into the next session, so degrading through a monitor on the same
	// state path IS the switch (v3_x08, checkpoint_degraded_test).
	statePath := filepath.Join(paths.Of(p.Root).State, "contract.json")
	forced := []contract.Result{{
		ID: contract.ID("v5x10.forced"), OK: false, Severity: contract.SevCritical,
		Expected: "host contract holds", Observed: "forced by V5 §4.10's negative control",
		TS: core.NowMilli(p.Clock),
	}}
	preMon := contract.NewMonitor(p.Log, nil, statePath)
	preMon.Degrade("v5 x10 negative control", forced)
	require.Equal(t, contract.ModeDegradedPassive, preMon.Mode())

	r := x10v5StartRig(t, p)
	require.Equal(t, contract.ModeDegradedPassive.String(), e2eStatus(t, p.Root).Mode,
		"the daemon must adopt the persisted degradation at New")
	mode, _ := x10v5Status(t, r)
	require.Equal(t, contract.ModeDegradedPassive.String(), mode,
		"`qompack status` is where a user sees the degraded banner; it must report the degraded mode")

	f := x10v5NewFeed(t, r, x10v5DegradedSession)
	// ONE session-start: every SessionStart runs the monitor's RunAll and two consecutive clean
	// runs restore ModeFull, so this is clean run 1 — the restore is arm's second half, on purpose.
	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x10v5DegradedSession), f.env)
	require.Equal(t, contract.ModeDegradedPassive.String(), e2eStatus(t, p.Root).Mode,
		"one clean contract run must not restore yet")

	// ── The identical loop; the prompt hook must say nothing ─────────────────────────────────────
	f.cycles(x10v5Cycles, x10v5Spacers[:x10v5Cycles-1])
	out := f.prompt("still red — try the exact same fix again")
	require.Nil(t, out.HookSpecificOutput,
		"NEGATIVE CONTROL: in ModeDegradedPassive the same loop that warned in arm 1 must produce "+
			"no additionalContext at all (§12.1: record, do not act)")
	require.Len(t, x10v5PrimaryRecords(t, p.Root), f.indexed,
		"every tool use and the prompt must still have been recorded while degraded, exactly once")
	require.Equal(t, contract.ModeDegradedPassive.String(), e2eStatus(t, p.Root).Mode,
		"the mode must still be degraded at the instant the prompt hook ran, or the silence above proves nothing")

	// ── Restore, then the queued warning surfaces: the grammar recorded the loop all along ───────
	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x10v5RestoreSession), f.env)
	require.Equal(t, contract.ModeFull.String(), e2eStatus(t, p.Root).Mode,
		"the second consecutive clean contract run must restore ModeFull")
	mode, _ = x10v5Status(t, r)
	require.Equal(t, contract.ModeFull.String(), mode, "and the status surface must show it")

	// On a reference disk the first full-mode prompt carries it; where that reply was observably late
	// or deferred and the run declared a non-reference disk or co-load, the row proves recovery
	// instead (x10v5DeliverOrRecover). Either way the warning
	// exists only because the grammar folded the loop while the daemon was degraded.
	const restoredWith = "and now?"
	counters := x10v5ReadCounters(r)
	out, spooled := f.promptSpooled(restoredWith)
	x10v5DeliverOrRecover(t, r, f, counters, restoredWith, out, spooled, false,
		"the warning queued while degraded must be delivered on the first full-mode prompt: the "+
			"grammar kept folding the loop while the daemon was forbidden to act on it")
	require.Zero(t, x10v5EliminationLines(t, p.Root), "still no elimination record")

	p.AssertAppendOnly(t)
}

// x10v5Sig is the state the in-process arm keeps repeating: same goal, same target, same action,
// same failure — the shape contract §5 calls a loop when nothing observable changes between
// occurrences.
var x10v5Sig = grammar.StateSignature{
	Goal:    "make TestRefresh pass without touching the token store",
	Target:  x10v5LoopFile,
	Action:  "Bash",
	Failure: "./src/auth.go:3: undefined: errExpired",
}

// x10v5Symbols is the rendering hint handed to every observation so the rendered warning names the
// same expansion the shipped path renders.
var x10v5Symbols = []grammar.Symbol{"FileRead", "FileEdit", "Bash"}

// x10v5Observe folds one observation of sig at turn `at` into det.
func x10v5Observe(det *grammar.Detector, sig grammar.StateSignature, at core.TurnIndex,
	progress grammar.Progress, self bool,
) (grammar.StateWarning, bool) {
	return det.Observe(grammar.Observation{
		Signature: sig, Turn: at, Progress: progress, SelfOriginated: self, Symbols: x10v5Symbols,
	})
}

// x10v5Repeat observes sig n times at turn `at` with the given progress and reports whether any of
// them delivered a warning, returning the last one that did.
func x10v5Repeat(det *grammar.Detector, sig grammar.StateSignature, at core.TurnIndex,
	progress grammar.Progress, self bool, n int,
) (grammar.StateWarning, bool) {
	var last grammar.StateWarning
	delivered := false
	for range n {
		if w, ok := x10v5Observe(det, sig, at, progress, self); ok {
			last, delivered = w, true
		}
	}
	return last, delivered
}

func x10v5DetectorArm(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)

	// ── The switch is recorded as DISABLED, through the real config surface ──────────────────────
	require.False(t, p.Cfg.Runtime.Selection.LoopWarningsEnabled,
		"the loaded project config must carry SP-15's switch off by default")
	stdout, stderr, code := Run(t, bin, []string{"config", "print", "--json"}, nil, e2eEnv(p))
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	var printed config.Config
	require.NoError(t, json.Unmarshal(stdout, &printed), "stdout:\n%s", stdout)
	require.False(t, printed.Runtime.Selection.LoopWarningsEnabled,
		"`qompack config print` must report runtime.selection.loopWarningsEnabled=false: the "+
			"state-aware detector is shipped disabled, and this row records that rather than a pass")

	det := grammar.NewDetector(grammar.DetectorConfig{})
	cfg := det.Config()
	// Every phase below sits at its own turn, and all of them inside ONE dedup window: the
	// premise is asserted at the end rather than assumed.
	var turn core.TurnIndex

	// ── Phase 1: a genuine loop warns exactly at MinRepeats, confidently ─────────────────────────
	turn++
	_, ok := x10v5Repeat(det, x10v5Sig, turn, grammar.ProgressNone, false, cfg.MinRepeats-1)
	require.False(t, ok, "fewer than MinRepeats identical states is a re-check, not a loop")
	w, ok := x10v5Observe(det, x10v5Sig, turn, grammar.ProgressNone, false)
	require.True(t, ok, "the MinRepeats-th identical, progress-free occurrence must warn")
	require.Equal(t, cfg.MinRepeats, w.Repeats)
	require.False(t, w.Uncertain, "full coverage and no progress is a confident finding")
	require.Equal(t, grammar.ProgressNone, w.Progress)
	text := grammar.FormatWarning(w.Warning)
	require.True(t, strings.HasPrefix(text, x10v5WarningPrefix), "rendered through the frozen formatter: %q", text)
	require.Contains(t, text, x10v5LoopExpansion, "%q", text)
	require.Contains(t, text, fmt.Sprintf("repeated %d×", cfg.MinRepeats), "%q", text)
	require.True(t, det.Deliverable(w, turn), "a warning is deliverable at the turn it was produced")
	require.False(t, det.Deliverable(w, w.ExpiresAt+1), "and not after ExpiresAt")

	// ── Phase 2: dedup — the same state again is collapsed, not re-warned ────────────────────────
	turn++
	_, ok = x10v5Observe(det, x10v5Sig, turn, grammar.ProgressNone, false)
	require.False(t, ok, "a repeat of an already-delivered warning inside the window must be deduplicated")
	require.Equal(t, 1, det.Stats().Deduplicated)

	// ── Phase 3: progress-aware — an observed change silences the window, and latches ───────────
	turn++
	moving := x10v5Sig
	moving.Failure = "FAIL TestRefresh: nil map assignment"
	_, ok = x10v5Repeat(det, moving, turn, grammar.ProgressNone, false, cfg.MinRepeats-1)
	require.False(t, ok)
	suppressedBefore := det.Stats().ProgressSuppressed
	_, ok = x10v5Observe(det, moving, turn, grammar.ProgressObserved, false)
	require.False(t, ok, "the occurrence that would cross the threshold observed progress: no warning")
	const afterProgress = 2 // further progress-free occurrences after the latch
	_, ok = x10v5Repeat(det, moving, turn, grammar.ProgressNone, false, afterProgress)
	require.False(t, ok, "once progress was observed the window is a working loop; it never warns again")
	require.Equal(t, suppressedBefore+1+afterProgress, det.Stats().ProgressSuppressed,
		"every occurrence from the progress observation onward is charged to the progress bound")

	// ── Phase 4: the feedback edge — Qompack's own text can never be the next warning's input ────
	turn++
	echoes := []grammar.StateSignature{
		{Goal: text, Target: x10v5Sig.Target, Action: x10v5Sig.Action, Failure: x10v5Sig.Failure},
		{Goal: x10v5Sig.Goal, Target: x10v5Sig.Target, Action: x10v5Sig.Action, Failure: text},
		{Goal: x10v5Sig.Goal, Target: x10v5Sig.Target, Action: "the assistant repeated: " + text, Failure: x10v5Sig.Failure},
		{Goal: x10v5Sig.Goal, Target: x10v5Sig.Target, Action: "mcp__qompack__recall", Failure: x10v5Sig.Failure},
		{Goal: x10v5Sig.Goal, Target: ".qompack/state/observer.json", Action: "FileRead", Failure: x10v5Sig.Failure},
		{Goal: "/qompack:status said the daemon is degraded", Target: x10v5Sig.Target, Action: x10v5Sig.Action, Failure: x10v5Sig.Failure},
	}
	statsBefore := det.Stats()
	perEcho := cfg.MinRepeats + 1
	for _, echo := range echoes {
		_, ok = x10v5Repeat(det, echo, turn, grammar.ProgressNone, false, perEcho)
		require.False(t, ok, "a state carrying Qompack's own marker must never warn: %+v", echo)
	}
	flagged := x10v5Sig
	flagged.Goal = "a goal the classifier would not recognise"
	_, ok = x10v5Repeat(det, flagged, turn, grammar.ProgressNone, true, perEcho)
	require.False(t, ok, "an observation the composition root marks SelfOriginated must never warn")
	statsAfter := det.Stats()
	require.Equal(t, (len(echoes)+1)*perEcho, statsAfter.SelfSuppressed-statsBefore.SelfSuppressed,
		"every echoed observation is attributed to the self-suppression bound")
	require.Equal(t, statsBefore.RepeatedStates, statsAfter.RepeatedStates,
		"self-originated records are not input: they must not even count as repetition")
	require.Equal(t, statsBefore.Delivered, statsAfter.Delivered)

	// ── Phase 5: warning-only — outcomes flow into counters, never back into a decision ──────────
	require.True(t, det.RecordOutcome(w.DedupKey, grammar.OutcomeFalseAlarm), "the first judgement is accepted")
	require.False(t, det.RecordOutcome(w.DedupKey, grammar.OutcomeUseful), "and cannot be revised")
	require.Equal(t, 1, det.Stats().FalseAlarms)
	for n := det.Stats().Delivered; n < cfg.MaxPerSession; n++ {
		turn++
		fresh := x10v5Sig
		fresh.Target = fmt.Sprintf("src/other_%d.go", n)
		_, ok = x10v5Repeat(det, fresh, turn, grammar.ProgressNone, false, cfg.MinRepeats)
		require.True(t, ok, "a genuinely new loop still warns after a false-alarm judgement: feedback is one-way")
	}
	require.Equal(t, cfg.MaxPerSession, det.Stats().Delivered)
	turn++
	over := x10v5Sig
	over.Target = "src/one_too_many.go"
	_, ok = x10v5Repeat(det, over, turn, grammar.ProgressNone, false, cfg.MinRepeats)
	require.False(t, ok, "the session cap is a hard bound on how loud the detector may be")
	require.Equal(t, 1, det.Stats().CapSuppressed)
	require.Less(t, turn, cfg.WindowTurns, "every phase above must have sat inside one dedup window")

	// ── Phase 6: unknown coverage renders as a question, not a finding ───────────────────────────
	unsure := grammar.NewDetector(grammar.DetectorConfig{})
	uw, ok := x10v5Repeat(unsure, x10v5Sig, 1, grammar.ProgressUnknown, false, cfg.MinRepeats)
	require.True(t, ok)
	require.True(t, uw.Uncertain, "missing observation coverage may only ever produce an Uncertain warning")
	require.Equal(t, grammar.ProgressUnknown, uw.Progress)
	require.NotEqual(t, w.Warning.Message, uw.Warning.Message,
		"the qualification must be visible in the rendered text, not only in the type")

	// ── NEGATIVE CONTROL: the in-code equivalent of the switch being off ─────────────────────────
	off := grammar.NewDetector(grammar.DetectorConfig{MaxPerSession: -1})
	_, ok = x10v5Repeat(off, x10v5Sig, 1, grammar.ProgressNone, false, cfg.MinRepeats+1)
	require.False(t, ok,
		"NEGATIVE CONTROL: with delivery disabled the stream that made the default detector warn "+
			"must deliver nothing — otherwise phase 1 could pass against a detector that always warns")
	require.Zero(t, off.Stats().Delivered)
	require.Positive(t, off.Stats().CapSuppressed, "the refusal is accounted, not silent")
}
